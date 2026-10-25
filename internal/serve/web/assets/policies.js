// The Policies view: the rules that every plan is judged by, as the state keeps them.
//
// A policy is a rule written in CUE. The state holds each one as numbered, signed versions that are never changed, and
// the latest version of a name is the one in force. The specification declares the rules it wants; where its text
// differs from the state, the next apply writes a new version, and the page says so and shows the difference. Each
// version lists the transactions that it judged, which is the receipt that a plan was held to it.
//
// The page reads /api/policies and shows it. Nothing here runs a command, writes a file or asks the server to.

import { h, clear, icon, plural, relTime, stamp, fmt, signatureMark } from './util.js';
import { tokenize } from './cue.js';
import { attachFolds } from './fold.js';
import { WorkbenchView } from './workbench.js';

// STATUS says what a policy is to the next apply, in words and in the one colour that means a decision is waiting.
const STATUS = {
  'in-force': { cls: 'ok', label: 'in force', mark: 'on' },
  pending: { cls: 'chg', label: 'pending', mark: 'wait' },
  new: { cls: 'chg', label: 'new', mark: 'wait' },
  removing: { cls: 'chg', label: 'removing', mark: 'wait' },
  removed: { cls: '', label: 'removed', mark: 'off' },
};

export const FOOTER = 'This page shows the policies. It runs nothing and writes nothing.';

// diffLines compares two texts line by line and returns rows { op: '+', '-' or ' ', side: 'a' or 'b', i: index of the line }.
// A text too long to compare is shown whole as added.
export function diffLines(a, b) {
  const x = a === '' ? [] : a.split('\n'), y = b === '' ? [] : b.split('\n');
  const n = x.length, m = y.length;
  if (n * m > 400000) return y.map((_, i) => ({ op: '+', side: 'b', i }));
  const t = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--) t[i][j] = x[i] === y[j] ? t[i + 1][j + 1] + 1 : Math.max(t[i + 1][j], t[i][j + 1]);
  const rows = [];
  let i = 0, j = 0;
  while (i < n && j < m) {
    if (x[i] === y[j]) { rows.push({ op: ' ', side: 'b', i: j }); i++; j++; }
    else if (t[i + 1][j] >= t[i][j + 1]) { rows.push({ op: '-', side: 'a', i }); i++; }
    else { rows.push({ op: '+', side: 'b', i: j }); j++; }
  }
  while (i < n) rows.push({ op: '-', side: 'a', i: i++ });
  while (j < m) rows.push({ op: '+', side: 'b', i: j++ });
  return rows;
}

// trim keeps the changed rows with two rows around each, and says how many it left out.
function trim(rows, ctx = 2) {
  const keep = rows.map(() => false);
  rows.forEach((r, k) => { if (r.op !== ' ') for (let d = -ctx; d <= ctx; d++) if (rows[k + d]) keep[k + d] = true; });
  const out = [];
  let skipped = 0;
  rows.forEach((r, k) => {
    if (keep[k]) { if (skipped) { out.push({ op: '…', n: skipped }); skipped = 0; } out.push(r); } else skipped++;
  });
  if (skipped) out.push({ op: '…', n: skipped });
  return out;
}

const short = (digest) => String(digest || '').slice(0, 12);

export class PoliciesView {
  constructor(root, cb = {}) {
    this.root = root;
    this.cb = cb;
    this.doc = null;
    this.sel = '';
    this.want = '';
    this.opened = new Map(); // "name@seq" -> the version's text or changes are open, as the person left it
    this.mode = 'register'; // or 'bench': the workbench, which tries a rule on plans
    this.bench = new WorkbenchView(root, cb);
  }

  route() { return { pol: this.mode === 'register' ? this.sel : '', pw: this.mode === 'bench' ? '1' : '' }; }

  // setMode switches between the register of policies and the workbench.
  setMode(mode) {
    if (this.mode === mode) return;
    this.mode = mode;
    if (mode === 'register') this.bench.close();
    this.render({ policies: this.doc });
    if (this.cb.onRoute) this.cb.onRoute();
  }

  toggleBench() { this.setMode(this.mode === 'bench' ? 'register' : 'bench'); }

  // tryPolicy opens the workbench on a rule of the register: the text in force, or the text the specification declares.
  tryPolicy(name, which) {
    this.mode = 'bench';
    this.bench.tryPolicy(name, which);
    this.render({ policies: this.doc });
    if (this.cb.onRoute) this.cb.onRoute();
  }

