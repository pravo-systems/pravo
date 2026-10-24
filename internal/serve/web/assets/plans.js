// The plans stored in the state: the list in the rail, the plan bar on the canvas, the badges that name an
// author, and the detail of one plan. A stored plan is a plan that someone made with "pravo plan --store" and
// that anyone who reads the state finds. The page only reads: nothing here applies a plan, approves it or
// removes it. What it offers is the exact command that applies a plan, to copy and run elsewhere.

import { h, s as svgEl, clear, icon, relTime, relUntil, stamp, plural, signatureMark } from './util.js';

// STATUS says where a plan stands. The word is the carrier; the colour agrees with it: green can be applied,
// blue is part way, amber moved on, grey is over. A plan that is partly stale is amber with a green edge: the
// state moved on under some of its changes, and the rest can be applied.
export const STATUS = {
  pending: { cls: 'ok', tip: 'It can be applied.' },
  partial: { cls: 'lock', tip: 'Some of its changes were applied; the rest can be.' },
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
// that has gone and the colour of the plan for the time that remains, amber under a day and red under three hours.
// The words come with it when the time is short, and the tooltip always has them.
export function expiryRing(p, now = Date.now()) {
  if (!p.open || !p.created || !p.expires) return null;
  const start = Date.parse(p.created), end = Date.parse(p.expires);
  if (!(end > start)) return null;
  const left = Math.max(0, end - now);
  const hours = left / 36e5;
  const cls = hours < 3 ? 'bad' : hours < 24 ? 'chg' : 'ok';
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

// counts are the adds, changes and destroys of a plan, in the colours of the actions
export function counts(c, cls = '') {
  const parts = [];
  if (c.add) parts.push(h('b', { class: 'ok' }, '+' + c.add));
  if (c.change) parts.push(h('b', { class: 'chg' }, '~' + c.change));
  if (c.destroy) parts.push(h('b', { class: 'bad' }, '−' + c.destroy));
  if (!parts.length) parts.push(h('b', { class: 'dim' }, c.read ? plural(c.read, 'read') : 'no changes'));
  return h('span', { class: 'pcnt' + (cls ? ' ' + cls : ''), 'aria-hidden': 'true' }, parts);
}

const countWords = (c) => [c.add && `${c.add} to add`, c.change && `${c.change} to change`, c.destroy && `${c.destroy} to destroy`].filter(Boolean).join(', ') || 'no changes';

// describe is the label of a plan for a screen reader
export function describe(p, reason) {
  const bits = [reason || p.id, reason ? p.id : null, statusLabel(p.status), `by ${p.author}`, relTime(p.created), countWords(p.summary || {})];
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
    ['expires', h('span', { title: stamp(p.expires) }, p.status === 'expired' ? 'expired ' + relTime(p.expires) : relUntil(p.expires))],
    text.reason ? ['reason', text.reason] : null,
    text.command ? ['command', h('code', { class: 'cmd' }, text.command)] : null,
    text.git_revision ? ['git', h('code', null, text.git_revision)] : null,
  ].filter(Boolean);
}

// detail is what the rail shows under the plan that is laid over the graph: the reason, what stops it or crosses
// it, and the command that applies it.
export function detail(p, { onPlan, byId, text } = {}) {
  const body = [];
  text = text || {};
  if (text.reason && text.reason.length > 60) body.push(h('p', { class: 'rp-reason', title: text.reason }, text.reason));
  if (p.applying) body.push(h('p', { class: 'rp-note chg' }, icon('hold'), h('span', null, 'An apply from this plan is running.')));
  if (p.applied && p.planned > 0) body.push(h('p', { class: 'rp-note' }, `${p.applied} of ${p.planned} changes applied.`));
  if ((p.reasons || []).length) {
    body.push(h('div', { class: 'rp-why' }, h('b', null, p.status === 'stale' ? 'Stale: it can no longer be applied.' : 'Why'), h('ul', null, p.reasons.map((r) => h('li', null, r)))));
  }
  if (p.status === 'partly-stale') body.push(conflictsBox(p));
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
  if (p.open) body.push(...planActions(p));
  else body.push(h('p', { class: 'rp-note dim' }, p.status === 'applied' ? 'Every change of this plan was applied.' : p.status === 'expired' ? 'This plan expired and will be collected.' : 'Make the plan again with pravo plan --store.'));
  return body;
}

// Plans is the list in the rail: one row for each plan, the filter that keeps the pending ones, and the detail
// of the plan that is laid over the graph. The rows are toggle buttons with the arrow keys of a list.
export class Plans {
  constructor(root, cb) {
    this.root = root;
    this.cb = cb;
    this.filter = null; // null: pending if any plan is, else all
    this.open = null; // the plan whose detail is shown under its row
    this.s = null;
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

  // mode is the filter: the one that was chosen, else the pending plans, else all of them; a plan that is not
  // pending and is laid over the graph (a link names it) shows the whole list around it
  toggle(id) {
    this.open = this.open === id ? null : id;
    this.render(this.s);
  }

  // reveal opens the detail of a plan under its row, for a link from another part of the page
  reveal(id) {
    this.open = id;
    if (this.s) this.render(this.s);
  }

  mode(plans, sel) {
    if (this.filter) return this.filter;
    const picked = sel && plans.find((p) => p.id === sel);
    return plans.some((p) => p.open) && !(picked && !picked.open) ? 'pending' : 'all';
  }

  // render draws the section. s: { plans, sel } ; a section with no plan is not there
  render(s) {
    this.s = s;
    const { plans, sel } = s;
    const open = this.open && plans.some((p) => p.id === this.open) ? this.open : null;
    const root = this.root;
    root.hidden = !plans.length;
    if (!plans.length) { clear(root); return; }
    const mode = this.mode(plans, sel);
    const pending = plans.filter((p) => p.open).length;
    const shown = plans.filter((p) => mode === 'all' || p.open || p.id === sel || p.id === open);
    // what has the focus is found again after the section is drawn: a row, a filter, or a control of the detail
    const at = root.contains(document.activeElement) ? document.activeElement.dataset : null;
    const focus = at && (at.id ? ['id', at.id] : at.filter ? ['filter', at.filter] : at.fk ? ['fk', at.fk] : null);
    const seg = (key, label, n) => h('button', { type: 'button', 'aria-pressed': String(mode === key), data: { filter: key }, 'data-tip': key === 'pending' ? 'Plans that can still be applied' : 'Every plan in the state, applied and expired too' }, label, h('em', null, String(n)));
    const rows = shown.map((p, i) => this.row(p, p.id === sel, p.id === open, open ? p.id === open : i === 0, (s.reasons && s.reasons.get(p.id)) || ""));
    const list = h('div', { class: 'rp-list', role: 'group', 'aria-label': 'Stored plans' }, rows);
    if (!shown.length) {
      list.append(h('p', { class: 'rp-none' }, 'No plan can be applied. ', h('button', { class: 'linkbtn', type: 'button', data: { showall: '1' } }, `Show all ${plans.length}`)));
    }
    const picked = open && plans.find((p) => p.id === open);
    const detailEl = picked ? h('div', { class: 'rp-detail', role: 'region', 'aria-label': 'Plan ' + picked.id }, detail(picked, { onPlan: (id) => { this.open = id; this.render(this.s); }, byId: new Map(plans.map((p) => [p.id, p])), text: s.texts && s.texts.get(picked.id) })) : null;
    // the detail sits under its row
    if (detailEl) rows[shown.findIndex((p) => p.id === open)].after(detailEl);
    clear(root);
    root.append(
      h('div', { class: 'rp-head' }, h('h2', { class: 'h' }, 'Plans'), h('div', { class: 'seg rp-seg', role: 'group', 'aria-label': 'Which plans to list' }, seg('pending', 'Pending', pending), seg('all', 'All', plans.length))),
      list);
    // the section scrolls to the plan that is laid over the graph when that changes, and not each time it is drawn
    if (picked && this.shownOpen !== open) { const el = root.querySelector('.rp[aria-expanded="true"]'); if (el) el.scrollIntoView({ block: 'nearest' }); }
    this.shownOpen = open;
    if (focus) { const el = root.querySelector(`[data-${focus[0]}="${CSS.escape(focus[1])}"]`); if (el) el.focus({ preventScroll: true }); }
  }

  // The title of a row is what the author wrote with the plan, which says more to a person than the id; the id is
  // the title of a plan that has no note, and the line under the title of one that has.
  row(p, shown, expanded, tab, reason) {
    const overlaps = (p.overlaps || []).length;
    const btn = h('button', { class: 'rp ps-' + (STATUS[p.status] || STATUS.expired).cls + (shown ? ' sel' : ''), type: 'button', 'aria-expanded': String(expanded), tabIndex: tab ? 0 : -1, data: { id: p.id }, 'aria-label': describe(p, reason) },
      avatar(p.author),
      h('span', { class: 'rp-main' },
        h('span', { class: 'rp-l1' }, reason ? h('span', { class: 'rp-title', title: reason }, reason) : h('code', { class: 'rp-id' }, p.id), overlaps ? h('span', { class: 'rp-ovl', 'data-tip': 'Changes resources that ' + plural(overlaps, 'other plan') + ' changes' }, icon('overlap')) : null),
        h('span', { class: 'rp-l2' }, statusChip(p.status), h('span', { class: 'who', title: p.author }, p.author), h('span', { class: 'when', title: stamp(p.created) }, relTime(p.created)), counts(p.summary || {}), expiryRing(p)),
        reason ? h('code', { class: 'rp-l3' }, p.id) : null));
    return h('div', { class: 'rp-row' + (shown ? ' sel' : '') }, btn,
      h('button', { class: 'rp-view', type: 'button', data: { id: p.id }, 'aria-label': 'View ' + (reason || p.id) + ' on the resources' }, shown ? 'Viewing' : 'View'));
  }

  onKey(ev) {
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

export function planStrip(el, p, cb) {
  // a strip that says the same is left as it is: drawing it again takes the focus from the button that has it
  const sig = p ? [p.id, p.status, p.author, cb.reason || '', JSON.stringify(p.summary || {}), JSON.stringify(cb.signatures || null), !!cb.onClear, p.open ? Math.floor(Date.now() / 60000) : ''].join('|') : '';
  if (el.dataset.sig === sig) return;
  el.dataset.sig = sig;
  clear(el);
  if (!p) { el.hidden = true; return; }
  el.hidden = false;
  const c = counts(p.summary || {});
  if (p.summary && p.summary.blast_radius) c.title = 'Blast radius: ' + plural(p.summary.blast_radius, 'resource');
  el.append(...[
    h('span', { class: 'ps-eyebrow' }, 'Plan'),
    p.author ? avatar(p.author) : null,
    cb.reason ? h('span', { class: 'ps-title', title: cb.reason }, cb.reason) : null,
    h('code', { class: 'ps-id' }, p.id),
    p.status ? statusChip(p.status) : null,
    c,
    h('span', { class: 'ps-by dim' }, p.author || null, signedIcon(cb.signatures), (p.author ? ' \u00b7 ' : '') + relTime(p.created)),
    expiryRing(p),
    h('span', { class: 'grow' }),
    cb.onClear ? h('button', { class: 'ps-x', type: 'button', 'aria-label': 'Stop showing this plan', 'data-tip': 'Stop showing this plan', on: { click: cb.onClear } }, h('span', { class: 'i i-x', 'aria-hidden': 'true' })) : null].filter(Boolean));
}
