(function () {
  "use strict";

  var speedLabel = null;

  function paintSpeedPort() {
    if (!speedLabel || typeof PB.ports !== "function") return;
    speedLabel.textContent = String(PB.ports().speed);
  }

  function mount(root) {
    var ports = PB.ports();
    speedLabel = PB.el("b", { class: "pb-num", text: String(ports.speed) });
    var port = PB.el("div", { class: "pb-port" },
      PB.el("div", { text: "测速端口" }),
      speedLabel
    );
    var url = PB.el("input", {
      attrs: {
        "aria-label": "下载测速 URL",
        value: "https://speed.cloudflare.com/__down?bytes=10485760"
      }
    });
    root.appendChild(port);
    root.appendChild(url);
  }

  if (typeof PB.addTab === "function") {
    PB.addTab("settings", {
      mount: mount,
      onCoreChange: paintSpeedPort
    });
  }

  if (typeof PB.registerTab === "function") {
    PB.registerTab("settings", {
      enter: function (root) {
        root.innerHTML = '<div class="empty"><div class="empty-ic">' + PB.ic("gear") + '</div><div class="empty-t">暂未迁移</div><p class="caption">设置将在下一阶段接入。</p></div>';
      }
    });
  }
})();
