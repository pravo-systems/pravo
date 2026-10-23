package engine

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// The function that finds the identity is given the reader keys of the state whose
// outputs are read, so that a user with several identities gets the one that can
// read that state, and a different one for each state.
func TestRemoteOutputsGivesTheIdentityTheReadersOfTheOtherState(t *testing.T) {
	unsealedStart(t)
	ctx := context.Background()
	ada, err := protect.NewIdentity("ada", "")
	if err != nil {
		t.Fatal(err)
	}
	dan, err := protect.NewIdentity("dan", "")
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	forAda, forDan, plain := otherRoot(t, true, ada), otherRoot(t, true, dan), otherRoot(t, false, nil)

	var asked [][]protect.ReaderPublic
	e := h.engine(spec{}, Options{})
	e.Opts.Identity = func(readers []protect.ReaderPublic) (*protect.Identity, error) {
		asked = append(asked, readers)
		for _, id := range []*protect.Identity{ada, dan} {
			if slices.ContainsFunc(readers, id.Reader.Public().Equal) {
				return id, nil
			}
		}
		return nil, os.ErrNotExist
	}
	for i, c := range []struct {
		name   string
		root   state.Backend
		reader *protect.Identity
	}{{"ada", forAda, ada}, {"dan", forDan, dan}} {
		got, err := e.openRemoteOutputs(ctx, c.root)
		if err != nil || string(got["url"]) != `"https://example.com"` {
			t.Fatalf("the root of %s: %v %v", c.name, got, err)
		}
		if len(asked) != i+1 || len(asked[i]) != 1 || !asked[i][0].Equal(c.reader.Reader.Public()) {
			t.Errorf("the root of %s: asked %d times, the last with %v, want the key of %s only", c.name, len(asked), asked[len(asked)-1], c.name)
		}
	}
	// A state that is not sealed never asks.
	if _, err := e.openRemoteOutputs(ctx, plain); err != nil || len(asked) != 2 {
		t.Errorf("unsealed: %v, asked %d times", err, len(asked))
	}
}
