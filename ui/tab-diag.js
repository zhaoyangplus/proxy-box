(function () {
  "use strict";
  var L = window.PBDiag;
  if (!L) throw new Error("ui/diag-logic.js must load before ui/tab-diag.js");

  var rootEl = null;
  var channel = "ai";
  var runs = L.emptyRuns();
  var stoppedError = "";
  var openMap = Object.create(null);
  var onlyIssues = false;
  var sawRunning = null;

  function coreRunning() {
    return !!(PB.state.core && PB.state.core.running);
  }

  function view() {
    return L.viewRun(runs, channel);
  }

  function paintBadge() {
    var badge = L.badgeFor(view().report);
    PB.setBadge("diag", badge.text, badge.cls);
  }

  function paintRun() {
    var btn = document.getElementById("dgRun");
    if (!btn) return;
    var busy = view().running;
    btn.disabled = busy || !coreRunning();
    btn.innerHTML = busy ? '<span class="spin xs"></span>诊断中…' : PB.ic("refresh") + "重新诊断";
  }

  function render() {
    paintBadge();
    paintRun();
    if (!rootEl || PB.current() !== "diag") return;
    var current = view();
    PB.setChannel(channel);
    if (!coreRunning()) {
      rootEl.innerHTML = L.blockedHTML(current.running ? "" : stoppedError);
      return;
    }
    if (!current.report) {
      rootEl.innerHTML = L.blockedHTML(!current.running && current.error ? current.error : "");
      return;
    }
    rootEl.innerHTML = L.pageHTML(current.report, { onlyIssues: onlyIssues, open: openMap, inflight: current.running });
  }

  function run(fromUser) {
    var started = L.beginRun(runs, channel);
    runs = started.book;
    var asked = started.channel;
    var token = started.run;
    render();
    PB.api("POST", "/api/diagnostics", { channel: asked }).then(function (data) {
      var report = L.mapReport(data);
      report.channel = asked;
      var done = L.finishRun(runs, asked, token, { ok: true, report: report });
      if (!done.applied) return;
      runs = done.book;
      if (coreRunning()) {
        stoppedError = "";
        report.rows.forEach(function (row) {
          if (row.status !== "ok") openMap[asked + ":" + row.id] = true;
        });
        if (fromUser && asked === channel) {
          var name = asked === "ai" ? "AI 专属通道" : "日常代理通道";
          var issues = report.totals.issues;
          PB.toast(
            issues ? name + "诊断完成，" + issues + " 项异常" : name + "诊断完成，全部 " + report.totals.total + " 项正常",
            { tone: issues ? (report.totals.fail ? "err" : "warn") : "ok" }
          );
        }
      }
      render();
    }).catch(function (err) {
      var message = (err && err.message) || "诊断失败";
      var hadReport = !!(runs[asked] && runs[asked].report);
      var done = L.finishRun(runs, asked, token, { ok: false, error: coreRunning() ? message : "" });
      if (!done.applied) return;
      runs = done.book;
      if (!coreRunning()) stoppedError = message;
      else if (hadReport && fromUser && asked === channel) PB.toast(message, { tone: "err" });
      render();
    });
  }

  function onCore() {
    var running = coreRunning();
    var first = sawRunning == null;
    var changed = first || sawRunning !== running;
    sawRunning = running;
    if (!changed) return;
    if (!running || !first) {
      if (running) stoppedError = "";
      runs = L.clearRuns(runs);
    }
    if (PB.current() !== "diag") {
      paintBadge();
      return;
    }
    var state = view();
    if (state.running) {
      render();
      return;
    }
    if (!running) {
      if (!stoppedError) run(false);
      else render();
      return;
    }
    if (state.status === "idle") run(false);
    else render();
  }

  function onClick(event) {
    var node = event.target && event.target.closest ? event.target : null;
    if (!node || !node.closest) return;
    if (node.closest("[data-act='overview']")) {
      PB.go("overview");
      return;
    }
    if (node.closest("[data-act='only']")) {
      if (view().running) return;
      onlyIssues = !onlyIssues;
      render();
      return;
    }
    var btn = node.closest(".drow-btn");
    if (!btn) return;
    var row = btn.closest(".drow");
    if (!row) return;
    var key = channel + ":" + row.getAttribute("data-k");
    if (openMap[key]) delete openMap[key];
    else openMap[key] = true;
    render();
  }

  function bindTop() {
    PB.topbar({ actions: L.actionsHTML(channel) });
    PB.mountSeg(document.getElementById("dgCh"), function (value) {
      if ((value !== "ai" && value !== "daily") || value === channel) return;
      channel = value;
      PB.setChannel(channel);
      render();
      if (coreRunning() && view().status === "idle") run(false);
    });
    var bar = document.getElementById("tbActions");
    if (!bar) return;
    bar.onclick = function (event) {
      var node = event.target && event.target.closest ? event.target : null;
      if (node && node.closest && node.closest("#dgRun")) run(true);
    };
  }

  function enter(root) {
    rootEl = root;
    PB.setChannel(channel);
    bindTop();
    root.onclick = onClick;
    if (sawRunning == null) {
      render();
      return;
    }
    var state = view();
    if (!coreRunning()) {
      if (!stoppedError && !state.running) run(false);
      else render();
      return;
    }
    if (state.status === "idle") run(false);
    else render();
  }

  PB.registerTab("diag", {
    enter: enter,
    onCore: onCore
  });
})();
