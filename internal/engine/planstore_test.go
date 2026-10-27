package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/fakes3"
	"github.com/pravo-systems/pravo/internal/state"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// planBackends returns the backends that a plan store is tried on: a directory,
// and a bucket served by the in-repo fake of S3.
func planBackends(t *testing.T) map[string]func() state.Backend {
	t.Helper()
	return map[string]func() state.Backend{
		"local": func() state.Backend {
			b, err := state.NewLocalBackend(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			return b
		},
		"s3": func() state.Backend {
			srv := httptest.NewServer(fakes3.New())
			t.Cleanup(srv.Close)
			b, err := state.NewS3Backend(context.Background(), state.S3Config{
				Bucket: "state", Prefix: "team/app", Region: "us-east-1", Endpoint: srv.URL, PathStyle: true, AccessKey: "test", SecretKey: "test",
			})
			if err != nil {
				t.Fatal(err)
			}
			return b
		},
	}
}

// eachBackend runs a test on every backend, with a plan store whose clock is the
// returned pointer.
func eachBackend(t *testing.T, run func(t *testing.T, ps *PlanStore, clock *time.Time)) {
	t.Helper()
	for name, mk := range planBackends(t) {
		t.Run(name, func(t *testing.T) {
			clock := t0
			ps := NewPlanStore(mk())
			ps.Now = func() time.Time { return clock }
			run(t, ps, &clock)
		})
	}
}

// change is a change of a test plan on a record that is at revision rev.
func change(addr string, action Action, rev int) savedChange {
	return savedChange{Addr: addr, Mode: "managed", Type: "test_thing", Name: addr, Action: action, ProviderKey: "test", PriorRevision: rev}
}

// putPlan stores a plan that was made at created, and returns its summary.
func putPlan(t *testing.T, ps *PlanStore, created time.Time, author string, ttl time.Duration, changes ...savedChange) *PlanMeta {
	t.Helper()
	pf := &PlanFile{
		Format: PlanFileFormat, Created: created, Command: "pravo plan --store", Lineage: "lineage-1", ConfigFingerprint: "fp-1",
		Providers: map[string]string{"test": "1.0.0"}, Changes: changes, Tags: []string{"env=prod"},
	}
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

// stateAt is a check that finds every record at the given revision, 0 when it is
// not listed.
func stateAt(revisions map[string]int) *PlanCheck {
	return &PlanCheck{Lineage: "lineage-1", Versions: func(addrs []string) (map[string]state.Version, error) {
		out := map[string]state.Version{}
		for _, a := range addrs {
			out[a] = state.Version{Revision: revisions[a]}
		}
		return out, nil
	}}
}

func listKeys(t *testing.T, b state.Backend, prefix string) []string {
	t.Helper()
	ks, err := b.List(context.Background(), prefix)
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

func TestStoredPlanIsThreeObjectsAndTheIDIsItsDigest(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0), change("b", ActionUpdate, 3))
		if !IsPlanID(m.ID) || len(m.ID) != len("plan-")+planIDHex {
			t.Fatalf("id %q", m.ID)
		}
		data, err := ps.B.Get(context.Background(), planKey(m.ID))
		if err != nil {
			t.Fatal(err)
		}
		if got := digestID(data, planIDHex); got != m.ID {
			t.Errorf("the id is %s and the digest of the plan gives %s", m.ID, got)
		}
		if m.Size != len(data) {
			t.Errorf("size %d, object %d", m.Size, len(data))
		}
		want := []string{"plans/" + m.ID + ".meta.json", "plans/" + m.ID + ".plan.json"}
		if got := listKeys(t, ps.B, PlanDir); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("objects %v, want %v", got, want)
		}
		if m.Author != "alice" || m.Summary != (PlanCounts{Add: 1, Change: 1}) || len(m.Changes) != 2 || !m.Expires.Equal(t0.Add(DefaultPlanTTL)) {
			t.Errorf("summary %+v", m)
		}
		if m.Bases["a"].Revision != 0 || m.Bases["b"].Revision != 3 {
			t.Errorf("bases %+v", m.Bases)
		}
		if got := m.ApplyCommand(); got != "pravo apply "+m.ID+" -t env=prod" {
			t.Errorf("apply command %q", got)
		}
		// Reading a plan that nothing was done with finds an empty progress and writes none.
		pr, err := ps.Progress(context.Background(), m.ID)
		if err != nil || pr.ID != m.ID || len(pr.Applied) != 0 || pr.Applying != nil {
			t.Errorf("progress %+v, %v", pr, err)
		}
		if got := listKeys(t, ps.B, PlanDir); len(got) != 2 {
			t.Errorf("reading made objects: %v", got)
		}
	})
}

