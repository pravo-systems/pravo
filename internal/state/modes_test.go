package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// State holds provider attributes in clear text, so what pravo creates is
// readable by its owner only.
func TestLocalStateIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pravo.state")
	b, err := NewLocalBackend(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Put(context.Background(), "resources/a.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.PutIfAbsent(context.Background(), "locks/a.lock", []byte("x")); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for _, p := range []string{dir, filepath.Join(dir, "resources"), filepath.Join(dir, "resources", "a.json"), filepath.Join(dir, "locks", "a.lock")} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s has mode %v; want no access for group and others", p, st.Mode().Perm())
		}
	}
}
