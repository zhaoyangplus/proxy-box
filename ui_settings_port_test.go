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
	cmd := exec.Command("node", script, filepath.Join("ui", "tab-settings.js"))
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
const path = process.argv[2];

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
    setAttribute(key, value) { this.attrs[key] = String(value); },
    getAttribute(key) { return this.attrs[key]; },
    appendChild(child) {
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
  state: { core: { mixedPort: 27890, controllerPort: 27891, aiDedicatedPort: 27892, speedTestPort: 27893 }, snapshot: { activeClient: {}, appConfig: {} } },
  ports() {
    const core = this.state.core || {};
    return {
      mixed: core.mixedPort || 27890,
      controller: core.controllerPort || 27891,
      ai: core.aiDedicatedPort || 27892,
      speed: core.speedTestPort || 27893
    };
  },
  addTab(id, tab) { this.tab = tab; },
  api() { return Promise.resolve({}); },
  toast() {},
  setSnapshot() {},
  importCore() {},
  openHome() {}
};
global.PB = PB;
eval(fs.readFileSync(path, "utf8"));
const root = el("div");
PB.tab.mount(root);

function walk(node, out) {
  out.push(node);
  (node.children || []).forEach((child) => walk(child, out));
  return out;
}
const all = walk(root, []);
const port = all.find((node) => node.className === "pb-port" && node.children[0] && node.children[0].text === "测速端口");
if (!port || !port.children[1]) {
  console.log("missing speed port");
  process.exit(1);
}
const label = port.children[1];
const url = all.find((node) => node.tag === "INPUT" && node.attrs["aria-label"] === "下载测速 URL");
if (!url) {
  console.log("missing download url");
  process.exit(1);
}
if (label === url || label.tag === "INPUT") {
  console.log("speed port is the download field");
  process.exit(1);
}
if (!String(label.text).includes("27893")) {
  console.log("initial port " + label.text);
  process.exit(1);
}
if (url.value.indexOf("speed.cloudflare.com") < 0) {
  console.log("url " + url.value);
  process.exit(1);
}
PB.state.core.speedTestPort = 27999;
PB.tab.onCoreChange();
if (!String(label.text).includes("27999")) {
  console.log("updated port " + label.text);
  process.exit(1);
}
if (url.value.indexOf("speed.cloudflare.com") < 0) {
  console.log("url overwritten " + url.value);
  process.exit(1);
}
console.log("ok");
`
