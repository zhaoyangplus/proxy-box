package speedtest

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"proxy-box/internal/clashscript"
	"proxy-box/internal/core"
)

func TestConstantsMatchDesign(t *testing.T) {
	if DefaultDownloadURL != "https://speed.cloudflare.com/__down?bytes=10485760" {
		t.Fatalf("download url %s", DefaultDownloadURL)
	}
	if FallbackDownloadURL != "https://proof.ovh.net/files/10Mb.dat" {
		t.Fatalf("fallback %s", FallbackDownloadURL)
	}
	if DefaultMaxBytes != 10<<20 || DefaultNodeTimeout != 6*time.Second || DefaultDialTimeout != 3*time.Second {
		t.Fatalf("limits %d %s %s", DefaultMaxBytes, DefaultNodeTimeout, DefaultDialTimeout)
	}
	if GroupName != clashscript.SpeedTestGroupName {
		t.Fatalf("group %s", GroupName)
	}
	if !skipped(Node{Name: core.DirectName}) || !skipped(Node{Name: "REJECT"}) || !skipped(Node{Name: core.BrowsingAutoName}) || !skipped(Node{Name: clashscript.AIAutoName}) {
		t.Fatal("skip list drifted from the core group names")
	}
}

func TestSpeedRoundsToTwoDecimals(t *testing.T) {
	bps, mbps := speeds(10<<20, 100*time.Millisecond)
	if bps != float64(10<<20)*10 || mbps != 100 {
		t.Fatalf("10MB in 100ms = %v B/s %v MB/s", bps, mbps)
	}
	_, mbps = speeds(1500*1024, time.Second)
	if mbps != 1.46 {
		t.Fatalf("1.46484375 MB/s rounded to %v", mbps)
	}
	_, mbps = speeds(1, time.Second)
	if mbps != 0 {
		t.Fatalf("tiny rate %v", mbps)
	}
}

func TestOneChunkFloorUsesReadStart(t *testing.T) {
	start := time.Unix(1_000, 0)
	end := start.Add(100 * time.Millisecond)
	elapsed, counted := sampleElapsed(1, 10<<20, 10<<20, start, end, end)
	if elapsed != 100*time.Millisecond || counted != 10<<20 {
		t.Fatalf("elapsed %s counted %d", elapsed, counted)
	}
	elapsed, counted = sampleElapsed(2, 3000, 1000, start, start.Add(40*time.Millisecond), start.Add(140*time.Millisecond))
	if elapsed != 100*time.Millisecond || counted != 2000 {
		t.Fatalf("multi elapsed %s counted %d", elapsed, counted)
	}
}

func TestOneChunkReadKeepsTheWait(t *testing.T) {
	body := &oneShot{delay: 60 * time.Millisecond, data: bytes.Repeat([]byte("a"), 48<<10)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	total, counted, elapsed, err := readLimited(ctx, body, DefaultMaxBytes, 5*time.Second, 2*time.Second, time.Now, cancel)
	if err != nil {
		t.Fatal(err)
	}
	if body.reads != 1 || total != int64(len(body.data)) || counted != total {
		t.Fatalf("reads %d total %d counted %d", body.reads, total, counted)
	}
	if elapsed < 50*time.Millisecond {
		t.Fatalf("elapsed %s", elapsed)
	}
	_, mbps := speeds(counted, elapsed)
	if mbps <= 0 || math.IsInf(mbps, 0) {
		t.Fatalf("mbps %v", mbps)
	}
}

func TestSingleNode(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("https_proxy", "http://127.0.0.1:1")

	proxy, seen := startProxy(t, func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() {
			t.Errorf("request was not proxied: %s", r.RequestURI)
		}
		if r.Header.Get("Connection") != "close" {
			t.Errorf("Connection = %q", r.Header.Get("Connection"))
		}
		writeBytes(w, 48<<10)
	})
	sel := &fakeSel{}
	runner := readyRunner(5 * time.Second)
	var mu sync.Mutex
	var steps []Progress
	runner.OnProgress(func(p Progress) {
		mu.Lock()
		steps = append(steps, p)
		mu.Unlock()
	})
	taskID, total, err := runner.Start(context.Background(), StartRequest{
		Nodes:       []Node{{Name: "US-1"}},
		ProxyURL:    proxy.URL,
		DownloadURL: "http://download.invalid/payload",
		FallbackURL: "http://fallback.invalid/payload",
		Selector:    sel,
	})
	if err != nil || taskID == "" || total != 1 {
		t.Fatalf("start %s %d %v", taskID, total, err)
	}
	progress := waitIdle(t, runner)
	if progress.Running || progress.Canceled || progress.Done != 1 || progress.TaskID != taskID {
		t.Fatalf("progress %+v", progress)
	}
	res, ok := runner.Cache()["US-1"]
	if !ok {
		t.Fatal("missing cache")
	}
	assertOK(t, res)
	if res.NodeName != "US-1" || res.TestedAt <= 0 {
		t.Fatalf("%+v", res)
	}
	urls := seen.urls()
	if len(urls) != 1 || !strings.Contains(urls[0], "download.invalid") {
		t.Fatalf("urls %v", urls)
	}
	assertOnlySpeedTest(t, sel.snapshot(), "US-1")
	mu.Lock()
	defer mu.Unlock()
	sawResult := false
	for _, step := range steps {
		if step.LastResult != nil && step.LastResult.NodeName == "US-1" && step.Done == 1 {
			sawResult = true
		}
	}
	if !sawResult {
		t.Fatalf("steps %+v", steps)
	}
}

