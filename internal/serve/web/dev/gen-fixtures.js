'use strict';
// Generates fixtures/graph.platform.json, graph.fleet.json, graph.empty.json, graph.specerror.json and
// the detail and source answers for the platform. Run: node gen-fixtures.js
// (graph.big.json comes from gen-big.js.) check-fixtures.js verifies the output against api.go.

const fs = require('fs');
const path = require('path');
const { MIN, H, D, iso, prune, Builder, planOf, resource } = require('./gen-lib');

const out = (name, v) => fs.writeFileSync(path.join(__dirname, 'fixtures', name), JSON.stringify(v, null, 1) + '\n');
const src = (file, line, end) => ({ file, line, col: 2, end_line: end });

const PROJECT_DIR = '/srv/infra/estate';
const AWS = { local: 'aws', source: 'registry.opentofu.org/hashicorp/aws', version: '5.61.0' };
const RANDOM = { local: 'random', source: 'registry.opentofu.org/hashicorp/random', version: '3.6.2' };
const LOCAL = { local: 'local', source: 'registry.opentofu.org/hashicorp/local', version: '2.5.1' };

// ---------------------------------------------------------------------------------------------------------
// platform: about 45 nodes in three top-level namespaces with nested namespaces, a plan, drift, a gone node, a
// spec-only node, an orphan and a locked node.

