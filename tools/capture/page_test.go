package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestTokenizeInfo(t *testing.T) {
	cases := []struct {
		info string
		want []attr
		err  string
	}{
		{"", nil, ""},
		{"cue", []attr{{Key: "cue"}}, ""},
		{`cue file=estate.cue title="estate.cue" hl="2-3,7"`, []attr{{Key: "cue"}, {"file", "estate.cue", true}, {"title", "estate.cue", true}, {"hl", "2-3,7", true}}, ""},
		{`sh run exit=2`, []attr{{Key: "sh"}, {Key: "run"}, {"exit", "2", true}}, ""},
		{`text title='a b' x`, []attr{{Key: "text"}, {"title", "a b", true}, {Key: "x"}}, ""},
		{`text title="say \"hi\""`, []attr{{Key: "text"}, {"title", `say "hi"`, true}}, ""},
		{`text title="open`, nil, "unterminated"},
		{`text =x`, nil, "without a name"},
	}
	for _, c := range cases {
		got, err := tokenizeInfo(c.info)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: error %v, want %q", c.info, err, c.err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %+v, %v; want %+v", c.info, got, err, c.want)
		}
	}
}

func TestBlockAttributes(t *testing.T) {
	cases := []struct {
		info string
		want block
	}{
		{"sh run", block{Lang: "sh", Run: true}},
		{"run", block{Run: true}},
		{"sh hidden run", block{Lang: "sh", Run: true, Hidden: true}},
		{"sh run exit=7", block{Lang: "sh", Run: true, Exit: 7, HasExit: true}},
		{"sh check", block{Lang: "sh", Check: true}},
		{"text out", block{Lang: "text", Out: true}},
		{"out", block{Out: true}},
		{`cue file="a b.cue" ln`, block{Lang: "cue", File: "a b.cue"}},
		{"cue title=x", block{Lang: "cue"}},
	}
	for _, c := range cases {
		var got block
		if err := parseInfoInto(&got, c.info); err != nil {
			t.Errorf("%q: %v", c.info, err)
			continue
		}
		got.Attrs = nil
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %+v, want %+v", c.info, got, c.want)
		}
	}
	for _, bad := range []string{"sh run=1", "sh exit=x", "sh exit=256", "sh exit=-1", "cue file=", "cue file"} {
		var b block
		if err := parseInfoInto(&b, bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func parse(src string) *page {
	return parsePage("t.md", []byte(src))
}

func errorLines(p *page) []string {
	var out []string
	for _, e := range p.Errors {
		out = append(out, fmt.Sprintf("%d %s", e.Line, e.Msg))
	}
	return out
}

func TestFences(t *testing.T) {
	p := parse(strings.Join([]string{
		"# Title",          // 1
		"",                 // 2
		"```sh run",        // 3
		"echo one",         // 4
		"```",              // 5
		"",                 // 6
		"~~~text out",      // 7
		"one",              // 8
		"~~~",              // 9
		"",                 // 10
		"````text",         // 11
		"```sh run",        // 12
		"inside",           // 13
		"```",              // 14
		"````",             // 15
		"",                 // 16
		"   ```sh run",     // 17: indented fence, content is dedented
		"   echo   three",  // 18
		"   ```",           // 19
		"",                 // 20
		"    ```sh run",    // 21: four spaces is indented code, not a fence
		"    echo no",      // 22
		"    ```",          // 23
		"```sh run hidden", // 24
		"echo four",        // 25
		"```",              // 26
	}, "\n") + "\n")
	if len(p.Errors) > 0 {
		t.Fatalf("errors: %v", errorLines(p))
	}
	var got []string
	for _, b := range p.Blocks {
		got = append(got, b.Lang+":"+strings.Join(b.Content, "/"))
	}
	want := []string{"sh:echo one", "text:one", "text:```sh run/inside/```", "sh:echo   three", "sh:echo four"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("blocks = %q, want %q", got, want)
	}
	if b := p.Blocks[0]; b.Open != 2 || b.Close != 4 || b.N != 1 {
		t.Errorf("first block = open %d close %d n %d", b.Open, b.Close, b.N)
	}
	if b := p.Blocks[1]; !b.Out || b.Owner != p.Blocks[0] || p.Blocks[0].Pair != b {
		t.Errorf("the tilde out block is not paired with the run block before it")
	}
	if b := p.Blocks[3]; b.Indent != 3 {
		t.Errorf("indent = %d, want 3", b.Indent)
	}
}

func TestUnclosedFence(t *testing.T) {
	p := parse("```sh run\necho hi\n")
	if got := errorLines(p); len(got) == 0 || !strings.Contains(got[0], "never closed") {
		t.Errorf("errors = %q", got)
	}
}

func TestContainersAndParts(t *testing.T) {
	p := parse(strings.Join([]string{
		":::exercise Title",
		"",
		"```sh run",
		"echo main",
		"```",
		"",
		"::hint",
		"",
		"```sh run",
		"echo hint",
		"```",
		"",
		"::solution",
		"",
		"```sh check",
		"echo solution",
		"```",
		"",
		":::",
		"",
		":::deep-dive Theory",
		"",
		"```sh run",
		"echo dive",
		"```",
		"",
		":::",
		"",
		":::predict Q?",
		"::reveal",
		"```sh run",
		"echo reveal",
		"```",
		":::",
		"",
		"```sh run",
		"echo after",
		"```",
	}, "\n") + "\n")
	if len(p.Errors) > 0 {
		t.Fatalf("errors: %v", errorLines(p))
	}
	type got struct {
		mode      mode
		container string
		part      string
	}
	var have []got
	for _, b := range p.Blocks {
		have = append(have, got{b.Mode, b.Container, b.Part})
	}
	want := []got{
		{modeMain, "exercise", ""},
		{modeIgnore, "exercise", "hint"},
		{modeSolution, "exercise", "solution"},
		{modeIsolated, "deep-dive", ""},
		{modeReveal, "predict", "reveal"},
		{modeMain, "", ""},
	}
	if !reflect.DeepEqual(have, want) {
		t.Errorf("modes = %+v, want %+v", have, want)
	}
	if p.Blocks[2].Scope == 0 || p.Blocks[2].Scope == p.Blocks[3].Scope {
		t.Errorf("solution and deep-dive share a scope: %d %d", p.Blocks[2].Scope, p.Blocks[3].Scope)
	}
	if p.Blocks[0].Scope != 0 || p.Blocks[5].Scope != 0 {
		t.Errorf("main blocks have a scope")
	}
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0].Msg, "::hint") {
		t.Errorf("warnings = %+v, want one for the hint", p.Warnings)
	}
	if p.Blocks[4].Scope == 0 || p.Blocks[4].Scope == p.Blocks[3].Scope || p.Blocks[4].Scope == p.Blocks[2].Scope {
		t.Errorf("the reveal does not have a scope of its own: %d", p.Blocks[4].Scope)
	}
}

