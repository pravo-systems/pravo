// Interface pieces shared by the panels: the tooltip, the tick of a number, the code window.

import { h, icon, copyButton, splitPath, motionOn } from './util.js';
import { tokenize, jsonTokens } from './cue.js';
import { attachFolds } from './fold.js';

// ---------------------------------------------------------------------------------------------------------
// the tooltip: one element, any element with data-tip gets it on hover (after a pause) or keyboard focus

export function initTips() {
  const tip = document.getElementById('tip');
  if (!tip) return;
  let cur = null, timer = 0;
  const hide = () => {
    clearTimeout(timer);
    if (cur) cur.removeAttribute('aria-describedby');
    cur = null;
    tip.hidden = true;
    tip.classList.remove('on');
  };
  const place = (el) => {
    const r = el.getBoundingClientRect();
    tip.style.left = '0px'; tip.style.top = '0px';
    const tw = tip.offsetWidth, th = tip.offsetHeight;
    const vw = document.documentElement.clientWidth, vh = document.documentElement.clientHeight;
    const pos = el.dataset.tipPos || 'bottom';
    let x, y;
    if (pos === 'left') { x = r.left - tw - 8; y = r.top + r.height / 2 - th / 2; if (x < 8) x = r.right + 8; } else if (pos === 'right') { x = r.right + 8; y = r.top + r.height / 2 - th / 2; if (x + tw > vw - 8) x = r.left - tw - 8; } else {
      x = r.left + r.width / 2 - tw / 2;
      y = pos === 'top' ? r.top - th - 8 : r.bottom + 8;
      if (y + th > vh - 8) y = r.top - th - 8;
      if (y < 8) y = r.bottom + 8;
    }
    x = Math.max(8, Math.min(vw - tw - 8, x));
    y = Math.max(8, Math.min(vh - th - 8, y));
    tip.style.left = Math.round(x) + 'px';
    tip.style.top = Math.round(y) + 'px';
  };
  const show = (el) => {
    const text = el.dataset.tip;
    if (!text) return;
    cur = el;
    tip.textContent = '';
    for (const [i, line] of text.split('\n').entries()) tip.append(h(i === 0 ? 'div' : 'div', { class: i === 0 ? 'tl1' : 'tl2' }, line));
    if (el.dataset.key) tip.append(h('kbd', null, el.dataset.key));
    tip.hidden = false;
    el.setAttribute('aria-describedby', 'tip');
    place(el);
    requestAnimationFrame(() => tip.classList.add('on'));
  };
  const target = (ev) => (ev.target instanceof Element ? ev.target.closest('[data-tip]') : null);
  document.addEventListener('pointerover', (ev) => {
    if (ev.pointerType === 'touch') return;
    const el = target(ev);
    if (!el || el === cur) return;
    hide();
    timer = setTimeout(() => show(el), cur ? 0 : 380);
  });
  document.addEventListener('pointerout', (ev) => { const el = target(ev); if (el && !el.contains(ev.relatedTarget)) hide(); });
  document.addEventListener('focusin', (ev) => { const el = target(ev); if (el && ev.target.matches(':focus-visible')) { hide(); show(el); } });
  document.addEventListener('focusout', hide);
  document.addEventListener('pointerdown', hide, true);
  document.addEventListener('scroll', hide, true);
  document.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') hide(); });
}

// ---------------------------------------------------------------------------------------------------------
// the code window: dark in both themes, a header with the path, a gutter, marks and a fade at the edge

const MARK_LABEL = { changed: 'changed', sensitive: 'sensitive' };

