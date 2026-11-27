(function () {
  "use strict";
  PB.registerTab("tools", {
    enter: function (root) {
      root.innerHTML = '<div class="empty"><div class="empty-ic">' + PB.ic("sparkle") + '</div><div class="empty-t">暂未迁移</div><p class="caption">AI 工具将在下一阶段接入。</p></div>';
    }
  });
})();
