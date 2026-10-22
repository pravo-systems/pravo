// The Access view: how the state is protected, who can read it, whether the readers file agrees with the store,
// and commands to copy that change it. The page reads /api/access and shows it; nothing here runs a command,
// writes a file or asks the server to. The text of every command comes from the server and is shown as text.

import { h, clear, icon, plural, relTime, stamp, fmt } from './util.js';

// STATE says what the banner calls each state; the sentence is the server's.
const STATE = {
  off: { cls: 'chg', label: 'not sealed', ic: 'bang' },
  immutable: { cls: 'lock', label: 'immutable records', ic: 'lock' },
  sealed: { cls: 'ok', label: 'sealed', ic: 'lock' },
  migrating: { cls: 'chg', label: 'sealing in progress', ic: 'hold' },
};

const VERDICT = {
  match: { cls: 'ok', label: 'matches the store' },
  differs: { cls: 'chg', label: 'differs from the store' },
  missing: { cls: 'chg', label: 'no file' },
  invalid: { cls: 'bad', label: 'does not load' },
  'expected-sealed': { cls: 'bad', label: 'state not sealed' },
  none: { cls: '', label: 'not compared' },
};

const YOU = {
  reader: { cls: 'ok', label: 'reader' },
  'not-reader': { cls: 'bad', label: 'not a reader' },
  'no-identity': { cls: 'chg', label: 'no identity' },
  locked: { cls: 'chg', label: 'identity locked' },
};

export const FOOTER = 'This page suggests commands. It runs nothing and writes nothing.';

// withParts shows the parts of a text that a person fills in (words in angle brackets) apart from the rest.
function withParts(text) {
  return String(text).split(/(<[^<>]+>)/).map((p, i) => (i % 2 ? h('span', { class: 'ph' }, p) : p));
}

export class AccessView {
  constructor(root, cb = {}) {
    this.root = root;
    this.cb = cb;
    this.opened = new Map(); // suggestion id -> open, as the person left it
    this.doc = null;
  }

  render({ access }) {
    if (!access) {
      clear(this.root);
      this.root.append(h('div', { class: 'ac' }, h('h2', { class: 'ac-title' }, 'Access'), h('p', { class: 'ac-empty' }, 'The access document could not be read. The rest of the page is unaffected.')));
      return;
    }
    this.doc = access;
    const focus = document.activeElement && this.root.contains(document.activeElement) ? document.activeElement.dataset.fk : null;
    const top = this.root.scrollTop;
    clear(this.root);
    const a = access;
    this.root.append(h('div', { class: 'ac' },
      this.banner(a),
      a.you ? this.you(a.you) : null,
      this.readers(a),
      this.file(a),
      a.state === 'off' || a.state === 'immutable' ? null : this.records(a),
      this.suggestions(a),
      h('p', { class: 'ac-foot' }, FOOTER)));
    this.root.scrollTop = top;
    if (focus) {
      const el = Array.from(this.root.querySelectorAll('[data-fk]')).find((x) => x.dataset.fk === focus);
      if (el) el.focus({ preventScroll: true });
    }
  }

  // banner is the sentence for the state of the store, with its facts.
  banner(a) {
    const st = STATE[a.state] || STATE.off;
    const facts = [
      h('li', null, h('span', { class: 'k' }, 'format'), h('b', null, String(a.format))),
      h('li', null, h('span', { class: 'k' }, 'backend'), h('b', null, a.backend)),
      h('li', { class: 'wide' }, h('span', { class: 'k' }, 'location'), h('code', null, a.location)),
      a.state === 'off' || a.state === 'immutable' ? null : h('li', null, h('span', { class: 'k' }, 'recovery'), h('b', null, a.recovery.state)),
    ];
    return h('section', { class: `ac-banner st-${a.state}`, 'aria-labelledby': 'ac-h-state' },
      h('div', { class: 'ac-bhead' }, h('h2', { id: 'ac-h-state', class: 'ac-title' }, 'Access'),
        h('span', { class: `b ${st.cls}` }, icon(st.ic), st.label)),
      h('p', { class: 'ac-sentence' }, a.summary),
      a.migration ? this.progress(a.migration) : null,
      h('ul', { class: 'ac-facts' }, facts),
      (a.problems || []).map((p) => h('p', { class: 'ac-problem' }, icon('bang'), h('span', null, p))));
  }

