package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

func protectRewrapCmd() *cobra.Command {
	var all, removeRecovery, yes bool
	var recovery string
	cmd := &cobra.Command{
		Use:   "rewrap",
		Short: "Bring the keys of the state in line with the readers file",
		Long: `Compares the readers file with the readers the state is sealed to, and
changes the state to match.

A reader who was added gets the data keys of every group, so that they can read
what is there. This rewrites meta.json and nothing else.

A reader who was removed loses their lock in every group, and the state gets a
new data key. Records are sealed under it as they are next written. They stay
under the old key until then, and a copy of the state that was made before the
removal still opens with the old key. --all seals every record, the outputs and
the stored plans again under the new key now.

--recovery K/N sets the recovery recipient of the state, or replaces it: a new key
is split into N shares, any K of which rebuild it. The shares are written to
recovery-shares/, and the public key to the recovery file next to the readers
file. Give each share to a different person, offline, and commit the recovery
file. A replaced recovery key opens nothing afterwards, and the old shares are
worth nothing, so replacing or removing a recovery asks first (--yes does not).
--remove-recovery takes the recovery out of the state. Without
either, a recovery file that names a key that the state does not have is set in the
state, and a recovery lock that is missing from a group is made again.

It needs an identity that is a reader of the state, and the readers file.
--all does not run while a command is running or a lock is held (exit 4).`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newProtectRun(cmd)
			if err != nil {
				return err
			}
			return p.rewrap(all, recovery, removeRecovery, yes)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "seal every record, the outputs and the stored plans again under the current key")
	cmd.Flags().StringVar(&recovery, "recovery", "", "set or replace the recovery recipient, as K/N: N shares, any K rebuild its key")
	cmd.Flags().BoolVar(&removeRecovery, "remove-recovery", false, "take the recovery recipient out of the state")
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask before a recovery is replaced or removed")
	return cmd
}

