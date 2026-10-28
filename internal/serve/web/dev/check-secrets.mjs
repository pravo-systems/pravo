// Checks the Secrets view and the top bar that holds its tab, in headless Chrome, on the fixtures:
//
//   - the bar fits at every width, with seven sections: no scroll of its own, the controls on the right in view, the
//     words of the tabs where there is room (all of them, then the open one only, then none) and every tab named;
//   - each secrets fixture draws what it says: the issues, the all-clear line, the table, the badge of the tab (a
//     number only when something needs action);
//   - the page does not scroll sideways at a phone's width or a desktop's;
//   - a chip shows its node in the graph, the key `e` and the palette open the section, a button opens the State page;
//   - the keys: every control of the view is a stop of Tab, with a ring that is seen and has a name.
//
//   node check-secrets.mjs
//
// It needs Chrome (the CHROME variable names it) and skips itself, with exit status 0, when it finds none.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { Browser, findChrome, serve, FOCUS } from './cdp-driver.mjs';

// FOCUS reads the stop of the Access view; the same reading, for this view
const FOCUS_HERE = FOCUS.replace("closest('#access-pane')", "closest('#secrets-pane')");

const chrome = findChrome();
if (!chrome) { console.log('skipped: no Chrome (set CHROME)'); process.exit(0); }

const here = path.dirname(fileURLToPath(import.meta.url));
const fixture = (name) => JSON.parse(fs.readFileSync(path.join(here, 'fixtures', `secrets.${name}.json`), 'utf8'));

let failed = 0;
const check = (ok, msg) => { if (!ok) { failed++; console.log('FAIL', msg); } return ok; };
const wait = (ms) => new Promise((r) => setTimeout(r, ms));

