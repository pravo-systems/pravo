package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/ui"
)

// verifyCmd checks the receipt of an apply: the signed start of its transaction names the
// plan it judged and the policy versions that judged it.
func verifyCmd() *cobra.Command {
	var planFile string
	cmd := &cobra.Command{
		Use:   "verify <txn>",
		Short: "Check that the policies a transaction lists judged its plan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			var pf *engine.PlanFile
			if planFile != "" {
				var err error
				if pf, err = readPlanFile(ctx, planFile); err != nil {
					return err
				}
				flags.Tags, flags.SysTags = pf.Tags, pf.SysTags
			}
			s, err := newSession(ctx, sessionOptions{install: true, schemas: true, loadState: true, readOnlyState: true})
			if err != nil {
				return err
			}
			defer s.close()
			txns, err := s.state.Transactions()
			if err != nil {
				return err
			}
			var matches []int
			for i, t := range txns {
				if t.ID == args[0] || strings.HasPrefix(t.ID, args[0]) {
					matches = append(matches, i)
				}
			}
			if len(matches) != 1 {
				return fmt.Errorf("%d transactions match %q; give the whole id of one (pravo state log lists them)", len(matches), args[0])
			}
			t := txns[matches[0]]
			if pf == nil {
				if t.PlanID == "" {
					return fmt.Errorf("transaction %s did not run from a stored plan: give the plan file with --plan", t.ID)
				}
				if pf, _, err = engine.NewPlanStoreFor(s.state).ReadFile(ctx, t.PlanID); err != nil {
					return fmt.Errorf("the plan %s of transaction %s is not available (give the plan file with --plan): %w", t.PlanID, t.ID, err)
				}
			}
			eng := s.engine(engine.Options{Refresh: engine.RefreshNone})
			rc, err := eng.VerifyReceipt(ctx, &t, pf)
			if err != nil {
				return err
			}
			if flags.JSON {
				return emitJSON(rc)
			}
			out.Printf("%s %s\n", out.C(ui.Bold, "Transaction"), rc.Txn)
			out.Printf("  signed by %s, pravo %s\n", rc.Signer, rc.Version)
			out.Printf("  plan      sha256:%s\n", rc.Digest)
			switch {
			case !rc.Judged:
				out.Println("  no resource was changed, so no policy judged it")
			case len(rc.Policies) == 0:
				out.Println("  no policy was in force")
			default:
				out.Printf("  judged by %d policy versions:\n", len(rc.Policies))
				for _, p := range rc.Policies {
					out.Printf("    %s\n", p)
				}
			}
			if rc.Unknown > 0 {
				out.Printf("  %d of them were signed by a key that is not in the readers file\n", rc.Unknown)
			}
			out.Println(out.C(ui.Bold+ui.Green, "Verified."))
			return nil
		},
	}
	cmd.Flags().StringVar(&planFile, "plan", "", "the plan file the apply judged, for a plan that is not stored")
	return cmd
}
