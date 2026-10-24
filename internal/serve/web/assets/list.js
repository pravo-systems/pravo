// The list view: every node as a row of a sortable table, with the same filters as the graph. It is grouped by
// namespace by default, as an outline of the graph (a namespace that is open here is open there), and flat when
// grouping is off. Only the rows in view are in the document, so an estate of thousands of resources scrolls freely.

import { h, clear, relTime, stamp, actionOf, store, icon } from './util.js';
import { natural, isOut, isEnv } from './model.js';
import { providerOf, pvClass } from './provider.js';
import { avatar } from './plans.js';
import { compileQuery } from './query.js';

const ROW = 30;
const COLS = [
  { key: 'addr', label: 'Address', cls: 'c-addr' },
  { key: 'type', label: 'Type', cls: 'c-type' },
  { key: 'provider', label: 'Provider', cls: 'c-prov' },
  { key: 'mode', label: 'Mode', cls: 'c-mode' },
  { key: 'status', label: 'Status', cls: 'c-status' },
  { key: 'action', label: 'Plan', cls: 'c-action' },
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
  refreshed: (a, b) => (Date.parse(a.refreshed_at) || 0) - (Date.parse(b.refreshed_at) || 0) || natural(a.addr, b.addr),
};

// facet is the pair of buttons at the end of a cell: show only the rows that hold this value, or hide them
const facet = (col, value) => (value ? h('span', { class: 'cf', role: 'group', 'aria-label': `Filter by ${col} ${value}` },
  h('button', { type: 'button', tabIndex: -1, 'data-tip': `Show only ${col}:${value}`, 'aria-label': `Show only ${col} ${value}`, data: { col, value, neg: '' } }, '+'),
  h('button', { type: 'button', tabIndex: -1, 'data-tip': `Hide ${col}:${value}`, 'aria-label': `Hide ${col} ${value}`, data: { col, value, neg: '1' } }, '−')) : null);

const keyOfNode = (addr) => 'n:' + addr;
const keyOfNs = (path) => 'ns:' + path;

export class ListView {
  constructor(root, cb) {
    this.root = root;
    this.cb = cb;
    this.sort = { key: 'addr', dir: 1 };
    this.grouped = store.get('pravo.group') !== 'flat';
    this.rows = [];
    this.index = new Map();
    this.selKey = null;
    this.count = 0;
    this.s = null;
    this.head = h('div', { class: 'tv-head', role: 'row' });
    for (const c of COLS) {
      this.head.append(h('div', { class: 'tv-h ' + c.cls, role: 'columnheader', 'aria-sort': 'none', data: { key: c.key } },
        h('button', { type: 'button', on: { click: () => this.setSort(c.key) } }, h('span', { class: 'lbl' }, c.label), h('span', { class: 'arrow', 'aria-hidden': 'true' }))));
    }
    this.spacer = h('div', { class: 'tv-spacer' });
    this.body = h('div', { class: 'tv-rows' });
    this.scroller = h('div', { class: 'tv-scroll', tabIndex: 0, role: 'rowgroup', 'aria-label': 'Resources and outputs' }, this.spacer, this.body);
    this.empty = h('p', { class: 'tv-empty', hidden: true }, 'Nothing matches.');
    this.grid = h('div', { class: 'tv-grid', role: 'grid', 'aria-label': 'All resources and outputs', 'aria-readonly': 'true' }, h('div', { class: 'tv-inner', role: 'presentation' }, this.head, this.scroller, this.empty));
    root.append(this.grid);
    this.scroller.addEventListener('scroll', () => this.paint(), { passive: true });
    this.scroller.addEventListener('keydown', (ev) => this.onKey(ev));
    this.body.addEventListener('click', (ev) => this.onClick(ev));
    this.body.addEventListener('dblclick', (ev) => {
      const row = ev.target.closest('[data-ns]');
      if (row) cb.onToggle(row.dataset.ns);
    });
    this.ro = new ResizeObserver(() => this.paint());
    this.ro.observe(this.scroller);
    const saved = store.get('pravo.sort');
    if (saved) { const [k, d] = saved.split(':'); if (SORTERS[k]) this.sort = { key: k, dir: d === 'desc' ? -1 : 1 }; }
    this.shape();
  }

