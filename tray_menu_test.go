package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"proxy-box/internal/configstore"
	"proxy-box/internal/core"
	"proxy-box/internal/tray"
)

func TestBuildTrayMenu(t *testing.T) {
	nodes := make([]TrayNode, 30)
	for i := range nodes {
		nodes[i] = TrayNode{Name: "n" + strconv.Itoa(i), Selected: i == 4}
	}
	items := buildTrayMenu(TrayMenuState{
		SystemProxyActive: true,
		Mode:              "GLOBAL",
		CoreRunning:       true,
		NodesLoaded:       true,
		Nodes:             nodes,
	})

	sys, ok := findTrayItem(items, tray.MenuSysProxy)
	if !ok || sys.Label != "系统代理" || !sys.Checked || sys.Arg != "0" {
		t.Fatalf("system proxy %+v", sys)
	}
	mode := menuByLabel(t, items, "路由模式")
	if len(mode.Children) != 3 {
		t.Fatalf("modes %+v", mode.Children)
	}
	rule, _ := findTrayItem(items, tray.MenuModeRule)
	global, _ := findTrayItem(items, tray.MenuModeGlobal)
	direct, _ := findTrayItem(items, tray.MenuModeDirect)
	if rule.Checked || !global.Checked || direct.Checked || rule.Arg != "rule" || global.Arg != "global" || direct.Arg != "direct" {
		t.Fatalf("checks rule %+v global %+v direct %+v", rule, global, direct)
	}
	nodeMenu := menuByLabel(t, items, "节点")
	if len(nodeMenu.Children) != 30 {
		t.Fatalf("node count %d", len(nodeMenu.Children))
	}
	picked, ok := findTrayItem(items, tray.MenuNodeBase+4)
	if !ok || picked.Arg != "n4" || picked.Label != "n4" || !picked.Checked {
		t.Fatalf("picked %+v", picked)
	}
	other, _ := findTrayItem(items, tray.MenuNodeBase)
	if other.Arg != "n0" || other.Checked {
		t.Fatalf("first node %+v", other)
	}
	if picked.ID < tray.MenuNodeBase || picked.ID >= tray.MenuNodeBase+tray.MenuNodeLimit {
		t.Fatalf("node id %d outside %d", picked.ID, tray.MenuNodeBase)
	}
	assertStandardTail(t, items)

	tooMany := append([]TrayNode{}, nodes...)
	tooMany = append(tooMany, TrayNode{Name: "extra"})
	hidden := buildTrayMenu(TrayMenuState{Mode: "rule", CoreRunning: true, NodesLoaded: true, Nodes: tooMany})
	if _, ok := findMenuLabel(hidden, "节点"); ok {
		t.Fatal("31 nodes still showed the node menu")
	}
	if item, ok := findTrayItem(hidden, tray.MenuModeRule); !ok || !item.Checked {
		t.Fatalf("rule %+v", item)
	}

	stopped := buildTrayMenu(TrayMenuState{Mode: "direct", CoreRunning: false, NodesLoaded: true, Nodes: nodes[:2]})
	if _, ok := findMenuLabel(stopped, "节点"); ok {
		t.Fatal("stopped core showed nodes")
	}
	if item, ok := findTrayItem(stopped, tray.MenuModeDirect); !ok || !item.Checked {
		t.Fatalf("direct %+v", item)
	}
	off, ok := findTrayItem(stopped, tray.MenuSysProxy)
	if !ok || off.Checked || off.Arg != "1" {
		t.Fatalf("unchecked proxy %+v", off)
	}

	failed := buildTrayMenu(TrayMenuState{CoreRunning: true, NodesLoaded: false, Nodes: nodes[:2]})
	if _, ok := findMenuLabel(failed, "节点"); ok || len(findMenuLabelOrEmpty(failed, "路由模式").Children) != 0 {
		t.Fatal("failed state showed a submenu")
	}
	assertStandardTail(t, failed)
}

