package core

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"proxy-box/internal/clashscript"
	"proxy-box/internal/mihomo"

	"gopkg.in/yaml.v3"
)

func TestRenderSpecAndGolden(t *testing.T) {
	if clashscript.AIGroupName != mihomo.AIGroupName || clashscript.AIAutoName != mihomo.AIAutoName {
		t.Fatalf("group names diverged")
	}
	cases := []struct {
		name      string
		params    ConfigParams
		providers []ProviderSpec
		bypass    bool
		geo       bool
	}{
		{name: "zero", params: ConfigParams{Secret: "s3cret", Mode: "rule"}},
		{
			name:   "one",
			params: ConfigParams{Secret: "s3cret", Mode: "rule"},
			providers: []ProviderSpec{
				{ID: "alpha", Name: "Alpha", Filter: `(?i)美国|Japan`, Enabled: true},
				{ID: "../nope", Name: "skip", Enabled: false},
			},
		},
		{
			name:   "multi",
			params: ConfigParams{Secret: "s3cret", Mode: "global"},
			providers: []ProviderSpec{
				{ID: "alpha", Name: "Alpha", Filter: "hk", Enabled: true},
				{ID: "beta", Name: "Beta", Enabled: false},
				{ID: "gamma", Name: "Gamma Air", Enabled: true},
			},
		},
		{name: "bypass", params: ConfigParams{Secret: "s3cret", Mode: "rule", BypassCN: true, GeoReady: true}, bypass: true, geo: true},
		{name: "bypass-off", params: ConfigParams{Secret: "s3cret", Mode: "rule", BypassCN: true, GeoReady: false}},
		{name: "geo-without-bypass", params: ConfigParams{Secret: "s3cret", Mode: "direct", BypassCN: false, GeoReady: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Render(tc.params, tc.providers)
			if err != nil {
				t.Fatal(err)
			}
			assertRendered(t, got, tc.params, tc.providers, tc.bypass && tc.geo)
			compareGolden(t, tc.name+".yaml", got)
		})
	}
}

