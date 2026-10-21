package statetest

import (
	"context"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

func TestCountingCountsByOperationAndPrefix(t *testing.T) {
	ctx := context.Background()
	lb, err := state.NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := NewCounting(lb)
	if err := c.Put(ctx, "resources/a.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutIfAbsent(ctx, "locks/a.lock", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, "resources/a.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.List(ctx, "resources"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, "locks/a.lock"); err != nil {
		t.Fatal(err)
	}
	for _, w := range []struct {
		op, prefix string
		n          int
	}{
		{"Put", "", 1}, {"Put", "resources", 1}, {"Put", "locks", 0},
		{"PutIfAbsent", "locks", 1}, {"Delete", "locks", 1}, {"Get", "resources", 1}, {"List", "resources", 1},
	} {
		var got int
		if w.prefix == "" {
			got = c.Count(w.op)
		} else {
			got = c.Count(w.op, w.prefix)
		}
		if got != w.n {
			t.Errorf("%s %s: %d calls, want %d", w.op, w.prefix, got, w.n)
		}
	}
	if got := c.Writes(); got != 3 {
		t.Errorf("%d writes, want 3", got)
	}
	c.Reset()
	if got := c.Writes() + c.Count("Get") + c.Count("List"); got != 0 {
		t.Errorf("%d calls after a reset", got)
	}
}

// A conditional delete reads the object and deletes it, and a conditional
// replace reads it and writes it: the counts and the time say so.
func TestConditionalCallsCountAndWaitAsTwoCalls(t *testing.T) {
	ctx := context.Background()
	lb, err := state.NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := NewCounting(lb)
	if _, err := c.PutIfAbsent(ctx, "locks/a.lock", []byte("one")); err != nil {
		t.Fatal(err)
	}
	c.Reset()
	if ok, err := c.ReplaceIf(ctx, "locks/a.lock", func(cur []byte) ([]byte, bool) { return append(cur, '2'), true }); !ok || err != nil {
		t.Fatalf("ReplaceIf: %v, %v", ok, err)
	}
	if ok, err := c.DeleteIf(ctx, "locks/a.lock", func(cur []byte) bool { return string(cur) == "one2" }); !ok || err != nil {
		t.Fatalf("DeleteIf: %v, %v", ok, err)
	}
	for _, w := range []struct {
		op string
		n  int
	}{{"Get", 2}, {"Put", 1}, {"Delete", 1}, {"PutIfAbsent", 0}} {
		if got := c.Count(w.op, "locks"); got != w.n {
			t.Errorf("%s: %d calls, want %d", w.op, got, w.n)
		}
	}

	delay := 30 * time.Millisecond
	l := NewLatency(lb, delay)
	if err := lb.Put(ctx, "locks/b.lock", []byte("x")); err != nil {
		t.Fatal(err)
	}
	begun := time.Now()
	if ok, err := l.DeleteIf(ctx, "locks/b.lock", func([]byte) bool { return true }); !ok || err != nil {
		t.Fatalf("DeleteIf: %v, %v", ok, err)
	}
	if took := time.Since(begun); took < 2*delay {
		t.Errorf("a conditional delete took %s, want at least the time of two calls, %s", took, 2*delay)
	}
}
