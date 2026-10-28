// The command palette (slash or Cmd/Ctrl-K) and the sheet of shortcuts (?).
//
// One box finds everything the page knows: the resources first (namespaces, items, outputs), then the plans, the
// transactions and the runs that pravo recorded, the objects that discoveries found, and the actions of the page.
// A leading ">" asks for the actions alone. The page gives the sources of the other groups through cb.sources(), and
// the actions with it: an action is { id, group, title, keys, words, hint, run }.

import { h, clear, icon, plural, nodeMark, relTime } from './util.js';
import { fuzzy } from './model.js';

const MAX_NODES = 40, MAX_MODS = 8, MAX_SHARED = 20, MAX_OTHER = 5, MAX_FINDINGS = 6, MAX_ACTIONS = 8;

// tokens splits a query into the words that must all be found.
const tokens = (q) => q.split(/\s+/).filter(Boolean);

// within finds every token in the text, as substrings; the positions of the first find of each are what the title marks.
function within(toks, text) {
  const t = text.toLowerCase();
  const cs = [];
  let score = 0;
  for (const w of toks) {
    const at = t.indexOf(w);
    if (at < 0) return null;
    const start = at === 0 || ' ._-/:'.includes(t[at - 1]);
    score += (start ? 6 : 1) + (at === 0 ? 3 : 0) + w.length;
    for (let k = 0; k < w.length; k++) cs.push(at + k);
  }
  return { score: score - t.length * 0.01, cs };
}

