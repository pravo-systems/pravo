// Checks that a find can be undone, in headless Chrome, on the platform fixture: a find (the palette's pick of a
// result) moves the picture, and the page keeps the picture that it left, so that
//
//   - the button "Back to where you were" shows after a find and returns to it: the same selection, the same open
//     and closed namespaces and the same camera (the middle of the picture and the scale), though the inspector
//     that the selection opened took a part of the canvas and gave it back
//   - Escape, a real key, does the same while the result is selected, and goes on through a chain of finds
//   - the Back button of the browser does the same, and Forward goes to the result again
//   - a find of what is selected already adds nothing to the history
//   - a page that is reloaded in the middle of a chain still has the button
//
//   node check-findback.mjs
//
// It needs Chrome (the CHROME variable names it) and skips itself, with exit status 0, when it finds none.

import { Browser, findChrome, serve } from './cdp-driver.mjs';

const chrome = findChrome();
if (!chrome) { console.log('skipped: no Chrome (set CHROME)'); process.exit(0); }

let failed = 0;
const check = (ok, msg) => { if (!ok) { failed++; console.log('FAIL', msg); } return ok; };

const srv = await serve();
const b = await Browser.launch(chrome, 1440, 900);
try {
  await b.init();
  await b.size(1440, 900);
  await b.load(`http://127.0.0.1:${srv.port}/index.html?mock=platform#/?motion=off`);

  const wait = (ms) => new Promise((r) => setTimeout(r, ms));
  const snap = () => b.eval(`(() => {
    const { S, gv } = window.__pravo;
    const btn = document.querySelector('.backbtn');
    return { sel: S.sel, open: [...S.collapsed].sort().join(','), view: S.view, finds: S.finds, entries: history.length,
      cx: Math.round((gv.size.w / 2 - gv.view.x) / gv.view.k), cy: Math.round((gv.size.h / 2 - gv.view.y) / gv.view.k), k: +gv.view.k.toFixed(3),
      back: btn && !btn.hidden && btn.dataset.kind === 'back' ? btn.querySelector('.bl').textContent : null };
  })()`);
  const find = async (q) => { await b.eval(`(() => { const { palette } = window.__pravo; palette.open(${JSON.stringify(q)}); palette.pick(0); })()`); await wait(900); };
  const same = (x, y) => x.sel === y.sel && x.open === y.open && x.view === y.view && x.cx === y.cx && x.cy === y.cy && x.k === y.k;
  const show = (x) => JSON.stringify(x);

  // the camera is not the first view
  await b.eval(`(() => { const { gv } = window.__pravo; gv.setView({ x: gv.view.x - 120, y: gv.view.y - 60, k: gv.view.k * 1.4 }, false); gv.userMoved = true; })()`);
  await wait(300);
  const start = await snap();
  check(start.finds === 0 && !start.back, `no way back before a find: ${show(start)}`);

  await find('db');
  const found = await snap();
  check(found.sel === 'api.db' && found.finds === 1 && found.entries === start.entries + 1, `a find selects api.db and adds one entry: ${show(found)}`);
  check(found.back === 'Back to where you were', `the button shows after a find: ${show(found)}`);
  check(found.open !== start.open, 'the find opened the namespace that hides the result');

  // the button
  await b.eval(`document.querySelector('.backbtn').click()`);
  await wait(900);
  let now = await snap();
  check(same(start, now) && now.finds === 0 && !now.back, `the button returns to the picture: ${show(start)} then ${show(now)}`);

  // Escape, a real key
  await find('db');
  await b.key('Escape');
  await wait(900);
  now = await snap();
  check(same(start, now) && now.finds === 0, `Escape returns to the picture: ${show(now)}`);

  // the browser's Back, and Forward
  await find('db');
  const again = await snap();
  await b.eval('history.back()');
  await wait(900);
  now = await snap();
  check(same(start, now) && now.finds === 0, `the browser's Back returns to the picture: ${show(now)}`);
  await b.eval('history.forward()');
  await wait(900);
  now = await snap();
  check(same(again, now) && now.finds === 1 && now.back, `Forward goes to the result again: ${show(again)} then ${show(now)}`);
  await b.eval('history.back()');
  await wait(700);

  // a chain of finds, undone one by one
  await find('db');
  const first = await snap();
  await find('web');
  const second = await snap();
  check(second.sel === 'edge.waf' && second.finds === 2, `a second find: ${show(second)}`);
  await b.key('Escape');
  await wait(900);
  now = await snap();
  check(same(first, now) && now.finds === 1 && now.back, `Escape returns to the first result: ${show(now)}`);
  await b.key('Escape');
  await wait(900);
  now = await snap();
  check(same(start, now) && now.finds === 0 && !now.back, `Escape returns to the start: ${show(now)}`);

  // a find of what is selected already makes no entry
  await find('db');
  const n1 = (await snap()).entries;
  await find('db');
  const n2 = (await snap()).entries;
  check(n1 === n2, `a find of the selected resource adds no entry: ${n1} then ${n2}`);

  // after a reload the entries are still there
  await b.eval('location.reload()');
  await wait(1500);
  for (let i = 0; i < 100 && !(await b.eval('document.documentElement.dataset.ready === "1"').catch(() => false)); i++) await wait(50);
  now = await snap();
  check(now.finds === 1 && now.back, `the button is there after a reload: ${show(now)}`);

  check(b.errors.length === 0, `the page logged errors: ${b.errors.join('; ')}`);
} finally { await b.close(); srv.close(); }
if (failed) { console.log(`${failed} failed`); process.exit(1); }
console.log('ok');
process.exit(0);
