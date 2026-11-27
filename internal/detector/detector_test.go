package detector

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"proxy-box/internal/configstore"
)

func TestIsHTTPProxyPortDistinguishesHTTPFromSocks(t *testing.T) {
	// 1. Start a mock HTTP server that speaks HTTP
	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer httpLn.Close()
	go func() {
		_ = http.Serve(httpLn, http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			rw.WriteHeader(http.StatusBadGateway)
		}))
	}()
	httpPort := httpLn.Addr().(*net.TCPAddr).Port

	reach, speaksHTTP := isHTTPProxyPort("127.0.0.1", httpPort)
	if !reach || !speaksHTTP {
		t.Fatalf("expected HTTP listener (%d) to report reach=true, speaksHTTP=true; got %v, %v", httpPort, reach, speaksHTTP)
	}

	// 2. Start a mock SOCKS5-only listener that rejects non-SOCKS5 bytes
	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socksLn.Close()
	go func() {
		for {
			c, err := socksLn.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte{0x05, 0xFF})
			_ = c.Close()
		}
	}()
	socksPort := socksLn.Addr().(*net.TCPAddr).Port

	reachSocks, speaksHTTPSocks := isHTTPProxyPort("127.0.0.1", socksPort)
	if !reachSocks || speaksHTTPSocks {
		t.Fatalf("expected SOCKS-only listener (%d) to report reach=true, speaksHTTP=false; got %v, %v", socksPort, reachSocks, speaksHTTPSocks)
	}
}

func TestSelectActiveClientAutoPriority(t *testing.T) {
	clients := []ProxyClientInfo{
		{ID: "clash_verge", Installed: true, Running: false, PortReachable: false},
		{ID: "clash_mi", Installed: false, Running: false},
		{ID: "v2rayn", Installed: true, Running: true, PortReachable: true, HTTPVerified: true, MixedPort: 10808},
	}
	picked := selectActiveClient("auto", clients, 0)
	if picked.ID != "v2rayn" {
		t.Fatalf("expected running v2rayn to be auto-selected, got %s", picked.ID)
	}

	forced := selectActiveClient("clash_verge", clients, 0)
	if forced.ID != "clash_verge" {
		t.Fatalf("expected forced clash_verge, got %s", forced.ID)
	}
}

func TestSnapshotKeepsEachMihomoPID(t *testing.T) {
	procs := map[string]*procDetail{}
	noteProcess(procs, "mihomo.exe", 11, `D:\ProxyBox\core\mihomo.exe`, []int{27890, 27891})
	noteProcess(procs, "mihomo.exe", 22, `D:\Clash Mi\mihomo.exe`, []int{7890})

	d := procs["mihomo.exe"]
	if d == nil || len(d.Instances) != 2 {
		t.Fatalf("instances = %+v", d)
	}
	if d.Instances[0].Path == d.Instances[1].Path {
		t.Fatalf("paths mixed: %+v", d.Instances)
	}
	if d.Instances[0].PID == d.Instances[1].PID {
		t.Fatal("pids mixed")
	}
	ours := portsMatching(d, func(path string) bool { return strings.Contains(path, "ProxyBox") })
	clash := portsMatching(d, func(path string) bool { return strings.Contains(strings.ToLower(path), "clash") })
	if strings.Join(ints(ours), ",") != "27890,27891" {
		t.Fatalf("builtin ports = %v", ours)
	}
	if strings.Join(ints(clash), ",") != "7890" {
		t.Fatalf("clash ports = %v", clash)
	}
	if containsInt(ours, 7890) || containsInt(clash, 27890) {
		t.Fatalf("ports mixed ours=%v clash=%v", ours, clash)
	}

	live := snapshotProcessesAndPorts()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	self := live[strings.ToLower(filepath.Base(exe))]
	if self == nil {
		t.Fatal("current process missing from snapshot")
	}
	pid := uint32(os.Getpid())
	var found bool
	for _, inst := range self.Instances {
		if inst.PID == pid {
			found = true
			if inst.Path == "" {
				t.Fatal("current process has no path")
			}
			if !samePath(inst.Path, exe) {
				t.Fatalf("path = %s, exe = %s", inst.Path, exe)
			}
		}
	}
	if !found {
		t.Fatalf("pid %d not stored on its own instance", pid)
	}
}

