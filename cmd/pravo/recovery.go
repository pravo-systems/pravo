package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

// The recovery recipient of a sealed state is one more reader whose key nobody
// holds whole: it is split into shares, and any K of N rebuild it. Its public key
// and the threshold are in .pravo/access/recovery.txt, committed next to the
// readers file, so that writers can tell a state whose recovery was taken out of
// the bucket. The shares are written once, to recovery-shares/, and are not
// committed.

// recoveryFileName is the name of the file in the access directory.
const recoveryFileName = "recovery.txt"

// recoverySharesDir is the directory the shares are written to, relative to the
// working directory.
const recoverySharesDir = "recovery-shares"

// maxRecoveryFileSize is the most that is read of a recovery file or a share file.
const maxRecoveryFileSize = 4 << 10

// recoveryFile is what was found of the recovery file of the repository.
type recoveryFile struct {
	Path  string
	Found bool
	Rec   *protect.Recovery
	Err   error
}

func (rcf recoveryFile) shown() string { return displayPath(rcf.Path) }

// loadRecoveryFile reads the recovery file from the access directory the readers
// file was found in, or would be written to. An error never holds a line of the
// file.
func loadRecoveryFile(rf readersFile) recoveryFile {
	rcf := recoveryFile{Path: filepath.Join(rf.Dir, recoveryFileName)}
	if _, err := os.Lstat(rcf.Path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return rcf
		}
		rcf.Found, rcf.Err = true, errors.New("cannot be read: "+errorReason(err))
		return rcf
	}
	rcf.Found = true
	if _, err := os.Stat(rf.Dir); err != nil {
		rcf.Err = errors.New("cannot be read: " + errorReason(err))
		return rcf
	}
	data, err := readInside(rf.Dir, rcf.Path)
	if err != nil {
		var fe *readersFileError
		if errors.As(err, &fe) {
			rcf.Err = errors.New(fe.Reason)
		} else {
			rcf.Err = err
		}
		return rcf
	}
	if rcf.Rec, err = protect.ParseRecoveryLine(data); err != nil {
		rcf.Err = errors.New(strings.TrimPrefix(err.Error(), "recovery file: "))
	}
	return rcf
}

// recoveryProblem compares the recovery file with the recovery recipient of the
// sealed state. It returns what a writer refuses for, and the exit code, or ""
// when they agree. The repository is the anchor: whoever can write the bucket can
// take the recovery out of meta.json, and a writer that has the repository sees it.
func recoveryProblem(prot state.Protection, rf readersFile) (string, engine.ExitCode) {
	if prot.Sealed == nil {
		return "", 0
	}
	rcf := loadRecoveryFile(rf)
	m := prot.Sealed
	switch {
	case rcf.Err != nil:
		return fmt.Sprintf("%s: %s", rcf.shown(), rcf.Err), engine.ExitInvalid
	case rcf.Found && m.Recovery == nil:
		return fmt.Sprintf("%s lists a recovery recipient that the state does not have. Run pravo protect rewrap.", rcf.shown()), engine.ExitRefused
	case rcf.Found && !rcf.Rec.Equal(m.Recovery):
		return fmt.Sprintf("%s and the state name different recovery keys. Run pravo protect rewrap.", rcf.shown()), engine.ExitRefused
	case !rcf.Found && m.Recovery != nil:
		return fmt.Sprintf("the state has a recovery recipient, and %s was not found (looked in %s). Commit the file, or remove the recovery with pravo protect rewrap --remove-recovery.", rcf.shown(), rf.shownDir()), engine.ExitRefused
	}
	if st := m.Status(nil); st.Recovery != nil && !st.Recovery.Locked {
		return fmt.Sprintf("the recovery lock is missing from %s. Run pravo protect rewrap.", groupList(st.Recovery.Groups)), engine.ExitRefused
	}
	if bad := prot.RecoveryUnverified; len(bad) > 0 {
		return fmt.Sprintf("the recovery lock of %s was changed in the bucket: it does not hold its check. Run pravo protect rewrap.", groupList(bad)), engine.ExitRefused
	}
	return "", 0
}

