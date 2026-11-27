"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const F = require("../ui/nodes-filter.js");

function memStorage(initial) {
  const map = new Map();
  const seed = initial || {};
  Object.keys(seed).forEach(function (key) {
    map.set(key, seed[key]);
  });
  return {
    getItem: function (key) {
      return map.has(key) ? map.get(key) : null;
    },
    setItem: function (key, value) {
      map.set(key, String(value));
    },
  };
}

function node(over) {
  return Object.assign({
    name: "HK 1",
    type: "vmess",
    typeLabel: "VMess",
    provider: "alpha",
    subName: "机场甲",
    region: "hk",
    regionLabel: "🇭🇰 香港",
    delayMs: 200,
    untested: false,
    alive: true,
    isAuto: false,
    speedMBps: 5,
    speedTested: true,
    speedError: "",
  }, over);
}

test("detectRegion matches each region in Chinese, flag, English, and abbreviation", function () {
  const samples = [
    ["香港 01", "hk"],
    ["🇭🇰 IEPL", "hk"],
    ["Hong Kong 02", "hk"],
    ["HK-03", "hk"],
    ["港2 专线", "hk"],
    ["hkbn", "hk"],
    ["台湾 01", "tw"],
    ["台灣 02", "tw"],
    ["🇹🇼 家宽", "tw"],
    ["Taiwan 03", "tw"],
    ["TW-04", "tw"],
    ["日本 01", "jp"],
    ["🇯🇵 东京", "jp"],
    ["Japan 02", "jp"],
    ["JP-03", "jp"],
    ["Tokyo 04", "jp"],
    ["新加坡 01", "sg"],
    ["狮城 02", "sg"],
    ["🇸🇬 家宽", "sg"],
    ["Singapore 03", "sg"],
    ["SG-04", "sg"],
    ["美国 01", "us"],
    ["🇺🇸 洛杉矶", "us"],
    ["United States 02", "us"],
    ["US-03", "us"],
    ["USA-04", "us"],
    ["韩国 01", "kr"],
    ["🇰🇷 首尔", "kr"],
    ["Korea 02", "kr"],
    ["KR-03", "kr"],
    ["Seoul 04", "kr"],
    ["英国 01", "uk"],
    ["🇬🇧 伦敦", "uk"],
    ["London 02", "uk"],
    ["UK-03", "uk"],
    ["GB-04", "uk"],
    ["德国 01", "de"],
    ["🇩🇪 法兰克福", "de"],
    ["Germany 02", "de"],
    ["DE-03", "de"],
    ["Frankfurt 04", "de"],
    ["法国 01", "eu"],
    ["🇫🇷 家宽", "eu"],
    ["France 02", "eu"],
    ["FR-03", "eu"],
    ["NL-04", "eu"],
  ];
  samples.forEach(function (pair) {
    assert.equal(F.detectRegion(pair[0]), pair[1], pair[0]);
  });
});

test("detectRegion keeps in-word fragments and unknown places in the right bucket", function () {
  assert.equal(F.detectRegion("scan-relay"), "other");
  assert.equal(F.detectRegion("reconnect-01"), "other");
  assert.equal(F.detectRegion("CN-2"), "other");
  assert.equal(F.detectRegion("think"), "other");
  assert.equal(F.detectRegion("chunk"), "other");
  assert.equal(F.detectRegion("myhknet"), "other");
  assert.equal(F.detectRegion("加拿大 01"), "other");
  assert.equal(F.detectRegion("Sydney-1"), "other");
  assert.equal(F.detectRegion("土耳其 01"), "other");
  assert.equal(F.regionLabel("other"), "其他");
  assert.equal(F.regionLabel("hk"), "香港");
  assert.equal(F.regionCode("hk"), "HK");
  assert.equal(F.regionCode("uk"), "GB");
  assert.equal(F.regionCode("other"), "··");
  assert.equal(F.regionLabel("nope"), "");
  assert.equal(F.regionCode("nope"), "");
  F.REGION_ORDER.forEach(function (key) {
    var shown = F.regionLabel(key) + F.regionCode(key);
    assert.equal(/\uD83C[\uDDE6-\uDDFF]/.test(shown), false, key);
    assert.ok(F.regionCode(key).length >= 2, key);
  });
});

