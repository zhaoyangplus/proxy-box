"use strict";

(function () {
  const state = {
    mounted: false,
    subs: [],
    loading: false,
    error: "",
    busyAll: false,
    busyId: "",
    confirmId: "",
    editingId: "",
    baseline: null,
    formError: "",
    queuedShow: false,
  };

  const dom = {};
  const locks = new Set();
  let loadGen = 0;
  let subTimer = 0;

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

  function maskURL(raw) {
    const text = String(raw || "");
    let shown = "";
    try {
      const u = new URL(text);
      const parts = u.pathname.split("/").filter(Boolean);
      let path = "";
      if (parts.length > 1) path = "/" + parts[0] + "/…";
      else if (parts.length === 1) path = "/…";
      shown = u.host + path;
    } catch (err) {
      const cut = text.split("?")[0].split("#")[0];
      const scheme = cut.indexOf("://");
      const rest = scheme >= 0 ? cut.slice(scheme + 3) : cut;
      shown = rest.split("/")[0] || "订阅地址";
    }
    if (shown.length > 72) shown = shown.slice(0, 69) + "…";
    return shown;
  }

  function validURL(raw) {
    const text = String(raw || "").trim();
    if (!text) return false;
    try {
      const u = new URL(text);
      return (u.protocol === "http:" || u.protocol === "https:") && !!u.host;
    } catch (err) {
      return false;
    }
  }

  function intervalValue(raw) {
    if (String(raw).trim() === "") return 0;
    const n = Math.round(Number(raw));
    if (!Number.isFinite(n) || n < 0) return 0;
    return n;
  }

  function fmtBytes(n) {
    if (PB.fmt && typeof PB.fmt.bytes === "function") return PB.fmt.bytes(n);
    return String(Math.round(num(n))) + " B";
  }

  function fmtWhen(value) {
    if (!value) return "尚未更新";
    const d = new Date(value);
    if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return "尚未更新";
    if (PB.fmt && typeof PB.fmt.date === "function") {
      try {
        const text = PB.fmt.date(value);
        if (text) return String(text);
      } catch (err) {
        return "尚未更新";
      }
    }
    return "尚未更新";
  }

  function fmtExpire(sec) {
    const n = num(sec);
    if (!n) return "";
    const d = new Date(n * 1000);
    if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return "";
    if (PB.fmt && typeof PB.fmt.date === "function") {
      try {
        const text = PB.fmt.date(n);
        if (text) return "到期 " + String(text);
      } catch (err) {
        return "";
      }
    }
    return "";
  }

  function intervalText(min) {
    return min > 0 ? "每 " + min + " 分钟" : "仅手动";
  }

  function findSub(id) {
    for (let i = 0; i < state.subs.length; i++) {
      if (state.subs[i].id === id) return state.subs[i];
    }
    return null;
  }

  function upsert(raw) {
    const sub = normalizeSub(raw);
    const idx = state.subs.findIndex(function (item) { return item.id === sub.id; });
    if (idx < 0) state.subs.push(sub);
    else state.subs[idx] = sub;
  }

  function absorb(data) {
    if (!data) return false;
    if (Array.isArray(data.subscriptions)) {
      state.subs = data.subscriptions.map(normalizeSub);
      return true;
    }
    if (data.subscription) {
      upsert(data.subscription);
      return true;
    }
    return false;
  }

  function resultErrors(data) {
    const results = data && Array.isArray(data.results) ? data.results : [];
    const msgs = [];
    for (let i = 0; i < results.length; i++) {
      const item = results[i];
      if (item && item.ok === false) msgs.push(item.error || "更新失败");
    }
    return msgs.join("；");
  }

  function field(form, label, id, type) {
    const input = document.createElement("input");
    input.type = type || "text";
    input.id = id;
    input.autocomplete = "off";
    input.spellcheck = false;
    const lab = PB.el("span", { text: label });
    const wrap = PB.el("label", { class: "pbs-field" }, lab, input);
    form.appendChild(wrap);
    return input;
  }

  function mount(root) {
    if (state.mounted) return;
    root.classList.add("pbs");
    dom.count = PB.el("span", { class: "pbs-count", attrs: { id: "pbsCount" }, text: "共 0 个订阅" });
    dom.updateAllBtn = PB.el("button", {
      class: "pbs-btn",
      text: "全部更新",
      attrs: { type: "button", id: "pbsUpdateAll" },
      on: { click: function () { updateAll(); } },
    });
    dom.addBtn = PB.el("button", {
      class: "pbs-btn pbs-btn-primary",
      text: "添加订阅",
      attrs: { type: "button", id: "pbsAdd" },
      on: { click: openCreate },
    });
    const bar = PB.el("div", { class: "pbs-bar" },
      dom.count,
      PB.el("div", { class: "pbs-bar-actions" }, dom.updateAllBtn, dom.addBtn));
    dom.error = PB.el("div", { class: "pbs-error", hidden: true });
    dom.loading = PB.el("div", { class: "pbs-loading", text: "正在读取订阅…", hidden: true });
    dom.empty = PB.el("div", { class: "pbs-empty", text: "还没有订阅。添加一个订阅地址即可。", hidden: true });
    dom.list = PB.el("div", { class: "pbs-list", attrs: { id: "pbsList" } });
    const body = PB.el("div", { class: "pbs-body" }, dom.error, dom.loading, dom.empty, dom.list);
    root.appendChild(bar);
    root.appendChild(body);
    buildModal();
    state.mounted = true;
    PB.on("subs", scheduleLoad);
    document.addEventListener("keydown", onKey);
    if (state.queuedShow) {
      state.queuedShow = false;
      show();
    } else {
      paint();
    }
  }

  function buildModal() {
    dom.formTitle = PB.el("span", { class: "pbs-dialog-title", text: "添加订阅" });
    const close = PB.el("button", {
      class: "pbs-btn",
      text: "关闭",
      attrs: { type: "button" },
      on: { click: closeForm },
    });
    const head = PB.el("div", { class: "pbs-dialog-head" }, dom.formTitle, close);
    const form = PB.el("form", {
      class: "pbs-form",
      on: { submit: function (ev) { ev.preventDefault(); saveSub(); } },
    });
    dom.fName = field(form, "名称", "pbsName", "text");
    dom.fURL = field(form, "URL", "pbsURL", "text");
    dom.fFilter = field(form, "Filter 正则", "pbsFilter", "text");
    dom.fUA = field(form, "自定义 User-Agent", "pbsUA", "text");
    dom.fUA.placeholder = "留空则使用 clash.meta";
    dom.fInterval = field(form, "更新间隔（分钟）", "pbsInterval", "number");
    dom.fInterval.min = "0";
    dom.fInterval.step = "1";
    dom.fInterval.value = "0";
    form.appendChild(PB.el("div", { class: "pbs-hint", text: "0 表示仅手动更新" }));
    dom.formError = PB.el("div", { class: "pbs-error", hidden: true });
    form.appendChild(dom.formError);
    const cancel = PB.el("button", {
      class: "pbs-btn",
      text: "取消",
      attrs: { type: "button" },
      on: { click: closeForm },
    });
    dom.saveBtn = PB.el("button", {
      class: "pbs-btn pbs-btn-primary",
      text: "保存",
      attrs: { type: "submit", id: "pbsSave" },
    });
    form.appendChild(PB.el("div", { class: "pbs-form-actions" }, cancel, dom.saveBtn));
    const dialog = PB.el("div", { class: "pbs-dialog", attrs: { role: "dialog", "aria-modal": "true" } }, head, form);
    dialog.addEventListener("click", function (ev) { ev.stopPropagation(); });
    dom.modal = PB.el("div", {
      class: "pbs-modal",
      attrs: { id: "pbsModal" },
      hidden: true,
      on: { click: closeForm },
    }, dialog);
    document.body.appendChild(dom.modal);
  }

  function show() {
    if (!state.mounted) {
      state.queuedShow = true;
      return;
    }
    loadSubs();
  }

  function hide() {}

  function onKey(ev) {
    if (ev.key === "Escape" && dom.modal && !dom.modal.hidden) closeForm();
  }

  function scheduleLoad() {
    clearTimeout(subTimer);
    subTimer = setTimeout(function () { loadSubs(); }, 300);
  }

  function loadSubs() {
    const gen = ++loadGen;
    const first = state.subs.length === 0;
    state.loading = first;
    if (first) state.error = "";
    paint();
    return PB.api("GET", "/api/subs").then(function (data) {
      if (gen !== loadGen) return;
      state.subs = data && Array.isArray(data.subscriptions) ? data.subscriptions.map(normalizeSub) : [];
      state.error = "";
    }).catch(function (err) {
      if (gen !== loadGen) return;
      state.error = (err && err.message) || "读取订阅失败";
    }).then(function () {
      if (gen !== loadGen) return;
      state.loading = false;
      paint();
    });
  }

  function reloadQuiet() {
    return PB.api("GET", "/api/subs").then(function (data) {
      if (data && Array.isArray(data.subscriptions)) state.subs = data.subscriptions.map(normalizeSub);
    }).catch(function () {});
  }

  function paint() {
    if (!state.mounted) return;
    dom.count.textContent = "共 " + state.subs.length + " 个订阅";
    dom.error.hidden = !state.error;
    dom.error.textContent = state.error || "";
    dom.loading.hidden = !state.loading;
    const showEmpty = !state.loading && state.subs.length === 0;
    dom.empty.hidden = !showEmpty;
    dom.updateAllBtn.disabled = state.busyAll || state.subs.length === 0;
    dom.updateAllBtn.textContent = state.busyAll ? "更新中" : "全部更新";
    const frag = document.createDocumentFragment();
    for (let i = 0; i < state.subs.length; i++) frag.appendChild(subCard(state.subs[i]));
    dom.list.replaceChildren(frag);
    paintFormError();
  }

  function subCard(sub) {
    const sw = PB.el("button", {
      class: "pbs-switch" + (sub.enabled ? " is-on" : ""),
      attrs: {
        type: "button",
        role: "switch",
        "aria-checked": sub.enabled ? "true" : "false",
        "aria-label": "启用 " + (sub.name || "订阅"),
      },
      disabled: state.busyAll || state.busyId === sub.id,
      on: { click: function () { toggleSub(sub); } },
    });
    const top = PB.el("div", { class: "pbs-top" },
      PB.el("div", { class: "pbs-name", text: sub.name || "订阅" }),
      sw);
    const card = PB.el("article", { class: "pbs-card" }, top);
    card.appendChild(PB.el("div", { class: "pbs-url", text: maskURL(sub.url), title: maskURL(sub.url) }));
    card.appendChild(PB.el("div", { class: "pbs-meta", text: "节点 " + sub.nodeCount }));
    card.appendChild(PB.el("div", { class: "pbs-meta", text: fmtWhen(sub.updatedAt) }));
    card.appendChild(PB.el("div", { class: "pbs-meta", text: intervalText(sub.updateIntervalMin) }));
    appendUsage(card, sub.usage);
    if (sub.lastError) card.appendChild(PB.el("div", { class: "pbs-card-error", text: sub.lastError }));
    card.appendChild(actionsFor(sub));
    return card;
  }

  function appendUsage(card, usage) {
    if (!usage) return;
    const showBar = usage.total > 0;
    const expire = fmtExpire(usage.expire);
    if (!showBar && !expire) return;
    if (showBar) {
      const used = usage.upload + usage.download;
      const pct = Math.max(0, Math.min(100, (used / usage.total) * 100));
      const bar = PB.el("span");
      bar.style.width = pct.toFixed(1) + "%";
      if (pct >= 90) bar.className = "is-full";
      else if (pct >= 75) bar.className = "is-high";
      card.appendChild(PB.el("div", { class: "pbs-usage" }, bar));
      card.appendChild(PB.el("div", { class: "pbs-meta", text: fmtBytes(used) + " / " + fmtBytes(usage.total) }));
    }
    if (expire) card.appendChild(PB.el("div", { class: "pbs-meta", text: expire }));
  }

  function actionsFor(sub) {
    const row = PB.el("div", { class: "pbs-actions" });
    if (state.confirmId === sub.id) {
      row.appendChild(PB.el("span", { class: "pbs-confirm", text: "确认删除「" + (sub.name || "订阅") + "」？" }));
      row.appendChild(PB.el("button", {
        class: "pbs-btn pbs-btn-danger",
        text: "删除",
        attrs: { type: "button" },
        on: { click: function () { deleteSub(sub.id); } },
      }));
      row.appendChild(PB.el("button", {
        class: "pbs-btn",
        text: "取消",
        attrs: { type: "button" },
        on: { click: function () { state.confirmId = ""; paint(); } },
      }));
      return row;
    }
    row.appendChild(PB.el("button", {
      class: "pbs-btn",
      text: state.busyId === sub.id ? "更新中" : "更新",
      attrs: { type: "button" },
      disabled: state.busyAll || state.busyId === sub.id,
      on: { click: function () { updateSub(sub.id); } },
    }));
    row.appendChild(PB.el("button", {
      class: "pbs-btn",
      text: "编辑",
      attrs: { type: "button" },
      on: { click: function () { openEdit(sub); } },
    }));
    row.appendChild(PB.el("button", {
      class: "pbs-btn",
      text: "删除",
      attrs: { type: "button" },
      on: { click: function () { state.confirmId = sub.id; paint(); } },
    }));
    return row;
  }

  function paintFormError() {
    if (!dom.formError) return;
    dom.formError.hidden = !state.formError;
    dom.formError.textContent = state.formError || "";
  }

  function openCreate() {
    state.editingId = "";
    state.baseline = null;
    state.formError = "";
    dom.formTitle.textContent = "添加订阅";
    dom.fName.value = "";
    dom.fURL.value = "";
    dom.fFilter.value = "";
    dom.fUA.value = "";
    dom.fInterval.value = "0";
    paintFormError();
    dom.modal.hidden = false;
    dom.fName.focus();
  }

  function openEdit(sub) {
    state.editingId = sub.id;
    state.baseline = {
      id: sub.id,
      name: sub.name,
      url: sub.url,
      filter: sub.filter,
      userAgent: sub.userAgent,
      updateIntervalMin: sub.updateIntervalMin,
    };
    state.formError = "";
    dom.formTitle.textContent = "编辑订阅";
    dom.fName.value = sub.name;
    dom.fURL.value = sub.url;
    dom.fFilter.value = sub.filter;
    dom.fUA.value = sub.userAgent;
    dom.fInterval.value = String(sub.updateIntervalMin || 0);
    paintFormError();
    dom.modal.hidden = false;
    dom.fName.focus();
  }

  function closeForm() {
    if (dom.modal) dom.modal.hidden = true;
    state.formError = "";
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

  function diffBody(orig, form) {
    const body = { id: orig.id };
    let changed = false;
    if (form.name !== orig.name) { body.name = form.name; changed = true; }
    if (form.url !== orig.url) { body.url = form.url; changed = true; }
    if (form.filter !== orig.filter) { body.filter = form.filter; changed = true; }
    if (form.userAgent !== orig.userAgent) { body.userAgent = form.userAgent; changed = true; }
    if (form.updateIntervalMin !== orig.updateIntervalMin) {
      body.updateIntervalMin = form.updateIntervalMin;
      changed = true;
    }
    return changed ? body : null;
  }

  function saveSub() {
    const form = readForm();
    if (!validURL(form.url)) {
      state.formError = "订阅地址无效，需要 http 或 https 链接";
      paintFormError();
      return;
    }
    return locked("saveSub", function () {
      dom.saveBtn.disabled = true;
      let req;
      if (!state.editingId) {
        req = PB.api("POST", "/api/subs", form);
      } else {
        const body = diffBody(state.baseline, form);
        if (!body) {
          closeForm();
          dom.saveBtn.disabled = false;
          return;
        }
        req = PB.api("PUT", "/api/subs", body);
      }
      return req.then(function (data) {
        if (!absorb(data)) return loadSubs();
      }).then(function () {
        state.error = "";
        state.formError = "";
        closeForm();
        paint();
      }).catch(function (err) {
        state.formError = (err && err.message) || "保存失败";
        paintFormError();
      }).then(function () {
        if (dom.saveBtn) dom.saveBtn.disabled = false;
      });
    });
  }

  function toggleSub(sub) {
    return locked("en:" + sub.id, function () {
      const next = !sub.enabled;
      sub.enabled = next;
      paint();
      return PB.api("PUT", "/api/subs", { id: sub.id, enabled: next }).then(function (data) {
        if (data && data.subscription) upsert(data.subscription);
        state.error = "";
      }).catch(function (err) {
        const cur = findSub(sub.id);
        if (cur) cur.enabled = !next;
        state.error = (err && err.message) || "切换失败";
      }).then(function () {
        paint();
      });
    });
  }

  function deleteSub(id) {
    return locked("del:" + id, function () {
      return PB.api("DELETE", "/api/subs", { id: id }).then(function () {
        state.subs = state.subs.filter(function (sub) { return sub.id !== id; });
        state.confirmId = "";
        state.error = "";
      }).catch(function (err) {
        state.error = (err && err.message) || "删除失败";
      }).then(function () {
        paint();
      });
    });
  }

  function updateSub(id) {
    return locked("up:" + id, function () {
      state.busyId = id;
      paint();
      return PB.api("POST", "/api/subs/update", { id: id }).then(function (data) {
        absorb(data);
        state.error = "";
      }).catch(function (err) {
        state.error = (err && err.message) || "更新失败";
        return reloadQuiet();
      }).then(function () {
        state.busyId = "";
        paint();
      });
    });
  }

  function updateAll() {
    return locked("upAll", function () {
      state.busyAll = true;
      paint();
      return PB.api("POST", "/api/subs/update", {}).then(function (data) {
        absorb(data);
        state.error = resultErrors(data);
      }).catch(function (err) {
        state.error = (err && err.message) || "更新失败";
        return reloadQuiet();
      }).then(function () {
        state.busyAll = false;
        paint();
      });
    });
  }

  PB.registerTab("subs", {
    mount: mount,
    show: show,
    hide: hide,
  });
})();
