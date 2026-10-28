// The filter box of the Discovery section: the language of the Inventory (words, column:value terms, a comma for
// either, a minus for not, * for any run of characters) over the objects that discoveries found. A word matches the id,
// the name, the status, the adopter or the reason of an object, or a value of its identity. A term names a column:
// `status:unmanaged`, `discovery:network.vpcs`, `type:aws_s3*`, `instance:aws.west`, `by:web_old`, `field:region=eu`,
// `value:vpc-0abc`, `freshness:stale`. The first five of the columns of the card (discovery, type, provider, instance,
// freshness) say which discoveries are shown; the others say which of their objects are. The text is the one that the
// box holds and the fragment carries (dq=), and the menu, the pills and the parsing are those of the Inventory (query.js).

import { parseQuery, pieces, unquote, quoted, same, TOKEN } from './query.js';

const ALIAS = {
  state: 'status', d: 'discovery', discoveries: 'discovery', kind: 'type', resource: 'type', fresh: 'freshness', run: 'freshness',
  adopter: 'by', ack: 'by', ref: 'by', address: 'id', identity: 'field', attr: 'field', attribute: 'field', fields: 'field', values: 'value',
};
export const COLUMNS = ['status', 'discovery', 'type', 'provider', 'instance', 'freshness', 'id', 'name', 'by', 'field', 'value'];
export const LANG = { columns: COLUMNS, alias: ALIAS };

// CARD is the columns that are about a discovery; the rest are about one of its objects.
const CARD = new Set(['discovery', 'type', 'provider', 'instance', 'freshness']);
const STATUSES = ['unmanaged', 'adopting', 'acknowledged'];
const STATUS_ALIAS = { unmanaged: 'unmanaged', adopting: 'adopting', adopted: 'adopting', ack: 'acknowledged', acked: 'acknowledged', acknowledged: 'acknowledged', known: 'acknowledged' };
const FRESH_ALIAS = { current: 'current', fresh: 'current', stale: 'stale', changed: 'stale', never: 'never', 'not-run': 'never', broken: 'broken', undeclared: 'undeclared', unknown: 'unknown' };

const HINTS = {
  status: 'unmanaged (asks to be adopted), adopting, acknowledged',
  discovery: 'A discovery, by its address; a group takes the discoveries below it: network or network.vpcs',
  type: 'The list resource type: aws_s3_bucket, or aws_s3* for a run of them',
  provider: 'The provider type, as in provider:aws: every instance of it',
  instance: 'A provider instance, by its whole key: aws.$west, or aws for a provider that has one',
  freshness: 'current, stale (the scope changed), never (not run), broken, undeclared, unknown',
  id: 'The id of the object, or its whole identity; * stands for any run',
  name: 'The name that the provider gave it; * stands for any run',
  by: 'The resource that adopts it, or the $ack entry that covers it: web_old, $ack[0]',
  field: 'An attribute of the identity: path=value, as in region=eu; a path alone matches the objects that have it',
  value: 'A value of the identity, anywhere: an account, a name, a region; * stands for any run',
};
const VALUE_HINTS = {
  'status:unmanaged': 'Nothing manages it: asks to be adopted',
  'status:adopting': 'A resource names it in $import; the next plan reads it',
  'status:acknowledged': 'Known, and left as it is: an $ack entry covers it',
  'freshness:current': 'The last run listed in the scope that is written now',
  'freshness:stale': 'The scope changed since the last run',
  'freshness:never': 'Not run yet',
};
export const hintOf = (col) => HINTS[col] || '';

// providerOf is the key of the provider instance of a discovery, or its source when the page was not given one.
export const providerOf = (x) => x.instance || String(x.provider || '').split('/').pop();
const providerType = (x) => providerOf(x).split('.')[0];

// hay is what a plain word is looked for in: the id, the name, the status, the adopter or entry, the reason and the identity.
function hay(it) {
  return [it.id, it.name, it.status, it.by, it.reason, ...(it.identity || []).map((f) => f.name + '=' + f.value)].join('\n').toLowerCase();
}

function testCard(col, v, x) {
  switch (col) {
    case 'discovery': { const n = x.name.toLowerCase(); return same(n, v) || n.startsWith(v + '.'); }
    case 'type': return same(x.type, v);
    case 'provider': return same(providerType(x), v) || (v.includes('.') && same(providerOf(x), v));
    case 'instance': return same(providerOf(x), v);
    case 'freshness': return x.freshness === (FRESH_ALIAS[v] || v);
    default: return false;
  }
}

