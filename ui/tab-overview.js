(function () {
  "use strict";
  PB.registerTab("overview", {
    enter: function (root) {
      root.innerHTML = '<div class="empty"><div class="empty-ic">' + PB.ic("home") + '</div><div class="empty-t">暂未迁移</div><p class="caption">总览将在下一阶段接入。</p></div>';
    }
  });
})();
