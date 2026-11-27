package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"proxy-box/internal/clashscript"
	"proxy-box/internal/configstore"
	"proxy-box/internal/core"
	"proxy-box/internal/detector"
	"proxy-box/internal/speedtest"
)

func TestSpeedRoutesAreRegistered(t *testing.T) {
	rt := &appRuntime{core: &fakeCore{}}
	mux := http.NewServeMux()
	rt.register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/speedtest/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var body speedStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Progress.Running || body.Results == nil {
		t.Fatalf("%s", rec.Body.String())
	}
}

func TestSpeedStartRequiresRunningCore(t *testing.T) {
	useTempConfig(t)
	rt := &appRuntime{core: &fakeCore{}}
	rec := postJSON(rt.handleSpeedTestStart, `{"group":"PROXY"}`)
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "内核未运行") {
		t.Fatalf("%s", rec.Body.String())
	}
	rt.core = &fakeCore{running: true}
	rec = postJSON(rt.handleSpeedTestStart, `{"group":"PROXY"}`)
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "测速端口未就绪") {
		t.Fatalf("%s", rec.Body.String())
	}
	rec = callSpeed(rt.handleSpeedTestStart, http.MethodGet, `{}`)
	if !strings.Contains(rec.Body.String(), "请使用 POST") {
		t.Fatalf("%s", rec.Body.String())
	}
}

func TestSpeedStartAliveNodesAfterOneDelay(t *testing.T) {
	fx := newSpeedFixture(t, "fresh", map[string]int{"US-1": 40}, nil)
	fx.saveURL(t)
	var raw [][]byte
	stop := fx.collectSSE(&raw)
	defer stop()

	rec := postJSON(fx.rt.handleSpeedTestStart, `{"group":"PROXY","nodes":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s", rec.Body.String())
	}
	var started speedStartResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if !started.OK || started.TaskID == "" || started.Total != 1 || strings.Contains(rec.Body.String(), "secret-marker") {
		t.Fatalf("%s", rec.Body.String())
	}
	waitSpeed(t, fx.rt)
	res := fx.rt.speed.Cache()["US-1"]
	if res.Error != "" || res.SpeedMBps <= 0 || res.BytesRead < 32<<10 {
		t.Fatalf("%+v", res)
	}
	if _, ok := fx.rt.speed.Cache()["DEAD"]; ok {
		t.Fatal("tested a dead node")
	}
	hits := fx.ctrl.snapshot()
	if delayHits(hits) != 1 {
		t.Fatalf("delay hits %v", hits)
	}
	assertSpeedPuts(t, hits)
	urls := fx.urls.snapshot()
	if len(urls) != 1 || !strings.Contains(urls[0], "download.invalid") {
		t.Fatalf("urls %v", urls)
	}

	status := callSpeed(fx.rt.handleSpeedTestStatus, http.MethodGet, "")
	var body speedStatusResponse
	if err := json.Unmarshal(status.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Progress.Running || body.Progress.TaskID != started.TaskID || body.Results["US-1"].SpeedMBps != res.SpeedMBps {
		t.Fatalf("status %s", status.Body.String())
	}
	stop()
	saw := false
	for _, buf := range raw {
		var ev sseEvent
		if json.Unmarshal(buf, &ev) != nil {
			continue
		}
		if ev.Type == "speedtest" && ev.Speedtest != nil && ev.Speedtest.LastResult != nil && ev.Speedtest.LastResult.NodeName == "US-1" {
			saw = true
		}
		if strings.Contains(string(buf), "secret-marker") {
			t.Fatalf("sse leaked secret %s", buf)
		}
	}
	if !saw {
		t.Fatal("missing speedtest event")
	}
}

func TestSpeedStartUsesKnownDelayWithoutAnotherProbe(t *testing.T) {
	fx := newSpeedFixture(t, "mixed", nil, nil)
	fx.saveURL(t)
	rec := postJSON(fx.rt.handleSpeedTestStart, `{"group":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s", rec.Body.String())
	}
	var started speedStartResponse
	if json.Unmarshal(rec.Body.Bytes(), &started) != nil || started.Total != 1 {
		t.Fatalf("%s", rec.Body.String())
	}
	waitSpeed(t, fx.rt)
	if delayHits(fx.ctrl.snapshot()) != 0 {
		t.Fatalf("delay ran: %v", fx.ctrl.snapshot())
	}
	assertSpeedPuts(t, fx.ctrl.snapshot())
	if fx.rt.speed.Cache()["US-1"].Error != "" {
		t.Fatalf("%+v", fx.rt.speed.Cache()["US-1"])
	}
}

func TestSpeedStartRejectsWhenNoneAlive(t *testing.T) {
	fx := newSpeedFixture(t, "dead", nil, nil)
	fx.saveURL(t)
	rec := postJSON(fx.rt.handleSpeedTestStart, `{"group":"PROXY"}`)
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "没有存活节点") {
		t.Fatalf("%s", rec.Body.String())
	}
	if delayHits(fx.ctrl.snapshot()) != 0 || len(fx.urls.snapshot()) != 0 {
		t.Fatalf("hits %v urls %v", fx.ctrl.snapshot(), fx.urls.snapshot())
	}

	fx = newSpeedFixture(t, "fresh", map[string]int{}, nil)
	fx.saveURL(t)
	rec = postJSON(fx.rt.handleSpeedTestStart, `{"group":"PROXY"}`)
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "没有存活节点") || delayHits(fx.ctrl.snapshot()) != 1 {
		t.Fatalf("body %s hits %v", rec.Body.String(), fx.ctrl.snapshot())
	}
}

