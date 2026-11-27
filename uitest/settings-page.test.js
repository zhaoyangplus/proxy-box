"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

function el(tag, props) {
  const node = {
    tag: String(tag || "").toUpperCase(),
    className: "",
    text: "",
    attrs: {},
    children: [],
    parent: null,
    value: "",
    hidden: false,
    disabled: false,
    setAttribute(key, value) { this.attrs[key] = String(value); },
    getAttribute(key) { return this.attrs[key]; },
    appendChild(child) {
      if (!child) return child;
      if (child.parent) child.parent.children = child.parent.children.filter(function (item) { return item !== child; });
      child.parent = this;
      this.children.push(child);
      return child;
    },
    addEventListener() {}
  };
  Object.defineProperty(node, "textContent", {
    get() { return this.text + this.children.map(function (child) { return child.textContent || ""; }).join(""); },
    set(value) { this.text = String(value); }
  });
  const p = props || {};
  if (p.class) node.className = p.class;
  if (p.text != null) node.textContent = String(p.text);
  if (p.attrs) {
    Object.keys(p.attrs).forEach(function (key) {
      if (p.attrs[key] != null && p.attrs[key] !== false) node.setAttribute(key, p.attrs[key]);
    });
  }
  if (p.on) {
    Object.keys(p.on).forEach(function (key) {
      if (typeof p.on[key] === "function") node[key] = p.on[key];
    });
  }
  if (node.attrs.value != null) node.value = node.attrs.value;
  for (let i = 2; i < arguments.length; i++) if (arguments[i]) node.appendChild(arguments[i]);
  return node;
}

function walk(node, out) {
  out.push(node);
  (node.children || []).forEach(function (child) { walk(child, out); });
  return out;
}

function saveBar(root) {
  return walk(root, []).find(function (node) { return node.attrs && node.attrs.id === "saveBar"; });
}

function field(root, label) {
  return walk(root, []).find(function (node) {
    return node.tag === "INPUT" && node.attrs["aria-label"] === label;
  });
}

function mount(config) {
  const PB = {
    el: el,
    state: {
      core: { mixedPort: 27890, controllerPort: 27891, aiDedicatedPort: 27892, speedTestPort: 27893, settings: {} },
      snapshot: { activeClient: {}, appConfig: config },
      version: "v1.4.0",
      releasesUrl: "",
      update: null,
      phase: "stopped"
    },
    theme: { get() { return "auto"; }, set() {} },
    registerTab(id, tab) { this.tab = tab; },
    goTab() {},
    api() { return Promise.resolve({}); },
    toast() {},
    topbar() {},
    setChannel() {},
    ic() { return ""; },
    onInput(input, fn) { input.onInput = fn; return function () {}; },
    mountSeg() {},
    current() { return "settings"; },
    refreshSnapshot() { return Promise.resolve(this.state.snapshot); }
  };
  global.PB = PB;
  require("../ui/settings-logic.js");
  const source = fs.readFileSync(path.join(__dirname, "../ui/tab-settings.js"), "utf8");
  eval(source);
  const root = el("div");
  PB.tab.enter(root);
  return { PB: PB, root: root };
}

test("a clean settings page shows no unsaved bar", function () {
  const configs = [
    {},
    {
      delayTestURL: " http://www.gstatic.com/generate_204 ",
      speedTestURL: "",
      speedTestMaxMB: "10",
      autoOptimizeNode: null,
      minimizeToTray: true
    },
    {
      speedTestMaxMB: 10,
      delayTestURL: "http://www.gstatic.com/generate_204",
      speedTestURL: "https://speed.cloudflare.com/__down?bytes=10485760",
      customCursorExe: "",
      autoSyncOnChange: true,
      syncUserEnv: false
    }
  ];
  configs.forEach(function (config) {
    const page = mount(config);
    const bar = saveBar(page.root);
    assert.ok(bar, "save bar exists");
    assert.equal(bar.className.includes("show"), false, JSON.stringify(config));
    const delay = field(page.root, "延迟测试地址");
    assert.equal(delay.value, String(config.delayTestURL || "").trim());
  });
});

test("an equivalent sync does not mark settings dirty, and an edit does", function () {
  const page = mount({
    delayTestURL: "http://www.gstatic.com/generate_204",
    speedTestURL: "",
    speedTestMaxMB: 10,
    autoOptimizeNode: true,
    autoSyncOnChange: true,
    syncUserEnv: false,
    minimizeToTray: true
  });
  page.PB.state.snapshot.appConfig = {
    minimizeToTray: true,
    syncUserEnv: false,
    autoSyncOnChange: true,
    autoOptimizeNode: true,
    speedTestMaxMB: "10",
    speedTestURL: "",
    delayTestURL: "http://www.gstatic.com/generate_204"
  };
  page.PB.tab.onSSE({ type: "sync" });
  assert.equal(saveBar(page.root).className.includes("show"), false);
  const delay = field(page.root, "延迟测试地址");
  delay.value = "http://example.test/204";
  delay.onInput(delay.value, delay);
  assert.equal(saveBar(page.root).className.includes("show"), true);
  const speed = field(page.root, "下载测速 URL");
  page.PB.state.snapshot.appConfig = {
    delayTestURL: "http://www.gstatic.com/generate_204",
    speedTestURL: "https://speed.cloudflare.com/__down?bytes=10485760",
    speedTestMaxMB: 25
  };
  page.PB.tab.onSSE({ type: "sync" });
  assert.equal(field(page.root, "延迟测试地址"), delay);
  assert.equal(delay.value, "http://example.test/204");
  assert.equal(speed.value, "https://speed.cloudflare.com/__down?bytes=10485760");
  assert.equal(saveBar(page.root).className.includes("show"), true);
});
