package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testBackends returns a local backend and a bucket, each in a function that
// makes an empty one, so that a test runs against both.
func testBackends() []struct {
	name string
	make func(t *testing.T) Backend
} {
	return []struct {
		name string
		make func(t *testing.T) Backend
	}{
		{"local", func(t *testing.T) Backend {
			b, err := NewLocalBackend(filepath.Join(t.TempDir(), "pravo.state"))
			if err != nil {
				t.Fatal(err)
			}
			return b
		}},
		{"s3", func(t *testing.T) Backend {
			_, b := newFakeS3(t)
			return b
		}},
	}
}

// openMany opens n stores over one backend, as n commands do.
func openMany(t *testing.T, b Backend, n int) []*Store {
	t.Helper()
	out := make([]*Store, n)
	for i := range out {
		s, err := OpenBackend(context.Background(), b)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = s
	}
	return out
}

// writeDeadLock stores a lock that nobody holds and that may be reclaimed on
// every backend: its process is gone on this host, and it was last renewed long
// ago.
func writeDeadLock(t *testing.T, b Backend, addr string) []byte {
	t.Helper()
	host, _ := os.Hostname()
	old := time.Now().Add(-2 * StaleAfter).UTC()
	l := Lock{Addr: addr, PID: 999999, Host: host, Command: "pravo apply", Txn: "dead", Nonce: "dead", Since: old, RenewedAt: old, Kind: "lock"}
	data, _ := json.Marshal(l)
	if err := b.Put(context.Background(), lockKey(addr), data); err != nil {
		t.Fatal(err)
	}
	return data
}

// DeleteIf and ReplaceIf act on the object only while it holds what the caller
// judged, and of callers that judge the same object, one acts.
func TestConditionalDeleteAndReplace(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			is := func(want string) func([]byte) bool {
				return func(cur []byte) bool { return string(cur) == want }
			}
			if _, err := b.DeleteIf(ctx, "locks/a.lock", is("one")); !errors.Is(err, ErrNotFound) {
				t.Fatalf("DeleteIf of a missing object: %v, want ErrNotFound", err)
			}
			if _, err := b.ReplaceIf(ctx, "locks/a.lock", func([]byte) ([]byte, bool) { return []byte("x"), true }); !errors.Is(err, ErrNotFound) {
				t.Fatalf("ReplaceIf of a missing object: %v, want ErrNotFound", err)
			}
			if err := b.Put(ctx, "locks/a.lock", []byte("one")); err != nil {
				t.Fatal(err)
			}
			if ok, err := b.DeleteIf(ctx, "locks/a.lock", is("two")); ok || err != nil {
				t.Fatalf("DeleteIf of other contents: %v, %v", ok, err)
			}
			if ok, err := b.ReplaceIf(ctx, "locks/a.lock", func([]byte) ([]byte, bool) { return []byte("x"), false }); ok || err != nil {
				t.Fatalf("ReplaceIf that declines: %v, %v", ok, err)
			}
			if got, _ := b.Get(ctx, "locks/a.lock"); string(got) != "one" {
				t.Fatalf("the object is %q after two refusals", got)
			}
			if ok, err := b.ReplaceIf(ctx, "locks/a.lock", func(cur []byte) ([]byte, bool) { return append(cur, "+two"...), true }); !ok || err != nil {
				t.Fatalf("ReplaceIf: %v, %v", ok, err)
			}
			if got, _ := b.Get(ctx, "locks/a.lock"); string(got) != "one+two" {
				t.Fatalf("the object is %q after a replace", got)
			}
			if ok, err := b.DeleteIf(ctx, "locks/a.lock", is("one+two")); !ok || err != nil {
				t.Fatalf("DeleteIf: %v, %v", ok, err)
			}
			if _, err := b.Get(ctx, "locks/a.lock"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("the object is still there: %v", err)
			}

			// Callers that judge the same object: one deletes it.
			for round := 0; round < 40; round++ {
				if err := b.Put(ctx, "locks/b.lock", []byte("held")); err != nil {
					t.Fatal(err)
				}
				var (
					wg       sync.WaitGroup
					deleted  atomic.Int32
					gate     = make(chan struct{})
					judgedBy atomic.Int32
				)
				for i := 0; i < 8; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-gate
						ok, err := b.DeleteIf(ctx, "locks/b.lock", func(cur []byte) bool { judgedBy.Add(1); return string(cur) == "held" })
						if err != nil && !errors.Is(err, ErrNotFound) {
							t.Error(err)
						}
						if ok {
							deleted.Add(1)
						}
					}()
				}
				close(gate)
				wg.Wait()
				if deleted.Load() != 1 {
					t.Fatalf("round %d: %d callers deleted the object, want 1", round, deleted.Load())
				}
			}
		})
	}
}