  // modes is the switch between the two pages of the section.
  modes() {
    const b = (mode, label, key) => h('button', { type: 'button', 'aria-pressed': this.mode === mode ? 'true' : 'false', 'data-fk': 'mode:' + mode, 'aria-keyshortcuts': key, on: { click: () => this.setMode(mode) } }, label);
    return h('div', { class: 'seg po-modes', role: 'group', 'aria-label': 'Page of the section' }, b('register', 'Register', 'w'), b('bench', 'Workbench', 'w'));
  }

  // open selects a policy, for a link from another part of the page; the next render shows it
  open(name) {
    this.sel = name;
    if (this.doc) this.render({ policies: this.doc });
  }

  setRoute(f) {
    this.want = f.pol || '';
    if (this.want) this.sel = this.want;
    this.mode = f.pw === '1' ? 'bench' : 'register';
  }

  render({ policies }) {
    const focus = document.activeElement && this.root.contains(document.activeElement) ? document.activeElement.dataset.fk : null;
    const top = this.root.scrollTop;
    if (this.mode === 'bench') {
      this.doc = policies || this.doc;
      clear(this.root);
      this.root.append(h('div', { class: 'po-bar' }, this.modes()));
      this.root.append(this.bench.mount());
      this.bench.show(policies);
      this.root.scrollTop = top;
      return;
    }
    this.bench.close();
    clear(this.root);
    if (!policies) {
      this.doc = null;
      this.root.append(h('div', { class: 'po' }, h('h2', { class: 'po-title' }, 'Policies'), h('p', { class: 'po-empty' }, 'The policies could not be read. The rest of the page is unaffected.')));
      return;
    }
    this.doc = policies;
    const list = policies.policies;
    if (!list.some((p) => p.name === this.sel)) this.sel = list.length ? (list.find((p) => p.status !== 'removed') || list[0]).name : '';
    const cur = list.find((p) => p.name === this.sel) || null;
    this.root.append(h('div', { class: 'po' },
      h('div', { class: 'po-bar' }, this.modes()),
      this.head(policies),
      list.length ? h('div', { class: 'po-body' }, this.register(list), cur ? this.dossier(cur) : null) : this.none(policies),
      h('p', { class: 'po-foot' }, FOOTER)));
    this.root.scrollTop = top;
    if (focus) {
      const el = Array.from(this.root.querySelectorAll('[data-fk]')).find((x) => x.dataset.fk === focus);
      if (el) el.focus({ preventScroll: true });
    }
  }

  // head is the title block: what the page is, and four counts that go together.
  head(d) {
    const versions = d.policies.reduce((n, p) => n + p.versions.length, 0);
    const judged = d.policies.reduce((n, p) => n + p.judged, 0);
    const cell = (k, v, cls, tip) => h('li', { class: cls || '', title: tip || null }, h('span', { class: 'k' }, k), h('b', null, fmt(v)));
    const notes = [];
    if (d.problem) notes.push(h('p', { class: 'po-problem' }, icon('bang'), h('span', null, 'The versions could not be read as trusted: ' + d.problem)));
    if (!d.spec_ok) notes.push(h('p', { class: 'po-warn' }, icon('bang'), h('span', null, 'The specification does not load, so the policies in the state cannot be compared with it.')));
    if (!d.checked && d.policies.length) notes.push(h('p', { class: 'po-warn' }, icon('bang'), h('span', null, 'There is no readers file to check the signatures against: the versions are shown, and none is vouched for.')));
    return h('header', { class: 'po-head' + (d.pending > 0 ? ' loud' : '') },
      h('div', { class: 'po-htext' },
        h('h2', { class: 'po-title', id: 'po-h' }, 'Policies'),
        h('p', { class: 'po-lede' }, d.policies.length
          ? 'Every plan that changes a resource is held to these rules when it is applied. The state keeps each rule as signed, numbered versions, and a transaction names the versions that judged it.'
          : 'A policy is a rule, written in CUE, that every plan must satisfy before it is applied. None is declared or stored.')),
      d.policies.length ? h('ul', { class: 'po-tally', 'aria-label': 'Counts' },
        cell('in force', d.in_force, 'ok', 'Policies whose latest version is a rule'),
        cell('pending', d.pending, d.pending > 0 ? 'chg' : '', 'Policies that the next apply adds, changes or removes'),
        cell('versions', versions, '', 'Versions in the state, removals included'),
        cell('judged', judged, '', 'Judgements that these versions made: one for each transaction that lists one')) : null,
      notes.length ? h('div', { class: 'po-notes' }, notes) : null);
  }

