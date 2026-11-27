(function () {
  "use strict";

  var listeners = { core: [], subs: [], sync: [], speedtest: [], update: [], open_update: [] };
  var phaseWatchers = [];
  var own = {};
  var mounted = {};
  var current = "";
  var toastTimer = 0;
  var es = null;
  var retryMs = 1000;
  var sawOpen = false;
  var dropped = false;
  var coreLock = false;
  var live = {
    phase: "stopped",
    message: "",
    downloaded: 0,
    total: 0,
    geoLive: "",
    geoPct: null,
    stopRequested: false
  };
  var versionText = "";
  var releasesUrl = "";
  var latestInfo = null;
  var updateChecking = false;
  var updateApplying = false;
  var updatePoll = 0;
  var restartToasted = false;
  var navPlaced = false;
  var navRO = null;

  var META = {
    overview: ["总览", "内核、双通道出口和常用开关"],
    nodes: ["代理节点", "日常代理与 AI 专属节点"],
    subs: ["订阅管理", "订阅来源、流量和更新"],
    tools: ["AI 工具", "仅对已开启开关的应用注入免 TUN 代理配置与内核专属分流规则，关闭即无损还原"],
    diag: ["连接诊断", "从所选通道检查本地端口、出口和四个 AI 服务"],
    settings: ["设置", "端口、测速地址、程序路径和偏好"]
  };

  var NAV = [
    ["overview", "总览", "M4 11.2 12 4l8 7.2V20a1 1 0 0 1-1 1h-5v-6H10v6H5a1 1 0 0 1-1-1z"],
    ["nodes", "代理节点", "M12 3.5 20 8v8l-8 4.5L4 16V8z M12 12.2 20 8 M12 12.2V20.5 M12 12.2 4 8"],
    ["subs", "订阅管理", "M4 7.5 12 4l8 3.5-8 3.5z M4 12l8 3.5L20 12 M4 16.5 12 20l8-3.5"],
    ["tools", "AI 工具", "M4 4h6.5v6.5H4z M13.5 4H20v6.5h-6.5z M4 13.5h6.5V20H4z M13.5 13.5H20V20h-6.5z"],
    ["diag", "连接诊断", "M3 12h4l2.5-6 4 12L16 12h5"],
    ["settings", "设置", "M4 7h16 M4 12h16 M4 17h16"]
  ];

  var MODE_LABEL = { rule: "规则", global: "全局", direct: "直连" };

  function el(tag, props) {
    var node = document.createElement(tag);
    var p = props || {};
    var children = Array.prototype.slice.call(arguments, 2);
    if (p.class) node.className = p.class;
    if (p.text != null) node.textContent = String(p.text);
    if (p.title) node.title = String(p.title);
    if (p.hidden) node.hidden = true;
    if (p.disabled) node.disabled = true;
    if (p.dataset) {
      Object.keys(p.dataset).forEach(function (key) {
        node.dataset[key] = String(p.dataset[key]);
      });
    }
    if (p.attrs) {
      Object.keys(p.attrs).forEach(function (key) {
        var value = p.attrs[key];
        if (value == null || value === false) return;
        node.setAttribute(key, String(value));
      });
    }
    if (p.on) {
      Object.keys(p.on).forEach(function (key) {
        if (typeof p.on[key] === "function") node.addEventListener(key, p.on[key]);
      });
    }
    if (tag === "button" && !node.hasAttribute("type")) node.type = "button";
    children.forEach(function (child) {
      if (child == null || child === false) return;
      if (typeof child === "string" || typeof child === "number") {
        node.appendChild(document.createTextNode(String(child)));
      } else if (child instanceof Node) {
        node.appendChild(child);
      }
    });
    return node;
  }

  function api(method, path, body) {
    var opts = { method: method, headers: { Accept: "application/json" } };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    return fetch(path, opts).then(function (res) {
      return res.text().then(function (text) {
        var data = null;
        if (text) {
          try { data = JSON.parse(text); } catch (e) { data = null; }
        }
        if (!res.ok) {
          var message = data && typeof data.error === "string" && data.error
            ? data.error
            : "请求失败 (" + res.status + ")";
          throw new Error(message);
        }
        return data == null ? {} : data;
      });
    }).catch(function (err) {
      if (err && err.name === "TypeError") throw new Error("网络请求失败");
      throw err;
    });
  }

  function toast(message, kind) {
    var node = document.getElementById("pb-toast");
    if (!node) return;
    node.textContent = message == null ? "" : String(message);
    node.className = "pb-toast" + (kind === "err" || kind === "warn" ? " pb-toast-" + kind : "");
    node.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { node.hidden = true; }, 3800);
  }

  function on(type, fn) {
    if (!listeners[type] || typeof fn !== "function") return;
    listeners[type].push(fn);
  }

  function emit(type, msg) {
    (listeners[type] || []).forEach(function (fn) { fn(msg); });
  }

  function emitPhase() {
    paintSide();
    phaseWatchers.forEach(function (fn) { fn(); });
  }

  function pad(n) { return String(n).padStart(2, "0"); }

  var fmt = {
    bytes: function (n) {
      var v = Number(n);
      if (!isFinite(v) || v < 0) return "0 B";
      if (v < 1024) return Math.round(v) + " B";
      var units = ["KB", "MB", "GB", "TB"];
      var x = v / 1024;
      var i = 0;
      while (x >= 1024 && i < units.length - 1) { x /= 1024; i++; }
      return x.toFixed(1) + " " + units[i];
    },
    date: function (unixSecOrIso) {
      var d;
      if (typeof unixSecOrIso === "number") d = new Date(unixSecOrIso * 1000);
      else if (typeof unixSecOrIso === "string" && /^\d+$/.test(unixSecOrIso)) d = new Date(Number(unixSecOrIso) * 1000);
      else d = new Date(unixSecOrIso);
      if (!d || isNaN(d.getTime())) return "";
      return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()) + " " + pad(d.getHours()) + ":" + pad(d.getMinutes());
    },
    ms: function (n) {
      var v = Number(n);
      if (!isFinite(v)) return "—";
      return Math.round(v) + " ms";
    },
    mbps: function (n) {
      var v = Number(n);
      if (!isFinite(v)) return "—";
      return v.toFixed(2) + " MB/s";
    }
  };

  function num(v) {
    var n = Number(v);
    return isFinite(n) ? n : 0;
  }

  function ports() {
    var c = PB.state.core || {};
    return {
      mixed: num(c.mixedPort) || 27890,
      controller: num(c.controllerPort) || 27891,
      ai: num(c.aiDedicatedPort) || 27892,
      speed: num(c.speedTestPort) || 27893
    };
  }

  function userEnabled(target) {
    if (target && typeof target.enabledByUser === "boolean") return target.enabledByUser;
    var snap = PB.state.snapshot;
    var apps = snap && snap.appConfig && snap.appConfig.managedApps;
    return !!(apps && target && apps[target.id]);
  }

  function setDot(id, kind) {
    var dot = document.querySelector('[data-dot="' + id + '"]');
    if (!dot) return;
    dot.className = "pb-dot" + (kind ? " pb-dot-" + kind : "");
    dot.hidden = !kind;
  }

  function paintToolsDot() {
    var snap = PB.state.snapshot;
    var targets = snap && snap.targets || [];
    var onCount = 0;
    targets.forEach(function (t) { if (userEnabled(t)) onCount += 1; });
    setDot("tools", onCount > 0 ? "ok" : "");
  }

  function paintSide() {
    var core = PB.state.core;
    var phase = live.phase;
    var light = document.getElementById("pb-core-light");
    var word = document.getElementById("pb-core-word");
    var modeWord = document.getElementById("pb-mode-word");
    var badge = document.getElementById("pb-sys-badge");
    var mode = core && core.mode ? core.mode : "rule";
    if (modeWord) modeWord.textContent = MODE_LABEL[mode] || mode;
    var proxyOn = !!(core && core.systemProxyActive);
    if (badge) {
      badge.textContent = proxyOn ? "系统代理 开" : "系统代理 关";
      badge.className = "pb-badge " + (proxyOn ? "pb-badge-ok" : "pb-badge-muted");
    }
    var phaseWord = { download: "下载中", running: "运行中", error: "启动失败", stopped: "已停止" };
    if (word) word.textContent = phaseWord[phase] || "已停止";
    var lightKind = phase === "running" ? "ok" : phase === "error" ? "err" : phase === "download" ? "warn" : "off";
    if (light) light.className = "pb-light pb-light-" + lightKind;
    setDot("overview", phase === "running" ? "ok" : phase === "error" ? "err" : phase === "download" ? "warn" : "");
    setDot("nodes", phase === "running" ? "ok" : "");
  }

  function notePhase(phase, message, downloaded, total) {
    live.phase = phase;
    if (message != null) live.message = message;
    if (phase === "download") {
      live.downloaded = num(downloaded);
      live.total = num(total);
    } else {
      live.downloaded = 0;
      live.total = 0;
    }
    emitPhase();
  }

  function eachTab(fn) {
    var seen = {};
    Object.keys(own).concat(Object.keys(PB.tabs)).forEach(function (id) {
      if (seen[id]) return;
      seen[id] = true;
      var tab = own[id] || PB.tabs[id];
      if (tab) fn(tab);
    });
  }

  function publishCore(status) {
    PB.state.core = status || null;
    if (status && status.running) {
      live.phase = "running";
      live.message = "";
      live.downloaded = 0;
      live.total = 0;
    } else if (live.phase !== "download" && live.phase !== "error") {
      live.phase = "stopped";
    }
    if (status && !status.geoDownloading && status.geoReady) {
      live.geoLive = "";
      live.geoPct = null;
    }
    paintSide();
    eachTab(function (tab) {
      if (typeof tab.onCoreChange === "function") tab.onCoreChange(status);
    });
  }

  function refreshCore() {
    return api("GET", "/api/core/status").then(function (data) {
      publishCore(data);
      return data;
    });
  }

  function setSnapshot(snap) {
    PB.state.snapshot = snap || null;
    paintToolsDot();
    Object.keys(own).forEach(function (id) {
      if (typeof own[id].onSnapshot === "function") own[id].onSnapshot(snap);
    });
  }

  function pullSnapshot() {
    return api("GET", "/api/snapshot").then(function (snap) {
      setSnapshot(snap);
      return snap;
    });
  }

  function tabOf(id) { return own[id] || PB.tabs[id] || null; }

  function setActions(nodes) {
    var slot = document.getElementById("pb-actions");
    if (!slot) return;
    slot.replaceChildren();
    (nodes || []).forEach(function (node) {
      if (node) slot.appendChild(node);
    });
  }

  function go(id) {
    if (!META[id]) return;
    if (current && current !== id) {
      var prev = tabOf(current);
      if (prev && mounted[current] && typeof prev.hide === "function") prev.hide();
    }
    current = id;
    Object.keys(META).forEach(function (key) {
      var sec = document.getElementById("tab-" + key);
      if (sec) sec.hidden = key !== id;
      var btn = document.querySelector('.pb-nav-btn[data-tab="' + key + '"]');
      if (btn) btn.setAttribute("aria-current", key === id ? "page" : "false");
    });
    document.getElementById("pb-title").textContent = META[id][0];
    document.getElementById("pb-sub").textContent = META[id][1];
    setActions([]);
    var sec = document.getElementById("tab-" + id);
    var tab = tabOf(id);
    if (!tab) {
      sec.replaceChildren(el("p", { class: "pb-empty", text: "这个页面由并行模块提供，合并后显示。" }));
      mounted[id] = false;
    } else {
      if (!mounted[id]) {
        sec.replaceChildren();
        tab.mount(sec);
        mounted[id] = true;
      }
      if (typeof tab.show === "function") tab.show();
    }
    var content = document.getElementById("pb-content");
    if (content) content.scrollTop = 0;
    if (sec) sec.scrollTop = 0;
    if (sec) {
      sec.classList.remove("is-enter");
      void sec.offsetWidth;
      sec.classList.add("is-enter");
    }
    placeNav(navPlaced);
    navPlaced = true;
  }

  function icon(d) {
    var svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("width", "16");
    svg.setAttribute("height", "16");
    svg.setAttribute("aria-hidden", "true");
    var path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", d);
    path.setAttribute("fill", "none");
    path.setAttribute("stroke", "currentColor");
    path.setAttribute("stroke-width", "1.8");
    path.setAttribute("stroke-linejoin", "round");
    path.setAttribute("stroke-linecap", "round");
    svg.appendChild(path);
    return svg;
  }

  function navThumbEl() {
    var nav = document.getElementById("pb-nav");
    if (!nav) return null;
    var thumb = nav.querySelector(":scope > .pb-nav-thumb");
    if (!thumb) {
      thumb = document.createElement("span");
      thumb.className = "pb-nav-thumb";
      thumb.setAttribute("aria-hidden", "true");
      nav.insertBefore(thumb, nav.firstChild);
    }
    return thumb;
  }

  function placeNav(animate) {
    var nav = document.getElementById("pb-nav");
    if (!nav) return;
    var btn = nav.querySelector('.pb-nav-btn[aria-current="page"]');
    var thumb = navThumbEl();
    if (!btn || !thumb || btn.offsetWidth <= 0) return;
    var nextW = btn.offsetWidth + "px";
    var nextH = btn.offsetHeight + "px";
    var nextT = "translate(" + btn.offsetLeft + "px, " + btn.offsetTop + "px)";
    if (thumb.style.width === nextW && thumb.style.height === nextH && thumb.style.transform === nextT) return;
    if (!animate) thumb.style.transition = "none";
    thumb.style.width = nextW;
    thumb.style.height = nextH;
    thumb.style.transform = nextT;
    if (!animate) {
      void thumb.offsetWidth;
      thumb.style.transition = "";
    }
  }

  function buildNav() {
    var nav = document.getElementById("pb-nav");
    nav.replaceChildren();
    navThumbEl();
    NAV.forEach(function (item) {
      var dot = el("span", { class: "pb-dot", attrs: { "data-dot": item[0] }, hidden: true });
      var btn = el("button", {
        class: "pb-nav-btn",
        attrs: { "data-tab": item[0], "aria-current": "false" },
        on: { click: function () { go(item[0]); } }
      }, icon(item[2]), el("span", { text: item[1] }), dot);
      nav.appendChild(btn);
    });
    if (!navRO && window.ResizeObserver) {
      navRO = new ResizeObserver(function () { placeNav(navPlaced); });
      navRO.observe(nav);
    }
  }

  function applyCoreEvent(core) {
    var phase = core && core.phase;
    if (phase === "download") {
      live.phase = "download";
      live.message = core.message || "正在下载内核";
      live.downloaded = num(core.downloaded);
      live.total = num(core.total);
      emitPhase();
      return;
    }
    if (phase === "running") {
      live.phase = "running";
      live.message = core.message || "内核已启动";
      live.downloaded = 0;
      live.total = 0;
      emitPhase();
      refreshCore().catch(function () {});
      return;
    }
    if (phase === "stopped") {
      live.phase = "stopped";
      live.message = core.message || "内核已停止";
      live.downloaded = 0;
      live.total = 0;
      emitPhase();
      refreshCore().catch(function () {});
      return;
    }
    if (phase === "error") {
      live.phase = "error";
      live.message = core.message || "启动失败";
      live.downloaded = 0;
      live.total = 0;
      emitPhase();
      return;
    }
    if (phase === "geo") {
      live.geoLive = core.message || "";
      live.geoPct = num(core.total) > 0 ? Math.max(0, Math.min(100, Math.round((num(core.downloaded) / num(core.total)) * 100))) : null;
      emitPhase();
    }
  }

  function dispatch(msg) {
    if (!msg || typeof msg !== "object") return;
    if (msg.type === "core" && msg.core) applyCoreEvent(msg.core);
    if (msg.type === "sync") {
      if (msg.snapshot) setSnapshot(msg.snapshot);
      if (msg.reason) toast(msg.reason, "ok");
    }
    if (msg.type === "update" && msg.update) renderUpdateProgress(msg.update);
    if (msg.type === "open_update") openUpdate();
    if (listeners[msg.type]) emit(msg.type, msg);
  }

  function connectEvents() {
    if (es) es.close();
    es = new EventSource("/api/events");
    es.onopen = function () {
      var again = dropped;
      dropped = false;
      sawOpen = true;
      retryMs = 1000;
      if (again) {
        refreshCore().catch(function () {});
        pullSnapshot().catch(function () {});
      }
    };
    es.onmessage = function (ev) {
      try { dispatch(JSON.parse(ev.data)); } catch (e) { /* ignore a bad frame */ }
    };
    es.onerror = function () {
      dropped = true;
      if (es && es.readyState === EventSource.CLOSED) {
        var wait = retryMs;
        retryMs = Math.min(retryMs * 2, 15000);
        setTimeout(connectEvents, wait);
      }
    };
  }

  function startCore() {
    if (coreLock) return Promise.resolve(null);
    coreLock = true;
    live.stopRequested = false;
    notePhase("download", "正在启动", 0, 0);
    return api("POST", "/api/core/start", {}).then(function (data) {
      if (data && typeof data.running === "boolean") publishCore(data);
      else return refreshCore();
    }).then(function () {
      return pullSnapshot().catch(function () { return null; });
    }).catch(function (err) {
      var canceled = live.stopRequested || String(err && err.message || "").indexOf("已取消") >= 0;
      if (canceled) notePhase("stopped", (err && err.message) || "内核已停止");
      else {
        notePhase("error", (err && err.message) || "启动失败");
        toast((err && err.message) || "启动失败", "err");
      }
      return null;
    }).finally(function () { coreLock = false; });
  }

  function stopCore() {
    live.stopRequested = true;
    return api("POST", "/api/core/stop", {}).then(function () {
      notePhase("stopped", "内核已停止");
      return refreshCore().catch(function () { return null; });
    }).then(function () {
      return pullSnapshot().catch(function () { return null; });
    }).catch(function (err) {
      toast((err && err.message) || "停止失败", "err");
      return null;
    });
  }

  function importCore() {
    return api("POST", "/api/browse-core", {}).then(function (picked) {
      if (!picked || !picked.path) {
        toast("未选择文件", "warn");
        return null;
      }
      return api("POST", "/api/core/import-binary", { path: picked.path }).then(function () { return true; });
    }).then(function (ok) {
      if (!ok) return null;
      toast("已导入内核，正在启动");
      return startCore();
    }).catch(function (err) {
      notePhase("error", (err && err.message) || "导入失败");
      toast((err && err.message) || "导入失败", "err");
      return null;
    });
  }

  function openHome() {
    return api("POST", "/api/open-backups", {}).then(function () {
      toast("已打开 ~/.proxy-box");
    }).catch(function (err) {
      toast((err && err.message) || "打开失败", "err");
    });
  }

  function $(id) { return document.getElementById(id); }

  function paintVersion() {
    var text = $("pb-version-text");
    var dot = $("pb-version-dot");
    var btn = $("pb-version");
    if (text) text.textContent = versionText || "…";
    var has = !!(latestInfo && latestInfo.hasUpdate);
    if (dot) dot.hidden = !has;
    if (btn) {
      btn.setAttribute("aria-label", has
        ? "版本 " + (versionText || "") + "，有可用更新"
        : "版本 " + (versionText || "") + "，检查更新");
      if (has && latestInfo.latestVersion) btn.title = "新版本 " + latestInfo.latestVersion;
      else btn.removeAttribute("title");
    }
  }

  function renderUpdateInfo(info, errMsg) {
    var caption = $("pb-update-caption");
    var flow = $("pb-update-flow");
    var pill = $("pb-update-pill");
    var meta = $("pb-update-meta");
    var notes = $("pb-update-notes");
    var apply = $("pb-update-apply");
    flow.replaceChildren();
    if (errMsg) {
      caption.textContent = "检查更新失败，请确认网络后重试";
      flow.textContent = versionText ? "当前版本 " + versionText : "";
      pill.className = "pb-badge pb-badge-warn";
      pill.textContent = "检查失败";
      meta.textContent = "";
      notes.textContent = errMsg;
      apply.hidden = true;
      return;
    }
    if (!info) return;
    var parts = [];
    if (info.publishedAt) parts.push("发布时间 " + (fmt.date(info.publishedAt) || info.publishedAt));
    if (info.assetName) parts.push("安装包 " + info.assetName + (info.assetSize > 0 ? " (" + fmt.bytes(info.assetSize) + ")" : ""));
    if (info.checkedAt) parts.push("检查于 " + info.checkedAt);
    meta.textContent = parts.join(" · ");
    notes.textContent = info.releaseNotes || ((info.releaseName || info.latestVersion || "") + "\n暂无更新日志。");
    if (info.hasUpdate) {
      caption.textContent = "发现新版本" + (info.releaseName ? "：" + info.releaseName : "");
      flow.appendChild(el("span", { text: info.currentVersion || versionText || "" }));
      flow.appendChild(document.createTextNode(" → "));
      flow.appendChild(el("span", { class: "pb-em", text: info.latestVersion || "" }));
      pill.className = "pb-badge pb-badge-ok";
      pill.textContent = "可更新";
      apply.hidden = !info.downloadUrl;
      apply.disabled = updateApplying;
    } else {
      caption.textContent = "当前已是最新版本";
      flow.textContent = (info.currentVersion || versionText || "") + (info.latestVersion ? "（最新 " + info.latestVersion + "）" : "");
      pill.className = "pb-badge pb-badge-ok";
      pill.textContent = "已是最新";
      apply.hidden = true;
    }
  }

  function renderUpdateProgress(prog) {
    if (!prog || prog.status === "idle") return;
    var wrap = $("pb-update-progress");
    var bar = $("pb-update-bar");
    var msg = $("pb-update-msg");
    var bytes = $("pb-update-bytes");
    var apply = $("pb-update-apply");
    var recheck = $("pb-update-recheck");
    wrap.hidden = false;
    if (prog.status === "error") {
      updateApplying = false;
      msg.className = "pb-error";
      msg.textContent = "更新失败: " + (prog.error || prog.message || "未知错误");
      bytes.textContent = "";
      bar.style.width = "0";
      wrap.classList.remove("is-busy");
      if (apply) {
        apply.hidden = false;
        apply.disabled = false;
        apply.textContent = "重试更新";
      }
      if (recheck) recheck.disabled = false;
      clearInterval(updatePoll);
      return;
    }
    msg.className = "pb-muted";
    msg.textContent = prog.message || "正在下载更新";
    var pct = Math.max(0, Math.min(100, Number(prog.percent) || 0));
    if (prog.totalBytes > 0) {
      wrap.classList.remove("is-busy");
      bar.style.width = pct.toFixed(1) + "%";
      bytes.textContent = fmt.bytes(prog.downloadedBytes) + " / " + fmt.bytes(prog.totalBytes);
    } else if ((Number(prog.percent) || 0) > 0) {
      wrap.classList.remove("is-busy");
      bar.style.width = pct.toFixed(1) + "%";
      bytes.textContent = prog.downloadedBytes > 0 ? fmt.bytes(prog.downloadedBytes) : "";
    } else {
      wrap.classList.add("is-busy");
      bar.style.width = "";
      bytes.textContent = "";
    }
    if (prog.status === "restarting") {
      wrap.classList.remove("is-busy");
      bar.style.width = "100%";
      if (apply) apply.textContent = "正在重启";
      if (!restartToasted) {
        restartToasted = true;
        toast(prog.message || "更新完成，正在重启 Proxy Box");
      }
      clearInterval(updatePoll);
    }
  }

  function checkUpdate(silent) {
    if (updateChecking) return;
    updateChecking = true;
    var recheck = $("pb-update-recheck");
    if (recheck) {
      recheck.disabled = true;
      recheck.textContent = "检查中";
    }
    if (!silent) {
      $("pb-update-caption").textContent = "正在检查最新版本";
      $("pb-update-pill").className = "pb-badge pb-badge-muted";
      $("pb-update-pill").textContent = "检查中";
    }
    api("POST", "/api/update/check", {}).then(function (info) {
      latestInfo = info;
      if (info.currentVersion) versionText = info.currentVersion;
      paintVersion();
      renderUpdateInfo(info, "");
      if (silent && info.hasUpdate) toast("发现新版本 " + (info.latestVersion || "") + "，点击版本号可更新");
    }).catch(function (err) {
      if (!silent) renderUpdateInfo(null, (err && err.message) || "检查失败");
    }).finally(function () {
      updateChecking = false;
      if (recheck) {
        recheck.disabled = updateApplying;
        recheck.textContent = "重新检查";
      }
    });
  }

  function applyUpdate() {
    if (!latestInfo || !latestInfo.downloadUrl || updateApplying) return;
    updateApplying = true;
    restartToasted = false;
    var apply = $("pb-update-apply");
    var recheck = $("pb-update-recheck");
    apply.disabled = true;
    apply.textContent = "正在更新";
    if (recheck) recheck.disabled = true;
    renderUpdateProgress({
      status: "downloading",
      percent: 2,
      downloadedBytes: 0,
      totalBytes: latestInfo.assetSize || 0,
      message: "正在开始下载 " + (latestInfo.latestVersion || "")
    });
    api("POST", "/api/update/apply", {
      downloadUrl: latestInfo.downloadUrl,
      targetVersion: latestInfo.latestVersion,
      assetSize: latestInfo.assetSize || 0
    }).then(function () {
      clearInterval(updatePoll);
      updatePoll = setInterval(function () {
        fetch("/api/update/status").then(function (res) { return res.json(); }).then(function (prog) {
          renderUpdateProgress(prog);
        }).catch(function () { clearInterval(updatePoll); });
      }, 450);
    }).catch(function (err) {
      updateApplying = false;
      renderUpdateProgress({ status: "error", message: "启动更新失败", error: (err && err.message) || "启动更新失败" });
      toast((err && err.message) || "启动更新失败", "err");
    });
  }

  function openUpdate() {
    $("pb-update").hidden = false;
    if (latestInfo) renderUpdateInfo(latestInfo, "");
    if (!updateApplying && !updateChecking) checkUpdate(false);
  }

  function closeUpdate() {
    if (updateApplying) {
      toast("正在下载并应用更新，请稍候", "warn");
      return;
    }
    $("pb-update").hidden = true;
  }

  function openRelease() {
    var url = (latestInfo && latestInfo.releaseUrl) || releasesUrl;
    if (!url) return;
    api("POST", "/api/open-url", { url: url }).catch(function (err) {
      toast((err && err.message) || "打开链接失败", "err");
    });
  }

  function loadVersion() {
    api("GET", "/api/version").then(function (data) {
      if (data.version) versionText = data.version;
      if (data.releasesUrl) releasesUrl = data.releasesUrl;
      paintVersion();
    }).catch(function () {});
  }

  function bindChrome() {
    buildNav();
    $("pb-version").addEventListener("click", openUpdate);
    $("pb-open-home").addEventListener("click", openHome);
    $("pb-update-close").addEventListener("click", closeUpdate);
    $("pb-update-backdrop").addEventListener("click", closeUpdate);
    $("pb-update-recheck").addEventListener("click", function () { checkUpdate(false); });
    $("pb-update-apply").addEventListener("click", applyUpdate);
    $("pb-update-releases").addEventListener("click", openRelease);
    document.addEventListener("keydown", function (ev) {
      if (ev.key === "Escape") closeUpdate();
    });
  }

  function segShown(root) {
    return !!(root && root.isConnected && root.offsetWidth > 0 && root.offsetHeight > 0);
  }

  function segThumb(root) {
    var thumb = root.querySelector(":scope > .pb-seg-thumb");
    if (!thumb) {
      thumb = document.createElement("span");
      thumb.className = "pb-seg-thumb";
      thumb.setAttribute("aria-hidden", "true");
      root.insertBefore(thumb, root.firstChild);
    }
    return thumb;
  }

  function segTarget(root) {
    return root.querySelector(":scope > .pb-seg-btn.is-on") || root.querySelector(":scope > .is-on");
  }

  function segPlace(root, animate) {
    var on = segTarget(root);
    var thumb = segThumb(root);
    if (!on || !segShown(root) || on.offsetWidth <= 0) return false;
    var nextW = on.offsetWidth + "px";
    var nextT = "translateX(" + on.offsetLeft + "px)";
    if (thumb.style.width === nextW && thumb.style.transform === nextT) {
      root.__pbSegPlaced = true;
      return true;
    }
    if (!animate) thumb.style.transition = "none";
    thumb.style.width = nextW;
    thumb.style.transform = nextT;
    if (!animate) {
      void thumb.offsetWidth;
      thumb.style.transition = "";
    }
    root.__pbSegPlaced = true;
    return true;
  }

  function segClearWait(root) {
    if (root.__pbSegWait) {
      root.__pbSegWait.disconnect();
      root.__pbSegWait = null;
    }
  }

  function segWait(root) {
    if (root.__pbSegWait) return;
    var mo = new MutationObserver(function () {
      if (!segShown(root)) return;
      segClearWait(root);
      segSync(root);
    });
    var node = root;
    while (node) {
      mo.observe(node, { attributes: true, attributeFilter: ["hidden", "class", "style"] });
      node = node.parentElement;
    }
    root.__pbSegWait = mo;
  }

  function segWatch(root) {
    if (root.__pbSegRO || !window.ResizeObserver) return;
    root.__pbSegRO = new ResizeObserver(function () {
      if (!segShown(root)) return;
      if (!root.__pbSegSeen) {
        segPlace(root, false);
        root.__pbSegSeen = true;
        segClearWait(root);
        return;
      }
      segPlace(root, true);
    });
    root.__pbSegRO.observe(root);
  }

  function segSync(root) {
    if (!root) return;
    segWatch(root);
    segThumb(root);
    if (!segShown(root)) {
      segWait(root);
      return;
    }
    segPlace(root, !!root.__pbSegPlaced);
    root.__pbSegSeen = true;
    segClearWait(root);
  }

  function seg(root) {
    if (!root) return { sync: function () {} };
    segWatch(root);
    segThumb(root);
    if (!root.__pbSegPlaced) {
      if (segPlace(root, false)) root.__pbSegSeen = true;
      else segWait(root);
    }
    return { sync: function () { segSync(root); } };
  }

  seg.sync = segSync;

  var PB = {
    api: api,
    toast: toast,
    el: el,
    on: on,
    state: { core: null, snapshot: null },
    refreshCore: refreshCore,
    registerTab: function (id, tab) {
      if ((id !== "nodes" && id !== "subs") || !tab || typeof tab.mount !== "function") return;
      PB.tabs[id] = tab;
    },
    go: go,
    seg: seg,
    fmt: fmt,
    tabs: {},
    setActions: setActions,
    setSnapshot: setSnapshot,
    ports: ports,
    userEnabled: userEnabled,
    setDot: setDot,
    phaseView: function () {
      return {
        phase: live.phase,
        message: live.message,
        downloaded: live.downloaded,
        total: live.total,
        geoLive: live.geoLive,
        geoPct: live.geoPct
      };
    },
    onPhase: function (fn) { if (typeof fn === "function") phaseWatchers.push(fn); },
    notePhase: notePhase,
    startCore: startCore,
    stopCore: stopCore,
    paintChrome: paintSide,
    importCore: importCore,
    openHome: openHome,
    addTab: function (id, tab) { if (tab && typeof tab.mount === "function") own[id] = tab; }
  };

  window.PB = PB;

  document.addEventListener("DOMContentLoaded", function () {
    bindChrome();
    connectEvents();
    go("overview");
    requestAnimationFrame(function () { placeNav(false); });
    loadVersion();
    setTimeout(function () { checkUpdate(true); }, 2500);
  });
})();
