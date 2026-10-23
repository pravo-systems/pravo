// Drives the Access view of the mock with real key events in headless Chrome (Input.dispatchKeyEvent, so that
// :focus-visible applies) and checks it as a person with a keyboard or a screen reader meets it, for every
// fixture, in both themes, at 1250 px and at 375 px:
//
//   - every interactive element of the view is a Tab stop, in the order of the page, and Shift+Tab visits the
//     same stops backwards; the focus comes round or leaves the page (no keyboard trap)
//   - each stop has :focus-visible, a ring of at least 3:1 against what is behind it, that no scroll container clips,
//     and an accessible name; copy buttons say what they copy
//   - a suggestion opens and closes with Enter and with Space and says so (expanded in the accessibility tree);
//     Escape leaves it where it was
//   - a copy announces what it copied in the live region
//   - the skip link moves the focus into the view, and the next Tab goes to the first stop of the view
//   - the accessibility tree has the verdict and the state of "You" as text, the tables with their header cells,
//     the marks with names, and the headings in order
//
//   node check-keyboard.mjs              all of it (about a minute)
//   node check-keyboard.mjs --quick      the platform and the drift fixtures only
//   node check-keyboard.mjs --fixtures migrating,locked
//   node check-keyboard.mjs --order drift 1250 light    print the focus order of one fixture
//
// It needs Chrome (the CHROME variable names it) and skips itself, with exit status 0, when it finds none.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { Browser, findChrome, serve, tabOrder, sig, FOCUS, REACHABLE } from './cdp-driver.mjs';

const dir = path.dirname(fileURLToPath(import.meta.url));
const FIXTURES = ['platform', 'off', 'immutable', 'drift', 'locked', 'notreader', 'missing', 'migrating', 'invalid', 'downgraded', 'solo'];
const fixture = (n) => JSON.parse(fs.readFileSync(path.join(dir, 'fixtures', `access.${n}.json`), 'utf8'));
const args = process.argv.slice(2);

const chrome = findChrome();
if (!chrome) { console.log('skipped: no Chrome (set CHROME)'); process.exit(0); }

let failed = 0;
let minRing = { light: 99, dark: 99 };
const check = (ok, msg) => { if (!ok) { failed++; console.log('FAIL', msg); } return ok; };

const srv = await serve();
const origin = `http://127.0.0.1:${srv.port}`;
const url = (f, theme, n) => `${origin}/index.html?mock=platform&access=${f}&r=${n}#/?view=access&theme=${theme}&motion=off`;
let nonce = 0;

async function open(width) {
  const b = await Browser.launch(chrome, width, 900);
  await b.init();
  await b.size(width, width < 500 ? 740 : 900);
  await b.send('Browser.grantPermissions', { origin, permissions: ['clipboardReadWrite', 'clipboardSanitizedWrite'] }).catch(() => {});
  return b;
}

const label = (st, map) => (map[st] || {}).label;
const VERDICT = { match: 'matches the store', differs: 'differs from the store', missing: 'no file', invalid: 'does not load', 'expected-sealed': 'state not sealed' };
const YOU = { reader: 'reader', 'not-reader': 'not a reader', 'no-identity': 'no identity', locked: 'identity locked' };

// the text of the accessibility tree under the pane, with roles
async function paneTree(b) {
  const nodes = await b.fullTree();
  const doc = await b.send('DOM.getDocument', { depth: 0 });
  const q = await b.send('DOM.querySelector', { nodeId: doc.root.nodeId, selector: '#access-pane' });
  const d = await b.send('DOM.describeNode', { nodeId: q.nodeId });
  const byId = new Map(nodes.map((n) => [n.nodeId, n]));
  const pane = nodes.find((n) => n.backendDOMNodeId === d.node.backendNodeId);
  const out = [];
  (function walk(n, depth) {
    if (!n) return;
    const role = n.role?.value;
    const props = {};
    for (const p of n.properties || []) props[p.name] = p.value.value;
    if (!n.ignored) out.push({ role, name: n.name?.value || '', props, depth });
    for (const c of n.childIds || []) walk(byId.get(c), depth + 1);
  })(pane, 0);
  return { pane, out };
}