  none(d) {
    const sample = 'no_destroy: pravo.#Policy & {\n\trule: {\n\t\tchanges: [...{\n\t\t\taddr:   string\n\t\t\tmode:   string\n\t\t\taction: string\n\t\t\tif mode == "managed" && (action == "delete" || action == "replace") {\n\t\t\t\terror("\\(addr): \\(action) is not allowed")\n\t\t\t}\n\t\t}]\n\t}\n}\n';
    return h('section', { class: 'po-none' },
      h('p', null, 'Write one in the specification and apply it. It is stored as version 1, signed, and every later plan is judged by it:'),
      this.code(sample.split('\n').slice(0, -1).map((text, i) => ({ op: ' ', text, no: i + 1 })), { copy: sample, label: 'Copy the example' }),
      h('p', { class: 'po-note' }, 'A state must be sealed to hold policies: each version is signed by whoever writes it.'));
  }

  // register lists the policies, one row each, as a column of squares that say what each is to the next apply.
  register(list) {
    const rows = list.map((p) => {
      const st = STATUS[p.status] || STATUS['in-force'];
      return h('li', null, h('button', { type: 'button', class: 'po-row mk-' + st.mark, 'aria-current': p.name === this.sel ? 'true' : 'false', 'data-fk': 'row:' + p.name,
        on: { click: () => this.select(p.name) } },
        h('i', { class: 'po-mark', 'aria-hidden': 'true' }),
        h('span', { class: 'po-rname' }, p.name),
        h('span', { class: 'po-rmeta' },
          h('span', { class: 'po-rstat ' + st.cls }, st.label),
          p.head ? h('span', null, 'v' + p.head) : h('span', null, 'no version'),
          p.judged ? h('span', { title: 'Transactions judged by any version' }, plural(p.judged, 'run')) : null)));
    });
    return h('nav', { class: 'po-reg', 'aria-label': 'Policies' }, h('h3', { class: 'po-h3' }, 'Register', h('span', { class: 'po-n' }, String(list.length))), h('ul', null, rows));
  }

  select(name) {
    if (this.sel === name) return;
    this.sel = name;
    this.render({ policies: this.doc });
    if (this.cb.onRoute) this.cb.onRoute();
  }

  // dossier is one policy: what it is to the next apply, the rule in force, and its versions.
  dossier(p) {
    const st = STATUS[p.status] || STATUS['in-force'];
    const head = p.versions[0];
    const fact = (k, ...v) => h('li', null, h('span', { class: 'k' }, k), ...v);
    return h('section', { class: 'po-doc', 'aria-labelledby': 'po-dn' },
      h('div', { class: 'po-dhead' },
        h('h3', { class: 'po-dname', id: 'po-dn' }, p.name),
        h('span', { class: 'b ' + st.cls }, st.label),
        p.source ? h('code', { class: 'po-src', title: 'Where the specification writes this policy' }, `${p.source.file}:${p.source.line}`) : null),
      h('ul', { class: 'po-facts' },
        fact('version', h('b', null, p.head ? 'v' + p.head : 'none')),
        fact('digest', head ? h('code', { title: head.digest }, short(head.digest)) : h('b', null, '–')),
        fact('signed', head ? this.sig(head) : h('b', null, '–')),
        fact('written', head ? h('b', { title: stamp(head.at) }, relTime(head.at)) : h('b', null, '–')),
        fact('judged', h('b', null, p.judged ? plural(p.judged, 'run') : 'none yet'))),
      this.pending(p),
      this.rule(p),
      this.history(p));
  }

  sig(v) {
    if (!v.signature) return h('b', null, 'not checked');
    return signatureMark(v.signature, v.signer);
  }

