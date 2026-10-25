// The Traces view: the runs that pravo recorded in the project, and one of them as a timeline.
//
// A command that did some work (a plan, an apply, a discovery, a read of the state) leaves its spans in .pravo/traces, and the page reads
// them from /api/traces and /api/trace. The timeline has the time of the run across and the nesting of the spans down:
// a span that runs at the same time as another sits in a row of its own, so the width of a bar is how long it took and
// its row says what it was part of. The table lists the same spans by how long they took, which is the question when
// something is slow. Nothing here runs a command or writes a file.

import { h, s as svg, clear, icon, plural, relTime, stamp, clamp, fmt } from './util.js';
import * as api from './api.js';
import { build, fmtDur, ticks, path as pathOf, matcher, labelOf, LANG } from './trace-model.js';
import { Pills } from './pills.js';
import { QueryMenu, removeTerm, addTerm, removeValues, parseQuery } from './query.js';
import { suggest, tally, hasKind, hasErrors, toggleKind, toggleErrors } from './tquery.js';

const ROW = 22; // the height of a row of the timeline
const BAR = 18; // and of a bar in it
const MIN_WINDOW = 20; // the narrowest window, in microseconds
const PAGE = 300; // rows of the table before the button that shows more
const CHAR = 6.4; // the width of a character of the label of a bar, in pixels

const KIND = { command: 'command', engine: 'engine', phase: 'phase', setup: 'setup', resource: 'resource', provider: 'provider call', state: 'state request', seal: 'seal', mcp: 'tool call', other: 'span' };
const CHIP = { engine: 'Engine', phase: 'Phases', setup: 'Setup', resource: 'Resources', provider: 'Providers', state: 'State', seal: 'Sealing', mcp: 'Tools' };

export const FOOTER = 'This page reads the runs that pravo recorded. It runs nothing and writes nothing.';

// subject adds the phase and the action to the label of a resource, for the table and the detail.
function phaseOf(sp) {
  const a = sp.attrs || {};
  return [a['pravo.phase'], a['pravo.action']].filter(Boolean).join(' · ');
}

export class TracesView {
  constructor(root, cb = {}) {
    this.root = root;
    this.cb = cb;
    this.list = null; // the answer of /api/traces
    this.error = '';
    this.id = ''; // the run that is open
    this.want = ''; // the run that the address names, until the list is read
    this.doc = null;
    this.model = null;
    this.loading = false;
    this.sel = null; // the id of the selected span
    this.mode = 'flame'; // 'flame' or 'table'
    this.q = '';
    this.match = null;
    this.v0 = 0; // the window of the timeline, in microseconds from the start of the run
    this.v1 = 1;
    this.sort = { by: 'dur', dir: -1 };
    this.shown = PAGE;
    this.follow = true; // the newest run is the one open: a run that lands is opened
    this.seq = 0;
    this.active = false;
    this.tip = null;
    this.raf = 0;
    this.errorsOnly = false;
  }

  // ---- the address ----

  route() {
    const zoom = this.model && (this.v0 > 0.5 || this.v1 < this.model.dur - 0.5) ? `${Math.round(this.v0)}-${Math.round(this.v1)}` : '';
    return { trace: this.id && !(this.follow && this.list && this.list.traces[0] && this.list.traces[0].id === this.id) ? this.id : '', span: this.sel || '', tv: this.mode === 'table' ? 'table' : '', tz: zoom, tq: this.q };
  }

  setRoute(f) {
    this.want = f.trace || '';
    this.wantSpan = f.span || '';
    this.mode = f.tv === 'table' ? 'table' : 'flame';
    this.q = f.tq || '';
    this.match = matcher(this.q);
    const m = /^(\d+)-(\d+)$/.exec(f.tz || '');
    this.wantZoom = m ? [Number(m[1]), Number(m[2])] : null;
    if (this.want) this.follow = false;
  }

  // ---- loading ----

  // open is called when the section is shown: the list is read again, and the run that is open with it.
  async open() {
    this.active = true;
    await this.reload();
  }

  close() {
    this.active = false;
    this.hideTip();
  }

  // changed is the page being told that a run was recorded.
  changed() {
    if (this.active) this.reload();
    else this.list = null; // read when the section is next shown
  }

  async reload() {
    const seq = ++this.seq;
    let list;
    try { list = await api.traces(); } catch (err) {
      if (seq !== this.seq) return;
      if (err instanceof api.Unauthorized || err instanceof api.Unreachable) return;
      this.error = err.message || String(err);
      this.render();
      return;
    }
    if (seq !== this.seq) return;
    this.error = '';
    const first = !this.list;
    this.list = list;
    const ids = list.traces.map((x) => x.id);
    let id = this.id;
    if (this.want && ids.includes(this.want)) { id = this.want; this.want = ''; } else if (this.want && first) { this.notFound = this.want; this.want = ''; }
    // the newest run is followed unless somebody chose another: a run that lands is the one to look at
    if (!ids.includes(id) || (this.follow && ids[0] && id !== ids[0])) id = ids[0] || '';
    if (id !== this.id || !this.doc) { await this.load(id, first); if (seq !== this.seq) return; }
    this.render();
  }

