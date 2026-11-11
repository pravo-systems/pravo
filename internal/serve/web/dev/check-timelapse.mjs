// Checks the model of the Time lapse view (assets/lapse-model.js) on small histories. Run: node check-timelapse.mjs
import { build, HERE, GONE, NOT_YET, activity } from '../assets/lapse-model.js';

let bad = 0;
const check = (ok, what) => { if (!ok) { bad++; console.log('FAIL ' + what); } };
const ch = (addr, action) => ({ addr, namespace: addr.includes('.') ? addr.slice(0, addr.lastIndexOf('.')) : '', action });
const step = (id, ...changes) => ({ id, kind: 'apply', status: 'applied', time: '2026-01-01T00:00:00Z', changes });
const node = (addr, o = {}) => ({ addr, namespace: addr.includes('.') ? addr.slice(0, addr.lastIndexOf('.')) : '', mode: 'managed', ...o });

// adds, a change, a removal and a rename
{
  const doc = { steps: [
    step('1', ch('net.vpc', 'add'), ch('net.sg', 'add')),
    step('2', ch('net.sg', 'change'), ch('app.db', 'add')),
    step('3', ch('net.sg', 'remove'), ch('app.db', 'replace')),
    step('4', ch('app.db', 'remove'), ch('app.db2', 'move')),
  ] };
  const m = build(doc, [node('net.vpc'), node('app.db2')]);
  const at = (addr) => m.byAddr.get(addr).i;
  check(m.frames.length === 5, 'a frame for each step and one before the first');
  check(m.frames[0].state.every((v) => v === NOT_YET), 'nothing is there before a run that adds everything');
  check(m.frames[1].state[at('net.vpc')] === HERE && m.frames[1].state[at('app.db')] === NOT_YET, 'added by step 1');
  check(m.frames[1].counts.here === 2 && m.frames[2].counts.here === 3, 'the count of what is here');
  check(m.frames[3].state[at('net.sg')] === GONE && m.frames[3].counts.here === 2, 'a removed node is gone, not yet-to-come');
  check(m.frames[2].marks.get(at('net.sg')) === 'change' && m.frames[3].marks.get(at('app.db')) === 'replace', 'a step marks what it wrote');
  check(m.frames[4].state[at('app.db2')] === HERE && m.frames[4].state[at('app.db')] === GONE, 'a rename: the old address is gone, the new one is here');
  check(m.frames[4].counts.by.remove === 1 && m.frames[4].counts.by.move === 1, 'counts by action');
  check(m.groups.map((g) => g.ns).join() === 'app,net', 'groups by namespace in order');
  check(m.groups[0].cells.map((c) => c.addr).join() === 'app.db,app.db2', 'cells in the order of their addresses');
}
// a node that a run changes and no run adds was there before the first run; so is one that no run names
{
  const m = build({ steps: [step('1', ch('a.x', 'change'))] }, [node('a.x'), node('a.y'), node('a.z', { presence: 'spec' }), node('a.d', { mode: 'data' }), node('o', { mode: 'output' })]);
  check(m.frames[0].state[m.byAddr.get('a.x').i] === HERE, 'changed first means there before');
  check(m.frames[0].state[m.byAddr.get('a.y').i] === HERE && m.frames[1].state[m.byAddr.get('a.y').i] === HERE, 'recorded and never named: there all along');
  check(!m.byAddr.has('a.z') && !m.byAddr.has('a.d') && !m.byAddr.has('o'), 'a declared node, a data source and an output are not cells');
}
// no steps: one frame
{
  const m = build({ steps: [] }, [node('a.x')]);
  check(m.frames.length === 1 && m.frames[0].counts.here === 1, 'no runs, one frame');
  check(build(null, null).frames.length === 1, 'no document');
  check(activity(build({ steps: [step('1', ch('a.x', 'add'), ch('a.y', 'add'))] }, [])).join() === '2', 'activity is the cells a step wrote');
}
console.log(bad ? `${bad} failed` : 'ok');
process.exit(bad ? 1 : 0);
