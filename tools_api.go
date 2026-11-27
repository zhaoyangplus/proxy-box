package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"proxy-box/internal/configstore"
	"proxy-box/internal/configurator"
	"proxy-box/internal/detector"
	"proxy-box/internal/diagnostics"
)

const stoppedSwitchMessage = "已开启，启动内核后自动生效"

const errNeedManagedApp = "请先在「AI 工具」页开启至少一个需要免 TUN 代理的应用"

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

func (a *appRuntime) toggleApp(ctx context.Context, id string, enabled bool) (msg string, snap detector.SystemSnapshot, err error) {
	if err := configstore.UpdateConfig(func(cfg *configstore.AppConfig) error {
		cfg.ManagedApps[id] = enabled
		return nil
	}); err != nil {
		return "", detector.SystemSnapshot{}, err
	}
	// A failed switch must leave the flag where the app files really are.
	defer func() {
		if err == nil {
			return
		}
		if enabled {
			_, _ = configurator.RestoreTarget(id)
		}
		_ = configstore.UpdateConfig(func(cfg *configstore.AppConfig) error {
			cfg.ManagedApps[id] = !enabled
			return nil
		})
	}()
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
	return a.reloadLive(ctx, nil)
}

func (a *appRuntime) applyEnabledTargetsLocked(port int) error {
	if port <= 0 {
		return nil
	}
	cfg, _ := configstore.LoadConfig()
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	for _, id := range cfg.EnabledAppIDs() {
		if _, err := configurator.ApplyTarget(id, proxyURL); err != nil {
			return err
		}
	}
	return nil
}

