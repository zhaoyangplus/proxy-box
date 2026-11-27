package configurator

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"proxy-box/internal/configstore"
)

func TestStripJSONCAndNonDestructiveRestore(t *testing.T) {
	configstore.SetBaseDir(t.TempDir())
	t.Cleanup(func() { configstore.SetBaseDir("") })
	rawJSONC := []byte(`{
		// 用户原有的注释
		"editor.fontSize": 15,
		/* 多行注释
		   包含特殊符号 "//" */
		"http.proxy": "http://127.0.0.1:10809",
		"terminal.integrated.env.windows": {
			"MY_CUSTOM_VAR": "keep_me",
		},
	}`)

	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.json")
	if err := os.WriteFile(settingsFile, rawJSONC, 0644); err != nil {
		t.Fatal(err)
	}

	st := configstore.StateManifest{
		Targets: make(map[string]configstore.TargetBackupState),
	}

	// 1. Apply proxy config
	if err := applyIDEConfig("cursor_test", settingsFile, "http://127.0.0.1:7897", true, &st); err != nil {
		t.Fatalf("applyIDEConfig failed: %v", err)
	}

	m, _, err := ReadJSONMap(settingsFile)
	if err != nil {
		t.Fatalf("ReadJSONMap after apply failed: %v", err)
	}
	var gotProxy string
	_ = json.Unmarshal(m["http.proxy"], &gotProxy)
	if gotProxy != "http://127.0.0.1:7897" {
		t.Fatalf("expected proxy 7897, got %s", gotProxy)
	}
	var disableH2 bool
	_ = json.Unmarshal(m["cursor.general.disableHttp2"], &disableH2)
	if !disableH2 {
		t.Fatalf("expected disableHttp2=true")
	}

	var envMap map[string]string
	_ = json.Unmarshal(m["terminal.integrated.env.windows"], &envMap)
	if envMap["MY_CUSTOM_VAR"] != "keep_me" || envMap["HTTP_PROXY"] != "http://127.0.0.1:7897" {
		t.Fatalf("unexpected envMap: %+v", envMap)
	}

	// Simulate user changing unrelated editor setting while proxy is active
	m["editor.tabSize"] = rawJSON(2)
	_ = writeJSONMap(settingsFile, m)

	// 2. Restore original proxy settings without losing editor.tabSize or MY_CUSTOM_VAR
	if err := restoreIDETarget("cursor_test", settingsFile, []string{"cursor.general.disableHttp2", "http.proxy", "http.proxySupport", "http.proxyStrictSSL"}, &st); err != nil {
		t.Fatalf("restoreIDETarget failed: %v", err)
	}

	mAfter, _, err := ReadJSONMap(settingsFile)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(mAfter["http.proxy"], &gotProxy)
	if gotProxy != "http://127.0.0.1:10809" {
		t.Fatalf("expected restored original proxy 10809, got %s", gotProxy)
	}
	if _, exists := mAfter["cursor.general.disableHttp2"]; exists {
		t.Fatalf("expected disableHttp2 key to be deleted on restore")
	}
	var tabSize int
	_ = json.Unmarshal(mAfter["editor.tabSize"], &tabSize)
	if tabSize != 2 {
		t.Fatalf("expected unrelated user edit editor.tabSize=2 to be preserved, got %d", tabSize)
	}
	var envMapAfter map[string]string
	_ = json.Unmarshal(mAfter["terminal.integrated.env.windows"], &envMapAfter)
	if envMapAfter["MY_CUSTOM_VAR"] != "keep_me" {
		t.Fatalf("expected MY_CUSTOM_VAR preserved")
	}
	if _, hasHTTP := envMapAfter["HTTP_PROXY"]; hasHTTP {
		t.Fatalf("expected HTTP_PROXY removed on restore, got: %+v", envMapAfter)
	}
}

