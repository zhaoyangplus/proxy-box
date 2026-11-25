package clashscript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"proxy-box/internal/configstore"
	"proxy-box/internal/mihomo"
	"gopkg.in/yaml.v3"
)

const (
	MarkerStart            = "/* === PROXY-BOX-AI-START === */"
	LegacyMarkerStart      = "/* === CURSOR-BOX-AI-START === */"
	MarkerEnd              = "/* === PROXY-BOX-AI-END === */"
	ScriptVersion          = "// ProxyBox-Script-Version: 5"
	AIGroupName            = "🤖 AI-IDE"
	AIAutoName             = "⚡ AI-Auto"
	VergeDedicatedPort     = 17897
	ClashMiDedicatedPort   = 17890
	DedicatedListenerName  = "proxybox-ai-dedicated"
)

func ScriptFilePath(clashDir string) string {
	if clashDir == "" {
		return ""
	}
	return filepath.Join(clashDir, "profiles", "Script.js")
}

func hasAnyMarker(raw []byte) bool {
	return bytes.Contains(raw, []byte(MarkerStart)) || bytes.Contains(raw, []byte(LegacyMarkerStart))
}

func IsInjected(clashDir string) bool {
	if clashDir == "" {
		return false
	}
	raw, err := os.ReadFile(ScriptFilePath(clashDir))
	if err != nil {
		return false
	}
	return hasAnyMarker(raw)
}

func NeedsUpgrade(clashDir string) bool {
	if clashDir == "" {
		return false
	}
	raw, err := os.ReadFile(ScriptFilePath(clashDir))
	if err != nil {
		return false
	}
	return hasAnyMarker(raw) && !bytes.Contains(raw, []byte(ScriptVersion))
}

func IsActiveInRuntime(clashDir string) bool {
	if clashDir == "" {
		return false
	}
	for _, fn := range []string{"clash-verge.yaml", "config.yaml"} {
		raw, err := os.ReadFile(filepath.Join(clashDir, fn))
		if err == nil && bytes.Contains(raw, []byte(AIGroupName)) {
			return true
		}
	}
	return false
}

func HasBackup() bool {
	backupsDir, err := configstore.BackupsDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(backupsDir, "ClashVerge_Script.js.bak"))
	return err == nil
}

