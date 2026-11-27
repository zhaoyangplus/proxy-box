(function () {
  "use strict";

  var L = globalThis.PBSettings;
  if (!L || typeof PB === "undefined" || typeof PB.registerTab !== "function") return;

  var root = null;
  var baseline = L.draftFrom({});
  var draft = L.draftFrom({});
  var nums = {};
  var fields = {};
  var errors = {};
  var saveBar = null;
  var saveText = null;
  var saveBtn = null;
  var mbSeg = null;

  var PORTS = [
    ["mixed", "日常代理端口", "系统代理与浏览器使用", "globe", "var(--green)"],
    ["ai", "AI 专属端口", "AI 工具固定使用", "sparkle", "var(--indigo)"],
    ["controller", "控制 API 端口", "external-controller，仅本机访问", "cpu", "#8e8e93"],
    ["speed", "测速端口", "测网速时独占，避免干扰正常流量", "gauge", "var(--blue)"]
  ];
  var EXES = [
    ["customCursorExe", "Cursor 可执行文件路径", "留空则自动检测 Cursor.exe"],
    ["customAntigravityExe", "Antigravity 可执行文件路径", "留空则自动检测 Antigravity.exe"],
    ["customCodexExe", "Codex 可执行文件路径", "留空则自动检测 codex.exe / codex.cmd"]
  ];
  var PREFS = [
    ["autoOptimizeNode", "启动时自动优选 AI 专属节点", "按延迟选择合规地区中最快的节点"],
    ["autoSyncOnChange", "节点变更时自动同步 AI 工具", "已托管的应用无需手动点同步"],
    ["syncUserEnv", "写入 Windows 用户环境变量", "让新打开的终端默认走 AI 专属通道"],
    ["minimizeToTray", "关闭窗口时最小化到托盘", "内核保持在后台运行"]
  ];

  function pushChild(args, child) {
    if (Array.isArray(child)) {
      for (var i = 0; i < child.length; i++) pushChild(args, child[i]);
      return;
    }
    args.push(child);
  }

  function h(tag, props) {
    var args = [tag, props || {}];
    for (var i = 2; i < arguments.length; i++) pushChild(args, arguments[i]);
    return PB.el.apply(null, args);
  }

  function clear(node) {
    if (!node) return;
    if (typeof node.replaceChildren === "function") node.replaceChildren();
    else if (node.children) node.children.length = 0;
  }

  function add(parent) {
    for (var i = 1; i < arguments.length; i++) {
      var child = arguments[i];
      if (!child) continue;
      if (Array.isArray(child)) add.apply(null, [parent].concat(child));
      else parent.appendChild(child);
    }
    return parent;
  }

  function icon(name, color) {
    var node = h("span", { class: "row-ic", attrs: { style: "--c:" + color } });
    if (typeof PB.ic === "function") node.innerHTML = PB.ic(name);
    return node;
  }

  function configNow() {
    var snap = PB.state && PB.state.snapshot;
    return (snap && snap.appConfig) || {};
  }

  function resetDraft() {
    baseline = L.draftFrom(configNow());
    draft = L.draftFrom(configNow());
  }

  function isDirty() {
    return !L.sameDraft(baseline, draft);
  }

  function coreInfo() {
    var snap = (PB.state && PB.state.snapshot) || {};
    var client = snap.activeClient || {};
    var status = (PB.state && PB.state.core) || {};
    return {
      path: client.exePath || "",
      dir: client.configDir || "",
      version: status.version || ""
    };
  }

  function toast(msg, tone) {
    if (typeof PB.toast === "function") PB.toast(msg, tone ? { tone: tone } : undefined);
  }

  function bindField(input, key) {
    function apply(value) {
      draft[key] = value;
      refreshBar();
    }
    if (typeof PB.onInput === "function") PB.onInput(input, apply, 0);
    else if (input && typeof input.addEventListener === "function") {
      input.addEventListener("input", function () { apply(input.value); });
    }
  }

  function bindSeg(seg, handler) {
    if (seg && typeof PB.mountSeg === "function") PB.mountSeg(seg, handler);
  }

  function seg(id, options, active, cls) {
    var args = ["div", { class: "seg " + cls, attrs: { id: id, role: "radiogroup" } }, h("span", { class: "seg-thumb", attrs: { "aria-hidden": "true" } })];
    options.forEach(function (opt) {
      var on = String(opt.v) === String(active);
      args.push(h("button", {
        class: "seg-btn" + (on ? " is-active" : ""),
        text: opt.label,
        attrs: { "data-v": String(opt.v), role: "radio", "aria-checked": on ? "true" : "false" }
      }));
    });
    return h.apply(null, args);
  }

  function markField(input, message) {
    if (!input) return;
    var name = String(input.className || "").replace(/\binvalid\b/g, "").replace(/\s+/g, " ").trim();
    input.className = message ? (name + " invalid").trim() : name;
    if (input._err) input._err.textContent = message || "";
  }

  function paintErrors(result) {
    errors = (result && result.errors) || {};
    markField(fields.delayTestURL, errors.delayTestURL || "");
    markField(fields.speedTestURL, errors.speedTestURL || "");
    if (fields.maxErr) fields.maxErr.textContent = errors.speedTestMaxMB || "";
  }

  function refreshBar() {
    var result = L.validate(draft);
    var dirty = isDirty();
    if (saveBar) saveBar.className = "savebar" + (dirty ? " show" : "");
    if (saveText) saveText.textContent = result.ok ? "有未保存的更改" : "有设置需要修正";
    if (saveBtn) saveBtn.disabled = !dirty || !result.ok;
    paintErrors(result);
  }

  function paintPorts(status) {
    var ports = L.portsFrom(status || (PB.state && PB.state.core));
    Object.keys(nums).forEach(function (key) {
      if (nums[key]) nums[key].textContent = String(ports[key]);
    });
  }

  function fieldRow(title, input, hint) {
    var err = h("div", { class: "row-err", text: "" });
    input._err = err;
    return h("div", { class: "row stack" },
      h("div", { class: "row-title", text: title }),
      input,
      err,
      hint ? h("div", { class: "row-sub", text: hint }) : null
    );
  }

  function render() {
    if (!root) return;
    clear(root);
    nums = {};
    fields = {};
    var theme = PB.theme && typeof PB.theme.get === "function" ? PB.theme.get() : "auto";
    var themeSeg = seg("stTheme", [
      { v: "light", label: "浅色" },
      { v: "dark", label: "深色" },
      { v: "auto", label: "跟随系统" }
    ], theme, "sm");
    var info = coreInfo();
    var coreSub = info.path ? ((info.version ? info.version + " · " : "") + info.path) : "尚未导入 mihomo.exe";
    var ports = L.portsFrom(PB.state && PB.state.core);
    var portRows = PORTS.map(function (item) {
      var num = h("b", { class: "mono pb-num", text: String(ports[item[0]]) });
      nums[item[0]] = num;
      var rowClass = "row has-ic" + (item[0] === "speed" ? " pb-port" : "");
      return h("div", { class: rowClass },
        icon(item[3], item[4]),
        h("div", { class: "row-main" },
          h("div", { class: "row-title", text: item[1] }),
          h("div", { class: "row-sub", text: item[2] })
        ),
        num
      );
    });
    var delayInput = h("input", {
      class: "field mono",
      attrs: { "aria-label": "延迟测试地址", value: draft.delayTestURL, spellcheck: "false", placeholder: "http://www.gstatic.com/generate_204" }
    });
    var speedInput = h("input", {
      class: "field mono",
      attrs: {
        "aria-label": "下载测速 URL",
        value: draft.speedTestURL,
        spellcheck: "false",
        placeholder: "https://speed.cloudflare.com/__down?bytes=10485760"
      }
    });
    fields.delayTestURL = delayInput;
    fields.speedTestURL = speedInput;
    mbSeg = seg("stMaxMB", [
      { v: 5, label: "5 MB" },
      { v: 10, label: "10 MB" },
      { v: 25, label: "25 MB" }
    ], draft.speedTestMaxMB, "sm");
    fields.maxErr = h("div", { class: "row-err", text: "" });
    var exeRows = EXES.map(function (item) {
      var input = h("input", {
        class: "field mono",
        attrs: { "aria-label": item[1], value: draft[item[0]], spellcheck: "false", placeholder: item[2] }
      });
      fields[item[0]] = input;
      var button = h("button", { class: "btn sm", text: "浏览", on: { click: function () { browseExe(item[0], input); } } });
      return h("div", { class: "row stack" },
        h("div", { class: "row-title", text: item[1] }),
        h("div", { class: "path-input" }, input, button)
      );
    });
    var prefRows = PREFS.map(function (item) {
      var button = h("button", {
        class: "sw",
        attrs: { role: "switch", "aria-checked": draft[item[0]] ? "true" : "false", "aria-label": item[1] },
        on: { click: function (event) {
          draft[item[0]] = !draft[item[0]];
          event.currentTarget.setAttribute("aria-checked", draft[item[0]] ? "true" : "false");
          refreshBar();
        } }
      });
      return h("div", { class: "row" },
        h("div", { class: "row-main" },
          h("div", { class: "row-title", text: item[1] }),
          h("div", { class: "row-sub", text: item[2] })
        ),
        button
      );
    });
    var version = (PB.state && PB.state.version) || "…";
    var update = PB.state && PB.state.update;
    var aboutSub = update && update.hasUpdate && update.latestVersion ? "发现新版本 " + update.latestVersion : "检查 GitHub Releases";
    saveText = h("span", { text: "有未保存的更改" });
    saveBtn = h("button", { class: "btn sm prominent", text: "保存", on: { click: function () { save(); } } });
    saveBar = h("div", {
      class: "savebar",
      attrs: { id: "saveBar", role: "region", "aria-label": "未保存的更改" }
    },
      h("span", { class: "caption" }, h("span", { class: "dot" }), saveText),
      h("button", { class: "btn sm plain", text: "放弃", on: { click: discard } }),
      saveBtn
    );

    add(root,
      h("div", { class: "settings" },
        h("h3", { class: "group-t", text: "外观" }),
        h("div", { class: "list" }, h("div", { class: "row has-ic" },
          icon("sun", "#ff9f0a"),
          h("div", { class: "row-main" },
            h("div", { class: "row-title", text: "主题" }),
            h("div", { class: "row-sub", text: "浅色玻璃为默认，深色适合夜间使用" })
          ),
          themeSeg
        )),
        h("h3", { class: "group-t", text: "内核" }),
        h("div", { class: "list" }, h("div", { class: "row has-ic" },
          icon("bolt", "var(--green)"),
          h("div", { class: "row-main" },
            h("div", { class: "row-title", text: "mihomo 内核" }),
            h("div", { class: "row-sub", text: coreSub })
          ),
          h("div", { class: "row-actions" },
            h("button", { class: "btn sm", text: "手动导入 mihomo.exe / .zip", on: { click: importBinary } }),
            h("button", { class: "btn sm plain", text: "打开内核目录", on: { click: openCoreDir } }),
            h("button", { class: "btn sm plain", text: "打开配置目录", on: { click: openConfigDir } })
          )
        )),
        h("h3", { class: "group-t", text: "端口分配" }),
        h("div", { class: "list" }, portRows),
        h("p", { class: "group-f", text: "这些端口由程序分配，这里只读显示。端口如果变化，需要重启内核后才会生效。" }),
        h("h3", { class: "group-t", text: "测速" }),
        h("div", { class: "list" },
          fieldRow("延迟测试地址", delayInput, "返回 204 即视为可用。留空使用内置地址。"),
          fieldRow("网速测试下载地址", speedInput, "通过测速专用端口下载。留空使用 Cloudflare 测速地址。"),
          h("div", { class: "row has-ic" },
            icon("gauge", "var(--blue)"),
            h("div", { class: "row-main" },
              h("div", { class: "row-title", text: "单节点测速上限" }),
              h("div", { class: "row-sub", text: "达到上限即结束该节点下载" }),
              fields.maxErr
            ),
            mbSeg
          )
        ),
        h("h3", { class: "group-t", text: "AI 工具路径覆盖" }),
        h("div", { class: "list" }, exeRows),
        h("p", { class: "group-f", text: "仅在绿色版或自定义安装目录导致无法自动检出时填写。" }),
        h("h3", { class: "group-t", text: "通用" }),
        h("div", { class: "list" }, prefRows),
        h("h3", { class: "group-t", text: "关于" }),
        h("div", { class: "list" }, h("div", { class: "row has-ic" },
          icon("download", "var(--green)"),
          h("div", { class: "row-main" },
            h("div", { class: "row-title", text: "Proxy Box " + version }),
            h("div", { class: "row-sub", text: aboutSub })
          ),
          h("div", { class: "row-actions" },
            h("button", { class: "btn sm tinted", text: "检查更新", on: { click: openUpdate } }),
            h("button", { class: "btn sm plain", text: "打开发布页", on: { click: openReleases } })
          )
        )),
        saveBar
      )
    );
    bindField(delayInput, "delayTestURL");
    bindField(speedInput, "speedTestURL");
    EXES.forEach(function (item) { bindField(fields[item[0]], item[0]); });
    bindSeg(themeSeg, function (value) {
      if (PB.theme && typeof PB.theme.set === "function") PB.theme.set(value);
    });
    bindSeg(mbSeg, function (value) {
      draft.speedTestMaxMB = Number(value);
      refreshBar();
    });
    refreshBar();
  }

  function mergeSaved(body) {
    if (!PB.state.snapshot) PB.state.snapshot = {};
    if (!PB.state.snapshot.appConfig) PB.state.snapshot.appConfig = {};
    Object.keys(body).forEach(function (key) {
      PB.state.snapshot.appConfig[key] = body[key];
    });
  }

  function save(opt) {
    var result = L.validate(draft);
    paintErrors(result);
    if (!result.ok) {
      toast("请先修正标出的设置", "err");
      return Promise.resolve(false);
    }
    var body = L.changedFields(baseline, draft);
    if (!Object.keys(body).length) {
      refreshBar();
      return Promise.resolve(true);
    }
    if (saveBtn) saveBtn.disabled = true;
    return PB.api("POST", "/api/save-config", body).then(function () {
      mergeSaved(body);
      resetDraft();
      if (!opt || opt.render !== false) render();
      toast("设置已保存");
      return true;
    }).catch(function (err) {
      toast((err && err.message) || "保存失败", "err");
      refreshBar();
      return false;
    });
  }

  function discard() {
    resetDraft();
    render();
    toast("已放弃未保存的更改", "info");
  }

  function importBinary() {
    PB.api("POST", "/api/browse-core", {}).then(function (res) {
      var path = res && String(res.path || "").trim();
      if (!path) return;
      return PB.api("POST", "/api/core/import-binary", { path: path }).then(function (imported) {
        toast("已导入内核");
        if (imported && imported.path && PB.state.snapshot && PB.state.snapshot.activeClient) {
          PB.state.snapshot.activeClient.exePath = imported.path;
        }
        if (typeof PB.refreshSnapshot === "function") return PB.refreshSnapshot();
      }).then(function () {
        render();
      });
    }).catch(function (err) {
      toast((err && err.message) || "导入失败", "err");
    });
  }

  function openCoreDir() {
    var info = coreInfo();
    var target = info.dir || info.path;
    if (!target) {
      toast("还没有内核目录", "err");
      return;
    }
    PB.api("POST", "/api/open-path", { path: target, parent: !info.dir && !!info.path }).then(function () {
      toast("已打开内核目录");
    }).catch(function (err) {
      toast((err && err.message) || "打开失败", "err");
    });
  }

  function openConfigDir() {
    PB.api("POST", "/api/open-backups", {}).then(function () {
      toast("已在资源管理器中打开 ~/.proxy-box");
    }).catch(function (err) {
      toast((err && err.message) || "打开失败", "err");
    });
  }

  function browseExe(key, input) {
    PB.api("POST", "/api/browse-exe", {}).then(function (res) {
      var path = res && res.path ? String(res.path) : "";
      if (!path.trim()) return;
      draft[key] = path;
      if (input) input.value = path;
      refreshBar();
    }).catch(function (err) {
      toast((err && err.message) || "选择文件失败", "err");
    });
  }

  function openUpdate() {
    if (typeof PB.openUpdate === "function") PB.openUpdate();
    else toast("更新面板还没有准备好", "err");
  }

  function openReleases() {
    var known = PB.state.releasesUrl || "";
    var ready = known ? Promise.resolve(known) : PB.api("GET", "/api/version").then(function (data) {
      if (data && data.releasesUrl) PB.state.releasesUrl = data.releasesUrl;
      if (data && data.version) PB.state.version = data.version;
      return PB.state.releasesUrl || "";
    });
    ready.then(function (url) {
      if (!url) throw new Error("没有发布页地址");
      return PB.api("POST", "/api/open-url", { url: url });
    }).then(function () {
      toast("已打开发布页");
    }).catch(function (err) {
      toast((err && err.message) || "打开发布页失败", "err");
    });
  }

  function leave(targetId, opt) {
    var decision = L.leaveDecision({
      dirty: isDirty(),
      force: !!(opt && opt.force),
      target: targetId
    });
    if (decision.allow) return;
    var id = decision.target;
    if (!id || id === "settings" || typeof PB.openSheet !== "function") return false;
    PB.openSheet({
      title: "有未保存的更改",
      body: "离开前要保存设置吗？未保存的更改会丢失。",
      icon: '<span class="sheet-ic">' + (typeof PB.ic === "function" ? PB.ic("info") : "") + "</span>",
      actions: [
        { label: "继续编辑", kind: "plain" },
        { label: "放弃更改", kind: "plain danger-text", fn: function () {
          resetDraft();
          PB.goTab(id, { force: true });
        } },
        { label: "保存并离开", kind: "prominent", fn: function () {
          return save({ render: false }).then(function (ok) {
            if (!ok) return false;
            PB.goTab(id, { force: true });
          });
        } }
      ]
    });
    return false;
  }

  PB.registerTab("settings", {
    enter: function (node) {
      root = node;
      if (typeof PB.setChannel === "function") PB.setChannel("daily");
      if (typeof PB.topbar === "function") {
        PB.topbar({
          title: "设置",
          sub: "外观、端口分配、测速参数与通用偏好",
          actions: "",
          acc: ""
        });
      }
      if (!isDirty()) resetDraft();
      render();
      if (typeof PB.refreshSnapshot === "function") {
        var before = JSON.stringify(configNow());
        PB.refreshSnapshot().then(function () {
          if (isDirty() || !root || (PB.current && PB.current() !== "settings")) return;
          if (JSON.stringify(configNow()) === before) return;
          resetDraft();
          render();
        }).catch(function () {});
      }
    },
    leave: leave,
    onCore: function (status) {
      paintPorts(status);
    },
    onSSE: function (msg) {
      if (!msg || msg.type !== "sync" || isDirty()) return;
      resetDraft();
      if (root && (!PB.current || PB.current() === "settings")) render();
    }
  });
})();
