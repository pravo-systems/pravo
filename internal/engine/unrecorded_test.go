package engine

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
)

// flakyWrites fails the writes of chosen objects, as a connection that is reset
// does. A count is how many calls fail; a negative count fails every call.
type flakyWrites struct {
	state.Backend

	mu         sync.Mutex
	failPut    map[string]int
	failDelete map[string]int
}

func newFlaky(b state.Backend) *flakyWrites {
	return &flakyWrites{Backend: b, failPut: map[string]int{}, failDelete: map[string]int{}}
}

// fails reports whether the call for key fails, and counts it.
func (f *flakyWrites) fails(m map[string]int, key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch n := m[key]; {
	case n < 0:
		return true
	case n > 0:
		m[key] = n - 1
		return true
	}
	return false
}

func (f *flakyWrites) set(m map[string]int, key string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m[key] = n
}

func (f *flakyWrites) Put(ctx context.Context, key string, data []byte) error {
	if f.fails(f.failPut, key) {
		return errors.New("simulated: connection reset by peer")
	}
	return f.Backend.Put(ctx, key, data)
}

func (f *flakyWrites) Delete(ctx context.Context, key string) error {
	if f.fails(f.failDelete, key) {
		return errors.New("simulated: connection reset by peer")
	}
	return f.Backend.Delete(ctx, key)
}

// flakyHarness returns a harness whose state goes through a flakyWrites.
func flakyHarness(t *testing.T) (*harness, *flakyWrites) {
	t.Helper()
	var f *flakyWrites
	h := newHarnessOver(t, func(b state.Backend) state.Backend {
		f = newFlaky(b)
		return f
	})
	return h, f
}

// lastSpec has a and b, and c, which waits for both: when c fails, a and b are
// recorded.
func lastSpec(content string) spec {
	return spec{
		cue: "a: " + thing("a", "content: \""+content+"\"") + "\nb: " + thing("b", "content: \""+content+"\"") + "\n" +
			"c: " + thing("c", "content: \"after \\(a.id) \\(b.id)\"") + "\n",
		resources: []string{"a", "b", "c"},
	}
}

// planAndApply plans and applies a specification and returns the error of the apply.
func planAndApply(t *testing.T, e *Engine) error {
	t.Helper()
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Apply(context.Background(), plan)
	return err
}

// The provider created the object and the write of its record failed. The error
// names the node and says that the state does not record the creation, and the
// transaction carries the same words: the object exists with no record, and
// nothing else would say so.
func TestRecordWriteFailureAfterCreationNamesTheNode(t *testing.T) {
	h, f := flakyHarness(t)
	f.set(f.failPut, "resources/c.json", -1)
	err := planAndApply(t, h.engine(lastSpec("1"), Options{}))
	if err == nil {
		t.Fatal("the apply did not fail although the record of c could not be written")
	}
	for _, want := range []string{
		"c: the record could not be written",
		"connection reset by peer",
		"creation of c finished, but the state does not record it; check the provider",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to say %q", err, want)
		}
	}
	if n := h.fake.called("ApplyResourceChange"); n != 3 {
		t.Errorf("ApplyResourceChange ran %d times, want 3: c was created", n)
	}
	h.reopen()
	if h.state.Get("a") == nil || h.state.Get("b") == nil {
		t.Error("a and b were applied and have no record")
	}
	if h.state.Get("c") != nil {
		t.Error("the record of c could not be written and it has one")
	}
	tx := lastApply(t, h.state)
	if tx.Status != "failed" || !strings.Contains(tx.Summary, "creation of c finished, but the state does not record it") {
		t.Errorf("transaction %s %q; want failed with the note about c", tx.Status, tx.Summary)
	}
	if got := locksHeld(t, h.state); len(got) != 0 {
		t.Errorf("locks left after the apply: %v", got)
	}
}

