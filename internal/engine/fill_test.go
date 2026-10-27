package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// chainSpec is n resources, each of which reads the id of the one before it
// when after is set, and is independent of the others otherwise.
func chainSpec(n int, after bool) spec {
	var src strings.Builder
	var names []string
	for i := range n {
		name := fmt.Sprintf("r%03d", i)
		names = append(names, name)
		extra := ""
		if after && i > 0 {
			extra = fmt.Sprintf("content: \"after \\(r%03d.id)\"", i-1)
		}
		fmt.Fprintf(&src, "%s: %s\n", name, thing(name, extra))
	}
	return spec{cue: src.String(), resources: names}
}

// Every fill unified into the configuration evaluates the whole of it again,
// so independent resources must not each cost one.
func TestIndependentResourcesAreFilledInOneStep(t *testing.T) {
	s := chainSpec(120, false)
	h := newHarness(t)

	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if e.settles > 1 {
		t.Errorf("planning unified fills %d times, want at most once", e.settles)
	}
	if _, err := e.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if e.settles > 2 {
		t.Errorf("planning and applying unified fills %d times, want at most twice", e.settles)
	}

	e = h.engine(s, Options{})
	plan, err = e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.HasChanges() {
		t.Error("a second plan found changes")
	}
	if e.settles > 1 {
		t.Errorf("a plan without changes unified fills %d times, want at most once", e.settles)
	}
}

// A resource that reads another's computed value runs after that value is part
// of the configuration, however the fills are batched.
func TestDependentReadsTheFilledValueOfItsDependency(t *testing.T) {
	s := chainSpec(12, true)
	h := newHarness(t)
	h.apply(s)
	for i := 1; i < 12; i++ {
		prev := h.state.Get(fmt.Sprintf("r%03d", i-1))
		got := h.state.Get(fmt.Sprintf("r%03d", i))
		if prev == nil || got == nil {
			t.Fatalf("r%03d is not in the state", i)
		}
		if want := fmt.Sprintf("%q", "after "+idFromJSON(t, prev.Attributes)); !strings.Contains(string(got.Attributes), `"content":`+want) {
			t.Errorf("r%03d has %s, want content %s", i, got.Attributes, want)
		}
	}
	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.HasChanges() {
		t.Error("a second plan found changes")
	}
}

func idFromJSON(t *testing.T, attrs []byte) string {
	t.Helper()
	const key = `"id":"`
	s := string(attrs)
	i := strings.Index(s, key)
	if i < 0 {
		t.Fatalf("no id in %s", s)
	}
	s = s[i+len(key):]
	return s[:strings.Index(s, `"`)]
}

// A value the provider computes that conflicts with the specification is
// refused, and the error names the resource it came from even when its fill
// was unified together with those of others.
func TestConflictingFillIsRefusedAndNamesItsResource(t *testing.T) {
	s := spec{
		cue: "a: " + thing("a", "") + "\n" +
			"b: {name: \"b\", id: =~\"^never\"}\n" +
			"c: " + thing("c", "") + "\n",
		resources: []string{"a", "b", "c"},
	}
	h := newHarness(t)
	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("apply accepted a value that conflicts with the specification")
	}
	if !strings.Contains(err.Error(), "b: filling values into the configuration") {
		t.Errorf("error %q does not name the resource b", err)
	}
	if strings.Contains(err.Error(), "a: filling") || strings.Contains(err.Error(), "c: filling") {
		t.Errorf("error %q names a resource that did not conflict", err)
	}
}
