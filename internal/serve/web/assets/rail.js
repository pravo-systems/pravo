// The left rail: filter chips and the module tree. The tree is the outline of the graph: a module that is
// open here is open there. It is a flat list of rows with the tree role, a roving tabindex and arrow keys.
// A change of the rows reuses the rows that stay, so that a module opens and closes with a motion and the
// focus stays where it was.

import { h, icon, tick, motionOn, baseName, nodeMark } from './util.js';
import { FILTERS, notCounted } from './model.js';
import { avatar } from './plans.js';

const LEAF_LIMIT = 120;
const ANIMATE_MAX = 40;

export const CHIPS = [
  { key: null, label: 'All', count: 'all', hint: 'Every resource' },
  { key: 'plan', label: 'In plan', count: 'plan', hint: 'Resources the loaded plan changes' },
  { key: 'drifted', label: 'Drifted', count: 'drifted', hint: 'Resources that differ from the real object, or are gone' },
  { key: 'sensitive', label: 'Sensitive', count: 'sensitive', hint: 'Resources with values that are withheld' },
  { key: 'orphans', label: 'Orphans', count: 'orphans', hint: 'In the state, no longer declared' },
  { key: 'declared', label: 'Not created', count: 'declared', hint: 'Declared only: in the specification, not created yet' },
  { key: 'outputs', label: 'Outputs', count: 'outputs', hint: 'The values the root publishes' },
  { key: 'env', label: 'Env', count: 'env', hint: 'The environment variables the specification reads' },
];

export class Rail {
  constructor(root, cb) {
    this.root = root;
    this.cb = cb;
    this.chipsEl = root.querySelector('.chips');
    this.treeEl = root.querySelector('.tree');
    this.titleEl = root.querySelector('.rail-project');
    this.limits = new Map();
    this.els = new Map();
    this.rows = [];
    this.focusKey = null;
    this.chips = new Map();
    this.chipsEl.addEventListener('click', (ev) => {
      const b = ev.target.closest('button[data-filter]');
      if (!b) return;
      cb.onFilter(b.dataset.filter || null);
    });
    if (this.titleEl) this.titleEl.addEventListener('click', () => cb.onRoot && cb.onRoot());
    this.treeEl.addEventListener('click', (ev) => this.onClick(ev));
    this.treeEl.addEventListener('dblclick', (ev) => {
      const row = ev.target.closest('[data-mod]');
      if (row) cb.onToggle(row.dataset.mod);
    });
    this.treeEl.addEventListener('keydown', (ev) => this.onKey(ev));
    this.treeEl.addEventListener('focusin', (ev) => {
      const row = ev.target.closest('[role=treeitem]');
      if (row) this.focusKey = row.dataset.key;
    });
  }

  buildChips() {
    this.chipsEl.textContent = '';
    for (const c of CHIPS) {
      const em = h('em', null, '0');
      const b = h('button', { class: 'chip k-' + (c.key || 'all'), type: 'button', 'aria-pressed': 'false', 'data-tip': c.hint, data: { filter: c.key || '' } }, h('span', null, c.label), em);
      this.chipsEl.append(b);
      this.chips.set(c.key || '', { b, em });
    }
  }

