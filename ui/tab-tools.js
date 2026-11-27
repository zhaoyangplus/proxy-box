(function () {
  "use strict";
  var L = window.PBTools;
  if (!L) throw new Error("ui/tools-logic.js must load before ui/tab-tools.js");

  var rootEl = null;
  var pending = null;
  var seenPort = null;

  function rawTargets() {
    var snap = PB.state.snapshot;
    return (snap && snap.targets) || [];
  }

  function viewTargets() {
    if (!pending) return rawTargets();
    return L.applyEnabled(rawTargets(), pending.id, pending.enabled);
  }

  function port() {
    return PB.state.core && PB.state.core.aiDedicatedPort;
  }

  function paintBadge() {
    var n = L.enabledCount(viewTargets());
    PB.setBadge("tools", n ? String(n) : "");
  }

  function show() {
    paintBadge();
    if (!rootEl || PB.current() !== "tools") return;
    rootEl.innerHTML = L.pageHTML(viewTargets(), { port: port(), busyId: pending ? pending.id : "" });
  }

  function targetById(id) {
    var list = L.orderedTargets(viewTargets());
    for (var i = 0; i < list.length; i++) {
      if (list[i].id === id) return list[i];
    }
    return null;
  }

  function toggle(target) {
    if (!target || pending) return;
    var next = !target.enabledByUser;
    pending = { id: target.id, enabled: next };
    show();
    PB.api("POST", "/api/tools/toggle", { targetId: target.id, enabled: next }).then(function (res) {
      pending = null;
      if (res && res.snapshot) PB.state.snapshot = res.snapshot;
      PB.toast((res && res.message) || (next ? "已开启" : "已关闭"), { tone: next ? "ok" : "info" });
      if (res && res.snapshot) return null;
      return PB.refreshSnapshot().catch(function () { return null; });
    }).then(function () {
      show();
    }).catch(function (err) {
      pending = null;
      show();
      PB.toast((err && err.message) || "切换失败", { tone: "err" });
    });
  }

  function launch(target) {
    var restart = !!target.running;
    PB.api("POST", "/api/launch", { targetId: target.id, restartIfRunning: restart }).then(function (res) {
      PB.toast((res && res.message) || (restart ? "已重启" : "已代理启动"));
      return PB.refreshSnapshot().catch(function () { return null; });
    }).then(function () {
      show();
    }).catch(function (err) {
      PB.toast((err && err.message) || "启动失败", { tone: "err" });
    });
  }

  function shortcut(target) {
    PB.api("POST", "/api/shortcut", { targetId: target.id }).then(function (res) {
      PB.toast((res && res.message) || "已创建桌面快捷方式");
    }).catch(function (err) {
      PB.toast((err && err.message) || "创建快捷方式失败", { tone: "err" });
    });
  }

  function openDir(target) {
    PB.api("POST", "/api/open-path", { path: target.settingsPath || "", parent: true }).then(function () {
      PB.toast("已打开所在目录", { tone: "info" });
    }).catch(function (err) {
      PB.toast((err && err.message) || "打开目录失败", { tone: "err" });
    });
  }

  function copyPath(target) {
    if (!target.settingsPath) {
      PB.toast("没有可复制的路径", { tone: "info" });
      return;
    }
    PB.copyText(target.settingsPath, "配置路径已复制");
  }

  function copyEnv() {
    var snippet = L.envSnippet(port());
    if (!snippet) {
      PB.toast("AI 端口未知", { tone: "err" });
      return;
    }
    PB.copyText(snippet, "PowerShell 环境变量命令已复制");
  }

  function restoreAll() {
    PB.confirmSheet({
      title: "还原全部应用？",
      danger: true,
      okLabel: "全部还原",
      body: "将关闭所有应用的托管，并只回滚 Proxy Box 写入的代理相关键值。",
      onOk: function () {
        return PB.api("POST", "/api/restore-all", {}).then(function (res) {
          PB.toast((res && res.message) || "已还原全部应用", { tone: "info" });
          return PB.refreshSnapshot().catch(function () { return null; });
        }).then(function () {
          show();
        });
      }
    });
  }

  function syncOn() {
    var btn = document.getElementById("tlSync");
    if (btn && btn.disabled) return;
    if (btn) btn.disabled = true;
    PB.api("POST", "/api/one-click", {}).then(function (res) {
      PB.toast((res && res.message) || "已同步已开启的应用");
      return PB.refreshSnapshot().catch(function () { return null; });
    }).then(function () {
      show();
    }).catch(function (err) {
      PB.toast((err && err.message) || "同步失败", { tone: "err" });
    }).then(function () {
      var again = document.getElementById("tlSync");
      if (again) again.disabled = false;
    });
  }

  function onClick(event) {
    var actEl = event.target && event.target.closest ? event.target.closest("[data-act]") : null;
    if (!actEl || actEl.disabled) return;
    var card = event.target.closest("[data-tid]");
    if (!card) return;
    var target = targetById(card.getAttribute("data-tid"));
    if (!target) return;
    var act = actEl.getAttribute("data-act");
    if (act === "toggle") toggle(target);
    else if (act === "copy") copyPath(target);
    else if (act === "dir") openDir(target);
    else if (act === "launch") launch(target);
    else if (act === "shortcut") shortcut(target);
    else if (act === "env") copyEnv();
  }

  function bindTop() {
    var bar = document.getElementById("tbActions");
    if (!bar) return;
    bar.onclick = function (event) {
      var node = event.target && event.target.closest ? event.target : null;
      if (!node || !node.closest) return;
      if (node.closest("#tlRestore")) restoreAll();
      else if (node.closest("#tlSync")) syncOn();
    };
  }

  PB.registerTab("tools", {
    enter: function (root) {
      rootEl = root;
      PB.topbar({ actions: L.topbarHTML() });
      bindTop();
      root.onclick = onClick;
      show();
      if (!PB.state.snapshot) {
        PB.refreshSnapshot().then(show).catch(function (err) {
          PB.toast((err && err.message) || "读取应用失败", { tone: "err" });
        });
      }
    },
    onSSE: function (msg) {
      if (!msg || msg.type !== "sync" || pending) return;
      show();
    },
    onCore: function (status) {
      var next = status && status.aiDedicatedPort;
      if (seenPort === next) {
        paintBadge();
        return;
      }
      seenPort = next;
      if (PB.current() === "tools") show();
      else paintBadge();
    }
  });

  document.addEventListener("DOMContentLoaded", function () {
    if (PB.state.snapshot) paintBadge();
    else PB.refreshSnapshot().then(paintBadge).catch(function () {});
  });
})();
