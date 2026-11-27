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
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
)

const (
	AssetName   = "mihomo-windows-amd64-compatible-v1.19.34.zip"
	AssetSHA256 = "cd6738d4648cc1a5f48049a1343b697ab510889d648f045c5c780fc1ab443a79"
	AssetSize   = 22417383
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
	lookupCurl        = func() (string, error) { return exec.LookPath("curl.exe") }
	downloadStall     = 30 * time.Second
	downloadOverall   = 15 * time.Minute
	downloadAttempts  = 8
)

var downloadResumes atomic.Int64

// ProgressFunc reports bytes written. total is the response length, or AssetSize when the response omits it.
type ProgressFunc func(downloaded, total int64)

// EnsureBinary returns a mihomo.exe to run.
// It checks beside the current executable, then coreDir/mihomo.exe,
// then %LOCALAPPDATA%\ProxyBox\core\mihomo.exe, then each reachable proxy URL,
// then a direct download.
// A downloaded zip must match AssetSHA256 and the exe inside must start with MZ.
func EnsureBinary(ctx context.Context, coreDir string, proxyURLs []string, onProgress ProgressFunc) (string, error) {
	if p, ok := siblingBinary(); ok {
		return p, nil
	}
	if p, ok := validLocalBinary(filepath.Join(coreDir, BinaryFileName)); ok {
		return p, nil
	}
	if p, ok := localAppDataBinary(); ok {
		return p, nil
	}
	if err := os.MkdirAll(coreDir, 0755); err != nil {
		return "", err
	}
	downloadResumes.Store(0)
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
	if err := downloadAsset(ctx, zipPath, proxyURL, onProgress); err != nil {
		if errors.Is(err, ErrHashMismatch) || fileSize(zipPath) > maxAssetBytes {
			_ = os.Remove(zipPath)
		}
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
	if err := fetchAsset(ctx, dest, proxyURL, onProgress); err != nil {
		return err
	}
	return verifyAssetHash(dest)
}

// fetchSpec is one resumable HTTP download. fallbackTotal is reported until the response has a length.
type fetchSpec struct {
	dest          string
	url           string
	proxyURL      string
	maxBytes      int64
	fallbackTotal int64
}

func fetchAsset(ctx context.Context, dest, proxyURL string, onProgress ProgressFunc) error {
	return fetchURL(ctx, fetchSpec{
		dest:          dest,
		url:           assetURL,
		proxyURL:      proxyURL,
		maxBytes:      maxAssetBytes,
		fallbackTotal: AssetSize,
	}, onProgress)
}

func fetchURL(ctx context.Context, spec fetchSpec, onProgress ProgressFunc) error {
	ctx, cancel := context.WithTimeout(ctx, downloadOverall)
	defer cancel()
	var total atomic.Int64
	total.Store(spec.fallbackTotal)
	report := func() {
		if onProgress != nil {
			onProgress(fileSize(spec.dest), total.Load())
		}
	}
	curl, err := lookupCurl()
	// curl.exe is preferred because its TLS stack finishes the release asset when net/http stops mid-body.
	if err != nil && !errors.Is(err, exec.ErrNotFound) {
		return err
	}
	report()
	if err == nil {
		return downloadWithRetry(ctx, spec.dest, spec.maxBytes, report, func(ctx context.Context, _ int64) error {
			return curlAttempt(ctx, curl, spec.dest, spec.proxyURL, spec.url)
		})
	}
	return downloadWithRetry(ctx, spec.dest, spec.maxBytes, report, func(ctx context.Context, have int64) error {
		return httpAttempt(ctx, spec, have, &total)
	})
}

func downloadWithRetry(ctx context.Context, dest string, maxBytes int64, report func(), once func(context.Context, int64) error) error {
	var last error
	for attempt := 1; attempt <= downloadAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		have := fileSize(dest)
		if have > maxBytes {
			return fmt.Errorf("asset exceeds %d bytes", maxBytes)
		}
		if have > 0 {
			downloadResumes.Add(1)
		}
		err := watchAttempt(ctx, dest, maxBytes, report, func(ctx context.Context) error {
			return once(ctx, have)
		})
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		last = err
	}
	if last == nil {
		return errors.New("download failed")
	}
	return last
}

func watchAttempt(ctx context.Context, dest string, maxBytes int64, report func(), run func(context.Context) error) error {
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(attemptCtx) }()
	tick := time.NewTicker(stallPoll(downloadStall))
	defer tick.Stop()
	last := fileSize(dest)
	changed := time.Now()
	for {
		select {
		case err := <-done:
			report()
			return err
		case <-ctx.Done():
			cancel()
			<-done
			report()
			return ctx.Err()
		case <-tick.C:
			sz := fileSize(dest)
			if sz != last {
				last = sz
				changed = time.Now()
			}
			report()
			if sz > maxBytes {
				cancel()
				<-done
				return fmt.Errorf("asset exceeds %d bytes", maxBytes)
			}
			if time.Since(changed) >= downloadStall {
				cancel()
				<-done
				return fmt.Errorf("download stalled after %s", downloadStall)
			}
		}
	}
}

func stallPoll(stall time.Duration) time.Duration {
	poll := stall / 4
	if poll < 10*time.Millisecond {
		poll = 10 * time.Millisecond
	}
	if poll > 100*time.Millisecond {
		poll = 100 * time.Millisecond
	}
	return poll
}

func curlAttempt(ctx context.Context, curl, dest, proxyURL, rawURL string) error {
	args := []string{"-sS", "-L", "--fail", "-C", "-", "-A", "proxy-box", "-o", dest, "-x", proxyURL}
	if proxyURL != "" {
		args = append(args, "--noproxy", "")
	}
	args = append(args, rawURL)
	cmd := commandHidden(ctx, curl, args...)
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("curl: %w", err)
		}
		return fmt.Errorf("curl: %w: %s", err, msg)
	}
	return nil
}

func httpAttempt(ctx context.Context, spec fetchSpec, have int64, total *atomic.Int64) error {
	client, err := httpClient(spec.proxyURL)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "proxy-box")
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	restart := resp.StatusCode == http.StatusOK
	switch resp.StatusCode {
	case http.StatusOK:
		have = 0
	case http.StatusPartialContent:
	default:
		return fmt.Errorf("download http %d", resp.StatusCode)
	}
	total.Store(responseTotal(resp, have, spec.fallbackTotal))
	flag := os.O_CREATE | os.O_WRONLY
	if restart {
		flag |= os.O_TRUNC
	} else {
		flag |= os.O_APPEND
	}
	f, err := os.OpenFile(spec.dest, flag, 0644)
	if err != nil {
		return err
	}
	limit := spec.maxBytes - have
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	syncErr := f.Sync()
	closeErr := f.Close()
	if n > limit {
		return fmt.Errorf("asset exceeds %d bytes", spec.maxBytes)
	}
	if copyErr != nil {
		return copyErr
	}
	return errors.Join(syncErr, closeErr)
}

func responseTotal(resp *http.Response, already, fallback int64) int64 {
	if n, ok := contentRangeSize(resp.Header.Get("Content-Range")); ok {
		return n
	}
	if resp.ContentLength < 0 {
		return fallback
	}
	if resp.StatusCode == http.StatusPartialContent {
		return already + resp.ContentLength
	}
	return resp.ContentLength
}

func contentRangeSize(header string) (int64, bool) {
	_, size, ok := strings.Cut(header, "/")
	if !ok || size == "" || size == "*" {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
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
