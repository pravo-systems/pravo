package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/config"
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

// scanShadowRoot finds the package of the specification in this directory and the
// names it holds at the top level, which a shadow must not take.
func scanShadowRoot(out string) (pkg string, reserved []string, err error) {
	files, _ := filepath.Glob("*.cue")
	for _, f := range files {
		if f == out {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return "", nil, err
		}
		if m := shadowPackageRE.FindSubmatch(b); m != nil && pkg == "" {
			pkg = string(m[1])
		}
		for _, m := range shadowTopRE.FindAllSubmatch(b, -1) {
			reserved = append(reserved, string(m[1]))
		}
	}
	if pkg == "" {
		return "", nil, errors.New("no specification here: declare the providers of the state in a .cue file and run pravo init")
	}
	return pkg, reserved, nil
}

// setShadowAside moves the file of an earlier run out of the root, which must not load
// it. The returned function puts it back when the run failed and removes it otherwise.
func setShadowAside(out string, force bool) (func(error), error) {
	if _, err := os.Stat(out); err != nil {
		return func(error) {}, nil
	}
	if !force {
		return nil, fmt.Errorf("%s exists; use --force to write it again", out)
	}
	aside := out + ".replaced"
	if err := os.Rename(out, aside); err != nil {
		return nil, err
	}
	return func(failed error) {
		if failed != nil {
			_ = os.Rename(aside, out)
			return
		}
		_ = os.Remove(aside)
	}, nil
}

// shadowInputs is what the engine decompiles: the records of the state and where each sat.
func shadowInputs(res *tfstate.Result) []engine.ShadowInput {
	var in []engine.ShadowInput
	for _, r := range res.Resources {
		o := res.Origins[r.Address()]
		x := engine.ShadowInput{Record: r, Name: o.Name, Key: o.Key, Keyed: o.Keyed, Address: o.Address}
		for _, st := range o.Module {
			x.Module = append(x.Module, engine.ShadowStep{Name: st.Name, Key: st.Key, Keyed: st.Keyed})
		}
		in = append(in, x)
	}
	return in
}

// shadowFlags are the flags of pravo shadow.
type shadowFlags struct {
	out, report                                                 string
	offline, keepDerived, flat, noRefs, force, noState, noGuard bool
}

