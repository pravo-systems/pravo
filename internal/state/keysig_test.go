package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// editMeta changes meta.json as someone who can write the bucket and holds no key.
func editMeta(t *testing.T, b Backend, change func(m *meta)) {
	t.Helper()
	ctx := context.Background()
	data, err := b.Get(ctx, "meta.json")
	if err != nil {
		t.Fatal(err)
	}
	var m meta
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	change(&m)
	out, err := indentJSON(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, "meta.json", out); err != nil {
		t.Fatal(err)
	}
}

// keysIn says what the readers make of the list of key groups that the bucket holds.
func keysIn(t *testing.T, b Backend, readers []protect.Reader) string {
	t.Helper()
	m := formatMeta(t, b)
	st, name := m.Sealed.KeysStatus(readers, m.Lineage)
	return fmt.Sprintf("%s %s", st.Key(), name)
}

// plantGroup adds a group whose data key the caller knows, locked for the readers, and
// makes it the current one: what the locks alone cannot tell from a group of a reader.
func plantGroup(t *testing.T, b Backend, readers []protect.Reader) (id string) {
	t.Helper()
	editMeta(t, b, func(m *meta) {
		id = fmt.Sprintf("g%d", len(m.Sealed.Groups)+1)
		g, _, err := protect.NewGroup(id, m.Lineage, readers)
		if err != nil {
			t.Fatal(err)
		}
		m.Sealed.Groups = append(m.Sealed.Groups, g)
		m.Sealed.Current = id
	})
	return id
}

// groupOf is the group that the record of addr is sealed in.
func groupOf(t *testing.T, s *Store, b Backend, addr string) string {
	t.Helper()
	data, err := b.Get(context.Background(), s.heldRecord(addr))
	if err != nil {
		t.Fatal(err)
	}
	var rec sealedRecord
	if err := json.Unmarshal(data, &rec); err != nil || rec.Sealed == nil {
		t.Fatalf("the record of %s is not sealed (%v)", addr, err)
	}
	return rec.Sealed.Group
}

// A state is sealed with a list of key groups that the reader who sealed it signed, and
// each group commits to its key.
func TestASealedStateSignsItsKeys(t *testing.T) {
	skipWhenSealed(t)
	b, _ := NewLocalBackend(t.TempDir())
	ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
	s := sealedStore(t, b, ada, fay)
	readers := readersOf(ada, fay)
	if got := keysIn(t, b, readers); got != "verified ada" {
		t.Fatalf("keys = %s", got)
	}
	if st, name := s.KeysStatus(readers); st != protect.SigVerified || name != "ada" {
		t.Fatalf("KeysStatus = %s, %s", st, name)
	}
	for _, g := range formatMeta(t, b).Sealed.Groups {
		if len(g.Commit) == 0 {
			t.Fatalf("group %s has no commitment", g.ID)
		}
	}
	// To readers that do not list ada, nobody vouches for the list.
	if got := keysIn(t, b, readersOf(fay)); got != "key_not_in_file " {
		t.Fatalf("keys to a file without the signer = %s", got)
	}
}

// Someone who can write the bucket and holds no key adds a group with a key of their own
// and makes it the current one. A reader that writes seals nothing with it: a list that a
// reader signed no longer verifies, and a list without a signature gets a new group first.
func TestAGroupPlantedInTheBucketSealsNothing(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	for _, strip := range []bool{false, true} {
		t.Run(fmt.Sprintf("signature stripped %v", strip), func(t *testing.T) {
			b, _ := NewLocalBackend(t.TempDir())
			ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
			sealedStore(t, b, ada, fay).Close()
			readers := readersOf(ada, fay)
			planted := plantGroup(t, b, readers)
			if strip {
				editMeta(t, b, func(m *meta) { m.Sealed.Signed = nil })
			}
			want := map[bool]string{false: "invalid ", true: "unsigned "}[strip]
			if got := keysIn(t, b, readers); got != want {
				t.Fatalf("keys after the group was planted = %s, want %s", got, want)
			}
			w := openIdentity(t, b, fay)
			group, err := w.AdoptKeys(ctx, readers)
			if err != nil || group == "" || group == planted {
				t.Fatalf("AdoptKeys = %q, %v", group, err)
			}
			if got := keysIn(t, b, readers); got != "verified fay" {
				t.Fatalf("keys after the adoption = %s", got)
			}
			mustSet(t, w, node("after"))
			if got := groupOf(t, w, b, "t.after"); got != group {
				t.Fatalf("a record written after the adoption is sealed in %s, want %s", got, group)
			}
			// What was sealed before still opens, and nothing is adopted twice.
			if r := w.Get("db.main"); r == nil || !bytes.Contains(r.Attributes, []byte(secretValue)) {
				t.Fatal("a record from before the adoption does not open")
			}
			if again, err := w.AdoptKeys(ctx, readers); err != nil || again != "" {
				t.Fatalf("a second AdoptKeys = %q, %v", again, err)
			}
			if _, err := OpenBackendWith(ctx, b, Options{Identity: ada}); err != nil {
				t.Fatalf("the other reader does not open the state: %v", err)
			}
		})
	}
}

