"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

test("backdrop-filter stays on the five fixed layers", function () {
  const ui = path.join(__dirname, "..", "ui");
  const lines = [];
  fs.readdirSync(ui).forEach(function (name) {
    if (!/\.(css|html)$/.test(name)) return;
    const text = fs.readFileSync(path.join(ui, name), "utf8");
    text.split(/\n/).forEach(function (line, index) {
      if (line.includes("backdrop-filter")) lines.push(name + ":" + (index + 1) + ":" + line.trim());
    });
  });
  assert.deepEqual(lines.map(function (line) { return line.replace(/^styles\.css:\d+:/, ""); }), [
    "-webkit-backdrop-filter: blur(24px) saturate(180%);",
    "backdrop-filter: blur(24px) saturate(180%);",
  ]);
  const css = fs.readFileSync(path.join(ui, "styles.css"), "utf8");
  assert.match(css, /\.side,\s*\.topbar,\s*\.pop,\s*\.sheet,\s*\.toast\s*\{[^}]*backdrop-filter: blur\(24px\) saturate\(180%\)/);
});
