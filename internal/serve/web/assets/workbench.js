// The policy workbench: write a rule, see which plans it would refuse and why, before it is stored.
//
// A rule is CUE that is unified with the document of a plan and has to make it concrete. The page sends the text of a
// draft with the plans to try it on (GET /api/workbench/try), and the server answers for each plan whether the rule holds,
// and where in the document it does not. The server runs the rule in a CUE context of its own and writes nothing: the
// state, the plans and the policies are as they were. The page also explains the plan document, which a rule is written
// against, and what the apply does with a rule.

import { h, clear, icon, plural, relTime, stamp, debounce, store } from './util.js';
import { tokenize } from './cue.js';
import * as api from './api.js';

const DRAFT_KEY = 'pravo.workbench.draft';
const MAX_PLANS = 100;

const BLANK = '// A rule says what must hold of the document of a plan. This one allows no destroy.\nsummary: destroy: 0\n';

// OUTCOME says what a plan is to the rule, in words and in the one colour that means it: green holds, red is refused,
// grey is not judged.
const OUTCOME = {
  holds: { cls: 'ok', label: 'holds', mark: 'ok' },
  refused: { cls: 'bad', label: 'would be refused', mark: 'bad' },
  invalid: { cls: '', label: 'the rule cannot be read', mark: 'off' },
  'not-judged': { cls: '', label: 'not judged', mark: 'off' },
  sealed: { cls: '', label: 'sealed', mark: 'off' },
  unreadable: { cls: '', label: 'cannot be read', mark: 'off' },
  unknown: { cls: '', label: 'gone', mark: 'off' },
};

const STAGE = {
  parse: 'The text is not valid CUE.',
  imports: 'A policy is standalone: it may import the CUE standard library and nothing else.',
  compile: 'The text does not compile.',
  empty: 'A rule is checked when it is added against the document of a plan that changes nothing, and this one refuses it. A rule that requires a value must require it only of the changes that exist: write it inside a list over changes.',
  shape: 'The rule does not fit the plan document. It names a field that the document does not have, or a value outside the enum of a field.',
};

const ident = (name) => (/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) ? name : JSON.stringify(name));

// asPolicy is the rule as the specification would declare it.
export function asPolicy(name, text) {
  const body = text.replace(/\s+$/, '').split('\n').map((l) => (l ? '\t\t' + l : l)).join('\n');
  return `import "pravo.dev/pravo"\n\n${ident(name || 'my_policy')}: pravo.#Policy & {\n\trule: {\n${body}\n\t}\n}\n`;
}

// pathChange reads the index of the change that a path of the document leads into, or -1.
export function pathChange(path) {
  const m = /^changes\.(\d+)(\.|$)/.exec(path || '');
  return m ? Number(m[1]) : -1;
}

export class WorkbenchView {
  constructor(root, cb = {}) {
    this.root = root;
    this.cb = cb;
    this.data = null;
    this.loadedRev = null;
    this.loading = null;
    this.error = '';
    this.name = 'draft';
    this.text = BLANK;
    this.base = ''; // the policy whose version in force is tried beside the draft
    this.scope = { open: true, closed: false, files: true };
    this.start = 'blank';
    this.result = null;
    this.busy = false;
    this.ctl = null;
    this.docs = new Map(); // ref -> the opened document: { outline, head, sel, change }
    this.opened = new Set(); // refs whose rows are open
    this.docsOpen = new Set(); // refs whose document is open
    this.filter = 'all';
    try {
      const d = JSON.parse(store.get(DRAFT_KEY) || 'null');
      if (d && typeof d.text === 'string' && d.text.length < 32768) { this.text = d.text; this.name = d.name || 'draft'; }
    } catch { /* no draft */ }
    this.tryLater = debounce(() => this.run(), 280);
  }

  // mount is the element the workbench draws in; it is made once and put in the pane each time the page is drawn.
  mount() {
    if (!this.shell) this.build();
    return this.shell;
  }

  // show draws the workbench. The policies document says which revision the page is at; when it moved, the plans are read again.
  show(policies) {
    this.policies = policies;
    const rev = policies ? policies.revision : null;
    if (!this.shell) this.build();
    if (!this.data || (rev && rev !== this.loadedRev)) this.load(rev);
    else this.paint();
  }

  // load reads the plans and policies to start from; the draft stays as it is.
  load(rev) {
    this.loadedRev = rev;
    this.error = '';
    if (!this.data) this.paintBusy();
    const mine = (this.loading = {});
    api.workbench().then((d) => {
      if (this.loading !== mine) return;
      this.data = d;
      this.docs.clear();
      this.paint();
      if (this.want) this.applyWant(); else this.run();
    }, (err) => {
      if (this.loading !== mine) return;
      this.error = err && err.message ? err.message : 'the workbench could not be read';
      this.paint();
    });
  }

