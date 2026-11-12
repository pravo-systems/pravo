// Checks the Time lapse view in headless Chrome, on the platform fixture (nine runs): the film starts at the end, the keys of the
// timeline step it, a run lights up what it wrote, a removed resource is an empty frame afterwards, play stops at the end, the step is
// in the address, a name picks the resource and the panel shows it as the run left it, and the way to the inventory and back keeps the place.
//
//   node check-timelapse-view.mjs
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
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
try {
  await b.init();
  await b.size(1440, 900);
  await b.load(`${origin}#/?view=timelapse&motion=off`);
  await wait(300);
  const st = () => b.eval(`(() => {
    const q = (s) => document.querySelectorAll('#timelapse-pane ' + s).length;
    const strip = document.querySelector('#timelapse-pane .tl-strip');
    return { step: strip && Number(strip.getAttribute('aria-valuenow')), max: strip && Number(strip.getAttribute('aria-valuemax')), k: (document.querySelector('#timelapse-pane .tl-readout .tl-k') || {}).textContent,
      add: q('.tl-c.a-create'), chg: q('.tl-c.a-update'), rep: q('.tl-c.a-replace'), rem: q('.tl-c.a-delete'), mov: q('.tl-c.a-move'), on: q('.tl-c.tl-on'), gone: q('.tl-c.tl-gone'), off: q('.tl-c.tl-off'),
      n: (document.querySelector('#timelapse-pane .tl-n') || {}).textContent, hash: location.hash, play: (document.querySelector('#timelapse-pane .tl-play') || {}).textContent,
      rows: q('.tl-table tbody tr'), listed: q('.tl-list li'), view: document.getElementById('app').dataset.view, title: document.title };
  })()`);
  const focus = () => b.eval(`document.querySelector('#timelapse-pane .tl-strip').focus()`);

  let s = await st();
  check(s.max === 9 && s.step === 9 && s.k === 'Run 9 of 9', `the film opens on the last run: ${JSON.stringify(s)}`);
  check(s.rows === 9 && /Time lapse/.test(s.title), `a row for each run and the section in the title: ${s.rows} ${s.title}`);
  check(s.view === 'timelapse', 'the view is the time lapse');
  // the last run changed one resource and did not add anything
  check(s.chg === 1 && s.add === 0 && s.listed === 1, `run 9 changed one resource: ${JSON.stringify(s)}`);

  await focus();
  await b.key('Home');
  s = await st();
  check(s.step === 0 && s.k === 'Before the first run' && s.add === 0 && s.chg === 0, `Home shows the estate before the first run: ${JSON.stringify(s)}`);
  check(s.off > 0, 'what the runs add is not there yet');
  const before = s.on;
  await b.key('ArrowRight');
  s = await st();
  check(s.step === 1 && s.add === 7 && s.on === before, `run 1 adds seven resources, which light up: ${JSON.stringify(s)}`);
  check(/#.*ls=1\b/.test(s.hash), `the step is in the address: ${s.hash}`);
  check(s.n !== undefined && Number(s.n) === before + 7, `the count follows: ${s.n}, wanted ${before + 7}`);

  await b.key('End');
  await b.key('ArrowLeft');
  s = await st();
  check(s.step === 8 && s.rem === 3 && s.mov === 1, `run 8 removes three resources and renames one: ${JSON.stringify(s)}`);
  await b.key('End');
  s = await st();
  check(s.gone === 3 && s.rem === 0, `a resource that a run removed is an empty frame after it: ${JSON.stringify(s)}`);

  // play from the end starts again from the beginning, and stops at the end
  await b.key('ArrowLeft'); await b.key('ArrowLeft');
  await b.key(' ');
  s = await st();
  check(/Pause/.test(s.play), `Space plays: ${s.play}`);
  await wait(3200);
  s = await st();
  check(s.step === 9 && /Replay/.test(s.play), `it plays to the end and stops: ${JSON.stringify(s)}`);

  // the address names a step
  await b.load(`${origin}&again=1#/?view=timelapse&ls=3&motion=off`);
  await wait(300);
  s = await st();
  check(s.step === 3 && s.k === 'Run 3 of 9', `ls=3 opens run 3: ${JSON.stringify(s)}`);

  // a name in the list picks the resource: the film stays, and the panel shows it as the shown run left it
  const panel = () => b.eval(`(() => ({ view: document.getElementById('app').dataset.view, sel: document.getElementById('app').dataset.sel, k: (document.querySelector('.asof-k') || {}).textContent, title: (document.querySelector('#ih .ih-title') || {}).textContent, hash: location.hash,
    ticks: document.querySelectorAll('#timelapse-pane .tl-tick.on').length, picked: document.querySelectorAll('#timelapse-pane .tl-stage .tl-c.sel').length, prevOff: (document.querySelector('.asof-b .btn') || {}).disabled,
    jumps: document.querySelectorAll('#ibody .tl-jump').length, back: (document.getElementById('lapse-back') || {}).hidden }))()`);
  await b.eval(`[...document.querySelectorAll('#timelapse-pane .tl-addr')].find((x) => x.textContent === 'api.db_pass').click()`);
  await wait(500);
  let pn = await panel();
  check(pn.view === 'timelapse' && pn.sel === '1' && pn.picked === 1, `a name in the list picks the resource and stays in the film: ${JSON.stringify(pn)}`);
  check(/After run 3 of 9/.test(pn.k) && pn.title === 'db_pass', `the panel is of the shown run: ${JSON.stringify(pn)}`);
  check(/lr=api\.db_pass/.test(pn.hash) && /ls=3/.test(pn.hash), `the pick is in the address: ${pn.hash}`);
  check(pn.ticks >= 1, 'the runs that wrote it are marked under the timeline');
  const key = await b.eval(`(document.querySelector('#timelapse-pane .tl-rel') || {}).textContent`);
  check(key && !/null|undefined/.test(key), `the key of the picked resource says no null: ${key}`);
  // the panel follows the film
  await focus();
  await b.key('ArrowRight');
  await wait(400);
  pn = await panel();
  check(/After run 4 of 9/.test(pn.k), `the panel follows the film: ${pn.k}`);
  // the history of the resource links to the film, and the film to the history
  await b.eval(`document.querySelector('.itab[data-tab="history"]').click()`);
  await wait(400);
  pn = await panel();
  check(pn.jumps >= 1 && /lt=history/.test(pn.hash), `the history lists runs that open in the film: ${JSON.stringify(pn)}`);
  // Escape puts it down
  await b.key('Escape');
  await wait(300);
  pn = await panel();
  check(pn.sel === '' && pn.picked === 0 && !/lr=/.test(pn.hash), `Escape puts the resource down: ${JSON.stringify(pn)}`);
  // a pick in the address, and the way to the inventory and back
  await b.load(`${origin}&again=2#/?view=timelapse&ls=7&lr=api.db&motion=off`);
  await wait(600);
  pn = await panel();
  check(/After run 7 of 9/.test(pn.k) && pn.picked === 1 && pn.title === 'db', `an address with lr= opens the film with the resource picked: ${JSON.stringify(pn)}`);
  await b.eval(`document.querySelector('.asof-open').click()`);
  await wait(500);
  pn = await panel();
  check(pn.view === 'graph' && /sel=api\.db/.test(pn.hash) && pn.back === false, `the inventory opens on it, with the way back: ${JSON.stringify(pn)}`);
  await b.eval(`document.getElementById('lapse-back').click()`);
  await wait(500);
  pn = await panel();
  check(pn.view === 'timelapse' && /After run 7 of 9/.test(pn.k) && pn.picked === 1, `the way back returns to the same run and resource: ${JSON.stringify(pn)}`);
  // the history of the inventory opens the film at that run
  await b.eval(`document.getElementById('sec-inventory').click()`);
  await wait(400);
  await b.eval(`document.querySelector('.itab[data-tab="history"]').click()`);
  await wait(400);
  await b.eval(`document.querySelector('#ibody .tl-jump').click()`);
  await wait(500);
  pn = await panel();
  check(pn.view === 'timelapse' && pn.picked === 1 && /After run \d of 9/.test(pn.k), `a history entry opens the film at its run: ${JSON.stringify(pn)}`);
  // a resource with no recorded connection says so, and nothing prints null
  await b.load(`${origin}&again=4#/?view=timelapse&ls=2&lr=network.sg_old&motion=off`);
  await wait(600);
  const none = await b.eval(`(document.querySelector('#timelapse-pane .tl-rel') || {}).textContent`);
  check(none && !/null|undefined/.test(none), `the key of a resource without connections: ${none}`);

  // a resource that a run removed is told, with its history
  await b.load(`${origin}&again=3#/?view=timelapse&ls=9&lr=network.sg_old&motion=off`);
  await wait(600);
  const gone = await b.eval(`(document.querySelector('#ibody') || {}).textContent`);
  check(/removed this resource/.test(gone || ''), `a removed resource is told: ${gone}`);

  // a project without a run says so
  await b.load(`http://127.0.0.1:${srv.port}/index.html?mock=estate#/?view=timelapse&motion=off`);
  await wait(300);
  const empty = await b.eval(`(document.querySelector('#timelapse-pane .tl-empty') || {}).textContent`);
  check(/No run has written/.test(empty || ''), `an empty history is told: ${empty}`);
  const errs = b.errors.filter((e) => !/favicon|404/.test(String(e)));
  check(errs.length === 0, `errors in the page: ${errs.join(' | ')}`);
} finally {
  await b.close();
  srv.close();
}
console.log(failed ? `${failed} failed` : 'ok');
process.exit(failed ? 1 : 0);
