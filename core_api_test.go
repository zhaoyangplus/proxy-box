package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"proxy-box/internal/configstore"
	"proxy-box/internal/core"
	"proxy-box/internal/detector"
	"proxy-box/internal/mihomo"
)

func useTempConfig(t *testing.T) {
	t.Helper()
	configstore.SetBaseDir(t.TempDir())
	t.Cleanup(func() { configstore.SetBaseDir("") })
}

func mustAppConfig(t *testing.T) configstore.AppConfig {
	t.Helper()
	cfg, err := configstore.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestResponsesOmitSecret(t *testing.T) {
	useTempConfig(t)
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	secret := settings.Secret
	if secret == "" {
		t.Fatal("empty secret")
	}
	status, err := (&appRuntime{core: &fakeCore{running: true, mode: core.ModeRule}}).coreStatusResponse()
	if err != nil {
		t.Fatal(err)
	}
	sub := configstore.Subscription{ID: "abc", Name: "local", URL: "http://127.0.0.1/sub", Enabled: true}
	snap := detector.SystemSnapshot{
		ActiveClient: detector.ProxyClientInfo{ID: "builtin", Name: "内置内核", Secret: secret},
	}
	values := []any{
		status,
		subsResponse{Subscriptions: []configstore.Subscription{sub}},
		subResponse{OK: true, Subscription: sub},
		modeResponse{OK: true, Mode: "global", Global: "alpha"},
		sysproxyResponse{OK: true, SystemProxy: false},
		importResponse{OK: true, Path: `D:\core\mihomo.exe`},
		browseResponse{},
		nodeDelayResponse{OK: true, NodeName: "US01", DelayMs: 42, Tested: true},
		subsUpdateResponse{
			Subscriptions: []configstore.Subscription{sub},
			Results:       []subUpdateItem{{ID: "abc", OK: false, Error: "订阅下载失败"}},
		},
		snap,
		sseEvent{Type: "sync", Snapshot: &snap, Core: &coreEvent{Phase: "running", Message: "内核已启动"}},
	}
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if strings.Contains(text, secret) || strings.Contains(text, `"secret"`) {
			t.Fatalf("%T contains secret: %s", value, text)
		}
	}
}

func TestPublicErrIsChinese(t *testing.T) {
	cases := []error{
		core.ErrNotRunning,
		core.ErrAlreadyRunning,
		core.ErrHashMismatch,
		core.ErrNoPE,
		errors.New(`subscription url "ftp://x"`),
		errors.New("subscription body is empty"),
		errors.New("subscription body is html"),
		errors.New("subscription has no nodes"),
		errors.New("subscription body exceeds 16MB"),
		errors.New("subscription status 500"),
		errors.New("providers dir is empty"),
		errors.New(`subscription id "../x"`),
		errors.New("mihomo not ready: timeout\nsecret=hidden"),
		errors.New("mixed, controller, and ai ports must differ"),
		errors.New("no free port from 27890"),
		errors.New("port 70000 out of range"),
		errors.New("mode is empty"),
		errors.New(`mode "tun"`),
		errors.New("import note.txt: want .exe or .zip"),
		errors.New("download failed"),
		errors.New("download stalled after 30s"),
		core.ErrGeoHTML,
		core.ErrGeoTooSmall,
		core.ErrGeoPreflight,
		errors.New("geo download: timeout"),
		errors.New("127.0.0.1:7897: unreachable"),
	}
	for _, err := range cases {
		got := publicErr(err)
		if !containsChinese(got) {
			t.Fatalf("%v -> %s", err, got)
		}
		if strings.Contains(got, "secret=") || strings.Contains(got, "hidden") {
			t.Fatalf("mapped error kept the log: %s", got)
		}
	}
	got := startFailure(errors.New("download failed"))
	if !strings.Contains(got, "请手动导入 mihomo.exe/.zip") {
		t.Fatalf("start failure = %s", got)
	}
}

