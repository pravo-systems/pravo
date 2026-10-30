// The client of the JSON contract in internal/serve/api.go. The page sends GET requests, and two POSTs: a plan file dropped on the studio, which the server judges and writes nowhere, and a question to the chat, which only reads (README rule 12).

export class Unauthorized extends Error {
  constructor() { super('unauthorized'); this.name = 'Unauthorized'; }
}

// Unreachable is a request that got no answer at all: the server is gone, or the network is.
export class Unreachable extends Error {
  constructor() { super('the server did not answer'); this.name = 'Unreachable'; }
}

// onUnauthorized sets what runs when any request is refused for want of the token: the page then shows the gate.
let authHandler = null;
export function onUnauthorized(fn) { authHandler = fn; }

async function get(path, signal) {
  let r;
  try { r = await fetch(path, { headers: { Accept: 'application/json' }, cache: 'no-store', credentials: 'same-origin', signal }); } catch (err) { if (err && err.name === 'AbortError') throw err; throw new Unreachable(); }
  if (r.status === 401) { if (authHandler) authHandler(); throw new Unauthorized(); }
  if (!r.ok) {
    let msg = r.statusText || `status ${r.status}`;
    try { const j = await r.json(); if (j && j.error) msg = j.error; } catch { /* not JSON */ }
    throw Object.assign(new Error(msg), { status: r.status });
  }
  try { return await r.json(); } catch { throw new Error('the answer is not JSON'); }
}

// plan is the id of a stored plan, or 'none' for no plan at all; without it the plan is the one the graph has.
const withPlan = (plan) => (plan ? '&plan=' + encodeURIComponent(plan) : '');

export const graph = () => get('/api/graph');
export const detail = (addr, plan) => get('/api/detail?addr=' + encodeURIComponent(addr) + withPlan(plan));
export const source = (addr, plan) => get('/api/source?addr=' + encodeURIComponent(addr) + withPlan(plan));
export const search = (q) => get('/api/search?q=' + encodeURIComponent(q));
// fields lists the nodes whose recorded values match path=value; the server searches what the detail would send
export const fields = (q) => get('/api/fields?q=' + encodeURIComponent(q));
export const ping = () => get('/api/ping');
// plans lists the plans stored in the state; a server that does not know them has none.
export const plans = () => get('/api/plans').catch((err) => { if (err && err.status === 404) return { version: 1, revision: '', plans: [] }; throw err; });
// access is how the state is protected, who can read it and the commands that change that; a server that does not know it says the state is not sealed.
export const access = () => get('/api/access').catch((err) => { if (err && err.status === 404) return { version: 1, revision: '', state: 'off', format: 1, location: '', backend: 'local', summary: '', readers: [], file: { verdict: 'none', path: '', text: '', count: 0 }, records: { total: 0, sealed: 0, plain: 0, unreadable: 0 }, groups: [], recovery: { state: 'none', text: '' }, suggestions: [] }; throw err; });
// discovery is what the discoveries found, set against the state; a server that does not know them has none.
export const discovery = () => get('/api/discovery').catch((err) => { if (err && err.status === 404) return { version: 1, revision: '', spec_ok: true, unmanaged: 0, discoveries: [], command: 'pravo discover' }; throw err; });
// policies is the rules that every plan is judged by, with their signed versions, set against the specification; a server that does not know them has none.
export const policies = () => get('/api/policies').catch((err) => { if (err && err.status === 404) return { version: 1, revision: '', spec_ok: true, checked: false, in_force: 0, pending: 0, policies: [], command: 'pravo apply' }; throw err; });
// secrets is the secrets of the sealed state, in the clear: their names, versions and signers, never a value, set against the nodes that read them; a server that does not know them says the state keeps none.
export const secrets = () => get('/api/secrets').catch((err) => { if (err && err.status === 404) return { version: 1, revision: '', spec_ok: true, sealed: false, checked: false, secrets: [], issues: [], problems: 0 }; throw err; });
// templates is the templates of the specification with their inputs and instances; a server that does not know them has none.
export const templates = () => get('/api/templates').catch((err) => { if (err && err.status === 404) return { version: 1, revision: '', spec_ok: true, templates: [] }; throw err; });
// templateTry loads the specification with the code of a new instance laid over it in memory, and answers what CUE and the policies say. Nothing is written.
export const templateTry = (params, signal) => get('/api/templates/try?' + params.toString(), signal);
// templateRefs lists attributes of the resources of the estate that an input of a kind could read. It carries no value.
export const templateRefs = (kind, q) => get('/api/templates/refs?kind=' + encodeURIComponent(kind) + '&q=' + encodeURIComponent(q || ''));
// studio is what a rule can be tried on (the stored plans and the plan files of the project), the policies and examples to start from, and the schema of the plan document.
export const studio = () => get('/api/studio');
// tryRule holds the policies of a text against plans. Nothing is written: it is a GET, as every request of the page is.
export const tryRule = ({ rule, refs }, signal) => {
  const q = new URLSearchParams();
  q.set('rule', rule);
  for (const r of refs) q.append('ref', r);
  return get('/api/studio/try?' + q.toString(), signal);
};
// studioDoc is the document that a plan is judged by: its outline, and the JSON of the change i (or of the document without its changes).
export const studioDoc = (ref, i) => get('/api/studio/doc?ref=' + encodeURIComponent(ref) + (i === undefined ? '' : '&i=' + i));
// uploadPlan hands the server a plan file that was dropped on the studio; it answers with the entry of the plan for the list of plans. The file is kept in the server's memory only.
export async function uploadPlan(name, bytes) {
  let r;
  try { r = await fetch('/api/studio/plan?name=' + encodeURIComponent(name), { method: 'POST', headers: { 'Content-Type': 'application/octet-stream', Accept: 'application/json' }, body: bytes, credentials: 'same-origin' }); } catch { throw new Unreachable(); }
  if (r.status === 401) { if (authHandler) authHandler(); throw new Unauthorized(); }
  let j = null;
  try { j = await r.json(); } catch { /* not JSON */ }
  if (!r.ok) throw Object.assign(new Error((j && j.error) || r.statusText || `status ${r.status}`), { status: r.status });
  return j;
}
// traces lists the runs that pravo recorded in the project; a server that does not know them has none.
export const traces = () => get('/api/traces').catch((err) => { if (err && err.status === 404) return { version: 1, dir: '.pravo/traces', traces: [] }; throw err; });
// trace is the spans of one run.
export const trace = (id) => get('/api/trace?id=' + encodeURIComponent(id));
// adopt is what to do with one object of a discovery: the resource that adopts it, and commands to copy.
export const adopt = (discovery, key) => get('/api/adopt?discovery=' + encodeURIComponent(discovery) + '&key=' + encodeURIComponent(key));
// plan is a stored plan as an overlay of the graph: the plan, and the action of each node it ran.
export const plan = (id) => get('/api/plan?id=' + encodeURIComponent(id));

