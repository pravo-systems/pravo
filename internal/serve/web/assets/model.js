// The graph held by the page: indexes over the answer of /api/graph, the items that are visible for a
// given set of collapsed modules and open clusters, and the diff of two graphs. No DOM.

import { G, FIT, fitScale, layout } from './layout.js';
import { setProviders } from './provider.js';

export const CLUSTER_MIN = 8;
export const CLUSTER_CAP = 200;

// isOut says that a node is an output of the root. An output is not a resource: it has no record, and the counts
// of resources, of the chips and of the modules leave it out.
export const isOut = (n) => n.mode === 'output';

const collator = new Intl.Collator('en', { numeric: true, sensitivity: 'base' });
export const natural = (a, b) => collator.compare(a, b);

const CHANGING = new Set(['create', 'update', 'replace', 'delete', 'move', 'import']);
export const isChange = (a) => CHANGING.has(a);

// n counts the resources of a module, out its outputs
const zero = () => ({ n: 0, out: 0, plan: 0, create: 0, update: 0, replace: 0, delete: 0, read: 0, drifted: 0, gone: 0, sensitive: 0, locked: 0, orphan: 0, spec: 0 });

// FILTERS match a node; the key is the value of filter= in the fragment. All but outputs match resources only.
export const FILTERS = {
  plan: (n) => !isOut(n) && !!n.action && n.action !== 'no-op',
  drifted: (n) => !isOut(n) && !!(n.drifted || n.gone),
  sensitive: (n) => !isOut(n) && n.sensitive > 0,
  orphans: (n) => !isOut(n) && n.presence === 'state',
  declared: (n) => !isOut(n) && n.presence === 'spec',
  outputs: (n) => isOut(n),
  env: (n) => !isOut(n) && (n.type === 'env' || n.type === 'secret_env'),
};

// The counts that a sealed record leaves out: its attributes and its marks are not read, so a zero beside sealed
// records says nothing, and the page shows a dash for it. A count above zero is shown: it is at least that many.
const SEALED_COUNTS = new Set(['drifted', 'sensitive']);
export const notCounted = (counts, key) => counts.sealed > 0 && counts[key] === 0 && SEALED_COUNTS.has(key);

