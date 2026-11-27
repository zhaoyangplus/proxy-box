package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"proxy-box/internal/configstore"
)

func handleSaveConfig(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(rw, err)
		return
	}
	current, _ := configstore.LoadConfig()
	merged, err := mergeAppConfig(current, body)
	if err != nil {
		writeErr(rw, err)
		return
	}
	if err := configstore.SaveConfig(merged); err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true})
}

// mergeAppConfig copies current and replaces only fields present in body.
func mergeAppConfig(current configstore.AppConfig, body []byte) (configstore.AppConfig, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return current, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return current, err
	}
	out := current
	fields := []struct {
		key string
		dst any
	}{
		{"customCursorExe", &out.CustomCursorExe},
		{"customAntigravityExe", &out.CustomAntigravityExe},
		{"customCodexExe", &out.CustomCodexExe},
		{"syncUserEnv", &out.SyncUserEnv},
		{"minimizeToTray", &out.MinimizeToTray},
		{"autoOptimizeNode", &out.AutoOptimizeNode},
		{"autoSyncOnChange", &out.AutoSyncOnChange},
		{"nodeSelectionMode", &out.NodeSelectionMode},
		{"manualNodeName", &out.ManualNodeName},
		{"managedApps", &out.ManagedApps},
		{"delayTestURL", &out.DelayTestURL},
		{"speedTestURL", &out.SpeedTestURL},
		{"speedTestMaxMB", &out.SpeedTestMaxMB},
	}
	for _, field := range fields {
		rawValue, ok := raw[field.key]
		if !ok {
			continue
		}
		if err := json.Unmarshal(rawValue, field.dst); err != nil {
			return current, err
		}
	}
	return out, nil
}
