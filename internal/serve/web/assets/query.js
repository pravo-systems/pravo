// The filter box: plain words and column:value terms. A word matches the address, the type, the action and the mode of
// a node, and every word must match. A term names a column and what it is to hold: `type:aws_s3_bucket`, `mode:data`,
// `entity:data`, `provider:aws`, `ns:api.services`, `status:drifted`, `plan:create`, `refreshed:>1d`. A comma ORs the values
// of one term (`mode:data,ephemeral`), and a minus before the column turns it round (`-mode:managed`). Terms are ANDed
// with each other and with the words. A word with a colon that names no column is a word, and a term without a value
// (what is being typed) is left out. A type, a provider or an address is matched whole, and * stands for any run of
// characters: `type:aws_s3*`. A field term looks into the values of the records, which the server searches: `field:settings.region=eu`, and `value:vpc-0abc` looks for a value in any field. provider:aws is the provider type, whatever its instance, and instance:aws.west is that instance of it.

import { isOut, isEnv, isSecret } from './model.js';
import { h, clear } from './util.js';

const ALIAS = { address: 'addr', name: 'addr', namespace: 'ns', group: 'ns', module: 'ns', action: 'plan', changes: 'plan', age: 'refreshed', fields: 'field', attr: 'field', attribute: 'field', values: 'value' };
export const COLUMNS = ['entity', 'type', 'provider', 'instance', 'mode', 'status', 'plan', 'ns', 'addr', 'field', 'value', 'refreshed'];

// A language is the columns that a filter box knows and the other names for them. The Inventory has one; the Discovery section has its own
// (see dquery.js), and the box, its pills and its menu are the same.
export const INVENTORY = { columns: COLUMNS, alias: ALIAS };

// The key of a provider instance is the provider type, and after a dot the name of the instance when the type has several:
// "aws" and "aws.$west". providerType is the type of a key; the instance is named by the whole key.
export const providerType = (key) => (key || '').split('.')[0];

// entityOf says what kind of thing a node is: a resource, a data source, an ephemeral resource, an output or an
// environment variable. The mode of the node says less: an output and a variable have one of their own, or none.
export function entityOf(n) {
  if (isOut(n)) return 'output';
  if (isEnv(n)) return 'env';
  if (isSecret(n)) return 'secret';
  return n.mode === 'managed' ? 'resource' : n.mode;
}
// ENTITY_LABEL is what the viewer calls an entity where it names a node: the kicker of the inspector
export const ENTITY_LABEL = { resource: 'Resource', data: 'Data source', ephemeral: 'Ephemeral resource', output: 'Output', env: 'Environment variable', secret: 'Secret', call: 'Call', builtin: 'Built-in value' };
const ENTITY_ALIAS = { resources: 'resource', managed: 'resource', datasource: 'data', 'data-source': 'data', datasources: 'data', environment: 'env', secrets: 'secret', outputs: 'output', ephemerals: 'ephemeral' };
export const ENTITIES = ['resource', 'data', 'ephemeral', 'output', 'env', 'secret'];

const HINTS = {
  entity: 'resource, data (a data source), ephemeral, output, env (an environment variable), secret',
  type: 'The resource type: aws_s3_bucket, or aws_s3* for a run of them',
  provider: 'The provider type, as in provider:aws: every instance of it',
  instance: 'A provider instance, by its whole key: aws.$west, or aws for a provider that has one',
  mode: 'The mode of the block: managed, data, ephemeral, output',
  status: 'drifted, gone, orphan, declared, locked, sensitive',
  plan: 'What the plan does: any, create, update, replace, delete',
  ns: 'A group and everything below it',
  addr: 'The whole address; * stands for any run',
  value: 'A value anywhere in the record: an id, an arn, a url; * stands for any run, as in value:*inventory*',
  field: 'A value in the record: path=value, as in settings.region=eu or tags.env=prod; a path alone matches the nodes that hold it',
  refreshed: 'How long since the refresh: >1d is older than a day',
};
export const hintOf = (col) => HINTS[col] || '';

