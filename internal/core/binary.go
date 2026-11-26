package core

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const (
	AssetName   = "mihomo-windows-amd64-compatible-v1.19.34.zip"
	AssetSHA256 = "cd6738d4648cc1a5f48049a1343b697ab510889d648f045c5c780fc1ab443a79"
	AssetURL    = "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.34/" + AssetName

	BinaryFileName = "mihomo.exe"
	ConfigFileName = "config.yaml"
	LogFileName    = "mihomo.log"

	maxAssetBytes = 64 << 20
	maxEXEBytes   = 96 << 20
)

var (
	ErrHashMismatch = errors.New("sha256 mismatch")
	ErrNoPE         = errors.New("file is not a PE executable")

	currentExecutable = os.Executable
	assetURL          = AssetURL
)

// ProgressFunc reports download bytes. total is -1 when the server omits the length.
type ProgressFunc func(downloaded, total int64)

// EnsureBinary returns a mihomo.exe to run.
// It checks beside the current executable, then %LOCALAPPDATA%\ProxyBox\core\mihomo.exe,
// then each reachable proxy URL, then a direct download.
// A downloaded zip must match AssetSHA256 and the exe inside must start with MZ.
func EnsureBinary(ctx context.Context, coreDir string, proxyURLs []string, onProgress ProgressFunc) (string, error) {
	if p, ok := siblingBinary(); ok {
		return p, nil
	}
	if p, ok := localAppDataBinary(); ok {
		return p, nil
	}
	if err := os.MkdirAll(coreDir, 0755); err != nil {
		return "", err
	}
	var attempt []error
	for _, proxy := range proxyURLs {
		if strings.TrimSpace(proxy) == "" {
			continue
		}
		if !proxyReachable(ctx, proxy) {
			attempt = append(attempt, fmt.Errorf("%s: unreachable", proxy))
			continue
		}
		path, err := downloadAndInstall(ctx, coreDir, proxy, onProgress)
		if err == nil {
			return path, nil
		}
		if errors.Is(err, ErrHashMismatch) {
			return "", err
		}
		attempt = append(attempt, fmt.Errorf("%s: %w", proxy, err))
	}
	path, err := downloadAndInstall(ctx, coreDir, "", onProgress)
	if err != nil {
		return "", errors.Join(append(attempt, err)...)
	}
	return path, nil
}

// ImportBinary copies a user-chosen .exe or .zip into coreDir/mihomo.exe after checking MZ.
func ImportBinary(srcPath, coreDir string) (string, error) {
	if err := os.MkdirAll(coreDir, 0755); err != nil {
		return "", err
	}
	dest := filepath.Join(coreDir, BinaryFileName)
	switch strings.ToLower(filepath.Ext(srcPath)) {
	case ".exe":
		f, err := os.Open(srcPath)
		if err != nil {
			return "", err
		}
		defer f.Close()
		if err := installEXE(dest, f); err != nil {
			return "", err
		}
	case ".zip":
		if err := extractMihomo(srcPath, dest); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("import %s: want .exe or .zip", filepath.Base(srcPath))
	}
	abs, err := filepath.Abs(dest)
	if err != nil {
		return dest, nil
	}
	return abs, nil
}

func siblingBinary() (string, bool) {
	exe, err := currentExecutable()
	if err != nil {
		return "", false
	}
	return validLocalBinary(filepath.Join(filepath.Dir(exe), BinaryFileName))
}

func localAppDataBinary() (string, bool) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return "", false
	}
	return validLocalBinary(filepath.Join(base, "ProxyBox", "core", BinaryFileName))
}

func validLocalBinary(path string) (string, bool) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() < 2 {
		return "", false
	}
	if !fileHasMZ(path) {
		return "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path, true
	}
	return abs, true
}

func proxyReachable(ctx context.Context, proxyURL string) bool {
	u, err := parseProxy(proxyURL)
	if err != nil || u.Host == "" {
		return false
	}
	dialCtx, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", u.Host)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func parseProxy(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty proxy")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	return url.Parse(raw)
}

func downloadAndInstall(ctx context.Context, coreDir, proxyURL string, onProgress ProgressFunc) (string, error) {
	zipPath := filepath.Join(coreDir, AssetName+".tmp")
	attemptCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := downloadAsset(attemptCtx, zipPath, proxyURL, onProgress); err != nil {
		_ = os.Remove(zipPath)
		return "", err
	}
	defer os.Remove(zipPath)
	dest := filepath.Join(coreDir, BinaryFileName)
	if err := extractMihomo(zipPath, dest); err != nil {
		return "", err
	}
	return filepath.Abs(dest)
}

