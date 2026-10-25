'use strict';
// Generates fixtures/traces.platform.json (the answer of /api/traces) and fixtures/trace.XXXXXXXX.json (the answers of
// /api/trace?id=..., named by the first eight digits of the id) for the platform: an apply, a plan, an apply that failed
// and a drift check. The runs are made by a small scheduler that walks the dependencies of graph.platform.json with
// eight workers, the way the engine does, with durations that depend on the type. Run: node gen-traces.js
// check-fixtures.js verifies the output against api.go. makeRun is used by check-traces.mjs.

const fs = require('fs');
const path = require('path');
const { rng } = require('./gen-lib');

const graph = JSON.parse(fs.readFileSync(path.join(__dirname, 'fixtures', 'graph.platform.json'), 'utf8'));

// Durations in microseconds by type: [validate, read, plan, apply].
const COST = {
  aws_db_instance: [4e3, 420e3, 38e3, 5.4e6], aws_nat_gateway: [3e3, 180e3, 22e3, 2.1e6], aws_ecs_service: [3e3, 160e3, 30e3, 1.8e6],
  aws_ecs_cluster: [2e3, 90e3, 12e3, 640e3], aws_lb: [3e3, 210e3, 26e3, 2.6e6], aws_vpc: [2e3, 120e3, 14e3, 720e3],
  aws_elasticache_cluster: [3e3, 260e3, 24e3, 4.1e6], aws_cloudfront_distribution: [4e3, 300e3, 40e3, 3.3e6],
  aws_caller_identity: [1e3, 140e3, 0, 0], random_id: [1e3, 0, 4e3, 8e3], local_file: [1e3, 3e3, 6e3, 14e3],
};
const DEFAULT = [2e3, 110e3, 14e3, 380e3];

const hex = (n, seed, pre = '') => (pre + Array.from({ length: n }, (_, i) => ((seed * 2654435761 + i * 40503 + i * i * 977) >>> 0).toString(16).slice(-1)).join('')).slice(0, n);

