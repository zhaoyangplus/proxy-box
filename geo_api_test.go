package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"proxy-box/internal/configstore"
	"proxy-box/internal/core"
)

func TestCoreParamsGeoReadyFollowsFiles(t *testing.T) {
	dir := t.TempDir()
	rt := &appRuntime{coreDir: dir}
	settings := configstore.CoreSettings{
		MixedPort: 27890, ControllerPort: 27891, AIDedicatedPort: 27892,
		Secret: "s3cret", Mode: "rule", BypassCN: true,
	}
	if rt.coreParams(settings).GeoReady {
		t.Fatal("missing files looked ready")
	}
	if err := writeReadyGeo(dir); err != nil {
		t.Fatal(err)
	}
	params := rt.coreParams(settings)
	if !params.GeoReady || !params.BypassCN {
		t.Fatalf("params %+v", params)
	}
	html := append([]byte("<html>"), bytes.Repeat([]byte("g"), 1<<20)...)
	if err := os.WriteFile(filepath.Join(dir, core.GeoSiteFile), html, 0644); err != nil {
		t.Fatal(err)
	}
	if rt.coreParams(settings).GeoReady {
		t.Fatal("html page looked ready")
	}
}

func TestBypassCNReportsChineseErrors(t *testing.T) {
	useTempConfig(t)
	dir := t.TempDir()
	rt := &appRuntime{coreDir: dir, core: &fakeCore{}}
	rec := postJSON(rt.handleBypassCN, `{"enable":true}`)
	if !strings.Contains(rec.Body.String(), "内核未运行") {
		t.Fatalf("body %s", rec.Body.String())
	}
	rt.core = &fakeCore{running: true, ports: core.Ports{Mixed: 27890, Controller: 27891, AIDedicated: 27892}}
	rec = postJSON(rt.handleBypassCN, `{"enable":true}`)
	if !strings.Contains(rec.Body.String(), "没有可用节点") {
		t.Fatalf("body %s", rec.Body.String())
	}
}

func TestBypassCNDownloadsThenApplies(t *testing.T) {
	rt, fc := newGeoRuntime(t)
	var yaml string
	var yamlMu sync.Mutex
	var n atomic.Int32
	fc.onReload = func() { n.Add(1) }
	rt.downloadGeo = func(ctx context.Context, dir, proxy string, on core.ProgressFunc) error {
		if proxy != "http://127.0.0.1:27890" {
			return errors.New("geo download: bad proxy " + proxy)
		}
		if on != nil {
			on(1, 2)
		}
		return writeReadyGeo(dir)
	}
	rt.preflightGeo = func(ctx context.Context, binary, dir, config string) error {
		yamlMu.Lock()
		yaml = config
		yamlMu.Unlock()
		if binary == "" {
			return core.ErrGeoPreflight
		}
		return nil
	}
	rec := postJSON(rt.handleBypassCN, `{"enable":true,"retry":false}`)
	if rec.Code != 200 {
		t.Fatalf("body %s", rec.Body.String())
	}
	waitGeo(t, func() bool { return n.Load() >= 1 })
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.BypassCN || settings.SystemProxy {
		t.Fatalf("settings %+v", settings)
	}
	if !fc.last.BypassCN || !fc.last.GeoReady {
		t.Fatalf("reload %+v", fc.last)
	}
	yamlMu.Lock()
	got := yaml
	yamlMu.Unlock()
	assertBypassRuleOrder(t, got)
	body, err := rt.coreStatusResponse()
	if err != nil {
		t.Fatal(err)
	}
	if !body.GeoReady || body.GeoDownloading || body.GeoError != "" || body.Settings.BypassCN != true {
		t.Fatalf("status %+v", body)
	}
	raw := rec.Body.String()
	secret := settings.Secret
	if strings.Contains(raw, secret) {
		t.Fatalf("secret leaked %s", raw)
	}
}

