// The client of the JSON contract in internal/serve/api.go. The page sends nothing but GET requests.

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

async function get(path) {
  let r;
  try { r = await fetch(path, { headers: { Accept: 'application/json' }, cache: 'no-store', credentials: 'same-origin' }); } catch { throw new Unreachable(); }
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
