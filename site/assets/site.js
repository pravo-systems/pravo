/* pravo site script. Everything is an enhancement: the pages are complete
   without it. Theme, header, drawer, search palette, copy, tabs, scroll-spy,
   progress, glossary popovers, reveal, counters and pointer light.
   No dependencies. */
(function () {
  "use strict";

  var d = document, root = d.documentElement, body = d.body;
  var $ = function (s, c) { return (c || d).querySelector(s); };
  var $$ = function (s, c) { return Array.prototype.slice.call((c || d).querySelectorAll(s)); };
  var reduced = window.matchMedia ? window.matchMedia("(prefers-reduced-motion: reduce)") : { matches: false };
  var fine = window.matchMedia && window.matchMedia("(hover: hover) and (pointer: fine)").matches;
  root.classList.add("js");

  /* storage can be blocked (private windows, site data off): keep the value for the life of the page */
  var mem = {};
  function get(k) { try { return localStorage.getItem(k); } catch (e) { return mem[k] == null ? null : mem[k]; } }
  function set(k, v) { try { localStorage.setItem(k, v); } catch (e) { mem[k] = v; } }
  function el(tag, cls, text) {
    var n = d.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }

  /* one polite live region for short messages: copied, not available */
  var announcer = null, announceTimer = 0;
  function announce(msg) {
    if (!announcer) {
      announcer = el("p", "sr-only");
      announcer.setAttribute("role", "status");
      body.appendChild(announcer);
    }
    announcer.textContent = msg;
    clearTimeout(announceTimer);
    announceTimer = setTimeout(function () { announcer.textContent = ""; }, 2500);
  }

  /* The site root: body[data-root] when the generator sets it, else the brand link. All paths are relative to it. */
  var brand = $(".brand"), rootAttr = body.getAttribute("data-root");
  var rootURL = rootAttr != null ? new URL(rootAttr || "./", location.href) :
    new URL((brand && brand.getAttribute("href")) || "./", location.href);
  var rootDir = rootURL.href.replace(/[^/]*([?#].*)?$/, "");
  /* sibling scripts (search.js, timeline.js) load on demand from the folder of this file */
  var here = d.currentScript && d.currentScript.src, loaded = {};
  function loadSibling(name, cb) {
    var l = loaded[name];
    if (l && l.done) { if (cb) cb(); return; }
    if (l) { if (cb) l.cbs.push(cb); return; }
    l = loaded[name] = { cbs: cb ? [cb] : [], done: false };
    var s = d.createElement("script");
    s.src = here ? here.replace(/[^/]*$/, name) : rootDir + "assets/" + name;
    s.onload = function () { l.done = true; l.cbs.forEach(function (f) { f(); }); };
    s.onerror = function () { delete loaded[name]; };
    d.head.appendChild(s);
  }
  function pageKey(a) { return a.getAttribute("data-page") || relPath(a.href); }
  function relPath(url) {
    var u = new URL(url, location.href).href.split("#")[0].split("?")[0];
    return u.indexOf(rootDir) === 0 ? u.slice(rootDir.length) : u;
  }

  /* ---------- theme ---------- */
  var tbtn = $("#theme-toggle") || $("[data-theme-toggle]");
  function theme() {
    return root.getAttribute("data-theme") ||
      (window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
  }
  function themeLabel() {
    if (tbtn) tbtn.setAttribute("aria-label", theme() === "dark" ? "Switch to light theme" : "Switch to dark theme");
  }
  if (tbtn) {
    themeLabel();
    tbtn.addEventListener("click", function () {
      var t = theme() === "dark" ? "light" : "dark";
      root.setAttribute("data-theme", t);
      set("pravo-theme", t);
      themeLabel();
    });
  }

  /* ---------- header hairline and reading progress ---------- */
  var header = $(".site-header");
  var rp = null;
  if ($(".doc")) { rp = el("div", "read-progress"); rp.setAttribute("aria-hidden", "true"); body.appendChild(rp); }
  var ticking = false;
  function onScroll() {
    ticking = false;
    var y = window.pageYOffset;
    if (header) header.classList.toggle("is-scrolled", y > 4);
    if (rp) {
      var h = root.scrollHeight - window.innerHeight;
      rp.style.setProperty("--p", h > 0 ? Math.min(1, y / h).toFixed(4) : 0);
    }
  }
  window.addEventListener("scroll", function () {
    if (!ticking) { ticking = true; requestAnimationFrame(onScroll); }
  }, { passive: true });
  onScroll();

  /* ---------- the sidebar opens at the page that is open ---------- */
  /* A long sidebar scrolls inside its column. It starts with the open page in view, a third of the way down. */
  (function () {
    var nav = $(".doc-sidebar > nav"), cur = nav && $("a[aria-current='page']", nav);
    if (cur && nav.scrollHeight > nav.clientHeight) nav.scrollTop = Math.max(0, cur.offsetTop - nav.clientHeight / 3);
  })();

  /* ---------- sidebar drawer (phones) ---------- */
  var side = $(".doc-sidebar"), main = $(".doc-main");
  if (side && main) {
    if (!side.id) side.id = "doc-sidebar";
    var bar = el("div", "drawer-bar"), open = el("button", "drawer-open", "Contents");
    open.type = "button";
    open.setAttribute("aria-controls", side.id);
    open.setAttribute("aria-expanded", "false");
    bar.appendChild(open);
    main.insertBefore(bar, main.firstChild);
    var scrim = el("div", "scrim");
    body.appendChild(scrim);
    /* while the drawer is open it is a dialog: the page behind it cannot be reached */
    var behind = $$(".site-header, .doc-main, .doc-toc, .site-footer");
    var drawer = function (on) {
      body.classList.toggle("drawer-on", on);
      open.setAttribute("aria-expanded", on ? "true" : "false");
      behind.forEach(function (n) { n.inert = on; });
      if (on) {
        side.setAttribute("role", "dialog");
        side.setAttribute("aria-modal", "true");
        side.setAttribute("aria-label", "Contents");
      } else {
        side.removeAttribute("role");
        side.removeAttribute("aria-modal");
        side.removeAttribute("aria-label");
      }
      if (on) { var a = $("a[aria-current]", side) || $("a", side); if (a) a.focus(); } else open.focus();
    };
    open.addEventListener("click", function () { drawer(true); });
    scrim.addEventListener("click", function () { drawer(false); });
    side.addEventListener("click", function (e) { if (e.target.closest("a")) drawer(false); });
    d.addEventListener("keydown", function (e) { if (e.key === "Escape" && body.classList.contains("drawer-on")) drawer(false); });
  }

  /* ---------- heading anchors ---------- */
  $$(".doc h2[id], .doc h3[id], .doc h4[id]").forEach(function (h) {
    if ($(".anchor, .anchor-side", h)) return;
    var a = el("a", "anchor-side", "#");
    a.href = "#" + h.id;
    a.setAttribute("aria-label", "Link to this section");
    h.appendChild(a);
  });

  /* ---------- tables: each cell learns its column heading ---------- */
  /* A table of three or more columns is set as a list of records on a phone (style sheet: .is-stack).
     The cells carry the text of their heading in data-label; tables with merged cells are left alone. */
  $$(".doc table").forEach(function (t) {
    var heads = $$("thead th", t).map(function (th) { return th.textContent.trim(); });
    if (heads.length < 3 || $("[colspan], [rowspan], tbody th", t)) return;
    $$("tbody tr", t).forEach(function (tr) {
      [].forEach.call(tr.children, function (td, i) { if (heads[i]) td.setAttribute("data-label", heads[i]); });
    });
    t.classList.add("is-stack");
  });
  /* a table of two columns that does not fit a phone is set the same way, without labels */
  (function () {
    var mq = window.matchMedia && window.matchMedia("(max-width: 40rem)");
    var pairs = $$(".doc table").filter(function (t) { return $$("thead th", t).length === 2 && !$("[colspan], [rowspan], tbody th", t); });
    if (!mq || !pairs.length) return;
    function fit() {
      pairs.forEach(function (t) {
        t.classList.remove("is-pair");
        var box = t.parentElement.classList.contains("table-wrap") ? t.parentElement : t;
        if (mq.matches && box.scrollWidth > box.clientWidth + 2) t.classList.add("is-pair");
      });
    }
    fit();
    if (mq.addEventListener) mq.addEventListener("change", fit);
  })();

  /* ---------- figures: on a phone the text of a diagram keeps a readable size ---------- */
  /* A diagram that is scaled to a narrow screen would set its labels in 6 or 7 px. Below 40 rem the
     figure keeps the width at which its smallest label is 9.5 px and scrolls sideways instead. */
  (function () {
    var mq = window.matchMedia && window.matchMedia("(max-width: 40rem)");
    var svgs = $$(".fig-body svg[viewBox]");
    if (!mq || !svgs.length) return;
    var MIN = 9.5;
    function fit() {
      svgs.forEach(function (s) {
        if (s.getAttribute("data-min0") == null) s.setAttribute("data-min0", s.style.minWidth || "");
        var base = parseFloat(s.getAttribute("data-min0")) || 0, need = 0;
        if (mq.matches) {
          var vb = s.viewBox.baseVal, small = 1e9;
          $$("text", s).forEach(function (t) {
            var px = parseFloat(getComputedStyle(t).fontSize);
            if (px > 0 && px < small) small = px;
          });
          if (vb && vb.width && small < 1e9) need = Math.ceil(vb.width * MIN / small);
        }
        var w = Math.max(base, need);
        if (w) s.style.minWidth = w + "px"; else s.style.removeProperty("min-width");
      });
    }
    fit();
    if (mq.addEventListener) mq.addEventListener("change", fit);
  })();

  /* ---------- table of contents scroll-spy ---------- */
  /* The current heading is the last one above a line in the upper part of the
     viewport, computed on scroll, so a jump (anchor, reload, restore) is right at once. */
  (function () {
    var links = $$(".doc-toc a[href^='#']");
    if (!links.length) return;
    var map = {}, heads = [], cur = null, queued = false;
    links.forEach(function (a) {
      var h = d.getElementById(decodeURIComponent(a.getAttribute("href").slice(1)));
      if (h) { map[h.id] = a; heads.push(h); }
    });
    if (!heads.length) return;
    function update() {
      queued = false;
      var line = Math.min(260, Math.max(104, window.innerHeight * 0.28)), active = null, i;
      for (i = 0; i < heads.length; i++) {
        if (heads[i].getBoundingClientRect().top <= line) active = heads[i]; else break;
      }
      if (window.innerHeight + window.pageYOffset >= root.scrollHeight - 2) active = heads[heads.length - 1];
      if (active === cur) return;
      cur = active;
      links.forEach(function (a) { a.removeAttribute("aria-current"); });
      if (!active) return;
      var a = map[active.id], nav = a.closest("nav");
      a.setAttribute("aria-current", "true");
      if (nav && nav.scrollHeight > nav.clientHeight + 1) {
        var top = a.getBoundingClientRect().top - nav.getBoundingClientRect().top + nav.scrollTop;
        if (top < nav.scrollTop + 24) nav.scrollTop = Math.max(0, top - 24);
        else if (top + a.offsetHeight > nav.scrollTop + nav.clientHeight - 24) nav.scrollTop = top + a.offsetHeight - nav.clientHeight + 24;
      }
    }
    function queue() { if (!queued) { queued = true; requestAnimationFrame(update); } }
    window.addEventListener("scroll", queue, { passive: true });
    window.addEventListener("resize", queue);
    update();
  })();

  /* ---------- copy ---------- */
  function codeText(fig) {
    if (fig.classList.contains("term")) {
      return $$(".cmd-text", fig).map(function (n) { return n.textContent; }).join("\n");
    }
    var lines = $$(".line", fig);
    if (lines.length) {
      return lines.map(function (l) {
        var c = l.cloneNode(true), ln = $(".ln", c);
        if (ln) ln.remove();
        return c.textContent.replace(/\n$/, "");
      }).join("\n");
    }
    var code = $("code", fig);
    return (code || fig).textContent;
  }
  function copyText(text, done) {
    function fallback() {
      var t = el("textarea");
      t.value = text; t.setAttribute("readonly", "");
      t.style.cssText = "position:fixed;top:0;left:-9999px";
      body.appendChild(t); t.select();
      try { d.execCommand("copy"); done(); } catch (e) { /* no clipboard */ }
      t.remove();
    }
    if (navigator.clipboard && window.isSecureContext) navigator.clipboard.writeText(text).then(done, fallback);
    else fallback();
  }
  $$(".copy-page").forEach(function (b) { if (location.protocol === "file:" || !window.fetch) b.hidden = true; });
  d.addEventListener("click", function (e) {
    var cp = e.target.closest(".copy-page");
    if (cp) {
      fetch(cp.getAttribute("data-md")).then(function (r) { return r.text(); }).then(function (t) {
        copyText(t, function () { cp.textContent = "Copied"; announce("Copied the page as Markdown"); setTimeout(function () { cp.textContent = "Copy page"; }, 1600); });
      }).catch(function () { cp.textContent = "Not available"; announce("The page could not be copied"); });
      return;
    }
    var b = e.target.closest(".copy");
    if (!b) return;
    var fig = b.closest("figure, .install");
    var text = b.getAttribute("data-copy");
    if (text == null) text = fig ? (fig.matches(".install") ? $("code", fig).textContent : codeText(fig)) : "";
    if (!b.hasAttribute("data-label")) b.setAttribute("data-label", b.getAttribute("aria-label") || "Copy");
    copyText(text, function () {
      b.classList.add("is-copied");
      b.setAttribute("aria-label", "Copied");
      announce("Copied");
      setTimeout(function () { b.classList.remove("is-copied"); b.setAttribute("aria-label", b.getAttribute("data-label")); }, 1600);
    });
  });
  $$(".copy").forEach(function (b) { if (!b.getAttribute("aria-label")) b.setAttribute("aria-label", "Copy"); });

  /* ---------- tabs ---------- */
  function selectTab(tabs, i, focus) {
    var list = $$("[role=tab]", tabs);
    list.forEach(function (t, j) {
      var on = j === i, p = d.getElementById(t.getAttribute("aria-controls"));
      t.setAttribute("aria-selected", on ? "true" : "false");
      t.tabIndex = on ? 0 : -1;
      if (p) p.hidden = !on;
      if (on && focus) t.focus();
    });
  }
  $$(".tabs").forEach(function (tabs) {
    var list = $$("[role=tab]", tabs), sel = 0;
    list.forEach(function (t, i) {
      if (t.getAttribute("aria-selected") === "true") sel = i;
      var p = d.getElementById(t.getAttribute("aria-controls"));
      if (p && !p.getAttribute("aria-labelledby") && t.id) p.setAttribute("aria-labelledby", t.id);
      t.addEventListener("click", function () { selectTab(tabs, i); });
      t.addEventListener("keydown", function (e) {
        var n = list.length, k = e.key, j = k === "ArrowRight" ? (i + 1) % n : k === "ArrowLeft" ? (i + n - 1) % n :
          k === "Home" ? 0 : k === "End" ? n - 1 : -1;
        if (j < 0) return;
        e.preventDefault();
        selectTab(tabs, j, true);
      });
    });
    tabs.classList.add("is-ready");
    selectTab(tabs, sel);
  });
  window.pravoUI = {
    selectTab: selectTab, root: rootDir, el: el,
    /* pravoUI.timeline(function (T) { T.create(...) }) loads timeline.js when needed */
    timeline: function (cb) {
      if (window.pravoTimeline) return cb(window.pravoTimeline);
      loadSibling("timeline.js", function () { cb(window.pravoTimeline); });
    }
  };

  /* ---------- open what a link points into: details and tab panels ---------- */
  function openForHash() {
    if (location.hash.length < 2) return;
    var t;
    try { t = d.getElementById(decodeURIComponent(location.hash.slice(1))); } catch (e) { return; }
    for (var n = t; n; n = n.parentElement) {
      if (n.tagName === "DETAILS") n.open = true;
      if (n.hasAttribute && n.getAttribute("role") === "tabpanel" && n.hidden) {
        var tabs = n.closest(".tabs"), tab = tabs && $("[aria-controls='" + n.id + "']", tabs);
        if (tab) tab.click();
      }
    }
    if (t) t.scrollIntoView();
  }
  window.addEventListener("hashchange", openForHash);
  openForHash();

  window.addEventListener("beforeprint", function () {
    $$("details").forEach(function (x) { if (!x.open) { x.open = true; x.setAttribute("data-was-closed", ""); } });
  });
  window.addEventListener("afterprint", function () {
    $$("details[data-was-closed]").forEach(function (x) { x.open = false; x.removeAttribute("data-was-closed"); });
  });

  /* ---------- progress: lessons marked done, kept in this browser ---------- */
  var PK = "pravo-progress";
  function done() { try { return JSON.parse(get(PK) || "[]"); } catch (e) { return []; } }
  function saveDone(a) { set(PK, JSON.stringify(a)); }
  function paintProgress() {
    var list = done();
    var groups = $$(".doc-sidebar nav");
    $$(".doc-sidebar li a").forEach(function (a) {
      var on = list.indexOf(pageKey(a)) >= 0, tag = $(".sr-done", a);
      a.classList.toggle("is-done", on);
      /* the state is also in the name of the link: a diamond that fills in is no use to a screen reader */
      if (on && !tag) { tag = el("span", "sr-only sr-done", " (done)"); a.appendChild(tag); }
      else if (!on && tag) tag.remove();
    });
    /* the label stays the same: aria-pressed says whether the page is marked */
    $$(".mark-done").forEach(function (b) {
      b.setAttribute("aria-pressed", list.indexOf(b.getAttribute("data-page")) >= 0 ? "true" : "false");
    });
    groups.forEach(function (nav) {
      var sec = body.getAttribute("data-section");
      if (sec !== "learn" && sec !== "migrate") return;
      var links = $$("li a[data-page]", nav).filter(function (a) { return !/(^|\/)index\.html$/.test(pageKey(a)); });
      if (links.length < 2) return;
      var n = links.filter(function (a) { return a.classList.contains("is-done"); }).length;
      var box = $(".progress-summary", nav);
      if (!box) {
        box = el("p", "progress-summary");
        box.setAttribute("role", "status");
        box.appendChild(el("span", "txt"));
        var bar = el("span", "bar"); bar.appendChild(el("i"));
        box.appendChild(bar);
        nav.insertBefore(box, nav.firstChild);
      }
      $(".txt", box).textContent = n + " of " + links.length + " pages done";
      $("i", box).style.setProperty("--p", (n / links.length).toFixed(3));
    });
  }
  $$(".mark-done").forEach(function (b) {
    b.addEventListener("click", function () {
      var list = done(), p = b.getAttribute("data-page"), i = list.indexOf(p);
      if (i >= 0) list.splice(i, 1); else list.push(p);
      saveDone(list);
      paintProgress();
    });
  });
  paintProgress();

  /* ---------- reveal on scroll, typed kickers, counters ---------- */
  function countUp(n) {
    var to = parseFloat(n.getAttribute("data-countup")), dec = parseInt(n.getAttribute("data-countup-decimals") || "0", 10);
    var fin = n.textContent, t0 = 0;
    if (isNaN(to) || reduced.matches) return;
    function fmt(v) { return v.toLocaleString("en-US", { minimumFractionDigits: dec, maximumFractionDigits: dec }); }
    function step(t) {
      if (!t0) t0 = t;
      var p = Math.min(1, (t - t0) / 900), e = p === 1 ? 1 : 1 - Math.pow(2, -10 * p);
      n.textContent = fmt(to * e);
      if (p < 1) requestAnimationFrame(step); else n.textContent = fin;
    }
    requestAnimationFrame(step);
  }
  $$(".kicker-type").forEach(function (k) {
    k.style.setProperty("--n", k.textContent.length);
  });
  $$("[data-stagger]").forEach(function (g) {
    Array.prototype.forEach.call(g.children, function (c, i) { c.style.setProperty("--i", i); });
  });
  var rv = $$(".reveal, [data-reveal], .kicker-type, [data-countup], [data-draw]");
  if (reduced.matches || !("IntersectionObserver" in window)) {
    rv.forEach(function (n) { n.classList.add("is-in"); });
  } else {
    var rio = new IntersectionObserver(function (es) {
      es.forEach(function (e) {
        if (!e.isIntersecting) return;
        e.target.classList.add("is-in");
        if (e.target.hasAttribute("data-countup")) countUp(e.target);
        rio.unobserve(e.target);
      });
    }, { threshold: 0.15, rootMargin: "0px 0px -6% 0px" });
    rv.forEach(function (n) { rio.observe(n); });
  }

  /* ---------- the page as a story: the rail follows the band in the middle ---------- */
  (function () {
    var b = $$("[data-band]"), a = $$(".rail a");
    if (!a.length || !("IntersectionObserver" in window)) return;
    var io = new IntersectionObserver(function (es) {
      es.forEach(function (e) {
        if (!e.isIntersecting) return;
        e.target.classList.add("in");
        a.forEach(function (x, i) { x.toggleAttribute("aria-current", b[i] === e.target); });
      });
    }, { rootMargin: "-45% 0px -45% 0px" });
    b.forEach(function (n) { io.observe(n); });
  })();

  /* ---------- the hero: the file and the plan on tabs ---------- */
  (function () {
    var h = $("[data-htabs]");
    if (!h) return;
    var tabs = $$("[role=tab]", h);
    function pick(i, f) {
      tabs.forEach(function (t, j) {
        t.setAttribute("aria-selected", i === j);
        t.tabIndex = i === j ? 0 : -1;
        $("#" + t.getAttribute("aria-controls")).classList.toggle("on", i === j);
        if (f && i === j) t.focus();
      });
    }
    tabs.forEach(function (t, i) {
      t.addEventListener("click", function () { pick(i); });
      t.addEventListener("keydown", function (e) {
        if (e.key === "ArrowRight" || e.key === "ArrowLeft") { e.preventDefault(); pick(1 - i, 1); }
      });
    });
  })();

  /* ---------- the viewer showcase: tabs, arrow keys, auto-advance ---------- */
  (function () {
    var v = $("[data-vw]");
    if (!v) return;
    var tabs = $$("[role=tab]", v), tl = $("[role=tablist]", v);
    function pick(i, f) {
      tabs.forEach(function (t, j) {
        var on = i === j;
        t.setAttribute("aria-selected", on);
        t.tabIndex = on ? 0 : -1;
        $("#" + t.getAttribute("aria-controls")).classList.toggle("on", on);
        if (on && f) { t.focus(); t.scrollIntoView({ block: "nearest", inline: "nearest" }); }
      });
    }
    function at() { return tabs.findIndex(function (t) { return t.getAttribute("aria-selected") === "true"; }); }
    tabs.forEach(function (t, i) { t.addEventListener("click", function () { pick(i); }); });
    tl.addEventListener("keydown", function (e) {
      var k = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key], n = tabs.length;
      if (e.key === "Home") k = -at(); else if (e.key === "End") k = n - 1 - at();
      if (!k) return;
      e.preventDefault();
      pick((at() + k + n) % n, 1);
    });
    tl.addEventListener("animationend", function () { pick((at() + 1) % tabs.length); });
  })();

  /* ---------- the headline's first word types itself out and back ---------- */
  (function () {
    var n = $("[data-swap]");
    if (!n || reduced.matches) return;
    var words = n.getAttribute("data-swap").split(","), at = 0;
    function wait(ms, f) { setTimeout(function () { if (d.hidden) wait(ms, f); else f(); }, ms); }
    function type(w, i, f) {
      n.textContent = w.slice(0, i);
      if (i < w.length) wait(75, function () { type(w, i + 1, f); }); else f();
    }
    function erase(i, f) {
      n.textContent = n.textContent.slice(0, i);
      if (i > 0) wait(45, function () { erase(i - 1, f); }); else f();
    }
    function next() {
      at = (at + 1) % words.length;
      erase(n.textContent.length - 1, function () { wait(260, function () { type(words[at], 1, function () { wait(2600, next); }); }); });
    }
    n.classList.add("is-live");
    wait(2600, next);
  })();

  /* ---------- pointer light: one rAF-throttled listener ---------- */
  if (fine && !reduced.matches && $("[data-light], .spot")) {
    var px = 0, py = 0, pt = null, pending = false;
    d.addEventListener("pointermove", function (e) {
      px = e.clientX; py = e.clientY; pt = e.target;
      if (!pending) { pending = true; requestAnimationFrame(paintLight); }
    }, { passive: true });
    root.addEventListener("pointerleave", function () { $$("[data-light].is-lit").forEach(function (n) { n.classList.remove("is-lit"); }); });
  }
  function paintLight() {
    pending = false;
    var host = pt && pt.closest ? pt.closest("[data-light]") : null;
    $$("[data-light].is-lit").forEach(function (n) { if (n !== host) n.classList.remove("is-lit"); });
    if (host) {
      var r = host.getBoundingClientRect();
      host.style.setProperty("--mx", (px - r.left) + "px");
      host.style.setProperty("--my", (py - r.top) + "px");
      host.classList.add("is-lit");
    }
    var card = pt && pt.closest ? pt.closest(".spot") : null;
    if (card) {
      var c = card.getBoundingClientRect();
      card.style.setProperty("--mx", (px - c.left) + "px");
      card.style.setProperty("--my", (py - c.top) + "px");
    }
  }

  /* ---------- glossary popover ---------- */
  (function () {
    var pop = null, cache = {}, cur = null, timer = 0;
    function mk() {
      pop = el("div", "term-pop");
      pop.setAttribute("role", "tooltip");
      pop.id = "term-pop";
      pop.addEventListener("pointerenter", function () { clearTimeout(timer); });
      pop.addEventListener("pointerleave", hide);
      body.appendChild(pop);
    }
    function place(a) {
      var r = a.getBoundingClientRect(), pw = pop.offsetWidth;
      var x = Math.max(8, Math.min(r.left + window.pageXOffset, window.pageXOffset + root.clientWidth - pw - 8));
      pop.style.left = x + "px";
      pop.style.top = (r.bottom + window.pageYOffset + 8) + "px";
    }
    function show(a, text) {
      if (!pop) mk();
      pop.textContent = "";
      var b = el("b", null, a.getAttribute("data-term") || a.textContent);
      pop.appendChild(b);
      pop.appendChild(el("span", null, text));
      pop.appendChild(el("small", null, "Open in the glossary"));
      place(a);
      pop.classList.add("is-on");
      a.setAttribute("aria-describedby", "term-pop");
    }
    function hide() {
      timer = setTimeout(function () {
        if (pop) pop.classList.remove("is-on");
        if (cur) cur.removeAttribute("aria-describedby");
        cur = null;
      }, 120);
    }
    function definition(a, cb) {
      var def = a.getAttribute("data-def");
      if (def) return cb(def);
      var href = a.href, base = href.split("#")[0], id = decodeURIComponent((href.split("#")[1] || ""));
      if (cache[href] != null) return cb(cache[href]);
      if (!id || !window.fetch) return cb("");
      fetch(base).then(function (r) { return r.text(); }).then(function (t) {
        var doc = new DOMParser().parseFromString(t, "text/html"), n = doc.getElementById(id), out = "";
        if (n) {
          if (n.tagName === "DT" && n.nextElementSibling) out = n.nextElementSibling.textContent;
          else if (/^H[1-6]$/.test(n.tagName)) {
            var s = n.nextElementSibling;
            while (s && !/^(P|DD|UL)$/.test(s.tagName)) s = s.nextElementSibling;
            out = s ? s.textContent : "";
          } else out = n.textContent;
        }
        cache[href] = out.replace(/\s+/g, " ").trim().slice(0, 280);
        cb(cache[href]);
      }).catch(function () { cb(""); });
    }
    function enter(e) {
      var a = e.target.closest && e.target.closest(".term-link");
      if (!a || (e.type === "pointerover" && e.pointerType === "touch")) return;
      clearTimeout(timer);
      cur = a;
      definition(a, function (text) { if (cur === a && text) show(a, text); });
    }
    d.addEventListener("pointerover", enter);
    d.addEventListener("focusin", enter);
    d.addEventListener("pointerout", function (e) { if (e.target.closest && e.target.closest(".term-link")) hide(); });
    d.addEventListener("focusout", function (e) { if (e.target.closest && e.target.closest(".term-link")) hide(); });
    d.addEventListener("keydown", function (e) { if (e.key === "Escape" && pop) pop.classList.remove("is-on"); });
  })();

  /* ---------- search palette: search.js is loaded on first use ---------- */
  (function () {
    function openPalette() {
      if (window.pravoSearch) return window.pravoSearch.open();
      loadSibling("search.js", function () { window.pravoSearch.open(); });
    }
    $$(".search-open, [data-search-open]").forEach(function (b) {
      b.addEventListener("click", openPalette);
      b.addEventListener("pointerenter", function warm() {
        b.removeEventListener("pointerenter", warm);
        if (fine && !window.pravoSearch) loadSibling("search.js");
      });
    });
    d.addEventListener("keydown", function (e) {
      var t = e.target, typing = t && (/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) || t.isContentEditable);
      if ((e.key === "k" || e.key === "K") && (e.ctrlKey || e.metaKey)) { e.preventDefault(); openPalette(); }
      else if (e.key === "/" && !typing && !e.ctrlKey && !e.metaKey && !e.altKey) { e.preventDefault(); openPalette(); }
    });
  })();

  /* ---------- the page that was asked for, on the 404 page ---------- */
  (function () {
    var n = $("[data-notfound-path]");
    if (n) n.textContent = "Requested address: " + location.pathname;
  })();

  /* ---------- the live regions of a demo are silent until the reader uses it ---------- */
  $$(".band [aria-live], .demo [aria-live]").forEach(function (r) {
    var band = r.closest(".band, .demo"), evs = ["pointerdown", "keydown", "focusin"];
    r.setAttribute("aria-live", "off");
    function wake() {
      r.setAttribute("aria-live", "polite");
      evs.forEach(function (ev) { band.removeEventListener(ev, wake, true); });
    }
    evs.forEach(function (ev) { band.addEventListener(ev, wake, true); });
  });

  /* ---------- wide code, terminals and tables scroll: let the keyboard reach them ---------- */
  (function () {
    var boxes = $$(".table-wrap, .code-body, .term-body").filter(function (b) { return !b.hasAttribute("tabindex"); });
    if (!boxes.length) return;
    function name(b) {
      if (b.classList.contains("table-wrap")) return "Scrollable table";
      var fig = b.closest("figure"), file = fig && $(".code-file", fig);
      return (b.classList.contains("term-body") ? "Scrollable terminal" : "Scrollable code") + (file ? ": " + file.textContent : "");
    }
    function fit() {
      boxes.forEach(function (b) {
        var over = b.scrollWidth > b.clientWidth + 1, on = b.hasAttribute("data-scroll");
        if (over && !on) {
          b.tabIndex = 0;
          b.setAttribute("role", "group");
          b.setAttribute("aria-label", name(b));
          b.setAttribute("data-scroll", "");
        } else if (!over && on) {
          b.removeAttribute("tabindex");
          b.removeAttribute("role");
          b.removeAttribute("aria-label");
          b.removeAttribute("data-scroll");
        }
      });
    }
    fit();
    window.addEventListener("resize", fit);
    if (d.fonts && d.fonts.ready) d.fonts.ready.then(fit);
  })();
})();
