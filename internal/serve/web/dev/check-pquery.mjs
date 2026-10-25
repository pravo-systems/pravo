// Checks the filter box of the Plans section: the same language as the Inventory over the stored plans and the changes
// of each, which plans a term leaves, which of their changes it names, what the menu offers. No DOM, no browser.
//
//   node check-pquery.mjs

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseQuery, addTerm, removeTerm } from '../assets/query.js';
import { compile, rowsOf, suggest, holds, LANG, COLUMNS } from '../assets/pquery.js';

const dir = path.join(path.dirname(fileURLToPath(import.meta.url)), 'fixtures');
const read = (n) => JSON.parse(fs.readFileSync(path.join(dir, n), 'utf8'));
const graph = read('graph.platform.json');
const stored = read('plans.platform.json');
// the clock is the one of the fixture: the plans were made a few hours before it
const NOW = Date.parse('2026-10-21T14:02:11Z');
const reasons = new Map([['plan-7b04d1c9', 'rotate the database secret'], ['plan-2f9c1a4e', 'raise the database to m6g.large'], ['plan-5a2c9e71', 'retire the nat gateway and its route']]);
const env = { reasons, model: { nodes: new Map(graph.nodes.map((n) => [n.addr, n])) }, now: NOW };
const doc = { plans: stored.plans, env };
let failed = 0;
const check = (ok, msg) => { if (!ok) { failed++; console.log('FAIL', msg); } };
const ids = (q) => rowsOf(doc, compile(q)).map((r) => r.p.id.replace('plan-', '')).sort().join();
const hits = (q, id) => { const r = rowsOf(doc, compile(q)).find((x) => x.p.id === 'plan-' + id); return r && r.hits ? r.hits.map((c) => c.addr).join() : null; };
const by = (f) => stored.plans.filter(f).map((p) => p.id.replace('plan-', '')).sort().join();

check(compile('') === null && compile('  ') === null, 'empty text asks for nothing');
check(compile('status:') === null, 'a term without a value asks for nothing');
check(parseQuery('foo:bar', LANG).words.length === 1 && parseQuery('foo:bar', LANG).terms.length === 0, 'an unknown column is a word');
check(parseQuery('state:stale author:ben does:delete', LANG).terms.map((t) => t.col).join() === 'status,by,action', 'aliases of columns');
check(rowsOf(doc, null).length === stored.plans.length, 'no filter lists every plan');

// about the plan
check(ids('status:pending') === by((p) => p.status === 'pending'), 'status:pending');
check(ids('status:open') === by((p) => p.open) && by((p) => p.open).split(',').length === 4, 'status:open is every plan that can be applied');
check(ids('status:partly-stale') === by((p) => p.status === 'partly-stale') && ids('status:partly') === ids('status:partly-stale'), 'status:partly-stale and its short name');
check(ids('status:applied,expired') === by((p) => p.status === 'applied' || p.status === 'expired'), 'a comma ORs the values');
check(ids('-status:open') === by((p) => !p.open), '-status:open is the rest');
check(ids('by:ben@acme') === by((p) => p.author === 'ben@acme') && ids('by:*@acme') === by(() => true) && ids('by:ben') === '', 'by: is the whole author, with *');
check(ids('author:ana*') === by((p) => p.author.startsWith('ana')), 'author: is by:');
check(ids('id:plan-7b04d1c9') === '7b04d1c9' && ids('id:7b04') === '7b04d1c9' && ids('id:plan-7b') === '7b04d1c9', 'id: is the id or its first characters');
check(ids('reason:database') === '2f9c1a4e,7b04d1c9' && ids('reason:"nat gateway"') === '5a2c9e71' && ids('note:ROTATE') === '7b04d1c9', 'reason: is a part of the note');
check(ids('created:<1d') === by((p) => NOW - Date.parse(p.created) < 864e5) && ids('created:>7d') === 'e0a17c33', 'created: how long ago');
check(ids('expires:<1d') === '' && ids('expires:<7d') === by((p) => p.open && Date.parse(p.expires) - NOW < 7 * 864e5), 'expires: only a plan that can be applied lapses');
check(ids('overlap:any') === '2f9c1a4e,7b04d1c9' && ids('overlap:none') === by((p) => !(p.overlaps || []).length) && ids('overlap:plan-2f9c1a4e') === '7b04d1c9' && ids('overlap:2f9c1a4e') === '7b04d1c9', 'overlap:');

// words: the plan's own, then the changes'
check(ids('ben') === '7b04d1c9' && ids('rotate') === '7b04d1c9' && ids('STALE') === 'b86e2d14,c3e8a052', 'a word is looked for in the author, the reason and the status, whatever its case');
check(ids('edge.lb') === '7b04d1c9,c3e8a052' && hits('edge.lb', 'c3e8a052') === 'edge.lb', 'a word that no plan has is looked for in the addresses of its changes');
check(ids('aws_db_instance') === '2f9c1a4e,7b04d1c9', 'a word is looked for in the type of a change');
check(ids('database db') === '2f9c1a4e,7b04d1c9', 'every word must be found');

