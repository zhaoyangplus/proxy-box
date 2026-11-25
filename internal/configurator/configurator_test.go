package configurator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cursor-box/internal/configstore"
)

func TestStripJSONCAndNonDestructiveRestore(t *testing.T) {
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