function platform() {
  const b = new Builder();
  const N = 'modules/network/main.cue', A = 'modules/api/main.cue', E = 'modules/edge/main.cue';

  b.add('build_id', 'random_id', { provider: 'random', source: src('main.cue', 6, 9), revision: 3, refreshed: 9 * H });
  b.add('account', 'aws_caller_identity', { mode: 'data', action: 'read', source: src('main.cue', 11, 11), revision: 2, refreshed: 4 * MIN });
  b.add('release_marker', 'local_file', { provider: 'local', action: 'create', source: src('main.cue', 13, 17), presence: 'both', revision: 0 });

  // network
  b.ref('network', src('main.cue', 20, 24), src(N, 1, 60));
  b.ref('network.public', src(N, 30, 36), src(N, 62, 90));
  b.ref('network.private', src(N, 38, 44), src(N, 62, 90));
  b.add('network.vpc', 'aws_vpc', { action: 'no-op', source: src(N, 4, 10), revision: 4, refreshed: 6 * MIN });
  b.add('network.igw', 'aws_internet_gateway', { source: src(N, 12, 14), revision: 2 });
  for (const n of ['lb', 'app', 'db']) b.add('network.sg_' + n, 'aws_security_group', { source: src(N, 16 + ['lb', 'app', 'db'].indexOf(n) * 4, 19 + ['lb', 'app', 'db'].indexOf(n) * 4), revision: 3 });
  for (const s of ['public', 'private']) {
    const o = s === 'public' ? 64 : 76;
    b.add(`network.${s}.subnet_a`, 'aws_subnet', { source: src(N, o, o + 4), revision: 2 });
    b.add(`network.${s}.subnet_b`, 'aws_subnet', { source: src(N, o + 5, o + 9), revision: 2 });
    b.add(`network.${s}.rt`, 'aws_route_table', { source: src(N, o + 10, o + 12), revision: 5 });
    b.add(`network.${s}.rta_a`, 'aws_route_table_association', { source: src(N, o + 13, o + 15), revision: 1 });
    b.add(`network.${s}.rta_b`, 'aws_route_table_association', { source: src(N, o + 16, o + 18), revision: 1 });
  }
  b.add('network.private.nat_eip', 'aws_eip', { source: src(N, 94, 96), revision: 1 });
  b.add('network.private.nat', 'aws_nat_gateway', { source: src(N, 97, 101), revision: 2 });

  // api
  b.ref('api', src('main.cue', 38, 41), src(A, 3, 140));
  b.ref('api.worker', src(A, 120, 126), src(A, 142, 190));
  b.add('api.db_pass', 'random_password', { provider: 'random', sensitive: 2, source: src(A, 8, 12), revision: 6, refreshed: 3 * D });
  b.add('api.db_subnets', 'aws_db_subnet_group', { source: src(A, 14, 17), revision: 2 });
  b.add('api.db', 'aws_db_instance', { action: 'update', sensitive: 2, source: src(A, 19, 33), revision: 14, refreshed: 2 * H });
  b.add('api.db_password', 'aws_ssm_parameter', { sensitive: 4, source: src(A, 35, 43), revision: 3, refreshed: 5 * H });
  b.add('api.secret', 'aws_secretsmanager_secret', { action: 'no-op', source: src(A, 45, 48), revision: 2, refreshed: 7 * MIN });
  b.add('api.secret_version', 'aws_secretsmanager_secret_version', { sensitive: 1, source: src(A, 50, 54), revision: 9, refreshed: 2 * H });
  b.add('api.cluster', 'aws_ecs_cluster', { source: src(A, 56, 58), revision: 1 });
  b.add('api.role', 'aws_iam_role', { source: src(A, 60, 66), revision: 2 });
  b.add('api.policy', 'aws_iam_role_policy', { source: src(A, 68, 74), revision: 5 });
  b.add('api.logs', 'aws_cloudwatch_log_group', { source: src(A, 76, 79), revision: 1 });
  b.add('api.task', 'aws_ecs_task_definition', { action: 'update', sensitive: 1, source: src(A, 81, 98), revision: 21, refreshed: 2 * H });
  b.add('api.service', 'aws_ecs_service', { action: 'update', source: src(A, 100, 112), revision: 17, refreshed: 2 * H });
  b.add('api.scaling', 'aws_appautoscaling_target', { source: src(A, 114, 118), revision: 4 });
  b.add('api.cache', 'aws_elasticache_cluster', { presence: 'spec', action: 'create', source: src(A, 128, 134), refreshed: null, revision: 0 });
  b.add('api.worker.queue', 'aws_sqs_queue', { action: 'no-op', source: src(A, 144, 148), revision: 3, refreshed: 9 * MIN });
  b.add('api.worker.dlq', 'aws_sqs_queue', { action: 'create', source: src(A, 150, 153), presence: 'both', refreshed: null, revision: 0 });
  b.add('api.worker.task', 'aws_ecs_task_definition', { action: 'replace', source: src(A, 155, 170), revision: 8, refreshed: 2 * H });
  b.add('api.worker.service', 'aws_ecs_service', { action: 'update', source: src(A, 172, 186), revision: 8, refreshed: 2 * H });

  // edge
  b.ref('edge', src('main.cue', 28, 33), src(E, 1, 80));
  b.add('edge.zone', 'aws_route53_zone', { mode: 'data', action: 'read', source: src(E, 4, 6), revision: 1, refreshed: 3 * MIN });
  b.add('edge.cert', 'aws_acm_certificate', { gone: true, drifted: true, action: 'create', source: src(E, 8, 14), revision: 3, refreshed: 40 * MIN });
  b.add('edge.lb', 'aws_lb', { drifted: true, action: 'update', source: src(E, 16, 26), revision: 6, refreshed: 40 * MIN });
  b.add('edge.waf', 'aws_wafv2_web_acl', { locked: true, source: src(E, 28, 46), revision: 12, refreshed: 1 * D });
  b.add('edge.tg', 'aws_lb_target_group', { action: 'no-op', source: src(E, 48, 54), revision: 4, refreshed: 8 * MIN });
  b.add('edge.listener', 'aws_lb_listener', { source: src(E, 56, 62), revision: 3 });
  b.add('edge.assoc', 'aws_wafv2_web_acl_association', { source: src(E, 64, 67), revision: 1 });
  b.add('edge.dns', 'aws_route53_record', { action: 'create', source: src(E, 69, 76), refreshed: null, revision: 0 });
  b.add('edge.old_lb', 'aws_lb', { presence: 'state', action: 'delete', revision: 2, refreshed: 6 * D });

  // the secrets of the sealed state that the specification reads (secrets.platform.json says the same of them): a node
  // that is never recorded, and the number of the version of a secret, which is an ordinary value
  b.transient('api.db_secret', 'secret', { source: src(A, 200, 202) });
  b.transient('api.db_secret_version', 'secret_version', { mode: 'builtin', source: src(A, 204, 206) });
  b.transient('api.api_token', 'secret', { source: src(A, 208, 211) });
  b.transient('edge.origin_secret', 'secret', { source: src(E, 82, 84) });
  b.transient('deploy_key', 'secret', { source: src('main.cue', 88, 90) });

  // edges: dependency -> dependent
  b.dep('network.vpc', 'network.igw');
  for (const n of ['lb', 'app', 'db']) b.dep('network.vpc', 'network.sg_' + n);
  b.dep('network.sg_lb', 'network.sg_app');
  b.dep('network.sg_app', 'network.sg_db');
  for (const s of ['public', 'private']) {
    b.deps(`network.${s}.subnet_a`, 'network.vpc');
    b.deps(`network.${s}.subnet_b`, 'network.vpc');
    b.deps(`network.${s}.rt`, 'network.vpc');
    b.deps(`network.${s}.rta_a`, `network.${s}.subnet_a`, `network.${s}.rt`);
    b.deps(`network.${s}.rta_b`, `network.${s}.subnet_b`, `network.${s}.rt`);
  }
  b.dep('network.igw', 'network.public.rt');
  b.deps('network.private.nat', 'network.private.nat_eip', 'network.public.subnet_a');
  b.dep('network.private.nat', 'network.private.rt');

  b.deps('api.db_subnets', 'network.private.subnet_a', 'network.private.subnet_b');
  b.deps('api.db', 'api.db_pass', 'api.db_subnets', 'network.sg_db');
  b.deps('api.db_password', 'api.db_pass');
  b.deps('api.secret_version', 'api.secret', 'api.db');
  b.deps('api.policy', 'api.role', 'api.secret');
  b.deps('api.task', 'api.role', 'api.logs', 'api.db', 'api.secret_version');
  b.deps('api.service', 'api.cluster', 'api.task', 'edge.tg', 'network.private.subnet_a', 'network.private.subnet_b', 'network.sg_app');
  b.deps('api.scaling', 'api.service', 'api.cluster');
  b.deps('api.cache', 'network.sg_app', 'network.private.subnet_a', 'network.private.subnet_b');
  b.deps('api.worker.task', 'api.role', 'api.worker.queue', 'api.worker.dlq');
  b.deps('api.worker.dlq', 'api.logs');
  b.deps('api.worker.service', 'api.cluster', 'api.worker.task', 'network.private.subnet_a');

  b.deps('edge.cert', 'edge.zone');
  b.deps('edge.lb', 'network.sg_lb', 'network.public.subnet_a', 'network.public.subnet_b');
  b.deps('edge.tg', 'network.vpc');
  b.deps('edge.listener', 'edge.lb', 'edge.tg', 'edge.cert');
  b.deps('edge.assoc', 'edge.lb', 'edge.waf');
  b.deps('edge.dns', 'edge.zone', 'edge.lb');
  b.deps('release_marker', 'build_id', 'api.service', 'deploy_key');
  b.deps('edge.old_lb', 'network.sg_lb');
  // a secret reaches the write-only attributes of resources and nothing that is recorded; the number of its version
  // is what makes the provider take a new one
  b.deps('api.secret_version', 'api.db_secret', 'api.db_secret_version');
  b.deps('api.cache', 'api.api_token');
  b.deps('edge.listener', 'edge.origin_secret');

  // the outputs of the root are nodes too, joined to what they read; one reads other outputs, one is not declared any more
  b.output('url', { source: src('main.cue', 58, 61) });
  b.output('db_endpoint', { source: src('main.cue', 63, 66) });
  b.output('db_password', { sensitive: true, source: src('main.cue', 68, 72) });
  b.output('cache_endpoint', { presence: 'spec', source: src('main.cue', 74, 77) });
  b.output('summary', { source: src('main.cue', 79, 86) });
  b.output('legacy_url', { presence: 'state' });
  b.output('network.cidr', { source: src(N, 112, 114) });
  b.output('api.cluster_arn', { source: src(A, 192, 195) });
  b.deps('url', 'edge.dns');
  b.deps('db_endpoint', 'api.db');
  b.deps('db_password', 'api.db_pass');
  b.deps('cache_endpoint', 'api.cache');
  b.deps('summary', 'build_id', 'db_endpoint', 'url');
  b.deps('network.cidr', 'network.vpc');
  b.deps('api.cluster_arn', 'api.cluster');

  const stored = storedPlans(b, 'r-7c41e9b2');
  const plan = planOf(b, {
    file: '.pravo/plans/release-42.plan',
    createdAgo: 14 * MIN,
    actor: 'ci@acme',
    hints: ['edge.old_lb is no longer declared and would be destroyed.', 'api.worker.task is replaced: container_definitions cannot be updated in place.'],
  });
  const g = b.graph({
    revision: 'r-7c41e9b2',
    project: {
      dir: PROJECT_DIR,
      package: 'estate',
      version: '0.9.0-dev',
      state: { location: 's3://acme-state/estate', backend: 's3', lineage: '5c8d7e2a-61b4-4f0e-9a3b-0f6f5d1c2e77', format: 3, records: 46, locks: 1 },
      providers: [AWS, RANDOM, LOCAL],
      loaded_at: iso(0),
    },
    outputs: [
      { name: 'api.cluster_arn', value: 'arn:aws:ecs:eu-west-1:412058817224:cluster/estate-api' },
      { name: 'cache_endpoint', unknown: true },
      { name: 'db_endpoint', value: 'estate-db.c8x2kq1.eu-west-1.rds.amazonaws.com:5432' },
      { name: 'db_password', sensitive: true, redacted: 'sensitive' },
      { name: 'legacy_url', value: 'https://old.estate.example.com' },
      { name: 'network.cidr', value: '10.20.0.0/16' },
      { name: 'summary', value: { endpoint: 'estate-db.c8x2kq1.eu-west-1.rds.amazonaws.com:5432', release: 'a3f9c012', url: 'https://api.estate.example.com' } },
      { name: 'url', value: 'https://api.estate.example.com' },
    ],
    plan,
    warnings: ['The schema of aws_wafv2_web_acl is not cached: its values are withheld. Run pravo providers schema to cache it.'],
  });
  return { b, g, stored };
}

