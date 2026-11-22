// The drawing board (README rule 14): new resources drawn into the Inventory graph, and changes to the ones that the
// specification has. The page holds the drawing; the server writes its CUE, says what CUE, the schemas and the policies
// say of it, and, when pravo serve runs with --edit, saves it (one new file, and the files a change is written in) and
// plans it. Nothing here stores a position: the layout engine places what is drawn, as it
// places everything else, and the code is the only thing that is kept.

import { h, clear, icon, debounce, plural, copyText } from './util.js';
import { codeWindow } from './ui.js';
import * as api from './api.js';

const CHECK_MS = 240;
const STEP_MS = 90; // the sweep crosses one column of the graph in this long
const KEEP = 'pravo.board.';

export class Board {
  // cb: { raw(): the graph the server sent, redraw(o), select(addr), selected(), toast(text, kind, ttl, detail), announce(text),
  //       inspector(), revision(), project() }
  constructor(cb) {
    this.cb = cb;
    this.cfg = null;
    this.on = false;
    this.items = [];
    this.forms = new Map();
    this.check = null;
    this.seq = 0;
    this.plan = null;
    this.saved = null;
    this.moved = null;
    // the start from a Terraform state: shown on a board whose state holds nothing
    this.empty = false;
    this.starting = false;
    this.startOpen = true;
    this.startFile = null;
    this.codeOpen = false;
    this.el = null;
    this.hits = [];
    this.hit = 0;
    // choosing is the hit of a type that several providers can draw, while the bar asks which; lastProv is the provider
    // last picked for each source
    this.choosing = null;
    this.provHit = 0;
    this.lastProv = {};
    this.typeSeq = 0;
    this.recheck = debounce(() => this.runCheck(), CHECK_MS);
    this.findTypes = debounce((q) => this.searchTypes(q), 110);
  }

  // ---- setup ----

  async load() {
    try { this.cfg = await api.board(); } catch { this.cfg = null; }
    return this.cfg;
  }

  get edit() { return !!(this.cfg && this.cfg.edit); }

  // mount builds the chrome of the board over the canvas: the strip with the bar, the lamps and the actions, the code drawer,
  // the title block and the sweep.
  mount(canvas) {
    this.canvas = canvas;
    this.input = h('input', { type: 'text', class: 'bd-in', autocomplete: 'off', spellcheck: 'false', role: 'combobox', 'aria-expanded': 'false', 'aria-controls': 'bd-hits', 'aria-autocomplete': 'list',
      'aria-label': 'Add a resource: type what it is', placeholder: 'Add a resource: type what it is, such as database, bucket or network' });
    this.hitsEl = h('div', { class: 'bd-hits', id: 'bd-hits', role: 'listbox', 'aria-label': 'Resource types', hidden: true });
    this.lampEls = { spec: lamp('Spec'), policy: lamp('Policy'), plan: lamp('Plan') };
    this.btn = {
      review: h('button', { type: 'button', class: 'bd-b sq', hidden: true, 'data-tip': 'Ask the guide what the drawing does and what could go wrong' }, icon('ask'), h('span', { class: 'lbl' }, 'Review')),
      code: h('button', { type: 'button', class: 'bd-b sq', 'aria-pressed': 'false', 'data-tip': 'Show the code of the drawing' }, icon('code'), h('span', { class: 'lbl' }, 'Code')),
      save: h('button', { type: 'button', class: 'bd-b', 'data-tip': 'Write the drawing as a new file of the specification' }, icon('file'), h('span', { class: 'lbl' }, 'Save')),
      plan: h('button', { type: 'button', class: 'bd-b go', 'data-tip': 'Plan the specification with the drawing in it, as pravo plan does' }, h('span', { class: 'bd-play', 'aria-hidden': 'true' }), h('span', { class: 'lbl' }, 'Plan')),
      apply: h('button', { type: 'button', class: 'bd-b apply', hidden: true }, icon('check'), h('span', { class: 'lbl' }, 'Apply')),
    };
    this.hint = h('p', { class: 'bd-hint' });
    this.tb = h('div', { class: 'bd-tb', role: 'group', 'aria-label': 'Title block of the drawing' });
    this.code = h('section', { class: 'bd-code', 'aria-label': 'The code of the drawing', hidden: true });
    this.sweep = h('div', { class: 'bd-sweep', 'aria-hidden': 'true' });
    const bar = h('div', { class: 'bd-bar' }, h('span', { class: 'bd-plus', 'aria-hidden': 'true' }, icon('plus')), this.input, this.hitsEl);
    this.strip = h('div', { class: 'bd-strip' }, bar,
      h('div', { class: 'bd-lamps', role: 'group', 'aria-label': 'Checks' }, this.lampEls.spec.el, this.lampEls.policy.el, this.lampEls.plan.el),
      h('div', { class: 'bd-acts' }, this.btn.review, this.btn.code, this.btn.save, this.btn.plan, this.btn.apply));
    this.guideEl = h('section', { class: 'bd-guide', 'aria-label': 'The guide', 'aria-live': 'polite', hidden: true });
    this.startEl = h('section', { class: 'bd-start', 'aria-labelledby': 'bd-start-h', hidden: true });
    this.el = h('div', { class: 'overlay board', id: 'board', hidden: true }, this.sweep, this.startEl, this.guideEl, this.code, this.tb, this.hint, this.strip);
    canvas.append(this.el);
    this.input.addEventListener('input', () => { this.choosing = null; this.findTypes(this.input.value); });
    this.input.addEventListener('keydown', (ev) => this.onBarKey(ev));
    // an empty bar lists every type as it takes the focus; over the start of an empty state it waits for a word
    this.input.addEventListener('focus', () => { if (this.input.value) this.findTypes(this.input.value); else if (!this.startShown()) this.searchTypes(''); });
    this.input.addEventListener('blur', () => setTimeout(() => { if (!this.hitsEl.contains(document.activeElement)) this.closeHits(); }, 120));
    this.hitsEl.addEventListener('mousedown', (ev) => ev.preventDefault());
    this.hitsEl.addEventListener('click', (ev) => {
      const b = ev.target.closest('[data-i],[data-p],[data-back]');
      if (!b) return;
      if (b.dataset.back) this.unchoose();
      else if (b.dataset.p) this.pick(this.choosing, this.choosing.providers[+b.dataset.p]);
      else if (b.dataset.ask) this.askFromBar();
      else this.pick(this.hits[+b.dataset.i]);
    });
    this.btn.code.addEventListener('click', () => this.toggleCode());
    this.btn.review.addEventListener('click', () => this.ask('review'));
    this.btn.save.addEventListener('click', () => this.save());
    this.btn.plan.addEventListener('click', () => this.runPlan());
    this.btn.apply.addEventListener('click', () => this.confirmApply());
    // the toasts of the page rise above the strip while the board is on, so that they never cover its buttons
    new ResizeObserver(() => { if (this.strip.offsetHeight) document.documentElement.style.setProperty('--bd-strip', this.strip.offsetHeight + 'px'); }).observe(this.strip);
    this.el.addEventListener('keydown', (ev) => { if (ev.key === 'Escape' && ev.target === this.input) { ev.stopPropagation(); this.closeHits(); this.input.blur(); this.cb.focusCanvas(); } });
  }

  // ---- on and off ----

  toggle(on = !this.on) {
    if (!this.cfg || on === this.on) { if (on && this.on) this.input.focus(); return; }
    this.on = on;
    this.el.hidden = !on;
    document.documentElement.classList.toggle('board-on', on);
    if (on && !this.items.length) this.restoring = this.restore();
    this.render();
    this.cb.redraw({ fit: true });
    if (on) { this.input.focus(); this.cb.announce('Drawing. Type what to add.'); if (this.items.length) this.recheck(); }
    else { this.closeHits(); this.cb.announce('Drawing hidden. What is drawn is kept.'); }
  }

  // ---- what the graph shows ----

  // decorate is the graph with the drawing in it: a node for each thing drawn, an edge for each wire, a dotted edge for
  // each wire that offers itself, and the actions of the last plan of the board.
  decorate(g) {
    if (!this.on || !g) return g;
    // another revision: the title block names it (an apply checks its plan against the state itself)
    if (g.revision !== this.rev) { this.rev = g.revision; queueMicrotask(() => this.render()); }
    // a state with no record yet can start from a Terraform state
    const empty = !(g.nodes || []).some((n) => (n.mode === 'managed' || n.mode === 'data') && n.presence !== 'spec');
    if (empty !== this.empty) { this.empty = empty; queueMicrotask(() => this.renderStart()); }
    const have = new Set((g.nodes || []).map((n) => n.addr));
    // a thing that was saved and that the graph now shows is the real node: the drawing lets go of it
    const before = this.items.length;
    this.items = this.items.filter((it) => !(it.saved && have.has(it.label)));
    if (this.items.length !== before) {
      this.keep();
      // the chrome follows once the graph is drawn: what is left is checked again, or the code drawer says what was saved
      queueMicrotask(() => { if (this.items.length) this.recheck(); else { this.check = null; this.plan = null; } this.render(); });
    }
    const changes = new Map(this.items.filter((it) => it.kind === 'change').map((it) => [it.addr, it]));
    const into = this.fold ? foldWord(this.fold) : '';
    const nodes = (g.nodes || []).map((n) => (this.inFold(n.addr) ? Object.assign({}, n, { boardEdit: 'fold', foldInto: into }) : this.changedNode(n, changes.get(n.addr))));
    let edges = (g.edges || []).slice();
    const sel = this.cb.selected();
    const acts = this.planActions();
    for (const it of this.items) {
      if (it.kind === 'change' || have.has(it.label)) continue;
      const missing = this.missing(it);
      nodes.push({
        addr: it.label, mode: 'managed', type: it.kind === 'template' ? (it.form ? it.form.title : it.template) : it.type, name: it.label, namespace: '',
        provider: this.providerKey(it), presence: 'spec', draft: true, template: it.kind === 'template', adopts: !!it.adopt, guided: !!it.guide, incomplete: missing.length,
        sockets: this.sockets(it), boardAction: acts ? acts.get(it.label) || '' : '',
      });
    }
    const shown = new Set(nodes.map((n) => n.addr));
    for (const c of changes.values()) edges = this.changedEdges(c, edges, shown, sel);
    for (const it of this.items) {
      if (it.kind === 'change' || have.has(it.label)) continue;
      // its edges are its wires, and what the last check found its code to read: a reference typed into an expression too
      const reads = new Set();
      for (const v of Object.values(it.values)) for (const w of wiresOf(v)) reads.add(w.to);
      for (const a of (this.check && this.check.reads && this.check.reads[it.label]) || []) reads.add(a);
      for (const a of reads) if (shown.has(a)) edges.push({ dependency: a, dependent: it.label, draft: true });
      this.offers(it, edges, sel);
    }
    const out = Object.assign({}, g, { nodes, edges });
    if (acts) out.nodes = nodes.map((n) => (n.draft || !acts.has(n.addr) ? n : Object.assign({}, n, { boardAction: acts.get(n.addr) })));
    return out;
  }

  // offers are the dotted edges of the wires a thing could take: the selected thing shows every wire it could take; another
  // shows the one that fits a socket it needs, when one alone does
  offers(it, edges, sel) {
    for (const f of this.fields(it)) {
      if (!f.wire || it.values[f.name]) continue;
      const cands = this.candidates(it, f);
      if (sel === it.label) for (const c of cands.slice(0, 4)) edges.push({ dependency: c.addr, dependent: it.label, offer: true });
      else if (f.required && cands.length === 1) edges.push({ dependency: cands[0].addr, dependent: it.label, offer: true });
    }
  }

  // changedNode is a node of the graph that the drawing changes: it says so, and carries the sockets of its form
  changedNode(n, c) {
    if (!c) return n;
    return Object.assign({}, n, { boardEdit: c.remove ? 'remove' : this.editOf(c) ? 'change' : 'open', guided: !!c.guide, sockets: c.remove ? [] : this.sockets(c), incomplete: c.remove ? 0 : this.missing(c).length });
  }

  // changedEdges are the edges of the graph with those of a changed resource as its new code has them: a wire taken off
  // goes once the check says the code does not read it, and a wire put on is drawn at once
  changedEdges(c, edges, shown, sel) {
    if (c.remove) return edges;
    const wires = new Set();
    for (const v of Object.values(c.values)) for (const w of wiresOf(v)) wires.add(w.to);
    const chk = this.check;
    const reads = chk && chk.reads && chk.state !== 'invalid' ? new Set((chk.reads[c.addr] || [])) : null;
    let out = edges;
    if (reads) out = edges.filter((e) => e.dependent !== c.addr || e.offer || reads.has(e.dependency) || wires.has(e.dependency));
    const has = new Set(out.filter((e) => e.dependent === c.addr).map((e) => e.dependency));
    for (const a of new Set([...wires, ...(reads || [])])) if (shown.has(a) && !has.has(a)) out.push({ dependency: a, dependent: c.addr, draft: true });
    this.offers(c, out, sel);
    return out;
  }

