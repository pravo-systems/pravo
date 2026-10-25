// Development only: a fetch shim that answers /api/* from the fixtures, so that the interface can be built and
// checked without the Go server. The page loads it only when its URL has mock=NAME before the fragment:
//   /index.html?mock=platform            graph.platform.json (also: fleet, big, empty, specerror)
//   &auth=401                             every /api request answers 401
//   &fail=detail                          /api/detail answers 500 {"error": ...}
//   &latency=300                          every answer is delayed by that many milliseconds
//   &stale=1                              the saved plan is stale, with reasons
//   &plan=plan-2f9c1a4e                   the server lays that stored plan itself (pravo serve --plan plan-2f9c1a4e)
//   &discovery=NAME                       the Discovery view answers discovery.NAME.json: platform (five discoveries of every freshness), empty
//   &policies=NAME                        the Policies view answers policies.NAME.json: platform (six policies in every status), empty
//   &traces=NAME                          the Traces view answers traces.NAME.json: platform (an apply, a plan, an apply that failed and a drift check), empty
//   &access=NAME                          the Access view answers access.NAME.json: platform (sealed, a reader), off, immutable,
//                                         drift, locked (no identity: every record is sealed), notreader, missing (no readers file), migrating, invalid,
//                                         downgraded (the repository lists readers and the state is not sealed), solo (sealed to one reader: the warning)
// The plans stored in the state are plans.NAME.json (the platform has seven) and plan.ID.json, one for each.
// window.__mock.mutate() changes the graph and sends a "change" event; storePlan() stores another plan and
// collectPlan(id) removes one, each with its event; setAccess(name) swaps the Access document, with the event for the readers file; window.__errors collects the errors and warnings of the
// page, for a headless run.

const here = new URL('.', import.meta.url);
const params = new URLSearchParams(location.search);
const NAME = params.get('mock') || 'platform';
const latency = Number(params.get('latency') || 0);

// ---- errors, for a headless run ----
window.__errors = [];
for (const k of ['error', 'warn']) {
  const orig = console[k].bind(console);
  console[k] = (...a) => { window.__errors.push(k + ': ' + a.map(String).join(' ')); orig(...a); };
}
window.addEventListener('error', (e) => window.__errors.push('error: ' + e.message));
window.addEventListener('unhandledrejection', (e) => window.__errors.push('rejection: ' + (e.reason && e.reason.message || e.reason)));

const fixture = async (file) => {
  const r = await fetch(new URL('fixtures/' + file, here).href, { cache: 'no-store' }).catch(() => null);
  if (!r || !r.ok) return null;
  return r.json();
};

// ---- time: move every timestamp so that loaded_at is a little while ago ----
const TIME = /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/;
function shift(v, by) {
  if (Array.isArray(v)) return v.map((x) => shift(x, by));
  if (v && typeof v === 'object') { const o = {}; for (const k in v) o[k] = shift(v[k], by); return o; }
  if (typeof v === 'string' && TIME.test(v)) return new Date(Date.parse(v) + by).toISOString().replace(/\.\d{3}Z$/, 'Z');
  return v;
}

let graph = await fixture(`graph.${NAME}.json`);
let by = 0;
if (graph) {
  by = Date.now() - 40e3 - Date.parse(graph.project.loaded_at);
  graph = shift(graph, by);
  if (params.get('stale') === '1' && graph.plan) {
    graph.plan.stale = true;
    graph.plan.reasons = ['the state changed after the plan was made: api.db is at revision 15, the plan read revision 14', 'the provider aws is now version 5.62.0, the plan was made with 5.61.0'];
  }
}
// ---- the Discovery view: discovery.NAME.json, the platform's unless the query says another ----
// ---- the Policies view: policies.NAME.json, the platform's unless the query says another ----
let policiesName = params.get('policies') || (NAME === 'platform' ? 'platform' : 'empty');
let policies = await fixture(`policies.${policiesName}.json`);
// ---- the Traces view: traces.NAME.json lists the runs, and trace.XXXXXXXX.json (the first eight digits of the id) is each one ----
let tracesName = params.get('traces') || (NAME === 'platform' ? 'platform' : 'empty');
let traces = (await fixture(`traces.${tracesName}.json`)) || { version: 1, dir: '.pravo/traces', traces: [] };
const runStart = (iso, by) => new Date(Date.parse(iso) + by).toISOString();
let discoveryName = params.get('discovery') || (NAME === 'platform' ? 'platform' : 'empty');
let discovery = await fixture(`discovery.${discoveryName}.json`);
const adoptExample = await fixture('adopt.example.json');
// adoptOf makes the answer of /api/adopt for an object of the fixture, as the server words it
function adoptOf(name, key) {
  const d = discovery && discovery.discoveries.find((x) => x.name === name);
  const it = d && d.items.find((x) => x.key === key);
  if (!it) return null;
  const label = it.name ? ` (${it.name})` : '';
  const ident = `{\n${(it.identity && it.identity.length ? it.identity : [{ name: 'id', value: it.id }]).map((f) => `\t\t${f.name}: ${JSON.stringify(f.value)}\n`).join('')}\t}`;
  const res = (it.name || it.id).toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'adopted';
  const snippet = `// Adopts the ${d.type} "${it.id}"${label} that the discovery ${d.name} found.\n${res}: $${d.instance || 'provider'}.#Resource.${d.type} & {\n\t$import: ${ident}\n}\n`;
  const steps = [];
  if (it.status === 'adopting') steps.push({ kind: 'note', text: `${it.by} already names this object in $import. The next plan reads it and an apply records it.` });
  else {
    steps.push({ kind: 'note', text: 'To write the resource with the attributes the object has, ask the provider for it:' });
    steps.push({ kind: 'command', text: `pravo discover adopt ${d.name} ${it.id} --read` });
  }
  const ack = it.status === 'unmanaged' ? `// An entry of $ack in the discovery ${d.name}, which is the list \`$ack: [...]\` of its struct.\n{\n\tids: ["${it.id}"]\n\treason: "<why>"\n}\n` : undefined;
  return Object.assign({}, adoptExample, { discovery: name, key, id: it.id, status: it.status, snippet, steps, ack });
}