// runShadow decompiles the state at place into the root of the working directory.
func runShadow(cmd *cobra.Command, rt *runContext, place string, f *shadowFlags) (err error) {
	began := time.Now()
	src := shadowSourceFlags(cmd, place)
	data, err := src.Read(cmd.Context())
	if err != nil {
		return err
	}
	res, err := tfstate.Convert(data)
	if err != nil {
		return fmt.Errorf("%s: %w", src.Name(), err)
	}
	pkg, reserved, err := scanShadowRoot(f.out)
	if err != nil {
		return err
	}
	restore, err := setShadowAside(f.out, f.force)
	if err != nil {
		return err
	}
	defer func() { restore(err) }()
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
	if !f.noState {
		if err := refuseReshadow(cmd.Context(), s, rt); err != nil {
			return err
		}
	}
	if !f.force && !f.noState && s.state.Len() > 0 {
		return fmt.Errorf("%s already holds %d resources; use --force to replace them", s.state.Dir(), s.state.Len())
	}
	vault := !f.noState && shadowVaultOpen(s.state)
	in := shadowInputs(res)
	asked := !f.offline
	eng := s.engine(rt, engine.Options{})
	sh, err := eng.Shadow(cmd.Context(), in, engine.ShadowOptions{
		Repair: asked, Minimise: asked && !f.keepDerived, Refresh: asked,
		Reserved: reserved, Flat: f.flat, NoReferences: f.noRefs, Vault: vault,
	})
	if err != nil {
		return err
	}
	header := fmt.Sprintf("package %s\n\n// Decompiled from %s by pravo shadow: %d resources of the state, no HCL read.\n\n", pkg, src.Name(), sh.Report.Resources)
	if err := os.WriteFile(f.out, []byte(header+sh.Source), 0o644); err != nil {
		return err
	}
	sh.Report.Lines = strings.Count(header+sh.Source, "\n")
	if !f.noState {
		if err := writeShadowRecords(s.state, sh, res, src.Name(), f.noGuard); err != nil {
			return err
		}
		lock := engine.NewShadowLock(src, data)
		keepShadowSecrets(rt, s.state, sh.Secrets, vault, src.Name(), lock.Serial)
		lock.Addresses = map[string]string{}
		for _, n := range sh.Report.Nodes {
			lock.Addresses[n.Address] = n.From
		}
		if f.noGuard {
			lock.Owner = "pravo"
		}
		if err := lock.Write("."); err != nil {
			return err
		}
	}
	closeSession()
	rep := sh.Report
	printShadowReport(rt, rep, src.Name(), f.out, time.Since(began))
	var verdict error
	if !f.noState {
		verdict = verifyShadow(cmd, rt, &rep)
	}
	if f.report != "" {
		type timed struct {
			engine.ShadowReport
			Seconds float64 `json:"seconds"`
		}
		if err := os.WriteFile(f.report, engineJSON(timed{rep, time.Since(began).Seconds()}), 0o644); err != nil {
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
	if !f.noState && !f.noGuard {
		rt.out.Println()
		rt.out.Println("Terraform still owns the objects: an apply here that would change one is refused. Plan as often as you like.")
		rt.out.Println("When you are ready to leave Terraform: pravo shadow take-over")
	}
	return nil
}

func shadowCmd(rt *runContext) *cobra.Command {
	f := &shadowFlags{}
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
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShadow(cmd, rt, args[0], f)
		},
	}
	cmd.Flags().StringVar(&f.out, "out", "shadow.cue", "the file to write the resources to")
	cmd.Flags().StringVar(&f.report, "report", "", "write the numbers as JSON to this file")
	cmd.Flags().BoolVar(&f.offline, "offline", false, "render from the state without asking the provider about anything: the specification may then need repair")
	cmd.Flags().BoolVar(&f.keepDerived, "keep-derived", false, "also write what the provider derives anyway")
	cmd.Flags().BoolVar(&f.flat, "flat", false, "write every resource at the top level")
	cmd.Flags().BoolVar(&f.noRefs, "no-references", false, "write every value as a literal")
	cmd.Flags().BoolVar(&f.force, "force", false, "replace the file and a non-empty state")
	cmd.Flags().BoolVar(&f.noState, "no-state", false, "write the file only: no records, no guard and no plan")
	cmd.Flags().BoolVar(&f.noGuard, "no-guard", false, "let apply change objects at once: the root is not a shadow, it takes the objects over")
	addShadowSourceFlags(cmd)
	addTagFlags(rt, cmd)
	cmd.AddCommand(shadowTakeOverCmd(rt), shadowStatusCmd(rt), shadowBackCmd(rt))
	return cmd
}