func TestCodexDotEnvApplyAndRestore(t *testing.T) {
	configstore.SetBaseDir(t.TempDir())
	t.Cleanup(func() { configstore.SetBaseDir("") })
	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, ".env")
	orig := "# Custom user setting\nOPENAI_API_KEY=sk-test-123\n"
	if err := os.WriteFile(envFile, []byte(orig), 0644); err != nil {
		t.Fatal(err)
	}

	st := configstore.StateManifest{Targets: make(map[string]configstore.TargetBackupState)}
	if err := applyCodexEnvConfig(envFile, "http://127.0.0.1:7897", &st); err != nil {
		t.Fatalf("applyCodexEnvConfig failed: %v", err)
	}

	got, _, err := ReadEnvFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if got["OPENAI_API_KEY"] != "sk-test-123" || got["HTTPS_PROXY"] != "http://127.0.0.1:7897" || got["NO_PROXY"] != DefaultNoProxy {
		t.Fatalf("unexpected codex .env after apply: %+v", got)
	}

	if err := restoreCodexEnvConfig(envFile, &st); err != nil {
		t.Fatalf("restoreCodexEnvConfig failed: %v", err)
	}
	gotAfter, _, _ := ReadEnvFile(envFile)
	if gotAfter["OPENAI_API_KEY"] != "sk-test-123" {
		t.Fatalf("expected OPENAI_API_KEY preserved after restore, got: %+v", gotAfter)
	}
	if gotAfter["HTTPS_PROXY"] != "" || gotAfter["HTTP_PROXY"] != "" {
		t.Fatalf("expected proxy keys removed after restore, got: %+v", gotAfter)
	}
}

func useTempHome(t *testing.T) (cursor, antigravity, claude, codex string) {
	t.Helper()
	root := t.TempDir()
	roaming := filepath.Join(root, "roaming")
	home := filepath.Join(root, "home")
	t.Setenv("APPDATA", roaming)
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "local"))
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	configstore.SetBaseDir(filepath.Join(root, "box"))
	t.Cleanup(func() { configstore.SetBaseDir("") })
	return filepath.Join(roaming, "Cursor", "User", "settings.json"),
		filepath.Join(roaming, "Antigravity", "User", "settings.json"),
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".codex", ".env")
}

func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestApplyAllWithNothingEnabledLeavesFiles(t *testing.T) {
	cursor, antigravity, claude, codex := useTempHome(t)
	files := map[string][]byte{
		cursor:      []byte("{\"editor.fontSize\":14}\n"),
		antigravity: []byte("{\"editor.fontSize\":16}\n"),
		claude:      []byte("{\"theme\":\"dark\"}\n"),
		codex:       []byte("# keep\nOPENAI_API_KEY=sk-test\n"),
	}
	for path, body := range files {
		writeFile(t, path, body)
	}
	summary, err := ApplyAll(ApplyOptions{ProxyURL: "http://127.0.0.1:27892"})
	if err != nil {
		t.Fatal(err)
	}
	if len(summary) != 0 {
		t.Fatalf("summary = %v", summary)
	}
	for path, body := range files {
		if got := readFile(t, path); !bytes.Equal(got, body) {
			t.Fatalf("%s changed: %s", path, got)
		}
	}
	st := configstore.LoadState()
	if st.UserEnvManaged {
		t.Fatal("user env was written")
	}
	for _, id := range []string{"cursor", "antigravity", "claude_code", "codex"} {
		if st.Targets[id].Managed {
			t.Fatalf("%s managed", id)
		}
	}
}