// ---- the Access view: access.NAME.json, the sealed platform unless the query says another ----
let accessName = params.get('access') || (NAME === 'platform' ? 'platform' : 'off');
let access = await fixture(`access.${accessName}.json`);
// a state that is sealed to a process that holds no key lists its records and shows no value
function lockGraph(g) {
  g = JSON.parse(JSON.stringify(g));
  let n = 0;
  for (const x of g.nodes) {
    if (x.presence === 'spec' || x.mode !== 'managed') continue;
    x.sealed = true;
    delete x.sensitive;
    delete x.no_schema;
    n++;
  }
  for (const o of g.outputs || []) { o.sensitive = true; o.redacted = 'sealed'; delete o.value; delete o.unknown; }
  g.warnings = (g.warnings || []).concat(`The state is sealed and ${n} records could not be opened, so their values are not shown.`);
  return g;
}
if (graph && access && access.you && access.you.state !== 'reader' && access.state !== 'off') graph = lockGraph(graph);
if (graph && access) graph.project.state.sealed = access.state === 'sealed' || access.state === 'migrating' ? true : undefined;
const detailFiles = NAME === 'platform' ? { 'api.db': 'detail.db.json', 'api.db_password': 'detail.password.json', 'edge.lb': 'detail.drifted.json', url: 'detail.output.json', summary: 'detail.outputmap.json', db_password: 'detail.outputsecret.json' } : {};
const sourceFiles = NAME === 'platform' ? { 'api.db': 'source.db.json', 'api.db_password': 'source.password.json', url: 'source.output.json' } : {};

// ---- the plans stored in the state: plans.NAME.json, and plan.ID.json for the overlay of each (the id without "plan-") ----
// pravo serve --plan plan-ID is mock=NAME&plan=plan-ID before the fragment: the graph then has that plan as its own.
let stored = graph && NAME === 'platform' ? await fixture(`plans.${NAME}.json`) : null;
stored = stored ? shift(stored, by) : { version: 1, revision: graph ? graph.revision : '', plans: [] };
const overlays = new Map();
async function overlayOf(id) {
  if (!overlays.has(id)) {
    const o = await fixture(`plan.${id.replace(/^plan-/, '')}.json`);
    overlays.set(id, o ? shift(o, by) : null);
  }
  return overlays.get(id);
}
if (graph && params.get('plan')) {
  // the server laid the plan itself: its actions are on the nodes and it is the plan of the graph
  const o = await overlayOf(params.get('plan'));
  if (o) {
    const act = new Map(o.nodes.map((n) => [n.addr, n.action]));
    graph.plan = o.plan;
    for (const n of graph.nodes) { if (act.has(n.addr)) n.action = act.get(n.addr); else delete n.action; }
  }
}

// ---- synthesised answers ----

const hash = (s) => { let h = 2166136261; for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); } return h >>> 0; };
const hex = (s, n) => { let o = ''; let h = hash(s); while (o.length < n) { o += h.toString(16); h = Math.imul(h ^ (h >>> 13), 0x5bd1e995) >>> 0; } return o.slice(0, n); };