func TestStructureErrors(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"unclosed container", ":::exercise X\ntext\n", "never closed"},
		{"stray close", "text\n:::\n", "closes a container"},
		{"nesting", ":::compare\n:::tabs\n:::\n:::\n", "do not nest"},
		{"part outside", "::solution\n", "outside a container"},
		{"unknown part", ":::exercise X\n::nope\n:::\n", "unknown part ::nope"},
		{"orphan out", "```text out\nx\n```\n", "does not follow a run block"},
		{"out after prose", "```sh run\nx\n```\n\ntext\n\n```text out\nx\n```\n", "does not follow a run block"},
		{"two outs", "```sh run\nx\n```\n```text out\nx\n```\n```text out\nx\n```\n", "does not follow a run block"},
		{"out across part", ":::exercise X\n```sh run\nx\n```\n::solution\n```text out\nx\n```\n:::\n", "does not follow a run block"},
		{"prompt", "```sh run\n$ pravo eval\n```\n", "without the '$ ' prompt"},
		{"empty run", "```sh run\n\n```\n", "no commands"},
		{"file escape", "```cue file=../x\n```\n", "relative path"},
		{"file absolute", "```cue file=/etc/x\n```\n", "relative path"},
		{"file and run", "```sh file=a run\n```\n", "cannot be combined"},
		{"run and out", "```sh run out\nx\n```\n", "both a command"},
		{"exit on file", "```cue file=a exit=1\n```\n", "belongs on a run block"},
		{"check exit", "```sh check exit=1\nx\n```\n", "must exit 0"},
		{"callout", "> ```sh run\n> echo\n> ```\n", "inside a callout"},
		{"bad quote", "```sh title=\"x\n```\n", "unterminated"},
		{"front matter", "---\ntitle: x\n", "not closed"},
		{"bad regexp", "---\ntitle: x\nnormalize:\n  - pattern: \"(\"\n---\n", "normalize entry 1"},
		{"bad key", "---\ntitle: x\nnormalize:\n  - pattern: a\n    with: b\n---\n", "unknown key"},
		{"not a list", "---\ntitle: x\nnormalize: nope\n---\n", "must be a list"},
	}
	for _, c := range cases {
		p := parse(c.src)
		found := false
		for _, e := range p.Errors {
			if strings.Contains(e.Msg, c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: errors %v do not mention %q", c.name, errorLines(p), c.want)
		}
	}
}

