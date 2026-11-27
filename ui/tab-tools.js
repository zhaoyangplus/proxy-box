(function () {
  "use strict";

  var ORDER = ["cursor", "antigravity", "claude_code", "codex"];
  var SUB = {
    cursor: "AI 代码编辑器",
    antigravity: "AI 开发环境",
    claude_code: "终端编程助手",
    codex: "OpenAI 编程智能体"
  };
  var MARK = { cursor: "C", antigravity: "A", claude_code: "L", codex: "X" };
  var grid = null;
  var focusId = "";

  function byId(id) {
    var list = (PB.state.snapshot && PB.state.snapshot.targets) || [];
    var i;
    for (i = 0; i < list.length; i++) {
      if (list[i] && list[i].id === id) return list[i];
    }
    return { id: id, name: id, installed: false };
  }

  function badge(text, kind) {
    return PB.el("span", { class: "pb-badge pb-badge-" + kind, text: text });
  }

  function run(btn, path, body, done) {
    var prev = btn.textContent;
    btn.disabled = true;
    PB.api("POST", path, body || {}).then(function (res) {
      PB.toast((res && res.message) || "已完成");
      if (done) return done(res);
    }).catch(function (err) {
      PB.toast((err && err.message) || "操作失败", "err");
    }).finally(function () {
      btn.disabled = false;
      btn.textContent = prev;
    });
  }

  function card(t) {
    var on = PB.userEnabled(t);
    var root = PB.el("article", { class: "pb-card pb-tool" + (on ? "" : " is-off") });
    var sw = PB.el("button", {
      class: "pb-switch",
      attrs: {
        role: "switch",
        "aria-checked": on ? "true" : "false",
        "aria-label": (t.name || t.id) + " 托管开关",
        "data-switch": t.id
      },
      on: { click: function () { toggle(t, !PB.userEnabled(t)); } }
    });
    var pills = PB.el("div", { class: "pb-pills" });
    if (!t.installed) pills.appendChild(badge("未检出", "warn"));
    else if (t.running) pills.appendChild(badge("运行中", "ok"));
    else pills.appendChild(badge("未运行", "muted"));
    var body = PB.el("div", { class: "pb-tool-body" }, pills);
    if (!on) {
      body.appendChild(PB.el("p", { text: "未开启托管（保持应用默认设置）" }));
    } else {
      body.appendChild(PB.el("div", { class: "pb-mono pb-num", text: "代理端口 :" + PB.ports().ai }));
      var lines = [];
      if (t.id === "cursor") lines.push(t.http2Disabled ? "HTTP/2 已禁用" : "HTTP/2 未禁用");
      lines.push(t.terminalEnvConfigured ? "终端环境已写入" : "终端环境未写入");
      lines.push(t.proxyConfigured ? "已检测到代理配置" : "未检测到代理配置");
      if (t.statusText) lines.push(t.statusText);
      lines.forEach(function (line) {
        body.appendChild(PB.el("div", { class: "pb-muted", text: line }));
      });
      var launch = PB.el("button", {
        class: "pb-btn pb-btn-primary pb-btn-sm",
        text: t.running ? "代理重启" : "代理启动",
        disabled: !t.installed,
        title: t.installed ? "" : "未检测到程序",
        on: {
          click: function (ev) {
            run(ev.currentTarget, "/api/launch", { targetId: t.id, restartIfRunning: !!t.running }, function () {
              return PB.api("GET", "/api/snapshot").then(PB.setSnapshot);
            });
          }
        }
      });
      var shortcut = PB.el("button", {
        class: "pb-btn pb-btn-sm",
        text: "创建桌面快捷方式",
        on: { click: function (ev) { run(ev.currentTarget, "/api/shortcut", { targetId: t.id }); } }
      });
      var open = PB.el("button", {
        class: "pb-btn pb-btn-sm",
        text: "打开配置目录",
        disabled: !t.settingsPath,
        title: t.settingsPath ? t.settingsPath : "未找到配置目录",
        on: {
          click: function (ev) {
            run(ev.currentTarget, "/api/open-path", { path: t.settingsPath, parent: true });
          }
        }
      });
      body.appendChild(PB.el("div", { class: "pb-tool-actions" }, launch, shortcut, open));
    }
    root.appendChild(PB.el("div", { class: "pb-tool-head" },
      PB.el("div", { class: "pb-id" },
        PB.el("span", { class: "pb-mark", attrs: { "aria-hidden": "true" }, text: MARK[t.id] || "•" }),
        PB.el("div", {},
          PB.el("div", { class: "pb-tool-name", text: t.name || t.id }),
          PB.el("div", { class: "pb-tool-sub", text: SUB[t.id] || "AI 开发工具" })
        )
      ),
      sw
    ));
    root.appendChild(body);
    return root;
  }

  function paint(id) {
    if (!grid) return;
    focusId = id || "";
    grid.replaceChildren.apply(grid, ORDER.map(function (key) { return card(byId(key)); }));
    if (focusId && window.CSS && CSS.escape) {
      var sw = grid.querySelector('[data-switch="' + CSS.escape(focusId) + '"]');
      if (sw) sw.focus();
    }
  }

  function toggle(target, enabled) {
    var had = Object.prototype.hasOwnProperty.call(target, "enabledByUser");
    var prev = target.enabledByUser;
    target.enabledByUser = enabled;
    paint(target.id);
    PB.api("POST", "/api/tools/toggle", { targetId: target.id, enabled: !!enabled }).then(function (res) {
      if (res && res.snapshot) {
        PB.setSnapshot(res.snapshot);
      } else if (PB.state.snapshot) {
        var snap = PB.state.snapshot;
        if (!snap.appConfig) snap.appConfig = {};
        if (!snap.appConfig.managedApps) snap.appConfig.managedApps = {};
        snap.appConfig.managedApps[target.id] = !!enabled;
        PB.setSnapshot(snap);
      }
      if (res && res.message) PB.toast(res.message);
    }).catch(function (err) {
      if (had) target.enabledByUser = prev;
      else delete target.enabledByUser;
      paint(target.id);
      PB.toast((err && err.message) || "开关失败", "err");
    });
  }

  function headerActions() {
    var sync = PB.el("button", { class: "pb-btn pb-btn-primary", text: "同步已开启应用" });
    var restore = PB.el("button", { class: "pb-btn", text: "还原全部" });
    sync.addEventListener("click", function () {
      run(sync, "/api/one-click", {}, function () {
        return PB.api("GET", "/api/snapshot").then(PB.setSnapshot);
      });
    });
    restore.addEventListener("click", function () {
      run(restore, "/api/restore-all", {}, function () {
        return PB.api("GET", "/api/snapshot").then(PB.setSnapshot);
      });
    });
    return [sync, restore];
  }

  function mount(root) {
    grid = PB.el("div", { class: "pb-grid-2" });
    root.appendChild(grid);
    paint();
  }

  function show() {
    PB.setActions(headerActions());
    paint();
    PB.api("GET", "/api/snapshot").then(PB.setSnapshot).catch(function (err) {
      PB.toast((err && err.message) || "读取应用状态失败", "err");
    });
  }

  PB.addTab("tools", {
    mount: mount,
    show: show,
    hide: function () {},
    onSnapshot: function () { paint(); }
  });
})();
