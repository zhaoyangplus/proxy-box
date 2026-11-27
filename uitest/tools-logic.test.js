"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
require("../ui/util.js");
const T = require("../ui/tools-logic.js");

const HOSTILE = "<img src=x onerror=alert(1)>";
const SNIPPET = '$env:HTTP_PROXY="http://127.0.0.1:27892"; $env:HTTPS_PROXY="http://127.0.0.1:27892"; $env:NO_PROXY="localhost,127.0.0.1"';

function assertInert(html) {
  assert.equal(html.indexOf("<img"), -1);
  assert.equal(html.indexOf("<script"), -1);
  assert.match(html, /&lt;img src=x onerror=alert\(1\)&gt;/);
}

test("env snippet uses the AI port and drops a hostile port", function () {
  assert.equal(T.envSnippet(27892), SNIPPET);
  assert.equal(T.envSnippet("27892"), SNIPPET);
  assert.equal(T.envSnippet('27892"; Remove-Item C:\\Windows'), "");
  assert.equal(T.envSnippet(HOSTILE), "");
  assert.equal(T.envSnippet(0), "");
  assert.equal(T.envSnippet(65536), "");
  assert.equal(T.envSnippet(HOSTILE).indexOf("<img"), -1);
  assert.equal(T.envSnippet('27892"; Remove-Item C:\\Windows').indexOf("Remove-Item"), -1);
});

test("enabled count and optimistic toggle stay on the four known apps", function () {
  const src = [
    { id: "cursor", enabledByUser: true, name: "Cursor" },
    { id: "extra", enabledByUser: true, name: HOSTILE }
  ];
  assert.equal(T.enabledCount(src), 1);
  assert.equal(T.enabledCount(null), 0);
  const next = T.applyEnabled(src, "codex", true);
  assert.equal(src[0].enabledByUser, true);
  assert.equal(src.length, 2);
  assert.equal(next.length, 4);
  assert.equal(next[3].id, "codex");
  assert.equal(next[3].enabledByUser, true);
  assert.equal(next[0].enabledByUser, true);
  assert.equal(T.enabledCount(next), 2);
});

test("tool cards escape hostile fields and keep brand chrome", function () {
  const html = T.pageHTML([
    {
      id: "cursor",
      name: HOSTILE,
      enabledByUser: true,
      running: true,
      http2Disabled: true,
      settingsPath: HOSTILE,
      statusText: HOSTILE,
      background: HOSTILE
    },
    { id: "codex", enabledByUser: true, terminalEnvConfigured: true, settingsPath: "C:\\codex\\config.toml" },
    { id: "claude_code", enabledByUser: false, name: "Claude Code", settingsPath: "" }
  ], { port: HOSTILE });
  assertInert(html);
  assert.equal(html.split('data-tid="').length - 1, 4);
  assert.match(html, /style="background:linear-gradient\(145deg,#3a3a3c,#0b0b0d\)"/);
  assert.match(html, /重启/);
  assert.match(html, /代理启动/);
  assert.match(html, /创建桌面快捷方式/);
  assert.equal(html.split("复制终端环境变量").length - 1, 2);
  assert.match(html, /HTTP\/2 已禁用/);
  assert.match(html, /环境变量已写入/);
  assert.match(html, />AI 通道</);
  assert.equal(html.indexOf("AI 通道 :"), -1);
  assert.match(html, /未开启托管（保持应用默认设置）/);
  assert.match(html, /run-dot"/);
  assert.match(html, /class="card tool off"/);
  const off = T.pageHTML([{ id: "cursor", enabledByUser: false, statusText: HOSTILE, name: "Cursor" }], { port: 27892 });
  assert.equal(off.indexOf("AI 通道"), -1);
  assert.match(off, /未开启托管（保持应用默认设置）/);
  assert.equal(off.indexOf(HOSTILE), -1);
});

test("channel chips use a real port only when the app is enabled", function () {
  const on = T.chips({ enabledByUser: true, http2Disabled: false, terminalEnvConfigured: false, statusText: "" }, 27892);
  assert.deepEqual(on, [{ tone: "indigo", text: "AI 通道 :27892" }]);
  assert.deepEqual(T.chips({ enabledByUser: false, statusText: HOSTILE }, 27892), []);
});

test("tool signatures change only the apps whose fields changed", function () {
  const saved = [{ id: "cursor", enabledByUser: true, running: false, settingsPath: "C:\\c.json" }];
  const same = [{ settingsPath: "C:\\c.json", running: false, enabledByUser: true, id: "cursor" }];
  assert.equal(T.toolsSignature(saved, 27892, ""), T.toolsSignature(same, 27892, ""));
  assert.deepEqual(T.diffTargets(saved, same, 27892, ""), []);
  const next = [{ id: "cursor", enabledByUser: false, running: true, settingsPath: "C:\\c.json" }];
  assert.deepEqual(T.diffTargets(saved, next, 27892, ""), ["cursor"]);
});