// codeWindow draws one block of lines.
//   b:     { file, label, role, kind, start_line, end_line, lines: [{no, text, mark, dim}], modified }
//   opts:  { json: bool, title: text, announce, newer: bool, relTime }
export function codeWindow(b, opts = {}) {
  const lines = dedent(b.lines || []);
  const toks = opts.json ? jsonTokens(lines.map((l) => l.text)) : tokenize(lines.map((l) => l.text));
  const focused = lines.some((l) => l.mark);
  const first = lines.length ? lines[0].no : b.start_line;
  const focusLines = lines.filter((l) => l.mark === 'focus');
  // the lines to name in "open in editor": the first line of the node itself, else the block's first
  const at = focusLines.length ? focusLines[0].no : b.start_line || first;
  const range = b.start_line && b.end_line && b.end_line !== b.start_line ? `${b.start_line}–${b.end_line}` : String(b.start_line || first);

  const { head, tail } = splitPath(b.file, 1);
  const bar = h('div', { class: 'cw-bar' },
    h('div', { class: 'cw-file', 'data-tip': b.file + (b.kind && b.kind !== 'file' ? '\n' + b.kind : '') },
      icon('file'),
      head ? h('span', { class: 'cw-dir' }, head) : null,
      h('b', { class: 'cw-name' }, tail)),
    copyButton(() => `${b.file}:${at}`, { label: 'Copy the path and line', tip: `Copy ${b.file}:${at}`, cls: 'cw-cp', done: 'Copied the path and line', announce: opts.announce }));
  const sub = h('div', { class: 'cw-sub' },
    b.role ? h('span', { class: 'cw-role r-' + b.role }, b.role === 'definition' ? 'definition' : 'instance') : null,
    b.label ? h('span', { class: 'cw-label', title: b.label }, b.label) : null,
    b.kind && b.kind !== 'file' ? h('em', { class: 'kindpill k-' + b.kind }, kindLabel(b.kind)) : null,
    h('span', { class: 'cw-rng' }, `lines ${range}`));

  const scroll = h('div', { class: 'cw-scroll', role: 'region', tabIndex: 0, 'aria-label': opts.title || `Source of ${b.file}, lines ${range}` });
  const body = h('div', { class: 'cw-body' + (focused ? ' has-marks' : '') });
  const parts = [];
  lines.forEach((l, i) => {
    const code = h('span', { class: 'cw-code' });
    for (const t of toks[i]) code.append(t.t ? h('span', { class: 'tk-' + t.t }, t.text) : t.text);
    if (!l.text) code.append('​');
    const mark = l.mark || '';
    const no = h('i', { class: 'cw-no' }, String(l.no));
    const row = h('div', { class: 'cw-ln' + (mark ? ' m-' + mark : '') + (l.dim ? ' dim' : '') },
      no, code,
      MARK_LABEL[mark] ? h('span', { class: 'cw-tag t-' + mark, 'aria-hidden': 'true' }, mark === 'sensitive' ? [icon('lock'), 'masked'] : 'changed by the plan') : null,
      MARK_LABEL[mark] ? h('span', { class: 'sr-only' }, mark === 'changed' ? ' (changed by the plan)' : ' (sensitive, masked)') : null);
    body.append(row);
    parts.push({ row, host: no, code });
  });
  attachFolds(parts, toks);
  scroll.append(body);
  const wrap = h('div', { class: 'cw-wrap' }, scroll);
  // the fade at the right edge tells that a long line goes on; the gutter gets a shadow once the code scrolls under it
  const sync = () => {
    const max = scroll.scrollWidth - scroll.clientWidth;
    wrap.dataset.more = max > 2 && scroll.scrollLeft < max - 2 ? '1' : '0';
    wrap.dataset.moved = scroll.scrollLeft > 2 ? '1' : '0';
  };
  scroll.addEventListener('scroll', sync, { passive: true });
  if (typeof ResizeObserver !== 'undefined') { const ro = new ResizeObserver(sync); ro.observe(scroll); }
  requestAnimationFrame(sync);

  return h('div', { class: 'cw' + (opts.cls ? ' ' + opts.cls : '') },
    bar, sub, wrap,
    opts.newer ? h('div', { class: 'cw-note' }, icon('bang'), opts.newer) : null);
}

// dedent takes the indentation that every line shares off the lines, so that a block that sits deep in a file
// is not shown with a wide empty margin in a narrow panel.
function dedent(lines) {
  let common = null;
  for (const l of lines) {
    if (!l.text.trim()) continue;
    const lead = /^[\t ]*/.exec(l.text)[0];
    if (common === null) common = lead;
    else { let i = 0; while (i < common.length && i < lead.length && common[i] === lead[i]) i++; common = common.slice(0, i); }
    if (!common) return lines;
  }
  if (!common) return lines;
  return lines.map((l) => Object.assign({}, l, { text: l.text.startsWith(common) ? l.text.slice(common.length) : l.text }));
}

export function kindLabel(k) {
  return { file: 'file', cache: 'cache', tag: 'tag', generated: 'generated', builtin: 'built in', policy: 'policy', definition: 'definition' }[k] || k;
}

// a plain block of text or JSON in the same dark window, without a header: for values in the State tab
export function textBlock(text, o = {}) {
  const lines = String(text).split('\n');
  const toks = o.json ? jsonTokens(lines) : null;
  const body = h('div', { class: 'tb-body' + (o.wrap ? ' wrap' : '') });
  lines.forEach((l, i) => {
    const row = h('div', { class: 'tb-ln' });
    if (toks) for (const t of toks[i]) row.append(t.t ? h('span', { class: 'tk-' + t.t }, t.text) : t.text);
    else row.append(l || '​');
    body.append(row);
  });
  return h('div', { class: 'tb' + (o.json ? ' json' : '') }, body);
}

export { motionOn };
