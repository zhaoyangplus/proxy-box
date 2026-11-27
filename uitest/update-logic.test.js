"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");

globalThis.PBUtil = require("../ui/util.js");
const U = require("../ui/update-logic.js");

const HOSTILE = '<img src=x onerror=alert(1)>';

test("update sheet maps check results, progress, and errors", function () {
  assert.equal(U.loading().kind, "loading");
  const available = U.fromCheck({
    currentVersion: "v1.4.0",
    latestVersion: "v1.5.0",
    hasUpdate: true,
    releaseNotes: "第一行\n- 第二行",
    assetSize: 10485760,
    downloadUrl: "https://example.invalid/ProxyBox.exe",
    releaseUrl: "https://example.invalid/releases",
    publishedAt: "2026-11-20T02:00:00Z"
  });
  assert.equal(available.kind, "available");
  assert.equal(available.assetSize, 10485760);
  assert.equal(U.fromCheck({ currentVersion: "v1.4.0", latestVersion: "v1.4.0", hasUpdate: false }).kind, "current");
  assert.equal(U.fromCheck({}).kind, "error");
  const progress = U.fromProgress(available, {
    status: "downloading",
    percent: 40,
    downloadedBytes: 400,
    totalBytes: 1000,
    message: "正在下载 v1.5.0",
    targetVersion: "v1.5.0"
  });
  assert.equal(progress.kind, "progress");
  assert.equal(progress.percent, 40);
  const failed = U.fromProgress(progress, { status: "error", error: "测试环境不应用更新" });
  assert.equal(failed.kind, "error");
  assert.equal(failed.message, "测试环境不应用更新");
  const applying = U.fromProgress(available, { status: "restarting" });
  assert.equal(applying.message, "正在重启");
  assert.equal(U.fromProgress(available, { status: "idle" }).kind, "available");
});

test("update markup escapes versions, notes, and errors", function () {
  const html = U.bodyHTML(U.fromCheck({
    currentVersion: HOSTILE,
    latestVersion: HOSTILE,
    hasUpdate: true,
    releaseNotes: HOSTILE + "\n普通说明",
    assetSize: 2048,
    releaseUrl: "https://example.invalid/releases"
  }));
  assert.equal(html.includes("<img"), false);
  assert.equal(html.includes("&lt;img src=x onerror=alert(1)&gt;"), true);
  assert.match(html, /id="updGo"/);
  assert.match(html, /2\.0 KB/);
  const error = U.bodyHTML(U.fromError(HOSTILE));
  assert.equal(error.includes("<img"), false);
  assert.equal(error.includes("&lt;img src=x onerror=alert(1)&gt;"), true);
  assert.match(error, /id="updRetry"/);
  const bar = U.bodyHTML(U.fromProgress(U.fromCheck({
    currentVersion: "v1",
    latestVersion: "v2",
    hasUpdate: true
  }), { status: "downloading", percent: 140, downloadedBytes: 10, totalBytes: 10, message: HOSTILE, targetVersion: HOSTILE }));
  assert.match(bar, /width:100\.0%/);
  assert.equal(bar.includes("<img"), false);
  assert.equal(bar.includes("&lt;img src=x onerror=alert(1)&gt;"), true);
  assert.match(U.bodyHTML(U.fromCheck({ currentVersion: "v1.4.0", latestVersion: "v1.4.0", hasUpdate: false })), /当前已是最新版本/);
  assert.match(U.bodyHTML(U.loading()), /正在检查 GitHub Releases/);
});

test("update status moves the download forward once and stops when it ends", function () {
  const optimistic = U.fromProgress(U.fromCheck({
    currentVersion: "v1",
    latestVersion: "v2",
    hasUpdate: true
  }), { status: "downloading", percent: 0, message: "正在下载…" });
  const waiting = U.reconcileUpdate(optimistic, { status: "idle" });
  assert.equal(waiting.changed, false);
  assert.equal(waiting.polling, true);
  assert.equal(waiting.view, optimistic);
  const moved = U.reconcileUpdate(optimistic, {
    status: "downloading",
    percent: 40,
    downloadedBytes: 40,
    totalBytes: 100,
    message: "正在下载 v2",
    targetVersion: "v2"
  });
  assert.equal(moved.changed, true);
  assert.equal(moved.polling, true);
  assert.equal(moved.view.percent, 40);
  assert.equal(moved.view.confirmed, true);
  const same = U.reconcileUpdate(moved.view, {
    status: "downloading",
    percent: 40,
    downloadedBytes: 40,
    totalBytes: 100,
    message: "正在下载 v2",
    targetVersion: "v2"
  });
  assert.equal(same.changed, false);
  assert.equal(same.view, moved.view);
  assert.equal(same.polling, true);
  const failed = U.reconcileUpdate(moved.view, { status: "error", error: "下载失败" });
  assert.equal(failed.changed, true);
  assert.equal(failed.polling, false);
  assert.equal(failed.view.kind, "error");
  assert.equal(failed.view.message, "下载失败");
  const again = U.reconcileUpdate(failed.view, { status: "error", error: "下载失败" });
  assert.equal(again.changed, false);
  assert.equal(again.polling, false);
  const dropped = U.reconcileUpdate(moved.view, { status: "idle" });
  assert.equal(dropped.changed, true);
  assert.equal(dropped.polling, false);
  assert.equal(dropped.view.kind, "error");
});