test("detectRegion gives DIRECT, REJECT, and auto groups no region", function () {
  assert.equal(F.detectRegion("DIRECT"), "");
  assert.equal(F.detectRegion("direct"), "");
  assert.equal(F.detectRegion("REJECT"), "");
  assert.equal(F.detectRegion("Reject"), "");
  assert.equal(F.detectRegion("⚡ AUTO"), "");
  assert.equal(F.detectRegion("⚡ AI-Auto"), "");
  assert.equal(F.detectRegion("⚡ ai-auto"), "");
  assert.equal(F.detectRegion(""), "");
  assert.equal(F.detectRegion("香港 日本"), "hk");
});

test("regionCounts keeps only regions with a positive count", function () {
  const counts = F.regionCounts([
    { region: "hk" },
    { region: "hk" },
    { region: "us" },
    { region: "" },
    { region: "nope" },
    { name: "plain" },
    { region: "other" },
  ]);
  assert.deepEqual(counts, { hk: 2, us: 1, other: 1 });
});

test("two groups keep independent filters across a storage round trip", function () {
  const store = memStorage();
  const filters = F.loadFilters(store);
  filters[F.GROUP_PROXY].query = "proxy-q";
  filters[F.GROUP_PROXY].region = "hk";
  filters[F.GROUP_PROXY].sortKey = "name";
  filters[F.GROUP_PROXY].sortDir = 1;
  filters[F.GROUP_AI].region = "sg";
  filters[F.GROUP_AI].delayPreset = "200";
  filters[F.GROUP_AI].maxDelayMs = 200;
  filters[F.GROUP_AI].speedPreset = "custom";
  filters[F.GROUP_AI].minSpeedMBps = 1.5;
  filters[F.GROUP_AI].sortKey = "speed";
  filters[F.GROUP_AI].sortDir = -1;
  filters[F.GROUP_AI].query = "ai-q";
  F.saveFilters(store, filters);

  const again = F.loadFilters(store);
  assert.equal(again[F.GROUP_PROXY].query, "proxy-q");
  assert.equal(again[F.GROUP_PROXY].region, "hk");
  assert.equal(again[F.GROUP_PROXY].delayPreset, "all");
  assert.equal(again[F.GROUP_PROXY].sortKey, "name");
  assert.equal(again[F.GROUP_AI].query, "ai-q");
  assert.equal(again[F.GROUP_AI].region, "sg");
  assert.equal(again[F.GROUP_AI].delayPreset, "200");
  assert.equal(again[F.GROUP_AI].maxDelayMs, 200);
  assert.equal(again[F.GROUP_AI].minSpeedMBps, 1.5);
  assert.equal(again[F.GROUP_AI].sortKey, "speed");
  assert.equal(again[F.GROUP_AI].sortDir, -1);
  assert.notEqual(again[F.GROUP_PROXY], again[F.GROUP_AI]);

  again[F.GROUP_AI].query = "changed";
  again[F.GROUP_AI].region = "jp";
  assert.equal(again[F.GROUP_PROXY].query, "proxy-q");
  assert.equal(again[F.GROUP_PROXY].region, "hk");
});

