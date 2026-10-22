package engine

import (
	"context"
	"testing"
	"time"
)

// A record that the plan only has as a no-op, and that other changes were planned
// from, makes the whole plan stale when it moves: no part of the plan is advertised
// as applicable.
func TestAMovedNoopThatNothingSelectsMakesThePlanStaleAsAWhole(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		noop := savedChange{Addr: "base", Mode: "managed", Type: "test_thing", Name: "base", Action: ActionNoop, ProviderKey: "test", PriorRevision: 1}
		m := putFile(t, ps, gcPlan(t0, noop, change("dep", ActionUpdate, 1), change("other", ActionUpdate, 1)), "alice", 0)
		e, err := ps.Entry(ctx, m.ID, stateAt(map[string]int{"base": 2, "dep": 1, "other": 1}))
		if err != nil {
			t.Fatal(err)
		}
		if e.Status != PlanStale || len(e.Live) != 0 {
			t.Errorf("status %s, live %v, reasons %v", e.Status, e.Live, e.Reasons)
		}
		// With the record as it was, the plan is pending.
		if e, _ = ps.Entry(ctx, m.ID, stateAt(map[string]int{"base": 1, "dep": 1, "other": 1})); e.Status != PlanPending {
			t.Errorf("unmoved: %s", e.Status)
		}
		// A change that moved is still only that change.
		if e, _ = ps.Entry(ctx, m.ID, stateAt(map[string]int{"base": 1, "dep": 2, "other": 1})); e.Status != PlanPartlyStale || len(e.Live) != 1 || e.Live[0] != "other" {
			t.Errorf("a change moved: %s, live %v", e.Status, e.Live)
		}
	})
}

// A move was made when the plan was; an apply of a part does not repeat it, so a plan
// whose only change that is not blocked is a move has nothing live.
func TestAMoveIsNotALiveChange(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		move := savedChange{Addr: "moved", Mode: "managed", Type: "test_thing", Name: "moved", Action: ActionMove, ProviderKey: "test", MovedFrom: "old"}
		m := putFile(t, ps, gcPlan(t0, move, change("a", ActionUpdate, 1)), "alice", 0)
		e, err := ps.Entry(ctx, m.ID, stateAt(map[string]int{"a": 2}))
		if err != nil {
			t.Fatal(err)
		}
		if e.Status != PlanStale || len(e.Live) != 0 {
			t.Errorf("status %s, live %v", e.Status, e.Live)
		}
	})
}

// An apply that failed does not say that an apply finished, whichever one finished before.
func TestAPartialMarkClearsTheTransactionOfAnApplyThatFinished(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0), change("b", ActionCreate, 0))
		if err := ps.MarkApplied(ctx, m.ID, []string{"a"}, "txn-1", []string{"a", "b"}); err != nil {
			t.Fatal(err)
		}
		if pr, _ := ps.Progress(ctx, m.ID); pr.ApplyTxn != "txn-1" {
			t.Fatalf("progress %+v", pr)
		}
		if err := ps.MarkPartlyApplied(ctx, m.ID, []string{"b"}, "txn-2"); err != nil {
			t.Fatal(err)
		}
		if pr, _ := ps.Progress(ctx, m.ID); pr.ApplyTxn != "" || len(pr.Applied) != 2 {
			t.Errorf("progress %+v", pr)
		}
	})
}
