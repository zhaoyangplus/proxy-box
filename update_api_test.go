package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"proxy-box/internal/updater"
)

type updateRoundTrip func(*http.Request) (*http.Response, error)

func (f updateRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdateApplyIgnoresRequestURL(t *testing.T) {
	const assetURL = "https://github.com/zhaoyangplus/proxy-box/releases/download/v2.4.0/ProxyBox.exe"
	const digest = "sha256:abababababababababababababababababababababababababababababababab"
	var mu sync.Mutex
	var urls []string
	sawAsset := make(chan string, 1)
	u := updater.New("v1.0.0")
	u.Transport = updateRoundTrip(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		urls = append(urls, r.URL.String())
		mu.Unlock()
		if strings.Contains(r.URL.Path, "/releases/latest") {
			body := `{"tag_name":"v2.4.0","assets":[{"name":"ProxyBox.exe","size":80,"browser_download_url":"` + assetURL + `","digest":"` + digest + `"}]}`
			return &http.Response{
				StatusCode:    http.StatusOK,
				Status:        "200 OK",
				Header:        make(http.Header),
				Body:          io.NopCloser(strings.NewReader(body)),
				ContentLength: int64(len(body)),
				Request:       r,
			}, nil
		}
		sawAsset <- r.URL.String()
		return &http.Response{
			StatusCode:    http.StatusInternalServerError,
			Status:        "500",
			Header:        make(http.Header),
			Body:          io.NopCloser(strings.NewReader("no")),
			ContentLength: 2,
			Request:       r,
		}, nil
	})

	mux := http.NewServeMux()
	registerUpdateRoutes(mux, newSSEHub(), u, nil, nil)
	body := `{"owner":"evil","repo":"evil","downloadUrl":"https://evil.example/p.exe","targetVersion":"v9.9.9","assetSize":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/update/apply", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}

	var downloaded string
	select {
	case downloaded = <-sawAsset:
	case <-time.After(5 * time.Second):
		t.Fatal("apply did not request the checked asset")
	}
	if downloaded != assetURL {
		t.Fatalf("downloaded %s", downloaded)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, raw := range urls {
		if strings.Contains(raw, "evil.example") || strings.Contains(raw, "/repos/evil/") {
			t.Fatalf("request used the body: %s", raw)
		}
	}
	joined := strings.Join(urls, "\n")
	if !strings.Contains(joined, "/repos/zhaoyangplus/proxy-box/releases/latest") {
		t.Fatalf("check URL %s", joined)
	}
}