test("storage tolerates corrupt, missing, and old payloads", function () {
  assert.doesNotThrow(function () {
    F.loadFilters({ getItem: function () { throw new Error("boom"); } });
  });
  assert.doesNotThrow(function () {
    F.saveFilters({ setItem: function () { throw new Error("boom"); } }, F.loadFilters(null));
  });
  assert.deepEqual(F.loadFilters(null)[F.GROUP_PROXY], F.defaultGroupFilter());
  assert.deepEqual(F.loadFilters(memStorage())[F.GROUP_AI], F.defaultGroupFilter());

  ["{", "null", "[]", "\"x\"", "42", ""].forEach(function (raw) {
    const store = memStorage();
    if (raw !== "") store.setItem(F.STORAGE_KEY, raw);
    const loaded = F.loadFilters(store);
    assert.deepEqual(loaded[F.GROUP_PROXY], F.defaultGroupFilter());
    assert.deepEqual(loaded[F.GROUP_AI], F.defaultGroupFilter());
  });

  const partial = memStorage();
  partial.setItem(F.STORAGE_KEY, JSON.stringify({
    v: 1,
    groups: { PROXY: { region: "NOPE", delayPreset: "nope", sortDir: 9, maxDelayMs: -5 } },
  }));
  const one = F.loadFilters(partial);
  assert.equal(one[F.GROUP_PROXY].region, "all");
  assert.equal(one[F.GROUP_PROXY].delayPreset, "all");
  assert.equal(one[F.GROUP_PROXY].sortDir, 1);
  assert.equal(one[F.GROUP_AI].region, "all");

  const legacy = memStorage();
  legacy.setItem(F.STORAGE_KEY, JSON.stringify({ query: "legacy", sortKey: "name", sortDir: -1 }));
  const old = F.loadFilters(legacy);
  assert.equal(old[F.GROUP_PROXY].query, "legacy");
  assert.equal(old[F.GROUP_AI].query, "legacy");
  assert.equal(old[F.GROUP_PROXY].sortKey, "name");
  assert.equal(old[F.GROUP_PROXY].sortDir, -1);
  old[F.GROUP_PROXY].query = "only-proxy";
  assert.equal(old[F.GROUP_AI].query, "legacy");
});

test("delay and speed filters use the design boundaries and AND together", function () {
  const rows = [
    node({ name: "exact" }),
    node({ name: "just-over", delayMs: 200.1 }),
    node({ name: "timeout", delayMs: 0, untested: false, alive: false }),
    node({ name: "untested", delayMs: 80, untested: true, alive: false }),
    node({ name: "fast-alive", delayMs: 5000 }),
    node({ name: "speed-eq", speedMBps: 5 }),
    node({ name: "speed-under", speedMBps: 4.99 }),
    node({ name: "speed-err", speedMBps: 20, speedError: "boom" }),
    node({ name: "speed-none", speedTested: false, speedMBps: 0, speedError: "" }),
    node({ name: "sg", region: "sg", regionLabel: "🇸🇬 新加坡" }),
    node({ name: "other-sub", provider: "beta", subName: "机场乙" }),
  ];

  const delay = F.defaultGroupFilter();
  delay.delayPreset = "200";
  delay.maxDelayMs = 200;
  const delayNames = F.applyFilters(rows, delay).map(function (item) { return item.name; });
  assert.ok(delayNames.indexOf("exact") >= 0);
  assert.ok(delayNames.indexOf("speed-eq") >= 0);
  assert.equal(delayNames.indexOf("just-over"), -1);
  assert.equal(delayNames.indexOf("timeout"), -1);
  assert.equal(delayNames.indexOf("untested"), -1);

  const alive = F.defaultGroupFilter();
  alive.delayPreset = "alive";
  const aliveNames = F.applyFilters(rows, alive).map(function (item) { return item.name; });
  assert.ok(aliveNames.indexOf("fast-alive") >= 0);
  assert.equal(aliveNames.indexOf("timeout"), -1);
  assert.equal(aliveNames.indexOf("untested"), -1);

  const speed = F.defaultGroupFilter();
  speed.speedPreset = "5";
  speed.minSpeedMBps = 5;
  const speedNames = F.applyFilters(rows, speed).map(function (item) { return item.name; });
  assert.ok(speedNames.indexOf("speed-eq") >= 0);
  assert.ok(speedNames.indexOf("exact") >= 0);
  assert.equal(speedNames.indexOf("speed-under"), -1);
  assert.equal(speedNames.indexOf("speed-err"), -1);
  assert.equal(speedNames.indexOf("speed-none"), -1);

  const both = F.defaultGroupFilter();
  both.region = "hk";
  both.sub = "alpha";
  both.delayPreset = "200";
  both.maxDelayMs = 200;
  both.speedPreset = "5";
  both.minSpeedMBps = 5;
  both.query = "vmess";
  const bothNames = F.applyFilters(rows, both).map(function (item) { return item.name; });
  assert.deepEqual(bothNames, ["exact", "speed-eq"]);
});

