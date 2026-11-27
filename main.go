package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"proxy-box/internal/configstore"
	"proxy-box/internal/configurator"
	"proxy-box/internal/detector"
	"proxy-box/internal/diagnostics"
	"proxy-box/internal/launcher"
	"proxy-box/internal/speedtest"
	"proxy-box/internal/sysproxy"
	"proxy-box/internal/tray"
	"proxy-box/internal/updater"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

//go:embed ui/*
var embeddedUI embed.FS

const appWindowTitle = "Proxy Box - 本地代理工作台"

// appVersion can be overridden at build time via: -ldflags "-X main.appVersion=v1.0.0"
var appVersion = "v1.0.0"

type sseEvent struct {
	Type       string                    `json:"type"`
	Reason     string                    `json:"reason,omitempty"`
	ShouldDiag bool                      `json:"shouldDiag"`
	Snapshot   *detector.SystemSnapshot  `json:"snapshot,omitempty"`
	Update     *updater.DownloadProgress `json:"update,omitempty"`
	Core       *coreEvent                `json:"core,omitempty"`
	Speedtest  *speedtest.Progress       `json:"speedtest,omitempty"`
}

type sseHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func newSSEHub() *sseHub {
	return &sseHub{clients: make(map[chan []byte]struct{})}
}

const sseClientBuffer = 32

func (h *sseHub) subscribe() chan []byte {
	ch := make(chan []byte, sseClientBuffer)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *sseHub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

func (h *sseHub) broadcast(ev sseEvent) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		enqueueSSE(ch, data)
	}
}

// enqueueSSE keeps the newest frame. A full buffer drops the oldest frame.
func enqueueSSE(ch chan []byte, data []byte) {
	select {
	case ch <- data:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- data:
	default:
	}
}