// A lock that exists has all of its contents. A reader that looks for the lock
// while others create it never finds one that is empty or cut short, which a
// reader would take for a lock left broken by a crash.
func TestLocalLockIsCreatedWhole(t *testing.T) {
	b, err := NewLocalBackend(filepath.Join(t.TempDir(), "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const keys = 300
	body := []byte(strings.Repeat("0123456789", 40))
	var (
		wg      sync.WaitGroup
		created atomic.Int32
		done    = make(chan struct{})
	)
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() { // readers
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				for i := 0; i < keys; i++ {
					got, err := b.Get(ctx, fmt.Sprintf("locks/k%03d.lock", i))
					if errors.Is(err, ErrNotFound) {
						continue
					}
					if err != nil || string(got) != string(body) {
						t.Errorf("k%03d: read %d bytes, %v; want the %d bytes that were written", i, len(got), err, len(body))
						return
					}
				}
			}
		}()
	}
	var creators sync.WaitGroup
	for c := 0; c < 4; c++ {
		creators.Add(1)
		go func() {
			defer creators.Done()
			for i := 0; i < keys; i++ {
				ok, err := b.PutIfAbsent(ctx, fmt.Sprintf("locks/k%03d.lock", i), body)
				if err != nil {
					t.Error(err)
					return
				}
				if ok {
					created.Add(1)
				}
			}
		}()
	}
	creators.Wait()
	close(done)
	wg.Wait()
	if created.Load() != keys {
		t.Errorf("%d locks were created for %d keys: each key is created once", created.Load(), keys)
	}
	entries, _ := os.ReadDir(filepath.Join(b.Dir, "locks"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file %s left", e.Name())
		}
	}
}

// Of commands that find a lock left by a process that is gone, one takes it. The
// others see the lock of the one that did and are turned away; none of them
// deletes the lock the first has just taken.
func TestReclaimOfAStaleLockHasOneWinner(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			stores := openMany(t, b, 4)
			rounds := 150
			if testing.Short() {
				rounds = 30
			}
			for round := 0; round < rounds; round++ {
				writeDeadLock(t, b, "x")
				var (
					wg   sync.WaitGroup
					gate = make(chan struct{})
					errs = make([]error, len(stores))
				)
				for k, s := range stores {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-gate
						errs[k] = s.Lock("x", "pravo apply", fmt.Sprintf("t%d", k))
					}()
				}
				close(gate)
				wg.Wait()
				winners := 0
				for k, err := range errs {
					var locked *ErrLocked
					switch {
					case err == nil:
						winners++
					case !errors.As(err, &locked):
						t.Fatalf("round %d: store %d: %v", round, k, err)
					}
				}
				if winners != 1 {
					t.Fatalf("round %d: %d stores hold the lock, want 1 (%v)", round, winners, errs)
				}
				for k, err := range errs {
					if err == nil {
						if cur, err := stores[k].readLock("x"); err != nil || cur.Nonce != stores[k].nonce {
							t.Fatalf("round %d: the winner is store %d, and the lock is %+v (%v)", round, k, cur, err)
						}
						if err := stores[k].Unlock("x"); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
		})
	}
}

