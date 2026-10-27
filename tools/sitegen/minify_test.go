package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/js"
)

const sampleJS = `/* a header comment */
(function () {
  "use strict";
  var longVariableName = window.pravoUI || {};
  function showTheThing(element, className) {
    // a line comment
    element.classList.add(className);
    return element.getAttribute("data-state");
  }
  window.pravoUI = {
    timeline: function (callback) { callback(longVariableName); },
    show: showTheThing
  };
  document.addEventListener("DOMContentLoaded", function () {
    var anchor = document.querySelector(".band");
    if (anchor) showTheThing(anchor, "is-on");
  });
})();
`

const sampleCSS = `/* tokens */
:root {
  --accent: #0a735a;
  --gap: 16px;
}
:root[data-theme="dark"] {
  --accent: #3ddbb0;
}
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) { --accent: #3ddbb0; }
}
.band > .wrap {
  color: var(--accent);
  margin: 0 auto;
  padding: calc(var(--gap) * 2)   var(--gap);
}
@keyframes rise { from { opacity: 0 } to { opacity: 1 } }
`

// assetSite writes a small site whose assets are the given files and returns
// the options to build it.
func assetSite(t *testing.T, assets map[string]string) (options, string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"site/site.json":          testSiteJSON,
		"site/assets/logo.svg":    "<svg/>",
		"site/assets/style.css":   "",
		"site/assets/site.js":     "",
		"docs/learn/index.md":     indexMD,
		"docs/reference/index.md": strings.Replace(indexMD, "Learn", "Reference", 1),
	}
	for name, content := range assets {
		files["site/assets/"+name] = content
	}
	writeTree(t, dir, files)
	out := filepath.Join(dir, "out")
	return options{docs: filepath.Join(dir, "docs"), site: filepath.Join(dir, "site"), out: out, readme: filepath.Join(dir, "none"), lint: true, check: true}, out
}

