package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/plugin"
)

// deferSpec is a cluster (base), an object whose content needs the cluster to be
// planned (a: its provider defers it), something that reads that object (b) and an
// output of it.
func deferSpec() spec {
	return spec{
		cue: "base: " + thing("base", "") + "\n" +
			"a: " + thing("a", "content: \"in-\\(base.id)\"") + "\n" +
			"b: " + thing("b", "content: \"after-\\(a.name)\"") + "\n" +
			"c: " + thing("c", "content: \"alone\"") + "\n" +
			"seen: {value: a.id}\n",
		resources: []string{"base", "a", "b", "c"}, outputs: []string{"seen"},
	}
}

func TestPlanDefersWhatTheProviderCannotPlanYet(t *testing.T) {
	h := newHarness(t)
	h.fake.defersUnknown = true
	s := deferSpec()
	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a := changeOf(t, plan, "a"); a.Action != ActionDefer || !strings.Contains(a.DeferReason, plugin.DeferredResourceConfigUnknown) {
		t.Fatalf("a: %s (%q), want deferred because its configuration is unknown", a.Action, a.DeferReason)
	}
	if b := changeOf(t, plan, "b"); b.Action != ActionDefer || !strings.Contains(b.DeferReason, "it reads a") {
		t.Fatalf("b: %s (%q), want deferred because it reads a", b.Action, b.DeferReason)
	}
	for _, addr := range []string{"base", "c"} {
		if c := changeOf(t, plan, addr); c.Action != ActionCreate {
			t.Fatalf("%s: %s, want create", addr, c.Action)
		}
	}
	if n := plan.DeferredCount(); n != 2 {
		t.Fatalf("deferred %d, want 2", n)
	}
	if add, _, _, _ := plan.Counts(); add != 2 {
		t.Fatalf("to add %d, want 2: a deferred change is not counted as one", add)
	}
	for _, o := range plan.EvalOutputs {
		if o == "seen" {
			t.Fatalf("the apply evaluates an output that reads a deferred change")
		}
	}

	res, err := e.Apply(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 2 || res.Deferred != 2 {
		t.Fatalf("added %d, deferred %d; want 2 and 2", res.Added, res.Deferred)
	}
	if h.state.Get("a") != nil || h.state.Get("b") != nil {
		t.Fatalf("the apply wrote a deferred change")
	}
	if h.state.Get("base") == nil || h.state.Get("c") == nil {
		t.Fatalf("the apply left out a change that was not deferred")
	}
	if _, ok := h.state.Outputs()["seen"]; ok {
		t.Fatalf("the apply recorded an output that reads a deferred change")
	}

	// The next plan knows the id of base: nothing waits any more.
	e = h.engine(s, Options{})
	plan, err = e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n := plan.DeferredCount(); n != 0 {
		t.Fatalf("the second plan defers %d changes, want none", n)
	}
	if res, err = e.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if res.Added != 2 || res.Deferred != 0 || h.state.Get("a") == nil || h.state.Get("b") == nil {
		t.Fatalf("second apply: added %d, deferred %d; want a and b created", res.Added, res.Deferred)
	}
	if _, ok := h.state.Outputs()["seen"]; !ok {
		t.Fatalf("the second apply did not record the output")
	}
}

func TestSavedPlanKeepsItsDeferredChanges(t *testing.T) {
	h := newHarness(t)
	h.fake.defersUnknown = true
	s := deferSpec()
	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "p.plan")
	if err := e.SavePlan(file, plan, nil, false); err != nil {
		t.Fatal(err)
	}
	pf, err := ReadPlanFile(file)
	if err != nil {
		t.Fatal(err)
	}
	e = h.engine(s, Options{})
	loaded, err := e.LoadPlan(pf)
	if err != nil {
		t.Fatal(err)
	}
	if a := changeOf(t, loaded, "a"); a.Action != ActionDefer || a.DeferReason == "" {
		t.Fatalf("a in the loaded plan: %s (%q), want deferred with its reason", a.Action, a.DeferReason)
	}
	res, err := e.ApplySaved(context.Background(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deferred != 2 || h.state.Get("a") != nil {
		t.Fatalf("deferred %d, a recorded %v; want 2 and no record", res.Deferred, h.state.Get("a") != nil)
	}
}

// An apply never defers: a node the plan did not defer and that the provider cannot
// answer at the apply fails, and says to plan again.
func TestApplyOfAChangeTheProviderDefersOnlyThenFails(t *testing.T) {
	h := newHarness(t)
	s := deferSpec()
	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.DeferredCount() != 0 {
		t.Fatalf("a provider that does not defer had changes deferred")
	}
	h.fake.mu.Lock()
	h.fake.defersNamed = map[string]bool{"c": true}
	h.fake.mu.Unlock()
	_, err = e.Apply(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "plan again") {
		t.Fatalf("apply: %v, want a failure that says to plan again", err)
	}
}

func TestDeferredDiagnosticIsAnError(t *testing.T) {
	d := plugin.Diagnostics{{Severity: plugin.SeverityDeferred, Summary: plugin.DeferredProviderConfigUnknown}}
	if !d.HasErrors() || d.Err() == nil || !strings.Contains(d.Error(), "cannot defer") {
		t.Fatalf("a deferred answer that nothing handles must fail: %v", d.Err())
	}
	if why, ok := d.Deferred(); !ok || why != plugin.DeferredProviderConfigUnknown {
		t.Fatalf("Deferred: %q %v", why, ok)
	}
}

// The note of what an environment runs says how many changes its apply deferred.
func TestNoteOfAnEnvironmentSaysWhatWasDeferred(t *testing.T) {
	h := newHarness(t)
	h.fake.defersUnknown = true
	s := deferSpec()
	e := h.engine(s, Options{})
	e.Root.Environment = "dev"
	e.Root.Environments = []config.Environment{{Name: "dev"}}
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	n, _, err := h.state.LatestRun(context.Background(), time.Now().Add(time.Minute))
	if err != nil || n == nil || n.Deferred != 2 {
		t.Fatalf("note: %+v %v; want 2 deferred", n, err)
	}
	e = h.engine(s, Options{})
	e.Root.Environment = "dev"
	e.Root.Environments = []config.Environment{{Name: "dev"}}
	if plan, err = e.Plan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if n, _, err = h.state.LatestRun(context.Background(), time.Now().Add(time.Minute)); err != nil || n == nil || n.Deferred != 0 {
		t.Fatalf("note after the second apply: %+v %v; want none deferred", n, err)
	}
}

// A change that is deferred still points at the old instance of a create_before_destroy
// replacement: the instance is destroyed by the apply that follows, after the change.
func TestCreateBeforeDestroyKeepsTheOldInstanceForADeferredDependent(t *testing.T) {
	ctx := context.Background()
	mk := func(name string) spec {
		return spec{
			cue:       "base: " + thing(name, "$lifecycle: create_before_destroy: true") + "\na: " + thing("a", "content: \"in-\\(base.id)\"") + "\n",
			resources: []string{"base", "a"},
		}
	}
	h := newHarness(t)
	h.apply(mk("one"))
	h.fake.defersUnknown = true
	h.fake.mu.Lock()
	h.fake.order = nil
	h.fake.mu.Unlock()
	e := h.engine(mk("two"), Options{})
	plan := mustPlan(t, e)
	if plan.DeferredCount() != 1 {
		t.Fatalf("deferred %d, want 1", plan.DeferredCount())
	}
	res, err := e.Apply(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	order := func() string {
		h.fake.mu.Lock()
		defer h.fake.mu.Unlock()
		return strings.Join(h.fake.order, "; ")
	}
	if got := order(); strings.Contains(got, "destroy") {
		t.Errorf("calls = %q: the old instance was destroyed while a change that points at it was deferred", got)
	}
	if res.Added != 1 || res.Destroyed != 0 {
		t.Errorf("apply counts = %d added, %d destroyed, want 1 and 0", res.Added, res.Destroyed)
	}
	h.reopen()
	h.fake.defersUnknown = false
	h.fake.mu.Lock()
	h.fake.order = nil
	h.fake.mu.Unlock()
	e = h.engine(mk("two"), Options{})
	res, err = e.Apply(ctx, mustPlan(t, e))
	if err != nil {
		t.Fatal(err)
	}
	if got := order(); got != "update a; destroy one" || res.Destroyed != 1 {
		t.Errorf("follow-up calls = %q, %d destroyed; want the update, then the destroy", got, res.Destroyed)
	}
}
