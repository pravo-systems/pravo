// Checks the policy of the first view (firstView in ../assets/model.js) on the fixtures: at several canvas
// sizes the picture stays within the budget, fits at a legible zoom when it can, opens namespaces of one shape
// together, opens what it is told to, and takes a few milliseconds. No DOM, no browser.
//
//   node check-firstview.mjs

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildModel, buildItems, firstView, touchedNamespaces, isChange, budgetFor } from '../assets/model.js';
import { layout, fitScale, FIT } from '../assets/layout.js';

const dir = path.join(path.dirname(fileURLToPath(import.meta.url)), 'fixtures');
const load = (n) => buildModel(JSON.parse(fs.readFileSync(path.join(dir, n), 'utf8')));
const sizes = [[560, 620], [776, 820], [1250, 960], [1700, 800]];
let failed = 0;
const check = (ok, msg) => { if (!ok) { failed++; console.log('FAIL', msg); } };

for (const name of ['graph.platform.json', 'graph.fleet.json', 'graph.big.json']) {
  const m = load(name);
  // an output is a light item: it does not decide which namespaces open first (see autoCollapsed in app.js)
  const touched = touchedNamespaces(m, (n) => n.mode !== 'output' && (isChange(n.action) || n.drifted || n.gone || n.presence !== 'both'));
  for (const [w, h] of sizes) {
    const t0 = performance.now();
    const fv = firstView(m, new Map(), { width: w, height: h, touched });
    const ms = performance.now() - t0;
    const vis = buildItems(m, fv.collapsed, new Map());
    const fit = { w: w - FIT.padX * 2, h: h - FIT.padTop - FIT.padBottom };
    const lay = layout({ root: vis.root, items: vis.items, edges: vis.edges, aspect: fit.w / fit.h, fit });
    const k = fitScale(lay.bounds, w, h, 99);
    const tag = `${name} ${w}x${h}`;
    const open = [...m.namespaces.keys()].filter((p) => p && !fv.collapsed.has(p));
    const everyClosed = open.length === 0;
    check(vis.items.size <= Math.max(budgetFor(w, h), m.root.kids.length + m.root.nodes.length), `${tag}: ${vis.items.size} items over the budget ${budgetFor(w, h)}`);
    // above the floor (every namespace a card) the fit zoom is legible
    check(everyClosed || k >= 0.78, `${tag}: fit zoom ${k.toFixed(2)} is too small with ${open.length} namespaces open`);
    check(ms < 120, `${tag}: ${ms.toFixed(0)} ms is too slow`);
    // determinism
    const again = firstView(m, new Map(), { width: w, height: h, touched });
    check([...again.collapsed].sort().join() === [...fv.collapsed].sort().join(), `${tag}: not deterministic`);
    console.log(`${tag.padEnd(34)} items ${String(vis.items.size).padStart(3)}  k ${k.toFixed(2)}  ${ms.toFixed(1)} ms  open ${open.length}`);
  }
}

// peers open together: the three regions of the fleet are one shape
{
  const m = load('graph.fleet.json');
  for (const [w, h] of sizes) {
    const fv = firstView(m, new Map(), { width: w, height: h });
    const regions = m.root.kids.filter((k) => k.kids.length).map((k) => !fv.collapsed.has(k.path));
    check(regions.every((x) => x === regions[0]), `fleet ${w}x${h}: regions of one shape open apart`);
  }
}

// a forced namespace stays open, and so does what is above it
{
  const m = load('graph.platform.json');
  const fv = firstView(m, new Map(), { width: 560, height: 620, must: new Set(['api', 'api.services', 'api.services.billing']) });
  for (const p of ['api', 'api.services', 'api.services.billing']) check(!fv.collapsed.has(p), `must: ${p} was closed`);
}

// an empty graph is no trouble
{
  const m = load('graph.empty.json');
  const fv = firstView(m, new Map(), { width: 776, height: 820 });
  check(fv.collapsed.size === 0, 'empty: namespaces out of nothing');
}

if (failed) { console.log(`${failed} failed`); process.exit(1); }
console.log('ok');
