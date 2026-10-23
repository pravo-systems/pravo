package state

import (
	"fmt"
	"strconv"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// Attribution. In a sealed state every writer is a reader, with an identity that is
// open, and the transactions it records are signed with the signing key of that
// identity: once when the run starts, for what it set out to do, and once when it
// finishes, for what it did. A signature covers the event and not the bodies of the
// records: the address, the action and the revisions of each change, but not the
// values, which sealing, unsealing and a rewrap rewrite. The text of the error of a
// failed run is left out for the same reason.
//
// A signature is trusted when its key is in the readers file of the repository. It
// does not say that nothing was deleted, replaced by an older copy or reordered:
// that takes a ledger.

// txnStartParts are the fields the first signature covers.
func txnStartParts(t *Transaction) [][]byte {
	return [][]byte{
		[]byte(t.ID), []byte(t.Kind), []byte(t.Command), []byte(t.User), []byte(t.Host),
		[]byte(t.Started.UTC().Format(time.RFC3339Nano)),
		[]byte(t.Actor), []byte(t.Reason), []byte(t.GitRevision), []byte(t.Version),
		[]byte(t.PlanID), []byte(t.PlanDigest),
	}
}

// txnFinishParts are the fields the second signature covers: the first's, and the
// outcome.
func txnFinishParts(t *Transaction) [][]byte {
	parts := txnStartParts(t)
	finished := ""
	if t.Finished != nil {
		finished = t.Finished.UTC().Format(time.RFC3339Nano)
	}
	parts = append(parts, []byte(finished), []byte(t.Status), []byte(strconv.Itoa(len(t.Changes))))
	for _, c := range t.Changes {
		parts = append(parts, []byte(c.Addr), []byte(c.Action), []byte(strconv.Itoa(c.Before)), []byte(strconv.Itoa(c.After)))
	}
	return parts
}

// identity is the identity that signs for the store: the one it was opened with, in a
// sealed state. A state that is not sealed has none.
func (s *Store) signingIdentity() *protect.Identity {
	if !s.sealedState() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// SetPlan records the plan an apply runs from: the id of a stored plan, if it was one,
// and the digest of the plan file. It is called before Start.
func (t *Txn) SetPlan(id, digest string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.t.PlanID, t.t.PlanDigest = id, digest
}

// signStart signs the start of the transaction, in a sealed state.
func (t *Txn) signStart() error {
	if !t.store.sealedState() {
		return nil
	}
	id := t.store.signingIdentity()
	if id == nil {
		return fmt.Errorf("the state is sealed and this process has no identity to sign with: %w", ErrNotReader)
	}
	sig, err := id.SignMessage(protect.DomainTxnStart, t.store.Lineage(), txnStartParts(&t.t)...)
	if err != nil {
		return fmt.Errorf("signing the transaction: %w", err)
	}
	t.t.StartSig = &sig
	return nil
}

// signFinish signs the end of the transaction, and the start if a run that did not
// call Start reaches here. A failure leaves the transaction without a finish signature.
func (t *Txn) signFinish() error {
	t.t.FinishSig = nil
	if !t.store.sealedState() {
		return nil
	}
	id := t.store.signingIdentity()
	if id == nil {
		return fmt.Errorf("the state is sealed and this process has no identity to sign with: %w", ErrNotReader)
	}
	lineage := t.store.Lineage()
	if t.t.StartSig == nil {
		sig, err := id.SignMessage(protect.DomainTxnStart, lineage, txnStartParts(&t.t)...)
		if err != nil {
			return fmt.Errorf("the transaction %s was recorded without a signature: %w", t.t.ID, err)
		}
		t.t.StartSig = &sig
	}
	sig, err := id.SignMessage(protect.DomainTxnFinish, lineage, txnFinishParts(&t.t)...)
	if err != nil {
		return fmt.Errorf("the transaction %s was recorded without the signature of its end: %w", t.t.ID, err)
	}
	t.t.FinishSig = &sig
	return nil
}

// TxnVerdict is what a verifier makes of the signatures of one transaction.
type TxnVerdict struct {
	Start, Finish protect.SigStatus
	// Signer is the name the readers file gives the key that signed, when a signature
	// verified; Claimed is the name the signature gives itself.
	Signer, Claimed string
	// Finished is set when the transaction records an end.
	Finished bool
}

// Overall is the one status a listing shows: the worst of the signatures that the
// transaction should have. A transaction that is still running is judged by its start; one
// that has finished is judged by both, and a finish with no signature is unsigned, since
// the status and the changes it records are not covered by the start.
func (v TxnVerdict) Overall() protect.SigStatus {
	rank := func(s protect.SigStatus) int {
		switch s {
		case protect.SigInvalid:
			return 3
		case protect.SigUnknownKey:
			return 2
		case protect.SigUnsigned:
			return 1
		}
		return 0
	}
	worst := v.Start
	if v.Finished && rank(v.Finish) > rank(worst) {
		worst = v.Finish
	}
	return worst
}

// VerifyTransaction checks the signatures of t against the readers of the state.
func VerifyTransaction(lineage string, t *Transaction, readers []protect.Reader) TxnVerdict {
	v := TxnVerdict{Finished: t.Finished != nil || t.Status != "running"}
	if t.StartSig != nil {
		v.Claimed = t.StartSig.Signer
	} else if t.FinishSig != nil {
		v.Claimed = t.FinishSig.Signer
	}
	v.Start, v.Signer = protect.VerifySignature(t.StartSig, readers, protect.DomainTxnStart, lineage, txnStartParts(t)...)
	var name string
	v.Finish, name = protect.VerifySignature(t.FinishSig, readers, protect.DomainTxnFinish, lineage, txnFinishParts(t)...)
	if v.Signer == "" {
		v.Signer = name
	}
	return v
}

// SignMessage signs a message of the domain, the lineage of the state and the parts
// with the identity the store was opened with. The second result is false for a state
// that is not sealed, which signs nothing. A signature that cannot be made is an error,
// and the caller refuses what it was about to write.
func (s *Store) SignMessage(domain string, parts ...[]byte) (protect.Signature, bool, error) {
	if !s.sealedState() {
		return protect.Signature{}, false, nil
	}
	id := s.signingIdentity()
	if id == nil {
		return protect.Signature{}, false, fmt.Errorf("the state is sealed and this process has no identity to sign with: %w", ErrNotReader)
	}
	sig, err := id.SignMessage(domain, s.Lineage(), parts...)
	if err != nil {
		return protect.Signature{}, false, fmt.Errorf("signing: %w", err)
	}
	return sig, true, nil
}