  async load(id, restore) {
    this.id = id;
    this.doc = this.model = null;
    this.sel = null;
    if (!id) { this.render(); return; }
    this.loading = true;
    this.render();
    let doc;
    try { doc = await api.trace(id); } catch (err) {
      this.loading = false;
      if (err instanceof api.Unauthorized || err instanceof api.Unreachable) return;
      this.error = `The run could not be read: ${err.message || err}`;
      this.id = '';
      return;
    }
    this.loading = false;
    if (this.id !== id) return;
    this.error = '';
    this.doc = doc;
    this.model = build(doc);
    this.v0 = 0;
    this.v1 = Math.max(this.model.dur, 1);
    this.shown = PAGE;
    if (restore && this.wantSpan && this.model.byId.has(this.wantSpan)) this.sel = this.wantSpan;
    if (restore && this.wantZoom) this.setWindow(this.wantZoom[0], this.wantZoom[1]);
    this.wantSpan = '';
    this.wantZoom = null;
  }

  pick(id, user = true) {
    if (user) this.follow = !!(this.list && this.list.traces[0] && this.list.traces[0].id === id);
    if (id === this.id) return;
    this.load(id, false).then(() => { this.render(); this.cb.onRoute && this.cb.onRoute(); });
  }

  // ---- drawing the page ----

  render() {
    if (!this.active) return;
    const top = this.root.scrollTop;
    const keepFocus = document.activeElement && this.root.contains(document.activeElement) ? document.activeElement.dataset.fk : null;
    this.hideTip();
    clear(this.root);
    this.plot = this.canvas = this.ruler = null;
    const list = this.list;
    const body = !list ? h('p', { class: 'tr-empty' }, this.error ? '' : 'Reading the runs')
      : !list.traces.length ? this.none(list)
        : h('div', { class: 'tr-layout' }, this.runs(list), this.main());
    this.root.append(h('div', { class: 'tr' },
      h('header', { class: 'tr-head' },
        h('div', { class: 'tr-bhead' }, h('h2', { class: 'tr-title', id: 'tr-h-title' }, 'Traces'),
          list && list.traces.length ? h('span', { class: 'b' }, plural(list.traces.length, 'run')) : null),
        h('p', { class: 'tr-lede' }, 'With PRAVO_TRACES=on, every command that does some work is recorded as a trace: the spans of the command, its setup, the stages of the engine, each resource, each call to a provider and each request to the state, with the time each took. Open one to see where a run spent its time or where it stopped.')),
      this.error ? h('p', { class: 'tr-warn', role: 'alert' }, icon('bang'), h('span', null, this.error)) : null,
      this.notFound ? h('p', { class: 'tr-warn', role: 'note' }, icon('bang'), h('span', null, 'The run in the address is not in the directory any more.')) : null,
      body,
      h('p', { class: 'tr-foot' }, FOOTER)));
    this.notFound = '';
    this.root.scrollTop = top;
    if (this.model && this.mode === 'flame') this.layoutPlot();
    if (keepFocus) {
      const el = Array.from(this.root.querySelectorAll('[data-fk]')).find((x) => x.dataset.fk === keepFocus);
      if (el) el.focus({ preventScroll: true });
    }
  }

  none(list) {
    const cmd = (text) => h('div', { class: 'cmdline' }, h('span', { class: 'ps', 'aria-hidden': 'true' }, '$'), h('code', null, text),
      h('button', { class: 'cpbtn', type: 'button', 'data-copy': text, 'aria-label': 'Copy the command', 'data-tip': 'Copy the command' }, icon('copy')));
    return h('div', { class: 'tr-none' },
      h('h3', { class: 'tr-nh' }, 'Recording is off'),
      h('p', null, 'pravo records nothing about a run unless you ask it to. Set ', h('code', null, 'PRAVO_TRACES=on'), ' for the command, and its trace is written here when it ends:'),
      cmd('PRAVO_TRACES=on pravo plan'),
      h('p', null, 'To record every command of a shell session, export it:'),
      cmd('export PRAVO_TRACES=on'),
      h('p', { class: 'tr-note' }, 'Only a command that did some work leaves a trace. The runs are read from ', h('code', null, list.dir), ', and ', h('code', null, 'PRAVO_TRACE_DIR'),
        ' moves that folder. The newest 100 are kept, and the folder ignores itself in git. A trace holds names, kinds, timings and counts, never the values of attributes.'));
  }

  // runs is the list of recorded runs, newest first.
  runs(list) {
    const items = list.traces.map((t) => {
      const on = t.id === this.id;
      const failed = t.status === 'error';
      return h('li', null, h('button', { class: 'tr-run' + (failed ? ' bad' : ''), type: 'button', 'aria-pressed': on ? 'true' : 'false', 'data-fk': 'run-' + t.id, 'data-id': t.id,
        'aria-label': `${t.command}, ${relTime(t.start)}, ${fmtDur(t.duration_us)}${failed ? ', failed' : ''}`, on: { click: () => this.pick(t.id) } },
      h('span', { class: 'tr-st', 'aria-hidden': 'true' }),
      h('span', { class: 'tr-r1' }, h('span', { class: 'tr-cmd' }, t.line || t.command), h('span', { class: 'tr-dur' }, fmtDur(t.duration_us))),
      h('span', { class: 'tr-r2' }, h('span', null, relTime(t.start)), h('span', null, t.resources ? plural(t.resources, 'resource') : plural(t.span_count, 'span')), failed ? h('span', { class: 'tr-bad' }, 'failed') : null),
      t.reason ? h('span', { class: 'tr-r3' }, t.reason) : null));
    });
    return h('nav', { class: 'tr-runs', 'aria-label': 'Recorded runs' },
      h('ul', null, items),
      list.omitted ? h('p', { class: 'tr-note' }, `${plural(list.omitted, 'older run')} not listed.`) : null,
      list.unreadable ? h('p', { class: 'tr-note' }, `${plural(list.unreadable, 'file')} in ${list.dir} ${list.unreadable === 1 ? 'is' : 'are'} not a run of this version.`) : null);
  }

