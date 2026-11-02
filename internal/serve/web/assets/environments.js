// The Environments view: the ladder of a specification that declares $environments, as a flow from left to right. Each
// environment is a box: what its state runs (the specification by its id, the git revision, since when and by whom) and
// how the release of this directory stands there. The page reads one environment at a time, the one marked here; a box
// opens another, which the server loads as a command run with --env NAME does.
//
// The page reads /api/environments and shows it. Nothing here runs a command or writes anything: a promotion is signed
// on the command line, and the command to type next is shown as text to copy.

import { h, clear, icon, relTime, stamp } from './util.js';

export const FOOTER = 'This page reads the state of each environment without a key. It runs nothing and writes nothing: a promotion is signed with pravo promote.';

// STATUS is how a box words the release of this directory, and the tone of its mark: ok when it runs it, chg when someone
// has a step to take.
const STATUS = {
  runs: ['ok', 'runs this directory'],
  promoted: ['chg', 'promoted, waiting for an apply'],
  waived: ['chg', 'waived, waiting for an apply'],
  ready: ['chg', 'ready to promote'],
};

const shortID = (fp) => String(fp || '').slice(0, 12);
const shortRev = (rev) => (rev && rev.length > 7 ? rev.slice(0, 7) + (rev.endsWith('-dirty') ? '-dirty' : '') : rev || '');

// menuLine is the one line that the menu of the bar shows under the name of an environment: what is wrong with it, how
// the release of this directory stands there, or what it runs. tone is the mark of the line, as on the page.
export function menuLine(e) {
  if (!e) return ['', ''];
  if (e.problem) return ['bad', e.problem];
  const [tone, words] = STATUS[e.release] || ['', ''];
  if (words) return [tone, words];
  if (e.runs) return ['', `runs ${shortID(e.runs.fingerprint)}${e.runs.at ? ', ' + relTime(e.runs.at) : ''}`];
  return ['', 'nothing applied yet'];
}

export class EnvironmentsView {
  // cb.onSwitch(name) makes the server read another environment.
  constructor(root, cb = {}) {
    this.root = root;
    this.cb = cb;
    this.busy = '';
  }

  // switching is the environment that the page is opening, if any: its button says so, and the others wait.
  render({ environments, switching = '' }) {
    this.busy = switching;
    const top = this.root.scrollTop;
    clear(this.root);
    const d = environments;
    if (!d || d.error || !(d.environments || []).length) {
      const why = !d ? 'The environments could not be read. The rest of the page is unaffected.'
        : d.error ? d.error
          : 'This specification declares no environments. A row of $environments for each, with $after naming the one it follows, makes a ladder that a release climbs.';
      this.root.append(h('div', { class: 'ev' }, this.head(d), h('p', { class: 'ev-empty' }, why), h('p', { class: 'ev-foot' }, FOOTER)));
      return;
    }
    this.root.append(h('div', { class: 'ev' },
      this.head(d),
      this.ladder(d),
      this.next(d),
      h('p', { class: 'ev-foot' }, FOOTER)));
    this.root.scrollTop = top;
  }

  head(d) {
    const waiting = d ? d.waiting : 0;
    return h('header', { class: 'ev-head' + (waiting ? ' chg' : '') },
      h('h2', { class: 'ev-title', id: 'ev-h' }, 'Environments', d && d.ladder ? h('span', { class: 'ev-ladder' }, d.ladder) : null),
      h('p', { class: 'ev-lede' }, 'One specification, applied to each environment in turn. An environment that follows another takes only a release that was promoted into it: signed by a writer of its state, carrying the signed note that the one before runs it.'));
  }

  // ladder is the environments in columns by their step, left to right, with the line of the flow between the columns.
  ladder(d) {
    const cols = [];
    for (const e of d.environments) (cols[e.depth] = cols[e.depth] || []).push(e);
    const parts = [];
    cols.forEach((col, i) => {
      if (i > 0) parts.push(h('div', { class: 'ev-flow', 'aria-hidden': 'true' }, h('span', { class: 'ev-arrow' })));
      parts.push(h('div', { class: 'ev-col' }, (col || []).map((e) => this.box(e, d))));
    });
    return h('section', { class: 'ev-sec', 'aria-labelledby': 'ev-h-ladder' },
      h('h3', { id: 'ev-h-ladder', class: 'h' }, 'The ladder'),
      h('div', { class: 'ev-lad', role: 'list' }, parts));
  }

