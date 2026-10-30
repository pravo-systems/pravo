// The Templates view: the catalog of the templates of the specification, the instances made from each, and the wizard
// that writes the CUE of a new instance.
//
// A template is a definition that embeds pravo.#Template. Its inputs are the fields of the definition that hold no
// resource. An instance is a struct built from it, an ordinary group of the graph that carries the marker of the template.
// The wizard asks for the inputs, has the server load the specification with the new instance laid over it in memory, and
// shows what CUE and the policies say. It shows the CUE; a person pastes it. Nothing here writes a file, runs a command or
// changes the state, and every request of the page is a GET.

import { h, clear, icon, debounce, fmt, plural, relTime } from './util.js';
import { codeWindow } from './ui.js';
import * as api from './api.js';

export const FOOTER = 'This page shows the templates and checks new instances. It writes nothing: the code is yours to paste.';

const MARKER_EXAMPLE = `#Service: pravo.#Template & {
	$template: {name: "service", title: "Web service"}
	name: string
	port: *8080 | int
	...
}`;

// cueValue turns what a person typed for an input into CUE.
function cueValue(input, f) {
  if (!f) return '';
  if (input.secret) return '';
  if (f.items) {
    if (!f.items.length) return '';
    return '[' + f.items.map((x) => (input.elem === 'string' ? JSON.stringify(x) : x)).join(', ') + ']';
  }
  if (f.text === undefined || f.text === '') return '';
  if (f.ref) return f.text;
  switch (input.kind) {
    case 'string': return JSON.stringify(f.text);
    case 'enum': return f.text;
    case 'bool': return f.text;
    default: return f.text;
  }
}

// refValue says in words what the state holds for an attribute that a reference reads.
function refValue(x) {
  switch (x.value_state) {
    case 'known': return x.value;
    case 'unapplied': return 'known after apply';
    case 'withheld': return 'withheld';
    case 'null': return 'null';
    case 'structured': return 'a list or an object';
    default: return '';
  }
}

// secretName is the name a secret has unless the person gives another: the template, the instance and the input.
function secretName(source, tpl, label, input) {
  const l = label || 'name';
  if (source === 'env') return (l + '_' + input).toUpperCase().replace(/[^A-Z0-9_]/g, '_');
  return `${tpl}/${l}/${input}`;
}

export class TemplatesView {
  // cb.onNamespace(path) shows a group in the graph; cb.onRoute() says that the address changed.
  constructor(root, cb = {}) {
    this.root = root;
    this.cb = cb;
    this.doc = null;
    this.sel = '';      // the name of the open template
    this.want = '';
    this.wizard = false;
    this.form = null;   // the state of the wizard: { label, at, custom, fields: {input: {text, ref}} }
    this.seq = 0;
    this.ctl = null;
    this.out = null;    // the elements the wizard redraws
    this.refsFor = '';  // the input whose graph picker is open
    this.choices = {};  // the values that the inputs with choices offer, by input
    this.vault = null;  // whether the state keeps secrets, and the names it holds
    this.check = debounce(() => this.runCheck(), 260);
  }

  route() { return { tpl: this.sel, tn: this.wizard ? '1' : '' }; }

  setRoute(f) {
    this.want = f.tpl || '';
    if (this.want) this.sel = this.want;
    this.wizard = f.tn === '1' && !!this.want;
  }

  get tpl() { return this.doc && this.doc.templates.find((t) => t.name === this.sel) || null; }

  render({ templates }) {
    const top = this.root.scrollTop;
    this.doc = templates || this.doc;
    clear(this.root);
    this.out = null;
    if (!templates) {
      this.root.append(h('div', { class: 'tp' }, h('h2', { class: 'tp-title' }, 'Templates'), h('p', { class: 'tp-empty' }, 'The templates could not be read. The rest of the page is unaffected.')));
      return;
    }
    const list = templates.templates;
    if (!this.tpl && list.length) this.sel = list[0].name;
    if (this.wizard && !this.tpl) this.wizard = false;
    this.root.append(h('div', { class: 'tp' },
      this.head(templates),
      !templates.spec_ok ? this.broken() : !list.length ? this.none() : this.wizard ? this.wizardView() : this.catalog(list),
      h('p', { class: 'tp-foot' }, FOOTER)));
    this.root.scrollTop = top;
  }

  head(d) {
    const n = d.templates.length;
    return h('header', { class: 'tp-head' + (n ? '' : ' none') },
      h('h2', { class: 'tp-title', id: 'tp-h' }, 'Templates', n ? h('span', { class: 'tp-n', 'aria-label': plural(n, 'template') }, fmt(n)) : null),
      h('p', { class: 'tp-lede' }, 'A template is a definition that makes a group of resources from a few inputs. Choose one, fill in its form, and the page gives you the code of a new instance to paste into the specification. An instance stays an ordinary group of the graph, and stays tied to its template.'));
  }

  broken() {
    return h('p', { class: 'tp-warn' }, icon('bang'), h('span', null, 'The specification does not load, so no template is known. Fix the error on the Inventory page first.'));
  }

