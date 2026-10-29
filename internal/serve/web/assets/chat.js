// The Ask panel: a question to a language model that can only read what this page reads. It sits in the slot of the
// inspector. The server holds no conversation: the page keeps it, and sends it with each question.

import * as api from './api.js';
import { h, clear, plural, icon } from './util.js';

const OK_KEY = 'pravo.chat.ok';
const store = {
  get() { try { return localStorage.getItem(OK_KEY) === '1'; } catch { return false; } },
  set() { try { localStorage.setItem(OK_KEY, '1'); } catch { /* the notice is shown again */ } },
};

const kb = (n) => (n < 1024 ? n + ' B' : (n / 1024).toFixed(1) + ' KB');

// what a read says in a line: "node api.db"
function readLine(tool, args) {
  let a = {};
  try { a = JSON.parse(args || '{}'); } catch { /* shown without */ }
  const v = a.addr || a.q || a.id || '';
  return v ? `${tool} ${v}` : tool;
}

export class ChatView {
  // opts: context() -> {addr, plan, view}; isAddr(a); isPlan(id); onNode(a); onPlan(id); onToggle(open)
  constructor(root, opts) {
    this.root = root;
    this.o = opts;
    this.cfg = { enabled: false };
    this.open_ = false;
    this.msgs = [];
    this.reads = [];
    this.busy = false;
    this.err = '';
    this.chipOff = null; // the address whose chip was cleared
    this.draft = '';
    this.ctl = null;
    this.built = false;
  }

  get enabled() { return !!this.cfg.enabled; }
  get isOpen() { return this.open_; }

  async init() {
    try { this.cfg = await api.chatConfig(); } catch { this.cfg = { enabled: false }; }
    return this.enabled;
  }

  open() { if (!this.enabled) return; this.open_ = true; this.root.hidden = false; this.render(); this.o.onToggle(true); const i = this.root.querySelector('textarea'); if (i) i.focus(); }
  close() { if (!this.open_) return; this.open_ = false; this.root.hidden = true; this.o.onToggle(false); }
  toggle() { if (this.open_) this.close(); else this.open(); }

  // the selection or the section changed
  refreshContext() { if (this.open_ && this.built) this.renderCtx(); }

  ctx() {
    const c = this.o.context();
    if (this.chipOff && c.addr !== this.chipOff) this.chipOff = null;
    return Object.assign({}, c, { addr: c.addr && c.addr === this.chipOff ? '' : c.addr });
  }

  render() {
    clear(this.root);
    this.built = false;
    this.root.classList.toggle('noticing', !store.get());
    this.root.append(h('div', { class: 'chat-head' },
      h('h2', { text: 'Ask' }),
      h('span', { class: 'who', text: `${this.cfg.provider} · ${this.cfg.model}`, title: `Questions go to ${this.cfg.host}` })));
    if (!store.get()) { this.root.append(this.notice()); return; }
    this.ctxEl = h('div', { class: 'chat-ctx' });
    this.logEl = h('div', { class: 'chat-log', tabindex: 0, role: 'log', 'aria-live': 'polite', 'aria-label': 'Conversation' });
    this.chipsEl = h('div', { class: 'chips' });
    this.input = h('textarea', { rows: 1, placeholder: 'Ask about this view', 'aria-label': 'Your question', maxlength: 2000, value: this.draft, on: {
      input: () => { this.draft = this.input.value; this.sizeInput(); this.syncSend(); },
      keydown: (ev) => {
        if (ev.key === 'Enter' && !ev.shiftKey && !ev.isComposing) { ev.preventDefault(); this.send(); }
        else if (ev.key === 'Escape') { ev.stopPropagation(); this.close(); }
      } } });
    this.sendBtn = h('button', { class: 'go', type: 'button', 'aria-label': 'Send', on: { click: () => this.send() } }, '↵');
    this.root.append(this.ctxEl, this.logEl,
      h('div', { class: 'chat-foot' }, this.chipsEl, h('div', { class: 'ask' }, this.input, this.sendBtn),
        h('div', { class: 'chat-fine' }, icon('eye-off'), 'Reads the redacted views of this page. It cannot change anything.')));
    this.built = true;
    this.renderCtx();
    this.renderLog();
    this.sizeInput();
    this.syncSend();
  }

