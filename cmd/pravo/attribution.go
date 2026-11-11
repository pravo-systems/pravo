package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/pravo"
)

// Attribution is shown where transactions and plans are: a signature is checked against
// the readers file of the repository. Without that file nothing is checked, and the
// listings say so, since every signature would otherwise read as a key that is not listed.

// attribution returns the readers a signature is checked against, and whether there are any
// to check against. They are the readers that an apply verifies with: the file's keys with
// the roles that decide for st, so that a key an admin removed in the roles of the state is
// not "verified" in a listing while an apply refuses it. A roles chain that does not verify
// leaves the file's own readers, since a listing still says who signed.
func (rf readersFile) attribution(st *state.Store) ([]pravo.Reader, bool) {
	// (see notChecked for what to say when this is false)
	if !rf.Found || rf.Err != nil {
		return nil, false
	}
	if eff, _, err := rf.withRoles(st); err == nil {
		return eff.Readers, true
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

func countSignatures(lineage string, txns []state.Transaction, readers []pravo.Reader) sigCounts {
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
func storedPlanSignatures(ctx context.Context, rt *runContext, s *session, id string) ([]engine.PlanVerdict, string) {
	if s.state.Protection().Sealed == nil {
		return nil, ""
	}
	root, err := loadRoot(rt)
	if err != nil {
		return nil, ""
	}
	rf := readersOf(rt, root)
	readers, ok := rf.attribution(s.state)
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

// storedPlanApprovals says who approved a stored plan and what the policies in force ask
// of it. approved is empty when nobody did or the readers file cannot say; needs has a line
// for each policy that asks, with how many counted approvals the plan has. An approval that
// can never count under a policy is marked in approved, with the reason.
func storedPlanApprovals(ctx context.Context, rt *runContext, s *session, eng *engine.Engine, pf *engine.PlanFile, id string) (approved string, needs []string) {
	if s.state.Protection().Sealed == nil {
		return "", nil
	}
	root, err := loadRoot(rt)
	if err != nil {
		return "", nil
	}
	readers, ok := readersOf(rt, root).attribution(s.state)
	if !ok {
		return "", nil
	}
	ps := engine.NewPlanStoreFor(s.state)
	digest, err := ps.PlanDigest(ctx, id)
	if err != nil {
		return "", nil
	}
	approvals, err := ps.Approvals(ctx, id, digest, readers)
	if err != nil {
		return "", nil
	}
	var asked []engine.ApprovalNeed
	var storers []string
	if eng != nil && pf != nil {
		if set, err := s.state.PolicySet(ctx, readers); err == nil {
			if ns, refused, err := eng.ApprovalNeeds(set, pf); err == nil && !refused {
				asked = ns
			}
		}
		if sigs, err := ps.SignaturesOf(ctx, id, digest, readers); err == nil {
			for _, v := range sigs {
				if v.Status == protect.SigVerified {
					storers = append(storers, v.Key)
				}
			}
		}
	}
	for _, n := range asked {
		have := len(n.Counted(approvals, storers))
		line := fmt.Sprintf("needs %s; has %d", n.Ask(), have)
		if have >= n.Min {
			line += " (met)"
		} else {
			line += " (not met; approve with pravo plan approve " + id + ")"
		}
		needs = append(needs, line)
	}
	var parts []string
	for _, a := range approvals {
		switch a.Status {
		case protect.SigVerified:
			part := fmt.Sprintf("%s (%s)", safeName(a.Signer), a.Role)
			for _, n := range asked {
				if why := n.Why(a, storers); why != "" {
					part = fmt.Sprintf("%s (%s; does not count: %s)", safeName(a.Signer), a.Role, printable(why))
					break
				}
			}
			parts = append(parts, part)
		case protect.SigUnknownKey:
			parts = append(parts, safeName(a.Claimed)+" (key not in the readers file, does not count)")
		default:
			parts = append(parts, "an approval that does not verify")
		}
	}
	return strings.Join(parts, ", "), needs
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
