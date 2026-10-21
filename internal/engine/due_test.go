package engine

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// DriftDue reads the records that are past their refresh age, the oldest
// first, a record that was never refreshed before any other, and no more than
// the limit. It reads nothing else and plans nothing.
func TestDriftDueReadsTheOldestRecordsPastTheirAge(t *testing.T) {
	s := spec{
		cue: "a: " + thing("a", "") + "\nb: " + thing("b", "") + "\nc: " + thing("c", "") + "\nd: " + thing("d", "") + "\n" +
			"e: " + thing("e", "") + "\nyoung: " + thing("young", `$lifecycle: refresh: "1h"`) + "\n",
		resources: []string{"a", "b", "c", "d", "e", "young"},
	}
	h := newHarness(t)
	h.apply(s)
	// Make the records as old as the test needs: c is the oldest, a and b share
	// a time, d was never refreshed, and e is the newest.
	now := time.Now().UTC()
	age := func(addr string, ago time.Duration) {
		r := *h.state.Get(addr)
		at := now.Add(-ago)
		r.RefreshedAt = &at
		if ago == 0 {
			r.RefreshedAt = nil
		}
		if err := h.state.Set(&r, ""); err != nil {
			t.Fatal(err)
		}
	}
	age("c", 5*time.Hour)
	age("a", 3*time.Hour)
	age("b", 3*time.Hour)
	age("d", 0)
	age("e", 2*time.Hour)
	h.fake.vanish("b")
	h.fake.resetCalls()

	run := func(limit int) *DueReport {
		t.Helper()
		e := h.engine(s, Options{DefaultRefresh: "1m"})
		rep, err := e.DriftDue(context.Background(), limit)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	rep := run(4)
	if want := []string{"d", "c", "a", "b"}; !reflect.DeepEqual(rep.Read, want) {
		t.Errorf("read %v, want %v", rep.Read, want)
	}
	// young sets an age of an hour and was refreshed by the apply just now.
	if rep.Records != 6 || rep.Due != 5 {
		t.Errorf("%d records, %d due; want 6 and 5", rep.Records, rep.Due)
	}
	if want := []DueDrift{{Addr: "b", Gone: true}}; !reflect.DeepEqual(rep.Drifted, want) {
		t.Errorf("drifted %v, want %v", rep.Drifted, want)
	}
	if got := h.fake.readNames(); !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("the provider read %v", got)
	}
	expectCalls(t, h, map[string]int{"PlanResourceChange": 0, "ValidateResourceConfig": 0})
	if b := h.state.Get("b"); !b.Gone || b.DriftedAt == nil {
		t.Errorf("b has no mark after its read: %+v", b)
	}
	if e := h.state.Get("e"); e.Revision != 2 {
		t.Errorf("e was not read and has revision %d", e.Revision)
	}

	// The records that were read are the newest now: the next run reads e, which
	// is the only record left that is past its age.
	h.fake.resetCalls()
	if rep = run(4); !reflect.DeepEqual(rep.Read, []string{"e"}) {
		t.Errorf("second run read %v, want [e]", rep.Read)
	}

	// Without a limit every record that is due is read.
	for _, addr := range []string{"a", "b", "c", "d", "e"} {
		age(addr, time.Hour)
	}
	rep = run(0)
	if want := []string{"a", "b", "c", "d", "e"}; !reflect.DeepEqual(rep.Read, want) {
		t.Errorf("a run without a limit read %v, want %v", rep.Read, want)
	}
}
