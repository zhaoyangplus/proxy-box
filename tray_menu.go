package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"proxy-box/internal/configstore"
	"proxy-box/internal/core"
	"proxy-box/internal/tray"
)

const (
	trayStateTTL       = 1500 * time.Millisecond
	trayControllerWait = 300 * time.Millisecond
)

type trayStateCache struct {
	mu    sync.Mutex
	at    time.Time
	state TrayMenuState
	ok    bool
}

// TrayNode is one selectable entry in the daily PROXY group.
type TrayNode struct {
	Name     string
	Selected bool
}

// TrayMenuState is the snapshot the tray menu is built from.
// An empty Mode hides the mode submenu.
// Nodes are shown only when the core is running, the list loaded, and it has 1 to 30 entries.
type TrayMenuState struct {
	SystemProxyActive bool
	Mode              string
	CoreRunning       bool
	NodesLoaded       bool
	Nodes             []TrayNode
}

func buildTrayMenu(state TrayMenuState) []tray.MenuItem {
	sysArg := "1"
	if state.SystemProxyActive {
		sysArg = "0"
	}
	items := []tray.MenuItem{{
		ID:      tray.MenuSysProxy,
		Label:   "系统代理",
		Arg:     sysArg,
		Checked: state.SystemProxyActive,
	}}
	if mode := strings.ToLower(strings.TrimSpace(state.Mode)); mode != "" {
		items = append(items, tray.MenuItem{
			Label: "路由模式",
			Children: []tray.MenuItem{
				{ID: tray.MenuModeRule, Label: "规则", Arg: core.ModeRule, Checked: mode == core.ModeRule},
				{ID: tray.MenuModeGlobal, Label: "全局", Arg: core.ModeGlobal, Checked: mode == core.ModeGlobal},
				{ID: tray.MenuModeDirect, Label: "直连", Arg: core.ModeDirect, Checked: mode == core.ModeDirect},
			},
		})
	}
	if showTrayNodes(state) {
		children := make([]tray.MenuItem, len(state.Nodes))
		for i, node := range state.Nodes {
			children[i] = tray.MenuItem{
				ID:      tray.MenuNodeBase + i,
				Label:   node.Name,
				Arg:     node.Name,
				Checked: node.Selected,
			}
		}
		items = append(items, tray.MenuItem{Label: "节点", Children: children})
	}
	items = append(items, tray.MenuItem{Separator: true})
	items = append(items, tray.StandardItems()...)
	return items
}

func trayMenuCommand(menuID int) bool {
	switch menuID {
	case tray.MenuSysProxy, tray.MenuModeRule, tray.MenuModeGlobal, tray.MenuModeDirect:
		return true
	default:
		return menuID >= tray.MenuNodeBase && menuID < tray.MenuNodeBase+tray.MenuNodeLimit
	}
}

func showTrayNodes(state TrayMenuState) bool {
	n := len(state.Nodes)
	return state.CoreRunning && state.NodesLoaded && n > 0 && n <= tray.MenuNodeLimit
}

func findTrayItem(items []tray.MenuItem, id int) (tray.MenuItem, bool) {
	for _, item := range items {
		if item.ID == id && !item.Separator {
			return item, true
		}
		if found, ok := findTrayItem(item.Children, id); ok {
			return found, true
		}
	}
	return tray.MenuItem{}, false
}

func (a *appRuntime) invalidateTrayState() {
	if a == nil {
		return
	}
	a.trayState.mu.Lock()
	a.trayState.ok = false
	a.trayState.mu.Unlock()
}

func (a *appRuntime) cachedTrayState() TrayMenuState {
	if a == nil {
		return TrayMenuState{}
	}
	a.trayState.mu.Lock()
	if a.trayState.ok && time.Since(a.trayState.at) < trayStateTTL {
		state := a.trayState.state
		a.trayState.mu.Unlock()
		return state
	}
	a.trayState.mu.Unlock()

	state := a.loadTrayMenuState()
	a.trayState.mu.Lock()
	a.trayState.at = time.Now()
	a.trayState.state = state
	a.trayState.ok = true
	a.trayState.mu.Unlock()
	return state
}

func (a *appRuntime) loadTrayMenuState() TrayMenuState {
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		return TrayMenuState{}
	}
	st := core.Status{}
	if a != nil && a.core != nil {
		st = a.core.Status()
	}
	mode := strings.TrimSpace(settings.Mode)
	if strings.TrimSpace(st.Mode) != "" {
		mode = strings.TrimSpace(st.Mode)
	}
	state := TrayMenuState{
		SystemProxyActive: st.Running && settings.SystemProxy,
		Mode:              mode,
		CoreRunning:       st.Running,
	}
	if !st.Running || st.Ports.Controller <= 0 {
		return state
	}
	nodes, err := a.fetchProxyNodes(st.Ports.Controller, settings.Secret)
	if err != nil {
		return state
	}
	state.Nodes = nodes
	state.NodesLoaded = true
	return state
}

