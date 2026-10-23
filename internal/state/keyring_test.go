package state

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// heldKeys is the keys the store holds now, for a test that looks at whether they
// are wiped.
func heldKeys(s *Store) *protect.Keys {
	s.ring.mu.RLock()
	defer s.ring.mu.RUnlock()
	return s.ring.keys
}

// Close wipes the data keys. The store lists what it read, and gives no value and
// takes no write.
func TestCloseWipesTheKeys(t *testing.T) {
	skipWhenSealed(t)
	ada := testIdentity(t, "ada")
	b, _ := NewLocalBackend(t.TempDir())
	s := sealedStore(t, b, ada)
	keys := heldKeys(s)
	box, err := s.Sealer().Seal(protect.KindPlan, "plan-1", []byte("a plan"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Open(protect.Ctx{Lineage: s.Lineage(), Kind: protect.KindPlan, Name: "plan-1"}, box); err != nil {
		t.Fatalf("the keys do not open what they sealed: %v", err)
	}

	s.Close()
	s.Close() // twice is no harm

	if _, err := keys.Open(protect.Ctx{Lineage: s.Lineage(), Kind: protect.KindPlan, Name: "plan-1"}, box); err == nil {
		t.Error("the keys of a closed store still open a sealed value")
	}
	if !s.Locked() {
		t.Error("a closed store is not locked")
	}
	if s.Get("db.main") == nil {
		t.Error("a closed store no longer lists what it read")
	}
	if _, err := s.Sealer().Seal(protect.KindPlan, "plan-2", []byte("x")); !errors.Is(err, ErrNotReader) {
		t.Errorf("Seal after Close = %v, want ErrNotReader", err)
	}
	if _, err := s.Sealer().Open(protect.KindPlan, "plan-1", box); !errors.Is(err, ErrNotReader) {
		t.Errorf("Open after Close = %v, want ErrNotReader", err)
	}
	r := *s.Get("net.vpc")
	r.Attributes = json.RawMessage(`{"id":"vpc-2"}`)
	if err := s.Set(&r, ""); !errors.Is(err, ErrNotReader) {
		t.Errorf("Set after Close = %v, want ErrNotReader", err)
	}
	if tag := s.Sealer().(Tagger).Tag("v"); tag != "" {
		t.Errorf("a closed store tags %q", tag)
	}

	t.Run("a store that is not sealed is unchanged", func(t *testing.T) {
		plain, err := OpenBackend(context.Background(), mustLocal2(t))
		if err != nil {
			t.Fatal(err)
		}
		mustSet(t, plain, node("a"))
		plain.Close()
		r := *plain.Get("t.a")
		r.Attributes = json.RawMessage(`{"id":"2"}`)
		if err := plain.Set(&r, ""); err != nil {
			t.Errorf("Set after Close in a store that is not sealed = %v", err)
		}
	})
}

// A store that switches to the current group after a rewrap in another process
// wipes the keys it had.
func TestKeysAreWipedWhenTheStoreSwitchesGroup(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
	b, _ := NewLocalBackend(t.TempDir())
	long := sealedStore(t, b, ada, fay)
	was := sealRecheck
	defer func() { sealRecheck = was }()
	sealRecheck = 0

	old := heldKeys(long)
	c := protect.Ctx{Lineage: long.Lineage(), Kind: protect.KindPlan, Name: "q"}
	g1 := old.Seal(c, []byte("y"))
	if g1.Group != "g1" {
		t.Fatalf("sealed in %s, want g1", g1.Group)
	}
	if _, err := old.Open(c, g1); err != nil {
		t.Fatalf("the keys do not open what they sealed: %v", err)
	}

	if _, err := openIdentity(t, b, ada).Rewrap(ctx, RewrapOptions{Readers: readersOf(ada)}); err != nil {
		t.Fatal(err)
	}
	if _, err := long.Sealer().Seal(protect.KindPlan, "p", []byte("x")); err != nil {
		t.Fatal(err)
	}
	now := heldKeys(long)
	if now == old {
		t.Fatal("the store did not switch to the new keys")
	}
	if now.Current() != "g2" {
		t.Errorf("the store seals in %s, want g2", now.Current())
	}
	if _, err := now.Open(c, g1); err != nil {
		t.Errorf("the new keys do not open a value in g1: %v", err)
	}
	if _, err := old.Open(c, g1); err == nil {
		t.Error("the keys that were replaced still open a value")
	}
}

// Keys are replaced while other goroutines seal and open with them. Every call
// gets a working key: a key is wiped only when nobody is using it.
func TestKeysSwapUnderUse(t *testing.T) {
	skipWhenSealed(t)
	ada := testIdentity(t, "ada")
	b, _ := NewLocalBackend(t.TempDir())
	s := sealedStore(t, b, ada)
	m := formatMeta(t, b)
	sl := s.Sealer()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	failed := make(chan error, 8)
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				box, err := sl.Seal(protect.KindPlan, "plan-1", []byte("a plan"))
				if err == nil {
					var got []byte
					if got, err = sl.Open(protect.KindPlan, "plan-1", box); err == nil && string(got) != "a plan" {
						err = errors.New("opened to " + string(got))
					}
				}
				if err != nil {
					failed <- err
					return
				}
				_ = sl.(Tagger).Tag("value")
			}
		}()
	}
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		k, err := m.Sealed.Unlock(m.Lineage, ada.Reader)
		if err != nil {
			t.Fatal(err)
		}
		s.ring.set(k)
	}
	close(stop)
	wg.Wait()
	select {
	case err := <-failed:
		t.Fatalf("a call failed while the keys were replaced: %v", err)
	default:
	}
}

// A closed store does not take its keys back: a value sealed in a group that the
// store has not heard of makes it read meta.json again, and it must not unlock the
// group with the identity it was opened with.
func TestClosedStoreDoesNotTakeItsKeysBack(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
	b, _ := NewLocalBackend(t.TempDir())
	admin := sealedStore(t, b, ada, fay)
	s := openIdentity(t, b, ada)
	s.Close()
	// A rewrap in another process: a new group, and the outputs sealed in it.
	if err := admin.SetOutputs(map[string]json.RawMessage{"o": json.RawMessage(`1`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada), All: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReloadOutputs(); err == nil {
		t.Error("a closed store reads the outputs of a group it learned of after it was closed")
	}
	if heldKeys(s) != nil {
		t.Error("a closed store holds data keys again")
	}
}
