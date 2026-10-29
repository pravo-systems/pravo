// Checks the Ask panel in headless Chrome, on the mock (chat=on answers, chat=error fails, no chat= is a server without it):
//
//   - with the chat off there is no control and the key `c` does nothing;
//   - the first open shows the notice, and the notice says where questions go and what is never sent;
//   - a question is answered: the reads are listed while it works, the answer links the resources of the estate and not a
//     name that is not one, a command is a copy box with no run control, and the ledger lists what was sent;
//   - a refused question shows the message and a retry;
//   - the panel and the top bar fit at every width.
//
//   node check-chat.mjs
//
// It needs Chrome (the CHROME variable names it) and skips itself, with exit status 0, when it finds none.

import { Browser, findChrome, serve } from './cdp-driver.mjs';

const chrome = findChrome();
if (!chrome) { console.log('skipped: no Chrome (set CHROME)'); process.exit(0); }

let failed = 0;
const check = (ok, msg) => { if (!ok) { failed++; console.log('FAIL', msg); } return ok; };
const wait = (ms) => new Promise((r) => setTimeout(r, ms));

const srv = await serve();
const b = await Browser.launch(chrome, 1440, 900);
let loads = 0;
const url = (chat, frag = '') => `http://127.0.0.1:${srv.port}/index.html?mock=platform${chat ? '&chat=' + chat : ''}&n=${++loads}#/?motion=off${frag}`;
try {
  await b.init();

  // off
  await b.load(url('', ''));
  await wait(300);
  check(await b.eval(`document.getElementById('ask-btn').hidden`), 'off: the control is hidden');
  await b.key('c');
  await wait(100);
  check(await b.eval(`document.getElementById('chat').hidden`), 'off: the key c opens nothing');

  // the notice
  await b.load(url('on', '&sel=api.db'));
  await wait(400);
  check(await b.eval(`!document.getElementById('ask-btn').hidden`), 'on: the control is shown');
  await b.key('c');
  await wait(150);
  const notice = await b.eval(`(() => { const c = document.getElementById('chat'); return { open: !c.hidden, text: c.textContent, btn: !!c.querySelector('.btn.primary'), sel: document.getElementById('insp').classList.contains('chat-on') }; })()`);
  check(notice.open && notice.sel, 'on: the key c opens the panel in the slot of the inspector');
  check(notice.btn && /api\.anthropic\.com/.test(notice.text) && /Never sent/.test(notice.text) && /Secret values/.test(notice.text), 'the notice names the host and what is never sent');

  // a question
  await b.eval(`document.querySelector('#chat .btn.primary').click()`);
  await wait(150);
  check(await b.eval(`document.querySelector('#chat .chat-ctx .x').textContent.includes('api.db')`), 'the context chip names the selected resource');
  await b.eval(`document.querySelector('#chat .chips button').click()`);
  await wait(100);
  check(await b.eval(`!!document.querySelector('#chat .work')`), 'a question that is being answered shows its reads');
  await wait(900);
  const ans = await b.eval(`(() => {
    const c = document.getElementById('chat');
    const refs = [...c.querySelectorAll('.a button.ref')].map((x) => x.textContent);
    return { a: !!c.querySelector('.a'), refs, plain: [...c.querySelectorAll('.a code')].map((x) => x.textContent), cmd: !!c.querySelector('.a .cmdline [data-copy]'), runBtn: [...c.querySelectorAll('.a button')].some((x) => /run/i.test(x.textContent)), ledger: c.querySelectorAll('.ledger .row').length, work: !!c.querySelector('.work'), user: c.querySelector('.q').textContent };
  })()`);
  check(ans.a && !ans.work, 'the answer replaces the reads');
  check(ans.refs.includes('api.db') && ans.refs.includes('plan-2f9c1a4e'), `a resource and a stored plan are links: ${ans.refs}`);
  check(ans.plain.includes('identifier') && !ans.refs.includes('identifier'), 'a name that is not a resource of the estate is not a link');
  check(ans.cmd && !ans.runBtn, 'a command is a copy box and has no run control');
  check(ans.ledger === 2, `the ledger lists the two reads (${ans.ledger})`);
  // a link selects the node
  await b.eval(`document.querySelector('#chat .a button.ref:not(.plan)').click()`);
  await wait(200);
  check(await b.eval(`window.__pravo.S.sel === 'api.db'`), 'a link selects the resource');

  // an error
  await b.load(url('error', '&sel=api.db'));
  await wait(400);
  await b.key('c');
  await b.eval(`const p = document.querySelector('#chat .btn.primary'); if (p) p.click(); true`);
  await wait(100);
  await b.eval(`document.querySelector('#chat .chips button').click()`);
  await wait(900);
  check(await b.eval(`(() => { const e = document.querySelector('#chat .err'); return !!e && /529/.test(e.textContent) && !!e.querySelector('button'); })()`), 'a refused question shows the message and a retry');

  // widths
  for (const w of [390, 700, 1100, 1440, 1920]) {
    await b.size(w, 850);
    await b.load(url('on', '&sel=api.db'));
    await wait(300);
    await b.key('c');
    await wait(150);
    const r = await b.eval(`(() => { const top = document.querySelector('.top'); const th = document.getElementById('theme-btn').getBoundingClientRect(); const ch = document.getElementById('chat').getBoundingClientRect(); return { over: top.scrollWidth - top.clientWidth, right: Math.round(th.right), page: document.documentElement.scrollWidth - innerWidth, cw: Math.round(ch.width), ch: Math.round(ch.height) }; })()`);
    check(r.over <= 1 && r.right <= w, `${w}: the top bar fits with the Ask control (${JSON.stringify(r)})`);
    check(r.page <= 1, `${w}: the page scrolls sideways by ${r.page}`);
    check(r.cw > 200 && r.ch > 150, `${w}: the panel has room (${r.cw}x${r.ch})`);
  }
  // a sidebar down to a tablet, on any section, and a sheet only on a phone
  for (const [w, view] of [[1000, 'traces'], [800, 'plans'], [1000, 'graph'], [1440, 'traces']]) {
    await b.size(w, 850);
    await b.load(url('on', `&view=${view}`));
    await wait(300);
    await b.key('c');
    await wait(150);
    const r = await b.eval(`(() => { const c = document.getElementById('chat').getBoundingClientRect(); const m = document.querySelector('main.stage').getBoundingClientRect(); return { left: Math.round(c.left), top: Math.round(c.top), w: Math.round(c.width), h: Math.round(c.height), mainRight: Math.round(m.right), vh: innerHeight }; })()`);
    check(r.left >= r.mainRight - 2 && r.top < 200 && r.h > 500, `${w} ${view}: the chat is a sidebar (${JSON.stringify(r)})`);
  }
  await b.size(390, 850);
  await b.load(url('on', '&view=traces'));
  await wait(300);
  await b.key('c');
  await wait(150);
  check(await b.eval(`(() => { const c = document.getElementById('chat').getBoundingClientRect(); return c.top > 250 && c.width > 350; })()`), '390: on a phone the chat is a sheet under the page');
  check((await b.eval(`(window.__errors || []).length`)) === 0, 'no errors on the page');
} finally {
  await b.close();
  srv.close();
}
console.log(failed ? `${failed} failed` : 'ok');
process.exit(failed ? 1 : 0);
