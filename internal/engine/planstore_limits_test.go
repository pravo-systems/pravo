package engine

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// countingBackend counts the reads of each key.
type countingBackend struct {
	state.Backend
	reads map[string]*atomic.Int32
}

func (c *countingBackend) Get(ctx context.Context, key string) ([]byte, error) {
	if n := c.reads[key]; n != nil {
		n.Add(1)
	}
	return c.Backend.Get(ctx, key)
}

// An object of a stored plan that is larger than what is read of it is refused
// with ErrTooLarge naming the plan and the size: the listing skips the plan and
// reads the others, and the plan itself is refused as well.
func TestLargeObjectsOfAStoredPlanAreRefused(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		good := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		bigMeta := putPlan(t, ps, t0.Add(time.Hour), "bob", 0, change("b", ActionCreate, 0))
		bigProgress := putPlan(t, ps, t0.Add(2*time.Hour), "carol", 0, change("c", ActionCreate, 0))
		bigPlan := putPlan(t, ps, t0.Add(3*time.Hour), "dave", 0, change("d", ActionCreate, 0))

		// A hostile summary of 20 MiB, which is JSON that decodes.
		hostile := `{"id":"` + bigMeta.ID + `","author":"` + strings.Repeat("x", 20<<20) + `"}`
		if err := ps.B.Put(ctx, metaKey2(bigMeta.ID), []byte(hostile)); err != nil {
			t.Fatal(err)
		}
		if err := ps.B.Put(ctx, progressKey(bigProgress.ID), []byte(`{"id":"`+bigProgress.ID+`","applied":{"`+strings.Repeat("y", 2<<20)+`":"t"}}`)); err != nil {
			t.Fatal(err)
		}
		es, skipped, err := ps.EntriesAndSkipped(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(es) != 2 || es[len(es)-1].Meta.ID != good.ID {
			t.Fatalf("listed %d entries, want the good plan and the one with a large plan object", len(es))
		}
		got := map[string]error{}
		for _, s := range skipped {
			got[s.ID] = s.Err
		}
		for _, id := range []string{bigMeta.ID, bigProgress.ID} {
			if err := got[id]; !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), id) || !strings.Contains(err.Error(), "bytes") {
				t.Errorf("%s: skipped with %v, want ErrTooLarge naming the plan and the size", id, err)
			}
		}
		if len(skipped) != 2 {
			t.Errorf("skipped %d plans, want 2", len(skipped))
		}
		if _, err := ps.Meta(ctx, bigMeta.ID); !errors.Is(err, ErrTooLarge) {
			t.Errorf("Meta of the large summary: %v", err)
		}
		if _, err := ps.Progress(ctx, bigProgress.ID); !errors.Is(err, ErrTooLarge) {
			t.Errorf("Progress of the large progress: %v", err)
		}
		// A plan object over the limit is refused by ReadFile. The limit is too large for a test to write, so the
		// check is on the helper that ReadFile uses.
		if err := tooLarge("plan", bigPlan.ID, MaxPlanFileBytes+1, MaxPlanFileBytes); !errors.Is(err, ErrTooLarge) {
			t.Errorf("tooLarge: %v", err)
		}
		if _, _, err := ps.ReadFile(ctx, good.ID); err != nil {
			t.Errorf("ReadFile of a plan within the limit: %v", err)
		}
	})
}

