package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

func promoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "promote",
		Short: "Vouch that this state runs a specification, for the environments that come after it",
		Long: `Promotion lets one specification pass through several states in order, each one
only after the one before ran it. The state before (staging) holds an attestation, a signed
object that names the specification by its fingerprint, and the state after (production) has
a policy that refuses an apply without one:

  if env == "prod" {
      promotion: pravo.#Policy & {rule: promote: {
          from:    state: path: "state/staging"
          signers: ["SHA256:…"]
          vary:    ["env"]
      }}
  }

"attest" writes the attestation, after an apply of that specification here. "list" shows the
attestations a state holds. A plan in the state after prints whether the attestation it needs
is there. The commands take -t and -T, since the tags select the state and are part of what is
vouched for. See the policy reference for the fields of promote.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	pf := cmd.PersistentFlags()
	pf.StringVar(&flags.AccessDir, "access-dir", "", accessDirUsage)
	pf.StringArrayVarP(&flags.Tags, "tag", "t", nil, tagUsage)
	pf.BoolVarP(&flags.SysTags, "inject-vars", "T", false, sysTagsUsage)
	cmd.AddCommand(promoteAttestCmd(), promoteListCmd())
	return cmd
}

// promotionJSON is an attestation in the JSON of the commands.
type promotionJSON struct {
	Fingerprint string    `json:"fingerprint"`
	Txn         string    `json:"txn,omitempty"`
	Applied     time.Time `json:"applied,omitempty"`
	At          time.Time `json:"at"`
	Signer      string    `json:"signer"`
	Key         string    `json:"key"`
	Status      string    `json:"status"`
	Current     bool      `json:"current,omitempty"`
	Note        string    `json:"note,omitempty"`
	Lineage     string    `json:"lineage,omitempty"`
}

func promoteAttestCmd() *cobra.Command {
	var note, refresh string
	cmd := &cobra.Command{
		Use:   "attest",
		Short: "Sign that this state runs the specification of the directory",
		Long: `Signs an attestation that this state runs the specification of the directory you are in,
with the tags you give. To be sure of it, it plans the specification against the state, as
"pravo plan" does, and signs only when the plan changes nothing: what the state records is what the
specification says. A state that is behind gets no attestation until it is applied. A policy of the
state after this one reads the attestation and decides whether it counts: whose key signed it, how
long ago, and that the tags it does not list as varying agree.

Vouching is a decision and not a side effect of every apply, so it is a command of its own. Run it
once the specification has done what you wanted here, for example after the checks of staging pass.
Run it again and the attestation moves to the new time. The state must be sealed, since an attestation
is a signature, and with roles in the readers file the identity must be a writer.

