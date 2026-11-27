package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"proxy-box/internal/clashscript"
	"proxy-box/internal/configstore"
	"proxy-box/internal/core"
	"proxy-box/internal/detector"
	"proxy-box/internal/mihomo"
	"proxy-box/internal/subscription"
)

var subIDRe = regexp.MustCompile(`^[a-z0-9_-]+$`)

type coreSettingsView struct {
	Enabled         bool   `json:"enabled"`
	MixedPort       int    `json:"mixedPort"`
	ControllerPort  int    `json:"controllerPort"`
	AIDedicatedPort int    `json:"aiDedicatedPort"`
	Mode            string `json:"mode"`
	BypassCN        bool   `json:"bypassCN"`
	SystemProxy     bool   `json:"systemProxy"`
}

type coreStatusResponse struct {
	OK              bool             `json:"ok"`
	Running         bool             `json:"running"`
	PID             int              `json:"pid"`
	Version         string           `json:"version"`
	Mode            string           `json:"mode"`
	MixedPort       int              `json:"mixedPort"`
	ControllerPort  int              `json:"controllerPort"`
	AIDedicatedPort int              `json:"aiDedicatedPort"`
	Settings        coreSettingsView `json:"settings"`
}

type subsResponse struct {
	Subscriptions []configstore.Subscription `json:"subscriptions"`
}

type subResponse struct {
	OK           bool                     `json:"ok"`
	Subscription configstore.Subscription `json:"subscription"`
}

type modeResponse struct {
	OK     bool   `json:"ok"`
	Mode   string `json:"mode"`
	Global string `json:"global,omitempty"`
}

type sysproxyResponse struct {
	OK          bool `json:"ok"`
	SystemProxy bool `json:"systemProxy"`
}

type importResponse struct {
	OK   bool   `json:"ok"`
	Path string `json:"path"`
}

type browseResponse struct {
	Path string `json:"path"`
}

type nodeDelayResponse struct {
	OK       bool   `json:"ok"`
	NodeName string `json:"nodeName"`
	DelayMs  int    `json:"delayMs"`
	Tested   bool   `json:"tested"`
}

func viewSettings(s configstore.CoreSettings) coreSettingsView {
	return coreSettingsView{
		Enabled:         s.Enabled,
		MixedPort:       s.MixedPort,
		ControllerPort:  s.ControllerPort,
		AIDedicatedPort: s.AIDedicatedPort,
		Mode:            s.Mode,
		BypassCN:        s.BypassCN,
		SystemProxy:     s.SystemProxy,
	}
}

func (a *appRuntime) register(mux *http.ServeMux) {
	mux.HandleFunc("/api/subs", a.handleSubs)
	mux.HandleFunc("/api/subs/update", a.handleSubsUpdate)
	mux.HandleFunc("/api/core/status", a.handleCoreStatus)
	mux.HandleFunc("/api/core/start", a.handleCoreStart)
	mux.HandleFunc("/api/core/stop", a.handleCoreStop)
	mux.HandleFunc("/api/core/mode", a.handleCoreMode)
	mux.HandleFunc("/api/core/import-binary", a.handleImportBinary)
	mux.HandleFunc("/api/browse-core", a.handleBrowseCore)
	mux.HandleFunc("/api/sysproxy/set", a.handleSysproxySet)
	mux.HandleFunc("/api/nodes/list", a.handleNodesList)
	mux.HandleFunc("/api/nodes/select", a.handleNodesSelect)
	mux.HandleFunc("/api/nodes/test", a.handleNodesTest)
}

func (a *appRuntime) coreStatusResponse() (coreStatusResponse, error) {
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		return coreStatusResponse{}, err
	}
	st := core.Status{}
	if a != nil && a.core != nil {
		st = a.core.Status()
	}
	mode := settings.Mode
	mixed, ctrl, ai := settings.MixedPort, settings.ControllerPort, settings.AIDedicatedPort
	if st.Mode != "" {
		mode = st.Mode
	}
	if st.Running {
		if st.Ports.Mixed > 0 {
			mixed = st.Ports.Mixed
		}
		if st.Ports.Controller > 0 {
			ctrl = st.Ports.Controller
		}
		if st.Ports.AIDedicated > 0 {
			ai = st.Ports.AIDedicated
		}
	}
	settings.Mode = mode
	return coreStatusResponse{
		OK:              true,
		Running:         st.Running,
		PID:             st.PID,
		Version:         st.Version,
		Mode:            mode,
		MixedPort:       mixed,
		ControllerPort:  ctrl,
		AIDedicatedPort: ai,
		Settings:        viewSettings(settings),
	}, nil
}

