// Development only: a fetch shim that answers /api/* from the fixtures, so that the interface can be built and
// checked without the Go server. The page loads it only when its URL has mock=NAME before the fragment:
//   /index.html?mock=platform            graph.platform.json (also: fleet, big, empty, specerror)
//   &auth=401                             every /api request answers 401
//   &fail=detail                          /api/detail answers 500 {"error": ...}
//   &latency=300                          every answer is delayed by that many milliseconds
//   &stale=1                              the saved plan is stale, with reasons
// window.__mock.mutate() changes the graph and sends a "change" event; window.__errors collects the
// errors and warnings of the page, for a headless run.

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
const detailFiles = NAME === 'platform' ? { 'api.db': 'detail.db.json', 'api.db_password': 'detail.password.json', 'edge.lb': 'detail.drifted.json' } : {};
const sourceFiles = NAME === 'platform' ? { 'api.db': 'source.db.json', 'api.db_password': 'source.password.json' } : {};

// ---- synthesised answers ----

const hash = (s) => { let h = 2166136261; for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); } return h >>> 0; };
const hex = (s, n) => { let o = ''; let h = hash(s); while (o.length < n) { o += h.toString(16); h = Math.imul(h ^ (h >>> 13), 0x5bd1e995) >>> 0; } return o.slice(0, n); };

// type -> [name, type, kind, value] ; strings may use {name} {mod} {id}
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
  random_id: [['byte_length', 'number', 'required', 4], ['hex', 'string', 'computed', '{id}']],
  local_file: [['content', 'string', 'optional', 'release 42\n'], ['filename', 'string', 'required', './release.txt'], ['file_permission', 'string', 'optional', '0644']],
};
const GENERIC = [['name', 'string', 'optional,computed', '{name}']];

const fill = (v, n, ctx) => (typeof v !== 'string' ? v : v.replace(/\{name\}/g, n.name).replace(/\{mod\}/g, n.module).replace(/\{id\}/g, hex(n.addr, 17))
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
    attrs.push({ name: 'tags', type: 'map(string)', kind: 'optional', children: [{ name: 'Environment', type: 'string', value: 'production' }, { name: 'ManagedBy', type: 'string', value: 'pravo' }, { name: 'Module', type: 'string', value: n.module || 'root' }] });
  }
  attrs.sort((a, b) => (a.name < b.name ? -1 : 1));
  return attrs;
}

function synthDetail(n) {
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
  const d = { version: 1, node: { ...n, deps: undefined, used: undefined, mod: undefined }, provider: provRef(n), attrs, schema_known: n.type !== 'aws_wafv2_web_acl', depends_on: deps, used_by: used, times };
  if (!d.schema_known) d.attrs = attrs.map((a) => ({ name: a.name, type: a.type, kind: a.kind, redacted: 'schema' }));
  if (seen.size) d.reach = [...seen].sort();
  if (notes.length) d.notes = notes;
  if (n.locked) d.lock = { kind: 'lock', user: 'ci', host: 'runner-07', command: 'pravo apply release-42.plan', actor: 'ci@acme', since: new Date(Date.now() - 6 * 60e3).toISOString().replace(/\.\d{3}Z$/, 'Z') };
  // history from the revision
  const hist = [];
  const kinds = ['apply', 'apply', 'edit', 'apply', 'import'];
  for (let i = 0; i < Math.min(n.revision || 0, 5); i++) {
    const rev = (n.revision || 0) - i;
    hist.push({ id: `20261${(10 - i % 9)}${String(10 + i).padStart(2, '0')}T0${i}1200Z-${hex(n.addr + i, 6)}`, kind: i === (n.revision - 1) ? 'import' : kinds[i % kinds.length], status: i === 3 ? 'failed' : 'applied', time: new Date(Date.now() - (i * 5 + 2) * 86400e3).toISOString().replace(/\.\d{3}Z$/, 'Z'), user: i % 2 ? 'grace' : 'ada', command: i === 3 ? 'pravo apply release.plan' : 'pravo apply', action: i === (n.revision - 1) ? 'import' : 'update', rev_before: rev - 1, rev_after: rev });
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
  const ctxBefore = [{ no: start - 2, text: '', dim: true }, { no: start - 1, text: `// ${n.module || 'root'}`, dim: true }];
  const blocks = [{ role: 'definition', label: n.name, file: f, kind: 'file', start_line: start - 2, end_line: no - 1, modified: new Date(Date.now() - 3 * 86400e3).toISOString().replace(/\.\d{3}Z$/, 'Z'), lines: [...ctxBefore, ...lines] }];
  const mod = graph.modules.find((m) => m.path === n.module);
  if (mod && mod.declared) {
    const d = mod.declared;
    blocks.push({ role: 'instance', label: mod.name, file: d.file, kind: 'file', start_line: d.line, end_line: d.line + 3, modified: new Date(Date.now() - 3 * 86400e3).toISOString().replace(/\.\d{3}Z$/, 'Z'), lines: [
      { no: d.line, text: `${mod.name}: svc.#Service & {`, mark: 'focus' }, { no: d.line + 1, text: '\tname: "estate"', mark: 'focus' }, { no: d.line + 2, text: '\tsize: "m6g.large"', mark: 'focus' }, { no: d.line + 3, text: '}', mark: 'focus' }] });
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
  if (p === '/api/detail' || p === '/api/source') {
    const addr = url.searchParams.get('addr') || '';
    const n = graph.nodes.find((x) => x.addr === addr);
    if (!n) return json(404, { error: 'unknown address ' + addr });
    const kind = p === '/api/detail' ? 'detail' : 'source';
    if (params.get('fail') === kind) return json(500, { error: `could not read the record of ${addr}` });
    const files = kind === 'detail' ? detailFiles : sourceFiles;
    if (files[addr] && mutations === 0) { const v = await fixture(files[addr]); if (v) return json(200, shift(v, by)); }
    return json(200, kind === 'detail' ? synthDetail(n) : synthSource(n));
  }
  if (p === '/api/search') {
    const q = (url.searchParams.get('q') || '').toLowerCase();
    return json(200, { version: 1, nodes: graph.nodes.filter((n) => n.addr.includes(q)).slice(0, 50), modules: graph.modules.filter((m) => m.path.includes(q)) });
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

window.__mock = {
  name: NAME,
  emit(data) { for (const s of sources) s.fire('change', { data: JSON.stringify(data || { revision: graph.revision, what: ['state'] }) }); },
  // mutate changes a few nodes and sends the event, as a state change would
  mutate() {
    mutations++;
    const g = JSON.parse(JSON.stringify(graph));
    const pick = g.nodes.filter((n) => !n.action).slice(0, 3);
    for (const n of pick) { n.revision = (n.revision || 0) + 1; n.action = 'update'; n.refreshed_at = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z'); }
    const first = g.nodes[0];
    if (first && first.mode === 'managed') first.drifted = true;
    g.revision = 'r-live-' + mutations;
    g.project.loaded_at = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z');
    graph = g;
    this.emit({ revision: g.revision, what: ['state'] });
    return pick.map((n) => n.addr);
  },
  drop() { for (const s of sources) { s.readyState = 0; s.fire('error', {}); } },
  restore() { for (const s of sources) { s.readyState = 1; s.fire('open', {}); } },
};
