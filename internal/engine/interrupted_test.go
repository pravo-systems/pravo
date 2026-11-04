package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/state"
)

// deadBackend is a backend whose process was killed: once it is dead every write
// fails, so nothing the dying apply tries to do afterwards reaches the state, its
// locks included.
type deadBackend struct {
	state.Backend
	dead atomic.Bool
	// refuse, when set, fails the writes of the objects under these prefixes only, as a
	// store that answers 403 on the records and works for the locks.
	refuse atomic.Pointer[[]string]
}

func (b *deadBackend) refused(key string) bool {
	if b.dead.Load() {
		return true
	}
	if p := b.refuse.Load(); p != nil {
		for _, pre := range *p {
			if strings.HasPrefix(key, pre) {
				return true
			}
		}
	}
	return false
}

var errKilled = errors.New("the process was killed")

func (b *deadBackend) Put(ctx context.Context, key string, data []byte) error {
	if b.refused(key) {
		return errKilled
	}
	return b.Backend.Put(ctx, key, data)
}

func (b *deadBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	if b.refused(key) {
		return false, errKilled
	}
	return b.Backend.PutIfAbsent(ctx, key, data)
}

func (b *deadBackend) DeleteIf(ctx context.Context, key string, match func([]byte) bool) (bool, error) {
	if b.dead.Load() {
		return false, errKilled
	}
	return b.Backend.DeleteIf(ctx, key, match)
}

func (b *deadBackend) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	if b.refused(key) {
		return false, errKilled
	}
	return b.Backend.ReplaceIf(ctx, key, update)
}

func (b *deadBackend) Delete(ctx context.Context, key string) error {
	if b.dead.Load() {
		return errKilled
	}
	return b.Backend.Delete(ctx, key)
}

// An apply that is killed after the provider created the object and before the
// record was written leaves the object in the cloud, no record and a lock that
// goes stale. The next apply must not create the node again, with or without
// approval, and says how to go on; --allow-create names the node that is known
// not to exist.
func TestApplyDoesNotCreateAgainAfterAnInterruptedCreate(t *testing.T) {
	ctx := context.Background()
	var dead *deadBackend
	h := newHarnessOver(t, func(b state.Backend) state.Backend { dead = &deadBackend{Backend: b}; return dead })
	s := spec{cue: "a: " + thing("a", "content: \"x\"") + "\nb: " + thing("b", "content: a.content") + "\n", resources: []string{"a", "b"}}

	// The process dies when the provider has created a.
	h.fake.afterApply = func(name string) error {
		if name == "a" {
			dead.dead.Store(true)
		}
		return nil
	}
	e := h.engine(s, Options{})
	plan, err := e.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(ctx, plan); err == nil {
		t.Fatal("the apply of a killed process succeeded")
	}
	h.fake.afterApply = nil
	dead.dead.Store(false)
	h.reopen()
	if h.state.Get("a") != nil {
		t.Fatal("the record of a was written by a process that was dead")
	}
	creates := h.fake.called("ApplyResourceChange")
	if creates == 0 {
		t.Fatal("the provider created nothing")
	}
	// The lock the dead process held is what is left; nothing renewed it for an hour.
	if got := locksHeld(t, h.state); len(got) == 0 {
		t.Fatal("the killed apply left no lock")
	}
	// b was locked too, and never asked for: only the lock of a says that a create began.
	// The locks are made stale as time does it, by what the heartbeat last wrote.
	for _, addr := range locksHeld(t, h.state) {
		raw, err := h.state.Backend().Get(ctx, "locks/"+addr+".lock")
		if err != nil {
			t.Fatal(err)
		}
		var l state.Lock
		if err := json.Unmarshal(raw, &l); err != nil {
			t.Fatal(err)
		}
		if l.Creating != (addr == "a") {
			t.Fatalf("the lock of %s has creating=%v", addr, l.Creating)
		}
		old := time.Now().Add(-time.Hour).UTC()
		l.PID, l.Txn, l.Since, l.RenewedAt = 999999, "T1", old, old
		data, _ := json.Marshal(l)
		if err := h.state.Backend().Put(ctx, "locks/"+addr+".lock", data); err != nil {
			t.Fatal(err)
		}
	}

	var said []string
	opts := Options{Progress: func(f string, a ...any) { said = append(said, f) }}
	e2 := h.engine(s, opts)
	plan2, err := e2.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e2.Apply(ctx, plan2)
	var coded *CodedError
	if err == nil || !errors.As(err, &coded) || coded.Code != ExitRefused {
		t.Fatalf("apply after an interrupted create = %v, want a refusal with exit code %d", err, ExitRefused)
	}
	for _, want := range []string{"a: an apply (txn T1", "pravo import a", "pravo discover", "--allow-create a", "Nothing was changed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %v", want, err)
		}
	}
	if n := h.fake.called("ApplyResourceChange"); n != creates {
		t.Errorf("the refused apply reached the provider: %d calls, was %d", n, creates)
	}
	if h.state.Get("b") != nil {
		t.Error("the refused apply wrote the record of b")
	}

	// Saying that the create never reached the cloud lets the apply go on.
	e3 := h.engine(s, Options{AllowCreate: []string{"a"}})
	plan3, err := e3.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e3.Apply(ctx, plan3); err != nil {
		t.Fatalf("apply with --allow-create: %v", err)
	}
	if h.state.Get("a") == nil || h.state.Get("b") == nil {
		t.Error("a and b were not recorded")
	}
}