func TestBypassCNRollsBackWhenPreflightFails(t *testing.T) {
	rt, fc := newGeoRuntime(t)
	var n atomic.Int32
	fc.onReload = func() { n.Add(1) }
	rt.downloadGeo = func(ctx context.Context, dir, proxy string, on core.ProgressFunc) error {
		return writeReadyGeo(dir)
	}
	rt.preflightGeo = func(context.Context, string, string, string) error {
		return core.ErrGeoPreflight
	}
	rec := postJSON(rt.handleBypassCN, `{"enable":true}`)
	if rec.Code != 200 {
		t.Fatalf("body %s", rec.Body.String())
	}
	waitGeo(t, func() bool { return n.Load() >= 1 })
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.BypassCN {
		t.Fatal("BypassCN stayed on")
	}
	_, _, geoErr := rt.geoView()
	if !strings.Contains(geoErr, "地理数据无法加载") {
		t.Fatalf("geoError %q", geoErr)
	}
	if fc.last.BypassCN {
		t.Fatalf("reload left bypass on %+v", fc.last)
	}
	if fc.running != true {
		t.Fatal("core stopped")
	}
}

func TestBypassCNRollsBackWhenReloadFails(t *testing.T) {
	rt, fc := newGeoRuntime(t)
	fc.failReloads = 1
	var n atomic.Int32
	fc.onReload = func() { n.Add(1) }
	rt.downloadGeo = func(ctx context.Context, dir, proxy string, on core.ProgressFunc) error {
		return writeReadyGeo(dir)
	}
	rt.preflightGeo = func(context.Context, string, string, string) error { return nil }
	if rec := postJSON(rt.handleBypassCN, `{"enable":true}`); rec.Code != 200 {
		t.Fatalf("body %s", rec.Body.String())
	}
	waitGeo(t, func() bool { return n.Load() >= 2 })
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.BypassCN {
		t.Fatal("BypassCN stayed on")
	}
	_, _, geoErr := rt.geoView()
	if !strings.Contains(geoErr, "已恢复原配置") {
		t.Fatalf("geoError %q", geoErr)
	}
	if len(fc.seen) < 2 || !fc.seen[0].BypassCN || !fc.seen[0].GeoReady || fc.seen[1].BypassCN {
		t.Fatalf("reloads %+v", fc.seen)
	}
}

func TestBypassCNDisablePersists(t *testing.T) {
	useTempConfig(t)
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.BypassCN = true
	settings.SystemProxy = false
	if err := configstore.SaveCoreSettings(settings); err != nil {
		t.Fatal(err)
	}
	fc := &fakeCore{running: true, ports: core.Ports{Mixed: 27890, Controller: 27891, AIDedicated: 27892}}
	rt := &appRuntime{coreDir: t.TempDir(), core: fc}
	rec := postJSON(rt.handleBypassCN, `{"enable":false,"retry":false}`)
	if rec.Code != 200 {
		t.Fatalf("body %s", rec.Body.String())
	}
	stored, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if stored.BypassCN || stored.SystemProxy || fc.reloads != 1 || fc.last.BypassCN {
		t.Fatalf("stored %+v reloads %d last %+v", stored, fc.reloads, fc.last)
	}
}

func TestBypassCNKeepsOneDownload(t *testing.T) {
	rt, _ := newGeoRuntime(t)
	release := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(release) }) }
	t.Cleanup(stop)
	var calls atomic.Int32
	rt.downloadGeo = func(ctx context.Context, dir, proxy string, on core.ProgressFunc) error {
		calls.Add(1)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
		}
		return writeReadyGeo(dir)
	}
	rt.preflightGeo = func(context.Context, string, string, string) error { return nil }
	first := postJSON(rt.handleBypassCN, `{"enable":true}`)
	waitGeo(t, func() bool { return calls.Load() == 1 })
	second := postJSON(rt.handleBypassCN, `{"enable":true}`)
	if first.Code != 200 || second.Code != 200 {
		t.Fatalf("first %s second %s", first.Body.String(), second.Body.String())
	}
	if !strings.Contains(first.Body.String(), `"geoDownloading":true`) || !strings.Contains(second.Body.String(), `"geoDownloading":true`) {
		t.Fatalf("first %s second %s", first.Body.String(), second.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("downloads %d", calls.Load())
	}
	stop()
	waitGeo(t, func() bool {
		_, busy, _ := rt.geoView()
		return !busy
	})
}

