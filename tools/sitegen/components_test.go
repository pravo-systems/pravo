package main

import (
	"strings"
	"testing"
)

func page3(title, order, extra, body string) string {
	return "---\ntitle: " + title + "\ndescription: A page.\norder: " + order + "\n" + extra + "---\n\n" + body
}

func TestMappingMarksNoneAndStatus(t *testing.T) {
	s := newTestSite(t, map[string]string{
		"learn/map.md": page3("Map", "1", "", `:::mapping
| OpenTofu | pravo | Note |
|---|---|---|
| `+"`a`"+` | `+"`b`"+` | Same. |
| `+"`c`"+` | No equivalent | Use d. |
| `+"`e`"+` | None. | |
:::

:::mapping
| Construct | Status | In pravo |
|---|---|---|
| `+"`x`"+` | Supported | A. |
| `+"`y`"+` | Partial | B. |
| `+"`z`"+` | Not supported | C. |
:::
`),
	})
	noErrors(t, s.msgs)
	got := string(s.mustPage(t, "learn/map.md").Content)
	mustContain(t, got,
		`<div class="mapping">`,
		`<div class="mapping mapping-status">`,
		`<td class="map-status" data-v="yes">Supported</td>`,
		`<td class="map-status" data-v="part">Partial</td>`,
		`<td class="map-status" data-v="no">Not supported</td>`,
	)
	if n := strings.Count(got, `<tr class="map-none">`); n != 3 {
		t.Errorf("%d rows are marked map-none, want 3 (two empty replacements and one unsupported construct)", n)
	}
	if strings.Contains(got, `<tr class="map-none">`+"\n"+`<td><code>a</code>`) {
		t.Error("a row that has a replacement is marked map-none")
	}
}

func TestMappingNeedsATable(t *testing.T) {
	s := newTestSite(t, map[string]string{"learn/map.md": page3("Map", "1", "", ":::mapping\nJust text.\n:::\n")})
	if !hasError(s.msgs, ":::mapping holds a table") {
		t.Errorf("errors %v", s.msgs.errs)
	}
}

func TestCardsSplitAtHeadings(t *testing.T) {
	s := newTestSite(t, map[string]string{
		"learn/cards.md": page3("Cards", "1", "", ":::cards\n### First\nText one.\n\n[Go](#first)\n\n### Second\nText two.\n:::\n"),
	})
	noErrors(t, s.msgs)
	got := string(s.mustPage(t, "learn/cards.md").Content)
	mustContain(t, got, `<div class="cards doc-cards">`, `<h3 class="card-title">First</h3>`, `<h3 class="card-title">Second</h3>`, `<p class="card-text">Text one.</p>`)
	if n := strings.Count(got, `<div class="card spot">`); n != 2 {
		t.Errorf("%d cards, want 2", n)
	}
	for name, body := range map[string]string{
		"no heading":  ":::cards\nText only.\n:::\n",
		"text before": ":::cards\nIntro.\n### A\nx\n:::\n",
	} {
		s := newTestSite(t, map[string]string{"learn/cards.md": page3("Cards", "1", "", body)})
		if len(s.msgs.errs) == 0 {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestRunbookMarksTheOpenStep(t *testing.T) {
	group := "group: Runbook\n"
	s := newTestSite(t, map[string]string{
		"learn/m1-first.md":  page3("First", "1", group+"gate: The first gate\n", "Text.\n"),
		"learn/m2-second.md": page3("Second", "2", group+"gate: \"`cmd` succeeds\"\n", "Text.\n"),
		"learn/m3-third.md":  page3("Third", "3", group+"gate: The third gate\n", "Text.\n"),
		"learn/m4-lookup.md": page3("Lookup", "4", group, "Text.\n"),
		"learn/index.md":     "---\ntitle: Learn\ndescription: The lessons.\n---\n\n:::runbook\n:::\n",
	})
	noErrors(t, s.msgs)
	v := s.docViewFor(s.mustPage(t, "learn/m2-second.md"))
	got := string(v.Runbook)
	mustContain(t, got,
		`aria-label="The runbook, step 2 of 3"`,
		`<li class="rb-step rb-past`, `<li class="rb-step rb-here`, `<li class="rb-step rb-ahead`,
		`aria-current="step"`,
		`<span class="rb-g"><span class="sr-only">Gate: </span>The first gate</span>`,
		`<code>cmd</code> succeeds`,
		`href="m1-first.html"`,
		`class="rb-lookup"`, `Lookups`, `href="m4-lookup.html"`,
	)
	if strings.Contains(got, `href="m2-second.html"`) {
		t.Error("the open step links to itself")
	}
	if v := s.docViewFor(s.mustPage(t, "learn/m4-lookup.md")); v.Runbook != "" {
		t.Error("a page without a gate shows the runbook")
	}
	idx := string(s.mustPage(t, "learn/index.md").Content)
	mustContain(t, idx, `aria-label="The runbook, 3 steps"`)
	if strings.Contains(idx, "rb-here") || strings.Contains(idx, "rb-past") {
		t.Error("the index marks a step as open")
	}
}

func TestRunbookNeedsAGate(t *testing.T) {
	s := newTestSite(t, map[string]string{"learn/page.md": page3("Page", "1", "", ":::runbook\n:::\n")})
	if !hasError(s.msgs, ":::runbook finds no page with a gate") {
		t.Errorf("errors %v", s.msgs.errs)
	}
}

func TestStripLayoutKeepsWhatTheContainersHold(t *testing.T) {
	in := "Intro.\n\n:::mapping\n| A | B |\n|---|---|\n| x | y |\n:::\n\n:::runbook\n:::\n\n:::compare\n```cue\n:::mapping\n```\n:::\n"
	got := string(stripLayout([]byte(in)))
	if strings.Contains(got, ":::mapping\n| A") || strings.Contains(got, ":::runbook") {
		t.Errorf("a layout fence is left:\n%s", got)
	}
	mustContain(t, got, "| A | B |", ":::compare", "```cue\n:::mapping\n```")
	if n := strings.Count(got, "\n:::\n"); n != 1 {
		t.Errorf("%d closing fences are left, want the one of :::compare:\n%s", n, got)
	}
}

func TestGateKeyIsKnown(t *testing.T) {
	fm, err := parseFrontMatter("page.md", "title: T\ndescription: D\ngate: The check passes\n")
	if err != nil || fm.Gate != "The check passes" {
		t.Errorf("gate %q, error %v", fm.Gate, err)
	}
}

func TestCauseFixIsSetAsTwoLines(t *testing.T) {
	s := newTestSite(t, map[string]string{
		"learn/p.md": page3("P", "1", "", "Cause: the file is empty. Fix: pull the state again.\n\nCause: only a cause.\n\nCause is a word, not a label.\n"),
	})
	noErrors(t, s.msgs)
	got := string(s.mustPage(t, "learn/p.md").Content)
	mustContain(t, got,
		`<span class="cf-l">Cause</span> The file is empty.</p>`,
		`<span class="cf-l cf-fix">Fix</span> Pull the state again.</p>`,
		`<span class="cf-l">Cause</span> Only a cause.</p>`,
		`<p>Cause is a word, not a label.</p>`,
	)
}