func (p *protectRun) rewrap(all bool, recoverySpec string, removeRecovery, yes bool) error {
	if recoverySpec != "" && removeRecovery {
		return errors.New("set the recovery or remove it, not both")
	}
	var newRec *protect.Recovery
	var shares []protect.Share
	if recoverySpec != "" {
		var err error
		if newRec, shares, err = newRecovery(recoverySpec); err != nil {
			return err
		}
	}
	b, err := p.backend(true)
	if err != nil {
		return err
	}
	st, id, idErr, err := p.open(b, true, true)
	if err != nil {
		return err
	}
	prot := st.Protection()
	if prot.State == "unsealing" {
		return errors.New("an unseal is in progress; finish it with pravo protect unseal")
	}
	if prot.State != "sealed" {
		return errors.New("the state is not sealed; see pravo protect check")
	}
	if st.Locked() || prot.You.State != "reader" {
		return engine.WithCode(engine.ExitRefused, notReaderError(p.rf, id, idErr))
	}
	if err := p.rf.require(); err != nil {
		return err
	}
	added, removed := readersDiff(prot, p.rf)
	// Without a flag the repository decides: a recovery file that names a key that
	// the state does not have is set in the state, without new shares.
	rcf := loadRecoveryFile(p.rf)
	setRec, fromFile := newRec, false
	if setRec == nil && !removeRecovery && rcf.Found {
		if rcf.Err != nil {
			return engine.WithCode(engine.ExitInvalid, fmt.Errorf("%s: %s", rcf.shown(), rcf.Err))
		}
		if !rcf.Rec.Equal(prot.Sealed.Recovery) {
			setRec, fromFile = rcf.Rec, true
		}
	}
	removing := removeRecovery && prot.Sealed.Recovery != nil
	// A recovery file that the state has no recovery for is the repository's mistake: it goes.
	fileRemoving := removeRecovery && !removing && rcf.Found
	if len(added) == 0 && len(removed) == 0 && !all && setRec == nil && !removing && !fileRemoving && len(prot.RecoveryUnverified) == 0 {
		if removeRecovery && !flagJSON {
			out.Println("The state has no recovery recipient, and there is no recovery file.")
			return nil
		}
		if flagJSON {
			return emitJSON(map[string]any{"added": nil, "removed": nil, "new_group": "", "resealed": 0})
		}
		out.Println("The store already matches the readers file.")
		return nil
	}
	replacing := newRec != nil && prot.Sealed.Recovery != nil
	if all {
		// --all deletes the records that nothing names; a writer that is half way
		// through a write has made one and not yet named it.
		if err := atRest(st); err != nil {
			return err
		}
	}
	if replacing || removing {
		what := "The recovery key will be replaced"
		if removing {
			what = "The recovery recipient will be removed"
		}
		out.Printf("%s (%d of %d shares, key %s). The shares that were given out open nothing that is written from now on.\n",
			what, prot.Sealed.Recovery.K, prot.Sealed.Recovery.N, prot.Sealed.Recovery.Key.Short())
		if ok, err := confirm("Proceed?", yes); err != nil {
			return err
		} else if !ok {
			return errors.New("protect rewrap cancelled; nothing was written")
		}
	}
	var removeShares func()
	if newRec != nil {
		if removeShares, err = writeShares(shares); err != nil {
			return err
		}
	}
	began := time.Now()
	res, err := st.Rewrap(p.ctx, state.RewrapOptions{
		Readers: p.rf.Readers, All: all, By: id.Name, Recovery: setRec, RemoveRecovery: removing,
		GuardRecovery: setRec != nil || removing, RecoveryWas: prot.Sealed.Recovery,
		Plans: engine.NewPlanStoreFor(st).ResealAll,
	})
	if err != nil && newRec != nil {
		p.afterFailure(newRec, removeShares, rcf)
	}
	if errors.Is(err, state.ErrRecoveryChanged) {
		return engine.WithCode(engine.ExitStale, errors.New("another command changed the recovery of the state while this ran, so nothing was set and any new shares were removed; run it again"))
	}
	if errors.Is(err, state.ErrChanged) {
		return engine.WithCode(engine.ExitStale, errors.New("meta.json changed while rewrapping; run it again"))
	}
	if err != nil {
		return err
	}
	// The files of the recovery are settled before anything is reported.
	var recNote func()
	var recJSON map[string]any
	switch {
	case newRec != nil:
		if err := writeRecoveryFile(rcf, newRec); err != nil {
			return fmt.Errorf("the new recovery is in the state, but %s could not be written: %w. Write this line to it:\n%s", rcf.shown(), err, newRec.Line())
		}
		recJSON = recoveryJSON("set", newRec, rcf, true)
		if replacing {
			recJSON["action"] = "replaced"
		}
		recNote = func() {
			printShares(newRec, shares, rcf)
			if replacing {
				out.Println("The old shares open nothing that is written from now on. Collect them and destroy them.")
			}
		}
	case fromFile:
		recJSON = recoveryJSON("from_file", setRec, rcf, false)
		recNote = func() {
			out.Printf("Recovery recipient set from %s: key %s, any %d of %d shares rebuild its key.\n", rcf.shown(), setRec.Key.Short(), setRec.K, setRec.N)
		}
	case removing || fileRemoving:
		if err := os.Remove(rcf.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		recJSON = recoveryJSON("removed", nil, rcf, false)
		recNote = func() {
			if removing {
				out.Printf("Recovery removed. Commit the deletion of %s, and destroy the shares.\n", rcf.shown())
			} else {
				out.Printf("The state has no recovery recipient. Removed %s; commit the deletion.\n", rcf.shown())
			}
		}
	}
	if flagJSON {
		j := map[string]any{"added": res.Change.Added, "removed": res.Change.Removed, "new_group": res.Change.NewGroup, "resealed": res.Resealed, "plans_skipped": res.PlansSkipped}
		if recJSON != nil {
			j["recovery"] = recJSON
		}
		return emitJSON(j)
	}
	line := "Readers in " + p.rf.shown() + ": " + nameList(readerNames(p.rf.Readers))
	if len(res.Change.Removed) > 0 {
		line += "  (" + nameList(safeNames(res.Change.Removed)) + " removed)"
	}
	out.Println(line)
	if n := len(res.Change.Added); n > 0 {
		groups := len(prot.Sealed.Groups)
		out.Printf("Wrapped the data keys for %s (%s, %s)\n", nameList(res.Change.Added),
			plural(groups, "reader group", "reader groups"), plural(groups*n, "new wrapped key", "new wrapped keys"))
	}
	if res.Change.NewGroup != "" {
		if all {
			out.Println("New data keys for 1 reader group.")
		} else {
			out.Println("New data keys for 1 reader group. Records are sealed again as they are next written.")
		}
	}
	if all {
		out.Printf("Sealed %s again in %s.\n", counted(res.Resealed, "record", "records"), elapsed(time.Since(began)))
		if res.PlansSkipped > 0 {
			out.Println(plansInUseLine(res.PlansSkipped, true))
		}
		// A plan that an apply uses is left alone, and stays as readable as it was.
		if entries, _, err := engine.NewPlanStoreFor(st).EntriesAndSkipped(p.ctx, engine.PlanCheckFor(st)); err == nil {
			warnUnsealed(unsealedPlans(entries))
		}
	} else if len(res.Change.Added) > 0 || len(res.Change.Removed) == 0 {
		out.Println(out.C(ui.Green, "meta.json updated"))
	}
	if recNote != nil {
		recNote()
	}
	return nil
}

// plansInUseLine says that n stored plans were left as they were because
// something uses them, in the number that n takes. again is set for the plans that
// a rewrap leaves under the group they were sealed in, and not for those that an
// upgrade leaves in the clear.
func plansInUseLine(n int, again bool) string {
	sealed := "sealed"
	if again {
		sealed = "sealed again"
	}
	if n == 1 {
		return "1 stored plan in use was not " + sealed + "; run pravo protect rewrap --all when it is done."
	}
	return fmt.Sprintf("%d stored plans in use were not %s; run pravo protect rewrap --all when they are done.", n, sealed)
}