const coreEnhanceJSBody = `function __cursorBoxEnhance(config) {
  if (!config || typeof config !== "object") return config;

  // 1. 开启进程匹配，确保精确识别 Cursor / Antigravity / LanguageServer / Claude / CursorBox
  config["find-process-mode"] = "always";

  // 2. 保护本地回环 RPC，避免 Fake-IP 干扰 IDE 与本地 Language Server 通信
  if (config.dns && typeof config.dns === "object") {
    const fakeFilter = Array.isArray(config.dns["fake-ip-filter"]) ? config.dns["fake-ip-filter"] : [];
    for (const item of ["localhost", "*.localhost", "127.0.0.1", "*.local"]) {
      if (!fakeFilter.includes(item)) fakeFilter.push(item);
    }
    config.dns["fake-ip-filter"] = fakeFilter;
  }

  // 3. 精准过滤香港/澳门/大陆/俄罗斯等受限地区（兼容 HK01/HKTJ/🇭🇰/港01 等命名）及机场信息节点
  const excludeRe = /香港|🇭🇰|Hong\s*Kong|港\d|hktj|hkbn|hkix|hgc|hkt|澳门|🇲🇴|Macau|Macao|中国|大陆|🇨🇳|China|俄罗斯|🇷🇺|Russia|白俄罗斯|伊朗|朝鲜|(?:^|[^a-z])(hk|mo|cn|ru|by|ir|kp)(?:[^a-z]|$)|剩余|流量|到期|官网|套餐|重置|订阅|网址|失联|群|邀请|距离|下次|说明|过期|维护/i;
  const preferRe = /美国|🇺🇸|United\s*States|日本|🇯🇵|Japan|新加坡|🇸🇬|Singapore|狮城|台湾|🇹🇼|Taiwan|韩国|🇰🇷|Korea|英国|🇬🇧|德国|🇩🇪|(?:^|[^a-z])(us|usa|jp|sg|tw|kr|uk|gb|de)(?:[^a-z]|$)/i;

  const allProxies = Array.isArray(config.proxies) ? config.proxies : [];
  const preferred = [];
  const others = [];

  for (const p of allProxies) {
    if (!p || !p.name) continue;
    if (excludeRe.test(p.name)) continue;
    if (preferRe.test(p.name)) {
      preferred.push(p.name);
    } else {
      others.push(p.name);
    }
  }
  const validNodes = preferred.concat(others);

  config["proxy-groups"] = Array.isArray(config["proxy-groups"]) ? config["proxy-groups"] : [];
  config["proxy-groups"] = config["proxy-groups"].filter(
    (g) => g && g.name !== "🤖 AI-IDE" && g.name !== "⚡ AI-Auto"
  );

  const hasProviders = config["proxy-providers"] && Object.keys(config["proxy-providers"]).length > 0;
  const excludeFilterStr = "(?i)香港|🇭🇰|Hong Kong|港\\d|hktj|hkbn|hkt|澳门|🇲🇴|Macau|中国|大陆|🇨🇳|China|俄罗斯|🇷🇺|Russia|(^|[^a-z])(hk|mo|cn|ru)([^a-z]|$)|剩余|流量|到期|官网|套餐|重置|订阅|网址";

  if (validNodes.length > 0) {
    const autoGroup = {
      name: "⚡ AI-Auto",
      type: "url-test",
      url: "https://www.gstatic.com/generate_204",
      interval: 180,
      tolerance: 30,
      lazy: false,
      proxies: validNodes
    };
    const aiGroup = {
      name: "🤖 AI-IDE",
      type: "select",
      proxies: ["⚡ AI-Auto"].concat(validNodes)
    };
    config["proxy-groups"].unshift(aiGroup, autoGroup);
  } else if (hasProviders) {
    const autoGroup = {
      name: "⚡ AI-Auto",
      type: "url-test",
      url: "https://www.gstatic.com/generate_204",
      interval: 180,
      tolerance: 30,
      lazy: false,
      "include-all": true,
      "exclude-filter": excludeFilterStr
    };
    const aiGroup = {
      name: "🤖 AI-IDE",
      type: "select",
      proxies: ["⚡ AI-Auto"],
      "include-all": true,
      "exclude-filter": excludeFilterStr
    };
    config["proxy-groups"].unshift(aiGroup, autoGroup);
  } else {
    const fallback = config["proxy-groups"].length > 0 ? [config["proxy-groups"][0].name] : ["DIRECT"];
    config["proxy-groups"].unshift({
      name: "🤖 AI-IDE",
      type: "select",
      proxies: fallback
    });
  }

  // 4. 置顶注入本地回环直连保护 + AI 进程与核心域名分流规则（含 CursorBox.exe 诊断进程）
  const aiRules = [
    "IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
    "IP-CIDR6,::1/128,DIRECT,no-resolve",
    "PROCESS-NAME,Cursor.exe,🤖 AI-IDE",
    "PROCESS-NAME,Antigravity.exe,🤖 AI-IDE",
    "PROCESS-NAME,language_server_windows_x64.exe,🤖 AI-IDE",
    "PROCESS-NAME,agy.exe,🤖 AI-IDE",
    "PROCESS-NAME,claude.exe,🤖 AI-IDE",
    "PROCESS-NAME,codex.exe,🤖 AI-IDE",
    "PROCESS-NAME,Codex.exe,🤖 AI-IDE",
    "PROCESS-NAME,codex-plus-plus.exe,🤖 AI-IDE",
    "PROCESS-NAME,ProxyBox.exe,🤖 AI-IDE",
    "PROCESS-NAME,CursorBox.exe,🤖 AI-IDE",
    "DOMAIN-SUFFIX,cursor.sh,🤖 AI-IDE",
    "DOMAIN-SUFFIX,cursor.com,🤖 AI-IDE",
    "DOMAIN-SUFFIX,cursorapi.com,🤖 AI-IDE",
    "DOMAIN-SUFFIX,cursor-cdn.com,🤖 AI-IDE",
    "DOMAIN-SUFFIX,anthropic.com,🤖 AI-IDE",
    "DOMAIN-SUFFIX,claude.ai,🤖 AI-IDE",
    "DOMAIN-SUFFIX,claude.com,🤖 AI-IDE",
    "DOMAIN-SUFFIX,openai.com,🤖 AI-IDE",
    "DOMAIN-SUFFIX,chatgpt.com,🤖 AI-IDE",
    "DOMAIN-SUFFIX,oaistatic.com,🤖 AI-IDE",
    "DOMAIN-SUFFIX,oaiusercontent.com,🤖 AI-IDE",
    "DOMAIN-SUFFIX,antigravity.google,🤖 AI-IDE",
    "DOMAIN-SUFFIX,antigravity-unleash.goog,🤖 AI-IDE",
    "DOMAIN-SUFFIX,generativelanguage.googleapis.com,🤖 AI-IDE",
    "DOMAIN-SUFFIX,cloudcode-pa.googleapis.com,🤖 AI-IDE",
    "DOMAIN-SUFFIX,alkalimakersuite-pa.clients6.google.com,🤖 AI-IDE",
    "DOMAIN-SUFFIX,aistudio.google.com,🤖 AI-IDE",
    "DOMAIN-SUFFIX,ai.google.dev,🤖 AI-IDE",
    "DOMAIN-SUFFIX,googleapis.com,🤖 AI-IDE"
  ];

  const existingRules = Array.isArray(config.rules) ? config.rules : [];
  const ruleSet = new Set(aiRules);
  const filteredExisting = existingRules.filter((r) => !ruleSet.has(r));
  config.rules = aiRules.concat(filteredExisting);

  // 5. 创建 Proxy Box 专属独立混合入站端口，硬绑定至 🤖 AI-IDE 分组，
  //    彻底绕过外部代理软件的主分组与全局/规则模式，实现双向切换节点互不影响
  const dedicatedPort = typeof __PROXYBOX_DEDICATED_PORT !== "undefined" ? __PROXYBOX_DEDICATED_PORT : 17897;
  config.listeners = Array.isArray(config.listeners) ? config.listeners : [];
  config.listeners = config.listeners.filter(
    (l) => l && l.name !== "proxybox-ai-dedicated" && l.name !== "cursorbox-ai-dedicated" && l.port !== dedicatedPort
  );
  config.listeners.push({
    name: "proxybox-ai-dedicated",
    type: "mixed",
    port: dedicatedPort,
    listen: "127.0.0.1",
    udp: true,
    proxy: "🤖 AI-IDE"
  });

  return config;
}
`

