// The Discovery view: what the discoveries of the specification found, and what each object is to the state.
//
// A discovery lists the objects that exist in a scope. The ones that a record of the state already is are counted
// and left out. The rest are the point of the page: the objects that nothing manages ask to be adopted, in the one
// colour of the page that means a decision is wanted; the ones that were acknowledged are listed quietly, with their
// reason, and ask for nothing; the ones that a resource is about to adopt say which. Objects are shown here and
// nowhere else: they are not in the graph or in the list of the Inventory.
//
// The page reads /api/discovery and /api/adopt and shows them. Nothing here runs a command, writes a file or asks the
// server to: a command is text that the server worded, with a button that copies it.

import { h, clear, icon, plural, relTime, stamp, fmt } from './util.js';
import * as api from './api.js';
import { Pills } from './pills.js';
import { QueryMenu, addTerm, removeTerm } from './query.js';
import * as dq from './dquery.js';

// FRESH says what the state of a discovery is and what to do about it.
const FRESH = {
  current: { cls: 'ok', label: 'current' },
  stale: { cls: 'chg', label: 'scope changed', ic: 'bang' },
  never: { cls: 'chg', label: 'not run', ic: 'hold' },
  broken: { cls: 'bad', label: 'cannot run', ic: 'bang' },
  undeclared: { cls: '', label: 'not in the specification' },
  unknown: { cls: '', label: 'not compared' },
};

const FRESH_TEXT = {
  stale: 'The scope changed since this ran, so the list below answers the old scope. Run it again to list the new one.',
  never: 'This discovery has not run. Run it to list what exists.',
  undeclared: 'The specification has no discovery of this name now. This is what its last run found.',
  unknown: 'The specification does not load, so the runs cannot be compared with it.',
};

// PAGE is how many rows of a group are drawn before the button that draws more.
const PAGE = 100;

export const FOOTER = 'This page suggests commands. It runs nothing and writes nothing.';

// withParts shows the parts of a text that a person fills in (words in angle brackets) apart from the rest.
function withParts(text) {
  return String(text).split(/(<[^<>]+>)/).map((p, i) => (i % 2 ? h('span', { class: 'ph' }, p) : p));
}

function command(text, placeholder, label, fk) {
  return h('div', { class: 'cmdline dv-cmd' }, h('span', { class: 'ps', 'aria-hidden': 'true' }, '$'), h('code', null, placeholder ? withParts(text) : text),
    h('button', { class: 'cpbtn', type: 'button', 'data-copy': text, 'data-fk': fk, 'aria-label': `${label}: ${text}`, 'data-tip': label }, icon('copy')));
}

// snippet is a block of CUE with a button that copies it.
function snippet(text, label, fk) {
  return h('div', { class: 'dv-snip' }, h('pre', { class: 'dv-code' }, h('code', null, withParts(text))),
    h('button', { class: 'cpbtn', type: 'button', 'data-copy': text, 'data-fk': fk, 'aria-label': label, 'data-tip': label }, icon('copy')));
}

export class DiscoveryView {
  constructor(root, cb = {}) {
    this.root = root;
    this.cb = cb;
    this.doc = null;
    this.q = ''; // the text of the filter box, which the fragment carries as dq=
    this.f = null; // what dq.compile made of it
    this.open = new Map(); // "discovery\0key" -> true: the rows whose adoption is open
    this.adopt = new Map(); // "discovery\0key" -> the answer of /api/adopt, or an error
    this.shown = new Map(); // "discovery\0status" -> rows drawn
    this.fold = new Map(); // discovery name -> true when the person closed it
    this.revision = '';
  }

