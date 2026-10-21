package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"pravo.dev/pravo/internal/state"
	"pravo.dev/pravo/internal/state/statetest"
)

// applying runs the apply of a plan in the background and returns the channel
// that gets its error.
func applying(e *Engine, ctx context.Context, plan *Plan) chan error {
	done := make(chan error, 1)
	go func() {
		_, err := e.Apply(ctx, plan)
		done <- err
	}()
	return done
}

// stillRunning fails the test when the apply has returned.
func stillRunning(t *testing.T, done chan error, what string) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("%s: the apply returned (%v) while a provider was still at work", what, err)
	case <-time.After(150 * time.Millisecond):
	}
}

func returned(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("the apply did not return")
		return nil
	}
}

// lastApply returns the newest recorded transaction of an apply.
func lastApply(t *testing.T, st *state.Store) state.Transaction {
	t.Helper()
	txns, err := st.Transactions()
	if err != nil {
		t.Fatal(err)
	}
	for _, tx := range txns {
		if tx.Kind == "apply" {
			return tx
		}
	}
	t.Fatal("no apply was recorded")
	return state.Transaction{}
}

func changedBy(tx state.Transaction, action string) []string {
	var out []string
	for _, c := range tx.Changes {
		if c.Action == action {
			out = append(out, c.Addr)
		}
	}
	return out
}

// A node that fails stops the walk from starting tasks. The tasks that providers
// are working on finish, and their records are written; the locks are released
// when both are done, and the error does not call the node that finished lost.
func TestInFlightOperationsSurviveAnError(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	s := spec{
		cue: "x: " + thing("x", "content: \"x\"") + "\ny: " + thing("y", "content: \"y\"") + "\n" +
			"z: " + thing("z", "content: \"after \\(x.id)\"") + "\n",
		resources: []string{"x", "y", "z"},
	}
	e := h.engine(s, Options{})
	plan, err := e.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	x, y := h.fake.hold("x"), h.fake.hold("y")
	done := applying(e, ctx, plan)
	waitFor(t, x, y)
	x.refuse()
	stillRunning(t, done, "x failed and y is in flight")
	if got := locksHeld(t, h.state); !slices.Equal(got, []string{"x", "y", "z"}) {
		t.Errorf("locks while y is in flight: %v, want all three nodes the apply writes", got)
	}
	if h.state.Get("y") != nil {
		t.Error("y has a record before its provider answered")
	}
	y.let()
	err = returned(t, done)
	if err == nil || !strings.Contains(err.Error(), "could not apply the change") {
		t.Fatalf("error = %v, want the failure of x", err)
	}
	if strings.Contains(err.Error(), "in flight") {
		t.Errorf("error = %v: y finished and is not lost", err)
	}
	if h.state.Get("y") == nil {
		t.Error("y was applied and has no record")
	}
	if h.state.Get("x") != nil || h.state.Get("z") != nil {
		t.Error("x failed and z never started; neither has a record")
	}
	if n := h.fake.called("ApplyResourceChange"); n != 2 {
		t.Errorf("ApplyResourceChange was called %d times, want 2: z waits for x", n)
	}
	tx := lastApply(t, h.state)
	if tx.Status != "failed" || !slices.Equal(changedBy(tx, "create"), []string{"y"}) || len(changedBy(tx, "unsettled")) != 0 {
		t.Errorf("transaction: %s, created %v, unsettled %v; want failed, y created, nothing unsettled", tx.Status, changedBy(tx, "create"), changedBy(tx, "unsettled"))
	}
	if got := locksHeld(t, h.state); len(got) != 0 {
		t.Errorf("locks left after the apply: %v", got)
	}
}

// flightSpec has two resources that exist and a third that does not.
func flightSpec(content string) spec {
	return spec{
		cue: "a: " + thing("a", "content: \""+content+"\"") + "\nb: " + thing("b", "content: \""+content+"\"") + "\n" +
			"c: " + thing("c", "content: \""+content+"\"") + "\n",
		resources: []string{"a", "b", "c"},
	}
}