// buildModel indexes a graph. plans are the stored plans (the answer of /api/plans), which the badges on the
// nodes name by id.
export function buildModel(graph, plans) {
  const g = graph;
  const nodes = new Map();
  const list = [];
  // when the specification does not load every node is "state only": that says nothing about any one node
  const specError = !!(g.project && g.project.spec_error);
  // an output has no provider: it must not take a look from the providers
  setProviders((g.nodes || []).filter((n) => n.mode !== 'output'), g.project);
  for (const raw of g.nodes || []) {
    const n = Object.assign({}, raw, { deps: [], used: [], sensitive: raw.sensitive || 0 });
    if (specError) n.presence = 'both';
    nodes.set(n.addr, n);
    list.push(n);
  }
  const root = { path: '', parent: null, name: '', depth: -1, kids: [], nodes: [], total: 0, agg: zero(), isRoot: true };
  const modules = new Map([['', root]]);
  for (const m of g.modules || []) modules.set(m.path, Object.assign({}, m, { kids: [], nodes: [], total: m.resources || 0, agg: zero() }));
  // a node whose module the list left out still gets a place
  for (const n of list) {
    const p = n.module || '';
    if (!modules.has(p)) modules.set(p, { path: p, parent: '', name: p, depth: 0, kids: [], nodes: [], total: 0, agg: zero() });
  }
  for (const m of modules.values()) {
    if (m.isRoot) continue;
    const parent = modules.get(m.parent || '') || root;
    m.up = parent;
    parent.kids.push(m);
  }
  const setDepth = (m, d) => { m.depth = d; for (const k of m.kids) setDepth(k, d + 1); };
  for (const k of root.kids) setDepth(k, 0);
  for (const n of list) {
    n.mod = modules.get(n.module || '') || root;
    n.mod.nodes.push(n);
  }
  const edges = [];
  for (const e of g.edges || []) {
    const a = nodes.get(e.dependency), b = nodes.get(e.dependent);
    if (!a || !b || a === b) continue;
    a.used.push(b.addr);
    b.deps.push(a.addr);
    edges.push({ a: a.addr, b: b.addr });
  }
  const nout = list.filter(isOut).length;
  const counts = { all: list.length - nout, plan: 0, drifted: 0, sensitive: 0, orphans: 0, declared: 0, outputs: 0, env: 0, sealed: 0 };
  for (const n of list) {
    for (const k of Object.keys(FILTERS)) if (FILTERS[k](n)) counts[k]++;
    if (n.sealed) counts.sealed++;
  }
  // aggregates, bottom-up
  const add = (agg, n) => {
    if (isOut(n)) { agg.out++; return; }
    agg.n++;
    if (n.action && n.action !== 'no-op') agg.plan++;
    if (n.action === 'create') agg.create++;
    else if (n.action === 'update') agg.update++;
    else if (n.action === 'replace') agg.replace++;
    else if (n.action === 'delete') agg.delete++;
    else if (n.action === 'read') agg.read++;
    if (n.drifted) agg.drifted++;
    if (n.gone) agg.gone++;
    if (n.sensitive > 0) agg.sensitive++;
    if (n.locked) agg.locked++;
    if (n.presence === 'state') agg.orphan++;
    if (n.presence === 'spec') agg.spec++;
  };
  const sum = (m) => {
    m.kids.sort((p, q) => natural(p.name, q.name));
    m.nodes.sort((p, q) => natural(p.name, q.name));
    for (const n of m.nodes) add(m.agg, n);
    for (const k of m.kids) {
      sum(k);
      for (const key of Object.keys(m.agg)) m.agg[key] += k.agg[key];
    }
    m.total = m.agg.n;
  };
  sum(root);
  const planned = list.filter((n) => n.action).length;
  const stored = plans || [];
  return {
    graph: g, nodes, list, modules, root, edges, counts, planned, hasPlan: !!g.plan, specError, nres: list.length - nout, nout,
    plans: stored, planById: new Map(stored.map((p) => [p.id, p])), authors: new Map(stored.map((p) => [p.id, p.author])),
  };
}

// descendants calls fn for every node below a module.
export function eachNode(mod, fn) {
  for (const n of mod.nodes) fn(n);
  for (const k of mod.kids) eachNode(k, fn);
}

// ancestors of a module, from the root's child down to the module itself.
export function chain(mod) {
  const out = [];
  for (let m = mod; m && !m.isRoot; m = m.up) out.unshift(m);
  return out;
}

export const isUnder = (mod, path) => { for (let m = mod; m; m = m.up) if (m.path === path) return true; return false; };

// ---- items ----

export const clusterId = (modPath, n) => 'c:' + modPath + '|' + n.mode + '|' + n.type;

function groupsOf(mod) {
  const groups = new Map();
  for (const n of mod.nodes) {
    const k = n.mode + '|' + n.type;
    let a = groups.get(k);
    if (!a) groups.set(k, (a = []));
    a.push(n);
  }
  return groups;
}

// a card for a module or a group is taller when it has room for two rows of cells
export const CELLS_ROW = 18;
export const CELLS_MAX = 36;
export const cardHeight = (n) => (n > CELLS_ROW ? G.MH2 : G.MH);

const aggOf = (nodes) => { const a = zero(); for (const n of nodes) { a.n++; if (isOut(n)) a.out++; if (n.action && n.action !== 'no-op') a.plan++; if (n.action === 'create') a.create++; else if (n.action === 'update') a.update++; else if (n.action === 'replace') a.replace++; else if (n.action === 'delete') a.delete++; else if (n.action === 'read') a.read++; if (n.drifted) a.drifted++; if (n.gone) a.gone++; if (n.sensitive > 0) a.sensitive++; if (n.locked) a.locked++; if (n.presence === 'state') a.orphan++; if (n.presence === 'spec') a.spec++; } return a; };

