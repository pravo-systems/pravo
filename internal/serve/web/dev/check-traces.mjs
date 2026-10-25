// Checks the model of the Traces view (assets/trace-model.js) on small runs and on the fixture. Run: node check-traces.mjs
import { build, fmtDur, ticks, matcher } from '../assets/trace-model.js';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

let bad = 0;
const check = (ok, what) => { if (!ok) { bad++; console.log('FAIL ' + what); } };
const sp = (id, parent, name, start, dur, extra = {}) => ({ id, parent, name, kind: name.split(' ')[0], start_us: start, dur_us: dur, ...extra });

// nesting, self time, and lanes for spans that overlap
{
  const t = build({ duration_us: 100, spans: [
    sp('a', '', 'pravo apply', 0, 100),
    sp('b', 'a', 'engine apply', 10, 80),
    sp('c', 'b', 'resource x', 10, 40), sp('d', 'b', 'resource y', 20, 30), sp('e', 'b', 'resource z', 60, 20),
    sp('f', 'c', 'provider Apply', 15, 20),
  ] });
  const g = (id) => t.byId.get(id);
  check(g('b').depth === 1 && g('f').depth === 3, 'depth');
  check(g('b').self === 80 - (50 - 10) - 20 + 0 || g('b').self === 20, `self of the engine span is the time no resource covers (got ${g('b').self})`);
  check(g('c').self === 20, 'self of x');
  check(g('c').row === 2 && g('d').row === 4 && g('e').row === 2, `x and z share a lane, y is under x's subtree (rows ${g('c').row} ${g('d').row} ${g('e').row})`);
  check(g('f').row === g('c').row + 1, 'the child is one row under its parent');
  check(t.rows === 5, `rows ${t.rows}`);
  const all = t.spans.map((x) => x.row);
  // no two spans overlap in time on one row
  for (const a of t.spans) for (const b of t.spans) if (a !== b && a.row === b.row && a.start_us < b.end && b.start_us < a.end) check(false, `${a.id} and ${b.id} overlap on row ${a.row}`);
  check(Math.max(...all) < t.rows, 'rows are counted');
}
// a parent that is not in the run makes a root; a cycle does not lose spans
{
  const t = build({ spans: [sp('a', 'zz', 'pravo plan', 0, 10), sp('b', 'a', 'engine plan', 1, 5)] });
  check(t.roots.length === 1 && t.roots[0].id === 'a', 'foreign parent is a root');
  const c = build({ spans: [sp('a', 'b', 'x', 0, 10), sp('b', 'a', 'y', 1, 5)] });
  check(c.spans.length === 2 && c.roots.length >= 1 && c.rows >= 1, 'a cycle');
}
// time
check(fmtDur(840) === '840µs' && fmtDur(12400) === '12.4ms' && fmtDur(3.2e6) === '3.20s' && fmtDur(72e6) === '1m 12s' && fmtDur(1500) === '1.50ms', 'fmtDur');
{
  const t = ticks(0, 55865, 8);
  check(t.length >= 4 && t.length <= 12 && t[0] === 0 && t[1] === 10000, `ticks ${t}`);
  check(ticks(5, 5).length === 0, 'no ticks for an empty window');
}
// the filter
{
  const m = matcher('is:error db');
  check(m(sp('a', '', 'resource db', 0, 1, { status: 'error', kind: 'resource' })) && !m(sp('a', '', 'resource db', 0, 1, { kind: 'resource' })), 'is:error');
  check(matcher('kind:provider')(sp('a', '', 'provider X', 0, 1, { kind: 'provider' })) && !matcher('kind:provider')(sp('a', '', 'x', 0, 1, { kind: 'resource' })), 'kind:');
  check(matcher('') === null, 'empty filter');
  check(matcher('create')(sp('a', '', 'resource x', 0, 1, { attrs: { 'pravo.action': 'create' } })), 'attribute values are searched');
}
// the fixtures
const dir = fileURLToPath(new URL('./fixtures/', import.meta.url));
for (const f of readdirSync(dir).filter((x) => /^trace\..+\.json$/.test(x))) {
  const doc = JSON.parse(readFileSync(dir + f, 'utf8'));
  const t0 = performance.now();
  const t = build(doc);
  const ms = performance.now() - t0;
  check(t.spans.length === doc.spans.length, `${f}: every span is in the model`);
  check(t.roots.length >= 1, `${f}: a root`);
  check(new Set(doc.spans.map((x) => x.id)).size === doc.spans.length, `${f}: span ids are unique`);
  let overlap = 0;
  const byRow = new Map();
  for (const s of t.spans) { if (!byRow.has(s.row)) byRow.set(s.row, []); byRow.get(s.row).push(s); }
  for (const list of byRow.values()) { list.sort((a, b) => a.start_us - b.start_us); for (let i = 1; i < list.length; i++) if (list[i].start_us < list[i - 1].end) overlap++; }
  check(overlap === 0, `${f}: ${overlap} spans overlap on a row`);
  for (const s of t.spans) if (s.parent && s.row <= s.parent.row) check(false, `${f}: ${s.id} is not under its parent`);
  check(ms < 200, `${f}: built ${t.spans.length} spans in ${ms.toFixed(0)} ms`);
  console.log(`${f}: ${t.spans.length} spans, ${t.rows} rows, ${ms.toFixed(1)} ms`);
}
console.log(bad ? `${bad} checks failed` : 'ok');
process.exit(bad ? 1 : 0);
