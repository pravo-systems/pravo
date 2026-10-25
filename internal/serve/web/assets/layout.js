// Layered left-to-right layout with nested namespace lanes, shaped to the canvas. Pure functions, no DOM.
//
// Ranks are the longest path over the visible items. A namespace that is open is a lane: a box that holds a
// block (its own items, one column for each rank that it uses, the columns closed up) and its sub-lanes.
// Nothing is placed by a global column any more: every lane is measured bottom-up, and the entries of a
// lane (its block and its sub-lanes, in the order of the flow) are arranged by whichever of two packings
// comes closest to the shape that is wanted: rows that wrap (shelves) or columns that fill from the
// shortest (masonry). A block folds a tall column into several, and wraps its ranks into rows, when that
// brings it nearer to the shape. The root picks the arrangement that gives the largest fit zoom on the
// canvas, the others aim for a plain landscape box. Items inside a column are ordered by barycenter
// sweeps, then pulled towards their neighbours. An edge leaves by the side that faces its target.

export const G = {
  W: 224, H: 52, CW: 224, MH: 80, MH2: 94, CTL: 36,
  GAPX: 50, GAPY: 12, FOLDX: 14, ROWGAP: 26,
  PAD: 14, HEAD: 38, LANEGAP: 20,
  TB: 1.5, TL: 1.55,
};

// the room that the camera leaves around the picture, and the zoom that makes it fit
export const FIT = { padX: 52, padTop: 40, padBottom: 60, maxK: 1.25, minK: 0.03 };
export function fitScale(b, w, h, maxK = FIT.maxK) {
  if (!b || !b.w || !b.h || !w || !h) return 1;
  const k = Math.min((w - FIT.padX * 2) / b.w, (h - FIT.padTop - FIT.padBottom) / b.h);
  return Math.min(maxK, Math.max(FIT.minK, k));
}

const log = Math.log;

