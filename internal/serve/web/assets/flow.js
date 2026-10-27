// The layered layout of the graph: columns by depth, wires in reserved lanes. Pure functions, no DOM.
//
// Every lane (an open namespace; the page itself is the lane at the root) is laid out on its own, from the inside
// out, and then stands in the lane above as one box. A lane is a layered graph:
//
//   ranks     a node is in the column after the last of the nodes that it reads; what reads nothing stands next to
//             the first that reads it. An edge that closes a cycle is the only one that may run the other way.
//   strands   one wire leaves each item and branches to every item that reads it (a bus). A strand that has to cross
//             a column passes through it at a free height, as a node of its own that has no width (a dummy).
//   order     the nodes of a column are ordered by barycenter sweeps, keeping the count of crossings that is least.
//   heights   Brandes and Koepf: long strands run straight, and every node stands level with its neighbours. A last pass
//             moves what stands level to the height of a neighbour where that saves a turn, and a wire leaves and
//             arrives at the height that it runs at, when that is within the item.
//   gutters   between two columns, a wire that changes height does so on a line of its own in the gutter. The lines
//             are taken in an order that crosses the fewest wires; the gutter is as wide as the lines it holds.
//   ports     a strand that crosses the border of a lane goes in at the left edge or out at the right edge, at a
//             height that the lane chose for it, so the wire is one line from the item that is read to the item that
//             reads it, whatever lanes stand between.
//
// A wire therefore runs to the right and turns only at the gutters, up or down; the arrowhead is always on a
// horizontal run into the left edge of the node that reads. The edges that close a cycle go back along a line
// above the nodes of their lane.

const TRK = 8; // between two wires that turn in the same gutter
const PADG = 17; // from a column to the first line of the gutter that follows it
const GUTMIN = 46;
const GUTLITE = 50;
const GAPRR = 12; // between two nodes of a column
const GAPRD = 14; // between a node and a wire that passes it
const GAPDD = 8; // between two wires that pass a column
const RET0 = 10; // the first line above the nodes for the edges that go back
const RETSTEP = 7;
const MAXPIN = 8; // between two wires that end on the same node
const CAPF = 1.15; // the picture is at most this much taller than its tallest column, nodes and wires together

const f2 = (n) => Math.round(n * 100) / 100;

// ---- order of the nodes in the columns ----

function countCrossings(layers) {
  let total = 0;
  for (let l = 0; l + 1 < layers.length; l++) {
    const lo = layers[l], hi = layers[l + 1];
    if (!lo.length || !hi.length) continue;
    const tree = new Int32Array(hi.length + 2);
    let seen = 0;
    for (const u of lo) {
      if (!u.dn.length) continue;
      const ps = u.dn.map((v) => v.pos).sort((a, b) => a - b);
      for (const p of ps) {
        let s = 0;
        for (let i = p + 1; i > 0; i -= i & -i) s += tree[i];
        total += seen - s;
        for (let i = p + 1; i <= hi.length; i += i & -i) tree[i]++;
        seen++;
      }
    }
  }
  return total;
}

function reorder(layer, up) {
  const keyed = [];
  for (const v of layer) {
    if (v.glue) continue;
    const nb = up ? v.up : v.dn;
    let b = v.pos;
    if (nb.length) { b = 0; for (const w of nb) b += w.pos; b /= nb.length; }
    keyed.push({ v, b });
  }
  keyed.sort((p, q) => p.b - q.b || p.v.pos - q.v.pos);
  layer.length = 0;
  for (const k of keyed) {
    layer.push(k.v);
    if (k.v.fol) for (const f of k.v.fol) layer.push(f);
  }
  for (let i = 0; i < layer.length; i++) layer[i].pos = i;
}

// transpose swaps two neighbours in a column whenever that takes crossings away
function transpose(layers) {
  let swapped = true;
  for (let pass = 0; swapped && pass < 12; pass++) {
    swapped = false;
    for (const L of layers) {
      for (let i = 0; i + 1 < L.length; i++) {
        const u = L[i], v = L[i + 1];
        if (u.glue || v.glue || u.fol || v.fol) continue;
        let uv = 0, vu = 0;
        for (const a of u.up) for (const b of v.up) { if (a.pos > b.pos) uv++; else if (a.pos < b.pos) vu++; }
        for (const a of u.dn) for (const b of v.dn) { if (a.pos > b.pos) uv++; else if (a.pos < b.pos) vu++; }
        if (vu < uv) { L[i] = v; L[i + 1] = u; v.pos = i; u.pos = i + 1; swapped = true; }
      }
    }
  }
}

function orderLayers(layers, rounds) {
  const setPos = () => { for (const L of layers) for (let i = 0; i < L.length; i++) L[i].pos = i; };
  setPos();
  let best = countCrossings(layers);
  if (!best) return 0;
  let snap = layers.map((L) => L.slice());
  let stale = 0;
  for (let it = 0; it < rounds && best > 0 && stale < 5; it++) {
    for (let l = 1; l < layers.length; l++) reorder(layers[l], true);
    for (let l = layers.length - 2; l >= 0; l--) reorder(layers[l], false);
    transpose(layers);
    const c = countCrossings(layers);
    if (c < best) { best = c; snap = layers.map((L) => L.slice()); stale = 0; } else stale++;
  }
  layers.forEach((L, l) => { L.length = 0; for (const v of snap[l]) L.push(v); });
  setPos();
  return best;
}

// ---- heights: Brandes and Koepf, "Fast and Simple Horizontal Coordinate Assignment" ----

const sepOf = (u, v) => (u.h + v.h) / 2 + (u.h && v.h ? GAPRR : u.h || v.h ? GAPRD : GAPDD);