const STATUS = {
  drifted: (n) => !isOut(n) && !!(n.drifted || n.gone),
  gone: (n) => !!n.gone,
  orphan: (n) => !isOut(n) && n.presence === 'state',
  declared: (n) => !isOut(n) && n.presence === 'spec',
  locked: (n) => !!n.locked,
  sensitive: (n) => n.sensitive > 0,
};
const STATUS_ALIAS = { drift: 'drifted', orphans: 'orphan', new: 'declared', create: 'declared', spec: 'declared', lock: 'locked' };

// LEGACY_FILTERS is the term that an old link means by filter=: the viewer once had a chip for each, and now writes the term.
export const LEGACY_FILTERS = {
  resources: { col: 'entity', value: 'resource' },
  data: { col: 'entity', value: 'data' },
  ephemeral: { col: 'entity', value: 'ephemeral' },
  plan: { col: 'plan', value: 'any' },
  drifted: { col: 'status', value: 'drifted' },
  sensitive: { col: 'status', value: 'sensitive' },
  orphans: { col: 'status', value: 'orphan' },
  declared: { col: 'status', value: 'declared' },
  outputs: { col: 'entity', value: 'output' },
  env: { col: 'entity', value: 'env' },
  secret: { col: 'entity', value: 'secret' },
};

// fieldHits holds what the server answered for each field spec (path=value, as written): the addresses of the nodes. A spec
// that has not been answered yet matches nothing. The page fills it and clears it when the state changes.
export const fieldHits = new Map();

// fieldSpec is what the server is asked for a term: path=value for a field, any path for a value
const fieldSpec = (col, v) => (col === 'value' ? '*=' + v : v);

// fieldSpecs lists the specs that a text asks of the server
export function fieldSpecs(text) {
  const out = new Set();
  for (const t of parseQuery(text).terms) if (t.col === 'field' || t.col === 'value') for (const v of t.values) out.add(fieldSpec(t.col, v));
  return [...out];
}

// fieldMatcher returns a function (path, value) that tells whether a value of a record is one that a field or value term of
// the text asks for, by the rules of the server, or null when the text has no such term. The State tab uses it to show which
// rows are why the node is in the result. A term that turns the result round does not show anything.
export function fieldMatcher(text) {
  const specs = [];
  for (const t of parseQuery(text).terms) if (!t.neg && (t.col === 'field' || t.col === 'value')) for (const v of t.values) specs.push(fieldSpec(t.col, v));
  const list = specs.map((sp) => {
    const eq = sp.indexOf('=');
    const path = (eq < 0 ? sp : sp.slice(0, eq)).trim();
    return { path, value: eq < 0 ? null : sp.slice(eq + 1), bare: !/[.*]/.test(path) };
  }).filter((s) => s.path);
  if (!list.length) return null;
  // the path is the one named or lies below it
  const pathOk = (s, p) => {
    for (;;) {
      if (s.bare ? p === s.path || p.endsWith('.' + s.path) : same(p, s.path)) return true;
      const i = p.lastIndexOf('.');
      if (i < 0) return false;
      p = p.slice(0, i);
    }
  };
  return (path, value) => list.some((s) => pathOk(s, String(path).toLowerCase()) && (s.value === null || same(String(value), s.value)));
}

export const UNIT = { m: 60e3, h: 3600e3, d: 86400e3, w: 7 * 86400e3 };