// layout takes { root, items, edges, aspect, fit, route, sweeps }.
//   root:   a lane { path, label, items: [id], sub: [lane] }; the root lane is virtual (it draws no box)
//   items:  Map id -> { id, w, h }
//   edges:  [{ a, b }] from dependency to dependent, between item ids
//   aspect: the width over the height that the picture should have when fit is not given
//   fit:    { w, h } the canvas; the root then picks the arrangement that fits it at the largest zoom
//   route:  false leaves the routes out (a trial layout needs the bounds only)
//   hint:   the arr of an earlier layout. A lane whose entries were arranged then keeps its entries in the rows
//           (or columns) they were in, when that costs little of the fit: a namespace that opens or closes does not
//           send every other lane across the canvas.
// It returns { pos, lanes, routes, back, bounds, rank, maxRank, arr }.
export function layout({ root, items, edges, aspect = 1.4, fit = null, route = true, sweeps = null, hint = null }) {
  const ids = [...items.keys()];
  const succ = new Map(), pred = new Map();
  for (const id of ids) { succ.set(id, []); pred.set(id, []); }
  const E = [];
  for (const e of edges) {
    if (e.a === e.b || !items.has(e.a) || !items.has(e.b)) continue;
    succ.get(e.a).push(e.b);
    pred.get(e.b).push(e.a);
    E.push(e);
  }

  // ---- ranks: break cycles, longest path, pull sources next to their first consumer ----
  const back = new Set();
  {
    const state = new Map();
    const starts = ids.filter((i) => !pred.get(i).length).concat(ids.filter((i) => pred.get(i).length));
    for (const r of starts) {
      if (state.get(r)) continue;
      state.set(r, 1);
      const stack = [[r, 0]];
      while (stack.length) {
        const top = stack[stack.length - 1];
        const s = succ.get(top[0]);
        if (top[1] < s.length) {
          const w = s[top[1]++];
          const c = state.get(w) || 0;
          if (c === 0) { state.set(w, 1); stack.push([w, 0]); } else if (c === 1) back.add(top[0] + '\u0000' + w);
        } else { state.set(top[0], 2); stack.pop(); }
      }
    }
  }
  const isBack = (a, b) => back.has(a + '\u0000' + b);
  const rank = new Map();
  {
    const indeg = new Map();
    for (const id of ids) indeg.set(id, 0);
    for (const e of E) if (!isBack(e.a, e.b)) indeg.set(e.b, indeg.get(e.b) + 1);
    const queue = [];
    for (const id of ids) { rank.set(id, 0); if (!indeg.get(id)) queue.push(id); }
    for (let qi = 0; qi < queue.length; qi++) {
      const v = queue[qi];
      for (const w of succ.get(v)) {
        if (isBack(v, w)) continue;
        if (rank.get(w) < rank.get(v) + 1) rank.set(w, rank.get(v) + 1);
        indeg.set(w, indeg.get(w) - 1);
        if (!indeg.get(w)) queue.push(w);
      }
    }
    for (const id of ids) {
      const ps = pred.get(id).filter((p) => !isBack(p, id));
      if (ps.length) continue;
      let m = Infinity;
      for (const w of succ.get(id)) if (!isBack(id, w)) m = Math.min(m, rank.get(w));
      if (m !== Infinity && m - 1 > rank.get(id)) rank.set(id, m - 1);
    }
  }
  // an item may ask to stand in the column of another (the control of an open group, with its members)
  for (const id of ids) {
    const w = items.get(id).with;
    if (w && rank.has(w)) rank.set(id, rank.get(w));
  }
  let maxRank = 0;
  for (const r of rank.values()) maxRank = Math.max(maxRank, r);
  // it reads like a page: what an item depends on stands to its right, and what depends on nothing stands furthest right
  for (const id of ids) rank.set(id, maxRank - rank.get(id));

  // ---- lanes ----
  const lanes = [];
  const pos = new Map();
  function mk(src, parent, depth, top) {
    const l = { path: src.path, label: src.label || '', parent, depth, top, virtual: !parent, sub: [], cols: new Map(), entries: [], x: 0, y: 0, w: 0, h: 0, ox: 0, minRank: Infinity, sumRank: 0, nItems: 0 };
    lanes.push(l);
    for (const id of src.items) {
      const r = rank.get(id);
      let c = l.cols.get(r);
      if (!c) l.cols.set(r, (c = []));
      c.push(id);
      l.minRank = Math.min(l.minRank, r);
      l.sumRank += r;
      l.nItems++;
    }
    src.sub.forEach((s, i) => {
      const sl = mk(s, l, depth + 1, parent ? top : i);
      l.sub.push(sl);
      l.minRank = Math.min(l.minRank, sl.minRank);
      l.sumRank += sl.sumRank;
      l.nItems += sl.nItems;
    });
    return l;
  }
  const R = mk(root, null, 0, -1);

  const H = (id) => items.get(id).h || G.H;
  const Wd = (id) => items.get(id).w || G.W;
  const labelWidth = (l) => l.label.length * 8.4 + 92;

  // blockOf measures the items of a lane: columns by rank, closed up, folded and wrapped to the shape.
  function blockOf(l) {
    if (!l.cols.size) return null;
    const ranks = [...l.cols.keys()].sort((a, b) => a - b);
    const cols = ranks.map((r) => { const cids = l.cols.get(r); let w = 0; for (const id of cids) w = Math.max(w, Wd(id)); return { rank: r, ids: cids, w }; });
    let maxLen = 0;
    for (const c of cols) maxLen = Math.max(maxLen, c.ids.length);
    const rowCands = new Set();
    if (maxLen <= 16) for (let i = 1; i <= maxLen; i++) rowCands.add(i);
    else { rowCands.add(maxLen); for (let c = 2; c <= 16; c++) rowCands.add(Math.ceil(maxLen / c)); }
    let best = null, bestScore = Infinity;
    for (const rows of rowCands) {
      for (let per = 1; per <= cols.length; per++) {
        const b = measure(cols, rows, per);
        // a fold that leaves a column nearly empty (12 items in 11 and 1) is worse than an even one
        let ragged = 0;
        for (const c of cols) { const k = Math.ceil(c.ids.length / rows); if (k > 1) ragged += (k * rows - c.ids.length) / c.ids.length; }
        const sc = Math.abs(log(b.w / b.h / Math.min(1.7, Math.max(1, aspect)))) + (rows < maxLen ? 0.05 : 0) + (per < cols.length ? 0.14 : 0) + ragged * 0.3 + b.h * 0.00002;
        if (sc < bestScore - 1e-9) { bestScore = sc; best = b; }
      }
    }
    let sum = 0, n = 0;
    for (const c of cols) { sum += c.rank * c.ids.length; n += c.ids.length; }
    best.minRank = cols[0].rank;
    best.mean = sum / n;
    return best;
  }
  function measure(cols, rowsMax, per) {
    const rows = [];
    let y = 0, wMax = 0;
    for (let i = 0; i < cols.length; i += per) {
      const units = [];
      let x = 0, rh = 0;
      for (const c of cols.slice(i, i + per)) {
        for (let s = 0; s < c.ids.length; s += rowsMax) {
          const uids = c.ids.slice(s, s + rowsMax);
          let hh = (uids.length - 1) * G.GAPY;
          for (const id of uids) hh += H(id);
          units.push({ rank: c.rank, ids: uids, x, w: c.w, h: hh, lo: 0, hi: 0, ax: 0 });
          rh = Math.max(rh, hh);
          x += c.w + (s + rowsMax < c.ids.length ? G.FOLDX : G.GAPX);
        }
      }
      const last = units[units.length - 1];
      wMax = Math.max(wMax, last.x + last.w);
      rows.push({ y, h: rh, units });
      y += rh + G.ROWGAP;
    }
    return { w: wMax, h: y - G.ROWGAP, rows, minRank: 0, mean: 0 };
  }

  // compose arranges the entries of a lane; it sets ox and oy on each and returns the size.
  const arrOut = new Map();
  const entryKey = (e) => (e.sub ? 'L:' + e.sub.path : 'B');
  function compose(entries, root, lanePath) {
    const n = entries.length;
    if (!n) return { w: 0, h: 0 };
    if (n === 1) { entries[0].ox = 0; entries[0].oy = 0; return { w: entries[0].w, h: entries[0].h }; }
    const gap = G.LANEGAP;
    let area = 0, maxW = 0;
    for (const e of entries) { area += e.w * e.h; maxW = Math.max(maxW, e.w); }
    const useFit = root && fit && fit.w > 0 && fit.h > 0;
    const T = root ? aspect : Math.min(1.7, Math.max(1, aspect)); // a lane aims for the shape of the canvas, within reason
    const score = (w, h) => {
      const asp = Math.abs(log(w / h / T));
      const waste = log(Math.max(1, (w * h) / area));
      if (useFit) {
        const k = Math.min(fit.w / w, fit.h / h);
        return -log(Math.min(k, FIT.maxK)) + 0.05 * asp + 0.12 * waste;
      }
      return asp + 0.35 * waste;
    };
    let best = null, bestScore = Infinity;
    // a score that is not a number (a canvas with no size yet) still leaves an arrangement: the first one is kept
    const take = (sc, kind, offs, w, h) => { if (!best || sc < bestScore - 1e-9) { bestScore = sc; best = { kind, offs, w, h }; } };
    // shelves: rows that wrap at a limit
    const limits = new Set();
    if (n <= 26) {
      for (let i = 0; i < n; i++) { let s = 0; for (let j = i; j < n; j++) { s += entries[j].w + (j > i ? gap : 0); limits.add(Math.round(s)); } }
    } else {
      let s = 0;
      for (let j = 0; j < n; j++) { s += entries[j].w + (j ? gap : 0); limits.add(Math.round(s)); }
      for (let c = 1; c <= 24; c++) limits.add(Math.round(c * (maxW + gap)));
    }
    for (const lim of limits) {
      if (lim < maxW) continue;
      let x = 0, y = 0, rh = 0, wm = 0;
      const offs = [];
      for (const e of entries) {
        if (x > 0 && x + e.w > lim) { y += rh + gap; x = 0; rh = 0; }
        offs.push([x, y]);
        x += e.w + gap;
        rh = Math.max(rh, e.h);
        wm = Math.max(wm, x - gap);
      }
      take(score(wm, y + rh), 'shelf', offs, wm, y + rh);
    }
    // masonry: c columns, each entry on the shortest; in the order of the flow, and, tighter, tallest first
    const idx = entries.map((_, i) => i);
    const tallest = idx.slice().sort((p, q) => entries[q].h - entries[p].h || p - q);
    for (const [kind, seq, bias] of [['masonry', idx, 0.02], ['masonry', tallest, 0.04]]) {
      for (let c = 2; c <= Math.min(n, 8); c++) {
        const hs = new Array(c).fill(0), cw = new Array(c).fill(0), at = new Array(n);
        for (const i of seq) {
          const e = entries[i];
          let k = 0;
          for (let j = 1; j < c; j++) if (hs[j] < hs[k] - 0.5) k = j;
          at[i] = [k, hs[k]];
          hs[k] += e.h + gap;
          cw[k] = Math.max(cw[k], e.w);
        }
        const xs = [];
        let acc = 0;
        for (let j = 0; j < c; j++) { xs.push(acc); acc += (cw[j] > 0 ? cw[j] + gap : 0); }
        const w = acc - gap, h = Math.max(...hs) - gap;
        if (w <= 0 || h <= 0) continue;
        take(score(w, h) + bias, kind, at.map(([k, y]) => [xs[k], y]), w, h);
      }
    }
    // an arrangement that keeps the entries where they were, when it fits nearly as well as the best one
    const prev = hint && hint.get(lanePath);
    if (prev) {
      const st = stableArrange(entries, prev, gap);
      if (st && score(st.w, st.h) + (st.kind === 'masonry' ? 0.02 : 0) <= bestScore + (useFit ? 0.13 : 0.25)) best = st;
    }
    entries.forEach((e, i) => { e.ox = best.offs[i][0]; e.oy = best.offs[i][1]; });
    arrOut.set(lanePath, describeArrangement(entries, best));
    return { w: best.w, h: best.h };
  }

  // describeArrangement notes which row (a shelf) or column (masonry) each entry is in, and its place in it
  function describeArrangement(entries, a) {
    const shelf = a.kind === 'shelf';
    const ax = (i) => Math.round(a.offs[i][shelf ? 1 : 0]);
    const bx = (i) => a.offs[i][shelf ? 0 : 1];
    const groups = [...new Set(entries.map((_, i) => ax(i)))].sort((p, q) => p - q);
    return { kind: a.kind, keys: entries.map(entryKey), gs: entries.map((_, i) => groups.indexOf(ax(i))), ord: entries.map((_, i) => bx(i)) };
  }

  // stableArrange lays the entries of a lane out in the rows or columns that they were in, in the same order,
  // with the entries that are new beside the block (where a namespace that has opened stood as a card). It
  // gives up (null) when most of the entries are new.
  function stableArrange(entries, prev, gap) {
    const at = new Map(prev.keys.map((k, i) => [k, i]));
    let known = 0;
    for (const e of entries) if (at.has(entryKey(e))) known++;
    if (known < Math.ceil(entries.length / 2)) return null;
    const ng = Math.max(...prev.gs) + 1;
    const groups = Array.from({ length: ng }, () => []);
    const blockG = at.has('B') ? prev.gs[at.get('B')] : ng - 1;
    for (const e of entries) { const i = at.get(entryKey(e)); if (i !== undefined) groups[prev.gs[i]].push({ e, o: prev.ord[i] }); }
    for (const g of groups) g.sort((p, q) => p.o - q.o);
    for (const e of entries) {
      if (at.has(entryKey(e))) continue;
      const g = groups[blockG], bi = g.findIndex((x) => entryKey(x.e) === 'B');
      g.splice(bi >= 0 ? bi + 1 : g.length, 0, { e, o: 0 });
    }
    const idx = new Map(entries.map((e, i) => [e, i]));
    const offs = new Array(entries.length);
    let w = 0, h = 0;
    const gs = groups.filter((g) => g.length);
    if (prev.kind === 'shelf') {
      let y = 0;
      for (const g of gs) {
        let x = 0, rh = 0;
        for (const { e } of g) { offs[idx.get(e)] = [x, y]; x += e.w + gap; rh = Math.max(rh, e.h); }
        w = Math.max(w, x - gap); y += rh + gap;
      }
      h = y - gap;
    } else {
      let x = 0;
      for (const g of gs) {
        let y = 0, cw = 0;
        for (const { e } of g) { offs[idx.get(e)] = [x, y]; y += e.h + gap; cw = Math.max(cw, e.w); }
        h = Math.max(h, y - gap); x += cw + gap;
      }
      w = x - gap;
    }
    return w > 0 && h > 0 ? { kind: prev.kind, offs, w, h } : null;
  }

  function size(l) {
    for (const s of l.sub) size(s);
    const entries = [];
    const blk = blockOf(l);
    if (blk) entries.push({ block: blk, sub: null, w: blk.w, h: blk.h, minRank: blk.minRank, mean: blk.mean, ox: 0, oy: 0 });
    for (const s of l.sub) entries.push({ block: null, sub: s, w: s.w, h: s.h, minRank: s.minRank, mean: s.nItems ? s.sumRank / s.nItems : 0, ox: 0, oy: 0 });
    entries.sort((p, q) => p.minRank - q.minRank || p.mean - q.mean);
    const c = compose(entries, l.virtual, l.path);
    l.entries = entries;
    if (l.virtual) { l.w = c.w; l.h = c.h; l.ox = 0; return; }
    const inner = Math.max(c.w, labelWidth(l) - 2 * G.PAD);
    l.w = inner + 2 * G.PAD;
    l.h = G.HEAD + c.h + G.PAD;
    l.ox = (inner - c.w) / 2;
  }

  let units = [];
  function assign(l, ax, ay) {
    l.x = ax; l.y = ay;
    const cx = ax + (l.virtual ? 0 : G.PAD + l.ox), cy = ay + (l.virtual ? 0 : G.HEAD);
    for (const e of l.entries) {
      const ex = cx + e.ox, ey = cy + e.oy;
      if (e.sub) { assign(e.sub, ex, ey); continue; }
      for (const row of e.block.rows) {
        const top = ey + row.y;
        for (const u of row.units) {
          let yy = top + (row.h - u.h) / 2;
          u.lo = top; u.hi = top + row.h; u.ax = ex + u.x;
          for (const id of u.ids) {
            const it = items.get(id);
            pos.set(id, { x: u.ax, y: yy, w: it.w || G.W, h: it.h || G.H, rank: rank.get(id) });
            yy += (it.h || G.H) + G.GAPY;
          }
          units.push(u);
        }
      }
    }
  }
  const place = () => { units = []; size(R); assign(R, 0, 0); };
  place();

  const cyv = new Map();
  const loadCy = () => { for (const [id, p] of pos) cyv.set(id, p.y + p.h / 2); };

  // ---- ordering: barycenter sweeps ----
  function sweepColumns(down) {
    const rs = [];
    for (let r = 0; r <= maxRank; r++) rs.push(r);
    if (!down) rs.reverse();
    for (const r of rs) {
      for (const l of lanes) {
        const col = l.cols.get(r);
        if (!col || col.length < 2) continue;
        const want = col.map((id, i) => {
          const ns = down ? pred.get(id) : succ.get(id);
          let s = 0, n = 0;
          for (const o of ns) { if (isBack(down ? o : id, down ? id : o)) continue; s += cyv.get(o); n++; }
          return { id, i, d: n ? s / n : cyv.get(id) };
        });
        want.sort((p, q) => (p.d - q.d) || (p.i - q.i));
        // the new order takes the slots of the old one
        const slots = col.map((id) => cyv.get(id)).sort((p, q) => p - q);
        col.length = 0;
        want.forEach((w, i) => { col.push(w.id); cyv.set(w.id, slots[i]); });
      }
    }
  }
  const nSweeps = sweeps === null ? (items.size > 400 ? 2 : 4) : sweeps;
  for (let it = 0; it < nSweeps; it++) {
    loadCy();
    sweepColumns(true);
    sweepColumns(false);
    place();
  }
  if (nSweeps) { loadCy(); sweepColumns(true); place(); }

  // ---- straighten: pull each unit towards the centres of its neighbours, inside its band ----
  const cy = (id) => { const p = pos.get(id); return p.y + p.h / 2; };
  function straighten(down) {
    const order = units.slice().sort((p, q) => (down ? p.rank - q.rank : q.rank - p.rank));
    for (const u of order) {
      const hs = u.ids.map(H);
      const ys = u.ids.map((id, i) => {
        let s = 0, n = 0;
        for (const o of pred.get(id)) { s += cy(o); n++; }
        for (const o of succ.get(id)) { s += cy(o); n++; }
        return (n ? s / n : cy(id)) - hs[i] / 2;
      });
      spread(ys, hs, u.lo, u.hi);
      u.ids.forEach((id, i) => { pos.get(id).y = ys[i]; });
    }
  }
  if (nSweeps) for (let it = 0; it < 3; it++) { straighten(true); straighten(false); }

  // the titles of the lanes are in the way of an edge, too
  const heads = [];
  for (const l of lanes) if (!l.virtual) heads.push({ id: 'h:' + l.path, x: l.x + 8, y: l.y + 4, w: Math.min(l.w - 16, labelWidth(l) + 40), h: 28 });
  const routes = route ? routeAll(E, pos, isBack, heads) : new Map();

  // ---- result ----
  const out = [];
  let bx0 = Infinity, by0 = Infinity, bx1 = -Infinity, by1 = -Infinity;
  for (const l of lanes) {
    if (l.virtual) continue;
    out.push({ path: l.path, label: l.label, depth: l.depth, top: l.top, x: l.x, y: l.y, w: l.w, h: l.h });
    bx0 = Math.min(bx0, l.x); by0 = Math.min(by0, l.y); bx1 = Math.max(bx1, l.x + l.w); by1 = Math.max(by1, l.y + l.h);
  }
  for (const p of pos.values()) {
    bx0 = Math.min(bx0, p.x); by0 = Math.min(by0, p.y); bx1 = Math.max(bx1, p.x + p.w); by1 = Math.max(by1, p.y + p.h);
  }
  if (!isFinite(bx0)) { bx0 = by0 = 0; bx1 = by1 = 100; }
  return { pos, lanes: out, routes, back, bounds: { x: bx0, y: by0, w: bx1 - bx0, h: by1 - by0 }, rank, maxRank, arr: arrOut };
}