func TestSpeedStartOneNamedNode(t *testing.T) {
	fx := newSpeedFixture(t, "dead", nil, nil)
	fx.saveURL(t)
	rec := postJSON(fx.rt.handleSpeedTestStart, `{"group":"PROXY","nodes":[" DEAD "]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s", rec.Body.String())
	}
	var started speedStartResponse
	if json.Unmarshal(rec.Body.Bytes(), &started) != nil || started.Total != 1 {
		t.Fatalf("%s", rec.Body.String())
	}
	waitSpeed(t, fx.rt)
	if delayHits(fx.ctrl.snapshot()) != 0 {
		t.Fatalf("delay ran for a named node: %v", fx.ctrl.snapshot())
	}
	if fx.rt.speed.Cache()["DEAD"].Error != "" {
		t.Fatalf("%+v", fx.rt.speed.Cache()["DEAD"])
	}
	assertSpeedPuts(t, fx.ctrl.snapshot())

	rec = postJSON(fx.rt.handleSpeedTestStart, fmt.Sprintf(`{"group":"PROXY","nodes":[%q]}`, core.BrowsingAutoName))
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "没有可测速的节点") {
		t.Fatalf("%s", rec.Body.String())
	}
}

func TestSpeedStartRejectsBadMax(t *testing.T) {
	fx := newSpeedFixture(t, "mixed", nil, nil)
	cfg := configstore.LoadConfig()
	cfg.SpeedTestURL = "http://download.invalid/payload"
	cfg.SpeedTestMaxMB = 7
	if err := configstore.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	rec := postJSON(fx.rt.handleSpeedTestStart, `{"group":"PROXY","nodes":["US-1"]}`)
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "5") {
		t.Fatalf("%s", rec.Body.String())
	}
	if len(fx.urls.snapshot()) != 0 {
		t.Fatalf("downloaded %v", fx.urls.snapshot())
	}
	rec = postJSON(fx.rt.handleSpeedTestStart, `{"group":"PROXY","nodes":["US-1"],"maxMB":8}`)
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "25") {
		t.Fatalf("%s", rec.Body.String())
	}
}

func TestSpeedStartReturnsBeforeDownloadFinishes(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	fx := newSpeedFixture(t, "mixed", nil, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), 1024))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write(bytes.Repeat([]byte("b"), 48<<10))
	})
	fx.saveURL(t)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- postJSON(fx.rt.handleSpeedTestStart, `{"nodes":["US-1"]}`)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not reach the proxy")
	}
	var rec *httptest.ResponseRecorder
	select {
	case rec = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("start blocked on the download")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("%s", rec.Body.String())
	}
	if !fx.rt.speed.Progress().Running {
		t.Fatal("task was not running when start returned")
	}
	close(release)
	waitSpeed(t, fx.rt)
	if fx.rt.speed.Cache()["US-1"].BytesRead < 32<<10 {
		t.Fatalf("%+v", fx.rt.speed.Cache()["US-1"])
	}
}

func TestSpeedStopCancels(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	fx := newSpeedFixture(t, "mixed", nil, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), 1024))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		once.Do(func() { close(started) })
		<-r.Context().Done()
	})
	fx.saveURL(t)
	go postJSON(fx.rt.handleSpeedTestStart, `{"nodes":["US-1"]}`)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not start")
	}
	begin := time.Now()
	rec := postJSON(fx.rt.handleSpeedTestStop, "")
	if rec.Code != http.StatusOK || time.Since(begin) > time.Second {
		t.Fatalf("stop %s %s", time.Since(begin), rec.Body.String())
	}
	postJSON(fx.rt.handleSpeedTestStop, "")
	progress := fx.rt.speed.Progress()
	if progress.Running || !progress.Canceled {
		t.Fatalf("%+v", progress)
	}
	if !strings.Contains(fx.rt.speed.Cache()["US-1"].Error, "取消") {
		t.Fatalf("%+v", fx.rt.speed.Cache()["US-1"])
	}
}

func TestEmptySpeedURLTriesBuiltinHosts(t *testing.T) {
	fx := newSpeedFixture(t, "mixed", nil, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			http.Error(w, "no tunnel", http.StatusBadGateway)
			return
		}
		_, _ = w.Write(bytes.Repeat([]byte("p"), 48<<10))
	})
	cfg := configstore.LoadConfig()
	cfg.SpeedTestURL = ""
	cfg.SpeedTestMaxMB = 10
	if err := configstore.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	rec := postJSON(fx.rt.handleSpeedTestStart, `{"nodes":["US-1"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s", rec.Body.String())
	}
	waitSpeed(t, fx.rt)
	if time.Since(begin) > 2*time.Second {
		t.Fatalf("took %s urls %v", time.Since(begin), fx.urls.snapshot())
	}
	joined := strings.Join(fx.urls.snapshot(), " ")
	if !strings.Contains(joined, "speed.cloudflare.com") || !strings.Contains(joined, "ovh.net") {
		t.Fatalf("urls %v result %+v", fx.urls.snapshot(), fx.rt.speed.Cache()["US-1"])
	}
	if !strings.Contains(fx.rt.speed.Cache()["US-1"].Error, "失败") {
		t.Fatalf("%+v", fx.rt.speed.Cache()["US-1"])
	}
}

func TestCoreStopHaltsSpeedBeforeRestore(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	fx := newSpeedFixture(t, "mixed", nil, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), 1024))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		once.Do(func() { close(started) })
		<-r.Context().Done()
	})
	fx.saveURL(t)
	var log []string
	fx.rt.core.(*fakeCore).log = &log
	fx.rt.restore = func() error {
		if fx.rt.speed != nil && fx.rt.speed.Progress().Running {
			t.Errorf("speed test still running at restore")
		}
		log = append(log, "restore")
		return nil
	}
	go postJSON(fx.rt.handleSpeedTestStart, `{"nodes":["US-1"]}`)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not start")
	}
	begin := time.Now()
	rec := postJSON(fx.rt.handleCoreStop, "")
	if rec.Code != http.StatusOK || time.Since(begin) > time.Second {
		t.Fatalf("stop %s %s", time.Since(begin), rec.Body.String())
	}
	if strings.Join(log, ",") != "restore,stop" {
		t.Fatalf("log %v", log)
	}
	if fx.rt.speed.Progress().Running {
		t.Fatal("still running")
	}
	rec = postJSON(fx.rt.handleCoreStop, "")
	if rec.Code != http.StatusOK || strings.Join(log, ",") != "restore,stop,restore,stop" {
		t.Fatalf("second %s log %v", rec.Body.String(), log)
	}
}