function placeY(layers, V) {
  const n = V.length;
  const out = new Float64Array(n);
  if (!n) return out;
  const key = (u, v) => u.i * n + v.i;
  // type 1 conflicts: an edge that crosses a segment between two dummies
  const conflict = new Set();
  for (let l = 1; l < layers.length; l++) {
    const prev = layers[l - 1], layer = layers[l];
    let k0 = 0, scan = 0;
    for (let i = 0; i < layer.length; i++) {
      const v = layer[i];
      let w = null;
      if (v.kind === 'dummy') for (const u of v.up) if (u.kind === 'dummy') { w = u; break; }
      const k1 = w ? w.pos : prev.length;
      if (w || i === layer.length - 1) {
        for (let j = scan; j <= i; j++) {
          const sn = layer[j];
          for (const u of sn.up) if ((u.pos < k0 || k1 < u.pos) && !(u.kind === 'dummy' && sn.kind === 'dummy')) conflict.add(key(u, sn));
        }
        scan = i + 1;
        k0 = k1;
      }
    }
  }
  const runs = [];
  for (const down of [false, true]) {
    for (const left of [true, false]) {
      const L = layers.map((x) => (left ? x.slice() : x.slice().reverse()));
      if (down) L.reverse();
      const root = new Int32Array(n), align = new Int32Array(n), apos = new Int32Array(n), pred = new Int32Array(n).fill(-1);
      for (const layer of L) layer.forEach((v, i) => { root[v.i] = v.i; align[v.i] = v.i; apos[v.i] = i; if (i) pred[v.i] = layer[i - 1].i; });
      for (const layer of L) {
        let prevIdx = -1;
        for (const v of layer) {
          const ws = (down ? v.dn : v.up).slice().sort((a, b) => apos[a.i] - apos[b.i]);
          if (!ws.length) continue;
          const mp = (ws.length - 1) / 2;
          for (let i = Math.floor(mp), il = Math.ceil(mp); i <= il; i++) {
            const w = ws[i];
            if (align[v.i] === v.i && prevIdx < apos[w.i] && !conflict.has(key(w, v)) && !conflict.has(key(v, w))) {
              align[w.i] = v.i;
              root[v.i] = root[w.i];
              align[v.i] = root[v.i];
              prevIdx = apos[w.i];
            }
          }
        }
      }
      const sink = new Int32Array(n), shift = new Float64Array(n).fill(Infinity), x = new Float64Array(n);
      for (let i = 0; i < n; i++) sink[i] = i;
      // placing a block is iterative: a layer of a thousand nodes would otherwise be a recursion a thousand deep
      const state = new Uint8Array(n);
      const place = (r0) => {
        const stack = [r0];
        while (stack.length) {
          const v = stack[stack.length - 1];
          if (state[v] === 2) { stack.pop(); continue; }
          if (state[v] === 0) {
            state[v] = 1;
            let w = v;
            do {
              if (pred[w] >= 0) { const u = root[pred[w]]; if (state[u] !== 2) stack.push(u); }
              w = align[w];
            } while (w !== v);
            continue;
          }
          // the blocks of all the predecessors are placed
          x[v] = 0;
          let w = v;
          do {
            if (pred[w] >= 0) {
              const u = root[pred[w]];
              if (sink[v] === v) sink[v] = sink[u];
              const d = sepOf(V[pred[w]], V[w]);
              if (sink[v] !== sink[u]) shift[sink[u]] = Math.min(shift[sink[u]], x[v] - x[u] - d);
              else x[v] = Math.max(x[v], x[u] + d);
            }
            w = align[w];
          } while (w !== v);
          state[v] = 2;
          stack.pop();
        }
      };
      for (let i = 0; i < n; i++) if (root[i] === i) place(i);
      const xs = new Float64Array(n);
      for (let i = 0; i < n; i++) {
        const r = root[i];
        xs[i] = x[r];
        const s = shift[sink[r]];
        if (s < Infinity) xs[i] += s;
        if (!left) xs[i] = -xs[i];
      }
      runs.push({ xs, left });
    }
  }
  let small = runs[0], sw = Infinity;
  for (const r of runs) {
    let lo = Infinity, hi = -Infinity;
    for (let i = 0; i < n; i++) { lo = Math.min(lo, r.xs[i]); hi = Math.max(hi, r.xs[i]); }
    r.lo = lo; r.hi = hi;
    if (hi - lo < sw) { sw = hi - lo; small = r; }
  }
  for (const r of runs) {
    const d = r.left ? small.lo - r.lo : small.hi - r.hi;
    for (let i = 0; i < n; i++) r.xs[i] += d;
  }
  for (let i = 0; i < n; i++) {
    const t = [runs[0].xs[i], runs[1].xs[i], runs[2].xs[i], runs[3].xs[i]].sort((a, b) => a - b);
    out[i] = (t[1] + t[2]) / 2;
  }
  // the median of four keeps the order, but hold the gaps
  for (const L of layers) for (let i = 1; i < L.length; i++) out[L[i].i] = Math.max(out[L[i].i], out[L[i - 1].i] + sepOf(L[i - 1], L[i]));
  {
    let packed = 0;
    for (const L of layers) { let p = 0; for (let i = 0; i < L.length; i++) { p += L[i].h; if (i) p += sepOf(L[i - 1], L[i]) - (L[i - 1].h + L[i].h) / 2; } packed = Math.max(packed, p); }
    compress(layers, out, packed * CAPF);
  }
  straighten(layers, V, out);
  gravity(layers, V, out);
  straighten(layers, V, out);
  return out;
}

// compress holds the picture to a height: when the heights that the alignment gave stand further apart than cap allows,
// the columns are scaled to it and the nodes of each column are set apart again as little as that takes (the closest
// fit, in the least squares, that keeps their order and their gaps).
function compress(layers, y, cap) {
  let top = Infinity, bot = -Infinity;
  for (const L of layers) for (const v of L) { top = Math.min(top, y[v.i] - v.h / 2); bot = Math.max(bot, y[v.i] + v.h / 2); }
  const span = bot - top;
  if (!(span > cap)) return;
  const a = cap / span;
  for (const L of layers) {
    const m = L.length;
    if (!m) continue;
    const S = new Array(m).fill(0);
    for (let i = 1; i < m; i++) S[i] = S[i - 1] + sepOf(L[i - 1], L[i]);
    // pool adjacent violators on the targets less the minimum offsets
    const blocks = [];
    for (let i = 0; i < m; i++) {
      const tau = top + (y[L[i].i] - top) * a - S[i];
      blocks.push({ sum: tau, n: 1 });
      while (blocks.length > 1) {
        const q = blocks[blocks.length - 1], p = blocks[blocks.length - 2];
        if (p.sum / p.n <= q.sum / q.n) break;
        p.sum += q.sum; p.n += q.n; blocks.pop();
      }
    }
    let i = 0;
    for (const b of blocks) for (let k = 0; k < b.n; k++, i++) y[L[i].i] = b.sum / b.n + S[i];
    // inside the box
    const lo = y[L[0].i] - L[0].h / 2 - top, hi = top + cap - (y[L[m - 1].i] + L[m - 1].h / 2);
    const d = lo < 0 ? -lo : hi < 0 ? hi : 0;
    if (d) for (const v of L) y[v.i] += d;
  }
}

// gravity closes the holes that the alignment leaves. Nodes that stand level with their neighbours (a straight run
// of wire) move together; each such group moves, as far as the gaps in its columns let it, to the height at which
// the wires that leave it by a turn are shortest, the median of their lengths. A group that lands level with a
// neighbour is one run longer.
function gravity(layers, V, y) {
  const n = V.length;
  const up = new Int32Array(n).map((_, i) => i);
  const find = (a) => { while (up[a] !== a) { up[a] = up[up[a]]; a = up[a]; } return a; };
  const level = () => { for (let i = 0; i < n; i++) up[i] = i; for (const v of V) for (const w of v.dn) if (Math.abs(y[v.i] - y[w.i]) < 0.75) up[find(v.i)] = find(w.i); };
  level();
  const members = new Map();
  for (const v of V) { const r = find(v.i); let a = members.get(r); if (!a) members.set(r, (a = [])); a.push(v); }
  const groups = [...members.values()];
  const gid = new Int32Array(n);
  groups.forEach((g, k) => { for (const v of g) gid[v.i] = k; });
  const above = new Array(n), below = new Array(n);
  for (const L of layers) L.forEach((v, i) => { above[v.i] = i ? L[i - 1] : null; below[v.i] = i + 1 < L.length ? L[i + 1] : null; });
  const MU = 0.6;
  const order = groups.map((_, k) => k);
  const gy = (k) => y[groups[k][0].i];
  for (let it = 0; it < 60; it++) {
    let moved = 0;
    let lo = Infinity, hi = -Infinity;
    for (const v of V) { lo = Math.min(lo, y[v.i] - v.h / 2); hi = Math.max(hi, y[v.i] + v.h / 2); }
    const mid = (lo + hi) / 2;
    order.sort((p, q) => (it % 2 ? gy(q) - gy(p) : gy(p) - gy(q)));
    for (const k of order) {
      const g = groups[k];
      const offs = [];
      let upRoom = Infinity, downRoom = Infinity;
      for (const v of g) {
        for (const w of v.up) if (gid[w.i] !== k) offs.push([y[w.i] - y[v.i], 1]);
        for (const w of v.dn) if (gid[w.i] !== k) offs.push([y[w.i] - y[v.i], 1]);
        const a = above[v.i], b = below[v.i];
        if (a && gid[a.i] !== k) upRoom = Math.min(upRoom, y[v.i] - y[a.i] - sepOf(a, v));
        if (b && gid[b.i] !== k) downRoom = Math.min(downRoom, y[b.i] - y[v.i] - sepOf(v, b));
      }
      if (!offs.length) continue;
      // the median of the lengths of the wires that turn, with a weak pull towards the middle of the picture
      if (MU > 0) offs.push([mid - gy(k), MU * g.filter((v) => v.h).length]);
      offs.sort((p, q) => p[0] - q[0]);
      let total = 0;
      for (const o of offs) total += o[1];
      let acc = 0, d = 0;
      for (let i = 0; i < offs.length; i++) {
        acc += offs[i][1];
        if (acc >= total / 2 - 1e-9) { d = acc - offs[i][1] > total / 2 - 1e-9 && i ? (offs[i - 1][0] + offs[i][0]) / 2 : offs[i][0]; if (Math.abs(acc - total / 2) < 1e-9 && i + 1 < offs.length) d = (offs[i][0] + offs[i + 1][0]) / 2; break; }
      }
      d = Math.max(-upRoom, Math.min(downRoom, d));
      if (Math.abs(d) < 0.01) continue;
      for (const v of g) y[v.i] += d;
      moved++;
    }
    if (!moved) break;
  }
  // what stands level stands exactly level
  level();
  const base = new Map();
  for (const v of V) { const r = find(v.i); if (!base.has(r)) base.set(r, y[v.i]); }
  for (const v of V) y[v.i] = base.get(find(v.i));
}