  none() {
    return h('section', { class: 'tp-none' },
      h('p', null, 'The specification has no template. A definition becomes one when it embeds pravo.#Template and names itself:'),
      h('pre', { class: 'tp-pre' }, h('code', null, MARKER_EXAMPLE)),
      h('p', null, 'Its fields become the inputs of the form. The instances of the definition are listed under it.'));
  }

  // ---- the catalog: the templates on the left, the open one on the right ----

  catalog(list) {
    return h('div', { class: 'tp-cols' },
      h('nav', { class: 'tp-list', 'aria-label': 'Templates' }, list.map((t) => this.card(t))),
      this.detail(this.tpl));
  }

  card(t) {
    const on = t.name === this.sel;
    return h('button', { type: 'button', class: 'tp-card' + (on ? ' on' : ''), 'aria-pressed': on ? 'true' : 'false', 'data-fk': 'card:' + t.name,
      on: { click: () => { this.sel = t.name; this.wizard = false; this.render({ templates: this.doc }); this.cb.onRoute && this.cb.onRoute(); } } },
      h('span', { class: 'tp-ct' }, t.title),
      h('code', { class: 'tp-cd' }, t.definition),
      t.description ? h('span', { class: 'tp-cx' }, t.description) : null,
      h('span', { class: 'tp-cs' },
        h('b', null, fmt(t.instances.length)), ' ', t.instances.length === 1 ? 'instance' : 'instances',
        h('i', { 'aria-hidden': 'true' }, '·'), h('b', null, fmt(t.inputs.length)), ' ', t.inputs.length === 1 ? 'input' : 'inputs'));
  }

  detail(t) {
    return h('section', { class: 'tp-detail', 'aria-labelledby': 'tp-dt' },
      h('header', { class: 'tp-dh' },
        h('div', { class: 'tp-dhl' },
          h('h3', { id: 'tp-dt', class: 'tp-dtitle' }, t.title),
          h('p', { class: 'tp-dsub' }, h('code', null, t.definition), t.source ? h('span', { class: 'tp-where' }, ' written at ', h('code', null, `${t.source.file}:${t.source.line}`)) : null)),
        h('button', { type: 'button', class: 'btn tp-new', 'data-fk': 'new', on: { click: () => this.openWizard() } }, icon('plus'), 'New instance')),
      t.description ? h('p', { class: 'tp-desc' }, t.description) : null,
      t.problems && t.problems.length ? this.check_('chg', 'The inputs are not described correctly', 'Fix these in $template.inputs of the definition.', t.problems) : null,
      h('h4', { class: 'h' }, 'Inputs'),
      this.inputsTable(t),
      h('h4', { class: 'h' }, `Instances · ${t.instances.length}`),
      t.instances.length ? this.instancesTable(t) : h('p', { class: 'tp-note' }, 'Nothing is made from this template yet.'));
  }

  inputsTable(t) {
    if (!t.inputs.length) return h('p', { class: 'tp-note' }, 'The template takes no input.');
    return h('div', { class: 'ac-tablewrap' },
      h('table', { class: 'ac-table small tp-table' },
        h('caption', { class: 'sr-only' }, `Inputs of ${t.title}`),
        h('thead', null, h('tr', null, ['Input', 'Takes', 'Default', 'Help'].map((x) => h('th', { scope: 'col' }, x)))),
        h('tbody', null, t.inputs.map((i) => h('tr', null,
          h('th', { scope: 'row', 'data-label': 'Input' }, h('code', { class: 'nm' }, i.name), i.label && i.label !== i.name ? h('span', { class: 'dim' }, ' ' + i.label) : null, i.required ? h('span', { class: 'b chg', title: 'The instance must give it a value' }, 'required') : null),
          h('td', { 'data-label': 'Takes' }, h('code', { class: 'tp-type' }, i.type || i.kind)),
          h('td', { 'data-label': 'Default' }, i.default ? h('code', null, i.default) : h('span', { class: 'dim' }, '–')),
          h('td', { 'data-label': 'Help' }, i.doc || h('span', { class: 'dim' }, '–')))))));
  }

  instancesTable(t) {
    const cols = t.inputs.slice(0, 4);
    return h('div', { class: 'ac-tablewrap' },
      h('table', { class: 'ac-table small tp-table' },
        h('caption', { class: 'sr-only' }, `Instances of ${t.title}`),
        h('thead', null, h('tr', null, [h('th', { scope: 'col' }, 'Instance'), ...cols.map((c) => h('th', { scope: 'col' }, c.name)), h('th', { scope: 'col' }, 'Resources')])),
        h('tbody', null, t.instances.map((n) => h('tr', null,
          h('th', { scope: 'row', 'data-label': 'Instance' },
            h('button', { type: 'button', class: 'se-chip tp-inst', 'data-fk': 'inst:' + n.address, title: `Show ${n.address} in the graph`, on: { click: () => this.cb.onNamespace && this.cb.onNamespace(n.address) } }, h('code', null, n.address))),
          ...cols.map((c) => h('td', { 'data-label': c.name }, n.values[c.name] !== undefined ? h('code', { class: 'tp-val' }, n.values[c.name]) : h('span', { class: 'dim' }, c.default ? 'default' : '–'))),
          h('td', { 'data-label': 'Resources' }, h('div', { class: 'tp-rc' }, h('span', { class: 'tp-res' }, `${n.recorded} of ${n.resources}`), n.recorded < n.resources ? h('span', { class: 'b chg' }, n.recorded ? 'partly applied' : 'not applied') : null)))))));
  }

