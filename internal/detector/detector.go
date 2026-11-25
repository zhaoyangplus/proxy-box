package detector

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"cursor-box/internal/clashscript"
	"cursor-box/internal/configstore"
	"cursor-box/internal/configurator"
	"cursor-box/internal/mihomo"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"gopkg.in/yaml.v3"
)

type ProxyClientInfo struct {
	ID                  string `json:"id"`   // "clash_verge", "clash_mi", "v2rayn"
	Name                string `json:"name"` // "Clash Verge", "Clash Mi", "v2rayN"
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
	ControllerPipe      string `json:"-"`
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
	ShortcutExists        bool   `json:"shortcutExists"`
	StatusText            string `json:"statusText"`
}

type ClashScriptStatus struct {
	ScriptPath      string `json:"scriptPath"`
	Injected        bool   `json:"injected"`
	ActiveInRuntime bool   `json:"activeInRuntime"`
	HasBackup       bool   `json:"hasBackup"`
}

type SystemSnapshot struct {
	PreferredClient string                `json:"preferredClient"`
	ActiveClient    ProxyClientInfo       `json:"activeClient"`
	Clients         []ProxyClientInfo     `json:"clients"`
	Clash           ProxyClientInfo       `json:"clash"`
	Targets         []AppTargetInfo       `json:"targets"`
	ClashScript     ClashScriptStatus     `json:"clashScript"`
	UserEnvProxy    string                `json:"userEnvProxy"`
	AppConfig       configstore.AppConfig `json:"appConfig"`
	CheckedAt       string                `json:"checkedAt"`
}

type procDetail struct {
	Path  string
	PIDs  []uint32
	Ports []int
}

// OptimizeClientNode triggers Mihomo optimal compliant node selection ONLY on CursorBox's isolated 🤖 AI-IDE group.
func OptimizeClientNode(c ProxyClientInfo) (*mihomo.OptimizeResult, error) {
	if c.ID == "v2rayn" {
		return nil, fmt.Errorf("v2rayN 未暴露 Mihomo 控制接口，请在 v2rayN 主界面选择美/日/新等非香港节点")
	}
	mc := mihomo.NewClient(c.ControllerPipe, c.ExternalController, c.Secret)
	if !mc.Available() {
		return nil, fmt.Errorf("未能连接到 %s 内核控制接口，请确认 %s 已运行", c.Name, c.Name)
	}
	return mc.OptimizeBestNode()
}

// ListClientNodes returns all selectable nodes in CursorBox's isolated 🤖 AI-IDE group.
func ListClientNodes(c ProxyClientInfo, runDelayTest bool) (*mihomo.NodeListResult, error) {
	if c.ID == "v2rayn" {
		return nil, fmt.Errorf("v2rayN 暂不支持双向独立节点选择，请使用 Clash Verge 或 Clash Mi 启用独立通道")
	}
	mc := mihomo.NewClient(c.ControllerPipe, c.ExternalController, c.Secret)
	if !mc.Available() {
		return nil, fmt.Errorf("未能连接到 %s 内核控制接口，请确认 %s 已运行", c.Name, c.Name)
	}
	return mc.ListAINodes(runDelayTest)
}

// SelectClientNode switches ONLY CursorBox's isolated 🤖 AI-IDE group to targetNode.
func SelectClientNode(c ProxyClientInfo, targetNode string) (*mihomo.LiveNodeStatus, error) {
	if c.ID == "v2rayn" {
		return nil, fmt.Errorf("v2rayN 暂不支持双向独立节点选择")
	}
	mc := mihomo.NewClient(c.ControllerPipe, c.ExternalController, c.Secret)
	if !mc.Available() {
		return nil, fmt.Errorf("未能连接到 %s 内核控制接口", c.Name)
	}
	return mc.SelectAINode(targetNode)
}

