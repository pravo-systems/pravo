// Completion for the policy editor. It reads the text up to the cursor, works out which struct the cursor is in (a
// policy, its rule, a field of the plan document, an element of changes) and offers what the plan document has there:
// the names of its fields with what the schema says of them, the values of an enum, and the names in the pravo package.
// It does not parse CUE; it follows the braces, which is enough to know where a field sits in the document.

// Names in the pravo package that a policy file uses.
const PRAVO = [
  { label: '#Policy', detail: 'definition', doc: 'A rule that every plan is judged by at apply. A policy is a pravo.#Policy with a rule.' },
  { label: '#Plan', detail: 'definition', doc: 'The document that a rule is judged against.' },
  { label: '#Change', detail: 'definition', doc: 'One entry of changes in the plan document.' },
  { label: '#Action', detail: 'definition', doc: 'What a plan does to a node: create, update, replace, delete and the rest.' },
];

const POLICY_FIELDS = [
  { name: 'rule', type: 'struct', doc: 'Standalone CUE, unified with the plan document, which it must make concrete.' },
  { name: 'description', type: 'string', optional: true, doc: 'What the policy is for.' },
];

const KEYWORDS = [
  { label: 'if', insert: 'if ', detail: 'guard', doc: 'Applies what is inside only when the condition holds, such as if action == "delete" { error(addr) }.' },
  { label: 'error', insert: 'error("")', cursorBack: 2, detail: 'builtin', doc: 'Puts your own words in the refusal.' },
  { label: 'for', insert: 'for ', detail: 'comprehension', doc: 'Repeats what is inside for each entry of a list or a struct.' },
  { label: 'let', insert: 'let ', detail: 'binding', doc: 'Names a value inside this struct.' },
];

const LABEL = /^\s*("(?:[^"\\]|\\.)*"|[A-Za-z_$#][\w$#]*)\??\s*:\s*/;

// scan walks the text up to the offset, skipping strings and comments, and keeps a stack of the structs that are open.
// A frame knows where it sits: path is the list of field names from the root of the plan document, or null when the
// struct is not inside a rule; policy says that it is the body of a pravo.#Policy.
function scan(text, offset) {
  const stack = [];
  let stmt = 0;
  let i = 0;
  while (i < offset) {
    const c = text[i];
    if (c === '/' && text[i + 1] === '/') { while (i < offset && text[i] !== '\n') i++; continue; }
    if (c === '"') {
      i++;
      while (i < offset && text[i] !== '"' && text[i] !== '\n') i += text[i] === '\\' ? 2 : 1;
      i++;
      continue;
    }
    if (c === '{') {
      stack.push(open(stack.length ? stack[stack.length - 1] : { path: null, policy: false }, text.slice(stmt, i)));
      stmt = i + 1;
    } else if (c === '}') {
      stack.pop();
      stmt = i + 1;
    } else if (c === ',' || c === '\n') {
      stmt = i + 1;
    }
    i++;
  }
  return { stack, stmt };
}

