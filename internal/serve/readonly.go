package serve

import (
	"context"
	"errors"
	"fmt"

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
	return state.OpenBackend(ctx, ReadOnly(&state.LocalBackend{Dir: root.StateFile()}))
}

// ReadOnly wraps a backend so that every write is refused. The store writes its
// metadata when the location has none; that write is kept in memory, so that an
// empty location reads as an empty state.
func ReadOnly(b state.Backend) state.Backend { return &readOnlyBackend{Backend: b} }

type readOnlyBackend struct {
	state.Backend
	meta []byte
}

const metaKey = "meta.json"

func (b *readOnlyBackend) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := b.Backend.Get(ctx, key)
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