// type -> [name, type, kind, value] ; strings may use {name} {ns} {id}
const TPL = {
  aws_vpc: [['cidr_block', 'string', 'required', '10.{n}.0.0/16'], ['enable_dns_hostnames', 'bool', 'optional', true], ['enable_dns_support', 'bool', 'optional', true], ['instance_tenancy', 'string', 'optional', 'default']],
  aws_subnet: [['availability_zone', 'string', 'optional,computed', 'eu-west-1a'], ['cidr_block', 'string', 'optional,computed', '10.{n}.{m}.0/24'], ['map_public_ip_on_launch', 'bool', 'optional', false], ['vpc_id', 'string', 'required', 'vpc-{id}']],
  aws_security_group: [['description', 'string', 'optional', 'Managed by pravo'], ['name', 'string', 'optional,computed', '{name}'], ['vpc_id', 'string', 'optional,computed', 'vpc-{id}'], ['revoke_rules_on_delete', 'bool', 'optional', false]],
  aws_instance: [['ami', 'string', 'optional,computed', 'ami-0{id}'], ['instance_type', 'string', 'optional,computed', 't3.medium'], ['subnet_id', 'string', 'optional,computed', 'subnet-{id}'], ['monitoring', 'bool', 'optional,computed', false], ['private_ip', 'string', 'computed', '10.{n}.{m}.{k}']],
  aws_lb: [['load_balancer_type', 'string', 'optional', 'application'], ['internal', 'bool', 'optional,computed', false], ['idle_timeout', 'number', 'optional', 60], ['dns_name', 'string', 'computed', '{name}-{id}.eu-west-1.elb.amazonaws.com']],
  aws_lb_target_group: [['port', 'number', 'optional,computed', 8080], ['protocol', 'string', 'optional,computed', 'HTTP'], ['target_type', 'string', 'optional,computed', 'ip'], ['vpc_id', 'string', 'optional,computed', 'vpc-{id}']],
  aws_ecs_service: [['cluster', 'string', 'optional,computed', 'arn:aws:ecs:eu-west-1:412058817224:cluster/estate'], ['desired_count', 'number', 'optional', 3], ['launch_type', 'string', 'optional,computed', 'FARGATE'], ['task_definition', 'string', 'required', '{name}:{rev}']],
  aws_ecs_task_definition: [['cpu', 'string', 'optional', '512'], ['family', 'string', 'required', '{name}'], ['memory', 'string', 'optional', '1024'], ['network_mode', 'string', 'optional,computed', 'awsvpc'], ['container_definitions', 'string', 'required', '[{"name":"{name}","image":"registry.example.com/{name}:1.4.2"}]']],
  aws_iam_role: [['assume_role_policy', 'string', 'required', '{"Version":"2012-10-17","Statement":[...]}'], ['max_session_duration', 'number', 'optional', 3600], ['name', 'string', 'optional,computed', '{name}'], ['path', 'string', 'optional', '/']],
  aws_sqs_queue: [['message_retention_seconds', 'number', 'optional', 345600], ['name', 'string', 'optional,computed', '{name}'], ['visibility_timeout_seconds', 'number', 'optional', 30]],
  aws_route53_record: [['name', 'string', 'required', '{name}.estate.example.com'], ['ttl', 'number', 'optional', 300], ['type', 'string', 'required', 'CNAME'], ['zone_id', 'string', 'required', 'Z0{id}']],
  aws_cloudwatch_log_group: [['name', 'string', 'optional,computed', '/estate/{name}'], ['retention_in_days', 'number', 'optional', 30]],
  aws_cloudwatch_metric_alarm: [['comparison_operator', 'string', 'required', 'GreaterThanThreshold'], ['evaluation_periods', 'number', 'required', 3], ['metric_name', 'string', 'optional', 'CPUUtilization'], ['threshold', 'number', 'optional', 80]],
  random_password: [['length', 'number', 'required', 24], ['special', 'bool', 'optional', true], ['upper', 'bool', 'optional', true]],
  aws_secretsmanager_random_password: [['password_length', 'number', 'optional', 32], ['exclude_punctuation', 'bool', 'optional', true]],
  random_id: [['byte_length', 'number', 'required', 4], ['hex', 'string', 'computed', '{id}']],
  local_file: [['content', 'string', 'optional', 'release 42\n'], ['filename', 'string', 'required', './release.txt'], ['file_permission', 'string', 'optional', '0644']],
};
const GENERIC = [['name', 'string', 'optional,computed', '{name}']];
// what the provider schema says about an attribute, for the Schema tab of a node that has no record
const DESC = { length: 'The length of the string desired.', special: 'Include special characters in the result.', upper: 'Include uppercase alphabet characters in the result.', result: 'The generated random string.',
  password_length: 'The length of the password. The default is 32.', exclude_punctuation: 'Do not include punctuation in the password.', random_password: 'The generated password.' };

const fill = (v, n, ctx) => (typeof v !== 'string' ? v : v.replace(/\{name\}/g, n.name).replace(/\{ns\}/g, n.namespace).replace(/\{id\}/g, hex(n.addr, 17))
  .replace(/\{rev\}/g, String(n.revision || 1)).replace(/\{n\}/g, String(10 + (ctx.h % 20))).replace(/\{m\}/g, String((ctx.h >> 5) % 250)).replace(/\{k\}/g, String(4 + ((ctx.h >> 9) % 240))));

function provRef(n) {
  const key = n.provider || n.type.split('_')[0];
  const base = key.split('.')[0];
  const src = { aws: ['hashicorp/aws', '5.61.0'], random: ['hashicorp/random', '3.6.2'], local: ['hashicorp/local', '2.5.1'], null: ['hashicorp/null', '3.2.2'] }[base] || ['hashicorp/' + base, '1.0.0'];
  return { key, source: 'registry.opentofu.org/' + src[0], version: src[1], protocol: '6' };
}

function synthAttrs(n) {
  const ctx = { h: hash(n.addr) };
  const t = TPL[n.type] || GENERIC;
  const attrs = [];
  const spec = n.presence === 'spec';
  if (n.type.startsWith('aws_')) {
    attrs.push({ name: 'arn', type: 'string', kind: 'computed', ...(spec ? { unknown: true } : { value: `arn:aws:${n.type.split('_')[1]}:eu-west-1:412058817224:${n.name}/${hex(n.addr, 10)}` }) });
    attrs.push({ name: 'id', type: 'string', kind: 'optional,computed', ...(spec ? { unknown: true } : { value: hex(n.addr, 12) }) });
  }
  for (const [name, type, kind, v] of t) {
    const a = { name, type, kind };
    if (spec && kind.includes('computed') && !kind.includes('optional')) a.unknown = true;
    else a.value = fill(v, n, ctx);
    attrs.push(a);
  }
  for (let i = 0; i < n.sensitive; i++) {
    const nm = ['password', 'secret_string', 'token', 'private_key'][i % 4];
    attrs.push({ name: nm, type: 'string', kind: 'optional', sensitive: true, redacted: 'sensitive' });
  }
  if (n.type.startsWith('aws_')) {
    attrs.push({ name: 'tags', type: 'map(string)', kind: 'optional', children: [{ name: 'Environment', type: 'string', value: 'production' }, { name: 'ManagedBy', type: 'string', value: 'pravo' }, { name: 'Namespace', type: 'string', value: n.namespace || 'root' }] });
  }
  attrs.sort((a, b) => (a.name < b.name ? -1 : 1));
  return attrs;
}