test("an active filter drops auto, DIRECT, and REJECT", function () {
  const rows = [
    { name: "⚡ AUTO", isAuto: true, region: "", delayMs: 10, untested: false, speedTested: false },
    { name: "DIRECT", type: "direct", region: "", delayMs: 10, untested: false },
    { name: "REJECT", type: "reject", region: "" },
    node({ name: "HK 1", delayMs: 50, speedMBps: 8 }),
  ];
  assert.equal(F.applyFilters(rows, F.defaultGroupFilter()).length, 4);
  assert.equal(F.isFilterActive(F.defaultGroupFilter()), false);
  assert.equal(F.activeFilterCount(F.defaultGroupFilter()), 0);

  const region = F.defaultGroupFilter();
  region.region = "hk";
  assert.equal(F.isFilterActive(region), true);
  assert.deepEqual(F.applyFilters(rows, region).map(function (item) { return item.name; }), ["HK 1"]);

  const sortOnly = F.defaultGroupFilter();
  sortOnly.sortKey = "speed";
  sortOnly.sortDir = -1;
  assert.equal(F.isFilterActive(sortOnly), false);
  assert.equal(F.speedTestNames(rows, sortOnly), null);

  const names = F.speedTestNames(rows, region);
  assert.deepEqual(names, ["HK 1"]);
  assert.equal(names.indexOf("DIRECT"), -1);
  assert.equal(names.indexOf("REJECT"), -1);
  assert.equal(names.indexOf("⚡ AUTO"), -1);
});

test("emptyReason names why the current filter matched nothing", function () {
  const bare = [node({
    name: "HK 1",
    speedTested: false,
    speedMBps: 0,
    speedError: "",
    untested: true,
    delayMs: 0,
    alive: false,
  })];
  const speed = F.defaultGroupFilter();
  speed.speedPreset = "tested";
  assert.equal(F.emptyReason(bare, speed), "当前分组尚无网速记录，可先点击「测网速」");
  assert.equal(F.emptyReason(bare, F.defaultGroupFilter()), "");

  const delay = F.defaultGroupFilter();
  delay.delayPreset = "alive";
  assert.equal(F.emptyReason(bare, delay), "当前分组尚无延迟记录，可先点击「测延迟」");

  const recorded = [node({ name: "HK 1", speedMBps: 3, delayMs: 400 })];
  const tooFast = F.defaultGroupFilter();
  tooFast.speedPreset = "20";
  tooFast.minSpeedMBps = 20;
  assert.equal(F.emptyReason(recorded, tooFast), "没有达到当前网速要求的节点");

  const tooSlow = F.defaultGroupFilter();
  tooSlow.delayPreset = "100";
  tooSlow.maxDelayMs = 100;
  assert.equal(F.emptyReason(recorded, tooSlow), "没有符合当前延迟条件的节点");

  const mixed = [
    node({ name: "hk-slow", region: "hk", delayMs: 400, speedMBps: 8 }),
    node({ name: "sg-fast", region: "sg", regionLabel: "🇸🇬 新加坡", delayMs: 80, speedMBps: 8 }),
  ];
  const combo = F.defaultGroupFilter();
  combo.region = "hk";
  combo.delayPreset = "200";
  combo.maxDelayMs = 200;
  assert.equal(F.applyFilters(mixed, combo).length, 0);
  assert.equal(F.emptyReason(mixed, combo), "没有同时满足当前筛选条件的节点");

  const missingRegion = F.defaultGroupFilter();
  missingRegion.region = "uk";
  assert.equal(F.emptyReason(recorded, missingRegion), "当前分组没有该地区的节点");

  const utilities = [
    { name: "DIRECT", type: "direct" },
    { name: "⚡ AUTO", isAuto: true },
  ];
  const active = F.defaultGroupFilter();
  active.region = "hk";
  assert.equal(F.emptyReason(utilities, active), "");
});

