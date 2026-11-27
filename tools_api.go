package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"proxy-box/internal/configstore"
	"proxy-box/internal/configurator"
	"proxy-box/internal/detector"
)

const stoppedSwitchMessage = "已开启，启动内核后自动生效"

func (a *appRuntime) handleToolsToggle(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeUserErr(rw, errors.New("请使用 POST"))
		return
	}
	var req struct {
		TargetID string `json:"targetId"`
		Enabled  bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUserErr(rw, err)
		return
	}
	id := strings.TrimSpace(req.TargetID)
	if !configurator.KnownTarget(id) {
		writeUserErr(rw, configurator.ErrUnknownTarget)
		return
	}

	a.opMu.Lock()
	msg, snap, err := a.toggleApp(r.Context(), id, req.Enabled)
	a.opMu.Unlock()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	if a.hub != nil {
		fresh := snap
		a.hub.broadcast(sseEvent{
			Type:       "sync",
			Reason:     msg,
			ShouldDiag: a.core != nil && a.core.Status().Running,
			Snapshot:   &fresh,
		})
	}
	writeJSON(rw, http.StatusOK, map[string]any{
		"ok":       true,
		"message":  msg,
		"snapshot": snap,
	})
}

func (a *appRuntime) toggleApp(ctx context.Context, id string, enabled bool) (string, detector.SystemSnapshot, error) {
	cfg := configstore.LoadConfig()
	cfg.ManagedApps[id] = enabled
	if err := configstore.SaveConfig(cfg); err != nil {
		return "", detector.SystemSnapshot{}, err
	}
	running := a != nil && a.core != nil && a.core.Status().Running
	if enabled {
		if !running {
			return stoppedSwitchMessage, a.snapshot(), nil
		}
		port := a.core.Status().Ports.AIDedicated
		if port <= 0 {
			return "", detector.SystemSnapshot{}, errors.New("AI 专属端口未知")
		}
		summary, err := configurator.ApplyTarget(id, fmt.Sprintf("http://127.0.0.1:%d", port))
		if err != nil {
			return "", detector.SystemSnapshot{}, err
		}
		if err := a.reloadCoreLocked(ctx); err != nil {
			return "", detector.SystemSnapshot{}, err
		}
		if summary == "" {
			summary = "已开启"
		}
		return summary, a.snapshot(), nil
	}
	if _, err := configurator.RestoreTarget(id); err != nil {
		return "", detector.SystemSnapshot{}, err
	}
	if running {
		if err := a.reloadCoreLocked(ctx); err != nil {
			return "", detector.SystemSnapshot{}, err
		}
	}
	return "已关闭，并已还原该应用配置", a.snapshot(), nil
}

func (a *appRuntime) reloadCoreLocked(ctx context.Context) error {
	if a == nil || a.core == nil || !a.core.Status().Running {
		return nil
	}
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		return err
	}
	if mode := strings.TrimSpace(a.core.Status().Mode); mode != "" {
		settings.Mode = mode
	}
	subs, err := configstore.LoadSubscriptions()
	if err != nil {
		return err
	}
	return a.core.Reload(ctx, a.coreParams(settings), providerSpecs(subs))
}

func (a *appRuntime) applyEnabledTargetsLocked(port int) error {
	if port <= 0 {
		return nil
	}
	cfg := configstore.LoadConfig()
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	for _, id := range cfg.EnabledAppIDs() {
		if _, err := configurator.ApplyTarget(id, proxyURL); err != nil {
			return err
		}
	}
	return nil
}