// A holder that was taken for gone, and finds its lock taken, neither renews nor
// releases the lock of the command that took it.
func TestReclaimedLockIsNotRenewedOrReleasedByItsFormerHolder(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			stores := openMany(t, b, 2)
			a, c := stores[0], stores[1]
			if err := a.Lock("x", "pravo apply", "ta"); err != nil {
				t.Fatal(err)
			}
			// a is suspended for longer than the lock lives: c takes the lock over.
			cur, _ := a.readLock("x")
			cur.RenewedAt = cur.RenewedAt.Add(-2 * StaleAfter)
			cur.Since = cur.Since.Add(-2 * StaleAfter)
			cur.Host = "elsewhere"
			data, _ := json.Marshal(cur)
			if err := b.Put(context.Background(), lockKey("x"), data); err != nil {
				t.Fatal(err)
			}
			if err := c.Lock("x", "pravo apply", "tc"); err != nil {
				t.Fatalf("the lock of a suspended holder is not reclaimed: %v", err)
			}
			var locked *ErrLocked
			if err := a.Renew("x"); !errors.As(err, &locked) || locked.Lock.Nonce != c.nonce {
				t.Errorf("Renew by the former holder: %v, want the lock of the new holder", err)
			}
			if err := a.Unlock("x"); !errors.As(err, &locked) || locked.Lock.Nonce != c.nonce {
				t.Errorf("Unlock by the former holder: %v, want the lock of the new holder", err)
			}
			if got, err := c.readLock("x"); err != nil || got.Nonce != c.nonce || got.Txn != "tc" {
				t.Errorf("the lock is %+v (%v) after the former holder tried to renew and release it", got, err)
			}
			if err := c.Renew("x"); err != nil {
				t.Errorf("Renew by the holder: %v", err)
			}
			if err := c.Unlock("x"); err != nil {
				t.Errorf("Unlock by the holder: %v", err)
			}
			if _, err := c.readLock("x"); !errors.Is(err, ErrNotFound) {
				t.Errorf("the lock is still there after its holder released it: %v", err)
			}
		})
	}
}

// A lock that cannot be read is not deleted by the command that holds the
// address: it is left for the one that reclaims it.
func TestUnlockLeavesALockThatCannotBeRead(t *testing.T) {
	b, err := NewLocalBackend(filepath.Join(t.TempDir(), "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	s := openMany(t, b, 1)[0]
	if err := b.Put(context.Background(), lockKey("x"), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Unlock("x"); err == nil {
		t.Error("Unlock of an unreadable lock reported nothing")
	}
	if _, err := b.Get(context.Background(), lockKey("x")); err != nil {
		t.Errorf("the unreadable lock was deleted by Unlock: %v", err)
	}
	// A command that wants the address takes it: the lock is broken.
	if err := s.Lock("x", "pravo apply", ""); err != nil {
		t.Errorf("Lock over an unreadable lock: %v", err)
	}
}

// A refresh writes what the provider reported over the record the command read.
// When another command wrote the record, removed it or created it again since,
// nothing is written.
func TestRefreshWritesNothingFromAnOlderView(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			rec := func(content string) *Resource {
				return &Resource{Mode: "managed", Type: "t", Name: "r", Attributes: json.RawMessage(content), ConfigHash: "h" + content}
			}
			if err := openMany(t, b, 1)[0].Set(rec(`{"v":1}`), "t1"); err != nil {
				t.Fatal(err)
			}
			// Two commands read the record.
			readers := openMany(t, b, 2)
			viewer, other := readers[0], readers[1]

			// The same record: the refresh is written and the revision stays.
			fresh := *viewer.Get("t.r")
			fresh.RefreshedAt = Now()
			if err := viewer.Refresh(&fresh, "t2"); err != nil {
				t.Fatalf("Refresh of a record that did not change: %v", err)
			}
			if got := viewer.Get("t.r"); got.Revision != 1 || got.RefreshedAt == nil {
				t.Fatalf("after a refresh: %+v", got)
			}

			// Another command applies the record.
			if err := other.Set(other.Get("t.r"), "t3"); err != nil {
				t.Fatal(err)
			}
			if _, err := other.Remove("t.r"); err != nil {
				t.Fatal(err)
			}
			if err := other.Set(rec(`{"v":3}`), "t4"); err != nil {
				t.Fatal(err)
			}
			want, _ := b.Get(context.Background(), nodeKey("t.r"))
			stale := *viewer.Get("t.r")
			stale.Attributes = json.RawMessage(`{"v":2}`)
			stale.DriftedAt = Now()
			if err := viewer.Refresh(&stale, "t5"); !errors.Is(err, ErrChanged) {
				t.Fatalf("Refresh from an older view: %v, want ErrChanged", err)
			}
			if got, _ := b.Get(context.Background(), nodeKey("t.r")); string(got) != string(want) {
				t.Errorf("the record was written over:\n%s\nwas\n%s", got, want)
			}
			if got := viewer.Get("t.r"); got.Revision != 1 || !jsonSame(got.Attributes, []byte(`{"v":1}`)) {
				t.Errorf("the node of the older view changed: %+v", got)
			}

			// A record that was removed.
			if _, err := other.Remove("t.r"); err != nil {
				t.Fatal(err)
			}
			again := *viewer.Get("t.r")
			if err := viewer.Refresh(&again, "t6"); !errors.Is(err, ErrChanged) {
				t.Fatalf("Refresh of a record that was removed: %v, want ErrChanged", err)
			}
			if _, err := b.Get(context.Background(), nodeKey("t.r")); !errors.Is(err, ErrNotFound) {
				t.Errorf("the refresh created the record again: %v", err)
			}
		})
	}
}

