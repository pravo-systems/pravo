// The policy workbench: write policies, see which plans they would refuse and why, before they are stored.
//
// The editor holds the text of a specification: the import of the pravo package and policies, each a pravo.#Policy with its rule.
// A rule is CUE that is unified with the document of a plan and has to make it concrete. The page sends the text with the
// plans to try it on (GET /api/workbench/try), and the server checks it as CUE and answers for each plan whether the
// policies hold, and where in the document they do not. The server runs the rule in a CUE context of its own and writes nothing: the
// state, the plans and the policies are as they were. The page also explains the plan document, which a rule is written
// against, and what the apply does with a rule.

import { h, clear, icon, plural, relTime, stamp, debounce, store } from './util.js';
import { tokenize } from './cue.js';
import { attachFolds, foldRanges, hiddenRows } from './fold.js';
import * as api from './api.js';
import { picker } from './picker.js';
import { complete } from './complete.js';

const DRAFT_KEY = 'pravo.workbench.policies';
const MAX_PLANS = 100;

const BLANK = `import "pravo.dev/pravo"

// A policy is a rule that every plan is judged by. The rule says what must hold of the plan's document.
// This one allows no destroy.
draft: pravo.#Policy & {
\trule: summary: destroy: 0
}
`;

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
  empty: 'Whenever a policy is added, its rule is tried on a plan that changes nothing, and it has to hold there. Yours does not: it demands a value that such a plan does not have, as listed below. Use a limit such as <=1 instead of an exact number, or demand the value only of the changes that exist, inside a list over changes. The plans on the right are judged as they are; this is only about storing the policy.',
  shape: 'The rule does not fit the plan document. It names a field that the document does not have, or a value outside the enum of a field.',
};

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
    this.text = BLANK;
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
      if (d && typeof d.text === 'string' && d.text.length < 32768) { this.text = d.text; }
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

  persist() { store.set(DRAFT_KEY, JSON.stringify({ text: this.text })); }

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
    api.tryRule({ rule: this.text, refs }, mine.signal).then((r) => {
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
          h('h3', { class: 'wb-h3', id: 'wb-eh' }, 'Policies'),
          this.controlsEl, this.editor.wrap, this.editorNotes()),
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
      h('p', { class: 'po-lede' }, 'Write policies as the specification declares them and see which plans they would refuse, and why, before they are stored. The plans are the ones stored in the state and the plan files of the project; each is judged by the policies exactly as an apply would judge it.')),
      h('p', { class: 'wb-promise' }, icon('lock'), h('span', null, 'Nothing is applied or stored. The policies are run in a scratch evaluator, and the project, the state and the policies in force stay as they were.')));
  }

  // ---- the controls over the editor ----

  paintControls() {
    const d = this.data;
    const groups = [{ label: null, items: [{ value: 'blank', label: 'A blank policy' }] }];
    if (d.examples.length) groups.push({ label: 'Examples', items: d.examples.map((e) => ({ value: 'ex:' + e.id, label: e.title })) });
    const inForce = d.policies.filter((p) => p.in_force), declared = d.policies.filter((p) => p.declared && p.declared !== p.in_force);
    if (inForce.length) groups.push({ label: 'Policies in force', items: inForce.map((p) => ({ value: 'policy:' + p.name, label: p.description || p.name, sub: p.description ? p.name : '' })) });
    if (declared.length) groups.push({ label: 'The specification’s text', items: declared.map((p) => ({ value: 'declared:' + p.name, label: p.description || p.name, sub: (p.description ? p.name + ' · ' : '') + 'declared, not yet stored' })) });
    clear(this.controlsEl);
    this.controlsEl.append(h('div', { class: 'wb-f' }, h('span', { class: 'k', id: 'wb-start-k' }, 'start from'), picker(groups, this.start, (v) => this.startFrom(v), 'wb-start-k')));
  }

  startFrom(v) {
    this.start = v;
    const d = this.data;
    const [kind, ...rest] = v.split(':');
    const key = rest.join(':');
    if (kind === 'blank') this.text = BLANK;
    else if (kind === 'ex') { const e = d.examples.find((x) => x.id === key); if (e) this.text = e.policy; }
    else if (kind === 'policy') { const p = d.policies.find((x) => x.name === key); if (p) this.text = p.in_force; }
    else if (kind === 'declared') { const p = d.policies.find((x) => x.name === key); if (p) this.text = p.declared; }
    this.persist();
    this.paintControls();
    this.syncEditor();
    this.run();
  }

  // ---- the editor: a text area over the same text drawn in colour ----

  buildEditor() {
    const ta = h('textarea', { class: 'wb-ta', spellcheck: 'false', autocomplete: 'off', autocapitalize: 'off', autocorrect: 'off', wrap: 'off', 'aria-label': 'The policies, in CUE', 'aria-describedby': 'wb-keys' });
    const hl = h('pre', { class: 'wb-hl', 'aria-hidden': 'true' });
    const gut = h('div', { class: 'wb-gut', 'aria-hidden': 'true' });
    const view = h('div', { class: 'wb-view' }, hl, ta);
    const wrap = h('div', { class: 'wb-ed' }, h('div', { class: 'wb-gutwrap' }, gut), view);
    this.folded = new Set();
    ta.addEventListener('input', (ev) => { this.text = ta.value; this.persist(); this.start = this.start.startsWith('blank') ? 'blank' : this.start; this.drawText(); this.tryLater(); this.suggest(ev.inputType); });
    ta.addEventListener('blur', () => this.closeSuggest());
    ta.addEventListener('click', () => this.closeSuggest());
    // A text with folds shows less than it holds, so an edit first unfolds everything, and then is made.
    ta.addEventListener('beforeinput', (ev) => {
      if (!this.folded.size) return;
      ev.preventDefault();
      this.unfoldKeepCaret();
      if (ev.inputType === 'insertText' && ev.data) this.insert(ev.data);
      else if (ev.inputType === 'insertLineBreak' || ev.inputType === 'insertParagraph') this.insert('\n');
    });
    ta.addEventListener('scroll', () => this.sync());
    ta.addEventListener('keydown', (ev) => this.key(ev));
    return { ta, hl, gut, wrap, view, marks: new Map(), ends: new Map(), vis: [], lines: [] };
  }

  // key makes a few keys useful in a rule: Tab indents and Enter keeps the indent. Escape leaves the editor, as it leaves any field of the page.
  // Ctrl or Cmd with Shift and [ folds the block at the cursor, and ] unfolds it.
  key(ev) {
    const ta = this.editor.ta;
    if (this.suggestKey(ev)) return;
    if ((ev.ctrlKey || ev.metaKey) && ev.shiftKey && !ev.altKey && (ev.code === 'BracketLeft' || ev.code === 'BracketRight')) {
      ev.preventDefault();
      const line = this.caretLine();
      if (ev.code === 'BracketRight') {
        for (const [i, e] of this.editor.ends) if (this.folded.has(i) && i <= line && line <= e) { this.toggleFold(i); break; }
      } else {
        let at = -1;
        for (const [i, e] of this.editor.ends) if (i <= line && line <= e && !this.folded.has(i) && i > at && !this.editor.hidden[i]) at = i;
        if (at >= 0) this.toggleFold(at);
      }
      return;
    }
    if (ev.key === 'Tab' && !ev.shiftKey && !ev.ctrlKey && !ev.metaKey && !ev.altKey) {
      ev.preventDefault();
      this.insert('\t');
    } else if (ev.key === 'Enter' && !ev.ctrlKey && !ev.metaKey && !ev.altKey && !ev.shiftKey) {
      ev.preventDefault();
      this.unfoldKeepCaret();
      const before = ta.value.slice(0, ta.selectionStart);
      const line = before.slice(before.lastIndexOf('\n') + 1);
      const indent = /^[\t ]*/.exec(line)[0];
      const opens = /[{[(]\s*$/.test(line);
      this.insert('\n' + indent + (opens ? '\t' : ''));
    }
  }

  // ---- completion: the names the plan document has at the cursor, offered as the person types ----

  // suggest opens the list after a word is typed or the name of a package is begun; Ctrl+Space asks for it anywhere.
  suggest(inputType, force = false) {
    const ta = this.editor.ta;
    this.closeSuggest();
    if (this.folded.size || ta.selectionStart !== ta.selectionEnd) return;
    if (!force && !/^insert(Text|Replacement)/.test(inputType || '')) return;
    const r = complete((this.data && this.data.fields) || [], ta.value, ta.selectionStart, force);
    if (!r || !r.items.length) return;
    this.sg = { r, at: 0, pop: h('div', { class: 'wb-sg', role: 'listbox', 'aria-label': 'Completions' }) };
    r.items.slice(0, 40).forEach((it, i) => this.sg.pop.append(h('div', { class: 'wb-sgi', role: 'option', on: { mousedown: (ev) => { ev.preventDefault(); this.sg.at = i; this.acceptSuggest(); }, mousemove: () => this.markSuggest(i) } },
      h('span', { class: 'wb-sgl' }, it.label), it.detail ? h('span', { class: 'wb-sgd' }, it.detail) : null)));
    this.sg.doc = h('div', { class: 'wb-sgdoc' });
    this.sg.pop.append(this.sg.doc);
    this.editor.wrap.append(this.sg.pop);
    this.placeSuggest();
    this.markSuggest(0);
  }

  placeSuggest() {
    const e = this.editor, ta = e.ta, pop = this.sg.pop;
    const cs = getComputedStyle(ta);
    const cv = this.measure || (this.measure = document.createElement('canvas').getContext('2d'));
    cv.font = `${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`;
    const cw = cv.measureText('0').width;
    const lh = parseFloat(cs.lineHeight) || parseFloat(cs.fontSize) * 1.55;
    const before = ta.value.slice(0, this.sg.r.from).split('\n');
    const line = before.length - 1;
    const col = before[line].replace(/\t/g, '    ').length;
    const w = e.wrap.getBoundingClientRect(), t = ta.getBoundingClientRect();
    const x = t.left - w.left + parseFloat(cs.paddingLeft) + col * cw - ta.scrollLeft;
    const y = t.top - w.top + parseFloat(cs.paddingTop) + (line + 1) * lh - ta.scrollTop;
    pop.style.left = Math.max(0, Math.min(x, w.width - 260)) + 'px';
    pop.style.top = y + 'px';
  }

  markSuggest(i) {
    const sg = this.sg;
    if (!sg) return;
    sg.at = i;
    const rows = sg.pop.querySelectorAll('.wb-sgi');
    rows.forEach((r, j) => r.classList.toggle('on', j === i));
    if (rows[i]) rows[i].scrollIntoView({ block: 'nearest' });
    const it = sg.r.items[i];
    sg.doc.textContent = (it && it.doc) || '';
    sg.doc.hidden = !(it && it.doc);
  }

  closeSuggest() {
    if (this.sg) { this.sg.pop.remove(); this.sg = null; }
  }

  acceptSuggest() {
    const { r, at } = this.sg;
    const it = r.items[at];
    const ta = this.editor.ta;
    this.closeSuggest();
    this.replace(r.from, r.to, it.insert);
    let caret = r.from + it.insert.length;
    if (it.cursorAt != null) caret = r.from + it.cursorAt;
    else if (it.cursorBack) caret -= it.cursorBack;
    ta.setSelectionRange(caret, caret);
    ta.dispatchEvent(new Event('input', { bubbles: true }));
  }

  // suggestKey handles the keys of the open list, and Ctrl+Space, which opens it. It tells whether it took the key.
  suggestKey(ev) {
    if (ev.key === ' ' && ev.ctrlKey && !ev.metaKey && !ev.altKey) { ev.preventDefault(); this.suggest('', true); return true; }
    const sg = this.sg;
    if (!sg) return false;
    const n = Math.min(sg.r.items.length, 40);
    if (ev.ctrlKey || ev.metaKey || ev.altKey) return false;
    if (ev.key === 'ArrowDown') this.markSuggest((sg.at + 1) % n);
    else if (ev.key === 'ArrowUp') this.markSuggest((sg.at + n - 1) % n);
    else if (ev.key === 'Enter' || ev.key === 'Tab') this.acceptSuggest();
    else if (ev.key === 'Escape') { this.closeSuggest(); ev.stopPropagation(); } else return false;
    ev.preventDefault();
    return true;
  }

  // origOffset is the place in the text of a place in what the editor shows, which leaves out the folded lines.
  origOffset(v) {
    const e = this.editor;
    const starts = [];
    let n = 0;
    for (const l of e.lines) { starts.push(n); n += l.length + 1; }
    let pos = 0;
    for (let k = 0; k < e.vis.length; k++) {
      const len = e.vis[k].text.length;
      if (v <= pos + len || k === e.vis.length - 1) return starts[e.vis[k].i] + Math.min(Math.max(0, v - pos), e.lines[e.vis[k].i].length);
      pos += len + 1;
    }
    return 0;
  }

  // caretLine is the index of the line of the text that the cursor is on.
  caretLine() {
    const at = this.folded.size ? this.origOffset(this.editor.ta.selectionStart) : this.editor.ta.selectionStart;
    let n = 0;
    for (let i = 0; i < at; i++) if (this.text.charCodeAt(i) === 10) n++;
    return n;
  }

  unfoldKeepCaret() {
    if (!this.folded.size) return;
    const ta = this.editor.ta;
    const at = this.origOffset(ta.selectionStart), to = this.origOffset(ta.selectionEnd);
    this.folded.clear();
    this.drawText();
    ta.setSelectionRange(at, to);
  }

  toggleFold(i) {
    const ta = this.editor.ta;
    if (this.folded.has(i)) this.folded.delete(i); else this.folded.add(i);
    this.drawText();
    const k = this.editor.vis.findIndex((v) => v.i === i);
    let at = 0;
    for (let j = 0; j < k; j++) at += this.editor.vis[j].text.length + 1;
    ta.setSelectionRange(at, at + (k >= 0 ? this.editor.lines[i].length : 0));
    ta.focus({ preventScroll: true });
  }

  insert(s) {
    const ta = this.editor.ta;
    this.unfoldKeepCaret();
    this.replace(ta.selectionStart, ta.selectionEnd, s);
  }

  // replace writes text over a range through the browser's own edit command, so that undo and redo keep working across it. A browser
  // without the command gets the plain write, which cannot be undone.
  replace(from, to, s) {
    const ta = this.editor.ta;
    ta.focus({ preventScroll: true });
    ta.setSelectionRange(from, to);
    if (document.execCommand && document.execCommand('insertText', false, s)) return;
    ta.setRangeText(s, from, to, 'end');
    ta.dispatchEvent(new Event('input', { bubbles: true }));
  }

  syncEditor() {
    this.folded.clear();
    this.drawText();
  }

  // drawText colours the text and sizes the window to it. The lines inside a folded block are left out, and the line that opens it ends in a mark.
  drawText() {
    const e = this.editor;
    const lines = this.text.replace(/\n$/, '').split('\n');
    const toks = tokenize(lines);
    const ends = foldRanges(toks);
    for (const i of Array.from(this.folded)) if (!ends.has(i)) this.folded.delete(i);
    const hidden = hiddenRows(lines.length, ends, this.folded);
    Object.assign(e, { ends, lines, hidden, vis: [] });
    clear(e.hl);
    clear(e.gut);
    lines.forEach((l, i) => {
      if (hidden[i]) return;
      const folded = this.folded.has(i);
      const mark = folded ? ` ⋯ ${plural(ends.get(i) - i, 'line')}` : '';
      e.vis.push({ i, text: l + mark });
      const row = h('div', { class: 'wb-ln', 'data-no': String(i + 1) });
      for (const t of toks[i]) row.append(t.t ? h('span', { class: 'tk-' + t.t }, t.text) : t.text);
      if (!toks[i].length && !mark) row.append('\u200b');
      if (mark) row.append(h('span', { class: 'tk-fold' }, mark));
      e.hl.append(row);
      const no = h('i', { class: 'wb-no', 'data-no': String(i + 1) }, String(i + 1));
      if (ends.has(i)) no.append(h('button', { type: 'button', class: 'fold', tabindex: '-1', 'aria-expanded': folded ? 'false' : 'true', 'data-tip': folded ? 'Unfold' : 'Fold', on: { click: () => this.toggleFold(i) } }, icon('chev')));
      e.gut.append(no);
    });
    const shown = this.folded.size ? e.vis.map((v) => v.text).join('\n') : this.text;
    if (e.ta.value !== shown) e.ta.value = shown;
    e.wrap.style.setProperty('--wb-lines', String(Math.min(30, Math.max(12, e.vis.length + 1))));
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
    // a mark on a line that a fold hides is shown on the line that opens the fold
    for (const [i, end] of e.ends) {
      if (!this.folded.has(i) || e.hidden[i]) continue;
      for (let j = i + 2; j <= end + 1; j++) {
        if (bad.has(j) && !bad.has(i + 1)) bad.set(i + 1, bad.get(j));
        if (this.hot.has(j)) this.hot.set(i + 1, (this.hot.get(i + 1) || 0) + this.hot.get(j));
      }
    }
    Array.from(e.hl.children).forEach((row) => {
      const n = Number(row.dataset.no);
      row.classList.toggle('bad', bad.has(n));
      row.classList.toggle('hot', !bad.has(n) && this.hot.has(n));
    });
    Array.from(e.gut.children).forEach((g) => {
      const n = Number(g.dataset.no);
      g.classList.toggle('bad', bad.has(n));
      g.classList.toggle('hot', !bad.has(n) && this.hot.has(n));
      if (bad.has(n)) g.title = bad.get(n);
      else if (this.hot.has(n)) g.title = `Where ${plural(this.hot.get(n), 'refusal')} point into the rule`;
      else g.removeAttribute('title');
    });
  }

  editorNotes() {
    return h('p', { class: 'wb-keys', id: 'wb-keys' }, h('kbd', null, 'Ctrl+Space'), ' lists what the plan document has at the cursor; the list also opens as you type. ', h('kbd', null, 'Tab'), ' indents (or takes the choice in the list) and ', h('kbd', null, 'Esc'), ' leaves the editor. ', h('kbd', null, 'Ctrl+Shift+['), ' folds the block at the cursor and ', h('kbd', null, 'Ctrl+Shift+]'), ' unfolds it (', h('kbd', null, 'Cmd'), ' on a Mac). A red line is where CUE found a problem with the text; an amber one is where a refusal points into the rule.');
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
    const issues = (c.issues || []).filter((is) => is.message);
    return h('div', { class: 'wb-check' },
      h('p', { class: 'wb-ct' }, icon('bang'), h('span', null, h('b', null, 'This text cannot be stored as a policy yet. '), STAGE[c.stage] || '')),
      issues.length ? h('ul', { class: 'wb-cissues' }, issues.slice(0, 8).map((is) => h('li', null,
        is.line ? h('span', { class: 'wb-iline' }, `line ${is.line}`) : null,
        is.policy && this.policyCount() > 1 ? h('span', { class: 'wb-ipol' }, is.policy) : null,
        is.path ? h('code', null, is.path) : null,
        h('span', { class: 'wb-cmsg' }, is.message)))) : null,
      h('details', { class: 'wb-raw' }, h('summary', null, 'What CUE says, in full'), h('pre', null, c.message)));
  }

  planRow(tp, r) {
    const label = this.labelOf(tp.ref);
    const meta = this.metaOf(tp.ref);
    const res = tp.draft;
    const key = res ? res.outcome : tp.state;
    const out = OUTCOME[key] || OUTCOME.unreadable;
    const open = this.opened.has(tp.ref);
    const refusal = res && res.outcome === 'refused';
    const body = h('div', { class: 'wb-rbody', hidden: !open });
    if (open) body.append(...this.rowBody(tp, res, meta));
    const head = h('button', { type: 'button', class: 'wb-row mk-' + out.mark, 'aria-expanded': open ? 'true' : 'false', 'data-fk': 'wb:' + tp.ref,
      on: { click: () => { if (this.opened.has(tp.ref)) this.opened.delete(tp.ref); else this.opened.add(tp.ref); this.paintResults(); } } },
      h('i', { class: 'po-mark', 'aria-hidden': 'true' }),
      h('span', { class: 'wb-rn' }, h('b', null, label), meta ? h('span', { class: 'wb-rmeta' }, this.metaText(meta)) : null),
      h('span', { class: 'wb-ro ' + out.cls }, out.label, refusal ? ` · ${plural((res.issues || []).length + (res.more || 0), 'reason')}` : ''),
      h('span', { class: 'chev' + (open ? ' open' : ''), 'aria-hidden': 'true' }, icon('chev')));
    return h('li', { class: 'wb-item' }, head, body);
  }

  policyCount() { return this.result && this.result.policies ? this.result.policies.length : 1; }

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

  rowBody(tp, res, meta) {
    const out = [];
    if (tp.note) out.push(h('p', { class: 'po-note' }, tp.note));
    if (res && res.outcome === 'holds') out.push(h('p', { class: 'po-note' }, `${this.policyCount() > 1 ? 'Every policy holds' : 'The policy holds'}: the plan’s document of ${plural(tp.changes, 'change')} satisfies ${this.policyCount() > 1 ? 'them' : 'it'} and the result is concrete.`));
    if (res && res.outcome === 'invalid') out.push(h('p', { class: 'po-note' }, 'The text does not check, so it says nothing of this plan. The problem is above the plans.'));
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
        is.policy && this.policyCount() > 1 ? h('span', { class: 'wb-ipol' }, is.policy) : null,
        is.line ? h('span', { class: 'wb-iline', title: 'Where the policies say it' }, `line ${is.line}`) : null),
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
    if (n >= 0) d.focus = true;
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
      d.outline.map((c) => h('li', null, h('button', { type: 'button', 'aria-current': d.sel === c.index ? 'true' : 'false', on: { click: () => { d.sel = c.index; d.focus = true; this.paintResults(); } } },
        h('i', { class: 'wb-ix' }, String(c.index)), h('span', { class: 'wb-act a-' + c.action }, c.action), h('span', { class: 'wb-addr' }, c.addr)))));
    const json = d.sel !== undefined && d.sel >= 0 && d.change ? d.change : d.head;
    const label = d.sel !== undefined && d.sel >= 0 && d.change ? `changes.${d.sel}` : 'the document, without its changes';
    const view = this.json(json || '', '');
    if (d.focus && d.sel !== undefined && d.sel >= 0 && d.change && d.loadedSel === d.sel) {
      // a change was picked: the code is what the person came for, so it takes the focus and the view
      d.focus = false;
      const scroller = view.querySelector('.po-scroll');
      requestAnimationFrame(() => { if (!scroller.isConnected) return; scroller.focus({ preventScroll: true }); scroller.scrollIntoView({ block: 'nearest', behavior: 'smooth' }); });
    }
    body.append(h('div', { class: 'wb-docgrid' },
      h('div', null, h('h4', { class: 'po-h4' }, 'Changes', h('span', { class: 'po-n' }, String(d.outline.length))), d.outline.length ? list : h('p', { class: 'po-note' }, 'The plan has no change.')),
      h('div', null, h('h4', { class: 'po-h4' }, label), view, d.truncated ? h('p', { class: 'po-note' }, 'The document is cut here.') : null,
        d.sel !== undefined && d.sel >= 0 ? h('button', { type: 'button', class: 'wb-link', on: { click: () => { d.sel = -1; this.paintResults(); } } }, 'show the rest of the document') : null)));
  }

  // json draws a document in the dark window, with the colours of CUE: JSON is CUE.
  json(text, label) {
    const lines = text.replace(/\n$/, '').split('\n');
    const toks = tokenize(lines);
    const body = h('div', { class: 'po-cbody' });
    const parts = [];
    lines.forEach((l, i) => {
      const code = h('span', { class: 'po-code' });
      for (const t of toks[i]) code.append(t.t ? h('span', { class: 'tk-' + t.t }, t.text) : t.text);
      if (!toks[i].length) code.append('\u200b');
      const sg = h('span', { class: 'po-sg' });
      const row = h('div', { class: 'po-ln' }, h('i', { class: 'po-no' }, String(i + 1)), sg, code);
      body.append(row);
      parts.push({ row, host: sg, code });
    });
    attachFolds(parts, toks);
    return h('div', { class: 'po-cw' }, h('div', { class: 'po-scroll', role: 'region', tabIndex: 0, 'aria-label': label || 'Document' }, body));
  }

  // ---- how it works ----

  paintGuide() {
    const d = this.data;
    const code = (text) => this.json(text, 'Example');
    const sec = (title, ...kids) => h('details', { class: 'wb-det wb-gsec' }, h('summary', null, h('span', { class: 'chev', 'aria-hidden': 'true' }, icon('chev')), title), h('div', { class: 'wb-gbody' }, kids));
    const p = (...kids) => h('p', { class: 'wb-gp' }, ...kids);
    clear(this.guideEl);
    this.guideEl.append(h('h3', { class: 'wb-h3', id: 'wb-gh' }, 'How it works'),
      sec('What you write',
        p('The editor holds what a specification holds: the import of the ', h('code', null, 'pravo'), ' package and any number of policies, each a field that is a ', h('code', null, 'pravo.#Policy'), ' with its ', h('code', null, 'rule'), '. A policy at a path is written as it is in a specification, such as ', h('code', null, 'cost: max_instances: pravo.#Policy & {…}'), '. The text is checked as CUE against the package: a field that the plan document does not have, a value of the wrong type or a rule that does not compile is an error at its line, before any plan is looked at.'),
        p('Every policy of the text is tried on every plan, and a refusal names the policy. The policies in force are not touched: a policy of the same name as one in force is a draft of it.')),
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
        p('For each plan the server builds the document as the apply would: it decodes the values of the plan with the schemas of the providers that are cached, and runs your text in a CUE context of its own. It checks the file as CUE against the pravo package, so a misspelt field or a value of the wrong type is an error at its line, and then reports for each plan whether each policy holds and where it does not. It stores nothing and takes no lock.'),
        p('A plan that cannot be decoded is listed and not judged: its provider schema is not cached, the specification does not load, or the plan is sealed to a key this identity does not hold. A refusal quotes the values it names, and a value the viewer withholds elsewhere is withheld here. The document shows the plan as it was made, with the applied marks cleared.'),
        p('The check that a stored rule must pass is stricter than trying it: the text has to compile, import only the standard library, accept the document of an empty plan, and fit the shape of the schema above. The workbench tries a text that fails it and says what failed.')),
      sec('Mistakes that forbid nothing',
        p('A misspelt literal forbids nothing. A misspelt list template adds a field and forbids nothing. A misspelt field with a concrete value is a new field that holds. A misspelt field with a constraint is refused when the rule is added. Try the rule on a plan it must refuse, as well as on one it must pass; the tally above is the test.')));
  }
}