// refusedAfterEnding checks what every ending of an apply that cannot say whether a create
// reached the cloud must leave: the lock of the node, marked and stale at once, and a next
// apply that is refused with exit 5 before it reaches the provider.
func refusedAfterEnding(t *testing.T, h *harness, s spec, addr string) {
	t.Helper()
	ctx := context.Background()
	h.reopen()
	if h.state.Get(addr) != nil {
		t.Fatalf("%s has a record", addr)
	}
	var found bool
	locks, err := h.state.Locks()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range locks {
		if l.Addr == addr {
			found = true
			if !l.Creating || !l.Stale {
				t.Errorf("the lock of %s: creating %v, stale %v; want both", addr, l.Creating, l.Stale)
			}
		}
	}
	if !found {
		t.Fatalf("the lock of %s was released (locks: %v)", addr, locksHeld(t, h.state))
	}
	h.fake.resetCalls()
	e := h.engine(s, Options{})
	plan, err := e.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Apply(ctx, plan)
	var coded *CodedError
	if err == nil || !errors.As(err, &coded) || coded.Code != ExitRefused {
		t.Fatalf("apply after the ending = %v, want exit code %d", err, ExitRefused)
	}
	if n := h.fake.called("ApplyResourceChange"); n != 0 {
		t.Errorf("the refused apply reached the provider %d times", n)
	}
}

// The third signal, or the end of the time the providers are given, gives the apply up and
// says that the result of the create is unknown: the lock of the node stays.
func TestAbandonedCreateKeepsItsMark(t *testing.T) {
	h, in, cancel, done, holds := startFlight(t)
	h.fake.goDeaf()
	cancel()
	in.Stop()
	stillRunning(t, done, "a provider that is deaf")
	in.Abandon()
	if got := locksHeld(t, h.state); !slices.Equal(got, []string{"c"}) {
		t.Errorf("locks after the apply was given up: %v, want the lock of c alone", got)
	}
	// The command exits here; the providers are not waited for.
	refusedAfterEnding(t, h, flightSpec("2"), "c")
	for _, hd := range holds {
		hd.let()
	}
	returned(t, done)
}

