package detector

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"proxy-box/internal/configstore"
	"proxy-box/internal/configurator"
	"proxy-box/internal/mihomo"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type ProxyClientInfo struct {
	ID                  string `json:"id"`   // "builtin"
	Name                string `json:"name"` // "内置内核"
	Installed           bool   `json:"installed"`
	Running             bool   `json:"running"`
	CoreRunning         bool   `json:"coreRunning"`
	ConfigDir           string `json:"configDir"`
	ExePath             string `json:"exePath"`
	ProxyHost           string `json:"proxyHost"`
	MainPort            int    `json:"mainPort"`
	DedicatedPort       int    `json:"dedicatedPort,omitempty"`
	DedicatedPortActive bool   `json:"dedicatedPortActive"`
	MixedPort           int    `json:"mixedPort"`
	PortReachable       bool   `json:"portReachable"`
	HTTPVerified        bool   `json:"httpVerified"`
	TunEnabled          bool   `json:"tunEnabled"`
	SysProxyEnabled     bool   `json:"sysProxyEnabled"`
	ActiveProfileUID    string `json:"activeProfileUid,omitempty"`
	ActiveProfileName   string `json:"activeProfileName"`
	ExternalController  string `json:"externalController,omitempty"`
	Secret              string `json:"-"`
	ControllerReachable bool   `json:"controllerReachable"`
	IsolatedGroup       bool   `json:"isolatedGroup"`
	ActiveGroup         string `json:"activeGroup,omitempty"`
	ActiveNode          string `json:"activeNode,omitempty"`
	ActiveNodeDelayMs   int    `json:"activeNodeDelayMs,omitempty"`
	ActiveNodeCompliant bool   `json:"activeNodeCompliant"`
	ExternalGroup       string `json:"externalGroup,omitempty"`
	ExternalNode        string `json:"externalNode,omitempty"`
	ProxyURL            string `json:"proxyUrl"`
	RuleSupportMode     string `json:"ruleSupportMode"` // "verge_auto", "clashmi_override", "v2rayn_clipboard"
	RuleInjected        bool   `json:"ruleInjected"`
	RuleActiveInRuntime bool   `json:"ruleActiveInRuntime"`
	ModeHint            string `json:"modeHint,omitempty"`
	ConfigModUnix       int64  `json:"configModUnix,omitempty"`
}

type AppTargetInfo struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Installed             bool   `json:"installed"`
	Running               bool   `json:"running"`
	HelperRunning         bool   `json:"helperRunning"`
	ExePath               string `json:"exePath"`
	SettingsPath          string `json:"settingsPath"`
	ProxyConfigured       bool   `json:"proxyConfigured"`
	ProxyMatchesClash     bool   `json:"proxyMatchesClash"`
	CurrentProxy          string `json:"currentProxy"`
	Http2Disabled         bool   `json:"http2Disabled"`
	TerminalEnvConfigured bool   `json:"terminalEnvConfigured"`
	ManagedByBox          bool   `json:"managedByBox"`
	EnabledByUser         bool   `json:"enabledByUser"`
	ShortcutExists        bool   `json:"shortcutExists"`
	StatusText            string `json:"statusText"`
}

type SystemSnapshot struct {
	ActiveClient ProxyClientInfo       `json:"activeClient"`
	Targets      []AppTargetInfo       `json:"targets"`
	UserEnvProxy string                `json:"userEnvProxy"`
	AppConfig    configstore.AppConfig `json:"appConfig"`
	CheckedAt    string                `json:"checkedAt"`
}

// procInstance is one process. Several mihomo.exe processes share an exe name and must not share a path or port list.
type procInstance struct {
	PID   uint32
	Path  string
	Ports []int
}

type procDetail struct {
	Path      string
	PIDs      []uint32
	Ports     []int
	Instances []procInstance
}

func controllerClient(c ProxyClientInfo) *mihomo.Client {
	mc := mihomo.NewClient(c.ExternalController, c.Secret)
	cfg, _ := configstore.LoadConfig()
	mc.SetDelayTestURL(cfg.DelayTestURL)
	return mc
}

