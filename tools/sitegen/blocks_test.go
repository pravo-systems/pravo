package main

import (
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	body := "```hcl title=\"main.tf\"\na = 1\n```\n\n```cue title=\"main.cue\"\na: 1\n```\n"
	html, msgs := renderBody(t, ":::compare\n"+body+":::\n")
	noErrors(t, msgs)
	mustContain(t, html, `<div class="compare" data-count="2">`, `<span class="code-file">main.tf</span>`, `<span class="code-file">main.cue</span>`)
	if strings.Count(html, `<figure class="code"`) != 2 {
		t.Errorf("want two figures:\n%s", html)
	}

	_, msgs = renderBody(t, ":::compare\n```cue title=\"x\"\na: 1\n```\n:::\n")
	if !hasError(msgs, "needs at least two code blocks") {
		t.Errorf("one block: %v", msgs.errs)
	}
	_, msgs = renderBody(t, ":::compare\n```cue\na: 1\n```\n\n```hcl title=\"b\"\na = 1\n```\n:::\n")
	if !hasError(msgs, "needs a title or a file name") {
		t.Errorf("untitled block: %v", msgs.errs)
	}
}

func TestTabs(t *testing.T) {
	body := ":::tabs\n```sh title=\"macOS\"\nbrew install x\n```\n\n```sh run title=\"Check\"\npravo version\n```\n\n```text out\npravo 0.1\n```\n:::\n"
	html, msgs := renderBody(t, body)
	noErrors(t, msgs)
	mustContain(t, html,
		`<div class="tabs">`,
		`<div class="tab-list" role="tablist" aria-label="macOS, Check">`,
		`<button role="tab" type="button" id="tab-1-1" aria-selected="true" aria-controls="panel-1-1">macOS</button>`,
		`<button role="tab" type="button" id="tab-1-2" aria-selected="false" aria-controls="panel-1-2">Check</button>`,
		`<div class="tab-panel" role="tabpanel" id="panel-1-1" aria-labelledby="tab-1-1" data-title="macOS" data-active>`,
		`<div class="tab-panel" role="tabpanel" id="panel-1-2" aria-labelledby="tab-1-2" data-title="Check">`,
		`<figure class="term"`,
	)
	// The run block and its out block are one tab.
	if strings.Count(html, `role="tabpanel"`) != 2 {
		t.Errorf("want two panels:\n%s", html)
	}

	// Two tab groups on one page get distinct ids.
	html, _ = renderBody(t, body+"\n"+body)
	mustContain(t, html, `id="tab-1-1"`, `id="tab-2-1"`)

	_, msgs = renderBody(t, ":::tabs\n```sh\na\n```\n\n```sh title=\"b\"\nb\n```\n:::\n")
	if !hasError(msgs, "needs a title") {
		t.Errorf("untitled tab: %v", msgs.errs)
	}
	_, msgs = renderBody(t, ":::tabs\n```sh title=\"a\"\na\n```\n\n```sh title=\"a\"\nb\n```\n:::\n")
	if !hasError(msgs, "two tabs are titled") {
		t.Errorf("duplicate tab: %v", msgs.errs)
	}
}

func TestPredict(t *testing.T) {
	html, msgs := renderBody(t, ":::predict What does `a & b` print?\nLook at the two fields.\n\n::reveal\nIt prints `3`.\n:::\n")
	noErrors(t, msgs)
	mustContain(t, html,
		`<div class="predict-group">`,
		`<p>Look at the two fields.</p>`,
		`<details class="predict">`,
		`<summary><span class="predict-label">Predict</span> What does <code>a &amp; b</code> print?</summary>`,
		`<div class="predict-reveal">`,
		`<p>It prints <code>3</code>.</p>`,
	)

	// Without ::reveal the whole body is the answer and there is no group.
	html, msgs = renderBody(t, ":::predict Which value wins?\nNeither. It is a conflict.\n:::\n")
	noErrors(t, msgs)
	mustNotContain(t, html, "predict-group")
	mustContain(t, html, `<div class="predict-reveal">`, "Neither. It is a conflict.")

	_, msgs = renderBody(t, ":::predict\nanswer\n:::\n")
	if !hasError(msgs, "needs the question") {
		t.Errorf("no question: %v", msgs.errs)
	}
}

