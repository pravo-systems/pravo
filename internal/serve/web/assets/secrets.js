// The Secrets view: the secrets that the sealed state keeps, who set each one, and which nodes of the specification read it.
//
// A secret is a named value that the members of the state can read and nobody else. A run reads it from the sealed state and
// hands it to a provider; it reaches no record of the state and no plan. This page never shows a value: the server that feeds
// it holds no key and lists only what is in the clear, which is the name, the number of the version, who signed it and when.
// It says what is wrong, worst first, or says in one line that nothing is. What is long or rare, such as the history of a
// secret, is left to the command line.
//
// The page reads /api/secrets and shows it. Nothing here runs a command, writes a file or asks the server to. The text of
// every command comes from the server and is shown as text.

import { h, clear, icon, relTime, stamp, fmt } from './util.js';
import { cueText } from './ui.js';

export const FOOTER = 'This page shows the secrets. It runs nothing and writes nothing, and it never shows a value.';

// SHOWN is how many nodes a row names before the rest fold behind a button.
const SHOWN = 3;

// sentence is a reason as a sentence of its own: the server writes it to follow a name, in lower case.
function sentence(text) {
  const t = String(text || '').trim();
  if (!t) return '';
  const s = t[0].toUpperCase() + t.slice(1);
  return /[.!?]$/.test(s) ? s : s + '.';
}

export class SecretsView {
  // cb.onNode(addr) shows a node in the graph. cb.onState() opens the State page, which is where a state is sealed.
  constructor(root, cb = {}) {
    this.root = root;
    this.cb = cb;
    this.doc = null;
    this.opened = new Set(); // the secrets whose whole list of nodes is open, as the person left it
  }

  render({ secrets }) {
    const focus = document.activeElement && this.root.contains(document.activeElement) ? document.activeElement.dataset.fk : null;
    const top = this.root.scrollTop;
    clear(this.root);
    if (!secrets) {
      this.doc = null;
      this.root.append(h('div', { class: 'se' }, h('h2', { class: 'se-title' }, 'Secrets'), h('p', { class: 'se-empty' }, 'The secrets could not be read. The rest of the page is unaffected.')));
      return;
    }
    this.doc = secrets;
    const d = secrets;
    const issues = d.issues || [];
    this.root.append(h('div', { class: 'se' },
      this.head(d),
      this.attention(d),
      d.sealed && d.secrets.length ? this.table(d) : null,
      !issues.length && !d.secrets.length ? this.none(d) : null,
      this.notes(d),
      h('p', { class: 'se-foot' }, FOOTER)));
    this.root.scrollTop = top;
    if (focus) {
      const el = Array.from(this.root.querySelectorAll('[data-fk]')).find((x) => x.dataset.fk === focus);
      if (el) el.focus({ preventScroll: true });
    }
  }

  // head is the title and what a secret is, in two sentences. The square before the title has the colour of the worst
  // thing the page flags, and none when there is nothing to show.
  head(d) {
    const n = d.sealed ? d.secrets.length : 0;
    const issues = d.issues || [];
    const tone = issues.some((i) => i.level === 'bad') ? ' bad' : issues.length ? ' chg' : n ? '' : ' none';
    return h('header', { class: 'se-head' + tone },
      h('h2', { class: 'se-title', id: 'se-h' }, 'Secrets', n ? h('span', { class: 'se-n', 'aria-label': `${n} in the state` }, fmt(n)) : null),
      h('p', { class: 'se-lede' }, 'A secret is a named value that only the members of the state can read. A run takes it from the sealed state when it needs it, for the settings of a provider or a write-only attribute, and keeps it out of the state and out of every plan.'));
  }

