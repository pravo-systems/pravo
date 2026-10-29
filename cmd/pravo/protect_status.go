package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

func protectStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the state is sealed, to whom, and whether it verifies",
		Long: `Opens the state with your identity, checks every immutable record against its
name and every pointer against its record, and prints one screen: whether the
state is sealed and to how many readers, whether the readers file matches the
store, who you are in relation to it, and whether anything was left unfinished.
It writes nothing.

It exits with 7 if a record does not match its name.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newProtectRun(cmd)
			if err != nil {
				return err
			}
			return p.status()
		},
	}
}

func (p *protectRun) status() error {
	b, err := p.backend(false)
	if err != nil {
		return err
	}
	st, id, idErr, err := p.open(b, false, true)
	if errors.Is(err, state.ErrUntrusted) {
		return reportUntrusted(err)
	}
	if err != nil {
		return err
	}
	prot := st.Protection()
	rows := [][2]string{}
	add := func(k, v string) { rows = append(rows, [2]string{k, v}) }
	sealed := prot.State == "sealed" || prot.State == "migrating" || prot.State == "unsealing"
	// A state that does not exist yet is read as an empty one of format 1, and
	// nothing is created. What is true of it is that there is none.
	_, metaErr := b.Get(p.ctx, metaKey)
	none := errors.Is(metaErr, state.ErrNotFound) && prot.Records == 0 && !sealed
	switch {
	case none:
		if p.rf.Found {
			add("Sealed", "no state yet · create it sealed with: pravo protect init --encrypt")
		} else {
			add("Sealed", "no state yet")
		}
	case prot.State == "sealed":
		names := protectedNames(prot)
		recov := "recovery none"
		if r := prot.Sealed.Recovery; r != nil {
			recov = fmt.Sprintf("recovery %d of %d", r.K, r.N)
		}
		add("Sealed", fmt.Sprintf("on · format %d · %s (%s) · %s", prot.Format, plural(len(names), "reader", "readers"), nameList(names), recov))
	case prot.State == "migrating":
		done, total := 0, prot.Records
		if m := prot.Migration; m != nil {
			done, total = m.Done, m.Total
		}
		add("Sealed", fmt.Sprintf("migrating · %s of %s records · run pravo protect init --encrypt to finish", commas(done), commas(total)))
	case prot.State == "unsealing":
		done, total := 0, prot.Records
		if m := prot.Migration; m != nil {
			done, total = m.Done, m.Total
		}
		add("Sealed", fmt.Sprintf("unsealing · %s of %s records · run pravo protect unseal to finish", commas(done), commas(total)))
	case prot.State == "immutable":
		line := fmt.Sprintf("off · format %d · immutable records", prot.Format)
		if n := len(prot.StraySealed); n > 0 {
			line += fmt.Sprintf(" · %s sealed, and meta.json says the state is not: it was changed", plural(n, "record is", "records are"))
		}
		add("Sealed", line)
	default:
		add("Sealed", fmt.Sprintf("off · format %d", prot.Format))
	}
	rf := p.rf
	expects := expectsSealed(prot, rf)
	if expects && !none {
		rows[len(rows)-1][1] += " · the repository expects a sealed state"
	}
	add("Store", p.location())
	switch {
	case !rf.Found:
		add("Readers", fmt.Sprintf("file not found (looked in %s)", rf.shownDir()))
	case rf.Err != nil:
		add("Readers", "file is invalid: "+rf.problem())
	case !sealed:
		add("Readers", fmt.Sprintf("%s · %s", rf.shown(), plural(len(rf.Readers), "reader", "readers")))
	default:
		added, removed := readersDiff(prot, rf)
		if len(added) == 0 && len(removed) == 0 {
			add("Readers", rf.shown()+" matches the store")
		} else {
			add("Readers", differsText(added, removed))
		}
	}
	if sealed {
		switch {
		case prot.You.State == "reader":
			add("You", fmt.Sprintf("%s · reader key %s", prot.You.Name, prot.You.Key))
		case id != nil:
			add("You", "identity "+id.Name+" is not a reader")
		case idErr != nil && !errors.Is(idErr, errNoIdentity):
			add("You", "not a reader of this state ("+strings.TrimSuffix(idErr.Error(), ".")+")")
		default:
			add("You", "not a reader of this state (no identity found)")
		}
	}
	recoveryFileState := ""
	if prot.State == "sealed" && (prot.Sealed.Recovery != nil || loadRecoveryFile(rf).Found) {
		var text string
		text, recoveryFileState = recoveryStatusText(prot, rf)
		add("Recovery", text)
	}
	txns, err := st.Transactions()
	if err != nil {
		return err
	}
	erf, rstat, rerr := rf.withRoles(st)
	if rerr != nil {
		return engine.WithCode(engine.ExitUntrusted, fmt.Errorf("roles: %s", printable(rerr.Error())))
	}
	rolesOn := erf.Found && erf.Err == nil && protect.RolesOn(erf.Readers)
	add("Roles", rolesText(erf, rstat, st))
	var recs *state.RecordVerdict
	if rolesOn && prot.Sealed != nil && !st.Locked() {
		v := st.VerifyRecordsWith(txns, recordRules(erf.Readers, rstat))
		recs = &v
		add("Records", recordsText(v))
	}
	// The secrets are judged without a key: what is checked is the clear part of each version
	// and the transactions that wrote it.
	var secs *state.SecretVerdict
	if prot.Sealed != nil {
		v, err := st.VerifySecrets(p.ctx, txns, recordRules(erf.Readers, rstat))
		if err != nil {
			return err
		}
		if v.Secrets > 0 {
			secs = &v
			add("Secrets", secretsText(v))
		}
	}
	var sigs *sigCounts
	if prot.Sealed != nil {
		if readers, ok := attributionReaders(rf); ok {
			c := countSignatures(st.Lineage(), txns, readers)
			sigs = &c
			add("Signatures", c.text(len(txns)))
		} else {
			add("Signatures", notChecked(rf))
		}
	}
	interrupted, err := st.Interrupted(txns)
	if err != nil {
		return err
	}
	if n := len(interrupted); n == 0 {
		add("Unsettled", "none")
	} else {
		add("Unsettled", plural(n, "interrupted transaction", "interrupted transactions")+" (pravo state log)")
	}
	ver, err := st.VerifyAll(p.ctx)
	if err != nil {
		return err
	}
	if flags.JSON {
		doc := map[string]any{"state": prot.State, "format": prot.Format, "store": p.location(), "records": prot.Records,
			"readers": protectedNames(prot), "checked": ver.Checked, "untrusted": ver.Bad, "unsettled": len(interrupted)}
		if none {
			doc["state"] = "none"
			delete(doc, "format")
		}
		if expects {
			doc["expects_sealed"] = true
		}
		if sigs != nil {
			doc["signatures"] = sigs
		}
		doc["roles"] = rolesDoc(erf, rstat, st)
		if recs != nil {
			doc["record_check"] = recs
		}
		if secs != nil {
			doc["secrets"] = secs
		}
		if sealed {
			doc["you"] = prot.You.State
			added, removed := readersDiff(prot, rf)
			if r := prot.Sealed.Recovery; r != nil {
				doc["recovery"] = map[string]any{"k": r.K, "n": r.N, "key": r.Key.String(), "file": recoveryFileState,
					"locked": prot.Sealed.Status(nil).Recovery.Locked}
			}
			doc["readers_file"] = map[string]any{"path": rf.shown(), "found": rf.Found, "valid": rf.Found && rf.Err == nil, "matches": rf.Found && rf.Err == nil && len(added) == 0 && len(removed) == 0, "added": added, "removed": removed}
		}
		if err := emitJSON(doc); err != nil {
			return err
		}
	} else {
		for _, r := range rows {
			out.Printf("%-12s %s\n", r[0], r[1])
		}
		for _, bad := range ver.Bad {
			out.Println(out.C(ui.Red, "Untrusted: "+printable(bad)+" does not match its name"))
		}
		if sigs != nil {
			for _, id := range sigs.Bad {
				out.Println(out.C(ui.Red, "Untrusted: txn/"+printable(id)+".json has a signature that does not verify"))
			}
		}
		if recs != nil {
			for _, pr := range recs.Problems {
				out.Println(out.C(ui.Red, "Untrusted: "+printable(pr.Addr)+": "+printable(pr.Why)))
			}
		}
		if secs != nil {
			for _, pr := range secs.Problems {
				out.Println(out.C(ui.Red, "Untrusted: "+secretProblemText(pr)))
			}
		}
	}
	if len(ver.Bad) > 0 {
		return fmt.Errorf("%w: %s", state.ErrUntrusted, plural(len(ver.Bad), "object does", "objects do")+" not match")
	}
	if sigs != nil && sigs.Invalid > 0 {
		return invalidTxns(sigs.Invalid)
	}
	if recs != nil && len(recs.Problems) > 0 {
		return fmt.Errorf("%w: %s that no transaction of a writer accounts for", state.ErrUntrusted, plural(len(recs.Problems), "record", "records"))
	}
	if secs != nil && len(secs.Problems) > 0 {
		return fmt.Errorf("%w: %s that no transaction of a writer accounts for", state.ErrUntrusted, plural(len(secs.Problems), "secret version", "secret versions"))
	}
	return nil
}

// secretsText is the Secrets line of protect status.
func secretsText(v state.SecretVerdict) string {
	line := fmt.Sprintf("%s · %s", plural(v.Secrets, "secret", "secrets"), plural(v.Versions, "version", "versions"))
	line += fmt.Sprintf(" · %d bound to a transaction of a writer", v.Bound)
	if v.Vouched > 0 {
		line += fmt.Sprintf(" · %d vouched for by an admin", v.Vouched)
	}
	if v.Unchecked > 0 {
		line += fmt.Sprintf(" · %d not checked (signed by a key that is not in the readers file)", v.Unchecked)
	}
	if v.Unconfirmed > 0 {
		line += fmt.Sprintf(" · %d from a transaction that did not finish", v.Unconfirmed)
	}
	if n := len(v.Problems); n > 0 {
		line += fmt.Sprintf(" · %d that no transaction of a writer accounts for", n)
	}
	return line
}

// secretProblemText says which version of which secret is not accounted for, and why.
func secretProblemText(p state.SecretProblem) string {
	what := printable(p.Name)
	if p.Version > 0 {
		what += fmt.Sprintf(" version %d", p.Version)
	}
	return what + ": " + printable(p.Why)
}

// rolesText is the Roles line of protect status.
func rolesText(rf readersFile, rs state.RolesStatus, st *state.Store) string {
	if !rf.Found || rf.Err != nil || !protect.RolesOn(rf.Readers) {
		return "off"
	}
	var n [4]int
	for _, r := range rf.Readers {
		role, _ := protect.RoleOf(rf.Readers, protect.SSHFingerprint(r.Sign))
		n[role]++
	}
	line := fmt.Sprintf("on · %d admin · %d writer · %d reader", n[protect.RoleAdmin], n[protect.RoleWriter], n[protect.RoleReader])
	switch {
	case rs.Source == "state":
		line += fmt.Sprintf(" · pinned, version %d in the state", rs.Version)
		if rs.Differs {
			line += " (the readers file asks for other roles: pravo protect roles)"
		}
	case rs.Unpinned:
		line += " · from the readers file; the state holds roles it does not pin (pravo protect roles)"
	default:
		line += " · from the readers file, not pinned (pravo protect roles)"
	}
	if role, ok := protect.RoleOf(rf.Readers, st.SigningKey()); ok {
		line += " · you are " + article(role)
	}
	return line
}

func rolesDoc(rf readersFile, rs state.RolesStatus, st *state.Store) map[string]any {
	doc := map[string]any{"on": rf.Found && rf.Err == nil && protect.RolesOn(rf.Readers)}
	if doc["on"] == true {
		doc["source"] = rs.Source
		doc["pinned"] = rs.Source == "state"
		if rs.Version > 0 {
			doc["version"] = rs.Version
		}
		if rs.Differs {
			doc["file_differs"] = true
		}
		if role, ok := protect.RoleOf(rf.Readers, st.SigningKey()); ok {
			doc["you"] = role.String()
		}
	}
	return doc
}

// recordsText is the Records line of protect status, for a repository with roles.
func recordsText(v state.RecordVerdict) string {
	line := fmt.Sprintf("%d bound to a transaction of a writer", v.Bound)
	if v.Vouched > 0 {
		line += fmt.Sprintf(" · %d vouched for by an admin", v.Vouched)
	}
	if v.Earlier > 0 {
		line += fmt.Sprintf(" · %d from before roles", v.Earlier)
	}
	if v.Unchecked > 0 {
		line += fmt.Sprintf(" · %d not checked (transaction running, or signed by a key not in the readers file)", v.Unchecked)
	}
	if n := len(v.Problems); n > 0 {
		line += fmt.Sprintf(" · %d that no transaction of a writer accounts for", n)
	}
	return line
}