// A record has a revision and a generation. The generation is the transaction
// that created it; an update keeps it, and a record that is removed and created
// again has another one, at the same revision.
func TestGenerationOfARecord(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			s := openMany(t, b, 1)[0]
			ctx := context.Background()
			rec := func() *Resource {
				return &Resource{Mode: "managed", Type: "t", Name: "r", Attributes: json.RawMessage(`{}`)}
			}
			if err := s.Set(rec(), "t1"); err != nil {
				t.Fatal(err)
			}
			first := s.Get("t.r")
			if first.Generation != "t1" || first.Revision != 1 {
				t.Fatalf("a new record: %+v, want revision 1 in generation t1", first)
			}
			// An update from a fresh value keeps the generation.
			if err := s.Set(rec(), "t2"); err != nil {
				t.Fatal(err)
			}
			if got := s.Get("t.r"); got.Generation != "t1" || got.Revision != 2 {
				t.Fatalf("an update: %+v, want revision 2 in generation t1", got)
			}
			v, err := s.Versions([]string{"t.r", "t.none"})
			if err != nil || v["t.r"] != (Version{2, "t1"}) || v["t.none"] != (Version{}) {
				t.Fatalf("Versions: %v, %v", v, err)
			}
			// Removed and created again.
			if _, err := s.Remove("t.r"); err != nil {
				t.Fatal(err)
			}
			if err := s.Set(rec(), "t3"); err != nil {
				t.Fatal(err)
			}
			if got := s.Get("t.r"); got.Generation != "t3" || got.Revision != 1 {
				t.Fatalf("created again: %+v, want revision 1 in generation t3", got)
			}
			// What a reader of the backend finds.
			data, _ := b.Get(ctx, nodeKey("t.r"))
			if !strings.Contains(string(data), `"generation": "t3"`) {
				t.Errorf("the object does not hold the generation:\n%s", data)
			}

			// A record written before generations were recorded has none. A refresh
			// leaves it so, and the next write gives it one.
			old := rec()
			old.Name = "old"
			if err := s.Set(old, "t4"); err != nil {
				t.Fatal(err)
			}
			legacy := *s.Get("t.old")
			legacy.Generation = ""
			data, _ = indentJSON(&legacy)
			if err := b.Put(ctx, nodeKey("t.old"), data); err != nil {
				t.Fatal(err)
			}
			s2 := openMany(t, b, 1)[0]
			fresh := *s2.Get("t.old")
			fresh.RefreshedAt = Now()
			if err := s2.Refresh(&fresh, "t5"); err != nil {
				t.Fatal(err)
			}
			if got := s2.Get("t.old"); got.Generation != "" {
				t.Errorf("a refresh gave the record a generation: %+v", got)
			}
			if err := s2.Set(s2.Get("t.old"), "t6"); err != nil {
				t.Fatal(err)
			}
			if got := s2.Get("t.old"); got.Generation != "t6" || got.Revision != 2 {
				t.Errorf("a write of a record with no generation: %+v, want revision 2 in generation t6", got)
			}
		})
	}
}

