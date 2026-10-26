// Folding for the code windows of the page: a block that a line opens with a bracket and a later line closes folds
// into its first line and a chip that says how many lines are behind it.

import { h, icon, plural } from './util.js';

// Token classes that hold text, not structure: a bracket inside one is not a bracket.
const TEXT = new Set(['s', 'x', 'c', 'e']);

// foldRanges returns a Map from the index of a line that opens a block to the index of the line that closes it, for the tokens
// that tokenize or jsonTokens made. A block that opens and closes on one line does not fold.
export function foldRanges(toks) {
  const stack = [];
  const out = new Map();
  toks.forEach((line, i) => {
    for (const t of line) {
      if (TEXT.has(t.t)) continue;
      for (const ch of t.text) {
        if (ch === '{' || ch === '[' || ch === '(') stack.push(i);
        else if (ch === '}' || ch === ']' || ch === ')') {
          const s = stack.pop();
          if (s !== undefined && s < i && !(out.get(s) >= i)) out.set(s, i);
        }
      }
    }
  });
  return out;
}

// hiddenRows says which lines a set of folds hides: the lines after a folded line, up to and with the one that closes it.
export function hiddenRows(count, ends, folded) {
  const hidden = new Array(count).fill(false);
  let to = -1;
  for (let i = 0; i < count; i++) {
    if (i <= to) { hidden[i] = true; continue; }
    if (folded.has(i) && ends.has(i)) to = ends.get(i);
  }
  return hidden;
}

// attachFolds adds a button for each block to the gutter of a read-only window. parts[i] is { row, host, code }: the element of
// the line, the cell of the gutter that holds the button, and the element that holds the text, which gets the chip.
export function attachFolds(parts, toks) {
  const ends = foldRanges(toks);
  if (!ends.size) return null;
  const folded = new Set();
  const buttons = new Map();
  const chips = new Map();
  const paint = () => {
    const hidden = hiddenRows(parts.length, ends, folded);
    parts.forEach((p, i) => {
      p.row.hidden = hidden[i];
      const on = folded.has(i) && ends.has(i) && !hidden[i];
      const b = buttons.get(i);
      if (b) {
        b.setAttribute('aria-expanded', on ? 'false' : 'true');
        b.dataset.tip = on ? 'Unfold' : 'Fold';
      }
      const c = chips.get(i);
      if (c) c.hidden = !on;
    });
  };
  const toggle = (i) => { if (folded.has(i)) folded.delete(i); else folded.add(i); paint(); };
  for (const [i, end] of ends) {
    const p = parts[i];
    if (!p || p.row.classList.contains('gap')) continue;
    const b = h('button', { type: 'button', class: 'fold', 'aria-expanded': 'true', 'data-tip': 'Fold', 'aria-label': `Fold lines ${i + 1} to ${end + 1}`, on: { click: () => toggle(i) } }, icon('chev'));
    const c = h('button', { type: 'button', class: 'fold-chip', hidden: true, 'aria-label': `Unfold lines ${i + 2} to ${end + 1}`, on: { click: () => toggle(i) } }, '⋯ ' + plural(end - i, 'line'));
    p.host.append(b);
    p.code.append(c);
    buttons.set(i, b);
    chips.set(i, c);
  }
  return { fold: (all) => { folded.clear(); if (all) for (const i of buttons.keys()) folded.add(i); paint(); } };
}