  // render draws the chips and the rows. s: { model, collapsed, filter, q, sel }
  render(s) {
    const first = !this.rendered;
    this.s = s;
    const { model } = s;
    if (!this.chips.size) this.buildChips();
    for (const c of CHIPS) {
      const n = model.counts[c.count];
      const { b, em } = this.chips.get(c.key || '');
      const on = (s.filter || null) === c.key;
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
      b.classList.toggle('zero', n === 0 && !!c.key);
      if (notCounted(model.counts, c.count)) {
        // the records are sealed and say nothing of this: a dash, not a zero
        if (em._tick) { cancelAnimationFrame(em._tick); em._tick = 0; }
        delete em.dataset.v;
        em.textContent = '\u2014';
        em.title = 'sealed';
        b.setAttribute('aria-label', `${c.label}, sealed`);
      } else {
        em.removeAttribute('title');
        b.setAttribute('aria-label', `${c.label}, ${n}`);
        tick(em, n);
      }
    }
    if (this.titleEl) {
      const g = model.graph.project;
      this.titleEl.firstChild.textContent = g.package || baseName(g.dir);
      tick(this.titleEl.lastChild, model.nres);
      this.titleEl.classList.toggle('sel', !s.sel);
      this.titleEl.setAttribute('aria-current', !s.sel ? 'true' : 'false');
    }

    const active = !!(s.filter || s.q);
    const test = this.matcher(s);
    const has = new Set();
    if (active) {
      for (const n of model.list) if (test(n)) for (let m = n.mod; m; m = m.up) has.add(m.path);
    }
    const rows = [];
    const visit = (mod, depth) => {
      for (const k of mod.kids) {
        if (active && !has.has(k.path)) continue;
        const open = active ? true : !s.collapsed.has(k.path);
        rows.push({ kind: 'mod', mod: k, depth, open, key: 'm:' + k.path });
        if (open) visit(k, depth + 1);
      }
      let shown = 0;
      const limit = Math.max(this.limits.get(mod.path) || 0, LEAF_LIMIT);
      let total = 0;
      for (const n of mod.nodes) if (!active || test(n)) total++;
      for (const n of mod.nodes) {
        if (active && !test(n)) continue;
        if (shown >= limit) break;
        rows.push({ kind: 'node', n, depth, key: 'n:' + n.addr });
        shown++;
      }
      if (total > shown) rows.push({ kind: 'more', mod, depth, rest: total - shown, key: 'x:' + mod.path });
    };
    visit(model.root, 0);
    this.rows = rows;

    if (!rows.length) {
      this.els = new Map();
      this.treeEl.replaceChildren(h('p', { class: 'rail-empty' }, active ? 'Nothing matches the filter.' : 'No resources.'));
      this.rendered = true;
      return;
    }
    const selKey = s.sel ? (model.nodes.has(s.sel) ? 'n:' + s.sel : 'm:' + s.sel) : null;
    const tabKey = rows.some((r) => r.key === selKey) ? selKey : rows[0].key;
    this.reconcile(rows, s, tabKey, selKey, !first);
    this.focusKey = tabKey;
    this.rendered = true;
  }

  // reconcile makes the list of rows the one given: rows that stay are kept, new rows come in, rows that go
  // leave with a motion.
  reconcile(rows, s, tabKey, selKey, animate) {
    const prev = this.els;
    const next = new Map();
    const fresh = rows.filter((r) => !prev.has(r.key)).length;
    const gone = [...prev.keys()].filter((k) => !rows.some((r) => r.key === k)).length;
    const move = animate && motionOn() && prev.size > 0 && fresh <= ANIMATE_MAX && gone <= ANIMATE_MAX;
    const list = [];
    let k = 0;
    for (const r of rows) {
      const sig = signature(r, s);
      let el = prev.get(r.key);
      if (!el || el.dataset.sig !== sig) {
        const isNew = !el;
        el = this.row(r, s);
        el.dataset.sig = sig;
        if (isNew && move) { el.classList.add('enter'); el.style.setProperty('--k', String(Math.min(k++, 14))); }
      }
      next.set(r.key, el);
      list.push(el);
    }
    // the old order, with the rows that go kept where they were
    const old = [...this.treeEl.children].filter((c) => c.dataset && c.dataset.key);
    const merged = [];
    const leaving = [];
    let oi = 0;
    for (const el of list) {
      if (prev.get(el.dataset.key) && old.includes(el)) {
        while (oi < old.length && old[oi] !== el) {
          const o = old[oi++];
          if (!next.has(o.dataset.key) && move) { merged.push(o); leaving.push(o); }
        }
        oi++;
      }
      merged.push(el);
    }
    while (oi < old.length) { const o = old[oi++]; if (!next.has(o.dataset.key) && move) { merged.push(o); leaving.push(o); } }
    const active = document.activeElement && this.treeEl.contains(document.activeElement) ? document.activeElement.dataset.key : null;
    for (const o of leaving) { o.classList.add('leave'); o.tabIndex = -1; o.setAttribute('aria-hidden', 'true'); }
    this.treeEl.replaceChildren(...merged);
    if (leaving.length) setTimeout(() => { for (const o of leaving) o.remove(); }, 240);
    this.els = next;
    for (const [key, el] of next) {
      const on = key === selKey;
      el.classList.toggle('sel', on);
      el.setAttribute('aria-selected', on ? 'true' : 'false');
      el.tabIndex = key === tabKey ? 0 : -1;
    }
    if (active && next.has(active) && document.activeElement !== next.get(active)) next.get(active).focus({ preventScroll: true });
  }

