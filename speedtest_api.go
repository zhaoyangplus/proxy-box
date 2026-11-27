package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"proxy-box/internal/configstore"
	"proxy-box/internal/core"
	"proxy-box/internal/detector"
	"proxy-box/internal/mihomo"
	"proxy-box/internal/speedtest"
)

type speedStartResponse struct {
	OK     bool   `json:"ok"`
	TaskID string `json:"taskId"`
	Total  int    `json:"total"`
}

type speedStatusResponse struct {
	Progress speedtest.Progress          `json:"progress"`
	Results  map[string]speedtest.Result `json:"results"`
}

func (a *appRuntime) registerSpeedTest(mux *http.ServeMux) {
	mux.HandleFunc("/api/speedtest/start", a.handleSpeedTestStart)
	mux.HandleFunc("/api/speedtest/stop", a.handleSpeedTestStop)
	mux.HandleFunc("/api/speedtest/status", a.handleSpeedTestStatus)
}

func (a *appRuntime) handleSpeedTestStart(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeUserErr(rw, errors.New("请使用 POST"))
		return
	}
	var req struct {
		Group string   `json:"group"`
		Nodes []string `json:"nodes"`
		MaxMB int      `json:"maxMB"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeUserErr(rw, err)
		return
	}
	proxyURL, err := a.speedProxyURL()
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	client, err := nodeClient(a.snapshot(), "builtin")
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	nodes, err := speedTargets(client, req.Group, req.Nodes)
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	cfg, _ := configstore.LoadConfig()
	maxMB := req.MaxMB
	if maxMB == 0 {
		maxMB = cfg.SpeedTestMaxMB
	}
	// Background: the request context ends when this handler returns.
	taskID, total, err := a.speedRunner().Start(context.Background(), speedtest.StartRequest{
		Nodes:       nodes,
		MaxMB:       maxMB,
		ProxyURL:    proxyURL,
		DownloadURL: strings.TrimSpace(cfg.SpeedTestURL),
		Selector:    mihomoSelector{client: client},
	})
	if err != nil {
		writeUserErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, speedStartResponse{OK: true, TaskID: taskID, Total: total})
}

func (a *appRuntime) handleSpeedTestStop(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeUserErr(rw, errors.New("请使用 POST"))
		return
	}
	a.speedRunner().Stop()
	writeJSON(rw, http.StatusOK, map[string]any{"ok": true})
}

func (a *appRuntime) handleSpeedTestStatus(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeUserErr(rw, errors.New("请使用 GET"))
		return
	}
	runner := a.speedRunner()
	results := runner.Cache()
	if results == nil {
		results = map[string]speedtest.Result{}
	}
	writeJSON(rw, http.StatusOK, speedStatusResponse{Progress: runner.Progress(), Results: results})
}

func (a *appRuntime) speedRunner() *speedtest.Runner {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.speed == nil {
		a.speed = speedtest.New()
	}
	a.speed.OnProgress(a.broadcastSpeed)
	return a.speed
}

func (a *appRuntime) broadcastSpeed(p speedtest.Progress) {
	if a == nil || a.hub == nil {
		return
	}
	cp := p
	a.hub.broadcast(sseEvent{Type: "speedtest", Speedtest: &cp})
}

func (a *appRuntime) speedCache() map[string]speedtest.Result {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	runner := a.speed
	a.mu.Unlock()
	if runner == nil {
		return nil
	}
	return runner.Cache()
}

func mergeSpeedCache(res *mihomo.NodeListResult, cache map[string]speedtest.Result) {
	if res == nil || len(cache) == 0 {
		return
	}
	for i := range res.Nodes {
		item, ok := cache[res.Nodes[i].Name]
		if !ok {
			continue
		}
		res.Nodes[i].SpeedMBps = item.SpeedMBps
		res.Nodes[i].SpeedTested = true
		res.Nodes[i].SpeedError = item.Error
	}
}

func (a *appRuntime) speedProxyURL() (string, error) {
	if a == nil || a.core == nil || !a.core.Status().Running {
		return "", errors.New("内核未运行")
	}
	port := a.core.Status().Ports.SpeedTest
	if port <= 0 {
		return "", errors.New("测速端口未就绪")
	}
	return "http://127.0.0.1:" + strconv.Itoa(port), nil
}

type mihomoSelector struct {
	client detector.ProxyClientInfo
}

func (s mihomoSelector) SelectGroupNode(group, node string) error {
	mc, err := groupClient(s.client)
	if err != nil {
		return err
	}
	return mc.SelectGroupNode(group, node)
}

func speedTargets(client detector.ProxyClientInfo, group string, names []string) ([]speedtest.Node, error) {
	group = strings.TrimSpace(group)
	if group == "" {
		group = core.ProxyGroupName
	}
	named := speedNames(names)
	listed, err := listClientGroup(client, group, false)
	if err != nil {
		return nil, err
	}
	if len(named) > 0 {
		return namedTargets(listed, named)
	}
	if everyRealUntested(listed.Nodes) {
		listed, err = listClientGroup(client, group, true)
		if err != nil {
			return nil, err
		}
	}
	nodes := aliveTargets(listed.Nodes)
	if len(nodes) == 0 {
		return nil, errors.New("没有存活节点")
	}
	return nodes, nil
}

func speedNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func namedTargets(listed *mihomo.NodeListResult, names []string) ([]speedtest.Node, error) {
	lookup := map[string]mihomo.NodeOption{}
	if listed != nil {
		for _, node := range listed.Nodes {
			lookup[node.Name] = node
		}
	}
	out := make([]speedtest.Node, 0, len(names))
	for _, name := range names {
		out = append(out, speedtest.Node{Name: name, IsAuto: lookup[name].IsAuto})
	}
	if len(out) == 0 {
		return nil, errors.New("请指定节点")
	}
	return out, nil
}

func aliveTargets(nodes []mihomo.NodeOption) []speedtest.Node {
	out := make([]speedtest.Node, 0, len(nodes))
	for _, node := range nodes {
		if skipListedNode(node) || node.DelayMs <= 0 {
			continue
		}
		out = append(out, speedtest.Node{Name: node.Name})
	}
	return out
}

func everyRealUntested(nodes []mihomo.NodeOption) bool {
	real := 0
	for _, node := range nodes {
		if skipListedNode(node) {
			continue
		}
		real++
		if !node.Untested {
			return false
		}
	}
	return real > 0
}

func skipListedNode(node mihomo.NodeOption) bool {
	if node.IsAuto {
		return true
	}
	switch strings.ToUpper(strings.TrimSpace(node.Name)) {
	case "DIRECT", "REJECT":
		return true
	default:
		return false
	}
}
