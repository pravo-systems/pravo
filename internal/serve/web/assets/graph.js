// The graph view: one SVG, a layer that pans and zooms by a CSS transform, items that glide to their new
// positions when the layout changes, the choreography of the first paint, of a selection and of a live
// update, the camera, the card that a hover opens, and the minimap. The layout is in layout.js and the items
// are in model.js.
//
// What is on screen depends on the zoom, and it changes without a jump: a few variables on the svg (--lab,
// --sub) carry how much of the text shows, and the titles of the lanes are counter-scaled (--ls) so that they
// stay readable when the picture is small. Nothing in the picture is a bitmap.

import { s, h, clamp, ease, schedule, actionOf, plural } from './util.js';
import { G, FIT, fitScale, pathOf, midOf, lengthOf } from './layout.js';
import { eachNode, cellsOf, isOut, isEnv } from './model.js';
import { initials, hueOf } from './plans.js';
import { providerOf, pvClass } from './provider.js';
import { ENTITY_LABEL, instanceName } from './query.js';

// glyphs on a 16 by 16 grid
const GLYPH = {
  lock: [['fill', 'M4 7h8v6.5H4z'], ['line', 'M5.6 7V5.2a2.4 2.4 0 0 1 4.8 0V7']],
  hold: [['line', 'M8 2.4a5.6 5.6 0 1 0 .01 0z'], ['line', 'M6.4 5.8v4.4M9.6 5.8v4.4']],
  drift: [['line', 'M3 6h10M3 10h10M10.2 3.4L5.8 12.6']],
  gone: [['line', 'M6.6 9.4l-1.8 1.8a2.1 2.1 0 0 1-3-3l1.8-1.8a2.1 2.1 0 0 1 3 0M9.4 6.6l1.8-1.8a2.1 2.1 0 0 1 3 3l-1.8 1.8a2.1 2.1 0 0 1-3 0M6 3.2l.6 1.6M10 12.8l-.6-1.6M3.2 6l1.6.6M12.8 10l-1.6-.6']],
  orphan: [['line', 'M8 3.4a4.6 4.6 0 1 0 .01 0zM4.8 11.2l6.4-6.4']],
  chevD: [['line', 'M4.5 6.2L8 9.8l3.5-3.6']],
  chevR: [['line', 'M6.2 4.5L9.8 8l-3.6 3.5']],
  grid: [['fill', 'M3 3h3v3H3zM6.5 3h3v3h-3zM10 3h3v3h-3zM3 6.5h3v3H3zM6.5 6.5h3v3h-3zM10 6.5h3v3h-3zM3 10h3v3H3zM6.5 10h3v3h-3zM10 10h3v3h-3z']],
  up: [['line', 'M4.5 9.8L8 6.2l3.5 3.6']],
  plus: [['line', 'M8 3.5v9M3.5 8h9']],
  // an output: a value leaves the root through a door
  out: [['line', 'M7 3.5H3.6v9H7M7.6 8h5.8M11 5.4L13.6 8 11 10.6']],
  // a data source reads: an eye; an ephemeral resource lives for a run: an hourglass
  eye: [['line', 'M1.8 8S4.2 3.8 8 3.8 14.2 8 14.2 8 11.8 12.2 8 12.2 1.8 8 1.8 8zM8 6.2a1.8 1.8 0 1 0 .01 0z']],
  glass: [['line', 'M4 2.8h8M4 13.2h8M5 2.8c0 3 3 3.4 3 5.2s-3 2.2-3 5.2M11 2.8c0 3-3 3.4-3 5.2s3 2.2 3 5.2']],
  // an environment variable: a dollar sign
  env: [['line', 'M10.6 5.3C10.1 4.3 9.1 3.8 8 3.8c-1.4 0-2.4.7-2.4 1.8 0 2.4 4.9 1.2 4.9 3.8 0 1.1-1 1.9-2.5 1.9-1.1 0-2.1-.5-2.6-1.4M8 2.4v1.4M8 12.2v1.4']],
};

// facet marks an element of a card as a way to filter: a click shows only what holds the value, shift hides it
function facet(el, col, value, label) {
  el.dataset.facet = col + ':' + value;
  el.classList.add('fx');
  el.append(s('title', null, label));
  return el;
}

function glyph(name, x, y, size, cls) {
  const g = s('g', { class: 'gl' + (cls ? ' ' + cls : ''), transform: `translate(${x} ${y}) scale(${size / 16})` });
  for (const [k, d] of GLYPH[name]) g.append(s('path', { d, class: k === 'fill' ? 'gf' : 'gs' }));
  return g;
}

// what an item shows: when it changes the element is built again
function itemSig(x, ctx) {
  if (x.kind === 'node') return [x.n.name, x.n.type, x.n.action, x.n.drifted, x.n.gone, x.n.locked, x.n.sensitive, x.n.presence, x.n.mode, pvClass(providerOf(x.n.type, x.n.provider)), ctx.hasPlan, x.look, (x.n.plans || []).join(','), x.n.overlap ? 1 : 0].join('|');
  if (x.kind === 'ctl') return [x.shown, x.total].join('|');
  const a = x.agg;
  const pi = plansIn(x, ctx);
  return [x.kind, pi.list.map((p) => p.id).join(','), pi.overlap ? 1 : 0, a.n, a.out, a.plan, a.create, a.update, a.replace, a.delete, a.drifted, a.gone, a.sensitive, a.locked, a.orphan, x.look, x.h, x.kind === 'namespace' ? x.ns.kids.length : ''].join('|');
}

const trunc = (t, n) => (t.length > n ? t.slice(0, Math.max(1, n - 1)) + '…' : t);
const MONO = 0.6; // the advance of the monospaced face, in em
const chars = (px, size) => Math.max(3, Math.floor(px / (size * MONO)));

export function nodeClasses(n, ctx) {
  let c = 'node';
  const a = actionOf(n.action);
  if (a) c += ' ' + a.cls;
  if (ctx.hasPlan && !n.action && !isOut(n)) c += ' notread';
  if (n.drifted) c += ' drifted';
  if (n.gone) c += ' gone';
  if (n.locked) c += ' held';
  if (n.sensitive > 0) c += ' sens';
  if (n.presence === 'spec') c += ' spec';
  if (n.presence === 'state') c += ' orphan';
  if (n.mode && n.mode !== 'managed') c += ' m-' + n.mode;
  if (isEnv(n)) c += ' m-env';
  if (n.overlap) c += ' overlap';
  return c;
}

// authorsOf lists the authors of the open stored plans that change a node, one for each plan, newest first
export function authorsOf(n, ctx) {
  return (n.plans || []).map((id) => ({ id, author: (ctx.authors && ctx.authors.get(id)) || '?' }));
}

// plansIn says which open stored plans change something inside a namespace or a group of siblings: the authors, one for
// each plan, in the order that the nodes give them, how many resources are changed, and whether two plans change one.
// A card has no resource of its own to carry the discs, so it carries those of what it holds.
export function plansIn(it, ctx) {
  const out = { list: [], resources: 0, overlap: false };
  if (!ctx.authors || !ctx.authors.size) return out;
  const seen = new Set();
  const visit = (n) => {
    if (isOut(n) || !(n.plans || []).length) return;
    out.resources++;
    if (n.overlap) out.overlap = true;
    for (const p of authorsOf(n, ctx)) if (!seen.has(p.id)) { seen.add(p.id); out.list.push(p); }
  };
  if (it.kind === 'namespace') eachNode(it.ns, visit); else for (const n of it.members || []) visit(n);
  return out;
}

export function describeNode(n, ctx) {
  const bits = [isOut(n) ? `${n.name}, output` : `${n.name}, ${n.type}`];
  if (n.action) bits.push(n.action === 'no-op' ? 'no change planned' : 'plan: ' + n.action);
  if ((n.plans || []).length) bits.push(`${n.overlap ? 'overlap: ' : ''}changed by ${authorsOf(n, ctx || {}).map((p) => `${p.id} of ${p.author}`).join(', ')}`);
  if (n.drifted) bits.push('drifted');
  if (n.gone) bits.push('gone');
  if (n.locked) bits.push('locked');
  if (n.sensitive) bits.push(isOut(n) ? 'sensitive, the value is withheld' : `${n.sensitive} sensitive values`);
  if (n.presence === 'spec') bits.push('declared only');
  if (n.presence === 'state') bits.push('orphan');
  return bits.join(', ');
}

// the plan counts of a namespace, as small coloured chips; the chips end at x
function planBadges(agg, x, y, anchorRight, bare) {
  const parts = [];
  if (agg.create) parts.push(['a-create', agg.create]);
  const up = agg.update + agg.replace;
  if (up) parts.push(['a-update', up]);
  if (agg.delete) parts.push(['a-delete', agg.delete]);
  const g = s('g', { class: 'pb' });
  const wOf = (n) => (bare ? 8 : 11 + String(n).length * 6.4) + 6;
  let cx = anchorRight ? x - parts.reduce((w, p) => w + wOf(p[1]), 0) + 8 : x;
  for (const [cls, n] of parts) {
    const bg = s('g', { class: 'pbi ' + cls, transform: `translate(${cx} ${y})` });
    bg.append(s('rect', { class: 'sq', width: 8, height: 8, rx: 2 }));
    if (!bare) bg.append(s('text', { class: 'pbn', x: 11, y: 8 }, String(n)));
    g.append(bg);
    cx += wOf(n);
  }
  return g;
}

const stateMarks = (agg) => {
  const m = [];
  if (agg.drifted || agg.gone) m.push(['drift', agg.drifted + agg.gone]);
  if (agg.sensitive) m.push(['lock', agg.sensitive]);
  return m;
};

// motion that settles with a little give
const spring = (t) => { if (t <= 0) return 0; if (t >= 1) return 1; const c = 0.8, u = t - 1; return 1 + (c + 1) * u * u * u + c * u * u; };

