package diagnostics

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type CheckItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Target    string `json:"target"`
	Status    string `json:"status"` // "ok", "warn", "error"
	LatencyMs int64  `json:"latencyMs"`
	Detail    string `json:"detail"`
}

type DiagnosticReport struct {
	ClientName     string      `json:"clientName"`
	ProxyURL       string      `json:"proxyUrl"`
	ExitIP         string      `json:"exitIp"`
	ExitRegion     string      `json:"exitRegion"`
	ExitRegionName string      `json:"exitRegionName"`
	RegionAllowed  bool        `json:"regionAllowed"`
	Items          []CheckItem `json:"items"`
	CheckedAt      string      `json:"checkedAt"`
}

var restrictedRegions = map[string]string{
	"CN": "中国大陆 (受限)",
	"HK": "中国香港 (Claude/Google 受限)",
	"MO": "中国澳门 (受限)",
	"RU": "俄罗斯 (受限)",
	"BY": "白俄罗斯 (受限)",
	"IR": "伊朗 (受限)",
	"KP": "朝鲜 (受限)",
}

var friendlyRegions = map[string]string{
	"US": "美国",
	"JP": "日本",
	"SG": "新加坡",
	"TW": "台湾",
	"KR": "韩国",
	"GB": "英国",
	"UK": "英国",
	"DE": "德国",
	"NL": "荷兰",
	"FR": "法国",
	"CA": "加拿大",
	"AU": "澳大利亚",
}

func RunDiagnostics(clientName, proxyHost string, proxyPort int) DiagnosticReport {
	if clientName == "" {
		clientName = "本地代理"
	}
	proxyURLStr := fmt.Sprintf("http://%s:%d", proxyHost, proxyPort)
	report := DiagnosticReport{
		ClientName: clientName,
		ProxyURL:   proxyURLStr,
		CheckedAt:  time.Now().Format("15:04:05"),
		Items:      make([]CheckItem, 6),
	}

	portLabel := clientName + " 本地代理端口"

	// 1. Local TCP Handshake check
	startTCP := time.Now()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", proxyHost, proxyPort), 1200*time.Millisecond)
	if err != nil {
		report.Items[0] = CheckItem{
			ID:     "local_port",
			Name:   portLabel,
			Target: fmt.Sprintf("%s:%d", proxyHost, proxyPort),
			Status: "error",
			Detail: fmt.Sprintf("无法连接本地端口，请确认 %s 已启动并开启本地监听", clientName),
		}
		for idx, item := range []struct{ id, name, target string }{
			{"exit_region", "代理出口 IP 与地区合规", "cloudflare.com/cdn-cgi/trace"},
			{"cursor_api", "Cursor 核心服务", "api2.cursor.sh"},
			{"claude_api", "Claude / Anthropic 服务", "api.anthropic.com"},
			{"openai_api", "OpenAI / Codex 服务", "api.openai.com"},
			{"antigravity_api", "Google Antigravity 服务", "antigravity.google"},
		} {
			report.Items[idx+1] = CheckItem{
				ID:     item.id,
				Name:   item.name,
				Target: item.target,
				Status: "error",
				Detail: "前置本地代理端口不可达",
			}
		}
		return report
	}
	_ = conn.Close()
	report.Items[0] = CheckItem{
		ID:        "local_port",
		Name:      portLabel,
		Target:    fmt.Sprintf("%s:%d", proxyHost, proxyPort),
		Status:    "ok",
		LatencyMs: time.Since(startTCP).Milliseconds(),
		Detail:    "本地 HTTP/混合端口握手正常",
	}

	parsedProxy, _ := url.Parse(proxyURLStr)
	client := &http.Client{
		Timeout: 5500 * time.Millisecond,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(parsedProxy),
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
			DisableKeepAlives: true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 2 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	var wg sync.WaitGroup
	wg.Add(5)

	// 2. Exit IP & Region check (with fallback trace endpoint to prevent transient TLS EOF)
	go func() {
		defer wg.Done()
		traceURLs := []string{
			"https://www.cloudflare.com/cdn-cgi/trace",
			"https://1.1.1.1/cdn-cgi/trace",
		}
		var body []byte
		var lat int64
		var lastErr error
		for _, tu := range traceURLs {
			t0 := time.Now()
			req, _ := http.NewRequest("GET", tu, nil)
			req.Header.Set("User-Agent", "ProxyBox-Diagnostics/1.3")
			resp, err := client.Do(req)
			lat = time.Since(t0).Milliseconds()
			if err != nil {
				lastErr = err
				continue
			}
			body, _ = io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if len(body) > 0 {
				lastErr = nil
				break
			}
		}
		if lastErr != nil {
			report.Items[1] = CheckItem{
				ID:     "exit_region",
				Name:   "代理出口 IP 与地区合规",
				Target: "cloudflare.com/cdn-cgi/trace",
				Status: "error",
				Detail: "代理隧道请求超时: " + shortErr(lastErr),
			}
			return
		}

		var ip, loc string
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "ip=") {
				ip = strings.TrimPrefix(line, "ip=")
			} else if strings.HasPrefix(line, "loc=") {
				loc = strings.ToUpper(strings.TrimPrefix(line, "loc="))
			}
		}
		report.ExitIP = ip
		report.ExitRegion = loc

		if badDesc, isBad := restrictedRegions[loc]; isBad {
			report.ExitRegionName = badDesc
			report.RegionAllowed = false
			report.Items[1] = CheckItem{
				ID:        "exit_region",
				Name:      "代理出口 IP 与地区合规",
				Target:    fmt.Sprintf("%s (%s)", ip, loc),
				Status:    "warn",
				LatencyMs: lat,
				Detail:    fmt.Sprintf("当前出口为 %s，会被 Claude/Antigravity 拒绝，请配置分流规则或切换美/日/新节点", badDesc),
			}
		} else if loc != "" {
			rName := friendlyRegions[loc]
			if rName == "" {
				rName = loc
			}
			report.ExitRegionName = rName + " (" + loc + ")"
			report.RegionAllowed = true
			report.Items[1] = CheckItem{
				ID:        "exit_region",
				Name:      "代理出口 IP 与地区合规",
				Target:    fmt.Sprintf("%s (%s)", ip, rName),
				Status:    "ok",
				LatencyMs: lat,
				Detail:    fmt.Sprintf("出口地区 %s (%s) 合规可用", rName, loc),
			}
		} else {
			report.Items[1] = CheckItem{
				ID:        "exit_region",
				Name:      "代理出口 IP 与地区合规",
				Target:    "cloudflare.com/cdn-cgi/trace",
				Status:    "warn",
				LatencyMs: lat,
				Detail:    "已连通但未能解析出口地区标识",
			}
		}
	}()

	// 3. Cursor API
	go func() {
		defer wg.Done()
		report.Items[2] = probeEndpoint(client, "cursor_api", "Cursor 核心服务", "https://api2.cursor.sh")
	}()

	// 4. Anthropic / Claude API
	go func() {
		defer wg.Done()
		report.Items[3] = probeEndpoint(client, "claude_api", "Claude / Anthropic 服务", "https://api.anthropic.com")
	}()

	// 5. OpenAI / Codex API
	go func() {
		defer wg.Done()
		report.Items[4] = probeEndpoint(client, "openai_api", "OpenAI / Codex 服务", "https://api.openai.com")
	}()

	// 6. Google Antigravity
	go func() {
		defer wg.Done()
		report.Items[5] = probeEndpoint(client, "antigravity_api", "Google Antigravity 服务", "https://antigravity.google")
	}()

	wg.Wait()
	return report
}

