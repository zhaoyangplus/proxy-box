package core

import (
	"archive/zip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(t.TempDir(), "custom.exe")
	if err := os.WriteFile(exe, []byte("MZhello"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ImportBinary(exe, dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != BinaryFileName {
		t.Fatal(got)
	}
	body, err := os.ReadFile(got)
	if err != nil || string(body) != "MZhello" {
		t.Fatalf("body %q %v", body, err)
	}
	if leftover(t, dir, BinaryFileName) {
		t.Fatal("temp file left behind")
	}

	bad := filepath.Join(t.TempDir(), "bad.exe")
	if err := os.WriteFile(bad, []byte("not-pe"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportBinary(bad, dir); !errors.Is(err, ErrNoPE) {
		t.Fatalf("bad exe: %v", err)
	}
	if _, err := ImportBinary(bad+".txt", dir); err == nil {
		t.Fatal("expected extension error")
	}

	zipPath := filepath.Join(t.TempDir(), "core.zip")
	writeZip(t, zipPath, map[string]string{"dir/mihomo-windows-amd64-compatible.exe": "MZfromzip"})
	got, err = ImportBinary(zipPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(got)
	if string(body) != "MZfromzip" {
		t.Fatalf("zip body %q", body)
	}

	badZip := filepath.Join(t.TempDir(), "bad.zip")
	writeZip(t, badZip, map[string]string{"mihomo.exe": "XX"})
	if _, err := ImportBinary(badZip, dir); !errors.Is(err, ErrNoPE) {
		t.Fatalf("bad zip: %v", err)
	}
	body, _ = os.ReadFile(got)
	if string(body) != "MZfromzip" {
		t.Fatal("bad zip replaced a good exe")
	}

	outside := t.TempDir()
	slip := filepath.Join(t.TempDir(), "slip.zip")
	writeZip(t, slip, map[string]string{"../../mihomo.exe": "MZslip"})
	if _, err := ImportBinary(slip, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "mihomo.exe")); !os.IsNotExist(err) {
		t.Fatal("zip entry escaped the core dir")
	}
}

func TestEnsureBinarySiblingThenLocal(t *testing.T) {
	exeDir := t.TempDir()
	local := t.TempDir()
	coreDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	restoreExec(t, filepath.Join(exeDir, "ProxyBox.exe"))
	hits := 0
	restoreAsset(t, "http://127.0.0.1:1/missing")

	sibling := filepath.Join(exeDir, BinaryFileName)
	if err := os.WriteFile(sibling, []byte("MZsibling"), 0644); err != nil {
		t.Fatal(err)
	}
	appData := filepath.Join(local, "ProxyBox", "core", BinaryFileName)
	if err := os.MkdirAll(filepath.Dir(appData), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appData, []byte("MZlocal"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := EnsureBinary(context.Background(), coreDir, []string{"http://127.0.0.1:1"}, func(int64, int64) { hits++ })
	if err != nil {
		t.Fatal(err)
	}
	if !sameFile(got, sibling) {
		t.Fatalf("got %s want sibling %s", got, sibling)
	}
	if hits != 0 {
		t.Fatal("download ran")
	}

	if err := os.WriteFile(sibling, []byte("NO"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err = EnsureBinary(context.Background(), coreDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sameFile(got, appData) {
		t.Fatalf("got %s want local %s", got, appData)
	}
}

func TestEnsureBinaryRejectsBadSHA(t *testing.T) {
	const body = "not-the-official-zip"
	var directHits int
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directHits++
		_, _ = w.Write([]byte(body))
	}))
	defer direct.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("bad-from-proxy"))
	}))
	defer proxy.Close()

	dir := t.TempDir()
	restoreExec(t, filepath.Join(t.TempDir(), "ProxyBox.exe"))
	t.Setenv("LOCALAPPDATA", t.TempDir())
	restoreAsset(t, direct.URL)
	var n int64
	_, err := EnsureBinary(context.Background(), dir, []string{proxy.URL}, func(got, _ int64) { n = got })
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("err %v", err)
	}
	if directHits != 0 {
		t.Fatalf("direct hits %d", directHits)
	}
	if n != int64(len("bad-from-proxy")) {
		t.Fatalf("progress %d", n)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("left %v", entries)
	}

	_, err = EnsureBinary(context.Background(), dir, []string{"http://127.0.0.1:1"}, func(got, _ int64) { n = got })
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("direct err %v", err)
	}
	if directHits != 1 {
		t.Fatalf("direct hits %d", directHits)
	}
	if n != int64(len(body)) {
		t.Fatalf("progress %d", n)
	}
}

func restoreExec(t *testing.T, path string) {
	t.Helper()
	old := currentExecutable
	currentExecutable = func() (string, error) { return path, nil }
	t.Cleanup(func() { currentExecutable = old })
}

func restoreAsset(t *testing.T, raw string) {
	t.Helper()
	old := assetURL
	assetURL = raw
	t.Cleanup(func() { assetURL = old })
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func leftover(t *testing.T, dir, keep string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != keep {
			return true
		}
	}
	return false
}

func sameFile(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && strings.EqualFold(aa, bb)
}
