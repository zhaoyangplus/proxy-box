(function () {
  "use strict";
  PB.registerTab("diag", {
    enter: function (root) {
      root.innerHTML = '<div class="empty"><div class="empty-ic">' + PB.ic("pulse") + '</div><div class="empty-t">暂未迁移</div><p class="caption">连接诊断将在下一阶段接入。</p></div>';
    }
  });
})();
