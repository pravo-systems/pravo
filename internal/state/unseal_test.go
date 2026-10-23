package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// unsealed opens the store with no identity and says whether it holds the secret in
// the open.
// fastRecheck makes a store look at meta.json at once, so that an unseal does not wait.
func fastRecheck(t *testing.T) {
	t.Helper()
	old := metaRecheck
	metaRecheck = time.Millisecond
	t.Cleanup(func() { metaRecheck = old })
}

func holdsSecret(t *testing.T, b Backend) bool {
	t.Helper()
	for _, data := range allObjects(t, b) {
		if bytes.Contains(data, []byte(secretValue)) {
			return true
		}
	}
	return false
}

// A state that is unsealed is the same state: the format stays, the records read the
// same without a key, and the secret is in the bucket in the open.
func TestUnsealReturnsTheStateWithoutAKey(t *testing.T) {
	skipWhenSealed(t)
	fastRecheck(t)
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
			s := sealedStore(t, b, ada, dan)
			want, wantOut := listJSON(t, s), outputsJSON(s.Outputs())
			assertNoSecret(t, b, secretValue)
			res, err := s.Unseal(ctx, UnsealOptions{Identity: ada})
			if err != nil {
				t.Fatal(err)
			}
			if res.Records == 0 || res.Resumed {
				t.Errorf("result %+v", res)
			}
			if p := s.Protection(); p.State != "immutable" || p.Format != FormatSealed || p.Sealed != nil {
				t.Errorf("protection after the unseal: %+v", p)
			}
			// A new process with no identity reads everything.
			plain, err := OpenBackendWith(ctx, b, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if plain.Locked() || listJSON(t, plain) != want || outputsJSON(plain.Outputs()) != wantOut {
				t.Errorf("the state differs after the unseal (locked %v)", plain.Locked())
			}
			if !holdsSecret(t, b) {
				t.Error("the secret is not in the bucket in the open")
			}
			m := formatMeta(t, b)
			if m.Sealed != nil || m.Migration != nil || m.Format != FormatSealed || m.GroupsUsed != 1 {
				t.Errorf("meta after the unseal: %+v", m)
			}
			if last := m.History[len(m.History)-1]; last.What != "unsealed" || last.By != "ada" {
				t.Errorf("history %+v", m.History)
			}
			if st, err := plain.VerifyAll(ctx); err != nil || len(st.Bad) != 0 {
				t.Errorf("verify: %+v %v", st, err)
			}
			// No sealed record is left in the bucket.
			for k, data := range allObjects(t, b) {
				if strings.HasPrefix(k, "rec/") && bytes.Contains(data, []byte(`"sealed"`)) {
					t.Errorf("%s is still sealed", k)
				}
			}
			// It writes again, and the state is not sealed.
			again := *plain.Get("net.vpc")
			again.Attributes = json.RawMessage(`{"id":"vpc-2"}`)
			mustSet(t, plain, &again)
			if _, err := s.Unseal(ctx, UnsealOptions{Identity: ada}); !errors.Is(err, ErrNotSealed) {
				t.Errorf("second unseal: %v", err)
			}
		})
	}
}

// Sealed again, the groups carry on from the number the state reached.
func TestASealAfterAnUnsealDoesNotReuseAGroup(t *testing.T) {
	skipWhenSealed(t)
	fastRecheck(t)
	b := testBackends()[0].make(t)
	ctx := context.Background()
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	s := sealedStore(t, b, ada, dan)
	if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada)}); err != nil { // dan leaves: g2
		t.Fatal(err)
	}
	if _, err := s.Unseal(ctx, UnsealOptions{Identity: ada}); err != nil {
		t.Fatal(err)
	}
	want := listJSON(t, s)
	plain := openIdentity(t, b, nil)
	if _, err := plain.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(dan), Identity: dan}); err != nil {
		t.Fatal(err)
	}
	p := plain.Protection()
	if p.State != "sealed" || p.Sealed.Current != "g3" || len(p.Sealed.Groups) != 1 {
		t.Fatalf("protection %+v", p.Sealed)
	}
	if got := listJSON(t, openIdentity(t, b, dan)); got != want {
		t.Errorf("the state differs after sealing again")
	}
	assertNoSecret(t, b, secretValue)
}