  matcher(s) {
    const f = s.filter ? FILTERS[s.filter] : null;
    const q = (s.q || '').trim().toLowerCase();
    return (n) => (!f || f(n)) && (!q || n.addr.toLowerCase().includes(q) || n.type.toLowerCase().includes(q));
  }

  row(r, s) {
    const lvl = r.depth;
    const base = { role: 'treeitem', 'aria-level': String(lvl + 1), 'aria-selected': 'false', tabIndex: -1, data: { key: r.key }, style: `--lvl:${lvl}` };
    if (r.kind === 'mod') {
      const m = r.mod, a = m.agg;
      const seg = (n, cls) => (n ? h('i', { class: cls, style: `width:${(n / a.n) * 100}%` }) : null);
      const plan = a.plan
        ? h('span', { class: 'mini', 'data-tip': [a.create && `${a.create} create`, a.update + a.replace && `${a.update + a.replace} change`, a.delete && `${a.delete} delete`, a.read && `${a.read} read`].filter(Boolean).join(', ') + ` of ${a.n}`, 'aria-hidden': 'true' },
          seg(a.create, 'm-ok'), seg(a.update + a.replace, 'm-chg'), seg(a.delete, 'm-bad'), seg(a.read, 'm-key'))
        : null;
      const drift = a.drifted + a.gone;
      return h('div', Object.assign(base, { class: 'row mod', 'aria-expanded': r.open ? 'true' : 'false', data: { key: r.key, mod: m.path }, title: m.path }),
        h('button', { class: 'chev' + (r.open ? ' open' : ''), type: 'button', tabIndex: -1, 'aria-label': (r.open ? 'Collapse ' : 'Expand ') + m.name, data: { toggle: m.path } }, icon('chev')),
        h('span', { class: 'nm' }, m.name),
        h('span', { class: 'mk' }, plan, drift ? h('span', { class: 'i i-drift warn', title: `${drift} drifted` }) : null,
          a.out ? h('span', { class: 'mout', title: `${a.out} ${a.out === 1 ? 'output' : 'outputs'}` }, icon('output'), String(a.out)) : null,
          h('span', { class: 'n' }, String(a.n))));
    }
    if (r.kind === 'more') {
      return h('div', Object.assign(base, { class: 'row more', data: { key: r.key, more: r.mod.path } }), h('span', { class: 'nm' }, `${r.rest} more…`));
    }
    const n = r.n;
    return h('div', Object.assign(base, { class: 'row leaf' + (n.mode === 'output' ? ' out' : ''), data: { key: r.key, addr: n.addr }, title: n.mode === 'output' ? `${n.addr}\noutput` : `${n.addr}\n${n.type}` }),
      nodeMark(n, s.model.hasPlan),
      h('span', { class: 'nm' }, n.name),
      h('span', { class: 'marks' },
        n.overlap ? h('span', { class: 'i i-overlap', title: 'changed by ' + (n.plans || []).length + ' open plans' }) : null,
        (n.plans || []).slice(0, 2).map((id) => avatar(s.model.authors.get(id) || '?', 'tiny')),
        n.drifted || n.gone ? h('span', { class: 'i ' + (n.gone ? 'i-gone' : 'i-drift') + ' warn', title: n.gone ? 'gone' : 'drifted' }) : null,
        n.locked ? h('span', { class: 'i i-hold', title: 'locked' }) : null,
        n.sensitive > 0 ? h('span', { class: 'i i-lock key', title: n.mode === 'output' ? 'the value is withheld' : `${n.sensitive} sensitive values` }) : null));
  }

  onClick(ev) {
    const t = ev.target.closest('[data-toggle]');
    if (t) { ev.stopPropagation(); this.cb.onToggle(t.dataset.toggle); return; }
    const row = ev.target.closest('[role=treeitem]');
    if (!row) return;
    this.activate(row);
  }

  activate(row) {
    if (row.dataset.more) { this.limits.set(row.dataset.more, 1e9); this.render(this.s); return; }
    if (row.dataset.mod) this.cb.onSelectModule(row.dataset.mod);
    else if (row.dataset.addr) this.cb.onSelectNode(row.dataset.addr);
  }

  rowsEls() { return [...this.treeEl.querySelectorAll('[role=treeitem]:not(.leave)')]; }

  focusRow(el) {
    if (!el) return;
    for (const r of this.rowsEls()) r.tabIndex = -1;
    el.tabIndex = 0;
    el.focus();
    el.scrollIntoView({ block: 'nearest' });
  }