  progress(m) {
    const pct = m.total > 0 ? Math.min(100, Math.round((100 * m.done) / m.total)) : 0;
    return h('div', { class: 'ac-prog' },
      h('progress', { max: String(Math.max(m.total, 1)), value: String(Math.min(m.done, m.total)), 'aria-label': `${fmt(m.done)} of ${fmt(m.total)} records sealed` }),
      h('span', null, `${fmt(m.done)} of ${fmt(m.total)} records · ${pct}%`),
      h('span', { class: 'dim', title: stamp(m.started) }, m.by ? `started by ${m.by} ${relTime(m.started)}` : `started ${relTime(m.started)}`));
  }

  // you says who the page is read as: the identity that the command opened the state with.
  you(y) {
    const c = YOU[y.state] || YOU['no-identity'];
    return h('section', { class: `ac-you y-${y.state}`, 'aria-labelledby': 'ac-h-you' },
      h('h3', { id: 'ac-h-you', class: 'h' }, 'You'),
      h('div', { class: 'ac-youline' }, h('span', { class: `b ${c.cls}` }, icon(y.state === 'reader' ? 'check' : 'lock'), c.label), y.name && y.state !== 'reader' ? h('code', { class: 'ac-who' }, y.name) : null, h('p', null, y.text)));
  }

  readers(a) {
    const rows = a.readers || [];
    const sealed = a.state === 'sealed' || a.state === 'migrating';
    const head = h('h3', { id: 'ac-h-readers', class: 'h' }, rows.length ? `Readers · ${rows.length}` : 'Readers');
    if (!rows.length) {
      return h('section', { class: 'ac-sec', 'aria-labelledby': 'ac-h-readers' }, head,
        h('p', { class: 'ac-empty' }, sealed ? 'The store lists no readers.' : 'No readers are listed. A sealed state lists them here, from the store and from the readers file.'));
    }
    const mark = (on, what) => on
      ? h('span', { class: 'ac-mark yes', role: 'img', 'aria-label': `in the ${what}` }, icon('check'))
      : h('span', { class: 'ac-mark no', role: 'img', 'aria-label': `not in the ${what}` }, '–');
    const body = rows.map((r) => h('tr', { class: 'ac-r' + (r.you ? ' you' : '') + (r.name_ok ? '' : ' bad') },
      h('th', { scope: 'row', 'data-label': 'Name' }, h('span', { class: 'nm' }, r.name || '(no name)'),
        r.you ? h('span', { class: 'b ok' }, 'you') : null,
        r.name_ok ? null : h('span', { class: 'b bad', 'data-tip': 'This name is not a valid reader name. It is listed and used in no command.' }, 'name not usable')),
      h('td', { 'data-label': 'Reader key' }, r.key ? this.key(r.key_short, r.key, 'reader key of ' + (r.name || 'a reader')) : h('span', { class: 'dim' }, 'none')),
      h('td', { 'data-label': 'Signing key' }, r.fingerprint ? this.key(r.fingerprint.length > 18 ? r.fingerprint.slice(0, 18) + '…' : r.fingerprint, r.fingerprint, 'signing key fingerprint of ' + (r.name || 'a reader')) : h('span', { class: 'dim' }, 'none')),
      h('td', { class: 'c', 'data-label': 'In the file' }, mark(r.in_file, 'readers file')),
      h('td', { class: 'c', 'data-label': 'In the store' }, mark(r.in_store, 'store')),
      h('td', { class: 'c', 'data-label': 'Line' }, r.line ? String(r.line) : h('span', { class: 'dim' }, '–'))));
    return h('section', { class: 'ac-sec', 'aria-labelledby': 'ac-h-readers' }, head,
      h('div', { class: 'ac-tablewrap' }, h('table', { class: 'ac-table' },
        h('caption', { class: 'sr-only' }, 'The readers of the state, in the readers file and in the store'),
        h('thead', null, h('tr', null, ['Name', 'Reader key', 'Signing key', 'In the file', 'In the store', 'Line'].map((t, i) => h('th', { scope: 'col', class: i > 2 ? 'c' : '' }, t)))),
        h('tbody', null, body))));
  }

