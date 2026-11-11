// The Time lapse view: the history of the estate as a film. The runs that wrote resources, oldest first, are replayed over a drawing of
// the estate in which every resource has a place of its own, so that between two steps only what a run did moves: what it added,
// changed, replaced, moved or removed lights up, and the readout says by whom and when. The drawing is of the nodes that the
// graph has and of the nodes that the runs named; the places are the namespaces in the order of their addresses, and the connections
// are left out, because a node that was removed has none to show.
//
// The page reads /api/timelapse, which is made from the transactions of the state. Nothing here runs a command or writes a file.

import { h, clear, icon, plural, stamp, clamp, fmt, motionOn, signatureMark } from './util.js';
import * as api from './api.js';
import { busy } from './ui.js';
import { build, activity, HERE, GONE, CLASS, WORD, ORDER } from './lapse-model.js';

export const FOOTER = 'This page replays the runs that the state recorded. It runs nothing and writes nothing.';

const LISTED = 40; // changes of a run listed before the button that shows the rest
const PLAY_MS = [150, 900]; // the time of a step while it plays: the whole film takes about fourteen seconds, within these bounds
const BAR_MAX = 38; // the tallest bar of the timeline, in pixels

// sizeFor is the class of a cell by how many there are: the glyph of an action shows at the full size only.
const sizeFor = (n) => (n <= 400 ? '' : n <= 1500 ? ' tiny' : ' tiny micro');

const who = (s) => s.actor || s.user || '';
const shortRev = (rev) => (rev && rev.length > 7 ? rev.slice(0, 7) + (rev.endsWith('-dirty') ? '-dirty' : '') : rev || '');

export class TimeLapseView {
  constructor(root, cb = {}) {
    this.root = root;
    this.cb = cb; // nodes() lists the nodes of the graph, revision() is the revision of the project, onOpen(addr) opens a node, onRoute() says the address changed
    this.doc = null;
    this.rev = '';
    this.model = null;
    this.error = '';
    this.step = -1; // the frame that is shown: 0 is before the first run
    this.want = 0; // the step that the address names, 1-based, until the document is read
    this.active = false;
    this.playing = false;
    this.timer = 0;
    this.seq = 0;
    this.all = false; // every change of the run is listed
    this.els = {};
  }

  // ---- the address ----

  route() {
    return { ls: this.model && this.step > 0 && this.step < this.model.steps.length ? String(this.step) : '' };
  }

  setRoute(f) {
    const n = Number(f.ls);
    this.want = Number.isInteger(n) && n > 0 ? n : 0;
    if (this.model && this.want) this.go(Math.min(this.want, this.model.steps.length), false);
  }

  // ---- loading ----

  async open() {
    this.active = true;
    if (!this.doc) this.render();
    await this.reload();
  }

  close() {
    this.active = false;
    this.stop();
  }

  // changed is the page being told that the project changed: the runs are read again when the section is next shown, or now.
  changed() {
    if (this.active) this.reload();
    else this.rev = '';
  }

  async reload() {
    const seq = ++this.seq;
    const want = this.cb.revision ? this.cb.revision() : '';
    if (this.doc && this.rev === want) return;
    let doc;
    try { doc = await api.timelapse(); } catch (err) {
      if (seq !== this.seq) return;
      if (err instanceof api.Unauthorized || err instanceof api.Unreachable) return;
      this.error = err.message || String(err);
      this.render();
      return;
    }
    if (seq !== this.seq) return;
    this.error = '';
    this.doc = doc;
    this.rev = want;
    this.model = build(doc, this.cb.nodes ? this.cb.nodes() : []);
    const n = this.model.steps.length;
    // a film that is open stays where it is when a run lands; one that was not opened shows the end, where the news is
    const keep = this.step >= 0 && this.step < n ? this.step : -1;
    this.step = this.want ? Math.min(this.want, n) : keep >= 0 ? keep : n;
    this.want = 0;
    this.render();
  }

  // ---- the page ----

  render() {
    this.stop();
    clear(this.root);
    this.els = {};
    const head = h('header', { class: 'tl-head' },
      h('h2', { class: 'tl-title', id: 'tl-h' }, 'Time lapse'),
      h('p', { class: 'tl-lede' }, 'The estate as the runs left it, one run after another. Every resource keeps its place, so that only what a run did moves.'));
    let body;
    if (this.error) body = h('p', { class: 'tl-empty' }, this.error);
    else if (!this.doc) body = busy('Reading the runs…');
    else if (!this.model.steps.length) body = h('p', { class: 'tl-empty' }, 'No run has written a resource yet. The film is made from the runs that the state records, and the first apply starts it.');
    else body = this.film();
    this.root.append(h('div', { class: 'tl' }, head, body, h('p', { class: 'tl-foot' }, FOOTER)));
    if (this.model && this.model.steps.length && !this.error) this.show(false);
  }