func TestNodesFromProxies(t *testing.T) {
	body := []byte(`{"proxies":{
		"PROXY":{"type":"Selector","now":"alpha","all":["⚡ AUTO","DIRECT","alpha","SKIP"]},
		"⚡ AUTO":{"type":"URLTest","now":"alpha"},
		"DIRECT":{"type":"Direct"},
		"alpha":{"type":"HTTP"},
		"SKIP":{"type":"Selector"}
	}}`)
	nodes, err := nodesFromProxies(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 || nodes[0].Name != "⚡ AUTO" || nodes[1].Name != "DIRECT" || !nodes[2].Selected || nodes[2].Name != "alpha" {
		t.Fatalf("nodes %+v", nodes)
	}
	if _, err := nodesFromProxies([]byte(`{"proxies":{}}`), nil); err == nil {
		t.Fatal("missing PROXY group")
	}
}

func TestTrayStateTimesOutAndCaches(t *testing.T) {
	useTempConfig(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, `{"proxies":{"PROXY":{"type":"Selector","now":"alpha","all":["alpha"]},"alpha":{"type":"HTTP"}}}`)
	}))
	defer srv.Close()
	rt := &appRuntime{core: &fakeCore{running: true, mode: core.ModeGlobal, ports: core.Ports{Controller: mustPort(t, srv.URL)}}}
	state := rt.cachedTrayState()
	if !state.CoreRunning || !state.NodesLoaded || state.Mode != core.ModeGlobal || len(state.Nodes) != 1 || !state.Nodes[0].Selected {
		t.Fatalf("state %+v", state)
	}
	if hits.Load() != 2 {
		t.Fatalf("hits %d", hits.Load())
	}
	again := rt.cachedTrayState()
	if hits.Load() != 2 || again.Nodes[0].Name != "alpha" {
		t.Fatalf("cache hits %d state %+v", hits.Load(), again)
	}

	stall := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer stall.Close()
	slow := &appRuntime{core: &fakeCore{running: true, mode: core.ModeRule, ports: core.Ports{Controller: mustPort(t, stall.URL)}}}
	start := time.Now()
	miss := slow.loadTrayMenuState()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("controller wait %s", elapsed)
	}
	if miss.NodesLoaded || miss.Mode != core.ModeRule || !miss.CoreRunning {
		t.Fatalf("stall state %+v", miss)
	}
	items := buildTrayMenu(miss)
	if _, ok := findMenuLabel(items, "节点"); ok {
		t.Fatal("stall showed nodes")
	}
	if _, ok := findMenuLabel(items, "路由模式"); !ok {
		t.Fatal("stall hid the mode menu")
	}
}

func TestTrayMenuIncludesProviderNodes(t *testing.T) {
	useTempConfig(t)
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Secret = "tray-secret"
	if err := configstore.SaveCoreSettings(settings); err != nil {
		t.Fatal(err)
	}
	proxies := `{
		"proxies": {
			"PROXY": {"type":"Selector","now":"prov-b","all":["prov-b","skip-group","local","⚡ AUTO","prov-a"]},
			"local": {"type":"HTTP"}
		}
	}`
	providers := `{
		"providers": {
			"late": {"proxies":[
				{"name":"prov-a","type":"SS"},
				{"name":"skip-group","type":"Selector"},
				{"name":"⚡ AUTO","type":"URLTest"}
			]},
			"early": {"proxies":[
				{"name":"prov-b","type":"VMess"},
				{"name":"local","type":"Trojan"}
			]}
		}
	}`
	var authMiss atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tray-secret" {
			authMiss.Add(1)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/proxies":
			_, _ = io.WriteString(w, proxies)
		case "/providers/proxies":
			_, _ = io.WriteString(w, providers)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	rt := &appRuntime{core: &fakeCore{running: true, mode: core.ModeRule, ports: core.Ports{Controller: mustPort(t, srv.URL)}}}
	state := rt.loadTrayMenuState()
	want := []TrayNode{
		{Name: "prov-b", Selected: true},
		{Name: "local", Selected: false},
		{Name: "⚡ AUTO", Selected: false},
		{Name: "prov-a", Selected: false},
	}
	if authMiss.Load() != 0 || !state.NodesLoaded || len(state.Nodes) != len(want) {
		t.Fatalf("authMiss %d state %+v", authMiss.Load(), state)
	}
	for i := range want {
		if state.Nodes[i] != want[i] {
			t.Fatalf("nodes %+v", state.Nodes)
		}
	}
	items := buildTrayMenu(state)
	menu := menuByLabel(t, items, "节点")
	if len(menu.Children) != len(want) {
		t.Fatalf("children %+v", menu.Children)
	}
	picked, ok := findTrayItem(items, tray.MenuNodeBase)
	if !ok || picked.Label != "prov-b" || picked.Arg != "prov-b" || !picked.Checked {
		t.Fatalf("checked %+v", picked)
	}
	second, _ := findTrayItem(items, tray.MenuNodeBase+1)
	if second.Label != "local" || second.Checked {
		t.Fatalf("second %+v", second)
	}
	if _, ok := findTrayItem(items, tray.MenuNodeBase+len(want)); ok {
		t.Fatal("selector group was listed")
	}
}

