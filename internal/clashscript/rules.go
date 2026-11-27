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

// InfoNodeExcludeFilter drops subscription info pseudo-nodes from everyday groups.
const InfoNodeExcludeFilter = `(?i)剩余|流量|到期|官网|套餐|重置|订阅|网址|失联|群|邀请|距离|下次|说明|过期|维护|故障|通知`

const (
	AIGroupName           = "🤖 AI-IDE"
	AIAutoName            = "⚡ AI-Auto"
	SpeedTestGroupName    = "🧪 SPEEDTEST"
	DedicatedListenerName = "proxybox-ai-dedicated"
	SpeedTestListenerName = "proxybox-speedtest"
)

// AppIDs is the stable order used when rendering rules and listing enabled apps.
var AppIDs = []string{"cursor", "antigravity", "claude_code", "codex"}

// AppRuleSet is the process and domain rules for one AI app.
type AppRuleSet struct {
	ID             string
	Processes      []string
	DomainSuffixes []string
}

// AppRules maps an app id to the rules that send it through 🤖 AI-IDE.
var AppRules = map[string]AppRuleSet{
	"cursor": {
		ID:             "cursor",
		Processes:      []string{"Cursor.exe"},
		DomainSuffixes: []string{"cursor.sh", "cursor.com", "cursorapi.com", "cursor-cdn.com"},
	},
	"antigravity": {
		ID:        "antigravity",
		Processes: []string{"Antigravity.exe", "language_server_windows_x64.exe", "agy.exe"},
		DomainSuffixes: []string{
			"antigravity.google", "antigravity-unleash.goog",
			"generativelanguage.googleapis.com", "cloudcode-pa.googleapis.com",
			"alkalimakersuite-pa.clients6.google.com", "aistudio.google.com",
			"ai.google.dev", "googleapis.com",
		},
	},
	"claude_code": {
		ID:             "claude_code",
		Processes:      []string{"claude.exe"},
		DomainSuffixes: []string{"anthropic.com", "claude.ai", "claude.com"},
	},
	"codex": {
		ID:             "codex",
		Processes:      []string{"codex.exe", "Codex.exe", "codex-plus-plus.exe"},
		DomainSuffixes: []string{"openai.com", "chatgpt.com", "oaistatic.com", "oaiusercontent.com"},
	},
}