test("sort sinks timeouts and untested nodes", function () {
  const rows = [
    { name: "slow", delayMs: 300, untested: false, isAuto: false, speedMBps: 1, speedTested: true, speedError: "" },
    { name: "fast", delayMs: 20, untested: false, isAuto: false, speedMBps: 9, speedTested: true, speedError: "" },
    { name: "dead", delayMs: 0, untested: false, isAuto: false, speedMBps: 0, speedTested: true, speedError: "x" },
    { name: "none", delayMs: 0, untested: true, isAuto: false, speedMBps: 0, speedTested: false, speedError: "" },
    { name: "auto", delayMs: 1, untested: false, isAuto: true, speedMBps: 1, speedTested: true, speedError: "" },
  ];
  const asc = F.defaultGroupFilter();
  assert.deepEqual(F.sortNodes(rows, asc).map(function (item) { return item.name; }), ["auto", "fast", "slow", "dead", "none"]);
  const desc = F.defaultGroupFilter();
  desc.sortDir = -1;
  assert.deepEqual(F.sortNodes(rows, desc).map(function (item) { return item.name; }), ["auto", "slow", "fast", "dead", "none"]);
  const speed = F.defaultGroupFilter();
  speed.sortKey = "speed";
  speed.sortDir = -1;
  assert.deepEqual(F.sortNodes(rows, speed).map(function (item) { return item.name; }), ["auto", "fast", "slow", "dead", "none"]);
});

test("a hostile node name stays text through the filter and the DOM builder", function () {
  const hostile = "<img src=x onerror=alert(1)>";
  const rows = [node({ name: hostile, region: "other", regionLabel: "🌐 其他地区" })];
  const out = F.applyFilters(rows, F.defaultGroupFilter());
  assert.equal(out.length, 1);
  assert.equal(out[0].name, hostile);
  assert.equal(out[0], rows[0]);

  function el(tag, props) {
    if (props && (Object.prototype.hasOwnProperty.call(props, "html") || Object.prototype.hasOwnProperty.call(props, "innerHTML"))) {
      throw new Error("refused html");
    }
    const nodeEl = {
      nodeName: String(tag).toUpperCase(),
      className: props && props.class ? props.class : "",
      childNodes: [],
      _text: "",
    };
    Object.defineProperty(nodeEl, "textContent", {
      get: function () { return nodeEl._text; },
      set: function (value) {
        nodeEl._text = String(value);
        nodeEl.childNodes = [{ nodeType: 3, textContent: nodeEl._text }];
      },
    });
    if (props && props.text != null) nodeEl.textContent = props.text;
    return nodeEl;
  }

  const built = F.buildNodeName(el, hostile);
  assert.equal(built.nodeName, "SPAN");
  assert.equal(built.className, "pbn-name");
  assert.equal(built.childNodes.length, 1);
  assert.equal(built.childNodes[0].nodeType, 3);
  assert.equal(built.childNodes[0].textContent, hostile);
  assert.equal(built.textContent, hostile);
});

