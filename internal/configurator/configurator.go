package configurator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"cursor-box/internal/configstore"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const DefaultNoProxy = "localhost,127.0.0.1,::1,.local"

// StripJSONC removes // line comments, /* block comments */, and trailing commas
// while preserving string literals intact so VS Code / Cursor settings.json parses cleanly.
func StripJSONC(src []byte) []byte {
	out := make([]byte, 0, len(src))
	n := len(src)
	inStr := false
	for i := 0; i < n; i++ {
		c := src[i]
		if inStr {
			out = append(out, c)
			if c == '\\' && i+1 < n {
				i++
				out = append(out, src[i])
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < n {
			if src[i+1] == '/' {
				i += 2
				for i < n && src[i] != '\n' {
					i++
				}
				if i < n {
					out = append(out, '\n')
				}
				continue
			}
			if src[i+1] == '*' {
				i += 2
				for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
					i++
				}
				i++
				continue
			}
		}
		out = append(out, c)
	}

	// Second pass: remove trailing commas before } or ] outside strings
	clean := make([]byte, 0, len(out))
	inStr = false
	for i := 0; i < len(out); i++ {
		c := out[i]
		if inStr {
			clean = append(clean, c)
			if c == '\\' && i+1 < len(out) {
				i++
				clean = append(clean, out[i])
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			clean = append(clean, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(out) && (out[j] == ' ' || out[j] == '\t' || out[j] == '\r' || out[j] == '\n') {
				j++
			}
			if j < len(out) && (out[j] == '}' || out[j] == ']') {
				continue
			}
		}
		clean = append(clean, c)
	}
	return bytes.TrimPrefix(clean, []byte{0xEF, 0xBB, 0xBF})
}

func ReadJSONMap(path string) (map[string]json.RawMessage, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]json.RawMessage), false, nil
		}
		return nil, false, err
	}
	stripped := bytes.TrimSpace(StripJSONC(raw))
	if len(stripped) == 0 {
		return make(map[string]json.RawMessage), true, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(stripped, &m); err != nil {
		return nil, true, fmt.Errorf("解析 JSON 失败 (%s): %w", filepath.Base(path), err)
	}
	if m == nil {
		m = make(map[string]json.RawMessage)
	}
	return m, true, nil
}

func writeJSONMap(path string, m map[string]json.RawMessage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func rawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func ensureRawFileBackup(targetID, path string) {
	backupsDir, err := configstore.BackupsDir()
	if err != nil {
		return
	}
	bakPath := filepath.Join(backupsDir, targetID+"_original.json.bak")
	if _, err := os.Stat(bakPath); err == nil {
		return // Keep earliest untouched backup
	}
	if raw, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(bakPath, raw, 0644)
	}
}

func CursorSettingsPath() string {
	appData := os.Getenv("APPDATA")
	return filepath.Join(appData, "Cursor", "User", "settings.json")
}

func AntigravitySettingsPath() string {
	appData := os.Getenv("APPDATA")
	return filepath.Join(appData, "Antigravity", "User", "settings.json")
}

func ClaudeCodeSettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

func CodexEnvPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", ".env")
}

// ReadEnvFile parses key=value pairs from a dotenv file (ignoring comments).
func ReadEnvFile(path string) (map[string]string, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), false, nil
		}
		return nil, false, err
	}
	res := make(map[string]string)
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if eq := strings.IndexByte(trim, '='); eq > 0 {
			k := strings.TrimSpace(trim[:eq])
			v := strings.Trim(strings.TrimSpace(trim[eq+1:]), `"'`)
			res[k] = v
		}
	}
	return res, true, nil
}

// ApplyOptions specifies which targets to configure.
type ApplyOptions struct {
	ProxyURL    string `json:"proxyUrl"`
	Cursor      bool   `json:"cursor"`
	ClaudeCode  bool   `json:"claudeCode"`
	Antigravity bool   `json:"antigravity"`
	Codex       bool   `json:"codex"`
	SyncUserEnv bool   `json:"syncUserEnv"`
}

