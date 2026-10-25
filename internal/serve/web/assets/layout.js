// The graph's layout: columns by depth inside nested namespace lanes, with the wires routed in lanes of their own.
// flow.js does the work; this file holds the sizes, the zoom that fits a picture, and the drawing of the wires.
//
// One rule holds everywhere: if B reads A, B stands in a column to the right of A, and the wire from A to B runs to the
// right, turning only in the gutter between two columns. An edge that closes a cycle is the one exception: it goes
// back along a line above the nodes of its lane and comes in from the left all the same.

import { flow } from './flow.js';

export const G = {
  W: 224, H: 52, CW: 224, MH: 80, MH2: 94, CTL: 36,
  GAPX: 50, GAPY: 12,
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

// layout takes { root, items, edges, aspect, fit, route, sweeps }.
//   root:   a lane { path, label, items: [id], sub: [lane] }; the root lane is virtual (it draws no box)
//   items:  Map id -> { id, w, h, kind, with }; an item with a `with` stands next to that item, in its column
//   edges:  [{ key, a, b, n }] from dependency to dependent, between item ids
//   aspect: the width over the height that the picture should have when a lane has items that stand alone
//   route:  false leaves the routes out and the order of the columns unsought (a trial layout needs the bounds only)
// It returns { pos, lanes, routes, back, bounds, rank, maxRank }: pos maps an item to { x, y, w, h, rank }, lanes lists
// the boxes of the open namespaces, and routes maps an edge key to { m: 'w', p } where p is the corners of its wire.
export function layout(args) {
  return flow(args, G);
}

const f1 = (n) => Math.round(n * 10) / 10;

// polyOf is the corners of a route: the wire runs along the axes, to the right, and turns at the gutters. The points of
// the route are the corners already.
const polyOf = (r) => r.p;

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

// midOf is where the count of a merged edge is written: the middle of the last run, along the wire, that is long
// enough to hold it, or else of the longest run.
export function midOf(r) {
  const q = polyOf(r);
  let best = -1, at = 0, last = 0;
  for (let i = 2; i < q.length; i += 2) {
    if (Math.abs(q[i + 1] - q[i - 1]) > 0.01) continue;
    const l = Math.abs(q[i] - q[i - 2]);
    if (l >= 44) last = i;
    if (l > best + 0.5) { best = l; at = i; }
  }
  const i = last || at;
  if (!i) return [q[0], q[1]];
  return [(q[i - 2] + q[i]) / 2, q[i - 1]];
}

// badgeSize is the box of the count that a merged edge carries: 18 high, and as wide as its digits.
export const badgeSize = (n) => ({ w: 14 + String(n).length * 6.6, h: 18 });

// badges says where the count of each merged edge is written. A count sits on its own wire, on a run that is long enough
// to hold it, as near as it can to the middle of the last long run (midOf), and no two counts touch. Where the wires
// of a bus run close together, a count would cover its neighbours', so a place that covers fewer wires and fewer items
// is the better one, and a count moves along its wire to find it. routes is a Map key -> route, counts a Map key -> n,
// and boxes the items that a count should stay off. It returns a Map key -> [x, y], the centre of the count.
const AIR = 6;       // the least space between two counts
const EDGE = 4;      // the least run that is left on each side of a count, for the turn of the wire
const UP = 4;        // what a place on a vertical run costs over one on a horizontal run
const CELL = 64;
export function badges(routes, counts, boxes = []) {
  const out = new Map();
  const grid = new Map();
  const segs = [];
  const put = (item, x0, y0, x1, y1) => {
    for (let cx = Math.floor(x0 / CELL); cx <= Math.floor(x1 / CELL); cx++) for (let cy = Math.floor(y0 / CELL); cy <= Math.floor(y1 / CELL); cy++) {
      const k = cx + ',' + cy;
      let a = grid.get(k);
      if (!a) grid.set(k, (a = []));
      a.push(item);
    }
  };
  for (const [key, r] of routes) {
    const q = polyOf(r);
    for (let i = 2; i < q.length; i += 2) {
      const sg = { key, x0: Math.min(q[i - 2], q[i]), x1: Math.max(q[i - 2], q[i]), y0: Math.min(q[i - 1], q[i + 1]), y1: Math.max(q[i - 1], q[i + 1]), wire: true };
      segs.push(sg);
      put(sg, sg.x0, sg.y0, sg.x1, sg.y1);
    }
  }
  for (const b of boxes) put({ x0: b.x, x1: b.x + b.w, y0: b.y, y1: b.y + b.h, wire: false }, b.x, b.y, b.x + b.w, b.y + b.h);
  // what a box would cover: the other wires that run through it, and the items that it overlaps
  const clutter = (key, x0, y0, x1, y1) => {
    const seen = new Set();
    let c = 0;
    for (let cx = Math.floor(x0 / CELL); cx <= Math.floor(x1 / CELL); cx++) for (let cy = Math.floor(y0 / CELL); cy <= Math.floor(y1 / CELL); cy++) {
      const a = grid.get(cx + ',' + cy);
      if (!a) continue;
      for (const g of a) {
        if (seen.has(g) || g.key === key) continue;
        seen.add(g);
        if (g.x1 > x0 && g.x0 < x1 && g.y1 > y0 && g.y0 < y1 ) c += g.wire ? 10 : 200;
      }
    }
    return c;
  };
  // the places on its wire where a count could stand: along every run that is long enough to hold it
  const list = [];
  for (const [key, n] of counts) {
    const r = routes.get(key);
    if (!r) continue;
    const q = polyOf(r);
    const { w, h } = badgeSize(n);
    const hw = w / 2, hh = h / 2;
    const [dx, dy] = midOf({ p: q });
    const cands = [[dx, dy, 0]];
    for (let i = 2; i < q.length; i += 2) {
      if (Math.abs(q[i + 1] - q[i - 1]) > 0.01) {
        // a vertical run: a count sits on it as well, when the horizontal ones are taken
        if (Math.abs(q[i] - q[i - 2]) > 0.01) continue;
        const a = Math.min(q[i - 1], q[i + 1]) + hh + EDGE, b = Math.max(q[i - 1], q[i + 1]) - hh - EDGE;
        if (b < a) continue;
        const step = Math.max(5, (b - a) / 48);
        for (let y = a; y <= b + 0.01; y += step) cands.push([q[i], y, UP]);
        continue;
      }
      const a = Math.min(q[i - 2], q[i]) + hw + EDGE, b = Math.max(q[i - 2], q[i]) - hw - EDGE;
      if (b < a) continue;
      const step = Math.max(5, (b - a) / 48);
      for (let x = a; x <= b + 0.01; x += step) cands.push([x, q[i + 1], 0]);
      cands.push([(a + b) / 2, q[i + 1], 0]);
    }
    list.push({ key, n, hw, hh, dx, dy, cands });
  }
  // the count with the fewest places chooses first, and the ones with room give way
  list.sort((p, q) => p.cands.length - q.cands.length || q.n - p.n || (p.key < q.key ? -1 : p.key > q.key ? 1 : 0));
  const placed = [];
  const free = (x0, y0, x1, y1) => {
    for (const p of placed) if (p.x0 - AIR < x1 && p.x1 + AIR > x0 && p.y0 - AIR < y1 && p.y1 + AIR > y0) return false;
    return true;
  };
  for (const e of list) {
    let best = null, bestAny = null;
    for (const [cx, cy, extra] of e.cands) {
      const x0 = cx - e.hw, x1 = cx + e.hw, y0 = cy - e.hh, y1 = cy + e.hh;
      const cost = clutter(e.key, x0, y0, x1, y1) + Math.hypot(cx - e.dx, cy - e.dy) * 0.15 + extra;
      if (!bestAny || cost < bestAny.cost) bestAny = { cx, cy, cost, x0, y0, x1, y1 };
      if ((!best || cost < best.cost) && free(x0, y0, x1, y1)) best = { cx, cy, cost, x0, y0, x1, y1 };
    }
    const at = best || bestAny;
    placed.push(at);
    out.set(e.key, [at.cx, at.cy]);
  }
  return out;
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
