// Checks the finder of the Plans section in headless Chrome, on the platform fixture: the box filters the list of stored plans,
// names the changes that it asked for in each row, narrows the list of changes in the detail of a plan, has chips that make
// terms, a menu that offers the columns and the values, and keeps its text in the address (plq=).
//
//   node check-plansfinder.mjs
//
// It needs Chrome (the CHROME variable names it) and skips itself, with exit status 0, when it finds none.

import { Browser, findChrome, serve } from './cdp-driver.mjs';

const chrome = findChrome();
if (!chrome) { console.log('skipped: no Chrome (set CHROME)'); process.exit(0); }

let failed = 0;
const check = (ok, msg) => { if (!ok) { failed++; console.log('FAIL', msg); } return ok; };

const srv = await serve();
const origin = `http://127.0.0.1:${srv.port}/index.html?mock=platform`;
const b = await Browser.launch(chrome, 1440, 900);
try {
  await b.init();
  await b.size(1440, 900);
  await b.load(`${origin}#/?view=plans&motion=off`);
  const wait = (ms) => new Promise((r) => setTimeout(r, ms));
  const type = async (text) => {
    await b.eval(`(() => { const i = document.getElementById('pl-filter'); i.focus(); i.value = ${JSON.stringify(text)}; i.dispatchEvent(new Event('input', { bubbles: true })); })()`);
    await wait(450);
  };
  const state = () => b.eval(`(() => ({
    rows: [...document.querySelectorAll('#plans-list .rp')].map((r) => r.dataset.id.replace('plan-', '')).sort().join(),
    named: [...document.querySelectorAll('#plans-list .rp .rp-l4')].map((r) => r.textContent.trim().replace(/\\s+/g, ' ')),
    pills: [...document.querySelectorAll('#plans-list .qpill')].map((p) => p.querySelector('b').textContent + ': ' + p.querySelector('span').textContent),
    count: document.querySelector('#plans-list .filterbox .count').textContent,
    hash: location.hash,
    value: document.getElementById('pl-filter').value,
    seg: [...document.querySelectorAll('#plans-list .rp-seg button')].filter((x) => x.getAttribute('aria-pressed') === 'true').map((x) => x.dataset.filter).join(),
    changes: [...document.querySelectorAll('#plans-list .rp-chlist .rp-chg code')].map((c) => c.textContent).join(),
    facets: [...document.querySelectorAll('#plans-list .rp-facet')].map((c) => c.querySelector('span').textContent + ' ' + c.querySelector('em').textContent + (c.classList.contains('on') ? '*' : '')).join('|'),
    menu: [...document.querySelectorAll('#plans-list .qsug li[role=option] .v')].map((x) => x.textContent),
  }))()`);

  let s = await state();
  check(s.rows.split(',').length === 4 && s.seg === 'pending', `the page opens on the plans that can be applied: ${s.rows} ${s.seg}`);
  check(s.count === '7 plans', `the count says how many plans there are: ${s.count}`);

  // the menu of an empty box
  await b.eval(`document.getElementById('pl-filter').click()`);
  await wait(300);
  s = await state();
  check(s.menu.includes('status:open') && s.menu.includes('overlap:any') && s.menu.includes('status:'), `an empty box offers filters and columns: ${s.menu.slice(0, 6)}`);
  check(await b.eval(`document.getElementById('pl-filter').getAttribute('aria-controls') === 'qsug-pl' && !!document.getElementById('qsug-pl') && document.querySelectorAll('[id="qsug"]').length <= 1`), 'the menu has an id of its own');

  // a filter reaches plans that the list did not show
  await type('status:applied,expired');
  s = await state();
  check(s.rows === '91d5f6b8,e0a17c33' && s.seg === 'all', `a finder looks at every plan: ${s.rows} ${s.seg}`);
  check(s.pills.join('|') === 'status: applied, expired' && s.count === '2 matches', `a pill for the term and a count: ${s.pills} ${s.count}`);
  check(s.hash.includes('plq=status%3Aapplied%2Cexpired'), `the text is in the address: ${s.hash}`);

  // a term about the changes names them in the row
  await type('ns:network');
  s = await state();
  check(s.rows === '5a2c9e71,e0a17c33' && s.named.length === 2 && s.named[0].includes('network.private'), `ns: names the changes in the row: ${s.rows} ${s.named}`);
  await type('change:conflict');
  s = await state();
  check(s.rows === 'b86e2d14' && s.named[0].includes('api.policy') && s.named[0].includes('api.role') && !s.named[0].includes('api.logs'), `change:conflict: ${s.named}`);

  // the detail narrows its list of changes with the finder, and its chips make terms
  await type('action:update ns:api');
  await b.eval(`document.querySelector('#plans-list .rp[data-id="plan-2f9c1a4e"]').click()`);
  await wait(300);
  s = await state();
  check(s.changes === 'api.db,api.service,api.task,api.worker.service', `the detail lists the changes that match: ${s.changes}`);
  check(s.facets.includes('api 3*') && s.facets.includes('api.worker 1'), `the chips show the namespaces, and the term that is held is pressed: ${s.facets}`);
  await b.eval(`[...document.querySelectorAll('#plans-list .rp-facet')].find((c) => c.textContent.includes('api.worker')).click()`);
  await wait(450);
  s = await state();
  check(s.value.includes('ns:api,api.worker') && s.rows.length > 0, `a chip adds its value to the term of its column: ${s.value}`);
  await b.eval(`[...document.querySelectorAll('#plans-list .rp-facet')].find((c) => c.textContent.includes('api.worker')).click()`);
  await wait(450);
  s = await state();
  check(s.value === 'action:update ns:api', `a chip that is pressed takes its value out again: ${s.value}`);
  await b.eval(`document.querySelector('#plans-list .rp-chg').click()`);
  await wait(900);
  check(await b.eval(`window.__pravo.S.view === 'graph' && window.__pravo.S.sel === 'api.db' && window.__pravo.S.plan === 'plan-2f9c1a4e'`), 'a change in the detail goes to the resource, with the plan laid over the graph');

  // a pill takes a term out, and a link carries the text
  await b.load(`${origin}&r=2#/?view=plans&motion=off&plq=${encodeURIComponent('by:ana* action:update')}`);
  s = await state();
  check(s.value === 'by:ana* action:update' && s.rows === '2f9c1a4e' && s.pills.length === 2, `a link carries the text of the finder: ${s.value} ${s.rows}`);
  await b.eval(`document.querySelector('#plans-list .qpill button').click()`);
  await wait(450);
  s = await state();
  check(s.pills.length === 1 && s.value === 'action:update', `a pill takes its term out: ${s.value}`);

  // nothing matches
  await type('status:applied ns:network');
  check(await b.eval(`document.querySelector('#plans-list .rp-none').textContent.includes('No plan matches')`), 'a finder that leaves nothing says so');
  await b.eval(`document.querySelector('#plans-list .rp-none .linkbtn').click()`);
  await wait(450);
  s = await state();
  check(s.value === '' && s.rows.split(',').length === 4, `the way out of an empty list clears the finder: ${s.value}`);

  // the strip on the canvas says what the plan changes, and leads to the list of its changes
  await b.load(`${origin}&r=3#/?motion=off&plan=plan-91d5f6b8`);
  const strip = () => b.eval(`(() => { const e = document.getElementById('planstrip'); const l = e.querySelector('.ps-chg'); return { text: e.querySelector('.pcnt') && e.querySelector('.pcnt').textContent, list: !!l, label: l && l.getAttribute('aria-label'), prog: e.querySelector('.ps-prog') && e.querySelector('.ps-prog').textContent }; })()`);
  let t = await strip();
  check(t.text === '+2' && t.list && t.label.startsWith('2 added') && !t.prog, `the strip of an applied plan counts what it made: ${JSON.stringify(t)}`);
  await b.eval(`document.querySelector('#planstrip .ps-chg').click()`);
  await wait(500);
  s = await state();
  check(await b.eval(`window.__pravo.S.view === 'plans' && document.querySelector('#plans-list .rp[data-id="plan-91d5f6b8"]').getAttribute('aria-expanded') === 'true'`)
    && s.changes === 'api.cache,api.worker.dlq', `the counts lead to the changes of the plan: ${s.changes}`);
  await b.load(`${origin}&r=4#/?motion=off&plan=plan-5a2c9e71`);
  t = await strip();
  check(t.text === '~2' && t.prog === '1 of 2 applied', `the strip of a partial plan says how much was applied: ${JSON.stringify(t)}`);

  // a plan that adopts objects and moves one has a summary of nothing, and is not said to have no changes
  const adopt = JSON.stringify({ id: 'plan-ac30e3aa', status: 'applied', open: false, author: 'marcinw', created: new Date(Date.now() - 36e6).toISOString(), expires: new Date(Date.now() + 6e8).toISOString(),
    summary: { add: 0, change: 0, destroy: 0, read: 0 }, planned: 3, applied: 3,
    touches: [{ addr: 'api.db', action: 'no-op', applied: true }, { addr: 'api.cache', action: 'no-op', applied: true }, { addr: 'network.private.nat', action: 'move', applied: true }] });
  const made = await b.eval(`(async () => {
    const m = await import('./assets/plans.js');
    const el = document.createElement('div');
    m.planStrip(el, ${adopt}, {});
    return { strip: el.querySelector('.pcnt').textContent, row: m.describe(${adopt}, '') };
  })()`);
  check(made.strip === '2 imported1 moved' && made.row.includes('2 imported, 1 moved') && !made.strip.includes('no changes'), `a plan that adopts is not a plan with no changes: ${JSON.stringify(made)}`);
  check(b.errors.length === 0, `the page logged errors: ${b.errors.join('; ')}`);
} finally { await b.close(); srv.close(); }
if (failed) { console.log(`${failed} failed`); process.exit(1); }
console.log('ok');
process.exit(0);
