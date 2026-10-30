// The plans stored in the state: the list in the rail, the plan bar on the canvas, the badges that name an
// author, and the detail of one plan. A stored plan is a plan that someone made with "pravo plan --store" and
// that anyone who reads the state finds. The page only reads: nothing here applies a plan, approves it or
// removes it. What it offers is the exact command that applies a plan, to copy and run elsewhere.

import { h, s as svgEl, clear, icon, relTime, relUntil, stamp, plural, signatureMark, actionOf } from './util.js';
import { Pills } from './pills.js';
import { QueryMenu, addTerm, removeTerm, removeValues } from './query.js';
import * as pq from './pquery.js';
import { touchAction, planCounts, countWords } from './plancount.js';

// STATUS says where a plan stands. The word is the carrier; the colour agrees with it, and with the history of the
// transactions: blue can still be applied (the whole of it or what is left), green was applied, amber moved on,
// grey is over. A plan that is partly stale is amber: the state moved on under some of its changes, and the rest
// can be applied.
export const STATUS = {
  pending: { cls: 'open', tip: 'It can be applied.' },
  partial: { cls: 'open', tip: 'Some of its changes were applied; the rest can be.' },
  'partly-stale': { cls: 'half', tip: 'The state moved on under some of its changes. The rest can be applied by name.' },
  stale: { cls: 'chg', tip: 'The state moved on, so it can no longer be applied.' },
  applied: { cls: 'done', tip: 'Every change was applied.' },
  expired: { cls: 'gone', tip: 'It was not applied before it expired.' },
};

// initials are the letters of an author that the badges show: "ana@acme" is A, "ben.ortiz@acme" is BO.
export function initials(author) {
  const name = String(author || '?').split('@')[0];
  const parts = name.split(/[\s._-]+/).filter(Boolean);
  // by code points: a letter outside the basic plane is two UTF-16 units, and half of one is no letter
  const first = (w) => Array.from(w)[0] || '?';
  const out = parts.length > 1 ? first(parts[0]) + first(parts[1]) : first(parts[0] || '?');
  return out.toUpperCase();
}

// hueOf picks one of the six hues of the providers' tints for an author, so that two authors with the same
// initial usually differ. The hues are clear of green, amber and red, which belong to the plan.
export function hueOf(author) {
  let x = 0;
  for (const c of String(author || '')) x = (x * 31 + c.charCodeAt(0)) >>> 0;
  return x % 6;
}

// avatar is the badge of an author in the rail and the inspector (the graph draws its own in SVG).
export function avatar(author, cls) {
  return h('span', { class: `au au${hueOf(author)}${cls ? ' ' + cls : ''}`, 'aria-hidden': 'true', 'data-tip': author }, initials(author));
}

// statusLabel is the word for a status: "partly stale" for partly-stale, the status itself for the others
export const statusLabel = (status) => String(status || '').replace(/-/g, ' ');

export function statusChip(status) {
  const st = STATUS[status] || STATUS.expired;
  return h('span', { class: 'b ps ps-' + st.cls, 'data-tip': st.tip }, status === 'applied' ? icon('check') : null, statusLabel(status));
}

// expiryRing is the small ring that says how much of the life of a plan that can be applied is left: grey for the time
// that has gone and blue, the colour of a plan that can be applied, for the time that remains, amber under a day and
// red under three hours.
// The words come with it when the time is short, and the tooltip always has them.
export function expiryRing(p, now = Date.now()) {
  if (!p.open || !p.created || !p.expires) return null;
  const start = Date.parse(p.created), end = Date.parse(p.expires);
  if (!(end > start)) return null;
  const left = Math.max(0, end - now);
  const hours = left / 36e5;
  const cls = hours < 3 ? 'bad' : hours < 24 ? 'chg' : 'open';
  const size = 16, r = 6, c = 2 * Math.PI * r, mid = size / 2;
  const svg = svgEl('svg', { width: size, height: size, viewBox: `0 0 ${size} ${size}`, 'aria-hidden': 'true' },
    svgEl('circle', { class: 'rg-track', cx: mid, cy: mid, r, fill: 'none', 'stroke-width': 1.75 }),
    svgEl('circle', { class: 'rg-left', cx: mid, cy: mid, r, fill: 'none', 'stroke-width': 1.75, 'stroke-linecap': 'round', 'stroke-dasharray': `${c * Math.min(1, left / (end - start))} ${c}`, transform: `rotate(-90 ${mid} ${mid})` }));
  const when = relUntil(p.expires, now);
  return h('span', { class: 'rg rg-' + cls, 'data-tip': 'Expires ' + when + '\n' + stamp(p.expires), role: 'img', 'aria-label': 'expires ' + when }, svg, hours < 24 ? h('span', { class: 'rg-t' }, when) : null);
}

// conflictAt says whether a plan has a change on the resource that it cannot apply now (it is partly stale)
export const conflictAt = (p, addr) => !!p && (p.touches || []).some((t) => t.addr === addr && t.conflict);

