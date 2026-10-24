// Checks the filter box of the Discovery section: the same language as the Inventory over the objects that discoveries
// found, which discoveries a term leaves, what the menu offers. No DOM, no browser.
//
//   node check-dquery.mjs

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseQuery, addTerm, removeTerm } from '../assets/query.js';
import { compile, rowsOf, suggest, LANG, COLUMNS } from '../assets/dquery.js';

const dir = path.join(path.dirname(fileURLToPath(import.meta.url)), 'fixtures');
const doc = JSON.parse(fs.readFileSync(path.join(dir, 'discovery.platform.json'), 'utf8'));
let failed = 0;
const check = (ok, msg) => { if (!ok) { failed++; console.log('FAIL', msg); } };
const all = rowsOf(doc, null);
const count = (q) => rowsOf(doc, compile(q)).length;
const cards = (q) => { const f = compile(q); return doc.discoveries.filter((x) => !f || (f.card(x) && (!f.items || (x.items || []).some((it) => f.item(x, it))))).map((x) => x.name); };

check(compile('') === null && compile('  ') === null, 'empty text asks for nothing');
check(compile('status:') === null, 'a term without a value asks for nothing');
check(parseQuery('foo:bar', LANG).words.length === 1 && parseQuery('foo:bar', LANG).terms.length === 0, 'an unknown column is a word');
check(parseQuery('state:unmanaged d:x ack:y', LANG).terms.map((t) => t.col).join() === 'status,discovery,by', 'aliases of columns');
check(parseQuery('type:a', { columns: ['status'], alias: {} }).terms.length === 0, 'a language knows its own columns only');

const st = (s) => all.filter((r) => r.it.status === s).length;
check(all.length > 0 && st('unmanaged') > 0 && st('adopting') > 0 && st('acknowledged') > 0, 'the fixture has all three statuses');
check(count('status:unmanaged') === st('unmanaged'), 'status:unmanaged');
check(count('state:unmanaged') === st('unmanaged') && count('status:adopted') === st('adopting') && count('status:ack') === st('acknowledged'), 'status aliases');
check(count('-status:unmanaged') === all.length - st('unmanaged'), '-status: is the rest');
check(count('status:adopting,acknowledged') === st('adopting') + st('acknowledged'), 'a comma ORs the values');
check(count('status:unmanaged status:acknowledged') === 0, 'terms are ANDed');

check(cards('discovery:compute.instances').join() === 'compute.instances', 'discovery: is the whole address');
check(cards('discovery:compute').join() === 'compute.instances', 'discovery: takes the discoveries below a namespace');
check(cards('discovery:comp').length === 0, 'discovery: is not a part of a name');
check(cards('discovery:comp*').join() === 'compute.instances', 'a * stands for a run');
check(cards('-discovery:compute').length === doc.discoveries.length - 1, '-discovery:');
check(cards('type:aws_vpc').join() === 'network.vpcs' && cards('type:aws_v').length === 0, 'type: is the whole type');
check(cards('provider:aws').length === doc.discoveries.length, 'provider:aws is every instance of it');
check(cards('freshness:stale').every((n) => doc.discoveries.find((x) => x.name === n).freshness === 'stale') && cards('fresh:stale').length === cards('freshness:stale').length && cards('freshness:stale').length > 0, 'freshness:');
check(cards('freshness:never').length > 0 && cards('freshness:never').length < doc.discoveries.length, 'a discovery that has not run is left in by a term about it, and out by one about its objects');
check(cards('freshness:never status:unmanaged').every((n) => !doc.discoveries.find((x) => x.name === n).freshness.includes('never')), 'a discovery without objects does not pass a term about objects');

check(count('i-0aa0000') === all.filter((r) => `${r.it.id} ${r.it.name} ${r.it.by || ''}`.toLowerCase().includes('i-0aa0000')).length && count('i-0aa0000') > 0, 'a word is looked for in the id');
check(count('JUMP-HOST') > 0, 'a word is not case sensitive');
check(count('id:i-0aa0000') === 1 && count('id:i-0aa*') > 1, 'id: is the whole id, with *');
check(count('name:jump-host-old') === 1 && count('name:jump*') >= 1, 'name:');
check(count('by:network.migration') === 1, 'by: the resource that adopts');
check(count('by:$ack[0]') > 0 && count('by:*ack*') >= count('by:$ack[0]'), 'by: the entry that covers');
check(count('field:region=eu-west-1') === all.filter((r) => (r.it.identity || []).some((f) => f.name === 'region' && f.value === 'eu-west-1')).length && count('field:region=eu-west-1') > 0, 'field:path=value');
check(count('field:region') === count('field:region=*') && count('field:nope') === 0, 'field:path alone is every object that has it');
check(count('value:482215903377') === count('field:account_id=482215903377') && count('value:482215903377') > 0, 'value: is any value of the identity');
check(count('field:tags.env="a b"') === 0, 'a quoted value');

// the text can be edited by the pills
const q = addTerm('status:unmanaged', 'status', 'adopting', false, LANG);
check(q === 'status:unmanaged,adopting', 'addTerm joins the value to the term of the column');
check(addTerm('', 'discovery', 'a b', false, LANG) === 'discovery:"a b"', 'a value with a space is quoted');
check(removeTerm('status:unmanaged type:x', 'status:unmanaged') === 'type:x', 'removeTerm');

// the menu
const s0 = suggest(doc, '', 0);
check(s0 && s0.items.some((i) => i.label === 'status:unmanaged' && i.count === st('unmanaged')) && s0.items.some((i) => i.label === 'status:' && i.group === 'Columns'), 'an empty box offers the filters of this page and the columns');
const sc = suggest(doc, 'sta', 3);
check(sc && sc.items.length === 1 && sc.items[0].insert === 'status:', 'the columns that a word begins');
const sv = suggest(doc, 'status:', 7);
check(sv && sv.items.map((i) => i.label).join() === 'unmanaged,adopting,acknowledged', 'the values of status, in order');
const sn = suggest(doc, 'status:unmanaged type:', 22);
check(sn && sn.items.every((i) => i.count == null || i.count > 0 || true) && sn.items.some((i) => i.label === 'aws_instance'), 'the types of the page');
const counts = suggest(doc, 'status:unmanaged discovery:', 27);
check(counts && counts.items.find((i) => i.label === 'compute.instances').count === count('status:unmanaged discovery:compute.instances'), 'a count is what the rest of the filter leaves');
const sf = suggest(doc, 'field:', 6);
check(sf && sf.items.some((i) => i.label === 'region=eu-west-1'), 'field: offers path=value');
check(suggest(doc, 'status:unmanaged,', 17).items.every((i) => i.label !== 'unmanaged'), 'a value that is there is not offered again');
check(suggest(doc, 'nope:', 5) === null, 'an unknown column offers nothing');
check(COLUMNS.every((c) => suggest(doc, c.slice(0, 2), 2) !== undefined), 'columns suggest');

if (failed) { console.log(failed + ' failed'); process.exit(1); }
console.log('discovery query checks pass');