// FastConfigStamp inspects modification times & sizes of key proxy config files in <1ms
// so the background watcher detects subscription/port/config changes immediately with near-zero CPU.
func FastConfigStamp(cfg configstore.AppConfig) string {
	appData := os.Getenv("APPDATA")
	files := []string{
		filepath.Join(appData, "io.github.clash-verge-rev.clash-verge-rev", "profiles.yaml"),
		filepath.Join(appData, "io.github.clash-verge-rev.clash-verge-rev", "verge.yaml"),
		filepath.Join(appData, "io.github.clash-verge-rev.clash-verge-rev", "clash-verge.yaml"),
		filepath.Join(appData, "io.github.zzzgydi.clash-verge", "profiles.yaml"),
		filepath.Join(appData, "io.github.zzzgydi.clash-verge", "verge.yaml"),
		filepath.Join(appData, "clashmi", "clashmi", "profiles.json"),
		filepath.Join(appData, "clashmi", "clashmi", "setting.json"),
		filepath.Join(appData, "clashmi", "clashmi", "service_core_runtime_profile.yaml"),
		filepath.Join(appData, "clashmi", "profiles.json"),
	}
	if cfg.CustomClashDir != "" {
		files = append(files,
			filepath.Join(cfg.CustomClashDir, "profiles.yaml"),
			filepath.Join(cfg.CustomClashDir, "verge.yaml"),
			filepath.Join(cfg.CustomClashDir, "clash-verge.yaml"),
		)
	}
	if cfg.CustomClashMiDir != "" {
		files = append(files,
			filepath.Join(cfg.CustomClashMiDir, "profiles.json"),
			filepath.Join(cfg.CustomClashMiDir, "service_core_runtime_profile.yaml"),
		)
	}
	if cfg.CustomV2rayNExe != "" {
		gdir := filepath.Join(filepath.Dir(cfg.CustomV2rayNExe), "guiConfigs")
		files = append(files,
			filepath.Join(gdir, "guiNConfig.json"),
			filepath.Join(gdir, "guiNDB.db"),
		)
	}

	var b strings.Builder
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			fmt.Fprintf(&b, "%d:%d|", st.ModTime().UnixNano(), st.Size())
		}
	}
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
			d, ok := result[exeName]
			if !ok {
				d = &procDetail{}
				result[exeName] = d
			}
			d.PIDs = append(d.PIDs, pe.ProcessID)
			if d.Path == "" {
				d.Path = queryProcessPath(pe.ProcessID)
			}
			if ports, has := pidPorts[pe.ProcessID]; has {
				for _, p := range ports {
					if !containsInt(d.Ports, p) {
						d.Ports = append(d.Ports, p)
					}
				}
			}
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return result
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

func readWindowsSystemProxy() (enabled bool, host string, port int) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return false, "", 0
	}
	defer k.Close()

	en, _, _ := k.GetIntegerValue("ProxyEnable")
	srv, _, _ := k.GetStringValue("ProxyServer")
	enabled = en == 1
	srv = strings.TrimSpace(srv)
	if srv == "" {
		return enabled, "", 0
	}
	for _, part := range strings.Split(srv, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "socks=") {
			continue
		}
		if idx := strings.IndexByte(part, '='); idx >= 0 {
			part = part[idx+1:]
		}
		part = strings.TrimPrefix(strings.TrimPrefix(part, "http://"), "https://")
		h, pStr, err := net.SplitHostPort(part)
		if err == nil {
			if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
				return enabled, h, p
			}
		}
	}
	return enabled, "", 0
}