  close() { if (this.ctl) this.ctl.abort(); }

  // tryPolicy starts the draft from a policy of the register: the version in force ('policy') or the text the specification declares ('declared').
  tryPolicy(name, which) {
    this.want = { name, which };
    if (this.data) this.applyWant();
  }

  applyWant() {
    const w = this.want;
    this.want = null;
    if (!w) return;
    const p = this.data.policies.find((x) => x.name === w.name);
    if (!p) return;
    this.startFrom((w.which === 'declared' && p.declared ? 'declared:' : 'policy:') + p.name);
  }

  persist() { store.set(DRAFT_KEY, JSON.stringify({ name: this.name, text: this.text })); }

  // ---- the plans a request names ----

  refs() {
    const out = [];
    for (const p of (this.data && this.data.plans) || []) {
      const on = p.kind === 'file' ? this.scope.files : p.open ? this.scope.open : this.scope.closed;
      if (on) out.push(p.ref);
    }
    return out;
  }

  // run tries the draft on the plans that are in scope, and gives up an earlier request that is still out.
  run() {
    if (!this.data) return;
    if (this.ctl) this.ctl.abort();
    const refs = this.refs().slice(0, MAX_PLANS);
    this.ctl = new AbortController();
    const mine = this.ctl;
    this.busy = true;
    this.paintResults();
    api.tryRule({ rule: this.text, name: this.name, base: this.base, refs }, mine.signal).then((r) => {
      if (this.ctl !== mine) return;
      this.busy = false;
      this.result = r;
      this.error = '';
      this.paintResults();
      this.markEditor();
    }, (err) => {
      if (this.ctl !== mine || (err && err.name === 'AbortError')) return;
      this.busy = false;
      this.result = null;
      this.error = err && err.message ? err.message : 'the rule could not be tried';
      this.paintResults();
    });
  }

  // ---- the page ----

  build() {
    this.shell = h('div', { class: 'wb' });
    this.editor = this.buildEditor();
    this.resultsEl = h('section', { class: 'wb-results', 'aria-labelledby': 'wb-rh', 'aria-live': 'polite' });
    this.controlsEl = h('div', { class: 'wb-controls' });
    this.guideEl = h('section', { class: 'wb-guide', 'aria-labelledby': 'wb-gh' });
    this.headEl = h('header', { class: 'wb-head' });
  }

  paintBusy() {
    clear(this.shell);
    this.shell.append(this.headEl, h('p', { class: 'wb-empty' }, 'Reading the plans…'));
    this.paintHead();
  }

  paint() {
    clear(this.shell);
    this.paintHead();
    if (!this.data) {
      this.shell.append(this.headEl, h('p', { class: 'wb-empty' }, this.error ? 'The workbench could not be read: ' + this.error : 'Reading the plans…'));
      return;
    }
    this.paintControls();
    this.shell.append(this.headEl,
      h('div', { class: 'wb-body' },
        h('section', { class: 'wb-left', 'aria-labelledby': 'wb-eh' },
          h('h3', { class: 'wb-h3', id: 'wb-eh' }, 'Rule'),
          this.controlsEl, this.editor.wrap, this.editorNotes(), this.takeAway()),
        this.resultsEl),
      this.guideEl);
    this.paintGuide();
    this.paintResults();
    this.syncEditor();
    this.markEditor();
  }

  paintHead() {
    clear(this.headEl);
    this.headEl.append(h('div', { class: 'wb-htext' },
      h('h2', { class: 'po-title', id: 'wb-h' }, 'Policy workbench'),
      h('p', { class: 'po-lede' }, 'Write a rule and see which plans it would refuse, and why, before it is stored. The plans are the ones stored in the state and the plan files of the project; each is judged by the rule exactly as an apply would judge it.')),
      h('p', { class: 'wb-promise' }, icon('lock'), h('span', null, 'Nothing is applied or stored. The rule is run in a scratch evaluator, and the project, the state and the policies stay as they were.')));
  }

  // ---- the controls over the editor ----

