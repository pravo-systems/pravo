package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// replChain has a, b and c where b reads the name of a and c the name of b, so that renaming a
// replaces all three. extra adds lifecycle fields to the named node.
func replChain(v string, extra map[string]string) spec {
	node := func(name, ex string) string { return fmt.Sprintf("{name: %s, id: string | null\n%s}", name, ex) }
	return spec{cue: "a: " + node(`"a-`+v+`"`, extra["a"]) + "\nb: " + node(`"b-\(a.name)"`, extra["b"]) + "\nc: " + node(`"c-\(b.name)"`, extra["c"]) + "\n",
		resources: []string{"a", "b", "c"}}
}

func orderOf(h *harness) string { return strings.Join(h.fake.order, "; ") }

// A replaced resource goes after the replaced resources that depend on it, and the new objects
// are created in dependency order: the provider refuses to delete an object that is still in use.
func TestReplaceDestroysDependentsFirst(t *testing.T) {
	// b and c names are overridden by the references, so only the a name differs between versions.
	h := newHarness(t)
	h.apply(replChain("1", nil))
	h.fake.order = nil
	res := h.apply(replChain("2", nil))
	got := orderOf(h)
	inOrder(t, got, "destroy c-b-a-1", "destroy b-a-1", "destroy a-1", "create a-2", "create b-a-2", "create c-b-a-2")
	if res.Destroyed != 3 || res.Added != 3 {
		t.Errorf("destroyed %d, added %d", res.Destroyed, res.Added)
	}
	for _, a := range []string{"a", "b", "c"} {
		if h.state.Get(a) == nil {
			t.Errorf("no record for %s", a)
		}
	}
	// Nothing is left to do.
	e := h.engine(replChain("2", nil), Options{})
	if p := mustPlan(t, e); p.NeedsApply() {
		t.Errorf("a second plan still has changes")
	}
}

// A dependent that is only updated is not destroyed; one that is not changed is left alone.
func TestReplaceLeavesUpdatedDependentsAlone(t *testing.T) {
	mk := func(v, cv string) spec {
		return spec{cue: fmt.Sprintf("a: %s\nb: %s\n", thing("a-"+v, ""), thing("b", `content: "\(a.name)`+cv+`"`)), resources: []string{"a", "b"}}
	}
	h := newHarness(t)
	h.apply(mk("1", "x"))
	h.fake.order = nil
	h.apply(mk("2", "x"))
	got := orderOf(h)
	if got != "destroy a-1; create a-2; update b" {
		t.Fatalf("order = %s", got)
	}
}

// A stop between the destroys and the creates leaves a state the next apply finishes.
func TestReplaceFailingBetweenDestroysAndCreates(t *testing.T) {
	h := newHarness(t)
	h.apply(replChain("1", nil))
	h.fake.refuseDestroy("a-1")
	e := h.engine(replChain("2", nil), Options{})
	if _, err := e.Apply(context.Background(), mustPlan(t, e)); err == nil {
		t.Fatal("apply succeeded")
	}
	// c and b were destroyed and their records removed; a is untouched and still recorded.
	if h.state.Get("c") != nil || h.state.Get("b") != nil || h.state.Get("a") == nil {
		t.Fatalf("records after the failure: a=%v b=%v c=%v", h.state.Get("a") != nil, h.state.Get("b") != nil, h.state.Get("c") != nil)
	}
	h.fake.undestroyable = nil
	h.fake.order = nil
	h.reopen()
	h.apply(replChain("2", nil))
	if got := orderOf(h); got != "destroy a-1; create a-2; create b-a-2; create c-b-a-2" {
		t.Fatalf("order of the second apply = %s", got)
	}
}

// The same order holds when the plan is saved to a file and applied from it, and for a diamond:
// d reads b and c, which both read a.
func TestReplaceOrderFromASavedPlan(t *testing.T) {
	diamond := func(v string) spec {
		node := func(name string) string { return fmt.Sprintf("{name: %s, id: string | null}", name) }
		return spec{cue: "a: " + node(`"a-`+v+`"`) + "\nb: " + node(`"b-\(a.name)"`) + "\nc: " + node(`"c-\(a.name)"`) +
			"\nd: {name: \"d-\\(b.name)-\\(c.name)\", id: string | null}\n", resources: []string{"a", "b", "c", "d"}}
	}
	h := newHarness(t)
	h.apply(diamond("1"))
	h.fake.order = nil
	s := diamond("2")
	_, pf := planned(t, h, s)
	e := h.engine(s, Options{})
	loaded, err := e.LoadPlan(pf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplySaved(context.Background(), loaded); err != nil {
		t.Fatal(err)
	}
	got := orderOf(h)
	inOrder(t, got, "destroy d-", "destroy b-a-1", "destroy a-1", "create a-2", "create b-a-2", "create d-")
	inOrder(t, got, "destroy d-", "destroy c-a-1", "destroy a-1", "create a-2", "create c-a-2", "create d-")
}

// inOrder fails unless each step occurs in got after the one before it.
func inOrder(t *testing.T, got string, steps ...string) {
	t.Helper()
	at := 0
	for _, w := range steps {
		i := strings.Index(got[at:], w)
		if i < 0 {
			t.Fatalf("%q does not follow the steps before it in %s", w, got)
		}
		at += i + len(w)
	}
}
