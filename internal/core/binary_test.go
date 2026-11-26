package core

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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

func restoreCurlLookup(t *testing.T, fn func() (string, error)) {
	t.Helper()
	old := lookupCurl
	lookupCurl = fn
	t.Cleanup(func() { lookupCurl = old })
}

func restoreLimits(t *testing.T, stall, overall time.Duration, attempts int) {
	t.Helper()
	oldStall, oldOverall, oldAttempts := downloadStall, downloadOverall, downloadAttempts
	downloadStall, downloadOverall, downloadAttempts = stall, overall, attempts
	t.Cleanup(func() {
		downloadStall, downloadOverall, downloadAttempts = oldStall, oldOverall, oldAttempts
	})
}

func TestDownloadKeepsPartialAfterStall(t *testing.T) {
	restoreCurlLookup(t, func() (string, error) { return "", exec.ErrNotFound })
	restoreLimits(t, 200*time.Millisecond, 3*time.Second, 2)
	payload := bytes.Repeat([]byte("abc"), 4000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload[:1500])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()
	restoreAsset(t, srv.URL)
	dir := t.TempDir()
	_, err := downloadAndInstall(context.Background(), dir, "", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	st, statErr := os.Stat(filepath.Join(dir, AssetName+".tmp"))
	if statErr != nil || st.Size() == 0 {
		t.Fatalf("partial size %v err %v download %v", st, statErr, err)
	}
}

func TestCurlKeepsPartialAcrossStall(t *testing.T) {
	if _, err := exec.LookPath("curl.exe"); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("0123456789abcdef"), 8192)
	const cut = 20000
	hits := &hitLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.add(r.Header.Get("Accept"), r.Header.Get("Range"))
		if n == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload[:cut])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
			return
		}
		start, ok := rangeStart(r.Header.Get("Range"))
		if !ok || start > len(payload) {
			http.Error(w, "bad range", http.StatusBadRequest)
			return
		}
		chunk := payload[start:]
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(payload)-1, len(payload)))
		w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(chunk)
	}))
	t.Cleanup(srv.Close)
	restoreAsset(t, srv.URL)
	restoreLimits(t, 300*time.Millisecond, 8*time.Second, 4)
	dest := filepath.Join(t.TempDir(), "asset.bin")
	err := fetchAsset(context.Background(), dest, "", nil)
	got := hits.snapshot()
	if err != nil {
		t.Fatalf("err %v hits %+v resumes %d", err, got, downloadResumes.Load())
	}
	sum := sha256.Sum256(payload)
	if hashFile(t, dest) != hex.EncodeToString(sum[:]) {
		t.Fatal("file hash mismatch")
	}
	if len(got) < 2 || got[1].rang == "" {
		t.Fatalf("hits %+v", got)
	}
}

func TestCurlDownloadResumes(t *testing.T) {
	if _, err := exec.LookPath("curl.exe"); err != nil {
		t.Fatal(err)
	}
	assertResume(t, false, http.StatusPartialContent)
}

func TestHTTPDownloadResumes(t *testing.T) {
	restoreCurlLookup(t, func() (string, error) { return "", exec.ErrNotFound })
	assertResume(t, true, http.StatusPartialContent)
}

func TestHTTPDownloadRestartsOnFullResponse(t *testing.T) {
	restoreCurlLookup(t, func() (string, error) { return "", exec.ErrNotFound })
	assertResume(t, true, http.StatusOK)
}

func TestHTTPProgressUsesAssetSizeWithoutLength(t *testing.T) {
	restoreCurlLookup(t, func() (string, error) { return "", exec.ErrNotFound })
	restoreLimits(t, 5*time.Second, 20*time.Second, 2)
	payload := []byte("chunked-body")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	restoreAsset(t, srv.URL)
	var total int64
	dest := filepath.Join(t.TempDir(), "asset.bin")
	if err := fetchAsset(context.Background(), dest, "", func(_, tot int64) { total = tot }); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(dest)
	if err != nil || string(body) != string(payload) {
		t.Fatalf("body %q %v", body, err)
	}
	if total != AssetSize {
		t.Fatalf("total %d", total)
	}
}