func ApplyAll(opts ApplyOptions) ([]string, error) {
	proxyURL := strings.TrimSpace(opts.ProxyURL)
	if proxyURL == "" {
		return nil, fmt.Errorf("代理地址不能为空")
	}
	if !strings.HasPrefix(proxyURL, "http://") && !strings.HasPrefix(proxyURL, "https://") {
		proxyURL = "http://" + proxyURL
	}

	st := configstore.LoadState()
	var summary []string

	if opts.Cursor {
		if err := applyIDEConfig("cursor", CursorSettingsPath(), proxyURL, true, &st); err != nil {
			return summary, fmt.Errorf("配置 Cursor 失败: %w", err)
		}
		summary = append(summary, "Cursor: 已禁用 HTTP/2 并写入本地代理与终端环境变量")
	}

	if opts.ClaudeCode {
		if err := applyClaudeCodeConfig(ClaudeCodeSettingsPath(), proxyURL, &st); err != nil {
			return summary, fmt.Errorf("配置 Claude Code 失败: %w", err)
		}
		summary = append(summary, "Claude Code: 已写入 ~/.claude/settings.json 代理环境变量")
	}

	if opts.Antigravity {
		if err := applyIDEConfig("antigravity", AntigravitySettingsPath(), proxyURL, false, &st); err != nil {
			return summary, fmt.Errorf("配置 Antigravity 失败: %w", err)
		}
		summary = append(summary, "Antigravity: 已写入 IDE 代理及内置终端环境变量")
	}

	if opts.Codex {
		if err := applyCodexEnvConfig(CodexEnvPath(), proxyURL, &st); err != nil {
			return summary, fmt.Errorf("配置 OpenAI Codex 失败: %w", err)
		}
		summary = append(summary, "OpenAI Codex: 已写入 ~/.codex/.env 代理与本地回环排除")
	}

	if opts.SyncUserEnv {
		if err := applyWindowsUserEnv(proxyURL, &st); err != nil {
			return summary, fmt.Errorf("写入用户环境变量失败: %w", err)
		}
		summary = append(summary, "Windows 用户环境变量: 已同步 HTTP_PROXY / HTTPS_PROXY / NO_PROXY")
	}

	if err := configstore.SaveState(st); err != nil {
		return summary, err
	}
	return summary, nil
}

func applyIDEConfig(targetID, path, proxyURL string, setDisableHttp2 bool, st *configstore.StateManifest) error {
	m, existed, err := ReadJSONMap(path)
	if err != nil {
		return err
	}

	ensureRawFileBackup(targetID, path)

	managedKeys := []string{"http.proxy", "http.proxySupport", "http.proxyStrictSSL"}
	if setDisableHttp2 {
		managedKeys = append(managedKeys, "cursor.general.disableHttp2")
	}
	envSubKeys := []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"}

	tb, alreadyManaged := st.Targets[targetID]
	if !alreadyManaged || !tb.Managed {
		tb = configstore.TargetBackupState{
			Managed:     true,
			FileExisted: existed,
			Keys:        make(map[string]configstore.KeySnapshot),
			EnvKeys:     make(map[string]configstore.KeySnapshot),
		}
		for _, k := range managedKeys {
			val, ok := m[k]
			tb.Keys[k] = configstore.KeySnapshot{Existed: ok, Value: val}
		}
		// Snapshot terminal.integrated.env.windows subkeys
		var envMap map[string]json.RawMessage
		if rawEnv, ok := m["terminal.integrated.env.windows"]; ok {
			tb.Keys["terminal.integrated.env.windows"] = configstore.KeySnapshot{Existed: true, Value: rawEnv}
			_ = json.Unmarshal(rawEnv, &envMap)
		} else {
			tb.Keys["terminal.integrated.env.windows"] = configstore.KeySnapshot{Existed: false}
		}
		for _, ek := range envSubKeys {
			if envMap != nil {
				v, ok := envMap[ek]
				tb.EnvKeys[ek] = configstore.KeySnapshot{Existed: ok, Value: v}
			} else {
				tb.EnvKeys[ek] = configstore.KeySnapshot{Existed: false}
			}
		}
	}

	if setDisableHttp2 {
		m["cursor.general.disableHttp2"] = rawJSON(true)
	}
	m["http.proxy"] = rawJSON(proxyURL)
	m["http.proxySupport"] = rawJSON("override")
	m["http.proxyStrictSSL"] = rawJSON(false)

	envMap := make(map[string]json.RawMessage)
	if rawEnv, ok := m["terminal.integrated.env.windows"]; ok {
		_ = json.Unmarshal(rawEnv, &envMap)
		if envMap == nil {
			envMap = make(map[string]json.RawMessage)
		}
	}
	envMap["HTTP_PROXY"] = rawJSON(proxyURL)
	envMap["HTTPS_PROXY"] = rawJSON(proxyURL)
	envMap["NO_PROXY"] = rawJSON(DefaultNoProxy)
	m["terminal.integrated.env.windows"] = rawJSON(envMap)

	if err := writeJSONMap(path, m); err != nil {
		return err
	}

	tb.Managed = true
	tb.AppliedProxy = proxyURL
	tb.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
	st.Targets[targetID] = tb
	return nil
}

