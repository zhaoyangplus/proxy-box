package mihomo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const (
	AIGroupName = "🤖 AI-IDE"
	AIAutoName  = "⚡ AI-Auto"
	TestURL     = "https://www.gstatic.com/generate_204"
)

var (
	// Restricted regions for Claude / Anthropic / Google Antigravity + subscription info pseudo-nodes
	restrictedNodeRe = regexp.MustCompile(`(?i)香港|🇭🇰|hong\s*kong|港\d|hktj|hkbn|hkix|hgc|hkt|澳门|🇲🇴|macau|macao|中国|大陆|🇨🇳|china|俄罗斯|🇷🇺|russia|白俄罗斯|伊朗|朝鲜|(?:^|[^a-z])(hk|mo|cn|ru|by|ir|kp)(?:[^a-z]|$)|剩余|流量|到期|官网|套餐|重置|订阅|网址|失联|群|邀请|距离|下次|说明|过期|维护|direct|reject|compatible|pass`)
	preferredNodeRe  = regexp.MustCompile(`(?i)美国|🇺🇸|united\s*states|日本|🇯🇵|japan|新加坡|🇸🇬|singapore|狮城|台湾|🇹🇼|taiwan|韩国|🇰🇷|korea|英国|🇬🇧|德国|🇩🇪|(?:^|[^a-z])(us|usa|jp|sg|tw|kr|uk|gb|de)(?:[^a-z]|$)`)
)

func IsRestrictedNodeName(name string) bool {
	return restrictedNodeRe.MatchString(strings.TrimSpace(name))
}

func IsPreferredNodeName(name string) bool {
	return preferredNodeRe.MatchString(strings.TrimSpace(name))
}

type Client struct {
	pipePath string
	httpBase string
	secret   string
	usePipe  bool
	client   *http.Client
}

// LiveNodeStatus summarizes the currently active proxy group and resolved leaf node in Mihomo.
type LiveNodeStatus struct {
	Connected       bool   `json:"connected"`
	IsolatedGroup   bool   `json:"isolatedGroup"` // true when dedicated 🤖 AI-IDE group exists
	GroupName       string `json:"groupName"`
	SelectedName    string `json:"selectedName"`
	ResolvedNode    string `json:"resolvedNode"`
	ResolvedDelayMs int    `json:"resolvedDelayMs"`
	Compliant       bool   `json:"compliant"`
	TotalNodes      int    `json:"totalNodes"`
	CompliantNodes  int    `json:"compliantNodes"`
	ExternalGroup   string `json:"externalGroup,omitempty"`
	ExternalNode    string `json:"externalNode,omitempty"`
}

// NodeOption represents one selectable proxy node inside CursorBox's independent 🤖 AI-IDE group.
type NodeOption struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	DelayMs    int    `json:"delayMs"`
	Alive      bool   `json:"alive"`
	Compliant  bool   `json:"compliant"`
	Preferred  bool   `json:"preferred"`
	IsAuto     bool   `json:"isAuto"`
	Selected   bool   `json:"selected"`
	ResolvedTo string `json:"resolvedTo,omitempty"`
}

// NodeListResult is returned to the UI's independent node picker modal.
type NodeListResult struct {
	IsolatedGroup string       `json:"isolatedGroup"`
	CurrentSelect string       `json:"currentSelect"`
	ResolvedNode  string       `json:"resolvedNode"`
	ExternalGroup string       `json:"externalGroup,omitempty"`
	ExternalNode  string       `json:"externalNode,omitempty"`
	Nodes         []NodeOption `json:"nodes"`
}

// OptimizeResult describes the outcome of an automatic optimal node selection pass.
type OptimizeResult struct {
	GroupName    string `json:"groupName"`
	SelectedNode string `json:"selectedNode"`
	DelayMs      int    `json:"delayMs"`
	TestedCount  int    `json:"testedCount"`
	AliveCount   int    `json:"aliveCount"`
	Switched     bool   `json:"switched"`
	Message      string `json:"message"`
}