// How a namespace is coloured: a namespace whose resources all use one provider instance has the look of that
// provider (the class of its tile), and any other namespace is neutral, so is one that holds only built-in values and
// outputs. The built-in values, the environment variables and the outputs belong to no provider and do not count.
const looks = new WeakMap();
const unowned = (n) => isOut(n) || isEnv(n) || n.mode === 'call' || n.mode === 'builtin';
function lookOfNodes(nodes) {
  let first = null;
  for (const n of nodes) {
    if (unowned(n)) continue;
    if (!first) first = n;
    else if ((n.provider || '') !== (first.provider || '') || (!n.provider && n.type.split('_')[0] !== first.type.split('_')[0])) return null;
  }
  return first ? providerOf(first.type, first.provider) : null;
}
function lookOf(ns) {
  if (!ns || ns.isRoot) return null;
  if (!looks.has(ns)) {
    const all = [];
    eachNode(ns, (n) => all.push(n));
    looks.set(ns, lookOfNodes(all));
  }
  return looks.get(ns);
}
const lookClass = (p) => (p ? 'pvl ' + pvClass(p) : 'neutral');
const LOOK_CLASSES = /^(pvl|neutral|pv\d|pvx)$/;
// setLook gives an element the classes of a look and takes off the ones of the look it had
function setLook(el, p) {
  const next = lookClass(p).split(' ');
  for (const c of [...el.classList]) if (LOOK_CLASSES.test(c)) el.classList.remove(c);
  el.classList.add(...next);
}

export class GraphView {
  constructor(host, cb) {
    this.host = host;
    this.cb = cb;
    this.vis = null;
    this.lay = null;
    this.ctx = { hasPlan: false, authors: new Map() };
    this.items = new Map();
    this.lanes = new Map();
    this.edges = new Map();
    this.adj = new Map();
    this.view = { x: 0, y: 0, k: 1 };
    this.goal = null;
    this.size = { w: 0, h: 0 };
    this.selection = null;
    this.tween = null;
    this.camTween = null;
    this.glideRaf = 0;
    this.motion = true;
    this.lodKey = '';
    this.userMoved = false;
    this.guardUntil = 0;
    this.clips = new Map();

    this.svg = s('svg', { class: 'graph', 'aria-hidden': 'true', focusable: 'false' });
    const defs = s('defs');
    for (const [id, cls] of [['ah', 'ah'], ['ah-up', 'ah-up'], ['ah-down', 'ah-down'], ['ah-dim', 'ah-dim']]) {
      defs.append(s('marker', { id, viewBox: '0 0 10 10', refX: 9, refY: 5, markerWidth: 8, markerHeight: 8, orient: 'auto', markerUnits: 'userSpaceOnUse' },
        s('path', { d: 'M1 1.2L9.2 5 1 8.8z', class: cls })));
    }
    // a card is a shade lighter at the top than at the bottom
    defs.append(s('linearGradient', { id: 'card-fill', x1: 0, y1: 0, x2: 0, y2: 1 }, s('stop', { offset: '0', class: 'cf0' }), s('stop', { offset: '1', class: 'cf1' })));
    this.defs = defs;
    this.svg.append(defs);
    this.vp = s('g', { class: 'vp' });
    this.laneLayer = s('g', { class: 'lanes' });
    this.edgeLayer = s('g', { class: 'edges' });
    this.labelLayer = s('g', { class: 'elabels' });
    this.headLayer = s('g', { class: 'heads' }); // the titles of the lanes lie above the edges, so that no line runs through a word
    this.nodeLayer = s('g', { class: 'nodes' });
    this.fxLayer = s('g', { class: 'fx' });
    this.vp.append(this.laneLayer, this.edgeLayer, this.labelLayer, this.headLayer, this.nodeLayer, this.fxLayer);
    this.svg.append(this.vp);
    this.grid = h('div', { class: 'grid', 'aria-hidden': 'true' });
    this.tip = h('div', { class: 'gtip overlay', 'aria-hidden': 'true', hidden: true });
    host.append(this.grid, this.svg, this.tip);

    this.ro = new ResizeObserver(() => this.resize());
    this.ro.observe(host);
    this.bindPointer();
    this.bindHover();
  }

  get reduced() { return !this.motion || matchMedia('(prefers-reduced-motion: reduce)').matches; }

  resize() {
    const r = this.host.getBoundingClientRect();
    const first = !this.size.w;
    const old = this.size;
    this.size = { w: r.width, h: r.height };
    if (!r.width || !r.height) return;
    if (first && this.lay && !this.userMoved) this.fit(false);
    else if (!first && this.lay && !this.userMoved && (Math.abs(old.w - r.width) > 40 || Math.abs(old.h - r.height) > 40)) this.fit(false);
    else if (!first) { this.view.x += (r.width - old.w) / 2; this.view.y += (r.height - old.h) / 2; this.applyView(); }
    if (this.cb.onView) this.cb.onView();
  }

  // the shape of the canvas, for the layout and for the first view
  get shape() { return { w: this.size.w, h: this.size.h, fit: { w: this.size.w - FIT.padX * 2, h: this.size.h - FIT.padTop - FIT.padBottom } }; }

  // ---- scene ----

  // setScene shows a layout. vis is from buildItems, lay from layout. opts: animate, hasPlan, intro.
  setScene(vis, lay, opts = {}) {
    const first = !this.vis;
    const prevVis = this.vis, prevLay = this.lay;
    this.vis = vis;
    this.lay = lay;
    this.ctx = { hasPlan: !!opts.hasPlan, authors: opts.authors || new Map() };
    const animate = !!opts.animate && !first && !this.reduced;
    this.stopTween();
    const moves = { items: [], lanes: [], edges: [] };

    // items
    const seen = new Set();
    let idx = 0;
    for (const it of vis.order) {
      const p = lay.pos.get(it.id);
      seen.add(it.id);
      it.look = it.kind === 'namespace' ? lookClass(lookOf(it.ns)) : it.kind === 'cluster' ? lookClass(lookOfNodes(it.members)) : '';
      let r = this.items.get(it.id);
      if (!r) {
        r = this.createItem(it);
        this.items.set(it.id, r);
        this.nodeLayer.append(r.g);
        const from = animate ? this.anchor(it, prevVis, prevLay) : null;
        r.x = from ? from.x : p.x;
        r.y = from ? from.y : p.y;
        if (animate) r.g.classList.add('enter');
      } else {
        this.updateItem(r, it);
        r.g.classList.remove('leave');
        r.leaving = false;
      }
      r.it = it;
      r.fx = r.x; r.fy = r.y; r.tx = p.x; r.ty = p.y; r.rank = p.rank;
      r.g.style.setProperty('--r', String(p.rank));
      r.g.style.setProperty('--i', String(Math.min(idx++, 70)));
      if (!animate) { r.x = p.x; r.y = p.y; this.placeItem(r); } else moves.items.push(r);
      if (animate && r.g.classList.contains('enter')) {
        this.placeItem(r);
        // one listener for an item that is still entering, however many scenes come before the animation ends
        if (!r.enterL) { r.enterL = true; r.g.addEventListener('animationend', () => { r.enterL = false; r.g.classList.remove('enter'); }, { once: true }); }
      }
    }
    for (const [id, r] of [...this.items]) {
      if (seen.has(id)) continue;
      if (!animate) { r.g.remove(); this.items.delete(id); continue; }
      const to = this.anchor(r.it, vis, lay);
      r.leaving = true;
      r.g.classList.add('leave');
      r.fx = r.x; r.fy = r.y;
      r.tx = to ? to.x : r.x; r.ty = to ? to.y : r.y;
      moves.items.push(r);
    }

    // lanes: a lane that opens grows out of the card that stood for it, and one that closes shrinks into it
    const laneSeen = new Set();
    let li = 0;
    for (const L of lay.lanes) {
      laneSeen.add(L.path);
      let r = this.lanes.get(L.path);
      const ns = this.cb.namespaceOf(L.path);
      if (!r) {
        r = this.createLane(L, ns);
        this.lanes.set(L.path, r);
        this.laneLayer.append(r.g);
        const card = animate ? this.cardBox('ns:' + L.path, prevVis, prevLay) : null;
        r.box = card || { x: L.x, y: L.y, w: L.w, h: L.h };
        if (animate) { r.g.classList.add('enter'); r.hg.classList.add('enter'); }
      } else {
        r.g.classList.remove('leave');
        r.hg.classList.remove('leave');
        r.leaving = false;
        this.updateLane(r, L, ns);
      }
      r.depth = L.depth;
      for (const el of [r.g, r.hg]) {
        el.style.setProperty('--depth', String(L.depth));
        el.classList.toggle('deep', L.depth > 1);
        el.style.setProperty('--i', String(li));
      }
      li++;
      L.look = lookOf(ns);
      setLook(r.g, L.look);
      setLook(r.hg, L.look);
      r.from = Object.assign({}, r.box);
      r.to = { x: L.x, y: L.y, w: L.w, h: L.h };
      if (!animate) { r.box = Object.assign({}, r.to); this.placeLane(r); } else { moves.lanes.push(r); this.placeLane(r); }
    }
    for (const [path, r] of [...this.lanes]) {
      if (laneSeen.has(path)) continue;
      if (!animate) { r.g.remove(); r.hg.remove(); this.lanes.delete(path); continue; }
      r.leaving = true;
      r.g.classList.add('leave');
      r.hg.classList.add('leave');
      r.from = Object.assign({}, r.box);
      r.to = this.cardBox('ns:' + path, vis, lay) || Object.assign({}, r.box);
      moves.lanes.push(r);
    }

    // edges
    this.adj = new Map();
    const edgeSeen = new Set();
    let calm = 0;
    for (const e of vis.edges) {
      const pts = lay.routes.get(e.key);
      if (!pts) continue;
      edgeSeen.add(e.key);
      for (const id of [e.a, e.b]) { let a = this.adj.get(id); if (!a) this.adj.set(id, (a = [])); a.push(e.key); }
      let r = this.edges.get(e.key);
      if (!r) {
        r = this.createEdge(e);
        this.edges.set(e.key, r);
        this.edgeLayer.append(r.path);
        r.pts = clonePts(pts);
        if (animate) r.path.classList.add('enter');
      } else {
        r.path.classList.remove('leave');
        r.leaving = false;
      }
      r.e = e;
      r.rank = lay.pos.get(e.a).rank;
      r.path.style.setProperty('--r', String(r.rank));
      r.from = clonePts(r.pts);
      r.to = clonePts(pts);
      this.updateEdgeLabel(r, e);
      if (!animate) { r.pts = clonePts(pts); this.placeEdge(r); } else { moves.edges.push(r); this.placeEdge(r); }
      calm++;
    }
    // many edges say less each: they are drawn lighter as they grow in number
    this.svg.style.setProperty('--ed', clamp(1.2 - edgeSeen.size / 110, 0.5, 1).toFixed(2));
    for (const [key, r] of [...this.edges]) {
      if (edgeSeen.has(key)) continue;
      if (!animate) { r.path.remove(); if (r.label) r.label.remove(); this.edges.delete(key); continue; }
      r.leaving = true;
      r.path.classList.add('leave');
      if (r.label) r.label.classList.add('leave');
    }

    if (first || opts.intro) {
      this.pulseRings();
      this.svg.classList.add('intro');
      clearTimeout(this._introT);
      this._introT = setTimeout(() => this.svg.classList.remove('intro'), 2600);
    }
    // a great many pulsing rings cost more than they tell: they stand still
    let pulsing = 0;
    for (const it of vis.order) if (it.kind === 'node' && (it.n.drifted || it.n.gone || (this.ctx.hasPlan && it.n.action && it.n.action !== 'no-op'))) pulsing++;
    this.svg.classList.toggle('calm', pulsing > 28);
    if (animate && moves.items.length + moves.lanes.length + moves.edges.length) this.runTween(moves, opts.duration || 400);
    else this.finish(moves);
    this.restyleAll();
    this.updateLod(true);
    if (this.cb.onScene) this.cb.onScene();
  }

