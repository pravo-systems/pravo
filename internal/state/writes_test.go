package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// gateBackend lets a test hold up or fail the calls of a Backend.
type gateBackend struct {
	Backend

	mu sync.Mutex
	// before, when set, runs before every Put, Delete and Get; an error it
	// returns is the error of the call.
	putGate    func(key string) error
	deleteGate func(key string) error
	getGate    func(key string) ([]byte, bool)
}

func (g *gateBackend) Put(ctx context.Context, key string, data []byte) error {
	g.mu.Lock()
	gate := g.putGate
	g.mu.Unlock()
	if gate != nil {
		if err := gate(key); err != nil {
			return err
		}
	}
	return g.Backend.Put(ctx, key, data)
}

func (g *gateBackend) Delete(ctx context.Context, key string) error {
	g.mu.Lock()
	gate := g.deleteGate
	g.mu.Unlock()
	if gate != nil {
		if err := gate(key); err != nil {
			return err
		}
	}
	return g.Backend.Delete(ctx, key)
}

func (g *gateBackend) Get(ctx context.Context, key string) ([]byte, error) {
	g.mu.Lock()
	gate := g.getGate
	g.mu.Unlock()
	if gate != nil {
		if data, ok := gate(key); ok {
			return data, nil
		}
	}
	return g.Backend.Get(ctx, key)
}

func (g *gateBackend) set(f func(g *gateBackend)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f(g)
}

func openGated(t *testing.T) (*Store, *gateBackend) {
	t.Helper()
	lb, err := NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := &gateBackend{Backend: lb}
	s, err := OpenBackend(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	return s, g
}

func node(name string, deps ...string) *Resource {
	return &Resource{Mode: "managed", Type: "t", Name: name, Attributes: json.RawMessage(`{}`), Dependencies: deps}
}

// A write that waits for its backend does not hold up a read of the graph, or a
// write of another node.
func TestStoreWritesDoNotBlockReaders(t *testing.T) {
	s, g := openGated(t)
	if err := s.Set(node("a"), ""); err != nil {
		t.Fatal(err)
	}
	// Another node whose writes are not ordered with those of b.
	other := "c"
	for i := 0; s.stripe("t."+other) == s.stripe("t.b"); i++ {
		other = fmt.Sprintf("c%d", i)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	g.set(func(g *gateBackend) {
		g.putGate = func(key string) error {
			if key == nodeKey("t.b") {
				entered <- struct{}{}
				<-release
			}
			return nil
		}
	})
	done := make(chan error, 1)
	go func() { done <- s.Set(node("b", "t.a"), "") }()
	<-entered // the write is at the backend

	read := make(chan struct{})
	go func() {
		defer close(read)
		if s.Get("t.a") == nil || s.Len() != 1 || len(s.List()) != 1 || len(s.Dependents("t.a")) != 0 || len(s.Reach("t.a")) != 0 {
			t.Error("the graph is not as it was before the write")
		}
		_ = s.Outputs()
		_ = s.Edges()
		_ = s.Snapshot()
	}()
	select {
	case <-read:
	case <-time.After(5 * time.Second):
		t.Fatal("a read of the graph waits for a write")
	}
	written := make(chan error, 1)
	go func() { written <- s.Set(node(other), "") }()
	select {
	case err := <-written:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a write of one node waits for a write of another")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.Get("t.b") == nil || s.Get("t."+other) == nil {
		t.Error("the writes did not publish their nodes")
	}
}

// Two writers of one node are ordered: every write gets the next revision.
func TestSameAddressWritesStayOrdered(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const writers, each = 2, 500
	var (
		wg   sync.WaitGroup
		seen = make([][]int, writers)
	)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				r := node("a")
				if err := s.Set(r, ""); err != nil {
					t.Error(err)
					return
				}
				seen[w] = append(seen[w], r.Revision)
			}
		}()
	}
	wg.Wait()
	all := map[int]bool{}
	for w, revs := range seen {
		for i, rev := range revs {
			if i > 0 && rev <= revs[i-1] {
				t.Fatalf("writer %d: revision %d after %d", w, rev, revs[i-1])
			}
			all[rev] = true
		}
	}
	if len(all) != writers*each {
		t.Errorf("%d distinct revisions for %d writes", len(all), writers*each)
	}
	if got := s.Get("t.a").Revision; got != writers*each {
		t.Errorf("the node is at revision %d, want %d", got, writers*each)
	}
	again, err := Open(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Get("t.a").Revision; got != writers*each {
		t.Errorf("the backend holds revision %d, want %d", got, writers*each)
	}
}

