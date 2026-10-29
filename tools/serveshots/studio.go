package main

import (
	"encoding/json"
	"fmt"
	"path"
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
