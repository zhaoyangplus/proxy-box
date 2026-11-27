"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const U = require("../ui/util.js");

function storage(initial) {
  const map = new Map();
  Object.keys(initial || {}).forEach(function (key) {
    map.set(key, initial[key]);
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

test("esc encodes the five HTML special characters and empty values", function () {
  assert.equal(U.esc(`&<>"'`), "&amp;&lt;&gt;&quot;&#39;");
  assert.equal(U.esc(null), "");
  assert.equal(U.esc(undefined), "");
  assert.equal(U.esc(12), "12");
});

test("ic and sw build inert markup", function () {
  const icon = U.ic('home"><script>', "lg");
  assert.match(icon, /href="#i-homescript"/);
  assert.equal(icon.includes("<script"), false);
  assert.match(icon, /class="i lg"/);
  assert.equal(U.sw(true, 'data-k="v"', false, "sm").includes('aria-checked="true"'), true);
  assert.match(U.sw(false, "", true, "sm"), /class="sw sm"[^>]*disabled/);
  assert.equal(U.sw(false, "", true).includes('aria-checked="false"'), true);
});

test("ago, uptime, and daysLeft follow the clock boundaries", function () {
  const now = Date.UTC(2026, 10, 27, 8, 0, 0);
  assert.equal(U.ago(now - 20000, now), "刚刚");
  assert.equal(U.ago(now - 5 * 60000, now), "5 分钟前");
  assert.equal(U.ago(now - 60 * 60000, now), "1 小时前");
  assert.equal(U.ago(now - 23 * 3600000, now), "23 小时前");
  assert.equal(U.ago(now - 24 * 3600000, now), "1 天前");
  assert.equal(U.ago("nope", now), "");
  assert.equal(U.uptime(0), "0 分 0 秒");
  assert.equal(U.uptime(90000), "1 分 30 秒");
  assert.equal(U.uptime(3661000), "1 小时 1 分");
  assert.equal(U.uptime(-50), "0 分 0 秒");
  assert.equal(U.daysLeft(now, now), 0);
  assert.equal(U.daysLeft(now + 1, now), 1);
  assert.equal(U.daysLeft(now + 86400000, now), 1);
  const justGone = U.daysLeft(now - 1, now);
  assert.equal(String(justGone), "0");
  assert.equal(justGone < 0, false);
  assert.equal(U.daysLeft(now - 86400000, now), -1);
});

test("bytes, ms, mbps, and date format the values callers render", function () {
  assert.equal(U.bytes(0), "0 B");
  assert.equal(U.bytes(1536), "1.5 KB");
  assert.equal(U.bytes(1024 * 1024), "1.0 MB");
  assert.equal(U.bytes(-4), "0 B");
  assert.equal(U.bytes("x"), "0 B");
  assert.equal(U.ms(1.6), "2 ms");
  assert.equal(U.ms(Infinity), "—");
  assert.equal(U.mbps(1.5), "1.50 MB/s");
  assert.equal(U.mbps(undefined), "—");
  assert.equal(U.mbps(0), "0.00 MB/s");
  const local = new Date(2026, 10, 27, 3, 5);
  assert.equal(U.date(Math.round(local.getTime() / 1000)), "2026-11-27 03:05");
  assert.equal(U.date("not-a-date"), "");
});

test("maskUrl keeps the origin and hides a long path", function () {
  assert.equal(U.maskUrl("https://example.com/abcdefghijklmnop"), "https://example.com/abcdefghijk••••");
  assert.equal(U.maskUrl("https://example.com/short"), "https://example.com/short");
  assert.equal(U.maskUrl("http://a.b/12345678901234"), "http://a.b/12345678901••••");
  assert.equal(U.maskUrl("http://a.b/1234567890123"), "http://a.b/1234567890123");
  assert.equal(U.maskUrl("not a url"), "not a url");
  assert.equal(U.maskUrl(null), "");
});

test("theme resolution reads pb-v22-theme and follows the system only for auto", function () {
  assert.deepEqual(U.themeFromStorage(storage({}), true), { pref: "auto", resolved: "dark" });
  assert.deepEqual(U.themeFromStorage(storage({}), false), { pref: "auto", resolved: "light" });
  assert.deepEqual(U.themeFromStorage(storage({ "pb-v22-theme": "dark" }), false), { pref: "dark", resolved: "dark" });
  assert.deepEqual(U.themeFromStorage(storage({ "pb-v22-theme": "light" }), true), { pref: "light", resolved: "light" });
  assert.deepEqual(U.themeFromStorage(storage({ "pb-v22-theme": "nope" }), true), { pref: "auto", resolved: "dark" });
  assert.deepEqual(U.themeFromStorage({ getItem: function () { throw new Error("blocked"); } }, false), { pref: "auto", resolved: "light" });
  assert.equal(U.normalizeTheme("auto"), "auto");
  assert.equal(U.resolveTheme("dark", false), "dark");
});

function glassStore(raw, throwing) {
  return {
    getItem: function (key) {
      if (throwing) throw new Error("blocked");
      if (key !== U.GLASS_KEY) return null;
      return raw === undefined ? null : raw;
    }
  };
}

test("glass level falls back unless storage holds an integer from 0 to 100", function () {
  assert.equal(U.glassFromStorage(glassStore(undefined)), U.GLASS_DEFAULT);
  assert.equal(U.glassFromStorage(glassStore(null)), U.GLASS_DEFAULT);
  assert.equal(U.glassFromStorage(null), U.GLASS_DEFAULT);
  assert.equal(U.glassFromStorage(glassStore("40")), 40);
  assert.equal(U.glassFromStorage(glassStore("0")), 0);
  assert.equal(U.glassFromStorage(glassStore("100")), 100);
  assert.equal(U.glassFromStorage(glassStore("7")), 7);
  assert.equal(U.glassFromStorage(glassStore("40.5")), U.GLASS_DEFAULT);
  assert.equal(U.glassFromStorage(glassStore("74.0")), U.GLASS_DEFAULT);
  assert.equal(U.glassFromStorage(glassStore("101")), U.GLASS_DEFAULT);
  assert.equal(U.glassFromStorage(glassStore("-1")), U.GLASS_DEFAULT);
  assert.equal(U.glassFromStorage(glassStore("1000")), U.GLASS_DEFAULT);
  assert.equal(U.glassFromStorage(glassStore("nope")), U.GLASS_DEFAULT);
  assert.equal(U.glassFromStorage(glassStore("")), U.GLASS_DEFAULT);
  assert.equal(U.glassFromStorage(glassStore(" 40")), U.GLASS_DEFAULT);
  assert.equal(U.glassFromStorage(glassStore(undefined, true)), U.GLASS_DEFAULT);
});

test("glass alpha matches the stylesheet and gets clearer as the level rises", function () {
  const css = fs.readFileSync(path.join(__dirname, "..", "ui", "styles.css"), "utf8");
  const declared = css.match(/--glass-alpha:\s*([0-9.]+)\s*;/);
  assert.ok(declared);
  assert.equal(U.glassAlpha(U.glassFromStorage(glassStore(undefined))), declared[1]);
  assert.equal(U.glassAlpha(0), "0.82");
  assert.equal(U.glassAlpha(100), "0.28");
  assert.equal(U.glassAlpha(U.GLASS_DEFAULT), declared[1]);
  let prev = Number(U.glassAlpha(0));
  for (let n = 1; n <= 100; n += 1) {
    const alpha = Number(U.glassAlpha(n));
    assert.ok(alpha < prev, String(n));
    assert.ok(alpha >= 0.28 && alpha <= 0.82, String(n));
    prev = alpha;
  }
});

test("the first paint script uses the same glass alpha and does not write storage", function () {
  const html = fs.readFileSync(path.join(__dirname, "..", "ui", "index.html"), "utf8");
  const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];
  assert.equal(script.includes("setItem"), false);
  function boot(raw, throwing) {
    const props = {};
    let writes = 0;
    vm.runInNewContext(script, {
      localStorage: {
        getItem: function (key) {
          if (throwing) throw new Error("blocked");
          if (key === "pb-v23-glass") return raw == null ? null : String(raw);
          return null;
        },
        setItem: function () { writes += 1; }
      },
      document: {
        documentElement: {
          setAttribute: function () {},
          style: { setProperty: function (name, value) { props[name] = value; } }
        }
      },
      window: { matchMedia: function () { return { matches: false }; } }
    });
    assert.equal(writes, 0);
    return props["--glass-alpha"];
  }
  for (const raw of [null, "0", "40", "74", "100", "40.5", "74.0", "101", "-1", "nope", ""]) {
    assert.equal(boot(raw, false), U.glassAlpha(U.glassFromStorage(glassStore(raw))));
  }
  assert.equal(boot(null, true), U.glassAlpha(U.GLASS_DEFAULT));
});

test("seg markup escapes attributes and the thumb skips its first animation", function () {
  const html = U.segHTML('id"<', [{ v: "daily", html: "<b>日常</b>", title: 'say "hi"', disabled: false }, { v: "ai", html: "AI", disabled: true }], "daily", "sm icons");
  assert.match(html, /id="id&quot;&lt;"/);
  assert.match(html, /data-v="daily"/);
  assert.match(html, /title="say &quot;hi&quot;"/);
  assert.match(html, /class="seg-btn is-active"/);
  assert.match(html, /<b>日常<\/b>/);
  assert.match(html, /disabled/);
  assert.equal(html.includes('say "hi"'), false);
  const first = U.segThumbPlacement(48, 12, false, true);
  assert.equal(first.instant, true);
  assert.equal(first.visible, true);
  assert.equal(first.width, "48px");
  assert.equal(first.x, "12px");
  const later = U.segThumbPlacement(64, 70, true, true);
  assert.equal(later.instant, false);
  assert.equal(later.width, "64px");
  assert.equal(later.x, "70px");
  assert.equal(U.segThumbPlacement(0, 0, true, true).visible, false);
  assert.equal(U.segThumbPlacement(40, 8, true, false).instant, true);
});

test("nav indicator placement is the active item rect minus the nav rect", function () {
  const box = U.navIndicatorPlacement({ left: 18, top: 120, width: 212, height: 38 }, { left: 18, top: 80 });
  assert.deepEqual(box, { x: "0px", y: "40px", width: "212px", height: "38px" });
});
