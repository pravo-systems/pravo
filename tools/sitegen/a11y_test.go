package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The search index carries every word of a page, code and tables included,
// the keywords of the front matter and the pages offered before a query.
func TestSearchEntryTermsKeywordsSuggest(t *testing.T) {
	src := "---\ntitle: T\ndescription: D.\norder: 1\nkeywords: \"workspace tfvars -var-file\"\nlearn: What is a bucket?\n---\n\n" +
		"## Heading\n\nProse about locals.\n\n```hcl\nresource \"aws_s3_bucket\" \"logs\" {}\n```\n\n| Name | Value |\n|---|---|\n| for_each | 1 |\n\n" +
		"<svg viewBox=\"0 0 10 10\"><path d=\"M10 20L30 40\"/></svg>\n\nsha256:9b00cd2a8f3e4c11aa\n"
	s := newTestSite(t, map[string]string{"learn/a01-lesson.md": src})
	noErrors(t, s.msgs)
	s.cfg.Suggest = []string{"learn/a01-lesson.md"}
	var found *searchEntry
	for _, e := range s.searchEntries() {
		if e.URL == "learn/a01-lesson.html" {
			e := e
			found = &e
		}
	}
	if found == nil {
		t.Fatal("no entry for the lesson")
	}
	words := " " + found.Terms + " "
	for _, w := range []string{"aws", "s3", "bucket", "logs", "for", "each", "locals", "prose", "heading", "resource"} {
		if !strings.Contains(words, " "+w+" ") {
			t.Errorf("terms lack %q: %s", w, found.Terms)
		}
	}
	for _, w := range []string{"m10", "viewbox", "9b00cd2a8f3e4c11aa"} {
		if strings.Contains(words, " "+w+" ") {
			t.Errorf("terms hold the noise %q: %s", w, found.Terms)
		}
	}
	if found.Keywords != "workspace tfvars -var-file" {
		t.Errorf("keywords %q", found.Keywords)
	}
	if found.Suggest != 1 {
		t.Errorf("suggest %d", found.Suggest)
	}
	// every word appears once, in alphabetical order
	ws := strings.Fields(found.Terms)
	for i := 1; i < len(ws); i++ {
		if ws[i-1] >= ws[i] {
			t.Errorf("terms are not sorted and unique at %q %q", ws[i-1], ws[i])
			break
		}
	}
}

// A page that is not in the site cannot be suggested.
func TestSearchSuggestNamesAPage(t *testing.T) {
	dir := t.TempDir()
	cfg := strings.Replace(testSiteJSON, `"sections"`, `"search_suggest": ["learn/nowhere.md"], "sections"`, 1)
	writeTree(t, dir, map[string]string{
		"site/site.json":          cfg,
		"site/assets/logo.svg":    "<svg/>",
		"site/assets/style.css":   "",
		"site/assets/site.js":     "",
		"docs/learn/index.md":     indexMD,
		"docs/reference/index.md": strings.Replace(indexMD, "Learn", "Reference", 1),
	})
	o := options{docs: filepath.Join(dir, "docs"), site: filepath.Join(dir, "site"), out: filepath.Join(dir, "out"), readme: filepath.Join(dir, "none"), lint: true, check: true}
	err := run(o)
	if err == nil {
		t.Fatal("a build with a search_suggest page that does not exist passes")
	}
}

// A page the host cannot find is served the same file at any depth: the
// stylesheet, the script and every link start at the root of the site.
func TestNotFoundPage(t *testing.T) {
	out := buildTestdata(t)
	nf := readOut(t, out, "404.html")
	mustContain(t, nf,
		`<meta name="robots" content="noindex">`,
		`<h1>Page not found</h1>`,
		`<link rel="stylesheet" href="/assets/style.css">`,
		`<script src="/assets/site.js" defer></script>`,
		`href="/learn/index.html"`, `href="/contents.html"`,
		`data-root="/"`, `data-search-open`, `data-notfound-path`,
	)
	mustNotContain(t, nf, `rel="canonical"`)
	for _, m := range regexp.MustCompile(`(?:href|src)="([^"]*)"`).FindAllStringSubmatch(nf, -1) {
		v := m[1]
		if v == "" || strings.HasPrefix(v, "/") || strings.HasPrefix(v, "#") || strings.Contains(v, ":") {
			continue
		}
		t.Errorf("404.html has a path that depends on the address: %q", v)
	}
}

// The landing page takes the Go release and the minutes of Start from the
// sources, and the build fails when a source is missing.
func TestDerivedLandingTokens(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":                    "module example.test/m\n\ngo 1.26.0\n",
		"site/site.json":            strings.Replace(testSiteJSON, `"learn/index.html"`, `"start/index.html"`, 1),
		"site/assets/logo.svg":      "<svg/>",
		"site/assets/style.css":     "",
		"site/assets/site.js":       "",
		"site/landing/00-hero.html": `<section class="band" id="hero" data-band="hero"><div class="wrap"><p>Go {{go_version}} or newer. {{start_minutes}} minutes.</p></div></section>`,
		"docs/start/index.md":       "---\ntitle: Start\ndescription: Begin.\nminutes: 12\n---\n\nStart.\n",
		"docs/learn/index.md":       indexMD,
		"docs/reference/index.md":   strings.Replace(indexMD, "Learn", "Reference", 1),
	})
	cfg := strings.Replace(testSiteJSON, `"sections": [`, `"sections": [{"id": "start", "title": "Start", "blurb": "First.", "nav": false},`, 1)
	cfg = strings.Replace(cfg, `"learn/index.html"`, `"start/index.html"`, 1)
	writeTree(t, dir, map[string]string{"site/site.json": cfg})
	out := filepath.Join(dir, "out")
	o := options{docs: filepath.Join(dir, "docs"), site: filepath.Join(dir, "site"), out: out, readme: filepath.Join(dir, "none"), lint: true, check: true}
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	mustContain(t, readOut(t, out, "index.html"), "Go 1.26 or newer. 12 minutes.")

	// without go.mod the token is an error
	if err := os.Remove(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if err := run(o); err == nil {
		t.Error("a landing page that uses {{go_version}} builds without go.mod")
	}
}

// The landing page types no number that a source states: the Go release, the
// minutes of Start, the state format and the plugin protocols come from
// tokens, so that they cannot drift from go.mod, the page that start_href names,
// internal/tfstate and docs/reference/compatibility.md.
func TestLandingTypesNoSourceNumbers(t *testing.T) {
	files, _ := filepath.Glob("../../site/landing/*.html")
	if len(files) == 0 {
		t.Skip("no landing partials next to the tool")
	}
	typed := regexp.MustCompile(`Go 1\.\d+|state format \d|in format \d|reads format \d|plugin protocol \d|Terraform 0\.\d+|\b\d+ minutes &middot; no account`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range typed.FindAllString(string(b), -1) {
			t.Errorf("%s types %q: use {{go_version}}, {{start_minutes}} or a facts token", filepath.Base(f), m)
		}
	}
}
