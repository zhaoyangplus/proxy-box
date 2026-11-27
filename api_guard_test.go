package main

import (
	"bytes"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewSessionToken(t *testing.T) {
	a, err := newSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := newSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two launches produced the same token")
	}
	raw, err := hex.DecodeString(a)
	if err != nil || len(raw) != 32 {
		t.Fatalf("token %q is not 32 bytes", a)
	}
}

func TestAPIGuard(t *testing.T) {
	token := hex.EncodeToString(bytes.Repeat([]byte{0x11}, 32))
	const host = "127.0.0.1:12345"
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "INDEX_MARKER")
	})
	mux.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "STATIC_MARKER")
	})
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"version":"ok"}`)
	})
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: ok\n\n")
	})
	mux.HandleFunc("/api/launch", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/speedtest/stop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/subs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	g := newAPIGuard(token, host)
	h := g.wrap(mux)
	origin := "http://" + host

	t.Run("no cookie", func(t *testing.T) {
		rec := serveGuard(h, guardRequest(http.MethodGet, "http://"+host+"/api/version", host, "", ""))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"error"`) || strings.Contains(rec.Body.String(), "INDEX_MARKER") {
			t.Fatalf("body %s", rec.Body.String())
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		rec := serveGuard(h, guardRequest(http.MethodGet, "http://"+host+"/?t=00", host, "", ""))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", rec.Code)
		}
		if rec.Header().Get("Set-Cookie") != "" {
			t.Fatal("wrong token set a cookie")
		}
	})

	t.Run("bootstrap", func(t *testing.T) {
		req := guardRequest(http.MethodGet, "http://"+host+"/?t="+token, host, "", "stale-token")
		rec := serveGuard(h, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		if loc := rec.Header().Get("Location"); loc != "/" {
			t.Fatalf("location %q", loc)
		}
		if strings.Contains(rec.Body.String(), "INDEX_MARKER") {
			t.Fatal("bootstrap served the file")
		}
		raw := rec.Header().Get("Set-Cookie")
		if !strings.Contains(raw, sessionCookieName+"="+token) {
			t.Fatalf("cookie %q", raw)
		}
		for _, part := range []string{"HttpOnly", "SameSite=Strict", "Path=/"} {
			if !strings.Contains(raw, part) {
				t.Fatalf("cookie %q missing %s", raw, part)
			}
		}
		for _, banned := range []string{"Domain=", "Secure", "Max-Age", "Expires="} {
			if strings.Contains(raw, banned) {
				t.Fatalf("cookie %q contains %s", raw, banned)
			}
		}
		parsed := rec.Result().Cookies()
		if len(parsed) != 1 || parsed[0].Domain != "" || !parsed[0].HttpOnly || parsed[0].SameSite != http.SameSiteStrictMode || parsed[0].Path != "/" {
			t.Fatalf("parsed cookie %+v", parsed)
		}
	})

	t.Run("cookie allows api static and sse", func(t *testing.T) {
		api := serveGuard(h, guardRequest(http.MethodGet, "http://"+host+"/api/version", host, "", token))
		if api.Code != http.StatusOK || !strings.Contains(api.Body.String(), `"version":"ok"`) {
			t.Fatalf("api %d %s", api.Code, api.Body.String())
		}
		file := serveGuard(h, guardRequest(http.MethodGet, "http://"+host+"/app.js", host, "", token))
		if file.Code != http.StatusOK || file.Body.String() != "STATIC_MARKER" {
			t.Fatalf("static %d %s", file.Code, file.Body.String())
		}
		sse := serveGuard(h, guardRequest(http.MethodGet, "http://"+host+"/api/events", host, "", token))
		if sse.Code != http.StatusOK || !strings.Contains(sse.Body.String(), "data: ok") {
			t.Fatalf("sse %d %s", sse.Code, sse.Body.String())
		}
	})

	t.Run("query token is not an api credential", func(t *testing.T) {
		rec := serveGuard(h, guardRequest(http.MethodGet, "http://"+host+"/api/version?t="+token, host, "", ""))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", rec.Code)
		}
	})

	t.Run("wrong host", func(t *testing.T) {
		req := guardRequest(http.MethodGet, "http://"+host+"/api/version", "evil.example", "", token)
		rec := serveGuard(h, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status %d", rec.Code)
		}
	})

	t.Run("foreign origin", func(t *testing.T) {
		req := guardRequest(http.MethodGet, "http://"+host+"/api/version", host, "", token)
		req.Header.Set("Origin", "http://evil.example")
		rec := serveGuard(h, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status %d", rec.Code)
		}
	})

	t.Run("matching origin", func(t *testing.T) {
		req := guardRequest(http.MethodGet, "http://"+host+"/api/version", host, "", token)
		req.Header.Set("Origin", origin)
		rec := serveGuard(h, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
	})

	t.Run("cross-site text plain", func(t *testing.T) {
		req := guardRequest(http.MethodPost, "http://"+host+"/api/launch", host, `{"targetId":"cursor"}`, token)
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Origin", "http://evil.example")
		rec := serveGuard(h, req)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("status %d", rec.Code)
		}
	})

	t.Run("same-origin text plain", func(t *testing.T) {
		req := guardRequest(http.MethodPost, "http://"+host+"/api/launch", host, `{"targetId":"cursor"}`, token)
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Origin", origin)
		rec := serveGuard(h, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("json post", func(t *testing.T) {
		req := guardRequest(http.MethodPost, "http://"+host+"/api/launch", host, `{}`, token)
		req.Header.Set("Content-Type", "application/json")
		rec := serveGuard(h, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("json charset", func(t *testing.T) {
		req := guardRequest(http.MethodPost, "http://"+host+"/api/launch", host, `{}`, token)
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		rec := serveGuard(h, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status %d", rec.Code)
		}
	})

	t.Run("get state change", func(t *testing.T) {
		rec := serveGuard(h, guardRequest(http.MethodGet, "http://"+host+"/api/launch", host, "", token))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status %d", rec.Code)
		}
	})

	t.Run("post without body", func(t *testing.T) {
		rec := serveGuard(h, guardRequest(http.MethodPost, "http://"+host+"/api/speedtest/stop", host, "", token))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("delete without body", func(t *testing.T) {
		rec := serveGuard(h, guardRequest(http.MethodDelete, "http://"+host+"/api/subs?id=1", host, "", token))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
	})
}

func TestEarlyCLIFlagsSkipAPI(t *testing.T) {
	var buf bytes.Buffer
	cleaned := false
	tested := false
	if !takeEarlyCLI(&buf, true, true, func() { cleaned = true }, func() { tested = true }) {
		t.Fatal("version must return before the API")
	}
	if cleaned || tested {
		t.Fatal("version returned after cleanup or self-test")
	}
	if strings.TrimSpace(buf.String()) != "Proxy Box "+appVersion {
		t.Fatalf("version output %q", buf.String())
	}

	cleaned = false
	tested = false
	if !takeEarlyCLI(io.Discard, false, true, func() { cleaned = true }, func() { tested = true }) {
		t.Fatal("self-test must return before the API")
	}
	if !cleaned || !tested {
		t.Fatal("self-test did not run after cleanup")
	}

	reached := false
	cleaned = false
	if takeEarlyCLI(io.Discard, false, false, func() { cleaned = true }, func() { t.Fatal("self-test ran") }) {
		reached = true
	}
	if reached || !cleaned {
		t.Fatal("normal start should clean old binaries and continue to the API")
	}
}

func TestBootstrapURLShape(t *testing.T) {
	g := newAPIGuard("abc", "127.0.0.1:9")
	if g.bootstrapURL() != "http://127.0.0.1:9/?t=abc" {
		t.Fatal(g.bootstrapURL())
	}
}

func serveGuard(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func guardRequest(method, rawURL, host, body, cookie string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, rawURL, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, rawURL, nil)
	}
	r.Host = host
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	}
	return r
}