func TestRetryRestartsDownload(t *testing.T) {
	rt, fc := newGeoRuntime(t)
	if err := writeReadyGeo(rt.coreDir); err != nil {
		t.Fatal(err)
	}
	rt.storeGeoError(0, "旧错误")
	var calls atomic.Int32
	rt.downloadGeo = func(ctx context.Context, dir, proxy string, on core.ProgressFunc) error {
		calls.Add(1)
		if _, err := os.Stat(filepath.Join(dir, core.GeoIPFile)); !os.IsNotExist(err) {
			return errors.New("geo download: old file still present")
		}
		return writeReadyGeo(dir)
	}
	rt.preflightGeo = func(context.Context, string, string, string) error { return nil }
	var n atomic.Int32
	fc.onReload = func() { n.Add(1) }
	rec := postJSON(rt.handleBypassCN, `{"enable":true,"retry":true}`)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "旧错误") {
		t.Fatalf("body %s", rec.Body.String())
	}
	waitGeo(t, func() bool { return n.Load() >= 1 })
	if calls.Load() != 1 {
		t.Fatalf("downloads %d", calls.Load())
	}
	if _, _, geoErr := rt.geoView(); geoErr != "" {
		t.Fatalf("geoError %q", geoErr)
	}
}

func TestStartWithBypassCNKicksDownloadOnceNodesExist(t *testing.T) {
	rt, fc := newGeoRuntime(t)
	var n atomic.Int32
	var proxy string
	var proxyMu sync.Mutex
	fc.onReload = func() { n.Add(1) }
	rt.ensureBinary = func(context.Context, string, []string, core.ProgressFunc) (string, error) {
		return "mihomo.exe", nil
	}
	rt.proxyURLs = func() []string { return nil }
	rt.downloadGeo = func(ctx context.Context, dir, got string, on core.ProgressFunc) error {
		proxyMu.Lock()
		proxy = got
		proxyMu.Unlock()
		return writeReadyGeo(dir)
	}
	rt.preflightGeo = func(context.Context, string, string, string) error { return nil }
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Enabled = true
	settings.BypassCN = true
	settings.SystemProxy = false
	if _, err := rt.startCore(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if fc.started.GeoReady || !fc.started.BypassCN {
		t.Fatalf("start params %+v", fc.started)
	}
	waitGeo(t, func() bool { return n.Load() >= 1 })
	proxyMu.Lock()
	got := proxy
	proxyMu.Unlock()
	if got != "http://127.0.0.1:27890" {
		t.Fatalf("proxy %s", got)
	}
	if !fc.last.GeoReady || !fc.last.BypassCN {
		t.Fatalf("reload %+v", fc.last)
	}
}

func newGeoRuntime(t *testing.T) (*appRuntime, *fakeCore) {
	t.Helper()
	useTempConfig(t)
	if err := configstore.SaveSubscriptions([]configstore.Subscription{{
		ID: "sub", Name: "sub", URL: "http://127.0.0.1/sub", Enabled: true, NodeCount: 2,
	}}); err != nil {
		t.Fatal(err)
	}
	fc := &fakeCore{
		running: true,
		mode:    core.ModeRule,
		binary:  "mihomo.exe",
		ports:   core.Ports{Mixed: 27890, Controller: 27891, AIDedicated: 27892},
	}
	return &appRuntime{coreDir: t.TempDir(), core: fc}, fc
}

func writeReadyGeo(dir string) error {
	body := bytes.Repeat([]byte("g"), 1<<20)
	for _, name := range []string{core.GeoIPFile, core.GeoSiteFile} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0644); err != nil {
			return err
		}
	}
	return nil
}

func assertBypassRuleOrder(t *testing.T, yaml string) {
	t.Helper()
	cn := strings.Index(yaml, "GEOSITE,cn,DIRECT")
	rest := strings.Index(yaml, "GEOSITE,geolocation-!cn,PROXY")
	ip := strings.Index(yaml, "GEOIP,CN,DIRECT")
	match := strings.Index(yaml, "MATCH,PROXY")
	if cn < 0 || rest < cn || ip < rest || match < ip {
		t.Fatalf("rule order\n%s", yaml)
	}
}

func waitGeo(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out")
}
