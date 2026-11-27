(function () {
  "use strict";

  var L = globalThis.PBUpdate;
  if (!L || typeof PB === "undefined") return;

  var cardRef = null;
  var view = L.loading();

  function box() {
    if (!cardRef || cardRef.isConnected === false) return null;
    return cardRef.querySelector("#upd");
  }

  function paint() {
    var node = box();
    if (!node) return;
    node.innerHTML = L.bodyHTML(view);
    var retry = node.querySelector("#updRetry");
    var go = node.querySelector("#updGo");
    var releases = node.querySelector("#updReleases");
    if (retry) retry.addEventListener("click", check);
    if (go) go.addEventListener("click", apply);
    if (releases) releases.addEventListener("click", openReleases);
  }

  function remember(info) {
    if (!info) return;
    PB.state.update = info;
    if (info.currentVersion) PB.state.version = info.currentVersion;
    if (info.releaseUrl && !PB.state.releasesUrl) PB.state.releasesUrl = info.releaseUrl;
    var dot = document.getElementById("verDot");
    var text = document.getElementById("verText");
    var button = document.getElementById("verBtn");
    if (text && info.currentVersion) text.textContent = info.currentVersion;
    if (dot) dot.hidden = !info.hasUpdate;
    if (button) button.title = info.hasUpdate && info.latestVersion ? "有可用更新 " + info.latestVersion : "检查更新";
  }

  function check() {
    view = L.loading();
    paint();
    PB.api("POST", "/api/update/check", {}).then(function (res) {
      remember(res);
      view = L.fromCheck(res);
      paint();
    }).catch(function (err) {
      view = L.fromError((err && err.message) || "检查更新失败");
      paint();
    });
  }

  function apply() {
    var payload = {
      downloadUrl: view.downloadUrl || "",
      targetVersion: view.targetVersion || view.latest || "",
      assetSize: view.assetSize || 0
    };
    view = L.fromProgress(view, {
      status: "downloading",
      message: "正在下载…",
      percent: 0,
      targetVersion: payload.targetVersion,
      totalBytes: payload.assetSize
    });
    paint();
    PB.api("POST", "/api/update/apply", payload).catch(function (err) {
      view = L.fromError((err && err.message) || "下载失败");
      paint();
    });
  }

  function openReleases() {
    var url = (view && view.releaseUrl) || PB.state.releasesUrl || "";
    if (!url) {
      PB.toast("没有发布页地址", "err");
      return;
    }
    PB.api("POST", "/api/open-url", { url: url }).then(function () {
      PB.toast("已打开发布页");
    }).catch(function (err) {
      PB.toast((err && err.message) || "打开发布页失败", "err");
    });
  }

  PB.openUpdate = function () {
    var current = (PB.state && PB.state.version) || "";
    PB.openSheet({
      title: "软件更新",
      sub: current ? "当前版本 " + current : "正在读取当前版本",
      icon: '<span class="sheet-ic">' + PB.ic("download") + "</span>",
      body: '<div id="upd"></div>',
      onMount: function (card) {
        cardRef = card;
        view = L.loading();
        paint();
        check();
      }
    });
  };

  PB.on("update", function (msg) {
    if (!box()) return;
    view = L.fromProgress(view, (msg && msg.update) || {});
    paint();
  });
})();