// fieldTest says whether a field term (path=value, or a path) names one attribute of an identity
function fieldTest(v, f) {
  const eq = v.indexOf('=');
  const path = (eq < 0 ? v : v.slice(0, eq)).trim();
  const want = eq < 0 ? null : v.slice(eq + 1);
  return f.name.toLowerCase() === path && (want === null || same(f.value, want));
}

function testItem(col, v, it) {
  switch (col) {
    case 'status': return it.status === (STATUS_ALIAS[v] || v);
    case 'id': return same(it.id, v) || same(it.key || '', v);
    case 'name': return same(it.name || '', v);
    case 'by': return same(it.by || '', v);
    case 'value': return same(it.id, v) || same(it.name || '', v) || (it.identity || []).some((f) => same(f.value, v));
    case 'field': return (it.identity || []).some((f) => fieldTest(v, f));
    default: return false;
  }
}

// marks says, for the text, which of the things that the page shows are named by a term that is not turned round: the
// chips of a card, a count, an attribute of an identity. The page lights them, as the inspector lights a tag of the
// Inventory, so that what the filter holds can be seen in the cards it leaves.
export function marks(text) {
  const terms = parseQuery(text, LANG).terms.filter((t) => !t.neg);
  const any = (col, f) => terms.some((t) => t.col === col && t.values.some(f));
  return {
    any: terms.length > 0,
    discovery: (x) => any('discovery', (v) => testCard('discovery', v, x)),
    type: (x) => any('type', (v) => testCard('type', v, x)),
    provider: (x) => any('provider', (v) => testCard('provider', v, x)) || any('instance', (v) => testCard('instance', v, x)),
    freshness: (x) => any('freshness', (v) => testCard('freshness', v, x)),
    status: (s) => any('status', (v) => (STATUS_ALIAS[v] || v) === s),
    field: (f) => any('field', (v) => fieldTest(v, f)) || any('value', (v) => same(f.value, v)),
    by: (it) => any('by', (v) => same(it.by || '', v)),
    name: (it) => any('name', (v) => same(it.name || '', v)) || any('id', (v) => same(it.id, v) || same(it.key || '', v)),
  };
}

// compile makes the filter of a text: card(x) says whether a discovery is shown, item(x, it) whether one of its objects is,
// and `items` that the text asks something of the objects, so that a discovery with none that match is left out. It is
// null when the text asks for nothing.
export function compile(text) {
  const { words, terms } = parseQuery(text, LANG);
  if (!words.length && !terms.length) return null;
  const cardTerms = terms.filter((t) => CARD.has(t.col));
  const itemTerms = terms.filter((t) => !CARD.has(t.col));
  const test = (t, f) => t.values.some(f) === !t.neg;
  return {
    items: words.length > 0 || itemTerms.length > 0,
    card: (x) => cardTerms.every((t) => test(t, (v) => testCard(t.col, v, x))),
    item: (x, it) => words.every((w) => hay(it).includes(w)) && itemTerms.every((t) => test(t, (v) => testItem(t.col, v, it))),
  };
}

// rows lists the objects of the page that a filter lets through, with the discovery of each.
export function rowsOf(doc, f) {
  const out = [];
  for (const x of (doc && doc.discoveries) || []) {
    if (f && !f.card(x)) continue;
    for (const it of x.items || []) if (!f || f.item(x, it)) out.push({ x, it });
  }
  return out;
}

function valuesOf(doc, col, pool) {
  const c = new Map();
  const add = (v, n = 1) => { if (v) c.set(v, (c.get(v) || 0) + n); };
  switch (col) {
    case 'status': for (const { it } of pool) add(it.status); return STATUSES.filter((s) => c.has(s)).map((s) => [s, c.get(s)]);
    case 'discovery': for (const { x } of pool) add(x.name); for (const x of doc.discoveries) if (!c.has(x.name)) c.set(x.name, 0); break;
    case 'type': for (const { x } of pool) add(x.type); for (const x of doc.discoveries) if (!c.has(x.type)) c.set(x.type, 0); break;
    case 'provider': for (const { x } of pool) add(providerType(x)); for (const x of doc.discoveries) if (!c.has(providerType(x))) c.set(providerType(x), 0); break;
    case 'instance': for (const { x } of pool) add(providerOf(x)); for (const x of doc.discoveries) if (!c.has(providerOf(x))) c.set(providerOf(x), 0); break;
    case 'freshness': for (const x of doc.discoveries) add(x.freshness, 0); return [...c.keys()].map((k) => [k, 0]);
    case 'by': for (const { it } of pool) add(it.by); break;
    case 'field': {
      // name=value for an attribute that has few values (a region, a type), and the name alone for one that has many (an id)
      const byName = new Map();
      for (const { it } of pool) for (const f of it.identity || []) {
        if (!byName.has(f.name)) byName.set(f.name, new Map());
        const m = byName.get(f.name);
        m.set(f.value, (m.get(f.value) || 0) + 1);
      }
      for (const [name, m] of byName) {
        if (m.size <= 12) for (const [v, n] of m) add(name + '=' + v, n); else add(name, [...m.values()].reduce((a, b) => a + b, 0));
      }
      break;
    }
    default: return [];
  }
  return [...c].sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1));
}