  main() {
    if (this.loading && !this.model) return h('section', { class: 'tr-main' }, h('p', { class: 'tr-empty' }, 'Reading the run'));
    if (!this.model) return h('section', { class: 'tr-main' }, h('p', { class: 'tr-empty' }, 'Choose a run.'));
    const d = this.doc;
    return h('section', { class: 'tr-main', 'aria-label': `The run ${d.command}` },
      this.summary(d),
      this.notices(d),
      this.toolbar(),
      this.mode === 'table' ? this.table() : this.flame(),
      this.detail());
  }

  summary(d) {
    const failed = d.status === 'error';
    const fact = (k, v, copy) => h('li', null, h('span', { class: 'k' }, k), h('span', { class: 'v' }, v, copy ? h('button', { class: 'cpbtn', type: 'button', 'data-copy': copy, 'aria-label': `Copy ${k.toLowerCase()}`, 'data-tip': `Copy ${k.toLowerCase()}` }, icon('copy')) : null));
    return h('div', { class: 'tr-sum' + (failed ? ' bad' : '') },
      h('div', { class: 'tr-sumhead' },
        h('h3', { class: 'tr-name' }, d.line || d.command),
        failed ? h('span', { class: 'b bad' }, icon('bang'), `${plural(d.errors, 'span')} failed`) : h('span', { class: 'b ok' }, icon('check'), 'no span failed'),
        d.exit >= 0 ? h('span', { class: 'b' }, `exit ${d.exit}`) : null),
      h('ul', { class: 'tr-facts' },
        fact('Started', h('span', { 'data-tip': stamp(d.start) }, relTime(d.start))),
        fact('Took', fmtDur(d.duration_us)),
        fact('Spans', fmt(d.span_count)),
        d.resources ? fact('Resources', fmt(d.resources)) : null,
        d.txn ? fact('Transaction', h('code', null, d.txn), d.txn) : null,
        d.actor ? fact('Actor', d.actor) : null,
        d.reason ? fact('Reason', d.reason) : null,
        fact('Trace', h('code', null, d.trace_id.slice(0, 8) + '…'), d.trace_id)));
  }

  notices(d) {
    const out = [];
    if (d.dropped) out.push(`${plural(d.dropped, 'provider call')} ${d.dropped === 1 ? 'was' : 'were'} left out of this run when it was recorded, because it is very large.`);
    if (d.omitted) out.push(`${plural(d.omitted, 'span')} ${d.omitted === 1 ? 'is' : 'are'} not shown: the page shows the first ${fmt(d.spans.length)}.`);
    return out.length ? h('div', { class: 'tr-notices' }, out.map((t) => h('p', { class: 'tr-warn', role: 'note' }, icon('bang'), h('span', null, t)))) : null;
  }

  toolbar() {
    const input = h('input', { type: 'search', class: 'tr-q', 'data-fk': 'tq', placeholder: 'Filter, e.g. api.db  is:error  kind:provider', autocomplete: 'off', spellcheck: 'false', value: this.q,
      'aria-label': 'Filter the spans by words in their name, message or attributes, or by column:value such as is:error, kind:resource, kind:state, provider:aws or type:aws_s3*' });
    this.input = input;
    this.count = h('span', { class: 'count', 'aria-live': 'polite' });
    this.pillsEl = h('div', { class: 'pills tr-pills', role: 'group', 'aria-label': 'Active filters' });
    this.pills = new Pills(this.pillsEl, { onRemoveTerm: (raw) => this.setQuery(removeTerm(this.q, raw)) }, LANG);
    this.chipsEl = h('div', { class: 'tr-kinds', role: 'group', 'aria-label': 'Show only the spans of a kind' });
    this.chipsEl.addEventListener('click', (ev) => {
      const b = ev.target.closest('button[data-chip]');
      if (!b) return;
      this.setQuery(b.dataset.chip === 'is:error' ? toggleErrors(this.q) : toggleKind(this.q, b.dataset.chip));
    });
    const box = h('div', { class: 'filterbox' }, icon('search'), input, this.count);
    let t = 0;
    input.addEventListener('input', () => { clearTimeout(t); t = setTimeout(() => this.setQuery(input.value, true), 120); });
    new QueryMenu(input, box, { model: () => this.model, suggest, change: (v) => { clearTimeout(t); this.setQuery(v, true); } });
    const mode = (m, label, ic) => h('button', { type: 'button', 'aria-pressed': this.mode === m ? 'true' : 'false', 'data-fk': 'mode-' + m, on: { click: () => this.setMode(m) } }, icon(ic), label);
    const bar = h('div', { class: 'tr-bar' },
      h('div', { class: 'seg', role: 'group', 'aria-label': 'Show the spans as' }, mode('flame', 'Timeline', 'trace'), mode('table', 'Slowest', 'stack')),
      box,
      this.mode === 'flame' ? h('div', { class: 'seg zoom', role: 'group', 'aria-label': 'Zoom the timeline' },
        h('button', { type: 'button', 'aria-label': 'Zoom out', title: 'Zoom out ( - )', 'data-fk': 'zout', on: { click: () => this.zoomBy(2) } }, icon('minus')),
        h('button', { type: 'button', title: 'Show the whole run ( 0 )', 'data-fk': 'fit', on: { click: () => this.fit() } }, 'Fit'),
        h('button', { type: 'button', 'aria-label': 'Zoom in', title: 'Zoom in ( + )', 'data-fk': 'zin', on: { click: () => this.zoomBy(0.5) } }, icon('plus'))) : null);
    this.pills.render(this.q);
    this.chips();
    this.tally();
    return h('div', { class: 'tr-tools' }, bar, h('div', { class: 'tr-filters' }, this.chipsEl, this.pillsEl));
  }

