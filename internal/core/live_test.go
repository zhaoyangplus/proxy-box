package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLiveMihomo(t *testing.T) {
	if os.Getenv("PROXYBOX_LIVE") != "1" {
		t.Skip("set PROXYBOX_LIVE=1 to run the sidecar against the downloaded mihomo zip")
	}
	if os.Getenv("PROXYBOX_JOB_CHILD") == "1" {
		runJobChild()
		return
	}
	zipPath := mustEnv(t, "PROXYBOX_ZIP")
	providerSrc := mustEnv(t, "PROXYBOX_PROVIDER")
	geoIP := mustEnv(t, "PROXYBOX_GEOIP")
	geoSite := mustEnv(t, "PROXYBOX_GEOSITE")
	root := mustEnv(t, "PROXYBOX_LIVE_ROOT")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}

	sum := hashFile(t, zipPath)
	if !strings.EqualFold(sum, AssetSHA256) {
		t.Fatalf("zip sha256 %s", sum)
	}
	t.Logf("zip sha256 %s", sum)

	importDir := filepath.Join(root, "import")
	binary, err := ImportBinary(zipPath, importDir)
	if err != nil {
		t.Fatal(err)
	}
	if !fileHasMZ(binary) {
		t.Fatal("imported exe has no MZ")
	}

	var pids []int
	t.Cleanup(func() { killOwned(root, pids) })

	funcDir := filepath.Join(root, "func")
	if err := copyFile(binary, filepath.Join(funcDir, BinaryFileName)); err != nil {
		t.Fatal(err)
	}
	readyMS, ws, pid := runFunctional(t, funcDir, &pids)
	t.Logf("ready_ms %d working_set %d pid %d", readyMS, ws, pid)

	if os.Getenv("PROXYBOX_SKIP_DOWNLOAD") != "1" {
		dlDir := filepath.Join(root, "download")
		ctx, cancel := context.WithTimeout(context.Background(), 16*time.Minute)
		var last int64
		var logged int64
		got, err := EnsureBinary(ctx, dlDir, []string{"http://127.0.0.1:7897"}, func(n, total int64) {
			last = n
			if n == 0 || n-logged >= 4<<20 || (total > 0 && n >= total) {
				t.Logf("download %d/%d", n, total)
				logged = n
			}
		})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.EqualFold(filepath.Dir(got), dlDir) && !strings.EqualFold(filepath.Dir(got), absPath(t, dlDir)) {
			t.Fatalf("download installed to %s", got)
		}
		if !fileHasMZ(got) {
			t.Fatal("downloaded exe has no MZ")
		}
		t.Logf("ensure_binary %s bytes %d resumes %d", got, last, downloadResumes.Load())
	}

	runGeo(t, filepath.Join(root, "geo"), binary, providerSrc, geoIP, geoSite, &pids)
	runJobKill(t, filepath.Join(root, "job"), binary)
	if left := mihomoUnder(root); len(left) != 0 {
		t.Fatalf("mihomo still running: %s", left)
	}
}