  planActions() {
    const p = this.plan;
    if (!p || p.state !== 'done' || p.sig !== this.sig()) return null;
    // a move is stamped on the record where it stands, as well as where it goes
    return new Map(p.result.changes.flatMap((c) => (c.from ? [[c.addr, c.action], [c.from, c.action]] : [[c.addr, c.action]])));
  }

  // ---- the things drawn ----

  fields(it) { return (it.form && it.form.fields) || []; }

  providerKey(it) {
    if (it.kind === 'template') return '';
    const p = (this.cfg.providers || []).find((x) => x.label === it.provider);
    return p ? p.local : '';
  }

  missing(it) { return this.fields(it).filter((f) => f.required && !it.values[f.name]).map((f) => f.name); }

  // sockets are the fields that take a wire, as the node draws them: required or not, wired or not
  sockets(it) {
    return this.fields(it).filter((f) => f.wire && (f.required || it.values[f.name] || it.shown.has(f.name))).slice(0, 6)
      .map((f) => ({ name: f.name, required: !!f.required, wired: !!it.values[f.name] }));
  }

  // candidates are what a socket can be wired to: the nodes of the graph and the things drawn of a type it reads, the
  // likeliest type first
  candidates(it, f) {
    const raw = this.cb.raw();
    const out = [];
    const seen = new Set();
    (f.wire.to || []).forEach((t, rank) => {
      for (const n of (raw && raw.nodes) || []) {
        if (n.type === t.type && n.mode === 'managed' && n.presence !== 'state' && n.addr !== it.label && !seen.has(n.addr)) { seen.add(n.addr); out.push({ addr: n.addr, type: n.type, read: t.read, rank }); }
      }
      for (const d of this.items) {
        if (d !== it && d.kind === 'resource' && d.type === t.type && !seen.has(d.label)) { seen.add(d.label); out.push({ addr: d.label, type: d.type, read: t.read, rank, draft: true }); }
      }
    });
    return out;
  }

  item(label) { return this.items.find((x) => x.label === label) || null; }

  // freeLabel is a name that nothing has: the one the form suggests, else with a number after it
  freeLabel(base) {
    const raw = this.cb.raw();
    const taken = new Set([...((raw && raw.nodes) || []).map((n) => n.addr.split('.')[0]), ...this.items.map((x) => x.label)]);
    let l = base || 'thing';
    for (let i = 2; taken.has(l); i++) l = `${base}_${i}`;
    return l;
  }

  async form(kind, type, provider) {
    const key = kind + '|' + type + '|' + (provider || '');
    let p = this.forms.get(key);
    if (!p) {
      p = api.boardType(kind, type, provider);
      this.forms.set(key, p);
      p.catch(() => this.forms.delete(key));
    }
    return p;
  }

  // add puts a new thing on the drawing and selects it. o.wire wires a socket of another thing to it.
  async add(hit, o = {}) {
    await this.restoring; // a drawing that the tab kept comes back first, so that a name is not taken twice
    let form;
    try { form = await this.form(hit.kind, hit.type, hit.provider); } catch (err) { this.cb.toast(`Could not read the form of ${hit.title || hit.type}: ${err.message}`, 'error'); return null; }
    const it = { label: this.freeLabel(labelOf(o.name) || form.label), kind: hit.kind, type: hit.kind === 'resource' ? hit.type : '', template: hit.kind === 'template' ? hit.type : '', provider: hit.provider || '', values: {}, form, shown: new Set(), cue: new Set(), adopt: o.adopt || null };
    for (const x of o.identity || []) {
      const f = (form.fields || []).find((ff) => ff.name === x.name);
      if (f && f.kind === 'text' && !f.sensitive && typeof x.value === 'string' && !x.value.includes('•')) it.values[f.name] = { text: x.value };
    }
    this.items.push(it);
    if (o.wire) {
      const from = this.item(o.wire.label);
      const f = from && this.fields(from).find((x) => x.name === o.wire.field);
      if (f) setWire(from, f, it.label, (f.wire.to.find((t) => t.type === it.type) || f.wire.to[0]).read);
    }
    this.changed({ graph: true });
    if (!o.quiet) { this.cb.select(it.label); this.cb.announce(`${it.label} drawn, a ${form.title}`); }
    return it;
  }

  // addFound draws an object that a discovery found: a resource of its type that names it in $import, so that the plan
  // imports it. a is what /api/adopt says of it, x its row in /api/discovery. Drawn twice, it is selected again.
  async addFound(a, x) {
    await this.restoring;
    const same = this.items.find((it) => it.adopt && it.adopt.discovery === a.discovery && it.adopt.key === a.key);
    if (same) { this.cb.select(same.label); return same; }
    return this.add({ kind: 'resource', type: a.type, provider: a.provider, title: a.type }, { adopt: { discovery: a.discovery, key: a.key, name: x.name || x.id }, name: x.name || x.id, identity: x.identity });
  }

  // ---- changing what the specification has ----

  changeOf(addr) { return this.items.find((x) => x.kind === 'change' && x.addr === addr) || null; }

  // change puts a resource of the specification on the drawing to be changed, or removed (o.remove), and selects it. The
  // server reads it as the pen can change it; one that a template, a loop or a condition makes is changed where that is written.
  async change(addr, o = {}) {
    await this.restoring;
    const had = this.changeOf(addr);
    if (had) {
      if (o.remove && !had.remove) { had.remove = true; this.changed({ graph: true }); }
      this.cb.select(addr);
      return had;
    }
    let e;
    try { e = await api.boardEdit(addr); } catch (err) { this.cb.toast(`Could not read ${addr}: ${err.message}`, 'error'); return null; }
    if (e.reason) { this.cb.toast(`${addr} is not changed on the board`, 'info', 9000, e.reason); return null; }
    const it = this.changeItem(e, !!o.remove);
    this.items.push(it);
    this.changed({ graph: true });
    this.cb.select(addr);
    this.cb.announce(o.remove ? `${addr} is to be removed. Nothing is written until you save.` : `Changing ${addr}. Nothing is written until you save.`);
    return it;
  }

  // changeItem is a resource as the drawing changes it: what was read (orig) and what it is now (values)
  changeItem(e, remove) {
    const it = { kind: 'change', label: e.addr, addr: e.addr, type: e.type, template: '', provider: e.provider || '', form: e.form, base: e.base, file: e.file, line: e.line,
      orig: e.values || {}, values: JSON.parse(JSON.stringify(e.values || {})), withheld: new Set(e.withheld || []), kept: e.kept || [], remove, shown: new Set(), cue: new Set() };
    cueFields(this, it); // a text written as CUE, an interpolation say, is shown as CUE
    return it;
  }

  // editOf is the change as the server reads it: the fields set and unset since it was read, or the resource removed. A
  // resource on the drawing that nothing has changed yet sends nothing.
  editOf(it) {
    if (it.remove) return { addr: it.addr, base: it.base, remove: true };
    const set = {}, unset = [];
    for (const [k, v] of Object.entries(it.values)) if (!sameValue(v, it.orig[k])) set[k] = v;
    for (const k of Object.keys(it.orig)) if (!it.values[k]) unset.push(k);
    if (!Object.keys(set).length && !unset.length) return null;
    return { addr: it.addr, base: it.base, set, unset: unset.length ? unset : undefined };
  }

  // stopChange takes a resource off the drawing: the change of it is dropped, and the resource stays as it is written
  stopChange(addr) {
    this.items = this.items.filter((x) => !(x.kind === 'change' && x.addr === addr));
    this.changed({ graph: true });
    this.cb.select(addr);
  }

  // count is what the drawing sends: the things drawn and the resources it changes
  count() { return this.items.filter((it) => (it.kind !== 'change' && !it.saved) || (it.kind === 'change' && this.editOf(it))).length + (this.fold && this.fold.addrs.length ? 1 : 0); }

  // nodeTool is what the inspector of a resource of the specification shows while the board is on: change it, or remove it
  nodeTool(n) {
    if (!this.on || !n || n.draft || n.mode !== 'managed' || n.presence === 'state' || this.changeOf(n.addr) || this.inFold(n.addr)) return null;
    const into = this.fold ? foldWord(this.fold) : '';
    return h('div', { class: 'bd-nt' }, h('span', { class: 'cl' }, 'Board'),
      h('button', { type: 'button', class: 'bd-b sm', 'data-tip': 'Change its fields on the board, written where it is written', on: { click: () => this.change(n.addr) } }, icon('pen'), h('span', { class: 'lbl' }, 'Change it')),
      // a fold takes what is written at the top of a file: a resource inside a struct or an instance is folded in the code
      n.addr.includes('.') ? null : h('button', { type: 'button', class: 'bd-b sm', 'data-tip': this.fold ? `Fold it into ${into} with the others` : 'Fold it, and what you add to it, into a template: the records stay, the code becomes a definition and an instance', on: { click: () => this.startFold(n.addr) } }, icon('template'), h('span', { class: 'lbl' }, this.fold ? `Add to ${into}` : 'Fold it')),
      h('button', { type: 'button', class: 'bd-b sm danger', 'data-tip': 'Take it out of the specification on the board: the plan destroys it', on: { click: () => this.change(n.addr, { remove: true }) } }, icon('x'), h('span', { class: 'lbl' }, 'Remove it')));
  }

  remove(label) {
    this.items = this.items.filter((x) => x.label !== label);
    for (const it of this.items) for (const [k, v] of Object.entries(it.values)) {
      const ws = wiresOf(v);
      if (!ws.some((w) => w.to === label)) continue;
      const left = ws.filter((w) => w.to !== label);
      if (!left.length) delete it.values[k]; else if (v.wires) it.values[k] = { wires: left };
    }
    this.changed({ graph: true });
    this.cb.select(null);
  }

  rename(it, to) {
    const raw = this.cb.raw();
    if (to === it.label) return '';
    if (!/^[a-z][a-z0-9_]{0,62}$/.test(to)) return 'A name is small letters, digits and underscores, and starts with a letter.';
    if (this.item(to) || ((raw && raw.nodes) || []).some((n) => n.addr === to || n.addr.startsWith(to + '.'))) return `${to} is taken.`;
    const from = it.label;
    it.label = to;
    for (const x of this.items) for (const v of Object.values(x.values)) for (const w of wiresOf(v)) if (w.to === from) w.to = to;
    this.changed({ graph: true });
    this.cb.select(to);
    return '';
  }

  // complete wires every socket that needs a wire and has none: to the one thing that fits it, else to a new thing of the
  // type it reads first, which arrives with sockets of its own. It goes three steps deep at most.
  async complete(label, depth = 0) {
    const it = this.item(label);
    if (!it || depth > 2) return 0;
    let n = 0;
    for (const f of this.fields(it)) {
      if (!f.required || !f.wire || it.values[f.name]) continue;
      const cands = this.candidates(it, f);
      if (cands.length === 1 || (cands.length > 1 && cands[0].rank < (cands[1] ? cands[1].rank : 9))) { setWire(it, f, cands[0].addr, cands[0].read); n++; continue; }
      if (cands.length) continue; // several fit: the person chooses
      const t = f.wire.to[0];
      const hit = { kind: 'resource', type: t.type, provider: it.provider };
      const made = await this.add(hit, { quiet: true, wire: { label: it.label, field: f.name } });
      if (made) { n++; n += await this.complete(made.label, depth + 1); }
    }
    if (n && depth === 0) { this.changed({ graph: true }); this.cb.announce(`${plural(n, 'wire')} made`); }
    return n;
  }

  // ---- changes ----

  changed(o = {}) {
    this.keep();
    if (o.graph) this.cb.redraw({});
    this.render();
    if (this.count()) this.recheck(); else { this.check = null; this.render(); }
  }

  // draft is the drawing as the server reads it
  draft() {
    const edits = this.items.filter((it) => it.kind === 'change').map((it) => this.editOf(it)).filter(Boolean);
    // a thing that was saved is in the files: the drawing lets go of it once the graph shows it
    const f = this.fold && this.fold.addrs.length ? this.fold : null;
    const fold = f ? { name: f.name, title: f.title || undefined, label: f.label || undefined, addrs: f.addrs, ask: f.ask.length ? f.ask : undefined } : undefined;
    return { file: this.cfg.file, edits: edits.length ? edits : undefined, fold, items: this.items.filter((it) => it.kind !== 'change' && !it.saved).map((it) => ({ label: it.label, type: it.type || undefined, provider: it.provider || undefined, template: it.template || undefined, values: it.values, adopt: it.adopt ? { discovery: it.adopt.discovery, key: it.adopt.key } : undefined })) };
  }

  sig() { const d = this.draft(); return JSON.stringify([d.items, d.edits || [], d.fold || null]); }

  async runCheck() {
    if (!this.count()) return;
    const seq = ++this.seq;
    let out;
    try { out = await api.boardPost('/api/board/check', this.draft()); } catch (err) {
      if (seq !== this.seq) return;
      this.check = { state: 'invalid', problems: [{ message: err.message }], code: '', lines: {}, creates: [] };
      this.render();
      return;
    }
    if (seq !== this.seq) return;
    const before = JSON.stringify((this.check && this.check.reads) || {});
    const fold = JSON.stringify((this.check && this.check.fold) || null);
    this.check = out;
    this.render();
    this.paintProblems();
    if (JSON.stringify(out.reads || {}) !== before) this.cb.redraw({});
    // the fold shows what the server made of it: its inputs, and the values it can ask for
    if (this.formItem && this.formItem.fold && JSON.stringify(out.fold || null) !== fold) this.cb.redrawInspector();
  }