test("region codes stay two letters and nearby words do not steal a region", function () {
  const samples = {
    hk: "HK-1",
    tw: "TW-1",
    jp: "JP-1",
    sg: "SG-1",
    us: "US-1",
    kr: "KR-1",
    uk: "GB-1",
    de: "DE-1",
    eu: "FR-1",
  };
  Object.keys(samples).forEach(function (key) {
    assert.equal(F.detectRegion(samples[key]), key, samples[key]);
    assert.equal(F.regionCode(key).length, 2);
  });
  assert.equal(F.detectRegion("ARGB-1"), "other");
  assert.equal(F.detectRegion("think"), "other");
  assert.equal(F.detectRegion("chunk"), "other");
  assert.equal(F.detectRegion("myhknet"), "other");
  assert.equal(F.detectRegion("reconnect"), "other");
  assert.equal(F.regionCode("other"), "··");
});

test("an old pb.nodeFilters.v1 payload migrates into both channels and then splits", function () {
  const store = memStorage();
  store.setItem(F.STORAGE_KEY, JSON.stringify({
    query: "legacy",
    region: "uk",
    sub: "alpha",
    delayPreset: "300",
    maxDelayMs: 300,
    speedPreset: "10",
    minSpeedMBps: 10,
    sortKey: "name",
    sortDir: -1,
  }));
  const loaded = F.loadFilters(store);
  assert.equal(loaded[F.GROUP_PROXY].query, "legacy");
  assert.equal(loaded[F.GROUP_PROXY].region, "uk");
  assert.equal(loaded[F.GROUP_PROXY].delayPreset, "300");
  assert.equal(loaded[F.GROUP_AI].region, "uk");
  assert.equal(loaded[F.GROUP_AI].sortDir, -1);
  loaded[F.GROUP_PROXY].query = "only-daily";
  loaded[F.GROUP_PROXY].region = "jp";
  F.saveFilters(store, loaded);
  const again = F.loadFilters(store);
  assert.equal(again[F.GROUP_PROXY].query, "only-daily");
  assert.equal(again[F.GROUP_PROXY].region, "jp");
  assert.equal(again[F.GROUP_AI].query, "legacy");
  assert.equal(again[F.GROUP_AI].region, "uk");
  assert.equal(again[F.GROUP_AI].speedPreset, "10");
});

test("rate multipliers parse the same marks the node names use", function () {
  const cases = [
    ["香港-01-VR-4x", 4],
    ["新加坡-A-01-AT-0.5x", 0.5],
    ["节点×4", 4],
    ["x0.5 香港", 0.5],
    ["4倍专线", 4],
    ["0.5×", 0.5],
    ["美国-NL-02-VR-2x", 2],
    ["X4", 4],
    ["倍率 x0.5", 0.5],
    ["1倍", 1],
    ["1x", 1],
    ["普通节点", null],
    ["4xbox", null],
    ["max4", null],
    ["think", null],
  ];
  cases.forEach(function (pair) {
    assert.equal(F.parseRate(pair[0]), pair[1], pair[0]);
  });
  assert.equal(F.formatRate(4), "4");
  assert.equal(F.formatRate(0.5), "0.5");
});

test("delay and speed badges use the tier boundaries", function () {
  assert.equal(F.delayBadge({ untested: false, delayMs: 200 }).cls, "metric d-good");
  assert.equal(F.delayBadge({ untested: false, delayMs: 201 }).cls, "metric d-mid");
  assert.equal(F.delayBadge({ untested: false, delayMs: 500 }).cls, "metric d-mid");
  assert.equal(F.delayBadge({ untested: false, delayMs: 501 }).cls, "metric d-bad");
  assert.equal(F.delayBadge({ untested: false, delayMs: 0 }).text, "超时");
  assert.equal(F.delayBadge({ untested: false, delayMs: 0 }).cls, "metric d-bad");
  assert.equal(F.delayBadge({ untested: true, delayMs: 20 }).text, "未测速");

  assert.equal(F.speedBadge({ speedTested: true, speedMBps: 0.99, speedError: "" }).cls, "metric speed spd-low");
  assert.equal(F.speedBadge({ speedTested: true, speedMBps: 1, speedError: "" }).cls, "metric speed spd-mid");
  assert.equal(F.speedBadge({ speedTested: true, speedMBps: 4.99, speedError: "" }).cls, "metric speed spd-mid");
  assert.equal(F.speedBadge({ speedTested: true, speedMBps: 5, speedError: "" }).cls, "metric speed spd-good");
  assert.equal(F.speedBadge({ speedTested: true, speedMBps: 20, speedError: "boom" }).text, "失败");
  assert.equal(F.speedBadge({ speedTested: false, speedMBps: 0, speedError: "" }).text, "未测速");
  assert.equal(F.speedBadge({ speedTested: false }, { speedPhase: "current" }).text, "测速中");
});

