// A small driver for headless Chrome over the DevTools protocol, for the checks that need real key events
// (Input.dispatchKeyEvent, which is what makes :focus-visible apply) and the accessibility tree of the
// browser. It serves ../ (the web directory) itself on a free port of the loopback interface. No dependency:
// Node's own WebSocket and http.
//
// Used by check-keyboard.mjs. A check that cannot find Chrome skips itself (see findChrome).

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import http from 'node:http';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const webDir = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');

export function findChrome() {
  const list = [process.env.CHROME, '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', '/usr/bin/google-chrome', '/usr/bin/chromium', '/usr/bin/chromium-browser'];
  return list.find((p) => p && fs.existsSync(p)) || null;
}

const TYPES = { '.html': 'text/html', '.js': 'text/javascript', '.mjs': 'text/javascript', '.css': 'text/css', '.json': 'application/json', '.woff2': 'font/woff2', '.svg': 'image/svg+xml' };

// serve answers files of the web directory; the mock reads its fixtures from dev/fixtures.
export function serve() {
  const srv = http.createServer((req, res) => {
    const p = decodeURIComponent(new URL(req.url, 'http://x').pathname);
    const f = path.join(webDir, p === '/' ? 'index.html' : p);
    if (!f.startsWith(webDir) || !fs.existsSync(f) || fs.statSync(f).isDirectory()) { res.writeHead(404); res.end(); return; }
    res.writeHead(200, { 'content-type': TYPES[path.extname(f)] || 'application/octet-stream', 'cache-control': 'no-store' });
    fs.createReadStream(f).pipe(res);
  });
  return new Promise((resolve) => srv.listen(0, '127.0.0.1', () => resolve({ port: srv.address().port, close: () => srv.close() })));
}

