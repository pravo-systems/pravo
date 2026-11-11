package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/tfstate"
)

var shadowPackageRE = regexp.MustCompile(`(?m)^package\s+([A-Za-z_][A-Za-z0-9_]*)`)
var shadowTopRE = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*:`)

// shadowSource reads the flags that say where a state is read from.
func shadowSourceFlags(cmd *cobra.Command, place string) engine.ShadowSource {
	src := engine.ShadowSource{Place: place, Stdin: cmd.InOrStdin()}
	src.Region, _ = cmd.Flags().GetString("region")
	src.Endpoint, _ = cmd.Flags().GetString("endpoint")
	src.Profile, _ = cmd.Flags().GetString("profile")
	return src
}

func addShadowSourceFlags(cmd *cobra.Command) {
	cmd.Flags().String("region", "", "the region of an s3:// source (default: the usual AWS chain)")
	cmd.Flags().String("endpoint", "", "the URL of an S3-compatible store for an s3:// source")
	cmd.Flags().String("profile", "", "the shared-configuration profile for an s3:// source")
}

func shadowCmd(rt *runContext) *cobra.Command {
	var out, report string
	var offline, keepDerived, flat, noRefs, force, noState, noGuard bool
	cmd := &cobra.Command{
		Use:   "shadow <terraform.tfstate | s3://bucket/key | ->",
		Short: "Decompile an OpenTofu or Terraform state into a specification that plans clean",
		Long: `Reads the state of an OpenTofu or Terraform root (a file, an s3:// object, or "-" for
standard input) and writes, beside the provider declarations of this root, one
resource per instance of the state, grouped by module, with the references that the
values of the state show, and the records of the objects at the addresses of those
resources. No HCL is read.

The provider is the oracle. Every resource is validated and planned against its object,
and what the provider objects to is left out or left to it, until it plans no change.
The command ends with a plan of its own: if that plan says "No changes", the
specification restates every object, and the next plan reads only what a change
reaches.

The objects stay Terraform's. Until "pravo shadow take-over", an apply that would
change one is refused, so the root can be planned as often as you like beside the
Terraform root. "pravo shadow back" writes the records into a Terraform state again.

