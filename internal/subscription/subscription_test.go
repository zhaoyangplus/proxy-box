package subscription

import (
	"bytes"
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"proxy-box/internal/configstore"
)

const oldBody = "OLD-PROVIDER"

func TestUpdateSingleLineBase64(t *testing.T) {
	text, canonical := uriFixture(t)
	usage := &configstore.SubUsage{Upload: 1, Download: 2, Total: 9, Expire: 8}
	res, path, ua := runUpdate(t, http.StatusOK, "total=9; upload=1; expire=8; download=2", "", canonical, nil)
	if res.NodeCount != 2 || !usageEqual(res.Usage, usage) || ua != defaultUA {
		t.Fatalf("result %+v ua %q", res, ua)
	}
	assertFile(t, path, canonical)
	decoded := decodeFile(t, path)
	if decoded != text {
		t.Fatalf("decoded %q", decoded)
	}
}

func TestUpdateWrappedBase64(t *testing.T) {
	text, canonical := uriFixture(t)
	res, path, _ := runUpdate(t, http.StatusOK, "upload=1; download=2; total=9; expire=8", "", wrap64(canonical), nil)
	if res.NodeCount != 2 || !usageEqual(res.Usage, &configstore.SubUsage{Upload: 1, Download: 2, Total: 9, Expire: 8}) {
		t.Fatalf("result %+v", res)
	}
	assertFile(t, path, canonical)
	if decodeFile(t, path) != text {
		t.Fatal("wrapped base64 did not round-trip")
	}
}

func TestUpdateURLSafeBase64(t *testing.T) {
	text, canonical := uriFixture(t)
	raw := base64.RawURLEncoding.EncodeToString([]byte(text))
	if !strings.ContainsAny(raw, "-_") {
		t.Fatal("fixture is not url-safe")
	}
	res, path, _ := runUpdate(t, http.StatusOK, "expire=8; total=9; download=2; upload=1", "", []byte(raw), nil)
	if res.NodeCount != 2 {
		t.Fatalf("nodes %d", res.NodeCount)
	}
	assertFile(t, path, canonical)
	if decodeFile(t, path) != text {
		t.Fatal("url-safe base64 did not become standard base64")
	}
}

func TestUpdatePlainURIList(t *testing.T) {
	text, canonical := uriFixture(t)
	plain := strings.ReplaceAll(text, "\n", "\r\n") + "\r\n\r\n"
	res, path, _ := runUpdate(t, http.StatusOK, " expire = 50 ; UPLOAD = 7 ; note=x", "vmess", []byte(plain), nil)
	if res.NodeCount != 2 || !usageEqual(res.Usage, &configstore.SubUsage{Upload: 7, Expire: 50}) {
		t.Fatalf("result %+v", res)
	}
	assertFile(t, path, canonical)
	if decodeFile(t, path) != text {
		t.Fatal("plain list was filtered or rewritten")
	}
}

func TestUpdateClashYAML(t *testing.T) {
	body := []byte("mixed-port: 7890\nproxies:\n  - name: \"节点甲\"\n    type: vmess\n    server: 127.0.0.1\n    port: 18001\n    uuid: b831381d-6324-4d53-ad4f-8cda48b30811\n    alterId: 0\n    cipher: auto\n  - name: node-b\n    type: ss\n    server: 127.0.0.1\n    port: 18002\n    cipher: aes-256-gcm\n    password: password\n")
	res, path, ua := runUpdate(t, http.StatusOK, "download=2; upload=1; expire=8; total=9", "", body, func(sub *configstore.Subscription) {
		sub.UserAgent = "ProxyBoxTest/1"
	})
	if res.NodeCount != 2 || ua != "ProxyBoxTest/1" {
		t.Fatalf("nodes %d ua %q", res.NodeCount, ua)
	}
	if !usageEqual(res.Usage, &configstore.SubUsage{Upload: 1, Download: 2, Total: 9, Expire: 8}) {
		t.Fatalf("usage %+v", res.Usage)
	}
	assertFile(t, path, body)
}

