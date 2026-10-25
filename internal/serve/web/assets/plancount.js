// What a stored plan does, counted from what it recorded. No DOM.

// touchAction is the action of a change of a stored plan, as the page names it. The plan lists a change that only imports an object as
// "no-op": the object is in the state, and the configuration asks for nothing more of it. It lists no other change that does nothing.
export const touchAction = (c) => (c.action === 'no-op' ? 'import' : c.action);

// planCounts is what a plan changes. The summary that a stored plan keeps counts the adds, the changes, the destroys and the reads; a
// move and an import are in its list of changes and not in the summary, so that a plan that only adopts objects, or only moves them,
// would read as one that did nothing. They are counted from the list here. planned is the number of changes of the plan, those that
// the list leaves out (it stops at 2000) included.
export function planCounts(p) {
  const s = p.summary || {};
  const c = { add: s.add || 0, change: s.change || 0, destroy: s.destroy || 0, read: s.read || 0, move: 0, import: 0, planned: p.planned || 0 };
  for (const t of p.touches || []) {
    const a = touchAction(t);
    if (a === 'move') c.move++;
    else if (a === 'import') c.import++;
  }
  return c;
}

// countWords says the counts in words, for a screen reader and a tooltip: what the plan will do, or, with done, what it did.
export function countWords(c, done = false) {
  const w = done ? ['added', 'changed', 'destroyed', 'imported', 'moved', 'read'] : ['to add', 'to change', 'to destroy', 'to import', 'to move', 'to read'];
  const bits = [c.add && `${c.add} ${w[0]}`, c.change && `${c.change} ${w[1]}`, c.destroy && `${c.destroy} ${w[2]}`,
    c.import && `${c.import} ${w[3]}`, c.move && `${c.move} ${w[4]}`].filter(Boolean);
  if (!bits.length && c.read) bits.push(`${c.read} ${w[5]}`);
  if (!bits.length && c.planned) bits.push(`${c.planned} ${c.planned === 1 ? 'change' : 'changes'}`);
  return bits.join(', ') || 'no changes';
}