func probeEndpoint(client *http.Client, id, name, rawURL string) CheckItem {
	u, _ := url.Parse(rawURL)
	var resp *http.Response
	var err error
	var lat int64

	for attempt := 0; attempt < 2; attempt++ {
		t0 := time.Now()
		req, _ := http.NewRequest("GET", rawURL, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) ProxyBox/1.3")
		resp, err = client.Do(req)
		lat = time.Since(t0).Milliseconds()
		if err == nil {
			break
		}
		if attempt == 0 && (strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "reset")) {
			continue
		}
		break
	}
	if err != nil {
		return CheckItem{
			ID:        id,
			Name:      name,
			Target:    u.Host,
			Status:    "error",
			LatencyMs: lat,
			Detail:    "连接失败: " + shortErr(err),
		}
	}
	defer resp.Body.Close()
	snippetBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	snippet := strings.ToLower(string(snippetBytes))

	if resp.StatusCode == 403 && (strings.Contains(snippet, "region") || strings.Contains(snippet, "country") || (strings.Contains(snippet, "forbidden") && id == "claude_api")) {
		return CheckItem{
			ID:        id,
			Name:      name,
			Target:    u.Host,
			Status:    "warn",
			LatencyMs: lat,
			Detail:    fmt.Sprintf("网络已通 (HTTP %d)，但当前节点疑似受地区限制", resp.StatusCode),
		}
	}

	return CheckItem{
		ID:        id,
		Name:      name,
		Target:    u.Host,
		Status:    "ok",
		LatencyMs: lat,
		Detail:    fmt.Sprintf("TLS 代理隧道畅通 (HTTP %d)", resp.StatusCode),
	}
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 72 {
		return s[:72] + "..."
	}
	return s
}
