// Package subscription downloads a subscription and writes providers/<id>.txt.
package subscription

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	"proxy-box/internal/configstore"
)

const (
	defaultUA      = "clash.meta"
	maxBody        = 16 << 20
	requestTimeout = 20 * time.Second
)

var idPattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

// UpdateResult is a successful download.
type UpdateResult struct {
	NodeCount int
	Usage     *configstore.SubUsage
}

// Update downloads sub.URL and writes providersDir/<id>.txt.
// It does not apply sub.Filter. mihomo does that.
// A failed update leaves the previous file unchanged.
func Update(ctx context.Context, sub configstore.Subscription, proxyURLs []string, providersDir string) (UpdateResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateID(sub.ID); err != nil {
		return UpdateResult{}, err
	}
	if strings.TrimSpace(providersDir) == "" {
		return UpdateResult{}, errors.New("providers dir is empty")
	}
	rawURL, err := parseSubURL(sub.URL)
	if err != nil {
		return UpdateResult{}, err
	}
	ua := strings.TrimSpace(sub.UserAgent)
	if ua == "" {
		ua = defaultUA
	}
	body, header, err := download(ctx, rawURL, ua, proxyURLs)
	if err != nil {
		return UpdateResult{}, err
	}
	out, n, err := normalize(body)
	if err != nil {
		return UpdateResult{}, err
	}
	path := filepath.Join(providersDir, sub.ID+".txt")
	if err := configstore.WriteAtomic(path, out); err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{NodeCount: n, Usage: parseUsage(header)}, nil
}

func validateID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("subscription id %q", id)
	}
	return nil
}

func parseSubURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("subscription url %q", raw)
	}
	return u.String(), nil
}

func download(ctx context.Context, rawURL, ua string, proxyURLs []string) ([]byte, http.Header, error) {
	var proxies []string
	for _, proxy := range proxyURLs {
		proxy = strings.TrimSpace(proxy)
		if proxy != "" {
			proxies = append(proxies, proxy)
		}
	}
	for _, proxy := range proxies {
		body, header, err := fetch(ctx, rawURL, ua, proxy)
		if err == nil {
			return body, header, nil
		}
		if !canTryNext(err) {
			return nil, nil, err
		}
	}
	return fetch(ctx, rawURL, ua, "")
}

func fetch(ctx context.Context, rawURL, ua, proxy string) ([]byte, http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", ua)

	transport := &http.Transport{
		Proxy:             func(*http.Request) (*url.URL, error) { return nil, nil },
		DisableKeepAlives: true,
	}
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil || u.Scheme == "" || u.Host == "" {
			if err == nil {
				err = errors.New("missing host")
			}
			return nil, nil, err
		}
		transport.Proxy = http.ProxyURL(u)
	}
	defer transport.CloseIdleConnections()

	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, &statusError{code: resp.StatusCode, viaProxy: proxy != ""}
	}
	body, err := readLimited(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	return body, resp.Header.Clone(), nil
}

func readLimited(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBody {
		return nil, &bodyTooLarge{}
	}
	return body, nil
}

type statusError struct {
	code     int
	viaProxy bool
}

func (e *statusError) Error() string {
	return fmt.Sprintf("subscription status %d", e.code)
}

type bodyTooLarge struct{}

func (*bodyTooLarge) Error() string { return "subscription body exceeds 16MB" }

func canTryNext(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var status *statusError
	if errors.As(err, &status) {
		switch status.code {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusProxyAuthRequired:
			return status.viaProxy
		default:
			return false
		}
	}
	var big *bodyTooLarge
	if errors.As(err, &big) {
		return false
	}
	return true
}

func parseUsage(header http.Header) *configstore.SubUsage {
	if header == nil {
		return nil
	}
	raw := strings.TrimSpace(header.Get("subscription-userinfo"))
	if raw == "" {
		return nil
	}
	var usage configstore.SubUsage
	parsed := false
	for _, part := range strings.Split(raw, ";") {
		key, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
		if err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "upload":
			usage.Upload = n
		case "download":
			usage.Download = n
		case "total":
			usage.Total = n
		case "expire":
			usage.Expire = n
		default:
			continue
		}
		parsed = true
	}
	if !parsed {
		return nil
	}
	return &usage
}

