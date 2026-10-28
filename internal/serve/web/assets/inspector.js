// The inspector on the right: the project when nothing is selected, a namespace, or a node with the tabs
// State, Source, Schema, History and Change. Values arrive redacted from the server; this file never asks
// for more and has no control that reveals a withheld value.

import { h, clear, icon, relTime, stamp, copyButton, actionOf, plural, motionOn, splitPath, pathEl, baseName, nodeMark, signatureMark } from './util.js';
import * as api from './api.js';
import { isChange, chain, isOut, isEnv, isSecret } from './model.js';
import { entityOf, ENTITY_LABEL, facetHit } from './query.js';
import { codeWindow, kindLabel } from './ui.js';
import { StateView, withheld, unknownEl, timeRow } from './values.js';
import { projectPanel, namespacePanel } from './panels.js';
import { avatar, statusChip, conflictAt } from './plans.js';

const TABS = [
  { key: 'state', label: 'State' },
  { key: 'source', label: 'Source' },
  { key: 'schema', label: 'Schema' },
  { key: 'history', label: 'History' },
  { key: 'change', label: 'Change' },
];

export class Inspector {
  constructor(root, cb) {
    this.root = root;
    this.cb = cb;
    this.head = root.querySelector('.ih');
    this.strip = root.querySelector('.istrip');
    this.plansRow = root.querySelector('.iplans');
    this.tabs = root.querySelector('.itabs');
    this.body = root.querySelector('.ibody');
    this.cache = new Map();
    this.token = 0;
    this.open = new Map();
    this.mode = null;
    this.addr = null;
    this.tab = 'state';
    this.ink = h('i', { class: 'itab-ink', 'aria-hidden': 'true' });
    this.state = new StateView({ open: this.open, announce: (t) => cb.announce(t), onFacet: (col, value, neg) => cb.onFacet(col, value, neg), fieldMatch: () => cb.fieldMatch() });
    this.values = this.state;
    this.head.addEventListener('click', (ev) => {
      const f = ev.target.closest('button.fx');
      if (f) cb.onFacet(f.dataset.col, f.dataset.value, ev.shiftKey);
    });
    this.tabs.addEventListener('keydown', (ev) => this.onTabKey(ev));
    this.body.addEventListener('scroll', () => { this.root.classList.toggle('scrolled', this.body.scrollTop > 4); }, { passive: true });
    this.body.addEventListener('click', (ev) => {
      const ref = ev.target.closest('[data-goto]');
      if (ref) { ev.preventDefault(); cb.onSelectNode(ref.dataset.goto); return; }
      const ns = ev.target.closest('[data-gons]');
      if (ns) { ev.preventDefault(); cb.onSelectNamespace(ns.dataset.gons); return; }
      const tb = ev.target.closest('[data-tab]');
      if (tb) { cb.onTab(tb.dataset.tab); return; }
      const rc = ev.target.closest('[data-reach]');
      if (rc) { cb.onReach(rc.dataset.reach); return; }
      const rt = ev.target.closest('[data-retry]');
      if (rt) { this.cache.clear(); this.render(); return; }
      const pl = ev.target.closest('[data-plan]');
      if (pl) cb.onPlan(pl.dataset.plan);
    });
    this.plansRow.addEventListener('click', (ev) => {
      const pl = ev.target.closest('[data-plan]');
      if (pl) cb.onPlan(pl.dataset.plan);
    });
    this.head.addEventListener('click', (ev) => {
      const ns = ev.target.closest('[data-gons]');
      if (ns) { ev.preventDefault(); cb.onSelectNamespace(ns.dataset.gons); }
    });
    window.addEventListener('resize', () => this.placeInk(false));
  }

  invalidate() { this.cache.clear(); }

  // paint puts a new body in and lets it come in.
  paint(...kids) {
    clear(this.body);
    this.body.classList.remove('loading');
    this.body.append(...kids.flat().filter(Boolean));
    this.body.classList.remove('in');
    if (motionOn()) { void this.body.offsetWidth; this.body.classList.add('in'); }
  }

  fetch(key, fn) {
    let p = this.cache.get(key);
    if (!p) {
      p = fn();
      this.cache.set(key, p);
      p.catch(() => this.cache.delete(key));
    }
    return p;
  }

  // ---- the header: a title, the place of the thing in its namespaces, tags ----

  // facetPill is a tag that filters by what it says: a click shows only that, shift hides it
  facetPill(cls, col, value, text, what) {
    return h('button', { type: 'button', class: cls + ' fx', 'data-col': col, 'data-value': value, title: `Show only ${what || col} ${value} (shift: hide it)` }, text);
  }

  // markFacets lights the tags of the header that the terms of the filter name
  markFacets() {
    const q = this.cb.query ? this.cb.query() : '';
    for (const b of this.head.querySelectorAll('button.fx')) {
      const on = facetHit(q, b.dataset.col, b.dataset.value);
      b.classList.toggle('hit', on);
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
    }
  }

  // queryChanged shows again which values the field terms of the filter are about, when the State tab is open
  queryChanged() {
    this.markFacets();
    if (this.mode === 'node' && this.state.host && this.state.host.isConnected) this.state.drawKeep();
  }

  header({ kick, kickFacet, title, path, tags, copy, long }) {
    clear(this.head);
    // append() writes the word "null" for a null: only what exists goes in
    this.head.append(...[
      h('div', { class: 'ih-top' }, (kickFacet ? this.facetPill('kick fx', 'entity', kickFacet, kick) : h('span', { class: 'kick' }, kick)), copy ? this.copyBtn(copy) : null),
      h('h2', { class: 'ih-title' + (long ? ' long' : '') }, title),
      path,
      tags && tags.length ? h('div', { class: 'ih-tags' }, tags) : null].filter(Boolean));
    this.markFacets();
  }