func TestMinifyKind(t *testing.T) {
	for name, want := range map[string]string{
		"site.js":           mimeJS,
		"demos/idea.js":     mimeJS,
		"style.css":         mimeCSS,
		"STYLE.CSS":         mimeCSS,
		"vendor.min.js":     "",
		"vendor.min.css":    "",
		"logo.svg":          "",
		"fonts/a.woff2":     "",
		"fonts/README.md":   "",
		"search-index.json": "",
	} {
		if got := minifyKind(name); got != want {
			t.Errorf("minifyKind(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestMinifyAssetShrinksAndKeepsNames(t *testing.T) {
	m := newAssetMinifier()
	in := []byte(sampleJS)
	out, err := m.minify("site.js", in)
	if err != nil {
		t.Fatal(err)
	}
	if string(in) != sampleJS {
		t.Errorf("the minifier changed its input:\n%s", in)
	}
	got := string(out)
	if len(out) >= len(sampleJS)*3/4 {
		t.Errorf("script is %d bytes after minification, from %d", len(out), len(sampleJS))
	}
	mustContain(t, got, "window.pravoUI", "timeline:", "show:", `"DOMContentLoaded"`, `".band"`, `"is-on"`, `"data-state"`, ".classList.add(", ".getAttribute(", `"use strict"`)
	mustNotContain(t, got, "longVariableName", "showTheThing", "a header comment", "a line comment", "\n\n")

	out, err = m.minify("style.css", []byte(sampleCSS))
	if err != nil {
		t.Fatal(err)
	}
	got = string(out)
	if len(out) >= len(sampleCSS)*4/5 {
		t.Errorf("style sheet is %d bytes after minification, from %d", len(out), len(sampleCSS))
	}
	mustContain(t, got, "--accent:#0a735a", "--gap:16px", `:root[data-theme=dark]`, "prefers-color-scheme:dark", `:root:not([data-theme=light])`, ".band>.wrap", "var(--gap)", "@keyframes rise")
	mustNotContain(t, got, "/* tokens */")

	same, err := m.minify("logo.svg", []byte("<svg>  </svg>"))
	if err != nil || string(same) != "<svg>  </svg>" {
		t.Errorf("a file of another type is returned as it is: %q, %v", same, err)
	}
	same, err = m.minify("vendor.min.js", []byte("var a = 1;   var b = 2;"))
	if err != nil || string(same) != "var a = 1;   var b = 2;" {
		t.Errorf("a file that is minified already is returned as it is: %q, %v", same, err)
	}
	tiny, err := m.minify("x.js", []byte("a()"))
	if err != nil || string(tiny) != "a()" {
		t.Errorf("a result that is not smaller is not used: %q, %v", tiny, err)
	}
	if _, err := m.minify("bad.js", []byte("function (")); err == nil {
		t.Errorf("a script that does not parse is an error")
	}
}

func TestBuildMinifiesAssets(t *testing.T) {
	o, out := assetSite(t, map[string]string{
		"site.js":       sampleJS,
		"style.css":     sampleCSS,
		"logo.svg":      "<svg>  </svg>",
		"vendor.min.js": "var a = 1;   var b = 2;",
	})
	o.minify = true
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	script := readOut(t, out, "assets/site.js")
	sheet := readOut(t, out, "assets/style.css")
	if len(script) >= len(sampleJS) || strings.Contains(script, "longVariableName") {
		t.Errorf("assets/site.js is not minified:\n%s", script)
	}
	if len(sheet) >= len(sampleCSS) || strings.Contains(sheet, "/* tokens */") {
		t.Errorf("assets/style.css is not minified:\n%s", sheet)
	}
	if got := readOut(t, out, "assets/logo.svg"); got != "<svg>  </svg>" {
		t.Errorf("assets/logo.svg changed: %q", got)
	}
	if got := readOut(t, out, "assets/vendor.min.js"); got != "var a = 1;   var b = 2;" {
		t.Errorf("assets/vendor.min.js changed: %q", got)
	}
	// The sources stay as they are.
	src, err := os.ReadFile(filepath.Join(o.site, "assets", "site.js"))
	if err != nil || string(src) != sampleJS {
		t.Errorf("the source script changed: %v", err)
	}
}

func TestBuildWithoutMinifyCopiesAssets(t *testing.T) {
	o, out := assetSite(t, map[string]string{"site.js": sampleJS, "style.css": sampleCSS})
	o.minify = false
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	if got := readOut(t, out, "assets/site.js"); got != sampleJS {
		t.Errorf("assets/site.js differs from the source:\n%s", got)
	}
	if got := readOut(t, out, "assets/style.css"); got != sampleCSS {
		t.Errorf("assets/style.css differs from the source:\n%s", got)
	}
}

func TestMinifyErrorFailsBeforeWriting(t *testing.T) {
	o, out := assetSite(t, map[string]string{"site.js": "function (", "style.css": sampleCSS})
	o.minify = true
	err := run(o)
	if err == nil || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("want a failed build, got %v", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("the output directory was written")
	}
	// The same file builds with -minify=false: the minifier is the one that reads it.
	o.minify = false
	if err := run(o); err != nil {
		t.Errorf("without minification: %v", err)
	}
}

func TestMinifyFlag(t *testing.T) {
	o, err := parseOptions(nil, flag.ContinueOnError)
	if err != nil || !o.minify {
		t.Errorf("minification is on by default: %+v, %v", o, err)
	}
	o, err = parseOptions([]string{"-minify=false"}, flag.ContinueOnError)
	if err != nil || o.minify {
		t.Errorf("-minify=false turns it off: %+v, %v", o, err)
	}
}

// --- the real assets ---

// realSiteDirs returns the module's docs and site directories, or skips.
func realSiteDirs(t *testing.T) (docs, site string) {
	t.Helper()
	docs, site = filepath.Join("..", "..", "docs"), filepath.Join("..", "..", "site")
	for _, d := range []string{docs, filepath.Join(site, "site.json"), filepath.Join(site, "assets")} {
		if _, err := os.Stat(d); err != nil {
			t.Skipf("no %s next to the tool", d)
		}
	}
	return docs, site
}

func realAssets(t *testing.T, ext string) map[string][]byte {
	t.Helper()
	_, site := realSiteDirs(t)
	root := filepath.Join(site, "assets")
	files := map[string][]byte{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ext) {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skipf("no %s files in %s", ext, root)
	}
	return files
}

// jsNames collects what a minifier must not rename: the free variables of the
// script (browser globals and members of the page), the property names read
// with a dot, and the keys of object literals.
func jsNames(t *testing.T, name string, src []byte) (free, props map[string]bool) {
	t.Helper()
	// The parser reads the buffer it is given and may write into it.
	ast, err := js.Parse(parse.NewInputBytes(append([]byte(nil), src...)), js.Options{})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	free, props = map[string]bool{}, map[string]bool{}
	js.Walk(nameVisitor{free, props}, &ast.BlockStmt)
	return free, props
}

type nameVisitor struct{ free, props map[string]bool }

func (v nameVisitor) Enter(n js.INode) js.IVisitor {
	switch x := n.(type) {
	case *js.Var:
		r := x
		for r.Link != nil {
			r = r.Link
		}
		if r.Decl == js.NoDecl {
			v.free[string(r.Data)] = true
		}
	case *js.DotExpr:
		switch y := x.Y.(type) {
		case js.LiteralExpr:
			v.props[string(y.Data)] = true
		case *js.LiteralExpr:
			v.props[string(y.Data)] = true
		}
	case *js.PropertyName:
		if x.Computed == nil {
			v.props[strings.Trim(string(x.Literal.Data), `"'`)] = true
		}
	}
	return v
}

func (v nameVisitor) Exit(js.INode) {}

// rewritten names are ones that the minifier replaces with an expression of the
// same value: undefined becomes void 0, Infinity becomes 1/0 and Math.pow(a, b)
// becomes a**b.
var rewritten = map[string]bool{"undefined": true, "Infinity": true, "NaN": true, "pow": true}

func missing(want, have map[string]bool) []string {
	var out []string
	for k := range want {
		if !have[k] && !rewritten[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// The minifier renames the local variables of a script and nothing else: the
// names that the page and the browser see are the same before and after.
func TestMinifiedScriptsKeepTheirNames(t *testing.T) {
	m := newAssetMinifier()
	for name, src := range realAssets(t, ".js") {
		out, err := m.minify(name, src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(out) >= len(src) {
			t.Errorf("%s: %d bytes after minification, from %d", name, len(out), len(src))
		}
		freeSrc, propSrc := jsNames(t, name, src)
		freeOut, propOut := jsNames(t, name+" (minified)", out)
		if m := missing(freeSrc, freeOut); len(m) > 0 {
			t.Errorf("%s: free variables lost in minification: %v", name, m)
		}
		if m := missing(freeOut, freeSrc); len(m) > 0 {
			t.Errorf("%s: free variables added by minification: %v", name, m)
		}
		if m := missing(propSrc, propOut); len(m) > 0 {
			t.Errorf("%s: property names lost in minification: %v", name, m)
		}
	}
}

var (
	cssCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssURLRe     = regexp.MustCompile(`url\([^)]*\)`)
	cssStringRe  = regexp.MustCompile(`"[^"\n]*"|'[^'\n]*'`)
	cssClassRe   = regexp.MustCompile(`\.[a-zA-Z_][\w-]*`)
	cssVarRe     = regexp.MustCompile(`--[a-zA-Z][\w-]*`)
	cssAtRe      = regexp.MustCompile(`@[a-z-]+(?:\s+[\w-]+)?`)
)

// cssNames collects the names that the markup and the scripts refer to: class
// selectors, custom properties and at-rules with their names.
func cssNames(src string) (classes, vars, ats map[string]bool) {
	src = cssCommentRe.ReplaceAllString(src, "")
	src = cssURLRe.ReplaceAllString(src, "url()")
	src = cssStringRe.ReplaceAllString(src, `""`)
	set := func(re *regexp.Regexp, norm func(string) string) map[string]bool {
		m := map[string]bool{}
		for _, s := range re.FindAllString(src, -1) {
			m[norm(s)] = true
		}
		return m
	}
	id := func(s string) string { return s }
	at := func(s string) string {
		f := strings.Fields(s)
		if f[0] == "@keyframes" || f[0] == "@property" || f[0] == "@layer" || f[0] == "@view-transition" {
			return strings.Join(f, " ")
		}
		return f[0]
	}
	return set(cssClassRe, id), set(cssVarRe, id), set(cssAtRe, at)
}

func TestMinifiedStyleSheetKeepsItsNames(t *testing.T) {
	m := newAssetMinifier()
	for name, src := range realAssets(t, ".css") {
		out, err := m.minify(name, src)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(out) >= len(src) {
			t.Errorf("%s: %d bytes after minification, from %d", name, len(out), len(src))
		}
		c1, v1, a1 := cssNames(string(src))
		c2, v2, a2 := cssNames(string(out))
		for label, pair := range map[string][2]map[string]bool{"class selectors": {c1, c2}, "custom properties": {v1, v2}, "at-rules": {a1, a2}} {
			if m := missing(pair[0], pair[1]); len(m) > 0 {
				t.Errorf("%s: %s lost in minification: %v", name, label, m)
			}
			if m := missing(pair[1], pair[0]); len(m) > 0 {
				t.Errorf("%s: %s added by minification: %v", name, label, m)
			}
		}
	}
}

// --- the budget of the landing page ---

const (
	// landingJSBudget is the most gzipped JavaScript that the landing page
	// loads, in bytes: the scripts it names and the script they load by
	// themselves while the page is shown.
	landingJSBudget = 8000
	// landingTotalBudget is the most gzipped bytes of everything the landing
	// page loads except fonts: the page, the style sheet, the scripts and the
	// images.
	landingTotalBudget = 50000
)

// loadedWithLanding lists the scripts that the scripts of the landing page
// load by themselves, below the site root. The landing page loads none: search.js
// loads when the reader opens the search palette, and timeline.js loads for a
// demo that a page of the documentation embeds.
var loadedWithLanding = []string{}

var (
	tagScriptRe = regexp.MustCompile(`<script\b[^>]*\bsrc="([^"]+)"`)
	tagLinkRe   = regexp.MustCompile(`<link\b[^>]*>`)
	tagImgRe    = regexp.MustCompile(`<img\b[^>]*\bsrc="([^"]+)"`)
	attrRelRe   = regexp.MustCompile(`\brel="([^"]+)"`)
	attrHrefRe  = regexp.MustCompile(`\bhref="([^"]+)"`)
	attrAsRe    = regexp.MustCompile(`\bas="([^"]+)"`)
	cssRefRe    = regexp.MustCompile(`url\(\s*["']?([^"')\s]+)["']?\s*\)|@import\s+["']([^"']+)["']`)
)

var fontExts = map[string]bool{".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true}

// loadedAsset is a file that the landing page loads, with its size on the wire.
type loadedAsset struct {
	Path string // slash path below the output directory
	Kind string // html, css, js or image
	Raw  int
	Wire int // the smaller of the file and its gzip
}

func gzipSize(b []byte) int {
	var buf bytes.Buffer
	w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Len()
}

func localRef(from, ref string) (string, bool) {
	if i := strings.IndexAny(ref, "?#"); i >= 0 {
		ref = ref[:i]
	}
	if ref == "" || strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "//") || strings.Contains(ref, "://") {
		return "", false
	}
	return path.Join(path.Dir(from), ref), true
}

// landingAssets finds what index.html in out loads: the page, the style
// sheets, the scripts, the icons and the images, and the images that the style
// sheets draw. Fonts are left out.
func landingAssets(t *testing.T, out string) []loadedAsset {
	t.Helper()
	seen := map[string]string{}
	var order []string
	add := func(p, kind string) {
		if _, ok := seen[p]; !ok {
			seen[p] = kind
			order = append(order, p)
		}
	}
	add("index.html", "html")
	page := readOut(t, out, "index.html")
	for _, m := range tagScriptRe.FindAllStringSubmatch(page, -1) {
		if p, ok := localRef("index.html", m[1]); ok {
			add(p, "js")
		}
	}
	for _, p := range loadedWithLanding {
		add(p, "js")
	}
	for _, m := range tagImgRe.FindAllStringSubmatch(page, -1) {
		if p, ok := localRef("index.html", m[1]); ok {
			add(p, "image")
		}
	}
	for _, tag := range tagLinkRe.FindAllString(page, -1) {
		rel, href := attrRelRe.FindStringSubmatch(tag), attrHrefRe.FindStringSubmatch(tag)
		if rel == nil || href == nil {
			continue
		}
		p, ok := localRef("index.html", href[1])
		if !ok {
			continue
		}
		r := " " + rel[1] + " "
		switch {
		case strings.Contains(r, " stylesheet "):
			add(p, "css")
		case strings.Contains(r, " icon ") || strings.Contains(r, " shortcut ") || strings.Contains(r, " apple-touch-icon ") || strings.Contains(r, " mask-icon "):
			add(p, "image")
		case strings.Contains(r, " preload "):
			if as := attrAsRe.FindStringSubmatch(tag); as != nil && as[1] != "font" {
				add(p, map[string]string{"style": "css", "script": "js", "image": "image"}[as[1]])
			}
		}
	}
	// The images that the style sheets draw, and the sheets they import.
	for i := range order {
		p := order[i]
		if seen[p] != "css" {
			continue
		}
		for _, m := range cssRefRe.FindAllStringSubmatch(readOut(t, out, p), -1) {
			ref := m[1] + m[2]
			q, ok := localRef(p, ref)
			if !ok || fontExts[strings.ToLower(path.Ext(q))] {
				continue
			}
			if m[2] != "" || strings.HasSuffix(q, ".css") {
				add(q, "css")
			} else {
				add(q, "image")
			}
		}
	}
	var assets []loadedAsset
	for _, p := range order {
		if seen[p] == "" {
			t.Errorf("index.html loads %s, which has no known type", p)
			continue
		}
		data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(p)))
		if err != nil {
			t.Errorf("index.html loads %s: %v", p, err)
			continue
		}
		gz := gzipSize(data)
		wire := gz
		if len(data) < wire {
			wire = len(data)
		}
		assets = append(assets, loadedAsset{Path: p, Kind: seen[p], Raw: len(data), Wire: wire})
	}
	return assets
}

// buildRealSite builds the site of the module into a new directory.
func buildRealSite(t *testing.T, minify bool) string {
	t.Helper()
	docs, site := realSiteDirs(t)
	out := filepath.Join(t.TempDir(), "public")
	o := options{docs: docs, site: site, out: out, readme: filepath.Join(t.TempDir(), "none"), lint: false, check: false, minify: minify}
	if err := run(o); err != nil {
		t.Fatalf("building the site: %v", err)
	}
	return out
}

// landingTotals sums the bytes on the wire: all of them, and those of the
// scripts.
func landingTotals(assets []loadedAsset) (total, js, jsFiles int) {
	for _, a := range assets {
		total += a.Wire
		if a.Kind == "js" {
			js += a.Wire
			jsFiles++
		}
	}
	return total, js, jsFiles
}

// budgetProblems reports what is over the budget.
func budgetProblems(assets []loadedAsset) []string {
	total, js, _ := landingTotals(assets)
	var out []string
	if js > landingJSBudget {
		out = append(out, fmt.Sprintf("the landing page loads %d bytes of gzipped JavaScript; the budget is %d", js, landingJSBudget))
	}
	if total > landingTotalBudget {
		out = append(out, fmt.Sprintf("the landing page loads %d bytes gzipped without fonts; the budget is %d", total, landingTotalBudget))
	}
	return out
}

func logLanding(t *testing.T, assets []loadedAsset) {
	t.Helper()
	sort.SliceStable(assets, func(i, j int) bool {
		if assets[i].Kind != assets[j].Kind {
			return assets[i].Kind < assets[j].Kind
		}
		return assets[i].Path < assets[j].Path
	})
	for _, a := range assets {
		t.Logf("%-6s %-32s %8d raw %8d gzip", a.Kind, a.Path, a.Raw, a.Wire)
	}
	total, js, jsFiles := landingTotals(assets)
	t.Logf("landing page JavaScript: %d bytes gzipped in %d files (budget %d)", js, jsFiles, landingJSBudget)
	t.Logf("landing page without fonts: %d bytes gzipped in %d files (budget %d)", total, len(assets), landingTotalBudget)
}

// TestLandingPageBudget fails when the landing page grows past its budget. It
// builds the site as `go run ./tools/sitegen` does and measures what
// index.html loads, with each file gzipped at the highest level on its own.
// Run it with -v to print the table that site/README.md keeps, for the
// minified build that is published and for the build with -minify=false.
func TestLandingPageBudget(t *testing.T) {
	out := buildRealSite(t, true)
	assets := landingAssets(t, out)
	logLanding(t, assets)

	_, js, jsFiles := landingTotals(assets)
	if jsFiles < 1 || js == 0 {
		t.Fatalf("found %d scripts and %d bytes: the measurement does not see the page", jsFiles, js)
	}
	for _, p := range loadedWithLanding {
		found := false
		for _, a := range assets {
			found = found || a.Path == p
		}
		if !found {
			t.Errorf("%s is listed in loadedWithLanding and is not in the output", p)
		}
	}
	for _, p := range budgetProblems(assets) {
		t.Error(p)
	}

	if testing.Verbose() {
		t.Logf("the same build with -minify=false:")
		logLanding(t, landingAssets(t, buildRealSite(t, false)))
	}
}

func TestBudgetProblems(t *testing.T) {
	mk := func(js, other int) []loadedAsset {
		return []loadedAsset{{Path: "index.html", Kind: "html", Wire: other}, {Path: "assets/site.js", Kind: "js", Wire: js}}
	}
	for _, c := range []struct {
		name      string
		js, other int
		want      int
	}{
		{"within both", landingJSBudget / 2, landingTotalBudget / 2, 0},
		{"scripts at the limit", landingJSBudget, 10000, 0},
		{"everything at the limit", landingJSBudget, landingTotalBudget - landingJSBudget, 0},
		{"scripts over", landingJSBudget + 1, 10000, 1},
		{"everything over", landingJSBudget, landingTotalBudget - landingJSBudget + 1, 1},
		{"both over", landingJSBudget + 1, landingTotalBudget, 2},
	} {
		if got := budgetProblems(mk(c.js, c.other)); len(got) != c.want {
			t.Errorf("%s: %d problems, want %d: %v", c.name, len(got), c.want, got)
		}
	}
}

func TestLandingAssetsFollowTheMarkup(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"index.html": `<!doctype html><link rel="icon" href="favicon.ico"><link rel="stylesheet" href="assets/style.css">` +
			`<link rel="preload" href="assets/fonts/a.woff2" as="font" type="font/woff2" crossorigin>` +
			`<link rel="canonical" href="https://example.org/"><script src="assets/site.js" defer></script>` +
			`<script src="https://cdn.example.org/x.js"></script><img src="assets/logo.svg" alt="">`,
		"favicon.ico":      "ico",
		"assets/style.css": `@font-face{src:url(fonts/a.woff2)} .m{mask:url("data:image/svg+xml,<svg/>")} .b{background:url(bg.png)} @import "more.css";`,
		"assets/more.css":  "a{}",
		"assets/bg.png":    "png",
		"assets/site.js":   "var a;",
		"assets/logo.svg":  "<svg/>",
	})
	var got []string
	for _, a := range landingAssets(t, dir) {
		got = append(got, a.Kind+" "+a.Path)
	}
	sort.Strings(got)
	want := []string{
		"css assets/more.css", "css assets/style.css", "html index.html",
		"image assets/bg.png", "image assets/logo.svg", "image favicon.ico",
		"js assets/site.js",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("assets:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