async function checkOne(b, f, theme, width, report) {
  const tag = `${f}/${theme}/${width}`;
  const doc = fixture(f);
  await b.load(url(f, theme, ++nonce));
  const { stops, back, wrapped } = await tabOrder(b);
  const inPane = stops.filter((s) => s.inPane);
  if (report) {
    for (const [i, s] of stops.entries()) console.log(String(i).padStart(2), (s.ax?.role || s.tag).padEnd(18), JSON.stringify(s.ax?.name || '').slice(0, 96).padEnd(98), `ring ${s.contrast}:1`, s.ax?.props.expanded === undefined ? '' : `expanded=${s.ax.props.expanded}`);
  }

  // the stops: a ring, an accessible name, no clipping
  for (const s of stops) {
    const what = `${tag}: ${s.tag} "${s.text}"`;
    check(s.focusVisible, `${what} does not match :focus-visible after Tab`);
    if (s.contrast !== null) minRing[theme] = Math.min(minRing[theme], s.contrast);
    check(s.contrast !== null && s.contrast >= 3, `${what} has a focus ring of ${s.contrast}:1 against its background (3:1 is the least)`);
    check(!s.clipped, `${what} has a focus ring that is clipped by ${s.clipped}`);
    check(s.ax && s.ax.name.trim() !== '', `${what} has no accessible name`);
    check(s.inViewport, `${what} is not scrolled into view when it has the focus`);
  }
  check(stops.length > 0 && stops.length < 150, `${tag}: ${stops.length} stops`);
  check(wrapped, `${tag}: the focus neither came round nor left the page within ${stops.length} stops (a trap?)`);
  check(back.length === stops.length && back.every((u, i) => u === sig(stops[stops.length - 1 - i])), `${tag}: Shift+Tab does not visit the stops backwards`);

  // every interactive element of the view is one of the stops, and the stops follow the page
  const reach = await b.eval(REACHABLE);
  const keys = inPane.map((s) => s.fk || s.text);
  for (const r of reach) check(keys.includes(r.fk || r.text), `${tag}: ${r.tag} "${r.text}" is not a Tab stop`);
  const order = await b.eval(`(() => { const all = Array.from(document.querySelectorAll('#access-pane [data-fk]')); return all.map((e) => e.dataset.fk); })()`);
  const seq = inPane.map((s) => s.fk).filter(Boolean);
  check(JSON.stringify(seq) === JSON.stringify(order.filter((k) => seq.includes(k))), `${tag}: the Tab order is not the order of the page`);
  for (const s of inPane) {
    if (s.tag === 'button' && s.fk === 'single') check(/^Add a backup reader/.test(s.ax?.name || ''), `${tag}: the warning button is named "${s.ax?.name}"`);
    else if (s.tag === 'button') check(/^Copy /.test(s.ax?.name || ''), `${tag}: the button "${s.ax?.name}" does not say what it copies`);
    if (s.tag === 'summary') {
      check(s.ax?.props.expanded !== undefined, `${tag}: the suggestion "${s.ax?.name}" does not say whether it is open`);
      const sug = (doc.suggestions || []).find((x) => 'sum:' + x.id === s.fk);
      if (sug) check(s.ax.name.startsWith(sug.title) && (!sug.who || s.ax.name.endsWith(', ' + sug.who)), `${tag}: the name of the suggestion runs its parts together: "${s.ax.name}"`);
    }
  }

  // the Tab order is the reading order: down the page (a row of the stacked table has its stops left to right)
  const visual = inPane.every((s, i) => i === 0 || s.docTop >= inPane[i - 1].docTop - 2 || (s.rect[0] > inPane[i - 1].rect[0] && Math.abs(s.docTop - inPane[i - 1].docTop) < 8));
  check(visual, `${tag}: the Tab order goes up the page: ${inPane.map((s) => s.docTop).join(',')}`);

  // no horizontal scroll, of the page or of the view or of the table (at 375 px the rows are stacked)
  const over = await b.eval(`(() => { const p = document.getElementById('access-pane'); const w = p.querySelector('.ac-tablewrap'); return [document.documentElement.scrollWidth - innerWidth, p.scrollWidth - p.clientWidth, w ? w.scrollWidth - w.clientWidth : 0]; })()`);
  check(over.every((x) => x <= 1), `${tag}: the page, the view or the table scrolls sideways (${over.join(', ')} px too wide)`);

  // what the accessibility tree says
  const { pane, out } = await paneTree(b);
  const texts = out.filter((n) => n.role === 'StaticText').map((n) => n.name.toLowerCase());
  const has = (t) => texts.some((x) => x.includes(t.toLowerCase()));
  check(pane && out.some((n) => n.role === 'heading' && n.name === 'State'), `${tag}: no heading State in the view`);
  if (doc.you && doc.you.state === 'reader') {
    // a reader is named in the bar, not in the view
    const who = await b.eval(`(() => { const w = document.getElementById('who'); return w && !w.hidden ? w.getAttribute('aria-label') : null; })()`);
    check(who && who.includes(YOU.reader), `${tag}: the bar does not name the reader (${who})`);
  } else if (doc.you) check(has(doc.you.text.slice(0, 20)) && has(YOU[doc.you.state]), `${tag}: the state of You (${YOU[doc.you.state]}) is not in the accessibility tree`);
  const sealed = doc.state === 'sealed' || doc.state === 'migrating' || doc.file.verdict === 'expected-sealed';
  if (sealed && VERDICT[doc.file.verdict]) check(has(VERDICT[doc.file.verdict]), `${tag}: the verdict "${VERDICT[doc.file.verdict]}" is not in the accessibility tree`);
  const bare = doc.state !== 'sealed' && doc.state !== 'migrating' && !(doc.readers || []).length && doc.file.verdict === 'none';
  if (!bare) check(has(doc.file.text.slice(0, 30)), `${tag}: the sentence of the verdict is not in the accessibility tree`);
  if (doc.migration) {
    const pb = out.find((n) => n.role === 'progressbar');
    check(pb && /records (un)?sealed$/.test(pb.name), `${tag}: the progress of a seal is not a named progress bar in the accessibility tree`);
  }
  const levels = out.filter((n) => n.role === 'heading').map((n) => n.props.level);
  check(levels.every((l, i) => i === 0 || l - levels[i - 1] <= 1), `${tag}: the headings skip a level: ${levels.join(',')}`);
  const rows = (doc.readers || []).length;
  if (rows) {
    check(out.some((n) => n.role === 'table'), `${tag}: the readers are not a table in the accessibility tree`);
    check(out.filter((n) => n.role === 'columnheader').length >= 6, `${tag}: the readers table has no column headers`);
    check(out.filter((n) => n.role === 'rowheader').length >= rows, `${tag}: the readers table has no row headers`);
    const marks = out.filter((n) => n.role === 'image').map((n) => n.name);
    check(marks.length === 2 * rows && marks.every((m) => /^(not )?in the (readers file|store)$/.test(m)), `${tag}: the marks of the table are not all named: ${marks.length} of ${2 * rows}`);
    // the label that a stacked cell draws is upper case in the tree, as it is on the page; a mark's name is lower case
    const echoed = out.filter((n) => n.role === 'cell' && /^(READER KEY|SIGNING KEY|IN THE FILE|IN THE STORE|LINE)\b/.test(n.name));
    check(echoed.length === 0, `${tag}: a cell reads out its column label, which its header cell already gives: "${echoed[0] && echoed[0].name}"`);
    const unusable = (doc.readers || []).filter((r) => !r.name_ok).length;
    check(out.filter((n) => n.role === 'note' && n.name === 'name not usable').length === unusable, `${tag}: a name that cannot be used is not marked in the accessibility tree`);
  }
  const t = await b.eval(`document.querySelector('#main > h1').textContent`);
  check(/access|protected|read the state/i.test(t), `${tag}: the h1 of the page does not belong to the view: ${t}`);
  check(await b.eval(`document.getElementById('access-pane').getAttribute('role') === 'region'`), `${tag}: the pane has a label and no role`);
  return { stops, inPane };
}