  copyBtn(text) {
    return copyButton(text, { label: 'Copy the address', tip: 'Copy the address', cls: 'ih-cp', announce: (t) => this.cb.announce(t), done: 'Copied the address' });
  }

  // crumbs: the namespaces a thing sits in, each a link to that namespace
  crumbs(ns, model, last) {
    const nss = ns ? chain(ns) : [];
    if (!nss.length && !last) return null;
    const out = h('div', { class: 'ih-path', 'aria-label': 'Where it sits' });
    nss.forEach((m, i) => {
      if (i) out.append(h('span', { class: 'sep', 'aria-hidden': 'true' }, '.'));
      out.append(h('a', { class: 'mseg', href: '#', data: { gons: m.path }, title: `Namespace ${m.path}` }, m.name));
    });
    if (last) { if (nss.length) out.append(h('span', { class: 'sep', 'aria-hidden': 'true' }, '.')); out.append(h('span', { class: 'cur' }, last)); }
    return out;
  }

  noTabs() {
    this.body.removeAttribute('aria-labelledby');
    this.body.setAttribute('role', 'region');
    this.body.setAttribute('aria-label', 'Details');
    this.tabs.hidden = true;
    clear(this.tabs);
    this.strip.hidden = true;
    clear(this.strip);
    this.plansRow.hidden = true;
    clear(this.plansRow);
    this.ink.style.opacity = '0';
  }

  // ---- entry points ----

  showProject(model) {
    this.mode = 'project';
    this.token++;
    this.noTabs();
    const g = model.graph, p = g.project;
    this.header({
      kick: 'Project',
      title: p.package || baseName(p.dir),
      path: h('div', { class: 'ih-dir', 'data-tip': p.dir }, pathEl(p.dir)),
      copy: p.dir,
      tags: [h('span', { class: 'b' }, p.state && p.state.backend || 'state'), h('span', { class: 'b' }, 'pravo ' + p.version)],
    });
    this.paint(projectPanel(this, model));
    this.root.dataset.mode = 'project';
    this.root.dataset.act = '';
    this.root.classList.remove('scrolled');
  }

  showNamespace(ns, model, collapsed) {
    this.mode = 'namespace';
    this.nsAddr = ns.path;
    this.token++;
    this.noTabs();
    const a = ns.agg;
    this.header({
      kick: 'Namespace',
      title: ns.name,
      long: ns.name.length > 22,
      path: this.crumbs(ns.up && !ns.up.isRoot ? ns.up : null, model, null),
      copy: ns.path,
      tags: [h('span', { class: 'b' }, plural(a.n, 'resource')), ns.kids.length ? h('span', { class: 'b' }, plural(ns.kids.length, 'sub-namespace')) : null, a.out ? h('span', { class: 'b' }, plural(a.out, 'output')) : null],
    });
    this.paint(namespacePanel(this, ns, model, collapsed));
    this.root.dataset.mode = 'namespace';
    this.root.dataset.act = '';
    this.root.classList.remove('scrolled');
  }

  // showNode shows a node; tab is the tab to show.
  showNode(node, model, tab, graph) {
    const same = this.mode === 'node' && this.addr === node.addr;
    this.prevDetail = same ? this.detail : null; // a refresh of the node on show: what changed flashes
    this.mode = 'node';
    this.model = model;
    this.graph = graph;
    this.node = node;
    this.addr = node.addr;
    this.tab = tab;
    if (!same) { this.state.q = ''; this.root.classList.remove('scrolled'); }
    this.root.dataset.mode = 'node';
    this.render(same);
  }

  setTab(tab) {
    this.tab = tab;
    if (this.mode === 'node') this.render(true);
  }

  // an output has a value and a declaration: no schema, no history, and a plan never changes it
  tabList() {
    if (this.node && isOut(this.node)) return TABS.filter((t) => t.key === 'state' || t.key === 'source');
    return TABS.filter((t) => t.key !== 'change' || this.model.hasPlan);
  }

  // ---- header, strip, tabs of a node ----

  nodeHead(node, detail) {
    const n = detail ? detail.node : node;
    const prov = detail ? detail.provider : null;
    const tags = [];
    const out = isOut(n);
    // an output and a plain variable are what the kicker says: the type would only repeat it
    if (!out && n.type !== 'env') tags.push(this.facetPill('b ty', 'type', n.type, n.type));
    // the provider key is the provider type and its instance (aws.west): three chips with the version, the first two facets of
    // the filter (provider:aws is the type, instance:aws.west the instance); the version is not a column
    const pkey = node.provider || (prov && prov.key) || '';
    const dot = pkey.indexOf('.');
    const pname = dot < 0 ? pkey : pkey.slice(0, dot);
    const pinst = dot < 0 ? '' : pkey.slice(dot + 1);
    const pchip = (col, value, text, what) => (node.provider ? this.facetPill('b pv', col, value, text, what) : h('span', { class: 'b pv' }, text));
    if (pname) tags.push(pchip('provider', pname, pname, 'provider type'));
    if (pinst) tags.push(pchip('instance', pkey, pinst, 'provider instance'));
    if (prov && prov.version) tags.push(h('span', { class: 'b pv', title: 'Provider version' }, prov.version));
    if (n.sealed) tags.push(h('span', { class: 'b lock' }, icon('lock'), 'sealed'));
    if (n.sensitive > 0) tags.push(h('span', { class: 'b lock' }, icon('lock'), out ? 'sensitive' : `${n.sensitive} sensitive`));
    if (n.no_schema) tags.push(h('span', { class: 'b chg' }, 'no schema'));
    // the header takes the colour of what the plan does to the resource, so that the panel says it before the strip does
    this.root.dataset.act = n.action && n.action !== 'no-op' ? n.action : '';
    this.header({
      kick: ENTITY_LABEL[entityOf(n)] || n.mode,
      kickFacet: entityOf(n),
      title: n.name,
      long: n.name.length > 22,
      path: this.crumbs(this.model.nodes.get(n.addr) ? this.model.nodes.get(n.addr).ns : null, this.model, null),
      copy: n.addr,
      tags,
    });
    this.nodeStrip(n, detail);
  }

