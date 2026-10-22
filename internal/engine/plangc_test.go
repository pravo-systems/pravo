package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// putFile stores a plan file and returns its summary.
func putFile(t *testing.T, ps *PlanStore, pf *PlanFile, author string, ttl time.Duration) *PlanMeta {
	t.Helper()
	data, err := indentJSON(pf)
	if err != nil {
		t.Fatal(err)
	}
	meta := newPlanMeta(pf, author, ttl, len(data))
	if err := ps.Put(context.Background(), data, meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

func gcPlan(created time.Time, changes ...savedChange) *PlanFile {
	return &PlanFile{
		Format: PlanFileFormat, Created: created, Command: "pravo plan --store", Lineage: "lineage-1", ConfigFingerprint: "fp-1",
		Providers: map[string]string{}, Changes: changes,
	}
}

// snapshot reads every object of the backend, for comparing before and after.
func snapshot(t *testing.T, b state.Backend) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, prefix := range []string{"plans", "resources", "txn", "locks"} {
		for _, k := range listKeys(t, b, prefix) {
			data, err := b.Get(context.Background(), k)
			if err != nil {
				t.Fatal(k, err)
			}
			out[k] = string(data)
		}
	}
	if data, err := b.Get(context.Background(), "meta.json"); err == nil {
		out["meta.json"] = string(data)
	}
	return out
}

func ids(items []GCItem) string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// sorted joins ids in the order that ids gives.
func sorted(id ...string) string {
	sort.Strings(id)
	return strings.Join(id, ",")
}

func exists(ps *PlanStore, id string) bool {
	for _, k := range []string{planKey(id), metaKey2(id), progressKey(id)} {
		if _, err := ps.B.Get(context.Background(), k); err == nil {
			return true
		}
	}
	return false
}

func collect(t *testing.T, ps *PlanStore, o GCOptions) *GCResult {
	t.Helper()
	res, err := ps.GC(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAnUnappliedPlanIsRemovedWhenItExpires(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		week := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		day := putPlan(t, ps, t0.Add(time.Second), "bob", 24*time.Hour, change("b", ActionCreate, 0))

		*clock = t0.Add(23 * time.Hour)
		if res := collect(t, ps, GCOptions{}); len(res.Removed) != 0 || res.Looked != 2 {
			t.Errorf("nothing expired yet: %+v", res)
		}
		*clock = t0.Add(25 * time.Hour)
		res := collect(t, ps, GCOptions{})
		if ids(res.Removed) != day.ID || !strings.HasPrefix(res.Removed[0].Reason, "expired 1 hour ago") || res.Removed[0].Author != "bob" {
			t.Errorf("after a day: %+v", res.Removed)
		}
		if exists(ps, day.ID) || !exists(ps, week.ID) {
			t.Errorf("objects: day %v, week %v", exists(ps, day.ID), exists(ps, week.ID))
		}
		*clock = t0.Add(DefaultPlanTTL + time.Minute)
		if res := collect(t, ps, GCOptions{}); ids(res.Removed) != week.ID {
			t.Errorf("after the default ttl: %+v", res.Removed)
		}
		if got := listKeys(t, ps.B, PlanDir); len(got) != 0 {
			t.Errorf("left over: %v", got)
		}
	})
}

func TestAnAppliedPlanIsKeptForAShorterTime(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		*clock = t0.Add(time.Hour)
		if err := ps.MarkApplied(ctx, m.ID, []string{"a"}, "txn", []string{"a"}); err != nil {
			t.Fatal(err)
		}
		*clock = t0.Add(time.Hour + 23*time.Hour)
		if res := collect(t, ps, GCOptions{}); len(res.Removed) != 0 {
			t.Errorf("kept for a day: %+v", res)
		}
		*clock = t0.Add(time.Hour + 25*time.Hour)
		if res := collect(t, ps, GCOptions{AppliedKeep: 48 * time.Hour}); len(res.Removed) != 0 {
			t.Errorf("kept for two days when asked: %+v", res)
		}
		res := collect(t, ps, GCOptions{})
		if ids(res.Removed) != m.ID || !strings.HasPrefix(res.Removed[0].Reason, "applied 1 day 1 hour ago") {
			t.Errorf("removed: %+v", res.Removed)
		}
		// A plan that was applied in part is not an applied plan: it expires like one that was not applied.
		half := putPlan(t, ps, *clock, "bob", 0, change("a", ActionCreate, 0), change("b", ActionCreate, 0))
		ps.MarkApplied(ctx, half.ID, []string{"a"}, "txn", []string{"left"})
		*clock = clock.Add(DefaultPlanTTL - time.Hour)
		if res := collect(t, ps, GCOptions{}); len(res.Removed) != 0 {
			t.Errorf("a plan applied in part within its ttl: %+v", res)
		}
		*clock = clock.Add(2 * time.Hour)
		if res := collect(t, ps, GCOptions{}); ids(res.Removed) != half.ID {
			t.Errorf("a plan applied in part past its ttl: %+v", res)
		}
	})
}