// groupList names groups for a message: "group g1", "groups g1, g2".
func groupList(ids []string) string {
	if len(ids) == 1 {
		return "group " + ids[0]
	}
	return "groups " + strings.Join(ids, ", ")
}

// recoveryStatusText is the Recovery line of protect status, and the file's verdict.
func recoveryStatusText(prot state.Protection, rf readersFile) (text, file string) {
	m := prot.Sealed
	if m == nil || m.Recovery == nil {
		rcf := loadRecoveryFile(rf)
		if rcf.Found {
			return fmt.Sprintf("none in the state, and %s lists one: run pravo protect rewrap", rcf.shown()), "differs"
		}
		return "none", "none"
	}
	r := m.Recovery
	text = fmt.Sprintf("%d of %d shares · key %s", r.K, r.N, r.Key.Short())
	rcf := loadRecoveryFile(rf)
	switch {
	case rcf.Err != nil:
		file = "invalid"
		text += " · " + rcf.shown() + " does not load: " + rcf.Err.Error()
	case !rcf.Found:
		file = "missing"
		text += " · " + rcf.shown() + " not found"
	case !rcf.Rec.Equal(r):
		file = "differs"
		text += " · " + rcf.shown() + " differs from the state"
	default:
		file = "match"
		text += " · " + rcf.shown() + " matches the state"
	}
	if st := m.Status(nil); st.Recovery != nil && !st.Recovery.Locked {
		text += " · the lock is missing from " + strings.Join(st.Recovery.Groups, ", ")
	}
	if len(prot.RecoveryUnverified) > 0 {
		text += " · the lock of " + strings.Join(prot.RecoveryUnverified, ", ") + " does not hold its check: run pravo protect rewrap"
	}
	return text, file
}

// newRecovery makes the recovery recipient and its shares from "K/N".
func newRecovery(spec string) (*protect.Recovery, []protect.Share, error) {
	k, n, err := protect.ParseThreshold(spec)
	if err != nil {
		return nil, nil, err
	}
	return protect.NewRecovery(k, n)
}

// shareName is the name of the file of a share.
func shareName(s protect.Share) string {
	return fmt.Sprintf("share-%d-of-%d.txt", s.Index, s.N)
}

// sharesDirFree is nil when recovery-shares/ holds no share.
func sharesDirFree() error {
	if old, _ := filepath.Glob(filepath.Join(recoverySharesDir, "share-*-of-*.txt")); len(old) > 0 {
		return fmt.Errorf("%s already holds shares (%s). Move them away first: two sets of shares must not be mixed. Shares that you are using now belong elsewhere.", recoverySharesDir, filepath.Base(old[0]))
	}
	return nil
}

func fileMissing(p string) bool {
	_, err := os.Lstat(p)
	return errors.Is(err, os.ErrNotExist)
}

// writeShares writes the share files to recovery-shares/, with mode 0600, and
// returns a function that removes what it wrote. It refuses to go on when the
// directory already holds a share, so that two sets are never mixed.
func writeShares(shares []protect.Share) (remove func(), err error) {
	if err := sharesDirFree(); err != nil {
		return nil, err
	}
	if err := protect.EnsurePrivateDir(recoverySharesDir); err != nil {
		return nil, err
	}
	// A repository that holds the directory must not commit it by git add -A.
	if p := filepath.Join(recoverySharesDir, ".gitignore"); fileMissing(p) {
		_ = protect.WritePrivateFile(p, []byte("*\n"), false)
	}
	var written []string
	remove = func() {
		for _, p := range written {
			os.Remove(p)
		}
	}
	for _, s := range shares {
		p := filepath.Join(recoverySharesDir, shareName(s))
		if err := protect.WritePrivateFile(p, s.File(), false); err != nil {
			remove()
			return nil, err
		}
		written = append(written, p)
	}
	return remove, nil
}