func TestApplyTargetTouchesOnlyItsFiles(t *testing.T) {
	cursor, antigravity, claude, codex := useTempHome(t)
	cursorBody := []byte("{\"editor.fontSize\":14}\n")
	antiBody := []byte("{\"editor.fontSize\":16}\n")
	claudeBody := []byte("{\"theme\":\"dark\"}\n")
	codexBody := []byte("# keep\nOPENAI_API_KEY=sk-test\n")
	writeFile(t, cursor, cursorBody)
	writeFile(t, antigravity, antiBody)
	writeFile(t, claude, claudeBody)
	writeFile(t, codex, codexBody)

	msg, err := ApplyTarget("cursor", "127.0.0.1:27892")
	if err != nil {
		t.Fatal(err)
	}
	if msg == "" {
		t.Fatal("empty summary")
	}
	if bytes.Equal(readFile(t, cursor), cursorBody) {
		t.Fatal("cursor file was not updated")
	}
	got, _, err := ReadJSONMap(cursor)
	if err != nil {
		t.Fatal(err)
	}
	var proxy string
	if json.Unmarshal(got["http.proxy"], &proxy) != nil || proxy != "http://127.0.0.1:27892" {
		t.Fatalf("proxy = %s", proxy)
	}
	for _, item := range []struct {
		path string
		body []byte
	}{
		{antigravity, antiBody},
		{claude, claudeBody},
		{codex, codexBody},
	} {
		if got := readFile(t, item.path); !bytes.Equal(got, item.body) {
			t.Fatalf("%s changed: %s", item.path, got)
		}
	}
	st := configstore.LoadState()
	if !st.Targets["cursor"].Managed || st.Targets["codex"].Managed || st.UserEnvManaged {
		t.Fatalf("state = %+v userEnv %v", st.Targets, st.UserEnvManaged)
	}
}

func TestRestoreIsByteIdenticalAndKeepsFirstBackup(t *testing.T) {
	cursor, _, _, _ := useTempHome(t)
	original := []byte("{\r\n  // keep\r\n  \"editor.fontSize\": 15,\r\n  \"http.proxy\": \"http://127.0.0.1:9\",\r\n  \"terminal.integrated.env.windows\": {\r\n    \"MY_CUSTOM_VAR\": \"keep_me\"\r\n  }\r\n}\r\n")
	writeFile(t, cursor, original)
	if _, err := ApplyTarget("cursor", "http://127.0.0.1:27892"); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyTarget("cursor", "http://127.0.0.1:27893"); err != nil {
		t.Fatal(err)
	}
	dir, err := configstore.BackupsDir()
	if err != nil {
		t.Fatal(err)
	}
	if bak := readFile(t, filepath.Join(dir, "cursor_original.json.bak")); !bytes.Equal(bak, original) {
		t.Fatalf("backup changed:\n%s", bak)
	}
	got, _, err := ReadJSONMap(cursor)
	if err != nil {
		t.Fatal(err)
	}
	var proxy string
	_ = json.Unmarshal(got["http.proxy"], &proxy)
	if proxy != "http://127.0.0.1:27893" {
		t.Fatalf("second apply proxy = %s", proxy)
	}
	if _, err := RestoreTarget("cursor"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, cursor); !bytes.Equal(got, original) {
		t.Fatalf("restore = %s", got)
	}
	st := configstore.LoadState()
	if st.Targets["cursor"].Managed {
		t.Fatal("still managed")
	}
	if _, err := RestoreTarget("cursor"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, cursor); !bytes.Equal(got, original) {
		t.Fatal("second restore changed the file")
	}
}

func TestRestoreNeverAppliedIsNoop(t *testing.T) {
	_, _, _, codex := useTempHome(t)
	original := []byte("# user\nOPENAI_API_KEY=sk-test\n")
	writeFile(t, codex, original)
	msg, err := RestoreTarget("codex")
	if err != nil {
		t.Fatal(err)
	}
	if msg != "" {
		t.Fatalf("message = %q", msg)
	}
	if got := readFile(t, codex); !bytes.Equal(got, original) {
		t.Fatalf("codex changed: %s", got)
	}
	base, err := configstore.BaseDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "state.json")); !os.IsNotExist(err) {
		t.Fatalf("state.json err = %v", err)
	}
}