test("scope targets are the filtered real nodes and null when the filter is idle", function () {
  const rows = [
    { name: "⚡ AUTO", isAuto: true, region: "", delayMs: 10, untested: false },
    { name: "DIRECT", type: "direct", region: "", delayMs: 10, untested: false },
    { name: "REJECT", type: "reject", region: "" },
    node({ name: "HK 1", delayMs: 50, speedMBps: 8 }),
    node({ name: "SG 1", region: "sg", delayMs: 50, speedMBps: 8 }),
  ];
  assert.equal(F.scopeTargets(rows, F.defaultGroupFilter()), null);
  const region = F.defaultGroupFilter();
  region.region = "hk";
  assert.deepEqual(F.scopeTargets(rows, region), ["HK 1"]);
  assert.deepEqual(F.speedTestNames(rows, region), ["HK 1"]);
  const miss = F.defaultGroupFilter();
  miss.region = "de";
  assert.deepEqual(F.scopeTargets(rows, miss), []);
});

test("empty-state chips list each active condition and one chip can be removed", function () {
  const filter = F.defaultGroupFilter();
  filter.query = "机场";
  filter.sub = "alpha";
  filter.region = "hk";
  filter.delayPreset = "200";
  filter.maxDelayMs = 200;
  filter.speedPreset = "custom";
  filter.minSpeedMBps = 1.5;
  const chips = F.emptyChips(filter, function (id) { return id === "alpha" ? "机场甲" : id; });
  assert.deepEqual(chips.map(function (chip) { return chip.key; }), ["query", "sub", "region", "delay", "speed"]);
  assert.equal(chips[1].label, "订阅：机场甲");
  assert.equal(chips[2].label, "地区：香港");
  assert.equal(chips[3].label, "延迟 ≤ 200 ms");
  assert.equal(chips[4].label, "网速 ≥ 1.5 MB/s");
  const next = F.clearChip(filter, "delay");
  assert.equal(next.delayPreset, "all");
  assert.equal(filter.delayPreset, "200");
  assert.equal(next.region, "hk");
  assert.deepEqual(F.emptyChips(next).map(function (chip) { return chip.key; }), ["query", "sub", "region", "speed"]);
});

test("html builders escape a hostile name in cards, rows, and empty chips", function () {
  const hostile = "<img src=x onerror=alert(1)>";
  const broke = '"><img src=x onerror=alert(1)>';
  const row = node({
    name: hostile,
    type: hostile,
    provider: hostile,
    subName: hostile,
    region: "other",
    speedError: hostile,
    speedTested: true,
    speedMBps: 3,
    delayMs: 10,
    untested: false,
  });
  [F.nodeCardHTML(row), F.nodeRowHTML(row), F.nodeCardHTML(node({ name: broke }))].forEach(function (html) {
    assert.equal(html.indexOf("<img"), -1, html);
    assert.ok(html.indexOf("&lt;img src=x onerror=alert(1)&gt;") >= 0, html);
  });
  const filter = F.defaultGroupFilter();
  filter.query = hostile;
  const chips = F.emptyChipsHTML(filter);
  assert.equal(chips.indexOf("<img"), -1);
  assert.ok(chips.indexOf("&lt;img src=x onerror=alert(1)&gt;") >= 0);
  assert.ok(chips.indexOf('data-rm="query"') >= 0);
});

