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
import { cueText, busy } from './ui.js';
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
  return h('div', { class: 'dv-snip' }, h('pre', { class: 'dv-code' }, h('code', null, cueText(text))),
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
    this.expanded = new Map(); // discovery name -> whether the person opened or closed it; a card the person did not touch is closed unless it is the only one
    this.ackAll = new Set(); // the discoveries whose entry for all their unmanaged objects is open
    this.marks = dq.marks('');
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
    this.marks = dq.marks(this.q);
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
      h('pre', { class: 'dv-code' }, h('code', null, cueText('vms: $aws.#Discovery.aws_instance & {region: "eu-west-1"}'))),
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
    new QueryMenu(input, box, { id: 'qsug-dv', model: () => this.doc, suggest: dq.suggest, change: (v) => { clearTimeout(t); this.setQuery(v, true, true); } });
    return h('div', { class: 'dv-tools' }, box, this.pillsEl);
  }

  // setQuery puts a text in the filter box and draws the cards for it. notify tells the page, which keeps it in the
  // address; typed says that the box already holds the text, so that the caret stays where it is.
  setQuery(q, notify = false, typed = false) {
    this.q = q;
    this.f = dq.compile(q);
    this.marks = dq.marks(q);
    if (this.input && !typed && this.input.value !== q) this.input.value = q;
    const list = this.root.querySelector('.dv-list');
    if (list) { clear(list); this.fill(list); }
    this.summary();
    if (notify && this.cb.onQuery) this.cb.onQuery(q);
  }

  // redraw draws the cards again, for a card that was opened or closed or a button that was pressed, and leaves the filter box alone.
  redraw() {
    const list = this.root.querySelector('.dv-list');
    if (list) { clear(list); this.fill(list); }
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

  // isOpen says whether a card shows its body. The cards are closed until someone opens one, unless there is only one, or the
  // filter asks something of the objects, which is what the cards that it leaves are for; what a person chose stays.
  isOpen(x) {
    const v = this.expanded.get(x.name);
    if (v !== undefined) return v;
    return this.doc.discoveries.length === 1 || !!(this.f && this.f.items);
  }

  card(x) {
    const f = FRESH[x.freshness] || FRESH.unknown;
    const loud = x.counts.unmanaged > 0;
    const open = this.isOpen(x);
    const m = this.marks;
    const idn = 'dv-' + x.name.replace(/[^A-Za-z0-9_-]/g, '_');
    const lit = (on) => (on ? ' hit' : '');
    const head = h('header', { class: 'dv-chead' },
      h('div', { class: 'dv-ctitle' },
        h('h3', { id: idn, class: 'dv-name' },
          h('button', { class: 'dv-toggle', type: 'button', 'aria-expanded': open ? 'true' : 'false', 'aria-controls': idn + '-body', 'data-fk': 'fold:' + x.name,
            on: { click: () => { this.expanded.set(x.name, !open); this.redraw(); } } },
          h('span', { class: 'chev' + (open ? ' open' : ''), 'aria-hidden': 'true' }, icon('chev')), h('span', { class: 'nm' + lit(m.discovery(x)) }, x.name))),
        this.facet('button', [['type', x.type]], [x.type], `Filter by type ${x.type}: the list resource type the provider lists`, 'b ty' + lit(m.type(x))),
        this.facet('button', [['instance', dq.providerOf(x)]], [x.instance || x.provider], `Filter by the provider ${dq.providerOf(x)}`, 'b pv' + lit(m.provider(x))),
        this.facet('button', [['freshness', x.freshness]], [f.ic ? icon(f.ic) : null, f.label], `Filter by freshness ${x.freshness}`, `b ${f.cls}` + lit(m.freshness(x)))),
      // a card that is closed still says what it holds
      open || !x.run ? null : this.summaryOf(x));
    const card = h('section', { class: 'dv-card' + (loud ? ' loud' : '') + (open ? ' open' : ' closed') + ' f-' + x.freshness, 'aria-labelledby': idn, 'data-name': x.name }, head);
    if (!open) return card;
    const body = h('div', { class: 'dv-body', id: idn + '-body' });
    card.append(body);
    body.append(this.facts(x));
    if (FRESH_TEXT[x.freshness]) body.append(this.notice(x));
    if (x.problem) body.append(h('p', { class: 'dv-problem' }, icon('bang'), h('span', null, x.problem)));
    for (const w of (x.run && x.run.warnings) || []) body.append(h('p', { class: 'dv-problem soft' }, icon('bang'), h('span', null, w)));
    if (x.unreadable) body.append(h('p', { class: 'dv-problem soft' }, icon('lock'), h('span', null, `${plural(x.unreadable, 'record')} of this type could not be opened, so the objects ${x.unreadable === 1 ? 'it is' : 'they are'} may be listed as unmanaged.`)));
    if (x.run) body.append(this.counts(x));
    if (x.run && x.run.status === 'truncated') body.append(h('p', { class: 'dv-problem soft' }, icon('bang'), h('span', null, `The list was cut off at the limit of ${fmt(x.limit)} objects, so there may be more.`)));
    body.append(this.rows(x));
    const stale = this.stale(x);
    if (stale) body.append(stale);
    return card;
  }

  // summaryOf is the line that a closed card has: the objects of the last run by what they are, the unmanaged ones in colour.
  summaryOf(x) {
    const c = x.counts;
    const m = this.marks;
    const part = (n, cls, word) => h('li', { class: cls + (n ? '' : ' zero') + (m.status(word) ? ' hit' : '') }, h('b', null, fmt(n)), ' ', word);
    return h('ul', { class: 'dv-sum', 'aria-label': 'What the last run found' },
      part(c.unmanaged, 'u', 'unmanaged'), part(c.adopting, 'a', 'adopting'), part(c.acknowledged, 'k', 'acknowledged'), part(c.managed, 'm', 'managed'));
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

  // counts is the strip of the objects of the last run by what they are. Only the unmanaged ones are in colour. A count is a button
  // that shows those objects of this discovery.
  counts(x) {
    const c = x.counts;
    const m = this.marks;
    const part = (n, cls, word) => h('li', { class: cls + (n ? '' : ' zero') + (m.status(word) ? ' hit' : '') },
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
      ['acknowledged', 'Acknowledged', 'Known, and meant to stay as they are. An $ack entry of the discovery in the specification covers each of them.'],
    ];
    let any = false;
    for (const [status, title, why] of groups) {
      const list = by(status);
      if (!list.length) continue;
      any = true;
      const key = x.name + '\0' + status;
      const n = this.shown.get(key) || PAGE;
      const total = x.counts[status];
      const allOpen = this.ackAll.has(x.name);
      const canAckAll = status === 'unmanaged' && x.ack_all && !filtered;
      out.append(h('div', { class: 'dv-group g-' + status },
        h('h4', { class: 'dv-gh' + (this.marks.status(status) ? ' hit' : '') }, title, h('span', { class: 'n' }, filtered ? `${list.length} of ${fmt(total)}` : fmt(total)),
          canAckAll ? h('button', { class: 'btn dv-ackbtn', type: 'button', 'aria-pressed': allOpen ? 'true' : 'false', 'data-fk': 'ackall:' + x.name, on: { click: () => { if (allOpen) this.ackAll.delete(x.name); else this.ackAll.add(x.name); this.redraw(); } } }, 'Acknowledge all') : null),
        status !== 'adopting' ? h('p', { class: 'dv-why' }, why) : null,
        canAckAll && allOpen ? h('div', { class: 'dv-ackall' }, h('p', { class: 'dv-note' }, 'To leave all of them as they are, add this to the $ack list of the discovery and give the reason:'), snippet(x.ack_all, 'Copy the $ack entry for all of them', 'ackall-snip:' + x.name)) : null,
        h('ul', { class: 'dv-items' }, list.slice(0, n).map((it) => this.item(x, it))),
        list.length > n ? h('button', { class: 'btn dv-more', type: 'button', 'data-fk': 'more:' + key, on: { click: () => { this.shown.set(key, n + PAGE); this.redraw(); } } }, `Show ${Math.min(PAGE, list.length - n)} more of ${fmt(list.length - n)}`) : null));
    }
    if (!any) {
      const quiet = x.counts.unmanaged + x.counts.adopting + x.counts.acknowledged === 0;
      out.append(h('p', { class: 'dv-empty' }, filtered ? 'No object matches the filter.' : quiet ? (x.counts.managed ? `Every object the run found is managed (${fmt(x.counts.managed)}).` : 'The run found no object.') : ''));
    }
    if (x.items_omitted) out.append(h('p', { class: 'dv-note' }, `${fmt(x.items_omitted)} more are not listed here. Narrow the scope of the discovery.`));
    return out;
  }

  // item is one object. What can be done with it is behind buttons, one at a time: Adopt shows the resource that adopts it,
  // Acknowledge the entry that says it is known. The button that is open is pressed, and a second press closes it.
  item(x, it) {
    const k = x.name + '\0' + it.key;
    const mode = this.open.get(k) || null;
    const m = this.marks;
    const li = h('li', { class: 'dv-item s-' + it.status + (mode ? ' open' : ''), 'data-key': it.key });
    const lit = (on) => (on ? ' hit' : '');
    const id = h('div', { class: 'dv-id' },
      h('span', { class: 'nm' + lit(m.name(it)) }, it.name || it.id), it.name && it.name !== it.id && it.id !== it.key ? h('code', { class: 'id', title: it.key }, it.id) : null,
      it.status === 'adopting' ? h('span', { class: 'by' }, 'adopted by ', h('code', { class: lit(m.by(it)).trim() }, it.by)) : null);
    const ident = h('span', { class: 'dv-ident' }, (it.identity || []).filter((f) => f.value !== it.id && f.value !== it.name).map((f) => h('span', { class: 'sc' + lit(m.field(f)) }, h('b', null, f.name), '=', f.value)));
    const press = (which, label, cls, aria) => h('button', { class: cls, type: 'button', 'aria-pressed': mode === which ? 'true' : 'false', 'data-fk': which + ':' + k, 'aria-label': aria, on: { click: () => this.toggle(x, it, li, which) } }, label);
    let tail;
    if (it.status === 'unmanaged') {
      tail = h('span', { class: 'dv-acts' },
        press('ack', 'Acknowledge', 'btn dv-act', `Acknowledge ${it.name || it.id}: say that it is known and meant to stay as it is`),
        press('adopt', 'Adopt', 'btn primary dv-adopt dv-act', `Adopt ${it.name || it.id}`));
    } else if (it.status === 'acknowledged') {
      tail = h('span', { class: 'dv-ack', title: it.by }, it.reason ? h('span', { class: 'rs' }, '“', it.reason, '”') : h('span', { class: 'dim' }, 'no reason given'),
        press('adopt', 'adopt anyway', 'linkbtn', `How to adopt ${it.name || it.id} after all`));
    } else {
      tail = press('adopt', 'details', 'linkbtn', `Details of ${it.name || it.id}`);
    }
    li.append(h('div', { class: 'dv-row' }, id, ident, tail));
    if (mode) li.append(this.detail(x, it, mode));
    return li;
  }

  toggle(x, it, li, which) {
    const k = x.name + '\0' + it.key;
    const next = this.open.get(k) === which ? null : which;
    if (next) this.open.set(k, next); else this.open.delete(k);
    const fresh = this.item(x, it);
    li.replaceWith(fresh);
    const btn = fresh.querySelector('[data-fk="' + which + ':' + k.replace(/"/g, '\\"') + '"]');
    if (btn) btn.focus({ preventScroll: true });
    if (next && !this.adopt.has(k)) this.load(x, it, k);
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

  // detail is what the open button of an object says: the resource that adopts it, and the commands; or the entry that acknowledges it.
  detail(x, it, mode) {
    const k = x.name + '\0' + it.key;
    const a = this.adopt.get(k);
    const box = h('div', { class: 'dv-detail', role: 'group', 'aria-label': (mode === 'ack' ? 'Acknowledging ' : 'Adopting ') + (it.name || it.id) });
    if (!a) {
      box.append(busy('Reading the resource'));
      return box;
    }
    if (a.error) {
      box.append(h('p', { class: 'dv-problem' }, icon('bang'), h('span', null, a.error)));
      return box;
    }
    if (mode === 'ack' && a.ack) {
      box.append(h('p', { class: 'dv-lead' }, 'Say that it is known, and is meant to stay as it is. Add this to the $ack list of the discovery, and give the reason:'),
        snippet(a.ack, 'Copy the $ack entry', 'ack-snip:' + k));
    } else {
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
    }
    if (it.identity && it.identity.length) {
      box.append(h('details', { class: 'dv-idt' }, h('summary', null, 'Identity the provider gave'),
        h('dl', null, it.identity.map((f) => [h('dt', null, f.name), h('dd', null, h('code', null, f.value))]))));
    }
    return box;
  }

  // stale says which $ack entries of the specification cover no object of the last run. The rows of the acknowledged
  // objects already show every other entry, with its reason, so only these have something to add.
  stale(x) {
    const list = (x.acks || []).filter((a) => a.covers === 0);
    if (!list.length) return null;
    const pats = (a) => [...(a.ids || []).map((p) => ['id', p]), ...(a.names || []).map((p) => ['name', p])];
    return h('div', { class: 'dv-stale' },
      h('p', { class: 'dv-note' }, list.length === 1
        ? 'One $ack entry of the specification covers no object of the last run. Remove it, or check what it names:'
        : `${list.length} $ack entries of the specification cover no object of the last run. Remove them, or check what they name:`),
      h('ul', null, list.map((a) => h('li', null, h('code', null, a.ref),
        pats(a).map(([k, p]) => h('span', { class: 'pat' }, h('i', null, k), h('code', null, p))),
        a.reason ? h('span', { class: 'rs' }, '“', a.reason, '”') : null))));
  }
}
