// The filter box of the Plans section: the language of the Inventory (words, column:value terms, a comma for either, a
// minus for not, * for any run of characters) over the plans that are stored in the state and the changes that each of
// them makes. A word matches the id, the author, the status or the reason of a plan, or the address or the type of one of
// its changes. A term names a column. Those about the plan: `status:pending` (open is any plan that can be applied),
// `by:ben*`, `id:plan-7b`, `reason:rotate`, `created:<1d`, `expires:<1d`, `overlap:any`. Those about one of its changes:
// `action:delete`, `resource:api.db`, `ns:network`, `type:aws_db_instance`, `provider:aws`, `change:conflict`. A plan is
// shown when it passes every term about the plan and has a change that passes every term about a change, and it lists
// the changes that do. The text is the one that the box holds and the fragment carries (plq=); the menu, the pills and the
// parsing are those of the Inventory (query.js).

import { parseQuery, pieces, unquote, quoted, same, TOKEN, providerType, compare, UNIT } from './query.js';
import { touchAction } from './plancount.js';

const ALIAS = {
  state: 'status', author: 'by', who: 'by', owner: 'by', plan: 'id', note: 'reason', title: 'reason', age: 'created', expiry: 'expires', lapses: 'expires',
  overlaps: 'overlap', op: 'action', does: 'action', kind: 'type', address: 'resource', addr: 'resource', touches: 'resource', resources: 'resource',
  namespace: 'ns', group: 'ns', module: 'ns', changes: 'change', progress: 'change',
};
export const COLUMNS = ['status', 'by', 'id', 'reason', 'created', 'expires', 'overlap', 'action', 'resource', 'ns', 'type', 'provider', 'change'];
export const LANG = { columns: COLUMNS, alias: ALIAS };

// PLAN is the columns that are about a plan; the rest are about one of its changes.
const PLAN = new Set(['status', 'by', 'id', 'reason', 'created', 'expires', 'overlap']);
export const STATUSES = ['pending', 'partial', 'partly-stale', 'stale', 'applied', 'expired'];
const STATUS_ALIAS = { partly: 'partly-stale', 'partly_stale': 'partly-stale', partlystale: 'partly-stale', done: 'applied', lapsed: 'expired', old: 'expired', open: 'open', live: 'open' };
const ACTIONS = ['create', 'update', 'replace', 'delete', 'read', 'move', 'import'];
const ACTION_ALIAS = { add: 'create', new: 'create', change: 'update', modify: 'update', destroy: 'delete', remove: 'delete', recreate: 'replace' };
const CHANGE_STATES = ['pending', 'applied', 'conflict'];
const CHANGE_ALIAS = { left: 'pending', todo: 'pending', done: 'applied', conflicts: 'conflict', stuck: 'conflict' };

const HINTS = {
  status: 'pending, partial, partly-stale, stale, applied, expired; open is any plan that can be applied',
  by: 'The author of the plan; * stands for any run',
  id: 'The id of the plan, or its first characters: plan-7b04',
  reason: 'A word of the note that the author wrote with the plan',
  created: 'How long ago it was made: <1d is newer than a day, >7d older than a week',
  expires: 'How long it has to live: <1d lapses within a day (a plan that can be applied)',
  overlap: 'any, none, or the id of a plan: plans that change a resource that another one changes',
  action: 'What a change does: create, update, replace, delete, read, move, import',
  resource: 'The address of a resource that the plan changes; * stands for any run',
  ns: 'A group and everything below it',
  type: 'The type of a resource that the plan changes: aws_db_instance, or aws_s3* for a run of them',
  provider: 'The provider type of a resource that the plan changes, as in provider:aws',
  change: 'pending (still to apply), applied, conflict (cannot be applied now)',
};
const VALUE_HINTS = {
  'status:open': 'It can be applied, all of it or in part',
  'status:pending': 'Nothing was applied yet, and it can be',
  'status:partial': 'Some changes were applied; the rest can be',
  'status:partly-stale': 'The state moved on under some changes; the rest can be applied by name',
  'status:stale': 'The state moved on, so it cannot be applied',
  'status:applied': 'Every change was applied',
  'status:expired': 'It was not applied before it expired',
  'overlap:any': 'Changes a resource that another plan that can be applied changes',
  'expires:<1d': 'Lapses within a day',
  'created:<1d': 'Made within a day',
  'change:conflict': 'The state moved on under the resource: the change cannot be applied now',
};
export const hintOf = (col) => HINTS[col] || '';

const lower = (s) => String(s || '').toLowerCase();