func TestBatchSerialAndOnlySpeedTestGroup(t *testing.T) {
	proxy, _ := startProxy(t, func(w http.ResponseWriter, r *http.Request) {
		writeBytes(w, 48<<10)
	})
	sel := &fakeSel{}
	runner := readyRunner(5 * time.Second)
	nodes := []Node{
		{Name: "US-1"},
		{Name: "DIRECT"},
		{Name: "direct"},
		{Name: "REJECT"},
		{Name: "reject"},
		{Name: core.BrowsingAutoName},
		{Name: clashscript.AIAutoName},
		{Name: "HK-Auto", IsAuto: true},
		{Name: "JP-1"},
	}
	_, total, err := runner.Start(context.Background(), baseReq(proxy.URL, nodes, sel))
	if err != nil || total != 2 {
		t.Fatalf("start total %d err %v", total, err)
	}
	waitIdle(t, runner)
	if sel.overlap {
		t.Fatal("nodes ran together")
	}
	assertOnlySpeedTest(t, sel.snapshot(), "US-1", "JP-1")
	cache := runner.Cache()
	if _, ok := cache["DIRECT"]; ok {
		t.Fatal("cached DIRECT")
	}
	assertOK(t, cache["US-1"])
	assertOK(t, cache["JP-1"])
}

func TestCancelMidDownload(t *testing.T) {
	started := make(chan struct{})
	proxy, _ := startProxy(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("b"), 1024))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(started)
		<-r.Context().Done()
	})
	runner := readyRunner(5 * time.Second)
	_, _, err := runner.Start(context.Background(), baseReq(proxy.URL, []Node{{Name: "US-1"}}, &fakeSel{}))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("download did not start")
	}
	begin := time.Now()
	runner.Stop()
	runner.Stop()
	if time.Since(begin) > time.Second {
		t.Fatalf("stop took %s", time.Since(begin))
	}
	progress := runner.Progress()
	if progress.Running || !progress.Canceled {
		t.Fatalf("progress %+v", progress)
	}
	res := runner.Cache()["US-1"]
	if !strings.Contains(res.Error, "失败") || !strings.Contains(res.Error, "取消") {
		t.Fatalf("%+v", res)
	}
}

func TestBodyTruncatedAtMax(t *testing.T) {
	for _, mb := range []int{0, 5, 10} {
		t.Run(strconv.Itoa(mb), func(t *testing.T) {
			want := 10 << 20
			if mb == 5 {
				want = 5 << 20
			}
			proxy, _ := startProxy(t, func(w http.ResponseWriter, r *http.Request) {
				streamUntil(w, want+(1<<20))
			})
			runner := readyRunner(30 * time.Second)
			_, _, err := runner.Start(context.Background(), withMax(baseReq(proxy.URL, []Node{{Name: "US-1"}}, &fakeSel{}), mb))
			if err != nil {
				t.Fatal(err)
			}
			waitIdle(t, runner)
			res := runner.Cache()["US-1"]
			if res.Error != "" || res.BytesRead != int64(want) {
				t.Fatalf("%+v", res)
			}
			if res.SpeedMBps <= 0 {
				t.Fatalf("speed %v", res.SpeedMBps)
			}
		})
	}
}