// ---------------------------------------------------------------------------------------------------------
// the plans stored in the state of the platform: two that can be applied, by two authors, and that both change
// api.db; one the state moved on from; one the state moved on from under two of its four changes (partly stale: the
// other two can be applied by name); one applied in part; one applied; one that expired. storedPlans marks the nodes
// (Node.plans, Node.overlap), and returns the list that /api/plans sends and the overlay of each plan that /api/plan sends.

function storedPlans(b, revision) {
  const defs = [
    { id: 'plan-7b04d1c9', status: 'pending', author: 'ben@acme', ago: 40 * MIN, reason: 'rotate the database secret', git: 'c41e07d',
      command: 'pravo plan --store --reason "rotate the database secret"',
      changes: { 'api.db': 'update', 'api.secret_version': 'update', 'edge.lb': 'update', 'edge.dns': 'create' } },
    { id: 'plan-c3e8a052', status: 'stale', author: 'ci@acme', ago: 1 * H + 5 * MIN, reason: 'renew the certificate', git: '3b9d6f0',
      command: 'pravo plan --store --reason "renew the certificate"',
      changes: { 'edge.cert': 'create', 'edge.lb': 'update' },
      why: ['edge.cert changed in state (revision 3, plan expected 2)', 'edge.lb changed in state (revision 6, plan expected 5)'] },
    // api.role moved in the state, so it conflicts, and api.policy has to be applied with it; api.secret and api.logs are live
    { id: 'plan-b86e2d14', status: 'partly-stale', author: 'kim@acme', ago: 2 * H, reason: 'narrow the task role and its policy', git: 'a7d3e19',
      command: 'pravo plan --store --reason "narrow the task role and its policy"',
      changes: { 'api.role': 'update', 'api.policy': 'update', 'api.secret': 'update', 'api.logs': 'update' },
      conflicts: { 'api.policy': ['api.policy must be applied with api.role, which cannot be applied'], 'api.role': ['api.role changed in state (revision 7, plan expected 6)'] },
      live: ['api.logs', 'api.secret'] },
    { id: 'plan-2f9c1a4e', status: 'pending', author: 'ana@acme', ago: 3 * H, reason: 'raise the database to m6g.large', git: '9f3c2ab',
      command: 'pravo plan --store --reason "raise the database to m6g.large"',
      changes: { 'api.db': 'update', 'api.task': 'update', 'api.service': 'update', 'api.worker.service': 'update' } },
    { id: 'plan-5a2c9e71', status: 'partial', author: 'dee@acme', ago: 5 * H, reason: 'retire the nat gateway and its route', git: 'e5a1b08',
      command: 'pravo plan --store --reason "retire the nat gateway and its route"',
      changes: { 'network.private.nat': 'update', 'network.private.rt': 'update' }, done: ['network.private.nat'] },
    { id: 'plan-91d5f6b8', status: 'applied', author: 'grace@acme', ago: 26 * H, reason: 'add the cache and a dead letter queue', git: '71ae2c5',
      command: 'pravo plan --store --reason "add the cache and a dead letter queue"',
      changes: { 'api.cache': 'create', 'api.worker.dlq': 'create' }, done: ['api.cache', 'api.worker.dlq'] },
    { id: 'plan-e0a17c33', status: 'expired', author: 'ada@acme', ago: 9 * D, reason: 'tune the nat gateway',
      command: 'pravo plan --store --reason "tune the nat gateway" --ttl 7d',
      changes: { 'network.private.nat': 'update' } },
  ];
  const open = (d) => d.status === 'pending' || d.status === 'partial' || d.status === 'partly-stale';
  const counts = (acts) => {
    const c = { add: 0, change: 0, destroy: 0, read: 0 };
    for (const a of acts) {
      if (a === 'create') c.add++;
      else if (a === 'update') c.change++;
      else if (a === 'delete') c.destroy++;
      else if (a === 'replace') { c.add++; c.destroy++; }
      else if (a === 'read') c.read++;
    }
    return c;
  };
  // what a plan has left to do: the addresses it has not applied
  const left = (d) => Object.keys(d.changes).filter((a) => !(d.done || []).includes(a)).sort();
  // what a plan can still change: a partly stale plan cannot apply the changes that conflict
  const changing = (d) => left(d).filter((a) => !(d.conflicts || {})[a]);
  // the nodes: the plans that can be applied, newest first, on the nodes that they have a change left on; two that can
  // apply a change there are an overlap, and a change that conflicts is not one
  const livePlans = new Map();
  for (const d of defs.filter(open)) {
    for (const addr of left(d)) {
      const n = b.index.get(addr);
      n.plans = (n.plans || []).concat(d.id);
      if (changing(d).includes(addr)) livePlans.set(addr, (livePlans.get(addr) || 0) + 1);
      n.overlap = livePlans.get(addr) > 1 ? true : undefined;
    }
  }
  const plans = [], overlays = {};
  for (const d of defs) {
    const touches = Object.keys(d.changes).sort().map((addr) => ({ addr, action: d.changes[addr], applied: (d.done || []).includes(addr) ? true : undefined, conflict: (d.conflicts || {})[addr] ? true : undefined }));
    // the other open plans that have a change left on a resource that this one has one left on: symmetric
    const overlaps = !open(d) ? undefined : defs.filter((o) => o !== d && open(o))
      .map((o) => { const shared = changing(o).filter((a) => changing(d).includes(a)); return { id: o.id, count: shared.length, addrs: shared.slice(0, 20) }; }).filter((o) => o.count);
    const summary = counts(touches.map((t) => t.action));
    const applied = (d.done || []).length;
    plans.push(prune({
      id: d.id, status: d.status, open: open(d) ? true : undefined, author: d.author, created: iso(d.ago), expires: iso(d.ago - 7 * D),
      summary, planned: touches.length, applied, reasons: d.why,
      conflicts: d.conflicts ? Object.keys(d.conflicts).sort().map((addr) => ({ addr, reasons: d.conflicts[addr] })) : undefined,
      unapplied: d.conflicts ? left(d).length : undefined,
      apply_only: d.live, apply_live: d.live ? `pravo apply ${d.id} --only ${d.live.join(',')}` : undefined,
      touches, overlaps: overlaps && overlaps.length ? overlaps : undefined, apply: 'pravo apply ' + d.id,
    }));
    const blast = new Set();
    for (const t of touches) if (['create', 'update', 'replace', 'delete'].includes(t.action) && !t.applied) { blast.add(t.addr); for (const r of b.reach(t.addr)) blast.add(r); }
    overlays[d.id] = prune({
      version: 1,
      revision,
      plan: {
        file: d.id, id: d.id, status: d.status, created: iso(d.ago), actor: d.author, stale: d.status === 'stale' ? true : undefined, reasons: d.why,
        summary: Object.assign({}, summary, { blast_radius: blast.size }), planned: touches.length, total: b.nodes.filter(resource).length, applied,
      },
      nodes: touches.map((t) => ({ addr: t.addr, action: t.action, applied: t.applied })),
      warnings: d.status === 'stale' ? ['The plan is stale and can no longer be applied: ' + d.why.join('; ') + '.'] : undefined,
      reason: d.reason, command: d.command, git_revision: d.git,
    });
  }
  return { list: { version: 1, revision, plans }, overlays };
}

