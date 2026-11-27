(function () {
  "use strict";
  var L = window.PBDiag;
  if (!L) throw new Error("ui/diag-logic.js must load before ui/tab-diag.js");

  var rootEl = null;
  var channel = "ai";
  var reports = { ai: null, daily: null };
  var failures = { ai: "", daily: "" };
  var stoppedError = "";
  var openMap = Object.create(null);
  var onlyIssues = false;
  var inflight = false;
  var again = false;
  var sawRunning = null;

  function coreRunning() {
    return !!(PB.state.core && PB.state.core.running);
  }

  function paintBadge() {
    var badge = L.badgeFor(reports[channel]);
    PB.setBadge("diag", badge.text, badge.cls);
  }

  function paintRun() {
    var btn = document.getElementById("dgRun");
    if (!btn) return;
    btn.disabled = inflight || !coreRunning();
    btn.innerHTML = inflight ? '<span class="spin xs"></span>诊断中…' : PB.ic("refresh") + "重新诊断";
  }

  function render() {
    paintBadge();
    paintRun();
    if (!rootEl || PB.current() !== "diag") return;
    PB.setChannel(channel);
    if (!coreRunning()) {
      rootEl.innerHTML = L.blockedHTML(inflight ? "" : stoppedError);
      return;
    }
    var report = reports[channel];
    if (!report) {
      rootEl.innerHTML = L.blockedHTML(!inflight && failures[channel] ? failures[channel] : "");
      return;
    }
    rootEl.innerHTML = L.pageHTML(report, { onlyIssues: onlyIssues, open: openMap, inflight: inflight });
  }

  function finish() {
    inflight = false;
    if (again) {
      again = false;
      if (PB.current() === "diag") {
        if (!coreRunning()) {
          if (!stoppedError) {
            run(false);
            return;
          }
        } else if (!reports[channel]) {
          run(false);
          return;
        }
      }
    }
    render();
  }

  function run(fromUser) {
    if (inflight) return;
    var asked = channel;
    inflight = true;
    render();
    PB.api("POST", "/api/diagnostics", { channel: asked }).then(function (data) {
      var report = L.mapReport(data);
      report.channel = asked;
      reports[asked] = report;
      failures[asked] = "";
      if (coreRunning()) stoppedError = "";
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
    }).catch(function (err) {
      var message = (err && err.message) || "诊断失败";
      if (!coreRunning()) {
        reports[asked] = null;
        stoppedError = message;
      } else if (!reports[asked]) {
        failures[asked] = message;
      } else if (fromUser) {
        PB.toast(message, { tone: "err" });
      }
    }).then(finish);
  }

  function onCore() {
    var running = coreRunning();
    var first = sawRunning == null;
    var changed = first || sawRunning !== running;
    sawRunning = running;
    if (!changed) return;
    if (!running) {
      reports.ai = null;
      reports.daily = null;
    } else if (!first) {
      stoppedError = "";
      failures.ai = "";
      failures.daily = "";
      reports.ai = null;
      reports.daily = null;
    }
    if (PB.current() !== "diag") {
      paintBadge();
      return;
    }
    if (inflight) {
      again = true;
      render();
      return;
    }
    if (!running) {
      if (!stoppedError) run(false);
      else render();
      return;
    }
    if (!reports[channel]) run(false);
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
      if (inflight) return;
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
      if (coreRunning() && !reports[channel] && !inflight) run(false);
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
    if (!coreRunning()) {
      if (!stoppedError && !inflight) run(false);
      else render();
      return;
    }
    if (!reports[channel] && !inflight) run(false);
    else render();
  }

  PB.registerTab("diag", {
    enter: enter,
    onCore: onCore
  });
})();