// OptimizeClientNode selects the lowest-latency compliant node on the builtin 🤖 AI-IDE group.
func OptimizeClientNode(c ProxyClientInfo) (*mihomo.OptimizeResult, error) {
	if !c.Running {
		return nil, fmt.Errorf("内置内核未运行，无法优选节点")
	}
	mc := controllerClient(c)
	if !mc.Available() {
		return nil, fmt.Errorf("未能连接到 %s 内核控制接口，请确认内核已运行", c.Name)
	}
	return mc.OptimizeBestNode()
}

// ListClientNodes returns selectable nodes in the builtin 🤖 AI-IDE group.
func ListClientNodes(c ProxyClientInfo, runDelayTest bool) (*mihomo.NodeListResult, error) {
	if !c.Running {
		return nil, fmt.Errorf("内置内核未运行")
	}
	mc := controllerClient(c)
	if !mc.Available() {
		return nil, fmt.Errorf("未能连接到 %s 内核控制接口，请确认内核已运行", c.Name)
	}
	return mc.ListAINodes(runDelayTest)
}

// SelectClientNode switches the builtin 🤖 AI-IDE group to targetNode.
func SelectClientNode(c ProxyClientInfo, targetNode string) (*mihomo.LiveNodeStatus, error) {
	if !c.Running {
		return nil, fmt.Errorf("内置内核未运行")
	}
	mc := controllerClient(c)
	if !mc.Available() {
		return nil, fmt.Errorf("未能连接到 %s 内核控制接口", c.Name)
	}
	return mc.SelectAINode(targetNode)
}

// FastConfigStamp watches Proxy Box's own config, core, and provider files.
func FastConfigStamp() string {
	var files []string
	if base, err := configstore.BaseDir(); err == nil {
		files = append(files,
			filepath.Join(base, "config.json"),
			filepath.Join(base, "core.json"),
			filepath.Join(base, "subscriptions.json"),
		)
	}
	coreDir := proxyBoxCoreDir()
	files = append(files, filepath.Join(coreDir, "config.yaml"))

	var b strings.Builder
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && !st.IsDir() {
			fmt.Fprintf(&b, "%s\t%d\t%d\n", f, st.ModTime().UnixNano(), st.Size())
		}
	}
	stampProvidersDir(&b, filepath.Join(coreDir, "providers"))
	return b.String()
}

func snapshotProcessesAndPorts() map[string]*procDetail {
	pidPorts := getListeningTCPPortsByPID()
	result := make(map[string]*procDetail)

	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return result
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return result
	}

	for {
		exeName := strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
		if exeName != "" {
			noteProcess(result, exeName, pe.ProcessID, queryProcessPath(pe.ProcessID), pidPorts[pe.ProcessID])
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return result
}

// noteProcess stores this PID's path and ports under the exe name without merging them into another PID.
func noteProcess(result map[string]*procDetail, exeName string, pid uint32, path string, ports []int) {
	exeName = strings.ToLower(strings.TrimSpace(exeName))
	if exeName == "" {
		return
	}
	d, ok := result[exeName]
	if !ok {
		d = &procDetail{}
		result[exeName] = d
	}
	owned := append([]int(nil), ports...)
	d.Instances = append(d.Instances, procInstance{PID: pid, Path: path, Ports: owned})
	d.PIDs = append(d.PIDs, pid)
	if d.Path == "" {
		d.Path = path
	}
	for _, p := range owned {
		if !containsInt(d.Ports, p) {
			d.Ports = append(d.Ports, p)
		}
	}
}

func getListeningTCPPortsByPID() map[uint32][]int {
	res := make(map[uint32][]int)
	iphlpapi := windows.NewLazySystemDLL("iphlpapi.dll")
	getExtTcp := iphlpapi.NewProc("GetExtendedTcpTable")

	const (
		afInet                   = 2
		tcpTableOwnerPIDListener = 3
	)

	var size uint32
	_, _, _ = getExtTcp.Call(0, uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPIDListener, 0)
	if size == 0 {
		return res
	}
	buf := make([]byte, size+512)
	size = uint32(len(buf))
	ret, _, _ := getExtTcp.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPIDListener, 0)
	if ret != 0 || len(buf) < 4 {
		return res
	}

	numEntries := binary.LittleEndian.Uint32(buf[0:4])
	offset := 4
	for i := uint32(0); i < numEntries && offset+24 <= len(buf); i++ {
		localAddr := buf[offset+4 : offset+8]
		if localAddr[0] == 127 || (localAddr[0] == 0 && localAddr[1] == 0 && localAddr[2] == 0 && localAddr[3] == 0) {
			portNetOrder := binary.BigEndian.Uint16(buf[offset+8 : offset+10])
			pid := binary.LittleEndian.Uint32(buf[offset+20 : offset+24])
			port := int(portNetOrder)
			if port > 0 && !containsInt(res[pid], port) {
				res[pid] = append(res[pid], port)
			}
		}
		offset += 24
	}
	return res
}