// ---------------------------------------------------------------------------------------------------------
// details of the platform

const a = (name, type, kind, value, x) => Object.assign({ name, type, kind, value }, x);
const str = (name, value, kind = 'optional,computed', x) => a(name, 'string', kind, value, x);
const num = (name, value, kind = 'optional,computed', x) => a(name, 'number', kind, value, x);
const bool = (name, value, kind = 'optional,computed', x) => a(name, 'bool', kind, value, x);
const nul = (name, type, kind = 'optional', x) => Object.assign({ name, type, kind, null: true }, x);
const sensitive = (name, type = 'string', kind = 'optional') => ({ name, type, kind, sensitive: true, redacted: 'sensitive' });
const kids = (name, type, kind, children, x) => Object.assign({ name, type, kind, children }, x);
const kv = (obj, type = 'string') => Object.entries(obj).map(([k, v]) => ({ name: k, type, value: v }));
const list = (arr, type = 'string') => arr.map((v, i) => ({ name: String(i), type, value: v }));

function detailOf(b, g, addr, o) {
  const n = g.nodes.find((x) => x.addr === addr);
  const prov = n.provider === 'random' ? RANDOM : n.provider === 'local' ? LOCAL : AWS;
  return prune({
    version: 1,
    node: n,
    provider: { key: n.provider, source: prov.source, version: prov.version, protocol: '6' },
    attrs: o.attrs,
    schema_known: o.schemaKnown !== false,
    depends_on: b.dependsOn(addr),
    used_by: b.usedBy(addr),
    reach: b.reach(addr),
    change: o.change,
    history: o.history,
    lock: o.lock,
    times: o.times,
    notes: o.notes,
  });
}

function detailDB(b, g) {
  const attrs = [
    str('address', 'estate-db.c8x2kq1.eu-west-1.rds.amazonaws.com', 'computed'),
    num('allocated_storage', 100),
    bool('apply_immediately', false, 'optional'),
    str('arn', 'arn:aws:rds:eu-west-1:412058817224:db:estate-db', 'computed'),
    bool('auto_minor_version_upgrade', true, 'optional'),
    str('availability_zone', 'eu-west-1a'),
    num('backup_retention_period', 7),
    str('ca_cert_identifier', 'rds-ca-rsa2048-g1'),
    str('db_name', 'estate'),
    str('db_subnet_group_name', 'estate-db-subnets'),
    bool('deletion_protection', true, 'optional'),
    kids('enabled_cloudwatch_logs_exports', 'set(string)', 'optional', list(['postgresql', 'upgrade'])),
    str('endpoint', 'estate-db.c8x2kq1.eu-west-1.rds.amazonaws.com:5432', 'computed'),
    str('engine', 'postgres'),
    str('engine_version', '16.3'),
    str('identifier', 'estate-db'),
    str('instance_class', 'db.t3.medium', 'required'),
    num('iops', 3000),
    str('kms_key_id', 'arn:aws:kms:eu-west-1:412058817224:key/6f1c0b7e-2d3a-4b9f-8c11-7a52e0d9a4c3'),
    str('maintenance_window', 'sun:03:00-sun:04:00'),
    kids('master_user_secret', 'list(object)', 'computed', [
      kids('0', 'object', undefined, [str('kms_key_id', 'alias/aws/secretsmanager', undefined), str('secret_arn', 'arn:aws:secretsmanager:eu-west-1:412058817224:secret:rds!db-6f1c-AbCdEf', undefined), str('secret_status', 'active', undefined)]),
    ]),
    bool('multi_az', false),
    sensitive('password'),
    sensitive('password_wo', 'string', 'optional'),
    bool('performance_insights_enabled', false, 'optional'),
    num('port', 5432),
    bool('publicly_accessible', false),
    nul('restore_to_point_in_time', 'list(object)', 'optional'),
    bool('skip_final_snapshot', false, 'optional'),
    bool('storage_encrypted', true),
    str('storage_type', 'gp3'),
    kids('tags', 'map(string)', 'optional', kv({ Environment: 'production', ManagedBy: 'pravo', Team: 'platform', Version: '41' })),
    nul('timeouts', 'object', 'optional'),
    str('username', 'estate_admin'),
    kids('vpc_security_group_ids', 'set(string)', 'optional,computed', list(['sg-0a1b2c3d4e5f60718', 'sg-0f1e2d3c4b5a69788']), { truncated: 6 }),
  ];
  const change = {
    action: 'update',
    reasons: ['instance_class changed in main.cue:40: size "t3.medium" is now "m6g.large"', 'allocated_storage changed in modules/api/db.cue:20'],
    attrs: [
      { path: 'allocated_storage', before: 100, after: 150 },
      { path: 'enabled_cloudwatch_logs_exports.2', after: 'iam-db-auth-error' },
      { path: 'instance_class', before: 'db.t3.medium', after: 'db.m6g.large' },
      { path: 'maintenance_window', before: 'sun:03:00-sun:04:00', after_unknown: true },
      { path: 'password', before_redacted: true, after_redacted: true },
      { path: 'tags.Version', before: '41', after: '42' },
    ],
  };
  const history = [
    { id: '20261019T151044Z-b81c2e', kind: 'apply', status: 'applied', time: iso(2 * D + 1 * H), user: 'ada', command: 'pravo apply', reason: 'raise the backup window', action: 'update', rev_before: 13, rev_after: 14, plan: 'plan-2f9c1a4e', git_revision: '7c41e9b2d0f3',
      attrs: [{ path: 'backup_retention_period', before: 7, after: 14 }, { path: 'backup_window', before: '02:00-03:00', after: '03:00-04:00' }, { path: 'password', before_redacted: true, after_redacted: true }] },
    { id: '20261012T080319Z-0d4e77', kind: 'apply', status: 'applied', time: iso(9 * D), user: 'ada', command: 'pravo apply', action: 'update', rev_before: 12, rev_after: 13, attrs_sealed: true },
    { id: '20261002T170455Z-91ac3b', kind: 'apply', status: 'failed', time: iso(19 * D), actor: 'ci@acme', command: 'pravo apply release-39.plan', reason: 'timeout waiting for the instance to become available', action: 'update', rev_before: 11, rev_after: 11 },
    { id: '20260921T101502Z-47be05', kind: 'import', status: 'applied', time: iso(30 * D), user: 'grace', command: 'pravo import api.db estate-db', action: 'import', rev_before: 0, rev_after: 1 },
  ];
  return detailOf(b, g, 'api.db', {
    attrs,
    change,
    history,
    times: { applied_at: iso(2 * D + 1 * H), refreshed_at: iso(2 * H), updated_at: iso(2 * D + 1 * H) },
    notes: ['The plan changes this record in place; a change of instance_class restarts the instance.'],
  });
}

