package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFmtCheckWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	src := "package demo\n\nx:   1\n"
	if err := os.WriteFile(filepath.Join(dir, "a.cue"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := fmtCmd()
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want an error for an unformatted file")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.cue"))
	if string(got) != src {
		t.Fatalf("--check changed the file: %q", got)
	}
	cmd = fmtCmd()
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	cmd = fmtCmd()
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("formatted file must pass: %v", err)
	}
}

// A file keeps the line ending it has. Git on Windows checks CUE files out with CRLF, and a
// formatter that wanted LF would call every one of them unformatted and rewrite it.
func TestFmtKeepsCRLF(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	crlf := "package demo\r\n\r\nx: 1\r\ny: \"\"\"\r\n\ttext\r\n\t\"\"\"\r\n"
	lf := "package demo\n\nx: 1\n"
	mixed := "package demo\r\n\r\nx: 1\ny: 2\r\n"
	for name, src := range map[string]string{"crlf.cue": crlf, "lf.cue": lf, "mixed.cue": mixed, "loose.cue": "package demo\r\n\r\nx:   1\r\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := fmtCmd()
	cmd.SetArgs([]string{"--check"})
	err := cmd.Execute()
	if err == nil || exitCodeOf(err) != 2 {
		t.Fatalf("want exit 2 for the two files that are not formatted, got %v", err)
	}
	cmd = fmtCmd()
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"crlf.cue":  crlf,
		"lf.cue":    lf,
		"mixed.cue": "package demo\r\n\r\nx: 1\r\ny: 2\r\n",
		"loose.cue": "package demo\r\n\r\nx: 1\r\n",
	}
	for name, w := range want {
		got, _ := os.ReadFile(filepath.Join(dir, name))
		if string(got) != w {
			t.Errorf("%s: got %q, want %q", name, got, w)
		}
	}
	cmd = fmtCmd()
	cmd.SetArgs([]string{"--check"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("after fmt every file passes: %v", err)
	}
}
