package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"proxy-box/internal/configstore"
	"proxy-box/internal/core"
)

// geoCtl is the single background download. gen rejects a job that lost the race with retry or disable.
// untrusted is set when preflight rejects the files. geoView stays false while those same files remain.
type geoCtl struct {
	mu        sync.Mutex
	gen       int
	busy      bool
	apply     bool
	errText   string
	cancel    context.CancelFunc
	untrusted bool
	badStamp  geoFileStamp
}

type geoFileStamp struct {
	ip   geoOneStamp
	site geoOneStamp
}

type geoOneStamp struct {
	ok   bool
	size int64
	mod  int64
}

func (a *appRuntime) geoCtl() *geoCtl {
	a.geoOnce.Do(func() { a.geo = &geoCtl{} })
	return a.geo
}

func (a *appRuntime) geoView() (ready, downloading bool, errText string) {
	if a == nil {
		return false, false, ""
	}
	g := a.geoCtl()
	g.mu.Lock()
	defer g.mu.Unlock()
	return a.geoFilesReady(g), g.busy, g.errText
}

func (a *appRuntime) geoFilesReady(g *geoCtl) bool {
	if !core.GeoFilesReady(a.coreDir) {
		return false
	}
	if g.untrusted && currentGeoStamp(a.coreDir) == g.badStamp {
		return false
	}
	return true
}

func (a *appRuntime) setBypassCN(ctx context.Context, enable, retry bool) error {
	if !enable && !retry {
		a.cancelGeo()
		a.clearGeoError()
		return a.persistBypass(ctx, false)
	}
	if a == nil || a.core == nil || !a.core.Status().Running || a.core.Status().Ports.Mixed <= 0 {
		return core.ErrNotRunning
	}
	if retry {
		a.clearGeoError()
		settings, err := configstore.LoadCoreSettings()
		if err != nil {
			return err
		}
		return a.beginGeo(enable || settings.BypassCN, true)
	}
	if core.GeoFilesReady(a.coreDir) {
		return a.applyBypass(ctx, 0)
	}
	return a.beginGeo(true, false)
}

// kickGeoDownload starts one download when bypass is already on, the files are missing, and a subscription has nodes.
func (a *appRuntime) kickGeoDownload() {
	if a == nil || a.core == nil || !a.core.Status().Running {
		return
	}
	settings, err := configstore.LoadCoreSettings()
	if err != nil || !settings.BypassCN || core.GeoFilesReady(a.coreDir) {
		return
	}
	g := a.geoCtl()
	g.mu.Lock()
	busy, failed := g.busy, g.errText != ""
	g.mu.Unlock()
	if busy || failed {
		return
	}
	_ = a.beginGeo(true, false)
}

func (a *appRuntime) beginGeo(apply, force bool) error {
	if a == nil || a.core == nil || !a.core.Status().Running || a.core.Status().Ports.Mixed <= 0 {
		return core.ErrNotRunning
	}
	if !subsHaveNodes() {
		return errors.New("没有可用节点，无法下载地理数据")
	}
	g := a.geoCtl()
	g.mu.Lock()
	if g.busy && !force {
		if apply {
			g.apply = true
		}
		g.mu.Unlock()
		return nil
	}
	if g.cancel != nil {
		g.cancel()
	}
	g.gen++
	gen := g.gen
	ctx, cancel := context.WithCancel(context.Background())
	g.cancel = cancel
	g.busy = true
	g.apply = apply
	g.errText = ""
	g.mu.Unlock()
	if force {
		removeGeoFiles(a.coreDir)
	}
	go a.runGeo(ctx, gen)
	return nil
}

func (a *appRuntime) runGeo(ctx context.Context, gen int) {
	defer a.finishGeo(gen)
	if !a.geoCurrent(gen) {
		return
	}
	port := a.core.Status().Ports.Mixed
	a.broadcastCore("geo", "正在下载地理数据", 0, 0)
	err := a.callDownload(ctx, fmt.Sprintf("http://127.0.0.1:%d", port))
	if ctx.Err() != nil || !a.geoCurrent(gen) {
		return
	}
	if err != nil {
		a.failGeo(gen, err)
		return
	}
	a.clearGeoUntrusted(gen)
	g := a.geoCtl()
	g.mu.Lock()
	apply := g.apply && g.gen == gen
	g.mu.Unlock()
	if !apply {
		a.broadcastCore("geo", "地理数据已下载", 0, 0)
		return
	}
	_ = a.applyBypass(ctx, gen)
}

func (a *appRuntime) applyBypass(ctx context.Context, gen int) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if gen != 0 && !a.geoCurrent(gen) {
		return nil
	}
	if a.core == nil || !a.core.Status().Running {
		return a.failApply(ctx, gen, core.ErrNotRunning)
	}
	if !core.GeoFilesReady(a.coreDir) {
		return a.failApply(ctx, gen, core.ErrGeoTooSmall)
	}
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		return err
	}
	if mode := strings.TrimSpace(a.core.Status().Mode); mode != "" {
		settings.Mode = mode
	}
	settings.BypassCN = true
	subs, err := configstore.LoadSubscriptions()
	if err != nil {
		return err
	}
	params := a.coreParams(settings)
	body, err := core.Render(params, providerSpecs(subs))
	if err != nil {
		return err
	}
	if err := a.callPreflight(ctx, a.core.Status().BinaryPath, body); err != nil {
		a.markGeoUntrusted(gen)
		return a.failApply(ctx, gen, err)
	}
	if gen != 0 && !a.geoCurrent(gen) {
		return nil
	}
	if err := configstore.SaveCoreSettings(settings); err != nil {
		return err
	}
	if err := a.core.Reload(ctx, params, providerSpecs(subs)); err != nil {
		return a.failApply(ctx, gen, errors.New("绕过大陆切换失败，已恢复原配置"))
	}
	a.clearGeoError()
	a.broadcastCore("geo", "已开启绕过大陆", 0, 0)
	return nil
}