// writeShadowRecords replaces the records of the root by those of the shadow, as one
// transaction.
func writeShadowRecords(st *state.Store, sh *engine.ShadowResult, res *tfstate.Result, from string, handedOver bool) error {
	// The kind of the transaction is what says whose the objects are, in the state itself.
	kind := state.KindShadow
	if handedOver {
		kind = state.KindHandover
	}
	txn := st.Begin(kind, "pravo shadow "+from)
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

// shadowVaultOpen says whether the sensitive values that a shadow leaves out can be kept in
// the vault: the state is sealed, and the identity can write the first secret.
func shadowVaultOpen(st *state.Store) bool {
	if st.CanKeepSecrets("") != nil || st.Sealer() == nil {
		return false
	}
	return st.CanRaiseFormat(state.FormatSecret) == nil
}

// keepShadowSecrets writes the sensitive values that the specification does not hold into the
// vault as shadow/ADDRESS/ATTRIBUTE, so that they can be read, handed to a command and rotated
// with pravo secret. The records keep them as well. A value that cannot be kept is a warning:
// the shadow does not depend on the copy.
func keepShadowSecrets(rt *runContext, st *state.Store, secrets []engine.ShadowSecret, vault bool, from string, serial int64) {
	if len(secrets) == 0 {
		return
	}
	if !vault {
		rt.out.Warning(fmt.Sprintf("%d sensitive values stay in the records only: with a sealed state (pravo protect init --encrypt) they are also kept in the vault, where pravo secret reads them", len(secrets)))
		return
	}
	note := fmt.Sprintf("from the Terraform state %s, serial %d, by pravo shadow", from, serial)
	kept := 0
	for _, sec := range secrets {
		if _, err := st.SetSecret(context.Background(), sec.Name, []byte(sec.Value), note, "pravo shadow "+from); err != nil {
			rt.out.Warning(fmt.Sprintf("%s: not kept in the vault: %v", sec.Name, err))
			continue
		}
		kept++
	}
	rt.out.Printf("  sensitive values kept in the vault: %d (pravo secret list; they are named shadow/ADDRESS/ATTRIBUTE)\n", kept)
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

// shadowOwnership reads whose the objects of the root are, from its state.
func shadowOwnership(ctx context.Context, rt *runContext, root *config.Root, st *state.Store) (string, *engine.ShadowLock, error) {
	eff, _, err := readersOf(rt, root).withRoles(st)
	if err != nil {
		return "", nil, engine.WithCode(engine.ExitUntrusted, errors.New(printable(err.Error())))
	}
	owner, _, err := st.ShadowOwner(eff.Readers)
	if err != nil {
		return "", nil, err
	}
	l, err := engine.ReadShadowLock(root.Dir)
	if err != nil {
		return "", nil, err
	}
	if owner == "" && l != nil {
		owner = l.Owner
	}
	return owner, l, nil
}

// refuseReshadow refuses a new shadow over a root that was handed over to pravo: its records
// hold what pravo changed since, and a shadow replaces them with those of the Terraform state.
func refuseReshadow(ctx context.Context, s *session, rt *runContext) error {
	owner, _, err := shadowOwnership(ctx, rt, s.root, s.state)
	if err != nil || owner != state.OwnerPravo || s.state.Len() == 0 {
		return err
	}
	return engine.WithCode(engine.ExitRefused, errors.New("this root was handed over to pravo, and a new shadow would replace the records that pravo has changed since with those of the Terraform state. Take the shadow in a root of its own"))
}

// refuseShadowOwned refuses a command that changes the records of a root that Terraform
// owns (import, state rm, state mv, state import-tfstate).
func refuseShadowOwned(ctx context.Context, rt *runContext, root *config.Root, st *state.Store, what string) error {
	eff, _, err := readersOf(rt, root).withRoles(st)
	if err != nil {
		return engine.WithCode(engine.ExitUntrusted, errors.New(printable(err.Error())))
	}
	return engine.RefuseShadowEdit(ctx, st, eff.Readers, root.Dir, what)
}

func shadowTakeOverCmd(rt *runContext) *cobra.Command {
	return &cobra.Command{
		Use:   "take-over",
		Short: "hand the objects of a shadow over to pravo, so that apply may change them",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession(cmd.Context(), rt, sessionOptions{loadState: true, editsRecords: true})
			if err != nil {
				return err
			}
			defer s.close()
			owner, l, err := shadowOwnership(cmd.Context(), rt, s.root, s.state)
			if err != nil {
				return err
			}
			switch owner {
			case "":
				return errors.New("this root is not a shadow: its state holds no shadow")
			case state.OwnerPravo:
				return errors.New("the objects are pravo's already")
			}
			if l != nil {
				if m := l.Moved(cmd.Context()); m != "" {
					return fmt.Errorf("not taken over: %s", m)
				}
			}
			txn := s.state.Begin(state.KindHandover, "pravo shadow take-over")
			if err := txn.Start(); err != nil {
				return err
			}
			if err := txn.Finish("applied", "handed the objects over to pravo", true); err != nil {
				return err
			}
			from := "the Terraform state"
			if l != nil {
				l.Owner = state.OwnerPravo
				if err := l.Write(s.root.Dir); err != nil {
					return err
				}
				if l.Source != "" {
					from = l.Source
				}
			}
			rt.out.Printf("The objects are pravo's now. Stop applying %s with Terraform: remove its backend or lock it.\n", from)
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
			root, err := loadRoot(rt)
			if err != nil {
				return err
			}
			st, err := openStateReadOnly(cmd.Context(), rt, root)
			if err != nil {
				return err
			}
			defer st.Close()
			owner, l, err := shadowOwnership(cmd.Context(), rt, root, st)
			if err != nil {
				return err
			}
			if owner == "" {
				rt.out.Println("Not a shadow.")
				return nil
			}
			if l == nil {
				rt.out.Printf("Owner: %s. The note %s is gone; the state says so.\n", owner, engine.ShadowLockFile)
				return nil
			}
			rt.out.Printf("Owner: %s. Shadow of %s (serial %d, lineage %s), taken %s, %d resources.\n", owner, l.Source, l.Serial, l.Lineage, l.Taken, len(l.Addresses))
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
				return errors.New("the shadow was taken from standard input: name the state file to rewrite with --from")
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
