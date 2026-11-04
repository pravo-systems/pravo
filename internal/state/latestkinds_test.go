package state

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// getCounter counts the objects read from txn/.
type getCounter struct {
	Backend
	txnGets atomic.Int64
}

func (c *getCounter) Get(ctx context.Context, key string) ([]byte, error) {
	if strings.HasPrefix(key, "txn/") {
		c.txnGets.Add(1)
	}
	return c.Backend.Get(ctx, key)
}

func runTxn(t *testing.T, s *Store, kind, status string) string {
	t.Helper()
	tx := s.Begin(kind, kind)
	if err := tx.Start(); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		if err := tx.Finish(status, "", true); err != nil {
			t.Fatal(err)
		}
	}
	return tx.ID()
}

// The look for the newest apply or destroy does not read the plans, edits and secret sets
// that came after it, and answers as the scan of every object does.
func TestLatestOfKindsSkipsHintedTransactions(t *testing.T) {
	for name, tc := range map[string]struct {
		after    []string // kinds of status applied that follow the destroy
		status   string   // status of the destroy
		wantKind string
		wantOK   string
	}{
		"destroy then tail":       {nil, "applied", "destroy", "applied"},
		"failed destroy":          {nil, "failed", "destroy", "failed"},
		"apply after the destroy": {[]string{"apply"}, "applied", "apply", "applied"},
	} {
		t.Run(name, func(t *testing.T) {
			cb := &getCounter{Backend: &LocalBackend{Dir: filepath.Join(t.TempDir(), "pravo.state")}}
			s, err := OpenBackend(context.Background(), cb)
			if err != nil {
				t.Fatal(err)
			}
			runTxn(t, s, "apply", "applied")
			runTxn(t, s, "destroy", tc.status)
			for _, k := range tc.after {
				runTxn(t, s, k, "applied")
			}
			for i := range 300 {
				runTxn(t, s, []string{"plan", "edit", "secret"}[i%3], "applied")
			}
			cb.txnGets.Store(0)
			got, err := s.LatestOfKinds("apply", "destroy")
			if err != nil || got == nil {
				t.Fatalf("LatestOfKinds = %v, %v", got, err)
			}
			if got.Kind != tc.wantKind || got.Status != tc.wantOK {
				t.Errorf("got %s %s, want %s %s", got.Kind, got.Status, tc.wantKind, tc.wantOK)
			}
			if n := cb.txnGets.Load(); n > 3 {
				t.Errorf("read %d transaction objects after 300 other transactions, want at most 3", n)
			}
		})
	}
}

// A state whose transactions have no hints (made by an earlier pravo) gets the same
// answer by reading them.
func TestLatestOfKindsWithoutHints(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pravo.state")
	s, err := OpenBackend(context.Background(), &LocalBackend{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	runTxn(t, s, "apply", "applied")
	runTxn(t, s, "destroy", "applied")
	runTxn(t, s, "plan", "applied")
	keys, _ := s.b.List(context.Background(), KindDir)
	if len(keys) != 3 {
		t.Fatalf("hints = %v, want 3", keys)
	}
	for _, k := range keys {
		if err := s.b.Delete(context.Background(), k); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LatestOfKinds("apply", "destroy")
	if err != nil || got == nil || got.Kind != "destroy" {
		t.Fatalf("LatestOfKinds = %v, %v", got, err)
	}
}

// The facts of one kind are read without the transactions of the others.
func TestSignedFactsOfSkipsHintedTransactions(t *testing.T) {
	cb := &getCounter{Backend: &LocalBackend{Dir: filepath.Join(t.TempDir(), "pravo.state")}}
	s, err := OpenBackend(context.Background(), cb)
	if err != nil {
		t.Fatal(err)
	}
	runTxn(t, s, "environment", "applied")
	for range 50 {
		runTxn(t, s, "secret", "applied")
	}
	cb.txnGets.Store(0)
	facts, err := s.signedFactsOf(context.Background(), nil, "environment")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || cb.txnGets.Load() != 1 {
		t.Errorf("facts = %d, reads = %d, want 1 and 1", len(facts), cb.txnGets.Load())
	}
	cb.txnGets.Store(0)
	if facts, _ = s.signedFacts(context.Background(), nil); len(facts) != 51 || cb.txnGets.Load() != 51 {
		t.Errorf("all facts = %d, reads = %d, want 51 and 51", len(facts), cb.txnGets.Load())
	}
}