  // shape sets what depends on grouping: the name of the first column and the role of the grid
  shape() {
    this.grid.classList.toggle('grouped', this.grouped);
    this.grid.setAttribute('role', this.grouped ? 'treegrid' : 'grid');
    this.head.querySelector('.c-addr .lbl').textContent = this.grouped ? 'Name' : 'Address';
  }

  setGrouped(on) {
    on = !!on;
    if (on === this.grouped) return;
    this.grouped = on;
    store.set('pravo.group', on ? 'group' : 'flat');
    this.shape();
    this.compute();
    this.paint(true);
  }

  setSort(key) {
    this.sort = { key, dir: this.sort.key === key ? -this.sort.dir : 1 };
    store.set('pravo.sort', this.sort.key + ':' + (this.sort.dir < 0 ? 'desc' : 'asc'));
    this.compute();
    this.paint(true);
  }

  // render takes { model, collapsed, q, sel }; it returns the number of nodes that match.
  render(s) {
    this.s = s;
    this.selKey = this.keyOf(s.sel);
    // the Plan column says what the loaded plan does: with no plan it would be empty, so it is left out
    this.grid.classList.toggle('no-plan', !s.model.hasPlan);
    this.compute();
    this.paint(true);
    return this.count;
  }

  keyOf(sel) {
    if (!sel || !this.s) return null;
    return this.s.model.nodes.has(sel) ? keyOfNode(sel) : keyOfNs(sel);
  }

  matcher() {
    const s = this.s;
    const q = compileQuery(s.q);
    return (n) => !q || q(n);
  }

