package updater

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

const (
	DefaultOwner     = "zhaoyangplus"
	DefaultRepo      = "proxy-box"
	DefaultAssetName = "ProxyBox.exe"
	defaultAPIBase   = "https://api.github.com"
)

// CheckResult holds metadata about the latest GitHub Release compared to the running build.
type CheckResult struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	HasUpdate      bool   `json:"hasUpdate"`
	ReleaseName    string `json:"releaseName"`
	ReleaseNotes   string `json:"releaseNotes"`
	ReleaseURL     string `json:"releaseUrl"`
	PublishedAt    string `json:"publishedAt"`
	AssetName      string `json:"assetName"`
	AssetSize      int64  `json:"assetSize"`
	DownloadURL    string `json:"downloadUrl"`
	CheckedAt      string `json:"checkedAt"`
}

// DownloadProgress tracks the live state of an in-app update.
type DownloadProgress struct {
	Status          string  `json:"status"` // idle, checking, downloading, applying, restarting, error
	CurrentVersion  string  `json:"currentVersion"`
	TargetVersion   string  `json:"targetVersion"`
	DownloadedBytes int64   `json:"downloadedBytes"`
	TotalBytes      int64   `json:"totalBytes"`
	Percent         float64 `json:"percent"`
	Message         string  `json:"message"`
	Error           string  `json:"error,omitempty"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt string    `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Updater checks GitHub Releases and performs atomic in-place Windows binary upgrades.
type Updater struct {
	Owner          string
	Repo           string
	CurrentVersion string
	AssetName      string
	APIBaseURL     string

	mu         sync.Mutex
	inProgress bool
	progress   DownloadProgress
	lastCheck  *CheckResult
}

func New(currentVersion string) *Updater {
	if strings.TrimSpace(currentVersion) == "" {
		currentVersion = "v1.0.0"
	}
	return &Updater{
		Owner:          DefaultOwner,
		Repo:           DefaultRepo,
		CurrentVersion: currentVersion,
		AssetName:      DefaultAssetName,
		APIBaseURL:     defaultAPIBase,
		progress: DownloadProgress{
			Status:         "idle",
			CurrentVersion: currentVersion,
		},
	}
}

func (u *Updater) RepoURL() string {
	return fmt.Sprintf("https://github.com/%s/%s", u.Owner, u.Repo)
}

func (u *Updater) ReleasesURL() string {
	return fmt.Sprintf("https://github.com/%s/%s/releases", u.Owner, u.Repo)
}

func (u *Updater) GetProgress() DownloadProgress {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.progress
}

func (u *Updater) LastCheck() *CheckResult {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.lastCheck == nil {
		return nil
	}
	cp := *u.lastCheck
	return &cp
}

func (u *Updater) setProgress(p DownloadProgress, cb func(DownloadProgress)) {
	u.mu.Lock()
	u.progress = p
	u.mu.Unlock()
	if cb != nil {
		cb(p)
	}
}

// CleanupOldBinary removes leftover .old / .new artifacts created by a previous self-update.
func CleanupOldBinary() {
	exePath, err := resolveExecutablePath()
	if err != nil || exePath == "" {
		return
	}
	for _, suffix := range []string{".old", ".new"} {
		candidate := exePath + suffix
		if _, err := os.Stat(candidate); err == nil {
			_ = os.Remove(candidate)
		}
	}
}

// CheckLatest queries GitHub Releases API for the latest published release.
// If proxyURL is provided, it tries through that proxy first and falls back to direct/env proxy on transport failure.
func (u *Updater) CheckLatest(ctx context.Context, proxyURL string) (*CheckResult, error) {
	apiBase := strings.TrimRight(u.APIBaseURL, "/")
	if apiBase == "" {
		apiBase = defaultAPIBase
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases/latest", apiBase, u.Owner, u.Repo)

	resp, err := u.doGetWithFallback(ctx, endpoint, proxyURL, 15*time.Second, true)
	if err != nil {
		return nil, fmt.Errorf("检查更新失败（无法连接 GitHub）: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("仓库尚未发布任何 Release 版本")
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("GitHub API 请求频率受限（HTTP %d），请稍后重试或切换代理节点", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GitHub API 返回异常（HTTP %d）: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析 Release 信息失败: %w", err)
	}

	asset := selectWindowsAsset(rel.Assets, u.AssetName)
	releaseName := strings.TrimSpace(rel.Name)
	if releaseName == "" {
		releaseName = rel.TagName
	}
	releaseURL := rel.HTMLURL
	if releaseURL == "" {
		releaseURL = u.ReleasesURL()
	}

	res := &CheckResult{
		CurrentVersion: u.CurrentVersion,
		LatestVersion:  strings.TrimSpace(rel.TagName),
		HasUpdate:      CompareVersions(u.CurrentVersion, rel.TagName) < 0,
		ReleaseName:    releaseName,
		ReleaseNotes:   strings.TrimSpace(rel.Body),
		ReleaseURL:     releaseURL,
		PublishedAt:    rel.PublishedAt,
		AssetName:      asset.Name,
		AssetSize:      asset.Size,
		DownloadURL:    asset.BrowserDownloadURL,
		CheckedAt:      time.Now().Format("15:04:05"),
	}

	u.mu.Lock()
	cp := *res
	u.lastCheck = &cp
	u.mu.Unlock()

	return res, nil
}

// DownloadAndApply downloads the new executable, verifies its PE header and size,
// atomically swaps it with the running binary, invokes onBeforeRestart, and spawns the new process.
func (u *Updater) DownloadAndApply(
	ctx context.Context,
	downloadURL string,
	targetVersion string,
	expectedSize int64,
	proxyURL string,
	onProgress func(DownloadProgress),
	onBeforeRestart func(),
) error {
	u.mu.Lock()
	if u.inProgress {
		u.mu.Unlock()
		return errors.New("已有更新任务正在进行中")
	}
	u.inProgress = true
	u.mu.Unlock()

	defer func() {
		u.mu.Lock()
		u.inProgress = false
		u.mu.Unlock()
	}()

	fail := func(err error) error {
		u.setProgress(DownloadProgress{
			Status:         "error",
			CurrentVersion: u.CurrentVersion,
			TargetVersion:  targetVersion,
			Message:        "更新失败",
			Error:          err.Error(),
		}, onProgress)
		return err
	}

	downloadURL = strings.TrimSpace(downloadURL)
	if downloadURL == "" {
		return fail(errors.New("未找到可下载的 Windows 程序附件，请前往 GitHub Releases 手动下载"))
	}

	exePath, err := resolveExecutablePath()
	if err != nil {
		return fail(fmt.Errorf("无法定位当前程序路径: %w", err))
	}

	u.setProgress(DownloadProgress{
		Status:         "downloading",
		CurrentVersion: u.CurrentVersion,
		TargetVersion:  targetVersion,
		TotalBytes:     expectedSize,
		Message:        fmt.Sprintf("正在连接下载源 (%s)...", targetVersion),
	}, onProgress)

	resp, err := u.doGetWithFallback(ctx, downloadURL, proxyURL, 5*time.Minute, false)
	if err != nil {
		return fail(fmt.Errorf("下载更新包失败: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("下载更新包返回 HTTP %d", resp.StatusCode))
	}

	totalBytes := resp.ContentLength
	if totalBytes <= 0 && expectedSize > 0 {
		totalBytes = expectedSize
	}

	newPath := exePath + ".new"
	_ = os.Remove(newPath)
	outFile, err := os.OpenFile(newPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fail(fmt.Errorf("无法在程序目录写入临时文件（请检查目录写权限）: %w", err))
	}

	buf := make([]byte, 32*1024)
	var downloaded int64
	lastEmit := time.Time{}

	for {
		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := outFile.Write(buf[:n]); wErr != nil {
				_ = outFile.Close()
				_ = os.Remove(newPath)
				return fail(fmt.Errorf("写入更新文件失败: %w", wErr))
			}
			downloaded += int64(n)
			if time.Since(lastEmit) >= 120*time.Millisecond {
				pct := 0.0
				if totalBytes > 0 {
					pct = float64(downloaded) * 100 / float64(totalBytes)
					if pct > 99.5 {
						pct = 99.5
					}
				}
				u.setProgress(DownloadProgress{
					Status:          "downloading",
					CurrentVersion:  u.CurrentVersion,
					TargetVersion:   targetVersion,
					DownloadedBytes: downloaded,
					TotalBytes:      totalBytes,
					Percent:         pct,
					Message:         fmt.Sprintf("正在下载 %s (%.1f%%)", u.AssetName, pct),
				}, onProgress)
				lastEmit = time.Now()
			}
		}
		if rErr == io.EOF {
			break
		}
		if rErr != nil {
			_ = outFile.Close()
			_ = os.Remove(newPath)
			return fail(fmt.Errorf("下载中断: %w", rErr))
		}
	}

	if err := outFile.Sync(); err != nil {
		_ = outFile.Close()
		_ = os.Remove(newPath)
		return fail(fmt.Errorf("落盘校验失败: %w", err))
	}
	if err := outFile.Close(); err != nil {
		_ = os.Remove(newPath)
		return fail(err)
	}

	u.setProgress(DownloadProgress{
		Status:          "applying",
		CurrentVersion:  u.CurrentVersion,
		TargetVersion:   targetVersion,
		DownloadedBytes: downloaded,
		TotalBytes:      totalBytes,
		Percent:         99,
		Message:         "下载完成，正在校验文件完整性并替换程序...",
	}, onProgress)

	if err := validatePEFile(newPath, expectedSize); err != nil {
		_ = os.Remove(newPath)
		return fail(err)
	}

	if err := replaceExecutable(exePath, newPath); err != nil {
		_ = os.Remove(newPath)
		return fail(fmt.Errorf("替换可执行文件失败: %w", err))
	}

	u.setProgress(DownloadProgress{
		Status:          "restarting",
		CurrentVersion:  u.CurrentVersion,
		TargetVersion:   targetVersion,
		DownloadedBytes: downloaded,
		TotalBytes:      downloaded,
		Percent:         100,
		Message:         fmt.Sprintf("已升级至 %s，正在重启 Proxy Box...", targetVersion),
	}, onProgress)

	// Allow UI to display restarting status before tearing down local server & window
	time.Sleep(550 * time.Millisecond)

	if onBeforeRestart != nil {
		onBeforeRestart()
	}

	cmd := exec.Command(exePath, "--restarted")
	cmd.Dir = filepath.Dir(exePath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
	if err := cmd.Start(); err != nil {
		return fail(fmt.Errorf("已替换新版本但自动重启失败，请手动重新打开程序: %w", err))
	}
	return nil
}

func (u *Updater) doGetWithFallback(ctx context.Context, rawURL, proxyURL string, timeout time.Duration, isAPI bool) (*http.Response, error) {
	doOnce := func(pURL string) (*http.Response, error) {
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		}
		if pURL != "" {
			if parsed, err := url.Parse(pURL); err == nil {
				tr.Proxy = http.ProxyURL(parsed)
			}
		} else {
			tr.Proxy = http.ProxyFromEnvironment
		}

		client := &http.Client{
			Transport: tr,
			Timeout:   timeout,
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", fmt.Sprintf("ProxyBox/%s", u.CurrentVersion))
		if isAPI {
			req.Header.Set("Accept", "application/vnd.github+json")
			req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		}
		return client.Do(req)
	}

	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL != "" {
		resp, err := doOnce(proxyURL)
		if err == nil {
			return resp, nil
		}
		// Fallback to environment / direct if local proxy is unavailable
		return doOnce("")
	}
	return doOnce("")
}

func resolveExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil && resolved != "" {
		return resolved, nil
	}
	return exe, nil
}

func validatePEFile(path string, expectedSize int64) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("校验更新文件失败: %w", err)
	}
	if info.Size() < 64 {
		return fmt.Errorf("下载文件异常过小（%d 字节），已中止更新", info.Size())
	}
	if expectedSize > 0 && info.Size() != expectedSize {
		return fmt.Errorf("下载文件大小不匹配（预期 %d 字节，实际 %d 字节），请重试", expectedSize, info.Size())
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var magic [2]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return fmt.Errorf("读取更新文件头失败: %w", err)
	}
	if magic[0] != 'M' || magic[1] != 'Z' {
		return errors.New("下载文件不是有效的 Windows 可执行程序（PE 文件头校验失败）")
	}
	return nil
}

func replaceExecutable(exePath, newPath string) error {
	oldPath := exePath + ".old"
	_ = os.Remove(oldPath)

	if err := os.Rename(exePath, oldPath); err != nil {
		return fmt.Errorf("备份当前程序失败: %w", err)
	}
	if err := os.Rename(newPath, exePath); err != nil {
		// Rollback so user is never left without a binary
		_ = os.Rename(oldPath, exePath)
		return fmt.Errorf("写入新版本程序失败: %w", err)
	}
	return nil
}

func selectWindowsAsset(assets []ghAsset, preferredName string) ghAsset {
	prefLower := strings.ToLower(strings.TrimSpace(preferredName))
	if prefLower != "" {
		for _, a := range assets {
			if strings.ToLower(strings.TrimSpace(a.Name)) == prefLower {
				return a
			}
		}
	}
	for _, a := range assets {
		lower := strings.ToLower(a.Name)
		if strings.HasSuffix(lower, ".exe") && strings.Contains(lower, "proxy") {
			return a
		}
	}
	for _, a := range assets {
		if strings.HasSuffix(strings.ToLower(a.Name), ".exe") {
			return a
		}
	}
	return ghAsset{}
}

// CompareVersions compares two semantic version strings (e.g. "v1.0.0" vs "v1.2.0").
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func CompareVersions(a, b string) int {
	pa := parseVersion(a)
	pb := parseVersion(b)

	for i := 0; i < 3; i++ {
		if pa.nums[i] < pb.nums[i] {
			return -1
		}
		if pa.nums[i] > pb.nums[i] {
			return 1
		}
	}

	// Stable release > prerelease
	if pa.prerelease == "" && pb.prerelease != "" {
		return 1
	}
	if pa.prerelease != "" && pb.prerelease == "" {
		return -1
	}
	return strings.Compare(pa.prerelease, pb.prerelease)
}

type parsedVer struct {
	nums       [3]int
	prerelease string
}

func parseVersion(raw string) parsedVer {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if idx := strings.IndexByte(s, '+'); idx >= 0 {
		s = s[:idx]
	}
	if s == "" || strings.EqualFold(s, "dev") {
		return parsedVer{prerelease: "dev"}
	}

	var pre string
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		pre = s[idx+1:]
		s = s[:idx]
	}

	parts := strings.Split(s, ".")
	var nums [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err == nil && n >= 0 {
			nums[i] = n
		}
	}
	return parsedVer{nums: nums, prerelease: pre}
}