// A write that fails changes nothing: not the node the store holds, not the node
// that was passed in, and the next write continues from the last revision that
// was written.
func TestFailedPutChangesNothingInMemory(t *testing.T) {
	s, g := openGated(t)
	boom := errors.New("backend down")
	a := node("a")
	if err := s.Set(a, "txn1"); err != nil {
		t.Fatal(err)
	}
	g.set(func(g *gateBackend) { g.putGate = func(string) error { return boom } })

	t.Run("Set", func(t *testing.T) {
		next := node("a")
		next.Attributes = json.RawMessage(`{"x":1}`)
		if err := s.Set(next, "txn2"); !errors.Is(err, boom) {
			t.Fatalf("Set = %v", err)
		}
		if got := s.Get("t.a"); got != a || got.Revision != 1 || got.Txn != "txn1" || string(got.Attributes) != `{}` {
			t.Errorf("the node is %+v", got)
		}
		if next.Revision != 0 || next.Txn != "" || !next.UpdatedAt.IsZero() {
			t.Errorf("the node that was passed in is %+v", next)
		}
	})

	t.Run("Set of the node the store holds", func(t *testing.T) {
		held := s.Get("t.a")
		held.Attributes = json.RawMessage(`{"y":2}`)
		if err := s.Set(held, "txn2"); !errors.Is(err, boom) {
			t.Fatalf("Set = %v", err)
		}
		if held.Revision != 1 || held.Txn != "txn1" {
			t.Errorf("the node is %+v", held)
		}
		held.Attributes = json.RawMessage(`{}`)
	})

	t.Run("Refresh", func(t *testing.T) {
		same := node("a")
		if err := s.Refresh(same, "txn2"); !errors.Is(err, boom) {
			t.Fatalf("Refresh = %v", err)
		}
		if got := s.Get("t.a"); got != a || got.Txn != "txn1" || same.Revision != 0 {
			t.Errorf("the node is %+v, the one passed in %+v", got, same)
		}
	})

	t.Run("Remove", func(t *testing.T) {
		g.set(func(g *gateBackend) { g.deleteGate = func(string) error { return boom } })
		if ok, err := s.Remove("t.a"); !ok || !errors.Is(err, boom) {
			t.Fatalf("Remove = %v, %v", ok, err)
		}
		if s.Get("t.a") == nil {
			t.Error("the node is gone from the store, and still in the backend")
		}
		g.set(func(g *gateBackend) { g.deleteGate = nil })
	})

	g.set(func(g *gateBackend) { g.putGate = nil })
	next := node("a")
	if err := s.Set(next, "txn3"); err != nil {
		t.Fatal(err)
	}
	if next.Revision != 2 {
		t.Errorf("the write after the failures is revision %d, want 2", next.Revision)
	}
	again, err := Open(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Get("t.a"); got == nil || got.Revision != 2 || got.Txn != "txn3" {
		t.Errorf("the backend holds %+v", got)
	}
}

// UpdateOutputs reads the outputs under their lock, so that two commands which
// finish together each keep what the other recorded.
func TestUpdateOutputsKeepsBothApplysChanges(t *testing.T) {
	dir := t.TempDir()
	const commands, rounds = 4, 15
	stores := make([]*Store, commands)
	for k := range stores {
		s, err := Open(dir)
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
				t.Fatalf("round %d: o%d = %s, want %s; outputs %v", round, k, got[fmt.Sprintf("o%d", k)], want, got)
			}
		}
	}
	if _, err := stores[0].b.Get(context.Background(), outputsLockKey); !errors.Is(err, ErrNotFound) {
		t.Errorf("the lock on the outputs is left: %v", err)
	}
}