// The provider process dies after the cloud accepted the create while pravo lives: the call
// fails, and the node keeps its mark. A provider that fails cleanly, with no state, leaves
// nothing: the retry goes on.
func TestProviderDeathAndCleanFailureOfACreate(t *testing.T) {
	ctx := context.Background()
	s := spec{cue: "a: " + thing("a", "content: \"x\"") + "\n", resources: []string{"a"}}
	t.Run("the provider dies", func(t *testing.T) {
		h := newHarness(t)
		h.fake.afterApply = func(string) error { return errors.New("rpc error: code = Unavailable") }
		e := h.engine(s, Options{})
		plan, _ := e.Plan(ctx)
		if _, err := e.Apply(ctx, plan); err == nil || !strings.Contains(err.Error(), "its result is unknown") {
			t.Fatalf("apply = %v, want the result unknown", err)
		}
		h.fake.afterApply = nil
		refusedAfterEnding(t, h, s, "a")
	})
	t.Run("the provider fails cleanly", func(t *testing.T) {
		h := newHarness(t)
		hd := h.fake.hold("a")
		e := h.engine(s, Options{})
		plan, _ := e.Plan(ctx)
		done := applying(e, ctx, plan)
		waitFor(t, hd)
		hd.refuse()
		if err := returned(t, done); err == nil {
			t.Fatal("the apply of a refused create succeeded")
		}
		if got := locksHeld(t, h.state); len(got) != 0 {
			t.Errorf("a clean failure left locks %v", got)
		}
		h.fake.holds = nil
		if _, err := h.engine(s, Options{}).Apply(ctx, mustPlan(t, h.engine(s, Options{}))); err != nil {
			t.Fatalf("the retry after a clean failure: %v", err)
		}
	})
}

