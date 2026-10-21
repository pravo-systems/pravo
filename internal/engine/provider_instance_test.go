package engine

import (
	"context"
	"strings"
	"testing"
)

// rekey makes the engine build the resources of the specification through
// the provider instance key, which the fake provider also serves.
func rekey(e *Engine, key string) {
	for _, r := range e.Root.Resources {
		r.ProviderKey = key
	}
	base, err := e.Providers.instance("fake")
	if err != nil {
		panic(err)
	}
	e.Providers.instances[key] = &instance{p: base.p, key: key, cfg: true}
}

func reasonsOf(plan *Plan, addr string) []string {
	for _, ch := range plan.Changes {
		if ch.Addr == addr {
			return ch.Reasons
		}
	}
	return nil
}

func hasReason(reasons []string, want string) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}

// A resource that is now built through another provider instance than the one
// the state records is planned as no change to the object. The plan says why,
// and the apply records the new key.
func TestChangedProviderInstanceIsReportedAndRecorded(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	s := spec{cue: "a: " + thing("a", "content: \"x\"") + "\n", resources: []string{"a"}}
	h.apply(s)
	before := h.state.Get("a")
	if before.ProviderKey != "fake" {
		t.Fatalf("recorded key = %q, want fake", before.ProviderKey)
	}

	// The same instance is skipped, with nothing to say about instances.
	e := h.engine(s, Options{})
	plan, err := e.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reasonsOf(plan, "a") {
		if strings.Contains(r, "provider instance") {
			t.Errorf("an unchanged key must not be reported: %q", r)
		}
	}

	// Another instance: the plan keeps the object and names both keys.
	e = h.engine(s, Options{})
	rekey(e, "fake.docs")
	plan, err = e.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if plan.HasChanges() {
		t.Error("a changed provider instance must not propose a change to the object")
	}
	var ch *Change
	for _, c := range plan.Changes {
		if c.Addr == "a" {
			ch = c
		}
	}
	if ch == nil || ch.Action != ActionNoop || ch.Skipped {
		t.Fatalf("change = %+v, want a no-op that is not skipped", ch)
	}
	if want := "provider instance changed (fake -> fake.docs)"; !hasReason(ch.Reasons, want) {
		t.Errorf("reasons = %q, want %q", ch.Reasons, want)
	}
	if got := h.state.Get("a").ProviderKey; got != "fake" {
		t.Errorf("a plan changed the recorded key to %q", got)
	}

	// The apply records the key. The object, its configuration hash and the
	// time it was applied stay as they are.
	if _, err := e.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	after := h.state.Get("a")
	if after.ProviderKey != "fake.docs" {
		t.Errorf("recorded key = %q, want fake.docs", after.ProviderKey)
	}
	if string(after.Attributes) != string(before.Attributes) || after.ConfigHash != before.ConfigHash {
		t.Errorf("the object changed: %s -> %s", before.Attributes, after.Attributes)
	}
	if !after.AppliedAt.Equal(*before.AppliedAt) {
		t.Errorf("applied_at moved from %v to %v", before.AppliedAt, after.AppliedAt)
	}
	if after.Revision != before.Revision+1 {
		t.Errorf("revision = %d, want %d", after.Revision, before.Revision+1)
	}

	// The next plan under the new key skips the resource.
	e = h.engine(s, Options{})
	rekey(e, "fake.docs")
	plan, err = e.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range plan.Changes {
		if c.Addr == "a" && !c.Skipped {
			t.Errorf("a was planned again after its key was recorded: %q", c.Reasons)
		}
	}
}