func (a *appRuntime) fetchProxyNodes(port int, secret string) ([]TrayNode, error) {
	ctx, cancel := context.WithTimeout(context.Background(), trayControllerWait)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	var (
		proxiesBody   []byte
		providersBody []byte
		proxiesCode   int
		providersCode int
		proxiesErr    error
		providersErr  error
		wg            sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		proxiesBody, proxiesCode, proxiesErr = readTrayController(ctx, client, port, secret, "/proxies")
	}()
	go func() {
		defer wg.Done()
		providersBody, providersCode, providersErr = readTrayController(ctx, client, port, secret, "/providers/proxies")
	}()
	wg.Wait()
	if proxiesErr != nil {
		return nil, proxiesErr
	}
	if proxiesCode < 200 || proxiesCode >= 300 {
		return nil, fmt.Errorf("proxy http %d", proxiesCode)
	}
	// Provider nodes are absent from /proxies. A failed providers call still returns that list.
	if providersErr != nil || providersCode < 200 || providersCode >= 300 {
		providersBody = nil
	}
	return nodesFromProxies(proxiesBody, providersBody)
}

func readTrayController(ctx context.Context, client *http.Client, port int, secret, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+path, nil)
	if err != nil {
		return nil, 0, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

type trayProxy struct {
	Type string   `json:"type"`
	Now  string   `json:"now"`
	All  []string `json:"all"`
}

func nodesFromProxies(proxiesBody, providersBody []byte) ([]TrayNode, error) {
	var doc struct {
		Proxies map[string]trayProxy `json:"proxies"`
	}
	if err := json.Unmarshal(proxiesBody, &doc); err != nil {
		return nil, err
	}
	if doc.Proxies == nil {
		doc.Proxies = map[string]trayProxy{}
	}
	mergeTrayProviders(doc.Proxies, providersBody)
	grp, ok := doc.Proxies[core.ProxyGroupName]
	if !ok {
		return nil, fmt.Errorf("代理组 %s 不存在", core.ProxyGroupName)
	}
	nodes := make([]TrayNode, 0, len(grp.All))
	for _, name := range grp.All {
		item, found := doc.Proxies[name]
		if !found {
			continue
		}
		switch strings.ToLower(item.Type) {
		case "selector", "loadbalance", "relay":
			continue
		}
		nodes = append(nodes, TrayNode{Name: name, Selected: name == grp.Now})
	}
	return nodes, nil
}

func mergeTrayProviders(proxies map[string]trayProxy, body []byte) {
	if len(body) == 0 || proxies == nil {
		return
	}
	var resp struct {
		Providers map[string]struct {
			Proxies []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"proxies"`
		} `json:"providers"`
	}
	if json.Unmarshal(body, &resp) != nil || len(resp.Providers) == 0 {
		return
	}
	for _, prov := range resp.Providers {
		for _, item := range prov.Proxies {
			if item.Name == "" {
				continue
			}
			if _, exists := proxies[item.Name]; exists {
				continue
			}
			proxies[item.Name] = trayProxy{Type: item.Type}
		}
	}
}

func (a *appRuntime) handleTrayCommand(menuID int, arg string, balloon func(title, info string)) {
	if balloon == nil {
		balloon = func(string, string) {}
	}
	switch menuID {
	case tray.MenuSysProxy:
		on, err := a.applySystemProxy(arg == "1")
		a.invalidateTrayState()
		if err != nil {
			balloon("系统代理", publicErr(err))
			return
		}
		if on {
			balloon("系统代理", "已开启系统代理")
			return
		}
		balloon("系统代理", "已关闭系统代理")
	case tray.MenuModeRule, tray.MenuModeGlobal, tray.MenuModeDirect:
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		resp, err := a.applyCoreMode(ctx, arg)
		a.invalidateTrayState()
		if err != nil {
			balloon("路由模式", publicErr(err))
			return
		}
		balloon("路由模式", modeBalloon(resp.Mode))
	default:
		if menuID < tray.MenuNodeBase || menuID >= tray.MenuNodeBase+tray.MenuNodeLimit {
			return
		}
		msg, _, _, err := a.selectRequestedNode("builtin", core.ProxyGroupName, arg)
		a.invalidateTrayState()
		if err != nil {
			balloon("节点", publicErr(err))
			return
		}
		balloon("节点", msg)
	}
}

func modeBalloon(mode string) string {
	switch mode {
	case core.ModeGlobal:
		return "已切换为全局模式"
	case core.ModeDirect:
		return "已切换为直连模式"
	default:
		return "已切换为规则模式"
	}
}
