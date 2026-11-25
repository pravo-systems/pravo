package engine

import "testing"

// A read that finds only what the specification does not manage leaves the plan
// with nothing to do, and no mark that says otherwise.
func TestDriftOfAnUnmanagedFieldIsNoopAndLeavesNoMark(t *testing.T) {
	h := newHarness(t)
	s := spec{cue: "a: " + thing("a", "") + "\n", resources: []string{"a"}}
	h.apply(s)
	h.fake.driftID("a", "other-id")
	p := plan(t, h.engine(s, Options{Refresh: RefreshAll}))
	if len(p.Changes) != 1 || !p.Changes[0].Drifted || p.Changes[0].Action != ActionNoop || p.HasChanges() {
		t.Fatalf("the plan %+v: want a drifted no-op and no changes", p.Changes)
	}
	if h.state.Get("a").Marked() {
		t.Error("a is marked: an apply has nothing to write for it")
	}
	if got := recordID(t, h.state.Get("a")); got != "other-id" {
		t.Errorf("the record holds id %q, not what the read found", got)
	}
	p2 := plan(t, h.engine(s, Options{Refresh: RefreshAll}))
	for _, c := range p2.Changes {
		if c.Drifted {
			t.Errorf("%s drifted again", c.Addr)
		}
		for _, r := range c.Reasons {
			if r == "an earlier read found the real state differs from the state (drift); it is planned until an apply writes the record" {
				t.Errorf("%s: %s", c.Addr, r)
			}
		}
	}
}
