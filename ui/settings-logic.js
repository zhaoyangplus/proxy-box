(function (root, factory) {
  "use strict";
  var api = factory();
  if (typeof module !== "undefined" && module && module.exports) module.exports = api;
  if (root) root.PBSettings = api;
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  var KEYS = [
    "delayTestURL",
    "speedTestURL",
    "speedTestMaxMB",
    "customCursorExe",
    "customAntigravityExe",
    "customCodexExe",
    "autoOptimizeNode",
    "autoSyncOnChange",
    "syncUserEnv",
    "minimizeToTray"
  ];
  var URL_KEYS = ["delayTestURL", "speedTestURL"];
  var BOOL_KEYS = ["autoOptimizeNode", "autoSyncOnChange", "syncUserEnv", "minimizeToTray"];
  var DEFAULT_BOOL = { autoOptimizeNode: true, autoSyncOnChange: true, syncUserEnv: false, minimizeToTray: true };
  var URL_ERROR = "请输入 http 或 https 地址";
  var MB_ERROR = "测速大小只允许 5、10 或 25 MB";

  function text(value) {
    return String(value == null ? "" : value).trim();
  }

  function flag(value, fallback) {
    if (value == null) return fallback;
    return !!value;
  }

  function normalizeMB(value) {
    if (value == null || value === "") return 10;
    var n = Number(value);
    if (!isFinite(n)) return NaN;
    if (n === 0) return 10;
    return n;
  }

  function draftFrom(config) {
    config = config || {};
    var draft = {
      delayTestURL: text(config.delayTestURL),
      speedTestURL: text(config.speedTestURL),
      speedTestMaxMB: normalizeMB(config.speedTestMaxMB),
      customCursorExe: text(config.customCursorExe),
      customAntigravityExe: text(config.customAntigravityExe),
      customCodexExe: text(config.customCodexExe)
    };
    for (var i = 0; i < BOOL_KEYS.length; i++) {
      var key = BOOL_KEYS[i];
      draft[key] = flag(config[key], DEFAULT_BOOL[key]);
    }
    return draft;
  }

  function changedFields(saved, draft) {
    var before = draftFrom(saved);
    var after = draftFrom(draft);
    var out = {};
    for (var i = 0; i < KEYS.length; i++) {
      var key = KEYS[i];
      if (before[key] !== after[key]) out[key] = after[key];
    }
    return out;
  }

  function validURL(value) {
    var raw = text(value);
    if (!raw) return true;
    if (!/^https?:\/\/\S+$/i.test(raw)) return false;
    try {
      var url = new URL(raw);
      return url.protocol === "http:" || url.protocol === "https:";
    } catch (err) {
      return false;
    }
  }

  function validMB(value) {
    return value === 5 || value === 10 || value === 25;
  }

  function validate(draft) {
    var next = draftFrom(draft);
    var errors = {};
    for (var i = 0; i < URL_KEYS.length; i++) {
      var key = URL_KEYS[i];
      if (!validURL(next[key])) errors[key] = URL_ERROR;
    }
    if (!validMB(next.speedTestMaxMB)) errors.speedTestMaxMB = MB_ERROR;
    return { ok: Object.keys(errors).length === 0, errors: errors, draft: next };
  }

  function pickPort(top, nested, fallback) {
    var n = Number(top);
    if (n > 0) return n;
    n = Number(nested);
    if (n > 0) return n;
    return fallback;
  }

  function portsFrom(core) {
    core = core || {};
    var settings = core.settings || {};
    return {
      mixed: pickPort(core.mixedPort, settings.mixedPort, 27890),
      controller: pickPort(core.controllerPort, settings.controllerPort, 27891),
      ai: pickPort(core.aiDedicatedPort, settings.aiDedicatedPort, 27892),
      speed: pickPort(core.speedTestPort, settings.speedTestPort, 27893)
    };
  }

  return {
    KEYS: KEYS,
    URL_ERROR: URL_ERROR,
    MB_ERROR: MB_ERROR,
    draftFrom: draftFrom,
    changedFields: changedFields,
    validate: validate,
    portsFrom: portsFrom
  };
});