// about the changes
check(ids('action:create') === by((p) => p.touches.some((c) => c.action === 'create')) && hits('action:create', '7b04d1c9') === 'edge.dns', 'action:create names the change');
check(ids('does:update status:open') === by((p) => p.open && p.touches.some((c) => c.action === 'update')), 'a term about the plan and one about its changes');
check(ids('action:delete') === '' && ids('-action:update') === by((p) => p.touches.some((c) => c.action !== 'update')), '-action: leaves the plans that have a change of another action');
check(ids('resource:api.db') === '2f9c1a4e,7b04d1c9' && ids('resource:api') === '' && ids('address:api.worker*') === '2f9c1a4e,91d5f6b8', 'resource: is the whole address, with *');
check(ids('ns:network') === '5a2c9e71,e0a17c33' && ids('ns:api.worker') === '2f9c1a4e,91d5f6b8' && hits('ns:api.worker', '2f9c1a4e') === 'api.worker.service', 'ns: a namespace and everything below');
check(ids('type:aws_db_instance') === '2f9c1a4e,7b04d1c9' && hits('type:aws_db_instance', '7b04d1c9') === 'api.db' && ids('type:aws_db') === '', 'type: is the whole type of the resource');
check(ids('type:aws_lb*') === by((p) => p.touches.some((c) => /^edge\.lb$/.test(c.addr))), 'type: takes *');
check(ids('provider:aws') === by(() => true), 'provider:aws');
check(ids('change:conflict') === 'b86e2d14' && hits('change:conflict', 'b86e2d14') === 'api.policy,api.role', 'change:conflict lists the changes that cannot be applied');
check(ids('change:applied') === '5a2c9e71,91d5f6b8' && hits('change:applied', '5a2c9e71') === 'network.private.nat', 'change:applied');
check(hits('change:pending', '5a2c9e71') === 'network.private.rt' && ids('change:left') === ids('change:pending'), 'change:pending is what is left');
check(ids('action:update ns:api change:conflict') === 'b86e2d14', 'the terms about a change are all met by one change');
check(ids('action:create ns:network') === '', 'two terms about the changes need one change that has both');
check(hits('status:open', '7b04d1c9') === null, 'a plan that is not asked about its changes lists none');

// the menu
check(holds('status:open ns:api', 'ns', 'API') && !holds('-ns:api', 'ns', 'api') && !holds('ns:api', 'ns', 'edge'), 'holds says whether a term names a value');
const labels = (s) => (s ? s.items.map((i) => i.label) : []);
const q = suggest(doc, '', 0);
check(q && q.items.some((i) => i.label === 'status:open' && i.count === 4) && q.items.some((i) => i.group === 'Narrow by' && i.label === 'status:'), 'an empty word offers the filters and the columns');
check(labels(q).includes('overlap:any') && labels(q).includes('action:update') && labels(q).includes('ns:api'), 'an empty word offers what this page differs in');
check(labels(q).includes('change:conflict'), 'a conflict is worth finding');
check(!labels(suggest(doc, 'status:open ', 12)).includes('status:open'), 'a filter that is held is not offered again');
check(labels(suggest(doc, 'st', 2)).join() === 'status:', 'a part of a column offers the column');
check(labels(suggest(doc, 'status:', 7)).includes('open') && labels(suggest(doc, 'status:', 7)).includes('partly-stale'), 'a column offers its values');
check(labels(suggest(doc, 'by:b', 4)).join() === 'ben@acme', 'a part of a value narrows the values');
check(labels(suggest(doc, 'action:', 7)).join() === 'create,update', 'a column offers the values that the plans have');
check(labels(suggest(doc, 'status:open action:', 19)).join() === 'create,update', 'the values leave out what the rest of the filter excludes');
check(labels(suggest(doc, 'type:', 5)).includes('aws_db_instance') && labels(suggest(doc, 'ns:', 3)).includes('api.worker') && labels(suggest(doc, 'ns:', 3)).includes('network.private'), 'type: and ns: offer what the changes touch');
check(labels(suggest(doc, 'resource:api.d', 14)).join() === 'api.db', 'resource: offers the addresses that the plans change');
check(labels(suggest(doc, 'overlap:', 8))[0] === 'any', 'overlap: offers any first');
check(suggest(doc, 'foo:', 4) === null, 'an unknown column offers nothing');
const s = suggest(doc, 'status:open by:', 15);
check(s && s.from === 12 && s.to === 15, 'the menu says which part of the text it replaces');
check(COLUMNS.every((c) => LANG.columns.includes(c)), 'the language knows its columns');

// the text
check(addTerm('', 'action', 'update', false, LANG) === 'action:update' && addTerm('action:update', 'action', 'delete', false, LANG) === 'action:update,delete', 'a chip adds a term, or a value to the term of its column');
check(removeTerm('status:open ns:api', 'ns:api') === 'status:open', 'a pill takes a term out');

if (failed) { console.log(`${failed} failed`); process.exit(1); }
console.log('ok');
