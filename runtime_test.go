package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"proxy-box/internal/configstore"
	"proxy-box/internal/core"
)

type fakeCore struct {
	log         *[]string
	running     bool
	mode        string
	ports       core.Ports
	version     string
	binary      string
	reloads     int
	failReloads int
	modes       []string
	started     core.ConfigParams
	last        core.ConfigParams
	seen        []core.ConfigParams
	onReload    func()
	onExit      func()
}

func (f *fakeCore) Start(_ context.Context, _ string, params core.ConfigParams, _ []core.ProviderSpec) (core.Status, error) {
	f.started = params
	f.running = true
	if f.mode == "" {
		f.mode = core.ModeRule
	}
	return f.Status(), nil
}

func (f *fakeCore) Stop() error {
	if f.log != nil {
		*f.log = append(*f.log, "stop")
	}
	f.running = false
	return nil
}

func (f *fakeCore) SetExitHook(fn func()) {
	if f == nil {
		return
	}
	f.onExit = fn
}

func (f *fakeCore) crash() {
	f.running = false
	if f.onExit != nil {
		f.onExit()
	}
}

func (f *fakeCore) Reload(_ context.Context, params core.ConfigParams, _ []core.ProviderSpec) error {
	f.reloads++
	f.last = params
	f.seen = append(f.seen, params)
	var err error
	if f.failReloads > 0 {
		f.failReloads--
		err = errors.New("reload refused")
	}
	if f.onReload != nil {
		f.onReload()
	}
	if err == nil && params.Mode != "" {
		f.mode = params.Mode
	}
	return err
}

func (f *fakeCore) SwitchMode(_ context.Context, mode string) error {
	f.mode = mode
	f.modes = append(f.modes, mode)
	return nil
}

func (f *fakeCore) Status() core.Status {
	return core.Status{
		Running:    f.running,
		Mode:       f.mode,
		Ports:      f.ports,
		Version:    f.version,
		BinaryPath: f.binary,
	}
}

func TestShutdownRestoresProxyBeforeStop(t *testing.T) {
	var log []string
	rt := &appRuntime{
		restore: func() error {
			log = append(log, "restore")
			return nil
		},
		core: &fakeCore{log: &log},
	}
	rt.Shutdown()
	rt.Shutdown()
	if strings.Join(log, ",") != "restore,stop,restore,stop" {
		t.Fatalf("log = %v", log)
	}

	log = nil
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rt.Shutdown()
		}()
	}
	wg.Wait()
	if len(log)%2 != 0 {
		t.Fatalf("log = %v", log)
	}
	for i := 0; i < len(log); i += 2 {
		if log[i] != "restore" || log[i+1] != "stop" {
			t.Fatalf("order = %v", log)
		}
	}
}

