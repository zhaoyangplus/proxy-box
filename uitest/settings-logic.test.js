"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const S = require("../ui/settings-logic.js");

const HOSTILE = "<img src=x onerror=alert(1)>";

test("settings leave allows a clean page, blocks a dirty page, and allows force", function () {
  assert.deepEqual(S.leaveDecision({ dirty: false, target: "nodes" }), { allow: true, target: "nodes" });
  assert.deepEqual(S.leaveDecision({ dirty: true, target: "overview" }), { allow: false, target: "overview" });
  assert.deepEqual(S.leaveDecision({ dirty: true, force: true, target: "diag" }), { allow: true, target: "diag" });
  assert.deepEqual(S.leaveDecision({ dirty: true, target: "" }), { allow: false, target: "" });
});

test("a clean settings draft matches the loaded config after normalization", function () {
  assert.equal(S.draftFrom({}).delayTestURL, "");
  assert.equal(S.draftFrom({}).speedTestURL, "");
  assert.equal(S.draftFrom({}).speedTestMaxMB, 10);
  const missing = {};
  const typed = {
    speedTestMaxMB: "10",
    autoOptimizeNode: null,
    autoSyncOnChange: true,
    syncUserEnv: false,
    minimizeToTray: 1,
    delayTestURL: " http://www.gstatic.com/generate_204 ",
    speedTestURL: ""
  };
  const reordered = {
    customCodexExe: "",
    minimizeToTray: true,
    speedTestURL: "",
    delayTestURL: "http://www.gstatic.com/generate_204",
    speedTestMaxMB: 10,
    autoOptimizeNode: true
  };
  assert.equal(S.sameDraft(missing, S.draftFrom(missing)), true);
  assert.equal(S.sameDraft(typed, S.draftFrom(typed)), true);
  assert.equal(S.sameDraft(typed, reordered), true);
  assert.equal(S.sameDraft({ speedTestMaxMB: 0 }, { speedTestMaxMB: 10 }), true);
  assert.equal(S.sameDraft({ speedTestMaxMB: "10" }, { speedTestMaxMB: 10 }), true);
  assert.equal(S.sameDraft({ speedTestMaxMB: "nope" }, { speedTestMaxMB: "nope" }), true);
  assert.equal(S.sameDraft({ autoOptimizeNode: null, autoSyncOnChange: undefined }, { autoOptimizeNode: true, autoSyncOnChange: true }), true);
  assert.equal(S.sameDraft({ speedTestURL: "" }, { speedTestURL: "https://speed.cloudflare.com/__down?bytes=10485760" }), false);
  const edited = S.draftFrom(typed);
  edited.customCursorExe = "D:\\Cursor.exe";
  assert.equal(S.sameDraft(typed, edited), false);
  assert.deepEqual(S.changedFields(typed, edited), { customCursorExe: "D:\\Cursor.exe" });
});

test("a server refresh updates untouched settings and keeps the edited field", function () {
  const saved = { delayTestURL: "http://www.gstatic.com/generate_204", speedTestURL: "", speedTestMaxMB: 10, minimizeToTray: true };
  const draft = S.draftFrom(saved);
  draft.delayTestURL = "http://example.test/204";
  const merged = S.mergeServer(saved, draft, {
    speedTestMaxMB: "25",
    delayTestURL: "http://www.gstatic.com/generate_204",
    speedTestURL: "https://speed.cloudflare.com/__down?bytes=10485760",
    minimizeToTray: false
  });
  assert.equal(merged.draft.delayTestURL, "http://example.test/204");
  assert.equal(merged.baseline.speedTestURL, "https://speed.cloudflare.com/__down?bytes=10485760");
  assert.equal(merged.draft.speedTestURL, merged.baseline.speedTestURL);
  assert.equal(merged.draft.minimizeToTray, false);
  assert.equal(merged.draft.speedTestMaxMB, 25);
  assert.deepEqual(merged.untouched.slice().sort(), ["minimizeToTray", "speedTestMaxMB", "speedTestURL"]);
  assert.equal(S.sameDraft(merged.baseline, merged.draft), false);
  const clean = S.mergeServer(saved, S.draftFrom(saved), { speedTestMaxMB: "10", delayTestURL: "http://www.gstatic.com/generate_204" });
  assert.equal(S.sameDraft(clean.baseline, clean.draft), true);
  assert.deepEqual(clean.untouched, []);
  assert.equal(S.draftSignature(saved), S.draftSignature({ speedTestMaxMB: "10", delayTestURL: " http://www.gstatic.com/generate_204 " }));
});

test("settings diff posts only changed fields", function () {
  const saved = {
    delayTestURL: "http://www.gstatic.com/generate_204",
    speedTestURL: "",
    speedTestMaxMB: 0,
    autoOptimizeNode: null,
    autoSyncOnChange: true,
    syncUserEnv: false,
    minimizeToTray: true,
    customCursorExe: "C:\\Cursor.exe",
    managedApps: { cursor: true, codex: false }
  };
  assert.deepEqual(S.changedFields(saved, S.draftFrom(saved)), {});
  const edited = S.draftFrom(saved);
  edited.speedTestURL = " https://speed.cloudflare.com/__down?bytes=10485760 ";
  edited.customCursorExe = "D:\\Cursor.exe";
  edited.managedApps = { cursor: false };
  assert.deepEqual(S.changedFields(saved, edited), {
    speedTestURL: "https://speed.cloudflare.com/__down?bytes=10485760",
    customCursorExe: "D:\\Cursor.exe"
  });
  const flags = S.draftFrom(saved);
  flags.autoOptimizeNode = false;
  flags.minimizeToTray = false;
  assert.deepEqual(S.changedFields(saved, flags), {
    autoOptimizeNode: false,
    minimizeToTray: false
  });
  assert.equal(Object.prototype.hasOwnProperty.call(S.changedFields(saved, edited), "managedApps"), false);
});

test("settings validation accepts only http(s) URLs and 5, 10, or 25 MB", function () {
  const ok = S.validate({
    delayTestURL: "",
    speedTestURL: "https://speed.cloudflare.com/__down?bytes=10485760",
    speedTestMaxMB: 0
  });
  assert.equal(ok.ok, true);
  assert.equal(ok.draft.speedTestMaxMB, 10);
  for (const size of [5, 10, 25, "25"]) {
    assert.equal(S.validate({ speedTestMaxMB: size }).ok, true, String(size));
  }
  const bad = S.validate({
    delayTestURL: "javascript:alert(1)",
    speedTestURL: HOSTILE,
    speedTestMaxMB: 7
  });
  assert.equal(bad.ok, false);
  assert.equal(bad.errors.delayTestURL, S.URL_ERROR);
  assert.equal(bad.errors.speedTestURL, S.URL_ERROR);
  assert.equal(bad.errors.speedTestMaxMB, S.MB_ERROR);
  assert.equal(S.validate({ delayTestURL: "ftp://files.example/a" }).ok, false);
  assert.equal(S.validate({ speedTestURL: "http://example.com/a" }).ok, true);
});

test("port numbers prefer the live status and fall back to the fixed defaults", function () {
  assert.deepEqual(S.portsFrom({
    mixedPort: 27990,
    settings: { mixedPort: 1, controllerPort: 27991, aiDedicatedPort: 27992, speedTestPort: 27993 }
  }), {
    mixed: 27990,
    controller: 27991,
    ai: 27992,
    speed: 27993
  });
  assert.deepEqual(S.portsFrom(null), {
    mixed: 27890,
    controller: 27891,
    ai: 27892,
    speed: 27893
  });
});