  box(e, d) {
    const reading = e.name === d.selected;
    const [tone, words] = STATUS[e.release] || ['', ''];
    const facts = [];
    const fact = (k, v, title) => facts.push(h('div', { class: 'ev-f' }, h('dt', null, k), h('dd', { title: title || null }, v)));
    if (e.runs) {
      fact('runs', h('code', null, shortID(e.runs.fingerprint)), e.runs.fingerprint);
      if (e.runs.git_revision) fact('git', h('code', null, shortRev(e.runs.git_revision)), e.runs.git_revision);
      fact('since', relTime(e.runs.at), stamp(e.runs.at));
      if (e.runs.by) fact('by', e.runs.by);
    } else if (!e.problem) {
      fact('runs', h('span', { class: 'dim' }, 'nothing applied yet'));
    }
    const lines = [];
    if (e.problem) lines.push(h('p', { class: 'ev-st bad' }, icon('bang'), h('span', null, e.problem)));
    if (words) {
      const who = e.promoted ? ` by ${e.promoted.by} ${relTime(e.promoted.at)}` : '';
      lines.push(h('p', { class: 'ev-st ' + tone }, h('span', { class: 'ev-dot', 'aria-hidden': 'true' }), h('span', null, words + (e.release === 'promoted' || e.release === 'waived' ? who : ''))));
    }
    if (e.promoted && e.promoted.kind === 'waived' && e.promoted.note) lines.push(h('p', { class: 'ev-note' }, 'Reason: ' + e.promoted.note));
    if (e.other_tags) lines.push(h('p', { class: 'ev-note' }, 'Runs this specification with other tags.'));
    if (!e.sealed && e.after.length && e.state) lines.push(h('p', { class: 'ev-note' }, 'Not sealed, so it takes any release.'));
    const name = h('div', { class: 'ev-name' },
      h('code', null, e.name),
      reading ? h('span', { class: 'ev-tag on' }, 'reading') : null,
      e.default ? h('span', { class: 'ev-tag' }, 'default') : null);
    const body = [name,
      e.after.length ? h('p', { class: 'ev-after' }, 'after ', e.after.map((a, i) => [i ? ', ' : '', h('code', null, a)])) : h('p', { class: 'ev-after' }, 'first: takes any release'),
      facts.length ? h('dl', { class: 'ev-facts' }, facts) : null,
      lines,
      e.command ? this.command(e.command) : null];
    const open = reading ? null : h('button', {
      type: 'button', class: 'btn ev-open', 'data-fk': 'open:' + e.name, disabled: this.busy ? true : null,
      on: { click: () => this.open(e.name) },
    }, this.busy === e.name ? 'Opening…' : 'Read this environment');
    if (open && this.busy === e.name) open.setAttribute('aria-busy', 'true');
    return h('article', { class: 'ev-box' + (reading ? ' reading' : '') + (tone ? ' ' + tone : '') + (e.problem ? ' bad' : ''), role: 'listitem', 'aria-label': e.name + (reading ? ', the environment this page reads' : '') }, body, open);
  }

  open(name) {
    if (this.cb.onSwitch && !this.busy) this.cb.onSwitch(name);
  }

  // next is the command that moves the release of this directory one step along.
  next(d) {
    const tags = (d.release.tags || []).length ? ` · tags ${d.release.tags.join(', ')}` : '';
    const done = !/^pravo /.test(d.next || '');
    return h('section', { class: 'ev-sec', 'aria-labelledby': 'ev-h-next' },
      h('h3', { id: 'ev-h-next', class: 'h' }, 'Next'),
      h('p', { class: 'ev-rel' }, 'This directory is ', h('code', { title: d.release.fingerprint }, shortID(d.release.fingerprint)), tags, '.'),
      done ? h('p', { class: 'ev-rel' }, d.next.charAt(0).toUpperCase() + d.next.slice(1) + '.') : this.command(d.next));
  }

  command(text) {
    return h('div', { class: 'cmdline ev-cmd' }, h('span', { class: 'ps', 'aria-hidden': 'true' }, '$'), h('code', null, text),
      h('button', { class: 'cpbtn', type: 'button', 'data-copy': text, 'aria-label': 'Copy the command: ' + text, 'data-tip': 'Copy the command' }, icon('copy')));
  }
}
