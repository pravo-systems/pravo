package engine

import (
	"errors"
	"testing"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/state"
)

// A move whose source record was changed by another command after this one read it ends as
// the other races do: exit 3 (stale), with the plan to be made again, and nothing is moved (F41).
func TestMoveOfARecordChangedSinceItWasReadIsStale(t *testing.T) {
	h := newHarness(t)
	h.apply(spec{cue: "a: " + thing("x", `content: "one"`) + "\n", resources: []string{"a"}})
	e := h.engine(spec{cue: "c: " + thing("x", `content: "one"`) + "\n", resources: []string{"c"}}, Options{})
	e.Root.Moves = []config.Move{{From: "a", To: "c"}}
	// Another command writes the record: it is a new store over the same backend.
	st, err := state.OpenBackend(t.Context(), h.backend)
	if err != nil {
		t.Fatal(err)
	}
	h.state = st
	h.apply(spec{cue: "a: " + thing("x", `content: "two"`) + "\n", resources: []string{"a"}})

	_, err = e.moveNode("a", "c", e.Root.Resource("c"), false)
	if err == nil || !errors.Is(err, state.ErrChanged) {
		t.Fatalf("err = %v, want one wrapping state.ErrChanged", err)
	}
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != ExitStale {
		t.Errorf("err = %v, want exit code %d", err, ExitStale)
	}
	if h.state.Get("a") == nil {
		t.Error("the record left a")
	}
}
