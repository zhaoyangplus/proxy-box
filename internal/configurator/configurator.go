package configurator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"proxy-box/internal/configstore"
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

func saveOriginalBackup(backupKey, path string, sessionStart bool) {
	if !sessionStart {
		return
	}
	backupsDir, err := configstore.BackupsDir()
	if err != nil {
		return
	}
	bakPath := filepath.Join(backupsDir, backupKey+"_original.json.bak")
	raw, err := os.ReadFile(path)
	if err != nil {
		_ = os.Remove(bakPath)
		return
	}
	_ = os.WriteFile(bakPath, raw, 0644)
}

func saveAppliedSnapshot(backupKey, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	backupsDir, err := configstore.BackupsDir()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(backupsDir, backupKey+"_applied.json.bak"), raw, 0644)
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

// ErrUnknownTarget is returned for an id outside the four AI apps.
var ErrUnknownTarget = errors.New("未知的应用标识，仅支持 cursor、antigravity、claude_code、codex")

var fileMu sync.Mutex

type appTarget struct {
	id          string
	backupKey   string
	appliedMsg  string
	restoredMsg string
	failPrefix  string
	directKeys  []string
	envKey      string
	dotenv      bool
	path        func() string
	apply       func(path, proxyURL string, st *configstore.StateManifest) error
	restore     func(path string, st *configstore.StateManifest) error
}

func appTargetTable() []appTarget {
	return []appTarget{
		{
			id: "cursor", backupKey: "cursor",
			appliedMsg:  "Cursor: 已禁用 HTTP/2 并写入本地代理与终端环境变量",
			restoredMsg: "Cursor: 已还原代理与 HTTP/2 设置",
			failPrefix:  "配置 Cursor 失败",
			directKeys:  []string{"cursor.general.disableHttp2", "http.proxy", "http.proxySupport", "http.proxyStrictSSL"},
			envKey:      "terminal.integrated.env.windows",
			path:        CursorSettingsPath,
			apply: func(path, proxyURL string, st *configstore.StateManifest) error {
				return applyIDEConfig("cursor", path, proxyURL, true, st)
			},
			restore: func(path string, st *configstore.StateManifest) error {
				return restoreIDETarget("cursor", path, []string{"cursor.general.disableHttp2", "http.proxy", "http.proxySupport", "http.proxyStrictSSL"}, st)
			},
		},
		{
			id: "claude_code", backupKey: "claude_code",
			appliedMsg:  "Claude Code: 已写入 ~/.claude/settings.json 代理环境变量",
			restoredMsg: "Claude Code: 已还原 ~/.claude/settings.json",
			failPrefix:  "配置 Claude Code 失败",
			envKey:      "env",
			path:        ClaudeCodeSettingsPath,
			apply: func(path, proxyURL string, st *configstore.StateManifest) error {
				return applyClaudeCodeConfig(path, proxyURL, st)
			},
			restore: func(path string, st *configstore.StateManifest) error {
				return restoreClaudeTarget(path, st)
			},
		},
		{
			id: "antigravity", backupKey: "antigravity",
			appliedMsg:  "Antigravity: 已写入 IDE 代理及内置终端环境变量",
			restoredMsg: "Antigravity: 已还原 IDE 代理设置",
			failPrefix:  "配置 Antigravity 失败",
			directKeys:  []string{"http.proxy", "http.proxySupport", "http.proxyStrictSSL"},
			envKey:      "terminal.integrated.env.windows",
			path:        AntigravitySettingsPath,
			apply: func(path, proxyURL string, st *configstore.StateManifest) error {
				return applyIDEConfig("antigravity", path, proxyURL, false, st)
			},
			restore: func(path string, st *configstore.StateManifest) error {
				return restoreIDETarget("antigravity", path, []string{"http.proxy", "http.proxySupport", "http.proxyStrictSSL"}, st)
			},
		},
		{
			id: "codex", backupKey: "codex_env",
			appliedMsg:  "OpenAI Codex: 已写入 ~/.codex/.env 代理与本地回环排除",
			restoredMsg: "OpenAI Codex: 已还原 ~/.codex/.env",
			failPrefix:  "配置 OpenAI Codex 失败",
			dotenv:      true,
			path:        CodexEnvPath,
			apply: func(path, proxyURL string, st *configstore.StateManifest) error {
				return applyCodexEnvConfig(path, proxyURL, st)
			},
			restore: func(path string, st *configstore.StateManifest) error {
				return restoreCodexEnvConfig(path, st)
			},
		},
	}
}