// conflictCount is how many changes of a partly stale plan cannot be applied now, those that the list leaves out included
const conflictCount = (p) => (p.conflicts || []).length + (p.conflicts_omitted || 0);

// counts are the adds, changes and destroys of a plan, in the colours of the actions, and the imports and the moves that the summary
// leaves out (see planCounts). A plan is never said to have no changes while it has some.
export function counts(c, cls = '') {
  const parts = [];
  if (c.add) parts.push(h('b', { class: 'ok' }, '+' + c.add));
  if (c.change) parts.push(h('b', { class: 'chg' }, '~' + c.change));
  if (c.destroy) parts.push(h('b', { class: 'bad' }, '−' + c.destroy));
  if (c.import) parts.push(h('b', { class: 'dim' }, c.import + ' imported'));
  if (c.move) parts.push(h('b', { class: 'dim' }, c.move + ' moved'));
  if (c.policies) parts.push(h('b', { class: 'chg' }, plural(c.policies, 'policy', 'policies')));
  if (!parts.length) parts.push(h('b', { class: 'dim' }, c.read ? plural(c.read, 'read') : c.planned ? plural(c.planned, 'change') : 'no changes'));
  return h('span', { class: 'pcnt' + (cls ? ' ' + cls : ''), 'aria-hidden': 'true' }, parts);
}

// signerOf is the verified signer of a plan when it is not the author, from the signatures that the overlay of the plan
// carries. The lists do not check signatures: a row shows the name once its overlay has been read, as it does the reason.
export function signerOf(o, author) {
  const sg = o && o.plan && o.plan.signatures;
  const it = sg && !sg.note ? (sg.items || []).find((x) => x.status === 'verified' && x.signer) : null;
  return it && it.signer !== author ? it.signer : null;
}

// describe is the label of a plan for a screen reader
export function describe(p, reason) {
  const bits = [reason || p.id, reason ? p.id : null, statusLabel(p.status), `by ${p.author}`, relTime(p.created), countWords(planCounts(p), p.status === 'applied')];
  if (conflictCount(p)) bits.push(plural(conflictCount(p), 'change') + (conflictCount(p) === 1 ? ' conflicts' : ' conflict'));
  if ((p.overlaps || []).length) bits.push('overlaps ' + plural(p.overlaps.length, 'other plan'));
  if (p.applying) bits.push('an apply is running');
  return bits.filter(Boolean).join(', ');
}

// the command that applies the plan, in the look of the other commands of the page; for a plan that is partly
// stale it is the command that applies the part that can be, with --only, which the server words (apply_live).
// A partly stale plan whose part has no command (it is too long) has none to give.
export function applyCommand(p) {
  const cmd = p.status === 'partly-stale' ? p.apply_live : p.apply || 'pravo apply ' + p.id;
  if (!cmd) return null;
  const what = p.status === 'partly-stale' ? `the part of ${p.id} that can be applied` : p.id;
  return h('div', { class: 'cmdline' + (p.status === 'partly-stale' ? ' live' : '') }, h('span', { class: 'ps', 'aria-hidden': 'true' }, '$'), h('code', null, cmd),
    h('button', { class: 'cpbtn', type: 'button', 'data-copy': cmd, 'data-fk': 'copy', 'aria-label': 'Copy the command that applies ' + what, 'data-tip': 'Copy the command' }, icon('copy')));
}

// conflictsBox says which changes of a partly stale plan are left out and why: the reasons begin with the address
export function conflictsBox(p) {
  const n = conflictCount(p), left = p.unapplied || n;
  const list = (p.conflicts || []).map((c) => h('li', null, (c.reasons || []).join('; ') || c.addr));
  if (p.conflicts_omitted) list.push(h('li', { class: 'dim' }, `and ${p.conflicts_omitted} more`));
  return h('div', { class: 'rp-why' }, h('b', null, `Partly stale: ${n} of ${left} unapplied ${left === 1 ? 'change conflicts' : 'changes conflict'} with the state and ${n === 1 ? 'is' : 'are'} left out.`), h('ul', null, list));
}

// planActions is what the page gives to do with an open plan: the command that applies it, and for a plan that is
// partly stale the words that say what the command leaves out
export function planActions(p) {
  if (p.status !== 'partly-stale') return [applyCommand(p)];
  const cmd = applyCommand(p);
  if (cmd) return [h('p', { class: 'rp-note' }, 'The rest can be applied by name:'), cmd];
  const n = (p.apply_only || []).length + (p.apply_only_omitted || 0);
  return [h('p', { class: 'rp-note' }, h('span', null, `The part that can be applied is ${plural(n, 'change')}, too many for one command. `, h('code', null, `pravo plan show ${p.id}`), ' lists them.'))];
}