// A lock that was replaced by one that holds another key does not open: the group
// committed to its key, and a reader signed the commitment.
func TestALockWithAnotherKeyDoesNotOpen(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
	sealedStore(t, b, ada, fay).Close()
	readers := readersOf(ada, fay)
	editMeta(t, b, func(m *meta) {
		other, _, err := protect.NewGroup(m.Sealed.Current, m.Lineage, readers)
		if err != nil {
			t.Fatal(err)
		}
		m.Sealed.Groups[0].Locks = other.Locks // the commitment and the signature stay
	})
	if got := keysIn(t, b, readers); got != "verified ada" {
		t.Fatalf("keys = %s: the signature covers no lock", got)
	}
	if _, err := OpenBackendWith(ctx, b, Options{Identity: fay}); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("open with a lock that holds another key = %v, want ErrUntrusted", err)
	}
}

// A state that a pravo sealed before lists were signed has no signature and no
// commitment. The first reader that writes gives it a new group, once.
func TestAStateSealedBeforeSignaturesGetsANewKeyOnce(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
	sealedStore(t, b, ada, fay).Close()
	readers := readersOf(ada, fay)
	editMeta(t, b, func(m *meta) {
		m.Sealed.Signed = nil
		for i := range m.Sealed.Groups {
			m.Sealed.Groups[i].Commit = nil
		}
	})
	w := openIdentity(t, b, fay)
	if group, err := w.AdoptKeys(ctx, readers); err != nil || group != "g2" {
		t.Fatalf("AdoptKeys = %q, %v", group, err)
	}
	m := formatMeta(t, b)
	if got := keysIn(t, b, readers); got != "verified fay" || m.Sealed.Current != "g2" {
		t.Fatalf("keys = %s, current = %s", got, m.Sealed.Current)
	}
	for _, g := range m.Sealed.Groups {
		if len(g.Commit) == 0 {
			t.Fatalf("group %s has no commitment after the adoption", g.ID)
		}
	}
	if last := m.History[len(m.History)-1]; last.By != "fay" || last.What == "rewrap" {
		t.Fatalf("history = %+v", last)
	}
	if group, err := openIdentity(t, b, ada).AdoptKeys(ctx, readers); err != nil || group != "" {
		t.Fatalf("AdoptKeys of the next reader = %q, %v", group, err)
	}
}

// A rewrap that adds a reader leaves the list and its signature as they are. One that
// removes the reader who signed makes a new group, which the one who removes signs. One
// that finds a list nobody vouches for makes a new group even when the readers agree.
func TestRewrapKeepsOrMakesTheSignature(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	ada, fay, gil := testIdentity(t, "ada"), testIdentity(t, "fay"), testIdentity(t, "gil")
	s := sealedStore(t, b, ada, fay)
	was := formatMeta(t, b).Sealed.Signed.Sig

	res, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada, fay, gil)})
	if err != nil || res.Change.NewGroup != "" || res.KeysWere != "" {
		t.Fatalf("rewrap that adds gil = %+v, %v", res, err)
	}
	if m := formatMeta(t, b); !bytes.Equal(m.Sealed.Signed.Sig, was) || keysIn(t, b, readersOf(ada, fay, gil)) != "verified ada" {
		t.Fatalf("the signature changed with a reader added: %s", keysIn(t, b, readersOf(ada, fay, gil)))
	}

	f := openIdentity(t, b, fay)
	res, err = f.Rewrap(ctx, RewrapOptions{Readers: readersOf(fay, gil)})
	if err != nil || res.Change.NewGroup != "g2" {
		t.Fatalf("rewrap that removes ada = %+v, %v", res, err)
	}
	if got := keysIn(t, b, readersOf(fay, gil)); got != "verified fay" {
		t.Fatalf("keys after ada was removed = %s", got)
	}

	editMeta(t, b, func(m *meta) { m.Sealed.Signed = nil })
	g := openIdentity(t, b, gil)
	res, err = g.Rewrap(ctx, RewrapOptions{Readers: readersOf(fay, gil)})
	if err != nil || res.Change.NewGroup != "g3" || res.KeysWere != "unsigned" || len(res.Change.Added)+len(res.Change.Removed) != 0 {
		t.Fatalf("rewrap of a list without a signature = %+v, %v", res, err)
	}
	if got := keysIn(t, b, readersOf(fay, gil)); got != "verified gil" {
		t.Fatalf("keys after the rewrap of a list without a signature = %s", got)
	}
}

