/* pravo search palette. Loaded by site.js on first use (/, Ctrl or Cmd K, the
   Search button). Reads search-index.json from the site root; where fetch is
   unavailable (file:// pages) it loads search-index.js, which sets
   window.PRAVO_SEARCH. Entry: {title, url, section, lesson?, description,
   headings: [{text, id}], keywords?, terms?, suggest?}. terms holds every word
   of the page once, code and tables included; keywords holds words the page
   does not say. Words are lower-cased, split at anything that is not a letter
   or a digit and reduced to a stem, so for_each, for-each and "for each" are
   one query and policy finds Policies. No dependencies. */
(function () {
  "use strict";
  var d = document, body = d.body, ui = window.pravoUI, el = ui.el, rootDir = ui.root;
  var $$ = function (s, c) { return Array.prototype.slice.call((c || d).querySelectorAll(s)); };
  var dlg = null, input, list, status, index = null, results = [], sel = 0, opener = null, loading = false;

  function load(cb) {
    if (index) return cb();
    function take(data) { index = prepare(Array.isArray(data) ? data : (data && data.pages) || []); cb(); }
    function viaScript() {
      var s = d.createElement("script");
      s.src = rootDir + "search-index.js";
      s.onload = function () { take(window.PRAVO_SEARCH || []); };
      s.onerror = function () { take([]); };
      d.head.appendChild(s);
    }
    if (loading) return;
    loading = true;
    if (!window.fetch || location.protocol === "file:") return viaScript();
    fetch(rootDir + "search-index.json").then(function (r) { if (!r.ok) throw new Error("search-index.json: " + r.status); return r.json(); }).then(take).catch(viaScript);
  }

  /* ---------- words ---------- */
  /* A light stemmer: plurals, -ing, -ed and a final e go, so that resource and resources, policy and
     policies, move, moved and moving meet. Both the query and the page go through it. */
  function stem(w) {
    var s = w, cut = false;
    if (s.length > 4 && /ies$/.test(s)) s = s.slice(0, -3) + "y";
    else if (s.length > 5 && /ing$/.test(s)) { s = s.slice(0, -3); cut = true; }
    else if (s.length > 4 && /ed$/.test(s)) { s = s.slice(0, -2); cut = true; }
    else if (s.length > 4 && /(ss|x|z|ch|sh)es$/.test(s)) s = s.slice(0, -2);
    else if (s.length > 3 && /s$/.test(s) && !/(ss|us|is)$/.test(s)) s = s.slice(0, -1);
    if (cut && /i$/.test(s)) s = s.slice(0, -1) + "y";
    if (cut && s.length > 3 && /([bdfgmnprt])\1$/.test(s)) s = s.slice(0, -1);
    if (s.length > 3 && /e$/.test(s)) s = s.slice(0, -1);
    return s;
  }
  function words(str) { return String(str || "").toLowerCase().split(/[^a-z0-9]+/).filter(Boolean); }
  function norm(str) { return words(str).map(stem).join(" "); }

  /* the fields of every entry, normalised once */
  function prepare(entries) {
    entries.forEach(function (p) {
      p._t = norm(p.title);
      p._d = norm(p.description);
      p._k = norm(p.keywords);
      p._h = (p.headings || []).map(function (h) {
        h = typeof h === "string" ? { text: h } : h;
        return { h: h, t: norm(h.text) };
      });
      var seen = {}, ws = [];
      words((p.terms || "") + " " + (p.body || p.text || "")).forEach(function (w) {
        var s = stem(w);
        if (!seen[s]) { seen[s] = 1; ws.push(s); }
      });
      p._w = " " + ws.join(" ");
    });
    return entries;
  }

  /* ---------- scoring ---------- */
  /* Score of one query word against one normalised field. A hit must start a word. Letters that
     are merely in order count only in a title, only for a word of four letters or more, and only
     when they sit close together. */
  function field(str, tok, fuzzy) {
    var i = str.indexOf(tok);
    while (i > 0 && str.charAt(i - 1) !== " ") i = str.indexOf(tok, i + 1);
    if (i >= 0) return 10 + (i === 0 ? 6 : 4) - Math.min(i, 20) / 10;
    if (!fuzzy || tok.length < 4) return 0;
    var j = 0, first = -1, last = -1;
    for (var k = 0; k < str.length && j < tok.length; k++) {
      if (str.charAt(k) === tok.charAt(j)) { if (first < 0) first = k; last = k; j++; }
    }
    return j === tok.length && last - first + 1 <= tok.length + 2 ? 5 : 0;
  }
  /* words of a question that say nothing about the page: "how do I migrate" is a search for migrate */
  var STOP = { a: 1, an: 1, and: 1, are: 1, can: 1, do: 1, does: 1, for: 1, how: 1, i: 1, in: 1, is: 1, it: 1, my: 1, of: 1, on: 1, or: 1, the: 1, to: 1, use: 1, what: 1, with: 1 };
  function rank(q) {
    var raw = words(q), out = [];
    var keep = raw.filter(function (w) { return !STOP[w]; });
    if (keep.length) raw = keep;
    var toks = raw.map(stem), phrase = toks.join(" ");
    if (!toks.length) return out;
    index.forEach(function (p) {
      var total = 0, best = null, bestScore = 0, optional = 0, v, i, j;
      for (i = 0; i < toks.length; i++) {
        var tk = toks[i], s = field(p._t, tk, true) * 4;
        if ((v = field(p._k, tk) * 3) > s) s = v;
        if ((v = field(p._d, tk) * 1.4) > s) s = v;
        for (j = 0; j < p._h.length; j++) {
          v = field(p._h[j].t, tk) * 2.4;
          if (v > s) s = v;
          if (v > bestScore) { bestScore = v; best = p._h[j].h; }
        }
        if (s < 6 && p._w.indexOf(" " + tk) >= 0) s = 6;
        /* a word of one or two characters may be missing from a page that otherwise answers */
        if (!s && tk.length < 3) { optional++; continue; }
        if (!s) return;
        total += s;
      }
      if (optional === toks.length) return;
      if (toks.length > 1) {
        if (p._t.indexOf(phrase) >= 0) total += 20;
        if (p._k.indexOf(phrase) >= 0) total += 20;
        for (j = 0; j < p._h.length; j++) {
          if (p._h[j].t.indexOf(phrase) >= 0) { total += 12; best = p._h[j].h; break; }
        }
      }
      var r = { p: p, s: total };
      if (best && best.id && bestScore * toks.length >= total * 0.9) r.h = best;
      out.push(r);
    });
    out.sort(function (a, b) { return b.s - a.s; });
    return out.slice(0, 40);
  }

  /* the pages offered before anything is typed: those that site.json lists, else the first few */
  function suggestions() {
    var s = index.filter(function (p) { return p.suggest; }).sort(function (a, b) { return a.suggest - b.suggest; });
    return (s.length ? s : index.slice(0, 8)).map(function (p) { return { p: p }; });
  }

  /* ---------- results ---------- */
  function mark(text, q) {
    var frag = d.createDocumentFragment(), toks = words(q).map(stem), re = /[A-Za-z0-9]+/g, pos = 0, m;
    while ((m = re.exec(text))) {
      var s = stem(m[0].toLowerCase()), hit = false;
      for (var i = 0; i < toks.length; i++) if (s.indexOf(toks[i]) === 0 || (toks[i].length > 2 && toks[i].indexOf(s) === 0 && s.length > 2)) hit = true;
      if (!hit) continue;
      frag.appendChild(d.createTextNode(text.slice(pos, m.index)));
      frag.appendChild(el("mark", null, m[0]));
      pos = m.index + m[0].length;
    }
    frag.appendChild(d.createTextNode(text.slice(pos)));
    return frag;
  }
  function render() {
    var q = input.value.trim();
    list.textContent = "";
    results = q ? rank(q) : suggestions();
    sel = 0;
    if (!results.length) {
      list.appendChild(el("li", "search-empty", "No page matches “" + q + "”."));
      input.removeAttribute("aria-activedescendant");
      status.textContent = "No results";
      return;
    }
    results.forEach(function (r, i) {
      var li = el("li"), a = el("a");
      var url = r.p.url + (r.h && r.h.id ? "#" + r.h.id : "");
      li.setAttribute("role", "presentation");
      a.href = rootDir + url;
      a.id = "sr" + i;
      a.tabIndex = -1;
      a.setAttribute("role", "option");
      var t = el("span", "r-title");
      t.appendChild(mark(r.p.title || r.p.url, q));
      if (r.h) t.appendChild(d.createTextNode(" › " + r.h.text));
      a.appendChild(t);
      a.appendChild(el("span", "r-sec", (r.p.section || "") + (r.p.lesson ? " · " + r.p.lesson : "")));
      if (r.p.description) a.appendChild(el("span", "r-desc", r.p.description));
      a.addEventListener("pointermove", function () { choose(i, false); });
      /* a result in the page that is open changes only the hash: close the palette first */
      a.addEventListener("click", function () { dlg.close(); });
      li.appendChild(a); list.appendChild(li);
    });
    status.textContent = q ? results.length + (results.length === 1 ? " result" : " results") : "Suggested pages";
    choose(0, false);
  }
  function choose(i, scroll) {
    var as = $$("a", list);
    if (!as.length) return;
    sel = (i + as.length) % as.length;
    as.forEach(function (a, j) { a.setAttribute("aria-selected", j === sel ? "true" : "false"); });
    input.setAttribute("aria-activedescendant", as[sel].id);
    if (scroll !== false) as[sel].scrollIntoView({ block: "nearest" });
  }
  function build() {
    dlg = el("dialog", "search-dialog");
    dlg.setAttribute("aria-label", "Search the documentation");
    var f = el("div", "search-field");
    input = el("input", "search-input");
    input.type = "text"; input.setAttribute("enterkeyhint", "search"); input.placeholder = "Search the documentation";
    input.setAttribute("role", "combobox");
    input.setAttribute("aria-expanded", "true");
    input.setAttribute("aria-controls", "search-list");
    input.setAttribute("aria-label", "Search");
    input.autocomplete = "off"; input.spellcheck = false;
    f.appendChild(input);
    list = el("ul", "search-results");
    list.id = "search-list"; list.setAttribute("role", "listbox");
    list.setAttribute("aria-label", "Results");
    status = el("p", "sr-only");
    status.setAttribute("role", "status");
    var foot = el("div", "search-foot");
    foot.innerHTML = "<span><kbd>↑</kbd> <kbd>↓</kbd> move</span><span><kbd>Enter</kbd> open</span><span><kbd>Esc</kbd> close</span>";
    dlg.appendChild(f); dlg.appendChild(list); dlg.appendChild(status); dlg.appendChild(foot);
    body.appendChild(dlg);
    input.addEventListener("input", render);
    input.addEventListener("keydown", function (e) {
      if (e.key === "ArrowDown") { e.preventDefault(); choose(sel + 1); }
      else if (e.key === "ArrowUp") { e.preventDefault(); choose(sel - 1); }
      else if (e.key === "Enter") {
        var a = $$("a", list)[sel];
        if (a) { e.preventDefault(); dlg.close(); location.href = a.href; }
      }
    });
    dlg.addEventListener("click", function (e) { if (e.target === dlg) dlg.close(); });
    dlg.addEventListener("close", function () { if (opener) opener.focus(); });
  }

  function openPalette() {
    opener = d.activeElement;
    if (!dlg) build();
    if (!dlg.open) { if (dlg.showModal) dlg.showModal(); else dlg.setAttribute("open", ""); }
    input.value = ""; input.focus();
    load(render);
  }
  window.pravoSearch = { open: openPalette, rank: function (q, entries) { if (entries) index = prepare(entries); return rank(q); } };
})();
