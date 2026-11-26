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

	"proxy-box/internal/clashscript"
	"proxy-box/internal/configstore"
	"proxy-box/internal/configurator"
	"proxy-box/internal/detector"
	"proxy-box/internal/diagnostics"
	"proxy-box/internal/launcher"
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
}

type sseHub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func newSSEHub() *sseHub {
	return &sseHub{clients: make(map[chan []byte]struct{})}
}

func (h *sseHub) subscribe() chan []byte {
	ch := make(chan []byte, 8)
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
		select {
		case ch <- data:
		default:
			// Drop stale frame if slow consumer
		}
	}
}

func main() {
	launchTarget := flag.String("launch", "", "Quickly launch target IDE (cursor or antigravity) with live proxy")
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

	var w webview2.WebView
	var trayMgr *tray.Manager

	appUpdater := updater.New(appVersion)
	hub := newSSEHub()
	rt := newAppRuntime(hub)
	defer rt.Shutdown()
	srvURL, err := startLocalAPIServer(hub, appUpdater, func() {
		rt.Shutdown()
		releaseMutex()
		if trayMgr != nil {
			trayMgr.Cleanup()
		}
	}, func() {
		if w != nil {
			w.Dispatch(func() {
				w.Terminate()
			})
		}
		go func() {
			time.Sleep(1500 * time.Millisecond)
			os.Exit(0)
		}()
	})
	if err != nil {
		showMessageBox("Proxy Box 启动失败", "无法启动本地控制服务: "+err.Error(), 0x10)
		return
	}
	go rt.bootCore()

	dataPath := filepath.Join(os.Getenv("LOCALAPPDATA"), "ProxyBox", "WebView2Data")
	_ = os.MkdirAll(dataPath, 0755)

	trayMgr = tray.NewManager(func(menuID int) {
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
			snap := detector.DetectAll()
			_, err := configurator.ApplyAll(configurator.ApplyOptions{
				ProxyURL:    snap.ActiveClient.ProxyURL,
				Cursor:      true,
				ClaudeCode:  true,
				Antigravity: true,
				Codex:       true,
				SyncUserEnv: snap.AppConfig.SyncUserEnv,
			})
			if snap.ActiveClient.ID == "clash_verge" && snap.ActiveClient.ConfigDir != "" {
				_, _ = clashscript.Inject(snap.ActiveClient.ConfigDir, snap.ActiveClient.ControllerPipe, snap.ActiveClient.ExternalController, snap.ActiveClient.Secret)
			}
			if snap.ActiveClient.ControllerReachable {
				_, _ = detector.OptimizeClientNode(snap.ActiveClient)
			}
			if err != nil {
				trayMgr.NotifyBalloon("配置失败", err.Error())
			} else {
				trayMgr.NotifyBalloon("免 TUN 代理已生效", fmt.Sprintf("已同步 %s 端口 %s 并优选最优节点", snap.ActiveClient.Name, snap.ActiveClient.ProxyURL))
			}
		case tray.MenuRestoreAll:
			_, _ = configurator.RestoreAll()
			snap := detector.DetectAll()
			for _, c := range snap.Clients {
				if c.ID == "clash_verge" && c.ConfigDir != "" && c.RuleInjected {
					_ = clashscript.Remove(c.ConfigDir, c.ControllerPipe, c.ExternalController, c.Secret)
				}
			}
			trayMgr.NotifyBalloon("已还原默认配置", "Cursor / Claude Code / Antigravity 及扩展脚本已恢复")
		case tray.MenuCheckUpdate:
			if w != nil {
				w.Dispatch(func() {
					trayMgr.ShowMainWindow()
				})
			}
			hub.broadcast(sseEvent{Type: "open_update"})
		case tray.MenuQuit:
			rt.Shutdown()
			trayMgr.Cleanup()
			if w != nil {
				w.Terminate()
			}
		}
	}, func() bool {
		return configstore.LoadConfig().MinimizeToTray
	})

	trayMgr.SetShutdown(rt.Shutdown)
	trayMgr.Start("Proxy Box - 本地代理工作台")
	defer trayMgr.Cleanup()

	// Start real-time proxy config / subscription / node watcher
	go startLiveWatcher(hub, trayMgr)

	w = webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  appWindowTitle,
			Width:  1140,
			Height: 810,
			Center: true,
		},
	})
	if w == nil {
		showMessageBox("缺少 WebView2 运行时", "未检测到 Microsoft Edge WebView2 Runtime，请安装后再运行。", 0x10)
		return
	}
	defer w.Destroy()

	w.Dispatch(func() {
		trayMgr.AttachMainWindow(uintptr(w.Window()))
	})

	w.Navigate(srvURL)
	w.Run()
}