// facts are what the inspector says of a plan, as rows of a list: by whom, when it lapses, why, what it was made
// with. The list in the rail keeps to what a person acts on (the reason, the overlaps, the command).
// The reason, the command and the revision are in the overlay of the plan (text), not in the list.
export function facts(p, text) {
  text = text || {};
  return [
    ['made by', [p.author, h('span', { class: 'dim', title: stamp(p.created) }, ' · ' + relTime(p.created))]],
    signerOf(text, p.author) ? ['signed by', signerOf(text, p.author)] : null,
    ['expires', h('span', { title: stamp(p.expires) }, p.status === 'expired' ? 'expired ' + relTime(p.expires) : relUntil(p.expires))],
    text.reason ? ['reason', text.reason] : null,
    text.command ? ['command', h('code', { class: 'cmd' }, text.command)] : null,
    text.git_revision ? ['git', h('code', null, text.git_revision)] : null,
  ].filter(Boolean);
}

// detail is what the rail shows under the plan that is laid over the graph: the reason, what stops it or crosses
// it, and the command that applies it.
export function detail(p, { onPlan, byId, text, changes, policies } = {}) {
  const body = [];
  text = text || {};
  if (text.reason && text.reason.length > 60) body.push(h('p', { class: 'rp-reason', title: text.reason }, text.reason));
  if (p.applying) body.push(h('p', { class: 'rp-note chg' }, icon('hold'), h('span', null, 'An apply from this plan is running.')));
  if (p.applied && p.planned > 0) body.push(h('p', { class: 'rp-note' }, `${p.applied} of ${p.planned} changes applied.`));
  if ((p.reasons || []).length) {
    body.push(h('div', { class: 'rp-why' }, h('b', null, p.status === 'stale' ? 'Stale: it can no longer be applied.' : 'Why'), h('ul', null, p.reasons.map((r) => h('li', null, r)))));
  }
  if (p.status === 'partly-stale') body.push(conflictsBox(p));
  if (changes) body.push(changes);
  if (policies) body.push(policies);
  if ((p.overlaps || []).length) {
    body.push(h('div', { class: 'rp-why ovl' }, h('b', null, 'Overlaps: applying one makes the other stale.'),
      h('ul', null, p.overlaps.map((o) => {
        const other = byId && byId.get(o.id);
        return h('li', null, h('button', { class: 'linkbtn', type: 'button', 'data-plan': o.id, 'data-fk': 'ovl-' + o.id, on: onPlan ? { click: () => onPlan(o.id) } : null }, o.id),
          other ? ' ' + other.author : '', h('span', { class: 'dim' }, ' · ' + plural(o.count, 'resource')));
      }))));
  }
  // what else is known of the plan: the author and the age are in its row, and the reason is above
  body.push(h('dl', { class: 'meta rp-more' }, facts(p, text).filter(([k]) => k !== 'reason' && k !== 'made by').map(([k, v]) => [h('dt', null, k), h('dd', null, v)])));
  if (text.plan && text.plan.approvals) body.push(...approvalsBox(p, text.plan.approvals));
  if (p.open) body.push(...planActions(p));
  else body.push(h('p', { class: 'rp-note dim' }, p.status === 'applied' ? 'Every change of this plan was applied.' : p.status === 'expired' ? 'This plan expired and will be collected.' : 'Make the plan again with pravo plan --store.'));
  return body;
}

// changeRow is what a plan does to a resource, as a row of its detail: the action, the address, the type, and whether the change was
// applied, or cannot be now. A click goes to the resource, with the plan laid over the graph.
function changeRow(p, c, env, onChange) {
  const act = touchAction(c);
  const a = actionOf(act);
  const type = pq.infoOf(c, env).type;
  return h('li', null, h('button', { class: 'rp-chg', type: 'button', data: { addr: c.addr, plan: p.id },
    'data-tip': 'Show it on the resources, with this plan laid over them', 'aria-label': `${c.addr}, ${a ? a.label : act}${c.applied ? ', applied' : ''}${c.conflict ? ', conflicts with the state' : ''}. Show it on the resources`,
    on: { click: () => onChange && onChange(p.id, c.addr) } },
  a ? h('i', { class: 'sq ' + a.cls, 'data-tip': a.label }) : h('i', { class: 'sq' }),
  h('code', null, c.addr),
  type ? h('span', { class: 'rp-ct' }, type) : null,
  c.applied ? h('span', { class: 'b ok' }, icon('check'), 'applied')
    : c.conflict ? h('span', { class: 'b chg', 'data-tip': 'The state moved on under this resource, or under one that this change has to be applied with. The plan cannot apply it now.' }, 'conflict') : null));
}

// Plans is the list in the rail: one row for each plan, the finder that narrows the list by words and column:value terms, the filter that
// keeps the pending ones, and the detail of the plan that is laid over the graph, with what the plan changes. The rows are toggle buttons
// with the arrow keys of a list.
const ROWS = 8; // the changes that a detail lists before it offers the rest
const GLANCE = 3; // the changes that a row names when the finder asks for them

