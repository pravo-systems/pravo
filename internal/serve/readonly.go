package serve

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/state"
)

// OpenReadOnly opens the state of a local backend for reading only. A location
// that holds none is read as an empty state, and nothing is created: opening a
// store otherwise makes the directories and writes its metadata. A caller that
// has a backend of another kind passes its own function in Options.OpenState.
func OpenReadOnly(ctx context.Context, root *config.Root) (*state.Store, error) {
	if root.State.Backend == "s3" {
		return nil, errors.New("the state is in a bucket: the caller has to open it")
	}
	path := root.StateFile()
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		// Windows reports a path below a file as absent, which would read as an
		// empty state: let state.Open say that the path is a file.
		return state.Open(path)
	}
	return state.OpenBackend(ctx, ReadOnly(&state.LocalBackend{Dir: path}))
}

// ReadOnly wraps a backend so that every write is refused. The store writes its
// metadata when the location has none; that write is kept in memory, so that an
// empty location reads as an empty state.
func ReadOnly(b state.Backend) state.Backend { return &readOnlyBackend{b: b} }

// readOnlyBackend holds the backend in a field and not by embedding it: a method
// that state.Backend gains is then a compile error here until someone decides
// whether the viewer may call it, and not a write that falls through.
type readOnlyBackend struct {
	b    state.Backend
	meta []byte
}

func (b *readOnlyBackend) Name() string { return b.b.Name() }

func (b *readOnlyBackend) Local() bool { return b.b.Local() }

func (b *readOnlyBackend) List(ctx context.Context, prefix string) ([]string, error) {
	return b.b.List(ctx, prefix)
}

const metaKey = "meta.json"

func (b *readOnlyBackend) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := b.b.Get(ctx, key)
	if key == metaKey && errors.Is(err, state.ErrNotFound) && b.meta != nil {
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