func TestRenderRejectsBadInput(t *testing.T) {
	if _, err := Render(ConfigParams{Mode: "auto"}, nil); err == nil {
		t.Fatal("expected bad mode")
	}
	if _, err := Render(ConfigParams{MixedPort: 1, ControllerPort: 1, AIDedicatedPort: 2}, nil); err == nil {
		t.Fatal("expected duplicate ports")
	}
	if _, err := Render(ConfigParams{}, []ProviderSpec{{ID: "../x", Enabled: true}}); err == nil {
		t.Fatal("expected bad id")
	}
	got, err := Render(ConfigParams{Secret: "a'b"}, []ProviderSpec{{ID: "a", Name: "A", Filter: "it's", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	doc := parseDoc(t, got)
	if doc.Secret != "a'b" {
		t.Fatalf("secret %q", doc.Secret)
	}
	if doc.ProxyProviders["a"].Filter != "it's" {
		t.Fatalf("filter %q", doc.ProxyProviders["a"].Filter)
	}
}

func TestAllocatePortsSkipsTaken(t *testing.T) {
	base := freeRun(t, 4)
	ln, err := net.Listen("tcp", addr(base))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got, err := AllocatePorts(ConfigParams{MixedPort: base, ControllerPort: base + 1, AIDedicatedPort: base + 2})
	if err != nil {
		t.Fatal(err)
	}
	if got.Mixed != base+1 || got.Controller != base+2 || got.AIDedicated != base+3 {
		t.Fatalf("ports %+v", got)
	}
	def, err := AllocatePorts(ConfigParams{})
	if err != nil {
		t.Fatal(err)
	}
	if def.Mixed == 0 || def.Controller == 0 || def.AIDedicated == 0 {
		t.Fatalf("defaults %+v", def)
	}
	if def.Mixed == def.Controller || def.Controller == def.AIDedicated || def.Mixed == def.AIDedicated {
		t.Fatalf("defaults overlap %+v", def)
	}
}

func assertRendered(t *testing.T, raw string, params ConfigParams, providers []ProviderSpec, wantGeo bool) {
	t.Helper()
	doc := parseDoc(t, raw)
	if doc.AllowLan || doc.BindAddress != "127.0.0.1" || doc.FindProcessMode != "always" {
		t.Fatalf("bind/lan/process %+v", doc)
	}
	if doc.DNS.Enable || !doc.Profile.StoreSelected {
		t.Fatalf("dns/profile %+v %+v", doc.DNS, doc.Profile)
	}
	if len(doc.Listeners) != 1 || doc.Listeners[0].Name != clashscript.DedicatedListenerName || doc.Listeners[0].Proxy != mihomo.AIGroupName {
		t.Fatalf("listener %+v", doc.Listeners)
	}
	mode := params.Mode
	if mode == "" {
		mode = ModeRule
	}
	if doc.Mode != mode {
		t.Fatalf("mode %s", doc.Mode)
	}
	var enabled []ProviderSpec
	for _, p := range providers {
		if p.Enabled {
			enabled = append(enabled, p)
		}
	}
	if len(enabled) == 0 {
		if strings.Contains(raw, "proxy-providers:") || len(doc.ProxyProviders) != 0 {
			t.Fatal("expected no providers")
		}
	}
	if len(enabled) == 1 {
		p := doc.ProxyProviders[enabled[0].ID]
		if p.Type != "file" || p.Path != "providers/"+enabled[0].ID+".txt" || p.Filter != enabled[0].Filter || p.Override.AdditionalPrefix != "" {
			t.Fatalf("provider %+v", p)
		}
	}
	if len(enabled) > 1 {
		for _, spec := range enabled {
			p := doc.ProxyProviders[spec.ID]
			name := spec.Name
			if name == "" {
				name = spec.ID
			}
			if p.Override.AdditionalPrefix != "["+name+"] " {
				t.Fatalf("prefix %q", p.Override.AdditionalPrefix)
			}
			if p.Filter != spec.Filter {
				t.Fatalf("filter %q", p.Filter)
			}
		}
		if _, ok := doc.ProxyProviders["beta"]; ok {
			t.Fatal("disabled provider present")
		}
	}
	if len(doc.ProxyGroups) != 4 {
		t.Fatalf("groups %d", len(doc.ProxyGroups))
	}
	if doc.ProxyGroups[1].Name != BrowsingAutoName || doc.ProxyGroups[1].Type != "url-test" {
		t.Fatalf("auto %+v", doc.ProxyGroups[1])
	}
	if doc.ProxyGroups[2].Name != mihomo.AIGroupName || doc.ProxyGroups[3].Name != mihomo.AIAutoName || doc.ProxyGroups[3].Type != "url-test" {
		t.Fatalf("ai groups %+v %+v", doc.ProxyGroups[2], doc.ProxyGroups[3])
	}
	if doc.ProxyGroups[3].Name != mihomo.AIAutoName {
		t.Fatal(doc.ProxyGroups[3].Name)
	}
	if doc.ProxyGroups[2].ExcludeFilter != clashscript.RestrictedRegionFilter || doc.ProxyGroups[3].ExcludeFilter != clashscript.RestrictedRegionFilter {
		t.Fatal("missing exclude-filter")
	}
	if doc.ProxyGroups[1].ExcludeFilter != "" {
		t.Fatal("browsing auto has exclude-filter")
	}
	if len(enabled) == 0 {
		if !contains(doc.ProxyGroups[0].Proxies, DirectName) || !contains(doc.ProxyGroups[1].Proxies, DirectName) {
			t.Fatal("missing DIRECT fallback")
		}
	} else if len(doc.ProxyGroups[1].Use) != len(enabled) {
		t.Fatalf("use %+v", doc.ProxyGroups[1].Use)
	}
	wantRules := renderRules(wantGeo, wantGeo)
	if params.BypassCN && params.GeoReady {
		wantRules = renderRules(true, true)
	} else {
		wantRules = renderRules(false, false)
	}
	if strings.Join(doc.Rules, "\n") != strings.Join(wantRules, "\n") {
		t.Fatalf("rules\n%s", strings.Join(doc.Rules, "\n"))
	}
	if len(doc.Rules) < 6 || doc.Rules[0] != "IP-CIDR,127.0.0.0/8,DIRECT,no-resolve" || doc.Rules[len(doc.Rules)-1] != "MATCH,PROXY" {
		t.Fatalf("rule ends %+v", doc.Rules)
	}
	if wantGeo {
		geo := doc.Rules[len(doc.Rules)-4 : len(doc.Rules)]
		want := []string{"GEOSITE,cn,DIRECT", "GEOSITE,geolocation-!cn,PROXY", "GEOIP,CN,DIRECT", "MATCH,PROXY"}
		if strings.Join(geo, "|") != strings.Join(want, "|") {
			t.Fatalf("geo order %v", geo)
		}
	} else if strings.Contains(strings.Join(doc.Rules, "\n"), "GEOSITE") || strings.Contains(strings.Join(doc.Rules, "\n"), "GEOIP") {
		t.Fatal("geo rules present")
	}
}

type rendered struct {
	AllowLan        bool   `yaml:"allow-lan"`
	BindAddress     string `yaml:"bind-address"`
	Mode            string `yaml:"mode"`
	FindProcessMode string `yaml:"find-process-mode"`
	Secret          string `yaml:"secret"`
	DNS             struct {
		Enable bool `yaml:"enable"`
	} `yaml:"dns"`
	Profile struct {
		StoreSelected bool `yaml:"store-selected"`
	} `yaml:"profile"`
	Listeners []struct {
		Name  string `yaml:"name"`
		Proxy string `yaml:"proxy"`
		Port  int    `yaml:"port"`
	} `yaml:"listeners"`
	ProxyProviders map[string]struct {
		Type     string `yaml:"type"`
		Path     string `yaml:"path"`
		Filter   string `yaml:"filter"`
		Override struct {
			AdditionalPrefix string `yaml:"additional-prefix"`
		} `yaml:"override"`
	} `yaml:"proxy-providers"`
	ProxyGroups []struct {
		Name          string   `yaml:"name"`
		Type          string   `yaml:"type"`
		Proxies       []string `yaml:"proxies"`
		Use           []string `yaml:"use"`
		ExcludeFilter string   `yaml:"exclude-filter"`
	} `yaml:"proxy-groups"`
	Rules []string `yaml:"rules"`
}

func parseDoc(t *testing.T, raw string) rendered {
	t.Helper()
	var doc rendered
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if want != got {
		t.Fatalf("golden %s\n%s", name, firstDiff(want, got))
	}
}

func firstDiff(want, got string) string {
	wb, gb := strings.Split(want, "\n"), strings.Split(got, "\n")
	n := len(wb)
	if len(gb) > n {
		n = len(gb)
	}
	for i := 0; i < n; i++ {
		var w, g string
		if i < len(wb) {
			w = wb[i]
		}
		if i < len(gb) {
			g = gb[i]
		}
		if w != g {
			return strings.TrimSpace(w + "\n!=\n" + g)
		}
	}
	return "length"
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func freeRun(t *testing.T, n int) int {
	t.Helper()
	for base := 20000; base < 60000; base += n {
		var lns []net.Listener
		ok := true
		for i := 0; i < n; i++ {
			ln, err := net.Listen("tcp", addr(base+i))
			if err != nil {
				ok = false
				break
			}
			lns = append(lns, ln)
		}
		for _, ln := range lns {
			_ = ln.Close()
		}
		if ok {
			return base
		}
	}
	t.Fatal("no free port run")
	return 0
}

func addr(port int) string {
	return "127.0.0.1:" + strconv.Itoa(port)
}