The root must declare the providers of the state and be initialised (pravo init).`,
		Example: `  pravo shadow terraform.tfstate
  pravo shadow s3://my-tf-state/estate/terraform.tfstate --region eu-west-1
  tofu state pull | pravo shadow -`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			began := time.Now()
			src := shadowSourceFlags(cmd, args[0])
			data, err := src.Read(cmd.Context())
			if err != nil {
				return err
			}
			res, err := tfstate.Convert(data)
			if err != nil {
				return fmt.Errorf("%s: %w", src.Name(), err)
			}
			pkg, reserved := "", []string{}
			files, _ := filepath.Glob("*.cue")
			for _, f := range files {
				if f == out {
					continue
				}
				b, err := os.ReadFile(f)
				if err != nil {
					return err
				}
				if m := shadowPackageRE.FindSubmatch(b); m != nil && pkg == "" {
					pkg = string(m[1])
				}
				for _, m := range shadowTopRE.FindAllSubmatch(b, -1) {
					reserved = append(reserved, string(m[1]))
				}
			}
			if pkg == "" {
				return fmt.Errorf("no specification here: declare the providers of the state in a .cue file and run pravo init")
			}
			// The file of an earlier run must not be part of the root that is loaded. It is
			// put aside, and comes back if this run fails.
			if _, statErr := os.Stat(out); statErr == nil {
				if !force {
					return fmt.Errorf("%s exists; use --force to write it again", out)
				}
				aside := out + ".replaced"
				if err := os.Rename(out, aside); err != nil {
					return err
				}
				defer func() {
					if err != nil {
						_ = os.Rename(aside, out)
						return
					}
					_ = os.Remove(aside)
				}()
			}
			s, err := newSession(cmd.Context(), rt, sessionOptions{install: true, schemas: true, loadState: true, editsRecords: true})
			if err != nil {
				return err
			}
			closed := false
			closeSession := func() {
				if !closed {
					closed = true
					s.close()
				}
			}
			defer closeSession()
			if !force && !noState && s.state.Len() > 0 {
				return fmt.Errorf("%s already holds %d resources; use --force to replace them", s.state.Dir(), s.state.Len())
			}
			var in []engine.ShadowInput
			for _, r := range res.Resources {
				o := res.Origins[r.Address()]
				x := engine.ShadowInput{Record: r, Name: o.Name, Key: o.Key, Keyed: o.Keyed, Address: o.Address}
				for _, st := range o.Module {
					x.Module = append(x.Module, engine.ShadowStep{Name: st.Name, Key: st.Key, Keyed: st.Keyed})
				}
				in = append(in, x)
			}
			asked := !offline
			eng := s.engine(rt, engine.Options{})
			sh, err := eng.Shadow(cmd.Context(), in, engine.ShadowOptions{
				Repair: asked, Minimise: asked && !keepDerived, Refresh: asked,
				Reserved: reserved, Flat: flat, NoReferences: noRefs,
			})
			if err != nil {
				return err
			}
			header := fmt.Sprintf("package %s\n\n// Decompiled from %s by pravo shadow: %d resources of the state, no HCL read.\n\n", pkg, src.Name(), sh.Report.Resources)
			if err := os.WriteFile(out, []byte(header+sh.Source), 0o644); err != nil {
				return err
			}
			sh.Report.Lines = strings.Count(header+sh.Source, "\n")
			if !noState {
				if err := writeShadowRecords(s.state, sh, res, src.Name()); err != nil {
					return err
				}
				lock := engine.NewShadowLock(src, data)
				lock.Addresses = map[string]string{}
				for _, n := range sh.Report.Nodes {
					lock.Addresses[n.Address] = n.From
				}
				if noGuard {
					lock.Owner = "pravo"
				}
				if err := lock.Write("."); err != nil {
					return err
				}
			}
			closeSession()
			rep := sh.Report
			printShadowReport(rt, rep, src.Name(), out, time.Since(began))
			var verdict error
			if !noState {
				verdict = verifyShadow(cmd, rt, &rep)
			}
			if report != "" {
				type timed struct {
					engine.ShadowReport
					Seconds float64 `json:"seconds"`
				}
				if err := os.WriteFile(report, engineJSON(timed{rep, time.Since(began).Seconds()}), 0o644); err != nil {
					return err
				}
			}
			for _, n := range rep.Notes {
				rt.out.Warning(n)
			}
			for _, n := range res.Notes {
				rt.out.Warning(n)
			}
			if verdict != nil {
				return verdict
			}
			if !noState && !noGuard {
				rt.out.Println()
				rt.out.Println("Terraform still owns the objects: an apply here that would change one is refused. Plan as often as you like.")
				rt.out.Println("When you are ready to leave Terraform: pravo shadow take-over")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "shadow.cue", "the file to write the resources to")
	cmd.Flags().StringVar(&report, "report", "", "write the numbers as JSON to this file")
	cmd.Flags().BoolVar(&offline, "offline", false, "render from the state without asking the provider about anything: the specification may then need repair")
	cmd.Flags().BoolVar(&keepDerived, "keep-derived", false, "also write what the provider derives anyway")
	cmd.Flags().BoolVar(&flat, "flat", false, "write every resource at the top level")
	cmd.Flags().BoolVar(&noRefs, "no-references", false, "write every value as a literal")
	cmd.Flags().BoolVar(&force, "force", false, "replace the file and a non-empty state")
	cmd.Flags().BoolVar(&noState, "no-state", false, "write the file only: no records, no guard and no plan")
	cmd.Flags().BoolVar(&noGuard, "no-guard", false, "let apply change objects at once: the root is not a shadow, it takes the objects over")
	addShadowSourceFlags(cmd)
	addTagFlags(rt, cmd)
	cmd.AddCommand(shadowTakeOverCmd(rt), shadowStatusCmd(rt), shadowBackCmd(rt))
	return cmd
}

// writeShadowRecords replaces the records of the root by those of the shadow, as one
// transaction.
func writeShadowRecords(st *state.Store, sh *engine.ShadowResult, res *tfstate.Result, from string) error {
	txn := st.Begin("import", "pravo shadow "+from)
	if err := txn.WillRemove(st.Addresses()...); err != nil {
		return err
	}
	if err := txn.Start(); err != nil {
		return err
	}
	if err := st.Clear(); err != nil {
		return err
	}
	for _, r := range sh.Records {
		if err := st.Set(r, txn.ID()); err != nil {
			return err
		}
		txn.Record(r.Address(), "import", 0, r.Revision)
	}
	if err := st.SetOutputs(res.Outputs); err != nil {
		return err
	}
	return txn.Finish("applied", fmt.Sprintf("shadowed %d resources from %s", len(sh.Records), from), true)
}

func printShadowReport(rt *runContext, rep engine.ShadowReport, from, out string, took time.Duration) {
	rt.out.Printf("Shadowed %d resources in %d modules from %s into %s (%d lines) in %s\n", rep.Resources, rep.Modules, from, out, rep.Lines, took.Round(time.Millisecond))
	rt.out.Printf("  data sources dropped: %d (their values are written as constants)\n", rep.DataSources)
	rt.out.Printf("  edges of the state: %d = %d by reference + %d by interpolation + %d implied by those + %d in $depends_on + %d to data sources + %d lost\n",
		rep.Edges.State, rep.Edges.Reference, rep.Edges.Interpolation, rep.Edges.Implied, rep.Edges.DependsOn, rep.Edges.Data, rep.Edges.Lost)
	rt.out.Printf("  sensitive values kept out of the source: %d left out, %d by reference (%d more are computed and never written)\n", rep.SensitiveOmitted, rep.SensitiveReferred, rep.SensitiveComputed)
	if rep.OracleAsked {
		rt.out.Printf("  the provider on the first rendering: %d clean, %d invalid, %d with changes\n", rep.FirstClean, rep.FirstInvalid, rep.FirstChanges)
		rt.out.Printf("  the provider on what was written:    %d clean, %d invalid, %d with changes (%d questions)\n", rep.FinalClean, rep.FinalInvalid, rep.FinalChanges, rep.OracleCalls)
		kinds := make([]string, 0, len(rep.Repairs))
		for k := range rep.Repairs {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		for _, k := range kinds {
			rt.out.Printf("    %s: %d\n", k, rep.Repairs[k])
		}
	}
}

// verifyShadow plans the root that was just written, as pravo plan does. The plan reads
// every object once and writes what it finds into the records, with the hashes of the
// configuration, so that the plans after it read only what a change reaches. An empty
// plan is the proof that the specification restates the objects.
func verifyShadow(cmd *cobra.Command, rt *runContext, rep *engine.ShadowReport) error {
	s, err := newSession(cmd.Context(), rt, sessionOptions{install: true, schemas: true, loadState: true, planning: true})
	if err != nil {
		return fmt.Errorf("the specification was written, but this root cannot be planned: %w", err)
	}
	defer s.close()
	eng := s.engine(rt, engine.Options{})
	plan, err := eng.Plan(cmd.Context())
	if err != nil {
		return fmt.Errorf("the specification was written, but planning it failed: %w", err)
	}
	if !planWorth(plan) {
		// An apply of nothing writes the records again with the hash of the configuration
		// that the provider has seen, and that is what scopes the plans after it.
		if _, err := eng.Apply(cmd.Context(), plan); err != nil {
			return fmt.Errorf("the specification was written and plans clean, but recording the hashes failed: %w", err)
		}
		rep.Verified = true
		rt.out.Printf("\nVerified: the plan of the specification says No changes (%s planned, %d refreshed).\n", shadowNodes(plan.Stats.Total), plan.Stats.Refreshed)
		return nil
	}
	var wants []string
	for _, c := range plan.Changes {
		switch c.Action {
		case engine.ActionCreate, engine.ActionUpdate, engine.ActionReplace, engine.ActionDelete:
			wants = append(wants, fmt.Sprintf("%s (%s)", c.Addr, c.Action))
		}
	}
	sort.Strings(wants)
	rep.PlanWants = wants
	shown := wants
	if len(shown) > 8 {
		shown = append(append([]string(nil), shown[:8]...), fmt.Sprintf("and %d more", len(wants)-8))
	}
	return fmt.Errorf("the specification was written, but it does not restate every object: the plan wants to change %s: %s.\nRun pravo plan to see why. Nothing was changed in the cloud", shadowNodes(len(wants)), strings.Join(shown, ", "))
}

func shadowTakeOverCmd(rt *runContext) *cobra.Command {
	return &cobra.Command{
		Use:   "take-over",
		Short: "hand the objects of a shadow over to pravo, so that apply may change them",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := engine.ReadShadowLock(".")
			if err != nil {
				return err
			}
			if l == nil {
				return fmt.Errorf("this root is not a shadow: there is no %s", engine.ShadowLockFile)
			}
			if m := l.Moved(cmd.Context()); m != "" {
				return fmt.Errorf("not taken over: %s", m)
			}
			l.Owner = "pravo"
			if err := l.Write("."); err != nil {
				return err
			}
			rt.out.Printf("The objects are pravo's now. Stop applying %s with Terraform: remove its backend or lock it.\n", l.Source)
			return nil
		},
	}
}

func shadowStatusCmd(rt *runContext) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "say whose the objects of this root are",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := engine.ReadShadowLock(".")
			if err != nil {
				return err
			}
			if l == nil {
				rt.out.Println("Not a shadow.")
				return nil
			}
			rt.out.Printf("Owner: %s. Shadow of %s (serial %d, lineage %s), taken %s, %d resources.\n", l.Owner, l.Source, l.Serial, l.Lineage, l.Taken, len(l.Addresses))
			if m := l.Moved(cmd.Context()); m != "" {
				rt.out.Warning(m)
			}
			return nil
		},
	}
}

// shadowBackCmd is the reverse gear: the records of this root written back into the
// state file that the shadow was taken from.
func shadowBackCmd(rt *runContext) *cobra.Command {
	var out, from string
	cmd := &cobra.Command{
		Use:   "back",
		Short: "write the records back into a Terraform state file (format 4)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := engine.ReadShadowLock(".")
			if err != nil {
				return err
			}
			if l == nil {
				return fmt.Errorf("this root is not a shadow: there is no %s", engine.ShadowLockFile)
			}
			src := l.SourceOf()
			if from != "" {
				src = shadowSourceFlags(cmd, from)
			}
			if src.Place == "" {
				return fmt.Errorf("the shadow was taken from standard input: name the state file to rewrite with --from")
			}
			data, err := src.Read(cmd.Context())
			if err != nil {
				return err
			}
			root, err := loadRoot(rt)
			if err != nil {
				return err
			}
			st, err := openStateReadOnly(cmd.Context(), rt, root)
			if err != nil {
				return err
			}
			defer st.Close()
			back := map[string]string{}
			for addr, tf := range l.Addresses {
				back[tf] = addr
			}
			seen := map[string]bool{}
			res, stats, err := tfstate.Rewrite(data, func(tfAddr string) *tfstate.Replacement {
				addr, ok := back[tfAddr]
				if !ok {
					return nil
				}
				seen[addr] = true
				r := st.Get(addr)
				if r == nil {
					return &tfstate.Replacement{Gone: true}
				}
				return &tfstate.Replacement{Attributes: r.Attributes, SchemaVersion: r.SchemaVersion, Private: r.Private}
			})
			if err != nil {
				return err
			}
			var orphans []string
			for _, addr := range st.Addresses() {
				if r := st.Get(addr); r != nil && r.Mode == "managed" && !seen[addr] {
					orphans = append(orphans, addr)
				}
			}
			if err := os.WriteFile(out, res, 0o644); err != nil {
				return err
			}
			rt.out.Printf("Wrote %s: %d instances from the records (%d of them changed), %d kept as they were (data sources and what the shadow left out), %d gone.\n", out, stats.Rewritten, stats.Changed, stats.Kept, stats.Dropped)
			if len(orphans) > 0 {
				rt.out.Warning(fmt.Sprintf("%d resources were created here and have no place in the Terraform configuration: %s", len(orphans), strings.Join(orphans, ", ")))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "back.tfstate", "the file to write")
	cmd.Flags().StringVar(&from, "from", "", "the state file to rewrite (default: the one the shadow was taken from)")
	addShadowSourceFlags(cmd)
	addTagFlags(rt, cmd)
	return cmd
}

func shadowNodes(n int) string {
	if n == 1 {
		return "1 resource"
	}
	return fmt.Sprintf("%d resources", n)
}

func engineJSON(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return []byte("{}\n")
	}
	return append(b, '\n')
}
