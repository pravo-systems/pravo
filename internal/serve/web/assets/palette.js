// The search palette (slash or Cmd/Ctrl-K) and the sheet of shortcuts (?).

import { h, clear, icon, plural, nodeMark } from './util.js';
import { fuzzy } from './model.js';

const MAX_NODES = 40, MAX_MODS = 8;

export class Palette {
  constructor(root, cb) {
    this.root = root;
    this.cb = cb;
    this.model = null;
    this.results = [];
    this.active = 0;
    this.openFlag = false;
    this.input = root.querySelector('input');
    this.list = root.querySelector('.pal-list');
    this.foot = root.querySelector('.pal-count');
    root.querySelector('.pal-scrim').addEventListener('click', () => this.close());
    this.input.addEventListener('input', () => { this.search(this.input.value); this.cb.onQuery && this.cb.onQuery(this.input.value); });
    this.input.addEventListener('keydown', (ev) => this.onKey(ev));
    this.list.addEventListener('pointermove', (ev) => {
      const o = ev.target.closest('[role=option]');
      if (o) this.setActive(Number(o.dataset.i), false);
    });
    this.list.addEventListener('click', (ev) => {
      const o = ev.target.closest('[role=option]');
      if (o) this.pick(Number(o.dataset.i));
    });
    root.addEventListener('keydown', (ev) => {
      if (ev.key === 'Tab') { ev.preventDefault(); this.input.focus(); }
    });
  }

  get isOpen() { return this.openFlag; }

  setModel(model) { this.model = model; if (this.openFlag) this.search(this.input.value); }

  open(q = '') {
    if (this.openFlag) return;
    this.openFlag = true;
    this.returnTo = document.activeElement;
    this.root.hidden = false;
    this.root.classList.add('on');
    this.input.value = q;
    this.input.setAttribute('aria-expanded', 'true');
    this.search(q);
    this.input.focus();
    this.input.select();
    this.cb.onOpen && this.cb.onOpen(true);
  }

  close(restore = true) {
    if (!this.openFlag) return;
    this.openFlag = false;
    this.root.classList.remove('on');
    this.root.hidden = true;
    this.input.setAttribute('aria-expanded', 'false');
    this.input.removeAttribute('aria-activedescendant');
    if (restore && this.returnTo && this.returnTo.focus) { try { this.returnTo.focus(); } catch { /* gone */ } }
    this.cb.onOpen && this.cb.onOpen(false);
  }

  search(raw) {
    const model = this.model;
    if (!model) return;
    const q = raw.trim().toLowerCase();
    const mods = [], nodes = [];
    if (!q) {
      // without a query: the modules and the resources the plan changes
      for (const m of model.modules.values()) if (!m.isRoot && m.depth === 0) mods.push({ kind: 'mod', m, score: 0, cs: [] });
      for (const n of model.list) if (n.action && n.action !== 'no-op' && nodes.length < 12) nodes.push({ kind: 'node', n, score: 0, cs: [] });
      if (!nodes.length) for (const n of model.list.filter((x) => x.mode !== 'output').concat(model.list.filter((x) => x.mode === 'output')).slice(0, 12)) nodes.push({ kind: 'node', n, score: 0, cs: [] });
    } else {
      for (const m of model.modules.values()) {
        if (m.isRoot) continue;
        const f = fuzzy(q, m.path);
        if (f) mods.push({ kind: 'mod', m, score: f.score + 2, cs: f.cs });
      }
      for (const n of model.list) {
        const f = fuzzy(q, n.addr);
        let score = f ? f.score + (n.name.toLowerCase().includes(q) ? 4 : 0) : -1;
        let cs = f ? f.cs : [];
        const ft = fuzzy(q, n.type);
        if (ft && ft.score * 0.8 > score) { score = ft.score * 0.8; cs = []; }
        if (score > -1) nodes.push({ kind: 'node', n, score, cs });
      }
      mods.sort((a, b) => b.score - a.score);
      nodes.sort((a, b) => b.score - a.score);
    }
    const m = mods.slice(0, MAX_MODS), found = nodes.slice(0, MAX_NODES);
    // the outputs are a group of their own, after the resources
    const n = found.filter((x) => x.n.mode !== 'output'), o = found.filter((x) => x.n.mode === 'output');
    this.total = mods.length + nodes.length;
    this.results = [...m, ...n, ...o];
    this.groups = { mods: m.length, nodes: n.length, outs: o.length, q };
    this.active = 0;
    this.draw();
    const msg = this.results.length ? `${plural(this.total, 'result')}` : 'No results';
    this.cb.announce && this.cb.announce(msg);
  }

  draw() {
    clear(this.list);
    const { mods, nodes, outs, q } = this.groups;
    let i = 0;
    const add = (title, count, items) => {
      this.list.append(h('div', { class: 'pal-grp', role: 'presentation' }, title, h('em', null, String(count))));
      for (const r of items) this.list.append(this.option(r, i++));
    };
    if (mods) add('Modules', this.total > this.results.length && q ? `${mods}` : String(mods), this.results.slice(0, mods));
    if (nodes) add(q ? 'Resources' : 'In the plan', String(nodes), this.results.slice(mods, mods + nodes));
    if (outs) add('Outputs', String(outs), this.results.slice(mods + nodes));
    if (!this.results.length) this.list.append(h('p', { class: 'pal-none' }, q ? `Nothing matches “${q}”.` : 'Nothing to show.'));
    const extra = this.total - this.results.length;
    this.foot.textContent = this.results.length ? `${this.results.length}${extra > 0 ? ' of ' + this.total : ''} shown` : '';
    this.setActive(0, true);
  }

