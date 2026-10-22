// Checks that a locked state shows no zero for what its sealed records leave out: the counts of drifted and
// sensitive resources are not counted (notCounted) when every record is sealed, and are counted when none is.
// No DOM, no browser.
//
//   node check-sealed-counts.mjs

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildModel, notCounted } from '../assets/model.js';

const dir = path.join(path.dirname(fileURLToPath(import.meta.url)), 'fixtures');
const graph = () => JSON.parse(fs.readFileSync(path.join(dir, 'graph.platform.json'), 'utf8'));
let failed = 0;
const check = (ok, msg) => { if (!ok) { failed++; console.log('FAIL', msg); } };

const open = buildModel(graph()).counts;
check(open.sealed === 0 && open.drifted > 0 && open.sensitive > 0, `the platform counts drifted ${open.drifted} and sensitive ${open.sensitive}`);
check(!notCounted(open, 'drifted') && !notCounted(open, 'sensitive'), 'a readable state shows its numbers');

// the locked state: every node sealed, with no attribute read and no mark known
const g = graph();
for (const n of g.nodes) { n.sealed = true; n.sensitive = 0; n.drifted = false; n.gone = false; }
const locked = buildModel(g).counts;
check(locked.sealed === g.nodes.length, `sealed ${locked.sealed} of ${g.nodes.length}`);
check(locked.drifted === 0 && locked.sensitive === 0, 'a sealed record is not counted as drifted or sensitive');
check(notCounted(locked, 'drifted') && notCounted(locked, 'sensitive'), 'the zeros of a locked state are dashes');
check(!notCounted(locked, 'orphans') && !notCounted(locked, 'plan') && !notCounted(locked, 'all'), 'the counts that the address alone decides stay numbers');

// some sealed, some read: a count above zero is a number, and a zero is still not known
const part = graph();
part.nodes[0].sealed = true;
const mixed = buildModel(part).counts;
check(!notCounted(mixed, 'drifted') && !notCounted(mixed, 'sensitive'), 'counts above zero stay numbers beside a sealed record');
for (const n of part.nodes) { n.drifted = false; n.gone = false; n.sensitive = 0; }
check(notCounted(buildModel(part).counts, 'drifted'), 'a zero beside a sealed record is a dash');

if (failed) process.exit(1);
console.log('ok');
