package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Pushing an older copy of a sealed state over the same state after a reader was
// removed must not give the reader back her lock, or leave records that the meta.json
// it puts back cannot open.
func TestPushOfAnOlderCopyDoesNotUndoARewrap(t *testing.T) {
	ctx := context.Background()
	ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
	b, _ := NewLocalBackend(t.TempDir())
	s := sealedStore(t, b, ada, fay)
	pulled, _ := NewLocalBackend(t.TempDir())
	if _, err := Copy(ctx, b, pulled); err != nil {
		t.Fatal(err)
	}
	// Fay leaves, and a record is written in the new group.
	admin := openIdentity(t, b, ada)
	if _, err := admin.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada)}); err != nil {
		t.Fatal(err)
	}
	r := *admin.Get("net.vpc")
	r.Attributes = json.RawMessage(`{"id":"vpc-after"}`)
	mustSet(t, admin, &r)
	_ = s
	// The old copy is pushed back.
	if _, err := Copy(ctx, pulled, b); !errors.Is(err, ErrCopyRefused) {
		t.Errorf("push of an older copy = %v, want ErrCopyRefused", err)
	}
	got, err := OpenBackendWith(ctx, b, Options{Identity: ada})
	if err != nil {
		t.Fatalf("ada opens the state after the push: %v", err)
	}
	if v := got.Get("net.vpc"); v == nil || string(v.Attributes) != `{"id":"vpc-after"}` {
		t.Errorf("net.vpc after the refused push: %+v", v)
	}
	// A copy that is newer than the destination is still taken.
	if _, err := Copy(ctx, b, pulled); err != nil {
		t.Errorf("pull of a newer state over an older copy = %v", err)
	}
	if fayOpens := openIdentity(t, b, fay); !fayOpens.Locked() {
		t.Errorf("fay, who was removed, opens the state again after the push")
	}
}

// Pushing a copy of format 1 onto a state of format 2 would leave pointers under a
// meta.json that says there are none.
func TestPushOfFormatOneOntoFormatTwoIsRefused(t *testing.T) {
	ctx := context.Background()
	dst, _ := NewLocalBackend(t.TempDir())
	s := openFormat2(t, dst)
	mustSet(t, s, node("a"), node("b"), node("c"))
	src, _ := NewLocalBackend(t.TempDir())
	one, err := OpenBackendWith(ctx, src, Options{NewFormat: Format})
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, one, node("a"))
	if _, err := Copy(ctx, src, dst); !errors.Is(err, ErrCopyRefused) {
		t.Errorf("push of format 1 onto format 2 = %v, want ErrCopyRefused", err)
	}
	if got := formatOf(t, dst); got != FormatImmutable {
		t.Errorf("format of the destination after the refused push = %d", got)
	}
	// An empty destination takes it.
	empty, _ := NewLocalBackend(t.TempDir())
	if _, err := OpenBackendWith(ctx, empty, Options{NewFormat: FormatImmutable}); err != nil {
		t.Fatal(err)
	}
	if _, err := Copy(ctx, src, empty); err != nil {
		t.Errorf("push of format 1 onto an empty state of format 2 = %v", err)
	}
}
