(function () {
  "use strict";

  var NAMES = {
    local_port: "本地端口",
    exit_region: "出口 IP/地区",
    cursor_api: "Cursor",
    claude_api: "Claude",
    openai_api: "OpenAI",
    antigravity_api: "Antigravity"
  };
  var RANK = { error: 0, warn: 1, ok: 2 };
  var channel = "ai";
  var onlyIssues = false;
  var report = null;
  var busy = false;
  var visible = false;
  var rootEl = null;
  var countsEl = null;
  var exitEl = null;
  var tableEl = null;
  var errorEl = null;
  var channelSeg = null;
  var channelAI = null;
  var channelDaily = null;
  var issueSwitch = null;
  var issueLabel = null;
  var againBtn = null;

  function latency(item) {
    if (item.id === "local_port" && item.latencyMs <= 1) return item.status === "ok" ? "—" : "";
    return item.latencyMs > 0 ? PB.fmt.ms(item.latencyMs) : "—";
  }

  function statusMeta(status) {
    if (status === "warn") return { kind: "warn", text: "警告", mark: "is-warn" };
    if (status === "error") return { kind: "err", text: "失败", mark: "is-error" };
    return { kind: "ok", text: "正常", mark: "is-ok" };
  }

  function ensureChrome() {
    if (channelSeg) return;
    channelAI = PB.el("button", {
      class: "pb-seg-btn is-on",
      attrs: { "data-channel": "ai", "aria-pressed": "true" },
      text: "AI 专属",
      on: { click: function () { pick("ai"); } }
    });
    channelDaily = PB.el("button", {
      class: "pb-seg-btn",
      attrs: { "data-channel": "daily", "aria-pressed": "false" },
      text: "日常代理",
      on: { click: function () { pick("daily"); } }
    });
    channelSeg = PB.el("div", {
      class: "pb-seg",
      attrs: { role: "group", "aria-label": "测试通道" }
    }, channelAI, channelDaily);
    issueLabel = PB.el("span", { class: "pb-muted", text: "仅看异常" });
    issueSwitch = PB.el("button", {
      class: "pb-switch",
      attrs: { role: "switch", "aria-checked": "false", "aria-label": "仅看异常" },
      on: {
        click: function () {
          onlyIssues = !onlyIssues;
          paint();
          renderActions();
        }
      }
    });
    againBtn = PB.el("button", {
      class: "pb-btn pb-btn-primary",
      text: "重新诊断",
      on: { click: run }
    });
  }

  function renderActions() {
    ensureChrome();
    var aiOn = channel === "ai";
    channelAI.classList.toggle("is-on", aiOn);
    channelDaily.classList.toggle("is-on", !aiOn);
    channelAI.setAttribute("aria-pressed", aiOn ? "true" : "false");
    channelDaily.setAttribute("aria-pressed", aiOn ? "false" : "true");
    issueSwitch.setAttribute("aria-checked", onlyIssues ? "true" : "false");
    againBtn.disabled = busy;
    againBtn.textContent = busy ? "诊断中" : "重新诊断";
    if (!visible) return;
    var slot = document.getElementById("pb-actions");
    if (!slot || channelSeg.parentNode !== slot) {
      PB.setActions([channelSeg, issueLabel, issueSwitch, againBtn]);
      PB.seg(channelSeg);
    }
    PB.seg.sync(channelSeg);
  }

  function pick(next) {
    if (channel === next) return;
    channel = next;
    renderActions();
    run();
  }

  function paintExit() {
    exitEl.replaceChildren();
    var rep = report || {};
    var ports = PB.ports();
    var port = channel === "ai" ? ports.ai : ports.mixed;
    var channelName = channel === "ai" ? "AI 专属" : "日常代理";
    var allowed = !!rep.regionAllowed;
    var badgeKind = rep.exitIp ? (allowed ? "ok" : "err") : "muted";
    var badgeText = rep.exitIp ? (allowed ? "合规" : "受限") : "无结果";
    exitEl.appendChild(PB.el("div", {},
      PB.el("div", { class: "pb-kicker pb-num", text: channelName + " :" + port }),
      PB.el("div", {}, "出口 IP ", PB.el("b", { class: "pb-num", text: rep.exitIp || "—" }))
    ));
    exitEl.appendChild(PB.el("div", {},
      PB.el("div", { class: "pb-kicker", text: "地区" }),
      PB.el("div", {}, PB.el("b", { text: rep.exitRegionName || rep.exitRegion || "—" }))
    ));
    exitEl.appendChild(PB.el("span", { class: "pb-badge pb-badge-" + badgeKind, text: badgeText }));
  }

  function paint() {
    if (!tableEl) return;
    var items = report && Array.isArray(report.items) ? report.items.slice() : [];
    var okCount = items.filter(function (i) { return i.status === "ok"; }).length;
    var warnCount = items.filter(function (i) { return i.status === "warn"; }).length;
    var errCount = items.filter(function (i) { return i.status === "error"; }).length;
    countsEl.replaceChildren(
      PB.el("span", { class: "pb-num", text: okCount + " 项正常" }),
      PB.el("span", { class: "pb-num", text: warnCount + " 项警告" }),
      PB.el("span", { class: "pb-num", text: errCount + " 项失败" })
    );
    if (errCount) PB.setDot("diag", "err");
    else if (warnCount) PB.setDot("diag", "warn");
    else if (items.length) PB.setDot("diag", "ok");
    paintExit();
    items.sort(function (a, b) { return (RANK[a.status] == null ? 3 : RANK[a.status]) - (RANK[b.status] == null ? 3 : RANK[b.status]); });
    var filtered = onlyIssues ? items.filter(function (i) { return i.status !== "ok"; }) : items;
    tableEl.replaceChildren();
    tableEl.appendChild(PB.el("div", { class: "pb-tr pb-th", attrs: { role: "row" } },
      PB.el("div", { attrs: { role: "columnheader" }, text: "项目" }),
      PB.el("div", { attrs: { role: "columnheader" }, text: "目标" }),
      PB.el("div", { attrs: { role: "columnheader" }, text: "延迟" }),
      PB.el("div", { attrs: { role: "columnheader" }, text: "结果" })
    ));
    errorEl.hidden = true;
    errorEl.textContent = "";
    if (!report) {
      tableEl.appendChild(PB.el("p", { class: "pb-muted", text: busy ? "正在诊断" : "尚未诊断" }));
      return;
    }
    if (!filtered.length) {
      tableEl.appendChild(PB.el("p", { class: "pb-muted", text: onlyIssues ? "当前没有异常项" : "没有诊断结果" }));
      return;
    }
    filtered.forEach(function (item) {
      var meta = statusMeta(item.status);
      var open = item.status !== "ok";
      var detail = PB.el("div", { class: "pb-diag-detail", text: item.detail || "", hidden: !open });
      var row = PB.el("button", {
        class: "pb-tr pb-diag-main",
        attrs: { "aria-expanded": open ? "true" : "false" },
        on: {
          click: function () {
            var next = row.getAttribute("aria-expanded") !== "true";
            row.setAttribute("aria-expanded", next ? "true" : "false");
            detail.hidden = !next;
          }
        }
      },
        PB.el("span", { text: NAMES[item.id] || item.name || "检查项" }),
        PB.el("span", { class: "pb-mono pb-ellipsis", text: item.target || "", title: item.target || "" }),
        PB.el("span", { class: "pb-mono pb-num", text: latency(item) }),
        PB.el("span", { class: "pb-badge pb-badge-" + meta.kind, text: meta.text })
      );
      tableEl.appendChild(PB.el("div", { class: "pb-diag-item " + meta.mark }, row, detail));
    });
  }

  function paintError(message) {
    report = null;
    countsEl.replaceChildren();
    exitEl.replaceChildren();
    tableEl.replaceChildren();
    errorEl.hidden = false;
    errorEl.textContent = message || "诊断失败";
    PB.setDot("diag", "err");
  }

  function run() {
    if (busy) return;
    busy = true;
    renderActions();
    if (!report) paint();
    if (errorEl) {
      errorEl.hidden = true;
      errorEl.textContent = "";
    }
    PB.api("POST", "/api/diagnostics", { channel: channel }).then(function (rep) {
      report = rep || {};
      paint();
    }).catch(function (err) {
      paintError((err && err.message) || "诊断失败");
      PB.toast((err && err.message) || "诊断失败", "err");
    }).finally(function () {
      busy = false;
      renderActions();
    });
  }

  function mount(root) {
    rootEl = root;
    countsEl = PB.el("div", { class: "pb-counts" });
    exitEl = PB.el("div", { class: "pb-exit" });
    tableEl = PB.el("div", { class: "pb-stack", attrs: { role: "table", "aria-label": "诊断结果" } });
    errorEl = PB.el("p", { class: "pb-error", hidden: true });
    root.appendChild(PB.el("div", { class: "pb-stack" },
      PB.el("section", { class: "pb-card" }, exitEl),
      countsEl,
      errorEl,
      tableEl
    ));
  }

  function show() {
    visible = true;
    renderActions();
    if (!report) run();
    else paint();
  }

  PB.on("sync", function (msg) {
    if (msg && msg.shouldDiag && visible) run();
  });
  PB.addTab("diag", {
    mount: mount,
    show: show,
    hide: function () { visible = false; },
    onCoreChange: function () { if (report) paint(); }
  });
})();
