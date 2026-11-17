// The drawing board (README rule 14): new resources drawn into the Inventory graph. The page holds the drawing; the
// server writes its CUE, says what CUE, the schemas and the policies say of it, and, when pravo serve runs with --edit,
// saves it as one new file and plans it. Nothing here stores a position: the layout engine places what is drawn, as it
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
    this.codeOpen = false;
    this.el = null;
    this.hits = [];
    this.hit = 0;
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
      code: h('button', { type: 'button', class: 'bd-b', 'aria-pressed': 'false', 'data-tip': 'Show the code of the drawing' }, icon('code'), h('span', { class: 'lbl' }, 'Code')),
      save: h('button', { type: 'button', class: 'bd-b', 'data-tip': 'Write the drawing as a new file of the specification' }, icon('file'), h('span', { class: 'lbl' }, 'Save')),
      plan: h('button', { type: 'button', class: 'bd-b go', 'data-tip': 'Plan the specification with the drawing in it, as pravo plan does' }, h('span', { class: 'bd-play', 'aria-hidden': 'true' }), h('span', { class: 'lbl' }, 'Plan')),
    };
    this.hint = h('p', { class: 'bd-hint' });
    this.tb = h('div', { class: 'bd-tb', role: 'group', 'aria-label': 'Title block of the drawing' });
    this.code = h('section', { class: 'bd-code', 'aria-label': 'The code of the drawing', hidden: true });
    this.sweep = h('div', { class: 'bd-sweep', 'aria-hidden': 'true' });
    const bar = h('div', { class: 'bd-bar' }, h('span', { class: 'bd-plus', 'aria-hidden': 'true' }, icon('plus')), this.input, this.hitsEl);
    this.strip = h('div', { class: 'bd-strip' }, bar,
      h('div', { class: 'bd-lamps', role: 'group', 'aria-label': 'Checks' }, this.lampEls.spec.el, this.lampEls.policy.el, this.lampEls.plan.el),
      h('div', { class: 'bd-acts' }, this.btn.code, this.btn.save, this.btn.plan));
    this.el = h('div', { class: 'overlay board', id: 'board', hidden: true }, this.sweep, this.code, this.tb, this.hint, this.strip);
    canvas.append(this.el);
    this.input.addEventListener('input', () => this.findTypes(this.input.value));
    this.input.addEventListener('keydown', (ev) => this.onBarKey(ev));
    this.input.addEventListener('focus', () => { if (this.input.value) this.findTypes(this.input.value); else this.searchTypes(''); });
    this.input.addEventListener('blur', () => setTimeout(() => { if (!this.hitsEl.contains(document.activeElement)) this.closeHits(); }, 120));
    this.hitsEl.addEventListener('mousedown', (ev) => ev.preventDefault());
    this.hitsEl.addEventListener('click', (ev) => { const b = ev.target.closest('[data-i]'); if (b) this.pick(this.hits[+b.dataset.i]); });
    this.btn.code.addEventListener('click', () => this.toggleCode());
    this.btn.save.addEventListener('click', () => this.save());
    this.btn.plan.addEventListener('click', () => this.runPlan());
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
    const have = new Set((g.nodes || []).map((n) => n.addr));
    // a thing that was saved and that the graph now shows is the real node: the drawing lets go of it
    const before = this.items.length;
    this.items = this.items.filter((it) => !(it.saved && have.has(it.label)));
    if (this.items.length !== before) {
      this.keep();
      // the chrome follows once the graph is drawn: what is left is checked again, or the code drawer says what was saved
      queueMicrotask(() => { if (this.items.length) this.recheck(); else { this.check = null; this.plan = null; } this.render(); });
    }
    const nodes = (g.nodes || []).slice(), edges = (g.edges || []).slice();
    const sel = this.cb.selected();
    const acts = this.planActions();
    for (const it of this.items) {
      if (have.has(it.label)) continue;
      const missing = this.missing(it);
      nodes.push({
        addr: it.label, mode: 'managed', type: it.kind === 'template' ? (it.form ? it.form.title : it.template) : it.type, name: it.label, namespace: '',
        provider: this.providerKey(it), presence: 'spec', draft: true, template: it.kind === 'template', adopts: !!it.adopt, incomplete: missing.length,
        sockets: this.sockets(it), boardAction: acts ? acts.get(it.label) || '' : '',
      });
    }
    const shown = new Set(nodes.map((n) => n.addr));
    for (const it of this.items) {
      if (have.has(it.label)) continue;
      // its edges are its wires, and what the last check found its code to read: a reference typed into an expression too
      const reads = new Set();
      for (const v of Object.values(it.values)) for (const w of wiresOf(v)) reads.add(w.to);
      for (const a of (this.check && this.check.reads && this.check.reads[it.label]) || []) reads.add(a);
      for (const a of reads) if (shown.has(a)) edges.push({ dependency: a, dependent: it.label, draft: true });
      for (const f of this.fields(it)) {
        if (!f.wire || it.values[f.name]) continue;
        const cands = this.candidates(it, f);
        // the selected thing shows every wire it could take; another shows the one that fits a socket it needs, when one alone does
        if (sel === it.label) for (const c of cands.slice(0, 4)) edges.push({ dependency: c.addr, dependent: it.label, offer: true });
        else if (f.required && cands.length === 1) edges.push({ dependency: cands[0].addr, dependent: it.label, offer: true });
      }
    }
    const out = Object.assign({}, g, { nodes, edges });
    if (acts) out.nodes = nodes.map((n) => (n.draft || !acts.has(n.addr) ? n : Object.assign({}, n, { boardAction: acts.get(n.addr) })));
    return out;
  }

  planActions() {
    const p = this.plan;
    if (!p || p.state !== 'done' || p.sig !== this.sig()) return null;
    return new Map(p.result.changes.map((c) => [c.addr, c.action]));
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
        if (n.type === t.type && n.mode === 'managed' && n.presence !== 'state' && !seen.has(n.addr)) { seen.add(n.addr); out.push({ addr: n.addr, type: n.type, read: t.read, rank }); }
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
    if (this.items.length) this.recheck(); else { this.check = null; this.render(); }
  }

  // draft is the drawing as the server reads it
  draft() {
    return { file: this.cfg.file, items: this.items.map((it) => ({ label: it.label, type: it.type || undefined, provider: it.provider || undefined, template: it.template || undefined, values: it.values, adopt: it.adopt ? { discovery: it.adopt.discovery, key: it.adopt.key } : undefined })) };
  }

  sig() { return JSON.stringify(this.draft().items); }

  async runCheck() {
    if (!this.items.length) return;
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
    this.check = out;
    this.render();
    this.paintProblems();
    if (JSON.stringify(out.reads || {}) !== before) this.cb.redraw({});
  }

  // ---- the bar ----

  async searchTypes(q) {
    const seq = ++this.typeSeq;
    let out;
    try { out = await api.boardTypes(q); } catch { return; }
    if (seq !== this.typeSeq || document.activeElement !== this.input) return;
    this.hits = out.hits || [];
    this.hit = 0;
    this.drawHits(q);
  }

  drawHits(q) {
    clear(this.hitsEl);
    if (!this.hits.length) {
      this.hitsEl.append(h('p', { class: 'bd-none' }, q ? `No resource type of ${this.providerNames()} is called “${q}”.` : 'Type what to add.'));
    }
    this.hits.forEach((x, i) => {
      this.hitsEl.append(h('button', { type: 'button', class: 'bd-hit' + (i === this.hit ? ' on' : ''), role: 'option', 'aria-selected': i === this.hit ? 'true' : 'false', data: { i } },
        h('b', null, x.title), h('code', null, x.kind === 'template' ? 'template ' + x.type : x.type), x.provider ? h('span', { class: 'bd-pv' }, x.provider) : h('span', { class: 'bd-pv t' }, 'template'),
        x.blurb ? h('span', { class: 'bd-blurb' }, x.blurb) : null));
    });
    this.hitsEl.hidden = false;
    this.input.setAttribute('aria-expanded', 'true');
  }

  providerNames() {
    const ps = (this.cfg && this.cfg.providers) || [];
    return ps.length ? ps.map((p) => p.label).join(', ') : 'the providers';
  }

  closeHits() { this.hitsEl.hidden = true; this.input.setAttribute('aria-expanded', 'false'); }

  onBarKey(ev) {
    if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
      ev.preventDefault();
      if (!this.hits.length) return;
      this.hit = (this.hit + (ev.key === 'ArrowDown' ? 1 : this.hits.length - 1)) % this.hits.length;
      this.drawHits(this.input.value);
      const on = this.hitsEl.querySelector('.on');
      if (on) on.scrollIntoView({ block: 'nearest' });
    } else if (ev.key === 'Enter') {
      ev.preventDefault();
      if (this.hits[this.hit]) this.pick(this.hits[this.hit]);
    }
  }

  async pick(hit) {
    this.closeHits();
    this.input.value = '';
    const it = await this.add(hit);
    if (it) this.input.focus();
  }

  // ---- the chrome ----

  render() {
    if (!this.el || !this.cfg) return;
    const chk = this.check;
    const n = this.items.length;
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
    this.btn.save.disabled = !(n && chk && chk.state === 'valid');
    this.btn.plan.disabled = !!(p && p.state === 'running') || (n > 0 && !(chk && chk.state === 'valid'));
    this.btn.code.setAttribute('aria-pressed', this.codeOpen ? 'true' : 'false');
    this.hint.hidden = n > 0;
    this.hint.textContent = this.cfg.problem || (this.edit
      ? 'Draw into the specification. Nothing is written until you save, and nothing is applied: that stays a command.'
      : 'Draw into the specification. Nothing is written: copy the code, or start pravo serve --edit to save and plan from here.');
    this.renderTitle();
    if (this.codeOpen) this.renderCode();
  }

  renderTitle() {
    const proj = this.cb.project() || {};
    const p = this.plan;
    const pol = this.check && this.check.policy;
    const checked = !pol ? '·' : pol.state === 'holds' ? plural(pol.policies.length, 'policy', 'policies') : pol.state === 'refused' ? 'refused' : pol.state === 'none' ? 'no policy' : 'not yet';
    const planned = p && p.state === 'done' ? `${clockOf(p.at)} · ${planWords(p.result)}` : p && p.state === 'running' ? 'planning' : '·';
    const rows = [['Project', proj.package || baseOf(proj.dir)], ['Revision', String(this.cb.revision() || '·').slice(0, 10)], ['Drawn by', this.items.length ? this.cfg.actor || 'you' : '·'], ['Checked by', checked], ['Planned', planned], ['Applied by', '·']];
    clear(this.tb);
    for (const [k, v] of rows) this.tb.append(h('div', { class: 'bd-tc' + (k === 'Applied by' ? ' ap' : '') }, h('span', null, k), h('b', { title: k === 'Applied by' ? 'Apply is a command, run from a terminal: pravo apply' : v }, v)));
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

  // renderCode shows the new file, with the lines of the selected thing lit
  renderCode() {
    clear(this.code);
    const chk = this.check;
    const sel = this.cb.selected();
    const head = h('div', { class: 'bd-ch' }, h('b', null, this.saved && !this.items.length ? this.saved.file : (chk && chk.file) || this.cfg.file),
      h('span', { class: 'grow' }),
      chk && chk.code ? h('button', { type: 'button', class: 'bd-b sm', on: { click: () => copyText(chk.code).then((ok) => this.cb.announce(ok ? 'Copied the code' : 'Could not copy')) } }, icon('copy'), h('span', { class: 'lbl' }, 'Copy the code')) : null,
      h('button', { type: 'button', class: 'iconbtn', 'aria-label': 'Close the code', on: { click: () => this.toggleCode(false) } }, icon('x')));
    this.code.append(head);
    if (this.saved && (!chk || !this.items.length)) {
      this.code.append(h('div', { class: 'bd-next' }, h('p', null, `Saved as ${this.saved.file}. Apply it from a terminal, where your keys are:`),
        ...this.saved.commands.map((c) => h('div', { class: 'cmdline' }, h('span', { class: 'ps' }, '$'), h('code', null, c), h('button', { class: 'cpbtn', type: 'button', 'aria-label': 'Copy the command', on: { click: () => copyText(c) } }, icon('copy'))))));
    }
    if (!chk || !chk.code) {
      if (!this.saved) this.code.append(h('p', { class: 'bd-empty' }, this.items.length ? 'Writing the code…' : 'The code appears here as you draw.'));
      return;
    }
    const rng = chk.lines && chk.lines[sel];
    const lines = chk.code.replace(/\n$/, '').split('\n').map((text, i) => ({ no: i + 1, text, mark: rng && i + 1 >= rng[0] && i + 1 <= rng[1] ? 'focus' : '' }));
    this.code.append(codeWindow({ file: chk.file, start_line: 1, end_line: lines.length, lines, kind: 'file' }, { title: `The code of the drawing, ${chk.file}`, cls: 'bd-cw' }));
    const focus = this.code.querySelector('.m-focus');
    if (focus) focus.scrollIntoView({ block: 'nearest' });
  }

  // ---- save and plan ----

  async save() {
    if (!this.edit || !this.check || this.check.state !== 'valid') return;
    this.btn.save.disabled = true;
    let out;
    try { out = await api.boardPost('/api/board/save', this.draft()); } catch (err) {
      this.cb.toast(`Could not save: ${err.message}`, 'error', 9000, err.check ? (err.check.problems || []).map(problemText).join(' · ') : '');
      this.render();
      return;
    }
    for (const it of this.items) it.saved = true;
    this.saved = { file: out.file, commands: out.commands, at: Date.now() };
    this.cb.toast(`Saved ${out.file}`, 'info', 6000, 'The graph shows it once the specification is read again. Apply it from a terminal: ' + out.commands[out.commands.length - 1]);
    this.cb.announce(`Saved ${out.file}`);
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

  // showForm draws the form of a thing drawn in the inspector
  showForm(label, insp) {
    const it = this.item(label);
    if (!it) return;
    const f = it.form || { fields: [], title: it.type };
    const nameIn = h('input', { type: 'text', class: 'bd-name', value: it.label, spellcheck: 'false', 'aria-label': 'Name' });
    const nameErr = h('p', { class: 'bd-err', role: 'alert' });
    nameIn.addEventListener('change', () => { const msg = this.rename(it, nameIn.value.trim()); nameErr.textContent = msg; if (msg) nameIn.value = it.label; });
    nameIn.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') nameIn.blur(); });
    const head = {
      kick: it.kind === 'template' ? 'Drawing · template' : it.adopt ? 'Drawing · resource that adopts' : 'Drawing · resource',
      title: nameIn,
      path: h('div', { class: 'ih-path bd-what' }, h('b', null, f.title), h('code', null, it.kind === 'template' ? it.template : it.type), it.provider ? h('span', { class: 'b' }, it.provider) : null),
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
      h('button', { type: 'button', class: 'bd-b', 'data-act': 'code', 'data-tip': 'Show its lines in the code' }, icon('code'), h('span', { class: 'lbl' }, 'Its code')));
    body.append(tools);
    tools.addEventListener('click', (ev) => {
      const b = ev.target.closest('[data-act]');
      if (!b) return;
      if (b.dataset.act === 'complete') this.complete(it.label).then((n) => { if (!n) this.cb.announce('Nothing to wire: every socket it needs has a wire, or several things fit'); this.cb.redrawInspector(); });
      if (b.dataset.act === 'code') this.toggleCode(true);
    });
    const req = f.fields.filter((x) => x.required);
    const set = f.fields.filter((x) => !x.required && (it.values[x.name] || it.shown.has(x.name)));
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
        const pick = rest.filter((x) => !it.values[x.name] && !it.shown.has(x.name) && (!w || x.name.includes(w) || (x.doc || '').toLowerCase().includes(w)));
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
    body.append(h('div', { class: 'bd-foot' }, h('button', { type: 'button', class: 'bd-b danger', on: { click: () => this.remove(it.label) } }, icon('x'), h('span', { class: 'lbl' }, 'Remove from the drawing'))));
    insp.showDraft(head, body);
    this.paintProblems();
  }

  // paintProblems puts what the check says of the selected thing in its form
  paintProblems() {
    const body = this.formEl, it = this.formItem;
    if (!body || !it || !body.isConnected) return;
    const chk = this.check;
    const mine = ((chk && chk.problems) || []).filter((p) => p.item === it.label || (!p.item && chk.state !== 'incomplete'));
    const pl = this.plan;
    if (pl && pl.state === 'failed' && pl.sig === this.sig()) mine.push(...pl.problems.filter((p) => p.item === it.label));
    const box = body.querySelector('.bd-probs');
    clear(box);
    const rows = new Set([...body.querySelectorAll('.bd-row')].map((r) => r.dataset.f));
    const said = new Set();
    for (const p of mine.filter((x) => !x.field || x.field.startsWith('@') || !rows.has(x.field))) {
      if (said.has(p.message)) continue; // a provider says the same of each field of a set
      said.add(p.message);
      box.append(h('p', { class: 'bd-err' }, p.message));
    }
    for (const row of body.querySelectorAll('.bd-row')) {
      const p = mine.find((x) => x.field === row.dataset.f);
      const err = row.querySelector('.bd-fe');
      err.textContent = p && p.message !== 'needs a value' ? p.message : '';
      row.classList.toggle('need', !it.values[row.dataset.f] && row.classList.contains('req'));
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
        ctl.append(h('p', { class: 'bd-sens' }, icon('lock'), 'Sensitive: wire it, or write CUE that reads a secret. The board writes no secret into the code.'));
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
      if (!this.items.length) { sessionStorage.removeItem(key); return; }
      sessionStorage.setItem(key, JSON.stringify(this.items.map((it) => ({ label: it.label, kind: it.kind, type: it.type, template: it.template, provider: it.provider, values: it.values, shown: [...it.shown], cue: [...it.cue], saved: !!it.saved, adopt: it.adopt || undefined }))));
    } catch { /* a private window */ }
  }

  async restore() {
    let list;
    try { list = JSON.parse(sessionStorage.getItem(KEEP + ((this.cb.project() || {}).dir || '')) || '[]'); } catch { list = []; }
    if (!Array.isArray(list) || !list.length) return;
    for (const x of list.slice(0, 64)) {
      try {
        const form = await this.form(x.kind, x.kind === 'template' ? x.template : x.type, x.provider);
        if (this.item(x.label)) continue; // drawn again while the drawing came back
        const adopt = x.adopt && typeof x.adopt.discovery === 'string' && typeof x.adopt.key === 'string' ? { discovery: x.adopt.discovery, key: x.adopt.key, name: String(x.adopt.name || '') } : null;
        this.items.push({ label: x.label, kind: x.kind, type: x.type || '', template: x.template || '', provider: x.provider || '', values: x.values || {}, shown: new Set(x.shown || []), cue: new Set(x.cue || []), saved: !!x.saved, adopt, form });
      } catch { /* a type that the providers do not have any more */ }
    }
    if (this.items.length) this.changed({ graph: true });
  }
}

// ---- helpers ----

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

// planMarks is a plan in the marks of pravo plan: +2 ~1 -1
function planMarks(r) {
  if (!r.add && !r.change && !r.destroy) return 'No changes';
  return [r.add ? `+${r.add}` : '', r.change ? `~${r.change}` : '', r.destroy ? `\u2212${r.destroy}` : ''].filter(Boolean).join(' ');
}

function planWords(r) {
  if (!r) return '';
  if (!r.add && !r.change && !r.destroy) return 'No changes';
  return [r.add ? `${r.add} to add` : '', r.change ? `${r.change} to change` : '', r.destroy ? `${r.destroy} to destroy` : ''].filter(Boolean).join(', ');
}

const pad = (n) => String(n).padStart(2, '0');
function clockOf(ms) { const d = new Date(ms); return `${pad(d.getHours())}:${pad(d.getMinutes())}`; }
function baseOf(p) { const parts = String(p || '').split(/[\\/]/).filter(Boolean); return parts[parts.length - 1] || '·'; }
