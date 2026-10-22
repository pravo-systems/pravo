package engine

import (
	"encoding/json"
	"testing"
)

func readBackContent(t *testing.T, raw []byte) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	s, _ := m["content"].(string)
	return s
}

// An apply reads back what it wrote, and records what the provider returns: a
// resource that another one of the run changed is recorded as it is, with no mark
// that the next read would have left on it.
func TestApplyReadsBackWhatItWrote(t *testing.T) {
	// b reads a, so that a is made first; making b changes a.
	two := spec{
		cue:       "a: " + thing("a", `content: "x"`) + "\nb: " + thing("b", `content: "of \(a.id)"`) + "\n",
		resources: []string{"a", "b"},
	}
	h := newHarness(t)
	h.fake.changeOnApply("b", "a", "changed by b")
	h.apply(two)
	h.reopen()
	a, b := h.state.Get("a"), h.state.Get("b")
	if got := readBackContent(t, a.Attributes); got != "changed by b" {
		t.Errorf("a is recorded with content %q, want what the read returned", got)
	}
	if a.Marked() {
		t.Errorf("a carries a mark after the apply")
	}
	if got := readBackContent(t, b.Attributes); got == "" || got == "changed by b" {
		t.Errorf("b is recorded with content %q, want the one that it was given", got)
	}
	if n := h.fake.called("ReadResource"); n != 2 {
		t.Errorf("the apply read %d records, want one for each node that it wrote", n)
	}
}

// A read that finds no object, or that fails, does not fail the apply and leaves
// the record as the apply wrote it.
func TestApplyWhoseReadBackFindsNothingStillSucceeds(t *testing.T) {
	one := spec{cue: "a: " + thing("a", `content: "x"`) + "\n", resources: []string{"a"}}
	h := newHarness(t)
	h.fake.vanish("a")
	h.apply(one)
	h.reopen()
	a := h.state.Get("a")
	if a == nil || a.Marked() || a.Gone {
		t.Fatalf("a is %+v after an apply whose read found nothing", a)
	}
	if got := readBackContent(t, a.Attributes); got != "x" {
		t.Errorf("a is recorded with content %q, want the one that was written", got)
	}
}

// An apply that changes nothing reads nothing.
func TestApplyOfNothingReadsNothing(t *testing.T) {
	one := spec{cue: "a: " + thing("a", `content: "x"`) + "\n", resources: []string{"a"}}
	h := newHarness(t)
	h.apply(one)
	h.fake.resetCalls()
	h.apply(one)
	if n := h.fake.called("ReadResource"); n != 0 {
		t.Errorf("an apply of an unchanged specification read %d records", n)
	}
}