func DetectAll() SystemSnapshot {
	cfg := configstore.LoadConfig()
	st := configstore.LoadState()
	procs := snapshotProcessesAndPorts()
	sysProxyEn, sysHost, sysPort := readWindowsSystemProxy()

	vergeClient := detectClashVerge(cfg, procs)
	clashMiClient := detectClashMi(cfg, procs, sysProxyEn, sysHost, sysPort)
	v2rayNClient := detectV2rayN(cfg, procs, sysProxyEn, sysHost, sysPort)

	clients := []ProxyClientInfo{vergeClient, clashMiClient, v2rayNClient}
	active := selectActiveClient(cfg.PreferredClient, clients, cfg.CustomPortOverride)

	if cfg.CustomPortOverride > 0 {
		active.MixedPort = cfg.CustomPortOverride
		active.ProxyURL = fmt.Sprintf("http://%s:%d", active.ProxyHost, active.MixedPort)
		active.PortReachable, active.HTTPVerified = isHTTPProxyPort(active.ProxyHost, active.MixedPort)
	}

	scriptStatus := ClashScriptStatus{
		ScriptPath:      clashscript.ScriptFilePath(vergeClient.ConfigDir),
		Injected:        vergeClient.RuleInjected,
		ActiveInRuntime: vergeClient.RuleActiveInRuntime,
		HasBackup:       clashscript.HasBackup(),
	}

	targets := []AppTargetInfo{
		detectCursor(cfg, st, procs, active.ProxyURL),
		detectAntigravity(cfg, st, procs, active.ProxyURL),
		detectClaudeCode(st, procs, active.ProxyURL),
		detectCodex(cfg, st, procs, active.ProxyURL),
	}

	return SystemSnapshot{
		PreferredClient: cfg.PreferredClient,
		ActiveClient:    active,
		Clients:         clients,
		Clash:           active,
		Targets:         targets,
		ClashScript:     scriptStatus,
		UserEnvProxy:    readWindowsUserEnvProxy(),
		AppConfig:       cfg,
		CheckedAt:       time.Now().Format("15:04:05"),
	}
}

func selectActiveClient(preferred string, clients []ProxyClientInfo, portOverride int) ProxyClientInfo {
	if preferred != "" && preferred != "auto" {
		for _, c := range clients {
			if c.ID == preferred {
				return c
			}
		}
	}
	for _, c := range clients {
		if c.Running && c.PortReachable && c.HTTPVerified {
			return c
		}
	}
	for _, c := range clients {
		if c.Running && c.PortReachable {
			return c
		}
	}
	for _, c := range clients {
		if c.Running {
			return c
		}
	}
	for _, c := range clients {
		if c.Installed {
			return c
		}
	}
	return clients[0]
}

func latestModTime(paths ...string) int64 {
	var maxT int64
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil {
			if t := st.ModTime().UnixNano(); t > maxT {
				maxT = t
			}
		}
	}
	return maxT
}