  // ---- the wizard ----

  openWizard() {
    const t = this.tpl;
    if (!t) return;
    const at = t.targets.length ? t.targets[0].path : '';
    this.form = { label: '', at, custom: false, fields: {}, touchedName: false };
    this.wizard = true;
    this.result = null;
    this.refsFor = '';
    this.choices = {};
    this.vault = null;
    this.render({ templates: this.doc });
    this.cb.onRoute && this.cb.onRoute();
    const first = this.root.querySelector('[data-fk="label"]');
    if (first) first.focus({ preventScroll: true });
    this.runCheck();
  }

  closeWizard() {
    this.wizard = false;
    this.ctl && this.ctl.abort();
    this.render({ templates: this.doc });
    this.cb.onRoute && this.cb.onRoute();
  }

  wizardView() {
    const t = this.tpl;
    if (!this.form) this.form = { label: '', at: t.targets.length ? t.targets[0].path : '', custom: false, fields: {}, touchedName: false };
    const f = this.form;
    const pane = h('div', { class: 'tp-preview', 'aria-live': 'polite' });
    const code = h('div', { class: 'tp-code' });
    const checks = h('div', { class: 'tp-checks' });
    pane.append(code, checks);
    this.out = { code, checks };
    this.drawResult();

    const targets = t.targets.length ? t.targets : [];
    const atSel = h('div', { class: 'tp-seg', role: 'group', 'aria-label': 'Where the instance goes' },
      targets.map((x) => this.segBtn(x.path === '' ? 'top level' : x.path, !f.custom && f.at === x.path, () => { f.custom = false; f.at = x.path; this.changed(); this.redrawAt(); }, x.count > 1 ? `${x.count} instances` : '1 instance')),
      this.segBtn('top level', !f.custom && f.at === '' && !targets.some((x) => x.path === ''), () => { f.custom = false; f.at = ''; this.changed(); this.redrawAt(); }));
    this.atEl = atSel;

    return h('div', { class: 'tp-wiz' },
      h('div', { class: 'tp-wbar' },
        h('button', { type: 'button', class: 'tp-back', 'data-fk': 'back', on: { click: () => this.closeWizard() } }, icon('back'), `${t.title} · ${t.definition}`),
        h('h3', { class: 'tp-wtitle' }, 'New instance')),
      h('div', { class: 'tp-wcols' },
        h('form', { class: 'tp-form', 'aria-label': `A new ${t.title}`, on: { submit: (ev) => ev.preventDefault() } },
          this.field({ label: 'Name', help: 'The label of the instance. It is the last part of every address below it.', id: '@label', forId: 'label', required: true },
            h('input', { type: 'text', class: 'tp-in', 'data-fk': 'label', id: 'tpf-label', value: f.label, placeholder: 'checkout', autocomplete: 'off', spellcheck: 'false',
              on: { input: (ev) => { f.label = ev.target.value.trim(); this.syncName(); this.syncSecrets(); this.changed(); } } })),
          this.field({ label: 'Put it in', help: 'The struct the instance joins. Instances of this template already sit in these.', id: 'at' }, atSel),
          h('div', { class: 'tp-rule', 'aria-hidden': 'true' }),
          t.inputs.map((i) => this.inputField(i))),
        pane));
  }

  segBtn(label, on, click, tip) {
    return h('button', { type: 'button', class: 'tp-sb', 'aria-pressed': on ? 'true' : 'false', title: tip || null, on: { click } }, label);
  }

  redrawAt() {
    // rebuild the pressed state of the "put it in" buttons
    const f = this.form;
    const t = this.tpl;
    if (!this.atEl) return;
    const btns = this.atEl.querySelectorAll('.tp-sb');
    const targets = t.targets;
    btns.forEach((b, i) => {
      const path = i < targets.length ? targets[i].path : '';
      b.setAttribute('aria-pressed', !f.custom && f.at === path ? 'true' : 'false');
    });
  }

  field(o, ...control) {
    return h('div', { class: 'tp-field', 'data-field': o.id },
      h('label', { class: 'tp-fl', for: 'tpf-' + (o.forId || o.id) }, h('span', { class: 'tp-fn' }, o.label), o.required ? h('span', { class: 'tp-req' }, 'required') : null, o.tag ? h('code', { class: 'tp-ftype' }, o.tag) : null),
      o.help ? h('p', { class: 'tp-help' }, o.help) : null,
      ...control,
      h('p', { class: 'tp-err', 'data-err': o.id, hidden: true }));
  }