  film() {
    const m = this.model;
    const e = this.els;
    e.readout = h('section', { class: 'tl-readout', 'aria-live': 'polite', 'aria-label': 'The run that is shown' });
    e.stage = h('section', { class: 'tl-stage', 'aria-label': `The estate: ${plural(m.cells.length, 'resource')} in ${plural(m.groups.length, 'group')}` });
    e.cells = new Array(m.cells.length);
    const size = sizeFor(m.cells.length);
    for (const g of m.groups) {
      const cols = clamp(Math.ceil(Math.sqrt(g.cells.length * 2.2)), 2, 24);
      const grid = h('div', { class: 'tl-cells', style: `grid-template-columns: repeat(${Math.min(cols, g.cells.length)}, max-content)` });
      for (const c of g.cells) {
        const el = h('span', { class: 'sq tl-c' + size, 'data-tip': c.addr });
        el.addEventListener('click', () => this.cb.onOpen && this.cb.onOpen(c.addr));
        e.cells[c.i] = el;
        grid.append(el);
      }
      e.stage.append(h('div', { class: 'tl-g' }, h('p', { class: 'tl-gn', title: g.ns || 'the root' }, g.ns || 'root', h('span', { class: 'tl-gc' }, String(g.cells.length))), grid));
    }
    e.legend = h('ul', { class: 'tl-key', 'aria-label': 'Key' },
      ORDER.map((a) => h('li', null, h('span', { class: 'sq ' + CLASS[a], 'aria-hidden': 'true' }), WORD[a])),
      h('li', null, h('span', { class: 'sq tl-on', 'aria-hidden': 'true' }), 'there'),
      h('li', null, h('span', { class: 'sq tl-gone', 'aria-hidden': 'true' }), 'was there'));
    e.back = h('button', { type: 'button', class: 'btn tl-btn', 'aria-label': 'Previous run', on: { click: () => { this.stop(); this.go(this.step - 1); } } }, icon('arrow-l'));
    e.play = h('button', { type: 'button', class: 'btn primary tl-btn tl-play', on: { click: () => this.toggle() } });
    e.next = h('button', { type: 'button', class: 'btn tl-btn', 'aria-label': 'Next run', on: { click: () => { this.stop(); this.go(this.step + 1); } } }, icon('arrow'));
    const act = activity(m);
    const top = Math.max(1, ...act);
    e.bars = act.map((n, i) => h('i', { class: 'tl-bar', style: `height: ${Math.round(3 + (BAR_MAX - 3) * Math.sqrt(n / top))}px`, 'aria-hidden': 'true', data: { i: String(i + 1) } }));
    e.strip = h('div', { class: 'tl-strip', role: 'slider', tabIndex: 0, 'aria-label': 'Run', 'aria-valuemin': '0', 'aria-valuemax': String(m.steps.length) }, e.bars);
    e.strip.addEventListener('pointerdown', (ev) => this.scrub(ev));
    e.strip.addEventListener('keydown', (ev) => this.key(ev));
    const first = m.steps[0], last = m.steps[m.steps.length - 1];
    e.changes = h('section', { class: 'tl-sec', 'aria-labelledby': 'tl-h-ch' });
    e.runs = h('section', { class: 'tl-sec', 'aria-labelledby': 'tl-h-runs' });
    const older = this.doc.older ? h('p', { class: 'tl-note' }, `${plural(this.doc.older, 'older run')} are left out. What they made is shown as there from the start.`) : null;
    return h('div', { class: 'tl-film' },
      e.readout,
      e.stage,
      e.legend,
      h('div', { class: 'tl-ctl' },
        h('div', { class: 'tl-btns' }, e.back, e.play, e.next),
        h('div', { class: 'tl-time' }, e.strip, h('div', { class: 'tl-ends' }, h('span', null, stamp(first.time)), h('span', null, stamp(last.time))))),
      older,
      e.changes,
      e.runs);
  }

  // ---- the step ----

