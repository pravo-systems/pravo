package state

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

// OutputsReaders lists the reader keys with a lock in the current group, which
// meta.json holds in the clear, of a state that another root reads.
func TestOutputsReaders(t *testing.T) {
	ctx := context.Background()
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	b, err := NewLocalBackend(filepath.Join(t.TempDir(), "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	if keys, err := OutputsReaders(ctx, b); keys != nil || err != nil {
		t.Fatalf("a state with no meta.json: %v, %v", keys, err)
	}
	s, err := OpenBackendWith(ctx, b, Options{NewFormat: Format})
	if err != nil {
		t.Fatal(err)
	}
	if keys, err := OutputsReaders(ctx, b); keys != nil || err != nil {
		t.Fatalf("a state that is not sealed: %v, %v", keys, err)
	}
	if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada, dan), Identity: ada}); err != nil {
		t.Fatal(err)
	}
	keys, err := OutputsReaders(ctx, b)
	if err != nil || len(keys) != 2 || !slices.ContainsFunc(keys, ada.Reader.Public().Equal) || !slices.ContainsFunc(keys, dan.Reader.Public().Equal) {
		t.Fatalf("a state sealed to ada and dan: %v, %v", keys, err)
	}
}
