// The inspector when nothing is selected (the project) and when a module is: summaries, bars, navigable lists,
// and the places where a module is written.

import { h, icon, plural, relTime, stamp, actionOf, tick, copyButton, pathEl, baseName, fmt, nodeMark } from './util.js';
import { eachNode, isOut } from './model.js';
import { providerOf, pvClass } from './provider.js';
import { codeWindow } from './ui.js';
import * as api from './api.js';

const num = (n, cls = '') => {
  const el = h('span', { class: 'bn' + (cls ? ' ' + cls : '') }, '0');
  queueMicrotask(() => tick(el, n || 0));
  return el;
};
const sec = (title, ...kids) => h('section', { class: 'sec' }, title ? h('h3', { class: 'h' }, title) : null, kids);
const kv = (rows) => h('dl', { class: 'meta' }, rows.filter(Boolean).map(([k, v]) => [h('dt', null, k), h('dd', null, v)]));

// ---------------------------------------------------------------------------------------------------------
// the project

export function projectPanel(ins, model) {
  const g = model.graph, p = g.project;
  const body = [];
  const plan = g.plan;
  // a stored plan is named, counted and judged in the strip under the bar, the banner and the status bar: this is for a plan file
  if (plan && !plan.id) body.push(planSection(ins, plan, model));

  if ((p.providers || []).length) {
    // an instance is named by its key and drawn in its colour; a provider with one instance that is named like it needs no list
    const dot = (key) => h('span', { class: 'pvdot ' + pvClass(providerOf('', key)), 'aria-hidden': 'true' });
    body.push(sec(`Providers · ${p.providers.length}`, h('ul', { class: 'rows provs' }, p.providers.flatMap((v) => {
      const inst = v.instances || [];
      const lone = inst.length === 1 && inst[0].key === v.local;
      return [
        h('li', null, h('b', null, lone || !inst.length ? [dot(v.local), v.local] : v.local), h('span', { class: 'rs' }, v.source || ''), v.version ? h('em', { class: 'ver' }, v.version) : null),
        ...(lone ? [] : inst.map((i) => h('li', { class: 'inst' }, h('span', { class: 'ik' }, dot(i.key), i.key), h('span', { class: 'ic', 'data-tip': 'Resources that use this instance' }, plural(i.count, 'resource'))))),
      ];
    }))));
  }
  if ((p.policies || []).length) body.push(sec(`Policies · ${p.policies.length}`, h('ul', { class: 'rows' }, p.policies.map((v) => h('li', null, h('b', { class: 'mono-s' }, v))))));
  if ((g.warnings || []).length) body.push(sec(`Warnings · ${g.warnings.length}`, g.warnings.map((w) => h('p', { class: 'note warn' }, icon('bang'), h('span', null, w)))));
  body.push(h('section', { class: 'sec hintsec' }, h('p', { class: 'hint' }, 'Select a resource or a module to see its values, its source and its history. Press ', h('kbd', null, '?'), ' for the shortcuts.')));
  return body;
}

function pathValue(p, ins) {
  if (!p) return null;
  return h('span', { class: 'pv', 'data-tip': p }, pathEl(p), copyButton(p, { label: 'Copy the path', tip: 'Copy the path', announce: ins.cb.announce, done: 'Copied the path' }));
}

