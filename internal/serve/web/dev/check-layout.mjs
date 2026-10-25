// Checks the layout of the graph (flow.js through layout.js) on the fixtures, collapsed and open: nothing overlaps,
// every lane holds what it should, and every wire is a line of right-angled runs that leaves the right edge of what
// it reads, arrives at the left edge of what reads it, runs to the right all the way (the edges that close a cycle
// excepted) and stays out of every item and every lane that it does not belong to. No DOM, no browser.
//
//   node check-layout.mjs [fixture ...]

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildModel, buildItems, firstView, touchedNamespaces, isChange } from '../assets/model.js';
import { layout, fitScale, FIT, badges, badgeSize } from '../assets/layout.js';

const dir = path.join(path.dirname(fileURLToPath(import.meta.url)), 'fixtures');
const load = (n) => buildModel(JSON.parse(fs.readFileSync(path.join(dir, n), 'utf8')));
const names = process.argv.slice(2).length ? process.argv.slice(2) : ['platform', 'fleet', 'estate', 'big'];
let failed = 0;
const fails = new Map();
const check = (ok, msg) => { if (!ok) { failed++; const k = msg.replace(/[0-9.]+/g, '#').slice(0, 60); fails.set(k, (fails.get(k) || 0) + 1); if (fails.get(k) <= 3) console.log('FAIL', msg); } };

const EPS = 0.6;
const inter = (a, b, m = 0) => a.x + m < b.x + b.w && b.x + m < a.x + a.w && a.y + m < b.y + b.h && b.y + m < a.y + a.h;

