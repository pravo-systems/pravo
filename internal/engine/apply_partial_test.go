package engine

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// applied is the result and the error of an apply.
type applied struct {
	res *ApplyResult
	err error
}

// applyingResult runs the apply of a plan in the background and returns the
// channel that gets its result and its error.
func applyingResult(e *Engine, ctx context.Context, plan *Plan) chan applied {
	done := make(chan applied, 1)
	go func() {
		res, err := e.Apply(ctx, plan)
		done <- applied{res, err}
	}()
	return done
}

func finished(t *testing.T, done chan applied) applied {
	t.Helper()
	select {
	case a := <-done:
		return a
	case <-time.After(10 * time.Second):
		t.Fatal("the apply did not return")
		return applied{}
	}
}

// An apply that fails reports the changes it finished and recorded: the
// ones that were being made when another failed, and not the one that failed or
// the one that waited for it.
func TestFailedApplyReportsWhatItApplied(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	s := spec{
		cue: "a: " + thing("a", "content: \"a\"") + "\nb: " + thing("b", "content: \"b\"") + "\n" +
			"c: " + thing("c", "content: \"c\"") + "\nd: " + thing("d", "content: \"after \\(b.id)\"") + "\n",
		resources: []string{"a", "b", "c", "d"},
	}
	e := h.engine(s, Options{})
	plan, err := e.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := h.fake.hold("a"), h.fake.hold("b"), h.fake.hold("c")
	done := applyingResult(e, ctx, plan)
	waitFor(t, a, b, c)
	b.refuse()
	a.let()
	c.let()
	got := finished(t, done)
	if got.err == nil || !strings.Contains(got.err.Error(), "could not apply the change") {
		t.Fatalf("error = %v, want the failure of b", got.err)
	}
	if got.res == nil || got.res.Txn == "" {
		t.Fatalf("result %+v: the transaction of a failed apply is not named", got.res)
	}
	if !slices.Equal(got.res.Applied, []string{"a", "c"}) {
		t.Errorf("applied %v, want [a c]", got.res.Applied)
	}
	for _, addr := range got.res.Applied {
		if h.state.Get(addr) == nil {
			t.Errorf("%s is reported applied and has no record", addr)
		}
	}
	if tx := lastApply(t, h.state); tx.ID != got.res.Txn || tx.Status != "failed" {
		t.Errorf("transaction %s is %s, want %s failed", tx.ID, tx.Status, got.res.Txn)
	}
}

// A change whose provider returned part of its state has a record, and is not
// applied: the object is not what the plan asked for.
func TestChangeThatFailedPartwayIsNotApplied(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	s := spec{
		cue: "ok: " + thing("ok", "content: \"ok\"") + "\nbad: " + thing("bad", "content: \"bad\"") + "\n" +
			"after: " + thing("after", "content: \"after \\(bad.id)\"") + "\n",
		resources: []string{"ok", "bad", "after"},
	}
	e := h.engine(s, Options{})
	plan, err := e.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h.fake.partial = map[string]bool{"bad": true}
	ok, bad := h.fake.hold("ok"), h.fake.hold("bad")
	done := applyingResult(e, ctx, plan)
	waitFor(t, ok, bad)
	bad.let()
	ok.let()
	got := finished(t, done)
	if got.err == nil {
		t.Fatal("an apply whose provider failed succeeded")
	}
	if h.state.Get("bad") == nil {
		t.Error("the partial state of bad was not recorded")
	}
	if h.state.Get("after") != nil {
		t.Error("after was applied; it waits for bad")
	}
	if !slices.Equal(got.res.Applied, []string{"ok"}) {
		t.Errorf("applied %v, want [ok]: bad has a record and is not applied", got.res.Applied)
	}
}

// An interrupted apply reports the changes that finished after the signal; the
// ones it did not start are not among them.
func TestInterruptedApplyReportsWhatItApplied(t *testing.T) {
	h := newHarness(t)
	s := chainSpec(3, true)
	in := &Interrupt{}
	ctx, cancel := context.WithCancel(WithInterrupt(context.Background(), in))
	defer cancel()
	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first := h.fake.hold("r000")
	done := applyingResult(e, ctx, plan)
	waitFor(t, first)
	cancel() // the first signal
	first.let()
	got := finished(t, done)
	if got.err == nil || !strings.Contains(got.err.Error(), "interrupted") {
		t.Fatalf("error = %v, want the apply to say it was interrupted", got.err)
	}
	if !slices.Equal(got.res.Applied, []string{"r000"}) || got.res.Txn == "" {
		t.Errorf("applied %v, txn %q; want [r000] and a transaction", got.res.Applied, got.res.Txn)
	}
	if h.state.Get("r001") != nil {
		t.Error("r001 was started after the signal")
	}
}

