(function (root, factory) {
  "use strict";
  var api = factory();
  if (typeof module !== "undefined" && module && module.exports) module.exports = api;
  if (root) root.PBOverview = api;
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  var GROUP = { daily: "PROXY", ai: "🤖 AI-IDE" };
  var MODE_TEXT = {
    rule: "国内网站直连；AI 服务域名自动走 AI 专属通道；其余流量走日常代理节点。",
    global: "除局域网外的全部流量都经由日常代理节点；AI 工具仍固定走 AI 专属通道。",
    direct: "所有流量直连，不经过任何代理节点。适合临时排查网络问题。"
  };
  var MODE_LABEL = { rule: "规则分流", global: "全局代理", direct: "全部直连" };
  var APP_IDS = ["cursor", "antigravity", "claude_code", "codex"];
  var DIAG_TEXT = "查看本地端口、出口地区与 AI 服务";

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

  function modeText(mode) {
    return MODE_TEXT[mode] || "当前模式无法识别。";
  }

  function modeLabel(mode) {
    return MODE_LABEL[mode] || "未知模式";
  }

  function findNode(list, name) {
    var nodes = list && list.nodes;
    if (!name || !nodes) return null;
    for (var i = 0; i < nodes.length; i++) {
      if (nodes[i] && nodes[i].name === name) return nodes[i];
    }
    return null;
  }

  function delayOf(node) {
    if (!node || node.untested || node.delayMs == null || node.delayMs === "") {
      return { kind: "untested", label: "未测延迟", cls: "d-none" };
    }
    var ms = Number(node.delayMs);
    if (ms > 0) {
      return {
        kind: "value",
        label: String(Math.round(ms)),
        cls: ms <= 120 ? "d-good" : ms <= 250 ? "d-mid" : "d-bad",
        ms: ms
      };
    }
    return { kind: "timeout", label: "超时", cls: "d-none" };
  }

  function speedOf(node) {
    if (!node) return "";
    var n = Number(node.speedMBps);
    var present = !!node.speedTested || (isFinite(n) && n > 0);
    if (!present) return "";
    if (!isFinite(n) || n < 0) n = 0;
    return n.toFixed(1);
  }

  function channelTile(group, list, running, port) {
    var title = group === "ai" ? "AI 专属通道" : "日常代理";
    var base = {
      group: group === "ai" ? "ai" : "daily",
      title: title,
      running: !!running,
      port: Number(port) > 0 ? Number(port) : 0,
      name: "",
      via: "",
      delay: { kind: "untested", label: "未测延迟", cls: "d-none" },
      speedLabel: ""
    };
    if (!running) {
      base.name = "内核未运行";
      return base;
    }
    if (list === undefined) {
      base.name = "正在读取节点";
      return base;
    }
    if (list === null) {
      base.name = "节点列表暂不可用";
      return base;
    }
    var selectedName = list.currentSelect || "";
    var resolvedName = list.resolvedNode || "";
    var selected = findNode(list, selectedName);
    var resolved = findNode(list, resolvedName);
    var via = "";
    if (selected && resolved && resolvedName && resolvedName !== selectedName) via = resolvedName;
    else if (selected && selected.resolvedTo && selected.resolvedTo !== selected.name) via = selected.resolvedTo;
    var metric = via && resolved ? resolved : (selected || resolved);
    if (!metric && via) metric = findNode(list, via);
    base.name = (selected && selected.name) || selectedName || resolvedName || "未选择节点";
    base.via = via;
    base.delay = delayOf(metric);
    base.speedLabel = speedOf(metric);
    return base;
  }

  function cssPercent(n) {
    var value = Number(n);
    if (!isFinite(value) || value < 0) value = 0;
    if (value > 100) value = 100;
    return value.toFixed(1) + "%";
  }

  function settleStart(input) {
    input = input || {};
    if (input.cancelled) return { phase: "stopped", error: "", warning: "" };
    if (input.error) return { phase: "error", error: String(input.error), warning: "" };
    var warning = input.warning ? String(input.warning) : "";
    if (warning) return { phase: "running", error: "", warning: warning };
    if (input.running === false) return { phase: "stopped", error: "", warning: "" };
    return { phase: "running", error: "", warning: "" };
  }

  function nextCorePhase(input) {
    input = input || {};
    if (input.running) return "running";
    var phase = input.phase || "stopped";
    if (input.holdDownload && !input.cancelRequested && phase === "download") return "download";
    if (phase === "error" || phase === "geo") return phase;
    return "stopped";
  }

  function applyCorePhase(state, event) {
    state = state || {};
    event = event || {};
    var next = event.phase || state.phase || "stopped";
    if (state.cancelRequested && next !== "running" && next !== "stopped") {
      return state.phase === "running" ? "running" : "stopped";
    }
    if (next === "download" && !(state.holdDownload && !state.cancelRequested)) {
      if (state.phase === "error" || state.phase === "running" || state.phase === "geo") return state.phase;
      return "stopped";
    }
    return next;
  }

  function heroState(input) {
    input = input || {};
    var running = !!input.running;
    var phase = input.phase || (running ? "running" : "stopped");
    var downloading = !running && phase === "download";
    var message = input.message || "";
    var error = "";
    if (!running && !downloading) error = input.error || (phase === "error" ? message : "");
    var downloaded = Number(input.downloaded) || 0;
    var total = Number(input.total) || 0;
    var progress = null;
    if (downloading) {
      progress = {
        percent: total > 0 ? (downloaded / total) * 100 : 0,
        label: total > 0 ? bytes(downloaded) + " / " + bytes(total) : (message || "正在准备内核")
      };
    }
    var primary = downloading ? "cancel" : running ? "stop" : "start";
    var title = downloading ? "正在启动" : running ? "运行中" : error ? "启动失败" : "已停止";
    var caption = "启动后日常代理与 AI 专属通道同时可用";
    if (running) caption = "双通道监听中";
    else if (downloading) caption = message || "正在下载内核";
    else if (error) caption = "可以重试启动，或手动导入 mihomo.exe / .zip";
    return {
      running: running,
      downloading: downloading,
      title: title,
      kicker: input.version ? "MIHOMO 内核 · " + input.version : "MIHOMO 内核",
      caption: caption,
      primary: primary,
      busy: !!input.busy,
      showImport: !running && !downloading,
      progress: progress,
      error: error
    };
  }

  function geoStatus(core, event) {
    core = core || {};
    event = event || {};
    if (core.geoError) return { kind: "error", text: String(core.geoError), retry: true };
    var downloading = !!core.geoDownloading || (event.phase === "geo" && /下载/.test(event.message || ""));
    if (downloading) {
      var extra = "";
      if (event.phase === "geo" && Number(event.total) > 0) {
        extra = " · " + bytes(event.downloaded) + " / " + bytes(event.total);
      }
      return { kind: "busy", text: "正在下载地理数据" + extra, retry: false };
    }
    if (core.geoReady) return { kind: "ready", text: "GeoSite / GeoIP 已就绪 · 国内站点直连", retry: false };
    var bypass = !!(core.settings && core.settings.bypassCN);
    if (bypass) return { kind: "wait", text: "已开启，等待地理数据", retry: false };
    return { kind: "off", text: "关闭后国内流量也会经过代理", retry: false };
  }

  function sysSub(running, active, port) {
    if (!running) return "内核停止时不可用";
    var n = Number(port);
    if (active && n > 0) return "已指向 127.0.0.1:" + n;
    if (active) return "已开启";
    return "未接管系统流量";
  }

  function listSignature(list) {
    if (list === undefined) return "?";
    if (list === null) return "-";
    var nodes = list.nodes || [];
    var parts = [list.currentSelect || "", list.resolvedNode || ""];
    for (var i = 0; i < nodes.length; i++) {
      var node = nodes[i] || {};
      parts.push(node.name || "", node.delayMs, node.untested ? 1 : 0, node.speedMBps, node.speedTested ? 1 : 0, node.resolvedTo || "");
    }
    return parts.join("\u0001");
  }

  function subsSignature(list) {
    if (list == null) return "-";
    var parts = [];
    for (var i = 0; i < list.length; i++) {
      var sub = list[i] || {};
      var usage = sub.usage || {};
      parts.push([sub.id, sub.name, sub.enabled ? 1 : 0, sub.nodeCount, sub.updatedAt, sub.lastError, usage.expire].join("\u0001"));
    }
    return parts.join("\u0002");
  }

  function subsSummary(list, now) {
    if (list == null) return { state: "error", text: "暂时无法读取订阅" };
    var enabled = 0;
    var nodes = 0;
    var soon = null;
    var clock = now == null ? Date.now() : Number(now);
    for (var i = 0; i < list.length; i++) {
      var sub = list[i];
      if (!sub || !sub.enabled) continue;
      enabled += 1;
      nodes += Number(sub.nodeCount) || 0;
      var exp = sub.usage && Number(sub.usage.expire);
      if (!exp) continue;
      var ms = exp > 1e12 ? exp : exp * 1000;
      var days = Math.ceil((ms - clock) / 86400000);
      if (days <= 7 && (soon == null || days < soon.days)) soon = { name: sub.name || "", days: days };
    }
    return { state: "ok", enabled: enabled, nodes: nodes, soon: soon };
  }

  function subsText(summary) {
    summary = summary || { state: "error", text: "暂时无法读取订阅" };
    if (summary.state !== "ok") return summary.text || "暂时无法读取订阅";
    var text = summary.enabled + " 个启用 · " + summary.nodes + " 个节点";
    if (summary.soon && summary.soon.name) {
      var days = summary.soon.days;
      var when = days < 0 ? "已过期" : days === 0 ? "今天到期" : days + " 天后到期";
      text += " · " + summary.soon.name + " " + when;
    }
    return text;
  }

  function appsSummary(config) {
    var apps = (config && config.managedApps) || {};
    var on = 0;
    for (var i = 0; i < APP_IDS.length; i++) if (apps[APP_IDS[i]]) on += 1;
    return { on: on, total: APP_IDS.length, text: on + " / " + APP_IDS.length + " 个应用走 AI 专属通道" };
  }

  function buttonLabel(primary, busy) {
    if (primary === "cancel") return "取消启动";
    if (primary === "stop") return busy ? "正在停止…" : "停止内核";
    return busy ? "正在启动…" : "启动内核";
  }

  function buttonIcon(primary) {
    if (primary === "cancel") return "x";
    if (primary === "stop") return "stop";
    return "play";
  }

  function quietName(name) {
    return name === "正在读取节点" || name === "节点列表暂不可用" || name === "内核未运行";
  }

  function delayHTML(tile) {
    if (!tile.running) return '<span class="caption">启动内核后生效</span>';
    if (quietName(tile.name)) return "";
    if (tile.delay.kind === "value") {
      return '<span class="metric ' + tile.delay.cls + '"><i class="dot"></i>' + esc(tile.delay.label) + "<small>ms</small></span>";
    }
    if (tile.delay.kind === "timeout") return '<span class="metric d-none"><i class="dot"></i>超时</span>';
    return '<span class="metric none">' + esc(tile.delay.label || "未测延迟") + "</span>";
  }

  function speedHTML(tile) {
    if (!tile.running || quietName(tile.name)) return "";
    if (tile.speedLabel) return '<span class="metric speed">' + ic("gauge") + esc(tile.speedLabel) + "<small>MB/s</small></span>";
    return '<span class="metric none">未测速</span>';
  }

  function tileHTML(tile, aiBusy) {
    var off = tile.running ? "" : " is-off";
    var port = tile.port > 0 ? "127.0.0.1:" + tile.port : "端口未就绪";
    var headAction = tile.group === "ai"
      ? '<button type="button" class="btn sm tinted" id="ovOpt"' + (!tile.running || aiBusy ? " disabled" : "") + ">" + ic("sparkle") + (aiBusy ? "优选中…" : "一键优选") + "</button>"
      : '<span class="ch-go">' + ic("chev-right") + "</span>";
    var name = tile.running
      ? esc(tile.name) + (tile.via ? '<span class="caption" style="font-weight:500"> → ' + esc(tile.via) + "</span>" : "")
      : '<span class="caption" style="font-size:14px">' + esc(tile.name) + "</span>";
    return '<article class="ch-tile' + off + '" data-ch="' + tile.group + '" data-go-ch="' + tile.group + '" tabindex="0" role="button" aria-label="' + esc(tile.title + "：" + tile.name) + '">' +
      '<div class="ch-head"><span class="ch-icon">' + ic(tile.group === "ai" ? "sparkle" : "globe") + "</span>" +
      '<div class="grow"><div class="ch-name">' + esc(tile.title) + '</div><div class="caption mono">' + esc(port) + "</div></div>" +
      headAction + "</div>" +
      '<div class="ch-node">' + name + "</div>" +
      '<div class="ch-metrics">' + delayHTML(tile) + speedHTML(tile) + "</div></article>";
  }

  function pageHTML(model) {
    model = model || {};
    var hero = model.hero || heroState({});
    var daily = model.daily || channelTile("daily", undefined, false, 0);
    var ai = model.ai || channelTile("ai", undefined, false, 0);
    var geo = model.geo || { text: "", retry: false };
    var primaryClass = hero.primary === "start" ? "btn lg prominent" : "btn lg tinted danger";
    var progress = hero.progress
      ? '<div class="hero-prog"><div class="usage-row"><span class="caption" data-field="progress">' + esc(hero.progress.label) + '</span></div><div class="bar"><i data-field="bar" style="width:' + cssPercent(hero.progress.percent) + '"></i></div></div>'
      : "";
    var error = hero.error
      ? '<div class="hero-err"><div class="notice warn">' + ic("warn") + "<span>" + esc(hero.error) + "</span></div></div>"
      : "";
    var importBtn = hero.showImport ? '<button type="button" class="btn sm" id="ovImport">导入 mihomo.exe / .zip</button>' : "";
    var retry = geo.retry ? '<button type="button" class="btn sm" id="ovGeoRetry">重试</button>' : "";
    var subs = model.subs || { state: "error", text: "暂时无法读取订阅" };
    var soon = "";
    if (subs.state === "ok" && subs.soon && subs.soon.name) {
      var days = subs.soon.days;
      var when = days < 0 ? "已过期" : days === 0 ? "今天到期" : days + " 天后到期";
      soon = ' · <span style="color:var(--orange-text)">' + esc(subs.soon.name) + " " + esc(when) + "</span>";
    }
    var subsLine = subs.state === "ok"
      ? esc(subs.enabled + " 个启用 · " + subs.nodes + " 个节点") + soon
      : esc(subs.text || "暂时无法读取订阅");
    var apps = model.apps || appsSummary(null);
    return '<div class="ov"><section class="hero ' + (hero.running ? "on" : "off") + '"><div class="hero-top">' +
      '<div class="core-orb">' + ic("bolt") + "</div>" +
      '<div class="hero-txt"><div class="kicker">' + esc(hero.kicker) + '</div><div class="hero-title" data-field="title">' + esc(hero.title) + '</div>' +
      '<div class="caption" data-field="caption">' + esc(hero.caption) + "</div></div>" +
      '<div class="hero-cta">' + importBtn +
      '<button type="button" class="' + primaryClass + '" id="ovCore"' + (hero.busy ? " disabled" : "") + ">" + ic(buttonIcon(hero.primary)) + esc(buttonLabel(hero.primary, hero.busy)) + "</button></div></div>" +
      progress + error +
      '<div class="channels">' + tileHTML(daily, false) + tileHTML(ai, !!model.aiBusy) + "</div></section>" +
      '<div class="ov-grid"><section class="card"><div class="card-h"><h3 class="card-t">路由模式</h3><span class="caption">热切换 · 无需重启</span></div>' +
      (model.modeHTML || "") + '<p class="caption mode-desc" id="ovModeDesc">' + esc(modeText(model.mode)) + "</p></section>" +
      '<section class="card tg"><div class="row has-ic"><span class="row-ic" style="--c:var(--blue)">' + ic("desktop") + "</span>" +
      '<div class="row-main"><div class="row-title">系统代理</div><div class="row-sub" id="ovSysSub">' + esc(model.sysSub || "") + "</div></div>" +
      (model.sysSwitch || "") + "</div>" +
      '<div class="row has-ic"><span class="row-ic" style="--c:var(--orange)">' + ic("route") + "</span>" +
      '<div class="row-main"><div class="row-title">绕过中国大陆</div><div class="row-sub" id="ovGeoSub"><span data-field="geo">' + esc(geo.text || "") + "</span>" + retry + "</div></div>" +
      (model.bypassSwitch || "") + "</div></section></div>" +
      '<section class="list"><div class="row link has-ic" data-go="subs" tabindex="0" role="button"><span class="row-ic" style="--c:#5b5bd6">' + ic("layers") + "</span>" +
      '<div class="row-main"><div class="row-title">订阅</div><div class="row-sub">' + subsLine + "</div></div>" + ic("chev-right") + "</div>" +
      '<div class="row link has-ic" data-go="tools" tabindex="0" role="button"><span class="row-ic" style="--c:#af52de">' + ic("sparkle") + "</span>" +
      '<div class="row-main"><div class="row-title">AI 工具托管</div><div class="row-sub">' + esc(apps.text) + "</div></div>" + ic("chev-right") + "</div>" +
      '<div class="row link has-ic" data-go="diag" tabindex="0" role="button"><span class="row-ic" style="--c:#8e8e93">' + ic("pulse") + "</span>" +
      '<div class="row-main"><div class="row-title">连接诊断</div><div class="row-sub">' + esc(DIAG_TEXT) + "</div></div>" + ic("chev-right") + "</div></section></div>";
  }

  return {
    GROUP: GROUP,
    MODE_TEXT: MODE_TEXT,
    DIAG_TEXT: DIAG_TEXT,
    modeText: modeText,
    modeLabel: modeLabel,
    channelTile: channelTile,
    settleStart: settleStart,
    nextCorePhase: nextCorePhase,
    applyCorePhase: applyCorePhase,
    heroState: heroState,
    geoStatus: geoStatus,
    sysSub: sysSub,
    subsSummary: subsSummary,
    listSignature: listSignature,
    subsSignature: subsSignature,
    subsText: subsText,
    appsSummary: appsSummary,
    cssPercent: cssPercent,
    pageHTML: pageHTML
  };
});
