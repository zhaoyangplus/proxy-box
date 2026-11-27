package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"proxy-box/internal/configstore"
	"proxy-box/internal/core"
	"proxy-box/internal/detector"
)

func useTempHome(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("APPDATA", filepath.Join(root, "roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "local"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	configstore.SetBaseDir(filepath.Join(root, "box"))
	t.Cleanup(func() { configstore.SetBaseDir("") })
}

func writeRaw(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0644); err != nil {
		t.Fatal(err)
	}
}

func cursorSettings(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.Getenv("APPDATA"), "Cursor", "User", "settings.json")
}

func codexEnv(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.Getenv("USERPROFILE"), ".codex", ".env")
}

func enableApp(t *testing.T, id string, on bool) {
	t.Helper()
	cfg := configstore.LoadConfig()
	cfg.ManagedApps[id] = on
	if err := configstore.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestToggleOnCoreStoppedDoesNotWriteAppFiles(t *testing.T) {
	useTempHome(t)
	original := []byte("{\"editor.fontSize\":14}\n")
	writeRaw(t, cursorSettings(t), original)
	other := []byte("# keep\n")
	writeRaw(t, codexEnv(t), other)
	fc := &fakeCore{}
	hub := newSSEHub()
	events := hub.subscribe()
	rt := &appRuntime{
		core: fc,
		hub:  hub,
		detect: func() detector.SystemSnapshot {
			return detector.SystemSnapshot{AppConfig: configstore.LoadConfig()}
		},
	}
	rec := postJSON(rt.handleToolsToggle, `{"targetId":"cursor","enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		OK       bool                    `json:"ok"`
		Message  string                  `json:"message"`
		Snapshot detector.SystemSnapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Message != stoppedSwitchMessage {
		t.Fatalf("body %s", rec.Body.String())
	}
	if !body.Snapshot.AppConfig.IsAppEnabled("cursor") {
		t.Fatalf("snapshot %+v", body.Snapshot.AppConfig.ManagedApps)
	}
	if fc.reloads != 0 {
		t.Fatalf("reloads %d", fc.reloads)
	}
	if got := mustRead(t, cursorSettings(t)); !bytes.Equal(got, original) {
		t.Fatalf("cursor file = %s", got)
	}
	if got := mustRead(t, codexEnv(t)); !bytes.Equal(got, other) {
		t.Fatalf("codex file = %s", got)
	}
	if !configstore.LoadConfig().IsAppEnabled("cursor") {
		t.Fatal("flag not saved")
	}
	select {
	case raw := <-events:
		if !bytes.Contains(raw, []byte(`"type":"sync"`)) {
			t.Fatalf("event %s", raw)
		}
	default:
		t.Fatal("missing sync event")
	}
}

func TestToggleOnAndOffReloadsEnabledApps(t *testing.T) {
	useTempHome(t)
	original := []byte("{\r\n  \"editor.fontSize\": 14\r\n}\r\n")
	writeRaw(t, cursorSettings(t), original)
	other := []byte("# keep\n")
	writeRaw(t, codexEnv(t), other)
	fc := &fakeCore{running: true, ports: core.Ports{Mixed: 27890, AIDedicated: 27892}}
	rt := &appRuntime{
		core: fc,
		detect: func() detector.SystemSnapshot {
			return detector.SystemSnapshot{AppConfig: configstore.LoadConfig()}
		},
	}
	rec := postJSON(rt.handleToolsToggle, `{"targetId":"cursor","enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("on code %d body %s", rec.Code, rec.Body.String())
	}
	if fc.reloads != 1 || len(fc.last.EnabledAIApps) != 1 || fc.last.EnabledAIApps[0] != "cursor" {
		t.Fatalf("reload %+v count %d", fc.last.EnabledAIApps, fc.reloads)
	}
	got := mustRead(t, cursorSettings(t))
	if !bytes.Contains(got, []byte("http://127.0.0.1:27892")) || bytes.Contains(got, []byte("27890")) {
		t.Fatalf("cursor file = %s", got)
	}
	if !bytes.Equal(mustRead(t, codexEnv(t)), other) {
		t.Fatal("codex file changed")
	}

	rec = postJSON(rt.handleToolsToggle, `{"targetId":"cursor","enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("off code %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Message == "" {
		t.Fatalf("off body %s", rec.Body.String())
	}
	if fc.reloads != 2 || len(fc.last.EnabledAIApps) != 0 {
		t.Fatalf("off reload %+v count %d", fc.last.EnabledAIApps, fc.reloads)
	}
	if restored := mustRead(t, cursorSettings(t)); !bytes.Equal(restored, original) {
		t.Fatalf("restored = %s", restored)
	}
	if configstore.LoadConfig().IsAppEnabled("cursor") {
		t.Fatal("cursor still enabled")
	}
}

func TestToggleRejectsUnknownApp(t *testing.T) {
	useTempHome(t)
	enableApp(t, "cursor", true)
	rt := &appRuntime{core: &fakeCore{}}
	rec := postJSON(rt.handleToolsToggle, `{"targetId":"notepad","enabled":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code %d body %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("未知的应用标识")) {
		t.Fatalf("body %s", rec.Body.String())
	}
	if !configstore.LoadConfig().IsAppEnabled("cursor") {
		t.Fatal("cursor flag changed")
	}
}

func TestToggleSerializesDoubleClicks(t *testing.T) {
	useTempHome(t)
	writeRaw(t, cursorSettings(t), []byte("{}\n"))
	fc := &fakeCore{running: true, ports: core.Ports{Mixed: 27890, AIDedicated: 27892}}
	rt := &appRuntime{
		core: fc,
		detect: func() detector.SystemSnapshot {
			return detector.SystemSnapshot{AppConfig: configstore.LoadConfig()}
		},
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(on bool) {
			defer wg.Done()
			body := `{"targetId":"cursor","enabled":false}`
			if on {
				body = `{"targetId":"cursor","enabled":true}`
			}
			rec := postJSON(rt.handleToolsToggle, body)
			if rec.Code != http.StatusOK {
				t.Errorf("code %d body %s", rec.Code, rec.Body.String())
			}
		}(i%2 == 0)
	}
	wg.Wait()
	cfg := configstore.LoadConfig()
	if _, ok := cfg.ManagedApps["cursor"]; !ok {
		t.Fatalf("apps %+v", cfg.ManagedApps)
	}
	st := configstore.LoadState()
	if cfg.IsAppEnabled("cursor") != st.Targets["cursor"].Managed && st.Targets["cursor"].Managed {
		t.Fatalf("flag %v managed %v", cfg.IsAppEnabled("cursor"), st.Targets["cursor"].Managed)
	}
}

func TestStartCoreAppliesOnlyEnabledApps(t *testing.T) {
	useTempHome(t)
	cursorBody := []byte("{\"editor.fontSize\":1}\n")
	codexBody := []byte("# leave\n")
	writeRaw(t, cursorSettings(t), cursorBody)
	writeRaw(t, codexEnv(t), codexBody)
	enableApp(t, "cursor", true)
	fc := &fakeCore{ports: core.Ports{Mixed: 27890, Controller: 27891, AIDedicated: 27992}}
	rt := &appRuntime{
		core:    fc,
		coreDir: t.TempDir(),
		ensureBinary: func(context.Context, string, []string, core.ProgressFunc) (string, error) {
			return "mihomo.exe", nil
		},
	}
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Enabled = true
	settings.SystemProxy = false
	settings.BypassCN = false
	if _, err := rt.startCore(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if len(fc.started.EnabledAIApps) != 1 || fc.started.EnabledAIApps[0] != "cursor" {
		t.Fatalf("start apps %+v", fc.started.EnabledAIApps)
	}
	if !bytes.Contains(mustRead(t, cursorSettings(t)), []byte("http://127.0.0.1:27992")) {
		t.Fatalf("cursor = %s", mustRead(t, cursorSettings(t)))
	}
	if !bytes.Equal(mustRead(t, codexEnv(t)), codexBody) {
		t.Fatal("codex was rewritten")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