  paintControls() {
    const d = this.data;
    const startSel = h('select', { class: 'wb-select', 'aria-label': 'Start from', on: { change: (ev) => this.startFrom(ev.target.value) } });
    const opt = (value, label, group) => (group || startSel).append(h('option', { value, selected: this.start === value }, label));
    opt('blank', 'A blank rule');
    if (d.examples.length) {
      const g = h('optgroup', { label: 'Examples' });
      d.examples.forEach((e) => opt('ex:' + e.id, e.title, g));
      startSel.append(g);
    }
    const inForce = d.policies.filter((p) => p.in_force), declared = d.policies.filter((p) => p.declared && p.declared !== p.in_force);
    if (inForce.length) {
      const g = h('optgroup', { label: 'A policy in force' });
      inForce.forEach((p) => opt('policy:' + p.name, p.name, g));
      startSel.append(g);
    }
    if (declared.length) {
      const g = h('optgroup', { label: 'The specification’s text' });
      declared.forEach((p) => opt('declared:' + p.name, p.name + ' (declared)', g));
      startSel.append(g);
    }
    const baseSel = h('select', { class: 'wb-select', 'aria-label': 'Compare with', disabled: !inForce.length, on: { change: (ev) => { this.base = ev.target.value; this.run(); } } },
      h('option', { value: '' }, inForce.length ? 'nothing' : 'no policy in force'),
      inForce.map((p) => h('option', { value: p.name, selected: this.base === p.name }, 'in force: ' + p.name)));
    const nameIn = h('input', { class: 'wb-name', type: 'text', value: this.name, spellcheck: 'false', maxlength: '100', 'aria-label': 'Name of the policy',
      on: { input: (ev) => { this.name = ev.target.value.trim() || 'draft'; this.persist(); this.paintTakeAway(); this.tryLater(); } } });
    clear(this.controlsEl);
    this.controlsEl.append(
      h('label', { class: 'wb-f' }, h('span', { class: 'k' }, 'start from'), startSel),
      h('label', { class: 'wb-f' }, h('span', { class: 'k' }, 'name'), nameIn),
      h('label', { class: 'wb-f' }, h('span', { class: 'k' }, 'compare with'), baseSel));
  }

  startFrom(v) {
    this.start = v;
    const d = this.data;
    const [kind, ...rest] = v.split(':');
    const key = rest.join(':');
    if (kind === 'blank') { this.text = BLANK; this.name = 'draft'; this.base = ''; }
    else if (kind === 'ex') { const e = d.examples.find((x) => x.id === key); if (e) { this.text = e.rule; this.name = e.id.replace(/-/g, '_'); this.base = ''; } }
    else if (kind === 'policy') { const p = d.policies.find((x) => x.name === key); if (p) { this.text = p.in_force; this.name = p.name; this.base = p.name; } }
    else if (kind === 'declared') { const p = d.policies.find((x) => x.name === key); if (p) { this.text = p.declared; this.name = p.name; this.base = p.in_force ? p.name : ''; } }
    this.persist();
    this.paintControls();
    this.syncEditor();
    this.paintTakeAway();
    this.run();
  }

  // ---- the editor: a text area over the same text drawn in colour ----

  buildEditor() {
    const ta = h('textarea', { class: 'wb-ta', spellcheck: 'false', autocomplete: 'off', autocapitalize: 'off', autocorrect: 'off', wrap: 'off', 'aria-label': 'The rule, in CUE', 'aria-describedby': 'wb-keys' });
    const hl = h('pre', { class: 'wb-hl', 'aria-hidden': 'true' });
    const gut = h('div', { class: 'wb-gut', 'aria-hidden': 'true' });
    const view = h('div', { class: 'wb-view' }, hl, ta);
    const wrap = h('div', { class: 'wb-ed' }, h('div', { class: 'wb-gutwrap' }, gut), view);
    ta.addEventListener('input', () => { this.text = ta.value; this.persist(); this.start = this.start.startsWith('blank') ? 'blank' : this.start; this.drawText(); this.tryLater(); });
    ta.addEventListener('scroll', () => this.sync());
    ta.addEventListener('keydown', (ev) => this.key(ev));
    return { ta, hl, gut, wrap, view, marks: new Map() };
  }

