// Package speedtest measures download speed through the dedicated 🧪 SPEEDTEST proxy.
// One task runs at a time, and each task walks its nodes serially.
package speedtest

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultDownloadURL is the Cloudflare 10 MB payload.
	DefaultDownloadURL = "https://speed.cloudflare.com/__down?bytes=10485760"
	// FallbackDownloadURL is tried once when the first URL fails before any HTTP response.
	FallbackDownloadURL = "https://proof.ovh.net/files/10Mb.dat"
	DefaultMaxBytes     = 10 << 20
	DefaultNodeTimeout  = 6 * time.Second
	DefaultDialTimeout  = 3 * time.Second

	// GroupName is the only selector the runner switches.
	GroupName = "🧪 SPEEDTEST"

	minBytes = 32 << 10
)

// Result is one finished node.
type Result struct {
	NodeName   string  `json:"nodeName"`
	SpeedBps   float64 `json:"speedBps"`
	SpeedMBps  float64 `json:"speedMBps"`
	BytesRead  int64   `json:"bytesRead"`
	DurationMs int64   `json:"durationMs"`
	Error      string  `json:"error,omitempty"`
	TestedAt   int64   `json:"testedAt"`
}

// Progress is the task snapshot pushed after each step.
type Progress struct {
	TaskID      string  `json:"taskId"`
	Running     bool    `json:"running"`
	CurrentNode string  `json:"currentNode,omitempty"`
	Done        int     `json:"done"`
	Total       int     `json:"total"`
	LastResult  *Result `json:"lastResult,omitempty"`
	Canceled    bool    `json:"canceled,omitempty"`
}

// Node is one candidate. Auto groups are skipped.
type Node struct {
	Name   string
	IsAuto bool
}

// Selector switches a mihomo selector group. Tests pass a fake.
// The runner calls it only with GroupName.
type Selector interface {
	SelectGroupNode(group, node string) error
}

// StartRequest is the input for one task.
// DownloadURL empty means DefaultDownloadURL.
// FallbackURL empty means FallbackDownloadURL.
// MaxMB 0 means 10. Any other value must be 5, 10, or 25.
type StartRequest struct {
	Nodes       []Node
	MaxMB       int
	ProxyURL    string
	DownloadURL string
	FallbackURL string
	Selector    Selector
}

// Runner is the single in-process speed test.
type Runner struct {
	mu          sync.Mutex
	cache       map[string]Result
	progress    Progress
	running     bool
	cancel      context.CancelFunc
	done        chan struct{}
	onProgress  func(Progress)
	nodeTimeout time.Duration
	dialTimeout time.Duration
	now         func() time.Time
	seq         uint64
}

// New returns an idle runner with an empty cache.
func New() *Runner {
	return &Runner{cache: make(map[string]Result)}
}

// OnProgress registers the callback invoked after each step.
// The callback runs without the runner lock.
func (r *Runner) OnProgress(fn func(Progress)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.onProgress = fn
	r.mu.Unlock()
}