func (a *appRuntime) handleCoreStatus(rw http.ResponseWriter, r *http.Request) {
	body, err := a.coreStatusResponse()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, body)
}

func (a *appRuntime) handleCoreStart(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeUserErr(rw, errors.New("请使用 POST"))
		return
	}
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	settings.Enabled = true
	if _, err := a.startCore(r.Context(), settings); err != nil {
		if errors.Is(err, core.ErrAlreadyRunning) {
			body, statusErr := a.coreStatusResponse()
			if statusErr != nil {
				writeUserErr(rw, statusErr)
				return
			}
			writeJSON(rw, http.StatusOK, body)
			return
		}
		writeErr(rw, errors.New(startFailure(err)))
		return
	}
	body, err := a.coreStatusResponse()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, body)
}

func (a *appRuntime) handleCoreStop(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeUserErr(rw, errors.New("请使用 POST"))
		return
	}
	a.Shutdown()
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	settings.Enabled = false
	if err := configstore.SaveCoreSettings(settings); err != nil {
		writeUserErr(rw, err)
		return
	}
	a.broadcastCore("stopped", "内核已停止", 0, 0)
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true})
}

func (a *appRuntime) handleCoreMode(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPatch {
		writeUserErr(rw, errors.New("请使用 POST"))
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUserErr(rw, err)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if err := a.core.SwitchMode(r.Context(), mode); err != nil {
		writeUserErr(rw, err)
		return
	}
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	settings.Mode = mode
	if err := configstore.SaveCoreSettings(settings); err != nil {
		writeUserErr(rw, err)
		return
	}
	resp := modeResponse{OK: true, Mode: mode}
	if mode == core.ModeGlobal {
		target, selErr := a.followGlobal()
		if selErr != nil {
			writeUserErr(rw, selErr)
			return
		}
		resp.Global = target
	}
	writeJSON(rw, http.StatusOK, resp)
}

func (a *appRuntime) followGlobal() (string, error) {
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		return "", err
	}
	st := a.core.Status()
	mc := mihomo.NewClient("", fmt.Sprintf("127.0.0.1:%d", st.Ports.Controller), settings.Secret)
	if !mc.Available() {
		return "", errors.New("未能连接到内置内核")
	}
	target := core.ProxyGroupName
	if res, listErr := mc.ListGroupNodes(core.ProxyGroupName, false); listErr == nil && res != nil && strings.TrimSpace(res.CurrentSelect) != "" {
		target = res.CurrentSelect
	}
	if err := mc.SelectGroupNode("GLOBAL", target); err != nil {
		if target != core.ProxyGroupName {
			if err2 := mc.SelectGroupNode("GLOBAL", core.ProxyGroupName); err2 == nil {
				return core.ProxyGroupName, nil
			}
		}
		return "", err
	}
	return target, nil
}

func (a *appRuntime) handleImportBinary(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeUserErr(rw, errors.New("请使用 POST"))
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUserErr(rw, err)
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		writeUserErr(rw, errors.New("请选择 mihomo.exe 或 zip 压缩包"))
		return
	}
	path, err := a.importBinary(req.Path, a.coreDir)
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, importResponse{OK: true, Path: path})
}

func (a *appRuntime) handleBrowseCore(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, http.StatusOK, browseResponse{Path: openFileDialogCore()})
}

func (a *appRuntime) handleSysproxySet(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeUserErr(rw, errors.New("请使用 POST"))
		return
	}
	var req struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUserErr(rw, err)
		return
	}
	if req.Enable && (a.core == nil || !a.core.Status().Running) {
		writeUserErr(rw, errors.New("内核未运行，不能开启系统代理"))
		return
	}
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	if req.Enable {
		port := settings.MixedPort
		if st := a.core.Status(); st.Ports.Mixed > 0 {
			port = st.Ports.Mixed
		}
		if err := a.setProxy("127.0.0.1", port, ""); err != nil {
			writeUserErr(rw, err)
			return
		}
		settings.SystemProxy = true
	} else {
		if a.restore != nil {
			if err := a.restore(); err != nil {
				writeUserErr(rw, err)
				return
			}
		}
		settings.SystemProxy = false
	}
	if err := configstore.SaveCoreSettings(settings); err != nil {
		writeUserErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, sysproxyResponse{OK: true, SystemProxy: settings.SystemProxy})
}

