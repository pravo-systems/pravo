// The filter box: plain words and column:value terms. A word matches the address, the type, the action and the mode of
// a node, and every word must match. A term names a column and what it is to hold: `type:aws_s3_bucket`, `mode:data`,
// `provider:aws`, `ns:api.services`, `status:drifted`, `plan:create`, `rev:>3`, `refreshed:>1d`. A comma ORs the values
// of one term (`mode:data,ephemeral`), and a minus before the column turns it round (`-mode:managed`). Terms are ANDed
// with each other and with the words. A word with a colon that names no column is a word, and a term without a value
// (what is being typed) is left out. A type, a provider or an address is matched whole, and * stands for any run of
// characters: `type:aws_s3*`. The provider aws is every instance of aws, and aws.west is that one.

import { isOut } from './model.js';
import { h, clear } from './util.js';

const ALIAS = { address: 'addr', name: 'addr', namespace: 'ns', module: 'ns', action: 'plan', changes: 'plan', age: 'refreshed' };
export const COLUMNS = ['type', 'provider', 'mode', 'status', 'plan', 'ns', 'addr', 'rev', 'refreshed'];

const HINTS = {
  type: 'The resource type: aws_s3_bucket, or aws_s3* for a run of them',
  provider: 'A provider and all its instances, or one instance',
  mode: 'managed, data, ephemeral, output',
  status: 'drifted, gone, orphan, declared, locked, sensitive',
  plan: 'What the plan does: create, update, replace, delete',
  ns: 'A namespace and everything below it',
  addr: 'The whole address; * stands for any run',
  rev: 'The revision: 3, >3, <=2',
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

const UNIT = { m: 60e3, h: 3600e3, d: 86400e3, w: 7 * 86400e3 };

// parseQuery splits the text into words and terms
export function parseQuery(text) {
  const words = [], terms = [];
  for (const raw of String(text || '').trim().split(/\s+/)) {
    if (!raw) continue;
    const m = /^(-?)([a-z]+):(.*)$/i.exec(raw);
    const col = m && (ALIAS[m[2].toLowerCase()] || m[2].toLowerCase());
    if (m && COLUMNS.includes(col)) {
      const values = m[3].split(',').map((v) => v.trim().toLowerCase()).filter(Boolean);
      if (values.length) terms.push({ col, neg: m[1] === '-', values, raw });
      continue;
    }
    words.push(raw.toLowerCase());
  }
  return { words, terms };
}

// removeTerm takes one term, as parseQuery wrote it down, out of the text
export function removeTerm(text, raw) {
  const words = String(text || '').trim().split(/\s+/).filter(Boolean);
  const i = words.indexOf(raw);
  if (i >= 0) words.splice(i, 1);
  return words.join(' ');
}

// addTerm makes the text hold col:value, or -col:value to leave it out. The value joins a term of the same column and sign
// that is there, and leaves the term of the other sign.
export function addTerm(text, col, value, neg) {
  const words = String(text || '').trim().split(/\s+/).filter(Boolean);
  const v = value.toLowerCase();
  let joined = false;
  const out = [];
  for (const w of words) {
    const m = /^(-?)([a-z]+):(.*)$/i.exec(w);
    const c = m && (ALIAS[m[2].toLowerCase()] || m[2].toLowerCase());
    if (!m || c !== col) { out.push(w); continue; }
    const vals = m[3].split(',').filter(Boolean);
    if ((m[1] === '-') === neg) {
      joined = true;
      if (!vals.some((x) => x.toLowerCase() === v)) vals.push(value);
      out.push(m[1] + m[2] + ':' + vals.join(','));
    } else {
      const rest = vals.filter((x) => x.toLowerCase() !== v);
      if (rest.length) out.push(m[1] + m[2] + ':' + rest.join(','));
    }
  }
  if (!joined) out.push((neg ? '-' : '') + col + ':' + value);
  return out.join(' ');
}

function compare(op, a, b) {
  switch (op) {
    case '>': return a > b;
    case '>=': return a >= b;
    case '<': return a < b;
    case '<=': return a <= b;
    default: return a === b;
  }
}

// same tells whether a value is the one that v names, whole; a * in v stands for any run of characters
function same(value, v) {
  value = value.toLowerCase();
  if (!v.includes('*')) return value === v;
  return new RegExp('^' + v.split('*').map((p) => p.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('.*') + '$').test(value);
}

function test(col, v, n, now) {
  switch (col) {
    case 'type': return same(n.type, v);
    // aws is every instance of the provider, aws.west is that one
    case 'provider': return !!n.provider && (same(n.provider, v) || same(n.provider.split('.')[0], v));
    case 'mode': return n.mode === v;
    case 'addr': return same(n.addr, v);
    case 'ns': {
      const p = (n.namespace || '').toLowerCase();
      return p === v || p.startsWith(v + '.');
    }
    case 'plan': return v === 'none' ? !n.action || n.action === 'no-op' : n.action === v;
    case 'status': { const f = STATUS[STATUS_ALIAS[v] || v]; return !!f && f(n); }
    case 'rev': {
      const m = /^(>=|<=|>|<|=)?(\d+)$/.exec(v);
      return !!m && !isOut(n) && compare(m[1] || '=', n.revision || 0, +m[2]);
    }
    case 'refreshed': {
      const m = /^(>=|<=|>|<)?(\d+)([mhdw])$/.exec(v);
      const t = Date.parse(n.refreshed_at);
      return !!m && !!t && compare(m[1] || '<', now - t, +m[2] * UNIT[m[3]]);
    }
    default: return false;
  }
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
    case 'provider':
      for (const n of pool) {
        add(n.provider);
        // the provider itself, when it has instances: aws beside aws.west
        const base = n.provider && n.provider.split('.')[0];
        if (base && base !== n.provider) add(base);
      }
      break;
    case 'mode': for (const n of pool) add(n.mode); break;
    case 'plan': for (const n of pool) if (n.action && n.action !== 'no-op') add(n.action); break;
    case 'ns':
      for (const n of pool) {
        const parts = (n.namespace || '').split('.');
        for (let i = 1; i <= parts.length && parts[0]; i++) add(parts.slice(0, i).join('.'));
      }
      break;
    case 'status': for (const k of Object.keys(STATUS)) { let x = 0; for (const n of pool) if (STATUS[k](n)) x++; if (x) c.set(k, x); } break;
    case 'rev': return ['>1', '>3', '1'].map((v) => [v, 0]);
    case 'refreshed': return ['<1h', '>1d', '>7d'].map((v) => [v, 0]);
    default: return [];
  }
  return [...c].sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1));
}

