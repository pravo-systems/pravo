package state

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"

	"pravo.dev/pravo/internal/fakes3"
)

func newFakeS3(t *testing.T) (*fakes3.Server, *S3Backend) {
	t.Helper()
	f := fakes3.New()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	b, err := NewS3Backend(context.Background(), S3Config{
		Bucket: "state", Prefix: "team/app", Region: "us-east-1", Endpoint: srv.URL, PathStyle: true,
		AccessKey: "test", SecretKey: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, b
}

func TestS3BackendStore(t *testing.T) {
	f, b := newFakeS3(t)
	ctx := context.Background()
	s, err := OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Objects["team/app/meta.json"]; !ok {
		t.Fatal("meta.json not written under the prefix")
	}
	txn := s.Begin("apply", "test")
	r := &Resource{Mode: "managed", Type: "random_pet", Name: "a", Attributes: json.RawMessage(`{"id":"x"}`), Dependencies: []string{"random_pet.b"}}
	if err := s.Set(r, txn.ID()); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(&Resource{Mode: "managed", Type: "random_pet", Name: "b", Attributes: json.RawMessage(`{}`)}, txn.ID()); err != nil {
		t.Fatal(err)
	}
	if err := txn.Finish("applied", "ok", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOutputs(map[string]json.RawMessage{"o": json.RawMessage(`1`)}); err != nil {
		t.Fatal(err)
	}

	// Reopen from the bucket: everything must come back.
	s2, err := OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Lineage() != s.Lineage() || s2.Len() != 2 || string(s2.Outputs()["o"]) != "1" {
		t.Fatalf("reopen lost data: lineage %s/%s, %d nodes, outputs %v", s2.Lineage(), s.Lineage(), s2.Len(), s2.Outputs())
	}
	if deps := s2.Dependents("random_pet.b"); len(deps) != 1 {
		t.Errorf("dependents = %v", deps)
	}
	txns, _ := s2.Transactions()
	if len(txns) != 1 || txns[0].Kind != "apply" {
		t.Errorf("transactions = %+v", txns)
	}
	if ok, _ := s2.Remove("random_pet.a"); !ok {
		t.Error("remove")
	}
	if _, ok := f.Objects["team/app/resources/random_pet.a.json"]; ok {
		t.Error("object not deleted from the bucket")
	}
}

func TestS3Locks(t *testing.T) {
	_, b := newFakeS3(t)
	ctx := context.Background()
	s1, err := OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := OpenBackend(ctx, b) // a second process, possibly on another host
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Lock("a.b", "apply one", "t1"); err != nil {
		t.Fatal(err)
	}
	err = s2.Lock("a.b", "apply two", "t2")
	var locked *ErrLocked
	if err == nil || !asLocked(err, &locked) || locked.Lock.Command != "apply one" {
		t.Fatalf("second holder should be rejected, got %v", err)
	}
	if err := s1.Renew("a.b"); err != nil {
		t.Errorf("renew by holder: %v", err)
	}
	if err := s2.Renew("a.b"); err == nil {
		t.Error("renew by non-holder must fail")
	}
	if err := s2.Unlock("a.b"); err == nil {
		t.Error("unlock by non-holder must fail")
	}
	if err := s1.Unlock("a.b"); err != nil {
		t.Errorf("unlock by holder: %v", err)
	}
	if err := s2.Lock("a.b", "apply two", "t2"); err != nil {
		t.Errorf("lock after release: %v", err)
	}
	locks, _ := s2.Locks()
	if len(locks) != 1 || locks[0].Command != "apply two" {
		t.Errorf("locks = %+v", locks)
	}
	// A lock that has not been renewed for StaleAfter is reclaimed.
	l, _ := s2.readLock("a.b")
	l.RenewedAt = l.RenewedAt.Add(-2 * StaleAfter)
	data, _ := json.Marshal(l)
	_ = b.Put(ctx, lockKey("a.b"), data)
	if err := s1.Lock("a.b", "apply one", "t3"); err != nil {
		t.Errorf("stale lock not reclaimed: %v", err)
	}
}

func asLocked(err error, target **ErrLocked) bool {
	e, ok := err.(*ErrLocked)
	if ok {
		*target = e
	}
	return ok
}

func TestCopyBetweenBackends(t *testing.T) {
	_, remote := newFakeS3(t)
	ctx := context.Background()
	local, err := NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := OpenBackend(ctx, local)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Set(&Resource{Mode: "managed", Type: "t", Name: "n", Attributes: json.RawMessage(`{}`)}, "")
	_ = s.SetOutputs(map[string]json.RawMessage{"o": json.RawMessage(`true`)})
	n, err := Copy(ctx, local, remote)
	if err != nil || n != 3 {
		t.Fatalf("copy: %d %v", n, err)
	}
	rs, err := OpenBackend(ctx, remote)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Lineage() != s.Lineage() || rs.Len() != 1 || string(rs.Outputs()["o"]) != "true" {
		t.Errorf("remote copy differs: %s %d %v", rs.Lineage(), rs.Len(), rs.Outputs())
	}
}

// The lock on the outputs and the writes outside the store's mutex work over a
// bucket as they do over a directory: commands that finish together keep each
// other's outputs, and the lock object is gone afterwards.
func TestS3UpdateOutputsKeepsBothCommandsChanges(t *testing.T) {
	f, b := newFakeS3(t)
	ctx := context.Background()
	const commands, rounds = 3, 8
	stores := make([]*Store, commands)
	for k := range stores {
		s, err := OpenBackend(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		stores[k] = s
	}
	for round := 0; round < rounds; round++ {
		var wg sync.WaitGroup
		for k, s := range stores {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := s.UpdateOutputs(func(recorded map[string]json.RawMessage, present bool) (map[string]json.RawMessage, bool, error) {
					out := map[string]json.RawMessage{}
					for name, v := range recorded {
						out[name] = v
					}
					out[fmt.Sprintf("o%d", k)] = json.RawMessage(fmt.Sprintf("%d", round))
					return out, true, nil
				})
				if err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		got, _, err := stores[0].LoadOutputs()
		if err != nil {
			t.Fatal(err)
		}
		for k := 0; k < commands; k++ {
			if want := fmt.Sprintf("%d", round); string(got[fmt.Sprintf("o%d", k)]) != want {
				t.Fatalf("round %d: o%d = %s, want %s", round, k, got[fmt.Sprintf("o%d", k)], want)
			}
		}
	}
	if _, ok := f.Objects["team/app/outputs.lock"]; ok {
		t.Error("the lock on the outputs is left in the bucket")
	}
}

// The calls of a store do not end with the context it was opened with. A command
// that is interrupted cancels that context, and still has to write the records of
// the changes that finished, finish its transaction and release its locks; the S3
// client refuses every call made with a context that is done.
func TestStoreCallsOutliveTheCancelledOpeningContext(t *testing.T) {
	_, b := newFakeS3(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := OpenBackend(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	txn := s.Begin("apply", "test")
	if err := s.Lock("random_pet.a", "test", txn.ID()); err != nil {
		t.Fatal(err)
	}
	if err := txn.Start(); err != nil {
		t.Fatal(err)
	}

	cancel() // the first signal

	r := &Resource{Mode: "managed", Type: "random_pet", Name: "a", Attributes: json.RawMessage(`{"id":"x"}`)}
	if err := s.Set(r, txn.ID()); err != nil {
		t.Errorf("Set after the context was cancelled: %v", err)
	}
	if _, _, _, err := s.Invalidate("random_pet.a", txn.ID()); err != nil {
		t.Errorf("Invalidate after the context was cancelled: %v", err)
	}
	if err := s.Renew("random_pet.a"); err != nil {
		t.Errorf("Renew after the context was cancelled: %v", err)
	}
	err = s.UpdateOutputs(func(map[string]json.RawMessage, bool) (map[string]json.RawMessage, bool, error) {
		return map[string]json.RawMessage{"o": json.RawMessage(`1`)}, true, nil
	})
	if err != nil {
		t.Errorf("UpdateOutputs after the context was cancelled: %v", err)
	}
	if err := txn.Finish("failed", "interrupted", true); err != nil {
		t.Errorf("Finish after the context was cancelled: %v", err)
	}
	if err := s.Unlock("random_pet.a"); err != nil {
		t.Errorf("Unlock after the context was cancelled: %v", err)
	}

	again, err := OpenBackend(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if again.Get("random_pet.a") == nil {
		t.Error("the record written after the cancellation is not in the bucket")
	}
	if locks, err := again.Locks(); err != nil || len(locks) != 0 {
		t.Errorf("locks left after Unlock: %v (err %v)", locks, err)
	}
	if string(again.Outputs()["o"]) != "1" {
		t.Errorf("outputs = %v", again.Outputs())
	}
	if txns, _ := again.Transactions(); len(txns) != 1 || txns[0].Status != "failed" {
		t.Errorf("transactions = %+v, want the one transaction, failed", txns)
	}
	if ok, err := again.Remove("random_pet.a"); err != nil || !ok {
		t.Errorf("Remove: %v, %v", ok, err)
	}

	// The opening itself still stops with its context.
	done, stop := context.WithCancel(context.Background())
	stop()
	if _, err := OpenBackend(done, b); err == nil {
		t.Error("a store was opened with a context that is done")
	}
}