// the detail of an output: one attribute, value, shaped by what the graph lists for it; no record, no history, no lock
function synthOutput(n) {
  const g = graph;
  const o = (g.outputs || []).find((x) => x.name === n.addr) || {};
  const val = { name: 'value', type: 'output' };
  const kids = (v) => (Array.isArray(v) ? v.map((x, i) => [String(i), x]) : Object.entries(v)).map(([k, x]) => (x && typeof x === 'object' ? { name: k, children: kids(x) } : { name: k, value: x }));
  if (o.sensitive || n.sensitive) { val.sensitive = true; val.redacted = o.redacted || 'sensitive'; } else if (o.unknown || n.presence === 'spec') val.unknown = true;
  else if (o.value && typeof o.value === 'object') val.children = kids(o.value);
  else val.value = o.value;
  const attrs = [val];
  const notes = [n.presence === 'state' ? 'Recorded in the state, and the specification no longer declares it: the next apply would remove it.' : n.presence === 'spec' ? 'Declared in the specification and not published yet: no apply has recorded its value.' : 'The value is the one the last apply recorded.'];
  return { version: 1, node: { ...n, deps: undefined, used: undefined, ns: undefined }, provider: { key: '' }, attrs, schema_known: true,
    depends_on: g.edges.filter((e) => e.dependent === n.addr).map((e) => e.dependency).sort(), used_by: g.edges.filter((e) => e.dependency === n.addr).map((e) => e.dependent).sort(), times: {}, notes };
}

// what the server sends for a node that is never recorded: the schema of its type, and for an ephemeral resource what the specification sets,
// for an environment variable its declaration and the value in the environment of the process
function synthTransient(n, d, deps) {
  const env = n.type === 'env' || n.type === 'secret_env';
  if (env) {
    const secret = n.type === 'secret_env';
    const name = n.name.replace(/([a-z0-9])([A-Z])/g, '$1_$2').toUpperCase();
    const def = n.name === 'prefix' ? 'acme' : null;
    d.provider = { key: '' };
    d.schema = [{ name: 'name', type: 'string', kind: 'required', description: 'The name of the environment variable.' },
      ...(secret ? [] : [{ name: 'default', type: 'string', kind: 'optional', description: 'Used when the variable is not set. Without it, an unset variable is an error.' }]),
      { name: 'value', type: 'string', kind: 'computed', description: 'What the variable holds.', ...(secret ? { sensitive: true } : {}) }];
    d.attrs = [{ name: 'name', type: 'string', kind: 'required', value: name }];
    if (!secret) d.attrs.push({ name: 'default', type: 'string', kind: 'optional', ...(def ? { value: def } : { null: true }) });
    const value = { name: 'value', type: 'string', kind: 'computed' };
    if (secret) Object.assign(value, { sensitive: true, redacted: 'sensitive' });
    else if (def) value.value = def;
    else value.value = 'acme-' + n.name.replace(/([a-z0-9])([A-Z])/g, '$1-$2').toLowerCase();
    d.attrs.push(value);
    d.notes = (d.notes || []).concat(secret
      ? ['A secret variable shows no value. Its value is ephemeral: it is kept out of the state and out of a saved plan, and out of this page.']
      : def ? ['The variable ' + name + ' is not set in the environment of pravo serve, so the value is the default.'] : ['The value is that of the variable in the environment of pravo serve. A command reads the variable from its own environment.']);
    d.notes.push('A built-in value is evaluated when a command runs and is never recorded in the state.');
    return;
  }
  const t = TPL[n.type] || GENERIC;
  const ctx = { h: hash(n.addr) };
  d.schema = [...t.map(([name, type, kind]) => ({ name, type, kind, description: DESC[name] })),
    ...(n.mode === 'ephemeral' ? [{ name: 'result', type: 'string', kind: 'computed', sensitive: true, description: DESC.result }] : [])].sort((a, b) => (a.name < b.name ? -1 : 1));
  if (n.mode === 'ephemeral') {
    // the values that the specification sets; one that reads another node is not known before a command runs
    d.attrs = t.filter(([, , kind]) => !kind.includes('computed') || kind.includes('optional')).map(([name, type, kind, v]) => ({ name, type, kind, value: fill(v, n, ctx) }));
    if (deps.length && d.attrs.length > 1) { const a = d.attrs[d.attrs.length - 1]; delete a.value; a.unknown = true; }
    d.notes = (d.notes || []).concat('These are the values that the specification sets. What the provider fills in is known only while a command runs.',
      'An ephemeral resource is evaluated when a command runs and is never recorded in the state.');
  } else {
    d.attrs = [];
    d.notes = (d.notes || []).concat('A call is evaluated when a command runs and is never recorded in the state.');
  }
}