func TestRestoreKeepsLaterUnrelatedEdits(t *testing.T) {
	cursor, _, _, _ := useTempHome(t)
	original := []byte("{\n  \"editor.fontSize\": 15,\n  \"http.proxy\": \"http://127.0.0.1:9\",\n  \"terminal.integrated.env.windows\": {\n    \"MY_CUSTOM_VAR\": \"keep_me\"\n  }\n}\n")
	writeFile(t, cursor, original)
	if _, err := ApplyTarget("cursor", "http://127.0.0.1:27892"); err != nil {
		t.Fatal(err)
	}
	m, _, err := ReadJSONMap(cursor)
	if err != nil {
		t.Fatal(err)
	}
	m["editor.tabSize"] = rawJSON(2)
	if err := writeJSONMap(cursor, m); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreTarget("cursor"); err != nil {
		t.Fatal(err)
	}
	got, _, err := ReadJSONMap(cursor)
	if err != nil {
		t.Fatal(err)
	}
	var proxy string
	_ = json.Unmarshal(got["http.proxy"], &proxy)
	if proxy != "http://127.0.0.1:9" {
		t.Fatalf("proxy = %s", proxy)
	}
	var tab int
	_ = json.Unmarshal(got["editor.tabSize"], &tab)
	if tab != 2 {
		t.Fatalf("tabSize = %d", tab)
	}
	var env map[string]string
	_ = json.Unmarshal(got["terminal.integrated.env.windows"], &env)
	if env["MY_CUSTOM_VAR"] != "keep_me" {
		t.Fatalf("env = %+v", env)
	}
	if _, ok := env["HTTP_PROXY"]; ok {
		t.Fatalf("proxy env kept: %+v", env)
	}
	if _, ok := got["cursor.general.disableHttp2"]; ok {
		t.Fatal("disableHttp2 kept")
	}
}

func TestRestoreRemovesFileWeCreated(t *testing.T) {
	_, _, claude, _ := useTempHome(t)
	if _, err := ApplyTarget("claude_code", "http://127.0.0.1:27892"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(claude); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreTarget("claude_code"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(claude); !os.IsNotExist(err) {
		t.Fatalf("claude file err = %v", err)
	}
}

func TestUnknownTarget(t *testing.T) {
	useTempHome(t)
	_, err := ApplyTarget("notepad", "http://127.0.0.1:27892")
	if !errors.Is(err, ErrUnknownTarget) {
		t.Fatalf("apply err = %v", err)
	}
	_, err = RestoreTarget("notepad")
	if !errors.Is(err, ErrUnknownTarget) {
		t.Fatalf("restore err = %v", err)
	}
}

func TestApplyAllReusesApplyTarget(t *testing.T) {
	cursor, antigravity, claude, codex := useTempHome(t)
	cursorBody := []byte("{}\n")
	antiBody := []byte("{}\n")
	claudeBody := []byte("{}\n")
	codexBody := []byte("# c\n")
	writeFile(t, cursor, cursorBody)
	writeFile(t, antigravity, antiBody)
	writeFile(t, claude, claudeBody)
	writeFile(t, codex, codexBody)
	_, err := ApplyAll(ApplyOptions{ProxyURL: "http://127.0.0.1:27892", Cursor: true, Codex: true})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(readFile(t, cursor), cursorBody) || bytes.Equal(readFile(t, codex), codexBody) {
		t.Fatal("selected files were not updated")
	}
	if !bytes.Equal(readFile(t, antigravity), antiBody) || !bytes.Equal(readFile(t, claude), claudeBody) {
		t.Fatal("unselected files changed")
	}
	st := configstore.LoadState()
	if st.UserEnvManaged || !st.Targets["cursor"].Managed || !st.Targets["codex"].Managed || st.Targets["claude_code"].Managed {
		t.Fatalf("state user=%v targets=%+v", st.UserEnvManaged, st.Targets)
	}
}