  // ---- the bar ----

  async searchTypes(q) {
    const seq = ++this.typeSeq;
    let out;
    try { out = await api.boardTypes(q); } catch { return; }
    if (seq !== this.typeSeq || document.activeElement !== this.input) return;
    this.hits = out.hits || [];
    // a wish of several words that names no type goes to the guide first
    this.hit = !this.hits.length || (this.cfg.guide && q.trim().split(/\s+/).length > 3 && this.hits.length < 2) ? Math.min(this.hits.length, this.options() - 1) : 0;
    this.drawHits(q);
  }

  // options are the hits, and the guide when there is one and something is typed
  options() { return this.hits.length + (this.cfg.guide && this.input.value.trim() ? 1 : 0); }

  drawHits(q) {
    clear(this.hitsEl);
    this.hitsEl.setAttribute('aria-label', 'Resource types');
    const ask = !!(this.cfg.guide && q.trim());
    if (!this.hits.length && !ask) {
      this.hitsEl.append(h('p', { class: 'bd-none' }, q ? `No resource type of ${this.providerNames()} is called “${q}”.` : 'Type what to add.'));
    }
    this.hits.forEach((x, i) => {
      this.hitsEl.append(h('button', { type: 'button', class: 'bd-hit' + (i === this.hit ? ' on' : ''), role: 'option', 'aria-selected': i === this.hit ? 'true' : 'false', data: { i } },
        h('b', null, x.title), h('code', null, x.kind === 'template' ? 'template ' + x.type : x.type), this.hitChip(x),
        x.blurb ? h('span', { class: 'bd-blurb' }, x.blurb) : null));
    });
    if (ask) {
      const on = this.hit === this.hits.length;
      this.hitsEl.append(h('button', { type: 'button', class: 'bd-hit bd-ask' + (on ? ' on' : ''), role: 'option', 'aria-selected': on ? 'true' : 'false', data: { i: this.hits.length, ask: '1' } },
        h('b', null, icon('ask'), 'Ask the guide to draw it'), h('code', null, '\u21e7\u21b5'), h('span', { class: 'bd-pv t' }, this.cfg.guide), h('span', { class: 'bd-blurb' }, `\u201c${clipText(q.trim(), 140)}\u201d`)));
    }
    this.hitsEl.hidden = false;
    this.input.setAttribute('aria-expanded', 'true');
  }

  // hitChip names who draws a hit: its provider, how many providers can when there are several, or template
  hitChip(x) {
    if (x.kind === 'template') return h('span', { class: 'bd-pv t' }, 'template');
    const n = (x.providers || []).length;
    return h('span', { class: 'bd-pv', title: n > 1 ? x.providers.join(', ') : null }, n > 1 ? `${n} providers` : x.provider);
  }

  // chooseProvider turns the list of the bar into the providers that can draw the hit, the one last picked for its
  // source first in focus
  chooseProvider(hit) {
    this.choosing = hit;
    this.provHit = Math.max(0, hit.providers.indexOf(this.lastProv[this.sourceOf(hit.provider)]));
    this.drawProviders();
  }

  unchoose() {
    this.choosing = null;
    this.drawHits(this.input.value);
    this.input.focus();
  }

  sourceOf(label) {
    const p = (this.cfg.providers || []).find((x) => x.label === label);
    return p ? p.source : label;
  }

  drawProviders() {
    const hit = this.choosing;
    const last = this.lastProv[this.sourceOf(hit.provider)];
    clear(this.hitsEl);
    this.hitsEl.setAttribute('aria-label', `Providers of ${hit.type}`);
    this.hitsEl.append(h('div', { class: 'bd-ph' },
      h('button', { type: 'button', class: 'iconbtn sm', 'aria-label': 'Back to the types', 'data-tip': 'Back to the types (Esc)', data: { back: '1' } }, icon('back')),
      h('span', null, h('b', null, hit.title), ' ', h('code', null, hit.type), ' with which provider?')));
    hit.providers.forEach((label, i) => {
      const p = (this.cfg.providers || []).find((x) => x.label === label) || { label };
      const on = i === this.provHit;
      this.hitsEl.append(h('button', { type: 'button', class: 'bd-hit bd-prov' + (on ? ' on' : ''), role: 'option', 'aria-selected': on ? 'true' : 'false', data: { p: String(i) } },
        h('b', null, label), h('code', null, p.hint || ''), label === last ? h('span', { class: 'bd-pv' }, 'last used') : h('span')));
    });
    this.hitsEl.hidden = false;
    this.input.setAttribute('aria-expanded', 'true');
  }

  providerNames() {
    const ps = (this.cfg && this.cfg.providers) || [];
    return ps.length ? ps.map((p) => p.label).join(', ') : 'the providers';
  }

  closeHits() { this.choosing = null; this.hitsEl.hidden = true; this.input.setAttribute('aria-expanded', 'false'); }

