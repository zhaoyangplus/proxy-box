package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestMain(m *testing.M) {
	if os.Getenv("PROXYBOX_FAKE_CORE") == "1" {
		os.Exit(runFakeCore())
	}
	os.Exit(m.Run())
}

func TestLogTruncatesAboveTwoMB(t *testing.T) {
	if maxLogBytes != 2*1024*1024 {
		t.Fatalf("cap %d", maxLogBytes)
	}
	path := filepath.Join(t.TempDir(), LogFileName)
	if err := os.WriteFile(path, bytes.Repeat([]byte("a"), int(maxLogBytes)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := trimLogIfLarge(path, maxLogBytes); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() != maxLogBytes {
		t.Fatalf("size %d %v", st.Size(), err)
	}
	f, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(bytes.Repeat([]byte("b"), 8)); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.WriteFile(path, bytes.Repeat([]byte("c"), int(maxLogBytes)+8), 0644); err != nil {
		t.Fatal(err)
	}
	f, err = openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	st, err = os.Stat(path)
	if err != nil || st.Size() != 0 {
		t.Fatalf("truncated size %d %v", st.Size(), err)
	}
}

func TestJobCloseKillsHiddenChild(t *testing.T) {
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		t.Fatal("ComSpec is empty")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, LogFileName)
	child, err := startChild(comspec, dir, logPath, []string{"/d", "/c", "ping", "-n", "40", "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupRun(child) })
	attr := child.cmd.SysProcAttr
	if attr == nil || !attr.HideWindow || attr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("flags %+v", attr)
	}
	if child.cmd.Stdout != child.logFile || child.cmd.Stderr != child.logFile {
		t.Fatalf("stdio %T", child.cmd.Stdout)
	}
	if _, isPipe := child.cmd.Stdout.(*os.File); !isPipe {
		t.Fatal("stdout is not the log file")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !processAlive(child.cmd.Process.Pid) {
		if time.Now().After(deadline) {
			t.Fatal("child never became alive")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := visibleWindowCount(child.cmd.Process.Pid); n != 0 {
		t.Fatalf("visible windows %d", n)
	}
	if err := windows.CloseHandle(child.job); err != nil {
		t.Fatal(err)
	}
	child.job = 0
	select {
	case <-child.done:
	case <-time.After(3 * time.Second):
		t.Fatal("child still running after job close")
	}
	if processAlive(child.cmd.Process.Pid) {
		t.Fatal("pid still alive")
	}
}

func TestSwitchModeAndReload(t *testing.T) {
	var method, uri, body, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		uri = r.URL.RequestURI()
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	dir := t.TempDir()
	m := NewManager(dir)
	params := ConfigParams{MixedPort: 1, ControllerPort: port, AIDedicatedPort: 3, Secret: "sek", Mode: ModeRule}
	raw, err := Render(params, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.dir, ConfigFileName)
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	m.phase = phaseRunning
	m.run = &running{}
	m.params = params
	m.ports = Ports{Mixed: 1, Controller: port, AIDedicated: 3}
	m.configPath = path

	if err := m.SwitchMode(context.Background(), "nope"); err == nil {
		t.Fatal("expected bad mode")
	}
	if err := m.SwitchMode(context.Background(), ModeGlobal); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPatch || uri != "/configs" || auth != "Bearer sek" || !strings.Contains(body, `"mode":"global"`) {
		t.Fatalf("patch %s %s %s %s", method, uri, auth, body)
	}
	disk, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(disk), "mode: global\n") {
		t.Fatalf("disk %s", disk)
	}
	if m.Status().Mode != ModeGlobal {
		t.Fatal(m.Status().Mode)
	}

	if err := m.Reload(context.Background(), ConfigParams{Secret: "sek"}, []ProviderSpec{{ID: "alpha", Name: "Alpha", Filter: "us", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPut || uri != "/configs?force=true" {
		t.Fatalf("put %s %s", method, uri)
	}
	if !strings.Contains(body, filepath.ToSlash(path)) {
		t.Fatalf("put body %s", body)
	}
	disk, _ = os.ReadFile(path)
	if !strings.Contains(string(disk), "providers/alpha.txt") || !strings.Contains(string(disk), "mode: global\n") {
		t.Fatalf("reloaded\n%s", disk)
	}
}

func TestStopIsIdempotentAndStartRejectsBadBinary(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	m.phase = phaseRunning
	m.run = &running{}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(context.Background(), ConfigParams{}, nil); !errors.Is(err, ErrNotRunning) {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), filepath.Join(t.TempDir(), "mihomo.exe"), ConfigParams{}, nil); err == nil {
		t.Fatal("expected missing binary")
	}
	bad := filepath.Join(t.TempDir(), "mihomo.exe")
	if err := os.WriteFile(bad, []byte("not-an-exe"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), bad, ConfigParams{}, nil); !errors.Is(err, ErrNoPE) {
		t.Fatal(err)
	}
	if m.phase != phaseStopped {
		t.Fatalf("phase %d", m.phase)
	}
}

func TestChildExitAllowsRestart(t *testing.T) {
	m := NewManager(t.TempDir())
	t.Cleanup(func() { _ = m.Stop() })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROXYBOX_FAKE_CORE", "1")
	t.Setenv("PROXYBOX_FAKE_EXIT", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	first, err := m.Start(ctx, exe, fakeCoreParams(), nil)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if !first.Running || first.PID == 0 {
		t.Fatalf("first %+v", first)
	}
	waitNotRunning(t, m, 3*time.Second)

	t.Setenv("PROXYBOX_FAKE_EXIT", "")
	ctx, cancel = context.WithTimeout(context.Background(), 8*time.Second)
	second, err := m.Start(ctx, exe, fakeCoreParams(), nil)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if !second.Running || second.PID == 0 || second.PID == first.PID {
		t.Fatalf("second %+v first pid %d", second, first.PID)
	}
}

func TestStopOverlapsChildExit(t *testing.T) {
	m := NewManager(t.TempDir())
	t.Cleanup(func() { _ = m.Stop() })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROXYBOX_FAKE_CORE", "1")
	t.Setenv("PROXYBOX_FAKE_EXIT", "")

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	first, err := m.Start(ctx, exe, stoppedOverlapParams(), nil)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	done := m.run.done
	m.mu.Unlock()
	stopErr := make(chan error, 1)
	go func() {
		<-done
		stopErr <- m.Stop()
	}()
	out, killErr := exec.Command("taskkill", "/F", "/PID", strconv.Itoa(first.PID)).CombinedOutput()
	if killErr != nil {
		t.Fatalf("taskkill %v %s", killErr, out)
	}
	select {
	case err := <-stopErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop hung")
	}
	waitNotRunning(t, m, 2*time.Second)

	ctx, cancel = context.WithTimeout(context.Background(), 8*time.Second)
	second, err := m.Start(ctx, exe, stoppedOverlapParams(), nil)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if !second.Running || second.PID == 0 || second.PID == first.PID {
		t.Fatalf("second %+v first pid %d", second, first.PID)
	}
}

func TestStaleExitLeavesCurrentRun(t *testing.T) {
	m := NewManager(t.TempDir())
	logf, err := os.Create(filepath.Join(t.TempDir(), "stale.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logf.Close()
	job, err := newJob()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	run := &running{done: done, stopLog: make(chan struct{}), logFile: logf, job: job}
	m.mu.Lock()
	m.phase = phaseRunning
	m.generation = 2
	m.run = run
	m.mu.Unlock()

	m.watchDone(1, run)
	if !m.Status().Running {
		t.Fatal("stale exit stopped the run")
	}
	if !jobOpen(job) {
		t.Fatal("stale exit closed the job")
	}
	if _, err := logf.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}

	m.watchDone(2, run)
	if m.Status().Running {
		t.Fatal("current exit left the run up")
	}
	if jobOpen(job) {
		t.Fatal("job still open")
	}
	if _, err := logf.Write([]byte("y")); err == nil {
		t.Fatal("log still open")
	}
}

func TestExitHookSkipsStopAndRunsOnce(t *testing.T) {
	m := NewManager(t.TempDir())
	calls := 0
	m.SetExitHook(func() { calls++ })

	run := finishedRun(t)
	m.mu.Lock()
	m.phase = phaseRunning
	m.generation = 2
	m.run = run
	m.mu.Unlock()
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	m.watchDone(2, run)
	if calls != 0 {
		t.Fatalf("stop calls %d", calls)
	}

	run = finishedRun(t)
	m.mu.Lock()
	m.phase = phaseRunning
	m.generation = 4
	m.run = run
	m.mu.Unlock()
	m.watchDone(4, run)
	m.watchDone(4, run)
	if calls != 1 {
		t.Fatalf("crash calls %d", calls)
	}
}

func finishedRun(t *testing.T) *running {
	t.Helper()
	logf, err := os.Create(filepath.Join(t.TempDir(), "exit.log"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := newJob()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	return &running{done: done, stopLog: make(chan struct{}), logFile: logf, job: job}
}

func fakeCoreParams() ConfigParams {
	return ConfigParams{
		Secret:          "sek",
		Mode:            ModeRule,
		MixedPort:       39190,
		ControllerPort:  39191,
		AIDedicatedPort: 39192,
	}
}

func stoppedOverlapParams() ConfigParams {
	return ConfigParams{
		Secret:          "sek",
		Mode:            ModeRule,
		MixedPort:       39200,
		ControllerPort:  39201,
		AIDedicatedPort: 39202,
	}
}

func jobOpen(h windows.Handle) bool {
	var dup windows.Handle
	err := windows.DuplicateHandle(windows.CurrentProcess(), h, windows.CurrentProcess(), &dup, 0, false, windows.DUPLICATE_SAME_ACCESS)
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(dup)
	return true
}

func waitNotRunning(t *testing.T, m *Manager, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for m.Status().Running {
		if time.Now().After(deadline) {
			t.Fatalf("still running pid %d", m.Status().PID)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func runFakeCore() int {
	dir := "."
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "-d" && i+1 < len(os.Args) {
			dir = os.Args[i+1]
		}
	}
	port, err := controllerPort(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	exit := os.Getenv("PROXYBOX_FAKE_EXIT") == "1"
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"version":"fake"}`)
		if exit {
			time.AfterFunc(500*time.Millisecond, func() { _ = ln.Close() })
		}
	})}
	_ = srv.Serve(ln)
	return 0
}

func controllerPort(dir string) (int, error) {
	b, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		const key = "external-controller:"
		if !strings.HasPrefix(line, key) {
			continue
		}
		val := strings.Trim(strings.TrimSpace(line[len(key):]), "'\"")
		_, portStr, ok := strings.Cut(val, ":")
		if !ok {
			return 0, fmt.Errorf("controller %q", val)
		}
		return strconv.Atoi(portStr)
	}
	return 0, fmt.Errorf("no external-controller")
}