func TestUpdateHTMLPageKeepsOldFile(t *testing.T) {
	body := []byte("<!DOCTYPE html>\n<html><body>failed <a href=\"https://example.com\">retry</a></body></html>\n")
	_, path := runFail(t, http.StatusOK, body)
	assertFile(t, path, []byte(oldBody))
}

func TestUpdateEmptyBodyKeepsOldFile(t *testing.T) {
	_, path := runFail(t, http.StatusOK, nil)
	assertFile(t, path, []byte(oldBody))

	_, path = runFail(t, http.StatusOK, []byte(" \r\n\t"))
	assertFile(t, path, []byte(oldBody))
}

func TestUpdateHTTP500KeepsOldFile(t *testing.T) {
	_, canonical := uriFixture(t)
	_, path := runFail(t, http.StatusInternalServerError, canonical)
	assertFile(t, path, []byte(oldBody))
}

func TestUpdateNoSchemeKeepsOldFile(t *testing.T) {
	_, path := runFail(t, http.StatusOK, []byte("this is not a proxy list\n"))
	assertFile(t, path, []byte(oldBody))
}

func TestUpdateEmptyProxiesKeepsOldFile(t *testing.T) {
	_, path := runFail(t, http.StatusOK, []byte("proxies: []\n"))
	assertFile(t, path, []byte(oldBody))
}

func TestUpdateBase64HTMLKeepsOldFile(t *testing.T) {
	html := []byte("<!DOCTYPE html><html><body>https://example.com</body></html>")
	_, path := runFail(t, http.StatusOK, []byte(base64.StdEncoding.EncodeToString(html)))
	assertFile(t, path, []byte(oldBody))
}

func TestUpdateAllSchemes(t *testing.T) {
	lines := []string{
		"vmess://a",
		"vless://b",
		"ss://c",
		"ssr://d",
		"trojan://e",
		"hysteria2://f",
		"hy2://g",
		"tuic://h",
		"socks5://i",
		"socks://j",
		"http://k",
		"https://l",
		"anytls://m",
		"VMESS://upper",
	}
	text := strings.Join(lines, "\n")
	res, path, _ := runUpdate(t, http.StatusOK, "", "", []byte(text+"\n"), nil)
	if res.NodeCount != len(lines) {
		t.Fatalf("nodes %d", res.NodeCount)
	}
	if decodeFile(t, path) != text {
		t.Fatalf("decoded %q", decodeFile(t, path))
	}
	if res.Usage != nil {
		t.Fatal("missing usage header returned a value")
	}
}

func TestUpdateProxyFallback(t *testing.T) {
	_, canonical := uriFixture(t)
	var hits [2]int32
	var proxies []string
	for i := 0; i < 2; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ln.Close() })
		i := i
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				atomic.AddInt32(&hits[i], 1)
				conn.Close()
			}
		}()
		proxies = append(proxies, "http://"+ln.Addr().String())
	}
	res, path, _ := runUpdate(t, http.StatusOK, "upload=1; download=2; total=9; expire=8", "", canonical, nil, proxies...)
	if res.NodeCount != 2 {
		t.Fatalf("nodes %d", res.NodeCount)
	}
	assertFile(t, path, canonical)
	if hits[0] == 0 || hits[1] == 0 {
		t.Fatalf("proxy hits %d %d", hits[0], hits[1])
	}
}

func TestUpdateBodyCap(t *testing.T) {
	_, canonical := uriFixture(t)
	exact := make([]byte, maxBody)
	copy(exact, canonical)
	for i := len(canonical); i < len(exact); i++ {
		exact[i] = '\n'
	}
	res, path, _ := runUpdate(t, http.StatusOK, "", "", exact, nil)
	if res.NodeCount != 2 {
		t.Fatalf("nodes %d", res.NodeCount)
	}
	assertFile(t, path, canonical)

	over := append(append([]byte{}, exact...), '\n')
	_, path = runFail(t, http.StatusOK, over)
	assertFile(t, path, []byte(oldBody))
}

