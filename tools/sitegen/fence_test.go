package main

import (
	"strings"
	"testing"
)

func TestParseFenceInfo(t *testing.T) {
	fi, err := parseFenceInfo(`cue file=estate.cue title="Estate file" hl="2-3,7" ln status=conflict`)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Lang != "cue" || fi.File != "estate.cue" || fi.Title != "Estate file" || !fi.LN || fi.Status != "conflict" {
		t.Errorf("parsed %+v", fi)
	}
	for _, n := range []int{2, 3, 7} {
		if !fi.HL[n] {
			t.Errorf("line %d not highlighted", n)
		}
	}
	if fi.HL[4] {
		t.Errorf("line 4 highlighted")
	}

	fi, err = parseFenceInfo(`sh run exit=1`)
	if err != nil || !fi.Run || fi.Exit != 1 || fi.Lang != "sh" {
		t.Errorf("run block: %+v %v", fi, err)
	}
	fi, err = parseFenceInfo(`out`)
	if err != nil || !fi.Out || fi.Lang != "text" {
		t.Errorf("bare out: %+v %v", fi, err)
	}
	fi, err = parseFenceInfo(`run`)
	if err != nil || !fi.Run || fi.Lang != "bash" {
		t.Errorf("bare run: %+v %v", fi, err)
	}
	fi, err = parseFenceInfo(`sh run hidden`)
	if err != nil || !fi.Hidden {
		t.Errorf("hidden: %+v %v", fi, err)
	}
	fi, err = parseFenceInfo(`sh run check`)
	if err != nil || !fi.Check {
		t.Errorf("check: %+v %v", fi, err)
	}
	fi, err = parseFenceInfo(`cue title='single quotes'`)
	if err != nil || fi.Title != "single quotes" {
		t.Errorf("single quotes: %+v %v", fi, err)
	}

	bad := map[string]string{
		`cue colour=red`:       "unknown code attribute",
		`cue status=fine`:      "status",
		`cue hl="a-b"`:         "hl",
		`cue hl="3-2"`:         "hl",
		`sh run out`:           "either run or out",
		`cue check`:            "check belongs on a run block",
		`cue exit=1`:           "exit belongs on a run block",
		`sh run exit=x`:        "exit",
		`cue title="open`:      "unterminated",
		`sh run file=a.cue`:    "cannot also have file",
		`cue ln=3`:             "takes no value",
		`cue file=`:            "file needs a name",
		`sh run exit=300`:      "exit",
		`cue hl="0"`:           "hl",
		`cue status=conflict2`: "status",
	}
	for info, want := range bad {
		_, err := parseFenceInfo(info)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want %q", info, err, want)
		}
	}
}

func TestRenderCode(t *testing.T) {
	fi, _ := parseFenceInfo(`cue file=main.cue hl="2" ln status=rejected`)
	out, err := renderCode(fi, "a: 1\nb: \"x\"\n")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out,
		`<figure class="code" data-lang="cue" data-status="rejected" data-ln>`,
		`<span class="code-file">main.cue</span>`,
		`<span class="code-badge" title="Rejected before any provider starts">rejected</span>`,
		`<span class="line"><span class="ln"></span><span class="tok-id">a</span>: <span class="tok-num">1</span>`,
		`<span class="line hl"><span class="ln"></span><span class="tok-id">b</span>`,
		`<button class="copy" type="button" aria-label="Copy main.cue"></button>`,
	)
	if strings.Count(out, `class="line`) != 2 {
		t.Errorf("want two lines:\n%s", out)
	}

	// No title and no status: no caption.
	fi, _ = parseFenceInfo(`text`)
	out, _ = renderCode(fi, "plain <b>")
	mustNotContain(t, out, "figcaption")
	mustContain(t, out, "plain &lt;b&gt;")

	// Highlight beyond the end of the block is an error.
	fi, _ = parseFenceInfo(`cue hl="5"`)
	if _, err := renderCode(fi, "a: 1"); err == nil || !strings.Contains(err.Error(), "past the end") {
		t.Errorf("hl past the end: %v", err)
	}

	// Diff lines carry ins and del.
	fi, _ = parseFenceInfo(`diff`)
	out, _ = renderCode(fi, "-old\n+new\n context")
	mustContain(t, out, `class="line del"`, `class="line ins"`)
}

