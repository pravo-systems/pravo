// Checks the small functions of the policy workbench that need no browser: the declaration that a rule becomes, and the index of
// the change that a path of a refusal points into.
//
//   node check-workbench.mjs

import { pathChange } from '../assets/workbench.js';
import { foldRanges, hiddenRows } from '../assets/fold.js';
import { tokenize } from '../assets/cue.js';

let failed = 0;
const check = (ok, msg) => { if (!ok) { failed++; console.log('FAIL', msg); } };

check(pathChange('changes.12.after.tags') === 12, 'a path into a change names its index');
check(pathChange('changes.3') === 3, 'the path of a change itself names it');
check(pathChange('summary.destroy') === -1 && pathChange('') === -1 && pathChange('changes') === -1 && pathChange('changesx.1') === -1, 'any other path names no change');

const src = 'a: {\n\tb: [\n\t\t1,\n\t]\n\tc: "{ not a block"\n\td: {x: 1}\n}\n// }\n'.split('\n');
const ends = foldRanges(tokenize(src));
check(ends.get(0) === 6 && ends.get(1) === 3 && ends.size === 2, 'blocks fold from the line that opens them to the line that closes them; brackets in strings, comments and one-line blocks do not: ' + JSON.stringify([...ends]));
const hid = hiddenRows(src.length, ends, new Set([0]));
check(hid.slice(0, 8).join() === 'false,true,true,true,true,true,true,false', 'a folded block hides the lines up to and with its closer: ' + hid.join());
check(hiddenRows(src.length, ends, new Set([1])).slice(0, 5).join() === 'false,false,true,true,false', 'a fold inside a block hides its own lines');

if (failed) { console.log(failed + ' failed'); process.exit(1); }
console.log('ok');