// Start runs nodes one by one in the background.
// A second start while the task is alive returns a Chinese error.
func (r *Runner) Start(ctx context.Context, req StartRequest) (taskID string, total int, err error) {
	if r == nil {
		return "", 0, errors.New("测速尚未初始化")
	}
	maxBytes, err := parseMaxMB(req.MaxMB)
	if err != nil {
		return "", 0, err
	}
	if _, err := proxyURL(req.ProxyURL); err != nil {
		return "", 0, err
	}
	if req.Selector == nil {
		return "", 0, errors.New("测速选择器未就绪")
	}
	nodes := filterNodes(req.Nodes)
	if len(nodes) == 0 {
		return "", 0, errors.New("没有可测速的节点")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return "", 0, errors.New("已有测速任务正在运行")
	}
	nodeTimeout := r.nodeTimeout
	if nodeTimeout <= 0 {
		nodeTimeout = DefaultNodeTimeout
	}
	dialTimeout := r.dialTimeout
	if dialTimeout <= 0 {
		dialTimeout = DefaultDialTimeout
	}
	now := r.now
	if now == nil {
		now = time.Now
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.seq++
	taskID = strconv.FormatUint(r.seq, 10)
	done := make(chan struct{})
	r.running = true
	r.cancel = cancel
	r.done = done
	r.progress = Progress{TaskID: taskID, Running: true, Total: len(nodes)}
	r.mu.Unlock()

	go func() {
		defer close(done)
		r.run(runCtx, nodes, req, maxBytes, nodeTimeout, dialTimeout, now)
	}()
	return taskID, len(nodes), nil
}

// Stop cancels the active task and waits until it has left the read loop.
// An idle runner returns immediately. A second call is a no-op.
func (r *Runner) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	cancel := r.cancel
	done := r.done
	r.progress.Canceled = true
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// Progress returns a copy of the current task snapshot.
func (r *Runner) Progress() Progress {
	if r == nil {
		return Progress{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return copyProgress(r.progress)
}

// Cache returns a copy of results keyed by node name.
func (r *Runner) Cache() map[string]Result {
	if r == nil {
		return map[string]Result{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]Result, len(r.cache))
	for name, item := range r.cache {
		out[name] = item
	}
	return out
}

// Reset drops cached results. It does not cancel a running task.
func (r *Runner) Reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.cache = make(map[string]Result)
	r.mu.Unlock()
}

func (r *Runner) run(ctx context.Context, nodes []Node, req StartRequest, maxBytes int64, nodeTimeout, dialTimeout time.Duration, now func() time.Time) {
	defer func() {
		r.mu.Lock()
		r.running = false
		r.progress.Running = false
		r.progress.CurrentNode = ""
		if ctx.Err() != nil {
			r.progress.Canceled = true
		}
		r.mu.Unlock()
		r.emit()
	}()
	r.emit()
	for i, node := range nodes {
		if ctx.Err() != nil {
			return
		}
		r.mu.Lock()
		r.progress.CurrentNode = node.Name
		r.mu.Unlock()
		r.emit()

		res := measure(ctx, node.Name, req, maxBytes, nodeTimeout, dialTimeout, now)
		r.mu.Lock()
		if r.cache == nil {
			r.cache = make(map[string]Result)
		}
		r.cache[node.Name] = res
		r.progress.Done = i + 1
		stored := res
		r.progress.LastResult = &stored
		if ctx.Err() != nil {
			r.progress.Canceled = true
		}
		r.mu.Unlock()
		r.emit()
		if ctx.Err() != nil {
			return
		}
	}
}

func (r *Runner) emit() {
	r.mu.Lock()
	progress := copyProgress(r.progress)
	fn := r.onProgress
	r.mu.Unlock()
	if fn != nil {
		fn(progress)
	}
}

func copyProgress(p Progress) Progress {
	if p.LastResult != nil {
		stored := *p.LastResult
		p.LastResult = &stored
	}
	return p
}

func measure(ctx context.Context, node string, req StartRequest, maxBytes int64, nodeTimeout, dialTimeout time.Duration, now func() time.Time) Result {
	res := Result{NodeName: node, TestedAt: now().Unix()}
	if err := req.Selector.SelectGroupNode(GroupName, node); err != nil {
		res.Error = "失败：无法切换到该节点"
		return res
	}
	if ctx.Err() != nil {
		res.Error = "失败：已取消"
		return res
	}
	client, err := newClient(req.ProxyURL, dialTimeout)
	if err != nil {
		res.Error = "失败：测速代理地址无效"
		return res
	}
	primary := strings.TrimSpace(req.DownloadURL)
	if primary == "" {
		primary = DefaultDownloadURL
	}
	fallback := strings.TrimSpace(req.FallbackURL)
	if fallback == "" {
		fallback = FallbackDownloadURL
	}
	total, counted, elapsed, status, err := fetch(ctx, client, primary, maxBytes, nodeTimeout, dialTimeout, now)
	if err != nil && ctx.Err() == nil && primary != fallback {
		total, counted, elapsed, status, err = fetch(ctx, client, fallback, maxBytes, nodeTimeout, dialTimeout, now)
	}
	if ctx.Err() != nil && total < minBytes {
		res.BytesRead = total
		res.Error = "失败：已取消"
		return res
	}
	if err != nil {
		res.Error = "失败：无法连接测速服务器"
		return res
	}
	if status < 200 || status >= 300 {
		res.Error = fmt.Sprintf("失败：HTTP %d", status)
		return res
	}
	res.BytesRead = total
	res.DurationMs = durationMs(elapsed)
	if total < minBytes {
		res.Error = "失败：下载不足 32KB"
		return res
	}
	res.SpeedBps, res.SpeedMBps = speeds(counted, elapsed)
	return res
}

func fetch(ctx context.Context, client *http.Client, rawURL string, maxBytes int64, nodeTimeout, dialTimeout time.Duration, now func() time.Time) (total, counted int64, elapsed time.Duration, status int, err error) {
	readCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(readCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	req.Close = true
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return 0, 0, 0, resp.StatusCode, nil
	}
	total, counted, elapsed, err = readLimited(readCtx, resp.Body, maxBytes, nodeTimeout, dialTimeout, now, cancel)
	return total, counted, elapsed, resp.StatusCode, err
}

// readLimited counts payload bytes. The clock starts after the first non-empty
// chunk. A body that arrives in that single read uses the whole read wait as
// the duration, so a fast buffer cannot report an infinite speed.
func readLimited(ctx context.Context, body io.ReadCloser, maxBytes int64, nodeTimeout, firstByteTimeout time.Duration, now func() time.Time, cancel context.CancelFunc) (total, counted int64, elapsed time.Duration, err error) {
	defer body.Close()
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = body.Close()
		case <-stopped:
		}
	}()
	defer close(stopped)

	bufSize := 256 << 10
	if maxBytes > 0 && maxBytes < int64(bufSize) {
		bufSize = int(maxBytes)
	}
	if bufSize < 1 {
		bufSize = 1
	}
	buf := make([]byte, bufSize)
	firstTimer := time.AfterFunc(firstByteTimeout, cancel)
	defer firstTimer.Stop()
	var nodeTimer *time.Timer
	defer func() {
		if nodeTimer != nil {
			nodeTimer.Stop()
		}
	}()

	var first int64
	chunks := 0
	var tBefore time.Time
	var tFirst time.Time
	for total < maxBytes {
		if ctx.Err() != nil {
			break
		}
		if chunks > 0 && now().Sub(tFirst) >= nodeTimeout {
			break
		}
		if chunks == 0 {
			tBefore = now()
		}
		n, readErr := body.Read(buf)
		if n > 0 {
			if remain := maxBytes - total; int64(n) > remain {
				n = int(remain)
			}
			if chunks == 0 {
				tFirst = now()
				first = int64(n)
				firstTimer.Stop()
				nodeTimer = time.AfterFunc(nodeTimeout, cancel)
			}
			total += int64(n)
			chunks++
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			if ctx.Err() != nil {
				break
			}
			if total == 0 {
				return 0, 0, 0, readErr
			}
			break
		}
	}
	elapsed, counted = sampleElapsed(chunks, total, first, tBefore, tFirst, now())
	return total, counted, elapsed, nil
}

