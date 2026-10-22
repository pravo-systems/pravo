package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureFalseHasNoRanTag(t *testing.T) {
	body := "```sh run\npravo version\n```\n\n```text out\npravo 0.1\n```\n"
	html, msgs := renderBody(t, body)
	noErrors(t, msgs)
	mustContain(t, html, `data-ran="0.1"`)
	s := newTestSite(t, map[string]string{"learn/a01-lesson.md": strings.Replace(lessonSource(body), "order: 1", "order: 1\ncapture: false", 1)})
	noErrors(t, s.msgs)
	mustNotContain(t, string(s.mustPage(t, "learn/a01-lesson.md").Content), "data-ran")
}

func TestMissingSectionAndStrayDirectory(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"site/site.json":          strings.Replace(testSiteJSON, `{"id": "reference", "title": "Reference", "blurb": "Lookup."}`, `{"id": "reference", "title": "Reference", "blurb": "Lookup."}, {"id": "later", "title": "Later"}`, 1),
		"site/assets/logo.svg":    "<svg/>",
		"site/assets/style.css":   "",
		"site/assets/site.js":     "",
		"docs/learn/index.md":     indexMD,
		"docs/learn/_draft.md":    "no front matter, skipped\n",
		"docs/learn/img/fig.svg":  "<svg/>",
		"docs/learn/img/notes.md": "---\ntitle: N\ndescription: N.\norder: 1\n---\n",
		"docs/learn/data.json":    "{}",
		"docs/reference/index.md": strings.Replace(indexMD, "Learn", "Reference", 1),
		"docs/old/page.md":        "---\ntitle: Old\ndescription: Old.\norder: 1\n---\n",
	})
	out := filepath.Join(dir, "out")
	o := options{docs: filepath.Join(dir, "docs"), site: filepath.Join(dir, "site"), out: out, readme: filepath.Join(dir, "none"), lint: true, check: true}
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	nav := readOut(t, out, "learn/index.html")
	mustNotContain(t, nav, "later/index.html", "Later")
	if _, err := os.Stat(filepath.Join(out, "learn", "img", "fig.svg")); err != nil {
		t.Errorf("images next to pages are copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "learn", "data.json")); err == nil {
		t.Errorf("only images are copied")
	}
	if _, err := os.Stat(filepath.Join(out, "learn", "img", "notes.html")); err != nil {
		t.Errorf("pages in subdirectories are built: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "old")); err == nil {
		t.Errorf("a directory that is not a section is not published")
	}
	if _, err := os.Stat(filepath.Join(out, "learn", "_draft.html")); err == nil {
		t.Errorf("underscore files are skipped")
	}
	// The subdirectory page links back with the right depth.
	mustContain(t, readOut(t, out, "learn/img/notes.html"), `href="../../assets/style.css"`, `data-root="../../"`)
}

func TestStartHrefMustExist(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"site/site.json":          strings.Replace(testSiteJSON, `"start_href": "learn/index.html"`, `"start_href": "start/nowhere.html"`, 1),
		"docs/learn/index.md":     indexMD,
		"docs/reference/index.md": strings.Replace(indexMD, "Learn", "Reference", 1),
	})
	o := options{docs: filepath.Join(dir, "docs"), site: filepath.Join(dir, "site"), out: filepath.Join(dir, "out"), readme: filepath.Join(dir, "none"), lint: true}
	err := run(o)
	if err == nil || !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("want a failed build, got %v", err)
	}
}

func TestPinnedLinkToMissingPageWarns(t *testing.T) {
	cfg := strings.Replace(testSiteJSON, `{"id": "learn", "title": "Learn", "blurb": "Lessons.", "progress": true}`,
		`{"id": "learn", "title": "Learn", "blurb": "Lessons.", "pinned": [{"title": "Cheat sheet", "href": "reference/cheat.md"}]}`, 1)
	s := newTestSite(t, map[string]string{"site/site.json": cfg})
	p := s.mustPage(t, "learn/index.md")
	v := s.docViewFor(p)
	if len(v.Pinned) != 0 || !strings.Contains(strings.Join(s.msgs.warns, "\n"), "reference/cheat.md") {
		t.Errorf("pinned %v warns %v", v.Pinned, s.msgs.warns)
	}
}

func TestDirectoryLinks(t *testing.T) {
	s := newTestSite(t, map[string]string{
		"learn/a01-lesson.md": lessonSource("[up](./) and [ref](../reference/) and [file](pic.svg)\n"),
	})
	noErrors(t, s.msgs)
	html := string(s.mustPage(t, "learn/a01-lesson.md").Content)
	mustContain(t, html, `<a href="index.html">up</a>`, `<a href="../reference/index.html">ref</a>`, `<a href="pic.svg">file</a>`)
}

// Images that several pages use sit in docs/img and are published at /img. The
// directory is not a section, so it raises no warning, and a page links to its
// files like to any other.
func TestSharedImagesArePublished(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"site/site.json":               testSiteJSON,
		"site/assets/logo.svg":         "<svg/>",
		"site/assets/style.css":        "",
		"site/assets/site.js":          "",
		"docs/learn/index.md":          indexMD,
		"docs/reference/index.md":      strings.Replace(indexMD, "Learn", "Reference", 1),
		"docs/learn/page.md":           "---\ntitle: Page\ndescription: A page.\norder: 1\n---\n\n![A shot.](../img/serve/shot-dark.png)\n",
		"docs/img/serve/shot-dark.png": "png",
		"docs/img/serve/_draft.png":    "png",
		"docs/img/serve/notes.txt":     "notes",
		"docs/img/diagram.SVG":         "<svg/>",
	})
	out := filepath.Join(dir, "out")
	o := options{docs: filepath.Join(dir, "docs"), site: filepath.Join(dir, "site"), out: out, readme: filepath.Join(dir, "none"), lint: true, check: true}
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"img/serve/shot-dark.png", "img/diagram.SVG"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(f))); err != nil {
			t.Errorf("%s is published: %v", f, err)
		}
	}
	for _, f := range []string{"img/serve/_draft.png", "img/serve/notes.txt"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(f))); err == nil {
			t.Errorf("%s is not an image to publish", f)
		}
	}
	mustContain(t, readOut(t, out, "learn/page.html"), `src="../img/serve/shot-dark.png"`)
}

func TestSharedImagesAreNotASection(t *testing.T) {
	s := newTestSite(t, map[string]string{"img/a.png": "png"})
	for _, w := range s.msgs.warns {
		if strings.Contains(w, "docs/img") {
			t.Errorf("unexpected warning: %s", w)
		}
	}
}

// A page that links to an image that does not exist fails the link check.
func TestMissingSharedImageFailsTheLinkCheck(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"site/site.json":          testSiteJSON,
		"site/assets/logo.svg":    "<svg/>",
		"site/assets/style.css":   "",
		"site/assets/site.js":     "",
		"docs/learn/index.md":     indexMD,
		"docs/reference/index.md": strings.Replace(indexMD, "Learn", "Reference", 1),
		"docs/learn/page.md":      "---\ntitle: Page\ndescription: A page.\norder: 1\n---\n\n![A shot.](../img/serve/missing.png)\n",
	})
	o := options{docs: filepath.Join(dir, "docs"), site: filepath.Join(dir, "site"), out: filepath.Join(dir, "out"), readme: filepath.Join(dir, "none"), lint: true, check: true}
	err := run(o)
	if err == nil || !strings.Contains(err.Error(), "broken links") {
		t.Errorf("error = %v", err)
	}
}