// relevant lists the columns that this page has something to say about, for the objects of the pool: a column that every card
// shares, or that no object has a value for, cannot narrow anything and is not offered.
function relevant(doc, pool) {
  const ds = doc.discoveries;
  const distinct = (f) => new Set(ds.map(f)).size > 1;
  const has = pool.length > 0;
  return COLUMNS.filter((c) => {
    switch (c) {
      case 'status': return new Set(pool.map((r) => r.it.status)).size > 1 || (has && !pool.every((r) => r.it.status === 'unmanaged'));
      case 'discovery': return ds.length > 1;
      case 'type': return distinct((x) => x.type);
      case 'provider': return distinct(providerType);
      case 'instance': return distinct(providerOf);
      case 'freshness': return distinct((x) => x.freshness);
      case 'by': return pool.some((r) => r.it.by);
      case 'field': case 'value': return pool.some((r) => (r.it.identity || []).length);
      default: return has;
    }
  });
}

// quick is what an empty word can become: the filters that this page has something for, with how many objects each leaves, and
// then what it can be narrowed by, as far as the objects in view differ in it
function quick(doc, text, start, caret) {
  const rest = compile(text);
  const pool = rowsOf(doc, rest);
  const held = new Set(parseQuery(text, LANG).terms.filter((t) => !t.neg).flatMap((t) => t.values.map((v) => t.col + ':' + v)));
  const items = [];
  const add = (col, v, n, hint) => {
    if (!held.has(col + ':' + v.toLowerCase())) items.push({ label: col + ':' + v, count: n, hint: hint || VALUE_HINTS[col + ':' + v], group: 'Filters', insert: col + ':' + quoted(col, v) + ' ' });
  };
  const statuses = valuesOf(doc, 'status', pool);
  if (statuses.length > 1) for (const [v, n] of statuses) add('status', v, n);
  // the discoveries and the types of the page, when there is more than one to tell apart
  if (doc.discoveries.length > 1) for (const [v, n] of valuesOf(doc, 'discovery', pool).filter(([, n]) => n > 0).slice(0, 5)) add('discovery', v, n);
  const types = valuesOf(doc, 'type', pool).filter(([, n]) => n > 0);
  if (types.length > 1) for (const [v, n] of types.slice(0, 4)) add('type', v, n);
  // a discovery that needs to run is worth finding
  for (const k of ['stale', 'never']) {
    const n = doc.discoveries.filter((x) => x.freshness === k).length;
    if (n > 0 && doc.discoveries.length > n) add('freshness', k, 0, VALUE_HINTS['freshness:' + k]);
  }
  // an attribute of the identity that tells a few groups apart: a region, a kind
  for (const [pair, n] of valuesOf(doc, 'field', pool).filter(([v]) => v.includes('=')).slice(0, 4)) {
    if (n < pool.length) add('field', pair, n);
  }
  for (const c of relevant(doc, pool)) items.push({ label: c + ':', hint: HINTS[c], group: 'Narrow by', insert: c + ':' });
  return { from: start, to: caret, items };
}

// suggest says what the word at the caret can become, as the Inventory's does: the columns for a word, the values of a column
// once its name and the colon are typed.
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
  const rest0 = compile(text.slice(0, start) + text.slice(caret));
  const pool = rowsOf(doc, rest0);
  const items = valuesOf(doc, col, pool)
    .filter(([v]) => !have.has(v.toLowerCase()) && v.toLowerCase() !== typed && (!typed || v.toLowerCase().includes(typed)))
    .sort((a, b) => (b[0].toLowerCase().startsWith(typed) ? 1 : 0) - (a[0].toLowerCase().startsWith(typed) ? 1 : 0))
    .slice(0, 12)
    .map(([v, count]) => ({ label: v, count, hint: VALUE_HINTS[col + ':' + v], insert: head + quoted(col, v) }));
  return items.length ? { from: start, to: caret, items } : null;
}
