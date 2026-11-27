package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"1.0.0", "v1.0.0", 0},
		{"v1.0.0", "v1.0.1", -1},
		{"v1.0.9", "v1.0.10", -1},
		{"v1.2.0", "v1.1.9", 1},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.0.0-beta.1", "v1.0.0", -1},
		{"v1.0.0", "v1.0.0-rc.1", 1},
		{"dev", "v1.0.0", -1},
	}

	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCheckLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/zhaoyangplus/proxy-box/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(ghRelease{
			TagName:     "v1.2.0",
			Name:        "Proxy Box v1.2.0",
			Body:        "- 新增软件内自动更新\n- 优化节点优选",
			HTMLURL:     "https://github.com/zhaoyangplus/proxy-box/releases/tag/v1.2.0",
			PublishedAt: "2026-11-25T12:00:00Z",
			Assets: []ghAsset{
				{Name: "checksums.txt", Size: 128, BrowserDownloadURL: "https://example.com/checksums.txt"},
				{Name: "ProxyBox.exe", Size: 8500000, BrowserDownloadURL: "https://example.com/ProxyBox.exe"},
			},
		})
	}))
	defer srv.Close()

	u := New("v1.0.0")
	u.APIBaseURL = srv.URL

	res, err := u.CheckLatest(context.Background(), "")
	if err != nil {
		t.Fatalf("CheckLatest failed: %v", err)
	}
	if !res.HasUpdate {
		t.Fatalf("expected HasUpdate=true for v1.0.0 -> v1.2.0")
	}
	if res.AssetName != "ProxyBox.exe" || res.AssetSize != 8500000 {
		t.Fatalf("unexpected asset: %+v", res)
	}
	if res.DownloadURL != "https://example.com/ProxyBox.exe" {
		t.Fatalf("unexpected downloadUrl: %s", res.DownloadURL)
	}

	// Same version should report HasUpdate=false
	uSame := New("v1.2.0")
	uSame.APIBaseURL = srv.URL
	resSame, err := uSame.CheckLatest(context.Background(), "")
	if err != nil {
		t.Fatalf("CheckLatest same version failed: %v", err)
	}
	if resSame.HasUpdate {
		t.Fatalf("expected HasUpdate=false when already on latest")
	}
}

func TestValidatePEAndReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	origExe := filepath.Join(dir, "ProxyBox.exe")
	newExe := filepath.Join(dir, "ProxyBox.exe.new")

	oldData := append([]byte("MZ"), make([]byte, 128)...)
	newData := append([]byte("MZNEWVERSION"), make([]byte, 200)...)

	if err := os.WriteFile(origExe, oldData, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newExe, newData, 0755); err != nil {
		t.Fatal(err)
	}

	if err := validatePEFile(newExe, int64(len(newData))); err != nil {
		t.Fatalf("validatePEFile valid PE failed: %v", err)
	}

	// Invalid HTML payload must be rejected
	badFile := filepath.Join(dir, "bad.exe")
	_ = os.WriteFile(badFile, []byte("<html>404 Not Found</html>"+string(make([]byte, 100))), 0755)
	if err := validatePEFile(badFile, 0); err == nil {
		t.Fatalf("expected non-PE file to be rejected")
	}

	if err := replaceExecutable(origExe, newExe); err != nil {
		t.Fatalf("replaceExecutable failed: %v", err)
	}
	got, err := os.ReadFile(origExe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got[:12]) != "MZNEWVERSION" {
		t.Fatalf("expected swapped binary content, got %q", string(got[:12]))
	}
	if _, err := os.Stat(origExe + ".old"); err != nil {
		t.Fatalf("expected backup .old file to exist: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func httpResponse(req *http.Request, status int, hdr http.Header, body []byte) *http.Response {
	if hdr == nil {
		hdr = make(http.Header)
	}
	if body == nil {
		body = []byte{}
	}
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Header:        hdr,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

func peBytes(fill byte, n int) []byte {
	if n < 64 {
		n = 64
	}
	buf := bytes.Repeat([]byte{fill}, n)
	buf[0], buf[1] = 'M', 'Z'
	return buf
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func releaseBody(downloadURL, digest string, size int) string {
	raw, _ := json.Marshal(ghRelease{
		TagName: "v2.4.0",
		Name:    "Proxy Box v2.4.0",
		Assets: []ghAsset{{
			Name:               "ProxyBox.exe",
			Size:               int64(size),
			BrowserDownloadURL: downloadURL,
			Digest:             digest,
		}},
	})
	return string(raw)
}

func TestCheckLatestPinsRepoAndDigest(t *testing.T) {
	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = io.WriteString(w, releaseBody(
			"https://github.com/zhaoyangplus/proxy-box/releases/download/v2.4.0/ProxyBox.exe",
			digest,
			80,
		))
	}))
	defer srv.Close()

	u := New("v2.3.0")
	u.APIBaseURL = srv.URL
	res, err := u.CheckLatest(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/repos/"+DefaultOwner+"/"+DefaultRepo+"/releases/latest" {
		t.Fatalf("path %s", path)
	}
	if res.Digest != digest {
		t.Fatalf("digest %q", res.Digest)
	}
}

