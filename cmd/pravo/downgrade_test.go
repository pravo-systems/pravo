package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const downgradeWant = "the repository lists readers in .pravo/access/readers.txt, but the state is not sealed: its meta.json may have been changed. Run pravo protect status; to seal it, pravo protect init --encrypt; if the state is not meant to be sealed, remove the readers file"

// downgrade replaces meta.json of the bucket by one of the given format, without
// the sealed part, as a writer of the bucket who holds no key can.
func (s *sealed) downgrade(t *testing.T, format int) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(s.f.Objects["prod/meta.json"], &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"sealed", "history", "migration"} {
		delete(m, k)
	}
	m["format"] = json.RawMessage([]byte{byte('0' + format)})
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	s.f.Objects["prod/meta.json"] = data
}

func TestDowngradedStateIsRefusedToWriters(t *testing.T) {
	s := sealedBucket(t)
	s.downgrade(t, 2)
	puts := s.puts()

	t.Run("a writer refuses with exit 5 and says what to do", func(t *testing.T) {
		for _, args := range [][]string{{"state", "rm", "net.vpc"}, {"state", "unlock", "--stale"}, {"state", "mv", "net.vpc", "net.vpc2"}, {"protect", "gc"}} {
			text, code := runPravo(t, s.dir, args...)
			if code != 5 || !strings.Contains(text, "Error: "+downgradeWant) {
				t.Errorf("%v: exit %d\n%s", args, code, text)
			}
		}
		if s.puts() != puts {
			t.Error("a refused command wrote to the bucket")
		}
		if _, ok := s.f.Objects["prod/resources/net.vpc.json"]; !ok {
			t.Error("the refused command removed the record")
		}
	})
	t.Run("a reader warns", func(t *testing.T) {
		var text string
		var code int
		stderr := withStderr(t, func() { text, code = runPravo(t, s.dir, "state", "list") })
		if code != 0 || text != "net.db\nnet.vpc\n" {
			t.Fatalf("exit %d\n%s", code, text)
		}
		if !strings.Contains(stderr, "Warning: "+downgradeWant) {
			t.Errorf("standard error: %q", stderr)
		}
	})
	t.Run("status says it in the Sealed line", func(t *testing.T) {
		text, code := runPravo(t, s.dir, "protect", "status")
		if code != 0 || !strings.Contains(text, "Sealed       off · format 2 · immutable records · the repository expects a sealed state\n") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		text, code = runPravo(t, s.dir, "protect", "status", "--json")
		if code != 0 || !strings.Contains(text, `"expects_sealed": true`) {
			t.Fatalf("json: exit %d\n%s", code, text)
		}
	})
	t.Run("a dry run of gc warns and changes nothing", func(t *testing.T) {
		var text string
		var code int
		stderr := withStderr(t, func() { text, code = runPravo(t, s.dir, "protect", "gc", "--dry-run") })
		if code != 0 || !strings.Contains(stderr, "Warning: "+downgradeWant) || s.puts() != puts {
			t.Errorf("exit %d\n%s\n%s", code, text, stderr)
		}
	})
	t.Run("check is exempt", func(t *testing.T) {
		text, _ := runPravo(t, s.dir, "protect", "check")
		if strings.Contains(text, "may have been changed") {
			t.Errorf("check refused:\n%s", text)
		}
	})
	t.Run("a repository without a readers file does not expect it", func(t *testing.T) {
		if err := os.Remove(filepath.Join(s.dir, ".pravo", "access", "readers.txt")); err != nil {
			t.Fatal(err)
		}
		text, code := runPravo(t, s.dir, "protect", "status")
		if code != 0 || strings.Contains(text, "expects") || !strings.Contains(text, "Sealed       off · format 2 · immutable records\n") {
			t.Fatalf("exit %d\n%s", code, text)
		}
		var stderr string
		stderr = withStderr(t, func() { text, code = runPravo(t, s.dir, "state", "list") })
		if code != 0 || strings.Contains(stderr, "Warning") {
			t.Errorf("list: exit %d\n%s\n%s", code, text, stderr)
		}
	})
}

