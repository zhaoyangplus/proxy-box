package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proxy-box/internal/configstore"
)

func postHandler(handler http.HandlerFunc, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr
}

func bodyMap(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v body %s", err, rr.Body.String())
	}
	return body
}

func swapOpeners(t *testing.T, app func(string) error, urlFn func(string) error) {
	t.Helper()
	prevApp, prevURL := openAppOrPathFn, openURLFn
	if app != nil {
		openAppOrPathFn = app
	}
	if urlFn != nil {
		openURLFn = urlFn
	}
	t.Cleanup(func() {
		openAppOrPathFn = prevApp
		openURLFn = prevURL
	})
}

func TestOpenBackupsSurfacesError(t *testing.T) {
	useTempConfig(t)
	swapOpeners(t, func(string) error {
		return fmt.Errorf("路径不存在: D:\\missing")
	}, nil)
	rr := postHandler(handleOpenBackups, "/api/open-backups", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	body := bodyMap(t, rr)
	if body["error"] != "路径不存在: D:\\missing" {
		t.Fatalf("error %#v", body["error"])
	}
	if _, ok := body["ok"]; ok {
		t.Fatalf("success field on error: %#v", body)
	}
}

func TestOpenBackupsOpensBaseDir(t *testing.T) {
	useTempConfig(t)
	want, err := configstore.BaseDir()
	if err != nil {
		t.Fatal(err)
	}
	var got string
	swapOpeners(t, func(path string) error {
		got = path
		return nil
	}, nil)
	rr := postHandler(handleOpenBackups, "/api/open-backups", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	if got != want {
		t.Fatalf("opened %q, want %q", got, want)
	}
	if bodyMap(t, rr)["ok"] != true {
		t.Fatalf("body %s", rr.Body.String())
	}
}

func TestOpenPathRejectsEmpty(t *testing.T) {
	called := false
	swapOpeners(t, func(string) error {
		called = true
		return nil
	}, nil)
	rr := postHandler(handleOpenPath, "/api/open-path", `{"path":"   ","parent":true}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	if bodyMap(t, rr)["error"] != "路径为空" {
		t.Fatalf("body %s", rr.Body.String())
	}
	if called {
		t.Fatal("empty path called opener")
	}
}

func TestOpenPathCleansParentAndSurfacesErrors(t *testing.T) {
	root := t.TempDir()
	leaf := filepath.Join(root, "app", "settings.json")
	want := filepath.Join(root, "app")
	var got string
	swapOpeners(t, func(path string) error {
		got = path
		return nil
	}, nil)
	payload, err := json.Marshal(map[string]any{"path": leaf, "parent": true})
	if err != nil {
		t.Fatal(err)
	}
	rr := postHandler(handleOpenPath, "/api/open-path", string(payload))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	if got != want {
		t.Fatalf("opened %q, want %q", got, want)
	}
	if st, err := os.Stat(want); err != nil || !st.IsDir() {
		t.Fatalf("parent dir: %v", err)
	}

	messy := filepath.Join(root, "sub", "inner", "..")
	clean := filepath.Join(root, "sub")
	payload, err = json.Marshal(map[string]any{"path": messy, "parent": false})
	if err != nil {
		t.Fatal(err)
	}
	rr = postHandler(handleOpenPath, "/api/open-path", string(payload))
	if rr.Code != http.StatusOK || got != clean {
		t.Fatalf("status %d opened %q body %s", rr.Code, got, rr.Body.String())
	}

	swapOpeners(t, func(string) error {
		return fmt.Errorf("打开失败(错误码 5): 拒绝访问")
	}, nil)
	payload, err = json.Marshal(map[string]any{"path": root, "parent": false})
	if err != nil {
		t.Fatal(err)
	}
	rr = postHandler(handleOpenPath, "/api/open-path", string(payload))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	if bodyMap(t, rr)["error"] != "打开失败(错误码 5): 拒绝访问" {
		t.Fatalf("body %s", rr.Body.String())
	}
}

func TestOpenPathMkdirError(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "not-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	called := false
	swapOpeners(t, func(string) error {
		called = true
		return nil
	}, nil)
	payload, err := json.Marshal(map[string]any{
		"path":   filepath.Join(blocker, "child"),
		"parent": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	rr := postHandler(handleOpenPath, "/api/open-path", string(payload))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	if bodyMap(t, rr)["error"] == "" {
		t.Fatal("empty error")
	}
	if called {
		t.Fatal("mkdir failure still opened")
	}
}

func TestOpenURLHandler(t *testing.T) {
	rr := postHandler(handleOpenURL, "/api/open-url", `{"url":"ftp://example.com"}`)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "仅支持打开 http/https 链接") {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}

	var got string
	swapOpeners(t, nil, func(raw string) error {
		got = raw
		return fmt.Errorf("打开失败(错误码 31): 没有关联的程序")
	})
	rr = postHandler(handleOpenURL, "/api/open-url", `{"url":"https://example.com/release"}`)
	if got != "https://example.com/release" {
		t.Fatalf("url %q", got)
	}
	if rr.Code != http.StatusBadRequest || bodyMap(t, rr)["error"] != "打开失败(错误码 31): 没有关联的程序" {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}

	swapOpeners(t, nil, func(raw string) error {
		got = raw
		return nil
	})
	rr = postHandler(handleOpenURL, "/api/open-url", `{"url":"http://example.com"}`)
	if rr.Code != http.StatusOK || got != "http://example.com" || bodyMap(t, rr)["ok"] != true {
		t.Fatalf("status %d url %q body %s", rr.Code, got, rr.Body.String())
	}

	rr = postHandler(handleOpenURL, "/api/open-url", `{`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad json status %d %s", rr.Code, rr.Body.String())
	}
}