  // pending says what the next apply does to this policy, and shows the change.
  pending(p) {
    const head = p.versions[0];
    let text = '', diff = null, tryIt = null;
    switch (p.status) {
      case 'pending':
        text = `The specification declares other text than version ${p.head}. The next apply writes version ${p.head + 1}.`;
        diff = this.diff(head.text, p.rule, `v${p.head}`, 'specification');
        tryIt = h('button', { type: 'button', class: 'po-try', on: { click: () => this.tryPolicy(p.name, 'declared') } }, 'Try the new text on plans');
        break;
      case 'new':
        text = p.head
          ? `Version ${p.head} removed this policy and the specification declares it again. The next apply writes version ${p.head + 1}.`
          : 'The specification declares this policy and the state has no version of it. The first apply writes version 1.';
        tryIt = h('button', { type: 'button', class: 'po-try', on: { click: () => this.tryPolicy(p.name, 'declared') } }, 'Try the rule on plans');
        break;
      case 'removing':
        text = `The specification no longer declares this policy. The next apply writes a removal as version ${p.head + 1}, and the rule judges nothing after it.`;
        break;
      default:
        return null;
    }
    return h('div', { class: 'po-pend' },
      h('p', { class: 'po-pt' }, icon('hold'), h('span', null, text)),
      diff,
      tryIt,
      h('div', { class: 'cmdline po-cmd' }, h('span', { class: 'ps', 'aria-hidden': 'true' }, '$'), h('code', null, this.doc.command),
        h('button', { class: 'cpbtn', type: 'button', 'data-copy': this.doc.command, 'aria-label': `Copy the command: ${this.doc.command}`, 'data-tip': 'Copy the command' }, icon('copy'))));
  }

  // rule is the text in force, or the text the specification declares when nothing is in force yet.
  rule(p) {
    const head = p.versions[0];
    const inForce = head && !head.removal ? head : null;
    const text = inForce ? inForce.text : p.status === 'new' ? p.rule : '';
    if (!text) {
      return h('div', { class: 'po-rulebox' }, h('h4', { class: 'po-h4' }, 'Rule'), h('p', { class: 'po-note' }, 'Nothing is in force: this policy judges no plan.'));
    }
    const lines = text.replace(/\n$/, '').split('\n').map((t, i) => ({ op: ' ', text: t, no: i + 1 }));
    const declared = !inForce && p.status === 'new';
    return h('div', { class: 'po-rulebox' },
      h('h4', { class: 'po-h4' }, inForce ? `Rule in force · v${inForce.seq}` : 'Rule declared', h('span', { class: 'po-n' }, plural(lines.length, 'line')),
        h('button', { type: 'button', class: 'po-try', 'data-tip': 'Try this rule on stored plans and plan files', on: { click: () => this.tryPolicy(p.name, declared ? 'declared' : 'policy') } }, 'Try on plans')),
      this.code(lines, { copy: text, label: 'Copy the rule' }));
  }

  // history is the versions as a ledger on a rail, newest first. A version opens to show what it changed, and the transactions it judged.
  history(p) {
    if (!p.versions.length) return null;
    const items = p.versions.map((v, k) => {
      const prev = p.versions[k + 1] || null;
      const key = `${p.name}@${v.seq}`;
      const open = this.opened.has(key) ? this.opened.get(key) : false;
      const body = h('div', { class: 'po-vbody', hidden: !open });
      if (open) body.append(...this.versionBody(v, prev));
      const mk = v.removal ? 'rm' : k === 0 ? 'cur' : 'old';
      return h('li', { class: 'po-ver mk-' + mk },
        h('i', { class: 'po-node', 'aria-hidden': 'true' }),
        h('button', { type: 'button', class: 'po-vhead', 'aria-expanded': open ? 'true' : 'false', 'data-fk': 'ver:' + key,
          on: { click: () => { this.opened.set(key, !open); this.render({ policies: this.doc }); } } },
          h('span', { class: 'chev' + (open ? ' open' : ''), 'aria-hidden': 'true' }, icon('chev')),
          h('b', { class: 'po-vn' }, 'v' + v.seq),
          h('span', { class: 'po-vwhat' }, v.removal ? 'removal' : prev ? this.changeCount(v, prev) : 'first version'),
          h('code', { class: 'po-vd', title: v.ref }, short(v.digest)),
          this.sig(v),
          h('span', { class: 'po-vt', title: stamp(v.at) }, relTime(v.at)),
          v.judged ? h('span', { class: 'po-vj', title: 'Transactions that this version judged' }, plural(v.judged, 'run')) : null),
        body);
    });
    return h('div', { class: 'po-hist' },
      h('h4', { class: 'po-h4' }, 'Versions', h('span', { class: 'po-n' }, String(p.versions.length))),
      h('ol', { class: 'po-vers' }, items));
  }

  changeCount(v, prev) {
    if (prev.removal) return 'written again';
    const rows = diffLines(prev.text, v.text);
    const add = rows.filter((r) => r.op === '+').length, del = rows.filter((r) => r.op === '-').length;
    return `+${add} −${del}`;
  }

