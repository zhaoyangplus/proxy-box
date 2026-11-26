package mihomo

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestRestrictedAndPreferredNodeRegex(t *testing.T) {
	restricted := []string{
		"🇭🇰香港高速01|BGP|CMCU",
		"HK01",
		"HKTJ-Reality",
		"HK-BGP-02",
		"港01",
		"澳门CN2",
		"俄罗斯莫斯科",
		"剩余流量：999 GB",
		"套餐到期：2026-12-18",
	}
	for _, n := range restricted {
		if !IsRestrictedNodeName(n) {
			t.Errorf("expected %q to be restricted", n)
		}
	}

	allowed := []string{
		"🇯🇵日本高速03|BGP|CUCM",
		"JP01",
		"US01-AWS",
		"🇸🇬新加坡高速01",
		"SG-02",
		"🇬🇧英国伦敦01",
	}
	for _, n := range allowed {
		if IsRestrictedNodeName(n) {
			t.Errorf("expected %q to be allowed, not restricted", n)
		}
		if !IsPreferredNodeName(n) {
			t.Errorf("expected %q to be preferred", n)
		}
	}
}

func TestStrictIsolationOnlyTouchesAIIDEGroup(t *testing.T) {
	aiNow := AIAutoName
	extNow := "HK01" // External proxy stays on Hong Kong!

	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxies":
			_ = json.NewEncoder(rw).Encode(map[string]any{
				"proxies": map[string]any{
					"PROXY": map[string]any{
						"name": "PROXY",
						"type": "Selector",
						"now":  extNow,
						"all":  []string{"HK01", "US01", "JP02", "SG03"},
					},
					AIGroupName: map[string]any{
						"name": AIGroupName,
						"type": "Selector",
						"now":  aiNow,
						"all":  []string{AIAutoName, "US01", "JP02", "SG03"},
					},
					AIAutoName: map[string]any{
						"name": AIAutoName,
						"type": "URLTest",
						"now":  "US01",
						"all":  []string{"US01", "JP02", "SG03"},
					},
					"HK01": map[string]any{"name": "HK01", "type": "Vless"},
					"US01": map[string]any{"name": "US01", "type": "Vless"},
					"JP02": map[string]any{"name": "JP02", "type": "Vless"},
					"SG03": map[string]any{"name": "SG03", "type": "Vless"},
				},
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/group/"):
			_ = json.NewEncoder(rw).Encode(map[string]int{
				"US01": 180,
				"JP02": 72,
				"SG03": 98,
			})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/proxies/"):
			grp, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/proxies/"))
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if grp == AIGroupName {
				aiNow = body.Name
			} else if grp == "PROXY" {
				extNow = body.Name
			}
			rw.WriteHeader(http.StatusNoContent)
		default:
			rw.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	mc := NewClient("", srv.URL, "")

	// 1. Optimize CursorBox's isolated group -> should pick JP02 and NEVER alter external PROXY (HK01)
	res, err := mc.OptimizeBestNode()
	if err != nil {
		t.Fatalf("OptimizeBestNode failed: %v", err)
	}
	if res.SelectedNode != "JP02" {
		t.Fatalf("expected JP02 selected, got %+v", res)
	}
	if extNow != "HK01" {
		t.Fatalf("external PROXY group was modified! expected HK01, got %q", extNow)
	}

	// 2. Manual lock CursorBox node to SG03 -> external PROXY must STILL be HK01
	live, err := mc.SelectAINode("SG03")
	if err != nil {
		t.Fatalf("SelectAINode failed: %v", err)
	}
	if live.ResolvedNode != "SG03" || live.ExternalNode != "HK01" {
		t.Fatalf("expected isolated SG03 and untouched external HK01, got %+v", live)
	}
}

func TestListMergesProviderNodes(t *testing.T) {
	var delayHits int
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxies":
			_ = json.NewEncoder(rw).Encode(map[string]any{
				"proxies": map[string]any{
					"PROXY": map[string]any{
						"name": "PROXY", "type": "Selector", "now": "US01",
						"all": []string{"US01", "ss-dummy", "old-dead", "missing"},
					},
					"US01": map[string]any{
						"name": "US01", "type": "Vless", "alive": true,
						"history": []any{map[string]any{"delay": 80}},
					},
					"old-dead": map[string]any{
						"name": "old-dead", "type": "Vless", "alive": false,
						"history": []any{map[string]any{"delay": 0}},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/providers/proxies":
			_ = json.NewEncoder(rw).Encode(map[string]any{
				"providers": map[string]any{
					"default": map[string]any{
						"name": "default",
						"proxies": []any{
							map[string]any{"name": "US01", "type": "Vless", "alive": true, "history": []any{}},
						},
					},
					"SELECT": map[string]any{
						"name": "SELECT",
						"proxies": []any{
							map[string]any{"name": "US01", "type": "Vless", "alive": true, "history": []any{}},
						},
					},
					"uri-b64": map[string]any{
						"name": "uri-b64",
						"proxies": []any{
							map[string]any{
								"name": "ss-dummy", "type": "Shadowsocks", "alive": true,
								"provider-name": "uri-b64", "history": []any{},
							},
						},
					},
				},
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/group/"):
			delayHits++
			rw.WriteHeader(http.StatusInternalServerError)
		default:
			rw.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	got, err := NewClient("", srv.URL, "").ListGroupNodes("PROXY", false)
	if err != nil {
		t.Fatalf("ListGroupNodes: %v", err)
	}
	if delayHits != 0 {
		t.Fatalf("delay endpoint was called %d times", delayHits)
	}
	byName := map[string]NodeOption{}
	for _, n := range got.Nodes {
		byName[n.Name] = n
	}
	if len(byName) != 3 {
		t.Fatalf("nodes = %+v", got.Nodes)
	}
	if _, ok := byName["missing"]; ok {
		t.Fatal("missing member was listed")
	}
	ss := byName["ss-dummy"]
	if ss.Provider != "uri-b64" || !ss.Untested || ss.DelayMs != 0 || ss.Type != "Shadowsocks" {
		t.Fatalf("ss-dummy = %+v", ss)
	}
	us := byName["US01"]
	if us.Provider != "" || us.Untested || us.DelayMs != 80 {
		t.Fatalf("US01 = %+v", us)
	}
	dead := byName["old-dead"]
	if dead.Untested || dead.DelayMs != 0 {
		t.Fatalf("old-dead = %+v", dead)
	}
}

func TestListGroupNodesDelayAndAIAutoProbe(t *testing.T) {
	var mu sync.Mutex
	var delayGroups []string
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxies":
			_ = json.NewEncoder(rw).Encode(aiAndProxyPayload())
		case r.Method == http.MethodGet && r.URL.Path == "/providers/proxies":
			_ = json.NewEncoder(rw).Encode(uriProviderPayload())
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/group/") && strings.HasSuffix(r.URL.Path, "/delay"):
			name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/group/"), "/delay")
			if decoded, err := url.PathUnescape(name); err == nil {
				name = decoded
			}
			mu.Lock()
			delayGroups = append(delayGroups, name)
			mu.Unlock()
			if name == "PROXY" {
				_ = json.NewEncoder(rw).Encode(map[string]int{"US01": 100})
				return
			}
			_ = json.NewEncoder(rw).Encode(map[string]int{"ss-dummy": 55, "US01": 90})
		default:
			rw.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	mc := NewClient("", srv.URL, "")
	proxyList, err := mc.ListGroupNodes("PROXY", true)
	if err != nil {
		t.Fatalf("ListGroupNodes PROXY: %v", err)
	}
	byName := map[string]NodeOption{}
	for _, n := range proxyList.Nodes {
		byName[n.Name] = n
	}
	if byName["US01"].DelayMs != 100 || byName["US01"].Untested {
		t.Fatalf("US01 = %+v", byName["US01"])
	}
	ss := byName["ss-dummy"]
	if ss.Provider != "uri-b64" || ss.DelayMs != 0 || ss.Untested {
		t.Fatalf("timed-out provider node = %+v", ss)
	}
	if proxyList.CurrentSelect != "US01" {
		t.Fatalf("current select = %q", proxyList.CurrentSelect)
	}

	aiList, err := mc.ListAINodes(true)
	if err != nil {
		t.Fatalf("ListAINodes: %v", err)
	}
	found := false
	foundAuto := false
	for _, n := range aiList.Nodes {
		if n.Name == "ss-dummy" {
			found = true
			if n.Provider != "uri-b64" || n.DelayMs != 55 || n.Untested {
				t.Fatalf("AI ss-dummy = %+v", n)
			}
		}
		if n.Name == AIAutoName {
			foundAuto = true
			if !n.IsAuto || n.Type != "URLTest" || !n.Preferred || !n.Compliant || n.Provider != "" ||
				n.ResolvedTo != "US01" || n.DelayMs != 90 || n.Untested || !n.Selected || !n.Alive {
				t.Fatalf("AI-Auto = %+v", n)
			}
		}
	}
	if !found || !foundAuto {
		t.Fatalf("provider node or AI-Auto missing from AI list: %+v", aiList.Nodes)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(delayGroups) != 2 || delayGroups[0] != "PROXY" || delayGroups[1] != AIAutoName {
		t.Fatalf("delay groups = %#v", delayGroups)
	}
}

func TestGroupDelay504IsMeasuredTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxies":
			_ = json.NewEncoder(rw).Encode(map[string]any{
				"proxies": map[string]any{
					"DEAD": map[string]any{
						"name": "DEAD", "type": "Selector", "now": "dead-00",
						"all": []string{"⚡ AUTO", "dead-00", "dead-01"},
					},
					"⚡ AUTO": map[string]any{
						"name": "⚡ AUTO", "type": "URLTest", "now": "dead-00", "alive": true,
						"all": []string{"dead-00", "dead-01"}, "history": []any{},
					},
					"dead-00": map[string]any{"name": "dead-00", "type": "Shadowsocks", "alive": true, "history": []any{}},
					"dead-01": map[string]any{"name": "dead-01", "type": "Shadowsocks", "alive": true, "history": []any{}},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/providers/proxies":
			rw.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/group/DEAD/delay":
			rw.WriteHeader(http.StatusGatewayTimeout)
			_, _ = rw.Write([]byte(`{"message":"get delay: all proxies timeout"}`))
		default:
			rw.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	got, err := NewClient("", srv.URL, "").ListGroupNodes("DEAD", true)
	if err != nil {
		t.Fatalf("ListGroupNodes: %v", err)
	}
	if len(got.Nodes) != 3 {
		t.Fatalf("nodes = %+v", got.Nodes)
	}
	for _, n := range got.Nodes {
		if n.Alive || n.DelayMs != 0 || n.Untested {
			t.Fatalf("node after 504 = %+v", n)
		}
		if n.Name == "⚡ AUTO" && (!n.IsAuto || n.ResolvedTo != "dead-00") {
			t.Fatalf("auto group after 504 = %+v", n)
		}
	}
}

func TestListGroupNodesListsAutoGroups(t *testing.T) {
	const (
		autoName = "⚡ AUTO"
		fallName = "⚡ FALL"
	)
	var delayHits int
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxies":
			_ = json.NewEncoder(rw).Encode(map[string]any{
				"proxies": map[string]any{
					"PROXY": map[string]any{
						"name": "PROXY", "type": "Selector", "now": autoName,
						"all": []string{autoName, fallName, "US01", "DIRECT", "REJECT", "NEST"},
					},
					autoName: map[string]any{
						"name": autoName, "type": "URLTest", "now": "US01", "alive": true,
						"all": []string{"US01"}, "history": []any{},
					},
					fallName: map[string]any{
						"name": fallName, "type": "Fallback", "now": "JP02", "alive": true,
						"all": []string{"JP02"}, "history": []any{},
					},
					"US01": map[string]any{
						"name": "US01", "type": "Vless", "alive": true,
						"history": []any{map[string]any{"delay": 40}},
					},
					"JP02": map[string]any{
						"name": "JP02", "type": "Vless", "alive": true,
						"history": []any{map[string]any{"delay": 25}},
					},
					"DIRECT": map[string]any{"name": "DIRECT", "type": "Direct", "alive": true, "history": []any{}},
					"REJECT": map[string]any{"name": "REJECT", "type": "Reject", "alive": false, "history": []any{}},
					"NEST": map[string]any{
						"name": "NEST", "type": "Selector", "now": "US01",
						"all": []string{"US01"},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/providers/proxies":
			rw.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/group/PROXY/delay":
			delayHits++
			_ = json.NewEncoder(rw).Encode(map[string]int{"US01": 88, "JP02": 70})
		default:
			rw.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	mc := NewClient("", srv.URL, "")
	history, err := mc.ListGroupNodes("PROXY", false)
	if err != nil {
		t.Fatalf("ListGroupNodes history: %v", err)
	}
	if delayHits != 0 {
		t.Fatalf("delay endpoint was called %d times", delayHits)
	}
	byName := map[string]NodeOption{}
	for _, n := range history.Nodes {
		byName[n.Name] = n
	}
	if len(byName) != 5 {
		t.Fatalf("nodes = %+v", history.Nodes)
	}
	if _, ok := byName["NEST"]; ok {
		t.Fatal("select group was listed")
	}
	if _, ok := byName["JP02"]; ok {
		t.Fatal("leaf outside the group was listed")
	}
	auto := byName[autoName]
	if !auto.IsAuto || auto.Type != "URLTest" || auto.ResolvedTo != "US01" || auto.DelayMs != 40 || auto.Untested || !auto.Selected || !auto.Alive || auto.Provider != "" {
		t.Fatalf("url-test member = %+v", auto)
	}
	fall := byName[fallName]
	if !fall.IsAuto || fall.Type != "Fallback" || fall.ResolvedTo != "JP02" || fall.DelayMs != 25 || fall.Untested || fall.Selected || !fall.Alive {
		t.Fatalf("fallback member = %+v", fall)
	}
	if byName["DIRECT"].IsAuto || !byName["DIRECT"].Untested || !byName["DIRECT"].Alive {
		t.Fatalf("DIRECT = %+v", byName["DIRECT"])
	}
	if byName["REJECT"].IsAuto || !byName["REJECT"].Untested || byName["REJECT"].Alive {
		t.Fatalf("REJECT = %+v", byName["REJECT"])
	}

	probed, err := mc.ListGroupNodes("PROXY", true)
	if err != nil {
		t.Fatalf("ListGroupNodes probe: %v", err)
	}
	byName = map[string]NodeOption{}
	for _, n := range probed.Nodes {
		byName[n.Name] = n
	}
	auto = byName[autoName]
	if !auto.IsAuto || auto.ResolvedTo != "US01" || auto.DelayMs != 88 || auto.Untested || !auto.Alive {
		t.Fatalf("probed url-test member = %+v", auto)
	}
	fall = byName[fallName]
	if !fall.IsAuto || fall.ResolvedTo != "JP02" || fall.DelayMs != 70 || fall.Untested || !fall.Alive {
		t.Fatalf("probed fallback member = %+v", fall)
	}
	direct := byName["DIRECT"]
	if direct.IsAuto || direct.Untested || direct.Alive || direct.DelayMs != 0 {
		t.Fatalf("probed DIRECT = %+v", direct)
	}
	if delayHits != 1 {
		t.Fatalf("delay endpoint was called %d times", delayHits)
	}
}

func TestProviderHealthcheckFallback(t *testing.T) {
	cases := []struct {
		name      string
		hcStatus  int
		hcBody    string
		wantErr   bool
		wantDelay int
	}{
		{name: "ok", hcStatus: http.StatusOK, hcBody: `{"delay":42}`, wantDelay: 42},
		{name: "dead", hcStatus: http.StatusServiceUnavailable, hcBody: `{"message":"An error occurred in the delay test"}`, wantErr: true},
		{name: "missing", hcStatus: http.StatusNotFound, hcBody: `{"message":"Resource not found"}`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var hits []string
			srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				mu.Lock()
				hits = append(hits, r.URL.Path)
				mu.Unlock()
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/proxies":
					_ = json.NewEncoder(rw).Encode(providerOnlyAIPayload())
				case r.Method == http.MethodGet && r.URL.Path == "/providers/proxies":
					_ = json.NewEncoder(rw).Encode(uriProviderPayload())
				case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/group/"):
					rw.WriteHeader(http.StatusGatewayTimeout)
					_, _ = rw.Write([]byte(`{"message":"get delay: all proxies timeout"}`))
				case r.Method == http.MethodGet && r.URL.Path == "/proxies/US01/delay":
					rw.WriteHeader(http.StatusNotFound)
					_, _ = rw.Write([]byte(`{"message":"Resource not found"}`))
				case r.Method == http.MethodGet && r.URL.Path == "/providers/proxies/uri-b64/US01/healthcheck":
					if r.URL.Query().Get("url") != TestURL || r.URL.Query().Get("timeout") == "" {
						t.Errorf("healthcheck query = %s", r.URL.RawQuery)
					}
					rw.WriteHeader(tc.hcStatus)
					_, _ = io.WriteString(rw, tc.hcBody)
				case r.Method == http.MethodPut:
					rw.WriteHeader(http.StatusNoContent)
				default:
					rw.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()

			res, err := NewClient("", srv.URL, "").OptimizeBestNode()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", res)
				}
			} else if err != nil {
				t.Fatalf("OptimizeBestNode: %v", err)
			} else if res.SelectedNode != "US01" || res.DelayMs != tc.wantDelay {
				t.Fatalf("result = %+v", res)
			}
			mu.Lock()
			defer mu.Unlock()
			if !containsPath(hits, "/providers/proxies/uri-b64/US01/healthcheck") {
				t.Fatalf("healthcheck was not requested: %v", hits)
			}
		})
	}
}

func TestSingleNode404WithoutProviderDoesNotHealthcheck(t *testing.T) {
	var hits []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.URL.Path)
		mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxies":
			_ = json.NewEncoder(rw).Encode(map[string]any{
				"proxies": map[string]any{
					AIGroupName: map[string]any{
						"name": AIGroupName, "type": "Selector", "now": "US01",
						"all": []string{AIAutoName, "US01"},
					},
					AIAutoName: map[string]any{
						"name": AIAutoName, "type": "URLTest", "now": "US01",
						"all": []string{"US01"},
					},
					"US01": map[string]any{"name": "US01", "type": "Vless", "alive": true},
				},
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/group/"):
			rw.WriteHeader(http.StatusGatewayTimeout)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/healthcheck"):
			t.Errorf("unexpected healthcheck %s", r.URL.Path)
			rw.WriteHeader(http.StatusOK)
			_, _ = rw.Write([]byte(`{"delay":1}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/delay"):
			rw.WriteHeader(http.StatusNotFound)
		default:
			rw.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	if _, err := NewClient("", srv.URL, "").OptimizeBestNode(); err == nil {
		t.Fatal("expected optimize to fail when the only node 404s")
	}
	mu.Lock()
	defer mu.Unlock()
	if !containsPath(hits, "/proxies/US01/delay") {
		t.Fatalf("single delay was not requested: %v", hits)
	}
}

func TestSelectGroupNode(t *testing.T) {
	var mu sync.Mutex
	type put struct{ group, name string }
	var puts []put
	now := map[string]string{"PROXY": "HK01", AIGroupName: "US01", "GLOBAL": "DIRECT"}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxies":
			mu.Lock()
			body := map[string]any{
				"proxies": map[string]any{
					"PROXY": map[string]any{
						"name": "PROXY", "type": "Selector", "now": now["PROXY"],
						"all": []string{"HK01", "US01"},
					},
					AIGroupName: map[string]any{
						"name": AIGroupName, "type": "Selector", "now": now[AIGroupName],
						"all": []string{"US01"},
					},
					"GLOBAL": map[string]any{
						"name": "GLOBAL", "type": "Selector", "now": now["GLOBAL"],
						"all": []string{"DIRECT", "US01"},
					},
					"HK01":   map[string]any{"name": "HK01", "type": "Vless"},
					"US01":   map[string]any{"name": "US01", "type": "Vless"},
					"DIRECT": map[string]any{"name": "DIRECT", "type": "Direct"},
				},
			}
			mu.Unlock()
			_ = json.NewEncoder(rw).Encode(body)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/proxies/"):
			grp, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/proxies/"))
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			puts = append(puts, put{grp, body.Name})
			now[grp] = body.Name
			mu.Unlock()
			rw.WriteHeader(http.StatusNoContent)
		default:
			rw.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	mc := NewClient("", srv.URL, "")
	if err := mc.SelectGroupNode("PROXY", "US01"); err != nil {
		t.Fatalf("SelectGroupNode PROXY: %v", err)
	}
	if err := mc.SelectGroupNode("PROXY", "nope"); err == nil {
		t.Fatal("expected unknown node to fail")
	}
	if err := mc.SelectGroupNode("GLOBAL", "US01"); err != nil {
		t.Fatalf("SelectGroupNode GLOBAL: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(puts) != 2 || puts[0] != (put{"PROXY", "US01"}) || puts[1] != (put{"GLOBAL", "US01"}) {
		t.Fatalf("puts = %+v", puts)
	}
	if now[AIGroupName] != "US01" {
		t.Fatalf("AI group changed to %q", now[AIGroupName])
	}
}

func TestPatchModeAndReloadConfigPath(t *testing.T) {
	var mu sync.Mutex
	var patched string
	var reloaded string
	var force string
	var patchCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/configs":
			var body struct {
				Mode string `json:"mode"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			patched = body.Mode
			patchCalls++
			mu.Unlock()
			rw.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPut && r.URL.Path == "/configs":
			var body struct {
				Path string `json:"path"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			reloaded = body.Path
			force = r.URL.Query().Get("force")
			mu.Unlock()
			rw.WriteHeader(http.StatusNoContent)
		default:
			rw.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	mc := NewClient("", srv.URL, "")
	if err := mc.PatchMode("tun"); err == nil {
		t.Fatal("expected invalid mode to fail")
	}
	if err := mc.PatchMode("Global"); err != nil {
		t.Fatalf("PatchMode: %v", err)
	}
	const cfg = `D:\cfg\config.yaml`
	if err := mc.ReloadConfigPath(cfg); err != nil {
		t.Fatalf("ReloadConfigPath: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if patchCalls != 1 || patched != "global" {
		t.Fatalf("patched %q calls %d", patched, patchCalls)
	}
	if reloaded != cfg || force != "true" {
		t.Fatalf("reload path %q force %q", reloaded, force)
	}
}

func aiAndProxyPayload() map[string]any {
	return map[string]any{
		"proxies": map[string]any{
			"PROXY": map[string]any{
				"name": "PROXY", "type": "Selector", "now": "US01",
				"all": []string{"US01", "ss-dummy"},
			},
			AIGroupName: map[string]any{
				"name": AIGroupName, "type": "Selector", "now": AIAutoName,
				"all": []string{AIAutoName, "US01", "ss-dummy"},
			},
			AIAutoName: map[string]any{
				"name": AIAutoName, "type": "URLTest", "now": "US01",
				"all": []string{"US01", "ss-dummy"},
			},
			"US01": map[string]any{"name": "US01", "type": "Vless", "alive": true, "history": []any{}},
		},
	}
}

func uriProviderPayload() map[string]any {
	return map[string]any{
		"providers": map[string]any{
			"uri-b64": map[string]any{
				"name": "uri-b64",
				"proxies": []any{
					map[string]any{
						"name": "ss-dummy", "type": "Shadowsocks", "alive": true,
						"provider-name": "uri-b64", "history": []any{},
					},
					map[string]any{
						"name": "US01", "type": "Vless", "alive": true,
						"provider-name": "uri-b64", "history": []any{},
					},
				},
			},
		},
	}
}

func providerOnlyAIPayload() map[string]any {
	return map[string]any{
		"proxies": map[string]any{
			AIGroupName: map[string]any{
				"name": AIGroupName, "type": "Selector", "now": AIAutoName,
				"all": []string{AIAutoName, "US01"},
			},
			AIAutoName: map[string]any{
				"name": AIAutoName, "type": "URLTest", "now": "",
				"all": []string{"US01"},
			},
		},
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}