export class Plans {
  constructor(root, cb) {
    this.root = root;
    this.cb = cb;
    this.filter = null; // null: pending if any plan is, else all
    this.open = null; // the plan whose detail is shown under its row
    this.s = null;
    this.q = ''; // the text of the finder, which the fragment carries as plq=
    this.f = null; // what pq.compile made of it
    this.env = null;
    this.whole = new Set(); // the plans whose detail lists every change
    this.tools = this.toolbar();
    root.addEventListener('click', (ev) => {
      const f = ev.target.closest('button[data-filter]');
      if (f) { this.filter = f.dataset.filter; this.render(this.s); return; }
      const all = ev.target.closest('button[data-showall]');
      if (all) { this.filter = 'all'; this.render(this.s); return; }
      const view = ev.target.closest('button.rp-view');
      if (view) { cb.onView(view.dataset.id); return; }
      const row = ev.target.closest('button.rp');
      if (row) this.toggle(row.dataset.id);
    });
    root.addEventListener('keydown', (ev) => this.onKey(ev));
  }

  // toolbar is the filter box of the Inventory over the plans and what they change: words, column:value terms, a menu that offers the
  // columns and the values that the plans have, and a pill for each term.
  toolbar() {
    const input = h('input', { type: 'search', class: 'rp-q', id: 'pl-filter', placeholder: 'Find a plan, or what it changes: status:open ns:network', autocomplete: 'off', spellcheck: 'false', 'data-fk': 'q', value: this.q,
      'aria-label': 'Filter the plans by words, or by column:value such as status:open, by:ana, action:delete, ns:network or type:aws_db_instance' });
    this.input = input;
    this.countEl = h('span', { class: 'count', 'aria-live': 'polite' });
    this.pillsEl = h('div', { class: 'rp-pills', role: 'group', 'aria-label': 'Active filters' });
    this.pills = new Pills(this.pillsEl, { onRemoveTerm: (raw) => this.setQuery(removeTerm(this.q, raw), true) }, pq.LANG);
    const box = h('div', { class: 'filterbox' }, icon('search'), input, this.countEl);
    let t = 0;
    input.addEventListener('input', () => { clearTimeout(t); t = setTimeout(() => this.setQuery(input.value, true, true), 100); });
    new QueryMenu(input, box, { id: 'qsug-pl', model: () => (this.s && this.env ? { plans: this.s.plans, env: this.env } : null), suggest: pq.suggest, change: (v) => { clearTimeout(t); this.setQuery(v, true, true); } });
    return h('div', { class: 'rp-tools' }, box, this.pillsEl);
  }

  // setQuery puts a text in the finder and draws the list for it. notify tells the page, which keeps it in the address; typed says that
  // the box already holds the text, so that the caret stays where it is.
  setQuery(q, notify = false, typed = false) {
    this.q = q;
    this.f = pq.compile(q);
    if (this.input && !typed && this.input.value !== q) this.input.value = q;
    if (this.s) this.render(this.s);
    if (notify && this.cb.onQuery) this.cb.onQuery(q);
  }

  // facet is a value on a plan that a click makes a term of the finder, or takes out of it again.
  facet(col, value) {
    this.setQuery(pq.holds(this.q, col, value) ? removeValues(this.q, col, [value], pq.LANG) : addTerm(this.q, col, value, false, pq.LANG), true);
  }

  // mode is the filter: the one that was chosen, else every plan while the finder asks for something, else the pending plans, else all of
  // them; a plan that is not pending and is laid over the graph (a link names it) shows the whole list around it
  mode(plans, sel) {
    if (this.filter) return this.filter;
    if (this.f) return 'all';
    const picked = sel && plans.find((p) => p.id === sel);
    return plans.some((p) => p.open) && !(picked && !picked.open) ? 'pending' : 'all';
  }

  toggle(id) {
    this.open = this.open === id ? null : id;
    this.render(this.s);
  }

  // reveal opens the detail of a plan under its row, for a link from another part of the page
  reveal(id) {
    this.open = id;
    if (this.s) this.render(this.s);
  }

