package engine

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A move changes the resource that it takes the record from: another plan that changes
// that resource overlaps with it.
func TestAMoveOverlapsWithAPlanThatChangesItsOldAddress(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		move := savedChange{Addr: "greeting", Mode: "managed", Type: "test_thing", Name: "greeting", Action: ActionMove, ProviderKey: "test", MovedFrom: "hello"}
		mover := putFile(t, ps, gcPlan(t0, move, change("greeting", ActionUpdate, 2)), "alice", 0)
		other := putPlan(t, ps, t0.Add(time.Second), "bob", 0, change("hello", ActionUpdate, 1))
		es, _ := ps.Entries(ctx, nil)
		over := OverlapsOf(es, other)
		if len(over) != 1 || over[0].Plan.ID != mover.ID || strings.Join(over[0].Addrs, ",") != "hello" {
			t.Fatalf("%+v", over)
		}
		// and the other way round
		if over := OverlapsOf(es, mover); len(over) != 1 || over[0].Plan.ID != other.ID || strings.Join(over[0].Addrs, ",") != "hello" {
			t.Errorf("%+v", over)
		}
		if got := Touching(es, "hello"); len(got) != 2 {
			t.Errorf("touching the old address: %d", len(got))
		}
	})
}

// A plan that reads a record is made stale by the plan that changes it, though it changes
// nothing there: the overlap says so, in the direction that is true of each plan.
func TestAPlanThatReadsWhatAnotherChangesOverlapsWithIt(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		writer := putPlan(t, ps, t0, "alice", 0, change("x", ActionUpdate, 1))
		pf := gcPlan(t0.Add(time.Second), change("y", ActionCreate, 0))
		pf.Inputs = map[string]int{"x": 1}
		reader := putFile(t, ps, pf, "bob", 0)
		es, _ := ps.Entries(ctx, nil)
		// seen from the reader: the writer changes what it reads
		if over := OverlapsOf(es, reader); len(over) != 1 || over[0].Plan.ID != writer.ID || strings.Join(over[0].Changes, ",") != "x" || len(over[0].Addrs) != 0 || len(over[0].ReadBy) != 0 {
			t.Errorf("from the reader: %+v", over)
		}
		// seen from the writer: the reader reads what it changes
		if over := OverlapsOf(es, writer); len(over) != 1 || over[0].Plan.ID != reader.ID || strings.Join(over[0].ReadBy, ",") != "x" || len(over[0].Addrs) != 0 || len(over[0].Changes) != 0 {
			t.Errorf("from the writer: %+v", over)
		}
		// Overlaps, which is about what two plans both change, does not mention it.
		if over := Overlaps(es, reader.ID, reader.Changes); len(over) != 0 {
			t.Errorf("Overlaps: %+v", over)
		}
	})
}

func TestBeingCollected(t *testing.T) {
	p := &PlanProgress{Collecting: &PlanClaim{By: "x", At: t0}}
	if !p.BeingCollected(t0.Add(time.Minute)) || p.BeingCollected(t0.Add(claimTimeout+time.Second)) || (&PlanProgress{}).BeingCollected(t0) {
		t.Error("the claim of a collector holds for claimTimeout")
	}
}