// spread moves ys (tops) as little as it can so that they keep their order, leave at least GAPY between
// items and stay inside [lo, hi].
function spread(ys, hs, lo, hi) {
  const n = ys.length;
  if (!n) return;
  for (let i = 1; i < n; i++) ys[i] = Math.max(ys[i], ys[i - 1] + hs[i - 1] + G.GAPY);
  if (ys[n - 1] + hs[n - 1] > hi) {
    ys[n - 1] = hi - hs[n - 1];
    for (let i = n - 2; i >= 0; i--) ys[i] = Math.min(ys[i], ys[i + 1] - hs[i] - G.GAPY);
  }
  if (ys[0] < lo) {
    ys[0] = lo;
    for (let i = 1; i < n; i++) ys[i] = Math.max(ys[i], ys[i - 1] + hs[i - 1] + G.GAPY);
  }
}

// ---- tracks ----
//
// Wires that run side by side each get a track of their own, TRACK apart, and keep inside the gap that they run
// in: a vertical run between two columns, a horizontal run between two rows. A run that touches a port stays where
// it is, so that the wires of one port still leave and enter along one line.
const TRACK = 8;
function spreadWires(routes, boxes) {
  const list = [];
  for (const r of routes.values()) { r.q = polyOf({ m: r.m, p: r.p }).slice(); list.push(r); }
  const segs = (vertical) => {
    const out = [];
    for (const r of list) {
      const q = r.q;
      // a run between two corners: not the first or the last of the wire
      for (let i = 4; i < q.length - 2; i += 2) {
        const x0 = q[i - 2], y0 = q[i - 1], x1 = q[i], y1 = q[i + 1];
        if (vertical && Math.abs(x0 - x1) < 0.01 && Math.abs(y0 - y1) > 0.01) out.push({ r, i, c: x0, a: Math.min(y0, y1), b: Math.max(y0, y1), up: y1 < y0, from: y0 });
        if (!vertical && Math.abs(y0 - y1) < 0.01 && Math.abs(x0 - x1) > 0.01) out.push({ r, i, c: y0, a: Math.min(x0, x1), b: Math.max(x0, x1), up: false, from: x0 });
      }
    }
    return out;
  };
  const place = (g, vertical) => {
    const n = g.length;
    const mean = g.reduce((t, v) => t + v.c, 0) / n;
    const a = Math.min(...g.map((v) => v.a)), b = Math.max(...g.map((v) => v.b));
    let lo = -Infinity, hi = Infinity;
    for (const bx of boxes) {
      if (vertical) {
        if (bx.y >= b || bx.y + bx.h <= a) continue;
        if (bx.x + bx.w <= mean) lo = Math.max(lo, bx.x + bx.w + 4);
        else if (bx.x >= mean) hi = Math.min(hi, bx.x - 4);
      } else {
        if (bx.x >= b || bx.x + bx.w <= a) continue;
        if (bx.y + bx.h <= mean) lo = Math.max(lo, bx.y + bx.h + 4);
        else if (bx.y >= mean) hi = Math.min(hi, bx.y - 4);
      }
    }
    const room = hi - lo;
    const gap = Math.min(TRACK, Number.isFinite(room) ? room / (n + 1) : TRACK);
    if (gap < 2.5) return;
    const half = (n - 1) * gap / 2;
    const mid = Math.min(Math.max(mean, lo + half + gap / 2), hi - half - gap / 2);
    g.forEach((v, k) => {
      const c = mid - half + k * gap;
      const q = v.r.q;
      if (vertical) { q[v.i - 2] = c; q[v.i] = c; } else { q[v.i - 1] = c; q[v.i + 1] = c; }
    });
  };
  for (const vertical of [true, false]) {
    const all = segs(vertical).sort((p, q) => p.c - q.c);
    const used = new Set();
    for (let s = 0; s < all.length; s++) {
      if (used.has(s)) continue;
      const g = [all[s]];
      used.add(s);
      // the run that lie next to each other: close, and over one stretch
      for (let t = s + 1; t < all.length && all[t].c - all[s].c < 2.2 * TRACK + 6; t++) {
        if (used.has(t)) continue;
        if (g.some((v) => v.r === all[t].r)) continue;
        if (g.some((v) => all[t].a < v.b - 1 && all[t].b > v.a + 1)) { g.push(all[t]); used.add(t); }
      }
      if (g.length < 2) continue;
      // the order that keeps wires from crossing: of the wires that go down, the one that starts lower is on the
      // outside, and the other way round for the wires that go up
      if (vertical) g.sort((p, q) => (p.up === q.up ? (p.up ? p.from - q.from : q.from - p.from) : p.up ? -1 : 1));
      else g.sort((p, q) => p.from - q.from);
      place(g, vertical);
    }
  }
}