func TestTheSameBytesAreTheSamePlanAndAnIDCollisionLengthensTheID(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		pf := &PlanFile{Format: PlanFileFormat, Created: t0, Lineage: "l", Providers: map[string]string{}, Changes: []savedChange{change("a", ActionCreate, 0)}}
		data, _ := indentJSON(pf)
		first, second := newPlanMeta(pf, "alice", 0, len(data)), newPlanMeta(pf, "bob", 0, len(data))
		if err := ps.Put(ctx, data, first); err != nil {
			t.Fatal(err)
		}
		if err := ps.Put(ctx, data, second); err != nil {
			t.Fatal(err)
		}
		if first.ID != second.ID || len(listKeys(t, ps.B, PlanDir)) != 2 {
			t.Errorf("ids %s and %s, objects %v", first.ID, second.ID, listKeys(t, ps.B, PlanDir))
		}
		// The summary is written once: the second author does not replace the first.
		if m, _ := ps.Meta(ctx, first.ID); m.Author != "alice" {
			t.Errorf("the summary was replaced by %s", m.Author)
		}

		// Another plan that has the same short digest gets a longer id.
		other, _ := indentJSON(&PlanFile{Format: PlanFileFormat, Created: t0.Add(time.Hour), Lineage: "l", Providers: map[string]string{}, Changes: []savedChange{}})
		short := digestID(other, planIDHex)
		if ok, err := ps.B.PutIfAbsent(ctx, planKey(short), []byte(`{"squatter":true}`)); err != nil || !ok {
			t.Fatal(ok, err)
		}
		om := newPlanMeta(&PlanFile{Created: t0}, "carol", 0, len(other))
		if err := ps.Put(ctx, other, om); err != nil {
			t.Fatal(err)
		}
		if om.ID == short || !strings.HasPrefix(digestID(other, 64), om.ID) || len(om.ID) != len("plan-")+planIDHex+4 {
			t.Errorf("colliding id %s (short %s)", om.ID, short)
		}
	})
}

func TestResolveFindsAPlanByAUniquePrefix(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		var ids []string
		for i := range 12 {
			ids = append(ids, putPlan(t, ps, t0.Add(time.Duration(i)*time.Minute), "alice", 0, change(fmt.Sprintf("r%d", i), ActionCreate, 0)).ID)
		}
		if got, err := ps.Resolve(ctx, ids[3]); err != nil || got != ids[3] {
			t.Errorf("full id: %s, %v", got, err)
		}
		if got, err := ps.Resolve(ctx, ids[3][:len("plan-")+6]); err != nil || got != ids[3] {
			t.Errorf("prefix: %s, %v", got, err)
		}
		if _, err := ps.Resolve(ctx, "plan-"); err == nil {
			t.Error("a bare prefix is not an id")
		}
		if _, err := ps.Resolve(ctx, "plan-ffffff"); !errors.Is(err, ErrNoPlan) {
			t.Errorf("unknown id: %v", err)
		}
		if _, err := ps.Resolve(ctx, "plan.json"); err == nil || !strings.Contains(err.Error(), "not the id") {
			t.Errorf("not an id: %v", err)
		}
		// A prefix that fits several plans names them.
		ps.B.PutIfAbsent(ctx, planKey("plan-abcdef01"), []byte(`{}`))
		ps.B.PutIfAbsent(ctx, planKey("plan-abcdef02"), []byte(`{}`))
		if _, err := ps.Resolve(ctx, "plan-abcdef"); err == nil || !strings.Contains(err.Error(), "plan-abcdef01") || !strings.Contains(err.Error(), "plan-abcdef02") {
			t.Errorf("ambiguous prefix: %v", err)
		}
	})
}