// A double quote keeps what is inside it together: `field:tags.team="platform core"` is one term, and the comma of
// `field:tags.note="a,b"` is not a separator. The quotes themselves are not part of a value. A quote that is not closed
// holds to the end of the text, which is what the box has while the closing quote is being typed.
export const TOKEN = /(?:[^\s"]|"[^"]*"?)+/g;

// wordsOf splits the text into the words that the spaces outside of quotes separate
export const wordsOf = (text) => String(text || '').match(TOKEN) || [];

// pieces splits a list of values at the commas outside of quotes, keeping the quotes on each piece
export function pieces(s) {
  const out = [];
  let cur = '', q = false;
  for (const ch of s) {
    if (ch === '"') q = !q;
    if (ch === ',' && !q) { out.push(cur); cur = ''; } else cur += ch;
  }
  out.push(cur);
  return out.filter(Boolean);
}

// unquote takes the quotes off a value
export const unquote = (s) => s.replace(/"/g, '');

// quoted writes a value so that it is read back whole: quoted when it holds a space or a comma. For a field spec only
// what follows the = is quoted, as in field:tags.team="platform core".
export function quoted(col, value) {
  if (!/[\s,]/.test(value)) return value;
  const eq = col === 'field' ? value.indexOf('=') : -1;
  return eq < 0 ? '"' + value + '"' : value.slice(0, eq + 1) + '"' + value.slice(eq + 1) + '"';
}

// parseQuery splits the text into words and terms
export function parseQuery(text, lang = INVENTORY) {
  const words = [], terms = [];
  for (const raw of wordsOf(text)) {
    const m = /^(-?)([a-z]+):(.*)$/i.exec(raw);
    const col = m && (lang.alias[m[2].toLowerCase()] || m[2].toLowerCase());
    if (m && lang.columns.includes(col)) {
      const values = pieces(m[3]).map((v) => unquote(v).trim().toLowerCase()).filter(Boolean);
      if (values.length) terms.push({ col, neg: m[1] === '-', values, raw });
      continue;
    }
    const word = unquote(raw).trim().toLowerCase();
    if (word) words.push(word);
  }
  return { words, terms };
}

// removeTerm takes one term, as parseQuery wrote it down, out of the text
export function removeTerm(text, raw) {
  const words = wordsOf(text);
  const i = words.indexOf(raw);
  if (i >= 0) words.splice(i, 1);
  return words.join(' ');
}

// addTerm makes the text hold col:value, or -col:value to leave it out. The value joins a term of the same column and sign
// that is there, and leaves the term of the other sign.
export function addTerm(text, col, value, neg, lang = INVENTORY) {
  const words = wordsOf(text);
  const v = value.toLowerCase();
  let joined = false;
  const out = [];
  for (const w of words) {
    const m = /^(-?)([a-z]+):(.*)$/i.exec(w);
    const c = m && (lang.alias[m[2].toLowerCase()] || m[2].toLowerCase());
    if (!m || c !== col) { out.push(w); continue; }
    const vals = pieces(m[3]);
    if ((m[1] === '-') === neg) {
      joined = true;
      if (!vals.some((x) => unquote(x).toLowerCase() === v)) vals.push(quoted(col, value));
      out.push(m[1] + m[2] + ':' + vals.join(','));
    } else {
      const rest = vals.filter((x) => unquote(x).toLowerCase() !== v);
      if (rest.length) out.push(m[1] + m[2] + ':' + rest.join(','));
    }
  }
  if (!joined) out.push((neg ? '-' : '') + col + ':' + quoted(col, value));
  return out.join(' ');
}

// removeValues takes values of a column out of the terms of the text that include it; a term left with none goes
export function removeValues(text, col, values, lang = INVENTORY, neg = false) {
  const drop = new Set(values.map((v) => v.toLowerCase()));
  const out = [];
  for (const w of wordsOf(text)) {
    const m = /^(-?)([a-z]+):(.*)$/i.exec(w);
    const c = m && (lang.alias[m[2].toLowerCase()] || m[2].toLowerCase());
    if (!m || c !== col || (m[1] === '-') !== neg) { out.push(w); continue; }
    const rest = pieces(m[3]).filter((x) => !drop.has(unquote(x).toLowerCase()));
    if (rest.length) out.push(m[1] + m[2] + ':' + rest.join(','));
  }
  return out.join(' ');
}

export function compare(op, a, b) {
  switch (op) {
    case '>': return a > b;
    case '>=': return a >= b;
    case '<': return a < b;
    case '<=': return a <= b;
    default: return a === b;
  }
}

// same tells whether a value is the one that v names, whole; a * in v stands for any run of characters
export function same(value, v) {
  value = value.toLowerCase();
  if (!v.includes('*')) return value === v;
  return new RegExp('^' + v.split('*').map((p) => p.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('.*') + '$').test(value);
}

function test(col, v, n, now) {
  switch (col) {
    case 'type': return same(n.type, v);
    // aws is the provider type, whatever instance; a value with a dot is the whole key of an instance, as an old link wrote it
    case 'provider': return !!n.provider && (same(providerType(n.provider), v) || (v.includes('.') && same(n.provider, v)));
    // the instance is its whole key: aws.$west, or aws for a type that has one instance
    case 'instance': return !!n.provider && same(n.provider, v);
    case 'entity': return entityOf(n) === (ENTITY_ALIAS[v] || v);
    case 'mode': return n.mode === v;
    case 'addr': return same(n.addr, v);
    case 'field': case 'value': { const hits = fieldHits.get(fieldSpec(col, v)); return !!hits && hits.has(n.addr); }
    case 'ns': {
      const p = (n.namespace || '').toLowerCase();
      return p === v || p.startsWith(v + '.');
    }
    case 'plan': return v === 'none' ? !n.action || n.action === 'no-op' : v === 'any' ? !isOut(n) && !!n.action && n.action !== 'no-op' : n.action === v;
    case 'status': { const f = STATUS[STATUS_ALIAS[v] || v]; return !!f && f(n); }
    case 'refreshed': {
      const m = /^(>=|<=|>|<)?(\d+)([mhdw])$/.exec(v);
      const t = Date.parse(n.refreshed_at);
      return !!m && !!t && compare(m[1] || '<', now - t, +m[2] * UNIT[m[3]]);
    }
    default: return false;
  }
}

// facetHit tells whether a term of the text, not turned round, names this value of the column: what a tag of the value in
// the inspector is lit for. aws.west is named by provider:aws.west, not by provider:aws, which lights the tag aws alone.
export function facetHit(text, col, value) {
  const v = String(value).toLowerCase();
  return parseQuery(text).terms.some((t) => t.col === col && !t.neg && t.values.some((x) => same(v, x)));
}

// compileQuery returns a function that tells whether a node matches, or null when the text asks for nothing
export function compileQuery(text) {
  const { words, terms } = parseQuery(text);
  if (!words.length && !terms.length) return null;
  const now = Date.now();
  return (n) => {
    for (const w of words) {
      if (!(n.addr.toLowerCase().includes(w) || n.type.toLowerCase().includes(w) || (n.action || '').includes(w) || n.mode.includes(w))) return false;
    }
    for (const t of terms) {
      const hit = t.values.some((v) => test(t.col, v, n, now));
      if (hit === t.neg) return false;
    }
    return true;
  };
}

// valuesOf lists what a column holds in the nodes of the pool, with how many nodes hold it, the most common first
function valuesOf(model, col, pool) {
  const c = new Map();
  const add = (v) => { if (v) c.set(v, (c.get(v) || 0) + 1); };
  switch (col) {
    case 'type': for (const n of pool) if (!isOut(n)) add(n.type); break;
    case 'provider': for (const n of pool) add(providerType(n.provider)); break;
    case 'instance': for (const n of pool) add(n.provider); break;
    case 'entity':
      for (const n of pool) add(entityOf(n));
      return [...ENTITIES, ...[...c.keys()].filter((k) => !ENTITIES.includes(k))].filter((k) => c.has(k)).map((k) => [k, c.get(k)]);
    case 'mode': for (const n of pool) add(n.mode); break;
    case 'plan':
      for (const n of pool) if (!isOut(n) && n.action && n.action !== 'no-op') { add(n.action); add('any'); }
      break;
    case 'ns':
      for (const n of pool) {
        const parts = (n.namespace || '').split('.');
        for (let i = 1; i <= parts.length && parts[0]; i++) add(parts.slice(0, i).join('.'));
      }
      break;
    case 'status': for (const k of Object.keys(STATUS)) { let x = 0; for (const n of pool) if (STATUS[k](n)) x++; if (x) c.set(k, x); } break;
    case 'refreshed': return ['<1h', '>1d', '>7d'].map((v) => [v, 0]);
    default: return [];
  }
  return [...c].sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1));
}

const VALUE_HINTS = {
  'entity:resource': 'Resources',
  'entity:data': 'Data sources: values read, not created',
  'entity:ephemeral': 'Ephemeral resources: opened for a run, never recorded',
  'entity:output': 'The values the root publishes',
  'entity:env': 'Environment variables that the specification reads',
  'plan:any': 'What the plan laid over the graph changes',
  'status:drifted': 'Differs from the real object, or is gone',
  'status:gone': 'The real object is absent',
  'status:sensitive': 'Holds withheld values',
  'status:orphan': 'In the state, no longer declared',
  'status:declared': 'In the specification, not created yet',
  'status:locked': 'Held by a lock',
};
const QUICK_COLUMNS = ['entity', 'type', 'provider', 'instance', 'status', 'plan', 'ns', 'field', 'value', 'refreshed'];

// quick is what a word with nothing in it can become: the filters that this estate has something for, with how many nodes
// each leaves, then the columns
function quick(model, text, start, caret) {
  const rest = compileQuery(text);
  const pool = rest ? model.list.filter(rest) : model.list;
  const held = new Set(parseQuery(text).terms.filter((t) => !t.neg).flatMap((t) => t.values.map((v) => t.col + ':' + v)));
  const items = [];
  const add = (col, v, count) => {
    if (count > 0 && !held.has(col + ':' + v)) items.push({ label: col + ':' + v, count, hint: VALUE_HINTS[col + ':' + v], group: 'Filters', insert: col + ':' + v + ' ' });
  };
  for (const [v, count] of valuesOf(model, 'entity', pool)) add('entity', v, count);
  if (model.hasPlan) add('plan', 'any', valuesOf(model, 'plan', pool).find(([v]) => v === 'any')?.[1] || 0);
  for (const [v, count] of valuesOf(model, 'status', pool)) add('status', v, count);
  for (const c of QUICK_COLUMNS) items.push({ label: c + ':', hint: HINTS[c], group: 'Columns', insert: c + ':' });
  return { from: start, to: caret, items };
}

// suggest says what the word at the caret can become: a list of { label, hint, count, insert } and the span of the text that
// the choice replaces. The columns are offered for a word, and the values of a column once its name and the colon are typed.
export function suggest(model, text, caret) {
  // the word that the caret is in or at the end of; after a space, the empty word
  let start = caret;
  for (const m of text.matchAll(TOKEN)) if (m.index < caret && caret <= m.index + m[0].length) { start = m.index; break; }
  const word = text.slice(start, caret);
  const m = /^(-?)([a-z]*)(:?)(.*)$/i.exec(word);
  if (!m) return null;
  if (!word) return model ? quick(model, text, start, caret) : null;
  const neg = m[1];
  if (!m[3]) {
    // no colon yet: the columns that the word begins
    const p = m[2].toLowerCase();
    if (!p) return null;
    const items = COLUMNS.filter((c) => c.startsWith(p) && c !== p).map((c) => ({ label: c + ':', hint: HINTS[c], insert: neg + c + ':' }));
    return items.length ? { from: start, to: caret, items } : null;
  }
  const col = ALIAS[m[2].toLowerCase()] || m[2].toLowerCase();
  if (!COLUMNS.includes(col) || !model) return null;
  // the value being typed is the part after the last comma
  const rest = m[4];
  const cut = rest.lastIndexOf(',') + 1;
  const have = new Set(rest.slice(0, cut).split(',').map((v) => v.toLowerCase()));
  const typed = rest.slice(cut).toLowerCase();
  const head = word.slice(0, word.length - rest.length + cut);
  // the counts are of what the rest of the filter leaves: every term but the word being typed
  const rest0 = compileQuery(text.slice(0, start) + text.slice(caret));
  const pool = rest0 ? model.list.filter(rest0) : model.list;
  const items = valuesOf(model, col, pool)
    .filter(([v]) => !have.has(v.toLowerCase()) && v.toLowerCase() !== typed && (!typed || v.toLowerCase().includes(typed)))
    .sort((a, b) => (b[0].toLowerCase().startsWith(typed) ? 1 : 0) - (a[0].toLowerCase().startsWith(typed) ? 1 : 0))
    .slice(0, 12)
    .map(([v, count]) => ({ label: v, count, hint: VALUE_HINTS[col + ':' + v], insert: head + v }));
  return items.length ? { from: start, to: caret, items } : null;
}

// The menu under the filter box: what suggest() offers for the word at the caret. Arrows move, Tab or Enter take the
// choice, Escape closes it; it does not take a key that it has no use for.
export class QueryMenu {
  constructor(input, host, cb) {
    this.input = input;
    this.cb = cb; // { model(), change(text) }
    // each box has an id of its own for its menu: the Inventory, the Discovery section and the Plans section have one each
    this.id = cb.id || 'qsug';
    this.list = h('ul', { class: 'qsug', id: this.id, role: 'listbox', 'aria-label': 'Suggestions for the filter', hidden: true });
    host.append(this.list);
    this.items = [];
    this.at = -1;
    this.span = null;
    input.setAttribute('aria-autocomplete', 'list');
    input.setAttribute('aria-controls', this.id);
    input.setAttribute('aria-expanded', 'false');
    input.addEventListener('input', () => this.refresh());
    // an empty box that is entered offers what there is to filter by
    input.addEventListener('focus', () => this.refresh());
    input.addEventListener('click', () => { if (!this.isOpen) this.refresh(); });
    input.addEventListener('keydown', (ev) => this.onKey(ev));
    input.addEventListener('blur', () => setTimeout(() => this.close(), 120));
    this.list.addEventListener('mousedown', (ev) => {
      // a click must not take the focus from the input
      ev.preventDefault();
      const li = ev.target.closest('li[data-i]');
      if (li) this.take(+li.dataset.i);
    });
  }

  get isOpen() { return !this.list.hidden; }

  refresh() {
    const r = (this.cb.suggest || suggest)(this.cb.model(), this.input.value, this.input.selectionStart ?? this.input.value.length);
    if (!r) { this.close(); return; }
    this.span = r;
    this.items = r.items;
    this.at = -1;
    clear(this.list);
    let group = null;
    r.items.forEach((it, i) => {
      if (it.group && it.group !== group) this.list.append(h('li', { class: 'grp', role: 'presentation' }, it.group));
      group = it.group || group;
      this.list.append(h('li', { id: this.id + '-' + i, role: 'option', 'aria-selected': 'false', data: { i: String(i) } },
        h('span', { class: 'v' }, it.label),
        h('em', null, it.count != null && it.count > 0 ? String(it.count) : ''),
        h('span', { class: 'hint' }, it.hint || '')));
    });
    this.list.hidden = false;
    this.input.setAttribute('aria-expanded', 'true');
  }

  close() {
    this.list.hidden = true;
    this.items = [];
    this.at = -1;
    this.input.setAttribute('aria-expanded', 'false');
    this.input.removeAttribute('aria-activedescendant');
  }

  move(d) {
    const n = this.items.length;
    this.at = (this.at + d + n + (this.at < 0 && d < 0 ? 1 : 0)) % n;
    const opts = this.list.querySelectorAll('li[role="option"]');
    opts.forEach((li, i) => li.setAttribute('aria-selected', String(i === this.at)));
    this.input.setAttribute('aria-activedescendant', this.id + '-' + this.at);
    opts[this.at].scrollIntoView({ block: 'nearest' });
  }

  take(i) {
    const it = this.items[i];
    if (!it || !this.span) return;
    const v = this.input.value;
    const { from, to } = this.span;
    this.input.value = v.slice(0, from) + it.insert + v.slice(to);
    const pos = from + it.insert.length;
    this.input.setSelectionRange(pos, pos);
    this.cb.change(this.input.value);
    // a column asks for its value next, and a filter that is whole leaves the next word open: the menu stays
    if (it.insert.endsWith(':') || it.insert.endsWith(' ')) this.refresh(); else this.close();
  }

  onKey(ev) {
    if (ev.key === 'ArrowDown' && !this.isOpen) { this.refresh(); if (this.isOpen) ev.preventDefault(); return; }
    if (!this.isOpen) return;
    if (ev.key === 'ArrowDown') { ev.preventDefault(); this.move(1); } else if (ev.key === 'ArrowUp') { ev.preventDefault(); this.move(-1); } else if (ev.key === 'Escape') { ev.preventDefault(); this.close(); } else if (ev.key === 'Enter' && this.at >= 0) { ev.preventDefault(); this.take(this.at); } else if (ev.key === 'Tab' && (this.at >= 0 || !this.items[0].group)) { ev.preventDefault(); this.take(this.at >= 0 ? this.at : 0); }
  }
}