func TestTrayMenuHidesWhenProviderNodesExceedLimit(t *testing.T) {
	useTempConfig(t)
	showProxies, showProviders := trayControllerBodies(0, 30, "prov-03")
	show := loadTrayNodes(t, showProxies, showProviders, http.StatusOK)
	if len(show.Nodes) != 30 || !show.NodesLoaded || !show.Nodes[3].Selected || show.Nodes[3].Name != "prov-03" {
		t.Fatalf("30 provider nodes %+v", show)
	}
	for i, node := range show.Nodes {
		if node.Name != fmt.Sprintf("prov-%02d", i) {
			t.Fatalf("order %+v", show.Nodes)
		}
	}
	shown := buildTrayMenu(show)
	menu := menuByLabel(t, shown, "节点")
	if len(menu.Children) != 30 {
		t.Fatalf("menu %d", len(menu.Children))
	}
	picked, ok := findTrayItem(shown, tray.MenuNodeBase+3)
	if !ok || !picked.Checked || picked.Label != "prov-03" {
		t.Fatalf("checked %+v", picked)
	}

	hideProxies, hideProviders := trayControllerBodies(20, 11, "prov-00")
	hide := loadTrayNodes(t, hideProxies, hideProviders, http.StatusOK)
	if !hide.NodesLoaded || len(hide.Nodes) != 31 || hide.Nodes[0].Name != "local-00" || hide.Nodes[30].Name != "prov-10" {
		t.Fatalf("31 nodes %+v", hide.Nodes)
	}
	if _, ok := findMenuLabel(buildTrayMenu(hide), "节点"); ok {
		t.Fatal("31 nodes still showed the node menu")
	}
}

func TestTrayMenuKeepsProxiesWhenProvidersFail(t *testing.T) {
	useTempConfig(t)
	proxies := `{"proxies":{"PROXY":{"type":"Selector","now":"alpha","all":["alpha","only-provider"]},"alpha":{"type":"HTTP"}}}`
	providers := `{"providers":{"sub":{"proxies":[{"name":"only-provider","type":"SS"}]}}}`

	miss := loadTrayNodes(t, proxies, providers, http.StatusNotFound)
	if !miss.NodesLoaded || len(miss.Nodes) != 1 || miss.Nodes[0].Name != "alpha" || !miss.Nodes[0].Selected {
		t.Fatalf("404 state %+v", miss)
	}
	if _, ok := findMenuLabel(buildTrayMenu(miss), "节点"); !ok {
		t.Fatal("404 hid the node menu")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/providers/proxies" {
			time.Sleep(2 * time.Second)
			return
		}
		_, _ = io.WriteString(w, proxies)
	}))
	defer srv.Close()
	rt := &appRuntime{core: &fakeCore{running: true, mode: core.ModeRule, ports: core.Ports{Controller: mustPort(t, srv.URL)}}}
	start := time.Now()
	late := rt.loadTrayMenuState()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("providers wait %s", elapsed)
	}
	if !late.NodesLoaded || len(late.Nodes) != 1 || late.Nodes[0].Name != "alpha" || !late.Nodes[0].Selected {
		t.Fatalf("timeout state %+v", late)
	}
	if _, ok := findMenuLabel(buildTrayMenu(late), "节点"); !ok {
		t.Fatal("timeout hid the node menu")
	}
}