  notice() {
    const c = this.cfg;
    const row = (cls, dt, ...dd) => h('div', { class: 'r ' + cls }, h('dt', { text: dt }), h('dd', null, ...dd));
    return h('div', { class: 'note' },
      h('h3', { text: 'Ask questions about this estate.' }),
      h('p', { class: 'lede', text: 'The assistant reads what this page shows, through the same redaction, and answers in words. It cannot change anything.' }),
      h('dl', { class: 'facts' },
        row('', 'Provider', h('code', { text: c.provider }), ' · ', h('code', { text: c.model }), h('br'), h('span', { class: 'never' }, 'Questions go to ', h('code', { text: c.host || '' }))),
        row('', 'Key', 'Read from the environment of the ', h('code', { text: 'pravo serve' }), ' process. This page never sees it.'),
        row('sent', 'Sent', 'Only what a question needs: resource addresses, types, values that are not sensitive, plan text, run names.'),
        row('safe', 'Never sent', 'Secret values, withheld attributes, the private blob, provider configuration, credentials, the state itself.'),
        row('', 'Read', (c.tools || []).join(', ')),
        row('', 'Check', 'Every answer lists what was sent for it.')),
      h('div', { class: 'row' },
        h('button', { class: 'btn primary', type: 'button', on: { click: () => { store.set(); this.render(); const i = this.root.querySelector('textarea'); if (i) i.focus(); } } }, 'Turn on for this project'),
        h('button', { class: 'btn', type: 'button', on: { click: () => this.close() } }, 'Not now')));
  }

  renderCtx() {
    const c = this.ctx();
    clear(this.ctxEl);
    const bits = [];
    if (c.addr) bits.push(h('span', { class: 'x' }, c.addr, h('button', { type: 'button', 'aria-label': 'Do not use ' + c.addr, on: { click: () => { this.chipOff = c.addr; this.renderCtx(); } } }, icon('x'))));
    else if (c.plan) bits.push(h('span', { class: 'x', text: c.plan }));
    else bits.push(h('span', { class: 'x', text: 'the whole estate' }));
    this.ctxEl.append('About ', ...bits, h('span', { class: 'grow' }), 'reads only');
    this.renderChips();
  }

  renderChips() {
    clear(this.chipsEl);
    if (this.msgs.length || this.busy) return;
    const c = this.ctx();
    const qs = c.addr ? ['What reaches it?', 'Why is it like this?'] : ['What needs attention?', 'What changed last?'];
    for (const q of qs) this.chipsEl.append(h('button', { type: 'button', text: q, on: { click: () => this.send(q) } }));
  }

  sizeInput() { this.input.style.height = 'auto'; this.input.style.height = Math.min(this.input.scrollHeight, 140) + 'px'; }
  syncSend() { const on = !this.busy && this.input.value.trim() !== ''; this.sendBtn.classList.toggle('on', on); this.sendBtn.disabled = !on; }

  renderLog() {
    clear(this.logEl);
    if (!this.msgs.length && !this.busy) {
      const c = this.ctx();
      this.logEl.append(h('p', { class: 'chat-empty' }, 'Ask about ', h('b', { text: c.addr || 'the estate' }), '. Answers link to the resources and plans they are about.'));
    }
    for (const m of this.msgs) {
      if (m.role === 'user') this.logEl.append(h('div', { class: 'q' }, h('small', { text: 'You' }), m.text));
      else this.logEl.append(this.answer(m));
    }
    if (this.busy) {
      this.logEl.append(h('div', { class: 'work', role: 'status' },
        ...this.reads.map((r) => h('div', null, h('span', { class: 'st' }), readLine(r.tool, r.args))),
        h('div', null, h('span', { class: 'st run' }), this.reads.length ? 'writing the answer' : 'reading')));
    }
    if (this.err) this.logEl.append(h('div', { class: 'err', role: 'alert' }, h('span', { text: this.err }), h('button', { class: 'btn', type: 'button', text: 'Try again', on: { click: () => this.retry() } })));
    this.logEl.scrollTop = this.logEl.scrollHeight;
  }