// events opens /api/events. onChange gets the Event of a "change"; onState gets "live", "reconnecting" or
// "offline". EventSource reconnects by itself unless the server answered with an error status.
export function events(onChange, onState) {
  let es = null;
  const open = () => {
    es = new EventSource('/api/events');
    es.addEventListener('open', () => onState('live'));
    es.addEventListener('change', (ev) => {
      let data = null;
      try { data = JSON.parse(ev.data); } catch { /* the page fetches the graph anyway */ }
      onChange(data);
    });
    es.addEventListener('error', () => {
      if (es.readyState === 2) onState('offline'); else onState('reconnecting');
    });
  };
  open();
  return { close() { if (es) es.close(); }, reopen() { if (es) es.close(); open(); } };
}

export const chatConfig = () => get('/api/chat/config').catch((err) => { if (err && err.status === 404) return { enabled: false }; throw err; });

// askChat sends the conversation and calls onEvent for each event of the answer: read, text, done or error. It resolves when the stream ends.
export async function askChat(messages, context, onEvent, signal) {
  let r;
  try { r = await fetch('/api/chat', { method: 'POST', headers: { 'Content-Type': 'application/json' }, cache: 'no-store', credentials: 'same-origin', body: JSON.stringify({ messages, context }), signal }); } catch (err) { if (err && err.name === 'AbortError') throw err; throw new Unreachable(); }
  if (r.status === 401) { if (authHandler) authHandler(); throw new Unauthorized(); }
  if (!r.ok) {
    let msg = r.statusText || `status ${r.status}`;
    try { const j = await r.json(); if (j && j.error) msg = j.error; } catch { /* not JSON */ }
    throw Object.assign(new Error(msg), { status: r.status });
  }
  const rd = r.body.getReader();
  const dec = new TextDecoder();
  let buf = '';
  for (;;) {
    const { value, done } = await rd.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let i;
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const block = buf.slice(0, i);
      buf = buf.slice(i + 2);
      if (block.startsWith('data: ')) { try { onEvent(JSON.parse(block.slice(6))); } catch { /* a broken event is skipped */ } }
    }
  }
}