  // key makes a few keys useful in a rule: Tab indents and Enter keeps the indent. Escape leaves the editor, as it leaves any field of the page.
  key(ev) {
    const ta = this.editor.ta;
    if (ev.key === 'Tab' && !ev.shiftKey && !ev.ctrlKey && !ev.metaKey && !ev.altKey) {
      ev.preventDefault();
      this.insert('\t');
    } else if (ev.key === 'Enter' && !ev.ctrlKey && !ev.metaKey && !ev.altKey && !ev.shiftKey) {
      const before = ta.value.slice(0, ta.selectionStart);
      const line = before.slice(before.lastIndexOf('\n') + 1);
      const indent = /^[\t ]*/.exec(line)[0];
      const opens = /[{[(]\s*$/.test(line);
      ev.preventDefault();
      this.insert('\n' + indent + (opens ? '\t' : ''));
    }
  }

  insert(s) {
    const ta = this.editor.ta;
    ta.setRangeText(s, ta.selectionStart, ta.selectionEnd, 'end');
    ta.dispatchEvent(new Event('input', { bubbles: true }));
  }

  syncEditor() {
    const e = this.editor;
    if (e.ta.value !== this.text) e.ta.value = this.text;
    this.drawText();
  }

  // drawText colours the text and sizes the window to it.
  drawText() {
    const e = this.editor;
    const lines = this.text.replace(/\n$/, '').split('\n');
    const toks = tokenize(lines);
    clear(e.hl);
    clear(e.gut);
    lines.forEach((l, i) => {
      const row = h('div', { class: 'wb-ln', 'data-no': String(i + 1) });
      for (const t of toks[i]) row.append(t.t ? h('span', { class: 'tk-' + t.t }, t.text) : t.text);
      if (!toks[i].length) row.append('​');
      e.hl.append(row);
      e.gut.append(h('i', { class: 'wb-no', 'data-no': String(i + 1) }, String(i + 1)));
    });
    e.wrap.style.setProperty('--wb-lines', String(Math.min(30, Math.max(12, lines.length + 1))));
    this.markEditor();
    this.sync();
  }

  sync() {
    const e = this.editor;
    e.hl.style.transform = `translate(${-e.ta.scrollLeft}px, ${-e.ta.scrollTop}px)`;
    e.gut.style.transform = `translateY(${-e.ta.scrollTop}px)`;
  }

  // markEditor marks the lines the last answer is about: a problem of the text, and the lines of the rule that a refusal points at.
  markEditor() {
    const e = this.editor;
    const bad = new Map();
    const r = this.result;
    if (r && r.check && r.check.issues) for (const is of r.check.issues) if (is.line) bad.set(is.line, is.message);
    if (r) for (const p of r.plans) if (p.draft && p.draft.outcome === 'invalid') for (const is of p.draft.issues || []) if (is.line && !bad.has(is.line)) bad.set(is.line, is.message);
    this.hot = new Map();
    if (r) for (const p of r.plans) if (p.draft && p.draft.outcome === 'refused') for (const is of p.draft.issues || []) if (is.line) this.hot.set(is.line, (this.hot.get(is.line) || 0) + 1);
    Array.from(e.hl.children).forEach((row, i) => {
      const n = i + 1;
      row.classList.toggle('bad', bad.has(n));
      row.classList.toggle('hot', !bad.has(n) && this.hot.has(n));
    });
    Array.from(e.gut.children).forEach((g, i) => {
      const n = i + 1;
      g.classList.toggle('bad', bad.has(n));
      g.classList.toggle('hot', !bad.has(n) && this.hot.has(n));
      if (bad.has(n)) g.title = bad.get(n);
      else if (this.hot.has(n)) g.title = `Where ${plural(this.hot.get(n), 'refusal')} point into the rule`;
      else g.removeAttribute('title');
    });
  }

  editorNotes() {
    return h('p', { class: 'wb-keys', id: 'wb-keys' }, h('kbd', null, 'Tab'), ' indents and ', h('kbd', null, 'Esc'), ' leaves the editor. A red line is where CUE found a problem with the text; an amber one is where a refusal points into the rule.');
  }

  // takeAway is what to do with a rule that works: the policy as the specification declares it, and the command that tries it again.
  takeAway() {
    this.takeEl = h('div', { class: 'wb-take' });
    this.paintTakeAway();
    return this.takeEl;
  }

  paintTakeAway() {
    if (!this.takeEl) return;
    const spec = asPolicy(this.name, this.text);
    const plan = (this.data && this.data.plans.find((p) => p.open)) || null;
    const cmd = `pravo policy test ${this.name.replace(/[^A-Za-z0-9_.-]/g, '_')}.cue` + (plan ? ` --plan ${plan.kind === 'file' ? plan.label : plan.ref}` : '');
    const copyBtn = (text, label) => h('button', { class: 'cpbtn', type: 'button', 'data-copy': text, 'aria-label': label, 'data-tip': label }, icon('copy'));
    clear(this.takeEl);
    this.takeEl.append(
      h('details', { class: 'wb-det' },
        h('summary', null, h('span', { class: 'chev', 'aria-hidden': 'true' }, icon('chev')), 'Use this rule'),
        h('p', { class: 'po-note' }, 'To store the rule, declare it in the specification. The next ', h('code', null, 'pravo apply'), ' writes it to the state as a signed version, and every later plan is judged by it. The plan that adds it is not judged by it.'),
        h('div', { class: 'wb-snip' }, h('pre', null, spec), copyBtn(spec, 'Copy the declaration')),
        h('p', { class: 'po-note' }, 'To try it from a terminal or a pipeline, save the text of the rule in a file and run:'),
        h('div', { class: 'cmdline po-cmd' }, h('span', { class: 'ps', 'aria-hidden': 'true' }, '$'), h('code', null, cmd), copyBtn(cmd, 'Copy the command')),
        h('p', { class: 'po-note' }, h('code', null, 'pravo policy test'), ' exits 5 when a rule would refuse a plan, so it can gate a change before the apply does.')));
  }

  // ---- the results ----

  paintResults() {
    const el = this.resultsEl;
    clear(el);
    const r = this.result;
    const plans = (this.data && this.data.plans) || [];
    el.append(h('div', { class: 'wb-rhead' },
      h('h3', { class: 'wb-h3', id: 'wb-rh' }, 'Plans', h('span', { class: 'po-n' }, String(this.refs().length))),
      this.scopeBar(plans),
      this.busy ? h('span', { class: 'wb-busy' }, 'trying…') : r ? h('span', { class: 'wb-took', title: 'How long the server took' }, `${r.took_ms} ms`) : null));
    if (this.error) el.append(h('p', { class: 'po-problem' }, icon('bang'), h('span', null, this.error)));
    if (this.data && !this.data.spec_ok) el.append(h('p', { class: 'po-warn' }, icon('bang'), h('span', null, 'The specification does not load, so the values of a plan cannot be decoded and no rule can be tried on one.')));
    if (this.data && this.data.note) el.append(h('p', { class: 'po-note' }, this.data.note));
    if (r && r.check) el.append(this.checkBox(r.check));
    if (!plans.length) {
      el.append(h('p', { class: 'wb-empty' }, 'There is no plan to try the rule on. Store one with ', h('code', null, 'pravo plan --store'), ', or save one with ', h('code', null, 'pravo plan --out plan.json'), ' in the project directory.'));
      return;
    }
    if (!r) { if (!this.busy && !this.error) el.append(h('p', { class: 'wb-empty' }, 'Choose which plans to try the rule on.')); return; }
    const tally = { holds: 0, refused: 0, none: 0 };
    for (const p of r.plans) {
      const o = p.draft ? p.draft.outcome : p.state;
      if (o === 'holds') tally.holds++; else if (o === 'refused') tally.refused++; else tally.none++;
    }
    el.append(h('ul', { class: 'wb-tally', 'aria-label': 'What the rule says' },
      this.tallyCell('holds', tally.holds, 'ok', 'holds'),
      this.tallyCell('refused', tally.refused, tally.refused ? 'bad' : '', 'refused'),
      this.tallyCell('not judged', tally.none, '', 'none')));
    const rows = r.plans.filter((p) => {
      const o = p.draft ? p.draft.outcome : p.state;
      if (this.filter === 'holds') return o === 'holds';
      if (this.filter === 'refused') return o === 'refused';
      if (this.filter === 'none') return !(o === 'holds' || o === 'refused');
      return true;
    });
    if (this.refs().length > MAX_PLANS) el.append(h('p', { class: 'po-note' }, `The first ${MAX_PLANS} of ${this.refs().length} plans are tried.`));
    el.append(h('ul', { class: 'wb-plans' }, rows.map((p) => this.planRow(p, r))));
    if (!rows.length) el.append(h('p', { class: 'wb-empty' }, 'No plan is in this group.'));
  }

  tallyCell(k, n, cls, filter) {
    return h('li', { class: cls }, h('button', { type: 'button', class: 'wb-tb', 'aria-pressed': this.filter === filter ? 'true' : 'false', on: { click: () => { this.filter = this.filter === filter ? 'all' : filter; this.paintResults(); } } },
      h('span', { class: 'k' }, k), h('b', null, String(n))));
  }

  // scopeBar chooses which plans are tried: the ones that can still be applied, the rest of the stored ones, and the plan files.
  scopeBar(plans) {
    const n = { open: 0, closed: 0, files: 0 };
    for (const p of plans) n[p.kind === 'file' ? 'files' : p.open ? 'open' : 'closed']++;
    const chip = (key, label, tip) => h('button', { type: 'button', class: 'wb-chip', 'aria-pressed': this.scope[key] ? 'true' : 'false', disabled: !n[key], title: tip,
      on: { click: () => { this.scope[key] = !this.scope[key]; this.paintResults(); this.run(); } } }, label, h('span', { class: 'n' }, String(n[key])));
    return h('div', { class: 'wb-scope', role: 'group', 'aria-label': 'Plans to try' },
      chip('open', 'can be applied', 'Stored plans that are pending, partial or partly stale'),
      chip('closed', 'other stored', 'Stored plans that are applied, stale or expired'),
      chip('files', 'plan files', 'Plan files in the project directory'));
  }

  checkBox(c) {
    return h('div', { class: 'wb-check' },
      h('p', { class: 'wb-ct' }, icon('bang'), h('span', null, h('b', null, 'This text cannot be stored as a policy yet. '), STAGE[c.stage] || '')),
      h('pre', null, c.message));
  }

  planRow(tp, r) {
    const label = this.labelOf(tp.ref);
    const meta = this.metaOf(tp.ref);
    const res = tp.draft;
    const key = res ? res.outcome : tp.state;
    const out = OUTCOME[key] || OUTCOME.unreadable;
    const open = this.opened.has(tp.ref);
    const delta = this.delta(tp);
    const refusal = res && res.outcome === 'refused';
    const body = h('div', { class: 'wb-rbody', hidden: !open });
    if (open) body.append(...this.rowBody(tp, res, meta));
    const head = h('button', { type: 'button', class: 'wb-row mk-' + out.mark, 'aria-expanded': open ? 'true' : 'false', 'data-fk': 'wb:' + tp.ref,
      on: { click: () => { if (this.opened.has(tp.ref)) this.opened.delete(tp.ref); else this.opened.add(tp.ref); this.paintResults(); } } },
      h('i', { class: 'po-mark', 'aria-hidden': 'true' }),
      h('span', { class: 'wb-rn' }, h('b', null, label), meta ? h('span', { class: 'wb-rmeta' }, this.metaText(meta)) : null),
      h('span', { class: 'wb-ro ' + out.cls }, out.label, refusal ? ` · ${plural((res.issues || []).length + (res.more || 0), 'reason')}` : ''),
      delta ? h('span', { class: 'b ' + delta.cls }, delta.text) : null,
      h('span', { class: 'chev' + (open ? ' open' : ''), 'aria-hidden': 'true' }, icon('chev')));
    return h('li', { class: 'wb-item' }, head, body);
  }

  labelOf(ref) { const p = this.data && this.data.plans.find((x) => x.ref === ref); return p ? p.label : ref; }
  metaOf(ref) { return this.data && this.data.plans.find((x) => x.ref === ref); }

  metaText(m) {
    const bits = [];
    if (m.kind === 'file') bits.push('file'); else bits.push(m.status);
    if (m.author) bits.push(m.author);
    bits.push(relTime(m.created));
    bits.push(`+${m.add}\u00a0~${m.change}\u00a0−${m.destroy}`);
    return bits.filter(Boolean).join(' · ');
  }

  // delta compares the draft with the version in force: a plan that the draft refuses and the version holds is the one to look at.
  delta(tp) {
    if (!tp.base || !tp.draft) return null;
    const was = tp.base.outcome === 'holds', is = tp.draft.outcome === 'holds';
    if (was && !is) return { cls: 'bad', text: 'newly refused' };
    if (!was && is) return { cls: 'ok', text: 'no longer refused' };
    return null;
  }

  rowBody(tp, res, meta) {
    const out = [];
    if (tp.note) out.push(h('p', { class: 'po-note' }, tp.note));
    if (tp.base && this.result && this.result.base) {
      const b = OUTCOME[tp.base.outcome] || OUTCOME.unreadable;
      out.push(h('p', { class: 'po-note' }, `The version in force of ${this.result.base} `, h('b', { class: b.cls }, tp.base.outcome === 'holds' ? 'holds' : 'refuses'), ' this plan.'));
    }
    if (res && res.outcome === 'holds') out.push(h('p', { class: 'po-note' }, `The rule holds: the plan’s document of ${plural(tp.changes, 'change')} satisfies it and the result is concrete.`));
    if (res && res.outcome === 'invalid') out.push(h('p', { class: 'po-note' }, 'The text of the rule does not compile, so it says nothing of this plan. The problem is above the plans.'));
    if (res && res.outcome === 'refused' && (res.issues || []).length) out.push(h('ol', { class: 'wb-issues' }, res.issues.map((is) => this.issue(tp, is))));
    if (res && res.more) out.push(h('p', { class: 'po-note' }, `and ${res.more} more`));
    if (tp.state === 'judged' || tp.state === 'not-judged') out.push(this.docBlock(tp.ref));
    if (meta && meta.kind === 'stored') out.push(h('p', { class: 'wb-cmd' }, h('span', { class: 'k' }, 'plan'), h('code', null, meta.ref),
      h('button', { class: 'cpbtn', type: 'button', 'data-copy': meta.ref, 'aria-label': 'Copy the id of the plan', 'data-tip': 'Copy the id' }, icon('copy'))));
    return out;
  }

  // issue is one reason for a refusal: where in the document, what CUE says, and what the document holds there.
  issue(tp, is) {
    const n = pathChange(is.path);
    const where = is.change ? h('button', { type: 'button', class: 'wb-link', on: { click: () => this.openDoc(tp.ref, n) } }, is.change) : null;
    return h('li', { class: 'wb-issue' },
      h('div', { class: 'wb-ipath' }, is.path ? h('code', null, is.path) : h('span', { class: 'dim' }, 'the text of the rule'), where ? h('span', { class: 'dim' }, ' in change ') : null, where,
        is.line ? h('span', { class: 'wb-iline', title: 'Where the rule says it' }, `rule line ${is.line}`) : null),
      h('p', { class: 'wb-imsg' }, is.message),
      is.got ? h('p', { class: 'wb-igot' }, h('span', { class: 'k' }, 'the plan has'), h('code', null, is.got)) : null);
  }

  // ---- the document of a plan: what a rule sees ----

  docBlock(ref) {
    const open = this.docsOpen.has(ref);
    const box = h('div', { class: 'wb-doc' });
    const btn = h('button', { type: 'button', class: 'wb-docbtn', 'aria-expanded': open ? 'true' : 'false',
      on: { click: () => { if (this.docsOpen.has(ref)) this.docsOpen.delete(ref); else this.docsOpen.add(ref); this.paintResults(); } } },
      h('span', { class: 'chev' + (open ? ' open' : ''), 'aria-hidden': 'true' }, icon('chev')), 'The document this plan is judged by');
    box.append(btn);
    if (open) {
      const body = h('div', { class: 'wb-docbody' });
      box.append(body);
      this.fillDoc(ref, body);
    }
    return box;
  }

  openDoc(ref, n) {
    this.opened.add(ref);
    this.docsOpen.add(ref);
    const d = this.docs.get(ref) || {};
    d.sel = n >= 0 ? n : d.sel;
    this.docs.set(ref, d);
    this.paintResults();
  }

  fillDoc(ref, body) {
    let d = this.docs.get(ref);
    if (!d) { d = {}; this.docs.set(ref, d); }
    const want = d.sel === undefined ? -1 : d.sel;
    if (!d.outline || (want >= 0 && d.change === undefined) || d.loadedSel !== want) {
      body.append(h('p', { class: 'po-note' }, 'Reading the document…'));
      const mine = (d.req = {});
      api.workbenchDoc(ref, want >= 0 ? want : undefined).then((r) => {
        if (d.req !== mine) return;
        d.outline = r.outline; d.head = r.head; d.change = r.change; d.loadedSel = want; d.truncated = r.truncated; d.err = '';
        this.paintResults();
      }, (err) => {
        if (d.req !== mine) return;
        d.err = err && err.message ? err.message : 'the document could not be read';
        d.outline = d.outline || [];
        d.loadedSel = want;
        this.paintResults();
      });
      return;
    }
    if (d.err) body.append(h('p', { class: 'po-problem' }, icon('bang'), h('span', null, d.err)));
    const list = h('ol', { class: 'wb-outline', 'aria-label': 'Changes of the document, by index' },
      d.outline.map((c) => h('li', null, h('button', { type: 'button', 'aria-current': d.sel === c.index ? 'true' : 'false', on: { click: () => { d.sel = c.index; this.paintResults(); } } },
        h('i', { class: 'wb-ix' }, String(c.index)), h('span', { class: 'wb-act a-' + c.action }, c.action), h('span', { class: 'wb-addr' }, c.addr)))));
    const json = d.sel !== undefined && d.sel >= 0 && d.change ? d.change : d.head;
    const label = d.sel !== undefined && d.sel >= 0 && d.change ? `changes.${d.sel}` : 'the document, without its changes';
    body.append(h('div', { class: 'wb-docgrid' },
      h('div', null, h('h4', { class: 'po-h4' }, 'Changes', h('span', { class: 'po-n' }, String(d.outline.length))), d.outline.length ? list : h('p', { class: 'po-note' }, 'The plan has no change.')),
      h('div', null, h('h4', { class: 'po-h4' }, label), this.json(json || '', ''), d.truncated ? h('p', { class: 'po-note' }, 'The document is cut here.') : null,
        d.sel !== undefined && d.sel >= 0 ? h('button', { type: 'button', class: 'wb-link', on: { click: () => { d.sel = -1; this.paintResults(); } } }, 'show the rest of the document') : null)));
  }

  // json draws a document in the dark window, with the colours of CUE: JSON is CUE.
  json(text, label) {
    const lines = text.replace(/\n$/, '').split('\n');
    const toks = tokenize(lines);
    const body = h('div', { class: 'po-cbody' });
    lines.forEach((l, i) => {
      const code = h('span', { class: 'po-code' });
      for (const t of toks[i]) code.append(t.t ? h('span', { class: 'tk-' + t.t }, t.text) : t.text);
      if (!toks[i].length) code.append('​');
      body.append(h('div', { class: 'po-ln' }, h('i', { class: 'po-no' }, String(i + 1)), h('span', { class: 'po-sg', 'aria-hidden': 'true' }), code));
    });
    return h('div', { class: 'po-cw' }, h('div', { class: 'po-scroll', role: 'region', tabIndex: 0, 'aria-label': label || 'Document' }, body));
  }

  // ---- how it works ----

  paintGuide() {
    const d = this.data;
    const code = (text) => {
      const lines = text.replace(/\n$/, '').split('\n');
      const toks = tokenize(lines);
      const body = h('div', { class: 'po-cbody' });
      lines.forEach((l, i) => {
        const c = h('span', { class: 'po-code' });
        for (const t of toks[i]) c.append(t.t ? h('span', { class: 'tk-' + t.t }, t.text) : t.text);
        if (!toks[i].length) c.append('​');
        body.append(h('div', { class: 'po-ln' }, h('i', { class: 'po-no' }, String(i + 1)), h('span', { class: 'po-sg', 'aria-hidden': 'true' }), c));
      });
      return h('div', { class: 'po-cw' }, h('div', { class: 'po-scroll', role: 'region', tabIndex: 0, 'aria-label': 'Example' }, body));
    };
    const sec = (title, ...kids) => h('details', { class: 'wb-det wb-gsec' }, h('summary', null, h('span', { class: 'chev', 'aria-hidden': 'true' }, icon('chev')), title), h('div', { class: 'wb-gbody' }, kids));
    const p = (...kids) => h('p', { class: 'wb-gp' }, ...kids);
    clear(this.guideEl);
    this.guideEl.append(h('h3', { class: 'wb-h3', id: 'wb-gh' }, 'How it works'),
      sec('What a rule is, and when it is judged',
        p('A policy is a rule written in CUE. When a plan changes at least one resource, ', h('code', null, 'pravo apply'), ' builds the ', h('b', null, 'plan document'), ', unifies it with the rule of every policy in force, and refuses the plan, with exit code 5, unless the result is concrete. ', h('code', null, 'pravo plan'), ' judges the plan the same way, as feedback. A plan that changes no resource is not judged, and a change to a policy is not judged.'),
        p('Rules are not ordered and do not combine: each policy judges the whole plan on its own, and all of them have to hold. The signed start of the apply lists the versions that judged it.')),
      sec('The plan document',
        p('The document is the JSON of ', h('code', null, 'pravo plan --json'), ' without what only the state, the clock or the invocation can give: no blast radius, dependents or hints. A field that is not in it cannot be read by a rule. Open a plan above, then “The document this plan is judged by”, to see the real thing with the numbers of its changes. A path in a refusal such as ', h('code', null, 'changes.2.after.tags'), ' counts from the top of it.'),
        p('This is its schema, from the ', h('code', null, 'pravo'), ' package:'),
        code(d.schema || '')),
      sec('Strict by default',
        p('CUE accepts an incomplete value: a field that the data lacks does not conflict with a constraint. A plan rule does not. A plan satisfies it only when the document shows compliance, so a value that the rule requires and that is absent, null or unknown until apply is a violation.'),
        p('A ', h('b', null, 'guard'), ' picks the changes a requirement is for. Declare the fields the guard reads, because CUE scope is lexical, and do not forbid a value with ', h('code', null, '=='), ': an unknown value equals nothing, so the guard is false and the plan passes. Write the allowed set as a constraint instead.'),
        code('changes: [...{\n\ttype:   string\n\taction: string\n\tif type == "aws_s3_bucket" && action == "create" {\n\t\tafter: tags: ManagedBy: "pravo"\n\t}\n}]\n'),
        p(h('code', null, 'error("…")'), ' inside a guard puts your own words in the refusal. A limit binds one plan: twenty plans of five changes each pass ', h('code', null, 'summary: add: <=5'), '.')),
      sec('What the workbench runs, and what it leaves out',
        p('For each plan the server builds the document as the apply would: it decodes the values of the plan with the schemas of the providers that are cached, and runs your text, and the version in force if you compare, in a CUE context of its own. It then reports for each plan whether the rule holds and where it does not. It stores nothing and takes no lock.'),
        p('A plan that cannot be decoded is listed and not judged: its provider schema is not cached, the specification does not load, or the plan is sealed to a key this identity does not hold. A refusal quotes the values it names, and a value the viewer withholds elsewhere is withheld here. The document shows the plan as it was made, with the applied marks cleared.'),
        p('The check that a stored rule must pass is stricter than trying it: the text has to compile, import only the standard library, accept the document of an empty plan, and fit the shape of the schema above. The workbench tries a text that fails it and says what failed.')),
      sec('Mistakes that forbid nothing',
        p('A misspelt literal forbids nothing. A misspelt list template adds a field and forbids nothing. A misspelt field with a concrete value is a new field that holds. A misspelt field with a constraint is refused when the rule is added. Try the rule on a plan it must refuse, as well as on one it must pass; the tally above is the test.')));
  }
}