Give the -t and -T that the specification is applied with. Prints the lines for the policy of the
state after.`,
		Example: `  pravo apply -t env=staging
  pravo promote attest -t env=staging --note "smoke tests passed"`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := parseRefresh(refresh)
			if err != nil {
				return err
			}
			s, err := newSession(cmd.Context(), sessionOptions{install: true, schemas: true, loadState: true, planning: true})
			if err != nil {
				return promoteErr(err)
			}
			defer s.close()
			if s.state.Sealer() == nil {
				return engine.WithCode(engine.ExitRefused, errors.New("the state is not sealed, and an attestation is a signature (pravo protect init --encrypt)"))
			}
			if msg, code := roleProblem(loadReadersFile(s.root.Dir, flags.AccessDir), s.state, protect.RoleWriter, "attest a promotion"); msg != "" {
				return engine.WithCode(code, errors.New(msg))
			}
			plan, err := s.engine(engine.Options{Refresh: mode}).Plan(cmd.Context())
			if err != nil {
				return err
			}
			if planWorth(plan) {
				add, change, destroy, _ := plan.Counts()
				return engine.WithCode(engine.ExitRefused, fmt.Errorf("this state does not run the specification yet: a plan against it has %d to add, %d to change and %d to destroy. Apply it first, with the same -t, then vouch for it", add, change, destroy))
			}
			fp, err := s.root.Fingerprint()
			if err != nil {
				return err
			}
			p, err := s.state.Promote(cmd.Context(), fp, s.root.Tags, note)
			if err != nil {
				return err
			}
			lineage := s.state.Lineage()
			if flags.JSON {
				return emitJSON(promotionJSON{Fingerprint: p.Fingerprint, Txn: p.Txn, Applied: p.Applied, At: p.At, Signer: p.Signature.Signer,
					Key: p.Signature.Key, Status: "verified", Current: true, Note: p.Note, Lineage: lineage})
			}
			out.Printf("Attested: %s runs the specification %s.\n", s.state.Backend().Name(), p.Fingerprint[:12])
			out.Println("")
			out.Println("The policy of the state after this one pins this key and this state:")
			out.Printf("  signers: [%q]\n", p.Signature.Key)
			out.Printf("  lineage: %q\n", lineage)
			return nil
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "a line of text to keep with the attestation")
	cmd.Flags().StringVar(&refresh, "refresh", "", "how much of the state to read from the providers before the plan: as for plan")
	return cmd
}

func promoteListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the attestations this state holds",
		Long: `Lists the attestations in this state: which specification, who signed, when, and whether the
signature verifies. "current" marks the attestation of the specification in this directory. A key
that is not in the readers file is shown as such; whether it counts is for the policy of the state
after this one, which pins the keys it trusts. It needs no key.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := openSecrets(cmd, secretLook)
			if err != nil {
				return promoteErr(err)
			}
			defer r.close()
			fp, _ := r.root.Fingerprint()
			list, err := promotionList(r, fp)
			if err != nil {
				return err
			}
			if flags.JSON {
				return emitJSON(list)
			}
			if len(list) == 0 {
				out.Println("No attestations. Vouch for what this state runs with: pravo promote attest")
				return nil
			}
			rows := [][]string{{"SPECIFICATION", "SIGNED", "BY", "TRANSACTION", ""}}
			for _, p := range list {
				note := p.Status
				if p.Status == "verified" {
					note = ""
				}
				if p.Current {
					note = strings.TrimSpace("current " + note)
				}
				rows = append(rows, []string{p.Fingerprint[:12], ago(&p.At), safeName(p.Signer), p.Txn, note})
			}
			printTable(rows, -1)
			for _, p := range list {
				if p.Status == "invalid" {
					out.Println(out.C(ui.Red, fmt.Sprintf("Invalid: the attestation of %s by %s does not verify.", p.Fingerprint[:12], safeName(p.Signer))))
				}
			}
			return nil
		},
	}
}

// promotionList reads the attestations of the state against its lineage and the readers file.
func promotionList(r *secretRun, current string) ([]promotionJSON, error) {
	objs, err := state.ReadPromotions(r.ctx, r.st.Backend(), "")
	if err != nil {
		return nil, err
	}
	lineage := r.st.Lineage()
	list := []promotionJSON{}
	for _, o := range objs {
		if o.Promotion == nil {
			continue
		}
		p := o.Promotion
		status := "verified"
		switch p.Verify(lineage, nil) {
		case protect.SigVerified:
			if len(r.readers) > 0 {
				if _, ok := protect.RoleOf(r.readers, p.Signature.Key); !ok {
					status = "key not in the file"
				}
			}
		default:
			status = "invalid"
		}
		list = append(list, promotionJSON{Fingerprint: p.Fingerprint, Txn: p.Txn, Applied: p.Applied, At: p.At, Signer: p.Signature.Signer,
			Key: p.Signature.Key, Status: status, Current: p.Fingerprint == current, Note: p.Note, Lineage: lineage})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].At.After(list[j].At) })
	return list, nil
}

func promoteErr(err error) error { return secretErr(err) }