// makeRun builds one run. kind is 'apply', 'plan', 'drift' or 'failed'.
function makeRun(opts) {
  const { kind, traceId, start, seed = 1, workers = 8, resources: all = resourcesOf(graph) } = opts;
  const r = rng(seed);
  const spans = [];
  const used = new Set();
  const sid = () => { for (;;) { const x = Array.from({ length: 16 }, () => '0123456789abcdef'[Math.floor(r() * 16)]).join(''); if (!used.has(x) && x !== '0'.repeat(16)) { used.add(x); return x; } } };
  const span = (name, parent, startUS, durUS, attrs = {}, extra = {}) => {
    const s = { id: sid(), name, start_us: Math.round(startUS), dur_us: Math.max(1, Math.round(durUS)), ...extra };
    if (parent) s.parent = parent.id;
    if (Object.keys(attrs).length) s.attrs = attrs;
    spans.push(s);
    return s;
  };
  const jit = (v, spread = 0.35) => v * (1 + (r() - 0.5) * 2 * spread);
  const cmd = { apply: 'pravo apply', plan: 'pravo plan', drift: 'pravo drift', failed: 'pravo apply' }[kind];
  const root = span(cmd, null, 0, 0, {}, {});
  const events = [];
  let t = 1800; // loading the specification
  const ev = (parent, name, at, attrs) => { (parent.events = parent.events || []).push({ name, at_us: Math.round(at), ...(attrs ? { attrs } : {}) }); };
  ev(root, 'loaded', t, { specification: 'modules/api/main.cue' });
  t += 5200;
  ev(root, 'state opened', t);
  t += 2400;
  ev(root, 'provider version from the lock file', t, { provider: 'aws', version: '5.61.0' });
  ev(root, 'provider found installed', t + 600, { provider: 'aws' });
  ev(root, 'schema from the cache', t + 2300, { provider: 'aws' });
  t += 14000;
  ev(root, 'schemas ready', t);

  const phase = (name, items, begin, mode) => {
    const eng = span(name, root, begin, 0, { 'pravo.actor': 'ana@acme', 'pravo.destroy': false });
    for (const prov of ['aws', 'random', 'local']) {
      const v = span('provider ValidateProviderConfig', eng, begin + 200, jit(3e3), { 'rpc.system': 'grpc', 'rpc.service': 'tfplugin.Provider', 'rpc.method': 'ValidateProviderConfig', 'pravo.provider': prov, 'pravo.protocol': 6 });
      span('provider ConfigureProvider', eng, v.start_us + v.dur_us, jit(prov === 'aws' ? 38e3 : 4e3), { 'rpc.system': 'grpc', 'rpc.service': 'tfplugin.Provider', 'rpc.method': 'ConfigureProvider', 'pravo.provider': prov, 'pravo.protocol': 6 });
    }
    const ready = begin + 60000;
    const done = new Map(); // addr -> end
    const free = Array.from({ length: workers }, () => ready);
    const failed = new Set();
    let end = ready;
    for (const it of items) {
      const deps = it.deps.filter((d) => done.has(d) || failed.has(d));
      if (deps.some((d) => failed.has(d))) { failed.add(it.addr); continue; } // a dependant of a failure is not started
      free.sort((a, b) => a - b);
      const at = Math.max(free[0], ...deps.map((d) => done.get(d)), ready);
      const [vd, rd, pl, ap] = it.cost;
      let c = at;
      const attrs = { 'pravo.resource.address': it.addr, 'pravo.resource.type': it.type, 'pravo.mode': it.mode, 'pravo.provider': it.type.split('_')[0], 'pravo.phase': mode, 'pravo.action': it.action || 'no-op', 'pravo.skipped': false, 'pravo.refreshed': mode === 'plan' && rd > 0, 'pravo.drifted': !!it.drifted, 'pravo.deferred': false };
      const rs = span('resource ' + it.addr, eng, at, 0, attrs);
      const rpc = (method, d, extra = {}) => {
        const s = span('provider ' + method, rs, c, d, { 'rpc.system': 'grpc', 'rpc.service': 'tfplugin.Provider', 'rpc.method': method, 'pravo.provider': attrs['pravo.provider'], 'pravo.protocol': 6, 'pravo.resource.type': it.type, ...extra });
        c += s.dur_us;
        return s;
      };
      ev(rs, 'configuring provider', c, { provider: attrs['pravo.provider'] });
      rpc('ValidateResourceConfig', jit(vd));
      if (mode === 'plan') {
        if (rd > 0) rpc('ReadResource', jit(rd));
        rpc('PlanResourceChange', jit(pl));
        ev(rs, 'task', c, { action: attrs['pravo.action'], reasons: it.action === 'create' ? ['not in the state'] : [] });
      } else {
        rpc('PlanResourceChange', jit(pl));
        const bad = kind === 'failed' && it.addr === 'api.db';
        const a = rpc('ApplyResourceChange', jit(ap * (bad ? 0.5 : 1)));
        if (bad) {
          const msg = 'creating RDS DB Instance (api-db): operation error RDS: CreateDBInstance, https response error StatusCode: 400, api error InvalidParameterCombination: Cannot find version 15.9 for postgres';
          a.status = 'error'; a.message = msg;
          (a.events = a.events || []).push({ name: 'exception', at_us: a.start_us + a.dur_us, attrs: { 'exception.type': '*errors.errorString', 'exception.message': msg } });
          rs.status = 'error'; rs.message = msg;
          failed.add(it.addr);
        }
        ev(rs, 'task', at, { action: attrs['pravo.action'] });
      }
      rs.dur_us = Math.round(c - at);
      free[0] = c;
      done.set(it.addr, c);
      end = Math.max(end, c);
    }
    eng.dur_us = Math.round(end + 3000 - begin);
    return { eng, end: end + 3000, failed };
  };

  const plan = phase('engine plan', all, t + 1000, 'plan');
  const counts = all.filter((x) => x.action && x.action !== 'no-op').length;
  Object.assign(plan.eng.attrs, { 'pravo.plan.add': all.filter((x) => x.action === 'create').length, 'pravo.plan.change': all.filter((x) => x.action === 'update').length, 'pravo.plan.destroy': all.filter((x) => x.action === 'delete').length, 'pravo.nodes.total': all.length, 'pravo.nodes.planned': counts });
  ev(plan.eng, 'plan', plan.eng.start_us + 4000, { refresh: 'age', full: false, tasks: all.length });
  ev(plan.eng, 'tasks run', plan.end - 3000);
  ev(plan.eng, 'settled', plan.end - 1000);
  let end = plan.end;
  let txn = '';
  let exit = kind === 'plan' || kind === 'drift' ? 2 : 0;
  if (kind === 'apply' || kind === 'failed') {
    const changing = all.filter((x) => x.action && x.action !== 'no-op');
    const ap = phase('engine apply', changing, end + 12000, 'apply');
    txn = '20261021T' + opts.txnTime + 'Z-' + hex(6, seed, '');
    Object.assign(ap.eng.attrs, { 'pravo.txn': txn, 'pravo.apply.added': changing.filter((x) => x.action === 'create').length, 'pravo.apply.changed': changing.filter((x) => x.action === 'update').length, 'pravo.apply.destroyed': 0 });
    ev(ap.eng, 'apply', ap.eng.start_us + 3000, { changes: changing.length });
    ev(ap.eng, 'locked ' + changing.length + ' resources', ap.eng.start_us + 21000);
    ev(ap.eng, 'tasks run', ap.end - 3000);
    ev(ap.eng, 'settled', ap.end - 1000);
    if (ap.failed.size) { ap.eng.status = 'error'; ap.eng.message = 'api.db failed; ' + (ap.failed.size - 1) + ' dependants were not started'; exit = 1; }
    end = ap.end;
  }
  root.dur_us = Math.round(end + 2000);
  Object.assign(root.attrs = root.attrs || {}, { 'pravo.command': cmd, 'pravo.version': '0.9.0-dev', 'pravo.actor': 'ana@acme', 'pravo.reason': opts.reason || '', 'pravo.json': false, 'pravo.exit_code': exit });
  if (exit === 1) { root.status = 'error'; root.message = 'apply failed: api.db'; }
  spans.sort((a, b) => a.start_us - b.start_us || b.dur_us - a.dur_us);
  const errors = spans.filter((x) => x.status === 'error').length;
  const res = new Set(spans.filter((x) => x.name.startsWith('resource ')).map((x) => x.attrs['pravo.resource.address']));
  for (const x of spans) x.kind = x.name.startsWith('pravo ') ? 'command' : x.name.split(' ')[0];
  const id = traceId + '-' + root.id;
  const info = { id, command: cmd, start, duration_us: root.dur_us, exit, status: errors ? 'error' : 'ok', span_count: spans.length, errors, resources: res.size };
  if (txn) info.txn = txn;
  info.actor = 'ana@acme';
  if (opts.reason) info.reason = opts.reason;
  return { info, doc: { version: 1, ...info, trace_id: traceId, spans } };
}

