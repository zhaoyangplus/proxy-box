package core

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"proxy-box/internal/clashscript"
	"proxy-box/internal/mihomo"
)

const (
	DefaultMixedPort       = 27890
	DefaultControllerPort  = 27891
	DefaultAIDedicatedPort = 27892
	DefaultSpeedTestPort   = 27893

	ModeRule   = "rule"
	ModeGlobal = "global"
	ModeDirect = "direct"

	ProxyGroupName   = "PROXY"
	BrowsingAutoName = "⚡ AUTO"
	DirectName       = "DIRECT"

	healthURL      = mihomo.TestURL
	healthInterval = 300
)

var providerIDRe = regexp.MustCompile(`^[a-z0-9_-]+$`)

// ConfigParams is the input Render and Manager accept.
// GeoReady is true only when geoip.metadb and GeoSite.dat are already in the core directory.
type ConfigParams struct {
	MixedPort       int
	ControllerPort  int
	AIDedicatedPort int
	SpeedTestPort   int
	Secret          string
	Mode            string
	BypassCN        bool
	GeoReady        bool
	EnabledAIApps   []string
}

// ProviderSpec is one subscription file the core should load.
// Filter is copied into the mihomo proxy-provider filter.
type ProviderSpec struct {
	ID      string
	Name    string
	Filter  string
	Enabled bool
}

// Ports are the listeners a running core actually bound.
type Ports struct {
	Mixed       int
	Controller  int
	AIDedicated int
	SpeedTest   int
}

// Render writes a mihomo config. Ports of 0 become the defaults.
// Geo rules are emitted only when BypassCN and GeoReady are both true.
func Render(params ConfigParams, providers []ProviderSpec) (string, error) {
	params, err := normalizeParams(params)
	if err != nil {
		return "", err
	}
	enabled, err := enabledProviders(providers)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "mixed-port: %d\n", params.MixedPort)
	b.WriteString("allow-lan: false\n")
	fmt.Fprintf(&b, "bind-address: %s\n", yamlSingle("127.0.0.1"))
	fmt.Fprintf(&b, "mode: %s\n", params.Mode)
	b.WriteString("find-process-mode: always\n")
	b.WriteString("unified-delay: true\n")
	b.WriteString("tcp-concurrent: true\n")
	b.WriteString("keep-alive-interval: 30\n")
	fmt.Fprintf(&b, "external-controller: %s\n", yamlSingle(fmt.Sprintf("127.0.0.1:%d", params.ControllerPort)))
	fmt.Fprintf(&b, "secret: %s\n", yamlSingle(params.Secret))
	b.WriteString("dns:\n  enable: false\n")
	b.WriteString("profile:\n  store-selected: true\n")
	b.WriteString("listeners:\n")
	writeListener(&b, clashscript.DedicatedListenerName, params.AIDedicatedPort, true, mihomo.AIGroupName)
	writeListener(&b, clashscript.SpeedTestListenerName, params.SpeedTestPort, false, clashscript.SpeedTestGroupName)
	writeProviders(&b, enabled)
	writeGroups(&b, enabled)
	b.WriteString("rules:\n")
	for _, rule := range renderRules(params.EnabledAIApps, params.BypassCN, params.GeoReady) {
		fmt.Fprintf(&b, "  - %s\n", yamlSingle(rule))
	}
	return b.String(), nil
}

func normalizeParams(p ConfigParams) (ConfigParams, error) {
	if p.MixedPort == 0 {
		p.MixedPort = DefaultMixedPort
	}
	if p.ControllerPort == 0 {
		p.ControllerPort = DefaultControllerPort
	}
	if p.AIDedicatedPort == 0 {
		p.AIDedicatedPort = DefaultAIDedicatedPort
	}
	if p.SpeedTestPort == 0 {
		p.SpeedTestPort = DefaultSpeedTestPort
	}
	ports := []int{p.MixedPort, p.ControllerPort, p.AIDedicatedPort, p.SpeedTestPort}
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return p, fmt.Errorf("port %d out of range", port)
		}
	}
	seenPort := map[int]bool{}
	for _, port := range ports {
		if seenPort[port] {
			return p, fmt.Errorf("mixed, controller, ai, and speedtest ports must differ")
		}
		seenPort[port] = true
	}
	if strings.ContainsAny(p.Secret, "\r\n") {
		return p, fmt.Errorf("secret contains a newline")
	}
	mode, err := normalizeMode(p.Mode, true)
	if err != nil {
		return p, err
	}
	p.Mode = mode
	return p, nil
}

