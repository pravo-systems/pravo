package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// A meta.json that was lowered while the records stayed sealed holds the only
// copy of the keys that open them. Sealing such a state would write new keys over
// it, so it is refused before anything is written.
func TestSealRefusesAStateWhoseRecordsAreSealedAlready(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	b, _ := NewLocalBackend(t.TempDir())
	sealedStore(t, b, ada, dan)
	raw, err := b.Get(ctx, "meta.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["format"] = json.RawMessage("2")
	delete(m, "sealed")
	lowered, _ := json.Marshal(m)
	if err := b.Put(ctx, "meta.json", lowered); err != nil {
		t.Fatal(err)
	}
	s, err := OpenBackendWith(ctx, b, Options{Identity: ada})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada, dan), Identity: ada})
	if !errors.Is(err, ErrUntrusted) {
		t.Fatalf("Upgrade = %v, want ErrUntrusted", err)
	}
	after, err := b.Get(ctx, "meta.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, lowered) {
		t.Errorf("meta.json was written:\n%s", after)
	}
	// With the meta.json put back, the store is what it was.
	if err := b.Put(ctx, "meta.json", raw); err != nil {
		t.Fatal(err)
	}
	if s := openIdentity(t, b, ada); s.Locked() {
		t.Error("the store does not open with the meta.json that was put back")
	}
}