func targetByID(id string) (appTarget, bool) {
	id = strings.TrimSpace(id)
	for _, spec := range appTargetTable() {
		if spec.id == id {
			return spec, true
		}
	}
	return appTarget{}, false
}

// KnownTarget reports whether id is one of the four AI apps.
func KnownTarget(id string) bool {
	_, ok := targetByID(id)
	return ok
}

func normalizeProxyURL(proxyURL string) (string, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return "", fmt.Errorf("代理地址不能为空")
	}
	if !strings.HasPrefix(proxyURL, "http://") && !strings.HasPrefix(proxyURL, "https://") {
		proxyURL = "http://" + proxyURL
	}
	return proxyURL, nil
}

// ApplyTarget writes one app's proxy settings and records state.json.
// The original file bytes are kept from the first apply in this managed session.
func ApplyTarget(targetID, proxyURL string) (string, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	return applyTargetLocked(targetID, proxyURL)
}

func applyTargetLocked(targetID, proxyURL string) (string, error) {
	spec, ok := targetByID(targetID)
	if !ok {
		return "", ErrUnknownTarget
	}
	proxyURL, err := normalizeProxyURL(proxyURL)
	if err != nil {
		return "", err
	}
	st := configstore.LoadState()
	path := spec.path()
	if err := spec.apply(path, proxyURL, &st); err != nil {
		return "", fmt.Errorf("%s: %w", spec.failPrefix, err)
	}
	if err := saveAppliedSnapshot(spec.backupKey, path); err != nil {
		return "", err
	}
	if err := configstore.SaveState(st); err != nil {
		return "", err
	}
	return spec.appliedMsg, nil
}

func ApplyAll(opts ApplyOptions) ([]string, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	proxyURL, err := normalizeProxyURL(opts.ProxyURL)
	if err != nil {
		return nil, err
	}
	selected := []struct {
		on bool
		id string
	}{
		{opts.Cursor, "cursor"},
		{opts.ClaudeCode, "claude_code"},
		{opts.Antigravity, "antigravity"},
		{opts.Codex, "codex"},
	}
	var summary []string
	for _, item := range selected {
		if !item.on {
			continue
		}
		msg, err := applyTargetLocked(item.id, proxyURL)
		if err != nil {
			return summary, err
		}
		summary = append(summary, msg)
	}
	if opts.SyncUserEnv {
		st := configstore.LoadState()
		if err := applyWindowsUserEnv(proxyURL, &st); err != nil {
			return summary, fmt.Errorf("写入用户环境变量失败: %w", err)
		}
		if err := configstore.SaveState(st); err != nil {
			return summary, err
		}
		summary = append(summary, "Windows 用户环境变量: 已同步 HTTP_PROXY / HTTPS_PROXY / NO_PROXY")
	}
	return summary, nil
}

