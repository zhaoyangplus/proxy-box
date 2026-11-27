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
	"proxy-box/internal/diagnostics"
	"proxy-box/internal/speedtest"
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

// Locking:
//
// mu guards cancelStart and the speed runner pointer.
// shutdownMu guards system-proxy enable/restore and Shutdown's restore-then-stop section.
// opMu guards core configuration: mode switches, reloads, subscription list edits, and app switches that reload the core.
//
// Lock order is opMu, then shutdownMu. Never take opMu while holding shutdownMu. mu is independent.
// Binary download, subscription download, controller network checks, and delay tests run outside opMu.
// reloadLive is the one load-settings, copy-live-mode, Reload sequence. Callers hold opMu.

type appRuntime struct {
	hub          *sseHub
	core         coreController
	speed        *speedtest.Runner
	coreDir      string
	restore      func() error
	setProxy     func(host string, port int, bypass string) error
	ensureBinary func(ctx context.Context, coreDir string, proxyURLs []string, onProgress core.ProgressFunc) (string, error)
	importBinary func(srcPath, coreDir string) (string, error)
	proxyURLs    func() []string
	detect       func() detector.SystemSnapshot
	runDiag      func(clientName, proxyHost string, proxyPort int) diagnostics.DiagnosticReport

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
	mgr := core.NewManager(dir)
	a := &appRuntime{
		hub:          hub,
		core:         mgr,
		coreDir:      dir,
		restore:      sysproxy.Restore,
		setProxy:     sysproxy.Set,
		ensureBinary: core.EnsureBinary,
		importBinary: core.ImportBinary,
		flight:       newSubFlight(),
	}
	mgr.SetExitHook(a.handleCoreExit)
	return a
}

// handleCoreExit runs after mihomo exits by itself. A deliberate Stop does not call it.
func (a *appRuntime) handleCoreExit() {
	if a == nil {
		return
	}
	if a.restore != nil {
		_ = a.restore()
	}
	_ = configstore.UpdateCoreSettings(func(settings *configstore.CoreSettings) error {
		settings.SystemProxy = false
		return nil
	})
	a.broadcastCore("error", "内核意外退出，已还原系统代理", 0, 0)
	a.invalidateTrayState()
}

func bindCoreExit(a *appRuntime) {
	if a == nil || a.core == nil {
		return
	}
	hook, ok := a.core.(interface{ SetExitHook(func()) })
	if !ok {
		return
	}
	hook.SetExitHook(a.handleCoreExit)
}

// Shutdown stops a download test, cancels geo work, restores the system proxy, and then stops the core.
// A second call repeats that order. Overlapping calls wait and do the same.
func (a *appRuntime) Shutdown() {
	if a == nil {
		return
	}
	a.mu.Lock()
	cancel := a.cancelStart
	speed := a.speed
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if speed != nil {
		speed.Stop()
	}
	a.cancelGeo()
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
	if err := runCtx.Err(); err != nil {
		return core.Status{}, err
	}
	st, err := a.core.Start(runCtx, path, a.coreParams(settings), providerSpecs(subs))
	if err != nil {
		return core.Status{}, err
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	settings.Enabled = true
	settings.MixedPort = st.Ports.Mixed
	settings.ControllerPort = st.Ports.Controller
	settings.AIDedicatedPort = st.Ports.AIDedicated
	settings.SpeedTestPort = st.Ports.SpeedTest
	if st.Mode != "" {
		settings.Mode = st.Mode
	}
	if err := configstore.SaveCoreSettings(settings); err != nil {
		return st, err
	}
	if err := a.applyEnabledTargetsLocked(st.Ports.AIDedicated); err != nil {
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

// reloadLive loads settings, copies the running core mode, applies mutate, and reloads when the core is running.
// A nil mutate does not save settings. A non-nil mutate saves before the reload.
// The caller holds opMu.
func (a *appRuntime) reloadLive(ctx context.Context, mutate func(*configstore.CoreSettings) error) error {
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		return err
	}
	if a != nil && a.core != nil {
		if mode := strings.TrimSpace(a.core.Status().Mode); mode != "" {
			settings.Mode = mode
		}
	}
	if mutate != nil {
		if err := mutate(&settings); err != nil {
			return err
		}
		if err := configstore.SaveCoreSettings(settings); err != nil {
			return err
		}
	}
	if a == nil || a.core == nil || !a.core.Status().Running {
		return nil
	}
	subs, err := configstore.LoadSubscriptions()
	if err != nil {
		return err
	}
	if err := a.core.Reload(ctx, a.coreParams(settings), providerSpecs(subs)); err != nil {
		return err
	}
	a.kickGeoDownload()
	return nil
}

// noteProviderCounts asks the controller for provider sizes outside opMu, then merges those counts onto the current list.
func (a *appRuntime) noteProviderCounts(ctx context.Context) {
	if a == nil || a.core == nil || !a.core.Status().Running {
		return
	}
	st := a.core.Status()
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		return
	}
	counts, err := providerNodeCounts(ctx, st.Ports.Controller, settings.Secret)
	if err != nil {
		return
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	subs, err := configstore.LoadSubscriptions()
	if err != nil {
		return
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
		return
	}
	_ = configstore.SaveSubscriptions(subs)
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
	client := detector.DetectAll().ActiveClient
	if client.ID != "builtin" || !client.Running || client.MixedPort <= 0 {
		return nil
	}
	host := client.ProxyHost
	if host == "" {
		host = "127.0.0.1"
	}
	return []string{fmt.Sprintf("http://%s:%d", host, client.MixedPort)}
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
	cfg, _ := configstore.LoadConfig()
	return core.ConfigParams{
		MixedPort:       settings.MixedPort,
		ControllerPort:  settings.ControllerPort,
		AIDedicatedPort: settings.AIDedicatedPort,
		SpeedTestPort:   settings.SpeedTestPort,
		Secret:          settings.Secret,
		Mode:            settings.Mode,
		BypassCN:        settings.BypassCN,
		GeoReady:        core.GeoFilesReady(dir),
		EnabledAIApps:   cfg.EnabledAppIDs(),
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