func TestShutdownCancelsGeoBeforeRestore(t *testing.T) {
	rt, fc := newGeoRuntime(t)
	entered := make(chan struct{})
	var mu sync.Mutex
	var geoCtx context.Context
	rt.downloadGeo = func(ctx context.Context, dir, proxy string, on core.ProgressFunc) error {
		mu.Lock()
		geoCtx = ctx
		mu.Unlock()
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	var log []string
	fc.log = &log
	rt.restore = func() error {
		mu.Lock()
		ctx := geoCtx
		mu.Unlock()
		if ctx == nil || ctx.Err() == nil {
			t.Errorf("geo job was not cancelled before restore")
		}
		log = append(log, "restore")
		return nil
	}
	if err := rt.beginGeo(true, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("geo download did not start")
	}
	rt.Shutdown()
	rt.Shutdown()
	if strings.Join(log, ",") != "restore,stop,restore,stop" {
		t.Fatalf("log = %v", log)
	}
}

func TestCoreCrashRestoresProxyWithoutFiringOnStop(t *testing.T) {
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
	restores := 0
	fc := &fakeCore{running: true}
	rt := &appRuntime{
		hub:  hub,
		core: fc,
		restore: func() error {
			restores++
			return nil
		},
	}
	bindCoreExit(rt)

	if err := fc.Stop(); err != nil {
		t.Fatal(err)
	}
	if restores != 0 {
		t.Fatalf("stop restores %d", restores)
	}
	select {
	case payload := <-ch:
		t.Fatalf("stop broadcast %s", payload)
	default:
	}

	fc.running = true
	fc.crash()
	if restores != 1 || fc.running {
		t.Fatalf("restores %d running %v", restores, fc.running)
	}
	stored, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.SystemProxy {
		t.Fatal("system proxy stayed on")
	}
	select {
	case payload := <-ch:
		var ev sseEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Type != "core" || ev.Core == nil || ev.Core.Phase != "error" || !strings.Contains(ev.Core.Message, "内核意外退出，已还原系统代理") {
			t.Fatalf("event = %+v", ev)
		}
	default:
		t.Fatal("crash did not broadcast")
	}
}

func TestModeSwitchAndReloadFinishOnPersistedMode(t *testing.T) {
	useTempConfig(t)
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Mode = core.ModeRule
	if err := configstore.SaveCoreSettings(settings); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	hold := make(chan struct{})
	var once sync.Once
	fc := &fakeCore{running: true, mode: core.ModeRule}
	fc.onReload = func() {
		once.Do(func() { close(entered) })
		<-hold
	}
	rt := &appRuntime{core: fc}
	reloadDone := make(chan error, 1)
	go func() {
		_, err := rt.reloadSubs(context.Background())
		reloadDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("reload did not start")
	}
	switchDone := make(chan error, 1)
	go func() {
		_, err := rt.applyCoreMode(context.Background(), core.ModeDirect)
		switchDone <- err
	}()
	select {
	case err := <-switchDone:
		close(hold)
		if reloadErr := <-reloadDone; reloadErr != nil {
			t.Fatal(reloadErr)
		}
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(150 * time.Millisecond):
		close(hold)
		if err := <-reloadDone; err != nil {
			t.Fatal(err)
		}
		if err := <-switchDone; err != nil {
			t.Fatal(err)
		}
	}
	stored, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if fc.Status().Mode != stored.Mode || stored.Mode != core.ModeDirect {
		t.Fatalf("core %s stored %s", fc.Status().Mode, stored.Mode)
	}
}

func TestModeSwitchProceedsWhileCoreStartIsBlocked(t *testing.T) {
	useTempConfig(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	fc := &fakeCore{}
	rt := &appRuntime{
		core:    &startGate{core: fc, started: started, release: release},
		coreDir: t.TempDir(),
		ensureBinary: func(context.Context, string, []string, core.ProgressFunc) (string, error) {
			return "mihomo.exe", nil
		},
	}
	go func() {
		settings, err := configstore.LoadCoreSettings()
		if err != nil {
			return
		}
		_, _ = rt.startCore(context.Background(), settings)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("start did not reach the core")
	}
	done := make(chan error, 1)
	go func() {
		_, err := rt.applyCoreMode(context.Background(), core.ModeDirect)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("mode switch blocked on core start")
	}
}

type startGate struct {
	core    *fakeCore
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *startGate) Start(ctx context.Context, binary string, params core.ConfigParams, providers []core.ProviderSpec) (core.Status, error) {
	g.once.Do(func() { close(g.started) })
	select {
	case <-g.release:
	case <-ctx.Done():
		return core.Status{}, ctx.Err()
	}
	return g.core.Start(ctx, binary, params, providers)
}

func (g *startGate) Stop() error { return g.core.Stop() }
func (g *startGate) Reload(ctx context.Context, params core.ConfigParams, providers []core.ProviderSpec) error {
	return g.core.Reload(ctx, params, providers)
}
func (g *startGate) SwitchMode(ctx context.Context, mode string) error {
	return g.core.SwitchMode(ctx, mode)
}
func (g *startGate) Status() core.Status { return g.core.Status() }

func TestEnableSystemProxyDoesNotRunAfterShutdownStarts(t *testing.T) {
	useTempConfig(t)
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.SystemProxy = false
	if err := configstore.SaveCoreSettings(settings); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	fc := &fakeCore{running: true, ports: core.Ports{Mixed: 27890}}
	setCalled := false
	rt := &appRuntime{
		core: fc,
		restore: func() error {
			close(entered)
			<-release
			return nil
		},
		setProxy: func(string, int, string) error {
			setCalled = true
			return nil
		},
	}
	go rt.Shutdown()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not start")
	}
	done := make(chan struct{})
	go func() {
		_, _ = rt.applySystemProxy(true)
		close(done)
	}()
	select {
	case <-done:
		close(release)
		t.Fatal("system proxy enable finished while shutdown was restoring")
	case <-time.After(80 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("enable did not finish after shutdown")
	}
	if setCalled {
		t.Fatal("system proxy was enabled after shutdown started")
	}
	stored, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.SystemProxy {
		t.Fatal("system proxy flag stayed on")
	}
}
