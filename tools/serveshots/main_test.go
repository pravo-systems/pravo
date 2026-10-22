package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplacements(t *testing.T) {
	dir := t.TempDir()
	m := &maker{spaces: map[string]*workspace{"platform/changed": {Dir: dir}}}
	sc, _ := findScenario("platform")
	v := &viewer{Base: "http://127.0.0.1:43211"}
	pairs := m.replacements(sc, situationPlan, v)
	if len(pairs) < 2 {
		t.Fatalf("pairs = %q", pairs)
	}
	if pairs[0] != [2]string{dir, "~/estate/platform"} {
		t.Errorf("the directory: %q", pairs[0])
	}
	if last := pairs[len(pairs)-1]; last != [2]string{"127.0.0.1:43211", "127.0.0.1:8700"} {
		t.Errorf("the address: %q", last)
	}
	// A situation whose workspace was not made has nothing to replace.
	if got := m.replacements(sc, situationClean, v); got != nil {
		t.Errorf("pairs = %q", got)
	}
}

func TestRunFlags(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-list"}, &out, &errb); code != 0 {
		t.Fatalf("-list: exit %d: %s", code, errb.String())
	}
	if n := strings.Count(out.String(), "\n"); n != len(allShots()) {
		t.Errorf("-list printed %d lines for %d shots", n, len(allShots()))
	}
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{}, "-out is required"},
		{[]string{"-out", t.TempDir()}, "-bin is required"},
		{[]string{"-out", t.TempDir(), "-bin", filepath.Join(t.TempDir(), "none")}, "no such file"},
		{[]string{"-out", t.TempDir(), "-only", "nope", "-placeholders"}, "names no shot"},
		{[]string{"extra"}, "unexpected argument"},
	} {
		out.Reset()
		errb.Reset()
		if code := run(tt.args, &out, &errb); code != 2 || !strings.Contains(errb.String(), tt.want) {
			t.Errorf("%q: exit %d, stderr %q, want 2 and %q", tt.args, code, errb.String(), tt.want)
		}
	}
	dir := t.TempDir()
	out.Reset()
	if code := run([]string{"-out", dir, "-placeholders", "-only", "overview-dark"}, &out, &errb); code != 0 {
		t.Fatalf("-placeholders: exit %d", code)
	}
	if _, err := checkImage(filepath.Join(dir, "overview-dark.png"), 1440, 900); err != nil {
		t.Error(err)
	}
}