// ---- routing ----
//
// A route is { m, p }: a mode and a flat list of points. Between two points the line runs along the axes, in the
// way of a wiring diagram (polyOf). Mode "h" leaves the right side of the dependency and enters the left
// side of the dependent: the plain curve unless that meets an item, in which case it passes through a gap
// between items (two points that lead the line straight across the gap, found by trying the gaps of the
// vertical strip in the middle of the edge and keeping the one with the fewest meetings; a half that still
// meets an item is routed again). Mode "l" goes the other way, left side to right side, and mode "v" leaves
// the bottom or the top, for items that sit one over the other.

// The channel is the line of the vertical run: in the gutter just before what the wire enters. Wires that share a
// gutter are then given a track each (spreadWires).
const CH = 24;
const channelX = (ax, ay, bx, by) => bx - Math.min(CH, Math.max(0, (bx - ax) / 2));

const CELL = 96;
const FAR = 96;
function routeAll(E, pos, isBack, heads = []) {
  const boxes = [];
  const grid = new Map();
  const solids = [...pos].map(([id, p]) => ({ id, x: p.x - 5, y: p.y - 5, w: p.w + 10, h: p.h + 10 })).concat(heads);
  for (const b of solids) {
    boxes.push(b);
    for (let gx = Math.floor(b.x / CELL); gx <= Math.floor((b.x + b.w) / CELL); gx++) {
      for (let gy = Math.floor(b.y / CELL); gy <= Math.floor((b.y + b.h) / CELL); gy++) {
        const k = gx + ',' + gy;
        let a = grid.get(k);
        if (!a) grid.set(k, (a = []));
        a.push(b);
      }
    }
  }
  const at = (px, py, sa, sb) => {
    const l = grid.get(Math.floor(px / CELL) + ',' + Math.floor(py / CELL));
    if (!l) return false;
    for (const b of l) if (b.id !== sa && b.id !== sb && px > b.x && px < b.x + b.w && py > b.y && py < b.y + b.h) return true;
    return false;
  };
  // the number of samples of the wire from (ax, ay) to (bx, by) that lie inside an item
  const hits = (ax, ay, bx, by, sa, sb) => {
    if (bx - ax < 24) return 0;
    const cx = channelX(ax, ay, bx, by);
    let n = 0;
    const run = (x0, y0, x1, y1) => {
      const len = Math.hypot(x1 - x0, y1 - y0);
      for (let d = 6; d < len - 2; d += 8) {
        const t = d / len;
        if (at(x0 + (x1 - x0) * t, y0 + (y1 - y0) * t, sa, sb)) n++;
      }
    };
    run(ax, ay, cx, ay);
    run(cx, ay, cx, by);
    run(cx, by, bx, by);
    return n;
  };

  function refine(ax, ay, bx, by, sa, sb, depth, ei) {
    if (hits(ax, ay, bx, by, sa, sb) === 0 || depth >= 2 || bx - ax < G.W + 60) return [ax, ay, bx, by];
    const mx = (ax + bx) / 2;
    const lineY = (ay + by) / 2;
    const strip = boxes.filter((b) => b.id !== sa && b.id !== sb && b.x < mx + G.W / 2 && b.x + b.w > mx - G.W / 2).sort((p, q) => p.y - q.y);
    const cands = [lineY];
    if (strip.length) {
      cands.push(strip[0].y - 10, strip[strip.length - 1].y + strip[strip.length - 1].h + 10);
      for (let i = 0; i + 1 < strip.length; i++) {
        const gap = strip[i + 1].y - (strip[i].y + strip[i].h);
        if (gap >= 6) cands.push(strip[i].y + strip[i].h + gap / 2);
      }
    }
    const hx0 = mx - G.W / 2 - 10, hx1 = mx + G.W / 2 + 10;
    let best = null, bestCost = Infinity;
    for (const yy of cands) {
      const h = hits(ax, ay, hx0, yy, sa, sb) + hits(hx1, yy, bx, by, sa, sb);
      let leg = 0;
      for (let x = hx0; x <= hx1; x += 12) if (at(x, yy, sa, sb)) leg++;
      const cost = (h + leg * 3) * 1000 + Math.abs(yy - lineY) * 0.6 + Math.abs(yy - ay) * 0.2;
      if (cost < bestCost) { bestCost = cost; best = { yy, h: h + leg }; }
    }
    const wy = best.yy + (((ei * 7) % 5) - 2) * 1.8;
    if (best.h > 0 && depth < 1) {
      return [...refine(ax, ay, hx0, wy, sa, sb, depth + 1, ei), ...refine(hx1, wy, bx, by, sa, sb, depth + 1, ei)];
    }
    return [ax, ay, hx0, wy, hx1, wy, bx, by];
  }

  // a wire that runs against the grain, from the left side of an item to the right side of one that lies to the left
  // of it: straight along its own height when that is clear, else along the gap between items that has the fewest meetings
  const polyHits = (q, sa, sb) => {
    let n = 0;
    for (let i = 2; i < q.length; i += 2) {
      const x0 = q[i - 2], y0 = q[i - 1], x1 = q[i], y1 = q[i + 1];
      const len = Math.hypot(x1 - x0, y1 - y0);
      for (let d = 6; d < len - 2; d += 8) {
        const t = d / len;
        if (at(x0 + (x1 - x0) * t, y0 + (y1 - y0) * t, sa, sb)) n++;
      }
    }
    return n;
  };
  function refineLoop(x0, y0, x1, y1, sa, sb) {
    const cx = x1 - CH;
    const c0 = x0 + CH;
    const wire = (yy) => [x0, y0, c0, y0, c0, yy, cx, yy, cx, y1, x1, y1];
    const ys = new Set([y0, y1]);
    for (const b of boxes) if (b.id !== sa && b.id !== sb && b.x < x0 && b.x + b.w > x1) { ys.add(b.y - 8); ys.add(b.y + b.h + 8); }
    let best = y0, bestCost = Infinity;
    for (const yy of ys) {
      const cost = polyHits(wire(yy), sa, sb) * 1000 + Math.abs(yy - y0) * 0.3 + Math.abs(yy - y1) * 0.3;
      if (cost < bestCost) { bestCost = cost; best = yy; }
    }
    return [x0, y0, c0, best, cx, best, x1, y1];
  }

  // the way an edge goes, and the side of each item that it leaves or enters
  // A is what reads and B is what it reads: the wire runs from A to B, and its arrowhead points right
  const modeOf = (A, B) => {
    const gapR = B.x - (A.x + A.w), gapL = A.x - (B.x + B.w);
    if (gapR >= 20) return ['h', 'r', 'l'];
    if (gapL >= 20) return ['l', 'r', 'l'];
    if (B.y >= A.y + A.h + FAR || A.y >= B.y + B.h + FAR) return ['c', 'l', 'l'];
    if (B.y >= A.y + A.h - 1) return ['v', 'b', 't'];
    // what it reads stands above it: in from the left, along the left side, so that no arrow points up
    if (A.y >= B.y + B.h - 1) return ['c', 'l', 'l'];
    return ['h2', 'r', 'l'];
  };
  // every wire has a pin of its own on the side of an item, in the order of the items at their other ends
  const pins = new Map();
  const ways = E.map((e) => modeOf(pos.get(e.b), pos.get(e.a)));
  {
    const groups = new Map();
    E.forEach((e, i) => {
      for (const end of [0, 1]) {
        const id = end ? e.a : e.b, other = pos.get(end ? e.b : e.a);
        const k = id + '|' + ways[i][1 + end];
        let g = groups.get(k);
        if (!g) groups.set(k, (g = []));
        g.push({ e, end, at: ways[i][1 + end] === 't' || ways[i][1 + end] === 'b' ? other.x + other.w / 2 : other.y + other.h / 2 });
      }
    });
    for (const [k, g] of groups) {
      const id = k.slice(0, k.lastIndexOf('|')), side = k.slice(-1);
      const P = pos.get(id);
      const span = side === 't' || side === 'b' ? P.w - 36 : P.h - 14;
      const n = g.length;
      const step = n > 1 ? Math.min(side === 't' || side === 'b' ? 14 : 8, span / (n - 1)) : 0;
      if (n < 2 || step < 3) continue;
      g.sort((p, q) => p.at - q.at);
      g.forEach((v, j) => pins.set(v.e.a + '>' + v.e.b + '|' + v.end, (j - (n - 1) / 2) * step));
    }
  }

  const routes = new Map();
  let ei = 0;
  E.forEach((e, i) => {
    const A = pos.get(e.b), B = pos.get(e.a);
    ei++;
    const key = e.a + '>' + e.b;
    const m = ways[i][0];
    const oa = pins.get(key + '|0') || 0, ob = pins.get(key + '|1') || 0;
    const ya = A.y + A.h / 2 + oa, yb = B.y + B.h / 2 + ob;
    if (m === 'h') {
      const x0 = A.x + A.w, x1 = B.x;
      routes.set(key, { m: 'h', p: isBack(e.a, e.b) ? [x0, ya, x1, yb] : refine(x0, ya, x1, yb, e.b, e.a, 0, ei) });
    } else if (m === 'l') {
      routes.set(key, { m: 'l', p: refineLoop(A.x + A.w, ya, B.x, yb, e.b, e.a) });
    } else if (m === 'c') {
      // far apart, one over the other: along the left side, where nothing stands, rather than through what is between
      routes.set(key, { m: 'c', p: [A.x, ya, B.x, yb] });
    } else if (m === 'v') {
      routes.set(key, { m: 'v', p: [A.x + A.w / 2 + oa, A.y + A.h, B.x + B.w / 2 + ob, B.y] });
    } else {
      routes.set(key, { m: 'h', p: [A.x + A.w, ya, B.x, yb] });
    }
  });
  spreadWires(routes, boxes);
  return routes;
}