// nodeOf is the node of the graph that an address names, which says its type, its provider and its namespace; a resource that the
// specification no longer declares is not there.
const nodeOf = (addr, env) => (env && env.model && env.model.nodes && env.model.nodes.get(addr)) || null;

// infoOf says what a change is made to: the namespace, the type and the provider type of the resource, as far as the graph knows it.
export function infoOf(c, env) {
  const n = nodeOf(c.addr, env);
  const i = c.addr.lastIndexOf('.');
  return {
    ns: lower(n ? n.namespace : i < 0 ? '' : c.addr.slice(0, i)),
    type: lower(n && n.type),
    provider: lower(n && n.provider ? providerType(n.provider) : ''),
    instance: lower(n && n.provider),
  };
}

const overlapsOf = (p) => p.overlaps || [];
const when = (iso) => Date.parse(iso) || 0;

function ageTest(v, ms) {
  const m = /^(>=|<=|>|<)?(\d+)([mhdw])$/.exec(v);
  return !!m && compare(m[1] || '<', ms, +m[2] * UNIT[m[3]]);
}

function testPlan(col, v, p, env) {
  switch (col) {
    case 'status': return v === 'open' ? !!p.open : p.status === (STATUS_ALIAS[v] || v);
    case 'by': return same(p.author || '', v);
    case 'id': { const id = lower(p.id), w = v.includes('*') ? v : v + '*'; return same(id, w) || same(id, 'plan-' + w); }
    case 'reason': { const r = lower(env.reasons.get(p.id)); return v.includes('*') ? same(r, v) : r.includes(v); }
    case 'created': return !!p.created && ageTest(v, env.now - when(p.created));
    case 'expires': return !!p.open && !!p.expires && ageTest(v, when(p.expires) - env.now);
    case 'overlap': return v === 'any' ? overlapsOf(p).length > 0 : v === 'none' ? overlapsOf(p).length === 0 : overlapsOf(p).some((o) => same(o.id, v) || same(o.id, 'plan-' + v));
    default: return false;
  }
}

function testChange(col, v, c, env) {
  switch (col) {
    case 'action': return touchAction(c) === (ACTION_ALIAS[v] || v);
    case 'resource': return same(c.addr, v);
    case 'ns': { const p = infoOf(c, env).ns; return p === v || p.startsWith(v + '.'); }
    case 'type': return same(infoOf(c, env).type, v);
    case 'provider': { const i = infoOf(c, env); return same(i.provider, v) || (v.includes('.') && same(i.instance, v)); }
    case 'change': { const w = CHANGE_ALIAS[v] || v; return w === 'applied' ? !!c.applied : w === 'conflict' ? !!c.conflict : w === 'pending' ? !c.applied : false; }
    default: return false;
  }
}

// planHay is what a plain word is looked for in, for the plan: the id, the author, the status and the reason.
const planHay = (p, env) => [p.id, p.author, p.status, p.status.replace(/-/g, ' '), env.reasons.get(p.id)].join('\n').toLowerCase();
const changeHay = (c, env) => [c.addr, infoOf(c, env).type, touchAction(c)].join('\n').toLowerCase();

// compile makes the filter of a text: run(p, env) is null when a plan is not shown, and else says which of its changes the text asks
// for (hits, none when the text asks nothing of the changes). The environment is { reasons, model, now }: the notes that
// the authors wrote (id to text), the graph and the time. It is null when the text asks for nothing.
export function compile(text) {
  const { words, terms } = parseQuery(text, LANG);
  if (!words.length && !terms.length) return null;
  const planTerms = terms.filter((t) => PLAN.has(t.col));
  const changeTerms = terms.filter((t) => !PLAN.has(t.col));
  const test = (t, f) => t.values.some(f) === !t.neg;
  return {
    run(p, env) {
      if (!planTerms.every((t) => test(t, (v) => testPlan(t.col, v, p, env)))) return null;
      const hay = planHay(p, env);
      // a word that the plan does not have is looked for in its changes
      const loose = words.filter((w) => !hay.includes(w));
      if (!changeTerms.length && !loose.length) return { hits: null };
      const hits = (p.touches || []).filter((c) => {
        const ch = changeHay(c, env);
        return loose.every((w) => ch.includes(w)) && changeTerms.every((t) => test(t, (v) => testChange(t.col, v, c, env)));
      });
      return hits.length ? { hits } : null;
    },
  };
}