  inputField(i) {
    const f = this.form;
    const st = () => f.fields[i.name] || (f.fields[i.name] = { text: '', ref: false });
    const row = h('div', { class: 'tp-ctl' });
    const holder = h('div', { class: 'tp-refs', hidden: true });
    const draw = () => {
      clear(row);
      const cur = st();
      if (i.secret) { this.secretControl(i, row, draw); return; }
      if (cur.ref) {
        row.append(h('div', { class: 'tp-refval' }, icon('link'), h('code', null, cur.text),
          cur.value_state ? h('span', { class: 'tp-rv ' + cur.value_state, title: 'What the state holds now. An apply may produce another value.' }, '= ', h('code', null, refValue(cur))) : null,
          h('button', { type: 'button', class: 'tp-x', 'aria-label': `Choose another value for ${i.name}`, title: 'Choose another', on: { click: () => { f.fields[i.name] = { text: '', ref: false }; draw(); this.changed(); } } }, icon('x'))));
        return;
      }
      if (i.choices) { this.choiceControl(i, row, draw, cur); return; }
      if (i.entity) { this.entityControl(i, row, draw); return; }
      if (i.kind === 'list' && i.elem) { this.chipsControl(i, row, cur); return; }
      if (i.kind === 'enum' || i.kind === 'bool') {
        const opts = i.kind === 'bool' ? ['true', 'false'] : i.options;
        const dflt = i.default;
        row.append(h('div', { class: 'tp-seg', role: 'group', 'aria-label': i.name },
          opts.map((o) => {
            const label = o.replace(/^"|"$/g, '');
            const chosen = cur.text ? cur.text === o : false;
            const isDefault = !cur.text && dflt === o;
            return h('button', { type: 'button', class: 'tp-sb' + (isDefault ? ' dflt' : ''), 'aria-pressed': chosen || isDefault ? 'true' : 'false', title: isDefault ? 'The default' : null, 'data-fk': `${i.name}=${label}`,
              on: { click: () => { cur.text = dflt === o ? '' : o; draw(); this.changed(); } } }, label);
          })));
      } else if (i.long) {
        row.append(h('textarea', { class: 'tp-in tp-ta', id: 'tpf-' + i.name, 'data-fk': 'in:' + i.name, rows: 4, placeholder: i.default && i.default !== '""' ? `default ${i.default}` : 'A few lines of text', spellcheck: 'true', value: cur.text,
          on: { input: (ev) => { cur.text = ev.target.value; this.changed(); } } }));
      } else if (i.kind === 'object' || i.kind === 'list' || i.kind === 'any') {
        row.append(h('textarea', { class: 'tp-in tp-ta', id: 'tpf-' + i.name, rows: 3, placeholder: 'CUE', spellcheck: 'false', value: cur.text,
          on: { input: (ev) => { cur.text = ev.target.value; this.changed(); } } }));
      } else {
        row.append(h('input', { type: 'text', class: 'tp-in', id: 'tpf-' + i.name, 'data-fk': 'in:' + i.name, value: cur.text, autocomplete: 'off', spellcheck: 'false',
          inputmode: i.kind === 'int' || i.kind === 'number' ? 'numeric' : null, placeholder: i.default ? `default ${i.default}` : i.required ? 'needed' : '',
          on: { input: (ev) => { cur.text = ev.target.value; if (i.name === 'name') f.touchedName = true; this.changed(); } } }));
      }
      if (['string', 'int', 'number', 'bool'].includes(i.kind) && !i.long) {
        row.append(h('button', { type: 'button', class: 'tp-gb', 'data-fk': 'graph:' + i.name, 'aria-expanded': this.refsFor === i.name ? 'true' : 'false', on: { click: () => this.toggleRefs(i, draw, holder) } }, icon('link'), 'From the graph'));
      }
    };
    draw();
    const tag = i.secret ? 'secret' : i.choices ? 'from ' + i.choices.split('.')[0] : i.entity ? i.entity.type : i.kind === 'enum' ? 'one of' : i.kind === 'list' && i.elem ? 'list of ' + i.elem : i.long ? 'text' : i.type && i.type.length < 34 ? i.type : i.kind;
    return this.field({ label: i.label || i.name, help: i.doc, id: i.name, required: i.required, tag }, row, holder);
  }

  // ---- values that are not typed: choices, existing resources, lists, secrets ----