// An unseal that stops leaves the state sealed, refuses writers, and is finished by
// running it again.
func TestUnsealResumes(t *testing.T) {
	skipWhenSealed(t)
	fastRecheck(t)
	b := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	want := listJSON(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	_, err := s.Unseal(ctx, UnsealOptions{Identity: ada, Progress: func(done, total int) {
		if calls.Add(1) == 1 {
			cancel()
		}
	}})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted unseal: %v", err)
	}
	p := s.Protection()
	if p.State != "unsealing" || p.Migration == nil || !p.Migration.Unseal {
		t.Fatalf("protection %+v", p)
	}
	// Writers are refused, by this store and by another.
	other := openIdentity(t, b, ada)
	if err := other.writable(); !errors.Is(err, ErrUnsealing) || !errors.Is(err, ErrSealing) {
		t.Errorf("writable = %v", err)
	}
	// A seal cannot start over it.
	if _, err := other.Upgrade(context.Background(), UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}); err == nil || !strings.Contains(err.Error(), "unseal is in progress") {
		t.Errorf("seal over an unseal: %v", err)
	}
	// A reader still reads it, whether a record is sealed or not.
	if got := listJSON(t, other); got != want {
		t.Errorf("the half unsealed state reads differently")
	}
	res, err := openIdentity(t, b, ada).Unseal(context.Background(), UnsealOptions{Identity: ada})
	if err != nil || !res.Resumed {
		t.Fatalf("resume = %+v, %v", res, err)
	}
	plain, err := OpenBackendWith(context.Background(), b, Options{})
	if err != nil || listJSON(t, plain) != want || plain.Protection().State != "immutable" {
		t.Errorf("after the resume: %v %v", err, plain.Protection().State)
	}
}

// A key that cannot open every group cannot finish, and nothing is written.
func TestUnsealRefusesGroupsItCannotOpen(t *testing.T) {
	skipWhenSealed(t)
	fastRecheck(t)
	b := testBackends()[0].make(t)
	ctx := context.Background()
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	s := sealedStore(t, b, ada, dan)
	// Dan's lock is gone from g1, as if he had been added after the records were sealed
	// and the old group were never opened for him.
	m := formatMeta(t, b)
	g := &m.Sealed.Groups[0]
	for i, l := range g.Locks {
		if l.Key.Equal(dan.Reader.Public()) {
			g.Locks = append(g.Locks[:i], g.Locks[i+1:]...)
			break
		}
	}
	data, _ := json.Marshal(m)
	if err := b.Put(ctx, "meta.json", data); err != nil {
		t.Fatal(err)
	}
	// Dan can still open the current group g1? It is the only one: he cannot open anything,
	// so the store is locked to him.
	locked := openIdentity(t, b, dan)
	if !locked.Locked() {
		t.Fatal("dan opens a group he has no lock in")
	}
	before, _ := b.Get(ctx, "meta.json")
	if _, err := locked.Unseal(ctx, UnsealOptions{Identity: dan}); !errors.Is(err, ErrNotReader) {
		t.Fatalf("Unseal = %v", err)
	}
	after, _ := b.Get(ctx, "meta.json")
	if !bytes.Equal(before, after) {
		t.Error("meta.json was written")
	}
	_ = s
}