func TestProgressMarksWhatWasAppliedAndThePlanFileShowsIt(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0), change("b", ActionCreate, 0), change("c", ActionUpdate, 2))
		original, _ := ps.B.Get(ctx, planKey(m.ID))

		if err := ps.BeginApply(ctx, m.ID, "alice#1"); err != nil {
			t.Fatal(err)
		}
		pr, _ := ps.Progress(ctx, m.ID)
		if len(pr.Applying) != 1 || pr.Applying[0].By != "alice#1" {
			t.Fatalf("no claim: %+v", pr)
		}
		*clock = t0.Add(time.Minute)
		if err := ps.MarkApplied(ctx, m.ID, []string{"a", "b"}, "txn-1", []string{"left"}); err != nil {
			t.Fatal(err)
		}
		ps.EndApply(ctx, m.ID, "alice#1")
		pf, pr, err := ps.ReadFile(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if pr.Applying != nil || pr.DoneAt != nil || pr.ApplyTxn != "txn-1" {
			t.Errorf("progress %+v", pr)
		}
		applied := map[string]string{}
		for _, c := range pf.Changes {
			if c.Applied {
				applied[c.Addr] = c.AppliedTxn
			}
		}
		if len(applied) != 2 || applied["a"] != "txn-1" || applied["b"] != "txn-1" || pf.ApplyTxn != "txn-1" {
			t.Errorf("applied %v, txn %q", applied, pf.ApplyTxn)
		}
		if e, _ := ps.Entry(ctx, m.ID, nil); e.Status != PlanPartial {
			t.Errorf("status %s", e.Status)
		}

		*clock = t0.Add(2 * time.Minute)
		if err := ps.MarkApplied(ctx, m.ID, []string{"c"}, "txn-2", []string{"c"}); err != nil {
			t.Fatal(err)
		}
		e, _ := ps.Entry(ctx, m.ID, nil)
		if e.Status != PlanApplied || e.Progress.DoneAt == nil || !e.Progress.DoneAt.Equal(t0.Add(2*time.Minute)) || len(e.Progress.Applied) != 3 {
			t.Errorf("entry %+v %+v", e.Status, e.Progress)
		}

		// The plan object itself never changed.
		if now, _ := ps.B.Get(ctx, planKey(m.ID)); string(now) != string(original) {
			t.Error("the plan object was rewritten")
		}
		// A pulled plan is a plan file with the same marks.
		path := filepath.Join(t.TempDir(), "pulled.json")
		if err := ps.Pull(ctx, m.ID, path); err != nil {
			t.Fatal(err)
		}
		file, err := ReadPlanFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(file.Changes) != 3 || !file.Changes[0].Applied || file.ApplyTxn != "txn-2" || file.Lineage != "lineage-1" {
			t.Errorf("pulled file %+v", file)
		}
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm()&0o077 != 0 && !isWindows() {
			t.Errorf("mode %v, %v", fi.Mode(), err)
		}
	})
}

func isWindows() bool { return filepath.Separator == '\\' }

func TestMarksOfApplyiesThatRunAtTheSameTimeAreAllKept(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		var changes []savedChange
		for i := range 8 {
			changes = append(changes, change(fmt.Sprintf("r%d", i), ActionCreate, 0))
		}
		m := putPlan(t, ps, t0, "alice", 0, changes...)
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := ps.MarkApplied(ctx, m.ID, []string{fmt.Sprintf("r%d", i)}, fmt.Sprintf("txn-%d", i), []string{"left"}); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		pr, err := ps.Progress(ctx, m.ID)
		if err != nil || len(pr.Applied) != 8 {
			t.Errorf("%d marks kept of 8: %+v, %v", len(pr.Applied), pr.Applied, err)
		}
	})
}