func runFunctional(t *testing.T, dir string, pids *[]int) (int64, uint64, int) {
	t.Helper()
	m := NewManager(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	start := time.Now()
	st, err := m.Start(ctx, filepath.Join(dir, BinaryFileName), ConfigParams{Secret: "pr2-secret", Mode: ModeRule}, nil)
	cancel()
	ready := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	*pids = append(*pids, st.PID)
	t.Cleanup(func() { _ = m.Stop() })
	if st.Ports.Mixed < 27890 || st.Ports.AIDedicated > 27899 || st.Ports.Controller < 27890 || st.Ports.Controller > 27899 {
		t.Fatalf("ports outside 27890-27899: %+v", st.Ports)
	}
	if !strings.Contains(st.Version, "v1.19.34") {
		t.Fatalf("version %s", st.Version)
	}
	if ready > 1500*time.Millisecond {
		t.Errorf("ready %s exceeds 1.5s", ready)
	}
	if n := visibleWindowCount(st.PID); n != 0 {
		t.Fatalf("visible windows %d", n)
	}
	time.Sleep(3 * time.Second)
	ws := workingSet(t, st.PID)
	if ws > 60*1024*1024 {
		t.Errorf("working set %d exceeds 60MB", ws)
	}
	logText := readText(filepath.Join(dir, LogFileName))
	t.Logf("log_marker %s", lastLineContaining(logText, "Initial configuration complete"))

	hits := servePing(t)
	out, err := curlProxy(st.Ports.Mixed, hits.addr+"/ping")
	if err != nil || !strings.Contains(out, "pong") || hits.n != 1 {
		t.Fatalf("mixed direct out=%q hits=%d err=%v", out, hits.n, err)
	}
	ext, extErr := curlProxy(st.Ports.Mixed, "http://www.gstatic.com/generate_204")
	t.Logf("generate_204 err=%v body=%q", extErr, trimBody(ext))

	before := st.PID
	reloadCtx, reloadCancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = m.Reload(reloadCtx, ConfigParams{Secret: "pr2-secret", Mode: ModeRule}, nil)
	reloadCancel()
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Status().PID; got != before || !processAlive(before) {
		t.Fatalf("reload pid %d -> %d alive %v", before, got, processAlive(before))
	}

	modeCtx, modeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := m.SwitchMode(modeCtx, ModeGlobal); err != nil {
		modeCancel()
		t.Fatal(err)
	}
	modeCancel()
	if mode := getMode(t, st.Ports.Controller, "pr2-secret"); mode != ModeGlobal {
		t.Fatalf("controller mode %s", mode)
	}
	disk := readText(filepath.Join(dir, ConfigFileName))
	if !strings.Contains(disk, "mode: global\n") {
		t.Fatal("config.yaml mode was not persisted")
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for processAlive(before) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if processAlive(before) {
		t.Fatal("stop left mihomo running")
	}
	return ready.Milliseconds(), ws, before
}

func runGeo(t *testing.T, dir, binary, providerSrc, geoIP, geoSite string, pids *[]int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "providers"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(providerSrc, filepath.Join(dir, "providers", "demo.txt")); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(geoIP, filepath.Join(dir, "geoip.metadb")); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(geoSite, filepath.Join(dir, "GeoSite.dat")); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	st, err := m.Start(ctx, binary, ConfigParams{
		Secret:   "pr2-secret",
		Mode:     ModeRule,
		BypassCN: true,
		GeoReady: true,
	}, []ProviderSpec{{ID: "demo", Name: "Demo", Enabled: true}})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	*pids = append(*pids, st.PID)
	defer m.Stop()
	if st.Ports.Mixed < 27890 || st.Ports.AIDedicated > 27899 {
		t.Fatalf("geo ports %+v", st.Ports)
	}
	if !strings.Contains(st.Version, "v1.19.34") {
		t.Fatalf("geo version %s", st.Version)
	}
	logText := readText(filepath.Join(dir, LogFileName))
	if !strings.Contains(logText, "Load GeoSite") {
		t.Fatalf("geo log missing Load GeoSite:\n%s", tail(logText))
	}
	if strings.Contains(logText, "start download") {
		t.Fatalf("geo core tried to download:\n%s", tail(logText))
	}
	t.Logf("geo pid %d ports %+v", st.PID, st.Ports)
}

func runJobKill(t *testing.T, dir, binary string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "host.log")
	logf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logf.Close()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestLiveMihomo$", "-test.v")
	cmd.Env = append(os.Environ(),
		"PROXYBOX_LIVE=1",
		"PROXYBOX_JOB_CHILD=1",
		"PROXYBOX_JOB_DIR="+dir,
		"PROXYBOX_JOB_BIN="+binary,
	)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.Process != nil && processAlive(cmd.Process.Pid) {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()
	marker := filepath.Join(dir, "child.txt")
	deadline := time.Now().Add(12 * time.Second)
	var hostPID, childPID, visible int
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(marker)
		if err == nil {
			_, _ = fmt.Sscanf(string(raw), "%d %d %d", &hostPID, &childPID, &visible)
			if hostPID != 0 && childPID != 0 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if hostPID == 0 || childPID == 0 {
		t.Fatalf("job child did not report\n%s", readText(logPath))
	}
	if visible != 0 {
		t.Fatalf("mihomo visible windows %d", visible)
	}
	killOut, killErr := exec.Command("taskkill", "/F", "/PID", strconv.Itoa(hostPID)).CombinedOutput()
	if killErr != nil {
		t.Fatalf("taskkill %v %s", killErr, killOut)
	}
	waitDead(t, childPID)
	t.Logf("job kill host %d child %d", hostPID, childPID)
}

func runJobChild() {
	dir := os.Getenv("PROXYBOX_JOB_DIR")
	binary := os.Getenv("PROXYBOX_JOB_BIN")
	m := NewManager(dir)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	st, err := m.Start(ctx, binary, ConfigParams{
		Secret:          "pr2-job",
		Mode:            ModeRule,
		MixedPort:       27896,
		ControllerPort:  27897,
		AIDedicatedPort: 27898,
	}, nil)
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if st.Ports.Mixed < 27890 || st.Ports.AIDedicated > 27899 {
		fmt.Fprintf(os.Stderr, "ports %+v\n", st.Ports)
		_ = m.Stop()
		os.Exit(1)
	}
	marker := fmt.Sprintf("%d %d %d\n", os.Getpid(), st.PID, visibleWindowCount(st.PID))
	if err := os.WriteFile(filepath.Join(dir, "child.txt"), []byte(marker), 0644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	select {}
}

type pingServer struct {
	addr string
	n    int
}

func servePing(t *testing.T) *pingServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ps := &pingServer{addr: "http://" + ln.Addr().String()}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.n++
		_, _ = io.WriteString(w, "pong")
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { _ = srv.Close() })
	return ps
}

func curlProxy(port int, target string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "curl.exe", "--noproxy", "*", "-sS", "-x", fmt.Sprintf("http://127.0.0.1:%d", port), target)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func getMode(t *testing.T, port int, secret string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/configs", port), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := controllerHTTP.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var doc struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("configs %s", body)
	}
	return doc.Mode
}

func workingSet(t *testing.T, pid int) uint64 {
	t.Helper()
	cmd := exec.Command("powershell", "-NoProfile", "-Command", fmt.Sprintf("(Get-Process -Id %d).WorkingSet64", pid))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func hashFile(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func copyFile(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return installStream(dest, in)
}

func installStream(dest string, r io.Reader) error {
	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return moveReplace(tmp, dest)
}

func mustEnv(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("%s is empty", key)
	}
	return v
}

func absPath(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func readText(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func lastLineContaining(text, needle string) string {
	var found string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			found = strings.TrimSpace(line)
		}
	}
	return found
}

func tail(s string) string {
	if len(s) > 2000 {
		return s[len(s)-2000:]
	}
	return s
}

func trimBody(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 180 {
		return s[:180]
	}
	return s
}

func waitDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Fatalf("pid %d still alive", pid)
	}
}

func killOwned(root string, pids []int) {
	for _, pid := range pids {
		if processAlive(pid) {
			_ = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
		}
	}
	for _, line := range strings.Split(mihomoUnder(root), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err == nil {
			_ = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
		}
	}
}

func mihomoUnder(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	script := fmt.Sprintf(`Get-CimInstance Win32_Process -Filter "Name = 'mihomo.exe'" | Where-Object { $_.ExecutablePath -like '%s*' } | ForEach-Object { "$($_.ProcessId) $($_.ExecutablePath)" }`, strings.ReplaceAll(abs, "'", "''"))
	out, err := exec.Command("powershell", "-NoProfile", "-Command", script).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
