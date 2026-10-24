// The terms of the filter box, each as a pill after the toolbar: what the filter holds is in view, and the cross on a
// pill takes that term out of the text. The row is not there while the text has no term.

import { h } from './util.js';
import { parseQuery } from './query.js';

export class Pills {
  constructor(root, cb) {
    this.root = root;
    this.key = null;
    root.addEventListener('click', (ev) => {
      const x = ev.target.closest('button[data-term]');
      if (x) cb.onRemoveTerm(x.dataset.term);
    });
  }

  render(q) {
    const terms = parseQuery(q).terms;
    const key = terms.map((t) => t.raw).join(' ');
    if (key === this.key) return;
    this.key = key;
    this.root.textContent = '';
    for (const t of terms) {
      const what = `${t.neg ? 'not ' : ''}${t.col} ${t.values.join(' or ')}`;
      this.root.append(h('span', { class: 'qpill' + (t.neg ? ' neg' : ''), role: 'group', 'aria-label': 'Filter: ' + what },
        t.neg ? h('i', null, 'not') : null, h('b', null, t.col), h('span', null, t.values.join(', ')),
        h('button', { type: 'button', 'aria-label': 'Remove the filter ' + what, 'data-tip': 'Remove', data: { term: t.raw } }, '×')));
    }
  }
}