func main() {
	launchTarget := flag.String("launch", "", "Launch cursor, antigravity, or codex through the builtin proxy")
	selfTest := flag.Bool("self-test", false, "Run non-destructive self-test JSON output and exit")
	optimizeNow := flag.Bool("optimize", false, "Run optimal compliant node selection and print JSON result")
	showVersion := flag.Bool("version", false, "Print Proxy Box version and exit")
	restarted := flag.Bool("restarted", false, "Internal flag indicating process was restarted after self-update")
	flag.Parse()

	if *showVersion {
		fmt.Println("Proxy Box", appVersion)
		return
	}

	updater.CleanupOldBinary()

	if *selfTest {
		runSelfTest()
		return
	}

	if *optimizeNow {
		snap := detector.DetectAll()
		res, err := detector.OptimizeClientNode(snap.ActiveClient)
		if err != nil {
			fmt.Println("ERR:", err)
			os.Exit(1)
		}
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		return
	}

	if *launchTarget != "" {
		runQuickLaunch(*launchTarget)
		return
	}

	mutexName, _ := windows.UTF16PtrFromString("Local\\ProxyBoxSingleInstanceMutex_v1")
	var hMutex windows.Handle
	var err error
	maxAttempts := 1
	if *restarted {
		maxAttempts = 25
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		hMutex, err = windows.CreateMutex(nil, false, mutexName)
		if err != windows.ERROR_ALREADY_EXISTS {
			break
		}
		if hMutex != 0 {
			_ = windows.CloseHandle(hMutex)
			hMutex = 0
		}
		if attempt+1 < maxAttempts {
			time.Sleep(150 * time.Millisecond)
		}
	}
	if err == windows.ERROR_ALREADY_EXISTS {
		focusExistingWindow()
		return
	}

	var mutexOnce sync.Once
	releaseMutex := func() {
		mutexOnce.Do(func() {
			if hMutex != 0 {
				_ = windows.CloseHandle(hMutex)
				hMutex = 0
			}
		})
	}
	defer releaseMutex()
	_ = sysproxy.RecoverIfStale(false)

	appUpdater := updater.New(appVersion)
	hub := newSSEHub()
	rt := newAppRuntime(hub)
	sched := startSubScheduler(rt)
	stopRuntime := func() {
		sched.Stop()
		rt.Shutdown()
	}
	defer stopRuntime()

	dataPath := filepath.Join(os.Getenv("LOCALAPPDATA"), "ProxyBox", "WebView2Data")
	_ = os.MkdirAll(dataPath, 0755)
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  appWindowTitle,
			Width:  1180,
			Height: 780,
			Center: true,
		},
	})
	if w == nil {
		showMessageBox("缺少 WebView2 运行时", "未检测到 Microsoft Edge WebView2 Runtime，请安装后再运行。", 0x10)
		return
	}
	defer w.Destroy()

	var quitApp *quitSeq
	var trayMgr *tray.Manager
	trayMgr = tray.NewManager(func(menuID int, arg string) {
		if trayMenuCommand(menuID) {
			rt.handleTrayCommand(menuID, arg, trayMgr.NotifyBalloon)
			return
		}
		switch menuID {
		case tray.MenuShowWindow:
			if w != nil {
				w.Dispatch(func() {
					trayMgr.ShowMainWindow()
				})
			}
		case tray.MenuLaunchAntigrav:
			snap := detector.DetectAll()
			if err := quickSyncAndLaunch("antigravity", false); err != nil {
				trayMgr.NotifyBalloon("启动 Antigravity 失败", err.Error())
			} else {
				trayMgr.NotifyBalloon("已代理启动 Antigravity", fmt.Sprintf("已对接 %s (%s)", snap.ActiveClient.Name, snap.ActiveClient.ProxyURL))
			}
		case tray.MenuLaunchCursor:
			snap := detector.DetectAll()
			if err := quickSyncAndLaunch("cursor", false); err != nil {
				trayMgr.NotifyBalloon("启动 Cursor 失败", err.Error())
			} else {
				trayMgr.NotifyBalloon("已代理启动 Cursor", fmt.Sprintf("已禁用 HTTP/2 并对接 %s (%s)", snap.ActiveClient.Name, snap.ActiveClient.ProxyURL))
			}
		case tray.MenuApplyAll:
			rt.applyEnabledFromTray(trayMgr.NotifyBalloon)
		case tray.MenuRestoreAll:
			msg, err := rt.restoreAllManaged(context.Background())
			if err != nil {
				trayMgr.NotifyBalloon("还原失败", err.Error())
			} else {
				trayMgr.NotifyBalloon("已还原默认配置", msg)
			}
		case tray.MenuCheckUpdate:
			if w != nil {
				w.Dispatch(func() {
					trayMgr.ShowMainWindow()
				})
			}
			hub.broadcast(sseEvent{Type: "open_update"})
		case tray.MenuQuit:
			quitApp.run()
		}
	}, func() bool {
		cfg, _ := configstore.LoadConfig()
		return cfg.MinimizeToTray
	})

	trayMgr.SetMenu(func() []tray.MenuItem {
		return buildTrayMenu(rt.cachedTrayState())
	})
	trayMgr.SetShutdown(stopRuntime)
	quitApp = &quitSeq{
		shutdown: stopRuntime,
		cleanup:  trayMgr.Cleanup,
		release:  releaseMutex,
		win: func() uiQuitter {
			return w
		},
		exit:    os.Exit,
		timeout: 3 * time.Second,
	}

	srvURL, err := startLocalAPIServer(hub, rt, appUpdater, func() {
		stopRuntime()
		releaseMutex()
		trayMgr.Cleanup()
	}, func() {
		requestUITerminate(w, releaseMutex, os.Exit, 3*time.Second)
	})
	if err != nil {
		showMessageBox("Proxy Box 启动失败", "无法启动本地控制服务: "+err.Error(), 0x10)
		return
	}
	go rt.bootCore()
	trayMgr.Start("Proxy Box - 本地代理工作台")
	defer trayMgr.Cleanup()

	// Start real-time proxy config / subscription / node watcher
	go startLiveWatcher(hub, trayMgr, rt)

	w.Dispatch(func() {
		trayMgr.AttachMainWindow(uintptr(w.Window()))
	})

	w.Navigate(srvURL)
	w.Run()
}