  nodeStrip(n, detail) {
    const cell = (label, value, cls, tip) => h('div', { class: 'cell' + (cls ? ' ' + cls : ''), 'data-tip': tip || null }, h('span', { class: 'cl' }, label), h('span', { class: 'cv' }, value));
    const model = this.model;
    const cells = [];
    const a = actionOf(n.action);
    if (isOut(n)) {
      // an output has no record, no revision, no lock and no read time: what it has is a value and a place in the state
      const sens = n.sensitive > 0;
      let rec, rcls, rtip;
      if (n.presence === 'spec') { rec = 'not published'; rcls = 'dimv'; rtip = 'Declared in the specification; no apply has recorded its value yet'; } else if (n.presence === 'state') { rec = 'orphan'; rcls = 'bad'; rtip = 'Recorded in the state, no longer declared: the next apply would remove it'; } else { rec = [icon('check'), 'recorded']; rcls = 'ok'; rtip = 'Declared, and the last apply recorded its value'; }
      cells.push(cell('Value', sens ? [icon('lock'), 'withheld'] : n.presence === 'spec' ? 'not known yet' : 'shown', sens ? 'lock' : n.presence === 'spec' ? 'dimv' : '', sens ? 'The output is sensitive: the server does not send the value' : 'The value the last apply recorded'));
      cells.push(cell('Record', rec, rcls, rtip));
      clear(this.plansRow);
      this.plansRow.hidden = true;
      clear(this.strip);
      this.strip.append(...cells);
      this.strip.hidden = false;
      return;
    }
    if (model.hasPlan) {
      if (a && n.action !== 'no-op') cells.push(cell('Plan', [h('i', { class: 'sq ' + a.cls }), a.label], 'act-' + n.action, 'What the loaded plan does to this resource'));
      else if (n.action === 'no-op') cells.push(cell('Plan', 'no change', '', 'The plan ran this resource and found nothing to change'));
      else cells.push(cell('Plan', 'not read', 'dimv', 'The loaded plan did not run this resource'));
    }
    let sync, scls, stip;
    const never = ['ephemeral', 'call', 'builtin'].includes(n.mode);
    if (never) { sync = 'not recorded'; scls = 'dimv'; stip = 'Evaluated when a command runs and never written to the state'; } else if (n.gone) { sync = [icon('gone'), 'gone']; scls = 'bad'; stip = 'A read found that the real object is absent'; } else if (n.drifted) { sync = [icon('drift'), 'drifted']; scls = 'chg'; stip = 'A read found that the real object differs from the record'; } else if (n.presence === 'spec') { sync = 'declared only'; scls = 'dimv'; stip = 'Declared in the specification, not created yet'; } else if (n.presence === 'state') { sync = 'orphan'; scls = 'bad'; stip = 'Recorded in the state, no longer declared: the next apply would destroy it'; } else if (n.sealed) { sync = [icon('lock'), 'sealed']; scls = 'dimv'; stip = 'The record is sealed and this viewer cannot open it, so whether it drifted is not known'; } else { sync = [icon('check'), 'in sync']; scls = 'ok'; stip = 'Declared and recorded, with no drift found'; }
    cells.push(cell('Record', sync, scls, stip));
    if (n.locked) cells.push(cell('Lock', [icon('hold'), 'held'], 'lock', 'A lock or reservation holds this resource'));
    if (!never) {
      cells.push(cell('Revision', n.revision ? String(n.revision) : '—', n.revision ? '' : 'dimv', 'The revision of the record'));
      const when = n.refreshed_at || (detail && detail.times && detail.times.refreshed_at);
      cells.push(cell('Refreshed', when ? relTime(when) : '—', when ? '' : 'dimv', when ? 'Last read from the provider: ' + stamp(when) : 'Never read'));
    }
    const open = n.plans || [];
    if (open.length) {
      // the stored plans that can still be applied and change this resource, on a row of their own: each is a way to lay it over the graph
      const chips = open.slice(0, 4).map((id) => {
        const author = model.authors.get(id) || '?';
        return h('button', { class: 'plchip', type: 'button', data: { plan: id }, 'aria-label': `Lay ${id} by ${author} over the graph`, 'data-tip': `${id}\nby ${author}\nClick to lay it over the graph` }, avatar(author));
      });
      if (open.length > 4) chips.push(h('span', { class: 'plmore' }, '+' + (open.length - 4)));
      // a row of its own under the strip, and not a cell of it: the cells keep the room that they have
      // a partly stale plan lists the change although it cannot apply it now; it is no overlap
      const stuck = open.filter((id) => conflictAt(model.planById.get(id), n.addr)).length;
      clear(this.plansRow);
      this.plansRow.className = 'iplans' + (n.overlap ? ' ovl' : '');
      this.plansRow.append(h('span', { class: 'cl' }, 'Plans'), h('span', { class: 'plchips' }, chips),
        h('span', { class: 'plsay' }, (open.length > 1 ? `${open.length} open plans change it` : '1 open plan changes it') + (stuck ? ` · ${stuck} can't apply it` : '')), ...(n.overlap ? [h('span', { class: 'plovl' }, 'overlap')] : []));
      this.plansRow.dataset.tip = n.overlap ? 'Applying one of these makes the others stale' : stuck ? 'The state moved on under this resource: a partly stale plan cannot apply its change' : 'A stored plan that can be applied changes this resource';
    }
    this.plansRow.hidden = !open.length;
    if (!open.length) clear(this.plansRow);
    clear(this.strip);
    this.strip.append(...cells);
    this.strip.hidden = false;
  }

