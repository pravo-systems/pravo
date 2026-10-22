package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPage(rel, body string) (*page, map[string]*page) {
	p := &page{Rel: rel, Section: strings.SplitN(rel, "/", 2)[0], Title: "A page", Desc: "What it says.", Body: body}
	return p, map[string]*page{rel: p, "reference/other.md": {Rel: "reference/other.md"}}
}

func convertOK(t *testing.T, rel, body string) string {
	t.Helper()
	p, inc := testPage(rel, body)
	out, err := convert(p, inc)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestFencesLoseTheirAttributesAndKeepTheirMeaning(t *testing.T) {
	got := convertOK(t, "how-to/x.md", "```cue file=root/main.cue title=\"root/main.cue\" hl=\"2\"\na: 1\n```\n\n```sh run\npravo plan\n```\n\n```text out\nPlan: 1 to add.\n```\n\n```cue status=conflict\nb: 1 & 2\n```\n")
	for _, want := range []string{"File `root/main.cue`:\n\n```cue\na: 1\n```", "```sh\npravo plan\n```", "Output:\n\n```text\nPlan: 1 to add.\n```", "Status: conflict.\n\n```cue\nb: 1 & 2\n```"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "title=") || strings.Contains(got, "hl=") {
		t.Errorf("attributes were left in:\n%s", got)
	}
}

func TestHiddenAndCheckBlocksAreDropped(t *testing.T) {
	got := convertOK(t, "how-to/x.md", "```sh run hidden\nsetup-only\n```\n\n```text out\nhidden output\n```\n\n```sh run check\ntest -f x\n```\n\nText.\n\n```sh run\nshown\n```\n\n```text out\nshown output\n```\n")
	if strings.Contains(got, "setup-only") || strings.Contains(got, "hidden output") || strings.Contains(got, "test -f x") {
		t.Errorf("a hidden block was kept:\n%s", got)
	}
	if !strings.Contains(got, "shown output") {
		t.Errorf("the output of a shown block was dropped:\n%s", got)
	}
}

func TestContainers(t *testing.T) {
	src := ":::predict What happens?\nGuess.\n::reveal\nIt fails.\n:::\n\nBefore.\n\n:::tabs Choose\n```cue title=\"A\"\na: 1\n```\n:::\n\n:::figure The graph\n<svg></svg>\n:::\n\n:::deep-dive Theory\nMore.\n:::\n\n:::demo blast\n:::\n"
	got := convertOK(t, "concepts/x.md", src)
	if strings.Contains(got, "Guess") || strings.Contains(got, "It fails") || strings.Contains(got, "<svg") || strings.Contains(got, ":::") {
		t.Errorf("lesson furniture or markup was kept:\n%s", got)
	}
	for _, want := range []string{"Before.", "```cue\na: 1\n```", "*Figure: The graph*", "**Theory**\n\nMore."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	p, inc := testPage("concepts/x.md", ":::novel thing\ntext\n:::\n")
	if _, err := convert(p, inc); err == nil || !strings.Contains(err.Error(), "novel") {
		t.Errorf("an unknown container must be an error: %v", err)
	}
	p, inc = testPage("concepts/x.md", "```cue weird=1\na: 1\n```\n")
	if _, err := convert(p, inc); err == nil {
		t.Error("an unknown fence attribute must be an error")
	}
	p, inc = testPage("concepts/x.md", "```cue\na: 1\n")
	if _, err := convert(p, inc); err == nil {
		t.Error("an open fence must be an error")
	}
}

func TestLinks(t *testing.T) {
	got := convertOK(t, "how-to/x.md", "See [other](../reference/other.md#a), [lesson](../learn/a03-x.md#b), [web](https://example.com/x), [self](#here) and ![a picture](../img/p.png).\n")
	for _, want := range []string{"[other](../reference/other.md#a)", "[lesson](https://pravo.systems/learn/a03-x.html#b)", "[web](https://example.com/x)", "[self](#here)", "*a picture*"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestParsePage(t *testing.T) {
	p, redirect, err := parsePage("reference/x.md", "reference", []byte("---\ntitle: The `x` page\ndescription: About [x](y.md).\norder: 3\ngroup: Lookup\n---\nBody\n"))
	if err != nil || redirect || p.Title != "The x page" || p.Desc != "About x." || p.Order != 3 || p.Group != "Lookup" || p.Body != "Body\n" {
		t.Fatalf("%+v %v %v", p, redirect, err)
	}
	if _, redirect, _ := parsePage("reference/y.md", "reference", []byte("---\ntitle: y\ndescription: z\nredirect: x.md\n---\n")); !redirect {
		t.Error("a redirect stub is not a page")
	}
	if _, _, err := parsePage("reference/z.md", "reference", []byte("no front matter")); err == nil {
		t.Error("a page needs front matter")
	}
}

// The skill in the repository is the one that the documentation gives. A page
// that changes, or a page that is added, makes this fail until
// "go run ./tools/skillgen" has been run and the result committed.
func TestTheSkillInTheRepositoryIsCurrent(t *testing.T) {
	files, err := build("../../docs", "../../skills/pravo")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 50 {
		t.Fatalf("the skill has %d reference files", len(files))
	}
	if problems := compare("../../skills/pravo", files); len(problems) > 0 {
		t.Fatalf("run go run ./tools/skillgen and commit the result:\n%s", strings.Join(problems, "\n"))
	}
	// No page is left out by accident: what is not in the skill matches a skip pattern.
	index := string(files["references/INDEX.md"])
	for _, must := range []string{"reference/plan.md", "concepts/plans.md", "how-to/quick-start.md", "migrate/m1-before-you-start.md", "learn/cue-cheat-sheet.md"} {
		if !strings.Contains(index, must) {
			t.Errorf("INDEX.md does not list %s", must)
		}
	}
	if strings.Contains(index, "learn/a03") {
		t.Error("track A is in the skill")
	}
}

func TestCompareFindsMissingStaleAndExtraFiles(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "references"), 0o755)
	os.WriteFile(filepath.Join(dir, "references", "a.md"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(dir, "references", "extra.md"), []byte("x"), 0o644)
	got := strings.Join(compare(dir, map[string][]byte{"references/a.md": []byte("new"), "references/b.md": []byte("b")}), "\n")
	for _, want := range []string{"references/a.md differs", "references/b.md is missing", "references/extra.md is not generated"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	n, err := write(dir, map[string][]byte{"references/a.md": []byte("new"), "references/b.md": []byte("b")})
	if err != nil || n != 1 {
		t.Fatalf("write: %d %v", n, err)
	}
	if left := compare(dir, map[string][]byte{"references/a.md": []byte("new"), "references/b.md": []byte("b")}); len(left) != 0 {
		t.Errorf("after write: %v", left)
	}
}

func TestSkipPatternThatMatchesNothingIsAnError(t *testing.T) {
	dir := t.TempDir()
	for _, s := range sections {
		os.MkdirAll(filepath.Join(dir, s.Dir), 0o755)
	}
	os.WriteFile(filepath.Join(dir, "reference", "x.md"), []byte("---\ntitle: x\ndescription: y\n---\n"), 0o644)
	if _, err := readPages(dir); err == nil || !strings.Contains(err.Error(), "matches no page") {
		t.Errorf("a stale skip pattern must be an error: %v", err)
	}
}

func TestCoreMustNameRealFilesAndAValidSkill(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) { os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(s), 0o644) }
	files := map[string][]byte{"references/reference/plan.md": nil}
	write("---\nname: pravo\ndescription: d\n---\nRead `references/reference/plan.md`.\n")
	if err := checkCore(dir, files); err != nil {
		t.Error(err)
	}
	write("---\nname: pravo\ndescription: d\n---\nRead `references/reference/gone.md`.\n")
	if err := checkCore(dir, files); err == nil {
		t.Error("a reference file that does not exist must be an error")
	}
	write("---\nname: other\ndescription: d\n---\n")
	if err := checkCore(dir, files); err == nil {
		t.Error("the name must be pravo")
	}
	write("---\nname: pravo\ndescription: " + strings.Repeat("x", 1025) + "\n---\n")
	if err := checkCore(dir, files); err == nil {
		t.Error("the description is at most 1024 characters")
	}
}

func TestZipIsStableAndHasTheSkillFolder(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: pravo\ndescription: d\n---\n"), 0o644)
	files := map[string][]byte{"references/INDEX.md": []byte("i")}
	a, b := filepath.Join(dir, "a.zip"), filepath.Join(dir, "b.zip")
	if err := writeZip(a, dir, "1.2.3", files); err != nil {
		t.Fatal(err)
	}
	if err := writeZip(b, dir, "1.2.3", files); err != nil {
		t.Fatal(err)
	}
	da, _ := os.ReadFile(a)
	db, _ := os.ReadFile(b)
	if !bytes.Equal(da, db) {
		t.Error("the same skill gave two different archives")
	}
	zr, err := zip.OpenReader(a)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "pravo/SKILL.md,pravo/VERSION,pravo/references/INDEX.md" {
		t.Errorf("entries %v", names)
	}
}
