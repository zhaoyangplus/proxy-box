package clashscript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectAndRestorePreservesCustomMain(t *testing.T) {
	clashDir := t.TempDir()
	profilesDir := filepath.Join(clashDir, "profiles")
	_ = os.MkdirAll(profilesDir, 0755)

	origScript := "// My custom rule\nfunction main(config, profileName) {\n  config.ipv6 = false;\n  return config;\n}\n"
	scriptPath := filepath.Join(profilesDir, "Script.js")
	if err := os.WriteFile(scriptPath, []byte(origScript), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := Inject(clashDir, "", "", ""); err != nil {
		t.Fatalf("Inject failed: %v", err)
	}
	if !IsInjected(clashDir) {
		t.Fatalf("expected IsInjected=true")
	}

	injectedBytes, _ := os.ReadFile(scriptPath)
	injectedStr := string(injectedBytes)
	if !strings.Contains(injectedStr, "function __cursorBoxUserMain") {
		t.Fatalf("expected custom user main to be preserved as __cursorBoxUserMain")
	}
	if !strings.Contains(injectedStr, AIGroupName) {
		t.Fatalf("expected AI group %s in injected script", AIGroupName)
	}

	if err := Remove(clashDir, "", "", ""); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	restoredBytes, _ := os.ReadFile(scriptPath)
	if string(restoredBytes) != origScript {
		t.Fatalf("expected exact original script restored, got:\n%s", string(restoredBytes))
	}
}

func TestClashMiAndV2rayNRuleGenerators(t *testing.T) {
	miJS := GenerateClashMiScript()
	if !strings.Contains(miJS, "function main(config") || !strings.Contains(miJS, AIGroupName) {
		t.Fatalf("Clash Mi JS override missing main or AI group")
	}

	miYAML := GenerateClashMiYAMLOverride()
	if !strings.Contains(miYAML, "exclude-filter:") || !strings.Contains(miYAML, "antigravity.google") {
		t.Fatalf("Clash Mi YAML override missing expected rules")
	}

	v2JSON, _, err := GenerateV2rayNRulesJSON()
	if err != nil {
		t.Fatalf("GenerateV2rayNRulesJSON failed: %v", err)
	}
	var rules []V2rayNRuleItem
	if err := json.Unmarshal([]byte(v2JSON), &rules); err != nil {
		t.Fatalf("invalid v2rayN JSON: %v", err)
	}
	if len(rules) != 3 || rules[0].OutboundTag != "direct" || rules[2].OutboundTag != "proxy" {
		t.Fatalf("unexpected v2rayN rules structure: %+v", rules)
	}
}
