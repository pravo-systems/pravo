package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// A command that opened the store before it was sealed does not store a plan in
// the clear into the sealed state.
func TestStoredPlanOfAStoreOpenedBeforeTheSeal(t *testing.T) {
	unsealedStart(t)
	ctx := context.Background()
	b, err := state.NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stale, err := state.OpenBackendWith(ctx, b, state.Options{NewFormat: state.FormatImmutable})
	if err != nil {
		t.Fatal(err)
	}
	id, err := protect.NewIdentity("ada", "")
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := state.OpenBackendWith(ctx, b, state.Options{Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sealer.Upgrade(ctx, state.UpgradeOptions{To: state.FormatSealed, Readers: []protect.Reader{id.AsReader()}, Identity: id}); err != nil {
		t.Fatal(err)
	}
	err = NewPlanStoreFor(stale).Put(ctx, []byte(`{"secret":"`+plantedValue+`"}`), &PlanMeta{})
	if !errors.Is(err, state.ErrUpgraded) {
		t.Errorf("Put from a store opened before the seal = %v, want ErrUpgraded", err)
	}
	if keys, _ := b.List(ctx, "plans"); len(keys) != 0 {
		t.Errorf("the sealed state holds %v", keys)
	}
}
