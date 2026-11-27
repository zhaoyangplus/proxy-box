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
    "-webkit-backdrop-filter: blur(40px) saturate(190%);",
    "backdrop-filter: blur(40px) saturate(190%);",
  ]);
  const css = fs.readFileSync(path.join(ui, "styles.css"), "utf8");
  assert.match(css, /\.side,\s*\.topbar,\s*\.pop,\s*\.sheet,\s*\.toast\s*\{[^}]*backdrop-filter: blur\(40px\) saturate\(190%\)/);
});

test("selected node card shadow stays inside the border box", function () {
  const css = fs.readFileSync(path.join(__dirname, "..", "ui", "styles.css"), "utf8");
  const card = css.match(/\.nd-card\.is-sel\s*\{([^}]*)\}/);
  assert.ok(card, "missing .nd-card.is-sel");
  const shadow = card[1].match(/box-shadow:\s*([^;]+)/);
  assert.ok(shadow, "missing selected box-shadow");
  shadow[1].split(",").forEach(function (part) {
    assert.match(part.trim(), /^inset\b/);
  });
  assert.equal(/transform\s*:/.test(card[1]), false);
  assert.match(css, /\.nd-card\s*\{[^}]*content-visibility:\s*auto/);
  const row = css.match(/\.nd-tr\.is-sel\s*\{([^}]*)\}/);
  assert.ok(row, "missing .nd-tr.is-sel");
  assert.match(row[1], /background:\s*var\(--tint-wash\)/);
  assert.equal(/height\s*:|box-shadow\s*:|transform\s*:/.test(row[1]), false);
});