func startLiveWatcher(hub *sseHub, trayMgr *tray.Manager, rt *appRuntime) {
	cfg, _ := configstore.LoadConfig()
	prevStamp := detector.FastConfigStamp()
	prevSnap := detector.DetectAll()
	lastOptimizeAt := time.Time{}

	// Startup self-heal: upgrade v1 Script.js -> v2 (fixes HK01 regex, lazy:false & CursorBox.exe rule)
	// and auto-optimize node if currently sitting on a restricted/dead node.
	go func() {
		time.Sleep(1200 * time.Millisecond)
		initSnap := detector.DetectAll()
		changed := false
		reason := ""

		if initSnap.AppConfig.IsAutoOptimizeEnabled() && initSnap.AppConfig.NodeSelectionMode != "manual" &&
			initSnap.ActiveClient.ControllerReachable && initSnap.ActiveClient.IsolatedGroup {
			if !initSnap.ActiveClient.ActiveNodeCompliant || initSnap.ActiveClient.ActiveNodeDelayMs == 0 {
				if opt, err := detector.OptimizeClientNode(initSnap.ActiveClient); err == nil {
					changed = true
					reason = opt.Message
				}
			}
		}

		if changed {
			fresh := detector.DetectAll()
			hub.broadcast(sseEvent{
				Type:       "sync",
				Reason:     reason,
				ShouldDiag: true,
				Snapshot:   &fresh,
			})
		}
	}()

	ticker := time.NewTicker(1200 * time.Millisecond)
	defer ticker.Stop()
	tickCount := 0

	for range ticker.C {
		tickCount++
		cfg, _ = configstore.LoadConfig()
		currStamp := detector.FastConfigStamp()
		stampChanged := currStamp != prevStamp

		// Run full DetectAll immediately on any config/subscription file change, or every ~3.6s for live node/process changes
		if !stampChanged && tickCount%3 != 0 {
			continue
		}

		curr := detector.DetectAll()
		prevClient := prevSnap.ActiveClient
		currClient := curr.ActiveClient

		clientOrPortChanged := currClient.ID != prevClient.ID || currClient.ProxyURL != prevClient.ProxyURL || currClient.DedicatedPortActive != prevClient.DedicatedPortActive
		profileChanged := currClient.ActiveProfileUID != prevClient.ActiveProfileUID ||
			currClient.ActiveProfileName != prevClient.ActiveProfileName ||
			(currClient.ConfigModUnix != 0 && currClient.ConfigModUnix != prevClient.ConfigModUnix)
		tunChanged := currClient.TunEnabled != prevClient.TunEnabled
		nodeChanged := currClient.ActiveNode != prevClient.ActiveNode
		extNodeChanged := currClient.ExternalNode != prevClient.ExternalNode
		reachChanged := currClient.PortReachable != prevClient.PortReachable

		var reasons []string
		shouldDiag := false

		// 1. Auto-sync IDE proxy ports when active client, mixed port, or dedicated isolated port changes
		if clientOrPortChanged && currClient.PortReachable && cfg.IsAutoSyncEnabled() {
			subs, subErr := configstore.LoadSubscriptions()
			_, reason, err := rt.syncLiveApps(cfg, currClient, subs, subErr)
			if err != nil {
				reasons = append(reasons, err.Error())
			} else if reason != "" {
				reasons = append(reasons, reason)
			}
			shouldDiag = true
		}

		// 2. Auto-optimize ONLY CursorBox's isolated 🤖 AI-IDE group when subscription changes or node becomes restricted
		//    Respects user's manual node selection unless subscription itself changed!
		needOptimize := false
		if profileChanged {
			reasons = append(reasons, fmt.Sprintf("检测到订阅/配置更新（%s）", currClient.ActiveProfileName))
			shouldDiag = true
			if cfg.IsAutoOptimizeEnabled() && currClient.ControllerReachable && currClient.IsolatedGroup {
				needOptimize = true
			}
		} else if cfg.IsAutoOptimizeEnabled() && cfg.NodeSelectionMode != "manual" &&
			currClient.ControllerReachable && currClient.IsolatedGroup &&
			(!currClient.ActiveNodeCompliant || (nodeChanged && currClient.ActiveNodeDelayMs == 0)) {
			if time.Since(lastOptimizeAt) > 15*time.Second {
				needOptimize = true
			}
		}

		if needOptimize && time.Since(lastOptimizeAt) > 6*time.Second {
			lastOptimizeAt = time.Now()
			if profileChanged {
				time.Sleep(900 * time.Millisecond)
			}
			if opt, err := detector.OptimizeClientNode(currClient); err == nil {
				reasons = append(reasons, opt.Message)
				shouldDiag = true
				curr = detector.DetectAll()
			}
		}

		if tunChanged && len(reasons) == 0 {
			if curr.ActiveClient.TunEnabled {
				reasons = append(reasons, fmt.Sprintf("%s 已切换为 TUN 模式", curr.ActiveClient.Name))
			} else {
				reasons = append(reasons, fmt.Sprintf("%s 已切换为免 TUN 模式", curr.ActiveClient.Name))
			}
		}

		if nodeChanged || reachChanged {
			shouldDiag = true
		}

		anythingChanged := stampChanged || clientOrPortChanged || profileChanged || tunChanged || nodeChanged || extNodeChanged || reachChanged
		if anythingChanged {
			prevStamp = detector.FastConfigStamp()
			prevSnap = curr

			reasonText := strings.Join(reasons, "；")
			hub.broadcast(sseEvent{
				Type:       "sync",
				Reason:     reasonText,
				ShouldDiag: shouldDiag,
				Snapshot:   &curr,
			})
		}
	}
}

