package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
)

func protectPoliciesCmd() *cobra.Command {
	var restore bool
	cmd := &cobra.Command{
		Use:   "policies",
		Short: "List the policy versions that signed transactions name and the state lost, and restore them",
		Long: `A transaction that applied a plan lists, under its signature, the policy versions
that judged the plan. A policy version is never deleted from a state. When an object
of policy/ is gone, or holds other text than a transaction lists, every command that
reads the policies refuses the state (exit 7) and names the policy and the transaction.
Otherwise deleting the only version of a policy would lift it: the next plan that
declares the policy again would add it as a new policy, which no policy judges.

This command lists those versions. With --restore it writes them back, signed by your
key, from the text of the policies in the specification: the text must have exactly the
digest the transaction lists. It needs an identity that is a reader, and an admin when
the readers file gives roles. A version whose text is not in the specification cannot be
restored this way; bring the object back from the bucket's own history, or from a copy of
the state. Removing a policy on purpose is not this: remove it from the specification and
apply, as an admin.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := newProtectRun(cmd)
			if err != nil {
				return err
			}
			return p.policies(restore)
		},
	}
	cmd.Flags().BoolVar(&restore, "restore", false, "write back the missing versions whose text the specification holds")
	return cmd
}

func (p *protectRun) policies(restore bool) error {
	if err := p.rf.require(); err != nil {
		return err
	}
	b, err := p.backend(restore)
	if err != nil {
		return err
	}
	st, id, idErr, err := p.open(b, restore, true)
	if err != nil {
		return err
	}
	if st.Protection().State != "sealed" {
		return errors.New("the state is not sealed; see pravo protect check")
	}
	if st.Locked() {
		return engine.WithCode(engine.ExitRefused, notReaderError(p.rf, id, idErr))
	}
	erf, _, rerr := p.rf.withRoles(st)
	if rerr != nil {
		return engine.WithCode(engine.ExitUntrusted, fmt.Errorf("roles: %s", printable(rerr.Error())))
	}
	missing, err := st.MissingPolicies(p.ctx, erf.Readers)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		out.Println("No policy version that a signed transaction names is missing.")
		return nil
	}
	for _, m := range missing {
		out.Printf("missing: %s (sha256:%s), listed by transaction %s\n", m.Display(), m.Digest[:12], m.Txn)
	}
	if !restore {
		return engine.WithCode(engine.ExitUntrusted, fmt.Errorf("%w: %s missing; pravo protect policies --restore writes them back from the specification", state.ErrUntrusted, plural(len(missing), "policy version is", "policy versions are")))
	}
	if err := p.needAdmin(st, "restore a policy version"); err != nil {
		return err
	}
	texts := map[string]string{}
	for _, pol := range p.root.Policies {
		texts[state.TextDigest(pol.Rule)] = pol.Rule
	}
	left := 0
	for _, m := range missing {
		text, ok := texts[m.Digest]
		if !ok {
			out.Printf("cannot restore %s: no policy of the specification has that text\n", m.Display())
			left++
			continue
		}
		if err := st.RestorePolicyVersion(p.ctx, m, text, time.Now().UTC()); err != nil {
			return err
		}
		out.Printf("restored %s\n", m.Display())
	}
	if left > 0 {
		return engine.WithCode(engine.ExitUntrusted, fmt.Errorf("%w: %s not restored", state.ErrUntrusted, plural(left, "version", "versions")))
	}
	return nil
}