func TestDowngradeToFormatOneIsRefused(t *testing.T) {
	s := sealedBucket(t)
	s.downgrade(t, 1)
	text, code := runPravo(t, s.dir, "state", "rm", "net.vpc")
	if code != 5 || !strings.Contains(text, "Error: "+downgradeWant) {
		t.Fatalf("exit %d\n%s", code, text)
	}
	text, code = runPravo(t, s.dir, "protect", "status")
	if code != 0 || !strings.Contains(text, "Sealed       off · format 1 · the repository expects a sealed state\n") {
		t.Errorf("status: exit %d\n%s", code, text)
	}
}

func TestDowngradeIsNoticedWhenTheReadersFileDoesNotParse(t *testing.T) {
	s := sealedBucket(t)
	s.downgrade(t, 2)
	s.readers(t, "this is not a reader")
	text, code := runPravo(t, s.dir, "state", "rm", "net.vpc")
	if code != 5 || !strings.Contains(text, "Error: "+downgradeWant) {
		t.Fatalf("exit %d\n%s", code, text)
	}
	if strings.Contains(text, "this is not a reader") {
		t.Error("a line of the readers file is in the message")
	}
}

// TestFirstSealOfAStateWithAReadersFile follows the order that the guide gives:
// the readers file first, then check and init, which do not write through the
// guard, and then the commands that write.
func TestFirstSealOfAStateWithAReadersFile(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	st := e.store(t)
	e.record(t, st, "net.vpc", secretA)
	_, ada := person(t, ids, "ada")

	// Before the readers file there is nothing to expect.
	if text, code := runPravo(t, e.dir, "state", "unlock", "--stale"); code != 0 {
		t.Fatalf("without a readers file: exit %d\n%s", code, text)
	}
	e.readers(t, ada)

	if text, code := runPravo(t, e.dir, "state", "unlock", "--stale"); code != 5 || !strings.Contains(text, "but the state is not sealed") {
		t.Fatalf("unsealed, with a readers file: exit %d\n%s", code, text)
	}
	text, code := runPravo(t, e.dir, "protect", "status")
	if code != 0 || !strings.Contains(text, "Sealed       off · format 1 · the repository expects a sealed state\n") {
		t.Fatalf("status: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, e.dir, "protect", "check"); code != 0 {
		t.Fatalf("check: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes"); code != 0 {
		t.Fatalf("init: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, e.dir, "state", "unlock", "--stale"); code != 0 {
		t.Fatalf("sealed: exit %d\n%s", code, text)
	}
	text, code = runPravo(t, e.dir, "protect", "status")
	if code != 0 || strings.Contains(text, "expects") {
		t.Fatalf("status after init: exit %d\n%s", code, text)
	}
}

// TestLocalStateAndTheReadersFile is the same on a state in a directory, with
// the readers file in the repository.
func TestLocalStateAndTheReadersFile(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	dir := specDir(t, map[string]string{"main.cue": "package t\n"})
	_, ada := person(t, ids, "ada")
	if text, code := runPravo(t, dir, "state", "unlock", "--stale"); code != 0 {
		t.Fatalf("without a readers file: exit %d\n%s", code, text)
	}
	writeReaders(t, filepath.Join(dir, ".pravo", "access"), ada)
	if text, code := runPravo(t, dir, "state", "unlock", "--stale"); code != 5 || !strings.Contains(text, "but the state is not sealed") {
		t.Fatalf("exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, dir, "protect", "init", "--encrypt", "--yes", "--allow-local"); code != 0 {
		t.Fatalf("init: exit %d\n%s", code, text)
	}
	if text, code := runPravo(t, dir, "state", "unlock", "--stale"); code != 0 {
		t.Fatalf("sealed: exit %d\n%s", code, text)
	}
}

// A root in a repository whose readers file is meant for other roots names
// another access directory, and is then not expected to be sealed.
func TestDowngradeCheckFollowsTheAccessDirectory(t *testing.T) {
	s := sealedBucket(t)
	s.downgrade(t, 2)
	empty := t.TempDir()
	t.Setenv("PRAVO_ACCESS_DIR", empty)
	if text, code := runPravo(t, s.dir, "state", "unlock", "--stale"); code != 0 {
		t.Fatalf("with another access directory: exit %d\n%s", code, text)
	}
	t.Setenv("PRAVO_ACCESS_DIR", "")
	if text, code := runPravo(t, s.dir, "state", "unlock", "--stale"); code != 5 || !strings.Contains(text, downgradeWant) {
		t.Fatalf("with the repository's: exit %d\n%s", code, text)
	}
}
