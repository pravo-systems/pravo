'use strict';
// Generates fixtures/graph.big.json: about 1800 nodes in 30 modules, with flat groups of siblings of one
// type, a plan and a few marks. Deterministic. Run: node gen-big.js

const fs = require('fs');
const path = require('path');
const { MIN, H, D, iso, rng, Builder, planOf } = require('./gen-lib');

const SCALE = 0.45; // tuned so that the graph has about 1800 nodes
const rand = rng(20261021);
const pick = (arr) => arr[Math.floor(rand() * arr.length)];
const int = (lo, hi) => lo + Math.floor(rand() * (hi - lo + 1));

// module -> singles (types of distinct resources) and groups ([type, count])
const SINGLES = {
  vpc: ['aws_vpc', 'aws_internet_gateway', 'aws_nat_gateway', 'aws_eip', 'aws_route_table', 'aws_vpc_endpoint', 'aws_flow_log'],
  db: ['aws_db_subnet_group', 'aws_db_parameter_group', 'aws_db_instance', 'aws_kms_key', 'aws_secretsmanager_secret', 'aws_cloudwatch_metric_alarm'],
  svc: ['aws_ecs_cluster', 'aws_ecs_task_definition', 'aws_ecs_service', 'aws_lb', 'aws_lb_target_group', 'aws_lb_listener', 'aws_iam_role', 'aws_iam_role_policy', 'aws_cloudwatch_log_group', 'aws_security_group', 'aws_appautoscaling_target', 'aws_appautoscaling_policy'],
  iam: ['aws_iam_policy', 'aws_iam_role', 'aws_iam_group', 'aws_iam_openid_connect_provider'],
  obs: ['aws_cloudwatch_dashboard', 'aws_sns_topic', 'aws_cloudwatch_log_group', 'aws_cloudwatch_metric_alarm', 'aws_kinesis_firehose_delivery_stream'],
};
const GROUPS = {
  vpc: [['aws_subnet', 36], ['aws_security_group_rule', 90], ['aws_route', 44]],
  db: [['aws_db_instance', 28], ['aws_cloudwatch_metric_alarm', 60]],
  svc: [['aws_ecs_service', 42], ['aws_lb_listener_rule', 66], ['aws_cloudwatch_metric_alarm', 50], ['aws_iam_role_policy_attachment', 34]],
  iam: [['aws_iam_user', 72], ['aws_iam_user_policy_attachment', 96], ['aws_iam_access_key', 40]],
  obs: [['aws_cloudwatch_metric_alarm', 120], ['aws_sns_topic_subscription', 48], ['aws_cloudwatch_log_metric_filter', 64]],
  dns: [['aws_route53_record', 110], ['aws_acm_certificate', 12]],
  queue: [['aws_sqs_queue', 40], ['aws_sns_topic_subscription', 30]],
  ec2: [['aws_instance', 58], ['aws_ebs_volume', 58], ['aws_volume_attachment', 58]],
};

// [path, profile, groups]
const MODS = [
  ['platform', 'vpc'], ['platform.network', 'vpc', ['vpc']], ['platform.compute', 'svc', ['ec2']], ['platform.iam', 'iam', ['iam']], ['platform.dns', 'svc', ['dns']],
  ['data', 'db'], ['data.postgres', 'db', ['db']], ['data.postgres.replicas', 'db', ['db']], ['data.cache', 'db'], ['data.streams', 'obs', ['queue']], ['data.warehouse', 'db', ['db']],
  ['apps', 'svc'], ['apps.checkout', 'svc', ['svc']], ['apps.checkout.workers', 'svc', ['ec2']], ['apps.catalog', 'svc', ['svc']], ['apps.catalog.images', 'svc', ['queue']], ['apps.search', 'svc', ['svc']],
  ['apps.billing', 'svc', ['svc']], ['apps.billing.jobs', 'svc', ['queue']], ['apps.identity', 'svc', ['svc', 'iam']], ['apps.notify', 'svc', ['queue']], ['apps.notify.senders', 'svc', ['queue']],
  ['edge', 'svc'], ['edge.cdn', 'svc', ['dns']], ['edge.waf', 'iam', ['iam']], ['edge.lb', 'svc', ['svc']],
  ['observability', 'obs'], ['observability.logs', 'obs', ['obs']], ['observability.metrics', 'obs', ['obs']], ['observability.alerts', 'obs', ['obs']],
];
// the list above has 30 module paths; security is the 31st below and is folded into iam
MODS.length = 30;

const b = new Builder();
const singles = new Map(); // module -> addrs of singles
const groups = new Map(); // module -> [[addrs of group]]
let n = 0;

function nameFor(type, i) { return type.replace(/^aws_/, '').replace(/_/g, '') .slice(0, 12) + '_' + String(i).padStart(3, '0'); }

