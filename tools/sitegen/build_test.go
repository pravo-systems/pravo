package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

// buildTestdata builds the kitchen-sink site into a temporary directory.
func buildTestdata(t *testing.T) string {
	t.Helper()
	out := t.TempDir()
	o := options{
		docs: "testdata/docs", site: "testdata/site", out: out,
		readme: filepath.Join(t.TempDir(), "none.md"), lint: true, check: true,
	}
	if err := run(o); err != nil {
		t.Fatalf("build failed: %v", err)
	}
	return out
}

func readOut(t *testing.T, out, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestBuildTestdata(t *testing.T) {
	out := buildTestdata(t)

	for _, f := range []string{
		"index.html", "contents.html", "404.html", "search-index.json", "search-index.js", "llms.txt", "llms-full.txt",
		"assets/style.css", "assets/logo.svg",
		"start/index.html", "start/first-plan.html", "learn/a03-writing-a-field-twice.html", "learn/a03-writing-a-field-twice.md",
		"reference/eval.html", "reference/glossary.md",
	} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(f))); err != nil {
			t.Errorf("missing output %s", f)
		}
	}

	page := readOut(t, out, "learn/a03-writing-a-field-twice.html")
	mustContain(t, page,
		`<html lang="en" class="no-js">`,
		`<body class="page page-doc" data-section="learn" data-root="../">`,
		`<header class="site-header">`,
		`<a class="brand" href="../index.html">`,
		`<nav class="site-nav" aria-label="Documentation sections">`,
		`<a href="index.html" aria-current="true">Learn</a>`,
		`<button class="search-open" type="button">Search <kbd>/</kbd></button>`,
		`<button id="theme-toggle" type="button"`,
		`<a class="btn btn-primary" href="../start/index.html">Start</a>`,
		`<div class="doc-shell">`,
		`<div class="doc-sidebar"`,
		`<p class="side-group">Track A, CUE from zero</p>`,
		`<p class="side-group">Track B, pravo</p>`,
		`<main class="doc-main"`,
		`<nav class="breadcrumb"`,
		`<article class="doc">`,
		`<header class="doc-head">`,
		`<p class="kicker">§ A3 · Track A</p>`,
		`<h1>Writing a field twice</h1>`,
		`<p class="lede">`,
		`<dl class="lesson-meta">`,
		`<footer class="doc-foot">`,
		`<nav class="pager"`, `rel="prev"`, `rel="next"`,
		`<button class="mark-done" type="button" data-page="learn/a03-writing-a-field-twice.html">Mark as done</button>`,
		`<p class="applies">pravo 0.1 · CUE 0.17</p>`,
		`<div class="doc-toc">`, `<details class="toc-inline">`, `<p class="toc-title">On this page</p>`, `<li class="toc-l2"><a href="#constraints-that-agree">`,
		`<footer class="site-footer">`, `<nav class="foot-nav" aria-label="Footer">`, `<span class="applies">pravo 0.1 · CUE 0.17</span> · pravo is an independent project`,
		`<span class="pager-kicker">§ A2</span><span class="pager-title">Fields and structs</span><span class="pager-desc">A struct groups fields.`,
		`<script src="../assets/site.js" defer></script>`,
		`<script src="../assets/widgets.js" defer></script>`,
		`<link rel="alternate" type="text/markdown" href="a03-writing-a-field-twice.md">`,
		`localStorage.getItem("pravo-theme")`,
	)
	// The page with a widget loads widgets.js; the page without one does not.
	mustNotContain(t, readOut(t, out, "learn/a01-a-value-is-a-type.html"), "widgets.js")
	// Pages with no progress tracking have no mark-done button.
	mustNotContain(t, readOut(t, out, "guides/remote-state.html"), "mark-done")
	mustNotContain(t, readOut(t, out, "learn/index.html"), "mark-done")
	// The Start section is not in the nav but is the button.
	mustNotContain(t, page, `>Start</a>
<a href="../migrate`)

	// The landing page is assembled from the partials in file name order.
	index := readOut(t, out, "index.html")
	mustContain(t, index,
		`<body class="page page-landing"`,
		`<section class="band" id="hero" data-band="hero">`,
		`<a class="btn btn-primary" href="start/index.html">Start</a> <a class="btn" href="migrate/index.html">`,
		`<p class="applies">pravo 0.1 · CUE 0.17</p>`,
		`<main class="landing-main"`,
		`<header class="site-header">`,
	)
	if strings.Index(index, `data-band="hero"`) > strings.Index(index, `data-band="sections"`) {
		t.Errorf("partials are out of order")
	}
	mustNotContain(t, index, "{{start}}", "{{applies}}")

	// Redirect stub.
	stub := readOut(t, out, "reference/eval.html")
	mustContain(t, stub, `<meta http-equiv="refresh" content="0; url=command-eval.html">`, `<a href="command-eval.html">`)
	if _, err := os.Stat(filepath.Join(out, "reference", "eval.md")); err == nil {
		t.Errorf("a redirect stub has no twin")
	}

	// Terminal merged from a run block and its out block.
	mustContain(t, page, `<figure class="term" data-exit="0" data-ran="0.1">`)

	// Section images and the contents page.
	contents := readOut(t, out, "contents.html")
	mustContain(t, contents, `<a href="learn/a03-writing-a-field-twice.html">Writing a field twice</a>`, `<h3>Track A, CUE from zero</h3>`)
	mustNotContain(t, contents, `href="reference/eval.html"`, "Moved")

	// Redirected pages stay out of the index, the sitemap and the llms files.
	for _, f := range []string{"search-index.json", "llms.txt", "llms-full.txt", "contents.html"} {
		mustNotContain(t, readOut(t, out, f), "reference/eval.")
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	a, b := buildTestdata(t), buildTestdata(t)
	read := func(root string) map[string]string {
		m := map[string]string{}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(root, p)
				data, _ := os.ReadFile(p)
				m[rel] = string(data)
			}
			return nil
		})
		return m
	}
	ma, mb := read(a), read(b)
	if len(ma) != len(mb) {
		t.Fatalf("file counts differ: %d and %d", len(ma), len(mb))
	}
	for name, content := range ma {
		if mb[name] != content {
			t.Errorf("%s differs between two builds", name)
		}
	}
}