  // chips are the kinds of span that the run has, to filter by with a click, and the failed spans when there are any.
  chips() {
    if (!this.chipsEl || !this.model) return;
    clear(this.chipsEl);
    const chip = (key, kind, label, n, on) => h('button', { type: 'button', class: 'tr-chip', 'aria-pressed': on ? 'true' : 'false', 'data-chip': key, 'data-fk': 'chip-' + key },
      kind ? h('span', { class: 'tr-sw k-' + kind, 'aria-hidden': 'true' }) : null, label, h('em', null, fmt(n)));
    for (const [k, n] of tally(this.model, 'kind')) if (CHIP[k]) this.chipsEl.append(chip(k, k, CHIP[k], n, hasKind(this.q, k)));
    const err = tally(this.model, 'is').find(([v]) => v === 'error');
    if (err) this.chipsEl.append(chip('is:error', 'err', 'Errors', err[1], hasErrors(this.q)));
  }

  tally() {
    if (!this.count || !this.model) return;
    if (!this.match) { this.count.textContent = plural(this.model.spans.length, 'span'); return; }
    const n = this.model.spans.filter(this.match).length;
    this.count.textContent = plural(n, 'match', 'matches');
  }

  setQuery(q, typed = false) {
    this.q = q;
    this.match = matcher(q);
    if (this.input && !typed && this.input.value !== q) this.input.value = q;
    if (this.pills) this.pills.render(q);
    this.chips();
    this.tally();
    if (this.mode === 'table') this.fillTable(); else this.draw();
    // the detail has buttons that show the filter, and the count of what it keeps
    const old = this.root.querySelector('.tr-detail');
    if (old && this.sel) old.replaceWith(this.detail());
    this.cb.onRoute && this.cb.onRoute();
  }

  setMode(m) {
    if (this.mode === m) return;
    this.mode = m;
    this.render();
    this.cb.onRoute && this.cb.onRoute();
  }

  // ---- the timeline ----

  flame() {
    this.ruler = svg('svg', { class: 'tr-ruler', 'aria-hidden': 'true', height: 26 });
    this.canvas = svg('svg', { class: 'tr-canvas', role: 'img', 'aria-label': `Timeline of ${this.doc.command}: ${plural(this.model.spans.length, 'span')} in ${fmtDur(this.doc.duration_us)}. Arrow keys move between spans.` });
    this.plot = h('div', { class: 'tr-plot', tabindex: '0', 'data-fk': 'plot', 'aria-label': 'Timeline. Arrow keys move between spans, plus and minus zoom, zero shows the whole run, shift and the arrows move along.' },
      this.ruler, this.canvas);
    this.bindPlot();
    return h('div', { class: 'tr-chart' },
      this.plot,
      h('p', { class: 'tr-hint' }, 'Ctrl or ⌘ and scroll zoom. Drag moves along the run. Double click a span to fit it. Beside each attribute, Only and Hide filter by it.'));
  }

  layoutPlot() {
    if (!this.plot) return;
    this.draw();
    const sel = this.sel && this.model.byId.get(this.sel);
    if (sel) this.revealRow(sel);
  }

  revealRow(sp) {
    const y = sp.row * ROW, p = this.plot;
    if (y < p.scrollTop + 26 || y + ROW > p.scrollTop + p.clientHeight) p.scrollTop = Math.max(0, y - p.clientHeight / 3);
  }

  width() { return Math.max(320, (this.canvas && this.canvas.parentNode ? this.canvas.parentNode.clientWidth : 0) || this.root.clientWidth - 40 || 800); }

  draw() {
    if (!this.canvas || !this.model) return;
    cancelAnimationFrame(this.raf);
    this.raf = requestAnimationFrame(() => this.paint());
  }