func TestAStalePlanIsMarkedThenRemovedAfterTheGrace(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionUpdate, 1))
		ok := putPlan(t, ps, t0.Add(time.Second), "bob", 0, change("z", ActionCreate, 0))
		moved := stateAt(map[string]int{"a": 2})

		*clock = t0.Add(time.Hour)
		res := collect(t, ps, GCOptions{Check: moved})
		if len(res.Removed) != 0 || ids(res.Marked) != m.ID || !strings.Contains(res.Marked[0].Reason, "a changed in state") {
			t.Fatalf("first sight: %+v", res)
		}
		if pr, _ := ps.Progress(ctx, m.ID); pr.StaleSince == nil || !pr.StaleSince.Equal(*clock) {
			t.Errorf("not marked: %+v", pr)
		}
		// Within the grace the plan stays, and the mark is not renewed.
		*clock = t0.Add(time.Hour + 30*time.Minute)
		res = collect(t, ps, GCOptions{Check: moved})
		if len(res.Removed) != 0 || len(res.Marked) != 0 {
			t.Errorf("within the grace: %+v", res)
		}
		if pr, _ := ps.Progress(ctx, m.ID); !pr.StaleSince.Equal(t0.Add(time.Hour)) {
			t.Errorf("the mark moved: %v", pr.StaleSince)
		}
		// A dry run says what the next collection removes and writes nothing.
		*clock = t0.Add(2*time.Hour + time.Minute)
		before := snapshot(t, ps.B)
		res = collect(t, ps, GCOptions{Check: moved, DryRun: true})
		if ids(res.Removed) != m.ID || !res.DryRun || fmt.Sprint(before) != fmt.Sprint(snapshot(t, ps.B)) {
			t.Errorf("dry run: %+v", res)
		}
		res = collect(t, ps, GCOptions{Check: moved})
		if ids(res.Removed) != m.ID || !strings.Contains(res.Removed[0].Reason, "stale for 1 hour") {
			t.Errorf("after the grace: %+v", res)
		}
		if exists(ps, m.ID) || !exists(ps, ok.ID) {
			t.Error("wrong plan removed")
		}
	})
}

func TestAPlanThatIsNotStaleAnyMoreLosesItsMark(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionUpdate, 1))
		*clock = t0.Add(time.Minute)
		collect(t, ps, GCOptions{Check: stateAt(map[string]int{"a": 2})})
		*clock = t0.Add(time.Hour)
		collect(t, ps, GCOptions{Check: stateAt(map[string]int{"a": 1})})
		if pr, _ := ps.Progress(ctx, m.ID); pr.StaleSince != nil {
			t.Errorf("the mark stayed: %v", pr.StaleSince)
		}
		// Stale again, it starts its grace again and is not removed on sight.
		*clock = t0.Add(3 * time.Hour)
		res := collect(t, ps, GCOptions{Check: stateAt(map[string]int{"a": 2})})
		if len(res.Removed) != 0 || ids(res.Marked) != m.ID {
			t.Errorf("%+v", res)
		}
	})
}

func TestWithoutACheckNoPlanIsStale(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		putPlan(t, ps, t0, "alice", 0, change("a", ActionUpdate, 1))
		*clock = t0.Add(24 * time.Hour)
		if res := collect(t, ps, GCOptions{}); len(res.Removed)+len(res.Marked) != 0 {
			t.Errorf("%+v", res)
		}
	})
}