  onKey(ev) {
    const row = ev.target.closest('[role=treeitem]');
    if (!row) return;
    const rows = this.rowsEls();
    const i = rows.indexOf(row);
    let handled = true;
    switch (ev.key) {
      case 'ArrowDown': this.focusRow(rows[Math.min(rows.length - 1, i + 1)]); break;
      case 'ArrowUp': this.focusRow(rows[Math.max(0, i - 1)]); break;
      case 'PageDown': this.focusRow(rows[Math.min(rows.length - 1, i + 10)]); break;
      case 'PageUp': this.focusRow(rows[Math.max(0, i - 10)]); break;
      case 'Home': this.focusRow(rows[0]); break;
      case 'End': this.focusRow(rows[rows.length - 1]); break;
      case 'ArrowRight':
        if (row.dataset.mod) {
          if (row.getAttribute('aria-expanded') === 'false') this.cb.onToggle(row.dataset.mod);
          else this.focusRow(rows[i + 1]);
        }
        break;
      case 'ArrowLeft':
        if (row.dataset.mod && row.getAttribute('aria-expanded') === 'true') this.cb.onToggle(row.dataset.mod);
        else {
          const lvl = Number(row.getAttribute('aria-level'));
          for (let j = i - 1; j >= 0; j--) if (Number(rows[j].getAttribute('aria-level')) < lvl) { this.focusRow(rows[j]); break; }
        }
        break;
      case '*':
        // open every module at this level
        for (const r of rows) if (r.dataset.mod && r.getAttribute('aria-level') === row.getAttribute('aria-level') && r.getAttribute('aria-expanded') === 'false') this.cb.onToggle(r.dataset.mod);
        break;
      case 'Enter':
      case ' ':
        this.activate(row);
        break;
      default: {
        // type a letter to jump to the next row that starts with it
        // (a letter is the tree's own, found or not: it never reaches the shortcuts of the page, which would
        // switch the theme or the view for a person who looked for a row that starts with t or g)
        if (/^[a-z]$/i.test(ev.key) && !ev.ctrlKey && !ev.metaKey && !ev.altKey) {
          const c = ev.key.toLowerCase();
          const hit = [...rows.slice(i + 1), ...rows.slice(0, i)].find((r) => { const nm = r.querySelector('.nm'); return nm && nm.textContent.toLowerCase().startsWith(c); });
          if (hit) this.focusRow(hit);
          break;
        }
        handled = false;
      }
    }
    if (handled) ev.preventDefault();
  }

  // setSelected updates the selection without drawing the rows again
  setSelected(key, scroll = true) {
    let found = null;
    for (const r of this.treeEl.querySelectorAll('[role=treeitem]')) {
      const on = r.dataset.key === key;
      r.setAttribute('aria-selected', on ? 'true' : 'false');
      r.classList.toggle('sel', on);
      if (on) found = r;
    }
    if (this.titleEl) { this.titleEl.classList.toggle('sel', key === null); this.titleEl.setAttribute('aria-current', key === null ? 'true' : 'false'); }
    if (found) {
      for (const r of this.rowsEls()) r.tabIndex = -1;
      found.tabIndex = 0;
      if (scroll) found.scrollIntoView({ block: 'nearest' });
    }
    return !!found || key === null;
  }

  ensureVisible(addr) {
    // a leaf beyond the limit of its module is shown by raising the limit
    const n = this.s && this.s.model.nodes.get(addr);
    if (!n) return false;
    const i = n.mod.nodes.indexOf(n);
    if (i >= Math.max(this.limits.get(n.mod.path) || 0, LEAF_LIMIT)) { this.limits.set(n.mod.path, i + 1); return true; }
    return false;
  }
}

// signature is what a row looks like, apart from its selection: when it changes the row is drawn again.
function signature(r, s) {
  if (r.kind === 'mod') { const a = r.mod.agg; return ['m', r.mod.name, r.depth, r.open, a.n, a.out, a.plan, a.create, a.update, a.replace, a.delete, a.read, a.drifted, a.gone].join('|'); }
  if (r.kind === 'more') return ['x', r.depth, r.rest].join('|');
  const n = r.n;
  return ['n', n.addr, r.depth, n.mode, n.action || '', n.drifted ? 1 : 0, n.gone ? 1 : 0, n.locked ? 1 : 0, n.sensitive || 0, s.model.hasPlan ? 1 : 0, (n.plans || []).join(','), n.overlap ? 1 : 0].join('|');
}