// startFlight applies a specification in which a and b exist and c is new, and
// returns the engine, the context to cancel, the Interrupt and the apply in
// progress, with the three providers at work.
func startFlight(t *testing.T) (h *harness, in *Interrupt, cancel context.CancelFunc, done chan error, holds []*hold) {
	t.Helper()
	return startFlightOn(t, false)
}

// startFlightOn is startFlight; remote opens the state of the apply as the command
// does, with the context that the first signal cancels, over a backend that
// refuses the calls made with a context that is done.
func startFlightOn(t *testing.T, remote bool) (h *harness, in *Interrupt, cancel context.CancelFunc, done chan error, holds []*hold) {
	t.Helper()
	h = newHarness(t)
	old := flightSpec("1")
	old.resources = []string{"a", "b"}
	old.cue = "a: " + thing("a", "content: \"1\"") + "\nb: " + thing("b", "content: \"1\"") + "\n"
	h.apply(old)
	in = &Interrupt{}
	var ctx context.Context
	ctx, cancel = context.WithCancel(WithInterrupt(context.Background(), in))
	t.Cleanup(cancel)
	if remote {
		st, err := state.OpenBackend(ctx, statetest.NewHonouring(h.backend))
		if err != nil {
			t.Fatal(err)
		}
		h.state = st
	}
	e := h.engine(flightSpec("2"), Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		holds = append(holds, h.fake.hold(name))
	}
	h.fake.resetCalls()
	done = applying(e, ctx, plan)
	waitFor(t, holds...)
	return h, in, cancel, done, holds
}

// The first signal lets the providers finish. The second asks them to stop, and
// the apply records what they return: a node that has a record is marked so that
// the next plan reads it, and the error names every change whose result is not
// known.
func TestFirstSignalWaitsSecondSignalStops(t *testing.T) {
	h, in, cancel, done, _ := startFlight(t)
	before := map[string]int{"a": h.revision("a"), "b": h.revision("b")}
	if n := in.InFlight(); n != 3 {
		t.Fatalf("in flight = %d, want 3", n)
	}

	cancel() // the first signal
	stillRunning(t, done, "after the first signal")
	if in.Stopped() || h.fake.stopped() != 0 {
		t.Errorf("the first signal asked the providers to stop")
	}
	if n := in.InFlight(); n != 3 {
		t.Errorf("in flight after the first signal = %d, want 3", n)
	}

	in.Stop() // the second
	err := returned(t, done)
	if h.fake.stopped() != 1 {
		t.Errorf("the provider was asked to stop %d times, want 1", h.fake.stopped())
	}
	if err == nil {
		t.Fatal("an apply that was stopped did not fail")
	}
	for _, want := range []string{
		"update of a was in flight; its result is unknown; check the provider",
		"update of b was in flight; its result is unknown; check the provider",
		"creation of c was in flight; its result is unknown; check the provider",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to say %q", err, want)
		}
	}
	for _, addr := range []string{"a", "b"} {
		n := h.state.Get(addr)
		if n.RefreshedAt != nil || n.Revision != before[addr]+1 || !strings.Contains(string(n.Attributes), `"1"`) {
			t.Errorf("%s: refreshed_at %v, revision %d (was %d), attributes %s; want it marked for a read and otherwise as it was", addr, n.RefreshedAt, n.Revision, before[addr], n.Attributes)
		}
	}
	if h.state.Get("c") != nil {
		t.Error("c was being created and has a record")
	}
	tx := lastApply(t, h.state)
	if got := changedBy(tx, "unsettled"); !slices.Equal(got, []string{"a", "b"}) || tx.Status != "failed" {
		t.Errorf("transaction %s: unsettled %v; want failed with a and b: c has no record to mark", tx.Status, got)
	}
	if got := locksHeld(t, h.state); len(got) != 0 {
		t.Errorf("locks left after the apply: %v", got)
	}

	// The marked nodes are read by the next plan, whatever the refresh age.
	h.reopen()
	h.fake.resetCalls()
	if _, err := h.engine(flightSpec("2"), Options{DefaultRefresh: "6h"}).Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.readNames(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("the next plan read %v, want a and b", got)
	}
}