  render({ discovery }) {
    if (!discovery) {
      clear(this.root);
      this.root.append(h('div', { class: 'dv' }, h('h2', { class: 'dv-title' }, 'Discovery'), h('p', { class: 'dv-empty' }, 'The discoveries could not be read. The rest of the page is unaffected.')));
      return;
    }
    if (discovery.revision !== this.revision) this.adopt.clear(); // an answer is about the objects of one revision
    this.revision = discovery.revision;
    this.doc = discovery;
    const focus = document.activeElement && this.root.contains(document.activeElement) ? document.activeElement.dataset.fk : null;
    const top = this.root.scrollTop;
    clear(this.root);
    const d = discovery;
    this.f = dq.compile(this.q);
    this.input = null;
    const list = d.discoveries.length ? h('div', { class: 'dv-list' }) : null;
    this.root.append(h('div', { class: 'dv' },
      this.head(d),
      (d.warnings || []).map((w) => h('p', { class: 'dv-warn', role: 'note' }, icon('bang'), h('span', null, w))),
      d.discoveries.length ? this.toolbar() : null,
      list || this.none(d),
      h('p', { class: 'dv-foot' }, FOOTER)));
    if (list) this.fill(list);
    this.summary();
    this.root.scrollTop = top;
    if (focus) {
      const el = Array.from(this.root.querySelectorAll('[data-fk]')).find((x) => x.dataset.fk === focus);
      if (el) el.focus({ preventScroll: true });
    }
  }

  // head is the sentence for the whole page: how many objects ask to be adopted, or that none does.
  head(d) {
    const n = d.unmanaged;
    const stale = d.discoveries.filter((x) => x.declared && (x.freshness === 'stale' || x.freshness === 'never')).length;
    const sentence = !d.discoveries.length
      ? 'The specification has no discoveries.'
      : n > 0
        ? `${plural(n, 'object')} that nothing manages ${n === 1 ? 'asks' : 'ask'} to be adopted.`
        : 'No object is waiting to be adopted.';
    return h('section', { class: 'dv-head' + (n > 0 ? ' loud' : ''), 'aria-labelledby': 'dv-h-title' },
      h('div', { class: 'dv-bhead' }, h('h2', { id: 'dv-h-title', class: 'dv-title' }, 'Discovery'),
        n > 0 ? h('span', { class: 'b chg dv-count' }, icon('bang'), `${fmt(n)} unmanaged`) : h('span', { class: 'b ok' }, icon('check'), 'nothing unmanaged')),
      h('p', { class: 'dv-sentence' }, sentence, stale ? ` ${plural(stale, 'discovery', 'discoveries')} ${stale === 1 ? 'needs' : 'need'} to run.` : ''),
      stale ? command(d.command, false, 'Copy the command that runs them', 'run-all') : null);
  }

  none(d) {
    return h('div', { class: 'dv-none' },
      h('p', null, 'A discovery asks a provider which objects exist in a scope, and shows the ones that nothing in the state accounts for. Write one in the specification:'),
      h('pre', { class: 'dv-code' }, h('code', null, 'vms: $aws.#Discovery.aws_instance & {region: "eu-west-1"}')),
      h('p', { class: 'dv-note' }, 'Then run ', h('code', null, d.command), ' and come back.'));
  }

  // toolbar is the filter box of the Inventory over the objects of the page: words, column:value terms, a menu that offers
  // the columns and the values that this page has, and a pill for each term.
  toolbar() {
    const input = h('input', { type: 'search', class: 'dv-q', id: 'dv-filter', placeholder: 'Filter, e.g. status:unmanaged type:s3', autocomplete: 'off', spellcheck: 'false', 'data-fk': 'q', value: this.q,
      'aria-label': 'Filter the objects by words, or by column:value such as status:unmanaged, type:aws_s3_bucket or field:region=eu' });
    this.input = input;
    this.countEl = h('span', { class: 'count', 'aria-live': 'polite' });
    this.pillsEl = h('div', { class: 'pills dv-pills', role: 'group', 'aria-label': 'Active filters' });
    this.pills = new Pills(this.pillsEl, { onRemoveTerm: (raw) => this.setQuery(removeTerm(this.q, raw), true) }, dq.LANG);
    const box = h('div', { class: 'filterbox' }, icon('search'), input, this.countEl);
    let t = 0;
    input.addEventListener('input', () => { clearTimeout(t); t = setTimeout(() => this.setQuery(input.value, true, true), 100); });
    new QueryMenu(input, box, { model: () => this.doc, suggest: dq.suggest, change: (v) => { clearTimeout(t); this.setQuery(v, true, true); } });
    return h('div', { class: 'dv-tools' }, box, this.pillsEl);
  }