func (a *appRuntime) handleSubs(rw http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		subs, err := configstore.LoadSubscriptions()
		if err != nil {
			writeUserErr(rw, err)
			return
		}
		writeJSON(rw, http.StatusOK, subsResponse{Subscriptions: subs})
	case http.MethodPost:
		a.createSub(rw, r)
	case http.MethodPut:
		a.updateSubFields(rw, r)
	case http.MethodDelete:
		a.deleteSub(rw, r)
	default:
		writeUserErr(rw, errors.New("不支持的请求方法"))
	}
}

func (a *appRuntime) createSub(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Name              string `json:"name"`
		URL               string `json:"url"`
		UserAgent         string `json:"userAgent"`
		Filter            string `json:"filter"`
		Enabled           *bool  `json:"enabled"`
		UpdateIntervalMin int    `json:"updateIntervalMin"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeUserErr(rw, err)
		return
	}
	if err := validateSubURL(req.URL); err != nil {
		writeUserErr(rw, err)
		return
	}
	id, err := configstore.NewSubscriptionID()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "订阅"
	}
	sub := configstore.Subscription{
		ID:                id,
		Name:              name,
		URL:               strings.TrimSpace(req.URL),
		UserAgent:         strings.TrimSpace(req.UserAgent),
		Filter:            req.Filter,
		Enabled:           enabled,
		UpdateIntervalMin: nonNegative(req.UpdateIntervalMin),
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	subs, err := configstore.LoadSubscriptions()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	subs = append(subs, sub)
	if err := configstore.SaveSubscriptions(subs); err != nil {
		writeUserErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, subResponse{OK: true, Subscription: sub})
}

func (a *appRuntime) updateSubFields(rw http.ResponseWriter, r *http.Request) {
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &raw); err != nil {
		writeUserErr(rw, err)
		return
	}
	var id string
	if v, ok := raw["id"]; ok {
		if err := json.Unmarshal(v, &id); err != nil {
			writeUserErr(rw, err)
			return
		}
	}
	id = strings.TrimSpace(id)
	a.opMu.Lock()
	defer a.opMu.Unlock()
	subs, err := configstore.LoadSubscriptions()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	idx := subIndex(subs, id)
	if idx < 0 {
		writeUserErr(rw, errors.New("订阅不存在"))
		return
	}
	sub := subs[idx]
	if v, ok := raw["name"]; ok {
		if err := json.Unmarshal(v, &sub.Name); err != nil {
			writeUserErr(rw, err)
			return
		}
	}
	if v, ok := raw["url"]; ok {
		var next string
		if err := json.Unmarshal(v, &next); err != nil {
			writeUserErr(rw, err)
			return
		}
		if err := validateSubURL(next); err != nil {
			writeUserErr(rw, err)
			return
		}
		sub.URL = strings.TrimSpace(next)
	}
	if v, ok := raw["userAgent"]; ok {
		if err := json.Unmarshal(v, &sub.UserAgent); err != nil {
			writeUserErr(rw, err)
			return
		}
	}
	if v, ok := raw["filter"]; ok {
		if err := json.Unmarshal(v, &sub.Filter); err != nil {
			writeUserErr(rw, err)
			return
		}
	}
	if v, ok := raw["enabled"]; ok {
		if err := json.Unmarshal(v, &sub.Enabled); err != nil {
			writeUserErr(rw, err)
			return
		}
	}
	if v, ok := raw["updateIntervalMin"]; ok {
		if err := json.Unmarshal(v, &sub.UpdateIntervalMin); err != nil {
			writeUserErr(rw, err)
			return
		}
		sub.UpdateIntervalMin = nonNegative(sub.UpdateIntervalMin)
	}
	subs[idx] = sub
	if err := configstore.SaveSubscriptions(subs); err != nil {
		writeUserErr(rw, err)
		return
	}
	if err := a.reloadIfRunning(r.Context(), subs); err != nil {
		writeUserErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, subResponse{OK: true, Subscription: sub})
}

func (a *appRuntime) deleteSub(rw http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		var req struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req)
		id = strings.TrimSpace(req.ID)
	}
	if !subIDRe.MatchString(id) {
		writeUserErr(rw, errors.New("订阅标识无效"))
		return
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	subs, err := configstore.LoadSubscriptions()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	idx := subIndex(subs, id)
	if idx < 0 {
		writeUserErr(rw, errors.New("订阅不存在"))
		return
	}
	subs = append(subs[:idx], subs[idx+1:]...)
	if err := configstore.SaveSubscriptions(subs); err != nil {
		writeUserErr(rw, err)
		return
	}
	_ = os.Remove(filepath.Join(a.coreDir, "providers", id+".txt"))
	if err := a.reloadIfRunning(r.Context(), subs); err != nil {
		writeUserErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true})
}

func (a *appRuntime) handleSubsUpdate(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeUserErr(rw, errors.New("请使用 POST"))
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req)
	}
	id := strings.TrimSpace(req.ID)
	subs, err := configstore.LoadSubscriptions()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	var targets []configstore.Subscription
	for _, sub := range subs {
		if (id == "" && sub.Enabled) || (id != "" && sub.ID == id) {
			targets = append(targets, sub)
		}
	}
	if id != "" && len(targets) == 0 {
		writeUserErr(rw, errors.New("订阅不存在"))
		return
	}
	if len(targets) == 0 {
		writeUserErr(rw, errors.New("没有启用的订阅"))
		return
	}
	dir := filepath.Join(a.coreDir, "providers")
	proxies := a.candidateProxies()
	updated := make([]configstore.Subscription, 0, len(targets))
	for _, sub := range targets {
		res, upErr := subscription.Update(r.Context(), sub, proxies, dir)
		if upErr != nil {
			sub.LastError = publicErr(upErr)
			_ = a.saveSub(sub)
			writeUserErr(rw, upErr)
			return
		}
		sub.NodeCount = res.NodeCount
		sub.Usage = res.Usage
		sub.UpdatedAt = time.Now()
		sub.LastError = ""
		if err := a.saveSub(sub); err != nil {
			writeUserErr(rw, err)
			return
		}
		updated = append(updated, sub)
	}
	a.opMu.Lock()
	subs, err = configstore.LoadSubscriptions()
	if err != nil {
		a.opMu.Unlock()
		writeUserErr(rw, err)
		return
	}
	if err := a.reloadIfRunning(r.Context(), subs); err != nil {
		a.opMu.Unlock()
		writeUserErr(rw, err)
		return
	}
	subs, err = configstore.LoadSubscriptions()
	a.opMu.Unlock()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	if id != "" {
		if idx := subIndex(subs, id); idx >= 0 {
			writeJSON(rw, http.StatusOK, subResponse{OK: true, Subscription: subs[idx]})
			return
		}
	}
	writeJSON(rw, http.StatusOK, subsResponse{Subscriptions: updated})
}

func (a *appRuntime) saveSub(sub configstore.Subscription) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	subs, err := configstore.LoadSubscriptions()
	if err != nil {
		return err
	}
	idx := subIndex(subs, sub.ID)
	if idx < 0 {
		subs = append(subs, sub)
	} else {
		subs[idx] = sub
	}
	return configstore.SaveSubscriptions(subs)
}

func (a *appRuntime) reloadIfRunning(ctx context.Context, subs []configstore.Subscription) error {
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
	if err := a.core.Reload(ctx, coreParams(settings), providerSpecs(subs)); err != nil {
		return err
	}
	counts, err := providerNodeCounts(ctx, a.core.Status().Ports.Controller, settings.Secret)
	if err != nil {
		return nil
	}
	changed := false
	for i := range subs {
		n, ok := counts[subs[i].ID]
		if !ok || subs[i].NodeCount == n {
			continue
		}
		subs[i].NodeCount = n
		changed = true
	}
	if !changed {
		return nil
	}
	return configstore.SaveSubscriptions(subs)
}

func (a *appRuntime) handleNodesList(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		TestDelay bool   `json:"testDelay"`
		Group     string `json:"group"`
		Client    string `json:"client"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	snap := a.snapshot()
	client, err := nodeClient(snap, req.Client)
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	res, err := listClientGroup(client, req.Group, req.TestDelay)
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, res)
}