func startLocalAPIServer(hub *sseHub, rt *appRuntime, appUpdater *updater.Updater, onBeforeRestart func(), onAfterRestart func()) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}

	uiSub, err := fs.Sub(embeddedUI, "ui")
	if err != nil {
		return "", err
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(uiSub)))
	if rt != nil {
		rt.register(mux)
		mux.HandleFunc("/api/tools/toggle", rt.handleToolsToggle)
		mux.HandleFunc("/api/one-click", rt.handleOneClick)
		mux.HandleFunc("/api/restore-all", rt.handleRestoreAll)
		mux.HandleFunc("/api/diagnostics", rt.handleDiagnostics)
	}

	mux.HandleFunc("/api/events", func(rw http.ResponseWriter, r *http.Request) {
		flusher, ok := rw.(http.Flusher)
		if !ok {
			http.Error(rw, "SSE unsupported", http.StatusInternalServerError)
			return
		}
		rw.Header().Set("Content-Type", "text/event-stream")
		rw.Header().Set("Cache-Control", "no-cache")
		rw.Header().Set("Connection", "keep-alive")

		ch := hub.subscribe()
		defer hub.unsubscribe(ch)

		ping := time.NewTicker(20 * time.Second)
		defer ping.Stop()

		_, _ = fmt.Fprintf(rw, ": connected\n\n")
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case payload := <-ch:
				_, _ = fmt.Fprintf(rw, "data: %s\n\n", payload)
				flusher.Flush()
			case <-ping.C:
				_, _ = fmt.Fprintf(rw, ": ping\n\n")
				flusher.Flush()
			}
		}
	})

	mux.HandleFunc("/api/snapshot", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, http.StatusOK, detector.DetectAll())
	})

	mux.HandleFunc("/api/optimize-node", func(rw http.ResponseWriter, r *http.Request) {
		snap := detector.DetectAll()
		res, err := detector.OptimizeClientNode(snap.ActiveClient)
		if err != nil {
			writeErr(rw, err)
			return
		}
		if err := configstore.UpdateConfig(func(cfg *configstore.AppConfig) error {
			cfg.NodeSelectionMode = "auto"
			cfg.ManualNodeName = ""
			return nil
		}); err != nil {
			writeErr(rw, err)
			return
		}

		fresh := detector.DetectAll()
		hub.broadcast(sseEvent{
			Type:       "sync",
			Reason:     res.Message,
			ShouldDiag: true,
			Snapshot:   &fresh,
		})
		writeJSON(rw, http.StatusOK, map[string]any{
			"ok":       true,
			"result":   res,
			"message":  res.Message,
			"snapshot": fresh,
		})
	})

	mux.HandleFunc("/api/launch", func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			TargetID         string `json:"targetId"`
			RestartIfRunning bool   `json:"restartIfRunning"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(rw, err)
			return
		}
		snap := detector.DetectAll()
		if err := quickSyncAndLaunch(req.TargetID, req.RestartIfRunning); err != nil {
			writeErr(rw, err)
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{
			"ok":      true,
			"message": fmt.Sprintf("已携带 %s (%s) 代理参数免 TUN 启动 %s", snap.ActiveClient.Name, snap.ActiveClient.ProxyURL, strings.ToUpper(req.TargetID[:1])+req.TargetID[1:]),
		})
	})

	mux.HandleFunc("/api/shortcut", func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			TargetID string `json:"targetId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		snap := detector.DetectAll()
		var exePath, title string
		for _, t := range snap.Targets {
			if t.ID == req.TargetID {
				exePath = t.ExePath
				break
			}
		}
		if req.TargetID == "cursor" {
			title = "Cursor (免TUN代理)"
		} else if req.TargetID == "antigravity" {
			title = "Antigravity (免TUN代理)"
		} else if req.TargetID == "codex" {
			title = "Codex (免TUN代理)"
		} else {
			writeErr(rw, fmt.Errorf("不支持的目标类型"))
			return
		}
		lnk, err := launcher.CreateProxyDesktopShortcut(req.TargetID, title, exePath)
		if err != nil {
			writeErr(rw, err)
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{
			"ok":      true,
			"message": "已在桌面生成快捷方式: " + filepath.Base(lnk),
		})
	})

	mux.HandleFunc("/api/open-backups", handleOpenBackups)
	mux.HandleFunc("/api/open-path", handleOpenPath)

	mux.HandleFunc("/api/browse-exe", func(rw http.ResponseWriter, r *http.Request) {
		p := openFileDialogExe()
		writeJSON(rw, http.StatusOK, map[string]any{"path": p})
	})

	mux.HandleFunc("/api/save-config", handleSaveConfig)

	mux.HandleFunc("/api/version", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, http.StatusOK, map[string]any{
			"version":     appVersion,
			"repoUrl":     appUpdater.RepoURL(),
			"releasesUrl": appUpdater.ReleasesURL(),
		})
	})

	mux.HandleFunc("/api/open-url", handleOpenURL)

	mux.HandleFunc("/api/update/check", func(rw http.ResponseWriter, r *http.Request) {
		snap := detector.DetectAll()
		proxyURL := ""
		if snap.ActiveClient.PortReachable {
			proxyURL = snap.ActiveClient.ProxyURL
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()

		res, err := appUpdater.CheckLatest(ctx, proxyURL)
		if err != nil {
			writeErr(rw, err)
			return
		}
		writeJSON(rw, http.StatusOK, res)
	})

	mux.HandleFunc("/api/update/status", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, http.StatusOK, appUpdater.GetProgress())
	})

	mux.HandleFunc("/api/update/apply", func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			DownloadURL   string `json:"downloadUrl"`
			TargetVersion string `json:"targetVersion"`
			AssetSize     int64  `json:"assetSize"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		snap := detector.DetectAll()
		proxyURL := ""
		if snap.ActiveClient.PortReachable {
			proxyURL = snap.ActiveClient.ProxyURL
		}

		downloadURL := strings.TrimSpace(req.DownloadURL)
		targetVer := strings.TrimSpace(req.TargetVersion)
		assetSize := req.AssetSize

		if downloadURL == "" {
			if last := appUpdater.LastCheck(); last != nil && last.DownloadURL != "" {
				downloadURL = last.DownloadURL
				targetVer = last.LatestVersion
				assetSize = last.AssetSize
			} else {
				ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
				res, err := appUpdater.CheckLatest(ctx, proxyURL)
				cancel()
				if err != nil {
					writeErr(rw, err)
					return
				}
				if !res.HasUpdate {
					writeErr(rw, fmt.Errorf("当前已是最新版本 (%s)", res.CurrentVersion))
					return
				}
				downloadURL = res.DownloadURL
				targetVer = res.LatestVersion
				assetSize = res.AssetSize
			}
		}

		if downloadURL == "" {
			writeErr(rw, fmt.Errorf("未找到可下载的 Windows 程序附件，请前往 GitHub Releases 手动下载"))
			return
		}

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer cancel()
			err := appUpdater.DownloadAndApply(
				ctx,
				downloadURL,
				targetVer,
				assetSize,
				proxyURL,
				func(p updater.DownloadProgress) {
					cp := p
					hub.broadcast(sseEvent{
						Type:   "update",
						Update: &cp,
					})
				},
				onBeforeRestart,
			)
			if err == nil && onAfterRestart != nil {
				onAfterRestart()
			}
		}()

		writeJSON(rw, http.StatusOK, map[string]any{
			"ok":      true,
			"message": "已开始下载新版本更新包...",
		})
	})

	go func() {
		_ = http.Serve(ln, mux)
	}()

	return "http://" + ln.Addr().String(), nil
}

var detectSnapshot = detector.DetectAll

var launchWithProxy = launcher.LaunchTargetWithProxy

func quickSyncAndLaunch(targetID string, restartIfRunning bool) error {
	snap := detectSnapshot()
	var exePath string
	for _, t := range snap.Targets {
		if t.ID == targetID {
			exePath = t.ExePath
			break
		}
	}
	port := snap.ActiveClient.DedicatedPort
	if port <= 0 {
		port = 27892
	}
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if snap.AppConfig.IsAppEnabled(targetID) {
		_, _ = configurator.ApplyTarget(targetID, proxyURL)
	}
	return launchWithProxy(targetID, exePath, proxyURL, restartIfRunning)
}

func runQuickLaunch(targetID string) {
	snap := detector.DetectAll()
	if !snap.ActiveClient.PortReachable {
		showMessageBox(
			"内置内核端口未就绪",
			fmt.Sprintf("未能连接到内置内核端口 (%s)，请先启动 Proxy Box 内核后重试。", snap.ActiveClient.ProxyURL),
			0x30,
		)
		return
	}
	if err := quickSyncAndLaunch(strings.ToLower(strings.TrimSpace(targetID)), false); err != nil {
		showMessageBox("启动失败", err.Error(), 0x10)
	}
}

func runSelfTest() {
	snap := detector.DetectAll()
	rep := diagnostics.RunDiagnostics(snap.ActiveClient.Name, snap.ActiveClient.ProxyHost, snap.ActiveClient.MixedPort)
	out, _ := json.MarshalIndent(map[string]any{
		"snapshot":    snap,
		"diagnostics": rep,
	}, "", "  ")
	fmt.Println(string(out))
}

var openAppOrPathFn = launcher.OpenAppOrPath
var openURLFn = launcher.OpenURL

func handleOpenBackups(rw http.ResponseWriter, r *http.Request) {
	dir, err := configstore.BaseDir()
	if err != nil {
		writeErr(rw, err)
		return
	}
	if err := openAppOrPathFn(dir); err != nil {
		writeUserErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true})
}

func handleOpenPath(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Path   string `json:"path"`
		Parent bool   `json:"parent"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	target := strings.TrimSpace(req.Path)
	if target == "" {
		writeUserErr(rw, fmt.Errorf("路径为空"))
		return
	}
	target = filepath.Clean(target)
	if req.Parent {
		target = filepath.Dir(target)
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		writeUserErr(rw, err)
		return
	}
	if err := openAppOrPathFn(target); err != nil {
		writeUserErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true})
}