func buildInjectedScript(originalUserMain string, dedicatedPort int) string {
	var b strings.Builder
	b.WriteString(MarkerStart + "\n")
	b.WriteString(ScriptVersion + "\n")
	b.WriteString("// 由 Proxy Box 自动生成：建立独立隔离端口与免受限地区 AI 分流组（与外部代理节点双向隔离）\n")
	b.WriteString(fmt.Sprintf("const __PROXYBOX_DEDICATED_PORT = %d;\n", dedicatedPort))
	if strings.TrimSpace(originalUserMain) != "" {
		b.WriteString(originalUserMain + "\n\n")
	}
	b.WriteString(coreEnhanceJSBody + "\n")
	b.WriteString(`function main(config, profileName) {
  if (typeof __cursorBoxUserMain === "function") {
    config = __cursorBoxUserMain(config, profileName) || config;
  }
  return __cursorBoxEnhance(config);
}
`)
	b.WriteString(MarkerEnd + "\n")
	return b.String()
}

// GenerateClashMiScript returns a standalone JavaScript override script compatible with Clash Mi / Mihomo.
func GenerateClashMiScript() string {
	return buildInjectedScript("", ClashMiDedicatedPort)
}

// GenerateClashMiYAMLOverride returns a Mihomo YAML override template for Clash Mi (with dedicated isolated port 17890).
func GenerateClashMiYAMLOverride() string {
	return `# Proxy Box - Clash Mi (Mihomo) 独立隔离端口 + AI 专属免香港分流覆写 (YAML)
find-process-mode: always

listeners:
  - name: "proxybox-ai-dedicated"
    type: mixed
    port: 17890
    listen: 127.0.0.1
    udp: true
    proxy: "🤖 AI-IDE"

proxy-groups:
  - name: "🤖 AI-IDE"
    type: select
    proxies:
      - "⚡ AI-Auto"
    include-all: true
    exclude-filter: "(?i)香港|🇭🇰|Hong Kong|港\\d|hktj|hkbn|hkt|澳门|🇲🇴|Macau|中国|大陆|🇨🇳|China|俄罗斯|🇷🇺|Russia|(^|[^a-z])(hk|mo|cn|ru)([^a-z]|$)|剩余|流量|到期|官网|套餐|重置|订阅|网址"
  - name: "⚡ AI-Auto"
    type: url-test
    url: "https://www.gstatic.com/generate_204"
    interval: 180
    tolerance: 30
    lazy: false
    include-all: true
    exclude-filter: "(?i)香港|🇭🇰|Hong Kong|港\\d|hktj|hkbn|hkt|澳门|🇲🇴|Macau|中国|大陆|🇨🇳|China|俄罗斯|🇷🇺|Russia|(^|[^a-z])(hk|mo|cn|ru)([^a-z]|$)|剩余|流量|到期|官网|套餐|重置|订阅|网址"

rules:
  - IP-CIDR,127.0.0.0/8,DIRECT,no-resolve
  - IP-CIDR6,::1/128,DIRECT,no-resolve
  - PROCESS-NAME,Cursor.exe,🤖 AI-IDE
  - PROCESS-NAME,Antigravity.exe,🤖 AI-IDE
  - PROCESS-NAME,language_server_windows_x64.exe,🤖 AI-IDE
  - PROCESS-NAME,agy.exe,🤖 AI-IDE
  - PROCESS-NAME,claude.exe,🤖 AI-IDE
  - PROCESS-NAME,codex.exe,🤖 AI-IDE
  - PROCESS-NAME,Codex.exe,🤖 AI-IDE
  - PROCESS-NAME,codex-plus-plus.exe,🤖 AI-IDE
  - PROCESS-NAME,ProxyBox.exe,🤖 AI-IDE
  - DOMAIN-SUFFIX,cursor.sh,🤖 AI-IDE
  - DOMAIN-SUFFIX,cursor.com,🤖 AI-IDE
  - DOMAIN-SUFFIX,cursorapi.com,🤖 AI-IDE
  - DOMAIN-SUFFIX,cursor-cdn.com,🤖 AI-IDE
  - DOMAIN-SUFFIX,anthropic.com,🤖 AI-IDE
  - DOMAIN-SUFFIX,claude.ai,🤖 AI-IDE
  - DOMAIN-SUFFIX,claude.com,🤖 AI-IDE
  - DOMAIN-SUFFIX,openai.com,🤖 AI-IDE
  - DOMAIN-SUFFIX,chatgpt.com,🤖 AI-IDE
  - DOMAIN-SUFFIX,oaistatic.com,🤖 AI-IDE
  - DOMAIN-SUFFIX,oaiusercontent.com,🤖 AI-IDE
  - DOMAIN-SUFFIX,antigravity.google,🤖 AI-IDE
  - DOMAIN-SUFFIX,antigravity-unleash.goog,🤖 AI-IDE
  - DOMAIN-SUFFIX,generativelanguage.googleapis.com,🤖 AI-IDE
  - DOMAIN-SUFFIX,cloudcode-pa.googleapis.com,🤖 AI-IDE
  - DOMAIN-SUFFIX,alkalimakersuite-pa.clients6.google.com,🤖 AI-IDE
  - DOMAIN-SUFFIX,aistudio.google.com,🤖 AI-IDE
  - DOMAIN-SUFFIX,ai.google.dev,🤖 AI-IDE
  - DOMAIN-SUFFIX,googleapis.com,🤖 AI-IDE
`
}