  paint() {
    const m = this.model, W = this.width(), v0 = this.v0, v1 = this.v1;
    const k = W / (v1 - v0);
    const H = m.rows * ROW + 10;
    const x = (t) => (t - v0) * k;
    // the ruler and the grid
    const ts = ticks(v0, v1, Math.max(3, Math.floor(W / 110)));
    clear(this.ruler);
    clear(this.canvas);
    this.ruler.setAttribute('width', W);
    this.canvas.setAttribute('width', W);
    this.canvas.setAttribute('height', H);
    for (const t of ts) {
      const px = Math.round(x(t)) + 0.5;
      this.ruler.append(svg('path', { class: 'tr-tick', d: `M${px} 19V26` }));
      if (px < W - 44) this.ruler.append(svg('text', { class: 'tr-tl', x: px + 4, y: 15 }, fmtDur(t)));
      this.canvas.append(svg('path', { class: 'tr-grid', d: `M${px} 0V${H}` }));
    }
    this.ruler.append(svg('path', { class: 'tr-axis', d: `M0 25.5H${W}` }));
    const match = this.match, sel = this.sel;
    const dense = m.spans.length > 4000;
    const bars = svg('g', { class: 'tr-bars' });
    const labels = svg('g', { class: 'tr-labels' });
    for (const sp of m.spans) {
      const x0 = x(sp.start_us), w = sp.dur_us * k, x1 = x0 + w;
      if (x1 < -1 || x0 > W + 1) continue;
      const isSel = sp.id === sel;
      if (w < (dense ? 0.6 : 0.25) && !isSel) continue;
      const cx = Math.max(x0, -2), full = Math.max(Math.min(x1, W + 2) - cx, 1);
      // a bar stops a pixel short, so that two spans that follow each other never read as one
      const cw = full > 3 ? full - 1 : full;
      const dim = match && !match(sp);
      const cls = 'bar k-' + sp.kind + (sp.status === 'error' ? ' err' : '') + (dim ? ' dim' : '') + (match && !dim ? ' hit' : '') + (isSel ? ' sel' : '');
      const y = sp.row * ROW + 5;
      // the border is drawn inside the span, so that it ends where the span does
      bars.append(svg('rect', { class: cls, x: cx + 0.5, y: y + 0.5, width: Math.max(cw - 1, 0.5), height: BAR - 1, 'data-i': sp.idx }));
      if (full > 44) {
        let text = labelOf(sp);
        const room = Math.floor((cw - 10) / CHAR);
        const dur = cw > 170 ? fmtDur(sp.dur_us) : '';
        const avail = room - (dur ? dur.length + 2 : 0);
        if (avail >= 3) {
          if (text.length > avail) text = text.slice(0, Math.max(1, avail - 1)) + '…';
          labels.append(svg('text', { class: 'tr-bt k-' + sp.kind + (dim ? ' dim' : ''), x: cx + 5, y: y + 13 }, text));
          if (dur) labels.append(svg('text', { class: 'tr-bd k-' + sp.kind + (dim ? ' dim' : ''), x: cx + cw - 5, y: y + 13, 'text-anchor': 'end' }, dur));
        }
      }
    }
    this.canvas.append(bars, labels);
    if (sel) {
      const sp = m.byId.get(sel);
      if (sp) {
        // the selected span keeps its outline over its neighbours
        const x0 = Math.max(x(sp.start_us), -2), x1 = Math.min(x(sp.end), W + 2);
        // the dashed frame is the subtree: the rows that the span and what it called take, over its time
        if (sp.kids.length) this.canvas.append(svg('rect', { class: 'tr-subbox', x: x0 - 3, y: sp.row * ROW + 2, width: Math.max(x1 - x0, 1) + 6, height: sp.h * ROW }));
        this.canvas.append(svg('rect', { class: 'tr-selbox', x: x0 - 1, y: sp.row * ROW + 4, width: Math.max(x1 - x0, 1) + 2, height: BAR + 2 }));
      }
    }
  }

  bindPlot() {
    const p = this.plot;
    let drag = null, last = null;
    const spanAt = (ev) => {
      const t = ev.target;
      return t && t.dataset && t.dataset.i !== undefined ? this.model.spans[Number(t.dataset.i)] : null;
    };
    p.addEventListener('pointerdown', (ev) => {
      if (ev.button !== 0) return;
      drag = { x: ev.clientX, v0: this.v0, v1: this.v1, moved: false, id: ev.pointerId, span: spanAt(ev) };
    });
    p.addEventListener('pointermove', (ev) => {
      if (drag) {
        const dx = ev.clientX - drag.x;
        if (!drag.moved && Math.abs(dx) > 3) { drag.moved = true; p.setPointerCapture(drag.id); p.classList.add('grab'); this.hideTip(); }
        if (drag.moved) {
          const dt = -dx / (this.width() / (drag.v1 - drag.v0));
          this.setWindow(drag.v0 + dt, drag.v1 + dt, true);
          return;
        }
      }
      const sp = spanAt(ev);
      if (sp) this.showTip(sp, ev); else this.hideTip();
    });
    const end = (ev) => {
      if (!drag) return;
      const d = drag;
      drag = null;
      p.classList.remove('grab');
      if (d.moved) { this.cb.onRoute && this.cb.onRoute(); return; }
      if (ev.type !== 'pointerup') return;
      // Selecting redraws the bars under the pointer, and the browser sends no click, and so no double click, to a bar
      // that is gone: a second press on the same span within the time of a double click is read here.
      const now = ev.timeStamp;
      if (d.span && last && last.id === d.span.id && now - last.at < 450 && Math.abs(ev.clientX - last.x) < 6) { last = null; this.zoomTo(d.span); return; }
      last = d.span ? { id: d.span.id, at: now, x: ev.clientX } : null;
      this.select(d.span ? d.span.id : null);
    };
    p.addEventListener('pointerup', end);
    p.addEventListener('pointercancel', end);
    p.addEventListener('pointerleave', () => { if (!drag) this.hideTip(); });
        p.addEventListener('wheel', (ev) => {
      if (ev.ctrlKey || ev.metaKey) {
        ev.preventDefault();
        const r = this.canvas.getBoundingClientRect();
        const at = this.v0 + ((ev.clientX - r.left) / r.width) * (this.v1 - this.v0);
        this.zoomAt(at, Math.exp(ev.deltaY * 0.004), true);
      } else if (ev.shiftKey || Math.abs(ev.deltaX) > Math.abs(ev.deltaY)) {
        ev.preventDefault();
        const dt = ((ev.deltaX || ev.deltaY) / this.width()) * (this.v1 - this.v0);
        this.setWindow(this.v0 + dt, this.v1 + dt, true);
      }
    }, { passive: false });
    p.addEventListener('keydown', (ev) => this.key(ev));
  }

