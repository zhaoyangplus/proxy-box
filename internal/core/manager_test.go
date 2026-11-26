package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

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
