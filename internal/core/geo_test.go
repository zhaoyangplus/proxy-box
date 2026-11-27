package core

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGeoFilesReadyNeedsBothSaneFiles(t *testing.T) {
	dir := t.TempDir()
	if GeoFilesReady(dir) || GeoFilesReady("") {
		t.Fatal("empty directory looked ready")
	}
	body := bytes.Repeat([]byte("G"), minGeoIPBytes)
	if err := os.WriteFile(filepath.Join(dir, GeoIPFile), body, 0644); err != nil {
		t.Fatal(err)
	}
	if GeoFilesReady(dir) {
		t.Fatal("mmdb alone looked ready")
	}
	if err := os.WriteFile(filepath.Join(dir, GeoSiteFile), body, 0644); err != nil {
		t.Fatal(err)
	}
	if !GeoFilesReady(dir) {
		t.Fatal("both files should be ready")
	}
	html := append([]byte("<!DOCTYPE html>\n"), body...)
	if err := os.WriteFile(filepath.Join(dir, GeoSiteFile), html, 0644); err != nil {
		t.Fatal(err)
	}
	if GeoFilesReady(dir) {
		t.Fatal("html page looked ready")
	}
}

func TestDownloadGeoSuccessThroughProxy(t *testing.T) {
	if _, err := exec.LookPath("curl.exe"); err != nil {
		t.Fatal(err)
	}
	restoreLimits(t, 5*time.Second, 20*time.Second, 3)
	ip := bytes.Repeat([]byte("I"), 80)
	site := bytes.Repeat([]byte("S"), 90)
	var hosts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts = append(hosts, r.Host)
		if strings.Contains(r.URL.Path, "missing") {
			http.NotFound(w, r)
			return
		}
		if strings.Contains(r.URL.Path, "geoip") {
			_, _ = w.Write(ip)
			return
		}
		_, _ = w.Write(site)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	var last int64
	err := DownloadGeo(context.Background(), dir, srv.URL, []GeoFile{
		{Name: GeoIPFile, URLs: []string{"http://geo.test/missing", "http://geo.test/" + GeoIPFile}, MinBytes: 50},
		{Name: GeoSiteFile, URLs: []string{"http://geo.test/" + GeoSiteFile}, MinBytes: 50},
	}, func(n, _ int64) { last = n })
	if err != nil {
		t.Fatal(err)
	}
	assertGeoBody(t, filepath.Join(dir, GeoIPFile), ip)
	assertGeoBody(t, filepath.Join(dir, GeoSiteFile), site)
	if _, err := os.Stat(filepath.Join(dir, GeoIPFile+".tmp")); !os.IsNotExist(err) {
		t.Fatalf("tmp left behind: %v", err)
	}
	if last != int64(len(ip)+len(site)) {
		t.Fatalf("progress %d", last)
	}
	if !strings.Contains(strings.Join(hosts, ","), "geo.test") {
		t.Fatalf("hosts %v, request did not go through the proxy", hosts)
	}
}