// buildItems computes what is visible: the lane tree for the layout, the items, the merged edges and, for
// every node, the item that stands for it.
//   collapsed: Set of module paths shown as one item
//   clusters:  Map cluster id -> number of members shown (an entry means the cluster is open)
export function buildItems(model, collapsed, clusters) {
  const items = new Map();
  const rep = new Map();
  const order = [];
  const put = (it) => { items.set(it.id, it); order.push(it); return it; };

  function walk(mod, lane) {
    for (const [k, ns] of groupsOf(mod)) {
      const cid = 'c:' + mod.path + '|' + k;
      if (ns.length >= CLUSTER_MIN) {
        const lim = clusters.get(cid);
        if (lim === undefined) {
          const it = put({ id: cid, kind: 'cluster', type: ns[0].type, mode: ns[0].mode, members: ns, mod, agg: aggOf(ns), w: G.CW, h: cardHeight(ns.length), lane: mod.path });
          lane.items.push(it.id);
          for (const n of ns) rep.set(n.addr, it.id);
          continue;
        }
        const shown = Math.min(lim, ns.length);
        for (let i = 0; i < shown; i++) {
          const n = ns[i];
          const it = put({ id: 'n:' + n.addr, kind: 'node', n, w: G.W, h: G.H, lane: mod.path, cluster: cid });
          lane.items.push(it.id);
          rep.set(n.addr, it.id);
        }
        const ctl = put({ id: 'x:' + cid, kind: 'ctl', cluster: cid, type: ns[0].type, total: ns.length, shown, w: G.CW, h: G.CTL, lane: mod.path, with: shown ? 'n:' + ns[0].addr : '' });
        lane.items.push(ctl.id);
        for (let i = shown; i < ns.length; i++) rep.set(ns[i].addr, ctl.id);
        continue;
      }
      for (const n of ns) {
        const it = put({ id: 'n:' + n.addr, kind: 'node', n, w: G.W, h: G.H, lane: mod.path });
        lane.items.push(it.id);
        rep.set(n.addr, it.id);
      }
    }
    for (const cm of mod.kids) {
      if (collapsed.has(cm.path)) {
        const it = put({ id: 'm:' + cm.path, kind: 'module', mod: cm, agg: cm.agg, w: G.CW, h: cardHeight(cm.agg.n), lane: mod.path });
        lane.items.push(it.id);
        eachNode(cm, (n) => rep.set(n.addr, it.id));
      } else {
        const sub = { path: cm.path, label: cm.name, mod: cm, items: [], sub: [] };
        lane.sub.push(sub);
        walk(cm, sub);
      }
    }
  }
  const root = { path: '', label: '', mod: model.root, items: [], sub: [] };
  walk(model.root, root);

  const merged = new Map();
  for (const e of model.edges) {
    const a = rep.get(e.a), b = rep.get(e.b);
    if (!a || !b || a === b) continue;
    const k = a + '>' + b;
    const m = merged.get(k);
    if (m) m.n++;
    else merged.set(k, { key: k, a, b, n: 1 });
  }
  return { root, items, order, rep, edges: [...merged.values()] };
}

// countItems is buildItems without the work: how many items a set of collapsed modules leaves.
export function countItems(model, collapsed, clusters) {
  const memo = new Map();
  function direct(mod) {
    let c = 0;
    for (const [k, ns] of groupsOf(mod)) {
      const lim = clusters.get('c:' + mod.path + '|' + k);
      if (ns.length >= CLUSTER_MIN) c += lim === undefined ? 1 : Math.min(lim, ns.length) + 1;
      else c += ns.length;
    }
    return c;
  }
  function count(mod) {
    let c = direct(mod);
    for (const k of mod.kids) c += collapsed.has(k.path) ? 1 : count(k);
    memo.set(mod.path, c);
    return c;
  }
  return { total: count(model.root), memo };
}

