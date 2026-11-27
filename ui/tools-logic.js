(function (root, factory) {
  "use strict";
  var api = factory();
  if (typeof module !== "undefined" && module && module.exports) module.exports = api;
  if (root) root.PBTools = api;
})(typeof window !== "undefined" ? window : globalThis, function () {
  "use strict";

  var ORDER = ["cursor", "antigravity", "claude_code", "codex"];
  var BRANDS = {
    cursor: { name: "Cursor", desc: "AI 代码编辑器", glyph: "C", bg: "linear-gradient(145deg,#3a3a3c,#0b0b0d)", kind: "ide" },
    antigravity: { name: "Antigravity", desc: "Google AI 开发环境", glyph: "A", bg: "linear-gradient(145deg,#6b95ff,#2f5bea)", kind: "ide" },
    claude_code: { name: "Claude Code", desc: "Anthropic 终端编程助手", glyph: "\u2733", bg: "linear-gradient(145deg,#ea946a,#c4623a)", kind: "cli" },
    codex: { name: "Codex", desc: "OpenAI 编程智能体 CLI", glyph: "\u203a_", bg: "linear-gradient(145deg,#1fbf8f,#0d8a66)", kind: "cli" }
  };
  var DISABLED = "未开启托管（保持应用默认设置）";

  function util() {
    var u = globalThis.PBUtil;
    if (!u) throw new Error("ui/util.js must load before tools-logic.js");
    return u;
  }

  function portNumber(port) {
    if (typeof port === "number" && Number.isInteger(port) && port >= 1 && port <= 65535) return port;
    if (typeof port === "string" && /^\d{1,5}$/.test(port)) {
      var n = Number(port);
      if (n >= 1 && n <= 65535) return n;
    }
    return 0;
  }

  function envSnippet(port) {
    var n = portNumber(port);
    if (!n) return "";
    var proxy = "http://127.0.0.1:" + n;
    return '$env:HTTP_PROXY="' + proxy + '"; $env:HTTPS_PROXY="' + proxy + '"; $env:NO_PROXY="localhost,127.0.0.1"';
  }

  function orderedTargets(targets) {
    var byId = Object.create(null);
    (targets || []).forEach(function (target) {
      if (target && ORDER.indexOf(target.id) >= 0) byId[target.id] = target;
    });
    return ORDER.map(function (id) {
      var found = byId[id] || {};
      var brand = BRANDS[id];
      return {
        id: id,
        name: found.name || brand.name,
        desc: brand.desc,
        glyph: brand.glyph,
        bg: brand.bg,
        kind: brand.kind,
        enabledByUser: !!found.enabledByUser,
        running: !!found.running,
        installed: !!found.installed,
        http2Disabled: !!found.http2Disabled,
        terminalEnvConfigured: !!found.terminalEnvConfigured,
        settingsPath: found.settingsPath == null ? "" : String(found.settingsPath),
        statusText: found.statusText == null ? "" : String(found.statusText)
      };
    });
  }

  function applyEnabled(targets, id, enabled) {
    return orderedTargets(targets).map(function (target) {
      if (target.id !== id) return target;
      return Object.assign({}, target, { enabledByUser: !!enabled });
    });
  }

  function enabledCount(targets) {
    var n = 0;
    orderedTargets(targets).forEach(function (target) {
      if (target.enabledByUser) n += 1;
    });
    return n;
  }

  function chips(target, port) {
    if (!target || !target.enabledByUser) return [];
    var n = portNumber(port);
    var list = [{ tone: "indigo", text: n ? "AI 通道 :" + n : "AI 通道" }];
    if (target.http2Disabled) list.push({ tone: "green", text: "HTTP/2 已禁用" });
    if (target.terminalEnvConfigured) list.push({ tone: "green", text: "环境变量已写入" });
    var status = String(target.statusText || "").trim();
    if (status) list.push({ tone: "", text: status });
    return list;
  }

  function cardHTML(target, port, busyId) {
    var u = util();
    var on = !!target.enabledByUser;
    var path = target.settingsPath || "";
    var busy = busyId === target.id;
    var stateHTML = on
      ? chips(target, port).map(function (chip) {
        var cls = chip.tone ? " " + u.safeClass(chip.tone) : "";
        return '<span class="pill' + cls + '">' + u.esc(chip.text) + "</span>";
      }).join("")
      : '<span class="caption">' + DISABLED + "</span>";
    var envBtn = target.kind === "cli"
      ? '<button type="button" class="btn sm plain" data-act="env">' + u.ic("terminal") + "复制终端环境变量</button>"
      : "";
    return '<article class="card tool' + (on ? "" : " off") + '" data-tid="' + u.esc(target.id) + '">' +
      '<div class="tool-top">' +
        '<span class="app-ic" style="background:' + target.bg + '">' + u.esc(target.glyph) + "</span>" +
        '<div class="row-main"><div class="tool-name"><span class="card-t">' + u.esc(target.name) + "</span></div>" +
        '<div class="caption"><span class="run-dot' + (target.running ? "" : " off") + '"></span>' +
          (target.running ? "运行中" : "未运行") + " · " + u.esc(target.desc) + "</div></div>" +
        u.sw(on, 'data-act="toggle" aria-label="' + u.esc("托管 " + target.name) + '"', busy) +
      "</div>" +
      '<div class="tool-state">' + stateHTML + "</div>" +
      '<div class="path"><span class="mono ellipsis" title="' + u.esc(path) + '">' + (path ? u.esc(path) : "未找到配置路径") + "</span>" +
        '<button type="button" class="ibtn" data-act="copy" title="复制路径" aria-label="复制路径">' + u.ic("copy") + "</button>" +
        '<button type="button" class="ibtn" data-act="dir" title="打开所在目录" aria-label="打开所在目录">' + u.ic("folder") + "</button></div>" +
      '<div class="tool-acts">' +
        '<button type="button" class="btn sm' + (on ? " prominent" : "") + '" data-act="launch">' + u.ic("play") + (target.running ? "重启" : "代理启动") + "</button>" +
        '<button type="button" class="btn sm plain" data-act="shortcut">' + u.ic("desktop") + "创建桌面快捷方式</button>" +
        envBtn +
      "</div></article>";
  }

  function pageHTML(targets, state) {
    state = state || {};
    var view = state.pending ? applyEnabled(targets, state.pending.id, state.pending.enabled) : orderedTargets(targets);
    return '<div class="tools">' + view.map(function (target) {
      return cardHTML(target, state.port, state.busyId);
    }).join("") + "</div>";
  }

  function topbarHTML() {
    var u = util();
    return '<button type="button" class="btn sm cl" id="tlRestore" title="还原全部">' + u.ic("undo") + '<span class="lbl">还原全部</span></button>' +
      '<button type="button" class="btn sm prominent" id="tlSync">' + u.ic("refresh") + "同步已开启</button>";
  }

  return {
    ORDER: ORDER,
    DISABLED: DISABLED,
    portNumber: portNumber,
    envSnippet: envSnippet,
    orderedTargets: orderedTargets,
    applyEnabled: applyEnabled,
    enabledCount: enabledCount,
    chips: chips,
    pageHTML: pageHTML,
    topbarHTML: topbarHTML
  };
});