func TestOlderThanRemovesUnappliedPlansBeforeTheyExpire(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		old := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		recent := putPlan(t, ps, t0.Add(5*time.Hour), "bob", 0, change("b", ActionCreate, 0))
		done := putPlan(t, ps, t0.Add(time.Second), "carol", 0, change("c", ActionCreate, 0))
		ps.MarkApplied(context.Background(), done.ID, []string{"c"}, "txn", []string{"c"})
		*clock = t0.Add(6 * time.Hour)
		res := collect(t, ps, GCOptions{OlderThan: 4 * time.Hour})
		if ids(res.Removed) != old.ID || !strings.Contains(res.Removed[0].Reason, "made more than 4 hours ago") {
			t.Errorf("removed %+v", res.Removed)
		}
		if !exists(ps, recent.ID) || !exists(ps, done.ID) {
			t.Error("a recent plan or an applied plan was removed")
		}
	})
}

func TestAPlanInUseIsNeverRemoved(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		applying := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
		collecting := putPlan(t, ps, t0.Add(time.Second), "bob", time.Hour, change("b", ActionCreate, 0))
		reserved := gcPlan(t0.Add(2*time.Second), change("c", ActionCreate, 0))
		reserved.Reservation = &ReservationView{Until: t0.Add(5 * time.Hour), Addrs: []string{"c"}}
		reservedMeta := putFile(t, ps, reserved, "carol", time.Hour)
		free := putPlan(t, ps, t0.Add(3*time.Second), "dave", time.Hour, change("d", ActionCreate, 0))

		*clock = t0.Add(2 * time.Hour)
		if err := ps.BeginApply(ctx, applying.ID, "alice#1"); err != nil {
			t.Fatal(err)
		}
		if err := ps.updateProgress(ctx, collecting.ID, func(p *PlanProgress) error {
			p.Collecting = &PlanClaim{By: "other#2", At: *clock}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		res := collect(t, ps, GCOptions{Owner: "me#3"})
		if ids(res.Removed) != free.ID {
			t.Errorf("removed %+v", res.Removed)
		}
		if ids(res.Kept) != sorted(applying.ID, collecting.ID, reservedMeta.ID) {
			t.Errorf("kept %+v", res.Kept)
		}
		for _, it := range res.Kept {
			if !strings.Contains(it.Reason, "kept: ") {
				t.Errorf("no reason: %+v", it)
			}
		}
		for _, id := range []string{applying.ID, collecting.ID, reservedMeta.ID} {
			if _, _, err := ps.ReadFile(ctx, id); err != nil {
				t.Errorf("%s was damaged: %v", id, err)
			}
		}

		// The reservation ends; the claim of a collector that went away lapses.
		*clock = t0.Add(5*time.Hour + time.Minute)
		res = collect(t, ps, GCOptions{Owner: "me#3"})
		if ids(res.Removed) != sorted(collecting.ID, reservedMeta.ID) || ids(res.Kept) != applying.ID {
			t.Errorf("later: removed %+v, kept %+v", res.Removed, res.Kept)
		}
		// An apply that did not say that it ended does not keep the plan for ever.
		*clock = t0.Add(2*time.Hour + applyingKeep + time.Minute)
		if res = collect(t, ps, GCOptions{Owner: "me#3"}); ids(res.Removed) != applying.ID {
			t.Errorf("a day later: %+v", res)
		}
	})
}

func TestAnApplyThatBeginsAfterTheClaimOfACollectorIsRefused(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		if err := ps.updateProgress(ctx, m.ID, func(p *PlanProgress) error {
			p.Collecting = &PlanClaim{By: "other#2", At: t0}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := ps.BeginApply(ctx, m.ID, "alice#1"); !errors.Is(err, ErrPlanRemoving) {
			t.Fatalf("%v", err)
		}
		// The claim of a collector that went away does not keep the plan from being used.
		*clock = t0.Add(claimTimeout + time.Second)
		if err := ps.BeginApply(ctx, m.ID, "alice#1"); err != nil {
			t.Fatal(err)
		}
		if pr, _ := ps.Progress(ctx, m.ID); pr.Collecting != nil || pr.Applying == nil {
			t.Errorf("%+v", pr)
		}
		// The end of an apply clears only its own claim.
		ps.EndApply(ctx, m.ID, "bob#9")
		if pr, _ := ps.Progress(ctx, m.ID); pr.Applying == nil {
			t.Error("another apply's end cleared the claim")
		}
		ps.EndApply(ctx, m.ID, "alice#1")
		if pr, _ := ps.Progress(ctx, m.ID); pr.Applying != nil {
			t.Error("the claim stayed")
		}
	})
}