func mustPlan(t *testing.T, e *Engine) *Plan {
	t.Helper()
	p, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The record write fails alone (a store that answers 403 on the records) while the release
// of the lock works: the node keeps its mark, and the error names the object's id.
func TestRecordWriteFailingAloneKeepsTheMark(t *testing.T) {
	ctx := context.Background()
	var b *deadBackend
	h := newHarnessOver(t, func(x state.Backend) state.Backend { b = &deadBackend{Backend: x}; return b })
	s := spec{cue: "a: " + thing("a", "content: \"x\"") + "\n", resources: []string{"a"}}
	b.refuse.Store(&[]string{"resources/", "rec/"})
	e := h.engine(s, Options{})
	plan, _ := e.Plan(ctx)
	_, err := e.Apply(ctx, plan)
	if err == nil || !strings.Contains(err.Error(), "the state does not record it") || !strings.Contains(err.Error(), "pravo import a id-1") {
		t.Fatalf("apply = %v, want the unrecorded creation with the id to import", err)
	}
	b.refuse.Store(nil)
	refusedAfterEnding(t, h, s, "a")
}

// A replacement that creates before it destroys runs while the record still holds the old
// object. Every ending that cannot say whether the create reached the cloud leaves a note,
// and the next apply is refused before it reaches the provider; a plan in between does not
// lose the note. A create that is recorded, or that failed cleanly, leaves none.
func TestCreateBeforeDestroyKeepsTheNote(t *testing.T) {
	ctx := context.Background()
	mk := func(name string) spec {
		return spec{cue: "a: " + thing(name, "$lifecycle: create_before_destroy: true") + "\n", resources: []string{"a"}}
	}
	setup := func(t *testing.T) (*harness, *deadBackend) {
		var b *deadBackend
		h := newHarnessOver(t, func(x state.Backend) state.Backend { b = &deadBackend{Backend: x}; return b })
		h.apply(mk("one"))
		return h, b
	}
	refused := func(t *testing.T, h *harness, b *deadBackend) {
		t.Helper()
		b.dead.Store(false)
		b.refuse.Store(nil)
		h.fake.afterApply = nil
		h.reopen()
		old := h.state.Get("a")
		if old == nil || old.Revision < 1 {
			t.Fatalf("record after the ending: %+v", old)
		}
		calls := h.fake.called("ApplyResourceChange")
		// A plan in between, as a person looks at it, then the apply.
		var said []string
		e := h.engine(mk("two"), Options{Progress: func(f string, a ...any) { said = append(said, fmt.Sprintf(f, a...)) }})
		plan, err := e.Plan(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(said, "\n"), "stopped while it created the replacement") {
			t.Errorf("the plan does not warn: %v", said)
		}
		for i := range 2 {
			_, err = e.Apply(ctx, plan)
			var coded *CodedError
			if err == nil || !errors.As(err, &coded) || coded.Code != ExitRefused {
				t.Fatalf("apply %d after the ending = %v, want exit code %d", i+1, err, ExitRefused)
			}
			for _, want := range []string{"a: an apply (txn ", "pravo state rm a", "--allow-create a", "Nothing was changed"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal lacks %q: %v", want, err)
				}
			}
			if n := h.fake.called("ApplyResourceChange"); n != calls {
				t.Fatalf("the refused apply reached the provider: %d calls, was %d", n, calls)
			}
			// A second plan after the lock was reclaimed.
			e = h.engine(mk("two"), Options{})
			if plan, err = e.Plan(ctx); err != nil {
				t.Fatal(err)
			}
		}
		e = h.engine(mk("two"), Options{AllowCreate: []string{"a"}})
		if _, err := e.Apply(ctx, mustPlan(t, e)); err != nil {
			t.Fatalf("apply with --allow-create: %v", err)
		}
		if p, _ := h.state.PendingCreateOf("a"); p != nil {
			t.Error("the note stays after the replacement was recorded")
		}
	}
	t.Run("killed", func(t *testing.T) {
		h, b := setup(t)
		h.fake.afterApply = func(string) error { b.dead.Store(true); return nil }
		e := h.engine(mk("two"), Options{})
		if _, err := e.Apply(ctx, mustPlan(t, e)); err == nil {
			t.Fatal("the apply of a killed process succeeded")
		}
		// The process is gone: its locks go stale as time does it.
		b.dead.Store(false)
		for _, addr := range locksHeld(t, h.state) {
			raw, err := h.state.Backend().Get(ctx, "locks/"+addr+".lock")
			if err != nil {
				t.Fatal(err)
			}
			var l state.Lock
			if err := json.Unmarshal(raw, &l); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Hour).UTC()
			l.PID, l.Since, l.RenewedAt = 999999, old, old
			data, _ := json.Marshal(l)
			if err := h.state.Backend().Put(ctx, "locks/"+addr+".lock", data); err != nil {
				t.Fatal(err)
			}
		}
		refused(t, h, b)
	})
	t.Run("the provider dies", func(t *testing.T) {
		h, b := setup(t)
		h.fake.afterApply = func(string) error { return errors.New("rpc error: code = Unavailable") }
		e := h.engine(mk("two"), Options{})
		if _, err := e.Apply(ctx, mustPlan(t, e)); err == nil {
			t.Fatal("apply succeeded")
		}
		refused(t, h, b)
	})
	t.Run("the record write fails alone", func(t *testing.T) {
		h, b := setup(t)
		h.fake.afterApply = func(string) error { b.refuse.Store(&[]string{"resources/", "rec/"}); return nil }
		e := h.engine(mk("two"), Options{})
		if _, err := e.Apply(ctx, mustPlan(t, e)); err == nil {
			t.Fatal("apply succeeded")
		}
		refused(t, h, b)
	})
	t.Run("a recorded replacement leaves nothing", func(t *testing.T) {
		h, _ := setup(t)
		h.apply(mk("two"))
		if p, _ := h.state.PendingCreateOf("a"); p != nil {
			t.Error("a note stays")
		}
		h.apply(mk("three"))
	})
	t.Run("a clean failure leaves nothing", func(t *testing.T) {
		h, _ := setup(t)
		hd := h.fake.hold("two")
		e := h.engine(mk("two"), Options{})
		done := applying(e, ctx, mustPlan(t, e))
		waitFor(t, hd)
		hd.refuse()
		if err := returned(t, done); err == nil {
			t.Fatal("apply succeeded")
		}
		if p, _ := h.state.PendingCreateOf("a"); p != nil {
			t.Error("a clean failure left a note")
		}
		h.fake.holds = nil
		h.apply(mk("two"))
	})
}

