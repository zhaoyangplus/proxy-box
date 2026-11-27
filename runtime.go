package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"proxy-box/internal/configstore"
	"proxy-box/internal/core"
	"proxy-box/internal/detector"
	"proxy-box/internal/sysproxy"
)

type coreController interface {
	Start(ctx context.Context, binaryPath string, params core.ConfigParams, providers []core.ProviderSpec) (core.Status, error)
	Stop() error
	Reload(ctx context.Context, params core.ConfigParams, providers []core.ProviderSpec) error
	SwitchMode(ctx context.Context, mode string) error
	Status() core.Status
}

type coreEvent struct {
	Phase      string `json:"phase"`
	Downloaded int64  `json:"downloaded,omitempty"`
	Total      int64  `json:"total,omitempty"`
	Message    string `json:"message,omitempty"`
}

type appRuntime struct {
	hub          *sseHub
	core         coreController
	coreDir      string
	restore      func() error
	setProxy     func(host string, port int, bypass string) error
	ensureBinary func(ctx context.Context, coreDir string, proxyURLs []string, onProgress core.ProgressFunc) (string, error)
	importBinary func(srcPath, coreDir string) (string, error)
	proxyURLs    func() []string
	detect       func() detector.SystemSnapshot

	mu          sync.Mutex
	cancelStart context.CancelFunc
	shutdownMu  sync.Mutex
	opMu        sync.Mutex

	geoOnce      sync.Once
	geo          *geoCtl
	downloadGeo  func(context.Context, string, string, core.ProgressFunc) error
	preflightGeo func(context.Context, string, string, string) error
	flight       *subFlight
	trayState    trayStateCache
}

func newAppRuntime(hub *sseHub) *appRuntime {
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "ProxyBox", "core")
	return &appRuntime{
		hub:          hub,
		core:         core.NewManager(dir),
		coreDir:      dir,
		restore:      sysproxy.Restore,
		setProxy:     sysproxy.Set,
		ensureBinary: core.EnsureBinary,
		importBinary: core.ImportBinary,
		flight:       newSubFlight(),
	}
}

// Shutdown restores the system proxy and then stops the core.
// A second call repeats that order. Overlapping calls wait and do the same.
func (a *appRuntime) Shutdown() {
	if a == nil {
		return
	}
	a.mu.Lock()
	cancel := a.cancelStart
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.shutdownMu.Lock()
	defer a.shutdownMu.Unlock()
	if a.restore != nil {
		_ = a.restore()
	}
	if a.core != nil {
		_ = a.core.Stop()
	}
}

func (a *appRuntime) bootCore() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := a.StartEnabled(ctx); err != nil {
		a.broadcastCore("error", startFailure(err), 0, 0)
	}
}

func (a *appRuntime) StartEnabled(ctx context.Context) (core.Status, error) {
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		return core.Status{}, err
	}
	if !settings.Enabled {
		return core.Status{}, nil
	}
	return a.startCore(ctx, settings)
}

func (a *appRuntime) startCore(ctx context.Context, settings configstore.CoreSettings) (core.Status, error) {
	runCtx, cancel := a.replaceStart(ctx)
	defer cancel()

	a.broadcastCore("download", "正在准备内核", 0, 0)
	path, err := a.ensureBinary(runCtx, a.coreDir, a.candidateProxies(), func(downloaded, total int64) {
		a.broadcastCore("download", "正在下载内核", downloaded, total)
	})
	if err != nil {
		return core.Status{}, err
	}
	if err := runCtx.Err(); err != nil {
		return core.Status{}, err
	}
	subs, err := configstore.LoadSubscriptions()
	if err != nil {
		return core.Status{}, err
	}

	a.opMu.Lock()
	defer a.opMu.Unlock()
	if err := runCtx.Err(); err != nil {
		return core.Status{}, err
	}
	st, err := a.core.Start(runCtx, path, a.coreParams(settings), providerSpecs(subs))
	if err != nil {
		return core.Status{}, err
	}
	settings.Enabled = true
	settings.MixedPort = st.Ports.Mixed
	settings.ControllerPort = st.Ports.Controller
	settings.AIDedicatedPort = st.Ports.AIDedicated
	if st.Mode != "" {
		settings.Mode = st.Mode
	}
	if err := configstore.SaveCoreSettings(settings); err != nil {
		return st, err
	}
	a.kickGeoDownload()
	if settings.SystemProxy {
		if err := a.enableSystemProxy(runCtx, st.Ports.Mixed); err != nil {
			return st, err
		}
	}
	a.broadcastCore("running", "内核已启动", 0, 0)
	return st, nil
}