func TestDownloadGeoRejectsHTML(t *testing.T) {
	if _, err := exec.LookPath("curl.exe"); err != nil {
		t.Fatal(err)
	}
	restoreLimits(t, 5*time.Second, 20*time.Second, 2)
	body := []byte("<!DOCTYPE html><html><body>not a database</body></html>")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	err := DownloadGeo(context.Background(), dir, srv.URL, []GeoFile{
		{Name: GeoIPFile, URLs: []string{"http://geo.test/" + GeoIPFile}, MinBytes: 8},
	}, nil)
	if !errors.Is(err, ErrGeoHTML) {
		t.Fatalf("err %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, GeoIPFile)); !os.IsNotExist(statErr) {
		t.Fatalf("html was saved: %v", statErr)
	}
}

func TestDownloadGeoRejectsShortFile(t *testing.T) {
	if _, err := exec.LookPath("curl.exe"); err != nil {
		t.Fatal(err)
	}
	restoreLimits(t, 5*time.Second, 20*time.Second, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("abcd"))
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	err := DownloadGeo(context.Background(), dir, srv.URL, []GeoFile{
		{Name: GeoIPFile, URLs: []string{"http://geo.test/" + GeoIPFile}, MinBytes: 100},
	}, nil)
	if !errors.Is(err, ErrGeoTooSmall) {
		t.Fatalf("err %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, GeoIPFile)); !os.IsNotExist(statErr) {
		t.Fatalf("short file was saved: %v", statErr)
	}
}

func TestDownloadGeoResumesAfterCut(t *testing.T) {
	if _, err := exec.LookPath("curl.exe"); err != nil {
		t.Fatal(err)
	}
	restoreLimits(t, 5*time.Second, 20*time.Second, 4)
	payload := bytes.Repeat([]byte("0123456789abcdef"), 400)
	srv, hits := newCutServer(t, payload, 1500, http.StatusPartialContent)
	dir := t.TempDir()
	err := DownloadGeo(context.Background(), dir, srv.URL, []GeoFile{
		{Name: GeoIPFile, URLs: []string{"http://geo.test/" + GeoIPFile}, MinBytes: 100},
	}, nil)
	if err != nil {
		t.Fatalf("err %v hits %+v", err, hits.snapshot())
	}
	assertGeoBody(t, filepath.Join(dir, GeoIPFile), payload)
	got := hits.snapshot()
	if len(got) < 2 || !strings.HasPrefix(got[1].rang, "bytes=") {
		t.Fatalf("hits %+v", got)
	}
}

func TestPreflightGeoAcceptsRealFilesAndRejectsGarbage(t *testing.T) {
	exe := `D:\go\lean-code\proxy-box-scratch\pr2-fix-live\run2\mihomo.exe`
	src := `D:\go\lean-code\proxy-box-scratch\proto\geodata`
	if _, err := os.Stat(exe); err != nil {
		t.Skip(err)
	}
	if _, err := os.Stat(filepath.Join(src, "geosite.dat")); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	if err := copyGeoFile(filepath.Join(src, "geoip.metadb"), filepath.Join(dir, GeoIPFile)); err != nil {
		t.Fatal(err)
	}
	if err := copyGeoFile(filepath.Join(src, "geosite.dat"), filepath.Join(dir, GeoSiteFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "providers"), 0755); err != nil {
		t.Fatal(err)
	}
	provider := "proxies:\n  - {name: local, type: http, server: 127.0.0.1, port: 1}\n"
	if err := os.WriteFile(filepath.Join(dir, "providers", "sub.txt"), []byte(provider), 0644); err != nil {
		t.Fatal(err)
	}
	body, err := Render(ConfigParams{
		MixedPort: 17890, ControllerPort: 17891, AIDedicatedPort: 17892,
		Secret: "s3cret", Mode: ModeRule, BypassCN: true, GeoReady: true,
	}, []ProviderSpec{{ID: "sub", Name: "sub", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := PreflightGeo(context.Background(), exe, dir, body); err != nil {
		t.Fatal(err)
	}
	site := filepath.Join(dir, GeoSiteFile)
	info, err := os.Stat(site)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, info.Size())
	raw, err := os.ReadFile(site)
	if err != nil {
		t.Fatal(err)
	}
	copy(buf, raw)
	buf[0] = 1
	if err := os.WriteFile(site, buf, 0644); err != nil {
		t.Fatal(err)
	}
	err = PreflightGeo(context.Background(), exe, dir, body)
	if !errors.Is(err, ErrGeoPreflight) {
		t.Fatalf("err %v", err)
	}
	got, err := os.ReadFile(site)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(buf) || got[0] != 1 {
		t.Fatalf("live geosite changed len %d first %d", len(got), got[0])
	}
}

func assertGeoBody(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("body len %d want %d", len(got), len(want))
	}
}