func normalizeMode(mode string, emptyMeansRule bool) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		if emptyMeansRule {
			return ModeRule, nil
		}
		return "", fmt.Errorf("mode is empty")
	}
	switch mode {
	case ModeRule, ModeGlobal, ModeDirect:
		return mode, nil
	default:
		return "", fmt.Errorf("mode %q", mode)
	}
}

func enabledProviders(in []ProviderSpec) ([]ProviderSpec, error) {
	seen := map[string]bool{}
	var out []ProviderSpec
	for _, p := range in {
		if !p.Enabled {
			continue
		}
		if !providerIDRe.MatchString(p.ID) {
			return nil, fmt.Errorf("provider id %q", p.ID)
		}
		if seen[p.ID] {
			return nil, fmt.Errorf("duplicate provider id %q", p.ID)
		}
		seen[p.ID] = true
		out = append(out, p)
	}
	return out, nil
}

func writeProviders(b *strings.Builder, providers []ProviderSpec) {
	if len(providers) == 0 {
		return
	}
	b.WriteString("proxy-providers:\n")
	prefix := len(providers) > 1
	for _, p := range providers {
		fmt.Fprintf(b, "  %s:\n    type: file\n    path: providers/%s.txt\n", yamlSingle(p.ID), p.ID)
		if p.Filter != "" {
			fmt.Fprintf(b, "    filter: %s\n", yamlSingle(p.Filter))
		}
		if prefix {
			name := p.Name
			if name == "" {
				name = p.ID
			}
			fmt.Fprintf(b, "    override:\n      additional-prefix: %s\n", yamlSingle("["+name+"] "))
		}
	}
}

func writeListener(b *strings.Builder, name string, port int, udp bool, proxy string) {
	udpText := "false"
	if udp {
		udpText = "true"
	}
	fmt.Fprintf(b, "  - name: %s\n    type: mixed\n    port: %d\n    listen: %s\n    udp: %s\n    proxy: %s\n",
		yamlSingle(name), port, yamlSingle("127.0.0.1"), udpText, yamlSingle(proxy))
}

func writeGroups(b *strings.Builder, providers []ProviderSpec) {
	ids := make([]string, len(providers))
	for i, p := range providers {
		ids[i] = p.ID
	}
	fallback := len(providers) == 0
	b.WriteString("proxy-groups:\n")
	writeGroup(b, ProxyGroupName, "select", []string{BrowsingAutoName, DirectName}, ids, "", clashscript.InfoNodeExcludeFilter)
	autoProxies := []string(nil)
	if fallback {
		autoProxies = []string{DirectName}
	}
	writeGroup(b, BrowsingAutoName, "url-test", autoProxies, ids, healthURL, clashscript.InfoNodeExcludeFilter)
	aiProxies := []string{mihomo.AIAutoName}
	if fallback {
		aiProxies = append(aiProxies, DirectName)
	}
	writeGroup(b, mihomo.AIGroupName, "select", aiProxies, ids, "", clashscript.RestrictedRegionFilter)
	aiAutoProxies := []string(nil)
	if fallback {
		aiAutoProxies = []string{DirectName}
	}
	writeGroup(b, mihomo.AIAutoName, "url-test", aiAutoProxies, ids, healthURL, clashscript.RestrictedRegionFilter)
	writeGroup(b, clashscript.SpeedTestGroupName, "select", []string{DirectName}, ids, "", clashscript.InfoNodeExcludeFilter)
}

