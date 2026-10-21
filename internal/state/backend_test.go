package state

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Two writers of the same key do not share a temporary file: every Put
// succeeds, the object is one of the values written, and no temporary file is
// left or listed.
func TestLocalBackendPutsOfOneKeyDoNotCollide(t *testing.T) {
	dir := t.TempDir()
	b, err := NewLocalBackend(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const writers, rounds = 16, 40
	values := map[string]bool{}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for w := 0; w < writers; w++ {
		value := fmt.Sprintf(`{"writer": %d}`, w)
		values[value] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				if err := b.Put(ctx, "outputs.json", []byte(value)); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("%d writes failed, the first with %v", len(errs), errs[0])
	}
	got, err := b.Get(ctx, "outputs.json")
	if err != nil || !values[string(got)] {
		t.Fatalf("outputs.json = %q, %v: not one of the values written", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("a temporary file is left: %s", e.Name())
		}
	}
	info, err := os.Stat(filepath.Join(dir, "outputs.json"))
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Errorf("outputs.json mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
}

// A temporary file that a crashed writer left is not a resource.
func TestLocalBackendListSkipsTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	b, err := NewLocalBackend(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := b.Put(ctx, "resources/a.json", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resources", "a.json.123456.tmp"), []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	keys, err := b.List(ctx, "resources")
	if err != nil || len(keys) != 1 || keys[0] != "resources/a.json" {
		t.Errorf("List = %v, %v", keys, err)
	}
}

// Revisions reads the backend. A store opened before another process wrote a
// record holds the old node, and the revision it reports is the new one.
func TestRevisionsComeFromTheBackend(t *testing.T) {
	dir := t.TempDir()
	mine, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	set := func(s *Store, name string) {
		t.Helper()
		if err := s.Set(&Resource{Mode: "managed", Type: "t", Name: name, Attributes: json.RawMessage(`{}`)}, ""); err != nil {
			t.Fatal(err)
		}
	}
	set(mine, "a")
	set(mine, "b")

	theirs, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	set(theirs, "a") // a is at revision 2
	set(theirs, "c") // c is new
	if _, err := theirs.Remove("t.b"); err != nil {
		t.Fatal(err)
	}

	if got := mine.Get("t.a").Revision; got != 1 {
		t.Fatalf("my node a is at revision %d", got)
	}
	got, err := mine.Revisions([]string{"t.a", "t.b", "t.c", "t.d"})
	if err != nil {
		t.Fatal(err)
	}
	for addr, want := range map[string]int{"t.a": 2, "t.b": 0, "t.c": 1, "t.d": 0} {
		if got[addr] != want {
			t.Errorf("revision of %s = %d, want %d", addr, got[addr], want)
		}
	}
	if got := mine.Get("t.a").Revision; got != 1 {
		t.Errorf("Revisions changed the node this store holds: revision %d", got)
	}
	if empty, err := mine.Revisions(nil); err != nil || len(empty) != 0 {
		t.Errorf("Revisions of nothing = %v, %v", empty, err)
	}
}

// LoadOutputs tells a state that never wrote its outputs from one that wrote
// none.
func TestLoadOutputsSaysWhetherTheObjectExists(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, present, err := s.LoadOutputs(); err != nil || present || len(out) != 0 {
		t.Fatalf("a new state: %v, present %v, %v", out, present, err)
	}
	if err := s.SetOutputs(nil); err != nil {
		t.Fatal(err)
	}
	if out, present, err := s.LoadOutputs(); err != nil || !present || len(out) != 0 {
		t.Fatalf("an object with no outputs: %v, present %v, %v", out, present, err)
	}
	if err := s.SetOutputs(map[string]json.RawMessage{"o": json.RawMessage(`1`)}); err != nil {
		t.Fatal(err)
	}
	if out, present, err := s.LoadOutputs(); err != nil || !present || string(out["o"]) != `1` {
		t.Fatalf("an object with one: %v, present %v, %v", out, present, err)
	}
}