func applyIDEConfig(targetID, path, proxyURL string, setDisableHttp2 bool, st *configstore.StateManifest) error {
	m, existed, err := ReadJSONMap(path)
	if err != nil {
		return err
	}

	managedKeys := []string{"http.proxy", "http.proxySupport", "http.proxyStrictSSL"}
	if setDisableHttp2 {
		managedKeys = append(managedKeys, "cursor.general.disableHttp2")
	}
	envSubKeys := []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"}

	tb, alreadyManaged := st.Targets[targetID]
	sessionStart := !alreadyManaged || !tb.Managed
	saveOriginalBackup(targetID, path, sessionStart)
	if sessionStart {
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

	envSubKeys := []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"}

	tb, alreadyManaged := st.Targets["claude_code"]
	sessionStart := !alreadyManaged || !tb.Managed
	saveOriginalBackup("claude_code", path, sessionStart)
	if sessionStart {
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
	managedKeys := []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"}
	tb, alreadyManaged := st.Targets["codex"]
	sessionStart := !alreadyManaged || !tb.Managed
	saveOriginalBackup("codex_env", path, sessionStart)
	if sessionStart {
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

// RestoreTarget rolls one app back to the bytes from before it was managed.
// Proxy keys are restored alone when the file gained unrelated edits since the last apply.
// A target that was never applied is left untouched.
func RestoreTarget(targetID string) (string, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	return restoreTargetLocked(targetID)
}

func restoreTargetLocked(targetID string) (string, error) {
	spec, ok := targetByID(targetID)
	if !ok {
		return "", ErrUnknownTarget
	}
	st := configstore.LoadState()
	tb, ok := st.Targets[spec.id]
	if !ok || !tb.Managed {
		return "", nil
	}
	path := spec.path()
	exact, err := tryExactRestore(spec, path, tb)
	if err != nil {
		return "", err
	}
	if exact {
		tb.Managed = false
		st.Targets[spec.id] = tb
	} else if err := spec.restore(path, &st); err != nil {
		return "", err
	}
	if err := configstore.SaveState(st); err != nil {
		return "", err
	}
	return spec.restoredMsg, nil
}

func tryExactRestore(spec appTarget, path string, tb configstore.TargetBackupState) (bool, error) {
	dir, err := configstore.BackupsDir()
	if err != nil {
		return false, nil
	}
	applied, err := os.ReadFile(filepath.Join(dir, spec.backupKey+"_applied.json.bak"))
	if err != nil {
		return false, nil
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	if !bytes.Equal(current, applied) {
		return false, nil
	}
	var original []byte
	if tb.FileExisted {
		original, err = os.ReadFile(filepath.Join(dir, spec.backupKey+"_original.json.bak"))
		if err != nil {
			return false, nil
		}
	}
	match, err := sameNonProxy(spec, original, current)
	if err != nil || !match {
		return false, nil
	}
	if !tb.FileExisted {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return false, err
		}
		return true, nil
	}
	if err := os.WriteFile(path, original, 0644); err != nil {
		return false, err
	}
	return true, nil
}

func sameNonProxy(spec appTarget, original, current []byte) (bool, error) {
	if spec.dotenv {
		return dotenvRemainder(original) == dotenvRemainder(current), nil
	}
	a, err := jsonRemainder(original, spec.directKeys, spec.envKey)
	if err != nil {
		return false, err
	}
	b, err := jsonRemainder(current, spec.directKeys, spec.envKey)
	if err != nil {
		return false, err
	}
	return a == b, nil
}

func jsonRemainder(raw []byte, direct []string, envKey string) (string, error) {
	stripped := bytes.TrimSpace(StripJSONC(raw))
	if len(stripped) == 0 {
		return "{}", nil
	}
	var m map[string]any
	if err := json.Unmarshal(stripped, &m); err != nil {
		return "", err
	}
	if m == nil {
		return "{}", nil
	}
	for _, key := range direct {
		delete(m, key)
	}
	if envKey != "" {
		if env, ok := m[envKey].(map[string]any); ok {
			delete(env, "HTTP_PROXY")
			delete(env, "HTTPS_PROXY")
			delete(env, "NO_PROXY")
			if len(env) == 0 {
				delete(m, envKey)
			} else {
				m[envKey] = env
			}
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func dotenvRemainder(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	var keep []string
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if eq := strings.IndexByte(trim, '='); eq > 0 {
			key := strings.TrimSpace(trim[:eq])
			if key == "HTTP_PROXY" || key == "HTTPS_PROXY" || key == "NO_PROXY" {
				continue
			}
		}
		keep = append(keep, line)
	}
	return strings.TrimRight(strings.Join(keep, "\n"), "\n")
}

// RestoreAll rolls back every app Proxy Box is managing.
// Windows user env is restored only when SyncUserEnv had been applied.
func RestoreAll() ([]string, error) {
	fileMu.Lock()
	defer fileMu.Unlock()
	var summary []string
	for _, id := range []string{"cursor", "claude_code", "antigravity", "codex"} {
		msg, err := restoreTargetLocked(id)
		if err != nil {
			return summary, err
		}
		if msg != "" {
			summary = append(summary, msg)
		}
	}
	st := configstore.LoadState()
	if st.UserEnvManaged {
		if err := restoreWindowsUserEnv(&st); err == nil {
			summary = append(summary, "Windows 用户环境变量: 已清理还原")
			_ = configstore.SaveState(st)
		}
	}
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