func (a *appRuntime) failApply(ctx context.Context, gen int, cause error) error {
	if gen != 0 && !a.geoCurrent(gen) {
		return nil
	}
	settings, err := configstore.LoadCoreSettings()
	msg := publicErr(cause)
	if err == nil {
		settings.BypassCN = false
		if a.core != nil {
			if mode := strings.TrimSpace(a.core.Status().Mode); mode != "" {
				settings.Mode = mode
			}
		}
		_ = configstore.SaveCoreSettings(settings)
		if a.core != nil && a.core.Status().Running {
			if subs, loadErr := configstore.LoadSubscriptions(); loadErr == nil {
				_ = a.core.Reload(ctx, a.coreParams(settings), providerSpecs(subs))
			}
		}
	}
	a.storeGeoError(gen, msg)
	a.broadcastCore("geo", msg, 0, 0)
	return errors.New(msg)
}

func (a *appRuntime) persistBypass(ctx context.Context, on bool) error {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	settings, err := configstore.LoadCoreSettings()
	if err != nil {
		return err
	}
	settings.BypassCN = on
	if a.core != nil {
		if mode := strings.TrimSpace(a.core.Status().Mode); mode != "" {
			settings.Mode = mode
		}
	}
	if err := configstore.SaveCoreSettings(settings); err != nil {
		return err
	}
	if a.core == nil || !a.core.Status().Running {
		return nil
	}
	subs, err := configstore.LoadSubscriptions()
	if err != nil {
		return err
	}
	return a.core.Reload(ctx, a.coreParams(settings), providerSpecs(subs))
}

func (a *appRuntime) callDownload(ctx context.Context, proxy string) error {
	fn := a.downloadGeo
	if fn == nil {
		fn = func(ctx context.Context, dir, proxyURL string, on core.ProgressFunc) error {
			return core.DownloadGeo(ctx, dir, proxyURL, nil, on)
		}
	}
	return fn(ctx, a.coreDir, proxy, func(n, tot int64) {
		a.broadcastCore("geo", "正在下载地理数据", n, tot)
	})
}

func (a *appRuntime) callPreflight(ctx context.Context, binary, configYAML string) error {
	if a.preflightGeo != nil {
		return a.preflightGeo(ctx, binary, a.coreDir, configYAML)
	}
	return core.PreflightGeo(ctx, binary, a.coreDir, configYAML)
}

func (a *appRuntime) cancelGeo() {
	if a == nil {
		return
	}
	g := a.geoCtl()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancel != nil {
		g.cancel()
		g.cancel = nil
	}
	g.gen++
	g.busy = false
	g.apply = false
}

func (a *appRuntime) finishGeo(gen int) {
	g := a.geoCtl()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.gen != gen {
		return
	}
	g.busy = false
	g.cancel = nil
}

func (a *appRuntime) geoCurrent(gen int) bool {
	g := a.geoCtl()
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.gen == gen
}

func (a *appRuntime) storeGeoError(gen int, msg string) {
	g := a.geoCtl()
	g.mu.Lock()
	defer g.mu.Unlock()
	if gen != 0 && g.gen != gen {
		return
	}
	g.errText = msg
}

func (a *appRuntime) clearGeoError() {
	a.storeGeoError(0, "")
}

func (a *appRuntime) clearGeoUntrusted(gen int) {
	g := a.geoCtl()
	g.mu.Lock()
	defer g.mu.Unlock()
	if gen != 0 && g.gen != gen {
		return
	}
	g.untrusted = false
	g.badStamp = geoFileStamp{}
}

func (a *appRuntime) markGeoUntrusted(gen int) {
	g := a.geoCtl()
	g.mu.Lock()
	defer g.mu.Unlock()
	if gen != 0 && g.gen != gen {
		return
	}
	removeGeoFiles(a.coreDir)
	g.untrusted = true
	g.badStamp = currentGeoStamp(a.coreDir)
}

func (a *appRuntime) failGeo(gen int, err error) {
	msg := publicErr(err)
	a.storeGeoError(gen, msg)
	if a.geoCurrent(gen) {
		a.broadcastCore("geo", msg, 0, 0)
	}
}

func subsHaveNodes() bool {
	subs, err := configstore.LoadSubscriptions()
	if err != nil {
		return false
	}
	for _, sub := range subs {
		if sub.Enabled && sub.NodeCount > 0 {
			return true
		}
	}
	return false
}

func removeGeoFiles(dir string) {
	for _, name := range []string{core.GeoIPFile, core.GeoSiteFile} {
		_ = os.Remove(filepath.Join(dir, name))
		_ = os.Remove(filepath.Join(dir, name+".tmp"))
	}
}

func currentGeoStamp(dir string) geoFileStamp {
	return geoFileStamp{
		ip:   oneGeoStamp(filepath.Join(dir, core.GeoIPFile)),
		site: oneGeoStamp(filepath.Join(dir, core.GeoSiteFile)),
	}
}

func oneGeoStamp(path string) geoOneStamp {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return geoOneStamp{}
	}
	return geoOneStamp{ok: true, size: info.Size(), mod: info.ModTime().UnixNano()}
}