function synthDetail(n) {
  if (n.mode === 'output') return synthOutput(n);
  const g = graph;
  const attrs = synthAttrs(n);
  const deps = g.edges.filter((e) => e.dependent === n.addr).map((e) => e.dependency).sort();
  const used = g.edges.filter((e) => e.dependency === n.addr).map((e) => e.dependent).sort();
  const out = new Map();
  for (const e of g.edges) { if (!out.has(e.dependency)) out.set(e.dependency, []); out.get(e.dependency).push(e.dependent); }
  const seen = new Set(), q = [n.addr];
  while (q.length) for (const x of out.get(q.pop()) || []) if (!seen.has(x)) { seen.add(x); q.push(x); }
  const times = {};
  if (n.refreshed_at) { times.refreshed_at = n.refreshed_at; times.updated_at = n.refreshed_at; times.applied_at = n.refreshed_at; }
  if (n.drifted) times.drifted_at = new Date(Date.now() - 42 * 60e3).toISOString().replace(/\.\d{3}Z$/, 'Z');
  const notes = [];
  if (n.presence === 'state') notes.push('This resource is in the state but no longer declared. The next apply destroys it.');
  if (n.presence === 'spec') notes.push('This resource is declared and not created yet. The values below are the planned ones; the rest is known after apply.');
  if (n.gone) notes.push('The object does not exist at the provider any more. The next plan creates it again.');
  const d = { version: 1, node: { ...n, deps: undefined, used: undefined, ns: undefined }, provider: provRef(n), attrs, schema_known: n.type !== 'aws_wafv2_web_acl', depends_on: deps, used_by: used, times };
  if (n.sealed) { d.attrs = []; d.schema_known = true; notes.push('The attributes of this record are sealed, and no identity was given. Start the viewer with pravo serve --identity NAME. Resources are listed and no value is shown.'); }
  if (!d.schema_known) d.attrs = attrs.map((a) => ({ name: a.name, type: a.type, kind: a.kind, redacted: 'schema' }));
  if (seen.size) d.reach = [...seen].sort();
  if (notes.length) d.notes = notes;
  if (['ephemeral', 'call', 'builtin'].includes(n.mode)) { synthTransient(n, d, deps); return d; }
  if (n.locked) d.lock = { kind: 'lock', user: 'ci', host: 'runner-07', command: 'pravo apply release-42.plan', actor: 'ci@acme', since: new Date(Date.now() - 6 * 60e3).toISOString().replace(/\.\d{3}Z$/, 'Z') };
  // history from the revision
  const hist = [];
  const kinds = ['apply', 'apply', 'edit', 'apply', 'import'];
  for (let i = 0; i < Math.min(n.revision || 0, 5); i++) {
    const rev = (n.revision || 0) - i;
    hist.push({ id: `20261${(10 - i % 9)}${String(10 + i).padStart(2, '0')}T0${i}1200Z-${hex(n.addr + i, 6)}`, kind: i === (n.revision - 1) ? 'import' : kinds[i % kinds.length], status: i === 3 ? 'failed' : 'applied', time: new Date(Date.now() - (i * 5 + 2) * 86400e3).toISOString().replace(/\.\d{3}Z$/, 'Z'), user: i % 2 ? 'grace' : 'ada', command: i === 3 ? 'pravo apply release.plan' : 'pravo apply', action: i === (n.revision - 1) ? 'import' : 'update', rev_before: rev - 1, rev_after: rev, ...(i === 0 ? { plan: 'plan-2f9c1a4e', git_revision: hex(n.addr, 8) + 'c0ffee' } : {}), ...(i === 0 ? { attrs: [{ path: 'port', before: 5432, after: 5433 }, { path: 'tags.owner', before: 'team', after: 'ops' }, { path: 'password', before_redacted: true, after_redacted: true }] } : i === 1 ? { attrs_sealed: true } : i === 2 ? { attrs_omitted: true } : {}) });
  }
  if (hist.length) d.history = hist;
  // the plan's decision
  if (n.action && n.action !== 'no-op' && n.action !== 'read') {
    const c = { action: n.action, attrs: [] };
    const plain = attrs.filter((a) => !a.children && !a.sensitive && !a.redacted);
    if (n.action === 'create') {
      c.reasons = ['not in the state'];
      for (const a of attrs) { if (a.children) continue; c.attrs.push(a.sensitive ? { path: a.name, after_redacted: true } : a.unknown ? { path: a.name, after_unknown: true } : { path: a.name, after: a.value }); }
    } else if (n.action === 'delete') {
      c.reasons = [n.presence === 'state' ? 'no longer declared' : 'removed from the specification'];
      for (const a of attrs) { if (a.children) continue; c.attrs.push(a.sensitive ? { path: a.name, before_redacted: true } : { path: a.name, before: a.value }); }
    } else {
      const pick = plain.filter((a) => a.kind !== 'computed' && a.value !== undefined).slice(0, n.action === 'replace' ? 1 : 2);
      for (const a of pick) c.attrs.push({ path: a.name, before: typeof a.value === 'number' ? a.value - 1 : typeof a.value === 'boolean' ? !a.value : String(a.value).replace(/\d+/, (m) => String(Number(m) + 1)), after: a.value });
      if (n.drifted) c.reasons = ['the provider reports a different value than the state'];
      else c.reasons = pick.map((a) => `${a.name} changed in ${n.source ? n.source.file + ':' + n.source.line : 'the specification'}`);
      if (n.action === 'replace' && pick[0]) { c.requires_replace = [pick[0].name]; c.reasons.push(`${pick[0].name} cannot be updated in place`); }
      if (n.sensitive) c.attrs.push({ path: 'password', before_redacted: true, after_redacted: true });
    }
    d.change = c;
  }
  return d;
}

