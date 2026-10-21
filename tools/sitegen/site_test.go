package main

import (
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func fm(title string, order int, extra string) string {
	return "---\ntitle: " + title + "\ndescription: " + title + " page.\norder: " + strconv.Itoa(order) + "\n" + extra + "---\n\nBody.\n"
}

func TestFrontMatterSplit(t *testing.T) {
	y, body, n, ok := splitFrontMatter([]byte("---\ntitle: A\norder: 1\n---\nBody\n"))
	if !ok || y != "title: A\norder: 1\n" || string(body) != "Body\n" || n != 4 {
		t.Errorf("got %q %q %d %v", y, body, n, ok)
	}
	_, _, _, ok = splitFrontMatter([]byte("No header\n"))
	if ok {
		t.Errorf("a file without a header has no front matter")
	}
	_, _, _, ok = splitFrontMatter([]byte("---\ntitle: A\nnever closed\n"))
	if ok {
		t.Errorf("an unclosed header is not front matter")
	}
	y, body, _, ok = splitFrontMatter([]byte("---\r\ntitle: A\r\n---\r\nBody\r\n"))
	if !ok || y != "title: A\n" || string(body) != "Body\n" {
		t.Errorf("CRLF: %q %q", y, body)
	}
}

func TestParseFrontMatter(t *testing.T) {
	got, err := parseFrontMatter("f.md", `title: Writing a field twice          # required
description: One sentence.
order: 3
group: Track A, CUE from zero
minutes: 10
needs: A2
learn: What happens when two values disagree?
done_when: pravo eval prints one value.
level: beginner
applies_to: pravo 0.2, CUE 0.17
capture: false
normalize:
  - pattern: "id=[a-z-]+"
    replace: "id=<id>"
services: [fakes3:9000, fakeoci]
env:
  CUE_REGISTRY: example.com=localhost:5078+insecure
`)
	if err != nil {
		t.Fatal(err)
	}
	want := frontMatter{Title: "Writing a field twice", Description: "One sentence.", Order: 3, HasOrder: true,
		Group: "Track A, CUE from zero", Minutes: 10, Needs: "A2", Learn: "What happens when two values disagree?",
		DoneWhen: "pravo eval prints one value.", Level: "beginner", AppliesTo: "pravo 0.2, CUE 0.17", Capture: false}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	bad := map[string]string{
		"title: A\nbogus: 1\n":          "unknown key bogus",
		"order: three\n":                "not an integer",
		"minutes: 0\n":                  "positive integer",
		"level: expert\n":               "beginner, intermediate or advanced",
		"capture: maybe\n":              "true or false",
		"title: [a, b]\n":               "single value",
		"title: a: b\n":                 "front matter",
		"- a\n- b\n":                    "must be a mapping",
		"title: A\ntitle2: B\nfoo: 1\n": "unknown key foo, title2",
	}
	for text, want := range bad {
		_, err := parseFrontMatter("f.md", text)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want %q", text, err, want)
		}
	}
	// Required keys.
	f, _ := parseFrontMatter("f.md", "title: A\n")
	if err := f.validate("f.md", false); err == nil || !strings.Contains(err.Error(), "description, order") {
		t.Errorf("validate: %v", err)
	}
	if err := f.validate("f.md", true); err == nil || strings.Contains(err.Error(), "order") {
		t.Errorf("an index needs no order: %v", err)
	}
	r, _ := parseFrontMatter("f.md", "redirect: x.md\n")
	if err := r.validate("f.md", false); err != nil {
		t.Errorf("a redirect stub needs only redirect: %v", err)
	}
}