func detectClashVerge(cfg configstore.AppConfig, procs map[string]*procDetail) ProxyClientInfo {
	info := ProxyClientInfo{
		ID:              "clash_verge",
		Name:            "Clash Verge",
		ProxyHost:       "127.0.0.1",
		MixedPort:       7897,
		RuleSupportMode: "verge_auto",
	}

	appData := os.Getenv("APPDATA")
	candidates := []string{}
	if cfg.CustomClashDir != "" {
		candidates = append(candidates, cfg.CustomClashDir)
	}
	candidates = append(candidates,
		filepath.Join(appData, "io.github.clash-verge-rev.clash-verge-rev"),
		filepath.Join(appData, "io.github.zzzgydi.clash-verge"),
	)

	for _, dir := range candidates {
		if dirExists(dir) {
			info.ConfigDir = dir
			info.Installed = true
			break
		}
	}

	for _, pname := range []string{"clash-verge.exe", "clash verge.exe"} {
		if d, ok := procs[pname]; ok {
			info.Running = true
			if d.Path != "" && info.ExePath == "" {
				info.ExePath = d.Path
			}
		}
	}
	for _, cname := range []string{"verge-mihomo.exe", "verge-mihomo-alpha.exe"} {
		if _, ok := procs[cname]; ok {
			info.CoreRunning = true
			info.Running = true
		}
	}

	if cfg.CustomClashExe != "" && fileExists(cfg.CustomClashExe) {
		info.ExePath = cfg.CustomClashExe
		info.Installed = true
	} else if info.ExePath == "" {
		info.ExePath = findExecutable(
			[]string{"Clash Verge"},
			[]string{
				filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Clash Verge", "clash-verge.exe"),
				filepath.Join(os.Getenv("ProgramFiles"), "Clash Verge", "clash-verge.exe"),
			},
			[]string{`Clash Verge\clash-verge.exe`, `ClashVerge\clash-verge.exe`, `tizi\clash-verge.exe`},
		)
		if info.ExePath != "" {
			info.Installed = true
		}
	}

	if info.ConfigDir != "" {
		info.ConfigModUnix = latestModTime(
			filepath.Join(info.ConfigDir, "verge.yaml"),
			filepath.Join(info.ConfigDir, "profiles.yaml"),
			filepath.Join(info.ConfigDir, "clash-verge.yaml"),
		)

		if raw, err := os.ReadFile(filepath.Join(info.ConfigDir, "verge.yaml")); err == nil {
			var v struct {
				ProxyHost         string `yaml:"proxy_host"`
				VergeMixedPort    int    `yaml:"verge_mixed_port"`
				VergePort         int    `yaml:"verge_port"`
				EnableTunMode     bool   `yaml:"enable_tun_mode"`
				EnableSystemProxy bool   `yaml:"enable_system_proxy"`
			}
			if yaml.Unmarshal(raw, &v) == nil {
				if v.ProxyHost != "" {
					info.ProxyHost = v.ProxyHost
				}
				if v.VergeMixedPort > 0 {
					info.MixedPort = v.VergeMixedPort
				} else if v.VergePort > 0 {
					info.MixedPort = v.VergePort
				}
				info.TunEnabled = v.EnableTunMode
				info.SysProxyEnabled = v.EnableSystemProxy
			}
		}

		for _, fn := range []string{"clash-verge.yaml", "config.yaml"} {
			if raw, err := os.ReadFile(filepath.Join(info.ConfigDir, fn)); err == nil {
				var c struct {
					MixedPort              int    `yaml:"mixed-port"`
					Port                   int    `yaml:"port"`
					ExternalController     string `yaml:"external-controller"`
					ExternalControllerPipe string `yaml:"external-controller-pipe"`
					Secret                 string `yaml:"secret"`
				}
				if yaml.Unmarshal(raw, &c) == nil {
					if info.MixedPort == 0 && c.MixedPort > 0 {
						info.MixedPort = c.MixedPort
					}
					if c.ExternalControllerPipe != "" && info.ControllerPipe == "" {
						info.ControllerPipe = c.ExternalControllerPipe
					}
					if c.ExternalController != "" && info.ExternalController == "" {
						info.ExternalController = c.ExternalController
						info.Secret = c.Secret
					}
				}
			}
		}

		if info.ControllerPipe == "" && info.CoreRunning {
			info.ControllerPipe = `\\.\pipe\verge-mihomo`
		}

		if raw, err := os.ReadFile(filepath.Join(info.ConfigDir, "profiles.yaml")); err == nil {
			var p struct {
				Current string `yaml:"current"`
				Items   []struct {
					UID  string `yaml:"uid"`
					Name string `yaml:"name"`
					File string `yaml:"file"`
				} `yaml:"items"`
			}
			if yaml.Unmarshal(raw, &p) == nil {
				info.ActiveProfileUID = p.Current
				for _, item := range p.Items {
					if item.UID == p.Current {
						if item.Name != "" {
							info.ActiveProfileName = item.Name
						} else {
							info.ActiveProfileName = item.File
						}
						break
					}
				}
			}
		}

		info.RuleInjected = clashscript.IsInjected(info.ConfigDir)
		info.RuleActiveInRuntime = clashscript.IsActiveInRuntime(info.ConfigDir)
	}

	info.MainPort = info.MixedPort
	info.DedicatedPort = clashscript.VergeDedicatedPort

	// Check if CursorBox's dedicated isolated listener (127.0.0.1:17897 -> 🤖 AI-IDE) is live!
	if info.Running {
		if dReach, dHTTP := isHTTPProxyPort(info.ProxyHost, info.DedicatedPort); dReach && dHTTP {
			info.DedicatedPortActive = true
			info.MixedPort = info.DedicatedPort
			info.PortReachable = true
			info.HTTPVerified = true
		}
	}
	if !info.DedicatedPortActive {
		info.PortReachable, info.HTTPVerified = isHTTPProxyPort(info.ProxyHost, info.MainPort)
	}
	info.ProxyURL = fmt.Sprintf("http://%s:%d", info.ProxyHost, info.MixedPort)

	// Query live Mihomo node & group status over Named Pipe or HTTP controller
	if info.Running && (info.ControllerPipe != "" || info.ExternalController != "") {
		mc := mihomo.NewClient(info.ControllerPipe, info.ExternalController, info.Secret)
		live := mc.GetLiveStatus()
		info.ControllerReachable = live.Connected
		info.IsolatedGroup = live.IsolatedGroup
		info.ActiveGroup = live.GroupName
		info.ActiveNode = live.ResolvedNode
		info.ActiveNodeDelayMs = live.ResolvedDelayMs
		info.ActiveNodeCompliant = live.Compliant
		info.ExternalGroup = live.ExternalGroup
		info.ExternalNode = live.ExternalNode
	}

	return info
}