// Unsealing needs a reader.
func TestUnsealNeedsAReader(t *testing.T) {
	skipWhenSealed(t)
	fastRecheck(t)
	b := testBackends()[0].make(t)
	ada, eve := testIdentity(t, "ada"), testIdentity(t, "eve")
	sealedStore(t, b, ada)
	locked := openIdentity(t, b, eve)
	if _, err := locked.Unseal(context.Background(), UnsealOptions{Identity: eve}); !errors.Is(err, ErrNotReader) {
		t.Errorf("a stranger unseals: %v", err)
	}
	if _, err := locked.Unseal(context.Background(), UnsealOptions{}); !errors.Is(err, ErrNotReader) {
		t.Errorf("no identity: %v", err)
	}
	if formatMeta(t, b).Sealed == nil {
		t.Error("the seal came off")
	}
	_ = protect.RecoveryName
}

// A sealed record that is young is not kept: the key goes with the next write.
func TestUnsealLeavesNoSealedRecordBehind(t *testing.T) {
	skipWhenSealed(t)
	fastRecheck(t)
	b := testBackends()[0].make(t)
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	// A new revision leaves the old one unnamed, and young.
	again := *s.Get("db.main")
	again.Attributes = json.RawMessage(`{"id":"db-1","password":"` + secretValue + `","rev":3}`)
	mustSet(t, s, &again)
	if _, err := s.Unseal(ctx, UnsealOptions{Identity: ada}); err != nil {
		t.Fatal(err)
	}
	for k, data := range allObjects(t, b) {
		if strings.HasPrefix(k, "rec/") && bytes.Contains(data, []byte(`"sealed"`)) {
			t.Errorf("%s is still sealed", k)
		}
	}
}

// The text of the error of a transaction that failed comes back in the open.
func TestUnsealRestoresTheTextOfErrors(t *testing.T) {
	skipWhenSealed(t)
	fastRecheck(t)
	b := testBackends()[0].make(t)
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	tx := s.Begin("apply", "pravo apply")
	if err := tx.Start(); err != nil {
		t.Fatal(err)
	}
	tx.Record("net.vpc", "update", 1, 2)
	if err := tx.FinishError("failed", "the apply failed", "provider said: "+secretValue, true); err != nil {
		t.Fatal(err)
	}
	text := func() string {
		txns, _ := s.Transactions()
		return txns[0].Summary
	}
	if got := text(); got != "the apply failed" {
		t.Fatalf("summary while sealed: %q", got)
	}
	if _, err := s.Unseal(ctx, UnsealOptions{Identity: ada}); err != nil {
		t.Fatal(err)
	}
	if got := text(); !strings.Contains(got, secretValue) {
		t.Errorf("summary after the unseal: %q", got)
	}
	for _, data := range allObjects(t, b) {
		if bytes.Contains(data, []byte("summary_sealed")) {
			t.Error("a sealed summary is left")
		}
	}
}

// A stored plan that an apply uses holds the unseal: the state stays sealed and
// unsealing, and the command can be run again.
func TestUnsealHoldsForAPlanInUse(t *testing.T) {
	skipWhenSealed(t)
	fastRecheck(t)
	b := testBackends()[0].make(t)
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	busy := 1
	plans := func(context.Context) (int, int, error) { return 0, busy, nil }
	_, err := s.Unseal(ctx, UnsealOptions{Identity: ada, Plans: plans})
	if !errors.Is(err, ErrPlansInUse) {
		t.Fatalf("Unseal = %v", err)
	}
	if p := s.Protection(); p.State != "unsealing" || p.Sealed == nil {
		t.Fatalf("protection %+v", p)
	}
	busy = 0
	if _, err := s.Unseal(ctx, UnsealOptions{Identity: ada, Plans: plans}); err != nil {
		t.Fatal(err)
	}
	if s.Protection().State != "immutable" {
		t.Error("not unsealed")
	}
}