func ExportClashMiTemplates() (jsPath, yamlPath string, err error) {
	base, err := configstore.BaseDir()
	if err != nil {
		return "", "", err
	}
	jsPath = filepath.Join(base, "clashmi-ai-override.js")
	yamlPath = filepath.Join(base, "clashmi-ai-override.yaml")
	if err := os.WriteFile(jsPath, []byte(GenerateClashMiScript()), 0644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(yamlPath, []byte(GenerateClashMiYAMLOverride()), 0644); err != nil {
		return "", "", err
	}
	return jsPath, yamlPath, nil
}

type V2rayNRuleItem struct {
	Remarks     string   `json:"remarks,omitempty"`
	OutboundTag string   `json:"outboundTag"`
	IP          []string `json:"ip,omitempty"`
	Domain      []string `json:"domain,omitempty"`
	Process     []string `json:"process,omitempty"`
	Enabled     bool     `json:"enabled"`
}

func BuildV2rayNRules() []V2rayNRuleItem {
	return []V2rayNRuleItem{
		{
			Remarks:     "ProxyBox-LocalLoopback",
			OutboundTag: "direct",
			IP:          []string{"127.0.0.1/8", "::1/128"},
			Enabled:     true,
		},
		{
			Remarks:     "ProxyBox-AI-Processes",
			OutboundTag: "proxy",
			Process: []string{
				"Cursor.exe",
				"Antigravity.exe",
				"language_server_windows_x64.exe",
				"agy.exe",
				"claude.exe",
				"codex.exe",
				"Codex.exe",
				"codex-plus-plus.exe",
				"ProxyBox.exe",
			},
			Enabled: true,
		},
		{
			Remarks:     "ProxyBox-AI-Domains",
			OutboundTag: "proxy",
			Domain: []string{
				"domain:cursor.sh",
				"domain:cursor.com",
				"domain:cursorapi.com",
				"domain:cursor-cdn.com",
				"domain:anthropic.com",
				"domain:claude.ai",
				"domain:claude.com",
				"domain:openai.com",
				"domain:chatgpt.com",
				"domain:oaistatic.com",
				"domain:oaiusercontent.com",
				"domain:antigravity.google",
				"domain:antigravity-unleash.goog",
				"domain:generativelanguage.googleapis.com",
				"domain:cloudcode-pa.googleapis.com",
				"domain:alkalimakersuite-pa.clients6.google.com",
				"domain:aistudio.google.com",
				"domain:ai.google.dev",
				"domain:googleapis.com",
			},
			Enabled: true,
		},
	}
}

func GenerateV2rayNRulesJSON() (string, string, error) {
	rules := BuildV2rayNRules()
	data, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return "", "", err
	}
	jsonStr := string(data)
	base, bErr := configstore.BaseDir()
	var savedPath string
	if bErr == nil {
		savedPath = filepath.Join(base, "v2rayn-ai-rules.json")
		_ = os.WriteFile(savedPath, data, 0644)
	}
	return jsonStr, savedPath, nil
}

