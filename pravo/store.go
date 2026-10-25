package pravo

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/state"
)

// StoreOptions say how OpenStore opens the state of a root.
type StoreOptions struct {
	// Write opens the state for a command that changes it. Without it the state is
	// open for reading: a location that holds none reads as an empty state and
	// nothing is created or changed.
	Write bool
	// Identity is the key of a reader, for a state that is sealed. Nil opens a sealed
	// state locked: it lists addresses and revisions and returns no value.
	Identity *Identity
	// Env is the environment the credentials of a remote store come from
	// (AWS_SESSION_TOKEN, PRAVO_STATE_TOKEN).
	Env Env
	// CheckConditions, when set, is called with the remote backend of a store that is
	// opened for writing, to see that it honours the conditional requests the locks
	// rest on. Its error stops the open.
	CheckConditions func(ctx context.Context, b Backend) error
	// Warn receives the warnings of the store, such as the end of a transaction that
	// could not be signed. Nil drops them.
	Warn func(msg string)
}

// OpenStore opens the state that the settings of root name: a directory, a
// bucket or a server. It does no more than that: whether a sealed state needs
// a key, and which readers a repository expects, is for the caller to judge from
// the store it gets back (Store.Locked, Store.Protection).
func OpenStore(ctx context.Context, root *Root, o StoreOptions) (*Store, error) {
	st, err := openStore(ctx, root, o)
	if st != nil && o.Warn != nil {
		st.SetWarn(o.Warn)
	}
	return st, err
}

func openStore(ctx context.Context, root *config.Root, o StoreOptions) (*state.Store, error) {
	so := state.Options{Identity: o.Identity}
	switch {
	case root.State.Remote():
		b, err := RemoteBackend(ctx, root, o.Env)
		if err != nil {
			return nil, err
		}
		if o.Write {
			if o.CheckConditions != nil {
				if err := o.CheckConditions(ctx, b); err != nil {
					return nil, err
				}
			}
		} else {
			b = &readOnlyBackend{Backend: b}
		}
		return state.OpenBackendWith(ctx, b, so)
	case o.Write:
		return state.OpenWithContext(ctx, root.StateFile(), so)
	}
	path := root.StateFile()
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		return state.Open(path) // says that the path is a file
	}
	return state.OpenBackendWith(ctx, &readOnlyBackend{Backend: &state.LocalBackend{Dir: path}}, so)
}

// RemoteBackend opens the bucket or the server that the root keeps its state on.
// The credentials the settings leave out come from env.
func RemoteBackend(ctx context.Context, root *Root, e env.Env) (Backend, error) {
	return engine.RemoteBackend(ctx, root.State, e)
}

// readOnlyBackend refuses every write but one. The store writes its metadata
// when the location has none; that write is kept in memory, so that an empty
// location reads as an empty state.
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

// ReadOnlyBackend wraps a backend so that it refuses every write. The one
// exception is its metadata: the store writes meta.json when the location has
// none, and that write is kept in memory so that an empty location reads as an
// empty state.
func ReadOnlyBackend(b Backend) Backend { return &readOnlyBackend{Backend: b} }