func TestUpdateRejectsBadID(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte("vmess://a\n"))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "x.txt")
	before, beforeErr := os.Stat(outside)
	for _, id := range []string{"", "../x", "Bad", "a/b", ".."} {
		_, err := Update(context.Background(), configstore.Subscription{
			ID:  id,
			URL: srv.URL,
		}, nil, dir)
		if err == nil {
			t.Fatalf("id %q succeeded", id)
		}
	}
	after, afterErr := os.Stat(outside)
	if os.IsNotExist(beforeErr) {
		if !os.IsNotExist(afterErr) {
			t.Fatal("bad id created a file outside the providers dir")
		}
	} else if beforeErr != nil {
		t.Fatal(beforeErr)
	} else if afterErr != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("bad id changed a file outside the providers dir")
	}
	if hits != 0 {
		t.Fatalf("bad id downloaded %d times", hits)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("providers dir has %d entries", len(entries))
	}
}

func TestUpdateRejectsEmptyProvidersDir(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cwd, "sub_1.txt")
	t.Cleanup(func() { os.Remove(path) })
	_, err = Update(context.Background(), configstore.Subscription{
		ID:  "sub_1",
		URL: "http://127.0.0.1/sub",
	}, nil, " ")
	if err == nil {
		t.Fatal("expected error")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("wrote sub_1.txt into the working directory")
	}
}

func uriFixture(t *testing.T) (string, []byte) {
	t.Helper()
	text := "vmess://11111111-1111-1111-1111-111111111111@127.0.0.1:10001?x=???#vmess-a\nvless://22222222-2222-2222-2222-222222222222@127.0.0.1:10002?x=???#vless-b"
	canonical := []byte(base64.StdEncoding.EncodeToString([]byte(text)))
	if !strings.ContainsAny(string(canonical), "+/") {
		t.Fatal("fixture base64 has no standard-only alphabet")
	}
	return text, canonical
}

func wrap64(in []byte) []byte {
	var out []byte
	for i := 0; i < len(in); i += 64 {
		if len(out) > 0 {
			out = append(out, '\r', '\n')
		}
		j := i + 64
		if j > len(in) {
			j = len(in)
		}
		out = append(out, in[i:j]...)
	}
	return out
}

func runUpdate(t *testing.T, status int, header, filter string, body []byte, edit func(*configstore.Subscription), proxies ...string) (UpdateResult, string, string) {
	t.Helper()
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		if header != "" {
			w.Header().Set("Subscription-Userinfo", header)
		}
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	path := filepath.Join(dir, "sub_1.txt")
	if err := os.WriteFile(path, []byte(oldBody), 0644); err != nil {
		t.Fatal(err)
	}
	sub := configstore.Subscription{
		ID:      "sub_1",
		Name:    "demo",
		URL:     srv.URL,
		Filter:  filter,
		Enabled: true,
	}
	if edit != nil {
		edit(&sub)
	}
	res, err := Update(context.Background(), sub, proxies, dir)
	if err != nil {
		t.Fatal(err)
	}
	assertNoTemps(t, dir)
	return res, path, ua
}

func runFail(t *testing.T, status int, body []byte) (error, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	path := filepath.Join(dir, "sub_1.txt")
	if err := os.WriteFile(path, []byte(oldBody), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Update(context.Background(), configstore.Subscription{
		ID:      "sub_1",
		Name:    "demo",
		URL:     srv.URL,
		Enabled: true,
	}, nil, dir)
	if err == nil {
		t.Fatal("expected error")
	}
	assertNoTemps(t, dir)
	return err, path
}

func assertFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(got, want) {
		return
	}
	if len(got) > 180 || len(want) > 180 {
		t.Fatalf("file length got %d want %d", len(got), len(want))
	}
	t.Fatalf("file\n got %q\nwant %q", got, want)
}

func assertNoTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", entry.Name())
		}
	}
}

func decodeFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return string(decoded)
}

func usageEqual(got, want *configstore.SubUsage) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
}