// the suggestions and the copy buttons, with the keys
async function checkKeys(b, f, theme, width) {
  const tag = `${f}/${theme}/${width} keys`;
  await b.load(url(f, theme, ++nonce));
  const fk = () => b.eval(`document.activeElement.dataset.fk || ''`);
  const tabTo = async (pred) => { for (let i = 0; i < 80; i++) { await b.key('Tab'); if (pred(await fk())) return true; } return false; };
  // the skip link
  await b.key('Tab');
  check(await b.eval(`document.activeElement.className === 'skiplink'`), `${tag}: the first Tab does not reach the skip link`);
  await b.key('Enter');
  check(await b.eval(`document.activeElement.id === 'access-pane' && document.activeElement.matches(':focus-visible')`), `${tag}: the skip link does not move the focus to the view`);
  const pf = await b.eval(FOCUS);
  check(pf && pf.contrast >= 3 && !pf.clipped, `${tag}: the ring of the view when the skip link has moved the focus to it is ${pf && pf.contrast}:1 and clipped by ${pf && pf.clipped}`);
  await b.key('Tab');
  check(await b.eval(`document.activeElement.closest('#access-pane') !== null && document.activeElement.id !== 'access-pane'`), `${tag}: the Tab after the skip link does not go to the first stop of the view`);

  // a suggestion: Enter and Space
  check(await tabTo((k) => k.startsWith('sum:')), `${tag}: no suggestion to open`);
  const state = async () => JSON.stringify([await b.eval('document.activeElement.parentElement.open'), (await b.axOfFocus()).props.expanded]);
  const s0 = await state();
  const was = JSON.parse(s0)[0];
  await b.key('Enter');
  check(JSON.parse(await state())[0] === !was && JSON.parse(await state())[1] === !was, `${tag}: Enter does not toggle the suggestion (${s0} then ${await state()})`);
  await b.key(' ');
  check(await state() === s0, `${tag}: Space does not toggle the suggestion back (${s0} then ${await state()})`);
  await b.key(' ');
  await b.key('Escape');
  check(await fk() !== '' && (await b.eval('document.activeElement.tagName')) === 'SUMMARY', `${tag}: Escape moves the focus away from the suggestion`);
  check(JSON.parse(await state())[0] === !was, `${tag}: Escape changes the suggestion`);

  // a refresh of the view (the server says that the readers file changed) keeps the focus on the same stop, and the cards as they were
  await b.key('Enter');
  const before = JSON.stringify([await fk(), await b.eval('document.activeElement.parentElement.open')]);
  await b.eval(`window.__mock.setAccess(${JSON.stringify(f)})`);
  await new Promise((r) => setTimeout(r, 150));
  check(JSON.stringify([await fk(), await b.eval('document.activeElement.parentElement && document.activeElement.parentElement.open')]) === before, `${tag}: a refresh moves the focus or changes the cards (${before} then ${await fk()})`);

  // a copy button announces what it copied; Escape hides its tip and leaves the focus
  await b.load(url(f, theme, ++nonce));
  check(await tabTo((k) => k.startsWith('st:') || k === 'path' || k.startsWith('k:')), `${tag}: no copy button`);
  const name = await b.eval(`document.activeElement.getAttribute('aria-label')`);
  const live = () => b.eval(`document.getElementById('live').textContent`);
  await b.key('Enter');
  let said = '';
  for (let i = 0; i < 40 && !said.startsWith('Cop'); i++) { await new Promise((r) => setTimeout(r, 50)); said = await live(); }
  check(said === name.replace(/^Copy /, 'Copied '), `${tag}: after Enter on "${name}" the live region says "${said}"`);
  check(await b.eval(`!document.getElementById('tip').hidden`), `${tag}: the tip of the focused copy button is not shown`);
  await b.key('Escape');
  check(await b.eval(`document.getElementById('tip').hidden && document.activeElement.hasAttribute('data-copy')`), `${tag}: Escape does not hide the tip and leave the focus on the button`);
  await b.eval(`document.getElementById('live').textContent = ''`);
  await b.key(' ');
  said = '';
  for (let i = 0; i < 40 && !said.startsWith('Cop'); i++) { await new Promise((r) => setTimeout(r, 50)); said = await live(); }
  check(said === name.replace(/^Copy /, 'Copied '), `${tag}: after Space on "${name}" the live region says "${said}"`);

  // the warning that one reader holds the only lock: its button opens the add-reader suggestion and moves the focus into it
  await b.load(url(f, theme, ++nonce));
  if (await b.eval(`!!document.querySelector('#access-pane [data-fk="single"]')`)) {
    for (const key of ['Enter', ' ']) {
      await b.load(url(f, theme, ++nonce));
      check(await tabTo((k) => k === 'single'), `${tag}: the warning button is not a Tab stop`);
      await b.key(key);
      check(await b.eval(`document.activeElement.tagName === 'SUMMARY' && document.activeElement.parentElement.open && document.activeElement.parentElement.dataset.id === 'add-reader'`), `${tag}: ${key === ' ' ? 'Space' : 'Enter'} on the warning button does not open the add-reader suggestion and focus it`);
    }
  } else {
    check(f !== 'solo', `${tag}: the solo state shows no single-reader warning`);
  }
}

