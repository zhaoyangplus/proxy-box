(function (root, factory) {
  "use strict";
  var api = factory();
  if (typeof module !== "undefined" && module && module.exports) module.exports = api;
  if (root) root.PBSubs = api;
})(typeof window !== "undefined" ? window : globalThis, function () {
  "use strict";

  var PALETTE = ["#2c7a52", "#0090ff", "#6b5cff", "#d4537e", "#e07a2f", "#0d8a66", "#3a3a3c", "#7a5af8", "#c4623a", "#1f6feb"];
  var PRESETS = [
    { v: 0, label: "仅手动" },
    { v: 30, label: "30 分钟" },
    { v: 60, label: "1 小时" },
    { v: 360, label: "6 小时" },
    { v: 720, label: "12 小时" },
    { v: 1440, label: "24 小时" }
  ];
  var PRESET_TEXT = {
    0: "仅手动更新",
    30: "每 30 分钟自动更新",
    60: "每 1 小时自动更新",
    360: "每 6 小时自动更新",
    720: "每 12 小时自动更新",
    1440: "每 24 小时自动更新"
  };
  var DAY = 86400000;
  var MIN = 60000;

  function util() {
    var u = globalThis.PBUtil;
    if (!u) throw new Error("ui/util.js must load before subs-logic.js");
    return u;
  }

  function badgeColor(name) {
    var text = String(name == null ? "" : name);
    var hash = 2166136261;
    for (var i = 0; i < text.length; i++) {
      hash ^= text.charCodeAt(i);
      hash = Math.imul(hash, 16777619);
    }
    return PALETTE[(hash >>> 0) % PALETTE.length];
  }

  function safeColor(name) {
    var color = badgeColor(name);
    return /^#[0-9a-f]{6}$/.test(color) ? color : PALETTE[0];
  }

  function initials(name) {
    var text = String(name == null ? "" : name).trim();
    if (!text) return "?";
    return Array.from(text).slice(0, 2).join("").toUpperCase();
  }

  function validSubURL(raw) {
    var text = String(raw == null ? "" : raw).trim();
    if (!text) return false;
    try {
      var parsed = new URL(text);
      return (parsed.protocol === "http:" || parsed.protocol === "https:") && !!parsed.host;
    } catch (err) {
      return false;
    }
  }

  function defaultName(url) {
    try {
      var host = new URL(String(url == null ? "" : url).trim()).host;
      return host || "订阅";
    } catch (err) {
      return "订阅";
    }
  }

  function intervalMinutes(value) {
    var n = Number(value);
    if (!isFinite(n)) return 0;
    n = Math.round(n);
    return n < 0 ? 0 : n;
  }

  function intervalOptions(current) {
    var minutes = intervalMinutes(current);
    var known = false;
    var options = PRESETS.map(function (preset) {
      if (preset.v === minutes) known = true;
      return { v: preset.v, html: preset.label };
    });
    if (!known) options.push({ v: minutes, html: "自定义 " + minutes + " 分钟", custom: true });
    return { active: minutes, options: options, custom: !known };
  }

  function intervalText(minutes) {
    var n = intervalMinutes(minutes);
    return PRESET_TEXT[n] || ("每 " + n + " 分钟自动更新");
  }

  function timeMs(value) {
    if (value == null || value === "") return null;
    var parsed;
    if (typeof value === "number") {
      if (!isFinite(value) || value <= 0) return null;
      parsed = value < 1e12 ? value * 1000 : value;
    } else {
      parsed = Date.parse(value);
    }
    if (!isFinite(parsed) || parsed < Date.UTC(2000, 0, 1)) return null;
    return parsed;
  }

  function countOf(sub) {
    var n = Number(sub && sub.nodeCount);
    if (!isFinite(n) || n <= 0) return 0;
    return Math.round(n);
  }

  function usageState(sub) {
    var usage = sub && sub.usage;
    var upload = usage ? Number(usage.upload) : 0;
    var download = usage ? Number(usage.download) : 0;
    var total = usage ? Number(usage.total) : 0;
    if (!isFinite(upload) || upload < 0) upload = 0;
    if (!isFinite(download) || download < 0) download = 0;
    if (!isFinite(total) || total < 0) total = 0;
    var used = upload + download;
    var hasTotal = total > 0;
    var ratio = hasTotal ? used / total : 0;
    return {
      used: used,
      total: total,
      hasTotal: hasTotal,
      ratio: ratio,
      pct: hasTotal ? Math.min(100, ratio * 100) : 0,
      high: hasTotal && ratio >= 0.9
    };
  }

  function expiryInfo(expire, now) {
    var sec = Number(expire);
    if (!isFinite(sec) || sec <= 0) return { kind: "none", days: null, seconds: 0 };
    var ms = sec * 1000;
    var base = Number(now);
    if (!isFinite(base)) base = Date.now();
    if (ms <= base) {
      return { kind: "past", days: Math.ceil((base - ms) / DAY), seconds: sec };
    }
    var days = Math.ceil((ms - base) / DAY);
    if (days <= 7) return { kind: "soon", days: days, seconds: sec };
    return { kind: "ok", days: days, seconds: sec };
  }

  function warningBadges(sub, now) {
    var badges = [];
    var exp = expiryInfo(sub && sub.usage && sub.usage.expire, now);
    if (exp.kind === "past") badges.push({ kind: "past", tone: "red", text: "已过期" });
    else if (exp.kind === "soon") badges.push({ kind: "soon", tone: "orange", text: exp.days <= 0 ? "今天到期" : exp.days + " 天后到期" });
    var usage = usageState(sub);
    if (usage.high) badges.push({ kind: "usage", tone: "red", text: "用量 " + Math.round(usage.pct) + "%" });
    return badges;
  }

  function relativeFuture(at, now) {
    var delta = at - now;
    if (delta <= MIN) return "即将";
    var minutes = Math.round(delta / MIN);
    if (minutes < 60) return Math.max(1, minutes) + " 分钟后";
    var hours = Math.round(minutes / 60);
    if (hours < 24) return hours + " 小时后";
    return Math.max(1, Math.round(hours / 24)) + " 天后";
  }

  function nextAutoUpdate(subs, now) {
    var base = Number(now);
    if (!isFinite(base)) base = Date.now();
    var soonest = null;
    (subs || []).forEach(function (sub) {
      if (!sub || !sub.enabled) return;
      var every = intervalMinutes(sub.updateIntervalMin);
      if (every <= 0) return;
      var updated = timeMs(sub.updatedAt);
      if (updated == null) return;
      var at = updated + every * MIN;
      if (soonest == null || at < soonest) soonest = at;
    });
    if (soonest == null) return { at: null, text: "\u2014" };
    return { at: soonest, text: relativeFuture(soonest, base) };
  }

  function summarize(subs, now) {
    var list = subs || [];
    var enabled = 0;
    var nodes = 0;
    list.forEach(function (sub) {
      if (!sub || !sub.enabled) return;
      enabled += 1;
      nodes += countOf(sub);
    });
    return {
      enabled: enabled,
      total: list.length,
      nodes: nodes,
      next: nextAutoUpdate(list, now).text
    };
  }

  function errorCount(subs) {
    var n = 0;
    (subs || []).forEach(function (sub) {
      if (sub && String(sub.lastError == null ? "" : sub.lastError).trim()) n += 1;
    });
    return n;
  }

  function sameText(a, b) {
    return (a == null ? "" : String(a)) === (b == null ? "" : String(b));
  }

  function diffSub(prev, draft) {
    if (!prev || !draft) return null;
    var body = {};
    if (!sameText(draft.name, prev.name)) body.name = draft.name == null ? "" : String(draft.name);
    if (!sameText(draft.url, prev.url)) body.url = draft.url == null ? "" : String(draft.url);
    if (!sameText(draft.userAgent, prev.userAgent)) body.userAgent = draft.userAgent == null ? "" : String(draft.userAgent);
    if (!sameText(draft.filter, prev.filter)) body.filter = draft.filter == null ? "" : String(draft.filter);
    if (intervalMinutes(draft.updateIntervalMin) !== intervalMinutes(prev.updateIntervalMin)) {
      body.updateIntervalMin = intervalMinutes(draft.updateIntervalMin);
    }
    if (!Object.keys(body).length) return null;
    body.id = prev.id;
    return body;
  }

  function enabledPatch(id, enabled) {
    return { id: id, enabled: !!enabled };
  }

  function updateSummary(results) {
    var list = results || [];
    var fail = 0;
    list.forEach(function (item) {
      if (!item || !item.ok) fail += 1;
    });
    var ok = list.length - fail;
    if (!list.length) return { text: "没有需要更新的订阅", tone: "info", ok: 0, fail: 0 };
    if (!fail) return { text: "已更新全部 " + ok + " 个订阅", tone: "ok", ok: ok, fail: 0 };
    return { text: ok + " 个订阅已更新，" + fail + " 个失败", tone: "warn", ok: ok, fail: fail };
  }

  function markedBusy(busy, id) {
    return !!(busy && Object.prototype.hasOwnProperty.call(busy, id) && busy[id]);
  }

  function barWidth(pct) {
    var n = Number(pct);
    if (!isFinite(n) || n < 0) n = 0;
    if (n > 100) n = 100;
    return n.toFixed(1);
  }

  function cardHTML(sub, now, busy) {
    var u = util();
    var id = sub.id == null ? "" : String(sub.id);
    var name = sub.name == null ? "" : String(sub.name);
    var url = sub.url == null ? "" : String(sub.url);
    var usage = usageState(sub);
    var exp = expiryInfo(sub.usage && sub.usage.expire, now);
    var when = timeMs(sub.updatedAt);
    var ago = when == null ? "尚未更新" : (u.ago(when, now) || "尚未更新");
    var badges = warningBadges(sub, now).map(function (badge) {
      return '<span class="pill ' + u.safeClass(badge.tone) + '">' + u.esc(badge.text) + "</span>";
    }).join("");
    if (!sub.enabled) badges += '<span class="pill">已停用</span>';
    var dateText = exp.kind === "none" ? "无到期时间" : ("到期 " + (u.date(exp.seconds) || "\u2014"));
    var traffic = usage.hasTotal
      ? '<div class="usage-row"><span><b>' + u.esc(u.bytes(usage.used)) + "</b> / " + u.esc(u.bytes(usage.total)) + '</span><span class="caption">' + u.esc(dateText) + "</span></div>" +
        '<div class="bar' + (usage.pct >= 90 ? " bad" : usage.pct >= 70 ? " mid" : "") + '"><i style="width:' + barWidth(usage.pct) + '%"></i></div>'
      : '<div class="usage-row"><span class="caption">服务商未提供流量信息</span><span class="caption">' + u.esc(dateText) + "</span></div>";
    var err = String(sub.lastError == null ? "" : sub.lastError).trim();
    var errHTML = err ? '<div class="notice warn sub-err">' + u.ic("warn") + "<span>" + u.esc(err) + "</span></div>" : "";
    var updating = markedBusy(busy, id);
    return '<article class="card sub-card' + (sub.enabled ? "" : " is-off") + '" data-sid="' + u.esc(id) + '">' +
      '<div class="sub-top">' +
        '<span class="sub-av" style="--c:' + safeColor(name) + '">' + u.esc(initials(name)) + "</span>" +
        '<div class="sub-info"><div class="sub-name"><span class="card-t">' + u.esc(name) + "</span>" + badges + "</div>" +
        '<div class="sub-url"><span class="mono ellipsis">' + u.esc(u.maskUrl(url)) + '</span>' +
        '<button type="button" class="ibtn" data-act="copy" title="复制订阅链接" aria-label="复制订阅链接">' + u.ic("copy") + "</button></div></div>" +
        u.sw(!!sub.enabled, 'data-act="toggle" aria-label="' + u.esc("启用 " + name) + '"', updating) +
      "</div>" +
      '<div class="sub-meta">' +
        '<span class="meta">' + u.ic("globe") + countOf(sub) + " 个节点</span>" +
        '<span class="meta">' + u.ic("clock") + u.esc(intervalText(sub.updateIntervalMin)) + "</span>" +
        '<span class="meta">' + u.ic("refresh") + (updating ? "正在更新…" : u.esc(ago) + "更新") + "</span>" +
      "</div>" +
      traffic + errHTML +
      '<div class="sub-acts">' +
        '<button type="button" class="btn sm" data-act="update"' + (updating ? " disabled" : "") + ">" +
          (updating ? '<span class="spin xs"></span>更新中' : u.ic("refresh") + "立即更新") + "</button>" +
        '<button type="button" class="btn sm plain" data-act="edit">' + u.ic("pencil") + "编辑</button>" +
        '<button type="button" class="btn sm plain danger-text push" data-act="del">' + u.ic("trash") + "删除</button>" +
      "</div></article>";
  }

  function pageHTML(subs, state) {
    var u = util();
    state = state || {};
    var now = state.now == null ? Date.now() : Number(state.now);
    var list = subs || [];
    var stats = summarize(list, now);
    var cards = "";
    if (!state.loaded) {
      cards = '<div class="empty"><div class="empty-ic">' + u.ic("layers") + '</div><div class="empty-t">正在读取订阅…</div></div>';
    } else if (!list.length) {
      cards = '<div class="empty"><div class="empty-ic">' + u.ic("layers") + '</div><div class="empty-t">还没有订阅</div>' +
        '<div class="caption">粘贴机场提供的 Clash / Mihomo 订阅链接即可导入节点。</div>' +
        '<button type="button" class="btn sm prominent" data-act="add">' + u.ic("plus") + "添加订阅</button></div>";
    } else {
      cards = list.map(function (sub) { return cardHTML(sub || {}, now, state.busy); }).join("");
    }
    return '<div class="subs"><div class="sub-sum">' +
      '<div class="card stat"><div class="kicker">启用订阅</div><div class="stat-v">' + stats.enabled + "<small>/ " + stats.total + "</small></div></div>" +
      '<div class="card stat"><div class="kicker">可用节点</div><div class="stat-v">' + stats.nodes + "<small>个</small></div></div>" +
      '<div class="card stat"><div class="kicker">下次自动更新</div><div class="stat-v" style="font-size:18px;margin-top:8px">' + u.esc(stats.next) + "</div></div>" +
      "</div>" + cards + "</div>";
  }

  function formHTML(sub) {
    var u = util();
    var current = sub ? intervalMinutes(sub.updateIntervalMin) : 360;
    var choice = intervalOptions(current);
    var filter = sub && sub.filter != null ? String(sub.filter) : "";
    var ua = sub && sub.userAgent != null ? String(sub.userAgent) : "";
    var open = filter || ua ? " open" : "";
    return '<div class="form">' +
      '<label class="fl">名称<input class="field" id="fName" value="' + u.esc(sub && sub.name) + '" placeholder="留空则使用域名"' + (sub ? " autofocus" : "") + '><em class="err" id="eName"></em></label>' +
      '<label class="fl">订阅链接<input class="field mono" id="fUrl" value="' + u.esc(sub && sub.url) + '" placeholder="https://"' + (sub ? "" : " autofocus") + '><em class="err" id="eUrl"></em></label>' +
      '<div class="fl">自动更新' + u.segHTML("fInt", choice.options, choice.active, "full sm sub-int") + "</div>" +
      '<details class="adv"' + open + "><summary>" + u.ic("chev-right") + "高级选项</summary><div class=\"form\">" +
      '<label class="fl">节点过滤（正则）<input class="field mono" id="fFilter" value="' + u.esc(filter) + '" placeholder="例如：^(?!.*(到期|剩余)).*$"></label>' +
      '<label class="fl">User-Agent<input class="field mono" id="fUa" value="' + u.esc(ua) + '" placeholder="clash.meta"></label>' +
      "</div></details></div>";
  }

  function topbarHTML() {
    var u = util();
    return '<button type="button" class="btn sm cl" id="subAll" title="全部更新">' + u.ic("refresh") + '<span class="lbl">全部更新</span></button>' +
      '<button type="button" class="btn sm prominent" id="subAdd">' + u.ic("plus") + "添加订阅</button>";
  }

  return {
    PALETTE: PALETTE,
    badgeColor: badgeColor,
    initials: initials,
    validSubURL: validSubURL,
    defaultName: defaultName,
    intervalMinutes: intervalMinutes,
    intervalOptions: intervalOptions,
    intervalText: intervalText,
    timeMs: timeMs,
    usageState: usageState,
    expiryInfo: expiryInfo,
    warningBadges: warningBadges,
    nextAutoUpdate: nextAutoUpdate,
    summarize: summarize,
    errorCount: errorCount,
    diffSub: diffSub,
    enabledPatch: enabledPatch,
    updateSummary: updateSummary,
    pageHTML: pageHTML,
    formHTML: formHTML,
    topbarHTML: topbarHTML
  };
});