  key(ev) {
    if (ev.altKey || ev.ctrlKey || ev.metaKey) return;
    const m = this.model, sel = this.sel && m.byId.get(this.sel);
    const k = ev.key;
    const go = (sp) => { ev.preventDefault(); if (sp) this.select(sp.id); };
    const sibs = sel ? (sel.parent ? sel.parent.kids : m.roots) : [];
    if (k === '+' || k === '=') { ev.preventDefault(); this.zoomBy(0.5); return; }
    if (k === '-' || k === '_') { ev.preventDefault(); this.zoomBy(2); return; }
    if (k === '0') { ev.preventDefault(); this.fit(); return; }
    if (k === 'Escape' && sel) { ev.preventDefault(); ev.stopPropagation(); this.select(null); return; }
    if (k === 'Enter' && sel) { ev.preventDefault(); this.zoomTo(sel); return; }
    if (ev.shiftKey && (k === 'ArrowLeft' || k === 'ArrowRight')) {
      ev.preventDefault();
      const dt = (this.v1 - this.v0) * 0.15 * (k === 'ArrowLeft' ? -1 : 1);
      this.setWindow(this.v0 + dt, this.v1 + dt, true);
      this.cb.onRoute && this.cb.onRoute();
      return;
    }
    if (!sel) {
      if (k === 'ArrowDown' || k === 'ArrowRight' || k === 'ArrowLeft' || k === 'ArrowUp') go(m.roots[0]);
      return;
    }
    if (k === 'ArrowUp') go(sel.parent);
    else if (k === 'ArrowDown') go(sel.kids[0]);
    else if (k === 'ArrowLeft') go(sibs[sibs.indexOf(sel) - 1]);
    else if (k === 'ArrowRight') go(sibs[sibs.indexOf(sel) + 1]);
  }

  // ---- the window ----

  setWindow(a, b, soft = false) {
    const dur = this.model.dur;
    let w = clamp(b - a, Math.min(MIN_WINDOW, dur), dur);
    let s = a;
    if (s < 0) s = 0;
    if (s + w > dur) s = dur - w;
    this.v0 = s;
    this.v1 = s + w;
    this.draw();
    if (!soft) this.cb.onRoute && this.cb.onRoute();
    this.syncZoomButtons();
  }

  syncZoomButtons() {
    const fit = this.root.querySelector('[data-fk="fit"]');
    if (fit) fit.disabled = this.v0 <= 0.5 && this.v1 >= this.model.dur - 0.5;
  }

  fit() { this.setWindow(0, this.model.dur); }

  zoomAt(t, factor, soft) {
    const w = this.v1 - this.v0;
    const f = (t - this.v0) / w;
    const nw = clamp(w * factor, Math.min(MIN_WINDOW, this.model.dur), this.model.dur);
    this.setWindow(t - f * nw, t - f * nw + nw, soft);
  }

  zoomBy(factor) { this.zoomAt((this.v0 + this.v1) / 2, factor); }

  zoomTo(sp) {
    const pad = Math.max(sp.dur_us * 0.04, 1);
    this.setWindow(sp.start_us - pad, sp.end + pad);
  }

  // ---- the selection ----

  select(id) {
    this.sel = id && this.model.byId.has(id) ? id : null;
    if (this.mode === 'flame') this.paint();
    const old = this.root.querySelector('.tr-detail');
    if (old) old.replaceWith(this.detail());
    this.root.querySelectorAll('.tr-table tbody tr').forEach((tr) => tr.classList.toggle('sel', tr.dataset.id === this.sel));
    if (this.sel && this.mode === 'flame') this.revealRow(this.model.byId.get(this.sel));
    const now = this.root.querySelector('.tr-detail');
    // in the timeline the page stays where it is: moving it would take the bar from under the pointer, and a double click
    // would hit what is below
    if (this.sel && now && now.scrollIntoView && this.mode !== 'flame') now.scrollIntoView({ block: 'nearest' });
    this.cb.onRoute && this.cb.onRoute();
  }

  // ---- the tooltip ----

  showTip(sp, ev) {
    if (!this.tip) { this.tip = h('div', { class: 'tr-tip', role: 'tooltip' }); document.body.append(this.tip); }
    const t = this.tip;
    clear(t);
    t.append(h('b', null, labelOf(sp)), h('span', { class: 'tr-tk' }, KIND[sp.kind] || 'span'),
      h('span', null, `${fmtDur(sp.dur_us)}${sp.self !== sp.dur_us ? ` · ${fmtDur(sp.self)} in itself` : ''} · at ${fmtDur(sp.start_us)}`));
    if (sp.status === 'error') t.append(h('span', { class: 'tr-te' }, sp.message ? sp.message.slice(0, 160) : 'failed'));
    t.hidden = false;
    const w = t.offsetWidth, hh = t.offsetHeight;
    let x = ev.clientX + 14, y = ev.clientY + 16;
    if (x + w > innerWidth - 8) x = ev.clientX - w - 14;
    if (y + hh > innerHeight - 8) y = ev.clientY - hh - 12;
    t.style.transform = `translate(${Math.max(8, x)}px, ${Math.max(8, y)}px)`;
  }

  hideTip() { if (this.tip) this.tip.hidden = true; }

  // ---- the table ----

  table() {
    const wrap = h('div', { class: 'tr-tablewrap' });
    this.tableWrap = wrap;
    this.fillTable();
    return wrap;
  }