  compute() {
    const s = this.s;
    if (!s) return;
    const { model } = s;
    const active = !!s.q;
    const test = active ? this.matcher() : () => true;
    const cmp = SORTERS[this.sort.key];
    const dir = this.sort.dir;
    const rows = [];
    let count = 0;
    if (!this.grouped) {
      const list = (active ? model.list.filter(test) : model.list.slice()).sort((a, b) => cmp(a, b) * dir);
      for (const n of list) rows.push({ kind: 'node', n, depth: 0 });
      count = list.length;
    } else {
      // a namespace shows when something below it matches, and the namespaces that hold a match are open
      const has = new Set();
      if (active) for (const n of model.list) if (test(n)) for (let m = n.ns; m; m = m.up) has.add(m.path);
      const visit = (ns, depth) => {
        const kids = ns.kids.slice();
        if (this.sort.key === 'addr' && dir < 0) kids.reverse();
        for (const k of kids) {
          if (active && !has.has(k.path)) continue;
          const open = active ? true : !s.collapsed.has(k.path);
          rows.push({ kind: 'ns', ns: k, depth, open });
          if (open) visit(k, depth + 1);
        }
        const nodes = (active ? ns.nodes.filter(test) : ns.nodes.slice()).sort((a, b) => cmp(a, b) * dir);
        for (const n of nodes) rows.push({ kind: 'node', n, depth });
      };
      visit(model.root, 0);
      count = active ? model.list.filter(test).length : model.list.length;
    }
    this.rows = rows;
    this.count = count;
    this.index = new Map(rows.map((r, i) => [r.kind === 'ns' ? keyOfNs(r.ns.path) : keyOfNode(r.n.addr), i]));
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

  row(r, i, now) {
    return r.kind === 'ns' ? this.nsRow(r, i) : this.nodeRow(r, i, now);
  }

  // a namespace is a row that opens and closes: its name, what it holds, and what a plan does to it
  nsRow(r, i) {
    const m = r.ns, a = m.agg;
    const key = keyOfNs(m.path);
    const on = key === this.selKey;
    const seg = (n, cls) => (n ? h('i', { class: cls, style: `width:${(n / a.n) * 100}%` }) : null);
    const plan = a.plan
      ? h('span', { class: 'mini', 'data-tip': [a.create && `${a.create} create`, a.update + a.replace && `${a.update + a.replace} change`, a.delete && `${a.delete} delete`, a.read && `${a.read} read`].filter(Boolean).join(', ') + ` of ${a.n}`, 'aria-hidden': 'true' },
        seg(a.create, 'm-ok'), seg(a.update + a.replace, 'm-chg'), seg(a.delete, 'm-bad'), seg(a.read, 'm-key'))
      : null;
    const drift = a.drifted + a.gone;
    const what = [a.n || !a.out ? `${a.n} ${a.n === 1 ? 'resource' : 'resources'}` : '', a.out ? `${a.out} ${a.out === 1 ? 'output' : 'outputs'}` : ''].filter(Boolean).join(' · ');
    return h('div', { class: 'tv-row ns' + (on ? ' sel' : ''), role: 'row', 'aria-rowindex': String(i + 2), 'aria-level': String(r.depth + 1), 'aria-expanded': r.open ? 'true' : 'false', 'aria-selected': on ? 'true' : 'false', data: { key, ns: m.path }, style: `--lvl:${r.depth}` },
      h('div', { class: 'c-addr', role: 'gridcell', title: m.path },
        h('button', { class: 'chev' + (r.open ? ' open' : ''), type: 'button', tabIndex: -1, 'aria-label': (r.open ? 'Collapse ' : 'Expand ') + m.name, data: { toggle: m.path } }, icon('chev')),
        h('span', { class: 'nm' }, m.name)),
      h('div', { class: 'c-type', role: 'gridcell' }, what),
      h('div', { class: 'c-prov', role: 'gridcell' }),
      h('div', { class: 'c-mode', role: 'gridcell' }),
      h('div', { class: 'c-status', role: 'gridcell' }, drift ? h('span', { class: 'b chg' }, h('span', { class: 'i i-drift' }), String(drift)) : null),
      h('div', { class: 'c-action', role: 'gridcell' }, plan),
      h('div', { class: 'c-ref', role: 'gridcell' }));
  }

  nodeRow(r, i, now) {
    const n = r.n;
    const a = actionOf(n.action);
    const key = keyOfNode(n.addr);
    const on = key === this.selKey;
    const stat = [];
    if (n.gone) stat.push(h('span', { class: 'b bad' }, h('span', { class: 'i i-gone' }), 'gone'));
    if (n.drifted && !n.gone) stat.push(h('span', { class: 'b chg' }, h('span', { class: 'i i-drift' }), 'drifted'));
    if (n.locked) stat.push(h('span', { class: 'b lock' }, h('span', { class: 'i i-hold' }), 'locked'));
    // beside another badge the word goes, so that the narrow column does not clip it: the lock and the title stay
    if (n.sealed) stat.push(h('span', { class: 'b lock', title: 'The record is sealed and this viewer cannot open it: no value is shown.', 'aria-label': 'sealed' }, h('span', { class: 'i i-lock' }), stat.length ? null : 'sealed'));
    else if (n.sensitive > 0) stat.push(h('span', { class: 'b lock' }, h('span', { class: 'i i-lock' }), String(n.sensitive)));
    if (n.presence === 'spec') stat.push(h('span', { class: 'b' }, isOut(n) ? 'not published' : 'declared only'));
    if (n.presence === 'state') stat.push(h('span', { class: 'b bad' }, 'orphan'));
    const model = this.s.model;
    // a plan lists the value of every output and does not say which it changes: an output is neither planned nor "not read"
    const act = a ? h('span', { class: 'act' }, h('span', { class: 'sq ' + a.cls }), a.label) : (model.hasPlan && !isOut(n) ? h('span', { class: 'dim' }, 'not read') : '');
    const who = (n.plans || []).slice(0, 2).map((id) => avatar(model.authors.get(id) || '?', 'tiny'));
    const name = this.grouped || !(n.addr.length > n.name.length && n.addr.endsWith(n.name))
      ? (this.grouped ? h('b', null, n.name) : n.addr)
      : [h('span', { class: 'pre' }, n.addr.slice(0, n.addr.length - n.name.length)), h('b', null, n.name)];
    return h('div', { class: 'tv-row leaf' + (on ? ' sel' : ''), role: 'row', 'aria-rowindex': String(i + 2), 'aria-selected': on ? 'true' : 'false', 'aria-level': this.grouped ? String(r.depth + 1) : null, data: { key, addr: n.addr }, style: `--lvl:${r.depth}` },
      h('div', { class: 'c-addr' + (isOut(n) ? ' out' : ''), role: 'gridcell', title: n.addr }, name),
      h('div', { class: 'c-type', role: 'gridcell', title: n.type }, n.type, isOut(n) ? null : facet('type', n.type)),
      h('div', { class: 'c-prov', role: 'gridcell', title: n.provider ? 'The provider instance: ' + n.provider : '' }, n.provider ? [h('span', { class: 'pvdot ' + pvClass(providerOf(n.type, n.provider)), 'aria-hidden': 'true' }), n.provider, facet('instance', n.provider)] : h('span', { class: 'dim' }, '—')),
      h('div', { class: 'c-mode' + (n.mode === 'managed' ? ' plain' : ''), role: 'gridcell' }, n.mode, facet('mode', n.mode)),
      h('div', { class: 'c-status', role: 'gridcell' }, stat),
      h('div', { class: 'c-action', role: 'gridcell' }, act, who.length ? h('span', { class: 'who' }, who) : null, a ? facet('plan', n.action) : null),
      h('div', { class: 'c-ref', role: 'gridcell', title: n.refreshed_at ? stamp(n.refreshed_at) : '' }, n.refreshed_at ? relTime(n.refreshed_at, now) : '—'));
  }

  onClick(ev) {
    const f = ev.target.closest('.cf button');
    if (f) { ev.stopPropagation(); this.cb.onFacet(f.dataset.col, f.dataset.value, !!f.dataset.neg); return; }
    const t = ev.target.closest('[data-toggle]');
    if (t) { ev.stopPropagation(); this.cb.onToggle(t.dataset.toggle); return; }
    const row = ev.target.closest('[data-key]');
    if (!row) return;
    if (row.dataset.ns) this.cb.onSelectNamespace(row.dataset.ns);
    else this.cb.onSelectNode(row.dataset.addr);
  }

  // setSelected marks the row of a node or a namespace, and brings it into view
  setSelected(sel, scroll = true) {
    this.selKey = this.keyOf(sel);
    if (!this.s) return;
    const i = this.selKey ? this.index.get(this.selKey) : undefined;
    if (i !== undefined && scroll) {
      const top = i * ROW, st = this.scroller.scrollTop, hgt = this.scroller.clientHeight;
      if (top < st) this.scroller.scrollTop = top - ROW;
      else if (top + ROW > st + hgt) this.scroller.scrollTop = top + ROW - hgt + ROW;
    }
    for (const r of this.body.children) {
      const on = r.dataset.key === this.selKey;
      r.classList.toggle('sel', on);
      r.setAttribute('aria-selected', on ? 'true' : 'false');
    }
  }

  select(i) {
    const r = this.rows[i];
    if (!r) return;
    if (r.kind === 'ns') this.cb.onSelectNamespace(r.ns.path);
    else this.cb.onSelectNode(r.n.addr);
  }

  onKey(ev) {
    if (!this.rows.length || ev.ctrlKey || ev.metaKey || ev.altKey) return;
    const cur = this.selKey ? this.index.get(this.selKey) : undefined;
    const row = cur === undefined ? null : this.rows[cur];
    const last = this.rows.length - 1;
    let next = null;
    if (ev.key === 'ArrowDown') next = cur === undefined ? 0 : Math.min(last, cur + 1);
    else if (ev.key === 'ArrowUp') next = cur === undefined ? 0 : Math.max(0, cur - 1);
    else if (ev.key === 'PageDown') next = Math.min(last, (cur || 0) + 15);
    else if (ev.key === 'PageUp') next = Math.max(0, (cur || 0) - 15);
    else if (ev.key === 'Home') next = 0;
    else if (ev.key === 'End') next = last;
    else if (this.grouped && row) {
      // the keys of a tree: right opens or goes in, left closes or goes out, Enter and space open and close
      if (ev.key === 'ArrowRight' && row.kind === 'ns') {
        if (!row.open) { this.cb.onToggle(row.ns.path); ev.preventDefault(); } else if (cur < last) next = cur + 1;
      } else if (ev.key === 'ArrowLeft') {
        if (row.kind === 'ns' && row.open && !this.s.q) { this.cb.onToggle(row.ns.path); ev.preventDefault(); return; }
        for (let j = cur - 1; j >= 0; j--) if (this.rows[j].kind === 'ns' && this.rows[j].depth < row.depth) { next = j; break; }
      } else if ((ev.key === 'Enter' || ev.key === ' ') && row.kind === 'ns') {
        this.cb.onToggle(row.ns.path); ev.preventDefault(); return;
      } else if (ev.key === '*' && row.kind === 'ns') {
        // open every namespace at this level
        for (const r of this.rows) if (r.kind === 'ns' && r.depth === row.depth && !r.open) this.cb.onToggle(r.ns.path);
        ev.preventDefault();
        return;
      }
    }
    if (next === null) return;
    ev.preventDefault();
    this.select(next);
  }
}
