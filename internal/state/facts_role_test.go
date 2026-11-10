package state

import (
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// A finish signature explains a missing record only when a key that may write made it:
// a reader holds what the bucket needs to write a transaction, and its signature is
// valid, but it vouches for nothing.
func TestFactsTakeOnlyAWritersFinishSignature(t *testing.T) {
	writer, reader := testIdentity(t, "wendy"), testIdentity(t, "rhea")
	readers := rolesFor([]*protect.Identity{writer, reader}, protect.RoleWriter, protect.RoleReader)
	const lineage = "lineage-1"
	signed := func(id *protect.Identity) *Transaction {
		now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		tx := &Transaction{ID: "t1", Kind: "apply", Started: now, Finished: &now, Status: "complete",
			Changes: []Change{{Addr: "a", Action: "delete", Before: 3}}}
		sig, err := id.SignMessage(protect.DomainTxnFinish, lineage, txnFinishParts(tx)...)
		if err != nil {
			t.Fatal(err)
		}
		tx.FinishSig = &sig
		return tx
	}
	if f := factsOf(signed(writer), readers, lineage); len(f.nodes) != 1 {
		t.Errorf("a writer's finish signature vouches for %d changes, want 1", len(f.nodes))
	}
	if f := factsOf(signed(reader), readers, lineage); len(f.nodes) != 0 {
		t.Errorf("a reader's finish signature vouches for %d changes, want none", len(f.nodes))
	}
	// Without roles every key is an admin.
	if f := factsOf(signed(reader), readersOf(writer, reader), lineage); len(f.nodes) != 1 {
		t.Errorf("without roles a key's signature vouches for %d changes, want 1", len(f.nodes))
	}
}
