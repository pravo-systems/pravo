package state

import (
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// One comparison of a vouch with a transaction serves records, secrets and facts.
func TestVouchesOfAndTheSecretJudgeAgreeWithRecords(t *testing.T) {
	admin := testIdentity(t, "ada")
	readers := rolesFor([]*protect.Identity{admin}, protect.RoleAdmin)
	const lineage = "lineage-1"
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	tx := &Transaction{ID: "t1", Kind: "secret", Started: now, Finished: &now, Status: "complete",
		Changes: []Change{{Addr: "secret.s", Action: "create", After: 1, Record: "x"}}}
	rules := RecordRules{Readers: readers, Vouched: map[string]protect.Vouch{
		"t1": {Key: protect.UnsignedKey, Txn: "t1", Digest: VouchDigest(lineage, tx)},
	}}
	v := rules.VouchesOf(lineage, tx)
	if !v.Unsigned || v.Finish || v.Start || v.Ended {
		t.Fatalf("VouchesOf = %+v, want only Unsigned", v)
	}
	// An unsigned transaction that an admin vouched for is accepted by the secret judge, as it
	// is by the record check (it was a problem there before).
	claim := secretClaim{addr: "secret.s", action: "create", seq: 1, commit: "x"}
	s := &Store{}
	s.meta.Lineage = lineage
	trust, why, _ := s.secretTxn(tx, protect.RoleWriter, claim, rules)
	if trust != TrustVouched {
		t.Errorf("secretTxn of a vouched unsigned transaction = %v (%s), want vouched", trust, why)
	}
	rules.Vouched = nil
	if trust, _, _ := s.secretTxn(tx, protect.RoleWriter, claim, rules); trust != TrustProblem {
		t.Errorf("secretTxn of an unsigned transaction nobody vouched for = %v, want a problem", trust)
	}
}

// A transaction whose start and end are signed by two keys is altered: it explains no record
// that is missing, as for the secrets and the records.
func TestFactsOfAnAlteredTransactionAreNone(t *testing.T) {
	a, b := testIdentity(t, "ada"), testIdentity(t, "bob")
	readers := rolesFor([]*protect.Identity{a, b}, protect.RoleWriter, protect.RoleWriter)
	const lineage = "lineage-1"
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	tx := &Transaction{ID: "t1", Kind: "apply", Started: now, Finished: &now, Status: "complete",
		Changes: []Change{{Addr: "a", Action: "delete", Before: 3}}}
	start, err := a.SignMessage(protect.DomainTxnStart, lineage, txnStartParts(tx)...)
	if err != nil {
		t.Fatal(err)
	}
	finish, err := b.SignMessage(protect.DomainTxnFinish, lineage, txnFinishParts(tx)...)
	if err != nil {
		t.Fatal(err)
	}
	tx.StartSig, tx.FinishSig = &start, &finish
	if f := factsOf(tx, readers, lineage); len(f.nodes) != 0 {
		t.Errorf("an altered transaction vouches for %d changes, want none", len(f.nodes))
	}
}
