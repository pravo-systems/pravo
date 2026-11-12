package state

import (
	"testing"
	"time"
)

func finishKind(t *testing.T, s *Store, kind string) {
	t.Helper()
	txn := s.Begin(kind, "test "+kind)
	if err := txn.Start(); err != nil {
		t.Fatal(err)
	}
	if err := txn.Finish("applied", kind, true); err != nil {
		t.Fatal(err)
	}
}

// The owner is the newest transaction of kind shadow or handover, in a sealed state too, and
// a handover that nobody signed does not count.
func TestShadowOwnerIsTheNewestTransaction(t *testing.T) {
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			admin := testIdentity(t, "admin")
			readers := readersOf(admin)
			s := sealedStore(t, bk.make(t), admin)
			owner := func() string {
				t.Helper()
				o, _, err := s.ShadowOwner(readers)
				if err != nil {
					t.Fatal(err)
				}
				return o
			}
			if got := owner(); got != "" {
				t.Fatalf("owner of a state no shadow wrote = %q", got)
			}
			finishKind(t, s, KindShadow)
			if got := owner(); got != OwnerTerraform {
				t.Fatalf("owner after a shadow = %q", got)
			}
			finishKind(t, s, KindHandover)
			if got := owner(); got != OwnerPravo {
				t.Fatalf("owner after a handover = %q", got)
			}
			// A handover that none of the readers signed is not one.
			other := readersOf(testIdentity(t, "stranger"))
			s.forgetFacts()
			if o, _, err := s.ShadowOwner(other); err != nil || o != OwnerTerraform {
				t.Fatalf("a handover a stranger judged = %q, %v", o, err)
			}
			finishKind(t, s, KindShadow)
			if got := owner(); got != OwnerTerraform {
				t.Fatalf("owner after a second shadow = %q", got)
			}
		})
	}
}

// Transactions begun one after the other never share a start, whatever the clock's grain:
// the newest of them is the one with the latest start, and a tie left it to a random id.
func TestTransactionsStartInOrder(t *testing.T) {
	s := sealedStore(t, testBackends()[0].make(t), testIdentity(t, "admin"))
	var last time.Time
	for i := range 5000 {
		at := s.Begin("test", "t").t.Started
		if !at.After(last) {
			t.Fatalf("transaction %d started at %v, not after %v", i, at, last)
		}
		last = at
	}
}
