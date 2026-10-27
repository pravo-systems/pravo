package state

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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

// text is a field as the log stores it. The log is JSON, which replaces each byte that is
// not valid UTF-8 by U+FFFD, so a signature that covered the raw bytes would not verify
// after the transaction was read back. The signature covers the stored form.
func text(s string) []byte {
	if utf8.ValidString(s) {
		return []byte(s)
	}
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(r) // an invalid byte ranges as U+FFFD
	}
	return []byte(b.String())
}

// txnStartParts are the fields the first signature covers.
func txnStartParts(t *Transaction) [][]byte {
	parts := [][]byte{
		text(t.ID), text(t.Kind), text(t.Command), text(t.User), text(t.Host),
		[]byte(t.Started.UTC().Format(time.RFC3339Nano)),
		text(t.Actor), text(t.Reason), text(t.GitRevision), text(t.Version),
		text(t.PlanID), text(t.PlanDigest), text(t.TraceID),
	}
	// The policies the run judged by, one part present only when there are any: the
	// message frames its part count, so a transaction without them verifies as before.
	if len(t.Policies) > 0 {
		parts = append(parts, policyList(t.Policies))
	}
	return parts
}

// txnFinishParts are the fields the second signature covers: the first's, and the
// outcome.
func txnFinishParts(t *Transaction) [][]byte {
	parts := txnStartParts(t)
	finished := ""
	if t.Finished != nil {
		finished = t.Finished.UTC().Format(time.RFC3339Nano)
	}
	parts = append(parts, []byte(finished), text(t.Status), []byte(strconv.Itoa(len(t.Changes))))
	for _, c := range t.Changes {
		parts = append(parts, text(c.Addr), text(c.Action), []byte(strconv.Itoa(c.Before)), []byte(strconv.Itoa(c.After)))
		if c.Record != "" {
			parts = append(parts, text(c.Record))
		}
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

// SetPolicies records the policies the run judged by, as NAME@SEQ:SHA-256 refs. It is
// called before Start.
func (t *Txn) SetPolicies(refs []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.t.Policies = append([]string(nil), refs...)
	sort.Strings(t.t.Policies)
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
	// Altered is set when the transaction is not what a signer could have signed in the
	// state it claims: a run that has not finished carries an outcome, or the two
	// signatures are by different keys.
	Altered bool
}

// Overall is the one status a listing shows: the worst of the signatures that the
// transaction should have. A transaction that is still running is judged by its start,
// and must hold nothing that only the end signs; one that has finished is judged by both,
// and a finish with no signature is unsigned, since the status and the changes it records
// are not covered by the start. Two signatures by different keys are invalid, since a
// transaction is the work of one identity.
func (v TxnVerdict) Overall() protect.SigStatus {
	if v.Altered {
		return protect.SigInvalid
	}
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
	// A signature on the end counts when it is there, finished or not: a run that is
	// running has none.
	if (v.Finished || v.Finish != protect.SigUnsigned) && rank(v.Finish) > rank(worst) {
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
	if !v.Finished && (len(t.Changes) > 0 || t.Summary != "" || t.SummarySealed != nil) {
		v.Altered = true // the start was signed before there was an outcome to sign
	}
	if t.StartSig != nil && t.FinishSig != nil && t.StartSig.Key != t.FinishSig.Key {
		v.Altered = true
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

// SetWarn sets the function that receives a warning that a command is to show its user: the
// end of a transaction that could not be signed. A store without one returns that as an error.
func (s *Store) SetWarn(f func(string)) {
	s.mu.Lock()
	s.warn = f
	s.mu.Unlock()
}

func (s *Store) warnFunc() func(string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.warn
}

// SigningKey is the fingerprint of the signing key of the identity the store was
// opened with, empty for a state that is not sealed or a store with no identity. The
// role of that key in the readers file says what the store may be used for.
func (s *Store) SigningKey() string {
	id := s.signingIdentity()
	if id == nil || len(id.SignPub) == 0 {
		return ""
	}
	return protect.SSHFingerprint(id.SignPub)
}
