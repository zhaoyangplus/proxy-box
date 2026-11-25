package mihomo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