  // changes is what a plan changes, in its detail: the chips that narrow the finder by the actions, the namespaces and the types of the
  // changes, and the changes themselves, which are the ones that the finder names when it asks for any.
  changes(p, hits) {
    const all = p.touches || [];
    if (!all.length) return null;
    const env = this.env;
    const list = (hits || all).slice().sort((a, b) => (a.addr < b.addr ? -1 : a.addr > b.addr ? 1 : 0));
    const whole = this.whole.has(p.id);
    const count = (f) => { const m = new Map(); for (const c of list) { const v = f(c); if (v) m.set(v, (m.get(v) || 0) + 1); } return [...m].sort((x, y) => y[1] - x[1] || (x[0] < y[0] ? -1 : 1)); };
    const chip = (col, value, n, label, mark) => {
      const held = pq.holds(this.q, col, value);
      return h('button', { class: 'rp-facet' + (held ? ' on' : ''), type: 'button', 'aria-pressed': String(held), 'data-tip': `${held ? 'Stop filtering' : 'Filter'} by ${col} ${value}`, data: { fk: `f-${p.id}-${col}-${value}` }, on: { click: () => this.facet(col, value) } },
        mark || null, h('span', null, label || value), h('em', null, String(n)));
    };
    const acts = count(touchAction), nss = count((c) => pq.infoOf(c, env).ns), types = count((c) => pq.infoOf(c, env).type);
    const chips = [
      ...(acts.length > 1 ? acts.map(([v, n]) => { const a = actionOf(v); return chip('action', v, n, v, a ? h('i', { class: 'sq ' + a.cls }) : null); }) : []),
      ...(nss.length > 1 ? nss.slice(0, 4).map(([v, n]) => chip('ns', v, n)) : []),
      ...(types.length > 1 ? types.slice(0, 4).map(([v, n]) => chip('type', v, n)) : []),
    ];
    const shown = whole ? list : list.slice(0, ROWS);
    return h('section', { class: 'rp-chgs', 'aria-label': 'What the plan changes' },
      h('div', { class: 'rp-ch-h' }, h('b', null, 'Changes'), h('span', { class: 'cnt' }, hits ? `${list.length} of ${all.length}` : String(all.length)),
        hits ? h('button', { class: 'linkbtn', type: 'button', data: { fk: 'all-' + p.id }, on: { click: () => this.setQuery('', true) } }, 'Clear the finder') : null),
      chips.length ? h('div', { class: 'rp-facets', role: 'group', 'aria-label': 'Narrow the changes' }, chips) : null,
      h('ul', { class: 'rp-chlist' }, shown.map((c) => changeRow(p, c, env, this.cb.onChange))),
      list.length > ROWS ? h('button', { class: 'linkbtn rp-ch-more', type: 'button', data: { fk: 'more-' + p.id }, on: { click: () => { if (whole) this.whole.delete(p.id); else this.whole.add(p.id); this.render(this.s); } } }, whole ? 'Show fewer' : `Show all ${list.length}`) : null,
      p.truncated ? h('p', { class: 'rp-note dim' }, `${plural(p.truncated, 'more change')} ${p.truncated === 1 ? 'is' : 'are'} not listed here. `, h('code', null, `pravo plan show ${p.id}`), ' lists every one.') : null);
  }

  // policies is what a plan does to the policies, in its detail: a change of the plan that is not a resource, with a link to the policy.
  policies(p) {
    const all = p.policies || [];
    if (!all.length) return null;
    const WORD = { new: 'new', changed: 'new version', removed: 'removal' };
    return h('section', { class: 'rp-chgs rp-pol', 'aria-label': 'What the plan changes in the policies' },
      h('div', { class: 'rp-ch-h' }, h('b', null, 'Policies'), h('span', { class: 'cnt' }, String(all.length))),
      h('ul', { class: 'rp-chlist' }, all.map((c) => h('li', { class: 'rp-pc' },
        h('i', { class: 'sq ' + (c.change === 'removed' ? 'a-delete' : c.change === 'new' ? 'a-create' : 'a-update'), 'aria-hidden': 'true' }),
        h('button', { class: 'linkbtn', type: 'button', data: { fk: `pol-${p.id}-${c.name}` }, 'data-tip': 'Show the policy', on: { click: () => this.cb.onPolicy && this.cb.onPolicy(c.name) } }, c.name),
        h('span', { class: 'dim' }, WORD[c.change] || c.change)))));
  }

