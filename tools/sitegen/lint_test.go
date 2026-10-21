package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBannedList(t *testing.T) {
	rules, err := loadBanned("")
	if err != nil {
		t.Fatal(err)
	}
	match := func(line string) bool {
		for _, r := range rules {
			if r.re.MatchString(line) {
				return true
			}
		}
		return false
	}
	banned := []string{
		"pravo has no workspaces by decision.",
		"This is ON PURPOSE.",
		"It deliberately omits the field.",
		"We chose a lattice.", "We decided against it.",
		"The flag is no longer available.",
		"Previously the plan was printed twice.",
		"Formerly known as x.", "Originally a script.", "Historically, yes.",
		"The reasoning is in the notes.", "Why pravo exists.",
		"Use this instead of the old command.",
		"Unlike Terraform, pravo checks first.",
		"Cueform wrote this.", "See State" + "graph.",
		"The norms are enforced.", "Write two norms.",
	}
	for _, line := range banned {
		if !match(line) {
			t.Errorf("not matched: %q", line)
		}
	}
	allowed := []string{
		"The normal case.", "pravo normally prints the plan.", "pravo normalises paths.", "Normalise the output.",
		"A decision table.", "The purpose of the field.", "The state is long-lived.", "A longer name.",
		"Compared with the previous plan.", "The first original.", "A reasonable default.",
	}
	for _, line := range allowed {
		if match(line) {
			t.Errorf("matched but allowed: %q", line)
		}
	}
}

func TestParseBanned(t *testing.T) {
	rules, err := parseBanned("# comment\n\n  \\bfoo\\b  \nbar+\n", "x")
	if err != nil || len(rules) != 2 || rules[0].pattern != `\bfoo\b` {
		t.Errorf("rules %v err %v", rules, err)
	}
	if _, err := parseBanned("(unclosed\n", "banned.txt"); err == nil || !strings.Contains(err.Error(), "banned.txt:1") {
		t.Errorf("a bad pattern names its line: %v", err)
	}
}

func TestLintSkipsFencedCodeAndAuthoring(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"docs/learn/a.md":        "---\ntitle: A\n---\n\nFine line.\nIt is no longer here.\n\n```text\nno longer in code\n```\n\n~~~\npreviously in code\n~~~\n\nAfter the fences, unlike this.\n",
		"docs/AUTHORING.md":      "The banned phrase no longer appears here.\n",
		"site/landing/01-x.html": "<section>\n<p>deliberately written</p>\n<pre>\nno longer in pre\n</pre>\n<script>var s = 'previously';</script>\n</section>\n",
		"site/site.json":         "{\"blurb\": \"on purpose\"}\n",
		"site/assets/x.css":      "/* no longer */\n",
		"README.md":              "Root text.\nformerly that.\n",
	})
	rules, _ := loadBanned("")
	msgs := lintRoots(rules, nil, filepath.Join(dir, "docs"), filepath.Join(dir, "site"), filepath.Join(dir, "README.md"))
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{"a.md:6:", "a.md:16:", "01-x.html:2:", "site.json:1:", "README.md:2:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("want a message for %s in:\n%s", want, joined)
		}
	}
	for _, unwanted := range []string{"a.md:9:", "a.md:13:", "AUTHORING", "01-x.html:4:", "01-x.html:6:", "x.css"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("unexpected message %s in:\n%s", unwanted, joined)
		}
	}
	if len(msgs) != 5 {
		t.Errorf("want 5 messages, got %d:\n%s", len(msgs), joined)
	}
	if !strings.Contains(joined, `banned phrase "no longer" (rule \bno longer\b)`) {
		t.Errorf("message format:\n%s", joined)
	}
}

func TestLintSkip(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"site/design/preview.html": "<p>no longer</p>\n",
		"site/index.html":          "<p>no longer</p>\n",
		"site/other.html":          "<p>no longer</p>\n",
	})
	rules, _ := loadBanned("")
	skip := []string{filepath.Join(dir, "site", "design"), filepath.Join(dir, "site", "index.html")}
	msgs := lintRoots(rules, skip, filepath.Join(dir, "site"))
	if len(msgs) != 1 || !strings.Contains(msgs[0], "other.html") {
		t.Errorf("messages: %v", msgs)
	}
}

func TestBuildFailsOnBannedPhrase(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"site/site.json":          testSiteJSON,
		"site/assets/logo.svg":    "<svg/>",
		"site/assets/style.css":   "",
		"site/assets/site.js":     "",
		"docs/learn/index.md":     "---\ntitle: Learn\ndescription: Lessons.\n---\n\nThis was done deliberately.\n",
		"docs/reference/index.md": indexMD,
	})
	o := options{docs: filepath.Join(dir, "docs"), site: filepath.Join(dir, "site"), out: filepath.Join(dir, "out"), readme: filepath.Join(dir, "none.md"), lint: true, check: true}
	err := run(o)
	if err == nil || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("want a failed build, got %v", err)
	}
	if _, serr := filepath.Glob(filepath.Join(dir, "out", "*")); serr != nil {
		t.Fatal(serr)
	}
	o.lint = false
	if err := run(o); err != nil {
		t.Fatalf("with -lint=false the build passes: %v", err)
	}
}
