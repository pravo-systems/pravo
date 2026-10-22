package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// Once a collector has committed to the removal of a plan, no apply is let in, whatever its
// clock says: the commit is a write that the apply sees, and not a time that it compares.
func TestAnApplyIsRefusedOnceACollectorHasCommitted(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
		*clock = t0.Add(2 * time.Hour)
		ahead := NewPlanStore(ps.B)
		ahead.Now = func() time.Time { return clock.Add(claimTimeout + time.Hour) } // a clock that sees every claim lapsed
		var refused error
		fired := false
		hooked := &PlanStore{B: &hookBackend{Backend: ps.B, before: func(op, key string) {
			if !fired && op == "DeleteIf" && key == planKey(m.ID) {
				fired = true
				refused = ahead.BeginApply(ctx, m.ID, "alice#1")
			}
		}}, Now: ps.Now}
		res := collect(t, hooked, GCOptions{Owner: "collector#1"})
		if !fired || !errors.Is(refused, ErrPlanRemoving) {
			t.Errorf("fired %v, apply: %v", fired, refused)
		}
		if ids(res.Removed) != m.ID || exists(ps, m.ID) {
			t.Errorf("removed %+v, exists %v", res.Removed, exists(ps, m.ID))
		}
	})
}

// A committed claim does not lapse for applies and is hidden for good; another collection finishes
// the removal once the claim has not been renewed for claimTimeout, whatever the rules say then.
func TestACommittedRemovalIsFinishedByAnotherCollector(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0)) // not expired: the rules would keep it
		if err := ps.updateProgress(ctx, m.ID, func(p *PlanProgress) error {
			p.Collecting = &PlanClaim{By: "dead#1", At: t0, Committed: true}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		*clock = t0.Add(5 * time.Minute)
		if res := collect(t, ps, GCOptions{Owner: "me#2"}); len(res.Removed) != 0 {
			t.Errorf("within the claim: %+v", res.Removed)
		}
		*clock = t0.Add(26 * time.Hour)
		if err := ps.BeginApply(ctx, m.ID, "bob#3"); !errors.Is(err, ErrPlanRemoving) {
			t.Errorf("a committed claim lapsed for an apply: %v", err)
		}
		if es, _, _ := ps.EntriesAndSkipped(ctx, nil); len(es) != 0 {
			t.Errorf("a plan that is being removed is listed: %d", len(es))
		}
		if l, _ := ps.List(ctx, nil); len(l.Hidden) != 1 || !l.Hidden[0].Until.IsZero() {
			t.Errorf("hidden %+v", l.Hidden)
		}
		res := collect(t, ps, GCOptions{Owner: "me#2"})
		if ids(res.Removed) != m.ID || exists(ps, m.ID) {
			t.Errorf("after the claim went stale: %+v, exists %v", res.Removed, exists(ps, m.ID))
		}
	})
}

// A claim that is not committed still lapses, as before.
func TestAnUncommittedClaimStillLapses(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		if err := ps.updateProgress(ctx, m.ID, func(p *PlanProgress) error {
			p.Collecting = &PlanClaim{By: "slow#1", At: t0}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		*clock = t0.Add(claimTimeout + time.Second)
		if err := ps.BeginApply(ctx, m.ID, "bob#2"); err != nil {
			t.Errorf("%v", err)
		}
	})
}

// An apply that runs for longer than a claim lasts renews it, so that the plan stays in use.
func TestARunningApplyRenewsItsClaim(t *testing.T) {
	was := applyRenewEvery
	applyRenewEvery = 5 * time.Millisecond
	defer func() { applyRenewEvery = was }()
	for name, mk := range planBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			var now atomic.Int64
			now.Store(t0.UnixNano())
			ps := NewPlanStore(mk())
			ps.Now = func() time.Time { return time.Unix(0, now.Load()).UTC() }
			m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
			if err := ps.BeginApply(ctx, m.ID, "alice#1"); err != nil {
				t.Fatal(err)
			}
			stop := ps.KeepApply(ctx, m.ID, "alice#1")
			defer stop()
			now.Store(t0.Add(30 * time.Hour).UnixNano()) // the apply has run for longer than a claim lasts
			deadline := time.Now().Add(5 * time.Second)
			for {
				pr, _ := ps.Progress(ctx, m.ID)
				if len(pr.Applying) == 1 && !pr.Applying[0].At.Before(t0.Add(30*time.Hour)) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the claim was not renewed: %+v", pr.Applying)
				}
				time.Sleep(5 * time.Millisecond)
			}
			res, err := ps.GC(ctx, GCOptions{})
			if err != nil || len(res.Removed) != 0 || len(res.Kept) != 1 {
				t.Errorf("%+v %v", res, err)
			}
			// the end of the apply stops the renewal, and nothing renews a claim that is gone
			stop()
			ps.EndApply(ctx, m.ID, "alice#1")
			ps.RenewApply(ctx, m.ID, "alice#1")
			if pr, _ := ps.Progress(ctx, m.ID); len(pr.Applying) != 0 {
				t.Errorf("a claim that had ended was made again: %+v", pr.Applying)
			}
		})
	}
}
