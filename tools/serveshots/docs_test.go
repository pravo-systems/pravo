package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var (
	shotRefRe = regexp.MustCompile(`img/serve/([A-Za-z0-9._-]+)\.png`)
	imgTagRe  = regexp.MustCompile(`<img[^>]*src="[^"]*img/serve/([A-Za-z0-9._-]+)\.png"[^>]*>`)
	sizeRe    = regexp.MustCompile(`\b(width|height)="(\d+)"`)
)

// repoRoot is the module root, two directories above this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skip("not inside the pravo module:", err)
	}
	return root
}

// docFiles returns README.md and every Markdown page below docs/.
func docFiles(t *testing.T, root string) []string {
	t.Helper()
	files := []string{filepath.Join(root, "README.md")}
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// Every picture that a page links to is a shot, and every shot is linked from
// a page, so that the program and the documentation agree.
func TestDocumentationLinksExactlyTheShots(t *testing.T) {
	root := repoRoot(t)
	linked := map[string]bool{}
	for _, f := range docFiles(t, root) {
		if strings.HasSuffix(f, "AUTHORING.md") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range shotRefRe.FindAllStringSubmatch(string(data), -1) {
			linked[m[1]] = true
		}
	}
	shots := map[string]bool{}
	for _, s := range allShots() {
		shots[s.Name] = true
		if !linked[s.Name] {
			t.Errorf("no page links img/serve/%s.png", s.Name)
		}
	}
	for name := range linked {
		if !shots[name] {
			t.Errorf("a page links img/serve/%s.png, which is not a shot", name)
		}
	}
}

// docs/img/serve holds one PNG for each shot and nothing else.
func TestImageDirectoryHoldsTheShots(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "docs", "img", "serve")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var have, want []string
	for _, e := range entries {
		have = append(have, e.Name())
	}
	for _, s := range allShots() {
		want = append(want, s.File())
	}
	sort.Strings(have)
	sort.Strings(want)
	if strings.Join(have, " ") != strings.Join(want, " ") {
		t.Errorf("docs/img/serve holds\n  %s\nwant\n  %s", strings.Join(have, " "), strings.Join(want, " "))
	}
}

// An img tag of a page states the size of the viewport of its shot, so that
// the page does not shift when the picture loads. The picture itself is twice
// as large.
func TestImageTagsStateTheViewportSize(t *testing.T) {
	root := repoRoot(t)
	sizes := map[string][2]int{}
	for _, s := range allShots() {
		sizes[s.Name] = [2]int{s.Width, s.Height}
	}
	tags := 0
	for _, f := range docFiles(t, root) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range imgTagRe.FindAllStringSubmatch(string(data), -1) {
			tags++
			want, ok := sizes[m[1]]
			if !ok {
				continue
			}
			got := map[string]int{}
			for _, a := range sizeRe.FindAllStringSubmatch(m[0], -1) {
				got[a[1]], _ = strconv.Atoi(a[2])
			}
			if got["width"] != want[0] || got["height"] != want[1] {
				rel, _ := filepath.Rel(root, f)
				t.Errorf("%s: %s.png has width=%d height=%d, the shot is %dx%d", rel, m[1], got["width"], got["height"], want[0], want[1])
			}
			if !strings.Contains(m[0], `alt="`) || strings.Contains(m[0], `alt=""`) {
				t.Errorf("%s.png has no alt text", m[1])
			}
		}
	}
	if tags == 0 {
		t.Error("no page shows a shot with an img tag")
	}
}

var listingRe = regexp.MustCompile("(?ms)^```[a-z]* [^\n]*title=\"((?:platform|fleet)/[^\"]+)\"[^\n]*\n(.*?)^```")

// A listing of a page that is titled platform/FILE or fleet/FILE shows lines
// of that file of the scenario. A line that holds only three dots stands for
// lines that the listing leaves out, so every run of lines between them has to
// occur in the file.
func TestListingsShowTheScenarioFiles(t *testing.T) {
	root := repoRoot(t)
	found := 0
	for _, f := range docFiles(t, root) {
		// Other pages have a root called platform of their own.
		if !strings.Contains(f, "explore-your-estate") && !strings.Contains(f, "the-viewer") && !strings.HasSuffix(f, "serve.md") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range listingRe.FindAllStringSubmatch(string(data), -1) {
			found++
			scenario, file, _ := strings.Cut(m[1], "/")
			src, err := scenarioFS.ReadFile("scenarios/" + scenario + "/" + file)
			if err != nil {
				t.Errorf("%s: the listing %s: %v", filepath.Base(f), m[1], err)
				continue
			}
			var chunk []string
			flush := func() {
				if len(chunk) == 0 {
					return
				}
				if !strings.Contains(string(src), strings.Join(chunk, "\n")) {
					t.Errorf("%s: the listing %s has lines that %s does not have:\n%s", filepath.Base(f), m[1], file, strings.Join(chunk, "\n"))
				}
				chunk = nil
			}
			for _, line := range strings.Split(strings.TrimRight(m[2], "\n"), "\n") {
				if strings.TrimSpace(line) == "..." {
					flush()
					continue
				}
				chunk = append(chunk, line)
			}
			flush()
		}
	}
	if found == 0 {
		t.Error("no page lists a file of a scenario")
	}
}

