(function (root, factory) {
  "use strict";
  var api = factory();
  if (typeof module !== "undefined" && module && module.exports) module.exports = api;
  if (root) root.PBUtil = api;
})(typeof window !== "undefined" ? window : globalThis, function () {
  "use strict";

  var THEME_KEY = "pb-v22-theme";
  var MIN = 60000;
  var DAY = 86400000;
  var ESC = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };

  function esc(value) {
    return String(value == null ? "" : value).replace(/[&<>"']/g, function (ch) {
      return ESC[ch];
    });
  }

  function safeClass(value) {
    return String(value == null ? "" : value).replace(/[^A-Za-z0-9 _-]/g, "");
  }

  function ic(name, cls) {
    var id = String(name == null ? "" : name).replace(/[^A-Za-z0-9-]/g, "");
    var extra = safeClass(cls);
    return '<svg class="i' + (extra ? " " + extra : "") + '" aria-hidden="true"><use href="#i-' + id + '"/></svg>';
  }

  function sw(on, attrs, disabled, cls) {
    var extra = safeClass(cls);
    var raw = attrs ? " " + String(attrs) : "";
    return '<button type="button" class="sw' + (extra ? " " + extra : "") + '" role="switch" aria-checked="' + (on ? "true" : "false") + '"' + raw + (disabled ? " disabled" : "") + "></button>";
  }

  function pad(n) {
    return String(n).padStart(2, "0");
  }

  function ago(ts, now) {
    var base = now == null ? Date.now() : Number(now);
    var minutes = Math.round((base - Number(ts)) / MIN);
    if (!isFinite(minutes)) return "";
    if (minutes < 1) return "刚刚";
    if (minutes < 60) return minutes + " 分钟前";
    var hours = Math.round(minutes / 60);
    if (hours < 24) return hours + " 小时前";
    return Math.round(hours / 24) + " 天前";
  }

  function bytes(n) {
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

  function ms(n) {
    var value = Number(n);
    if (!isFinite(value)) return "—";
    return Math.round(value) + " ms";
  }

  function mbps(n) {
    var value = Number(n);
    if (!isFinite(value)) return "—";
    return value.toFixed(2) + " MB/s";
  }

  function uptime(duration) {
    var seconds = Math.floor(Number(duration) / 1000);
    if (!isFinite(seconds) || seconds < 0) seconds = 0;
    var hours = Math.floor(seconds / 3600);
    var minutes = Math.floor((seconds % 3600) / 60);
    if (hours) return hours + " 小时 " + minutes + " 分";
    return minutes + " 分 " + (seconds % 60) + " 秒";
  }

  function daysLeft(ts, now) {
    var base = now == null ? Date.now() : Number(now);
    return Math.ceil((Number(ts) - base) / DAY);
  }

  function date(unixSecOrIso) {
    var parsed;
    if (typeof unixSecOrIso === "number") parsed = new Date(unixSecOrIso * 1000);
    else if (typeof unixSecOrIso === "string" && /^\d+$/.test(unixSecOrIso)) parsed = new Date(Number(unixSecOrIso) * 1000);
    else parsed = new Date(unixSecOrIso);
    if (!parsed || isNaN(parsed.getTime())) return "";
    return parsed.getFullYear() + "-" + pad(parsed.getMonth() + 1) + "-" + pad(parsed.getDate()) + " " + pad(parsed.getHours()) + ":" + pad(parsed.getMinutes());
  }

  function maskUrl(url) {
    var text = url == null ? "" : String(url);
    var match = text.match(/^(https?:\/\/[^/]+)(.*)$/);
    if (!match) return text;
    if (match[2].length > 14) return match[1] + match[2].slice(0, 12) + "••••";
    return text;
  }

  function normalizeTheme(value) {
    if (value === "light" || value === "dark" || value === "auto") return value;
    return "auto";
  }

  function resolveTheme(pref, prefersDark) {
    var choice = normalizeTheme(pref);
    if (choice === "light" || choice === "dark") return choice;
    return prefersDark ? "dark" : "light";
  }

  function themeFromStorage(storage, prefersDark) {
    var raw = null;
    try {
      raw = storage && storage.getItem(THEME_KEY);
    } catch (err) {
      raw = null;
    }
    var pref = normalizeTheme(raw);
    return { pref: pref, resolved: resolveTheme(pref, !!prefersDark) };
  }

  function segHTML(id, opts, active, cls) {
    var buttons = (opts || []).map(function (opt) {
      var on = String(opt.v) === String(active);
      var title = opt.title ? ' title="' + esc(opt.title) + '" aria-label="' + esc(opt.title) + '"' : "";
      var disabled = opt.disabled ? " disabled" : "";
      return '<button type="button" class="seg-btn' + (on ? " is-active" : "") + '" data-v="' + esc(opt.v) + '" role="radio" aria-checked="' + (on ? "true" : "false") + '"' + title + disabled + ">" + (opt.html || "") + "</button>";
    }).join("");
    return '<div class="seg' + (cls ? " " + safeClass(cls) : "") + '" id="' + esc(id) + '" role="radiogroup"><span class="seg-thumb" aria-hidden="true"></span>' + buttons + "</div>";
  }

  function segThumbPlacement(offsetWidth, offsetLeft, placed, animate) {
    var width = Number(offsetWidth);
    var left = Number(offsetLeft);
    var visible = isFinite(width) && width > 0;
    return {
      visible: visible,
      instant: !animate || !placed,
      width: (visible ? width : 0) + "px",
      x: (isFinite(left) ? left : 0) + "px"
    };
  }

  function navIndicatorPlacement(item, nav) {
    return {
      x: (Number(item.left) - Number(nav.left)) + "px",
      y: (Number(item.top) - Number(nav.top)) + "px",
      width: Number(item.width) + "px",
      height: Number(item.height) + "px"
    };
  }

  return {
    THEME_KEY: THEME_KEY,
    esc: esc,
    safeClass: safeClass,
    ic: ic,
    sw: sw,
    ago: ago,
    bytes: bytes,
    ms: ms,
    mbps: mbps,
    uptime: uptime,
    daysLeft: daysLeft,
    date: date,
    maskUrl: maskUrl,
    normalizeTheme: normalizeTheme,
    resolveTheme: resolveTheme,
    themeFromStorage: themeFromStorage,
    segHTML: segHTML,
    segThumbPlacement: segThumbPlacement,
    navIndicatorPlacement: navIndicatorPlacement
  };
});