func TestPublicErrModeMatchIgnoresUnrelatedText(t *testing.T) {
	const modeText = "模式只能是 rule、global 或 direct"
	unrelated := []error{
		errors.New("failed to switch mode for the user"),
		errors.New("the mode is not available right now"),
		errors.New("switch mode http 500: bad gateway"),
	}
	for _, err := range unrelated {
		got := publicErr(err)
		if got == modeText {
			t.Fatalf("%v mapped to %s", err, got)
		}
	}
	if publicErr(errors.New("mode is empty")) != modeText || publicErr(errors.New(`mode "tun"`)) != modeText {
		t.Fatal("exact mode errors were not translated")
	}
	if publicErr(&core.InvalidModeError{Mode: "tun"}) != modeText || publicErr(&core.InvalidModeError{}) != modeText {
		t.Fatal("typed invalid mode was not translated")
	}
}

func TestCoreStatusSystemProxyActiveFollowsRunningCore(t *testing.T) {
	useTempConfig(t)
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.SystemProxy = true
	if err := configstore.SaveCoreSettings(settings); err != nil {
		t.Fatal(err)
	}
	rt := &appRuntime{core: &fakeCore{}}
	rec := postJSON(rt.handleCoreStatus, "")
	var body coreStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || body.SystemProxyActive || !body.Settings.SystemProxy {
		t.Fatalf("stopped code %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"systemProxyActive":false`) || !strings.Contains(rec.Body.String(), `"systemProxy":true`) {
		t.Fatalf("stopped body %s", rec.Body.String())
	}

	rt.core = &fakeCore{running: true}
	rec = postJSON(rt.handleCoreStatus, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.SystemProxyActive || !body.Settings.SystemProxy {
		t.Fatalf("running body %s", rec.Body.String())
	}

	settings.SystemProxy = false
	if err := configstore.SaveCoreSettings(settings); err != nil {
		t.Fatal(err)
	}
	rec = postJSON(rt.handleCoreStatus, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SystemProxyActive || body.Settings.SystemProxy {
		t.Fatalf("preference off body %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), configstoreSecret(t)) || strings.Contains(rec.Body.String(), `"secret"`) {
		t.Fatalf("secret in status %s", rec.Body.String())
	}
}

func TestSysproxyEnableRejectedWhenCoreStopped(t *testing.T) {
	called := false
	rt := &appRuntime{
		core: &fakeCore{},
		setProxy: func(string, int, string) error {
			called = true
			return nil
		},
		restore: func() error {
			t.Fatal("restore ran")
			return nil
		},
	}
	rec := postJSON(rt.handleSysproxySet, `{"enable":true}`)
	if rec.Code == http.StatusOK || called {
		t.Fatalf("code %d called %v body %s", rec.Code, called, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "内核未运行") {
		t.Fatalf("body %s", rec.Body.String())
	}
}

func TestCoreStopClearsEnabledAndKeepsOrder(t *testing.T) {
	useTempConfig(t)
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Enabled = true
	settings.SystemProxy = true
	if err := configstore.SaveCoreSettings(settings); err != nil {
		t.Fatal(err)
	}
	var log []string
	rt := &appRuntime{
		restore: func() error {
			log = append(log, "restore")
			return nil
		},
		core: &fakeCore{log: &log, running: true},
	}
	rec := postJSON(rt.handleCoreStop, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("body %s", rec.Body.String())
	}
	if strings.Join(log, ",") != "restore,stop" {
		t.Fatalf("log = %v", log)
	}
	stored, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled {
		t.Fatal("enabled stayed true")
	}
	if !stored.SystemProxy {
		t.Fatal("system proxy preference was cleared")
	}
	if strings.Contains(rec.Body.String(), stored.Secret) {
		t.Fatalf("secret in stop response %s", rec.Body.String())
	}
}

func TestStartFailureTellsUserToImport(t *testing.T) {
	useTempConfig(t)
	rt := &appRuntime{
		core: &fakeCore{},
		ensureBinary: func(ctx context.Context, coreDir string, proxyURLs []string, onProgress core.ProgressFunc) (string, error) {
			return "", errors.New("download failed")
		},
		proxyURLs: func() []string { return nil },
	}
	rec := postJSON(rt.handleCoreStart, "")
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "mihomo.exe/.zip") {
		t.Fatalf("body %s", rec.Body.String())
	}
}

func TestCoreStartFailureBroadcastsError(t *testing.T) {
	useTempConfig(t)
	hub := newSSEHub()
	ch := hub.subscribe()
	defer hub.unsubscribe(ch)
	rt := &appRuntime{
		hub:  hub,
		core: &fakeCore{},
		ensureBinary: func(context.Context, string, []string, core.ProgressFunc) (string, error) {
			return "", errors.New("download failed")
		},
		proxyURLs: func() []string { return nil },
	}
	rec := postJSON(rt.handleCoreStart, "")
	if rec.Code == http.StatusOK {
		t.Fatalf("body %s", rec.Body.String())
	}
	sawError := false
	for {
		select {
		case payload := <-ch:
			var ev sseEvent
			if err := json.Unmarshal(payload, &ev); err != nil {
				t.Fatal(err)
			}
			if ev.Type == "core" && ev.Core != nil && ev.Core.Phase == "error" && containsChinese(ev.Core.Message) {
				sawError = true
			}
		default:
			if !sawError {
				t.Fatal("start failure did not broadcast an error")
			}
			return
		}
	}
}

func TestCoreStartKeepsRunningWhenFollowUpFails(t *testing.T) {
	useTempConfig(t)
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.SystemProxy = true
	if err := configstore.SaveCoreSettings(settings); err != nil {
		t.Fatal(err)
	}
	hub := newSSEHub()
	ch := hub.subscribe()
	defer hub.unsubscribe(ch)
	fc := &fakeCore{ports: core.Ports{Mixed: 27890, Controller: 27891, AIDedicated: 27892}}
	rt := &appRuntime{
		hub:  hub,
		core: fc,
		ensureBinary: func(context.Context, string, []string, core.ProgressFunc) (string, error) {
			return "mihomo.exe", nil
		},
		setProxy: func(string, int, string) error {
			return errors.New("access denied")
		},
		proxyURLs: func() []string { return nil },
	}
	rec := postJSON(rt.handleCoreStart, "")
	if rec.Code != http.StatusOK || !fc.running {
		t.Fatalf("code %d running %v body %s", rec.Code, fc.running, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"warning"`) || !strings.Contains(rec.Body.String(), "系统代理") {
		t.Fatalf("body %s", rec.Body.String())
	}
	var sawRunning bool
	for {
		select {
		case payload := <-ch:
			var ev sseEvent
			if err := json.Unmarshal(payload, &ev); err != nil {
				t.Fatal(err)
			}
			if ev.Core != nil && ev.Core.Phase == "running" {
				sawRunning = true
			}
			if ev.Core != nil && ev.Core.Phase == "error" {
				t.Fatalf("follow-up was broadcast as start failure: %s", ev.Core.Message)
			}
		default:
			if !sawRunning {
				t.Fatal("running was not broadcast")
			}
			return
		}
	}
}

func TestSubsRoundTripAndCoreNodeCount(t *testing.T) {
	useTempConfig(t)
	t.Setenv("LOCALAPPDATA", t.TempDir())
	subSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "proxies:\n  - {name: alpha, type: http, server: 127.0.0.1, port: 1}\n")
	}))
	defer subSrv.Close()

	var providerID string
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"providers":{"`+providerID+`":{"proxies":[{},{},{}]}}}`)
	}))
	defer controller.Close()
	port := mustPort(t, controller.URL)

	coreFake := &fakeCore{running: false}
	rt := newAppRuntime(nil)
	rt.core = coreFake
	rt.proxyURLs = func() []string { return nil }

	rec := postJSON(rt.handleSubs, `{"id":"../etc/passwd","name":"bad","url":"ftp://example.com/sub"}`)
	if rec.Code == http.StatusOK {
		t.Fatalf("bad url accepted: %s", rec.Body.String())
	}

	rec = postJSON(rt.handleSubs, `{"id":"client-picked","name":"local","url":"`+subSrv.URL+`","enabled":true,"filter":"keep-me"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create %s", rec.Body.String())
	}
	var created subResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Subscription.ID == "" || created.Subscription.ID == "client-picked" || !subIDRe.MatchString(created.Subscription.ID) {
		t.Fatalf("id = %s", created.Subscription.ID)
	}
	providerID = created.Subscription.ID

	rec = postJSON(rt.handleSubsUpdate, `{"id":"`+created.Subscription.ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update %s", rec.Body.String())
	}
	var updated subResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Subscription.NodeCount != 1 || coreFake.reloads != 0 {
		t.Fatalf("count %d reloads %d", updated.Subscription.NodeCount, coreFake.reloads)
	}

	coreFake.running = true
	coreFake.ports = core.Ports{Controller: port, Mixed: 27890, AIDedicated: 27892}
	coreFake.mode = core.ModeRule
	rec = postJSON(rt.handleSubsUpdate, `{"id":"`+created.Subscription.ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("second update %s", rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Subscription.NodeCount != 3 || coreFake.reloads != 1 {
		t.Fatalf("count %d reloads %d", updated.Subscription.NodeCount, coreFake.reloads)
	}
	if strings.Contains(rec.Body.String(), configstoreSecret(t)) {
		t.Fatalf("secret in sub response %s", rec.Body.String())
	}

	rtUpdate := httptest.NewRequest(http.MethodPut, "/api/subs", bytes.NewBufferString(`{"id":"`+created.Subscription.ID+`","name":"renamed"}`))
	rec = httptest.NewRecorder()
	rt.updateSubFields(rec, rtUpdate)
	if rec.Code != http.StatusOK {
		t.Fatalf("put %s", rec.Body.String())
	}
	stored, err := configstore.LoadSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Name != "renamed" || stored[0].Filter != "keep-me" || stored[0].URL != subSrv.URL {
		t.Fatalf("stored = %+v", stored[0])
	}
}

func TestBatchSubUpdateContinuesAfterFailure(t *testing.T) {
	useTempConfig(t)
	t.Setenv("LOCALAPPDATA", t.TempDir())
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "proxies:\n  - {name: alpha, type: http, server: 127.0.0.1, port: 1}\n")
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer bad.Close()

	subs := []configstore.Subscription{
		{ID: "one", Name: "one", URL: good.URL, Enabled: true},
		{ID: "two", Name: "two", URL: bad.URL, Enabled: true},
		{ID: "three", Name: "three", URL: good.URL, Enabled: true},
	}
	if err := configstore.SaveSubscriptions(subs); err != nil {
		t.Fatal(err)
	}

	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"providers":{"one":{"proxies":[{},{},{},{}]},"three":{"proxies":[{},{},{},{},{}]}}}`)
	}))
	defer controller.Close()

	coreFake := &fakeCore{
		running: true,
		mode:    core.ModeRule,
		ports:   core.Ports{Controller: mustPort(t, controller.URL)},
	}
	rt := newAppRuntime(nil)
	rt.core = coreFake
	rt.proxyURLs = func() []string { return nil }

	rec := postJSON(rt.handleSubsUpdate, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch %s", rec.Body.String())
	}
	var body subsUpdateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Subscriptions) != 3 || len(body.Results) != 3 {
		t.Fatalf("subs %d results %d", len(body.Subscriptions), len(body.Results))
	}
	byID := map[string]configstore.Subscription{}
	for _, sub := range body.Subscriptions {
		byID[sub.ID] = sub
	}
	if byID["one"].NodeCount != 4 || byID["one"].LastError != "" || byID["one"].UpdatedAt.IsZero() {
		t.Fatalf("one = %+v", byID["one"])
	}
	if byID["three"].NodeCount != 5 || byID["three"].LastError != "" || byID["three"].UpdatedAt.IsZero() {
		t.Fatalf("three = %+v", byID["three"])
	}
	if byID["two"].NodeCount != 0 || byID["two"].LastError != "订阅下载失败，服务器返回错误" || !byID["two"].UpdatedAt.IsZero() {
		t.Fatalf("two = %+v", byID["two"])
	}
	want := []subUpdateItem{
		{ID: "one", OK: true},
		{ID: "two", OK: false, Error: "订阅下载失败，服务器返回错误"},
		{ID: "three", OK: true},
	}
	for i, item := range want {
		got := body.Results[i]
		if got.ID != item.ID || got.OK != item.OK || got.Error != item.Error {
			t.Fatalf("result[%d] = %+v", i, got)
		}
	}
	if coreFake.reloads != 1 {
		t.Fatalf("reloads %d", coreFake.reloads)
	}
	stored, err := configstore.LoadSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 3 || stored[0].LastError != "" || stored[1].LastError != "订阅下载失败，服务器返回错误" || stored[2].LastError != "" {
		t.Fatalf("stored errors = %q %q %q", stored[0].LastError, stored[1].LastError, stored[2].LastError)
	}
	dir := filepath.Join(rt.coreDir, "providers")
	for _, id := range []string{"one", "three"} {
		raw, err := os.ReadFile(filepath.Join(dir, id+".txt"))
		if err != nil || !strings.Contains(string(raw), "alpha") {
			t.Fatalf("%s provider file: %v %s", id, err, raw)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "two.txt")); !os.IsNotExist(err) {
		t.Fatalf("failed subscription wrote a provider file: %v", err)
	}
	if strings.Contains(rec.Body.String(), configstoreSecret(t)) || strings.Contains(rec.Body.String(), `"secret"`) {
		t.Fatalf("secret in batch response %s", rec.Body.String())
	}
}

func TestGlobalFollowsProxySelection(t *testing.T) {
	useTempConfig(t)
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/providers/proxies" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{
			"proxies": {
				"PROXY": {"name":"PROXY","type":"Selector","now":"alpha","all":["alpha","DIRECT"]},
				"GLOBAL": {"name":"GLOBAL","type":"Selector","now":"DIRECT","all":["alpha","PROXY","DIRECT"]},
				"`+mihomo.AIGroupName+`": {"name":"`+mihomo.AIGroupName+`","type":"Selector","now":"beta","all":["beta","DIRECT"]},
				"alpha": {"name":"alpha","type":"HTTP"},
				"beta": {"name":"beta","type":"HTTP"},
				"DIRECT": {"name":"DIRECT","type":"Direct"}
			}
		}`)
	}))
	defer srv.Close()

	client := detector.ProxyClientInfo{
		ID:                 "builtin",
		Name:               "内置内核",
		Running:            true,
		ExternalController: strings.TrimPrefix(srv.URL, "http://"),
		Secret:             "not-in-the-response",
	}
	proxyList, err := listClientGroup(client, "PROXY", false)
	if err != nil {
		t.Fatal(err)
	}
	if proxyList.CurrentSelect != "alpha" {
		t.Fatalf("PROXY now = %s", proxyList.CurrentSelect)
	}
	aiList, err := listClientGroup(client, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if aiList.CurrentSelect != "beta" {
		t.Fatalf("AI now = %s", aiList.CurrentSelect)
	}

	rt := &appRuntime{core: &fakeCore{mode: core.ModeGlobal, ports: core.Ports{Controller: mustPort(t, srv.URL)}}}
	if _, err := rt.selectClientGroup(client, "PROXY", "alpha"); err != nil {
		t.Fatal(err)
	}
	if !containsStr(hits, "PUT /proxies/PROXY") || !containsStr(hits, "PUT /proxies/GLOBAL") {
		t.Fatalf("hits = %v", hits)
	}

	hits = nil
	req := httptest.NewRequest(http.MethodPost, "/api/core/mode", strings.NewReader(`{"mode":"global"}`))
	rec := httptest.NewRecorder()
	rt.handleCoreMode(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mode %s", rec.Body.String())
	}
	var mode modeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &mode); err != nil {
		t.Fatal(err)
	}
	if mode.Mode != "global" || mode.Global != "alpha" {
		t.Fatalf("mode response = %+v", mode)
	}
	if strings.Contains(rec.Body.String(), "not-in-the-response") || strings.Contains(rec.Body.String(), `"secret"`) {
		t.Fatalf("mode response leaked secret %s", rec.Body.String())
	}
}

