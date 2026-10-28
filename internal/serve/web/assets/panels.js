// The inspector when nothing is selected (the project) and when a namespace is: summaries, bars, navigable lists,
// and the places where a namespace is written.

import { h, icon, plural, actionOf, tick, copyButton, nodeMark } from './util.js';
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

// ---------------------------------------------------------------------------------------------------------
// the project

export function projectPanel(ins, model) {
  const g = model.graph, p = g.project;
  const body = [];

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
  if ((g.warnings || []).length) body.push(sec(`Warnings · ${g.warnings.length}`, g.warnings.map((w) => h('p', { class: 'note warn' }, icon('bang'), h('span', null, w)))));
  body.push(h('section', { class: 'sec hintsec' }, h('p', { class: 'hint' }, 'Select a resource or a group to see its values, its source and its history. Press ', h('kbd', null, '?'), ' for the shortcuts.')));
  return body;
}

// ---------------------------------------------------------------------------------------------------------
// a namespace

const ACTION_ROWS = [['create', 'create'], ['update', 'update'], ['replace', 'replace'], ['delete', 'delete'], ['read', 'read']];

export function namespacePanel(ins, ns, model, collapsed) {
  const a = ns.agg;
  const body = [];
  const closed = collapsed.has(ns.path);

  body.push(h('section', { class: 'sec tight' },
    h('div', { class: 'actions' },
      h('button', { class: 'btn', type: 'button', on: { click: () => ins.cb.onToggleNamespace(ns.path) } }, closed ? 'Open in the graph' : 'Collapse in the graph'),
      h('button', { class: 'btn', type: 'button', on: { click: () => ins.cb.onFit(ns.path) } }, 'Fit in the graph'))));

  // the numbers
  body.push(h('section', { class: 'sec' },
    h('div', { class: 'tiles', style: model.hasPlan ? null : 'grid-template-columns: repeat(3, minmax(0, 1fr))' },
      h('div', { class: 'tile big' }, num(a.n), h('span', { class: 'tlb' }, a.n === 1 ? 'resource' : 'resources')),
      h('div', { class: 'tile' + (ns.kids.length ? '' : ' zero') }, num(ns.kids.length), h('span', { class: 'tlb' }, ns.kids.length === 1 ? 'group' : 'groups')),
      // with no plan laid over the graph there is nothing that it could change
      model.hasPlan ? h('div', { class: 'tile' + (a.plan ? ' chg' : ' zero') }, num(a.plan), h('span', { class: 'tlb' }, 'in plan')) : null,
      h('div', { class: 'tile' + (a.drifted + a.gone ? ' chg' : ' zero') }, num(a.drifted + a.gone), h('span', { class: 'tlb' }, 'drifted'))),
    flags(a)));

  // by resource type
  const types = new Map();
  eachNode(ns, (n) => { if (!isOut(n)) types.set(n.type, (types.get(n.type) || 0) + 1); });
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
      ]) : h('p', { class: 'hint' }, 'The plan does not touch this group.')));
  }

  // children
  if (ns.kids.length) {
    const maxKid = Math.max(1, ...ns.kids.map((k) => k.agg.n));
    body.push(sec(`Subgroups · ${ns.kids.length}`, h('ul', { class: 'nav' }, ns.kids.map((k) =>
      h('li', null, h('a', { class: 'navrow ns', href: '#', data: { gons: k.path }, title: k.path },
        icon('chev', 'tiny'), h('span', { class: 'nn' }, k.name),
        k.agg.plan ? h('span', { class: 'sq a-update tiny', title: `${k.agg.plan} in plan` }) : null,
        k.agg.drifted + k.agg.gone ? h('span', { class: 'i i-drift warn', title: 'drifted' }) : null,
        h('span', { class: 'nb', 'aria-hidden': 'true' }, h('i', { style: `--w:${Math.max(6, (k.agg.n / maxKid) * 100)}%` })),
        h('span', { class: 'nc' }, String(k.agg.n))))))));
  }
  if (ns.nodes.some((n) => !isOut(n))) body.push(resourcesSection(ins, ns, model, false));
  if (ns.nodes.some(isOut)) body.push(resourcesSection(ins, ns, model, true));

  // where it is written
  const where = h('div', { class: 'where' });
  body.push(h('section', { class: 'sec' }, h('h3', { class: 'h' }, 'Where it is written'), where));
  fillWhere(ins, where, ns, model);
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

function resourcesSection(ins, ns, model, outputs) {
  const LIMIT = 12;
  const nodes = ns.nodes.filter((n) => isOut(n) === outputs);
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
  const filter = nodes.length > LIMIT ? h('input', { class: 'filter', type: 'search', placeholder: 'Filter these resources', 'aria-label': 'Filter the resources of the group', on: { input: (ev) => draw(ev.target.value.trim().toLowerCase(), false) } }) : null;
  return sec(`${outputs ? 'Outputs' : 'Resources'} · ${nodes.length}`, filter, host);
}

// fillWhere shows the namespace's own lines: where the instance is written and the definition it comes from. The
// server serves lines by node address, so it asks for a resource of the namespace and takes the blocks that
// belong to the namespace.
async function fillWhere(ins, host, ns, model) {
  const refs = [];
  const ref = (label, r) => r && h('div', { class: 'refline' }, h('span', { class: 'rl' }, label), h('code', null, `${r.file}:${r.line}${r.end_line && r.end_line !== r.line ? '–' + r.end_line : ''}`),
    copyButton(`${r.file}:${r.line}`, { label: `Copy ${r.file}:${r.line}`, tip: `Copy ${r.file}:${r.line}`, announce: ins.cb.announce, done: 'Copied the path and line' }));
  refs.push(ref('declared', ns.declared), ref('definition', ns.definition));
  const fallback = () => { host.textContent = ''; const r = refs.filter(Boolean); host.append(...(r.length ? r : [h('p', { class: 'hint' }, 'The specification does not say where this group is written.')])); };
  let rep = null;
  eachNode(ns, (n) => { if (!rep && n.presence !== 'state' && !isOut(n)) rep = n; });
  if (!rep || !(ns.declared || ns.definition)) { fallback(); return; }
  host.append(h('div', { class: 'cw-skel', 'aria-hidden': 'true' }, h('i'), h('i'), h('i'), h('i')));
  let src;
  try { src = await ins.fetch(`source:${rep.addr}:${model.graph.revision}`, () => api.source(rep.addr)); } catch { fallback(); return; }
  if (ins.mode !== 'namespace' || ins.nsAddr !== ns.path) return;
  host.textContent = '';
  const blocks = src.blocks || [];
  const inst = blocks.find((b) => b.role === 'instance' && (b.label === ns.path || (b.label || '').startsWith(ns.path + ' of ')));
  const defn = ns.definition ? blocks.find((b) => b.role === 'definition' && b.file === ns.definition.file) : null;
  const opts = { announce: ins.cb.announce };
  if (inst) { host.append(h('h4', { class: 'src-h' }, 'Declared here'), codeWindow(inst, Object.assign({ title: `Where ${ns.path} is declared` }, opts))); } else if (ns.declared) host.append(ref('declared', ns.declared));
  if (defn) {
    host.append(...[h('h4', { class: 'src-h' }, 'Its definition'), ref('definition', ns.definition),
      h('p', { class: 'hint' }, `One resource that the definition writes: ${defn.label || rep.name}.`), codeWindow(defn, Object.assign({ title: 'Lines of the definition' }, opts))].filter(Boolean));
  } else if (ns.definition) host.append(ref('definition', ns.definition));
  if (!host.childNodes.length) fallback();
}
