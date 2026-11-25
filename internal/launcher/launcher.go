package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"proxy-box/internal/configurator"
	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

// LaunchTargetWithProxy launches Cursor or Antigravity with full proxy environment variables
// AND Chromium/Electron --proxy-server flags so both the main process and child sidecars
// (such as language_server_windows_x64.exe and integrated terminals) route through Clash Verge without TUN.
func LaunchTargetWithProxy(targetID, exePath, proxyURL string, restartIfRunning bool) error {
	if exePath == "" {
		return fmt.Errorf("未找到 %s 的可执行文件路径，请在路径设置中指定", targetID)
	}
	if _, err := os.Stat(exePath); err != nil {
		return fmt.Errorf("可执行文件不存在: %s", exePath)
	}
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return fmt.Errorf("代理地址为空")
	}

	if restartIfRunning {
		switch targetID {
		case "antigravity":
			terminateProcessByName("antigravity.exe")
			terminateProcessByName("language_server_windows_x64.exe")
			time.Sleep(650 * time.Millisecond)
		case "cursor":
			terminateProcessByName("cursor.exe")
			time.Sleep(650 * time.Millisecond)
		case "codex":
			terminateProcessByName("codex-plus-plus.exe")
			terminateProcessByName("codex.exe")
			time.Sleep(650 * time.Millisecond)
		}
	}

	args := []string{
		"--proxy-server=" + proxyURL,
		"--proxy-bypass-list=<-loopback>;localhost;127.0.0.1;::1;*.local",
	}

	cmd := exec.Command(exePath, args...)
	cmd.Dir = filepath.Dir(exePath)
	cmd.Env = buildProxyEnviron(proxyURL)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}

	return cmd.Start()
}

func buildProxyEnviron(proxyURL string) []string {
	override := map[string]string{
		"HTTP_PROXY":  proxyURL,
		"HTTPS_PROXY": proxyURL,
		"ALL_PROXY":   proxyURL,
		"NO_PROXY":    configurator.DefaultNoProxy,
		"http_proxy":  proxyURL,
		"https_proxy": proxyURL,
		"all_proxy":   proxyURL,
		"no_proxy":    configurator.DefaultNoProxy,
	}

	base := os.Environ()
	filtered := make([]string, 0, len(base)+len(override))
	for _, entry := range base {
		eq := strings.IndexByte(entry, '=')
		if eq <= 0 {
			continue
		}
		key := entry[:eq]
		if _, shouldReplace := override[key]; shouldReplace {
			continue
		}
		if _, shouldReplaceUpper := override[strings.ToUpper(key)]; shouldReplaceUpper {
			continue
		}
		filtered = append(filtered, entry)
	}
	for k, v := range override {
		filtered = append(filtered, k+"="+v)
	}
	return filtered
}

func terminateProcessByName(targetExeLower string) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return
	}
	for {
		name := strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
		if name == targetExeLower && pe.ProcessID != 0 {
			if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pe.ProcessID); err == nil {
				_ = windows.TerminateProcess(h, 0)
				_ = windows.CloseHandle(h)
			}
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
}

// CreateProxyDesktopShortcut creates a Windows .lnk shortcut on Desktop that invokes
// `cursor-box.exe --launch <targetID>` so clicking it always reads Clash Verge's live port.
func CreateProxyDesktopShortcut(targetID, shortcutTitle, targetExePath string) (string, error) {
	selfExe, err := os.Executable()
	if err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	desktopDir := filepath.Join(home, "Desktop")
	lnkPath := filepath.Join(desktopDir, shortcutTitle+".lnk")

	escapePS := func(s string) string {
		return strings.ReplaceAll(s, "'", "''")
	}

	iconLoc := selfExe + ",0"
	if targetExePath != "" {
		iconLoc = targetExePath + ",0"
	}

	psScript := fmt.Sprintf(
		`$ws = New-Object -ComObject WScript.Shell; `+
			`$sc = $ws.CreateShortcut('%s'); `+
			`$sc.TargetPath = '%s'; `+
			`$sc.Arguments = '--launch %s'; `+
			`$sc.WorkingDirectory = '%s'; `+
			`$sc.IconLocation = '%s'; `+
			`$sc.Description = '通过 Proxy Box 自动读取本地代理端口免 TUN 启动'; `+
			`$sc.Save()`,
		escapePS(lnkPath),
		escapePS(selfExe),
		escapePS(targetID),
		escapePS(filepath.Dir(selfExe)),
		escapePS(iconLoc),
	)

	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("创建桌面快捷方式失败: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return lnkPath, nil
}

func OpenAppOrPath(path string) error {
	if path == "" {
		return fmt.Errorf("路径为空")
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		cmd := exec.Command("explorer.exe", path)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		return cmd.Start()
	}
	cmd := exec.Command(path)
	cmd.Dir = filepath.Dir(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
	return cmd.Start()
}

// OpenURL opens an HTTP/HTTPS URL in the user's default Windows browser safely.
func OpenURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
		return fmt.Errorf("仅支持打开 http/https 链接")
	}
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", rawURL)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	return cmd.Start()
}
