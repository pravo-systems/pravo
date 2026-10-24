// Checks the filter box: words, column:value terms, commas, negation, ranges, and what the menu offers.
// No DOM, no browser.
//
//   node check-query.mjs

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildModel } from '../assets/model.js';
import { parseQuery, compileQuery, fieldHits, fieldSpecs, fieldMatcher, suggest, addTerm, removeTerm, removeValues, entityOf, LEGACY_FILTERS } from '../assets/query.js';

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
check(count('refreshed:>100000d') === 0 && count('refreshed:<100000d') > 0, 'refreshed: counts the age');
check(count('API') === count('api'), 'a word has no case');
check(count('api db') <= count('api'), 'every word must match');

const at = (text) => suggest(model, text, text.length);
check(at('zz') === null, 'nothing to offer for a word that begins no column');
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

// the old filter= of a link is a term: each one matches what the filter of its name matched
import { FILTERS, isEnv } from '../assets/model.js';
for (const [key, f] of Object.entries(FILTERS)) {
  const { col, value } = LEGACY_FILTERS[key];
  // the resources of the old filter held the environment variables too, which are an entity of their own now
  const want = model.list.filter((n) => f(n) && !(key === 'resources' && isEnv(n))).length;
  check(count(addTerm('', col, value, false)) === want, `filter=${key} is ${col}:${value}: ${count(addTerm('', col, value, false))} of ${want}`);
}
check(Object.keys(FILTERS).every((k) => k in LEGACY_FILTERS), 'every old filter has a term');
check(count('entity:resource') + count('entity:data') + count('entity:ephemeral') + count('entity:output') + count('entity:env') === model.list.length, 'every node is one entity');
check(count('entity:datasource') === count('entity:data') && count('entity:environment') === count('entity:env') && count('entity:managed') === count('entity:resource'), 'the names of an entity');
check(count('-entity:output') === model.list.length - count('entity:output'), '-entity:output');
check(entityOf({ mode: 'managed', type: 'env' }) === 'env' && entityOf({ mode: 'data', type: 'x' }) === 'data' && entityOf({ mode: 'output', type: 'output' }) === 'output', 'entityOf');
const quick = at('');
check(quick && quick.items[0].group === 'Filters' && quick.items.some((i) => i.group === 'Columns' && i.insert === 'entity:'), 'an empty word offers the filters and the columns');
check(quick.items.filter((i) => i.group === 'Filters').every((i) => i.count > 0 && i.insert.endsWith(' ')), 'a filter that is offered has something to show');
check(!at('entity:resource ').items.some((i) => i.label === 'entity:resource'), 'a filter that is on is not offered again');
check(at('entity:').items.map((i) => i.label).join() === at('entity:').items.map((i) => i.label).sort((a, b) => ['resource', 'data', 'ephemeral', 'output', 'env'].indexOf(a) - ['resource', 'data', 'ephemeral', 'output', 'env'].indexOf(b)).join(), 'the entities come in their order');
check(at('en').items[0].insert === 'entity:', 'en offers entity:');

const inst = { addr: 'a', type: 'aws_lb', mode: 'managed', provider: 'aws.$awsPlatform', namespace: '' };
check(compileQuery('provider:aws')(inst) && compileQuery('provider:aws.$awsplatform')(inst) && !compileQuery('provider:aws.west')(inst) && !compileQuery('provider:platform')(inst), 'a provider is every instance of it, or one instance');

// a field term is answered by the server: what it has not answered matches nothing, and the answer decides after it
check(parseQuery('field:settings.region=eu').terms[0].col === 'field' && parseQuery('attr:a=b').terms[0].col === 'field', 'field and its aliases are a column');
check(fieldSpecs('field:a=b,c=d -field:e x:y field:a=b').join() === 'a=b,c=d,e', 'the specs of a text, once each');
check(fieldSpecs('value:arn:aws:x value:"a b"').join('|') === '*=arn:aws:x|*=a b', 'value: asks for the value at any path');
const anyv = compileQuery('value:abc*');
fieldHits.set('*=abc*', new Set([model.list[0].addr]));
check(anyv(model.list[0]) && !anyv(model.list[1]), 'a value term is answered like a field term');
fieldHits.clear();
const fl = model.list[0], other = model.list[1];
const ask = compileQuery('field:region=eu');
check(!ask(fl), 'a field term that is not answered matches nothing');
fieldHits.set('region=eu', new Set([fl.addr]));
check(ask(fl) && !ask(other) && compileQuery('-field:region=eu')(other) && !compileQuery('-field:region=eu')(fl), 'a field term matches the nodes that the server named, and the minus turns it round');
fieldHits.clear();

// the rows that a field or value term is about, by the rules of the server
const fm = fieldMatcher('field:settings.region=eu value:10.*');
check(fm('settings.region', 'EU') && !fm('settings.region', 'us') && fm('rule.0.cidr', '10.0.0.0/8') && !fm('rule.0.cidr', '192.168.0.1'), 'fieldMatcher: a field and a value');
check(fieldMatcher('region=eu mode:data') === null && fieldMatcher('-field:a=b') === null, 'fieldMatcher: nothing to show without a positive term');
check(fieldMatcher('field:zones=eu-1')('zones.1', 'eu-1') && fieldMatcher('field:region')('settings.region', 'x') && !fieldMatcher('field:regions')('settings.region', 'x'), 'fieldMatcher: below a path, a bare name at any depth');

// a double quote keeps a value together: a space or a comma inside it is part of the value
const q1 = parseQuery('field:tags.team="platform core" mode:data');
check(q1.terms.length === 2 && q1.terms[0].values.join('|') === 'tags.team=platform core' && q1.terms[0].raw === 'field:tags.team="platform core"', 'a quoted value holds a space');
check(parseQuery('field:note="a,b",x=y').terms[0].values.join('|') === 'note=a,b|x=y', 'a quoted comma is not a separator');
check(fieldSpecs('field:tags.team="platform core"').join() === 'tags.team=platform core', 'the spec has no quotes');
check(parseQuery('"two words" x').words.join('|') === 'two words|x', 'a quoted phrase is one word');
check(parseQuery('field:a="open').terms[0].values[0] === 'a=open', 'an unclosed quote holds to the end');
check(addTerm('', 'field', 'tags.team=platform core', false) === 'field:tags.team="platform core"', 'addTerm quotes the value of a field spec');
check(addTerm('field:tags.team="platform core"', 'field', 'tags.team=platform core', false) === 'field:tags.team="platform core"', 'a value that is there is not added twice');
check(addTerm('field:a=b', 'field', 'c=d e', false) === 'field:a=b,c="d e"', 'a quoted value joins a term');
check(removeTerm('mode:data field:a="b c" x', 'field:a="b c"') === 'mode:data x', 'removeTerm takes out a quoted term');
check(removeValues('field:a="b c",d=e', 'field', ['a=b c']) === 'field:d=e', 'removeValues takes out a quoted value');
check(addTerm('field:a="b c"', 'field', 'a=b c', true) === 'field:a="b c" -field:a="b c"' || addTerm('field:a="b c"', 'field', 'a=b c', true).includes('-field'), 'a hidden value goes in a term of its own');

if (failed) { console.log(`${failed} failed`); process.exit(1); }
console.log('query checks pass');