func handleOpenURL(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(rw, err)
		return
	}
	if err := openURLFn(req.URL); err != nil {
		writeUserErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true})
}

func writeJSON(rw http.ResponseWriter, code int, v any) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	rw.WriteHeader(code)
	_ = json.NewEncoder(rw).Encode(v)
}

func writeErr(rw http.ResponseWriter, err error) {
	writeJSON(rw, http.StatusBadRequest, map[string]any{"error": err.Error()})
}

func focusExistingWindow() {
	user32 := windows.NewLazySystemDLL("user32.dll")
	findWindow := user32.NewProc("FindWindowW")
	showWindow := user32.NewProc("ShowWindow")
	setFg := user32.NewProc("SetForegroundWindow")

	titlePtr, _ := windows.UTF16PtrFromString(appWindowTitle)
	hwnd, _, _ := findWindow.Call(0, uintptr(unsafe.Pointer(titlePtr)))
	if hwnd != 0 {
		showWindow.Call(hwnd, 9)
		setFg.Call(hwnd)
	}
}

func showMessageBox(title, text string, style uintptr) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	msgBox := user32.NewProc("MessageBoxW")
	tPtr, _ := windows.UTF16PtrFromString(title)
	mPtr, _ := windows.UTF16PtrFromString(text)
	msgBox.Call(0, uintptr(unsafe.Pointer(mPtr)), uintptr(unsafe.Pointer(tPtr)), style)
}

