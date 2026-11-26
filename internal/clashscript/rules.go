package clashscript

// AIProcessNames is the process list in the injected Clash script, including CursorBox.exe.
// The Clash Mi YAML template and the v2rayN export omit CursorBox.exe.
var AIProcessNames = []string{
	"Cursor.exe",
	"Antigravity.exe",
	"language_server_windows_x64.exe",
	"agy.exe",
	"claude.exe",
	"codex.exe",
	"Codex.exe",
	"codex-plus-plus.exe",
	"ProxyBox.exe",
	"CursorBox.exe",
}

// AIDomainSuffixes is the domain list the Clash scripts route to the AI group.
var AIDomainSuffixes = []string{
	"cursor.sh",
	"cursor.com",
	"cursorapi.com",
	"cursor-cdn.com",
	"anthropic.com",
	"claude.ai",
	"claude.com",
	"openai.com",
	"chatgpt.com",
	"oaistatic.com",
	"oaiusercontent.com",
	"antigravity.google",
	"antigravity-unleash.goog",
	"generativelanguage.googleapis.com",
	"cloudcode-pa.googleapis.com",
	"alkalimakersuite-pa.clients6.google.com",
	"aistudio.google.com",
	"ai.google.dev",
	"googleapis.com",
}

// RestrictedRegionFilter is the mihomo exclude-filter shared by the AI groups.
const RestrictedRegionFilter = `(?i)香港|🇭🇰|Hong Kong|港\d|hktj|hkbn|hkt|澳门|🇲🇴|Macau|中国|大陆|🇨🇳|China|俄罗斯|🇷🇺|Russia|(^|[^a-z])(hk|mo|cn|ru)([^a-z]|$)|剩余|流量|到期|官网|套餐|重置|订阅|网址`
