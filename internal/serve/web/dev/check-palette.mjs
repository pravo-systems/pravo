// Checks the command palette in headless Chrome, on the platform fixture: the keys that open it, the groups it
// searches (resources first, then plans, transactions, runs and the objects that discoveries found), the actions
// with the shortcut each shows, ">" for the actions alone, and where a pick goes.
//
//   node check-palette.mjs
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
  // what the palette lists now: [group, [titles]]
  const shown = () => b.eval(`(() => {
    const out = []; let g = null;
    for (const el of document.querySelectorAll('#pal-list > *')) {
      if (el.classList.contains('pal-grp')) { g = { group: el.firstChild.textContent, rows: [] }; out.push(g); }
      else if (g && el.getAttribute('role') === 'option') g.rows.push({ t: el.querySelector('.pal-t').textContent, keys: [...el.querySelectorAll('.pal-keys kbd')].map((k) => k.textContent), sel: el.getAttribute('aria-selected') === 'true' });
    }
    return out;
  })()`);
  const type = async (q) => { await b.eval(`(() => { const i = document.querySelector('#palette input'); i.value = ${JSON.stringify(q)}; i.dispatchEvent(new Event('input')); })()`); await wait(80); };
  const open = async (q = '') => { await b.eval(`window.__pravo.palette.open(${JSON.stringify(q)})`); await wait(250); };
  const enter = async () => { await b.key('Enter'); await wait(700); };
  const names = (g) => g.map((x) => x.group);
  const view = () => b.eval('window.__pravo.S.view');

  // the keys
  await b.key('/');
  await wait(200);
  check(await b.eval('window.__pravo.palette.isOpen'), 'the slash opens the palette');
  await b.key('Escape');
  await wait(200);
  check(!(await b.eval('window.__pravo.palette.isOpen')), 'Escape closes it');
  await b.key('k', 2); // control
  await wait(200);
  check(await b.eval('window.__pravo.palette.isOpen'), 'Ctrl-K opens the palette');
  await b.key('Escape');
  await wait(200);

  // no text: where to go first, then the namespaces
  await open();
  let g = await shown();
  check(names(g)[0] === 'Go to' && names(g).includes('Namespaces'), `no text lists where to go, then the namespaces: ${names(g)}`);
  check(g[0].rows.some((r) => r.t === 'Go to Traces' && r.keys.join() === 'r'), 'a section shows its key');
  check(!g[0].rows.some((r) => r.t === 'Go to Inventory'), 'the section that is open is not offered');
  await b.key('Escape');
  await wait(200);

  // a word: the resources keep their place at the front
  await open();
  await type('api');
  g = await shown();
  check(names(g)[0] === 'Namespaces' && names(g).includes('Items'), `the resources come first: ${names(g)}`);
  await b.key('Escape');
  await wait(200);

  // plans
  await open();
  await type('ben@acme');
  g = await shown();
  check(names(g)[0] === 'Plans' && g[0].rows.length >= 1, `a plan is found by its author: ${names(g)}`);
  const planId = g[0].rows[0].t;
  await enter();
  check(await view() === 'plans', `a plan opens the Plans section: ${await view()}`);
  check(await b.eval(`!!document.querySelector('#plans-list [data-id="${planId}"], #plans-list')`), 'the Plans section is drawn');

  // runs and transactions
  await open();
  await type('database');
  g = await shown();
  const txn = g.find((x) => x.group === 'Transactions');
  check(!!txn && txn.rows.length >= 1, `a run that applied is a transaction, found by its reason: ${names(g)}`);
  await type('20261021T140112Z');
  g = await shown();
  check(names(g).join() === 'Transactions', `a transaction is found by its id: ${names(g)}`);
  await enter();
  check(await view() === 'traces', `a transaction opens the Traces section: ${await view()}`);
  check(await b.eval(`window.__pravo.tracesView.id.startsWith('5a1c0001')`), 'with its run open');
  await open();
  await type('pravo plan');
  g = await shown();
  check(names(g).includes('Runs'), `a run that wrote nothing is a run: ${names(g)}`);
  await b.key('Escape');
  await wait(200);

  // what discoveries found
  await b.eval(`window.__pravo.setView('graph')`);
  await open();
  await type('vpc-0a91');
  g = await shown();
  check(names(g).join() === 'Discovery', `an object of a discovery is found: ${names(g)}`);
  await enter();
  check(await view() === 'discovery', 'it opens the Discovery section');
  const dq = await b.eval('window.__pravo.S.dq');
  check(/id:vpc-0a91c2/.test(dq) && /discovery:network\.vpcs/.test(dq), `with the filter on it: ${dq}`);

  // actions
  await b.eval(`window.__pravo.setView('graph')`);
  await open('>');
  g = await shown();
  check(names(g).every((n) => n !== 'Namespaces' && n !== 'Items') && names(g).includes('View') && names(g).includes('Copy'), `a leading > lists the actions alone, by group: ${names(g)}`);
  const view1 = g.find((x) => x.group === 'View');
  check(view1.rows.some((r) => r.t === 'Fit the graph' && r.keys.join() === 'f'), 'an action shows its shortcut');
  await type('>theme');
  g = await shown();
  check(g.length === 1 && g[0].rows[0].t.startsWith('Switch to the'), `an action is found by a word: ${JSON.stringify(g)}`);
  const before = await b.eval(`document.documentElement.getAttribute('data-theme') || ''`);
  await enter();
  const after = await b.eval(`document.documentElement.getAttribute('data-theme') || ''`);
  check(before !== after, `the action ran: the theme was ${before || 'system'} and is ${after}`);
  check(!(await b.eval('window.__pravo.palette.isOpen')), 'the palette closed');

  // an action of the view only
  await b.eval(`window.__pravo.setView('plans')`);
  await open('>');
  g = await shown();
  check(!g.some((x) => x.rows.some((r) => r.t === 'Fit the graph')), 'the graph actions are not offered in the Plans section');
  await b.key('Escape');
  await wait(200);

  // an action by plain typing comes after the resources
  await b.eval(`window.__pravo.setView('graph')`);
  await open();
  await type('expand');
  g = await shown();
  check(names(g).at(-1) === 'Actions' && g.at(-1).rows[0].t === 'Expand every namespace' && g.at(-1).rows[0].keys.join() === ']', `plain typing finds an action last: ${JSON.stringify(g.at(-1))}`);
  await b.key('ArrowDown');
  check(await b.eval(`document.querySelectorAll('#pal-list [aria-selected=true]').length === 1`), 'one row is selected');
  await b.key('Escape');

  check(b.errors.length === 0, `the page logged errors: ${b.errors.join('; ')}`);
} finally { await b.close(); srv.close(); }
if (failed) { console.log(`${failed} failed`); process.exit(1); }
console.log('ok');
process.exit(0);
