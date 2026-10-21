package engine

import "testing"

// The records of resources that the specification no longer declares are in
// Refreshed, Fresh and NotRead, and Removed says how many they are, so that the
// counts add up to the records of the declarations and the removed ones.
func TestStatsCountTheRecordsOfRemovedResources(t *testing.T) {
	same := func(int) string { return "v1" }
	for _, c := range []struct {
		name    string
		age     string
		destroy bool
		want    Stats
	}{
		{"read by default", "", false, Stats{Total: 3, Skipped: 3, Removed: 2, Refreshed: 2, NotRead: 3}},
		{"within the refresh age", "6h", false, Stats{Total: 3, Skipped: 3, Removed: 2, Fresh: 5}},
		{"a destroy plan counts none", "6h", true, Stats{Total: 3, Fresh: 5}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.apply(manySpec(5, same))
			p := plan(t, h.engine(manySpec(3, same), Options{DefaultRefresh: c.age, Destroy: c.destroy}))
			if p.Stats != c.want {
				t.Errorf("stats = %+v, want %+v", p.Stats, c.want)
			}
			if got, want := p.Stats.Refreshed+p.Stats.Fresh+p.Stats.NotRead, p.Stats.Total+p.Stats.Removed; !c.destroy && got != want {
				t.Errorf("%d refreshed, within the age or not read; the declarations and the removed ones are %d", got, want)
			}
		})
	}
}