  // setQuery puts a text in the filter box and draws the cards for it. notify tells the page, which keeps it in the
  // address; typed says that the box already holds the text, so that the caret stays where it is.
  setQuery(q, notify = false, typed = false) {
    this.q = q;
    this.f = dq.compile(q);
    if (this.input && !typed && this.input.value !== q) this.input.value = q;
    const list = this.root.querySelector('.dv-list');
    if (list) { clear(list); this.fill(list); }
    this.summary();
    if (notify && this.cb.onQuery) this.cb.onQuery(q);
  }

  // facet is a value on a card that a click makes a term of the filter.
  facet(tag, cols, label, tip, cls) {
    return h(tag, { class: cls + ' facet', type: 'button', 'data-tip': tip, 'aria-label': tip,
      on: { click: () => { let q = this.q; for (const [col, value] of cols) q = addTerm(q, col, value, false, dq.LANG); this.setQuery(q, true); } } }, ...label);
  }

  // summary says how many objects the filter leaves, and shows its terms as pills.
  summary() {
    if (!this.countEl || !this.doc) return;
    const all = dq.rowsOf(this.doc, null).length;
    const n = this.f ? dq.rowsOf(this.doc, this.f).length : all;
    this.countEl.textContent = this.f ? plural(n, 'match', 'matches') : plural(all, 'object');
    this.pills.render(this.q);
  }

  // fill draws the cards that the filter leaves: the discoveries that it names, and, when it asks something of the objects,
  // those that have one that matches.
  fill(list) {
    const f = this.f;
    const shown = this.doc.discoveries.filter((x) => !f || (f.card(x) && (!f.items || (x.items || []).some((it) => f.item(x, it)))));
    if (!shown.length) {
      list.append(h('div', { class: 'dv-empty dv-nomatch' }, h('span', null, 'No object matches the filter.'),
        h('button', { class: 'linkbtn', type: 'button', 'data-fk': 'clear-filter', on: { click: () => this.setQuery('', true) } }, 'Clear the filter')));
      return;
    }
    for (const x of shown) list.append(this.card(x));
  }

  card(x) {
    const f = FRESH[x.freshness] || FRESH.unknown;
    const loud = x.counts.unmanaged > 0;
    const folded = this.fold.get(x.name) === true;
    const idn = 'dv-' + x.name.replace(/[^A-Za-z0-9_-]/g, '_');
    const head = h('header', { class: 'dv-chead' },
      h('div', { class: 'dv-ctitle' },
        h('h3', { id: idn, class: 'dv-name' }, this.facet('button', [['discovery', x.name]], [x.name], `Show only ${x.name}`, 'nm')),
        this.facet('button', [['type', x.type]], [x.type], `Filter by type ${x.type}: the list resource type the provider lists`, 'b ty'),
        this.facet('button', [['instance', dq.providerOf(x)]], [x.instance || x.provider], `Filter by the provider ${dq.providerOf(x)}`, 'b pv'),
        h('span', { class: `b ${f.cls}` }, f.ic ? icon(f.ic) : null, f.label)),
      h('button', { class: 'iconbtn dv-fold', type: 'button', 'aria-expanded': folded ? 'false' : 'true', 'aria-label': (folded ? 'Open ' : 'Close ') + x.name, 'data-fk': 'fold:' + x.name, 'data-tip': folded ? 'Open' : 'Close',
        on: { click: () => { this.fold.set(x.name, !folded); this.render({ discovery: this.doc }); } } }, icon('chev', folded ? '' : 'open')));
    const card = h('section', { class: 'dv-card' + (loud ? ' loud' : '') + ' f-' + x.freshness, 'aria-labelledby': idn, 'data-name': x.name }, head);
    if (folded) return card;
    card.append(this.facts(x));
    if (FRESH_TEXT[x.freshness]) card.append(this.notice(x));
    if (x.problem) card.append(h('p', { class: 'dv-problem' }, icon('bang'), h('span', null, x.problem)));
    for (const w of (x.run && x.run.warnings) || []) card.append(h('p', { class: 'dv-problem soft' }, icon('bang'), h('span', null, w)));
    if (x.unreadable) card.append(h('p', { class: 'dv-problem soft' }, icon('lock'), h('span', null, `${plural(x.unreadable, 'record')} of this type could not be opened, so the objects ${x.unreadable === 1 ? 'it is' : 'they are'} may be listed as unmanaged.`)));
    if (x.run) card.append(this.counts(x));
    if (x.run && x.run.status === 'truncated') card.append(h('p', { class: 'dv-problem soft' }, icon('bang'), h('span', null, `The list was cut off at the limit of ${fmt(x.limit)} objects, so there may be more.`)));
    card.append(this.rows(x));
    if ((x.acks || []).length) card.append(this.acks(x));
    return card;
  }

