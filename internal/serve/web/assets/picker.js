// A square list to choose one thing from, in groups, in place of the browser's select: each choice has a label and
// may have a second line. Keys: Up and Down move, Home and End jump, Enter and Space choose, Escape closes.
import { h, icon } from './util.js';

export function picker(groups, value, onPick, labelledBy) {
  const flat = [];
  groups.forEach((g) => g.items.forEach((it) => flat.push(it)));
  const current = flat.find((x) => x.value === value) || flat[0];
  const list = h('div', { class: 'pk-list', role: 'listbox', 'aria-labelledby': labelledBy, hidden: true });
  const btn = h('button', { type: 'button', class: 'pk-btn', 'aria-haspopup': 'listbox', 'aria-expanded': 'false', 'aria-labelledby': labelledBy }, h('span', { class: 'pk-cur' }, current.label), icon('chev'));
  const root = h('div', { class: 'pk' }, btn, list);
  let active = flat.indexOf(current);
  const rows = [];

  groups.forEach((g) => {
    if (g.label) list.append(h('div', { class: 'pk-g', role: 'presentation' }, g.label));
    g.items.forEach((it) => {
      const i = rows.length;
      const row = h('div', { class: 'pk-i', role: 'option', 'aria-selected': String(it === current), id: 'pk-' + labelledBy + '-' + i, on: { mousemove: () => mark(i), mousedown: (ev) => { ev.preventDefault(); choose(i); } } },
        h('span', { class: 'pk-l' }, it.label), it.sub ? h('span', { class: 'pk-s' }, it.sub) : null);
      rows.push(row);
      list.append(row);
    });
  });

  function mark(i) {
    active = Math.max(0, Math.min(rows.length - 1, i));
    rows.forEach((r, j) => r.classList.toggle('on', j === active));
    btn.setAttribute('aria-activedescendant', rows[active].id);
    rows[active].scrollIntoView({ block: 'nearest' });
  }
  function show(on) {
    list.hidden = !on;
    btn.setAttribute('aria-expanded', String(on));
    if (on) mark(flat.indexOf(current));
  }
  function choose(i) {
    show(false);
    btn.focus();
    if (flat[i] !== current) onPick(flat[i].value);
  }
  btn.addEventListener('click', () => show(list.hidden));
  btn.addEventListener('blur', () => show(false));
  btn.addEventListener('keydown', (ev) => {
    const open = !list.hidden;
    const move = { ArrowDown: 1, ArrowUp: -1 }[ev.key];
    if (move) { ev.preventDefault(); if (!open) show(true); else mark(active + move); } else if (ev.key === 'Home' && open) { ev.preventDefault(); mark(0); } else if (ev.key === 'End' && open) { ev.preventDefault(); mark(rows.length - 1); } else if ((ev.key === 'Enter' || ev.key === ' ') && open) { ev.preventDefault(); choose(active); } else if (ev.key === 'Escape' && open) { ev.preventDefault(); ev.stopPropagation(); show(false); }
  });
  return root;
}