function detailPassword(b, g) {
  const attrs = [
    str('arn', 'arn:aws:ssm:eu-west-1:412058817224:parameter/estate/db/password', 'computed'),
    str('data_type', 'text'),
    str('description', 'Bootstrap password of the estate database'),
    nul('insecure_value', 'string', 'optional,computed'),
    str('key_id', 'alias/aws/ssm'),
    kids('metadata', 'dynamic', 'computed', undefined, { redacted: 'schema' }),
    str('name', '/estate/db/password', 'required'),
    bool('overwrite', true, 'optional'),
    kids('tags', 'map(string)', 'optional', [
      { name: 'ManagedBy', type: 'string', value: 'pravo' },
      { name: 'owner', type: 'string', value: 'platform' },
      { name: 'rotation_token', type: 'string', redacted: 'name' },
    ]),
    str('tier', 'Standard'),
    str('type', 'SecureString', 'required'),
    sensitive('value'),
    num('version', 3, 'computed'),
    { name: 'value_wo', type: 'string', kind: 'optional', redacted: 'write-only' },
    num('value_wo_version', 1, 'optional'),
  ];
  delete attrs[5].children;
  return detailOf(b, g, 'api.db_password', {
    attrs,
    history: [
      { id: '20260929T112200Z-6ac1f0', kind: 'apply', status: 'applied', time: iso(22 * D), user: 'ada', command: 'pravo apply', action: 'update', rev_before: 2, rev_after: 3 },
      { id: '20260915T083410Z-e2b9d4', kind: 'apply', status: 'applied', time: iso(36 * D), user: 'ada', command: 'pravo apply', action: 'create', rev_before: 0, rev_after: 1 },
    ],
    times: { applied_at: iso(22 * D), refreshed_at: iso(5 * H), updated_at: iso(22 * D) },
    notes: ['4 values are withheld: the schema marks value as sensitive, value_wo is write-only, tags.rotation_token matches a name that looks secret, and the type of metadata is not known.'],
  });
}

function detailDrifted(b, g) {
  const attrs = [
    str('arn', 'arn:aws:elasticloadbalancing:eu-west-1:412058817224:loadbalancer/app/estate-edge/50dc6c495c0c9188', 'computed'),
    str('dns_name', 'estate-edge-1829304441.eu-west-1.elb.amazonaws.com', 'computed'),
    bool('drop_invalid_header_fields', false),
    bool('enable_deletion_protection', true, 'optional'),
    bool('enable_http2', true, 'optional'),
    num('idle_timeout', 120, 'optional'),
    bool('internal', false),
    str('ip_address_type', 'ipv4'),
    str('load_balancer_type', 'application', 'optional'),
    str('name', 'estate-edge', 'optional,computed'),
    kids('security_groups', 'set(string)', 'optional,computed', list(['sg-0b7f3a91c2d4e5f60'])),
    kids('subnets', 'set(string)', 'optional,computed', list(['subnet-0a11b22c33d44e55f', 'subnet-0c66d77e88f99a0b1'])),
    kids('tags', 'map(string)', 'optional', kv({ Environment: 'production', ManagedBy: 'pravo' })),
    str('vpc_id', 'vpc-0e5f6a7b8c9d0e1f2', 'computed'),
    num('zone_id_count', 2, 'computed', { deprecated: true }),
  ];
  return detailOf(b, g, 'edge.lb', {
    attrs,
    change: {
      action: 'update',
      reasons: ['the provider reports idle_timeout 60; the state and the specification say 120'],
      attrs: [{ path: 'idle_timeout', before: 60, after: 120 }],
    },
    history: [
      { id: '20261021T093003Z-77d0a1', kind: 'plan', status: 'planned', time: iso(42 * MIN), actor: 'pravo drift', command: 'pravo drift --due', reason: 'found a change at the provider', action: 'drift', rev_before: 5, rev_after: 6,
        attrs: [{ path: 'idle_timeout', before: 120, after: 60 }, { path: 'tags.Owner', after: 'platform' }, { path: 'access_logs', before_redacted: true, after_redacted: true }] },
      { id: '20261014T141200Z-1c9ee2', kind: 'apply', status: 'applied', time: iso(7 * D), user: 'grace', command: 'pravo apply', action: 'update', rev_before: 4, rev_after: 5 },
    ],
    times: { applied_at: iso(7 * D), refreshed_at: iso(40 * MIN), updated_at: iso(40 * MIN), drifted_at: iso(42 * MIN) },
    notes: ['The object at the provider differs from the record: idle_timeout is 60 there. The saved plan sets it back to 120.'],
  });
}

// the details of two outputs: a plain one that has a description, and a sensitive one. An output has one attribute,
// value, and a description when it has one; no history, no lock and no times; its schema is always known.
function detailOutput(b, g, addr, o) {
  const n = g.nodes.find((x) => x.addr === addr);
  return prune({
    version: 1,
    node: n,
    provider: { key: '' },
    attrs: o.attrs,
    schema_known: true,
    depends_on: b.dependsOn(addr),
    used_by: b.usedBy(addr),
    times: {},
    notes: o.notes,
  });
}

function detailURL(b, g) {
  return detailOutput(b, g, 'url', {
    attrs: [
      { name: 'value', type: 'output', value: 'https://api.estate.example.com' },
      { name: 'description', type: 'string', value: 'Public address of the service' },
    ],
    notes: ['The value is the one the last apply recorded.'],
  });
}

function detailSummary(b, g) {
  return detailOutput(b, g, 'summary', {
    attrs: [
      { name: 'value', type: 'output', children: kv({ endpoint: 'estate-db.c8x2kq1.eu-west-1.rds.amazonaws.com:5432', release: 'a3f9c012', url: 'https://api.estate.example.com' }, undefined) },
      { name: 'description', type: 'string', value: 'What an operator needs to reach the service' },
    ],
    notes: ['The value is the one the last apply recorded.'],
  });
}

function detailDBPassword(b, g) {
  return detailOutput(b, g, 'db_password', {
    attrs: [
      { name: 'value', type: 'output', sensitive: true, redacted: 'sensitive' },
      { name: 'description', type: 'string', value: 'Password of the database administrator' },
    ],
    notes: ['The value is the one the last apply recorded.'],
  });
}

// ---------------------------------------------------------------------------------------------------------
// sources of the platform

