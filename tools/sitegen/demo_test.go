package main

import (
	"strings"
	"testing"
)

const demoEmbed = `<div class="d" id="d"><a href="learn/index.html">Learn</a> <a href="#top">Top</a> <a href="https://example.org/x">X</a>` +
	`<div data-widget="unify"></div><span>{{data demo.name}}</span><script defer src="assets/demos/d.js"></script></div>`

func TestDemoContainerEmbedsTheFile(t *testing.T) {
	s := newTestSite(t, map[string]string{
		"site/demos/d/embed.html": demoEmbed,
		"site/data/demo.json":     `{"name": "unify"}`,
		"learn/deep/page.md":      "---\ntitle: Page\ndescription: A page.\norder: 1\n---\n\nBefore.\n\n:::demo d\n:::\n\nAfter.\n",
	})
	noErrors(t, s.msgs)
	p := s.mustPage(t, "learn/deep/page.md")
	got := string(p.Content)
	mustContain(t, got,
		`<div class="demo" data-demo="d">`,
		`href="../../learn/index.html"`, // the path to the site root is added
		`href="#top"`,                   // a fragment is left alone
		`href="https://example.org/x"`,  // so is an absolute address
		`src="../../assets/demos/d.js"`,
		`<span>unify</span>`, // data tokens are expanded
	)
	if !p.HasWidget {
		t.Error("a demo with a widget does not load widgets.js")
	}
	if tw := string(twinText(p)); strings.Contains(tw, ":::demo") || !strings.Contains(tw, "interactive demo") {
		t.Errorf("the Markdown twin keeps the demo directive:\n%s", tw)
	}
}

func TestDemoContainerReportsProblems(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"missing file": {":::demo nowhere\n:::\n", "site/demos/nowhere/embed.html cannot be read"},
		"bad name":     {":::demo Bad_Name\n:::\n", "needs a name of lowercase letters"},
		"no name":      {":::demo\n:::\n", "needs the demo name"},
	} {
		s := newTestSite(t, map[string]string{
			"learn/page.md": "---\ntitle: Page\ndescription: A page.\norder: 1\n---\n\n" + tc.body,
		})
		if !hasError(s.msgs, tc.want) {
			t.Errorf("%s: errors %v, want one containing %q", name, s.msgs.errs, tc.want)
		}
	}
}

func TestTOCFalseHidesTheTableOfContents(t *testing.T) {
	body := "\n\nText.\n\n## One\n\nx\n\n## Two\n\ny\n"
	s := newTestSite(t, map[string]string{
		"learn/with.md":    "---\ntitle: With\ndescription: A page.\norder: 1\n---" + body,
		"learn/without.md": "---\ntitle: Without\ndescription: A page.\norder: 2\ntoc: false\n---" + body,
	})
	if v := s.docViewFor(s.mustPage(t, "learn/with.md")); len(v.TOC) != 2 {
		t.Errorf("a page without toc: false has %d entries in its table of contents, want 2", len(v.TOC))
	}
	if v := s.docViewFor(s.mustPage(t, "learn/without.md")); len(v.TOC) != 0 {
		t.Errorf("a page with toc: false has %d entries in its table of contents, want 0", len(v.TOC))
	}
	if _, err := parseFrontMatter("page.md", "title: T\ndescription: D\ntoc: maybe\n"); err == nil {
		t.Error("toc: maybe is accepted")
	}
}
