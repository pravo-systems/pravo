// The State tab: the attributes of a record as three groups (configured, sensitive, computed), a filter, and
// values drawn for what they are: a hash, a JSON document, a long text, a list, an object, a withheld value,
// an unknown one, a null. Values arrive redacted from the server; nothing here asks for more, and there is no
// control that reveals a withheld value.

import { h, icon, plural, store, copyButton, relTime, stamp, softBreaks } from './util.js';
import { textBlock } from './ui.js';
import { isEnv, isSecret } from './model.js';

export const REDACTED = {
  sensitive: ['withheld', 'The provider schema marks this attribute sensitive.'],
  name: ['withheld', 'The name of this attribute looks like a secret, so the server does not send it.'],
  schema: ['withheld', 'The schema of this type is not cached, so nothing says which values are safe. Every value of it is withheld.'],
  derived: ['withheld', 'The text holds a sensitive value of this resource or of one it reads.'],
  'write-only': ['write-only', 'A write-only attribute is never recorded in the state.'],
};

// withheld draws a value that the server did not send: a quiet locked chip that says why, on hover and on focus.
export function withheld(kind) {
  const [label, why] = REDACTED[kind] || REDACTED.sensitive;
  return h('span', { class: 'wh' + (kind === 'schema' ? ' warn' : ''), tabIndex: 0, 'data-tip': `Withheld by the server\n${why}\nThis page has the name and the type, never the content.`, 'aria-label': `${label}. ${why}` },
    icon('lock'), h('span', { class: 'wh-dots', 'aria-hidden': 'true' }, '••••••••'), h('b', null, label));
}

export const unknownEl = () => h('span', { class: 'v-unk', 'data-tip': 'Not known until the plan is applied.' }, 'known after apply');
export const nullEl = () => h('span', { class: 'v-nul' }, 'null');

const HASHY = /(sha\d*|md5|hash|digest|checksum|etag|fingerprint|thumbprint)/i;
export function isHash(name, v) {
  if (typeof v !== 'string' || v.length < 16 || /\s/.test(v)) return false;
  if (/^[0-9a-f]{32,}$/i.test(v)) return true;
  return HASHY.test(name || '') && /^[A-Za-z0-9+/_=-]+$/.test(v);
}

function parseJSON(v) {
  if (typeof v !== 'string' || v.length < 2) return null;
  const t = v.trim();
  if (!((t[0] === '{' && t[t.length - 1] === '}') || (t[0] === '[' && t[t.length - 1] === ']'))) return null;
  try { const o = JSON.parse(t); return o && typeof o === 'object' ? o : null; } catch { return null; }
}

// plain: an environment variable has no provider to fill anything in, so its attributes are all the page and none is computed
const bucketOf = (a, plain) => {
  if (a.redacted || a.sensitive) return 'sensitive';
  const k = (a.kind || '').split(',');
  if (!plain && k.includes('computed') && !k.includes('optional') && !k.includes('required')) return 'computed';
  return 'configured';
};

