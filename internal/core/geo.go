package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	GeoIPFile   = "geoip.metadb"
	GeoSiteFile = "GeoSite.dat"

	// Real MetaCubeX releases are about 8.5 MB and 4.3 MB. A megabyte rejects HTML error pages and empty files.
	minGeoIPBytes   = 1 << 20
	minGeoSiteBytes = 1 << 20
	maxGeoBytes     = 64 << 20

	// The GitHub URLs are compiled into mihomo v1.19.34. jsDelivr serves the same two release files.
	geoIPURL      = "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.metadb"
	geoSiteURL    = "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat"
	geoIPMirror   = "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geoip.metadb"
	geoSiteMirror = "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geosite.dat"
)

var (
	ErrGeoHTML      = errors.New("geo file is html")
	ErrGeoTooSmall  = errors.New("geo file is too small")
	ErrGeoPreflight = errors.New("geo preflight failed")
)

// GeoFile is one database DownloadGeo should fetch.
// URLs are tried in order. MinBytes rejects a short or empty body.
type GeoFile struct {
	Name     string
	URLs     []string
	MinBytes int64
}

func defaultGeoFiles() []GeoFile {
	return []GeoFile{
		{Name: GeoIPFile, URLs: []string{geoIPURL, geoIPMirror}, MinBytes: minGeoIPBytes},
		{Name: GeoSiteFile, URLs: []string{geoSiteURL, geoSiteMirror}, MinBytes: minGeoSiteBytes},
	}
}

// GeoFilesReady reports whether both geo databases are present and large enough to load.
func GeoFilesReady(coreDir string) bool {
	if strings.TrimSpace(coreDir) == "" {
		return false
	}
	for _, file := range defaultGeoFiles() {
		if err := rejectGeo(filepath.Join(coreDir, file.Name), file.MinBytes); err != nil {
			return false
		}
	}
	return true
}

// DownloadGeo fetches each file through proxyURL (the core mixed port) into coreDir.
// A partial download stays in name.tmp and the next attempt of that URL resumes it.
// The final name is replaced only after the size and HTML checks pass.
// A nil files slice uses the MetaCubeX release URLs.
func DownloadGeo(ctx context.Context, coreDir, proxyURL string, files []GeoFile, onProgress ProgressFunc) error {
	if strings.TrimSpace(coreDir) == "" {
		return errors.New("geo download: empty dir")
	}
	if err := os.MkdirAll(coreDir, 0755); err != nil {
		return err
	}
	if len(files) == 0 {
		files = defaultGeoFiles()
	}
	var completed int64
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := downloadGeoFile(ctx, coreDir, proxyURL, file, func(got, tot int64) {
			if onProgress == nil {
				return
			}
			all := int64(0)
			if tot > 0 {
				all = completed + tot
			}
			onProgress(completed+got, all)
		})
		if err != nil {
			if errors.Is(err, ErrGeoHTML) || errors.Is(err, ErrGeoTooSmall) || errors.Is(err, context.Canceled) {
				return err
			}
			return fmt.Errorf("geo download: %w", err)
		}
		completed += n
	}
	return nil
}

func downloadGeoFile(ctx context.Context, coreDir, proxyURL string, file GeoFile, onProgress ProgressFunc) (int64, error) {
	name := file.Name
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return 0, errors.New("geo download: bad name")
	}
	if len(file.URLs) == 0 || file.MinBytes <= 0 {
		return 0, errors.New("geo download: bad spec")
	}
	dest := filepath.Join(coreDir, name)
	tmp := dest + ".tmp"
	var last error
	for i, raw := range file.URLs {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if i > 0 {
			_ = os.Remove(tmp)
		}
		err := fetchURL(ctx, fetchSpec{
			dest:          tmp,
			url:           raw,
			proxyURL:      proxyURL,
			maxBytes:      maxGeoBytes,
			fallbackTotal: 0,
		}, onProgress)
		if err != nil {
			last = err
			continue
		}
		if err := rejectGeo(tmp, file.MinBytes); err != nil {
			_ = os.Remove(tmp)
			last = err
			continue
		}
		if err := moveReplace(tmp, dest); err != nil {
			_ = os.Remove(tmp)
			return 0, err
		}
		return fileSize(dest), nil
	}
	_ = os.Remove(tmp)
	if last == nil {
		last = errors.New("geo download failed")
	}
	return 0, last
}

func rejectGeo(path string, minBytes int64) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() || st.Size() < minBytes {
		return ErrGeoTooSmall
	}
	if fileLooksLikeHTML(path) {
		return ErrGeoHTML
	}
	return nil
}

func fileLooksLikeHTML(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	if n == 0 {
		return false
	}
	s := strings.ToLower(strings.TrimLeft(string(buf[:n]), " \t\r\n\x00"))
	return strings.HasPrefix(s, "<!doctype") ||
		strings.HasPrefix(s, "<html") ||
		strings.HasPrefix(s, "<head") ||
		strings.HasPrefix(s, "<body")
}

// PreflightGeo runs `mihomo -t` against a copy of the geo files and configYAML.
// mihomo v1.19.34 deletes an unreadable geo file, downloads a replacement, and still exits 0.
// The copied config sends that download to a closed port, so a bad file fails here and the live directory is left untouched.
func PreflightGeo(ctx context.Context, binaryPath, coreDir, configYAML string) error {
	if strings.TrimSpace(binaryPath) == "" || strings.TrimSpace(coreDir) == "" {
		return fmt.Errorf("%w: missing binary or directory", ErrGeoPreflight)
	}
	tmp, err := os.MkdirTemp("", "proxybox-geo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, name := range []string{GeoIPFile, GeoSiteFile} {
		if err := copyGeoFile(filepath.Join(coreDir, name), filepath.Join(tmp, name)); err != nil {
			return fmt.Errorf("%w: %s", ErrGeoPreflight, name)
		}
	}
	if err := copyFlatDir(filepath.Join(coreDir, "providers"), filepath.Join(tmp, "providers")); err != nil {
		return fmt.Errorf("%w: %s", ErrGeoPreflight, err.Error())
	}
	cfg := strings.TrimRight(configYAML, "\r\n") + "\n" + geoPreflightURLs
	cfgPath := filepath.Join(tmp, "preflight.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0644); err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := commandHidden(runCtx, binaryPath, "-t", "-d", tmp, "-f", cfgPath)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	text := out.String()
	if runErr != nil ||
		!strings.Contains(text, "test is successful") ||
		!strings.Contains(text, "Finished initial GeoSite rule cn") ||
		strings.Contains(text, "invalid") ||
		strings.Contains(text, "start download") {
		return fmt.Errorf("%w: %s", ErrGeoPreflight, geoFailLine(text))
	}
	return nil
}

const geoPreflightURLs = `geox-url:
  geoip: 'http://127.0.0.1:1/geoip.dat'
  geosite: 'http://127.0.0.1:1/geosite.dat'
  mmdb: 'http://127.0.0.1:1/geoip.metadb'
  asn: 'http://127.0.0.1:1/asn.mmdb'
`

func geoFailLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if len(line) > 180 {
			return line[:180]
		}
		return line
	}
	return "mihomo -t failed"
}

func copyGeoFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	return errors.Join(copyErr, closeErr)
}

func copyFlatDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := copyGeoFile(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