// TestLinksAreRelative guards the requirement that the site works from the
// file system and from a sub-path: no root-relative or absolute internal URL.
func TestLinksAreRelative(t *testing.T) {
	out := buildTestdata(t)
	var offenders []string
	_ = filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		data, _ := os.ReadFile(p)
		info := scanHTML(data)
		rel, _ := filepath.Rel(out, p)
		for _, l := range append(info.links, info.refresh...) {
			// 404.html is served for an address at any depth, so it alone uses paths from the root
			if rel == "404.html" {
				continue
			}
			if strings.HasPrefix(l, "/") && !strings.HasPrefix(l, "//") {
				offenders = append(offenders, rel+": "+l)
			}
		}
		return nil
	})
	if len(offenders) > 0 {
		t.Errorf("root-relative URLs:\n%s", strings.Join(offenders, "\n"))
	}
}

// TestPagesWorkWithoutScript checks that nothing in the generated pages is
// hidden until a script runs: no hidden attribute, no inline display:none.
func TestPagesWorkWithoutScript(t *testing.T) {
	out := buildTestdata(t)
	_ = filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		data, _ := os.ReadFile(p)
		z := xhtml.NewTokenizer(strings.NewReader(string(data)))
		for tt := z.Next(); tt != xhtml.ErrorToken; tt = z.Next() {
			if tt != xhtml.StartTagToken && tt != xhtml.SelfClosingTagToken {
				continue
			}
			tok := z.Token()
			for _, a := range tok.Attr {
				if a.Key == "hidden" || (a.Key == "style" && strings.Contains(strings.ReplaceAll(a.Val, " ", ""), "display:none")) {
					t.Errorf("%s: <%s> is hidden without a script", p, tok.Data)
				}
			}
		}
		// Every tab panel is in the HTML.
		s := string(data)
		if strings.Count(s, `role="tab"`) != strings.Count(s, `role="tabpanel"`) {
			t.Errorf("%s: tabs and panels differ in number", p)
		}
		return nil
	})
}

