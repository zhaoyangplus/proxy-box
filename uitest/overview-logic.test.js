"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");

globalThis.PBUtil = require("../ui/util.js");
const O = require("../ui/overview-logic.js");

const HOSTILE = '<img src=x onerror=alert(1)>';

test("core start settles to error, running, or stopped without reviving download", function () {
  assert.deepEqual(O.settleStart({ cancelled: true, error: "已取消" }), { phase: "stopped", error: "", warning: "" });
  assert.deepEqual(O.settleStart({ error: "内核下载失败" }), { phase: "error", error: "内核下载失败", warning: "" });
  const warned = O.settleStart({ warning: "系统代理没有打开", running: false });
  assert.equal(warned.phase, "running");
  assert.equal(warned.error, "");
  assert.equal(warned.warning, "系统代理没有打开");
  assert.equal(O.settleStart({ running: true }).phase, "running");
  assert.equal(O.settleStart({}).phase, "running");
  assert.equal(O.settleStart({ running: false }).phase, "stopped");

  assert.equal(O.nextCorePhase({ running: false, phase: "download", holdDownload: true }), "download");
  assert.equal(O.nextCorePhase({ running: false, phase: "download", holdDownload: false }), "stopped");
  assert.equal(O.nextCorePhase({ running: false, phase: "download", holdDownload: true, cancelRequested: true }), "stopped");
  assert.equal(O.nextCorePhase({ running: true, phase: "download", holdDownload: true }), "running");
  assert.equal(O.nextCorePhase({ running: false, phase: "error", holdDownload: false }), "error");
  assert.equal(O.nextCorePhase({ running: false, phase: "geo" }), "geo");

  assert.equal(O.applyCorePhase({ phase: "download", holdDownload: true }, { phase: "download" }), "download");
  assert.equal(O.applyCorePhase({ phase: "download", holdDownload: true }, { phase: "error" }), "error");
  assert.equal(O.applyCorePhase({ phase: "error", holdDownload: false }, { phase: "download" }), "error");
  assert.equal(O.applyCorePhase({ phase: "error", holdDownload: false }, { phase: "error" }), "error");
  assert.equal(O.applyCorePhase({ phase: "running", holdDownload: false }, { phase: "download" }), "running");
  assert.equal(O.applyCorePhase({ phase: "stopped", holdDownload: false }, { phase: "download" }), "stopped");
  assert.equal(O.applyCorePhase({ phase: "download", cancelRequested: true }, { phase: "error" }), "stopped");
  assert.equal(O.applyCorePhase({ phase: "running", cancelRequested: true }, { phase: "stopped" }), "stopped");
});

test("mode text follows the three routing modes", function () {
  assert.equal(O.modeText("rule"), O.MODE_TEXT.rule);
  assert.equal(O.modeText("global"), O.MODE_TEXT.global);
  assert.equal(O.modeText("direct"), O.MODE_TEXT.direct);
  assert.equal(O.modeLabel("global"), "全局代理");
  assert.equal(O.modeText("weird"), "当前模式无法识别。");
  assert.equal(O.modeLabel("weird"), "未知模式");
});

test("channel tiles use the selected node and the resolved leaf", function () {
  assert.equal(O.GROUP.daily, "PROXY");
  assert.equal(O.GROUP.ai, "🤖 AI-IDE");
  const stopped = O.channelTile("daily", null, false, 27890);
  assert.equal(stopped.running, false);
  assert.equal(stopped.name, "内核未运行");
  assert.equal(stopped.speedLabel, "");

  const list = {
    currentSelect: "⚡ AUTO",
    resolvedNode: "新加坡-02",
    nodes: [
      { name: "⚡ AUTO", isAuto: true, delayMs: 10, untested: false },
      { name: "新加坡-02", delayMs: 180, speedMBps: 4.25, speedTested: true, untested: false },
      { name: "香港-02", delayMs: 0, untested: false, alive: false }
    ]
  };
  const tile = O.channelTile("ai", list, true, 27892);
  assert.equal(tile.name, "⚡ AUTO");
  assert.equal(tile.via, "新加坡-02");
  assert.equal(tile.delay.kind, "value");
  assert.equal(tile.delay.label, "180");
  assert.equal(tile.delay.cls, "d-mid");
  assert.equal(tile.speedLabel, "4.3");
  assert.equal(tile.port, 27892);

  const plain = O.channelTile("daily", {
    currentSelect: "日本-01",
    resolvedNode: "日本-01",
    nodes: [{ name: "日本-01", delayMs: 80, untested: false }]
  }, true, 27890);
  assert.equal(plain.via, "");
  assert.equal(plain.delay.cls, "d-good");
  assert.equal(plain.speedLabel, "");

  const slow = O.channelTile("daily", {
    currentSelect: "美国-09",
    resolvedNode: "美国-09",
    nodes: [{ name: "美国-09", delayMs: 400, untested: false, speedTested: true, speedMBps: 0 }]
  }, true, 1);
  assert.equal(slow.delay.cls, "d-bad");
  assert.equal(slow.speedLabel, "0.0");

  const dead = O.channelTile("daily", {
    currentSelect: "香港-02",
    resolvedNode: "香港-02",
    nodes: [{ name: "香港-02", delayMs: 0, untested: false }]
  }, true, 1);
  assert.equal(dead.delay.kind, "timeout");

  const fresh = O.channelTile("daily", {
    currentSelect: "新节点",
    resolvedNode: "新节点",
    nodes: [{ name: "新节点", untested: true, delayMs: 0 }]
  }, true, 1);
  assert.equal(fresh.delay.kind, "untested");
  assert.equal(O.channelTile("ai", undefined, true, 1).name, "正在读取节点");
  assert.equal(O.channelTile("ai", null, true, 1).name, "节点列表暂不可用");
});

