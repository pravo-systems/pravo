package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSiteJSON = `{
  "name": "pravo",
  "pravo_version": "0.1",
  "cue_version": "0.17",
  "start_href": "learn/index.html",
  "glossary": "reference/glossary.md",
  "footer_note": "pravo is an independent project, licensed under Apache-2.0.",
  "sections": [
    {"id": "learn", "title": "Learn", "blurb": "Lessons.", "progress": true},
    {"id": "reference", "title": "Reference", "blurb": "Lookup."}
  ]
}`

const indexMD = "---\ntitle: Learn\ndescription: The lessons.\n---\n\nStart here.\n"

// writeTree writes files (slash paths relative to dir) below dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// newTestSite writes a small docs tree and runs discovery, arrangement and
// rendering. docs maps "learn/page.md" to file content; an index.md is added
// for each section that has none and the default site.json is used unless the
// files include "site/site.json".
func newTestSite(t *testing.T, docs map[string]string) *site {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{"site/site.json": testSiteJSON}
	for name, content := range docs {
		if strings.HasPrefix(name, "site/") {
			files[name] = content
		} else {
			files["docs/"+name] = content
		}
	}
	for _, sec := range []string{"learn", "reference"} {
		if _, ok := files["docs/"+sec+"/index.md"]; !ok {
			files["docs/"+sec+"/index.md"] = strings.Replace(indexMD, "Learn", strings.ToUpper(sec[:1])+sec[1:], 1)
		}
	}
	writeTree(t, dir, files)
	cfg, err := loadConfig(filepath.Join(dir, "site", "site.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := &site{cfg: cfg, docsDir: filepath.Join(dir, "docs"), siteDir: filepath.Join(dir, "site"), outDir: filepath.Join(dir, "out"), msgs: &messages{}}
	s.applies = cfg.applies()
	s.discover()
	s.arrange()
	s.loadGlossary()
	for _, p := range s.allPages() {
		s.renderPage(p)
	}
	return s
}

// page returns the page with the given docs path.
func (s *site) mustPage(t *testing.T, doc string) *page {
	t.Helper()
	p, ok := s.byDoc[doc]
	if !ok {
		t.Fatalf("no page %s", doc)
	}
	return p
}

// lessonSource wraps body in valid front matter.
func lessonSource(body string) string {
	return "---\ntitle: Lesson\ndescription: A lesson.\norder: 1\n---\n\n" + body
}

// renderBody renders one lesson body and returns the HTML and the messages.
func renderBody(t *testing.T, body string) (string, *messages) {
	t.Helper()
	s := newTestSite(t, map[string]string{"learn/a01-lesson.md": lessonSource(body)})
	return string(s.mustPage(t, "learn/a01-lesson.md").Content), s.msgs
}

func mustContain(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

func mustNotContain(t *testing.T, got string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(got, w) {
			t.Errorf("unexpected %q in:\n%s", w, got)
		}
	}
}

func noErrors(t *testing.T, m *messages) {
	t.Helper()
	if len(m.errs) > 0 {
		t.Fatalf("unexpected errors:\n%s", strings.Join(m.errs, "\n"))
	}
}

func hasError(m *messages, sub string) bool {
	for _, e := range m.errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}
