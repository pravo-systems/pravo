package state

import (
	"context"
	"errors"
	"fmt"
)

// ReadOnly wraps a backend so that every write is refused. The store writes its
// metadata when the location has none; that write is kept in memory, so that an
// empty location reads as an empty state.
func ReadOnly(b Backend) Backend { return &readOnlyBackend{b: b} }

// readOnlyBackend holds the backend in a field and not by embedding it: a method
// that Backend gains is then a compile error here until someone decides whether
// a reader may call it, and not a write that falls through.
type readOnlyBackend struct {
	b    Backend
	meta []byte
}

func (b *readOnlyBackend) Name() string { return b.b.Name() }

func (b *readOnlyBackend) Local() bool { return b.b.Local() }

func (b *readOnlyBackend) List(ctx context.Context, prefix string) ([]string, error) {
	return b.b.List(ctx, prefix)
}

// ListsVersions and ListVersions let a reader read a state through its snapshot when
// the backend under it lists versions.
func (b *readOnlyBackend) ListsVersions() bool {
	l, ok := b.b.(interface{ ListsVersions() bool })
	_, isL := b.b.(VersionLister)
	return isL && (!ok || l.ListsVersions())
}

func (b *readOnlyBackend) ListVersions(ctx context.Context, prefix string) ([]ObjectVersion, error) {
	vl, ok := b.b.(VersionLister)
	if !ok {
		return nil, errors.New("the backend does not list versions")
	}
	return vl.ListVersions(ctx, prefix)
}

// ReadOnlyView says that nothing is written through it, so that no snapshot is kept.
func (b *readOnlyBackend) ReadOnlyView() {}

const metaKey = "meta.json"

func (b *readOnlyBackend) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := b.b.Get(ctx, key)
	if key == metaKey && errors.Is(err, ErrNotFound) && b.meta != nil {
		return b.meta, nil
	}
	return data, err
}

func (b *readOnlyBackend) Put(ctx context.Context, key string, data []byte) error {
	if key == metaKey {
		b.meta = data
		return nil
	}
	return b.refuse()
}

func (b *readOnlyBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	return false, b.refuse()
}

func (b *readOnlyBackend) Delete(ctx context.Context, key string) error { return b.refuse() }

func (b *readOnlyBackend) DeleteIf(ctx context.Context, key string, match func(current []byte) bool) (bool, error) {
	return false, b.refuse()
}

func (b *readOnlyBackend) ReplaceIf(ctx context.Context, key string, update func(current []byte) ([]byte, bool)) (bool, error) {
	return false, b.refuse()
}

func (b *readOnlyBackend) refuse() error {
	return fmt.Errorf("the state at %s is open for reading and is not changed", b.Name())
}
