package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFmtCheckWritesNothing(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
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
