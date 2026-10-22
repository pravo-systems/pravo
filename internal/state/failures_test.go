package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
)

// The ways a store of format 2 or 3 can be damaged or refused that no other test
// reaches.
func TestUpgradeAndSealFailures(t *testing.T) {
	ctx := context.Background()
	ada := testIdentity(t, "ada")

	t.Run("a record under the key of another address", func(t *testing.T) {
		b, _ := NewLocalBackend(t.TempDir())
		s, err := OpenBackendWith(ctx, b, Options{NewFormat: Format})
		if err != nil {
			t.Fatal(err)
		}
		mustSet(t, s, node("a"), node("b"))
		data, _ := b.Get(ctx, nodeKey("t.a"))
		if err := b.Put(ctx, nodeKey("t.b"), data); err != nil {
			t.Fatal(err)
		}
		_, err = s.Upgrade(ctx, UpgradeOptions{To: FormatImmutable})
		if err == nil || !strings.Contains(err.Error(), "holds the record of t.a") {
			t.Errorf("upgrade = %v, want an error that names the record", err)
		}
	})

	t.Run("a store of format 3 is not moved to format 2", func(t *testing.T) {
		b, _ := NewLocalBackend(t.TempDir())
		s := sealedStore(t, b, ada)
		if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatImmutable}); err == nil || !strings.Contains(err.Error(), "not moved back") {
			t.Errorf("upgrade of a sealed state to format 2 = %v", err)
		}
	})

	t.Run("a format 3 meta.json with no sealed part", func(t *testing.T) {
		b, _ := NewLocalBackend(t.TempDir())
		if err := b.Put(ctx, "meta.json", []byte(`{"format":3,"lineage":"abc","created":"2026-01-01T00:00:00Z"}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenBackendWith(ctx, b, Options{Identity: ada}); err == nil || !strings.Contains(err.Error(), "damaged") {
			t.Errorf("open = %v, want an error that says meta.json is damaged", err)
		}
	})

	t.Run("a rewrap of a state that is not sealed", func(t *testing.T) {
		skipWhenSealed(t)
		b, _ := NewLocalBackend(t.TempDir())
		s, _ := OpenBackend(ctx, b)
		if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada)}); err == nil || !strings.Contains(err.Error(), "not sealed") {
			t.Errorf("rewrap = %v", err)
		}
	})

	t.Run("a rewrap of a state whose lineage changed", func(t *testing.T) {
		b, _ := NewLocalBackend(t.TempDir())
		s := sealedStore(t, b, ada)
		m, _ := readMeta(ctx, b)
		m.Lineage = "another"
		if err := b.Put(ctx, "meta.json", mustIndent(t, m)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada, testIdentity(t, "dan"))}); err == nil || !strings.Contains(err.Error(), "lineage") {
			t.Errorf("rewrap = %v, want an error about the lineage", err)
		}
	})

	t.Run("a sealed record of a version this pravo does not read", func(t *testing.T) {
		b, _ := NewLocalBackend(t.TempDir())
		sealedStore(t, b, ada)
		p := pointerAt(t, b, "net.vpc")
		data, _ := b.Get(ctx, p.Record)
		var rec map[string]any
		_ = json.Unmarshal(data, &rec)
		rec["v"] = float64(7)
		out := mustIndent(t, rec)
		p.Record = recKey(out)
		_ = b.Put(ctx, p.Record, out)
		_ = b.Put(ctx, nodeKey("net.vpc"), mustIndent(t, p))
		_, err := OpenBackendWith(ctx, b, Options{Identity: ada})
		if err == nil || !strings.Contains(err.Error(), "version 7") {
			t.Errorf("open = %v, want an error that names the version", err)
		}
		// A record of a version that is not known is not read even for its readable part.
		if _, err := OpenBackendWith(ctx, b, Options{}); err == nil || !strings.Contains(err.Error(), "version 7") {
			t.Errorf("a locked open of the same store: %v", err)
		}
	})

	t.Run("a revision that is negative has no place to be bound to", func(t *testing.T) {
		b, _ := NewLocalBackend(t.TempDir())
		s := sealedStore(t, b, ada)
		if _, err := s.recordCtx(&Resource{Mode: "managed", Type: "t", Name: "a", Revision: -1}); !errors.Is(err, ErrUntrusted) {
			t.Errorf("recordCtx of revision -1 = %v, want ErrUntrusted", err)
		}
	})

	t.Run("a pointer that names a record in a group that meta.json does not list", func(t *testing.T) {
		b, _ := NewLocalBackend(t.TempDir())
		sealedStore(t, b, ada)
		p := pointerAt(t, b, "net.vpc")
		data, _ := b.Get(ctx, p.Record)
		var rec map[string]any
		_ = json.Unmarshal(data, &rec)
		rec["sealed"].(map[string]any)["group"] = "g9"
		out := mustIndent(t, rec)
		p.Record = recKey(out)
		_ = b.Put(ctx, p.Record, out)
		_ = b.Put(ctx, nodeKey("net.vpc"), mustIndent(t, p))
		for _, id := range []*protect.Identity{ada, nil} {
			if _, err := OpenBackendWith(ctx, b, Options{Identity: id}); !errors.Is(err, ErrUntrusted) || errors.Is(err, ErrNotReader) || !strings.Contains(err.Error(), "g9") {
				t.Errorf("open = %v, want ErrUntrusted that names the group", err)
			}
		}
	})
}

// A bucket that answers that an object changed after the compare accepted it is
// a lost race. The upgrade and the rewrap read again, and say so when they cannot
// win.
func TestUpgradeAndRewrapAgainstLostRaces(t *testing.T) {
	ctx := context.Background()
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")

	t.Run("a record that is converted", func(t *testing.T) {
		lb, _ := NewLocalBackend(t.TempDir())
		b := &lostRace{Backend: lb, prefix: "resources/"}
		s, err := OpenBackendWith(ctx, b, Options{NewFormat: Format})
		if err != nil {
			t.Fatal(err)
		}
		seedRecords(t, s)
		b.lose.Store(3)
		if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatImmutable}); err != nil {
			t.Fatalf("upgrade after three lost races: %v", err)
		}
		if v, err := s.VerifyAll(ctx); err != nil || len(v.Bad) != 0 || v.Checked != s.Len() {
			t.Errorf("VerifyAll = %+v, %v", v, err)
		}
	})

	t.Run("a record that is always lost", func(t *testing.T) {
		lb, _ := NewLocalBackend(t.TempDir())
		b := &lostRace{Backend: lb, prefix: "resources/"}
		s, err := OpenBackendWith(ctx, b, Options{NewFormat: Format})
		if err != nil {
			t.Fatal(err)
		}
		mustSet(t, s, node("a"))
		b.lose.Store(1000)
		if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatImmutable}); err == nil || !strings.Contains(err.Error(), "t.a") {
			t.Errorf("upgrade that never won = %v, want an error that names t.a", err)
		}
		b.lose.Store(0)
		if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatImmutable}); err != nil {
			t.Errorf("the upgrade, run again: %v", err)
		}
	})

	t.Run("meta.json", func(t *testing.T) {
		lb, _ := NewLocalBackend(t.TempDir())
		b := &lostRace{Backend: lb, prefix: "meta.json"}
		s := sealedStore(t, b, ada)
		b.lose.Store(2)
		if res, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada, dan)}); err != nil || len(res.Change.Added) != 1 {
			t.Fatalf("rewrap after two lost races = %+v, %v", res, err)
		}
		b.lose.Store(1000)
		if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada)}); err == nil || !strings.Contains(err.Error(), "run the command again") {
			t.Errorf("rewrap that never won = %v, want the advice to run it again", err)
		}
		b.lose.Store(0)
		if got := openIdentity(t, lb, dan); got.Locked() {
			t.Error("the rewrap that did not win changed the locks")
		}
	})
}
