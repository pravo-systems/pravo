package statetest_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/state/statetest"
)

func TestSealMakesAStateThatOnlyItsReadersOpen(t *testing.T) {
	if state.NewStoreIdentity != nil {
		t.Skip("the suite runs over sealed stores, and every open has the identity of the suite")
	}
	ctx := context.Background()
	b, err := state.NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ada, eve := statetest.Identity(t, "ada"), statetest.Identity(t, "eve")
	st := statetest.Seal(t, b, ada)
	if err := st.Set(&state.Resource{Mode: "managed", Type: "t", Name: "a", Attributes: json.RawMessage(`{"k":"v"}`)}, ""); err != nil {
		t.Fatal(err)
	}
	if got := st.Protection(); got.State != "sealed" || got.Format != state.FormatSealed {
		t.Errorf("Protection = %+v", got)
	}
	reader, err := state.OpenBackendWith(ctx, b, state.Options{Identity: ada})
	if err != nil || reader.Locked() || string(reader.Get("t.a").Attributes) != `{"k":"v"}` {
		t.Errorf("the reader's view: %v", err)
	}
	for _, id := range []*state.Options{{}, {Identity: eve}} {
		stranger, err := state.OpenBackendWith(ctx, b, *id)
		if err != nil || !stranger.Locked() || stranger.Get("t.a").Attributes != nil {
			t.Errorf("a stranger's view: locked %v, %v", stranger != nil && stranger.Locked(), err)
		}
		if err := stranger.Set(stranger.Get("t.a"), ""); !errors.Is(err, state.ErrNotReader) {
			t.Errorf("a stranger's write = %v", err)
		}
	}
}