function planSection(ins, plan) {
  const c = plan.summary || {};
  const tile = (n, label, cls) => h('div', { class: 'tile ' + cls + (n ? '' : ' zero') }, num(n), h('span', { class: 'tlb' }, label));
  const notRead = plan.total - plan.planned;
  return h('section', { class: 'sec plan' },
    h('h3', { class: 'h' }, 'The plan'),
    h('div', { class: 'planned' },
      h('span', { class: 'pl-big' }, num(plan.planned), h('span', { class: 'of' }, ' of '), h('span', { class: 'bn' }, fmt(plan.total))),
      h('span', { class: 'pl-sub' }, `resources planned${notRead ? ` \u00b7 ${notRead} not read` : ''}`),
      plan.stale ? h('span', { class: 'b bad' }, 'stale') : null),
    h('div', { class: 'tiles' },
      tile(c.add, 'add', 'ok'), tile(c.change, 'change', 'chg'), tile(c.destroy, 'destroy', 'bad'), tile(c.read, 'read', 'lock')),
    h('div', { class: 'plan-meta' },
      kv([
        ['file', h('span', { class: 'mono-s' }, baseName(plan.file))],
        ['created', stamp(plan.created) + (plan.actor ? ' \u00b7 ' + plan.actor : '')],
        ['blast radius', plural(c.blast_radius || 0, 'resource')],
        plan.applied ? ['applied', `${plan.applied} of ${plan.planned}`] : null,
      ])),
    plan.stale ? h('p', { class: 'note bad' }, icon('bang'), h('span', null, 'This plan is stale: ' + (plan.reasons || []).join('; '))) : null,
    (plan.hints || []).map((x) => h('p', { class: 'note' }, icon('bang'), h('span', null, x))));
}

// ---------------------------------------------------------------------------------------------------------
// a module

const ACTION_ROWS = [['create', 'create'], ['update', 'update'], ['replace', 'replace'], ['delete', 'delete'], ['read', 'read']];

export function modulePanel(ins, mod, model, collapsed) {
  const a = mod.agg;
  const body = [];
  const closed = collapsed.has(mod.path);

  body.push(h('section', { class: 'sec tight' },
    h('div', { class: 'actions' },
      h('button', { class: 'btn', type: 'button', on: { click: () => ins.cb.onToggleModule(mod.path) } }, closed ? 'Open in the graph' : 'Collapse in the graph'),
      h('button', { class: 'btn', type: 'button', on: { click: () => ins.cb.onFit(mod.path) } }, 'Fit in the graph'))));

  // the numbers
  body.push(h('section', { class: 'sec' },
    h('div', { class: 'tiles' },
      h('div', { class: 'tile big' }, num(a.n), h('span', { class: 'tlb' }, a.n === 1 ? 'resource' : 'resources')),
      h('div', { class: 'tile' + (mod.kids.length ? '' : ' zero') }, num(mod.kids.length), h('span', { class: 'tlb' }, mod.kids.length === 1 ? 'module' : 'modules')),
      h('div', { class: 'tile' + (a.plan ? ' chg' : ' zero') }, num(a.plan), h('span', { class: 'tlb' }, 'in plan')),
      h('div', { class: 'tile' + (a.drifted + a.gone ? ' chg' : ' zero') }, num(a.drifted + a.gone), h('span', { class: 'tlb' }, 'drifted'))),
    flags(a)));

  // by resource type
  const types = new Map();
  eachNode(mod, (n) => { if (!isOut(n)) types.set(n.type, (types.get(n.type) || 0) + 1); });
  const tlist = [...types.entries()].sort((x, y) => y[1] - x[1] || x[0].localeCompare(y[0]));
  if (tlist.length) {
    const max = tlist[0][1];
    const shown = tlist.slice(0, 7);
    body.push(sec(`By resource type · ${tlist.length}`,
      h('ul', { class: 'bars' }, shown.map(([t, n]) => h('li', null, h('span', { class: 'bl', title: t }, t), h('span', { class: 'bt' }, h('i', { style: `--w:${Math.max(3, (n / max) * 100)}%` })), h('span', { class: 'bv' }, String(n))))),
      tlist.length > shown.length ? h('p', { class: 'hint' }, `and ${tlist.length - shown.length} more types, ${tlist.slice(shown.length).reduce((s, x) => s + x[1], 0)} resources`) : null));
  }

  // by plan action
  if (model.hasPlan) {
    const rows = ACTION_ROWS.filter(([k]) => a[k]);
    const notRead = a.n - a.plan;
    const max = Math.max(1, ...rows.map(([k]) => a[k]), notRead);
    body.push(sec('By plan action',
      rows.length || notRead ? h('ul', { class: 'bars act' }, [
        ...rows.map(([k, label]) => { const at = actionOf(k); return h('li', { class: 'k-' + k }, h('span', { class: 'bl' }, h('i', { class: 'sq ' + at.cls }), label), h('span', { class: 'bt' }, h('i', { style: `--w:${Math.max(4, (a[k] / max) * 100)}%` })), h('span', { class: 'bv' }, String(a[k]))); }),
        notRead ? h('li', { class: 'k-skip' }, h('span', { class: 'bl' }, h('i', { class: 'sq skip' }), 'not changed'), h('span', { class: 'bt' }, h('i', { style: `--w:${Math.max(4, (notRead / max) * 100)}%` })), h('span', { class: 'bv' }, String(notRead))) : null,
      ]) : h('p', { class: 'hint' }, 'The plan does not touch this module.')));
  }

  // children
  if (mod.kids.length) {
    const maxKid = Math.max(1, ...mod.kids.map((k) => k.agg.n));
    body.push(sec(`Sub-modules · ${mod.kids.length}`, h('ul', { class: 'nav' }, mod.kids.map((k) =>
      h('li', null, h('a', { class: 'navrow mod', href: '#', data: { gomod: k.path }, title: k.path },
        icon('chev', 'tiny'), h('span', { class: 'nn' }, k.name),
        k.agg.plan ? h('span', { class: 'sq a-update tiny', title: `${k.agg.plan} in plan` }) : null,
        k.agg.drifted + k.agg.gone ? h('span', { class: 'i i-drift warn', title: 'drifted' }) : null,
        h('span', { class: 'nb', 'aria-hidden': 'true' }, h('i', { style: `--w:${Math.max(6, (k.agg.n / maxKid) * 100)}%` })),
        h('span', { class: 'nc' }, String(k.agg.n))))))));
  }
  if (mod.nodes.some((n) => !isOut(n))) body.push(resourcesSection(ins, mod, model, false));
  if (mod.nodes.some(isOut)) body.push(resourcesSection(ins, mod, model, true));

  // where it is written
  const where = h('div', { class: 'where' });
  body.push(h('section', { class: 'sec' }, h('h3', { class: 'h' }, 'Where it is written'), where));
  fillWhere(ins, where, mod, model);
  return body;
}