// The node an interrupted apply was changing is read by the next plan although
// the specification is back to what the record says and the plan does not reach
// it, whatever the refresh age: the object may be half changed.
func TestUnsettledNodeIsReadByAPlanThatDoesNotReachIt(t *testing.T) {
	h, in, cancel, done, _ := startFlight(t)
	cancel()
	in.Stop()
	if err := returned(t, done); err == nil {
		t.Fatal("an apply that was stopped did not fail")
	}
	h.reopen()
	old := flightSpec("1")
	old.resources = []string{"a", "b"}
	old.cue = "a: " + thing("a", "content: \"1\"") + "\nb: " + thing("b", "content: \"1\"") + "\n"

	// -refresh=false reads nothing, and says which nodes it leaves unread.
	var said []string
	var mu sync.Mutex
	h.fake.resetCalls()
	opts := Options{Refresh: RefreshNone, DefaultRefresh: "6h", Progress: func(f string, a ...any) {
		mu.Lock()
		defer mu.Unlock()
		said = append(said, fmt.Sprintf(f, a...))
	}}
	if _, err := h.engine(old, opts).Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.readNames(); len(got) != 0 {
		t.Errorf("-refresh=false read %v", got)
	}
	for _, addr := range []string{"a", "b"} {
		want := "Warning: " + addr + ": " + noReadTime + "; it is not read because -refresh=false was given"
		if !slices.Contains(said, want) {
			t.Errorf("-refresh=false did not warn %q; it said %q", want, said)
		}
	}

	h.fake.resetCalls()
	p, err := h.engine(old, Options{DefaultRefresh: "6h"}).Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := h.fake.readNames(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("read %v, want a and b: their results are not known", got)
	}
	if want := noReadTime + "; its real state is read again whatever the refresh age"; !hasReason(reasonsOf(p, "a"), want) {
		t.Errorf("a: reasons = %q, want %q", reasonsOf(p, "a"), want)
	}
	if p.HasChanges() || p.Stats.Refreshed != 2 || p.Stats.NotRead != 0 {
		t.Errorf("changes %v, refreshed %d, not read %d; want none, 2 and 0", p.HasChanges(), p.Stats.Refreshed, p.Stats.NotRead)
	}
	if n := h.state.Get("a"); n.RefreshedAt == nil {
		t.Error("the read of a did not record the time")
	}

	// The read ends it: the next plan reads nothing.
	h.fake.resetCalls()
	if _, err := h.engine(old, Options{DefaultRefresh: "6h"}).Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.readNames(); len(got) != 0 {
		t.Errorf("the next plan read %v", got)
	}
}

// A provider that does not hear Stop is given up after the time the command
// allows: what it was doing is marked, the transaction is written and the locks
// are released, without the providers having answered.
func TestAbandonedApplyMarksWhatWasInFlight(t *testing.T) {
	h, in, cancel, done, holds := startFlight(t)
	h.fake.goDeaf()
	cancel()
	in.Stop()
	stillRunning(t, done, "a provider that is deaf")

	notes := in.Abandon()
	want := []string{
		"update of a was in flight; its result is unknown; check the provider",
		"update of b was in flight; its result is unknown; check the provider",
		"creation of c was in flight; its result is unknown; check the provider",
	}
	if !slices.Equal(notes, want) {
		t.Errorf("notes = %q, want %q", notes, want)
	}
	for _, addr := range []string{"a", "b"} {
		if n := h.state.Get(addr); n.RefreshedAt != nil {
			t.Errorf("%s was not marked: refreshed_at %v", addr, n.RefreshedAt)
		}
	}
	tx := lastApply(t, h.state)
	if tx.Status != "failed" || !slices.Equal(changedBy(tx, "unsettled"), []string{"a", "b"}) || !strings.Contains(tx.Summary, "interrupted") {
		t.Errorf("transaction %s %q: unsettled %v", tx.Status, tx.Summary, changedBy(tx, "unsettled"))
	}
	if got := locksHeld(t, h.state); len(got) != 0 {
		t.Errorf("locks held after the apply was given up: %v", got)
	}
	for _, hd := range holds {
		hd.let()
	}
	returned(t, done)
}