// ---- the first view ----
//
// firstView chooses which modules are open when the page loads. The aim is a picture that can be read at
// once: at the fit zoom the text is at least about 11 pixels high, and no more items are on the canvas than
// a person takes in (the budget, which grows with the area of the canvas: about 75 at 1440 by 900).
//
// The policy goes level by level, breadth first. It starts with every module closed (a card each) and
// walks the closed modules that are in view, those whose parents are open, in this order: first the ones
// that the plan, the drift, a filter or the selection touch, then the larger before the smaller. A module
// opens when the picture that follows still fits (a trial layout, bounds only) at a zoom of at least minK
// and within the budget; a module that does not fit stays a card, and so does everything below it. When a
// level is done the modules that its openings revealed are the next level. A module opened by the
// selection (must) is open whatever it costs. The result is the set of closed modules.
//
//   o.width, o.height  the canvas in pixels
//   o.minK             the least fit zoom (0.8 makes a 14 px name 11 px)
//   o.budget           the most items
//   o.touched          Set of module paths that deserve to open first
//   o.must             Set of module paths that open at any price
export const budgetFor = (w, h) => Math.min(220, Math.max(40, Math.round((w * h) / 8500)));

export function firstView(model, clusters, o = {}) {
  const width = o.width > 0 ? o.width : 780, height = o.height > 0 ? o.height : 820;
  const minK = o.minK === undefined ? 0.8 : o.minK;
  const budget = o.budget || budgetFor(width, height);
  const touched = o.touched || new Set();
  const must = o.must || new Set();
  const fit = { w: width - FIT.padX * 2, h: height - FIT.padTop - FIT.padBottom };
  const collapsed = new Set();
  for (const m of model.modules.values()) if (!m.isRoot) collapsed.add(m.path);
  for (const p of must) { const m = model.modules.get(p); if (m) for (const c of chain(m)) collapsed.delete(c.path); }

  let trials = 0, lastK = 0, lastItems = 0;
  const measure = () => {
    const count = countItems(model, collapsed, clusters).total;
    lastItems = count;
    if (count > budget) return false;
    trials++;
    const vis = buildItems(model, collapsed, clusters);
    const lay = layout({ root: vis.root, items: vis.items, edges: vis.edges, aspect: fit.w / fit.h, fit, route: false, sweeps: 0 });
    lastK = fitScale(lay.bounds, width, height, 99);
    return lastK >= minK;
  };
  measure();
  const failed = new Set();
  for (let level = 0; level < 12; level++) {
    const cands = [];
    for (const m of model.modules.values()) {
      if (m.isRoot || !collapsed.has(m.path) || failed.has(m.path) || (!m.total && !m.agg.out)) continue;
      if (m.up && !m.up.isRoot && collapsed.has(m.up.path)) continue;
      cands.push(m);
    }
    if (!cands.length) break;
    // peers (the same depth, the same size, the same number of modules inside) open together or not at all,
    // so that three regions of one shape do not end as one deep and two shallow; what is touched goes alone
    const groups = new Map();
    for (const m of cands) {
      const key = touched.has(m.path) ? 't|' + m.path : [m.depth, m.total, m.kids.length].join('|');
      let g = groups.get(key);
      if (!g) groups.set(key, (g = { size: m.total, touched: touched.has(m.path), mods: [] }));
      g.mods.push(m);
    }
    const order = [...groups.values()].sort((a, b) => (b.touched - a.touched) || b.size - a.size || natural(a.mods[0].path, b.mods[0].path));
    let opened = 0;
    for (const g of order) {
      for (const m of g.mods) collapsed.delete(m.path);
      if (measure()) opened++;
      else for (const m of g.mods) { collapsed.add(m.path); failed.add(m.path); }
      if (trials > 160) break;
    }
    if (!opened || trials > 160) break;
  }
  measure();
  return { collapsed, k: lastK, items: countItems(model, collapsed, clusters).total, trials, budget };
}

// touchedModules is the set of module paths that hold a node for which pred is true.
export function touchedModules(model, pred) {
  const out = new Set();
  for (const n of model.list) if (pred(n)) for (let m = n.mod; m && !m.isRoot; m = m.up) { if (out.has(m.path)) break; out.add(m.path); }
  return out;
}

