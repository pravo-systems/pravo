/* pravo brownfield design site: shared behaviour. No build step, no network, no dependencies.
   What it does, in order:
     1. theme (system preference, toggle, persisted)        6. stepper     ([data-stepper])
     2. shared header, footer and pager from one page list  7. tabs        ([data-tabs])
     3. progress bar, heading anchors, on-page contents     8. code        (pre[data-lang], copy buttons)
     4. scroll reveal ([data-reveal])                       9. demos       ([data-demo], components page only)
     5. SVG arrow markers for the diagram helpers
   Everything degrades: with no script the page is a plain readable document. */
(function () {
  'use strict';

  /* ---- the one list of pages: edit here to change the nav, footer and pager everywhere ---- */
  var PAGES = [
    { file: 'index.html', nav: 'Start', title: 'Start here: the story', n: '' },
    { file: '01-the-problem.html', nav: 'The problem', title: 'The problem', n: '01' },
    { file: '02-the-idea.html', nav: 'The idea', title: 'The idea', n: '02' },
    { file: '03-a-worked-example.html', nav: 'Worked example', title: 'A worked example', n: '03' },
    { file: '04-how-it-works.html', nav: 'How it works', title: 'How it works', n: '04' },
    { file: '05-what-providers-can-do.html', nav: 'Providers', title: 'What providers can do', n: '05' },
    { file: '06-where-it-gets-messy.html', nav: 'Messy bits', title: 'Where it gets messy', n: '06' },
    { file: '07-decisions-and-roadmap.html', nav: 'Decisions', title: 'Decisions and roadmap', n: '07' },
    { file: 'glossary.html', nav: 'Glossary', title: 'Glossary', n: '' }
  ];
  var SITE_TITLE = 'brownfield design';
  var DRAFT_LINE = 'Draft for discussion. Written 22 October 2026. Nothing here is built yet.';

  var doc = document, root = doc.documentElement;
  var reduceMotion = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  root.classList.add('js');
  /* ?static turns all motion and reveals off: handy for screenshots and printing */
  if (/[?&]static\b/.test(window.location.search)) root.classList.add('no-motion');

  /* ---- tiny helpers ---- */
  function $(sel, ctx) { return (ctx || doc).querySelector(sel); }
  function $$(sel, ctx) { return Array.prototype.slice.call((ctx || doc).querySelectorAll(sel)); }
  function el(tag, cls, html) { var e = doc.createElement(tag); if (cls) e.className = cls; if (html != null) e.innerHTML = html; return e; }
  function esc(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); }
  function store(k, v) { try { if (v === undefined) return window.localStorage.getItem(k); if (v === null) window.localStorage.removeItem(k); else window.localStorage.setItem(k, v); } catch (e) { /* storage blocked: fine */ } return null; }
  function ico(name) { return '<span class="ico ico-' + name + '" aria-hidden="true"></span>'; }
  function slug(s) { return String(s).toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 48) || 'section'; }

  /* ---- 1. theme ---- */
  function effectiveTheme() {
    var t = root.getAttribute('data-theme');
    if (t === 'light' || t === 'dark') return t;
    return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  }
  var saved = store('bf-theme');
  if (saved === 'light' || saved === 'dark') root.setAttribute('data-theme', saved);

  function setTheme(t) {
    root.setAttribute('data-theme', t);
    store('bf-theme', t);
    var b = $('.theme-toggle');
    if (b) { b.setAttribute('aria-label', 'Colour theme: ' + t + '. Switch to ' + (t === 'dark' ? 'light' : 'dark')) }
  }

  /* ---- 2. header, footer, pager ---- */
  function currentFile() {
    var b = doc.body;
    if (b && b.getAttribute('data-page')) return b.getAttribute('data-page');
    var p = window.location.pathname.split('/').pop();
    return p || 'index.html';
  }

  function buildHeader() {
    var host = $('[data-site-header]');
    if (!host) { host = el('header'); doc.body.insertBefore(host, doc.body.firstChild); }
    host.className = 'site-header';
    var here = currentFile();
    var links = PAGES.map(function (p) {
      var cur = p.file === here ? ' aria-current="page"' : '';
      return '<li><a href="' + p.file + '"' + cur + '>' + (p.n ? '<span class="num">' + p.n + '</span>' : '') + esc(p.nav) + '</a></li>';
    }).join('');
    host.innerHTML =
      '<div class="bar">' +
      '<a class="brand" href="index.html" aria-label="pravo ' + SITE_TITLE + ', start page"><span class="brand-mark" aria-hidden="true"></span><span class="brand-name">pravo</span><span class="brand-sub">' + SITE_TITLE + '</span></a>' +
      '<nav class="nav" id="site-nav" aria-label="Chapters"><ul>' + links + '</ul></nav>' +
      '<div class="header-tools">' +
      '<button class="icon-btn theme-toggle" type="button"><span class="ico" aria-hidden="true"></span></button>' +
      '<button class="icon-btn menu-btn" type="button" aria-expanded="false" aria-controls="site-nav" aria-label="Open the chapter list"><span class="ico" aria-hidden="true"></span></button>' +
      '</div></div><div class="progress" aria-hidden="true"></div>';
    var nav = $('.nav', host);
    var menu = $('.menu-btn', host);
    menu.addEventListener('click', function () {
      var open = host.hasAttribute('data-open');
      if (open) host.removeAttribute('data-open'); else host.setAttribute('data-open', '');
      menu.setAttribute('aria-expanded', open ? 'false' : 'true');
      menu.setAttribute('aria-label', open ? 'Open the chapter list' : 'Close the chapter list');
      if (!open) { var first = $('a', nav); if (first) first.focus(); }
    });
    nav.addEventListener('click', function (e) { if (e.target.closest('a')) host.removeAttribute('data-open'); });
    doc.addEventListener('keydown', function (e) { if (e.key === 'Escape' && host.hasAttribute('data-open')) { host.removeAttribute('data-open'); menu.setAttribute('aria-expanded', 'false'); menu.setAttribute('aria-label', 'Open the chapter list'); menu.focus(); } });
    $('.theme-toggle', host).addEventListener('click', function () { setTheme(effectiveTheme() === 'dark' ? 'light' : 'dark'); });
    setTheme(effectiveTheme());
    if (!saved) { /* do not persist the system choice until the reader picks one */ store('bf-theme', null); }
  }

  function buildFooter() {
    var host = $('[data-site-footer]');
    var here = currentFile(), i = -1;
    PAGES.forEach(function (p, k) { if (p.file === here) i = k; });
    var pager = '';
    if (i >= 0 && !$('[data-no-pager]')) {
      var prev = PAGES[i - 1], next = PAGES[i + 1];
      pager = '<nav class="pager wrap" aria-label="Previous and next chapter">' +
        (prev ? '<a class="prev" href="' + prev.file + '" rel="prev"><small>' + ico('arrow-l') + 'Previous</small><strong>' + esc(prev.title) + '</strong></a>' : '<span></span>') +
        (next ? '<a class="next" href="' + next.file + '" rel="next"><small>' + ico('arrow') + 'Next</small><strong>' + esc(next.title) + '</strong></a>' : '') +
        '</nav>';
    }
    var list = PAGES.map(function (p) { return '<li><a href="' + p.file + '"' + (p.file === here ? ' aria-current="page"' : '') + '>' + (p.n ? p.n + ' ' : '') + esc(p.nav === 'Start' ? 'Start here' : p.title) + '</a></li>'; }).join('');
    var wrapper = el('div');
    wrapper.innerHTML = pager +
      '<footer class="site-footer"><div class="wrap"><div class="cols">' +
      '<div><h2>About this site</h2><p>This is the design for bringing existing cloud resources under pravo, written for people who have never done it and for the engineers who will build it.</p><p>' + esc(DRAFT_LINE) + '</p>' +
      '<a class="to-top" href="#top">' + ico('up') + 'Back to top</a><button type="button" class="motion-toggle" aria-pressed="false">Pause motion</button></div>' +
      '<div><h2>Chapters</h2><ul>' + list + '</ul></div></div></div></footer>';
    while (wrapper.firstChild) doc.body.appendChild(wrapper.firstChild);
    if (host) host.parentNode.removeChild(host);
    var mt = $('.motion-toggle');
    function setMotion(off) { root.classList.toggle('no-motion', off); mt.setAttribute('aria-pressed', off ? 'true' : 'false'); mt.textContent = off ? 'Motion paused. Resume' : 'Pause motion'; try { localStorage.setItem('bf-motion', off ? 'off' : 'on'); } catch (e) {} }
    var m0 = null; try { m0 = localStorage.getItem('bf-motion'); } catch (e) {}
    if (m0 === 'off') setMotion(true);
    mt.addEventListener('click', function () { setMotion(!root.classList.contains('no-motion')); });
    $$('.stepper-steps h4, .timeline h4').forEach(function (h) { h.setAttribute('aria-level', '3'); });
    $('.to-top').addEventListener('click', function (e) { e.preventDefault(); window.scrollTo({ top: 0, behavior: reduceMotion ? 'auto' : 'smooth' }); });
  }

  /* ---- 3. progress, anchors, contents ---- */
  function ensureIds() {
    var seen = {};
    $$('[id]').forEach(function (n) { seen[n.id] = 1; });
    $$('main h2, main h3').forEach(function (h) {
      if (h.id || h.closest('.demo')) return;
      var base = slug(h.textContent), id = base, k = 2;
      while (seen[id]) id = base + '-' + (k++);
      seen[id] = 1; h.id = id;
    });
  }
  function addAnchors() {
    $$('main :is(h2, h3)[id]').forEach(function (h) {
      if ($('.heading-anchor', h) || h.closest('.demo, .stepper, .decision, .pushback, .analogy')) return;
      var a = el('a', 'heading-anchor', '#');
      a.href = '#' + h.id; a.setAttribute('aria-label', 'Link to this section');
      h.appendChild(a);
    });
  }
  function buildToc() {
    $$('[data-toc]').forEach(function (nav) {
      var hs = $$('main .section > h2[id]').filter(function (h) { return !h.closest('.demo'); });
      if (hs.length < 2) { nav.hidden = true; return; }
      nav.classList.add('toc');
      nav.setAttribute('aria-label', 'On this page');
      nav.innerHTML = '<span class="toc-label">On this page</span>' + hs.map(function (h) {
        var t = h.cloneNode(true); var a = $('.heading-anchor', t); if (a) a.remove();
        return '<a class="chip" href="#' + h.id + '">' + esc(t.textContent.trim()) + '</a>';
      }).join('');
    });
  }
  function initScroll() {
    var bar = $('.progress'), links = $$('.toc a'), heads = links.map(function (a) { return doc.getElementById(a.getAttribute('href').slice(1)); }), ticking = false;
    function update() {
      ticking = false;
      var h = root.scrollHeight - window.innerHeight, y = window.scrollY || root.scrollTop;
      if (bar) bar.style.transform = 'scaleX(' + (h > 0 ? Math.min(1, Math.max(0, y / h)) : 0).toFixed(4) + ')';
      var cur = -1;
      heads.forEach(function (n, i) { if (n && n.getBoundingClientRect().top < 140) cur = i; });
      links.forEach(function (a, i) { a.classList.toggle('is-current', i === cur); if (i === cur) a.setAttribute('aria-current', 'location'); else a.removeAttribute('aria-current'); });
    }
    function req() { if (!ticking) { ticking = true; window.requestAnimationFrame(update); } }
    window.addEventListener('scroll', req, { passive: true });
    window.addEventListener('resize', req);
    update();
    /* in-page anchors: move focus to the target and keep the URL in step */
    doc.addEventListener('click', function (e) {
      var a = e.target.closest && e.target.closest('a[href^="#"]');
      if (!a || e.defaultPrevented || e.metaKey || e.ctrlKey || e.shiftKey) return;
      var id = a.getAttribute('href').slice(1);
      if (!id || id === 'top') return;
      var t = doc.getElementById(id);
      if (!t) return;
      e.preventDefault();
      t.scrollIntoView({ behavior: reduceMotion ? 'auto' : 'smooth', block: 'start' });
      try { history.pushState(null, '', '#' + id); } catch (er) { /* file:// may refuse */ }
      if (!t.hasAttribute('tabindex')) t.setAttribute('tabindex', '-1');
      t.focus({ preventScroll: true });
    });
  }

  /* ---- 4. scroll reveal ---- */
  function initReveal() {
    var items = $$('[data-reveal]');
    if (!items.length) return;
    if (!('IntersectionObserver' in window) || reduceMotion || root.classList.contains('no-motion')) { items.forEach(function (n) { n.classList.add('is-in'); }); return; }
    var io = new IntersectionObserver(function (es) {
      es.forEach(function (e) { if (e.isIntersecting) { e.target.classList.add('is-in'); io.unobserve(e.target); } });
    }, { rootMargin: '0px 0px -8% 0px', threshold: 0.05 });
    items.forEach(function (n) { io.observe(n); });
    /* safety net: never leave content hidden */
    window.setTimeout(function () { items.forEach(function (n) { if (!n.classList.contains('is-in')) { var r = n.getBoundingClientRect(); if (r.top < window.innerHeight) n.classList.add('is-in'); } }); }, 1200);
  }

  /* ---- 5. SVG arrow markers, shared by every .dg diagram on the page ---- */
  function addMarkers() {
    if (!$('.dg') || $('#dg-ah')) return;
    var defs = [['dg-ah', 'm-neutral'], ['dg-ah-good', 'm-good'], ['dg-ah-warn', 'm-warn'], ['dg-ah-bad', 'm-bad'], ['dg-ah-key', 'm-key']].map(function (m) {
      return '<marker id="' + m[0] + '" viewBox="0 0 10 10" refX="7.5" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse"><path class="' + m[1] + '" d="M1 1.2L9 5 1 8.8z"/></marker>';
    }).join('');
    var s = el('div');
    s.innerHTML = '<svg class="dg-defs" width="0" height="0" aria-hidden="true" focusable="false"><defs>' + defs + '</defs></svg>';
    doc.body.insertBefore(s.firstChild, doc.body.firstChild);
  }

  /* ---- 6. stepper ---- */
  function parseSteps(str, n) { /* "1 3-4" or "2,3" or "*" -> array of booleans, index 1..n */
    var on = []; var t = String(str || '').trim();
    if (t === '*') { for (var i = 1; i <= n; i++) on[i] = true; return on; }
    t.split(/[\s,]+/).forEach(function (tok) {
      if (!tok) return;
      var m = tok.match(/^(\d+)-(\d+)?$/);
      if (m) { var a = +m[1], b = m[2] ? +m[2] : n; for (var k = a; k <= b; k++) on[k] = true; }
      else on[+tok] = true;
    });
    return on;
  }
  function parseMap(str) { /* "1:node--seen 3:node--managed" -> [[step, value], ...] sorted */
    return String(str || '').trim().split(/\s+(?=\d+:)/).map(function (p) { var i = p.indexOf(':'); return [+p.slice(0, i), p.slice(i + 1)]; }).filter(function (p) { return p[0] > 0; }).sort(function (a, b) { return a[0] - b[0]; });
  }
  function pick(map, step) { var v = null; map.forEach(function (p) { if (p[0] <= step) v = p[1]; }); return v; }

  function initStepper(root_) {
    var stage = $('.stepper-stage', root_), list = $('.stepper-steps', root_);
    if (!stage || !list) return;
    var items = $$(':scope > li', list), n = items.length, cur = 0, timer = null;
    var shows = $$('[data-show]', stage).map(function (e) { return [e, parseSteps(e.getAttribute('data-show'), n)]; });
    var hides = $$('[data-hide]', stage).map(function (e) { return [e, parseSteps(e.getAttribute('data-hide'), n)]; });
    var clss = $$('[data-cls]', stage).map(function (e) { var m = parseMap(e.getAttribute('data-cls')); var all = []; m.forEach(function (p) { p[1].split(/\s+/).forEach(function (c) { if (c && all.indexOf(c) < 0) all.push(c); }); }); return [e, m, all]; });
    var texts = $$('[data-text]', stage).map(function (e) { return [e, parseMap(e.getAttribute('data-text'))]; });
    /* structure: heading becomes a button, body collapses */
    items.forEach(function (li, i) {
      var h = $('h4, h3', li); if (!h) return;
      var b = el('button', 'step-btn'); b.type = 'button'; b.innerHTML = h.innerHTML; h.innerHTML = ''; h.appendChild(b);
      var body = el('div', 'step-body'), inner = el('div'); body.appendChild(inner);
      while (h.nextSibling) inner.appendChild(h.nextSibling);
      li.appendChild(body);
      b.addEventListener('click', function () { stop(); go(i); });
    });
    var ctl = el('div', 'stepper-ctl',
      '<button class="btn" type="button" data-prev>' + ico('arrow-l') + 'Back</button>' +
      '<button class="btn btn-primary" type="button" data-next>Next' + ico('arrow') + '</button>' +
      '<button class="btn btn-quiet" type="button" data-play>' + ico('play') + '<span>Play</span></button>' +
      '<span class="stepper-count" aria-live="polite"></span>');
    $('.stepper-panel', root_).appendChild(ctl);
    var prev = $('[data-prev]', ctl), next = $('[data-next]', ctl), play = $('[data-play]', ctl), count = $('.stepper-count', ctl);
    function go(i) {
      cur = Math.max(0, Math.min(n - 1, i)); var s = cur + 1;
      root_.setAttribute('data-current', s);
      items.forEach(function (li, k) {
        li.classList.toggle('is-active', k === cur); li.classList.toggle('is-done', k < cur);
        var b = $('.step-btn', li); if (b) { if (k === cur) b.setAttribute('aria-current', 'step'); else b.removeAttribute('aria-current'); b.setAttribute('aria-expanded', k === cur ? 'true' : 'false'); }
      });
      shows.forEach(function (p) { p[0].classList.toggle('is-on', !!p[1][s]); });
      hides.forEach(function (p) { p[0].classList.toggle('is-off', !!p[1][s]); });
      clss.forEach(function (c) { var want = pick(c[1], s); c[2].forEach(function (k) { c[0].classList.remove(k); }); if (want) want.split(/\s+/).forEach(function (k) { if (k) c[0].classList.add(k); }); });
      texts.forEach(function (t) { var w = pick(t[1], s); if (w != null) t[0].textContent = w; });
      prev.disabled = cur === 0; next.disabled = cur === n - 1;
      count.textContent = 'Step ' + s + ' of ' + n;
    }
    function stop() { if (timer) { window.clearInterval(timer); timer = null; } play.innerHTML = ico('play') + '<span>Play</span>'; }
    function start() {
      if (cur >= n - 1) go(0);
      play.innerHTML = ico('pause') + '<span>Pause</span>';
      timer = window.setInterval(function () { if (cur >= n - 1) { stop(); play.innerHTML = ico('replay') + '<span>Replay</span>'; return; } go(cur + 1); }, +(root_.getAttribute('data-interval') || 3400));
    }
    prev.addEventListener('click', function () { stop(); go(cur - 1); });
    next.addEventListener('click', function () { stop(); go(cur + 1); });
    play.addEventListener('click', function () { if (timer) stop(); else start(); });
    root_.addEventListener('keydown', function (e) {
      if (e.target.closest && e.target.closest('input, textarea')) return;
      if (e.key === 'ArrowRight') { e.preventDefault(); stop(); go(cur + 1); } else if (e.key === 'ArrowLeft') { e.preventDefault(); stop(); go(cur - 1); }
    });
    go(Math.max(0, (+root_.getAttribute('data-start') || 1) - 1));
    if (reduceMotion) root_.removeAttribute('data-autoplay');
    if (root_.hasAttribute('data-autoplay') && 'IntersectionObserver' in window) {
      var io = new IntersectionObserver(function (es) { if (es[0].isIntersecting) { io.disconnect(); start(); } }, { threshold: 0.6 }); io.observe(root_);
    }
  }

  /* ---- 7. tabs ---- */
  var tabSeq = 0;
  function initTabs(box) {
    var panels = $$(':scope > .tab', box); if (!panels.length) return;
    var group = box.getAttribute('data-sync'), id0 = 'tabs' + (++tabSeq);
    var list = el('div', 'tablist'); list.setAttribute('role', 'tablist');
    var lbl = box.getAttribute('data-tabs-label') || 'Manual path or with an agent', sib = box.previousElementSibling;
    while (sib && !(sib.classList && sib.classList.contains('step-head'))) { sib = sib.previousElementSibling; }
    if (sib && !box.getAttribute('data-tabs-label')) { var hh = sib.querySelector('h3'); if (hh) lbl += ': ' + hh.textContent.replace(/\s*#\s*$/, ''); }
    list.setAttribute('aria-label', lbl);
    var btns = panels.map(function (p, i) {
      var b = el('button', null, (p.getAttribute('data-tab-icon') ? ico(p.getAttribute('data-tab-icon')) : '') + '<span>' + esc(p.getAttribute('data-tab-title') || 'Tab ' + (i + 1)) + '</span>');
      b.type = 'button'; b.setAttribute('role', 'tab'); b.id = id0 + '-t' + i; b.setAttribute('aria-controls', id0 + '-p' + i);
      p.setAttribute('role', 'tabpanel'); p.id = id0 + '-p' + i; p.setAttribute('aria-labelledby', b.id); p.setAttribute('tabindex', '0');
      b.addEventListener('click', function () { select(i, true); });
      list.appendChild(b); return b;
    });
    box.insertBefore(list, box.firstChild); box.classList.add('is-ready');
    function keyOf(i) { return panels[i].getAttribute('data-key') || String(i); }
    function select(i, user, focus) {
      panels.forEach(function (p, k) { p.hidden = k !== i; btns[k].setAttribute('aria-selected', k === i ? 'true' : 'false'); btns[k].tabIndex = k === i ? 0 : -1; });
      if (focus) btns[i].focus();
      if (user && group) { store('bf-tab-' + group, keyOf(i)); doc.dispatchEvent(new CustomEvent('bf:tabsync', { detail: { group: group, key: keyOf(i), from: box } })); }
    }
    list.addEventListener('keydown', function (e) {
      var i = btns.indexOf(doc.activeElement); if (i < 0) return; var j = i;
      if (e.key === 'ArrowRight') j = (i + 1) % btns.length; else if (e.key === 'ArrowLeft') j = (i - 1 + btns.length) % btns.length; else if (e.key === 'Home') j = 0; else if (e.key === 'End') j = btns.length - 1; else return;
      e.preventDefault(); select(j, true, true);
    });
    var start = 0;
    if (group) { var k = store('bf-tab-' + group); if (k) panels.forEach(function (p, i) { if (keyOf(i) === k) start = i; }); doc.addEventListener('bf:tabsync', function (e) { if (e.detail.group === group && e.detail.from !== box) panels.forEach(function (p, i) { if (keyOf(i) === e.detail.key) select(i, false); }); }); }
    select(start, false);
  }

  /* ---- 8. code windows: highlight, line marks, copy ---- */
  var LANGS = {
    cue: [['com', '\\/\\/[^\\n]*'], ['str', '"(?:[^"\\\\\\n]|\\\\.)*"'], ['def', '#[A-Za-z_]\\w*'], ['type', '\\$[A-Za-z_]\\w*'], ['num', '\\b\\d[\\d_.]*\\b'], ['kw', '\\b(?:package|import|let|if|for|in|true|false|null)\\b'], ['fn', '\\b[A-Za-z_]\\w*(?=\\()'], ['key', '[A-Za-z_][\\w-]*(?=[?!]?:)']],
    json: [['key', '"(?:[^"\\\\\\n]|\\\\.)*"(?=\\s*:)'], ['str', '"(?:[^"\\\\\\n]|\\\\.)*"'], ['num', '-?\\b\\d[\\d.eE+-]*\\b|\\b(?:true|false|null)\\b']],
    hcl: [['com', '#[^\\n]*|\\/\\/[^\\n]*'], ['str', '"(?:[^"\\\\\\n]|\\\\.)*"'], ['num', '\\b\\d[\\d_.]*\\b|\\b(?:true|false|null)\\b'], ['kw', '\\b(?:resource|data|provider|variable|output|locals|module|import|terraform|moved|removed|required_providers)\\b'], ['key', '[A-Za-z_][\\w-]*(?=\\s*=)']],
    sh: [['com', '(?:^|\\s)#[^\\n]*'], ['str', '"(?:[^"\\\\\\n]|\\\\.)*"|\'[^\'\\n]*\''], ['fn', '(?:^|\\s)--?[A-Za-z][\\w-]*'], ['num', '\\b\\d[\\d_.]*\\b']],
    html: [['com', '<!--[\\s\\S]*?-->'], ['kw', '<\\/?[A-Za-z][^\\s>\\/]*'], ['str', '"[^"]*"'], ['fn', '(?<=\\s)[\\w:-]+(?==)']]
  };
  var RX = {};
  function rxFor(lang) {
    if (RX[lang] || !LANGS[lang]) return RX[lang];
    RX[lang] = new RegExp(LANGS[lang].map(function (r) { return '(' + r[1] + ')'; }).join('|'), 'gm');
    return RX[lang];
  }
  function tokenize(text, lang) {
    var rx = rxFor(lang); if (!rx) return esc(text);
    var out = '', last = 0, m; rx.lastIndex = 0;
    while ((m = rx.exec(text))) {
      if (m[0] === '') { rx.lastIndex++; continue; }
      var gi = 1; while (gi < m.length && m[gi] === undefined) gi++;
      var cls = LANGS[lang][gi - 1][0], s = m[0], lead = '';
      var ws = s.match(/^\s+/); if ((cls === 'com' || cls === 'fn') && ws && lang === 'sh') { lead = ws[0]; s = s.slice(lead.length); }
      out += esc(text.slice(last, m.index)) + esc(lead) + '<span class="tk-' + cls + '">' + esc(s) + '</span>';
      last = m.index + m[0].length;
    }
    return out + esc(text.slice(last));
  }
  var MARK = /\{([garkd])\|([^}\n]*)\}/g, MARKCLS = { g: 't-add', a: 't-chg', r: 't-del', k: 't-key', d: 't-dim' };
  function termLine(line) {
    if (/^\$ /.test(line)) return '<span class="t-prompt">$</span> <span class="t-cmd">' + tokenize(line.slice(2), 'sh') + '</span>';
    if (/^#/.test(line)) return '<span class="t-dim">' + esc(line) + '</span>';
    var h = esc(line).replace(MARK, function (_, c, t) { return '<span class="' + MARKCLS[c] + '">' + t + '</span>'; });
    if (/^\+ /.test(line)) return '<span class="t-add">' + h + '</span>';
    if (/^~ /.test(line)) return '<span class="t-chg">' + h + '</span>';
    if (/^- /.test(line)) return '<span class="t-del">' + h + '</span>';
    return h;
  }
  function hlSet(spec) {
    var s = {}; String(spec || '').split(/[\s,]+/).forEach(function (t) { if (!t) return; var m = t.match(/^(\d+)(?:-(\d+))?$/); if (!m) return; for (var i = +m[1]; i <= +(m[2] || m[1]); i++) s[i] = 1; }); return s;
  }
  function decorate(pre) {
    var lang = pre.getAttribute('data-lang') || 'text';
    var code = $('code', pre) || pre;
    var raw = code.textContent.replace(/^\n+/, '').replace(/\s+$/, '');
    pre.setAttribute('data-raw', raw);
    if (code.children.length || pre.hasAttribute('data-manual')) return raw; /* hand-marked-up: leave as written */
    var marks = hlSet(pre.getAttribute('data-hl')), lines = raw.split('\n');
    var html = lines.map(function (ln, i) {
      var inner, cls = 'ln' + (marks[i + 1] ? ' is-hl' : '');
      if (lang === 'term') inner = termLine(ln);
      else if (lang === 'diff') { cls += /^\+/.test(ln) ? ' d-add' : /^-/.test(ln) ? ' d-del' : /^@@/.test(ln) ? ' d-hunk' : ' d-ctx'; inner = esc(ln); }
      else if (LANGS[lang]) inner = tokenize(ln, lang);
      else inner = esc(ln);
      return '<span class="' + cls + '">' + inner + '</span>';
    }).join('');
    if (code === pre) { var c = el('code'); pre.textContent = ''; pre.appendChild(c); code = c; }
    code.innerHTML = html;
    return raw;
  }
  function copyText(text, btn) {
    function done(ok) { if (!btn) return; var old = btn.innerHTML; btn.classList.add('is-done'); btn.innerHTML = ok ? ico('check') + 'Copied' : 'Press Ctrl+C'; window.setTimeout(function () { btn.classList.remove('is-done'); btn.innerHTML = old; }, 1600); }
    function fallback() { var t = el('textarea'); t.value = text; t.style.cssText = 'position:fixed;opacity:0;top:0'; doc.body.appendChild(t); t.select(); var ok = false; try { ok = doc.execCommand('copy'); } catch (e) { ok = false; } doc.body.removeChild(t); done(ok); }
    if (navigator.clipboard && window.isSecureContext) navigator.clipboard.writeText(text).then(function () { done(true); }, fallback); else fallback();
  }
  function initCode() {
    $$('pre[data-lang]').forEach(function (pre) { if (pre.getAttribute('data-raw') == null) decorate(pre); });
    $$('.window').forEach(function (w) {
      if (w.hasAttribute('data-nocopy') || $('.copy-btn', w)) return;
      var bar = $('.window-bar', w), pre = $('pre', w); if (!bar || !pre) return;
      var b = el('button', 'copy-btn', ico('copy') + 'Copy'); b.type = 'button';
      b.setAttribute('aria-label', 'Copy ' + (pre.getAttribute('data-lang') === 'term' ? 'the commands' : 'the code'));
      b.addEventListener('click', function () {
        var raw = pre.getAttribute('data-raw') || pre.textContent, txt = raw;
        if (pre.getAttribute('data-lang') === 'term') { var cmds = raw.split('\n').filter(function (l) { return /^\$ /.test(l); }).map(function (l) { return l.slice(2); }); if (cmds.length) txt = cmds.join('\n'); }
        copyText(txt, b);
      });
      bar.appendChild(b);
    });
    $$('[data-copy-text]').forEach(function (b) { b.addEventListener('click', function () { copyText(b.getAttribute('data-copy-text'), null); }); });
  }

  /* ---- 9. demos: show a live example and its exact markup (components page) ---- */
  function dedent(s) { /* ignore the inside of <pre>: its text must stay flush left */
    var lines = s.replace(/^\n+/, '').replace(/\s+$/, '').split('\n'), min = 1e9, inPre = false, skip = [];
    lines.forEach(function (l, i) {
      skip[i] = inPre || /^<pre[\s>]/.test(l);
      if (/<pre[\s>]/.test(l) && !/<\/pre>/.test(l)) inPre = true;
      if (/<\/pre>/.test(l)) inPre = false;
      if (!skip[i] && l.trim()) { var m = l.match(/^ */)[0].length; if (m < min) min = m; }
    });
    if (min === 1e9) min = 0;
    return lines.map(function (l, i) { return skip[i] ? l : l.slice(min); }).join('\n');
  }
  function initDemos() {
    $$('[data-demo]').forEach(function (d) {
      var src = dedent(d.innerHTML.replace(/ data-reveal(="[^"]*")?/g, ''));
      var det = el('details', 'markup'); det.open = !d.hasAttribute('data-collapsed');
      var sum = el('summary', null, 'Markup to copy'); det.appendChild(sum);
      var fig = el('figure', 'window'); fig.innerHTML = '<div class="window-bar"><span class="window-dots" aria-hidden="true"><i></i><i></i><i></i></span><span class="window-title">' + esc(d.getAttribute('data-demo') || 'markup') + '</span><span class="window-kind">html</span></div><pre data-lang="html"><code></code></pre>';
      $('code', fig).textContent = src; det.appendChild(fig);
      d.classList.add('demo'); d.parentNode.insertBefore(det, d.nextSibling);
    });
  }

  /* ---- decision notes (kept in this browser only) ---- */
  function initDecisions() {
    $$('.decision[id]').forEach(function (d) {
      if (d.hasAttribute('data-no-notes')) return;
      var key = 'bf-note-' + d.id, stKey = 'bf-stance-' + d.id;
      var box = el('div', 'decision-notes',
        '<label for="' + d.id + '-note">Your take (saved only in this browser)</label>' +
        '<div class="stance" role="group" aria-label="Your stance"><button type="button" data-s="agree" aria-pressed="false">I agree</button><button type="button" data-s="unsure" aria-pressed="false">Not sure</button><button type="button" data-s="disagree" aria-pressed="false">I would choose differently</button></div>' +
        '<textarea id="' + d.id + '-note" rows="2" placeholder="Why, or what would change your mind"></textarea>');
      d.appendChild(box);
      var ta = $('textarea', box), cur = store(stKey);
      var v = store(key); if (v) ta.value = v;
      ta.addEventListener('input', function () { store(key, ta.value || null); });
      $$('.stance button', box).forEach(function (b) {
        if (b.getAttribute('data-s') === cur) b.setAttribute('aria-pressed', 'true');
        b.addEventListener('click', function () { var on = b.getAttribute('aria-pressed') !== 'true'; $$('.stance button', box).forEach(function (o) { o.setAttribute('aria-pressed', 'false'); }); if (on) b.setAttribute('aria-pressed', 'true'); store(stKey, on ? b.getAttribute('data-s') : null); });
      });
    });
  }

  /* ---- boot ---- */
  function boot() {
    initDemos();            /* first: capture markup before anything decorates it */
    buildHeader();
    buildFooter();
    ensureIds();
    addAnchors();
    buildToc();
    addMarkers();
    $$('[data-stepper]').forEach(initStepper);
    $$('[data-tabs]').forEach(initTabs);
    initCode();
    initDecisions();
    initScroll();
    initReveal();
    /* #hash on load: the header is sticky, scroll-padding handles the offset */
    if (window.location.hash) { var t = doc.getElementById(window.location.hash.slice(1)); if (t) window.setTimeout(function () { t.scrollIntoView(); }, 60); }
  }
  if (doc.readyState === 'loading') doc.addEventListener('DOMContentLoaded', boot); else boot();
})();