// normalize keeps Clash YAML with a non-empty proxies list.
// Every other accepted body becomes one line of standard base64.
func normalize(body []byte) ([]byte, int, error) {
	body = bytes.TrimPrefix(body, []byte("\xEF\xBB\xBF"))
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, 0, errors.New("subscription body is empty")
	}
	if n, ok := clashProxies(body); ok {
		return bytes.Clone(body), n, nil
	}
	if htmlPage(body) {
		return nil, 0, errors.New("subscription body is html")
	}
	if decoded, ok := decodeBlob(stripSpace(body)); ok {
		if htmlPage(decoded) {
			return nil, 0, errors.New("subscription body is html")
		}
		if n := schemeCount(decoded); n > 0 {
			return []byte(base64.StdEncoding.EncodeToString(decoded)), n, nil
		}
	}
	text, n := plainText(body)
	if n == 0 {
		return nil, 0, errors.New("subscription has no nodes")
	}
	return []byte(base64.StdEncoding.EncodeToString([]byte(text))), n, nil
}

func clashProxies(body []byte) (int, bool) {
	var root yaml.Node
	if err := yaml.Unmarshal(body, &root); err != nil {
		return 0, false
	}
	doc := &root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return 0, false
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key, val := doc.Content[i], doc.Content[i+1]
		if key.Value == "proxies" && val.Kind == yaml.SequenceNode && len(val.Content) > 0 {
			return len(val.Content), true
		}
	}
	return 0, false
}

func htmlPage(body []byte) bool {
	head := bytes.TrimSpace(body)
	if len(head) == 0 {
		return false
	}
	if len(head) > 1024 {
		head = head[:1024]
	}
	lower := bytes.ToLower(head)
	if bytes.HasPrefix(lower, []byte("<!doctype")) ||
		bytes.HasPrefix(lower, []byte("<html")) ||
		bytes.HasPrefix(lower, []byte("<head")) ||
		bytes.HasPrefix(lower, []byte("<body")) {
		return true
	}
	return bytes.Contains(lower, []byte("<html")) || bytes.Contains(lower, []byte("<!doctype"))
}

func stripSpace(body []byte) string {
	var bld strings.Builder
	bld.Grow(len(body))
	for _, r := range string(body) {
		if !unicode.IsSpace(r) {
			bld.WriteRune(r)
		}
	}
	return bld.String()
}

func decodeBlob(s string) ([]byte, bool) {
	if s == "" || !base64Alphabet(s) {
		return nil, false
	}
	std := strings.NewReplacer("-", "+", "_", "/").Replace(s)
	std = strings.TrimRight(std, "=")
	if std == "" || len(std)%4 == 1 {
		return nil, false
	}
	if pad := len(std) % 4; pad != 0 {
		std += strings.Repeat("=", 4-pad)
	}
	out, err := base64.StdEncoding.DecodeString(std)
	if err != nil || len(out) == 0 {
		return nil, false
	}
	return out, true
}

func base64Alphabet(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '+' || r == '/' || r == '-' || r == '_' || r == '=':
		default:
			return false
		}
	}
	return true
}

func splitLines(body []byte) []string {
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	parts := strings.Split(text, "\n")
	lines := make([]string, 0, len(parts))
	for _, line := range parts {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func schemeCount(body []byte) int {
	n := 0
	for _, line := range splitLines(body) {
		if knownScheme(line) {
			n++
		}
	}
	return n
}

func plainText(body []byte) (string, int) {
	lines := splitLines(body)
	n := 0
	for _, line := range lines {
		if knownScheme(line) {
			n++
		}
	}
	return strings.Join(lines, "\n"), n
}

func knownScheme(line string) bool {
	scheme, _, ok := strings.Cut(line, "://")
	if !ok || scheme == "" {
		return false
	}
	switch strings.ToLower(scheme) {
	case "vmess", "vless", "ss", "ssr", "trojan", "hysteria2", "hy2", "tuic", "socks5", "socks", "http", "https", "anytls":
		return true
	default:
		return false
	}
}