// The note follows the record when the node is renamed: by a $from in the plan and the
// apply, and by the move of the record in the state.
func TestCreateBeforeDestroyNoteFollowsAMove(t *testing.T) {
	ctx := context.Background()
	old := spec{cue: "a: " + thing("one", "$lifecycle: create_before_destroy: true") + "\n", resources: []string{"a"}}
	moved := func(name string) spec {
		return spec{cue: "b: " + thing(name, "$lifecycle: create_before_destroy: true") + "\n", resources: []string{"b"}}
	}
	interrupted := func(t *testing.T) *harness {
		h := newHarness(t)
		h.apply(old)
		h.fake.afterApply = func(string) error { return errors.New("rpc error: code = Unavailable") }
		e := h.engine(spec{cue: "a: " + thing("two", "$lifecycle: create_before_destroy: true") + "\n", resources: []string{"a"}}, Options{})
		if _, err := e.Apply(ctx, mustPlan(t, e)); err == nil {
			t.Fatal("apply succeeded")
		}
		h.fake.afterApply = nil
		h.reopen()
		return h
	}
	refused := func(t *testing.T, h *harness, s spec, from bool) {
		t.Helper()
		calls := h.fake.called("ApplyResourceChange")
		e := h.engine(s, Options{})
		if from {
			e.Root.Moves = []config.Move{{From: "a", To: "b"}}
		}
		_, err := e.Apply(ctx, mustPlan(t, e))
		var coded *CodedError
		if err == nil || !errors.As(err, &coded) || coded.Code != ExitRefused || !strings.Contains(err.Error(), "pravo state rm b") {
			t.Fatalf("apply after the move = %v, want a refusal that names b", err)
		}
		if n := h.fake.called("ApplyResourceChange"); n != calls {
			t.Fatalf("the refused apply reached the provider: %d calls, was %d", n, calls)
		}
	}
	t.Run("$from in the specification", func(t *testing.T) {
		h := interrupted(t)
		refused(t, h, moved("two"), true)
	})
	t.Run("a move of the record", func(t *testing.T) {
		h := interrupted(t)
		rec := h.state.Get("a")
		txn := h.state.Begin("edit", "mv")
		if err := txn.Start(); err != nil {
			t.Fatal(err)
		}
		if _, err := h.state.Remove("a"); err != nil {
			t.Fatal(err)
		}
		mv := *rec
		mv.Addr, mv.Name, mv.Revision = "b", "b", 0
		if err := h.state.Set(&mv, txn.ID()); err != nil {
			t.Fatal(err)
		}
		if err := h.state.MovePendingCreate("a", "b"); err != nil {
			t.Fatal(err)
		}
		if err := txn.Finish("applied", "mv", true); err != nil {
			t.Fatal(err)
		}
		if p, _ := h.state.PendingCreateOf("a"); p != nil {
			t.Error("the note stays under the old address")
		}
		refused(t, h, spec{cue: "b: " + thing("two", "$lifecycle: create_before_destroy: true") + "\n", resources: []string{"b"}}, false)
	})
}