func startLiveWatcher(hub *sseHub, trayMgr *tray.Manager) {
	cfg := configstore.LoadConfig()
	prevStamp := detector.FastConfigStamp(cfg)
	prevSnap := detector.DetectAll()
	lastOptimizeAt := time.Time{}

	// Startup self-heal: upgrade v1 Script.js -> v2 (fixes HK01 regex, lazy:false & CursorBox.exe rule)
	// and auto-optimize node if currently sitting on a restricted/dead node.
	go func() {
		time.Sleep(1200 * time.Millisecond)
		initSnap := detector.DetectAll()
		changed := false
		reason := ""

		for _, c := range initSnap.Clients {
			if c.ID == "clash_verge" && c.ConfigDir != "" && clashscript.NeedsUpgrade(c.ConfigDir) {
				_, _ = clashscript.Inject(c.ConfigDir, c.ControllerPipe, c.ExternalController, c.Secret)
				changed = true
				reason = "已自动升级分流脚本至 v4（支持 :17897 独立隔离通道，刷新一次订阅即生效）"
			}
		}

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
		cfg = configstore.LoadConfig()
		currStamp := detector.FastConfigStamp(cfg)
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
			st := configstore.LoadState()
			hasManaged := false
			for _, tb := range st.Targets {
				if tb.Managed {
					hasManaged = true
					break
				}
			}
			subs, subErr := configstore.LoadSubscriptions()
			if currClient.ID == "builtin" && (subErr != nil || !detector.IDESyncAllowed(currClient, subs)) {
				reasons = append(reasons, "内置内核还没有可用订阅或节点，未改写 IDE 配置")
			} else if hasManaged {
				_, _ = configurator.ApplyAll(configurator.ApplyOptions{
					ProxyURL:    currClient.ProxyURL,
					Cursor:      true,
					ClaudeCode:  true,
					Antigravity: true,
					Codex:       true,
					SyncUserEnv: cfg.SyncUserEnv,
				})
				if currClient.DedicatedPortActive {
					reasons = append(reasons, fmt.Sprintf("独立隔离通道 %s 已上线，已自动同步全部 AI 工具（与外部代理节点双向隔离）", currClient.ProxyURL))
				} else {
					reasons = append(reasons, fmt.Sprintf("检测到代理切换至 %s (%s)，已自动同步 IDE 端口", currClient.Name, currClient.ProxyURL))
				}
			} else {
				reasons = append(reasons, fmt.Sprintf("检测到代理源变更为 %s (%s)", currClient.Name, currClient.ProxyURL))
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
			prevStamp = detector.FastConfigStamp(cfg)
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

func startLocalAPIServer(hub *sseHub, appUpdater *updater.Updater, onBeforeRestart func(), onAfterRestart func()) (string, error) {
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
		cfg := configstore.LoadConfig()
		cfg.NodeSelectionMode = "auto"
		cfg.ManualNodeName = ""
		_ = configstore.SaveConfig(cfg)

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

	mux.HandleFunc("/api/nodes/list", func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			TestDelay bool `json:"testDelay"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		snap := detector.DetectAll()
		res, err := detector.ListClientNodes(snap.ActiveClient, req.TestDelay)
		if err != nil {
			writeErr(rw, err)
			return
		}
		writeJSON(rw, http.StatusOK, res)
	})

	mux.HandleFunc("/api/nodes/select", func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			NodeName string `json:"nodeName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(rw, err)
			return
		}
		snap := detector.DetectAll()
		live, err := detector.SelectClientNode(snap.ActiveClient, req.NodeName)
		if err != nil {
			writeErr(rw, err)
			return
		}
		cfg := configstore.LoadConfig()
		if req.NodeName == clashscript.AIAutoName {
			cfg.NodeSelectionMode = "auto"
			cfg.ManualNodeName = ""
		} else {
			cfg.NodeSelectionMode = "manual"
			cfg.ManualNodeName = req.NodeName
		}
		_ = configstore.SaveConfig(cfg)

		fresh := detector.DetectAll()
		msg := fmt.Sprintf("Proxy Box 专属通道已锁定节点：%s（外部代理软件节点不受影响）", live.ResolvedNode)
		if req.NodeName == clashscript.AIAutoName {
			msg = fmt.Sprintf("Proxy Box 专属通道已切回自动优选：%s", live.ResolvedNode)
		}
		hub.broadcast(sseEvent{
			Type:       "sync",
			Reason:     msg,
			ShouldDiag: true,
			Snapshot:   &fresh,
		})
		writeJSON(rw, http.StatusOK, map[string]any{
			"ok":       true,
			"message":  msg,
			"live":     live,
			"snapshot": fresh,
		})
	})

	mux.HandleFunc("/api/select-client", func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			ClientID string `json:"clientId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(rw, err)
			return
		}
		cfg := configstore.LoadConfig()
		cfg.PreferredClient = strings.TrimSpace(req.ClientID)
		if cfg.PreferredClient == "" {
			cfg.PreferredClient = "auto"
		}
		if err := configstore.SaveConfig(cfg); err != nil {
			writeErr(rw, err)
			return
		}
		writeJSON(rw, http.StatusOK, detector.DetectAll())
	})

	mux.HandleFunc("/api/one-click", func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			ProxyURL    string `json:"proxyUrl"`
			SyncUserEnv bool   `json:"syncUserEnv"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		snap := detector.DetectAll()
		active := snap.ActiveClient
		proxyURL := req.ProxyURL
		if proxyURL == "" {
			proxyURL = active.ProxyURL
		}

		_, err := configurator.ApplyAll(configurator.ApplyOptions{
			ProxyURL:    proxyURL,
			Cursor:      true,
			ClaudeCode:  true,
			Antigravity: true,
			Codex:       true,
			SyncUserEnv: req.SyncUserEnv,
		})
		if err != nil {
			writeErr(rw, err)
			return
		}

		msg := fmt.Sprintf("已按 %s (%s) 配置全部工具", active.Name, proxyURL)
		switch active.ID {
		case "clash_verge":
			if active.ConfigDir != "" {
				reloaded, sErr := clashscript.Inject(active.ConfigDir, active.ControllerPipe, active.ExternalController, active.Secret)
				if sErr == nil {
					if reloaded {
						msg += "，已热重载 AI 分流脚本"
					} else {
						msg += "，已写入 AI 分流脚本"
					}
				}
			}
			if active.ControllerReachable {
				if opt, oErr := detector.OptimizeClientNode(active); oErr == nil {
					msg += fmt.Sprintf("，已优选节点 %s (%d ms)", opt.SelectedNode, opt.DelayMs)
				}
			}
		case "clash_mi":
			if _, _, cErr := clashscript.ExportClashMiTemplates(); cErr == nil {
				_ = setWindowsClipboard(clashscript.GenerateClashMiScript())
				msg += "，已复制 Clash Mi 防香港覆写脚本"
			}
			if active.ControllerReachable {
				if opt, oErr := detector.OptimizeClientNode(active); oErr == nil {
					msg += fmt.Sprintf("，已优选节点 %s (%d ms)", opt.SelectedNode, opt.DelayMs)
				}
			}
		case "v2rayn":
			if rulesJSON, _, vErr := clashscript.GenerateV2rayNRulesJSON(); vErr == nil {
				_ = setWindowsClipboard(rulesJSON)
				msg += "，已复制 v2rayN 路由规则到剪贴板"
			}
		}

		writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "message": msg})
	})

	mux.HandleFunc("/api/apply", func(rw http.ResponseWriter, r *http.Request) {
		var opts configurator.ApplyOptions
		if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
			writeErr(rw, err)
			return
		}
		summary, err := configurator.ApplyAll(opts)
		if err != nil {
			writeErr(rw, err)
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "summary": summary})
	})

	mux.HandleFunc("/api/restore-all", func(rw http.ResponseWriter, r *http.Request) {
		_, err := configurator.RestoreAll()
		if err != nil {
			writeErr(rw, err)
			return
		}
		snap := detector.DetectAll()
		for _, c := range snap.Clients {
			if c.ID == "clash_verge" && c.ConfigDir != "" && c.RuleInjected {
				_ = clashscript.Remove(c.ConfigDir, c.ControllerPipe, c.ExternalController, c.Secret)
			}
		}
		writeJSON(rw, http.StatusOK, map[string]any{
			"ok":      true,
			"message": "已无损还原 Cursor / Claude Code / Antigravity 配置与扩展脚本",
		})
	})

	mux.HandleFunc("/api/script/inject", func(rw http.ResponseWriter, r *http.Request) {
		snap := detector.DetectAll()
		var verge detector.ProxyClientInfo
		for _, c := range snap.Clients {
			if c.ID == "clash_verge" {
				verge = c
				break
			}
		}
		reloaded, err := clashscript.Inject(verge.ConfigDir, verge.ControllerPipe, verge.ExternalController, verge.Secret)
		if err != nil {
			writeErr(rw, err)
			return
		}
		msg := "已注入 v2 AI 分流脚本！请在 Clash Verge 订阅页点击刷新图标使新规则生效"
		if reloaded {
			msg = "已注入 v2 AI 分流脚本并已自动热重载 Mihomo 内核！"
		}
		if verge.ControllerReachable {
			if opt, oErr := detector.OptimizeClientNode(verge); oErr == nil {
				msg += fmt.Sprintf("（当前最优节点：%s · %d ms）", opt.SelectedNode, opt.DelayMs)
			}
		}
		writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "message": msg})
	})

	mux.HandleFunc("/api/script/remove", func(rw http.ResponseWriter, r *http.Request) {
		snap := detector.DetectAll()
		var verge detector.ProxyClientInfo
		for _, c := range snap.Clients {
			if c.ID == "clash_verge" {
				verge = c
				break
			}
		}
		if err := clashscript.Remove(verge.ConfigDir, verge.ControllerPipe, verge.ExternalController, verge.Secret); err != nil {
			writeErr(rw, err)
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"ok": true, "message": "已移除 AI 分流脚本并恢复原始 Script.js"})
	})

	mux.HandleFunc("/api/clashmi/export", func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			Format string `json:"format"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		jsPath, yamlPath, err := clashscript.ExportClashMiTemplates()
		if err != nil {
			writeErr(rw, err)
			return
		}
		if strings.ToLower(req.Format) == "yaml" {
			_ = setWindowsClipboard(clashscript.GenerateClashMiYAMLOverride())
			writeJSON(rw, http.StatusOK, map[string]any{
				"ok":      true,
				"path":    yamlPath,
				"message": "已复制 Clash Mi YAML 覆写规则到剪贴板（模板已存至 ~/.proxy-box/）",
			})
			return
		}
		_ = setWindowsClipboard(clashscript.GenerateClashMiScript())
		writeJSON(rw, http.StatusOK, map[string]any{
			"ok":      true,
			"path":    jsPath,
			"message": "已复制 Clash Mi 防香港 JS 覆写脚本到剪贴板！在 Clash Mi 粘贴添加覆写即可生效",
		})
	})

	mux.HandleFunc("/api/v2rayn/export", func(rw http.ResponseWriter, r *http.Request) {
		rulesJSON, savedPath, err := clashscript.GenerateV2rayNRulesJSON()
		if err != nil {
			writeErr(rw, err)
			return
		}
		if err := setWindowsClipboard(rulesJSON); err != nil {
			writeErr(rw, fmt.Errorf("写入系统剪贴板失败: %w", err))
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{
			"ok":      true,
			"path":    savedPath,
			"message": "已复制 v2rayN 路由规则 JSON！打开 v2rayN「设置 -> 路由设置 -> 双击规则集 -> 从剪贴板导入规则」即可",
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

	mux.HandleFunc("/api/diagnostics", func(rw http.ResponseWriter, r *http.Request) {
		snap := detector.DetectAll()
		rep := diagnostics.RunDiagnostics(snap.ActiveClient.Name, snap.ActiveClient.ProxyHost, snap.ActiveClient.MixedPort)
		writeJSON(rw, http.StatusOK, rep)
	})

	mux.HandleFunc("/api/open-clash", func(rw http.ResponseWriter, r *http.Request) {
		snap := detector.DetectAll()
		active := snap.ActiveClient
		if active.ExePath != "" {
			if err := launcher.OpenAppOrPath(active.ExePath); err != nil {
				writeErr(rw, err)
				return
			}
			writeJSON(rw, http.StatusOK, map[string]any{"ok": true})
			return
		}
		if active.ConfigDir != "" {
			_ = launcher.OpenAppOrPath(active.ConfigDir)
			writeJSON(rw, http.StatusOK, map[string]any{"ok": true})
			return
		}
		writeErr(rw, fmt.Errorf("未找到 %s 程序路径，可在右上角设置中指定", active.Name))
	})

	mux.HandleFunc("/api/open-backups", func(rw http.ResponseWriter, r *http.Request) {
		dir, err := configstore.BaseDir()
		if err != nil {
			writeErr(rw, err)
			return
		}
		_ = launcher.OpenAppOrPath(dir)
		writeJSON(rw, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/open-path", func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			Path   string `json:"path"`
			Parent bool   `json:"parent"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		target := req.Path
		if req.Parent {
			target = filepath.Dir(target)
		}
		_ = os.MkdirAll(target, 0755)
		if err := launcher.OpenAppOrPath(target); err != nil {
			writeErr(rw, err)
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"ok": true})
	})

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

	mux.HandleFunc("/api/open-url", func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(rw, err)
			return
		}
		if err := launcher.OpenURL(req.URL); err != nil {
			writeErr(rw, err)
			return
		}
		writeJSON(rw, http.StatusOK, map[string]any{"ok": true})
	})

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