func TestSearchIndex(t *testing.T) {
	out := buildTestdata(t)
	var entries []searchEntry
	if err := json.Unmarshal([]byte(readOut(t, out, "search-index.json")), &entries); err != nil {
		t.Fatal(err)
	}
	var a3 *searchEntry
	urls := map[string]bool{}
	for i, e := range entries {
		urls[e.URL] = true
		if e.URL == "learn/a03-writing-a-field-twice.html" {
			a3 = &entries[i]
		}
	}
	if len(entries) != 18 || urls["reference/eval.html"] {
		t.Errorf("want 18 entries and no redirect stub, got %d", len(entries))
	}
	if a3 == nil {
		t.Fatal("no entry for A3")
	}
	if a3.Title != "Writing a field twice" || a3.Section != "Learn" || a3.Group != "Track A, CUE from zero" || a3.Lesson != "A3" || a3.Minutes != 10 {
		t.Errorf("entry %+v", *a3)
	}
	var ids []string
	for _, h := range a3.Headings {
		ids = append(ids, h.ID)
	}
	if strings.Join(ids, ",") != "constraints-that-agree,constraints-that-conflict,three-ways-to-bound-a-value,exercise,recap" {
		t.Errorf("headings %v", ids)
	}
	// The excerpt is prose: no code, no hidden answers.
	mustContain(t, a3.Body, "In HCL, a second assignment replaces the first.", "This is called unification.")
	mustNotContain(t, a3.Body, "package main", "It prints an error", "Only the range changes", "#Server", "conflicting values 4 and 3")
	if len(a3.Body) > 1300 {
		t.Errorf("excerpt is %d bytes", len(a3.Body))
	}
	js := readOut(t, out, "search-index.js")
	if !strings.HasPrefix(js, "window.PRAVO_SEARCH=[") || !strings.HasSuffix(strings.TrimSpace(js), "];") {
		t.Errorf("search-index.js: %.60s", js)
	}
}

func TestSitemapAndLLMS(t *testing.T) {
	out := buildTestdata(t)
	// A sitemap lists absolute URLs: without a base_url there is none.
	for _, f := range []string{"sitemap.xml", "robots.txt"} {
		if _, err := os.Stat(filepath.Join(out, f)); err == nil {
			t.Errorf("%s is written without a base_url", f)
		}
	}
	llms := readOut(t, out, "llms.txt")
	mustContain(t, llms, "# pravo\n", "> pravo specifies infrastructure in CUE", "## Learn", "- [Writing a field twice](learn/a03-writing-a-field-twice.md): Two values", "llms-full.txt")
	full := readOut(t, out, "llms-full.txt")
	mustContain(t, full, "# Learn: Writing a field twice", "URL: learn/a03-writing-a-field-twice.html", ":::predict What does pravo eval print")
	mustNotContain(t, full, "\ntitle: Writing a field twice\n")
	twin := readOut(t, out, "learn/a03-writing-a-field-twice.md")
	if !strings.HasPrefix(twin, "# Writing a field twice\n\nTwo values for one field") || strings.Contains(twin, "order: 3") {
		t.Errorf("twin: %.200s", twin)
	}
	mustContain(t, twin, "```cue file=main.cue ln hl=\"3-5\"")
}

