// The view is in the URL fragment: #/?sel=ADDR&tab=state&collapse=A,B&expand=C&theme=dark&q=TEXT
// &plan=plan-2f9c1a4e&view=graph|list|state|plans|discovery|traces&trace=ID&span=ID&tv=table&tz=START-END&tq=TEXT&motion=off&zoom=fit. Values are URL-encoded and lists are comma separated.

export function parseFragment(hash) {
  let t = (hash || '').replace(/^#/, '');
  t = t.replace(/^\/?\??/, '');
  const out = {};
  const raw = {};
  for (const part of t.split('&')) {
    if (!part) continue;
    const i = part.indexOf('=');
    const k = dec(i < 0 ? part : part.slice(0, i));
    const v = i < 0 ? '' : part.slice(i + 1);
    raw[k] = v;
    out[k] = dec(v.replace(/\+/g, '%20'));
  }
  Object.defineProperty(out, '__raw', { value: raw });
  return out;
}

function dec(s) { try { return decodeURIComponent(s); } catch { return s; } }

// a list is comma separated and each item is URL-encoded, so a comma inside an item travels as %2C:
// the list is read from the undecoded value
export function listOf(parsed, key) {
  const v = parsed.__raw && parsed.__raw[key];
  return v ? v.split(',').map((x) => dec(x)).filter(Boolean) : [];
}
export const joinList = (arr) => arr.map((x) => encodeURIComponent(x)).join(',');

// writeFragment returns the fragment for a view. Empty values are left out.
export function writeFragment(v) {
  const parts = [];
  const put = (k, val) => { if (val !== undefined && val !== null && val !== '') parts.push(k + '=' + encodeURIComponent(val)); };
  put('sel', v.sel);
  put('tab', v.tab);
  if (v.collapse && v.collapse.length) parts.push('collapse=' + joinList(v.collapse));
  if (v.expand && v.expand.length) parts.push('expand=' + joinList(v.expand));
  put('plan', v.plan);
  put('q', v.q);
  put('dq', v.dq);
  put('plq', v.plq);
  put('view', v.view === 'list' ? 'list' : v.view === 'access' ? 'state' : v.view === 'plans' ? 'plans' : v.view === 'discovery' ? 'discovery' : v.view === 'traces' ? 'traces' : '');
  put('trace', v.trace);
  put('span', v.span);
  put('tv', v.tv);
  put('tz', v.tz);
  put('tq', v.tq);
  put('theme', v.theme);
  put('motion', v.motion === 'off' ? 'off' : '');
  put('zoom', v.zoom && v.zoom !== 'fit' ? v.zoom : '');
  put('palette', v.palette ? '1' : '');
  put('pq', v.pq);
  put('help', v.help ? '1' : '');
  return '#/?' + parts.join('&');
}