export class Browser {
  static async launch(chrome, w, h) {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'pravo-kbd-'));
    const proc = spawn(chrome, ['--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check', '--disable-extensions', '--disable-background-networking', '--remote-debugging-port=0', `--user-data-dir=${dir}`, `--window-size=${w},${h}`, 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe'] });
    process.on('exit', () => { try { proc.kill(); } catch { /* gone */ } }); // an exception must not leave a browser behind
    const port = await new Promise((resolve, reject) => {
      let buf = '';
      const t = setTimeout(() => reject(new Error('chrome did not listen within 30 s')), 30000);
      proc.stderr.on('data', (d) => { buf += d; const m = /DevTools listening on ws:\/\/127\.0\.0\.1:(\d+)\//.exec(buf); if (m) { clearTimeout(t); resolve(m[1]); } });
      proc.on('exit', () => reject(new Error('chrome exited before it listened')));
    });
    // the page of about:blank is not always listed the moment that the browser listens, least of all when several start at once
    let page;
    for (let i = 0; i < 100 && !page; i++) {
      page = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()).find((x) => x.type === 'page');
      if (!page) await new Promise((r) => setTimeout(r, 50));
    }
    if (!page) throw new Error('chrome listed no page');
    const b = new Browser(proc, dir, page.webSocketDebuggerUrl);
    await b.open();
    return b;
  }

  constructor(proc, dir, url) { this.proc = proc; this.dir = dir; this.url = url; this.id = 0; this.pending = new Map(); this.errors = []; }

  open() {
    return new Promise((resolve, reject) => {
      this.ws = new WebSocket(this.url);
      this.ws.onopen = () => resolve();
      this.ws.onerror = () => reject(new Error('websocket'));
      this.ws.onmessage = (ev) => {
        const m = JSON.parse(ev.data);
        if (m.id && this.pending.has(m.id)) { const [ok, bad] = this.pending.get(m.id); this.pending.delete(m.id); if (m.error) bad(new Error(m.error.message)); else ok(m.result); return; }
        if (m.method === 'Runtime.exceptionThrown') this.errors.push(m.params.exceptionDetails.exception?.description || m.params.exceptionDetails.text);
        if (m.method === 'Runtime.consoleAPICalled' && m.params.type === 'error') this.errors.push(m.params.args.map((a) => a.value ?? a.description).join(' '));
      };
    });
  }

  send(method, params = {}) {
    const id = ++this.id;
    return new Promise((ok, bad) => { this.pending.set(id, [ok, bad]); this.ws.send(JSON.stringify({ id, method, params })); });
  }

  async init() {
    await this.send('Runtime.enable');
    await this.send('Page.enable');
    await this.send('Accessibility.enable');
    await this.send('DOM.enable');
  }

  // size sets the viewport: a width under 768 also gets the mobile user agent of the page's own media queries.
  async size(w, h) {
    await this.send('Emulation.setDeviceMetricsOverride', { width: w, height: h, deviceScaleFactor: 1, mobile: false });
  }

  async eval(expression) {
    const r = await this.send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
    return r.result.value;
  }

  // load opens a page and waits until it has drawn its first view (the old page is marked, so that its
  // data-ready is not taken for the new page's)
  async load(url) {
    try { await this.eval('window.__old = true'); } catch { /* no page yet */ }
    await this.send('Page.navigate', { url });
    const ready = 'document.documentElement.dataset.ready === "1" && !window.__old';
    for (let i = 0; i < 200; i++) {
      await new Promise((r) => setTimeout(r, 50));
      try { if (await this.eval(ready)) break; } catch { /* navigating */ }
    }
    if (!(await this.eval(ready))) throw new Error('the page did not get ready: ' + url);
    await this.eval('document.activeElement && document.activeElement.blur(); window.scrollTo(0, 0); true');
  }

  async key(key, mods = 0) {
    const K = {
      Tab: { code: 'Tab', vk: 9 }, Enter: { code: 'Enter', vk: 13, text: '\r' }, ' ': { code: 'Space', vk: 32, text: ' ' }, Escape: { code: 'Escape', vk: 27 },
      ArrowDown: { code: 'ArrowDown', vk: 40 }, ArrowUp: { code: 'ArrowUp', vk: 38 }, ArrowLeft: { code: 'ArrowLeft', vk: 37 }, ArrowRight: { code: 'ArrowRight', vk: 39 },
      Home: { code: 'Home', vk: 36 }, End: { code: 'End', vk: 35 }, '/': { code: 'Slash', vk: 191, text: '/' }, k: { code: 'KeyK', vk: 75, text: 'k' },
    }[key];
    if (K.text && mods & 6) K.text = undefined; // a key with Ctrl or Alt held types nothing
    const base = { key, code: K.code, windowsVirtualKeyCode: K.vk, nativeVirtualKeyCode: K.vk, modifiers: mods };
    await this.send('Input.dispatchKeyEvent', { type: K.text ? 'keyDown' : 'rawKeyDown', ...base, text: K.text, unmodifiedText: K.text });
    await this.send('Input.dispatchKeyEvent', { type: 'keyUp', ...base });
    await new Promise((r) => setTimeout(r, 40));
  }

  // axOf is the node of the accessibility tree of the focused element: role, name, description and states.
  async axOfFocus() {
    const r = await this.send('Runtime.evaluate', { expression: 'document.activeElement' });
    if (!r.result.objectId) return null;
    const t = await this.send('Accessibility.getPartialAXTree', { objectId: r.result.objectId, fetchRelatives: false });
    const n = t.nodes && t.nodes[0];
    if (!n) return null;
    const props = {};
    for (const p of n.properties || []) props[p.name] = p.value.value;
    return { role: n.role?.value, name: n.name?.value || '', description: n.description?.value || '', ignored: !!n.ignored, props };
  }

  async fullTree() {
    const t = await this.send('Accessibility.getFullAXTree');
    return t.nodes;
  }

  async close() {
    try { this.ws.close(); } catch { /* gone */ }
    this.proc.kill();
    await new Promise((r) => { this.proc.on('exit', r); setTimeout(r, 3000); });
    try { fs.rmSync(this.dir, { recursive: true, force: true }); } catch { /* the profile is in the temporary directory */ }
  }
}

// FOCUS reads the focused element in the page: what it is, whether :focus-visible applies, the ring it draws and
// the colour behind the ring. Evaluated in the page; the result is a plain object.
export const FOCUS = `(() => {
  window.__kbn = window.__kbn || 0;
  const el = document.activeElement;
  if (!el || el === document.body || el === document.documentElement) return null;
  const parse = (s) => {
    let m = /^rgba?\\(([^)]+)\\)$/.exec(s);
    if (m) { const p = m[1].split(/[ ,\\/]+/).filter(Boolean).map(Number); return [p[0], p[1], p[2], p.length > 3 ? p[3] : 1]; }
    m = /^color\\(srgb ([^)]+)\\)$/.exec(s);
    if (m) { const p = m[1].split(/[ \\/]+/).filter(Boolean).map(Number); return [p[0] * 255, p[1] * 255, p[2] * 255, p.length > 3 ? p[3] : 1]; }
    return null;
  };
  const over = (top, bot) => { const a = top[3] + bot[3] * (1 - top[3]); return a ? [0, 1, 2].map((i) => (top[i] * top[3] + bot[i] * bot[3] * (1 - top[3])) / a).concat(a) : [0, 0, 0, 0]; };
  const lum = (c) => { const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); }; return 0.2126 * f(c[0]) + 0.7152 * f(c[1]) + 0.0722 * f(c[2]); };
  const ratio = (a, b) => { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
  const cs = getComputedStyle(el);
  // the ring: an outline, or failing that the box shadows that are not a drop shadow of the page (spread only)
  let ring = null;
  if (cs.outlineStyle !== 'none' && parseFloat(cs.outlineWidth) > 0) ring = { kind: 'outline', color: cs.outlineColor, width: parseFloat(cs.outlineWidth), offset: parseFloat(cs.outlineOffset) || 0 };
  else if (cs.boxShadow !== 'none') ring = { kind: 'box-shadow', color: (/(rgba?\\([^)]*\\)|color\\([^)]*\\))/.exec(cs.boxShadow) || [])[1], width: 0, offset: 0, raw: cs.boxShadow };
  // what is behind the ring: the backgrounds of the element's ancestors (the ring of a positive offset lies outside the element)
  const chain = [];
  for (let n = ring && ring.offset < 0 ? el : el.parentElement; n; n = n.parentElement) chain.push(n);
  let bg = [255, 255, 255, 0];
  const layers = [];
  for (const n of chain) { const c = parse(getComputedStyle(n).backgroundColor); if (c && c[3] > 0) { layers.push(c); if (c[3] >= 1) break; } }
  const root = parse(getComputedStyle(document.documentElement).backgroundColor);
  let acc = root && root[3] > 0 ? root : [255, 255, 255, 1];
  if (layers.length && layers[layers.length - 1][3] >= 1) acc = layers.pop();
  for (let i = layers.length - 1; i >= 0; i--) acc = over(layers[i], acc);
  bg = acc;
  let contrast = null;
  if (ring && ring.color) { const rc = parse(ring.color); if (rc) contrast = ratio(over(rc, bg), bg); }
  // clipped: the ring (the rectangle grown by offset and width) leaves the area of an ancestor that clips
  let clipped = false;
  if (ring && ring.kind === 'outline') {
    const g = ring.offset + ring.width, r = el.getBoundingClientRect();
    const box = { l: r.left - g, t: r.top - g, r: r.right + g, b: r.bottom + g };
    for (let n = el.parentElement; n && n !== document.body; n = n.parentElement) {
      const o = getComputedStyle(n);
      if (o.overflowX === 'visible' && o.overflowY === 'visible') continue;
      const a = n.getBoundingClientRect();
      if ((o.overflowX !== 'visible' && (box.l < a.left - 0.5 || box.r > a.right + 0.5)) || (o.overflowY !== 'visible' && (box.t < a.top - 0.5 || box.b > a.bottom + 0.5))) { clipped = (n.id || n.className || n.tagName) + ' ' + [Math.round(box.l - a.left), Math.round(a.right - box.r), Math.round(box.t - a.top), Math.round(a.bottom - box.b)].join(','); break; }
    }
  }
  const r = el.getBoundingClientRect();
  return {
    uid: (el.__kb = el.__kb || ++window.__kbn), tag: el.tagName.toLowerCase(), id: el.id || null, cls: String(el.className && el.className.baseVal !== undefined ? el.className.baseVal : el.className),
    fk: el.dataset ? el.dataset.fk || null : null, inPane: !!el.closest('#access-pane'),
    clipBy: null,
    focusVisible: el.matches(':focus-visible'),
    ring, contrast: contrast && Math.round(contrast * 100) / 100, clipped,
    expanded: el.getAttribute('aria-expanded'), detailsOpen: el.tagName === 'SUMMARY' ? el.parentElement.open : null,
    rect: [Math.round(r.left), Math.round(r.top), Math.round(r.width), Math.round(r.height)],
    docTop: Math.round(r.top + (document.getElementById('access-pane').scrollTop || 0)),
    inViewport: r.bottom > 0 && r.top < innerHeight && r.right > 0 && r.left < innerWidth,
    text: (el.getAttribute('aria-label') || el.textContent || '').trim().replace(/\\s+/g, ' ').slice(0, 80),
  };
})()`;

// reach lists the interactive elements of the Access pane that are visible and that a person could focus
export const REACHABLE = `(() => {
  const pane = document.getElementById('access-pane');
  const sel = 'a[href], button, input, select, textarea, summary, [tabindex]:not([tabindex="-1"]), [contenteditable=true]';
  return Array.from(pane.querySelectorAll(sel)).filter((e) => {
    if (e.disabled) return false;
    const r = e.getBoundingClientRect();
    if (!r.width && !r.height) return false;
    for (let n = e; n && n !== pane; n = n.parentElement) { const s = getComputedStyle(n); if (s.visibility === 'hidden' || s.display === 'none') return false; }
    for (let d = e.closest('details'); d; d = d.parentElement && d.parentElement.closest('details')) {
      if (!d.open && !(e.tagName === 'SUMMARY' && e.parentElement === d)) return false;
    }
    return true;
  }).map((e) => ({ tag: e.tagName.toLowerCase(), fk: e.dataset.fk || null, text: (e.getAttribute('aria-label') || e.textContent || '').trim().replace(/\\s+/g, ' ').slice(0, 80) }));
})()`;

// tabOrder presses Tab until the focus leaves the page or comes round to a stop that it has seen, and returns the
// stops with the accessibility node of each; then it presses Shift+Tab as many times and returns what that visited.
export const sig = (f) => `${f.tag}|${f.fk || ''}|${f.text}`;

export async function tabOrder(b, max = 150) {
  const stops = [];
  const seen = new Set();
  let wrapped = false;
  for (let i = 0; i < max; i++) {
    await b.key('Tab');
    const f = await b.eval(FOCUS);
    if (!f) { wrapped = true; break; }
    if (seen.has(f.uid)) { wrapped = true; break; }
    seen.add(f.uid);
    stops.push({ ...f, ax: await b.axOfFocus() });
  }
  // Shift+Tab from outside the page (the focus left it, or sits on the body) enters from the end, in one press or
  // two, depending on where the browser keeps the focus: leading presses that land nowhere are not counted
  const back = [];
  for (let i = 0, lost = 0; i < stops.length; ) {
    await b.key('Tab', 8);
    const f = await b.eval(FOCUS);
    if (!f && back.length === 0 && ++lost < 3) continue;
    back.push(f ? sig(f) : null);
    i++;
  }
  return { stops, back, wrapped };
}
