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
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

func protectUnsealCmd() *cobra.Command {
	var yes, removeFiles bool
	cmd := &cobra.Command{
		Use:   "unseal",
		Short: "Take the seal off the state",
		Long: `Takes the seal off. It opens every record, the outputs and the stored plans
with your key and writes them in the open, deletes the sealed records, and takes
the sealed part out of meta.json. The state stays in the same format and the same
lineage: unsealing is not a step back to an older layout.

Afterwards anyone who can read the bucket can read every value. A copy of the
bucket that was made before still opens with the key of a reader. The values of
-t in the command lines of transactions that were recorded before the seal were
replaced by a tag when the state was sealed, and stay replaced.

It shows what it will do and asks for a yes. --yes answers it. Without a terminal
and without --yes it refuses to ask. It does not run while a command is running or
a lock is held, and writers are refused until it has finished. It needs an
identity that is a reader of every group of the state. A run that is interrupted
leaves the state unsealing; run it again to finish.

The repository keeps the readers file, and the recovery file, as the anchor that
tells a writer to expect a sealed state. Until they are removed a writer refuses
the unsealed state, with exit code 5. --remove-files deletes them; commit the
deletion. The recovery shares are not touched: destroy them.

Exit codes: 4 when a command is running or a lock is held, or a stored plan is in
use, 5 when your identity is not a reader of the state.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newProtectRun(cmd)
			if err != nil {
				return err
			}
			return p.unseal(yes, removeFiles)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&yes, "yes", false, "do not ask for confirmation")
	f.BoolVar(&removeFiles, "remove-files", false, "also delete the readers file and the recovery file of the repository")
	return cmd
}

func (p *protectRun) unseal(yes, removeFiles bool) error {
	// Everything before the yes only reads, and a state that does not exist is not made.
	rb, err := p.backend(false)
	if err != nil {
		return err
	}
	if _, err := rb.Get(p.ctx, metaKey); errors.Is(err, state.ErrNotFound) {
		return p.notSealed(nil, removeFiles, yes, "There is no state yet.")
	}
	b, err := p.backend(true)
	if err != nil {
		return err
	}
	st, id, idErr, err := p.open(b, true, true)
	if errors.Is(err, state.ErrUntrusted) {
		return reportUntrusted(err)
	}
	if err != nil {
		return err
	}
	prot := st.Protection()
	switch prot.State {
	case "sealed", "unsealing":
	case "migrating":
		return errors.New("a seal is in progress; finish it with pravo protect init --encrypt before the seal is taken off")
	default:
		return p.notSealed(&prot, removeFiles, yes, "The state is not sealed.")
	}
	if st.Locked() || prot.You.State != "reader" {
		return engine.WithCode(engine.ExitRefused, notReaderError(p.rf, id, idErr))
	}
	if err := atRest(st); err != nil {
		return err
	}
	names := protectedNames(prot)
	files := p.repoFiles()
	if !flagJSON {
		out.Printf("%-8s %s  (%s)\n", "State", p.location(), prot.State)
		if m := prot.Migration; prot.State == "unsealing" && m != nil {
			out.Printf("An unseal was started by %s and did not finish: %s of %s records are done.\n", safeName(m.By), commas(m.Done), commas(m.Total))
		}
		out.Println("This will:")
		out.Printf("  1  open %s, the outputs and the stored plans with your key (%s)\n", counted(prot.Records, "record", "records"), id.Name)
		out.Println("  2  write them in the open, and delete the sealed records")
		out.Println("  3  take the sealed part out of meta.json; the format stays")
		if removeFiles && len(files) > 0 {
			out.Printf("  4  delete %s from the repository\n", strings.Join(displayPaths(files), " and "))
		}
		out.Println(out.C(ui.Yellow, fmt.Sprintf("Afterwards anyone who can read the bucket can read every value. The readers (%s) lose nothing, and the bucket loses the seal.", nameList(names))))
	}
	if ok, err := confirm("Proceed?", yes); err != nil {
		return err
	} else if !ok {
		return errors.New("protect unseal cancelled; nothing was written")
	}
	began := time.Now()
	res, err := st.Unseal(p.ctx, state.UnsealOptions{
		Identity: id, By: id.Name,
		Plans: engine.NewPlanStoreFor(st).UnsealAll, PlanGroups: engine.NewPlanStoreFor(st).SealedGroups,
	})
	switch {
	case errors.Is(err, state.ErrPlansInUse):
		return engine.WithCode(engine.ExitLocked, err)
	case errors.Is(err, state.ErrNotReader):
		return engine.WithCode(engine.ExitRefused, err)
	case err != nil:
		return err
	}
	if flagJSON {
		gone, err := p.dropFiles(removeFiles)
		if err != nil {
			return err
		}
		return emitJSON(map[string]any{"unsealed": true, "records": res.Records, "plans": res.PlansUnsealed, "resumed": res.Resumed, "files_removed": gone, "files_left": displayPaths(leftOver(files, gone))})
	}
	if res.Resumed {
		out.Println("Finished an unseal that was begun earlier.")
	}
	if res.Records > 0 || !res.Resumed {
		out.Println(out.C(ui.Green, fmt.Sprintf("%s unsealed in %s", counted(res.Records, "record", "records"), elapsed(time.Since(began)))))
	}
	if res.PlansUnsealed > 0 {
		out.Println(out.C(ui.Green, fmt.Sprintf("%s unsealed", counted(res.PlansUnsealed, "stored plan", "stored plans"))))
	}
	out.Println(out.C(ui.Green, "outputs unsealed · the sealed part of meta.json is gone"))
	out.Println()
	out.Printf("The state is not sealed any more (format %d).\n", prot.Format)
	return p.finishFiles(removeFiles, true)
}

// notSealed is the answer for a state that is not sealed. The files of the repository
// are deleted only for a state that was sealed once, which is a state of format 3,
// and then only after a yes: for any other state they are the anchor of a seal that
// is yet to come, or of a state whose meta.json was lowered.
func (p *protectRun) notSealed(prot *state.Protection, removeFiles, yes bool, line string) error {
	wasSealed := prot != nil && prot.Format >= state.FormatSealed
	files := p.repoFiles()
	if removeFiles && len(files) > 0 && wasSealed {
		if !flagJSON {
			out.Println(line)
			out.Printf("This will delete %s from the repository.\n", strings.Join(displayPaths(files), " and "))
		}
		if ok, err := confirm("Proceed?", yes); err != nil {
			return err
		} else if !ok {
			return errors.New("protect unseal cancelled; nothing was deleted")
		}
	}
	if flagJSON {
		var gone []string
		if removeFiles && wasSealed {
			var err error
			if gone, err = p.dropFiles(true); err != nil {
				return err
			}
		}
		st := "none"
		if prot != nil {
			st = prot.State
		}
		return emitJSON(map[string]any{"unsealed": false, "state": st, "files_removed": nonNil(gone), "files_left": displayPaths(leftOver(files, gone))})
	}
	if !removeFiles || !wasSealed {
		out.Println(line)
		if removeFiles {
			out.Println("--remove-files does nothing: this state was never sealed, and the files of the repository belong to a seal that is yet to come.")
		}
		return nil
	}
	if line != "" && (len(files) == 0) {
		out.Println(line)
	}
	return p.finishFiles(true, false)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func displayPaths(files []string) []string {
	out := []string{}
	for _, f := range files {
		out = append(out, displayPath(f))
	}
	return out
}

// leftOver is the files that were not deleted.
func leftOver(files, gone []string) []string {
	var out []string
	for _, f := range files {
		if !slices.Contains(gone, displayPath(f)) {
			out = append(out, f)
		}
	}
	return out
}

// repoFiles are the files of the repository that tell a writer to expect a sealed
// state, that exist.
func (p *protectRun) repoFiles() []string {
	var files []string
	if p.rf.Found {
		files = append(files, p.rf.Path)
	}
	if rcf := loadRecoveryFile(p.rf); rcf.Found {
		files = append(files, rcf.Path)
	}
	return files
}

// dropFiles deletes the readers file and the recovery file when asked, and returns
// the paths it deleted.
func (p *protectRun) dropFiles(remove bool) ([]string, error) {
	if !remove {
		return nil, nil
	}
	var gone []string
	for _, f := range p.repoFiles() {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return gone, err
		}
		gone = append(gone, displayPath(f))
	}
	return nonNil(gone), nil
}

// finishFiles says what is left to do about the files of the repository.
func (p *protectRun) finishFiles(remove, unsealed bool) error {
	files := p.repoFiles()
	if remove {
		gone, err := p.dropFiles(true)
		if err != nil {
			return err
		}
		for _, f := range gone {
			out.Printf("Removed %s; commit the deletion.\n", f)
		}
		if unsealed {
			out.Println("Destroy the recovery shares, if there were any: they open nothing now.")
		}
		return nil
	}
	if len(files) == 0 {
		return nil
	}
	out.Println("The repository still expects a sealed state, and writers refuse this one (exit code 5) until these are removed:")
	for _, f := range files {
		out.Printf("  %s\n", displayPath(f))
	}
	out.Println("Remove them and commit the deletion, or run this again with --remove-files.")
	if unsealed {
		out.Println("Destroy the recovery shares, if there were any: they open nothing now.")
	}
	return nil
}
