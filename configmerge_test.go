package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"proxy-box/internal/configstore"
)

func TestMergeAppConfigKeepsAbsentFields(t *testing.T) {
	dir := t.TempDir()
	configstore.SetBaseDir(dir)
	t.Cleanup(func() { configstore.SetBaseDir("") })

	current := configstore.AppConfig{
		PreferredClient:   "clash_verge",
		CustomClashDir:    `D:\Clash`,
		CustomClashExe:    `D:\Clash\clash-verge.exe`,
		ExcludeRegionsRegex: "HK",
		SyncUserEnv:       true,
		MinimizeToTray:    true,
		AutoOptimizeNode:  configstore.BoolPtr(false),
		AutoSyncOnChange:  configstore.BoolPtr(true),
		NodeSelectionMode: "manual",
		ManualNodeName:    "US-01",
	}
	if err := configstore.SaveConfig(current); err != nil {
		t.Fatal(err)
	}

	merged, err := mergeAppConfig(current, []byte(`{"preferredClient":"builtin"}`))
	if err != nil {
		t.Fatal(err)
	}
	if merged.PreferredClient != "builtin" {
		t.Fatalf("preferred = %s", merged.PreferredClient)
	}
	if merged.CustomClashDir != current.CustomClashDir || merged.CustomClashExe != current.CustomClashExe {
		t.Fatalf("paths cleared: %+v", merged)
	}
	if merged.ExcludeRegionsRegex != "HK" || merged.ManualNodeName != "US-01" || merged.NodeSelectionMode != "manual" {
		t.Fatalf("text fields cleared: %+v", merged)
	}
	if !merged.SyncUserEnv || !merged.MinimizeToTray {
		t.Fatalf("bools cleared: %+v", merged)
	}
	if merged.AutoOptimizeNode == nil || *merged.AutoOptimizeNode {
		t.Fatalf("auto optimize = %v", merged.AutoOptimizeNode)
	}

	off, err := mergeAppConfig(merged, []byte(`{"syncUserEnv":false,"manualNodeName":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if off.SyncUserEnv {
		t.Fatal("syncUserEnv stayed true")
	}
	if off.ManualNodeName != "" {
		t.Fatalf("manual node = %q", off.ManualNodeName)
	}
	if off.PreferredClient != "builtin" || off.CustomClashDir != current.CustomClashDir {
		t.Fatalf("unrelated fields changed: %+v", off)
	}

	if err := configstore.SaveConfig(current); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/save-config", bytes.NewBufferString(`{"minimizeToTray":false}`))
	rec := httptest.NewRecorder()
	handleSaveConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	stored := configstore.LoadConfig()
	if stored.PreferredClient != current.PreferredClient || stored.CustomClashDir != current.CustomClashDir || stored.ManualNodeName != current.ManualNodeName {
		t.Fatalf("stored = %+v", stored)
	}
	if stored.MinimizeToTray {
		t.Fatal("minimize flag was not updated")
	}
	if !stored.SyncUserEnv || stored.ExcludeRegionsRegex != "HK" {
		t.Fatalf("absent fields changed: %+v", stored)
	}
	if stored.AutoOptimizeNode == nil || *stored.AutoOptimizeNode {
		t.Fatalf("auto optimize changed: %v", stored.AutoOptimizeNode)
	}
}
