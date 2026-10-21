/* pravo widgets. A widget is a <div class="widget" data-widget="NAME"> holding
   a <script type="application/json"> (produced by tools/capture) and a
   <noscript> fallback. This file defines the registry, the initialiser that
   reads data-widget and the embedded JSON, and two reference widgets:
   "unify" and "plan". No dependencies.

   pravoWidgets.register("name", function (el, data, cx) { ... });
     cx.h(tag, class, children, attrs)   build elements
     cx.reduced                          true under prefers-reduced-motion
     cx.timeline(function (T) { ... })   T is window.pravoTimeline, loaded on demand
   Widgets render their own DOM and never reimplement CUE: every result shown
   comes from the JSON. */
(function () {
  "use strict";

  var d = document, registry = {}, mounted = [], ready = false;
  var here = d.currentScript && d.currentScript.src;
  var reduced = window.matchMedia ? window.matchMedia("(prefers-reduced-motion: reduce)") : { matches: false };

  function h(tag, cls, kids, attrs) {
    var n = d.createElement(tag), k;
    if (cls) n.className = cls;
    [].concat(kids == null ? [] : kids).forEach(function (c) {
      if (c != null) n.appendChild(typeof c === "string" ? d.createTextNode(c) : c);
    });
    for (k in attrs || {}) n.setAttribute(k, attrs[k]);
    return n;
  }
  function timeline(cb) {
    if (window.pravoTimeline) return cb(window.pravoTimeline);
    if (window.pravoUI) return window.pravoUI.timeline(cb);
    var s = d.createElement("script");
    s.src = here ? here.replace(/[^\/]*$/, "timeline.js") : "timeline.js";
    s.onload = function () { cb(window.pravoTimeline); };
    d.head.appendChild(s);
  }
  var cx = { h: h, timeline: timeline, get reduced() { return reduced.matches; } };

  function mount(el) {
    if (mounted.indexOf(el) >= 0) return;
    var init = registry[el.getAttribute("data-widget")];
    if (!init) return;
    var js = el.querySelector('script[type="application/json"]'), data = null;
    try { data = js ? JSON.parse(js.textContent) : null; } catch (e) { return; }
    mounted.push(el);
    var keep = js;
    Array.prototype.slice.call(el.childNodes).forEach(function (n) { if (n !== keep) el.removeChild(n); });
    init(el, data || {}, cx);
    el.setAttribute("data-ready", "");
    /* the widget is one figure: name it, and describe it when the JSON carries a sentence for that */
    if (!el.hasAttribute("aria-label")) {
      el.setAttribute("role", "group");
      el.setAttribute("aria-label", (data && data.title) || el.getAttribute("data-widget"));
    }
    if (data && data.alt) {
      var id = "w-alt-" + mounted.length;
      el.appendChild(h("p", "sr-only", data.alt, { id: id }));
      el.setAttribute("aria-describedby", id);
    }
  }
  function mountAll() {
    Array.prototype.slice.call(d.querySelectorAll(".widget[data-widget]")).forEach(mount);
  }
  window.pravoWidgets = {
    register: function (name, init) { registry[name] = init; if (ready) mountAll(); }
  };
  function start() { ready = true; mountAll(); }
  if (d.readyState === "loading") d.addEventListener("DOMContentLoaded", start); else start();
  /* a widget nobody registered keeps its noscript text */
  window.addEventListener("load", function () {
    Array.prototype.slice.call(d.querySelectorAll(".widget[data-widget]:not([data-ready])")).forEach(function (el) {
      var ns = el.querySelector("noscript");
      if (ns && ns.textContent) { var p = h("p", "w-note"); p.innerHTML = ns.textContent; el.appendChild(p); }
    });
  });

  /* ---------------------------------------------------------------------
     unify. JSON:
     { "title": "Unification",
       "left":  {"tag": "a type",  "label": "int"},        // or just "int"
       "right": {"tag": "a range", "label": ">=1 & <=10"}, // or just ">=1 & <=10"
       "merged": "int & >=1 & <=10",                         // "meet" is accepted too
       "chips": [{"value": "3", "ok": true, "result": "3"},
                 {"value": "11", "ok": false, "result": "invalid value 11 (out of bound <=10)"}],
       "note": "optional sentence under the widget" }
     --------------------------------------------------------------------- */
  window.pravoWidgets.register("unify", function (el, data) {
    function side(v) { return typeof v === "string" ? { label: v } : v || {}; }
    var L = side(data.left), R = side(data.right), chips = data.chips || [];
    var left = h("div", "w-card w-left", [h("span", "w-tag", L.tag || ""), h("span", "w-val", L.label || "")]);
    var right = h("div", "w-card w-right", [h("span", "w-tag", R.tag || ""), h("span", "w-val", R.label || "")]);
    var slot = h("div", "w-slot", "no value yet");
    var merged = h("div", "w-card w-merged", [h("span", "w-tag", "unified"), h("span", "w-val", data.merged || data.meet || ""), slot]);
    var go = h("button", "btn btn-primary btn-sm", "Unify", { type: "button" });
    var result = h("p", "w-result", "", { role: "status", "aria-live": "polite" });
    var box = h("div", "w-chips", [], { role: "group", "aria-label": "Values to try" });
    var tokens = 0;

    var btns = chips.map(function (c) {
      var b = h("button", "chip", c.value, { type: "button", disabled: "" });
      b.addEventListener("click", function () { drop(c, b); });
      box.appendChild(b);
      return b;
    });
    el.appendChild(h("div", "w-head", [h("p", "w-title", data.title || "Unification")]));
    el.appendChild(h("div", "w-field", [left, right, merged]));
    el.appendChild(h("div", "w-bar", [go, h("span", "w-label", "Try a value"), box]));
    el.appendChild(result);
    if (data.note) el.appendChild(h("p", "w-note", data.note));

    function state(s) { el.setAttribute("data-state", s); }
    function enable(on) { btns.forEach(function (b) { b.disabled = !on; b.removeAttribute("aria-pressed"); }); }
    function clear() {
      merged.classList.remove("is-ok", "is-bad");
      slot.textContent = "no value yet";
      result.textContent = ""; result.className = "w-result";
    }
    function merge(instant) {
      var t = ++tokens;
      clear();
      if (instant || reduced.matches) { state("merged"); enable(true); go.textContent = "Reset"; return; }
      state("merging");
      go.disabled = true;
      setTimeout(function () {
        if (t !== tokens) return;
        state("merged"); enable(true); go.disabled = false; go.textContent = "Reset";
      }, 560);
    }
    function reset() {
      tokens++;
      state("apart"); enable(false); clear(); go.disabled = false; go.textContent = "Unify";
    }
    function drop(c, b) {
      var ok = !!c.ok;
      btns.forEach(function (x) { x.setAttribute("aria-pressed", x === b ? "true" : "false"); x.classList.toggle("is-bad", x === b && !ok); });
      merged.classList.remove("is-ok", "is-bad", "is-pulse", "is-shake");
      void merged.offsetWidth;
      slot.textContent = "";
      slot.appendChild(h("span", "dropped", c.value));
      merged.classList.add(ok ? "is-ok" : "is-bad");
      if (!reduced.matches) merged.classList.add(ok ? "is-pulse" : "is-shake");
      result.className = "w-result " + (ok ? "is-ok" : "is-bad");
      result.textContent = (ok ? "accepted: " : "rejected: ") + (c.result || "");
    }
    go.addEventListener("click", function () { if (el.getAttribute("data-state") === "apart") merge(); else reset(); });

    reset();
    if (reduced.matches) merge(true);
    else if ("IntersectionObserver" in window) {
      var io = new IntersectionObserver(function (es) {
        if (es[0].isIntersecting) { io.disconnect(); setTimeout(function () { if (el.getAttribute("data-state") === "apart") merge(); }, 500); }
      }, { threshold: 0.6 });
      io.observe(el);
    }
  });

  /* ---------------------------------------------------------------------
     plan. JSON:
     { "title": "pravo plan", "command": "pravo plan", "exit": 0,
       "lines": [{"k": "add", "t": "+ local_file.hello"},
                 {"k": "dim", "t": "    filename = \"hello-dev.txt\""}],
       "summary": {"k": "add", "t": "Plan: 2 to add, 0 to change, 0 to destroy."},
       "speed": 90 }
     k is one of add, change, replace, destroy, err, dim or empty.
     --------------------------------------------------------------------- */
  window.pravoWidgets.register("plan", function (el, data) {
    var lines = (data.lines || []).concat(data.summary ? [Object.assign({ sum: 1 }, data.summary)] : []);
    var speed = data.speed || 90, per = 35, cmd = data.command || "pravo plan";
    var cmdText = h("span", "cmd-text", cmd);
    var out = h("code");
    var els = lines.map(function (l) {
      var s = h("span", "pl" + (l.k ? " out-" + l.k : ""), l.t + "\n");
      if (l.sum) s.style.fontWeight = "600";
      out.appendChild(s);
      return s;
    });
    var live = h("p", "sr-only", "", { "aria-live": "polite" });
    var copy = h("button", "copy", "Copy", { type: "button", "data-copy": cmd });
    var foot = h("div", "term-foot");
    var fig = h("figure", "term", [
      h("figcaption", "code-title", [h("span", "code-file", data.title || "terminal")]),
      h("div", "term-body", [
        h("div", "term-line cmd", [h("span", "prompt", "$"), cmdText]),
        h("pre", "term-out", [out])
      ]),
      copy, foot
    ], { "data-exit": String(data.exit || 0) });
    el.classList.add("w-plan");
    el.appendChild(fig);
    el.appendChild(live);

    function show(i) { els[i].classList.remove("is-pending"); }
    var t0 = cmd.length * per + 450, steps = [[0, function (c) { c.type(cmdText, cmd, per); }]];
    els.forEach(function (_, i) { steps.push([t0 + i * speed, function () { show(i); }]); });
    var tEnd = t0 + els.length * speed;
    var summary = data.summary ? data.summary.t : "";
    cx.timeline(function (T) {
      var tl = T.create(el, {
        steps: steps,
        length: tEnd + 1800,
        loops: 1,
        live: live,
        beats: [
          { t: 0, label: "Command", say: "Command: " + cmd },
          { t: t0, label: "Plan", say: "The plan lists " + els.length + " lines." },
          { t: Math.max(t0, tEnd - speed), label: "Summary", say: summary }
        ],
        reset: function () {
          cmdText.textContent = "";
          els.forEach(function (e) { e.classList.add("is-pending"); });
        }
      });
      foot.appendChild(h("span", "w-label", "recorded output"));
      foot.appendChild(tl.controls());
    });
  });
})();