func queryProcessPath(pid uint32) string {
	if pid == 0 {
		return ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_PATH*2)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

// isHTTPProxyPort verifies in <5ms on loopback whether host:port speaks HTTP Proxy protocol.
func isHTTPProxyPort(host string, port int) (reachable bool, speaksHTTP bool) {
	if port <= 0 || port > 65535 {
		return false, false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 260*time.Millisecond)
	if err != nil {
		return false, false
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(220 * time.Millisecond))
	probe := "GET / HTTP/1.1\r\n\r\n"
	if _, err := conn.Write([]byte(probe)); err != nil {
		return true, false
	}
	buf := make([]byte, 16)
	n, _ := conn.Read(buf)
	if n >= 5 && bytes.HasPrefix(buf[:n], []byte("HTTP/")) {
		return true, true
	}
	return true, false
}

func DetectAll() SystemSnapshot {
	cfg, _ := configstore.LoadConfig()
	st := configstore.LoadState()
	procs := snapshotProcessesAndPorts()
	active := detectBuiltin(procs)
	targets := []AppTargetInfo{
		detectCursor(cfg, st, procs, active.ProxyURL),
		detectAntigravity(cfg, st, procs, active.ProxyURL),
		detectClaudeCode(cfg, st, procs, active.ProxyURL),
		detectCodex(cfg, st, procs, active.ProxyURL),
	}
	return SystemSnapshot{
		ActiveClient: active,
		Targets:      targets,
		UserEnvProxy: readWindowsUserEnvProxy(),
		AppConfig:    cfg,
		CheckedAt:    time.Now().Format("15:04:05"),
	}
}

func detectCursor(cfg configstore.AppConfig, st configstore.StateManifest, procs map[string]*procDetail, activeProxyURL string) AppTargetInfo {
	t := AppTargetInfo{
		ID:           "cursor",
		Name:         "Cursor IDE",
		SettingsPath: configurator.CursorSettingsPath(),
	}

	if d, ok := procs["cursor.exe"]; ok {
		t.Running = true
		if d.Path != "" {
			t.ExePath = d.Path
		}
	}
	if cfg.CustomCursorExe != "" && fileExists(cfg.CustomCursorExe) {
		t.ExePath = cfg.CustomCursorExe
	} else if t.ExePath == "" {
		t.ExePath = findExecutable(
			[]string{"Cursor"},
			[]string{
				filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "cursor", "Cursor.exe"),
				filepath.Join(os.Getenv("ProgramFiles"), "cursor", "Cursor.exe"),
			},
			[]string{`cursor\Cursor.exe`, `Programs\cursor\Cursor.exe`},
		)
	}
	t.Installed = t.ExePath != "" || fileExists(t.SettingsPath)
	t.ShortcutExists = desktopShortcutExists("Cursor (免TUN代理).lnk")

	if tb, ok := st.Targets["cursor"]; ok && tb.Managed {
		t.ManagedByBox = true
	}

	if m, existed, err := configurator.ReadJSONMap(t.SettingsPath); err == nil && existed {
		if raw, ok := m["http.proxy"]; ok {
			_ = json.Unmarshal(raw, &t.CurrentProxy)
		}
		if raw, ok := m["cursor.general.disableHttp2"]; ok {
			_ = json.Unmarshal(raw, &t.Http2Disabled)
		}
		if rawEnv, ok := m["terminal.integrated.env.windows"]; ok {
			var envMap map[string]string
			if json.Unmarshal(rawEnv, &envMap) == nil && envMap["HTTP_PROXY"] != "" {
				t.TerminalEnvConfigured = true
			}
		}
	}

	t.ProxyConfigured = t.CurrentProxy != "" && t.Http2Disabled
	t.ProxyMatchesClash = strings.EqualFold(strings.TrimRight(t.CurrentProxy, "/"), strings.TrimRight(activeProxyURL, "/")) && t.Http2Disabled

	switch {
	case t.ProxyMatchesClash:
		t.StatusText = "已就绪（HTTP/1.1 + 端口匹配）"
	case t.CurrentProxy != "" && !t.Http2Disabled:
		t.StatusText = "未禁用 HTTP/2（会导致直连漏流）"
	case t.CurrentProxy != "" && !t.ProxyMatchesClash:
		t.StatusText = "代理端口与当前客户端不一致"
	default:
		t.StatusText = "未配置免 TUN 代理"
	}
	noteUserSwitch(cfg, &t)
	return t
}

