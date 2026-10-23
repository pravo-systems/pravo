// The table view: a dense, searchable, sortable table of every node, with the same filters as the rail.
// Only the rows in view are in the document, so an estate of thousands of resources scrolls freely.

import { h, clear, relTime, stamp, actionOf, store } from './util.js';
import { FILTERS, natural, isOut } from './model.js';
import { providerOf, pvClass } from './provider.js';

const ROW = 30;
const COLS = [
  { key: 'addr', label: 'Address', cls: 'c-addr' },
  { key: 'type', label: 'Type', cls: 'c-type' },
  { key: 'provider', label: 'Provider', cls: 'c-prov' },
  { key: 'mode', label: 'Mode', cls: 'c-mode' },
  { key: 'status', label: 'Status', cls: 'c-status' },
  { key: 'action', label: 'Plan', cls: 'c-action' },
  { key: 'revision', label: 'Rev', cls: 'c-rev num' },
  { key: 'refreshed', label: 'Refreshed', cls: 'c-ref' },
];
const ACTION_ORDER = { delete: 0, replace: 1, update: 2, create: 3, move: 4, import: 5, read: 6, 'no-op': 7, '': 8 };
const statusRank = (n) => (n.gone ? 0 : n.drifted ? 1 : n.presence === 'state' ? 2 : n.locked ? 3 : n.presence === 'spec' ? 4 : n.sensitive ? 5 : 6);

const SORTERS = {
  addr: (a, b) => natural(a.addr, b.addr),
  type: (a, b) => natural(a.type, b.type) || natural(a.addr, b.addr),
  provider: (a, b) => natural(a.provider || '', b.provider || '') || natural(a.addr, b.addr),
  mode: (a, b) => natural(a.mode, b.mode) || natural(a.addr, b.addr),
  status: (a, b) => statusRank(a) - statusRank(b) || natural(a.addr, b.addr),
  action: (a, b) => (ACTION_ORDER[a.action || ''] ?? 9) - (ACTION_ORDER[b.action || ''] ?? 9) || natural(a.addr, b.addr),
  revision: (a, b) => (a.revision || 0) - (b.revision || 0) || natural(a.addr, b.addr),
  refreshed: (a, b) => (Date.parse(a.refreshed_at) || 0) - (Date.parse(b.refreshed_at) || 0) || natural(a.addr, b.addr),
};

export class TableView {
  constructor(root, cb) {
    this.root = root;
    this.cb = cb;
    this.sort = { key: 'addr', dir: 1 };
    this.rows = [];
    this.sel = null;
    this.s = null;
    this.head = h('div', { class: 'tv-head', role: 'row' });
    for (const c of COLS) {
      this.head.append(h('div', { class: 'tv-h ' + c.cls, role: 'columnheader', 'aria-sort': 'none', data: { key: c.key } },
        h('button', { type: 'button', on: { click: () => this.setSort(c.key) } }, c.label, h('span', { class: 'arrow', 'aria-hidden': 'true' }))));
    }
    this.spacer = h('div', { class: 'tv-spacer' });
    this.body = h('div', { class: 'tv-rows' });
    this.scroller = h('div', { class: 'tv-scroll', tabIndex: 0, role: 'rowgroup', 'aria-label': 'Resources and outputs' }, this.spacer, this.body);
    this.empty = h('p', { class: 'tv-empty', hidden: true }, 'Nothing matches.');
    this.grid = h('div', { class: 'tv-grid', role: 'grid', 'aria-label': 'All resources and outputs', 'aria-readonly': 'true' }, h('div', { class: 'tv-inner', role: 'presentation' }, this.head, this.scroller, this.empty));
    root.append(this.grid);
    this.scroller.addEventListener('scroll', () => this.paint(), { passive: true });
    this.scroller.addEventListener('keydown', (ev) => this.onKey(ev));
    this.body.addEventListener('click', (ev) => {
      const row = ev.target.closest('[data-addr]');
      if (row) cb.onSelectNode(row.dataset.addr);
    });
    this.ro = new ResizeObserver(() => this.paint());
    this.ro.observe(this.scroller);
    const saved = store.get('pravo.sort');
    if (saved) { const [k, d] = saved.split(':'); if (SORTERS[k]) this.sort = { key: k, dir: d === 'desc' ? -1 : 1 }; }
  }

  setSort(key) {
    this.sort = { key, dir: this.sort.key === key ? -this.sort.dir : 1 };
    store.set('pravo.sort', this.sort.key + ':' + (this.sort.dir < 0 ? 'desc' : 'asc'));
    this.compute();
    this.paint(true);
  }

  // render takes { model, filter, q, sel }; it returns the number of rows that match.
  render(s) {
    this.s = s;
    this.sel = s.sel;
    // the Plan column says what the loaded plan does: with no plan it would be empty, so it is left out
    this.grid.classList.toggle('no-plan', !s.model.hasPlan);
    this.compute();
    this.paint(true);
    return this.rows.length;
  }

  compute() {
    const s = this.s;
    if (!s) return;
    const f = s.filter ? FILTERS[s.filter] : null;
    const q = (s.q || '').trim().toLowerCase();
    let rows = s.model.list;
    if (f || q) {
      rows = rows.filter((n) => (!f || f(n)) && (!q || n.addr.toLowerCase().includes(q) || n.type.toLowerCase().includes(q) || (n.action || '').includes(q) || n.mode.includes(q)));
    } else rows = rows.slice();
    const cmp = SORTERS[this.sort.key];
    rows.sort((a, b) => cmp(a, b) * this.sort.dir);
    this.rows = rows;
    this.index = new Map(rows.map((n, i) => [n.addr, i]));
    for (const th of this.head.children) th.setAttribute('aria-sort', th.dataset.key === this.sort.key ? (this.sort.dir > 0 ? 'ascending' : 'descending') : 'none');
    this.empty.hidden = rows.length > 0;
    this.grid.setAttribute('aria-rowcount', String(rows.length + 1));
  }