// cellsOf turns nodes into at most max cells, in order; a cell that stands for several nodes takes the most
// telling state among them. States, most telling first: delete, replace, update, create, gone, drifted,
// read (and move, import), sensitive, plain.
const CELL_ORDER = ['delete', 'replace', 'update', 'create', 'gone', 'drift', 'read', 'sens', 'plain'];
export function cellState(n) {
  const a = n.action;
  if (a === 'delete') return 'delete';
  if (a === 'replace') return 'replace';
  if (a === 'update') return 'update';
  if (a === 'create') return 'create';
  if (n.gone) return 'gone';
  if (n.drifted) return 'drift';
  if (a === 'read' || a === 'move' || a === 'import') return 'read';
  if (n.sensitive > 0) return 'sens';
  return 'plain';
}
export function cellsOf(nodes, max = CELLS_MAX) {
  const per = Math.max(1, Math.ceil(nodes.length / max));
  const out = [];
  for (let i = 0; i < nodes.length; i += per) {
    let best = 8;
    for (let j = i; j < Math.min(nodes.length, i + per); j++) best = Math.min(best, CELL_ORDER.indexOf(cellState(nodes[j])));
    out.push(CELL_ORDER[best]);
  }
  return out;
}

// reveal opens whatever hides a node: collapsed modules above it and the cluster it sits in.
export function reveal(model, addr, collapsed, clusters) {
  const n = model.nodes.get(addr);
  if (!n) return false;
  let changed = false;
  for (const m of chain(n.mod)) if (collapsed.delete(m.path)) changed = true;
  const ns = n.mod.nodes.filter((x) => x.mode === n.mode && x.type === n.type);
  if (ns.length >= CLUSTER_MIN) {
    const cid = clusterId(n.mod.path, n);
    const lim = clusters.get(cid);
    const idx = ns.indexOf(n);
    if (lim === undefined) { clusters.set(cid, idx < CLUSTER_CAP ? CLUSTER_CAP : Infinity); changed = true; }
    else if (idx >= lim) { clusters.set(cid, Infinity); changed = true; }
  }
  return changed;
}

// ---- search ----

// fuzzy scores a subsequence match of q in text; -1 when it does not match. cs are the matched positions.
export function fuzzy(q, text) {
  if (!q) return { score: 0, cs: [] };
  const t = text.toLowerCase();
  let ti = 0, score = 0, run = 0;
  const cs = [];
  for (let qi = 0; qi < q.length; qi++) {
    const ch = q[qi];
    const at = t.indexOf(ch, ti);
    if (at < 0) return null;
    const boundary = at === 0 || '._-/ '.includes(t[at - 1]);
    if (at === ti && qi > 0) run++; else run = 0;
    score += 1 + run * 2 + (boundary ? 4 : 0) - Math.min(at - ti, 6) * 0.15;
    cs.push(at);
    ti = at + 1;
  }
  const sub = t.indexOf(q);
  if (sub >= 0) score += 10 + (sub === 0 ? 6 : 0);
  return { score: score - t.length * 0.02, cs };
}

// ---- diff of two graphs ----

const FIELDS = ['action', 'drifted', 'gone', 'locked', 'sealed', 'sensitive', 'revision', 'presence', 'overlap'];
export function diffModels(a, b) {
  const added = [], removed = [], changed = [];
  for (const [addr, n] of b.nodes) {
    const o = a.nodes.get(addr);
    if (!o) { added.push(addr); continue; }
    if (FIELDS.some((f) => (o[f] || 0) !== (n[f] || 0)) || (o.plans || []).join() !== (n.plans || []).join()) changed.push(addr);
  }
  for (const addr of a.nodes.keys()) if (!b.nodes.has(addr)) removed.push(addr);
  const edgeKey = (m) => m.edges.length + ':' + m.edges.map((e) => e.a + '>' + e.b).sort().join('|');
  return { added, removed, changed, edges: edgeKey(a) !== edgeKey(b), n: added.length + removed.length + changed.length };
}

// ---- walking the graph ----

// closure returns the set of addresses reached from start along deps (up) or used (down), start excluded.
export function closure(model, start, dir) {
  const out = new Set();
  const stack = [start];
  while (stack.length) {
    const n = model.nodes.get(stack.pop());
    if (!n) continue;
    for (const a of dir === 'up' ? n.deps : n.used) if (!out.has(a)) { out.add(a); stack.push(a); }
  }
  out.delete(start);
  return out;
}