func TestBaseURL(t *testing.T) {
	out := t.TempDir()
	o := options{docs: "testdata/docs", site: "testdata/site", out: out, readme: filepath.Join(out, "none"), lint: true, check: true, baseURL: "https://docs.example.org/pravo/"}
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	sm := readOut(t, out, "sitemap.xml")
	mustContain(t, sm, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`, `<loc>https://docs.example.org/pravo/learn/a03-writing-a-field-twice.html</loc>`, `<loc>https://docs.example.org/pravo/</loc>`)
	if strings.Count(sm, "<loc>") != 20 {
		t.Errorf("sitemap has %d locations", strings.Count(sm, "<loc>"))
	}
	mustContain(t, readOut(t, out, "robots.txt"), "Sitemap: https://docs.example.org/pravo/sitemap.xml")
	mustContain(t, readOut(t, out, "learn/index.html"), `<meta property="og:url" content="https://docs.example.org/pravo/learn/">`, `<meta name="twitter:card" content="summary">`)
	// 404.html is served at any depth: every path in it starts at the site path.
	nf := readOut(t, out, "404.html")
	mustContain(t, nf, `<meta name="robots" content="noindex">`, `<link rel="stylesheet" href="/pravo/assets/style.css">`, `href="/pravo/learn/"`, `data-root="/pravo/"`, `<h1>Page not found</h1>`)
	mustNotContain(t, nf, `rel="canonical"`)
	mustContain(t, readOut(t, out, "learn/index.html"), `<link rel="canonical" href="https://docs.example.org/pravo/learn/">`)
	mustContain(t, readOut(t, out, "reference/eval.html"), `<link rel="canonical" href="https://docs.example.org/pravo/reference/command-eval.html">`)
	mustContain(t, readOut(t, out, "llms.txt"), "(https://docs.example.org/pravo/learn/a03-writing-a-field-twice.md)")
}

func TestRepoLinkOnlyWhenSet(t *testing.T) {
	out := buildTestdata(t)
	mustNotContain(t, readOut(t, out, "index.html"), "repo-link", "Repository")

	dir := t.TempDir()
	cfg := strings.Replace(readFile(t, "testdata/site/site.json"), `"repo": ""`, `"repo": "https://github.com/example/pravo"`, 1)
	copyTreeT(t, "testdata/site", filepath.Join(dir, "site"))
	writeTree(t, dir, map[string]string{"site/site.json": cfg})
	out2 := filepath.Join(dir, "out")
	o := options{docs: "testdata/docs", site: filepath.Join(dir, "site"), out: out2, readme: filepath.Join(dir, "none"), lint: true, check: true}
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	mustContain(t, readOut(t, out2, "learn/index.html"), `<a class="repo-link" href="https://github.com/example/pravo" rel="noopener">GitHub</a>`)
	mustContain(t, readOut(t, out2, "index.html"), `<li><a href="https://github.com/example/pravo" rel="noopener">Repository</a></li>`)
	mustContain(t, readOut(t, out2, "index.html"), `<span class="applies">pravo 0.1 · CUE 0.17</span>`)
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func copyTreeT(t *testing.T, src, dst string) {
	t.Helper()
	if err := copyTree(src, dst, nil); err != nil {
		t.Fatal(err)
	}
}

func TestLandingFallbacks(t *testing.T) {
	// No partials, no index.html: a generated landing page.
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"site/site.json":          testSiteJSON,
		"site/assets/logo.svg":    "<svg/>",
		"site/assets/style.css":   "",
		"site/assets/site.js":     "",
		"docs/learn/index.md":     indexMD,
		"docs/reference/index.md": strings.Replace(indexMD, "Learn", "Reference", 1),
	})
	out := filepath.Join(dir, "out")
	o := options{docs: filepath.Join(dir, "docs"), site: filepath.Join(dir, "site"), out: out, readme: filepath.Join(dir, "none"), lint: true, check: true}
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	mustContain(t, readOut(t, out, "index.html"), `<section class="band" id="hero" data-band="hero">`, `<a href="learn/index.html">Learn</a>`)

	// No partials but a hand-written index.html: used as it is.
	writeTree(t, dir, map[string]string{"site/index.html": "<!doctype html><title>Old</title><a href=\"learn/index.html\">x</a>"})
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	if got := readOut(t, out, "index.html"); !strings.Contains(got, "<title>Old</title>") {
		t.Errorf("legacy index.html not used: %.80s", got)
	}

	// A partial wins over index.html; a badly named one draws a warning.
	writeTree(t, dir, map[string]string{"site/landing/01-hero.html": "<section class=\"band\" id=\"hero\" data-band=\"hero\"><div class=\"wrap\"><h1>New</h1></div></section>\n", "site/landing/bad.html": "<p>x</p>\n"})
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	got := readOut(t, out, "index.html")
	mustContain(t, got, "<h1>New</h1>", `<p>x</p>`)
}

