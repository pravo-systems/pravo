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
	"github.com/pravo-systems/pravo/internal/state/statetest"
)

// newLocks returns the set of locks of an apply on the store st.
func newLocks(st *state.Store) *heldLocks {
	return newHeldLocks(st, func(string, ...any) {}, time.Hour)
}

// An apply that finds a lock held looks at it before it gives up, if the command
// that holds it has not begun to apply, and does not if it has.
func TestApplyLooksAtALockBeforeItGivesUp(t *testing.T) {
	patient := 2 * time.Second
	was := minPatience
	minPatience = patient
	t.Cleanup(func() { minPatience = was })
	addrs := []string{"a", "b", "c"}

	t.Run("a holder that is taking its locks lets go", func(t *testing.T) {
		h := newHarness(t)
		other := h.process()
		if err := other.Lock("b", "pravo apply", "taking"); err != nil {
			t.Fatal(err)
		}
		go func() {
			time.Sleep(60 * time.Millisecond)
			_ = other.Unlock("b")
		}()
		held := newLocks(h.process())
		defer held.release()
		begun := time.Now()
		if err := held.acquire(addrs, "pravo apply", "mine"); err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if took := time.Since(begun); took < 50*time.Millisecond || took > patient {
			t.Errorf("acquire took %s: it should wait for the lock that was released after 60ms", took)
		}
		if got := held.held(); len(got) != 3 {
			t.Errorf("holds %v", got)
		}
	})

	t.Run("a holder that applies rejects it at once", func(t *testing.T) {
		h := newHarness(t)
		other := h.process()
		txn := other.Begin("apply", "pravo apply")
		if err := other.Lock("b", "pravo apply", txn.ID()); err != nil {
			t.Fatal(err)
		}
		if err := txn.Start(); err != nil {
			t.Fatal(err)
		}
		held := newLocks(h.process())
		defer held.release()
		begun := time.Now()
		err := held.acquire(addrs, "pravo apply", "mine")
		if exitCodeOf(err) != ExitLocked || !strings.Contains(err.Error(), "apply rejected: b is locked by") {
			t.Fatalf("acquire: %v, want exit code 4 for b", err)
		}
		if took := time.Since(begun); took > patient/2 {
			t.Errorf("the rejection took %s: a lock held by an apply that has begun is not worth waiting for", took)
		}
		if got := held.held(); len(got) != 0 {
			t.Errorf("the rejected apply holds %v", got)
		}
	})

	t.Run("a reservation rejects it at once", func(t *testing.T) {
		h := newHarness(t)
		bob := h.process()
		bob.SetActor("bob", "", "")
		if _, err := bob.ReserveAll([]string{"b"}, "pravo plan --reserve 1h", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		held := newLocks(h.process())
		defer held.release()
		begun := time.Now()
		err := held.acquire(addrs, "pravo apply", "mine")
		if exitCodeOf(err) != ExitLocked || !strings.Contains(err.Error(), "b is reserved by bob") {
			t.Fatalf("acquire: %v", err)
		}
		if took := time.Since(begun); took > patient/2 {
			t.Errorf("the rejection took %s", took)
		}
	})

	t.Run("a holder that stays is waited for as long as the patience", func(t *testing.T) {
		minPatience = 150 * time.Millisecond
		defer func() { minPatience = patient }()
		// The patience is several times the time that one try took, so the
		// longest wait that is right follows the speed of the machine: a try on a
		// slow runner takes tens of milliseconds, and ten of them are a second.
		free := newLocks(newHarness(t).process())
		tried := time.Now()
		if err := free.acquire(addrs, "pravo apply", "free"); err != nil {
			t.Fatal(err)
		}
		try := time.Since(tried)
		free.release()
		longest := min(time.Second+patienceTries*3*try, maxPatience+time.Second)

		h := newHarness(t)
		other := h.process()
		if err := other.Lock("b", "pravo apply", "taking"); err != nil {
			t.Fatal(err)
		}
		held := newLocks(h.process())
		defer held.release()
		h.backend.Reset()
		begun := time.Now()
		err := held.acquire(addrs, "pravo apply", "mine")
		if exitCodeOf(err) != ExitLocked {
			t.Fatalf("acquire: %v, want exit code 4", err)
		}
		if took := time.Since(begun); took < 100*time.Millisecond || took > longest {
			t.Errorf("the rejection took %s, want about the patience of %s (at most %s)", took, minPatience, longest)
		}
		if got := h.backend.Count("PutIfAbsent", "locks"); got != 3 {
			t.Errorf("%d locks were tried, want the 3 of one try: nothing is tried again while the lock on b is held", got)
		}
		if locks, _ := other.Locks(); len(locks) != 1 || locks[0].Addr != "b" {
			t.Errorf("locks left %v, want the lock of the other command", locks)
		}
	})
}

// The error of an apply that needs more locks than it can look at in time says
// that at least so many are held.
func TestRejectionCountsOnlyWhatItCanLookAt(t *testing.T) {
	was, wasMin := scanTime, minPatience
	scanTime, minPatience = 15*time.Millisecond, time.Millisecond
	t.Cleanup(func() { scanTime, minPatience = was, wasMin })
	h := newHarnessOver(t, func(b state.Backend) state.Backend { return statetest.NewLatency(b, 10*time.Millisecond) })
	other := h.process()
	var addrs []string
	for i := 0; i < 96; i++ {
		addrs = append(addrs, fmt.Sprintf("r%03d", i))
	}
	inParallel(len(addrs), lockWidth, func(i int) {
		if err := other.Lock(addrs[i], "pravo apply", ""); err != nil {
			t.Error(err)
		}
	})
	held := newLocks(h.process())
	defer held.release()
	err := held.acquire(addrs, "pravo apply", "mine")
	if exitCodeOf(err) != ExitLocked || !strings.Contains(err.Error(), "apply rejected: r000 is locked by") || !strings.Contains(err.Error(), "; at least ") || !strings.HasSuffix(err.Error(), " other nodes are held as well") {
		t.Fatalf("acquire: %v", err)
	}
}

// A plan is made against a record at a revision in a generation. A record that is
// removed and created again starts at revision 1 again, and a plan made against
// the record it replaced is not made against it.
func TestPlanIsStaleWhenARecordWasRemovedAndCreatedAgain(t *testing.T) {
	two := func(withR bool, content string) spec {
		cue := "q: " + thing("q", `content: "q"`) + "\n"
		res := []string{"q"}
		if withR {
			cue += "r: " + thing("r", fmt.Sprintf("content: %q", content)) + "\n"
			res = append(res, "r")
		}
		return spec{cue: cue, resources: res}
	}

	t.Run("the plan an apply waited on", func(t *testing.T) {
		h := newHarness(t)
		h.apply(two(true, "v1"))
		first := h.process().Get("r")
		e := h.engineOn(h.process(), two(true, "v2"), Options{})
		p := plan(t, e)
		if len(p.Write) != 1 || p.Write[0] != "r" {
			t.Fatalf("the plan writes %v", p.Write)
		}
		h.apply(two(false, ""))
		h.apply(two(true, "other"))
		again := h.process().Get("r")
		if again.Revision != first.Revision || again.Generation == first.Generation {
			t.Fatalf("r was at revision %d in generation %q and is at revision %d in generation %q: it must be the same revision in another generation", first.Revision, first.Generation, again.Revision, again.Generation)
		}
		h.fake.resetCalls()
		_, err := e.Apply(context.Background(), p)
		if exitCodeOf(err) != ExitStale || !strings.Contains(err.Error(), "r was removed and created again in state") {
			t.Fatalf("apply: %v, want exit code 3 and the reason", err)
		}
		expectCalls(t, h, map[string]int{"ApplyResourceChange": 0})
		if got := recordedContent(t, &harness{state: h.process()}, "r"); got != "other" {
			t.Errorf("r holds %q after the refused apply", got)
		}
	})

	t.Run("a saved plan, when it is loaded", func(t *testing.T) {
		h := newHarness(t)
		h.apply(two(true, "v1"))
		s := two(true, "v2")
		_, pf := planned(t, h, s)
		h.apply(two(false, ""))
		h.apply(two(true, "other"))
		reasons := staleReasons(t, h, s, pf)
		if len(reasons) != 1 || !strings.HasPrefix(reasons[0], "r was removed and created again in state (revision 1") {
			t.Fatalf("reasons %q", reasons)
		}
	})

	t.Run("a saved plan, after it was loaded", func(t *testing.T) {
		h := newHarness(t)
		h.apply(two(true, "v1"))
		s := two(true, "v2")
		_, pf := planned(t, h, s)
		e := h.engine(s, Options{Refresh: RefreshNone})
		loaded, err := e.LoadPlan(pf)
		if err != nil {
			t.Fatal(err)
		}
		h.apply(two(false, ""))
		h.apply(two(true, "other"))
		_, err = e.ApplySaved(context.Background(), loaded)
		var stale *ErrStalePlan
		if exitCodeOf(err) != ExitStale || !errors.As(err, &stale) || len(stale.Reasons) != 1 || !strings.Contains(stale.Reasons[0], "r was removed and created again") {
			t.Fatalf("apply: %v, want exit code 3 and the reason", err)
		}
	})

	t.Run("a record the plan reads", func(t *testing.T) {
		mk := func(suffix string) spec {
			return spec{
				cue: "in: " + thing("in", "") + "\n" +
					"w: " + thing("w", `content: "\(in.id)-`+suffix+`"`) + "\n",
				resources: []string{"in", "w"},
			}
		}
		h := newHarness(t)
		h.apply(mk("1"))
		e := h.engineOn(h.process(), mk("2"), Options{})
		p := plan(t, e)
		if _, ok := p.Inputs["in"]; !ok {
			t.Fatalf("the plan does not read in: %v", p.Inputs)
		}
		// The record is removed and created again, with the same content.
		other := h.process()
		old := other.Get("in")
		if _, err := other.Remove("in"); err != nil {
			t.Fatal(err)
		}
		copied := *old
		copied.Revision, copied.Generation = 0, ""
		if err := other.Set(&copied, "other-txn"); err != nil {
			t.Fatal(err)
		}
		if got := other.Get("in"); got.Revision != old.Revision || got.Generation == old.Generation {
			t.Fatalf("in is at revision %d in %q, was at %d in %q", got.Revision, got.Generation, old.Revision, old.Generation)
		}
		_, err := e.Apply(context.Background(), p)
		if exitCodeOf(err) != ExitStale || !strings.Contains(err.Error(), "in was removed and created again in state") || !strings.Contains(err.Error(), "the plan reads it") {
			t.Fatalf("apply: %v, want exit code 3 for the record the plan reads", err)
		}
	})
}

// What a refresh read belongs to the record the command holds. A command whose
// view is older than the state does not write it over a record that an apply
// finished since.
func TestRefreshFromAnOlderViewIsNotWritten(t *testing.T) {
	mk := func(content string) spec {
		return spec{cue: "r: " + thing("r", fmt.Sprintf("content: %q", content)) + "\n", resources: []string{"r"}}
	}
	h := newHarness(t)
	h.apply(mk("v1"))
	// P opens the state now, with r at revision 1 ...
	var (
		mu    sync.Mutex
		lines []string
	)
	progress := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	p := h.engineOn(h.process(), mk("v3"), Options{DefaultRefresh: "always", Progress: progress})
	// ... and A applies v2 and finishes: revision 2.
	a := h.engineOn(h.process(), mk("v2"), Options{})
	apply(t, a, plan(t, a))
	before := h.process().Get("r")
	if before.Revision != 2 {
		t.Fatalf("r is at revision %d after A", before.Revision)
	}
	// The real world holds v2, which P's view calls drift.
	h.fake.drift("r", "v2")
	h.fake.resetCalls()
	if _, err := p.Plan(context.Background()); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if h.fake.called("ReadResource") != 1 {
		t.Fatalf("the plan read r %d times, want once", h.fake.called("ReadResource"))
	}
	after := h.process().Get("r")
	if after.Revision != before.Revision || after.ConfigHash != before.ConfigHash || after.DriftedAt != nil || !after.AppliedAt.Equal(*before.AppliedAt) || after.Txn != before.Txn {
		t.Errorf("a refresh from an older view wrote the record:\nbefore %+v\nafter  %+v", before, after)
	}
	found := false
	for _, l := range lines {
		found = found || strings.HasPrefix(l, "Warning: r changed in the state since this command read it")
	}
	if !found {
		t.Errorf("the plan did not say that it left r alone: %q", lines)
	}
}

// A move writes the records that depend on the one that moves, so it takes their
// locks, and it does not write a record from a copy that is older than the state.
func TestMoveWritesDependentsUnderTheirLocks(t *testing.T) {
	estate := func(suffix string) spec {
		return spec{
			cue: "base: " + thing("base", "") + "\n" +
				"d1: " + thing("d1", `content: "\(base.id)-`+suffix+`"`) + "\n" +
				"d2: " + thing("d2", `content: "\(base.id)-`+suffix+`"`) + "\n",
			resources: []string{"base", "d1", "d2"},
		}
	}
	newMove := func(t *testing.T) (*harness, *Engine) {
		h := newHarness(t)
		h.apply(estate("1"))
		e := h.engineOn(h.process(), estate("1"), Options{Command: "pravo plan"})
		e.txn = e.State.Begin("plan", "pravo plan")
		return h, e
	}

	t.Run("a dependent that another command holds", func(t *testing.T) {
		h, e := newMove(t)
		other := h.process()
		if err := other.Lock("d2", "pravo apply", ""); err != nil {
			t.Fatal(err)
		}
		_, err := e.moveNode("base", "moved", nil, false)
		if err == nil || !strings.Contains(err.Error(), "d2 depends on base") || !strings.Contains(err.Error(), "d2 is locked by pravo apply") {
			t.Fatalf("moveNode: %v, want the lock of d2", err)
		}
		now := h.process()
		if now.Get("base") == nil || now.Get("moved") != nil || len(now.Get("d1").Dependencies) != 1 || now.Get("d1").Dependencies[0] != "base" {
			t.Errorf("the move changed the state: base %v, moved %v, d1 %v", now.Get("base"), now.Get("moved"), now.Get("d1").Dependencies)
		}
		if locks, _ := now.Locks(); len(locks) != 1 || locks[0].Addr != "d2" {
			t.Errorf("locks left %v, want the one of the other command", locks)
		}
	})

	t.Run("a dependent that another command wrote", func(t *testing.T) {
		h, e := newMove(t)
		a := h.engineOn(h.process(), estate("2"), Options{})
		apply(t, a, plan(t, a)) // writes d1 and d2
		_, err := e.moveNode("base", "moved", nil, false)
		if err == nil || !strings.Contains(err.Error(), "changed in the state since it was read") {
			t.Fatalf("moveNode: %v, want the refusal to write from a copy that is older", err)
		}
		now := h.process()
		if now.Get("base") == nil || now.Get("moved") != nil {
			t.Errorf("the move changed the state")
		}
		if got := recordedContent(t, &harness{state: now}, "d1"); !strings.HasSuffix(got, "-2") {
			t.Errorf("d1 holds %q: what the other command wrote is gone", got)
		}
		if locks, _ := now.Locks(); len(locks) != 0 {
			t.Errorf("locks left %v", locks)
		}
	})

	t.Run("nothing in the way", func(t *testing.T) {
		h, e := newMove(t)
		ch, err := e.moveNode("base", "moved", nil, false)
		if err != nil || ch == nil {
			t.Fatalf("moveNode: %v, %v", ch, err)
		}
		now := h.process()
		if now.Get("base") != nil || now.Get("moved") == nil {
			t.Fatalf("base %v, moved %v", now.Get("base"), now.Get("moved"))
		}
		for _, d := range []string{"d1", "d2"} {
			if deps := now.Get(d).Dependencies; len(deps) != 1 || deps[0] != "moved" {
				t.Errorf("%s depends on %v, want moved", d, deps)
			}
			if now.Get(d).Revision != 2 {
				t.Errorf("%s is at revision %d, want 2", d, now.Get(d).Revision)
			}
		}
		if locks, _ := now.Locks(); len(locks) != 0 {
			t.Errorf("locks left %v", locks)
		}
	})
}

// The locks of an apply are not renewed before the first heartbeat, and the
// heartbeat does not come within two minutes.
func TestNoLockIsWrittenTwiceInAShortApply(t *testing.T) {
	if state.HeartbeatInterval < 2*time.Minute {
		t.Fatalf("the heartbeat is %s", state.HeartbeatInterval)
	}
	h := newHarness(t)
	e := h.engine(manySpec(20, func(int) string { return "v1" }), Options{})
	p := plan(t, e)
	h.backend.Reset()
	apply(t, e, p)
	if got := h.backend.Count("Put", "locks"); got != 0 {
		t.Errorf("%d lock writes in an apply of 20 nodes", got)
	}
	if got := h.backend.Count("PutIfAbsent", "locks"); got != 20 {
		t.Errorf("%d locks taken, want 20", got)
	}
}