// holds says whether a term of the text, not turned round, names this value of the column: a chip that makes the term is pressed.
export function holds(text, col, value) {
  const v = lower(value);
  return parseQuery(text, LANG).terms.some((t) => t.col === col && !t.neg && t.values.includes(v));
}

// rowsOf lists the plans that a filter lets through, each with the changes that it asked for.
export function rowsOf(doc, f) {
  const out = [];
  for (const p of (doc && doc.plans) || []) {
    const r = f ? f.run(p, doc.env) : { hits: null };
    if (r) out.push({ p, hits: r.hits });
  }
  return out;
}

const changesOf = (r) => r.hits || r.p.touches || [];

// valuesOf lists what a column holds in the plans of the pool, with how many plans hold it, the most common first
function valuesOf(doc, col, pool) {
  const c = new Map();
  const add = (v, n = 1) => { if (v) c.set(v, (c.get(v) || 0) + n); };
  const once = (r, f) => { for (const v of new Set(changesOf(r).map(f))) add(v); };
  switch (col) {
    case 'status': {
      for (const { p } of pool) add(p.status);
      const open = pool.filter(({ p }) => p.open).length;
      return [...(open ? [['open', open]] : []), ...STATUSES.filter((s) => c.has(s)).map((s) => [s, c.get(s)])];
    }
    case 'by': for (const { p } of pool) add(p.author); break;
    case 'id': for (const { p } of pool) add(p.id); break;
    case 'action': {
      for (const r of pool) once(r, touchAction);
      return ACTIONS.filter((a) => c.has(a)).map((a) => [a, c.get(a)]).concat([...c].filter(([a]) => !ACTIONS.includes(a)));
    }
    case 'resource': for (const r of pool) once(r, (x) => x.addr); break;
    case 'ns':
      for (const r of pool) {
        const seen = new Set();
        for (const x of changesOf(r)) {
          const parts = infoOf(x, doc.env).ns.split('.');
          for (let i = 1; i <= parts.length && parts[0]; i++) seen.add(parts.slice(0, i).join('.'));
        }
        for (const v of seen) add(v);
      }
      break;
    case 'type': for (const r of pool) once(r, (x) => infoOf(x, doc.env).type); break;
    case 'provider': for (const r of pool) once(r, (x) => infoOf(x, doc.env).provider); break;
    case 'change': {
      for (const r of pool) once(r, (x) => (x.applied ? 'applied' : 'pending'));
      for (const r of pool) once(r, (x) => (x.conflict ? 'conflict' : ''));
      return CHANGE_STATES.filter((s) => c.has(s)).map((s) => [s, c.get(s)]);
    }
    case 'overlap': {
      const n = pool.filter(({ p }) => overlapsOf(p).length).length;
      if (n) add('any', n);
      if (pool.length - n) add('none', pool.length - n);
      for (const { p } of pool) for (const o of overlapsOf(p)) add(o.id);
      return [...c].sort((a, b) => (a[0] === 'any' ? -1 : b[0] === 'any' ? 1 : a[0] === 'none' ? -1 : b[0] === 'none' ? 1 : b[1] - a[1]));
    }
    case 'created': return ['<1h', '<1d', '>7d'].map((v) => [v, 0]);
    case 'expires': return ['<3h', '<1d', '>3d'].map((v) => [v, 0]);
    default: return [];
  }
  return [...c].sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1));
}

// relevant lists the columns that this page has something to say about, for the plans in the pool: a column that every plan
// shares, or that none has a value for, cannot narrow anything and is not offered.
function relevant(doc, pool) {
  const plans = pool.map((r) => r.p);
  const distinct = (f) => new Set(pool.flatMap((r) => changesOf(r).map(f))).size > 1;
  return COLUMNS.filter((c) => {
    switch (c) {
      case 'status': return new Set(plans.map((p) => p.status)).size > 1;
      case 'by': return new Set(plans.map((p) => p.author)).size > 1;
      case 'id': return plans.length > 1;
      case 'reason': return plans.some((p) => doc.env.reasons.get(p.id));
      case 'created': return plans.length > 1;
      case 'expires': return plans.some((p) => p.open);
      case 'overlap': return plans.some((p) => overlapsOf(p).length);
      case 'action': return distinct(touchAction);
      case 'resource': return pool.some((r) => changesOf(r).length);
      case 'ns': return distinct((x) => infoOf(x, doc.env).ns);
      case 'type': return distinct((x) => infoOf(x, doc.env).type);
      case 'provider': return distinct((x) => infoOf(x, doc.env).provider);
      case 'change': return pool.some((r) => changesOf(r).some((x) => x.applied || x.conflict));
      default: return false;
    }
  });
}

