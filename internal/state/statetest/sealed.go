package statetest

import (
	"context"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// Identity makes an identity for a test: a reader key and a signing key, in
// memory, that are cleared when the test ends.
func Identity(t testing.TB, name string) *protect.Identity {
	t.Helper()
	id, err := protect.NewIdentity(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(id.Close)
	return id
}

// Readers returns the identities as the list of readers a state is sealed to.
func Readers(ids ...*protect.Identity) []protect.Reader {
	out := make([]protect.Reader, len(ids))
	for i, id := range ids {
		out[i] = id.AsReader()
	}
	return out
}

// Seal seals the state in b to the identities, which opens as the first of them,
// and returns the store opened by it. The state is moved to format 3 as it is, so
// a test writes its records before it calls Seal, or writes them to the store it
// returns. The store b holds can be opened with any of the identities by
// state.OpenBackendWith, and with none it opens locked.
func Seal(t testing.TB, b state.Backend, ids ...*protect.Identity) *state.Store {
	t.Helper()
	if len(ids) == 0 {
		t.Fatal("statetest.Seal needs at least one identity")
	}
	ctx := context.Background()
	st, err := state.OpenBackendWith(ctx, b, state.Options{Identity: ids[0]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Upgrade(ctx, state.UpgradeOptions{To: state.FormatSealed, Readers: Readers(ids...), Identity: ids[0]}); err != nil {
		t.Fatal(err)
	}
	return st
}