function synthSource(n) {
  if (n.mode === 'output') {
    const f = n.source ? n.source.file : 'main.cue';
    const start = n.source ? n.source.line : 10;
    const text = n.presence === 'state' ? null : [`${n.name}: pravo.#Output & {`, n.sensitive ? '\tvalue:     "\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022"' : `\tvalue:     ${JSON.stringify(n.name + ' value')}`, n.sensitive ? '\tsensitive: true' : null, '}'].filter(Boolean);
    if (!text) return { version: 1, addr: n.addr, blocks: [], contributions: [], notes: ['The node is recorded in the state and the specification no longer declares it, so there are no lines to show.'] };
    const lines = text.map((t, i) => ({ no: start + i, text: t, mark: n.sensitive && i === 1 ? 'sensitive' : 'focus' }));
    return { version: 1, addr: n.addr, blocks: [{ role: 'definition', label: n.addr, file: f, kind: 'file', start_line: start, end_line: start + lines.length - 1, modified: new Date(Date.now() - 3 * 86400e3).toISOString().replace(/\.\d{3}Z$/, 'Z'), lines }],
      contributions: [{ kind: 'file', where: `${f}:${start}-${start + lines.length - 1}`, note: 'writes the output' }, { kind: 'builtin', where: 'pravo.dev/pravo', note: 'the built-in package gives pravo.#Output' }] };
  }
  const t = TPL[n.type] || GENERIC;
  const f = n.source ? n.source.file : 'main.cue';
  const start = n.source ? n.source.line : 10;
  const ctx = { h: hash(n.addr) };
  if (n.presence === 'state') {
    return { version: 1, addr: n.addr, blocks: [], contributions: [{ kind: 'generated', where: 'cue.mod/gen/pravo.dev/providers', note: 'schema of ' + n.type }], notes: ['This node is only in the state. Nothing in the specification declares it, so there are no lines to show.'] };
  }
  const lines = [];
  let no = start;
  lines.push({ no: no++, text: `${n.name}: $${n.provider.split('.')[0]}.#Resource.${n.type} & {`, mark: 'focus' });
  const first = t.find((x) => x[3] !== undefined);
  for (const [name, type, kind, v] of t) {
    if (kind === 'computed') continue;
    const val = fill(v, n, ctx);
    const text = typeof val === 'string' ? JSON.stringify(val) : String(val);
    const changed = n.action === 'update' && first && name === first[0];
    lines.push({ no: no++, text: `\t${name}: ${text}`, mark: changed ? 'changed' : 'focus' });
  }
  for (let i = 0; i < n.sensitive; i++) lines.push({ no: no++, text: `\t${['password', 'secret_string', 'token'][i % 3]}: "\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022"`, mark: 'sensitive' });
  lines.push({ no: no++, text: '}', mark: 'focus' });
  const ctxBefore = [{ no: start - 2, text: '', dim: true }, { no: start - 1, text: `// ${n.namespace || 'root'}`, dim: true }];
  const blocks = [{ role: 'definition', label: n.name, file: f, kind: 'file', start_line: start - 2, end_line: no - 1, modified: new Date(Date.now() - 3 * 86400e3).toISOString().replace(/\.\d{3}Z$/, 'Z'), lines: [...ctxBefore, ...lines] }];
  const ns = graph.namespaces.find((m) => m.path === n.namespace);
  if (ns && ns.declared) {
    const d = ns.declared;
    blocks.push({ role: 'instance', label: ns.name, file: d.file, kind: 'file', start_line: d.line, end_line: d.line + 3, modified: new Date(Date.now() - 3 * 86400e3).toISOString().replace(/\.\d{3}Z$/, 'Z'), lines: [
      { no: d.line, text: `${ns.name}: svc.#Service & {`, mark: 'focus' }, { no: d.line + 1, text: '\tname: "estate"', mark: 'focus' }, { no: d.line + 2, text: '\tsize: "m6g.large"', mark: 'focus' }, { no: d.line + 3, text: '}', mark: 'focus' }] });
  }
  return { version: 1, addr: n.addr, blocks, contributions: [
    { kind: 'file', where: `${f}:${start}`, note: 'defines the resource' },
    { kind: 'generated', where: 'cue.mod/gen/pravo.dev/providers', note: 'schema and defaults of ' + n.type },
    { kind: 'builtin', where: 'pravo.dev/pravo', note: '#Resource and $provider' }], generated: 1 };
}

// ---- the shim ----

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const json = (status, body) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const realFetch = window.fetch.bind(window);
let mutations = 0;

