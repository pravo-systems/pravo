// pravo serve: the page. It holds the view state, keeps it in the URL fragment, and connects the model, the
// graph, the rail, the table, the inspector and the palette. It only reads: every request is a GET.

import * as api from './api.js';
import { buildModel, buildItems, firstView, touchedModules, reveal, diffModels, FILTERS, CLUSTER_CAP, isChange, chain, isOut } from './model.js';
import { layout } from './layout.js';
import { GraphView, Minimap } from './graph.js';
import { Rail } from './rail.js';
import { TableView } from './table.js';
import { Inspector } from './inspector.js';
import { Plans, planBar, avatar, statusLabel } from './plans.js';
import { AccessView } from './access.js';
import { Palette, Sheet } from './palette.js';
import { parseFragment, listOf, writeFragment } from './route.js';
import { h, s as svgEl, clear, store, relTime, clock, plural, schedule, actionOf, icon, copyText, pathParts, tilde, motionOn, tick } from './util.js';
import { initTips } from './ui.js';

const $ = (id) => document.getElementById(id);
const TABKEYS = ['state', 'source', 'schema', 'history', 'change'];
const STICKY_TABS = new Set(['source', 'schema', 'history']);
const root = document.documentElement;

const S = {
  // graph is what the page draws; raw is what the server sent, and the stored plan that is laid over it (plan, with
  // its overlay when the server did not lay it itself) makes the difference
  graph: null, raw: null, plans: [], plan: null, overlay: null, model: null, vis: null, lay: null, access: null,
  view: 'graph', sel: null, tab: null, filter: null, q: '',
  collapsed: new Set(), auto: new Set(), clusters: new Map(), trail: [],
  theme: null, motion: true, reach: false, zoom: 'fit',
  live: 'connecting', focus: null, palette: false, help: false,
};
let gv, minimap, rail, table, insp, palette, sheet, conn, backBtn, plansList, accessView;
let booted = false;

// ---------------------------------------------------------------------------------------------------------
// announcements and toasts

// copiedWhat names what a copy button copied, from its label ("Copy the reader key of ada" gives "Copied the reader key of ada").
function copiedWhat(btn) {
  const label = btn.getAttribute('aria-label') || '';
  return label.startsWith('Copy ') ? 'Copied ' + label.slice(5) : 'Copied';
}

function announce(text) {
  const el = $('live');
  el.textContent = '';
  setTimeout(() => { el.textContent = text; }, 30);
}

// toast shows a message that slides in and goes by itself; detail is a second, quieter line.
function toast(text, kind = 'info', ttl = 4200, detail = '') {
  const ico = kind === 'error' ? 'bang' : kind === 'warn' ? 'bang' : 'check';
  const t = h('div', { class: 'toast ' + kind, style: `--ttl:${ttl}ms` },
    h('span', { class: 'tico' }, icon(ico)),
    h('div', { class: 'tbody' }, h('div', { class: 'tt' }, text), detail ? h('div', { class: 'td' }, detail) : null),
    h('i', { class: 'tbar' }));
  const box = $('toasts');
  box.append(t);
  while (box.children.length > 4) box.firstElementChild.remove();
  requestAnimationFrame(() => requestAnimationFrame(() => t.classList.add('in')));
  const gone = () => { t.classList.remove('in'); t.classList.add('out'); setTimeout(() => t.remove(), motionOn() ? 260 : 0); };
  let timer = setTimeout(gone, ttl);
  t.addEventListener('click', () => { clearTimeout(timer); gone(); });
  t.addEventListener('pointerenter', () => { clearTimeout(timer); const bar = t.querySelector('.tbar'); if (bar) bar.style.animationPlayState = 'paused'; });
  t.addEventListener('pointerleave', () => { const bar = t.querySelector('.tbar'); if (bar) bar.style.animationPlayState = ''; timer = setTimeout(gone, 1600); });
  announce(detail ? `${text}. ${detail}` : text);
}

// ---------------------------------------------------------------------------------------------------------
// theme and motion

function systemDark() { return matchMedia('(prefers-color-scheme: dark)').matches; }
function effectiveTheme() { return root.getAttribute('data-theme') || (systemDark() ? 'dark' : 'light'); }

function applyTheme(t, persist) {
  S.theme = t;
  if (t) root.setAttribute('data-theme', t); else root.removeAttribute('data-theme');
  if (persist && t) store.set('pravo.theme', t);
  const dark = effectiveTheme() === 'dark';
  const b = $('theme-btn');
  b.querySelector('.i').className = 'i ' + (dark ? 'i-sun' : 'i-moon');
  b.setAttribute('aria-label', dark ? 'Switch to the light theme' : 'Switch to the dark theme');
  if (minimap) { minimap.refreshColors(); minimap.draw(); }
}

function toggleTheme() {
  applyTheme(effectiveTheme() === 'dark' ? 'light' : 'dark', true);
  syncFragment();
  announce(effectiveTheme() === 'dark' ? 'Dark theme' : 'Light theme');
}

// beat makes the live dots beat for a few cycles: when the page opens and when something changes. At rest
// nothing on the page animates (see data-beat in app.css).
let beatT = 0;
function beat() {
  root.removeAttribute('data-beat');
  void root.offsetWidth;
  root.setAttribute('data-beat', '');
  clearTimeout(beatT);
  beatT = setTimeout(() => root.removeAttribute('data-beat'), 8600);
}

function applyMotion(on) {
  S.motion = on;
  if (on) root.removeAttribute('data-motion'); else root.setAttribute('data-motion', 'off');
  if (gv) gv.motion = on;
}

// ---------------------------------------------------------------------------------------------------------
// the fragment

function readFragment() {
  const p = parseFragment(location.hash);
  return {
    sel: p.sel || null,
    tab: TABKEYS.includes(p.tab) ? p.tab : null,
    filter: Object.hasOwn(FILTERS, p.filter) ? p.filter : null,
    // a stored plan by id, 'none' for no plan at all, '' for the plan the server laid over the graph (if any)
    plan: p.plan === 'none' || /^plan-[0-9a-f]{6,64}$/.test(p.plan || '') ? p.plan : '',
    q: p.q || '',
    // an address from before the view was renamed says tree
    view: p.view === 'table' || p.view === 'tree' ? 'table' : p.view === 'access' || p.view === 'state' ? 'access' : 'graph',
    theme: p.theme === 'light' || p.theme === 'dark' ? p.theme : null,
    motion: p.motion !== 'off',
    collapse: listOf(p, 'collapse'),
    expand: listOf(p, 'expand'),
    zoom: p.zoom || 'fit',
    palette: p.palette === '1',
    pq: p.pq || '',
    help: p.help === '1',
  };
}