func TestSlowBodyCutByNodeTimeout(t *testing.T) {
	proxy, _ := startProxy(t, func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(bytes.Repeat([]byte("s"), 48<<10)); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				if _, err := w.Write([]byte("x")); err != nil {
					return
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
		}
		t.Errorf("slow body was not cut")
	})
	runner := readyRunner(80 * time.Millisecond)
	begin := time.Now()
	_, _, err := runner.Start(context.Background(), baseReq(proxy.URL, []Node{{Name: "US-1"}}, &fakeSel{}))
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, runner)
	if time.Since(begin) > 1500*time.Millisecond {
		t.Fatalf("took %s", time.Since(begin))
	}
	res := runner.Cache()["US-1"]
	if res.Error != "" || res.BytesRead < 48<<10 || res.BytesRead > (48<<10)+32<<10 {
		t.Fatalf("%+v", res)
	}
	if res.DurationMs < 50 || res.DurationMs > 1000 {
		t.Fatalf("duration %d", res.DurationMs)
	}
}

func TestNon200(t *testing.T) {
	proxy, seen := startProxy(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	})
	runner := readyRunner(5 * time.Second)
	_, _, err := runner.Start(context.Background(), baseReq(proxy.URL, []Node{{Name: "US-1"}}, &fakeSel{}))
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, runner)
	res := runner.Cache()["US-1"]
	if !strings.Contains(res.Error, "失败") || !strings.Contains(res.Error, "404") || res.SpeedMBps != 0 {
		t.Fatalf("%+v", res)
	}
	if len(seen.urls()) != 1 {
		t.Fatalf("fallback ran: %v", seen.urls())
	}
}

func TestTinyBody(t *testing.T) {
	proxy, _ := startProxy(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("t"), 100))
	})
	runner := readyRunner(5 * time.Second)
	_, _, err := runner.Start(context.Background(), baseReq(proxy.URL, []Node{{Name: "US-1"}}, &fakeSel{}))
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, runner)
	res := runner.Cache()["US-1"]
	if !strings.Contains(res.Error, "失败") || !strings.Contains(res.Error, "32KB") || res.BytesRead != 100 {
		t.Fatalf("%+v", res)
	}
}

func TestCacheMergeAndReset(t *testing.T) {
	proxy, _ := startProxy(t, func(w http.ResponseWriter, r *http.Request) {
		writeBytes(w, 48<<10)
	})
	runner := readyRunner(5 * time.Second)
	sel := &fakeSel{}
	for _, name := range []string{"US-1", "JP-1"} {
		_, _, err := runner.Start(context.Background(), baseReq(proxy.URL, []Node{{Name: name}}, sel))
		if err != nil {
			t.Fatal(err)
		}
		waitIdle(t, runner)
	}
	cache := runner.Cache()
	if _, ok := cache["US-1"]; !ok {
		t.Fatal("lost US-1")
	}
	if _, ok := cache["JP-1"]; !ok {
		t.Fatal("lost JP-1")
	}
	runner.Reset()
	if len(runner.Cache()) != 0 {
		t.Fatalf("reset left %v", runner.Cache())
	}
	_, _, err := runner.Start(context.Background(), baseReq(proxy.URL, []Node{{Name: "US-1"}}, sel))
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, runner)
	cache = runner.Cache()
	if _, ok := cache["US-1"]; !ok || len(cache) != 1 {
		t.Fatalf("cache %v", cache)
	}
}

func TestRejectsConcurrentStart(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	sel := &fakeSel{entered: entered, block: release}
	proxy, _ := startProxy(t, func(w http.ResponseWriter, r *http.Request) {
		writeBytes(w, 48<<10)
	})
	runner := readyRunner(5 * time.Second)
	_, _, err := runner.Start(context.Background(), baseReq(proxy.URL, []Node{{Name: "US-1"}}, sel))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("selector did not run")
	}
	_, _, err = runner.Start(context.Background(), baseReq(proxy.URL, []Node{{Name: "JP-1"}}, sel))
	if err == nil || !strings.Contains(err.Error(), "已有测速任务正在运行") {
		t.Fatalf("second start %v", err)
	}
	close(release)
	waitIdle(t, runner)
	if _, ok := runner.Cache()["JP-1"]; ok {
		t.Fatal("second task ran")
	}
}