function verify(tag, m, vis, lay) {
  const { pos, lanes, routes } = lay;
  const ids = [...vis.items.keys()];
  check(pos.size === ids.length, `${tag}: ${pos.size} placed of ${ids.length}`);
  // no two items overlap
  const rects = ids.map((id) => ({ id, ...pos.get(id) }));
  rects.sort((a, b) => a.x - b.x);
  for (let i = 0; i < rects.length; i++) for (let j = i + 1; j < rects.length && rects[j].x < rects[i].x + rects[i].w; j++) check(!inter(rects[i], rects[j], 0.5), `${tag}: ${rects[i].id} overlaps ${rects[j].id}`);
  // lanes hold their items, and stand apart
  const byPath = new Map(lanes.map((l) => [l.path, l]));
  const laneOfItem = (it) => byPath.get(it.lane);
  for (const it of vis.items.values()) {
    const l = laneOfItem(it);
    const p = pos.get(it.id);
    if (!l) continue;
    check(p.x >= l.x - 0.01 && p.y >= l.y + 30 && p.x + p.w <= l.x + l.w + 0.01 && p.y + p.h <= l.y + l.h + 0.01, `${tag}: ${it.id} sticks out of lane ${l.path}`);
  }
  for (let i = 0; i < lanes.length; i++) for (let j = i + 1; j < lanes.length; j++) {
    const a = lanes[i], b = lanes[j];
    const nest = a.path.startsWith(b.path + '.') || b.path.startsWith(a.path + '.');
    if (!nest) check(!inter(a, b, 0.5), `${tag}: lanes ${a.path} and ${b.path} overlap`);
    else {
      const [o, n] = a.path.length < b.path.length ? [a, b] : [b, a];
      check(n.x >= o.x - 0.01 && n.y >= o.y - 0.01 && n.x + n.w <= o.x + o.w + 0.01 && n.y + n.h <= o.y + o.h + 0.01, `${tag}: lane ${n.path} sticks out of ${o.path}`);
    }
  }
  // lanes against items that are not in them
  for (const l of lanes) for (const it of vis.items.values()) {
    const inside = it.lane === l.path || it.lane.startsWith(l.path + '.') || (l.path === '' );
    if (!inside) check(!inter(l, pos.get(it.id), 0.5), `${tag}: ${it.id} stands in lane ${l.path}`);
  }
  // an environment variable stands in the first column of its lane, an output in the last
  const roleOf = (it) => {
    const x = it.kind === 'node' ? it.n : it;
    return x.mode === 'output' ? 'output' : x.type === 'env' || x.type === 'secret_env' ? 'env' : '';
  };
  const parentOf = (p) => (p.includes('.') ? p.slice(0, p.lastIndexOf('.')) : '');
  const groups = new Map();
  const group = (p) => { let g = groups.get(p); if (!g) groups.set(p, (g = { items: [], subs: [] })); return g; };
  for (const it of vis.items.values()) group(it.lane).items.push(it);
  for (const l of lanes) group(parentOf(l.path)).subs.push(l);
  for (const [path, g] of groups) {
    const xs = g.items.map((it) => pos.get(it.id).x).concat(g.subs.map((l) => l.x));
    if (!xs.length) continue;
    const first = Math.min(...xs), last = Math.max(...xs);
    for (const it of g.items) {
      const r = roleOf(it), x = pos.get(it.id).x;
      if (r === 'env') check(Math.abs(x - first) < 0.6, `${tag}: environment variable ${it.id} is not in the first column of lane '${path}' (${x} vs ${first})`);
      if (r === 'output') {
        check(Math.abs(x - last) < 0.6 || g.items.some((o) => roleOf(o) === 'output' && pos.get(o.id).x > x), `${tag}: output ${it.id} is not in the last columns of lane '${path}' (${x} vs ${last})`);
        for (const o of g.items) if (roleOf(o) !== 'output') check(pos.get(o.id).x < x - 0.5, `${tag}: ${o.id} stands in or after the column of output ${it.id}`);
        for (const l of g.subs) check(l.x < x - 0.5, `${tag}: lane ${l.path} stands in or after the column of output ${it.id}`);
      }
    }
  }
  // wires
  const E = vis.edges;
  let nback = 0;
  for (const e of E) {
    const r = routes.get(e.key);
    check(!!r, `${tag}: no route for ${e.key}`);
    if (!r) continue;
    const q = r.p;
    const A = pos.get(e.a), B = pos.get(e.b);
    check(q.length >= 4, `${tag}: ${e.key} is a point`);
    check(Math.abs(q[0] - (A.x + A.w)) < EPS && q[1] >= A.y - EPS && q[1] <= A.y + A.h + EPS, `${tag}: ${e.key} does not leave the right edge of ${e.a} (${q[0]},${q[1]} vs ${A.x + A.w},${A.y}..${A.y + A.h})`);
    const n = q.length;
    check(Math.abs(q[n - 2] - B.x) < EPS && q[n - 1] >= B.y - EPS && q[n - 1] <= B.y + B.h + EPS, `${tag}: ${e.key} does not arrive at the left edge of ${e.b}`);
    // right angles
    let back = false;
    for (let i = 2; i < n; i += 2) {
      const dx = q[i] - q[i - 2], dy = q[i + 1] - q[i - 1];
      check(Math.abs(dx) < 0.01 || Math.abs(dy) < 0.01, `${tag}: ${e.key} has a slanted run`);
      if (dx < -0.01) back = true;
    }
    if (back) nback++;
    // the last run goes right
    check(q[n - 2] - q[n - 4] > 0.5 && Math.abs(q[n - 1] - q[n - 3]) < 0.01, `${tag}: ${e.key} does not end on a run to the right`);
    if (!back) check(A.x + A.w <= B.x + 0.01, `${tag}: ${e.b} does not stand right of ${e.a}`);
    // stays out of items and lanes that are not its own
    const own = new Set([e.a, e.b]);
    for (let i = 2; i < n; i += 2) {
      const sx0 = Math.min(q[i - 2], q[i]), sx1 = Math.max(q[i - 2], q[i]), sy0 = Math.min(q[i - 1], q[i + 1]), sy1 = Math.max(q[i - 1], q[i + 1]);
      const seg = { x: sx0, y: sy0, w: sx1 - sx0, h: sy1 - sy0 };
      for (const id of ids) {
        if (own.has(id)) continue;
        const p = pos.get(id);
        if (seg.x + seg.w > p.x + 0.5 && seg.x < p.x + p.w - 0.5 && seg.y + seg.h > p.y + 0.5 && seg.y < p.y + p.h - 0.5) check(false, `${tag}: ${e.key} runs through ${id}`);
      }
      for (const l of lanes) {
        const ha = vis.items.get(e.a), hb = vis.items.get(e.b);
        const holds = (it) => it.lane === l.path || it.lane.startsWith(l.path + '.') || l.path === '';
        if (holds(ha) || holds(hb)) continue;
        if (seg.x + seg.w > l.x + 0.5 && seg.x < l.x + l.w - 0.5 && seg.y + seg.h > l.y + 0.5 && seg.y < l.y + l.h - 0.5) check(false, `${tag}: ${e.key} runs through lane ${l.path}`);
      }
    }
  }
  // the counts of merged edges: each sits on its own wire, no two touch, and none sits on an item unless no run of its wire could hold it
  const counts = new Map(E.filter((e) => e.n > 1 && routes.get(e.key)).map((e) => [e.key, e.n]));
  const at = badges(routes, counts, ids.map((id) => pos.get(id)));
  const boxes = [];
  for (const [key, [x, y]] of at) {
    const { w, h } = badgeSize(counts.get(key));
    const q = routes.get(key).p;
    let on = false, fits = false;
    for (let i = 2; i < q.length; i += 2) {
      const horiz = Math.abs(q[i + 1] - q[i - 1]) < 0.01, vert = Math.abs(q[i] - q[i - 2]) < 0.01;
      const lo = Math.min(q[i - 2], q[i]), hi = Math.max(q[i - 2], q[i]), lo2 = Math.min(q[i - 1], q[i + 1]), hi2 = Math.max(q[i - 1], q[i + 1]);
      if (horiz && Math.abs(q[i + 1] - y) < 0.01 && x >= lo - 0.01 && x <= hi + 0.01) on = true;
      if (vert && Math.abs(q[i] - x) < 0.01 && y >= lo2 - 0.01 && y <= hi2 + 0.01) on = true;
      if ((horiz && hi - lo >= w + 8) || (vert && hi2 - lo2 >= h + 8)) fits = true;
    }
    check(on, `${tag}: the count of ${key} is not on its wire`);
    const b = { key, x: x - w / 2, y: y - h / 2, w, h, fits };
    boxes.push(b);
    if (fits) for (const id of ids) check(!inter(b, pos.get(id), 0), `${tag}: the count of ${key} sits on ${id}`);
  }
  boxes.sort((a, b) => a.x - b.x);
  for (let i = 0; i < boxes.length; i++) for (let j = i + 1; j < boxes.length && boxes[j].x < boxes[i].x + boxes[i].w + 1; j++) check(!inter(boxes[i], boxes[j], -1), `${tag}: the counts of ${boxes[i].key} and ${boxes[j].key} touch`);
  // a wire that turns more than it must reads as a wicked line. The turns of a picture stay under 1.6 for each wire, and under
  // 2.1 when the namespaces are open (a wire that crosses the border of a lane turns at its port as well): before the heights of
  // the wires were straightened the estate had 1.9 and the platform open 2.1
  let turns = 0;
  for (const e of E) { const q = routes.get(e.key).p; for (let i = 2; i < q.length; i += 2) if (Math.abs(q[i] - q[i - 2]) < 0.01 && Math.abs(q[i + 1] - q[i - 1]) > 0.01) turns++; }
  if (E.length >= 10) check(turns <= (tag.endsWith('open') ? 2.1 : 1.6) * E.length, `${tag}: ${turns} turns for ${E.length} wires`);
  console.log(`${tag.padEnd(24)} turns per wire ${(turns / Math.max(E.length, 1)).toFixed(2)}`);
  return nback;
}