func detectClashMi(cfg configstore.AppConfig, procs map[string]*procDetail, sysEn bool, sysHost string, sysPort int) ProxyClientInfo {
	info := ProxyClientInfo{
		ID:                "clash_mi",
		Name:              "Clash Mi",
		ProxyHost:         "127.0.0.1",
		MixedPort:         7890,
		ActiveProfileName: "Mihomo 内核配置",
		RuleSupportMode:   "clashmi_override",
	}

	var procPorts []int
	for _, pname := range []string{"clashmi.exe", "clashmiservice.exe"} {
		if d, ok := procs[pname]; ok {
			info.Running = true
			if pname == "clashmiservice.exe" {
				info.CoreRunning = true
			}
			if d.Path != "" && info.ExePath == "" && pname == "clashmi.exe" {
				info.ExePath = d.Path
			}
			procPorts = append(procPorts, d.Ports...)
		}
	}
	if d, ok := procs["mihomo.exe"]; ok && strings.Contains(strings.ToLower(d.Path), "clash") {
		info.CoreRunning = true
		procPorts = append(procPorts, d.Ports...)
	}

	if cfg.CustomClashMiExe != "" && fileExists(cfg.CustomClashMiExe) {
		info.ExePath = cfg.CustomClashMiExe
		info.Installed = true
	} else if info.ExePath == "" {
		info.ExePath = findExecutable(
			[]string{"Clash Mi", "clashmi"},
			[]string{
				filepath.Join(os.Getenv("ProgramFiles"), "Clash Mi", "clashmi.exe"),
				filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Clash Mi", "clashmi.exe"),
				filepath.Join(os.Getenv("LOCALAPPDATA"), "Clash Mi", "clashmi.exe"),
			},
			[]string{`Clash Mi\clashmi.exe`, `clashmi\clashmi.exe`, `tizi\Clash Mi\clashmi.exe`},
		)
	}
	if info.ExePath != "" {
		info.Installed = true
	}

	appData := os.Getenv("APPDATA")
	dirCandidates := []string{}
	if cfg.CustomClashMiDir != "" {
		dirCandidates = append(dirCandidates, cfg.CustomClashMiDir)
	}
	if info.ExePath != "" {
		dirCandidates = append(dirCandidates, filepath.Join(filepath.Dir(info.ExePath), "data"))
	}
	dirCandidates = append(dirCandidates,
		filepath.Join(appData, "clashmi", "clashmi"),
		filepath.Join(appData, "clashmi"),
		filepath.Join(appData, "com.karingx.clashmi"),
	)

	for _, dir := range dirCandidates {
		if dirExists(dir) {
			info.ConfigDir = dir
			info.Installed = true
			break
		}
	}

	portFromConfig := 0
	if info.ConfigDir != "" {
		info.ConfigModUnix = latestModTime(
			filepath.Join(info.ConfigDir, "profiles.json"),
			filepath.Join(info.ConfigDir, "setting.json"),
			filepath.Join(info.ConfigDir, "service_core_runtime_profile.yaml"),
		)

		// Read active subscription name from profiles.json
		if raw, err := os.ReadFile(filepath.Join(info.ConfigDir, "profiles.json")); err == nil {
			var pm struct {
				CurrentID string `json:"current_id"`
				Profiles  []struct {
					ID     string `json:"id"`
					Remark string `json:"remark"`
				} `json:"profiles"`
			}
			if json.Unmarshal(raw, &pm) == nil && pm.CurrentID != "" {
				info.ActiveProfileUID = pm.CurrentID
				for _, p := range pm.Profiles {
					if p.ID == pm.CurrentID && p.Remark != "" {
						info.ActiveProfileName = p.Remark
						break
					}
				}
			}
		}

		for _, fn := range []string{"service_core_runtime_profile.yaml", "clash.yaml", "core.yaml", "config.yaml", "service_core_patch.yaml"} {
			raw, err := os.ReadFile(filepath.Join(info.ConfigDir, fn))
			if err != nil {
				continue
			}
			if bytes.Contains(raw, []byte(clashscript.AIGroupName)) {
				info.RuleActiveInRuntime = true
			}
			var m struct {
				MixedPort          int    `yaml:"mixed-port"`
				Port               int    `yaml:"port"`
				ExternalController string `yaml:"external-controller"`
				Secret             string `yaml:"secret"`
				Tun                struct {
					Enable bool `yaml:"enable"`
				} `yaml:"tun"`
			}
			if yaml.Unmarshal(raw, &m) == nil {
				if m.MixedPort > 0 && portFromConfig == 0 {
					portFromConfig = m.MixedPort
				} else if m.Port > 0 && portFromConfig == 0 {
					portFromConfig = m.Port
				}
				if m.Tun.Enable {
					info.TunEnabled = true
				}
				if m.ExternalController != "" && info.ExternalController == "" {
					info.ExternalController = m.ExternalController
					info.Secret = m.Secret
				}
			}
		}

		if raw, err := os.ReadFile(filepath.Join(info.ConfigDir, "setting.json")); err == nil {
			var sj map[string]any
			if json.Unmarshal(raw, &sj) == nil {
				for _, k := range []string{"mixed_port", "mixedPort", "http_port", "port"} {
					if v, ok := sj[k].(float64); ok && int(v) > 0 && portFromConfig == 0 {
						portFromConfig = int(v)
					}
				}
				if autoSys, ok := sj["auto_set_system_proxy"].(bool); ok {
					info.SysProxyEnabled = autoSys
				}
			}
		}
	}

	var candidatePorts []int
	if portFromConfig > 0 {
		candidatePorts = append(candidatePorts, portFromConfig)
	}
	sort.Ints(procPorts)
	for _, p := range procPorts {
		if p != 2744 && p != 2745 && p != 9090 && !containsInt(candidatePorts, p) {
			candidatePorts = append(candidatePorts, p)
		}
	}
	if info.Running && sysEn && sysPort > 0 && !containsInt(candidatePorts, sysPort) {
		candidatePorts = append(candidatePorts, sysPort)
		info.SysProxyEnabled = true
	}
	for _, defPort := range []int{7890, 7888, 3067} {
		if !containsInt(candidatePorts, defPort) {
			candidatePorts = append(candidatePorts, defPort)
		}
	}

	if portFromConfig > 0 {
		info.MixedPort = portFromConfig
	}

	info.MainPort = info.MixedPort
	info.DedicatedPort = clashscript.ClashMiDedicatedPort

	if info.Running {
		if dReach, dHTTP := isHTTPProxyPort(info.ProxyHost, info.DedicatedPort); dReach && dHTTP {
			info.DedicatedPortActive = true
			info.MixedPort = info.DedicatedPort
			info.PortReachable = true
			info.HTTPVerified = true
		} else {
			for _, p := range candidatePorts {
				reach, httpOK := isHTTPProxyPort(info.ProxyHost, p)
				if reach && httpOK {
					info.MainPort = p
					info.MixedPort = p
					info.PortReachable = true
					info.HTTPVerified = true
					break
				}
			}
		}
		// Query Clash Mi live Mihomo node via external-controller
		if info.ExternalController != "" {
			mc := mihomo.NewClient("", info.ExternalController, info.Secret)
			live := mc.GetLiveStatus()
			info.ControllerReachable = live.Connected
			info.IsolatedGroup = live.IsolatedGroup
			info.ActiveGroup = live.GroupName
			info.ActiveNode = live.ResolvedNode
			info.ActiveNodeDelayMs = live.ResolvedDelayMs
			info.ActiveNodeCompliant = live.Compliant
			info.ExternalGroup = live.ExternalGroup
			info.ExternalNode = live.ExternalNode
		}
	} else if portFromConfig > 0 {
		info.PortReachable, info.HTTPVerified = isHTTPProxyPort(info.ProxyHost, info.MixedPort)
	}

	if base, err := configstore.BaseDir(); err == nil {
		info.RuleInjected = fileExists(filepath.Join(base, "clashmi-ai-override.js"))
	}

	info.ProxyURL = fmt.Sprintf("http://%s:%d", info.ProxyHost, info.MixedPort)
	return info
}

