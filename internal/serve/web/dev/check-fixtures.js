'use strict';
// check-fixtures.js reads the Go types of ../../api.go and verifies that every fixture matches them:
// no key that the type does not have, every key that is not omitempty present, the JSON type of each value.
// Run: node check-fixtures.js   (exit status 1 on a mismatch)

const fs = require('fs');
const path = require('path');

const goSource = fs.readFileSync(path.join(__dirname, '..', '..', 'api.go'), 'utf8')
  .split('\n').map((l) => l.replace(/\/\/.*$/, '')).join('\n');

// parseStruct reads "struct {" ... "}" starting at the opening brace index; returns fields and the end index.
function parseBody(src, open) {
  let depth = 0, i = open;
  for (; i < src.length; i++) {
    if (src[i] === '{') depth++;
    else if (src[i] === '}' && --depth === 0) break;
  }
  const body = src.slice(open + 1, i);
  const fields = [];
  // split the body into declarations at top level
  let d = 0, cur = '';
  const decls = [];
  for (const ch of body) {
    if (ch === '{') d++;
    if (ch === '}') d--;
    if (ch === '\n' && d === 0) { if (cur.trim()) decls.push(cur.trim()); cur = ''; } else cur += ch;
  }
  if (cur.trim()) decls.push(cur.trim());
  for (const decl of decls) {
    const m = decl.match(/^(\w+)\s+([\s\S]+?)\s+`json:"([^"]*)"`$/);
    if (!m) continue;
    const [, goName, type, tag] = m;
    const [name, ...opts] = tag.split(',');
    if (name === '-') continue;
    let t = type.trim();
    let anon = null;
    if (t.startsWith('struct')) anon = parseBody(t, t.indexOf('{')).fields;
    fields.push({ goName, name, type: t, anon, omitempty: opts.includes('omitempty') });
  }
  return { fields, end: i };
}

