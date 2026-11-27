(function () {
  "use strict";
  var L = window.PBSubs;
  if (!L) throw new Error("ui/subs-logic.js must load before ui/subs.js");

  var list = [];
  var shown = [];
  var loaded = false;
  var busy = Object.create(null);
  var allBusy = false;
  var rootEl = null;
  var lastSig = "";

  function paintBadge() {
    var n = L.errorCount(list);
    PB.setBadge("subs", n ? String(n) : "", n ? "err" : "");
  }

  function findCard(id) {
    if (!rootEl || !rootEl.querySelectorAll) return null;
    var cards = rootEl.querySelectorAll(".sub-card");
    for (var i = 0; i < cards.length; i++) if (cards[i].getAttribute("data-sid") === id) return cards[i];
    return null;
  }

  function paintStats(now) {
    if (!rootEl || !rootEl.querySelectorAll) return;
    var stats = L.summarize(list, now);
    var values = rootEl.querySelectorAll(".stat-v");
    if (!values.length) return;
    if (values[0]) values[0].innerHTML = stats.enabled + "<small>/ " + stats.total + "</small>";
    if (values[1]) values[1].innerHTML = stats.nodes + "<small>个</small>";
    if (values[2]) values[2].textContent = stats.next;
  }

  function show() {
    paintBadge();
    if (!rootEl || PB.current() !== "subs") return;
    var sig = L.subsSignature(list, busy);
    if (sig === lastSig && rootEl.querySelector && rootEl.querySelector(".subs")) return;
    var plan = L.diffSubs(shown, list, busy);
    if (!plan.reorder && plan.changed.length && rootEl.querySelector && rootEl.querySelector(".sub-card")) {
      var now = Date.now();
      var missed = false;
      for (var i = 0; i < plan.changed.length; i++) {
        var id = plan.changed[i];
        var card = findCard(id);
        var sub = null;
        for (var n = 0; n < list.length; n++) if (list[n] && String(list[n].id) === id) sub = list[n];
        if (!card || !sub) { missed = true; break; }
        var focus = typeof document !== "undefined" && document.activeElement && card.contains && card.contains(document.activeElement);
        card.outerHTML = L.cardHTML(sub, now, busy);
        if (focus) {
          var next = findCard(id);
          var button = next && next.querySelector && next.querySelector("[data-act='toggle']");
          if (button && button.focus) button.focus();
        }
      }
      if (!missed) {
        paintStats(now);
        shown = list.slice();
        lastSig = sig;
        return;
      }
    }
    rootEl.innerHTML = L.pageHTML(list, { now: Date.now(), busy: busy, loaded: loaded });
    shown = list.slice();
    lastSig = sig;
  }

  function remember(data) {
    var next = data && data.subscriptions;
    list = Array.isArray(next) ? next.filter(function (sub) { return sub && typeof sub === "object"; }) : [];
    loaded = true;
    show();
  }

  function load() {
    return PB.api("GET", "/api/subs").then(remember).catch(function (err) {
      loaded = true;
      show();
      PB.toast((err && err.message) || "读取订阅失败", { tone: "err" });
    });
  }

  function subById(id) {
    for (var i = 0; i < list.length; i++) {
      if (list[i] && String(list[i].id) === String(id)) return list[i];
    }
    return null;
  }

  function markInvalid(card, input, errEl, message) {
    if (input) input.classList.add("invalid");
    if (errEl) errEl.textContent = message;
    if (input && input.focus) input.focus();
    card.classList.remove("shake");
    void card.offsetWidth;
    card.classList.add("shake");
  }

  function readForm(card, prev) {
    var name = card.querySelector("#fName").value.trim();
    var url = card.querySelector("#fUrl").value.trim();
    var filterEl = card.querySelector("#fFilter");
    var uaEl = card.querySelector("#fUa");
    var active = card.querySelector("#fInt .seg-btn.is-active");
    var interval = prev ? L.intervalMinutes(prev.updateIntervalMin) : 360;
    if (active) interval = L.intervalMinutes(active.getAttribute("data-v"));
    return {
      name: name,
      url: url,
      filter: filterEl ? filterEl.value : "",
      userAgent: uaEl ? uaEl.value.trim() : "",
      updateIntervalMin: interval
    };
  }

  function openEditor(sub) {
    var editing = !!sub;
    PB.openSheet({
      title: editing ? "编辑订阅" : "添加订阅",
      sub: editing ? (sub.name || "") : "支持 Clash / Mihomo YAML 与通用 Base64 订阅",
      icon: '<span class="sheet-ic">' + PB.ic(editing ? "pencil" : "link") + "</span>",
      body: L.formHTML(editing ? sub : null),
      onMount: function (card) {
        var seg = card.querySelector("#fInt");
        PB.mountSeg(seg);
        var url = card.querySelector("#fUrl");
        if (url) {
          url.addEventListener("input", function () {
            url.classList.remove("invalid");
            var err = card.querySelector("#eUrl");
            if (err) err.textContent = "";
          });
        }
      },
      actions: [
        { label: "取消", kind: "plain" },
        {
          label: editing ? "保存" : "添加并更新",
          kind: "prominent",
          fn: function (btn, card) {
            var current = editing ? (subById(sub.id) || sub) : null;
            var draft = readForm(card, current);
            if (!L.validSubURL(draft.url)) {
              markInvalid(card, card.querySelector("#fUrl"), card.querySelector("#eUrl"), "请输入以 http:// 或 https:// 开头的有效链接");
              return false;
            }
            if (editing && !draft.name) {
              markInvalid(card, card.querySelector("#fName"), card.querySelector("#eName"), "请填写名称");
              return false;
            }
            if (!editing && !draft.name) draft.name = L.defaultName(draft.url);
            if (editing) {
              var body = L.diffSub(current, draft);
              if (!body) {
                PB.toast("没有需要保存的更改", { tone: "info" });
                return;
              }
              return PB.api("PUT", "/api/subs", body).then(function () {
                PB.toast("订阅已保存");
                return load();
              });
            }
            return PB.api("POST", "/api/subs", {
              name: draft.name,
              url: draft.url,
              userAgent: draft.userAgent,
              filter: draft.filter,
              updateIntervalMin: L.intervalMinutes(draft.updateIntervalMin),
              enabled: true
            }).then(function (created) {
              var id = created && created.subscription && created.subscription.id;
              if (!id) return { updated: false, error: "订阅已保存，但没有返回标识" };
              return PB.api("POST", "/api/subs/update", { id: id }).then(function () {
                return { updated: true };
              }, function (err) {
                return { updated: false, error: (err && err.message) || "更新失败" };
              });
            }).then(function (result) {
              return load().then(function () {
                if (result && result.updated) PB.toast("已添加并更新订阅");
                else PB.toast("已添加订阅，更新失败：" + ((result && result.error) || "更新失败"), { tone: "warn" });
              });
            });
          }
        }
      ]
    });
  }

  function toggle(sub) {
    if (!sub || marked(sub.id)) return;
    var next = !sub.enabled;
    busy[sub.id] = true;
    sub.enabled = next;
    show();
    PB.api("PUT", "/api/subs", L.enabledPatch(sub.id, next)).then(function () {
      PB.toast((next ? "已启用「" : "已停用「") + (sub.name || "订阅") + "」", { tone: next ? "ok" : "info" });
    }).catch(function (err) {
      sub.enabled = !next;
      PB.toast((err && err.message) || "保存失败", { tone: "err" });
    }).then(function () {
      delete busy[sub.id];
      show();
    });
  }

  function marked(id) {
    return Object.prototype.hasOwnProperty.call(busy, id) && !!busy[id];
  }

  function updateOne(sub) {
    if (!sub || marked(sub.id)) return;
    busy[sub.id] = true;
    show();
    PB.api("POST", "/api/subs/update", { id: sub.id }).then(function () {
      PB.toast("「" + (sub.name || "订阅") + "」已更新");
      return load();
    }).catch(function (err) {
      PB.toast((err && err.message) || "更新失败", { tone: "err" });
      return load();
    }).then(function () {
      delete busy[sub.id];
      show();
    });
  }

  function updateAll() {
    if (allBusy) return;
    allBusy = true;
    var btn = document.getElementById("subAll");
    if (btn) {
      btn.disabled = true;
      btn.innerHTML = '<span class="spin xs"></span><span class="lbl">更新中</span>';
    }
    list.forEach(function (sub) {
      if (sub && sub.enabled && sub.id != null) busy[sub.id] = true;
    });
    show();
    PB.api("POST", "/api/subs/update", {}).then(function (data) {
      var summary = L.updateSummary(data && data.results);
      if (data && Array.isArray(data.subscriptions)) remember(data);
      else return load().then(function () { return summary; });
      return summary;
    }).then(function (summary) {
      if (summary) PB.toast(summary.text, { tone: summary.tone });
    }).catch(function (err) {
      PB.toast((err && err.message) || "更新失败", { tone: "err" });
      return load();
    }).then(function () {
      Object.keys(busy).forEach(function (id) { delete busy[id]; });
      allBusy = false;
      show();
      var again = document.getElementById("subAll");
      if (again) {
        again.disabled = false;
        again.innerHTML = PB.ic("refresh") + '<span class="lbl">全部更新</span>';
      }
    });
  }

  function remove(sub) {
    if (!sub) return;
    var nodes = Number(sub.nodeCount);
    if (!isFinite(nodes) || nodes < 0) nodes = 0;
    PB.confirmSheet({
      title: "删除「" + (sub.name || "订阅") + "」？",
      danger: true,
      okLabel: "删除订阅",
      body: "将删除该订阅及其 <b>" + Math.round(nodes) + "</b> 个节点。删除后不能撤销。",
      onOk: function () {
        return PB.api("DELETE", "/api/subs?id=" + encodeURIComponent(sub.id)).then(function () {
          PB.toast("已删除「" + (sub.name || "订阅") + "」", { tone: "info" });
          return load();
        });
      }
    });
  }

  function onClick(event) {
    var actEl = event.target && event.target.closest ? event.target.closest("[data-act]") : null;
    if (!actEl || actEl.disabled) return;
    var act = actEl.getAttribute("data-act");
    if (act === "add") {
      openEditor(null);
      return;
    }
    var card = event.target.closest("[data-sid]");
    if (!card) return;
    var sub = subById(card.getAttribute("data-sid"));
    if (!sub) return;
    if (act === "toggle") toggle(sub);
    else if (act === "update") updateOne(sub);
    else if (act === "edit") openEditor(sub);
    else if (act === "del") remove(sub);
    else if (act === "copy") PB.copyText(sub.url || "", "订阅链接已复制");
  }

  function bindTop() {
    var bar = document.getElementById("tbActions");
    if (!bar) return;
    bar.onclick = function (event) {
      var target = event.target && event.target.closest ? event.target : null;
      if (!target || !target.closest) return;
      if (target.closest("#subAll")) updateAll();
      else if (target.closest("#subAdd")) openEditor(null);
    };
  }

  var queueSubs = globalThis.PBUtil && PBUtil.coalesce ? PBUtil.coalesce(function () { load(); }) : function () { load(); };

  PB.registerTab("subs", {
    enter: function (root) {
      rootEl = root;
      PB.topbar({ actions: L.topbarHTML() });
      bindTop();
      root.onclick = onClick;
      show();
      load();
    },
    onSSE: function (msg) {
      if (msg && msg.type === "subs") queueSubs();
    }
  });

  document.addEventListener("DOMContentLoaded", function () {
    if (!loaded) load();
  });
})();
