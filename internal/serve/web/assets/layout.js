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

function lenOf(q) {
  let n = 0;
  for (let i = 2; i < q.length; i += 2) n += Math.hypot(q[i] - q[i - 2], q[i + 1] - q[i - 1]);
  return n;
}

// lengthOf is the length of a route, for the weight of an edge at rest.
export function lengthOf(r) {
  return lenOf(polyOf(r));
}
