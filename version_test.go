package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestEmbeddedVersionMatchesVERSIONFile(t *testing.T) {
	disk, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(disk))
	if strings.TrimSpace(embeddedVersion) != want {
		t.Fatalf("embedded %q file %q", embeddedVersion, want)
	}
	if appVersion != want {
		t.Fatalf("appVersion %q want %q", appVersion, want)
	}
	if !versionPattern.MatchString(want) {
		t.Fatalf("VERSION %q", want)
	}
	if versionLine() != "Proxy Box "+want {
		t.Fatalf("version line %q", versionLine())
	}
}

func TestVersionFlagPrintsEmbeddedVersion(t *testing.T) {
	disk, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatal(err)
	}
	want := "Proxy Box " + strings.TrimSpace(string(disk))
	var buf bytes.Buffer
	if !takeEarlyCLI(&buf, true, false, func() { t.Fatal("cleanup") }, func() { t.Fatal("self-test") }) {
		t.Fatal("--version did not return")
	}
	if strings.TrimSpace(buf.String()) != want {
		t.Fatalf("--version printed %q", buf.String())
	}
}

func TestParseEmbeddedVersion(t *testing.T) {
	if parseEmbeddedVersion("  v2.3.0\n") != "v2.3.0" {
		t.Fatal("trim")
	}
	if parseEmbeddedVersion("v1.2") != fallbackVersion || parseEmbeddedVersion("1.2.3") != fallbackVersion || parseEmbeddedVersion("v1.2.3-rc.1") != fallbackVersion {
		t.Fatal("fallback")
	}
}

func TestResolveAppVersionPrefersLinker(t *testing.T) {
	if resolveAppVersion(" v9.9.9 ", "v0.0.1") != "v9.9.9" {
		t.Fatal("linker value")
	}
	if resolveAppVersion("   ", "v2.3.0\n") != "v2.3.0" {
		t.Fatal("embedded value")
	}
	if resolveAppVersion("", "nope") != fallbackVersion {
		t.Fatal("bad embedded value")
	}
}
