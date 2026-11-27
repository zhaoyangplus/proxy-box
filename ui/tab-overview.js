(function () {
  "use strict";

  var O = globalThis.PBOverview;
  if (!O || typeof PB === "undefined" || typeof PB.registerTab !== "function") return;

  var root = null;
  var lists = { daily: undefined, ai: undefined };
  var subs = { state: "loading", text: "正在读取订阅" };
  var lastEvent = null;
  var localError = "";
  var actionLock = "";
  var stopRequested = false;
  var optBusy = false;
  var sysBusy = false;
  var bypassBusy = false;
  var listGen = 0;
  var subsGen = 0;
  var lastSig = "";
  var wasRunning = false;

  function shown() {
    if (!root || root.isConnected === false) return false;
    return !PB.current || PB.current() === "overview";
  }

  function core() {
    return PB.state.core || {};
  }

  function modeOf() {
    var value = core().mode || "rule";
    if (value === "rule" || value === "global" || value === "direct") return value;
    return value || "rule";
  }

  function build() {
    var status = core();
    var running = !!status.running;
    var phase = PB.state.phase || (running ? "running" : "stopped");
    var event = lastEvent || {};
    var busyStart = actionLock === "start" && phase !== "download";
    var hero = O.heroState({
      running: running,
      phase: phase,
      version: status.version || "",
      message: event.message || "",
      downloaded: event.downloaded,
      total: event.total,
      error: localError,
      busy: actionLock === "stop" || busyStart
    });
    var mode = modeOf();
    var runningMode = running && !hero.downloading;
    return {
      hero: hero,
      daily: O.channelTile("daily", lists.daily, running, status.mixedPort),
      ai: O.channelTile("ai", lists.ai, running, status.aiDedicatedPort),
      aiBusy: optBusy,
      mode: mode,
      modeHTML: PB.segHTML("ovMode", [
        { v: "rule", html: "规则分流" },
        { v: "global", html: "全局代理" },
        { v: "direct", html: "全部直连" }
      ], mode, "full" + (runningMode ? "" : " is-disabled")),
      sysSwitch: PB.sw(!!status.systemProxyActive, 'id="ovSys" aria-label="系统代理"', !running || sysBusy),
      sysSub: O.sysSub(running, !!status.systemProxyActive, status.mixedPort),
      bypassSwitch: PB.sw(!!(status.settings && status.settings.bypassCN), 'id="ovBypass" aria-label="绕过中国大陆"', bypassBusy),
      geo: O.geoStatus(status, event),
      subs: subs,
      apps: O.appsSummary(PB.state.snapshot && PB.state.snapshot.appConfig)
    };
  }

  function signature(model) {
    return JSON.stringify({
      primary: model.hero.primary,
      running: model.hero.running,
      title: model.hero.title,
      caption: model.hero.caption,
      error: model.hero.error,
      busy: model.hero.busy,
      showImport: model.hero.showImport,
      mode: model.mode,
      modeHTML: model.modeHTML,
      daily: model.daily,
      ai: model.ai,
      aiBusy: model.aiBusy,
      sys: model.sysSub,
      bypass: model.bypassSwitch,
      geoKind: model.geo.kind,
      geoRetry: model.geo.retry,
      subs: model.subs,
      apps: model.apps.text
    });
  }

  function patch(model) {
    var progress = root.querySelector("[data-field=progress]");
    var bar = root.querySelector("[data-field=bar]");
    var geo = root.querySelector("[data-field=geo]");
    if (model.hero.progress) {
      if (!progress || !bar) return false;
      progress.textContent = model.hero.progress.label;
      bar.style.width = O.cssPercent(model.hero.progress.percent);
    }
    if (geo) geo.textContent = model.geo.text || "";
    var button = root.querySelector("#ovCore");
    if (button) button.disabled = !!model.hero.busy;
    return true;
  }

  function paint() {
    if (!shown()) return;
    var model = build();
    var sig = signature(model);
    if (sig === lastSig && root.querySelector(".ov")) {
      if (patch(model)) return;
    }
    lastSig = sig;
    root.innerHTML = O.pageHTML(model);
    bind();
  }

  function bind() {
    var seg = root.querySelector("#ovMode");
    if (seg && typeof PB.mountSeg === "function") {
      PB.mountSeg(seg, function (value) {
        var prev = modeOf();
        if (!core().running) {
          if (typeof PB.segSet === "function") PB.segSet(seg, prev);
          return;
        }
        var desc = root.querySelector("#ovModeDesc");
        if (desc) desc.textContent = O.modeText(value);
        PB.api("POST", "/api/core/mode", { mode: value }).then(function () {
          PB.toast("已热切换为「" + O.modeLabel(value) + "」");
          if (typeof PB.refreshCore === "function") return PB.refreshCore();
        }).catch(function (err) {
          if (typeof PB.segSet === "function") PB.segSet(seg, prev);
          if (desc) desc.textContent = O.modeText(prev);
          PB.toast((err && err.message) || "切换模式失败", "err");
        });
      });
    }
    root.onclick = onClick;
    root.onkeydown = onKey;
  }

  function openGroup(group) {
    PB.state.nodeGroup = group === "ai" ? "ai" : "daily";
    PB.goTab("nodes");
  }

  function onClick(event) {
    var target = event.target;
    if (!target || !target.closest) return;
    if (target.closest("#ovCore")) return onPrimary();
    if (target.closest("#ovImport")) return importCore();
    if (target.closest("#ovOpt")) {
      event.stopPropagation();
      return optimize();
    }
    if (target.closest("#ovSys")) return toggleSys();
    if (target.closest("#ovBypass")) return toggleBypass();
    if (target.closest("#ovGeoRetry")) return retryGeo();
    if (target.closest("button")) return;
    var tile = target.closest("[data-go-ch]");
    if (tile) return openGroup(tile.getAttribute("data-go-ch"));
    var jump = target.closest("[data-go]");
    if (jump) PB.goTab(jump.getAttribute("data-go"));
  }

  function onKey(event) {
    var tile = event.target && event.target.closest && event.target.closest("[data-go-ch]");
    if (tile && event.target === tile && (event.key === "Enter" || event.key === " ")) {
      event.preventDefault();
      openGroup(tile.getAttribute("data-go-ch"));
      return;
    }
    var jump = event.target && event.target.closest && event.target.closest("[data-go]");
    if (jump && event.target === jump && (event.key === "Enter" || event.key === " ")) {
      event.preventDefault();
      PB.goTab(jump.getAttribute("data-go"));
    }
  }

  function loadNodes() {
    var gen = ++listGen;
    if (!core().running) {
      lists.daily = undefined;
      lists.ai = undefined;
      paint();
      return Promise.resolve();
    }
    return Promise.all([
      PB.api("POST", "/api/nodes/list", { client: "builtin", group: O.GROUP.daily, testDelay: false }),
      PB.api("POST", "/api/nodes/list", { client: "builtin", group: O.GROUP.ai, testDelay: false })
    ]).then(function (pair) {
      if (gen !== listGen) return;
      lists.daily = pair[0];
      lists.ai = pair[1];
      paint();
    }, function () {
      if (gen !== listGen) return;
      lists.daily = null;
      lists.ai = null;
      paint();
    });
  }

  function loadSubs() {
    var gen = ++subsGen;
    return PB.api("GET", "/api/subs").then(function (data) {
      if (gen !== subsGen) return;
      subs = O.subsSummary((data && data.subscriptions) || [], Date.now());
      paint();
    }, function () {
      if (gen !== subsGen) return;
      subs = null;
      paint();
    });
  }

  function startCore() {
    if (actionLock === "start") return;
    actionLock = "start";
    stopRequested = false;
    localError = "";
    lastSig = "";
    paint();
    PB.api("POST", "/api/core/start", {}).then(function () {
      localError = "";
      if (typeof PB.refreshCore === "function") return PB.refreshCore();
    }).catch(function (err) {
      var msg = (err && err.message) || "启动失败";
      if (stopRequested || msg.indexOf("取消") >= 0) return;
      localError = msg;
      PB.toast(msg, "err");
    }).then(function () {
      actionLock = "";
      lastSig = "";
      paint();
      loadNodes();
    });
  }

  function stopCore() {
    if (actionLock === "stop") return;
    stopRequested = true;
    actionLock = "stop";
    lastSig = "";
    paint();
    PB.api("POST", "/api/core/stop", {}).then(function () {
      localError = "";
      lastEvent = { phase: "stopped", message: "内核已停止", downloaded: 0, total: 0 };
      PB.toast("内核已停止", "info");
      if (typeof PB.refreshCore === "function") return PB.refreshCore();
    }).catch(function (err) {
      PB.toast((err && err.message) || "停止失败", "err");
    }).then(function () {
      actionLock = "";
      lastSig = "";
      paint();
    });
  }

  function onPrimary() {
    var status = core();
    if (status.running || PB.state.phase === "download") return stopCore();
    return startCore();
  }

  function importCore() {
    PB.api("POST", "/api/browse-core", {}).then(function (res) {
      var path = res && String(res.path || "").trim();
      if (!path) return;
      return PB.api("POST", "/api/core/import-binary", { path: path }).then(function () {
        PB.toast("已导入内核");
        return startCore();
      });
    }).catch(function (err) {
      var msg = (err && err.message) || "导入失败";
      localError = msg;
      lastSig = "";
      PB.toast(msg, "err");
      paint();
    });
  }

  function optimize() {
    if (optBusy || !core().running) return;
    optBusy = true;
    lastSig = "";
    paint();
    PB.api("POST", "/api/optimize-node", {}).then(function (res) {
      if (res && res.snapshot) PB.state.snapshot = res.snapshot;
      PB.toast((res && res.message) || "已完成优选");
      return loadNodes();
    }).catch(function (err) {
      PB.toast((err && err.message) || "优选失败", "err");
    }).then(function () {
      optBusy = false;
      lastSig = "";
      paint();
    });
  }

  function toggleSys() {
    if (sysBusy) return;
    var status = core();
    if (!status.running) return;
    var next = !status.systemProxyActive;
    sysBusy = true;
    lastSig = "";
    paint();
    PB.api("POST", "/api/sysproxy/set", { enable: next }).then(function () {
      PB.toast(next ? "系统代理已开启" : "系统代理已关闭", next ? "ok" : "info");
      if (typeof PB.refreshCore === "function") return PB.refreshCore();
    }).catch(function (err) {
      PB.toast((err && err.message) || "系统代理切换失败", "err");
    }).then(function () {
      sysBusy = false;
      lastSig = "";
      paint();
    });
  }

  function toggleBypass() {
    if (bypassBusy) return;
    var on = !!(core().settings && core().settings.bypassCN);
    var next = !on;
    bypassBusy = true;
    lastSig = "";
    paint();
    PB.api("POST", "/api/core/bypass-cn", { enable: next }).then(function () {
      PB.toast(next ? "已开启绕过中国大陆" : "已关闭绕过中国大陆", next ? "ok" : "info");
      if (typeof PB.refreshCore === "function") return PB.refreshCore();
    }).catch(function (err) {
      PB.toast((err && err.message) || "绕过大陆切换失败", "err");
    }).then(function () {
      bypassBusy = false;
      lastSig = "";
      paint();
    });
  }

  function retryGeo() {
    if (bypassBusy) return;
    bypassBusy = true;
    lastSig = "";
    paint();
    PB.api("POST", "/api/core/bypass-cn", { retry: true }).then(function () {
      PB.toast("正在重新下载地理数据");
      if (typeof PB.refreshCore === "function") return PB.refreshCore();
    }).catch(function (err) {
      PB.toast((err && err.message) || "重试失败", "err");
    }).then(function () {
      bypassBusy = false;
      lastSig = "";
      paint();
    });
  }

  PB.registerTab("overview", {
    enter: function (node) {
      root = node;
      lastSig = "";
      if (typeof PB.setChannel === "function") PB.setChannel("daily");
      if (typeof PB.topbar === "function") {
        PB.topbar({
          title: "总览",
          sub: "内核状态、双通道出口与常用开关",
          actions: "",
          acc: ""
        });
      }
      wasRunning = !!core().running;
      paint();
      loadNodes();
      loadSubs();
      if (typeof PB.refreshSnapshot === "function") {
        PB.refreshSnapshot().then(function () { paint(); }).catch(function () {});
      }
    },
    onCore: function () {
      var running = !!core().running;
      if (running !== wasRunning) {
        wasRunning = running;
        loadNodes();
      }
      paint();
    },
    onSSE: function (msg) {
      if (!msg) return;
      if (msg.type === "core" && msg.core) {
        lastEvent = msg.core;
        if (msg.core.phase === "running" || msg.core.phase === "stopped") localError = "";
        if (msg.core.phase === "error" && msg.core.message) localError = msg.core.message;
        if (msg.core.phase === "geo" && typeof PB.refreshCore === "function") PB.refreshCore();
        if (msg.core.phase === "running") loadNodes();
        paint();
      }
      if (msg.type === "sync") {
        loadNodes();
        loadSubs();
      }
    }
  });
})();