func TestFallbackAfterConnectionError(t *testing.T) {
	proxy, seen := startProxy(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.String(), "primary.invalid") {
			panic(http.ErrAbortHandler)
		}
		writeBytes(w, 48<<10)
	})
	runner := readyRunner(5 * time.Second)
	_, _, err := runner.Start(context.Background(), StartRequest{
		Nodes:       []Node{{Name: "US-1"}},
		ProxyURL:    proxy.URL,
		DownloadURL: "http://primary.invalid/file",
		FallbackURL: "http://fallback.invalid/file",
		Selector:    &fakeSel{},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, runner)
	res := runner.Cache()["US-1"]
	assertOK(t, res)
	urls := seen.urls()
	if len(urls) < 2 || !strings.Contains(urls[0], "primary.invalid") || !strings.Contains(urls[len(urls)-1], "fallback.invalid") {
		t.Fatalf("urls %v", urls)
	}
}

func TestCustomURLSkipsDefaults(t *testing.T) {
	proxy, seen := startProxy(t, func(w http.ResponseWriter, r *http.Request) {
		writeBytes(w, 48<<10)
	})
	runner := readyRunner(5 * time.Second)
	_, _, err := runner.Start(context.Background(), StartRequest{
		Nodes:       []Node{{Name: "US-1"}},
		ProxyURL:    proxy.URL,
		DownloadURL: "http://custom.invalid/a",
		FallbackURL: "http://fallback.invalid/b",
		Selector:    &fakeSel{},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, runner)
	assertOK(t, runner.Cache()["US-1"])
	joined := strings.Join(seen.urls(), " ")
	if !strings.Contains(joined, "custom.invalid") || strings.Contains(joined, "fallback.invalid") || strings.Contains(joined, "cloudflare.com") || strings.Contains(joined, "ovh.net") {
		t.Fatalf("urls %s", joined)
	}
}

func TestEmptyDownloadURLUsesBuiltinPrimary(t *testing.T) {
	proxy, seen := startProxy(t, func(w http.ResponseWriter, r *http.Request) {
		writeBytes(w, 48<<10)
	})
	runner := readyRunner(5 * time.Second)
	_, _, err := runner.Start(context.Background(), StartRequest{
		Nodes:       []Node{{Name: "US-1"}},
		ProxyURL:    proxy.URL,
		FallbackURL: "http://fallback.invalid/b",
		Selector:    &fakeSel{},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, runner)
	assertOK(t, runner.Cache()["US-1"])
	urls := seen.urls()
	if len(urls) == 0 || !strings.Contains(urls[0], "speed.cloudflare.com") || strings.Contains(urls[0], "fallback.invalid") {
		t.Fatalf("urls %v", urls)
	}
}

func TestRejectsBadMaxAndEmptyNodes(t *testing.T) {
	sel := &fakeSel{}
	runner := New()
	_, _, err := runner.Start(context.Background(), StartRequest{
		Nodes:    []Node{{Name: "US-1"}},
		MaxMB:    15,
		ProxyURL: "http://127.0.0.1:9",
		Selector: sel,
	})
	if err == nil || !strings.Contains(err.Error(), "5") || len(sel.snapshot()) != 0 {
		t.Fatalf("max %v calls %v", err, sel.snapshot())
	}
	_, _, err = runner.Start(context.Background(), StartRequest{
		Nodes:    []Node{{Name: "DIRECT"}, {Name: "⚡ AUTO", IsAuto: false}, {Name: "HK", IsAuto: true}},
		ProxyURL: "http://127.0.0.1:9",
		Selector: sel,
	})
	if err == nil || !strings.Contains(err.Error(), "没有可测速的节点") {
		t.Fatalf("nodes %v", err)
	}
	if len(sel.snapshot()) != 0 {
		t.Fatalf("calls %v", sel.snapshot())
	}
}

func TestSelectorFailureSkipsDownload(t *testing.T) {
	proxy, seen := startProxy(t, func(w http.ResponseWriter, r *http.Request) {
		writeBytes(w, 48<<10)
	})
	sel := &fakeSel{err: errSelect}
	runner := readyRunner(5 * time.Second)
	_, _, err := runner.Start(context.Background(), baseReq(proxy.URL, []Node{{Name: "US-1"}}, sel))
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(t, runner)
	res := runner.Cache()["US-1"]
	if !strings.Contains(res.Error, "失败") || len(seen.urls()) != 0 {
		t.Fatalf("%+v urls %v", res, seen.urls())
	}
	assertOnlySpeedTest(t, sel.snapshot(), "US-1")
}

type oneShot struct {
	delay time.Duration
	data  []byte
	reads int
}

func (o *oneShot) Read(p []byte) (int, error) {
	if o.reads > 0 {
		return 0, io.EOF
	}
	o.reads++
	time.Sleep(o.delay)
	return copy(p, o.data), io.EOF
}

func (o *oneShot) Close() error { return nil }

type fakeSel struct {
	mu       sync.Mutex
	calls    [][2]string
	inflight int
	overlap  bool
	block    chan struct{}
	entered  chan struct{}
	err      error
}

func (f *fakeSel) SelectGroupNode(group, node string) error {
	f.mu.Lock()
	if f.inflight > 0 {
		f.overlap = true
	}
	f.inflight++
	f.calls = append(f.calls, [2]string{group, node})
	block := f.block
	err := f.err
	entered := f.entered
	f.entered = nil
	f.mu.Unlock()
	if entered != nil {
		close(entered)
	}
	if block != nil {
		<-block
	} else {
		time.Sleep(20 * time.Millisecond)
	}
	f.mu.Lock()
	f.inflight--
	f.mu.Unlock()
	return err
}

func (f *fakeSel) snapshot() [][2]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][2]string, len(f.calls))
	copy(out, f.calls)
	return out
}

