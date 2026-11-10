package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
	"github.com/pravo-systems/pravo/pravo"
)

func protectRewrapCmd(rt *runContext) *cobra.Command {
	var all, removeRecovery, yes, allowNoAdmin bool
	var readd []string
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
			p, err := newProtectRun(rt, cmd)
			if err != nil {
				return err
			}
			return p.rewrap(rt, all, recovery, removeRecovery, yes, allowNoAdmin, readd)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "seal every record, the outputs and the stored plans again under the current key")
	cmd.Flags().StringVar(&recovery, "recovery", "", "set or replace the recovery recipient, as K/N: N shares, any K rebuild its key")
	cmd.Flags().BoolVar(&removeRecovery, "remove-recovery", false, "take the recovery recipient out of the state")
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask before a recovery is replaced or removed")
	cmd.Flags().StringArrayVar(&readd, "readd", nil, "NAME of a former key that the readers file lists again, and that you mean to add back")
	cmd.Flags().BoolVar(&allowNoAdmin, "allow-no-admin", false, "allow a change that leaves no admin among the readers")
	return cmd
}

// resealedAway lists the records that a transaction of a writer signed and that an earlier
// rewrap --all sealed again without signing: the state refuses them (exit 7). Sealing them again
// as this admin makes the rewrap sign them, which repairs the state. A record that fails
// for another reason is left alone.
func (p *protectRun) resealedAway(st *state.Store) []string {
	rf, rs, err := p.rf.withRoles(st)
	if err != nil || !pravo.RolesOn(rf.Readers) {
		return nil
	}
	// The rewrap signs the keys of the records it seals again, as every writer does.
	st.BindRecords(true)
	var addrs []string
	for _, r := range st.List() {
		addrs = append(addrs, r.Address())
	}
	var out []string
	for _, pr := range st.CheckRecordsWith(recordRules(rf.Readers, rs), addrs...).Problems {
		if strings.Contains(pr.Why, "signed another record for revision") {
			out = append(out, pr.Addr)
		}
	}
	return out
}

// rewrapPlan is what a rewrap has decided to do, before it writes anything.
type rewrapPlan struct {
	newRec       *protect.Recovery // the recovery that --recovery asked for
	shares       []protect.Share
	setRec       *protect.Recovery // the recovery to set in the state
	fromFile     bool              // setRec comes from the recovery file
	removing     bool              // the recovery recipient of the state goes
	replacing    bool
	fileRemoving bool // a recovery file that the state has no recovery for goes
	rcf          recoveryFile
	all          bool
}

