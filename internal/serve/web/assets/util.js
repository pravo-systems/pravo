// Small helpers shared by the modules of the page.

export const $ = (sel, root = document) => root.querySelector(sel);
export const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

const SVGNS = 'http://www.w3.org/2000/svg';

// h builds an element. props: class, style, text, on: {event: fn}, data: {k: v}, any other key is an
// attribute (true sets an empty attribute; false, null and undefined are skipped) except the properties
// value, checked, disabled, hidden and tabIndex, which are set directly.
export function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  apply(el, props);
  add(el, kids);
  return el;
}

// s builds an SVG element.
export function s(tag, props, ...kids) {
  const el = document.createElementNS(SVGNS, tag);
  apply(el, props);
  add(el, kids);
  return el;
}

const DIRECT = new Set(['value', 'checked', 'disabled', 'hidden', 'tabIndex']);
function apply(el, props) {
  if (!props) return;
  for (const k in props) {
    const v = props[k];
    if (v === undefined || v === null || v === false) continue;
    if (k === 'class') el.setAttribute('class', v);
    else if (k === 'text') el.textContent = v;
    else if (k === 'on') for (const ev in v) el.addEventListener(ev, v[ev]);
    else if (k === 'data') for (const d in v) el.dataset[d] = v[d];
    else if (DIRECT.has(k)) el[k] = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
}
function add(el, kids) {
  for (const k of kids) {
    if (k === null || k === undefined || k === false) continue;
    if (Array.isArray(k)) add(el, k);
    else el.append(k.nodeType ? k : document.createTextNode(String(k)));
  }
}

// softBreaks lets a long identifier wrap after its underscores, dots and slashes instead of in the middle of a word.
export const softBreaks = (str) => String(str).split(/(?<=[_./-])/).flatMap((part, i) => (i ? [document.createElement('wbr'), part] : [part]));

export const clear = (el) => { while (el.firstChild) el.removeChild(el.firstChild); return el; };
export const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));
export const plural = (n, one, many) => `${n} ${n === 1 ? one : many || one + 's'}`;

// the site's easing, cubic-bezier(.16, 1, .3, 1), as a function
export const ease = (() => {
  const x1 = 0.16, y1 = 1, x2 = 0.3, y2 = 1;
  const cx = 3 * x1, bx = 3 * (x2 - x1) - cx, ax = 1 - cx - bx;
  const cy = 3 * y1, by = 3 * (y2 - y1) - cy, ay = 1 - cy - by;
  const sx = (t) => ((ax * t + bx) * t + cx) * t;
  const sy = (t) => ((ay * t + by) * t + cy) * t;
  return (x) => {
    if (x <= 0) return 0;
    if (x >= 1) return 1;
    let t = x;
    for (let i = 0; i < 6; i++) {
      const e = sx(t) - x;
      const d = (3 * ax * t + 2 * bx) * t + cx;
      if (Math.abs(e) < 1e-4 || Math.abs(d) < 1e-6) break;
      t -= e / d;
    }
    return sy(clamp(t, 0, 1));
  };
})();

