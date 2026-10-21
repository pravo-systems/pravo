package main

import (
	"strings"
	"testing"
)

func TestCheckOutput(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a/one.html": `<html><body><h2 id="top">T</h2><a href="two.html#sec">ok</a><a href="two.html#nope">bad anchor</a>
<a href="missing.html">missing</a><a href="#top">self</a><a href="#gone">self bad</a><a href="https://example.com/x">ext</a>
<a href="mailto:a@b.c">mail</a><img src="../assets/x.svg"><img src="../assets/none.svg"><a href="../b/">dir</a><a href="/root.html">root relative</a>
<a href="../../outside.html">outside</a><a href="two%20x.html">encoded</a><a href="two.html#:~:text=hi">text fragment</a>
<button data-page="a/two.html">done</button><button data-page="a/none.html">gone</button></body></html>`,
		"a/two.html":    `<html><body><h2 id="sec">S</h2><p id="dup">1</p><p id="dup">2</p></body></html>`,
		"a/two x.html":  `<html><body></body></html>`,
		"b/index.html":  `<html><body></body></html>`,
		"assets/x.svg":  `<svg/>`,
		"r/stub.html":   `<html><head><meta http-equiv="refresh" content="0; url=../a/two.html#sec"><link rel="canonical" href="../a/two.html"></head></html>`,
		"r/broken.html": `<html><head><meta http-equiv="refresh" content="0; url=../a/gone.html"></head></html>`,
		"search-index.json": `[{"title":"One","section":"A","url":"a/one.html","description":"","headings":[{"text":"T","id":"top"},{"text":"X","id":"absent"}],"text":""},
{"title":"Gone","section":"A","url":"a/gone.html","description":"","headings":[],"text":""}]`,
	})
	problems, pages := checkOutput(dir)
	if pages != 6 {
		t.Errorf("pages: %d", pages)
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{
		`a/one.html: link "two.html#nope": no element with id "nope"`,
		`a/one.html: link "missing.html": no such file`,
		`a/one.html: link "#gone": no element with id "gone"`,
		`a/one.html: link "../assets/none.svg": no such file`,
		`a/one.html: link "../../outside.html" leaves the site`,
		`a/one.html: data-page "a/none.html": no such page`,
		`a/one.html: link "/root.html" is root-relative`,
		`a/two.html: id "dup" is used more than once`,
		`r/broken.html: link "../a/gone.html": no such file`,
		`search-index.json: url "a/gone.html": no such file`,
		`search-index.json: a/one.html: heading id "absent" is not on the page`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing problem %q in:\n%s", want, joined)
		}
	}
	if len(problems) != 11 {
		t.Errorf("want 11 problems, got %d:\n%s", len(problems), joined)
	}
}

func TestExternalRef(t *testing.T) {
	for ref, want := range map[string]bool{
		"https://a.b/c": true, "mailto:x@y": true, "//cdn.example/x": true, "data:text/plain,hi": true, "javascript:void(0)": true,
		"a.html": false, "../x/y.html#z": false, "#top": false, "": false,
	} {
		if externalRef(ref) != want {
			t.Errorf("externalRef(%q) = %v", ref, !want)
		}
	}
}