func detectV2rayN(cfg configstore.AppConfig, procs map[string]*procDetail, sysEn bool, sysHost string, sysPort int) ProxyClientInfo {
	info := ProxyClientInfo{
		ID:                "v2rayn",
		Name:              "v2rayN",
		ProxyHost:         "127.0.0.1",
		MixedPort:         10808,
		ActiveProfileName: "活动节点 / 路由规则",
		RuleSupportMode:   "v2rayn_clipboard",
	}

	var corePorts []int
	if d, ok := procs["v2rayn.exe"]; ok {
		info.Running = true
		if d.Path != "" {
			info.ExePath = d.Path
		}
		corePorts = append(corePorts, d.Ports...)
	}
	for _, cname := range []string{"xray.exe", "v2ray.exe", "sing-box.exe"} {
		if d, ok := procs[cname]; ok {
			if info.Running || strings.Contains(strings.ToLower(d.Path), "v2rayn") {
				info.CoreRunning = true
				corePorts = append(corePorts, d.Ports...)
				if info.ExePath == "" && strings.Contains(strings.ToLower(d.Path), "v2rayn") {
					dir := filepath.Dir(d.Path)
					for i := 0; i < 3; i++ {
						cand := filepath.Join(dir, "v2rayN.exe")
						if fileExists(cand) {
							info.ExePath = cand
							break
						}
						dir = filepath.Dir(dir)
					}
				}
			}
		}
	}

	if cfg.CustomV2rayNExe != "" && fileExists(cfg.CustomV2rayNExe) {
		info.ExePath = cfg.CustomV2rayNExe
	} else if info.ExePath == "" {
		info.ExePath = findV2rayNExe()
	}

	if info.ExePath != "" {
		info.Installed = true
		guiCfgDir := filepath.Join(filepath.Dir(info.ExePath), "guiConfigs")
		if dirExists(guiCfgDir) {
			info.ConfigDir = guiCfgDir
		} else {
			info.ConfigDir = filepath.Dir(info.ExePath)
		}
	}

	var candidatePorts []int
	if info.ConfigDir != "" {
		info.ConfigModUnix = latestModTime(
			filepath.Join(info.ConfigDir, "guiNConfig.json"),
			filepath.Join(info.ConfigDir, "guiNDB.db"),
		)

		guiCfgPath := filepath.Join(info.ConfigDir, "guiNConfig.json")
		if raw, err := os.ReadFile(guiCfgPath); err == nil {
			var g struct {
				SysProxyType int `json:"SysProxyType"`
				TunModeItem  struct {
					EnableTun bool `json:"EnableTun"`
				} `json:"TunModeItem"`
				Inbound []struct {
					LocalPort int    `json:"LocalPort"`
					Protocol  string `json:"Protocol"`
				} `json:"Inbound"`
			}
			if json.Unmarshal(raw, &g) == nil {
				info.TunEnabled = g.TunModeItem.EnableTun
				info.SysProxyEnabled = g.SysProxyType == 1

				hasV7DB := fileExists(filepath.Join(info.ConfigDir, "guiNDB.db"))
				for _, inb := range g.Inbound {
					if inb.LocalPort <= 0 {
						continue
					}
					proto := strings.ToLower(inb.Protocol)
					switch proto {
					case "http", "mixed":
						candidatePorts = append([]int{inb.LocalPort}, candidatePorts...)
						info.MixedPort = inb.LocalPort
					case "socks", "socks5":
						if hasV7DB {
							candidatePorts = append(candidatePorts, inb.LocalPort, inb.LocalPort+1)
							info.MixedPort = inb.LocalPort
							info.ModeHint = "v7+ 混合端口"
						} else {
							candidatePorts = append(candidatePorts, inb.LocalPort+1, inb.LocalPort)
							info.MixedPort = inb.LocalPort + 1
							info.ModeHint = "v6 HTTP 端口"
						}
					default:
						candidatePorts = append(candidatePorts, inb.LocalPort, inb.LocalPort+1)
					}
				}
			}
		}
	}

	for _, p := range corePorts {
		if !containsInt(candidatePorts, p) {
			candidatePorts = append(candidatePorts, p)
		}
	}
	if info.Running && sysEn && sysPort > 0 && !containsInt(candidatePorts, sysPort) {
		candidatePorts = append(candidatePorts, sysPort)
	}
	for _, def := range []int{10808, 10809} {
		if !containsInt(candidatePorts, def) {
			candidatePorts = append(candidatePorts, def)
		}
	}

	if info.Running || info.CoreRunning {
		for _, p := range candidatePorts {
			reach, httpOK := isHTTPProxyPort(info.ProxyHost, p)
			if reach && httpOK {
				info.MixedPort = p
				info.PortReachable = true
				info.HTTPVerified = true
				break
			}
		}
	} else if info.Installed {
		info.PortReachable, info.HTTPVerified = isHTTPProxyPort(info.ProxyHost, info.MixedPort)
	}

	if base, err := configstore.BaseDir(); err == nil {
		info.RuleInjected = fileExists(filepath.Join(base, "v2rayn-ai-rules.json"))
	}

	info.MainPort = info.MixedPort
	info.ProxyURL = fmt.Sprintf("http://%s:%d", info.ProxyHost, info.MixedPort)
	return info
}

