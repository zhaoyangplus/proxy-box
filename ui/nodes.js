"use strict";

(function () {
  const GROUP_PROXY = "PROXY";
  const GROUP_AI = "🤖 AI-IDE";
  const DELAY_PRESETS = [
    ["all", "全部"],
    ["alive", "可用"],
    ["100", "≤100"],
    ["200", "≤200"],
    ["300", "≤300"],
    ["500", "≤500"],
    ["custom", "自定义"],
  ];
  const SPEED_PRESETS = [
    ["all", "全部"],
    ["tested", "已测"],
    ["1", "≥1"],
    ["5", "≥5"],
    ["10", "≥10"],
    ["20", "≥20"],
    ["custom", "自定义"],
  ];
  const TYPE_LABELS = {
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
    relay: "Relay",
  };

  const state = {
    mounted: false,
    visible: false,
    forced: false,
    phase: "stopped",
    group: GROUP_PROXY,
    view: "cards",
    filters: PBNodeFilter.loadFilters(browserStorage()),
    nodes: [],
    painted: [],
    totalCount: 0,
    aliveCount: 0,
    providerNames: Object.create(null),
    speedMem: Object.create(null),
    loading: false,
    refreshing: false,
    delayAll: false,
    testingDelay: "",
    error: "",
    gen: 0,
    scrollTop: 0,
    queued: null,
    speed: { phase: "idle", done: 0, total: 0, current: "", taskId: "" },
    speedTicket: 0,
  };

  const dom = {};
  const locks = new Set();

  function browserStorage() {
    try {
      return window.localStorage;
    } catch (err) {
      return null;
    }
  }

  function currentFilter() {
    return state.filters[state.group] || state.filters[GROUP_PROXY];
  }

  function saveFilters() {
    PBNodeFilter.saveFilters(browserStorage(), state.filters);
  }

  function num(v) {
    const n = Number(v);
    return Number.isFinite(n) ? n : 0;
  }

  function readPositive(raw) {
    const text = String(raw == null ? "" : raw).trim();
    if (!text) return 0;
    const n = Number(text);
    if (!Number.isFinite(n) || n <= 0) return 0;
    return n;
  }

  function locked(key, fn) {
    if (locks.has(key)) return Promise.resolve();
    locks.add(key);
    return Promise.resolve().then(fn).finally(function () {
      locks.delete(key);
    });
  }

  function toast(message, kind) {
    if (window.PB && typeof PB.toast === "function") PB.toast(message, kind);
  }

  function fmtMs(ms) {
    if (PB.fmt && typeof PB.fmt.ms === "function") return PB.fmt.ms(ms);
    return Math.round(ms) + " ms";
  }

  function fmtMbps(n) {
    if (PB.fmt && typeof PB.fmt.mbps === "function") return PB.fmt.mbps(n);
    const v = Number(n);
    return (Number.isFinite(v) ? v.toFixed(2) : "0.00") + " MB/s";
  }

  function coreRunning() {
    return state.phase === "running";
  }

  function readPhase() {
    const core = PB.state && PB.state.core;
    if (core && typeof core.running === "boolean") return core.running ? "running" : "stopped";
    return "stopped";
  }

  function normalizeNode(raw) {
    const n = raw || {};
    let speed = Number(n.speedMBps);
    if (!Number.isFinite(speed)) speed = 0;
    const node = {
      name: n.name != null ? String(n.name) : "",
      type: n.type != null ? String(n.type) : "",
      provider: n.provider != null ? String(n.provider) : "",
      delayMs: num(n.delayMs),
      untested: !!n.untested,
      alive: !!n.alive,
      isAuto: !!n.isAuto,
      selected: !!n.selected,
      speedMBps: speed,
      speedTested: !!n.speedTested,
      speedError: n.speedError != null ? String(n.speedError) : "",
      subName: "",
      typeLabel: "",
      region: "",
      regionLabel: "",
    };
    node.typeLabel = prettyType(node.type);
    if (!PBNodeFilter.isUtilityNode(node)) {
      node.region = PBNodeFilter.detectRegion(node.name);
      node.regionLabel = node.region ? PBNodeFilter.regionLabel(node.region) : "";
    }
    return node;
  }

  function normalizeNodes(list) {
    const raw = Array.isArray(list) ? list : [];
    const out = new Array(raw.length);
    for (let i = 0; i < raw.length; i++) out[i] = normalizeNode(raw[i]);
    return out;
  }

  function canTest(node) {
    return !!node && !PBNodeFilter.isUtilityNode(node);
  }

  function countsFrom(nodes) {
    let total = 0;
    let alive = 0;
    for (let i = 0; i < nodes.length; i++) {
      const node = nodes[i];
      if (PBNodeFilter.isUtilityNode(node)) continue;
      total++;
      if (!node.untested && node.delayMs > 0) alive++;
    }
    return { total: total, alive: alive };
  }

  function stampSubNames() {
    const map = state.providerNames;
    const nodes = state.nodes;
    for (let i = 0; i < nodes.length; i++) {
      const id = nodes[i].provider;
      nodes[i].subName = id && map[id] ? map[id] : "";
    }
  }

  function visibleNodes() {
    stampSubNames();
    const filter = currentFilter();
    return PBNodeFilter.sortNodes(PBNodeFilter.applyFilters(state.nodes, filter), filter);
  }

  function prettyType(type) {
    const raw = String(type || "");
    if (!raw) return "—";
    return TYPE_LABELS[raw.toLowerCase()] || raw;
  }

  function providerLabel(node) {
    if (!node.provider) return "—";
    return state.providerNames[node.provider] || node.provider;
  }

  function delayView(node) {
    if (state.testingDelay && state.testingDelay === node.name) {
      return { text: "测延迟…", cls: "pbn-delay-wait", title: "正在测延迟" };
    }
    if (node.untested) return { text: "未测速", cls: "pbn-delay-none", title: "" };
    if (!(node.delayMs > 0)) return { text: "超时", cls: "pbn-delay-dead", title: "超时" };
    const ms = Math.round(node.delayMs);
    let cls = "pbn-delay-slow";
    if (ms <= 200) cls = "pbn-delay-good";
    else if (ms <= 500) cls = "pbn-delay-mid";
    return { text: fmtMs(ms), cls: cls, title: fmtMs(ms) };
  }

  function speedView(node) {
    const phase = state.speed && state.speed.phase;
    const current = phase === "running" || phase === "starting" ? state.speed.current : "";
    if (current && node.name === current) return { text: "测速中", cls: "pbn-speed-pulse", title: "正在测网速" };
    if (node.speedError) return { text: "失败", cls: "pbn-speed-bad", title: node.speedError };
    if (!node.speedTested) return { text: "未测网速", cls: "pbn-speed-none", title: "" };
    const n = node.speedMBps;
    let cls = "pbn-speed-low";
    if (n >= 5) cls = "pbn-speed-good";
    else if (n >= 1) cls = "pbn-speed-mid";
    const text = fmtMbps(n);
    return { text: text, cls: cls, title: text };
  }

  function applySpeedMem(nodes) {
    for (let i = 0; i < nodes.length; i++) {
      const node = nodes[i];
      const mem = state.speedMem[node.name];
      if (node.speedTested) {
        state.speedMem[node.name] = {
          speedMBps: node.speedMBps,
          speedTested: true,
          speedError: node.speedError,
        };
      } else if (mem) {
        node.speedMBps = mem.speedMBps;
        node.speedTested = true;
        node.speedError = mem.speedError;
      }
    }
  }

  function applyList(data) {
    const payload = data || {};
    const nodes = normalizeNodes(payload.nodes);
    applySpeedMem(nodes);
    state.nodes = nodes;
    if (payload.totalCount != null) state.totalCount = num(payload.totalCount);
    else state.totalCount = countsFrom(nodes).total;
    if (payload.aliveCount != null) state.aliveCount = num(payload.aliveCount);
    else state.aliveCount = countsFrom(nodes).alive;
  }

  function iconBtn(act, label, glyph) {
    return PB.el("button", {
      class: "pbn-icon",
      text: glyph,
      attrs: { type: "button", "data-act": act, "aria-label": label, title: label },
    });
  }

  function cardTemplate() {
    const auto = PB.el("span", { class: "pbn-auto", text: "自动", hidden: true });
    const name = PBNodeFilter.buildNodeName(PB.el, "");
    const check = PB.el("span", { class: "pbn-check", text: "✓", hidden: true, attrs: { "aria-hidden": "true" } });
    const row1 = PB.el("div", { class: "pbn-line" }, auto, name, check);
    const region = PB.el("span", { class: "pbn-region", hidden: true });
    const proto = PB.el("span", { class: "pbn-proto" });
    const sub = PB.el("span", { class: "pbn-sub" });
    const row2 = PB.el("div", { class: "pbn-line" }, region, proto, sub);
    const delay = PB.el("span", { class: "pbn-delay" });
    const speed = PB.el("span", { class: "pbn-speed" });
    const actions = PB.el("span", { class: "pbn-actions" }, iconBtn("delay", "测延迟", "延"), iconBtn("speed", "测网速", "速"));
    const row3 = PB.el("div", { class: "pbn-line" }, delay, speed, actions);
    return PB.el("article", { class: "pbn-card" }, row1, row2, row3);
  }

  function rowTemplate() {
    const auto = PB.el("span", { class: "pbn-auto", text: "自动", hidden: true });
    const name = PBNodeFilter.buildNodeName(PB.el, "");
    const check = PB.el("span", { class: "pbn-check", text: "✓", hidden: true, attrs: { "aria-hidden": "true" } });
    const region = PB.el("span", { class: "pbn-region", hidden: true });
    const nameCell = PB.el("td", {}, PB.el("div", { class: "pbn-namecell" }, auto, name, check, region));
    const proto = PB.el("td", {}, PB.el("span", { class: "pbn-proto" }));
    const sub = PB.el("td", {}, PB.el("span", { class: "pbn-sub" }));
    const delay = PB.el("td", {}, PB.el("span", { class: "pbn-delay" }));
    const speed = PB.el("td", {}, PB.el("span", { class: "pbn-speed" }));
    const actions = PB.el("span", { class: "pbn-actions" }, iconBtn("delay", "测延迟", "延"), iconBtn("speed", "测网速", "速"));
    return PB.el("tr", { class: "pbn-row" }, nameCell, proto, sub, delay, speed, PB.el("td", {}, actions));
  }

  function sortButton(key, id) {
    const button = PB.el("button", {
      class: "pbn-sorthead",
      attrs: { type: "button", id: id },
      on: { click: function () { onSortKey(key); } },
    });
    return { th: PB.el("th", {}, button), button: button };
  }

  function groupButton(id, name, groupId) {
    return PB.el("button", {
      class: "pbn-group",
      attrs: { type: "button", id: id, title: name + " " + groupId },
      on: { click: function () { setGroup(groupId); } },
    }, PB.el("span", { class: "pbn-group-name", text: name }), PB.el("span", { class: "pbn-group-id", text: groupId }));
  }

  function presetButtons(kind, items) {
    const map = {};
    const bar = PB.el("div", {
      class: "pbn-pills",
      attrs: {
        role: "group",
        "data-kind": kind,
        "aria-label": kind === "delay" ? "延迟筛选" : "网速筛选",
      },
    });
    for (let i = 0; i < items.length; i++) {
      const preset = items[i][0];
      const button = PB.el("button", {
        class: "pbn-pill",
        text: items[i][1],
        attrs: { type: "button", "data-preset": preset },
        on: { click: function () { setPreset(kind, preset); } },
      });
      map[preset] = button;
      bar.appendChild(button);
    }
    return { bar: bar, map: map };
  }

  function mount(root) {
    if (state.mounted) return;
    state.phase = readPhase();
    root.classList.add("pbn");
    const bar = PB.el("div", { class: "pbn-bar" });
    dom.progressFill = PB.el("span");
    dom.progress = PB.el("div", { class: "pbn-progress", hidden: true }, dom.progressFill);
    dom.groupProxy = groupButton("pbnGroupProxy", "日常代理", GROUP_PROXY);
    dom.groupAI = groupButton("pbnGroupAI", "AI 专属", GROUP_AI);
    dom.count = PB.el("span", { class: "pbn-count", text: "可用 0 / 共 0" });
    dom.note = PB.el("span", { class: "pbn-note", hidden: true });
    dom.search = PB.el("input", {
      class: "pbn-search",
      attrs: {
        id: "pbnSearch",
        type: "text",
        placeholder: "搜索节点、协议、订阅或地区",
        autocomplete: "off",
        spellcheck: "false",
        "aria-label": "搜索节点",
      },
      on: { input: onSearch },
    });
    dom.searchClear = PB.el("button", {
      class: "pbn-search-clear",
      text: "×",
      attrs: { type: "button", "aria-label": "清空搜索", title: "清空搜索" },
      hidden: true,
      on: { click: clearSearch },
    });
    dom.optDelay = PB.el("option", { text: "延迟 ↑", attrs: { value: "delay" } });
    dom.optSpeed = PB.el("option", { text: "网速 ↓", attrs: { value: "speed" } });
    dom.optName = PB.el("option", { text: "名称", attrs: { value: "name" } });
    dom.sort = PB.el("select", {
      class: "pbn-sort",
      attrs: { id: "pbnSort", "aria-label": "排序" },
      on: { change: onSortPick },
    }, dom.optDelay, dom.optSpeed, dom.optName);
    dom.viewCards = PB.el("button", {
      class: "is-on",
      text: "卡片",
      attrs: { type: "button", id: "pbnViewCards", "aria-label": "卡片视图", title: "卡片视图" },
      on: { click: function () { setView("cards"); } },
    });
    dom.viewList = PB.el("button", {
      text: "列表",
      attrs: { type: "button", id: "pbnViewList", "aria-label": "列表视图", title: "列表视图" },
      on: { click: function () { setView("list"); } },
    });
    dom.delayAllBtn = PB.el("button", {
      class: "pbn-btn",
      text: "测延迟",
      attrs: { type: "button", id: "pbnDelayAll" },
      on: { click: function () { testAllDelay(); } },
    });
    dom.speedBtn = PB.el("button", {
      class: "pbn-btn",
      text: "全部测网速",
      attrs: { type: "button", id: "pbnSpeedAll" },
      on: { click: onSpeedButton },
    });
    const views = PB.el("div", { class: "pbn-views" }, dom.viewCards, dom.viewList);
    const groups = PB.el("div", { class: "pbn-groups" }, dom.groupProxy, dom.groupAI);
    const searchWrap = PB.el("div", { class: "pbn-searchwrap" }, dom.search, dom.searchClear);
    const row1 = PB.el("div", { class: "pbn-row1" },
      groups, dom.count, searchWrap, dom.sort, views, dom.delayAllBtn, dom.speedBtn);
    const delayPills = presetButtons("delay", DELAY_PRESETS);
    const speedPills = presetButtons("speed", SPEED_PRESETS);
    dom.delayPills = delayPills.map;
    dom.speedPills = speedPills.map;
    dom.sub = PB.el("select", {
      class: "pbn-sub",
      attrs: { id: "pbnSub", "aria-label": "订阅" },
      on: { change: onSub },
    }, PB.el("option", { text: "全部订阅", attrs: { value: "all" } }));
    dom.delayInput = PB.el("input", {
      class: "pbn-num",
      attrs: {
        id: "pbnDelayMax",
        type: "text",
        inputmode: "numeric",
        autocomplete: "off",
        spellcheck: "false",
        "aria-label": "自定义最大延迟",
      },
      on: { input: onDelayInput },
    });
    dom.speedInput = PB.el("input", {
      class: "pbn-num",
      attrs: {
        id: "pbnSpeedMin",
        type: "text",
        inputmode: "decimal",
        autocomplete: "off",
        spellcheck: "false",
        "aria-label": "自定义最低网速",
      },
      on: { input: onSpeedInput },
    });
    dom.reset = PB.el("button", {
      class: "pbn-btn pbn-reset",
      text: "重置筛选",
      attrs: { type: "button", id: "pbnReset" },
      hidden: true,
      on: { click: resetFilters },
    });
    dom.aiHint = PB.el("span", {
      class: "pbn-ai-hint",
      text: "已自动过滤受限地区",
      hidden: true,
    });
    const row2 = PB.el("div", { class: "pbn-row2" },
      dom.sub,
      delayPills.bar,
      PB.el("label", { class: "pbn-custom" }, "≤", dom.delayInput, "ms"),
      speedPills.bar,
      PB.el("label", { class: "pbn-custom" }, "≥", dom.speedInput, "MB/s"),
      dom.reset,
      dom.aiHint);
    dom.regions = PB.el("div", {
      class: "pbn-regions",
      attrs: { role: "group", "aria-label": "地区" },
    });
    dom.regions.addEventListener("click", onRegionClick);
    dom.speedHint = PB.el("span", { class: "pbn-hint-limit", text: "单节点限 10MB/6s", hidden: true });
    dom.hints = PB.el("div", { class: "pbn-hints" }, dom.note, dom.speedHint);
    bar.appendChild(dom.progress);
    bar.appendChild(row1);
    bar.appendChild(row2);
    bar.appendChild(dom.regions);
    bar.appendChild(dom.hints);

    dom.body = PB.el("div", { class: "pbn-body" });
    dom.body.addEventListener("scroll", function () {
      state.scrollTop = dom.body.scrollTop;
    }, { passive: true });
    dom.loading = PB.el("div", { class: "pbn-loading", text: "正在读取节点…", hidden: true });
    dom.error = PB.el("div", { class: "pbn-error", hidden: true });
    dom.emptyText = PB.el("p", { text: "" });
    dom.emptyStart = PB.el("button", {
      class: "pbn-btn",
      text: "去总览启动",
      attrs: { type: "button", id: "pbnGoOverview" },
      hidden: true,
      on: { click: function () { PB.go("overview"); } },
    });
    dom.emptySubs = PB.el("button", {
      class: "pbn-btn",
      text: "去订阅管理",
      attrs: { type: "button", id: "pbnGoSubs" },
      hidden: true,
      on: { click: function () { PB.go("subs"); } },
    });
    dom.emptyClear = PB.el("button", {
      class: "pbn-btn pbn-reset",
      text: "清空筛选",
      attrs: { type: "button", id: "pbnEmptyClear" },
      hidden: true,
      on: { click: resetFilters },
    });
    dom.empty = PB.el("div", { class: "pbn-empty", hidden: true }, dom.emptyText, dom.emptyClear, dom.emptyStart, dom.emptySubs);
    dom.grid = PB.el("div", { class: "pbn-grid", attrs: { id: "pbnGrid" } });
    dom.grid.addEventListener("click", onItemClick);
    const nameSort = sortButton("name", "pbnSortName");
    const delaySort = sortButton("delay", "pbnSortDelay");
    const speedSort = sortButton("speed", "pbnSortSpeed");
    dom.sortName = nameSort.button;
    dom.sortDelay = delaySort.button;
    dom.sortSpeed = speedSort.button;
    const head = PB.el("tr", {},
      nameSort.th,
      PB.el("th", { class: "pbn-static", text: "协议" }),
      PB.el("th", { class: "pbn-static", text: "订阅" }),
      delaySort.th,
      speedSort.th,
      PB.el("th", { class: "pbn-static", text: "操作" }));
    dom.listBody = PB.el("tbody", { attrs: { id: "pbnListBody" } });
    dom.listBody.addEventListener("click", onItemClick);
    const table = PB.el("table", { class: "pbn-table" }, PB.el("thead", {}, head), dom.listBody);
    dom.list = PB.el("div", { class: "pbn-table-wrap", attrs: { id: "pbnList" }, hidden: true }, table);
    dom.body.appendChild(dom.loading);
    dom.body.appendChild(dom.error);
    dom.body.appendChild(dom.empty);
    dom.body.appendChild(dom.grid);
    dom.body.appendChild(dom.list);
    root.appendChild(bar);
    root.appendChild(dom.body);

    dom.cardTpl = cardTemplate();
    dom.rowTpl = rowTemplate();
    state.mounted = true;
    PB.on("core", onCoreEvent);
    PB.on("subs", function () { loadProviderNames(); });
    PB.on("speedtest", onSpeedEvent);

    if (state.queued) {
      const queued = state.queued;
      state.queued = null;
      render(queued);
      return;
    }
    paint();
  }

  function show() {
    state.visible = true;
    state.phase = readPhase();
    restoreSpeed();
    loadProviderNames();
    if (state.forced) {
      paint();
      return;
    }
    if (!coreRunning()) {
      clearStopped();
      return;
    }
    loadNodes();
  }

  function hide() {
    state.visible = false;
  }

  function onCoreChange(status) {
    if (!status) return;
    const running = !!status.running;
    if (!running) {
      state.phase = "stopped";
      if (!state.forced) clearStopped();
      return;
    }
    const was = state.phase;
    state.phase = "running";
    if (was !== "running" && !state.forced) loadNodes();
    else paint();
  }

  function onCoreEvent(msg) {
    const core = msg && msg.core;
    if (!core) return;
    if (core.phase === "running") {
      onCoreChange({ running: true });
      return;
    }
    if (core.phase === "download") {
      state.phase = "download";
      state.loading = false;
      state.delayAll = false;
      paint();
      return;
    }
    if (core.phase === "stopped" || core.phase === "error") onCoreChange({ running: false });
  }

  function clearStopped() {
    state.gen++;
    state.nodes = [];
    state.painted = [];
    state.totalCount = 0;
    state.aliveCount = 0;
    state.loading = false;
    state.refreshing = false;
    state.delayAll = false;
    state.testingDelay = "";
    state.error = "";
    idleSpeed();
    paint();
  }

  function idleSpeed() {
    state.speed = { phase: "idle", done: 0, total: 0, current: "", taskId: "" };
  }

  function loadProviderNames() {
    return PB.api("GET", "/api/subs").then(function (data) {
      const subs = data && Array.isArray(data.subscriptions) ? data.subscriptions : [];
      const map = Object.create(null);
      for (let i = 0; i < subs.length; i++) {
        const sub = subs[i] || {};
        if (sub.id) map[String(sub.id)] = String(sub.name || sub.id);
      }
      state.providerNames = map;
      if (state.mounted && state.nodes.length) paint();
    }).catch(function () {});
  }

  function loadNodes() {
    if (state.forced) return Promise.resolve();
    if (!coreRunning()) {
      clearStopped();
      return Promise.resolve();
    }
    const gen = ++state.gen;
    const first = state.nodes.length === 0;
    state.loading = first;
    state.refreshing = !first;
    state.error = "";
    paint();
    return PB.api("POST", "/api/nodes/list", {
      group: state.group,
      client: "builtin",
      testDelay: false,
    }).then(function (data) {
      if (gen !== state.gen) return;
      applyList(data);
    }).catch(function (err) {
      if (gen !== state.gen) return;
      state.error = (err && err.message) || "读取节点失败";
      if (first) state.nodes = [];
    }).then(function () {
      if (gen !== state.gen) return;
      state.loading = false;
      state.refreshing = false;
      paint();
    });
  }

  function setGroup(id) {
    if (state.group === id) return;
    state.group = id;
    state.forced = false;
    state.nodes = [];
    state.painted = [];
    state.totalCount = 0;
    state.aliveCount = 0;
    state.scrollTop = 0;
    if (coreRunning()) loadNodes();
    else paint();
  }

  function onSearch() {
    currentFilter().query = String(dom.search.value || "");
    saveFilters();
    state.scrollTop = 0;
    paint();
  }

  function clearSearch() {
    currentFilter().query = "";
    saveFilters();
    state.scrollTop = 0;
    paint();
    dom.search.focus();
  }

  function onSub() {
    currentFilter().sub = dom.sub.value || "all";
    saveFilters();
    state.scrollTop = 0;
    paint();
  }

  function onDelayInput() {
    const filter = currentFilter();
    filter.delayPreset = "custom";
    filter.maxDelayMs = readPositive(dom.delayInput.value);
    saveFilters();
    state.scrollTop = 0;
    paint();
  }

  function onSpeedInput() {
    const filter = currentFilter();
    filter.speedPreset = "custom";
    filter.minSpeedMBps = readPositive(dom.speedInput.value);
    saveFilters();
    state.scrollTop = 0;
    paint();
  }

  function setPreset(kind, preset) {
    const filter = currentFilter();
    if (kind === "delay") {
      filter.delayPreset = preset;
      if (preset === "100" || preset === "200" || preset === "300" || preset === "500") filter.maxDelayMs = Number(preset);
      else if (preset !== "custom") filter.maxDelayMs = 0;
    } else {
      filter.speedPreset = preset;
      if (preset === "1" || preset === "5" || preset === "10" || preset === "20") filter.minSpeedMBps = Number(preset);
      else if (preset !== "custom") filter.minSpeedMBps = 0;
    }
    saveFilters();
    state.scrollTop = 0;
    paint();
    if (preset === "custom") {
      if (kind === "delay") dom.delayInput.focus();
      else dom.speedInput.focus();
    }
  }

  function resetFilters() {
    const prev = currentFilter();
    const next = PBNodeFilter.defaultGroupFilter();
    next.sortKey = prev.sortKey;
    next.sortDir = prev.sortDir;
    state.filters[state.group] = next;
    saveFilters();
    state.scrollTop = 0;
    paint();
  }

  function onRegionClick(ev) {
    const button = ev.target.closest("[data-region]");
    if (!button || !dom.regions.contains(button)) return;
    currentFilter().region = button.getAttribute("data-region") || "all";
    saveFilters();
    state.scrollTop = 0;
    paint();
  }

  function onSortKey(key) {
    const filter = currentFilter();
    if (filter.sortKey === key) filter.sortDir = filter.sortDir === 1 ? -1 : 1;
    else {
      filter.sortKey = key;
      filter.sortDir = PBNodeFilter.defaultSortDir(key);
    }
    saveFilters();
    state.scrollTop = 0;
    paint();
  }

  function onSortPick() {
    const key = dom.sort.value;
    if (key !== "delay" && key !== "speed" && key !== "name") return;
    const filter = currentFilter();
    if (filter.sortKey === key) return;
    filter.sortKey = key;
    filter.sortDir = PBNodeFilter.defaultSortDir(key);
    saveFilters();
    state.scrollTop = 0;
    paint();
  }

  function setView(next) {
    state.view = next === "list" ? "list" : "cards";
    if (state.mounted) paint();
  }

  function render(list) {
    if (!state.mounted) {
      state.queued = list;
      return;
    }
    state.gen++;
    state.forced = true;
    state.loading = false;
    state.refreshing = false;
    state.delayAll = false;
    state.testingDelay = "";
    state.error = "";
    const data = Array.isArray(list) ? { nodes: list } : (list || {});
    applyList(data);
    paint();
  }

  function paint() {
    if (!state.mounted) return;
    const rows = visibleNodes();
    state.painted = rows;
    paintBar(rows);
    paintBody(rows);
  }

  function paintPills(map, preset) {
    const keys = Object.keys(map);
    for (let i = 0; i < keys.length; i++) {
      const on = keys[i] === preset;
      map[keys[i]].classList.toggle("is-on", on);
      map[keys[i]].setAttribute("aria-pressed", on ? "true" : "false");
    }
  }

  function subChoices() {
    const seen = Object.create(null);
    const ids = [];
    function add(id, label) {
      const key = String(id || "");
      if (!key || seen[key]) return;
      seen[key] = label || key;
      ids.push(key);
    }
    const names = state.providerNames;
    const keys = Object.keys(names);
    keys.sort();
    for (let i = 0; i < keys.length; i++) add(keys[i], names[keys[i]]);
    for (let n = 0; n < state.nodes.length; n++) {
      const node = state.nodes[n];
      add(node.provider, node.subName || node.provider);
    }
    const filter = currentFilter();
    if (filter.sub !== "all") add(filter.sub, names[filter.sub] || filter.sub);
    return { ids: ids, seen: seen };
  }

  function paintSubs(filter) {
    const choice = subChoices();
    const sig = choice.ids.map(function (id) { return id + "=" + choice.seen[id]; }).join("|");
    if (sig !== dom.subSig) {
      const frag = document.createDocumentFragment();
      frag.appendChild(PB.el("option", { text: "全部订阅", attrs: { value: "all" } }));
      for (let i = 0; i < choice.ids.length; i++) {
        const id = choice.ids[i];
        frag.appendChild(PB.el("option", { text: choice.seen[id], attrs: { value: id } }));
      }
      dom.sub.replaceChildren(frag);
      dom.subSig = sig;
    }
    const known = filter.sub === "all" || !!choice.seen[filter.sub];
    if (document.activeElement !== dom.sub) dom.sub.value = known ? filter.sub : "all";
  }

  function regionChip(key, label, on) {
    return PB.el("button", {
      class: "pbn-chip" + (on ? " is-on" : ""),
      text: label,
      attrs: { type: "button", "data-region": key, "aria-pressed": on ? "true" : "false" },
    });
  }

  function paintRegions(filter) {
    const counts = PBNodeFilter.regionCounts(state.nodes);
    const order = PBNodeFilter.REGION_ORDER;
    const keys = [];
    let total = 0;
    for (let i = 0; i < order.length; i++) {
      const count = counts[order[i]] || 0;
      if (count > 0) {
        keys.push(order[i]);
        total += count;
      }
    }
    const sig = filter.region + "|" + total + "|" + keys.map(function (key) {
      return key + ":" + counts[key];
    }).join(",");
    if (sig === dom.regionSig) return;
    dom.regionSig = sig;
    const frag = document.createDocumentFragment();
    frag.appendChild(regionChip("all", "全部 " + total, filter.region === "all"));
    for (let i = 0; i < keys.length; i++) {
      const key = keys[i];
      frag.appendChild(regionChip(key, PBNodeFilter.regionLabel(key) + " " + counts[key], filter.region === key));
    }
    dom.regions.replaceChildren(frag);
  }

  function paintBar(rows) {
    const list = rows || visibleNodes();
    const filter = currentFilter();
    const running = coreRunning();
    const active = PBNodeFilter.isFilterActive(filter);
    dom.groupProxy.classList.toggle("is-on", state.group === GROUP_PROXY);
    dom.groupAI.classList.toggle("is-on", state.group === GROUP_AI);
    dom.groupProxy.setAttribute("aria-pressed", state.group === GROUP_PROXY ? "true" : "false");
    dom.groupAI.setAttribute("aria-pressed", state.group === GROUP_AI ? "true" : "false");
    dom.count.textContent = active
      ? "命中 " + list.length + " / 共 " + state.totalCount
      : "可用 " + state.aliveCount + " / 共 " + state.totalCount;
    const busyNote = state.delayAll || (state.refreshing && !state.loading);
    dom.note.hidden = !busyNote;
    dom.note.textContent = state.delayAll ? "正在测延迟…" : (busyNote ? "正在刷新…" : "");
    if (document.activeElement !== dom.search) dom.search.value = filter.query;
    dom.searchClear.hidden = !String(filter.query || "").length;
    if (document.activeElement !== dom.sort) dom.sort.value = filter.sortKey;
    const arrow = filter.sortDir > 0 ? " ↑" : " ↓";
    dom.optName.textContent = "名称" + (filter.sortKey === "name" ? arrow : "");
    dom.optDelay.textContent = "延迟" + (filter.sortKey === "delay" ? arrow : "");
    dom.optSpeed.textContent = "网速" + (filter.sortKey === "speed" ? arrow : "");
    dom.sortName.textContent = "名称" + (filter.sortKey === "name" ? arrow : "");
    dom.sortDelay.textContent = "延迟" + (filter.sortKey === "delay" ? arrow : "");
    dom.sortSpeed.textContent = "网速" + (filter.sortKey === "speed" ? arrow : "");
    dom.sortName.classList.toggle("is-on", filter.sortKey === "name");
    dom.sortDelay.classList.toggle("is-on", filter.sortKey === "delay");
    dom.sortSpeed.classList.toggle("is-on", filter.sortKey === "speed");
    dom.viewCards.classList.toggle("is-on", state.view === "cards");
    dom.viewList.classList.toggle("is-on", state.view === "list");
    dom.viewCards.setAttribute("aria-pressed", state.view === "cards" ? "true" : "false");
    dom.viewList.setAttribute("aria-pressed", state.view === "list" ? "true" : "false");
    dom.delayAllBtn.textContent = state.delayAll ? "测延迟中" : "测延迟";
    dom.delayAllBtn.disabled = state.delayAll || !running;
    paintPills(dom.delayPills, filter.delayPreset);
    paintPills(dom.speedPills, filter.speedPreset);
    if (document.activeElement !== dom.delayInput) {
      const delayN = filter.delayPreset === "all" || filter.delayPreset === "alive" ? 0 : PBNodeFilter.delayThreshold(filter);
      dom.delayInput.value = delayN > 0 ? String(delayN) : "";
    }
    if (document.activeElement !== dom.speedInput) {
      const speedN = filter.speedPreset === "all" || filter.speedPreset === "tested" ? 0 : PBNodeFilter.speedThreshold(filter);
      dom.speedInput.value = speedN > 0 ? String(speedN) : "";
    }
    paintSubs(filter);
    paintRegions(filter);
    dom.reset.hidden = !active;
    dom.aiHint.hidden = state.group !== GROUP_AI;
    paintSpeedButton(running, list);
    const speedOn = state.speed && state.speed.phase !== "idle";
    dom.speedHint.hidden = !speedOn;
    dom.hints.hidden = dom.note.hidden && dom.speedHint.hidden;
  }

  function emptyCopy(rows) {
    const filter = currentFilter();
    if (PBNodeFilter.isFilterActive(filter) && rows.length === 0 && state.nodes.length > 0) {
      const reason = PBNodeFilter.emptyReason(state.nodes, filter) || "没有同时满足当前筛选条件的节点";
      return { text: reason, start: false, subs: false, clear: true };
    }
    if (!coreRunning() && !state.forced) {
      if (state.phase === "download") return { text: "内核正在启动，完成后可以查看节点。", start: false, subs: false, clear: false };
      return { text: "内核未运行。启动内核后可以查看和切换节点。", start: true, subs: false, clear: false };
    }
    return { text: "这个分组里还没有节点。先更新订阅。", start: false, subs: true, clear: false };
  }

  function paintBody(rows) {
    const list = rows || visibleNodes();
    state.painted = list;
    const showRows = list.length > 0 && (coreRunning() || state.forced);
    dom.loading.hidden = !state.loading;
    dom.error.hidden = !state.error;
    dom.error.textContent = state.error || "";
    const copy = emptyCopy(list);
    const showEmpty = !state.loading && !showRows;
    dom.empty.hidden = !showEmpty;
    dom.empty.classList.toggle("is-filter", !!(showEmpty && copy.clear));
    if (showEmpty) dom.emptyText.textContent = copy.text;
    dom.emptyClear.hidden = !showEmpty || !copy.clear;
    dom.emptyStart.hidden = !showEmpty || !copy.start;
    dom.emptySubs.hidden = !showEmpty || !copy.subs;
    const cards = showRows && state.view === "cards";
    const listView = showRows && state.view === "list";
    dom.grid.hidden = !cards;
    dom.list.hidden = !listView;
    if (cards) fillCards(list);
    else dom.grid.replaceChildren();
    if (listView) fillRows(list);
    else dom.listBody.replaceChildren();
    if (state.scrollTop) dom.body.scrollTop = state.scrollTop;
  }

  function paintItem(node, nameWrap, regionEl, protoEl, subEl, delayEl, speedEl, actions, selectedHost) {
    nameWrap.children[0].hidden = !node.isAuto;
    nameWrap.children[1].textContent = node.name;
    nameWrap.children[2].hidden = !node.selected;
    const label = node.regionLabel || "";
    regionEl.hidden = !label;
    regionEl.textContent = label;
    regionEl.title = label;
    protoEl.textContent = prettyType(node.type);
    subEl.textContent = providerLabel(node);
    const delay = delayView(node);
    delayEl.className = "pbn-delay " + delay.cls;
    delayEl.textContent = delay.text;
    delayEl.title = delay.title || "";
    const speed = speedView(node);
    speedEl.className = "pbn-speed " + speed.cls;
    speedEl.textContent = speed.text;
    speedEl.title = speed.title || "";
    const allow = canTest(node);
    const delayBtn = actions.children[0];
    delayBtn.hidden = !allow;
    delayBtn.disabled = !allow || state.testingDelay === node.name || !coreRunning();
    if (actions.children[1]) {
      const speedBtn = actions.children[1];
      speedBtn.hidden = !allow;
      speedBtn.disabled = !allow || !coreRunning() || (state.speed && state.speed.phase !== "idle");
    }
    selectedHost.classList.toggle("is-selected", !!node.selected);
  }

  function fillCards(rows) {
    const frag = document.createDocumentFragment();
    const tpl = dom.cardTpl;
    for (let i = 0; i < rows.length; i++) {
      const card = tpl.cloneNode(true);
      card.setAttribute("data-i", String(i));
      paintItem(
        rows[i],
        card.children[0],
        card.children[1].children[0],
        card.children[1].children[1],
        card.children[1].children[2],
        card.children[2].children[0],
        card.children[2].children[1],
        card.children[2].children[2],
        card
      );
      frag.appendChild(card);
    }
    dom.grid.replaceChildren(frag);
  }

  function fillRows(rows) {
    const frag = document.createDocumentFragment();
    const tpl = dom.rowTpl;
    for (let i = 0; i < rows.length; i++) {
      const tr = tpl.cloneNode(true);
      tr.setAttribute("data-i", String(i));
      const cluster = tr.children[0].children[0];
      paintItem(
        rows[i],
        cluster,
        cluster.children[3],
        tr.children[1].children[0],
        tr.children[2].children[0],
        tr.children[3].children[0],
        tr.children[4].children[0],
        tr.children[5].children[0],
        tr
      );
      frag.appendChild(tr);
    }
    dom.listBody.replaceChildren(frag);
  }

  function onItemClick(ev) {
    const host = ev.target.closest("[data-i]");
    if (!host) return;
    const node = state.painted[Number(host.getAttribute("data-i"))];
    if (!node) return;
    const act = ev.target.closest("[data-act]");
    if (act) {
      if (act.getAttribute("data-act") === "delay") testDelay(node);
      else if (act.getAttribute("data-act") === "speed") testSpeedOne(node);
      return;
    }
    selectNode(node);
  }

  function testAllDelay() {
    if (!coreRunning()) {
      state.error = "内核未运行";
      paint();
      return;
    }
    return locked("delayAll", function () {
      const gen = ++state.gen;
      state.delayAll = true;
      state.error = "";
      state.forced = false;
      paintBar();
      return PB.api("POST", "/api/nodes/list", {
        group: state.group,
        client: "builtin",
        testDelay: true,
      }).then(function (data) {
        if (gen !== state.gen) return;
        applyList(data);
      }).catch(function (err) {
        if (gen !== state.gen) return;
        state.error = (err && err.message) || "测延迟失败";
      }).then(function () {
        if (gen !== state.gen) return;
        state.delayAll = false;
        state.loading = false;
        state.refreshing = false;
        paint();
      });
    });
  }

  function testDelay(node) {
    if (!canTest(node)) return;
    if (!coreRunning()) {
      state.error = "内核未运行";
      paint();
      return;
    }
    return locked("delay:" + node.name, function () {
      state.testingDelay = node.name;
      paint();
      const name = node.name;
      return PB.api("POST", "/api/nodes/test", {
        client: "builtin",
        nodeName: name,
      }).then(function (data) {
        let live = node;
        for (let i = 0; i < state.nodes.length; i++) {
          if (state.nodes[i].name === name) live = state.nodes[i];
        }
        live.untested = false;
        live.delayMs = num(data && data.delayMs);
        live.alive = live.delayMs > 0;
        state.aliveCount = countsFrom(state.nodes).alive;
        state.error = "";
      }).catch(function (err) {
        state.error = (err && err.message) || "测延迟失败";
      }).then(function () {
        state.testingDelay = "";
        paint();
      });
    });
  }

  function selectNode(node) {
    if (!node || node.selected) return;
    if (!coreRunning()) {
      state.error = "内核未运行";
      paint();
      return;
    }
    return locked("select", function () {
      return PB.api("POST", "/api/nodes/select", {
        group: state.group,
        client: "builtin",
        nodeName: node.name,
      }).then(function () {
        for (let i = 0; i < state.nodes.length; i++) {
          state.nodes[i].selected = state.nodes[i].name === node.name;
        }
        state.error = "";
        paint();
      }).catch(function (err) {
        state.error = (err && err.message) || "切换节点失败";
        toast(state.error, "err");
        paint();
      });
    });
  }

  function paintSpeedButton(running, rows) {
    const speed = state.speed;
    const phase = speed.phase;
    const active = phase === "running" || phase === "stopping" || phase === "starting";
    const filtering = PBNodeFilter.isFilterActive(currentFilter());
    const matched = rows ? rows.length : 0;
    dom.speedBtn.classList.toggle("is-stop", active);
    if (active) {
      dom.speedBtn.textContent = "停止 (" + speed.done + "/" + speed.total + ")";
    } else if (filtering) {
      dom.speedBtn.textContent = "测当前筛选 (" + matched + ")";
    } else {
      dom.speedBtn.textContent = "全部测网速";
    }
    const noMatch = !active && filtering && matched === 0;
    dom.speedBtn.disabled = phase === "stopping" || state.delayAll || (!running && phase === "idle") || noMatch;
    const showBar = phase === "running" || phase === "starting" || phase === "stopping";
    dom.progress.hidden = !showBar;
    const pct = speed.total > 0 ? Math.max(0, Math.min(100, (speed.done / speed.total) * 100)) : (phase === "starting" ? 8 : 0);
    dom.progressFill.style.width = pct + "%";
  }

  function onSpeedButton() {
    const phase = state.speed.phase;
    if (phase === "running" || phase === "starting") stopSpeed();
    else if (phase === "idle") {
      stampSubNames();
      const names = PBNodeFilter.speedTestNames(state.nodes, currentFilter());
      if (names && !names.length) return;
      startSpeed(names);
    }
  }

  function startSpeed(names) {
    if (!coreRunning()) {
      state.error = "内核未运行";
      paint();
      return;
    }
    if (state.speed.phase === "running" || state.speed.phase === "starting" || state.speed.phase === "stopping") {
      toast("正在测网速", "warn");
      return;
    }
    return locked("speed", function () {
      const ticket = ++state.speedTicket;
      state.speed.phase = "starting";
      state.speed.done = 0;
      state.speed.total = 0;
      state.speed.current = names && names.length === 1 ? String(names[0]) : "";
      state.error = "";
      paintBar();
      const body = { group: state.group };
      if (names && names.length) body.nodes = names;
      return PB.api("POST", "/api/speedtest/start", body).then(function (data) {
        if (ticket !== state.speedTicket) return;
        state.speed.phase = "running";
        state.speed.taskId = data && data.taskId ? String(data.taskId) : "";
        state.speed.total = num(data && data.total);
        state.speed.done = 0;
        if (!(names && names.length === 1)) state.speed.current = "";
      }).catch(function (err) {
        if (ticket !== state.speedTicket) return;
        state.speed.phase = "idle";
        state.error = (err && err.message) || "测网速失败";
        toast(state.error, "err");
      }).then(function () {
        if (ticket !== state.speedTicket) return;
        paint();
      });
    });
  }

  function testSpeedOne(node) {
    if (!canTest(node)) return;
    startSpeed([node.name]);
  }

  function stopSpeed() {
    if (!state.speed || state.speed.phase === "idle") return;
    return locked("speedStop", function () {
      const ticket = ++state.speedTicket;
      state.speed.phase = "stopping";
      paintBar();
      return PB.api("POST", "/api/speedtest/stop").catch(function (err) {
        state.error = (err && err.message) || "停止测速失败";
        toast(state.error, "err");
      }).then(function () {
        if (ticket !== state.speedTicket) return;
        state.speed.phase = "idle";
        state.speed.current = "";
        return restoreSpeed();
      });
    });
  }

  function mergeResult(raw) {
    if (!raw) return;
    const name = raw.nodeName != null ? String(raw.nodeName) : "";
    if (!name) return;
    const speedError = raw.error != null ? String(raw.error) : "";
    let speedMBps = Number(raw.speedMBps);
    if (!Number.isFinite(speedMBps)) speedMBps = 0;
    state.speedMem[name] = { speedMBps: speedMBps, speedTested: true, speedError: speedError };
    for (let i = 0; i < state.nodes.length; i++) {
      if (state.nodes[i].name !== name) continue;
      state.nodes[i].speedMBps = speedMBps;
      state.nodes[i].speedTested = true;
      state.nodes[i].speedError = speedError;
      break;
    }
  }

  function applyResultMap(results) {
    if (!results) return;
    const names = Object.keys(results);
    for (let i = 0; i < names.length; i++) {
      const raw = results[names[i]];
      if (!raw) continue;
      mergeResult({
        nodeName: raw.nodeName || names[i],
        speedMBps: raw.speedMBps,
        error: raw.error || "",
      });
    }
  }

  function applyProgress(progress) {
    if (!progress) return;
    if (!state.speed) idleSpeed();
    if (progress.lastResult) mergeResult(progress.lastResult);
    state.speed.done = num(progress.done);
    state.speed.total = num(progress.total);
    state.speed.current = progress.currentNode ? String(progress.currentNode) : "";
    if (progress.taskId) state.speed.taskId = String(progress.taskId);
    if (state.speed.phase === "stopping" && progress.running) return;
    state.speed.phase = progress.running ? "running" : "idle";
    if (!progress.running) state.speed.current = "";
  }

  function onSpeedEvent(msg) {
    const progress = msg && msg.speedtest;
    if (!progress) return;
    applyProgress(progress);
    paint();
  }

  function restoreSpeed() {
    return PB.api("GET", "/api/speedtest/status").then(function (data) {
      const progress = data && data.progress && typeof data.progress === "object" ? data.progress : {};
      applyResultMap(data && data.results);
      if (state.speed.phase === "starting" && !progress.running) {
        paint();
        return;
      }
      if (state.speed.phase === "stopping") state.speed.phase = "idle";
      applyProgress(progress);
      paint();
    }).catch(function () {
      if (state.speed.phase === "starting") {
        paint();
        return;
      }
      state.speed.phase = "idle";
      state.speed.current = "";
      paint();
    });
  }


  window.PBNodes = {
    render: render,
    setView: setView,
  };

  PB.registerTab("nodes", {
    mount: mount,
    show: show,
    hide: hide,
    onCoreChange: onCoreChange,
  });
})();