const f1 = (n) => Math.round(n * 10) / 10;

// polyOf turns a route into the corners of a wiring diagram: lines that run along the axes and turn at right
// angles, in the channel that lies just before the thing they enter. Edges that leave or reach the same port
// share their line, as the wires of a bus do.
function polyOf(r) {
  if (r.q) return r.q;
  const p = r.p;
  const o = [];
  const add = (x, y) => {
    const n = o.length;
    if (n && Math.abs(o[n - 2] - x) < 0.01 && Math.abs(o[n - 1] - y) < 0.01) return;
    // a point on the line of the two before it is not a corner
    if (n >= 4 && ((Math.abs(o[n - 4] - o[n - 2]) < 0.01 && Math.abs(o[n - 2] - x) < 0.01) || (Math.abs(o[n - 3] - o[n - 1]) < 0.01 && Math.abs(o[n - 1] - y) < 0.01))) { o[n - 2] = x; o[n - 1] = y; return; }
    o.push(x, y);
  };
  if (r.m === 'c') {
    const [x0, y0, x1, y1] = p;
    const bx = Math.min(x0, x1) - CH;
    add(x0, y0); add(bx, y0); add(bx, y1); add(x1, y1);
    return o;
  }
  if (r.m === 'v') {
    const [x0, y0, x1, y1] = p;
    const my = (y0 + y1) / 2;
    add(x0, y0); add(x0, my); add(x1, my); add(x1, y1);
    return o;
  }
  if (r.m === 'l' && p.length === 8) {
    const [x0, y0, c0, yy, cx, , x1, y1] = p;
    add(x0, y0); add(c0, y0); add(c0, yy); add(cx, yy); add(cx, y1); add(x1, y1);
    return o;
  }
  if (r.m === 'l') {
    const [x0, y0, x1, y1] = p;
    const cx = x1 + Math.min(CH, (x0 - x1) / 2);
    add(x0, y0); add(cx, y0); add(cx, y1); add(x1, y1);
    return o;
  }
  if (p.length === 4 && p[2] < p[0] + 1) {
    // a back edge between items that are not side by side: out to the right, over, and in from the left
    const [x0, y0, x1, y1] = p;
    const up = Math.min(y0, y1) - 40;
    add(x0, y0); add(x0 + CH, y0); add(x0 + CH, up); add(x1 - CH, up); add(x1 - CH, y1); add(x1, y1);
    return o;
  }
  add(p[0], p[1]);
  for (let i = 2; i < p.length; i += 2) {
    const ax = p[i - 2], ay = p[i - 1], bx = p[i], by = p[i + 1];
    if (Math.abs(ay - by) > 0.5) {
      const cx = channelX(ax, ay, bx, by);
      add(cx, ay); add(cx, by);
    }
    add(bx, by);
  }
  return o;
}