func TestBuiltinRunningFollowsOurPathOnly(t *testing.T) {
	const secret = "detector-secret-marker"
	settings := configstore.CoreSettings{
		MixedPort:       27890,
		ControllerPort:  27891,
		AIDedicatedPort: 27892,
		Secret:          secret,
		Mode:            "rule",
	}
	paths := []string{`D:\ProxyBox\core\mihomo.exe`}
	noProbe := func(string, int) (bool, bool) { return false, false }

	clashOnly := map[string]*procDetail{}
	noteProcess(clashOnly, "mihomo.exe", 7, `D:\Clash Mi\mihomo.exe`, []int{7890})
	stopped := assembleBuiltin(clashOnly, paths, settings, builtinYAML{}, noProbe)
	if stopped.Running || stopped.CoreRunning || stopped.DedicatedPortActive {
		t.Fatalf("clash mihomo counted as builtin: %+v", stopped)
	}
	if stopped.ID != "builtin" || stopped.RuleSupportMode != "builtin" {
		t.Fatalf("identity = %+v", stopped)
	}
	if stopped.ActiveProfileName != "" || stopped.ActiveProfileUID != "" {
		t.Fatalf("stopped profile name %q uid %q", stopped.ActiveProfileName, stopped.ActiveProfileUID)
	}
	if stopped.MainPort != 27890 || stopped.DedicatedPort != 27892 {
		t.Fatalf("ports main=%d dedicated=%d", stopped.MainPort, stopped.DedicatedPort)
	}
	if stopped.ExternalController != "127.0.0.1:27891" || stopped.Secret != secret {
		t.Fatalf("controller=%s secret=%s", stopped.ExternalController, stopped.Secret)
	}

	both := map[string]*procDetail{}
	noteProcess(both, "mihomo.exe", 7, `D:\Clash Mi\mihomo.exe`, []int{7890})
	noteProcess(both, "mihomo.exe", 8, `D:\ProxyBox\core\mihomo.exe`, []int{27890, 27891, 27892})
	probe := func(host string, port int) (bool, bool) {
		if host == "127.0.0.1" && port == 27892 {
			return true, true
		}
		return false, false
	}
	running := assembleBuiltin(both, paths, settings, builtinYAML{}, probe)
	if !running.Running || !running.CoreRunning {
		t.Fatal("our mihomo was not running")
	}
	if running.ExePath != `D:\ProxyBox\core\mihomo.exe` {
		t.Fatalf("exe = %s", running.ExePath)
	}
	if !running.DedicatedPortActive || running.MainPort != 27890 || running.DedicatedPort != 27892 {
		t.Fatalf("ports active=%v main=%d dedicated=%d", running.DedicatedPortActive, running.MainPort, running.DedicatedPort)
	}
	if running.ProxyURL != "http://127.0.0.1:27892" {
		t.Fatalf("proxy url = %s", running.ProxyURL)
	}
	if running.ActiveProfileName != "内置订阅配置" || running.ActiveProfileUID != "" {
		t.Fatalf("profile name %q uid %q", running.ActiveProfileName, running.ActiveProfileUID)
	}
	raw, err := json.Marshal(running)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("secret leaked: %s", raw)
	}
}