// straighten takes a run of wire that stands level, with the nodes that stand level with it, and moves it to the height of one
// of its neighbours when that leaves fewer turns: a wire that steps down in every column goes straight and turns once, where
// it must. It moves a run only as far as the nodes above and below it let it, and never out of the picture, so the order of
// the columns and the height of the picture are the ones that the alignment gave. Of the places that leave the fewest turns the
// nearest wins, and the sum of the lengths of the turns breaks a tie.
function straighten(layers, V, y) {
  const n = V.length;
  if (!n) return;
  const up = new Int32Array(n);
  const find = (a) => { while (up[a] !== a) { up[a] = up[up[a]]; a = up[a]; } return a; };
  const EPS = 0.25;
  // a wire may leave and arrive anywhere on the side of an item but its corners, so two items stand level enough when they are
  // not further apart than that
  const reach = (v) => (v.kind === 'item' ? Math.max(v.h / 2 - 8, 0) : 0);
  const above = new Array(n), below = new Array(n);
  for (const L of layers) L.forEach((v, i) => { above[v.i] = i ? L[i - 1] : null; below[v.i] = i + 1 < L.length ? L[i + 1] : null; });
  let lo = Infinity, hi = -Infinity;
  for (const v of V) { lo = Math.min(lo, y[v.i] - v.h / 2); hi = Math.max(hi, y[v.i] + v.h / 2); }
  for (let pass = 0; pass < 24; pass++) {
    for (let i = 0; i < n; i++) up[i] = i;
    for (const v of V) for (const w of v.dn) if (Math.abs(y[v.i] - y[w.i]) < EPS) up[find(v.i)] = find(w.i);
    const members = new Map();
    for (const v of V) { const r = find(v.i); let a = members.get(r); if (!a) members.set(r, (a = [])); a.push(v); }
    const gid = new Int32Array(n);
    const groups = [...members.values()];
    groups.forEach((g, k) => { for (const v of g) gid[v.i] = k; });
    groups.sort((p, q) => (pass % 2 ? y[q[0].i] - y[p[0].i] : y[p[0].i] - y[q[0].i]));
    groups.forEach((g, k) => { for (const v of g) gid[v.i] = k; });
    let moved = 0;
    for (let k = 0; k < groups.length; k++) {
      const g = groups[k];
      const offs = [];
      let upRoom = Infinity, downRoom = Infinity, top = Infinity, bot = -Infinity;
      for (const v of g) {
        for (const w of v.up) if (gid[w.i] !== k) offs.push([y[w.i] - y[v.i], reach(v) + reach(w)]);
        for (const w of v.dn) if (gid[w.i] !== k) offs.push([y[w.i] - y[v.i], reach(v) + reach(w)]);
        const a = above[v.i], b = below[v.i];
        if (a && gid[a.i] !== k) upRoom = Math.min(upRoom, y[v.i] - y[a.i] - sepOf(a, v));
        if (b && gid[b.i] !== k) downRoom = Math.min(downRoom, y[b.i] - y[v.i] - sepOf(v, b));
        top = Math.min(top, y[v.i] - v.h / 2); bot = Math.max(bot, y[v.i] + v.h / 2);
      }
      if (!offs.length) continue;
      upRoom = Math.min(upRoom, top - lo);
      downRoom = Math.min(downRoom, hi - bot);
      const cost = (d) => { let c = 0, l = 0; for (const [o, r] of offs) { const t = Math.abs(o - d) - r; if (t > EPS) { c++; l += t; } } return [c, l]; };
      const [c0, l0] = cost(0);
      let best = 0, bc = c0, bl = l0;
      for (const [o, r] of offs) {
        for (const e of r ? [o - r, o, o + r] : [o]) {
        const d = Math.max(-upRoom, Math.min(downRoom, e));
        if (Math.abs(d) < EPS) continue;
        const [c, l] = cost(d);
        if (c < bc || (c === bc && l < bl - 1e-9 && c < c0) || (c === bc && c < c0 && l === bl && Math.abs(d) < Math.abs(best))) { best = d; bc = c; bl = l; }
        }
      }
      if (bc < c0 && Math.abs(best) >= EPS) {
        for (const v of g) y[v.i] += best;
        moved++;
      }
    }
    if (!moved) break;
  }
  // what stands level stands exactly level
  for (let i = 0; i < n; i++) up[i] = i;
  for (const v of V) for (const w of v.dn) if (Math.abs(y[v.i] - y[w.i]) < EPS) up[find(v.i)] = find(w.i);
  const base = new Map();
  for (const v of V) { const r = find(v.i); if (!base.has(r)) base.set(r, y[v.i]); }
  for (const v of V) y[v.i] = base.get(find(v.i));
}

// ---- lines of a gutter ----

// assignTracks takes the vertical runs of a gutter, { lo, hi, yin, outs, ord }: a wire comes in from the left at yin, turns
// on its run between lo and hi and goes out to the right at each of outs. It sets t, the number of the line (from 1, left
// to right), for each, so that runs that share heights stand on different lines and the fewest wires cross a run. It
// returns the number of lines.
function assignTracks(list) {
  const n = list.length;
  if (!n) return 0;
  const M = 4;
  const ov = (a, b) => a.lo < b.hi + M && b.lo < a.hi + M;
  const inside = (y, a) => y > a.lo + 0.5 && y < a.hi - 0.5;
  // the wires that a run of b on the right of a would cross
  const cost = (a, b) => { let c = inside(b.yin, a) ? 1 : 0; for (const y of a.outs) if (inside(y, b)) c++; return c; };
  list.sort((p, q) => p.lo - q.lo || p.ord - q.ord);
  const idx = list.map((_, i) => i);
  if (n <= 220) {
    const score = new Float64Array(n);
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n && list[j].lo < list[i].hi + M; j++) {
        if (!ov(list[i], list[j])) continue;
        const ab = cost(list[i], list[j]), ba = cost(list[j], list[i]);
        if (ab < ba) { score[j] += 1; score[i] -= 1; } else if (ba < ab) { score[i] += 1; score[j] -= 1; }
      }
    }
    idx.sort((p, q) => score[p] - score[q] || p - q);
  }
  const placed = [];
  let T = 0;
  for (const i of idx) {
    const a = list[i];
    let t = 1;
    for (const b of placed) if (ov(a, b) && b.t >= t) t = b.t + 1;
    a.t = t;
    if (t > T) T = t;
    placed.push(a);
  }
  return T;
}