  // choiceControl offers the values that a node of the graph holds, as the state has them.
  choiceControl(i, row, draw, cur) {
    const c = this.choices[i.name];
    if (!c) {
      row.append(h('p', { class: 'tp-note' }, 'Reading ' + i.choices + '…'));
      api.templateChoices(this.tpl.name, i.name).then((r) => { this.choices[i.name] = r; draw(); }, () => { this.choices[i.name] = { state: 'error', items: [], note: 'The values could not be read.' }; draw(); });
      return;
    }
    if (c.state === 'ok') {
      const sel = h('select', { class: 'tp-in tp-sel', id: 'tpf-' + i.name, 'data-fk': 'in:' + i.name, 'aria-label': i.label || i.name,
        on: { change: (ev) => { cur.text = ev.target.value; cur.pick = ev.target.value; this.changed(); } } },
      h('option', { value: '' }, i.required ? 'Choose…' : 'Choose, or leave it out'),
      c.items.map((x) => h('option', { value: x.value, selected: cur.pick === x.value ? true : null }, x.label ? `${x.value} — ${x.label}` : x.value)));
      row.append(sel);
      const age = c.read_at ? relTime(c.read_at) : '';
      row.append(h('p', { class: 'tp-note ok' }, icon('check'), h('span', null, `${plural(c.items.length, 'value')} that `, h('code', null, c.node), ' holds' + (age ? `, read ${age}` : '') + '. A plan checks the choice against what it reads then.')));
    } else {
      row.append(h('input', { type: 'text', class: 'tp-in', id: 'tpf-' + i.name, 'data-fk': 'in:' + i.name, value: cur.pick || '', autocomplete: 'off', spellcheck: 'false', placeholder: 'needed',
        on: { input: (ev) => { cur.pick = ev.target.value; cur.text = ev.target.value; this.changed(); } } }));
      row.append(h('p', { class: 'tp-note warn' }, icon('bang'), (c.note || 'The values could not be read.') + ' Type one; a plan checks it.'));
    }
  }

  // entityControl lists the resources of a type that the estate declares; a click writes the reference to one of them.
  entityControl(i, row, draw) {
    const f = this.form;
    const list = h('ul', { class: 'tp-rl tp-el', role: 'listbox', 'aria-label': `Existing ${i.entity.type}` });
    const note = h('p', { class: 'tp-note' });
    const q = h('input', { type: 'search', class: 'tp-in tp-rq', placeholder: `Find a ${i.entity.type}`, 'aria-label': `Find a ${i.entity.type}`, 'data-fk': 'entq:' + i.name, autocomplete: 'off' });
    const load = async () => {
      let r;
      try { r = await api.templateEntities(this.tpl.name, i.name, q.value); } catch (err) { clear(list); note.textContent = 'The resources could not be listed.'; return; }
      clear(list);
      note.textContent = r.note || '';
      for (const e of r.entities) {
        list.append(h('li', { role: 'option' }, h('button', { type: 'button', class: 'tp-ri tp-er', 'data-fk': 'ent:' + e.addr, on: { click: () => {
          f.fields[i.name] = { text: e.expr, ref: true, value: e.value, value_state: e.value_state };
          draw();
          this.changed();
        } } }, h('code', null, e.addr), h('span', { class: 'tp-rm' }, e.recorded ? null : h('span', { class: 'tp-rt' }, 'not applied'), h('span', { class: 'tp-rv ' + e.value_state }, refValue(e))))));
      }
    };
    q.addEventListener('input', debounce(load, 160));
    row.append(h('p', { class: 'tp-note' }, icon('link'), h('span', null, `The ${i.entity.type} resources the estate declares. The instance reads `, h('code', null, i.entity.attr), ' of the one you pick, so it follows that resource.')), q, list, note);
    load();
  }

  // chipsControl is a list of values: each is added with Enter or a comma, and a click on one takes it away.
  chipsControl(i, row, cur) {
    cur.items = cur.items || [];
    const chips = h('ul', { class: 'tp-chips', 'aria-label': `Values of ${i.name}` });
    const input = h('input', { type: 'text', class: 'tp-in tp-chipin', id: 'tpf-' + i.name, 'data-fk': 'in:' + i.name, autocomplete: 'off', spellcheck: 'false', placeholder: 'Type a value, then Enter' });
    const paint = () => {
      clear(chips);
      cur.items.forEach((x, n) => chips.append(h('li', null, h('span', { class: 'tp-chip' }, h('code', null, x),
        h('button', { type: 'button', class: 'tp-x', 'aria-label': `Take out ${x}`, 'data-fk': `chip-x:${i.name}:${n}`, on: { click: () => { cur.items.splice(n, 1); paint(); this.changed(); } } }, icon('x'))))));
    };
    const add = () => {
      const v = input.value.trim().replace(/,$/, '').trim();
      if (!v) return;
      if (i.elem !== 'string' && Number.isNaN(Number(v))) { input.setCustomValidity('A number'); input.reportValidity(); return; }
      input.setCustomValidity('');
      if (!cur.items.includes(v)) cur.items.push(v);
      input.value = '';
      paint();
      this.changed();
    };
    input.addEventListener('keydown', (ev) => { if (ev.key === 'Enter' || ev.key === ',') { ev.preventDefault(); add(); } else if (ev.key === 'Backspace' && !input.value && cur.items.length) { cur.items.pop(); paint(); this.changed(); } });
    input.addEventListener('blur', add);
    paint();
    row.append(chips, input);
  }

