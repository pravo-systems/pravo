/* pravo demo: blast radius.
   A graph of 39 resources and four changes, all recorded from the real binary
   (site/data/blast.json, embedded as #bl-data). The markup is the first
   change at rest; this script selects a change, ripples it along the edges,
   swaps the plan lines and the figure, and shows the CUE of the node under
   the pointer or focus. Nothing here evaluates anything. */
document.addEventListener("DOMContentLoaded", function () {
  "use strict";
  var fig = document.getElementById("bl-fig"), src = document.getElementById("bl-data");
  if (!fig || !src) return;
  var D;
  try { D = JSON.parse(src.textContent); } catch (e) { return; }
  var STEP = 130;
  var reduced = window.matchMedia && matchMedia("(prefers-reduced-motion: reduce)").matches;
  function q(s, r) { return (r || fig).querySelector(s); }
  function qa(s, r) { return Array.prototype.slice.call((r || fig).querySelectorAll(s)); }
  function esc(s) { return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;"); }

  var svg = q(".bl-svg"), term = q("#bl-term"), insp = q(".bl-insp"), chg = q(".bl-chg");
  var tabs = qa(".bl-chip", document), live = q("#bl-say", document.body);
  var nodes = qa(".bl-n"), ticks = qa(".bl-t"), edges = qa(".bl-e");
  var idx = {}, cur = -1, pinned = -1, shown = -1, planned = +q("#bl-planned").textContent, tween = 0;
  nodes.forEach(function (el, k) {
    idx[el.getAttribute("data-n")] = k;
    var t = el.querySelector("title");
    if (t) el.removeChild(t);
  });
  var eIdx = edges.map(function (e) { return [idx[e.getAttribute("data-f")], idx[e.getAttribute("data-t")]]; });
  var STATE = { add: "created", change: "updated in place", replace: "replaced", skip: "skipped", ghost: "not in the specification yet" };

  /* ---------- selecting a change ---------- */
  function actOf(s, k) {
    var n = D.nodes[k];
    return s.act[n.id] || (n.g && !s.ghost ? "ghost" : "skip");
  }
  function number(to) {
    var out = q("#bl-planned"), from = planned;
    cancelAnimationFrame(tween);
    planned = to;
    if (reduced || from === to) { out.textContent = to; return; }
    var t0 = 0;
    (function step(t) {
      if (!t0) t0 = t;
      var p = Math.min(1, (t - t0) / 700), e = p === 1 ? 1 : 1 - Math.pow(2, -10 * p);
      out.textContent = Math.round(from + (to - from) * e);
      if (p < 1) tween = requestAnimationFrame(step);
    })(performance.now());
  }
  function apply(i, play) {
    var s = D.scenes[i];
    cur = i;
    if (play) {
      /* clear what was drawn, without transitions, so that it draws again */
      fig.setAttribute("data-seeking", "");
      edges.forEach(function (e) { e.classList.remove("is-hit"); });
      void fig.offsetWidth;
      fig.removeAttribute("data-seeking");
      fig.setAttribute("data-r", fig.getAttribute("data-r") === "0" ? "1" : "0");
    }
    fig.setAttribute("data-scene", s.id);
    nodes.forEach(function (el, k) {
      var n = D.nodes[k], act = actOf(s, k), o = s.ord[n.id];
      el.setAttribute("data-act", act);
      el.style.setProperty("--d", (o === undefined ? 0 : o * STEP + 120) + "ms");
      if (s.lock && s.lock[n.id]) el.setAttribute("data-lock", s.lock[n.id]); else el.removeAttribute("data-lock");
      if (o === 0) el.setAttribute("data-org", ""); else el.removeAttribute("data-org");
      el.setAttribute("aria-label", n.id + ", " + n.t + ", " + STATE[act] + (s.lock && s.lock[n.id] ? ", locked by " + s.lock[n.id] : ""));
      var t = ticks[k];
      t.setAttribute("data-act", act);
      t.style.setProperty("--d", (o === undefined ? 0 : o * STEP + 120) + "ms");
    });
    edges.forEach(function (e, j) {
      var f = eIdx[j][0], t = eIdx[j][1], tn = D.nodes[t], on = s.act[tn.id];
      var hit = !!on && (!!s.act[D.nodes[f].id] || !!tn.g);
      e.classList.toggle("is-hit", hit);
      e.classList.toggle("is-dim", !on);
      if (hit) e.setAttribute("data-ea", on === "add" ? "add" : "change"); else e.removeAttribute("data-ea");
      e.style.setProperty("--o", hit ? s.ord[tn.id] : 0);
    });
    qa(".bl-lane").forEach(function (l) {
      var cap = q(".bl-cap", l), held = null;
      (s.lanes || []).forEach(function (x) { if (x.lane === l.getAttribute("data-lane")) held = x; });
      if (held) l.setAttribute("data-held", held.actor); else l.removeAttribute("data-held");
      cap.textContent = held ? (fig.classList.contains("is-tall") ? held.actor : held.text) : cap.getAttribute("data-cap");
    });
    tabs.forEach(function (b, j) {
      b.setAttribute("aria-selected", j === i ? "true" : "false");
      b.tabIndex = j === i ? 0 : -1;
    });
    fig.setAttribute("aria-labelledby", tabs[i].id);
    /* the plan lines, the change and the figure */
    term.innerHTML = s.term;
    term.scrollTop = 0;
    if (s.lock) {
      /* the lock lines are the point of the change: bring them into view */
      var first = q(".bl-pl[data-n]", term);
      if (first) setTimeout(function () { term.scrollTop = Math.max(0, first.offsetTop - 70); }, reduced ? 0 : 500);
    }
    term.classList.toggle("is-run", !!play && !reduced);
    chg.innerHTML = '<figcaption class="code-title"><span class="code-file bl-chg-file">' + esc(s.change.file) +
      '</span></figcaption><div class="code-body"><pre><code class="bl-chg-code">' + s.change.lines + "</code></pre></div>";
    q("#bl-total").textContent = s.total;
    q("#bl-unit").textContent = s.unit;
    q("#bl-gline").textContent = s.graph;
    live.textContent = s.say;
    number(s.planned);
    pinned = -1;
    shown = -1;
    mark(-1);
    show(idx[s.origin]);
  }

  /* ---------- the inspector ---------- */
  function show(k) {
    if (k < 0 || k === shown) return;
    shown = k;
    var s = D.scenes[cur], n = D.nodes[k], sn = D.snips[n.s], act = actOf(s, k);
    var line = s.lines[n.id] || (act === "skip" ? "This plan does not reach it." : act === "ghost" ? "It is not in the specification yet." : "");
    var reads = n.n.map(function (j) { return "<code>" + esc(D.nodes[j].id) + "</code>"; }).join(", ") || "nothing";
    var r = (s.ghost && n.rz) ? n.rz : n.r;
    insp.innerHTML = '<figcaption class="code-title"><span class="code-file bl-insp-file">' + esc(sn.file) + ":" + sn.from + "-" + sn.to +
      '</span><span class="bl-badge" data-act="' + (s.lock && s.lock[n.id] ? "lock" : act) + '">' + (s.lock && s.lock[n.id] ? "locked by " + esc(s.lock[n.id]) : STATE[act]) +
      '</span></figcaption><div class="code-body"><pre><code class="bl-insp-src">' + sn.html + '</code></pre></div>' +
      '<div class="bl-insp-meta"><p class="bl-insp-id"><b>' + esc(n.id) + "</b> <span>" + esc(n.t) + '</span></p><p class="bl-insp-line">' + esc(line) +
      '</p><p class="bl-insp-rel">reads ' + reads + " &middot; " + (r.length ? "a change here can reach " + r.length + (r.length === 1 ? " node" : " nodes") : "nothing reads it") + "</p></div>";
  }
  function mark(k) {
    qa(".is-pre, .is-self, .is-hot", fig).forEach(function (e) { e.classList.remove("is-pre", "is-self", "is-hot"); });
    if (k < 0) return;
    var n = D.nodes[k], s = D.scenes[cur], r = (s.ghost && n.rz) ? n.rz : n.r, set = {};
    r.forEach(function (j) { set[j] = 1; });
    nodes[k].classList.add("is-self");
    r.forEach(function (j) { nodes[j].classList.add("is-pre"); });
    edges.forEach(function (e, j) {
      var f = eIdx[j][0], t = eIdx[j][1];
      if ((f === k || set[f]) && set[t]) e.classList.add("is-pre");
    });
    qa('.bl-pl[data-n="' + n.id + '"]', term).forEach(function (l) {
      l.classList.add("is-hot");
      var top = l.offsetTop, h = l.offsetHeight;
      if (top < term.scrollTop || top + h > term.scrollTop + term.clientHeight) term.scrollTop = Math.max(0, top - term.clientHeight / 2);
    });
    ticks[k].classList.add("is-hot");
  }
  function focusNode(k) {
    mark(k);
    show(k);
  }
  function leave() {
    var a = document.activeElement, k = a && a.classList && a.classList.contains("bl-n") ? idx[a.getAttribute("data-n")] : pinned;
    mark(k);
    show(k >= 0 ? k : idx[D.scenes[cur].origin]);
  }
  svg.addEventListener("pointerover", function (e) {
    var g = e.target.closest && e.target.closest(".bl-n");
    if (g) focusNode(idx[g.getAttribute("data-n")]);
  });
  svg.addEventListener("pointerleave", leave);
  svg.addEventListener("focusin", function (e) {
    var g = e.target.closest && e.target.closest(".bl-n");
    if (g) { var k = idx[g.getAttribute("data-n")]; focusNode(k); nodes.forEach(function (n) { n.tabIndex = -1; }); g.tabIndex = 0; }
  });
  svg.addEventListener("focusout", function () { leave(); });
  svg.addEventListener("click", function (e) {
    var g = e.target.closest && e.target.closest(".bl-n");
    if (!g) { pinned = -1; leave(); return; }
    var k = idx[g.getAttribute("data-n")];
    pinned = pinned === k ? -1 : k;
    g.setAttribute("aria-pressed", pinned === k ? "true" : "false");
    focusNode(k);
  });
  term.addEventListener("pointerover", function (e) {
    var l = e.target.closest && e.target.closest(".bl-pl[data-n]");
    if (l && idx[l.getAttribute("data-n")] !== undefined) { var k = idx[l.getAttribute("data-n")]; mark(k); show(k); }
  });
  term.addEventListener("pointerleave", leave);
  var tallyEl = q(".bl-tally");
  tallyEl.addEventListener("pointerover", function (e) { var t = e.target.closest(".bl-t"); if (t) { var k = idx[t.getAttribute("data-n")]; mark(k); show(k); } });
  tallyEl.addEventListener("pointerleave", leave);

  /* arrow keys move to the nearest node in that direction on the screen */
  svg.addEventListener("keydown", function (e) {
    var d = { ArrowRight: [1, 0], ArrowLeft: [-1, 0], ArrowDown: [0, 1], ArrowUp: [0, -1] }[e.key], from = e.target.closest && e.target.closest(".bl-n");
    if (!from) return;
    var k = idx[from.getAttribute("data-n")];
    if (e.key === "Escape") { pinned = -1; nodes.forEach(function (n) { n.setAttribute("aria-pressed", "false"); }); leave(); return; }
    if (e.key === "Enter" || e.key === " ") { e.preventDefault(); from.dispatchEvent(new MouseEvent("click", { bubbles: true })); return; }
    if (e.key === "Home" || e.key === "End") { e.preventDefault(); nodes[e.key === "Home" ? 0 : nodes.length - 1].focus(); return; }
    if (!d) return;
    e.preventDefault();
    var a = nodes[k].getBoundingClientRect(), ax = a.left + a.width / 2, ay = a.top + a.height / 2, best = -1, bd = 1e9;
    nodes.forEach(function (el, j) {
      if (j === k) return;
      var b = el.getBoundingClientRect(), vx = b.left + b.width / 2 - ax, vy = b.top + b.height / 2 - ay;
      var along = vx * d[0] + vy * d[1], across = Math.abs(vx * d[1] - vy * d[0]);
      if (along < 4) return;
      var m = along + across * 2.4;
      if (m < bd) { bd = m; best = j; }
    });
    if (best >= 0) nodes[best].focus();
  });
  nodes.forEach(function (el) { el.setAttribute("aria-pressed", "false"); });

  /* ---------- the chips ---------- */
  tabs.forEach(function (b, i) {
    b.addEventListener("click", function () { apply(i, true); });
    b.addEventListener("keydown", function (e) {
      var n = tabs.length, j = e.key === "ArrowRight" ? (i + 1) % n : e.key === "ArrowLeft" ? (i + n - 1) % n : e.key === "Home" ? 0 : e.key === "End" ? n - 1 : -1;
      if (j < 0) return;
      e.preventDefault();
      tabs[j].focus();
      apply(j, true);
    });
  });
  q("#bl-replay", document).addEventListener("click", function () {
    /* back to the plain graph, then the same change again */
    fig.classList.add("is-neutral");
    fig.setAttribute("data-seeking", "");
    edges.forEach(function (e) { e.classList.remove("is-hit"); });
    void fig.offsetWidth;
    fig.removeAttribute("data-seeking");
    setTimeout(function () { fig.classList.remove("is-neutral"); apply(cur, true); }, reduced ? 0 : 260);
  });

  /* ---------- a layout for narrow screens ---------- */
  var wideKeep = null, mq = window.matchMedia ? matchMedia("(max-width: 47.99rem)") : null;
  function layout(tall) {
    var T = D.tall, es = qa(".bl-e"), bands = qa(".bl-band, .bl-hull"), caps = qa(".bl-cap");
    if (!wideKeep) {
      wideKeep = {
        vb: svg.getAttribute("viewBox"),
        nodes: nodes.map(function (n) { return n.getAttribute("transform"); }),
        d: es.map(function (e) { return q(".bl-eb", e).getAttribute("d"); }),
        a: es.map(function (e) { return q(".bl-ah", e).getAttribute("points"); }),
        j: es.map(function (e) { var c = q(".bl-j", e); return c ? [c.getAttribute("cx"), c.getAttribute("cy")] : null; }),
        b: bands.map(function (b) { return ["x", "y", "width", "height"].map(function (a) { return b.getAttribute(a); }); }),
        c: caps.map(function (c) { return [c.getAttribute("x"), c.getAttribute("y"), c.getAttribute("text-anchor")]; })
      };
    }
    var W = wideKeep;
    svg.setAttribute("viewBox", tall ? "0 0 " + T.w + " " + T.h : W.vb);
    nodes.forEach(function (n, k) { n.setAttribute("transform", tall ? "translate(" + T.nodes[k][0] + " " + T.nodes[k][1] + ")" : W.nodes[k]); });
    es.forEach(function (e, j) {
      var d = tall ? T.edges[j] : W.d[j], a = tall ? T.arrows[j] : W.a[j], c = q(".bl-j", e);
      qa(".bl-eb, .bl-ev", e).forEach(function (p) { p.setAttribute("d", d); });
      qa(".bl-ah, .bl-ahv", e).forEach(function (p) { p.setAttribute("points", a); });
      if (c) { var xy = tall ? T.dots[j] : W.j[j]; c.setAttribute("cx", xy[0]); c.setAttribute("cy", xy[1]); }
    });
    bands.forEach(function (b, j) {
      var v = tall ? T.bands[Math.floor(j / 2)] : W.b[j];
      ["x", "y", "width", "height"].forEach(function (a, m) { b.setAttribute(a, v[m]); });
    });
    caps.forEach(function (c, j) {
      var v = tall ? T.caps[j] : W.c[j];
      c.setAttribute("x", v[0]); c.setAttribute("y", v[1]);
      c.setAttribute("text-anchor", tall ? ["start", "middle", "end"][v[2]] : W.c[j][2]);
    });
    fig.classList.toggle("is-tall", tall);
    if (cur >= 0 && D.scenes[cur].lanes) apply(cur, false);
  }
  if (mq) {
    if (mq.matches) layout(true);
    (mq.addEventListener ? mq.addEventListener.bind(mq, "change") : mq.addListener.bind(mq))(function () { layout(mq.matches); });
  }

  /* ---------- first view ---------- */
  apply(0, false);
  term.classList.remove("is-run");
  if (!reduced && "IntersectionObserver" in window) {
    fig.classList.add("is-prep", "is-neutral");
    var io = new IntersectionObserver(function (es) {
      if (!es[0].isIntersecting) return;
      io.disconnect();
      fig.classList.remove("is-prep");
      setTimeout(function () { fig.classList.remove("is-neutral"); apply(0, true); }, 1300);
    }, { threshold: 0.35 });
    io.observe(svg);
    /* if the graph never comes into view in time, nothing stays hidden */
    setTimeout(function () { fig.classList.remove("is-prep", "is-neutral"); }, 15000);
  }
});