  facts(x) {
    const li = (k, ...v) => h('li', null, h('span', { class: 'k' }, k), ...v);
    return h('ul', { class: 'dv-facts' },
      (x.scope || []).length || x.limit ? li('scope', h('span', { class: 'dv-scope' }, (x.scope || []).map((f) => h('span', { class: 'sc' }, h('b', null, f.name), '=', f.value)), x.limit ? h('span', { class: 'sc' }, h('b', null, 'limit'), '=', String(x.limit)) : null)) : li('scope', h('span', { class: 'dim' }, 'none: everything the provider can list')),
      x.run ? li('last run', h('span', { title: stamp(x.run.finished) }, relTime(x.run.finished)), x.run.actor ? h('span', { class: 'dim' }, ' by ' + x.run.actor) : null) : null,
      x.source ? li('written at', h('code', null, `${x.source.file}:${x.source.line}`)) : null);
  }

  notice(x) {
    const run = x.declared && x.freshness !== 'undeclared' && x.freshness !== 'unknown';
    return h('div', { class: 'dv-notice n-' + x.freshness }, h('p', null, FRESH_TEXT[x.freshness]), run ? command(x.command, false, 'Copy the command that runs it', 'run:' + x.name) : null);
  }

  // counts is the strip of the objects of the last run by what they are. Only the unmanaged ones are in colour.
  counts(x) {
    const c = x.counts;
    const part = (n, cls, word) => h('li', { class: cls + (n ? '' : ' zero') },
      n && word !== 'managed' ? this.facet('button', [['discovery', x.name], ['status', word]], [h('b', null, fmt(n)), ' ', word], `Show the ${word} objects of ${x.name}`, 'ct') : [h('b', null, fmt(n)), ' ', word]);
    return h('ul', { class: 'dv-counts', 'aria-label': 'What the last run found' },
      part(c.unmanaged, 'u', 'unmanaged'), part(c.adopting, 'a', 'adopting'), part(c.acknowledged, 'k', 'acknowledged'), part(c.managed, 'm', 'managed'),
      h('li', { class: 'dim tot' }, `of ${fmt(x.run.total)} found`));
  }