  onBarKey(ev) {
    if (this.choosing) { this.onProviderKey(ev); return; }
    const n = this.options();
    if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
      ev.preventDefault();
      if (!n) return;
      this.hit = (this.hit + (ev.key === 'ArrowDown' ? 1 : n - 1)) % n;
      this.drawHits(this.input.value);
      const on = this.hitsEl.querySelector('.on');
      if (on) on.scrollIntoView({ block: 'nearest' });
    } else if (ev.key === 'Enter') {
      ev.preventDefault();
      if (ev.shiftKey || (this.hit === this.hits.length && n > this.hits.length)) this.askFromBar();
      else if (this.hits[this.hit]) this.pick(this.hits[this.hit]);
    }
  }

  onProviderKey(ev) {
    const ps = this.choosing.providers;
    if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
      ev.preventDefault();
      this.provHit = (this.provHit + (ev.key === 'ArrowDown' ? 1 : ps.length - 1)) % ps.length;
      this.drawProviders();
    } else if (ev.key === 'Enter') {
      ev.preventDefault();
      this.pick(this.choosing, ps[this.provHit]);
    } else if (ev.key === 'Escape' || (ev.key === 'Backspace' && !this.input.value)) {
      ev.preventDefault();
      ev.stopPropagation();
      this.unchoose();
    }
  }

  askFromBar() {
    const wish = this.input.value.trim();
    if (!wish || !this.cfg.guide) return;
    this.closeHits();
    this.input.value = '';
    this.ask('draw', { wish });
  }

  // pick draws the hit; a type that several providers can draw asks which first, unless provider names it
  async pick(hit, provider) {
    if (!provider && (hit.providers || []).length > 1) { this.chooseProvider(hit); return; }
    this.closeHits();
    this.input.value = '';
    if (provider) {
      this.lastProv[this.sourceOf(provider)] = provider;
      hit = { ...hit, provider };
    }
    const it = await this.add(hit);
    if (it) this.input.focus();
  }

  // ---- the guide ----

  // ask puts a question to the guide: draw (o.wish), review the drawing, or explain (o.addr). Its answer is a card on the
  // board; a drawing it proposes is on the board, marked, until the person keeps it or drops it.
  async ask(mode, o = {}) {
    if (!this.cfg.guide) return;
    if (this.guide && this.guide.state === 'running') this.guide.ctl.abort();
    if (this.guide && this.guide.state !== 'running' && this.items.some((it) => it.guide)) this.dropGuide(true);
    const g = { mode, wish: o.wish || '', addr: o.addr || '', state: 'running', reads: [], text: '', sent: null, made: [], ctl: new AbortController() };
    this.guide = g;
    this.renderGuide();
    this.cb.announce(mode === 'draw' ? 'Asked the guide to draw it' : mode === 'review' ? 'Asked the guide to review the drawing' : `Asked the guide about ${o.addr}`);
    const req = { mode, wish: g.wish || undefined, addr: g.addr || undefined, draft: this.draft() };
    try {
      await api.askGuide(req, (ev) => {
        if (this.guide !== g) return;
        if (ev.type === 'read') { g.reads.push(ev.tool); this.renderGuide(); }
        else if (ev.type === 'proposal') g.proposal = ev.proposal;
        else if (ev.type === 'text') g.text = ev.text;
        else if (ev.type === 'done') g.sent = ev.sent || [];
        else if (ev.type === 'error') { g.state = 'failed'; g.text = ev.text; }
      }, g.ctl.signal);
      if (this.guide !== g) return;
      if (g.state !== 'failed') {
        g.state = 'done';
        if (g.proposal) g.made = await this.applyProposal(g.proposal);
      }
    } catch (err) {
      if (this.guide !== g) return;
      g.state = err && err.name === 'AbortError' ? 'stopped' : 'failed';
      if (g.state === 'failed') g.text = err.message;
    }
    this.renderGuide();
    if (g.state === 'done') this.cb.announce(g.made.length ? `The guide drew ${plural(g.made.length, 'thing')}. Keep it or drop it.` : 'The guide answered');
  }

  // applyProposal lays what the guide drew on the board, marked as the guide's: new things, and changes of resources
  applyProposal(p) {
    return (async () => {
      const made = [];
      for (const x of p.items || []) {
        const kind = x.template ? 'template' : 'resource';
        if (this.item(x.label)) continue;
        let form;
        try { form = await this.form(kind, x.template || x.type, x.provider); } catch { continue; }
        const it = { label: x.label, kind, type: x.type || '', template: x.template || '', provider: x.provider || '', values: x.values || {}, form, shown: new Set(Object.keys(x.values || {})), cue: new Set(), adopt: null, guide: true };
        cueFields(this, it);
        this.items.push(it);
        made.push(it.label);
      }
      for (const e of p.edits || []) {
        if (this.changeOf(e.addr)) continue;
        let r;
        try { r = await api.boardEdit(e.addr); } catch { continue; }
        if (r.reason) continue;
        const it = this.changeItem(r, !!e.remove);
        for (const [k, v] of Object.entries(e.set || {})) { it.values[k] = v; it.shown.add(k); }
        for (const k of e.unset || []) delete it.values[k];
        cueFields(this, it);
        it.guide = true;
        this.items.push(it);
        made.push(it.addr);
      }
      if (made.length) { this.changed({ graph: true }); this.cb.select(made[0]); }
      return made;
    })();
  }

  keepGuide() {
    for (const it of this.items) delete it.guide;
    if (this.guide) this.guide.kept = true;
    this.changed({ graph: true });
    this.renderGuide();
    this.cb.redrawInspector();
    this.cb.announce('Kept what the guide drew');
  }

  dropGuide(quiet) {
    this.items = this.items.filter((it) => !it.guide);
    if (this.guide) this.guide.dropped = true;
    this.changed({ graph: true });
    if (quiet) return;
    this.renderGuide();
    this.cb.select(null);
    this.cb.announce('Dropped what the guide drew');
  }

  closeGuide() {
    if (this.guide && this.guide.state === 'running') this.guide.ctl.abort();
    if (this.items.some((it) => it.guide)) { this.dropGuide(true); }
    this.guide = null;
    this.renderGuide();
  }

  // renderGuide is the card of the guide: what was asked, what it read, its answer, and Keep and Drop for what it drew
  renderGuide() {
    const g = this.guide, el = this.guideEl;
    clear(el);
    el.hidden = !g;
    if (!g) return;
    const what = g.mode === 'draw' ? `\u201c${clipText(g.wish, 160)}\u201d` : g.mode === 'review' ? 'Review the drawing' : `Explain ${g.addr}`;
    el.append(h('div', { class: 'bd-gh' }, h('span', { class: 'bd-gk' }, icon('ask'), 'Guide'), h('span', { class: 'bd-gm' }, this.cfg.guide), h('span', { class: 'grow' }),
      h('button', { type: 'button', class: 'iconbtn', 'aria-label': g.state === 'running' ? 'Stop the guide' : 'Close the guide', on: { click: () => this.closeGuide() } }, icon('x'))));
    el.append(h('p', { class: 'bd-gq' }, what));
    if (g.reads.length) el.append(h('p', { class: 'bd-gr' }, h('span', null, 'read'), ...g.reads.slice(-8).map((t) => h('code', null, t)), g.reads.length > 8 ? h('span', null, `+${g.reads.length - 8}`) : null));
    if (g.state === 'running') { el.append(h('p', { class: 'bd-gw' }, h('span', { class: 'bd-dot' }), g.reads.length ? `Reading ${g.reads[g.reads.length - 1]}\u2026` : 'Asking\u2026')); return; }
    if (g.state === 'stopped') { el.append(h('p', { class: 'bd-gw' }, 'Stopped.')); return; }
    if (g.text) el.append(h('div', { class: 'bd-gt' + (g.state === 'failed' ? ' bad' : '') }, ...guideText(g.text)));
    const pending = this.items.filter((it) => it.guide);
    if (pending.length) {
      const adds = pending.filter((it) => it.kind !== 'change').length, chg = pending.length - adds;
      const said = [adds ? `draws ${plural(adds, 'thing')}` : '', chg ? `changes ${plural(chg, 'resource')}` : ''].filter(Boolean).join(' and ');
      el.append(h('div', { class: 'bd-gp' }, h('span', null, `It ${said}, marked guide on the board.`),
        h('span', { class: 'grow' }),
        h('button', { type: 'button', class: 'bd-b sm', 'data-tip': 'Take it off the board', on: { click: () => this.dropGuide() } }, icon('x'), h('span', { class: 'lbl' }, 'Drop')),
        h('button', { type: 'button', class: 'bd-b sm go', 'data-tip': 'Make it part of your drawing', on: { click: () => this.keepGuide() } }, icon('check'), h('span', { class: 'lbl' }, 'Keep'))));
    } else if (g.kept) el.append(h('p', { class: 'bd-gw' }, 'Kept: it is part of your drawing.'));
    else if (g.dropped) el.append(h('p', { class: 'bd-gw' }, 'Dropped.'));
    if (g.sent && g.sent.length) {
      el.append(h('details', { class: 'bd-gs' }, h('summary', null, `What was sent to ${this.cfg.guide}: ${plural(g.sent.length, 'read')}`),
        ...g.sent.map((x) => h('p', null, h('code', null, x.tool), ' ', clipText(typeof x.args === 'string' ? x.args : JSON.stringify(x.args || {}), 160), x.error ? h('span', { class: 'bad' }, ' \u2014 ' + x.error) : ` \u2014 ${x.bytes} bytes`))));
    }
  }

  // ---- the chrome ----

  render() {
    if (!this.el || !this.cfg) return;
    const chk = this.check;
    const n = this.count();
    // the lamps say a word or two; what they mean in full is their tip and their name to a screen reader
    const st = !n ? 'empty' : chk ? chk.state : 'checking';
    const probs = (chk && chk.problems) || [];
    if (st === 'checking') this.lampEls.spec.set('busy', 'Checking', '', 'Checking the drawing');
    else if (st === 'empty') this.lampEls.spec.set('off', 'Nothing drawn', '');
    else if (st === 'valid') this.lampEls.spec.set('ok', 'Loads', '', 'The drawing loads and checks with the specification');
    else if (st === 'incomplete') this.lampEls.spec.set('warn', `${probs.length} to fill`, probs.map(problemText).join('\n'), `${plural(probs.length, 'field')} to fill`);
    else if (st === 'exists') this.lampEls.spec.set('bad', 'File exists', firstProblem(chk), firstProblem(chk));
    else this.lampEls.spec.set('bad', plural(probs.length || 1, 'error'), probs.map(problemText).join('\n'), firstProblem(chk));
    const pol = chk && chk.policy;
    if (!n) this.lampEls.policy.set('off', 'Nothing yet', '');
    else if (!pol) this.lampEls.policy.set('off', 'Waits', 'The policies judge the drawing once it loads', 'Judged once the drawing loads');
    else if (pol.state === 'none') this.lampEls.policy.set('off', 'None', pol.note || 'No policy judges these resources', 'No policy');
    else if (pol.state === 'holds') this.lampEls.policy.set('ok', `${pol.policies.length} hold`, pol.policies.map((p) => p.name).join('\n'), `${plural(pol.policies.length, 'policy', 'policies')} hold`);
    else if (pol.state === 'refused') { const r = pol.policies.find((p) => p.outcome === 'refused'); this.lampEls.policy.set('bad', 'Refused', (r ? r.name + ': ' : '') + (r && r.issues ? r.issues.join('\n') : ''), `Refused by ${r ? r.name : 'a policy'}`); }
    else this.lampEls.policy.set('warn', 'Not yet', pol.note || 'A policy reads what only a plan knows: plan it', 'Cannot tell before a plan');
    const p = this.plan;
    if (!this.edit) this.lampEls.plan.set('off', 'Needs --edit', 'Start pravo serve --edit to plan from the board', 'Plan with pravo serve --edit');
    else if (!p) this.lampEls.plan.set('off', 'Not run', 'Plan runs pravo plan with the drawing in the specification', 'Not planned');
    else if (p.state === 'running') this.lampEls.plan.set('busy', 'Planning', '', 'Planning');
    else if (p.state === 'failed') this.lampEls.plan.set('bad', 'Failed', p.error || '', 'The plan failed');
    else if (p.sig !== this.sig()) this.lampEls.plan.set('off', 'Changed', 'The drawing changed since the plan: plan it again', 'Changed since the plan');
    else if (p.result.refused) this.lampEls.plan.set('bad', 'Refused', p.result.refused, 'A policy refuses the plan');
    else this.lampEls.plan.set('ok', planMarks(p.result), planWords(p.result), 'Planned: ' + planWords(p.result));
    // the actions
    this.btn.save.hidden = !this.edit;
    this.btn.plan.hidden = !this.edit;
    this.btn.review.hidden = !this.cfg.guide || !n;
    const pending = this.items.some((it) => it.guide);
    this.btn.save.disabled = !(n && chk && chk.state === 'valid') || pending;
    this.btn.save.dataset.tip = pending ? 'Keep or drop what the guide drew first' : chk && chk.files && chk.files.length ? `Write the drawing: ${[chk.code ? chk.file : '', ...chk.files.map((f) => f.file)].filter(Boolean).join(', ')}` : 'Write the drawing as a new file of the specification';
    this.btn.plan.disabled = !!(p && p.state === 'running') || (n > 0 && !(chk && chk.state === 'valid'));
    this.btn.code.setAttribute('aria-pressed', this.codeOpen ? 'true' : 'false');
    this.renderApply();
    this.hint.hidden = this.items.length > 0;
    this.hint.textContent = this.cfg.problem || (this.edit
      ? (this.cfg.apply
        ? 'Draw into the specification, or select a resource to change it. Nothing is written until you save, and nothing is applied until you plan the saved files and press Apply.'
        : 'Draw into the specification, or select a resource to change it. Nothing is written until you save, and nothing is applied: that stays a command.')
      : 'Draw into the specification, or select a resource to change it. Nothing is written: copy the code, or start pravo serve --edit to save and plan from here.');
    this.renderTitle();
    this.renderStart();
    if (this.codeOpen) this.renderCode();
    // the form of a change says whether there is one yet, without being drawn again under the cursor
    const it = this.formItem;
    if (it && it.kind === 'change' && this.formEl && this.formEl.isConnected) {
      const edit = this.editOf(it);
      if (this.changeTag) this.changeTag.textContent = changeWord(it, edit);
      if (this.dropLbl) this.dropLbl.textContent = edit ? 'Drop the change' : 'Leave it as it is';
    }
  }

  renderTitle() {
    const proj = this.cb.project() || {};
    const p = this.plan;
    const pol = this.check && this.check.policy;
    const checked = !pol ? '·' : pol.state === 'holds' ? plural(pol.policies.length, 'policy', 'policies') : pol.state === 'refused' ? 'refused' : pol.state === 'none' ? 'no policy' : 'not yet';
    const planned = p && p.state === 'done' ? `${clockOf(p.at)} · ${planWords(p.result)}` : p && p.state === 'running' ? 'planning' : '·';
    const rows = [['Project', proj.package || baseOf(proj.dir)], ['Revision', String(this.cb.revision() || '·').slice(0, 10)], ['Drawn by', this.count() ? this.cfg.actor || 'you' : '·'], ['Checked by', checked], ['Planned', planned], ['Applied by', this.applied ? `${this.cfg.actor || 'you'} · ${clockOf(this.applied.at)}` : '·']];
    clear(this.tb);
    for (const [k, v] of rows) this.tb.append(h('div', { class: 'bd-tc' + (k === 'Applied by' ? ' ap' : '') }, h('span', null, k), h('b', { title: k === 'Applied by' && !this.applied ? (this.cfg.apply ? 'Apply a plan of the files as they are from the board, or run pravo apply' : 'Apply is a command, run from a terminal: pravo apply') : v }, v)));
  }

  toggleCode(on = !this.codeOpen) {
    if (on === this.codeOpen) { if (on) this.renderCode(); return; }
    this.codeOpen = on;
    this.code.hidden = !on;
    this.btn.code.setAttribute('aria-pressed', on ? 'true' : 'false');
    if (on) this.renderCode();
    // the drawing is framed above the drawer, as it is above the strip
    this.cb.pad(on ? this.code.offsetHeight + 24 : 0);
  }

  // renderCode shows the new file, with the lines of the selected thing lit, and each file a change is written in, as
  // the lines it takes out and puts in
  renderCode() {
    clear(this.code);
    const chk = this.check;
    const sel = this.cb.selected();
    const files = (chk && chk.files) || [];
    const name = this.saved && !this.items.length ? this.saved.file : chk && chk.code ? chk.file : files.length ? plural(files.length, 'file') + ' changed' : (chk && chk.file) || this.cfg.file;
    const head = h('div', { class: 'bd-ch' }, h('b', null, name),
      h('span', { class: 'grow' }),
      chk && chk.code ? h('button', { type: 'button', class: 'bd-b sm', on: { click: () => copyText(chk.code).then((ok) => this.cb.announce(ok ? 'Copied the code' : 'Could not copy')) } }, icon('copy'), h('span', { class: 'lbl' }, 'Copy the code')) : null,
      h('button', { type: 'button', class: 'iconbtn', 'aria-label': 'Close the code', on: { click: () => this.toggleCode(false) } }, icon('x')));
    this.code.append(head);
    if (this.saved && (!chk || !this.items.length)) {
      this.code.append(h('div', { class: 'bd-next' }, h('p', null, this.cfg.apply ? `Wrote ${this.saved.file}. Plan it and press Apply, or run it from a terminal:` : `Wrote ${this.saved.file}. Apply it from a terminal, where your keys are:`),
        ...this.saved.commands.map((c) => h('div', { class: 'cmdline' }, h('span', { class: 'ps' }, '$'), h('code', null, c), h('button', { class: 'cpbtn', type: 'button', 'aria-label': 'Copy the command', on: { click: () => copyText(c) } }, icon('copy'))))));
    }
    if (!chk || (!chk.code && !files.length)) {
      if (!this.saved) this.code.append(h('p', { class: 'bd-empty' }, this.count() ? 'Writing the code…' : 'The code appears here as you draw.'));
      return;
    }
    // one window, or each with the name of its file over it
    const box = h('div', { class: 'bd-files' + (chk.code && files.length ? ' many' : files.length > 1 ? ' many' : '') });
    this.code.append(box);
    if (chk.code) {
      if (files.length) box.append(h('p', { class: 'bd-fn' }, h('b', null, chk.file), ' new'));
      const rng = chk.lines && chk.lines[sel];
      const lines = chk.code.replace(/\n$/, '').split('\n').map((text, i) => ({ no: i + 1, text, mark: rng && i + 1 >= rng[0] && i + 1 <= rng[1] ? 'focus' : '' }));
      box.append(codeWindow({ file: chk.file, start_line: 1, end_line: lines.length, lines, kind: 'file' }, { title: `The code of the drawing, ${chk.file}`, cls: 'bd-cw' }));
    }
    for (const fc of files) {
      const rng = fc.lines && fc.lines[sel];
      const lines = fc.diff.map((d) => (d.op === '\u2026' ? { no: '', text: '\u2026', dim: true }
        : { no: d.op === '-' ? '\u2212' : d.line, text: d.text || '', mark: d.op === '+' ? 'add' : d.op === '-' ? 'del' : rng && d.line >= rng[0] && d.line <= rng[1] ? 'focus' : '' }));
      const nums = fc.diff.filter((d) => d.op !== '-' && d.line).map((d) => d.line);
      const rm = Object.values(fc.lines || {}).filter((r) => !r[0]).length;
      if (box.classList.contains('many')) box.append(h('p', { class: 'bd-fn' }, h('b', null, fc.file), fc.new ? ' new' : rm ? ' changed, removes ' + plural(rm, 'resource') : ' changed'));
      box.append(codeWindow({ file: fc.file, start_line: nums[0] || 1, end_line: nums[nums.length - 1] || 1, lines, kind: 'file' }, { title: `The change of ${fc.file}`, cls: 'bd-cw bd-diff' }));
    }
    const focus = this.code.querySelector('.m-focus, .m-add, .m-del');
    if (focus) focus.scrollIntoView({ block: 'nearest' });
  }

  // ---- save and plan ----

  async save() {
    if (!this.edit || !this.check || this.check.state !== 'valid') return;
    this.btn.save.disabled = true;
    let out;
    // the files are written as the board showed their change, and no other way
    const seen = Object.fromEntries((this.check.files || []).map((f) => [f.file, f.base]));
    try { out = await api.boardPost('/api/board/save', Object.assign(this.draft(), { seen })); } catch (err) {
      this.cb.toast(`Could not save: ${err.message}`, 'error', 9000, err.check ? (err.check.problems || []).map(problemText).join(' · ') : '');
      this.render();
      return;
    }
    // what was changed is written: the drawing lets go of it at once; a thing drawn goes once the graph shows it
    this.items = this.items.filter((it) => it.kind !== 'change');
    for (const it of this.items) it.saved = true;
    this.fold = null;
    const files = [out.file, ...(out.changed || [])].filter(Boolean).join(', ');
    this.saved = { file: files, commands: out.commands, at: Date.now() };
    this.cb.toast(`Saved ${files}`, 'info', 6000, this.cfg.apply ? 'The graph shows it once the specification is read again. Plan it, then press Apply.' : 'The graph shows it once the specification is read again. Apply it from a terminal: ' + out.commands[out.commands.length - 1]);
    this.cb.announce(`Saved ${files}`);
    this.cb.redraw({});
    await this.load();
    this.keep();
    this.toggleCode(true);
    this.render();
  }

  async runPlan() {
    if (!this.edit || (this.plan && this.plan.state === 'running')) return;
    const sig = this.sig();
    this.plan = { state: 'running', sig, at: Date.now() };
    this.render();
    this.sweepRun(true);
    const seen = new Set();
    try {
      const out = await api.boardPlan(this.draft(), (line) => {
        if (line.step && line.step.addr && !seen.has(line.step.addr)) { seen.add(line.step.addr); this.cb.pulse(line.step.addr); }
      });
      if (out.error) throw new Error(out.error);
      this.plan = { state: 'done', sig, at: Date.now(), result: out.done };
      this.cb.announce('Planned: ' + planWords(out.done));
    } catch (err) {
      const probs = planProblems(err.message, new Set(this.items.map((it) => it.label)));
      this.plan = { state: 'failed', sig, at: Date.now(), error: err.message, problems: probs };
      const on = [...new Set(probs.map((x) => x.item))];
      this.cb.toast(on.length ? `The plan failed on ${on.join(', ')}` : 'The plan failed', 'error', 9000, on.length ? 'What the provider says is in the form of each.' : clipText(err.message, 300));
      if (on.length && !on.includes(this.cb.selected())) this.cb.select(on[0]);
    }
    this.sweepRun(false);
    this.render();
    this.cb.redraw({ stamp: true });
  }

  // ---- apply (pravo serve --apply) ----

  // renderApply says whether the last plan can be applied from here: one of the files as they are (nothing on the
  // board unsaved), that nothing refuses, and that the drawing has not changed since
  renderApply() {
    const b = this.btn.apply;
    b.hidden = !this.cfg.apply;
    if (b.hidden) return;
    const p = this.plan;
    let tip = '';
    if (this.applying) tip = 'Applying';
    else if (this.count()) tip = 'Save the drawing, then plan it: the board applies what the files say';
    else if (!p || p.state !== 'done') tip = 'Plan first: the board applies a plan that it showed you';
    else if (p.sig !== this.sig()) tip = 'The drawing changed since the plan: plan it again';
    else if (p.result.held) tip = p.result.held;
    else if (!p.result.ticket) tip = p.result.refused ? 'A policy refuses the plan' : 'The plan changes nothing';
    b.disabled = !!tip;
    b.dataset.tip = tip || `Apply the plan you see: ${planWords(p.result)}`;
  }

  // confirmApply asks once, beside the button, as pravo apply asks for yes: what the plan does, and that it is real
  confirmApply() {
    const p = this.plan;
    if (this.btn.apply.disabled || !p || !p.result || !p.result.ticket) return;
    this.closeMenu();
    const go = h('button', { type: 'button', class: 'bd-b sm apply' }, icon('check'), h('span', { class: 'lbl' }, 'Apply now'));
    const no = h('button', { type: 'button', class: 'bd-b sm' }, h('span', { class: 'lbl' }, 'Cancel'));
    const menu = h('div', { class: 'bd-menu bd-confirm', role: 'alertdialog', 'aria-label': 'Apply the plan' },
      h('p', { class: 'bd-mh' }, h('b', null, 'Apply the plan: '), planWords(p.result)),
      h('p', { class: 'bd-cw-note' }, onlyMoves(p.result) ? 'This moves records of the state to their new addresses, as pravo apply does: nothing that they made changes.' : 'This changes real infrastructure with your keys, as pravo apply does. Every change is checked against the plan you see.'),
      h('div', { class: 'bd-foot' }, no, go));
    document.body.append(menu);
    const r = this.btn.apply.getBoundingClientRect();
    const mw = Math.min(340, window.innerWidth - 16);
    menu.style.left = Math.max(8, Math.min(r.right - mw, window.innerWidth - mw - 8)) + 'px';
    menu.style.top = Math.max(8, r.top - 8 - menu.offsetHeight) + 'px';
    this.menu = menu;
    this.menuOff = (ev) => { if (!menu.contains(ev.target)) this.closeMenu(); };
    this.menuKey = (ev) => { if (ev.key === 'Escape') { ev.stopPropagation(); this.closeMenu(); this.btn.apply.focus(); } };
    setTimeout(() => { document.addEventListener('pointerdown', this.menuOff, true); document.addEventListener('keydown', this.menuKey, true); }, 0);
    no.addEventListener('click', () => { this.closeMenu(); this.btn.apply.focus(); });
    go.addEventListener('click', () => { this.closeMenu(); this.runApply(p.result.ticket); });
    no.focus();
  }

  async runApply(ticket) {
    if (this.applying) return;
    this.applying = true;
    this.render();
    this.sweepRun(true);
    const seen = new Set();
    try {
      const out = await api.boardApply(ticket, (line) => {
        if (line.step && line.step.addr && !seen.has(line.step.addr)) { seen.add(line.step.addr); this.cb.pulse(line.step.addr); }
      });
      if (out.error) throw new Error(out.error);
      const d = out.done;
      this.applied = { at: Date.now(), result: d };
      const words = `${d.added} added, ${d.changed} changed, ${d.destroyed} destroyed${d.moved ? `, ${d.moved} moved` : ''}`;
      this.cb.toast(`Applied: ${words}`, 'info', 9000, `txn ${d.txn}${d.deferred ? `. ${plural(d.deferred, 'change')} waited for what the apply made: plan again.` : ''}`);
      this.cb.announce(`Applied: ${words}`);
      // a record that moved is found at its new address when the graph is read again
      if (d.moved) this.moved = new Map(this.plan.result.changes.filter((c) => c.from).map((c) => [c.from, c.addr]));
      this.saved = null; // what was written is applied: the commands to apply it are done with
      this.plan = null; // the plan is used: the next is made of the state as the apply left it
    } catch (err) {
      this.cb.toast('The apply failed', 'error', 12000, clipText(err.message, 400));
      this.plan = null;
    }
    this.applying = false;
    this.sweepRun(false);
    this.render();
    this.cb.redraw({});
  }

  // sweepRun moves the line of the sweep across the drawing while the plan runs, and once more, in step with the stamps, when it ends
  sweepRun(on) {
    const el = this.sweep;
    el.classList.remove('run', 'pass');
    void el.offsetWidth;
    if (on) { el.classList.add('run'); return; }
    if (!this.plan || this.plan.state !== 'done') return;
    el.style.setProperty('--pass', `${Math.max(600, (this.cb.columns() + 1) * STEP_MS)}ms`);
    el.classList.add('pass');
  }

  // ---- the form, in the inspector ----

  // providerTag names the provider of a thing drawn; while the specification has other providers of its source, it is a
  // switch among them. A resource that adopts keeps the provider its discovery found it with.
  providerTag(it) {
    if (!it.provider) return null;
    const same = (this.cfg.providers || []).filter((p) => p.source === this.sourceOf(it.provider));
    if (it.kind !== 'resource' || it.adopt || it.saved || same.length < 2) return h('span', { class: 'b' }, it.provider);
    const sel = h('select', { class: 'bd-sel bd-psel', 'aria-label': 'Provider', 'data-tip': 'The provider that draws it' },
      ...same.map((p) => h('option', { value: p.label, selected: p.label === it.provider }, p.hint ? `${p.label} · ${p.hint}` : p.label)));
    sel.addEventListener('change', () => {
      it.provider = sel.value;
      this.lastProv[this.sourceOf(sel.value)] = sel.value;
      this.changed({ graph: true });
    });
    return sel;
  }

  // showForm draws the form of a thing drawn in the inspector
  showForm(label, insp) {
    if (this.inFold(label)) { this.showFold(label, insp); return; }
    const it = this.item(label);
    if (!it) return;
    if (it.kind === 'change') { this.showChange(it, insp); return; }
    const f = it.form || { fields: [], title: it.type };
    const nameIn = h('input', { type: 'text', class: 'bd-name', value: it.label, spellcheck: 'false', 'aria-label': 'Name' });
    const nameErr = h('p', { class: 'bd-err', role: 'alert' });
    nameIn.addEventListener('change', () => { const msg = this.rename(it, nameIn.value.trim()); nameErr.textContent = msg; if (msg) nameIn.value = it.label; });
    nameIn.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') nameIn.blur(); });
    const head = {
      kick: it.guide ? 'Drawn by the guide · keep it or drop it' : it.kind === 'template' ? 'Drawing · template' : it.adopt ? 'Drawing · resource that adopts' : 'Drawing · resource',
      title: nameIn,
      path: h('div', { class: 'ih-path bd-what' }, h('b', null, f.title), h('code', null, it.kind === 'template' ? it.template : it.type), this.providerTag(it)),
      tags: [h('span', { class: 'b bd-draft' }, 'draft')],
    };
    const body = h('div', { class: 'bd-form', data: { label: it.label } });
    this.formEl = body;
    this.formItem = it;
    if (f.doc) body.append(h('p', { class: 'bd-doc' }, f.doc));
    if (it.adopt) {
      body.append(h('div', { class: 'bd-adopt' },
        h('p', null, 'Adopts ', h('code', { title: it.adopt.key }, it.adopt.name || it.adopt.key), ', which the discovery ', h('b', null, it.adopt.discovery), ' found. The plan imports it and shows how it differs from what you write here.'),
        h('button', { type: 'button', class: 'linkbtn', on: { click: () => { it.adopt = null; this.changed({ graph: true }); this.cb.redrawInspector(); } } }, 'Draw a new one instead')));
    }
    body.append(nameErr, h('div', { class: 'bd-probs', role: 'status' }));
    const tools = h('div', { class: 'bd-tools' },
      h('button', { type: 'button', class: 'bd-b', 'data-act': 'complete', 'data-tip': 'Wire every socket it needs: to what fits, or to a new resource' }, icon('plus'), h('span', { class: 'lbl' }, 'Complete it')),
      h('button', { type: 'button', class: 'bd-b', 'data-act': 'code', 'data-tip': 'Show its lines in the code' }, icon('code'), h('span', { class: 'lbl' }, 'Its code')),
      this.cfg.guide ? h('button', { type: 'button', class: 'bd-b', 'data-act': 'explain', 'data-tip': 'Ask the guide what it is and what its fields do' }, icon('ask'), h('span', { class: 'lbl' }, 'Explain')) : null);
    body.append(tools);
    tools.addEventListener('click', (ev) => {
      const b = ev.target.closest('[data-act]');
      if (!b) return;
      if (b.dataset.act === 'complete') this.complete(it.label).then((n) => { if (!n) this.cb.announce('Nothing to wire: every socket it needs has a wire, or several things fit'); this.cb.redrawInspector(); });
      if (b.dataset.act === 'code') this.toggleCode(true);
      if (b.dataset.act === 'explain') this.ask('explain', { addr: it.label });
    });
    this.formFields(it, body);
    body.append(h('div', { class: 'bd-foot' }, h('button', { type: 'button', class: 'bd-b danger', on: { click: () => this.remove(it.label) } }, icon('x'), h('span', { class: 'lbl' }, 'Remove from the drawing'))));
    insp.showDraft(head, body);
    this.paintProblems();
  }

  // formFields are the fields of a form: what it needs, what is set, and a search over the rest
  formFields(it, body) {
    const f = it.form || { fields: [] };
    const req = f.fields.filter((x) => x.required);
    const has = (x) => it.values[x.name] || it.shown.has(x.name) || (it.withheld && it.withheld.has(x.name));
    const set = f.fields.filter((x) => !x.required && has(x));
    if (req.length) body.append(h('h3', { class: 'bd-sh' }, 'It needs'), ...req.map((x) => this.fieldRow(it, x)));
    const setBox = h('div', { class: 'bd-set' }, ...set.map((x) => this.fieldRow(it, x)));
    body.append(h('h3', { class: 'bd-sh' }, set.length ? 'Also set' : 'Set more'), setBox);
    // the rest, behind a search: a provider type has dozens of fields
    const rest = f.fields.filter((x) => !x.required);
    if (rest.length) {
      const q = h('input', { type: 'search', class: 'bd-q', placeholder: `Find one of ${plural(rest.length, 'field')}`, 'aria-label': 'Find a field to set', autocomplete: 'off', spellcheck: 'false' });
      const list = h('div', { class: 'bd-more' });
      const draw = () => {
        clear(list);
        const w = q.value.trim().toLowerCase();
        const pick = rest.filter((x) => !has(x) && (!w || x.name.includes(w) || (x.doc || '').toLowerCase().includes(w)));
        const show = w ? pick.slice(0, 12) : pick.filter((x) => x.wire).slice(0, 6);
        for (const x of show) list.append(h('button', { type: 'button', class: 'bd-mf', data: { f: x.name } }, h('code', null, x.name), x.wire ? h('span', { class: 'bd-sockm', title: 'takes a wire' }) : null, h('span', null, x.doc || x.type)));
        if (!show.length && w) list.append(h('p', { class: 'bd-none' }, 'No field is called that.'));
      };
      q.addEventListener('input', draw);
      list.addEventListener('click', (ev) => {
        const b = ev.target.closest('[data-f]');
        if (!b) return;
        const x = rest.find((y) => y.name === b.dataset.f);
        it.shown.add(x.name);
        setBox.append(this.fieldRow(it, x));
        q.value = '';
        draw();
        if (x.wire) this.changed({ graph: true });
        const inp = setBox.lastChild.querySelector('input, textarea, button');
        if (inp) inp.focus();
      });
      draw();
      body.append(q, list);
    }
  }

  // showChange draws the form of a resource of the specification that the drawing changes: its fields as written, and
  // what changing them makes of its file
  showChange(it, insp) {
    const f = it.form || { fields: [], title: it.type };
    const edit = this.editOf(it);
    const head = {
      kick: it.remove ? 'Drawing · removes a resource' : 'Drawing · changes a resource',
      title: it.addr,
      long: it.addr.length > 22,
      path: h('div', { class: 'ih-path bd-what' }, h('b', null, f.title || it.type), h('code', null, it.type), it.provider ? h('span', { class: 'b' }, it.provider) : null),
      copy: it.addr,
      tags: [this.changeTag = h('span', { class: 'b bd-draft' }, changeWord(it, edit))],
    };
    const body = h('div', { class: 'bd-form', data: { label: it.label } });
    this.formEl = body;
    this.formItem = it;
    body.append(h('p', { class: 'bd-where' }, 'Written in ', h('code', null, `${it.file}:${it.line}`), it.remove ? '. Saving takes it out of the file, with the comments above it; the plan destroys what it made.' : '. Saving writes the fields you change into that file, and leaves the rest of it as it is.'));
    body.append(h('div', { class: 'bd-probs', role: 'status' }));
    if (it.remove) {
      body.append(h('div', { class: 'bd-foot' }, h('button', { type: 'button', class: 'bd-b', on: { click: () => { it.remove = false; this.changed({ graph: true }); this.cb.redrawInspector(); } } }, icon('back'), h('span', { class: 'lbl' }, 'Keep it'))));
      insp.showDraft(head, body);
      this.paintProblems();
      return;
    }
    if (it.kept.length) body.append(h('p', { class: 'bd-kept' }, 'Also written, and left as it is: ', ...it.kept.flatMap((k, i) => [i ? ', ' : '', h('code', null, k)])));
    const tools = h('div', { class: 'bd-tools' },
      h('button', { type: 'button', class: 'bd-b', 'data-tip': 'Wire every socket it needs: to what fits, or to a new resource', on: { click: () => this.complete(it.label).then((n) => { if (!n) this.cb.announce('Nothing to wire: every socket it needs has a wire, or several things fit'); this.cb.redrawInspector(); }) } }, icon('plus'), h('span', { class: 'lbl' }, 'Complete it')),
      h('button', { type: 'button', class: 'bd-b', 'data-tip': 'Show the change of its file', on: { click: () => this.toggleCode(true) } }, icon('code'), h('span', { class: 'lbl' }, 'The change')),
      this.cfg.guide ? h('button', { type: 'button', class: 'bd-b', 'data-tip': 'Ask the guide what it is and what its fields do', on: { click: () => this.ask('explain', { addr: it.addr }) } }, icon('ask'), h('span', { class: 'lbl' }, 'Explain')) : null);
    body.append(tools);
    this.formFields(it, body);
    body.append(h('div', { class: 'bd-foot' },
      h('button', { type: 'button', class: 'bd-b', 'data-tip': 'Drop the change and take it off the drawing', on: { click: () => this.stopChange(it.addr) } }, icon('back'), this.dropLbl = h('span', { class: 'lbl' }, edit ? 'Drop the change' : 'Leave it as it is')),
      h('button', { type: 'button', class: 'bd-b danger', 'data-tip': 'Take it out of the specification: the plan destroys it', on: { click: () => { it.remove = true; this.changed({ graph: true }); this.cb.redrawInspector(); } } }, icon('x'), h('span', { class: 'lbl' }, 'Remove it'))));
    insp.showDraft(head, body);
    this.paintProblems();
  }

  // paintProblems puts what the check says of the selected thing in its form
  paintProblems() {
    const body = this.formEl, it = this.formItem;
    if (!body || !it || !body.isConnected) return;
    const chk = this.check;
    const mine = ((chk && chk.problems) || []).filter((p) => p.item === it.label || (it.fold && this.inFold(p.item)) || (!p.item && chk.state !== 'incomplete'));
    const pl = this.plan;
    if (pl && pl.state === 'failed' && pl.sig === this.sig()) mine.push(...pl.problems.filter((p) => p.item === it.label));
    const box = body.querySelector('.bd-probs');
    clear(box);
    const rows = new Set([...body.querySelectorAll('.bd-row')].map((r) => r.dataset.f));
    const said = new Set();
    for (const p of mine.filter((x) => !x.field || !rows.has(x.field))) {
      if (said.has(p.message)) continue; // a provider says the same of each field of a set
      said.add(p.message);
      box.append(h('p', { class: 'bd-err' }, p.message));
    }
    for (const row of body.querySelectorAll('.bd-row')) {
      const p = mine.find((x) => x.field === row.dataset.f);
      const err = row.querySelector('.bd-fe');
      err.textContent = p && p.message !== 'needs a value' ? p.message : '';
      const has = it.fold ? !!(this.fold && this.fold[row.dataset.f.slice(1)]) : !!it.values[row.dataset.f];
      row.classList.toggle('need', !has && row.classList.contains('req'));
    }
  }

  // fieldRow is one field of the form: its name, what it is, and the control that sets it
  fieldRow(it, f) {
    const row = h('div', { class: 'bd-row' + (f.required ? ' req' : ''), data: { f: f.name } });
    const lab = h('label', { class: 'bd-fl' }, h('code', null, f.name), f.wire ? h('span', { class: 'bd-sockm', title: 'takes a wire' }) : null, h('span', { class: 'bd-ty' }, f.type));
    row.append(lab);
    if (f.doc) row.append(h('p', { class: 'bd-fd' }, f.doc));
    const ctl = h('div', { class: 'bd-ctl' });
    row.append(ctl, h('p', { class: 'bd-fe', role: 'alert' }));
    const draw = () => {
      clear(ctl);
      const v = it.values[f.name];
      const ws = v ? wiresOf(v) : [];
      if (ws.length) {
        for (const w of ws) ctl.append(h('span', { class: 'bd-wire' }, h('span', { class: 'bd-sockm on' }), 'reads ', h('code', null, `${w.to}.${w.read}`),
          h('button', { type: 'button', class: 'iconbtn sm', 'aria-label': `Disconnect ${w.to}`, on: { click: () => { unwire(it, f, w.to); draw(); this.changed({ graph: true }); } } }, icon('x'))));
        if (f.wire && f.wire.many) ctl.append(h('button', { type: 'button', class: 'bd-b sm', on: { click: (ev) => this.socketMenu(it.label, f.name, ev.currentTarget) } }, icon('plus'), h('span', { class: 'lbl' }, 'Add a wire')));
        return;
      }
      if (f.wire) ctl.append(h('button', { type: 'button', class: 'bd-b sm', on: { click: (ev) => this.socketMenu(it.label, f.name, ev.currentTarget) } }, h('span', { class: 'bd-sockm' }), h('span', { class: 'lbl' }, 'Wire it')));
      if (f.sensitive || f.write_only) {
        const typed = it.withheld && it.withheld.has(f.name) && !v;
        ctl.append(h('p', { class: 'bd-sens' }, icon('lock'), typed ? 'A value is typed into the code here, and the board does not show it. It stays as it is, unless you wire the field or write CUE that reads a secret.' : 'Sensitive: wire it, or write CUE that reads a secret. The board writes no secret into the code.'));
        ctl.append(this.valueInput(it, f, 'expr', draw));
        return;
      }
      // a text or a number can be CUE instead: an interpolation, or a reference the form does not offer
      const typed = f.kind === 'text' || f.kind === 'number';
      const cueOn = typed && it.cue.has(f.name);
      ctl.append(this.valueInput(it, f, cueOn ? 'expr' : f.kind, draw));
      if (typed) {
        ctl.append(h('button', { type: 'button', class: 'bd-mode', 'aria-pressed': cueOn ? 'true' : 'false', 'aria-label': `Write ${f.name} as CUE`, 'data-tip': cueOn ? 'Back to a typed value' : 'Write CUE: a reference, or a string with \\(interpolation)',
          on: { click: () => { this.toggleCue(it, f); draw(); const el = ctl.querySelector('input, textarea'); if (el) el.focus(); } } }, 'CUE'));
      }
    };
    draw();
    return row;
  }

  // toggleCue turns a typed field into CUE and back, keeping what it can of the value: a text becomes its quoted
  // string, and a quoted string with nothing to evaluate in it becomes a text again
  toggleCue(it, f) {
    const v = it.values[f.name];
    if (it.cue.has(f.name)) {
      it.cue.delete(f.name);
      if (v && v.expr !== undefined) {
        let back = null;
        if (f.kind === 'number' && /^-?\d+(\.\d+)?$/.test(v.expr.trim())) back = { expr: v.expr.trim() };
        else if (f.kind === 'text') { try { const t = JSON.parse(v.expr); if (typeof t === 'string' && !/\\\(/.test(v.expr)) back = { text: t }; } catch { /* CUE that is not a plain string */ } }
        if (back) it.values[f.name] = back; else delete it.values[f.name];
      }
    } else {
      it.cue.add(f.name);
      if (v && v.text !== undefined) it.values[f.name] = { expr: JSON.stringify(v.text) };
    }
    this.changed({ graph: true });
  }

  // valueInput is the control of a value that is typed
  valueInput(it, f, kind, redraw) {
    const v = it.values[f.name];
    const commit = (val) => {
      const had = !!it.values[f.name];
      if (val === null) delete it.values[f.name]; else it.values[f.name] = val;
      this.changed({ graph: had !== !!it.values[f.name] });
    };
    if (kind === 'bool') {
      const cur = v && v.expr;
      const seg = h('div', { class: 'seg bd-seg', role: 'group', 'aria-label': f.name });
      for (const [t, val] of [['unset', null], ['true', 'true'], ['false', 'false']]) {
        seg.append(h('button', { type: 'button', 'aria-pressed': (cur || null) === val ? 'true' : 'false', on: { click: () => { commit(val ? { expr: val } : null); redraw(); } } }, t));
      }
      return seg;
    }
    if (kind === 'expr' || kind === 'list') {
      const list = kind === 'list';
      const text = !v ? '' : list ? listText(v.expr) : v.expr || '';
      const ta = h('textarea', { class: 'bd-ta', rows: list ? 3 : 2, spellcheck: 'false', 'aria-label': f.name, placeholder: list ? 'one per line' : f.kind === 'text' ? 'CUE, such as "app-\\(release.hex)"' : f.kind === 'number' ? 'CUE, such as listen.port + 1' : f.default ? `CUE, such as ${f.default}` : 'CUE, such as a struct or a list' });
      ta.value = text;
      ta.addEventListener('input', debounce(() => {
        const t = ta.value;
        if (!t.trim()) { commit(null); return; }
        commit({ expr: list ? JSON.stringify(t.split('\n').map((x) => x.trim()).filter(Boolean)) : t });
      }, 200));
      return ta;
    }
    const opts = f.options && f.options.length;
    if (opts) {
      const sel = h('select', { class: 'bd-sel', 'aria-label': f.name });
      sel.append(h('option', { value: '' }, 'unset'), ...f.options.map((o) => h('option', { value: o }, o)));
      sel.value = v ? v.expr || '' : '';
      sel.addEventListener('change', () => commit(sel.value ? { expr: sel.value } : null));
      return sel;
    }
    const inp = h('input', { type: 'text', class: 'bd-tin', spellcheck: 'false', 'aria-label': f.name, inputmode: kind === 'number' ? 'decimal' : 'text',
      placeholder: f.wire ? 'or type its value' : kind === 'number' ? 'a number' : f.default || '' });
    inp.value = !v ? '' : kind === 'number' ? v.expr || '' : v.text || '';
    inp.addEventListener('input', debounce(() => {
      const t = inp.value;
      if (!t) { commit(null); return; }
      commit(kind === 'number' ? { expr: t.trim() } : { text: t });
    }, 160));
    return inp;
  }

  // ---- starting from a Terraform state ----

  // renderStart shows the start on a board whose state holds nothing yet, and nothing is drawn: draw the first
  // resource, or bring a Terraform state, as pravo shadow does. Folded, it is one button in the corner.
  renderStart() {
    const el = this.startEl;
    if (!el) return;
    const show = !!(this.on && this.cfg && this.cfg.start && this.empty && (!this.items.length || this.starting));
    el.hidden = !show;
    // the hint says what the start says: one of them at a time
    this.hint.hidden = this.items.length > 0 || (show && this.startOpen);
    if (!show) return;
    el.classList.toggle('folded', !this.startOpen);
    el.classList.toggle('busy', this.starting);
    if (el.dataset.open === String(this.startOpen)) { this.startState(); return; }
    el.dataset.open = String(this.startOpen);
    clear(el);
    if (!this.startOpen) {
      el.append(h('button', { type: 'button', class: 'bd-b sm', 'data-tip': 'Bring what a Terraform state holds onto the board, as pravo shadow does', on: { click: () => { this.startOpen = true; this.renderStart(); this.startIn.focus(); } } }, icon('file'), h('span', { class: 'lbl' }, 'Bring a Terraform state')));
      return;
    }
    this.startIn = h('input', { type: 'text', class: 'bd-tin', placeholder: 'terraform.tfstate, or s3://bucket/key', spellcheck: 'false', autocomplete: 'off', 'aria-label': 'Where the Terraform state is' });
    this.startIn.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') this.runStart(); });
    this.startIn.addEventListener('input', () => { if (this.startFile) { this.startFile = null; this.startIn.classList.remove('chosen'); } });
    const pick = h('input', { type: 'file', accept: '.tfstate,.json,application/json', hidden: true });
    pick.addEventListener('change', () => {
      const f = pick.files && pick.files[0];
      if (!f) return;
      this.startFile = f;
      this.startIn.value = f.name;
      this.startIn.classList.add('chosen');
      this.startState();
    });
    this.startGo = h('button', { type: 'button', class: 'bd-b go', on: { click: () => this.runStart() } }, h('span', { class: 'bd-play', 'aria-hidden': 'true' }), h('span', { class: 'lbl' }, 'Bring it onto the board'));
    this.startSt = h('p', { class: 'bd-sst', role: 'status' });
    el.append(
      h('div', { class: 'bd-sk' }, h('span', null, 'Start'), h('button', { type: 'button', class: 'iconbtn sm', 'aria-label': 'Fold the start away', 'data-tip': 'Fold it away', on: { click: () => { this.startOpen = false; this.renderStart(); } } }, icon('minus'))),
      h('h2', { id: 'bd-start-h' }, 'The state holds nothing yet'),
      h('p', { class: 'bd-sp' }, 'Draw the first resource in the bar below, or bring what a Terraform state holds: every resource, written as code, with the record of its object.'),
      h('div', { class: 'bd-srow' }, this.startIn, h('button', { type: 'button', class: 'bd-b sm', on: { click: () => pick.click() } }, icon('file'), h('span', { class: 'lbl' }, 'Choose a file')), pick),
      h('div', { class: 'bd-srow' }, this.startGo),
      h('p', { class: 'bd-fd' }, 'As pravo shadow does: it writes a new file and the records of the objects, asks the provider about each, and plans it. Terraform keeps the objects until pravo shadow take-over, and nothing is applied. The providers of the state are declared here first.'),
      this.startSt);
    this.startState();
  }

  startShown() { return !!(this.on && this.cfg && this.cfg.start && this.empty && this.startOpen && !this.items.length); }

  startState() {
    if (!this.startGo) return;
    this.startGo.disabled = this.starting;
    this.startIn.disabled = this.starting;
    if (this.starting) this.startSt.textContent = 'Reading the state, and asking the provider about each resource. A large state takes a while.';
  }

  // runStart brings the state the person named, or chose, onto the board. The server refuses it once the state holds
  // anything.
  async runStart() {
    if (this.starting || !this.startIn) return;
    const place = this.startIn.value.trim();
    let body;
    if (this.startFile) {
      try { body = { state: await this.startFile.text() }; } catch { this.startSt.textContent = 'The file could not be read.'; return; }
    } else if (place) body = { place };
    else { this.startSt.textContent = 'Name the state: a path on this machine, an s3:// address, or a file you choose.'; this.startIn.focus(); return; }
    this.starting = true;
    this.renderStart();
    this.sweepRun(true);
    try {
      const out = await api.boardStart(body);
      if (out.error) throw new Error(out.error);
      const d = out.done;
      const said = d.verified ? 'the plan of it changes nothing' : `the plan of it wants ${plural((d.wants || []).length, 'change')}: ${(d.wants || []).slice(0, 4).join(', ')}`;
      const notes = (d.notes || []).length ? ` ${plural(d.notes.length, 'note')}: ${clipText(d.notes.join(' '), 300)}` : '';
      this.cb.toast(`Brought ${plural(d.resources, 'resource')} onto the board`, d.verified ? 'info' : 'warn', 14000, `Wrote ${d.file}; ${said}. Terraform keeps the objects until pravo shadow take-over.${notes}`);
      this.cb.announce(`Brought ${plural(d.resources, 'resource')} onto the board from ${d.from}: ${said}.`);
      this.startFile = null;
      this.startSt.textContent = '';
    } catch (err) {
      this.startSt.textContent = clipText(err.message, 600);
      this.cb.toast('The state was not brought onto the board', 'error', 12000, clipText(err.message, 400));
    }
    this.starting = false;
    this.sweepRun(false);
    this.renderStart();
  }

  // ---- folding into a template ----

  inFold(addr) { return !!(this.fold && this.fold.addrs.includes(addr)); }

  // movedTo is where the last apply moved a record to, once: a fold moves them under its instance
  movedTo(addr) {
    const to = this.moved && this.moved.get(addr);
    if (to) this.moved.delete(addr);
    return to || '';
  }

  // startFold begins a fold with a resource of the specification, or adds one to the fold that is open. A fold is one
  // change of the drawing: the server writes the template and its instance, and points what reads them at the instance.
  startFold(addr) {
    if (!this.fold) this.fold = { name: '', title: '', label: '', addrs: [], ask: [] };
    if (!this.fold.addrs.includes(addr)) this.fold.addrs.push(addr);
    this.changed({ graph: true });
    this.cb.select(addr);
    this.cb.redrawInspector();
    this.cb.announce(this.fold.addrs.length > 1 ? `${addr} is in the fold, with ${plural(this.fold.addrs.length - 1, 'other resource')}` : `Folding ${addr}. Select another resource and add it, then name the template.`);
  }

  // unfold takes a resource out of the fold; the last one out ends the fold
  unfold(addr) {
    if (!this.fold) return;
    this.fold.addrs = this.fold.addrs.filter((a) => a !== addr);
    this.fold.ask = this.fold.ask.filter((q) => !q.startsWith(addr + '.'));
    if (!this.fold.addrs.length) this.fold = null;
    this.changed({ graph: true });
    this.cb.select(addr);
  }

  dropFold() {
    const at = this.fold && this.fold.addrs[0];
    this.fold = null;
    this.changed({ graph: true });
    if (at) this.cb.select(at);
    this.cb.announce('The fold is dropped. Nothing was written.');
  }

  // showFold draws the fold in the inspector: its name, what it folds, the inputs the template gets, and the values it
  // can ask for
  showFold(addr, insp) {
    const f = this.fold;
    // drawn again when the check comes back: the control that had the focus keeps it
    const had = document.activeElement && document.activeElement.closest('.bd-fold') ? document.activeElement.dataset.k : '';
    const out = this.check && this.check.fold;
    const head = {
      kick: 'Drawing · folds into a template',
      title: out ? out.def : f.name ? foldWord(f) : 'A new template',
      path: h('div', { class: 'ih-path bd-what' }, h('b', null, plural(f.addrs.length, 'resource')), h('code', null, out ? `${out.file} · ${out.label}` : 'pravo.#Template')),
      tags: [h('span', { class: 'b bd-draft' }, 'fold')],
    };
    const body = h('div', { class: 'bd-form bd-fold', data: { label: '@fold' } });
    this.formEl = body;
    this.formItem = { label: '@fold', values: {}, fold: true };
    body.append(h('p', { class: 'bd-where' }, 'Saving writes a template that holds these resources into a new file, with one instance of it that keeps their records ($from). The plan moves the records and changes nothing they made.'));
    body.append(h('div', { class: 'bd-probs', role: 'status' }));
    const text = (key, label, ph, tip) => {
      const inp = h('input', { type: 'text', class: 'bd-tin', value: f[key] || '', placeholder: ph, spellcheck: 'false', autocomplete: 'off', 'aria-label': label, data: { k: '@' + key } });
      inp.addEventListener('change', () => { f[key] = inp.value.trim(); this.changed({ graph: true }); });
      inp.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') inp.blur(); });
      return h('div', { class: 'bd-row' + (key === 'name' ? ' req' : ''), data: { f: '@' + key } }, h('label', { class: 'bd-fl' }, h('code', null, label)), h('p', { class: 'bd-fd' }, tip), h('div', { class: 'bd-ctl' }, inp), h('p', { class: 'bd-fe', role: 'alert' }));
    };
    body.append(h('h3', { class: 'bd-sh' }, 'It needs'), text('name', 'name', 'web_app', 'The name of the template, in small letters, digits and underscores. The definition and the file take it.'));
    body.append(h('h3', { class: 'bd-sh' }, 'Also set'),
      text('title', 'title', f.name ? foldTitle(f.name) : 'Web app', 'What the catalog of templates calls it.'),
      text('label', 'instance', f.name || 'web_app', `The address of the instance. The resources move under it: ${f.label || f.name || 'web_app'}.${f.addrs[0]}.`));
    body.append(h('h3', { class: 'bd-sh' }, 'It folds'), h('ul', { class: 'bd-flist' }, ...f.addrs.map((a) => h('li', { class: a === addr ? 'on' : '' },
      h('button', { type: 'button', class: 'linkbtn', on: { click: () => this.cb.select(a) } }, h('code', null, a)),
      out && out.moves[a] ? h('span', { class: 'bd-ty' }, 'to ' + out.moves[a]) : null,
      h('button', { type: 'button', class: 'iconbtn sm', 'aria-label': `Take ${a} out of the fold`, 'data-tip': 'Take it out of the fold', on: { click: () => this.unfold(a) } }, icon('x'))))),
    h('p', { class: 'bd-fd' }, 'To fold another resource, select it and press Add.'));
    if (out) {
      const reads = out.inputs.filter((x) => !x.asked);
      body.append(h('h3', { class: 'bd-sh' }, 'Inputs'));
      if (!reads.length && !out.inputs.length) body.append(h('p', { class: 'bd-fd' }, 'It reads nothing of other resources, so it asks for nothing yet. Tick a value below to ask for it.'));
      for (const x of out.inputs) body.append(h('p', { class: 'bd-inp' }, h('code', null, x.name), h('span', { class: 'bd-ty' }, x.asked ? `asks for ${x.from}, as written by default` : `reads ${x.from}`)));
      const can = Object.entries(out.fields || {}).flatMap(([a, fs]) => fs.map((k) => `${a}.${k}`));
      if (can.length) {
        body.append(h('h3', { class: 'bd-sh' }, 'Ask for'), h('div', { class: 'bd-asks' }, ...can.map((q) => {
          const box = h('input', { type: 'checkbox', checked: f.ask.includes(q), data: { k: q } });
          box.addEventListener('change', () => { f.ask = box.checked ? [...f.ask, q] : f.ask.filter((x) => x !== q); this.changed({ graph: true }); });
          return h('label', { class: 'bd-askf' }, box, h('code', null, q));
        })));
      }
    }
    body.append(h('div', { class: 'bd-tools' }, h('button', { type: 'button', class: 'bd-b', 'data-tip': 'Show the template and the changes of the files', on: { click: () => this.toggleCode(true) } }, icon('code'), h('span', { class: 'lbl' }, 'The change'))));
    body.append(h('div', { class: 'bd-foot' }, h('button', { type: 'button', class: 'bd-b', 'data-tip': 'Drop the fold: the resources stay as they are written', on: { click: () => this.dropFold() } }, icon('back'), h('span', { class: 'lbl' }, 'Drop the fold'))));
    insp.showDraft(head, body);
    this.paintProblems();
    if (had) { const el = [...body.querySelectorAll('[data-k]')].find((x) => x.dataset.k === had); if (el) el.focus(); }
  }

  // ---- sockets ----

  // socketMenu offers what a socket can be wired to: what fits on the drawing, and a new resource of each type it reads
  socketMenu(label, field, anchor, at) {
    this.closeMenu();
    const it = this.item(label);
    const f = it && this.fields(it).find((x) => x.name === field);
    if (!f || !f.wire) return;
    const cands = this.candidates(it, f);
    const cur = wiresOf(it.values[f.name] || {});
    const menu = h('div', { class: 'bd-menu', role: 'menu', 'aria-label': `Wire ${label}.${field}` });
    menu.append(h('p', { class: 'bd-mh' }, h('code', null, `${label}.${field}`), f.required ? ' needs one' : ''));
    for (const c of cands.slice(0, 12)) {
      const on = cur.some((w) => w.to === c.addr);
      menu.append(h('button', { type: 'button', role: 'menuitem', class: 'bd-mi' + (on ? ' on' : ''), on: { click: () => { if (on) unwire(it, f, c.addr); else setWire(it, f, c.addr, c.read); this.closeMenu(); this.changed({ graph: true }); this.cb.redrawInspector(); } } },
        h('span', { class: 'bd-sockm' + (on ? ' on' : '') }), h('b', null, c.addr), h('code', null, `.${c.read}`), h('span', { class: 'bd-pv' + (c.draft ? ' d' : '') }, c.draft ? 'drawn' : c.type)));
    }
    if (!cands.length) menu.append(h('p', { class: 'bd-none' }, 'Nothing on the drawing fits.'));
    for (const t of f.wire.to.slice(0, 2)) {
      menu.append(h('button', { type: 'button', role: 'menuitem', class: 'bd-mi new', on: { click: async () => { this.closeMenu(); const made = await this.add({ kind: 'resource', type: t.type, provider: it.provider }, { wire: { label, field } }); if (made) this.cb.announce(`${made.label} drawn and wired to ${label}.${field}`); } } },
        icon('plus'), h('span', null, 'Draw a new ', h('code', null, t.type))));
    }
    if (cur.length) menu.append(h('button', { type: 'button', role: 'menuitem', class: 'bd-mi', on: { click: () => { delete it.values[f.name]; this.closeMenu(); this.changed({ graph: true }); this.cb.redrawInspector(); } } }, icon('x'), h('span', null, 'Disconnect')));
    document.body.append(menu);
    const r = anchor ? anchor.getBoundingClientRect() : { left: at.x, bottom: at.y, top: at.y };
    const mw = Math.min(340, window.innerWidth - 16);
    menu.style.left = Math.max(8, Math.min(r.left, window.innerWidth - mw - 8)) + 'px';
    const below = r.bottom + 6;
    menu.style.top = (below + 280 > window.innerHeight ? Math.max(8, r.top - 6 - Math.min(menu.offsetHeight, 320)) : below) + 'px';
    const first = menu.querySelector('button');
    if (first) first.focus();
    this.menu = menu;
    this.menuOff = (ev) => { if (!menu.contains(ev.target)) this.closeMenu(); };
    this.menuKey = (ev) => {
      if (ev.key === 'Escape') { ev.stopPropagation(); this.closeMenu(); if (anchor) anchor.focus(); }
      const bs = [...menu.querySelectorAll('button')];
      const i = bs.indexOf(document.activeElement);
      if (ev.key === 'ArrowDown') { ev.preventDefault(); bs[(i + 1) % bs.length].focus(); }
      if (ev.key === 'ArrowUp') { ev.preventDefault(); bs[(i - 1 + bs.length) % bs.length].focus(); }
    };
    setTimeout(() => { document.addEventListener('pointerdown', this.menuOff, true); document.addEventListener('keydown', this.menuKey, true); }, 0);
  }

  closeMenu() {
    if (!this.menu) return;
    this.menu.remove();
    this.menu = null;
    document.removeEventListener('pointerdown', this.menuOff, true);
    document.removeEventListener('keydown', this.menuKey, true);
  }

  // ---- keeping the drawing in the tab ----

  // keep writes the drawing to the storage of the tab, so that a reload does not lose it. It is the page's, never the server's.
  keep() {
    try {
      const key = KEEP + ((this.cb.project() || {}).dir || '');
      const mine = this.items.filter((it) => !it.guide);
      if (!mine.length && !this.fold) { sessionStorage.removeItem(key); return; }
      const fold = this.fold ? [{ kind: 'fold', fold: this.fold }] : [];
      sessionStorage.setItem(key, JSON.stringify(fold.concat(mine.map((it) => (it.kind === 'change' ? { kind: 'change', label: it.label, addr: it.addr, base: it.base, values: it.values, remove: it.remove, shown: [...it.shown], cue: [...it.cue] } : { label: it.label, kind: it.kind, type: it.type, template: it.template, provider: it.provider, values: it.values, shown: [...it.shown], cue: [...it.cue], saved: !!it.saved, adopt: it.adopt || undefined })))));
    } catch { /* a private window */ }
  }

  async restore() {
    let list;
    try { list = JSON.parse(sessionStorage.getItem(KEEP + ((this.cb.project() || {}).dir || '')) || '[]'); } catch { list = []; }
    if (!Array.isArray(list) || !list.length) return;
    let gone = 0;
    for (const x of list.slice(0, 96)) {
      try {
        if (x.kind === 'fold') {
          const f = x.fold || {};
          const str = (v) => (typeof v === 'string' ? v.slice(0, 80) : '');
          const list = (v) => (Array.isArray(v) ? v.filter((a) => typeof a === 'string').slice(0, 24) : []);
          if (list(f.addrs).length) this.fold = { name: str(f.name), title: str(f.title), label: str(f.label), addrs: list(f.addrs), ask: list(f.ask) };
          continue;
        }
        if (x.kind === 'change') {
          // a change is made against the file as it was read: one whose file changed since is let go
          const e = await api.boardEdit(x.addr);
          if (this.item(x.addr)) continue;
          if (e.reason || e.base !== x.base) { gone++; continue; }
          const it = this.changeItem(e, !!x.remove);
          Object.assign(it, { values: x.values || {}, shown: new Set(x.shown || []), cue: new Set(x.cue || []) });
          this.items.push(it);
          continue;
        }
        const form = await this.form(x.kind, x.kind === 'template' ? x.template : x.type, x.provider);
        if (this.item(x.label)) continue; // drawn again while the drawing came back
        const adopt = x.adopt && typeof x.adopt.discovery === 'string' && typeof x.adopt.key === 'string' ? { discovery: x.adopt.discovery, key: x.adopt.key, name: String(x.adopt.name || '') } : null;
        this.items.push({ label: x.label, kind: x.kind, type: x.type || '', template: x.template || '', provider: x.provider || '', values: x.values || {}, shown: new Set(x.shown || []), cue: new Set(x.cue || []), saved: !!x.saved, adopt, form });
      } catch { /* a type that the providers do not have any more */ }
    }
    if (gone) this.cb.toast(`${plural(gone, 'change')} let go`, 'info', 8000, 'Its file changed since it was read. Change it again on the board.');
    if (this.items.length || this.fold) { this.changed({ graph: true }); this.cb.redrawInspector(); }
  }
}

