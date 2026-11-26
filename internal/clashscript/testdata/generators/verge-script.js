/* === PROXY-BOX-AI-START === */
// ProxyBox-Script-Version: 5
// 由 Proxy Box 自动生成：建立独立隔离端口与免受限地区 AI 分流组（与外部代理节点双向隔离）
const __PROXYBOX_DEDICATED_PORT = 17897;
function __cursorBoxEnhance(config) {
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

function main(config, profileName) {
  if (typeof __cursorBoxUserMain === "function") {
    config = __cursorBoxUserMain(config, profileName) || config;
  }
  return __cursorBoxEnhance(config);
}
/* === PROXY-BOX-AI-END === */
