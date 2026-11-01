package main

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// A shot of the policy studio types a rule into the editor and turns the switch
// to what the rule is tried on, as a person does: no fragment reaches that view.
// The rules are the files of the scenario, so that a page shows the text that
// the picture holds.

// rule is the text of the file rules/NAME.cue of the scenario.
func rule(scenario, name string) string {
	data, err := scenarioFS.ReadFile(path.Join("scenarios", scenario, "rules", name+".cue"))
	if err != nil {
		panic(err)
	}
	return string(data)
}

// studioScript is the script of a shot of the studio: it writes text into the
// editor and dispatches the event that a keystroke does, so that the page runs
// the rule, and turns the switch to target, "resources" or "plan". For the
// target "plan", plan is the label of the plan to look at, the name of a plan
// file or the id of a stored plan. For the target "resources", open is the
// address of a resource that fails, whose row is opened, or empty.
func studioScript(text, target, plan, open string) string {
	if target == "plan" && plan == "" || target == "resources" && plan != "" || target != "plan" && target != "resources" {
		panic(fmt.Sprintf("studioScript: target %q with plan %q", target, plan))
	}
	enc := func(s string) string {
		b, _ := json.Marshal(s)
		return string(b)
	}
	return scriptPrelude + fmt.Sprintf(studioJS, enc(text), enc(target), enc(plan), enc(open))
}

// scriptPrelude is what every script starts with: until waits for what find
// returns, and gives up after twenty seconds saying what it waited for; all
// is the elements that match a selector.
const scriptPrelude = `
const until = async (find, what) => {
  for (let i = 0; i < 200; i++) {
    const v = find();
    if (v) return v;
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error('the page did not show ' + what);
};
const all = (sel) => Array.from(document.querySelectorAll(sel));
`

// studioJS waits for the editor, writes the text, turns the switch, chooses
// the plan from the list and opens the row of the resource.
const studioJS = `
const text = %s, target = %s, plan = %s, open = %s;
const ta = await until(() => document.querySelector('textarea.studio-ta'), 'its editor');
Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(ta, text);
ta.dispatchEvent(new Event('input', { bubbles: true }));
const word = target === 'plan' ? 'Plans' : 'Resources';
const button = await until(() => all('.studio-target button').find((b) => b.textContent.startsWith(word) && !b.disabled), 'the switch ' + word);
button.click();
if (plan) {
  const label = await until(() => all('.studio-results .pk-i .pk-l').find((l) => l.textContent.trim() === plan), 'the plan ' + plan);
  label.parentElement.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }));
}
if (open) {
  const row = await until(() => all('.studio-results .studio-row').find((r) => r.querySelector('b') && r.querySelector('b').textContent === open), 'the row of ' + open);
  if (row.getAttribute('aria-expanded') !== 'true') row.click();
}
if (document.activeElement && document.activeElement.blur) document.activeElement.blur();
return true;
`

// openPlanScript is the script of a shot of the Plans section with the detail
// of a stored plan open under its row, as a click on the row opens it: the
// plan is the one whose note is reason. A fragment lays a plan over the graph
// and does not open its row.
func openPlanScript(reason string) string {
	b, _ := json.Marshal(reason)
	return scriptPrelude + fmt.Sprintf(openPlanJS, b)
}

// openPlanJS waits for the row that carries the note, which the page reads
// after the list, and clicks it unless it is open.
const openPlanJS = `
const reason = %s;
const row = await until(() => all('#plans-pane button.rp').find((r) => { const t = r.querySelector('.rp-title'); return t && t.textContent === reason; }), 'the row of the plan ' + reason);
if (row.getAttribute('aria-expanded') !== 'true') row.click();
if (document.activeElement && document.activeElement.blur) document.activeElement.blur();
return true;
`

// withBound is the rule with its bound changed: the lesson raises the bound of
// a rule in the editor and turns the switch to the resources.
func withBound(text, from, to string) string {
	if strings.Count(text, from) != 1 {
		panic(fmt.Sprintf("withBound: %q is not in the rule once", from))
	}
	return strings.Replace(text, from, to, 1)
}

