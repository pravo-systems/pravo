/* pravo demo: the migration, from the state file to an empty plan.
   Three parts, each an enhancement of markup that is complete without it:
   1. lines of main.tf and stack.cue that correspond are linked on hover, and
      by the buttons below the window;
   2. the terminal types its recorded run on scroll (pravoUI.timeline);
   3. the six steps light up and their line draws as the page scrolls. */
document.addEventListener("DOMContentLoaded", function () {
  "use strict";
  var d = document, ui = window.pravoUI;
  var reduced = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  var $ = function (s, c) { return (c || d).querySelector(s); };
  var $$ = function (s, c) { return Array.prototype.slice.call((c || d).querySelectorAll(s)); };

  /* ---------- 1. corresponding lines ---------- */
  var files = $("#mig-files");
  if (files && ui) (function () {
    var tabs = $(".tabs", files), tab = $$("[role=tab]", files), note = $("#mig-note"), jump = $("#mig-jump");
    var pairs = $$("#mig-pairs .mig-pair"), groups = {}, cur = null, pinned = null, plain = note.innerHTML;
    try { JSON.parse($("#mig-groups").textContent).forEach(function (g) { groups[g.id] = g; }); } catch (e) { return; }
    function side() { return tab[1].getAttribute("aria-selected") === "true" ? 1 : 0; }
    function link(id) {
      var g = groups[id];
      if (!g) return;
      cur = id;
      $$(".line[data-g]", files).forEach(function (l) { l.classList.toggle("is-linked", l.getAttribute("data-g") === id); });
      pairs.forEach(function (b) { if (b.getAttribute("data-g") === id) b.setAttribute("data-on", ""); else b.removeAttribute("data-on"); });
      tab[1 - side()].setAttribute("data-link", "");
      tab[side()].removeAttribute("data-link");
      note.innerHTML = "<b></b> in main.tf is <b></b> in stack.cue.";
      var b = note.querySelectorAll("b");
      b[0].textContent = g.hcl;
      b[1].textContent = g.cue;
      var to = 1 - side(), r = g[to ? "cue_lines" : "hcl_lines"];
      jump.textContent = "Show " + (to ? "stack.cue" : "main.tf") + " " + (r[0] === r[1] ? "line " + r[0] : "lines " + r[0] + " to " + r[1]);
      jump.classList.add("is-on");
    }
    function unlink() {
      if (pinned) return link(pinned);
      cur = null;
      $$(".is-linked", files).forEach(function (l) { l.classList.remove("is-linked"); });
      pairs.forEach(function (b) { b.removeAttribute("data-on"); });
      tab.forEach(function (t) { t.removeAttribute("data-link"); });
      note.innerHTML = plain;
      jump.classList.remove("is-on");
    }
    files.addEventListener("pointerover", function (e) {
      var l = e.target.closest && e.target.closest(".line");
      if (!l || !l.hasAttribute("data-g")) { if (l) unlink(); return; }
      link(l.getAttribute("data-g"));
    });
    files.addEventListener("pointerleave", unlink);
    tabs.addEventListener("click", function () { if (cur) link(cur); });
    jump.addEventListener("click", function () { ui.selectTab(tabs, 1 - side()); if (cur) link(cur); });
    pairs.forEach(function (b) {
      var id = b.getAttribute("data-g");
      b.addEventListener("pointerenter", function () { link(id); });
      b.addEventListener("focus", function () { link(id); });
      b.addEventListener("pointerleave", unlink);
      b.addEventListener("blur", unlink);
      b.addEventListener("click", function () {
        pinned = pinned === id ? null : id;
        pairs.forEach(function (x) { x.setAttribute("aria-pressed", pinned && x === b ? "true" : "false"); });
        if (pinned) link(id); else unlink();
      });
    });
  })();

  /* ---------- 2. the terminal ---------- */
  var run = $("#mig-run");
  if (run && ui) (function () {
    var runs = $$(".mo-run", run), cmds = $$(".cmd-text", run), check = $(".mo-check", run), foot = $("#mig-foot");
    var status = $("#mig-status"), live = $("#mig-live"), beats, ready = false, starts = [], s = [], t = 400, TYPE = 32, LINE = 90;
    try { beats = JSON.parse($("#mig-beats").textContent); } catch (e) { return; }
    var last = status.textContent;
    run.classList.add("is-scripted");
    /* a command is typed into one span while the rest of it waits, invisible, in another, so that
       a command that wraps on a narrow screen has its final height from the start */
    var typed = [], rest = [];
    cmds.forEach(function (c, i) {
      var full = c.textContent;
      c.setAttribute("data-t", full);
      c.textContent = "";
      typed[i] = d.createElement("span"); typed[i].className = "t-typed";
      rest[i] = d.createElement("span"); rest[i].className = "t-rest"; rest[i].setAttribute("aria-hidden", "true");
      rest[i].textContent = full;
      c.appendChild(typed[i]); c.appendChild(rest[i]);
    });
    function type(c, i, text, per) {
      if (c.instant || !(per > 0)) { typed[i].textContent = text; rest[i].textContent = ""; return; }
      typed[i].textContent = ""; rest[i].textContent = text;
      for (var k = 1; k <= text.length; k++) (function (n) {
        c.after(n * per, function () { typed[i].textContent = text.slice(0, n); rest[i].textContent = text.slice(n); });
      })(k);
    }
    function say(i) {
      return function () {
        status.textContent = i === runs.length - 1 ? last : beats[i].label;
        status.classList.toggle("anno-ok", i === runs.length - 1);
      };
    }
    runs.forEach(function (r, i) {
      var lines = $$(".mo", r), text = cmds[i].getAttribute("data-t");
      starts.push(t);
      s.push([t, function (c) { r.classList.add("is-on", "is-typing"); type(c, i, text, TYPE); }]);
      s.push([t, say(i)]);
      t += text.length * TYPE + 350;
      s.push([t, function () { r.classList.remove("is-typing"); }]);
      lines.forEach(function (l, j) { s.push([t + j * LINE, function () { l.classList.add("is-on"); }]); });
      if (i === runs.length - 1) s.push([t + 160, function () { check.classList.add("is-on"); }]);
      t += lines.length * LINE + 700;
    });
    function reset() {
      $$(".is-on, .is-typing", run).forEach(function (e) { e.classList.remove("is-on", "is-typing"); });
      cmds.forEach(function (c, i) { typed[i].textContent = ""; rest[i].textContent = c.getAttribute("data-t"); });
      status.textContent = beats[0].label;
      status.classList.remove("anno-ok");
    }
    ui.timeline(function (T) {
      var tl = T.create(run, {
        steps: s, length: t, loops: 1, live: live, reset: reset, threshold: 0.2,
        beats: starts.map(function (ms, i) { return { t: ms, label: beats[i].label, say: beats[i].say }; })
      });
      foot.appendChild(tl.controls());
      ready = true;
    });
    setTimeout(function () { if (!ready) run.classList.remove("is-scripted"); }, 4000);
  })();

  /* ---------- 3. the six steps ---------- */
  var ol = $("#mig-steps");
  if (ol) (function () {
    var li = $$(".mig-step", ol), queued = false;
    ol.classList.add("is-scripted");
    function paint() {
      queued = false;
      var vh = window.innerHeight, r = ol.getBoundingClientRect();
      var p = reduced ? 1 : Math.max(0, Math.min(1, (vh * 0.85 - r.top) / Math.max(r.height, vh * 0.45)));
      var n = Math.ceil(p * li.length - 0.0001);
      li.forEach(function (x, i) { x.classList.toggle("is-reached", i < n); x.classList.toggle("is-drawn", i < n - 1); });
    }
    function queue() { if (!queued) { queued = true; requestAnimationFrame(paint); } }
    window.addEventListener("scroll", queue, { passive: true });
    window.addEventListener("resize", queue);
    paint();
  })();
});
