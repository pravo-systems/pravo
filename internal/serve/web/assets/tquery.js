// The menu of the filter box of the Traces view: what the word at the caret can become. It follows the Inventory's: the
// kinds and the other columns for an empty word, the columns that a word begins, the values of a column once its name
// and the colon are typed, and the names of the spans that a word is part of.

import { TOKEN, addTerm, removeValues } from './query.js';
import { LANG, ALIAS, COLUMNS, valueOf, labelOf } from './trace-model.js';

const HINTS = {
  kind: 'command, engine, phase, setup, resource, provider, state, seal, network, tool call',
  is: 'error keeps the spans that failed',
  provider: 'The provider a span is for: aws, random',
  type: 'The resource type: aws_s3_bucket, or aws_s3* for a run of them',
  attr: 'An attribute: attr:pravo.provider, or attr:pravo.provider=aws for one value',
};
const KIND_HINTS = {
  engine: 'A plan, an apply or a discovery',
  phase: 'A stage of a plan or an apply: evaluate, lock, record',
  setup: 'Loading the specification, opening the state, starting providers',
  resource: 'One resource in a plan or an apply',
  provider: 'A call to a provider',
  state: 'A request to the state: a read, a write, a lock',
  seal: 'Sealing a value for the state, or unsealing one: the keys, a record, the outputs, a plan',
  network: 'A request a provider made over the network: method, host, path and status, never headers or bodies',
  command: 'The command itself',
  mcp: 'A call of the tool server',
};
export const KIND_ORDER = ['engine', 'phase', 'setup', 'resource', 'provider', 'state', 'seal', 'network', 'command', 'mcp'];
const rank = (k) => { const i = KIND_ORDER.indexOf(k); return i < 0 ? 99 : i; };

// tally counts what the spans hold for a column, most first, with the kinds in the order they run in.
export function tally(model, col) {
  const n = new Map();
  for (const sp of model.spans) {
    const v = valueOf(sp, col);
    if (v) n.set(v, (n.get(v) || 0) + 1);
  }
  const out = [...n];
  if (col === 'kind') out.sort((a, b) => rank(a[0]) - rank(b[0]) || b[1] - a[1]);
  else out.sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  return out;
}

// attrTally counts the attributes that the spans carry: their names, or the values of one name.
function attrTally(model, key) {
  const n = new Map();
  for (const sp of model.spans) for (const k in sp.attrs || {}) {
    if (key === undefined) n.set(k, (n.get(k) || 0) + 1);
    else if (k === key) { const v = Array.isArray(sp.attrs[k]) ? sp.attrs[k].join(',') : String(sp.attrs[k]); n.set(v, (n.get(v) || 0) + 1); }
  }
  return [...n].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
}

