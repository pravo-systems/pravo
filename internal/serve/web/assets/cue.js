// A small tokenizer for CUE, enough to colour the lines of /api/source. It does not parse: it classifies.
//
// Token classes (the css class is tk-CLASS):
//   k keyword            d definition (#Name)      t predeclared type     b true, false, null
//   f call               v $provider               l field label          r reference
//   m member (selector)  s string                  x masked string        e interpolation \( )
//   n number             c comment                 o operator             q constraint (>=, =~, *)
//   p punctuation        a attribute (@tag(...))   "" white space

const KEYWORDS = new Set(['package', 'import', 'for', 'in', 'if', 'let', '_', 'div', 'mod', 'quo', 'rem', 'and', 'or', 'len', 'close']);
const CONSTS = new Set(['true', 'false', 'null']);
const TYPES = new Set(['string', 'number', 'int', 'float', 'bool', 'bytes', 'int8', 'int16', 'int32', 'int64', 'uint', 'uint8', 'uint16', 'uint32', 'uint64', 'rune', 'byte']);
const BUILTINS = new Set(['len', 'close', 'and', 'or', 'div', 'mod', 'quo', 'rem', 'error', 'regexp']);
const IDSTART = /[A-Za-z_$#À-￿]/;
const IDCHAR = /[A-Za-z0-9_$#À-￿]/;
const MERGE = new Set(['o', 's', 'c', '', 'p', 'q']);

// tokenize takes the lines of a block and returns one array of tokens for each line.
export function tokenize(lines) {
  const st = { ml: null };
  return lines.map((line) => lineTokens(line, st));
}

function lineTokens(line, st) {
  const out = [];
  const push = (t, text) => {
    if (!text) return;
    const last = out[out.length - 1];
    if (last && last.t === t && MERGE.has(t)) last.text += text;
    else out.push({ t, text });
  };
  let i = 0;
  if (st.ml) {
    const end = line.indexOf(st.ml);
    if (end < 0) { push('s', line); return out; }
    push('s', line.slice(0, end + st.ml.length));
    i = end + st.ml.length;
    st.ml = null;
  }
  scan(line, i, push, st, false);
  return out;
}

// scan reads tokens from i; with inner set it stops at the ")" that closes an interpolation.
function scan(line, i, push, st, inner) {
  const n = line.length;
  let depth = 0;
  while (i < n) {
    const c = line[i];
    if (c === ' ' || c === '\t') {
      let j = i;
      while (j < n && (line[j] === ' ' || line[j] === '\t')) j++;
      push('', line.slice(i, j));
      i = j;
      continue;
    }
    if (c === '/' && line[i + 1] === '/') { push('c', line.slice(i)); return n; }
    // strings: "..." '...' #"..."# and their triple forms
    const sm = /^(#*)("""|"|'''|')/.exec(line.slice(i, i + 12));
    if (sm) { i = scanString(line, i, sm, push, st); continue; }
    if (/[0-9]/.test(c) || (c === '.' && /[0-9]/.test(line[i + 1] || ''))) {
      const m = /^(0[xX][0-9a-fA-F_]+|0[bB][01_]+|0[oO][0-7_]+|[0-9][0-9_]*(\.[0-9_]*)?([eE][+-]?[0-9]+)?([KMGTP]i?)?|\.[0-9_]+([eE][+-]?[0-9]+)?)/.exec(line.slice(i));
      if (m) { push('n', m[0]); i += m[0].length; continue; }
    }
    if (c === '@') {
      // an attribute: @name(...) on one line
      const m = /^@[A-Za-z_][A-Za-z0-9_]*(\([^)]*\))?/.exec(line.slice(i));
      if (m) { push('a', m[0]); i += m[0].length; continue; }
    }
    if (IDSTART.test(c)) {
      let j = i + 1;
      while (j < n && IDCHAR.test(line[j])) j++;
      const w = line.slice(i, j);
      let k = j;
      while (k < n && (line[k] === ' ' || line[k] === '\t')) k++;
      const next = line[k];
      const isLabel = next === ':' || (next === '?' && line[k + 1] === ':') || (next === '!' && line[k + 1] === ':');
      const afterDot = line[i - 1] === '.' && line[i - 2] !== '.' && !/[0-9]/.test(line[i - 2] || '');
      let t = 'r';
      if (w[0] === '$') t = 'v';
      else if (w[0] === '#' || w.startsWith('_#')) t = 'd';
      else if (afterDot && line[j] === '(') t = 'f';
      else if (afterDot) t = isLabel ? 'l' : 'm';
      else if (TYPES.has(w)) t = 't';
      else if (CONSTS.has(w) && !(isLabel && w !== 'null')) t = 'b';
      else if (BUILTINS.has(w) && line[j] === '(') t = 'f';
      else if (KEYWORDS.has(w) && !isLabel) t = 'k';
      else if (line[j] === '(') t = 'f';
      else if (isLabel) t = 'l';
      push(t, w);
      i = j;
      continue;
    }
    if (c === '(') { depth++; push('p', c); i++; continue; }
    if (c === ')') {
      if (inner && depth === 0) return i;
      depth--; push('p', c); i++; continue;
    }
    if (c === '.' && line[i + 1] === '.' && line[i + 2] === '.') { push('p', '...'); i += 3; continue; }
    if ('{}[],.'.includes(c)) { push('p', c); i++; continue; }
    const two = line.slice(i, i + 2);
    if (two === '>=' || two === '<=' || two === '!=' || two === '=~' || two === '!~' || two === '==') { push('q', two); i += 2; continue; }
    if (c === '<' || c === '>' || c === '*') { push('q', c); i++; continue; }
    if (c === '?' || c === '!') { push('p', c); i++; continue; }
    if (':&|=+-/%@'.includes(c)) { push('o', c); i++; continue; }
    push('', c);
    i++;
  }
  return i;
}

// scanString reads a string literal starting at i (m is the match of its opening) and returns the index after it.
function scanString(line, i, m, push, st) {
  const hashes = m[1], q = m[2];
  const close = q + hashes;
  const n = line.length;
  const triple = q.length === 3;
  let j = i + m[0].length;
  let buf = line.slice(i, j);
  const esc = '\\' + hashes;
  while (j < n) {
    if (line.startsWith(close, j)) {
      const whole = buf + close;
      push(/•••/.test(whole) ? 'x' : 's', whole);
      return j + close.length;
    }
    if (line.startsWith(esc + '(', j)) {
      // an interpolation: the code inside is coloured as code
      push('s', buf);
      buf = '';
      push('e', esc + '(');
      j = scan(line, j + esc.length + 1, push, st, true);
      if (j < n) { push('e', ')'); j++; }
      continue;
    }
    if (line.startsWith(esc, j) && j + esc.length < n) { buf += line.slice(j, j + esc.length + 1); j += esc.length + 1; continue; }
    buf += line[j];
    j++;
  }
  push('s', buf);
  if (triple) st.ml = close;
  return n;
}

// jsonTokens classifies the lines of pretty-printed JSON: k key, s string, n number, b true false null, p punctuation.
export function jsonTokens(lines) {
  const re = /("(?:[^"\\]|\\.)*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false|null)\b|([{}[\],:])|(\s+)|(.)/g;
  return lines.map((line) => {
    const out = [];
    const push = (t, text) => { const last = out[out.length - 1]; if (last && last.t === t && (t === '' || t === 'p')) last.text += text; else out.push({ t, text }); };
    let m;
    re.lastIndex = 0;
    while ((m = re.exec(line))) {
      if (m[1] !== undefined) { push(m[2] ? 'l' : 's', m[1]); if (m[2]) push('p', m[2]); } else if (m[3] !== undefined) push('n', m[3]);
      else if (m[4] !== undefined) push('b', m[4]);
      else if (m[5] !== undefined) push('p', m[5]);
      else push('', m[0]);
    }
    return out;
  });
}
