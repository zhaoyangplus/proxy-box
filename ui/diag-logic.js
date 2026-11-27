(function (root, factory) {
  "use strict";
  var api = factory();
  if (typeof module !== "undefined" && module && module.exports) module.exports = api;
  if (root) root.PBDiag = api;
})(typeof window !== "undefined" ? window : globalThis, function () {
  "use strict";

  function util() {
    var u = globalThis.PBUtil;
    if (!u) throw new Error("ui/util.js must load before diag-logic.js");
    return u;
  }

  function portNumber(port) {
    if (typeof port === "number" && Number.isInteger(port) && port >= 1 && port <= 65535) return port;
    if (typeof port === "string" && /^\d{1,5}$/.test(port)) {
      var n = Number(port);
      if (n >= 1 && n <= 65535) return n;
    }
    return 0;
  }

  function mapStatus(status) {
    if (status === "ok") return "ok";
    if (status === "warn") return "warn";
    return "fail";
  }

  function ringTotals(rows) {
    var list = rows || [];
    var pass = 0;
    var warn = 0;
    var fail = 0;
    list.forEach(function (row) {
      var status = row && row.status;
      if (status === "ok") pass += 1;
      else if (status === "warn") warn += 1;
      else fail += 1;
    });
    var total = list.length;
    var circ = 2 * Math.PI * 24;
    var frac = total ? pass / total : 0;
    return {
      pass: pass,
      warn: warn,
      fail: fail,
      total: total,
      issues: warn + fail,
      label: pass + "/" + total,
      circumference: circ,
      dashOffset: circ * (1 - frac),
      color: fail ? "var(--red)" : warn ? "var(--orange)" : "var(--green)"
    };
  }

  function mapReport(report) {
    var src = report || {};
    var items = Array.isArray(src.items) ? src.items : [];
    var rows = items.map(function (item, index) {
      item = item || {};
      var latency = Number(item.latencyMs);
      if (!isFinite(latency) || latency < 0) latency = 0;
      return {
        id: item.id ? String(item.id) : "item-" + index,
        name: item.name == null ? "" : String(item.name),
        target: item.target == null ? "" : String(item.target),
        status: mapStatus(item.status),
        latencyMs: latency,
        detail: item.detail == null ? "" : String(item.detail)
      };
    });
    return {
      channel: src.channel === "daily" ? "daily" : "ai",
      port: portNumber(src.port),
      exitIp: src.exitIp == null ? "" : String(src.exitIp),
      exitRegionName: src.exitRegionName == null ? "" : String(src.exitRegionName),
      checkedAt: src.checkedAt == null ? "" : String(src.checkedAt),
      rows: rows,
      totals: ringTotals(rows)
    };
  }

  function badgeFor(report) {
    if (!report || !report.totals || !report.totals.issues) return { text: "", cls: "" };
    return { text: String(report.totals.issues), cls: report.totals.fail ? "err" : "warn" };
  }

  function visibleRows(rows, onlyIssues) {
    var list = rows || [];
    if (!onlyIssues) return list;
    return list.filter(function (row) { return row.status === "warn" || row.status === "fail"; });
  }

  function latencyLabel(row) {
    if (!(row.latencyMs > 0)) return row.status === "fail" ? "失败" : "\u2014";
    return Math.round(row.latencyMs) + " ms";
  }

  function latencyClass(row) {
    if (!(row.latencyMs > 0)) return row.status === "ok" ? "" : "d-bad";
    if (row.latencyMs <= 120) return "d-good";
    if (row.latencyMs <= 250) return "d-mid";
    return "d-bad";
  }

  function markClass(status) {
    if (status === "ok") return "ok";
    if (status === "warn") return "warn";
    return "err";
  }

  function iconName(status) {
    if (status === "ok") return "check";
    if (status === "warn") return "warn";
    return "x";
  }

  function isOpen(open, channel, id) {
    var key = channel + ":" + id;
    return !!(open && Object.prototype.hasOwnProperty.call(open, key) && open[key]);
  }

  function rowHTML(row, channel, open) {
    var u = util();
    var opened = isOpen(open, channel, row.id);
    return '<div class="drow' + (opened ? " open" : "") + '" data-k="' + u.esc(row.id) + '">' +
      '<button type="button" class="drow-btn" aria-expanded="' + (opened ? "true" : "false") + '">' +
        '<span class="st ' + markClass(row.status) + '">' + u.ic(iconName(row.status)) + "</span>" +
        '<span class="drow-name">' + u.esc(row.name) + "</span>" +
        '<span class="drow-target mono ellipsis">' + u.esc(row.target) + "</span>" +
        '<span class="drow-ms ' + latencyClass(row) + '">' + u.esc(latencyLabel(row)) + "</span>" +
        u.ic("chev-right", "chev") +
      "</button>" +
      '<div class="drow-detail"><div><p>' + u.esc(row.detail) + "</p></div></div></div>";
  }

  function pageHTML(report, state) {
    var u = util();
    state = state || {};
    report = report || { rows: [], totals: ringTotals([]) };
    var channel = report.channel === "daily" ? "daily" : "ai";
    var totals = report.totals && isFinite(report.totals.circumference) ? report.totals : ringTotals(report.rows);
    var color = totals.color === "var(--red)" || totals.color === "var(--orange)" ? totals.color : "var(--green)";
    var port = portNumber(report.port);
    var host = port ? "127.0.0.1:" + port : "";
    var exit = [report.exitIp, report.exitRegionName].filter(Boolean).join(" · ");
    var caption = "出口 " + (exit || "\u2014");
    if (report.checkedAt) caption += " · 检测于 " + report.checkedAt;
    var issues = totals.issues || 0;
    var pill = !totals.total
      ? '<span class="pill">没有检测项</span>'
      : !issues
        ? '<span class="pill green">' + u.ic("check") + "全部正常</span>"
        : '<span class="pill ' + (totals.fail ? "red" : "orange") + '">' + issues + " 项异常</span>";
    var rows = visibleRows(report.rows, state.onlyIssues);
    var list = rows.length
      ? rows.map(function (row) { return rowHTML(row, channel, state.open); }).join("")
      : '<div class="row"><span class="caption">' + (state.onlyIssues ? "没有异常项，所有检测都通过了。" : "没有检测项。") + "</span></div>";
    var notice = channel === "daily"
      ? '<div class="notice" data-ch="daily">' + u.ic("info") + "<span>日常通道仅用于浏览等普通流量，AI 服务失败属正常现象；AI 工具始终走 AI 专属通道。</span></div>"
      : "";
    return '<div class="diag" data-ch="' + channel + '">' +
      '<section class="card diag-sum">' +
        '<div class="ring" style="--ring-c:' + color + '"><svg viewBox="0 0 60 60">' +
          '<circle class="trk" cx="30" cy="30" r="24"></circle>' +
          '<circle class="val" cx="30" cy="30" r="24" stroke-dasharray="' + totals.circumference.toFixed(1) + '" stroke-dashoffset="' + totals.dashOffset.toFixed(1) + '"></circle>' +
        "</svg><b>" + u.esc(totals.label) + "</b></div>" +
        '<div class="diag-info"><div class="card-t">' + (channel === "ai" ? "AI 专属通道" : "日常代理通道") +
          (host ? ' · <span class="mono">' + u.esc(host) + "</span>" : "") + "</div>" +
          '<div class="caption">' + u.esc(caption) + "</div><div>" + pill + "</div></div>" +
        '<label class="only-issues" data-act="only">只看异常' +
          u.sw(!!state.onlyIssues, 'id="dgOnly" aria-label="只看异常"', !!state.inflight, "sm") + "</label>" +
      "</section>" + notice +
      '<section class="list diag-list">' + list + "</section></div>";
  }

  function blockedHTML(message) {
    var u = util();
    var text = String(message == null ? "" : message).trim();
    if (!text) {
      return '<div class="empty"><div class="empty-ic">' + u.ic("pulse") + '</div><div class="empty-t">正在诊断…</div></div>';
    }
    return '<div class="empty"><div class="empty-ic">' + u.ic("warn") + '</div><div class="empty-t">无法诊断</div>' +
      '<p class="caption">' + u.esc(text) + "</p>" +
      '<button type="button" class="btn sm prominent" data-act="overview">前往总览</button></div>';
  }

  function emptySlot() {
    return { run: 0, running: false, report: null, error: "" };
  }

  function channelName(channel) {
    return channel === "daily" ? "daily" : "ai";
  }

  function copySlot(slot) {
    slot = slot || emptySlot();
    return {
      run: slot.run || 0,
      running: !!slot.running,
      report: slot.report || null,
      error: slot.error ? String(slot.error) : ""
    };
  }

  function emptyRuns() {
    return { ai: emptySlot(), daily: emptySlot() };
  }

  function cloneRuns(book) {
    book = book || emptyRuns();
    return { ai: copySlot(book.ai), daily: copySlot(book.daily) };
  }

  function beginRun(book, channel) {
    var next = cloneRuns(book);
    var name = channelName(channel);
    next[name].run += 1;
    next[name].running = true;
    next[name].error = "";
    return { book: next, channel: name, run: next[name].run };
  }

  function finishRun(book, channel, run, payload) {
    var name = channelName(channel);
    var current = cloneRuns(book);
    if (!current[name] || current[name].run !== run) return { book: current, applied: false };
    payload = payload || {};
    current[name].running = false;
    if (payload.ok) {
      current[name].report = payload.report || null;
      current[name].error = "";
    } else if (!current[name].report) {
      current[name].error = payload.error ? String(payload.error) : "";
    }
    return { book: current, applied: true };
  }

  function viewRun(book, channel) {
    var slot = copySlot(book && book[channelName(channel)]);
    var status = slot.running ? "running" : slot.report ? "result" : slot.error ? "error" : "idle";
    return { status: status, running: slot.running, report: slot.report, error: slot.error };
  }

  function clearRuns(book) {
    var next = cloneRuns(book);
    ["ai", "daily"].forEach(function (name) {
      next[name] = { run: next[name].run + 1, running: false, report: null, error: "" };
    });
    return next;
  }

  function actionsHTML(channel) {
    var u = util();
    return u.segHTML("dgCh", [
      { v: "ai", html: '<span class="ch-dot ai"></span>AI 专属' },
      { v: "daily", html: '<span class="ch-dot daily"></span>日常代理' }
    ], channel === "daily" ? "daily" : "ai", "sm tinted") +
      '<button type="button" class="btn sm prominent" id="dgRun">' + u.ic("refresh") + "重新诊断</button>";
  }

  return {
    portNumber: portNumber,
    mapStatus: mapStatus,
    ringTotals: ringTotals,
    mapReport: mapReport,
    badgeFor: badgeFor,
    visibleRows: visibleRows,
    pageHTML: pageHTML,
    blockedHTML: blockedHTML,
    actionsHTML: actionsHTML,
    emptyRuns: emptyRuns,
    beginRun: beginRun,
    finishRun: finishRun,
    viewRun: viewRun,
    clearRuns: clearRuns
  };
});