window.fetch = async (input, init) => {
  const url = new URL(typeof input === 'string' ? input : input.url, location.href);
  if (!url.pathname.startsWith('/api/')) return realFetch(input, init);
  if (latency) await sleep(latency);
  if (params.get('auth') === '401') return json(401, { error: 'unauthorized' });
  if (!graph) return json(500, { error: 'no fixture for ' + NAME });
  const p = url.pathname;
  if (p === '/api/ping') return json(200, { ok: true, version: 1 });
  if (p === '/api/graph') return json(200, graph);
  if (p === '/api/access') return access ? json(200, Object.assign({}, access, { revision: graph.revision })) : json(404, { error: 'no such endpoint' });
  if (p === '/api/discovery') return discovery ? json(200, shift(Object.assign({}, discovery, { revision: graph.revision }), by)) : json(404, { error: 'no such endpoint' });
  if (p === '/api/policies') return policies ? json(200, shift(Object.assign({}, policies, { revision: graph.revision }), by)) : json(404, { error: 'no such endpoint' });
  if (p === '/api/adopt') {
    const a = adoptOf(url.searchParams.get('discovery') || '', url.searchParams.get('key') || '');
    return a ? json(200, a) : json(404, { error: 'no such object in the last run of that discovery' });
  }
  if (p === '/api/traces') return json(200, Object.assign({}, traces, { traces: traces.traces.map((x) => Object.assign({}, x, { start: runStart(x.start, x.live ? 0 : by) })) }));
  if (p === '/api/trace') {
    const id = url.searchParams.get('id') || '';
    if (!/^[0-9a-f]{32}-[0-9a-f]{16}$/.test(id)) return json(400, { error: 'id is not the id of a run' });
    const row = traces.traces.find((x) => x.id === id);
    const doc = row ? await fixture(`trace.${(row.from || id).slice(0, 8)}.json`) : null;
    if (!doc) return json(404, { error: 'no such run' });
    return json(200, Object.assign({}, doc, row, { start: runStart(row.start, row.live ? 0 : by) }));
  }
  if (p === '/api/plans') return json(200, Object.assign({}, stored, { revision: graph.revision }));
  if (p === '/api/plan') {
    const id = url.searchParams.get('id') || '';
    if (!/^plan-[0-9a-f]{6,64}$/.test(id)) return json(400, { error: 'id is required: the id of a stored plan, such as plan-2f9c1a4e' });
    const o = stored.plans.some((x) => x.id === id) ? await overlayOf(id) : null;
    if (!o) return json(404, { error: 'no such stored plan' });
    // a viewer without a key lays the plan over the graph from its summary, as the server does, and says so
    const sealed = access && access.you && access.you.state !== 'reader' && access.state !== 'off';
    return json(200, Object.assign({}, o, { revision: graph.revision }, sealed ? { warnings: (o.warnings || []).concat('The values of this plan are sealed.') } : {}));
  }
  if (p === '/api/detail' || p === '/api/source') {
    const addr = url.searchParams.get('addr') || '';
    let n = graph.nodes.find((x) => x.addr === addr);
    if (!n) return json(404, { error: 'unknown address ' + addr });
    const kind = p === '/api/detail' ? 'detail' : 'source';
    if (params.get('fail') === kind) return json(500, { error: `could not read the record of ${addr}` });
    // plan=ID answers for a stored plan, plan=none for no plan: the action of the node is that plan's
    const plan = url.searchParams.get('plan');
    if (plan) {
      const o = plan === 'none' ? null : await overlayOf(plan);
      if (plan !== 'none' && !o) return json(404, { error: 'no such stored plan' });
      const a = o && o.nodes.find((x) => x.addr === addr);
      n = Object.assign({}, n);
      if (a) n.action = a.action; else delete n.action;
      if (kind === 'detail') {
        // a change that a partly stale plan cannot apply says so, as the server's note does
        const d = synthDetail(n);
        const cf = ((stored.plans.find((x) => x.id === plan) || {}).conflicts || []).find((c) => c.addr === addr);
        if (cf) d.notes = (d.notes || []).concat('This change conflicts with the state and is left out of the part of the plan that can be applied: ' + cf.reasons.join('; ') + '.');
        return json(200, d);
      }
      return json(200, synthSource(n));
    }
    const files = kind === 'detail' ? detailFiles : sourceFiles;
    if (files[addr] && mutations === 0 && !n.sealed) { const v = await fixture(files[addr]); if (v) return json(200, shift(v, by)); }
    return json(200, kind === 'detail' ? synthDetail(n) : synthSource(n));
  }
  if (p === '/api/fields') {
    // the same rules as the server: only what the detail sends, a path that names a field or what lies below it, * for any run
    const spec = (url.searchParams.get('q') || '').toLowerCase().trim();
    const eq = spec.indexOf('=');
    const path = (eq < 0 ? spec : spec.slice(0, eq)).trim(), value = eq < 0 ? null : spec.slice(eq + 1);
    const glob = (pat, s) => new RegExp('^' + pat.split('*').map((x) => x.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('.*') + '$').test(s);
    const bare = !/[.*]/.test(path);
    const pathOk = (pp) => { for (;;) { if (bare ? pp === path || pp.endsWith('.' + path) : glob(path, pp)) return true; const i = pp.lastIndexOf('.'); if (i < 0) return false; pp = pp.slice(0, i); } };
    const rows = (a, pp, out) => {
      if (a.redacted || a.sensitive || a.unknown || a.null) return out;
      if ((a.children || []).length) { for (const c of a.children) rows(c, pp + '.' + c.name, out); return out; }
      if (a.value !== undefined && a.value !== null && typeof a.value !== 'object') out.push([pp.toLowerCase(), String(a.value).toLowerCase()]);
      return out;
    };
    const addrs = [];
    if (path) {
      for (const n of graph.nodes) {
        if (n.sealed) continue;
        // the detail that the page would get: a fixture where there is one
        let d = detailFiles[n.addr] && mutations === 0 && !n.sealed ? await fixture(detailFiles[n.addr]) : null;
        if (!d) d = n.mode === 'output' ? synthOutput(n) : synthDetail(n);
        if (!d.schema_known) continue;
        const all = (d.attrs || []).flatMap((a) => rows(a, a.name, []));
        if (all.some(([pp, v]) => pathOk(pp) && (value === null || glob(value, v)))) addrs.push(n.addr);
      }
    }
    return json(200, { version: 1, addrs, indexed: graph.nodes.length, withheld: 0 });
  }
  if (p === '/api/search') {
    const q = (url.searchParams.get('q') || '').toLowerCase();
    return json(200, { version: 1, nodes: graph.nodes.filter((n) => n.addr.includes(q)).slice(0, 50), namespaces: graph.namespaces.filter((m) => m.path.includes(q)) });
  }
  return json(404, { error: 'unknown endpoint' });
};

// EventSource: a "change" event on demand
const sources = new Set();
class MockEventSource {
  constructor() {
    this.readyState = 0;
    this.l = {};
    sources.add(this);
    setTimeout(() => { if (params.get('auth') === '401') { this.readyState = 2; this.fire('error', {}); return; } this.readyState = 1; this.fire('open', {}); }, 30);
  }
  addEventListener(t, f) { (this.l[t] = this.l[t] || []).push(f); }
  close() { this.readyState = 2; sources.delete(this); }
  fire(t, ev) { for (const f of this.l[t] || []) f(ev); }
}
window.EventSource = MockEventSource;

// canApply counts the plans on a node that can apply their change there: a partly stale plan that lists the node
// because the change conflicts cannot, and is no overlap
const canApply = (n) => (n.plans || []).filter((id) => {
  const p = stored.plans.find((x) => x.id === id);
  const t = p && p.touches.find((x) => x.addr === n.addr);
  return !(t && t.conflict);
}).length;

window.__mock = {
  name: NAME,
  emit(data) { for (const s of sources) s.fire('change', { data: JSON.stringify(data || { revision: graph.revision, what: ['state'] }) }); },
  // mutate changes a few nodes and sends the event, as a state change would
  mutate() {
    mutations++;
    const g = JSON.parse(JSON.stringify(graph));
    const pick = g.nodes.filter((n) => !n.action && n.mode !== 'output').slice(0, 3);
    for (const n of pick) { n.revision = (n.revision || 0) + 1; n.action = 'update'; n.refreshed_at = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z'); }
    const first = g.nodes[0];
    if (first && first.mode === 'managed') first.drifted = true;
    g.revision = 'r-live-' + mutations;
    g.project.loaded_at = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z');
    graph = g;
    this.emit({ revision: g.revision, what: ['state'] });
    return pick.map((n) => n.addr);
  },
  // storePlan stores another plan, by a third author, that changes api.db too, and sends the event for it: the
  // page shows it without a reload
  storePlan() {
    const g = JSON.parse(JSON.stringify(graph));
    const id = 'plan-d4b7e913';
    const now = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z');
    const touches = [{ addr: 'api.db', action: 'update' }, { addr: 'api.worker.queue', action: 'update' }];
    // a listing row has no reason, command or git revision: the overlay of the plan has them
    const plan = { id, status: 'pending', open: true, author: 'dee@acme', created: now, expires: new Date(Date.now() + 7 * 86400e3).toISOString().replace(/\.\d{3}Z$/, 'Z'),
      summary: { add: 0, change: 2, destroy: 0, read: 0 }, planned: 2, touches, apply: 'pravo apply ' + id };
    const others = [];
    for (const o of stored.plans) {
      if (!o.open) continue;
      // only the changes that a plan has left, and can apply, count, on both sides
      const addrs = o.touches.filter((t) => !t.applied && !t.conflict && touches.some((x) => x.addr === t.addr)).map((t) => t.addr).sort();
      if (!addrs.length) continue;
      (o.overlaps = o.overlaps || []).push({ id, count: addrs.length, addrs });
      others.push({ id: o.id, count: addrs.length, addrs });
    }
    if (others.length) plan.overlaps = others;
    stored = Object.assign({}, stored, { plans: [plan].concat(stored.plans) });
    for (const t of touches) { const n = g.nodes.find((x) => x.addr === t.addr); n.plans = [id].concat(n.plans || []); if (canApply(n) > 1) n.overlap = true; }
    overlays.set(id, { version: 1, revision: '', plan: { file: id, id, status: 'pending', created: now, actor: plan.author, summary: { add: 0, change: 2, destroy: 0, read: 0, blast_radius: 6 }, planned: 2, total: g.nodes.filter((n) => n.mode !== 'output').length }, nodes: touches,
      reason: 'resize the queue', command: 'pravo plan --store --reason "resize the queue"', git_revision: 'e5a1b08' });
    g.revision = 'r-plans-' + (++mutations);
    graph = g;
    this.emit({ revision: g.revision, what: ['plans'] });
    return id;
  },
  // record adds a run to the Traces view, as a command that did some work would, and sends the event for it
  record() {
    const base = traces.traces.find((x) => x.command === 'pravo plan') || traces.traces[0];
    const id = 'c0ffee00' + Math.random().toString(16).slice(2, 26).padEnd(24, '0') + '-' + Math.random().toString(16).slice(2, 18).padEnd(16, '0');
    traces = Object.assign({}, traces, { traces: [Object.assign({}, base, { id, from: base.id, live: true, start: new Date().toISOString() })].concat(traces.traces) });
    this.emit({ revision: graph.revision, what: ['traces'] });
    return id;
  },
  // collectPlan removes a stored plan, as a collection does
  collectPlan(id) {
    const g = JSON.parse(JSON.stringify(graph));
    stored = Object.assign({}, stored, { plans: stored.plans.filter((p) => p.id !== id) });
    for (const p of stored.plans) if (p.overlaps) { p.overlaps = p.overlaps.filter((o) => o.id !== id); if (!p.overlaps.length) delete p.overlaps; }
    for (const n of g.nodes) if (n.plans) { n.plans = n.plans.filter((x) => x !== id); if (!n.plans.length) delete n.plans; if (canApply(n) < 2) delete n.overlap; }
    overlays.delete(id);
    g.revision = 'r-plans-' + (++mutations);
    graph = g;
    this.emit({ revision: g.revision, what: ['plans'] });
  },
  // setAccess swaps the Access document for another fixture, as an edit of the readers file or a rewrap would, and sends the event
  async setAccess(name) {
    const a = await fixture(`access.${name}.json`);
    if (!a) return false;
    access = a;
    const g = JSON.parse(JSON.stringify(graph));
    g.revision = 'r-access-' + (++mutations);
    graph = g;
    this.emit({ revision: g.revision, what: ['access'] });
    return true;
  },
  // setDiscovery swaps the Discovery document for another fixture, as a run of "pravo discover" would, and sends the event
  async setDiscovery(name) {
    const d = await fixture(`discovery.${name}.json`);
    if (!d) return false;
    discovery = d;
    const g = JSON.parse(JSON.stringify(graph));
    g.revision = 'r-discovery-' + (++mutations);
    graph = g;
    this.emit({ revision: g.revision, what: ['discovery'] });
    return true;
  },
  drop() { for (const s of sources) { s.readyState = 0; s.fire('error', {}); } },
  restore() { for (const s of sources) { s.readyState = 1; s.fire('open', {}); } },
};