const L = (no, text, mark, dim) => ({ no, text, mark, dim });

function sourceDB() {
  const def = [
    L(17, '\t// db is the primary store of the service. Its size follows the size field.', undefined, true),
    L(18, '\tsize: string | *"t3.medium"', undefined, true),
    L(19, '\tdb: $aws.#Resource.aws_db_instance & {', 'focus'),
    L(20, '\t\tidentifier:        "\\(name)-db"', 'focus'),
    L(21, '\t\tengine:            "postgres"', 'focus'),
    L(22, '\t\tengine_version:    "16.3"', 'focus'),
    L(23, '\t\tinstance_class:    "db.\\(size)"', 'changed'),
    L(24, '\t\tallocated_storage: 150', 'changed'),
    L(25, '\t\tavailability_zone: "\\(region)a"', 'focus'),
    L(26, '\t\tpassword:          db_pass.result', 'focus'),
    L(27, '\t\tdb_subnet_group_name: db_subnets.name', 'focus'),
    L(28, '\t\tvpc_security_group_ids: [net.sg_db.id]', 'focus'),
    L(29, '\t\ttags: {Environment: env, Team: "platform", ManagedBy: "pravo", Version: "\\(version)"}', 'focus'),
    L(30, '\t}', 'focus'),
    L(31, '', undefined, true),
    L(32, '\t// the credentials of the database go to the secret below', undefined, true),
  ];
  const inst = [
    L(36, '// the api service: one database, one task, one service', undefined, true),
    L(37, '', undefined, true),
    L(38, 'api: svc.#Service & {', 'focus'),
    L(39, '\tname:   "estate"', 'focus'),
    L(40, '\tsize:   "m6g.large" // was "t3.medium"', 'changed'),
    L(41, '}', 'focus'),
    L(42, '', undefined, true),
    L(43, 'edge: edge.#Edge & { api: api.service }', undefined, true),
  ];
  return prune({
    version: 1,
    addr: 'api.db',
    blocks: [
      { role: 'definition', label: '#Service.db', file: 'modules/api/main.cue', kind: 'file', start_line: 17, end_line: 32, modified: iso(2 * D + 2 * H), lines: def },
      { role: 'instance', label: 'api', file: 'main.cue', kind: 'file', start_line: 36, end_line: 43, modified: iso(40 * MIN), lines: inst },
    ],
    contributions: [
      { kind: 'file', where: 'modules/api/main.cue:19', note: 'defines the resource' },
      { kind: 'file', where: 'main.cue:40', note: 'sets size, the change in this plan' },
      { kind: 'tag', where: '-t region=eu-west-1', note: 'sets region, used on line 25' },
      { kind: 'generated', where: 'cue.mod/gen/pravo.dev/providers/hashicorp/aws', note: 'schema, defaults and types of aws_db_instance' },
      { kind: 'builtin', where: 'pravo.dev/pravo', note: '#Resource and $provider' },
      { kind: 'policy', where: 'policy/no-public-db.cue', note: 'checks publicly_accessible of every aws_db_instance' },
    ],
    generated: 1,
    notes: ['main.cue changed 40 minutes ago, after the state was written.'],
  });
}

function sourceURL() {
  return prune({
    version: 1,
    addr: 'url',
    blocks: [
      { role: 'definition', label: 'url', file: 'main.cue', kind: 'file', start_line: 56, end_line: 63, modified: iso(40 * MIN), lines: [
        L(56, '', undefined, true),
        L(57, '// The address that clients use.', undefined, true),
        L(58, 'url: pravo.#Output & {', 'focus'),
        L(59, '\tvalue:       "https://(edge.dns.name)"', 'focus'),
        L(60, '\tdescription: "Public address of the service"', 'focus'),
        L(61, '}', 'focus'),
        L(62, '', undefined, true),
        L(63, '// The database, for the people who run it.', undefined, true),
      ] },
    ],
    contributions: [
      { kind: 'file', where: 'main.cue:58-61', note: 'writes the output' },
      { kind: 'builtin', where: 'pravo.dev/pravo', note: 'the built-in package gives pravo.#Output' },
    ],
  });
}

function sourcePassword() {
  const def = [
    L(8, '#Parameter: {', undefined, true),
    L(9, '\tname:  string', undefined, true),
    L(10, '\tvalue: string', undefined, true),
    L(11, '\ttags:  [string]: string', undefined, true),
    L(12, '\tparam: $aws.#Resource.aws_ssm_parameter & {', 'focus'),
    L(13, '\t\t"name":        name', 'focus'),
    L(14, '\t\t"type":        "SecureString"', 'focus'),
    L(15, '\t\t"value":       value', 'focus'),
    L(16, '\t\t"tier":        "Standard"', 'focus'),
    L(17, '\t\t"description": "Bootstrap password of the estate database"', 'focus'),
    L(18, '\t\t"tags":        tags', 'focus'),
    L(19, '\t}', 'focus'),
    L(20, '}', undefined, true),
  ];
  const inst = [
    L(31, 'db_password: params.#Parameter & {', 'focus'),
    L(32, '\tname:  "/estate/db/password"', 'focus'),
    L(33, '\tvalue: "••••••••"', 'sensitive'),
    L(34, '\ttags: {', 'focus'),
    L(35, '\t\towner:          "platform"', 'focus'),
    L(36, '\t\trotation_token: "••••••••"', 'sensitive'),
    L(37, '\t}', 'focus'),
    L(38, '}', 'focus'),
  ];
  return prune({
    version: 1,
    addr: 'api.db_password',
    blocks: [
      { role: 'definition', label: '#Parameter.param', file: 'cache/github.com/acme/pravo-aws-params@v0.4.2/param.cue', kind: 'cache', start_line: 8, end_line: 20, modified: iso(41 * D), lines: def },
      { role: 'instance', label: 'api.db_password', file: 'modules/api/main.cue', kind: 'file', start_line: 31, end_line: 38, modified: iso(23 * D), lines: inst },
    ],
    contributions: [
      { kind: 'cache', where: 'github.com/acme/pravo-aws-params@v0.4.2', note: 'defines #Parameter and the resource it writes' },
      { kind: 'file', where: 'modules/api/main.cue:31', note: 'instantiates #Parameter; two literals are masked' },
      { kind: 'tag', where: '-t env=production', note: 'sets env, used by the tags of the namespace' },
      { kind: 'generated', where: 'cue.mod/gen/pravo.dev/providers/hashicorp/aws', note: 'marks value as sensitive and value_wo as write-only' },
      { kind: 'builtin', where: 'pravo.dev/pravo', note: '#Resource and $provider' },
      { kind: 'policy', where: 'policy/no-public-db.cue', note: 'does not match this type; listed because it was loaded' },
    ],
    generated: 1,
    notes: ['A literal assigned to a sensitive attribute, or to an attribute whose name looks secret, is masked before the line is sent.'],
  });
}

// ---------------------------------------------------------------------------------------------------------
// fleet: regions x services nested three deep, with groups of twelve or more siblings of one type.