  answer(m) {
    const bytes = (m.sent || []).reduce((n, s) => n + (s.bytes || 0), 0);
    const el = h('div', { class: 'a' }, h('div', { class: 'a-h' }, h('span', { text: 'Answer' })), h('div', { class: 'a-b' }, ...this.prose(m.text)));
    if (m.sent && m.sent.length) {
      el.append(h('details', { class: 'ledger' },
        h('summary', null, icon('chev'), `Sent to the model · ${plural(m.sent.length, 'read')} · ${kb(bytes)}`),
        h('div', { class: 'rows' }, ...m.sent.map((s) => h('details', { class: 'row' },
          h('summary', null, h('b', { text: readLine(s.tool, JSON.stringify(s.args)) }), h('span', { text: s.error ? 'refused' : kb(s.bytes || 0) })),
          h('pre', { text: s.error ? s.error : s.body || '' })))),
        h('div', { class: 'withheld', text: 'Withheld values reach the model as the marker the page shows, never as the value.' })));
    }
    return el;
  }

  // prose turns the text of an answer into elements, without HTML: paragraphs, `code` and fenced blocks. A name in backticks
  // that is a resource or a stored plan of this estate is a link to it; a name that is not stays text.
  prose(text) {
    const out = [];
    const parts = String(text).split(/```[a-z]*\n?/);
    parts.forEach((part, i) => {
      if (i % 2 === 1) {
        const cmd = part.replace(/\n$/, '');
        if (!cmd.trim()) return;
        out.push(h('div', { class: 'cmd' },
          h('div', { class: 'cmdline' }, h('span', { class: 'ps', text: '$' }), h('code', { text: cmd }), h('span', { class: 'grow' }),
            h('button', { class: 'cpbtn', type: 'button', 'aria-label': 'Copy the command', data: { copy: cmd } }, icon('copy'))),
          h('small', { text: 'Run it yourself. This page runs nothing.' })));
        return;
      }
      for (const para of part.split(/\n{2,}/)) {
        if (!para.trim()) continue;
        out.push(h('p', null, ...para.trim().split(/(`[^`\n]+`)/).map((seg) => (seg.length > 2 && seg[0] === '`' && seg.endsWith('`') ? this.token(seg.slice(1, -1)) : seg))));
      }
    });
    return out;
  }

  token(t) {
    if (this.o.isAddr(t)) return h('button', { class: 'ref', type: 'button', title: 'Show ' + t, on: { click: () => this.o.onNode(t) } }, icon('stack'), t);
    if (/^plan-[0-9a-f]{6,}$/.test(t) && this.o.isPlan(t)) return h('button', { class: 'ref plan', type: 'button', title: 'Show ' + t, on: { click: () => this.o.onPlan(t) } }, icon('file'), t);
    return h('code', { text: t });
  }

  retry() {
    const last = this.msgs[this.msgs.length - 1];
    if (last && last.role === 'user') { const q = last.text; this.msgs.pop(); this.send(q); }
  }

  async send(text) {
    const q = (text || this.input.value).trim();
    if (!q || this.busy) return;
    this.err = '';
    this.msgs.push({ role: 'user', text: q });
    this.draft = '';
    this.input.value = '';
    this.sizeInput();
    this.busy = true;
    this.reads = [];
    this.syncSend();
    this.renderChips();
    this.renderLog();
    const turns = this.msgs.map((m) => ({ role: m.role, text: m.text }));
    const ctl = (this.ctl = new AbortController());
    let answered = false;
    try {
      await api.askChat(turns, this.ctx(), (e) => {
        if (e.type === 'read') { this.reads.push(e); this.renderLog(); }
        else if (e.type === 'text') { this.msgs.push({ role: 'assistant', text: e.text }); answered = true; }
        else if (e.type === 'done') { const a = this.msgs[this.msgs.length - 1]; if (answered && a) a.sent = e.sent || []; }
        else if (e.type === 'error') this.err = e.text || 'The assistant failed.';
      }, ctl.signal);
      if (!answered && !this.err) this.err = 'The assistant gave no answer.';
    } catch (e) {
      if (!(e && e.name === 'AbortError')) this.err = e && e.message ? e.message : 'The assistant could not be reached.';
    }
    this.busy = false;
    this.ctl = null;
    this.syncSend();
    this.renderLog();
  }
}
