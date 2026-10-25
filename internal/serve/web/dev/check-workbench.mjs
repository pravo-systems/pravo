// Checks the small functions of the policy workbench that need no browser: the declaration that a rule becomes, and the index of
// the change that a path of a refusal points into.
//
//   node check-workbench.mjs

import { asPolicy, pathChange } from '../assets/workbench.js';

let failed = 0;
const check = (ok, msg) => { if (!ok) { failed++; console.log('FAIL', msg); } };

check(pathChange('changes.12.after.tags') === 12, 'a path into a change names its index');
check(pathChange('changes.3') === 3, 'the path of a change itself names it');
check(pathChange('summary.destroy') === -1 && pathChange('') === -1 && pathChange('changes') === -1 && pathChange('changesx.1') === -1, 'any other path names no change');

const want = 'import "pravo.dev/pravo"\n\nno_destroy: pravo.#Policy & {\n\trule: {\n\t\tsummary: destroy: 0\n\t}\n}\n';
check(asPolicy('no_destroy', 'summary: destroy: 0\n') === want, 'a rule is declared as a policy: ' + JSON.stringify(asPolicy('no_destroy', 'summary: destroy: 0\n')));
check(asPolicy('rules/no destroy', 'a: 1').includes('"rules/no destroy": pravo.#Policy'), 'a name that is not an identifier is quoted');
check(asPolicy('x', 'a: {\n\tb: 1\n}\n\nc: 2\n').includes('\t\ta: {\n\t\t\tb: 1\n\t\t}\n\n\t\tc: 2\n'), 'the lines of the rule are indented and the blank ones are not');

if (failed) { console.log(failed + ' failed'); process.exit(1); }
console.log('ok');