function fleet() {
  const b = new Builder();
  const F = 'modules/service/main.cue';
  const regions = [['eu_west', 'eu-west-1'], ['us_east', 'us-east-1'], ['ap_south', 'ap-south-1']];
  const services = ['api', 'web', 'jobs', 'data'];
  b.add('build_id', 'random_id', { provider: 'random', source: src('main.cue', 4, 6), revision: 2, refreshed: 3 * D });
  b.ref('regions', src('main.cue', 10, 30), src('modules/region/main.cue', 1, 120));
  for (const [r] of regions) {
    b.ref(`regions.${r}`, src('main.cue', 12, 20), src('modules/region/main.cue', 1, 120));
    b.ref(`regions.${r}.net`, src('modules/region/main.cue', 4, 24), src('modules/net/main.cue', 1, 60));
    const net = `regions.${r}.net`;
    b.add(`${net}.vpc`, 'aws_vpc', { provider: 'aws', source: src('modules/net/main.cue', 3, 7), revision: 3 });
    b.add(`${net}.igw`, 'aws_internet_gateway', { source: src('modules/net/main.cue', 9, 11) });
    b.add(`${net}.subnet_a`, 'aws_subnet', { source: src('modules/net/main.cue', 13, 18) });
    b.add(`${net}.subnet_b`, 'aws_subnet', { source: src('modules/net/main.cue', 20, 25) });
    b.deps(`${net}.igw`, `${net}.vpc`);
    b.deps(`${net}.subnet_a`, `${net}.vpc`);
    b.deps(`${net}.subnet_b`, `${net}.vpc`);
    for (const s of services) {
      const m = `regions.${r}.${s}`;
      b.ref(m, src('modules/region/main.cue', 30 + services.indexOf(s) * 6, 35 + services.indexOf(s) * 6), src(F, 1, 140));
      const at = (k) => src(F, 10 + k * 8, 17 + k * 8);
      b.add(`${m}.sg`, 'aws_security_group', { source: at(0) });
      b.add(`${m}.lb`, 'aws_lb', { source: at(1) });
      b.add(`${m}.tg`, 'aws_lb_target_group', { source: at(2) });
      b.add(`${m}.listener`, 'aws_lb_listener', { source: at(3) });
      b.add(`${m}.role`, 'aws_iam_role', { source: at(4) });
      b.add(`${m}.logs`, 'aws_cloudwatch_log_group', { source: at(5) });
      b.add(`${m}.task`, 'aws_ecs_task_definition', { source: at(6), sensitive: s === 'api' || s === 'data' ? 1 : 0 });
      b.add(`${m}.service`, 'aws_ecs_service', { source: at(7) });
      b.add(`${m}.scaling`, 'aws_appautoscaling_target', { source: at(8) });
      b.deps(`${m}.sg`, `${net}.vpc`);
      b.deps(`${m}.lb`, `${m}.sg`, `${net}.subnet_a`, `${net}.subnet_b`);
      b.deps(`${m}.tg`, `${net}.vpc`);
      b.deps(`${m}.listener`, `${m}.lb`, `${m}.tg`);
      b.deps(`${m}.task`, `${m}.role`, `${m}.logs`);
      b.deps(`${m}.service`, `${m}.task`, `${m}.tg`, `${m}.sg`, `${net}.subnet_a`, `${m}.listener`);
      b.deps(`${m}.scaling`, `${m}.service`);
    }
    b.deps(`regions.${r}.web.service`, `regions.${r}.api.lb`);
  }
  // for-each groups: a struct of one type is a namespace of its own; flat siblings sit in their namespace
  for (let i = 0; i < 12; i++) {
    const q = `regions.eu_west.jobs.queues.q${String(i).padStart(2, '0')}`;
    b.add(q, 'aws_sqs_queue', { source: src('modules/jobs/queues.cue', 6, 12) });
    b.dep(q, 'regions.eu_west.jobs.service');
  }
  b.ref('regions.eu_west.jobs.queues', src('modules/service/main.cue', 150, 154), src('modules/jobs/queues.cue', 1, 20));
  for (let i = 0; i < 14; i++) {
    const n = `regions.us_east.data.nodes.n${String(i).padStart(2, '0')}`;
    b.add(n, 'aws_instance', { source: src('modules/data/nodes.cue', 5, 18), sensitive: 0 });
    b.dep('regions.us_east.net.subnet_a', n);
    b.dep(n, 'regions.us_east.data.tg');
  }
  b.ref('regions.us_east.data.nodes', src('modules/service/main.cue', 160, 164), src('modules/data/nodes.cue', 1, 24));
  for (let i = 0; i < 12; i++) {
    const n = `regions.ap_south.web.edge_${String(i).padStart(2, '0')}`;
    b.add(n, 'aws_instance', { source: src('modules/web/edge.cue', 3, 14) });
    b.dep('regions.ap_south.net.subnet_b', n);
    b.dep(n, 'regions.ap_south.web.lb');
  }
  // global
  b.ref('global', src('main.cue', 40, 60), src('modules/global/main.cue', 1, 60));
  b.add('global.zone', 'aws_route53_zone', { mode: 'data', source: src('modules/global/main.cue', 3, 5), action: 'read', refreshed: 5 * MIN });
  b.add('global.cdn', 'aws_cloudfront_distribution', { source: src('modules/global/main.cue', 8, 30), revision: 9 });
  b.add('global.oai', 'aws_cloudfront_origin_access_identity', { source: src('modules/global/main.cue', 32, 34) });
  b.add('global.cert', 'aws_acm_certificate', { provider: 'aws.us', source: src('modules/global/main.cue', 36, 42) });
  b.add('global.admins', 'aws_iam_group', { source: src('modules/global/main.cue', 44, 46) });
  for (const [r] of regions) {
    b.add(`global.dns_${r}`, 'aws_route53_record', { source: src('modules/global/main.cue', 50, 56), action: r === 'us_east' ? 'create' : undefined, refreshed: r === 'us_east' ? null : undefined, revision: r === 'us_east' ? 0 : undefined });
    b.deps(`global.dns_${r}`, 'global.zone', `regions.${r}.web.lb`);
  }
  b.deps('global.cdn', 'global.oai', 'global.cert', 'regions.eu_west.web.lb');
  b.deps('global.cert', 'global.zone');

  // the outputs: two at the root and one for each region
  b.output('cdn_domain', { source: src('main.cue', 62, 64) });
  b.output('regions', { source: src('main.cue', 66, 68) });
  b.deps('cdn_domain', 'global.cdn');
  for (const [r] of regions) { b.output(`regions.${r}.web.url`, { source: src('modules/web/main.cue', 70, 72) }); b.deps(`regions.${r}.web.url`, `regions.${r}.web.lb`); }

  // plan: a release of us_east
  const A = (addr, action, extra) => Object.assign(b.index.get(addr), { action }, extra || {});
  A('regions.us_east.api.task', 'update'); A('regions.us_east.api.service', 'update', { locked: true });
  A('regions.us_east.web.task', 'update'); A('regions.us_east.web.service', 'update');
  A('regions.eu_west.api.task', 'replace'); A('regions.eu_west.api.service', 'update');
  A('regions.ap_south.jobs.service', 'update');
  A('regions.eu_west.web.lb', 'update', { drifted: true });
  A('regions.ap_south.data.logs', 'delete', { presence: 'state', source: undefined });
  Object.assign(b.index.get('regions.ap_south.web.edge_05'), { gone: true, drifted: true, action: 'create' });
  for (const addr of ['regions.us_east.net.vpc', 'regions.eu_west.net.vpc', 'regions.ap_south.net.vpc', 'global.zone']) b.index.get(addr).action = b.index.get(addr).action || 'no-op';
  Object.assign(b.index.get('regions.us_east.data.nodes.n03'), { drifted: true });

  const plan = planOf(b, { file: '.pravo/plans/release-2026-10-21.plan', createdAgo: 31 * MIN, actor: 'release-bot', hints: ['regions.eu_west.api.task is replaced: its family changed.'] });
  const g = b.graph({
    revision: 'r-fleet-0a9d33f1',
    project: {
      dir: '/srv/infra/fleet',
      package: 'fleet',
      version: '0.9.0-dev',
      state: { location: '/srv/infra/fleet/.pravo/state', backend: 'local', lineage: '0e1f2a3b-4c5d-4e6f-8a9b-0c1d2e3f4a5b', format: 3, records: b.nodes.filter((n) => n.mode !== 'output').length - 2, locks: 1 },
      providers: [{ ...AWS, instances: ['aws', 'aws.us'].map((key) => ({ key, count: b.nodes.filter((n) => (n.provider || n.type.split('_')[0]) === key).length })) }, RANDOM],
      loaded_at: iso(0),
    },
    outputs: [{ name: 'cdn_domain', value: 'd2f8x1q0.cloudfront.net' }, { name: 'regions', value: ['eu-west-1', 'us-east-1', 'ap-south-1'] }, ...[...regions].map(([r]) => ({ name: `regions.${r}.web.url`, value: `https://web.${r.replace('_', '-')}.fleet.example.com` })).sort((x, y) => (x.name < y.name ? -1 : 1))].sort((x, y) => (x.name < y.name ? -1 : 1)),
    plan,
  });
  return { b, g };
}

