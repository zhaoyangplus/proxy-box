(function (root, factory) {
  "use strict";
  var api = factory();
  if (typeof module !== "undefined" && module && module.exports) module.exports = api;
  if (root) root.PBUpdate = api;
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  function util() {
    var host = typeof globalThis !== "undefined" ? globalThis : {};
    return host.PBUtil || null;
  }

  function esc(value) {
    var U = util();
    if (U && U.esc) return U.esc(value);
    return String(value == null ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  function ic(name) {
    var U = util();
    if (U && U.ic) return U.ic(name);
    return "";
  }

  function bytes(n) {
    var U = util();
    if (U && U.bytes) return U.bytes(n);
    var value = Number(n);
    if (!isFinite(value) || value < 0) return "0 B";
    if (value < 1024) return Math.round(value) + " B";
    var units = ["KB", "MB", "GB", "TB"];
    var size = value / 1024;
    var index = 0;
    while (size >= 1024 && index < units.length - 1) {
      size /= 1024;
      index += 1;
    }
    return size.toFixed(1) + " " + units[index];
  }

  function cssPercent(n) {
    var value = Number(n);
    if (!isFinite(value) || value < 0) value = 0;
    if (value > 100) value = 100;
    return value.toFixed(1) + "%";
  }

  function loading() {
    return { kind: "loading" };
  }

  function fromError(message) {
    return { kind: "error", message: message || "检查更新失败" };
  }

  function fromCheck(result) {
    result = result || {};
    if (!result.latestVersion && !result.currentVersion) return fromError("没有收到版本信息");
    var base = {
      current: result.currentVersion || "",
      latest: result.latestVersion || result.currentVersion || "",
      notes: result.releaseNotes || "",
      releaseUrl: result.releaseUrl || "",
      downloadUrl: result.downloadUrl || "",
      targetVersion: result.latestVersion || "",
      assetSize: Number(result.assetSize) || 0,
      publishedAt: result.publishedAt || ""
    };
    if (!result.hasUpdate) {
      base.kind = "current";
      return base;
    }
    base.kind = "available";
    return base;
  }

  function fromProgress(prev, progress) {
    progress = progress || {};
    prev = prev || {};
    var status = progress.status || "";
    if (status === "error") return fromError(progress.error || progress.message || "更新失败");
    if (status !== "downloading" && status !== "checking" && status !== "applying" && status !== "restarting") return prev.kind ? prev : loading();
    var total = Number(progress.totalBytes) || Number(prev.assetSize) || 0;
    var downloaded = Number(progress.downloadedBytes) || 0;
    var percent = Number(progress.percent);
    if (!isFinite(percent)) percent = total > 0 ? (downloaded / total) * 100 : 0;
    var message = progress.message || "";
    if (!message && status === "applying") message = "正在应用更新";
    if (!message && status === "restarting") message = "正在重启";
    if (!message) message = "正在下载…";
    return {
      kind: "progress",
      status: status,
      current: prev.current || progress.currentVersion || "",
      latest: progress.targetVersion || prev.latest || "",
      message: message,
      percent: Math.max(0, Math.min(100, percent)),
      downloaded: downloaded,
      total: total,
      releaseUrl: prev.releaseUrl || "",
      downloadUrl: prev.downloadUrl || "",
      targetVersion: progress.targetVersion || prev.targetVersion || prev.latest || "",
      assetSize: total || prev.assetSize || 0
    };
  }

  function sameUpdate(a, b) {
    if (!a || !b) return false;
    return a.kind === b.kind && a.status === b.status && a.message === b.message && a.percent === b.percent && a.downloaded === b.downloaded && a.total === b.total && a.latest === b.latest;
  }

  function reconcileUpdate(view, progress) {
    progress = progress || {};
    var status = progress.status || "idle";
    var active = status === "downloading" || status === "checking" || status === "applying" || status === "restarting";
    if (status === "error") {
      var failed = fromError(progress.error || progress.message || "更新失败");
      return { view: failed, changed: !sameUpdate(view, failed), polling: false };
    }
    if (active) {
      var next = fromProgress(view, progress);
      next.confirmed = true;
      var changed = !sameUpdate(view, next) || !(view && view.confirmed);
      return { view: changed ? next : view, changed: changed, polling: true };
    }
    if (view && view.kind === "progress" && view.confirmed) {
      return { view: fromError(progress.error || progress.message || "更新已中断"), changed: true, polling: false };
    }
    return { view: view, changed: false, polling: !!(view && view.kind === "progress") };
  }

  function notesHTML(notes) {
    var lines = String(notes || "").split(/\r?\n/);
    var items = [];
    for (var i = 0; i < lines.length; i++) {
      var line = lines[i].replace(/^\s*[-*]\s+/, "").trim();
      if (line) items.push("<li>" + esc(line) + "</li>");
    }
    if (!items.length) return "";
    return '<div class="notes"><h4>更新内容</h4><ul>' + items.join("") + "</ul></div>";
  }

  function whenText(iso) {
    var U = util();
    if (!iso || !U || !U.date) return "";
    return U.date(iso);
  }

  function releasesButton() {
    return '<button type="button" class="btn plain" id="updReleases">打开发布页</button>';
  }

  function bodyHTML(view) {
    view = view || loading();
    if (view.kind === "loading") {
      return '<div class="upd-center"><span class="spin lg"></span><span class="caption">正在检查 GitHub Releases…</span></div>';
    }
    if (view.kind === "error") {
      return '<div class="upd-center">' + ic("warn") + '<span class="caption">' + esc(view.message) + "</span></div>" +
        '<div class="sheet-f"><button type="button" class="btn prominent" id="updRetry">重试</button>' + releasesButton() + "</div>";
    }
    if (view.kind === "current") {
      return '<div class="upd-center"><span class="st ok">' + ic("check") + '</span><span class="upd-ver">' + esc(view.current || view.latest) + '</span><span class="caption">当前已是最新版本</span></div>' +
        '<div class="sheet-f"><button type="button" class="btn plain" data-close>关闭</button>' + releasesButton() + "</div>";
    }
    if (view.kind === "progress") {
      var amount = view.total > 0 ? bytes(view.downloaded) + " / " + bytes(view.total) : "";
      return '<div class="upd-hero"><span class="upd-ver">' + esc(view.latest) + '</span><span class="caption">' + esc(view.message) + "</span></div>" +
        '<div class="upd-prog"><div class="bar"><i style="width:' + cssPercent(view.percent) + '"></i></div>' +
        (amount ? '<div class="usage-row"><span class="caption">' + esc(amount) + "</span></div>" : "") + "</div>";
    }
    var size = view.assetSize > 0 ? bytes(view.assetSize) : "";
    var when = whenText(view.publishedAt);
    var meta = [size, when].filter(Boolean).join(" · ");
    return '<div class="upd-hero"><span class="upd-ver">' + esc(view.latest) + '</span><span class="pill green">新版本</span>' +
      (meta ? '<span class="caption">' + esc(meta) + "</span>" : "") + "</div>" +
      notesHTML(view.notes) +
      '<div class="sheet-f"><button type="button" class="btn plain" data-close>稍后</button>' + releasesButton() +
      '<button type="button" class="btn prominent" id="updGo">' + ic("download") + "下载并更新</button></div>";
  }

  return {
    loading: loading,
    fromError: fromError,
    fromCheck: fromCheck,
    fromProgress: fromProgress,
    reconcileUpdate: reconcileUpdate,
    notesHTML: notesHTML,
    bodyHTML: bodyHTML,
    cssPercent: cssPercent
  };
});