func TestNoErrorsForLegalPages(t *testing.T) {
	cases := []string{
		"text with ::before and `:::` in it\n",
		"| `:::compare` | a table cell |\n",
		"```sh run\necho hi\n```\n```text out\nhi\n```\n",
		"```sh run\necho hi\n```\n\n\n\n```text out\nhi\n```\n",
		"> [!NOTE]\n> ```sh\n> echo not run\n> ```\n",
		":::widget unify\n{\"a\": 1}\n:::\n",
		":::check\nIs it?\n\n::answer\nYes.\n:::\n",
	}
	for _, src := range cases {
		if p := parse(src); len(p.Errors) > 0 {
			t.Errorf("%q: %v", src, errorLines(p))
		}
	}
}

func TestFrontMatter(t *testing.T) {
	p := parse("---\ntitle: Writing a field twice          # required\ncapture: false\n---\n```sh run\n```\n")
	if p.FM.Capture || p.FM.Title != "Writing a field twice" {
		t.Errorf("front matter = %+v", p.FM)
	}
	if len(p.Blocks) != 0 {
		t.Errorf("a page with capture: false was scanned")
	}

	p = parse("---\ntitle: T\nnormalize:\n  - pattern: \"id=[a-z-]+\"\n    replace: \"id=<id>\"\n  - pattern: 'x+'\n---\n\n```sh run\ntrue\n```\n")
	if len(p.Errors) > 0 {
		t.Fatalf("errors: %v", errorLines(p))
	}
	if len(p.FM.Normalize) != 2 || p.FM.Normalize[0].Replace != "id=<id>" || p.FM.Normalize[1].Replace != "" {
		t.Errorf("rules = %+v", p.FM.Normalize)
	}
	if len(p.Blocks) != 1 || p.Blocks[0].Open != 8 {
		t.Errorf("blocks = %+v; line numbers must count the front matter", p.Blocks)
	}

	for _, v := range []string{"false", "no", "'false'", `"off"`} {
		if p := parse("---\ntitle: T\ncapture: " + v + "\n---\n"); p.FM.Capture {
			t.Errorf("capture: %s still captures", v)
		}
	}
	if p := parse("---\ntitle: T\ncapture: true\n---\n"); !p.FM.Capture {
		t.Errorf("capture: true does not capture")
	}
	if p := parse("no front matter\n"); !p.FM.Capture {
		t.Errorf("a page without front matter is not captured")
	}
	if p := parse("\xef\xbb\xbf---\ntitle: T\n---\n```sh run\ntrue\n```\n"); len(p.Blocks) != 1 || !p.BOM {
		t.Errorf("a byte order mark hides the front matter: %+v", p.Blocks)
	}
}

func TestRunnable(t *testing.T) {
	if parse("```cue file=a.cue\nx: 1\n```\n").runnable() {
		t.Errorf("a page with only files is runnable")
	}
	if !parse("```sh run\ntrue\n```\n").runnable() {
		t.Errorf("a page with a run block is not runnable")
	}
	if parse(":::exercise X\n::hint\n```sh run\ntrue\n```\n:::\n").runnable() {
		t.Errorf("a page with a run block only in a hint is runnable")
	}
}

func TestHiddenAloneIsAHiddenCommand(t *testing.T) {
	p := parsePage("x", []byte("```sh hidden\necho setup\n```\n\n```cue file=a.cue hidden\npackage a\n```\n"))
	if len(p.Errors) > 0 {
		t.Fatalf("errors: %v", errorLines(p))
	}
	if !p.Blocks[0].isCommand() || !p.Blocks[0].Hidden {
		t.Errorf("sh hidden is not a hidden command: %+v", p.Blocks[0])
	}
	if p.Blocks[1].isCommand() || p.Blocks[1].File != "a.cue" {
		t.Errorf("a hidden file block became a command: %+v", p.Blocks[1])
	}
}