func detectAntigravity(cfg configstore.AppConfig, st configstore.StateManifest, procs map[string]*procDetail, activeProxyURL string) AppTargetInfo {
	t := AppTargetInfo{
		ID:           "antigravity",
		Name:         "Google Antigravity",
		SettingsPath: configurator.AntigravitySettingsPath(),
	}

	if d, ok := procs["antigravity.exe"]; ok {
		t.Running = true
		if d.Path != "" {
			t.ExePath = d.Path
		}
	}
	if _, ok := procs["language_server_windows_x64.exe"]; ok {
		t.HelperRunning = true
	}

	if cfg.CustomAntigravityExe != "" && fileExists(cfg.CustomAntigravityExe) {
		t.ExePath = cfg.CustomAntigravityExe
	} else if t.ExePath == "" {
		t.ExePath = findExecutable(
			[]string{"Antigravity"},
			[]string{
				filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Antigravity", "Antigravity.exe"),
				filepath.Join(os.Getenv("ProgramFiles"), "Antigravity", "Antigravity.exe"),
			},
			[]string{`Antigravity\Antigravity.exe`, `Programs\Antigravity\Antigravity.exe`},
		)
	}
	t.Installed = t.ExePath != "" || dirExists(filepath.Dir(t.SettingsPath))
	t.ShortcutExists = desktopShortcutExists("Antigravity (免TUN代理).lnk")

	if tb, ok := st.Targets["antigravity"]; ok && tb.Managed {
		t.ManagedByBox = true
	}

	if m, existed, err := configurator.ReadJSONMap(t.SettingsPath); err == nil && existed {
		if raw, ok := m["http.proxy"]; ok {
			_ = json.Unmarshal(raw, &t.CurrentProxy)
		}
		if rawEnv, ok := m["terminal.integrated.env.windows"]; ok {
			var envMap map[string]string
			if json.Unmarshal(rawEnv, &envMap) == nil && envMap["HTTP_PROXY"] != "" {
				t.TerminalEnvConfigured = true
			}
		}
	}

	t.ProxyConfigured = t.CurrentProxy != "" && t.TerminalEnvConfigured
	t.ProxyMatchesClash = strings.EqualFold(strings.TrimRight(t.CurrentProxy, "/"), strings.TrimRight(activeProxyURL, "/")) && t.TerminalEnvConfigured

	switch {
	case t.ProxyMatchesClash:
		t.StatusText = "已配置（推荐通过下方代理启动器唤起）"
	case t.CurrentProxy != "":
		t.StatusText = "代理端口与当前客户端不一致"
	default:
		t.StatusText = "未配置免 TUN 代理"
	}
	noteUserSwitch(cfg, &t)
	return t
}