  go(i, say = true) {
    if (!this.model) return;
    const n = this.model.steps.length;
    const was = this.step;
    this.step = clamp(i, 0, n);
    if (this.step !== was) this.all = false;
    this.show(say);
  }

  // show draws the frame of the step that is chosen: the places stay, the states change.
  show(route = true) {
    const m = this.model, e = this.els, i = this.step;
    if (!m || !e.stage) return;
    const f = m.frames[i];
    const step = i > 0 ? m.steps[i - 1] : null;
    for (const c of m.cells) {
      const mark = f.marks.get(c.i);
      const v = f.state[c.i];
      const cls = 'sq tl-c' + sizeFor(m.cells.length) + (mark ? ' ' + CLASS[mark] + ' fresh' : v === HERE ? ' tl-on' : v === GONE ? ' tl-gone' : ' tl-off');
      if (e.cells[c.i].className !== cls) e.cells[c.i].className = cls;
    }
    e.stage.setAttribute('data-step', String(i));
    // the readout
    const parts = ORDER.filter((a) => f.counts.by[a]).map((a) => h('span', { class: 'tl-ct' }, h('span', { class: 'sq ' + CLASS[a], 'aria-hidden': 'true' }), `${fmt(f.counts.by[a])} ${WORD[a]}`));
    clear(e.readout);
    e.readout.append(
      h('div', { class: 'tl-r' },
        h('p', { class: 'tl-k' }, step ? `Run ${i} of ${m.steps.length}` : 'Before the first run'),
        step ? h('p', { class: 'tl-when' }, stamp(step.time)) : h('p', { class: 'tl-when dim' }, 'what the runs did not make')),
      h('div', { class: 'tl-r tl-what' },
        step ? h('p', { class: 'tl-line' }, h('span', { class: 'tl-kind' }, step.kind), step.status !== 'applied' && step.status !== 'done' ? h('span', { class: 'tl-st' }, step.status) : null, who(step) ? h('span', null, ' by ', h('b', null, who(step))) : null,
          step.git_revision ? h('span', { class: 'dim' }, ' · ' + shortRev(step.git_revision)) : null) : h('p', { class: 'tl-line dim' }, 'The resources that no run made.'),
        step && step.reason ? h('p', { class: 'tl-reason' }, step.reason) : null,
        step ? h('p', { class: 'tl-cmd' }, h('code', null, step.command || '')) : null,
        h('p', { class: 'tl-cts' }, parts.length ? parts : h('span', { class: 'dim' }, 'no change'))),
      h('div', { class: 'tl-r tl-num' }, h('p', { class: 'tl-k' }, 'Resources'), h('p', { class: 'tl-n' }, fmt(f.counts.here))));
    // the timeline
    e.bars.forEach((b, k) => { b.classList.toggle('past', k + 1 <= i); b.classList.toggle('cur', k + 1 === i); });
    e.strip.setAttribute('aria-valuenow', String(i));
    e.strip.setAttribute('aria-valuetext', step ? `Run ${i} of ${m.steps.length}, ${stamp(step.time)}, ${step.kind}` : 'Before the first run');
    e.back.disabled = i <= 0;
    e.next.disabled = i >= m.steps.length;
    this.drawPlay();
    this.drawChanges();
    this.drawRuns();
    if (route && this.cb.onRoute) this.cb.onRoute();
  }

  drawPlay() {
    const e = this.els;
    const end = this.step >= this.model.steps.length;
    clear(e.play);
    e.play.append(icon(this.playing ? 'pause' : end ? 'replay' : 'play'), h('span', null, this.playing ? 'Pause' : end ? 'Replay' : 'Play'));
    e.play.setAttribute('aria-pressed', this.playing ? 'true' : 'false');
  }

  // drawChanges lists what the run did, so that a change is a name and not only a lit square.
  drawChanges() {
    const m = this.model, e = this.els;
    clear(e.changes);
    if (this.step === 0) return;
    const step = m.steps[this.step - 1];
    const list = step.changes.slice().sort((a, b) => ORDER.indexOf(a.action) - ORDER.indexOf(b.action));
    const shown = this.all ? list : list.slice(0, LISTED);
    e.changes.append(h('h3', { class: 'h', id: 'tl-h-ch' }, `What run ${this.step} did`),
      h('ul', { class: 'tl-list' }, shown.map((c) => h('li', null,
        h('span', { class: 'sq ' + CLASS[c.action], 'aria-hidden': 'true' }),
        h('span', { class: 'tl-w' }, WORD[c.action]),
        h('button', { type: 'button', class: 'tl-addr', title: 'Open in the inventory', on: { click: () => this.cb.onOpen && this.cb.onOpen(c.addr) } }, c.addr)))),
      ...[
        list.length > shown.length ? h('button', { type: 'button', class: 'btn tl-more', on: { click: () => { this.all = true; this.drawChanges(); } } }, `Show the other ${fmt(list.length - shown.length)}`) : null,
        step.more ? h('p', { class: 'tl-note' }, `${plural(step.more, 'more change')} of this run are not listed.`) : null,
      ].filter(Boolean));
  }

