"use strict";

(function () {
  const GROUP_PROXY = "PROXY";
  const GROUP_AI = "🤖 AI-IDE";
  const SORT_DIR = { delay: 1, speed: -1, name: 1 };
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
    query: "",
    queryFold: "",
    sort: { key: "delay", dir: 1 },
    view: "cards",
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

  function num(v) {
    const n = Number(v);
    return Number.isFinite(n) ? n : 0;
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
    return {
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
    };
  }

  function normalizeNodes(list) {
    const raw = Array.isArray(list) ? list : [];
    const out = new Array(raw.length);
    for (let i = 0; i < raw.length; i++) out[i] = normalizeNode(raw[i]);
    return out;
  }

  function isDirectReject(node) {
    const name = String(node.name || "").toUpperCase();
    if (name === "DIRECT" || name === "REJECT") return true;
    const type = String(node.type || "").toLowerCase();
    return type === "direct" || type === "reject" || type === "rejectdrop";
  }

  function canTest(node) {
    return !!node && !node.isAuto && !isDirectReject(node);
  }

  function countsFrom(nodes) {
    let total = 0;
    let alive = 0;
    for (let i = 0; i < nodes.length; i++) {
      const node = nodes[i];
      if (node.isAuto || isDirectReject(node)) continue;
      total++;
      if (!node.untested && node.delayMs > 0) alive++;
    }
    return { total: total, alive: alive };
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

  function rankDelay(node) {
    if (node.untested) return 2;
    if (!(node.delayMs > 0)) return 1;
    return 0;
  }

  function rankSpeed(node) {
    if (!node.speedTested) return 2;
    if (node.speedError) return 1;
    return 0;
  }

  function comparePair(a, b) {
    if (a.isAuto !== b.isAuto) return a.isAuto ? -1 : 1;
    const dir = state.sort.dir;
    if (state.sort.key === "delay") {
      const ra = rankDelay(a);
      const rb = rankDelay(b);
      if (ra !== rb) return ra - rb;
      if (ra === 0) return (a.delayMs - b.delayMs) * dir;
      return 0;
    }
    if (state.sort.key === "speed") {
      const ra = rankSpeed(a);
      const rb = rankSpeed(b);
      if (ra !== rb) return ra - rb;
      if (ra === 0) return (a.speedMBps - b.speedMBps) * dir;
      return 0;
    }
    return String(a.name).localeCompare(String(b.name), "zh") * dir;
  }

  function ordered(list) {
    const copy = new Array(list.length);
    for (let i = 0; i < list.length; i++) copy[i] = { n: list[i], i: i };
    copy.sort(function (a, b) {
      return comparePair(a.n, b.n) || a.i - b.i;
    });
    const out = new Array(copy.length);
    for (let i = 0; i < copy.length; i++) out[i] = copy[i].n;
    return out;
  }

  function matches(node, q) {
    if (!q) return true;
    const proto = prettyType(node.type).toLowerCase();
    const sub = providerLabel(node).toLowerCase();
    if (node.name.toLowerCase().indexOf(q) >= 0) return true;
    if (proto.indexOf(q) >= 0) return true;
    if (String(node.type || "").toLowerCase().indexOf(q) >= 0) return true;
    if (sub.indexOf(q) >= 0) return true;
    if (String(node.provider || "").toLowerCase().indexOf(q) >= 0) return true;
    return false;
  }

  function visibleNodes() {
    const q = state.queryFold;
    if (!q) return ordered(state.nodes);
    const out = [];
    for (let i = 0; i < state.nodes.length; i++) {
      if (matches(state.nodes[i], q)) out.push(state.nodes[i]);
    }
    return ordered(out);
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
    const name = PB.el("span", { class: "pbn-name" });
    const check = PB.el("span", { class: "pbn-check", text: "✓", hidden: true, attrs: { "aria-hidden": "true" } });
    const row1 = PB.el("div", { class: "pbn-line" }, auto, name, check);
    const proto = PB.el("span", { class: "pbn-proto" });
    const sub = PB.el("span", { class: "pbn-sub" });
    const row2 = PB.el("div", { class: "pbn-line" }, proto, sub);
    const delay = PB.el("span", { class: "pbn-delay" });
    const speed = PB.el("span", { class: "pbn-speed" });
    const actions = PB.el("span", { class: "pbn-actions" }, iconBtn("delay", "测延迟", "延"), iconBtn("speed", "测网速", "速"));
    const row3 = PB.el("div", { class: "pbn-line" }, delay, speed, actions);
    return PB.el("article", { class: "pbn-card" }, row1, row2, row3);
  }

  function rowTemplate() {
    const auto = PB.el("span", { class: "pbn-auto", text: "自动", hidden: true });
    const name = PB.el("span", { class: "pbn-name" });
    const check = PB.el("span", { class: "pbn-check", text: "✓", hidden: true, attrs: { "aria-hidden": "true" } });
    const nameCell = PB.el("td", {}, PB.el("div", { class: "pbn-namecell" }, auto, name, check));
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
      attrs: { type: "button", id: id },
      on: { click: function () { setGroup(groupId); } },
    }, PB.el("span", { class: "pbn-group-name", text: name }), PB.el("span", { class: "pbn-group-id", text: groupId }));
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
        type: "search",
        placeholder: "搜索节点、协议或订阅",
        autocomplete: "off",
        spellcheck: "false",
        "aria-label": "搜索节点",
      },
      on: { input: onSearch },
    });
    dom.sort = PB.el("select", {
      class: "pbn-sort",
      attrs: { id: "pbnSort", "aria-label": "排序" },
      on: { change: onSortPick },
    },
      PB.el("option", { text: "延迟 ↑", attrs: { value: "delay" } }),
      PB.el("option", { text: "网速 ↓", attrs: { value: "speed" } }),
      PB.el("option", { text: "名称", attrs: { value: "name" } }));
    dom.viewCards = PB.el("button", {
      class: "is-on",
      text: "卡片网格",
      attrs: { type: "button", id: "pbnViewCards" },
      on: { click: function () { setView("cards"); } },
    });
    dom.viewList = PB.el("button", {
      text: "列表视图",
      attrs: { type: "button", id: "pbnViewList" },
      on: { click: function () { setView("list"); } },
    });
    dom.delayAllBtn = PB.el("button", {
      class: "pbn-btn",
      text: "全部测延迟",
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
    bar.appendChild(dom.progress);
    bar.appendChild(PB.el("div", { class: "pbn-controls" },
      groups, dom.count, dom.note, dom.search, dom.sort, views, dom.delayAllBtn, dom.speedBtn));
    dom.aiHint = PB.el("span", { hidden: true });
    dom.speedHint = PB.el("span", { class: "pbn-hint-limit", text: "单节点限 10MB/6s", hidden: true });
    dom.hints = PB.el("div", { class: "pbn-hints" }, dom.aiHint, dom.speedHint);
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
    dom.empty = PB.el("div", { class: "pbn-empty", hidden: true }, dom.emptyText, dom.emptyStart, dom.emptySubs);
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
    state.query = String(dom.search.value || "");
    state.queryFold = state.query.trim().toLowerCase();
    state.scrollTop = 0;
    paint();
  }

  function onSortKey(key) {
    if (state.sort.key === key) state.sort.dir = -state.sort.dir;
    else {
      state.sort.key = key;
      state.sort.dir = SORT_DIR[key] || 1;
    }
    state.scrollTop = 0;
    paint();
  }

  function onSortPick() {
    const key = dom.sort.value;
    if (!SORT_DIR[key] && key !== "name") return;
    state.sort.key = key;
    state.sort.dir = SORT_DIR[key];
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
    paintBar();
    paintBody();
  }

  function paintBar() {
    const running = coreRunning();
    dom.groupProxy.classList.toggle("is-on", state.group === GROUP_PROXY);
    dom.groupAI.classList.toggle("is-on", state.group === GROUP_AI);
    dom.groupProxy.setAttribute("aria-pressed", state.group === GROUP_PROXY ? "true" : "false");
    dom.groupAI.setAttribute("aria-pressed", state.group === GROUP_AI ? "true" : "false");
    dom.count.textContent = "可用 " + state.aliveCount + " / 共 " + state.totalCount;
    const busyNote = state.delayAll || (state.refreshing && !state.loading);
    dom.note.hidden = !busyNote;
    dom.note.textContent = state.delayAll ? "正在测延迟…" : (busyNote ? "正在刷新…" : "");
    if (document.activeElement !== dom.search) dom.search.value = state.query;
    if (document.activeElement !== dom.sort) dom.sort.value = state.sort.key;
    const arrow = state.sort.dir > 0 ? " ↑" : " ↓";
    dom.sortName.textContent = "名称" + (state.sort.key === "name" ? arrow : "");
    dom.sortDelay.textContent = "延迟" + (state.sort.key === "delay" ? arrow : "");
    dom.sortSpeed.textContent = "网速" + (state.sort.key === "speed" ? arrow : "");
    dom.sortName.classList.toggle("is-on", state.sort.key === "name");
    dom.sortDelay.classList.toggle("is-on", state.sort.key === "delay");
    dom.sortSpeed.classList.toggle("is-on", state.sort.key === "speed");
    dom.viewCards.classList.toggle("is-on", state.view === "cards");
    dom.viewList.classList.toggle("is-on", state.view === "list");
    dom.viewCards.setAttribute("aria-pressed", state.view === "cards" ? "true" : "false");
    dom.viewList.setAttribute("aria-pressed", state.view === "list" ? "true" : "false");
    dom.delayAllBtn.textContent = state.delayAll ? "测延迟中" : "全部测延迟";
    dom.delayAllBtn.disabled = state.delayAll || !running;
    paintSpeedButton(running);
    const ai = state.group === GROUP_AI;
    const countReady = ai && !(state.loading && state.nodes.length === 0);
    dom.aiHint.hidden = false;
    dom.aiHint.textContent = countReady
      ? "AI 专属已自动过滤受限地区 · " + state.totalCount + " 个"
      : "AI 专属已自动过滤受限地区";
    const speedOn = state.speed && state.speed.phase !== "idle";
    dom.speedHint.hidden = !speedOn;
    dom.hints.hidden = false;
  }

  function emptyCopy() {
    if (state.queryFold && state.nodes.length > 0) return { text: "没有匹配的节点。", start: false, subs: false };
    if (!coreRunning() && !state.forced) {
      if (state.phase === "download") return { text: "内核正在启动，完成后可以查看节点。", start: false, subs: false };
      return { text: "内核未运行。启动内核后可以查看和切换节点。", start: true, subs: false };
    }
    return { text: "这个分组里还没有节点。先更新订阅。", start: false, subs: true };
  }

  function paintBody() {
    const rows = visibleNodes();
    state.painted = rows;
    const showRows = rows.length > 0 && (coreRunning() || state.forced);
    dom.loading.hidden = !state.loading;
    dom.error.hidden = !state.error;
    dom.error.textContent = state.error || "";
    const copy = emptyCopy();
    const showEmpty = !state.loading && !showRows;
    dom.empty.hidden = !showEmpty;
    if (showEmpty) dom.emptyText.textContent = copy.text;
    dom.emptyStart.hidden = !showEmpty || !copy.start;
    dom.emptySubs.hidden = !showEmpty || !copy.subs;
    const cards = showRows && state.view === "cards";
    const list = showRows && state.view === "list";
    dom.grid.hidden = !cards;
    dom.list.hidden = !list;
    if (cards) fillCards(rows);
    else dom.grid.replaceChildren();
    if (list) fillRows(rows);
    else dom.listBody.replaceChildren();
    if (state.scrollTop) dom.body.scrollTop = state.scrollTop;
  }

  function paintItem(node, nameWrap, protoEl, subEl, delayEl, speedEl, actions, selectedHost) {
    nameWrap.children[0].hidden = !node.isAuto;
    nameWrap.children[1].textContent = node.name;
    nameWrap.children[2].hidden = !node.selected;
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
      paintItem(
        rows[i],
        tr.children[0].children[0],
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

  function paintSpeedButton(running) {
    const speed = state.speed;
    const phase = speed.phase;
    const active = phase === "running" || phase === "stopping" || phase === "starting";
    dom.speedBtn.classList.toggle("is-stop", active);
    if (active) {
      dom.speedBtn.textContent = speed.total > 0
        ? "停止测速 (" + speed.done + "/" + speed.total + ")"
        : "停止测速";
    } else {
      dom.speedBtn.textContent = "全部测网速";
    }
    dom.speedBtn.disabled = phase === "stopping" || state.delayAll || (!running && phase === "idle");
    const showBar = phase === "running" || phase === "starting" || phase === "stopping";
    dom.progress.hidden = !showBar;
    const pct = speed.total > 0 ? Math.max(0, Math.min(100, (speed.done / speed.total) * 100)) : (phase === "starting" ? 8 : 0);
    dom.progressFill.style.width = pct + "%";
  }

  function onSpeedButton() {
    const phase = state.speed.phase;
    if (phase === "running" || phase === "starting") stopSpeed();
    else if (phase === "idle") startSpeed(null);
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
