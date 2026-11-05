package serve

import (
	"context"
	"errors"
	"os"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/state"
)

// OpenReadOnly opens the state of a local backend for reading only. A location
// that holds none is read as an empty state, and nothing is created: opening a
// store otherwise makes the directories and writes its metadata. A caller that
// has a backend of another kind passes its own function in Options.OpenState.
func OpenReadOnly(ctx context.Context, root *config.Root) (*state.Store, error) {
	if root.State.Remote() {
		return nil, errors.New("the state is on a remote store: the caller has to open it")
	}
	path := root.StateFile()
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		// Windows reports a path below a file as absent, which would read as an
		// empty state: let state.Open say that the path is a file.
		return state.Open(path)
	}
	return state.OpenBackend(ctx, ReadOnly(&state.LocalBackend{Dir: path}))
}

// ReadOnly wraps a backend so that every write is refused; see state.ReadOnly.
func ReadOnly(b state.Backend) state.Backend { return state.ReadOnly(b) }
