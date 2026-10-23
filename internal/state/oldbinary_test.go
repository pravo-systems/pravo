package state

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A pravo that was released before formats 2 and 3 refuses a store of either, for
// the commands that write and the ones that only read, and reads a store of
// format 1. The test needs such a binary: build one from the tree before this
// work and name it in PRAVO_OLD_BINARY, as
//
//	go build -o /tmp/pravo-old ./cmd/pravo   (in a checkout that predates it)
//	PRAVO_OLD_BINARY=/tmp/pravo-old go test ./internal/state -run OldBinary
func TestOldBinaryRefusesNewerFormats(t *testing.T) {
	bin := os.Getenv("PRAVO_OLD_BINARY")
	if bin == "" {
		t.Skip("PRAVO_OLD_BINARY names no binary of an earlier pravo")
	}
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	project := t.TempDir()
	for name, content := range map[string]string{
		"cue.mod/module.cue": "module: \"pravo.local/old\"\nlanguage: version: \"v0.14.0\"\n",
		"main.cue":           "package infra\n\n$settings: {}\n",
	} {
		p := filepath.Join(project, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(project, "pravo.state")
	run := func(args ...string) (string, int) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = project
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	open := func(format int) *Store {
		_ = os.RemoveAll(dir)
		s, err := OpenWith(dir, Options{NewFormat: Format, Identity: ada})
		if err != nil {
			t.Fatal(err)
		}
		seedSecrets(t, s)
		if format >= FormatImmutable {
			if _, err := s.Upgrade(ctx, UpgradeOptions{To: format, Readers: readersOf(ada), Identity: ada}); err != nil {
				t.Fatal(err)
			}
		}
		return s
	}

	open(Format)
	if out, code := run("state", "list"); code != 0 || !strings.Contains(out, "db.main") {
		t.Fatalf("the old binary on a store of format 1: exit %d\n%s", code, out)
	}
	for _, format := range []int{FormatImmutable, FormatSealed} {
		open(format)
		for _, args := range [][]string{{"state", "list"}, {"state", "show", "db.main"}, {"plan"}, {"state", "log"}} {
			out, code := run(args...)
			want := fmt.Sprintf("was written by a newer pravo (format %d)", format)
			if code == 0 && args[0] == "plan" {
				code = 1 // plan prints the error and its exit code is of the plan
			}
			if code != 1 || !strings.Contains(out, want) {
				t.Errorf("format %d, pravo %s: exit %d\n%s", format, strings.Join(args, " "), code, out)
			}
		}
	}

	// A seal that stopped halfway is format 3 as well.
	_ = os.RemoveAll(dir)
	lb, _ := NewLocalBackend(dir)
	s, err := OpenBackendWith(ctx, lb, Options{NewFormat: Format})
	if err != nil {
		t.Fatal(err)
	}
	seedSecrets(t, s)
	fb := &failAfter{Backend: lb}
	fb.left.Store(3)
	s, _ = OpenBackendWith(ctx, fb, Options{})
	if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}); err == nil {
		t.Fatal("the seal did not crash")
	}
	if out, code := run("state", "list"); code != 1 || !strings.Contains(out, "was written by a newer pravo (format 3)") {
		t.Errorf("a seal that stopped halfway, pravo state list: exit %d\n%s", code, out)
	}
}