  key(shown, full, what) {
    return h('span', { class: 'ac-key' }, h('code', { title: full }, shown),
      h('button', { class: 'cpbtn', type: 'button', 'data-copy': full, 'aria-label': `Copy the ${what}`, 'data-tip': 'Copy', 'data-fk': 'k:' + full }, icon('copy')));
  }

  // file is the verdict between the readers file and the store.
  file(a) {
    const f = a.file;
    const v = VERDICT[f.verdict] || VERDICT.none;
    // The verdict is about the store, which a state that is not sealed has no locks for, except when the repository lists readers.
    const sealed = a.state === 'sealed' || a.state === 'migrating' || f.verdict === 'expected-sealed';
    return h('section', { class: `ac-sec ac-file v-${f.verdict}`, 'aria-labelledby': 'ac-h-file' },
      h('h3', { id: 'ac-h-file', class: 'h' }, 'Readers file'),
      h('div', { class: 'ac-verdict' },
        sealed ? h('span', { class: `b ${v.cls}` }, icon(f.verdict === 'match' ? 'check' : 'bang'), v.label) : null,
        h('p', null, f.text)),
      h('p', { class: 'ac-path' }, h('code', null, f.path), h('button', { class: 'cpbtn', type: 'button', 'data-copy': f.path, 'aria-label': 'Copy the path of the readers file', 'data-tip': 'Copy the path', 'data-fk': 'path' }, icon('copy'))),
      (f.added || []).length || (f.removed || []).length ? h('ul', { class: 'ac-diffs' },
        (f.added || []).map((n) => h('li', { class: 'add' }, h('span', { class: 'sg', 'aria-hidden': 'true' }, '+'), h('b', null, n), ' is in the file and not in the store')),
        (f.removed || []).map((n) => h('li', { class: 'del' }, h('span', { class: 'sg', 'aria-hidden': 'true' }, '−'), h('b', null, n), ' is in the store and not in the file'))) : null,
      (f.errors || []).length ? h('ul', { class: 'ac-errors' }, f.errors.map((e) => h('li', null, e.line > 0 ? [h('b', null, `line ${e.line}`), ' '] : null, e.reason))) : null);
  }

  // records says how many records are sealed, and which groups of data keys they are under.
  records(a) {
    const r = a.records;
    const stat = (k, v, cls) => h('li', { class: cls || null }, h('b', null, fmt(v)), h('span', null, k));
    return h('section', { class: 'ac-sec', 'aria-labelledby': 'ac-h-rec' },
      h('h3', { id: 'ac-h-rec', class: 'h' }, 'Records'),
      h('ul', { class: 'ac-stats' }, stat('records', r.total), stat('sealed', r.sealed), r.plain ? stat('not sealed yet', r.plain, 'chg') : null, r.unreadable ? stat('cannot be opened', r.unreadable, 'bad') : null),
      r.text ? h('p', { class: 'ac-note' }, r.text) : null,
      (a.groups || []).length ? this.groups(a.groups) : null,
      h('p', { class: 'ac-note' }, a.recovery.text),
      a.last_change ? h('p', { class: 'ac-note', title: stamp(a.last_change.at) }, a.last_change.text) : null);
  }

