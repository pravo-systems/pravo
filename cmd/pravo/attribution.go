package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// Attribution is shown where transactions and plans are: a signature is checked against
// the readers file of the repository. Without that file nothing is checked, and the
// listings say so, since every signature would otherwise read as a key that is not listed.

// attributionReaders returns the readers a signature is checked against, and whether
// there are any to check against.
func attributionReaders(rf readersFile) ([]protect.Reader, bool) {
	// (see notChecked for what to say when this is false)
	if !rf.Found || rf.Err != nil {
		return nil, false
	}
	return rf.Readers, true
}

// sigCounts count the transactions of a state by what became of their signatures.
type sigCounts struct {
	Verified   int      `json:"verified"`
	UnknownKey int      `json:"key_not_in_file"`
	Unsigned   int      `json:"unsigned"`
	Invalid    int      `json:"invalid"`
	Bad        []string `json:"invalid_transactions,omitempty"`
}

func countSignatures(lineage string, txns []state.Transaction, readers []protect.Reader) sigCounts {
	var c sigCounts
	for i := range txns {
		switch v := state.VerifyTransaction(lineage, &txns[i], readers); v.Overall() {
		case protect.SigVerified:
			c.Verified++
		case protect.SigUnknownKey:
			c.UnknownKey++
		case protect.SigInvalid:
			c.Invalid++
			c.Bad = append(c.Bad, txns[i].ID)
		default:
			c.Unsigned++
		}
	}
	return c
}

// text is the Signatures line of protect status.
func (c sigCounts) text(total int) string {
	line := fmt.Sprintf("%d of %s verified", c.Verified, plural(total, "transaction", "transactions"))
	if c.UnknownKey > 0 {
		line += fmt.Sprintf(" · %d signed with a key that is not in the file", c.UnknownKey)
	}
	if c.Unsigned > 0 {
		line += fmt.Sprintf(" · %d unsigned", c.Unsigned)
	}
	if c.Invalid > 0 {
		line += fmt.Sprintf(" · %d with a signature that does not verify", c.Invalid)
	}
	return line
}

// txnSigText is the line state log shows under a transaction of a sealed state.
func txnSigText(v state.TxnVerdict) string {
	switch v.Overall() {
	case protect.SigVerified:
		who := v.Signer
		if who == "" {
			who = v.Claimed
		}
		if v.Finished && v.Finish == protect.SigVerified {
			return "signed by " + safeName(who) + " (verified)"
		}
		return "start signed by " + safeName(who) + " (verified)"
	case protect.SigUnknownKey:
		return "signed by " + safeName(v.Claimed) + ", whose key is not in the readers file"
	case protect.SigInvalid:
		return "the signature does not verify: the transaction was changed"
	}
	return "unsigned"
}

// sigStatusJSON is the signature of a transaction as state log --json shows it.
type sigStatusJSON struct {
	Status  string `json:"status"`
	Signer  string `json:"signer,omitempty"`
	Claimed string `json:"claimed,omitempty"`
}

func sigJSON(v state.TxnVerdict) *sigStatusJSON {
	s := &sigStatusJSON{Status: v.Overall().Key()}
	switch v.Overall() {
	case protect.SigVerified:
		s.Signer = v.Signer
	case protect.SigUnknownKey, protect.SigInvalid:
		s.Claimed = v.Claimed
	}
	return s
}

// invalidTxns is the error of a listing that held transactions whose signatures do not
// verify: the state failed verification, exit code 7.
func invalidTxns(n int) error {
	if n == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", state.ErrUntrusted, plural(n, "transaction has a signature that does not verify", "transactions have a signature that does not verify"))
}

// storedPlanSignatures checks the signatures of a stored plan. The note is set instead
// when they cannot be checked, or when the state is not sealed and has none.
func storedPlanSignatures(ctx context.Context, s *session, id string) ([]engine.PlanVerdict, string) {
	if s.state.Protection().Sealed == nil {
		return nil, ""
	}
	root, err := loadRoot()
	if err != nil {
		return nil, ""
	}
	rf := loadReadersFile(root.Dir, flags.AccessDir)
	readers, ok := attributionReaders(rf)
	if !ok {
		return nil, notChecked(rf)
	}
	sigs, err := engine.NewPlanStoreFor(s.state).Signatures(ctx, id, readers)
	switch {
	case errors.Is(err, state.ErrNotReader):
		return nil, "not checked: this identity cannot open the plan"
	case err != nil:
		return nil, "not checked: " + printable(err.Error())
	case len(sigs) == 0:
		return nil, "unsigned"
	}
	return sigs, ""
}

func planSigsText(sigs []engine.PlanVerdict) string {
	var parts []string
	for _, v := range sigs {
		who := v.Signer
		if who == "" {
			who = v.Claimed
		}
		switch v.Status {
		case protect.SigVerified:
			parts = append(parts, safeName(who)+" (verified)")
		case protect.SigUnknownKey:
			parts = append(parts, safeName(who)+" (key not in the readers file)")
		default:
			parts = append(parts, "a signature that does not verify")
		}
	}
	return strings.Join(parts, ", ")
}

func planSigsJSON(sigs []engine.PlanVerdict, note string) any {
	out := []map[string]any{}
	for _, v := range sigs {
		m := map[string]any{"status": v.Status.Key()}
		if v.Signer != "" {
			m["signer"] = v.Signer
		} else if v.Claimed != "" {
			m["claimed"] = v.Claimed
		}
		if !v.At.IsZero() {
			m["at"] = v.At
		}
		out = append(out, m)
	}
	doc := map[string]any{"signatures": out}
	if note != "" {
		doc["note"] = note
	}
	return doc
}

// notChecked says why the signatures are not checked: the readers file is missing or invalid.
func notChecked(rf readersFile) string {
	if rf.Found && rf.Err != nil {
		return "not checked: the readers file is invalid"
	}
	return "not checked: the readers file is needed to check them"
}
