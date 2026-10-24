// Checks the filter box: words, column:value terms, commas, negation, ranges, and what the menu offers.
// No DOM, no browser.
//
//   node check-query.mjs

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildModel } from '../assets/model.js';
import { parseQuery, compileQuery, suggest, addTerm, removeTerm } from '../assets/query.js';

const dir = path.join(path.dirname(fileURLToPath(import.meta.url)), 'fixtures');
const model = buildModel(JSON.parse(fs.readFileSync(path.join(dir, 'graph.platform.json'), 'utf8')));
let failed = 0;
const check = (ok, msg) => { if (!ok) { failed++; console.log('FAIL', msg); } };
const count = (q) => { const f = compileQuery(q); return f ? model.list.filter(f).length : model.list.length; };

check(compileQuery('') === null && compileQuery('   ') === null, 'empty text asks for nothing');
check(compileQuery('type:') === null && compileQuery('mode:,') === null, 'a term without a value asks for nothing');
check(parseQuery('foo:bar').words.length === 1 && parseQuery('foo:bar').terms.length === 0, 'an unknown column is a word');
check(parseQuery('action:create').terms[0].col === 'plan' && parseQuery('namespace:api').terms[0].col === 'ns', 'aliases of columns');

const data = count('mode:data');
check(data === model.list.filter((n) => n.mode === 'data').length && data > 0, `mode:data holds ${data}`);
check(count('-mode:data') === model.list.length - data, '-mode:data is the rest');
check(count('mode:data,output') === data + model.list.filter((n) => n.mode === 'output').length, 'a comma ORs the values');
check(count('mode:data type:aws') <= data, 'terms are ANDed');
const buckets = model.list.filter((n) => n.type === 'aws_s3_bucket').length;
check(count('type:aws_s3_bucket') === buckets, 'type: is the whole type, not the types that begin with it');
check(count('type:aws_s3*') === model.list.filter((n) => n.type.startsWith('aws_s3')).length && count('type:aws_s3*') >= buckets, 'a * stands for a run');
check(count('type:*bucket') === model.list.filter((n) => n.type.endsWith('bucket')).length, 'a * at the start');
check(count('type:aws') === 0, 'a part of a type is not a type');
check(count('provider:aws') === model.list.filter((n) => n.provider === 'aws').length, 'provider: aws');
check(count('provider:aws.west') === 0, 'an instance that is not there');
check(count('ns:api') === model.list.filter((n) => n.namespace === 'api' || (n.namespace || '').startsWith('api.')).length, 'ns: is a namespace and what is below it');
check(count('ns:ap') === 0, 'ns: is not a part of a name');
check(count('plan:create') === model.list.filter((n) => n.action === 'create').length && count('plan:create') > 0, 'plan:create');
check(count('status:drifted') === model.list.filter((n) => !n.mode.startsWith('output') && (n.drifted || n.gone)).length, 'status:drifted');
check(count('rev:>1') === model.list.filter((n) => n.mode !== 'output' && n.revision > 1).length, 'rev:>1');
check(count('rev:1') + count('rev:>1') + count('rev:<1') === model.list.filter((n) => n.mode !== 'output').length, 'rev:1, >1 and <1 split the resources');
check(count('refreshed:>100000d') === 0 && count('refreshed:<100000d') > 0, 'refreshed: counts the age');
check(count('API') === count('api'), 'a word has no case');
check(count('api db') <= count('api'), 'every word must match');

const at = (text) => suggest(model, text, text.length);
check(at('') === null && at('zz') === null, 'nothing to offer for nothing');
check(at('ty').items.map((i) => i.insert).join() === 'type:', 'ty offers type:');
check(at('-st').items[0].insert === '-status:', 'a minus is kept');
const vals = at('provider:').items.map((i) => i.label);
check(vals.includes('aws') && vals.length === new Set(vals).size, `provider: offers ${vals}`);
const second = at('mode:data,');
check(second.items.every((i) => i.label !== 'data') && second.items[0].insert.startsWith('mode:data,'), 'a value that is used is not offered again');
check(at('mode:managed') === null, 'nothing more to offer for a whole value');
check(at('mode:data ty').from === 10, 'the word at the caret is the one that counts');

check(addTerm('', 'type', 'aws_lb', false) === 'type:aws_lb', 'addTerm to nothing');
check(addTerm('api type:aws_lb', 'type', 'aws_subnet', false) === 'api type:aws_lb,aws_subnet', 'addTerm joins a term of the column');
check(addTerm('type:aws_lb', 'type', 'aws_lb', false) === 'type:aws_lb', 'addTerm does not repeat a value');
check(addTerm('mode:data', 'mode', 'managed', true) === 'mode:data -mode:managed', 'addTerm of the other sign is another term');
check(addTerm('mode:data,managed', 'mode', 'data', true) === 'mode:managed -mode:data', 'addTerm of the other sign takes the value out');
check(removeTerm('api mode:data type:x', 'mode:data') === 'api type:x', 'removeTerm');
check(parseQuery('api -mode:data').terms[0].raw === '-mode:data', 'a term remembers how it was written');
// the counts of the menu are of what the rest of the filter leaves
const ctx = suggest(model, 'ns:api type:', 12).items.reduce((a, i) => a + i.count, 0);
const all = suggest(model, 'type:', 5).items.reduce((a, i) => a + i.count, 0);
check(ctx > 0 && ctx < all, `the counts of type: follow ns:api (${ctx} of ${all})`);
const scoped = suggest(model, 'type:', 5, (n) => n.mode === 'data');
check(scoped === null || scoped.items.every((i) => i.count <= 2), 'the counts follow the chip');

const inst = { addr: 'a', type: 'aws_lb', mode: 'managed', provider: 'aws.$awsPlatform', namespace: '' };
check(compileQuery('provider:aws')(inst) && compileQuery('provider:aws.$awsplatform')(inst) && !compileQuery('provider:aws.west')(inst) && !compileQuery('provider:platform')(inst), 'a provider is every instance of it, or one instance');

if (failed) { console.log(`${failed} failed`); process.exit(1); }
console.log('query checks pass');
