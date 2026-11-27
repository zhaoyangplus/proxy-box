package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"proxy-box/internal/configstore"
	"proxy-box/internal/configurator"
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

func TestOneClickRefusesWhenEveryAppIsOff(t *testing.T) {
	useTempHome(t)
	original := []byte("{\"editor.fontSize\":1}\n")
	writeRaw(t, cursorSettings(t), original)
	rt := &appRuntime{core: &fakeCore{running: true, ports: core.Ports{Mixed: 27890, AIDedicated: 27892}}}
	rec := postJSON(rt.handleOneClick, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != errNeedManagedApp {
		t.Fatalf("error %q", body.Error)
	}
	if !bytes.Equal(mustRead(t, cursorSettings(t)), original) {
		t.Fatal("one-click wrote a file while every app was off")
	}
}

func TestOneClickWritesOnlyEnabledApps(t *testing.T) {
	useTempHome(t)
	writeRaw(t, cursorSettings(t), []byte("{}\n"))
	codexBody := []byte("# c\n")
	writeRaw(t, codexEnv(t), codexBody)
	enableApp(t, "cursor", true)
	fc := &fakeCore{running: true, ports: core.Ports{Mixed: 27890, AIDedicated: 27992}}
	rt := &appRuntime{
		core: fc,
		detect: func() detector.SystemSnapshot {
			return detector.SystemSnapshot{AppConfig: configstore.LoadConfig()}
		},
	}
	rec := postJSON(rt.handleOneClick, `{"syncUserEnv":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d body %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(mustRead(t, cursorSettings(t)), []byte("http://127.0.0.1:27992")) {
		t.Fatalf("cursor = %s", mustRead(t, cursorSettings(t)))
	}
	if !bytes.Equal(mustRead(t, codexEnv(t)), codexBody) {
		t.Fatal("codex changed")
	}
	if configstore.LoadState().UserEnvManaged {
		t.Fatal("user env written")
	}
}

func TestRestoreAllClearsFlags(t *testing.T) {
	useTempHome(t)
	original := []byte("{\r\n  \"editor.fontSize\": 3\r\n}\r\n")
	writeRaw(t, cursorSettings(t), original)
	enableApp(t, "cursor", true)
	enableApp(t, "codex", true)
	if _, err := configurator.ApplyTarget("cursor", "http://127.0.0.1:27892"); err != nil {
		t.Fatal(err)
	}
	fc := &fakeCore{running: true, ports: core.Ports{Mixed: 27890, AIDedicated: 27892}}
	rt := &appRuntime{core: fc}
	rec := postJSON(rt.handleRestoreAll, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || !bytes.Contains([]byte(body.Message), []byte("关闭")) {
		t.Fatalf("body %s", rec.Body.String())
	}
	cfg := configstore.LoadConfig()
	if cfg.IsAppEnabled("cursor") || cfg.IsAppEnabled("codex") || len(cfg.EnabledAppIDs()) != 0 {
		t.Fatalf("flags %+v", cfg.ManagedApps)
	}
	if !bytes.Equal(mustRead(t, cursorSettings(t)), original) {
		t.Fatalf("cursor = %s", mustRead(t, cursorSettings(t)))
	}
	if fc.reloads != 1 || len(fc.last.EnabledAIApps) != 0 {
		t.Fatalf("reload %+v count %d", fc.last.EnabledAIApps, fc.reloads)
	}
}

func TestSyncSkipsDisabledAppsAndUnusableCore(t *testing.T) {
	useTempHome(t)
	cursorBody := []byte("{}\n")
	codexBody := []byte("# c\n")
	writeRaw(t, cursorSettings(t), cursorBody)
	writeRaw(t, codexEnv(t), codexBody)
	enableApp(t, "cursor", true)
	cfg := configstore.LoadConfig()
	if cfg.SyncUserEnv {
		t.Fatal("sync user env defaulted on")
	}
	client := detector.ProxyClientInfo{
		ID: "builtin", Name: "内置内核", DedicatedPort: 27892, ProxyURL: "http://127.0.0.1:27890",
	}
	applied, reason, err := syncEnabledApps(cfg, client, nil, nil)
	if err != nil || applied || reason == "" {
		t.Fatalf("applied %v reason %q err %v", applied, reason, err)
	}
	if !bytes.Equal(mustRead(t, cursorSettings(t)), cursorBody) {
		t.Fatal("sync wrote a file without a usable subscription")
	}
	subs := []configstore.Subscription{{ID: "s", Enabled: true, NodeCount: 2}}
	applied, _, err = syncEnabledApps(cfg, client, subs, nil)
	if err != nil || !applied {
		t.Fatalf("applied %v err %v", applied, err)
	}
	if !bytes.Contains(mustRead(t, cursorSettings(t)), []byte("http://127.0.0.1:27892")) {
		t.Fatalf("cursor = %s", mustRead(t, cursorSettings(t)))
	}
	if !bytes.Equal(mustRead(t, codexEnv(t)), codexBody) {
		t.Fatal("disabled codex was rewritten")
	}
	written := mustRead(t, cursorSettings(t))
	client.DedicatedPort = 27999
	applied, reason, err = syncEnabledApps(cfg, client, subs, errors.New("load failed"))
	if err != nil || applied || reason != "内置内核还没有可用订阅或节点，未改写 IDE 配置" {
		t.Fatalf("applied %v reason %q err %v", applied, reason, err)
	}
	if !bytes.Equal(mustRead(t, cursorSettings(t)), written) {
		t.Fatal("sync rewrote a file after the core became unusable")
	}

	off := configstore.LoadConfig()
	off.ManagedApps["cursor"] = false
	applied, _, err = syncEnabledApps(off, client, subs, nil)
	if err != nil || applied {
		t.Fatalf("all off applied %v err %v", applied, err)
	}
}

func TestTrayApplyAllBalloonWhenNoneEnabled(t *testing.T) {
	useTempHome(t)
	rt := &appRuntime{}
	var title, info string
	rt.applyEnabledFromTray(func(a, b string) {
		title, info = a, b
	})
	if title != errNeedManagedApp || info != errNeedManagedApp {
		t.Fatalf("balloon %q %q", title, info)
	}
}

func TestQuickSyncWritesOnlyEnabledApp(t *testing.T) {
	useTempHome(t)
	cursorBody := []byte("{\"a\":1}\n")
	writeRaw(t, cursorSettings(t), cursorBody)
	prevDetect := detectSnapshot
	prevLaunch := launchWithProxy
	t.Cleanup(func() {
		detectSnapshot = prevDetect
		launchWithProxy = prevLaunch
	})
	var calls int
	var gotProxy string
	detectSnapshot = func() detector.SystemSnapshot {
		return detector.SystemSnapshot{
			ActiveClient: detector.ProxyClientInfo{DedicatedPort: 27892, Name: "内置内核", ProxyURL: "http://127.0.0.1:27890"},
			AppConfig:    configstore.LoadConfig(),
			Targets:      []detector.AppTargetInfo{{ID: "cursor", ExePath: "cursor.exe"}},
		}
	}
	launchWithProxy = func(id, exe, proxy string, _ bool) error {
		calls++
		gotProxy = proxy
		if id != "cursor" || exe != "cursor.exe" {
			t.Errorf("launch %s %s", id, exe)
		}
		return nil
	}
	if err := quickSyncAndLaunch("cursor", false); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || gotProxy != "http://127.0.0.1:27892" {
		t.Fatalf("calls %d proxy %s", calls, gotProxy)
	}
	if !bytes.Equal(mustRead(t, cursorSettings(t)), cursorBody) {
		t.Fatal("disabled launch wrote settings")
	}
	enableApp(t, "cursor", true)
	if err := quickSyncAndLaunch("cursor", true); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls %d", calls)
	}
	if !bytes.Contains(mustRead(t, cursorSettings(t)), []byte("http://127.0.0.1:27892")) {
		t.Fatal("enabled launch did not write settings")
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