  // rows are the objects that are not managed, in groups: the unmanaged ones first.
  rows(x) {
    const f = this.f;
    const filtered = !!(f && f.items);
    const out = h('div', { class: 'dv-rows' });
    if (!x.run) return out;
    const by = (s) => (x.items || []).filter((it) => it.status === s && (!f || f.item(x, it)));
    const groups = [
      ['unmanaged', 'Unmanaged', 'Nothing manages these. Adopt them, or acknowledge them if they are meant to stay as they are.'],
      ['adopting', 'Adopting', 'A resource of the specification names these in $import. The next plan reads them.'],
      ['acknowledged', 'Acknowledged', 'Known, and left as they are.'],
    ];
    let any = false;
    for (const [status, title, why] of groups) {
      const list = by(status);
      if (!list.length) continue;
      any = true;
      const key = x.name + '\0' + status;
      const n = this.shown.get(key) || PAGE;
      const total = x.counts[status];
      out.append(h('div', { class: 'dv-group g-' + status },
        h('h4', { class: 'dv-gh' }, title, h('span', { class: 'n' }, filtered ? `${list.length} of ${fmt(total)}` : fmt(total))),
        status === 'unmanaged' ? h('p', { class: 'dv-why' }, why) : null,
        h('ul', { class: 'dv-items' }, list.slice(0, n).map((it) => this.item(x, it))),
        list.length > n ? h('button', { class: 'btn dv-more', type: 'button', 'data-fk': 'more:' + key, on: { click: () => { this.shown.set(key, n + PAGE); this.setQuery(this.q); } } }, `Show ${Math.min(PAGE, list.length - n)} more of ${fmt(list.length - n)}`) : null,
        status === 'unmanaged' && x.ack_all && !filtered ? h('div', { class: 'dv-ackall' }, h('p', { class: 'dv-note' }, 'To leave all of them as they are, add this to the $ack list of the discovery and give the reason:'), snippet(x.ack_all, 'Copy the $ack entry for all of them', 'ackall:' + x.name)) : null));
    }
    if (!any) {
      const quiet = x.counts.unmanaged + x.counts.adopting + x.counts.acknowledged === 0;
      out.append(h('p', { class: 'dv-empty' }, filtered ? 'No object matches the filter.' : quiet ? (x.counts.managed ? `Every object the run found is managed (${fmt(x.counts.managed)}).` : 'The run found no object.') : ''));
    }
    if (x.items_omitted) out.append(h('p', { class: 'dv-note' }, `${fmt(x.items_omitted)} more are not listed here. Narrow the scope of the discovery.`));
    return out;
  }

  item(x, it) {
    const k = x.name + '\0' + it.key;
    const opened = this.open.get(k) === true;
    const li = h('li', { class: 'dv-item s-' + it.status + (opened ? ' open' : ''), 'data-key': it.key });
    const id = h('div', { class: 'dv-id' },
      h('span', { class: 'nm' }, it.name || it.id), it.name && it.name !== it.id && it.id !== it.key ? h('code', { class: 'id', title: it.key }, it.id) : null,
      it.status === 'adopting' ? h('span', { class: 'by' }, 'adopted by ', h('code', null, it.by)) : null);
    const ident = h('span', { class: 'dv-ident' }, (it.identity || []).filter((f) => f.value !== it.id && f.value !== it.name).map((f) => h('span', { class: 'sc' }, h('b', null, f.name), '=', f.value)));
    let tail = null;
    if (it.status === 'unmanaged') {
      tail = h('button', { class: 'btn primary dv-adopt', type: 'button', 'aria-expanded': opened ? 'true' : 'false', 'data-fk': 'adopt:' + k,
        'aria-label': `Adopt ${it.name || it.id}`, on: { click: () => this.toggle(x, it, li) } }, 'Adopt');
    } else if (it.status === 'acknowledged') {
      tail = h('span', { class: 'dv-ack', title: it.by }, it.reason ? h('span', { class: 'rs' }, '“', it.reason, '”') : h('span', { class: 'dim' }, 'no reason given'),
        h('button', { class: 'linkbtn', type: 'button', 'aria-expanded': opened ? 'true' : 'false', 'data-fk': 'adopt:' + k, 'aria-label': `How to adopt ${it.name || it.id} after all`, on: { click: () => this.toggle(x, it, li) } }, 'adopt anyway'));
    } else {
      tail = h('button', { class: 'linkbtn', type: 'button', 'aria-expanded': opened ? 'true' : 'false', 'data-fk': 'adopt:' + k, on: { click: () => this.toggle(x, it, li) } }, 'details');
    }
    li.append(h('div', { class: 'dv-row' }, id, ident, tail));
    if (opened) li.append(this.detail(x, it));
    return li;
  }