  // secretControl asks where a secret comes from and what it is called. It never takes the value: the code reads the secret
  // with pravo.#Secret or pravo.#SecretEnv, and the value is set with a command, away from this page.
  secretControl(i, row, draw) {
    const f = this.form;
    const t = this.tpl;
    const cur = f.fields[i.name] || (f.fields[i.name] = { text: '', ref: false });
    if (!cur.secret) cur.secret = { source: 'state', name: '', touched: false };
    const sc = cur.secret;
    if (this.vault === null) {
      this.vault = { sealed: false, names: [], loading: true };
      api.secrets().then((r) => { this.vault = { sealed: !!r.sealed, names: (r.secrets || []).filter((x) => x.status === 'set').map((x) => x.name) }; if (!sc.touched && !this.vault.sealed) sc.source = 'env'; sc.name = sc.touched ? sc.name : secretName(sc.source, t.name, f.label, i.name); draw(); this.changed(); },
        () => { this.vault = { sealed: false, names: [] }; sc.source = 'env'; sc.name = secretName('env', t.name, f.label, i.name); draw(); this.changed(); });
    }
    if (!sc.touched) sc.name = secretName(sc.source, t.name, f.label, i.name);
    const vault = this.vault;
    const seg = h('div', { class: 'tp-seg', role: 'group', 'aria-label': 'Where the secret comes from' },
      h('button', { type: 'button', class: 'tp-sb', 'data-fk': `src:${i.name}:state`, 'aria-pressed': sc.source === 'state' ? 'true' : 'false', disabled: vault.sealed ? null : true, title: vault.sealed ? null : 'The state is not sealed, so it keeps no secret',
        on: { click: () => { sc.source = 'state'; if (!sc.touched) sc.name = secretName('state', t.name, f.label, i.name); draw(); this.changed(); } } }, 'Sealed state'),
      h('button', { type: 'button', class: 'tp-sb', 'data-fk': `src:${i.name}:env`, 'aria-pressed': sc.source === 'env' ? 'true' : 'false',
        on: { click: () => { sc.source = 'env'; if (!sc.touched) sc.name = secretName('env', t.name, f.label, i.name); draw(); this.changed(); } } }, 'Environment variable'));
    const name = h('input', { type: 'text', class: 'tp-in tp-mono', id: 'tpf-' + i.name, 'data-fk': 'secret:' + i.name, value: sc.name, autocomplete: 'off', spellcheck: 'false', 'aria-label': sc.source === 'env' ? 'Name of the variable' : 'Name of the secret',
      on: { input: (ev) => { sc.name = ev.target.value.trim(); sc.touched = true; this.changed(); } } });
    row.append(h('div', { class: 'tp-lock' }, icon('lock'), h('span', null, 'A secret. This page never takes its value.')), seg, name);
    if (sc.source === 'state' && vault.names.length) {
      row.append(h('div', { class: 'tp-have' }, h('span', { class: 'tp-hl' }, 'Already in the state'),
        vault.names.slice(0, 6).map((n) => h('button', { type: 'button', class: 'tp-sb tp-hv', 'data-fk': 'have:' + n, 'aria-pressed': sc.name === n ? 'true' : 'false',
          on: { click: () => { sc.name = n; sc.touched = true; draw(); this.changed(); } } }, n))));
    }
  }

  // syncSecrets renames the secrets that the person has not named, as the label changes.
  syncSecrets() {
    const f = this.form;
    for (const i of this.tpl.inputs) {
      const sc = i.secret && f.fields[i.name] && f.fields[i.name].secret;
      if (!sc || sc.touched) continue;
      sc.name = secretName(sc.source, this.tpl.name, f.label, i.name);
      const el = this.root.querySelector('#tpf-' + i.name);
      if (el) el.value = sc.name;
    }
  }

  // syncName keeps the input called name equal to the label until the person types in it.
  syncName() {
    const f = this.form;
    const t = this.tpl;
    if (!t.inputs.some((i) => i.name === 'name' && i.kind === 'string') || f.touchedName) return;
    f.fields.name = { text: f.label, ref: false };
    const el = this.root.querySelector('#tpf-name');
    if (el) el.value = f.label;
  }

  // ---- values from the graph ----

  async toggleRefs(i, draw, holder) {
    const f = this.form;
    if (this.refsFor === i.name) { this.refsFor = ''; holder.hidden = true; clear(holder); this.root.querySelectorAll('.tp-gb').forEach((b) => b.setAttribute('aria-expanded', 'false')); return; }
    this.refsFor = i.name;
    this.root.querySelectorAll('.tp-gb').forEach((b) => b.setAttribute('aria-expanded', b.dataset.fk === 'graph:' + i.name ? 'true' : 'false'));
    this.root.querySelectorAll('.tp-refs').forEach((r) => { if (r !== holder) { r.hidden = true; clear(r); } });
    holder.hidden = false;
    clear(holder);
    const q = h('input', { type: 'search', class: 'tp-in tp-rq', placeholder: 'Find a value: network, id, hex…', 'aria-label': 'Find a value of the graph', 'data-fk': 'refq', autocomplete: 'off' });
    const list = h('ul', { class: 'tp-rl', role: 'listbox', 'aria-label': 'Values of the graph' });
    holder.append(h('p', { class: 'tp-rh' }, 'These are attributes of resources the estate already declares. The instance will read the value when it is applied.'), q, list);
    const load = async () => {
      let r;
      try { r = await api.templateRefs(i.kind, q.value); } catch (err) { clear(list); list.append(h('li', { class: 'tp-ri none' }, 'Could not read the graph.')); return; }
      clear(list);
      if (!r.refs.length) { list.append(h('li', { class: 'tp-ri none' }, 'No attribute of this kind matches.')); return; }
      for (const ref of r.refs) {
        list.append(h('li', { role: 'option' }, h('button', { type: 'button', class: 'tp-ri', 'data-fk': 'ref:' + ref.expr, on: { click: () => {
          f.fields[i.name] = { text: ref.expr, ref: true, value: ref.value, value_state: ref.value_state };
          this.refsFor = '';
          holder.hidden = true; clear(holder);
          draw();
          this.changed();
          const b = this.root.querySelector(`[data-fk="${CSS.escape('graph:' + i.name)}"]`);
          if (b) b.focus({ preventScroll: true });
        } } }, h('code', null, ref.expr), h('span', { class: 'tp-rm' }, h('span', { class: 'tp-rt' }, ref.type), h('span', { class: 'tp-rv ' + ref.value_state }, refValue(ref))))));
      }
    };
    q.addEventListener('input', debounce(load, 160));
    await load();
    q.focus({ preventScroll: true });
    holder.scrollIntoView({ block: 'nearest' });
  }