  // anchor finds where an item of one scene was (or will be) in the other: the mean position of the items
  // that stand for its nodes there.
  anchor(it, vis, lay) {
    if (!vis || !lay || !it) return null;
    const addrs = [];
    if (it.kind === 'node') addrs.push(it.n.addr);
    else if (it.kind === 'namespace') { eachNode(it.ns, (n) => { if (addrs.length < 40) addrs.push(n.addr); }); }
    else if (it.kind === 'cluster') for (const n of it.members.slice(0, 40)) addrs.push(n.addr);
    else if (it.kind === 'ctl') { const c = vis.items.get(it.cluster); if (c) { const p = lay.pos.get(c.id); if (p) return { x: p.x, y: p.y }; } return null; }
    let sx = 0, sy = 0, n = 0;
    const done = new Set();
    for (const a of addrs) {
      const id = vis.rep.get(a);
      if (!id || done.has(id)) continue;
      done.add(id);
      const p = lay.pos.get(id);
      if (p) { sx += p.x; sy += p.y; n++; }
    }
    return n ? { x: sx / n, y: sy / n } : null;
  }

  cardBox(id, vis, lay) {
    if (!vis || !lay) return null;
    const p = lay.pos.get(id);
    return p ? { x: p.x, y: p.y, w: p.w, h: p.h } : null;
  }

  runTween(moves, dur) {
    const t0 = performance.now();
    const step = (now) => {
      const t = clamp((now - t0) / dur, 0, 1);
      const e = spring(t);
      for (const r of moves.items) { r.x = r.fx + (r.tx - r.fx) * e; r.y = r.fy + (r.ty - r.fy) * e; this.placeItem(r); }
      for (const r of moves.lanes) {
        r.box = { x: r.from.x + (r.to.x - r.from.x) * e, y: r.from.y + (r.to.y - r.from.y) * e, w: r.from.w + (r.to.w - r.from.w) * e, h: r.from.h + (r.to.h - r.from.h) * e };
        this.placeLane(r);
      }
      for (const r of moves.edges) { r.pts = mixPts(r.from, r.to, e); this.placeEdge(r); }
      if (t < 1) this.tween = requestAnimationFrame(step);
      else { this.tween = null; this.finish(moves); this.restyleAll(); if (this.cb.onSettled) this.cb.onSettled(); }
    };
    this.tween = requestAnimationFrame(step);
  }

  stopTween() {
    if (this.tween) { cancelAnimationFrame(this.tween); this.tween = null; }
  }

  finish(moves) {
    for (const r of moves.items) { r.x = r.tx; r.y = r.ty; this.placeItem(r); }
    for (const r of moves.lanes) { r.box = Object.assign({}, r.to); this.placeLane(r); }
    for (const r of moves.edges) { r.pts = clonePts(r.to); this.placeEdge(r); }
    for (const [id, r] of [...this.items]) if (r.leaving) { r.g.remove(); this.items.delete(id); }
    for (const [p, r] of [...this.lanes]) if (r.leaving) { r.g.remove(); r.hg.remove(); this.lanes.delete(p); }
    for (const [k, r] of [...this.edges]) if (r.leaving) { r.path.remove(); if (r.label) r.label.remove(); this.edges.delete(k); }
    if (this.cb.onSettled && !this.tween) schedule('settled', () => this.cb.onSettled());
  }

  placeItem(r) { r.g.setAttribute('transform', `translate(${r.x.toFixed(1)} ${r.y.toFixed(1)})`); }
  placeLane(r) {
    const b = r.box;
    const x = b.x.toFixed(1), y = b.y.toFixed(1), w = Math.max(0, b.w).toFixed(1), hh = Math.max(0, b.h).toFixed(1);
    r.rect.setAttribute('x', x); r.rect.setAttribute('y', y); r.rect.setAttribute('width', w); r.rect.setAttribute('height', hh);
    r.hit.setAttribute('x', x); r.hit.setAttribute('y', y); r.hit.setAttribute('width', w);
    r.head.setAttribute('transform', `translate(${(b.x + 14).toFixed(1)} ${(b.y + 10).toFixed(1)})`);
  }
  placeEdge(r) {
    const d = pathOf(r.pts);
    r.path.setAttribute('d', d);
    if (r.hot) r.hot.setAttribute('d', d);
    if (r.label) {
      const [x, y] = midOf(r.pts);
      r.label.setAttribute('transform', `translate(${x.toFixed(1)} ${y.toFixed(1)})`);
    }
    // a long edge says little at rest: it is drawn lighter in proportion to its length
    const len = lengthOf(r.pts);
    const f = clamp(520 / Math.max(len, 1), 0.3, 1);
    if (Math.abs(f - (r.lf || 0)) > 0.03) { r.lf = f; r.path.style.setProperty('--el', f.toFixed(2)); }
    r.path.style.setProperty('--dl', String(Math.ceil(len * 1.3)));
  }

  // ---- element factories ----

  clip(w, hh, rx) {
    const id = `cp-${w}-${hh}`;
    if (!this.clips.has(id)) {
      this.defs.append(s('clipPath', { id }, s('rect', { width: w, height: hh, rx })));
      this.clips.set(id, true);
    }
    return `url(#${id})`;
  }

  createItem(it) {
    const r = { it, g: null, x: 0, y: 0, base: '', leaving: false };
    let g;
    if (it.kind === 'node') g = this.buildNode(it);
    else if (it.kind === 'namespace') g = this.buildCard(it, 'ns-item');
    else if (it.kind === 'cluster') g = this.buildCard(it, 'cluster-item');
    else g = this.buildCtl(it);
    g.setAttribute('data-id', it.id);
    r.g = g;
    r.base = g.getAttribute('class');
    r.sig = itemSig(it, this.ctx);
    return r;
  }

  updateItem(r, it) {
    const was = r.sig;
    r.it = it;
    r.sig = itemSig(it, this.ctx);
    if (was === r.sig) return;
    let g;
    if (it.kind === 'node') g = this.buildNode(it);
    else if (it.kind === 'namespace') g = this.buildCard(it, 'ns-item');
    else if (it.kind === 'cluster') g = this.buildCard(it, 'cluster-item');
    else g = this.buildCtl(it);
    const keep = new Set(['sel', 'up', 'down', 'nomatch', 'rel', 'flash', 'enter', 'leave', 'kf', 'reach', 'hov']);
    const extra = [...r.g.classList].filter((c) => keep.has(c));
    g.setAttribute('data-id', it.id);
    for (const c of extra) g.classList.add(c);
    g.setAttribute('transform', r.g.getAttribute('transform') || '');
    g.style.cssText = r.g.style.cssText;
    r.g.replaceWith(g);
    r.g = g;
    r.enterL = false;
    r.base = g.getAttribute('class');
  }

  // buildOutput draws an output as a card like the others: the bar, the tile with the glyph of a
  // value leaving the root, the name, "output" under it, and the marks at the right
  buildOutput(it) {
    const n = it.n, W = it.w, H = it.h;
    const act = actionOf(n.action);
    const badge = !!(act && act.glyph);
    const marks = [];
    if (n.sensitive > 0) marks.push('lock');
    if (n.presence === 'state') marks.push('orphan');
    const g = s('g', { class: nodeClasses(n, this.ctx) + ' neutral', role: 'button', 'aria-label': describeNode(n, this.ctx) });
    const inn = s('g', { class: 'in' });
    inn.append(s('rect', { class: 'halo', x: -6, y: -6, width: W + 12, height: H + 12, rx: 12 }));
    if (this.ctx.hasPlan && n.action && n.action !== 'no-op') inn.append(s('rect', { class: 'ring', x: -3, y: -3, width: W + 6, height: H + 6, rx: 9 }));
    inn.append(s('rect', { class: 'box', width: W, height: H, rx: 7 }));
    inn.append(s('rect', { class: 'bar', width: 4, height: H, 'clip-path': this.clip(W, H, 7) }));
    const tile = s('g', { class: 'tile', transform: `translate(15 ${(H - 26) / 2})` });
    tile.append(s('rect', { class: 'tb', width: 26, height: 26, rx: 6 }), glyph('out', 5, 5, 16, 'mk mk-out'));
    inn.append(tile);
    const nameRoom = W - 52 - (badge ? 34 : 14);
    inn.append(s('text', { class: 'nm', x: 53, y: H / 2 + 5 }, trunc(n.name, chars(nameRoom, 14))));
    if (badge) {
      const b = s('g', { class: 'act', transform: `translate(${W - 26} 8)` });
      b.append(s('circle', { class: 'ab', cx: 8, cy: 8, r: 8 }), s('path', { class: 'ag', d: act.glyph, transform: 'translate(2 2)' }));
      inn.append(b);
    }
    let mx = W - 12 - 13;
    for (const m of marks) { inn.append(glyph(m, mx, 28, 13, 'mk mk-' + m)); mx -= 16; }
    inn.append(this.kindBadge('output', H));
    g.append(inn);
    return g;
  }