  groups(groups) {
    return h('div', { class: 'ac-groups' },
      h('table', { class: 'ac-table small' },
        h('caption', { class: 'sr-only' }, 'Groups of data keys'),
        h('thead', null, h('tr', null, ['Group', 'Locks', 'Records'].map((t, i) => h('th', { scope: 'col', class: i ? 'c' : '' }, t)))),
        h('tbody', null, groups.map((g) => h('tr', null,
          h('th', { scope: 'row', 'data-label': 'Group' }, h('code', null, g.id), g.current ? h('span', { class: 'b ok' }, 'current') : null),
          h('td', { class: 'c', 'data-label': 'Locks' }, String(g.locks)),
          h('td', { class: 'c', 'data-label': 'Records' }, fmt(g.records)))))),
      h('p', { class: 'ac-note dim' }, 'A removal starts a new group. Records stay under the old group until they are written again or resealed with rewrap --all.'));
  }

  suggestions(a) {
    const list = a.suggestions || [];
    const head = h('h3', { id: 'ac-h-sug', class: 'h' }, 'Suggested changes');
    if (!list.length) return h('section', { class: 'ac-sec', 'aria-labelledby': 'ac-h-sug' }, head, h('p', { class: 'ac-empty' }, 'Nothing to suggest.'));
    return h('section', { class: 'ac-sec', 'aria-labelledby': 'ac-h-sug' }, head,
      h('p', { class: 'ac-note' }, 'Copy a step and run it yourself. Parts in ', h('span', { class: 'ph' }, '<angle brackets>'), ' are for you to fill in.'),
      h('div', { class: 'ac-cards' }, list.map((s) => this.card(s))));
  }

  card(s) {
    const open = this.opened.has(s.id) ? this.opened.get(s.id) : !!s.primary;
    const d = h('details', { class: 'ac-card' + (s.primary ? ' primary' : ''), open: open ? '' : null, 'data-id': s.id });
    d.addEventListener('toggle', () => this.opened.set(s.id, d.open));
    d.append(h('summary', { 'data-fk': 'sum:' + s.id },
      h('span', { class: 'ti' }, s.title),
      s.primary ? h('span', { class: 'b ok' }, 'next') : null,
      s.who ? h('span', { class: 'who' }, s.who) : null),
      h('div', { class: 'ac-cbody' }, h('p', { class: 'ac-why' }, s.why), h('div', { class: 'ac-steps' }, s.steps.map((st, i) => this.step(s, st, i)))));
    return d;
  }

  step(s, st, i) {
    const fk = `st:${s.id}:${i}`;
    switch (st.kind) {
      case 'command':
        return h('div', { class: 'cmdline ac-cmd' }, h('span', { class: 'ps', 'aria-hidden': 'true' }, '$'), h('code', null, st.placeholder ? withParts(st.text) : st.text),
          h('button', { class: 'cpbtn', type: 'button', 'data-copy': st.text, 'data-fk': fk, 'aria-label': 'Copy the command: ' + st.text, 'data-tip': 'Copy the command' }, icon('copy')));
      case 'add-line':
      case 'remove-line': {
        const add = st.kind === 'add-line';
        return h('div', { class: 'ac-edit' },
          h('p', { class: 'ac-editfile' }, add ? 'Add this line to ' : 'Remove this line from ', h('code', null, st.file), add ? '' : '. Look for the line that starts with the name.'),
          h('div', { class: 'ac-diff ' + (add ? 'add' : 'del') },
            h('span', { class: 'sg', 'aria-hidden': 'true' }, add ? '+' : '−'),
            h('code', null, st.placeholder ? withParts(st.text) : st.text),
            h('button', { class: 'cpbtn', type: 'button', 'data-copy': st.text, 'data-fk': fk, 'aria-label': (add ? 'Copy the line to add' : 'Copy the line to remove') + (st.placeholder ? '' : ' (the line only)'), 'data-tip': 'Copy the line' }, icon('copy'))));
      }
      default:
        return h('p', { class: 'ac-stepnote' }, st.text);
    }
  }
}