// ---- helpers ----

// foldWord is the name of a fold as the graph and the buttons say it: the instance, else the template
function foldWord(f) { return f.label || f.name || 'a template'; }

// foldTitle is a title from a name: web_app is Web app
function foldTitle(name) { return name.charAt(0).toUpperCase() + name.slice(1).replace(/_/g, ' '); }

function lamp(name) {
  const dot = h('i', { class: 'bd-dot', 'aria-hidden': 'true' });
  const txt = h('span', { class: 'bd-lt' });
  const el = h('div', { class: 'bd-lamp off', tabIndex: 0, role: 'status', 'aria-label': name }, dot, h('span', { class: 'bd-ln' }, name), txt);
  return {
    el,
    set(state, words, tip, long) {
      el.className = 'bd-lamp ' + state;
      txt.textContent = words;
      el.setAttribute('aria-label', `${name}: ${long || words}`);
      if (tip) el.setAttribute('data-tip', tip); else el.removeAttribute('data-tip');
    },
  };
}

// labelOf is a name for the drawing made from the name of an object: small letters, digits and underscores.
function labelOf(name) {
  return String(name || '').toLowerCase().replace(/[^a-z0-9_]+/g, '_').replace(/^[^a-z]+/, '').slice(0, 40).replace(/_+$/, '');
}