type openFileNameW struct {
	LStructSize       uint32
	HwndOwner         uintptr
	HInstance         uintptr
	LpstrFilter       *uint16
	LpstrCustomFilter *uint16
	NMaxCustFilter    uint32
	NFilterIndex      uint32
	LpstrFile         *uint16
	NMaxFile          uint32
	LpstrFileTitle    *uint16
	NMaxFileTitle     uint32
	LpstrInitialDir   *uint16
	LpstrTitle        *uint16
	Flags             uint32
	NFileOffset       uint16
	NFileExtension    uint16
	LpstrDefExt       *uint16
	LCustData         uintptr
	LpfnHook          uintptr
	LpTemplateName    *uint16
	PvReserved        unsafe.Pointer
	DwReserved        uint32
	FlagsEx           uint32
}

func openFileDialogExe() string {
	return openFileDialog("选择应用程序可执行文件 (.exe)", fileDialogFilter("可执行程序 (*.exe)", "*.exe"))
}

func openFileDialogCore() string {
	return openFileDialog("选择 mihomo.exe 或 zip 压缩包", fileDialogFilter("内核文件 (*.exe;*.zip)", "*.exe;*.zip"))
}

func fileDialogFilter(desc, pattern string) []uint16 {
	return append(append(windows.StringToUTF16(desc), windows.StringToUTF16(pattern)...), 0)
}

func openFileDialog(titleText string, filter []uint16) string {
	comdlg32 := windows.NewLazySystemDLL("comdlg32.dll")
	getOpenFileName := comdlg32.NewProc("GetOpenFileNameW")

	buf := make([]uint16, windows.MAX_PATH*2)
	title, _ := windows.UTF16PtrFromString(titleText)
	ofn := openFileNameW{
		LStructSize: uint32(unsafe.Sizeof(openFileNameW{})),
		LpstrFilter: &filter[0],
		LpstrFile:   &buf[0],
		NMaxFile:    uint32(len(buf)),
		LpstrTitle:  title,
		Flags:       0x00080000 | 0x00001000 | 0x00000008,
	}
	ret, _, _ := getOpenFileName.Call(uintptr(unsafe.Pointer(&ofn)))
	if ret == 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}
