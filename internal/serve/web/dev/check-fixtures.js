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
    case 'int': if (!Number.isInteger(value)) fail(where, `want an integer, got ${JSON.stringify(value)}`); return;
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
  const type = { graph: 'Graph', detail: 'Detail', source: 'Source' }[kind];
  if (!type) { fail(f, 'unknown fixture kind'); continue; }
  const before = errors;
  check(v, type, f);
  // cross checks the type system does not know
  if (type === 'Graph') {
    const addrs = new Set(v.nodes.map((n) => n.addr));
    const mods = new Set((v.modules || []).map((m) => m.path));
    for (const e of v.edges || []) if (!addrs.has(e.dependency) || !addrs.has(e.dependent)) fail(f, `edge to a node that is not there: ${e.dependency} -> ${e.dependent}`);
    for (const n of v.nodes) if (n.module !== '' && !mods.has(n.module)) fail(f, `node ${n.addr} in unknown module ${n.module}`);
    for (const m of v.modules || []) if (m.parent !== '' && !mods.has(m.parent)) fail(f, `module ${m.path} has unknown parent ${m.parent}`);
    if (new Set(v.nodes.map((n) => n.addr)).size !== v.nodes.length) fail(f, 'duplicate addresses');
  }
  console.log(`${errors === before ? 'ok  ' : 'FAIL'} ${f}`);
}
process.exit(errors ? 1 : 0);