func TestTheRemainsOfAPlanThatWasNotStoredWholeAreRemovedAfterAnHour(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		data, _ := indentJSON(gcPlan(t0, change("a", ActionCreate, 0)))
		planOnly := digestID(data, planIDHex)
		if ok, err := ps.B.PutIfAbsent(ctx, planKey(planOnly), data); err != nil || !ok {
			t.Fatal(ok, err)
		}
		metaOnly := "plan-aaaaaa00"
		ps.B.PutIfAbsent(ctx, metaKey2(metaOnly), []byte(`{"format":1,"id":"plan-aaaaaa00","created":"2026-10-01T09:00:00Z"}`))
		progressOnly := "plan-bbbbbb00"
		ps.B.PutIfAbsent(ctx, progressKey(progressOnly), []byte(`{"format":1,"id":"plan-bbbbbb00","updated":"2026-10-01T09:00:00Z"}`))

		*clock = t0.Add(30 * time.Minute)
		if res := collect(t, ps, GCOptions{}); len(res.Removed) != 0 || res.Looked != 3 {
			t.Errorf("a write may still be running: %+v", res)
		}
		*clock = t0.Add(61 * time.Minute)
		res := collect(t, ps, GCOptions{})
		if ids(res.Removed) != sorted(metaOnly, planOnly, progressOnly) {
			t.Errorf("removed %+v", res.Removed)
		}
		if got := listKeys(t, ps.B, PlanDir); len(got) != 0 {
			t.Errorf("left over: %v", got)
		}
	})
}

func TestCollectionRemovesNothingButObjectsOfPlans(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
		strangers := map[string]string{
			"plans/README.txt":                  "notes",
			"plans/plan-abc.plan.json":          "{}", // too short to be an id
			"plans/plan-0123456789.plan.json.1": "{}",
			"plans/sub/plan-aaaaaa00.plan.json": "{}",
			"resources/plan-aaaaaa00.plan.json": "{}",
			"resources/thing.json":              "{}",
			"txn/plan-aaaaaa00.progress.json":   "{}",
			"outputs.json":                      "{}",
		}
		for k, v := range strangers {
			if ok, err := ps.B.PutIfAbsent(ctx, k, []byte(v)); err != nil || !ok {
				t.Fatal(k, ok, err)
			}
		}
		// A plan object that is not what its name says is left alone.
		if err := ps.B.Put(ctx, planKey("plan-cccccc00"), []byte(`{"format":1}`)); err != nil {
			t.Fatal(err)
		}
		*clock = t0.Add(1000 * time.Hour)
		res := collect(t, ps, GCOptions{OlderThan: time.Minute})
		if ids(res.Removed) != m.ID {
			t.Errorf("removed %+v", res.Removed)
		}
		for k, v := range strangers {
			if data, err := ps.B.Get(ctx, k); err != nil || string(data) != v {
				t.Errorf("%s was damaged: %q, %v", k, data, err)
			}
		}
		if _, err := ps.B.Get(ctx, planKey("plan-cccccc00")); err != nil {
			t.Errorf("a plan object whose digest is not its id was removed: %v", err)
		}
	})
}

func TestADryRunChangesNothing(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		expired := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
		stale := putPlan(t, ps, t0.Add(time.Second), "bob", 0, change("b", ActionUpdate, 1))
		done := putPlan(t, ps, t0.Add(2*time.Second), "carol", 0, change("c", ActionCreate, 0))
		ps.MarkApplied(ctx, done.ID, []string{"c"}, "txn", []string{"c"})
		*clock = t0.Add(48 * time.Hour)
		before := snapshot(t, ps.B)
		res := collect(t, ps, GCOptions{DryRun: true, Check: stateAt(map[string]int{"b": 2})})
		if ids(res.Removed) != sorted(expired.ID, done.ID) || ids(res.Marked) != stale.ID || !res.DryRun {
			t.Errorf("%+v", res)
		}
		if after := snapshot(t, ps.B); fmt.Sprint(before) != fmt.Sprint(after) {
			t.Errorf("a dry run wrote:\nbefore %v\nafter  %v", before, after)
		}
	})
}