function flags(a) {
  const f = [];
  if (a.gone) f.push(h('span', { class: 'b bad' }, icon('gone'), `${a.gone} gone`));
  if (a.drifted) f.push(h('span', { class: 'b chg' }, icon('drift'), `${a.drifted} drifted`));
  if (a.locked) f.push(h('span', { class: 'b lock' }, icon('hold'), `${a.locked} locked`));
  if (a.sensitive) f.push(h('span', { class: 'b lock' }, icon('lock'), `${a.sensitive} with secrets`));
  if (a.orphan) f.push(h('span', { class: 'b bad' }, `${a.orphan} orphan${a.orphan === 1 ? '' : 's'}`));
  if (a.spec) f.push(h('span', { class: 'b' }, `${a.spec} declared only`));
  return f.length ? h('div', { class: 'badges' }, f) : null;
}

function resourcesSection(ins, mod, model, outputs) {
  const LIMIT = 12;
  const nodes = mod.nodes.filter((n) => isOut(n) === outputs);
  const host = h('ul', { class: 'nav' });
  const draw = (q, all) => {
    host.textContent = '';
    const list = nodes.filter((n) => !q || n.addr.toLowerCase().includes(q) || n.type.toLowerCase().includes(q));
    const shown = all || q ? list : list.slice(0, LIMIT);
    for (const n of shown) {
      host.append(h('li', null, h('a', { class: 'navrow', href: '#', data: { goto: n.addr }, title: n.addr },
        nodeMark(n, model.hasPlan), h('span', { class: 'nn' }, n.name), outputs ? null : h('span', { class: 'nt' }, n.type),
        n.drifted || n.gone ? h('span', { class: 'i ' + (n.gone ? 'i-gone' : 'i-drift') + ' warn' }) : null,
        n.sensitive > 0 ? h('span', { class: 'i i-lock key' }) : null)));
    }
    if (!list.length) host.append(h('li', { class: 'hint pad0' }, 'Nothing matches.'));
    if (!all && !q && list.length > LIMIT) host.append(h('li', null, h('button', { class: 'more-btn', type: 'button', on: { click: () => draw('', true) } }, `Show all ${list.length}`)));
  };
  draw('', false);
  const filter = nodes.length > LIMIT ? h('input', { class: 'filter', type: 'search', placeholder: 'Filter these resources', 'aria-label': 'Filter the resources of the module', on: { input: (ev) => draw(ev.target.value.trim().toLowerCase(), false) } }) : null;
  return sec(`${outputs ? 'Outputs' : 'Resources'} · ${nodes.length}`, filter, host);
}