// The provider updated the object and the write of its record failed once. The
// record that is left is marked, so the next plan reads the object whatever the
// refresh age, and the transaction lists the node as unsettled.
func TestRecordWriteFailureAfterUpdateMarksTheRecord(t *testing.T) {
	h, f := flakyHarness(t)
	h.apply(flightSpec("1"))
	before := h.revision("a")
	e := h.engine(flightSpec("2"), Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.set(f.failPut, "resources/a.json", 1) // the plan wrote the time of its read of a
	_, err = e.Apply(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "a: the record could not be written") || !strings.Contains(err.Error(), "update of a finished, but the state does not record it") {
		t.Fatalf("error = %v, want it to name a and say that the update is not recorded", err)
	}
	h.reopen()
	a := h.state.Get("a")
	if a == nil || a.RefreshedAt != nil || a.Revision != before+1 || !strings.Contains(string(a.Attributes), `"1"`) {
		t.Fatalf("a: %+v; want the old record, marked for a read, one revision on", a)
	}
	tx := lastApply(t, h.state)
	if !slices.Equal(changedBy(tx, "unsettled"), []string{"a"}) {
		t.Errorf("unsettled in the transaction: %v, want a", changedBy(tx, "unsettled"))
	}
	h.fake.resetCalls()
	if _, err := h.engine(flightSpec("1"), Options{DefaultRefresh: "6h"}).Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.fake.readNames(); !slices.Equal(got, []string{"a"}) {
		t.Errorf("the next plan read %v, want a", got)
	}
}

// The provider returned a state with its error, and the write of that state failed
// as well: the error says both.
func TestPartialStateThatCannotBeRecordedIsReported(t *testing.T) {
	h, f := flakyHarness(t)
	h.fake.partial = map[string]bool{"c": true}
	f.set(f.failPut, "resources/c.json", -1)
	err := planAndApply(t, h.engine(lastSpec("1"), Options{}))
	if err == nil {
		t.Fatal("the apply did not fail")
	}
	for _, want := range []string{
		"the provider stopped before it set the content",
		"the state it returned: c: the record could not be written",
		"creation of c finished, but the state does not record it",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to say %q", err, want)
		}
	}
	h.reopen()
	if h.state.Get("c") != nil {
		t.Error("c has a record although it could not be written")
	}
	// A partial state that is written is a record, and nothing is reported of it.
	h2 := newHarness(t)
	h2.fake.partial = map[string]bool{"c": true}
	err = planAndApply(t, h2.engine(lastSpec("1"), Options{}))
	if err == nil || strings.Contains(err.Error(), "does not record") || h2.state.Get("c") == nil {
		t.Errorf("error = %v, record of c %v; want the provider's error and a record", err, h2.state.Get("c"))
	}
}

// The provider destroyed the object of a record that the specification no longer
// declares, and removing the record failed: the node is named, and its record is
// marked for a read.
func TestRecordRemovalFailureAfterDestroyNamesTheNode(t *testing.T) {
	h, f := flakyHarness(t)
	both := spec{
		cue:       "x: " + thing("x", "content: \"x\"") + "\ny: " + thing("y", "content: \"y\"") + "\n",
		resources: []string{"x", "y"},
	}
	h.apply(both)
	f.set(f.failDelete, "resources/x.json", -1)
	kept := spec{cue: "y: " + thing("y", "content: \"y\"") + "\n", resources: []string{"y"}}
	err := planAndApply(t, h.engine(kept, Options{}))
	if err == nil || !strings.Contains(err.Error(), "x: the record could not be removed") || !strings.Contains(err.Error(), "destruction of x finished, but the state does not record it") {
		t.Fatalf("error = %v, want it to name x and say that the destruction is not recorded", err)
	}
	h.reopen()
	if x := h.state.Get("x"); x == nil || x.RefreshedAt != nil {
		t.Errorf("x: %+v; want its record left, marked for a read", x)
	}
	if !slices.Equal(changedBy(lastApply(t, h.state), "unsettled"), []string{"x"}) {
		t.Errorf("unsettled in the transaction: %v, want x", changedBy(lastApply(t, h.state), "unsettled"))
	}
}