  ensureTabs() {
    const list = this.tabList();
    const keys = list.map((t) => t.key).join(',');
    this.tabs.hidden = false;
    this.body.setAttribute('role', 'tabpanel');
    this.body.removeAttribute('aria-label');
    if (this.tabKeys !== keys || !this.tabs.contains(this.ink)) {
      clear(this.tabs);
      this.tabs.append(this.ink);
      for (const t of list) {
        this.tabs.append(h('button', { class: 'itab', role: 'tab', type: 'button', id: 'tab-' + t.key, 'aria-controls': 'ibody', data: { tab: t.key }, on: { click: () => this.cb.onTab(t.key) } }, h('span', { class: 'tn' }, t.label), h('span', { class: 'tc' })));
      }
      this.tabKeys = keys;
      this.inkFresh = true;
    }
    for (const b of this.tabs.querySelectorAll('.itab')) {
      const on = b.dataset.tab === this.tab;
      b.classList.toggle('on', on);
      b.setAttribute('aria-selected', on ? 'true' : 'false');
      b.tabIndex = on ? 0 : -1;
      const dot = b.dataset.tab === 'change' && this.node && isChange(this.node.action);
      b.classList.toggle('flag', !!dot);
    }
    this.body.setAttribute('aria-labelledby', 'tab-' + this.tab);
    this.placeInk(!this.inkFresh);
    this.inkFresh = false;
  }

  placeInk(animate) {
    const on = this.tabs.querySelector('.itab.on');
    if (!on || this.tabs.hidden) return;
    const ink = this.ink;
    if (!animate) ink.style.transition = 'none';
    ink.style.opacity = '1';
    ink.style.width = on.offsetWidth - 2 + 'px';
    ink.style.transform = `translateX(${on.offsetLeft + 1}px)`;
    if (!animate) { void ink.offsetWidth; ink.style.transition = ''; }
  }

  counts(detail) {
    const set = (key, n) => { const el = this.tabs.querySelector(`#tab-${key} .tc`); if (el) { el.textContent = n ? String(n) : ''; el.parentNode.classList.toggle('counted', !!n); } };
    const c = detail.change;
    set('history', (detail.history || []).length);
    set('change', c ? (c.attrs || []).length : 0);
    this.placeInk(false);
  }

  render(keepScroll) {
    const node = this.node, model = this.model;
    const my = ++this.token;
    // the plan the answers are about: a stored plan by id, none at all, or (not given) the plan of the graph
    const plan = model.planParam;
    const key = (k) => `${k}:${node.addr}:${model.graph.revision}:${plan || ''}`;
    this.nodeHead(node, null);
    this.ensureTabs();
    if (!keepScroll) this.body.scrollTop = 0;
    this.body.classList.add('loading');
    const timer = setTimeout(() => { if (my === this.token && this.body.classList.contains('loading')) { clear(this.body); this.body.append(this.skeleton()); } }, 150);
    const need = [this.fetch(key('detail'), () => api.detail(node.addr, plan))];
    if (this.tab === 'source') need.push(this.fetch(key('source'), () => api.source(node.addr, plan)));
    Promise.all(need).then(([detail, source]) => {
      clearTimeout(timer);
      if (my !== this.token) return;
      if (model.specError && detail && detail.node) detail.node = Object.assign({}, detail.node, { presence: 'both' });
      this.detail = detail;
      this.nodeHead(node, detail);
      this.ensureTabs();
      this.counts(detail);
      this.state.prev = this.prevDetail && motionOn() ? attrIndex(this.prevDetail) : null;
      this.prevDetail = null;
      const scroll = keepScroll ? this.body.scrollTop : 0;
      switch (this.tab) {
        case 'source': this.paint(this.sourcePane(source, detail)); break;
        case 'schema': this.paint(this.schemaPane(detail)); break;
        case 'history': this.paint(this.historyPane(detail)); break;
        case 'change': this.paint(this.changePane(detail, node)); break;
        default: this.paint(this.statePane(detail, node));
      }
      if (keepScroll && this.tab === 'state') this.body.scrollTop = scroll;
    }).catch((err) => {
      clearTimeout(timer);
      if (my !== this.token) return;
      if (err instanceof api.Unauthorized) return; // the page shows the gate
      this.paint(h('div', { class: 'errbox', role: 'alert' }, h('p', null, 'Could not load this resource.'), h('p', { class: 'dim' }, String(err.message || err)), h('button', { class: 'btn', type: 'button', data: { retry: '1' } }, 'Try again')));
      this.cb.onError(`Could not load ${node.addr}: ${err.message || err}`);
    });
  }

  skeleton() {
    return h('div', { class: 'skel', 'aria-hidden': 'true' }, [1, 2, 3, 4, 5, 6].map((i) => h('div', { class: 'skel-row', style: `--i:${i}` }, h('i'), h('i'))));
  }

