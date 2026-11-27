"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
require("../ui/util.js");
const D = require("../ui/diag-logic.js");

const HOSTILE = "<img src=x onerror=alert(1)>";

function assertInert(html) {
  assert.equal(html.indexOf("<img"), -1);
  assert.equal(html.indexOf("<script"), -1);
  assert.match(html, /&lt;img src=x onerror=alert\(1\)&gt;/);
}

test("a diagnostic result applies only to the channel and run that started it", function () {
  let book = D.emptyRuns();
  const ai = D.beginRun(book, "ai");
  book = ai.book;
  const daily = D.beginRun(book, "daily");
  book = daily.book;
  assert.equal(D.viewRun(book, "ai").status, "running");
  assert.equal(D.viewRun(book, "daily").status, "running");
  const aiDone = D.finishRun(book, "ai", ai.run, { ok: true, report: { channel: "ai", exitIp: "1.1.1.1" } });
  assert.equal(aiDone.applied, true);
  book = aiDone.book;
  assert.equal(D.viewRun(book, "ai").status, "result");
  assert.equal(D.viewRun(book, "ai").report.exitIp, "1.1.1.1");
  assert.equal(D.viewRun(book, "daily").status, "running");
  const again = D.beginRun(book, "ai");
  book = again.book;
  const stale = D.finishRun(book, "ai", ai.run, { ok: true, report: { channel: "ai", exitIp: "9.9.9.9" } });
  assert.equal(stale.applied, false);
  assert.equal(D.viewRun(stale.book, "ai").status, "running");
  assert.equal(D.viewRun(book, "daily").status, "running");
  const failed = D.finishRun(book, "daily", daily.run, { ok: false, error: "诊断失败" });
  assert.equal(failed.applied, true);
  assert.equal(D.viewRun(failed.book, "daily").status, "error");
  assert.equal(D.viewRun(failed.book, "daily").error, "诊断失败");
  assert.equal(D.viewRun(failed.book, "ai").status, "running");
  const wiped = D.clearRuns(failed.book);
  assert.equal(D.viewRun(wiped, "ai").status, "idle");
  assert.equal(D.viewRun(wiped, "daily").status, "idle");
  assert.equal(D.finishRun(wiped, "daily", daily.run, { ok: true, report: { exitIp: "stale" } }).applied, false);
});

test("diagnostic rows map error to fail and total the ring", function () {
  const report = D.mapReport({
    channel: "daily",
    port: 27890,
    items: [
      { id: "local_port", status: "ok", latencyMs: 4 },
      { id: "exit_region", status: "warn", latencyMs: 80 },
      { id: "claude_api", status: "error", latencyMs: 0, detail: "HTTP 403" },
      { id: "odd", status: "nope", latencyMs: -3 }
    ]
  });
  assert.equal(report.channel, "daily");
  assert.equal(report.port, 27890);
  assert.deepEqual(report.rows.map(function (row) { return row.status; }), ["ok", "warn", "fail", "fail"]);
  assert.equal(report.rows[3].latencyMs, 0);
  assert.equal(report.totals.pass, 1);
  assert.equal(report.totals.warn, 1);
  assert.equal(report.totals.fail, 2);
  assert.equal(report.totals.issues, 3);
  assert.equal(report.totals.label, "1/4");
  assert.equal(report.totals.color, "var(--red)");
  const warnOnly = D.ringTotals([{ status: "warn" }, { status: "ok" }]);
  assert.equal(warnOnly.color, "var(--orange)");
  assert.equal(warnOnly.pass, 1);
  assert.equal(warnOnly.issues, 1);
  const clean = D.ringTotals([{ status: "ok" }, { status: "ok" }]);
  assert.equal(clean.color, "var(--green)");
  assert.equal(clean.dashOffset, 0);
  assert.equal(clean.label, "2/2");
  assert.deepEqual(D.badgeFor(report), { text: "3", cls: "err" });
  assert.deepEqual(D.badgeFor({ totals: warnOnly }), { text: "1", cls: "warn" });
  assert.deepEqual(D.badgeFor({ totals: clean }), { text: "", cls: "" });
  assert.equal(D.mapReport({ channel: "other", port: HOSTILE, items: null }).port, 0);
  assert.equal(D.mapReport({}).channel, "ai");
  assert.equal(D.visibleRows(report.rows, true).length, 3);
});

test("diagnostic html escapes details and draws the pass ring", function () {
  const open = Object.create(null);
  open["daily:" + HOSTILE] = true;
  const mapped = D.mapReport({
    channel: "daily",
    port: HOSTILE,
    exitIp: HOSTILE,
    exitRegionName: HOSTILE,
    checkedAt: HOSTILE,
    items: [
      { id: HOSTILE, name: HOSTILE, target: HOSTILE, status: "error", latencyMs: 0, detail: HOSTILE },
      { id: "ok1", name: "本地端口", target: "local-port", status: "ok", latencyMs: 20, detail: "正常" }
    ]
  });
  const html = D.pageHTML(mapped, { onlyIssues: false, open: open });
  const circ = 2 * Math.PI * 24;
  assertInert(html);
  assert.match(html, /data-ch="daily"/);
  assert.match(html, /class="st err"/);
  assert.match(html, /class="drow open"/);
  assert.match(html, /aria-expanded="true"/);
  assert.match(html, /<b>1\/2<\/b>/);
  assert.match(html, /--ring-c:var\(--red\)/);
  assert.match(html, new RegExp('stroke-dasharray="' + circ.toFixed(1) + '"'));
  assert.match(html, new RegExp('stroke-dashoffset="' + (circ / 2).toFixed(1) + '"'));
  assert.match(html, /失败/);
  assert.match(html, /1 项异常/);
  assert.match(html, /日常通道仅用于浏览/);
  assert.equal(html.indexOf("127.0.0.1:"), -1);
  const issues = D.pageHTML(mapped, { onlyIssues: true, open: open });
  assert.equal(issues.indexOf("本地端口"), -1);
  assert.match(issues, /没有异常项，所有检测都通过了。|失败/);
  assert.equal(issues.split("drow-name").length - 1, 1);
  const blocked = D.blockedHTML(HOSTILE);
  assertInert(blocked);
  assert.match(blocked, /无法诊断/);
  assert.match(blocked, /前往总览/);
  assert.match(blocked, /data-act="overview"/);
  const loading = D.blockedHTML("");
  assert.match(loading, /正在诊断…/);
  assert.equal(loading.indexOf("前往总览"), -1);
  assert.match(D.actionsHTML("ai"), /AI 专属/);
  assert.match(D.actionsHTML("daily"), /日常代理/);
  assert.match(D.actionsHTML("ai"), /id="dgRun"/);
});