const oi = args.indexOf('--order');
if (oi >= 0) {
  const [f, w, theme] = [args[oi + 1] || 'drift', Number(args[oi + 2] || 1250), args[oi + 3] || 'light'];
  const b = await open(w);
  await checkOne(b, f, theme, w, true);
  await b.close(); srv.close();
  process.exit(failed ? 1 : 0);
}

const fi = args.indexOf('--fixtures');
const list = fi >= 0 ? args[fi + 1].split(',') : args.includes('--quick') ? ['platform', 'drift'] : FIXTURES;
const combos = [];
for (const width of [1250, 375]) for (const theme of ['light', 'dark']) combos.push({ width, theme });
await Promise.all(combos.map(async ({ width, theme }) => {
  const b = await open(width);
  try {
    for (const f of list) await checkOne(b, f, theme, width, false);
    await checkKeys(b, 'drift', theme, width);
    await checkKeys(b, 'immutable', theme, width);
    check(b.errors.length === 0, `${theme}/${width}: the page logged errors: ${b.errors.join('; ')}`);
  } finally { await b.close(); }
}));
srv.close();
if (failed) { console.log(`${failed} failed`); process.exit(1); }
console.log(`ok (the smallest focus ring: ${minRing.light}:1 in the light theme, ${minRing.dark}:1 in the dark)`);
process.exit(0);