var errSelect = errString("select failed")

type errString string

func (e errString) Error() string { return string(e) }

type seenProxy struct {
	mu  sync.Mutex
	hit []string
	fn  func(http.ResponseWriter, *http.Request)
}

func (p *seenProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := r.URL.String()
	if !r.URL.IsAbs() {
		target = r.RequestURI
	}
	p.mu.Lock()
	p.hit = append(p.hit, target)
	p.mu.Unlock()
	p.fn(w, r)
}

func (p *seenProxy) urls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := append([]string(nil), p.hit...)
	return out
}

func startProxy(t *testing.T, fn func(http.ResponseWriter, *http.Request)) (*httptest.Server, *seenProxy) {
	t.Helper()
	p := &seenProxy{fn: fn}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	return srv, p
}

func readyRunner(node time.Duration) *Runner {
	return &Runner{
		cache:       map[string]Result{},
		nodeTimeout: node,
		dialTimeout: time.Second,
	}
}

func baseReq(proxy string, nodes []Node, sel Selector) StartRequest {
	return StartRequest{
		Nodes:       nodes,
		ProxyURL:    proxy,
		DownloadURL: "http://download.invalid/payload",
		FallbackURL: "http://fallback.invalid/payload",
		Selector:    sel,
	}
}

func withMax(req StartRequest, maxMB int) StartRequest {
	req.MaxMB = maxMB
	return req
}

func writeBytes(w http.ResponseWriter, n int) {
	buf := make([]byte, 32<<10)
	for n > 0 {
		m := len(buf)
		if m > n {
			m = n
		}
		nw, err := w.Write(buf[:m])
		n -= nw
		if err != nil {
			return
		}
	}
}

func streamUntil(w http.ResponseWriter, n int) {
	buf := make([]byte, 32<<10)
	sent := 0
	for sent < n {
		nw, err := w.Write(buf)
		sent += nw
		if err != nil {
			return
		}
	}
}

func waitIdle(t *testing.T, r *Runner) Progress {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last Progress
	for time.Now().Before(deadline) {
		last = r.Progress()
		if last.TaskID != "" && !last.Running {
			return last
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("still running: %+v", last)
	return last
}

func assertOK(t *testing.T, res Result) {
	t.Helper()
	if res.Error != "" || res.BytesRead < minBytes || res.DurationMs <= 0 || res.SpeedMBps <= 0 {
		t.Fatalf("%+v", res)
	}
	cents := res.SpeedMBps * 100
	if math.Abs(cents-math.Round(cents)) > 1e-6 || math.IsInf(res.SpeedMBps, 0) {
		t.Fatalf("speed %v", res.SpeedMBps)
	}
}

func assertOnlySpeedTest(t *testing.T, calls [][2]string, nodes ...string) {
	t.Helper()
	if len(calls) != len(nodes) {
		t.Fatalf("calls %+v", calls)
	}
	for i, call := range calls {
		if call[0] != GroupName || call[0] == "PROXY" || call[0] == "🤖 AI-IDE" || call[1] != nodes[i] {
			t.Fatalf("call %v", call)
		}
	}
}
