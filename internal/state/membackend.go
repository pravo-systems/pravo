package state

import (
	"bytes"
	"context"
	"path"
	"sort"
	"strings"
	"sync"
)

// MemBackend keeps objects in memory. It is the smallest backend that honours the
// whole contract of Backend, which makes it the reference for one written by a
// program that builds on pravo, and a state for tests.
type MemBackend struct {
	name string

	mu      sync.Mutex
	objects map[string][]byte
}

// NewMemBackend returns an empty backend. name is how messages call it.
func NewMemBackend(name string) *MemBackend {
	return &MemBackend{name: name, objects: map[string][]byte{}}
}

func (b *MemBackend) Name() string { return b.name }
func (b *MemBackend) Local() bool  { return false }

func (b *MemBackend) Get(ctx context.Context, key string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return bytes.Clone(data), nil
}

func (b *MemBackend) Put(ctx context.Context, key string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[key] = bytes.Clone(data)
	return nil
}

func (b *MemBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.objects[key]; ok {
		return false, nil
	}
	b.objects[key] = bytes.Clone(data)
	return true, nil
}

func (b *MemBackend) DeleteIf(ctx context.Context, key string, match func(current []byte) bool) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.objects[key]
	if !ok {
		return false, ErrNotFound
	}
	if !match(bytes.Clone(data)) {
		return false, nil
	}
	delete(b.objects, key)
	return true, nil
}

func (b *MemBackend) ReplaceIf(ctx context.Context, key string, update func(current []byte) ([]byte, bool)) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.objects[key]
	if !ok {
		return false, ErrNotFound
	}
	next, ok := update(bytes.Clone(data))
	if !ok {
		return false, nil
	}
	b.objects[key] = bytes.Clone(next)
	return true, nil
}

func (b *MemBackend) Delete(ctx context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objects, key)
	return nil
}

func (b *MemBackend) List(ctx context.Context, prefix string) ([]string, error) {
	dir := strings.TrimSuffix(prefix, "/")
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for k := range b.objects {
		if path.Dir(k) == dir {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Keys are all the keys of the backend, sorted, at any depth. It is for a test that
// looks at what a store wrote.
func (b *MemBackend) Keys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.objects))
	for k := range b.objects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