func TestLandingPartialChecks(t *testing.T) {
	s := &site{cfg: &siteConfig{}, msgs: &messages{}}
	s.checkPartial("01-hero.html", `<section class="band" id="hero" data-band="hero"><div class="wrap">x</div></section>`)
	s.checkPartial("02-proof.html", `<section class="band" id="wrong" data-band="proof"><div class="wrap">x</div></section>`)
	s.checkPartial("04-bare.html", `<section class="band" id="bare" data-band="bare">x</section>`)
	s.checkPartial("hero.html", `<section>x</section>`)
	s.checkPartial("03-div.html", `<div>x</div>`)
	joined := strings.Join(s.msgs.warns, "\n")
	for _, want := range []string{`02-proof.html: the opening tag lacks id="proof"`, "hero.html: file names are NN-name.html", "03-div.html: a partial is one <section", `04-bare.html: the content of a band sits in one <div class="wrap">`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "01-hero") || len(s.msgs.warns) != 4 {
		t.Errorf("warnings: %v", s.msgs.warns)
	}
}

func TestSafeOutDir(t *testing.T) {
	dir := t.TempDir()
	docs := filepath.Join(dir, "docs")
	site := filepath.Join(dir, "site")
	for _, bad := range []string{dir, docs, site, "/"} {
		if err := safeOutDir(bad, docs, site); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
	for _, ok := range []string{filepath.Join(site, "public"), filepath.Join(dir, "out")} {
		if err := safeOutDir(ok, docs, site); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
}

func TestDetectVersions(t *testing.T) {
	bin := "../../bin/pravo"
	if _, err := os.Stat(bin); err != nil {
		t.Skip("no pravo binary built at bin/pravo")
	}
	pv, cv, err := detectVersions(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\d+\.\d+$`).MatchString(pv) {
		t.Errorf("pravo version %q", pv)
	}
	if cv != "" && !regexp.MustCompile(`^\d+\.\d+$`).MatchString(cv) {
		t.Errorf("CUE version %q", cv)
	}
	if _, _, err := detectVersions("/nonexistent/pravo"); err == nil {
		t.Errorf("a missing binary is an error")
	}
}

// TestHTMLWellFormed parses every generated page and checks that the elements
// of the contract nest properly: every figure, details and section that
// opens also closes.
func TestHTMLWellFormed(t *testing.T) {
	out := buildTestdata(t)
	_ = filepath.WalkDir(out, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		data, _ := os.ReadFile(p)
		counts := map[string]int{}
		z := xhtml.NewTokenizer(strings.NewReader(string(data)))
		for tt := z.Next(); tt != xhtml.ErrorToken; tt = z.Next() {
			tok := z.Token()
			switch tt {
			case xhtml.StartTagToken:
				if !voidTags[tok.Data] {
					counts[tok.Data]++
				}
			case xhtml.EndTagToken:
				counts[tok.Data]--
			}
		}
		var names []string
		for n, c := range counts {
			if c != 0 && n != "p" && n != "li" && n != "dt" && n != "dd" {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		if len(names) > 0 {
			rel, _ := filepath.Rel(out, p)
			t.Errorf("%s: unbalanced elements %v", rel, names)
		}
		return nil
	})
}

func TestLandingDataTokens(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"site/data/hero.json": `{"version": ["pravo 0.1.0", "cue v0.17.1"], "stats": {"examples": 148, "ratio": 0.5, "ok": true},
		"blocks": [{"command": "pravo plan", "output": "Error: a <b> & \"c\"\n  ./main.cue:3:7"}]}`})
	s := &site{cfg: &siteConfig{}, msgs: &messages{}, siteDir: filepath.Join(dir, "site")}

	got := s.substituteTokens("landing/01-hero.html", `<dd>{{data hero.stats.examples}}</dd> <pre>{{data hero.blocks.0.output}}</pre> {{data hero.version.1}} {{data hero.stats.ok}}`)
	want := `<dd>148</dd> <pre>Error: a &lt;b&gt; &amp; &#34;c&#34;` + "\n" + `  ./main.cue:3:7</pre> cue v0.17.1 true`
	if got != want || len(s.msgs.errs) > 0 {
		t.Errorf("got %q, want %q (errors %v)", got, want, s.msgs.errs)
	}

	got = s.substituteTokens("landing/01-hero.html", `<script type="application/json">{{json hero.blocks.0}}</script>`)
	mustContain(t, got, `{"command":"pravo plan","output":"Error: a `+"\\"+`u003cb`+"\\"+`u003e `+"\\"+`u0026 \"c\"`+"\\"+`n  ./main.cue:3:7"}`)
	mustNotContain(t, got, "<b>")

	for token, sub := range map[string]string{
		`{{data hero.stats.missing}}`:    `no key "missing"`,
		`{{data hero.version.9}}`:        "no index",
		`{{data nowhere.x}}`:             "cannot read site/data/nowhere.json",
		`{{data hero.stats}}`:            "use {{json ...}}",
		`{{data hero.stats.examples.x}}`: "is not inside an object or an array",
	} {
		s.msgs.errs = nil
		s.substituteTokens("landing/02-proof.html", token)
		if !hasError(s.msgs, sub) || !hasError(s.msgs, "site/landing/02-proof.html") {
			t.Errorf("%s: errors %v, want one containing %q", token, s.msgs.errs, sub)
		}
	}
}

func TestLandingProblemsFailTheBuild(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"site/site.json":            testSiteJSON,
		"site/assets/logo.svg":      "<svg/>",
		"site/assets/style.css":     "",
		"site/assets/site.js":       "",
		"docs/learn/index.md":       indexMD,
		"docs/reference/index.md":   strings.Replace(indexMD, "Learn", "Reference", 1),
		"site/data/hero.json":       `{"n": 4}`,
		"site/landing/01-hero.html": "<section class=\"band\" id=\"hero\" data-band=\"hero\"><div class=\"wrap\"><p>{{data hero.n}}</p></div></section>\n",
	})
	out := filepath.Join(dir, "out")
	o := options{docs: filepath.Join(dir, "docs"), site: filepath.Join(dir, "site"), out: out, readme: filepath.Join(dir, "none"), lint: true, check: true}
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	mustContain(t, readOut(t, out, "index.html"), "<p>4</p>")

	// A data token that does not resolve stops the build before anything is written.
	writeTree(t, dir, map[string]string{"site/landing/01-hero.html": "<section class=\"band\" id=\"hero\" data-band=\"hero\"><div class=\"wrap\"><p>{{data hero.missing}}</p></div></section>\n"})
	if err := os.RemoveAll(out); err != nil {
		t.Fatal(err)
	}
	if err := run(o); err == nil || !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("a data token that does not resolve must fail the build, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "index.html")); err == nil {
		t.Errorf("index.html was written although the build failed")
	}
}

func TestFooterIsOneRow(t *testing.T) {
	s := newTestSite(t, map[string]string{
		"site/site.json": strings.Replace(testSiteJSON, `"name": "pravo",`, `"name": "pravo", "description": "Infrastructure as code in CUE. More text follows.", "repo": "https://example.org/pravo",`, 1),
	})
	f := s.footerFor("learn/page.html")
	var titles []string
	for _, it := range f.Sections {
		titles = append(titles, it.Title+"="+it.Href)
	}
	if got := strings.Join(titles, " "); got != "Learn=index.html Reference=../reference/index.html" {
		t.Errorf("sections %s", got)
	}
	var proj []string
	for _, it := range f.Project {
		proj = append(proj, it.Title)
	}
	if got := strings.Join(proj, ","); got != "Repository,All pages,llms.txt" {
		t.Errorf("project links %s", got)
	}
	if !f.Project[0].External {
		t.Error("the repository link is not marked external")
	}
}

func TestPrettyIndexLinks(t *testing.T) {
	cases := map[string]string{
		`<a href="index.html">`:                                `<a href="./">`,
		`<a href="../index.html">`:                             `<a href="../">`,
		`<a href="learn/index.html">`:                          `<a href="learn/">`,
		`<a href="../learn/index.html#top">`:                   `<a href="../learn/#top">`,
		`<a href="learn/a01-intro.html">`:                      `<a href="learn/a01-intro.html">`,
		`<a href="https://x.org/index.html">`:                  `<a href="https://x.org/index.html">`,
		`<a href="reference/myindex.html">`:                    `<a href="reference/myindex.html">`,
		`<link rel="canonical" href="https://pravo.systems/">`: `<link rel="canonical" href="https://pravo.systems/">`,
	}
	for in, want := range cases {
		if got := string(prettyIndexLinks([]byte(in))); got != want {
			t.Errorf("%s became %s, want %s", in, got, want)
		}
	}
}