func TestOrderingGroupsAndPager(t *testing.T) {
	s := newTestSite(t, map[string]string{
		"learn/b02-second.md":  fm("Second B", 2, "group: Track B, pravo\n"),
		"learn/a02-second.md":  fm("Second A", 2, "group: Track A, CUE from zero\n"),
		"learn/b01-first.md":   fm("First B", 1, "group: Track B, pravo\n"),
		"learn/a01-first.md":   fm("First A", 1, "group: Track A, CUE from zero\n"),
		"learn/cheat-sheet.md": fm("Cheat sheet", 1, ""),
		"learn/labs/lab-1.md":  fm("Lab", 1, "group: Labs\n"),
	})
	noErrors(t, s.msgs)
	sec := s.sections[0]
	var titles []string
	for _, p := range sec.Pages {
		titles = append(titles, p.FM.Title)
	}
	want := []string{"Learn", "Cheat sheet", "First A", "Second A", "First B", "Second B", "Lab"}
	if !reflect.DeepEqual(titles, want) {
		t.Errorf("order: %v\nwant:  %v", titles, want)
	}
	var groups []string
	for _, g := range sec.Groups {
		groups = append(groups, g.Name)
	}
	if !reflect.DeepEqual(groups, []string{"", "Track A, CUE from zero", "Track B, pravo", "Labs"}) {
		t.Errorf("groups: %q", groups)
	}
	// Previous and next follow that order across group boundaries.
	b1 := s.mustPage(t, "learn/b01-first.md")
	if b1.Prev.Rel != "a02-second.md" || b1.Next.Rel != "b02-second.md" {
		t.Errorf("pager of B1: %v %v", b1.Prev.Rel, b1.Next.Rel)
	}
	if sec.Pages[0].Prev != nil || sec.Pages[len(sec.Pages)-1].Next != nil {
		t.Errorf("the ends have no neighbour")
	}
	if b1.Lesson != "B1" || s.mustPage(t, "learn/a02-second.md").Lesson != "A2" || s.mustPage(t, "learn/cheat-sheet.md").Lesson != "" {
		t.Errorf("lesson ids wrong")
	}
	if got := b1.kicker(); got != "§ B1 · Track B" {
		t.Errorf("kicker %q", got)
	}
	if got := s.mustPage(t, "learn/labs/lab-1.md").kicker(); got != "§ Learn · Labs" {
		t.Errorf("kicker %q", got)
	}
	if got := s.mustPage(t, "learn/labs/lab-1.md").Out; got != "learn/labs/lab-1.html" {
		t.Errorf("nested page output %q", got)
	}
}

func TestConfiguredGroupOrder(t *testing.T) {
	cfg := strings.Replace(testSiteJSON, `{"id": "learn", "title": "Learn", "blurb": "Lessons.", "progress": true}`,
		`{"id": "learn", "title": "Learn", "blurb": "Lessons.", "groups": ["Zeta", "Alpha", "Unused"]}`, 1)
	s := newTestSite(t, map[string]string{
		"site/site.json": cfg,
		"learn/a.md":     fm("A", 1, "group: Alpha\n"),
		"learn/z.md":     fm("Z", 1, "group: Zeta\n"),
		"learn/m.md":     fm("M", 1, "group: Middle\n"),
	})
	var groups []string
	for _, g := range s.sections[0].Groups {
		groups = append(groups, g.Name)
	}
	if !reflect.DeepEqual(groups, []string{"", "Zeta", "Alpha", "Middle"}) {
		t.Errorf("groups: %q", groups)
	}
	if !strings.Contains(strings.Join(s.msgs.warns, "\n"), `group "Unused"`) {
		t.Errorf("want a warning for the unused group: %v", s.msgs.warns)
	}
}

func TestOrderErrors(t *testing.T) {
	s := newTestSite(t, map[string]string{
		"learn/a.md": fm("A", 1, "group: G\n"),
		"learn/b.md": fm("B", 1, "group: G\n"),
		"learn/c.md": "---\ntitle: C\ndescription: C.\n---\n",
		"learn/d.md": "No front matter\n",
		"learn/e.md": fm("E", 1, "bogus: 1\n"),
	})
	for _, want := range []string{"order 1 is also used", "missing order", "no front matter", "unknown key bogus"} {
		if !hasError(s.msgs, want) {
			t.Errorf("want an error containing %q: %v", want, s.msgs.errs)
		}
	}
	// A section without an index is an error.
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"site/site.json": testSiteJSON, "docs/learn/a.md": fm("A", 1, ""), "docs/reference/index.md": indexMD})
	cfg, _ := loadConfig(filepath.Join(dir, "site", "site.json"))
	s2 := &site{cfg: cfg, docsDir: filepath.Join(dir, "docs"), siteDir: filepath.Join(dir, "site"), msgs: &messages{}}
	s2.discover()
	s2.arrange()
	if !hasError(s2.msgs, "has no index.md") {
		t.Errorf("want a missing index error: %v", s2.msgs.errs)
	}
}