// An identity without a signing key, as the key of a recovery is, makes no group for the
// sake of a signature it cannot give, and leaves the list it changes without one.
func TestAnIdentityThatCannotSignLeavesTheListUnsigned(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b, _ := NewLocalBackend(t.TempDir())
	ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
	sealedStore(t, b, ada, fay).Close()
	mute := &protect.Identity{Name: "fay", Reader: fay.Reader}
	s := openIdentity(t, b, mute)
	if group, err := s.AdoptKeys(ctx, readersOf(ada)); err != nil || group != "" {
		t.Fatalf("AdoptKeys without a signing key = %q, %v", group, err)
	}
	res, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(fay)})
	if err != nil || res.Change.NewGroup != "g2" || res.KeysWere != "" {
		t.Fatalf("rewrap without a signing key = %+v, %v", res, err)
	}
	if got := keysIn(t, b, readersOf(fay)); got != "unsigned " {
		t.Fatalf("keys after a rewrap without a signing key = %s", got)
	}
}

// A collection that drops a group changes the list. It signs the new list when a reader
// the store trusts signed the old one, and leaves it unsigned when it cannot tell.
func TestGCSignsTheListOnlyWhenAReaderSignedTheOldOne(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	none := func(context.Context) (map[string]int, error) { return nil, nil }
	for _, trusted := range []bool{true, false} {
		t.Run(fmt.Sprintf("trusted %v", trusted), func(t *testing.T) {
			b, _ := NewLocalBackend(t.TempDir())
			ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
			s := sealedStore(t, b, ada, fay)
			if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada), All: true}); err != nil {
				t.Fatal(err)
			}
			s.Close()
			c := openIdentity(t, b, ada)
			if trusted {
				c.TrustReaders(readersOf(ada))
			}
			res, err := c.GC(ctx, GCOptions{Groups: true, Plans: none, Now: ahead(2*sweepGrace() + time.Minute)})
			if err != nil || len(res.Groups) != 1 || !res.Groups[0].Dropped {
				t.Fatalf("GC = %+v, %v", res, err)
			}
			want := map[bool]string{true: "verified ada", false: "unsigned "}[trusted]
			if got := keysIn(t, b, readersOf(ada)); got != want || fmt.Sprint(groupsOf(t, b)) != "[g2]" {
				t.Fatalf("keys after the collection = %s (groups %v), want %s", got, groupsOf(t, b), want)
			}
		})
	}
}

// A command that is running looks at meta.json again and moves to a new current group.
// It does not move to one that nobody it trusts signed: it goes on sealing in the group
// it opened with, and moves once a reader has made a group and signed the list.
func TestARunningCommandDoesNotMoveToAGroupNobodySigned(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	was := sealRecheck
	defer func() { sealRecheck = was }()
	sealRecheck = 0
	for _, strip := range []bool{false, true} {
		t.Run(fmt.Sprintf("signature stripped %v", strip), func(t *testing.T) {
			b, _ := NewLocalBackend(t.TempDir())
			ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
			readers := readersOf(ada, fay)
			long := sealedStore(t, b, ada, fay)
			long.TrustReaders(readers)

			planted := plantGroup(t, b, readers)
			if strip {
				editMeta(t, b, func(m *meta) { m.Sealed.Signed = nil })
			}
			sealed, err := long.Sealer().Seal(protect.KindPlan, "p", []byte("x"))
			if err != nil || sealed.Group != "g1" {
				t.Fatalf("with %s planted, the running command seals in %s (%v), want g1", planted, sealed.Group, err)
			}
			mustSet(t, long, node("during"))
			if got := groupOf(t, long, b, "t.during"); got != "g1" {
				t.Fatalf("a record written with %s planted is sealed in %s, want g1", planted, got)
			}

			// A reader makes a group and signs: the running command moves to it.
			res, err := openIdentity(t, b, fay).Rewrap(ctx, RewrapOptions{Readers: readers})
			if err != nil || res.Change.NewGroup != "g3" {
				t.Fatalf("rewrap = %+v, %v", res, err)
			}
			if sealed, err = long.Sealer().Seal(protect.KindPlan, "q", []byte("x")); err != nil || sealed.Group != "g3" {
				t.Fatalf("after a reader signed, the running command seals in %s (%v), want g3", sealed.Group, err)
			}
		})
	}
}