// suggest says what the word at the caret can become: a list of { label, hint, count, insert } and the span of the text that
// the choice replaces. The columns are offered for a word, and the values of a column once its name and the colon are typed.
export function suggest(model, text, caret, scope) {
  const start = text.lastIndexOf(' ', caret - 1) + 1;
  const word = text.slice(start, caret);
  const m = /^(-?)([a-z]*)(:?)(.*)$/i.exec(word);
  if (!m || !word) return null;
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
  // the counts are of what the rest of the filter leaves: the chip that is on and every term but the word being typed
  const rest0 = compileQuery(text.slice(0, start) + text.slice(caret));
  const pool = model.list.filter((n) => (!scope || scope(n)) && (!rest0 || rest0(n)));
  const items = valuesOf(model, col, pool)
    .filter(([v]) => !have.has(v.toLowerCase()) && v.toLowerCase() !== typed && (!typed || v.toLowerCase().includes(typed)))
    .sort((a, b) => (b[0].toLowerCase().startsWith(typed) ? 1 : 0) - (a[0].toLowerCase().startsWith(typed) ? 1 : 0))
    .slice(0, 12)
    .map(([v, count]) => ({ label: v, count, insert: head + v }));
  return items.length ? { from: start, to: caret, items } : null;
}

// The menu under the filter box: what suggest() offers for the word at the caret. Arrows move, Tab or Enter take the
// choice, Escape closes it; it does not take a key that it has no use for.
export class QueryMenu {
  constructor(input, host, cb) {
    this.input = input;
    this.cb = cb; // { model(), scope(), change(text) }
    this.list = h('ul', { class: 'qsug', id: 'qsug', role: 'listbox', 'aria-label': 'Suggestions for the filter', hidden: true });
    host.append(this.list);
    this.items = [];
    this.at = -1;
    this.span = null;
    input.setAttribute('aria-autocomplete', 'list');
    input.setAttribute('aria-controls', 'qsug');
    input.setAttribute('aria-expanded', 'false');
    input.addEventListener('input', () => this.refresh());
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
    const r = suggest(this.cb.model(), this.input.value, this.input.selectionStart ?? this.input.value.length, this.cb.scope && this.cb.scope());
    if (!r) { this.close(); return; }
    this.span = r;
    this.items = r.items;
    this.at = -1;
    clear(this.list);
    r.items.forEach((it, i) => {
      this.list.append(h('li', { id: 'qsug-' + i, role: 'option', 'aria-selected': 'false', data: { i: String(i) } },
        h('span', { class: 'v' }, it.label),
        it.count != null && it.count > 0 ? h('em', null, String(it.count)) : null,
        it.hint ? h('span', { class: 'hint' }, it.hint) : null));
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
    [...this.list.children].forEach((li, i) => li.setAttribute('aria-selected', String(i === this.at)));
    this.input.setAttribute('aria-activedescendant', 'qsug-' + this.at);
    this.list.children[this.at].scrollIntoView({ block: 'nearest' });
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
    // a column asks for its value next: the menu stays open on the values
    if (it.insert.endsWith(':')) this.refresh(); else this.close();
  }

  onKey(ev) {
    if (ev.key === 'ArrowDown' && !this.isOpen) { this.refresh(); if (this.isOpen) ev.preventDefault(); return; }
    if (!this.isOpen) return;
    if (ev.key === 'ArrowDown') { ev.preventDefault(); this.move(1); } else if (ev.key === 'ArrowUp') { ev.preventDefault(); this.move(-1); } else if (ev.key === 'Escape') { ev.preventDefault(); this.close(); } else if (ev.key === 'Tab' || (ev.key === 'Enter' && this.at >= 0)) { ev.preventDefault(); this.take(this.at >= 0 ? this.at : 0); }
  }
}