type proxyItem struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Now     string   `json:"now"`
	All     []string `json:"all"`
	Alive   bool     `json:"alive"`
	History []struct {
		Delay int `json:"delay"`
	} `json:"history"`
}

type proxiesResponse struct {
	Proxies map[string]proxyItem `json:"proxies"`
}

func openNamedPipe(ctx context.Context, pipePath string) (windows.Handle, error) {
	pPtr, err := windows.UTF16PtrFromString(pipePath)
	if err != nil {
		return windows.InvalidHandle, err
	}
	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		deadline = time.Now().Add(1500 * time.Millisecond)
	}
	for {
		h, err := windows.CreateFile(
			pPtr,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0,
			nil,
			windows.OPEN_EXISTING,
			0,
			0,
		)
		if err == nil {
			return h, nil
		}
		if err != windows.ERROR_PIPE_BUSY || time.Now().After(deadline) {
			return windows.InvalidHandle, err
		}
		time.Sleep(40 * time.Millisecond)
	}
}

// doPipeHTTP executes a strict sequential HTTP/1.1 request/response over a synchronous Windows Named Pipe,
// avoiding Go net/http.Transport's concurrent readLoop/writeLoop deadlock on synchronous pipe handles.
func doPipeHTTP(ctx context.Context, pipePath, method, reqPath string, body []byte) ([]byte, int, error) {
	h, err := openNamedPipe(ctx, pipePath)
	if err != nil {
		return nil, 0, err
	}
	f := os.NewFile(uintptr(h), pipePath)
	defer f.Close()

	// Cancel pending synchronous I/O cleanly if context expires
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = windows.CancelIoEx(h, nil)
		case <-done:
		}
	}()

	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, "http://localhost"+reqPath, rdr)
	if err != nil {
		return nil, 0, err
	}
	req.Close = true
	if body != nil {
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Type", "application/json")
	}

	if err := req.Write(f); err != nil {
		return nil, 0, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(f), req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	return data, resp.StatusCode, err
}

// NewClient creates a Mihomo controller client, preferring Windows Named Pipe when available.
func NewClient(pipePath, httpAddr, secret string) *Client {
	c := &Client{
		pipePath: strings.TrimSpace(pipePath),
		secret:   strings.TrimSpace(secret),
	}

	if c.pipePath != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
		_, status, err := doPipeHTTP(ctx, c.pipePath, http.MethodGet, "/version", nil)
		cancel()
		if err == nil && status == http.StatusOK {
			c.usePipe = true
			c.httpBase = "http://localhost"
			return c
		}
	}

	addr := strings.TrimSpace(httpAddr)
	if addr != "" {
		if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
			addr = "http://" + addr
		}
		c.httpBase = strings.TrimRight(addr, "/")
		c.client = &http.Client{
			Timeout: 6 * time.Second,
			Transport: &http.Transport{
				Proxy:             nil,
				DisableKeepAlives: true,
			},
		}
	}
	return c
}

func (c *Client) Available() bool {
	return c != nil && (c.usePipe || (c.client != nil && c.httpBase != ""))
}

func (c *Client) doRequest(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
	if !c.Available() {
		return nil, 0, fmt.Errorf("Mihomo 控制器不可用")
	}
	if c.usePipe {
		return doPipeHTTP(ctx, c.pipePath, method, path, body)
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.httpBase+path, rdr)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	return data, resp.StatusCode, err
}

func (c *Client) getProxies(ctx context.Context) (map[string]proxyItem, error) {
	data, status, err := c.doRequest(ctx, http.MethodGet, "/proxies", nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("HTTP %d", status)
	}
	var pr proxiesResponse
	if err := json.Unmarshal(data, &pr); err != nil {
		return nil, err
	}
	return pr.Proxies, nil
}