// cueFields shows as CUE the fields of a text or a number whose value is CUE: an interpolation, a reference
function cueFields(board, it) {
  for (const f of board.fields(it)) {
    const v = it.values[f.name];
    if (v && v.expr !== undefined && (f.kind === 'text' || (f.kind === 'number' && !/^-?\d+(\.\d+)?$/.test(v.expr.trim())))) it.cue.add(f.name);
  }
}

// guideText is the answer of the guide as the card shows it: lines, the ones that start with a dash as a list, and what
// is in backticks as code. It is text, never markup.
function guideText(text) {
  const out = [];
  let list = null;
  for (const line of String(text).split('\n')) {
    const t = line.trim();
    if (!t) { list = null; continue; }
    const bits = t.replace(/^[-*]\s+/, '').split('`').map((x, i) => (i % 2 ? h('code', null, x) : x));
    if (/^[-*]\s+/.test(t)) { if (!list) { list = h('ul'); out.push(list); } list.append(h('li', null, ...bits)); } else { list = null; out.push(h('p', null, ...bits)); }
  }
  return out;
}

const changeWord = (it, edit) => (it.remove ? 'draft · remove' : edit ? 'draft · change' : 'not changed yet');

// sameValue says whether two values of a field are the same: the same text, the same CUE, or the same wires
function sameValue(a, b) {
  if (!a || !b) return !a === !b;
  if (a.text !== undefined || b.text !== undefined) return a.text === b.text;
  if (a.expr !== undefined || b.expr !== undefined) return (a.expr || '').trim() === (b.expr || '').trim();
  const x = wiresOf(a), y = wiresOf(b);
  return x.length === y.length && x.every((w, i) => w.to === y[i].to && w.read === y[i].read) && !!a.wires === !!b.wires;
}