// A cache keeps the summaries that a listing read, so that the next listing reads
// only the progress; it keeps a refusal, forgets the plans that are gone, and
// does not keep a read that failed.
func TestPlanMetaCacheSavesTheReadOfImmutableSummaries(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		a := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		b := putPlan(t, ps, t0.Add(time.Hour), "bob", 0, change("b", ActionCreate, 0))
		hostile := `{"id":"` + b.ID + `","author":"` + strings.Repeat("x", 9<<20) + `"}`
		if err := ps.B.Put(ctx, metaKey2(b.ID), []byte(hostile)); err != nil {
			t.Fatal(err)
		}
		cb := &countingBackend{Backend: ps.B, reads: map[string]*atomic.Int32{}}
		for _, id := range []string{a.ID, b.ID} {
			cb.reads[metaKey2(id)] = new(atomic.Int32)
			cb.reads[progressKey(id)] = new(atomic.Int32)
		}
		ps.B = cb
		ps.Cache = NewPlanMetaCache()
		if err := ps.MarkApplied(ctx, a.ID, nil, "txn-1", []string{"a"}); err != nil {
			t.Fatal(err)
		}
		for _, n := range cb.reads {
			n.Store(0) // what MarkApplied read does not count
		}
		for i := 0; i < 4; i++ {
			es, skipped, err := ps.EntriesAndSkipped(ctx, nil)
			if err != nil || len(es) != 1 || len(skipped) != 1 || !errors.Is(skipped[0].Err, ErrTooLarge) {
				t.Fatalf("listing %d: %d entries, skipped %v, %v", i, len(es), skipped, err)
			}
		}
		for _, id := range []string{a.ID, b.ID} {
			if n := cb.reads[metaKey2(id)].Load(); n != 1 {
				t.Errorf("the summary of %s was read %d times in four listings, want once", id, n)
			}
		}
		if n := cb.reads[progressKey(a.ID)].Load(); n != 4 {
			t.Errorf("the progress of %s was read %d times in four listings, want each time", a.ID, n)
		}
		if ps.Cache.Len() != 2 {
			t.Errorf("the cache holds %d summaries", ps.Cache.Len())
		}
		// A plan that is gone is forgotten.
		for _, k := range []string{planKey(b.ID), metaKey2(b.ID), progressKey(b.ID)} {
			ps.B.Delete(ctx, k)
		}
		if _, _, err := ps.EntriesAndSkipped(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if ps.Cache.Len() != 1 {
			t.Errorf("after the plan was collected the cache holds %d summaries, want 1", ps.Cache.Len())
		}
	})
}

// A claim that lapses with the clock alone is found by the listing: the plan that a
// collector hides shows again at the end of the claim, and a claim of an apply on a
// listed plan ends after a day.
func TestListingSaysWhenClaimsLapse(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		a := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		b := putPlan(t, ps, t0, "bob", 0, change("b", ActionCreate, 0))
		l, err := ps.List(ctx, nil)
		if err != nil || !l.NextLapse().IsZero() {
			t.Fatalf("no claim: %v %v", l.NextLapse(), err)
		}
		*clock = t0.Add(time.Hour)
		if err := ps.updateProgress(ctx, a.ID, func(p *PlanProgress) error {
			p.Collecting = &PlanClaim{By: "x", At: *clock}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := ps.updateProgress(ctx, b.ID, func(p *PlanProgress) error {
			p.Applying = []PlanClaim{{By: "y", At: t0.Add(30 * time.Minute)}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		l, err = ps.List(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(l.Hidden) != 1 || l.Hidden[0].ID != a.ID || !l.Hidden[0].Until.Equal(t0.Add(time.Hour+claimTimeout)) {
			t.Fatalf("hidden %+v", l.Hidden)
		}
		if len(l.Entries) != 1 || !l.Entries[0].Progress.ApplyRunning(*clock) {
			t.Fatalf("entries %d", len(l.Entries))
		}
		if got, want := l.NextLapse(), t0.Add(time.Hour+claimTimeout); !got.Equal(want) {
			t.Errorf("next lapse %v, want %v (the collector's claim, before the apply's day)", got, want)
		}
		*clock = t0.Add(2 * time.Hour)
		l, _ = ps.List(ctx, nil)
		if len(l.Hidden) != 0 || len(l.Entries) != 2 {
			t.Fatalf("after the claim lapsed: hidden %d entries %d", len(l.Hidden), len(l.Entries))
		}
		if got, want := l.NextLapse(), t0.Add(30*time.Minute+applyingKeep); !got.Equal(want) {
			t.Errorf("next lapse %v, want %v (the apply's claim)", got, want)
		}
	})
}
