// The model of the Time lapse view: the runs that wrote nodes (GET /api/timelapse), replayed over the nodes that the
// graph has now. A pure module: no DOM, so that a script can check it (dev/check-timelapse.mjs).
//
// Every node that a run wrote, or that the graph shows as recorded, is a cell with a fixed place: the cells sit in a group
// for each namespace, in the order of their addresses, so that between two steps only what a run did changes. Step 0 is the
// estate before the first run that is kept; step i is the estate after the i-th run. In a frame a cell is NOT_YET (the node
// was not there), HERE, or GONE (it was there and a run removed it), and the run that made the frame marks the cells it wrote.

export const NOT_YET = 0;
export const HERE = 1;
export const GONE = 2;

// the action of a change, as the server words it, by the class that the rest of the page gives that action
export const CLASS = { add: 'a-create', change: 'a-update', replace: 'a-replace', remove: 'a-delete', move: 'a-move' };
export const WORD = { add: 'added', change: 'changed', replace: 'replaced', remove: 'removed', move: 'moved' };
export const ORDER = ['add', 'change', 'replace', 'remove', 'move'];

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

// build makes the model of a document. nodes are the nodes of the graph: a managed resource that the state records (and
// that no run names) was there before the first run that is kept.
export function build(doc, nodes) {
  const steps = (doc && doc.steps) || [];
  const byAddr = new Map(); // address -> cell
  const first = new Map(); // address -> the first action that names it
  const ns = new Map(); // namespace path -> group
  const cell = (addr, space) => {
    let c = byAddr.get(addr);
    if (c) return c;
    c = { addr, ns: space || '', i: 0 };
    byAddr.set(addr, c);
    return c;
  };
  for (const s of steps) for (const c of s.changes) if (!byAddr.has(c.addr)) { cell(c.addr, c.namespace); first.set(c.addr, c.action); }
  for (const n of nodes || []) {
    if (n.mode !== 'managed' || n.presence === 'spec' || byAddr.has(n.addr)) continue; // a node that is only declared is not in the estate
    cell(n.addr, n.namespace);
    first.set(n.addr, 'here');
  }
  const cells = [...byAddr.values()].sort((a, b) => collator.compare(a.ns, b.ns) || collator.compare(a.addr, b.addr));
  cells.forEach((c, i) => { c.i = i; });
  const groups = [];
  for (const c of cells) {
    let g = ns.get(c.ns);
    if (!g) { g = { ns: c.ns, cells: [] }; ns.set(c.ns, g); groups.push(g); }
    g.cells.push(c);
  }
  // frame 0: what the first run did not make is already there
  const state = new Uint8Array(cells.length);
  for (const c of cells) { const a = first.get(c.addr); if (a !== 'add' && a !== 'move') state[c.i] = HERE; }
  const frames = [{ state: state.slice(), marks: new Map(), counts: tally(state, null) }];
  for (const s of steps) {
    const marks = new Map();
    for (const ch of s.changes) {
      const c = byAddr.get(ch.addr);
      marks.set(c.i, ch.action);
      state[c.i] = ch.action === 'remove' ? GONE : HERE;
    }
    const by = {};
    for (const a of marks.values()) by[a] = (by[a] || 0) + 1;
    frames.push({ state: state.slice(), marks, counts: tally(state, by) });
  }
  // touches: for each address, the steps (1-based, oldest first) that wrote it, with the action, so that a resource can be followed run by run
  const touches = new Map();
  steps.forEach((s, k) => { for (const ch of s.changes) { if (!touches.has(ch.addr)) touches.set(ch.addr, []); touches.get(ch.addr).push({ step: k + 1, action: ch.action }); } });
  return { cells, groups, byAddr, steps, frames, touches };
}

// neighbours says which runs wrote an address before and after a step: the nearest of each, 0 when there is none.
export function neighbours(model, addr, step) {
  const list = (model.touches && model.touches.get(addr)) || [];
  let prev = 0, next = 0;
  for (const t of list) { if (t.step < step) prev = t.step; else if (t.step > step && !next) next = t.step; }
  return { prev, next };
}

function tally(state, by) {
  let here = 0;
  for (const v of state) if (v === HERE) here++;
  return { here, by: by || {} };
}

// perStep is how many cells each step wrote, for the heights of the bars of the timeline.
export function activity(model) {
  return model.frames.slice(1).map((f) => f.marks.size);
}