  onTabKey(ev) {
    const tabs = [...this.tabs.querySelectorAll('[role=tab]')];
    const i = tabs.indexOf(document.activeElement);
    if (i < 0) return;
    let n = -1;
    if (ev.key === 'ArrowRight') n = (i + 1) % tabs.length;
    else if (ev.key === 'ArrowLeft') n = (i - 1 + tabs.length) % tabs.length;
    else if (ev.key === 'Home') n = 0;
    else if (ev.key === 'End') n = tabs.length - 1;
    if (n < 0) return;
    ev.preventDefault();
    tabs[n].focus();
    this.cb.onTab(tabs[n].dataset.tab);
    setTimeout(() => { const t = this.tabs.querySelector('[aria-selected=true]'); if (t) t.focus(); }, 0);
  }

  // ---- State ----

  // refsRow is the two lists of what a node reads and what reads it
  refsRow(d) {
    const model = this.model;
    const refs = (title, addrs) => h('section', { class: 'sec' }, h('h3', { class: 'h' }, title, h('span', { class: 'cnt' }, String(addrs.length))),
      addrs.length ? this.nodeList(addrs.map((a) => model.nodes.get(a) || { addr: a, name: a, type: '' }), model) : h('p', { class: 'dim none' }, 'none'));
    return h('div', { class: 'cols2' }, refs('Depends on', d.depends_on || []), refs('Used by', d.used_by || []));
  }

  // outputPane is the State tab of an output: what it is for, its value (or the lock that says it is withheld), and the
  // nodes it reads and the outputs that read it
  outputPane(d) {
    const wrap = h('div', { class: 'pane state-pane out-pane' });
    const attrs = d.attrs || [];
    const desc = attrs.find((a) => a.name === 'description');
    if (desc && desc.value) wrap.append(h('p', { class: 'out-desc' }, desc.value));
    wrap.append(this.state.render(Object.assign({}, d, { attrs: attrs.filter((a) => a.name !== 'description'), bare: true }), new Set()));
    wrap.append(this.refsRow(d));
    for (const note of d.notes || []) wrap.append(h('p', { class: 'note' }, icon('info'), h('span', null, note)));
    wrap.append(h('p', { class: 'note quiet' }, icon('lock'), h('span', null, 'A value marked sensitive is withheld by the server. This page receives its name, never its content.')));
    return wrap;
  }

  statePane(d, node) {
    if (isOut(node)) return this.outputPane(d);
    const wrap = h('div', { class: 'pane state-pane' });
    const changed = new Set((d.change && d.change.attrs || []).map((a) => a.path));
    wrap.append(this.state.render(d, changed));

    wrap.append(this.refsRow(d));
    const reach = d.reach && d.reach.length ? d.reach : null;
    wrap.append(h('section', { class: 'sec' }, h('h3', { class: 'h' }, 'Reach'),
      reach
        ? h('div', { class: 'reach' }, h('span', { class: 'big' }, String(reach.length)), h('span', { class: 'rt' }, `${reach.length === 1 ? 'resource is' : 'resources are'} reached by a change to this one.`), h('button', { class: 'btn', type: 'button', data: { reach: node.addr } }, 'Show in graph'))
        : h('p', { class: 'dim none' }, 'No resource depends on this one.')));

    const t = d.times || {};
    const rows = [];
    const add = (k, v) => { if (v) rows.push([k, v]); };
    add('revision', d.node.revision ? String(d.node.revision) : null);
    add('last read', timeRow(t.refreshed_at));
    add('applied', timeRow(t.applied_at));
    add('updated', timeRow(t.updated_at));
    if (t.drifted_at) add('drifted', timeRow(t.drifted_at));
    add('provider', [d.provider.key, d.provider.source, d.provider.version && 'v' + d.provider.version, d.provider.protocol && 'protocol ' + d.provider.protocol].filter(Boolean).join(' · '));
    if (d.node.source) add('declared at', h('span', { class: 'ref-file' }, `${d.node.source.file}:${d.node.source.line}`));
    wrap.append(h('section', { class: 'sec' }, h('h3', { class: 'h' }, ['ephemeral', 'call', 'builtin'].includes(node.mode) ? 'The declaration' : 'The record'), h('dl', { class: 'meta' }, rows.map(([k, v]) => [h('dt', null, k), h('dd', null, v)]))));
    if (d.lock) wrap.append(this.lockSection(d.lock));
    for (const note of d.notes || []) wrap.append(h('p', { class: 'note' }, icon('info'), h('span', null, note)));
    wrap.append(h('p', { class: 'note quiet' }, icon('lock'), h('span', null, 'A value marked sensitive is withheld by the server. This page receives its name and its type, never its content.')));
    return wrap;
  }

  lockSection(l) {
    return h('section', { class: 'sec' }, h('h3', { class: 'h' }, l.kind === 'reservation' ? 'Reserved' : 'Locked'),
      h('dl', { class: 'meta' },
        h('dt', null, 'held by'), h('dd', null, [l.user, l.host && '@' + l.host].filter(Boolean).join('') || l.actor || 'unknown'),
        l.command ? [h('dt', null, 'command'), h('dd', { class: 'mono-s' }, l.command)] : null,
        l.actor ? [h('dt', null, 'actor'), h('dd', null, l.actor)] : null,
        h('dt', null, 'since'), h('dd', null, relTime(l.since) + ' · ' + stamp(l.since)),
        l.expires ? [h('dt', null, 'expires'), h('dd', null, stamp(l.expires))] : null));
  }

