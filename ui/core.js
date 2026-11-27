"use strict";

(function () {
  const GROUP_PROXY = "PROXY";
  const GROUP_AI = "🤖 AI-IDE";
  const MODES = [
    { id: "rule", label: "规则" },
    { id: "global", label: "全局" },
    { id: "direct", label: "直连" },
  ];
  const COLS = [
    { key: "name", label: "节点名" },
    { key: "provider", label: "所属订阅" },
    { key: "type", label: "协议类型" },
    { key: "delay", label: "延迟" },
  ];

  const state = {
    booted: false,
    phase: "stopped",
    message: "",
    progress: null,
    startFailed: false,
    stopRequested: false,
    status: null,
    controlError: "",
    geoLive: "",
    geoPct: null,
    subs: [],
    subsError: "",
    subsLoading: false,
    subsBusy: false,
    busySubId: "",
    confirmDeleteId: "",
    editingId: "",
    formError: "",
    group: GROUP_PROXY,
    nodes: [],
    painted: [],
    nodeView: "auto",
    nodesLoading: false,
    nodesError: "",
    testingName: "",
    sort: { key: "name", dir: 1 },
  };

  const dom = {};
  const locks = new Set();
  let nodeGen = 0;
  let statusChain = Promise.resolve();
  let statusTimer = 0;
  let nodeTimer = 0;
  let subTimer = 0;

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text != null) node.textContent = text;
    return node;
  }

  function num(v) {
    const n = Number(v);
    return Number.isFinite(n) ? n : 0;
  }

  function locked(key, fn) {
    if (locks.has(key)) return Promise.resolve();
    locks.add(key);
    return Promise.resolve()
      .then(fn)
      .finally(() => {
        locks.delete(key);
      });
  }

  function api(method, path, body) {
    const opts = { method: method, headers: { Accept: "application/json" } };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    return fetch(path, opts).then((res) => {
      return res.text().then((text) => {
        let data = {};
        if (text) {
          try {
            data = JSON.parse(text);
          } catch (err) {
            data = {};
          }
        }
        if (!res.ok) {
          const message = data && typeof data.error === "string" && data.error
            ? data.error
            : "请求失败: " + res.status;
          throw new Error(message);
        }
        return data;
      });
    });
  }

  function normalizeStatus(raw) {
    const s = raw || {};
    const settings = s.settings || {};
    const mode = typeof s.mode === "string" && s.mode ? s.mode : (settings.mode || "rule");
    return {
      running: !!s.running,
      version: typeof s.version === "string" ? s.version : "",
      mode: mode,
      systemProxyActive: !!s.systemProxyActive,
      geoReady: !!s.geoReady,
      geoDownloading: !!s.geoDownloading,
      geoError: typeof s.geoError === "string" ? s.geoError : "",
      bypassCN: !!settings.bypassCN,
    };
  }

  function normalizeSub(raw) {
    const s = raw || {};
    let usage = null;
    if (s.usage && typeof s.usage === "object") {
      usage = {
        upload: num(s.usage.upload),
        download: num(s.usage.download),
        total: num(s.usage.total),
        expire: num(s.usage.expire),
      };
    }
    return {
      id: String(s.id || ""),
      name: String(s.name || ""),
      url: String(s.url || ""),
      filter: String(s.filter || ""),
      userAgent: String(s.userAgent || ""),
      updateIntervalMin: Math.max(0, Math.round(num(s.updateIntervalMin))),
      enabled: !!s.enabled,
      updatedAt: s.updatedAt || "",
      lastError: String(s.lastError || ""),
      nodeCount: num(s.nodeCount),
      usage: usage,
    };
  }

  function normalizeNode(raw) {
    const n = raw || {};
    return {
      name: n.name != null ? String(n.name) : "",
      type: n.type != null ? String(n.type) : "",
      provider: n.provider != null ? String(n.provider) : "",
      delayMs: num(n.delayMs),
      untested: !!n.untested,
      isAuto: !!n.isAuto,
      selected: !!n.selected,
    };
  }

  function normalizeNodes(list) {
    const raw = Array.isArray(list) ? list : (list && Array.isArray(list.nodes) ? list.nodes : []);
    const out = new Array(raw.length);
    for (let i = 0; i < raw.length; i++) out[i] = normalizeNode(raw[i]);
    return out;
  }

  function safeURL(raw) {
    const text = String(raw || "");
    try {
      const u = new URL(text);
      const parts = u.pathname.split("/").filter(Boolean);
      let path = "";
      if (parts.length > 1) path = "/" + parts[0] + "/…";
      else if (parts.length === 1) path = "/…";
      return u.host + path;
    } catch (err) {
      const cut = text.split("?")[0].split("#")[0];
      const scheme = cut.indexOf("://");
      const rest = scheme >= 0 ? cut.slice(scheme + 3) : cut;
      const host = rest.split("/")[0];
      return host || "订阅地址";
    }
  }

  function pad(n) {
    return String(n).padStart(2, "0");
  }

  function formatWhen(iso) {
    if (!iso) return "尚未更新";
    const d = new Date(iso);
    if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return "尚未更新";
    return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()) + " " + pad(d.getHours()) + ":" + pad(d.getMinutes());
  }

  function formatExpire(sec) {
    if (!sec) return "";
    const d = new Date(sec * 1000);
    if (Number.isNaN(d.getTime())) return "";
    return "到期 " + d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate());
  }

  function formatBytes(n) {
    let v = Math.max(0, num(n));
    if (v < 1024) return Math.round(v) + " B";
    const units = ["KB", "MB", "GB", "TB"];
    let i = 0;
    v = v / 1024;
    while (v >= 1024 && i < units.length - 1) {
      v = v / 1024;
      i++;
    }
    return (v >= 100 ? v.toFixed(0) : v.toFixed(1)) + " " + units[i];
  }

  function providerLabel(node) {
    if (!node.provider) return "—";
    for (let i = 0; i < state.subs.length; i++) {
      if (state.subs[i].id === node.provider && state.subs[i].name) return state.subs[i].name;
    }
    return node.provider;
  }

  function delayInfo(node) {
    if (node.untested) return { text: "未测速", cls: "pb-ms-none" };
    if (!(node.delayMs > 0)) return { text: "超时", cls: "pb-ms-dead" };
    const ms = Math.round(node.delayMs);
    let cls = "pb-ms-slow";
    if (ms <= 200) cls = "pb-ms-good";
    else if (ms <= 500) cls = "pb-ms-mid";
    return { text: ms + " ms", cls: cls };
  }

  function delayRank(node) {
    if (node.untested) return 2;
    if (!(node.delayMs > 0)) return 1;
    return 0;
  }

  function compareKey(a, b) {
    const dir = state.sort.dir;
    if (state.sort.key === "delay") {
      const ra = delayRank(a);
      const rb = delayRank(b);
      if (ra !== rb) return ra - rb;
      if (ra === 0) return (a.delayMs - b.delayMs) * dir;
      return 0;
    }
    const va = state.sort.key === "provider" ? providerLabel(a) : String(a[state.sort.key] || "");
    const vb = state.sort.key === "provider" ? providerLabel(b) : String(b[state.sort.key] || "");
    return va.localeCompare(vb, "zh") * dir;
  }

  function ordered(list) {
    const copy = new Array(list.length);
    for (let i = 0; i < list.length; i++) copy[i] = { n: list[i], i: i };
    copy.sort((a, b) => {
      if (a.n.isAuto !== b.n.isAuto) return a.n.isAuto ? -1 : 1;
      return compareKey(a.n, b.n) || a.i - b.i;
    });
    const out = new Array(copy.length);
    for (let i = 0; i < copy.length; i++) out[i] = copy[i].n;
    return out;
  }

  function running() {
    return state.phase === "running";
  }

  function makeRowTemplate() {
    const tr = el("tr", "pb-row");
    const name = el("td", "pb-name-cell");
    const badge = el("span", "pb-badge", "自动");
    const nameText = el("span", "pb-name");
    name.appendChild(badge);
    name.appendChild(nameText);
    const provider = el("td");
    const type = el("td");
    const delay = el("td", "pb-delay-cell");
    const delayText = el("span", "pb-delay");
    const test = el("button", "pb-mini", "测速");
    test.type = "button";
    delay.appendChild(delayText);
    delay.appendChild(test);
    tr.appendChild(name);
    tr.appendChild(provider);
    tr.appendChild(type);
    tr.appendChild(delay);
    return tr;
  }

  function build(root) {
    root.textContent = "";
    const bar = el("div", "section-bar");
    const titleWrap = el("div", "section-title-wrap");
    titleWrap.appendChild(el("h2", "section-title", "内置内核"));
    bar.appendChild(titleWrap);
    dom.sectionState = el("div", "section-status", "已停止");
    bar.appendChild(dom.sectionState);
    root.appendChild(bar);

    const top = el("div", "pb-top");
    top.appendChild(buildControl());
    top.appendChild(buildSubs());
    root.appendChild(top);
    root.appendChild(buildNodes());
    document.body.appendChild(buildModal());
  }

  function buildControl() {
    const card = el("div", "white-card");
    card.id = "pbControl";
    card.appendChild(el("div", "pb-kicker", "内核"));
    dom.stateText = el("div", "pb-state", "已停止");
    card.appendChild(dom.stateText);

    const actions = el("div", "pb-actions");
    dom.startBtn = el("button", "btn-emerald", "启动内核");
    dom.startBtn.type = "button";
    dom.startBtn.id = "pbStart";
    dom.startBtn.addEventListener("click", () => {
      if (running()) stopCore();
      else startCore();
    });
    actions.appendChild(dom.startBtn);
    card.appendChild(actions);

    dom.progressWrap = el("div", "pb-progress");
    dom.progressWrap.id = "pbProgress";
    const row = el("div", "pb-progress-row");
    dom.progressPct = el("span", "", "正在下载");
    dom.cancelBtn = el("button", "btn-white", "取消");
    dom.cancelBtn.type = "button";
    dom.cancelBtn.id = "pbCancel";
    dom.cancelBtn.addEventListener("click", () => stopCore());
    row.appendChild(dom.progressPct);
    row.appendChild(dom.cancelBtn);
    dom.progressBar = el("div", "pb-bar");
    dom.progressFill = el("span");
    dom.progressBar.appendChild(dom.progressFill);
    dom.progressWrap.appendChild(row);
    dom.progressWrap.appendChild(dom.progressBar);
    card.appendChild(dom.progressWrap);

    dom.failText = el("div", "pb-fail");
    dom.failText.id = "pbFail";
    card.appendChild(dom.failText);

    dom.importBtn = el("button", "btn-white", "手动选择 mihomo.exe / .zip");
    dom.importBtn.type = "button";
    dom.importBtn.id = "pbImport";
    dom.importBtn.addEventListener("click", () => importManual());
    card.appendChild(dom.importBtn);

    dom.controlError = el("div", "pb-error");
    dom.controlError.id = "pbControlError";
    card.appendChild(dom.controlError);

    dom.modeBar = el("div", "pb-seg");
    dom.modeBar.id = "pbModes";
    dom.modeBtns = {};
    MODES.forEach((mode) => {
      const btn = el("button", "", mode.label);
      btn.type = "button";
      btn.id = "pbMode" + mode.id;
      btn.addEventListener("click", () => setMode(mode.id));
      dom.modeBar.appendChild(btn);
      dom.modeBtns[mode.id] = btn;
    });
    card.appendChild(dom.modeBar);

    dom.sysLine = lineSwitch("系统代理", "pbSysProxy", () => {
      if (!state.status) return;
      setSysProxy(!state.status.systemProxyActive);
    });
    card.appendChild(dom.sysLine.row);
    dom.sysHint = el("div", "pb-hint", "内核停止时不能开关系统代理");
    dom.sysHint.id = "pbSysHint";
    card.appendChild(dom.sysHint);

    dom.bypassLine = lineSwitch("绕过大陆", "pbBypass", () => {
      const on = state.status ? state.status.bypassCN : false;
      setBypass(!on, false);
    });
    card.appendChild(dom.bypassLine.row);
    const geoRow = el("div", "pb-line");
    dom.geoText = el("div", "pb-geo", "尚未下载地理数据");
    dom.geoText.id = "pbGeoText";
    dom.geoRetry = el("button", "pb-text-btn", "重试");
    dom.geoRetry.type = "button";
    dom.geoRetry.id = "pbGeoRetry";
    dom.geoRetry.addEventListener("click", () => setBypass(true, true));
    geoRow.appendChild(dom.geoText);
    geoRow.appendChild(dom.geoRetry);
    card.appendChild(geoRow);
    return card;
  }

  function lineSwitch(label, id, onClick) {
    const row = el("div", "pb-line");
    const left = el("div", "pb-line-label");
    left.appendChild(el("span", "", label));
    const word = el("span", "pb-hint", "未开启");
    left.appendChild(word);
    const btn = el("button", "pb-switch");
    btn.type = "button";
    btn.id = id;
    btn.setAttribute("role", "switch");
    btn.addEventListener("click", onClick);
    row.appendChild(left);
    row.appendChild(btn);
    return { row: row, btn: btn, word: word };
  }

  function buildSubs() {
    const card = el("div", "white-card");
    card.id = "pbSubsCard";
    const head = el("div", "pb-head");
    const left = el("div", "pb-line-label");
    left.appendChild(el("span", "", "订阅"));
    dom.subsCount = el("span", "count-pill", "0");
    left.appendChild(dom.subsCount);
    const actions = el("div", "pb-head-actions");
    dom.addBtn = el("button", "btn-emerald", "添加订阅");
    dom.addBtn.type = "button";
    dom.addBtn.id = "pbAddSub";
    dom.addBtn.addEventListener("click", openCreate);
    dom.updateAllBtn = el("button", "btn-white", "全部更新");
    dom.updateAllBtn.type = "button";
    dom.updateAllBtn.id = "pbUpdateAll";
    dom.updateAllBtn.addEventListener("click", () => updateAll());
    actions.appendChild(dom.updateAllBtn);
    actions.appendChild(dom.addBtn);
    head.appendChild(left);
    head.appendChild(actions);
    card.appendChild(head);
    dom.subsError = el("div", "pb-error");
    dom.subsError.id = "pbSubsError";
    card.appendChild(dom.subsError);
    dom.subsLoading = el("div", "pb-loading", "正在读取订阅…");
    card.appendChild(dom.subsLoading);
    dom.subsEmpty = el("div", "pb-empty", "还没有订阅。添加一个订阅地址即可。");
    card.appendChild(dom.subsEmpty);
    dom.subsList = el("div", "pb-sub-list");
    dom.subsList.id = "pbSubs";
    card.appendChild(dom.subsList);
    return card;
  }

  function buildNodes() {
    const card = el("div", "white-card pb-panel");
    card.id = "pbNodes";
    const head = el("div", "pb-head");
    dom.groups = el("div", "pb-groups");
    dom.groupProxy = el("button", "", "日常代理");
    dom.groupProxy.type = "button";
    dom.groupProxy.id = "pbGroupProxy";
    dom.groupAI = el("button", "", "AI 专属");
    dom.groupAI.type = "button";
    dom.groupAI.id = "pbGroupAI";
    dom.groupProxy.addEventListener("click", () => setGroup(GROUP_PROXY));
    dom.groupAI.addEventListener("click", () => setGroup(GROUP_AI));
    dom.groups.appendChild(dom.groupProxy);
    dom.groups.appendChild(dom.groupAI);
    dom.testAllBtn = el("button", "btn-white", "批量测速");
    dom.testAllBtn.type = "button";
    dom.testAllBtn.id = "pbTestAll";
    dom.testAllBtn.addEventListener("click", () => testAll());
    head.appendChild(dom.groups);
    head.appendChild(dom.testAllBtn);
    card.appendChild(head);

    dom.nodeLoading = el("div", "pb-loading", "正在读取节点…");
    dom.nodeLoading.id = "pbNodeLoading";
    card.appendChild(dom.nodeLoading);
    dom.nodeError = el("div", "pb-error");
    dom.nodeError.id = "pbNodeError";
    card.appendChild(dom.nodeError);
    dom.nodeEmpty = el("div", "pb-empty", "内核未运行。启动后可以查看和切换节点。");
    dom.nodeEmpty.id = "pbNodeEmpty";
    card.appendChild(dom.nodeEmpty);

    dom.scroll = el("div", "pb-scroll");
    const table = el("table", "pb-table");
    const thead = el("thead");
    const hr = el("tr");
    dom.sortBtns = {};
    COLS.forEach((col) => {
      const th = el("th");
      const btn = el("button", "pb-sort", col.label);
      btn.type = "button";
      btn.id = "pbSort" + col.key;
      btn.addEventListener("click", () => onSort(col.key));
      th.appendChild(btn);
      hr.appendChild(th);
      dom.sortBtns[col.key] = btn;
    });
    thead.appendChild(hr);
    dom.tbody = el("tbody");
    dom.tbody.id = "pbNodeBody";
    dom.tbody.addEventListener("click", onTbodyClick);
    table.appendChild(thead);
    table.appendChild(dom.tbody);
    dom.scroll.appendChild(table);
    card.appendChild(dom.scroll);
    dom.nodesPanel = card;
    dom.rowTpl = makeRowTemplate();
    return card;
  }

  function buildModal() {
    const backdrop = el("div", "modal-backdrop");
    backdrop.id = "pbSubModal";
    const modal = el("div", "modal");
    modal.addEventListener("click", (ev) => ev.stopPropagation());
    const head = el("div", "modal-head");
    dom.formTitle = el("span", "", "添加订阅");
    const close = el("button", "btn-white", "关闭");
    close.type = "button";
    close.addEventListener("click", closeForm);
    head.appendChild(dom.formTitle);
    head.appendChild(close);
    modal.appendChild(head);

    const form = el("form", "pb-form-grid");
    form.addEventListener("submit", (ev) => {
      ev.preventDefault();
      saveSub();
    });
    dom.fName = field(form, "名称", "text", "pbSubName");
    dom.fURL = field(form, "订阅地址", "text", "pbSubURL");
    dom.fFilter = field(form, "Filter 正则", "text", "pbSubFilter");
    dom.fUA = field(form, "自定义 User-Agent", "text", "pbSubUA");
    dom.fUA.placeholder = "留空则使用 clash.meta";
    dom.fInterval = field(form, "更新间隔（分钟）", "number", "pbSubInterval");
    dom.fInterval.min = "0";
    dom.fInterval.step = "1";
    dom.fInterval.value = "0";
    form.appendChild(el("div", "pb-form-hint", "0 表示仅手动更新"));
    dom.formError = el("div", "pb-error");
    dom.formError.id = "pbFormError";
    form.appendChild(dom.formError);
    const actions = el("div", "modal-actions");
    const cancel = el("button", "btn-white", "取消");
    cancel.type = "button";
    cancel.addEventListener("click", closeForm);
    dom.saveBtn = el("button", "btn-emerald", "保存");
    dom.saveBtn.type = "submit";
    dom.saveBtn.id = "pbSubSave";
    actions.appendChild(cancel);
    actions.appendChild(dom.saveBtn);
    form.appendChild(actions);
    modal.appendChild(form);
    backdrop.appendChild(modal);
    backdrop.addEventListener("click", closeForm);
    return backdrop;
  }

  function field(form, label, type, id) {
    const group = el("div", "form-group");
    const lab = el("label", "form-label", label);
    lab.htmlFor = id;
    const input = document.createElement("input");
    input.type = type;
    input.id = id;
    input.autocomplete = "off";
    input.spellcheck = false;
    group.appendChild(lab);
    group.appendChild(input);
    form.appendChild(group);
    return input;
  }

  function paintAll() {
    paintControl();
    paintSubs();
    paintNodesFrame();
  }

  function phaseLabel() {
    if (state.phase === "download") return state.message || "正在下载内核";
    if (state.phase === "running") {
      const version = state.status && state.status.version;
      return version ? "运行中 · " + version : "运行中";
    }
    if (state.phase === "error") return "启动失败";
    return "已停止";
  }

  function paintControl() {
    if (!dom.startBtn) return;
    const on = running();
    dom.stateText.textContent = phaseLabel();
    dom.sectionState.textContent = phaseLabel();
    dom.sectionState.className = "section-status" + (state.phase === "error" ? " is-bad" : "");
    dom.startBtn.textContent = on ? "停止内核" : (state.phase === "download" ? "启动中" : "启动内核");
    dom.startBtn.disabled = state.phase === "download" || locks.has("start") || locks.has("stop");

    const downloading = state.phase === "download";
    dom.progressWrap.hidden = !downloading;
    if (downloading) {
      const p = state.progress || { downloaded: 0, total: 0 };
      const pct = p.total > 0 ? Math.max(0, Math.min(100, (p.downloaded / p.total) * 100)) : null;
      if (pct == null) {
        dom.progressBar.classList.add("is-indeterminate");
        dom.progressFill.style.width = "";
        dom.progressPct.textContent = state.message || "正在下载";
      } else {
        dom.progressBar.classList.remove("is-indeterminate");
        dom.progressFill.style.width = pct.toFixed(1) + "%";
        dom.progressPct.textContent = (state.message || "正在下载内核") + " " + Math.round(pct) + "%";
      }
    }

    const failed = state.phase === "error";
    dom.failText.hidden = !failed;
    dom.failText.textContent = failed ? (state.message || "启动失败") : "";
    dom.importBtn.hidden = !failed;
    dom.controlError.hidden = !state.controlError;
    dom.controlError.textContent = state.controlError || "";

    const mode = state.status && state.status.mode ? state.status.mode : "rule";
    MODES.forEach((item) => {
      const btn = dom.modeBtns[item.id];
      btn.disabled = !on;
      btn.classList.toggle("is-on", mode === item.id);
      btn.setAttribute("aria-pressed", mode === item.id ? "true" : "false");
    });

    const proxyOn = !!(state.status && state.status.systemProxyActive);
    dom.sysLine.btn.disabled = !on;
    dom.sysLine.btn.classList.toggle("on", proxyOn);
    dom.sysLine.btn.setAttribute("aria-checked", proxyOn ? "true" : "false");
    dom.sysLine.word.textContent = proxyOn ? "已开启" : "未开启";
    dom.sysHint.hidden = on;

    const bypassOn = !!(state.status && state.status.bypassCN);
    dom.bypassLine.btn.disabled = !on;
    dom.bypassLine.btn.classList.toggle("on", bypassOn);
    dom.bypassLine.btn.setAttribute("aria-checked", bypassOn ? "true" : "false");
    dom.bypassLine.word.textContent = bypassOn ? "已开启" : "未开启";
    paintGeo();
    dom.groupProxy.disabled = !on && state.nodeView !== "force";
    dom.groupAI.disabled = !on && state.nodeView !== "force";
    dom.testAllBtn.disabled = !on || state.nodesLoading;
  }

  function geoText() {
    const status = state.status;
    if (status && status.geoError) return status.geoError;
    const live = state.geoLive || "";
    const downloading = (status && status.geoDownloading) || (/下载/.test(live) && !/已下载|已开启/.test(live));
    if (downloading) {
      if (state.geoPct != null) return "下载中 " + state.geoPct + "%";
      return live || "下载中";
    }
    if (status && status.geoReady) return "地理数据已就绪";
    if (live) return live;
    return "尚未下载地理数据";
  }

  function paintGeo() {
    if (!dom.geoText) return;
    const bad = !!(state.status && state.status.geoError);
    dom.geoText.textContent = geoText();
    dom.geoText.className = "pb-geo" + (bad ? " is-bad" : "");
    dom.geoRetry.hidden = !bad;
    dom.geoRetry.disabled = !running();
  }

  function paintSubs() {
    if (!dom.subsList) return;
    dom.subsCount.textContent = String(state.subs.length);
    dom.subsError.hidden = !state.subsError;
    dom.subsError.textContent = state.subsError || "";
    dom.subsLoading.hidden = !state.subsLoading;
    dom.subsEmpty.hidden = state.subsLoading || state.subs.length > 0;
    dom.updateAllBtn.disabled = state.subsBusy || state.subs.length === 0;
    dom.updateAllBtn.textContent = state.subsBusy ? "更新中" : "全部更新";
    const frag = document.createDocumentFragment();
    for (let i = 0; i < state.subs.length; i++) frag.appendChild(subCard(state.subs[i]));
    dom.subsList.textContent = "";
    dom.subsList.appendChild(frag);
  }

  function subCard(sub) {
    const card = el("div", "pb-sub");
    const top = el("div", "pb-sub-top");
    top.appendChild(el("div", "pb-sub-name", sub.name || "订阅"));
    const sw = el("button", "pb-switch");
    sw.type = "button";
    sw.setAttribute("role", "switch");
    sw.setAttribute("aria-checked", sub.enabled ? "true" : "false");
    sw.classList.toggle("on", sub.enabled);
    sw.disabled = state.subsBusy || state.busySubId === sub.id;
    sw.addEventListener("click", () => toggleSub(sub));
    top.appendChild(sw);
    card.appendChild(top);
    card.appendChild(el("div", "pb-url", safeURL(sub.url)));
    const interval = sub.updateIntervalMin > 0 ? "每 " + sub.updateIntervalMin + " 分钟" : "仅手动";
    card.appendChild(el("div", "pb-meta", "节点 " + sub.nodeCount + " · " + formatWhen(sub.updatedAt) + " · " + interval));
    appendUsage(card, sub.usage);
    if (sub.lastError) card.appendChild(el("div", "pb-error", sub.lastError));
    const actions = el("div", "pb-sub-actions");
    if (state.confirmDeleteId === sub.id) {
      actions.appendChild(el("span", "pb-confirm", "确认删除「" + (sub.name || "订阅") + "」？"));
      const yes = el("button", "", "删除");
      yes.type = "button";
      yes.addEventListener("click", () => deleteSub(sub.id));
      const no = el("button", "", "取消");
      no.type = "button";
      no.addEventListener("click", () => {
        state.confirmDeleteId = "";
        paintSubs();
      });
      actions.appendChild(yes);
      actions.appendChild(no);
    } else {
      const update = el("button", "", state.busySubId === sub.id ? "更新中" : "更新");
      update.type = "button";
      update.disabled = state.subsBusy || state.busySubId === sub.id;
      update.addEventListener("click", () => updateSub(sub.id));
      const edit = el("button", "", "编辑");
      edit.type = "button";
      edit.addEventListener("click", () => openEdit(sub));
      const del = el("button", "", "删除");
      del.type = "button";
      del.addEventListener("click", () => {
        state.confirmDeleteId = sub.id;
        paintSubs();
      });
      actions.appendChild(update);
      actions.appendChild(edit);
      actions.appendChild(del);
    }
    card.appendChild(actions);
    return card;
  }

  function appendUsage(card, usage) {
    if (!usage) return;
    const showBar = usage.total > 0;
    const expire = formatExpire(usage.expire);
    if (!showBar && !expire) return;
    if (showBar) {
      const used = usage.upload + usage.download;
      const pct = Math.max(0, Math.min(100, (used / usage.total) * 100));
      const track = el("div", "pb-usage");
      const bar = el("span");
      bar.style.width = pct.toFixed(1) + "%";
      if (pct >= 90) bar.className = "is-full";
      else if (pct >= 75) bar.className = "is-high";
      track.appendChild(bar);
      card.appendChild(track);
      card.appendChild(el("div", "pb-meta", formatBytes(used) + " / " + formatBytes(usage.total)));
    }
    if (expire) card.appendChild(el("div", "pb-meta", expire));
  }

  function paintNodesFrame() {
    if (!dom.nodesPanel) return;
    const force = state.nodeView === "force";
    const showTable = force || (running() && state.nodes.length > 0);
    dom.nodesPanel.classList.toggle("is-off", !running() && !force);
    dom.nodeLoading.hidden = !state.nodesLoading;
    dom.nodeError.hidden = !state.nodesError;
    dom.nodeError.textContent = state.nodesError || "";
    const showEmpty = !state.nodesLoading && !state.nodesError && !showTable;
    dom.nodeEmpty.hidden = !showEmpty;
    if (showEmpty) {
      if (state.phase === "download") dom.nodeEmpty.textContent = "内核正在启动，完成后可以查看节点。";
      else if (running()) dom.nodeEmpty.textContent = "这个分组里还没有节点。先更新订阅。";
      else dom.nodeEmpty.textContent = "内核未运行。启动后可以查看和切换节点。";
    }
    dom.scroll.hidden = !showTable && !force;
    if (showTable) paintRows();
    else if (!force) {
      dom.tbody.textContent = "";
      state.painted = [];
    }
    paintGroup();
    if (dom.testAllBtn) dom.testAllBtn.disabled = !running() || state.nodesLoading;
  }

  function paintGroup() {
    if (!dom.groupProxy) return;
    dom.groupProxy.classList.toggle("is-on", state.group === GROUP_PROXY);
    dom.groupAI.classList.toggle("is-on", state.group === GROUP_AI);
  }

  function paintRows() {
    const rows = ordered(state.nodes);
    state.painted = rows;
    const frag = document.createDocumentFragment();
    const tpl = dom.rowTpl;
    const testing = state.testingName;
    const canTest = running();
    for (let i = 0; i < rows.length; i++) {
      const node = rows[i];
      const tr = tpl.cloneNode(true);
      tr.dataset.i = String(i);
      tr.className = node.selected ? "pb-row is-selected" : "pb-row";
      const nameCell = tr.children[0];
      nameCell.children[0].hidden = !node.isAuto;
      nameCell.children[1].textContent = node.name;
      tr.children[1].textContent = providerLabel(node);
      tr.children[2].textContent = node.type || "—";
      const info = delayInfo(node);
      const delay = tr.children[3];
      delay.children[0].className = "pb-delay " + info.cls;
      delay.children[0].textContent = info.text;
      const busy = testing === node.name;
      delay.children[1].textContent = busy ? "测速中" : "测速";
      delay.children[1].disabled = busy || !canTest;
      frag.appendChild(tr);
    }
    dom.tbody.replaceChildren(frag);
    paintSortHeaders();
  }

  function paintSortHeaders() {
    COLS.forEach((col) => {
      const btn = dom.sortBtns[col.key];
      const on = state.sort.key === col.key;
      btn.classList.toggle("is-on", on);
      const arrow = on ? (state.sort.dir > 0 ? " ↑" : " ↓") : "";
      btn.textContent = col.label + arrow;
      btn.setAttribute("aria-sort", on ? (state.sort.dir > 0 ? "ascending" : "descending") : "none");
    });
  }

  function onSort(key) {
    if (state.sort.key === key) state.sort.dir = -state.sort.dir;
    else {
      state.sort.key = key;
      state.sort.dir = 1;
    }
    if (state.nodes.length) paintRows();
  }

  function onTbodyClick(ev) {
    const tr = ev.target.closest("tr");
    if (!tr || !dom.tbody.contains(tr)) return;
    const node = state.painted[Number(tr.dataset.i)];
    if (!node) return;
    if (ev.target.closest("button")) {
      testOne(node);
      return;
    }
    selectNode(node);
  }

  function applyStatus(raw, opts) {
    const s = normalizeStatus(raw);
    state.status = s;
    const keep = !!(opts && opts.keepLocal);
    const preserve = !s.running && keep && (state.phase === "download" || state.phase === "error");
    if (s.running) {
      state.phase = "running";
      state.startFailed = false;
      state.progress = null;
    } else if (!preserve) {
      state.phase = "stopped";
      state.startFailed = false;
      state.progress = null;
    }
    if (!s.geoDownloading && !(state.geoLive && /下载/.test(state.geoLive) && !/已下载|已开启/.test(state.geoLive))) {
      state.geoPct = null;
    }
    paintControl();
    if (state.phase !== "running" && state.nodeView !== "force") {
      state.nodesLoading = false;
    }
    paintNodesFrame();
  }

  function loadStatus(opts) {
    const run = statusChain.then(() => loadStatusNow(opts));
    statusChain = run.then(() => {}, () => {});
    return run;
  }

  function loadStatusNow(opts) {
    return api("GET", "/api/core/status").then((data) => {
      const prev = state.phase;
      applyStatus(data, opts);
      if (state.phase === "running" && prev !== "running" && state.nodeView !== "force") return loadNodes();
    }).catch((err) => {
      state.controlError = err.message || "读取内核状态失败";
      paintControl();
    });
  }

  function scheduleStatus() {
    clearTimeout(statusTimer);
    statusTimer = setTimeout(() => {
      loadStatus({ keepLocal: true });
    }, 400);
  }

  function loadSubs() {
    state.subsLoading = state.subs.length === 0;
    paintSubs();
    return api("GET", "/api/subs").then((data) => {
      state.subs = Array.isArray(data.subscriptions) ? data.subscriptions.map(normalizeSub) : [];
      state.subsError = "";
    }).catch((err) => {
      state.subsError = err.message || "读取订阅失败";
    }).then(() => {
      state.subsLoading = false;
      paintSubs();
    });
  }

  function scheduleSubs() {
    clearTimeout(subTimer);
    subTimer = setTimeout(() => {
      loadSubs();
    }, 300);
  }

  function reloadSubsQuiet() {
    return api("GET", "/api/subs").then((data) => {
      state.subs = Array.isArray(data.subscriptions) ? data.subscriptions.map(normalizeSub) : [];
    }).catch(() => {});
  }

  function absorbSubs(data) {
    if (!data) return;
    if (Array.isArray(data.subscriptions)) {
      state.subs = data.subscriptions.map(normalizeSub);
      return;
    }
    if (data.subscription) upsertSub(data.subscription);
  }

  function upsertSub(raw) {
    const sub = normalizeSub(raw);
    const idx = state.subs.findIndex((item) => item.id === sub.id);
    if (idx < 0) state.subs.push(sub);
    else state.subs[idx] = sub;
  }

  function renderSubs(list) {
    state.subs = Array.isArray(list) ? list.map(normalizeSub) : [];
    state.subsLoading = false;
    paintSubs();
  }

  function intervalValue(raw) {
    if (String(raw).trim() === "") return 0;
    const n = Math.round(Number(raw));
    if (!Number.isFinite(n) || n < 0) return 0;
    return n;
  }

  function readForm() {
    return {
      name: dom.fName.value.trim(),
      url: dom.fURL.value.trim(),
      filter: dom.fFilter.value,
      userAgent: dom.fUA.value.trim(),
      updateIntervalMin: intervalValue(dom.fInterval.value),
    };
  }

  function paintFormError() {
    if (!dom.formError) return;
    dom.formError.hidden = !state.formError;
    dom.formError.textContent = state.formError || "";
  }

  function openCreate() {
    state.editingId = "";
    state.formError = "";
    dom.formTitle.textContent = "添加订阅";
    dom.fName.value = "";
    dom.fURL.value = "";
    dom.fFilter.value = "";
    dom.fUA.value = "";
    dom.fInterval.value = "0";
    paintFormError();
    dom.modal.classList.add("open");
  }

  function openEdit(sub) {
    state.editingId = sub.id;
    state.formError = "";
    dom.formTitle.textContent = "编辑订阅";
    dom.fName.value = sub.name;
    dom.fURL.value = sub.url;
    dom.fFilter.value = sub.filter;
    dom.fUA.value = sub.userAgent;
    dom.fInterval.value = String(sub.updateIntervalMin || 0);
    paintFormError();
    dom.modal.classList.add("open");
  }

  function closeForm() {
    if (dom.modal) dom.modal.classList.remove("open");
  }

  function saveSub() {
    const body = readForm();
    return locked("saveSub", () => {
      dom.saveBtn.disabled = true;
      const req = state.editingId
        ? api("PUT", "/api/subs", Object.assign({ id: state.editingId }, body))
        : api("POST", "/api/subs", body);
      return req.then((data) => {
        if (data.subscription) upsertSub(data.subscription);
        else return loadSubs();
      }).then(() => {
        state.subsError = "";
        state.formError = "";
        closeForm();
        paintSubs();
        pokeNodes();
      }).catch((err) => {
        state.formError = err.message || "保存失败";
        paintFormError();
      }).then(() => {
        dom.saveBtn.disabled = false;
      });
    });
  }

  function toggleSub(sub) {
    return locked("en:" + sub.id, () => {
      return api("PUT", "/api/subs", { id: sub.id, enabled: !sub.enabled }).then((data) => {
        if (data.subscription) upsertSub(data.subscription);
        state.subsError = "";
        paintSubs();
        pokeNodes();
      }).catch((err) => {
        state.subsError = err.message || "切换失败";
        paintSubs();
      });
    });
  }

  function deleteSub(id) {
    return locked("del:" + id, () => {
      return api("DELETE", "/api/subs", { id: id }).then(() => {
        state.subs = state.subs.filter((sub) => sub.id !== id);
        state.confirmDeleteId = "";
        state.subsError = "";
        paintSubs();
        pokeNodes();
      }).catch((err) => {
        state.subsError = err.message || "删除失败";
        paintSubs();
      });
    });
  }

  function updateSub(id) {
    return locked("up:" + id, () => {
      state.busySubId = id;
      paintSubs();
      return api("POST", "/api/subs/update", { id: id }).then((data) => {
        absorbSubs(data);
        state.subsError = "";
        pokeNodes();
      }).catch((err) => {
        state.subsError = err.message || "更新失败";
        return reloadSubsQuiet();
      }).then(() => {
        state.busySubId = "";
        paintSubs();
      });
    });
  }

  function updateAll() {
    return locked("upAll", () => {
      state.subsBusy = true;
      paintSubs();
      return api("POST", "/api/subs/update", {}).then((data) => {
        absorbSubs(data);
        const bad = Array.isArray(data.results) ? data.results.filter((item) => item && !item.ok) : [];
        state.subsError = bad.length ? bad.map((item) => item.error || "更新失败").join("；") : "";
        pokeNodes();
      }).catch((err) => {
        state.subsError = err.message || "更新失败";
        return reloadSubsQuiet();
      }).then(() => {
        state.subsBusy = false;
        paintSubs();
      });
    });
  }

  function setGroup(id) {
    if (state.group === id) return;
    state.group = id;
    state.nodeView = "auto";
    paintGroup();
    if (running()) loadNodes();
    else paintNodesFrame();
  }

  function loadNodes() {
    if (!running()) {
      if (state.nodeView !== "force") {
        state.nodes = [];
        state.nodesLoading = false;
        paintNodesFrame();
      }
      return Promise.resolve();
    }
    const gen = ++nodeGen;
    state.nodesLoading = true;
    state.nodesError = "";
    paintNodesFrame();
    return api("POST", "/api/nodes/list", {
      group: state.group,
      client: "builtin",
      testDelay: false,
    }).then((data) => {
      if (gen !== nodeGen) return;
      state.nodes = normalizeNodes(data && data.nodes);
      state.nodeView = "auto";
    }).catch((err) => {
      if (gen !== nodeGen) return;
      state.nodesError = err.message || "读取节点失败";
      state.nodes = [];
    }).then(() => {
      if (gen !== nodeGen) return;
      state.nodesLoading = false;
      paintNodesFrame();
    });
  }

  function scheduleNodes() {
    clearTimeout(nodeTimer);
    nodeTimer = setTimeout(() => {
      if (running() && state.nodeView !== "force") loadNodes();
    }, 400);
  }

  function pokeNodes() {
    if (running() && state.nodeView !== "force") scheduleNodes();
  }

  function renderNodes(list) {
    if (!dom.tbody) return;
    nodeGen++;
    state.nodes = normalizeNodes(list);
    state.nodeView = "force";
    state.nodesLoading = false;
    state.nodesError = "";
    paintNodesFrame();
  }

  function testAll() {
    if (!running()) return;
    return locked("testAll", () => {
      const gen = ++nodeGen;
      state.nodesLoading = true;
      state.nodesError = "";
      paintNodesFrame();
      return api("POST", "/api/nodes/list", {
        group: state.group,
        client: "builtin",
        testDelay: true,
      }).then((data) => {
        if (gen !== nodeGen && state.nodeView === "force") return;
        state.nodes = normalizeNodes(data && data.nodes);
        state.nodeView = "auto";
        nodeGen++;
      }).catch((err) => {
        state.nodesError = err.message || "测速失败";
      }).then(() => {
        state.nodesLoading = false;
        if (state.nodeView !== "force") paintNodesFrame();
        else paintRows();
      });
    });
  }

  function testOne(node) {
    if (!running()) {
      state.nodesError = "内核未运行";
      paintNodesFrame();
      return;
    }
    return locked("test:" + node.name, () => {
      state.testingName = node.name;
      paintRows();
      return api("POST", "/api/nodes/test", {
        client: "builtin",
        nodeName: node.name,
      }).then((data) => {
        node.untested = false;
        node.delayMs = num(data.delayMs);
        state.nodesError = "";
      }).catch((err) => {
        state.nodesError = err.message || "测速失败";
      }).then(() => {
        state.testingName = "";
        paintNodesFrame();
      });
    });
  }

  function selectNode(node) {
    if (!running()) return;
    return locked("select", () => {
      return api("POST", "/api/nodes/select", {
        group: state.group,
        client: "builtin",
        nodeName: node.name,
      }).then(() => {
        for (let i = 0; i < state.nodes.length; i++) {
          state.nodes[i].selected = state.nodes[i].name === node.name;
        }
        state.nodesError = "";
        paintRows();
      }).catch((err) => {
        state.nodesError = err.message || "切换节点失败";
        paintNodesFrame();
      });
    });
  }

  function setMode(mode) {
    if (!running()) return;
    return locked("mode", () => {
      return api("POST", "/api/core/mode", { mode: mode }).then((data) => {
        if (!state.status) state.status = normalizeStatus({});
        state.status.mode = data.mode || mode;
        state.controlError = "";
        paintControl();
      }).catch((err) => {
        state.controlError = err.message || "切换模式失败";
        paintControl();
      });
    });
  }

  function setBypass(enable, retry) {
    if (!running()) return;
    return locked("bypass", () => {
      return api("POST", "/api/core/bypass-cn", { enable: !!enable, retry: !!retry }).then((data) => {
        state.controlError = "";
        applyStatus(data, { keepLocal: true });
      }).catch((err) => {
        state.controlError = err.message || "绕过大陆切换失败";
        if (!state.status) state.status = normalizeStatus({});
        state.status.geoError = err.message || "绕过大陆切换失败";
        paintControl();
      });
    });
  }

  function setSysProxy(enable) {
    if (!running()) return;
    return locked("sysproxy", () => {
      return api("POST", "/api/sysproxy/set", { enable: !!enable }).then(() => {
        state.controlError = "";
        return loadStatus({ keepLocal: true });
      }).catch((err) => {
        state.controlError = err.message || "系统代理切换失败";
        paintControl();
      });
    });
  }

  function startCore() {
    return locked("start", () => {
      state.stopRequested = false;
      state.phase = "download";
      state.startFailed = false;
      state.controlError = "";
      state.message = "正在启动";
      state.progress = { downloaded: 0, total: 0 };
      if (state.nodeView !== "force") state.nodeView = "auto";
      paintControl();
      paintNodesFrame();
      return api("POST", "/api/core/start", {}).then((data) => {
        applyStatus(data, { replace: true });
        if (running() && state.nodeView !== "force") return loadNodes();
      }).catch((err) => {
        state.progress = null;
        const canceled = state.stopRequested || state.phase === "stopped" || String(err.message || "").indexOf("已取消") >= 0;
        if (canceled) {
          state.phase = "stopped";
          state.startFailed = false;
          state.message = err.message || "内核已停止";
        } else if (state.phase !== "running") {
          state.phase = "error";
          state.startFailed = true;
          state.message = err.message || "启动失败";
        }
        paintControl();
        paintNodesFrame();
      });
    });
  }

  function stopCore() {
    return locked("stop", () => {
      state.stopRequested = true;
      return api("POST", "/api/core/stop", {}).then(() => {
        state.phase = "stopped";
        state.startFailed = false;
        state.progress = null;
        state.message = "内核已停止";
        state.controlError = "";
        if (state.nodeView !== "force") {
          state.nodes = [];
          state.nodeView = "auto";
        }
        return loadStatus({ keepLocal: false });
      }).catch((err) => {
        state.controlError = err.message || "停止失败";
        paintControl();
      }).then(() => {
        state.stopRequested = false;
      });
    });
  }

  function importPathBody(path) {
    return api("POST", "/api/core/import-binary", { path: path }).then(() => {
      state.controlError = "";
      state.message = "已导入内核，正在启动";
      paintControl();
      return startCore();
    }).catch((err) => {
      state.phase = "error";
      state.startFailed = true;
      state.message = err.message || "导入失败";
      state.controlError = err.message || "导入失败";
      paintControl();
    });
  }

  function importFromPath(path) {
    if (!path) {
      state.controlError = "未选择文件";
      paintControl();
      return Promise.resolve();
    }
    return locked("import", () => importPathBody(path));
  }

  function importManual() {
    return locked("import", () => {
      return api("POST", "/api/browse-core").then((picked) => {
        if (!picked || !picked.path) {
          state.controlError = "未选择文件";
          paintControl();
          return;
        }
        return importPathBody(picked.path);
      }).catch((err) => {
        state.controlError = err.message || "选择文件失败";
        state.phase = "error";
        state.startFailed = true;
        paintControl();
      });
    });
  }

  function onCore(c) {
    const phase = c && c.phase;
    if (phase === "download") {
      state.phase = "download";
      state.message = c.message || "正在下载内核";
      state.progress = { downloaded: num(c.downloaded), total: num(c.total) };
      state.startFailed = false;
      paintControl();
      paintNodesFrame();
      return;
    }
    if (phase === "running") {
      state.phase = "running";
      state.message = c.message || "内核已启动";
      state.progress = null;
      state.startFailed = false;
      paintControl();
      paintNodesFrame();
      scheduleStatus();
      scheduleNodes();
      return;
    }
    if (phase === "stopped") {
      state.phase = "stopped";
      state.message = c.message || "内核已停止";
      state.progress = null;
      state.startFailed = false;
      if (state.nodeView !== "force") {
        state.nodes = [];
        state.nodeView = "auto";
      }
      paintControl();
      paintNodesFrame();
      return;
    }
    if (phase === "error") {
      state.phase = "error";
      state.startFailed = true;
      state.message = c.message || "启动失败";
      state.progress = null;
      paintControl();
      paintNodesFrame();
      return;
    }
    if (phase === "geo") {
      state.geoLive = c.message || "";
      state.geoPct = c.total > 0 ? Math.max(0, Math.min(100, Math.round((num(c.downloaded) / num(c.total)) * 100))) : null;
      if (!state.status) state.status = normalizeStatus({});
      if (/下载/.test(state.geoLive) && !/已下载|已开启/.test(state.geoLive)) state.status.geoDownloading = true;
      paintGeo();
      scheduleStatus();
    }
  }

  function onSSE(msg) {
    if (!msg || !state.booted) return;
    if (msg.type === "core" && msg.core) onCore(msg.core);
    else if (msg.type === "subs") scheduleSubs();
    else if (msg.type === "sync" && running() && state.nodeView !== "force") scheduleNodes();
  }

  function refresh() {
    return loadStatus({ keepLocal: false }).then(() => loadSubs()).then(() => {
      if (running()) return loadNodes();
      if (state.nodeView !== "force") {
        state.nodes = [];
        paintNodesFrame();
      }
    });
  }

  function init() {
    if (state.booted) return;
    const root = document.getElementById("secCore");
    if (!root) return;
    state.booted = true;
    build(root);
    dom.modal = document.getElementById("pbSubModal");
    paintAll();
    loadStatus({ keepLocal: false });
    loadSubs();
  }

  window.ProxyBoxCore = {
    init: init,
    onSSE: onSSE,
    renderNodes: renderNodes,
    renderSubs: renderSubs,
    applyStatus: applyStatus,
    importManual: importManual,
    importFromPath: importFromPath,
    refresh: refresh,
  };
})();