  buildNode(it) {
    if (isOut(it.n)) return this.buildOutput(it);
    const n = it.n, W = it.w, H = it.h;
    const marks = [];
    if (n.sensitive > 0) marks.push('lock');
    if (n.locked) marks.push('hold');
    if (n.drifted && !n.gone) marks.push('drift');
    if (n.gone) marks.push('gone');
    if (n.presence === 'state') marks.push('orphan');
    const act = actionOf(n.action);
    const badge = !!(act && act.glyph);
    const pv = providerOf(n.type, n.provider);
    const g = s('g', { class: nodeClasses(n, this.ctx) + ' ' + (unowned(n) ? 'neutral' : pvClass(pv)), role: 'button', 'aria-label': describeNode(n, this.ctx) });
    const inn = s('g', { class: 'in' });
    inn.append(s('rect', { class: 'halo', x: -6, y: -6, width: W + 12, height: H + 12, rx: 12 }));
    if (n.drifted || n.gone || (this.ctx.hasPlan && n.action && n.action !== 'no-op')) inn.append(s('rect', { class: 'ring', x: -3, y: -3, width: W + 6, height: H + 6, rx: 9 }));
    // two open plans that change the node: a solid ring outside the dotted one of the action, in a colour of its own
    if (n.overlap) inn.append(s('rect', { class: 'ovl', x: -5, y: -5, width: W + 10, height: H + 10, rx: 10.5 }));
    inn.append(s('rect', { class: 'box', width: W, height: H, rx: 7 }));
    inn.append(s('rect', { class: 'bar', width: 4, height: H, 'clip-path': this.clip(W, H, 7) }));
    const tile = s('g', { class: 'tile', transform: `translate(15 ${(H - 26) / 2})` });
    tile.append(s('rect', { class: 'tb', width: 26, height: 26, rx: n.mode === 'data' ? 13 : 6 }), isEnv(n) ? glyph('env', 5, 5, 16, 'mk mk-env') : s('text', { class: 'tm', x: 13, y: 17.2 }, pv.mono));
    if (n.provider && !isEnv(n)) facet(tile, 'instance', instanceName(n.provider), `Show only the provider instance ${n.provider} (shift: hide it)`);
    inn.append(tile);
    const nameRoom = W - 52 - (badge ? 34 : 14);
    // an environment variable has the kind in its badge and no type to show
    inn.append(s('text', { class: 'nm', x: 53, y: isEnv(n) ? H / 2 + 5 : 22.5 }, trunc(n.name, chars(nameRoom, 14))));
    if (!isEnv(n)) inn.append(s('text', { class: 'ty', x: 53, y: 40 }, trunc(n.type, chars(W - 53 - 12 - marks.length * 16, 11))));
    if (badge) {
      const b = s('g', { class: 'act', transform: `translate(${W - 26} 8)` });
      b.append(s('circle', { class: 'ab', cx: 8, cy: 8, r: 8 }), s('path', { class: 'ag', d: act.glyph, transform: 'translate(2 2)' }));
      inn.append(b);
    }
    let mx = W - 12 - 13;
    for (const m of marks) { inn.append(glyph(m, mx, 28, 13, 'mk mk-' + m)); mx -= 16; }
    const kind = isEnv(n) ? 'env' : n.mode === 'data' || n.mode === 'ephemeral' ? n.mode : '';
    if (kind) inn.append(this.kindBadge(kind, H));
    const aus = this.authorBadges(authorsOf(n, this.ctx), W);
    if (aus) inn.append(aus);
    g.append(inn);
    return g;
  }

  // kindBadge hangs on the bottom edge at the left of a node (a stack of a collapsed namespace hangs below its card, so the top edge has no room) that is not a plain resource: a glyph and the kind
  kindBadge(mode, H) {
    const label = ENTITY_LABEL[mode] || mode;
    const word = mode, w = 24 + word.length * 5.8;
    const g = s('g', { class: 'kb', transform: `translate(15 ${H})` });
    g.append(s('rect', { class: 'kbr', x: 0, y: -7, width: w, height: 14, rx: 7 }), glyph({ data: 'eye', ephemeral: 'glass', output: 'out', env: 'env' }[mode], 5, -5.5, 11, 'kbg'), s('text', { class: 'kbt', x: 19, y: 3.4 }, word));
    return facet(g, 'entity', mode, `Show only ${label.toLowerCase()}s (shift: hide them)`);
  }

  // authorBadges hang on the top edge of a node that open stored plans change: a disc with the initial of the author
  // of each plan, newest at the right, overlapping a little; more than three give their number
  authorBadges(list, W) {
    if (!list.length) return null;
    const g = s('g', { class: 'aus' });
    const SHOWN = 3, R = 7.5, PITCH = 11;
    list.slice(0, SHOWN).forEach((p, i) => {
      const b = s('g', { class: 'au au' + hueOf(p.author), 'data-plan': p.id, transform: `translate(${W - 15 - i * PITCH} 0)` });
      b.append(s('circle', { class: 'aur', r: R + 1.6 }), s('circle', { class: 'aub', r: R }), s('text', { y: 2.7 }, initials(p.author).slice(0, 2)));
      g.append(b);
    });
    if (list.length > SHOWN) g.append(s('text', { class: 'aun', x: W - 15 - SHOWN * PITCH - 1, y: 3.2 }, '+' + (list.length - SHOWN)));
    return g;
  }

  // the cells of a namespace or a group: one for each resource, in the colour of what is to become of it
  cells(it, x, y, w) {
    const nodes = [];
    if (it.kind === 'namespace') eachNode(it.ns, (n) => { if (!isOut(n)) nodes.push(n); }); else for (const n of it.members) nodes.push(n);
    const list = cellsOf(nodes);
    const g = s('g', { class: 'cells' });
    const pitch = 11, per = Math.max(1, Math.floor((w + 3) / pitch));
    list.forEach((c, i) => {
      g.append(s('rect', { class: 'c c-' + c, x: x + (i % per) * pitch, y: y + Math.floor(i / per) * 13, width: 8, height: 10, rx: 2 }));
    });
    return g;
  }

  buildCard(it, cls) {
    const W = it.w, H = it.h;
    const isNs = it.kind === 'namespace';
    const agg = it.agg;
    const pv = it.look ? ' ' + it.look : '';
    const outs = it.mode === 'output';
    const outsNote = agg.out ? `, ${agg.out} ${agg.out === 1 ? 'output' : 'outputs'}` : '';
    const g = s('g', { class: cls + (agg.plan ? ' has-plan' : '') + pv, role: 'button', 'aria-label': isNs ? `namespace ${it.ns.name}, ${agg.n} resources${outsNote}, collapsed` : outs ? `${agg.n} outputs, grouped` : `${it.type}, ${agg.n} resources, grouped` });
    const inn = s('g', { class: 'in' });
    inn.append(s('rect', { class: 'halo', x: -6, y: -6, width: W + 12, height: H + 12, rx: 12 }));
    inn.append(s('rect', { class: 'stack s2', x: 10, y: 10, width: W - 20, height: H, rx: 7 }));
    inn.append(s('rect', { class: 'stack s1', x: 5, y: 5, width: W - 10, height: H, rx: 7 }));
    inn.append(s('rect', { class: 'box', width: W, height: H, rx: 7 }));
    inn.append(s('rect', { class: 'bar', width: 4, height: H, 'clip-path': this.clip(W, H, 7) }));
    // The wrapper carries the CSS transform of the hover (a nudge to the right):
    // a CSS transform replaces the transform attribute, so it must not sit on the
    // element that is placed and scaled by that attribute.
    inn.append(s('g', { class: 'cico' }, glyph(isNs ? 'chevR' : 'grid', 14, 12, 18)));
    const title = isNs ? it.ns.name : outs ? 'outputs' : it.type;
    const marks = stateMarks(agg);
    const counts = [agg.create, agg.update + agg.replace, agg.delete].filter(Boolean);
    const pbw = (bare) => counts.reduce((w, n) => w + (bare ? 14 : 17 + String(n).length * 6.4), 0) + (counts.length ? 6 : 0);
    // the title stands whole beside the plan counts: at 16 px, else at 14.5, else the counts give up their
    // numbers (the cells and the card of a hover still say them) and the title tries both sizes again
    let bare = false, size = 16;
    const fits = (z, b) => title.length * z * MONO <= W - 38 - 14 - pbw(b);
    if (fits(16, false)) size = 16;
    else if (fits(14.5, false)) size = 14.5;
    else { bare = true; size = fits(16, true) ? 16 : 14.5; }
    const troom = W - 38 - 14 - pbw(bare);
    const tt = s('text', { class: 'nm', x: 38, y: 27 }, trunc(title, chars(troom, size)));
    if (size !== 16) tt.style.fontSize = size + 'px';
    inn.append(tt);
    inn.append(planBadges(agg, W - 14, 14, true, bare));
    // the subtitle gives what fits, longest first
    const markW = marks.reduce((w, [, c]) => w + String(c).length * 7 + 23, 0);
    const room = chars(W - 38 - 14 - markW, 12);
    let opts;
    if (isNs) {
      const kids = it.ns.kids.length;
      const res = agg.n || !agg.out ? `${agg.n} ${agg.n === 1 ? 'resource' : 'resources'}` : '';
      const nss = kids ? `${kids} ${kids === 1 ? 'namespace' : 'namespaces'}` : '';
      const out = agg.out ? `${agg.out} ${agg.out === 1 ? 'output' : 'outputs'}` : '';
      const join = (...p) => p.filter(Boolean).join(' · ');
      opts = [...new Set([join(res, nss, out), join(res, nss), join(res, out), res || out, String(agg.n || agg.out)])];
    } else opts = it.ns.name ? [`× ${agg.n} in ${it.ns.name}`, `× ${agg.n}`] : [`× ${agg.n}`];
    inn.append(s('text', { class: 'ty', x: 38, y: 45 }, opts.find((o) => o.length <= room) || trunc(opts[opts.length - 1], room)));
    let mx = W - 14;
    for (const [m, count] of marks) {
      const t = String(count);
      mx -= t.length * 7 + 15;
      const mg = s('g', { class: 'mkc mk-' + m });
      mg.append(glyph(m, mx, 33, 13, 'mk mk-' + m), s('text', { class: 'mkn', x: mx + 16, y: 44 }, t));
      inn.append(mg);
      mx -= 8;
    }
    inn.append(this.cells(it, 14, 57, W - 28));
    // the discs of the plans that change something inside, which the resources would carry if the card were open
    const pin = plansIn(it, this.ctx);
    if (pin.overlap) inn.append(s('rect', { class: 'ovl', x: -5, y: -5, width: W + 10, height: H + 10, rx: 10.5 }));
    const aus = this.authorBadges(pin.list, W);
    if (aus) inn.append(aus);
    g.append(inn);
    return g;
  }