// An apply that destroys reports the deletes it finished when one fails.
func TestFailedDestroyReportsWhatItDestroyed(t *testing.T) {
	s := orphanSpec(3, 3)
	h := newHarness(t)
	h.apply(s)
	h.fake.refuseDestroy("r001")
	e := h.engine(s, Options{Destroy: true})
	p := plan(t, e)
	res, err := e.Apply(context.Background(), p)
	if err == nil {
		t.Fatal("a destroy that the provider refuses succeeded")
	}
	var want []string
	for _, addr := range s.resources {
		if h.state.Get(addr) == nil {
			want = append(want, addr)
		}
	}
	slices.Sort(want)
	if !slices.Equal(res.Applied, want) || slices.Contains(res.Applied, "r001") || !slices.Contains(res.Applied, "r002") {
		t.Errorf("applied %v, want the resources with no record: %v", res.Applied, want)
	}
	if res.Destroyed != len(want) {
		t.Errorf("destroyed %d, want %d", res.Destroyed, len(want))
	}
}

// An apply that succeeds reports the changes it made too.
func TestApplyReportsWhatItApplied(t *testing.T) {
	h := newHarness(t)
	res := h.apply(manySpec(3, func(int) string { return "v1" }))
	if !slices.Equal(res.Applied, []string{"r000", "r001", "r002"}) {
		t.Errorf("applied %v, want the three resources", res.Applied)
	}
}

// What an apply that failed marks in a plan file does not say that an apply from
// it finished: the outputs, which an apply records at its end, are left to the
// next one, and the rest of the plan applies from the file.
func TestPartlyAppliedFileStillHasItsOutputsLeft(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	s := spec{
		cue: "a: " + thing("a", "content: \"a\"") + "\nb: " + thing("b", "content: \"b\"") + "\n" +
			"out: {value: \"x-\\(a.id)\"}\n",
		resources: []string{"a", "b"},
		outputs:   []string{"out"},
	}
	file, pf := planned(t, h, s)
	e := h.engine(s, Options{Refresh: RefreshNone})
	loaded, err := e.LoadPlan(pf)
	if err != nil {
		t.Fatal(err)
	}
	a, b := h.fake.hold("a"), h.fake.hold("b")
	done := applyingResult(e, ctx, loaded)
	waitFor(t, a, b)
	b.refuse()
	a.let()
	got := finished(t, done)
	res := got.res
	if got.err == nil {
		t.Fatal("an apply whose provider failed succeeded")
	}
	if !slices.Equal(res.Applied, []string{"a"}) {
		t.Fatalf("applied %v, want [a]", res.Applied)
	}
	if _, ok := h.state.Outputs()["out"]; ok {
		t.Fatal("the outputs were recorded by an apply that failed")
	}
	if err := MarkPartlyApplied(file, pf, res.Applied, res.Txn); err != nil {
		t.Fatal(err)
	}
	after, err := ReadPlanFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := appliedIn(t, file); len(got) != 1 || got["a"] != res.Txn || after.ApplyTxn != "" {
		t.Errorf("marked %v, apply txn %q; want a marked by %s and no apply txn", got, after.ApplyTxn, res.Txn)
	}

	// The rest is applied from the file: b first; once it is, the outputs alone
	// are what is left, until an apply from the file records them.
	h.reopen()
	e = h.engine(s, Options{Refresh: RefreshNone})
	rest, err := e.LoadPlan(after)
	if err != nil {
		t.Fatalf("the rest of the plan cannot be loaded: %v", err)
	}
	if remaining := rest.Remaining(); !after.LeftToApply(remaining) || !slices.Equal(remaining.PendingAddrs(), []string{"b"}) {
		t.Errorf("pending %v: want b left to apply", remaining.PendingAddrs())
	}
	both := *after
	both.Changes = slices.Clone(after.Changes)
	for i := range both.Changes {
		both.Changes[i].Applied = true
	}
	if !both.LeftToApply(&Plan{EvalOutputs: []string{"out"}}) {
		t.Error("every change is marked and no apply from the file finished: the outputs are still left to record")
	}
	both.ApplyTxn = "T1"
	if both.LeftToApply(&Plan{EvalOutputs: []string{"out"}}) {
		t.Error("an apply from the file finished and the file still has something left")
	}
}