func applyClaudeCodeConfig(path, proxyURL string, st *configstore.StateManifest) error {
	m, existed, err := ReadJSONMap(path)
	if err != nil {
		return err
	}

	ensureRawFileBackup("claude_code", path)
	envSubKeys := []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"}

	tb, alreadyManaged := st.Targets["claude_code"]
	if !alreadyManaged || !tb.Managed {
		tb = configstore.TargetBackupState{
			Managed:     true,
			FileExisted: existed,
			Keys:        make(map[string]configstore.KeySnapshot),
			EnvKeys:     make(map[string]configstore.KeySnapshot),
		}
		var envMap map[string]json.RawMessage
		if rawEnv, ok := m["env"]; ok {
			tb.Keys["env"] = configstore.KeySnapshot{Existed: true, Value: rawEnv}
			_ = json.Unmarshal(rawEnv, &envMap)
		} else {
			tb.Keys["env"] = configstore.KeySnapshot{Existed: false}
		}
		for _, ek := range envSubKeys {
			if envMap != nil {
				v, ok := envMap[ek]
				tb.EnvKeys[ek] = configstore.KeySnapshot{Existed: ok, Value: v}
			} else {
				tb.EnvKeys[ek] = configstore.KeySnapshot{Existed: false}
			}
		}
	}

	envMap := make(map[string]json.RawMessage)
	if rawEnv, ok := m["env"]; ok {
		_ = json.Unmarshal(rawEnv, &envMap)
		if envMap == nil {
			envMap = make(map[string]json.RawMessage)
		}
	}
	envMap["HTTP_PROXY"] = rawJSON(proxyURL)
	envMap["HTTPS_PROXY"] = rawJSON(proxyURL)
	envMap["NO_PROXY"] = rawJSON(DefaultNoProxy)
	m["env"] = rawJSON(envMap)

	if err := writeJSONMap(path, m); err != nil {
		return err
	}

	tb.Managed = true
	tb.AppliedProxy = proxyURL
	tb.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
	st.Targets["claude_code"] = tb
	return nil
}

func applyCodexEnvConfig(path, proxyURL string, st *configstore.StateManifest) error {
	existingMap, existed, err := ReadEnvFile(path)
	if err != nil {
		return err
	}
	if existed {
		ensureRawFileBackup("codex_env", path)
	}

	managedKeys := []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"}
	tb, alreadyManaged := st.Targets["codex"]
	if !alreadyManaged || !tb.Managed {
		tb = configstore.TargetBackupState{
			Managed:     true,
			FileExisted: existed,
			Keys:        make(map[string]configstore.KeySnapshot),
			EnvKeys:     make(map[string]configstore.KeySnapshot),
		}
		for _, k := range managedKeys {
			if val, ok := existingMap[k]; ok {
				tb.EnvKeys[k] = configstore.KeySnapshot{Existed: true, Value: rawJSON(val)}
			} else {
				tb.EnvKeys[k] = configstore.KeySnapshot{Existed: false}
			}
		}
	}

	updates := map[string]string{
		"HTTP_PROXY":  proxyURL,
		"HTTPS_PROXY": proxyURL,
		"NO_PROXY":    DefaultNoProxy,
	}
	if err := upsertEnvFile(path, updates, nil); err != nil {
		return err
	}

	tb.Managed = true
	tb.AppliedProxy = proxyURL
	tb.UpdatedAt = time.Now().Format("2006-01-02 15:04:05")
	st.Targets["codex"] = tb
	return nil
}

