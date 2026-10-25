// Checks the completion of the policy editor against a small schema, without a browser.
//
//   node check-complete.mjs

import { complete } from '../assets/complete.js';

let failed = 0;
const check = (ok, msg) => { if (!ok) { failed++; console.log('FAIL', msg); } };

const fields = [
  { name: 'summary', type: 'struct', doc: 'Counts of actions.', fields: [{ name: 'add', type: 'int' }, { name: 'destroy', type: 'int' }] },
  { name: 'changes', type: 'list', list: true, doc: 'One entry for each node.', fields: [
    { name: 'addr', type: 'string' },
    { name: 'action', type: 'enum', enum: ['create', 'update', 'delete'] },
    { name: 'after', type: 'struct', optional: true },
  ] },
  { name: 'providers', type: 'struct' },
];
const at = (text) => { const i = text.indexOf('|'); return complete(fields, text.replace('|', ''), i); };
const labels = (r) => (r ? r.items.map((x) => x.label) : []);
const head = 'import "pravo.dev/pravo"\n\n';

check(labels(at(head + 'p: pravo.#Policy & {\n\trule: {\n\t\tsu|\n\t}\n}\n')).join() === 'summary', 'a field of the plan document, from the first letters');
check(labels(at(head + 'p: pravo.#Policy & {\n\trule: summary: de|\n}\n')).join() === 'destroy', 'a field after a chain of labels');
check(labels(at(head + 'p: pravo.#Policy & {\n\trule: summary: {\n\t\t|\n\t}\n}\n')) .length === 0, 'nothing is offered on an empty line unless asked');
check(labels(complete(fields, head + 'p: pravo.#Policy & {\n\trule: {\n\t\t\n\t}\n}\n', head.length + 'p: pravo.#Policy & {\n\trule: {\n\t\t'.length, true)).includes('changes'), 'asked for, the fields of the rule are offered');
let r = at(head + 'p: pravo.#Policy & {\n\trule: {\n\t\tchanges: [...{\n\t\t\tac|\n\t\t}]\n\t}\n}\n');
check(labels(r).join() === 'action', 'the fields of an element of changes: ' + labels(r));
check(r && r.items.find((x) => x.label === 'action').insert === 'action: string', 'inside an element a field is redeclared with its type: ' + (r && r.items.find((x) => x.label === 'action').insert));
r = at(head + 'p: pravo.#Policy & {\n\trule: {\n\t\tchanges: [...{\n\t\t\taction: "|\n\t\t}]\n\t}\n}\n');
check(labels(r).join() === 'create,update,delete' && r.items[0].insert === 'create"', 'the values of an enum, inside a quote: ' + JSON.stringify(r));
r = at(head + 'p: pravo.#Policy & {\n\trule: {\n\t\tchanges: [...{\n\t\t\taction: cr|\n\t\t}]\n\t}\n}\n');
check(labels(r).join() === 'create' && r.items[0].insert === '"create"', 'a value that is typed without a quote gets its quotes');
r = at('x: pravo.|');
check(labels(r).join() === '#Policy,#Plan,#Change,#Action', 'the names of the pravo package');
r = at('x: pravo.#Po|');
check(labels(r).join() === '#Policy' && r.from === 'x: pravo.'.length, 'a name of the package, from its first letters');
r = at(head + 'p: pravo.#Policy & {\n\t|\n}\n');
check(r === null, 'nothing unasked on a blank line of a policy');
r = complete(fields, head + 'p: pravo.#Policy & {\n\tr\n}\n', (head + 'p: pravo.#Policy & {\n\tr').length);
check(labels(r)[0] === 'rule', 'rule in the body of a policy');
r = at(head + 'po|');
check(labels(r).join() === 'policy', 'a policy to begin a file');
r = at(head + 'p: pravo.#Policy & {\n\trule: {\n\t\t// su|\n\t}\n}\n');
check(r === null, 'nothing is offered in a comment');
r = at(head + 'p: pravo.#Policy & {\n\trule: {\n\t\tchanges: [...{\n\t\t\taction: string\n\t\t\tif action == "delete" {\n\t\t\t\taf|\n\t\t\t}\n\t\t}]\n\t}\n}\n');
check(labels(r).join() === 'after', 'inside a guard the fields of the element are offered: ' + labels(r));

if (failed) { console.log(failed + ' failed'); process.exit(1); }
console.log('ok');
