"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
require("../ui/util.js");
const S = require("../ui/subs-logic.js");

const HOSTILE = "<img src=x onerror=alert(1)>";
const NOW = Date.UTC(2026, 10, 27, 8, 0, 0);

function assertInert(html) {
  assert.equal(html.indexOf("<img"), -1);
  assert.equal(html.indexOf("<script"), -1);
  assert.match(html, /&lt;img src=x onerror=alert\(1\)&gt;/);
}

test("badge color stays inside the palette for the same name", function () {
  const hostile = 'red;}</style>' + HOSTILE;
  assert.equal(S.badgeColor("主力"), S.badgeColor("主力"));
  assert.equal(S.badgeColor(hostile), S.badgeColor(hostile));
  assert.ok(S.PALETTE.indexOf(S.badgeColor(hostile)) >= 0);
  assert.match(S.badgeColor(hostile), /^#[0-9a-f]{6}$/);
});

test("next auto update uses the soonest enabled schedule", function () {
  const soon = {
    id: "b",
    enabled: true,
    updateIntervalMin: 30,
    updatedAt: new Date(NOW - 20 * 60000).toISOString()
  };
  const later = {
    id: "a",
    enabled: true,
    updateIntervalMin: 60,
    updatedAt: new Date(NOW - 30 * 60000).toISOString()
  };
  const manual = { id: "m", enabled: true, updateIntervalMin: 0, updatedAt: new Date(NOW).toISOString() };
  const off = { id: "off", enabled: false, updateIntervalMin: 1, updatedAt: new Date(NOW - 29 * 60000).toISOString() };
  const unscheduled = { id: "z", enabled: true, updateIntervalMin: 60, updatedAt: "" };
  const next = S.nextAutoUpdate([later, soon, manual, off, unscheduled], NOW);
  assert.equal(next.text, "10 分钟后");
  assert.equal(next.at, NOW - 20 * 60000 + 30 * 60000);
  assert.equal(S.nextAutoUpdate([{ enabled: true, updateIntervalMin: 60, updatedAt: new Date(NOW - 2 * 3600000).toISOString() }], NOW).text, "即将");
  assert.equal(S.nextAutoUpdate([manual, off, unscheduled], NOW).text, "\u2014");
  assert.equal(S.nextAutoUpdate([], NOW).text, "\u2014");
});

test("summarize counts nodes on enabled subscriptions only", function () {
  const stats = S.summarize([
    { enabled: true, nodeCount: 4, updateIntervalMin: 0 },
    { enabled: false, nodeCount: 9, updateIntervalMin: 60, updatedAt: new Date(NOW).toISOString() },
    { enabled: true, nodeCount: 1.2, updateIntervalMin: 0 }
  ], NOW);
  assert.equal(stats.enabled, 2);
  assert.equal(stats.total, 3);
  assert.equal(stats.nodes, 5);
  assert.equal(stats.next, "\u2014");
});

test("usage and expiry badges follow the 90 percent and 7 day rules", function () {
  const base = NOW;
  function sub(used, total, expire) {
    return { usage: { upload: 0, download: used, total: total, expire: expire } };
  }
  const day = 86400;
  assert.deepEqual(S.warningBadges(sub(900, 1000, Math.floor(base / 1000) + 30 * day), base).map(function (b) { return b.text; }), ["用量 90%"]);
  assert.deepEqual(S.warningBadges(sub(899, 1000, Math.floor(base / 1000) + 30 * day), base), []);
  assert.equal(S.warningBadges(sub(0, 0, Math.floor(base / 1000) + 7 * day), base)[0].text, "7 天后到期");
  assert.equal(S.warningBadges(sub(0, 0, Math.floor(base / 1000) + 8 * day), base).length, 0);
  assert.equal(S.warningBadges(sub(0, 0, Math.floor(base / 1000) + 3600), base)[0].text, "1 天后到期");
  assert.equal(S.warningBadges(sub(1000, 1000, Math.floor(base / 1000) - day), base).map(function (b) { return b.kind; }).join(","), "past,usage");
  assert.equal(S.warningBadges(sub(0, 0, 0), base).length, 0);
});

test("interval options keep a custom value beside the presets", function () {
  const custom = S.intervalOptions(15);
  assert.equal(custom.active, 15);
  assert.equal(custom.custom, true);
  assert.deepEqual(custom.options.map(function (opt) { return opt.v; }), [0, 30, 60, 360, 720, 1440, 15]);
  assert.equal(custom.options[6].html, "自定义 15 分钟");
  const preset = S.intervalOptions(60);
  assert.equal(preset.custom, false);
  assert.equal(preset.options.length, 6);
  assert.equal(preset.active, 60);
  assert.equal(S.intervalOptions(0).active, 0);
  assert.equal(S.intervalText(15), "每 15 分钟自动更新");
  assert.equal(S.intervalText(0), "仅手动更新");
});

test("diff sends only changed fields and keeps a custom interval", function () {
  const prev = { id: "s1", name: "主力", url: "https://example.com/a", userAgent: "clash", filter: "hk", updateIntervalMin: 15, enabled: true };
  assert.equal(S.diffSub(prev, { name: "主力", url: "https://example.com/a", userAgent: "clash", filter: "hk", updateIntervalMin: 15 }), null);
  assert.deepEqual(S.diffSub(prev, { name: "备用", url: "https://example.com/a", userAgent: "clash", filter: "hk", updateIntervalMin: 15 }), {
    name: "备用",
    id: "s1"
  });
  assert.deepEqual(S.diffSub(prev, { name: "主力", url: "https://example.com/b", userAgent: "", filter: "hk", updateIntervalMin: 30 }), {
    url: "https://example.com/b",
    userAgent: "",
    updateIntervalMin: 30,
    id: "s1"
  });
  assert.deepEqual(S.enabledPatch("s1", false), { id: "s1", enabled: false });
  assert.equal(Object.keys(S.enabledPatch("s1", true)).join(","), "id,enabled");
});

test("subscription url accepts only http and https with a host", function () {
  assert.equal(S.validSubURL(" https://example.com/sub "), true);
  assert.equal(S.validSubURL("http://127.0.0.1:8080/a"), true);
  assert.equal(S.validSubURL("ftp://example.com/a"), false);
  assert.equal(S.validSubURL("https://"), false);
  assert.equal(S.validSubURL("javascript:alert(1)"), false);
  assert.equal(S.validSubURL(""), false);
});

test("subscription html escapes hostile names, urls, and errors", function () {
  const url = "https://evil.example/<img src=x onerror=alert(1)>";
  const html = S.pageHTML([
    {
      id: 'x"><' + HOSTILE,
      name: HOSTILE,
      url: url,
      enabled: true,
      updateIntervalMin: 15,
      updatedAt: new Date(NOW - 5 * 60000).toISOString(),
      nodeCount: 3,
      lastError: HOSTILE,
      usage: { upload: 0, download: 900, total: 1000, expire: Math.floor(NOW / 1000) - 86400 }
    }
  ], { now: NOW, loaded: true, busy: Object.create(null) });
  assertInert(html);
  assert.match(html, /已过期/);
  assert.match(html, /用量 90%/);
  assert.match(html, /每 15 分钟自动更新/);
  assert.match(html, /启用订阅/);
  assert.match(html, /data-act="copy"/);
  assert.equal(html.indexOf(url), -1);
  const form = S.formHTML({
    name: HOSTILE,
    url: url,
    filter: HOSTILE,
    userAgent: HOSTILE,
    updateIntervalMin: 15
  });
  assertInert(form);
  assert.match(form, /class="seg-btn is-active" data-v="15"/);
  assert.match(form, /自定义 15 分钟/);
  assert.doesNotMatch(form, /class="seg-btn is-active" data-v="0"/);
  assert.match(form, /<details class="adv" open>/);
});

test("update summary counts per-item results", function () {
  assert.deepEqual(S.updateSummary([{ id: "a", ok: true }, { id: "b", ok: false, error: "超时" }]), {
    text: "1 个订阅已更新，1 个失败",
    tone: "warn",
    ok: 1,
    fail: 1
  });
  assert.equal(S.updateSummary([{ ok: true }, { ok: true }]).text, "已更新全部 2 个订阅");
  assert.equal(S.errorCount([{ lastError: "失败" }, { lastError: "  " }, { lastError: "" }]), 1);
});