  drawRuns() {
    const m = this.model, e = this.els;
    const rows = m.steps.map((s, k) => {
      const i = k + 1;
      const c = m.frames[i].counts;
      const sig = s.signature ? signatureMark(s.signature, s.signer) : null;
      const tr = h('tr', { class: i === this.step ? 'cur' : '', 'aria-current': i === this.step ? 'step' : null },
        h('td', { class: 'n' }, String(i)),
        h('td', null, h('button', { type: 'button', class: 'tl-pick', on: { click: () => { this.stop(); this.go(i); } } }, stamp(s.time))),
        h('td', null, h('span', { class: 'tl-kind' }, s.kind), who(s) ? ' ' + who(s) : ''),
        h('td', null, h('div', { class: 'tl-ch' }, ORDER.filter((a) => c.by[a]).map((a) => h('span', { class: 'tl-ct' }, h('span', { class: 'sq tiny ' + CLASS[a], 'aria-hidden': 'true' }), String(c.by[a]))))),
        h('td', { class: 'n' }, fmt(c.here)),
        h('td', null, sig));
      return tr;
    });
    clear(e.runs);
    e.runs.append(h('h3', { class: 'h', id: 'tl-h-runs' }, 'The runs'),
      h('div', { class: 'tl-scroll' }, h('table', { class: 'tl-table' },
        h('thead', null, h('tr', null, h('th', { class: 'n' }, '#'), h('th', null, 'When'), h('th', null, 'Run'), h('th', null, 'Changes'), h('th', { class: 'n' }, 'Resources after'), h('th', null, 'Signature'))),
        h('tbody', null, rows))));
    const cur = e.runs.querySelector('tr.cur');
    if (cur && this.playing) cur.scrollIntoView({ block: 'nearest' });
  }

  // ---- playing ----

  toggle() {
    if (this.playing) { this.stop(); return; }
    if (this.step >= this.model.steps.length) this.go(0);
    this.playing = true;
    const ms = clamp(14000 / this.model.steps.length, PLAY_MS[0], PLAY_MS[1]);
    this.timer = setInterval(() => {
      if (this.step >= this.model.steps.length) { this.stop(); return; }
      this.go(this.step + 1);
      if (this.step >= this.model.steps.length) this.stop();
    }, motionOn() ? ms : Math.max(ms, 600));
    this.drawPlay();
  }

  stop() {
    clearInterval(this.timer);
    this.timer = 0;
    if (this.playing) { this.playing = false; if (this.els.play && this.model) this.drawPlay(); }
  }

  scrub(ev) {
    ev.preventDefault();
    this.stop();
    const strip = this.els.strip;
    const at = (x) => {
      const r = strip.getBoundingClientRect();
      return clamp(Math.round(((x - r.left) / Math.max(1, r.width)) * this.model.steps.length + 0.5), 1, this.model.steps.length);
    };
    strip.focus({ preventScroll: true });
    this.go(at(ev.clientX));
    const move = (e) => this.go(at(e.clientX));
    const up = () => { removeEventListener('pointermove', move); removeEventListener('pointerup', up); };
    addEventListener('pointermove', move);
    addEventListener('pointerup', up);
  }

  key(ev) {
    const n = this.model.steps.length;
    const to = { ArrowLeft: this.step - 1, ArrowDown: this.step - 1, ArrowRight: this.step + 1, ArrowUp: this.step + 1, PageDown: this.step - 10, PageUp: this.step + 10, Home: 0, End: n }[ev.key];
    if (ev.key === ' ' || ev.key === 'Enter') { ev.preventDefault(); this.toggle(); return; }
    if (to === undefined || ev.altKey || ev.ctrlKey || ev.metaKey) return;
    ev.preventDefault();
    this.stop();
    this.go(to);
  }
}
