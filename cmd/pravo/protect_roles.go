package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// ---------- protect roles ----------

func protectRolesCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "roles",
		Short: "Store the roles of the readers file in the state and pin them",
		Long: `Stores the roles of the readers file in the state, as a new version, so that a
reader cannot give themselves a role by editing their own copy of the readers file.

The first version is pinned by a line in the readers file, "roles-genesis DIGEST", which
this command adds when it is missing. Commit the line with the readers file. From then on
the roles in the state decide, and the roles in the file say what an admin asks for.
Every later version is signed by a key that is an admin in the version before it, so a
change of roles is this command, run by an admin, and then a commit of the readers file.

It needs an identity that is a reader of the state, and that is an admin in the roles
that decide now (the readers file for the first version). The readers file must give roles.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newProtectRun(cmd)
			if err != nil {
				return err
			}
			return p.roles(yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask before the roles are stored")
	return cmd
}

func (p *protectRun) roles(yes bool) error {
	if err := p.rf.require(); err != nil {
		return err
	}
	if !protect.RolesOn(p.rf.Readers) {
		return engine.WithCode(engine.ExitInvalid, fmt.Errorf("%s gives no roles. Add role:reader, role:writer or role:admin to the lines of the readers first.", p.rf.shown()))
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
	if prot.State != "sealed" {
		return errors.New("the state is not sealed; see pravo protect check")
	}
	if st.Locked() || prot.You.State != "reader" {
		return engine.WithCode(engine.ExitRefused, notReaderError(p.rf, id, idErr))
	}
	chain, err := st.RolesChain(p.ctx)
	if err != nil {
		return engine.WithCode(engine.ExitUntrusted, errors.New(printable(err.Error())))
	}
	head := chain.Head()
	switch {
	case head == nil && p.rf.Pin != "":
		return engine.WithCode(engine.ExitUntrusted, fmt.Errorf("%w: %s pins the roles of the state, and the state has none", state.ErrUntrusted, p.rf.shown()))
	case head != nil && p.rf.Pin != "" && chain.Versions[0].Digest() != p.rf.Pin:
		return engine.WithCode(engine.ExitUntrusted, fmt.Errorf("%w: the first roles in the state are not the ones %s pins", state.ErrUntrusted, p.rf.shown()))
	}
	// The signer is an admin of the roles that decide now: the head of the chain, or the file
	// for the first version.
	deciding, whose := p.rf.Readers, p.rf.shown()
	if head != nil {
		deciding, whose = head.Set, "the roles in the state"
	}
	if role, ok := protect.RoleOf(deciding, st.SigningKey()); !ok || role != protect.RoleAdmin {
		got := "not listed"
		if ok {
			got = article(role)
		}
		return engine.WithCode(engine.ExitRefused, fmt.Errorf("your identity is %s in %s, and an admin is needed to store the roles.", got, whose))
	}
	text := protect.FormatRolesSet(p.rf.Readers)
	var before []protect.Reader
	if head != nil {
		before = head.Set
	}
	changes := roleChanges(before, p.rf.Readers)
	seq := 1
	if head != nil {
		seq = head.Seq + 1
	}
	wrote := head == nil || head.Text != text
	if wrote {
		if len(changes) > 0 && !flagJSON {
			for _, c := range changes {
				out.Println("  " + c)
			}
		}
		if ok, err := confirm(fmt.Sprintf("Store these roles as version %d?", seq), yes); err != nil {
			return err
		} else if !ok {
			return errors.New("protect roles cancelled; nothing was written")
		}
		if err := st.PutRolesVersion(p.ctx, seq, text, time.Now()); err != nil {
			if errors.Is(err, state.ErrRolesChanged) {
				return engine.WithCode(engine.ExitStale, errors.New("another admin changed the roles while this ran; run it again"))
			}
			return err
		}
	} else {
		seq = head.Seq
	}
	digest := state.TextDigest(text)
	if head != nil {
		digest = chain.Versions[0].Digest()
	}
	pinAdded := false
	if p.rf.Pin == "" {
		if err := appendPin(p.rf.Path, digest); err != nil {
			return fmt.Errorf("the roles are in the state, but %s could not be written: %w. Add this line to it:\n%s %s", p.rf.shown(), err, protect.RolesGenesisKey, digest)
		}
		pinAdded = true
	}
	if flagJSON {
		return emitJSON(map[string]any{"version": seq, "written": wrote, "pin": digest, "pin_added": pinAdded, "changes": changes})
	}
	switch {
	case wrote:
		out.Printf("Stored the roles as version %d.\n", seq)
	default:
		out.Printf("The state already holds these roles (version %d).\n", seq)
	}
	if pinAdded {
		out.Printf("Added %s %s to %s. Commit it: until it is in the repository the roles of the file decide.\n", protect.RolesGenesisKey, digest, p.rf.shown())
	}
	return nil
}

// roleChanges describes how the roles went from before to after, one line each, in name order.
func roleChanges(before, after []protect.Reader) []string {
	old := map[string]protect.Role{}
	for _, r := range before {
		old[r.Name] = r.Role
	}
	seen := map[string]bool{}
	var lines []string
	for _, r := range after {
		role := r.Role
		if role == protect.RoleNone {
			role = protect.RoleReader
		}
		seen[r.Name] = true
		was, had := old[r.Name]
		switch {
		case !had:
			lines = append(lines, fmt.Sprintf("%s: %s", r.Name, role))
		case was != role:
			lines = append(lines, fmt.Sprintf("%s: %s -> %s", r.Name, was, role))
		}
	}
	for _, r := range before {
		if !seen[r.Name] {
			lines = append(lines, fmt.Sprintf("%s: removed", r.Name))
		}
	}
	sort.Strings(lines)
	return lines
}

// appendPin adds the pin line to the readers file.
func appendPin(path, digest string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	data = append(data, []byte(protect.RolesGenesisKey+" "+digest+"\n")...)
	return os.WriteFile(path, data, 0o644)
}