func (p *protectRun) rewrap(rt *runContext, all bool, recoverySpec string, removeRecovery, yes, allowNoAdmin bool, readd []string) error {
	if recoverySpec != "" && removeRecovery {
		return errors.New("set the recovery or remove it, not both")
	}
	pl := &rewrapPlan{all: all}
	if recoverySpec != "" {
		var err error
		if pl.newRec, pl.shares, err = newRecovery(recoverySpec); err != nil {
			return err
		}
	}
	b, err := p.backend(true)
	if err != nil {
		return err
	}
	st, id, idErr, err := p.open(rt, b, true, true)
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
	if err := p.requireReader(st, prot, id, idErr); err != nil {
		return err
	}
	if err := p.needAdmin(st, "change who reads the state"); err != nil {
		return err
	}
	if err := p.rf.require(); err != nil {
		return err
	}
	added, removed := readersDiff(prot, p.rf)
	if err := p.refuseFormer(st, added, readd); err != nil {
		return err
	}
	if len(removed) > 0 && !allowNoAdmin {
		// The roles that decide are those in the state when they are pinned, and the readers
		// of the file are who remains after the rewrap.
		if eff, _, err := p.rf.withRoles(st); err == nil && pravo.RolesOn(eff.Readers) && !hasAdmin(eff.Readers, p.rf.Readers) {
			return engine.WithCode(engine.ExitRefused, noAdminError(removed, "removes"))
		}
	}
	if err := p.planRecovery(pl, prot, removeRecovery); err != nil {
		return err
	}
	if len(added) == 0 && len(removed) == 0 && !all && !keysUnsigned(st, p.rf) && pl.setRec == nil && !pl.removing && !pl.fileRemoving && len(prot.RecoveryUnverified) == 0 {
		return nothingToRewrap(rt, removeRecovery)
	}
	if all {
		// --all deletes the records that nothing names; a writer that is half way
		// through a write has made one and not yet named it.
		if err := atRest(st); err != nil {
			return err
		}
	}
	if pl.replacing || pl.removing {
		if err := confirmRecoveryLoss(rt, pl, prot, yes); err != nil {
			return err
		}
	}
	var removeShares func()
	if pl.newRec != nil {
		if removeShares, err = writeShares(pl.shares); err != nil {
			return err
		}
	}
	var rebind []string
	if all {
		rebind = p.resealedAway(st)
	}
	began := time.Now()
	res, err := st.Rewrap(p.ctx, pravo.RewrapOptions{
		Readers: p.rf.Readers, All: all, Rebind: rebind, By: id.Name, Recovery: pl.setRec, RemoveRecovery: pl.removing,
		GuardRecovery: pl.setRec != nil || pl.removing, RecoveryWas: prot.Sealed.Recovery,
		Plans: engine.NewPlanStoreFor(st).ResealAll,
	})
	if err != nil && pl.newRec != nil {
		p.afterFailure(rt, pl.newRec, removeShares, pl.rcf)
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
	recNote, recJSON, err := settleRecoveryFiles(rt, pl)
	if err != nil {
		return err
	}
	if rt.flags.JSON {
		j := map[string]any{"added": res.Change.Added, "removed": res.Change.Removed, "new_group": res.Change.NewGroup, "resealed": res.Resealed, "plans_skipped": res.PlansSkipped}
		if res.KeysWere != "" {
			j["keys_were"] = res.KeysWere
		}
		if recJSON != nil {
			j["recovery"] = recJSON
		}
		return emitJSON(j)
	}
	p.reportRewrap(rt, st, prot, res, all, began)
	if recNote != nil {
		recNote()
	}
	return nil
}

// keysUnsigned reports whether no reader of the file signed the list of keys of the state,
// and the identity of the store can sign: a rewrap then makes a new data key and signs.
func keysUnsigned(st *state.Store, rf readersFile) bool {
	keys, _ := st.KeysStatus(rf.Readers)
	return keys != protect.SigVerified && st.CanSignKeys()
}

// nothingToRewrap says that the state already is what the readers file asks for.
func nothingToRewrap(rt *runContext, removeRecovery bool) error {
	if removeRecovery && !rt.flags.JSON {
		rt.out.Println("The state has no recovery recipient, and there is no recovery file.")
		return nil
	}
	if rt.flags.JSON {
		return emitJSON(map[string]any{"added": nil, "removed": nil, "new_group": "", "resealed": 0})
	}
	rt.out.Println("The store already matches the readers file.")
	return nil
}

// planRecovery decides what a rewrap does about the recovery recipient. Without a flag the
// repository decides: a recovery file that names a key that the state does not have is set in
// the state, without new shares.
func (p *protectRun) planRecovery(pl *rewrapPlan, prot state.Protection, removeRecovery bool) error {
	pl.rcf = loadRecoveryFile(p.rf)
	pl.setRec = pl.newRec
	if pl.setRec == nil && !removeRecovery && pl.rcf.Found {
		if pl.rcf.Err != nil {
			return engine.WithCode(engine.ExitInvalid, fmt.Errorf("%s: %s", pl.rcf.shown(), pl.rcf.Err))
		}
		if !pl.rcf.Rec.Equal(prot.Sealed.Recovery) {
			pl.setRec, pl.fromFile = pl.rcf.Rec, true
		}
	}
	pl.removing = removeRecovery && prot.Sealed.Recovery != nil
	// A recovery file that the state has no recovery for is the repository's mistake: it goes.
	pl.fileRemoving = removeRecovery && !pl.removing && pl.rcf.Found
	pl.replacing = pl.newRec != nil && prot.Sealed.Recovery != nil
	return nil
}

// confirmRecoveryLoss asks before a recovery key is replaced or removed.
func confirmRecoveryLoss(rt *runContext, pl *rewrapPlan, prot state.Protection, yes bool) error {
	what := "The recovery key will be replaced"
	if pl.removing {
		what = "The recovery recipient will be removed"
	}
	rt.out.Printf("%s (%d of %d shares, key %s). The shares that were given out open nothing that is written from now on.\n",
		what, prot.Sealed.Recovery.K, prot.Sealed.Recovery.N, prot.Sealed.Recovery.Key.Short())
	if ok, err := confirm(rt, "Proceed?", yes); err != nil {
		return err
	} else if !ok {
		return errors.New("protect rewrap cancelled; nothing was written")
	}
	return nil
}

// settleRecoveryFiles writes or deletes the files of the recovery before anything is reported,
// and returns what to tell about it.
func settleRecoveryFiles(rt *runContext, pl *rewrapPlan) (note func(), j map[string]any, err error) {
	rcf := pl.rcf
	switch {
	case pl.newRec != nil:
		if err := writeRecoveryFile(rcf, pl.newRec); err != nil {
			return nil, nil, fmt.Errorf("the new recovery is in the state, but %s could not be written: %w. Write this line to it:\n%s", rcf.shown(), err, pl.newRec.Line())
		}
		j = recoveryJSON("set", pl.newRec, rcf, true)
		if pl.replacing {
			j["action"] = "replaced"
		}
		note = func() {
			printShares(rt, pl.newRec, pl.shares, rcf)
			if pl.replacing {
				rt.out.Println("The old shares open nothing that is written from now on. Collect them and destroy them.")
			}
		}
	case pl.fromFile:
		j = recoveryJSON("from_file", pl.setRec, rcf, false)
		note = func() {
			rt.out.Printf("Recovery recipient set from %s: key %s, any %d of %d shares rebuild its key.\n", rcf.shown(), pl.setRec.Key.Short(), pl.setRec.K, pl.setRec.N)
		}
	case pl.removing || pl.fileRemoving:
		if err := os.Remove(rcf.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		j = recoveryJSON("removed", nil, rcf, false)
		note = func() {
			if pl.removing {
				rt.out.Printf("Recovery removed. Commit the deletion of %s, and destroy the shares.\n", rcf.shown())
			} else {
				rt.out.Printf("The state has no recovery recipient. Removed %s; commit the deletion.\n", rcf.shown())
			}
		}
	}
	return note, j, nil
}

// reportRewrap says what a rewrap did.
func (p *protectRun) reportRewrap(rt *runContext, st *state.Store, prot state.Protection, res pravo.RewrapResult, all bool, began time.Time) {
	line := "Readers in " + p.rf.shown() + ": " + nameList(readerNames(p.rf.Readers))
	if len(res.Change.Removed) > 0 {
		line += "  (" + nameList(safeNames(res.Change.Removed)) + " removed)"
	}
	rt.out.Println(line)
	if n := len(res.Change.Added); n > 0 {
		groups := len(prot.Sealed.Groups)
		rt.out.Printf("Wrapped the data keys for %s (%s, %s)\n", nameList(res.Change.Added),
			plural(groups, "reader group", "reader groups"), plural(groups*n, "new wrapped key", "new wrapped keys"))
	}
	if res.KeysWere != "" {
		rt.out.Println("The list of keys in meta.json had no signature of a reader in " + p.rf.shown() + ". It has yours now, over a data key that this command made.")
	}
	if res.Change.NewGroup != "" {
		if all {
			rt.out.Println("New data keys for 1 reader group.")
		} else {
			rt.out.Println("New data keys for 1 reader group. Records are sealed again as they are next written.")
		}
		if !all && len(res.Change.Removed) > 0 {
			still := "pravo protect rewrap --all, "
			if protect.RolesOn(p.rf.Readers) {
				still += "pravo protect roles, "
			}
			rt.out.Println("Still to do for " + nameList(safeNames(res.Change.Removed)) + ": " + still + "pravo protect gc, and take the credentials of the bucket away. pravo protect offboard does all but the credentials.")
		}
	}
	if all {
		rt.out.Printf("Sealed %s again in %s.\n", counted(res.Resealed, "record", "records"), elapsed(time.Since(began)))
		if res.PlansSkipped > 0 {
			rt.out.Println(plansInUseLine(res.PlansSkipped, true))
		}
		// A plan that an apply uses is left alone, and stays as readable as it was.
		if entries, _, err := engine.NewPlanStoreFor(st).EntriesAndSkipped(p.ctx, engine.PlanCheckFor(st)); err == nil {
			warnUnsealed(rt, unsealedPlans(entries))
		}
	} else if len(res.Change.Added) > 0 || len(res.Change.Removed) == 0 {
		rt.out.Println(rt.out.C(ui.Green, "meta.json updated"))
	}
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

// hasAdmin tells whether one of the keys of the readers file is an admin in roles. A key that
// the file dropped keeps its role in the roles of the state, and can no longer read it.
func hasAdmin(roles, file []pravo.Reader) bool {
	for _, r := range roles {
		if r.Role != pravo.RoleAdmin {
			continue
		}
		for _, f := range file {
			if protect.SSHFingerprint(f.Sign) == protect.SSHFingerprint(r.Sign) {
				return true
			}
		}
	}
	return false
}

// adminReadsState tells whether an admin of roles is listed in the readers file and already has a
// lock in the state (is not among missing, the names the file lists and the state does not wrap for).
func adminReadsState(roles, file []pravo.Reader, missing []string) bool {
	for _, r := range roles {
		if r.Role != pravo.RoleAdmin {
			continue
		}
		for _, f := range file {
			if protect.SSHFingerprint(f.Sign) == protect.SSHFingerprint(r.Sign) && !slices.Contains(missing, f.Name) {
				return true
			}
		}
	}
	return false
}

// noAdminError is the refusal of a change that leaves nobody who may change who reads the state.
func noAdminError(who []string, verb string) error {
	return fmt.Errorf("this %s %s, and no admin would be left to read the state and change its readers or roles. Keep an admin among the readers: list the new admin next to the old one and run pravo protect rewrap and pravo protect roles first, then remove the old key. Or pass --allow-no-admin (a recovery set is then the only way back).", verb, nameList(safeNames(who)))
}

// refuseFormer stops a key that the roles in the state record as former, because an admin took
// it out, from being wrapped for again by a checkout whose readers file still lists it. names are
// the keys to add, and readd the ones the admin said to add back.
func (p *protectRun) refuseFormer(st *state.Store, names, readd []string) error {
	chain, err := st.RolesChain(p.ctx)
	if err != nil || chain.Head() == nil {
		return nil // no roles, or a chain that is judged elsewhere
	}
	return formerListed(chain.Head().Former, p.rf.Readers, names, readd)
}

// formerListed refuses the readers among names (all of them when names is nil) that are
// former keys of the roles, unless their name is in readd.
func formerListed(former []protect.FormerKey, file []pravo.Reader, names, readd []string) error {
	if len(former) == 0 {
		return nil
	}
	in := func(list []string, name string) bool {
		for _, n := range list {
			if n == name {
				return true
			}
		}
		return false
	}
	var refused []string
	for _, r := range file {
		if names != nil && !in(names, r.Name) {
			continue
		}
		fp := protect.SSHFingerprint(r.Sign)
		for _, f := range former {
			if f.Key == fp && !in(readd, r.Name) {
				refused = append(refused, r.Name)
			}
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return engine.WithCode(engine.ExitRefused, fmt.Errorf("%s was taken out of the roles of the state (it is a former key), and the readers file lists it again. A checkout whose readers file is out of date does that. If you mean to add it back, run this again with --readd %s.", nameList(refused), strings.Join(refused, " --readd ")))
}
