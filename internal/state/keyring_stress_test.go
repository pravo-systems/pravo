package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// One store is shared by many goroutines that write, read, seal and open, while
// another store rewraps the state again and again, so that the shared store
// switches its keys every few milliseconds, and the shared store is closed at the
// end. Run with -race: the keys are never read after they are wiped, nothing
// deadlocks, and the state opens afterwards.
func TestSharedStoreSurvivesRewrapsAndClose(t *testing.T) {
	skipWhenSealed(t)
	oldRecheck, oldMeta := sealRecheck, metaRecheck
	sealRecheck, metaRecheck = time.Millisecond, time.Millisecond
	defer func() { sealRecheck, metaRecheck = oldRecheck, oldMeta }()

	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
	admin := sealedStore(t, b, ada, fay)
	shared := openIdentity(t, b, ada)
	sl := shared.Sealer()

	var (
		wg       sync.WaitGroup
		stop     = make(chan struct{})
		written  sync.Map
		bad      atomic.Value
		refused  atomic.Int64
		accepted atomic.Int64
	)
	fail := func(format string, args ...any) { bad.CompareAndSwap(nil, fmt.Sprintf(format, args...)) }
	closed := func(err error) bool { return errors.Is(err, ErrNotReader) }

	for w := range 6 {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				name := fmt.Sprintf("w%dn%d", w, i%7)
				r := node(name)
				r.Attributes = json.RawMessage(fmt.Sprintf(`{"v":%d,"w":%d}`, i, w))
				switch err := shared.Set(r, ""); {
				case err == nil:
					accepted.Add(1)
					written.Store("t."+name, true)
				case closed(err), errors.Is(err, ErrUpgraded):
					refused.Add(1)
				default:
					fail("Set: %v", err)
				}
				if got := shared.Get("t." + name); got != nil && len(got.Attributes) == 0 && !shared.Locked() {
					fail("Get returned a record with no attributes while unlocked")
				}
				sealed, err := sl.Seal("tag", "x", []byte("plain"))
				if err == nil {
					if plain, err := sl.Open("tag", "x", sealed); err != nil && !closed(err) && !errors.Is(err, ErrUntrusted) {
						fail("Open of a value sealed a moment ago: %v", err)
					} else if err == nil && string(plain) != "plain" {
						fail("Open = %q", plain)
					}
				}
				if err := shared.SetOutputs(map[string]json.RawMessage{"o": json.RawMessage(fmt.Sprintf(`%d`, i))}); err != nil && !closed(err) && !errors.Is(err, ErrUpgraded) {
					fail("SetOutputs: %v", err)
				}
				_ = shared.Outputs()
				_ = shared.List()
				tx := shared.Begin("apply", "pravo apply -t k=v")
				_ = tx.Start()
				_ = tx.Finish("applied", "", false)
			}
		}(w)
	}

	// The admin rewraps, removing and adding fay, some of them with --all.
	for i := range 12 {
		readers := readersOf(ada)
		if i%2 == 1 {
			readers = readersOf(ada, fay)
		}
		_, err := admin.Rewrap(ctx, RewrapOptions{Readers: readers, All: i%3 == 0})
		if err != nil && !errors.Is(err, errConvertChanged) && !isBusy(err) {
			t.Logf("rewrap %d: %v", i, err)
		}
		time.Sleep(3 * time.Millisecond)
	}
	// Close the shared store while the workers are still going.
	shared.Close()
	shared.Close()
	time.Sleep(20 * time.Millisecond)
	close(stop)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the workers did not stop: a deadlock")
	}
	if msg, _ := bad.Load().(string); msg != "" {
		t.Fatal(msg)
	}
	if accepted.Load() == 0 {
		t.Fatal("no write was accepted")
	}
	// The state opens for its readers, and what was accepted is there.
	fresh, err := OpenBackendWith(ctx, b, Options{Identity: ada})
	if err != nil {
		t.Fatalf("the state does not open after the run: %v", err)
	}
	if fresh.Locked() {
		t.Fatal("the state is locked for ada")
	}
	written.Range(func(k, _ any) bool {
		if r := fresh.Get(k.(string)); r == nil {
			t.Errorf("%s was accepted and is not in the state", k)
		} else if len(r.Attributes) == 0 {
			t.Errorf("%s has no attributes", k)
		}
		return true
	})
	if ver, err := fresh.VerifyAll(ctx); err != nil || len(ver.Bad) != 0 {
		t.Errorf("VerifyAll = %+v, %v", ver, err)
	}
	if shared.Get("t.w0n0") == nil && !shared.Locked() {
		t.Error("a closed store is not locked")
	}
}

func isBusy(err error) bool {
	return err != nil && (errors.Is(err, ErrSealing) || contains(err.Error(), "not ready") || contains(err.Error(), "another command"))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
