package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsSpeedPortStaysSeparateFromDownloadURL(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "check.js")
	if err := os.WriteFile(script, []byte(settingsPortCheckJS), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", script, "ui")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("settings port check: %v\n%s", err, out)
	}
	if text := strings.TrimSpace(string(out)); text != "ok" {
		t.Fatalf("settings port check: %s", text)
	}
}

const settingsPortCheckJS = `
const fs = require("fs");
const path = require("path");
const ui = process.argv[2];

function hasClass(node, name) {
  return String(node.className || "").split(/\s+/).indexOf(name) >= 0;
}

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
      if (child.parent) {
        child.parent.children = child.parent.children.filter((item) => item !== child);
      }
      child.parent = this;
      this.children.push(child);
      return child;
    }
  };
  Object.defineProperty(node, "textContent", {
    get() { return this.text + this.children.map((child) => child.textContent || "").join(""); },
    set(value) { this.text = String(value); }
  });
  const p = props || {};
  if (p.class) node.className = p.class;
  if (p.text != null) node.textContent = String(p.text);
  if (p.attrs) {
    Object.keys(p.attrs).forEach((key) => {
      if (p.attrs[key] != null && p.attrs[key] !== false) node.setAttribute(key, p.attrs[key]);
    });
  }
  if (node.attrs.value) node.value = node.attrs.value;
  for (let i = 2; i < arguments.length; i++) {
    const child = arguments[i];
    if (child) node.appendChild(child);
  }
  return node;
}

const PB = {
  el: el,
  state: {
    core: { mixedPort: 27890, controllerPort: 27891, aiDedicatedPort: 27892, speedTestPort: 27893, settings: {} },
    snapshot: {
      activeClient: {},
      appConfig: {
        speedTestURL: "https://speed.cloudflare.com/__down?bytes=10485760",
        delayTestURL: "http://www.gstatic.com/generate_204",
        speedTestMaxMB: 10
      }
    },
    version: "v1.4.0",
    releasesUrl: "https://example.invalid/releases",
    update: null,
    phase: "stopped"
  },
  theme: { get() { return "auto"; }, set() {} },
  registerTab(id, tab) { this.tab = tab; },
  goTab() {},
  api() { return Promise.resolve({}); },
  toast() {},
  topbar() {},
  setChannel() {}
};
global.PB = PB;
eval(fs.readFileSync(path.join(ui, "settings-logic.js"), "utf8"));
eval(fs.readFileSync(path.join(ui, "tab-settings.js"), "utf8"));
const root = el("div");
PB.tab.enter(root);

function walk(node, out) {
  out.push(node);
  (node.children || []).forEach((child) => walk(child, out));
  return out;
}
const all = walk(root, []);
const port = all.find((node) => hasClass(node, "pb-port"));
if (!port) {
  console.log("missing speed port");
  process.exit(1);
}
const label = walk(port, []).find((node) => hasClass(node, "pb-num"));
const url = all.find((node) => node.tag === "INPUT" && node.attrs["aria-label"] === "下载测速 URL");
if (!label || !url) {
  console.log("missing port number or download url");
  process.exit(1);
}
if (label === url || label.tag === "INPUT" || url.tag !== "INPUT") {
  console.log("speed port is the download field");
  process.exit(1);
}
if (!String(label.textContent).includes("27893")) {
  console.log("initial port " + label.textContent);
  process.exit(1);
}
if (url.value.indexOf("speed.cloudflare.com") < 0) {
  console.log("url " + url.value);
  process.exit(1);
}
PB.state.core.speedTestPort = 27999;
PB.tab.onCore(PB.state.core);
if (!String(label.textContent).includes("27999")) {
  console.log("updated port " + label.textContent);
  process.exit(1);
}
if (url.value.indexOf("speed.cloudflare.com") < 0) {
  console.log("url overwritten " + url.value);
  process.exit(1);
}
console.log("ok");
`