func TestExercise(t *testing.T) {
	body := ":::exercise Change the bound\nMake `size` accept 1 to 20.\n\n::hint\nOnly the range changes.\n\n::hint\nSee the glossary.\n\n::solution\n```cue file=main.cue\nsize: <=20\n```\n\n```sh run check\ntest 1 = 1\n```\n:::\n"
	html, msgs := renderBody(t, body)
	noErrors(t, msgs)
	mustContain(t, html,
		`<section class="exercise">`,
		`<header><span class="exercise-label">Exercise</span><h3 id="exercise-change-the-bound"><a class="anchor" href="#exercise-change-the-bound">Change the bound</a></h3></header>`,
		`<div class="exercise-body">`,
		`<details class="hint" data-tier="1">`, `<summary>Hint 1</summary>`,
		`<details class="hint" data-tier="2">`, `<summary>Hint 2</summary>`,
		`<details class="solution">`, `<summary>Solution</summary>`,
		`<span class="code-file">main.cue</span>`,
	)
	// The check block verifies the solution and is not shown.
	mustNotContain(t, html, "test 1 = 1", `class="term"`)

	// A single hint is labelled plainly.
	html, _ = renderBody(t, ":::exercise One\nTask.\n\n::hint\nA nudge.\n:::\n")
	mustContain(t, html, `<summary>Hint</summary>`)
	mustNotContain(t, html, "solution")

	_, msgs = renderBody(t, ":::exercise Three\nTask.\n\n::hint\na\n\n::hint\nb\n\n::hint\nc\n:::\n")
	if !hasError(msgs, "at most two hints") {
		t.Errorf("three hints: %v", msgs.errs)
	}
	_, msgs = renderBody(t, ":::exercise\nTask.\n:::\n")
	if !hasError(msgs, "needs a title") {
		t.Errorf("no title: %v", msgs.errs)
	}
}

func TestCheck(t *testing.T) {
	html, msgs := renderBody(t, ":::check\nWhat is `&`?\n::answer\nUnification.\n\nWhy two lines?\n::answer\nBecause **both** apply.\n\nSecond paragraph.\n:::\n")
	noErrors(t, msgs)
	mustContain(t, html,
		`<section class="check">`, `<h3 id="check-yourself"><a class="anchor" href="#check-yourself">Check yourself</a></h3>`,
		`<summary>What is <code>&amp;</code>?</summary>`, `<p>Unification.</p>`,
		`<summary>Why two lines?</summary>`, `<strong>both</strong>`, `<p>Second paragraph.</p>`,
	)
	if strings.Count(html, "<details>") != 2 {
		t.Errorf("want two questions:\n%s", html)
	}

	_, msgs = renderBody(t, ":::check\nNot a question\n::answer\nx\n:::\n")
	if !hasError(msgs, "ends in ?") {
		t.Errorf("question without ?: %v", msgs.errs)
	}
	_, msgs = renderBody(t, ":::check\nQ1?\n::answer\nA1.\n\nstatement\n::answer\nA2.\n:::\n")
	if !hasError(msgs, "ends in ?") {
		t.Errorf("second question without ?: %v", msgs.errs)
	}
	_, msgs = renderBody(t, ":::check\nQ?\n:::\n")
	if !hasError(msgs, "needs questions") {
		t.Errorf("no answer: %v", msgs.errs)
	}
}

func TestDeepDiveAndFigures(t *testing.T) {
	html, msgs := renderBody(t, ":::deep-dive Why `&` commutes\nBecause it is a meet.\n:::\n\n:::figure First. See `a`.\n![alt text](x.svg)\n:::\n\n:::figure Second.\n<svg viewBox=\"0 0 1 1\"></svg>\n:::\n")
	noErrors(t, msgs)
	mustContain(t, html,
		`<details class="deep-dive">`, `<summary>Why <code>&amp;</code> commutes</summary>`,
		`<figure class="fig" id="fig-1">`, `<span class="fig-n">fig. 1</span> First. See <code>a</code>.`,
		`<figure class="fig" id="fig-2">`, `<span class="fig-n">fig. 2</span> Second.`,
		`<div class="fig-body">`, `<svg viewBox="0 0 1 1"></svg>`,
	)
	_, msgs = renderBody(t, ":::deep-dive\nbody\n:::\n")
	if !hasError(msgs, "needs a title") {
		t.Errorf("deep-dive without title: %v", msgs.errs)
	}
	_, msgs = renderBody(t, ":::figure\nbody\n:::\n")
	if !hasError(msgs, "needs a caption") {
		t.Errorf("figure without caption: %v", msgs.errs)
	}
}

