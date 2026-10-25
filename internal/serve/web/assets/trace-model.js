// The Traces view: the model of a recorded run, with no DOM, so that a script can check it (dev/check-traces.mjs).
//
// A run is a list of spans with a parent each. The model links them into a tree, finds each span's self time (the
// time in it that none of its children covers), and gives each span a row so that spans that overlap in time sit on
// different rows: the children of one span that run at once (the resources of an apply) are packed into lanes under
// it, and a lane is as tall as the tallest subtree in it.

import { parseQuery } from './query.js';

// fmtDur writes a duration given in microseconds: 840µs, 12.4ms, 3.20s, 1m 12s.
export function fmtDur(us) {
  if (!isFinite(us)) return '';
  const a = Math.abs(us);
  if (a < 1000) return `${Math.round(us)}µs`;
  if (a < 10000) return `${(us / 1000).toFixed(2)}ms`;
  if (a < 1e5) return `${(us / 1000).toFixed(1)}ms`;
  if (a < 1e6) return `${Math.round(us / 1000)}ms`;
  if (a < 6e7) return `${(us / 1e6).toFixed(a < 1e7 ? 2 : 1)}s`;
  const s = Math.round(us / 1e6);
  if (s < 3600) return `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`;
  return `${Math.floor(s / 3600)}h ${String(Math.floor((s % 3600) / 60)).padStart(2, '0')}m`;
}

// ticks returns the marks of a time axis over [v0, v1] microseconds: a step of 1, 2 or 5 times a power of ten, with
// about `want` marks.
export function ticks(v0, v1, want = 8) {
  const span = v1 - v0;
  if (!(span > 0)) return [];
  const raw = span / want;
  const p = Math.pow(10, Math.floor(Math.log10(raw)));
  const step = [1, 2, 5, 10].map((m) => m * p).find((x) => x >= raw) || 10 * p;
  const out = [];
  for (let t = Math.ceil(v0 / step) * step; t <= v1 + 1e-9; t += step) out.push(Math.round(t * 1e6) / 1e6);
  return out;
}

// build links the spans of a run. It returns {spans, byId, roots, rows, dur}: each span gets `end`, `kids`, `parent`
// (the span or null), `depth`, `self`, `row` and `idx` (its place in the array). A span whose parent is not in the
// run is a root: the run continued the trace of its caller.
export function build(trace) {
  const spans = (trace.spans || []).map((x, idx) => ({ ...x, pid: x.parent || '', idx, end: x.start_us + x.dur_us, kids: [], parent: null, depth: 0, self: 0, row: 0, h: 1, rel: 0 }));
  const byId = new Map(spans.map((x) => [x.id, x]));
  const roots = [];
  for (const sp of spans) {
    const p = byId.get(sp.pid);
    if (p && p !== sp) { sp.parent = p; p.kids.push(sp); } else roots.push(sp);
  }
  const byStart = (a, b) => a.start_us - b.start_us || b.end - a.end || a.idx - b.idx;
  roots.sort(byStart);
  const walk = (sp, depth, seen) => {
    sp.depth = depth;
    sp.kids.sort(byStart);
    // self time: the length of the span minus the union of its children, clipped to it
    let covered = 0, edge = sp.start_us;
    for (const k of sp.kids) {
      const a = Math.max(k.start_us, edge, sp.start_us), b = Math.min(k.end, sp.end);
      if (b > a) { covered += b - a; edge = b; }
    }
    sp.self = Math.max(0, sp.dur_us - covered);
    for (const k of sp.kids) walk(k, depth + 1, seen);
  };
  // a cycle in the parents would leave a span out of every root: a recorded run has none, but a file is a file
  for (const r of roots) walk(r, 0);
  const seen = new Set();
  const mark = (sp) => { seen.add(sp); sp.kids.forEach(mark); };
  roots.forEach(mark);
  for (const sp of spans) if (!seen.has(sp)) { sp.parent = null; sp.kids = []; roots.push(sp); walk(sp, 0); }
  const rows = packLanes(roots);
  const dur = Math.max(trace.duration_us || 0, ...spans.map((x) => x.end), 1);
  return { spans, byId, roots, rows, dur };
}

// packLanes gives every span a row and returns the number of rows.
function packLanes(roots) {
  const pack = (sp) => {
    for (const k of sp.kids) pack(k);
    sp.h = 1 + lanes(sp.kids);
  };
  // lanes places a list of spans, sorted by start, into the first lane that is free when each starts; a lane is as tall
  // as the tallest subtree in it. It sets rel (the row under the parent) and returns the height of all the lanes.
  const lanes = (list) => {
    const ls = [];
    for (const k of list) {
      let lane = ls.find((l) => l.end <= k.start_us);
      if (!lane) { lane = { end: -Infinity, h: 0, items: [] }; ls.push(lane); }
      lane.items.push(k);
      lane.end = k.end;
      lane.h = Math.max(lane.h, k.h);
    }
    let base = 0;
    for (const l of ls) { for (const k of l.items) k.rel = base; base += l.h; }
    return base;
  };
  for (const r of roots) pack(r);
  const total = lanes(roots);
  const place = (sp, row) => { sp.row = row; for (const k of sp.kids) place(k, row + 1 + k.rel); };
  for (const r of roots) place(r, r.rel);
  return total;
}