func TestRedirects(t *testing.T) {
	s := newTestSite(t, map[string]string{
		"learn/new.md":      fm("New", 1, ""),
		"learn/old.md":      "---\nredirect: new.md#part\n---\n",
		"learn/older.md":    "---\nredirect: old.md\n---\n",
		"learn/dangling.md": "---\nredirect: nowhere.md\n---\n",
		"learn/loop-a.md":   "---\nredirect: loop-b.md\n---\n",
		"learn/loop-b.md":   "---\nredirect: loop-a.md\n---\n",
	})
	if p := s.mustPage(t, "learn/older.md"); p.RedirOut != "learn/new.html" || p.RedirFrg != "part" {
		t.Errorf("chain: %q %q", p.RedirOut, p.RedirFrg)
	}
	if !hasError(s.msgs, "no page learn/nowhere.md") || !hasError(s.msgs, "redirect loop") {
		t.Errorf("errors: %v", s.msgs.errs)
	}
	// Stubs are not part of the reading order.
	for _, p := range s.sections[0].Pages {
		if p.isRedirect() {
			t.Errorf("stub %s in the reading order", p.Rel)
		}
	}
}

func TestRelURL(t *testing.T) {
	cases := []struct{ from, to, want string }{
		{"learn/a.html", "learn/b.html", "b.html"},
		{"learn/a.html", "reference/g.html", "../reference/g.html"},
		{"index.html", "learn/a.html", "learn/a.html"},
		{"learn/a.html", "index.html", "../index.html"},
		{"learn/sub/a.html", "learn/b.html", "../b.html"},
		{"learn/a.html", "learn/sub/b.html", "sub/b.html"},
		{"learn/sub/a.html", "reference/x/y.html", "../../reference/x/y.html"},
		{"learn/a.html", "assets/logo.svg", "../assets/logo.svg"},
		{"learn/a.html", "learn/a.html", "a.html"},
	}
	for _, c := range cases {
		if got := relURL(c.from, c.to); got != c.want {
			t.Errorf("relURL(%q, %q) = %q, want %q", c.from, c.to, got, c.want)
		}
	}
	if rootPrefix("learn/sub/a.html") != "../../" || rootPrefix("index.html") != "" {
		t.Errorf("rootPrefix")
	}
}

func TestConfigErrors(t *testing.T) {
	dir := t.TempDir()
	bad := map[string]string{
		`{"sections": []}`: "no sections",
		`{"sections": [{"id": "Learn", "title": "L"}]}`:                        "lowercase",
		`{"sections": [{"id": "a", "title": "A"}, {"id": "a", "title": "B"}]}`: "duplicate section id",
		`{"sections": [{"id": "a"}]}`:                                          "no title",
		`{"sections": [{"id": "a", "title": "A"}], "colour": 1}`:               "unknown field",
		`not json`: "invalid character",
	}
	for text, want := range bad {
		writeTree(t, dir, map[string]string{"c.json": text})
		_, err := loadConfig(filepath.Join(dir, "c.json"))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", text, err, want)
		}
	}
	writeTree(t, dir, map[string]string{"c.json": `{"sections": [{"id": "a", "title": "A", "nav": false}, {"id": "b", "title": "B"}]}`})
	c, err := loadConfig(filepath.Join(dir, "c.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Sections[0].inNav() || !c.Sections[1].inNav() || c.StartHref != "a/index.html" || c.Name != "pravo" {
		t.Errorf("defaults: %+v", c)
	}
	c.PravoVersion, c.CUEVersion = "0.1", "0.17"
	if c.applies() != "pravo 0.1 · CUE 0.17" {
		t.Errorf("applies %q", c.applies())
	}
}

func TestLessonMeta(t *testing.T) {
	s := newTestSite(t, map[string]string{
		"learn/a01-one.md": fm("One", 1, "minutes: 5\n"),
		"learn/a02-two.md": fm("Two", 2, "minutes: 7\nneeds: A1, and B9\nlearn: \"How `x` works?\"\ndone_when: \"`pravo eval` prints 3.\"\nlevel: advanced\n"),
	})
	noErrors(t, s.msgs)
	p := s.mustPage(t, "learn/a02-two.md")
	m := s.metaFor(p, &renderer{s: s, p: p})
	got := map[string]string{}
	for _, it := range m {
		got[it.Label] = string(it.Value)
	}
	if got["Time"] != "7 min" || got["Level"] != "advanced" {
		t.Errorf("meta %v", got)
	}
	if got["Needs"] != `<a href="a01-one.html" aria-label="A1, One">A1</a>, and B9` {
		t.Errorf("needs %q", got["Needs"])
	}
	if got["You will learn"] != "How <code>x</code> works?" || got["Done when"] != "<code>pravo eval</code> prints 3." {
		t.Errorf("meta %v", got)
	}
	// A page with no meta fields gets no list.
	p = s.mustPage(t, "learn/index.md")
	if len(s.metaFor(p, &renderer{s: s, p: p})) != 0 {
		t.Errorf("index has meta")
	}
}