  // attention is the one place that says whether something is wrong, and lists what is, worst first. A state with nothing
  // to flag says so in one line.
  attention(d) {
    const list = d.issues || [];
    if (!list.length) {
      if (!d.all_clear) return null;
      return h('section', { class: 'se-sec ac-clear', 'aria-labelledby': 'se-h-checks' },
        h('h3', { id: 'se-h-checks', class: 'h' }, 'Needs attention'),
        h('p', { class: 'ac-clearline' }, h('span', { class: 'b ok' }, icon('check'), 'all clear'), d.all_clear));
    }
    return h('section', { class: 'se-sec ac-attn', 'aria-labelledby': 'se-h-checks' },
      h('h3', { id: 'se-h-checks', class: 'h' }, `Needs attention · ${list.length}`),
      h('div', { class: 'ac-issues' }, list.map((i) => this.issue(i))));
  }

  issue(i) {
    const toState = i.kind === 'unsealed' && this.cb.onState;
    return h('div', { class: `ac-issue lv-${i.level}`, role: 'note' },
      icon('bang'),
      h('div', { class: 'ib' },
        h('h4', { class: 'ac-chk-h' }, i.title),
        h('p', null, i.text),
        (i.items || []).length ? h('ul', { class: 'se-items' },
          i.items.map((it) => this.item(it, i.kind)),
          i.more ? h('li', { class: 'se-more' }, `and ${fmt(i.more)} more`) : null) : null),
      toState ? h('button', { class: 'btn', type: 'button', 'data-fk': 'state', on: { click: () => this.cb.onState() } }, 'Open the State page') : null);
  }

  // item is one secret under an issue: what is wrong with it, the nodes it stops, and the command that mends it.
  item(it, kind) {
    return h('li', { class: 'se-item' },
      h('div', { class: 'se-ihead' },
        h('code', { class: 'se-name' }, it.name),
        kind === 'untrusted' && it.version ? h('span', { class: 'se-v' }, 'v' + it.version) : null),
      h('p', { class: 'se-why' }, sentence(it.why)),
      (it.used_by || []).length ? h('div', { class: 'se-iuse' }, h('span', { class: 'k' }, 'read by'), h('span', { class: 'se-chips' }, it.used_by.map((a) => this.chip({ addr: a })))) : null,
      it.command ? this.command(it.command) : null);
  }

  command(text) {
    return h('div', { class: 'cmdline se-cmd' }, h('span', { class: 'ps', 'aria-hidden': 'true' }, '$'), h('code', null, text),
      h('button', { class: 'cpbtn', type: 'button', 'data-copy': text, 'aria-label': 'Copy the command: ' + text, 'data-tip': 'Copy the command' }, icon('copy')));
  }

  // chip names a node of the specification. It shows the node in the graph.
  chip(u) {
    const go = this.cb.onNode;
    return h('button', { type: 'button', class: 'se-chip', 'data-fk': 'node:' + u.addr, title: `Show ${u.addr} in the graph`, on: { click: () => go && go(u.addr) } },
      h('code', null, u.addr),
      u.pin ? h('span', { class: 'se-tag', title: `Asks for version ${u.pin}` }, 'v' + u.pin) : null,
      u.number ? h('span', { class: 'se-tag', title: 'Reads the number of the version, not the value' }, 'number only') : null);
  }

  // users are the nodes that read a secret: the first few, and a button for the rest.
  users(e) {
    const list = e.used_by || [];
    if (!list.length) return h('span', { class: 'dim' }, '–');
    const open = this.opened.has(e.name);
    const shown = open ? list : list.slice(0, SHOWN);
    const fold = (on) => () => { if (on) this.opened.delete(e.name); else this.opened.add(e.name); this.render({ secrets: this.doc }); };
    return h('span', { class: 'se-chips' },
      shown.map((u) => this.chip(u)),
      list.length > SHOWN ? h('button', { type: 'button', class: 'se-fold', 'aria-expanded': open ? 'true' : 'false', 'data-fk': 'more:' + e.name, on: { click: fold(open) } },
        open ? 'fewer' : `and ${fmt(list.length - SHOWN)} more`) : null,
      e.used_more ? h('span', { class: 'dim' }, `and ${fmt(e.used_more)} more not listed`) : null);
  }