func TestWidget(t *testing.T) {
	html, msgs := renderBody(t, ":::widget unify\n{\"a\": \"<b> & </script>\", \"fallback\": \"Plain words.\"}\n:::\n")
	noErrors(t, msgs)
	mustContain(t, html, `<div class="widget" data-widget="unify">`, `<script type="application/json">`, `<noscript><p>Plain words.</p></noscript>`)
	mustNotContain(t, html, "</script> ", `"<b>`)
	if strings.Count(html, "</script>") != 1 {
		t.Errorf("the JSON must not close the script element:\n%s", html)
	}
	esc := func(code string) string { return "\\u" + code }
	mustContain(t, html, esc("003c")+"b"+esc("003e")+" "+esc("0026")+" "+esc("003c")+"/script"+esc("003e"))

	// A fenced JSON body with a ::fallback part.
	html, msgs = renderBody(t, ":::widget stepper\n```json\n{\"n\": 3}\n```\n\n::fallback\nThree steps: **a**, b, c.\n:::\n")
	noErrors(t, msgs)
	mustContain(t, html, `{"n":3}`, `<noscript><p>Three steps: <strong>a</strong>, b, c.</p></noscript>`)

	// A page with a widget needs widgets.js; the page struct records that.
	s := newTestSite(t, map[string]string{"learn/a01-lesson.md": lessonSource(":::widget w\n{\"fallback\": \"x\"}\n:::\n"), "learn/a02-other.md": strings.Replace(lessonSource("text\n"), "order: 1", "order: 2", 1)})
	if !s.mustPage(t, "learn/a01-lesson.md").HasWidget || s.mustPage(t, "learn/a02-other.md").HasWidget {
		t.Errorf("HasWidget is wrong")
	}

	_, msgs = renderBody(t, ":::widget w\n{not json}\n:::\n")
	if !hasError(msgs, "not valid JSON") {
		t.Errorf("invalid JSON: %v", msgs.errs)
	}
	_, msgs = renderBody(t, ":::widget\n{}\n:::\n")
	if !hasError(msgs, "needs a name") {
		t.Errorf("no name: %v", msgs.errs)
	}
	_, msgs = renderBody(t, ":::widget w\n{}\n:::\n")
	noErrors(t, msgs)
	if len(msgs.warns) == 0 || !strings.Contains(msgs.warns[0], "no fallback") {
		t.Errorf("want a fallback warning: %v", msgs.warns)
	}
}

func TestCallouts(t *testing.T) {
	for _, typ := range []string{"NOTE", "TIP", "WARNING", "PITFALL", "CHECK"} {
		html, msgs := renderBody(t, "> [!"+typ+"]\n> Text with `code`.\n>\n> Second paragraph.\n\nAfter.\n")
		noErrors(t, msgs)
		title := strings.ToUpper(typ[:1]) + strings.ToLower(typ[1:])
		mustContain(t, html,
			`<div class="callout callout-`+strings.ToLower(typ)+`" role="note">`,
			`<p class="callout-title">`+title+`</p>`,
			`<p>Text with <code>code</code>.</p>`, `<p>Second paragraph.</p>`, `<p>After.</p>`)
	}
	// Lower case works, other alert types stay blockquotes.
	html, _ := renderBody(t, "> [!note]\n> a\n")
	mustContain(t, html, "callout-note")
	html, _ = renderBody(t, "> [!IMPORTANT]\n> a\n")
	mustContain(t, html, "<blockquote>")
	mustNotContain(t, html, "callout")
	// A code block inside a callout.
	html, msgs := renderBody(t, "> [!TIP]\n> ```cue\n> a: 1\n> ```\n")
	noErrors(t, msgs)
	mustContain(t, html, `callout-tip`, `<figure class="code" data-lang="cue">`)
}

func TestContainersRespectFences(t *testing.T) {
	// A container line inside a fence is code, not a directive.
	body := "```text\n:::compare\n::hint\n:::\n```\n\nAfter the fence.\n"
	html, msgs := renderBody(t, body)
	noErrors(t, msgs)
	mustContain(t, html, ":::compare", "After the fence.")
	mustNotContain(t, html, `class="compare"`)

	// Inside a container, a fenced line `:::` does not close it.
	html, msgs = renderBody(t, ":::deep-dive Syntax\n````text\n:::\n````\n\nStill inside.\n:::\n")
	noErrors(t, msgs)
	mustContain(t, html, "Still inside.")
	if strings.Count(html, "<details") != 1 {
		t.Errorf("the container should be one element:\n%s", html)
	}
}