  buildCtl(it) {
    const W = it.w, H = it.h;
    const g = s('g', { class: 'ctl-item', 'aria-label': `${it.type} group, ${it.shown} of ${it.total} shown` });
    const inn = s('g', { class: 'in' });
    inn.append(s('rect', { class: 'box', width: W, height: H, rx: 7 }));
    const more = it.total > it.shown;
    const bw = more ? W / 2 : W;
    const b1 = s('g', { class: 'btn', 'data-act': 'collapse', role: 'button', 'aria-label': `Collapse ${it.type} group` });
    b1.append(s('rect', { class: 'hit', width: bw, height: H, rx: 7 }));
    b1.append(glyph('up', 12, 9, 18));
    b1.append(s('text', { x: 36, y: 22.5 }, `group of ${it.total}`));
    inn.append(b1);
    if (more) {
      const b2 = s('g', { class: 'btn', 'data-act': 'all', transform: `translate(${bw} 0)`, role: 'button', 'aria-label': `Show all ${it.total}` });
      b2.append(s('rect', { class: 'hit', width: bw, height: H, rx: 7 }));
      b2.append(glyph('plus', 12, 9, 18));
      b2.append(s('text', { x: 36, y: 22.5 }, `show ${it.total - it.shown} more`));
      b2.append(s('line', { class: 'sep', x1: 0, y1: 8, x2: 0, y2: H - 8 }));
      inn.append(b2);
    }
    g.append(inn);
    return g;
  }

  createLane(L, ns) {
    const g = s('g', { class: 'lane', 'data-lane': L.path });
    const rect = s('rect', { class: 'band', rx: L.depth > 1 ? 9 : 12 });
    g.append(rect);
    const hg = s('g', { class: 'lane-head', 'data-lane': L.path });
    const hit = s('rect', { class: 'hit', height: G.HEAD });
    const head = s('g', { class: 'head' });
    const lbl = s('text', { class: 'bandl', x: 20, y: 11 });
    const cnt = s('text', { class: 'bandc', x: 0, y: 11 });
    const hs = s('g', { class: 'hs' });
    hs.append(glyph('chevD', 0, 0, 16, 'chv'), lbl, cnt, s('g', { class: 'lpb' }));
    head.append(hs);
    hg.append(hit, head);
    this.headLayer.append(hg);
    const r = { g, hg, rect, hit, head, hs, lbl, cnt, box: null, leaving: false };
    this.updateLane(r, L, ns);
    return r;
  }

  updateLane(r, L, ns) {
    const label = L.label || '';
    r.lbl.textContent = label;
    const agg = ns ? ns.agg : null;
    // a lane that holds outputs only counts them
    const count = agg ? (agg.n || !agg.out ? String(agg.n) : agg.out + ' out') : '';
    r.cnt.textContent = count;
    const w = label.length * 8.2 + 8;
    r.cnt.setAttribute('x', 20 + w + 6);
    const pb = r.head.querySelector('.lpb');
    while (pb.firstChild) pb.removeChild(pb.firstChild);
    const cw = agg ? count.length * 7.4 + 6 : 0;
    if (agg && agg.plan) pb.append(planBadges(agg, 20 + w + 6 + cw + 10, 1, false));
    // how far the title may be scaled up before it outgrows the lane
    const textW = 20 + w + 6 + cw + 6;
    r.hg.style.setProperty('--cap', Math.max(1, (L.w - 28) / textW).toFixed(2));
    r.label = label;
  }

  createEdge(e) {
    const path = s('path', { class: 'ge', 'data-key': e.key });
    return { path, label: null, pts: null, e, hot: null };
  }

  updateEdgeLabel(r, e) {
    r.path.setAttribute('stroke-width', e.n > 1 ? (1.2 + Math.min(e.n, 8) * 0.22).toFixed(2) : '');
    if (e.n > 1) {
      if (!r.label) {
        r.label = s('g', { class: 'ec' });
        r.label.append(s('rect', { x: -10, y: -9, width: 20, height: 18, rx: 9 }), s('text', { x: 0, y: 3.7, 'text-anchor': 'middle' }));
        this.labelLayer.append(r.label);
      }
      r.label.querySelector('text').textContent = e.n;
      const wd = 14 + String(e.n).length * 6.6;
      const rc = r.label.querySelector('rect');
      rc.setAttribute('width', wd); rc.setAttribute('x', -wd / 2);
    } else if (r.label) { r.label.remove(); r.label = null; }
  }

  // ---- state classes ----

  restyleAll() {
    const sel = this.selection;
    const has = !!(sel && (sel.ids.size || sel.lane));
    this.svg.classList.toggle('has-sel', has);
    this.host.classList.toggle('has-sel', has);
    for (const [id, r] of this.items) this.styleItem(id, r, sel);
    for (const [path, r] of this.lanes) { const on = !!(sel && sel.lane === path); r.g.classList.toggle('sel', on); r.hg.classList.toggle('sel', on); }
    let flowing = 0;
    for (const [key, r] of this.edges) {
      const up = !!(sel && sel.upE.has(key)), down = !!(sel && sel.downE.has(key));
      r.path.classList.toggle('up', up);
      r.path.classList.toggle('down', down);
      if (up || down) flowing++;
      if (r.label) { r.label.classList.toggle('up', up); r.label.classList.toggle('down', down); }
    }
    this.svg.classList.toggle('flow-calm', flowing > 60);
    if (this.match) this.applyMatch(this.match);
    if (this.cb.onStyled) this.cb.onStyled();
  }

  styleItem(id, r, sel) {
    const c = r.g.classList;
    const isSel = !!(sel && sel.ids.has(id));
    const up = !!(sel && sel.up.has(id)), down = !!(sel && sel.down.has(id));
    // what stands inside a selected lane is as much part of the selection as the lane
    const inside = !!(sel && sel.lane && r.it && r.it.lane && (r.it.lane === sel.lane || r.it.lane.startsWith(sel.lane + '.')));
    c.toggle('sel', isSel);
    c.toggle('up', up && !isSel);
    c.toggle('down', down && !isSel);
    c.toggle('rel', isSel || up || down || inside);
    c.toggle('reach', !!(sel && sel.reach && (down || isSel)));
    r.g.setAttribute('aria-pressed', isSel ? 'true' : 'false');
  }

  setSelection(sel) {
    this.selection = sel;
    this.svg.classList.toggle('reach-mode', !!(sel && sel.reach));
    // the edges of a selection flow for a while, then stand still (see .flowing in graph.css)
    this.svg.classList.add('flowing');
    clearTimeout(this._flowT);
    this._flowT = setTimeout(() => this.svg.classList.remove('flowing'), 8000);
    this.restyleAll();
    this.clearFx();
    if (sel && sel.pulse && !this.reduced) this.ripple(sel);
  }

  // setMatch dims what a filter or a text search leaves out. match is a Set of item ids, or null.
  setMatch(match) { this.match = match; this.applyMatch(match); }
  applyMatch(match) {
    this.svg.classList.toggle('has-match', !!match);
    for (const [id, r] of this.items) r.g.classList.toggle('nomatch', !!match && !match.has(id));
  }

  clearFx() {
    clearTimeout(this._fxT);
    while (this.fxLayer.firstChild) this.fxLayer.removeChild(this.fxLayer.firstChild);
  }

  // ripple: a halo around the selection and a pulse that travels along each reached edge, delayed by its depth
  ripple(sel) {
    const mk = (map, cls) => {
      let n = 0;
      for (const [key, depth] of map) {
        const r = this.edges.get(key);
        if (!r || r.leaving) continue;
        if (++n > 120) break;
        const p = s('path', { class: 'pulse ' + cls, pathLength: 1, d: pathOf(r.pts) });
        p.style.animationDelay = `${(depth - 1) * 120 + 60}ms`;
        this.fxLayer.append(p);
      }
    };
    mk(sel.upE, 'up');
    mk(sel.downE, 'down');
    for (const id of sel.ids) {
      const r = this.items.get(id);
      if (!r) continue;
      const w = r.it.w, hh = r.it.h;
      this.fxLayer.append(s('rect', { class: 'halo-ripple', x: r.x - 3, y: r.y - 3, width: w + 6, height: hh + 6, rx: 10 }));
    }
    this._fxT = setTimeout(() => this.clearFx(), 2600);
  }

  // pulseRings lets the rings of the plan and of drift pulse for a while (see .pulsing in graph.css)
  pulseRings(ms = 9000) {
    this.svg.classList.add('pulsing');
    clearTimeout(this._pulseT);
    this._pulseT = setTimeout(() => this.svg.classList.remove('pulsing'), ms);
  }

  flash(ids) {
    if (this.reduced) return;
    if (ids.size) this.pulseRings();
    for (const id of ids) {
      const r = this.items.get(id);
      if (!r) continue;
      r.g.classList.remove('flash');
      void r.g.getBoundingClientRect();
      r.g.classList.add('flash');
      setTimeout(() => r.g.classList.remove('flash'), 1700);
    }
  }

