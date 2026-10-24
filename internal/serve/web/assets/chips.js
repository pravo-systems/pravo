// The filter chips: one row under the toolbar. Each chip is a filter of the nodes (see FILTERS in model.js) with the
// number that it matches; a chip that matches nothing is not offered, unless it is the one that is on.

import { h, tick } from './util.js';
import { notCounted } from './model.js';
import { parseQuery } from './query.js';

export const CHIPS = [
  { key: null, label: 'All', count: 'all', hint: 'Every resource' },
  { key: 'resources', label: 'Resources', count: 'resources', hint: 'The managed resources, without the data sources and the ephemeral ones', kinds: true },
  { key: 'data', label: 'Data sources', count: 'data', hint: 'Data sources: values read, not created' },
  { key: 'ephemeral', label: 'Ephemeral', count: 'ephemeral', hint: 'Ephemeral resources: opened for a run and never recorded' },
  { key: 'plan', label: 'In plan', count: 'plan', hint: 'Resources the plan that is laid over them changes', plan: true },
  { key: 'drifted', label: 'Drifted', count: 'drifted', hint: 'Resources that differ from the real object, or are gone' },
  { key: 'sensitive', label: 'Sensitive', count: 'sensitive', hint: 'Resources and outputs with values that are withheld' },
  { key: 'orphans', label: 'Orphans', count: 'orphans', hint: 'In the state, no longer declared' },
  { key: 'declared', label: 'To create', count: 'declared', hint: 'In the specification and not in the state: nothing has created them yet' },
  { key: 'outputs', label: 'Outputs', count: 'outputs', hint: 'The values the root publishes' },
  { key: 'env', label: 'Env', count: 'env', hint: 'The environment variables the specification reads' },
];

export class Chips {
  constructor(root, cb) {
    this.root = root;
    this.chips = new Map();
    this.pills = [];
    root.addEventListener('click', (ev) => {
      const b = ev.target.closest('button[data-filter]');
      if (b) cb.onFilter(b.dataset.filter || null);
      const x = ev.target.closest('button[data-term]');
      if (x) cb.onRemoveTerm(x.dataset.term);
    });
  }

  build() {
    this.root.textContent = '';
    for (const c of CHIPS) {
      const em = h('em', null, '0');
      const b = h('button', { class: 'chip k-' + (c.key || 'all'), type: 'button', 'aria-pressed': 'false', 'data-tip': c.hint, data: { filter: c.key || '' } }, h('span', null, c.label), em);
      this.root.append(b);
      this.chips.set(c.key || '', { b, em });
    }
  }

  // The terms of the filter box, each as a pill that takes itself out of the text
  renderPills(q) {
    const terms = parseQuery(q).terms;
    const key = terms.map((t) => t.raw).join(' ');
    if (key === this.pillKey) return;
    this.pillKey = key;
    for (const p of this.pills) p.remove();
    this.pills = terms.map((t) => {
      const what = `${t.neg ? 'not ' : ''}${t.col} ${t.values.join(' or ')}`;
      const pill = h('span', { class: 'qpill' + (t.neg ? ' neg' : ''), role: 'group', 'aria-label': 'Filter: ' + what },
        t.neg ? h('i', null, 'not') : null, h('b', null, t.col), h('span', null, t.values.join(', ')),
        h('button', { type: 'button', 'aria-label': 'Remove the filter ' + what, 'data-tip': 'Remove', data: { term: t.raw } }, '×'));
      this.root.append(pill);
      return pill;
    });
  }

  // render takes { model, filter, q }
  render(s) {
    const { model } = s;
    if (!this.chips.size) this.build();
    this.renderPills(s.q || '');
    for (const c of CHIPS) {
      const n = model.counts[c.count];
      const { b, em } = this.chips.get(c.key || '');
      const on = (s.filter || null) === c.key;
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
      // a filter of the plan has no use while none is laid over the resources, and one that matches nothing is not offered,
      // unless it is the one that is on or the records are sealed and cannot say.
      // Resources is there only beside another kind: alone it would be All
      b.hidden = (!!c.plan && !model.hasPlan) || (!!c.kinds && !on && model.counts.data + model.counts.ephemeral === 0) || (n === 0 && !!c.key && !on && !notCounted(model.counts, c.count));
      b.classList.toggle('zero', n === 0 && !!c.key);
      if (notCounted(model.counts, c.count)) {
        // the records are sealed and say nothing of this: a dash, not a zero
        if (em._tick) { cancelAnimationFrame(em._tick); em._tick = 0; }
        delete em.dataset.v;
        em.textContent = '—';
        em.title = 'sealed';
        b.setAttribute('aria-label', `${c.label}, sealed`);
      } else {
        em.removeAttribute('title');
        b.setAttribute('aria-label', `${c.label}, ${n}`);
        tick(em, n);
      }
    }
  }
}