func TestContainerErrors(t *testing.T) {
	cases := map[string]string{
		":::nonesuch x\nbody\n:::\n":                                   "unknown container",
		":::deep-dive T\nbody\n":                                       "is never closed",
		"text\n:::\n":                                                  "closes no container",
		":::deep-dive T\n:::compare\n:::\n:::\n":                       "do not nest",
		":::exercise T\n::reveal\n:::\n":                               "is not a part of :::exercise",
		":::deep-dive T\n::hint\nx\n:::\n":                             "is not a part of :::deep-dive",
		":::exercise T\n```cue file=a.cue unknown=1\na: 1\n```\n:::\n": "unknown code attribute",
		"```sh out\nonly output\n```\n":                                "must directly follow the run block",
		"## Heading\n\n# Title\n":                                      "level-1 heading",
		"[x](missing.md)\n":                                            "no page learn/missing.md",
		"```cue hl=\"9\"\na: 1\n```\n":                                 "past the end",
	}
	for body, want := range cases {
		_, msgs := renderBody(t, body)
		if !hasError(msgs, want) {
			t.Errorf("%q: errors %v, want %q", body, msgs.errs, want)
		}
	}
}

func TestErrorLinesPointAtTheFile(t *testing.T) {
	// Front matter is 5 lines, the blank line is 6, the intro is 7, the container starts on line 9.
	s := newTestSite(t, map[string]string{"learn/a01-lesson.md": lessonSource("Intro line.\n\n:::deep-dive\nbody\n:::\n")})
	found := false
	for _, e := range s.msgs.errs {
		if strings.Contains(e, "a01-lesson.md:9:") && strings.Contains(e, "needs a title") {
			found = true
		}
	}
	if !found {
		t.Errorf("want the error on line 9: %v", s.msgs.errs)
	}
	// A code block error inside a container also has the right line.
	s = newTestSite(t, map[string]string{"learn/a01-lesson.md": lessonSource(":::deep-dive T\nText.\n\n```cue bogus=1\na: 1\n```\n:::\n")})
	found = false
	for _, e := range s.msgs.errs {
		if strings.Contains(e, "a01-lesson.md:10:") && strings.Contains(e, "unknown code attribute") {
			found = true
		}
	}
	if !found {
		t.Errorf("want the code error on line 10: %v", s.msgs.errs)
	}
}

func TestRunOutPairing(t *testing.T) {
	body := "```sh run\npravo version\n```\n\n```text out\npravo 0.1\n```\n\n```sh run hidden\nmkdir x\n```\n\n```text out\nsomething\n```\n\nText.\n\n```sh run\npravo eval\n```\n\n```cue\na: 1\n```\n"
	html, msgs := renderBody(t, body)
	noErrors(t, msgs)
	if strings.Count(html, `<figure class="term"`) != 2 {
		t.Errorf("want two terminals (the hidden one and its output vanish):\n%s", html)
	}
	mustContain(t, html, `<pre class="term-out"><code>pravo 0.1</code></pre>`)
	mustNotContain(t, html, "mkdir", "something")
	// The second run has no out block: its terminal has no output element, and the cue block stays separate.
	if strings.Count(html, "term-out") != 1 || strings.Count(html, `<figure class="code"`) != 1 {
		t.Errorf("pairing is wrong:\n%s", html)
	}
}

func TestHeadingsAndTOC(t *testing.T) {
	s := newTestSite(t, map[string]string{"learn/a01-lesson.md": lessonSource("## One\n\ntext\n\n### Sub `code`\n\n#### Deeper\n\n## Two\n")})
	noErrors(t, s.msgs)
	p := s.mustPage(t, "learn/a01-lesson.md")
	if len(p.TOC) != 3 || p.TOC[0].ID != "one" || p.TOC[1].Level != 3 || p.TOC[1].Text != "Sub code" || p.TOC[2].ID != "two" {
		t.Errorf("TOC: %+v", p.TOC)
	}
	mustContain(t, string(p.Content),
		`<h2 id="one"><a class="anchor" href="#one">One</a></h2>`,
		`<h3 id="sub-code"><a class="anchor" href="#sub-code">Sub <code>code</code></a></h3>`)
}

// A heading that holds a link cannot be wrapped in a second link: its
// permalink follows the text.
func TestHeadingWithLinkKeepsSidePermalink(t *testing.T) {
	s := newTestSite(t, map[string]string{
		"learn/a01-lesson.md": lessonSource("## See [the other page](a02-other.md)\n\ntext\n"),
		"learn/a02-other.md":  "---\ntitle: Other\ndescription: Other.\norder: 2\n---\n\n## Top\n",
	})
	noErrors(t, s.msgs)
	p := s.mustPage(t, "learn/a01-lesson.md")
	mustContain(t, string(p.Content), `<a class="anchor-side" href="#see-`, `" aria-label="Link to this section">#</a></h2>`)
	mustNotContain(t, string(p.Content), `<a class="anchor"`)
}