// resourcesOf lists the resources of the graph in an order that respects the dependencies, with the cost of each.
function resourcesOf(g) {
  const nodes = g.nodes.filter((x) => x.mode === 'managed' || x.mode === 'data');
  const set = new Set(nodes.map((x) => x.addr));
  const deps = new Map(nodes.map((x) => [x.addr, []]));
  for (const e of g.edges) if (set.has(e.dependency) && set.has(e.dependent)) deps.get(e.dependent).push(e.dependency);
  const out = [], seen = new Set();
  const visit = (a) => { if (seen.has(a)) return; seen.add(a); deps.get(a).forEach(visit); out.push(a); };
  nodes.forEach((x) => visit(x.addr));
  const by = new Map(nodes.map((x) => [x.addr, x]));
  return out.map((a) => ({ addr: a, type: by.get(a).type, mode: by.get(a).mode, action: by.get(a).action, drifted: by.get(a).drifted, deps: deps.get(a), cost: COST[by.get(a).type] || DEFAULT }));
}

module.exports = { makeRun, resourcesOf, graph };

if (require.main === module) {
  const RUNS = [
    { name: 'apply', kind: 'apply', traceId: '5a1c0001' + '0'.repeat(24), start: '2026-10-21T14:01:12.318Z', seed: 11, txnTime: '140112', reason: 'raise the database to 15.7' },
    { name: 'plan', kind: 'plan', traceId: '5a1c0002' + '0'.repeat(24), start: '2026-10-21T13:48:40.902Z', seed: 12, reason: '' },
    { name: 'failed', kind: 'failed', traceId: '5a1c0003' + '0'.repeat(24), start: '2026-10-21T11:20:05.115Z', seed: 13, txnTime: '112005', reason: 'move the database to 15.9' },
    { name: 'drift', kind: 'drift', traceId: '5a1c0004' + '0'.repeat(24), start: '2026-10-20T22:00:01.470Z', seed: 14, reason: '' },
  ];
  const list = [];
  for (const o of RUNS) {
    const { info, doc } = makeRun(o);
    fs.writeFileSync(path.join(__dirname, 'fixtures', `trace.${o.traceId.slice(0, 8)}.json`), JSON.stringify(doc) + '\n');
    list.push(info);
  }
  fs.writeFileSync(path.join(__dirname, 'fixtures', 'traces.platform.json'), JSON.stringify({ version: 1, dir: '.pravo/traces', traces: list }, null, 1) + '\n');
  console.log(list.map((x) => `${x.id.slice(0, 8)} ${x.command} ${x.span_count} spans ${(x.duration_us / 1e6).toFixed(1)}s ${x.status}`).join('\n'));
}