  // ---- checking ----

  changed() {
    this.pending = true;
    this.check();
  }

  params() {
    const t = this.tpl;
    const f = this.form;
    const q = new URLSearchParams();
    q.set('template', t.name);
    q.set('name', f.label || '');
    q.set('at', f.at || '');
    for (const i of t.inputs) {
      if (i.secret) {
        const sf = f.fields[i.name];
        if (sf && sf.secret && sf.secret.name) { q.set('s.' + i.name, sf.secret.name); q.set('sk.' + i.name, sf.secret.source); }
        continue;
      }
      const v = cueValue(i, f.fields[i.name]);
      if (v) q.set('v.' + i.name, v);
    }
    return q;
  }

  async runCheck() {
    if (!this.wizard || !this.tpl || !this.form) return;
    const label = this.form.label;
    if (!label) { this.result = { state: 'nolabel' }; this.drawResult(); return; }
    this.ctl && this.ctl.abort();
    const ctl = (this.ctl = new AbortController());
    const seq = ++this.seq;
    this.busy = true;
    this.drawResult();
    try {
      const r = await api.templateTry(this.params(), ctl.signal);
      if (seq !== this.seq) return;
      this.result = r;
    } catch (err) {
      if (err && err.name === 'AbortError') return;
      if (seq !== this.seq) return;
      this.result = { state: 'error', message: err && err.message ? err.message : 'The check failed.' };
    }
    this.busy = false;
    this.drawResult();
  }

  // drawResult redraws the code and the checks; the form is left alone so that it keeps focus.
  drawResult() {
    if (!this.out) return;
    const { code, checks } = this.out;
    const r = this.result;
    clear(code);
    clear(checks);
    for (const el of this.root.querySelectorAll('.tp-err')) { el.hidden = true; el.textContent = ''; }
    for (const el of this.root.querySelectorAll('.tp-field.bad')) el.classList.remove('bad');
    if (!r || r.state === 'nolabel') {
      code.append(h('p', { class: 'tp-wait' }, 'Give the instance a name and the code appears here, checked against the specification.'));
      return;
    }
    if (r.state === 'error') {
      checks.append(h('p', { class: 'tp-warn' }, icon('bang'), h('span', null, r.message)));
      return;
    }
    const snippet = r.snippet || '';
    const lines = snippet.replace(/\n$/, '').split('\n').map((text, i) => ({ no: i + 1, text }));
    const win = codeWindow({ file: r.file ? `paste into ${r.file}` : 'paste into the specification', lines, kind: 'file' }, { cls: 'tp-cw', title: 'The code of the new instance' });
    code.append(h('div', { class: 'tp-ch' },
      h('h4', { class: 'h' }, 'The code'),
      h('button', { type: 'button', class: 'btn tp-copy', 'data-copy': snippet, 'data-fk': 'copy-code' }, icon('copy'), 'Copy the code')),
      win,
      h('p', { class: 'tp-paste' }, r.file ? [`Paste it into `, h('code', null, r.file), `, next to the other instances, or into any file of the package. `] : '',
        `It becomes `, h('code', null, r.address), '.'));

    const rows = [];
    // the specification
    if (r.state === 'valid') rows.push(this.check_('ok', 'The specification loads with it', 'Every constraint of the template holds for these values.'));
    else if (r.state === 'incomplete') rows.push(this.check_('chg', 'Not complete yet', null, r.problems.map((p) => `${(this.tpl.inputs.find((x) => x.name === p.field) || {}).label || p.field}: ${p.message}`)));
    else if (r.state === 'exists') rows.push(this.check_('bad', `${r.address} is taken`, 'Choose another name.'));
    else rows.push(this.check_('bad', r.problems.length === 1 ? 'The specification refuses it' : `The specification refuses it · ${r.problems.length} problems`, null, r.problems.map((p) => p.message)));
    for (const p of r.state === 'incomplete' ? [] : r.problems || []) {
      if (!p.field) continue;
      const fld = this.root.querySelector(`.tp-field[data-field="${CSS.escape(p.field)}"]`);
      if (fld) { fld.classList.add('bad'); const e = fld.querySelector('.tp-err'); if (e) { e.hidden = false; e.textContent = p.message; } }
    }
    // what it adds
    if (r.creates && r.creates.length) {
      rows.push(this.check_('', `Adds ${plural(r.creates.length, 'resource')}`, null, r.creates.map((c) => `${c.address}  ·  ${c.type}`), true));
    }
    // the secrets the code reads
    if (r.secrets && r.secrets.length) {
      const missing = r.secrets.filter((x) => x.source === 'state' && !x.exists);
      rows.push(this.secretCheck(missing.length ? 'chg' : 'ok', missing.length ? 'A secret is not set yet' : r.secrets.length === 1 ? 'The secret is in place' : 'The secrets are in place', r.secrets));
    }
    // the policies
    if (r.policy) {
      const p = r.policy;
      const tone = { holds: 'ok', refused: 'bad', unsure: 'chg', none: '' }[p.state] || '';
      const title = p.state === 'holds' ? 'Every policy in force holds' : p.state === 'refused' ? 'A policy would refuse the plan' : p.state === 'unsure' ? 'Policies: not everything could be decided' : 'No policy to judge by';
      const items = p.policies.map((x) => ({ tone: x.outcome === 'holds' ? 'ok' : x.outcome === 'refused' ? 'bad' : 'chg', name: x.name, status: x.status === 'declared' ? 'declared, not applied yet' : 'in force', outcome: x.outcome, issues: x.issues || [] }));
      rows.push(this.policyCheck(tone, title, p.note, items));
    }
    checks.append(...rows);
    if (this.busy) checks.classList.add('busy'); else checks.classList.remove('busy');
    // keep the copy button's text in step
    const cb = code.querySelector('.tp-copy');
    if (cb) cb.setAttribute('data-copy', snippet);
  }

