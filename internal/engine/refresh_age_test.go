package engine

import (
	"testing"
	"time"
)

// A record is past its refresh age when it is older than the age. A record that
// is exactly as old as the age is within it.
func TestOlderThan(t *testing.T) {
	read := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	const age = 6 * time.Hour
	for _, c := range []struct {
		name string
		now  time.Time
		past bool
	}{
		{"read just now", read, false},
		{"a nanosecond short of the age", read.Add(age - 1), false},
		{"exactly the age", read.Add(age), false},
		{"a nanosecond past the age", read.Add(age + 1), true},
		{"a day past the age", read.Add(age + 24*time.Hour), true},
		{"a clock that is behind the read", read.Add(-time.Hour), false},
	} {
		if got := olderThan(read, c.now, age); got != c.past {
			t.Errorf("%s: past the age is %v, want %v", c.name, got, c.past)
		}
	}
}

// The plan reads a record that it reaches only when the record is older than
// the refresh age: at the age it is not read, a nanosecond later it is.
func TestPlanReadsARecordPastItsAgeOnly(t *testing.T) {
	const age = 6 * time.Hour
	v1 := manySpec(3, func(int) string { return "v1" })
	v2 := manySpec(3, func(i int) string {
		if i == 0 {
			return "v2"
		}
		return "v1"
	})
	for _, c := range []struct {
		name    string
		elapsed time.Duration
		reads   []string
	}{
		{"just read", 0, nil},
		{"a nanosecond short of the age", age - 1, nil},
		{"exactly the age", age, nil},
		{"a nanosecond past the age", age + 1, []string{"r000"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.apply(v1)
			sr := h.state.Get("r000")
			if sr == nil || sr.RefreshedAt == nil {
				t.Fatal("the record of r000 has no read time")
			}
			read := *sr.RefreshedAt
			h.fake.resetCalls()

			e := h.engine(v2, Options{DefaultRefresh: "6h"})
			e.now = func() time.Time { return read.Add(c.elapsed) }
			p := plan(t, e)

			if got := h.fake.readNames(); len(got) != len(c.reads) || (len(got) == 1 && got[0] != c.reads[0]) {
				t.Errorf("read %v, want %v", got, c.reads)
			}
			ch := changeOf(t, p, "r000")
			if ch.Action != ActionUpdate {
				t.Errorf("r000 is %s, want update", ch.Action)
			}
			if read := len(c.reads) == 1; ch.Refreshed != read || ch.Fresh != !read {
				t.Errorf("r000: refreshed %v, within the age %v; want refreshed %v", ch.Refreshed, ch.Fresh, read)
			}
		})
	}
}
