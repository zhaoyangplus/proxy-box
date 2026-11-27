(function () {
  "use strict";
  PB.registerTab("nodes", {
    enter: function (root) {
      root.innerHTML = '<div class="empty"><div class="empty-ic">' + PB.ic("globe") + '</div><div class="empty-t">暂未迁移</div><p class="caption">代理节点将在下一阶段接入。</p></div>';
    }
  });
})();
