package state

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// listedThenDeleted hides the node at gone from the listing after the first read of it, which finds it
// not found: another process deleted the node between the listing and the read.
type listedThenDeleted struct {
	Backend
	gone    string
	mu      sync.Mutex
	vanish  bool // the node is deleted once it has been read
	deleted bool
}

func (v *listedThenDeleted) Get(ctx context.Context, key string) ([]byte, error) {
	if key == v.gone {
		v.mu.Lock()
		defer v.mu.Unlock()
		if v.vanish {
			v.deleted = true
		}
		if v.deleted || !v.vanish {
			return nil, ErrNotFound
		}
	}
	return v.Backend.Get(ctx, key)
}

func (v *listedThenDeleted) List(ctx context.Context, prefix string) ([]string, error) {
	keys, err := v.Backend.List(ctx, prefix)
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.deleted {
		keys = slices.DeleteFunc(keys, func(k string) bool { return k == v.gone })
	}
	return keys, err
}

// A node that is deleted between the listing and the read is a node that is gone: the open lists
// again and goes on. A pointer that stays missing while it is listed is an error that names the node.
func TestOpenReadsAgainWhenANodeVanishesAfterTheListing(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "st")
	lb, err := NewLocalBackend(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := OpenBackend(ctx, lb)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"keep", "flap"} {
		if err := s.Set(&Resource{Mode: "managed", Type: "random_pet", Name: name, Attributes: []byte(`{"id":"a"}`)}, ""); err != nil {
			t.Fatal(err)
		}
	}
	flap := nodeKey("random_pet.flap")

	v := &listedThenDeleted{Backend: lb, gone: flap, vanish: true}
	got, err := OpenBackend(ctx, v)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got.Get("random_pet.keep") == nil || got.Get("random_pet.flap") != nil {
		t.Errorf("state after the open: keep=%v flap=%v, want keep and no flap", got.Get("random_pet.keep") != nil, got.Get("random_pet.flap") != nil)
	}

	w := &listedThenDeleted{Backend: lb, gone: flap}
	_, err = OpenBackend(ctx, w)
	if err == nil || !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), flap) {
		t.Errorf("open with a pointer that stays missing = %v, want an error that names %s", err, flap)
	}
}