func TestDownloadAbortsOnStallAndOverallCap(t *testing.T) {
	cases := []struct {
		name    string
		http    bool
		stall   time.Duration
		overall time.Duration
		limit   time.Duration
	}{
		{name: "curl stall", stall: 200 * time.Millisecond, overall: 8 * time.Second, limit: 4 * time.Second},
		{name: "http stall", http: true, stall: 200 * time.Millisecond, overall: 8 * time.Second, limit: 4 * time.Second},
		{name: "curl overall", stall: 15 * time.Second, overall: time.Second, limit: 5 * time.Second},
		{name: "http overall", http: true, stall: 15 * time.Second, overall: time.Second, limit: 5 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.http {
				restoreCurlLookup(t, func() (string, error) { return "", exec.ErrNotFound })
			} else if _, err := exec.LookPath("curl.exe"); err != nil {
				t.Fatal(err)
			}
			restoreLimits(t, tc.stall, tc.overall, 2)
			srv := hangServer(t)
			restoreAsset(t, srv.URL)
			dest := filepath.Join(t.TempDir(), "asset.bin")
			start := time.Now()
			err := fetchAsset(context.Background(), dest, "", nil)
			elapsed := time.Since(start)
			if err == nil {
				t.Fatal("expected error")
			}
			if elapsed > tc.limit {
				t.Fatalf("elapsed %s limit %s err %v", elapsed, tc.limit, err)
			}
		})
	}
}

func assertResume(t *testing.T, httpFallback bool, follow int) {
	t.Helper()
	payload := bytes.Repeat([]byte("0123456789abcdef"), 2048)
	const cut = 2048
	srv, hits := newCutServer(t, payload, cut, follow)
	restoreAsset(t, srv.URL)
	restoreLimits(t, 5*time.Second, 20*time.Second, 4)
	downloadResumes.Store(0)
	var last, total int64
	dest := filepath.Join(t.TempDir(), "asset.bin")
	if err := fetchAsset(context.Background(), dest, "", func(n, tot int64) {
		last, total = n, tot
	}); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	if hashFile(t, dest) != hex.EncodeToString(sum[:]) {
		t.Fatal("file hash mismatch")
	}
	if last != int64(len(payload)) {
		t.Fatalf("progress %d", last)
	}
	wantTotal := int64(AssetSize)
	if httpFallback {
		wantTotal = int64(len(payload))
	}
	if total != wantTotal {
		t.Fatalf("total %d want %d", total, wantTotal)
	}
	got := hits.snapshot()
	if len(got) < 2 {
		t.Fatalf("requests %d", len(got))
	}
	if got[1].rang == "" || !strings.HasPrefix(got[1].rang, "bytes=") {
		t.Fatalf("second range %q hits %+v", got[1].rang, got)
	}
	if httpFallback && got[0].accept != "" {
		t.Fatalf("Accept %q, http path did not run", got[0].accept)
	}
	if !httpFallback && got[0].accept != "*/*" {
		t.Fatalf("Accept %q, curl path did not run", got[0].accept)
	}
	if downloadResumes.Load() < 1 {
		t.Fatalf("resumes %d", downloadResumes.Load())
	}
}

type downloadHit struct {
	accept string
	rang   string
}

type hitLog struct {
	mu   sync.Mutex
	hits []downloadHit
}

func (l *hitLog) add(accept, rang string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits = append(l.hits, downloadHit{accept, rang})
	return len(l.hits)
}

func (l *hitLog) snapshot() []downloadHit {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]downloadHit, len(l.hits))
	copy(out, l.hits)
	return out
}

func newCutServer(t *testing.T, payload []byte, cut, follow int) (*httptest.Server, *hitLog) {
	t.Helper()
	hits := &hitLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.add(r.Header.Get("Accept"), r.Header.Get("Range"))
		if n == 1 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "hijack unsupported", http.StatusInternalServerError)
				return
			}
			conn, buf, err := hj.Hijack()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer conn.Close()
			fmt.Fprintf(buf, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", len(payload))
			_, _ = buf.Write(payload[:cut])
			_ = buf.Flush()
			return
		}
		if follow == http.StatusOK {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
			return
		}
		start, ok := rangeStart(r.Header.Get("Range"))
		if !ok || start > len(payload) {
			http.Error(w, "bad range", http.StatusBadRequest)
			return
		}
		chunk := payload[start:]
		end := len(payload) - 1
		if end < start {
			end = start
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(chunk)
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

func rangeStart(header string) (int, bool) {
	header = strings.TrimSpace(header)
	header = strings.TrimPrefix(header, "bytes=")
	if header == "" || strings.HasPrefix(header, "-") {
		return 0, false
	}
	if i := strings.IndexByte(header, '-'); i >= 0 {
		header = header[:i]
	}
	n, err := strconv.Atoi(header)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func hangServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
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