// Locks reports which locks may be reclaimed, ReleaseStale releases those and no
// others, and Interrupted finds the running transactions that no live lock is
// held for.
func TestStaleLocksAreListedAndReleased(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			stores := openMany(t, b, 2)
			live, other := stores[0], stores[1]
			writeDeadLock(t, b, "dead.one")
			writeDeadLock(t, b, "dead.two")
			if err := live.Lock("live", "pravo apply", "tlive"); err != nil {
				t.Fatal(err)
			}
			expired := time.Now().Add(-time.Minute)
			if _, err := other.ReserveAll([]string{"reserved.old"}, "pravo plan --reserve 1m", time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			cur, _ := other.readLock("reserved.old")
			cur.Expires = &expired
			data, _ := json.Marshal(cur)
			_ = b.Put(context.Background(), lockKey("reserved.old"), data)

			locks, err := live.Locks()
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, l := range locks {
				got[l.Addr] = l.Stale
			}
			want := map[string]bool{"dead.one": true, "dead.two": true, "live": false, "reserved.old": true}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("stale by address: %v, want %v", got, want)
			}

			// Two transactions are running: one holds a live lock, one has none.
			lt := live.Begin("apply", "pravo apply")
			dead := live.Begin("apply", "pravo apply")
			for _, tx := range []*Txn{lt, dead} {
				if err := tx.Start(); err != nil {
					t.Fatal(err)
				}
			}
			if err := live.Lock("live.too", "pravo apply", lt.ID()); err != nil {
				t.Fatal(err)
			}
			txns, _ := live.Transactions()
			interrupted, err := live.Interrupted(txns)
			if err != nil {
				t.Fatal(err)
			}
			if len(interrupted) != 1 || !interrupted[dead.ID()] {
				t.Errorf("interrupted: %v, want only %s", interrupted, dead.ID())
			}

			// The lock of the dead apply is renewed by a process that is not gone,
			// between the listing and the release: it stays.
			released, err := live.ReleaseStale()
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(released) != "[dead.one dead.two reserved.old]" {
				t.Errorf("released %v", released)
			}
			locks, _ = live.Locks()
			var left []string
			for _, l := range locks {
				left = append(left, l.Addr)
			}
			if fmt.Sprint(left) != "[live live.too]" {
				t.Errorf("locks left %v", left)
			}
			if again, _ := live.ReleaseStale(); len(again) != 0 {
				t.Errorf("a second release found %v", again)
			}
		})
	}
}