export function debounce(fn, ms) {
  let t = 0;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

// once-per-frame batching: schedule(key, fn) runs fn in the next animation frame, once per key.
const pending = new Map();
export function schedule(key, fn) {
  if (pending.has(key)) return;
  pending.set(key, requestAnimationFrame(() => { pending.delete(key); fn(); }));
}

// ---- time ----
const pad = (n) => String(n).padStart(2, '0');
export function relTime(iso, now = Date.now()) {
  const t = Date.parse(iso);
  if (!t) return '';
  const s = Math.round((now - t) / 1000);
  if (s < -60) return 'in the future';
  if (s < 45) return 'just now';
  const m = Math.round(s / 60);
  if (m < 60) return `${m} min ago`;
  const hh = Math.round(m / 60);
  if (hh < 36) return `${hh} h ago`;
  const d = Math.round(hh / 24);
  if (d < 60) return `${d} d ago`;
  return `${Math.round(d / 30)} mo ago`;
}
// relUntil says how long it is to a time, or how long ago it was: "in 6 d", "2 d ago".
export function relUntil(iso, now = Date.now()) {
  const t = Date.parse(iso);
  if (!t) return '';
  const s = Math.round((t - now) / 1000);
  if (s <= 0) return relTime(iso, now);
  const m = Math.round(s / 60);
  if (m < 1) return 'in under a min';
  if (m < 60) return `in ${m} min`;
  const hh = Math.round(m / 60);
  if (hh < 36) return `in ${hh} h`;
  return `in ${Math.round(hh / 24)} d`;
}
export function clock(iso) {
  const d = new Date(iso);
  if (isNaN(d)) return '';
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}
export function stamp(iso) {
  const d = new Date(iso);
  if (isNaN(d)) return '';
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function copyText(text) {
  if (navigator.clipboard && navigator.clipboard.writeText) return navigator.clipboard.writeText(text).then(() => true, () => fallbackCopy(text));
  return Promise.resolve(fallbackCopy(text));
}
function fallbackCopy(text) {
  const t = h('textarea', { value: text, 'aria-hidden': 'true', style: 'position:fixed;opacity:0;top:0;left:0' });
  document.body.append(t);
  t.select();
  let ok = false;
  try { ok = document.execCommand('copy'); } catch { /* not allowed */ }
  t.remove();
  return ok;
}

// storage that never throws
export const store = {
  get(k) { try { return localStorage.getItem(k); } catch { return null; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch { /* private window or blocked */ } },
};

// icon returns a masked inline icon; the shapes are in app.css (.i-NAME).
export const icon = (name, cls) => h('span', { class: `i i-${name}${cls ? ' ' + cls : ''}`, 'aria-hidden': 'true' });

// the shape of a status square: a glyph for every action, so that colour is never the only carrier
export const ACTIONS = {
  create: { label: 'create', cls: 'a-create', glyph: 'M6 2.5v7M2.5 6h7' },
  update: { label: 'update', cls: 'a-update', glyph: 'M2 7.2C3 4.6 4.2 4.6 6 6.2s3 1.6 4-.9' },
  replace: { label: 'replace', cls: 'a-replace', glyph: 'M6 2.2v4M4 4.2h4M3.6 8.8h4.8' },
  delete: { label: 'delete', cls: 'a-delete', glyph: 'M2.8 6h6.4' },
  read: { label: 'read', cls: 'a-read', glyph: 'M6 4.2a1.8 1.8 0 1 0 .01 0z' },
  move: { label: 'move', cls: 'a-move', glyph: 'M2.5 6h6.5M6.6 3.6L9 6 6.6 8.4' },
  import: { label: 'import', cls: 'a-import', glyph: 'M6 2.2v6M3.6 6.2L6 8.6l2.4-2.4M3 10.2h6' },
  'no-op': { label: 'no change', cls: 'a-noop', glyph: '' },
};
export const actionOf = (a) => ACTIONS[a] || null;

// nodeMark is the small figure before a node in a list: the square of its plan action, or, for an output, the
// glyph of a value that leaves the root. An output has no action, and a plan never "does not read" it.
export const KINDS = { data: 'data source', ephemeral: 'ephemeral resource' };
export const kindClass = (n) => (KINDS[n.mode] ? ' k-' + n.mode : '');
export function nodeMark(n, hasPlan) {
  if (n.mode === 'output') return h('span', { class: 'i i-output omark', 'aria-hidden': 'true' });
  if (n.type === 'env' || n.type === 'secret_env') return h('span', { class: 'i i-env omark', 'aria-hidden': 'true' });
  const act = actionOf(n.action);
  return h('i', { class: 'sq' + kindClass(n) + (act ? ' ' + act.cls : '') + (hasPlan && !n.action ? ' skip' : '') });
}

// ---- motion ----
// motionOn is false under the fragment's motion=off and under prefers-reduced-motion: then nothing animates.
export function motionOn() {
  if (document.documentElement.getAttribute('data-motion') === 'off') return false;
  try { return !matchMedia('(prefers-reduced-motion: reduce)').matches; } catch { return true; }
}

// tick sets the number in el and, when motion is on and the number changed, counts up or down to it.
export function tick(el, to, ms = 520) {
  const prev = el.dataset.v === undefined ? NaN : Number(el.dataset.v);
  el.dataset.v = String(to);
  if (el._tick) { cancelAnimationFrame(el._tick); el._tick = 0; }
  if (!motionOn() || prev === to || to < 2 && isNaN(prev)) { el.textContent = fmt(to); return; }
  const from = isNaN(prev) ? 0 : prev;
  const t0 = performance.now();
  const step = (t) => {
    const k = Math.min(1, (t - t0) / ms);
    el.textContent = fmt(Math.round(from + (to - from) * ease(k)));
    el._tick = k < 1 ? requestAnimationFrame(step) : 0;
  };
  el._tick = requestAnimationFrame(step);
}

// ---- paths ----
// splitPath cuts a path in a head that may be truncated and a tail that is not: the last `keep` segments.
export function splitPath(p, keep = 1) {
  const s = String(p || '');
  const parts = s.split('/');
  if (parts.length <= keep) return { head: '', tail: s };
  const tail = parts.slice(-keep).join('/');
  const head = parts.slice(0, -keep).join('/') + '/';
  return { head, tail };
}
export function baseName(p) {
  const t = String(p || '').replace(/[\\/]+$/, '');
  const i = Math.max(t.lastIndexOf('/'), t.lastIndexOf('\\'));
  return i < 0 ? t : t.slice(i + 1);
}
// home shortens a path that starts with the user's home directory the way a shell does, when the path says so.
export function tilde(p) {
  const m = /^\/(?:Users|home)\/[^/]+(\/.*)?$/.exec(p || '');
  return m ? '~' + (m[1] || '') : p;
}
export const fmt = (n) => (typeof n === 'number' ? n.toLocaleString('en-US') : String(n));

// a copy button that says it copied: the icon turns into a check for a moment
export function copyButton(getText, opts = {}) {
  const b = h('button', { class: 'cpbtn' + (opts.cls ? ' ' + opts.cls : ''), type: 'button', 'aria-label': opts.label || 'Copy', 'data-tip': opts.tip || opts.label || 'Copy' });
  const ic = icon('copy');
  b.append(ic);
  if (opts.text) b.append(h('span', { class: 'cpt' }, opts.text));
  let t = 0;
  b.addEventListener('click', (ev) => {
    ev.stopPropagation();
    copyText(typeof getText === 'function' ? getText() : getText).then((ok) => {
      b.classList.toggle('done', ok);
      b.classList.toggle('failed', !ok);
      ic.className = 'i ' + (ok ? 'i-check' : 'i-x');
      if (opts.announce) opts.announce(ok ? (opts.done || 'Copied') : 'Could not copy');
      clearTimeout(t);
      t = setTimeout(() => { b.classList.remove('done', 'failed'); ic.className = 'i i-copy'; }, 1400);
    });
  });
  return b;
}

// pathParts cuts a path for display into [head, mid, name]: the name is the last segment, mid the one before
// it, and head the rest, which is the part that gives way when there is no room.
export function pathParts(text) {
  const t = String(text || '').replace(/\/+$/, '');
  const segs = t.split('/');
  const name = segs.pop();
  const mid = segs.length > 1 || (segs.length === 1 && segs[0] !== '') ? segs.pop() + '/' : '';
  return [segs.length ? segs.join('/') + '/' : '', mid, name];
}

// pathEl shows a path. The whole path is in the page as one text node (for a screen reader, for the tooltip, and
// for anything that rewrites text), and what the eye sees is drawn from it: the head dimmed and cut in the
// middle of the line when there is no room, the last two segments kept. When that text changes, it is drawn again.
export function pathEl(path, { shorten = true, keep = 2, cls = '' } = {}) {
  const src = h('span', { class: 'sr-only pe-src' }, path);
  const vis = h('span', { class: 'pe-vis', 'aria-hidden': 'true' });
  const draw = () => {
    const text = src.textContent;
    const { head, tail } = splitPath(shorten ? tilde(text) : text, keep);
    vis.replaceChildren(h('span', { class: 'pe-h' }, head), h('b', { class: 'pe-t' }, tail));
  };
  draw();
  if (typeof MutationObserver !== 'undefined') new MutationObserver(draw).observe(src, { characterData: true, childList: true, subtree: true });
  return h('span', { class: 'pe' + (cls ? ' ' + cls : '') }, src, vis);
}

// SIGNATURE says what the signatures of a transaction or a stored plan of a sealed state come to, as the readers
// file judges them.
const SIGNATURE = {
  verified: { tip: 'The signature verifies against a key in the readers file.' },
  key_not_in_file: { tip: 'Signed with a key that the readers file does not list: a member who has left, or a key that never was.' },
  role_changed: { tip: 'The key verifies, and the readers file now gives it a lesser role than this change needs: its role was lowered since it signed, or it never had it.' },
  invalid: { tip: 'The signature does not verify: it was changed, or made up.' },
  unsigned: { tip: 'There is no signature.' },
};

// signatureMark is the small mark that says who signed, or that nobody did, or that the signature is wrong.
export function signatureMark(status, signer, claimed) {
  const text = {
    verified: 'signed by ' + (signer || 'a reader'),
    key_not_in_file: claimed ? 'signed by ' + claimed + ', whose key is not in the readers file' : 'signer not in the readers file',
    role_changed: 'signed by ' + (signer || 'a reader') + ', whose role is lower than this change needs',
    invalid: 'signature does not verify',
  }[status] || 'unsigned';
  return h('span', { class: 'sigmark sig-' + status, title: (SIGNATURE[status] || SIGNATURE.unsigned).tip }, text);
}