func TestGlobalFallsBackToProxyGroupForProviderNode(t *testing.T) {
	useTempConfig(t)
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/providers/proxies" {
			_, _ = io.WriteString(w, `{"providers":{"sub":{"proxies":[{"name":"hk-1","type":"Shadowsocks","provider-name":"sub"}]}}}`)
			return
		}
		_, _ = io.WriteString(w, `{"proxies":{
			"PROXY": {"name":"PROXY","type":"Selector","now":"DIRECT","all":["hk-1","DIRECT"]},
			"GLOBAL": {"name":"GLOBAL","type":"Selector","now":"DIRECT","all":["PROXY","DIRECT"]},
			"DIRECT": {"name":"DIRECT","type":"Direct"}
		}}`)
	}))
	defer srv.Close()

	client := detector.ProxyClientInfo{ID: "builtin", Name: "内置内核", ExternalController: strings.TrimPrefix(srv.URL, "http://")}
	rt := &appRuntime{core: &fakeCore{mode: core.ModeGlobal, ports: core.Ports{Controller: mustPort(t, srv.URL)}}}
	if _, err := rt.selectClientGroup(client, "PROXY", "hk-1"); err != nil {
		t.Fatal(err)
	}
	if !containsStr(hits, "PUT /proxies/PROXY") || !containsStr(hits, "PUT /proxies/GLOBAL") {
		t.Fatalf("hits = %v", hits)
	}
}

