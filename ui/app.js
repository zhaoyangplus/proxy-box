/* Proxy Box V2.2 shell. ui/util.js loads first.
   Page scripts call PB.registerTab before DOMContentLoaded.
   The scratch contract UI_V22_CONTRACT.md mirrors this list.

   PB.api(method, path, body) -> Promise<object>
   PB.esc(value) -> string
   PB.ic(name, cls?) -> string
   PB.sw(on, attrs?, disabled?, cls?) -> string
   PB.el(tag, props, ...children) -> Element
   PB.copyText(text, msg?) -> void
   PB.fmt.ago(ts, now?) / bytes(n) / ms(n) / mbps(n) / uptime(ms) / daysLeft(ts, now?) / date(unixOrIso)
   PB.maskUrl(url) -> string
   PB.on(type, fn) -> void
   PB.onInput(el, fn, delay?) -> function
   PB.toast(msg, opt?) / PB.hideToast()
   PB.openMenu(anchor, cfg) / PB.closeMenu(instant?)
   PB.openSheet(cfg) / PB.closeSheet() / PB.confirmSheet(cfg)
   PB.segHTML(id, opts, active, cls?) -> string
   PB.mountSeg(segEl, cb?) / PB.segSet(segEl, value)
   PB.registerTab(id, tab) / PB.goTab(id, opt?) / PB.go(id)
   PB.current() -> string
   PB.setBadge(key, text, cls?) / PB.setChannel("daily"|"ai")
   PB.topbar(cfg) / PB.setActions(value) / PB.setAcc(value)
   PB.theme.get() / PB.theme.set(pref) / PB.theme.resolved()
   PB.refreshCore() / PB.refreshSnapshot() -> Promise
   PB.state is one object: core, snapshot, version, releasesUrl, update, phase
   PB.openUpdate is assigned later by update.js. The shell calls it when present.
*/
(function () {
  "use strict";

  var U = window.PBUtil;
  if (!U) throw new Error("ui/util.js must load before app.js");

  var TAB_ORDER = ["overview", "nodes", "subs", "tools", "diag", "settings"];
  var TAB_META = {
    overview: ["总览", "内核状态、双通道出口与常用开关"],
    nodes: ["代理节点", "日常代理与 AI 专属通道各自独立筛选、测速与切换"],
    subs: ["订阅管理", "导入订阅、自动更新周期与流量用量"],
    tools: ["AI 工具", "让 AI IDE 与 CLI 固定走 AI 专属通道"],
    diag: ["连接诊断", "按通道检测本地端口、出口地区与 AI 服务连通性"],
    settings: ["设置", "外观、端口分配、测速参数与通用偏好"]
  };
  var MODE_LABEL = { rule: "规则分流", global: "全局代理", direct: "全部直连" };
  var TOAST_ICON = { ok: "ok-circle", warn: "warn", err: "x-circle", info: "info" };

  var tabs = {};
  var listeners = {};
  var current = "";
  var state = {
    core: null,
    snapshot: null,
    version: "",
    releasesUrl: "",
    update: null,
    phase: "stopped"
  };
  var es = null;
  var retryMs = 1000;
  var dropped = false;
  var toastTimer = 0;
  var menu = null;
  var popTimer = 0;
  var sheetTimer = 0;
  var sheetReturn = null;
  var segRO = null;
  var themeMQ = window.matchMedia("(prefers-color-scheme: dark)");

  function $(id) {
    return document.getElementById(id);
  }

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
      if (typeof child === "string" || typeof child === "number") node.appendChild(document.createTextNode(String(child)));
      else if (child instanceof Node) node.appendChild(child);
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
          try { data = JSON.parse(text); } catch (err) { data = null; }
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

  function on(type, fn) {
    if (!type || typeof fn !== "function") return;
    if (!listeners[type]) listeners[type] = [];
    listeners[type].push(fn);
  }

  function emit(type, msg) {
    (listeners[type] || []).forEach(function (fn) { fn(msg); });
  }

  function eachTab(method, arg) {
    TAB_ORDER.forEach(function (id) {
      var tab = tabs[id];
      if (tab && typeof tab[method] === "function") tab[method](arg);
    });
  }

  function toast(msg, opt) {
    if (typeof opt === "string") opt = { tone: opt };
    opt = opt || {};
    var tone = TOAST_ICON[opt.tone] ? opt.tone : "ok";
    var node = $("toast");
    if (!node) return;
    var action = opt.action;
    node.className = "toast glass tone-" + tone;
    node.innerHTML = '<span class="t-ic">' + U.ic(TOAST_ICON[tone]) + '</span><span class="t-msg">' + U.esc(msg) + "</span>" +
      (action && action.label ? '<button type="button" class="t-act">' + U.esc(action.label) + "</button>" : "");
    if (action && action.label) {
      node.querySelector(".t-act").addEventListener("click", function () {
        if (typeof action.fn === "function") action.fn();
        hideToast();
      });
    }
    node.hidden = false;
    void node.offsetWidth;
    node.classList.add("show");
    clearTimeout(toastTimer);
    var wait = action && action.label ? 5200 : (opt.ms == null ? 2600 : Number(opt.ms));
    if (!isFinite(wait) || wait < 0) wait = 2600;
    toastTimer = setTimeout(hideToast, wait);
  }

  function hideToast() {
    var node = $("toast");
    if (!node) return;
    node.classList.remove("show");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { node.hidden = true; }, 320);
  }

  function copyText(text, msg) {
    var value = text == null ? "" : String(text);
    var done = function () { toast(msg || "已复制到剪贴板"); };
    var fail = function () { toast("复制失败", { tone: "err" }); };
    var fallback = function () {
      var area = document.createElement("textarea");
      area.value = value;
      area.setAttribute("readonly", "");
      area.style.position = "fixed";
      area.style.left = "-9999px";
      document.body.appendChild(area);
      area.select();
      var ok = false;
      try { ok = document.execCommand("copy"); } catch (err) { ok = false; }
      area.remove();
      if (ok) done();
      else fail();
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(value).then(done, fallback);
      return;
    }
    fallback();
  }

  function onInput(input, fn, delay) {
    if (!input || typeof fn !== "function") return function () {};
    var wait = delay == null ? 0 : Number(delay);
    if (!isFinite(wait) || wait < 0) wait = 0;
    var composing = false;
    var skipInput = false;
    var timer = 0;
    function fire() {
      clearTimeout(timer);
      var run = function () { fn(input.value, input); };
      if (wait > 0) timer = setTimeout(run, wait);
      else run();
    }
    function start() { composing = true; }
    function end() {
      composing = false;
      skipInput = true;
      setTimeout(function () { skipInput = false; }, 0);
      fire();
    }
    function typed() {
      if (composing || skipInput) return;
      fire();
    }
    input.addEventListener("compositionstart", start);
    input.addEventListener("compositionend", end);
    input.addEventListener("input", typed);
    return function () {
      clearTimeout(timer);
      input.removeEventListener("compositionstart", start);
      input.removeEventListener("compositionend", end);
      input.removeEventListener("input", typed);
    };
  }

  function directChild(root, pred) {
    if (!root) return null;
    var nodes = root.children;
    for (var i = 0; i < nodes.length; i++) {
      if (pred(nodes[i])) return nodes[i];
    }
    return null;
  }

  function placeThumb(seg, animate) {
    if (!seg) return;
    var thumb = directChild(seg, function (node) { return node.classList.contains("seg-thumb"); });
    var active = directChild(seg, function (node) {
      return node.classList.contains("seg-btn") && node.classList.contains("is-active");
    });
    if (!thumb) return;
    var box = U.segThumbPlacement(active ? active.offsetWidth : 0, active ? active.offsetLeft : 0, !!seg._pbPlaced, !!animate);
    if (!box.visible) {
      thumb.style.opacity = "0";
      return;
    }
    thumb.style.opacity = "";
    if (box.instant) thumb.style.transition = "none";
    thumb.style.width = box.width;
    thumb.style.setProperty("--x", box.x);
    if (box.instant) {
      void thumb.offsetWidth;
      thumb.style.transition = "";
    }
    seg._pbPlaced = true;
  }

  function segSet(seg, value) {
    if (!seg) return;
    var wanted = String(value);
    Array.prototype.forEach.call(seg.querySelectorAll(".seg-btn"), function (btn) {
      var on = btn.getAttribute("data-v") === wanted;
      btn.classList.toggle("is-active", on);
      btn.setAttribute("aria-checked", on ? "true" : "false");
    });
    placeThumb(seg, true);
  }

  function mountSeg(seg, cb) {
    if (!seg) return;
    if (!seg._pbSegOn) {
      seg._pbSegOn = true;
      seg.addEventListener("click", function (event) {
        var btn = event.target.closest ? event.target.closest(".seg-btn") : null;
        if (!btn || btn.parentElement !== seg || btn.disabled || btn.classList.contains("is-active")) return;
        var value = btn.getAttribute("data-v");
        segSet(seg, value);
        if (typeof cb === "function") cb(value, seg);
      });
      if (window.ResizeObserver) {
        if (!segRO) {
          segRO = new ResizeObserver(function (entries) {
            entries.forEach(function (entry) { placeThumb(entry.target, !!entry.target._pbPlaced); });
          });
        }
        segRO.observe(seg);
      }
    }
    placeThumb(seg, false);
  }

  function closeMenu(instant) {
    if (!menu) return;
    if (menu.anchor) menu.anchor.setAttribute("aria-expanded", "false");
    menu = null;
    var pop = $("pop");
    if (!pop) return;
    pop.classList.remove("open");
    clearTimeout(popTimer);
    if (instant) pop.hidden = true;
    else popTimer = setTimeout(function () { pop.hidden = true; }, 180);
  }

  function openMenu(anchor, cfg) {
    cfg = cfg || {};
    if (!anchor) return;
    if (menu && menu.anchor === anchor) {
      closeMenu();
      return;
    }
    closeMenu(true);
    var pop = $("pop");
    if (!pop) return;
    var items = cfg.items || [];
    var value = cfg.value;
    var custom = cfg.custom;
    var isCustom = !!(custom && typeof value === "number" && !items.some(function (item) { return item.v === value; }));
    var main = $("main");
    if (main) pop.setAttribute("data-ch", main.getAttribute("data-ch") || "daily");
    var html = '<div class="menu" role="menu">';
    if (cfg.title) html += '<div class="menu-title">' + U.esc(cfg.title) + "</div>";
    items.forEach(function (item, index) {
      if (item.sep) {
        html += '<div class="menu-sep"></div>';
        return;
      }
      var checked = item.v === value;
      html += '<button type="button" class="menu-item" role="menuitemradio" aria-checked="' + (checked ? "true" : "false") + '" data-i="' + index + '"' + (item.disabled ? " disabled" : "") + ">";
      html += '<span class="menu-check">' + (checked ? U.ic("check") : "") + "</span><span class=\"menu-lbl\">" + U.esc(item.label) + "</span>";
      if (item.hint != null) html += '<span class="menu-hint">' + U.esc(item.hint) + "</span>";
      html += "</button>";
    });
    if (custom) {
      var shown = isCustom ? value : custom.def;
      html += '<div class="menu-sep"></div><form class="menu-custom" id="menuCustom"><span class="menu-check">' + (isCustom ? U.ic("check") : "") + "</span>";
      html += '<span class="menu-lbl">' + U.esc(custom.label) + '</span><input class="field sm" type="number" min="' + U.esc(custom.min) + '" max="' + U.esc(custom.max) + '" step="' + U.esc(custom.step) + '" value="' + U.esc(shown) + '" aria-label="' + U.esc(custom.label) + '">';
      html += '<span class="caption">' + U.esc(custom.unit) + '</span><button type="submit" class="btn sm prominent">应用</button></form>';
    }
    html += "</div>";
    pop.innerHTML = html;
    clearTimeout(popTimer);
    pop.hidden = false;
    pop.classList.remove("open");
    var rect = anchor.getBoundingClientRect();
    var width = pop.offsetWidth;
    var height = pop.offsetHeight;
    var left = Math.max(8, Math.min(rect.left, window.innerWidth - width - 8));
    var top = rect.bottom + 6;
    var above = false;
    if (top + height > window.innerHeight - 8) {
      top = Math.max(8, rect.top - height - 6);
      above = true;
    }
    pop.style.left = left + "px";
    pop.style.top = top + "px";
    var originX = Math.max(12, Math.min(Math.max(12, width - 12), rect.left + rect.width / 2 - left));
    pop.style.transformOrigin = originX + "px " + (above ? "100%" : "0");
    void pop.offsetWidth;
    pop.classList.add("open");
    anchor.setAttribute("aria-expanded", "true");
    menu = { anchor: anchor };
    Array.prototype.forEach.call(pop.querySelectorAll(".menu-item"), function (btn) {
      btn.addEventListener("click", function () {
        var picked = items[Number(btn.getAttribute("data-i"))];
        closeMenu();
        if (picked && typeof cfg.onPick === "function") cfg.onPick(picked.v);
      });
    });
    var form = pop.querySelector("#menuCustom");
    if (form && custom) {
      form.addEventListener("submit", function (event) {
        event.preventDefault();
        var input = form.querySelector("input");
        var n = Number(input.value);
        if (!input.value || !(n >= custom.min && n <= custom.max)) {
          input.classList.add("invalid");
          form.classList.remove("shake");
          void form.offsetWidth;
          form.classList.add("shake");
          return;
        }
        closeMenu();
        if (typeof cfg.onPick === "function") cfg.onPick(n);
      });
    }
    var first = pop.querySelector('.menu-item[aria-checked="true"]:not(:disabled)') || pop.querySelector(".menu-item:not(:disabled)");
    if (first) {
      try { first.focus({ preventScroll: true }); } catch (err) { first.focus(); }
    }
  }

  function focusables(root) {
    return Array.prototype.filter.call(root.querySelectorAll("a[href], button, input, select, textarea, [tabindex]"), function (node) {
      if (node.disabled) return false;
      if (node.getAttribute("tabindex") === "-1") return false;
      if (node.closest && node.closest("[hidden]")) return false;
      return node.getClientRects().length > 0;
    });
  }

  function closeSheet() {
    var wrap = $("sheet");
    var card = $("sheetCard");
    if (!wrap || wrap.hidden || !wrap.classList.contains("open")) return;
    wrap.classList.remove("open");
    clearTimeout(sheetTimer);
    var back = sheetReturn;
    sheetReturn = null;
    sheetTimer = setTimeout(function () {
      wrap.hidden = true;
      if (card) card.innerHTML = "";
    }, 280);
    if (back && back.isConnected && typeof back.focus === "function") back.focus();
  }

  function openSheet(cfg) {
    cfg = cfg || {};
    closeMenu(true);
    var wrap = $("sheet");
    var card = $("sheetCard");
    if (!wrap || !card) return;
    clearTimeout(sheetTimer);
    var actions = cfg.actions || [];
    var sub = cfg.sub ? '<p class="caption">' + U.esc(cfg.sub) + "</p>" : "";
    var buttons = actions.map(function (action, index) {
      return '<button type="button" class="btn ' + U.safeClass(action.kind || "") + '" data-ai="' + index + '">' + U.esc(action.label || "") + "</button>";
    }).join("");
    card.innerHTML = '<div class="sheet-h">' + (cfg.icon || "") + '<div><h2 id="sheetTitle">' + U.esc(cfg.title || "") + "</h2>" + sub +
      '</div><button type="button" class="btn sm icon plain sheet-x" data-close aria-label="关闭">' + U.ic("x") + "</button></div>" +
      '<div class="sheet-b">' + (cfg.body || "") + "</div>" +
      (actions.length ? '<div class="sheet-f">' + buttons + "</div>" : "");
    card.setAttribute("aria-labelledby", "sheetTitle");
    card.onclick = function (event) {
      if (event.target.closest && event.target.closest("[data-close]")) {
        closeSheet();
        return;
      }
      var btn = event.target.closest ? event.target.closest("[data-ai]") : null;
      if (!btn || btn.disabled) return;
      var action = actions[Number(btn.getAttribute("data-ai"))];
      if (!action || typeof action.fn !== "function") {
        closeSheet();
        return;
      }
      btn.disabled = true;
      Promise.resolve().then(function () { return action.fn(btn, card); }).then(function (result) {
        if (btn.isConnected) btn.disabled = false;
        if (result !== false) closeSheet();
      }).catch(function (err) {
        if (btn.isConnected) btn.disabled = false;
        toast((err && err.message) || "操作失败", { tone: "err" });
      });
    };
    card.onkeydown = function (event) {
      if (event.key === "Enter" && event.target && event.target.matches && event.target.matches("input, select")) {
        var prominent = card.querySelector(".sheet-f .btn.prominent");
        if (prominent) {
          event.preventDefault();
          prominent.click();
        }
        return;
      }
      if (event.key !== "Tab") return;
      var list = focusables(card);
      if (!list.length) return;
      var first = list[0];
      var last = list[list.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    var opened = !wrap.hidden && wrap.classList.contains("open");
    if (!opened) sheetReturn = document.activeElement;
    wrap.hidden = false;
    wrap.classList.remove("open");
    void wrap.offsetWidth;
    wrap.classList.add("open");
    if (typeof cfg.onMount === "function") cfg.onMount(card);
    setTimeout(function () {
      var target = card.querySelector("[autofocus]") || card.querySelector(".sheet-f .btn:last-child") || card.querySelector(".sheet-x");
      if (target) target.focus();
    }, 60);
  }

  function confirmSheet(cfg) {
    cfg = cfg || {};
    var danger = !!cfg.danger;
    openSheet({
      title: cfg.title,
      body: cfg.body || "",
      icon: cfg.icon || ('<span class="sheet-ic' + (danger ? " red" : "") + '">' + U.ic(danger ? "warn" : "info") + "</span>"),
      actions: [
        { label: "取消", kind: "plain" },
        { label: cfg.okLabel || "确定", kind: danger ? "prominent danger" : "prominent", fn: cfg.onOk }
      ]
    });
  }

  function setSlot(slot, value) {
    if (!slot) return false;
    slot.replaceChildren();
    if (value == null || value === "") return false;
    if (typeof value === "string") {
      slot.innerHTML = value;
      return true;
    }
    if (value instanceof Node) {
      slot.appendChild(value);
      return true;
    }
    return false;
  }

  function syncTopbarHeight() {
    var main = $("main");
    var bar = $("topbar");
    if (main && bar) main.style.setProperty("--tb-h", bar.offsetHeight + "px");
  }

  function syncScrolled() {
    var scroller = $("scroll");
    var bar = $("topbar");
    if (scroller && bar) bar.classList.toggle("is-scrolled", scroller.scrollTop > 4);
  }

  function setActions(value) {
    setSlot($("tbActions"), value);
    syncTopbarHeight();
  }

  function setAcc(value) {
    var slot = $("tbAcc");
    var filled = setSlot(slot, value);
    if (slot) slot.hidden = !filled;
    syncTopbarHeight();
  }

  function topbar(cfg) {
    cfg = cfg || {};
    if (cfg.title != null && $("tbTitle")) $("tbTitle").textContent = String(cfg.title);
    if (cfg.sub != null && $("tbSub")) $("tbSub").textContent = String(cfg.sub);
    if (Object.prototype.hasOwnProperty.call(cfg, "actions")) setActions(cfg.actions);
    if (Object.prototype.hasOwnProperty.call(cfg, "acc")) setAcc(cfg.acc);
  }

  function setChannel(ch) {
    var main = $("main");
    if (main) main.setAttribute("data-ch", ch === "ai" ? "ai" : "daily");
  }

  function setBadge(key, text, cls) {
    var badge = document.querySelector('[data-nb="' + key + '"]');
    if (!badge) return;
    var show = text != null && String(text) !== "";
    badge.hidden = !show;
    badge.textContent = show ? String(text) : "";
    badge.className = "nav-badge" + (cls ? " " + U.safeClass(cls) : "");
  }

  function placeNav(animate) {
    var nav = $("nav");
    var ind = $("navInd");
    if (!nav || !ind) return;
    var item = nav.querySelector(".nav-item.is-active");
    if (!item || item.getBoundingClientRect().height <= 0) return;
    var box = U.navIndicatorPlacement(item.getBoundingClientRect(), nav.getBoundingClientRect());
    if (!animate) ind.style.transition = "none";
    ind.style.left = box.x;
    ind.style.right = "auto";
    ind.style.width = box.width;
    ind.style.height = box.height;
    ind.style.setProperty("--y", box.y);
    if (!animate) {
      void ind.offsetWidth;
      ind.style.transition = "";
    }
  }

  function goTab(id, opt) {
    opt = opt || {};
    if (TAB_ORDER.indexOf(id) < 0) return;
    if (current === id && !opt.force) return;
    var prev = current;
    if (prev && tabs[prev] && typeof tabs[prev].leave === "function") {
      if (tabs[prev].leave() === false && !opt.force) return;
    }
    closeMenu(true);
    closeSheet();
    current = id;
    Array.prototype.forEach.call(document.querySelectorAll(".nav-item"), function (btn) {
      var on = btn.getAttribute("data-tab") === id;
      btn.classList.toggle("is-active", on);
      btn.setAttribute("aria-current", on ? "page" : "false");
    });
    placeNav(!opt.initial);
    if ($("tbTitle")) $("tbTitle").textContent = TAB_META[id][0];
    if ($("tbSub")) $("tbSub").textContent = TAB_META[id][1];
    setActions("");
    setAcc("");
    setChannel("daily");
    var root = $("page");
    if (root) {
      root.innerHTML = "";
      if (tabs[id] && typeof tabs[id].enter === "function") tabs[id].enter(root);
      root.classList.remove("page-in");
      void root.offsetWidth;
      root.classList.add("page-in");
    }
    var scroller = $("scroll");
    if (scroller) scroller.scrollTop = 0;
    syncScrolled();
    syncTopbarHeight();
  }

  function registerTab(id, tab) {
    if (TAB_ORDER.indexOf(id) < 0 || !tab || typeof tab.enter !== "function") return;
    tabs[id] = tab;
    if (current === id) {
      var root = $("page");
      if (root) {
        root.replaceChildren();
        tab.enter(root);
      }
    }
  }

  function paintSide() {
    var core = state.core;
    var running = !!(core && core.running);
    var dot = $("sideDot");
    var word = $("sideCore");
    var mode = $("sideMode");
    var sys = $("sideSys");
    if (dot) dot.className = running ? "pulse" : "pulse off";
    if (word) word.textContent = running ? "内核运行中" : "内核已停止";
    var modeKey = core && core.mode ? core.mode : "rule";
    if (mode) mode.textContent = MODE_LABEL[modeKey] || modeKey;
    var proxyOn = !!(core && core.systemProxyActive);
    if (sys) {
      sys.textContent = proxyOn ? "系统代理 开" : "系统代理 关";
      sys.classList.toggle("on", proxyOn);
    }
  }

  function publishCore(status) {
    state.core = status || null;
    if (status && status.running) state.phase = "running";
    else if (state.phase !== "download" && state.phase !== "error" && state.phase !== "geo") state.phase = "stopped";
    paintSide();
    eachTab("onCore", status || null);
  }

  function refreshCore() {
    return api("GET", "/api/core/status").then(function (data) {
      publishCore(data);
      return data;
    });
  }

  function refreshSnapshot() {
    return api("GET", "/api/snapshot").then(function (snap) {
      state.snapshot = snap || null;
      return state.snapshot;
    });
  }

  function applyCoreEvent(core) {
    if (core && core.phase) state.phase = core.phase;
    paintSide();
    if (core && (core.phase === "running" || core.phase === "stopped")) refreshCore().catch(function () {});
  }

  function dispatch(msg) {
    if (!msg || typeof msg !== "object" || !msg.type) return;
    if (msg.type === "core" && msg.core) applyCoreEvent(msg.core);
    if (msg.type === "sync") {
      if (msg.snapshot) state.snapshot = msg.snapshot;
      else refreshSnapshot().catch(function () {});
    }
    if (msg.type === "open_update" && typeof PB.openUpdate === "function") PB.openUpdate();
    emit(msg.type, msg);
    eachTab("onSSE", msg);
  }

  function connectEvents() {
    if (es) {
      es.onopen = null;
      es.onmessage = null;
      es.onerror = null;
      es.close();
    }
    es = new EventSource("/api/events");
    es.onopen = function () {
      var again = dropped;
      dropped = false;
      retryMs = 1000;
      if (again) {
        refreshCore().catch(function () {});
        refreshSnapshot().catch(function () {});
      }
    };
    es.onmessage = function (ev) {
      var msg = null;
      try { msg = JSON.parse(ev.data); } catch (err) { return; }
      dispatch(msg);
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

  function paintVersion() {
    var text = $("verText");
    var dot = $("verDot");
    var btn = $("verBtn");
    if (text) text.textContent = state.version || "…";
    var has = !!(state.update && state.update.hasUpdate);
    if (dot) dot.hidden = !has;
    if (btn) btn.title = has && state.update.latestVersion ? "有可用更新 " + state.update.latestVersion : "检查更新";
  }

  function loadVersion() {
    return api("GET", "/api/version").then(function (data) {
      if (data && data.version) state.version = data.version;
      if (data && data.releasesUrl) state.releasesUrl = data.releasesUrl;
      paintVersion();
      return data;
    }).catch(function () { return null; });
  }

  function checkUpdate() {
    return api("POST", "/api/update/check", {}).then(function (info) {
      state.update = info || null;
      if (info && info.currentVersion) state.version = info.currentVersion;
      paintVersion();
      return info;
    }).catch(function () { return null; });
  }

  function openHome() {
    api("POST", "/api/open-backups", {}).then(function () {
      toast("已在资源管理器中打开 ~/.proxy-box", { tone: "info" });
    }).catch(function (err) {
      toast((err && err.message) || "打开失败", { tone: "err" });
    });
  }

  function themeView() {
    return U.themeFromStorage(window.localStorage, themeMQ.matches);
  }

  function applyTheme() {
    var view = themeView();
    document.documentElement.setAttribute("data-theme", view.resolved);
    var btn = $("sideTheme");
    if (!btn) return;
    btn.innerHTML = U.ic(view.resolved === "dark" ? "moon" : "sun");
    btn.title = view.resolved === "dark" ? "当前：深色，点击切换为浅色" : "当前：浅色，点击切换为深色";
  }

  function themeSet(pref) {
    var next = U.normalizeTheme(pref);
    try { window.localStorage.setItem(U.THEME_KEY, next); } catch (err) {}
    document.documentElement.classList.add("theming");
    applyTheme();
    setTimeout(function () { document.documentElement.classList.remove("theming"); }, 420);
    var seg = $("stTheme");
    if (seg) segSet(seg, next);
  }

  function onKey(event) {
    if (event.isComposing || event.key === "Process") return;
    if (event.key === "Escape") {
      if (menu) {
        var anchor = menu.anchor;
        closeMenu();
        if (anchor && anchor.focus) anchor.focus();
        event.preventDefault();
        return;
      }
      var sheet = $("sheet");
      if (sheet && !sheet.hidden && sheet.classList.contains("open")) {
        closeSheet();
        event.preventDefault();
        return;
      }
      var search = document.activeElement;
      if (search && search.matches && search.matches("[data-search]") && search.value) {
        search.value = "";
        search.dispatchEvent(new Event("input", { bubbles: true }));
        event.preventDefault();
      }
      return;
    }
    if (menu && (event.key === "ArrowDown" || event.key === "ArrowUp")) {
      var items = Array.prototype.slice.call(document.querySelectorAll("#pop .menu-item:not(:disabled)"));
      if (!items.length) return;
      var index = items.indexOf(document.activeElement);
      var step = event.key === "ArrowDown" ? 1 : -1;
      items[(index + step + items.length) % items.length].focus();
      event.preventDefault();
      return;
    }
    var mod = event.ctrlKey || event.metaKey;
    if (!mod || event.altKey) return;
    if (event.key === "f" || event.key === "F") {
      var field = document.querySelector("[data-search]");
      if (!field || typeof field.focus !== "function") return;
      event.preventDefault();
      field.focus();
      if (typeof field.select === "function") field.select();
      return;
    }
    if (/^[1-6]$/.test(event.key)) {
      var open = $("sheet");
      if (open && !open.hidden && open.classList.contains("open")) return;
      event.preventDefault();
      goTab(TAB_ORDER[Number(event.key) - 1]);
    }
  }

  function bindShell() {
    var nav = $("nav");
    if (nav) {
      nav.addEventListener("click", function (event) {
        var btn = event.target.closest ? event.target.closest(".nav-item") : null;
        if (btn) goTab(btn.getAttribute("data-tab"));
      });
      if (window.ResizeObserver) new ResizeObserver(function () { placeNav(false); }).observe(nav);
    }
    var bar = $("topbar");
    if (bar && window.ResizeObserver) new ResizeObserver(syncTopbarHeight).observe(bar);
    var scroller = $("scroll");
    if (scroller) {
      scroller.addEventListener("scroll", function () {
        syncScrolled();
        if (menu) closeMenu();
      }, { passive: true });
    }
    var sheet = $("sheet");
    if (sheet) {
      sheet.addEventListener("click", function (event) {
        if (event.target && event.target.id === "sheet") closeSheet();
      });
    }
    document.addEventListener("pointerdown", function (event) {
      if (!menu) return;
      var pop = $("pop");
      if (pop && pop.contains(event.target)) return;
      if (menu.anchor && menu.anchor.contains(event.target)) return;
      closeMenu();
    }, true);
    window.addEventListener("resize", function () {
      placeNav(false);
      if (menu) closeMenu(true);
    });
    document.addEventListener("keydown", onKey);
    if ($("sideStatus")) $("sideStatus").addEventListener("click", function () { goTab("overview"); });
    if ($("sideHome")) $("sideHome").addEventListener("click", openHome);
    if ($("sideTheme")) {
      $("sideTheme").addEventListener("click", function () {
        themeSet(themeView().resolved === "dark" ? "light" : "dark");
      });
    }
    if ($("verBtn")) {
      $("verBtn").addEventListener("click", function () {
        if (typeof PB.openUpdate === "function") PB.openUpdate();
      });
    }
    if (themeMQ.addEventListener) {
      themeMQ.addEventListener("change", function () {
        if (themeView().pref === "auto") applyTheme();
      });
    }
  }

  var PB = {
    api: api,
    esc: U.esc,
    ic: U.ic,
    sw: U.sw,
    el: el,
    copyText: copyText,
    fmt: {
      ago: U.ago,
      bytes: U.bytes,
      ms: U.ms,
      mbps: U.mbps,
      uptime: U.uptime,
      daysLeft: U.daysLeft,
      date: U.date
    },
    maskUrl: U.maskUrl,
    on: on,
    onInput: onInput,
    toast: toast,
    hideToast: hideToast,
    openMenu: openMenu,
    closeMenu: closeMenu,
    openSheet: openSheet,
    closeSheet: closeSheet,
    confirmSheet: confirmSheet,
    segHTML: U.segHTML,
    mountSeg: mountSeg,
    segSet: segSet,
    registerTab: registerTab,
    goTab: goTab,
    go: function (id) { goTab(id); },
    current: function () { return current; },
    setBadge: setBadge,
    setChannel: setChannel,
    topbar: topbar,
    setActions: setActions,
    setAcc: setAcc,
    theme: {
      get: function () { return themeView().pref; },
      set: themeSet,
      resolved: function () { return themeView().resolved; }
    },
    refreshCore: refreshCore,
    refreshSnapshot: refreshSnapshot,
    state: state,
    tabs: tabs
  };

  window.PB = PB;

  document.addEventListener("DOMContentLoaded", function () {
    bindShell();
    applyTheme();
    goTab("overview", { initial: true });
    connectEvents();
    refreshCore().catch(function () {});
    refreshSnapshot().catch(function () {});
    loadVersion();
    checkUpdate();
    setInterval(function () { refreshCore().catch(function () {}); }, 10000);
    requestAnimationFrame(function () { placeNav(false); });
  });
})();