export class Palette {
  constructor(root, cb) {
    this.root = root;
    this.cb = cb;
    this.model = null;
    this.results = [];
    this.groups = [];
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

  setModel(model) { this.model = model; if (this.openFlag) this.refresh(); }

  // refresh searches again for what the page learned meanwhile, and keeps the row the person moved to.
  refresh() {
    if (!this.openFlag) return;
    const was = this.active;
    this.search(this.input.value, true);
    this.setActive(Math.min(was, this.results.length - 1), false);
  }

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

  search(raw, quiet = false) {
    const model = this.model;
    if (!model) return;
    const src = (this.cb.sources && this.cb.sources()) || {};
    const actions = src.actions || [];
    const text = raw.trim();
    const groups = [];
    let total = 0;
    const add = (title, items, all = items.length) => { if (items.length) { groups.push({ title, items }); total += all; } };

    if (text.startsWith('>')) {
      // the actions alone, in the groups they belong to
      const toks = tokens(text.slice(1).toLowerCase());
      const found = this.actionsFor(actions, toks);
      const by = new Map();
      for (const r of found) { if (!by.has(r.a.group)) by.set(r.a.group, []); by.get(r.a.group).push(r); }
      for (const [g, items] of by) add(g, items);
      this.mode = 'actions';
    } else if (!text) {
      // without a query: where to go, the namespaces and the resources the plan changes
      this.mode = 'empty';
      add('Go to', actions.filter((a) => a.group === 'Go to').map((a) => ({ kind: 'action', a, cs: [] })));
      const nss = [], nodes = [];
      for (const m of model.namespaces.values()) if (!m.isRoot && m.depth === 0) nss.push({ kind: 'ns', m, score: 0, cs: [] });
      for (const n of model.list) if (n.action && n.action !== 'no-op' && nodes.length < 12) nodes.push({ kind: 'node', n, score: 0, cs: [] });
      if (!nodes.length) for (const n of model.list.filter((x) => x.mode !== 'output').concat(model.list.filter((x) => x.mode === 'output')).slice(0, 12)) nodes.push({ kind: 'node', n, score: 0, cs: [] });
      add('Namespaces', nss.slice(0, MAX_MODS), nss.length);
      add('In the plan', nodes.filter((x) => x.n.mode !== 'output'));
      add('Outputs', nodes.filter((x) => x.n.mode === 'output'));
    } else {
      this.mode = 'all';
      const q = text.toLowerCase();
      const toks = tokens(q);
      const nss = [], nodes = [];
      for (const m of model.namespaces.values()) {
        if (m.isRoot) continue;
        const f = fuzzy(q, m.path);
        if (f) nss.push({ kind: 'ns', m, score: f.score + 2, cs: f.cs });
      }
      for (const n of model.list) {
        const f = fuzzy(q, n.addr);
        let score = f ? f.score + (n.name.toLowerCase().includes(q) ? 4 : 0) : -1;
        let cs = f ? f.cs : [];
        const ft = fuzzy(q, n.type);
        if (ft && ft.score * 0.8 > score) { score = ft.score * 0.8; cs = []; }
        if (score > -1) nodes.push({ kind: 'node', n, score, cs });
      }
      nss.sort((a, b) => b.score - a.score);
      nodes.sort((a, b) => b.score - a.score);

      const plans = this.plansFor(src.plans || [], toks);
      const runs = this.runsFor(src.runs || [], toks);
      const txns = runs.filter((r) => r.run.txn), plain = runs.filter((r) => !r.run.txn);
      const finds = this.findingsFor(src.discovery, toks);
      const acts = this.actionsFor(actions, toks);
      // the resources keep all the room when nothing else matches; when something does, they leave some of it to that
      const others = plans.length + runs.length + finds.found.length + acts.length > 0;
      const found = nodes.slice(0, others ? MAX_SHARED : MAX_NODES);
      // the outputs are a group of their own, after the resources
      add('Namespaces', nss.slice(0, MAX_MODS), nss.length);
      add('Items', found.filter((x) => x.n.mode !== 'output'));
      add('Outputs', found.filter((x) => x.n.mode === 'output'));
      total += nodes.length - found.length;
      add('Plans', plans.slice(0, MAX_OTHER), plans.length);
      add('Transactions', txns.slice(0, MAX_OTHER), txns.length);
      add('Runs', plain.slice(0, MAX_OTHER), plain.length);
      add('Discovery', finds.found.slice(0, MAX_FINDINGS), finds.total);
      add('Actions', acts.slice(0, MAX_ACTIONS), acts.length);
    }

    this.total = total;
    this.groups = groups;
    this.query = text;
    this.results = groups.flatMap((g) => g.items);
    this.active = 0;
    this.draw();
    if (!quiet) {
      const msg = this.results.length ? `${plural(this.total, 'result')}` : 'No results';
      this.cb.announce && this.cb.announce(msg);
    }
  }

  plansFor(plans, toks) {
    const out = [];
    for (const p of plans) {
      const f = within(toks, `${p.id} ${p.author} ${String(p.status).replace(/-/g, ' ')} plan`);
      if (f) out.push({ kind: 'plan', p, score: f.score, cs: f.cs.filter((c) => c < p.id.length) });
    }
    return out.sort((a, b) => b.score - a.score || Date.parse(b.p.created) - Date.parse(a.p.created));
  }

  runsFor(runs, toks) {
    const out = [];
    for (const r of runs) {
      const title = r.line || r.command;
      const f = within(toks, `${title} ${r.txn || ''} ${r.actor || ''} ${r.reason || ''} ${r.id}`);
      if (f) out.push({ kind: 'run', run: r, score: f.score + (r.txn ? 1 : 0), cs: f.cs.filter((c) => c < title.length) });
    }
    return out.sort((a, b) => b.score - a.score || Date.parse(b.run.start) - Date.parse(a.run.start));
  }

  // findingsFor looks through the objects that the discoveries found; they are many, so a query of one letter leaves them alone.
  findingsFor(doc, toks) {
    const found = [];
    let total = 0;
    if (!doc || toks.join('').length < 2) return { found, total };
    for (const d of doc.discoveries || []) {
      for (const it of d.items || []) {
        const f = within(toks, `${it.id} ${it.name || ''} ${it.status} ${it.by || ''} ${d.name}`);
        if (!f) continue;
        total++;
        if (it.status === 'unmanaged') f.score += 2;
        found.push({ kind: 'finding', d, it, score: f.score, cs: f.cs.filter((c) => c < it.id.length) });
      }
    }
    found.sort((a, b) => b.score - a.score);
    return { found, total };
  }

  actionsFor(actions, toks) {
    const out = [];
    actions.forEach((a, order) => {
      if (!toks.length) { out.push({ kind: 'action', a, score: 0, order, cs: [] }); return; }
      const f = within(toks, `${a.title} ${a.words || ''}`);
      if (f) out.push({ kind: 'action', a, score: f.score, order, cs: f.cs.filter((c) => c < a.title.length) });
    });
    return toks.length ? out.sort((a, b) => b.score - a.score || a.order - b.order) : out;
  }

  draw() {
    clear(this.list);
    let i = 0;
    for (const g of this.groups) {
      this.list.append(h('div', { class: 'pal-grp', role: 'presentation' }, g.title, h('em', null, String(g.items.length))));
      for (const r of g.items) this.list.append(this.option(r, i++));
    }
    if (!this.results.length) {
      this.list.append(h('p', { class: 'pal-none' }, this.mode === 'actions' ? 'No action matches.' : this.query ? `Nothing matches “${this.query}”.` : 'Nothing to show.'));
    }
    const extra = this.total - this.results.length;
    this.foot.textContent = this.results.length ? `${this.results.length}${extra > 0 ? ' of ' + this.total : ''} shown` : '';
    this.setActive(0, true);
  }

  option(r, i) {
    const id = 'pal-o-' + i;
    const row = (cls, ico, title, right) => h('div', { class: 'pal-opt' + cls, role: 'option', id, data: { i: String(i) }, 'aria-selected': 'false' },
      h('span', { class: 'pal-ico' }, ico), h('span', { class: 'pal-t' }, title), right);
    switch (r.kind) {
      case 'ns':
        return row('', icon('chev'), this.hl(r.m.path, r.cs), h('span', { class: 'pal-r' }, plural(r.m.agg.n, 'resource')));
      case 'node':
        return row('', nodeMark(r.n, this.model.hasPlan), this.hl(r.n.addr, r.cs), h('span', { class: 'pal-r' }, r.n.type));
      case 'plan': {
        const p = r.p;
        return row('', icon('file'), this.hl(p.id, r.cs), h('span', { class: 'pal-r' }, `${String(p.status).replace(/-/g, ' ')} · ${p.author}`));
      }
      case 'run': {
        const x = r.run;
        const title = x.line || x.command;
        return row('', icon('trace'), this.hl(title, r.cs), h('span', { class: 'pal-r' }, (x.status === 'error' ? 'failed · ' : '') + (x.txn || relTime(x.start))));
      }
      case 'finding': {
        const it = r.it;
        return row('', icon('radar'), this.hl(it.id, r.cs), h('span', { class: 'pal-r' }, `${it.status} · ${r.d.name}`));
      }
      default: {
        const a = r.a;
        const right = a.keys && a.keys.length
          ? h('span', { class: 'pal-keys' }, a.keys.map((k) => h('kbd', null, k)))
          : a.hint ? h('span', { class: 'pal-r' }, a.hint) : null;
        return row(' act', icon(a.icon || 'chev'), this.hl(a.title, r.cs), right);
      }
    }
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
    switch (r.kind) {
      case 'ns': this.cb.onPick('namespace', r.m.path); break;
      case 'node': this.cb.onPick('node', r.n.addr); break;
      case 'action': r.a.run(); break;
      case 'plan': this.cb.onGo('plan', r.p.id); break;
      case 'run': this.cb.onGo('run', r.run.id); break;
      default: this.cb.onGo('finding', r);
    }
  }
}

export const SHORTCUTS = [
  ['Search', [['/', 'or', 'Ctrl K', 'Search everything, or run an action'], ['>', '', '', 'In the palette: the actions alone']]],
  ['Move around', [['←', '', '', 'To a dependency of the selection'], ['→', '', '', 'To a dependent'], ['↑ ↓', '', '', 'To the sibling above or below'], ['Esc', '', '', 'Go back to where a search began, or clear the selection']]],
  ['View', [['f', '', '', 'Fit the graph'], ['i', '', '', 'Show or hide the project'], ['0', '', '', 'Back to the first view'], ['+', 'or', '−', 'Zoom in or out'], ['[', '', ']', 'Collapse or expand every namespace'], ['Enter', '', '', 'Open or close the selected namespace'], ['t', '', '', 'Switch the theme'], ['g', '', '', 'Graph or list of the resources'], ['a', '', '', 'Inventory or state'], ['p', '', '', 'Inventory or plans'], ['d', '', '', 'Inventory or discovery'], ['o', '', '', 'Inventory or the policies'], ['e', '', '', 'Inventory or the secrets'], ['r', '', '', 'Inventory or the recorded runs'], ['?', '', '', 'This sheet']]],
  ['The list', [['↑ ↓', '', '', 'Select the row above or below, Home and End jump'], ['← →', '', '', 'Close or open a namespace, or go to its row'], ['Enter', 'or', 'Space', 'Close or open the namespace'], ['*', '', '', 'Open every namespace of this level']]],
  ['Pointer', [['Drag', '', '', 'Pan'], ['Wheel', '', 'Pinch', 'Zoom'], ['Double-click', '', '', 'Open or close a namespace']]],
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