// fillWhere shows the module's own lines: where the instance is written and the definition it comes from. The
// server serves lines by node address, so it asks for a resource of the module and takes the blocks that
// belong to the module.
async function fillWhere(ins, host, mod, model) {
  const refs = [];
  const ref = (label, r) => r && h('div', { class: 'refline' }, h('span', { class: 'rl' }, label), h('code', null, `${r.file}:${r.line}${r.end_line && r.end_line !== r.line ? '–' + r.end_line : ''}`),
    copyButton(`${r.file}:${r.line}`, { label: `Copy ${r.file}:${r.line}`, tip: `Copy ${r.file}:${r.line}`, announce: ins.cb.announce, done: 'Copied the path and line' }));
  refs.push(ref('declared', mod.declared), ref('definition', mod.definition));
  const fallback = () => { host.textContent = ''; const r = refs.filter(Boolean); host.append(...(r.length ? r : [h('p', { class: 'hint' }, 'The specification does not say where this module is written.')])); };
  let rep = null;
  eachNode(mod, (n) => { if (!rep && n.presence !== 'state' && !isOut(n)) rep = n; });
  if (!rep || !(mod.declared || mod.definition)) { fallback(); return; }
  host.append(h('div', { class: 'cw-skel', 'aria-hidden': 'true' }, h('i'), h('i'), h('i'), h('i')));
  let src;
  try { src = await ins.fetch(`source:${rep.addr}:${model.graph.revision}`, () => api.source(rep.addr)); } catch { fallback(); return; }
  if (ins.mode !== 'module' || ins.modAddr !== mod.path) return;
  host.textContent = '';
  const blocks = src.blocks || [];
  const inst = blocks.find((b) => b.role === 'instance' && (b.label === mod.path || (b.label || '').startsWith(mod.path + ' of ')));
  const defn = mod.definition ? blocks.find((b) => b.role === 'definition' && b.file === mod.definition.file) : null;
  const opts = { announce: ins.cb.announce };
  if (inst) { host.append(h('h4', { class: 'src-h' }, 'Declared here'), codeWindow(inst, Object.assign({ title: `Where ${mod.path} is declared` }, opts))); } else if (mod.declared) host.append(ref('declared', mod.declared));
  if (defn) {
    host.append(...[h('h4', { class: 'src-h' }, 'Its definition'), ref('definition', mod.definition),
      h('p', { class: 'hint' }, `One resource that the definition writes: ${defn.label || rep.name}.`), codeWindow(defn, Object.assign({ title: 'Lines of the definition' }, opts))].filter(Boolean));
  } else if (mod.definition) host.append(ref('definition', mod.definition));
  if (!host.childNodes.length) fallback();
}