  // render draws the section. s: { plans, sel, texts, reasons, model } ; a section with no plan is not there
  render(s) {
    this.s = s;
    const { plans, sel } = s;
    const f = this.f;
    const env = this.env = { reasons: s.reasons || new Map(), model: s.model || null, now: Date.now() };
    const root = this.root;
    const wasOpen = this.open && plans.some((p) => p.id === this.open) ? this.open : null;
    root.hidden = !plans.length;
    if (!plans.length) { clear(root); return; }
    const mode = this.mode(plans, sel);
    const pending = plans.filter((p) => p.open).length;
    // with a finder, a plan is shown when it passes it, and the changes that it names are what the plan lists
    const hits = new Map();
    const passes = (p) => { if (!f) return true; const r = f.run(p, env); if (!r) return false; if (r.hits) hits.set(p.id, r.hits); return true; };
    const shown = plans.filter((p) => passes(p) && (mode === 'all' || p.open || p.id === sel || p.id === wasOpen));
    const open = wasOpen && shown.some((p) => p.id === wasOpen) ? wasOpen : null;
    // what has the focus is found again after the section is drawn: a row, a filter, or a control of the detail
    const at = root.contains(document.activeElement) ? document.activeElement.dataset : null;
    const focus = at && (at.id ? ['id', at.id] : at.filter ? ['filter', at.filter] : at.fk ? ['fk', at.fk] : null);
    const caret = this.input === document.activeElement ? [this.input.selectionStart, this.input.selectionEnd] : null;
    const seg = (key, label, n) => h('button', { type: 'button', 'aria-pressed': String(mode === key), data: { filter: key }, 'data-tip': key === 'pending' ? 'Plans that can still be applied' : 'Every plan in the state, applied and expired too' }, label, h('em', null, String(n)));
    const rows = shown.map((p, i) => this.row(p, p.id === sel, p.id === open, open ? p.id === open : i === 0, (s.reasons && s.reasons.get(p.id)) || '', hits.get(p.id), approvalsIn(s.texts && s.texts.get(p.id)), signerOf(s.texts && s.texts.get(p.id), p.author)));
    const list = h('div', { class: 'rp-list', role: 'group', 'aria-label': 'Stored plans' }, rows);
    if (!shown.length) {
      list.append(f
        ? h('p', { class: 'rp-none' }, 'No plan matches the finder. ', h('button', { class: 'linkbtn', type: 'button', data: { fk: 'clear-filter' }, on: { click: () => this.setQuery('', true) } }, 'Clear the finder'))
        : h('p', { class: 'rp-none' }, 'No plan can be applied. ', h('button', { class: 'linkbtn', type: 'button', data: { showall: '1' } }, `Show all ${plans.length}`)));
    }
    const picked = open && plans.find((p) => p.id === open);
    const detailEl = picked ? h('div', { class: 'rp-detail', role: 'region', 'aria-label': 'Plan ' + picked.id }, detail(picked, { onPlan: (id) => { this.open = id; this.render(this.s); }, byId: new Map(plans.map((p) => [p.id, p])), text: s.texts && s.texts.get(picked.id), changes: this.changes(picked, hits.get(picked.id)), policies: this.policies(picked) })) : null;
    // the detail sits under its row
    if (detailEl) rows[shown.findIndex((p) => p.id === open)].after(detailEl);
    this.countEl.textContent = f ? plural(shown.length, 'match', 'matches') : plural(plans.length, 'plan');
    this.pills.render(this.q);
    clear(root);
    root.append(
      h('div', { class: 'rp-head' }, h('h2', { class: 'h' }, 'Stored plans'), h('div', { class: 'seg rp-seg', role: 'group', 'aria-label': 'Which plans to list' }, seg('pending', 'Pending', pending), seg('all', 'All', plans.length))),
      this.tools,
      list);
    // the section scrolls to the plan that is laid over the graph when that changes, and not each time it is drawn
    if (picked && this.shownOpen !== open) { const el = root.querySelector('.rp[aria-expanded="true"]'); if (el) el.scrollIntoView({ block: 'nearest' }); }
    this.shownOpen = open;
    if (focus) {
      const el = root.querySelector(`[data-${focus[0]}="${CSS.escape(focus[1])}"]`);
      if (el) { el.focus({ preventScroll: true }); if (caret && el === this.input) el.setSelectionRange(caret[0], caret[1]); }
    }
  }

  // The title of a row is what the author wrote with the plan, which says more to a person than the id; the id is
  // the title of a plan that has no note, and the line under the title of one that has. When the finder names changes, the row
  // names the first of them. The approvals come with the overlay of the plan, as the reason does, so the chip is there once it is read.
  row(p, shown, expanded, tab, reason, hits, ap, signer) {
    const overlaps = (p.overlaps || []).length;
    const appr = approvalChip(ap);
    const btn = h('button', { class: 'rp ps-' + (STATUS[p.status] || STATUS.expired).cls + (shown ? ' sel' : ''), type: 'button', 'aria-expanded': String(expanded), tabIndex: tab ? 0 : -1, data: { id: p.id }, 'aria-label': describe(p, reason) + (appr ? ', ' + appr.getAttribute('aria-label') : '') + (hits ? `. Matching changes: ${hits.slice(0, GLANCE).map((c) => c.addr).join(', ')}${hits.length > GLANCE ? ` and ${hits.length - GLANCE} more` : ''}` : '') },
      avatar(p.author),
      h('span', { class: 'rp-main' },
        h('span', { class: 'rp-l1' }, reason ? h('span', { class: 'rp-title', title: reason }, reason) : h('code', { class: 'rp-id' }, p.id), overlaps ? h('span', { class: 'rp-ovl', 'data-tip': 'Changes resources that ' + plural(overlaps, 'other plan') + ' changes' }, icon('overlap')) : null),
        h('span', { class: 'rp-l2' }, statusChip(p.status), h('span', { class: 'who', title: p.author }, p.author), signer ? h('span', { class: 'who dim', title: 'The signature of the storing verifies as ' + signer }, 'signed by ' + signer) : null, h('span', { class: 'when', title: stamp(p.created) }, relTime(p.created)), counts(planCounts(p)), appr, expiryRing(p)),
        hits ? h('span', { class: 'rp-l4', 'aria-hidden': 'true' },
          hits.slice(0, GLANCE).map((c) => { const a = actionOf(touchAction(c)); return h('span', { class: 'rp-ch' }, a ? h('i', { class: 'sq ' + a.cls }) : null, c.addr); }),
          hits.length > GLANCE ? h('span', { class: 'rp-chn' }, `+${hits.length - GLANCE} more`) : null) : null,
        reason ? h('code', { class: 'rp-l3' }, p.id) : null));
    return h('div', { class: 'rp-row' + (shown ? ' sel' : '') }, btn,
      h('button', { class: 'rp-view', type: 'button', data: { id: p.id }, 'aria-label': 'View ' + (reason || p.id) + ' on the resources' }, shown ? 'Viewing' : 'View'));
  }