  // a list of resources: the status square, the name, the namespace they sit in
  nodeList(nodes, model) {
    const LIMIT = 8;
    const ul = h('ul', { class: 'nav' });
    const draw = (all) => {
      ul.textContent = '';
      const shown = all ? nodes : nodes.slice(0, LIMIT);
      for (const n of shown) {
        const full = model.nodes.get(n.addr);
        const where = full && full.namespace ? full.namespace : '';
        ul.append(h('li', null, h('a', { class: 'navrow', href: '#', data: { goto: n.addr }, title: n.addr }, nodeMark(full || n, model.hasPlan && !!full), h('span', { class: 'nn' }, full ? full.name : n.addr), where ? h('span', { class: 'nt' }, where) : null)));
      }
      if (!all && nodes.length > LIMIT) ul.append(h('li', null, h('button', { class: 'more-btn', type: 'button', on: { click: () => draw(true) } }, `Show all ${nodes.length}`)));
    };
    draw(false);
    return ul;
  }

  // ---- Schema ----

  schemaPane(d) {
    const rows = [];
    const walk = (attrs, depth, prefix) => {
      for (const a of attrs) {
        rows.push({ a, depth, name: prefix + a.name });
        const kids = a.children || [];
        const t = a.type || '';
        if (kids.length && /^object/.test(t)) walk(kids, depth + 1, '');
        else if (kids.length && /^(list|set)\(object/.test(t) && kids[0].children) walk(kids[0].children, depth + 1, '');
        else if (kids.length && /^(list|set)\(/.test(t) && kids[0].children && kids[0].children.length && /^object/.test(kids[0].type || '')) walk(kids[0].children, depth + 1, '');
      }
    };
    // a node with a record reads its attributes from it; one without (an ephemeral resource, a variable, a resource not yet created) has the schema listed apart
    walk(d.schema && d.schema.length ? d.schema : d.attrs || [], 0, '');
    const input = h('input', { class: 'sfilter', type: 'search', placeholder: 'Filter attributes, types, descriptions', 'aria-label': 'Filter the attributes', autocomplete: 'off', spellcheck: 'false' });
    const count = h('span', { class: 'sf-count', 'aria-hidden': 'true' });
    const body = h('div', { class: 'schema', role: 'table', 'aria-label': 'Attributes' });
    const draw = () => {
      clear(body);
      const q = input.value.trim().toLowerCase();
      let n = 0;
      body.append(h('div', { class: 'srow head', role: 'row' }, h('div', { role: 'columnheader' }, 'Attribute'), h('div', { role: 'columnheader' }, 'Type'), h('div', { role: 'columnheader' }, 'Kind')));
      for (const r of rows) {
        const a = r.a;
        if (q && !(r.name.toLowerCase().includes(q) || (a.description || '').toLowerCase().includes(q) || (a.type || '').includes(q))) continue;
        n++;
        const flags = [];
        for (const k of (a.kind || '').split(',').filter(Boolean)) flags.push(h('span', { class: 'kd ' + k }, k));
        if (a.sensitive || a.redacted === 'sensitive') flags.push(h('span', { class: 'kd lock' }, icon('lock'), 'sensitive'));
        if (a.redacted === 'write-only') flags.push(h('span', { class: 'kd lock' }, icon('lock'), 'write-only'));
        if (a.deprecated) flags.push(h('span', { class: 'kd dep' }, 'deprecated'));
        body.append(h('div', { class: 'srow' + (a.deprecated ? ' deprecated' : ''), role: 'row', style: `--d:${r.depth}` },
          h('div', { class: 'sn', role: 'cell' }, h('code', null, r.name)),
          h('div', { class: 'st', role: 'cell' }, h('code', null, a.type || '')),
          h('div', { class: 'sflags', role: 'cell' }, flags),
          a.description ? h('div', { class: 'sd', role: 'cell' }, a.description) : null));
      }
      count.textContent = q ? `${n} of ${rows.length}` : '';
      if (!n) body.append(h('div', { class: 'blank' }, icon('search'), h('p', null, 'No attribute matches.')));
    };
    input.addEventListener('input', draw);
    draw();
    return h('div', { class: 'pane' },
      h('div', { class: 'stbar' }, h('label', { class: 'sfbox' }, icon('search'), input, count)),
      h('section', { class: 'sec flush' }, body),
      h('p', { class: 'note quiet' }, icon('info'), h('span', null, d.node && (isEnv(d.node) || isSecret(d.node)) ? 'Names, types and descriptions come from the declaration in the built-in package.' : 'Types, kinds and descriptions come from the provider schema. Descriptions are shown when the server has them.')));
  }

  // ---- History ----

  // historyDiff shows what a transaction altered in the resource's attributes, as it recorded it
  historyDiff(e) {
    const attrs = e.attrs || [];
    const note = e.attrs_sealed ? 'The values this change altered are sealed to the readers of the state. Start the viewer with --identity to read them.'
      : e.attrs_omitted ? 'This change altered too much to keep the values.' : null;
    if (!attrs.length) return note ? h('p', { class: 'tl-note' }, icon('lock'), h('span', null, note)) : null;
    const none = new Set();
    const n = attrs.length + (e.attrs_more || 0);
    return h('details', { class: 'tl-diff', open: attrs.length <= 4 },
      h('summary', null, `${n} ${n === 1 ? 'attribute' : 'attributes'} altered`),
      attrs.map((ch) => this.diffRow(ch, none)),
      e.attrs_more ? h('p', { class: 'tl-note' }, icon('info'), h('span', null, `${e.attrs_more} more not shown.`)) : null);
  }

  // planLink names the plan an apply ran from; it lays the plan over the graph while the state still stores it
  planLink(id) {
    if (!this.model.planById.has(id)) return h('span', { title: 'The plan is not stored any more' }, id);
    return h('button', { class: 'linkbtn', type: 'button', data: { plan: id }, 'aria-label': `Lay ${id} over the graph` }, id);
  }

  historyPane(d) {
    const list = d.history || [];
    if (!list.length) {
      const never = ['ephemeral', 'call', 'builtin'].includes(d.node && d.node.mode);
      return h('div', { class: 'pane' }, h('div', { class: 'blank' }, icon('stack'), h('p', null, never ? 'No transaction can write this.' : 'No transaction wrote this resource.'), never ? h('small', null, 'It is evaluated when a command runs and never recorded in the state, so it has no history.') : null));
    }
    const ol = h('ol', { class: 'timeline' });
    for (const e of list) {
      // a read that found the object changed outside pravo is a drift: it wrote the record to say so
      const a = actionOf(e.action) || (e.action === 'drift' ? { cls: 'a-update', label: 'drift' } : null);
      ol.append(h('li', { class: 'tl st-' + e.status },
        h('span', { class: 'tl-mark', 'aria-hidden': 'true' }),
        h('div', { class: 'tl-body' },
          h('div', { class: 'tl-top' },
            h('span', { class: 'b ' + statusClass(e.status) }, e.status),
            h('span', { class: 'kind' }, e.kind),
            a ? h('span', { class: 'act' }, h('span', { class: 'sq ' + a.cls }), a.label) : (e.action ? h('span', { class: 'dim' }, e.action) : null),
            h('time', { datetime: e.time, title: stamp(e.time) }, relTime(e.time))),
          e.command ? h('code', { class: 'cmd' }, e.command) : null,
          e.reason ? h('p', { class: 'reason' }, e.reason) : null,
          h('div', { class: 'tl-meta' },
            h('span', { title: e.id }, e.id),
            e.user || e.actor ? h('span', null, [e.user, e.actor].filter(Boolean).join(' · ')) : null,
            e.plan ? this.planLink(e.plan) : null,
            e.git_revision ? h('span', { title: 'Revision of the specification' }, 'git ' + e.git_revision.slice(0, 8)) : null,
            e.signature ? signatureMark(e.signature, e.signer) : null,
            e.rev_before || e.rev_after ? h('span', null, `rev ${e.rev_before || 0} → ${e.rev_after || 0}`) : null),
          this.historyDiff(e))));
    }
    return h('div', { class: 'pane' }, h('section', { class: 'sec' }, ol));
  }

  // ---- Change ----

  // openPlans lists the stored plans that can still be applied and change the resource; each lays itself over the graph
  openPlans(node) {
    const ids = node.plans || [];
    if (!ids.length) return null;
    const model = this.model;
    const cur = model.graph.plan && model.graph.plan.id;
    return h('section', { class: 'sec' },
      h('h3', { class: 'h' }, 'Open plans that change it', h('span', { class: 'cnt' }, String(ids.length))),
      h('ul', { class: 'plist' }, ids.map((id) => {
        const p = model.planById.get(id);
        const touch = p && (p.touches || []).find((t) => t.addr === node.addr);
        const a = touch && actionOf(touch.action);
        const stuck = !!(touch && touch.conflict);
        return h('li', null, h('button', { class: 'plrow' + (id === cur ? ' on' : ''), type: 'button', data: { plan: id }, 'aria-pressed': String(id === cur), 'aria-label': `${id}${p ? ' by ' + p.author : ''}${a ? ', ' + a.label : ''}${stuck ? ', conflicts with the state' : ''}` },
          avatar(p ? p.author : '?'), h('code', null, id), p ? h('span', { class: 'pa' }, p.author) : null,
          a ? h('span', { class: 'act' }, h('i', { class: 'sq ' + a.cls }), a.label) : null,
          h('span', { class: 'plst' }, stuck ? h('span', { class: 'b chg', 'data-tip': 'The state moved on under this resource, or under one that this change has to be applied with. The plan cannot apply it now.' }, 'conflict') : null, p ? statusChip(p.status) : null)));
      })),
      node.overlap ? h('p', { class: 'hint ovl' }, 'They overlap: applying one makes the others stale.') : null);
  }

  changePane(d, node) {
    const c = d.change;
    if (!c) {
      const pane = h('div', { class: 'pane' }, h('div', { class: 'blank' }, icon('info'), h('p', null, node.action === 'no-op' ? 'The plan ran this resource and found nothing to change.' : node.action ? 'The plan has no attribute changes for this resource.' : 'The loaded plan did not run this resource. It was not read.')));
      const open = this.openPlans(node);
      if (open) pane.append(open);
      return pane;
    }
    const a = actionOf(c.action);
    const wrap = h('div', { class: 'pane' });
    wrap.append(h('section', { class: 'sec' },
      h('div', { class: 'chg-head' }, h('span', { class: 'b ' + actClass(c.action) }, h('span', { class: 'sq ' + (a ? a.cls : '') }), a ? a.label : c.action),
        c.action === 'replace' ? h('span', { class: 'dim' }, 'destroyed and created again') : null,
        c.applied ? h('span', { class: 'b ok' }, icon('check'), 'applied') : null),
      (c.reasons || []).length ? h('ul', { class: 'reasons' }, c.reasons.map((r) => h('li', null, r))) : null,
      (c.requires_replace || []).length ? h('p', { class: 'forces' }, 'Forces replacement: ', c.requires_replace.map((p) => h('code', null, p))) : null));
    const rr = new Set(c.requires_replace || []);
    const diffs = h('section', { class: 'sec flush' });
    for (const ch of c.attrs || []) diffs.append(this.diffRow(ch, rr));
    if (!(c.attrs || []).length) diffs.append(h('div', { class: 'blank' }, icon('info'), h('p', null, 'No attribute is listed for this change.')));
    wrap.append(diffs);
    const open = this.openPlans(node);
    if (open) wrap.append(open);
    return wrap;
  }

  diffRow(ch, rr) {
    const val = (v, redacted, unknown) => redacted ? withheld('sensitive') : unknown ? unknownEl() : this.diffValue(v);
    const hasBefore = ch.before !== undefined || ch.before_redacted;
    const hasAfter = ch.after !== undefined || ch.after_redacted || ch.after_unknown;
    const lines = [];
    if (hasBefore) lines.push(h('div', { class: 'dl del' }, h('b', { 'aria-label': 'before' }, '−'), val(ch.before, ch.before_redacted, false)));
    if (hasAfter) lines.push(h('div', { class: 'dl add' }, h('b', { 'aria-label': 'after' }, '+'), val(ch.after, ch.after_redacted, ch.after_unknown)));
    const kind = hasBefore && hasAfter ? 'changed' : hasAfter ? 'added' : 'removed';
    return h('div', { class: 'diff ' + kind },
      h('div', { class: 'dp' }, h('code', null, ch.path), h('span', { class: 'dk' }, kind), rr.has(ch.path) ? h('span', { class: 'b bad' }, 'forces replacement') : null),
      lines);
  }

  diffValue(v) {
    if (v === undefined || v === null) return h('span', { class: 'v-nul' }, 'null');
    if (typeof v === 'string') return h('span', { class: 'str' }, JSON.stringify(v.length > 300 ? v.slice(0, 300) + '…' : v));
    let t;
    try { t = JSON.stringify(v); } catch { t = String(v); }
    return h('span', { class: 'str' }, t.length > 300 ? t.slice(0, 300) + '…' : t);
  }

  // ---- Source ----

  sourcePane(src, detail) {
    const wrap = h('div', { class: 'pane source' });
    const stateTime = detail && detail.times ? (detail.times.updated_at || detail.times.applied_at) : null;
    const blocks = src.blocks || [];
    if (!blocks.length) wrap.append(h('div', { class: 'blank' }, icon('file'), h('p', null, 'This node has no source in the specification.'), h('small', null, 'It is only in the state.')));
    if (src.generated > 1) wrap.append(h('div', { class: 'srcnote-top' }, icon('info'), h('span', null, `The same lines write ${src.generated} nodes: this one and ${src.generated - 1} more.`)));
    const marks = new Set();
    const sec = h('section', { class: 'sec srcsec' });
    blocks.forEach((b, i) => {
      for (const l of b.lines || []) if (l.mark) marks.add(l.mark);
      const newer = b.modified && stateTime && Date.parse(b.modified) > Date.parse(stateTime)
        ? `This file changed ${relTime(b.modified)}, after the state was written. The lines may differ from what created the resource.` : null;
      sec.append(h('h3', { class: 'h src-h' }, h('span', { class: 'step' }, String(i + 1)), b.role === 'definition' ? 'Where it is declared' : 'Where it is instantiated'));
      sec.append(codeWindow(b, { newer, announce: (t) => this.cb.announce(t) }));
    });
    if (blocks.length) sec.append(this.legend(marks));
    if (blocks.length) wrap.append(sec);
    const contrib = src.contributions || [];
    if (contrib.length) {
      wrap.append(h('section', { class: 'sec' }, h('h3', { class: 'h' }, 'What shapes it', h('span', { class: 'cnt' }, String(contrib.length))),
        h('ul', { class: 'contrib' }, contrib.map((c) => {
          const { head, tail } = c.kind === 'tag' || c.kind === 'policy' ? { head: '', tail: c.where } : splitPath(c.where, 1);
          return h('li', null,
            h('em', { class: 'kindpill k-' + c.kind }, kindLabel(c.kind)),
            h('code', { class: 'cwhere', title: c.where }, head ? h('span', { class: 'dir' }, head) : null, tail),
            c.note ? h('span', { class: 'cnote' }, c.note) : null);
        }))));
    }
    for (const note of src.notes || []) wrap.append(h('p', { class: 'note' }, icon('info'), h('span', null, note)));
    wrap.append(h('p', { class: 'note quiet' }, icon('lock'), h('span', null, 'Source comes only from files this evaluation loaded, including cached modules. A literal assigned to a sensitive attribute is masked before the line is sent.')));
    return wrap;
  }

  legend(marks) {
    const items = [];
    if (marks.has('focus')) items.push(h('span', { class: 'lg foc' }, 'the node itself'));
    if (marks.has('changed')) items.push(h('span', { class: 'lg chg' }, 'changed by the plan'));
    if (marks.has('sensitive')) items.push(h('span', { class: 'lg sens' }, 'sensitive, masked'));
    return items.length ? h('p', { class: 'cw-legend' }, items) : null;
  }
}

function actClass(a) {
  return a === 'create' ? 'ok' : a === 'delete' ? 'bad' : a === 'read' || a === 'move' || a === 'import' ? 'lock' : 'chg';
}
function statusClass(s) {
  return s === 'applied' ? 'ok' : s === 'failed' ? 'bad' : s === 'rejected' ? 'chg' : s === 'running' ? 'lock' : '';
}

// attrIndex maps the path of every attribute to what it shows, to tell afterwards which ones changed.
function attrIndex(d) {
  const m = new Map();
  const walk = (list, prefix) => {
    for (const a of list || []) {
      const path = prefix ? prefix + '.' + a.name : a.name;
      m.set(path, JSON.stringify([a.redacted || '', !!a.sensitive, !!a.unknown, !!a.null, a.value === undefined ? null : a.value]));
      walk(a.children, path);
    }
  };
  walk(d.attrs, '');
  return m;
}
