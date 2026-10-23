package engine

import (
	"context"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// The count of sealed objects by group is what a collection of the state asks
// before it drops a group: two for each plan that is sealed, and none for a plan
// that is not.
func TestSealedGroupsCountsThePlanObjectsOfEachGroup(t *testing.T) {
	eachBackend2(t, func(t *testing.T, b state.Backend) {
		clock := t0
		s := newSealedStore(t, b, &clock, 2)
		ctx := context.Background()
		if got, err := s.SealedGroups(ctx); err != nil || len(got) != 0 {
			t.Fatalf("no plans = %v, %v", got, err)
		}
		putSecret(t, s.PlanStore, t0, change("a", ActionCreate, 0))
		putSecret(t, s.PlanStore, t0.Add(time.Minute), change("b", ActionCreate, 0))
		if got, err := s.SealedGroups(ctx); err != nil || len(got) != 1 || got["g1"] != 4 {
			t.Fatalf("two plans in g1 = %v, %v", got, err)
		}
		if _, err := s.meta.Rewrap(s.keys, s.readers[:1]); err != nil {
			t.Fatal(err)
		}
		putSecret(t, s.PlanStore, t0.Add(2*time.Minute), change("c", ActionCreate, 0))
		if got, err := s.SealedGroups(ctx); err != nil || got["g1"] != 4 || got["g2"] != 2 {
			t.Fatalf("a plan in g2 = %v, %v", got, err)
		}
		if _, _, err := s.ResealAll(ctx); err != nil {
			t.Fatal(err)
		}
		if got, err := s.SealedGroups(ctx); err != nil || got["g1"] != 0 || got["g2"] != 6 {
			t.Fatalf("after resealing = %v, %v", got, err)
		}

		// A plan that is not sealed is in no group.
		plain := NewPlanStore(s.B)
		putSecret(t, plain, t0.Add(3*time.Minute), change("d", ActionCreate, 0))
		if got, err := s.SealedGroups(ctx); err != nil || got["g2"] != 6 || len(got) != 1 {
			t.Fatalf("with a plan in the clear = %v, %v", got, err)
		}

		// A summary that cannot be read is an error, so that no group is dropped on it.
		if err := s.B.Put(ctx, metaKey2("plan-00000000"), []byte("{")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SealedGroups(ctx); err == nil {
			t.Error("a summary that cannot be read was counted as nothing")
		}
	})
}
