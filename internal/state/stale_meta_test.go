package state

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
)

// firstMeta serves a fixed meta.json for the first read of it and the real one
// after, as a reader sees it that read meta.json just before a rewrap replaced
// it.
type firstMeta struct {
	Backend
	mu    sync.Mutex
	first []byte
	reads int
}

func (b *firstMeta) Get(ctx context.Context, key string) ([]byte, error) {
	if key == "meta.json" {
		b.mu.Lock()
		b.reads++
		first := b.reads == 1
		b.mu.Unlock()
		if first {
			return b.first, nil
		}
	}
	return b.Backend.Get(ctx, key)
}

func (b *firstMeta) metaReads() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reads
}

// A reader whose view of meta.json is older than a rewrap in another process
// meets values sealed in a group it does not know. It reads meta.json again before
// it calls the group unknown.
func TestReaderThatMissedARewrapReadsMetaAgain(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	ada, dan, fay := testIdentity(t, "ada"), testIdentity(t, "dan"), testIdentity(t, "fay")
	b, _ := NewLocalBackend(t.TempDir())
	sealedStore(t, b, ada, dan, fay)
	old, err := b.Get(ctx, "meta.json")
	if err != nil {
		t.Fatal(err)
	}
	stale := openIdentity(t, b, dan)
	admin := openIdentity(t, b, ada)
	if _, err := admin.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada, dan), All: true}); err != nil {
		t.Fatal(err)
	}
	if g := formatMeta(t, b).Sealed.Current; g != "g2" {
		t.Fatalf("the rewrap left %s as the current group, want g2", g)
	}

	t.Run("the outputs of a store that is open", func(t *testing.T) {
		out, err := stale.ReloadOutputs()
		if err != nil {
			t.Fatalf("ReloadOutputs = %v, want the outputs", err)
		}
		if _, ok := out["url"]; !ok {
			t.Errorf("the outputs are %v", out)
		}
		if p := stale.Protection(); p.Sealed == nil || p.Sealed.Current != "g2" {
			t.Errorf("the store still has the groups of the old meta.json: %+v", p.Sealed)
		}
	})
	t.Run("a store opened on a meta.json that is old", func(t *testing.T) {
		wrapped := &firstMeta{Backend: b, first: old}
		s, err := OpenBackendWith(ctx, wrapped, Options{Identity: dan})
		if err != nil {
			t.Fatalf("Open = %v, want a store", err)
		}
		if r := s.Get("db.main"); r == nil || len(r.Attributes) == 0 {
			t.Errorf("db.main = %+v", r)
		}
		if n := wrapped.metaReads(); n != 2 {
			t.Errorf("meta.json was read %d times, want 2", n)
		}
	})
	t.Run("a store that holds no key", func(t *testing.T) {
		wrapped := &firstMeta{Backend: b, first: old}
		s, err := OpenBackendWith(ctx, wrapped, Options{})
		if err != nil {
			t.Fatalf("Open = %v, want a locked store", err)
		}
		if !s.Locked() || s.Get("db.main") == nil {
			t.Error("the store is not locked, or does not list db.main")
		}
	})
	t.Run("a sealed value of a plan", func(t *testing.T) {
		box, err := admin.Sealer().Seal(protect.KindPlan, "plan-1", []byte("a plan"))
		if err != nil || box.Group != "g2" {
			t.Fatalf("Seal = %+v, %v", box, err)
		}
		fresh := openIdentity(t, &firstMeta{Backend: b, first: old}, dan)
		if got, err := fresh.Sealer().Open(protect.KindPlan, "plan-1", box); err != nil || string(got) != "a plan" {
			t.Errorf("Open = %q, %v", got, err)
		}
	})
}

// A group that no meta.json lists is untrusted after meta.json was read again,
// and many records in it cost one more read of meta.json and not one each.
func TestUnknownGroupIsUntrustedAfterOneMoreRead(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	b, _ := NewLocalBackend(t.TempDir())
	sealedStore(t, b, ada)
	for _, addr := range []string{"db.main", "net.vpc", "db.gone"} {
		p := pointerAt(t, b, addr)
		data, err := b.Get(ctx, p.Record)
		if err != nil {
			t.Fatal(err)
		}
		rec := map[string]any{}
		if err := json.Unmarshal(data, &rec); err != nil {
			t.Fatal(err)
		}
		rec["sealed"].(map[string]any)["group"] = "g9"
		out := mustIndent(t, rec)
		p.Record = recKey(out)
		if err := b.Put(ctx, p.Record, out); err != nil {
			t.Fatal(err)
		}
		if err := b.Put(ctx, nodeKey(addr), mustIndent(t, p)); err != nil {
			t.Fatal(err)
		}
	}
	counted := &metaCounter{Backend: b}
	_, err := OpenBackendWith(ctx, counted, Options{Identity: ada})
	if !errors.Is(err, ErrUntrusted) || errors.Is(err, ErrNotReader) {
		t.Fatalf("Open = %v, want ErrUntrusted", err)
	}
	if n := counted.reads(); n > 3 {
		t.Errorf("meta.json was read %d times for records in a group that is not listed", n)
	}
}

type metaCounter struct {
	Backend
	mu sync.Mutex
	n  int
}

func (b *metaCounter) Get(ctx context.Context, key string) ([]byte, error) {
	if key == "meta.json" {
		b.mu.Lock()
		b.n++
		b.mu.Unlock()
	}
	return b.Backend.Get(ctx, key)
}

func (b *metaCounter) reads() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.n
}

// meta.json that is read while it is being replaced may hold a sealed part that
// does not hold together. The open reads it again before it calls the state
// untrusted, and does not read it again when the bytes are the same.
func TestOpenReadsADamagedMetaAgain(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	b, _ := NewLocalBackend(t.TempDir())
	sealedStore(t, b, ada)
	good, err := b.Get(ctx, "meta.json")
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(good, &m); err != nil {
		t.Fatal(err)
	}
	m["sealed"].(map[string]any)["groups"] = []any{}
	damaged := mustIndent(t, m)

	wrapped := &firstMeta{Backend: b, first: damaged}
	if _, err := OpenBackendWith(ctx, wrapped, Options{Identity: ada}); err != nil {
		t.Fatalf("Open after a damaged first read = %v, want a store", err)
	}
	// Damage that stays is untrusted, and is read twice at most.
	if err := b.Put(ctx, "meta.json", damaged); err != nil {
		t.Fatal(err)
	}
	counted := &metaCounter{Backend: b}
	if _, err := OpenBackendWith(ctx, counted, Options{Identity: ada}); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("Open = %v, want ErrUntrusted", err)
	}
	if n := counted.reads(); n != 2 {
		t.Errorf("meta.json was read %d times, want 2", n)
	}
}