export function wiresOf(v) {
  if (!v) return [];
  if (v.wire) return [v.wire];
  if (v.wires) return v.wires;
  return [];
}

function setWire(it, f, to, read) {
  const v = it.values[f.name];
  if (f.wire && f.wire.many) {
    const ws = wiresOf(v).filter((w) => w.to !== to);
    ws.push({ to, read });
    it.values[f.name] = { wires: ws };
  } else it.values[f.name] = { wire: { to, read } };
}

function unwire(it, f, to) {
  const left = wiresOf(it.values[f.name]).filter((w) => w.to !== to);
  if (!left.length) delete it.values[f.name];
  else it.values[f.name] = f.wire && f.wire.many ? { wires: left } : { wire: left[0] };
}

function listText(expr) {
  try { const a = JSON.parse(expr); if (Array.isArray(a)) return a.join('\n'); } catch { /* CUE, not JSON */ }
  return expr || '';
}

const problemText = (p) => (p.item ? p.item + (p.field && !p.field.startsWith('@') ? '.' + p.field : '') + ': ' : '') + p.message;

function firstProblem(chk) {
  const p = (chk.problems || [])[0];
  if (!p) return 'Does not load';
  const t = problemText(p);
  return t.length > 64 ? t.slice(0, 63) + '…' : t;
}