func TestPrepareRejectsUnsafeAssetURL(t *testing.T) {
	payload := peBytes(0x22, 80)
	digest := digestOf(payload)
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"http", "http://github.com/zhaoyangplus/proxy-box/releases/download/v2.4.0/ProxyBox.exe", "HTTPS"},
		{"foreign", "https://example.com/ProxyBox.exe", "example.com"},
		{"lookalike", "https://github.com.evil.com/ProxyBox.exe", "github.com.evil.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := New("v1.0.0")
			u.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.github.com" {
					t.Errorf("download started for %s", r.URL)
				}
				return httpResponse(r, http.StatusOK, nil, []byte(releaseBody(tc.url, digest, len(payload)))), nil
			})
			_, err := u.PrepareDownload(context.Background(), "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v", err)
			}
		})
	}
}

func TestPrepareRejectsMissingDigest(t *testing.T) {
	assetURL := "https://github.com/zhaoyangplus/proxy-box/releases/download/v2.4.0/ProxyBox.exe"
	u := New("v1.0.0")
	u.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.github.com" {
			t.Errorf("download started for %s", r.URL)
		}
		return httpResponse(r, http.StatusOK, nil, []byte(releaseBody(assetURL, "", 80))), nil
	})
	_, err := u.PrepareDownload(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "SHA-256") || !strings.Contains(err.Error(), "手动下载") {
		t.Fatalf("err %v", err)
	}
}

func TestApplyRejectsForeignRedirect(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ProxyBox.exe")
	original := peBytes(0x11, 80)
	if err := os.WriteFile(exe, original, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := peBytes(0x22, 96)
	assetURL := "https://github.com/zhaoyangplus/proxy-box/releases/download/v2.4.0/ProxyBox.exe"
	u := New("v1.0.0")
	u.binaryPath = exe
	u.spawnFn = func(string) error {
		t.Fatal("spawned after a refused redirect")
		return nil
	}
	u.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.github.com":
			return httpResponse(r, http.StatusOK, nil, []byte(releaseBody(assetURL, digestOf(payload), len(payload)))), nil
		case "github.com":
			hdr := http.Header{"Location": []string{"https://evil.example/ProxyBox.exe"}}
			return httpResponse(r, http.StatusFound, hdr, nil), nil
		default:
			return nil, errors.New("unexpected host " + r.URL.Host)
		}
	})
	asset, err := u.PrepareDownload(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	err = u.DownloadAndApply(context.Background(), asset, "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "evil.example") {
		t.Fatalf("err %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatal("current executable changed")
	}
}

func TestApplyDigestMismatchLeavesBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ProxyBox.exe")
	original := peBytes(0x11, 80)
	if err := os.WriteFile(exe, original, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := peBytes(0x22, 96)
	assetURL := "https://github.com/zhaoyangplus/proxy-box/releases/download/v2.4.0/ProxyBox.exe"
	u := New("v1.0.0")
	u.binaryPath = exe
	u.spawnFn = func(string) error {
		t.Fatal("spawned after a bad digest")
		return nil
	}
	u.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.github.com":
			return httpResponse(r, http.StatusOK, nil, []byte(releaseBody(assetURL, digestOf([]byte("other")), len(payload)))), nil
		case "github.com":
			return httpResponse(r, http.StatusOK, nil, payload), nil
		default:
			return nil, errors.New("unexpected host " + r.URL.Host)
		}
	})
	asset, err := u.PrepareDownload(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	err = u.DownloadAndApply(context.Background(), asset, "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("err %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatal("current executable changed")
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Fatalf("backup replaced the current executable: %v", err)
	}
}

func TestApplyHappyPath(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ProxyBox.exe")
	original := peBytes(0x11, 80)
	if err := os.WriteFile(exe, original, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := peBytes(0x33, 128)
	assetURL := "https://github.com/zhaoyangplus/proxy-box/releases/download/v2.4.0/ProxyBox.exe"
	cdnURL := "https://release-assets.githubusercontent.com/github-production-release-asset/ProxyBox.exe"
	var spawned string
	u := New("v1.0.0")
	u.binaryPath = exe
	u.spawnFn = func(path string) error {
		spawned = path
		return nil
	}
	u.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.github.com":
			return httpResponse(r, http.StatusOK, nil, []byte(releaseBody(assetURL, digestOf(payload), len(payload)))), nil
		case "github.com":
			hdr := http.Header{"Location": []string{cdnURL}}
			return httpResponse(r, http.StatusFound, hdr, nil), nil
		case "release-assets.githubusercontent.com":
			return httpResponse(r, http.StatusOK, nil, payload), nil
		default:
			return nil, errors.New("unexpected host " + r.URL.Host)
		}
	})
	asset, err := u.PrepareDownload(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := u.DownloadAndApply(context.Background(), asset, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("installed bytes do not match the release asset")
	}
	if spawned != exe {
		t.Fatalf("spawned %q", spawned)
	}
}