func findV2rayNExe() string {
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE); err == nil {
		names, _ := k.ReadValueNames(-1)
		for _, name := range names {
			if strings.Contains(strings.ToLower(name), "v2rayn") {
				val, _, err := k.GetStringValue(name)
				if err == nil {
					clean := extractExeFromCommandLine(val)
					if fileExists(clean) {
						k.Close()
						return clean
					}
				}
			}
		}
		k.Close()
	}

	relCandidates := []string{
		`v2rayN\v2rayN.exe`,
		`v2rayN-With-Core\v2rayN.exe`,
		`v2rayN-windows-64\v2rayN.exe`,
		`tizi\v2rayN\v2rayN.exe`,
		`tizi\v2rayN-With-Core\v2rayN.exe`,
		`tools\v2rayN\v2rayN.exe`,
		`Program Files\v2rayN\v2rayN.exe`,
	}
	return findExecutable(
		[]string{"v2rayN"},
		[]string{
			filepath.Join(os.Getenv("LOCALAPPDATA"), "v2rayN", "v2rayN.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "v2rayN", "v2rayN.exe"),
		},
		relCandidates,
	)
}

func extractExeFromCommandLine(cmdLine string) string {
	s := strings.TrimSpace(cmdLine)
	if strings.HasPrefix(s, `"`) {
		end := strings.IndexByte(s[1:], '"')
		if end >= 0 {
			return s[1 : end+1]
		}
	}
	if idx := strings.Index(strings.ToLower(s), ".exe"); idx >= 0 {
		return strings.TrimSpace(s[:idx+4])
	}
	return s
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
	return t
}

func detectClaudeCode(st configstore.StateManifest, procs map[string]*procDetail, activeProxyURL string) AppTargetInfo {
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
	return t
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
