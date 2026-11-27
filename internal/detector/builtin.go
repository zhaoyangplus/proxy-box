package detector

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"proxy-box/internal/clashscript"
	"proxy-box/internal/configstore"
	"proxy-box/internal/mihomo"

	"gopkg.in/yaml.v3"
)

const (
	builtinMixedPort      = 27890
	builtinControllerPort = 27891
	builtinAIPort         = 27892
)

type builtinYAML struct {
	MixedPort          int    `yaml:"mixed-port"`
	ExternalController string `yaml:"external-controller"`
	Secret             string `yaml:"secret"`
	Listeners          []struct {
		Name string `yaml:"name"`
		Port int    `yaml:"port"`
	} `yaml:"listeners"`
}

// IDESyncAllowed reports whether the live watcher may rewrite IDE proxy settings.
// A builtin client with no subscription nodes and no selected proxy node is refused.
func IDESyncAllowed(client ProxyClientInfo, subs []configstore.Subscription) bool {
	if client.ID != "builtin" {
		return true
	}
	for _, sub := range subs {
		if sub.Enabled && sub.NodeCount > 0 {
			return true
		}
	}
	switch strings.TrimSpace(client.ActiveNode) {
	case "", "DIRECT", "PROXY", mihomo.AIGroupName, mihomo.AIAutoName, "⚡ AUTO":
		return false
	default:
		return true
	}
}

func detectBuiltin(procs map[string]*procDetail) ProxyClientInfo {
	settings := peekCoreSettings()
	paths := builtinBinaryPaths()
	doc := readBuiltinYAML(filepath.Join(proxyBoxCoreDir(), "config.yaml"))
	return assembleBuiltin(procs, paths, settings, doc, isHTTPProxyPort)
}

func assembleBuiltin(procs map[string]*procDetail, paths []string, settings configstore.CoreSettings, doc builtinYAML, probe func(host string, port int) (bool, bool)) ProxyClientInfo {
	mixed := positiveOr(settings.MixedPort, builtinMixedPort)
	controller := positiveOr(settings.ControllerPort, builtinControllerPort)
	aiPort := positiveOr(settings.AIDedicatedPort, builtinAIPort)
	secret := settings.Secret

	info := ProxyClientInfo{
		ID:              "builtin",
		Name:            "内置内核",
		ProxyHost:       "127.0.0.1",
		RuleSupportMode: "builtin",
		ConfigDir:       proxyBoxCoreDir(),
		SysProxyEnabled: settings.SystemProxy,
	}
	for _, path := range paths {
		if fileExists(path) {
			info.Installed = true
			if info.ExePath == "" {
				info.ExePath = path
			}
		}
	}

	inst, running := ourMihomo(procs, paths)
	info.Running = running
	info.CoreRunning = running
	if running {
		info.Installed = true
		info.ExePath = inst.Path
		if doc.MixedPort > 0 {
			mixed = doc.MixedPort
		}
		if doc.ExternalController != "" {
			controller = portFromController(doc.ExternalController, controller)
		}
		if p := dedicatedPort(doc); p > 0 {
			aiPort = p
		}
		if doc.Secret != "" {
			secret = doc.Secret
		}
	}

	info.MainPort = mixed
	info.MixedPort = mixed
	info.DedicatedPort = aiPort
	info.ExternalController = fmt.Sprintf("127.0.0.1:%d", controller)
	info.Secret = secret
	if st, err := os.Stat(filepath.Join(info.ConfigDir, "config.yaml")); err == nil {
		info.ConfigModUnix = st.ModTime().UnixNano()
	}

	proxyPort := mixed
	if running {
		if reach, httpOK := probe(info.ProxyHost, aiPort); reach && httpOK {
			info.DedicatedPortActive = true
			info.PortReachable = true
			info.HTTPVerified = true
			proxyPort = aiPort
		} else {
			info.PortReachable, info.HTTPVerified = probe(info.ProxyHost, mixed)
		}
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
	info.ProxyURL = fmt.Sprintf("http://%s:%d", info.ProxyHost, proxyPort)
	return info
}

func ourMihomo(procs map[string]*procDetail, paths []string) (procInstance, bool) {
	if procs == nil {
		return procInstance{}, false
	}
	d := procs["mihomo.exe"]
	if d == nil {
		return procInstance{}, false
	}
	for _, inst := range d.Instances {
		if pathIsBuiltin(inst.Path, paths) {
			return inst, true
		}
	}
	return procInstance{}, false
}

func pathIsBuiltin(path string, paths []string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	for _, candidate := range paths {
		if samePath(path, candidate) {
			return true
		}
	}
	return false
}

func builtinBinaryPaths() []string {
	paths := make([]string, 0, 2)
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "mihomo.exe"))
	}
	paths = append(paths, filepath.Join(proxyBoxCoreDir(), "mihomo.exe"))
	return paths
}

func proxyBoxCoreDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "ProxyBox", "core")
}

func peekCoreSettings() configstore.CoreSettings {
	cfg := configstore.CoreSettings{
		MixedPort:       builtinMixedPort,
		ControllerPort:  builtinControllerPort,
		AIDedicatedPort: builtinAIPort,
		Mode:            "rule",
	}
	base, err := configstore.BaseDir()
	if err != nil {
		return cfg
	}
	data, err := os.ReadFile(filepath.Join(base, "core.json"))
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, &cfg)
	if cfg.MixedPort == 0 {
		cfg.MixedPort = builtinMixedPort
	}
	if cfg.ControllerPort == 0 {
		cfg.ControllerPort = builtinControllerPort
	}
	if cfg.AIDedicatedPort == 0 {
		cfg.AIDedicatedPort = builtinAIPort
	}
	if cfg.Mode == "" {
		cfg.Mode = "rule"
	}
	return cfg
}

func readBuiltinYAML(path string) builtinYAML {
	raw, err := os.ReadFile(path)
	if err != nil {
		return builtinYAML{}
	}
	var doc builtinYAML
	if yaml.Unmarshal(raw, &doc) != nil {
		return builtinYAML{}
	}
	doc.ExternalController = strings.TrimSpace(doc.ExternalController)
	doc.ExternalController = strings.TrimPrefix(doc.ExternalController, "http://")
	doc.ExternalController = strings.TrimPrefix(doc.ExternalController, "https://")
	return doc
}

func dedicatedPort(doc builtinYAML) int {
	for _, ln := range doc.Listeners {
		if ln.Name == clashscript.DedicatedListenerName && ln.Port > 0 {
			return ln.Port
		}
	}
	return 0
}

func portFromController(addr string, fallback int) int {
	addr = strings.TrimSpace(addr)
	if i := strings.LastIndex(addr, ":"); i >= 0 && i+1 < len(addr) {
		var port int
		if _, err := fmt.Sscanf(addr[i+1:], "%d", &port); err == nil && port > 0 {
			return port
		}
	}
	return fallback
}

func samePath(a, b string) bool {
	return strings.EqualFold(cleanPath(a), cleanPath(b))
}

func cleanPath(path string) string {
	path = strings.TrimPrefix(path, `\\?\`)
	return filepath.Clean(path)
}

func positiveOr(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

func stampProvidersDir(b *strings.Builder, path string) {
	st, err := os.Stat(path)
	if err != nil || !st.IsDir() {
		return
	}
	fmt.Fprintf(b, "dir:%d:%d|", st.ModTime().UnixNano(), st.Size())
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		full := filepath.Join(path, name)
		if info, err := os.Stat(full); err == nil && !info.IsDir() {
			fmt.Fprintf(b, "%d:%d|", info.ModTime().UnixNano(), info.Size())
		}
	}
}
