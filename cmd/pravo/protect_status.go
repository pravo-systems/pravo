package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

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
	add("Roles", "off")
	txns, err := st.Transactions()
	if err != nil {
		return err
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
	if flagJSON {
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
	}
	if len(ver.Bad) > 0 {
		return fmt.Errorf("%w: %s", state.ErrUntrusted, plural(len(ver.Bad), "object does", "objects do")+" not match")
	}
	if sigs != nil && sigs.Invalid > 0 {
		return invalidTxns(sigs.Invalid)
	}
	return nil
}