func writeGroup(b *strings.Builder, name, typ string, proxies, use []string, url, excludeFilter string) {
	fmt.Fprintf(b, "  - name: %s\n    type: %s\n", yamlSingle(name), typ)
	if len(proxies) > 0 {
		b.WriteString("    proxies:\n")
		for _, p := range proxies {
			fmt.Fprintf(b, "      - %s\n", yamlSingle(p))
		}
	}
	if len(use) > 0 {
		b.WriteString("    use:\n")
		for _, id := range use {
			fmt.Fprintf(b, "      - %s\n", yamlSingle(id))
		}
	}
	if url != "" {
		fmt.Fprintf(b, "    url: %s\n    interval: %d\n    lazy: true\n", yamlSingle(url), healthInterval)
	}
	if excludeFilter != "" {
		fmt.Fprintf(b, "    exclude-filter: %s\n", yamlSingle(excludeFilter))
	}
}

func renderRules(enabled []string, bypassCN, geoReady bool) []string {
	rules := []string{
		"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR6,::1/128,DIRECT,no-resolve",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
		"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
		"PROCESS-NAME,ProxyBox.exe," + mihomo.AIGroupName,
	}
	seenProc := map[string]bool{"ProxyBox.exe": true}
	seenDomain := map[string]bool{}
	for _, id := range orderedEnabledApps(enabled) {
		set := clashscript.AppRules[id]
		for _, name := range set.Processes {
			if seenProc[name] {
				continue
			}
			seenProc[name] = true
			rules = append(rules, "PROCESS-NAME,"+name+","+mihomo.AIGroupName)
		}
		for _, domain := range set.DomainSuffixes {
			if seenDomain[domain] {
				continue
			}
			seenDomain[domain] = true
			rules = append(rules, "DOMAIN-SUFFIX,"+domain+","+mihomo.AIGroupName)
		}
	}
	if bypassCN && geoReady {
		rules = append(rules,
			"GEOSITE,cn,DIRECT",
			"GEOSITE,geolocation-!cn,PROXY",
			"GEOIP,CN,DIRECT",
		)
	}
	rules = append(rules, "MATCH,"+ProxyGroupName)
	return rules
}

// orderedEnabledApps returns known app ids in AppIDs order, once each.
func orderedEnabledApps(ids []string) []string {
	on := map[string]bool{}
	for _, id := range ids {
		on[id] = true
	}
	var out []string
	for _, id := range clashscript.AppIDs {
		if on[id] {
			out = append(out, id)
		}
	}
	return out
}

func yamlSingle(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// AllocatePorts returns four distinct free TCP ports, starting at the requested values.
// A zero request starts at the default port and moves upward when a port is taken.
func AllocatePorts(p ConfigParams) (Ports, error) {
	mixedStart := p.MixedPort
	if mixedStart == 0 {
		mixedStart = DefaultMixedPort
	}
	ctrlStart := p.ControllerPort
	if ctrlStart == 0 {
		ctrlStart = DefaultControllerPort
	}
	aiStart := p.AIDedicatedPort
	if aiStart == 0 {
		aiStart = DefaultAIDedicatedPort
	}
	speedStart := p.SpeedTestPort
	if speedStart == 0 {
		speedStart = DefaultSpeedTestPort
	}
	mixed, err := firstFree(mixedStart, nil)
	if err != nil {
		return Ports{}, err
	}
	ctrl, err := firstFree(ctrlStart, map[int]bool{mixed: true})
	if err != nil {
		return Ports{}, err
	}
	ai, err := firstFree(aiStart, map[int]bool{mixed: true, ctrl: true})
	if err != nil {
		return Ports{}, err
	}
	speed, err := firstFree(speedStart, map[int]bool{mixed: true, ctrl: true, ai: true})
	if err != nil {
		return Ports{}, err
	}
	return Ports{Mixed: mixed, Controller: ctrl, AIDedicated: ai, SpeedTest: speed}, nil
}

func firstFree(start int, skip map[int]bool) (int, error) {
	if start < 1 || start > 65535 {
		return 0, fmt.Errorf("port %d out of range", start)
	}
	for i := 0; i < 200; i++ {
		port := start + i
		if port > 65535 {
			break
		}
		if skip[port] {
			continue
		}
		if portFree(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port from %d", start)
}

func portFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
