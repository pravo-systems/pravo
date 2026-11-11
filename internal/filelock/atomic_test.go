package filelock

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.json")
	for i, body := range []string{"one", "two"} {
		if err := WriteFileAtomic(p, []byte(body), AtomicOptions{Mode: 0o640, Sync: i == 1}); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(p)
		if err != nil || string(got) != body {
			t.Fatalf("read %q, %v; want %q", got, err, body)
		}
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(p); info.Mode().Perm() != 0o640 {
			t.Errorf("mode %v, want 0640", info.Mode().Perm())
		}
	}
	// A temporary file of a pattern that a listing can skip, and none left behind.
	q := filepath.Join(dir, "b")
	if err := WriteFileAtomic(q, []byte("x"), AtomicOptions{TempPattern: "%s.*.tmp"}); err != nil {
		t.Fatal(err)
	}
	left, _ := os.ReadDir(dir)
	for _, e := range left {
		if strings.Contains(e.Name(), "tmp") {
			t.Errorf("temporary file %s left behind", e.Name())
		}
	}
	// The default mode is private.
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(q); info.Mode().Perm() != 0o600 {
			t.Errorf("default mode %v, want 0600", info.Mode().Perm())
		}
	}
	// A missing directory is an error, and nothing is created.
	if err := WriteFileAtomic(filepath.Join(dir, "no", "c"), []byte("x"), AtomicOptions{}); err == nil {
		t.Error("a missing directory must fail")
	}
}
