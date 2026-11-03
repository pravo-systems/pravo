package engine

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// A provider that is set up from a resource: app lives on prov, whose region is the
// id of base.
func providerFromBase() spec {
	return spec{
		cue: "base: " + thing("base", "") + "\nprov: {region: \"r-\\(base.id)\"}\n" +
			"app: " + thing("app", "") + "\n",
		resources: []string{"base", "app"}, providers: map[string]string{"fake": "prov", "fake.docs": "unset"},
		keys: map[string]string{"base": "fake.docs"},
	}
}

// Destroying everything reads and plans the resources of a provider that is
// configured from another resource, so the configuration is known when it is used.
func TestDestroyConfiguresAProviderFromTheStateOfWhatItReads(t *testing.T) {
	s := providerFromBase()
	for name, refresh := range map[string]RefreshMode{"default refresh": RefreshAge, "no refresh": RefreshNone} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.apply(s)
			h.fake.answersOnlyWhenConfigured()
			e := h.engine(s, Options{Destroy: true, Refresh: refresh})
			p, err := e.Plan(context.Background())
			if err != nil {
				t.Fatalf("plan destroy: %v", err)
			}
			if _, err := e.Apply(context.Background(), p); err != nil {
				t.Fatalf("destroy: %v", err)
			}
			if h.state.Len() != 0 {
				t.Errorf("%d records remain", h.state.Len())
			}
		})
	}
}

// Deleting a block whose provider is still declared and set up from another
// resource: the records of the removed block are read with the provider configured.
func TestRemovedBlockOfAProviderSetUpFromAResource(t *testing.T) {
	full := providerFromBase()
	h := newHarness(t)
	h.apply(full)
	h.fake.answersOnlyWhenConfigured()
	kept := spec{
		cue:       "base: " + thing("base", "") + "\nprov: {region: \"r-\\(base.id)\"}\n",
		resources: []string{"base"}, providers: full.providers, keys: full.keys,
	}
	e := h.engine(kept, Options{})
	p, err := e.Plan(context.Background())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := e.Apply(context.Background(), p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := h.state.Addresses(); !reflect.DeepEqual(got, []string{"base"}) {
		t.Errorf("records after the apply: %v, want base", got)
	}
}

// A removed resource whose object is already gone: the plan removes the record, and
// says so, instead of planning to create it again and leaving it for ever.
func TestRemovedResourceWhoseObjectIsGoneLosesItsRecord(t *testing.T) {
	full := spec{
		cue:       "keep: " + thing("keep", "") + "\nold: " + thing("old", "") + "\n",
		resources: []string{"keep", "old"},
	}
	h := newHarness(t)
	h.apply(full)
	h.fake.vanish("old")
	var said []string
	kept := spec{cue: "keep: " + thing("keep", "") + "\n", resources: []string{"keep"}}
	e := h.engine(kept, Options{Progress: func(f string, a ...any) { said = append(said, fmt.Sprintf(f, a...)) }})
	p, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if add, change, destroy, _ := p.Counts(); add != 0 || change != 0 || destroy != 1 {
		t.Errorf("plan counts %d/%d/%d, want one removal", add, change, destroy)
	}
	for _, line := range said {
		if strings.Contains(line, "created again") {
			t.Errorf("the plan of a removal says %q", line)
		}
	}
	if _, err := e.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if got := h.state.Addresses(); !reflect.DeepEqual(got, []string{"keep"}) {
		t.Errorf("records after the apply: %v, want keep", got)
	}
	if n, _ := h.fake.destroyed(); len(n) != 0 {
		t.Errorf("the provider was asked to destroy %v for an object that is gone", n)
	}
}

// pravo destroy of objects that are gone leaves no records either.
func TestDestroyOfObjectsThatAreGoneRemovesTheRecords(t *testing.T) {
	s := spec{cue: "x: " + thing("x", "") + "\ny: " + thing("y", "") + "\n", resources: []string{"x", "y"}}
	h := newHarness(t)
	h.apply(s)
	h.fake.vanish("x")
	h.fake.vanish("y")
	e := h.engine(s, Options{Destroy: true})
	p, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if h.state.Len() != 0 {
		t.Errorf("records left: %v", h.state.Addresses())
	}
}

// A record that carried prevent_destroy keeps the guard in the plan that removes
// its block: the plan warns, and names the resource for the caller that has to allow it.
func TestRemovedGuardedResourceIsFlagged(t *testing.T) {
	full := spec{
		cue:       "keep: " + thing("keep", "") + "\ndb: " + thing("db", `$lifecycle: prevent_destroy: true`) + "\n",
		resources: []string{"keep", "db"},
	}
	h := newHarness(t)
	h.apply(full)
	if n := h.state.Get("db"); n == nil || !n.PreventDestroy {
		t.Fatal("the record does not carry the guard")
	}
	kept := spec{cue: "keep: " + thing("keep", "") + "\n", resources: []string{"keep"}}
	e := h.engine(kept, Options{})
	p, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := p.GuardedDestroys(); !reflect.DeepEqual(got, []string{"db"}) {
		t.Fatalf("guarded destroys %v, want [db]", got)
	}
	warned := false
	for _, c := range p.Changes {
		for _, w := range c.Warnings {
			warned = warned || strings.Contains(w.Summary, "prevent_destroy")
		}
	}
	if !warned {
		t.Error("the plan does not warn about the guard")
	}
}
