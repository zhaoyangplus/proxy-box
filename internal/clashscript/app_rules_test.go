package clashscript

import "testing"

func TestAppRulesMatchTheV2Table(t *testing.T) {
	if got := join(AppIDs); got != "cursor,antigravity,claude_code,codex" {
		t.Fatalf("ids %s", got)
	}
	if InfoNodeExcludeFilter != `(?i)剩余|流量|到期|官网|套餐|重置|订阅|网址|失联|群|邀请|距离|下次|说明|过期|维护|故障|通知` {
		t.Fatalf("info filter %s", InfoNodeExcludeFilter)
	}
	if SpeedTestGroupName != "🧪 SPEEDTEST" || SpeedTestListenerName != "proxybox-speedtest" || DedicatedListenerName != "proxybox-ai-dedicated" {
		t.Fatalf("names %s %s %s", SpeedTestGroupName, SpeedTestListenerName, DedicatedListenerName)
	}
	cursor := AppRules["cursor"]
	if join(cursor.Processes) != "Cursor.exe" || join(cursor.DomainSuffixes) != "cursor.sh,cursor.com,cursorapi.com,cursor-cdn.com" {
		t.Fatalf("cursor %+v", cursor)
	}
	anti := AppRules["antigravity"]
	if join(anti.Processes) != "Antigravity.exe,language_server_windows_x64.exe,agy.exe" {
		t.Fatalf("antigravity processes %v", anti.Processes)
	}
	n := 0
	for _, domain := range anti.DomainSuffixes {
		if domain == "googleapis.com" || domain == "generativelanguage.googleapis.com" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("googleapis entries %d in %v", n, anti.DomainSuffixes)
	}
	if join(AppRules["claude_code"].Processes) != "claude.exe" || join(AppRules["codex"].Processes) != "codex.exe,Codex.exe,codex-plus-plus.exe" {
		t.Fatal("claude or codex processes")
	}
}

func join(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ","
		}
		out += item
	}
	return out
}
