'use strict';
// Shared helpers of the fixture generators (Node only; nothing here runs in the page).
// Output must match internal/serve/api.go exactly: check-fixtures.js verifies that.

const T0 = Date.parse('2026-10-21T14:02:11Z'); // loaded_at of every fixture
const MIN = 60e3, H = 3600e3, D = 86400e3;
const iso = (ago) => new Date(T0 - ago).toISOString().replace(/\.\d{3}Z$/, 'Z');

// A small deterministic generator so that regenerating changes nothing.
function rng(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// Fields of api.go that are not omitempty: their zero value is sent.
const KEEP = new Set(['records', 'locks', 'add', 'change', 'destroy', 'read', 'blast_radius', 'planned', 'total', 'resources', 'depth', 'line', 'no', 'schema_known', 'key', 'start_line', 'version', 'value', 'before', 'after', 'namespace', 'parent', 'text']);

// Drop keys whose value is the zero value of an omitempty field.
function prune(o) {
  if (Array.isArray(o)) return o.map(prune);
  if (o && typeof o === 'object') {
    const r = {};
    for (const k of Object.keys(o)) {
      const v = o[k];
      if (v === undefined) continue;
      if (!KEEP.has(k) && (v === false || v === '' || v === 0)) continue;
      r[k] = prune(v);
    }
    return r;
  }
  return o;
}

class Builder {
  constructor() {
    this.nodes = [];
    this.edges = [];
    this.index = new Map();
    this.refs = new Map(); // namespace path -> {declared, definition}
  }
  // add records a node. The namespace is the address without its last label.
  add(addr, type, o = {}) {
    const parts = addr.split('.');
    const n = {
      addr,
      mode: o.mode || 'managed',
      type,
      name: parts[parts.length - 1],
      namespace: parts.slice(0, -1).join('.'),
      provider: o.provider || type.split('_')[0],
      presence: o.presence || 'both',
      action: o.action,
      drifted: o.drifted,
      gone: o.gone,
      locked: o.locked,
      sensitive: o.sensitive,
      revision: o.revision === undefined ? 1 + (this.nodes.length * 7) % 13 : o.revision,
      refreshed_at: o.refreshed === null ? undefined : iso(o.refreshed === undefined ? 2 * H + (this.nodes.length * 11 % 40) * MIN : o.refreshed),
      source: o.presence === 'state' ? undefined : o.source,
    };
    this.nodes.push(n);
    this.index.set(addr, n);
    return n;
  }
  // output records the node of an output of the root: it has no provider, no revision and no refresh time, and
  // it is a node of the graph and not a resource (Namespace.resources and the counts of a plan leave it out)
  output(addr, o = {}) {
    const parts = addr.split('.');
    const n = {
      addr,
      mode: 'output',
      type: 'output',
      name: parts[parts.length - 1],
      namespace: parts.slice(0, -1).join('.'),
      presence: o.presence || 'both',
      sensitive: o.sensitive ? 1 : undefined,
      source: o.presence === 'state' ? undefined : o.source,
    };
    this.nodes.push(n);
    this.index.set(addr, n);
    return n;
  }
  dep(dependency, dependent) {
    if (!this.index.has(dependency) || !this.index.has(dependent)) throw new Error(`edge ${dependency} -> ${dependent}: unknown node`);
    this.edges.push({ dependency, dependent });
  }
  deps(dependent, ...dependencies) { for (const d of dependencies) this.dep(d, dependent); }
  ref(path, declared, definition) { this.refs.set(path, { declared, definition }); }

  namespaces() {
    const m = new Map();
    for (const n of this.nodes) {
      const parts = n.addr.split('.');
      for (let i = 1; i < parts.length; i++) {
        const path = parts.slice(0, i).join('.');
        let e = m.get(path);
        if (!e) {
          const r = this.refs.get(path) || {};
          e = { path, parent: parts.slice(0, i - 1).join('.'), name: parts[i - 1], resources: 0, outputs: 0, depth: i - 1, declared: r.declared, definition: r.definition };
          m.set(path, e);
        }
        if (n.mode === 'output') e.outputs++; else e.resources++;
      }
    }
    return [...m.values()].sort((a, b) => (a.path < b.path ? -1 : 1));
  }
  // reach returns the transitive dependents of addr.
  reach(addr) {
    const out = new Map();
    for (const e of this.edges) { if (!out.has(e.dependency)) out.set(e.dependency, []); out.get(e.dependency).push(e.dependent); }
    const seen = new Set(), q = [addr];
    while (q.length) for (const x of out.get(q.pop()) || []) if (!seen.has(x)) { seen.add(x); q.push(x); }
    return [...seen].sort();
  }
  dependsOn(addr) { return this.edges.filter((e) => e.dependent === addr).map((e) => e.dependency).sort(); }
  usedBy(addr) { return this.edges.filter((e) => e.dependency === addr).map((e) => e.dependent).sort(); }

  graph(meta) {
    const nodes = [...this.nodes].sort((a, b) => (a.addr < b.addr ? -1 : 1));
    const g = {
      version: 1,
      revision: meta.revision,
      project: meta.project,
      nodes,
      edges: [...this.edges].sort((a, b) => (a.dependency + a.dependent < b.dependency + b.dependent ? -1 : 1)),
      namespaces: this.namespaces(),
      outputs: meta.outputs || [],
      plan: meta.plan,
      warnings: meta.warnings,
    };
    return prune(g);
  }
}

// planOf summarises the actions of the nodes of a builder as a PlanInfo.
function planOf(b, o) {
  const c = { add: 0, change: 0, destroy: 0, read: 0 };
  let planned = 0;
  const changed = new Set();
  for (const n of b.nodes) {
    if (!n.action) continue;
    planned++;
    if (n.action === 'create') c.add++;
    else if (n.action === 'update') c.change++;
    else if (n.action === 'delete') c.destroy++;
    else if (n.action === 'replace') { c.add++; c.destroy++; }
    else if (n.action === 'read') c.read++;
    if (['create', 'update', 'replace', 'delete'].includes(n.action)) changed.add(n.addr);
  }
  const blast = new Set();
  for (const a of changed) for (const r of b.reach(a)) blast.add(r);
  return prune({
    file: o.file,
    created: iso(o.createdAgo),
    actor: o.actor,
    stale: o.stale,
    reasons: o.reasons,
    summary: { add: c.add, change: c.change, destroy: c.destroy, read: c.read, blast_radius: blast.size },
    planned,
    total: b.nodes.filter((n) => n.mode !== 'output').length,
    hints: o.hints,
  });
}

module.exports = { T0, MIN, H, D, iso, rng, prune, Builder, planOf };