// quick is what an empty word can become: the filters that this page has something for, with how many plans each leaves, and
// then what it can be narrowed by, as far as the plans in view differ in it
function quick(doc, text, start, caret) {
  const pool = rowsOf(doc, compile(text));
  const held = new Set(parseQuery(text, LANG).terms.filter((t) => !t.neg).flatMap((t) => t.values.map((v) => t.col + ':' + v)));
  const items = [];
  const add = (col, v, n, hint) => {
    if (!held.has(col + ':' + v.toLowerCase())) items.push({ label: col + ':' + v, count: n, hint: hint || VALUE_HINTS[col + ':' + v], group: 'Filters', insert: col + ':' + quoted(col, v) + ' ' });
  };
  const rel = relevant(doc, pool);
  if (rel.includes('status')) for (const [v, n] of valuesOf(doc, 'status', pool).slice(0, 5)) add('status', v, n);
  if (rel.includes('by')) for (const [v, n] of valuesOf(doc, 'by', pool).slice(0, 3)) add('by', v, n);
  if (rel.includes('action')) for (const [v, n] of valuesOf(doc, 'action', pool).slice(0, 4)) add('action', v, n);
  if (rel.includes('overlap')) add('overlap', 'any', valuesOf(doc, 'overlap', pool).find(([v]) => v === 'any')[1]);
  const soon = pool.filter(({ p }) => p.open && when(p.expires) - doc.env.now < UNIT.d).length;
  if (soon && soon < pool.length) add('expires', '<1d', soon);
  if (rel.includes('change') && valuesOf(doc, 'change', pool).some(([v]) => v === 'conflict')) add('change', 'conflict', valuesOf(doc, 'change', pool).find(([v]) => v === 'conflict')[1]);
  if (rel.includes('ns')) for (const [v, n] of valuesOf(doc, 'ns', pool).filter(([v]) => !v.includes('.')).slice(0, 3)) add('ns', v, n);
  for (const c of rel) items.push({ label: c + ':', hint: HINTS[c], group: 'Narrow by', insert: c + ':' });
  return { from: start, to: caret, items };
}

// suggest says what the word at the caret can become, as the Inventory's does: the columns for a word, the values of a column
// once its name and the colon are typed. doc is { plans, env }.
export function suggest(doc, text, caret) {
  let start = caret;
  for (const m of text.matchAll(TOKEN)) if (m.index < caret && caret <= m.index + m[0].length) { start = m.index; break; }
  const word = text.slice(start, caret);
  const m = /^(-?)([a-z]*)(:?)(.*)$/i.exec(word);
  if (!m) return null;
  if (!word) return doc ? quick(doc, text, start, caret) : null;
  const neg = m[1];
  if (!m[3]) {
    const p = m[2].toLowerCase();
    if (!p) return null;
    // the columns that the page can use first; one that cannot narrow anything is offered only when nothing else begins the word
    const pool = rowsOf(doc, compile(text.slice(0, start) + text.slice(caret)));
    const begins = COLUMNS.filter((c) => c.startsWith(p) && c !== p);
    const useful = begins.filter((c) => relevant(doc, pool).includes(c));
    const items = (useful.length ? useful : begins).map((c) => ({ label: c + ':', hint: HINTS[c], insert: neg + c + ':' }));
    return items.length ? { from: start, to: caret, items } : null;
  }
  const col = ALIAS[m[2].toLowerCase()] || m[2].toLowerCase();
  if (!COLUMNS.includes(col) || !doc) return null;
  const rest = m[4];
  const cut = rest.lastIndexOf(',') + 1;
  const have = new Set(pieces(rest.slice(0, cut)).map((v) => unquote(v).toLowerCase()));
  const typed = rest.slice(cut).toLowerCase();
  const head = word.slice(0, word.length - rest.length + cut);
  const pool = rowsOf(doc, compile(text.slice(0, start) + text.slice(caret)));
  const items = valuesOf(doc, col, pool)
    .filter(([v]) => !have.has(v.toLowerCase()) && v.toLowerCase() !== typed && (!typed || v.toLowerCase().includes(typed)))
    .sort((a, b) => (b[0].toLowerCase().startsWith(typed) ? 1 : 0) - (a[0].toLowerCase().startsWith(typed) ? 1 : 0))
    .slice(0, 12)
    .map(([v, count]) => ({ label: v, count, hint: VALUE_HINTS[col + ':' + v], insert: head + quoted(col, v) }));
  return items.length ? { from: start, to: caret, items } : null;
}