func isDefaultTemplate(content string) bool {
	s := strings.TrimSpace(content)
	if s == "" {
		return true
	}
	compact := strings.Join(strings.Fields(s), " ")
	return compact == "// Define main function (script entry) function main(config, profileName) { return config; }" ||
		compact == "// Define main function (script entry) function main(config) { return config; }" ||
		compact == "function main(config, profileName) { return config; }" ||
		compact == "function main(config) { return config; }"
}

// Inject writes or updates the AI-IDE routing script inside Clash Verge's Global Script.js.
func Inject(clashDir, controllerPipe, externalController, secret string) (reloadedViaAPI bool, err error) {
	if clashDir == "" {
		return false, fmt.Errorf("未找到 Clash Verge 配置目录")
	}
	scriptPath := ScriptFilePath(clashDir)
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
		return false, err
	}

	existingBytes, readErr := os.ReadFile(scriptPath)
	existing := string(existingBytes)

	backupsDir, err := configstore.BackupsDir()
	if err != nil {
		return false, err
	}
	bakPath := filepath.Join(backupsDir, "ClashVerge_Script.js.bak")

	if readErr == nil && !hasAnyMarker(existingBytes) {
		_ = os.WriteFile(bakPath, existingBytes, 0644)
		st := configstore.LoadState()
		st.ScriptBackedUp = true
		_ = configstore.SaveState(st)
	}

	var userMainPart string
	if readErr == nil && !hasAnyMarker(existingBytes) && !isDefaultTemplate(existing) {
		userMainPart = strings.Replace(existing, "function main", "function __cursorBoxUserMain", 1)
	} else if bytes.Contains(existingBytes, []byte("function __cursorBoxUserMain")) {
		if bakRaw, err := os.ReadFile(bakPath); err == nil && !isDefaultTemplate(string(bakRaw)) {
			userMainPart = strings.Replace(string(bakRaw), "function main", "function __cursorBoxUserMain", 1)
		}
	}

	newContent := buildInjectedScript(userMainPart, VergeDedicatedPort)
	if err := os.WriteFile(scriptPath, []byte(newContent), 0644); err != nil {
		return false, fmt.Errorf("写入 Script.js 失败: %w", err)
	}

	touchScriptTimestamp(clashDir)

	if controllerPipe != "" || externalController != "" {
		mc := mihomo.NewClient(controllerPipe, externalController, secret)
		reloadedViaAPI = mc.ReloadConfig()
	}
	return reloadedViaAPI, nil
}

