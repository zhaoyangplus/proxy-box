package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proxy-box/internal/configstore"
)

func TestMergeAppConfigKeepsAbsentFields(t *testing.T) {
	dir := t.TempDir()
	configstore.SetBaseDir(dir)
	t.Cleanup(func() { configstore.SetBaseDir("") })

	current := configstore.LoadConfig()
	current.CustomCursorExe = `D:\Cursor\Cursor.exe`
	current.CustomCodexExe = `D:\Codex\codex.exe`
	current.SyncUserEnv = true
	current.MinimizeToTray = true
	current.AutoOptimizeNode = configstore.BoolPtr(false)
	current.AutoSyncOnChange = configstore.BoolPtr(true)
	current.NodeSelectionMode = "manual"
	current.ManualNodeName = "US-01"
	current.DelayTestURL = "http://127.0.0.1/generate_204"
	current.ManagedApps = map[string]bool{"cursor": true}
	if err := configstore.SaveConfig(current); err != nil {
		t.Fatal(err)
	}
	current = configstore.LoadConfig()

	merged, err := mergeAppConfig(current, []byte(`{"customCursorExe":"E:\\Cursor.exe","preferredClient":"builtin","excludeRegionsRegex":"HK"}`))
	if err != nil {
		t.Fatal(err)
	}
	if merged.CustomCursorExe != `E:\Cursor.exe` || merged.CustomCodexExe != current.CustomCodexExe {
		t.Fatalf("paths = %+v", merged)
	}
	if merged.DelayTestURL != current.DelayTestURL || merged.ManualNodeName != "US-01" || merged.NodeSelectionMode != "manual" {
		t.Fatalf("text fields cleared: %+v", merged)
	}
	if !merged.SyncUserEnv || !merged.MinimizeToTray || !merged.IsAppEnabled("cursor") {
		t.Fatalf("bools cleared: %+v", merged)
	}
	if merged.AutoOptimizeNode == nil || *merged.AutoOptimizeNode {
		t.Fatalf("auto optimize = %v", merged.AutoOptimizeNode)
	}

	off, err := mergeAppConfig(merged, []byte(`{"syncUserEnv":false,"manualNodeName":"","managedApps":{"codex":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if off.SyncUserEnv || off.ManualNodeName != "" {
		t.Fatalf("cleared fields stayed: %+v", off)
	}
	if !off.IsAppEnabled("cursor") || !off.IsAppEnabled("codex") {
		t.Fatalf("managed apps = %+v", off.ManagedApps)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/save-config", bytes.NewBufferString(`{"minimizeToTray":false,"delayTestURL":"http://example.test/204","speedTestURL":"https://example.test/10mb","speedTestMaxMB":10}`))
	rec := httptest.NewRecorder()
	handleSaveConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	stored := configstore.LoadConfig()
	if stored.MinimizeToTray || stored.CustomCursorExe != `D:\Cursor\Cursor.exe` || stored.ManualNodeName != "US-01" {
		t.Fatalf("stored = %+v", stored)
	}
	if stored.DelayTestURL != "http://example.test/204" || stored.SpeedTestURL != "https://example.test/10mb" || stored.SpeedTestMaxMB != 10 {
		t.Fatalf("urls = %+v", stored)
	}
	if !stored.SyncUserEnv || stored.AutoOptimizeNode == nil || *stored.AutoOptimizeNode {
		t.Fatalf("absent fields changed: %+v", stored)
	}
	if !stored.IsAppEnabled("cursor") || stored.IsAppEnabled("codex") {
		t.Fatalf("apps = %+v", stored.ManagedApps)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "preferredClient") || strings.Contains(string(raw), "excludeRegionsRegex") {
		t.Fatalf("saved removed fields: %s", raw)
	}
}
