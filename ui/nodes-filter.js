(function (root) {
  "use strict";

  var STORAGE_KEY = "pb.nodeFilters.v1";
  var GROUP_PROXY = "PROXY";
  var GROUP_AI = "🤖 AI-IDE";

  var REGION_ORDER = ["hk", "tw", "jp", "sg", "us", "kr", "uk", "de", "eu", "other"];

  var REGION_LABEL = {
    hk: "🇭🇰 香港",
    tw: "🇹🇼 台湾",
    jp: "🇯🇵 日本",
    sg: "🇸🇬 新加坡",
    us: "🇺🇸 美国",
    kr: "🇰🇷 韩国",
    uk: "🇬🇧 英国",
    de: "🇩🇪 德国",
    eu: "🇪🇺 欧洲其他",
    other: "🌐 其他地区",
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

  function regionLabel(key) {
    return REGION_LABEL[key] || "";
  }

  function regionCounts(nodes) {
    var counts = {};
    var list = Array.isArray(nodes) ? nodes : [];
    for (var i = 0; i < list.length; i++) {
      var key = list[i] && list[i].region;
      if (!key || !REGION_LABEL[key]) continue;
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
    var fields = [node.name, node.type, node.typeLabel, node.provider, node.subName, node.region, node.regionLabel];
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

  function speedTestNames(nodes, filter) {
    if (!isFilterActive(filter)) return null;
    var rows = applyFilters(nodes, filter);
    var names = [];
    for (var i = 0; i < rows.length; i++) {
      if (isUtilityNode(rows[i])) continue;
      names.push(rows[i].name);
    }
    return names;
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
    regionCounts: regionCounts,
    activeFilterCount: activeFilterCount,
    isFilterActive: isFilterActive,
    isUtilityNode: isUtilityNode,
    delayThreshold: delayThreshold,
    speedThreshold: speedThreshold,
    applyFilters: applyFilters,
    sortNodes: sortNodes,
    emptyReason: emptyReason,
    speedTestNames: speedTestNames,
    buildNodeName: buildNodeName,
  };

  if (typeof module !== "undefined" && module && module.exports) module.exports = api;
  if (root) root.PBNodeFilter = api;
})(typeof window !== "undefined" ? window : globalThis);