// askScript is the script of a shot of the assistant. It opens the panel, with
// the button that a person presses. With answer it takes the notice, types the
// question and waits for the answer; with sent it opens what was sent.
func askScript(answer, sent bool) string {
	return scriptPrelude + fmt.Sprintf(askJS, answer, sent, jsString(chatQuestion))
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

const askJS = `
const answer = %t, sent = %t, question = %s;
const btn = await until(() => { const b = document.getElementById('ask-btn'); return b && !b.hidden ? b : null; }, 'the Ask button');
btn.click();
if (!answer) {
  await until(() => document.querySelector('#chat .note'), 'the notice');
} else {
  const on = await until(() => document.querySelector('#chat .note .btn.primary, #chat textarea'), 'the notice or the box');
  if (on.tagName === 'BUTTON') on.click();
  const ta = await until(() => document.querySelector('#chat textarea'), 'the box for the question');
  ta.value = question;
  ta.dispatchEvent(new Event('input', { bubbles: true }));
  await until(() => document.querySelector('#chat .go.on'), 'the send button');
  document.querySelector('#chat .go').click();
  await until(() => document.querySelector('#chat .a'), 'the answer');
  if (sent) {
    const led = await until(() => document.querySelector('#chat .ledger > summary'), 'what was sent');
    led.click();
    const row = await until(() => document.querySelector('#chat .ledger .row > summary'), 'a read');
    row.click();
    const log = document.querySelector('#chat .chat-log');
    log.scrollTop = log.scrollHeight;
  }
}
if (document.activeElement && document.activeElement.blur) document.activeElement.blur();
return true;
`

// wizardScript is the script of a shot of the wizard of the Templates section,
// which the fragment opens: it types the name of the instance and the text
// inputs, presses the buttons of the enum inputs, as a person does, and waits
// for the checks to come back with the first check in the state want, "valid"
// or "invalid". No fragment carries the values of the form.
func wizardScript(label string, text, enums map[string]string, want string) string {
	if want != "valid" && want != "invalid" {
		panic(fmt.Sprintf("wizardScript: want %q", want))
	}
	type kv struct{ K, V string }
	sorted := func(m map[string]string) []kv {
		var out []kv
		for k, v := range m {
			out = append(out, kv{k, v})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].K < out[j].K })
		return out
	}
	var b strings.Builder
	for _, f := range sorted(text) {
		fmt.Fprintf(&b, "type(await until(() => document.getElementById(%s), 'the field %s'), %s);\n", jsString("tpf-"+f.K), f.K, jsString(f.V))
	}
	for _, f := range sorted(enums) {
		fmt.Fprintf(&b, "(await until(() => document.querySelector(%s), 'the button %s')).click();\n", jsString(`.tp-sb[data-fk="`+f.K+"="+f.V+`"]`), f.K+"="+f.V)
	}
	tone := "ok"
	if want == "invalid" {
		tone = "bad"
	}
	return scriptPrelude + fmt.Sprintf(wizardJS, jsString(label), b.String(), jsString(tone))
}

// wizardJS types into a field with the setter of the element, so that the
// page sees an input event as it does from the keyboard, and waits until the
// checks are not busy and the first of them has the tone it wants; a form
// that the specification accepts also waits for the verdict of the policies.
const wizardJS = `
const type = (el, text) => {
  const proto = el.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, text);
  el.dispatchEvent(new Event('input', { bubbles: true }));
};
type(await until(() => document.getElementById('tpf-label'), 'the name of the instance'), %s);
%s
const tone = %s;
await until(() => {
  const c = document.querySelector('.tp-checks');
  if (!c || c.classList.contains('busy')) return null;
  const first = c.querySelector('.tp-check');
  return first && first.classList.contains(tone) && c.querySelectorAll('.tp-check').length >= (tone === 'ok' ? 3 : 1) ? c : null;
}, 'the checks');
if (document.activeElement && document.activeElement.blur) document.activeElement.blur();
return true;
`