function syncFragment() {
  schedule('fragment', () => {
    if (!S.model) return;
    // a module inside a closed one says nothing that anyone can see, and a link cannot say it (expand= opens
    // what is above): only the modules that show are written
    const mods = S.model.modules;
    const hidden = (p) => { const m = mods.get(p); for (let u = m && m.up; u && !u.isRoot; u = u.up) if (S.collapsed.has(u.path)) return true; return false; };
    let collapse = [...S.collapsed].filter((x) => !S.auto.has(x) && !hidden(x)).sort();
    let expand = [...S.auto].filter((x) => !S.collapsed.has(x) && !hidden(x)).sort();
    // every module closed, or every module open, has a short name
    if (collapse.length && S.collapsed.size === S.model.modules.size - 1) collapse = ['all'];
    if (expand.length && S.collapsed.size === 0) expand = ['all'];
    expand = expand.concat([...S.clusters.keys()].sort());
    const hash = writeFragment({
      sel: S.sel, tab: S.sel && S.model.nodes.has(S.sel) ? S.tab : null, collapse, expand, filter: S.filter, plan: planFragment(), q: S.q,
      view: S.view, theme: S.theme && (store.get('pravo.theme') === S.theme || new URLSearchParams(location.hash.replace(/^#\/?\??/, '')).has('theme')) ? S.theme : null,
      motion: S.motion ? '' : 'off', zoom: S.zoom,
    });
    if (hash !== location.hash && hash !== '#/?') {
      try { history.replaceState(null, '', location.pathname + location.search + hash); } catch { /* a sandboxed frame */ }
    } else if (hash === '#/?' && location.hash) {
      try { history.replaceState(null, '', location.pathname + location.search); } catch { /* a sandboxed frame */ }
    }
  });
}

// ---------------------------------------------------------------------------------------------------------
// the model and the scene

// linkHides says whether a link closes a module that the selection sits in: collapse= names it or one above
// it (or says all). A selection that no collapse= names is shown, however the first view was made.
function linkHides(frag, mod) {
  if (frag.collapse.includes('all')) return true;
  return chain(mod).some((m) => frag.collapse.includes(m.path));
}

// autoCollapsed is the first view for a model: which modules are closed before anyone has touched anything. It
// depends on the canvas and on what the plan, the drift and the state say, and on nothing the person chooses
// (the selection, a filter, a search): the fragment states the open and closed modules relative to it, so a
// reload, which works it out again, must reach the same answer.
function autoCollapsed(model) {
  // an output is a light item: it does not decide which modules open first
  const touched = touchedModules(model, (n) => !isOut(n) && (isChange(n.action) || n.drifted || n.gone || n.presence === 'state' || n.presence === 'spec'));
  // the canvas as it was when the page opened, before the inspector took room from it on a narrow screen
  const sz = S.autoSize || (gv ? gv.size : { w: 0, h: 0 });
  const fv = firstView(model, new Map(), { width: sz.w, height: sz.h, touched });
  S.firstView = fv;
  return fv.collapsed;
}

// revealSel opens what hides the selection of a link: the modules above a resource, and above a module. A
// collapse= that names a module above it wins.
function revealSel(frag, sel) {
  if (!sel) return;
  const m = S.model;
  if (m.nodes.has(sel)) { if (!linkHides(frag, m.nodes.get(sel).mod)) reveal(m, sel, S.collapsed, S.clusters); } else if (m.modules.has(sel)) {
    const up = m.modules.get(sel).up;
    if (up && !up.isRoot && !linkHides(frag, up)) for (const c of chain(up)) S.collapsed.delete(c.path);
  }
}

// ---------------------------------------------------------------------------------------------------------
// the stored plans
//
// The state holds plans that people stored for each other to look at (pravo plan --store). One of them can be
// laid over the graph: its actions replace the actions on the nodes, and the details of a node answer for it.
// The server lays one itself when it was started with --plan ID; the page can take that one off, or lay another.

// ownPlan is the id of the stored plan that the server laid over the graph itself.
const ownPlan = (raw) => (raw && raw.plan && raw.plan.id) || null;

// shown is the graph the page draws: what the server sent, with the selected stored plan laid over it.
function shown(raw) {
  const own = ownPlan(raw);
  const o = S.overlay;
  if (S.plan && S.plan !== own && o && o.plan.id === S.plan) {
    const act = new Map(o.nodes.map((n) => [n.addr, n.action]));
    return Object.assign({}, raw, {
      plan: o.plan,
      // what the server says of its own plan is not said of this one; the stale plan has its own banner
      warnings: withoutPlanWarnings(raw.warnings).concat((o.warnings || []).filter((w) => !/^The plan is stale/.test(w))),
      nodes: raw.nodes.map((n) => { const c = Object.assign({}, n); if (act.has(n.addr)) c.action = act.get(n.addr); else delete c.action; return c; }),
    });
  }
  if (!S.plan && own) {
    return Object.assign({}, raw, { plan: undefined, warnings: withoutPlanWarnings(raw.warnings), nodes: raw.nodes.map((n) => { const c = Object.assign({}, n); delete c.action; return c; }) });
  }
  return raw;
}

// The sentences that the server adds to the warnings of the graph for the plan that it laid over it.
const PLAN_WARNING = /^(The plan is stale and can no longer be applied|The values of the plan are not shown|The stored plan could not be read|The plan file could not be read)/;
function withoutPlanWarnings(list) { return (list || []).filter((w) => !PLAN_WARNING.test(w)); }

// planParam says which plan the details of a node are about: a stored plan by id, 'none' when the plan of the
// server was taken off, nothing for the plan the graph has.
function planParam() {
  return S.plan || (ownPlan(S.raw) ? 'none' : null);
}

// planFragment is plan= in the fragment: a plan other than the one the page opens with, or none where it opens with one.
function planFragment() {
  const own = ownPlan(S.raw);
  if (S.plan) return S.plan === own ? null : S.plan;
  return own ? 'none' : null;
}

// wantedPlan is the stored plan a fragment asks for, null for none, and the default where it says nothing.
function wantedPlan(f) {
  if (f.plan === 'none') return null;
  if (f.plan && S.plans.some((p) => p.id === f.plan)) return f.plan;
  return f.plan ? S.plan : ownPlan(S.raw);
}

// fetchAll reads the graph and the stored plans of one revision and, when the plan to show is a stored plan that
// the server did not lay itself, its overlay. want is a plan id, 'none', or undefined for the page's own choice.
async function fetchAll(want) {
  let raw, list, access;
  for (let i = 0; ; i++) {
    // the Access view does not stop the page: a document that cannot be read leaves the view saying so
    [raw, list, access] = await Promise.all([api.graph(), api.plans(), api.access().catch((err) => {
      if (err instanceof api.Unauthorized || err instanceof api.Unreachable) throw err;
      return null;
    })]);
    const same = (x) => !x || !x.revision || x.revision === raw.revision;
    if ((same(list) && same(access)) || i >= 2) break; // the project changed between the requests
  }
  const plans = list.plans || [];
  const own = ownPlan(raw);
  let plan = want === undefined ? own : want === 'none' ? null : want;
  let gone = null; // a plan that was asked for and is not stored: the page falls back to the plan the server laid, if any
  if (plan && !plans.some((p) => p.id === plan)) { gone = plan; plan = own; }
  // The overlay of a plan carries what the author wrote with it and, for a plan other than the server's, its actions. A
  // plan whose overlay cannot be read does not stop the page: it falls back to the plan of the server, and the page says so.
  let failed = null;
  const read = async (id) => {
    try { return await api.plan(id); } catch (err) {
      if (err instanceof api.Unauthorized || err instanceof api.Unreachable) throw err;
      if (err.status === 404) gone = gone || id; else failed = { id, message: err.message };
      return null;
    }
  };
  let overlay = plan ? await read(plan) : null;
  if (plan && !overlay && plan !== own) {
    plan = own;
    overlay = plan ? await read(plan) : null;
  }
  return { raw, plans, access, plan, overlay, gone, failed };
}

// the overlay of the plan that is laid over the graph, which has what the author wrote with it
function planText() { return S.overlay && S.plan && S.overlay.plan.id === S.plan ? S.overlay : null; }

// diffPlans says what changed in the list: plans that came, went, or stand elsewhere now
function diffPlans(before, after) {
  const was = new Map(before.map((p) => [p.id, p]));
  const now = new Map(after.map((p) => [p.id, p]));
  const added = after.filter((p) => !was.has(p.id)).map((p) => p.id);
  const removed = before.filter((p) => !now.has(p.id)).map((p) => p.id);
  const moved = after.filter((p) => was.has(p.id) && (was.get(p.id).status !== p.status || was.get(p.id).applied !== p.applied)).map((p) => `${p.id} ${statusLabel(p.status)}`);
  return { added, removed, moved };
}

// The notes of the plans, for the titles of the rows. A note is text that a person wrote, and the server masks it with
// the secrets of its own plan, so it comes with the overlay of the plan, which is asked for a few at a time and kept:
// a plan does not change.
const reasons = new Map();
let reasonsAsked = false;
async function loadReasons() {
  if (reasonsAsked) return;
  const want = S.plans.filter((p) => !reasons.has(p.id)).slice(0, 40).map((p) => p.id);
  if (!want.length) return;
  reasonsAsked = true;
  for (const id of want) reasons.set(id, '');
  let drawn = false;
  const draw = () => { if (drawn) return; drawn = true; requestAnimationFrame(() => { drawn = false; renderPlans(); }); };
  const worker = async () => {
    while (want.length) {
      const id = want.shift();
      try { const o = await api.plan(id); if (o && o.reason) { reasons.set(id, String(o.reason).replace(/\s+/g, ' ').trim()); draw(); } } catch (err) { /* the title stays the id */ }
    }
  };
  await Promise.all([worker(), worker(), worker()]);
  reasonsAsked = false;
  if (S.plans.some((p) => !reasons.has(p.id))) loadReasons();
}

function renderPlans() {
  const text = planText();
  if (text && text.reason) reasons.set(text.plan.id, String(text.reason).replace(/\s+/g, ' ').trim());
  plansList.render({ plans: S.plans, sel: S.plan, text, reasons });
  loadReasons();
  const p = S.plan && S.plans.find((x) => x.id === S.plan);
  planBar($('planbar'), p, {
    onClear: () => selectPlan(null),
    onDetail: () => {
      if (narrow() && !$('app').classList.contains('rail-open')) toggleRail();
      const el = document.querySelector('#rail-plans .rp[aria-pressed="true"]');
      if (el) el.focus();
    },
  });
}

// selectPlan lays a stored plan over the graph, or, with null, takes it off. The first view is made again for
// it: the modules the plan touches open first.
// planSeq counts the choices that were begun; planMark also counts the ones that were made, so that a read of the
// project that began before a choice does not put the old plan back.
let planSeq = 0;
let planMark = 0;
async function selectPlan(id) {
  id = id || null;
  if (!S.model) return;
  const seq = ++planSeq;
  planMark++;
  if (id === S.plan) { renderPlans(); return; }
  let overlay = null;
  if (id) {
    if (!S.plans.some((p) => p.id === id)) { toast(`${id} is not stored any more`, 'warn', 6000); return; }
    // the overlay has what the author wrote with the plan and, for a plan other than the server's, its actions
    try { overlay = await api.plan(id); } catch (err) {
      if (err instanceof api.Unauthorized || err instanceof api.Unreachable) return;
      if (id !== ownPlan(S.raw)) {
        toast(`Could not read ${id}: ${err.message}`, 'error', 6000);
        if (err.status === 404) onChange(null);
        return;
      }
    }
    if (seq !== planSeq) return; // another plan was picked while this one was read
  }
  planMark++;
  S.plan = id;
  S.overlay = overlay;
  applyPlan();
  const p = id && S.plans.find((x) => x.id === id);
  announce(p ? `${id} by ${p.author} is laid over the graph, ${statusLabel(p.status)}.` : 'No plan is laid over the graph.');
}

// applyPlan draws the graph again for the plan that is selected.
function applyPlan() {
  const g = shown(S.raw);
  S.graph = g;
  S.model = buildModel(g, S.plans);
  S.model.planParam = planParam();
  S.model.planText = planText();
  S.trail = [];
  updateBack();
  S.auto = autoCollapsed(S.model);
  S.collapsed = new Set(S.auto);
  S.clusters = new Map();
  if (S.sel && !(S.model.nodes.has(S.sel) || S.model.modules.has(S.sel))) { S.sel = null; S.reach = false; S.tab = null; }
  revealSel({ collapse: [], expand: [] }, S.sel);
  insp.invalidate();
  palette.setModel(S.model);
  relayout({ animate: true });
  renderRail();
  renderBanners();
  renderLegend();
  renderPlans();
  renderStatus();
  if (S.view === 'table') renderTable();
  showInspector();
  if (S.sel) rail.setSelected(S.model.nodes.has(S.sel) ? 'n:' + S.sel : 'm:' + S.sel, false);
  gv.fit(true);
  syncFragment();
}

// setModel makes the model of the graph that the page opens with, and the modules that its link asks to open or close.
function setModel(g, frag) {
  S.graph = g;
  S.model = buildModel(g, S.plans);
  S.model.planParam = planParam();
  S.model.planText = planText();
  S.clusters = new Map();
  S.trail = [];
  const mods = S.model.modules;
  S.auto = autoCollapsed(S.model);
  S.collapsed = new Set(S.auto);
  for (const x of frag.collapse) {
    if (x === 'all') { for (const m of mods.values()) if (!m.isRoot) S.collapsed.add(m.path); } else if (mods.has(x)) S.collapsed.add(x);
  }
  for (const x of frag.expand) {
    if (x === 'all') S.collapsed.clear();
    else if (x.startsWith('c:')) S.clusters.set(x, CLUSTER_CAP);
    else if (mods.has(x)) for (const m of chain(mods.get(x))) S.collapsed.delete(m.path);
  }
}

// relayout computes the items and the layout, and shows them.
function relayout(o = {}) {
  const t0 = performance.now();
  const vis = buildItems(S.model, S.collapsed, S.clusters);
  const sh = gv.shape;
  // a module that opens or closes keeps the lanes that it does not touch where they were (o.stable)
  const lay = layout({ root: vis.root, items: vis.items, edges: vis.edges, aspect: sh.fit.w > 0 ? sh.fit.w / sh.fit.h : 1.4, fit: sh.fit.w > 0 ? sh.fit : null, hint: o.stable && S.lay && S.shaped ? S.lay.arr : null });
  S.vis = vis;
  S.lay = lay;
  S.layoutMs = performance.now() - t0;
  S.shaped = sh.fit.w > 0; // false while the canvas is hidden (the table view): the layout is made again when it shows
  gv.setScene(vis, lay, { animate: o.animate !== false, intro: !!o.intro, hasPlan: S.model.hasPlan, authors: S.model.authors });
  applyMatch();
  applySelection(false);
  renderStatus();
  if (o.draw !== false) minimapDraw();
}

function minimapDraw() { schedule('minimap', () => minimap.draw()); }

function dist(model, start, dir) {
  const out = new Map();
  let frontier = [start];
  let d = 0;
  while (frontier.length) {
    d++;
    const next = [];
    for (const a of frontier) {
      const n = model.nodes.get(a);
      if (!n) continue;
      for (const b of dir === 'up' ? n.deps : n.used) if (b !== start && !out.has(b)) { out.set(b, d); next.push(b); }
    }
    frontier = next;
  }
  return out;
}

function applySelection(pulse) {
  if (!S.vis) return;
  const m = S.model, vis = S.vis;
  if (!S.sel) { gv.setSelection(null); renderReach(); return; }
  const out = { ids: new Set(), up: new Set(), down: new Set(), upE: new Map(), downE: new Map(), lane: null, pulse, reach: false, node: null };
  if (m.nodes.has(S.sel)) {
    const addr = S.sel;
    const id = vis.rep.get(addr);
    if (id) out.ids.add(id);
    out.node = id;
    const up = dist(m, addr, 'up'), down = dist(m, addr, 'down');
    for (const a of up.keys()) { const i = vis.rep.get(a); if (i && i !== id) out.up.add(i); }
    for (const a of down.keys()) { const i = vis.rep.get(a); if (i && i !== id) out.down.add(i); }
    const put = (map, key, d) => { const o = map.get(key); if (o === undefined || d < o) map.set(key, d); };
    for (const e of m.edges) {
      const ra = vis.rep.get(e.a), rb = vis.rep.get(e.b);
      if (!ra || !rb || ra === rb) continue;
      const key = ra + '>' + rb;
      if (up.has(e.a) && (e.b === addr || up.has(e.b))) put(out.upE, key, up.get(e.a));
      if (down.has(e.b) && (e.a === addr || down.has(e.a))) put(out.downE, key, down.get(e.b));
    }
    out.reach = S.reach;
  } else if (m.modules.has(S.sel)) {
    const item = 'm:' + S.sel;
    if (vis.items.has(item)) {
      out.ids.add(item);
      out.node = item;
      // the module as a card: what it depends on and what depends on it, one step out
      for (const e of vis.edges) {
        if (e.b === item) { out.up.add(e.a); out.upE.set(e.key, 1); } else if (e.a === item) { out.down.add(e.b); out.downE.set(e.key, 1); }
      }
    } else out.lane = S.sel;
  }
  gv.setSelection(out);
  renderReach();
}

function renderReach() {
  const el = $('reachbar');
  if (S.reach && S.sel && S.model.nodes.has(S.sel)) {
    const n = S.model.nodes.get(S.sel);
    const reached = [...dist(S.model, S.sel, 'down').keys()].map((a) => S.model.nodes.get(a)).filter(Boolean);
    const outs = reached.filter(isOut).length;
    clear(el);
    el.hidden = false;
    el.append(h('span', null, 'Reach of ', h('b', null, n.name), ' · ', plural(reached.length - outs, 'resource'), outs ? ` and ${plural(outs, 'output')}` : ''),
      h('button', { type: 'button', 'aria-label': 'Clear the reach', on: { click: () => setReach(false) } }, h('span', { class: 'i i-x', 'aria-hidden': 'true' })));
  } else el.hidden = true;
}

function setReach(on, fitIt) {
  S.reach = on;
  // the reach is drawn in the graph: from the table, go there first
  if (on && S.view !== 'graph') setView('graph');
  applySelection(on);
  if (on && fitIt && S.sel) {
    const ids = [S.vis.rep.get(S.sel), ...[...dist(S.model, S.sel, 'down').keys()].map((a) => S.vis.rep.get(a))].filter(Boolean);
    gv.fit(true, ids, 1);
  }
  announce(on ? 'Showing the reach of the selection' : 'Reach cleared');
}

function matchSet() {
  if (!S.filter && !S.q) return null;
  const f = S.filter ? FILTERS[S.filter] : null;
  const q = S.q.trim().toLowerCase();
  const set = new Set();
  let n = 0;
  for (const node of S.model.list) {
    if ((!f || f(node)) && (!q || node.addr.toLowerCase().includes(q) || node.type.toLowerCase().includes(q))) {
      n++;
      const id = S.vis.rep.get(node.addr);
      if (id) set.add(id);
    }
  }
  S.matches = n;
  return set;
}

function applyMatch() {
  if (!S.vis) return;
  const set = matchSet();
  gv.setMatch(set);
  const c = $('match-count');
  c.textContent = set || S.q || S.filter ? `${S.matches} of ${S.model.list.length}` : '';
}

// ---------------------------------------------------------------------------------------------------------
// selection

function itemOf(addr) { return S.vis && S.vis.rep.get(addr); }

function selectNode(addr, o = {}) {
  const n = S.model.nodes.get(addr);
  if (!n) return;
  const changed = reveal(S.model, addr, S.collapsed, S.clusters);
  if (S.sel !== addr) S.reach = false;
  S.sel = addr;
  if (!STICKY_TABS.has(S.tab) && !o.keepTab) S.tab = null;
  if (changed) { relayout({ animate: o.animate !== false }); renderRail(); } else applySelection(o.pulse !== false);
  if (changed) applySelection(o.pulse !== false);
  showInspector();
  rail.ensureVisible(addr);
  if (!rail.setSelected('n:' + addr)) { renderRail(); rail.setSelected('n:' + addr); }
  table.setSelected(addr);
  openDrawer(); // before the camera moves: on a narrow screen the panel takes room from the graph
  const id = itemOf(addr);
  S.focus = id;
  gv.setFocus(o.kbd ? id : null);
  if (S.view === 'graph' && id && (o.center || !gv.visible(id, 60))) gv.centerOn(id);
  syncFragment();
  announce(isOut(n) ? `${n.name}, output${n.sensitive ? ', sensitive' : ''}. Reads ${n.deps.length}, read by ${n.used.length}.`
    : `${n.name}, ${n.type}${n.action ? ', ' + n.action : ''}. ${plural(n.deps.length, 'dependency', 'dependencies')}, ${plural(n.used.length, 'dependent')}.`);
}

function selectModule(path, o = {}) {
  const mod = S.model.modules.get(path);
  if (!mod) return;
  S.sel = path;
  S.reach = false;
  S.tab = null;
  applySelection(false);
  showInspector();
  const ok = rail.setSelected('m:' + path);
  if (!ok) { renderRail(); rail.setSelected('m:' + path); }
  table.setSelected(null);
  openDrawer();
  const id = S.vis.items.has('m:' + path) ? 'm:' + path : null;
  S.focus = id;
  gv.setFocus(o.kbd ? id : null);
  if (S.view === 'graph' && o.center) {
    const lane = S.lay.lanes.find((l) => l.path === path);
    if (lane) gv.centerOnBox(lane); else if (id) gv.centerOn(id);
  }
  syncFragment();
  announce(`Module ${mod.name}, ${plural(mod.agg.n, 'resource')}${mod.agg.out ? ' and ' + plural(mod.agg.out, 'output') : ''}.`);
}

function clearSelection() {
  if (!S.sel && !S.reach) return;
  S.sel = null;
  S.reach = false;
  S.tab = null;
  applySelection(false);
  showInspector();
  rail.setSelected(null);
  table.setSelected(null);
  gv.setFocus(null);
  syncFragment();
  closeDrawer();
  announce('Selection cleared');
}

function showInspector() {
  const m = S.model;
  if (!S.sel) { insp.showProject(m); return; }
  if (m.nodes.has(S.sel)) {
    const n = m.nodes.get(S.sel);
    insp.showNode(n, m, effectiveTab(n), S.graph);
  } else if (m.modules.has(S.sel)) insp.showModule(m.modules.get(S.sel), m, S.collapsed);
  else insp.showProject(m);
}

function effectiveTab(n) {
  // an output has a value and a declaration only: the other tabs are not there for it
  if (isOut(n)) return S.tab === 'source' ? 'source' : 'state';
  if (S.tab && TABKEYS.includes(S.tab) && (S.tab !== 'change' || S.model.hasPlan)) return S.tab;
  return S.model.hasPlan && isChange(n.action) ? 'change' : 'state';
}

function setTab(t) {
  S.tab = t;
  if (S.sel && S.model.nodes.has(S.sel)) { insp.setTab(effectiveTab(S.model.nodes.get(S.sel))); syncFragment(); }
}

// ---------------------------------------------------------------------------------------------------------
// modules and clusters

// openModule opens a closed module the way a person drilling into the picture means it: the module opens,
// is selected, and the camera glides to frame it. What was on screen is kept on a trail, so that Escape (or
// the zoom-out control) can close the module again and return to the same picture. A module that is open
// is selected and framed.
function openModule(path) {
  const mod = S.model.modules.get(path);
  if (!mod) return;
  if (S.view !== 'graph') setView('graph');
  if (!S.collapsed.has(path)) { selectModule(path, { center: true }); return; }
  gv.guardUntil = performance.now() + 450;
  S.trail.push({ path, view: Object.assign({}, gv.view), moved: gv.userMoved });
  for (const m of chain(mod)) S.collapsed.delete(m.path);
  relayout({ stable: true });
  renderRail();
  selectModule(path, { center: true });
  updateBack();
  announce(`Opened module ${mod.name}, ${plural(mod.agg.n, 'resource')}. Escape zooms out.`);
}

// backOut undoes the last openModule: the module closes and the camera returns to where it was.
function backOut() {
  let t = S.trail.pop();
  while (t && S.collapsed.has(t.path)) t = S.trail.pop();
  updateBack();
  if (!t) return false;
  S.collapsed.add(t.path);
  // anything that the person opened inside it closes with it
  S.trail = S.trail.filter((x) => !S.collapsed.has(x.path));
  S.sel = t.path;
  S.reach = false;
  S.tab = null;
  relayout({ stable: true });
  renderRail();
  selectModule(t.path, { center: false });
  gv.setView(t.view, true);
  gv.userMoved = t.moved;
  updateBack();
  announce(`Closed module ${S.model.modules.get(t.path).name}.`);
  return true;
}

function updateBack() {
  if (!backBtn) return;
  const t = S.trail[S.trail.length - 1];
  backBtn.hidden = !t || S.view !== 'graph';
  if (t) backBtn.querySelector('.bl').textContent = 'Zoom out';
}

function toggleModule(path) {
  const mod = S.model.modules.get(path);
  if (!mod) return;
  const closing = !S.collapsed.has(path);
  // the module that was opened last by drilling in closes as Escape would close it
  if (closing && S.trail.length && S.trail[S.trail.length - 1].path === path && backOut()) return;
  if (closing) S.collapsed.add(path);
  else for (const m of chain(mod)) S.collapsed.delete(m.path);
  // a selection inside a module that closes moves to the module
  if (closing && S.sel && S.sel !== path) {
    const at = S.model.nodes.has(S.sel) ? S.model.nodes.get(S.sel).mod : S.model.modules.get(S.sel);
    for (let m = at; m; m = m.up) if (m.path === path) { S.sel = path; S.reach = false; S.tab = null; showInspector(); break; }
  }
  relayout({ stable: true });
  renderRail();
  if (S.sel) rail.setSelected(S.model.nodes.has(S.sel) ? 'n:' + S.sel : 'm:' + S.sel, false);
  if (!closing) {
    const lane = S.lay.lanes.find((l) => l.path === path);
    if (lane && !boxVisible(lane)) gv.centerOnBox(lane);
  }
  if (closing) S.trail = S.trail.filter((x) => !S.collapsed.has(x.path));
  updateBack();
  syncFragment();
  announce(`${closing ? 'Closed' : 'Opened'} module ${mod.name}, ${plural(mod.agg.n, 'resource')}.`);
}

function boxVisible(b) {
  const { x, y, k } = gv.view;
  const sx = b.x * k + x, sy = b.y * k + y;
  return sx > 0 && sy > 0 && sx + b.w * k < gv.size.w && sy + b.h * k < gv.size.h;
}

function setAllModules(collapse) {
  S.trail = [];
  updateBack();
  if (collapse) { for (const m of S.model.modules.values()) if (!m.isRoot) S.collapsed.add(m.path); } else S.collapsed.clear();
  if (S.sel && collapse) {
    // a selection inside a module moves to the module at the top that now holds it
    const at = S.model.nodes.has(S.sel) ? S.model.nodes.get(S.sel).mod : S.model.modules.get(S.sel);
    const top = at && !at.isRoot ? chain(at)[0] : null;
    if (top) { S.sel = top.path; S.reach = false; S.tab = null; showInspector(); }
  }
  relayout();
  renderRail();
  gv.fit(true);
  syncFragment();
  announce(collapse ? 'Every module is closed' : 'Every module is open');
}

// resetView goes back to the first view: the modules that were open when the page loaded, no group opened, the
// whole picture in frame. The selection stays when it still shows as itself; else it moves to the module that
// now holds it. The filter and the search stay.
function resetView() {
  if (S.view !== 'graph') return;
  S.trail = [];
  S.collapsed = new Set(S.auto);
  S.clusters = new Map();
  S.reach = false;
  if (S.sel) {
    const at = S.model.nodes.has(S.sel) ? S.model.nodes.get(S.sel).mod : S.model.modules.get(S.sel);
    const top = at ? chain(at).find((m) => S.collapsed.has(m.path)) : null;
    if (top) { S.sel = top.path; S.tab = null; }
  }
  updateBack();
  relayout();
  renderRail();
  if (S.sel) rail.setSelected(S.model.nodes.has(S.sel) ? 'n:' + S.sel : 'm:' + S.sel, false);
  showInspector();
  gv.fit(true);
  syncFragment();
  announce('Back to the first view');
}

function clusterAction(cid, act) {
  if (act === 'open') S.clusters.set(cid, CLUSTER_CAP);
  else if (act === 'all') S.clusters.set(cid, Infinity);
  else S.clusters.delete(cid);
  relayout({ stable: true });
  syncFragment();
  announce(act === 'collapse' ? 'Group closed' : 'Group opened');
}

// ---------------------------------------------------------------------------------------------------------
// filters, search, views

function setFilter(f) {
  S.filter = f || null;
  renderRail();
  applyMatch();
  if (S.view === 'table') renderTable();
  syncFragment();
  announce(f ? `Filter: ${f}` : 'No filter');
}

function setQuery(q) {
  S.q = q;
  renderRail();
  applyMatch();
  if (S.view === 'table') renderTable();
  syncFragment();
}

// TITLES is the h1 of the page for each view, which a screen reader gives as the name of the page.
const TITLES = {
  graph: 'pravo serve: the specification, the state and the plan as a graph',
  table: 'pravo serve: the resources of the state as a table',
  access: 'pravo serve: where the state is kept, how it is protected and who can read it',
};

function setView(v) {
  S.view = v;
  $('page-title').textContent = TITLES[v] || TITLES.graph;
  $('graph-pane').hidden = v !== 'graph';
  $('table-pane').hidden = v !== 'table';
  $('access-pane').hidden = v !== 'access';
  if (v !== 'access') S.lastResources = v;
  $('view-graph').setAttribute('aria-pressed', v === 'graph' ? 'true' : 'false');
  $('view-table').setAttribute('aria-pressed', v === 'table' ? 'true' : 'false');
  $('sec-resources').setAttribute('aria-pressed', v !== 'access' ? 'true' : 'false');
  $('sec-access').setAttribute('aria-pressed', v === 'access' ? 'true' : 'false');
  $('app').dataset.view = v;
  const skip = document.querySelector('.skiplink');
  if (skip) skip.textContent = v === 'graph' ? 'Skip to the graph' : v === 'table' ? 'Skip to the table' : 'Skip to the state';
  if (v === 'table') renderTable();
  else if (v === 'access') renderAccess();
  else { gv.resize(); if (S.vis && !S.shaped && gv.size.w > 0) relayout({ animate: false }); if (!gv.userMoved && gv.lay) gv.fit(false); minimapDraw(); }
  syncFragment();
  announce(v === 'table' ? 'Resources, table view' : v === 'access' ? 'State' : 'Resources, graph view');
}

function renderAccess() {
  accessView.render({ access: S.access, project: S.model && S.model.graph ? S.model.graph.project : null });
}

function renderTable() {
  const n = table.render({ model: S.model, filter: S.filter, q: S.q, sel: S.sel && S.model.nodes.has(S.sel) ? S.sel : null });
  const c = $('match-count');
  c.textContent = S.q || S.filter ? `${n} of ${S.model.list.length}` : `${n}`;
}

function renderRail() {
  rail.render({ model: S.model, collapsed: S.collapsed, filter: S.filter, q: S.q, sel: S.sel });
}

// ---------------------------------------------------------------------------------------------------------
// banners and the status bar

const dismissed = new Set();

function banner(kind, role, kids, key) {
  const x = key ? h('button', { class: 'bx', type: 'button', 'aria-label': 'Dismiss', on: { click: (ev) => { dismissed.add(key); ev.currentTarget.closest('.banner').remove(); } } }, icon('x')) : null;
  return h('div', { class: 'banner ' + kind, role }, h('span', { class: 'bi' }, icon('bang')), h('div', { class: 'bmsg' }, kids), x);
}

function renderBanners() {
  const box = $('banners');
  clear(box);
  const g = S.graph, p = g.project;
  if (lostSince) box.append(banner('bad', 'alert', [h('b', null, 'The viewer is not answering.'), ' pravo serve may have stopped. What is shown is the last thing it sent; the page keeps trying and reads the project again when the server is back.']));
  if (p.spec_error) {
    box.append(banner('bad', 'alert', [h('b', null, 'The specification does not load.'), ' Showing the state alone.', h('pre', null, p.spec_error)]));
  }
  if (g.plan && g.plan.stale) {
    box.append(banner('chg', 'status', [h('b', null, g.plan.id ? `The stored plan ${g.plan.id} is stale.` : 'The saved plan is stale.'), ' It can no longer be applied.',
      (g.plan.reasons || []).length ? h('ul', null, g.plan.reasons.map((r) => h('li', null, r))) : null]));
  }
  const warn = (g.warnings || []).filter((w) => !(p.spec_error && /does not load/i.test(w)) && !dismissed.has(w));
  if (warn.length) {
    const open = warn.length === 1;
    const body = open ? h('p', { class: 'wone' }, warn[0]) : h('ul', { class: 'wlist', hidden: true }, warn.map((w) => h('li', null, w)));
    const btn = h('button', { type: 'button', class: 'wtoggle', 'aria-expanded': open ? 'true' : 'false', on: { click: () => {
      const on = body.hidden; body.hidden = !on; btn.setAttribute('aria-expanded', on ? 'true' : 'false');
    } } }, plural(warn.length, 'warning'));
    box.append(banner('warn', 'status', [warn.length > 1 ? btn : null, body], warn.join('\n')));
  }
}

// the crumbs: a short project name with the folders before it dimmed, and the state beside it. The head of a
// path gives way first, so the name stays; the whole path is in the tooltip and a click copies it. The whole
// path is also in the page as one text node, and the visible pieces are drawn from it, again when it changes.
function fillCrumb(el, full, label, relativeTo) {
  let src = el.querySelector('.cp-src');
  if (!src) {
    src = h('span', { class: 'sr-only cp-src' });
    el.prepend(src);
    new MutationObserver(() => drawCrumb(el, relativeTo)).observe(src, { characterData: true, childList: true, subtree: true });
  }
  src.textContent = full;
  el.dataset.label = label;
  el.dataset.full = full;
  drawCrumb(el, relativeTo);
  el.onclick = () => copyText(el.dataset.full).then((ok) => { el.classList.add('done'); setTimeout(() => el.classList.remove('done'), 1200); announce(ok ? `Copied the ${label}` : 'Could not copy'); });
}

function drawCrumb(el, relativeTo) {
  const text = el.querySelector('.cp-src').textContent;
  let shown = tilde(text).replace(/\/+$/, '');
  if (relativeTo) {
    const base = (typeof relativeTo === 'function' ? relativeTo() : relativeTo).replace(/\/+$/, '');
    if (base && text.startsWith(base + '/')) shown = text.slice(base.length + 1);
  }
  const [head, mid, name] = pathParts(shown);
  el.querySelector('.cp-head').textContent = head;
  const tail = el.querySelector('.cp-tail');
  clear(tail);
  if (mid) tail.append(h('span', { class: 'mid' }, mid));
  tail.append(name);
  el.dataset.tip = `${text}\nClick to copy ${el.dataset.label}`;
  el.setAttribute('aria-label', `${el.dataset.label}: ${text}. Copy.`);
}

function renderHeader() {
  const p = S.graph.project;
  $('crumbs').hidden = false;
  fillCrumb($('crumb-dir'), p.dir, 'the project directory');
  const st = p.state || {};
  const cs = $('crumb-state');
  const sep = document.querySelector('.crumbs .sep');
  cs.hidden = !st.location;
  if (sep) sep.hidden = !st.location;
  if (st.location) {
    fillCrumb(cs, st.location, 'the state location', () => $('crumb-dir').querySelector('.cp-src').textContent);
    cs.querySelector('.cp-be').textContent = st.backend || '';
    cs.querySelector('.cp-be').hidden = !st.backend;
  }
  renderWho();
}

// renderWho names the identity that the page is read as. An unsealed state has none to name.
const WHO = { reader: 'reader', 'not-reader': 'not a reader', 'no-identity': 'no identity', locked: 'identity locked' };
function renderWho() {
  const el = $('who'), y = S.access && S.access.you;
  el.hidden = !y;
  if (!y) return;
  el.className = 'who w-' + y.state;
  const role = WHO[y.state] || WHO['no-identity'];
  el.replaceChildren(icon(y.state === 'reader' ? 'check' : 'lock'), y.name ? h('b', null, y.name) : null, h('span', { class: 'role' }, role));
  el.setAttribute('aria-label', (y.name ? y.name + ': ' : '') + role);
  el.dataset.tip = y.text;
}

function renderStatus() {
  const g = S.graph, m = S.model;
  if (!g) return;
  const plan = g.plan;
  const set = (id, kids) => { const el = $(id); clear(el); el.hidden = !kids; if (kids) el.append(kids); };
  set('st-plan', plan ? h('span', null, plan.id ? [h('b', null, plan.id), ' · '] : null, 'planned ', h('b', null, `${plan.planned} of ${plan.total}`)) : h('span', { class: 'quiet' }, 'no plan loaded'));
  set('st-notread', plan ? h('span', null, h('b', null, String(plan.total - plan.planned)), ' not read') : null);
  set('st-rev', h('span', null, 'revision ', h('b', null, (g.revision || '').replace(/^r-/, ''))));
  set('st-time', h('span', { title: g.project.loaded_at }, 'updated ', h('b', null, clock(g.project.loaded_at)), ' ', h('span', { class: 'quiet' }, relTime(g.project.loaded_at))));
  const shown = S.vis ? S.vis.items.size : 0;
  const sc = $('shown-count');
  sc.textContent = shown && shown !== m.list.length ? `${shown} items shown` : '';
  sc.title = shown !== m.list.length ? `${plural(m.nres, 'resource')}${m.nout ? ' and ' + plural(m.nout, 'output') : ''}, grouped into ${shown} items` : '';
}

function renderLive() {
  const el = $('st-live');
  el.dataset.state = S.live;
  el.querySelector('span').textContent = S.live === 'live' ? 'live' : S.live === 'reconnecting' ? 'reconnecting' : S.live === 'offline' ? 'offline' : 'connecting';
}

function renderLegend() {
  const el = $('legend');
  clear(el);
  const m = S.model;
  if (!m.list.length) return;
  const sq = (a) => h('i', { class: 'sq ' + actionOf(a).cls });
  const item = (...k) => h('span', { class: 'lg' }, ...k);
  const sel = (...k) => h('span', { class: 'lg lg-sel' }, ...k); // the keys of the edges show once something is selected
  if (m.hasPlan) {
    el.append(item(sq('create'), 'create'), item(sq('update'), 'update'), item(sq('replace'), 'replace'), item(sq('delete'), 'delete'), item(h('i', { class: 'sq skip' }), 'not read'));
  }
  el.append(
    sel(h('i', { class: 'ln up' }), 'depends on'), sel(h('i', { class: 'ln down' }), 'used by'),
    ...(m.nout ? [item(h('span', { class: 'i i-output key' }), 'output')] : []),
    item(h('span', { class: 'i i-lock key' }), 'sensitive'),
    item(h('span', { class: 'i i-drift warn' }), 'drifted'));
  // the badges of the authors of open plans, and the ring of a resource that two of them change
  if (m.list.some((n) => (n.plans || []).length)) el.append(item(avatar('author', 'tiny'), 'plan author'));
  if (m.list.some((n) => n.overlap)) el.append(item(h('i', { class: 'ringkey' }), 'overlap'));
}

// ---------------------------------------------------------------------------------------------------------
// drawers (narrow screens)

// Below 1100px (68.75em) the rail is a drawer over the graph, and the inspector is a panel under it: the graph keeps the
// top of the screen and shrinks, it is never covered. The size button lets the panel take the whole stage.
// (in em, like the media queries of the style sheets: a larger text size gives the narrow layout earlier)
const narrow = () => matchMedia('(max-width: 68.75em)').matches;

function openDrawer() {
  if (!narrow()) return;
  const app = $('app');
  app.classList.add('insp-open');
  app.classList.remove('rail-open');
  $('insp-btn').setAttribute('aria-expanded', 'true');
  $('menu-btn').setAttribute('aria-expanded', 'false');
  $('scrim').hidden = true;
  if (gv) gv.resize();
}
function closeDrawer() {
  const app = $('app');
  const was = app.classList.contains('insp-open');
  app.classList.remove('insp-open', 'rail-open');
  $('insp-btn').setAttribute('aria-expanded', 'false');
  $('menu-btn').setAttribute('aria-expanded', 'false');
  $('scrim').hidden = true;
  if (was && gv) gv.resize();
}
function toggleRail() {
  const on = !$('app').classList.contains('rail-open');
  $('app').classList.toggle('rail-open', on);
  $('app').classList.remove('insp-open');
  $('menu-btn').setAttribute('aria-expanded', on ? 'true' : 'false');
  $('insp-btn').setAttribute('aria-expanded', 'false');
  $('scrim').hidden = !on;
  if (gv) gv.resize();
}
function toggleInspector() {
  const on = !$('app').classList.contains('insp-open');
  $('app').classList.toggle('insp-open', on);
  $('app').classList.remove('rail-open');
  $('insp-btn').setAttribute('aria-expanded', on ? 'true' : 'false');
  $('scrim').hidden = true;
  if (gv) gv.resize();
}
function toggleInspSize() {
  const app = $('app');
  const full = app.dataset.size !== 'full';
  if (full) app.dataset.size = 'full'; else delete app.dataset.size;
  const b = $('insp-size');
  b.setAttribute('aria-pressed', full ? 'true' : 'false');
  b.setAttribute('aria-label', full ? 'Show the graph again' : 'Make the inspector taller');
  b.querySelector('.i').className = 'i ' + (full ? 'i-shrink' : 'i-expand');
  if (gv) gv.resize();
}

// ---------------------------------------------------------------------------------------------------------
// live updates

const WHAT = { state: 'the state', spec: 'the specification', plan: 'the plan', locks: 'a lock', plans: 'the stored plans', access: 'the readers file' };
let pendingWhat = new Set();

async function refresh() {
  let r;
  const what = [...pendingWhat];
  pendingWhat = new Set();
  const mark = planMark, asked = S.plan;
  try { r = await fetchAll(asked === null ? 'none' : asked); } catch (err) {
    if (err instanceof api.Unauthorized) return; // the gate is up
    if (err instanceof api.Unreachable) return; // the connection says so; the page asks again when it is back
    toast(`Could not read the project again: ${err.message}`, 'error');
    return;
  }
  if (r.raw.revision === S.raw.revision) return;
  if (mark !== planMark) {
    // the person chose a plan while the project was read: that choice stands, and what was read for the old one is dropped
    r.plan = S.plan;
    r.overlay = S.overlay;
    r.gone = r.failed = null;
  }
  const old = S.model;
  const dp = diffPlans(S.plans, r.plans);
  const droppedPlan = asked && r.plan !== asked && mark === planMark ? asked : null; // the plan that was laid over the graph is not stored any more
  S.raw = r.raw;
  S.plans = r.plans;
  S.access = r.access;
  S.plan = r.plan;
  S.overlay = r.overlay;
  const g = shown(r.raw);
  const nm = buildModel(g, S.plans);
  nm.planParam = planParam();
  nm.planText = planText();
  const d = diffModels(old, nm);
  // what the person was looking at may be gone: the page says so, and shows the project
  const lost = S.sel && !(nm.nodes.has(S.sel) || nm.modules.has(S.sel)) ? S.sel : null;
  if (lost) { S.sel = null; S.reach = false; S.tab = null; }
  S.graph = g;
  S.model = nm;
  for (const p of [...S.collapsed]) if (!nm.modules.has(p)) S.collapsed.delete(p);
  S.auto = autoCollapsed(nm); // the fragment is relative to it: a reload works it out from the new graph
  S.trail = S.trail.filter((x) => nm.modules.has(x.path));
  updateBack();
  insp.invalidate();
  palette.setModel(nm);
  relayout({ animate: true });
  for (const cid of [...S.clusters.keys()]) if (!S.vis.items.has('x:' + cid)) S.clusters.delete(cid); // a group that no longer exists
  renderRail();
  renderBanners();
  renderHeader();
  renderLegend();
  renderPlans();
  if (S.view === 'table') renderTable();
  if (S.view === 'access') renderAccess();
  const flash = [...d.changed, ...d.added].map((a) => S.vis.rep.get(a)).filter(Boolean);
  gv.flash(new Set(flash));
  showInspector();
  if (lost) { rail.setSelected(null); table.setSelected(null); gv.setFocus(null); }
  const bits = [];
  if (d.changed.length) bits.push(`${d.changed.length} changed`);
  if (d.added.length) bits.push(`${d.added.length} new`);
  if (d.removed.length) bits.push(`${d.removed.length} gone`);
  if (dp.added.length) bits.push(dp.added.length === 1 ? `${dp.added[0]} stored` : `${dp.added.length} plans stored`);
  if (dp.removed.length) bits.push(dp.removed.length === 1 ? `${dp.removed[0]} collected` : `${dp.removed.length} plans collected`);
  for (const m of dp.moved.slice(0, 2)) bits.push(m);
  const names = what.filter((w) => WHAT[w]).map((w) => WHAT[w]);
  const title = names.length ? `${names.length > 1 ? names.slice(0, -1).join(', ') + ' and ' + names[names.length - 1] : names[0]} changed` : 'The project changed';
  toast(title.charAt(0).toUpperCase() + title.slice(1), 'info', 5200, bits.length ? bits.join(' · ') : 'no resource differs');
  if (lost) toast(`${lost} is no longer there`, 'warn', 7000, 'The inspector shows the project again.');
  if (droppedPlan) toast(`${droppedPlan} is not stored any more`, 'warn', 7000, 'It was collected. The graph shows no plan now.');
  if (r.failed) toast(`Could not read ${r.failed.id}: ${r.failed.message}`, 'warn', 7000, 'The page shows the plan of the server instead.');
  beat();
  syncFragment();
}

let refreshTimer = 0;
function onChange(ev) {
  for (const w of (ev && ev.what) || []) pendingWhat.add(w);
  // an event that carries the revision the page already holds changes nothing
  if (ev && ev.revision && S.graph && ev.revision === S.graph.revision) return;
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(refresh, 120);
}

// A connection that stays down is said so: "reconnecting" is for a moment, after that the page tells the
// person what has happened. The browser keeps trying by itself, and the page reads the project again when it works.
const OFFLINE_AFTER = 12000;
let offlineTimer = 0;
let retryT = 0;
let lostSince = 0;

async function onLiveState(state) {
  if (state === 'reconnecting') {
    if (lostSince) {
      // already said offline: the word stays, and the browser's quick retries (each one a line in the console) give way to one try in a while
      if (conn) conn.close();
      clearTimeout(retryT);
      retryT = setTimeout(() => { if (conn && lostSince) conn.reopen(); }, 15000);
      return;
    }
    S.live = state;
    if (!offlineTimer) offlineTimer = setTimeout(() => { offlineTimer = 0; if (S.live === 'reconnecting') { lostSince = Date.now(); S.live = 'offline'; renderLive(); if (S.graph) renderBanners(); } }, OFFLINE_AFTER);
    renderLive();
    return;
  }
  clearTimeout(offlineTimer);
  clearTimeout(retryT);
  offlineTimer = 0;
  const wasLost = lostSince;
  lostSince = 0;
  S.live = state;
  renderLive();
  if (wasLost && S.graph) renderBanners();
  if (state === 'offline') {
    try { await api.ping(); } catch (err) { if (err instanceof api.Unauthorized) showGate(); }
  } else if (state === 'live' && booted) onChange(null);
}

function showGate() {
  $('skeleton').hidden = true;
  if (conn) conn.close();
  clearTimeout(offlineTimer);
  clearTimeout(retryT);
  const g = $('gate');
  if (booted) {
    // the page was working: the server was started again, which makes a new token, or the cookie went
    const lead = g.querySelector('.lead');
    if (lead) lead.textContent = 'This page was reading the project and is not allowed to any more. The server was probably started again: it makes a new token each time.';
  }
  g.hidden = false;
  const b = $('gate-retry');
  if (b && !b.dataset.bound) { b.dataset.bound = '1'; b.addEventListener('click', () => location.reload()); }
}

// ---------------------------------------------------------------------------------------------------------
// keyboard

function isTyping(t) {
  return t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable);
}

function currentItem() {
  if (S.sel && S.model.nodes.has(S.sel)) return itemOf(S.sel);
  if (S.sel && S.vis.items.has('m:' + S.sel)) return 'm:' + S.sel;
  return S.focus && S.vis.items.has(S.focus) ? S.focus : null;
}

function goItem(id) {
  const it = S.vis.items.get(id);
  if (!it) return;
  if (it.kind === 'node') selectNode(it.n.addr, { center: !gv.visible(id, 80), pulse: true, kbd: true });
  else if (it.kind === 'module') { selectModule(it.mod.path, { center: false, kbd: true }); if (!gv.visible(id, 80)) gv.centerOn(id); } else { S.focus = id; gv.setFocus(id); if (!gv.visible(id, 80)) gv.centerOn(id); }
}

function navigate(dir) {
  const vis = S.vis, lay = S.lay;
  if (!vis || !vis.items.size) return;
  const cur = currentItem();
  if (!cur) {
    const first = [...lay.pos.entries()].sort((a, b) => a[1].rank - b[1].rank || a[1].y - b[1].y)[0];
    goItem(first[0]);
    return;
  }
  const p = lay.pos.get(cur);
  const cy = (id) => lay.pos.get(id).y + lay.pos.get(id).h / 2;
  let best = null;
  if (dir === 'left' || dir === 'right') {
    const list = vis.edges.filter((e) => (dir === 'left' ? e.b === cur : e.a === cur)).map((e) => (dir === 'left' ? e.a : e.b));
    for (const id of list) if (!best || Math.abs(cy(id) - cy(cur)) < Math.abs(cy(best) - cy(cur))) best = id;
  } else {
    const it = vis.items.get(cur);
    const sib = [...vis.items.values()].filter((o) => o.lane === it.lane && lay.pos.get(o.id).rank === p.rank).sort((a, b) => lay.pos.get(a.id).y - lay.pos.get(b.id).y);
    const i = sib.findIndex((o) => o.id === cur);
    const n = sib[i + (dir === 'up' ? -1 : 1)];
    if (n) best = n.id;
  }
  if (best) goItem(best);
}

function onKey(ev) {
  if (ev.defaultPrevented) return;
  const k = ev.key;
  const mod = ev.metaKey || ev.ctrlKey;
  if (palette.isOpen || sheet.isOpen) return;
  if (mod && (k === 'k' || k === 'K')) { ev.preventDefault(); palette.open(); return; }
  if (ev.altKey || mod) return;
  const typing = isTyping(ev.target);
  if (k === 'Escape') {
    if ($('app').classList.contains('insp-open') || $('app').classList.contains('rail-open')) { closeDrawer(); return; }
    if (typing) { ev.target.blur(); return; }
    if (S.reach) { setReach(false); return; }
    // Escape zooms out of a module that was opened by drilling in, once nothing else is selected
    const top = S.trail[S.trail.length - 1];
    if (top && (!S.sel || S.sel === top.path) && backOut()) return;
    clearSelection();
    return;
  }
  if (typing) return;
  if (k === '/') { ev.preventDefault(); palette.open(); return; }
  if (k === '?') { ev.preventDefault(); sheet.open(); return; }
  if (k === 't') { toggleTheme(); return; }
  if (k === 'a') { setView(S.view === 'access' ? (S.lastResources || 'graph') : 'access'); return; }
  if (S.view === 'access') return; // the keys below are for the resources
  if (k === 'g') { setView(S.view === 'graph' ? 'table' : 'graph'); return; }
  if (S.view !== 'graph') return;
  const onCanvas = ev.target === document.body || ev.target === $('canvas') || ev.target.closest('#canvas');
  if (k === 'f') { gv.fit(true); return; }
  if (k === '0') { resetView(); return; }
  if (k === '[') { setAllModules(true); return; }
  if (k === ']') { setAllModules(false); return; }
  if (k === '+' || k === '=') { gv.zoomBy(1.25, undefined, undefined, true); return; }
  if (k === '-' || k === '_') { gv.zoomBy(0.8, undefined, undefined, true); return; }
  if (!onCanvas) return;
  if (k === 'ArrowLeft') { ev.preventDefault(); navigate('left'); } else if (k === 'ArrowRight') { ev.preventDefault(); navigate('right'); } else if (k === 'ArrowUp') { ev.preventDefault(); navigate('up'); } else if (k === 'ArrowDown') { ev.preventDefault(); navigate('down'); } else if (k === 'Enter') {
    const id = currentItem();
    const it = id && S.vis.items.get(id);
    if (it && it.kind === 'module') { ev.preventDefault(); openModule(it.mod.path); } else if (it && it.kind === 'cluster') { ev.preventDefault(); clusterAction(it.id, 'open'); } else if (S.sel && S.model.modules.has(S.sel)) { ev.preventDefault(); toggleModule(S.sel); } else if (S.sel && S.model.nodes.has(S.sel)) {
      // Enter on a resource: the module it sits in, when that has a lane header, is the one to close
      const m = S.model.nodes.get(S.sel).mod;
      if (m && !m.isRoot) { ev.preventDefault(); toggleModule(m.path); }
    }
  }
}

// ---------------------------------------------------------------------------------------------------------
// boot

function wire() {
  gv = new GraphView($('canvas'), {
    moduleOf: (p) => S.model && S.model.modules.get(p),
    onSelectNode: (a) => selectNode(a),
    onSelectModule: (p) => selectModule(p),
    onToggleModule: toggleModule,
    onOpenModule: openModule,
    onCluster: clusterAction,
    onClear: clearSelection,
    onView: () => minimapDraw(),
    onScene: () => minimapDraw(),
    onSettled: () => minimapDraw(),
  });
  gv.motion = S.motion; // motion=off in the fragment was read before the graph existed
  minimap = new Minimap($('canvas'), gv);
  backBtn = h('button', { type: 'button', class: 'overlay backbtn', hidden: true, 'aria-label': 'Zoom out of the module that was opened (Escape)', on: { click: () => backOut() } },
    svgEl('svg', { viewBox: '0 0 16 16', 'aria-hidden': 'true', focusable: 'false' }, svgEl('circle', { cx: 7, cy: 7, r: 4.6 }), svgEl('path', { d: 'M5 7h4M10.6 10.6L14 14' })), h('span', { class: 'bl' }, 'Zoom out'), h('kbd', null, 'esc'));
  $('canvas').append(backBtn);
  rail = new Rail($('rail'), {
    onFilter: setFilter,
    onToggle: toggleModule,
    onSelectNode: (a) => selectNode(a, { center: true }),
    onSelectModule: (p) => selectModule(p, { center: true }),
    onRoot: () => { if (S.sel) clearSelection(); else if (narrow()) openDrawer(); },
  });
  plansList = new Plans($('rail-plans'), { onSelect: (id) => selectPlan(id) });
  table = new TableView($('table-pane'), { onSelectNode: (a) => selectNode(a) });
  accessView = new AccessView($('access-pane'));
  insp = new Inspector($('insp'), {
    onPlan: (id) => selectPlan(id),
    onAccess: () => setView('access'), // the State section
    onSelectNode: (a) => selectNode(a, { center: true }),
    onSelectModule: (p) => selectModule(p, { center: true }),
    onTab: setTab,
    onReach: (a) => { if (S.sel !== a) selectNode(a, { pulse: false }); setReach(true, true); },
    onToggleModule: toggleModule,
    onFit: (p) => { const lane = S.lay.lanes.find((l) => l.path === p); if (lane) gv.centerOnBox(lane); else { const id = S.vis.items.get('m:' + p) ? 'm:' + p : null; if (id) gv.centerOn(id, 0.9); } if (S.view !== 'graph') setView('graph'); },
    onError: (m) => toast(m, 'error', 6000),
    announce,
  });
  palette = new Palette($('palette'), {
    announce,
    onPick: (kind, key) => {
      if (S.view === 'access') setView(S.lastResources || 'graph'); // a resource is found among the resources
      if (S.view !== 'graph' && kind === 'module') setView('graph');
      if (kind === 'module') {
        const mod = S.model.modules.get(key);
        for (const m of chain(mod)) S.collapsed.delete(m.path);
        relayout();
        renderRail();
        selectModule(key, { center: true });
      } else selectNode(key, { center: true });
    },
    onOpen: (on) => { S.palette = on; },
  });
  sheet = new Sheet($('sheet'), { onOpen: () => {} });

  $('view-graph').addEventListener('click', () => setView('graph'));
  $('view-table').addEventListener('click', () => setView('table'));
  $('sec-resources').addEventListener('click', () => { if (S.view === 'access') setView(S.lastResources || 'graph'); });
  $('sec-access').addEventListener('click', () => setView('access'));
  $('search-btn').addEventListener('click', () => palette.open());
  $('theme-btn').addEventListener('click', toggleTheme);
  $('btn-fit').addEventListener('click', () => gv.fit(true));
  $('btn-zin').addEventListener('click', () => gv.zoomBy(1.3, undefined, undefined, true));
  $('btn-zout').addEventListener('click', () => gv.zoomBy(1 / 1.3, undefined, undefined, true));
  $('btn-reset').addEventListener('click', resetView);
  $('btn-collapse').addEventListener('click', () => setAllModules(true));
  $('btn-expand').addEventListener('click', () => setAllModules(false));
  $('menu-btn').addEventListener('click', toggleRail);
  $('insp-btn').addEventListener('click', toggleInspector);
  $('insp-close').addEventListener('click', () => { closeDrawer(); });
  $('insp-size').addEventListener('click', toggleInspSize);
  document.addEventListener('click', (ev) => {
    const c = ev.target instanceof Element && ev.target.closest('[data-copy]');
    // the check mark is for a copy that worked; a copy that did not shows the cross, as the other copy buttons do
    if (c) copyText(c.dataset.copy).then((ok) => { c.classList.toggle('done', ok); c.classList.toggle('failed', !ok); const i = c.querySelector('.i'); if (i) i.className = 'i ' + (ok ? 'i-check' : 'i-x'); setTimeout(() => { c.classList.remove('done', 'failed'); if (i) i.className = 'i i-copy'; }, 1400); announce(ok ? copiedWhat(c) : 'Could not copy'); });
  });
  initTips();
  // the skip link goes to the graph, or to the table in the table view; it must not write #canvas into the address, which is the view
  const skip = document.querySelector('.skiplink');
  if (skip) skip.addEventListener('click', (ev) => { ev.preventDefault(); if (S.view === 'graph') $('canvas').focus(); else if (S.view === 'access') $('access-pane').focus(); else table.scroller.focus(); });
  // the mark at the top left goes back to the start: nothing selected, no filter, the first view (not the address "#/", which would drop the theme and the motion setting too)
  const brand = document.querySelector('.brand');
  if (brand) brand.addEventListener('click', (ev) => {
    ev.preventDefault();
    if (!S.model) return;
    applyExternal(Object.assign(readFragment(), { sel: null, tab: null, filter: null, q: '', collapse: [], expand: [], zoom: 'fit', palette: false, help: false }));
    gv.fit(true);
    syncFragment();
  });
  api.onUnauthorized(showGate);
  // a page that is not on screen does not animate (see data-hidden in app.css)
  const vis = () => root.toggleAttribute('data-hidden', document.hidden);
  document.addEventListener('visibilitychange', vis);
  vis();
  $('scrim').addEventListener('click', closeDrawer);
  $('fatal-retry').addEventListener('click', () => location.reload());
  const q = $('filter-q');
  let qt = 0;
  q.addEventListener('input', () => { clearTimeout(qt); qt = setTimeout(() => setQuery(q.value), 120); });
  document.addEventListener('keydown', onKey);
  window.addEventListener('hashchange', () => { if (booted) applyExternal(readFragment()); });
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => applyTheme(S.theme, false));
  // the ages of the plans move on; a list that has the focus is left alone until the focus goes
  setInterval(() => { if (S.graph) { renderStatus(); if (!$('rail-plans').contains(document.activeElement)) renderPlans(); } }, 30000);
}

// applyExternal brings the view in line with a fragment that someone else wrote (a pasted link).
function applyExternal(f) {
  const want = wantedPlan(f);
  // a link that names a plan that the state does not hold: said, as it is when the page opens with it
  if (f.plan && f.plan !== 'none' && !S.plans.some((p) => p.id === f.plan)) toast(`${f.plan} is not stored`, 'warn', 7000, 'The link names a plan that the state does not hold.');
  if (want !== S.plan) {
    // another plan is laid over the graph first; the rest of the link is applied to that picture
    selectPlan(want).then(() => applyView(f));
    return;
  }
  applyView(f);
}

function applyView(f) {
  if (f.theme && f.theme !== S.theme) applyTheme(f.theme, false);
  applyMotion(f.motion);
  const collapsedBefore = [...S.collapsed].sort().join('|') + '#' + [...S.clusters.keys()].sort().join('|');
  const mods = S.model.modules;
  const base = new Set(S.auto);
  for (const x of f.collapse) { if (x === 'all') for (const m of mods.values()) { if (!m.isRoot) base.add(m.path); } else if (mods.has(x)) base.add(x); }
  S.clusters = new Map();
  for (const x of f.expand) { if (x === 'all') base.clear(); else if (x.startsWith('c:')) S.clusters.set(x, CLUSTER_CAP); else if (mods.has(x)) for (const m of chain(mods.get(x))) base.delete(m.path); }
  S.collapsed = base;
  S.trail = [];
  updateBack();
  S.filter = f.filter;
  S.q = f.q;
  $('filter-q').value = f.q;
  S.tab = f.tab;
  const sel = f.sel && (S.model.nodes.has(f.sel) || mods.has(f.sel)) ? f.sel : null;
  revealSel(f, sel);
  const collapsedAfter = [...S.collapsed].sort().join('|') + '#' + [...S.clusters.keys()].sort().join('|');
  if (collapsedBefore !== collapsedAfter) relayout();
  renderRail();
  applyMatch();
  if (f.view !== S.view) setView(f.view);
  S.sel = sel;
  S.reach = false;
  applySelection(false);
  showInspector();
  rail.setSelected(sel ? (S.model.nodes.has(sel) ? 'n:' + sel : 'm:' + sel) : null, false);
  table.setSelected(S.model.nodes.has(sel) ? sel : null);
  if (S.view === 'table') renderTable();
  // the camera follows the link: a resource is brought into view at a scale that can be read, a link that
  // changes which modules are open shows the whole picture, and zoom= is a scale
  if (S.view === 'graph') {
    const zoom = parseFloat(f.zoom);
    const id = sel && S.model.nodes.has(sel) ? itemOf(sel) : null;
    if (zoom > 0 && isFinite(zoom)) {
      if (id) { const p = S.lay.pos.get(id); gv.userMoved = true; gv.setView({ x: gv.size.w / 2 - (p.x + p.w / 2) * zoom, y: gv.size.h / 2 - (p.y + p.h / 2) * zoom, k: zoom }, true); } else { gv.fit(false); gv.zoomBy(zoom / gv.view.k, undefined, undefined, true); }
    } else if (id) {
      if (gv.view.k < 0.8 || !gv.visible(id, 60)) gv.centerOn(id, 0.95);
    } else if (sel && S.model.modules.has(sel)) {
      const lane = S.lay.lanes.find((l) => l.path === sel);
      const box = lane || (S.lay.pos.get('m:' + sel));
      if (box && !boxVisible(box)) gv.centerOnBox(box);
    } else if (collapsedBefore !== collapsedAfter) gv.fit(true);
  }
  if (f.palette && !palette.isOpen) palette.open(f.pq);
  if (f.help && !sheet.isOpen) sheet.open();
}

async function boot() {
  const frag = readFragment();
  applyMotion(frag.motion);
  if (/[?&]mock=/.test(location.search)) {
    try { await import('../dev/mock.js'); } catch (err) { console.error('the mock shim did not load', err); }
  }
  wire();
  applyTheme(frag.theme || (root.getAttribute('data-theme') || null), false);
  if (!frag.theme && root.getAttribute('data-theme')) S.theme = root.getAttribute('data-theme');
  $('st-note').dataset.addr = location.host || '127.0.0.1';
  renderLive();
  $('filter-q').value = frag.q;
  await load(frag);
}

async function load(frag) {
  let r;
  try { r = await fetchAll(frag.plan || undefined); } catch (err) {
    $('skeleton').hidden = true;
    if (err instanceof api.Unauthorized) showGate();
    else {
      const gone = err instanceof api.Unreachable;
      $('fatal-msg').textContent = gone ? 'The server did not answer.' : String(err.message || err);
      $('fatal-fine').textContent = gone ? 'pravo serve may have stopped. Start it again and open the address that it prints.' : 'The server is still running. Fix what the message says and try again: the page reads the project afresh.';
      $('fatal').hidden = false;
    }
    return;
  }
  gv.resize();
  S.autoSize = { w: gv.size.w, h: gv.size.h };
  S.raw = r.raw;
  S.plans = r.plans;
  S.access = r.access;
  S.plan = r.plan;
  S.overlay = r.overlay;
  setModel(shown(r.raw), frag);
  S.view = frag.view;
  S.filter = frag.filter;
  S.q = frag.q;
  S.tab = frag.tab;
  S.zoom = frag.zoom;
  const sel = frag.sel && (S.model.nodes.has(frag.sel) || S.model.modules.has(frag.sel)) ? frag.sel : null;
  revealSel(frag, sel);
  S.sel = sel;
  $('skeleton').hidden = true;
  $('app').dataset.view = S.view;
  renderHeader();
  renderBanners();
  renderLegend();
  renderPlans();
  if (r.gone) toast(`${r.gone} is not stored`, 'warn', 7000, 'The link names a plan that the state does not hold.');
  if (r.failed) toast(`Could not read ${r.failed.id}: ${r.failed.message}`, 'warn', 7000, 'The page shows the plan of the server instead.');
  palette.setModel(S.model);
  $('empty').hidden = S.model.list.length > 0;
  $('app').classList.toggle('is-empty', S.model.list.length === 0);
  if (sel) openDrawer(); // before the picture is laid out and framed: on a narrow screen the panel takes room from the graph
  gv.resize();
  relayout({ animate: false, intro: true, draw: false });
  const zoom = parseFloat(S.zoom);
  if (zoom > 0 && isFinite(zoom)) {
    const id = sel && S.model.nodes.has(sel) ? itemOf(sel) : null;
    if (id) { const p = S.lay.pos.get(id); gv.userMoved = true; gv.setView({ x: gv.size.w / 2 - (p.x + p.w / 2) * zoom, y: gv.size.h / 2 - (p.y + p.h / 2) * zoom, k: zoom }, false); } else { gv.fit(false); gv.zoomBy(zoom / gv.view.k); }
  } else gv.fit(false);
  setView(S.view);
  renderRail();
  showInspector();
  if (sel) {
    const isNode = S.model.nodes.has(sel);
    rail.ensureVisible(sel);
    rail.setSelected((isNode ? 'n:' : 'm:') + sel, false);
    table.setSelected(isNode ? sel : null);
    // a deep link to a resource shows it at a scale that can be read: when the whole picture does not do that, the camera goes to the resource
    if (S.view === 'graph' && isNode && !(zoom > 0) && (gv.view.k < 0.8 || !gv.visible(itemOf(sel), 60))) gv.centerOn(itemOf(sel), 0.95, false);
    openDrawer();
  }
  minimapDraw();
  booted = true;
  conn = api.events(onChange, onLiveState);
  if (frag.palette) palette.open(frag.pq);
  if (frag.help) sheet.open();
  if (/[?&](mock|debug)=/.test(location.search)) window.__pravo = { S, gv, rail, plansList, table, accessView, insp, palette, relayout, selectNode, selectModule, toggleModule, clusterAction, setFilter, setView, refresh, toast, applyExternal, readFragment, resetView, selectPlan };
  beat();
  root.dataset.ready = '1';
}

boot();