// wiring draws all the routes of a graph together, as the wires of a schematic: where one wire crosses another
// without joining it, the horizontal one hops over the vertical one, and where one wire ends on another, or leaves
// the line of another, there is a dot. It returns the path data of each route and the places of the dots.
const HOP = 3.6;
export function wiring(routes) {
  const polys = new Map();
  const horiz = new Map(), verts = new Map();
  const put = (m, k, seg) => { let a = m.get(k); if (!a) m.set(k, (a = [])); a.push(seg); };
  const vlist = [];
  for (const [key, r] of routes) {
    const q = polyOf(r);
    polys.set(key, q);
    for (let i = 2; i < q.length; i += 2) {
      const x0 = q[i - 2], y0 = q[i - 1], x1 = q[i], y1 = q[i + 1];
      if (Math.abs(y0 - y1) < 0.01 && Math.abs(x0 - x1) > 0.01) put(horiz, Math.round(y0), { key, y: y0, a: Math.min(x0, x1), b: Math.max(x0, x1) });
      else if (Math.abs(x0 - x1) < 0.01 && Math.abs(y0 - y1) > 0.01) { const sg = { key, x: x0, a: Math.min(y0, y1), b: Math.max(y0, y1) }; put(verts, Math.round(x0), sg); vlist.push(sg); }
    }
  }
  vlist.sort((p, q) => p.x - q.x);
  const lowerBound = (x) => { let lo = 0, hi = vlist.length; while (lo < hi) { const m = (lo + hi) >> 1; if (vlist[m].x < x) lo = m + 1; else hi = m; } return lo; };
  const out = new Map();
  for (const [key, q] of polys) {
    let d = `M${f1(q[0])} ${f1(q[1])}`;
    for (let i = 2; i < q.length; i += 2) {
      const x0 = q[i - 2], y0 = q[i - 1], x1 = q[i], y1 = q[i + 1];
      if (Math.abs(y0 - y1) < 0.01 && Math.abs(x1 - x0) > 4 * HOP) {
        const sgn = x1 > x0 ? 1 : -1;
        const lo = Math.min(x0, x1) + HOP + 2, hi = Math.max(x0, x1) - HOP - 2;
        const xs = [];
        for (let j = lowerBound(lo); j < vlist.length && vlist[j].x <= hi; j++) {
          const v = vlist[j];
          if (v.key !== key && y0 > v.a + 0.5 && y0 < v.b - 0.5) xs.push(v.x);
        }
        xs.sort((p, q2) => sgn * (p - q2));
        let last = -Infinity;
        for (const x of xs) {
          if (sgn * (x - last) < 2 * HOP + 1) continue;
          last = x;
          d += `L${f1(x - sgn * HOP)} ${f1(y0)}A${HOP} ${HOP} 0 0 ${sgn > 0 ? 1 : 0} ${f1(x + sgn * HOP)} ${f1(y0)}`;
        }
      }
      d += `L${f1(x1)} ${f1(y1)}`;
    }
    out.set(key, d);
  }
  // a dot where a wire turns or ends on the line of another wire, which goes on past it
  const dots = new Map();
  const onLine = (m, c, test) => {
    for (const k of [c - 1, c, c + 1]) { const a = m.get(k); if (a) for (const sg of a) if (test(sg)) return true; }
    return false;
  };
  for (const [key, q] of polys) {
    for (let i = 2; i < q.length - 2; i += 2) {
      const x = q[i], y = q[i + 1];
      const hit = onLine(horiz, Math.round(y), (g) => g.key !== key && Math.abs(g.y - y) < 0.6 && x > g.a + 0.6 && x < g.b - 0.6)
        || onLine(verts, Math.round(x), (g) => g.key !== key && Math.abs(g.x - x) < 0.6 && y > g.a + 0.6 && y < g.b - 0.6);
      if (hit) dots.set(Math.round(x) + ',' + Math.round(y), [x, y]);
    }
  }
  return { d: out, dots: [...dots.values()] };
}