func restoreCodexEnvConfig(path string, st *configstore.StateManifest) error {
	_, existed, err := ReadEnvFile(path)
	if err != nil || !existed {
		return err
	}
	tb, hasState := st.Targets["codex"]
	updates := make(map[string]string)
	deletes := make(map[string]bool)

	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		if hasState && tb.Managed {
			if snap, ok := tb.EnvKeys[k]; ok && snap.Existed {
				var s string
				_ = json.Unmarshal(snap.Value, &s)
				updates[k] = s
			} else {
				deletes[k] = true
			}
		} else {
			deletes[k] = true
		}
	}

	if err := upsertEnvFile(path, updates, deletes); err != nil {
		return err
	}
	// If .env did not exist prior to CursorBox and is now empty, remove it cleanly
	if hasState && !tb.FileExisted {
		if raw, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(raw)) == "" {
			_ = os.Remove(path)
		}
	}
	tb.Managed = false
	st.Targets["codex"] = tb
	return nil
}

func upsertEnvFile(path string, updates map[string]string, deletes map[string]bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	raw, _ := os.ReadFile(path)
	lines := []string{}
	if len(raw) > 0 {
		lines = strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	}

	seen := make(map[string]bool)
	out := make([]string, 0, len(lines)+len(updates))
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			out = append(out, line)
			continue
		}
		if eq := strings.IndexByte(trim, '='); eq > 0 {
			k := strings.TrimSpace(trim[:eq])
			if deletes != nil && deletes[k] {
				continue
			}
			if newVal, ok := updates[k]; ok {
				out = append(out, fmt.Sprintf("%s=%s", k, newVal))
				seen[k] = true
				continue
			}
		}
		out = append(out, line)
	}
	// Deterministic insertion order
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		if newVal, ok := updates[k]; ok && !seen[k] {
			out = append(out, fmt.Sprintf("%s=%s", k, newVal))
		}
	}
	// Trim trailing empty lines
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	content := strings.Join(out, "\n")
	if content != "" {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0644)
}

// RestoreAll rolls back proxy settings written by CursorBox while keeping unrelated user edits intact.
func RestoreAll() ([]string, error) {
	st := configstore.LoadState()
	var summary []string

	if err := restoreIDETarget("cursor", CursorSettingsPath(), []string{"cursor.general.disableHttp2", "http.proxy", "http.proxySupport", "http.proxyStrictSSL"}, &st); err != nil {
		return summary, err
	}
	summary = append(summary, "Cursor: 已还原代理与 HTTP/2 设置")

	if err := restoreClaudeTarget(ClaudeCodeSettingsPath(), &st); err != nil {
		return summary, err
	}
	summary = append(summary, "Claude Code: 已还原 ~/.claude/settings.json")

	if err := restoreIDETarget("antigravity", AntigravitySettingsPath(), []string{"http.proxy", "http.proxySupport", "http.proxyStrictSSL"}, &st); err != nil {
		return summary, err
	}
	summary = append(summary, "Antigravity: 已还原 IDE 代理设置")

	if err := restoreCodexEnvConfig(CodexEnvPath(), &st); err != nil {
		return summary, err
	}
	summary = append(summary, "OpenAI Codex: 已还原 ~/.codex/.env")

	if st.UserEnvManaged {
		if err := restoreWindowsUserEnv(&st); err == nil {
			summary = append(summary, "Windows 用户环境变量: 已清理还原")
		}
	}

	_ = configstore.SaveState(st)
	return summary, nil
}

