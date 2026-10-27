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
  match: { cls: 'ok', label: 'matches the state' },
  differs: { cls: 'chg', label: 'differs from the state' },
  missing: { cls: 'chg', label: 'no list' },
  invalid: { cls: 'bad', label: "can't be read" },
  'expected-sealed': { cls: 'bad', label: 'state not sealed' },
  none: { cls: '', label: 'not compared' },
};

const YOU = {
  reader: { cls: 'ok', label: 'member' },
  'not-reader': { cls: 'bad', label: 'not a member' },
  'no-identity': { cls: 'chg', label: 'no key' },
  locked: { cls: 'chg', label: 'key locked' },
};

// ROLE_STATE says what the badge of the roles reads; the sentence is the server's.
const ROLE_STATE = {
  pinned: { cls: 'ok', label: 'locked in', ic: 'lock' },
  file: { cls: 'chg', label: 'not locked in', ic: 'bang' },
  untrusted: { cls: 'bad', label: 'check failed', ic: 'bang' },
  off: { cls: '', label: 'off', ic: 'hold' },
};

// ROLE_HINT is what a role lets a member do, in the words of a person who has not read the manual.
const ROLE_HINT = {
  admin: 'Manages who has access and who may do what. Can also do everything a writer can.',
  writer: 'Can change the state: apply, destroy, import. Can also do everything a reader can.',
  reader: 'Can open the state, look at it and plan. Cannot change anything.',
  former: 'Has left, or no longer holds a role. Only the changes an admin approved stay valid.',
};

// STATUS is the badge of a member: whether the list and the state agree about them.
const STATUS = {
  active: { cls: 'ok', ic: 'check' },
  'to-add': { cls: 'chg', ic: 'hold' },
  'to-remove': { cls: 'chg', ic: 'hold' },
};