  rowsOf() {
    const { by, dir } = this.sort;
    const get = { name: (s) => labelOf(s).toLowerCase(), kind: (s) => s.kind, start: (s) => s.start_us, dur: (s) => s.dur_us, self: (s) => s.self, status: (s) => (s.status === 'error' ? 1 : 0) }[by];
    let rows = this.model.spans.filter((s) => (!this.match || this.match(s)));
    rows = rows.slice().sort((a, b) => {
      const x = get(a), y = get(b);
      return (x < y ? -1 : x > y ? 1 : a.start_us - b.start_us) * dir;
    });
    return rows;
  }

  fillTable() {
    const wrap = this.tableWrap;
    if (!wrap) return;
    clear(wrap);
    const rows = this.rowsOf();
    const total = this.doc.duration_us || 1;
    const th = (key, label, cls) => {
      const on = this.sort.by === key;
      return h('th', { scope: 'col', class: cls, 'aria-sort': on ? (this.sort.dir < 0 ? 'descending' : 'ascending') : 'none' },
        h('button', { type: 'button', 'data-fk': 'sort-' + key, on: { click: () => { this.sort = { by: key, dir: on ? -this.sort.dir : (key === 'name' || key === 'kind' || key === 'start' ? 1 : -1) }; this.shown = PAGE; this.fillTable(); } } },
          label, on ? h('span', { class: 'sortmark', 'aria-hidden': 'true' }, this.sort.dir < 0 ? '↓' : '↑') : null));
    };
    if (!rows.length) { wrap.append(h('p', { class: 'tr-empty' }, 'No span matches the filter.')); return; }
    const body = h('tbody');
    for (const sp of rows.slice(0, this.shown)) {
      const bad = sp.status === 'error';
      body.append(h('tr', { class: (sp.id === this.sel ? 'sel' : '') + (bad ? ' bad' : ''), 'data-id': sp.id },
        h('td', { class: 'c-name' }, h('button', { type: 'button', class: 'rowbtn', on: { click: () => this.select(sp.id) } }, h('span', { class: 'tr-sw k-' + sp.kind, 'aria-hidden': 'true' }), h('span', { class: 'nmt' }, labelOf(sp)), phaseOf(sp) ? h('span', { class: 'c-ph' }, phaseOf(sp)) : null)),
        h('td', { class: 'c-kind' }, KIND[sp.kind] || 'span'),
        h('td', { class: 'c-num' }, fmtDur(sp.start_us)),
        h('td', { class: 'c-num c-dur' }, h('span', { class: 'meter', style: `--w:${Math.max(0.5, Math.min(100, (sp.dur_us / total) * 100))}%`, 'aria-hidden': 'true' }), fmtDur(sp.dur_us)),
        h('td', { class: 'c-num' }, fmtDur(sp.self)),
        h('td', { class: 'c-st' }, bad ? h('span', { class: 'b bad' }, 'failed') : '')));
    }
    wrap.append(h('table', { class: 'tr-table' },
      h('thead', null, h('tr', null, th('name', 'Span', 'c-name'), th('kind', 'Kind', 'c-kind'), th('start', 'Started', 'c-num'), th('dur', 'Took', 'c-num'), th('self', 'In itself', 'c-num'), th('status', 'Status', 'c-st'))), body));
    if (rows.length > this.shown) {
      wrap.append(h('div', { class: 'tr-more' }, h('button', { class: 'linkbtn', type: 'button', 'data-fk': 'more', on: { click: () => { this.shown += PAGE; this.fillTable(); } } }, `Show ${Math.min(PAGE, rows.length - this.shown)} more`), h('span', { class: 'dim' }, `${fmt(this.shown)} of ${fmt(rows.length)}`)));
    }
  }

  // ---- the detail of a span ----

