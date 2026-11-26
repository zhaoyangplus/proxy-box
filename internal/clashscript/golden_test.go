package clashscript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func generatorOutputs(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	if _, err := Inject(dir, "", "", ""); err != nil {
		t.Fatal(err)
	}
	verge, err := os.ReadFile(filepath.Join(dir, "profiles", "Script.js"))
	if err != nil {
		t.Fatal(err)
	}
	v2, _, err := GenerateV2rayNRulesJSON()
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"clashmi.js":      GenerateClashMiScript(),
		"clashmi.yaml":    GenerateClashMiYAMLOverride(),
		"verge-script.js": string(verge),
		"v2rayn.json":     v2,
	}
}

func TestGeneratorBytesMatchCaptured(t *testing.T) {
	got := generatorOutputs(t)
	dir := filepath.Join("testdata", "generators")
	for name, body := range got {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		want := strings.ReplaceAll(string(raw), "\r\n", "\n")
		if want != body {
			t.Fatalf("%s changed (%d bytes captured, %d bytes now)", name, len(want), len(body))
		}
	}
}
