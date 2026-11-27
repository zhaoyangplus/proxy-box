package main

import (
	"context"
	"net/http"
	"time"

	"proxy-box/internal/detector"
	"proxy-box/internal/updater"
)

func registerUpdateRoutes(mux *http.ServeMux, hub *sseHub, appUpdater *updater.Updater, onBeforeRestart func(), onAfterRestart func()) {
	mux.HandleFunc("/api/update/check", func(rw http.ResponseWriter, r *http.Request) {
		proxyURL := updateProxyURL()
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
		proxyURL := updateProxyURL()
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		asset, err := appUpdater.PrepareDownload(ctx, proxyURL)
		cancel()
		if err != nil {
			writeErr(rw, err)
			return
		}

		go func() {
			dlCtx, dlCancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer dlCancel()
			err := appUpdater.DownloadAndApply(dlCtx, asset, proxyURL, func(p updater.DownloadProgress) {
				cp := p
				hub.broadcast(sseEvent{Type: "update", Update: &cp})
			}, onBeforeRestart)
			if err == nil && onAfterRestart != nil {
				onAfterRestart()
			}
		}()

		writeJSON(rw, http.StatusOK, map[string]any{
			"ok":      true,
			"message": "已开始下载新版本更新包...",
		})
	})
}

func updateProxyURL() string {
	snap := detector.DetectAll()
	if snap.ActiveClient.PortReachable {
		return snap.ActiveClient.ProxyURL
	}
	return ""
}