func loadTrayNodes(t *testing.T, proxies, providers string, providersStatus int) TrayMenuState {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/proxies":
			_, _ = io.WriteString(w, proxies)
		case "/providers/proxies":
			if providersStatus != 0 && providersStatus != http.StatusOK {
				w.WriteHeader(providersStatus)
			}
			_, _ = io.WriteString(w, providers)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	rt := &appRuntime{core: &fakeCore{running: true, mode: core.ModeRule, ports: core.Ports{Controller: mustPort(t, srv.URL)}}}
	return rt.loadTrayMenuState()
}

func trayControllerBodies(localN, provN int, now string) (string, string) {
	all := make([]string, 0, localN+provN)
	proxies := map[string]any{}
	for i := 0; i < localN; i++ {
		name := fmt.Sprintf("local-%02d", i)
		all = append(all, name)
		proxies[name] = map[string]any{"type": "HTTP"}
	}
	prov := make([]map[string]string, 0, provN)
	for i := 0; i < provN; i++ {
		name := fmt.Sprintf("prov-%02d", i)
		all = append(all, name)
		prov = append(prov, map[string]string{"name": name, "type": "SS"})
	}
	proxies["PROXY"] = map[string]any{"type": "Selector", "now": now, "all": all}
	proxyBody, _ := json.Marshal(map[string]any{"proxies": proxies})
	providerBody, _ := json.Marshal(map[string]any{
		"providers": map[string]any{"sub": map[string]any{"proxies": prov}},
	})
	return string(proxyBody), string(providerBody)
}

func TestTraySystemProxyClickRefusesWhenStopped(t *testing.T) {
	useTempConfig(t)
	called := false
	rt := &appRuntime{
		core: &fakeCore{},
		setProxy: func(string, int, string) error {
			called = true
			return nil
		},
	}
	var title, info string
	rt.handleTrayCommand(tray.MenuSysProxy, "1", func(gotTitle, gotInfo string) {
		title, info = gotTitle, gotInfo
	})
	if called || title != "系统代理" || !strings.Contains(info, "内核未运行") {
		t.Fatalf("called %v balloon %s %s", called, title, info)
	}
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.SystemProxy {
		t.Fatal("click enabled the system proxy")
	}
}

func menuByLabel(t *testing.T, items []tray.MenuItem, label string) tray.MenuItem {
	t.Helper()
	item, ok := findMenuLabel(items, label)
	if !ok {
		t.Fatalf("missing %s", label)
	}
	return item
}

func findMenuLabel(items []tray.MenuItem, label string) (tray.MenuItem, bool) {
	for _, item := range items {
		if item.Label == label {
			return item, true
		}
		if found, ok := findMenuLabel(item.Children, label); ok {
			return found, true
		}
	}
	return tray.MenuItem{}, false
}

func findMenuLabelOrEmpty(items []tray.MenuItem, label string) tray.MenuItem {
	item, _ := findMenuLabel(items, label)
	return item
}

func assertStandardTail(t *testing.T, items []tray.MenuItem) {
	t.Helper()
	std := tray.StandardItems()
	if len(items) < len(std)+1 {
		t.Fatalf("menu length %d", len(items))
	}
	sep := items[len(items)-len(std)-1]
	if !sep.Separator {
		t.Fatal("standard items lost their leading separator")
	}
	tail := items[len(items)-len(std):]
	for i := range std {
		if tail[i].ID != std[i].ID || tail[i].Label != std[i].Label || tail[i].Separator != std[i].Separator {
			t.Fatalf("tail[%d] = %+v want %+v", i, tail[i], std[i])
		}
	}
	show, ok := findTrayItem(items, tray.MenuShowWindow)
	quit, quitOK := findTrayItem(items, tray.MenuQuit)
	if !ok || !quitOK || show.Label == "" || quit.Label == "" {
		t.Fatal("existing tray commands missing")
	}
}