  onKey(ev) {
    // a key in the finder is the finder's: Escape leaves its menu, and the arrows move in it
    if (ev.target.closest('.rp-tools')) return;
    const row = ev.target.closest('button.rp');
    if (ev.key === 'Escape' && this.open) { ev.preventDefault(); this.open = null; this.render(this.s); return; }
    if (!row) return;
    const rows = [...this.root.querySelectorAll('button.rp')];
    const i = rows.indexOf(row);
    const to = { ArrowDown: i + 1, ArrowUp: i - 1, Home: 0, End: rows.length - 1 }[ev.key];
    if (to === undefined) return;
    ev.preventDefault();
    const next = rows[Math.max(0, Math.min(rows.length - 1, to))];
    for (const r of rows) r.tabIndex = -1;
    next.tabIndex = 0;
    next.focus();
  }
}

// byLine is what the strip says of the maker of a plan: the author, and the signer that the readers file names when the
// signature verified and the name differs, since the label that a command was given is a claim and the signature is not.
function byLine(author, sg) {
  const it = sg && !sg.note ? (sg.items || []).find((x) => x.status === 'verified' && x.signer) : null;
  if (!it || it.signer === author) return author || null;
  if (!author) return it.signer;
  return [author, h('span', { class: 'dim', title: 'The signature of the storing verifies as ' + it.signer }, ' \u00b7 signed by ' + it.signer)];
}

// The strip under the bar that says which plan is laid over the resources, with the way to its detail and the way back.
// It is the one sign that the colours of the picture are a plan's, so it spans the page and does not hide in the picture.
// signedIcon is the mark after the author: a check when the signature verifies, a sign of caution when it does not, or
// when there is none, or when its key is not in the readers file. What it says in words is in its tooltip. A state
// that was not asked, and a state that is not sealed, have no signatures to mark.
const SIGNED = { verified: ['check', 'ok'], key_not_in_file: ['bang', 'chg'], invalid: ['bang', 'bad'], unsigned: ['bang', 'dim'] };
export function signedIcon(sg) {
  if (!sg || sg.note) return null;
  const it = (sg.items || [])[0];
  const status = it ? it.status : 'unsigned';
  const [glyph, cls] = SIGNED[status] || SIGNED.unsigned;
  const mark = signatureMark(status, it && it.signer, it && it.claimed);
  return h('span', { class: 'ps-sig ' + cls, role: 'img', 'aria-label': mark.textContent, 'data-tip': mark.textContent + '\n' + mark.title }, icon(glyph));
}

// approvalChip says where the approvals of the plan stand, one square chip: what the policies in force still ask for,
// in amber, or who approved it. An approval that does not verify, or whose key the readers file does not list, is
// named in the tooltip and does not make the chip. A plan nobody approved that no policy asks approvals for has none.
export function approvalChip(ap) {
  if (!ap) return null;
  const ok = verifiedApprovals(ap);
  const who = ok.map((it) => it.signer + (it.role ? ' (' + it.role + ')' : '')).join(', ');
  const skipped = (ap.items || []).length - ok.length;
  const more = skipped ? '\n' + skipped + ' more do not count: key not in the readers file, or the signature does not verify' : '';
  const open = (ap.needs || []).find((n) => !n.met);
  if (open) {
    const label = 'Awaiting ' + plural(open.min - open.have, 'approval');
    return h('span', { class: 'b ps ps-chg ps-appr', role: 'img', 'aria-label': label, 'data-tip': needWords(open) + (ok.length ? '\nApproved so far by ' + who : '') + more }, icon('hold'), label);
  }
  if (!ok.length) return null;
  const tip = 'Approved by ' + who + ((ap.needs || []).length ? '. What the policies ask for is met.' : '') + more;
  return h('span', { class: 'b ps ps-done ps-appr', role: 'img', 'aria-label': 'Approved by ' + who, 'data-tip': tip }, icon('check'), 'Approved \u00b7 ' + (ok.length === 1 ? ok[0].signer : ok.length));
}

function verifiedApprovals(ap) { return ((ap && ap.items) || []).filter((it) => it.status === 'verified'); }