  check_(tone, title, text, items, mono) {
    return h('section', { class: 'tp-check' + (tone ? ' ' + tone : ''), role: 'group', 'aria-label': title },
      h('span', { class: 'tp-dot', 'aria-hidden': 'true' }),
      h('div', { class: 'tp-cb' },
        h('h5', { class: 'tp-chead' }, title),
        text ? h('p', { class: 'tp-ctext' }, text) : null,
        items && items.length ? h('ul', { class: 'tp-ci' + (mono ? ' mono' : '') }, items.slice(0, 12).map((x) => h('li', null, mono ? h('code', null, x) : x))) : null));
  }

  secretCheck(tone, title, secrets) {
    return h('section', { class: 'tp-check' + (tone ? ' ' + tone : ''), role: 'group', 'aria-label': title },
      h('span', { class: 'tp-dot', 'aria-hidden': 'true' }),
      h('div', { class: 'tp-cb' },
        h('h5', { class: 'tp-chead' }, title),
        h('ul', { class: 'tp-sec' }, secrets.map((x) => h('li', null,
          h('div', { class: 'tp-sl' }, h('code', null, x.input), h('span', { class: 'tp-arrow', 'aria-hidden': 'true' }, '←'), h('code', null, x.name),
            h('span', { class: 'tp-ps' }, x.source === 'env' ? 'environment variable' : 'sealed state'),
            x.source === 'state' ? h('span', { class: 'tp-po ' + (x.exists ? 'yes' : 'no') }, x.exists ? 'set' : 'not set') : null),
          x.source === 'state' && !x.exists ? h('div', { class: 'tp-cmd' }, h('code', null, x.command),
            h('button', { type: 'button', class: 'btn tp-copy sm', 'data-copy': x.command, 'data-fk': 'copy-cmd:' + x.input }, icon('copy'), 'Copy')) : null))),
        h('p', { class: 'tp-ctext tp-approx' }, 'The code declares the secret and reads its value; the value itself is never typed here. The template must pass it only to a write-only attribute, to provider configuration or to an ephemeral resource.')));
  }

  policyCheck(tone, title, note, items) {
    return h('section', { class: 'tp-check' + (tone ? ' ' + tone : ''), role: 'group', 'aria-label': title },
      h('span', { class: 'tp-dot', 'aria-hidden': 'true' }),
      h('div', { class: 'tp-cb' },
        h('h5', { class: 'tp-chead' }, title),
        items.length ? h('ul', { class: 'tp-pol' }, items.map((x) => h('li', { class: x.tone },
          h('span', { class: 'tp-pd', 'aria-hidden': 'true' }),
          h('div', null,
            h('div', { class: 'tp-pn' }, h('code', null, x.name), h('span', { class: 'tp-ps' }, x.status), h('span', { class: 'tp-po' }, x.outcome === 'holds' ? 'holds' : x.outcome === 'refused' ? 'refuses' : 'not decided')),
            x.issues.length ? h('ul', { class: 'tp-pi' }, x.issues.map((s) => h('li', null, s))) : null)))) : null,
        note ? h('p', { class: 'tp-ctext tp-approx' }, note) : null));
  }
}