// Remove restores Clash Verge's Global Script.js to its original state before CursorBox injection.
func Remove(clashDir, controllerPipe, externalController, secret string) error {
	if clashDir == "" {
		return fmt.Errorf("未找到 Clash Verge 配置目录")
	}
	scriptPath := ScriptFilePath(clashDir)
	backupsDir, err := configstore.BackupsDir()
	if err == nil {
		bakPath := filepath.Join(backupsDir, "ClashVerge_Script.js.bak")
		if bakRaw, err := os.ReadFile(bakPath); err == nil {
			if err := os.WriteFile(scriptPath, bakRaw, 0644); err != nil {
				return err
			}
			_ = os.Remove(bakPath)
			touchScriptTimestamp(clashDir)
			if controllerPipe != "" || externalController != "" {
				mc := mihomo.NewClient(controllerPipe, externalController, secret)
				mc.ReloadConfig()
			}
			return nil
		}
	}

	defaultScript := "// Define main function (script entry)\n\nfunction main(config, profileName) {\n  return config;\n}\n"
	if err := os.WriteFile(scriptPath, []byte(defaultScript), 0644); err != nil {
		return err
	}
	touchScriptTimestamp(clashDir)
	return nil
}

func touchScriptTimestamp(clashDir string) {
	pPath := filepath.Join(clashDir, "profiles.yaml")
	raw, err := os.ReadFile(pPath)
	if err != nil {
		return
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return
	}
	top := root.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value == "items" && top.Content[i+1].Kind == yaml.SequenceNode {
			for _, item := range top.Content[i+1].Content {
				if item.Kind != yaml.MappingNode {
					continue
				}
				isScript := false
				for j := 0; j+1 < len(item.Content); j += 2 {
					if item.Content[j].Value == "uid" && item.Content[j+1].Value == "Script" {
						isScript = true
						break
					}
				}
				if isScript {
					for j := 0; j+1 < len(item.Content); j += 2 {
						if item.Content[j].Value == "updated" {
							item.Content[j+1].SetString(fmt.Sprintf("%d", time.Now().Unix()))
							item.Content[j+1].Tag = "!!int"
						}
					}
				}
			}
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err == nil {
		_ = enc.Close()
		_ = os.WriteFile(pPath, buf.Bytes(), 0644)
	}
}