func TestStatusOfAPlan(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		fresh := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0), change("b", ActionUpdate, 4))
		short := putPlan(t, ps, t0.Add(time.Second), "bob", time.Hour, change("c", ActionCreate, 0))
		done := putPlan(t, ps, t0.Add(2*time.Second), "carol", 0, change("d", ActionCreate, 0))
		if err := ps.MarkApplied(ctx, done.ID, []string{"d"}, "txn", []string{"d"}); err != nil {
			t.Fatal(err)
		}
		half := putPlan(t, ps, t0.Add(3*time.Second), "dave", 0, change("e", ActionCreate, 0), change("f", ActionCreate, 0))
		if err := ps.MarkApplied(ctx, half.ID, []string{"e"}, "txn", []string{"left"}); err != nil {
			t.Fatal(err)
		}
		status := func(id string, check *PlanCheck) (PlanStatus, []string) {
			e, err := ps.Entry(ctx, id, check)
			if err != nil {
				t.Fatal(err)
			}
			return e.Status, e.Reasons
		}

		*clock = t0.Add(30 * time.Minute)
		current := stateAt(map[string]int{"b": 4})
		for id, want := range map[string]PlanStatus{fresh.ID: PlanPending, short.ID: PlanPending, done.ID: PlanApplied, half.ID: PlanPartial} {
			if got, why := status(id, current); got != want {
				t.Errorf("%s: %s (%v), want %s", id, got, why, want)
			}
		}

		// The record moved on: the change that expects the old revision conflicts, with
		// the reason, and the plan is stale in part.
		if got, why := status(fresh.ID, stateAt(map[string]int{"b": 5})); got != PlanPartlyStale || len(why) != 1 || !strings.Contains(why[0], "b") {
			t.Errorf("b moved: %s %v", got, why)
		}
		// A record that the plan creates, found by someone else.
		if got, _ := status(fresh.ID, stateAt(map[string]int{"a": 1, "b": 4})); got != PlanPartlyStale {
			t.Errorf("a was created: %s", got)
		}
		// The plan is stale when no change of it can be applied.
		if got, why := status(fresh.ID, stateAt(map[string]int{"a": 1, "b": 5})); got != PlanStale || len(why) != 2 {
			t.Errorf("a and b moved: %s %v", got, why)
		}
		if got, why := status(short.ID, stateAt(map[string]int{"c": 1})); got != PlanStale || len(why) != 1 {
			t.Errorf("the only change moved: %s %v", got, why)
		}
		// A change that was applied from the plan does not make it stale when its record moves.
		if got, _ := status(half.ID, stateAt(map[string]int{"e": 1})); got != PlanPartial {
			t.Errorf("e was applied from the plan: %s", got)
		}
		// Another state.
		other := stateAt(nil)
		other.Lineage = "lineage-2"
		if got, why := status(fresh.ID, other); got != PlanStale || !strings.Contains(why[0], "lineage") {
			t.Errorf("lineage: %s %v", got, why)
		}
		// With nothing to compare with no plan is stale.
		if got, _ := status(fresh.ID, nil); got != PlanPending {
			t.Errorf("no check: %s", got)
		}
		// A plan expires; one that was applied does not.
		*clock = t0.Add(90 * time.Minute)
		if got, _ := status(short.ID, current); got != PlanExpired {
			t.Errorf("past the ttl: %s", got)
		}
		*clock = t0.Add(DefaultPlanTTL + time.Hour)
		if got, _ := status(fresh.ID, current); got != PlanExpired {
			t.Errorf("past the default ttl: %s", got)
		}
		if got, _ := status(done.ID, current); got != PlanApplied {
			t.Errorf("applied: %s", got)
		}
	})
}

func TestEntriesAreNewestFirstAndLeaveOutWhatIsNotWhole(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		a := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		b := putPlan(t, ps, t0.Add(time.Hour), "bob", 0, change("b", ActionCreate, 0))
		c := putPlan(t, ps, t0.Add(2*time.Hour), "carol", 0, change("c", ActionCreate, 0))
		// A plan without a summary, a summary without a plan, and a plan that is being removed.
		ps.B.PutIfAbsent(ctx, planKey("plan-111111"), []byte(`{}`))
		ps.B.PutIfAbsent(ctx, metaKey2("plan-222222"), []byte(`{"id":"plan-222222"}`))
		*clock = t0.Add(3 * time.Hour)
		if err := ps.updateProgress(ctx, c.ID, func(p *PlanProgress) error {
			p.Collecting = &PlanClaim{By: "x", At: *clock}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		es, err := ps.Entries(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range es {
			got = append(got, e.Meta.ID)
		}
		if strings.Join(got, ",") != b.ID+","+a.ID {
			t.Errorf("entries %v, want %s, %s", got, b.ID, a.ID)
		}
		// The claim of a collector that went away lapses.
		*clock = t0.Add(3*time.Hour + 2*claimTimeout)
		if es, _ := ps.Entries(ctx, nil); len(es) != 3 {
			t.Errorf("after the claim lapsed: %d entries", len(es))
		}
	})
}

func TestOverlapsNameTheOpenPlansThatChangeTheSameResources(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		mine := putPlan(t, ps, t0, "alice", 0, change("a", ActionUpdate, 1), change("b", ActionCreate, 0))
		theirs := putPlan(t, ps, t0.Add(time.Minute), "bob", 0, change("b", ActionCreate, 0), change("c", ActionCreate, 0), change("a", ActionDelete, 1))
		unrelated := putPlan(t, ps, t0.Add(2*time.Minute), "carol", 0, change("z", ActionCreate, 0))
		stale := putPlan(t, ps, t0.Add(3*time.Minute), "dave", 0, change("a", ActionUpdate, 1))
		es, _ := ps.Entries(ctx, stateAt(map[string]int{"a": 2}))
		over := Overlaps(es, mine.ID, mine.Changes)
		// dave's plan expects a at revision 1 and the state is at 2, so it is stale and does not count. Bob's
		// plan is stale in part: its change of a conflicts and does not count, its change of b does.
		if len(over) != 1 || over[0].Plan.ID != theirs.ID || strings.Join(over[0].Addrs, ",") != "b" {
			t.Errorf("stale plans overlap: %+v", over)
		}
		es, _ = ps.Entries(ctx, stateAt(map[string]int{"a": 1}))
		over = Overlaps(es, mine.ID, mine.Changes)
		var ids []string
		for _, o := range over {
			ids = append(ids, o.Plan.ID+":"+strings.Join(o.Addrs, "+"))
		}
		if len(over) != 2 {
			t.Fatalf("overlaps %v", ids)
		}
		for _, o := range over {
			switch o.Plan.ID {
			case theirs.ID:
				if strings.Join(o.Addrs, ",") != "a,b" {
					t.Errorf("bob: %v", o.Addrs)
				}
			case stale.ID:
				if strings.Join(o.Addrs, ",") != "a" {
					t.Errorf("dave: %v", o.Addrs)
				}
			default:
				t.Errorf("unexpected %s", o.Plan.ID)
			}
		}
		if got := Touching(es, "z"); len(got) != 1 || got[0].Meta.ID != unrelated.ID {
			t.Errorf("touching z: %v", got)
		}
		if got := Touching(es, "a"); len(got) != 3 {
			t.Errorf("touching a: %d", len(got))
		}
	})
}