  setFocus(id) {
    if (this._kf) { const o = this.items.get(this._kf); if (o) o.g.classList.remove('kf'); }
    this._kf = id;
    const r = id && this.items.get(id);
    if (r) r.g.classList.add('kf');
    // the card that a hover opens is also for the keyboard: it opens when the camera has come to rest on the
    // item that has the focus, and goes when the focus moves
    clearTimeout(this._kfT);
    if (this._tipHide) this._tipHide();
    if (!r) return;
    let tries = 0;
    const show = () => {
      if (this._kf !== id) return;
      if ((this.camTween || this.tween || this.glideRaf) && tries++ < 12) { this._kfT = setTimeout(show, 120); return; }
      if (!this.dragging) this.showTip(id);
    };
    this._kfT = setTimeout(show, 280);
  }

  // ---- hover: the edges of the item light up, and after a moment a card says what it is ----

  bindHover() {
    let cur = null, timer = 0;
    const clearTip = () => { clearTimeout(timer); this.tip.hidden = true; this.tip.classList.remove('in'); };
    const set = (id) => {
      if (id === cur) return;
      if (cur) {
        const lr = cur.startsWith('lane:') ? this.lanes.get(cur.slice(5)) : null;
        if (lr) lr.g.classList.remove('hov');
        const r = this.items.get(cur);
        if (r) r.g.classList.remove('hov');
        for (const k of this.adj.get(cur) || []) { const e = this.edges.get(k); if (e) e.path.classList.remove('hl'); }
        this.svg.classList.remove('hovering');
      }
      clearTip();
      cur = id;
      this.hoverId = id;
      if (cur) {
        const lr = cur.startsWith('lane:') ? this.lanes.get(cur.slice(5)) : null;
        if (lr) lr.g.classList.add('hov');
        const r = this.items.get(cur);
        if (r) r.g.classList.add('hov');
        for (const k of this.adj.get(cur) || []) { const e = this.edges.get(k); if (e) e.path.classList.add('hl'); }
        this.svg.classList.add('hovering');
        const want = cur;
        timer = setTimeout(() => { if (cur === want && !this.dragging) this.showTip(want); }, 340);
      }
    };
    this.svg.addEventListener('pointerover', (ev) => {
      if (this.dragging) return;
      const t = ev.target.closest && ev.target.closest('[data-id],[data-lane]');
      if (!t) { set(null); return; }
      if (t.dataset.id) set(t.dataset.id);
      else if (ev.target.closest('.hit')) set('lane:' + t.dataset.lane);
      else set(null);
    });
    this.svg.addEventListener('pointerleave', () => set(null));
    this._hoverReset = () => set(null);
    this._tipHide = clearTip;
  }

  showTip(id) {
    let content, box;
    if (id.startsWith('lane:')) {
      const path = id.slice(5);
      const L = this.lay && this.lay.lanes.find((l) => l.path === path);
      const ns = this.cb.namespaceOf(path);
      if (!L || !ns) return;
      box = { x: L.x, y: L.y, w: L.w, h: Math.min(L.h, G.HEAD) };
      content = this.tipNamespace(ns, true);
    } else {
      const it = this.vis && this.vis.items.get(id);
      const p = this.lay && this.lay.pos.get(id);
      if (!it || !p) return;
      box = p;
      if (it.kind === 'node') content = this.tipNode(it.n);
      else if (it.kind === 'namespace') content = this.tipNamespace(it.ns, false);
      else if (it.kind === 'cluster') content = this.tipCluster(it);
      else return;
    }
    const tip = this.tip;
    while (tip.firstChild) tip.removeChild(tip.firstChild);
    tip.append(...content);
    tip.hidden = false;
    const { x, y, k } = this.view;
    const sx = box.x * k + x, sy = box.y * k + y, sw = box.w * k, sh = box.h * k;
    const tw = tip.offsetWidth, th = tip.offsetHeight, W = this.size.w, Hh = this.size.h;
    let left = sx + sw + 12, top = sy + sh / 2 - th / 2;
    if (left + tw > W - 8) left = sx - tw - 12;
    if (left < 8) { left = clamp(sx + sw / 2 - tw / 2, 8, W - tw - 8); top = sy + sh + 10; if (top + th > Hh - 8) top = sy - th - 10; }
    tip.style.transform = `translate(${Math.round(left)}px, ${Math.round(clamp(top, 8, Hh - th - 8))}px)`;
    tip.classList.add('in');
  }

  tipNode(n) {
    const isO = isOut(n);
    const out = [h('div', { class: 'gtip-addr' }, n.addr), h('div', { class: 'gtip-type' }, isO ? 'output' : (n.mode && n.mode !== 'managed' ? n.mode + ' · ' : '') + n.type)];
    const chips = [];
    const a = actionOf(n.action);
    if (a && n.action !== 'no-op') chips.push(h('span', { class: 'gchip ' + a.cls }, a.label));
    else if (this.ctx.hasPlan && !n.action) chips.push(h('span', { class: 'gchip skip' }, 'not read'));
    if (n.sensitive > 0) chips.push(h('span', { class: 'gchip sens' }, isO ? 'sensitive' : `${n.sensitive} sensitive`));
    if (n.gone) chips.push(h('span', { class: 'gchip gone' }, 'gone'));
    else if (n.drifted) chips.push(h('span', { class: 'gchip drift' }, 'drifted'));
    if (n.locked) chips.push(h('span', { class: 'gchip hold' }, 'locked'));
    if (n.presence === 'state') chips.push(h('span', { class: 'gchip gone' }, 'orphan'));
    if (n.presence === 'spec') chips.push(h('span', { class: 'gchip skip' }, isO ? 'not published yet' : 'declared only'));
    if (n.overlap) chips.push(h('span', { class: 'gchip ovl' }, 'overlap'));
    if (chips.length) out.push(h('div', { class: 'gtip-row' }, chips));
    const authors = authorsOf(n, this.ctx);
    if (authors.length) out.push(h('div', { class: 'gtip-plans' }, authors.map((p) => h('div', null, h('span', { class: 'au au' + hueOf(p.author) }, initials(p.author)), ` ${p.author} · ${p.id}`)), h('div', { class: 'dim' }, 'Click a disc to open its plan')));
    out.push(h('div', { class: 'gtip-meta' }, isO ? `reads ${n.deps.length} · read by ${n.used.length}` : `${n.deps.length} ${n.deps.length === 1 ? 'dependency' : 'dependencies'} · ${n.used.length} ${n.used.length === 1 ? 'dependent' : 'dependents'}`));
    return out;
  }

  // tipPlans names the open plans that change something inside a card, and how many resources they change
  tipPlans(out, pin) {
    if (!pin.list.length) return;
    out.push(h('div', { class: 'gtip-plans' }, pin.list.map((p) => h('div', null, h('span', { class: 'au au' + hueOf(p.author) }, initials(p.author)), ` ${p.author} \u00b7 ${p.id}`)),
      h('div', { class: 'dim' }, `${plural(pin.resources, 'resource')} changed by open plans${pin.overlap ? ', two of them by more than one' : ''}`), h('div', { class: 'dim' }, 'Click a disc to open its plan')));
  }

  tipNamespace(ns, open) {
    const a = ns.agg;
    const outs = a.out ? `${a.out} ${a.out === 1 ? 'output' : 'outputs'}` : '';
    const out = [h('div', { class: 'gtip-addr' }, ns.path), h('div', { class: 'gtip-type' }, [a.n || !outs ? `${a.n} ${a.n === 1 ? 'resource' : 'resources'}` : '', ns.kids.length ? `${ns.kids.length} ${ns.kids.length === 1 ? 'namespace' : 'namespaces'}` : '', outs].filter(Boolean).join(' · '))];
    const chips = [];
    if (a.create) chips.push(h('span', { class: 'gchip a-create' }, `${a.create} create`));
    if (a.update + a.replace) chips.push(h('span', { class: 'gchip a-update' }, `${a.update + a.replace} change`));
    if (a.delete) chips.push(h('span', { class: 'gchip a-delete' }, `${a.delete} delete`));
    if (a.sensitive) chips.push(h('span', { class: 'gchip sens' }, `${a.sensitive} sensitive`));
    if (a.drifted + a.gone) chips.push(h('span', { class: 'gchip drift' }, `${a.drifted + a.gone} drifted`));
    if (chips.length) out.push(h('div', { class: 'gtip-row' }, chips));
    this.tipPlans(out, plansIn({ kind: 'namespace', ns }, this.ctx));
    out.push(h('div', { class: 'gtip-meta' }, open ? 'click to select, chevron to close' : 'click or press Enter to open'));
    return out;
  }

  tipCluster(it) {
    const a = it.agg;
    const out = [h('div', { class: 'gtip-addr' }, it.mode === 'output' ? 'outputs' : it.type), h('div', { class: 'gtip-type' }, `${a.n} ${it.mode === 'output' ? 'outputs' : 'resources'}` + (it.ns.name ? ` in ${it.ns.name}` : ''))];
    const chips = [];
    if (a.create) chips.push(h('span', { class: 'gchip a-create' }, `${a.create} create`));
    if (a.update + a.replace) chips.push(h('span', { class: 'gchip a-update' }, `${a.update + a.replace} change`));
    if (a.delete) chips.push(h('span', { class: 'gchip a-delete' }, `${a.delete} delete`));
    if (a.sensitive) chips.push(h('span', { class: 'gchip sens' }, `${a.sensitive} sensitive`));
    if (chips.length) out.push(h('div', { class: 'gtip-row' }, chips));
    this.tipPlans(out, plansIn(it, this.ctx));
    out.push(h('div', { class: 'gtip-meta' }, 'click or press Enter to show the group'));
    return out;
  }

  // ---- zoom: what shows depends on how large it is on the screen ----

  updateLod(force) {
    const k = this.view.k;
    const q = (v, n) => Math.round(v * n) / n;
    const lab = q(clamp((k - 0.3) / 0.22, 0, 1), 16);
    const sub = q(clamp((k - 0.5) / 0.2, 0, 1), 16);
    const ls = q(clamp(11.5 / (12.5 * k), 1, 9), 20);
    const far = k < 0.3 ? 1 : 0;
    const key = `${lab}|${sub}|${ls}|${far}`;
    if (key === this.lodKey && !force) return;
    this.lodKey = key;
    this.svg.style.setProperty('--lab', String(lab));
    this.svg.style.setProperty('--sub', String(sub));
    this.laneLayer.style.setProperty('--ls', String(ls));
    this.svg.classList.toggle('lod-far', !!far);
    this.svg.classList.toggle('lod-low', lab < 1);
  }