const srv = await serve();
const b = await Browser.launch(chrome, 1440, 900);
// the page is loaded afresh each time: n makes the address differ in its query, because a change of the fragment alone does not reload
let loads = 0;
const url = (name, view, extra = '') => `http://127.0.0.1:${srv.port}/index.html?mock=platform${name ? '&secrets=' + name : ''}&n=${++loads}#/?${view ? 'view=' + view + '&' : ''}motion=off${extra}`;
try {
  await b.init();

  // ---- the bar ----
  await b.size(1440, 900);
  await b.load(url('platform', ''));
  const bar = () => b.eval(`(() => {
    const top = document.querySelector('.top');
    const tabs = [...document.querySelectorAll('.sec-tab')].map((t) => {
      const l = t.querySelector('.lbl').getBoundingClientRect();
      const r = t.getBoundingClientRect();
      return { id: t.id, pressed: t.getAttribute('aria-pressed') === 'true', word: l.width > 2, w: Math.round(r.width), name: t.textContent.replace(/\\s+/g, ' ').trim() };
    });
    const theme = document.getElementById('theme-btn').getBoundingClientRect();
    return { vw: innerWidth, over: top.scrollWidth - top.clientWidth, right: Math.round(theme.right), tabs };
  })()`);
  const WIDTHS = [360, 390, 480, 600, 640, 760, 900, 1000, 1100, 1280, 1366, 1440, 1536, 1920];
  for (const w of WIDTHS) {
    await b.size(w, 800);
    await wait(120);
    const r = await bar();
    check(r.over <= 1, `${w}: the bar scrolls by ${r.over}px`);
    check(r.right <= w, `${w}: the theme button ends at ${r.right}, past the edge`);
    check(r.tabs.length === 7 && r.tabs.every((t) => t.w > 10), `${w}: seven tabs, each with a width: ${r.tabs.map((t) => t.w)}`);
    check(r.tabs.every((t) => t.name.length >= 4), `${w}: a tab has no name: ${r.tabs.map((t) => t.name)}`);
    const words = r.tabs.filter((t) => t.word).length;
    if (w >= 1441) check(words === 7, `${w}: every tab shows its word (${words})`);
    else if (w >= 993 && w <= 1408) check(words === 1 && r.tabs.find((t) => t.word).pressed, `${w}: only the open tab shows its word (${words})`);
    else if (w <= 992) check(words === 0, `${w}: no tab shows a word (${words})`);
  }
  // the badge of a tab does not widen it on a phone: it sits over the mark
  await b.size(390, 800);
  await wait(120);
  check(await b.eval(`(() => { const c = document.getElementById('sec-sec-count'); const t = document.getElementById('sec-secrets'); if (c.hidden) return false; const a = c.getBoundingClientRect(), r = t.getBoundingClientRect(); return getComputedStyle(c).position === 'absolute' && a.right <= r.right + 2 && a.top >= r.top; })()`), 'on a phone the count sits over the mark of its tab');
  // every tab names itself for the mouse when it shows no word
  check(await b.eval(`[...document.querySelectorAll('.sec-tab')].every((t) => t.dataset.tip && t.dataset.key)`), 'every tab has a tip with its key');

  // ---- each fixture ----
  const cases = ['platform', 'clear', 'empty', 'unchecked', 'unsealed', 'off'];
  for (const name of cases) {
    const d = fixture(name);
    for (const w of [1250, 375]) {
      await b.size(w, 900);
      await b.load(url(name, 'secrets'));
      const tag = `${name}/${w}`;
      const r = await b.eval(`(() => {
        const pane = document.getElementById('secrets-pane');
        const cnt = document.getElementById('sec-sec-count');
        return {
          view: window.__pravo.S.view, shown: !pane.hidden, pressed: document.getElementById('sec-secrets').getAttribute('aria-pressed'),
          badge: cnt.hidden ? null : cnt.textContent,
          title: pane.querySelector('.se-title') ? pane.querySelector('.se-title').firstChild.textContent : null,
          issues: pane.querySelectorAll('.ac-issue').length, clear: !!pane.querySelector('.ac-clearline'), clearText: pane.querySelector('.ac-clearline') ? pane.querySelector('.ac-clearline').textContent : '',
          rows: pane.querySelectorAll('.se-table tbody tr').length, none: !!pane.querySelector('.se-none'),
          page: document.documentElement.scrollWidth - innerWidth, pane: pane.scrollWidth - pane.clientWidth,
          errors: (window.__errors || []).length,
        };
      })()`);
      check(r.view === 'secrets' && r.shown && r.pressed === 'true', `${tag}: the section is open (${r.view}, ${r.shown}, ${r.pressed})`);
      check(r.title === 'Secrets', `${tag}: the title is ${r.title}`);
      check(r.badge === (d.problems > 0 ? String(d.problems) : null), `${tag}: the badge is ${r.badge}, the document counts ${d.problems} problems`);
      check(r.issues === d.issues.length, `${tag}: ${r.issues} issues drawn, ${d.issues.length} in the document`);
      check(r.clear === !!d.all_clear && (!d.all_clear || r.clearText.includes(d.all_clear)), `${tag}: the all-clear line is ${r.clear}`);
      check(r.rows === (d.sealed ? d.secrets.length : 0), `${tag}: ${r.rows} rows for ${d.secrets.length} secrets`);
      check(r.none === (!d.issues.length && !d.secrets.length), `${tag}: the empty state is ${r.none}`);
      check(r.page <= 1 && r.pane <= 1, `${tag}: the page scrolls sideways by ${r.page}px, the section by ${r.pane}px`);
      check(r.errors === 0, `${tag}: the page logged ${r.errors} errors`);
    }
  }

  // the rows say the same as the document: name, version and who, in the order of the document
  {
    const d = fixture('platform');
    await b.size(1250, 900);
    await b.load(url('platform', 'secrets'));
    const rows = await b.eval(`[...document.querySelectorAll('#secrets-pane .se-table tbody tr')].map((tr) => [...tr.children].map((c) => c.textContent.replace(/\\s+/g, ' ').trim()))`);
    check(rows.map((r) => r[0].replace(/removed$/, '')).join() === d.secrets.map((e) => e.name).join(), `the rows are in the order of the document: ${rows.map((r) => r[0])}`);
    check(rows.every((r, i) => r[1] === 'v' + d.secrets[i].version), `the versions: ${rows.map((r) => r[1])}`);
    check(/removed/.test(rows[0][0]) && /not trusted/.test(rows[2][2]), 'a removed secret and a version that is not trusted are marked');
    check(rows[3][4] === '–', `a secret that nothing reads says so: ${rows[3][4]}`);
    // what the page shows is the page's own words and the document's: nothing of a value exists in the document, and nothing is added
    const text = await b.eval(`document.getElementById('secrets-pane').innerText`);
    check(!/value[:=]/i.test(text), 'the page shows no "value:" line');
  }

  // ---- the ways in and out ----
  {
    await b.size(1250, 900);
    await b.load(url('platform', ''));
    await b.key('e');
    await wait(300);
    check(await b.eval('window.__pravo.S.view') === 'secrets', 'the key e opens the Secrets section');
    await b.key('e');
    await wait(300);
    check(await b.eval('window.__pravo.S.view') === 'graph', 'the key e again goes back to the resources');
    await b.eval(`window.__pravo.palette.open('secrets')`);
    await wait(300);
    const rows = await b.eval(`[...document.querySelectorAll('#pal-list [role=option] .pal-t')].map((e) => e.textContent)`);
    check(rows.includes('Go to Secrets'), `the palette offers the section: ${rows}`);
    await b.key('Escape');
    await wait(200);
    // a chip shows the node it names in the graph
    await b.load(url('platform', 'secrets'));
    const addr = await b.eval(`document.querySelector('#secrets-pane .se-chip code').textContent`);
    await b.eval(`document.querySelector('#secrets-pane .se-chip').click()`);
    await wait(500);
    check(await b.eval('window.__pravo.S.view') === 'graph' && await b.eval('window.__pravo.S.sel') === addr, `a chip shows ${addr} in the graph: ${await b.eval('window.__pravo.S.view')} ${await b.eval('window.__pravo.S.sel')}`);
    // the State page is where a state is sealed
    await b.load(url('unsealed', 'secrets'));
    await b.eval(`document.querySelector('#secrets-pane .btn').click()`);
    await wait(400);
    check(await b.eval('window.__pravo.S.view') === 'access', `the button opens the State page: ${await b.eval('window.__pravo.S.view')}`);
  }

  // ---- the keys ----
  for (const name of ['platform', 'clear', 'empty', 'unsealed']) {
    for (const theme of ['dark', 'light']) {
      await b.size(1250, 900);
      await b.load(url(name, 'secrets', `&theme=${theme}`));
      const want = await b.eval(`[...document.querySelectorAll('#secrets-pane button')].filter((e) => e.getClientRects().length).length`);
      await b.eval(`document.getElementById('secrets-pane').focus(); true`);
      const stops = [];
      for (let i = 0; i < want + 2; i++) {
        await b.key('Tab');
        const f = await b.eval(FOCUS_HERE);
        if (!f || !f.inPane) break;
        stops.push(f);
      }
      const tag = `${name}/${theme}`;
      check(stops.length === want, `${tag}: ${stops.length} stops of Tab for ${want} buttons`);
      check(stops.every((s) => s.ring && (s.contrast === null || s.contrast >= 3)), `${tag}: a stop has no ring, or a faint one: ${stops.filter((s) => !s.ring || s.contrast < 3).map((s) => s.fk)}`);
      check(stops.every((s) => !s.clipped), `${tag}: a ring is clipped: ${stops.filter((s) => s.clipped).map((s) => s.fk)}`);
      const names = await b.eval(`[...document.querySelectorAll('#secrets-pane button')].filter((e) => e.getClientRects().length).map((e) => (e.getAttribute('aria-label') || e.textContent || e.title || '').trim())`);
      check(names.every((n) => n.length > 1), `${tag}: a button has no name: ${names}`);
    }
  }
} finally {
  await b.close();
  srv.close();
}
if (failed) { console.log(`${failed} failed`); process.exit(1); }
console.log('ok');