func quickSyncAndLaunch(targetID string, restartIfRunning bool) error {
	snap := detector.DetectAll()
	var exePath string
	for _, t := range snap.Targets {
		if t.ID == targetID {
			exePath = t.ExePath
			break
		}
	}
	_, _ = configurator.ApplyAll(configurator.ApplyOptions{
		ProxyURL:    snap.ActiveClient.ProxyURL,
		Cursor:      targetID == "cursor",
		Antigravity: targetID == "antigravity",
		ClaudeCode:  targetID == "cursor",
		Codex:       targetID == "codex" || targetID == "cursor",
	})
	return launcher.LaunchTargetWithProxy(targetID, exePath, snap.ActiveClient.ProxyURL, restartIfRunning)
}

func runQuickLaunch(targetID string) {
	snap := detector.DetectAll()
	if !snap.ActiveClient.PortReachable {
		showMessageBox(
			"代理客户端端口未就绪",
			fmt.Sprintf("未能连接到 %s 本地端口 (%s)，请先启动代理客户端（Clash Verge / Clash Mi / v2rayN）后重试。", snap.ActiveClient.Name, snap.ActiveClient.ProxyURL),
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

func setWindowsClipboard(text string) error {
	user32 := windows.NewLazySystemDLL("user32.dll")
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")

	openClip := user32.NewProc("OpenClipboard")
	closeClip := user32.NewProc("CloseClipboard")
	emptyClip := user32.NewProc("EmptyClipboard")
	setClipData := user32.NewProc("SetClipboardData")

	globalAlloc := kernel32.NewProc("GlobalAlloc")
	globalLock := kernel32.NewProc("GlobalLock")
	globalUnlock := kernel32.NewProc("GlobalUnlock")

	utf16, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}
	byteSize := uintptr(len(utf16) * 2)

	const gmemMoveable = 0x0002
	const cfUnicodeText = 13

	hMem, _, err := globalAlloc.Call(gmemMoveable, byteSize)
	if hMem == 0 {
		return fmt.Errorf("GlobalAlloc failed: %v", err)
	}
	ptr, _, _ := globalLock.Call(hMem)
	if ptr == 0 {
		return fmt.Errorf("GlobalLock failed")
	}
	rtlMoveMemory := kernel32.NewProc("RtlMoveMemory")
	_, _, _ = rtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(&utf16[0])), byteSize)
	globalUnlock.Call(hMem)

	r, _, err := openClip.Call(0)
	if r == 0 {
		return fmt.Errorf("OpenClipboard failed: %v", err)
	}
	defer closeClip.Call()

	emptyClip.Call()
	res, _, err := setClipData.Call(cfUnicodeText, hMem)
	if res == 0 {
		return fmt.Errorf("SetClipboardData failed: %v", err)
	}
	return nil
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
	comdlg32 := windows.NewLazySystemDLL("comdlg32.dll")
	getOpenFileName := comdlg32.NewProc("GetOpenFileNameW")

	buf := make([]uint16, windows.MAX_PATH*2)
	filter := append(windows.StringToUTF16("可执行程序 (*.exe)"), 0)
	filter = append(filter, windows.StringToUTF16("*.exe")...)
	filter = append(filter, 0, 0)
	title, _ := windows.UTF16PtrFromString("选择应用程序可执行文件 (.exe)")

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
