package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"proxy-box/internal/configstore"
	"proxy-box/internal/detector"
	"proxy-box/internal/launcher"
	"proxy-box/internal/updater"
)

func handleEvents(hub *sseHub) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
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
	}
}

func handleSnapshot(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, http.StatusOK, detector.DetectAll())
}

func handleOptimizeNode(hub *sseHub) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
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
	}
}

func handleLaunch(rw http.ResponseWriter, r *http.Request) {
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
}

func handleShortcut(rw http.ResponseWriter, r *http.Request) {
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
}

func handleBrowseExe(rw http.ResponseWriter, r *http.Request) {
	p := openFileDialogExe()
	writeJSON(rw, http.StatusOK, map[string]any{"path": p})
}

func handleVersion(appUpdater *updater.Updater) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, http.StatusOK, map[string]any{
			"version":     appVersion,
			"repoUrl":     appUpdater.RepoURL(),
			"releasesUrl": appUpdater.ReleasesURL(),
		})
	}
}