// A command that rewrites the records that depend on a node takes their locks
// first, and changes none of them when it cannot.
func TestLockDependents(t *testing.T) {
	b, err := NewLocalBackend(filepath.Join(t.TempDir(), "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	stores := openMany(t, b, 1)
	setup := stores[0]
	for _, r := range []*Resource{
		{Mode: "managed", Type: "t", Name: "base", Attributes: json.RawMessage(`{}`)},
		{Mode: "managed", Type: "t", Name: "d1", Attributes: json.RawMessage(`{}`), Dependencies: []string{"t.base"}},
		{Mode: "managed", Type: "t", Name: "d2", Attributes: json.RawMessage(`{}`), Dependencies: []string{"t.base"}},
		{Mode: "managed", Type: "t", Name: "solo", Attributes: json.RawMessage(`{}`)},
	} {
		if err := setup.Set(r, "t1"); err != nil {
			t.Fatal(err)
		}
	}
	mine, theirs := openMany(t, b, 2)[0], openMany(t, b, 2)[1]

	deps, release, err := mine.LockDependents("t.base", "pravo state mv", "")
	if err != nil || fmt.Sprint(deps) != "[t.d1 t.d2]" {
		t.Fatalf("LockDependents: %v, %v", deps, err)
	}
	if locks, _ := mine.Locks(); len(locks) != 2 {
		t.Errorf("%d locks held, want those of the two dependents", len(locks))
	}
	var locked *ErrLocked
	if err := theirs.Lock("t.d2", "pravo apply", ""); !errors.As(err, &locked) {
		t.Errorf("the dependent is not locked: %v", err)
	}
	release()
	if locks, _ := mine.Locks(); len(locks) != 0 {
		t.Errorf("%d locks left after the release", len(locks))
	}

	// One of them is held by another command: none is taken.
	if err := theirs.Lock("t.d2", "pravo apply", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mine.LockDependents("t.base", "pravo state mv", ""); !errors.As(err, &locked) || !strings.Contains(err.Error(), "t.d2 depends on t.base") {
		t.Fatalf("LockDependents with a dependent held: %v", err)
	}
	if locks, _ := mine.Locks(); len(locks) != 1 || locks[0].Addr != "t.d2" {
		t.Errorf("locks %v, want only the one of the other command", locks)
	}
	if err := theirs.Unlock("t.d2"); err != nil {
		t.Fatal(err)
	}

	// One of them was written since this store read it.
	if err := theirs.Set(theirs.Get("t.d1"), "t2"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mine.LockDependents("t.base", "pravo state mv", ""); err == nil || !strings.Contains(err.Error(), "changed in the state since it was read") {
		t.Fatalf("LockDependents with a dependent written since: %v", err)
	}
	if locks, _ := mine.Locks(); len(locks) != 0 {
		t.Errorf("%d locks held after the refusal", len(locks))
	}
	if err := mine.Verify("t.solo"); err != nil {
		t.Errorf("Verify of a record that did not change: %v", err)
	}
	if err := mine.Verify("t.d1"); !errors.Is(err, ErrChanged) {
		t.Errorf("Verify of a record that was written: %v", err)
	}
}

// An update of the outputs that finds the lock of a command that is gone waits
// for it to be reclaimed and then writes, instead of failing after the resources
// were applied: the wait is longer than the time after which the lock is stale.
func TestOutputsLockOfAGoneHolderIsReclaimedWithinTheWait(t *testing.T) {
	if outputsLockWait <= outputsLockStale {
		t.Fatalf("an update waits %s for a lock that is stale after %s", outputsLockWait, outputsLockStale)
	}
	staleWas, waitWas := outputsLockStale, outputsLockWait
	outputsLockStale, outputsLockWait = 150*time.Millisecond, 3*time.Second
	t.Cleanup(func() { outputsLockStale, outputsLockWait = staleWas, waitWas })

	_, b := newFakeS3(t) // not on this machine: the holder's process cannot be looked at
	s := openMany(t, b, 1)[0]
	old := time.Now().Add(-10 * time.Millisecond).UTC()
	dead, _ := json.Marshal(Lock{Addr: "outputs.json", PID: 1, Host: "elsewhere", Command: "update outputs", Nonce: "dead", Since: old, RenewedAt: old, Kind: "lock"})
	if err := b.Put(context.Background(), outputsLockKey, dead); err != nil {
		t.Fatal(err)
	}
	begun := time.Now()
	err := s.UpdateOutputs(func(recorded map[string]json.RawMessage, present bool) (map[string]json.RawMessage, bool, error) {
		return map[string]json.RawMessage{"o": json.RawMessage(`1`)}, true, nil
	})
	if err != nil {
		t.Fatalf("UpdateOutputs: %v", err)
	}
	if took := time.Since(begun); took < 100*time.Millisecond {
		t.Errorf("the update took %s: it did not wait for the lock to go stale", took)
	}
	if got, _ := s.ReloadOutputs(); string(got["o"]) != "1" {
		t.Errorf("outputs %v", got)
	}
	if _, err := b.Get(context.Background(), outputsLockKey); !errors.Is(err, ErrNotFound) {
		t.Errorf("the lock on the outputs was left: %v", err)
	}
}

// A command that ends within two minutes renews no lock.
func TestHeartbeatIsRare(t *testing.T) {
	if HeartbeatInterval < 2*time.Minute {
		t.Errorf("HeartbeatInterval is %s: a short command renews its locks", HeartbeatInterval)
	}
	if StaleAfter < 4*HeartbeatInterval {
		t.Errorf("a lock goes stale after %s and is renewed every %s: fewer than four renewals may be missed", StaleAfter, HeartbeatInterval)
	}
}

// On a bucket the condition is on the entity tag of the version that was read:
// an object that another writer replaced between the read and the delete, or the
// replace, is left as it is.
func TestBucketConditionsHoldAgainstAWriterInBetween(t *testing.T) {
	_, b := newFakeS3(t)
	ctx := context.Background()
	if err := b.Put(ctx, "locks/a.lock", []byte("one")); err != nil {
		t.Fatal(err)
	}
	ok, err := b.DeleteIf(ctx, "locks/a.lock", func(cur []byte) bool {
		if err := b.Put(ctx, "locks/a.lock", []byte("two")); err != nil { // another command renews it
			t.Fatal(err)
		}
		return true
	})
	if ok || err != nil {
		t.Fatalf("DeleteIf over a replaced object: %v, %v; want false", ok, err)
	}
	if got, _ := b.Get(ctx, "locks/a.lock"); string(got) != "two" {
		t.Fatalf("the object is %q: the delete went through", got)
	}
	ok, err = b.ReplaceIf(ctx, "locks/a.lock", func(cur []byte) ([]byte, bool) {
		if err := b.Put(ctx, "locks/a.lock", []byte("three")); err != nil {
			t.Fatal(err)
		}
		return []byte("mine"), true
	})
	if ok || err != nil {
		t.Fatalf("ReplaceIf over a replaced object: %v, %v; want false", ok, err)
	}
	if got, _ := b.Get(ctx, "locks/a.lock"); string(got) != "three" {
		t.Fatalf("the object is %q: the replace went through", got)
	}
	// Removed in between: the object is not found, and not written again.
	ok, err = b.ReplaceIf(ctx, "locks/a.lock", func(cur []byte) ([]byte, bool) {
		if err := b.Delete(ctx, "locks/a.lock"); err != nil {
			t.Fatal(err)
		}
		return []byte("mine"), true
	})
	if ok || !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReplaceIf over a removed object: %v, %v; want ErrNotFound", ok, err)
	}
	if _, err := b.Get(ctx, "locks/a.lock"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the object was written again: %v", err)
	}
}

// Two commands judge the same lock gone. One takes it over before the other
// deletes it: the other deletes nothing, where it used to delete the lock that
// the first had just taken.
func TestDeleteLockLeavesTheLockThatReplacedTheOneJudged(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			stores := openMany(t, b, 2)
			a, c := stores[0], stores[1]
			writeDeadLock(t, b, "x")
			raw, holder, err := c.readHolder("x")
			if err != nil || !c.stale(holder) {
				t.Fatalf("the dead lock: %v, stale %v", err, c.stale(holder))
			}
			if err := a.Lock("x", "pravo apply", "ta"); err != nil {
				t.Fatalf("a reclaims the lock: %v", err)
			}
			if c.deleteLock("x", raw) {
				t.Fatal("c deleted the lock that a took in place of the one it judged")
			}
			if cur, err := a.readLock("x"); err != nil || cur.Nonce != a.nonce {
				t.Fatalf("the lock is %+v (%v), want a's", cur, err)
			}
			var locked *ErrLocked
			if err := c.Lock("x", "pravo apply", "tc"); !errors.As(err, &locked) || locked.Lock.Nonce != a.nonce {
				t.Fatalf("c: %v, want a's lock", err)
			}
		})
	}
}

// Suspects are the locks of commands that stopped. A lock that is renewed, a
// reservation and a store with no locks are not suspects.
func TestSuspectsAreTheStaleLocksOfCommands(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			stores := openMany(t, b, 2)
			live, other := stores[0], stores[1]
			if got, err := live.Suspects(); err != nil || len(got) != 0 {
				t.Fatalf("a store with no locks has suspects %v (%v)", got, err)
			}
			writeDeadLock(t, b, "dead.one")
			if err := live.Lock("live", "pravo apply", "tlive"); err != nil {
				t.Fatal(err)
			}
			if _, err := other.ReserveAll([]string{"reserved.old"}, "pravo plan --reserve 1m", time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			cur, _ := other.readLock("reserved.old")
			past := time.Now().Add(-time.Minute)
			cur.Expires = &past
			data, _ := json.Marshal(cur)
			_ = b.Put(context.Background(), lockKey("reserved.old"), data)

			got, err := live.Suspects()
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got["dead.one"].Addr != "dead.one" {
				t.Errorf("suspects = %v, want the lock on dead.one alone", got)
			}
		})
	}
}
