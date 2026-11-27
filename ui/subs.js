(function () {
  "use strict";
  PB.registerTab("subs", {
    enter: function (root) {
      root.innerHTML = '<div class="empty"><div class="empty-ic">' + PB.ic("layers") + '</div><div class="empty-t">暂未迁移</div><p class="caption">订阅管理将在下一阶段接入。</p></div>';
    }
  });
})();