type hitList struct {
	mu    sync.Mutex
	items []string
}

func (h *hitList) add(item string) {
	h.mu.Lock()
	h.items = append(h.items, item)
	h.mu.Unlock()
}

func (h *hitList) snapshot() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]string(nil), h.items...)
	return out
}

type speedFixture struct {
	rt   *appRuntime
	ctrl *hitList
	urls *hitList
}

func newSpeedFixture(t *testing.T, mode string, delay map[string]int, download http.HandlerFunc) *speedFixture {
	t.Helper()
	useTempConfig(t)
	if download == nil {
		download = func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(bytes.Repeat([]byte("p"), 48<<10))
		}
	}
	urls := &hitList{}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.String()
		if r.Method == http.MethodConnect {
			target = r.Host
		} else if !r.URL.IsAbs() {
			target = r.RequestURI
		}
		urls.add(target)
		download(w, r)
	}))
	t.Cleanup(proxy.Close)

	payload := proxyPayload(t, mode)
	ctrlHits := &hitList{}
	ctrl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctrlHits.add(r.Method + " " + r.URL.Path)
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.Contains(r.URL.Path, "/delay") {
			if delay == nil {
				delay = map[string]int{}
			}
			_ = json.NewEncoder(w).Encode(delay)
			return
		}
		_, _ = w.Write(payload)
	}))
	t.Cleanup(ctrl.Close)

	client := detector.ProxyClientInfo{
		ID:                 "builtin",
		Name:               "内置内核",
		Running:            true,
		ExternalController: strings.TrimPrefix(ctrl.URL, "http://"),
		Secret:             "secret-marker",
	}
	rt := &appRuntime{
		hub:  newSSEHub(),
		core: &fakeCore{running: true, ports: core.Ports{SpeedTest: mustPort(t, proxy.URL)}},
		detect: func() detector.SystemSnapshot {
			return detector.SystemSnapshot{ActiveClient: client}
		},
	}
	return &speedFixture{rt: rt, ctrl: ctrlHits, urls: urls}
}