// Two checks on a page get two headings with two ids.
func TestCheckTitleAndUniqueIDs(t *testing.T) {
	body := ":::check Warm-up\nOne?\n::answer\nYes.\n:::\n\n:::check\nTwo?\n::answer\nYes.\n:::\n\n:::check\nThree?\n::answer\nYes.\n:::\n"
	html, msgs := renderBody(t, body)
	noErrors(t, msgs)
	mustContain(t, html, `<h3 id="check-warm-up"><a class="anchor" href="#check-warm-up">Warm-up</a></h3>`, `<h3 id="check-yourself"><a class="anchor" href="#check-yourself">Check yourself</a></h3>`, `<h3 id="check-yourself-2"><a class="anchor" href="#check-yourself-2">Check yourself</a></h3>`)
}

// A tab list is named by the container argument, else by its tabs.
func TestTabListName(t *testing.T) {
	body := ":::tabs Environments\n```sh title=\"dev\"\na\n```\n\n```sh title=\"prod\"\nb\n```\n:::\n"
	html, msgs := renderBody(t, body)
	noErrors(t, msgs)
	mustContain(t, html, `role="tablist" aria-label="Environments"`)
}

func TestLinksAndGlossary(t *testing.T) {
	s := newTestSite(t, map[string]string{
		"learn/a01-lesson.md":   lessonSource("See [the section](./) and [the glossary](../reference/glossary.md#unification) and [other](a02-other.md#top) and [site](https://example.com/x.md) and [here](#local).\n"),
		"learn/a02-other.md":    "---\ntitle: Other\ndescription: Other.\norder: 2\n---\n\n## Top\n",
		"reference/glossary.md": "---\ntitle: Glossary\ndescription: Terms.\norder: 1\n---\n\n## unification\n\nCombining **two** values into one. A second sentence.\n\n## constraint\n\nA restriction.\n",
		"reference/moved.md":    "---\nredirect: glossary.md\n---\n",
		"reference/via.md":      "---\ntitle: Via\ndescription: Via.\norder: 3\n---\n\n[old](moved.md#constraint)\n",
	})
	noErrors(t, s.msgs)
	html := string(s.mustPage(t, "learn/a01-lesson.md").Content)
	mustContain(t, html,
		`<a href="../reference/glossary.html#unification" class="term-link" data-term="unification" data-def="Combining two values into one. A second sentence.">the glossary</a>`,
		`<a href="a02-other.html#top">other</a>`,
		`<a href="https://example.com/x.md">site</a>`,
		`<a href="#local">here</a>`,
	)
	// A directory link becomes a link to the index page.
	mustContain(t, html, `href="index.html"`)
	// A link to a redirect stub goes straight to the target.
	mustContain(t, string(s.mustPage(t, "reference/via.md").Content), `<a href="glossary.html#constraint">old</a>`)
	if s.gloss["constraint"].Def != "A restriction." {
		t.Errorf("glossary: %+v", s.gloss)
	}
}

func TestCheckQuestionWithPremise(t *testing.T) {
	html, msgs := renderBody(t, ":::check\n::question\nA file reads:\n\n```cue\na: 1\n```\n\nIs it accepted?\n::answer\nYes.\n\nAnd this?\n::answer\nAlso.\n\n::question\nLast one?\n::answer\nIt is.\n:::\n")
	noErrors(t, msgs)
	mustContain(t, html,
		`<div class="check-item">`, `<div class="check-premise">`, `<p>A file reads:</p>`, `<pre`,
		`<summary>Is it accepted?</summary>`, `<p>Yes.</p>`,
		`<summary>And this?</summary>`, `<p>Also.</p>`,
		`<summary>Last one?</summary>`, `<p>It is.</p>`,
	)
	if strings.Count(html, "<details>") != 3 || strings.Count(html, "check-item") != 1 {
		t.Errorf("want three questions and one premise:\n%s", html)
	}

	_, msgs = renderBody(t, ":::check\n::question\nA premise.\n\nNo question mark\n::answer\nx\n:::\n")
	if !hasError(msgs, "ends in ?") {
		t.Errorf("premise question without ?: %v", msgs.errs)
	}
	_, msgs = renderBody(t, ":::check\nQ?\n::question\nR?\n::answer\nx\n:::\n")
	if !hasError(msgs, "no ::answer") {
		t.Errorf("question upon question: %v", msgs.errs)
	}
	_, msgs = renderBody(t, ":::check\n::answer\nx\n:::\n")
	if !hasError(msgs, "follows no question") {
		t.Errorf("answer without a question: %v", msgs.errs)
	}
}
