package state

import (
	"bytes"
	"context"
	"errors"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
)

// overlayBackend lets a store work on a state that its user may read and not write. What
// the store writes stays in memory and is read back from there, so that a plan can refresh
// the records it reads and still leave the state as it found it. The objects under the
// prefixes that pass are written to the backend itself: the stored plans, which a reader
// may make (see plans/ in state-format.md).
type overlayBackend struct {
	Backend
	pass []string

	mu      sync.Mutex
	objects map[string][]byte
	deleted map[string]bool
}

// OverlayBackend wraps b so that every write but the ones under the pass prefixes is
// kept in memory and dropped with the backend. A reader who plans is given one. The locks
// are kept in memory as well: a reader who plans holds no lock that a writer would
// see.
func OverlayBackend(b Backend, pass ...string) Backend {
	return &overlayBackend{Backend: b, pass: pass, objects: map[string][]byte{}, deleted: map[string]bool{}}
}

func (b *overlayBackend) passes(key string) bool {
	for _, p := range b.pass {
		if strings.HasPrefix(key, strings.TrimSuffix(p, "/")+"/") {
			return true
		}
	}
	return false
}

func (b *overlayBackend) Get(ctx context.Context, key string) ([]byte, error) {
	if b.passes(key) {
		return b.Backend.Get(ctx, key)
	}
	b.mu.Lock()
	data, ok := b.objects[key]
	gone := b.deleted[key]
	b.mu.Unlock()
	switch {
	case ok:
		return bytes.Clone(data), nil
	case gone:
		return nil, ErrNotFound
	}
	return b.Backend.Get(ctx, key)
}

func (b *overlayBackend) Put(ctx context.Context, key string, data []byte) error {
	if b.passes(key) {
		return b.Backend.Put(ctx, key, data)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.set(key, data)
	return nil
}

// set stores an object. The caller holds b.mu.
func (b *overlayBackend) set(key string, data []byte) {
	b.objects[key] = bytes.Clone(data)
	delete(b.deleted, key)
}

func (b *overlayBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	if b.passes(key) {
		return b.Backend.PutIfAbsent(ctx, key, data)
	}
	if _, err := b.Get(ctx, key); err == nil {
		return false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.objects[key]; ok {
		return false, nil
	}
	b.set(key, data)
	return true, nil
}

func (b *overlayBackend) DeleteIf(ctx context.Context, key string, match func(current []byte) bool) (bool, error) {
	if b.passes(key) {
		return b.Backend.DeleteIf(ctx, key, match)
	}
	cur, err := b.Get(ctx, key)
	if err != nil {
		return false, err
	}
	if !match(cur) {
		return false, nil
	}
	return true, b.Delete(ctx, key)
}

func (b *overlayBackend) ReplaceIf(ctx context.Context, key string, update func(current []byte) ([]byte, bool)) (bool, error) {
	if b.passes(key) {
		return b.Backend.ReplaceIf(ctx, key, update)
	}
	cur, err := b.Get(ctx, key)
	if err != nil {
		return false, err
	}
	data, ok := update(cur)
	if !ok {
		return false, nil
	}
	return true, b.Put(ctx, key, data)
}

func (b *overlayBackend) Delete(ctx context.Context, key string) error {
	if b.passes(key) {
		return b.Backend.Delete(ctx, key)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objects, key)
	b.deleted[key] = true
	return nil
}

func (b *overlayBackend) List(ctx context.Context, prefix string) ([]string, error) {
	keys, err := b.Backend.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	dir := strings.TrimSuffix(prefix, "/")
	b.mu.Lock()
	defer b.mu.Unlock()
	keys = slices.DeleteFunc(keys, func(k string) bool { return b.deleted[k] && !b.passes(k) })
	for k := range b.objects {
		if path.Dir(k) == dir && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// ListsVersions is true while nothing has been written to the overlay and the backend
// under it lists versions: only then does the listing of the backend say what the store
// would find. A reader who plans opens the store before it writes anything.
func (b *overlayBackend) ListsVersions() bool {
	vl, _, ok := versionLister(b.Backend)
	if !ok || vl == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.objects) == 0 && len(b.deleted) == 0
}

// ListVersions lists the versions of the backend under the overlay.
func (b *overlayBackend) ListVersions(ctx context.Context, prefix string) ([]ObjectVersion, error) {
	vl, _, ok := versionLister(b.Backend)
	if !ok || !b.ListsVersions() {
		return nil, errors.New("the overlay holds writes that the listing of the backend does not")
	}
	return vl.ListVersions(ctx, prefix)
}

// ReadOnlyView says that nothing written through the overlay reaches the backend, so that no
// snapshot is kept through it.
func (b *overlayBackend) ReadOnlyView() {}