// path lists a span and its ancestors, outermost first.
export function path(sp) {
  const out = [];
  for (let x = sp; x; x = x.parent) out.unshift(x);
  return out;
}

// labelOf is what a bar says: the address of a resource, the RPC of a provider call, the name of anything else.
export function labelOf(sp) {
  if (sp.kind === 'resource') return sp.attrs && sp.attrs['pravo.resource.address'] ? String(sp.attrs['pravo.resource.address']) : sp.name.replace(/^resource /, '');
  if (sp.kind === 'provider') return (sp.attrs && sp.attrs['pravo.provider'] ? sp.attrs['pravo.provider'] + ' · ' : '') + sp.name.replace(/^provider /, '');
  if (sp.kind === 'phase' || sp.kind === 'setup' || sp.kind === 'state') return sp.name.replace(/^(phase|setup|state) /, '');
  if (sp.kind === 'seal') return sp.name;
  return sp.name;
}

// The filter language of the box: words, and column:value terms that the menu offers and the pills show.
export const COLUMNS = ['kind', 'is', 'provider', 'type', 'attr'];
export const ALIAS = { k: 'kind', kinds: 'kind', status: 'is', providers: 'provider', resource_type: 'type', attribute: 'attr', attrs: 'attr', a: 'attr' };
export const LANG = { columns: COLUMNS, alias: ALIAS };

// searchText is what a filter looks in: the name, the message and the values of the attributes.
export function searchText(sp) {
  const parts = [sp.name, sp.message || ''];
  for (const k in sp.attrs || {}) parts.push(k, String(sp.attrs[k]));
  return parts.join('\n').toLowerCase();
}

// valueOf is what a span holds for a column: its kind, `error` when it failed, the provider it is for, the resource type.
export function valueOf(sp, col) {
  const a = sp.attrs || {};
  if (col === 'kind') return sp.kind || '';
  if (col === 'is') return sp.status === 'error' ? 'error' : '';
  if (col === 'provider') return String(a['pravo.provider'] || '').split('.')[0];
  if (col === 'type') return String(a['pravo.resource.type'] || '');
  return '';
}

// attrHit tells whether a span has an attribute that `key` or `key=value` names. A key is a whole attribute name or its end
// (`provider` finds pravo.provider), and the value is matched whole, with * for any run of characters.
export function attrHit(sp, spec) {
  const eq = spec.indexOf('=');
  const key = (eq < 0 ? spec : spec.slice(0, eq)).toLowerCase();
  const want = eq < 0 ? null : spec.slice(eq + 1).toLowerCase();
  const re = want && want.includes('*') ? wild(want) : null;
  for (const k in sp.attrs || {}) {
    const lk = k.toLowerCase();
    if (lk !== key && !lk.endsWith('.' + key)) continue;
    if (want === null) return true;
    const v = Array.isArray(sp.attrs[k]) ? sp.attrs[k].join(',') : String(sp.attrs[k]);
    if (re ? re.test(v.toLowerCase()) : v.toLowerCase() === want) return true;
  }
  return false;
}

const wild = (v) => (v.includes('*') ? new RegExp('^' + v.split('*').map((x) => x.replace(/[.+?^${}()|[\]\\]/g, '\\$&')).join('.*') + '$') : null);

// matcher compiles the text of the filter box. Words must all be found (in any order); a term names a column and what it
// holds: `is:error` keeps the spans that failed, `kind:resource,provider` the spans of those kinds, `provider:aws`,
// `type:aws_s3*`; a minus turns a term round. Terms and words are ANDed. An empty filter is null.
export function matcher(q) {
  const { words, terms } = parseQuery(q, LANG);
  if (!words.length && !terms.length) return null;
  const tests = terms.map((t) => {
    if (t.col === 'attr') {
      return (sp) => {
        const hit = t.values.some((v) => attrHit(sp, v));
        return t.neg ? !hit : hit;
      };
    }
    const res = t.values.map((v) => wild(v));
    return (sp) => {
      const have = valueOf(sp, t.col).toLowerCase();
      const hit = t.values.some((v, i) => (res[i] ? res[i].test(have) : have === v));
      return t.neg ? !hit : hit;
    };
  });
  return (sp) => tests.every((f) => f(sp)) && words.every((w) => searchText(sp).includes(w));
}