  // ---- camera ----

  applyView() {
    schedule('graph-view', () => this.paintView());
  }

  paintView() {
    const { x, y, k } = this.view;
    this.vp.style.transform = `translate(${x.toFixed(2)}px, ${y.toFixed(2)}px) scale(${k.toFixed(4)})`;
    const gs = (() => { let g = 24 * k; while (g < 16) g *= 2; while (g > 48) g /= 2; return g; })();
    this.host.style.setProperty('--gs', gs.toFixed(2) + 'px');
    this.host.style.setProperty('--gx', (x % gs).toFixed(2) + 'px');
    this.host.style.setProperty('--gy', (y % gs).toFixed(2) + 'px');
    this.updateLod(false);
    if (this._tipHide && !this.tip.hidden) this._tipHide();
    if (this.cb.onView) this.cb.onView();
  }

  // setView moves the camera; animated, the point at the centre of the screen glides and the zoom changes
  // geometrically, so that the picture neither sweeps nor lingers.
  setView(v, animate) {
    this.cancelCam();
    if (!animate || this.reduced) { this.view = { x: v.x, y: v.y, k: v.k }; this.paintView(); return; }
    const from = Object.assign({}, this.view), t0 = performance.now();
    const W = this.size.w, Hh = this.size.h;
    const c0 = { x: (W / 2 - from.x) / from.k, y: (Hh / 2 - from.y) / from.k }, c1 = { x: (W / 2 - v.x) / v.k, y: (Hh / 2 - v.y) / v.k };
    const dist = Math.hypot(c1.x - c0.x, c1.y - c0.y) * Math.max(from.k, v.k) / Math.max(W, 1);
    const dur = clamp(380 + 200 * Math.min(dist, 1.5) + 140 * Math.min(Math.abs(Math.log(v.k / from.k)), 1.2), 380, 760);
    const step = (now) => {
      const t = clamp((now - t0) / dur, 0, 1), e = ease(t);
      const k = from.k * Math.pow(v.k / from.k, e);
      const cx = c0.x + (c1.x - c0.x) * e, cy = c0.y + (c1.y - c0.y) * e;
      this.view = { x: W / 2 - cx * k, y: Hh / 2 - cy * k, k };
      this.paintView();
      if (t < 1) this.camTween = requestAnimationFrame(step); else { this.camTween = null; this.view = { x: v.x, y: v.y, k: v.k }; this.paintView(); }
    };
    this.camTween = requestAnimationFrame(step);
  }
  cancelCam() {
    if (this.camTween) { cancelAnimationFrame(this.camTween); this.camTween = null; }
    if (this.glideRaf) { cancelAnimationFrame(this.glideRaf); this.glideRaf = 0; }
    this.goal = null;
  }

  // zoomGlide eases the zoom towards a goal that may move on the way, about the cursor: the wheel feeds it
  zoomGlide(f, ax, ay) {
    const base = this.goal ? this.goal.k : this.view.k;
    const k = clamp(base * f, 0.03, 3);
    this.userMoved = true;
    if (this.reduced) {
      const r = k / this.view.k;
      this.view = { x: ax - (ax - this.view.x) * r, y: ay - (ay - this.view.y) * r, k };
      this.paintView();
      return;
    }
    this.goal = { k, ax, ay };
    if (this.glideRaf) return;
    let last = performance.now();
    const step = (now) => {
      const g = this.goal;
      if (!g) { this.glideRaf = 0; return; }
      const dt = Math.min(64, now - last);
      last = now;
      const a = 1 - Math.exp(-dt / 70);
      const v = this.view;
      const done = Math.abs(Math.log(g.k / v.k)) < 0.003;
      const nk = done ? g.k : Math.exp(Math.log(v.k) + (Math.log(g.k) - Math.log(v.k)) * a);
      const r = nk / v.k;
      this.view = { x: g.ax - (g.ax - v.x) * r, y: g.ay - (g.ay - v.y) * r, k: nk };
      this.paintView();
      if (done) { this.goal = null; this.glideRaf = 0; return; }
      this.glideRaf = requestAnimationFrame(step);
    };
    this.glideRaf = requestAnimationFrame(step);
  }

  boundsOf(ids) {
    if (!this.lay) return null;
    if (!ids) return this.lay.bounds;
    let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
    for (const id of ids) {
      const p = this.lay.pos.get(id);
      if (!p) continue;
      x0 = Math.min(x0, p.x); y0 = Math.min(y0, p.y); x1 = Math.max(x1, p.x + p.w); y1 = Math.max(y1, p.y + p.h);
    }
    return isFinite(x0) ? { x: x0, y: y0, w: x1 - x0, h: y1 - y0 } : null;
  }

  // frame brings a box of the picture to the middle of the canvas as large as it will go, up to maxK
  frame(b, animate = true, maxK = FIT.maxK) {
    if (!b || !this.size.w) return;
    const k = fitScale(b, this.size.w, this.size.h, maxK);
    const cx = b.x + b.w / 2, cy = b.y + b.h / 2;
    const midY = FIT.padTop + (this.size.h - FIT.padTop - FIT.padBottom) / 2;
    this.setView({ x: this.size.w / 2 - cx * k, y: midY - cy * k, k }, animate);
  }

  fit(animate = true, ids = null, maxK = FIT.maxK) {
    const b = this.boundsOf(ids);
    if (!b) return;
    this.userMoved = false;
    this.frame(b, animate, maxK);
  }

  // zoomBy: about a point of the screen, at once or as a tween
  zoomBy(f, cx = this.size.w / 2, cy = this.size.h / 2, animate = false) {
    const base = this.view;
    const k = clamp(base.k * f, 0.03, 3);
    const r = k / base.k;
    this.userMoved = true;
    this.setView({ x: cx - (cx - base.x) * r, y: cy - (cy - base.y) * r, k }, animate);
  }

  // centerOn takes the camera to an item at a scale that can be read: not less than minK, not more than maxK
  centerOn(id, minK = 0.95, animate = true, maxK = 1.4) {
    const p = this.lay && this.lay.pos.get(id);
    if (!p) return;
    const k = clamp(this.view.k, minK, maxK);
    this.userMoved = true;
    const midY = FIT.padTop + (this.size.h - FIT.padTop - FIT.padBottom) / 2;
    this.setView({ x: this.size.w / 2 - (p.x + p.w / 2) * k, y: midY - (p.y + p.h / 2) * k, k }, animate);
  }

  centerOnBox(b, animate = true) {
    if (!b) return;
    this.userMoved = true;
    this.frame(b, animate, 1.2);
  }

  // is the item inside the visible part of the canvas?
  visible(id, margin = 40) {
    const p = this.lay && this.lay.pos.get(id);
    if (!p) return false;
    const { x, y, k } = this.view;
    const sx = p.x * k + x, sy = p.y * k + y, sw = p.w * k, sh = p.h * k;
    return sx > margin - sw && sy > margin - sh && sx < this.size.w - margin && sy < this.size.h - margin;
  }

  // ---- pointer: pan, pinch, click ----

  bindPointer() {
    const el = this.host;
    const pts = new Map();
    let down = null, pinch = null;
    el.addEventListener('wheel', (ev) => {
      ev.preventDefault();
      if (this.camTween) { cancelAnimationFrame(this.camTween); this.camTween = null; }
      const r = el.getBoundingClientRect();
      if (!ev.ctrlKey && (Math.abs(ev.deltaX) > Math.abs(ev.deltaY) * 1.2 || ev.shiftKey)) {
        this.cancelCam();
        this.userMoved = true;
        this.view.x -= ev.shiftKey && !ev.deltaX ? ev.deltaY : ev.deltaX;
        this.applyView();
        return;
      }
      const unit = ev.deltaMode === 1 ? 16 : ev.deltaMode === 2 ? 400 : 1;
      const f = Math.exp(-ev.deltaY * unit * (ev.ctrlKey ? 0.012 : 0.0016));
      this.zoomGlide(f, ev.clientX - r.left, ev.clientY - r.top);
    }, { passive: false });

    el.addEventListener('pointerdown', (ev) => {
      if (ev.button !== 0 && ev.pointerType === 'mouse') return;
      if (ev.target.closest && ev.target.closest('.overlay')) return;
      pts.set(ev.pointerId, { x: ev.clientX, y: ev.clientY });
      this.cancelCam();
      if (this._tipHide) this._tipHide();
      if (pts.size === 1) {
        down = { x: ev.clientX, y: ev.clientY, vx: this.view.x, vy: this.view.y, target: ev.target, moved: false, t: performance.now() };
      } else if (pts.size === 2) {
        const [a, b] = [...pts.values()];
        pinch = { d: Math.hypot(a.x - b.x, a.y - b.y), k: this.view.k, cx: (a.x + b.x) / 2, cy: (a.y + b.y) / 2, vx: this.view.x, vy: this.view.y };
        if (down) down.moved = true;
      }
    });
    const move = (ev) => {
      if (!pts.has(ev.pointerId)) return;
      pts.set(ev.pointerId, { x: ev.clientX, y: ev.clientY });
      if (pinch && pts.size >= 2) {
        const [a, b] = [...pts.values()];
        const d = Math.hypot(a.x - b.x, a.y - b.y) || 1;
        const r = el.getBoundingClientRect();
        const k = clamp(pinch.k * (d / pinch.d), 0.03, 3);
        const mx = (a.x + b.x) / 2 - r.left, my = (a.y + b.y) / 2 - r.top;
        const f = k / pinch.k;
        this.userMoved = true;
        this.view = { k, x: mx - (pinch.cx - r.left - pinch.vx) * f, y: my - (pinch.cy - r.top - pinch.vy) * f };
        this.applyView();
        return;
      }
      if (!down) return;
      const dx = ev.clientX - down.x, dy = ev.clientY - down.y;
      if (!down.moved && Math.hypot(dx, dy) > 4) {
        down.moved = true;
        this.dragging = true;
        el.classList.add('panning');
        try { el.setPointerCapture(ev.pointerId); } catch { /* the pointer is gone */ }
        if (this._hoverReset) this._hoverReset();
      }
      if (down.moved) {
        this.userMoved = true;
        this.view.x = down.vx + dx;
        this.view.y = down.vy + dy;
        this.applyView();
      }
    };
    el.addEventListener('pointermove', move);
    const up = (ev) => {
      if (!pts.has(ev.pointerId)) return;
      pts.delete(ev.pointerId);
      if (pts.size < 2) pinch = null;
      if (pts.size === 0 && down) {
        const d = down;
        down = null;
        el.classList.remove('panning');
        this.dragging = false;
        if (!d.moved && ev.type === 'pointerup') this.click(d.target, ev);
      }
    };
    el.addEventListener('pointerup', up);
    el.addEventListener('pointercancel', up);
    el.addEventListener('dblclick', (ev) => {
      if (performance.now() < this.guardUntil) return;
      const t = ev.target.closest && ev.target.closest('[data-id],[data-lane]');
      if (!t) return;
      if (t.dataset.id) { const it = this.vis && this.vis.items.get(t.dataset.id); if (it && it.kind === 'namespace') this.cb.onOpenNamespace(it.ns.path); }
      else if (t.dataset.lane) this.cb.onToggleNamespace(t.dataset.lane);
    });
  }