func TestBuiltinOrder(t *testing.T) {
	vergeInstalled := ProxyClientInfo{ID: "clash_verge", Name: "Clash Verge", Installed: true}
	vergeRunning := ProxyClientInfo{
		ID: "clash_verge", Name: "Clash Verge", Installed: true, Running: true,
		PortReachable: true, HTTPVerified: true, MixedPort: 7897,
	}
	builtinOff := ProxyClientInfo{ID: "builtin", Name: "内置内核", Installed: true}
	builtinOn := ProxyClientInfo{ID: "builtin", Name: "内置内核", Installed: true, Running: true}

	if got := selectActiveClient("auto", []ProxyClientInfo{vergeRunning, builtinOn}, 0); got.ID != "builtin" {
		t.Fatalf("running builtin lost to verge, got %s", got.ID)
	}
	if got := selectActiveClient("auto", []ProxyClientInfo{vergeInstalled, builtinOff}, 0); got.ID != "clash_verge" {
		t.Fatalf("stopped builtin outranked installed verge, got %s", got.ID)
	}
	if got := selectActiveClient("auto", []ProxyClientInfo{vergeRunning, builtinOff}, 0); got.ID != "clash_verge" {
		t.Fatalf("stopped builtin outranked running verge, got %s", got.ID)
	}
	if got := selectActiveClient("builtin", []ProxyClientInfo{vergeRunning, builtinOff}, 0); got.ID != "builtin" {
		t.Fatalf("preferred builtin = %s", got.ID)
	}
	onlyBuiltin := []ProxyClientInfo{
		{ID: "clash_verge"},
		{ID: "clash_mi"},
		{ID: "v2rayn"},
		{ID: "builtin", Installed: true},
	}
	if got := selectActiveClient("auto", onlyBuiltin, 0); got.ID != "builtin" {
		t.Fatalf("installed builtin should remain when nothing else is installed, got %s", got.ID)
	}
}

func TestFastConfigStampSeesCoreFiles(t *testing.T) {
	base := t.TempDir()
	configstore.SetBaseDir(base)
	t.Cleanup(func() { configstore.SetBaseDir("") })
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)

	coreDir := filepath.Join(local, "ProxyBox", "core")
	prov := filepath.Join(coreDir, "providers")
	if err := os.MkdirAll(prov, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(coreDir, "config.yaml"), []byte("mixed-port: 27890\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "core.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "subscriptions.json"), []byte("[]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := FastConfigStamp(configstore.AppConfig{})
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(prov, "abc.txt"), []byte("vmess://x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	after := FastConfigStamp(configstore.AppConfig{})
	if before == after {
		t.Fatalf("stamp ignored providers: %s", before)
	}
	for _, part := range []string{"core.json", "subscriptions.json", "config.yaml"} {
		if !strings.Contains(before, "|") {
			t.Fatalf("stamp empty before write, missing %s: %s", part, before)
		}
	}
	if before == "" {
		t.Fatal("empty stamp")
	}
}

func TestIDESyncSkipsBuiltinWithoutNodes(t *testing.T) {
	builtin := ProxyClientInfo{ID: "builtin", ActiveNode: "DIRECT"}
	if IDESyncAllowed(builtin, nil) {
		t.Fatal("empty builtin was allowed to rewrite IDE configs")
	}
	if IDESyncAllowed(builtin, []configstore.Subscription{{Enabled: true, NodeCount: 0}}) {
		t.Fatal("zero-node subscription was treated as usable")
	}
	if !IDESyncAllowed(builtin, []configstore.Subscription{{Enabled: true, NodeCount: 3}}) {
		t.Fatal("subscription with nodes was blocked")
	}
	picked := builtin
	picked.ActiveNode = "local-http"
	if !IDESyncAllowed(picked, nil) {
		t.Fatal("selected node was blocked")
	}
	verge := ProxyClientInfo{ID: "clash_verge"}
	if !IDESyncAllowed(verge, nil) {
		t.Fatal("external client was blocked")
	}
}

func portsMatching(d *procDetail, match func(string) bool) []int {
	var out []int
	for _, inst := range d.Instances {
		if !match(inst.Path) {
			continue
		}
		for _, port := range inst.Ports {
			if !containsInt(out, port) {
				out = append(out, port)
			}
		}
	}
	return out
}

func ints(v []int) []string {
	out := make([]string, len(v))
	for i, n := range v {
		out[i] = strconv.Itoa(n)
	}
	return out
}