func TestNodesUseBuiltinActiveClient(t *testing.T) {
	useTempConfig(t)
	var hits []string
	srv := httptest.NewServer(selectorHandler("builtin-node", &hits))
	defer srv.Close()

	builtinOn := detector.ProxyClientInfo{
		ID:                 "builtin",
		Name:               "内置内核",
		Running:            true,
		ExternalController: strings.TrimPrefix(srv.URL, "http://"),
		Secret:             "builtin-secret-marker",
	}
	snap := detector.SystemSnapshot{ActiveClient: builtinOn}
	rt := &appRuntime{
		core:   &fakeCore{mode: core.ModeRule},
		detect: func() detector.SystemSnapshot { return snap },
	}

	rec := postJSON(rt.handleNodesList, `{"group":"PROXY","client":"other"}`)
	assertListedNode(t, rec, "builtin-node")
	if !containsStr(hits, "GET /proxies") {
		t.Fatalf("hits %v", hits)
	}
	if strings.Contains(rec.Body.String(), "builtin-secret-marker") {
		t.Fatalf("secret in list %s", rec.Body.String())
	}

	hits = nil
	rec = postJSON(rt.handleNodesSelect, `{"group":"PROXY","nodeName":"builtin-node"}`)
	if rec.Code != http.StatusOK || !containsStr(hits, "PUT /proxies/PROXY") {
		t.Fatalf("select code %d hits %v body %s", rec.Code, hits, rec.Body.String())
	}

	snap.ActiveClient.Running = false
	hits = nil
	rec = postJSON(rt.handleNodesList, `{"group":"PROXY"}`)
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "内置内核未运行") || len(hits) != 0 {
		t.Fatalf("stopped list hits %v body %s", hits, rec.Body.String())
	}
}