func TestCommands(t *testing.T) {
	got := commands("pravo plan \\\n  -t env=prod\n\n# note\npravo apply\n")
	want := []string{"pravo plan \\\n  -t env=prod", "# note", "pravo apply"}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("command %d: %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRenderTerminal(t *testing.T) {
	fi, _ := parseFenceInfo(`sh run exit=1 status=conflict`)
	out := "Error: evaluating .:\nsize: conflicting values 4 and 3:\n    ./main.cue:3:7\n"
	html := renderTerminal(fi, "pravo eval\npravo version\n", &out, "0.1")
	mustContain(t, html,
		`<figure class="term" data-exit="1" data-status="conflict" data-ran="0.1">`,
		`<span class="code-file">terminal</span>`,
		`<span class="code-badge" data-exit="1" title="Exit code">exit 1</span>`,
		`<div class="term-line cmd"><span class="prompt">$</span><span class="cmd-text">pravo eval</span></div>`,
		`<span class="cmd-text">pravo version</span>`,
		`<pre class="term-out"><code><span class="out-err">Error: evaluating .:</span>`,
		`<span class="out-dim">    ./main.cue:3:7</span>`,
		`data-copy="pravo eval&#10;pravo version"`,
	)
	// No output block: no <pre>.
	fi, _ = parseFenceInfo(`sh run`)
	html = renderTerminal(fi, "pravo version", nil, "")
	mustNotContain(t, html, "term-out", "data-ran")
	mustContain(t, html, `data-exit="0"`)
}

func TestColorizeOutput(t *testing.T) {
	plan := "pravo will perform the following actions:\n\n  # pet (random_pet) will be created\n  + pet: {\n  +     id: (known after apply)\n    }\n\n  # hello must be replaced\n-/+ hello: {\n  ~     content: \"a\" -> \"b\" # forces replacement\n  -     old: 1\n    }\n\nPlan: 1 to add, 1 to change, 1 to destroy.\nGraph: planned 2 of 340, 338 skipped.\n"
	got := colorizeOutput(plan)
	mustContain(t, got,
		`<span class="out-dim">  # pet (random_pet) will be created</span>`,
		`<span class="out-add">  + pet: {</span>`,
		`<span class="out-dim">(known after apply)</span>`,
		`<span class="out-change">-/+ hello: {</span>`,
		`<span class="out-change"># forces replacement</span>`,
		`<span class="out-destroy">  -     old: 1</span>`,
		`Plan: <span class="out-add">1 to add</span>, <span class="out-change">1 to change</span>, <span class="out-destroy">1 to destroy</span>.`,
	)
	if strings.Contains(got, "Graph: <span") {
		t.Errorf("Graph line should stay plain:\n%s", got)
	}

	// Without plan markers, list bullets are left alone.
	list := "- one\n- two\n+ three\n"
	if strings.Contains(colorizeOutput(list), "out-") {
		t.Errorf("a list is not a plan:\n%s", colorizeOutput(list))
	}

	// Errors and their locations; a blank line ends the error.
	err := "Error: evaluating /x:\nsize: invalid value 11 (out of bound <=10):\n    /x/main.cue:3:19\n\nnext line\n"
	got = colorizeOutput(err)
	mustContain(t, got, `<span class="out-err">size: invalid value 11 (out of bound &lt;=10):</span>`, `<span class="out-dim">    /x/main.cue:3:19</span>`)
	if !strings.HasSuffix(got, "\nnext line") {
		t.Errorf("text after the error should be plain:\n%s", got)
	}

	// ANSI sequences are dropped; escaping is applied.
	got = colorizeOutput("\x1b[31mred\x1b[0m <b>\n")
	if got != "red &lt;b&gt;" {
		t.Errorf("got %q", got)
	}
	if colorizeOutput("\n\n") != "" {
		t.Errorf("empty output should render empty")
	}
}