// needWords says what one need of the policies asks for and how far the plan is, as the apply words its refusal.
function needWords(n) {
  const role = n.role === 'admin' ? 'an admin' : 'a writer or an admin';
  return `The policies ask for ${plural(n.min, 'approval')} by ${role} (${(n.by || []).join(', ')}), and the plan has ${n.have}.` +
    (n.self ? '' : ' The key that stored the plan does not count.') + ' An admin applies without approvals.';
}

// approvalsBox is what the detail says of the approvals of a stored plan: what the policies in force ask for and how far
// the plan is, who approved it, and, while it waits, the command that approves it. The viewer never approves.
function approvalsBox(p, ap) {
  const ok = verifiedApprovals(ap);
  const lines = (ap.needs || []).map((n) => h('li', { class: n.met ? 'ok' : 'chg' }, needWords(n)));
  if (ap.note) lines.push(h('li', { class: 'dim' }, ap.note));
  const skipped = (ap.items || []).length - ok.length;
  const who = ok.length ? 'Approved by ' + ok.map((it) => it.signer + (it.role ? ' (' + it.role + ')' : '') + (it.at ? ', ' + relTime(it.at) : '')).join('; ') + '.' : (ap.needs || []).length ? 'Nobody has approved it yet.' : '';
  const open = (ap.needs || []).some((n) => !n.met);
  const out = [h('div', { class: 'rp-why rp-appr' + (open ? '' : ' met') }, h('b', null, open ? 'Waiting for approvals' : 'Approvals'),
    lines.length ? h('ul', null, lines) : null,
    who ? h('p', null, who, skipped ? h('span', { class: 'dim' }, ` ${skipped} more ${skipped === 1 ? 'does' : 'do'} not count: key not in the readers file, or the signature does not verify.`) : null) : null)];
  if (open && p.open) out.push(approveCommand(p));
  return out;
}

// approveCommand is the command that approves the plan, in the look of the command that applies it. Each member who
// approves runs it as themselves.
function approveCommand(p) {
  const cmd = 'pravo plan approve ' + p.id;
  return h('div', { class: 'cmdline' }, h('span', { class: 'ps', 'aria-hidden': 'true' }, '$'), h('code', null, cmd),
    h('button', { class: 'cpbtn', type: 'button', 'data-copy': cmd, 'data-fk': 'copy-approve', 'aria-label': 'Copy the command that approves ' + p.id, 'data-tip': 'Copy the command' }, icon('copy')));
}

export function planStrip(el, p, cb) {
  // a strip that says the same is left as it is: drawing it again takes the focus from the button that has it
  const sig = p ? [p.id, p.status, p.author, cb.reason || '', JSON.stringify(p.summary || {}), JSON.stringify(p.policies || []), p.planned || 0, p.applied || 0, (p.touches || []).length, JSON.stringify(cb.signatures || null), JSON.stringify(cb.approvals || null), !!cb.onClear, !!cb.onList, p.open ? Math.floor(Date.now() / 60000) : ''].join('|') : '';
  if (el.dataset.sig === sig) return;
  el.dataset.sig = sig;
  clear(el);
  if (!p) { el.hidden = true; return; }
  el.hidden = false;
  const n = planCounts(p);
  let c = counts(n);
  if (p.summary && p.summary.blast_radius) c.title = 'Blast radius: ' + plural(p.summary.blast_radius, 'resource');
  // the counts of a stored plan lead to the list of what it changes: the changes of a plan that was applied are the point of looking at it
  if (cb.onList) c = h('button', { class: 'ps-chg', type: 'button', 'aria-label': `${countWords(n, p.status === 'applied')}. List what the plan changes`, 'data-tip': 'List what the plan changes', on: { click: cb.onList } }, c);
  el.append(...[
    h('span', { class: 'ps-eyebrow' }, 'Plan'),
    p.author ? avatar(p.author) : null,
    cb.reason ? h('span', { class: 'ps-title', title: cb.reason }, cb.reason) : null,
    h('code', { class: 'ps-id' }, p.id),
    p.status ? statusChip(p.status) : null,
    c,
    p.status === 'partial' && p.applied ? h('span', { class: 'ps-prog dim' }, `${p.applied} of ${p.planned} applied`) : null,
    h('span', { class: 'ps-by dim' }, byLine(p.author, cb.signatures), signedIcon(cb.signatures), (p.author ? ' \u00b7 ' : '') + relTime(p.created)),
    approvalChip(cb.approvals),
    expiryRing(p),
    h('span', { class: 'grow' }),
    cb.onClear ? h('button', { class: 'ps-x', type: 'button', 'aria-label': 'Stop showing this plan', 'data-tip': 'Stop showing this plan', on: { click: cb.onClear } }, h('span', { class: 'i i-x', 'aria-hidden': 'true' })) : null].filter(Boolean));
}

// approvalsIn is the approvals of a plan as its overlay carries them, or null before the overlay is read.
export function approvalsIn(overlay) { return overlay && overlay.plan && overlay.plan.approvals ? overlay.plan.approvals : null; }