func selectorHandler(now string, hits *[]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*hits = append(*hits, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/providers/proxies" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"proxies":{"PROXY":{"name":"PROXY","type":"Selector","now":"`+now+`","all":["`+now+`","DIRECT"]},"`+now+`":{"name":"`+now+`","type":"HTTP"},"DIRECT":{"name":"DIRECT","type":"Direct"}}}`)
	}
}

func assertListedNode(t *testing.T, rec *httptest.ResponseRecorder, now string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("list %s", rec.Body.String())
	}
	var res mihomo.NodeListResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.CurrentSelect != now {
		t.Fatalf("current = %s body %s", res.CurrentSelect, rec.Body.String())
	}
}

func TestNodeDelayUsesProviderHealthcheck(t *testing.T) {
	useTempConfig(t)
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		switch {
		case r.URL.Path == "/proxies":
			_, _ = io.WriteString(w, `{"proxies":{}}`)
		case r.URL.Path == "/providers/proxies":
			_, _ = io.WriteString(w, `{"providers":{"sub":{"proxies":[{"name":"US01","type":"Vless","provider-name":"sub"}]}}}`)
		case r.URL.Path == "/proxies/US01/delay":
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/providers/proxies/sub/US01/healthcheck":
			if r.URL.Query().Get("url") != mihomo.TestURL || r.URL.Query().Get("timeout") == "" || r.URL.Query().Get("timeout") == "0" {
				t.Errorf("healthcheck query = %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"delay":42}`)
		case r.URL.Path == "/proxies/dead/delay":
			w.WriteHeader(http.StatusGatewayTimeout)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	builtin := detector.ProxyClientInfo{
		ID:                 "builtin",
		Name:               "内置内核",
		Running:            true,
		ExternalController: strings.TrimPrefix(srv.URL, "http://"),
		Secret:             "delay-secret-marker",
	}
	rt := &appRuntime{detect: func() detector.SystemSnapshot {
		return detector.SystemSnapshot{ActiveClient: builtin}
	}}

	rec := postJSON(rt.handleNodesTest, `{"client":"builtin","nodeName":" US01 "}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("test %s", rec.Body.String())
	}
	var body nodeDelayResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || !body.Tested || body.NodeName != "US01" || body.DelayMs != 42 {
		t.Fatalf("delay = %+v", body)
	}
	if !containsStr(hits, "/proxies/US01/delay") || !containsStr(hits, "/providers/proxies/sub/US01/healthcheck") {
		t.Fatalf("hits = %v", hits)
	}
	if strings.Contains(rec.Body.String(), "delay-secret-marker") || strings.Contains(rec.Body.String(), `"secret"`) {
		t.Fatalf("secret in delay response %s", rec.Body.String())
	}

	hits = nil
	rec = postJSON(rt.handleNodesTest, `{"client":"builtin","nodeName":"dead"}`)
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || !body.Tested || body.DelayMs != 0 || body.NodeName != "dead" {
		t.Fatalf("timeout = %d %+v", rec.Code, body)
	}
	if containsStr(hits, "/providers/proxies/dead/healthcheck") || containsStr(hits, "/providers/proxies/sub/dead/healthcheck") {
		t.Fatalf("timeout took a healthcheck: %v", hits)
	}

	builtin.Running = false
	rec = postJSON(rt.handleNodesTest, `{"client":"builtin","nodeName":"US01"}`)
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "内置内核未运行") {
		t.Fatalf("stopped %s", rec.Body.String())
	}
}

func TestNodeSelectMessageUsesSwitchedNode(t *testing.T) {
	live := &mihomo.LiveNodeStatus{ResolvedNode: "SG03", ExternalNode: "HK01"}
	if got := nodeSelectMessage("PROXY", "alpha", live); got != "已切换 PROXY 节点：alpha" {
		t.Fatalf("PROXY leaf = %s", got)
	}
	if got := nodeSelectMessage("PROXY", core.BrowsingAutoName, live); got != "已切换 PROXY 节点：HK01" {
		t.Fatalf("PROXY auto = %s", got)
	}
	if got := nodeSelectMessage("PROXY", core.BrowsingAutoName, &mihomo.LiveNodeStatus{ResolvedNode: "SG03"}); got != "已切换 PROXY 节点："+core.BrowsingAutoName {
		t.Fatalf("PROXY auto fallback = %s", got)
	}
	if got := nodeSelectMessage("PROXY", "alpha", nil); got != "已切换 PROXY 节点：alpha" {
		t.Fatalf("nil live = %s", got)
	}
	if got := nodeSelectMessage(mihomo.AIGroupName, "US01", live); got != "Proxy Box 专属通道已锁定节点：SG03" {
		t.Fatalf("AI = %s", got)
	}
	if got := nodeSelectMessage("", mihomo.AIAutoName, live); got != "Proxy Box 专属通道已切回自动优选：SG03" {
		t.Fatalf("AI auto = %s", got)
	}
}

func TestSubscriptionUserAgentReachesOrigin(t *testing.T) {
	useTempConfig(t)
	t.Setenv("LOCALAPPDATA", t.TempDir())
	var mu sync.Mutex
	var uas []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		uas = append(uas, r.Header.Get("User-Agent"))
		mu.Unlock()
		_, _ = io.WriteString(w, "proxies:\n  - {name: alpha, type: http, server: 127.0.0.1, port: 1}\n")
	}))
	defer srv.Close()

	rt := newAppRuntime(nil)
	rt.core = &fakeCore{}
	rt.proxyURLs = func() []string { return nil }

	rec := postJSON(rt.handleSubs, `{"name":"custom","url":"`+srv.URL+`","userAgent":" ProxyBoxTest/9 ","filter":"keep","updateIntervalMin":15}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create custom %s", rec.Body.String())
	}
	var custom subResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &custom); err != nil {
		t.Fatal(err)
	}
	if custom.Subscription.UserAgent != "ProxyBoxTest/9" || custom.Subscription.Filter != "keep" || custom.Subscription.UpdateIntervalMin != 15 {
		t.Fatalf("created = %+v", custom.Subscription)
	}
	rec = postJSON(rt.handleSubsUpdate, `{"id":"`+custom.Subscription.ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update custom %s", rec.Body.String())
	}
	if got := lastUA(t, &mu, uas); got != "ProxyBoxTest/9" {
		t.Fatalf("custom ua %q", got)
	}

	rec = postJSON(rt.handleSubs, `{"name":"plain","url":"`+srv.URL+`","enabled":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create plain %s", rec.Body.String())
	}
	var plain subResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &plain); err != nil {
		t.Fatal(err)
	}
	if plain.Subscription.UserAgent != "" {
		t.Fatalf("plain ua stored %q", plain.Subscription.UserAgent)
	}
	rec = postJSON(rt.handleSubsUpdate, `{"id":"`+plain.Subscription.ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update plain %s", rec.Body.String())
	}
	if got := lastUA(t, &mu, uas); got != "clash.meta" {
		t.Fatalf("empty ua %q", got)
	}

	put := httptest.NewRequest(http.MethodPut, "/api/subs", bytes.NewBufferString(`{"id":"`+custom.Subscription.ID+`","name":"renamed"}`))
	rec = httptest.NewRecorder()
	rt.updateSubFields(rec, put)
	if rec.Code != http.StatusOK {
		t.Fatalf("put name %s", rec.Body.String())
	}
	stored := mustSub(t, custom.Subscription.ID)
	if stored.Name != "renamed" || stored.UserAgent != "ProxyBoxTest/9" || stored.Filter != "keep" || stored.URL != srv.URL || stored.UpdateIntervalMin != 15 {
		t.Fatalf("name put changed other fields: %+v", stored)
	}

	put = httptest.NewRequest(http.MethodPut, "/api/subs", bytes.NewBufferString(`{"id":"`+custom.Subscription.ID+`","userAgent":"  "}`))
	rec = httptest.NewRecorder()
	rt.updateSubFields(rec, put)
	if rec.Code != http.StatusOK {
		t.Fatalf("put ua %s", rec.Body.String())
	}
	stored = mustSub(t, custom.Subscription.ID)
	if stored.UserAgent != "" || stored.Name != "renamed" || stored.Filter != "keep" || stored.UpdateIntervalMin != 15 {
		t.Fatalf("ua put = %+v", stored)
	}
	rec = postJSON(rt.handleSubsUpdate, `{"id":"`+custom.Subscription.ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update cleared %s", rec.Body.String())
	}
	if got := lastUA(t, &mu, uas); got != "clash.meta" {
		t.Fatalf("cleared ua %q", got)
	}
}

func TestApplySystemProxyRefusesWhenCoreStopped(t *testing.T) {
	useTempConfig(t)
	called := false
	restored := false
	rt := &appRuntime{
		core: &fakeCore{},
		setProxy: func(string, int, string) error {
			called = true
			return nil
		},
		restore: func() error {
			restored = true
			return nil
		},
	}
	if _, err := rt.applySystemProxy(true); err == nil || err.Error() != "内核未运行，不能开启系统代理" || called {
		t.Fatalf("err %v called %v", err, called)
	}
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.SystemProxy {
		t.Fatal("stopped core enabled the system proxy")
	}

	rt.core = &fakeCore{running: true, ports: core.Ports{Mixed: 27890}}
	on, err := rt.applySystemProxy(true)
	if err != nil || !on || !called {
		t.Fatalf("enable on %v err %v called %v", on, err, called)
	}
	on, err = rt.applySystemProxy(false)
	if err != nil || on || !restored {
		t.Fatalf("disable on %v err %v restored %v", on, err, restored)
	}
	settings, err = configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.SystemProxy {
		t.Fatal("system proxy stayed on")
	}
}

func lastUA(t *testing.T, mu *sync.Mutex, uas []string) string {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if len(uas) == 0 {
		t.Fatal("subscription server saw no request")
	}
	return uas[len(uas)-1]
}

func mustSub(t *testing.T, id string) configstore.Subscription {
	t.Helper()
	subs, err := configstore.LoadSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	idx := subIndex(subs, id)
	if idx < 0 {
		t.Fatalf("missing %s", id)
	}
	return subs[idx]
}

func postJSON(h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func mustPort(t *testing.T, raw string) int {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func configstoreSecret(t *testing.T) string {
	t.Helper()
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	return settings.Secret
}

func containsStr(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