// planProblems finds, in the error of a failed plan, the lines that name a thing drawn: "label: summary (at field,
// file:line): detail", as the engine writes what a provider says
function planProblems(text, labels) {
  const by = new Map();
  let item = '';
  for (const raw of String(text || '').split('\n')) {
    // a line that names a thing starts its errors; the lines after it, which name nothing, are more of them
    const line = raw.trim().replace(/\s*\(checked by the provider[^)]*\)\s*$/, '');
    const m = /^([a-z][a-z0-9_]*): (.+)$/.exec(line);
    let rest;
    if (m && labels.has(m[1])) { item = m[1]; rest = m[2]; }
    else if (m) { item = ''; continue; } // the errors of another resource
    else if (!item || !line) continue;
    else rest = line;
    const at = /\s*\(at ([a-z_][a-z0-9_]*)(?:, [^)]*)?\):?\s*/.exec(rest);
    const summary = at ? rest.slice(0, at.index) : rest;
    const detail = at ? rest.slice(at.index + at[0].length) : '';
    // a provider says the same of each field of a set ("one of content, source ... is required"): one problem says it
    const key = item + '|' + summary;
    const p = by.get(key) || { item, fields: [], summary, detail };
    if (at && !p.fields.includes(at[1])) p.fields.push(at[1]);
    by.set(key, p);
  }
  return [...by.values()].map((p) => ({
    item: p.item, field: p.fields.length === 1 ? p.fields[0] : '',
    message: `The provider says: ${p.summary}${p.fields.length > 1 ? ', at ' + p.fields.sort().join(', ') : ''}${p.detail ? ': ' + p.detail : ''}`,
  }));
}

const clipText = (t, n) => (t.length > n ? t.slice(0, n - 1) + '\u2026' : t);

// planMarks is a plan in the marks of pravo plan: +2 ~1 -1, and a move as an arrow: \u21922
function planMarks(r) {
  if (!r.add && !r.change && !r.destroy && !r.move) return 'No changes';
  return [r.add ? `+${r.add}` : '', r.change ? `~${r.change}` : '', r.destroy ? `\u2212${r.destroy}` : '', r.move ? `\u2192${r.move}` : ''].filter(Boolean).join(' ');
}

// onlyMoves says that a plan only moves records: a fold, or a $from written by hand
const onlyMoves = (r) => !!(r && r.move && !r.add && !r.change && !r.destroy);

function planWords(r) {
  if (!r) return '';
  if (!r.add && !r.change && !r.destroy && !r.move) return 'No changes';
  return [r.add ? `${r.add} to add` : '', r.change ? `${r.change} to change` : '', r.destroy ? `${r.destroy} to destroy` : '', r.move ? `${r.move} to move` : ''].filter(Boolean).join(', ');
}

const pad = (n) => String(n).padStart(2, '0');
function clockOf(ms) { const d = new Date(ms); return `${pad(d.getHours())}:${pad(d.getMinutes())}`; }
function baseOf(p) { const parts = String(p || '').split(/[\\/]/).filter(Boolean); return parts[parts.length - 1] || '·'; }