func (a *appRuntime) handleNodesTest(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeUserErr(rw, errors.New("请使用 POST"))
		return
	}
	var req struct {
		Client   string `json:"client"`
		NodeName string `json:"nodeName"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeUserErr(rw, err)
		return
	}
	name := strings.TrimSpace(req.NodeName)
	if name == "" {
		writeUserErr(rw, errors.New("请指定节点"))
		return
	}
	client, err := nodeClient(a.snapshot(), req.Client)
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	mc, err := groupClient(client)
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, nodeDelayResponse{
		OK:       true,
		NodeName: name,
		DelayMs:  mc.TestNodeDelay(name),
		Tested:   true,
	})
}

func (a *appRuntime) handleNodesSelect(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeName string `json:"nodeName"`
		Group    string `json:"group"`
		Client   string `json:"client"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUserErr(rw, err)
		return
	}
	snap := a.snapshot()
	client, err := nodeClient(snap, req.Client)
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	live, err := a.selectClientGroup(client, req.Group, req.NodeName)
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	group := strings.TrimSpace(req.Group)
	if group == "" || group == mihomo.AIGroupName {
		cfg := configstore.LoadConfig()
		if req.NodeName == clashscript.AIAutoName {
			cfg.NodeSelectionMode = "auto"
			cfg.ManualNodeName = ""
		} else {
			cfg.NodeSelectionMode = "manual"
			cfg.ManualNodeName = req.NodeName
		}
		_ = configstore.SaveConfig(cfg)
	}
	fresh := a.snapshot()
	msg := nodeSelectMessage(group, req.NodeName, live)
	if a.hub != nil {
		a.hub.broadcast(sseEvent{
			Type:       "sync",
			Reason:     msg,
			ShouldDiag: true,
			Snapshot:   &fresh,
		})
	}
	writeJSON(rw, http.StatusOK, map[string]any{
		"ok":       true,
		"message":  msg,
		"live":     live,
		"snapshot": fresh,
	})
}

