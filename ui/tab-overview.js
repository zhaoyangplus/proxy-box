(function () {
  "use strict";

  var refs = null;
  var subs = null;
  var subsError = "";
  var egressGen = 0;
  var cache = { daily: null, ai: null, loading: false };
  var modes = {};

  function setSwitch(btn, on) {
    btn.setAttribute("aria-checked", on ? "true" : "false");
  }

  function running() {
    return !!(PB.state.core && PB.state.core.running);
  }

  function bypassOn() {
    var core = PB.state.core;
    return !!(core && core.settings && core.settings.bypassCN);
  }

  function geoText() {
    var status = PB.state.core;
    var live = PB.phaseView();
    var geoLive = live.geoLive || "";
    var downloading = (status && status.geoDownloading) || (/下载/.test(geoLive) && !/已下载|已开启/.test(geoLive));
    if (downloading) {
      if (live.geoPct != null) return "下载中 " + live.geoPct + "%";
      return geoLive || "下载中";
    }
    if (status && status.geoError) return status.geoError;
    if (status && status.geoReady) return "地理数据已就绪";
    if (geoLive) return geoLive;
    return "尚未下载地理数据";
  }

  function describe(list) {
    var name = (list && (list.resolvedNode || list.currentSelect)) || "";
    var nodes = (list && list.nodes) || [];
    var node = null;
    var i;
    for (i = 0; i < nodes.length; i++) {
      if (nodes[i] && nodes[i].name === name) { node = nodes[i]; break; }
    }
    if (!node) {
      for (i = 0; i < nodes.length; i++) {
        if (nodes[i] && nodes[i].selected) { node = nodes[i]; break; }
      }
    }
    var delay = "未测";
    if (node && !node.untested) delay = node.delayMs ? PB.fmt.ms(node.delayMs) : "超时";
    var extra = "";
    if (node && node.speedTested && isFinite(Number(node.speedMBps))) extra = " · " + PB.fmt.mbps(node.speedMBps);
    else if (node && node.speedError) extra = " · 测速失败";
    return { name: name || "未选择", meta: delay + extra };
  }

  function sideView(slot) {
    if (!running()) return { name: "内核未运行", meta: "—" };
    if (!slot) return cache.loading ? { name: "正在读取", meta: "—" } : { name: "未选择", meta: "未测" };
    if (slot.ok === false) return { name: "读取失败", meta: slot.error || "读取失败" };
    return describe(slot.value);
  }

  function paintHero() {
    var view = PB.phaseView();
    var phase = view.phase;
    var core = PB.state.core;
    if (phase === "running") {
      var ver = core && core.version;
      refs.title.textContent = ver ? "运行中 · " + ver : "运行中";
    } else if (phase === "download") refs.title.textContent = "下载中";
    else if (phase === "error") refs.title.textContent = "启动失败";
    else refs.title.textContent = "已停止";

    if (phase === "error") {
      refs.msg.textContent = "";
      refs.err.hidden = false;
      refs.err.textContent = view.message || "启动失败";
    } else {
      refs.err.hidden = true;
      refs.err.textContent = "";
      if (phase === "download") {
        var pct = view.total > 0 ? Math.round((view.downloaded / view.total) * 100) : null;
        refs.msg.textContent = (view.message || "正在下载内核") + (pct == null ? "" : " " + pct + "%");
      } else refs.msg.textContent = "";
    }

    var busy = phase === "download" && !(view.total > 0);
    refs.barWrap.hidden = phase !== "download";
    refs.barWrap.classList.toggle("is-busy", busy);
    if (phase === "download" && view.total > 0) {
      refs.bar.style.width = Math.max(0, Math.min(100, (view.downloaded / view.total) * 100)).toFixed(1) + "%";
    } else refs.bar.style.width = "";

    if (phase === "running") {
      refs.toggle.textContent = "停止内核";
      refs.toggle.className = "pb-btn pb-btn-danger pb-btn-lg";
    } else if (phase === "download") {
      refs.toggle.textContent = "取消启动";
      refs.toggle.className = "pb-btn pb-btn-danger pb-btn-lg";
    } else {
      refs.toggle.textContent = "启动内核";
      refs.toggle.className = "pb-btn pb-btn-primary pb-btn-lg";
    }
    refs.importBtn.hidden = phase === "running" || phase === "download";
  }

  function paintEgress() {
    if (!refs) return;
    var ports = PB.ports();
    refs.dailyPort.textContent = "日常出口 :" + ports.mixed;
    refs.aiPort.textContent = "AI 专属出口 :" + ports.ai;
    var daily = sideView(cache.daily);
    var ai = sideView(cache.ai);
    refs.dailyName.textContent = daily.name;
    refs.dailyName.title = daily.name;
    refs.dailyMeta.textContent = daily.meta;
    refs.aiName.textContent = ai.name;
    refs.aiName.title = ai.name;
    refs.aiMeta.textContent = ai.meta;
    refs.opt.disabled = !running();
  }

  function paintQuick() {
    var core = PB.state.core;
    var mode = (core && core.mode) || "rule";
    var on = running();
    ["rule", "global", "direct"].forEach(function (id) {
      var btn = modes[id];
      btn.disabled = !on;
      btn.setAttribute("aria-pressed", mode === id ? "true" : "false");
    });
    var proxyOn = !!(core && core.systemProxyActive);
    refs.sys.disabled = !on;
    setSwitch(refs.sys, proxyOn);
    refs.sysHint.hidden = on;
    refs.sysHint.textContent = on ? "" : "内核停止时不能开关系统代理";
    var bypass = bypassOn();
    refs.bypass.disabled = !on && !bypass;
    setSwitch(refs.bypass, bypass);
    refs.bypassHint.hidden = on;
    refs.bypassHint.textContent = on ? "" : "启动内核后才能下载地理数据";
    var bad = !!(core && core.geoError);
    refs.geo.textContent = geoText();
    refs.geo.className = bad ? "pb-error" : "pb-hint";
    refs.retry.hidden = !bad;
    refs.retry.disabled = !on;
  }

  function paintStats() {
    if (subsError) refs.subStat.textContent = subsError;
    else if (!subs) refs.subStat.textContent = "读取中";
    else {
      var enabled = subs.filter(function (s) { return s && s.enabled; });
      var nodes = enabled.reduce(function (sum, s) { return sum + (Number(s.nodeCount) || 0); }, 0);
      refs.subStat.textContent = enabled.length + " 个已启用 · " + nodes + " 个节点";
    }
    var targets = (PB.state.snapshot && PB.state.snapshot.targets) || [];
    var known = { cursor: 1, antigravity: 1, claude_code: 1, codex: 1 };
    var n = 0;
    targets.forEach(function (t) {
      if (t && known[t.id] && PB.userEnabled(t)) n += 1;
    });
    refs.aiStat.textContent = n + " / 4 已启用";
  }

  function paint() {
    if (!refs) return;
    paintHero();
    paintEgress();
    paintQuick();
    paintStats();
  }

  function settle(promise) {
    return promise.then(function (value) {
      return { ok: true, value: value };
    }, function (err) {
      return { ok: false, error: (err && err.message) || "读取失败" };
    });
  }

  function loadEgress() {
    var gen = ++egressGen;
    if (!running()) {
      cache.daily = null;
      cache.ai = null;
      cache.loading = false;
      paintEgress();
      return Promise.resolve();
    }
    if (!cache.daily && !cache.ai) cache.loading = true;
    paintEgress();
    var daily = PB.api("POST", "/api/nodes/list", { group: "PROXY", client: "builtin", testDelay: false });
    var ai = PB.api("POST", "/api/nodes/list", { group: "🤖 AI-IDE", client: "builtin", testDelay: false });
    return Promise.all([settle(daily), settle(ai)]).then(function (pair) {
      if (gen !== egressGen) return;
      cache.loading = false;
      cache.daily = pair[0];
      cache.ai = pair[1];
      paintEgress();
    });
  }

  function loadSubs() {
    if (!refs) return Promise.resolve();
    return PB.api("GET", "/api/subs").then(function (data) {
      subs = data.subscriptions || [];
      subsError = "";
      var bad = subs.some(function (s) { return s && s.lastError; });
      var any = subs.some(function (s) { return s && s.enabled; });
      PB.setDot("subs", bad ? "err" : any ? "ok" : "");
      paintStats();
    }).catch(function (err) {
      subsError = (err && err.message) || "读取订阅失败";
      paintStats();
    });
  }

  function onToggle() {
    var phase = PB.phaseView().phase;
    if (phase === "running" || phase === "download") PB.stopCore();
    else PB.startCore();
  }

  function setMode(mode) {
    if (!running() || !PB.state.core) return;
    var prev = PB.state.core.mode;
    PB.state.core.mode = mode;
    paintQuick();
    PB.paintChrome();
    PB.api("POST", "/api/core/mode", { mode: mode }).then(function () {
      return PB.refreshCore();
    }).catch(function (err) {
      if (PB.state.core) PB.state.core.mode = prev;
      paintQuick();
      PB.paintChrome();
      PB.toast((err && err.message) || "切换模式失败", "err");
    });
  }

  function setProxy(enable) {
    if (!running() || !PB.state.core) return;
    var prev = !!PB.state.core.systemProxyActive;
    PB.state.core.systemProxyActive = !!enable;
    paintQuick();
    PB.paintChrome();
    PB.api("POST", "/api/sysproxy/set", { enable: !!enable }).then(function () {
      return PB.refreshCore();
    }).catch(function (err) {
      if (PB.state.core) PB.state.core.systemProxyActive = prev;
      paintQuick();
      PB.paintChrome();
      PB.toast((err && err.message) || "系统代理切换失败", "err");
    });
  }

  function setBypass(enable, retry) {
    if (!running() && enable) return;
    refs.bypass.disabled = true;
    PB.api("POST", "/api/core/bypass-cn", { enable: !!enable, retry: !!retry }).then(function () {
      return PB.refreshCore();
    }).catch(function (err) {
      PB.toast((err && err.message) || "绕过大陆切换失败", "err");
      return PB.refreshCore().catch(function () {});
    }).finally(function () { paintQuick(); });
  }

  function optimize() {
    if (!running()) return;
    refs.opt.disabled = true;
    refs.opt.textContent = "优选中";
    PB.api("POST", "/api/optimize-node", {}).then(function (res) {
      if (res && res.snapshot) PB.setSnapshot(res.snapshot);
      PB.toast((res && res.message) || "已优选节点");
      return loadEgress();
    }).catch(function (err) {
      PB.toast((err && err.message) || "优选失败", "err");
    }).finally(function () {
      if (refs) {
        refs.opt.disabled = !running();
        refs.opt.textContent = "一键优选";
      }
    });
  }

  function mount(root) {
    refs = {};
    refs.title = PB.el("div", { class: "pb-hero-title", attrs: { id: "pb-hero-phase" }, text: "已停止" });
    refs.msg = PB.el("div", { class: "pb-hero-msg", attrs: { id: "pb-hero-msg" } });
    refs.err = PB.el("div", { class: "pb-error", hidden: true });
    refs.bar = PB.el("span");
    refs.barWrap = PB.el("div", { class: "pb-progress pb-progress-light", hidden: true }, refs.bar);
    refs.toggle = PB.el("button", {
      class: "pb-btn pb-btn-primary pb-btn-lg",
      attrs: { id: "pb-core-toggle" },
      text: "启动内核",
      on: { click: onToggle }
    });
    refs.importBtn = PB.el("button", {
      class: "pb-btn pb-btn-ghost pb-on-dark pb-btn-sm",
      attrs: { id: "pb-core-import" },
      text: "选择 mihomo.exe / .zip",
      on: { click: function () { PB.importCore(); } }
    });
    refs.dailyPort = PB.el("div", { class: "pb-kicker" });
    refs.dailyName = PB.el("div", { class: "pb-egress-name pb-ellipsis" });
    refs.dailyMeta = PB.el("div", { class: "pb-egress-meta pb-mono pb-ellipsis" });
    refs.aiPort = PB.el("div", { class: "pb-kicker" });
    refs.aiName = PB.el("div", { class: "pb-egress-name pb-ellipsis" });
    refs.aiMeta = PB.el("div", { class: "pb-egress-meta pb-mono pb-ellipsis" });
    refs.opt = PB.el("button", {
      class: "pb-btn pb-btn-primary pb-btn-sm",
      attrs: { id: "pb-optimize" },
      text: "一键优选",
      on: { click: optimize }
    });

    ["rule", "global", "direct"].forEach(function (id) {
      var label = { rule: "规则", global: "全局", direct: "直连" }[id];
      modes[id] = PB.el("button", {
        attrs: { id: "pb-mode-" + id, "aria-pressed": "false" },
        text: label,
        on: { click: function () { setMode(id); } }
      });
    });
    refs.sys = PB.el("button", {
      class: "pb-switch",
      attrs: { id: "pb-sys-switch", role: "switch", "aria-checked": "false", "aria-label": "系统代理" },
      on: { click: function () { setProxy(refs.sys.getAttribute("aria-checked") !== "true"); } }
    });
    refs.sysHint = PB.el("p", { class: "pb-hint", text: "内核停止时不能开关系统代理" });
    refs.bypass = PB.el("button", {
      class: "pb-switch",
      attrs: { id: "pb-bypass-switch", role: "switch", "aria-checked": "false", "aria-label": "绕过大陆" },
      on: { click: function () { setBypass(refs.bypass.getAttribute("aria-checked") !== "true", false); } }
    });
    refs.bypassHint = PB.el("p", { class: "pb-hint" });
    refs.geo = PB.el("p", { class: "pb-hint" });
    refs.retry = PB.el("button", {
      class: "pb-btn pb-btn-sm",
      attrs: { id: "pb-bypass-retry" },
      text: "重试",
      hidden: true,
      on: { click: function () { setBypass(true, true); } }
    });
    refs.subStat = PB.el("div", { class: "pb-stat-num", text: "读取中" });
    refs.aiStat = PB.el("div", { class: "pb-stat-num", text: "0 / 4 已启用" });

    var hero = PB.el("section", { class: "pb-hero" },
      PB.el("div", { class: "pb-hero-top" },
        PB.el("div", { class: "pb-hero-copy" },
          PB.el("div", { class: "pb-kicker", text: "内置内核" }),
          refs.title,
          refs.msg,
          refs.err,
          refs.barWrap
        ),
        PB.el("div", { class: "pb-hero-actions" }, refs.toggle, refs.importBtn)
      ),
      PB.el("div", { class: "pb-egress" },
        PB.el("div", { class: "pb-egress-card" }, refs.dailyPort, refs.dailyName, refs.dailyMeta),
        PB.el("div", { class: "pb-egress-card" },
          PB.el("div", { class: "pb-egress-top" }, refs.aiPort, refs.opt),
          refs.aiName,
          refs.aiMeta
        )
      )
    );

    var modeCard = PB.el("section", { class: "pb-card" },
      PB.el("h3", { text: "路由模式" }),
      PB.el("div", { class: "pb-seg", attrs: { role: "group", "aria-label": "路由模式" } }, modes.rule, modes.global, modes.direct)
    );
    var proxyCard = PB.el("section", { class: "pb-card" },
      PB.el("div", { class: "pb-row" },
        PB.el("h3", { text: "系统代理" }),
        refs.sys
      ),
      refs.sysHint
    );
    var bypassCard = PB.el("section", { class: "pb-card" },
      PB.el("div", { class: "pb-row" },
        PB.el("h3", { text: "绕过大陆" }),
        refs.bypass
      ),
      refs.geo,
      refs.bypassHint,
      refs.retry
    );
    var statCard = PB.el("section", { class: "pb-card" },
      PB.el("h3", { text: "统计" }),
      PB.el("div", { class: "pb-stat-line" },
        refs.subStat,
        PB.el("button", { class: "pb-linkish", text: "订阅管理", on: { click: function () { PB.go("subs"); } } })
      ),
      PB.el("div", { class: "pb-stat-line" },
        refs.aiStat,
        PB.el("button", { class: "pb-linkish", text: "AI 工具", on: { click: function () { PB.go("tools"); } } })
      ),
      PB.el("div", { class: "pb-stat-line" },
        PB.el("span", { class: "pb-muted", text: "节点在代理节点页切换" }),
        PB.el("button", { class: "pb-linkish", text: "代理节点", on: { click: function () { PB.go("nodes"); } } })
      )
    );

    root.appendChild(PB.el("div", { class: "pb-stack" },
      hero,
      PB.el("div", { class: "pb-grid-2" }, modeCard, proxyCard, bypassCard, statCard)
    ));
    paint();
  }

  function show() {
    paint();
    loadSubs();
    PB.api("GET", "/api/snapshot").then(PB.setSnapshot).catch(function (err) {
      PB.toast((err && err.message) || "读取状态失败", "err");
    });
    PB.refreshCore().then(loadEgress).catch(function (err) {
      PB.toast((err && err.message) || "读取内核状态失败", "err");
    });
  }

  PB.onPhase(paint);
  PB.on("core", paint);
  PB.on("subs", loadSubs);
  PB.on("sync", function () { if (running()) loadEgress(); });
  PB.on("speedtest", function () { if (running()) loadEgress(); });
  PB.addTab("overview", {
    mount: mount,
    show: show,
    hide: function () {},
    onCoreChange: function () { paint(); loadEgress(); },
    onSnapshot: paintStats
  });
})();