func restoreIDETarget(targetID, path string, directKeys []string, st *configstore.StateManifest) error {
	m, existed, err := ReadJSONMap(path)
	if err != nil || !existed {
		return err
	}

	tb, hasState := st.Targets[targetID]
	for _, k := range directKeys {
		if hasState && tb.Managed {
			if snap, ok := tb.Keys[k]; ok && snap.Existed {
				m[k] = snap.Value
			} else {
				delete(m, k)
			}
		} else {
			delete(m, k)
		}
	}

	if rawEnv, ok := m["terminal.integrated.env.windows"]; ok {
		var envMap map[string]json.RawMessage
		if json.Unmarshal(rawEnv, &envMap) == nil && envMap != nil {
			for _, ek := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
				if hasState && tb.Managed {
					if snap, ok := tb.EnvKeys[ek]; ok && snap.Existed {
						envMap[ek] = snap.Value
					} else {
						delete(envMap, ek)
					}
				} else {
					delete(envMap, ek)
				}
			}
			if len(envMap) == 0 && (!hasState || !tb.Keys["terminal.integrated.env.windows"].Existed) {
				delete(m, "terminal.integrated.env.windows")
			} else {
				m["terminal.integrated.env.windows"] = rawJSON(envMap)
			}
		}
	}

	tb.Managed = false
	st.Targets[targetID] = tb
	return writeJSONMap(path, m)
}

func restoreClaudeTarget(path string, st *configstore.StateManifest) error {
	m, existed, err := ReadJSONMap(path)
	if err != nil || !existed {
		return err
	}
	tb, hasState := st.Targets["claude_code"]
	if rawEnv, ok := m["env"]; ok {
		var envMap map[string]json.RawMessage
		if json.Unmarshal(rawEnv, &envMap) == nil && envMap != nil {
			for _, ek := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
				if hasState && tb.Managed {
					if snap, ok := tb.EnvKeys[ek]; ok && snap.Existed {
						envMap[ek] = snap.Value
					} else {
						delete(envMap, ek)
					}
				} else {
					delete(envMap, ek)
				}
			}
			if len(envMap) == 0 && (!hasState || !tb.Keys["env"].Existed) {
				delete(m, "env")
			} else {
				m["env"] = rawJSON(envMap)
			}
		}
	}
	tb.Managed = false
	st.Targets["claude_code"] = tb
	return writeJSONMap(path, m)
}

func applyWindowsUserEnv(proxyURL string, st *configstore.StateManifest) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	vars := map[string]string{
		"HTTP_PROXY":  proxyURL,
		"HTTPS_PROXY": proxyURL,
		"NO_PROXY":    DefaultNoProxy,
	}

	if !st.UserEnvManaged {
		st.PrevUserEnv = make(map[string]configstore.KeySnapshot)
		for name := range vars {
			val, _, err := k.GetStringValue(name)
			if err == nil {
				st.PrevUserEnv[name] = configstore.KeySnapshot{Existed: true, Value: rawJSON(val)}
			} else {
				st.PrevUserEnv[name] = configstore.KeySnapshot{Existed: false}
			}
		}
		st.UserEnvManaged = true
	}

	for name, val := range vars {
		if err := k.SetStringValue(name, val); err != nil {
			return err
		}
	}
	broadcastEnvChange()
	return nil
}

func restoreWindowsUserEnv(st *configstore.StateManifest) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		if snap, ok := st.PrevUserEnv[name]; ok && snap.Existed {
			var s string
			_ = json.Unmarshal(snap.Value, &s)
			_ = k.SetStringValue(name, s)
		} else {
			_ = k.DeleteValue(name)
		}
	}
	st.UserEnvManaged = false
	broadcastEnvChange()
	return nil
}

func broadcastEnvChange() {
	user32 := windows.NewLazySystemDLL("user32.dll")
	sendMessageTimeout := user32.NewProc("SendMessageTimeoutW")
	envPtr, _ := windows.UTF16PtrFromString("Environment")
	const (
		HWND_BROADCAST   = 0xffff
		WM_SETTINGCHANGE = 0x001A
		SMTO_ABORTIFHUNG = 0x0002
	)
	var result uintptr
	_, _, _ = sendMessageTimeout.Call(
		HWND_BROADCAST,
		WM_SETTINGCHANGE,
		0,
		uintptr(unsafe.Pointer(envPtr)),
		SMTO_ABORTIFHUNG,
		1500,
		uintptr(unsafe.Pointer(&result)),
	)
}