// writeRecoveryFile writes the recovery file to the access directory.
// It never writes through a link: a file in the way that is not a regular file is
// refused, and the new content replaces the old whole, by a rename.
func writeRecoveryFile(rcf recoveryFile, rec *protect.Recovery) error {
	dir := filepath.Dir(rcf.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if fi, err := os.Lstat(rcf.Path); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", rcf.shown())
	}
	f, err := os.CreateTemp(dir, ".recovery.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, werr := f.WriteString(rec.Line())
	if werr == nil {
		werr = f.Chmod(0o644)
	}
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	return os.Rename(f.Name(), rcf.Path)
}

// storedRecovery is the recovery recipient that the store holds now, read afresh. ok
// is false when the store could not be read, which a caller treats as "unknown".
func (p *protectRun) storedRecovery() (rec *protect.Recovery, ok bool) {
	b, err := p.backend(false)
	if err != nil {
		return nil, false
	}
	st, err := state.OpenBackendWith(p.ctx, b, state.Options{})
	if err != nil {
		return nil, false
	}
	prot := st.Protection()
	if prot.Sealed == nil {
		return nil, true
	}
	return prot.Sealed.Recovery, true
}

// afterFailure decides what to do with the shares of rec when the command that set it
// failed. meta.json is written early, so the recovery may be in the store already. Shares
// of a lock that is there are the only way in, and they stay; so do shares when the store
// cannot be read to tell. Only shares of a key that the store does not hold are removed.
func (p *protectRun) afterFailure(rec *protect.Recovery, removeShares func(), rcf recoveryFile) {
	if removeShares == nil {
		return
	}
	stored, ok := p.storedRecovery()
	switch {
	case ok && !stored.Equal(rec):
		removeShares()
	case ok:
		note := fmt.Sprintf("The recovery is already in the state, so its shares stay in %s. Do not delete them.", recoverySharesDir)
		if err := writeRecoveryFile(rcf, rec); err != nil {
			note += fmt.Sprintf(" %s could not be written: write this line to it:\n%s", rcf.shown(), rec.Line())
		} else {
			note += fmt.Sprintf(" %s was written; commit it.", rcf.shown())
		}
		out.Println(out.C(ui.Yellow, note))
	default:
		out.Println(out.C(ui.Yellow, fmt.Sprintf("Could not tell whether the recovery was set, so the shares stay in %s. Run pravo protect status, and delete them only if the state has no recovery.", recoverySharesDir)))
	}
}

// recoveryJSON is what --json says of a change of the recovery.
func recoveryJSON(action string, rec *protect.Recovery, rcf recoveryFile, shares bool) map[string]any {
	m := map[string]any{"action": action}
	if rec != nil {
		m["k"], m["n"], m["key"], m["file"] = rec.K, rec.N, rec.Key.String(), rcf.Path
		if shares {
			m["shares"] = recoverySharesDir
		}
	}
	return m
}

// printShares says where the shares are and what to do with them.
func printShares(rec *protect.Recovery, shares []protect.Share, rcf recoveryFile) {
	out.Println()
	out.Printf("Recovery: %d shares written (any %d rebuild the recovery key):\n", rec.N, rec.K)
	out.Printf("  ./%s/%s  …  %s\n", recoverySharesDir, shareName(shares[0]), shareName(shares[len(shares)-1]))
	out.Println("Do not commit them. Give each to a different person, offline. They are written once.")
	out.Println("Commit the recovery recipient with the readers file:")
	out.Printf("  %s\n", rcf.shown())
}

// ---------- protect recover ----------

func protectRecoverCmd() *cobra.Command {
	var shareFiles []string
	var newSpec string
	var all, yes bool
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Open a state with the shares of its recovery key, and make a new list of readers",
		Long: `Rebuilds the recovery key of the state from K of its shares, opens the state
with it, and makes the readers of the state the readers in the readers file. Use it
when no reader can open the state any more: make an identity (pravo id new), put its
line in the readers file in place of the lost ones, and run this with the share
files.

The recovery key is rebuilt on this machine, so it has been seen in one place.
Replace it when the state is open again: --recovery K/N makes a new key and new
shares in the same step, and the old shares open nothing that is written afterwards. Without it
the old recovery stays, and the command says to replace it.

Removing a reader makes a new data key, as protect rewrap does; --all seals every
record, the outputs and the stored plans again under it.

Exit codes: 5 when the shares do not rebuild the recovery key of this state, 6
when the readers file is missing or invalid.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newProtectRun(cmd)
			if err != nil {
				return err
			}
			return p.recover(shareFiles, newSpec, all, yes)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&shareFiles, "share", nil, "a share file; give it once for each share")
	f.StringVar(&newSpec, "recovery", "", "also make a new recovery key, as K/N, and replace this one")
	f.BoolVar(&all, "all", false, "seal every record, the outputs and the stored plans again under the new key")
	f.BoolVar(&yes, "yes", false, "do not ask for confirmation")
	return cmd
}

// readShare reads one share file. The error names the file and never its content.
func readShare(path string) (protect.Share, error) {
	info, err := os.Stat(path)
	if err != nil {
		return protect.Share{}, fmt.Errorf("%s: %s", path, errorReason(err))
	}
	if !info.Mode().IsRegular() || info.Size() > maxRecoveryFileSize {
		return protect.Share{}, fmt.Errorf("%s: not a share file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return protect.Share{}, fmt.Errorf("%s: %s", path, errorReason(err))
	}
	s, err := protect.ParseShare(data)
	if err != nil {
		return protect.Share{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

func (p *protectRun) recover(files []string, newSpec string, all, yes bool) error {
	if len(files) == 0 {
		return errors.New("pass the share files, one --share for each")
	}
	var newRec *protect.Recovery
	var newShares []protect.Share
	if newSpec != "" {
		var err error
		if newRec, newShares, err = newRecovery(newSpec); err != nil {
			return err
		}
	}
	if err := p.rf.require(); err != nil {
		return err
	}
	b, err := p.backend(true)
	if err != nil {
		return err
	}
	locked, err := state.OpenBackendWith(p.ctx, b, state.Options{})
	if errors.Is(err, state.ErrUntrusted) {
		return reportUntrusted(err)
	}
	if err != nil {
		return err
	}
	prot := locked.Protection()
	switch {
	case prot.State == "unsealing":
		return errors.New("an unseal is in progress; finish it with pravo protect unseal --share FILE, which opens the state with the shares")
	case prot.State != "sealed" || prot.Sealed == nil:
		return errors.New("the state is not sealed; there is nothing to recover")
	case prot.Sealed.Recovery == nil:
		return errors.New("the state has no recovery recipient; it was sealed without one, and only a reader can open it")
	}
	shares := make([]protect.Share, 0, len(files))
	for _, f := range files {
		s, err := readShare(f)
		if err != nil {
			return engine.WithCode(engine.ExitRefused, err)
		}
		shares = append(shares, s)
	}
	rk, err := prot.Sealed.Recovery.Open(shares)
	if err != nil {
		return engine.WithCode(engine.ExitRefused, err)
	}
	id := protect.RecoveryIdentity(rk)
	defer id.Close()

	if newRec != nil {
		// Before the prompt: the shares in use are often in the very directory the new ones go to.
		if err := sharesDirFree(); err != nil {
			return err
		}
	}
	added, removed := readersDiff(prot, p.rf)
	names := nameList(readerNames(p.rf.Readers))
	out.Printf("Recovery key rebuilt from %s.\n", plural(len(shares), "share", "shares"))
	out.Printf("%-8s %s  (%s)\n", "Readers", p.rf.shown(), names)
	out.Println("This will:")
	n := 1
	step := func(s string) { out.Printf("  %d  %s\n", n, s); n++ }
	switch {
	case len(added) == 0 && len(removed) == 0:
		step("keep the readers as they are: the file and the state agree")
	default:
		if len(added) > 0 {
			step("lock the data keys for " + nameList(added))
		}
		if len(removed) > 0 {
			step("remove " + nameList(removed) + ", and make a new data key")
		}
	}
	if all {
		step("seal every record, the outputs and the stored plans again under the new key")
	}
	if newRec != nil {
		step(fmt.Sprintf("replace the recovery key: %d new shares, any %d rebuild it, and the old shares open nothing written from now on", newRec.N, newRec.K))
	}
	if ok, err := confirm("Proceed?", yes); err != nil {
		return err
	} else if !ok {
		return errors.New("protect recover cancelled; nothing was written")
	}
	if all {
		if err := atRest(locked); err != nil {
			return err
		}
	}
	var removeShares func()
	if newRec != nil {
		if removeShares, err = writeShares(newShares); err != nil {
			return err
		}
	}
	rcf := loadRecoveryFile(p.rf)
	st, err := state.OpenBackendWith(p.ctx, b, state.Options{Identity: id})
	if err != nil {
		p.afterFailure(newRec, removeShares, rcf)
		return err
	}
	began := time.Now()
	// The recovery key opens the state until the recovery is replaced: the records are
	// sealed again first, and the replacement is a step of its own.
	first := state.RewrapOptions{Readers: p.rf.Readers, All: all, By: protect.RecoveryName, Plans: engine.NewPlanStoreFor(st).ResealAll}
	if newRec != nil && !all {
		first.Recovery, first.GuardRecovery, first.RecoveryWas = newRec, true, prot.Sealed.Recovery
	}
	res, err := st.Rewrap(p.ctx, first)
	if err == nil && newRec != nil && all {
		_, err = st.Rewrap(p.ctx, state.RewrapOptions{Readers: p.rf.Readers, By: protect.RecoveryName, Recovery: newRec,
			GuardRecovery: true, RecoveryWas: prot.Sealed.Recovery})
	}
	if err != nil {
		p.afterFailure(newRec, removeShares, rcf)
		if errors.Is(err, state.ErrRecoveryChanged) {
			return engine.WithCode(engine.ExitStale, errors.New("another command changed the recovery of the state while this ran, so this recovery was not set and its shares were removed; run it again"))
		}
		if errors.Is(err, state.ErrChanged) {
			return engine.WithCode(engine.ExitStale, errors.New("meta.json changed while recovering; run it again"))
		}
		return err
	}
	if newRec != nil {
		if err := writeRecoveryFile(rcf, newRec); err != nil {
			return fmt.Errorf("the new recovery is in the state, but %s could not be written: %w. Write this line to it:\n%s", rcf.shown(), err, newRec.Line())
		}
	}
	if flagJSON {
		j := map[string]any{"added": res.Change.Added, "removed": res.Change.Removed, "new_group": res.Change.NewGroup,
			"resealed": res.Resealed, "recovery_replaced": newRec != nil}
		if newRec != nil {
			j["recovery"] = recoveryJSON("replaced", newRec, rcf, true)
		}
		return emitJSON(j)
	}
	out.Println(out.C(ui.Green, "meta.json updated"))
	if res.Change.NewGroup != "" && !all {
		out.Println("New data keys for 1 reader group. Records are sealed again as they are next written.")
	}
	if all {
		out.Printf("Sealed %s again in %s.\n", counted(res.Resealed, "record", "records"), elapsed(time.Since(began)))
	}
	if newRec != nil {
		printShares(newRec, newShares, rcf)
	} else {
		out.Println(out.C(ui.Yellow, "The recovery key was rebuilt on this machine. Replace it: pravo protect rewrap --recovery K/N"))
	}
	return nil
}