  toggle(x, it, li) {
    const k = x.name + '\0' + it.key;
    const on = !(this.open.get(k) === true);
    this.open.set(k, on);
    const fresh = this.item(x, it);
    li.replaceWith(fresh);
    const btn = fresh.querySelector('[data-fk="adopt:' + k.replace(/"/g, '\\"') + '"]');
    if (btn) btn.focus({ preventScroll: true });
    if (on && !this.adopt.has(k)) this.load(x, it, k);
  }

  async load(x, it, k) {
    try {
      this.adopt.set(k, await api.adopt(x.name, it.key));
    } catch (err) {
      if (err instanceof api.Unauthorized || err instanceof api.Unreachable) return;
      this.adopt.set(k, { error: err.status === 404 ? 'This object is not in the last run any more.' : err.message });
    }
    const row = Array.from(this.root.querySelectorAll('.dv-card')).find((c) => c.dataset.name === x.name);
    const cur = row && Array.from(row.querySelectorAll('.dv-item')).find((e) => e.dataset.key === it.key);
    if (cur && this.open.get(k)) cur.replaceWith(this.item(x, it));
  }

  // detail is what to do with one object: the resource that adopts it, and the commands.
  detail(x, it) {
    const k = x.name + '\0' + it.key;
    const a = this.adopt.get(k);
    const box = h('div', { class: 'dv-detail', role: 'group', 'aria-label': `Adopting ${it.name || it.id}` });
    if (!a) {
      box.append(h('p', { class: 'dv-note' }, 'Reading…'));
      return box;
    }
    if (a.error) {
      box.append(h('p', { class: 'dv-problem' }, icon('bang'), h('span', null, a.error)));
      return box;
    }
    // An object that a resource adopts already has its resource: only the note applies.
    if (a.status !== 'adopting') {
      box.append(
        h('p', { class: 'dv-lead' }, 'Add this to the specification. The next plan reads the object and shows how it differs from what you wrote.'),
        snippet(a.snippet, 'Copy the resource', 'snip:' + k));
    }
    (a.steps || []).forEach((s, i) => {
      if (s.kind === 'command') box.append(command(s.text, !!s.placeholder, 'Copy the command', `step:${k}:${i}`));
      else box.append(h('p', { class: 'dv-note' }, s.text));
    });
    if (a.ack) {
      box.append(h('p', { class: 'dv-note' }, 'Or say that it is known, and is meant to stay as it is. Add this to the $ack list of the discovery, and give the reason:'),
        snippet(a.ack, 'Copy the $ack entry', 'ack:' + k));
    }
    if (it.identity && it.identity.length) {
      box.append(h('details', { class: 'dv-idt' }, h('summary', null, 'Identity the provider gave'),
        h('dl', null, it.identity.map((f) => [h('dt', null, f.name), h('dd', null, h('code', null, f.value))]))));
    }
    return box;
  }

  // acks lists the $ack entries of a discovery as the specification has them: which objects each covers and why.
  acks(x) {
    return h('details', { class: 'dv-acks' }, h('summary', null, `Acknowledged in the specification · ${x.acks.length}`),
      h('ul', null, x.acks.map((a) => this.ack(a))));
  }

  ack(a) {
    return h('li', { class: 'dv-ackrec' },
      h('div', { class: 'top' }, h('code', null, a.ref),
        a.covers >= 0 ? h('span', { class: 'b' }, `covers ${plural(a.covers, 'object')}`) : null),
      h('div', { class: 'pats' }, (a.ids || []).map((p) => h('span', { class: 'pat' }, h('i', null, 'id'), h('code', null, p))), (a.names || []).map((p) => h('span', { class: 'pat' }, h('i', null, 'name'), h('code', null, p)))),
      a.reason ? h('p', { class: 'rs' }, '“', a.reason, '”') : null);
  }
}