// The old object of a create_before_destroy replacement is kept in the state, under an address
// of its own, until its destroy has finished: a destroy that fails, or that the command does
// not live to see through, is retried by the next apply with no change to the specification.
func TestCreateBeforeDestroyKeepsTheOldInstanceUntilDestroyed(t *testing.T) {
	ctx := context.Background()
	mk := func(name string) spec {
		return spec{cue: "a: " + thing(name, "$lifecycle: create_before_destroy: true") + "\n", resources: []string{"a"}}
	}
	deposedAddrs := func(h *harness) []string {
		var out []string
		for _, r := range h.state.List() {
			if r.Address() != "a" {
				out = append(out, r.Address())
			}
		}
		return out
	}
	retried := func(t *testing.T, h *harness) {
		t.Helper()
		h.reopen()
		if got := deposedAddrs(h); len(got) != 1 || !strings.Contains(got[0], "deposed") {
			t.Fatalf("records after the ending: %v, want the deposed old instance", got)
		}
		if rec := h.state.Get("a"); rec == nil || rec.ID() == "" {
			t.Fatalf("the replacement is not recorded: %+v", rec)
		}
		// No change to the specification: the plan lists the destroy, the apply does it.
		e := h.engine(mk("two"), Options{})
		plan := mustPlan(t, e)
		var destroys []string
		for _, c := range plan.Changes {
			if c.Action == ActionDelete {
				destroys = append(destroys, c.Addr)
			}
		}
		if len(destroys) != 1 || !strings.Contains(destroys[0], "deposed") {
			t.Fatalf("the plan lists %v, want the deposed instance", destroys)
		}
		if _, err := e.Apply(ctx, plan); err != nil {
			t.Fatalf("the retry: %v", err)
		}
		if got := deposedAddrs(h); len(got) != 0 {
			t.Errorf("records after the retry: %v", got)
		}
		e = h.engine(mk("two"), Options{})
		if plan := mustPlan(t, e); len(plan.Changes) != 0 && plan.Changes[0].Action != ActionNoop {
			t.Errorf("a plan after the retry still has changes: %v", plan.Changes[0].Action)
		}
	}
	t.Run("the destroy fails", func(t *testing.T) {
		h := newHarness(t)
		h.apply(mk("one"))
		h.fake.refuseDestroy("one")
		e := h.engine(mk("two"), Options{})
		_, err := e.Apply(ctx, mustPlan(t, e))
		if err == nil || !strings.Contains(err.Error(), "the previous instance (id id-1) is kept in the state") {
			t.Fatalf("apply = %v, want the old instance named", err)
		}
		h.fake.mu.Lock()
		delete(h.fake.undestroyable, "one")
		h.fake.mu.Unlock()
		retried(t, h)
	})
	t.Run("the command dies during the destroy", func(t *testing.T) {
		var b *deadBackend
		h := newHarnessOver(t, func(x state.Backend) state.Backend { b = &deadBackend{Backend: x}; return b })
		h.apply(mk("one"))
		h.fake.beforeDestroy = func(string) { b.dead.Store(true) }
		e := h.engine(mk("two"), Options{})
		if _, err := e.Apply(ctx, mustPlan(t, e)); err == nil {
			t.Fatal("the apply of a killed process succeeded")
		}
		h.fake.beforeDestroy = nil
		b.dead.Store(false)
		retried(t, h)
	})
}

// A command that stops after the old object was kept and before the replacement was recorded
// leaves one object under two records. The plan does not destroy it as a leftover: the
// apply is refused as after any interrupted create, and once the create is allowed the
// replacement goes on and the old object is destroyed once.
func TestCreateBeforeDestroyStoppedBeforeTheReplacementIsRecorded(t *testing.T) {
	ctx := context.Background()
	mk := func(name string) spec {
		return spec{cue: "a: " + thing(name, "$lifecycle: create_before_destroy: true") + "\n", resources: []string{"a"}}
	}
	var b *deadBackend
	h := newHarnessOver(t, func(x state.Backend) state.Backend { b = &deadBackend{Backend: x}; return b })
	h.apply(mk("one"))
	h.fake.afterApply = func(string) error { b.refuse.Store(&[]string{"resources/a.json"}); return nil }
	e := h.engine(mk("two"), Options{})
	if _, err := e.Apply(ctx, mustPlan(t, e)); err == nil {
		t.Fatal("apply succeeded")
	}
	h.fake.afterApply = nil
	b.refuse.Store(nil)
	h.reopen()
	if n := len(h.state.List()); n != 2 {
		t.Fatalf("%d records after the ending, want the node and the deposed old object", n)
	}
	h.fake.resetCalls()
	e = h.engine(mk("two"), Options{})
	plan := mustPlan(t, e)
	for _, c := range plan.Changes {
		if c.Action == ActionDelete && !c.Prior.IsNull() {
			t.Errorf("the plan destroys %s, the object of the node", c.Addr)
		}
	}
	_, err := e.Apply(ctx, plan)
	var coded *CodedError
	if err == nil || !errors.As(err, &coded) || coded.Code != ExitRefused {
		t.Fatalf("apply = %v, want exit code %d", err, ExitRefused)
	}
	if n := h.fake.called("ApplyResourceChange"); n != 0 {
		t.Fatalf("the refused apply reached the provider %d times", n)
	}
	e = h.engine(mk("two"), Options{AllowCreate: []string{"a"}})
	if _, err := e.Apply(ctx, mustPlan(t, e)); err != nil {
		t.Fatalf("apply with --allow-create: %v", err)
	}
	h.reopen()
	if got := h.state.List(); len(got) != 1 || got[0].Address() != "a" {
		t.Errorf("records after the apply: %d", len(got))
	}
}