test("node rows patch changed names and rebuild when the order changes", function () {
  const prev = [
    { name: "甲", sig: F.rowSignature({ name: "甲", delayMs: 10, untested: false }, {}) },
    { name: "乙", sig: F.rowSignature({ name: "乙", delayMs: 20, untested: false }, {}) }
  ];
  const same = [
    { name: "甲", sig: prev[0].sig },
    { name: "乙", sig: prev[1].sig }
  ];
  assert.deepEqual(F.diffRows(prev, same), { same: true, reorder: false, changed: [] });
  const next = [
    { name: "甲", sig: F.rowSignature({ name: "甲", delayMs: 80, untested: false }, {}) },
    { name: "乙", sig: prev[1].sig }
  ];
  assert.deepEqual(F.diffRows(prev, next), { same: false, reorder: false, changed: ["甲"] });
  const moved = [same[1], same[0]];
  assert.equal(F.diffRows(prev, moved).reorder, true);
  assert.equal(F.rowSignature({ name: "甲", delayMs: "10" }, {}), F.rowSignature({ name: "甲", delayMs: 10 }, {}));
});

test("speed status applies each result once and ignores a stale frame", function () {
  const local = { running: true, done: 1, total: 3, current: "甲", taskId: "t1", seen: {} };
  const first = F.reconcileSpeed(local, {
    progress: { running: true, done: 2, total: 3, currentNode: "乙", taskId: "t1", lastResult: { nodeName: "甲", speedMBps: 4, testedAt: 10 } }
  });
  assert.equal(first.changed, true);
  assert.equal(first.polling, true);
  assert.equal(first.speed.done, 2);
  assert.equal(first.apply.length, 1);
  assert.equal(first.apply[0].nodeName, "甲");
  const again = F.reconcileSpeed({
    running: true, done: 2, total: 3, current: "乙", taskId: "t1", seen: first.seen
  }, {
    progress: { running: true, done: 2, total: 3, currentNode: "乙", taskId: "t1", lastResult: { nodeName: "甲", speedMBps: 4, testedAt: 10 } },
    results: { 甲: { nodeName: "甲", speedMBps: 4, testedAt: 10 } }
  });
  assert.equal(again.changed, false);
  assert.equal(again.apply.length, 0);
  assert.equal(again.polling, true);
  const stale = F.reconcileSpeed({
    running: true, done: 2, total: 3, current: "乙", taskId: "t1", seen: first.seen
  }, { progress: { running: true, done: 1, total: 3, taskId: "t1", currentNode: "甲" } });
  assert.equal(stale.changed, false);
  assert.equal(stale.speed.done, 2);
  const early = F.reconcileSpeed({ running: true, done: 0, total: 2, taskId: "", seen: {} }, {
    progress: { running: false, done: 0, total: 0 },
    results: {}
  });
  assert.equal(early.changed, false);
  assert.equal(early.polling, true);
  assert.equal(early.speed.running, true);
  const done = F.reconcileSpeed({
    running: true, done: 2, total: 3, taskId: "t1", seen: first.seen
  }, {
    progress: { running: false, done: 3, total: 3, taskId: "t1" },
    results: { 乙: { nodeName: "乙", speedMBps: 8, error: "", testedAt: 11 } }
  });
  assert.equal(done.changed, true);
  assert.equal(done.polling, false);
  assert.equal(done.speed.running, false);
  assert.equal(done.apply.length, 1);
  assert.equal(done.apply[0].nodeName, "乙");
  const late = F.reconcileSpeed({
    running: false, done: 3, total: 3, taskId: "t1", finishedTask: done.speed.finishedTask, seen: done.seen
  }, { progress: { running: true, done: 2, total: 3, taskId: "t1", currentNode: "乙" } });
  assert.equal(late.changed, false);
  assert.equal(late.polling, false);
});
