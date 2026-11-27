(function () {
  "use strict";

  var refs = null;
  var dirty = false;
  var DELAY_DEFAULT = "http://www.gstatic.com/generate_204";
  var SPEED_DEFAULT = "https://speed.cloudflare.com/__down?bytes=10485760";

  function setSwitch(btn, on) {
    btn.setAttribute("aria-checked", on ? "true" : "false");
  }

  function readSwitch(btn) {
    return btn.getAttribute("aria-checked") === "true";
  }

  function prefOn(value, fallback) {
    if (typeof value === "boolean") return value;
    return fallback;
  }

  function field(label, control) {
    var tag = control && control.tagName;
    if ((tag === "INPUT" || tag === "SELECT") && !control.getAttribute("aria-label")) {
      control.setAttribute("aria-label", label);
    }
    return PB.el("div", { class: "pb-field" }, PB.el("span", { text: label }), control);
  }

  function makeSwitch(label) {
    var btn = PB.el("button", {
      class: "pb-switch",
      attrs: { role: "switch", "aria-checked": "false", "aria-label": label },
      on: {
        click: function () {
          setSwitch(btn, !readSwitch(btn));
          dirty = true;
        }
      }
    });
    return btn;
  }

  function prefRow(title, btn) {
    return PB.el("div", { class: "pb-row" }, PB.el("span", { text: title }), btn);
  }

  function paintPorts() {
    if (!refs) return;
    var ports = PB.ports();
    refs.mixed.textContent = String(ports.mixed);
    refs.controller.textContent = String(ports.controller);
    refs.ai.textContent = String(ports.ai);
    refs.speedPort.textContent = String(ports.speed);
    var client = PB.state.snapshot && PB.state.snapshot.activeClient;
    refs.file.textContent = client && client.exePath ? client.exePath : "未找到 mihomo.exe";
    refs.fileState.textContent = client && client.exePath ? "已就绪" : "未导入";
    refs.fileState.className = "pb-badge " + (client && client.exePath ? "pb-badge-ok" : "pb-badge-muted");
  }

  function fill() {
    if (!refs || dirty) return;
    var cfg = (PB.state.snapshot && PB.state.snapshot.appConfig) || {};
    refs.delay.value = cfg.delayTestURL || DELAY_DEFAULT;
    refs.speed.value = cfg.speedTestURL || SPEED_DEFAULT;
    var mb = Number(cfg.speedTestMaxMB);
    refs.max.value = (mb === 5 || mb === 10 || mb === 25) ? String(mb) : "10";
    refs.cursor.value = cfg.customCursorExe || "";
    refs.anti.value = cfg.customAntigravityExe || "";
    refs.codex.value = cfg.customCodexExe || "";
    setSwitch(refs.opt, prefOn(cfg.autoOptimizeNode, true));
    setSwitch(refs.sync, prefOn(cfg.autoSyncOnChange, true));
    setSwitch(refs.env, prefOn(cfg.syncUserEnv, false));
    setSwitch(refs.tray, cfg.minimizeToTray !== false);
    paintPorts();
  }

  function markDirty() { dirty = true; }

  function browse(input) {
    PB.api("POST", "/api/browse-exe", {}).then(function (res) {
      if (res && res.path) {
        input.value = res.path;
        dirty = true;
      }
    }).catch(function (err) {
      PB.toast((err && err.message) || "选择文件失败", "err");
    });
  }

  function openCore() {
    var client = PB.state.snapshot && PB.state.snapshot.activeClient;
    var dir = client && client.configDir;
    if (!dir) {
      PB.toast("还没有内核目录", "warn");
      return;
    }
    PB.api("POST", "/api/open-path", { path: dir, parent: false }).then(function () {
      PB.toast("已打开内核目录");
    }).catch(function (err) {
      PB.toast((err && err.message) || "打开失败", "err");
    });
  }

  function save() {
    var mb = Number(refs.max.value);
    var body = {
      delayTestURL: refs.delay.value.trim(),
      speedTestURL: refs.speed.value.trim(),
      speedTestMaxMB: mb,
      customCursorExe: refs.cursor.value.trim(),
      customAntigravityExe: refs.anti.value.trim(),
      customCodexExe: refs.codex.value.trim(),
      autoOptimizeNode: readSwitch(refs.opt),
      autoSyncOnChange: readSwitch(refs.sync),
      syncUserEnv: readSwitch(refs.env),
      minimizeToTray: readSwitch(refs.tray)
    };
    refs.save.disabled = true;
    PB.api("POST", "/api/save-config", body).then(function () {
      dirty = false;
      PB.toast("设置已保存");
      return PB.api("GET", "/api/snapshot").then(PB.setSnapshot);
    }).catch(function (err) {
      PB.toast((err && err.message) || "保存失败", "err");
    }).finally(function () {
      if (refs) refs.save.disabled = false;
    });
  }

  function input(value, label) {
    var attrs = { type: "text", spellcheck: "false", autocomplete: "off", value: value || "" };
    if (label) attrs["aria-label"] = label;
    return PB.el("input", {
      class: "pb-input",
      attrs: attrs,
      on: { input: markDirty }
    });
  }

  function pathRow(inputEl) {
    return PB.el("div", { class: "pb-input-row" },
      inputEl,
      PB.el("button", { class: "pb-btn", text: "浏览", on: { click: function () { browse(inputEl); } } })
    );
  }

  function mount(root) {
    refs = {};
    refs.mixed = PB.el("b", { text: "27890" });
    refs.controller = PB.el("b", { text: "27891" });
    refs.ai = PB.el("b", { text: "27892" });
    refs.speedPort = PB.el("b", { text: "27893" });
    refs.file = PB.el("div", { class: "pb-mono pb-muted" });
    refs.fileState = PB.el("span", { class: "pb-badge pb-badge-muted", text: "未导入" });
    refs.delay = input(DELAY_DEFAULT, "延迟测试 URL");
    refs.speed = input(SPEED_DEFAULT, "下载测速 URL");
    refs.max = PB.el("select", {
      class: "pb-input",
      attrs: { "aria-label": "单节点测速上限" },
      on: { change: markDirty }
    },
      PB.el("option", { attrs: { value: "5" }, text: "5 MB" }),
      PB.el("option", { attrs: { value: "10" }, text: "10 MB" }),
      PB.el("option", { attrs: { value: "25" }, text: "25 MB" })
    );
    refs.cursor = input("", "Cursor 路径");
    refs.anti = input("", "Antigravity 路径");
    refs.codex = input("", "Codex 路径");
    refs.opt = makeSwitch("自动优选 AI 节点");
    refs.sync = makeSwitch("代理变更时自动同步已开启的 AI 工具");
    refs.env = makeSwitch("同步写入 Windows 用户环境变量");
    refs.tray = makeSwitch("关闭窗口时最小化到托盘");
    refs.save = PB.el("button", {
      class: "pb-btn pb-btn-primary",
      attrs: { id: "pb-save" },
      text: "保存设置",
      on: { click: save }
    });

    var port = function (label, node) {
      return PB.el("div", { class: "pb-port" }, PB.el("div", { class: "pb-kicker", text: label }), node);
    };
    root.appendChild(PB.el("div", { class: "pb-stack" },
      PB.el("section", { class: "pb-card" },
        PB.el("h3", { text: "内核与端口" }),
        PB.el("div", { class: "pb-ports" },
          port("日常代理", refs.mixed),
          port("控制端口", refs.controller),
          port("AI 专属", refs.ai),
          port("测速端口", refs.speedPort)
        ),
        PB.el("p", { class: "pb-hint", text: "端口由内核配置决定。更改后需要重启内核才会生效。" }),
        PB.el("div", { class: "pb-row" },
          PB.el("span", { text: "内核文件" }),
          refs.fileState
        ),
        refs.file,
        PB.el("div", { class: "pb-tool-actions" },
          PB.el("button", { class: "pb-btn", text: "手动导入 mihomo.exe / .zip", on: { click: function () { PB.importCore(); } } }),
          PB.el("button", { class: "pb-btn", text: "打开内核目录", on: { click: openCore } }),
          PB.el("button", { class: "pb-btn", text: "打开配置目录", on: { click: function () { PB.openHome(); } } })
        )
      ),
      PB.el("section", { class: "pb-card" },
        PB.el("h3", { text: "测速配置" }),
        field("延迟测试 URL", refs.delay),
        field("下载测速 URL", refs.speed),
        field("单节点测速上限", refs.max)
      ),
      PB.el("section", { class: "pb-card" },
        PB.el("h3", { text: "AI 工具路径覆盖" }),
        field("Cursor 路径", pathRow(refs.cursor)),
        field("Antigravity 路径", pathRow(refs.anti)),
        field("Codex 路径", pathRow(refs.codex))
      ),
      PB.el("section", { class: "pb-card" },
        PB.el("h3", { text: "通用偏好" }),
        PB.el("div", { class: "pb-stack" },
          prefRow("自动优选 AI 节点", refs.opt),
          prefRow("代理变更时自动同步已开启的 AI 工具", refs.sync),
          prefRow("同步写入 Windows 用户环境变量", refs.env),
          prefRow("关闭窗口时最小化到托盘", refs.tray)
        )
      ),
      PB.el("div", { class: "pb-savebar" }, refs.save)
    ));
    fill();
  }

  function show() {
    paintPorts();
    if (!dirty) fill();
    PB.api("GET", "/api/snapshot").then(PB.setSnapshot).catch(function (err) {
      PB.toast((err && err.message) || "读取设置失败", "err");
    });
  }

  PB.addTab("settings", {
    mount: mount,
    show: show,
    hide: function () {},
    onCoreChange: paintPorts,
    onSnapshot: function () {
      paintPorts();
      if (!dirty) fill();
    }
  });
})();
