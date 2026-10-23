package state

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
)

// A state sealed with a recovery recipient is opened by K shares when no reader
// can, and the readers can be made again through it.
func TestRecoveryOpensAStateThatNoReaderCan(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			ada, eve := testIdentity(t, "ada"), testIdentity(t, "eve")
			rec, shares, err := protect.NewRecovery(2, 3)
			if err != nil {
				t.Fatal(err)
			}
			s, err := OpenBackendWith(ctx, b, Options{NewFormat: Format, Identity: ada})
			if err != nil {
				t.Fatal(err)
			}
			seedSecrets(t, s)
			if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada, Recovery: rec}); err != nil {
				t.Fatal(err)
			}
			want := listJSON(t, s)
			p := s.Protection()
			if p.Sealed.Recovery == nil || !p.Sealed.Recovery.Equal(rec) {
				t.Fatalf("recovery in meta: %+v", p.Sealed.Recovery)
			}
			if st := p.Sealed.Status(readersOf(ada)); !st.Matches || len(st.Readers) != 1 || st.Recovery == nil || !st.Recovery.Locked {
				t.Fatalf("status %+v", st)
			}
			if got := readerNames(p.Sealed); fmt.Sprint(got) != "[ada]" {
				t.Errorf("readers named in the history: %v", got)
			}
			if m := formatMeta(t, b); len(m.History) == 0 || fmt.Sprint(m.History[len(m.History)-1].Added) != "[ada]" {
				t.Errorf("history %+v", m.History)
			}

			// Eve is no reader: the state is locked to her.
			if got := openIdentity(t, b, eve); !got.Locked() {
				t.Fatal("eve opens the store")
			}
			// Two shares rebuild the key and open everything.
			rk, err := rec.Open(shares[1:3])
			if err != nil {
				t.Fatal(err)
			}
			id := protect.RecoveryIdentity(rk)
			defer id.Close()
			rs := openIdentity(t, b, id)
			if rs.Locked() || listJSON(t, rs) != want {
				t.Fatalf("the recovery key does not open the state (locked %v)", rs.Locked())
			}
			// It makes eve the reader, in place of ada, and replaces itself.
			rec2, shares2, err := protect.NewRecovery(3, 4)
			if err != nil {
				t.Fatal(err)
			}
			// The key that opens the state cannot be replaced in the step that seals the
			// records again: the state says so, and nothing is written.
			if _, err := rs.Rewrap(ctx, RewrapOptions{Readers: readersOf(eve), All: true, By: protect.RecoveryName, Recovery: rec2}); err == nil || !strings.Contains(err.Error(), "second step") {
				t.Fatalf("replacing the key that opens the state while sealing again: %v", err)
			}
			res, err := rs.Rewrap(ctx, RewrapOptions{Readers: readersOf(eve), All: true, By: protect.RecoveryName})
			if err != nil || res.Change.NewGroup != "g2" {
				t.Fatalf("rewrap through the recovery = %+v, %v", res, err)
			}
			if _, err := rs.Rewrap(ctx, RewrapOptions{Readers: readersOf(eve), By: protect.RecoveryName, Recovery: rec2}); err != nil {
				t.Fatal(err)
			}
			if got := openIdentity(t, b, eve); got.Locked() || listJSON(t, got) != want {
				t.Errorf("eve does not open the state after the recovery (locked %v)", got.Locked())
			}
			if got := openIdentity(t, b, ada); !got.Locked() {
				t.Error("ada still opens the state")
			}
			// The old key opens nothing now; the new one does.
			if got := openIdentity(t, b, id); !got.Locked() {
				t.Error("the replaced recovery key opens the state")
			}
			newKey, err := rec2.Open(shares2[:3])
			if err != nil {
				t.Fatal(err)
			}
			nid := protect.RecoveryIdentity(newKey)
			defer nid.Close()
			if got := openIdentity(t, b, nid); got.Locked() {
				t.Error("the new recovery key does not open the state")
			}
			m := formatMeta(t, b)
			whats := []string{}
			for _, h := range m.History {
				whats = append(whats, h.What)
			}
			if !strings.Contains(strings.Join(whats, "|"), "recovery set 3/4") {
				t.Errorf("history %v", whats)
			}
		})
	}
}

// A recovery is set on a sealed state, kept through a change of readers, and removed.
func TestRewrapSetsKeepsAndRemovesARecovery(t *testing.T) {
	b := testBackends()[0].make(t)
	ctx := context.Background()
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	s := sealedStore(t, b, ada, dan)
	rec, _, err := protect.NewRecovery(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada, dan), Recovery: rec}); err != nil {
		t.Fatal(err)
	}
	if p := s.Protection(); p.Sealed.Recovery == nil || !p.Sealed.Status(readersOf(ada, dan)).Matches {
		t.Fatalf("recovery %+v", p.Sealed.Recovery)
	}
	// Dan leaves: the recovery lock is in the new group, too.
	if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada)}); err != nil {
		t.Fatal(err)
	}
	if st := s.Protection().Sealed.Status(readersOf(ada)); !st.Matches || st.Recovery == nil || !st.Recovery.Locked || st.Groups != 2 {
		t.Fatalf("status after the removal of a reader: %+v / %+v", st, st.Recovery)
	}
	// Setting and removing at once is refused.
	if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada), Recovery: rec, RemoveRecovery: true}); err == nil {
		t.Error("set and remove were accepted together")
	}
	// Nothing to change is no write.
	counted := &writeCounter{Backend: b}
	cs := openIdentity(t, counted, ada)
	counted.writes.Store(0)
	if _, err := cs.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada), Recovery: rec}); err != nil || counted.writes.Load() != 0 {
		t.Errorf("setting the recovery that is set: %v, %d writes", err, counted.writes.Load())
	}
	if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada), RemoveRecovery: true}); err != nil {
		t.Fatal(err)
	}
	if st := s.Protection().Sealed.Status(readersOf(ada)); !st.Matches || st.Recovery != nil {
		t.Fatalf("status after removing the recovery: %+v", st)
	}
	for _, g := range s.Protection().Sealed.Groups {
		if len(g.Locks) != 1 {
			t.Errorf("group %s has %d locks", g.ID, len(g.Locks))
		}
	}
}
