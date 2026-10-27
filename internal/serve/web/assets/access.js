// The Access view: how the state is protected, who can read it, whether the readers file agrees with the store,
// and commands to copy that change it. The page reads /api/access and shows it; nothing here runs a command,
// writes a file or asks the server to. The text of every command comes from the server and is shown as text.

import { h, clear, icon, relTime, stamp, fmt } from './util.js';

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

// ROLES says what the roles of the keys are called on the page; the sentence for each is the server's.
const ROLE_STATE = {
  pinned: { cls: 'ok', label: 'pinned in the state', ic: 'lock' },
  file: { cls: 'chg', label: 'from the readers file', ic: 'bang' },
  untrusted: { cls: 'bad', label: 'does not verify', ic: 'bang' },
  off: { cls: '', label: 'off', ic: 'hold' },
};

const ROLE_HINT = {
  admin: 'may do what a writer may, and remove a policy, change who reads, and store new roles',
  writer: 'may change the state: apply, destroy, import, edit state, add policies',
  reader: 'may open the state, plan without writing it, and store a plan',
  former: 'left or was demoted; only the transactions an admin vouched for stay valid',
};

const NAME_REASON = 'This name is not a valid reader name. It is listed and used in no command.';

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

  render({ access, project }) {
    if (!access) {
      clear(this.root);
      this.root.append(h('div', { class: 'ac' }, h('h2', { class: 'ac-title' }, 'State'), h('p', { class: 'ac-empty' }, 'The protection of the state could not be read. The rest of the page is unaffected.')));
      return;
    }
    this.doc = access;
    const focus = document.activeElement && this.root.contains(document.activeElement) ? document.activeElement.dataset.fk : null;
    const top = this.root.scrollTop;
    clear(this.root);
    const a = access;
    // A state that is not sealed and a repository with no readers file has no readers to list and no file to compare.
    const bare = a.state !== 'sealed' && a.state !== 'migrating' && !(a.readers || []).length && (!a.file || a.file.verdict === 'none');
    this.root.append(h('div', { class: 'ac' },
      this.banner(a, project),
      a.you && a.you.state !== 'reader' ? this.you(a.you) : null,
      bare ? null : this.readers(a),
      this.roles(a),
      this.single(a),
      this.recovery(a),
      bare ? null : this.file(a),
      this.records(a),
      this.suggestions(a),
      h('p', { class: 'ac-foot' }, FOOTER)));
    this.root.scrollTop = top;
    if (focus) {
      const el = Array.from(this.root.querySelectorAll('[data-fk]')).find((x) => x.dataset.fk === focus);
      if (el) el.focus({ preventScroll: true });
    }
  }

  // banner is the sentence for the state of the store, with its facts.
  banner(a, project) {
    const st = a.state === 'migrating' && a.migration && a.migration.unseal ? { cls: 'chg', label: 'unsealing in progress', ic: 'hold' } : STATE[a.state] || STATE.off;
    // Four columns, in rows that go together: what the state is, where it is, and when and by what it was read.
    const f = this.facts(project);
    const take = (k) => f.find((x) => x && x.dataset.k === k) || null;
    const facts = [
      h('li', { 'data-k': 'format' }, h('span', { class: 'k' }, 'format'), h('b', null, String(a.format))),
      h('li', { 'data-k': 'backend' }, h('span', { class: 'k' }, 'backend'), h('b', null, a.backend)),
      take('records'), take('locks'),
      h('li', { class: 'wide', 'data-k': 'location' }, h('span', { class: 'k' }, 'location'), h('code', null, a.location)),
      take('lineage'),
      take('pravo'), take('read'),
      a.last_change ? h('li', { class: 'wide', title: stamp(a.last_change.at), 'data-k': 'change' }, h('span', { class: 'k' }, 'last change'), h('b', null, a.last_change.text)) : null,
    ];
    return h('section', { class: `ac-banner st-${a.state}` },
      h('div', { class: 'ac-bhead' }, h('h2', { id: 'ac-h-state', class: 'ac-title' }, 'State'),
        h('span', { class: `b ${st.cls}` }, icon(st.ic), st.label)),
      h('p', { class: 'ac-sentence' }, a.summary, a.records.text ? ' ' + a.records.text : ''),
      a.migration ? this.progress(a.migration) : null,
      h('ul', { class: 'ac-facts' }, facts),
      (a.problems || []).map((p) => h('p', { class: 'ac-problem' }, icon('bang'), h('span', null, p))));
  }

  progress(m) {
    const pct = m.total > 0 ? Math.min(100, Math.round((100 * m.done) / m.total)) : 0;
    return h('div', { class: 'ac-prog' },
      h('progress', { max: String(Math.max(m.total, 1)), value: String(Math.min(m.done, m.total)), 'aria-label': `${fmt(m.done)} of ${fmt(m.total)} records ${m.unseal ? 'unsealed' : 'sealed'}` }),
      h('span', null, `${fmt(m.done)} of ${fmt(m.total)} records · ${pct}%`),
      h('span', { class: 'dim', title: stamp(m.started) }, m.by ? `started by ${m.by} ${relTime(m.started)}` : `started ${relTime(m.started)}`));
  }

  // facts are what the project says of the state besides where it is: the records, the locks, the lineage, and when
  // and by which pravo it was read.
  facts(p) {
    if (!p) return [];
    const st = p.state || {};
    return [
      st.records !== undefined ? h('li', { 'data-k': 'records' }, h('span', { class: 'k' }, 'records'), h('b', null, fmt(st.records))) : null,
      h('li', { 'data-k': 'locks' }, h('span', { class: 'k' }, 'locks'), h('b', null, String(st.locks || 0))),
      st.lineage ? h('li', { class: 'wide', 'data-k': 'lineage' }, h('span', { class: 'k' }, 'lineage'), h('code', null, st.lineage)) : null,
      p.version ? h('li', { 'data-k': 'pravo' }, h('span', { class: 'k' }, 'pravo'), h('b', null, p.version)) : null,
      p.loaded_at ? h('li', { title: stamp(p.loaded_at), 'data-k': 'read' }, h('span', { class: 'k' }, 'read'), h('b', null, relTime(p.loaded_at))) : null,
    ];
  }

  // you says why the page is read as nobody who can read. A reader is named in the bar and needs no block.
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
    const roles = rows.some((r) => r.role);
    const body = rows.map((r) => h('tr', { class: 'ac-r' + (r.you ? ' you' : '') + (r.name_ok ? '' : ' bad'), role: 'row' },
      h('th', { scope: 'row', role: 'rowheader', 'data-label': 'Name' }, h('span', { class: 'nm' }, r.name || '(no name)'),
        r.you ? h('span', { class: 'b ok' }, 'you') : null,
        r.name_ok ? null : h('span', { class: 'b bad', tabindex: '0', role: 'note', 'aria-label': 'name not usable', 'aria-description': NAME_REASON, 'data-tip': NAME_REASON, 'data-fk': 'nm:' + (r.name || '') }, 'name not usable')),
      roles ? h('td', { role: 'cell', 'data-label': 'Role' }, r.role ? this.rolePill(r.role) : h('span', { class: 'dim' }, 'none')) : null,
      h('td', { role: 'cell', 'data-label': 'Reader key' }, r.key ? this.key(r.key_short, r.key, 'reader key of ' + (r.name || 'a reader')) : h('span', { class: 'dim' }, 'none')),
      h('td', { role: 'cell', 'data-label': 'Signing key' }, r.fingerprint ? this.key(r.fingerprint.length > 18 ? r.fingerprint.slice(0, 18) + '…' : r.fingerprint, r.fingerprint, 'signing key fingerprint of ' + (r.name || 'a reader')) : h('span', { class: 'dim' }, 'none')),
      h('td', { class: 'c', role: 'cell', 'data-label': 'In the file' }, mark(r.in_file, 'readers file')),
      h('td', { class: 'c', role: 'cell', 'data-label': 'In the store' }, mark(r.in_store, 'store')),
      h('td', { class: 'c', role: 'cell', 'data-label': 'Line' }, r.line ? String(r.line) : h('span', { class: 'dim' }, h('span', { 'aria-hidden': 'true' }, '–'), h('span', { class: 'sr-only' }, 'none')))));
    return h('section', { class: 'ac-sec', 'aria-labelledby': 'ac-h-readers' }, head,
      h('div', { class: 'ac-tablewrap' }, h('table', { class: 'ac-table', role: 'table' },
        h('caption', { class: 'sr-only' }, 'The readers of the state, in the readers file and in the store'),
        h('thead', { role: 'rowgroup' }, h('tr', { role: 'row' }, (roles ? ['Name', 'Role', 'Reader key', 'Signing key', 'In the file', 'In the store', 'Line'] : ['Name', 'Reader key', 'Signing key', 'In the file', 'In the store', 'Line']).map((t) => h('th', { scope: 'col', role: 'columnheader', class: ['In the file', 'In the store', 'Line'].includes(t) ? 'c' : '' }, t)))),
        h('tbody', { role: 'rowgroup' }, body))));
  }

  rolePill(role) {
    return h('span', { class: `b rl rl-${role}`, title: ROLE_HINT[role] || null }, role);
  }

  // roles is the section for what each key may do: where the roles come from, the versions of them signed in the
  // state, the keys that left, and whether every record is the work of a writer.
  roles(a) {
    const r = a.roles;
    if (!r) return null;
    const st = ROLE_STATE[r.state] || ROLE_STATE.off;
    return h('section', { class: `ac-sec ac-roles rs-${r.state}`, 'aria-labelledby': 'ac-h-roles' },
      h('div', { class: 'ac-rhead' },
        h('h3', { id: 'ac-h-roles', class: 'h' }, 'Roles'),
        h('span', { class: `b ${st.cls}` }, icon(st.ic), st.label),
        r.you ? h('span', { class: 'ac-ryou' }, 'you are ', this.rolePill(r.you)) : null),
      h('p', { class: 'ac-rtext' }, r.text),
      (r.versions || []).length ? this.chain(r) : null,
      (r.others || []).length ? this.others(r) : null,
      r.records ? this.recordCheck(r.records) : null);
  }

  // chain draws the versions of the roles in the state from the first, which the readers file pins, to the newest.
  // Each is signed by an admin of the one before it, so the line between them is an arrow.
  chain(r) {
    const vs = r.versions;
    return h('div', { class: 'ac-block' },
      h('h4', { class: 'ac-sub' }, 'Versions in the state'),
      h('ol', { class: 'ac-chain', 'aria-label': 'Versions of the roles, oldest first' }, vs.map((v, i) => {
        const last = i === vs.length - 1;
        return h('li', { class: 'ac-ver' + (v.pinned ? ' pin' : '') + (last ? ' head' : ''), 'data-seq': String(v.seq) },
          h('div', { class: 'vh' }, h('b', { class: 'vn' }, 'v' + v.seq),
            v.pinned ? h('span', { class: 'b ok', title: 'The readers file pins this version by its digest' }, icon('lock'), 'pinned') : null,
            last && !v.pinned ? h('span', { class: 'b', title: 'The newest version' }, 'newest') : null),
          h('p', { class: 'vm', title: stamp(v.at) }, relTime(v.at), ' · signed by ', h('b', null, v.by || 'unknown')),
          (v.changes || []).length ? h('ul', { class: 'vc' }, v.changes.map((x) => h('li', null, x.replace(' -> ', ' → ')))) : h('p', { class: 'vm dim' }, 'no change in who holds a role'),
          v.vouched ? h('p', { class: 'vv' }, h('span', { class: 'b lock' }, 'vouched'), ` ${fmt(v.vouched)} ${v.vouched === 1 ? 'transaction' : 'transactions'}`) : null);
      })),
      r.differs ? h('p', { class: 'ac-note ac-differs' }, icon('bang'), 'The readers file asks for other roles than the newest version holds. An admin stores them with the card below.') : null);
  }

  // others lists the keys of the roles that no row of the readers table shows: keys that left, and keys in the state that the readers file does not list.
  others(r) {
    return h('div', { class: 'ac-block' },
      h('h4', { class: 'ac-sub' }, 'Keys that are not in the readers file'),
      h('div', { class: 'ac-tablewrap' }, h('table', { class: 'ac-table small' },
        h('caption', { class: 'sr-only' }, 'Keys named by the roles that the readers file does not list'),
        h('thead', null, h('tr', null, ['Name', 'Role', 'Signing key', 'Vouched'].map((t, i) => h('th', { scope: 'col', class: i === 3 ? 'c' : '' }, t)))),
        h('tbody', null, r.others.map((k) => h('tr', null,
          h('th', { scope: 'row', 'data-label': 'Name' }, h('span', { class: 'nm' }, k.name)),
          h('td', { 'data-label': 'Role' }, this.rolePill(k.role)),
          h('td', { 'data-label': 'Signing key' }, this.key(k.fingerprint_short, k.fingerprint, 'signing key fingerprint of ' + k.name)),
          h('td', { class: 'c', 'data-label': 'Vouched' }, k.vouched ? fmt(k.vouched) : h('span', { class: 'dim' }, '–'))))))));
  }

  // recordCheck says whether the records are the work of writers, as one bar of the four kinds there are.
  recordCheck(rc) {
    const parts = [
      { k: 'bound', n: rc.bound, label: 'bound to a transaction of a writer', cls: 'ok' },
      { k: 'vouched', n: rc.vouched, label: 'vouched for by an admin', cls: 'lock' },
      { k: 'before', n: rc.before_roles, label: 'from before roles', cls: 'dim' },
      { k: 'unchecked', n: rc.unchecked, label: 'unchecked: running or killed', cls: 'chg' },
    ].filter((p) => p.n > 0);
    const total = parts.reduce((s, p) => s + p.n, 0);
    const bad = (rc.problems || []).length + (rc.more_problems || 0);
    return h('div', { class: 'ac-block ac-check' },
      h('h4', { class: 'ac-sub' }, 'Records',
        rc.skipped ? null : (bad ? h('span', { class: 'b bad' }, icon('bang'), `${fmt(bad)} not a writer's`) : h('span', { class: 'b ok' }, icon('check'), 'all by writers'))),
      rc.skipped ? h('p', { class: 'ac-note' }, 'Not checked: ' + rc.skipped + '.') : [
        total ? h('div', { class: 'ac-bar', role: 'img', 'aria-label': parts.map((p) => `${fmt(p.n)} ${p.label}`).join(', ') },
          parts.map((p) => h('span', { class: `seg ${p.cls}`, style: `--n:${p.n}`, title: `${fmt(p.n)} ${p.label}` }))) : null,
        h('ul', { class: 'ac-legend' }, parts.map((p) => h('li', { class: p.cls }, h('i', { class: 'sw', 'aria-hidden': 'true' }), h('b', null, fmt(p.n)), p.label))),
        bad ? h('ul', { class: 'ac-problems' },
          rc.problems.map((p) => h('li', null, h('code', null, p.addr), h('span', null, p.why))),
          rc.more_problems ? h('li', { class: 'dim' }, `and ${fmt(rc.more_problems)} more`) : null) : null]);
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

  // single warns that one reader holds the only lock: there is no recovery key, so losing that key loses the state.
  // The button opens the suggestion that adds a backup reader.
  single(a) {
    const w = a.single_reader;
    if (!w || !(a.suggestions || []).some((x) => x.id === w.suggestion)) return null;
    const go = () => {
      const card = Array.from(this.root.querySelectorAll('details.ac-card')).find((d) => d.dataset.id === w.suggestion);
      if (!card) return;
      card.open = true;
      card.scrollIntoView({ block: 'nearest' });
      const sum = card.querySelector('summary');
      if (sum) sum.focus();
    };
    return h('section', { class: 'ac-warn', role: 'note', 'aria-labelledby': 'ac-h-single' },
      h('h3', { id: 'ac-h-single', class: 'sr-only' }, 'Only one reader'),
      icon('bang'), h('p', null, w.text),
      h('button', { class: 'btn', type: 'button', 'data-fk': 'single', on: { click: go } }, w.action));
  }

  // recovery says how the state can be opened when every reader has lost their key: a key that is split into shares.
  // A warning shows when the recovery does not hold, and the button opens the suggestion that mends it.
  recovery(a) {
    const r = a.recovery;
    if (!r) return null;
    const go = () => {
      const card = Array.from(this.root.querySelectorAll('details.ac-card')).find((d) => d.dataset.id === 'fix-recovery');
      if (!card) return;
      card.open = true;
      card.scrollIntoView({ block: 'nearest' });
      const sum = card.querySelector('summary');
      if (sum) sum.focus();
    };
    const fix = r.warning && (a.suggestions || []).some((x) => x.id === 'fix-recovery');
    return h('section', { class: 'ac-sec ac-recovery', 'aria-labelledby': 'ac-h-recovery' },
      h('h3', { id: 'ac-h-recovery', class: 'h' }, 'Recovery'),
      h('p', null, r.text, ' ', h('code', { title: r.key }, r.key_short)),
      r.warning ? h('div', { class: 'ac-warn', role: 'note' }, icon('bang'), h('p', null, r.warning),
        fix ? h('button', { class: 'btn', type: 'button', 'data-fk': 'recovery', on: { click: go } }, 'Bring the recovery in line') : null) : null);
  }

  // records is the section for what needs a look beyond the sentence in the banner: the groups of data keys, once
  // there is more than one. A state with one group has nothing to show here.
  records(a) {
    if (!(a.groups || []).length) return null;
    return h('section', { class: 'ac-sec', 'aria-labelledby': 'ac-h-rec' },
      h('h3', { id: 'ac-h-rec', class: 'h' }, 'Groups of data keys'),
      this.groups(a.groups, a.groups_text));
  }

  groups(groups, text) {
    return h('div', { class: 'ac-groups' },
      h('table', { class: 'ac-table small' },
        h('caption', { class: 'sr-only' }, 'Groups of data keys'),
        h('thead', null, h('tr', null, ['Group', 'Locks', 'Records'].map((t, i) => h('th', { scope: 'col', class: i ? 'c' : '' }, t)))),
        h('tbody', null, groups.map((g) => h('tr', null,
          h('th', { scope: 'row', 'data-label': 'Group' }, h('code', null, g.id), g.current ? h('span', { class: 'b ok' }, 'current') : null),
          h('td', { class: 'c', 'data-label': 'Locks' }, String(g.locks)),
          h('td', { class: 'c', 'data-label': 'Records' }, fmt(g.records)))))),
      text ? h('p', { class: 'ac-note dim' }, text) : null);
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
      s.primary ? [h('span', { class: 'sr-only' }, ', '), h('span', { class: 'b ok' }, 'next')] : null,
      s.who ? [h('span', { class: 'sr-only' }, ', '), h('span', { class: 'who' }, s.who)] : null),
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
