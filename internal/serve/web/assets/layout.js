// Layered left-to-right layout with nested module lanes, shaped to the canvas. Pure functions, no DOM.
//
// Ranks are the longest path over the visible items. A module that is open is a lane: a box that holds a
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
//           (or columns) they were in, when that costs little of the fit: a module that opens or closes does not
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
    const take = (sc, kind, offs, w, h) => { if (sc < bestScore - 1e-9) { bestScore = sc; best = { kind, offs, w, h }; } };
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
  // with the entries that are new beside the block (where a module that has opened stood as a card). It
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

// ---- routing ----
//
// A route is { m, p }: a mode and a flat list of points. Between two points the path is an S-curve whose
// tangents point the way the edge goes. Mode "h" leaves the right side of the dependency and enters the left
// side of the dependent: the plain curve unless that meets an item, in which case it passes through a gap
// between items (two points that lead the line straight across the gap, found by trying the gaps of the
// vertical strip in the middle of the edge and keeping the one with the fewest meetings; a half that still
// meets an item is routed again). Mode "l" goes the other way, left side to right side, and mode "v" leaves
// the bottom or the top, for items that sit one over the other.

const CELL = 96;
const FAR = 96;
const bulgeOf = (x0, y0, x1, y1) => Math.max(x0, x1) + 22 + Math.min(46, Math.abs(y1 - y0) * 0.035);
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
  // the number of samples of the S-curve from (ax, ay) to (bx, by) that lie inside an item
  const hits = (ax, ay, bx, by, sa, sb) => {
    if (bx - ax < 24) return 0;
    const dx = Math.max(8, (bx - ax) / 2);
    let n = 0;
    for (let i = 2; i < 23; i++) {
      const t = i / 24, u = 1 - t;
      const px = u * u * u * ax + 3 * u * u * t * (ax + dx) + 3 * u * t * t * (bx - dx) + t * t * t * bx;
      const py = u * u * u * ay + 3 * u * u * t * ay + 3 * u * t * t * by + t * t * t * by;
      if (at(px, py, sa, sb)) n++;
    }
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

  const routes = new Map();
  let ei = 0;
  for (const e of E) {
    const A = pos.get(e.a), B = pos.get(e.b);
    ei++;
    const key = e.a + '>' + e.b;
    const gapR = B.x - (A.x + A.w), gapL = A.x - (B.x + B.w);
    if (gapR >= 20) {
      const x0 = A.x + A.w, y0 = A.y + A.h / 2, x1 = B.x, y1 = B.y + B.h / 2;
      routes.set(key, { m: 'h', p: isBack(e.a, e.b) ? [x0, y0, x1, y1] : refine(x0, y0, x1, y1, e.a, e.b, 0, ei) });
    } else if (gapL >= 20) {
      routes.set(key, { m: 'l', p: [A.x, A.y + A.h / 2, B.x + B.w, B.y + B.h / 2] });
    } else if (B.y >= A.y + A.h + FAR || A.y >= B.y + B.h + FAR) {
      // far apart, one over the other: down the side, where nothing stands, rather than through what is between
      routes.set(key, { m: 'c', p: [A.x + A.w, A.y + A.h / 2, B.x + B.w, B.y + B.h / 2] });
    } else if (B.y >= A.y + A.h - 1) {
      routes.set(key, { m: 'v', p: [A.x + A.w / 2, A.y + A.h, B.x + B.w / 2, B.y] });
    } else if (A.y >= B.y + B.h - 1) {
      routes.set(key, { m: 'v', p: [A.x + A.w / 2, A.y, B.x + B.w / 2, B.y + B.h] });
    } else {
      routes.set(key, { m: 'h', p: [A.x + A.w, A.y + A.h / 2, B.x, B.y + B.h / 2] });
    }
  }
  return routes;
}

const f1 = (n) => Math.round(n * 10) / 10;

// pathOf turns a route into path data: S-curves whose tangents point the way the edge goes.
export function pathOf(r) {
  const p = r.p;
  if (r.m === 'c') {
    const [x0, y0, x1, y1] = p;
    const bx = bulgeOf(x0, y0, x1, y1);
    return `M${f1(x0)} ${f1(y0)}C${f1(bx)} ${f1(y0)} ${f1(bx)} ${f1(y1)} ${f1(x1)} ${f1(y1)}`;
  }
  if (r.m === 'v') {
    const [x0, y0, x1, y1] = p;
    const s = y1 >= y0 ? 1 : -1;
    const dy = Math.max(12, Math.abs(y1 - y0) / 2);
    return `M${f1(x0)} ${f1(y0)}C${f1(x0)} ${f1(y0 + s * dy)} ${f1(x1)} ${f1(y1 - s * dy)} ${f1(x1)} ${f1(y1)}`;
  }
  if (r.m === 'l') {
    const [x0, y0, x1, y1] = p;
    const dx = Math.max(12, Math.abs(x0 - x1) / 2);
    return `M${f1(x0)} ${f1(y0)}C${f1(x0 - dx)} ${f1(y0)} ${f1(x1 + dx)} ${f1(y1)} ${f1(x1)} ${f1(y1)}`;
  }
  if (p.length === 4 && p[2] < p[0] + 1) {
    // a back edge between items that are not side by side: loop out to the right, over and in
    const [x0, y0, x1, y1] = p;
    const up = Math.min(y0, y1) - 40;
    return `M${f1(x0)} ${f1(y0)}C${f1(x0 + 60)} ${f1(y0)} ${f1(x0 + 60)} ${f1(up)} ${f1((x0 + x1) / 2)} ${f1(up)}S${f1(x1 - 60)} ${f1(y1)} ${f1(x1)} ${f1(y1)}`;
  }
  let d = `M${f1(p[0])} ${f1(p[1])}`;
  for (let i = 2; i < p.length; i += 2) {
    const ax = p[i - 2], ay = p[i - 1], bx = p[i], by = p[i + 1];
    const dx = Math.max(8, (bx - ax) / 2);
    d += `C${f1(ax + dx)} ${f1(ay)} ${f1(bx - dx)} ${f1(by)} ${f1(bx)} ${f1(by)}`;
  }
  return d;
}

// midOf is the midpoint of a route, where the count of a merged edge is written.
export function midOf(r) {
  const p = r.p;
  if (r.m === 'c') return [(p[0] + 6 * bulgeOf(p[0], p[1], p[2], p[3]) + p[2]) / 8, (p[1] + p[3]) / 2];
  if (p.length === 4) return [(p[0] + p[2]) / 2, (p[1] + p[3]) / 2];
  const k = (p.length / 2 - 1) >> 1;
  const i = k * 2;
  return [(p[i] + p[i + 2]) / 2, (p[i + 1] + p[i + 3]) / 2];
}

// lengthOf is the rough length of a route, for the weight of an edge at rest.
export function lengthOf(r) {
  const p = r.p;
  let n = 0;
  for (let i = 2; i < p.length; i += 2) n += Math.hypot(p[i] - p[i - 2], p[i + 1] - p[i - 1]);
  return n;
}
