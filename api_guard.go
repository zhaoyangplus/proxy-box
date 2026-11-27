package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

const sessionCookieName = "pb_session"

// apiMethods is the method allow-list for /api/*. GET is only the read routes.
var apiMethods = map[string][]string{
	"/api/events":             {http.MethodGet},
	"/api/snapshot":           {http.MethodGet},
	"/api/version":            {http.MethodGet},
	"/api/core/status":        {http.MethodGet},
	"/api/update/status":      {http.MethodGet},
	"/api/speedtest/status":   {http.MethodGet},
	"/api/subs":               {http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete},
	"/api/diagnostics":        {http.MethodGet, http.MethodPost},
	"/api/core/mode":          {http.MethodPost, http.MethodPatch},
	"/api/subs/update":        {http.MethodPost},
	"/api/core/bypass-cn":     {http.MethodPost},
	"/api/core/start":         {http.MethodPost},
	"/api/core/stop":          {http.MethodPost},
	"/api/core/import-binary": {http.MethodPost},
	"/api/browse-core":        {http.MethodPost},
	"/api/sysproxy/set":       {http.MethodPost},
	"/api/nodes/list":         {http.MethodPost},
	"/api/nodes/select":       {http.MethodPost},
	"/api/nodes/test":         {http.MethodPost},
	"/api/speedtest/start":    {http.MethodPost},
	"/api/speedtest/stop":     {http.MethodPost},
	"/api/tools/toggle":       {http.MethodPost},
	"/api/one-click":          {http.MethodPost},
	"/api/restore-all":        {http.MethodPost},
	"/api/optimize-node":      {http.MethodPost},
	"/api/launch":             {http.MethodPost},
	"/api/shortcut":           {http.MethodPost},
	"/api/open-backups":       {http.MethodPost},
	"/api/open-path":          {http.MethodPost},
	"/api/browse-exe":         {http.MethodPost},
	"/api/save-config":        {http.MethodPost},
	"/api/open-url":           {http.MethodPost},
	"/api/update/check":       {http.MethodPost},
	"/api/update/apply":       {http.MethodPost},
}

type apiGuard struct {
	token string
	host  string
	next  http.Handler
}

func newSessionToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func newAPIGuard(token, host string) *apiGuard {
	return &apiGuard{token: token, host: host}
}

func (g *apiGuard) wrap(next http.Handler) http.Handler {
	g.next = next
	return g
}

func (g *apiGuard) bootstrapURL() string {
	q := url.Values{}
	q.Set("t", g.token)
	return "http://" + g.host + "/?" + q.Encode()
}

func (g *apiGuard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Host != g.host {
		writeGuardError(w, http.StatusForbidden, "禁止访问")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+g.host {
		writeGuardError(w, http.StatusForbidden, "禁止访问")
		return
	}
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		writeGuardError(w, http.StatusForbidden, "禁止访问")
		return
	}

	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	if r.Method == http.MethodGet && path == "/" && r.URL.Query().Has("t") {
		if !tokenMatch(r.URL.Query().Get("t"), g.token) {
			writeGuardError(w, http.StatusUnauthorized, "未授权")
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    g.token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || !tokenMatch(cookie.Value, g.token) {
		writeGuardError(w, http.StatusUnauthorized, "未授权")
		return
	}
	if strings.HasPrefix(path, "/api/") {
		if code, msg := rejectAPIWrite(w, r); code != 0 {
			writeGuardError(w, code, msg)
			return
		}
	}
	g.next.ServeHTTP(w, r)
}

func rejectAPIWrite(w http.ResponseWriter, r *http.Request) (int, string) {
	methods, ok := apiMethods[r.URL.Path]
	if !ok || !methodAllowed(methods, r.Method) {
		if ok {
			w.Header().Set("Allow", strings.Join(methods, ", "))
		}
		return http.StatusMethodNotAllowed, "不允许该请求方法"
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead || !requestHasBody(r) {
		return 0, ""
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return http.StatusUnsupportedMediaType, "Content-Type 必须是 application/json"
	}
	return 0, ""
}

func methodAllowed(methods []string, got string) bool {
	for _, m := range methods {
		if m == got {
			return true
		}
	}
	return false
}

func requestHasBody(r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return false
	}
	return r.ContentLength != 0
}

func tokenMatch(got, want string) bool {
	if want == "" {
		return false
	}
	gb := []byte(got)
	wb := []byte(want)
	if len(gb) != len(wb) {
		return subtle.ConstantTimeCompare(wb, wb) == 0
	}
	return subtle.ConstantTimeCompare(gb, wb) == 1
}

func writeGuardError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