// A provider that answers an update with an error and no state may have changed
// the object. The record is marked, as it is for a call that failed on the wire,
// and a plan of the specification as the record says it reads the object.
func TestFailedUpdateWithoutStateMarksTheRecord(t *testing.T) {
	h := newHarness(t)
	h.apply(flightSpec("1"))
	before := h.revision("a")
	e := h.engine(flightSpec("2"), Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hd := h.fake.hold("a")
	done := applying(e, context.Background(), plan)
	waitFor(t, hd)
	hd.refuse()
	err = returned(t, done)
	if err == nil || !strings.Contains(err.Error(), "could not apply the change") || !strings.Contains(err.Error(), "update of a was in flight; its result is unknown; check the provider") {
		t.Fatalf("error = %v, want the failure of a and the note that its result is unknown", err)
	}
	if strings.Contains(err.Error(), "of b was in flight") {
		t.Errorf("error = %v: b was updated and recorded", err)
	}
	h.reopen()
	a := h.state.Get("a")
	if a == nil || a.RefreshedAt != nil || a.Revision != before+1 {
		t.Fatalf("a: %+v; want it marked for a read, one revision on", a)
	}
	if got := changedBy(lastApply(t, h.state), "unsettled"); !slices.Equal(got, []string{"a"}) {
		t.Errorf("unsettled in the transaction: %v, want a", got)
	}

	// The specification is back at what the record says. The plan reaches nothing,
	// and still reads a.
	h.fake.resetCalls()
	p, err := h.engine(flightSpec("1"), Options{DefaultRefresh: "6h"}).Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := h.fake.readNames(); !slices.Equal(got, []string{"a"}) {
		t.Errorf("the plan read %v, want a", got)
	}
	if p.Stats.Refreshed != 1 {
		t.Errorf("refreshed %d, want 1", p.Stats.Refreshed)
	}
}

// A destroy that the provider refuses with an error and no state is the same: the
// object may be half destroyed, and its record is marked.
func TestFailedDestroyWithoutStateMarksTheRecord(t *testing.T) {
	h := newHarness(t)
	both := spec{
		cue:       "x: " + thing("x", "content: \"x\"") + "\ny: " + thing("y", "content: \"y\"") + "\n",
		resources: []string{"x", "y"},
	}
	h.apply(both)
	h.fake.refuseDestroy("x")
	kept := spec{cue: "y: " + thing("y", "content: \"y\"") + "\n", resources: []string{"y"}}
	err := planAndApply(t, h.engine(kept, Options{}))
	if err == nil || !strings.Contains(err.Error(), "could not destroy the object") || !strings.Contains(err.Error(), "destruction of x was in flight; its result is unknown; check the provider") {
		t.Fatalf("error = %v, want the failure of x and the note that its result is unknown", err)
	}
	h.reopen()
	if x := h.state.Get("x"); x == nil || x.RefreshedAt != nil {
		t.Errorf("x: %+v; want its record left, marked for a read", x)
	}
	if got := changedBy(lastApply(t, h.state), "unsettled"); !slices.Equal(got, []string{"x"}) {
		t.Errorf("unsettled in the transaction: %v, want x", got)
	}
}

// A creation that the provider refuses with an error and no state has no record
// to mark, and the error says what the provider said and nothing more.
func TestFailedCreationWithoutStateAddsNoNote(t *testing.T) {
	h := newHarness(t)
	e := h.engine(flightSpec("1"), Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hd := h.fake.hold("c")
	done := applying(e, context.Background(), plan)
	waitFor(t, hd)
	hd.refuse()
	err = returned(t, done)
	if err == nil || !strings.Contains(err.Error(), "could not apply the change") || strings.Contains(err.Error(), "in flight") {
		t.Fatalf("error = %v, want the failure of c alone", err)
	}
}