const types = {};
for (const m of goSource.matchAll(/type (\w+) struct \{/g)) {
  const open = m.index + m[0].length - 1;
  types[m[1]] = parseBody(goSource, open).fields;
}

let errors = 0;
const fail = (where, msg) => { errors++; console.error(`${where}: ${msg}`); };

function check(value, type, where, anon) {
  type = type.trim();
  if (type.startsWith('*')) {
    if (value === null || value === undefined) return;
    return check(value, type.slice(1), where, anon);
  }
  if (type.startsWith('[]')) {
    if (value === null) return;
    if (!Array.isArray(value)) return fail(where, 'want an array');
    value.forEach((v, i) => check(v, type.slice(2), `${where}[${i}]`));
    return;
  }
  switch (type) {
    case 'string': if (typeof value !== 'string') fail(where, `want a string, got ${JSON.stringify(value)}`); return;
    case 'int': case 'int64': if (!Number.isInteger(value)) fail(where, `want an integer, got ${JSON.stringify(value)}`); return;
    case 'bool': if (typeof value !== 'boolean') fail(where, 'want a bool'); return;
    case 'any': return;
    case 'time.Time':
      if (typeof value !== 'string' || !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?Z$/.test(value)) fail(where, `want a time, got ${JSON.stringify(value)}`);
      return;
  }
  const fields = anon || (type.startsWith('struct') ? null : types[type]);
  if (!fields) return fail(where, `unknown type ${type}`);
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return fail(where, 'want an object');
  const known = new Map(fields.map((f) => [f.name, f]));
  for (const k of Object.keys(value)) {
    if (!known.has(k)) { fail(`${where}.${k}`, 'not in api.go'); continue; }
    const f = known.get(k);
    if (value[k] === null && f.type !== 'any' && !f.type.startsWith('[]') && !f.type.startsWith('*')) fail(`${where}.${k}`, 'null');
    if (value[k] !== null) check(value[k], f.type, `${where}.${k}`, f.anon);
  }
  for (const f of fields) if (!f.omitempty && !(f.name in value)) fail(`${where}.${f.name}`, 'missing (not omitempty)');
}

const dir = path.join(__dirname, 'fixtures');
const files = fs.readdirSync(dir).filter((f) => f.endsWith('.json')).sort();
for (const f of files) {
  const v = JSON.parse(fs.readFileSync(path.join(dir, f), 'utf8'));
  const kind = f.split('.')[0];
  const type = { graph: 'Graph', detail: 'Detail', source: 'Source', plans: 'PlanList', plan: 'PlanOverlay', access: 'Access', discovery: 'Discovery', adopt: 'Adopt' }[kind];
  if (!type) { fail(f, 'unknown fixture kind'); continue; }
  const before = errors;
  check(v, type, f);
  // cross checks the type system does not know
  if (type === 'Graph') {
    const addrs = new Set(v.nodes.map((n) => n.addr));
    const nss = new Set((v.namespaces || []).map((m) => m.path));
    for (const e of v.edges || []) if (!addrs.has(e.dependency) || !addrs.has(e.dependent)) fail(f, `edge to a node that is not there: ${e.dependency} -> ${e.dependent}`);
    for (const n of v.nodes) if (n.namespace !== '' && !nss.has(n.namespace)) fail(f, `node ${n.addr} in unknown namespace ${n.namespace}`);
    for (const m of v.namespaces || []) if (m.parent !== '' && !nss.has(m.parent)) fail(f, `namespace ${m.path} has unknown parent ${m.parent}`);
    if (new Set(v.nodes.map((n) => n.addr)).size !== v.nodes.length) fail(f, 'duplicate addresses');
    // an output is a node but not a resource: no provider, no record, no action; the namespaces and the plan count it apart
    const outs = new Map((v.outputs || []).map((o) => [o.name, o]));
    for (const n of v.nodes.filter((x) => x.mode === 'output')) {
      const w = `${f} output ${n.addr}`;
      if (n.type !== 'output') fail(w, `type ${n.type}`);
      for (const k of ['provider', 'revision', 'refreshed_at', 'action', 'locked', 'drifted', 'gone', 'plans', 'overlap', 'no_schema']) if (n[k] !== undefined) fail(w, `${k} on an output`);
      if (n.name !== n.addr.split('.').pop() || n.namespace !== n.addr.split('.').slice(0, -1).join('.')) fail(w, 'name or namespace');
      const o = outs.get(n.addr);
      if (!o) fail(w, 'not among the outputs of the graph');
      else if ((n.sensitive === 1) !== !!o.sensitive) fail(w, 'sensitive does not agree with the list of outputs');
      if (n.presence === 'state' && n.source) fail(w, 'an output that is not declared has no source');
    }
    for (const name of outs.keys()) if (!v.nodes.some((n) => n.addr === name && n.mode === 'output')) fail(f, `output ${name} is not a node`);
    for (const e of v.edges || []) if (v.nodes.find((n) => n.addr === e.dependency).mode === 'output' && v.nodes.find((n) => n.addr === e.dependent).mode !== 'output') fail(f, `a resource reads an output: ${e.dependency} -> ${e.dependent}`);
    for (const m of v.namespaces || []) {
      const below = v.nodes.filter((n) => n.addr.startsWith(m.path + '.'));
      if (m.resources !== below.filter((n) => n.mode !== 'output').length || (m.outputs || 0) !== below.filter((n) => n.mode === 'output').length) fail(f, `namespace ${m.path} counts ${m.resources} resources and ${m.outputs || 0} outputs`);
    }
    if (v.plan && v.plan.total !== v.nodes.filter((n) => n.mode !== 'output').length) fail(f, 'plan.total counts the outputs');
  }
  console.log(`${errors === before ? 'ok  ' : 'FAIL'} ${f}`);
}

// The access documents: the state and the verdict are among the values the page knows, the readers are matched
// by key, a suggestion has steps of the four kinds, a command is one line of pravo or git, and the document of a
// name that a graph has goes with that graph.
{
  const read = (f) => JSON.parse(fs.readFileSync(path.join(dir, f), 'utf8'));
  const STATES = new Set(['off', 'immutable', 'sealed', 'migrating']);
  const VERDICTS = new Set(['match', 'differs', 'missing', 'invalid', 'expected-sealed', 'none']);
  const YOU = new Set(['reader', 'not-reader', 'no-identity', 'locked']);
  const KINDS = new Set(['command', 'add-line', 'remove-line', 'note']);
  for (const f of files.filter((x) => x.startsWith('access.'))) {
    const name = f.split('.')[1];
    const a = read(f);
    const before = errors;
    if (!STATES.has(a.state)) fail(f, `state ${a.state}`);
    if (!VERDICTS.has(a.file.verdict)) fail(f, `verdict ${a.file.verdict}`);
    if (a.you && !YOU.has(a.you.state)) fail(f, `you ${a.you.state}`);
    if (!!a.you !== (a.state === 'sealed' || a.state === 'migrating')) fail(f, 'you is for a sealed state only');
    if (!!a.migration !== (a.state === 'migrating')) fail(f, 'migration is for a state that is migrating only');
    const graphFile = `graph.${name}.json`;
    if (files.includes(graphFile) && a.revision !== read(graphFile).revision) fail(f, `revision ${a.revision} is not the graph's`);
    const keys = new Set();
    for (const r of a.readers) {
      if (r.key && keys.has(r.key)) fail(f, `reader ${r.name} twice`);
      keys.add(r.key);
      if (!r.in_file && !r.in_store) fail(f, `reader ${r.name} is in neither`);
      if (r.key && !/^x25519:[A-Za-z0-9_-]{43}$/.test(r.key)) fail(f, `reader key of ${r.name}`);
    }
    const names = (list) => a.readers.filter(list).map((r) => r.name).sort().join();
    if (a.file.verdict === 'differs') {
      if (names((r) => r.in_file && !r.in_store) !== [...(a.file.added || [])].sort().join()) fail(f, 'added does not agree with the readers');
      if (names((r) => r.in_store && !r.in_file) !== [...(a.file.removed || [])].sort().join()) fail(f, 'removed does not agree with the readers');
    }
    if (a.file.verdict === 'invalid' && !(a.file.errors || []).length) fail(f, 'invalid without an error');
    // a repository that lists readers and a state that is not sealed: the verdict goes with the first suggestion
    if (a.file.verdict === 'expected-sealed' && (a.state === 'sealed' || a.state === 'migrating' || !a.file.count || (a.suggestions[0] || {}).id !== 'check-downgrade')) fail(f, 'expected-sealed is for a state that is not sealed, a file that loads and the first suggestion check-downgrade');
    if (a.suggestions.filter((s) => s.primary).length > 1) fail(f, 'two primary suggestions');
    for (const s of a.suggestions) {
      for (const st of s.steps) {
        if (!KINDS.has(st.kind)) fail(f, `step kind ${st.kind}`);
        if (/[\n\r]/.test(st.text)) fail(f, `a step with a line break in ${s.id}`);
        if (st.kind === 'command' && !/^(pravo|git) /.test(st.text)) fail(f, `command ${st.text}`);
        if ((st.kind === 'add-line' || st.kind === 'remove-line') && !st.file) fail(f, `${st.kind} without a file in ${s.id}`);
        if (st.placeholder && !/<[A-Za-z -]+>/.test(st.text)) fail(f, `placeholder without a placeholder in ${s.id}`);
        if (!st.placeholder && st.kind !== 'note' && /<[A-Za-z -]+>/.test(st.text)) fail(f, `an unmarked placeholder in ${s.id}: ${st.text}`);
      }
    }
    if (errors === before) console.log(`ok   ${f} (suggestions)`);
  }
}

// The stored plans, the graph and the overlays say the same things: every address is a node, every id on a node
// is a plan that can be applied, a node that two such plans change has the overlap, and the counts add up.
{
  const read = (f) => JSON.parse(fs.readFileSync(path.join(dir, f), 'utf8'));
  const OPEN = new Set(['pending', 'partial', 'partly-stale']);
  const STATUS = new Set(['pending', 'partial', 'partly-stale', 'stale', 'applied', 'expired']);
  const COUNT = { create: ['add'], update: ['change'], delete: ['destroy'], replace: ['add', 'destroy'], read: ['read'] };
  for (const f of files.filter((x) => x.startsWith('plans.'))) {
    const name = f.split('.')[1];
    const where = `${f}`;
    const graphFile = `graph.${name}.json`;
    if (!files.includes(graphFile)) { fail(where, `no ${graphFile} to go with it`); continue; }
    const g = read(graphFile), l = read(f);
    const nodes = new Map(g.nodes.map((n) => [n.addr, n]));
    const byId = new Map(l.plans.map((p) => [p.id, p]));
    const before = errors;
    if (l.revision !== g.revision) fail(where, `revision ${l.revision} is not the graph's ${g.revision}`);
    if (byId.size !== l.plans.length) fail(where, 'duplicate ids');
    for (let i = 1; i < l.plans.length; i++) if (l.plans[i].created > l.plans[i - 1].created) fail(where, 'not newest first');
    for (const p of l.plans) {
      const w = `${where} ${p.id}`;
      if (!/^plan-[0-9a-f]{6,64}$/.test(p.id)) fail(w, 'not an id');
      if (!STATUS.has(p.status)) fail(w, `status ${p.status}`);
      if (!!p.open !== OPEN.has(p.status)) fail(w, 'open does not agree with the status');
      if (p.apply !== 'pravo apply ' + p.id) fail(w, `apply is ${p.apply}`);
      if (p.status === 'stale' && !(p.reasons || []).length) fail(w, 'stale without a reason');
      if (p.status !== 'stale' && (p.reasons || []).length) fail(w, 'reasons on a plan that is not stale');
      // a partly stale plan says what conflicts and what can be applied by name; the others say neither
      if (p.status === 'partly-stale') {
        const left = p.touches.filter((t) => !t.applied).map((t) => t.addr);
        const conflicts = p.conflicts || [];
        const cAddrs = conflicts.map((c) => c.addr);
        if (!conflicts.length) fail(w, 'partly stale without a conflict');
        if (cAddrs.join() !== [...cAddrs].sort().join()) fail(w, 'the conflicts are not sorted by address');
        for (const c of conflicts) if (!(c.reasons || []).length) fail(w, `conflict on ${c.addr} without a reason`);
        if ((p.conflicts_omitted || 0) + conflicts.length > p.unapplied) fail(w, `${conflicts.length} conflicts and ${p.conflicts_omitted || 0} omitted of ${p.unapplied} unapplied`);
        if (p.unapplied !== left.length) fail(w, `unapplied ${p.unapplied}, the touches say ${left.length}`);
        const only = p.apply_only || [];
        if (!only.length) fail(w, 'partly stale without a part to apply');
        if (only.join() !== [...only].sort().join()) fail(w, 'apply_only is not sorted');
        for (const a of only) if (cAddrs.includes(a)) fail(w, `${a} is both a conflict and in apply_only`);
        if (p.apply_live !== `${p.apply} --only ${only.join(',')}`) fail(w, `apply_live is ${p.apply_live}`);
        for (const t of p.touches) if (!!t.conflict !== (cAddrs.includes(t.addr) && !t.applied)) fail(w, `touch ${t.addr}: conflict does not agree with the conflicts`);
      } else {
        for (const k of ['conflicts', 'conflicts_omitted', 'unapplied', 'apply_only', 'apply_only_omitted', 'apply_live']) if (p[k] !== undefined) fail(w, `${k} on a plan that is not partly stale`);
        if (p.touches.some((t) => t.conflict)) fail(w, 'a conflict on a plan that is not partly stale');
      }
      if (p.planned !== p.touches.length) fail(w, `planned ${p.planned}, ${p.touches.length} touches`);
      if ((p.applied || 0) !== p.touches.filter((t) => t.applied).length) fail(w, 'applied does not agree with the touches');
      const sum = { add: 0, change: 0, destroy: 0, read: 0 };
      for (const t of p.touches) {
        if (!nodes.has(t.addr)) fail(w, `touches ${t.addr}, which is not a node`);
        for (const k of COUNT[t.action] || []) sum[k]++;
      }
      for (const k of Object.keys(sum)) if (sum[k] !== p.summary[k]) fail(w, `summary.${k} is ${p.summary[k]}, the touches say ${sum[k]}`);
      for (const o of p.overlaps || []) {
        const other = byId.get(o.id);
        if (!other || !OPEN.has(other.status) || !OPEN.has(p.status)) fail(w, `overlaps ${o.id}, which is not an open plan`);
        else if (!(other.overlaps || []).some((x) => x.id === p.id)) fail(w, `overlaps ${o.id}, which does not say so`);
        // what a plan can change: the changes it has left, less those that conflict
        const left = (q) => q.touches.filter((t) => !t.applied && !t.conflict).map((t) => t.addr);
        for (const a of o.addrs) if (!other || !left(other).includes(a) || !left(p).includes(a)) fail(w, `overlap on ${a}, which the two do not both have a change left on`);
        const shared = other ? left(other).filter((a) => left(p).includes(a)).length : -1;
        if (o.count !== shared || o.addrs.length !== Math.min(shared, 20) || (o.truncated || 0) !== shared - o.addrs.length) fail(w, `overlap with ${o.id}: count ${o.count}, the plans share ${shared}`);
      }
    }
    for (const [addr, n] of nodes) {
      const want = (l.plans.filter((p) => OPEN.has(p.status) && p.touches.some((t) => t.addr === addr && !t.applied))).map((p) => p.id);
      if ((n.plans || []).join() !== want.join()) fail(`${graphFile} ${addr}`, `plans [${(n.plans || []).join()}], the list says [${want.join()}]`);
      // a change that a partly stale plan cannot apply keeps the plan on the node and is no overlap
      const live = l.plans.filter((p) => OPEN.has(p.status) && p.touches.some((t) => t.addr === addr && !t.applied && !t.conflict));
      if (!!n.overlap !== live.length > 1) fail(`${graphFile} ${addr}`, 'overlap does not agree with the plans');
    }
    // an overlay for each plan
    for (const p of l.plans) {
      const of = `plan.${p.id.replace(/^plan-/, '')}.json`;
      if (!files.includes(of)) { fail(where, `no ${of} for ${p.id}`); continue; }
      const o = read(of);
      if (o.plan.id !== p.id || o.plan.file !== p.id) fail(of, 'the plan is not the one it is named after');
      if (o.revision !== g.revision) fail(of, `revision ${o.revision}, the graph ${g.revision}`);
      if (o.plan.status !== p.status || !!o.plan.stale !== (p.status === 'stale')) fail(of, 'status');
      if (p.status !== 'stale' && (o.plan.reasons || []).length) fail(of, 'reasons on a plan that is not stale');
      if (o.nodes.map((n) => n.addr).join() !== p.touches.map((t) => t.addr).join() || o.nodes.some((n, i) => n.action !== p.touches[i].action || !!n.applied !== !!p.touches[i].applied)) fail(of, 'the nodes are not the touches of the plan');
      for (const k of ['add', 'change', 'destroy', 'read']) if (o.plan.summary[k] !== p.summary[k]) fail(of, `summary.${k}`);
      if (o.plan.planned !== p.planned || o.plan.total !== g.nodes.filter((n) => n.mode !== 'output').length) fail(of, 'planned of total');
    }
    console.log(`${errors === before ? 'ok  ' : 'FAIL'} ${f} goes with ${graphFile}`);
  }
  // a graph that names plans has a list of them
  for (const f of files.filter((x) => x.startsWith('graph.'))) {
    const name = f.split('.')[1];
    const g = read(f);
    if (g.nodes.some((n) => (n.plans || []).length) && !files.includes(`plans.${name}.json`)) fail(f, 'names stored plans and has no plans.' + name + '.json');
  }
}
process.exit(errors ? 1 : 0);