// ---- the layout ----

// flow takes { root, items, edges, aspect, fit, route, sweeps } and the sizes G. It returns
// { pos, lanes, routes, back, bounds, rank, maxRank }, which layout.js describes.
export function flow(args, G) {
  const { root, items } = args;
  const lite = args.route === false;
  const aspect = args.aspect > 0 ? args.aspect : args.fit && args.fit.w > 0 && args.fit.h > 0 ? args.fit.w / args.fit.h : 1.6;
  const PADX = G.PAD + 4, PADT = 10, PADB = G.PAD;
  const labelWidth = (l) => Math.ceil(l.label.length * 8.4 + 92);

  // ---- lanes ----
  const lanes = [];
  const home = new Map();
  const mk = (src, parent, depth, top) => {
    const l = { id: 'L:' + src.path, path: src.path, label: src.label || '', parent, depth, top, virtual: !parent, items: src.items.filter((id) => items.has(id)), sub: [], all: new Set(), at: new Map() };
    lanes.push(l);
    for (const id of l.items) { home.set(id, l); l.all.add(id); l.at.set(id, id); }
    src.sub.forEach((sb, i) => {
      const c = mk(sb, l, depth + 1, parent ? top : i);
      l.sub.push(c);
      for (const id of c.all) { l.all.add(id); l.at.set(id, c.id); }
    });
    return l;
  };
  const R = mk(root, null, 0, -1);
  const E = [];
  for (const e of args.edges) if (e.a !== e.b && home.has(e.a) && home.has(e.b)) E.push(e);

  const backSet = new Set();

  // build lays out one lane (its sub-lanes first) and sets its size: w, h, and where its block stands in the box: ox, oy.
  function build(L, EL) {
    const rel = L.virtual ? EL : EL.filter((e) => L.all.has(e.a) || L.all.has(e.b));
    for (const sl of L.sub) build(sl, rel);
    if (!L.virtual) { arrange(L, rel, Infinity, lite); return; }
    // The page itself takes the shape of the canvas. A column that is taller than a limit lets some of its nodes
    // stand in the next column, and the nodes that read them in the columns after it, so the picture is wider and
    // lower; the limit that gives the largest zoom on the canvas wins. Whatever stands right of what it reads stays so.
    const box = args.fit && args.fit.w > 0 && args.fit.h > 0 ? args.fit : { w: aspect * 600, h: 600 };
    const kOf = (r) => Math.min(1.25, box.w / Math.max(r.W, 1), box.h / Math.max(r.H * 1.12, 1));
    const base = arrange(L, rel, Infinity, true);
    let best = { hmax: Infinity, k: kOf(base) };
    if (best.k < 1.25 && base.tall > 0) {
      for (const f of [0.85, 0.72, 0.62, 0.52, 0.44, 0.37, 0.3]) {
        const hm = Math.max(base.tall * f, base.unit);
        const r = arrange(L, rel, hm, true);
        const k = kOf(r);
        if (k > best.k * 1.02) best = { hmax: hm, k };
        if (hm <= base.unit) break;
      }
    }
    arrange(L, rel, best.hmax, lite);
  }

  // arrange lays out one lane whose sub-lanes are laid out already, with columns that are not taller than hmax. It
  // sets the size of the lane (w, h) and where its block stands in the box (ox, oy), and returns the size of the block;
  // with liteMode it only measures.
  function arrange(L, rel, hmax, liteMode) {
    const lite = liteMode;
    const at = L.at;

    // ---- nodes and strands ----
    const N = new Map();
    const real = [];
    const addReal = (n) => { n.ord = real.length; n.real = true; N.set(n.id, n); real.push(n); };
    for (const id of L.items) { const it = items.get(id); addReal({ id, kind: 'item', it, w: it.w || G.W, h: it.h || G.H }); }
    for (const sl of L.sub) addReal({ id: sl.id, kind: 'lane', lane: sl, w: sl.w, h: sl.h });
    const port = (kind, a) => {
      const id = kind + ':' + a;
      let p = N.get(id);
      if (!p) { p = { id, kind, a, w: 0, h: 0, ord: -1, real: false }; N.set(id, p); }
      return p;
    };
    const S = new Map();
    for (const e of rel) {
      const na = at.get(e.a), nb = at.get(e.b);
      if (na === undefined && nb === undefined) continue;
      if (na !== undefined && na === nb) continue;
      let s = S.get(e.a);
      if (!s) { s = { a: e.a, src: na !== undefined ? N.get(na) : port('in', e.a), cons: [], ord: S.size }; S.set(e.a, s); }
      const c = nb !== undefined ? N.get(nb) : port('out', e.a);
      if (!s.cons.includes(c)) s.cons.push(c);
    }
    const ins = [], outs = [];
    for (const n of N.values()) { if (n.kind === 'in') ins.push(n); else if (n.kind === 'out') outs.push(n); }
    const hasIN = ins.length > 0, hasOUT = outs.length > 0;

    // ---- ranks ----
    // An environment variable stands in the first column of its lane and an output in the last, whatever they read or
    // are read by: the outputs are ranked among themselves, after all the rest.
    const roleOf = (n) => {
      if (!n || n.kind !== 'item') return '';
      const x = n.it.kind === 'node' ? n.it.n : n.it;
      return x.mode === 'output' ? 'output' : x.type === 'env' || x.type === 'secret_env' ? 'env' : '';
    };
    const isOutR = (r) => roleOf(N.get(r)) === 'output';
    const isEnvR = (r) => roleOf(N.get(r)) === 'env';
    const rep = new Map();
    const fol = new Map();
    for (const n of real) {
      let r = n.id;
      if (n.kind === 'item' && n.it.with) {
        const w = at.get(n.it.with);
        if (w !== undefined && w !== n.id && N.has(w) && N.get(w).real && !(N.get(w).kind === 'item' && N.get(w).it.with)) r = w;
      }
      rep.set(n.id, r);
      if (r !== n.id) { let a = fol.get(r); if (!a) fol.set(r, (a = [])); a.push(n); }
    }
    const succ = new Map(), pred = new Map();
    const osucc = new Map(), opred = new Map();
    for (const n of real) {
      if (rep.get(n.id) !== n.id) continue;
      if (isOutR(n.id)) { osucc.set(n.id, new Set()); opred.set(n.id, new Set()); } else { succ.set(n.id, new Set()); pred.set(n.id, new Set()); }
    }
    const inCnt = new Map(), outCnt = new Map();
    const bump = (m, k) => m.set(k, (m.get(k) || 0) + 1);
    for (const s of S.values()) {
      for (const c of s.cons) {
        if (s.src.kind === 'in') { bump(inCnt, rep.get(c.id)); continue; }
        if (c.kind === 'out') { bump(outCnt, rep.get(s.src.id)); continue; }
        const u = rep.get(s.src.id), v = rep.get(c.id);
        if (isOutR(v)) {
          // what feeds an output stands as if it fed the right edge of the lane
          if (!isOutR(u)) bump(outCnt, u);
          else if (u !== v) { osucc.get(u).add(v); opred.get(v).add(u); }
          continue;
        }
        if (u !== v) { succ.get(u).add(v); pred.get(v).add(u); }
      }
    }
    // the edges that close a cycle
    const backs = new Set();
    {
      const reps = [...succ.keys()];
      const state = new Map();
      for (const r0 of reps.filter((r) => !pred.get(r).size).concat(reps)) {
        if (state.get(r0)) continue;
        state.set(r0, 1);
        const stack = [[r0, [...succ.get(r0)], 0]];
        while (stack.length) {
          const top = stack[stack.length - 1];
          if (top[2] < top[1].length) {
            const w = top[1][top[2]++];
            const c = state.get(w) || 0;
            if (c === 0) { state.set(w, 1); stack.push([w, [...succ.get(w)], 0]); } else if (c === 1) backs.add(top[0] + '\u0000' + w);
          } else { state.set(top[0], 2); stack.pop(); }
        }
      }
    }
    const fp = new Map(), fs = new Map();
    for (const r of succ.keys()) { fp.set(r, []); fs.set(r, []); }
    for (const [u, vs] of succ) for (const v of vs) if (!backs.has(u + '\u0000' + v)) { fs.get(u).push(v); fp.get(v).push(u); }
    const rank = new Map();
    const topo = [];
    {
      const indeg = new Map();
      for (const r of succ.keys()) { indeg.set(r, fp.get(r).length); rank.set(r, 0); }
      const q = [...succ.keys()].filter((r) => !indeg.get(r));
      for (let i = 0; i < q.length; i++) {
        const v = q[i];
        topo.push(v);
        for (const w of fs.get(v)) {
          if (rank.get(w) < rank.get(v) + 1) rank.set(w, rank.get(v) + 1);
          indeg.set(w, indeg.get(w) - 1);
          if (!indeg.get(w)) q.push(w);
        }
      }
    }
    const isIso = (r) => !isEnvR(r) && !succ.get(r).size && !pred.get(r).size && !inCnt.get(r) && !outCnt.get(r);
    let maxR = -1;
    for (const [r, k] of rank) if (!isIso(r)) maxR = Math.max(maxR, k);
    // a node with more wires in than out stands as near the nodes that feed it as it can; one with more out than in,
    // as near the nodes that read it; what reads nothing therefore stands next to the first that reads it
    for (let pass = 0; pass < 4; pass++) {
      let moved = false;
      for (const v of pass % 2 ? topo.slice().reverse() : topo) {
        if (isIso(v) || isEnvR(v)) continue;
        const ind = fp.get(v).length + (inCnt.get(v) || 0), outd = fs.get(v).length + (outCnt.get(v) || 0);
        if (ind === outd) continue;
        let lo = 0, hi = maxR;
        for (const p of fp.get(v)) lo = Math.max(lo, rank.get(p) + 1);
        for (const s of fs.get(v)) hi = Math.min(hi, rank.get(s) - 1);
        const to = outd > ind ? hi : lo;
        if (to !== rank.get(v) && to >= lo && to <= hi) { rank.set(v, to); moved = true; }
      }
      if (!moved) break;
    }
    // ---- columns that are not taller than hmax ----
    const unitH = (r) => { let h = N.get(r).h + GAPRR; for (const f of fol.get(r) || []) h += f.h + GAPRR; return h; };
    let unit = 0;
    for (const r of succ.keys()) if (!isIso(r)) unit = Math.max(unit, unitH(r) - GAPRR);
    for (const r of osucc.keys()) unit = Math.max(unit, unitH(r) - GAPRR);
    if (hmax < Infinity) {
      const reps = [...succ.keys()].filter((r) => !isIso(r));
      const passing = (l) => {
        let k = 0;
        for (const s of S.values()) {
          if (s.src.kind !== 'in' && isOutR(rep.get(s.src.id))) continue;
          const rs = s.src.kind === 'in' ? -1 : rank.get(rep.get(s.src.id));
          let rmax = -1;
          for (const c of s.cons) rmax = Math.max(rmax, c.kind === 'out' || isOutR(rep.get(c.id)) ? maxR + 1 : rank.get(rep.get(c.id)));
          if (l > rs && l < rmax) k++;
        }
        return k;
      };
      for (let l = 0; l <= maxR; l++) {
        const here = reps.filter((r) => rank.get(r) === l);
        let h = here.reduce((t, r) => t + unitH(r), 0) + passing(l) * GAPDD;
        let left = here.length;
        if (h <= hmax || left < 2) continue;
        const slack = (r) => { let m = Infinity; for (const x of fs.get(r)) m = Math.min(m, rank.get(x) - l - 1); return m; };
        const cand = here.filter((r) => !isEnvR(r)).sort((p, q) => slack(q) - slack(p) || N.get(q).ord - N.get(p).ord);
        for (const r of cand) {
          if (h <= hmax || left < 2) break;
          rank.set(r, l + 1);
          const stack = [r];
          while (stack.length) {
            const m = stack.pop();
            for (const x of fs.get(m)) if (rank.get(x) <= rank.get(m)) { rank.set(x, rank.get(m) + 1); stack.push(x); }
          }
          h -= unitH(r);
          left--;
        }
        for (const r of reps) maxR = Math.max(maxR, rank.get(r));
      }
      // what reads nothing goes back next to the first that reads it, where there is room
      const lh = new Array(maxR + 2).fill(0);
      for (const r of reps) lh[rank.get(r)] += unitH(r);
      for (const v of topo) {
        if (isIso(v) || isEnvR(v) || fp.get(v).length || inCnt.get(v) || !fs.get(v).length) continue;
        let to = Infinity;
        for (const x of fs.get(v)) to = Math.min(to, rank.get(x) - 1);
        const from = rank.get(v);
        if (to > from && lh[to] + unitH(v) <= hmax) { lh[from] -= unitH(v); lh[to] += unitH(v); rank.set(v, to); }
      }
    }

    // ---- what stands alone is spread over the columns to the shape that is wanted ----
    const units = [];
    for (const n of real) if (rep.get(n.id) === n.id && !isOutR(n.id) && isIso(n.id)) units.push({ n, list: [n].concat(fol.get(n.id) || []) });
    const cols0 = maxR + 1;
    const colH = new Array(Math.max(cols0, 1)).fill(0), colW = new Array(Math.max(cols0, 1)).fill(0);
    for (const n of real) {
      const r = rep.get(n.id);
      if (isOutR(r) || isIso(r)) continue;
      const l = rank.get(r);
      colH[l] += n.h + GAPRR;
      colW[l] = Math.max(colW[l], n.w);
    }
    // the wires that pass a column
    const nOut = new Set();
    const toEnd = (c) => c.kind === 'out' || isOutR(rep.get(c.id));
    for (const s of S.values()) {
      if (s.src.kind !== 'in' && isOutR(rep.get(s.src.id))) continue;
      const rs = s.src.kind === 'in' ? -1 : rank.get(rep.get(s.src.id));
      let rmax = -1;
      for (const c of s.cons) rmax = Math.max(rmax, toEnd(c) ? cols0 : rank.get(rep.get(c.id)));
      for (let l = rs + 1; l < rmax; l++) if (l >= 0 && l < cols0) colH[l] += GAPDD;
      if (s.cons.some(toEnd)) nOut.add(s.a);
    }
    // the outputs stand after everything else: in one column, or in one more for each output that they read
    const od = new Map();
    const depthOf = (r, seen) => {
      if (od.has(r)) return od.get(r);
      if (seen.has(r)) return 0;
      seen.add(r);
      let d = 0;
      for (const q of opred.get(r)) d = Math.max(d, depthOf(q, seen) + 1);
      seen.delete(r);
      od.set(r, d);
      return d;
    };
    const oH = [], oW = [];
    for (const r of osucc.keys()) {
      const d = depthOf(r, new Set());
      while (oH.length <= d) { oH.push(0); oW.push(0); }
      oH[d] += unitH(r);
      oW[d] = Math.max(oW[d], N.get(r).w, ...(fol.get(r) || []).map((f) => f.w));
    }
    const ocols = oH.length;
    const isoAt = new Map();
    let cols = cols0;
    if (units.length) {
      const base = Math.max(cols0, 1);
      let best = null;
      for (let c = base; c <= base + Math.min(units.length, 12); c++) {
        const h = colH.slice(), w = colW.slice();
        for (let i = h.length; i < c; i++) { h.push(nOut.size * GAPDD); w.push(0); }
        const at2 = [];
        for (const u of units) {
          let k = 0;
          for (let i = 1; i < c; i++) if (h[i] < h[k] - 0.5) k = i;
          at2.push(k);
          h[k] += u.list.reduce((s, m) => s + m.h + GAPRR, 0);
          w[k] = Math.max(w[k], ...u.list.map((m) => m.w));
        }
        const width = w.reduce((s, x) => s + x, 0) + oW.reduce((s, x) => s + x, 0) + (c + ocols - 1 + (hasIN ? 1 : 0) + (hasOUT ? 1 : 0)) * GUTLITE;
        const height = Math.max(...h, ...oH) - GAPRR;
        const sc = Math.abs(Math.log(width / Math.max(height, 1) / aspect)) + 0.03 * c;
        if (!best || sc < best.sc - 1e-9) best = { sc, c, at2 };
      }
      cols = best.c;
      units.forEach((u, i) => isoAt.set(u.n.id, best.at2[i]));
      for (let i = colH.length; i < cols; i++) { colH.push(nOut.size * GAPDD); colW.push(0); }
      units.forEach((u, i) => {
        const k = best.at2[i];
        colH[k] += u.list.reduce((s, m) => s + m.h + GAPRR, 0);
        colW[k] = Math.max(colW[k], ...u.list.map((m) => m.w));
      });
    }
    if (ocols) {
      colH.length = cols; colW.length = cols;
      for (let l = 0; l < ocols; l++) { colH.push(oH[l]); colW.push(oW[l]); }
      for (const r of osucc.keys()) rank.set(r, cols + od.get(r));
      for (const s of S.values()) {
        const rs = s.src.kind === 'in' ? -1 : rank.get(rep.get(s.src.id));
        let rmax = -1;
        for (const c of s.cons) if (isOutR(rep.get(c.id))) rmax = Math.max(rmax, rank.get(rep.get(c.id)));
        for (let l = Math.max(rs + 1, cols); l < rmax; l++) colH[l] += GAPDD;
      }
      cols += ocols;
    }
    const rankOf = (n) => { const r = rep.get(n.id); return isoAt.has(r) ? isoAt.get(r) : rank.get(r); };
    // the edges that go back, and so the columns that they need on either side
    const goes = [];
    for (const s of S.values()) {
      if (s.src.kind === 'in') continue;
      for (const c of s.cons) if (c.real && rankOf(c) <= rankOf(s.src)) goes.push({ s, c });
    }
    const pre = hasIN || goes.some((g) => rankOf(g.c) === 0);
    const post = hasOUT || goes.some((g) => rankOf(g.s.src) === cols - 1);
    const off = pre ? 1 : 0;
    const nL = off + cols + (post ? 1 : 0);
    const retMargin = goes.length ? RET0 + RETSTEP * (goes.length - 1) + 12 : 0;
    for (const g of goes) backSet.add(g.s.a + '\u0000' + g.c.id);

    // ---- the size of a lane that is laid out in the light way (for a trial that wants the bounds only) ----
    const finish = (W, H, inY, outY, parts) => {
      const ox = L.virtual ? 0 : hasIN ? 0 : PADX;
      const oy = L.virtual ? 0 : G.HEAD + PADT;
      const padR = L.virtual ? 0 : hasOUT ? 0 : PADX;
      let w = ox + W + padR;
      if (!L.virtual) w = Math.max(w, labelWidth(L));
      // a whole number of pixels, so that half its height keeps the centres of the lane on the grid of half pixels
      const h = L.virtual ? H : Math.ceil(oy + Math.max(H, 24) + PADB);
      L.ox = ox; L.oy = oy; L.w = w; L.h = h;
      L.inY = inY; L.outY = outY;
      L.lv = Object.assign({ real, W, H }, parts);
    };
    if (lite) {
      const dum = new Array(nL).fill(0);
      for (const s of S.values()) {
        const rs = s.src.kind === 'in' ? 0 : off + rankOf(s.src);
        let rmax = rs;
        for (const c of s.cons) rmax = Math.max(rmax, c.kind === 'out' ? nL - 1 : off + rankOf(c));
        for (let l = rs + 1; l < rmax; l++) dum[l]++;
      }
      let W = 0, Hh = 0;
      for (let l = 0; l < nL; l++) {
        const k = l - off;
        let h = k >= 0 && k < cols ? colH[k] - GAPRR : 0;
        h += dum[l] * GAPDD;
        Hh = Math.max(Hh, h);
        W += k >= 0 && k < cols ? colW[k] : 0;
      }
      W += Math.max(nL - 1, 0) * GUTLITE;
      finish(W, Hh + retMargin, new Map(), new Map(), {});
      return { W, H: Hh + retMargin, tall: Math.max(0, ...colH) - GAPRR, unit };
    }

    // ---- nodes of the layered graph ----
    const layers = Array.from({ length: nL }, () => []);
    const V = [];
    const mkv = (o) => { o.i = V.length; o.up = []; o.dn = []; o.fol = null; o.glue = null; V.push(o); return o; };
    const link = (u, v) => { if (!u.dn.includes(v)) { u.dn.push(v); v.up.push(u); } };
    for (const n of real) {
      n.v = mkv({ id: n.id, kind: n.kind, node: n, layer: off + rankOf(n), w: n.w, h: n.h, ord: n.ord });
    }
    for (const n of real) {
      const r = rep.get(n.id);
      if (r !== n.id) { const lead = N.get(r).v; n.v.glue = lead; (lead.fol || (lead.fol = [])).push(n.v); }
    }
    for (const p of ins) p.v = mkv({ id: p.id, kind: 'in', node: p, layer: 0, w: 0, h: 0, ord: -1 });
    for (const p of outs) p.v = mkv({ id: p.id, kind: 'out', node: p, layer: nL - 1, w: 0, h: 0, ord: -1 });
    const isoV = [];
    for (const n of real) {
      if (n.v.glue) continue;
      if (isoAt.has(n.id)) isoV.push(n.v); else layers[n.v.layer].push(n.v);
    }
    for (const p of ins.concat(outs)) layers[p.v.layer].push(p.v);

    for (const s of S.values()) {
      const sv = s.src.v;
      s.rs = sv.layer;
      s.fw = [];
      s.bk = [];
      for (const c of s.cons) (c.v.layer > s.rs ? s.fw : s.bk).push(c);
      if (!s.fw.length) continue;
      let rmax = 0;
      for (const c of s.fw) rmax = Math.max(rmax, c.v.layer);
      s.rmax = rmax;
      s.car = new Array(rmax + 1);
      s.car[s.rs] = sv;
      for (let l = s.rs + 1; l < rmax; l++) {
        const d = mkv({ id: 'd:' + s.a + ':' + l, kind: 'dummy', layer: l, w: 0, h: 0, ord: sv.ord < 0 ? s.ord : sv.ord + 0.5, strand: s });
        s.car[l] = d;
        layers[l].push(d);
        link(s.car[l - 1], d);
      }
      for (const c of s.fw) link(s.car[c.v.layer - 1], c.v);
    }
    // the first order: as given, and the followers behind what they follow
    for (const L2 of layers) {
      const lead = L2.filter((v) => !v.glue);
      lead.sort((p, q) => p.ord - q.ord);
      L2.length = 0;
      for (const v of lead) { L2.push(v); if (v.fol) for (const f of v.fol) L2.push(f); }
    }
    let edgeCount = 0;
    for (const v of V) edgeCount += v.dn.length;
    const rounds = typeof args.sweeps === 'number' && args.sweeps > 0 ? args.sweeps : Math.max(3, Math.min(24, Math.round(300000 / (edgeCount + 50))));
    orderLayers(layers, rounds);
    // what stands alone goes last in its column
    for (const v of isoV) { layers[v.layer].push(v); if (v.fol) for (const f of v.fol) layers[v.layer].push(f); }
    for (const L2 of layers) for (let i = 0; i < L2.length; i++) L2[i].pos = i;

    // ---- heights ----
    const ys = placeY(layers, V);
    let top = Infinity, bot = -Infinity;
    // every coordinate stands on a grid of half pixels, so that the sums of the lanes that hold it are exact
    for (const v of V) { v.y = Math.round(ys[v.i] * 2) / 2; top = Math.min(top, v.y - v.h / 2); bot = Math.max(bot, v.y + v.h / 2); }
    if (!V.length) { top = 0; bot = 0; }
    const dy = retMargin - top;
    for (const v of V) v.y += dy;
    const H = bot + dy;
    const topOf = (v) => v.y - v.h / 2;

    // ---- where a wire leaves an item and where it arrives ----
    // a wire leaves an item at the height of the first run of the wire when that is within the item (so it goes straight out),
    // and at the middle of the item when it is not: the middle of the heights that its branches go to
    const MARGIN = 8; // from the top or the bottom of an item to the nearest wire that touches it
    const within = (v, t) => Math.max(v.y - Math.max(v.h / 2 - MARGIN, 0), Math.min(v.y + Math.max(v.h / 2 - MARGIN, 0), t));
    const outAt = new Map();
    const srcY = (s) => {
      const v = s.src.v;
      if (v.kind === 'lane') return topOf(v) + v.node.lane.outY.get(s.a);
      if (v.kind !== 'item' || !s.fw.length) return v.y;
      let t = outAt.get(s);
      if (t === undefined) {
        const ts = [];
        if (s.rs + 1 < s.rmax) ts.push(s.car[s.rs + 1].y);
        for (const c of s.fw) if (c.v.layer === s.rs + 1) ts.push(c.v.y);
        ts.sort((p, q) => p - q);
        t = ts.length ? Math.round(within(v, ts[(ts.length - 1) >> 1]) * 2) / 2 : v.y;
        outAt.set(s, t);
      }
      return t;
    };
    // the lines above the nodes for the edges that go back: the longest on the highest
    goes.sort((p, q) => (Math.abs(rankOf(q.c) - rankOf(q.s.src)) - Math.abs(rankOf(p.c) - rankOf(p.s.src))) || p.s.ord - q.s.ord);
    goes.forEach((g, k) => { g.ret = RET0 + RETSTEP * k; });
    // wires that end on a node stand apart on its left edge, the highest approach on the highest
    const arrivals = new Map();
    const arrive = (v, rec) => { let a = arrivals.get(v); if (!a) arrivals.set(v, (a = [])); a.push(rec); };
    for (const s of S.values()) {
      if (s.fw.length) {
        for (const c of s.fw) {
          const l = c.v.layer - 1;
          arrive(c.v, { s, c, from: l === s.rs ? srcY(s) : s.car[l].y });
        }
      }
    }
    for (const g of goes) arrive(g.c.v, { s: g.s, c: g.c, from: g.ret - 1000 });
    const pin = new Map();
    for (const [v, list] of arrivals) {
      list.sort((p, q) => p.from - q.from || p.s.ord - q.s.ord);
      const k = list.length;
      if (v.kind === 'lane') list.forEach((r) => pin.set(r.s.a + '|' + r.c.id, topOf(v) + v.node.lane.inY.get(r.s.a)));
      else if (v.kind !== 'item') list.forEach((r) => pin.set(r.s.a + '|' + r.c.id, v.y));
      else {
        // each wire arrives at the height that it comes in at when that is within the item, and the wires that would stand
        // closer than sp are set apart as little as that takes (the closest fit, in the least squares)
        const sp = k > 1 ? Math.min(MAXPIN, Math.max(3, (v.h - 16) / (k - 1))) : 0;
        const blocks = [];
        list.forEach((r, i) => {
          blocks.push({ sum: within(v, r.from) - i * sp, n: 1 });
          while (blocks.length > 1) {
            const q = blocks[blocks.length - 1], p = blocks[blocks.length - 2];
            if (p.sum / p.n <= q.sum / q.n) break;
            p.sum += q.sum; p.n += q.n; blocks.pop();
          }
        });
        const ys = [];
        for (const b of blocks) for (let j = 0; j < b.n; j++) ys.push(b.sum / b.n + ys.length * sp);
        // inside the item: the i-th of the wires has i of them above it and k-1-i below, each sp away at the least
        const room = Math.max(v.h / 2 - MARGIN, 0);
        list.forEach((r, i) => pin.set(r.s.a + '|' + r.c.id, Math.round(Math.min(Math.max(ys[i], v.y - room + i * sp), v.y + room - (k - 1 - i) * sp) * 2) / 2));
      }
    }

    // ---- gutters ----
    const nG = Math.max(nL - 1, 0);
    const gut = Array.from({ length: nG }, () => []);
    const addG = (g, it) => { if (it.hi - it.lo > 0.25) gut[g].push(it); };
    const runs = new Map();
    for (const s of S.values()) {
      if (!s.fw.length) continue;
      for (let l = s.rs; l < s.rmax; l++) {
        const yin = l === s.rs ? srcY(s) : s.car[l].y;
        const outsY = [];
        if (l + 1 < s.rmax) outsY.push(s.car[l + 1].y);
        for (const c of s.fw) if (c.v.layer === l + 1) outsY.push(pin.get(s.a + '|' + c.id));
        let lo = yin, hi = yin;
        for (const y of outsY) { lo = Math.min(lo, y); hi = Math.max(hi, y); }
        const it = { key: s.a + '|' + l, lo, hi, yin, outs: outsY, ord: s.ord };
        runs.set(it.key, it);
        addG(l, it);
      }
    }
    for (const g of goes) {
      const sy = srcY(g.s), ty = pin.get(g.s.a + '|' + g.c.id);
      const a = { key: g.s.a + '|' + g.c.id + '|a', lo: g.ret, hi: sy, yin: sy, outs: [g.ret], ord: g.s.ord };
      const b = { key: g.s.a + '|' + g.c.id + '|b', lo: Math.min(g.ret, ty), hi: Math.max(g.ret, ty), yin: g.ret, outs: [ty], ord: g.s.ord };
      runs.set(a.key, a); runs.set(b.key, b);
      g.ga = rankOf(g.s.src) + off;
      g.gb = rankOf(g.c) + off - 1;
      addG(g.ga, a);
      addG(g.gb, b);
    }
    const gw = new Array(nG), gT = new Array(nG);
    for (let g = 0; g < nG; g++) {
      const T = assignTracks(gut[g]);
      gT[g] = T;
      gw[g] = Math.max(GUTMIN, 2 * PADG + Math.max(T - 1, 0) * TRK);
    }
    // columns
    const cw = new Array(nL).fill(0);
    for (const v of V) if (v.kind === 'item' || v.kind === 'lane') cw[v.layer] = Math.max(cw[v.layer], v.w);
    const x0 = new Array(nL).fill(0);
    const lanew = L.virtual ? 0 : labelWidth(L);
    const fixW = () => {
      for (let l = 1; l < nL; l++) x0[l] = x0[l - 1] + cw[l - 1] + gw[l - 1];
      return x0[nL - 1] + cw[nL - 1];
    };
    let W = nL ? fixW() : 0;
    const minW = lanew - (L.virtual ? 0 : hasIN ? 0 : PADX) - (L.virtual ? 0 : hasOUT ? 0 : PADX);
    if (nL && W < minW) {
      if (nL >= 2) gw[nL - 2] += minW - W; else cw[0] += minW - W;
      W = fixW();
    }
    const jx = (g, t) => x0[g] + cw[g] + Math.round((gw[g] - Math.max(gT[g] - 1, 0) * TRK)) / 2 + (t - 1) * TRK;
    const xOf = (key, g) => jx(g, runs.get(key).t);

    // ---- wires ----
    const pieces = new Map();
    const mkpoly = () => {
      const q = [];
      const add = (x, y) => {
        const m = q.length;
        if (m && Math.abs(q[m - 2] - x) < 0.01 && Math.abs(q[m - 1] - y) < 0.01) return;
        if (m >= 4 && ((Math.abs(q[m - 4] - q[m - 2]) < 0.01 && Math.abs(q[m - 2] - x) < 0.01) || (Math.abs(q[m - 3] - q[m - 1]) < 0.01 && Math.abs(q[m - 1] - y) < 0.01))) { q[m - 2] = x; q[m - 1] = y; return; }
        q.push(x, y);
      };
      return { q, add };
    };
    for (const s of S.values()) {
      const sv = s.src.v;
      const sy = srcY(s);
      const sx = x0[s.rs] + (sv.kind === 'in' ? 0 : sv.w);
      for (const c of s.fw) {
        const { q, add } = mkpoly();
        add(sx, sy);
        const ty = pin.get(s.a + '|' + c.id);
        for (let l = s.rs; l < c.v.layer; l++) {
          const cur = l === s.rs ? sy : s.car[l].y;
          const nxt = l + 1 < c.v.layer ? s.car[l + 1].y : ty;
          if (Math.abs(cur - nxt) > 0.25) { const x = xOf(s.a + '|' + l, l); add(x, cur); add(x, nxt); }
        }
        add(x0[c.v.layer], ty);
        pieces.set(s.a + '|' + c.id, q);
      }
    }
    for (const g of goes) {
      const sv = g.s.src.v;
      const sy = srcY(g.s), ty = pin.get(g.s.a + '|' + g.c.id);
      const { q, add } = mkpoly();
      add(x0[sv.layer] + sv.w, sy);
      const xa = xOf(g.s.a + '|' + g.c.id + '|a', g.ga), xb = xOf(g.s.a + '|' + g.c.id + '|b', g.gb);
      add(xa, sy); add(xa, g.ret); add(xb, g.ret); add(xb, ty);
      add(x0[g.c.v.layer], ty);
      pieces.set(g.s.a + '|' + g.c.id, q);
    }
    // where the ports stand, from the top of the box: the lane that holds this one wires to them
    const oy0 = L.virtual ? 0 : G.HEAD + PADT;
    const inY = new Map(), outY = new Map();
    for (const p of ins) inY.set(p.a, p.v.y + oy0);
    for (const p of outs) outY.set(p.a, p.v.y + oy0);
    let Hh = H;
    for (const g of goes) Hh = Math.max(Hh, g.ret + 4);
    finish(W, Hh, inY, outY, { pieces });
    for (const n of real) { n.bx = x0[n.v.layer]; n.by = n.v.y - n.v.h / 2; }
    return { W, H: Hh };
  }

  build(R, E);

  // ---- absolute positions ----
  const pos = new Map();
  const outLanes = [];
  const P = new Map();
  const emit = (L, ax, ay) => {
    const lv = L.lv;
    for (const n of lv.real) {
      const X = ax + (n.bx || 0), Y = ay + (n.by || 0);
      if (n.kind === 'item') pos.set(n.id, { x: X, y: Y, w: n.w, h: n.h, rank: 0 });
      else {
        const sl = n.lane;
        outLanes.push({ path: sl.path, label: sl.label, depth: sl.depth, top: sl.top, x: X, y: Y, w: sl.w, h: sl.h });
        emit(sl, X + sl.ox, Y + sl.oy);
      }
    }
    if (lv.pieces) for (const [k, q] of lv.pieces) {
      const o = new Array(q.length);
      for (let i = 0; i < q.length; i += 2) { o[i] = f2(q[i] + ax); o[i + 1] = f2(q[i + 1] + ay); }
      P.set(L.id + '|' + k, o);
    }
  };
  if (lite) {
    // a trial wants the bounds: the lane of the page, as one box
    return { pos, lanes: outLanes, routes: new Map(), back: backSet, bounds: { x: 0, y: 0, w: R.w, h: R.h }, rank: new Map(), maxRank: 0 };
  }
  emit(R, 0, 0);

  const routes = new Map();
  const chain = (l) => { const a = []; for (let x = l; x; x = x.parent) a.push(x); return a; };
  for (const e of E) {
    const la = home.get(e.a), lb = home.get(e.b);
    const cb = new Set(chain(lb));
    let lca = la;
    while (!cb.has(lca)) lca = lca.parent;
    const parts = [];
    for (let l = la; l !== lca; l = l.parent) parts.push(P.get(l.id + '|' + e.a + '|out:' + e.a));
    parts.push(P.get(lca.id + '|' + e.a + '|' + lca.at.get(e.b)));
    const down = [];
    for (let l = lb; l !== lca; l = l.parent) down.push(l);
    for (let i = down.length - 1; i >= 0; i--) parts.push(P.get(down[i].id + '|' + e.a + '|' + down[i].at.get(e.b)));
    if (parts.some((q) => !q)) continue;
    const q = [];
    const add = (x, y) => {
      const m = q.length;
      if (m && Math.abs(q[m - 2] - x) < 0.01 && Math.abs(q[m - 1] - y) < 0.01) return;
      if (m >= 4 && ((Math.abs(q[m - 4] - q[m - 2]) < 0.01 && Math.abs(q[m - 2] - x) < 0.01) || (Math.abs(q[m - 3] - q[m - 1]) < 0.01 && Math.abs(q[m - 1] - y) < 0.01))) { q[m - 2] = x; q[m - 1] = y; return; }
      q.push(x, y);
    };
    for (const part of parts) for (let i = 0; i < part.length; i += 2) add(part[i], part[i + 1]);
    routes.set(e.key, { m: 'w', p: q, q });
  }

  // ---- ranks for the picture: the order of the columns, left to right ----
  const xsSet = [...new Set([...pos.values()].map((p) => Math.round(p.x)))].sort((a, b) => a - b);
  const rk = new Map(xsSet.map((x, i) => [x, i]));
  const rank = new Map();
  for (const [id, p] of pos) { p.rank = rk.get(Math.round(p.x)); rank.set(id, p.rank); }

  // ---- bounds ----
  let bx0 = Infinity, by0 = Infinity, bx1 = -Infinity, by1 = -Infinity;
  const grow = (x0, y0, x1, y1) => { bx0 = Math.min(bx0, x0); by0 = Math.min(by0, y0); bx1 = Math.max(bx1, x1); by1 = Math.max(by1, y1); };
  for (const l of outLanes) grow(l.x, l.y, l.x + l.w, l.y + l.h);
  for (const p of pos.values()) grow(p.x, p.y, p.x + p.w, p.y + p.h);
  for (const r of routes.values()) for (let i = 0; i < r.p.length; i += 2) grow(r.p[i], r.p[i + 1], r.p[i], r.p[i + 1]);
  if (!isFinite(bx0)) { bx0 = by0 = 0; bx1 = by1 = 100; }
  return { pos, lanes: outLanes, routes, back: backSet, bounds: { x: bx0, y: by0, w: bx1 - bx0, h: by1 - by0 }, rank, maxRank: xsSet.length ? xsSet.length - 1 : 0 };

}
