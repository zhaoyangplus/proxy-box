(function (root) {
  "use strict";

  var STORAGE_KEY = "pb.nodeFilters.v1";
  var GROUP_PROXY = "PROXY";
  var GROUP_AI = "🤖 AI-IDE";

  var REGION_ORDER = ["hk", "tw", "jp", "sg", "us", "kr", "uk", "de", "eu", "other"];

  var REGION_META = {
    hk: { code: "HK", name: "香港", color: "#e5484d" },
    tw: { code: "TW", name: "台湾", color: "#3e63dd" },
    jp: { code: "JP", name: "日本", color: "#d6409f" },
    sg: { code: "SG", name: "新加坡", color: "#ef5f00" },
    us: { code: "US", name: "美国", color: "#6e56cf" },
    kr: { code: "KR", name: "韩国", color: "#0090ff" },
    uk: { code: "GB", name: "英国", color: "#3358d4" },
    de: { code: "DE", name: "德国", color: "#a07800" },
    eu: { code: "EU", name: "欧洲", color: "#0d74ce" },
    other: { code: "··", name: "其他", color: "#8b8d98" },
  };

  var REGION_SRC = [
    ["hk", "香港|🇭🇰|hong\\s*kong|港\\d|hktj|hkbn|hkix|hgc|hkt|(?:^|[^a-z])hk(?:[^a-z]|$)"],
    ["tw", "台湾|台灣|🇹🇼|taiwan|台北|新北|彰化|高雄|(?:^|[^a-z])tw(?:[^a-z]|$)"],
    ["jp", "日本|🇯🇵|japan|东京|東京|大阪|埼玉|tokyo|osaka|(?:^|[^a-z])jp(?:[^a-z]|$)"],
    ["sg", "新加坡|狮城|獅城|🇸🇬|singapore|(?:^|[^a-z])sg(?:[^a-z]|$)"],
    ["us", "美国|美國|🇺🇸|united\\s*states|america|洛杉矶|硅谷|圣何塞|西雅图|纽约|芝加哥|达拉斯|los\\s*angeles|san\\s*jose|seattle|(?:^|[^a-z])(?:us|usa)(?:[^a-z]|$)"],
    ["kr", "韩国|韓國|🇰🇷|korea|首尔|首爾|seoul|(?:^|[^a-z])kr(?:[^a-z]|$)"],
    ["uk", "英国|英國|🇬🇧|united\\s*kingdom|britain|伦敦|london|(?:^|[^a-z])(?:uk|gb)(?:[^a-z]|$)"],
    ["de", "德国|德國|🇩🇪|germany|法兰克福|frankfurt|(?:^|[^a-z])de(?:[^a-z]|$)"],
    ["eu", "法国|荷兰|瑞士|瑞典|芬兰|挪威|波兰|意大利|西班牙|爱尔兰|卢森堡|奥地利|🇫🇷|🇳🇱|🇨🇭|🇸🇪|🇫🇮|🇳🇴|🇵🇱|🇮🇹|🇪🇸|🇮🇪|france|netherlands|switzerland|ireland|europe|(?:^|[^a-z])(?:fr|nl|ch|se|fi|no|pl|it|es|ie|eu)(?:[^a-z]|$)"],
  ];

  var REGION_RE = [];
  for (var ri = 0; ri < REGION_SRC.length; ri++) {
    REGION_RE.push({ key: REGION_SRC[ri][0], re: new RegExp(REGION_SRC[ri][1], "i") });
  }

  var REGION_SET = { all: 1, other: 1 };
  for (var rs = 0; rs < REGION_ORDER.length; rs++) REGION_SET[REGION_ORDER[rs]] = 1;

  var DELAY_SET = { all: 1, alive: 1, "100": 1, "200": 1, "300": 1, "500": 1, custom: 1 };
  var SPEED_SET = { all: 1, tested: 1, "1": 1, "5": 1, "10": 1, "20": 1, custom: 1 };
  var SORT_SET = { delay: 1, speed: 1, name: 1 };
  var SORT_DEFAULT_DIR = { delay: 1, speed: -1, name: 1 };

  function defaultGroupFilter() {
    return {
      query: "",
      sub: "all",
      region: "all",
      delayPreset: "all",
      maxDelayMs: 0,
      speedPreset: "all",
      minSpeedMBps: 0,
      sortKey: "delay",
      sortDir: 1,
    };
  }

  function defaultSortDir(key) {
    return SORT_DEFAULT_DIR[key] || 1;
  }

  function finite(value, fallback) {
    var n = Number(value);
    return Number.isFinite(n) ? n : fallback;
  }

  function sanitizeFilter(raw) {
    var d = defaultGroupFilter();
    if (!raw || typeof raw !== "object") return d;
    try {
      if (raw.query != null) d.query = String(raw.query);
      var sub = raw.sub == null ? "all" : String(raw.sub).trim();
      d.sub = sub ? sub : "all";

      var region = raw.region == null ? "all" : String(raw.region).trim().toLowerCase();
      d.region = REGION_SET[region] ? region : "all";

      var delayPreset = raw.delayPreset == null ? "all" : String(raw.delayPreset).trim().toLowerCase();
      d.delayPreset = DELAY_SET[delayPreset] ? delayPreset : "all";
      if (d.delayPreset === "100" || d.delayPreset === "200" || d.delayPreset === "300" || d.delayPreset === "500") {
        d.maxDelayMs = Number(d.delayPreset);
      } else if (d.delayPreset === "custom") {
        d.maxDelayMs = finite(raw.maxDelayMs, 0);
        if (d.maxDelayMs < 0) d.maxDelayMs = 0;
      }

      var speedPreset = raw.speedPreset == null ? "all" : String(raw.speedPreset).trim().toLowerCase();
      d.speedPreset = SPEED_SET[speedPreset] ? speedPreset : "all";
      if (d.speedPreset === "1" || d.speedPreset === "5" || d.speedPreset === "10" || d.speedPreset === "20") {
        d.minSpeedMBps = Number(d.speedPreset);
      } else if (d.speedPreset === "custom") {
        d.minSpeedMBps = finite(raw.minSpeedMBps, 0);
        if (d.minSpeedMBps < 0) d.minSpeedMBps = 0;
      }

      var sortKey = raw.sortKey == null ? "delay" : String(raw.sortKey).trim().toLowerCase();
      d.sortKey = SORT_SET[sortKey] ? sortKey : "delay";
      if (raw.sortDir === -1 || raw.sortDir === "-1") d.sortDir = -1;
      else if (raw.sortDir === 1 || raw.sortDir === "1") d.sortDir = 1;
      else d.sortDir = defaultSortDir(d.sortKey);
    } catch (err) {
      return defaultGroupFilter();
    }
    return d;
  }

  function blankFilters() {
    var out = {};
    out[GROUP_PROXY] = defaultGroupFilter();
    out[GROUP_AI] = defaultGroupFilter();
    return out;
  }

  function looksLikeFilter(data) {
    if (!data || typeof data !== "object") return false;
    return data.query != null || data.sub != null || data.region != null
      || data.delayPreset != null || data.speedPreset != null || data.sortKey != null;
  }

  function parseStored(data) {
    var out = blankFilters();
    if (!data || typeof data !== "object" || Array.isArray(data)) return out;
    var groups = null;
    if (data.groups && typeof data.groups === "object" && !Array.isArray(data.groups)) groups = data.groups;
    else if (data[GROUP_PROXY] || data[GROUP_AI]) groups = data;
    if (groups) {
      if (groups[GROUP_PROXY]) out[GROUP_PROXY] = sanitizeFilter(groups[GROUP_PROXY]);
      if (groups[GROUP_AI]) out[GROUP_AI] = sanitizeFilter(groups[GROUP_AI]);
      return out;
    }
    if (looksLikeFilter(data)) {
      out[GROUP_PROXY] = sanitizeFilter(data);
      out[GROUP_AI] = sanitizeFilter(data);
    }
    return out;
  }

  function loadFilters(storage) {
    try {
      if (!storage || typeof storage.getItem !== "function") return blankFilters();
      var raw = storage.getItem(STORAGE_KEY);
      if (raw == null || raw === "") return blankFilters();
      return parseStored(JSON.parse(raw));
    } catch (err) {
      return blankFilters();
    }
  }

  function saveFilters(storage, filters) {
    try {
      if (!storage || typeof storage.setItem !== "function") return;
      var payload = { v: 1, groups: {} };
      var src = filters && typeof filters === "object" ? filters : {};
      payload.groups[GROUP_PROXY] = sanitizeFilter(src[GROUP_PROXY]);
      payload.groups[GROUP_AI] = sanitizeFilter(src[GROUP_AI]);
      storage.setItem(STORAGE_KEY, JSON.stringify(payload));
    } catch (err) {}
  }

  function isUtilityName(name) {
    var raw = String(name == null ? "" : name).trim();
    if (!raw) return false;
    var upper = raw.toUpperCase();
    return upper === "DIRECT" || upper === "REJECT" || upper === "⚡ AUTO" || upper === "⚡ AI-AUTO";
  }

  function isUtilityNode(node) {
    if (!node || typeof node !== "object") return false;
    if (node.isAuto) return true;
    if (isUtilityName(node.name)) return true;
    var type = String(node.type || "").toLowerCase();
    return type === "direct" || type === "reject" || type === "rejectdrop";
  }

  function detectRegion(name) {
    var raw = name == null ? "" : String(name).trim();
    if (!raw) return "";
    if (isUtilityName(raw)) return "";
    for (var i = 0; i < REGION_RE.length; i++) {
      if (REGION_RE[i].re.test(raw)) return REGION_RE[i].key;
    }
    return "other";
  }

  function parseRate(name) {
    var raw = name == null ? "" : String(name);
    if (!raw) return null;
    var re = /(\d+(?:\.\d+)?)\s*[x×]|[x×]\s*(\d+(?:\.\d+)?)|(\d+(?:\.\d+)?)\s*倍/gi;
    var found = null;
    var match;
    while ((match = re.exec(raw))) {
      var num = match[1] || match[2] || match[3];
      var start = match.index;
      var end = start + match[0].length;
      var prev = start > 0 ? raw.charAt(start - 1) : "";
      var next = end < raw.length ? raw.charAt(end) : "";
      if (!match[3]) {
        if (match[1] && /[a-z]/i.test(next)) continue;
        if (match[2] && (/[a-z]/i.test(prev) || /[a-z]/i.test(next))) continue;
      }
      var value = Number(num);
      if (value > 0 && isFinite(value)) found = value;
    }
    return found;
  }

  function formatRate(value) {
    var n = Number(value);
    if (!(n > 0) || !isFinite(n)) return "";
    var rounded = Math.round(n * 100) / 100;
    if (Math.abs(rounded - Math.round(rounded)) < 1e-9) return String(Math.round(rounded));
    return String(rounded);
  }

  function regionMeta(key) {
    return REGION_META[key] || null;
  }

  function regionLabel(key) {
    var meta = regionMeta(key);
    return meta ? meta.name : "";
  }

  function regionCode(key) {
    var meta = regionMeta(key);
    return meta ? meta.code : "";
  }

  function regionColor(key) {
    var meta = regionMeta(key);
    return meta ? meta.color : "";
  }

  function regionCounts(nodes) {
    var counts = {};
    var list = Array.isArray(nodes) ? nodes : [];
    for (var i = 0; i < list.length; i++) {
      var key = list[i] && list[i].region;
      if (!key || !REGION_META[key]) continue;
      counts[key] = (counts[key] || 0) + 1;
    }
    return counts;
  }

  function countActive(f) {
    var n = 0;
    if (String(f.query || "").trim()) n++;
    if (f.sub !== "all") n++;
    if (f.region !== "all") n++;
    if (f.delayPreset !== "all") n++;
    if (f.speedPreset !== "all") n++;
    return n;
  }

  function activeFilterCount(filter) {
    return countActive(sanitizeFilter(filter));
  }

  function isFilterActive(filter) {
    return activeFilterCount(filter) > 0;
  }

  function delayThreshold(filter) {
    var f = sanitizeFilter(filter);
    if (f.delayPreset === "100" || f.delayPreset === "200" || f.delayPreset === "300" || f.delayPreset === "500") {
      return Number(f.delayPreset);
    }
    if (f.delayPreset === "custom") {
      var n = Number(f.maxDelayMs);
      return Number.isFinite(n) && n > 0 ? n : 0;
    }
    return 0;
  }

  function speedThreshold(filter) {
    var f = sanitizeFilter(filter);
    if (f.speedPreset === "1" || f.speedPreset === "5" || f.speedPreset === "10" || f.speedPreset === "20") {
      return Number(f.speedPreset);
    }
    if (f.speedPreset === "custom") {
      var n = Number(f.minSpeedMBps);
      return Number.isFinite(n) && n > 0 ? n : 0;
    }
    return 0;
  }

  function hitQuery(node, q) {
    var fields = [node.name, node.type, node.typeLabel, node.provider, node.subName, node.region, node.regionLabel, node.regionCode];
    for (var i = 0; i < fields.length; i++) {
      if (fields[i] == null || fields[i] === "") continue;
      if (String(fields[i]).toLowerCase().indexOf(q) >= 0) return true;
    }
    return false;
  }

  function passDelay(node, f, limit) {
    if (f.delayPreset === "all") return true;
    var ms = Number(node.delayMs);
    var alive = !node.untested && Number.isFinite(ms) && ms > 0;
    if (f.delayPreset === "alive") return alive;
    if (!(limit > 0)) return true;
    return alive && ms <= limit;
  }

  function passSpeed(node, f, limit) {
    if (f.speedPreset === "all") return true;
    if (f.speedPreset !== "tested" && !(limit > 0)) return true;
    if (!node.speedTested || node.speedError) return false;
    var speed = Number(node.speedMBps);
    if (!Number.isFinite(speed)) return false;
    if (f.speedPreset === "tested") return speed > 0;
    return speed >= limit;
  }

  function applyFilters(nodes, filter) {
    var list = Array.isArray(nodes) ? nodes : [];
    var f = sanitizeFilter(filter);
    var active = countActive(f) > 0;
    var q = String(f.query || "").trim().toLowerCase();
    var delayLimit = delayThreshold(f);
    var speedLimit = speedThreshold(f);
    var out = [];
    for (var i = 0; i < list.length; i++) {
      var node = list[i];
      if (!node) continue;
      if (active && isUtilityNode(node)) continue;
      if (q && !hitQuery(node, q)) continue;
      if (f.sub !== "all" && String(node.provider || "") !== f.sub) continue;
      if (f.region !== "all" && node.region !== f.region) continue;
      if (!passDelay(node, f, delayLimit)) continue;
      if (!passSpeed(node, f, speedLimit)) continue;
      out.push(node);
    }
    return out;
  }

  function rankDelay(node) {
    if (node.untested) return 2;
    if (!(Number(node.delayMs) > 0)) return 1;
    return 0;
  }

  function rankSpeed(node) {
    if (!node.speedTested) return 2;
    if (node.speedError) return 1;
    return 0;
  }

  function compareNodes(a, b, key, dir) {
    if (!!a.isAuto !== !!b.isAuto) return a.isAuto ? -1 : 1;
    if (key === "delay") {
      var da = rankDelay(a);
      var db = rankDelay(b);
      if (da !== db) return da - db;
      if (da === 0) return (Number(a.delayMs) - Number(b.delayMs)) * dir;
      return 0;
    }
    if (key === "speed") {
      var sa = rankSpeed(a);
      var sb = rankSpeed(b);
      if (sa !== sb) return sa - sb;
      if (sa === 0) return (Number(a.speedMBps) - Number(b.speedMBps)) * dir;
      return 0;
    }
    return String(a.name).localeCompare(String(b.name), "zh") * dir;
  }

  function sortNodes(nodes, filter) {
    var list = Array.isArray(nodes) ? nodes : [];
    var f = sanitizeFilter(filter);
    var dir = f.sortDir === -1 ? -1 : 1;
    var indexed = new Array(list.length);
    for (var i = 0; i < list.length; i++) indexed[i] = { n: list[i], i: i };
    indexed.sort(function (a, b) {
      return compareNodes(a.n, b.n, f.sortKey, dir) || a.i - b.i;
    });
    var out = new Array(indexed.length);
    for (var j = 0; j < indexed.length; j++) out[j] = indexed[j].n;
    return out;
  }

  function realNodes(nodes) {
    var list = Array.isArray(nodes) ? nodes : [];
    var out = [];
    for (var i = 0; i < list.length; i++) {
      if (list[i] && !isUtilityNode(list[i])) out.push(list[i]);
    }
    return out;
  }

  function anySpeedRecord(nodes) {
    for (var i = 0; i < nodes.length; i++) {
      var node = nodes[i];
      if (node.speedTested && !node.speedError && Number(node.speedMBps) > 0) return true;
    }
    return false;
  }

  function anyDelayRecord(nodes) {
    for (var i = 0; i < nodes.length; i++) {
      var node = nodes[i];
      if (!node.untested && Number(node.delayMs) > 0) return true;
    }
    return false;
  }

  function emptyReason(nodes, filter) {
    var f = sanitizeFilter(filter);
    if (countActive(f) === 0) return "";
    if (applyFilters(nodes, f).length > 0) return "";
    var real = realNodes(nodes);
    if (!real.length) return "";
    var speedOn = f.speedPreset === "tested" || speedThreshold(f) > 0;
    var delayOn = f.delayPreset === "alive" || delayThreshold(f) > 0;
    if (speedOn && !anySpeedRecord(real)) return "当前分组尚无网速记录，可先点击「测网速」";
    if (delayOn && !anyDelayRecord(real)) return "当前分组尚无延迟记录，可先点击「测延迟」";
    if (f.region !== "all") {
      var regionHit = false;
      for (var i = 0; i < real.length; i++) if (real[i].region === f.region) regionHit = true;
      if (!regionHit) return "当前分组没有该地区的节点";
    }
    if (f.sub !== "all") {
      var subHit = false;
      for (var s = 0; s < real.length; s++) if (String(real[s].provider || "") === f.sub) subHit = true;
      if (!subHit) return "当前订阅下没有节点";
    }
    var q = String(f.query || "").trim().toLowerCase();
    if (q) {
      var queryHit = false;
      for (var k = 0; k < real.length; k++) if (hitQuery(real[k], q)) queryHit = true;
      if (!queryHit) return "没有匹配的节点";
    }
    if (speedOn && countActive(f) === 1) return "没有达到当前网速要求的节点";
    if (delayOn && countActive(f) === 1) return "没有符合当前延迟条件的节点";
    return "没有同时满足当前筛选条件的节点";
  }

  function scopeTargets(nodes, filter) {
    if (!isFilterActive(filter)) return null;
    var rows = applyFilters(nodes, filter);
    var names = [];
    for (var i = 0; i < rows.length; i++) {
      if (isUtilityNode(rows[i])) continue;
      names.push(rows[i].name);
    }
    return names;
  }

  function speedTestNames(nodes, filter) {
    return scopeTargets(nodes, filter);
  }

  function trimNum(n) {
    var rounded = Math.round(Number(n) * 100) / 100;
    if (!isFinite(rounded)) return "0";
    if (Math.abs(rounded - Math.round(rounded)) < 1e-9) return String(Math.round(rounded));
    return String(rounded);
  }

  function delayLabel(filter) {
    var f = sanitizeFilter(filter);
    if (f.delayPreset === "alive") return "仅可用";
    if (f.delayPreset === "custom") return "≤ " + trimNum(f.maxDelayMs) + " ms";
    if (f.delayPreset === "100" || f.delayPreset === "200" || f.delayPreset === "300" || f.delayPreset === "500") {
      return "≤ " + f.delayPreset + " ms";
    }
    return "不限";
  }

  function speedLabel(filter) {
    var f = sanitizeFilter(filter);
    if (f.speedPreset === "tested") return "已测速";
    if (f.speedPreset === "custom") return "≥ " + trimNum(f.minSpeedMBps) + " MB/s";
    if (f.speedPreset === "1" || f.speedPreset === "5" || f.speedPreset === "10" || f.speedPreset === "20") {
      return "≥ " + f.speedPreset + " MB/s";
    }
    return "不限";
  }

  function subLabel(filter, subName) {
    var id = sanitizeFilter(filter).sub;
    if (typeof subName === "function") return String(subName(id) || id);
    if (subName && typeof subName === "object" && subName[id] != null) return String(subName[id]);
    return id;
  }

  function emptyChips(filter, subName) {
    var f = sanitizeFilter(filter);
    var chips = [];
    var query = String(f.query || "").trim();
    if (query) chips.push({ key: "query", label: "搜索“" + query + "”" });
    if (f.sub !== "all") chips.push({ key: "sub", label: "订阅：" + subLabel(f, subName) });
    if (f.region !== "all") chips.push({ key: "region", label: "地区：" + (regionLabel(f.region) || f.region) });
    if (f.delayPreset !== "all") chips.push({ key: "delay", label: "延迟 " + delayLabel(f) });
    if (f.speedPreset !== "all") chips.push({ key: "speed", label: "网速 " + speedLabel(f) });
    return chips;
  }

  function clearChip(filter, key) {
    var f = sanitizeFilter(filter);
    if (key === "query") f.query = "";
    else if (key === "sub") f.sub = "all";
    else if (key === "region") f.region = "all";
    else if (key === "delay") {
      f.delayPreset = "all";
      f.maxDelayMs = 0;
    } else if (key === "speed") {
      f.speedPreset = "all";
      f.minSpeedMBps = 0;
    }
    return f;
  }

  function delayBadge(node, opt) {
    if (opt && opt.delayBusy) return { text: "测试中", cls: "metric testing" };
    if (!node || node.untested) return { text: "未测速", cls: "metric none" };
    var ms = Number(node.delayMs);
    if (!(ms > 0)) return { text: "超时", cls: "metric d-bad", dot: true };
    var rounded = Math.round(ms);
    var cls = "metric d-bad";
    if (rounded <= 200) cls = "metric d-good";
    else if (rounded <= 500) cls = "metric d-mid";
    return { text: String(rounded), unit: "ms", cls: cls, dot: true };
  }

  function speedBadge(node, opt) {
    var phase = opt && opt.speedPhase;
    if (phase === "current") return { text: "测速中", cls: "metric testing spd-pulse" };
    if (phase === "queued") return { text: "排队中…", cls: "metric none" };
    if (node && node.speedError) return { text: "失败", cls: "metric spd-bad", title: String(node.speedError) };
    if (!node || !node.speedTested) return { text: "未测速", cls: "metric none" };
    var n = Number(node.speedMBps);
    if (!isFinite(n)) n = 0;
    var cls = "metric speed spd-low";
    if (n >= 5) cls = "metric speed spd-good";
    else if (n >= 1) cls = "metric speed spd-mid";
    return { text: n.toFixed(2), unit: "MB/s", cls: cls, icon: "gauge" };
  }

  function esc(value) {
    return String(value == null ? "" : value).replace(/[&<>"']/g, function (ch) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[ch];
    });
  }

  function icon(name) {
    return '<svg class="i" aria-hidden="true"><use href="#i-' + name + '"/></svg>';
  }

  function metricHTML(badge) {
    if (!badge) return "";
    var title = badge.title ? ' title="' + esc(badge.title) + '"' : "";
    var body = "";
    if (badge.icon) body += icon(badge.icon);
    if (badge.dot) body += '<i class="dot"></i>';
    body += esc(badge.text == null ? "" : badge.text);
    if (badge.unit) body += "<small>" + esc(badge.unit) + "</small>";
    return '<span class="' + esc(badge.cls || "metric") + '"' + title + ">" + body + "</span>";
  }

  function flagHTML(node) {
    if (!node || node.isAuto || !node.region) return "";
    var code = regionCode(node.region);
    if (!code) return "";
    return '<span class="flag" style="--fc:' + esc(regionColor(node.region)) + '" title="' + esc(regionLabel(node.region)) + '">' + esc(code) + "</span>";
  }

  function rateHTML(name, utility) {
    if (utility) return "";
    var rate = parseRate(name);
    if (rate == null) return "";
    var cls = rate > 1 ? " mul-hi" : rate < 1 ? " mul-lo" : "";
    return '<span class="tag' + cls + '">' + esc(formatRate(rate)) + "× 倍率</span>";
  }

  function actsHTML(node, opt, keepSlot) {
    if (isUtilityNode(node)) return keepSlot ? '<div class="nd-acts"></div>' : "";
    opt = opt || {};
    var run = opt.core !== false;
    var delayDis = !run || opt.delayBusy ? " disabled" : "";
    var speedDis = !run || opt.speedBusy ? " disabled" : "";
    var speedTitle = opt.speedBusy ? "测速通道占用中" : "测网速";
    return '<div class="nd-acts"><button type="button" class="ibtn" data-act="delay" title="测延迟" aria-label="测延迟"' + delayDis + ">" + icon("bolt") +
      '</button><button type="button" class="ibtn" data-act="speed" title="' + esc(speedTitle) + '" aria-label="测网速"' + speedDis + ">" + icon("gauge") + "</button></div>";
  }

  function nodeView(node, opt) {
    node = node || {};
    opt = opt || {};
    var name = node.name == null ? "" : String(node.name);
    return {
      node: node,
      opt: opt,
      name: name,
      selected: opt.selected != null ? !!opt.selected : !!node.selected,
      utility: isUtilityNode(node),
    };
  }

  function tagsHTML(view) {
    var node = view.node;
    var proto = node.typeLabel || node.type || (view.utility ? "" : "—");
    var html = "";
    if (proto) html += '<span class="tag">' + esc(proto) + "</span>";
    if (node.isAuto && node.resolvedTo) html += '<span class="tag via">当前 → ' + esc(node.resolvedTo) + "</span>";
    if (!view.utility) {
      var sub = node.subName || node.provider || "";
      if (sub) html += '<span class="tag">' + esc(sub) + "</span>";
      html += rateHTML(view.name, false);
    }
    return html;
  }

  function nodeCardHTML(node, opt) {
    var view = nodeView(node, opt);
    var sel = view.selected ? " is-sel" : "";
    var check = view.selected ? '<span class="nd-check" title="当前使用">' + icon("check") + "</span>" : "";
    var auto = view.node.isAuto ? '<span class="tag">自动</span>' : "";
    var speed = view.utility ? "" : metricHTML(speedBadge(view.node, view.opt));
    return '<article class="nd-card' + sel + '" data-id="' + esc(view.name) + '" tabindex="0" role="button" aria-pressed="' + (view.selected ? "true" : "false") + '">' +
      '<div class="nd-top">' + flagHTML(view.node) + auto + '<div class="nd-name" title="' + esc(view.name) + '">' + esc(view.name) + "</div>" + check + "</div>" +
      '<div class="nd-tags">' + tagsHTML(view) + "</div>" +
      '<div class="nd-foot">' + metricHTML(delayBadge(view.node, view.opt)) + speed + actsHTML(view.node, view.opt) + "</div></article>";
  }

  function nodeRowHTML(node, opt) {
    var view = nodeView(node, opt);
    var sel = view.selected ? " is-sel" : "";
    var check = view.selected ? '<span class="nd-check sm" title="当前使用">' + icon("check") + "</span>" : "";
    var auto = view.node.isAuto ? '<span class="tag">自动</span>' : "";
    var via = view.node.isAuto && view.node.resolvedTo ? '<span class="caption"> → ' + esc(view.node.resolvedTo) + "</span>" : "";
    var sub = view.utility ? "—" : (view.node.subName || view.node.provider || "—");
    var proto = view.node.typeLabel || view.node.type || "—";
    var speed = view.utility ? '<span class="metric none">—</span>' : metricHTML(speedBadge(view.node, view.opt));
    return '<div class="nd-tr' + sel + '" data-id="' + esc(view.name) + '" tabindex="0" role="button" aria-pressed="' + (view.selected ? "true" : "false") + '">' +
      '<div class="c-name">' + flagHTML(view.node) + auto + '<span class="ellipsis" title="' + esc(view.name) + '">' + esc(view.name) + via + "</span>" + rateHTML(view.name, view.utility) + check + "</div>" +
      '<div class="c-proto">' + esc(proto) + "</div>" +
      '<div class="c-sub">' + esc(sub) + "</div>" +
      "<div>" + metricHTML(delayBadge(view.node, view.opt)) + "</div>" +
      "<div>" + speed + "</div>" +
      actsHTML(view.node, view.opt, true) + "</div>";
  }

  function emptyChipsHTML(filter, subName) {
    var chips = emptyChips(filter, subName);
    var html = "";
    for (var i = 0; i < chips.length; i++) {
      html += '<button type="button" class="chip on" data-rm="' + esc(chips[i].key) + '">' + esc(chips[i].label) + icon("x") + "</button>";
    }
    return html;
  }

  function resultSignature(item) {
    if (!item) return "";
    var mbps = Number(item.speedMBps);
    if (!isFinite(mbps)) mbps = 0;
    return mbps + "\u0001" + (item.error ? String(item.error) : "") + "\u0001" + (item.testedAt == null ? "" : String(item.testedAt));
  }

  function speedFields(source) {
    source = source || {};
    return {
      running: !!source.running,
      done: Number(source.done) || 0,
      total: Number(source.total) || 0,
      current: source.current ? String(source.current) : "",
      taskId: source.taskId ? String(source.taskId) : "",
      finishedTask: source.finishedTask ? String(source.finishedTask) : ""
    };
  }

  function reconcileSpeed(local, remote) {
    local = local || {};
    remote = remote || {};
    var progress = remote.progress || {};
    var known = speedFields(local);
    var running = !!progress.running;
    var done = Number(progress.done) || 0;
    var taskId = progress.taskId ? String(progress.taskId) : known.taskId;
    var current = progress.currentNode ? String(progress.currentNode) : "";
    var seen = local.seen || {};
    if (known.running && !running && !known.taskId && !progress.taskId) {
      return { changed: false, polling: true, speed: known, apply: [], seen: seen };
    }
    if (running && known.finishedTask && taskId === known.finishedTask && done <= known.done) {
      return { changed: false, polling: false, speed: known, apply: [], seen: seen };
    }
    if (running && known.running && taskId && known.taskId && taskId === known.taskId && done < known.done) {
      return { changed: false, polling: true, speed: known, apply: [], seen: seen };
    }
    var results = {};
    var raw = remote.results || {};
    Object.keys(raw).forEach(function (name) {
      if (raw[name]) results[name] = raw[name];
    });
    if (progress.lastResult && progress.lastResult.nodeName && !results[progress.lastResult.nodeName]) {
      results[progress.lastResult.nodeName] = progress.lastResult;
    }
    var nextSeen = {};
    Object.keys(seen).forEach(function (name) { nextSeen[name] = seen[name]; });
    var apply = [];
    Object.keys(results).forEach(function (name) {
      var item = results[name];
      var id = item && item.nodeName ? String(item.nodeName) : String(name);
      var key = resultSignature(item);
      if (nextSeen[id] !== key) apply.push(item);
      nextSeen[id] = key;
    });
    var speed = {
      running: running,
      done: done,
      total: Number(progress.total) || known.total,
      current: running ? current : "",
      taskId: taskId,
      finishedTask: running ? "" : (taskId || known.finishedTask)
    };
    var changed = apply.length > 0
      || known.running !== speed.running
      || known.done !== speed.done
      || known.total !== speed.total
      || known.current !== speed.current
      || known.taskId !== speed.taskId;
    return { changed: changed, polling: running, speed: speed, apply: apply, seen: nextSeen };
  }

  function buildNodeName(el, name) {
    var text = name == null ? "" : String(name);
    return el("span", { class: "pbn-name", text: text });
  }

  var api = {
    STORAGE_KEY: STORAGE_KEY,
    GROUP_PROXY: GROUP_PROXY,
    GROUP_AI: GROUP_AI,
    REGION_ORDER: REGION_ORDER,
    defaultGroupFilter: defaultGroupFilter,
    defaultSortDir: defaultSortDir,
    sanitizeFilter: sanitizeFilter,
    loadFilters: loadFilters,
    saveFilters: saveFilters,
    detectRegion: detectRegion,
    regionLabel: regionLabel,
    regionCode: regionCode,
    regionColor: regionColor,
    regionCounts: regionCounts,
    parseRate: parseRate,
    formatRate: formatRate,
    activeFilterCount: activeFilterCount,
    isFilterActive: isFilterActive,
    isUtilityNode: isUtilityNode,
    delayThreshold: delayThreshold,
    speedThreshold: speedThreshold,
    applyFilters: applyFilters,
    sortNodes: sortNodes,
    emptyReason: emptyReason,
    scopeTargets: scopeTargets,
    speedTestNames: speedTestNames,
    delayLabel: delayLabel,
    speedLabel: speedLabel,
    delayBadge: delayBadge,
    speedBadge: speedBadge,
    emptyChips: emptyChips,
    emptyChipsHTML: emptyChipsHTML,
    clearChip: clearChip,
    nodeCardHTML: nodeCardHTML,
    nodeRowHTML: nodeRowHTML,
    reconcileSpeed: reconcileSpeed,
    buildNodeName: buildNodeName,
  };

  if (typeof module !== "undefined" && module && module.exports) module.exports = api;
  if (root) root.PBNodeFilter = api;
})(typeof window !== "undefined" ? window : globalThis);