  click(target, ev) {
    if (performance.now() < this.guardUntil) return;
    // a disc of an author is a way into the plan that it stands for
    const disc = target.closest && target.closest('[data-plan]');
    if (disc) { this.cb.onPlan(disc.dataset.plan); return; }
    // the kind and the provider of a card filter the graph
    const fx = target.closest && target.closest('[data-facet]');
    if (fx && this.cb.onFacet) {
      const i = fx.dataset.facet.indexOf(':');
      this.cb.onFacet(fx.dataset.facet.slice(0, i), fx.dataset.facet.slice(i + 1), !!(ev && ev.shiftKey));
      return;
    }
    const act = target.closest && target.closest('[data-act]');
    const el = target.closest && target.closest('[data-id]');
    if (act && el) {
      const it = this.vis.items.get(el.dataset.id);
      if (it && it.kind === 'ctl') { this.cb.onCluster(it.cluster, act.dataset.act); return; }
    }
    if (el) {
      const it = this.vis.items.get(el.dataset.id);
      if (!it) return;
      if (it.kind === 'cluster') { this.cb.onCluster(it.id, 'open'); return; }
      if (it.kind === 'namespace') { this.cb.onOpenNamespace(it.ns.path, ev); return; }
      if (it.kind === 'node') { this.cb.onSelectNode(it.n.addr, ev); return; }
      return;
    }
    const lane = target.closest && target.closest('[data-lane]');
    if (lane && target.closest('.hit')) {
      // a click on the header selects the namespace; a click on its chevron closes it
      const r = this.host.getBoundingClientRect();
      const wx = (ev.clientX - r.left - this.view.x) / this.view.k;
      const L = this.lay.lanes.find((l) => l.path === lane.dataset.lane);
      const ls = Math.min(parseFloat(this.laneLayer.style.getPropertyValue('--ls')) || 1, parseFloat(lane.style.getPropertyValue('--cap')) || 1);
      if (L && wx < L.x + 14 + 24 * ls) { this.cb.onToggleNamespace(lane.dataset.lane); return; }
      this.cb.onSelectNamespace(lane.dataset.lane, ev);
      return;
    }
    this.cb.onClear();
  }

  destroy() { this.ro.disconnect(); this.stopTween(); this.cancelCam(); }
}

function clonePts(r) { return { m: r.m, p: r.p.slice() }; }

function mixPts(a, b, e) {
  const n = Math.max(a.p.length, b.p.length);
  const A = a.p.length === n ? a.p : stretch(a.p, n), B = b.p.length === n ? b.p : stretch(b.p, n);
  const out = new Array(n);
  for (let i = 0; i < n; i++) out[i] = A[i] + (B[i] - A[i]) * e;
  return { m: e < 0.5 ? a.m : b.m, p: out };
}
function stretch(p, n) {
  const m = p.length / 2, N = n / 2;
  const out = [];
  for (let i = 0; i < N; i++) {
    const j = Math.min(m - 1, Math.round((i * (m - 1)) / Math.max(1, N - 1)));
    out.push(p[j * 2], p[j * 2 + 1]);
  }
  return out;
}

// ---- minimap ----

export class Minimap {
  constructor(host, gv) {
    this.gv = gv;
    this.W = 184;
    this.Hh = 120;
    this.canvas = h('canvas', { class: 'mm-canvas', width: this.W, height: this.Hh, 'aria-hidden': 'true' });
    this.root = h('div', { class: 'minimap overlay off', 'aria-hidden': 'true' }, this.canvas);
    host.append(this.root);
    this.colors = null;
    this.enabled = true;
    const goto = (ev) => {
      const r = this.canvas.getBoundingClientRect();
      const f = this.frame;
      if (!f) return;
      const wx = f.x0 + ((ev.clientX - r.left) / r.width) * f.w;
      const wy = f.y0 + ((ev.clientY - r.top) / r.height) * f.h;
      const { k } = gv.view;
      gv.userMoved = true;
      gv.setView({ x: gv.size.w / 2 - wx * k, y: gv.size.h / 2 - wy * k, k }, false);
    };
    let drag = false;
    this.canvas.addEventListener('pointerdown', (ev) => { drag = true; this.canvas.setPointerCapture(ev.pointerId); goto(ev); ev.stopPropagation(); });
    this.canvas.addEventListener('pointermove', (ev) => { if (drag) goto(ev); });
    this.canvas.addEventListener('pointerup', () => { drag = false; });
  }

  // show(false) keeps the map away altogether; otherwise it appears when the picture is larger than the screen
  show(v) { this.enabled = !!v; if (!v) this.root.classList.add('off'); }

  refreshColors() {
    const cs = getComputedStyle(this.canvas);
    const g = (n) => cs.getPropertyValue(n).trim() || '#888';
    this.colors = { line: g('--line-strong'), text: g('--text-3'), accent: g('--accent'), change: g('--change'), conflict: g('--conflict'), key: g('--key'), overlap: g('--overlap'), surface: g('--surface-3'), pvh: [0, 1, 2, 3, 4, 5].map((i) => parseFloat(g('--pvh' + i)) || 232), lane: g('--mm-lane') || '60% 62%' };
  }

  draw() {
    const gv = this.gv;
    if (!gv.lay || !this.enabled) return;
    if (!this.colors) this.refreshColors();
    const c = this.canvas, ctx = c.getContext('2d');
    const dpr = Math.min(2, window.devicePixelRatio || 1);
    const W = this.W, Hh = this.Hh;
    if (c.width !== W * dpr) { c.width = W * dpr; c.height = Hh * dpr; c.style.width = W + 'px'; c.style.height = Hh + 'px'; }
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, W, Hh);
    const b = gv.lay.bounds;
    const pad = 8;
    const f = Math.min((W - pad * 2) / b.w, (Hh - pad * 2) / b.h);
    const ox = (W - b.w * f) / 2 - b.x * f, oy = (Hh - b.h * f) / 2 - b.y * f;
    this.frame = { x0: (0 - ox) / f, y0: (0 - oy) / f, w: W / f, h: Hh / f };
    const col = this.colors;
    // the viewport, in the units of the picture
    const { x, y, k } = gv.view;
    const vx = (-x / k) * f + ox, vy = (-y / k) * f + oy, vw = (gv.size.w / k) * f, vh = (gv.size.h / k) * f;
    // the map is only of use when the picture is larger than the screen
    const whole = (-x / k) <= b.x + 4 && (-y / k) <= b.y + 4 && (gv.size.w - x) / k >= b.x + b.w - 4 && (gv.size.h - y) / k >= b.y + b.h - 4;
    this.root.classList.toggle('off', whole);
    const [ls, ll] = col.lane.split(' ').map((v) => parseFloat(v));
    for (const L of gv.lay.lanes) {
      const hue = L.look ? col.pvh[L.look.hue] + (L.look.alt ? 16 : 0) : 220;
      const sat = L.look ? ls : 6;
      ctx.fillStyle = `hsla(${hue}, ${sat}%, ${ll}%, ${L.depth ? 0.1 : 0.14})`;
      roundRect(ctx, L.x * f + ox, L.y * f + oy, Math.max(1.5, L.w * f), Math.max(1.5, L.h * f), 2);
      ctx.fill();
      ctx.strokeStyle = `hsla(${hue}, ${sat}%, ${ll}%, .45)`;
      ctx.lineWidth = 1;
      ctx.stroke();
    }
    for (const [id, p] of gv.lay.pos) {
      const it = gv.vis.items.get(id);
      let fill = col.text, a = 0.5;
      if (it && it.kind === 'node') {
        const act = it.n.action;
        if (it.n.overlap) { fill = col.overlap; a = 1; } else if (act === 'create') { fill = col.accent; a = 1; } else if (act === 'update' || act === 'replace') { fill = col.change; a = 1; } else if (act === 'delete') { fill = col.conflict; a = 1; } else if (it.n.drifted || it.n.gone) { fill = col.change; a = 0.9; }
      } else if (it) { fill = col.key; a = 0.85; }
      ctx.fillStyle = fill;
      ctx.globalAlpha = a;
      ctx.fillRect(p.x * f + ox, p.y * f + oy, Math.max(1.5, p.w * f), Math.max(1.5, p.h * f));
    }
    ctx.globalAlpha = 1;
    ctx.fillStyle = col.key;
    ctx.globalAlpha = 0.1;
    ctx.fillRect(vx, vy, vw, vh);
    ctx.globalAlpha = 1;
    ctx.strokeStyle = col.key;
    ctx.lineWidth = 1.5;
    ctx.strokeRect(vx + 0.5, vy + 0.5, vw, vh);
  }
}

function roundRect(ctx, x, y, w, hh, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + hh, r);
  ctx.arcTo(x + w, y + hh, x, y + hh, r);
  ctx.arcTo(x, y + hh, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}