func TestACollectionRemovesAtMostTheLimit(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		for i := 0; i < 6; i++ {
			putPlan(t, ps, t0.Add(time.Duration(i)*time.Second), "alice", time.Hour, change(fmt.Sprintf("r%d", i), ActionCreate, 0))
		}
		*clock = t0.Add(2 * time.Hour)
		if res := collect(t, ps, GCOptions{Limit: 4}); len(res.Removed) != 4 {
			t.Errorf("removed %d", len(res.Removed))
		}
		if res := collect(t, ps, GCOptions{Limit: 4}); len(res.Removed) != 2 {
			t.Errorf("removed %d", len(res.Removed))
		}
	})
}

func TestCollectorsThatRunAtTheSameTimeRemoveEachPlanOnce(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		const plans = 12
		var want []string
		for i := 0; i < plans; i++ {
			want = append(want, putPlan(t, ps, t0.Add(time.Duration(i)*time.Second), "alice", time.Hour, change(fmt.Sprintf("r%d", i), ActionCreate, 0)).ID)
		}
		sort.Strings(want)
		*clock = t0.Add(2 * time.Hour)
		results := make([]*GCResult, 4)
		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := ps.GC(context.Background(), GCOptions{Owner: fmt.Sprintf("collector#%d", i)})
				if err != nil {
					t.Error(err)
				}
				results[i] = res
			}()
		}
		wg.Wait()
		var got []string
		for _, res := range results {
			for _, it := range res.Removed {
				got = append(got, it.ID)
			}
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("removed\n%v\nwant\n%v", got, want)
		}
		if left := listKeys(t, ps.B, PlanDir); len(left) != 0 {
			t.Errorf("left over: %v", left)
		}
	})
}

// An apply that has begun is not damaged by a collection, and an apply that begins
// after a collection claimed the plan is refused: in no order of the two does an apply
// find its plan gone after it began.
func TestAnApplyAndACollectionThatRunAtTheSameTimeDoNotCorruptEachOther(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		const plans = 16
		var metas []*PlanMeta
		for i := 0; i < plans; i++ {
			metas = append(metas, putPlan(t, ps, t0.Add(time.Duration(i)*time.Second), "alice", time.Hour, change(fmt.Sprintf("r%d", i), ActionCreate, 0)))
		}
		*clock = t0.Add(2 * time.Hour) // every plan is past its ttl, so the collector wants all of them
		type outcome struct {
			began, read bool
			beginErr    error
		}
		outcomes := make([]outcome, plans)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range metas {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				o := &outcomes[i]
				o.beginErr = ps.BeginApply(ctx, metas[i].ID, fmt.Sprintf("apply#%d", i))
				if o.beginErr != nil {
					return
				}
				o.began = true
				_, _, err := ps.ReadFile(ctx, metas[i].ID)
				o.read = err == nil
			}()
		}
		var res *GCResult
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var err error
			if res, err = ps.GC(ctx, GCOptions{Owner: "collector#1"}); err != nil {
				t.Error(err)
			}
		}()
		close(start)
		wg.Wait()

		removed := map[string]bool{}
		for _, it := range res.Removed {
			removed[it.ID] = true
		}
		var applied, refused, gone int
		for i, m := range metas {
			o := outcomes[i]
			switch {
			case o.began && o.read:
				applied++
				// The apply holds the plan: the collector did not remove it, and it is still whole.
				if removed[m.ID] {
					t.Errorf("%s was removed while an apply used it", m.ID)
				}
				if _, _, err := ps.ReadFile(ctx, m.ID); err != nil {
					t.Errorf("%s: %v", m.ID, err)
				}
				if pr, _ := ps.Progress(ctx, m.ID); pr.Applying == nil {
					t.Errorf("%s: the claim of the apply is gone", m.ID)
				}
			case errors.Is(o.beginErr, ErrPlanRemoving):
				refused++
				if !removed[m.ID] {
					t.Errorf("%s: refused because of a collection that did not remove it", m.ID)
				}
			default:
				// The plan was removed before the apply looked for it.
				gone++
				if !removed[m.ID] {
					t.Errorf("%s: began %v, read %v, error %v, and the collector did not remove it", m.ID, o.began, o.read, o.beginErr)
				}
			}
		}
		t.Logf("%d applies held their plan, %d were refused, %d found it gone", applied, refused, gone)
		// What the collector reported removed is gone; the rest is whole.
		for _, m := range metas {
			if removed[m.ID] {
				for _, k := range []string{planKey(m.ID), metaKey2(m.ID)} {
					if _, err := ps.B.Get(ctx, k); err == nil {
						t.Errorf("%s is reported removed and %s is there", m.ID, k)
					}
				}
			}
		}
	})
}