func downloadAsset(ctx context.Context, dest, proxyURL string, onProgress ProgressFunc) error {
	// net/http stalls mid-body on this machine's GitHub TLS path. curl.exe finishes it.
	if assetURL == AssetURL {
		err := downloadWithCurl(ctx, dest, proxyURL, onProgress)
		if err == nil {
			return verifyAssetHash(dest)
		}
		_ = os.Remove(dest)
		if !errors.Is(err, exec.ErrNotFound) {
			return err
		}
	}
	client, err := httpClient(proxyURL)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "proxy-box")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download http %d", resp.StatusCode)
	}
	total := resp.ContentLength
	if total < 0 {
		total = -1
	}
	if onProgress != nil {
		onProgress(0, total)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	src := &progressReader{r: io.TeeReader(resp.Body, hash), total: total, fn: onProgress}
	n, err := io.Copy(f, io.LimitReader(src, maxAssetBytes+1))
	if err != nil {
		return err
	}
	if n > maxAssetBytes {
		return fmt.Errorf("asset exceeds %d bytes", maxAssetBytes)
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return hashMatches(hash.Sum(nil))
}

func downloadWithCurl(ctx context.Context, dest, proxyURL string, onProgress ProgressFunc) error {
	curl, err := exec.LookPath("curl.exe")
	if err != nil {
		return err
	}
	args := []string{"-L", "--fail", "--retry", "2", "-A", "proxy-box", "-o", dest}
	if proxyURL != "" {
		args = append(args, "-x", proxyURL)
	}
	args = append(args, assetURL)
	cmd := commandHidden(ctx, curl, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			reportFile(dest, onProgress)
			if err != nil {
				return fmt.Errorf("curl: %w: %s", err, strings.TrimSpace(stderr.String()))
			}
			return nil
		case <-tick.C:
			reportFile(dest, onProgress)
		}
	}
}

func reportFile(path string, onProgress ProgressFunc) {
	if onProgress == nil {
		return
	}
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	onProgress(st.Size(), -1)
}

func verifyAssetHash(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	return hashMatches(h.Sum(nil))
}

func hashMatches(sum []byte) error {
	got := hex.EncodeToString(sum)
	if !strings.EqualFold(got, AssetSHA256) {
		return fmt.Errorf("%w: got %s", ErrHashMismatch, got)
	}
	return nil
}

func httpClient(proxyURL string) (*http.Client, error) {
	tr := &http.Transport{
		Proxy:               nil,
		DisableCompression:  true,
		ForceAttemptHTTP2:   false,
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
		TLSHandshakeTimeout: 20 * time.Second,
	}
	if proxyURL != "" {
		u, err := parseProxy(proxyURL)
		if err != nil {
			return nil, err
		}
		tr.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: tr}, nil
}

type progressReader struct {
	r     io.Reader
	n     int64
	total int64
	next  int64
	fn    ProgressFunc
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.r.Read(buf)
	p.n += int64(n)
	if p.fn != nil && (err == io.EOF || p.n >= p.next) {
		p.fn(p.n, p.total)
		p.next = p.n + 256*1024
	}
	return n, err
}

func extractMihomo(zipPath, dest string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	chosen, err := chooseEXE(zr.File)
	if err != nil {
		return err
	}
	rc, err := chosen.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return installEXE(dest, rc)
}

func chooseEXE(files []*zip.File) (*zip.File, error) {
	var chosen *zip.File
	exes := 0
	for _, f := range files {
		if f.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(f.Name)
		if !strings.HasSuffix(strings.ToLower(base), ".exe") {
			continue
		}
		exes++
		if strings.Contains(strings.ToLower(base), "mihomo") {
			return f, nil
		}
		if chosen == nil {
			chosen = f
		}
	}
	if chosen == nil {
		return nil, fmt.Errorf("zip contains no executable")
	}
	if exes > 1 {
		return nil, fmt.Errorf("zip contains %d executables", exes)
	}
	return chosen, nil
}

func installEXE(dest string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	tmp := dest + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(f, io.LimitReader(r, maxEXEBytes+1))
	syncErr := f.Sync()
	closeErr := f.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(tmp)
		return errors.Join(copyErr, syncErr, closeErr)
	}
	if n > maxEXEBytes {
		_ = os.Remove(tmp)
		return fmt.Errorf("executable exceeds %d bytes", maxEXEBytes)
	}
	if !fileHasMZ(tmp) {
		_ = os.Remove(tmp)
		return ErrNoPE
	}
	if err := moveReplace(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func fileHasMZ(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [2]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	return magic[0] == 'M' && magic[1] == 'Z'
}

func moveReplace(src, dest string) error {
	from, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(dest)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func writeFileAtomic(dest string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	tmp := dest + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(tmp)
		return errors.Join(writeErr, syncErr, closeErr)
	}
	if err := moveReplace(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
