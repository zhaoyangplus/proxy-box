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
  assert.equal(F.regionLabel("other"), "🌐 其他地区");
  assert.equal(F.regionLabel("hk"), "🇭🇰 香港");
  assert.equal(F.regionLabel("nope"), "");
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
