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
    throw new Error(msg);
  }
  try { return await r.json(); } catch { throw new Error('the answer is not JSON'); }
}

export const graph = () => get('/api/graph');
export const detail = (addr) => get('/api/detail?addr=' + encodeURIComponent(addr));
export const source = (addr) => get('/api/source?addr=' + encodeURIComponent(addr));
export const search = (q) => get('/api/search?q=' + encodeURIComponent(q));
export const ping = () => get('/api/ping');

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