// pathOf is the path data of a route: straight lines, sharp corners.
export function pathOf(r) {
  const q = polyOf(r);
  let d = `M${f1(q[0])} ${f1(q[1])}`;
  for (let i = 2; i < q.length; i += 2) d += `L${f1(q[i])} ${f1(q[i + 1])}`;
  return d;
}

// midOf is the middle of a route by length, where the count of a merged edge is written.
export function midOf(r) {
  const q = polyOf(r);
  const total = lenOf(q);
  let at = total / 2;
  for (let i = 2; i < q.length; i += 2) {
    const l = Math.hypot(q[i] - q[i - 2], q[i + 1] - q[i - 1]);
    if (at <= l || i === q.length - 2) { const t = l ? Math.min(1, at / l) : 0; return [q[i - 2] + (q[i] - q[i - 2]) * t, q[i - 1] + (q[i + 1] - q[i - 1]) * t]; }
    at -= l;
  }
  return [q[0], q[1]];
}

function lenOf(q) {
  let n = 0;
  for (let i = 2; i < q.length; i += 2) n += Math.hypot(q[i] - q[i - 2], q[i + 1] - q[i - 1]);
  return n;
}

// lengthOf is the length of a route, for the weight of an edge at rest.
export function lengthOf(r) {
  return lenOf(polyOf(r));
}