for (const [mpath, profile, gkeys] of MODS) {
  const s = [];
  const types = SINGLES[profile];
  const count = int(6, 16);
  for (let i = 0; i < count; i++) {
    const t = types[i % types.length];
    const name = t.replace(/^aws_/, '') + (i >= types.length ? '_' + (Math.floor(i / types.length) + 1) : '');
    const addr = `${mpath}.${name}`;
    b.add(addr, t, { source: { file: `modules/${mpath.split('.')[0]}/main.cue`, line: 10 + i * 6, col: 2, end_line: 15 + i * 6 }, revision: int(1, 18), refreshed: int(1, 200) * MIN, sensitive: t === 'aws_db_instance' || t === 'aws_secretsmanager_secret' || t === 'aws_iam_access_key' ? int(1, 2) : 0 });
    s.push(addr); n++;
  }
  singles.set(mpath, s);
  const gs = [];
  for (const key of gkeys || []) {
    for (const [t, c0] of GROUPS[key]) {
      const c = Math.max(8, Math.round(c0 * SCALE * (0.55 + rand() * 0.7)));
      const members = [];
      for (let i = 0; i < c; i++) {
        const addr = `${mpath}.${nameFor(t, i)}`;
        if (b.index.has(addr)) continue;
        b.add(addr, t, { source: { file: `modules/${mpath.split('.')[0]}/groups.cue`, line: 4 + (members.length % 5), col: 2, end_line: 12 + (members.length % 5) }, revision: int(1, 9), refreshed: int(1, 300) * MIN });
        members.push(addr); n++;
      }
      gs.push(members);
    }
  }
  groups.set(mpath, gs);
}

// edges: forward only, by module order, so the graph is acyclic
const order = MODS.map((m) => m[0]);
order.forEach((mpath, mi) => {
  const s = singles.get(mpath);
  for (let i = 1; i < s.length; i++) b.dep(s[int(0, i - 1)], s[i]);
  for (let i = 3; i < s.length; i += 2) if (rand() < 0.6) { const d = s[int(0, i - 2)]; if (!b.edges.some((e) => e.dependency === d && e.dependent === s[i])) b.dep(d, s[i]); }
  for (const members of groups.get(mpath)) {
    if (!s.length) continue;
    const feeder = s[int(0, Math.min(2, s.length - 1))];
    const sink = s[s.length - 1];
    members.forEach((m, i) => {
      b.dep(feeder, m);
      if (i % 3 === 0 && s.length > 4) b.dep(s[int(1, s.length - 2)], m);
    });
    // fan-in: a sample of the group feeds one of the singles
    const take = members.filter((_, i) => i % 3 === 0).slice(0, 24);
    for (const m of take) if (sink !== feeder) b.dep(m, sink);
  }
  if (mi > 0) {
    for (let k = 0; k < int(2, 4); k++) {
      const from = singles.get(order[int(0, mi - 1)]);
      if (from && from.length && s.length) { const d = pick(from), t = pick(s); if (!b.edges.some((e) => e.dependency === d && e.dependent === t)) b.dep(d, t); }
    }
  }
});

// marks and a plan
const all = b.nodes;
const mark = (k, f) => { for (let i = 0; i < k; i++) f(all[int(0, all.length - 1)]); };
mark(40, (x) => { x.action = pick(['update', 'update', 'update', 'create', 'replace']); });
mark(120, (x) => { if (!x.action) x.action = 'no-op'; });
mark(9, (x) => { x.drifted = true; if (!x.action) x.action = 'update'; });
mark(3, (x) => { x.gone = true; x.drifted = true; x.action = 'create'; });
mark(4, (x) => { x.locked = true; });
for (const x of all) if (x.action === 'create' && !x.gone) { x.presence = 'spec'; x.refreshed_at = undefined; x.revision = 0; }
const old = b.add('observability.alerts.old_pager', 'aws_sns_topic', { presence: 'state', action: 'delete' });
b.dep('observability.alerts.sns_topic', old.addr);

b.output('regions', { source: { file: 'main.cue', line: 90, col: 2, end_line: 93 } });
// a root with many outputs: each reads one resource, and the first view has to stay legible
const pool = all.filter((n) => n.mode !== 'output');
const outList = [{ name: 'regions', value: ['eu-west-1'] }];
for (let i = 1; i <= 26; i++) {
  const name = 'endpoint_' + String(i).padStart(2, '0');
  b.output(name, { sensitive: i % 9 === 0, source: { file: 'main.cue', line: 94 + i * 4, col: 2, end_line: 97 + i * 4 } });
  b.dep(pool[(i * 71) % pool.length].addr, name);
  outList.push(i % 9 === 0 ? { name, sensitive: true, redacted: 'sensitive' } : { name, value: `https://e${i}.atlas.example.com` });
}
const plan = planOf(b, { file: '.pravo/plans/nightly.plan', createdAgo: 8 * MIN, actor: 'nightly', hints: [] });
const g = b.graph({
  revision: 'r-big-31d2c07a',
  project: {
    dir: '/srv/infra/atlas',
    package: 'atlas',
    version: '0.9.0-dev',
    state: { location: 's3://acme-atlas-state/prod', backend: 's3', lineage: '9b1de7f0-2c4a-4f33-8d0e-5a6b7c8d9e0f', format: 3, records: b.nodes.filter((n) => n.mode !== 'output').length - 1, locks: 4 },
    providers: [{ local: 'aws', source: 'registry.opentofu.org/hashicorp/aws', version: '5.61.0' }],
    loaded_at: iso(0),
  },
  outputs: outList.sort((x, y) => (x.name < y.name ? -1 : 1)),
  plan,
});
fs.writeFileSync(path.join(__dirname, 'fixtures', 'graph.big.json'), JSON.stringify(g) + '\n');
console.log('big', g.nodes.length, 'nodes', g.edges.length, 'edges', g.modules.length, 'modules');