// ---------------------------------------------------------------------------------------------------------
// empty and spec error

function empty() {
  return {
    version: 1,
    revision: 'r-empty',
    project: {
      dir: '/srv/infra/new-estate',
      package: 'estate',
      version: '0.9.0-dev',
      state: { location: '/srv/infra/new-estate/.pravo/state', backend: 'local', records: 0, locks: 0 },
      providers: [AWS],
      loaded_at: iso(0),
    },
    nodes: [],
    edges: [],
    namespaces: [],
    outputs: [],
  };
}

function specError() {
  const b = new Builder();
  b.add('network.vpc', 'aws_vpc', { presence: 'state', revision: 4 });
  b.add('network.subnet_a', 'aws_subnet', { presence: 'state', revision: 2 });
  b.add('network.subnet_b', 'aws_subnet', { presence: 'state', revision: 2 });
  b.add('network.sg_db', 'aws_security_group', { presence: 'state', revision: 3 });
  b.add('api.db_pass', 'random_password', { presence: 'state', sensitive: 2, provider: 'random', revision: 6 });
  b.add('api.db', 'aws_db_instance', { presence: 'state', sensitive: 2, revision: 14 });
  b.add('api.task', 'aws_ecs_task_definition', { presence: 'state', revision: 20 });
  b.add('api.service', 'aws_ecs_service', { presence: 'state', revision: 16, drifted: true });
  b.add('edge.lb', 'aws_lb', { presence: 'state', revision: 5 });
  b.add('edge.tg', 'aws_lb_target_group', { presence: 'state', revision: 4 });
  b.add('edge.dns', 'aws_route53_record', { presence: 'state', revision: 2 });
  b.add('build_id', 'random_id', { presence: 'state', provider: 'random', revision: 3 });
  b.output('db_endpoint', { presence: 'state' });
  b.output('url', { presence: 'state' });
  b.deps('network.subnet_a', 'network.vpc'); b.deps('network.subnet_b', 'network.vpc'); b.deps('network.sg_db', 'network.vpc');
  b.deps('api.db', 'api.db_pass', 'network.sg_db', 'network.subnet_a', 'network.subnet_b');
  b.deps('api.task', 'api.db'); b.deps('api.service', 'api.task', 'edge.tg');
  b.deps('edge.lb', 'network.subnet_a', 'network.subnet_b'); b.deps('edge.tg', 'network.vpc'); b.deps('edge.dns', 'edge.lb');
  return b.graph({
    revision: 'r-specerror-5e11',
    project: {
      dir: PROJECT_DIR,
      package: 'estate',
      version: '0.9.0-dev',
      state: { location: 's3://acme-state/estate', backend: 's3', lineage: '5c8d7e2a-61b4-4f0e-9a3b-0f6f5d1c2e77', format: 3, records: 12, locks: 0 },
      providers: [AWS, RANDOM],
      spec_error: 'modules/api/main.cue:26:21: reference "db_pass.reslt" not found\n    (did you mean "db_pass.result"?)',
      loaded_at: iso(0),
    },
    outputs: [
      { name: 'db_endpoint', value: 'estate-db.c8x2kq1.eu-west-1.rds.amazonaws.com:5432' },
      { name: 'url', value: 'https://api.estate.example.com' },
    ],
    warnings: ['The specification does not load: showing the state alone. Fix main.cue and the page updates.'],
  });
}

// ---------------------------------------------------------------------------------------------------------

const P = platform();
out('graph.platform.json', P.g);
out('detail.db.json', detailDB(P.b, P.g));
out('detail.password.json', detailPassword(P.b, P.g));
out('detail.drifted.json', detailDrifted(P.b, P.g));
out('source.db.json', sourceDB());
out('source.password.json', sourcePassword());
out('detail.output.json', detailURL(P.b, P.g));
out('detail.outputmap.json', detailSummary(P.b, P.g));
out('detail.outputsecret.json', detailDBPassword(P.b, P.g));
out('source.output.json', sourceURL());
out('plans.platform.json', P.stored.list);
for (const [id, overlay] of Object.entries(P.stored.overlays)) out(`plan.${id.replace(/^plan-/, '')}.json`, overlay);
const F = fleet();
out('graph.fleet.json', F.g);
out('graph.empty.json', empty());
out('graph.specerror.json', specError());
console.log('platform', P.g.nodes.length, 'nodes', P.g.edges.length, 'edges', P.g.namespaces.length, 'namespaces; fleet', F.g.nodes.length, 'nodes', F.g.edges.length, 'edges', F.g.namespaces.length, 'namespaces');
