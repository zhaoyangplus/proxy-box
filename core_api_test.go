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
	"strconv"
	"strings"
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
		Clients:      []detector.ProxyClientInfo{{ID: "builtin", Secret: secret}, {ID: "clash_verge", Secret: secret}},
	}
	values := []any{
		status,
		subsResponse{Subscriptions: []configstore.Subscription{sub}},
		subResponse{OK: true, Subscription: sub},
		modeResponse{OK: true, Mode: "global", Global: "alpha"},
		sysproxyResponse{OK: true, SystemProxy: false},
		importResponse{OK: true, Path: `D:\core\mihomo.exe`},
		browseResponse{},
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
