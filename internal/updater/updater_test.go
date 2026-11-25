package updater

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
