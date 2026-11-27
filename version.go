package main

import (
	_ "embed"
	"regexp"
	"strings"
)

//go:embed VERSION
var embeddedVersion string

// appVersion stays empty unless the linker sets it with -X main.appVersion=...
// init loads VERSION when the linker value is empty.
var appVersion = ""

var versionPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

const fallbackVersion = "v0.0.0"

func init() {
	appVersion = resolveAppVersion(appVersion, embeddedVersion)
}

func resolveAppVersion(linkerValue, embedded string) string {
	if v := strings.TrimSpace(linkerValue); v != "" {
		return v
	}
	return parseEmbeddedVersion(embedded)
}

func parseEmbeddedVersion(raw string) string {
	v := strings.TrimSpace(raw)
	if versionPattern.MatchString(v) {
		return v
	}
	return fallbackVersion
}

func versionLine() string {
	return "Proxy Box " + appVersion
}
