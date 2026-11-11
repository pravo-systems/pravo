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
import { build, activity, neighbours, HERE, GONE, CLASS, WORD, ORDER } from './lapse-model.js';

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
    this.cb = cb; // nodes() lists the nodes of the graph, revision() is the revision of the project, onOpen(addr) opens a node in the inventory, onPick() says the picked resource or the run changed, related(addr) lists what a node depends on and what depends on it, onRoute() says the address changed
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
    this.sel = ''; // the resource that is picked: the panel shows it as the shown run left it
    this.tab = 'state'; // the tab of the panel: state or history
    this.rel = { deps: new Set(), used: new Set() };
    this.wantRun = ''; // a run to show when the runs are read
    this.els = {};
  }

  // ---- the address ----

  route() {
    return { ls: this.model && this.step > 0 && this.step < this.model.steps.length ? String(this.step) : '', lr: this.sel, lt: this.sel && this.tab === 'history' ? 'history' : '' };
  }

  setRoute(f) {
    const n = Number(f.ls);
    this.want = Number.isInteger(n) && n > 0 ? n : 0;
    this.sel = f.lr || '';
    this.tab = f.lt === 'history' ? 'history' : 'state';
    this.related();
    if (this.model && this.want) this.go(Math.min(this.want, this.model.steps.length), false);
    else if (this.model && this.els.stage) this.show(false);
  }

  // ---- the resource that is picked ----

  // pick shows a resource as the run that is shown left it; a second click on the same one lets it go. An address that is not a
  // square of the film (an output, a data source) is opened in the inventory.
  pick(addr) {
    if (!addr) { this.sel = ''; } else if (this.model && !this.model.byAddr.has(addr)) { if (this.cb.onOpen) this.cb.onOpen(addr); return; } else this.sel = addr === this.sel ? '' : addr;
    this.related();
    if (this.els.stage) this.show(false);
    const c = this.sel && this.model && this.model.byAddr.get(this.sel);
    if (c && this.els.cells && this.els.cells[c.i]) this.els.cells[c.i].scrollIntoView({ block: 'nearest', inline: 'nearest' });
    if (this.cb.onPick) this.cb.onPick();
    if (this.cb.onRoute) this.cb.onRoute();
  }

  setTab(t) {
    this.tab = t === 'history' ? 'history' : 'state';
    if (this.cb.onPick) this.cb.onPick();
    if (this.cb.onRoute) this.cb.onRoute();
  }

  // related reads what the picked resource is connected to, from what the state records now.
  related() {
    const r = this.sel && this.cb.related ? this.cb.related(this.sel) : null;
    this.rel = { deps: new Set((r && r.deps) || []), used: new Set((r && r.used) || []) };
  }

  // goToRun shows the film at the run with this id, and picks the resource when there is one.
  goToRun(id, addr) {
    if (!this.model) return false;
    const i = this.model.steps.findIndex((s) => s.id === id);
    if (i < 0) return false;
    this.stop();
    if (addr) { this.sel = addr; this.related(); }
    this.go(i + 1);
    return true;
  }

  // focusRun asks for the film at the run with this id, with the resource picked: at once when the runs are read, else when they are.
  focusRun(id, addr) {
    this.sel = addr || this.sel;
    this.related();
    this.wantRun = id;
    if (this.model) { this.goToRun(id); this.wantRun = ''; }
  }

  // asOf is what the panel is told: the run that is shown, and the nearest runs that wrote the resource before and after it.
  asOf() {
    const m = this.model, i = this.step;
    const c = m && m.byAddr.get(this.sel);
    if (!c) return null;
    const near = neighbours(m, this.sel, i);
    const step = i > 0 ? m.steps[i - 1] : null;
    const v = i > 0 ? m.frames[i].state[c.i] : -1;
    return { id: step ? step.id : '', label: step ? `After run ${i} of ${m.steps.length}` : 'Before the first run', when: step ? stamp(step.time) : '', by: step ? who(step) : '',
      here: !step ? 'before' : v === HERE ? 'here' : v === GONE ? 'gone' : 'not-yet', hasPrev: near.prev > 0, hasNext: near.next > 0, run: i, runs: m.steps.length };
  }

  picked() {
    const a = this.asOf();
    return a ? { addr: this.sel, tab: this.tab, asOf: a } : null;
  }

  // along walks to the run before or after the shown one that wrote the picked resource.
  along(dir) {
    const near = neighbours(this.model, this.sel, this.step);
    const to = dir === 'prev' ? near.prev : near.next;
    if (to) { this.stop(); this.go(to); }
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
    if (this.wantRun) { const k = this.model.steps.findIndex((x) => x.id === this.wantRun); if (k >= 0) this.want = k + 1; this.wantRun = ''; }
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
      h('p', { class: 'tl-lede' }, 'The estate as the runs left it, one run after another. Every resource keeps its place, so that only what a run did moves. Pick a resource to see it as the run left it, and every run that changed it.'));
    let body;
    if (this.error) body = h('p', { class: 'tl-empty' }, this.error);
    else if (!this.doc) body = busy('Reading the runs…');
    else if (!this.model.steps.length) body = h('p', { class: 'tl-empty' }, 'No run has written a resource yet. The film is made from the runs that the state records, and the first apply starts it.');
    else body = this.film();
    this.root.append(h('div', { class: 'tl-root' }, head, body, h('p', { class: 'tl-foot' }, FOOTER)));
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
        el.addEventListener('click', () => this.pick(c.addr));
        e.cells[c.i] = el;
        grid.append(el);
      }
      e.stage.append(h('div', { class: 'tl-g' }, h('p', { class: 'tl-gn', title: g.ns || 'the root' }, g.ns || 'root', h('span', { class: 'tl-gc' }, String(g.cells.length))), grid));
    }
    e.legend = h('ul', { class: 'tl-key', 'aria-label': 'Key' },
      ORDER.map((a) => h('li', null, h('span', { class: 'sq ' + CLASS[a], 'aria-hidden': 'true' }), WORD[a])),
      h('li', null, h('span', { class: 'sq tl-on', 'aria-hidden': 'true' }), 'there'),
      h('li', null, h('span', { class: 'sq tl-gone', 'aria-hidden': 'true' }), 'was there'));
    e.rel = h('ul', { class: 'tl-key tl-rel', 'aria-label': 'Connections of the picked resource', hidden: true });
    e.back = h('button', { type: 'button', class: 'btn tl-btn', 'aria-label': 'Previous run', on: { click: () => { this.stop(); this.go(this.step - 1); } } }, icon('arrow-l'));
    e.play = h('button', { type: 'button', class: 'btn primary tl-btn tl-play', on: { click: () => this.toggle() } });
    e.next = h('button', { type: 'button', class: 'btn tl-btn', 'aria-label': 'Next run', on: { click: () => { this.stop(); this.go(this.step + 1); } } }, icon('arrow'));
    const act = activity(m);
    const top = Math.max(1, ...act);
    e.bars = act.map((n, i) => h('i', { class: 'tl-bar', style: `height: ${Math.round(3 + (BAR_MAX - 3) * Math.sqrt(n / top))}px`, 'aria-hidden': 'true', data: { i: String(i + 1) } }));
    e.ticks = m.steps.map(() => h('i', { class: 'tl-tick', 'aria-hidden': 'true' }));
    e.tickrow = h('div', { class: 'tl-ticks', 'aria-hidden': 'true' }, e.ticks);
    e.strip = h('div', { class: 'tl-strip', role: 'slider', tabIndex: 0, 'aria-label': 'Run', 'aria-valuemin': '0', 'aria-valuemax': String(m.steps.length) }, e.bars);
    e.strip.addEventListener('pointerdown', (ev) => this.scrub(ev));
    e.strip.addEventListener('keydown', (ev) => this.key(ev));
    const first = m.steps[0], last = m.steps[m.steps.length - 1];
    e.changes = h('section', { class: 'tl-sec', 'aria-labelledby': 'tl-h-ch' });
    e.runs = h('section', { class: 'tl-sec', 'aria-labelledby': 'tl-h-runs' });
    const older = this.doc.older ? h('p', { class: 'tl-memo' }, `${plural(this.doc.older, 'older run')} are left out. What they made is shown as there from the start.`) : null;
    return h('div', { class: 'tl-film' },
      e.readout,
      e.stage,
      e.legend,
      e.rel,
      h('div', { class: 'tl-ctl' },
        h('div', { class: 'tl-btns' }, e.back, e.play, e.next),
        h('div', { class: 'tl-time' }, e.strip, e.tickrow, h('div', { class: 'tl-ends' }, h('span', null, stamp(first.time)), h('span', null, stamp(last.time))))),
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
      const cls = 'sq tl-c' + sizeFor(m.cells.length) + (mark ? ' ' + CLASS[mark] + ' fresh' : v === HERE ? ' tl-on' : v === GONE ? ' tl-gone' : ' tl-off')
        + (c.addr === this.sel ? ' sel' : this.rel.deps.has(c.addr) ? ' rel-dep' : this.rel.used.has(c.addr) ? ' rel-use' : '');
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
    this.drawTicks();
    this.drawRel();
    this.drawPlay();
    this.drawChanges();
    this.drawRuns();
    if (!this.playing && this.cb.onPick) this.cb.onPick();
    if (route && this.cb.onRoute) this.cb.onRoute();
  }

  // drawTicks marks, under the timeline, the runs that wrote the picked resource, in the colour of what each did to it.
  drawTicks() {
    const e = this.els, m = this.model;
    const mine = new Map(((this.sel && m.touches.get(this.sel)) || []).map((t) => [t.step, t.action]));
    e.ticks.forEach((t, k) => { const a = mine.get(k + 1); t.className = 'tl-tick' + (a ? ' on ' + CLASS[a] : ''); });
    e.tickrow.classList.toggle('on', mine.size > 0);
  }

  // drawRel says what the outlines mean when a resource is picked: what it depends on and what depends on it, as the state records them now.
  drawRel() {
    const e = this.els, m = this.model;
    const deps = [...this.rel.deps].filter((a) => m.byAddr.has(a)).length, used = [...this.rel.used].filter((a) => m.byAddr.has(a)).length;
    clear(e.rel);
    e.rel.hidden = !this.sel;
    if (!this.sel) return;
    e.rel.append(
      h('li', null, h('span', { class: 'sq tl-c sel', 'aria-hidden': 'true' }), 'picked'),
      deps ? h('li', null, h('span', { class: 'sq tl-c rel-dep', 'aria-hidden': 'true' }), `it depends on ${deps}`) : null,
      used ? h('li', null, h('span', { class: 'sq tl-c rel-use', 'aria-hidden': 'true' }), `${used} depend${used === 1 ? 's' : ''} on it`) : null,
      !deps && !used ? h('li', { class: 'dim' }, 'The state records no connection for it.') : h('li', { class: 'dim' }, 'Connections are as the state records them now.'));
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
        h('button', { type: 'button', class: 'tl-addr' + (c.addr === this.sel ? ' sel' : ''), title: 'Pick it: see it as this run left it', on: { click: () => this.pick(c.addr) } }, c.addr)))),
      ...[
        list.length > shown.length ? h('button', { type: 'button', class: 'btn tl-more', on: { click: () => { this.all = true; this.drawChanges(); } } }, `Show the other ${fmt(list.length - shown.length)}`) : null,
        step.more ? h('p', { class: 'tl-memo' }, `${plural(step.more, 'more change')} of this run are not listed.`) : null,
      ].filter(Boolean));
  }

  drawRuns() {
    const m = this.model, e = this.els;
    const rows = m.steps.map((s, k) => {
      const i = k + 1;
      const c = m.frames[i].counts;
      const sig = s.signature ? signatureMark(s.signature, s.signer) : null;
      const mine = this.sel && (m.touches.get(this.sel) || []).find((t) => t.step === i);
      const tr = h('tr', { class: (i === this.step ? 'cur' : '') + (mine ? ' mine' : ''), 'aria-current': i === this.step ? 'step' : null },
        h('td', { class: 'n' }, String(i)),
        h('td', null, h('button', { type: 'button', class: 'tl-pick', on: { click: () => { this.stop(); this.go(i); } } }, stamp(s.time))),
        h('td', null, h('span', { class: 'tl-kind' }, s.kind), who(s) ? ' ' + who(s) : ''),
        h('td', null, h('div', { class: 'tl-ch' }, ORDER.filter((a) => c.by[a]).map((a) => h('span', { class: 'tl-ct' }, h('span', { class: 'sq tiny ' + CLASS[a], 'aria-hidden': 'true' }), String(c.by[a]))))),
        h('td', { class: 'n' }, fmt(c.here)),
        this.sel ? h('td', null, mine ? h('span', { class: 'tl-ct' }, h('span', { class: 'sq tiny ' + CLASS[mine.action], 'aria-hidden': 'true' }), WORD[mine.action]) : null) : null,
        h('td', null, sig));
      return tr;
    });
    clear(e.runs);
    e.runs.append(h('h3', { class: 'h', id: 'tl-h-runs' }, 'The runs'),
      h('div', { class: 'tl-scroll' }, h('table', { class: 'tl-table' },
        h('thead', null, h('tr', null, h('th', { class: 'n' }, '#'), h('th', null, 'When'), h('th', null, 'Run'), h('th', null, 'Changes'), h('th', { class: 'n' }, 'Resources after'), this.sel ? h('th', null, 'The picked one') : null, h('th', null, 'Signature'))),
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
    if (this.playing) { this.playing = false; if (this.els.play && this.model) { this.drawPlay(); if (this.sel && this.cb.onPick) this.cb.onPick(); } }
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