func (f *speedFixture) saveURL(t *testing.T) {
	t.Helper()
	cfg := configstore.LoadConfig()
	cfg.SpeedTestURL = "http://download.invalid/payload"
	cfg.SpeedTestMaxMB = 10
	if err := configstore.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func (f *speedFixture) collectSSE(dst *[][]byte) func() {
	ch := f.rt.hub.subscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for raw := range ch {
			*dst = append(*dst, append([]byte(nil), raw...))
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			f.rt.hub.unsubscribe(ch)
			<-done
		})
	}
}

func proxyPayload(t *testing.T, mode string) []byte {
	t.Helper()
	usHist := []map[string]int{}
	deadHist := []map[string]int{}
	switch mode {
	case "fresh":
	case "mixed":
		usHist = []map[string]int{{"delay": 35}}
		deadHist = []map[string]int{{"delay": 0}}
	case "dead":
		usHist = []map[string]int{{"delay": 0}}
		deadHist = []map[string]int{{"delay": 0}}
	default:
		t.Fatalf("mode %s", mode)
	}
	auto := core.BrowsingAutoName
	speed := clashscript.SpeedTestGroupName
	doc := map[string]any{
		"proxies": map[string]any{
			"PROXY": map[string]any{
				"name": "PROXY", "type": "Selector", "now": "US-1",
				"all": []string{"US-1", "DEAD", "DIRECT", auto},
			},
			speed: map[string]any{
				"name": speed, "type": "Selector", "now": "DIRECT",
				"all": []string{"US-1", "DEAD", "DIRECT"},
			},
			auto: map[string]any{
				"name": auto, "type": "URLTest", "now": "US-1",
				"all": []string{"US-1"},
			},
			"US-1":   map[string]any{"name": "US-1", "type": "Vless", "history": usHist},
			"DEAD":   map[string]any{"name": "DEAD", "type": "Vless", "history": deadHist},
			"DIRECT": map[string]any{"name": "DIRECT", "type": "Direct"},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func callSpeed(h http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func waitSpeed(t *testing.T, rt *appRuntime) speedtest.Progress {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	var last speedtest.Progress
	for time.Now().Before(deadline) {
		if rt.speed != nil {
			last = rt.speed.Progress()
			if last.TaskID != "" && !last.Running {
				return last
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("still running %+v", last)
	return last
}

func delayHits(hits []string) int {
	n := 0
	for _, hit := range hits {
		if strings.Contains(hit, "/delay") {
			n++
		}
	}
	return n
}

func assertSpeedPuts(t *testing.T, hits []string) {
	t.Helper()
	puts := 0
	for _, hit := range hits {
		if !strings.HasPrefix(hit, "PUT ") {
			continue
		}
		puts++
		if strings.Contains(hit, "/proxies/PROXY") || strings.Contains(hit, "AI-IDE") || !strings.Contains(hit, "SPEEDTEST") {
			t.Fatalf("selected %s in %v", hit, hits)
		}
	}
	if puts == 0 {
		t.Fatalf("no select in %v", hits)
	}
}