func sampleElapsed(chunks int, total, first int64, tBefore, tFirst, tEnd time.Time) (time.Duration, int64) {
	if chunks <= 0 {
		return 0, 0
	}
	if chunks == 1 {
		elapsed := tEnd.Sub(tBefore)
		if elapsed <= 0 {
			elapsed = time.Millisecond
		}
		return elapsed, total
	}
	elapsed := tEnd.Sub(tFirst)
	counted := total - first
	if elapsed <= 0 {
		elapsed = tEnd.Sub(tBefore)
		if elapsed <= 0 {
			elapsed = time.Millisecond
		}
		return elapsed, total
	}
	if counted < 0 {
		counted = 0
	}
	return elapsed, counted
}

func durationMs(elapsed time.Duration) int64 {
	ms := elapsed.Milliseconds()
	if ms <= 0 && elapsed > 0 {
		ms = 1
	}
	return ms
}

func speeds(bytes int64, elapsed time.Duration) (bps, mbps float64) {
	if bytes < 0 {
		bytes = 0
	}
	ms := elapsed.Milliseconds()
	if ms <= 0 {
		ms = 1
	}
	secs := float64(ms) / 1000
	bps = float64(bytes) / secs
	mbps = math.Round(bps/1024/1024*100) / 100
	return bps, mbps
}

func parseMaxMB(maxMB int) (int64, error) {
	if maxMB == 0 {
		maxMB = 10
	}
	switch maxMB {
	case 5, 10, 25:
		return int64(maxMB) << 20, nil
	default:
		return 0, errors.New("测速大小只允许 5、10 或 25 MB")
	}
}

func filterNodes(in []Node) []Node {
	out := make([]Node, 0, len(in))
	for _, node := range in {
		node.Name = strings.TrimSpace(node.Name)
		if skipped(node) {
			continue
		}
		out = append(out, node)
	}
	return out
}

func skipped(node Node) bool {
	if node.IsAuto || node.Name == "" {
		return true
	}
	switch strings.ToUpper(node.Name) {
	case "DIRECT", "REJECT":
		return true
	}
	switch node.Name {
	case "⚡ AUTO", "⚡ AI-Auto":
		return true
	}
	return false
}

func proxyURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("测速代理地址无效")
	}
	return u, nil
}

func newClient(rawProxy string, dialTimeout time.Duration) (*http.Client, error) {
	u, err := proxyURL(rawProxy)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: dialTimeout}
	transport := &http.Transport{
		Proxy:                 http.ProxyURL(u),
		DisableKeepAlives:     true,
		DisableCompression:    true,
		ForceAttemptHTTP2:     false,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: dialTimeout,
	}
	return &http.Client{Transport: transport}, nil
}