const sizes = [[1440, 900], [1440, 762], [1920, 940]];
for (const name of names) {
  const m = load(`graph.${name}.json`);
  const touched = touchedNamespaces(m, (n) => n.mode !== 'output' && (isChange(n.action) || n.drifted || n.gone || n.presence !== 'both'));
  const states = [];
  for (const [w, h] of sizes) states.push([`first ${w}x${h}`, firstView(m, new Map(), { width: w, height: h, touched }).collapsed, w, h]);
  const none = new Set();
  const all = new Set([...m.namespaces.keys()].filter((p) => p));
  states.push(['open', none, 1440, 900]);
  states.push(['closed', all, 1440, 900]);
  for (const [label, collapsed, w, h] of states) {
    const vis = buildItems(m, collapsed, new Map());
    if (vis.items.size > 1500) continue;
    const fit = { w: w - FIT.padX * 2, h: h - FIT.padTop - FIT.padBottom };
    const t0 = performance.now();
    const lay = layout({ root: vis.root, items: vis.items, edges: vis.edges, aspect: fit.w / fit.h, fit });
    const ms = performance.now() - t0;
    const tag = `${name} ${label}`;
    const nback = verify(tag, m, vis, lay);
    const again = layout({ root: vis.root, items: vis.items, edges: vis.edges, aspect: fit.w / fit.h, fit });
    check(JSON.stringify([...again.routes]) === JSON.stringify([...lay.routes]), `${tag}: not deterministic`);
    const k = fitScale(lay.bounds, w, h, 99);
    console.log(`${tag.padEnd(24)} items ${String(vis.items.size).padStart(4)} edges ${String(vis.edges.length).padStart(4)} lanes ${String(lay.lanes.length).padStart(2)}  ${Math.round(lay.bounds.w)}x${Math.round(lay.bounds.h)}  k ${k.toFixed(2)}  back ${nback}  ${ms.toFixed(0)} ms`);
  }
}
console.log(failed ? `${failed} failures` : 'ok');
process.exit(failed ? 1 : 0);