// An apply whose context is cancelled before it begins has done nothing, and
// says so: it does not report that it finished.
func TestInterruptBeforeAnyTaskStartsIsAnError(t *testing.T) {
	h := newHarness(t)
	e := h.engine(flightSpec("1"), Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = e.Apply(ctx, plan)
	if err == nil || !strings.Contains(err.Error(), "interrupted: 3 of 3 tasks were not started") {
		t.Fatalf("error = %v", err)
	}
	if n := h.fake.called("ApplyResourceChange"); n != 0 || h.state.Len() != 0 {
		t.Errorf("ApplyResourceChange %d times, %d records; want none", n, h.state.Len())
	}
	if got := locksHeld(t, h.state); len(got) != 0 {
		t.Errorf("locks left: %v", got)
	}
}

// An interrupt stops the deletes from starting; the ones that run finish and
// their records go.
func TestInterruptStopsStartingDestroys(t *testing.T) {
	h := newHarness(t)
	var names []string
	var b strings.Builder
	for i := 0; i < 14; i++ {
		name := fmt.Sprintf("r%02d", i)
		names = append(names, name)
		fmt.Fprintf(&b, "%s: %s\n", name, thing(name, ""))
	}
	h.apply(spec{cue: b.String(), resources: names})
	e := h.engine(spec{cue: "keep: " + thing("keep", "") + "\n", resources: []string{"keep"}}, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h.fake.setLatency(300 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := applying(e, ctx, plan)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, peak := h.fake.destroyed(); peak >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no destroy began")
		}
	}
	cancel()
	err = returned(t, done)
	if err == nil || !strings.Contains(err.Error(), "interrupted before") {
		t.Fatalf("error = %v, want the interrupt", err)
	}
	ended, _ := h.fake.destroyed()
	left := 0
	for _, n := range names {
		if h.state.Get(n) != nil {
			left++
		}
	}
	if len(ended) == 0 || left == 0 || len(ended)+left != len(names) {
		t.Errorf("%d destroys ended and %d records are left of %d; want every destroy that began to be recorded, and not all of them to begin", len(ended), left, len(names))
	}
}

// StopAll asks the providers that pravo started, and leaves one it reattached to.
func TestStopAllLeavesAReattachedProvider(t *testing.T) {
	h := newHarness(t)
	e := h.engine(flightSpec("1"), Options{})
	if _, err := e.Providers.Provider(context.Background(), "fake"); err != nil {
		t.Fatal(err)
	}
	other := &fakeProvider{}
	e.Providers.instances["attached"] = &instance{p: other, key: "attached", reattached: true, cfg: true}
	e.Providers.StopAll(context.Background())
	if h.fake.stopped() != 1 || other.stopped() != 0 {
		t.Errorf("stopped: started provider %d times, reattached provider %d times; want 1 and 0", h.fake.stopped(), other.stopped())
	}
}

// On a remote state the first signal does not stop the writes that follow it. The
// changes that were in flight finish, and the records, the transaction and the
// release of the locks go through a backend that refuses a context that is done.
func TestFirstSignalOnARemoteStateRecordsWhatFinished(t *testing.T) {
	h, _, cancel, done, holds := startFlightOn(t, true)
	before := map[string]int{"a": h.revision("a"), "b": h.revision("b")}
	cancel() // the first signal
	stillRunning(t, done, "after the first signal")
	for _, hd := range holds {
		hd.let()
	}
	// Every change had begun, so the apply has nothing left to refuse.
	if err := returned(t, done); err != nil {
		t.Fatalf("error = %v: the three changes finished and were recorded", err)
	}
	h.reopen()
	for _, addr := range []string{"a", "b"} {
		if n := h.state.Get(addr); n == nil || n.Revision != before[addr]+1 || !strings.Contains(string(n.Attributes), `"2"`) {
			t.Errorf("%s: record %+v, want revision %d with the new content", addr, n, before[addr]+1)
		}
	}
	if h.state.Get("c") == nil {
		t.Error("c was created and has no record")
	}
	if got := locksHeld(t, h.state); len(got) != 0 {
		t.Errorf("locks left after the apply: %v", got)
	}
	tx := lastApply(t, h.state)
	if tx.Status != "applied" || len(tx.Changes) != 3 || len(changedBy(tx, "unsettled")) != 0 {
		t.Errorf("transaction %s with %d changes, unsettled %v; want applied, the 3 changes, nothing unsettled", tx.Status, len(tx.Changes), changedBy(tx, "unsettled"))
	}
}

// The second signal, and the end of the wait, write through the same state: the
// changes the providers did not answer for are marked, the transaction is
// written and the locks are released.
func TestStoppedAndAbandonedApplyWriteToARemoteState(t *testing.T) {
	t.Run("the second signal", func(t *testing.T) {
		h, in, cancel, done, _ := startFlightOn(t, true)
		cancel()
		in.Stop()
		err := returned(t, done)
		if err == nil || !strings.Contains(err.Error(), "update of a was in flight; its result is unknown") || !strings.Contains(err.Error(), "creation of c was in flight") {
			t.Fatalf("error = %v, want the changes whose result is not known", err)
		}
		h.reopen()
		for _, addr := range []string{"a", "b"} {
			if n := h.state.Get(addr); n == nil || n.RefreshedAt != nil {
				t.Errorf("%s: %+v, want it marked for a read", addr, n)
			}
		}
		tx := lastApply(t, h.state)
		if tx.Status != "failed" || !slices.Equal(changedBy(tx, "unsettled"), []string{"a", "b"}) {
			t.Errorf("transaction %s: unsettled %v; want failed with a and b", tx.Status, changedBy(tx, "unsettled"))
		}
		if got := locksHeld(t, h.state); len(got) != 0 {
			t.Errorf("locks left after the apply: %v", got)
		}
	})
	t.Run("the third signal", func(t *testing.T) {
		h, in, cancel, done, holds := startFlightOn(t, true)
		h.fake.goDeaf()
		cancel()
		in.Stop()
		stillRunning(t, done, "a provider that is deaf")
		if notes := in.Abandon(); len(notes) != 3 {
			t.Errorf("notes = %q, want the three changes in flight", notes)
		}
		h.reopen()
		for _, addr := range []string{"a", "b"} {
			if n := h.state.Get(addr); n == nil || n.RefreshedAt != nil {
				t.Errorf("%s: %+v, want it marked for a read", addr, n)
			}
		}
		tx := lastApply(t, h.state)
		if tx.Status != "failed" || !strings.Contains(tx.Summary, "interrupted") || !slices.Equal(changedBy(tx, "unsettled"), []string{"a", "b"}) {
			t.Errorf("transaction %s %q: unsettled %v", tx.Status, tx.Summary, changedBy(tx, "unsettled"))
		}
		if got := locksHeld(t, h.state); len(got) != 0 {
			t.Errorf("locks held after the apply was given up: %v", got)
		}
		for _, hd := range holds {
			hd.let()
		}
		returned(t, done)
	})
}