  versionBody(v, prev) {
    const out = [];
    if (v.removal) out.push(h('p', { class: 'po-note' }, 'This version removes the policy. It has no text.'));
    else if (prev && !prev.removal) out.push(this.diff(prev.text, v.text, 'v' + prev.seq, 'v' + v.seq));
    else out.push(this.code(v.text.replace(/\n$/, '').split('\n').map((t, i) => ({ op: ' ', text: t, no: i + 1 })), { copy: v.text, label: 'Copy the rule' }));
    out.push(h('p', { class: 'po-ref' }, h('span', { class: 'k' }, 'ref'), h('code', null, v.ref),
      h('button', { class: 'cpbtn', type: 'button', 'data-copy': v.ref, 'aria-label': 'Copy the reference of the version', 'data-tip': 'Copy the reference' }, icon('copy'))));
    if (v.runs && v.runs.length) {
      out.push(h('div', { class: 'po-runs' }, h('span', { class: 'k' }, 'judged'),
        h('ul', null, v.runs.map((r) => h('li', { title: stamp(r.time) }, h('code', null, r.id), h('span', null, r.kind), h('span', { class: 'rs rs-' + r.status }, r.status), h('span', { class: 'dim' }, relTime(r.time)))),
          v.judged > v.runs.length ? h('li', { class: 'dim' }, `and ${v.judged - v.runs.length} more`) : null)));
    } else out.push(h('p', { class: 'po-note' }, 'No transaction lists this version.'));
    return out;
  }

  // diff is the change from one text to another, with the lines around it.
  diff(from, to, fromLabel, toLabel) {
    const a = (from || '').replace(/\n$/, ''), b = (to || '').replace(/\n$/, '');
    const rows = trim(diffLines(a, b));
    const ta = tokenize(a === '' ? [] : a.split('\n')), tb = tokenize(b === '' ? [] : b.split('\n'));
    const lines = rows.map((r) => (r.op === '…' ? r : { op: r.op, toks: (r.side === 'a' ? ta : tb)[r.i], no: r.i + 1 }));
    const wrap = this.code(lines, {});
    wrap.prepend(h('div', { class: 'po-diffbar' }, h('span', null, fromLabel), icon('chev'), h('span', null, toLabel)));
    return wrap;
  }

  // code draws lines of CUE in the dark window of the page, with a gutter. A line has its text, or its tokens.
  code(lines, o) {
    const toks = lines.some((l) => l.text !== undefined) ? tokenize(lines.map((l) => (l.text === undefined ? '' : l.text))) : null;
    const body = h('div', { class: 'po-cbody' });
    const parts = [];
    lines.forEach((l, i) => {
      if (l.op === '…') { body.append(h('div', { class: 'po-ln gap' }, h('i', { class: 'po-no' }), h('span', { class: 'po-sg' }), h('span', { class: 'po-code' }, `${plural(l.n, 'unchanged line')}`))); return; }
      const code = h('span', { class: 'po-code' });
      const tk = l.toks || (toks && toks[i]) || [];
      for (const t of tk) code.append(t.t ? h('span', { class: 'tk-' + t.t }, t.text) : t.text);
      if (!tk.length) code.append('​');
      const sg = h('span', { class: 'po-sg' }, l.op === ' ' ? '' : l.op === '-' ? '−' : '+');
      if (l.op !== ' ') sg.setAttribute('aria-hidden', 'true');
      const row = h('div', { class: 'po-ln' + (l.op === '+' ? ' add' : l.op === '-' ? ' del' : '') },
        h('i', { class: 'po-no' }, l.no ? String(l.no) : ''), sg, code,
        l.op === '+' || l.op === '-' ? h('span', { class: 'sr-only' }, l.op === '+' ? ' (added)' : ' (removed)') : null);
      body.append(row);
      parts.push({ row, host: sg, code });
    });
    // a text that is shown whole, not as a change, can be folded
    if (toks && lines.every((l) => l.op === ' ')) attachFolds(parts, toks);
    const wrap = h('div', { class: 'po-cw' });
    const scroll = h('div', { class: 'po-scroll', role: 'region', tabIndex: 0, 'aria-label': o.label || 'Rule text' }, body);
    wrap.append(scroll);
    if (o.copy) wrap.append(h('button', { class: 'cpbtn po-cp', type: 'button', 'data-copy': o.copy, 'aria-label': o.label, 'data-tip': o.label }, icon('copy')));
    return wrap;
  }
}