  detail() {
    const sp = this.sel && this.model.byId.get(this.sel);
    if (!sp) return h('div', { class: 'tr-detail empty' }, h('p', { class: 'tr-note' }, this.mode === 'flame' ? 'Click a span to see what it did, how long it took, and what was in it.' : 'Click a span to see what it did and what was in it.'));
    const total = this.doc.duration_us || 1;
    const chain = pathOf(sp);
    const crumbs = h('ol', { class: 'tr-crumbs', 'aria-label': 'Where the span is' });
    chain.forEach((c, i) => crumbs.append(h('li', null, i === chain.length - 1 ? h('span', { 'aria-current': 'true' }, labelOf(c)) : h('button', { type: 'button', class: 'linkbtn', on: { click: () => this.select(c.id) } }, labelOf(c)))));
    const attrs = Object.entries(sp.attrs || {}).sort(([a], [b]) => a.localeCompare(b));
    const slow = sp.kids.slice().sort((a, b) => b.dur_us - a.dur_us).slice(0, 5);
    const stat = (k, v, sub) => h('div', { class: 'st' }, h('dt', null, k), h('dd', null, h('b', null, v), sub ? h('span', null, sub) : null));
    const copy = sp.kind === 'resource' && sp.attrs && sp.attrs['pravo.resource.address'] ? String(sp.attrs['pravo.resource.address']) : '';
    return h('div', { class: 'tr-detail' + (sp.status === 'error' ? ' bad' : ''), 'aria-label': `The span ${labelOf(sp)}`, role: 'region' },
      h('div', { class: 'tr-dhead' },
        h('span', { class: 'tr-sw k-' + sp.kind, 'aria-hidden': 'true' }),
        h('h3', { class: 'tr-dname' }, labelOf(sp)),
        h('span', { class: 'b' }, KIND[sp.kind] || 'span'),
        sp.status === 'error' ? h('span', { class: 'b bad' }, icon('bang'), 'failed') : null,
        h('span', { class: 'grow' }),
        copy ? h('button', { class: 'cpbtn', type: 'button', 'data-copy': copy, 'aria-label': 'Copy the address', 'data-tip': 'Copy the address' }, icon('copy')) : null,
        this.mode === 'flame' ? h('button', { class: 'linkbtn', type: 'button', on: { click: () => this.zoomTo(sp) } }, 'Fit in the timeline') : h('button', { class: 'linkbtn', type: 'button', on: { click: () => { this.setMode('flame'); this.zoomTo(sp); } } }, 'Show in the timeline'),
        h('button', { class: 'iconbtn', type: 'button', 'aria-label': 'Close the detail', on: { click: () => this.select(null) } }, icon('x'))),
      crumbs,
      sp.status === 'error' && sp.message ? h('pre', { class: 'tr-err' }, sp.message) : null,
      h('div', { class: 'tr-where', role: 'img', 'aria-label': `From ${fmtDur(sp.start_us)} to ${fmtDur(sp.end)} of ${fmtDur(total)}` },
        h('span', { class: 'tr-wb k-' + sp.kind + (sp.status === 'error' ? ' err' : ''), style: `left:${Math.min(99.5, (sp.start_us / total) * 100)}%;width:max(3px,${Math.min(100, (sp.dur_us / total) * 100)}%)` })),
      h('dl', { class: 'tr-stats' },
        stat('Took', fmtDur(sp.dur_us), `${(Math.min(100, (sp.dur_us / total) * 100)).toFixed(sp.dur_us / total < 0.1 ? 1 : 0)}% of the run`),
        stat('In itself', fmtDur(sp.self), sp.kids.length ? `${Math.round((sp.self / Math.max(sp.dur_us, 1)) * 100)}% of its time` : 'no parts'),
        stat('Started', fmtDur(sp.start_us), 'into the run'),
        stat('Ended', fmtDur(sp.end), phaseOf(sp) ? 'in ' + phaseOf(sp) : '')),
      h('div', { class: 'tr-cols' },
        attrs.length ? h('section', { 'aria-label': 'Attributes' }, h('h4', null, 'Attributes'),
          this.match ? h('p', { class: 'tr-af' }, h('b', null, plural(this.model.spans.filter(this.match).length, 'span')), ` of ${fmt(this.model.spans.length)} match the filter `,
            h('button', { type: 'button', class: 'linkbtn', on: { click: () => this.setQuery('') } }, 'Clear the filter')) : null,
          h('ul', { class: 'tr-attrs' }, attrs.map(([k, v]) => {
            const val = Array.isArray(v) ? v.join(',') : String(v);
            const spec = `${k}=${val}`;
            const isOn = (neg) => parseQuery(this.q || '', LANG).terms.some((t) => t.col === 'attr' && t.neg === neg && t.values.includes(spec.toLowerCase()));
            const only = isOn(false), hide = isOn(true);
            // a button that is on takes the term out again; one that is off puts it in, and takes the other sign out
            const term = (neg, on) => () => this.setQuery(on ? removeValues(this.q || '', 'attr', [spec], LANG, neg) : addTerm(this.q || '', 'attr', spec, neg, LANG));
            const btn = (label, neg, on, tip) => h('button', { type: 'button', class: 'tr-f' + (on ? ' on' : ''), 'aria-pressed': on ? 'true' : 'false', 'aria-label': `${label}: ${k} ${val}`, title: on ? `${tip} (click to remove)` : tip, on: { click: term(neg, on) } }, on ? '\u2713 ' + label : label);
            return h('li', { class: only ? 'f-only' : hide ? 'f-hide' : '' }, h('span', { class: 'ak' }, k), h('span', { class: 'av' }, val),
              h('span', { class: 'af' }, btn('Only', false, only, 'Keep only the spans with this'), btn('Hide', true, hide, 'Leave out the spans with this')));
          }))) : null,
        sp.events && sp.events.length ? h('section', { 'aria-label': 'Events' }, h('h4', null, 'Events'),
          h('ol', { class: 'tr-events' }, sp.events.map((e) => h('li', null, h('span', { class: 'at' }, '+' + fmtDur(Math.max(0, e.at_us - sp.start_us))), h('span', { class: 'en' }, e.name),
            e.attrs && Object.keys(e.attrs).length ? h('span', { class: 'ea' }, Object.entries(e.attrs).map(([k, v]) => `${k}=${Array.isArray(v) ? v.join(',') : v}`).join(' ')) : null)))) : null,
        slow.length ? h('section', { 'aria-label': 'Slowest parts' }, h('h4', null, sp.kids.length > slow.length ? `Slowest of ${sp.kids.length} parts` : 'Parts'),
          h('ol', { class: 'tr-kids' }, slow.map((c) => h('li', null,
            h('button', { type: 'button', class: 'linkbtn', on: { click: () => this.select(c.id) } }, h('span', { class: 'tr-sw k-' + c.kind, 'aria-hidden': 'true' }), labelOf(c)),
            h('span', { class: 'kd' }, h('span', { class: 'kb', style: `--w:${Math.max(1, Math.min(100, (c.dur_us / Math.max(sp.dur_us, 1)) * 100))}%` }), fmtDur(c.dur_us)))))) : null));
  }

  // resize redraws the timeline for a new width.
  resize() { if (this.active && this.model && this.mode === 'flame' && this.canvas) this.draw(); }
}