const quote = (v) => (/[\s,"]/.test(v) ? '"' + v.replace(/"/g, '') + '"' : v);

// names are the labels of the spans that hold the word, the slowest first, one of each.
function names(model, word) {
  const seen = new Set(), out = [];
  const hits = model.spans.filter((sp) => labelOf(sp).toLowerCase().includes(word)).sort((a, b) => b.dur_us - a.dur_us);
  for (const sp of hits) {
    const l = labelOf(sp);
    if (seen.has(l)) continue;
    seen.add(l);
    out.push({ label: l, hint: sp.kind, insert: quote(l) });
    if (out.length >= 8) break;
  }
  return out;
}

export function suggest(model, text, caret) {
  if (!model) return null;
  let start = caret;
  for (const m of text.matchAll(TOKEN)) if (m.index < caret && caret <= m.index + m[0].length) { start = m.index; break; }
  const word = text.slice(start, caret);
  const m = /^(-?)([a-z]*)(:?)(.*)$/i.exec(word);
  if (!m) return null;
  const items = [];
  if (!word) {
    for (const [v, count] of tally(model, 'kind')) items.push({ label: 'kind:' + v, count, hint: KIND_HINTS[v], group: 'Kinds', insert: 'kind:' + v + ' ' });
    const errs = tally(model, 'is').find(([v]) => v === 'error');
    if (errs) items.push({ label: 'is:error', count: errs[1], hint: 'The spans that failed', group: 'Status', insert: 'is:error ' });
    for (const c of ['provider', 'type', 'attr']) if (c === 'attr' ? attrTally(model).length : tally(model, c).length) items.push({ label: c + ':', hint: HINTS[c], group: 'Narrow by', insert: c + ':' });
    return items.length ? { from: start, to: caret, items } : null;
  }
  const neg = m[1];
  if (!m[3]) {
    const p = m[2].toLowerCase();
    if (!p) return null;
    for (const c of COLUMNS) if (c.startsWith(p) && c !== p) items.push({ label: c + ':', hint: HINTS[c], group: 'Columns', insert: neg + c + ':' });
    // a word that begins a kind is offered as that kind
    for (const [v, count] of tally(model, 'kind')) if (v.startsWith(p) && v !== p) items.push({ label: 'kind:' + v, count, hint: KIND_HINTS[v], group: 'Kinds', insert: neg + 'kind:' + v + ' ' });
    if ('error'.startsWith(p) && p.length >= 2 && tally(model, 'is').length) items.push({ label: 'is:error', hint: 'The spans that failed', group: 'Status', insert: neg + 'is:error ' });
    if (!neg && p.length >= 2) for (const it of names(model, p)) items.push({ ...it, group: 'Spans' });
    return items.length ? { from: start, to: caret, items } : null;
  }
  const col = ALIAS[m[2].toLowerCase()] || m[2].toLowerCase();
  if (!COLUMNS.includes(col)) return null;
  const rest = m[4];
  if (col === 'attr') {
    // the name first; once the equals sign is typed, the values that the name has
    const eq = rest.indexOf('=');
    const head = word.slice(0, word.length - rest.length) + (eq < 0 ? '' : rest.slice(0, eq + 1));
    const typed = (eq < 0 ? rest : rest.slice(eq + 1)).toLowerCase();
    const list = eq < 0 ? attrTally(model) : attrTally(model, rest.slice(0, eq));
    for (const [v, count] of list.filter(([v]) => v.toLowerCase() !== typed && (!typed || v.toLowerCase().includes(typed))).slice(0, 12)) {
      items.push({ label: v, count, hint: eq < 0 ? 'attribute' : '', insert: head + (eq < 0 ? v : quote(v)) + (eq < 0 ? '=' : ' ') });
    }
    return items.length ? { from: start, to: caret, items } : null;
  }
  const cut = rest.lastIndexOf(',') + 1;
  const have = new Set(rest.slice(0, cut).split(',').map((v) => v.toLowerCase()));
  const typed = rest.slice(cut).toLowerCase();
  const head = word.slice(0, word.length - rest.length + cut);
  const vals = tally(model, col)
    .filter(([v]) => !have.has(v.toLowerCase()) && v.toLowerCase() !== typed && (!typed || v.toLowerCase().includes(typed)))
    .slice(0, 12);
  for (const [v, count] of vals) items.push({ label: v, count, hint: col === 'kind' ? KIND_HINTS[v] : '', insert: head + v });
  return items.length ? { from: start, to: caret, items } : null;
}

const hasTerm = (q, col, value) => String(q || '').toLowerCase().split(/\s+/).some((w) => w.startsWith(col + ':') && w.slice(col.length + 1).split(',').includes(value));

export const hasKind = (q, kind) => hasTerm(q, 'kind', kind);
export const hasErrors = (q) => hasTerm(q, 'is', 'error');

// toggleKind adds the kind to the filter, or takes it out when it is there; toggleErrors does the same for is:error.
export const toggleKind = (q, kind) => (hasKind(q, kind) ? removeValues(q, 'kind', [kind], LANG) : addTerm(q, 'kind', kind, false, LANG));
export const toggleErrors = (q) => (hasErrors(q) ? removeValues(q, 'is', ['error'], LANG) : addTerm(q, 'is', 'error', false, LANG));
