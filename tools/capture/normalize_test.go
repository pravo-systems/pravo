package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func lines(s string) []string { return trimLines(strings.Split(s, "\n")) }

func TestTrimLines(t *testing.T) {
	got := trimLines([]string{"", "", "a  ", "\tb\t", "", "c\r", "", ""})
	if want := []string{"a", "\tb", "", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := trimLines([]string{"", " "}); got != nil {
		t.Errorf("blank input gave %q", got)
	}
}

func TestReplacePaths(t *testing.T) {
	n := &normalizer{}
	n.add("/work/ws", ".")
	n.add("/work", "<tmp>")
	n.add("/home/ann", "~")
	cases := []struct{ in, want string }{
		{"evaluating /work/ws:", "evaluating .:"},
		{"/work/ws/main.cue:3:7", "./main.cue:3:7"},
		{"  at /work/ws/sub/x.cue", "  at ./sub/x.cue"},
		{"cache /home/ann/.pravo/cache", "cache ~/.pravo/cache"},
		{"scratch /work/other/file", "scratch <tmp>/other/file"},
		{"in (/work/ws).", "in (.)."},
		{`path="/work/ws"`, `path="."`},
		// Not whole paths.
		{"/work/ws2/main.cue", "<tmp>/ws2/main.cue"},
		{"/work/ws-old", "<tmp>/ws-old"},
		{"/workspace/x", "/workspace/x"},
		{"/home/annabel/x", "/home/annabel/x"},
		{"a/work/ws/x", "a/work/ws/x"},
		{"x/home/ann", "x/home/ann"},
		{"no slash at all", "no slash at all"},
	}
	for _, c := range cases {
		if got := n.replacePaths(c.in); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizerResolvesSymlinks(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	link := filepath.Join(base, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	n := &normalizer{}
	n.addWorkspace(link)
	// pravo may print the resolved spelling of a directory it was given by
	// a link, as it does for /var and /private/var on macOS.
	got := n.lines([]byte("a " + link + "/x\nb " + realPath(real) + "/y\n"))
	if want := []string{"a ./x", "b ./y"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalizerLines(t *testing.T) {
	n := newNormalizer("/home/ann", []string{"/tmp"}, []rule{
		{Pattern: `id=[a-z-]+`, Replace: "id=<id>", re: regexp.MustCompile(`id=[a-z-]+`)},
		{Pattern: `(?m)^Downloading .*\n`, Replace: "", re: regexp.MustCompile(`(?m)^Downloading .*\n`)},
	})
	n.addWorkspace("/tmp/capture-1/ws")
	raw := "\x1b[1mDownloading hashicorp/random\x1b[0m\r\n" +
		"\x1b]0;title\x07created id=cool-tiger at /tmp/capture-1/ws/main.cue   \r\n" +
		"\n" +
		"in /home/ann/.pravo and /tmp/other  \n\n\n"
	want := []string{"created id=<id> at ./main.cue", "", "in ~/.pravo and <tmp>/other"}
	if got := n.lines([]byte(raw)); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	// Normalising a second time changes nothing.
	again := n.lines([]byte(strings.Join(want, "\n")))
	if !reflect.DeepEqual(again, want) {
		t.Errorf("not idempotent: %q", again)
	}
}

func TestSmallHomeIsNotReplaced(t *testing.T) {
	n := newNormalizer("/", []string{"/"}, nil)
	if got := n.lines([]byte("/usr/bin/x\n")); !reflect.DeepEqual(got, []string{"/usr/bin/x"}) {
		t.Errorf("got %q", got)
	}
}

func TestMatchElided(t *testing.T) {
	cases := []struct {
		name     string
		exp, act string
		ok       bool
		msg      string
	}{
		{"equal", "a\nb", "a\nb", true, ""},
		{"empty", "", "", true, ""},
		{"differs", "a\nb", "a\nc", false, "output line 2 differs"},
		{"short output", "a\nb", "a", false, "ends after line 1"},
		{"long output", "a", "a\nb", false, "more lines than the out block"},
		{"middle elided", "a\n...\nz", "a\nb\nc\nz", true, ""},
		{"nothing elided", "a\n...\nz", "a\nz", true, ""},
		{"head only", "a\nb\n...", "a\nb\nc", true, ""},
		{"head only, wrong", "a\nb\n...", "a\nx\nc", false, "output line 2 differs"},
		{"tail only", "...\ny\nz", "a\nb\ny\nz", true, ""},
		{"tail wrong", "a\n...\ny\nz", "a\nb\nx\nz", false, "output line 3 differs"},
		{"too short", "a\nb\n...\ny\nz", "a\nz", false, "needs at least 4"},
		{"two elisions", "a\n...\nm\n...\nz", "a\nx\nm\ny\nz", true, ""},
		{"middle segment", "a\n...\nm\nn\n...\nz", "a\nx\nm\nn\ny\nz", true, ""},
		{"middle segment out of order", "a\n...\nn\nm\n...\nz", "a\nx\nm\nn\ny\nz", false, "not found"},
		{"middle segment missing", "a\n...\nq\n...\nz", "a\nx\ny\nz", false, "lines 3 to 3"},
		{"middle may not overlap the tail", "a\n...\nz\n...\nz", "a\nz", false, "not found"},
		{"two elisions in a row", "a\n...\n...\nz", "a\nb\nz", true, ""},
	}
	for _, c := range cases {
		mm := matchElided(lines(c.exp), lines(c.act))
		switch {
		case c.ok && mm != nil:
			t.Errorf("%s: unexpected mismatch: %s", c.name, mm.Msg)
		case !c.ok && mm == nil:
			t.Errorf("%s: no mismatch", c.name)
		case !c.ok && !strings.Contains(mm.Msg, c.msg):
			t.Errorf("%s: message %q does not contain %q", c.name, mm.Msg, c.msg)
		}
	}
}

func TestRewriteKeepsTheElision(t *testing.T) {
	long := lines("1\n2\n3\n4\n5\n6\n7\n8")
	cases := []struct {
		name     string
		old, act []string
		want     []string
	}{
		{"one elision keeps its window", lines("a\nb\n...\ny\nz"), long, lines("1\n2\n...\n7\n8")},
		{"head only", lines("a\n..."), long, lines("1\n...")},
		{"tail only", lines("...\nz"), long, lines("...\n8")},
		{"output too short for the window", lines("a\nb\n...\ny\nz"), lines("1\n2\n3\n4"), lines("1\n2\n3\n4")},
		{"no elision", lines("a\nb"), long, long},
		{"two elisions are written out", lines("a\n...\nm\n...\nz"), long, long},
	}
	for _, c := range cases {
		if got := rewrite(c.old, c.act); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		if mm := matchElided(rewrite(c.old, c.act), c.act); mm != nil {
			t.Errorf("%s: the rewritten block does not match: %s", c.name, mm.Msg)
		}
	}
}

func TestLineDiff(t *testing.T) {
	d := lineDiff(lines("a\nb\nc\nd"), lines("a\nx\nc\nd\ne"))
	var got []string
	for _, l := range d {
		got = append(got, string(l.Op)+l.Text)
	}
	want := []string{" a", "-b", "+x", " c", " d", "+e"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := lineDiff(nil, lines("a")); len(got) != 1 || got[0].Op != '+' {
		t.Errorf("diff against nothing = %+v", got)
	}
}

func TestRenderDiffFoldsUnchangedLines(t *testing.T) {
	var exp, act []string
	for i := 0; i < 30; i++ {
		exp = append(exp, "same")
		act = append(act, "same")
	}
	act[15] = "changed"
	got := renderDiff(lineDiff(exp, act), 2)
	if len(got) != 8 || !strings.Contains(got[0], "13 lines unchanged") || !strings.Contains(got[len(got)-1], "12 lines unchanged") {
		t.Errorf("got %q", got)
	}
}

func TestFenceLength(t *testing.T) {
	cases := []struct {
		content []string
		want    int
	}{
		{nil, 3},
		{[]string{"plain", "``` not at the start is fine?"}, 4},
		{[]string{"``", "text ```"}, 3},
		{[]string{"````"}, 5},
		{[]string{"   ```"}, 4},
	}
	for _, c := range cases {
		if got := fenceLength(c.content, '`', 3); got != c.want {
			t.Errorf("%q: got %d, want %d", c.content, got, c.want)
		}
	}
}