  // table is the secrets of the state, one row each. A row that is not in order carries the one flag that says so; the issues
  // above say what is wrong with it.
  table(d) {
    const head = ['Name', 'Version', 'Set by', 'When', 'Read by'];
    return h('section', { class: 'se-sec', 'aria-label': 'Secrets in the state' },
      h('div', { class: 'ac-tablewrap' },
        h('table', { class: 'ac-table se-table' },
          h('caption', { class: 'sr-only' }, 'Secrets in the state'),
          h('thead', null, h('tr', null, head.map((t) => h('th', { scope: 'col' }, t)))),
          h('tbody', null, d.secrets.map((e) => this.row(e))))));
  }

  row(e) {
    const gone = e.status === 'removed';
    return h('tr', { class: gone ? 'gone' : '' },
      h('th', { scope: 'row', 'data-label': 'Name' }, h('code', { class: 'nm' }, e.name), gone ? h('span', { class: 'b' }, 'removed') : null),
      h('td', { 'data-label': 'Version' }, h('b', { class: 'se-ver', title: e.versions > 1 ? `${e.versions} versions` : null }, 'v' + e.version)),
      h('td', { 'data-label': 'Set by' }, e.by ? h('span', { class: 'se-by' }, e.by) : h('span', { class: 'dim' }, '–'), this.flag(e)),
      h('td', { 'data-label': 'When' }, h('span', { title: stamp(e.at) }, relTime(e.at))),
      h('td', { 'data-label': 'Read by' }, this.users(e)));
  }

  // flag is the mark on a row whose signature the page could not accept.
  flag(e) {
    if (e.trust === 'problem' || e.trust === 'unconfirmed') return h('span', { class: 'b bad', title: 'No signed record of a writer accounts for this version. The issue above says more.' }, 'not trusted');
    if (e.bad > 0) return h('span', { class: 'b bad', title: 'An older version of this secret is flagged. The issue above says more.' }, 'older version flagged');
    if (e.trust === 'unchecked') return h('span', { class: 'b chg', title: 'The key that signed it is not in the access list.' }, 'key not listed');
    return null;
  }

  // none is a state that keeps no secret and is not asked for one: what it takes to have one.
  none(d) {
    if (!d.sealed) {
      return h('section', { class: 'se-none' },
        h('p', null, 'This state is not sealed, so it cannot keep a secret. Sealing it encrypts the records, and the secrets with them, for the members you list.'),
        this.cb.onState ? h('button', { class: 'btn', type: 'button', 'data-fk': 'state', on: { click: () => this.cb.onState() } }, 'Open the State page') : null);
    }
    const spec = 'db: pravo.#Secret & {name: "db_password"}';
    return h('section', { class: 'se-none' },
      h('p', null, 'No secret is stored. A member with a writer’s key sets one:'),
      this.command('pravo secret set db_password'),
      h('p', null, 'The specification reads it by name:'),
      h('div', { class: 'cmdline se-cmd' }, h('code', null, cueText(spec)),
        h('button', { class: 'cpbtn', type: 'button', 'data-copy': spec, 'aria-label': 'Copy the line of the specification', 'data-tip': 'Copy the line' }, icon('copy'))));
  }

  // notes are the quiet lines under the table: what the page could not judge, and where the rest is.
  notes(d) {
    const out = [];
    if (!d.spec_ok) out.push(h('p', { class: 'se-warn' }, icon('bang'), h('span', null, 'The specification does not load, so the page cannot say which nodes read a secret.')));
    if (d.note) out.push(h('p', { class: 'se-note' }, d.note));
    if ((d.unresolved || []).length) {
      out.push(h('div', { class: 'se-note' }, h('p', null, `${fmt(d.unresolved.length)} ${d.unresolved.length === 1 ? 'node reads a secret whose name is' : 'nodes read a secret whose name is'} not known until a command runs:`),
        h('span', { class: 'se-chips' }, d.unresolved.map((a) => this.chip({ addr: a })))));
    }
    if (d.sealed && d.secrets.length) out.push(h('p', { class: 'se-note' }, 'Who wrote each version of a secret: ', h('code', null, 'pravo secret history NAME'), '.'));
    return out.length ? h('div', { class: 'se-notes' }, out) : null;
  }
}