func detectClaudeCode(cfg configstore.AppConfig, st configstore.StateManifest, procs map[string]*procDetail, activeProxyURL string) AppTargetInfo {
	t := AppTargetInfo{
		ID:           "claude_code",
		Name:         "Claude Code CLI",
		SettingsPath: configurator.ClaudeCodeSettingsPath(),
	}

	if d, ok := procs["claude.exe"]; ok {
		t.Running = true
		t.ExePath = d.Path
	}
	npmClaude := filepath.Join(os.Getenv("APPDATA"), "npm", "claude.cmd")
	if t.ExePath == "" && fileExists(npmClaude) {
		t.ExePath = npmClaude
	}
	home, _ := os.UserHomeDir()
	t.Installed = t.ExePath != "" || dirExists(filepath.Join(home, ".claude")) || fileExists(t.SettingsPath)

	if tb, ok := st.Targets["claude_code"]; ok && tb.Managed {
		t.ManagedByBox = true
	}

	if m, existed, err := configurator.ReadJSONMap(t.SettingsPath); err == nil && existed {
		if rawEnv, ok := m["env"]; ok {
			var envMap map[string]string
			if json.Unmarshal(rawEnv, &envMap) == nil {
				if envMap["HTTPS_PROXY"] != "" {
					t.CurrentProxy = envMap["HTTPS_PROXY"]
				} else if envMap["HTTP_PROXY"] != "" {
					t.CurrentProxy = envMap["HTTP_PROXY"]
				}
				t.TerminalEnvConfigured = t.CurrentProxy != ""
			}
		}
	}

	t.ProxyConfigured = t.CurrentProxy != ""
	t.ProxyMatchesClash = strings.EqualFold(strings.TrimRight(t.CurrentProxy, "/"), strings.TrimRight(activeProxyURL, "/"))

	switch {
	case t.ProxyMatchesClash:
		t.StatusText = "已就绪（环境变量自动生效）"
	case t.CurrentProxy != "":
		t.StatusText = "代理端口与当前客户端不一致"
	default:
		t.StatusText = "未配置 ~/.claude/settings.json"
	}
	noteUserSwitch(cfg, &t)
	return t
}

func detectCodex(cfg configstore.AppConfig, st configstore.StateManifest, procs map[string]*procDetail, activeProxyURL string) AppTargetInfo {
	t := AppTargetInfo{
		ID:           "codex",
		Name:         "OpenAI Codex",
		SettingsPath: configurator.CodexEnvPath(),
	}

	for _, pname := range []string{"codex-plus-plus.exe", "codex.exe"} {
		if d, ok := procs[pname]; ok {
			t.Running = true
			if d.Path != "" && t.ExePath == "" {
				t.ExePath = d.Path
			}
		}
	}
	if cfg.CustomCodexExe != "" && fileExists(cfg.CustomCodexExe) {
		t.ExePath = cfg.CustomCodexExe
	} else if t.ExePath == "" {
		t.ExePath = findExecutable(
			[]string{"Codex", "Codex++"},
			[]string{
				filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Codex", "Codex.exe"),
				filepath.Join(os.Getenv("LOCALAPPDATA"), "OpenAI", "Codex", "Codex.exe"),
			},
			[]string{`Codex++\codex-plus-plus.exe`, `Codex\Codex.exe`},
		)
	}
	npmCodex := filepath.Join(os.Getenv("APPDATA"), "npm", "codex.cmd")
	if t.ExePath == "" && fileExists(npmCodex) {
		t.ExePath = npmCodex
	}

	home, _ := os.UserHomeDir()
	t.Installed = t.ExePath != "" || dirExists(filepath.Join(home, ".codex")) || fileExists(t.SettingsPath)
	t.ShortcutExists = desktopShortcutExists("Codex (免TUN代理).lnk")

	if tb, ok := st.Targets["codex"]; ok && tb.Managed {
		t.ManagedByBox = true
	}

	if envMap, existed, err := configurator.ReadEnvFile(t.SettingsPath); err == nil && existed {
		if envMap["HTTPS_PROXY"] != "" {
			t.CurrentProxy = envMap["HTTPS_PROXY"]
		} else if envMap["HTTP_PROXY"] != "" {
			t.CurrentProxy = envMap["HTTP_PROXY"]
		}
		t.TerminalEnvConfigured = t.CurrentProxy != ""
	}

	t.ProxyConfigured = t.CurrentProxy != ""
	t.ProxyMatchesClash = strings.EqualFold(strings.TrimRight(t.CurrentProxy, "/"), strings.TrimRight(activeProxyURL, "/"))

	switch {
	case t.ProxyMatchesClash:
		t.StatusText = "已就绪（~/.codex/.env 环境变量已同步）"
	case t.CurrentProxy != "":
		t.StatusText = "代理端口与当前客户端不一致"
	default:
		t.StatusText = "未配置 ~/.codex/.env"
	}
	noteUserSwitch(cfg, &t)
	return t
}