func nodeClient(snap detector.SystemSnapshot, clientID string) (detector.ProxyClientInfo, error) {
	if strings.TrimSpace(clientID) != "builtin" {
		return snap.ActiveClient, nil
	}
	for _, client := range snap.Clients {
		if client.ID != "builtin" {
			continue
		}
		if !client.Running {
			return detector.ProxyClientInfo{}, errors.New("内置内核未运行")
		}
		return client, nil
	}
	return detector.ProxyClientInfo{}, errors.New("内置内核未运行")
}

func listClientGroup(client detector.ProxyClientInfo, group string, testDelay bool) (*mihomo.NodeListResult, error) {
	group = strings.TrimSpace(group)
	if group == "" || group == mihomo.AIGroupName {
		return detector.ListClientNodes(client, testDelay)
	}
	mc, err := groupClient(client)
	if err != nil {
		return nil, err
	}
	return mc.ListGroupNodes(group, testDelay)
}

func (a *appRuntime) selectClientGroup(client detector.ProxyClientInfo, group, node string) (*mihomo.LiveNodeStatus, error) {
	node = strings.TrimSpace(node)
	if node == "" {
		return nil, errors.New("请指定节点")
	}
	group = strings.TrimSpace(group)
	if group == "" {
		group = mihomo.AIGroupName
	}
	mc, err := groupClient(client)
	if err != nil {
		return nil, err
	}
	if err := mc.SelectGroupNode(group, node); err != nil {
		return nil, err
	}
	if group == core.ProxyGroupName && a.coreMode() == core.ModeGlobal {
		if err := mc.SelectGroupNode("GLOBAL", node); err != nil {
			if err2 := mc.SelectGroupNode("GLOBAL", core.ProxyGroupName); err2 != nil {
				return nil, err
			}
		}
	}
	live := mc.GetLiveStatus()
	return &live, nil
}

func groupClient(client detector.ProxyClientInfo) (*mihomo.Client, error) {
	if client.ID == "v2rayn" {
		return nil, errors.New("v2rayN 暂不支持按组选择节点")
	}
	mc := mihomo.NewClient(client.ControllerPipe, client.ExternalController, client.Secret)
	if !mc.Available() {
		return nil, fmt.Errorf("未能连接到 %s 内核控制接口", client.Name)
	}
	return mc, nil
}

func nodeSelectMessage(group, node string, live *mihomo.LiveNodeStatus) string {
	if group == "" || group == mihomo.AIGroupName {
		resolved := node
		if live != nil && live.ResolvedNode != "" {
			resolved = live.ResolvedNode
		}
		if node == clashscript.AIAutoName {
			return fmt.Sprintf("Proxy Box 专属通道已切回自动优选：%s", resolved)
		}
		return fmt.Sprintf("Proxy Box 专属通道已锁定节点：%s（外部代理软件节点不受影响）", resolved)
	}
	shown := node
	if node == core.BrowsingAutoName && live != nil && live.ExternalNode != "" {
		shown = live.ExternalNode
	}
	return fmt.Sprintf("已切换 %s 节点：%s", group, shown)
}

func validateSubURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("订阅地址无效，需要 http 或 https 链接")
	}
	return nil
}

func subIndex(subs []configstore.Subscription, id string) int {
	for i := range subs {
		if subs[i].ID == id {
			return i
		}
	}
	return -1
}

func nonNegative(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func writeUserErr(rw http.ResponseWriter, err error) {
	writeErr(rw, errors.New(publicErr(err)))
}