func (c *Client) selectProxy(ctx context.Context, groupName, targetName string) error {
	payload, _ := json.Marshal(map[string]string{"name": targetName})
	_, status, err := c.doRequest(ctx, http.MethodPut, "/proxies/"+url.PathEscape(groupName), payload)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("切换节点失败 (HTTP %d)", status)
	}
	return nil
}

func (c *Client) testGroupDelay(ctx context.Context, groupName string, timeoutMs int) (map[string]int, error) {
	q := fmt.Sprintf("/group/%s/delay?url=%s&timeout=%d", url.PathEscape(groupName), url.QueryEscape(TestURL), timeoutMs)
	data, status, err := c.doRequest(ctx, http.MethodGet, q, nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("群组测速返回 HTTP %d", status)
	}
	var delays map[string]int
	if err := json.Unmarshal(data, &delays); err != nil {
		return nil, err
	}
	return delays, nil
}

func (c *Client) testSingleNodeDelay(ctx context.Context, nodeName string, timeoutMs int) int {
	q := fmt.Sprintf("/proxies/%s/delay?url=%s&timeout=%d", url.PathEscape(nodeName), url.QueryEscape(TestURL), timeoutMs)
	data, status, err := c.doRequest(ctx, http.MethodGet, q, nil)
	if err != nil || status < 200 || status >= 300 {
		return 0
	}
	var res struct {
		Delay int `json:"delay"`
	}
	if json.Unmarshal(data, &res) != nil {
		return 0
	}
	return res.Delay
}

// ReloadConfig triggers Mihomo core hot-reload via Named Pipe or HTTP controller.
func (c *Client) ReloadConfig() bool {
	if !c.Available() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, status, err := c.doRequest(ctx, http.MethodPut, "/configs?force=true", []byte(`{"path":""}`))
	return err == nil && status >= 200 && status < 300
}

