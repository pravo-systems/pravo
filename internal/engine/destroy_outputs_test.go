package engine

import (
	"context"
	"path/filepath"
	"testing"
)

func destroyOutputsSpec() spec {
	return spec{
		cue: "a: " + thing("a", "") + "\n" +
			"b: " + thing("b", "") + "\n" +
			"mine: {value: a.id}\n" +
			"url: {value: \"https://\\(b.id)\"}\n" +
			"both: {value: {first: a.id, second: b.id}}\n" +
			"fixed: {value: \"constant\"}\n",
		resources: []string{"a", "b"},
		outputs:   []string{"both", "fixed", "mine", "url"},
	}
}

// destroyPlan plans a destroy of the specification, saves it and loads it
// again as a command that applies a plan file does.
func destroyPlan(t *testing.T, h *harness, s spec) (string, *PlanFile) {
	t.Helper()
	e := h.engine(s, Options{Destroy: true})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "destroy.plan")
	if err := e.SavePlan(file, plan, nil, false); err != nil {
		t.Fatal(err)
	}
	pf, err := ReadPlanFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return file, pf
}

// A destroy of a part of the state removes the outputs that read a resource
// it destroys and keeps the others. The destroy of the rest removes them all.
func TestPartialDestroyKeepsTheOutputsOfWhatRemains(t *testing.T) {
	h := newHarness(t)
	s := destroyOutputsSpec()
	h.apply(s)
	if got := keys(h.state.Outputs()); got != "both fixed mine url" {
		t.Fatalf("outputs after apply = %s", got)
	}
	file, pf := destroyPlan(t, h, s)

	// Only b: mine reads a and stays, url and both read b and go.
	e := h.engine(s, Options{Refresh: RefreshNone})
	loaded, err := e.LoadPlan(pf)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := loaded.Subset([]string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	if !sel.Partial {
		t.Fatal("a selection that leaves a out is not marked partial")
	}
	res, err := e.ApplySaved(context.Background(), sel)
	if err != nil {
		t.Fatalf("partial destroy: %v", err)
	}
	if res.Destroyed != 1 {
		t.Errorf("destroyed %d, want 1", res.Destroyed)
	}
	if h.state.Get("b") != nil || h.state.Get("a") == nil {
		t.Errorf("state after the partial destroy: a = %v, b = %v", h.state.Get("a") != nil, h.state.Get("b") != nil)
	}
	if got := keys(h.state.Outputs()); got != "fixed mine" {
		t.Errorf("outputs after the partial destroy = %s, want fixed mine", got)
	}
	// The outputs on disk are the ones in the store.
	reloaded, err := h.state.ReloadOutputs()
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(reloaded); got != "fixed mine" {
		t.Errorf("recorded outputs after the partial destroy = %s, want fixed mine", got)
	}
	if v, _ := UnwrapOutput(reloaded["mine"]); string(v) == "" || string(v) == "null" {
		t.Errorf("mine = %s, want the id of a", v)
	}
	if err := MarkApplied(file, pf, []string{"b"}, res.Txn); err != nil {
		t.Fatal(err)
	}

	// The rest: a goes, and with it every output.
	pf, err = ReadPlanFile(file)
	if err != nil {
		t.Fatal(err)
	}
	e = h.engine(s, Options{Refresh: RefreshNone})
	loaded, err = e.LoadPlan(pf)
	if err != nil {
		t.Fatalf("the plan is stale after a partial destroy: %v", err)
	}
	rest := loaded.Remaining()
	if rest.Partial {
		t.Fatal("the rest of the plan is partial")
	}
	if _, err := e.ApplySaved(context.Background(), rest); err != nil {
		t.Fatalf("destroy of the rest: %v", err)
	}
	if h.state.Len() != 0 {
		t.Errorf("%d records remain", h.state.Len())
	}
	if got := keys(h.state.Outputs()); got != "" {
		t.Errorf("outputs after the last resource is destroyed = %s, want none", got)
	}
}

// A destroy that leaves nothing behind clears the outputs, also those that
// read no resource.
func TestFullDestroyClearsEveryOutput(t *testing.T) {
	h := newHarness(t)
	s := destroyOutputsSpec()
	h.apply(s)
	e := h.engine(s, Options{Destroy: true})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if got := keys(h.state.Outputs()); got != "" {
		t.Errorf("outputs after destroy = %s, want none", got)
	}
}

// An output that reads a resource through a comprehension or an intermediate
// field goes with the resource.
func TestPartialDestroyDropsOutputsThatReadThroughOtherFields(t *testing.T) {
	h := newHarness(t)
	s := spec{
		cue: "a: " + thing("a", "") + "\n" +
			"b: " + thing("b", "") + "\n" +
			"mid: {x: b.id}\n" +
			"via: {value: mid.x}\n" +
			"all: {value: [for k, v in {p: a.id, q: b.id} {v}]}\n" +
			"kept: {value: [for k, v in {p: a.id} {v}]}\n",
		resources: []string{"a", "b"},
		outputs:   []string{"all", "kept", "via"},
	}
	h.apply(s)
	if got := keys(h.state.Outputs()); got != "all kept via" {
		t.Fatalf("outputs after apply = %s", got)
	}
	_, pf := destroyPlan(t, h, s)
	e := h.engine(s, Options{Refresh: RefreshNone})
	loaded, err := e.LoadPlan(pf)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := loaded.Subset([]string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplySaved(context.Background(), sel); err != nil {
		t.Fatal(err)
	}
	if got := keys(h.state.Outputs()); got != "kept" {
		t.Errorf("outputs after the partial destroy = %s, want kept", got)
	}
}