test("geo status, hero progress, and jump summaries use real fields only", function () {
  assert.deepEqual(O.geoStatus({ geoError: "地理数据下载失败" }, {}), {
    kind: "error",
    text: "地理数据下载失败",
    retry: true
  });
  const busy = O.geoStatus({ geoDownloading: true }, { phase: "geo", message: "正在下载地理数据", downloaded: 1536, total: 2048 });
  assert.equal(busy.kind, "busy");
  assert.equal(busy.retry, false);
  assert.match(busy.text, /正在下载地理数据 · 1\.5 KB \/ 2\.0 KB/);
  assert.equal(O.geoStatus({ geoReady: true }, {}).text, "GeoSite / GeoIP 已就绪 · 国内站点直连");
  assert.equal(O.geoStatus({ settings: { bypassCN: true } }, {}).kind, "wait");
  assert.equal(O.geoStatus({}, {}).kind, "off");

  const hero = O.heroState({ phase: "download", downloaded: 512, total: 1024, message: "正在下载内核", version: "v1" });
  assert.equal(hero.primary, "cancel");
  assert.equal(hero.showImport, false);
  assert.equal(hero.progress.label, "512 B / 1.0 KB");
  assert.equal(hero.title, "正在启动");
  const failed = O.heroState({ phase: "error", error: "内核下载失败。请手动导入 mihomo.exe/.zip" });
  assert.equal(failed.title, "启动失败");
  assert.equal(failed.showImport, true);
  assert.equal(failed.primary, "start");
  assert.equal(O.heroState({ running: true, version: "v9" }).title, "运行中");
  assert.equal(O.sysSub(false, true, 27890), "内核停止时不可用");
  assert.equal(O.sysSub(true, true, 27890), "已指向 127.0.0.1:27890");

  const now = Date.UTC(2026, 10, 27);
  const summary = O.subsSummary([
    { name: "主力", enabled: true, nodeCount: 4, usage: { expire: Math.floor((now + 3 * 86400000) / 1000) } },
    { name: "停用", enabled: false, nodeCount: 9, usage: { expire: Math.floor(now / 1000) } },
    { name: "很久", enabled: true, nodeCount: 1, usage: { expire: Math.floor((now + 30 * 86400000) / 1000) } }
  ], now);
  assert.equal(summary.enabled, 2);
  assert.equal(summary.nodes, 5);
  assert.equal(summary.soon.name, "主力");
  assert.equal(summary.soon.days, 3);
  assert.equal(O.subsSummary(null, now).state, "error");
  const apps = O.appsSummary({ managedApps: { cursor: true, codex: true, antigravity: false, claude_code: false, extra: true } });
  assert.deepEqual({ on: apps.on, total: apps.total }, { on: 2, total: 4 });
});

test("overview markup escapes hostile names and omits fake traffic", function () {
  const html = O.pageHTML({
    hero: O.heroState({ phase: "error", error: HOSTILE, version: HOSTILE }),
    daily: O.channelTile("daily", {
      currentSelect: HOSTILE,
      resolvedNode: HOSTILE,
      nodes: [{ name: HOSTILE, delayMs: 42, untested: false, speedMBps: 1.5, speedTested: true }]
    }, true, 27890),
    ai: O.channelTile("ai", undefined, false, 27892),
    mode: "rule",
    geo: O.geoStatus({ geoError: HOSTILE }, {}),
    subs: O.subsSummary([{ name: HOSTILE, enabled: true, nodeCount: 1, usage: { expire: 1 } }], Date.now()),
    apps: O.appsSummary(null)
  });
  assert.equal(html.includes("<img"), false);
  assert.equal(html.includes("&lt;img src=x onerror=alert(1)&gt;"), true);
  assert.equal(html.includes('class="spark"'), false);
  assert.equal(html.includes("spark-line"), false);
  assert.equal(html.includes("ovUp"), false);
  assert.equal(html.includes("ovDown"), false);
  assert.match(html, /42<small>ms<\/small>/);
  assert.match(html, /1\.5<small>MB\/s<\/small>/);
  assert.match(html, /id="ovGeoRetry"/);
});