func TestUpdateOutputs(t *testing.T) {
	setup := func(t *testing.T) *Store {
		t.Helper()
		s, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	put := func(t *testing.T, s *Store, l Lock) {
		t.Helper()
		data, _ := json.Marshal(l)
		if err := s.b.Put(context.Background(), outputsLockKey, data); err != nil {
			t.Fatal(err)
		}
	}
	record := func(s *Store) error {
		return s.UpdateOutputs(func(map[string]json.RawMessage, bool) (map[string]json.RawMessage, bool, error) {
			return map[string]json.RawMessage{"o": json.RawMessage(`1`)}, true, nil
		})
	}

	t.Run("says whether the object exists and writes only on request", func(t *testing.T) {
		s := setup(t)
		err := s.UpdateOutputs(func(recorded map[string]json.RawMessage, present bool) (map[string]json.RawMessage, bool, error) {
			if present || len(recorded) != 0 {
				t.Errorf("a new state: %v, present %v", recorded, present)
			}
			return nil, false, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.b.Get(context.Background(), "outputs.json"); !errors.Is(err, ErrNotFound) {
			t.Errorf("outputs.json was written: %v", err)
		}
		if err := record(s); err != nil {
			t.Fatal(err)
		}
		if got := s.Outputs(); string(got["o"]) != `1` {
			t.Errorf("outputs %v", got)
		}
	})

	t.Run("an error writes nothing and releases the lock", func(t *testing.T) {
		s := setup(t)
		boom := errors.New("boom")
		err := s.UpdateOutputs(func(map[string]json.RawMessage, bool) (map[string]json.RawMessage, bool, error) {
			return map[string]json.RawMessage{"o": json.RawMessage(`1`)}, true, boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("UpdateOutputs = %v", err)
		}
		if _, err := s.b.Get(context.Background(), "outputs.json"); !errors.Is(err, ErrNotFound) {
			t.Errorf("outputs.json was written: %v", err)
		}
		if err := record(s); err != nil {
			t.Errorf("the lock was not released: %v", err)
		}
	})

	t.Run("a lock of a process that is gone is reclaimed", func(t *testing.T) {
		s := setup(t)
		host, _ := os.Hostname()
		put(t, s, Lock{Addr: "outputs.json", PID: 999999, Host: host, Nonce: "gone", Since: time.Now()})
		if err := record(s); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a lock that was held too long is reclaimed", func(t *testing.T) {
		s := setup(t)
		put(t, s, Lock{Addr: "outputs.json", PID: os.Getpid(), Host: "elsewhere", Nonce: "old", Since: time.Now().Add(-time.Hour)})
		if err := record(s); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a lock that is held is waited for, then reported", func(t *testing.T) {
		s := setup(t)
		wait := outputsLockWait
		outputsLockWait = 150 * time.Millisecond
		defer func() { outputsLockWait = wait }()
		put(t, s, Lock{Addr: "outputs.json", PID: os.Getpid(), Host: "elsewhere", Command: "pravo apply", Nonce: "live", Since: time.Now()})
		start := time.Now()
		err := record(s)
		var locked *ErrLocked
		if !errors.As(err, &locked) || !strings.Contains(err.Error(), "outputs.json is locked by pravo apply") {
			t.Fatalf("UpdateOutputs = %v", err)
		}
		if took := time.Since(start); took < 100*time.Millisecond || took > 2*time.Second {
			t.Errorf("the update waited %s", took)
		}
		if _, err := s.b.Get(context.Background(), outputsLockKey); err != nil {
			t.Errorf("the lock of the other command was removed: %v", err)
		}
	})

	t.Run("a lock that is released meanwhile is taken", func(t *testing.T) {
		s := setup(t)
		put(t, s, Lock{Addr: "outputs.json", PID: os.Getpid(), Host: "elsewhere", Nonce: "live", Since: time.Now()})
		go func() {
			time.Sleep(50 * time.Millisecond)
			_ = s.b.Delete(context.Background(), outputsLockKey)
		}()
		if err := record(s); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a lock that cannot be read is reclaimed", func(t *testing.T) {
		s := setup(t)
		if err := s.b.Put(context.Background(), outputsLockKey, []byte("{")); err != nil {
			t.Fatal(err)
		}
		if err := record(s); err != nil {
			t.Fatal(err)
		}
	})
}

// A lock that is being created is not taken for a broken one: Lock reads it
// again, finds the holder, and leaves the lock alone.
func TestLockReadsAgainALockThatIsBeingCreated(t *testing.T) {
	s, g := openGated(t)
	other, err := Open(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Lock("a", "pravo apply", "t1"); err != nil {
		t.Fatal(err)
	}
	reads := 0
	g.set(func(g *gateBackend) {
		g.getGate = func(key string) ([]byte, bool) {
			if key != lockKey("a") {
				return nil, false
			}
			reads++
			if reads <= 2 {
				return nil, true // the object exists and has no contents yet
			}
			return nil, false
		}
	})
	err = s.Lock("a", "pravo apply", "t2")
	var locked *ErrLocked
	if !errors.As(err, &locked) || locked.Lock.Txn != "t1" {
		t.Fatalf("Lock = %v; want the lock of the other command", err)
	}
	if _, err := other.readLock("a"); err != nil {
		t.Errorf("the lock of the other command was deleted: %v", err)
	}
}

// Conflict says what Lock would say, and takes nothing.
func TestConflictReportsWhatLockWouldReport(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Conflict("x"); err != nil {
		t.Errorf("a free node: %v", err)
	}
	if err := a.Lock("x", "pravo apply", "t1"); err != nil {
		t.Fatal(err)
	}
	if err := a.Conflict("x"); err != nil {
		t.Errorf("a node this store holds: %v", err)
	}
	var locked *ErrLocked
	if err := b.Conflict("x"); !errors.As(err, &locked) || locked.Lock.Txn != "t1" {
		t.Errorf("a node another store holds: %v", err)
	}
	if err := b.Conflict("y"); err != nil {
		t.Errorf("a free node: %v", err)
	}
	if _, err := b.readLock("y"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Conflict took the lock on y: %v", err)
	}
}

// A transaction that was started and not finished is listed as running.
func TestTransactionStartLeavesItRunning(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	txn := s.Begin("apply", "pravo apply")
	if txns, _ := s.Transactions(); len(txns) != 0 {
		t.Fatalf("a transaction that was begun is listed: %v", txns)
	}
	if err := txn.Start(); err != nil {
		t.Fatal(err)
	}
	txns, _ := s.Transactions()
	if len(txns) != 1 || txns[0].Status != "running" || txns[0].Finished != nil || txns[0].Command != "pravo apply" || len(txns[0].Changes) != 0 {
		t.Fatalf("transactions: %+v", txns)
	}
	txn.Record("t.a", "create", 0, 1)
	if err := txn.Finish("applied", "1 added", true); err != nil {
		t.Fatal(err)
	}
	txns, _ = s.Transactions()
	if len(txns) != 1 || txns[0].Status != "applied" || txns[0].Finished == nil || len(txns[0].Changes) != 1 {
		t.Fatalf("transactions: %+v", txns)
	}
}