func (a *appRuntime) replaceStart(parent context.Context) (context.Context, context.CancelFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancelStart != nil {
		a.cancelStart()
	}
	ctx, cancel := context.WithCancel(parent)
	a.cancelStart = cancel
	return ctx, cancel
}

func (a *appRuntime) enableSystemProxy(ctx context.Context, port int) error {
	a.shutdownMu.Lock()
	defer a.shutdownMu.Unlock()
	if ctx.Err() != nil || a.core == nil || !a.core.Status().Running || a.setProxy == nil || port <= 0 {
		return ctx.Err()
	}
	return a.setProxy("127.0.0.1", port, "")
}

func (a *appRuntime) candidateProxies() []string {
	if a != nil && a.proxyURLs != nil {
		return a.proxyURLs()
	}
	return reachableProxyURLs()
}

func (a *appRuntime) broadcastCore(phase, message string, downloaded, total int64) {
	if a == nil || a.hub == nil {
		return
	}
	a.hub.broadcast(sseEvent{
		Type: "core",
		Core: &coreEvent{Phase: phase, Message: message, Downloaded: downloaded, Total: total},
	})
}

func (a *appRuntime) snapshot() detector.SystemSnapshot {
	if a != nil && a.detect != nil {
		return a.detect()
	}
	return detector.DetectAll()
}

func (a *appRuntime) coreMode() string {
	if a != nil && a.core != nil {
		if mode := strings.TrimSpace(a.core.Status().Mode); mode != "" {
			return mode
		}
	}
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		return ""
	}
	return settings.Mode
}

func reachableProxyURLs() []string {
	snap := detector.DetectAll()
	seen := map[string]bool{}
	var urls []string
	add := func(client detector.ProxyClientInfo) {
		if !client.PortReachable || !client.HTTPVerified || client.ProxyURL == "" || seen[client.ProxyURL] {
			return
		}
		seen[client.ProxyURL] = true
		urls = append(urls, client.ProxyURL)
	}
	for _, client := range snap.Clients {
		if client.ID == "builtin" && client.Running {
			add(client)
		}
	}
	for _, client := range snap.Clients {
		if client.ID != "builtin" {
			add(client)
		}
	}
	return urls
}

func providerSpecs(subs []configstore.Subscription) []core.ProviderSpec {
	out := make([]core.ProviderSpec, 0, len(subs))
	for _, sub := range subs {
		if !sub.Enabled || strings.TrimSpace(sub.ID) == "" {
			continue
		}
		out = append(out, core.ProviderSpec{
			ID:      sub.ID,
			Name:    sub.Name,
			Filter:  sub.Filter,
			Enabled: true,
		})
	}
	return out
}

func (a *appRuntime) coreParams(settings configstore.CoreSettings) core.ConfigParams {
	dir := ""
	if a != nil {
		dir = a.coreDir
	}
	return core.ConfigParams{
		MixedPort:       settings.MixedPort,
		ControllerPort:  settings.ControllerPort,
		AIDedicatedPort: settings.AIDedicatedPort,
		Secret:          settings.Secret,
		Mode:            settings.Mode,
		BypassCN:        settings.BypassCN,
		GeoReady:        core.GeoFilesReady(dir),
	}
}

func providerNodeCounts(ctx context.Context, port int, secret string) (map[string]int, error) {
	if port <= 0 {
		return nil, fmt.Errorf("controller port is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/providers/proxies", port), nil)
	if err != nil {
		return nil, err
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provider http %d", resp.StatusCode)
	}
	var body struct {
		Providers map[string]struct {
			Proxies []struct {
				Name string `json:"name"`
			} `json:"proxies"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(body.Providers))
	for id, provider := range body.Providers {
		counts[id] = len(provider.Proxies)
	}
	return counts, nil
}