func TestAnApplyThatBeginsOnAPlanThatIsGoneFindsNoPlan(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
		*clock = t0.Add(2 * time.Hour)
		collect(t, ps, GCOptions{})
		if _, err := ps.Resolve(ctx, m.ID); !errors.Is(err, ErrNoPlan) {
			t.Errorf("resolve: %v", err)
		}
		if _, _, err := ps.ReadFile(ctx, m.ID); !errors.Is(err, ErrNoPlan) {
			t.Errorf("read: %v", err)
		}
		// A claim made on a plan that was removed in between is cleaned up by the next collection.
		if err := ps.BeginApply(ctx, m.ID, "alice#1"); err != nil {
			t.Fatal(err)
		}
		ps.EndApply(ctx, m.ID, "alice#1")
		*clock = t0.Add(4 * time.Hour)
		collect(t, ps, GCOptions{})
		if got := listKeys(t, ps.B, PlanDir); len(got) != 0 {
			t.Errorf("left over: %v", got)
		}
	})
}

// hookBackend runs a function before the writes of a backend, to put another
// process's step between two steps of the code under test.
type hookBackend struct {
	state.Backend
	before func(op, key string)
}

func (h *hookBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	h.before("PutIfAbsent", key)
	return h.Backend.PutIfAbsent(ctx, key, data)
}

func (h *hookBackend) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	h.before("ReplaceIf", key)
	return h.Backend.ReplaceIf(ctx, key, update)
}

func (h *hookBackend) DeleteIf(ctx context.Context, key string, match func([]byte) bool) (bool, error) {
	h.before("DeleteIf", key)
	return h.Backend.DeleteIf(ctx, key, match)
}

func TestAnApplyThatBeginsBetweenTheJudgementAndTheClaimKeepsThePlan(t *testing.T) {
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
			t.Errorf("the plan was damaged: %v", err)
		}
		if pr, _ := ps.Progress(ctx, m.ID); pr.Applying == nil || pr.Collecting != nil {
			t.Errorf("progress %+v", pr)
		}
	})
}

func TestAnApplyThatBeginsAfterTheClaimButBeforeTheDeletionIsRefused(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
		*clock = t0.Add(2 * time.Hour)
		other := NewPlanStore(ps.B)
		other.Now = ps.Now
		var refused error
		fired := false
		hooked := &PlanStore{B: &hookBackend{Backend: ps.B, before: func(op, key string) {
			if !fired && op == "DeleteIf" && key == planKey(m.ID) {
				fired = true
				refused = other.BeginApply(ctx, m.ID, "alice#1")
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

func TestACollectorThatFindsThePlanGoneAfterItsClaimReportsNothing(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		m := putPlan(t, ps, t0, "alice", time.Hour, change("a", ActionCreate, 0))
		*clock = t0.Add(2 * time.Hour)
		other := NewPlanStore(ps.B)
		other.Now = ps.Now
		fired := false
		hooked := &PlanStore{B: &hookBackend{Backend: ps.B, before: func(op, key string) {
			if !fired && key == progressKey(m.ID) {
				fired = true
				if res := collect(t, other, GCOptions{Owner: "collector#2"}); ids(res.Removed) != m.ID {
					t.Errorf("the other collector: %+v", res)
				}
			}
		}}, Now: ps.Now}
		res := collect(t, hooked, GCOptions{Owner: "collector#1"})
		if len(res.Removed) != 0 {
			t.Errorf("reported a removal that another collector made: %+v", res.Removed)
		}
		if got := listKeys(t, ps.B, PlanDir); len(got) != 0 {
			t.Errorf("left over: %v", got)
		}
	})
}