// open makes the frame of a struct that the text before its brace introduces.
function open(parent, head) {
  const { labels, rest } = labelsOf(head);
  const r = rest.trim();
  let path = parent.path;
  let policy = false;
  if (/^(if|for|let)\b/.test(r) && !labels.length) return { path, policy: false };
  for (const l of labels) {
    if (parent.policy && path === null && l === 'rule') path = [];
    else if (path !== null) path = path.concat(l);
  }
  if (/pravo\s*\.\s*#Policy/.test(r)) policy = true;
  // a list template: [...{ and [...pravo.#Change & {
  if (/^\[\s*\.\.\./.test(r) && path !== null) path = path.concat('[]');
  if (parent.policy && !labels.length && path === null) return { path: null, policy: false };
  return { path: parent.policy && labels.length === 0 ? parent.path : path, policy };
}

// labelsOf splits the start of a statement into the field names before its colons and what is left.
function labelsOf(s) {
  const labels = [];
  let rest = s;
  for (let m; (m = LABEL.exec(rest));) {
    let l = m[1];
    if (l[0] === '"') l = l.slice(1, -1);
    labels.push(l);
    rest = rest.slice(m[0].length);
  }
  return { labels, rest };
}

// find looks a path up in the tree of fields. '[]' steps into the elements of a list.
function find(fields, path) {
  let cur = { fields, list: false };
  for (const step of path) {
    if (step === '[]') { if (!cur.list) return null; continue; }
    const f = (cur.fields || []).find((x) => x.name === step);
    if (!f) return null;
    cur = f;
  }
  return cur;
}

const scalar = (f) => f.type !== 'struct' && f.type !== 'list' && f.type !== 'any';

// complete returns what can be typed at the offset: { from, to, items } where the text from..to is replaced by the
// insert of the item chosen, or null. force is true when the person asked for completion with the keyboard.
export function complete(fields, text, offset, force = false) {
  pickText = text;
  const { stack, stmt } = scan(text, offset);
  const head = text.slice(stmt, offset);
  const here = stack.length ? stack[stack.length - 1] : { path: null, policy: false };
  const { labels, rest } = labelsOf(head);
  const prefixMatch = /^(\s*)(?:"?)([\w$#]*)$/.exec(rest);

  // pravo.#Policy and its neighbours
  const pk = /(?:^|[^\w$#.])pravo\s*\.\s*(#?\w*)$/.exec(head);
  if (pk) return pick(PRAVO, pk[1], offset - pk[1].length, offset);

  if (!prefixMatch) return null;
  const word = prefixMatch[2];
  const from = offset - word.length;
  if (!word && !force && !labels.length) return null;
  const quoted = /"$/.test(rest.slice(0, rest.length - word.length));

  // where the labels typed so far on this line lead
  let path = here.path;
  let policyBody = here.policy;
  for (const l of labels) {
    if (policyBody && path === null && l === 'rule') path = [];
    else if (path !== null) path = path.concat(l);
    policyBody = false;
  }
  const typed = labels.length;

  // the file: a policy to begin with
  if (!stack.length && !typed) {
    const items = [];
    if (!/import\s+"pravo\.dev\/pravo"/.test(text)) items.push({ label: 'import "pravo.dev/pravo"', insert: 'import "pravo.dev/pravo"\n', detail: 'import', doc: 'The package that policies are written against.' });
    items.push({ label: 'policy', insert: 'name: pravo.#Policy & {\n\trule: {\n\t\t\n\t}\n}\n', cursorAt: 'name'.length, select: true, detail: 'snippet', doc: 'A policy: a name, and the rule that every plan is judged by.' });
    return pick(items, word, from, offset, true);
  }
  // the body of a policy
  if (here.policy && !typed) return pick(POLICY_FIELDS.map((f) => ({ label: f.name, insert: f.name + ': ', detail: f.type, doc: f.doc })), word, from, offset);
  if (path === null) return null;

  const node = find(fields, path);
  if (!node) return null;
  // a value: the choices of an enum, true and false
  if (typed && scalar(node)) {
    const vals = node.enum && node.enum.length ? node.enum : node.type === 'bool' ? ['true', 'false'] : null;
    if (!vals) return null;
    const q = node.type === 'bool' ? '' : '"';
    const items = vals.map((v) => ({ label: v, insert: quoted ? v + '"' : q + v + q, detail: node.name ? node.type : '', doc: '' }));
    return pick(items, word, from, offset);
  }
  // a field name
  const inElement = path[path.length - 1] === '[]';
  const items = (node.fields || []).map((f) => {
    let insert = f.name + ': ';
    if (f.list && f.fields && f.fields.length) insert = f.name + ': [...{\n\t\n}]';
    else if (inElement && scalar(f)) insert = f.name + ': ' + (f.enum && f.enum.length ? 'string' : f.type);
    return { label: f.name, insert, cursorAt: f.list && f.fields && f.fields.length ? f.name.length + ': [...{\n\t'.length : null, detail: (f.optional ? 'optional ' : '') + f.type, doc: f.doc };
  });
  if (!typed) for (const k of KEYWORDS) items.push(Object.assign({}, k));
  return pick(items, word, from, offset);
}

// pick keeps the items that the word starts, then those that contain it, and tells where the replacement goes.
// reindent makes the lines of a multi-line insert follow the indent of the line it is typed on.
function reindent(it, indent) {
  if (!indent || !it.insert.includes('\n')) return it;
  const out = Object.assign({}, it, { insert: it.insert.replace(/\n/g, '\n' + indent) });
  if (it.cursorAt != null) out.cursorAt = it.cursorAt + indent.length * (it.insert.slice(0, it.cursorAt).split('\n').length - 1);
  return out;
}

let pickText = '';
function pick(items, word, from, to, keepAll) {
  const w = word.toLowerCase();
  const starts = items.filter((x) => x.label.toLowerCase().startsWith(w));
  const has = items.filter((x) => !x.label.toLowerCase().startsWith(w) && x.label.toLowerCase().includes(w));
  const out = starts.concat(has);
  if (!out.length || (!keepAll && out.length === 1 && out[0].label === word)) return null;
  const line = (pickText || '').slice((pickText || '').lastIndexOf('\n', from - 1) + 1);
  const indent = /^[\t ]*/.exec(line)[0];
  return { from, to, items: out.map((x) => reindent(x, indent)) };
}
