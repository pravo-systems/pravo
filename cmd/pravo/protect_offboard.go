package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
)

func protectOffboardCmd(rt *runContext) *cobra.Command {
	var yes, vouchNotes bool
	cmd := &cobra.Command{
		Use:   "offboard <name>",
		Short: "Take a member off: their line, their key, their roles and the old data keys",
		Long: `Does, in order, what it takes to lock a member out of the state:

  1. removes their line from the readers file;
  2. pravo protect rewrap --all: new data keys, and every record, the outputs
     and the stored plans sealed again under them;
  3. pravo protect roles, when the readers file gives roles: the member becomes a
     former key, and what they wrote stays vouched for;
  4. pravo protect gc: the reader groups that nothing is sealed in are dropped,
     and with them the old data key.

It then prints what is left for you: committing the readers file, a second gc
when the first one kept the old group (it says when), and taking the credentials
of the bucket from the member. Until the old group is dropped, a copy of the
bucket that was made before this still opens with the key of the member.

It needs an identity that is an admin. When a step fails, the steps before it
stay done and the readers file keeps the change; run the step that failed by
itself, then pravo protect offboard again for what is left. Name a member who is
already off the readers file to run the steps without the first. Nothing here
takes the seal off or touches the recovery recipient.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if rt.flags.JSON {
				return errors.New("protect offboard runs several steps and has no --json; run rewrap, roles and gc with --json instead")
			}
			p, err := newProtectRun(rt, cmd)
			if err != nil {
				return err
			}
			return p.offboard(rt, args[0], yes, vouchNotes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask before the roles are stored")
	cmd.Flags().BoolVar(&vouchNotes, "vouch-notes", false, "also vouch for the run note and the promotions the member signed (see pravo protect roles)")
	return cmd
}

// removeReaderLine drops the line of the reader called name from the text of a readers file.
// A comment, a blank line and the roles-genesis line are kept.
func removeReaderLine(text, name string) (string, bool) {
	var kept []string
	found := false
	for _, l := range strings.SplitAfter(text, "\n") {
		f := strings.Fields(l)
		if len(f) > 0 && !strings.HasPrefix(f[0], "#") && f[0] == name {
			found = true
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, ""), found
}

func (p *protectRun) offboard(rt *runContext, name string, yes, vouchNotes bool) error {
	if err := p.rf.require(); err != nil {
		return err
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
	if prot.State != "sealed" {
		return errors.New("the state is not sealed; there is nobody to take off (see pravo protect check)")
	}
	if st.Locked() || prot.You.State != "reader" {
		return engine.WithCode(engine.ExitRefused, notReaderError(p.rf, id, idErr))
	}
	if err := p.needAdmin(st, "take a member off"); err != nil {
		return err
	}
	inFile := false
	for _, r := range p.rf.Readers {
		if r.Name == name {
			inFile = true
		}
	}
	inState := slices.Contains(protectedNames(prot), safeName(name))
	if !inFile && !inState {
		return engine.WithCode(engine.ExitInvalid, fmt.Errorf("%s is not a reader in %s or in the state. Readers: %s", safeName(name), p.rf.shown(), nameList(readerNames(p.rf.Readers))))
	}
	if inFile {
		orig, err := os.ReadFile(p.rf.Path)
		if err != nil {
			return err
		}
		next, found := removeReaderLine(string(orig), name)
		if !found {
			return fmt.Errorf("could not find the line of %s in %s", safeName(name), p.rf.shown())
		}
		if err := os.WriteFile(p.rf.Path, []byte(next), 0o644); err != nil {
			return err
		}
		rt.out.Printf("1/4 Removed the line of %s from %s.\n", safeName(name), p.rf.shown())
		p.rf = readersOf(rt, p.root)
		if err := p.rewrap(rt, true, "", false, yes, false, nil); err != nil {
			// The state was not changed: put the line back.
			if werr := os.WriteFile(p.rf.Path, orig, 0o644); werr != nil {
				return fmt.Errorf("%w (and %s could not be restored: %v)", err, p.rf.shown(), werr)
			}
			return fmt.Errorf("%w\n(The line of %s is back in %s.)", err, safeName(name), p.rf.shown())
		}
	} else {
		rt.out.Printf("1/4 %s is not in %s any more.\n", safeName(name), p.rf.shown())
		if err := p.rewrap(rt, true, "", false, yes, false, nil); err != nil {
			return err
		}
	}
	rt.out.Println("2/4 Sealed every record again under the new data keys (above).")
	rolesDone := false
	if protect.RolesOn(p.rf.Readers) {
		rt.out.Println("3/4 Roles:")
		if err := p.roles(rt, yes, false, vouchNotes, false, false, nil); err != nil {
			return fmt.Errorf("%w\nThe state is rewrapped. Fix this, run pravo protect roles, then pravo protect offboard %s again for the rest", err, name)
		}
		rolesDone = true
	} else {
		rt.out.Println("3/4 The readers file gives no roles: nothing to record.")
	}
	rt.out.Println("4/4 Collecting the old data keys:")
	if err := p.gc(rt, 0, false); err != nil {
		return fmt.Errorf("%w\nThe state is rewrapped. Run pravo protect gc again for the rest", err)
	}
	rt.out.Println()
	rt.out.Printf("%s can no longer read what is written from now on. Still to do:\n", safeName(name))
	commit := "  - commit " + p.rf.shown()
	if rolesDone {
		commit += " (with its roles-genesis line)"
	}
	rt.out.Println(commit + ", so that every checkout agrees;")
	rt.out.Println("  - run pravo protect gc again after the time that gc gave above (about a minute for a state this size, up to a day for records that no pointer names): until the old group is dropped, a copy of the bucket made before today still opens with the key of " + safeName(name) + ";")
	rt.out.Println("  - take the credentials of the bucket and of the cloud account from " + safeName(name) + " where you keep them.")
	return nil
}