// facetValue is the text of a value that a filter term can carry: not long, one line, no double quote (a space or a comma is quoted)
function facetValue(v) {
  if (v === undefined || v === null || typeof v === 'object') return null;
  const s = String(v);
  return s && s.length <= 80 && !/["\n\r]/.test(s) ? s : null;
}

const isList = (t) => /^(list|set|tuple)/.test(t || '');
const isMap = (t) => /^(map|object)/.test(t || '');

export class StateView {
  // ctx: { open: Map, announce(text), nodeRef(node) }
  constructor(ctx) {
    this.ctx = ctx;
    this.q = '';
    this.showComputed = store.get('pravo.computed') === '1';
  }

  render(d, changed) {
    this.d = d;
    this.changed = changed;
    const attrs = d.attrs || [];
    this.buckets = { configured: [], sensitive: [], computed: [] };
    const plain = !!d.node && (isEnv(d.node) || isSecret(d.node));
    for (const a of attrs) this.buckets[bucketOf(a, plain)].push(a);
    const wrap = h('div', { class: 'state' });
    if (!d.schema_known) wrap.append(h('p', { class: 'note warn' }, icon('bang'), h('span', null, 'The provider schema could not be read, so no one can say which values are sensitive. Every value is withheld.')));
    if (attrs.length && !d.bare) {
      const input = h('input', { class: 'sfilter', type: 'search', placeholder: 'Filter attributes and values', 'aria-label': 'Filter the attributes', autocomplete: 'off', spellcheck: 'false' });
      input.addEventListener('input', () => { this.q = input.value.trim().toLowerCase(); this.draw(); });
      const sw = h('button', { class: 'switch', type: 'button', role: 'switch', 'aria-checked': this.showComputed ? 'true' : 'false', 'data-tip': 'Computed attributes are filled in by the provider: ids, hashes, timestamps.' },
        h('span', { class: 'knob', 'aria-hidden': 'true' }), 'computed');
      sw.addEventListener('click', () => {
        this.showComputed = !this.showComputed;
        store.set('pravo.computed', this.showComputed ? '1' : '0');
        sw.setAttribute('aria-checked', this.showComputed ? 'true' : 'false');
        this.draw();
      });
      this.count = h('span', { class: 'sf-count', 'aria-hidden': 'true' });
      wrap.append(h('div', { class: 'stbar' }, h('label', { class: 'sfbox' }, icon('search'), input, this.count), plain ? null : sw));
    }
    this.host = h('div', { class: 'groups' });
    wrap.append(this.host);
    this.draw();
    this.prev = null;
    return wrap;
  }

  // ---- drawing ----

  draw() {
    const q = this.q;
    const host = this.host;
    host.textContent = '';
    // the rows that the field and value terms of the filter are about
    this.hit = this.ctx.fieldMatch ? this.ctx.fieldMatch() : null;
    const total = (this.d.attrs || []).length;
    if (!total) {
      if (this.d.node && this.d.node.sealed) {
        host.append(h('div', { class: 'blank' }, icon('lock'), h('p', null, 'The attributes of this record are sealed.'), h('small', null, 'This viewer cannot open them. The State page says who can read the state.')));
        return;
      }
      const mode = this.d.node && this.d.node.mode;
      const tab = (this.d.schema || []).length ? ' The Schema tab lists the attributes of its type.' : '';
      if (mode === 'ephemeral') {
        host.append(h('div', { class: 'blank' }, icon('stack'), h('p', null, 'An ephemeral resource is never recorded.'), h('small', null, 'It is opened for each run and kept out of the state. The specification sets no value on it.' + tab)));
        return;
      }
      if (mode === 'call' || mode === 'builtin') {
        host.append(h('div', { class: 'blank' }, icon('stack'), h('p', null, 'There is no state to show.'), h('small', null, 'It is evaluated when a command runs and never recorded.')));
        return;
      }
      host.append(h('div', { class: 'blank' }, icon('stack'), h('p', null, 'No attributes are recorded for this resource.'), h('small', null, 'It exists in the state without values, or it is declared and not created yet.' + tab)));
      return;
    }
    let shown = 0;
    const order = this.d.bare
      ? [['configured', 'Value', 'What the root publishes'], ['sensitive', 'Value', 'Withheld by the server'], ['computed', 'Value', '']]
      : this.d.node && isEnv(this.d.node)
        ? [['configured', 'Variable', 'Read from the environment'], ['sensitive', 'Sensitive', 'Withheld by the server'], ['computed', 'Value', '']]
        : this.d.node && isSecret(this.d.node)
          ? [['configured', 'Secret', 'Read from the sealed state'], ['sensitive', 'Sensitive', 'Never sent: the viewer has no key for it'], ['computed', 'Value', '']]
          : [['configured', 'Configured', 'Set by the specification'], ['sensitive', 'Sensitive', 'Withheld by the server'], ['computed', 'Computed', 'Filled in by the provider']];
    for (const [key, title, hint] of order) {
      const list = this.buckets[key];
      if (!list.length) continue;
      const hidden = key === 'computed' && !this.showComputed && !q && !(this.hit && list.some((a) => this.hits(a, a.name)));
      const rows = h('div', { class: 'attrs', role: 'list', 'aria-label': title });
      let n = 0;
      if (!hidden) {
        for (const a of list) if (this.row(rows, a, a.name, 0, null)) n++;
      }
      if (q && !n) continue;
      shown += hidden ? 0 : n;
      host.append(h('section', { class: 'grp g-' + key },
        h('header', { class: 'grp-h' }, h('h3', null, title), h('span', { class: 'gc' }, String(q ? n : list.length)), h('span', { class: 'gh' }, hint)),
        hidden
          ? h('button', { class: 'grp-hide', type: 'button', on: { click: () => this.toggleComputed() } }, icon('eye-off'), `${plural(list.length, 'computed attribute')} hidden`, h('b', null, 'Show'))
          : rows));
    }
    if (q && !shown) host.append(h('div', { class: 'blank' }, icon('search'), h('p', null, 'No attribute or value matches.')));
    if (this.count) this.count.textContent = q ? `${shown} of ${total}` : '';
  }

  toggleComputed() {
    const sw = this.host.parentNode.querySelector('.switch');
    if (sw) sw.click();
  }

  // hits reports whether an attribute, or anything under it, is a value that a field or value term asks for
  hits(a, path) {
    const f = this.hit;
    if (!f || a.redacted || a.sensitive || a.unknown || a.null) return false;
    const kids = a.children || [];
    if (kids.length) return kids.some((k) => this.hits(k, path + '.' + k.name));
    return a.value !== undefined && a.value !== null && typeof a.value !== 'object' && f(path, a.value);
  }

  // matches reports whether an attribute, or anything under it, matches the filter.
  matches(a) {
    const q = this.q;
    if (!q) return true;
    if ((a.name || '').toLowerCase().includes(q)) return true;
    if (!a.redacted && !a.sensitive && !a.unknown && !a.null && a.value !== undefined && a.value !== null) {
      let t = typeof a.value === 'string' ? a.value : '';
      if (!t) { try { t = JSON.stringify(a.value); } catch { t = ''; } }
      if (t.toLowerCase().includes(q)) return true;
    }
    return (a.children || []).some((k) => this.matches(k));
  }

  row(host, a, path, depth, parent) {
    if (!this.matches(a)) return false;
    const q = this.q;
    const kids0 = a.children || [];
    const v = this.value(a, kids0, path);
    const kids = v.leaf ? [] : kids0;
    const hitHere = this.hit ? this.hits(a, path) : false;
    const open = kids.length ? (q ? true : this.ctx.open.has(path) ? this.ctx.open.get(path) : hitHere || (depth === 0 && kids.length <= 4 && !kids.some((k) => (k.children || []).length))) : false;
    const hasKids = kids.length > 0;
    const isChanged = this.changed.has(path) || [...this.changed].some((c) => c.startsWith(path + '.'));
    const flash = this.prev && !hasKids && this.prev.get(path) !== JSON.stringify([a.redacted || '', !!a.sensitive, !!a.unknown, !!a.null, a.value === undefined ? null : a.value]);
    const locked = !!(a.redacted || a.sensitive);
    const idx = parent && (isList(parent.type) || (parent.type === 'output' && (parent.children || []).every((k) => /^\d+$/.test(k.name)))) && /^\d+$/.test(a.name);
    const kind = (a.kind || '').split(',').filter(Boolean);
    const tags = [];
    if (kind.includes('required')) tags.push(h('em', { class: 'tg req' }, 'required'));
    if (kind.includes('computed') && (kind.includes('optional') || kind.includes('required'))) tags.push(h('em', { class: 'tg cmp' }, 'computed'));
    if (a.deprecated) tags.push(h('em', { class: 'tg dep' }, 'deprecated'));
    const nameEl = h('span', { class: 'an' + (idx ? ' idx' : '') }, idx ? `[${a.name}]` : softBreaks(a.name));
    const row = h('div', { class: 'attr' + (isChanged ? ' changed' : '') + (hasKids ? ' group' + (open ? ' open-kids' : '') : '') + (locked ? ' locked' : '') + (hitHere ? (hasKids ? ' hit-in' : ' hit') : '') + (v.block ? ' blk' : '') + (depth ? ' nested' : '') + (flash ? ' flash' : ''), role: 'listitem', style: `--d:${depth}` },
      h('div', { class: 'at' },
        h('div', { class: 'ar' },
          hasKids ? h('button', { class: 'twist' + (open ? ' open' : ''), type: 'button', 'aria-expanded': open ? 'true' : 'false', 'aria-label': (open ? 'Collapse ' : 'Expand ') + a.name, on: { click: () => this.toggle(path, open) } }, icon('chev')) : null,
          nameEl),
        h('div', { class: 'am' }, a.type ? h('span', { class: 'ty' }, a.type) : null, tags)),
      h('div', { class: 'av' }, v.el));
    if (v.copy !== undefined) row.append(copyButton(() => v.copy, { label: `Copy the value of ${a.name}`, tip: 'Copy the value', cls: 'row-cp', announce: this.ctx.announce, done: 'Copied the value' }));
    // a plain value is a way to find the nodes that hold the same: the server searches the values that it sends
    const fv = !locked && !hasKids && !a.unknown && !a.null && !this.d.bare ? facetValue(a.value) : null;
    if (fv !== null && this.ctx.onFacet) {
      row.append(h('button', { class: 'row-fx', type: 'button', 'aria-label': `Show the nodes with ${path} = ${fv}`, 'data-tip': 'Show the nodes with this value (shift: hide them, alt: under any name)',
        on: { click: (ev) => { ev.stopPropagation(); if (ev.altKey) this.ctx.onFacet('value', fv, ev.shiftKey); else this.ctx.onFacet('field', path + '=' + fv, ev.shiftKey); } } }, icon('search')));
    }
    host.append(row);
    if (hasKids && open) {
      const box = h('div', { class: 'kids', role: 'list', style: `--d:${depth + 1}` });
      for (const k of kids) this.row(box, k, path + '.' + k.name, depth + 1, a);
      if (a.truncated) box.append(h('div', { class: 'attr more-row', style: `--d:${depth + 1}` }, h('div', { class: 'at' }, h('small', null, `… ${a.truncated} more not sent`))));
      host.append(box);
    }
    return true;
  }

  toggle(path, was) {
    this.ctx.open.set(path, !was);
    this.drawKeep();
  }

  drawKeep() {
    const sc = this.host.closest('.ibody');
    const top = sc ? sc.scrollTop : 0;
    this.draw();
    if (sc) sc.scrollTop = top;
  }

  // ---- values ----

  // value returns { el, block, copy } for one attribute.
  value(a, kids, path) {
    if (a.redacted) return { el: withheld(a.redacted) };
    if (a.sensitive) return { el: withheld('sensitive') };
    if (a.unknown) return { el: unknownEl() };
    if (a.null) return { el: nullEl() };
    let t = a.type || '';
    // the value of an output has no type of its own: its shape says it
    if (t === 'output') t = kids.length ? (kids.every((k) => /^\d+$/.test(k.name)) ? 'list' : 'object') : a.value === undefined ? 'object' : 'string';
    if (kids.length || (/^(object|list|set|map|tuple)/.test(t) && a.value === undefined)) return this.collection(a, kids, t);
    return this.scalar(a.value, a.name, path, a.truncated);
  }

  collection(a, kids, t) {
    const n = kids.length + (a.truncated || 0);
    const word = isMap(t) ? plural(n, 'key') : plural(n, 'item');
    if (!kids.length) return { el: h('span', { class: 'v-sum' }, isMap(t) ? 'empty map' : 'empty list') };
    // a short list of plain values reads best as a row of tags
    if (isList(t) && kids.length <= 6 && kids.every((k) => !(k.children || []).length && !k.redacted && !k.sensitive && !k.unknown && !k.null && typeof k.value !== 'object')) {
      const vals = kids.map((k) => (typeof k.value === 'string' ? k.value : String(k.value)));
      if (vals.every((s) => s.length <= 34 && !s.includes('\n'))) {
        return { el: h('span', { class: 'v-tags' }, vals.map((s) => h('span', { class: 'v-tag' }, s)), a.truncated ? h('span', { class: 'v-sum' }, `+${a.truncated}`) : null), copy: JSON.stringify(vals), leaf: true };
      }
    }
    return { el: h('span', { class: 'v-sum' }, h('b', null, word), h('i', { class: 'pv' }, this.preview(kids, t))) };
  }

  preview(kids, t) {
    const parts = [];
    let len = 0;
    for (const k of kids) {
      let s;
      if (isMap(t) || !/^\d+$/.test(k.name)) s = k.name;
      else if (k.redacted || k.sensitive) s = '••••';
      else if (typeof k.value === 'string') s = k.value.length > 14 ? k.value.slice(0, 13) + '…' : k.value;
      else if (k.value !== undefined && k.value !== null && typeof k.value !== 'object') s = String(k.value);
      else if ((k.children || []).length) s = '{ ' + k.children.slice(0, 3).map((c) => c.name).join(', ') + (k.children.length > 3 ? ', …' : '') + ' }';
      else s = '…';
      if (len + s.length > 38) { parts.push('…'); break; }
      parts.push(s);
      len += s.length + 2;
    }
    return parts.join(', ');
  }

  scalar(v, name, path, truncated) {
    if (v === undefined || v === null) return { el: nullEl() };
    if (typeof v === 'boolean') return { el: h('span', { class: 'v-bool ' + v }, h('i', { 'aria-hidden': 'true' }), String(v)), copy: String(v) };
    if (typeof v === 'number') return { el: h('span', { class: 'v-num' }, String(v)), copy: String(v) };
    if (typeof v !== 'string') {
      let t;
      try { t = JSON.stringify(v, null, 2); } catch { t = String(v); }
      return this.jsonValue(t, path, null);
    }
    if (v === '') return { el: h('span', { class: 'v-empty' }, '""') };
    const doc = parseJSON(v);
    if (doc && !truncated) return this.jsonValue(JSON.stringify(doc, null, 2), path, doc);
    if (isHash(name, v)) return { el: this.hashEl(v), copy: v };
    const lines = v.split('\n');
    const long = v.length > 110 || lines.length > 1;
    if (!long) return { el: h('span', { class: 'v-str' }, v), copy: v };
    return this.longText(v, path, truncated);
  }

  hashEl(v) {
    const short = v.length > 26 ? v.slice(0, 10) + '…' + v.slice(-8) : v;
    const b = h('button', { class: 'v-hash', type: 'button', 'data-tip': `${v}\nClick to copy`, 'aria-label': `Hash ${short}. Click to copy.` }, short);
    b.addEventListener('click', () => {
      navigator.clipboard && navigator.clipboard.writeText(v).then(() => { b.classList.add('done'); b.textContent = 'copied'; setTimeout(() => { b.classList.remove('done'); b.textContent = short; }, 1100); }, () => {});
      this.ctx.announce && this.ctx.announce('Copied the hash');
    });
    return b;
  }

  jsonValue(pretty, path, doc) {
    const key = 'j:' + path;
    const lines = pretty.split('\n').length;
    const open = this.ctx.open.has(key) ? this.ctx.open.get(key) : lines <= 9;
    const isArr = Array.isArray(doc);
    const count = doc ? (isArr ? doc.length : Object.keys(doc).length) : 0;
    const label = doc ? (isArr ? `[ ${plural(count, 'item')} ]` : `{ ${plural(count, 'key')} }`) : 'JSON';
    const head = h('button', { class: 'jhead' + (open ? ' open' : ''), type: 'button', 'aria-expanded': open ? 'true' : 'false', on: { click: () => { this.ctx.open.set(key, !open); this.drawKeep(); } } },
      icon('chev'), h('span', { class: 'jl' }, 'JSON'), h('span', { class: 'jc' }, label));
    const el = h('div', { class: 'jval' }, head, open ? textBlock(pretty, { json: true }) : h('div', { class: 'jprev' }, oneLine(pretty)));
    return { el, block: true, copy: pretty };
  }

  longText(v, path, truncated) {
    const key = 't:' + path;
    const open = this.ctx.open.get(key) === true;
    const lines = v.split('\n').length;
    const el = h('div', { class: 'ltxt' + (open ? ' open' : '') },
      open ? textBlock(v, { wrap: true }) : h('div', { class: 'lclamp' }, v),
      h('button', { class: 'more-btn', type: 'button', 'aria-expanded': open ? 'true' : 'false', on: { click: () => { this.ctx.open.set(key, !open); this.drawKeep(); } } }, open ? 'Show less' : `Show all · ${lines > 1 ? plural(lines, 'line') : v.length + ' characters'}`),
      truncated ? h('span', { class: 'v-sum' }, ` · ${truncated} bytes more not sent`) : null);
    return { el, block: true, copy: v };
  }
}

function oneLine(pretty) {
  const t = pretty.replace(/\s+/g, ' ');
  return t.length > 90 ? t.slice(0, 89) + '…' : t;
}

// the record's times, as a definition list
export function timeRow(iso) {
  return iso ? [h('span', null, relTime(iso)), h('span', { class: 'dim' }, ' · ' + stamp(iso))] : null;
}