// Something sealed in a group the key cannot open is found before anything is written,
// whether it is a record, the outputs or the plans.
func TestUnsealRefusesWhatItCannotOpenBeforeItWrites(t *testing.T) {
	skipWhenSealed(t)
	fastRecheck(t)
	b := testBackends()[0].make(t)
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	before, _ := b.Get(ctx, "meta.json")
	_, err := s.Unseal(ctx, UnsealOptions{Identity: ada, PlanGroups: func(context.Context) (map[string]int, error) {
		return map[string]int{"g9": 2}, nil
	}})
	if !errors.Is(err, ErrNotReader) || !strings.Contains(err.Error(), "2 in group g9") {
		t.Fatalf("Unseal = %v", err)
	}
	after, _ := b.Get(ctx, "meta.json")
	if !bytes.Equal(before, after) {
		t.Error("meta.json was written")
	}
}

// A writer that last looked at meta.json before the mark is refused, or its record is
// converted: the secret never comes back sealed after the flip.
func TestUnsealIsNotUndoneByAStaleWriter(t *testing.T) {
	skipWhenSealed(t)
	b := testBackends()[0].make(t)
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	stale := openIdentity(t, b, ada)
	again := *stale.Get("net.vpc")
	again.Attributes = json.RawMessage(`{"id":"vpc-2"}`)
	mustSet(t, stale, &again) // its view of meta.json is fresh now
	if _, err := s.Unseal(ctx, UnsealOptions{Identity: ada}); err != nil {
		t.Fatal(err)
	}
	again.Attributes = json.RawMessage(`{"id":"vpc-3"}`)
	if err := stale.Set(&again, ""); err == nil {
		// The write was allowed: it must not have left a sealed record behind.
		plain, err := OpenBackendWith(ctx, b, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if got := plain.Get("net.vpc"); got == nil || !strings.Contains(string(got.Attributes), "vpc") {
			t.Errorf("net.vpc reads %+v", got)
		}
	}
}

// A store opened locked and unsealed with an identity reads the attributes afterwards.
func TestUnsealOfALockedStoreLoadsTheValues(t *testing.T) {
	skipWhenSealed(t)
	fastRecheck(t)
	b := testBackends()[0].make(t)
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	sealedStore(t, b, ada)
	locked, err := OpenBackendWith(ctx, b, Options{})
	if err != nil || !locked.Locked() {
		t.Fatalf("open: %v locked %v", err, locked.Locked())
	}
	if _, err := locked.Unseal(ctx, UnsealOptions{Identity: ada}); err != nil {
		t.Fatal(err)
	}
	if got := locked.Get("net.vpc"); got == nil || len(got.Attributes) == 0 {
		t.Errorf("net.vpc after the unseal: %+v", got)
	}
}

// A sealed record in a state that says it is not sealed is listed, so that a reader sees
// that something is wrong, and nothing is written over it.
func TestAStateThatIsNotSealedDoesNotWriteOverASealedRecord(t *testing.T) {
	skipWhenSealed(t)
	fastRecheck(t)
	b := testBackends()[0].make(t)
	ctx := context.Background()
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	ptr := pointerAt(t, b, "net.vpc")
	sealedRec, err := b.Get(ctx, ptr.Record)
	if err != nil {
		t.Fatal(err)
	}
	oldPtr, _ := b.Get(ctx, nodeKey("net.vpc"))
	if _, err := s.Unseal(ctx, UnsealOptions{Identity: ada}); err != nil {
		t.Fatal(err)
	}
	// The sealed record and its pointer come back, as a thief who restored them would.
	if err := b.Put(ctx, ptr.Record, sealedRec); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, nodeKey("net.vpc"), oldPtr); err != nil {
		t.Fatal(err)
	}
	plain, err := OpenBackendWith(ctx, b, Options{})
	if err != nil {
		t.Fatalf("a reader cannot see the state: %v", err)
	}
	if p := plain.Protection(); fmt.Sprint(p.StraySealed) != "[net.vpc]" {
		t.Errorf("stray %v", p.StraySealed)
	}
	again := *plain.Get("db.main")
	again.Attributes = json.RawMessage(`{"id":"db-2"}`)
	if err := plain.Set(&again, ""); !errors.Is(err, ErrUntrusted) {
		t.Errorf("Set = %v, want ErrUntrusted", err)
	}
}