const NAME_REASON = 'This name is not valid for a member. It is listed and used in no command.';

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
    // A state that is not sealed and a repository with no readers file has nobody to list and no file to compare.
    const bare = a.state !== 'sealed' && a.state !== 'migrating' && !(a.readers || []).length && (!a.file || a.file.verdict === 'none');
    this.root.append(h('div', { class: 'ac' },
      this.banner(a, project),
      a.you && a.you.state !== 'reader' ? this.you(a.you) : null,
      bare ? null : this.members(a),
      this.single(a),
      bare ? null : this.checks(a),
      this.recovery(a),
      this.todo(a),
      h('p', { class: 'ac-foot' }, FOOTER)));
    this.root.scrollTop = top;
    if (focus) {
      const el = Array.from(this.root.querySelectorAll('[data-fk]')).find((x) => x.dataset.fk === focus);
      if (el) el.focus({ preventScroll: true });
    }
  }

  // banner is the sentence for the state of the store, with the three facts a person wants first. The rest is
  // one disclosure away.
  banner(a, project) {
    const st = a.state === 'migrating' && a.migration && a.migration.unseal ? { cls: 'chg', label: 'unsealing in progress', ic: 'hold' } : STATE[a.state] || STATE.off;
    const f = this.facts(project);
    const take = (k) => f.find((x) => x && x.dataset.k === k) || null;
    const lead = [
      take('records'),
      h('li', { 'data-k': 'location' }, h('span', { class: 'k' }, 'stored in'), h('code', null, a.location)),
      a.last_change ? h('li', { title: stamp(a.last_change.at), 'data-k': 'change' }, h('span', { class: 'k' }, 'last change'), h('b', null, a.last_change.text)) : null,
    ];
    const tech = [
      h('li', { 'data-k': 'format' }, h('span', { class: 'k' }, 'format'), h('b', null, String(a.format))),
      h('li', { 'data-k': 'backend' }, h('span', { class: 'k' }, 'backend'), h('b', null, a.backend)),
      take('locks'), take('pravo'), take('read'), take('lineage'),
    ];
    return h('section', { class: `ac-banner st-${a.state}` },
      h('div', { class: 'ac-bhead' }, h('h2', { id: 'ac-h-state', class: 'ac-title' }, 'State'),
        h('span', { class: `b ${st.cls}` }, icon(st.ic), st.label)),
      h('p', { class: 'ac-sentence' }, a.summary, a.records.text ? ' ' + a.records.text : ''),
      a.migration ? this.progress(a.migration) : null,
      h('ul', { class: 'ac-facts lead' }, lead),
      (a.problems || []).map((p) => h('p', { class: 'ac-problem' }, icon('bang'), h('span', null, p))),
      h('details', { class: 'ac-tech' },
        h('summary', { 'data-fk': 'tech' }, 'Technical details'),
        h('ul', { class: 'ac-facts' }, tech),
        (a.groups || []).length ? h('div', { class: 'ac-techgroups' }, h('h3', { id: 'ac-h-rec', class: 'ac-sub' }, 'Groups of keys'), this.groups(a.groups, a.groups_text)) : null));
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

  // members lists who can open the state, one row each. A row opens to show what can be done with that member
  // (change their role, remove their access) and the keys behind them, which nobody needs to read day to day.
  members(a) {
    const rows = a.readers || [];
    const sealed = a.state === 'sealed' || a.state === 'migrating';
    const sugs = a.suggestions || [];
    const add = sugs.find((x) => x.kind === 'member-add');
    const head = h('div', { class: 'ac-mhead' },
      h('h3', { id: 'ac-h-readers', class: 'h' }, rows.length ? `Members · ${rows.length}` : 'Members'),
      add ? h('button', { class: 'btn', type: 'button', 'data-fk': 'add', on: { click: () => this.open(add.id) } }, icon('plus'), add.title) : null);
    if (!rows.length) {
      return h('section', { class: 'ac-sec', 'aria-labelledby': 'ac-h-readers' }, head,
        h('p', { class: 'ac-empty' }, sealed ? 'The state lists nobody who can open it.' : 'Nobody is listed. A sealed state lists here the members who can open it.'));
    }
    const roles = a.roles && a.roles.state !== 'off';
    const intro = sealed
      ? 'Everyone listed here holds a key that opens the secret values. A CI job is a member too, with a key of its own.'
      : 'These members are named in the access list.';
    const order = { 'member-role': 0, 'member-remove': 1 };
    const list = rows.map((r) => this.person(r, sugs.filter((x) => x.member === r.name && x.kind).sort((p, q) => order[p.kind] - order[q.kind]), roles));
    return h('section', { class: 'ac-sec ac-members', 'aria-labelledby': 'ac-h-readers' }, head,
      h('p', { class: 'ac-note first' }, intro),
      roles ? this.legend() : null,
      h('ul', { class: 'ac-people', 'aria-label': 'Members of the state' }, list),
      this.past(a.roles),
      add ? h('div', { class: 'ac-cards' }, this.card(add)) : null);
  }

  // legend says what the three roles are, once, above the list that uses them.
  legend() {
    return h('dl', { class: 'ac-legend-roles' }, ['admin', 'writer', 'reader'].map((k) => h('div', null,
      h('dt', null, this.rolePill(k)), h('dd', null, ROLE_HINT[k]))));
  }

  person(r, cards, roles) {
    const st = STATUS[r.status];
    const name = r.name || '(no name)';
    const d = h('details', { class: 'ac-person' + (r.you ? ' you' : '') + (r.name_ok ? '' : ' bad'), 'data-name': name });
    d.append(h('summary', { 'data-fk': 'p:' + name },
      h('span', { class: 'pn' }, h('span', { class: 'nm' }, name),
        r.you ? h('span', { class: 'b ok' }, 'you') : null,
        r.name_ok ? null : h('span', { class: 'b bad', tabindex: '0', role: 'note', 'aria-label': 'name not usable', 'aria-description': NAME_REASON, 'data-tip': NAME_REASON, 'data-fk': 'nm:' + name }, 'name not usable')),
      roles && r.role ? this.rolePill(r.role) : h('span', { class: 'pr' }),
      st ? h('span', { class: `b ${st.cls} ps` }, icon(st.ic), r.status_text) : h('span', { class: 'ps' })));
    d.append(h('div', { class: 'ac-pbody' },
      cards.length ? h('div', { class: 'ac-cards tight' }, cards.map((c) => this.card(c))) : null,
      h('dl', { class: 'ac-keys' },
        h('div', null, h('dt', null, 'Key that opens the state'), h('dd', null, r.key ? this.key(r.key_short, r.key, 'reader key of ' + name) : h('span', { class: 'dim' }, 'none'))),
        h('div', null, h('dt', null, 'Key that signs'), h('dd', null, r.fingerprint ? this.key(r.fingerprint.length > 18 ? r.fingerprint.slice(0, 18) + '…' : r.fingerprint, r.fingerprint, 'signing key fingerprint of ' + name) : h('span', { class: 'dim' }, 'none'))),
        r.line ? h('div', null, h('dt', null, 'Line in the access list'), h('dd', null, String(r.line))) : null)));
    return h('li', { class: 'ac-prow' }, d);
  }

  rolePill(role) {
    return h('span', { class: `b rl rl-${role}`, title: ROLE_HINT[role] || null }, role);
  }

  // past lists the keys that the roles name and that the list does not: members who left, and keys that were
  // given a role and are not on the list.
  past(r) {
    const list = r && r.others;
    if (!list || !list.length) return null;
    return h('div', { class: 'ac-block' },
      h('h4', { class: 'ac-sub' }, 'No longer on the list'),
      h('ul', { class: 'ac-past' }, list.map((k) => h('li', null,
        h('span', { class: 'nm' }, k.name), this.rolePill(k.role),
        h('span', { class: 'dim' }, k.vouched ? `${fmt(k.vouched)} ${k.vouched === 1 ? 'change' : 'changes'} they made stay valid, approved by an admin` : 'nothing they made needs approval'),
        h('span', { class: 'ac-keyend' }, this.key(k.fingerprint_short, k.fingerprint, 'signing key fingerprint of ' + k.name))))));
  }

  // checks is what a person can look at to know that all is in order: the list, the roles and the records.
  checks(a) {
    return h('section', { class: 'ac-sec ac-checks', 'aria-labelledby': 'ac-h-checks' },
      h('h3', { id: 'ac-h-checks', class: 'h' }, 'Checks'),
      this.file(a),
      a.roles ? this.roles(a) : null,
      a.roles && a.roles.records ? this.recordCheck(a.roles.records) : null);
  }

  // roles says where the roles come from, with the history of them one disclosure away.
  roles(a) {
    const r = a.roles;
    const st = ROLE_STATE[r.state] || ROLE_STATE.off;
    return h('div', { class: `ac-chk ac-roles rs-${r.state}` },
      h('h4', { class: 'ac-chk-h' }, 'Roles', h('span', { class: `b ${st.cls}` }, icon(st.ic), st.label),
        r.you ? h('span', { class: 'ac-ryou' }, 'you are ', this.rolePill(r.you)) : null),
      h('p', { class: 'ac-rtext' }, r.text),
      (r.versions || []).length ? this.chain(r) : null);
  }

  // chain draws the versions of the roles in the state from the first to the newest. Each is signed by an admin of the
  // one before it, so the line between them is an arrow.
  chain(r) {
    const vs = r.versions;
    const d = h('details', { class: 'ac-hist' });
    d.append(h('summary', { 'data-fk': 'hist' }, `History of the roles · ${vs.length} ${vs.length === 1 ? 'version' : 'versions'}`),
      h('ol', { class: 'ac-chain', 'aria-label': 'Versions of the roles, oldest first' }, vs.map((v, i) => {
        const last = i === vs.length - 1;
        return h('li', { class: 'ac-ver' + (v.pinned ? ' pin' : '') + (last ? ' head' : ''), 'data-seq': String(v.seq) },
          h('div', { class: 'vh' }, h('b', { class: 'vn' }, 'v' + v.seq),
            v.pinned ? h('span', { class: 'b ok', title: 'The access list remembers this first version, so it cannot be swapped' }, icon('lock'), 'first') : null,
            last && !v.pinned ? h('span', { class: 'b', title: 'The newest version' }, 'newest') : null),
          h('p', { class: 'vm', title: stamp(v.at) }, relTime(v.at), ' · signed by ', h('b', null, v.by || 'unknown')),
          (v.changes || []).length ? h('ul', { class: 'vc' }, v.changes.map((x) => h('li', null, x.replace(' -> ', ' → ')))) : h('p', { class: 'vm dim' }, 'nobody gained or lost a role'),
          v.vouched ? h('p', { class: 'vv' }, h('span', { class: 'b lock' }, 'approved'), ` ${fmt(v.vouched)} ${v.vouched === 1 ? 'change' : 'changes'} by someone who left`) : null);
      })),
      ...(r.differs ? [h('p', { class: 'ac-note ac-differs' }, icon('bang'), 'The access list asks for other roles than the newest version holds. An admin saves them with the card under To do.')] : []));
    return d;
  }

  // recordCheck says whether the records were written by members who may write, as one bar of the four kinds there are.
  recordCheck(rc) {
    const parts = [
      { k: 'bound', n: rc.bound, label: 'written by a member who may write', cls: 'ok' },
      { k: 'vouched', n: rc.vouched, label: 'written by someone who left, approved by an admin', cls: 'lock' },
      { k: 'before', n: rc.before_roles, label: 'older than roles', cls: 'dim' },
      { k: 'unchecked', n: rc.unchecked, label: 'not checked yet: still running, or interrupted', cls: 'chg' },
    ].filter((p) => p.n > 0);
    const total = parts.reduce((x, p) => x + p.n, 0);
    const bad = (rc.problems || []).length + (rc.more_problems || 0);
    return h('div', { class: 'ac-chk ac-check' },
      h('h4', { class: 'ac-chk-h' }, 'Who wrote the records',
        rc.skipped ? null : (bad ? h('span', { class: 'b bad' }, icon('bang'), `${fmt(bad)} to look at`) : h('span', { class: 'b ok' }, icon('check'), 'all in order'))),
      rc.skipped ? h('p', { class: 'ac-rtext' }, 'Not checked here: ' + rc.skipped + '.') : [
        h('p', { class: 'ac-rtext' }, bad ? 'These records were written by someone who may not change the state, or whom nobody approved.' : 'Every record was written by a member who may change the state.'),
        total ? h('div', { class: 'ac-bar', role: 'img', 'aria-label': parts.map((p) => `${fmt(p.n)} ${p.label}`).join(', ') },
          parts.map((p) => h('span', { class: `seg ${p.cls}`, style: `--n:${p.n}`, title: `${fmt(p.n)} ${p.label}` }))) : null,
        h('ul', { class: 'ac-legend' }, parts.map((p) => h('li', { class: p.cls }, h('i', { class: 'sw', 'aria-hidden': 'true' }), h('b', null, fmt(p.n)), p.label))),
        bad ? h('ul', { class: 'ac-problems' },
          rc.problems.map((p) => h('li', null, h('code', null, p.addr), h('span', null, p.why))),
          rc.more_problems ? h('li', { class: 'dim' }, `and ${fmt(rc.more_problems)} more`) : null) : null]);
  }

  // open opens the card of a suggestion and moves the focus to it.
  open(id) {
    const card = Array.from(this.root.querySelectorAll('details.ac-card')).find((d) => d.dataset.id === id);
    if (!card) return;
    card.open = true;
    card.scrollIntoView({ block: 'nearest' });
    const sum = card.querySelector('summary');
    if (sum) sum.focus();
  }

  key(shown, full, what) {
    return h('span', { class: 'ac-key' }, h('code', { title: full }, shown),
      h('button', { class: 'cpbtn', type: 'button', 'data-copy': full, 'aria-label': `Copy the ${what}`, 'data-tip': 'Copy', 'data-fk': 'k:' + full }, icon('copy')));
  }

  // file is the verdict between the access list (the readers file) and the state.
  file(a) {
    const f = a.file;
    const v = VERDICT[f.verdict] || VERDICT.none;
    // The verdict is about the store, which a state that is not sealed has no locks for, except when the repository lists members.
    const sealed = a.state === 'sealed' || a.state === 'migrating' || f.verdict === 'expected-sealed';
    return h('div', { class: `ac-chk ac-file v-${f.verdict}` },
      h('h4', { class: 'ac-chk-h' }, 'Access list', sealed ? h('span', { class: `b ${v.cls}` }, icon(f.verdict === 'match' ? 'check' : 'bang'), v.label) : null),
      h('p', { class: 'ac-rtext' }, f.text),
      h('p', { class: 'ac-path' }, h('code', null, f.path), h('button', { class: 'cpbtn', type: 'button', 'data-copy': f.path, 'aria-label': 'Copy the path of the access list', 'data-tip': 'Copy the path', 'data-fk': 'path' }, icon('copy'))),
      (f.errors || []).length ? h('ul', { class: 'ac-errors' }, f.errors.map((e) => h('li', null, e.line > 0 ? [h('b', null, `line ${e.line}`), ' '] : null, e.reason))) : null);
  }

  // single warns that one reader holds the only lock: there is no recovery key, so losing that key loses the state.
  // The button opens the suggestion that adds a backup reader.
  single(a) {
    const w = a.single_reader;
    if (!w || !(a.suggestions || []).some((x) => x.id === w.suggestion)) return null;
    const go = () => this.open(w.suggestion);
    return h('section', { class: 'ac-warn', role: 'note', 'aria-labelledby': 'ac-h-single' },
      h('h3', { id: 'ac-h-single', class: 'sr-only' }, 'Only one member'),
      icon('bang'), h('p', null, w.text),
      h('button', { class: 'btn', type: 'button', 'data-fk': 'single', on: { click: go } }, w.action));
  }

  // recovery says how the state can be opened when every reader has lost their key: a key that is split into shares.
  // A warning shows when the recovery does not hold, and the button opens the suggestion that mends it.
  recovery(a) {
    const r = a.recovery;
    if (!r) return null;
    const go = () => this.open('fix-recovery');
    const fix = r.warning && (a.suggestions || []).some((x) => x.id === 'fix-recovery');
    return h('section', { class: 'ac-sec ac-recovery', 'aria-labelledby': 'ac-h-recovery' },
      h('h3', { id: 'ac-h-recovery', class: 'h' }, 'Recovery'),
      h('p', null, r.text, ' ', h('code', { title: r.key }, r.key_short)),
      r.warning ? h('div', { class: 'ac-warn', role: 'note' }, icon('bang'), h('p', null, r.warning),
        fix ? h('button', { class: 'btn', type: 'button', 'data-fk': 'recovery', on: { click: go } }, 'Bring the recovery in line') : null) : null);
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

  // todo is what the page suggests doing about the state as a whole. What is about one member is shown with that member.
  todo(a) {
    const list = (a.suggestions || []).filter((s) => !s.kind);
    const head = h('h3', { id: 'ac-h-sug', class: 'h' }, 'To do');
    if (!list.length) return h('section', { class: 'ac-sec', 'aria-labelledby': 'ac-h-sug' }, head, h('p', { class: 'ac-empty' }, 'Nothing needs doing.'));
    return h('section', { class: 'ac-sec', 'aria-labelledby': 'ac-h-sug' }, head,
      h('p', { class: 'ac-note first' }, 'Copy a step and run it yourself. Parts in ', h('span', { class: 'ph' }, '<angle brackets>'), ' are for you to fill in.'),
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