func TestOnlyObjectsOfPlansAreRecognised(t *testing.T) {
	for key, want := range map[string]string{
		"plans/plan-2f9c1a4e.plan.json":     "plan-2f9c1a4e plan",
		"plans/plan-2f9c1a4e.meta.json":     "plan-2f9c1a4e meta",
		"plans/plan-2f9c1a4e.progress.json": "plan-2f9c1a4e progress",
		"plans/plan-2f9c1a4e.json":          "",
		"plans/plan-2f9c1a.plan.json":       "plan-2f9c1a plan",
		"plans/plan-2f9c1.plan.json":        "",
		"plans/plan-2F9C1A4E.plan.json":     "",
		"plans/readme.txt":                  "",
		"plans/sub/plan-2f9c1a4e.plan.json": "",
		"plans/plan-2f9c1a4e.plan.json.tmp": "",
		"resources/plan-2f9c1a4e.plan.json": "",
		"plan-2f9c1a4e.plan.json":           "",
		"meta.json":                         "",
		"txn/plan-2f9c1a4e.plan.json":       "",
	} {
		id, kind, ok := planObject(key)
		got := ""
		if ok {
			got = id + " " + kind
		}
		if got != want {
			t.Errorf("%s: %q, want %q", key, got, want)
		}
	}
}

func TestStoringAPlanCarriesTheStateCopy(t *testing.T) {
	for name, mk := range planBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			src := mk()
			ps := NewPlanStore(src)
			m := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
			if err := ps.MarkApplied(ctx, m.ID, []string{"a"}, "txn", []string{"a"}); err != nil {
				t.Fatal(err)
			}
			dst, err := state.NewLocalBackend(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := state.Copy(ctx, src, dst); err != nil {
				t.Fatal(err)
			}
			back := NewPlanStore(dst)
			e, err := back.Entry(ctx, m.ID, nil)
			if err != nil || e.Status != PlanApplied || e.Meta.Author != "alice" {
				t.Fatalf("%+v, %v", e, err)
			}
			if _, _, err := back.ReadFile(ctx, m.ID); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestStoredPlanFormatsAreReadableJSON(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, _ *time.Time) {
		ctx := context.Background()
		m := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		if err := ps.BeginApply(ctx, m.ID, "alice#1"); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{planKey(m.ID), metaKey2(m.ID), progressKey(m.ID)} {
			data, err := ps.B.Get(ctx, k)
			if err != nil {
				t.Fatal(k, err)
			}
			var v map[string]any
			if err := json.Unmarshal(data, &v); err != nil {
				t.Errorf("%s: %v", k, err)
			}
		}
	})
}

// A plan that only changes policies keeps them in its summary, so that a listing does not
// read it as a plan that does nothing.
func TestSummaryOfAPlanKeepsItsPolicyChanges(t *testing.T) {
	pf := &PlanFile{Created: t0, Policies: []PolicyEntry{{Name: "a", Digest: "d1"}, {Name: "b", Base: 2, Digest: "d2"}, {Name: "c", Base: 1}}}
	m := newPlanMeta(pf, "alice", 0, 10)
	if len(m.Changes) != 0 || m.Summary != (PlanCounts{}) || len(m.Policies) != 3 {
		t.Fatalf("summary %+v", m)
	}
	var kinds []string
	for _, p := range m.Policies {
		kinds = append(kinds, p.Kind())
	}
	if strings.Join(kinds, ",") != "new,changed,removed" {
		t.Errorf("kinds %v", kinds)
	}
	if a, c, r := CountPolicyEntries(m.Policies); a != 1 || c != 1 || r != 1 {
		t.Errorf("counts %d %d %d", a, c, r)
	}
}