  option(r, i) {
    const id = 'pal-o-' + i;
    if (r.kind === 'mod') {
      return h('div', { class: 'pal-opt', role: 'option', id, data: { i: String(i) }, 'aria-selected': 'false' },
        h('span', { class: 'pal-ico' }, icon('chev')),
        h('span', { class: 'pal-t' }, this.hl(r.m.path, r.cs)),
        h('span', { class: 'pal-r' }, plural(r.m.agg.n, 'resource')));
    }
    const n = r.n;
    return h('div', { class: 'pal-opt', role: 'option', id, data: { i: String(i) }, 'aria-selected': 'false' },
      h('span', { class: 'pal-ico' }, nodeMark(n, this.model.hasPlan)),
      h('span', { class: 'pal-t' }, this.hl(n.addr, r.cs)),
      h('span', { class: 'pal-r' }, n.type));
  }

  hl(text, cs) {
    if (!cs.length) return text;
    const out = [];
    const set = new Set(cs);
    let buf = '', on = false;
    for (let i = 0; i < text.length; i++) {
      const m = set.has(i);
      if (m !== on) { if (buf) out.push(on ? h('mark', null, buf) : buf); buf = ''; on = m; }
      buf += text[i];
    }
    if (buf) out.push(on ? h('mark', null, buf) : buf);
    return out;
  }

  setActive(i, scroll) {
    if (!this.results.length) { this.input.removeAttribute('aria-activedescendant'); return; }
    this.active = (i + this.results.length) % this.results.length;
    for (const o of this.list.querySelectorAll('[role=option]')) o.setAttribute('aria-selected', 'false');
    const el = this.list.querySelector('#pal-o-' + this.active);
    if (el) {
      el.setAttribute('aria-selected', 'true');
      this.input.setAttribute('aria-activedescendant', el.id);
      if (scroll) el.scrollIntoView({ block: 'nearest' });
    }
  }

  onKey(ev) {
    switch (ev.key) {
      case 'ArrowDown': ev.preventDefault(); this.setActive(this.active + 1, true); break;
      case 'ArrowUp': ev.preventDefault(); this.setActive(this.active - 1, true); break;
      case 'PageDown': ev.preventDefault(); this.setActive(Math.min(this.results.length - 1, this.active + 6), true); break;
      case 'PageUp': ev.preventDefault(); this.setActive(Math.max(0, this.active - 6), true); break;
      case 'Enter': ev.preventDefault(); this.pick(this.active); break;
      case 'Escape': ev.preventDefault(); ev.stopPropagation(); this.close(); break;
      default:
    }
  }

  pick(i) {
    const r = this.results[i];
    if (!r) return;
    this.close(false);
    if (r.kind === 'mod') this.cb.onPick('module', r.m.path);
    else this.cb.onPick('node', r.n.addr);
  }
}

export const SHORTCUTS = [
  ['Search', [['/', 'or', 'Ctrl K', 'Find a resource, a type or a module']]],
  ['Move around', [['←', '', '', 'To a dependency of the selection'], ['→', '', '', 'To a dependent'], ['↑ ↓', '', '', 'To the sibling above or below'], ['Esc', '', '', 'Clear the selection']]],
  ['View', [['f', '', '', 'Fit the graph'], ['0', '', '', 'Back to the first view'], ['+', 'or', '−', 'Zoom in or out'], ['[', '', ']', 'Collapse or expand every module'], ['Enter', '', '', 'Open or close the selected module'], ['t', '', '', 'Switch the theme'], ['g', '', '', 'Graph or table view'], ['?', '', '', 'This sheet']]],
  ['Module tree', [['↑ ↓', '', '', 'Move between rows, Home and End jump'], ['← →', '', '', 'Close or open a module'], ['Enter', '', '', 'Select the row'], ['*', '', '', 'Open every module of this level'], ['a–z', '', '', 'Jump to the next row that starts with it']]],
  ['Pointer', [['Drag', '', '', 'Pan'], ['Wheel', '', 'Pinch', 'Zoom'], ['Double-click', '', '', 'Open or close a module']]],
];

export class Sheet {
  constructor(root, cb) {
    this.root = root;
    this.cb = cb;
    this.openFlag = false;
    root.querySelector('.pal-scrim').addEventListener('click', () => this.close());
    root.querySelector('.close').addEventListener('click', () => this.close());
    root.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') { ev.stopPropagation(); this.close(); } else if (ev.key === 'Tab') { ev.preventDefault(); root.querySelector('.close').focus(); } });
    const body = root.querySelector('.sheet-body');
    for (const [title, rows] of SHORTCUTS) {
      body.append(h('section', null, h('h3', null, title), h('dl', null, rows.map(([a, mid, b, text]) => [
        h('dt', null, h('kbd', null, a), mid ? h('span', null, mid) : null, b ? h('kbd', null, b) : null),
        h('dd', null, text)]))));
    }
  }

  get isOpen() { return this.openFlag; }

  open() {
    if (this.openFlag) return;
    this.openFlag = true;
    this.returnTo = document.activeElement;
    this.root.hidden = false;
    this.root.classList.add('on');
    this.root.querySelector('.close').focus();
    this.cb.onOpen && this.cb.onOpen(true);
  }

  close() {
    if (!this.openFlag) return;
    this.openFlag = false;
    this.root.classList.remove('on');
    this.root.hidden = true;
    if (this.returnTo && this.returnTo.focus) { try { this.returnTo.focus(); } catch { /* gone */ } }
    this.cb.onOpen && this.cb.onOpen(false);
  }
}