  paint(force) {
    if (!this.s || !this.root.offsetParent) return;
    const top = this.scroller.scrollTop, hgt = this.scroller.clientHeight || 600;
    const start = Math.max(0, Math.floor(top / ROW) - 8);
    const end = Math.min(this.rows.length, Math.ceil((top + hgt) / ROW) + 8);
    if (!force && start === this._start && end === this._end) return;
    this._start = start; this._end = end;
    this.spacer.style.height = this.rows.length * ROW + 'px';
    this.body.style.transform = `translateY(${start * ROW}px)`;
    clear(this.body);
    const now = Date.now();
    const frag = document.createDocumentFragment();
    for (let i = start; i < end; i++) frag.append(this.row(this.rows[i], i, now));
    this.body.append(frag);
  }

  row(n, i, now) {
    const a = actionOf(n.action);
    const on = n.addr === this.sel;
    const stat = [];
    if (n.gone) stat.push(h('span', { class: 'b bad' }, h('span', { class: 'i i-gone' }), 'gone'));
    if (n.drifted && !n.gone) stat.push(h('span', { class: 'b chg' }, h('span', { class: 'i i-drift' }), 'drifted'));
    if (n.locked) stat.push(h('span', { class: 'b lock' }, h('span', { class: 'i i-hold' }), 'locked'));
    // beside another badge the word goes, so that the narrow column does not clip it: the lock and the title stay
    if (n.sealed) stat.push(h('span', { class: 'b lock', title: 'The record is sealed and this viewer cannot open it: no value is shown.', 'aria-label': 'sealed' }, h('span', { class: 'i i-lock' }), stat.length ? null : 'sealed'));
    else if (n.sensitive > 0) stat.push(h('span', { class: 'b lock' }, h('span', { class: 'i i-lock' }), String(n.sensitive)));
    if (n.presence === 'spec') stat.push(h('span', { class: 'b' }, isOut(n) ? 'not published' : 'declared only'));
    if (n.presence === 'state') stat.push(h('span', { class: 'b bad' }, 'orphan'));
    // a plan lists the value of every output and does not say which it changes: an output is neither planned nor "not read"
    const act = a ? h('span', { class: 'act' }, h('span', { class: 'sq ' + a.cls }), a.label) : (this.s.model.hasPlan && !isOut(n) ? h('span', { class: 'dim' }, 'not read') : '');
    return h('div', { class: 'tv-row' + (on ? ' sel' : ''), role: 'row', 'aria-rowindex': String(i + 2), 'aria-selected': on ? 'true' : 'false', data: { addr: n.addr } },
      h('div', { class: 'c-addr' + (isOut(n) ? ' out' : ''), role: 'gridcell', title: n.addr }, isOut(n) ? h('span', { class: 'i i-output omark', 'aria-hidden': 'true' }) : null, n.addr.length > n.name.length && n.addr.endsWith(n.name) ? [h('span', { class: 'pre' }, n.addr.slice(0, n.addr.length - n.name.length)), h('b', null, n.name)] : n.addr),
      h('div', { class: 'c-type', role: 'gridcell', title: n.type }, n.type),
      h('div', { class: 'c-prov', role: 'gridcell', title: n.provider ? 'The provider instance: ' + n.provider : '' }, n.provider ? [h('span', { class: 'pvdot ' + pvClass(providerOf(n.type, n.provider)), 'aria-hidden': 'true' }), n.provider] : h('span', { class: 'dim' }, '—')),
      h('div', { class: 'c-mode', role: 'gridcell' }, n.mode),
      h('div', { class: 'c-status', role: 'gridcell' }, stat),
      h('div', { class: 'c-action', role: 'gridcell' }, act),
      h('div', { class: 'c-rev num', role: 'gridcell' }, n.revision ? String(n.revision) : '—'),
      h('div', { class: 'c-ref', role: 'gridcell', title: n.refreshed_at ? stamp(n.refreshed_at) : '' }, n.refreshed_at ? relTime(n.refreshed_at, now) : '—'));
  }

  setSelected(addr, scroll = true) {
    this.sel = addr;
    if (!this.s) return;
    const i = addr ? this.index.get(addr) : undefined;
    if (i !== undefined && scroll) {
      const top = i * ROW, st = this.scroller.scrollTop, hgt = this.scroller.clientHeight;
      if (top < st) this.scroller.scrollTop = top - ROW;
      else if (top + ROW > st + hgt) this.scroller.scrollTop = top + ROW - hgt + ROW;
    }
    for (const r of this.body.children) {
      const on = r.dataset.addr === addr;
      r.classList.toggle('sel', on);
      r.setAttribute('aria-selected', on ? 'true' : 'false');
    }
  }

  onKey(ev) {
    if (!this.rows.length) return;
    const cur = this.sel ? this.index.get(this.sel) : undefined;
    let next = null;
    if (ev.key === 'ArrowDown') next = cur === undefined ? 0 : Math.min(this.rows.length - 1, cur + 1);
    else if (ev.key === 'ArrowUp') next = cur === undefined ? 0 : Math.max(0, cur - 1);
    else if (ev.key === 'PageDown') next = Math.min(this.rows.length - 1, (cur || 0) + 15);
    else if (ev.key === 'PageUp') next = Math.max(0, (cur || 0) - 15);
    else if (ev.key === 'Home') next = 0;
    else if (ev.key === 'End') next = this.rows.length - 1;
    if (next === null) return;
    ev.preventDefault();
    this.cb.onSelectNode(this.rows[next].addr);
  }
}