func noteUserSwitch(cfg configstore.AppConfig, t *AppTargetInfo) {
	t.EnabledByUser = cfg.IsAppEnabled(t.ID)
	if !t.EnabledByUser {
		t.StatusText = "未托管，保持默认设置"
	}
}

func findExecutable(regKeywords []string, directCandidates []string, driveRelativePaths []string) string {
	for _, p := range directCandidates {
		if fileExists(p) {
			return p
		}
	}

	for d := 'C'; d <= 'Z'; d++ {
		root := fmt.Sprintf("%c:\\", d)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		for _, rel := range driveRelativePaths {
			cand := filepath.Join(root, rel)
			if fileExists(cand) {
				return cand
			}
		}
	}

	regRoots := []struct {
		root registry.Key
		path string
	}{
		{registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.LOCAL_MACHINE, `Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
	}

	for _, rr := range regRoots {
		k, err := registry.OpenKey(rr.root, rr.path, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		subkeys, _ := k.ReadSubKeyNames(-1)
		k.Close()

		for _, sub := range subkeys {
			sk, err := registry.OpenKey(rr.root, rr.path+`\`+sub, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			dispName, _, _ := sk.GetStringValue("DisplayName")
			matched := false
			for _, kw := range regKeywords {
				if strings.Contains(strings.ToLower(dispName), strings.ToLower(kw)) {
					matched = true
					break
				}
			}
			if matched {
				icon, _, _ := sk.GetStringValue("DisplayIcon")
				icon = strings.Trim(strings.Split(icon, ",")[0], `" `)
				if strings.HasSuffix(strings.ToLower(icon), ".exe") && fileExists(icon) {
					sk.Close()
					return icon
				}
				loc, _, _ := sk.GetStringValue("InstallLocation")
				loc = strings.Trim(loc, `" `)
				if loc != "" {
					for _, kw := range regKeywords {
						exeCand := filepath.Join(loc, strings.ReplaceAll(kw, " ", "")+".exe")
						if fileExists(exeCand) {
							sk.Close()
							return exeCand
						}
					}
				}
			}
			sk.Close()
		}
	}
	return ""
}

func readWindowsUserEnvProxy() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	if v, _, err := k.GetStringValue("HTTPS_PROXY"); err == nil && v != "" {
		return v
	}
	if v, _, err := k.GetStringValue("HTTP_PROXY"); err == nil && v != "" {
		return v
	}
	return ""
}

func desktopShortcutExists(shortcutName string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	return fileExists(filepath.Join(home, "Desktop", shortcutName))
}

func containsInt(slice []int, target int) bool {
	for _, v := range slice {
		if v == target {
			return true
		}
	}
	return false
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dirExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
