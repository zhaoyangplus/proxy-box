(function () {
  "use strict";

  var F = window.PBNodeFilter;
  if (!F) throw new Error("ui/nodes-filter.js must load before nodes.js");

  var TYPE_LABELS = {
    ss: "Shadowsocks",
    shadowsocks: "Shadowsocks",
    ssr: "ShadowsocksR",
    shadowsocksr: "ShadowsocksR",
    vmess: "VMess",
    vless: "VLESS",
    trojan: "Trojan",
    hysteria: "Hysteria",
    hysteria2: "Hysteria2",
    hy2: "Hysteria2",
    tuic: "TUIC",
    wireguard: "WireGuard",
    wg: "WireGuard",
    http: "HTTP",
    socks: "SOCKS",
    socks5: "SOCKS5",
    snell: "Snell",
    anytls: "AnyTLS",
    mieru: "Mieru",
    direct: "Direct",
    reject: "Reject",
    rejectdrop: "Reject",
    urltest: "URLTest",
    "url-test": "URLTest",
    fallback: "Fallback",
    loadbalance: "LoadBalance",
    "load-balance": "LoadBalance",
    selector: "Selector",
    relay: "Relay"
  };

  var OWNER_KEY = "pb.speedOwner.v1";
  var view = "card";
  var filters = F.loadFilters(storage());
  var pools = { daily: [], ai: [] };
  var subMap = {};
  var speedMem = {};
  var delayMem = {};
  var picked = { daily: null, ai: null };
  var speed = idleSpeed();
  var speedTimer = 0;
  var delayJob = { daily: null, ai: null };
  var delayBusy = { daily: {}, ai: {} };
  var active = false;
  var sawCore = null;
  var loadGen = { daily: 0, ai: 0 };
  var loading = { daily: false, ai: false };
  var listEl = null;
  var unbindQ = null;
  var regionSeen = "";

  function storage() {
    try { return window.localStorage; } catch (err) { return null; }
  }

  function sessionStore() {
    try { return window.sessionStorage; } catch (err) { return null; }
  }

  function channel() {
    return PB.state && PB.state.nodeGroup === "ai" ? "ai" : "daily";
  }

  function useChannel(ch) {
    ch = ch === "ai" ? "ai" : "daily";
    if (!PB.state) return ch;
    PB.state.nodeGroup = ch;
    PB.setChannel(ch);
    return ch;
  }

  function groupOf(ch) {
    return ch === "ai" ? F.GROUP_AI : F.GROUP_PROXY;
  }

  function channelWord(ch) {
    return ch === "ai" ? "AI 专属" : "日常代理";
  }

  function otherChannel(ch) {
    return ch === "ai" ? "daily" : "ai";
  }

  function currentFilter() {
    return filters[groupOf(channel())];
  }

  function persist() {
    F.saveFilters(storage(), filters);
  }

  function coreOn() {
    var core = PB.state && PB.state.core;
    if (core && typeof core.running === "boolean") return !!core.running;
    return !!(PB.state && PB.state.phase === "running");
  }

  function pretty(type) {
    var raw = String(type || "");
    if (!raw) return "";
    return TYPE_LABELS[raw.toLowerCase()] || raw;
  }

  function subName(id) {
    if (!id) return "";
    var row = subMap[id];
    return row && row.name ? row.name : String(id);
  }

  function idleSpeed() {
    return {
      running: false,
      stopping: false,
      channel: "",
      taskId: "",
      done: 0,
      total: 0,
      current: "",
      pending: null
    };
  }

  function normalize(raw) {
    var src = raw || {};
    var node = {
      name: src.name != null ? String(src.name) : "",
      type: src.type != null ? String(src.type) : "",
      provider: src.provider != null ? String(src.provider) : "",
      delayMs: Number(src.delayMs),
      untested: !!src.untested,
      alive: !!src.alive,
      isAuto: !!src.isAuto,
      selected: !!src.selected,
      resolvedTo: src.resolvedTo != null ? String(src.resolvedTo) : "",
      speedMBps: Number(src.speedMBps),
      speedTested: !!src.speedTested,
      speedError: src.speedError != null ? String(src.speedError) : ""
    };
    if (!isFinite(node.delayMs)) node.delayMs = 0;
    if (!isFinite(node.speedMBps)) node.speedMBps = 0;
    node.typeLabel = pretty(node.type);
    node.subName = subName(node.provider);
    if (!F.isUtilityNode(node)) {
      node.region = F.detectRegion(node.name);
      node.regionLabel = F.regionLabel(node.region);
      node.regionCode = F.regionCode(node.region);
    } else {
      node.region = "";
      node.regionLabel = "";
      node.regionCode = "";
    }
    return node;
  }

  function normalizeList(raw) {
    var list = Array.isArray(raw) ? raw : [];
    var out = new Array(list.length);
    for (var i = 0; i < list.length; i++) out[i] = normalize(list[i]);
    return out;
  }

  function applySpeedTo(list) {
    for (var i = 0; i < list.length; i++) {
      var node = list[i];
      if (node.speedTested) {
        speedMem[node.name] = { speedMBps: node.speedMBps, speedTested: true, speedError: node.speedError };
      } else if (speedMem[node.name]) {
        node.speedMBps = speedMem[node.name].speedMBps;
        node.speedTested = true;
        node.speedError = speedMem[node.name].speedError;
      }
    }
  }

  function takeList(ch, raw, started, trustDelay) {
    var list = normalizeList(raw);
    var choice = picked[ch];
    for (var i = 0; i < list.length; i++) {
      var mem = delayMem[list[i].name];
      if (!trustDelay && mem && mem.at > started) {
        list[i].delayMs = mem.delayMs;
        list[i].untested = false;
        list[i].alive = mem.alive;
      }
      if (choice && choice.at > started) list[i].selected = list[i].name === choice.name;
    }
    if (trustDelay) {
      var now = Date.now();
      for (var j = 0; j < list.length; j++) {
        if (list[j].untested) continue;
        delayMem[list[j].name] = { delayMs: list[j].delayMs, alive: list[j].delayMs > 0, at: now };
      }
    }
    applySpeedTo(list);
    pools[ch] = list;
  }

  function countReal(pool) {
    var total = 0;
    var alive = 0;
    var list = pool || [];
    for (var i = 0; i < list.length; i++) {
      if (F.isUtilityNode(list[i])) continue;
      total++;
      if (!list[i].untested && list[i].delayMs > 0) alive++;
    }
    return { total: total, alive: alive };
  }

  function findIn(ch, name) {
    var list = pools[ch] || [];
    for (var i = 0; i < list.length; i++) if (list[i].name === name) return list[i];
    return null;
  }

  function findNode(name) {
    return findIn(channel(), name);
  }

  function findEl(name) {
    if (!listEl) return null;
    var nodes = listEl.querySelectorAll("[data-id]");
    for (var i = 0; i < nodes.length; i++) {
      if (nodes[i].getAttribute("data-id") === name) return nodes[i];
    }
    return null;
  }

  function cardOpt(node) {
    var ch = channel();
    var phase = "";
    if (speed.running && speed.channel === ch) {
      if (speed.current && node.name === speed.current) phase = "current";
      else if (speed.pending && speed.pending[node.name]) phase = "queued";
    }
    return {
      selected: !!node.selected,
      delayBusy: !!(delayBusy[ch] && delayBusy[ch][node.name]),
      speedPhase: phase,
      speedBusy: !!speed.running,
      core: coreOn()
    };
  }

  function emptyBox(icon, title, caption, extra) {
    return '<div class="empty"><div class="empty-ic">' + PB.ic(icon) + '</div><div class="empty-t">' + PB.esc(title) + "</div>" +
      (caption ? '<p class="caption">' + PB.esc(caption) + "</p>" : "") + (extra || "") + "</div>";
  }

  function paintCount(filtering, rows, stat) {
    var el = document.getElementById("ndCount");
    if (!el) return;
    if (!coreOn()) {
      el.textContent = "";
      return;
    }
    var hit = filtering ? rows.length : stat.alive;
    el.innerHTML = (filtering ? "命中 " : "可用 ") + "<b>" + hit + "</b> / " + stat.total;
  }

  function paintList() {
    if (!listEl) return;
    if (!coreOn()) {
      listEl.innerHTML = emptyBox("warn", "内核未运行", "启动内核后即可查看节点、测延迟和测网速。",
        '<button type="button" class="btn sm prominent" data-go="overview">' + PB.ic("play") + "前往总览</button>");
      paintCount(false, [], { total: 0, alive: 0 });
      return;
    }
    var pool = pools[channel()] || [];
    var f = currentFilter();
    var filtering = F.isFilterActive(f);
    var rows = F.sortNodes(F.applyFilters(pool, f), f);
    var stat = countReal(pool);
    paintCount(filtering, rows, stat);
    if (!stat.total) {
      if (loading[channel()]) {
        listEl.innerHTML = emptyBox("globe", "正在读取节点", "", "");
        return;
      }
      listEl.innerHTML = emptyBox("layers", "此通道暂无节点", "请先在订阅管理中添加或启用订阅。",
        '<button type="button" class="btn sm prominent" data-go="subs">' + PB.ic("layers") + "前往订阅管理</button>");
      return;
    }
    if (filtering && !rows.length) {
      var reason = F.emptyReason(pool, f);
      listEl.innerHTML = emptyBox("search", "没有符合条件的节点", reason || "点击下面的条件可以单独移除。",
        '<div class="empty-chips">' + F.emptyChipsHTML(f, subName) + '</div><button type="button" class="btn sm prominent" data-clear-all>清除筛选</button>');
      return;
    }
    var parts = new Array(rows.length);
    var asList = view === "list";
    for (var i = 0; i < rows.length; i++) {
      var opt = cardOpt(rows[i]);
      parts[i] = asList ? F.nodeRowHTML(rows[i], opt) : F.nodeCardHTML(rows[i], opt);
    }
    listEl.innerHTML = asList
      ? '<div class="nd-table">' + listHead() + parts.join("") + "</div>"
      : '<div class="nd-grid">' + parts.join("") + "</div>";
  }

  function listHead() {
    var f = currentFilter();
    function th(key, label) {
      var on = f.sortKey === key;
      var mark = on ? PB.ic(f.sortDir === 1 ? "up" : "down", "xs") : "";
      return '<button type="button" data-sort="' + key + '" class="' + (on ? "on" : "") + '">' + label + mark + "</button>";
    }
    return '<div class="nd-th"><div>' + th("name", "节点") + '</div><div class="c-proto">协议</div><div class="c-sub">订阅</div><div>' +
      th("delay", "延迟") + "</div><div>" + th("speed", "网速") + "</div><div></div></div>";
  }

  function paintSort() {
    var label = document.getElementById("ndSortLbl");
    var f = currentFilter();
    var text = f.sortKey === "speed" ? "按网速" : f.sortKey === "name" ? "按名称" : "按延迟";
    if (label) label.textContent = text;
    var btn = document.getElementById("ndSort");
    if (btn) btn.title = text + (f.sortDir === 1 ? "，升序" : "，降序");
  }

  function paintFades(el) {
    if (!el) return;
    el.classList.toggle("fade-l", el.scrollLeft > 2);
    el.classList.toggle("fade-r", el.scrollLeft + el.clientWidth < el.scrollWidth - 2);
  }

  function bindRegions(el) {
    paintFades(el);
    el.addEventListener("scroll", function () { paintFades(el); }, { passive: true });
    el.addEventListener("wheel", function (event) {
      if (Math.abs(event.deltaY) > Math.abs(event.deltaX) && el.scrollWidth > el.clientWidth) {
        el.scrollLeft += event.deltaY;
        event.preventDefault();
      }
    }, { passive: false });
  }

  function paintFilters() {
    var host = document.getElementById("ndFilters");
    if (!host) return;
    var pool = pools[channel()] || [];
    var f = currentFilter();
    var prev = document.getElementById("ndRegions");
    var left = prev ? prev.scrollLeft : 0;
    var changed = regionSeen !== f.region;
    regionSeen = f.region;
    var probe = F.sanitizeFilter(f);
    probe.region = "all";
    var counts = F.regionCounts(F.applyFilters(pool, probe));
    var total = 0;
    var chips = "";
    for (var i = 0; i < F.REGION_ORDER.length; i++) {
      var key = F.REGION_ORDER[i];
      var n = counts[key] || 0;
      if (n <= 0) continue;
      total += n;
      var on = f.region === key;
      chips += '<button type="button" class="rchip' + (on ? " on" : "") + '" data-region="' + PB.esc(key) + '" aria-pressed="' + (on ? "true" : "false") + '">' +
        '<span class="flag" style="--fc:' + PB.esc(F.regionColor(key)) + '" title="' + PB.esc(F.regionLabel(key)) + '">' + PB.esc(F.regionCode(key)) + "</span>" +
        PB.esc(F.regionLabel(key)) + '<span class="n">' + n + "</span></button>";
    }
    var activeN = F.activeFilterCount(f);
    host.innerHTML =
      '<button type="button" class="chip' + (f.sub !== "all" ? " on" : "") + '" data-menu="sub" aria-haspopup="menu" aria-expanded="false"><span class="k">订阅</span>' + PB.esc(f.sub === "all" ? "全部" : subName(f.sub)) + PB.ic("chev-down") + "</button>" +
      '<button type="button" class="chip' + (f.delayPreset !== "all" ? " on" : "") + '" data-menu="delay" aria-haspopup="menu" aria-expanded="false"><span class="k">延迟</span>' + PB.esc(F.delayLabel(f)) + PB.ic("chev-down") + "</button>" +
      '<button type="button" class="chip' + (f.speedPreset !== "all" ? " on" : "") + '" data-menu="speed" aria-haspopup="menu" aria-expanded="false"><span class="k">网速</span>' + PB.esc(F.speedLabel(f)) + PB.ic("chev-down") + "</button>" +
      '<span class="vsep"></span>' +
      '<div class="regions" id="ndRegions" role="group" aria-label="地区">' +
      '<button type="button" class="rchip all' + (f.region === "all" ? " on" : "") + '" data-region="all">全部地区<span class="n">' + total + "</span></button>" +
      chips + "</div>" +
      (activeN ? '<button type="button" class="chip clear" id="ndClear">' + PB.ic("x") + "清除 " + activeN + "</button>" : "");
    var rg = document.getElementById("ndRegions");
    if (!rg) return;
    if (!changed) rg.scrollLeft = left;
    bindRegions(rg);
    if (changed) {
      var chip = rg.querySelector(".rchip.on:not(.all)");
      if (chip) {
        var edge = chip.offsetLeft + chip.offsetWidth;
        if (chip.offsetLeft < rg.scrollLeft || edge > rg.scrollLeft + rg.clientWidth) rg.scrollLeft = Math.max(0, chip.offsetLeft - 16);
        paintFades(rg);
      }
    }
  }

  function paintActions() {
    var delayBtn = document.getElementById("ndDelay");
    var speedBtn = document.getElementById("ndSpeed");
    if (!delayBtn || !speedBtn) return;
    var ch = channel();
    var pool = pools[ch] || [];
    var names = F.scopeTargets(pool, currentFilter());
    var scoped = names !== null;
    var n = scoped ? names.length : countReal(pool).total;
    var run = coreOn();
    var job = delayJob[ch];
    if (job) {
      delayBtn.disabled = true;
      delayBtn.innerHTML = '<span class="spin xs"></span><span class="lbl">' + (job.batch ? "测延迟…" : "测延迟 " + job.done + "/" + job.total) + "</span>";
    } else {
      delayBtn.disabled = !run || !n;
      delayBtn.innerHTML = PB.ic("bolt") + '<span class="lbl">' + (scoped ? "测延迟 (" + n + ")" : "全部测延迟") + "</span>";
      delayBtn.title = scoped ? "测试当前筛选出的节点" : "测试本通道全部节点";
    }
    speedBtn.classList.remove("danger", "has-progress");
    speedBtn.style.removeProperty("--p");
    speedBtn.disabled = false;
    if (speed.running) {
      var pct = speed.total > 0 ? (speed.done / speed.total * 100) : 0;
      speedBtn.classList.add("has-progress");
      speedBtn.style.setProperty("--p", pct.toFixed(1) + "%");
      if (speed.stopping) {
        speedBtn.disabled = true;
        speedBtn.innerHTML = '<span class="spin xs"></span><span class="lbl">正在停止…</span>';
        speedBtn.title = "正在停止测速";
      } else if (speed.channel && speed.channel !== ch) {
        speedBtn.classList.add("danger");
        speedBtn.innerHTML = PB.ic("stop") + '<span class="lbl">' + channelWord(speed.channel) + "占用</span>";
        speedBtn.title = channelWord(speed.channel) + "的测速正在占用引擎";
      } else {
        speedBtn.classList.add("danger");
        speedBtn.innerHTML = PB.ic("stop") + '<span class="lbl">停止 ' + speed.done + "/" + speed.total + "</span>";
        speedBtn.title = "停止测速";
      }
      return;
    }
    speedBtn.disabled = !run || !n;
    speedBtn.innerHTML = PB.ic("gauge") + '<span class="lbl">' + (scoped ? "测网速 (" + n + ")" : "全部测网速") + "</span>";
    speedBtn.title = scoped ? "对当前筛选结果测网速" : "对本通道全部节点测网速";
  }

  function paintBadges() {
    var groups = ["daily", "ai"];
    for (var i = 0; i < groups.length; i++) {
      var ch = groups[i];
      var badge = document.querySelector('#ndGroup [data-badge="' + ch + '"]');
      if (!badge) continue;
      var n = F.activeFilterCount(filters[groupOf(ch)]);
      var busy = speed.running && speed.channel === ch;
      if (busy) {
        badge.hidden = false;
        badge.innerHTML = '<span class="spin xxs"></span>';
        badge.title = "测速进行中";
      } else if (n) {
        badge.hidden = false;
        badge.textContent = String(n);
        badge.title = "已启用 " + n + " 个筛选条件";
      } else {
        badge.hidden = true;
        badge.textContent = "";
        badge.title = "";
      }
    }
  }

  function refreshView() {
    paintSort();
    paintFilters();
    paintActions();
    paintBadges();
    paintList();
  }

  function patchNode(name) {
    if (!listEl || !name || !coreOn()) return;
    var node = findNode(name);
    if (!node) return;
    if (F.isFilterActive(currentFilter()) && !F.applyFilters([node], currentFilter()).length) {
      paintList();
      paintActions();
      return;
    }
    var el = findEl(name);
    if (!el) {
      paintList();
      return;
    }
    var html = view === "list" ? F.nodeRowHTML(node, cardOpt(node)) : F.nodeCardHTML(node, cardOpt(node));
    var focus = el.contains(document.activeElement);
    el.outerHTML = html;
    if (focus) {
      var next = findEl(name);
      if (next) {
        try { next.focus({ preventScroll: true }); } catch (err) { next.focus(); }
      }
    }
    var pool = pools[channel()] || [];
    var f = currentFilter();
    paintCount(F.isFilterActive(f), F.isFilterActive(f) ? F.applyFilters(pool, f) : [], countReal(pool));
  }

  function rememberOwner() {
    var store = sessionStore();
    if (!store) return;
    try {
      store.setItem(OWNER_KEY, JSON.stringify({ taskId: speed.taskId || "", channel: speed.channel || "" }));
    } catch (err) {}
  }

  function readOwner() {
    var store = sessionStore();
    if (!store) return null;
    try {
      var data = JSON.parse(store.getItem(OWNER_KEY) || "null");
      if (!data || typeof data !== "object") return null;
      return data;
    } catch (err) {
      return null;
    }
  }

  function clearOwner() {
    var store = sessionStore();
    if (!store) return;
    try { store.removeItem(OWNER_KEY); } catch (err) {}
  }

  function applyResult(result) {
    if (!result || !result.nodeName) return;
    var name = String(result.nodeName);
    var err = result.error ? String(result.error) : "";
    var mbps = Number(result.speedMBps);
    if (!isFinite(mbps)) mbps = 0;
    speedMem[name] = { speedMBps: mbps, speedTested: true, speedError: err };
    if (speed.pending) delete speed.pending[name];
    var groups = ["daily", "ai"];
    for (var g = 0; g < groups.length; g++) {
      var node = findIn(groups[g], name);
      if (!node) continue;
      node.speedMBps = mbps;
      node.speedTested = true;
      node.speedError = err;
    }
  }

  function loadSubs() {
    return PB.api("GET", "/api/subs").then(function (data) {
      var list = data && data.subscriptions ? data.subscriptions : [];
      subMap = {};
      for (var i = 0; i < list.length; i++) {
        var row = list[i];
        if (!row || row.id == null) continue;
        subMap[String(row.id)] = { name: row.name ? String(row.name) : String(row.id), enabled: !!row.enabled };
      }
      var groups = ["daily", "ai"];
      for (var g = 0; g < groups.length; g++) {
        var pool = pools[groups[g]];
        for (var n = 0; n < pool.length; n++) pool[n].subName = subName(pool[n].provider);
      }
    }).catch(function () {});
  }

  function speedLocal() {
    return {
      running: speed.running,
      done: speed.done,
      total: speed.total,
      current: speed.current,
      taskId: speed.taskId,
      finishedTask: speed.finishedTask || "",
      seen: speed.seen || {}
    };
  }

  function watchSpeed(polling) {
    if (!polling) {
      if (speedTimer) clearInterval(speedTimer);
      speedTimer = 0;
      return;
    }
    if (speedTimer) return;
    speedTimer = setInterval(function () { pullSpeed(); }, 2000);
  }

  function assignSpeedChannel() {
    if (!speed.running || speed.channel) return;
    var owner = readOwner();
    if (owner && owner.taskId && owner.taskId === speed.taskId && (owner.channel === "daily" || owner.channel === "ai")) {
      speed.channel = owner.channel;
      return;
    }
    if (speed.current && findIn(channel(), speed.current)) speed.channel = channel();
    else speed.channel = otherChannel(channel());
  }

  function ingestSpeed(remote) {
    var outcome = F.reconcileSpeed(speedLocal(), remote || {});
    watchSpeed(outcome.polling);
    if (!outcome.changed) return;
    var prev = speed.current;
    outcome.apply.forEach(applyResult);
    speed.running = outcome.speed.running;
    speed.done = outcome.speed.done;
    speed.total = outcome.speed.total;
    speed.current = outcome.speed.current;
    speed.taskId = outcome.speed.taskId || "";
    speed.finishedTask = outcome.speed.finishedTask || "";
    speed.seen = outcome.seen;
    if (!speed.running) {
      speed.stopping = false;
      speed.pending = null;
      speed.channel = "";
      clearOwner();
    } else assignSpeedChannel();
    if (!active) return;
    paintActions();
    paintBadges();
    if (!speed.running || channel() === speed.channel) {
      if (prev) patchNode(prev);
      if (speed.current) patchNode(speed.current);
      outcome.apply.forEach(function (item) {
        if (item && item.nodeName) patchNode(String(item.nodeName));
      });
    }
  }

  function pullSpeed() {
    return PB.api("GET", "/api/speedtest/status").then(function (data) {
      ingestSpeed({ progress: data && data.progress, results: data && data.results });
    }).catch(function () {});
  }

  function loadStatus() {
    return pullSpeed();
  }

  function loadNodes() {
    var ch = channel();
    var ticket = ++loadGen[ch];
    var started = Date.now();
    if (!coreOn()) {
      loading[ch] = false;
      paintList();
      paintActions();
      return Promise.resolve();
    }
    loading[ch] = true;
    if (!(pools[ch] && pools[ch].length) && channel() === ch) paintList();
    return PB.api("POST", "/api/nodes/list", {
      client: "builtin",
      group: groupOf(ch),
      testDelay: false
    }).then(function (data) {
      if (ticket === loadGen[ch]) loading[ch] = false;
      if (!active || ticket !== loadGen[ch]) return;
      takeList(ch, data && data.nodes, started, false);
      if (channel() === ch) refreshView();
    }).catch(function (err) {
      if (ticket === loadGen[ch]) loading[ch] = false;
      if (!active || ticket !== loadGen[ch] || channel() !== ch) return;
      var msg = err && err.message ? err.message : "节点加载失败";
      if (!coreOn() || msg.indexOf("未运行") >= 0) {
        paintList();
        return;
      }
      if (listEl) listEl.innerHTML = emptyBox("warn", "节点加载失败", msg, "");
      PB.toast(msg, "err");
    });
  }

  function setSort(key, dir) {
    var f = currentFilter();
    if (key) {
      f.sortKey = key;
      f.sortDir = dir == null ? F.defaultSortDir(key) : dir;
    } else if (dir) {
      f.sortDir = dir;
    }
    persist();
    paintSort();
    paintList();
  }

  function openSort(anchor) {
    var f = currentFilter();
    function hint(key, fallback) {
      if (f.sortKey !== key) return fallback;
      return f.sortDir === 1 ? "升序" : "降序";
    }
    PB.openMenu(anchor, {
      title: "排序",
      value: f.sortKey,
      items: [
        { v: "delay", label: "按延迟", hint: hint("delay", "低 → 高") },
        { v: "speed", label: "按网速", hint: hint("speed", "高 → 低") },
        { v: "name", label: "按名称", hint: hint("name", "A → Z") },
        { sep: true },
        { v: "asc", label: "升序" },
        { v: "desc", label: "降序" }
      ],
      onPick: function (value) {
        if (value === "asc" || value === "desc") setSort(null, value === "asc" ? 1 : -1);
        else if (value === f.sortKey) setSort(value, f.sortDir === 1 ? -1 : 1);
        else setSort(value, null);
      }
    });
  }

  function probeNodes(skip) {
    var f = F.sanitizeFilter(currentFilter());
    if (skip === "sub") f.sub = "all";
    if (skip === "delay") { f.delayPreset = "all"; f.maxDelayMs = 0; }
    if (skip === "speed") { f.speedPreset = "all"; f.minSpeedMBps = 0; }
    var rows = F.applyFilters(pools[channel()] || [], f);
    var out = [];
    for (var i = 0; i < rows.length; i++) if (!F.isUtilityNode(rows[i])) out.push(rows[i]);
    return out;
  }

  function openFilter(kind, anchor) {
    var f = currentFilter();
    if (kind === "sub") {
      var base = probeNodes("sub");
      var items = [{ v: "all", label: "全部订阅", hint: String(base.length) }, { sep: true }];
      Object.keys(subMap).forEach(function (id) {
        var count = 0;
        for (var i = 0; i < base.length; i++) if (base[i].provider === id) count++;
        items.push({
          v: id,
          label: subMap[id].name,
          hint: subMap[id].enabled ? String(count) : "已停用",
          disabled: !subMap[id].enabled
        });
      });
      PB.openMenu(anchor, {
        title: "订阅",
        value: f.sub,
        items: items,
        onPick: function (value) {
          f.sub = value ? String(value) : "all";
          persist();
          refreshView();
        }
      });
      return;
    }
    if (kind === "delay") {
      var value = f.delayPreset === "custom" ? Number(f.maxDelayMs) : f.delayPreset;
      PB.openMenu(anchor, {
        title: "延迟",
        value: value,
        items: [
          { v: "all", label: "不限" },
          { v: "alive", label: "仅可用" },
          { sep: true },
          { v: "100", label: "≤ 100 ms" },
          { v: "200", label: "≤ 200 ms" },
          { v: "300", label: "≤ 300 ms" },
          { v: "500", label: "≤ 500 ms" }
        ],
        custom: { label: "自定义 ≤", unit: "ms", min: 1, max: 10000, step: 1, def: f.delayPreset === "custom" && f.maxDelayMs > 0 ? f.maxDelayMs : 200 },
        onPick: function (pickedValue) {
          if (typeof pickedValue === "number") {
            f.delayPreset = "custom";
            f.maxDelayMs = pickedValue;
          } else {
            f.delayPreset = String(pickedValue);
            f.maxDelayMs = f.delayPreset === "100" || f.delayPreset === "200" || f.delayPreset === "300" || f.delayPreset === "500" ? Number(f.delayPreset) : 0;
          }
          persist();
          refreshView();
        }
      });
      return;
    }
    var speedValue = f.speedPreset === "custom" ? Number(f.minSpeedMBps) : f.speedPreset;
    PB.openMenu(anchor, {
      title: "网速",
      value: speedValue,
      items: [
        { v: "all", label: "不限" },
        { v: "tested", label: "已测速" },
        { sep: true },
        { v: "1", label: "≥ 1 MB/s" },
        { v: "5", label: "≥ 5 MB/s" },
        { v: "10", label: "≥ 10 MB/s" },
        { v: "20", label: "≥ 20 MB/s" }
      ],
      custom: { label: "自定义 ≥", unit: "MB/s", min: 0.1, max: 1000, step: 0.1, def: f.speedPreset === "custom" && f.minSpeedMBps > 0 ? f.minSpeedMBps : 5 },
      onPick: function (pickedValue) {
        if (typeof pickedValue === "number") {
          f.speedPreset = "custom";
          f.minSpeedMBps = pickedValue;
        } else {
          f.speedPreset = String(pickedValue);
          f.minSpeedMBps = f.speedPreset === "1" || f.speedPreset === "5" || f.speedPreset === "10" || f.speedPreset === "20" ? Number(f.speedPreset) : 0;
        }
        persist();
        refreshView();
      }
    });
  }

  function clearFilters() {
    var prev = currentFilter();
    var next = F.defaultGroupFilter();
    next.sortKey = prev.sortKey;
    next.sortDir = prev.sortDir;
    filters[groupOf(channel())] = next;
    persist();
    var input = document.getElementById("ndQ");
    var clear = document.getElementById("ndQClear");
    if (input) input.value = "";
    if (clear) clear.hidden = true;
    refreshView();
    PB.toast("已清除「" + channelWord(channel()) + "」的筛选条件", "info");
  }

  function removeChip(key) {
    filters[groupOf(channel())] = F.clearChip(currentFilter(), key);
    persist();
    if (key === "query") {
      var input = document.getElementById("ndQ");
      var clear = document.getElementById("ndQClear");
      if (input) input.value = "";
      if (clear) clear.hidden = true;
    }
    refreshView();
  }

  function poolRun(items, limit, worker) {
    var index = 0;
    var width = Math.min(limit, items.length);
    var jobs = [];
    function next() {
      if (index >= items.length) return Promise.resolve();
      var item = items[index++];
      return Promise.resolve()
        .then(function () { return worker(item); })
        .then(next, next);
    }
    for (var i = 0; i < width; i++) jobs.push(next());
    return Promise.all(jobs);
  }

  function rememberDelay(name, ms) {
    delayMem[name] = { delayMs: ms, alive: ms > 0, at: Date.now() };
  }

  function testOne(name) {
    var node = findNode(name);
    if (!node || F.isUtilityNode(node)) return;
    if (!coreOn()) {
      PB.toast("内核未运行，无法测延迟", "warn");
      return;
    }
    var ch = channel();
    if (delayBusy[ch][name]) return;
    delayBusy[ch][name] = true;
    patchNode(name);
    PB.api("POST", "/api/nodes/test", { client: "builtin", nodeName: name }).then(function (data) {
      var live = findIn(ch, name);
      var ms = Number(data && data.delayMs);
      if (!isFinite(ms)) ms = 0;
      rememberDelay(name, ms);
      if (!live) return;
      live.untested = false;
      live.delayMs = ms;
      live.alive = ms > 0;
    }).catch(function (err) {
      PB.toast((err && err.message) || "测延迟失败", "err");
    }).then(function () {
      delete delayBusy[ch][name];
      if (active && channel() === ch) patchNode(name);
    });
  }

  function testDelayAll() {
    if (!coreOn()) {
      PB.toast("内核未运行，无法测延迟", "warn");
      return;
    }
    var ch = channel();
    if (delayJob[ch]) return;
    var names = F.scopeTargets(pools[ch], currentFilter());
    if (names && !names.length) return;
    if (names) {
      delayJob[ch] = { done: 0, total: names.length, batch: false };
      paintActions();
      paintBadges();
      poolRun(names, 6, function (name) {
        delayBusy[ch][name] = true;
        if (active && channel() === ch) patchNode(name);
        return PB.api("POST", "/api/nodes/test", { client: "builtin", nodeName: name }).then(function (data) {
          var ms = Number(data && data.delayMs);
          if (!isFinite(ms)) ms = 0;
          rememberDelay(name, ms);
          var live = findIn(ch, name);
          if (!live) return;
          live.untested = false;
          live.delayMs = ms;
          live.alive = ms > 0;
        }).catch(function (err) {
          PB.toast((err && err.message) || "测延迟失败", "err");
        }).then(function () {
          delete delayBusy[ch][name];
          delayJob[ch].done++;
          if (active && channel() === ch) {
            patchNode(name);
            paintActions();
          }
        });
      }).then(function () {
        delayJob[ch] = null;
        if (!active) return;
        paintActions();
        paintBadges();
        if (channel() === ch) paintList();
      });
      return;
    }
    delayJob[ch] = { done: 0, total: 0, batch: true };
    paintActions();
    var started = Date.now();
    var ticket = ++loadGen[ch];
    PB.api("POST", "/api/nodes/list", {
      client: "builtin",
      group: groupOf(ch),
      testDelay: true
    }).then(function (data) {
      if (ticket !== loadGen[ch]) return;
      takeList(ch, data && data.nodes, started, true);
    }).catch(function (err) {
      PB.toast((err && err.message) || "测延迟失败", "err");
    }).then(function () {
      delayJob[ch] = null;
      if (!active) return;
      paintActions();
      if (channel() === ch) refreshView();
    });
  }

  function startSpeed(onlyName) {
    if (!coreOn()) {
      PB.toast("内核未运行，无法测速", "warn");
      return;
    }
    if (speed.running) {
      PB.toast((speed.channel ? channelWord(speed.channel) : "另一通道") + "的测速正在占用引擎", "warn");
      return;
    }
    var ch = channel();
    var names = onlyName ? [onlyName] : F.scopeTargets(pools[ch], currentFilter());
    if (names && !names.length) return;
    var body = { group: groupOf(ch) };
    if (names && names.length) body.nodes = names.slice();
    speed.running = true;
    speed.stopping = false;
    speed.channel = ch;
    speed.done = 0;
    speed.total = names ? names.length : 0;
    speed.current = names && names.length === 1 ? names[0] : "";
    speed.pending = null;
    speed.finishedTask = "";
    speed.seen = {};
    if (names && names.length) {
      speed.pending = {};
      for (var i = 0; i < names.length; i++) speed.pending[names[i]] = 1;
    }
    rememberOwner();
    watchSpeed(true);
    paintActions();
    paintBadges();
    if (speed.current) patchNode(speed.current);
    PB.api("POST", "/api/speedtest/start", body).then(function (data) {
      speed.taskId = data && data.taskId ? String(data.taskId) : speed.taskId;
      if (data && data.total) speed.total = Number(data.total) || speed.total;
      rememberOwner();
      if (active) paintActions();
    }).catch(function (err) {
      speed.running = false;
      speed.channel = "";
      speed.pending = null;
      speed.current = "";
      watchSpeed(false);
      clearOwner();
      PB.toast((err && err.message) || "测网速失败", "err");
      if (active) {
        paintActions();
        paintBadges();
        paintList();
      }
    });
  }

  function stopSpeed() {
    if (!speed.running || speed.stopping) return;
    speed.stopping = true;
    paintActions();
    PB.api("POST", "/api/speedtest/stop").catch(function (err) {
      speed.stopping = false;
      PB.toast((err && err.message) || "停止测速失败", "err");
      if (active) paintActions();
    });
  }

  function onSpeedMsg(msg) {
    var progress = msg && (msg.speedtest || msg.Speedtest);
    if (!progress) return;
    ingestSpeed({ progress: progress });
  }

  function selectNode(name) {
    var node = findNode(name);
    if (!node || node.selected) return;
    if (!coreOn()) {
      PB.toast("内核未运行，请先到总览启动", "warn");
      return;
    }
    var ch = channel();
    var prev = "";
    var list = pools[ch];
    for (var i = 0; i < list.length; i++) if (list[i].selected) prev = list[i].name;
    PB.api("POST", "/api/nodes/select", {
      client: "builtin",
      group: groupOf(ch),
      nodeName: name
    }).then(function () {
      picked[ch] = { name: name, at: Date.now() };
      for (var n = 0; n < pools[ch].length; n++) pools[ch][n].selected = pools[ch][n].name === name;
      if (active && channel() === ch) {
        if (prev) patchNode(prev);
        patchNode(name);
      }
      PB.toast(channelWord(ch) + "已切换到「" + name + "」");
    }).catch(function (err) {
      PB.toast((err && err.message) || "切换节点失败", "err");
    });
  }

  function onActions(event) {
    var target = event.target;
    if (!target || !target.closest) return;
    if (target.closest("#ndSort")) openSort(target.closest("#ndSort"));
    else if (target.closest("#ndDelay")) testDelayAll();
    else if (target.closest("#ndSpeed")) {
      if (speed.running) stopSpeed();
      else startSpeed();
    }
  }

  function onAcc(event) {
    var target = event.target;
    if (!target || !target.closest) return;
    var menu = target.closest("[data-menu]");
    if (menu) {
      openFilter(menu.getAttribute("data-menu"), menu);
      return;
    }
    var region = target.closest("[data-region]");
    if (region && document.getElementById("ndRegions") && document.getElementById("ndRegions").contains(region)) {
      var f = currentFilter();
      var key = region.getAttribute("data-region") || "all";
      f.region = key !== "all" && f.region === key ? "all" : key;
      persist();
      refreshView();
      return;
    }
    if (target.closest("#ndClear")) clearFilters();
  }

  function onPageClick(event) {
    var target = event.target;
    if (!target || !target.closest) return;
    var go = target.closest("[data-go]");
    if (go) {
      PB.goTab(go.getAttribute("data-go"));
      return;
    }
    if (target.closest("[data-clear-all]")) {
      clearFilters();
      return;
    }
    var chip = target.closest("[data-rm]");
    if (chip) {
      removeChip(chip.getAttribute("data-rm"));
      return;
    }
    var sort = target.closest("[data-sort]");
    if (sort) {
      var key = sort.getAttribute("data-sort");
      var f = currentFilter();
      if (f.sortKey === key) setSort(key, f.sortDir === 1 ? -1 : 1);
      else setSort(key, null);
      return;
    }
    var act = target.closest("[data-act]");
    var item = target.closest("[data-id]");
    if (!item) return;
    var name = item.getAttribute("data-id");
    if (act) {
      if (act.getAttribute("data-act") === "delay") testOne(name);
      else startSpeed(name);
      return;
    }
    selectNode(name);
  }

  function onPageKey(event) {
    if ((event.key !== "Enter" && event.key !== " ") || !event.target || !event.target.matches) return;
    if (!event.target.matches(".nd-card, .nd-tr")) return;
    event.preventDefault();
    selectNode(event.target.getAttribute("data-id"));
  }

  function bindSearch() {
    if (unbindQ) unbindQ();
    unbindQ = null;
    var input = document.getElementById("ndQ");
    var clear = document.getElementById("ndQClear");
    if (!input) return;
    input.value = currentFilter().query || "";
    if (clear) clear.hidden = !input.value;
    unbindQ = PB.onInput(input, function (value) {
      currentFilter().query = value;
      var btn = document.getElementById("ndQClear");
      if (btn) btn.hidden = !value;
      persist();
      refreshView();
    }, 120);
    if (clear) {
      clear.onclick = function () {
        input.value = "";
        input.dispatchEvent(new Event("input", { bubbles: true }));
        input.focus();
      };
    }
  }

  function mountChrome() {
    var ch = channel();
    PB.topbar({
      actions:
        '<button type="button" class="btn sm plain" id="ndSort" aria-haspopup="menu">' + PB.ic("sort") + '<span class="lbl" id="ndSortLbl">按延迟</span>' + PB.ic("chev-down", "xs") + "</button>" +
        PB.segHTML("ndView", [
          { v: "card", html: PB.ic("grid"), title: "卡片视图" },
          { v: "list", html: PB.ic("list"), title: "列表视图" }
        ], view, "sm icons") +
        '<button type="button" class="btn sm cl" id="ndDelay" title="测延迟"></button>' +
        '<button type="button" class="btn sm prominent" id="ndSpeed" title="测网速"></button>',
      acc:
        '<div class="acc-row">' +
        PB.segHTML("ndGroup", [
          { v: "daily", html: '<span class="ch-dot daily"></span>日常代理<span class="seg-badge" data-badge="daily" hidden></span>' },
          { v: "ai", html: '<span class="ch-dot ai"></span>AI 专属<span class="seg-badge" data-badge="ai" hidden></span>' }
        ], ch, "tinted") +
        '<label class="search">' + PB.ic("search") +
        '<input class="field" id="ndQ" data-search type="text" role="searchbox" placeholder="搜索节点、协议、地区  (Ctrl+F)" autocomplete="off" spellcheck="false" aria-label="搜索节点">' +
        '<button type="button" class="clear" id="ndQClear" aria-label="清空搜索" hidden>' + PB.ic("x") + "</button></label>" +
        '<span class="count" id="ndCount"></span></div>' +
        '<div class="acc-row" id="ndFilters"></div>'
    });
    PB.mountSeg(document.getElementById("ndGroup"), function (value) {
      if (value !== "daily" && value !== "ai") return;
      useChannel(value);
      regionSeen = "";
      bindSearch();
      var scroller = document.getElementById("scroll");
      if (scroller) scroller.scrollTop = 0;
      if (coreOn() && !(pools[value] && pools[value].length)) loading[value] = true;
      paintSort();
      paintFilters();
      paintActions();
      paintBadges();
      paintList();
      loadNodes();
    });
    PB.mountSeg(document.getElementById("ndView"), function (value) {
      view = value === "list" ? "list" : "card";
      paintList();
    });
    var actions = document.getElementById("tbActions");
    var acc = document.getElementById("tbAcc");
    if (actions) actions.onclick = onActions;
    if (acc) acc.onclick = onAcc;
    bindSearch();
    paintSort();
    paintFilters();
    paintActions();
    paintBadges();
  }

  function boot() {
    loadSubs().then(loadStatus).then(function () {
      if (!active) return;
      paintActions();
      paintBadges();
      if (coreOn()) return loadNodes();
      paintList();
    });
  }

  var tab = {
    enter: function (root) {
      active = true;
      useChannel(channel());
      sawCore = coreOn();
      root.innerHTML = '<div id="ndList"></div>';
      listEl = document.getElementById("ndList");
      root.addEventListener("click", onPageClick);
      root.addEventListener("keydown", onPageKey);
      mountChrome();
      if (coreOn() && !(pools[channel()] && pools[channel()].length)) loading[channel()] = true;
      paintList();
      boot();
    },
    leave: function () {
      active = false;
      listEl = null;
      if (unbindQ) {
        unbindQ();
        unbindQ = null;
      }
      var actions = document.getElementById("tbActions");
      var acc = document.getElementById("tbAcc");
      if (actions) actions.onclick = null;
      if (acc) acc.onclick = null;
    },
    onCore: function (status) {
      if (!active) return;
      var running = !!(status && status.running);
      paintActions();
      if (sawCore === running) return;
      sawCore = running;
      if (running) loadNodes();
      else paintList();
    },
    onSSE: function (msg) {
      if (!msg) return;
      if (msg.type === "speedtest") onSpeedMsg(msg);
      else if (msg.type === "sync" && active && coreOn() && !delayJob[channel()]) loadNodes();
    },
    paintForBench: function (next) {
      view = next === "list" ? "list" : "card";
      paintList();
    }
  };

  if (typeof PB.on === "function") {
    PB.on("reconnect", function () {
      if (speed.running) pullSpeed();
    });
  }

  PB.registerTab("nodes", tab);
})();
