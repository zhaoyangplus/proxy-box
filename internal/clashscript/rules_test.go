package clashscript

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestExportedListsMatchScriptOutput(t *testing.T) {
	yamlText := GenerateClashMiYAMLOverride()
	var doc struct {
		Groups []struct {
			Exclude string `yaml:"exclude-filter"`
		} `yaml:"proxy-groups"`
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		t.Fatal(err)
	}
	var processes, domains []string
	for _, rule := range doc.Rules {
		switch {
		case strings.HasPrefix(rule, "PROCESS-NAME,"):
			processes = append(processes, strings.Split(rule, ",")[1])
		case strings.HasPrefix(rule, "DOMAIN-SUFFIX,"):
			domains = append(domains, strings.Split(rule, ",")[1])
		}
	}
	if !reflect.DeepEqual(processes, aiProcessesWithoutCursorBox()) {
		t.Fatalf("yaml processes\n got %q\nwant %q", processes, aiProcessesWithoutCursorBox())
	}
	if !reflect.DeepEqual(domains, AIDomainSuffixes) {
		t.Fatalf("domains\n got %q\nwant %q", domains, AIDomainSuffixes)
	}
	if len(doc.Groups) == 0 {
		t.Fatal("no groups")
	}
	for _, g := range doc.Groups {
		if g.Exclude != RestrictedRegionFilter {
			t.Fatalf("exclude-filter %q", g.Exclude)
		}
	}

	js := GenerateClashMiScript()
	if !reflect.DeepEqual(scriptRuleNames(js, "PROCESS-NAME,"), AIProcessNames) {
		t.Fatalf("script processes %q", scriptRuleNames(js, "PROCESS-NAME,"))
	}
	if !reflect.DeepEqual(scriptRuleNames(js, "DOMAIN-SUFFIX,"), AIDomainSuffixes) {
		t.Fatalf("script domains %q", scriptRuleNames(js, "DOMAIN-SUFFIX,"))
	}
	if !strings.Contains(js, strings.ReplaceAll(RestrictedRegionFilter, `\`, `\\`)) {
		t.Fatal("script missing restricted-region filter")
	}

	raw, _, err := GenerateV2rayNRulesJSON()
	if err != nil {
		t.Fatal(err)
	}
	var items []V2rayNRuleItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatal(err)
	}
	wantProc := aiProcessesWithoutCursorBox()
	if !reflect.DeepEqual(items[1].Process, wantProc) {
		t.Fatalf("v2rayN processes\n got %q\nwant %q", items[1].Process, wantProc)
	}
	var wantDomains []string
	for _, domain := range AIDomainSuffixes {
		wantDomains = append(wantDomains, "domain:"+domain)
	}
	if !reflect.DeepEqual(items[2].Domain, wantDomains) {
		t.Fatalf("v2rayN domains\n got %q\nwant %q", items[2].Domain, wantDomains)
	}
}

func aiProcessesWithoutCursorBox() []string {
	var names []string
	for _, name := range AIProcessNames {
		if name == "CursorBox.exe" {
			continue
		}
		names = append(names, name)
	}
	return names
}

func scriptRuleNames(script, prefix string) []string {
	var names []string
	for _, line := range strings.Split(script, "\n") {
		i := strings.Index(line, prefix)
		if i < 0 {
			continue
		}
		rest := line[i+len(prefix):]
		comma := strings.Index(rest, ",")
		if comma < 0 {
			continue
		}
		names = append(names, rest[:comma])
	}
	return names
}