// GetLiveStatus inspects Mihomo's active group, resolves nested groups to the actual leaf node,
// and reports whether it is compliant (non-HK/CN) along with its last known latency.
func (c *Client) GetLiveStatus() LiveNodeStatus {
	if !c.Available() {
		return LiveNodeStatus{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	proxies, err := c.getProxies(ctx)
	if err != nil || len(proxies) == 0 {
		return LiveNodeStatus{}
	}

	_, hasIsolated := proxies[AIGroupName]
	groupName := pickPrimaryGroup(proxies)
	extGroup := pickExternalGroup(proxies)
	extNode := ""
	if extGroup != "" {
		extNode, _ = resolveLeafNode(proxies, extGroup, 6)
	}
	if groupName == "" {
		return LiveNodeStatus{Connected: true, ExternalGroup: extGroup, ExternalNode: extNode}
	}

	grp := proxies[groupName]
	resolved, delayMs := resolveLeafNode(proxies, groupName, 6)

	totalLeaf, compliantLeaf := 0, 0
	for _, member := range grp.All {
		if m, ok := proxies[member]; ok && !isGroupType(m.Type) {
			totalLeaf++
			if !IsRestrictedNodeName(member) {
				compliantLeaf++
			}
		}
	}
	if totalLeaf == 0 {
		if autoGrp, ok := proxies[AIAutoName]; ok {
			for _, member := range autoGrp.All {
				totalLeaf++
				if !IsRestrictedNodeName(member) {
					compliantLeaf++
				}
			}
		}
	}

	return LiveNodeStatus{
		Connected:       true,
		IsolatedGroup:   hasIsolated,
		GroupName:       groupName,
		SelectedName:    grp.Now,
		ResolvedNode:    resolved,
		ResolvedDelayMs: delayMs,
		Compliant:       resolved != "" && !IsRestrictedNodeName(resolved),
		TotalNodes:      totalLeaf,
		CompliantNodes:  compliantLeaf,
		ExternalGroup:   extGroup,
		ExternalNode:    extNode,
	}
}

// ListAINodes returns all selectable nodes in CursorBox's isolated 🤖 AI-IDE group
// along with the external proxy client's own main group/node so the user can see both are independent.
func (c *Client) ListAINodes(runDelayTest bool) (*NodeListResult, error) {
	if !c.Available() {
		return nil, fmt.Errorf("未连接到 Mihomo 控制接口，请确认代理软件已运行")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	proxies, err := c.getProxies(ctx)
	if err != nil {
		return nil, err
	}

	grp, hasAI := proxies[AIGroupName]
	if !hasAI {
		return nil, fmt.Errorf("当前配置尚未启用独立分组 %s，请先点击「一键配置全部应用」并在代理软件刷新订阅", AIGroupName)
	}

	var freshDelays map[string]int
	if runDelayTest {
		if _, hasAuto := proxies[AIAutoName]; hasAuto {
			if d, err := c.testGroupDelay(ctx, AIAutoName, 3200); err == nil {
				freshDelays = d
			}
		}
		// Refresh proxies snapshot after delay test to get updated history & ⚡ AI-Auto.now
		if updated, err := c.getProxies(ctx); err == nil && len(updated) > 0 {
			proxies = updated
			grp = proxies[AIGroupName]
		}
	}

	resolvedNow, _ := resolveLeafNode(proxies, AIGroupName, 6)
	extGroup := pickExternalGroup(proxies)
	extNode := ""
	if extGroup != "" {
		extNode, _ = resolveLeafNode(proxies, extGroup, 6)
	}

	nodes := make([]NodeOption, 0, len(grp.All))
	for _, member := range grp.All {
		p, ok := proxies[member]
		if !ok {
			continue
		}
		if member == AIAutoName {
			rName, rDelay := resolveLeafNode(proxies, AIAutoName, 4)
			nodes = append(nodes, NodeOption{
				Name:       AIAutoName,
				Type:       "URLTest",
				DelayMs:    rDelay,
				Alive:      rDelay > 0 || p.Alive,
				Compliant:  true,
				Preferred:  true,
				IsAuto:     true,
				Selected:   grp.Now == AIAutoName,
				ResolvedTo: rName,
			})
			continue
		}
		if isGroupType(p.Type) {
			continue
		}
		delay := 0
		if freshDelays != nil {
			delay = freshDelays[member]
		}
		if delay == 0 && len(p.History) > 0 {
			delay = p.History[len(p.History)-1].Delay
		}
		nodes = append(nodes, NodeOption{
			Name:      member,
			Type:      p.Type,
			DelayMs:   delay,
			Alive:     delay > 0 || p.Alive,
			Compliant: !IsRestrictedNodeName(member),
			Preferred: IsPreferredNodeName(member),
			IsAuto:    false,
			Selected:  grp.Now == member,
		})
	}

	// Sort: ⚡ AI-Auto first, then alive nodes by delay asc, then untested/dead nodes
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].IsAuto != nodes[j].IsAuto {
			return nodes[i].IsAuto
		}
		di, dj := nodes[i].DelayMs, nodes[j].DelayMs
		if di > 0 && dj > 0 {
			return di < dj
		}
		if (di > 0) != (dj > 0) {
			return di > 0
		}
		if nodes[i].Preferred != nodes[j].Preferred {
			return nodes[i].Preferred
		}
		return nodes[i].Name < nodes[j].Name
	})

	return &NodeListResult{
		IsolatedGroup: AIGroupName,
		CurrentSelect: grp.Now,
		ResolvedNode:  resolvedNow,
		ExternalGroup: extGroup,
		ExternalNode:  extNode,
		Nodes:         nodes,
	}, nil
}