// The concept page states the number of resources at which the viewer draws a
// cluster. It reads the constant from the interface, so that a change to one
// is a change to the other.
func TestClusterThresholdAgreesWithTheInterface(t *testing.T) {
	root := repoRoot(t)
	js, err := os.ReadFile(filepath.Join(root, "internal", "serve", "web", "assets", "model.js"))
	if err != nil {
		t.Skip("the interface is not in the tree:", err)
	}
	m := regexp.MustCompile(`export const CLUSTER_MIN = (\d+);`).FindSubmatch(js)
	if m == nil {
		t.Fatal("model.js does not define CLUSTER_MIN")
	}
	words := map[string]string{"2": "two", "3": "three", "4": "four", "5": "five", "6": "six", "7": "seven", "8": "eight", "9": "nine", "10": "ten", "12": "twelve"}
	word, ok := words[string(m[1])]
	if !ok {
		t.Fatalf("CLUSTER_MIN is %s: add its word to this test", m[1])
	}
	page, err := os.ReadFile(filepath.Join(root, "docs", "concepts", "the-viewer.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "holds "+word+" or more resources") {
		t.Errorf("the-viewer.md does not say that a namespace with %s or more resources of one type is drawn as a cluster", word)
	}
}

// A shot that comes as a dark and a light picture is linked in one figure with
// the classes shot-dark and shot-light, which the style sheet of the site uses
// to show the picture that matches the theme of the page.
func TestPairedShotsAreLinkedWithTheThemeClasses(t *testing.T) {
	root := repoRoot(t)
	var text strings.Builder
	for _, f := range docFiles(t, root) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		text.Write(data)
	}
	all := text.String()
	have := map[string]bool{}
	for _, s := range allShots() {
		have[s.Name] = true
	}
	pairs := 0
	for _, s := range allShots() {
		base, ok := strings.CutSuffix(s.Name, "-dark")
		if !ok || !have[base+"-light"] {
			continue
		}
		pairs++
		for _, theme := range []string{"dark", "light"} {
			want := `class="shot shot-` + theme + `" href="../img/serve/` + base + "-" + theme + `.png"`
			if !strings.Contains(all, want) {
				t.Errorf("no page links %s-%s.png with %s", base, theme, want)
			}
		}
	}
	if pairs == 0 {
		t.Error("no pair of dark and light shots")
	}
}

var (
	chipRe = regexp.MustCompile(`\{ key: (?:null|'(\w+)'), label: '([^']+)'`)
	tabRe  = regexp.MustCompile(`\{ key: '(\w+)', label: '([^']+)' \}`)
)

// The reference names the chips of the sidebar and the tabs of the inspector as
// the interface does, with the parameter value that each sets.
func TestReferenceNamesTheChipsAndTabsOfTheInterface(t *testing.T) {
	root := repoRoot(t)
	page, err := os.ReadFile(filepath.Join(root, "docs", "reference", "serve.md"))
	if err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(root, "internal", "serve", "web", "assets")
	check := func(file string, re *regexp.Regexp, column string, what string) {
		src, err := os.ReadFile(filepath.Join(web, file))
		if err != nil {
			t.Skip("the interface is not in the tree:", err)
		}
		ms := re.FindAllStringSubmatch(string(src), -1)
		if len(ms) == 0 {
			t.Fatalf("%s: no %s found", file, what)
		}
		for _, m := range ms {
			if !strings.Contains(string(page), "| "+m[2]+" |") {
				t.Errorf("serve.md has no table row for the %s %q of %s", what, m[2], file)
			}
			if m[1] != "" && !strings.Contains(string(page), "`"+m[1]+"`") {
				t.Errorf("serve.md does not name the %s value `%s` of the %s %q", column, m[1], what, m[2])
			}
		}
	}
	check("chips.js", chipRe, "filter", "chip")
	check("inspector.js", tabRe, "tab", "tab")
}