func (a *appRuntime) handleOneClick(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		SyncUserEnv bool `json:"syncUserEnv"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	cfg, err := configstore.LoadConfig()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	if len(cfg.EnabledAppIDs()) == 0 {
		writeUserErr(rw, errors.New(errNeedManagedApp))
		return
	}
	port := a.aiDedicatedPort()
	a.opMu.Lock()
	_, err = configurator.ApplyAll(managedApplyOptions(cfg, port, req.SyncUserEnv))
	a.opMu.Unlock()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	msg := fmt.Sprintf("已按 AI 专属端口 %d 配置已开启的应用", port)
	snap := a.snapshot()
	if snap.ActiveClient.ControllerReachable {
		if opt, oErr := detector.OptimizeClientNode(snap.ActiveClient); oErr == nil {
			msg += fmt.Sprintf("，已优选节点 %s (%d ms)", opt.SelectedNode, opt.DelayMs)
		}
	}
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (a *appRuntime) handleRestoreAll(rw http.ResponseWriter, r *http.Request) {
	msg, err := a.restoreAllManaged(r.Context())
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (a *appRuntime) restoreAllManaged(ctx context.Context) (string, error) {
	if a != nil {
		a.opMu.Lock()
		defer a.opMu.Unlock()
	}
	if _, err := configurator.RestoreAll(); err != nil {
		return "", err
	}
	if err := configstore.UpdateConfig(func(cfg *configstore.AppConfig) error {
		for id := range cfg.ManagedApps {
			cfg.ManagedApps[id] = false
		}
		return nil
	}); err != nil {
		return "", err
	}
	if err := a.reloadCoreLocked(ctx); err != nil {
		return "", err
	}
	return "已还原已开启应用的配置，并关闭全部免 TUN 开关", nil
}

func (a *appRuntime) applyEnabledFromTray(balloon func(title, info string)) {
	if balloon == nil {
		return
	}
	cfg, err := configstore.LoadConfig()
	if err != nil {
		balloon("读取设置失败", err.Error())
		return
	}
	if len(cfg.EnabledAppIDs()) == 0 {
		balloon(errNeedManagedApp, errNeedManagedApp)
		return
	}
	port := a.aiDedicatedPort()
	a.opMu.Lock()
	_, err = configurator.ApplyAll(managedApplyOptions(cfg, port, cfg.SyncUserEnv))
	a.opMu.Unlock()
	if err != nil {
		balloon("配置失败", err.Error())
		return
	}
	snap := a.snapshot()
	if snap.ActiveClient.ControllerReachable {
		_, _ = detector.OptimizeClientNode(snap.ActiveClient)
	}
	balloon("免 TUN 代理已生效", fmt.Sprintf("已同步 AI 专属端口 %d", port))
}

func (a *appRuntime) syncLiveApps(cfg configstore.AppConfig, client detector.ProxyClientInfo, subs []configstore.Subscription, subErr error) (bool, string, error) {
	if a != nil {
		a.opMu.Lock()
		defer a.opMu.Unlock()
	}
	return syncEnabledApps(cfg, client, subs, subErr)
}

func syncEnabledApps(cfg configstore.AppConfig, client detector.ProxyClientInfo, subs []configstore.Subscription, subErr error) (bool, string, error) {
	if client.ID == "builtin" && (subErr != nil || !detector.IDESyncAllowed(client, subs)) {
		return false, "内置内核还没有可用订阅或节点，未改写 IDE 配置", nil
	}
	if len(cfg.EnabledAppIDs()) == 0 {
		return false, fmt.Sprintf("检测到代理源变更为 %s (%s)", client.Name, client.ProxyURL), nil
	}
	if client.DedicatedPort <= 0 {
		return false, "", errors.New("AI 专属端口未知")
	}
	if _, err := configurator.ApplyAll(managedApplyOptions(cfg, client.DedicatedPort, cfg.SyncUserEnv)); err != nil {
		return false, "", err
	}
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", client.DedicatedPort)
	if client.DedicatedPortActive {
		return true, fmt.Sprintf("独立隔离通道 %s 已上线，已自动同步已开启的 AI 工具", proxyURL), nil
	}
	return true, fmt.Sprintf("检测到代理切换至 %s (%s)，已自动同步已开启的 IDE 端口", client.Name, proxyURL), nil
}

func (a *appRuntime) aiDedicatedPort() int {
	if a != nil && a.core != nil && a.core.Status().Running {
		if port := a.core.Status().Ports.AIDedicated; port > 0 {
			return port
		}
	}
	settings, err := configstore.LoadCoreSettings()
	if err == nil && settings.AIDedicatedPort > 0 {
		return settings.AIDedicatedPort
	}
	return 27892
}

func (a *appRuntime) handleDiagnostics(rw http.ResponseWriter, r *http.Request) {
	if a == nil || a.core == nil || !a.core.Status().Running {
		writeUserErr(rw, errors.New("内核未运行，无法诊断"))
		return
	}
	channel, err := diagChannel(r)
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	port := a.core.Status().Ports.AIDedicated
	if channel == "daily" {
		port = a.core.Status().Ports.Mixed
	}
	if port <= 0 {
		writeUserErr(rw, errors.New("内核端口未知，无法诊断"))
		return
	}
	run := diagnostics.RunDiagnostics
	if a.runDiag != nil {
		run = a.runDiag
	}
	rep := run("内置内核", "127.0.0.1", port)
	rep.Channel = channel
	rep.Port = port
	writeJSON(rw, http.StatusOK, rep)
}

func diagChannel(r *http.Request) (string, error) {
	ch := ""
	if r != nil && r.URL != nil {
		ch = strings.TrimSpace(r.URL.Query().Get("channel"))
	}
	if ch == "" && r != nil && r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut) {
		var body struct {
			Channel string `json:"channel"`
		}
		err := json.NewDecoder(r.Body).Decode(&body)
		if err != nil && !errors.Is(err, io.EOF) {
			return "", errors.New("请求格式不正确")
		}
		ch = body.Channel
	}
	switch strings.ToLower(strings.TrimSpace(ch)) {
	case "", "ai":
		return "ai", nil
	case "daily":
		return "daily", nil
	default:
		return "", errors.New("诊断通道只能是 ai 或 daily")
	}
}

func managedApplyOptions(cfg configstore.AppConfig, port int, syncUserEnv bool) configurator.ApplyOptions {
	return configurator.ApplyOptions{
		ProxyURL:    fmt.Sprintf("http://127.0.0.1:%d", port),
		Cursor:      cfg.IsAppEnabled("cursor"),
		Antigravity: cfg.IsAppEnabled("antigravity"),
		ClaudeCode:  cfg.IsAppEnabled("claude_code"),
		Codex:       cfg.IsAppEnabled("codex"),
		SyncUserEnv: syncUserEnv,
	}
}