// SelectAINode switches ONLY CursorBox's isolated 🤖 AI-IDE group to targetNode,
// strictly never touching the external proxy software's own main group.
func (c *Client) SelectAINode(targetNode string) (*LiveNodeStatus, error) {
	if !c.Available() {
		return nil, fmt.Errorf("未连接到 Mihomo 控制接口")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	proxies, err := c.getProxies(ctx)
	if err != nil {
		return nil, err
	}
	grp, hasAI := proxies[AIGroupName]
	if !hasAI {
		return nil, fmt.Errorf("尚未启用独立分组 %s，为避免影响外部代理软件节点，请先注入分流脚本并刷新订阅", AIGroupName)
	}

	valid := false
	for _, m := range grp.All {
		if m == targetNode {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("节点 %s 不在当前独立分组 %s 中", targetNode, AIGroupName)
	}

	if err := c.selectProxy(ctx, AIGroupName, targetNode); err != nil {
		return nil, err
	}
	live := c.GetLiveStatus()
	return &live, nil
}

// OptimizeBestNode actively probes compliant (non-HK/CN/RU) nodes inside Mihomo
// and immediately switches the active AI/Primary group to the lowest-latency reachable node.
func (c *Client) OptimizeBestNode() (*OptimizeResult, error) {
	if !c.Available() {
		return nil, fmt.Errorf("未连接到 Mihomo 控制接口（请确认代理软件已运行）")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()

	proxies, err := c.getProxies(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取 Mihomo 节点列表失败: %w", err)
	}

	// Strict isolation: NEVER touch external proxy software's own main group when 🤖 AI-IDE is missing!
	// Only operate on CursorBox's dedicated 🤖 AI-IDE group so switching nodes never affects external browsing.
	if _, hasAIIDE := proxies[AIGroupName]; !hasAIIDE {
		return nil, fmt.Errorf("未检测到独立隔离分组 %s（为保证不影响外部代理软件的节点，请先注入分流脚本并在代理软件刷新订阅）", AIGroupName)
	}
	groupName := AIGroupName
	var candidates []string
	var delays map[string]int

	if autoGrp, hasAuto := proxies[AIAutoName]; hasAuto {
		if d, err := c.testGroupDelay(ctx, AIAutoName, 3200); err == nil && len(d) > 0 {
			delays = d
		}
		for _, n := range autoGrp.All {
			if !IsRestrictedNodeName(n) {
				candidates = append(candidates, n)
			}
		}
	}
	if len(candidates) == 0 {
		for _, n := range proxies[AIGroupName].All {
			if m, ok := proxies[n]; ok && !isGroupType(m.Type) && !IsRestrictedNodeName(n) {
				candidates = append(candidates, n)
			}
		}
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("当前订阅中未找到非香港/非受限地区节点")
	}

	// If /group/.../delay returned empty (some Mihomo versions), test candidate nodes concurrently
	if len(delays) == 0 {
		delays = c.testNodesConcurrently(ctx, candidates, 3000)
	}

	type scoredNode struct {
		name      string
		delay     int
		preferred bool
		score     int
	}
	var alive []scoredNode
	for _, name := range candidates {
		d := delays[name]
		if d <= 0 {
			continue
		}
		pref := IsPreferredNodeName(name)
		// Give preferred US/JP/SG/TW/KR/EU regions a slight 20ms priority tie-breaker
		score := d
		if pref {
			score -= 20
		}
		alive = append(alive, scoredNode{
			name:      name,
			delay:     d,
			preferred: pref,
			score:     score,
		})
	}

	if len(alive) == 0 {
		return nil, fmt.Errorf("测速完成（共 %d 个合规节点），但暂无可达节点，请检查网络或切换订阅", len(candidates))
	}

	sort.Slice(alive, func(i, j int) bool {
		return alive[i].score < alive[j].score
	})
	best := alive[0]

	// Switch target group to best node!
	// If groupName is 🤖 AI-IDE, check whether ⚡ AI-Auto already picked a node within 30ms of best.
	switched := false
	if groupName == AIGroupName {
		// Directly select best node on 🤖 AI-IDE if ⚡ AI-Auto didn't pick it, OR select ⚡ AI-Auto if it matches best
		refreshed, _ := c.getProxies(ctx)
		autoNow := ""
		if refreshed != nil {
			if ag, ok := refreshed[AIAutoName]; ok {
				autoNow = ag.Now
			}
		}
		if autoNow == best.name {
			if proxies[AIGroupName].Now != AIAutoName {
				_ = c.selectProxy(ctx, AIGroupName, AIAutoName)
				switched = true
			}
		} else {
			if err := c.selectProxy(ctx, AIGroupName, best.name); err == nil {
				switched = true
			}
		}
	} else {
		if proxies[groupName].Now != best.name {
			if err := c.selectProxy(ctx, groupName, best.name); err != nil {
				return nil, err
			}
			switched = true
		}
	}

	// Also ensure primary default selector group isn't stuck on a restricted/HK node when no AI-IDE group exists
	return &OptimizeResult{
		GroupName:    groupName,
		SelectedNode: best.name,
		DelayMs:      best.delay,
		TestedCount:  len(candidates),
		AliveCount:   len(alive),
		Switched:     switched,
		Message:      fmt.Sprintf("已自动优选 [%s] 最优合规节点：%s (%d ms)", groupName, best.name, best.delay),
	}, nil
}

func (c *Client) testNodesConcurrently(ctx context.Context, nodes []string, timeoutMs int) map[string]int {
	results := make(map[string]int)
	var mu sync.Mutex
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup

	// Cap to first 35 nodes to keep optimization snappy (<3.5s)
	limit := len(nodes)
	if limit > 35 {
		limit = 35
	}
	for _, n := range nodes[:limit] {
		wg.Add(1)
		go func(nodeName string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			d := c.testSingleNodeDelay(ctx, nodeName, timeoutMs)
			if d > 0 {
				mu.Lock()
				results[nodeName] = d
				mu.Unlock()
			}
		}(n)
	}
	wg.Wait()
	return results
}

func pickPrimaryGroup(proxies map[string]proxyItem) string {
	if _, ok := proxies[AIGroupName]; ok {
		return AIGroupName
	}
	return pickExternalGroup(proxies)
}

// pickExternalGroup identifies the external proxy client's own primary Selector group (excluding 🤖 AI-IDE & GLOBAL).
func pickExternalGroup(proxies map[string]proxyItem) string {
	for _, common := range []string{"PROXY", "Proxy", "节点选择", "手动切换", "选择节点", "🚀 节点选择", "🔰 节点选择"} {
		if p, ok := proxies[common]; ok && p.Type == "Selector" && len(p.All) > 0 {
			return common
		}
	}
	var bestName string
	bestCount := -1
	for name, p := range proxies {
		if name == "GLOBAL" || name == AIGroupName || name == AIAutoName || p.Type != "Selector" {
			continue
		}
		if len(p.All) > bestCount {
			bestCount = len(p.All)
			bestName = name
		}
	}
	return bestName
}

func collectLeafCandidates(proxies map[string]proxyItem, name string, depth int, seen map[string]bool, out *[]string) {
	if depth <= 0 || seen[name] {
		return
	}
	seen[name] = true
	p, ok := proxies[name]
	if !ok {
		return
	}
	if !isGroupType(p.Type) {
		if !IsRestrictedNodeName(name) {
			*out = append(*out, name)
		}
		return
	}
	for _, child := range p.All {
		collectLeafCandidates(proxies, child, depth-1, seen, out)
	}
}

func resolveLeafNode(proxies map[string]proxyItem, name string, depth int) (string, int) {
	curr := name
	for i := 0; i < depth; i++ {
		p, ok := proxies[curr]
		if !ok {
			return curr, 0
		}
		if !isGroupType(p.Type) || p.Now == "" {
			delay := 0
			if len(p.History) > 0 {
				delay = p.History[len(p.History)-1].Delay
			}
			return p.Name, delay
		}
		curr = p.Now
	}
	return curr, 0
}

func isGroupType(t string) bool {
	switch strings.ToLower(t) {
	case "selector", "urltest", "fallback", "loadbalance", "relay":
		return true
	}
	return false
}
