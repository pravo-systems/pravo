package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	cueerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/format"
	cuejson "cuelang.org/go/encoding/json"
	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
	"github.com/pravo-systems/pravo/pravo"
)

func sortStrings(s []string) { sort.Strings(s) }

func initCmd() *cobra.Command {
	var upgrade bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Install providers, write the lock file and generate CUE schemas",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			root, _, err := loadOrGenerate(ctx, upgrade, false)
			var none *config.NoSpecificationError
			if errors.As(err, &none) {
				return initWithoutSpecification(ctx, upgrade)
			}
			if err != nil {
				return err
			}
			s, err := newSession(ctx, sessionOptions{install: true, upgrade: upgrade, schemas: true, root: root, relockModules: true, regenerates: true})
			if err != nil {
				return err
			}
			defer s.close()
			locals := make([]string, 0, len(s.providers.Resolved))
			for l := range s.providers.Resolved {
				locals = append(locals, l)
			}
			sort.Strings(locals)
			out.Println(out.C(ui.Bold, "Providers:"))
			for _, l := range locals {
				rp := s.providers.Resolved[l]
				out.Printf("  %s = %s v%s\n", l, rp.Req.Source.Display(), rp.Version)
			}
			if err := s.writeGenerated(true); err != nil {
				return err
			}
			out.Println()
			out.Println(out.C(ui.Bold+ui.Green, "pravo has been initialised.") + " Run \"pravo plan\" to see what it would do.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&upgrade, "upgrade", false, "ignore the lock file and select the newest matching versions")
	addTagFlags(cmd)
	return cmd
}

// loadOrGenerate loads the specification. When it imports generated packages
// that do not exist yet, it generates them first, and reports that it did.
// Bootstrap: read the import lines and the literal requirements, generate the
// packages, and load again. A package that only an imported module needs shows
// up as a load error, so repeat with what the error names. With report set,
// each package written is listed.
func loadOrGenerate(ctx context.Context, upgrade, report bool) (root *config.Root, generated bool, err error) {
	root, loadErr := loadRoot()
	var none *config.NoSpecificationError
	if errors.As(loadErr, &none) {
		return nil, false, loadErr // there is nothing to generate packages for
	}
	var extra []registry.Source
	for round := 0; loadErr != nil && round < 6; round++ {
		settings, err := config.LoadSettingsWith(".", config.LoadOptions{Tags: flagTags, SysTags: flagSysTags, Env: environment()})
		if err != nil {
			// The pravo block is the first thing to fix, and its error is
			// more useful than the load error about missing imports.
			return nil, generated, engine.WithCode(engine.ExitInvalid, err)
		}
		for _, src := range extra {
			known := false
			for _, pr := range settings.Providers {
				if pr.Source == src {
					known = true
				}
			}
			if !known {
				settings.Providers[src.Type] = &config.ProviderReq{LocalName: src.Type, Source: src, Protocol: config.ProtocolTfplugin, Implied: true}
			}
		}
		if round == 0 {
			out.Println(out.C(ui.Yellow, "Configuration does not load yet; generating schemas for the declared providers first."))
		}
		bs, err := newSession(ctx, sessionOptions{install: true, upgrade: upgrade, schemas: true, root: settings, regenerates: true})
		if err != nil {
			return nil, generated, err
		}
		err = bs.writeGenerated(report)
		bs.close()
		if err != nil {
			return nil, generated, err
		}
		generated = true
		root, loadErr = loadRoot()
		if loadErr == nil {
			break
		}
		missing := config.MissingProviderPackages(loadErr)
		fresh := false
		for _, m := range missing {
			have := false
			for _, e := range extra {
				if e == m {
					have = true
				}
			}
			if !have {
				extra = append(extra, m)
				fresh = true
			}
		}
		if !fresh {
			return nil, generated, loadErr
		}
	}
	if loadErr != nil {
		return nil, generated, loadErr
	}
	return root, generated, nil
}

// initWithoutSpecification initialises a directory that holds no CUE file yet:
// it creates the module and says what to write next.
func initWithoutSpecification(ctx context.Context, upgrade bool) error {
	settings, err := config.LoadSettingsWith(".", config.LoadOptions{Tags: flagTags, SysTags: flagSysTags, Env: environment()})
	if err != nil {
		return engine.WithCode(engine.ExitInvalid, err)
	}
	bs, err := newSession(ctx, sessionOptions{install: true, upgrade: upgrade, schemas: true, root: settings, regenerates: true})
	if err != nil {
		return err
	}
	defer bs.close()
	if err := bs.writeGenerated(false); err != nil {
		return err
	}
	out.Println("There is no specification yet. Write a .cue file in this directory, then run \"pravo eval\" or \"pravo validate\".")
	return nil
}

func genCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen",
		Short: "Regenerate the CUE schema packages under cue.mod/gen",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// A package that the imports name and that does not exist yet is
			// generated, as init does.
			root, generated, err := loadOrGenerate(cmd.Context(), false, true)
			if err != nil {
				return err
			}
			if generated {
				return nil // every package was written, and listed, on the way
			}
			s, err := newSession(cmd.Context(), sessionOptions{install: true, schemas: true, root: root, regenerates: true})
			if err != nil {
				return err
			}
			defer s.close()
			return s.writeGenerated(true)
		},
	}
	addTagFlags(cmd)
	return cmd
}

func planCmd() *cobra.Command {
	var refresh, outFile, ttlText string
	var destroy, full, detailed, includeSkipped, store bool
	var reserve time.Duration
	var replace []string
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show the changes required by the configuration",
		Args:  noArgsBesidesSubcommands,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := parseRefresh(refresh)
			if err != nil {
				return err
			}
			ttl := engine.DefaultPlanTTL
			if cmd.Flags().Changed("ttl") {
				if !store {
					return fmt.Errorf("--ttl applies to --store")
				}
				if ttl, err = parseSpan("ttl", ttlText); err != nil {
					return err
				}
				if ttl == 0 {
					return fmt.Errorf("--ttl %s: want a length of time above zero", ttlText)
				}
			}
			s, err := newSession(cmd.Context(), sessionOptions{install: true, schemas: true, loadState: true})
			if err != nil {
				return err
			}
			defer s.close()
			eng := s.engine(engine.Options{Refresh: mode, Destroy: destroy, Full: full, Reserve: reserve, Replace: replace})
			plan, err := eng.Plan(cmd.Context())
			if err != nil {
				return err
			}
			// The plan is printed before it is judged, and the document that is
			// judged is the one that is printed. With --store and --json the document
			// says where the plan was stored, so the plan is judged and stored first.
			view := eng.NewView(plan, engine.ViewOptions{Skipped: includeSkipped})
			var policyErr error
			var stored *storedPlan
			judged := false
			savedOut := false
			saveOut := func() error {
				if outFile == "" || savedOut {
					return nil
				}
				savedOut = true
				return eng.SavePlan(outFile, plan, flagTags, flagSysTags)
			}
			if store && flagJSON {
				policyErr, judged = eng.JudgePlan(cmd.Context(), plan), true
				if policyErr == nil && planWorth(plan) {
					// The file is written before the plan is stored, as it is without
					// --json: a plan that cannot be saved is not stored either.
					if err := saveOut(); err != nil {
						_ = emitJSON(view)
						return err
					}
					if stored, err = s.ws.StorePlan(cmd.Context(), eng, plan, pravo.StorePlanOptions{TTL: ttl}); err != nil {
						return err
					}
				}
			}
			if flagJSON {
				if stored != nil {
					err = emitJSON(storedPlanDoc{View: view, Stored: storedInfoOf(stored)})
				} else {
					err = emitJSON(view)
				}
				if err != nil {
					return err
				}
			} else {
				out.RenderPlan(plan)
				if h := hclHint(s.root); h != "" {
					out.Println()
					out.Println(h)
				}
				if plan.Reservation != nil {
					noun := "nodes"
					if len(plan.Reservation.Addrs) == 1 {
						noun = "node"
					}
					out.Printf("%s %d %s until %s.\n", out.C(ui.Dim, "Reserved"), len(plan.Reservation.Addrs), noun, plan.Reservation.Until.Local().Format(time.RFC3339))
				}
			}
			if !judged {
				policyErr = eng.JudgePlan(cmd.Context(), plan)
			}
			if policyErr != nil {
				return policyErr
			}
			if err := saveOut(); err != nil {
				return err
			}
			if outFile != "" && !flagJSON {
				out.Println()
				out.Printf("Saved the plan to %s. To apply exactly these actions, run:\n  pravo apply %s\n", outFile, outFile)
			}
			switch {
			case store && stored == nil && !planWorth(plan):
				out.Println()
				out.Println("Nothing to store: the plan has no changes.")
			case store && stored == nil:
				if stored, err = s.ws.StorePlan(cmd.Context(), eng, plan, pravo.StorePlanOptions{TTL: ttl}); err != nil {
					return err
				}
				printStored(stored)
			case store:
				printStored(stored)
			case planWorth(plan):
				if overlaps := s.ws.PlanOverlaps(cmd.Context(), eng, plan); len(overlaps) > 0 {
					out.Println()
					warnOverlaps(overlaps)
				}
			}
			if detailed && plan.HasChanges() {
				return engine.WithCode(engine.ExitChanges, errSilent)
			}
			return nil
		},
	}
	addRunFlags(cmd, &refresh, &full)
	cmd.Flags().BoolVar(&destroy, "destroy", false, "plan the destruction of everything in state")
	cmd.Flags().StringSliceVar(&replace, "replace", nil, "destroy and create this resource again even though the specification did not change (repeatable)")
	cmd.Flags().StringVar(&outFile, "out", "", "save the plan to this file, to be applied later with \"pravo apply <file>\"")
	cmd.Flags().BoolVar(&store, "store", false, "store the plan in the state, where others can list and apply it with \"pravo apply <plan-id>\"")
	cmd.Flags().StringVar(&ttlText, "ttl", "", "with --store: keep the plan this long if it is not applied (90m, 36h, 7d; the default is 7d)")
	cmd.Flags().BoolVar(&detailed, "detailed-exitcode", false, "exit 2 when the plan has changes, 0 when it has none")
	cmd.Flags().BoolVar(&includeSkipped, "include-skipped", false, "with --json, list the nodes the plan left alone too, each as an entry with its address, mode, type and name")
	cmd.Flags().DurationVar(&reserve, "reserve", 0, "reserve the nodes with pending changes for this long (e.g. 15m), so other actors cannot plan or apply them meanwhile")
	// The tags select the state, so the commands that list stored plans take them too.
	pf := cmd.PersistentFlags()
	pf.StringArrayVarP(&flagTags, "tag", "t", nil, tagUsage)
	pf.BoolVarP(&flagSysTags, "inject-vars", "T", false, sysTagsUsage)
	cmd.AddCommand(planCommands()...)
	return cmd
}

func showCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <planfile|plan-id>",
		Short: "Print a saved plan, or a plan stored in the state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if isStoredRef(args[0]) {
				return showStored(cmd, args[0])
			}
			pf, err := readPlanFile(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			flagTags, flagSysTags = pf.Tags, pf.SysTags
			s, err := newSession(cmd.Context(), sessionOptions{install: true, schemas: true, loadState: true, readOnlyState: true})
			if err != nil {
				return err
			}
			defer s.close()
			eng := s.engine(engine.Options{})
			plan, err := eng.LoadPlan(pf)
			var stale *engine.ErrStalePlan
			if errors.As(err, &stale) {
				out.Warning("this plan can no longer be applied: " + strings.Join(stale.Reasons, "; "))
				plan, _ = eng.LoadPlanUnchecked(pf)
			} else if err != nil {
				return err
			}
			if flagJSON {
				v := eng.NewView(plan, engine.ViewOptions{})
				v.Created, v.Actor, v.Reason, v.GitRevision = pf.Created, pf.Actor, pf.Reason, pf.GitRevision
				v.Reservation = pf.Reservation
				return emitJSON(map[string]any{"planfile": args[0], "stale": stale, "plan": v})
			}
			out.Printf("%s created %s by %q", out.C(ui.Bold, "Saved plan"), pf.Created.Local().Format("2006-01-02 15:04:05"), pf.Command)
			if pf.Actor != "" {
				out.Printf(" (actor %s)", pf.Actor)
			}
			out.Println()
			out.RenderPlan(plan)
			if err := eng.JudgeFile(cmd.Context(), pf); err != nil {
				out.Warning("the policies in force would refuse this plan: " + err.Error())
			}
			return nil
		},
	}
	// A plan in the state is found by the state, which the tags select.
	addTagFlags(cmd)
	return cmd
}

// readPlanFile reads a plan file. A sealed one opens with the key of a reader of the
// state, so the state is opened to find the key; the file names the tags that the
// session is opened with, so this session is for the key alone.
func readPlanFile(ctx context.Context, path string) (*engine.PlanFile, error) {
	sealed, err := engine.PlanFileSealed(path)
	if err != nil || !sealed {
		return engine.ReadPlanFile(path)
	}
	s, err := newSession(ctx, sessionOptions{loadState: true, readOnlyState: true})
	if err != nil {
		return nil, err
	}
	defer s.close()
	return engine.ReadPlanFileWith(path, s.state.Sealer())
}

// isStoredRef says whether the argument of show and apply names a plan in the
// state: it is the id of one and no file has that name.
func isStoredRef(arg string) bool {
	if !engine.IsPlanID(arg) {
		return false
	}
	_, err := os.Stat(arg)
	return err != nil
}

func addRunFlags(cmd *cobra.Command, refresh *string, full *bool) {
	cmd.Flags().StringVar(refresh, "refresh", "age", "when to read real state from providers: age (a record that the plan looks at, when it is older than its refresh age), expired (every record older than its refresh age), false (never) or all (every resource)")
	cmd.Flags().Lookup("refresh").NoOptDefVal = "all"
	cmd.Flags().BoolVar(full, "full", false, "plan every resource with its provider instead of skipping unchanged ones")
}

func applyCmd() *cobra.Command {
	var refresh string
	var autoApprove, full bool
	var only, replace []string
	cmd := &cobra.Command{
		Use:   "apply [planfile|plan-id]",
		Short: "Create, update or destroy infrastructure to match the configuration, or apply a saved plan",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				// "-refresh false" leaves "false" here as the plan file.
				if hint := refreshValueHint(cmd, args[0]); hint != "" {
					if _, err := os.Stat(args[0]); err != nil {
						return fmt.Errorf("%q is not a plan file; %s", args[0], hint)
					}
				}
			}
			if len(args) == 1 {
				if len(replace) > 0 {
					return fmt.Errorf("-replace is decided when the plan is made: use \"pravo plan -replace <addr> -out <file>\"")
				}
				if isStoredRef(args[0]) {
					return applyStored(cmd, args[0], refresh, full, only)
				}
				return applySaved(cmd, args[0], refresh, full, only)
			}
			if len(only) > 0 {
				return fmt.Errorf("-only applies to a saved plan: run \"pravo plan -out <file>\" first, then \"pravo apply <file> -only ...\"")
			}
			return runApply(cmd, refresh, full, autoApprove, false, replace)
		},
	}
	cmd.Flags().StringSliceVar(&replace, "replace", nil, "destroy and create this resource again even though the specification did not change (repeatable)")
	cmd.Flags().StringSliceVar(&only, "only", nil, "with a saved plan: apply only these resources (and nothing they depend on is left out); the rest of the plan stays in the file")
	addRunFlags(cmd, &refresh, &full)
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", false, "skip the interactive approval")
	addTagFlags(cmd)
	return cmd
}

func destroyCmd() *cobra.Command {
	var refresh string
	var autoApprove bool
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy every resource recorded in state",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runApply(cmd, refresh, true, autoApprove, true, nil)
		},
	}
	cmd.Flags().StringVar(&refresh, "refresh", "age", "when to read real state from providers: age, expired, false or all")
	cmd.Flags().Lookup("refresh").NoOptDefVal = "all"
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", false, "skip the interactive approval")
	addTagFlags(cmd)
	return cmd
}

// applySaved applies a plan file: no prompt, and every change is checked
// against the saved one.
func applySaved(cmd *cobra.Command, path, refresh string, full bool, only []string) error {
	ctx := cmd.Context()
	streamEvents = true
	if len(flagTags) > 0 || flagSysTags {
		return fmt.Errorf("-t and -T cannot be combined with a saved plan; the plan carries its own tags")
	}
	if cmd.Flags().Changed("refresh") || full {
		return fmt.Errorf("-refresh and -full do not apply to a saved plan")
	}
	pf, err := readPlanFile(ctx, path)
	if err != nil {
		return err
	}
	flagTags, flagSysTags = pf.Tags, pf.SysTags
	s, err := newSession(ctx, sessionOptions{install: true, schemas: true, loadState: true})
	if err != nil {
		return err
	}
	defer s.close()
	if s.state.Sealer() != nil && !pf.Sealed {
		// Only a reader can seal a plan file, so a plain one in the place of a sealed one
		// may be anyone's.
		return fmt.Errorf("%s is not sealed, and the state is: a plan file of a sealed state is sealed. Make the plan again with \"pravo plan --out\"", path)
	}
	// The saved plan already refreshed what its refresh ages required; apply
	// re-plans against the recorded state without refreshing again.
	// The note that the plan was made with is the reason of the apply, unless the
	// apply was given its own: the history of the resources says why.
	if flagReason == "" {
		flagReason = pf.Reason
	}
	eng := s.engine(engine.Options{Refresh: engine.RefreshNone})
	eng.SetPlan("", pf.Digest)
	mark := func(applied []string, txn string, pending []string, finished bool) error {
		mark := engine.MarkApplied
		if !finished {
			mark = engine.MarkPartlyApplied
		}
		if err := mark(path, pf, applied, txn); err != nil {
			return fmt.Errorf("the changes were applied but %s could not be updated: %w", path, err)
		}
		return nil
	}
	return applyPlanFile(cmd, eng, pf, path, "planfile", "pravo apply "+path, nil, only, mark)
}

// applyPlanFile applies what is left of a plan that was saved, in a file or in
// the state. name says which, and kind is the key under which --json repeats it.
// again is the command that applies the rest of the plan. mark records what was
// applied: the changes, the transaction and the changes that were left to apply
// when this apply began; finished says whether the apply ran to its end. An apply
// that failed or was interrupted records the changes it did finish, and the error
// it returns is the error of the apply.
func applyPlanFile(cmd *cobra.Command, eng *engine.Engine, pf *engine.PlanFile, name, kind, again string, next func() string, only []string, mark func(applied []string, txn string, pending []string, finished bool) error) error {
	ctx := cmd.Context()
	plan, err := eng.LoadPlanFor(pf, only)
	if err != nil {
		return engine.WithCode(engine.ExitStale, err)
	}
	remaining := plan.Remaining()
	if !pf.LeftToApply(remaining) {
		if flagJSON {
			emitLine(map[string]any{"kind": "apply", "phase": "done", "message": "nothing left to apply", kind: name})
			return nil
		}
		out.Println(out.C(ui.Bold+ui.Green, "No changes.") + " The saved plan has nothing left to apply.")
		return nil
	}
	// The saved plan is judged whole, whatever -only selects, so that a limit
	// on the size of a plan cannot be passed by applying it in stages.
	eng.SetPlanFile(pf)
	selected := remaining
	if len(only) > 0 {
		selected, err = plan.Subset(only)
		if err != nil {
			return err
		}
		out.Printf("Applying %d of %d remaining changes from %s.\n", selected.PendingCount(), remaining.PendingCount(), name)
	}
	res, err := eng.ApplySaved(ctx, selected)
	if err != nil {
		if res != nil && len(res.Applied) > 0 {
			if len(only) > 0 {
				again += " --only " + engine.ShellWord(strings.Join(only, ","))
			}
			recordPartial(res, name, again, next, remaining.PendingAddrs(), mark)
		}
		return err
	}
	var applied []string
	for _, ch := range selected.Changes {
		applied = append(applied, ch.Addr)
	}
	left := remaining.PendingCount() - selected.PendingCount()
	if err := mark(applied, res.Txn, remaining.PendingAddrs(), true); err != nil {
		return err
	}
	reportApply(res, plan.Destroy, kind, name, again, left)
	return nil
}

// recordPartial records in the saved plan the changes that an apply finished
// before it failed or was interrupted, and says so, so that the rest of the plan
// is applied by the same command. The error of the apply is the one the command
// returns: a mark that fails is said here and does not replace it. The notice is
// for the person who reads the output, and goes to the error stream with --json.
//
// next says what to do with the rest, once what was applied is recorded: it looks at
// the plan as it stands then, since the records that the apply changed may have made
// the rest of the plan stale. Without it the plan is a file, and the note says that
// the same command applies the rest unless the resource that failed was changed.
//
// The number is that of the changes of the plan, which pending lists, and not of the
// data sources and records that the apply also recorded: it is the number that
// "plan list" gives.
func recordPartial(res *engine.ApplyResult, name, again string, next func() string, pending []string, mark func(applied []string, txn string, pending []string, finished bool) error) {
	isChange := map[string]bool{}
	for _, a := range pending {
		isChange[a] = true
	}
	n := 0
	for _, a := range res.Applied {
		if isChange[a] {
			n++
		}
	}
	if err := mark(res.Applied, res.Txn, nil, false); err != nil {
		reportPartial(&pravo.PartialApply{Count: n, RecordErr: err}, name)
		return
	}
	step := fmt.Sprintf("run \"%s\" again to apply the rest; if the resource that failed was changed, the plan is stale and must be made again", again)
	if next != nil {
		step = next()
	}
	reportPartial(&pravo.PartialApply{Count: n, Next: step}, name)
}

// reportPartial says what an apply that failed had applied, and what to do with the rest.
func reportPartial(p *pravo.PartialApply, name string) {
	noun, are := "changes were", "are"
	if p.Count == 1 {
		noun, are = "change was", "is"
	}
	if p.RecordErr != nil {
		out.Warning(fmt.Sprintf("%d %s applied before the error and could not be recorded in %s: %v", p.Count, noun, name, p.RecordErr))
		return
	}
	if p.Count == 0 {
		return // only records were written; there is nothing to say about the plan's changes
	}
	out.Printf("%d %s applied before the error and %s recorded in %s; %s.\n", p.Count, noun, are, name, p.Next)
}

func runApply(cmd *cobra.Command, refresh string, full, autoApprove, destroy bool, replace []string) error {
	ctx := cmd.Context()
	streamEvents = true
	mode, err := parseRefresh(refresh)
	if err != nil {
		return err
	}
	s, err := newSession(ctx, sessionOptions{install: true, schemas: true, loadState: true})
	if err != nil {
		return err
	}
	defer s.close()
	eng := s.engine(engine.Options{Refresh: mode, Destroy: destroy, Full: full, Replace: replace})
	plan, err := eng.Plan(ctx)
	if err != nil {
		return err
	}
	view := eng.NewView(plan, engine.ViewOptions{})
	if flagJSON {
		emitLine(map[string]any{"kind": "plan", "phase": "done", "plan": view})
	} else {
		out.RenderPlan(plan)
	}
	if !plan.HasWrites() {
		// The plan has nothing to change. What it still writes is the record of a
		// node whose configuration changed and whose object did not, and an output
		// whose value changed.
		if !destroy && plan.NeedsApply() {
			if _, err := eng.Apply(ctx, plan); err != nil {
				return err
			}
		}
		return nil
	}
	if !autoApprove && flagJSON {
		return fmt.Errorf("apply with --json needs -auto-approve, or apply a reviewed plan file")
	}
	if !autoApprove {
		out.Println()
		if destroy {
			out.Println(out.C(ui.Bold, "Do you really want to destroy all resources?"))
		} else {
			out.Println(out.C(ui.Bold, "Do you want to perform these actions?"))
		}
		out.Println("  Only 'yes' will be accepted to approve.")
		out.Printf("\n  %s ", out.C(ui.Bold, "Enter a value:"))
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		out.Println()
		if strings.TrimSpace(line) != "yes" {
			return fmt.Errorf("apply cancelled")
		}
	}
	res, err := eng.Apply(ctx, plan)
	if err != nil {
		return err
	}
	reportApply(res, destroy, "", "", "", 0)
	return nil
}

// reportApply prints the outcome of an apply, as text or as a final JSON line.
func reportApply(res *engine.ApplyResult, destroy bool, kind, planfile, again string, remaining int) {
	if flagJSON {
		if kind == "" {
			kind = "planfile"
		}
		emitLine(map[string]any{"kind": "apply", "phase": "done", "txn": res.Txn, "added": res.Added, "changed": res.Changed,
			"destroyed": res.Destroyed, "policies": policiesWritten(res.Policies), "outputs": unwrapAll(res.Outputs), kind: planfile, "remaining": remaining})
		return
	}
	out.Println()
	verb := "Apply"
	if destroy {
		verb = "Destroy"
	}
	if len(res.Policies) > 0 {
		var added, changed, removed int
		for _, p := range res.Policies {
			switch p.Change {
			case "new":
				added++
			case "changed":
				changed++
			default:
				removed++
			}
		}
		out.Printf("Policies: +%d new, ~%d changed, -%d removed.\n", added, changed, removed)
	}
	out.Printf("%s Resources: %d added, %d changed, %d destroyed. %s\n", out.C(ui.Bold+ui.Green, verb+" complete!"), res.Added, res.Changed, res.Destroyed, out.C(ui.Dim, "[txn "+res.Txn+"]"))
	if remaining > 0 {
		noun := "changes remain"
		if remaining == 1 {
			noun = "change remains"
		}
		out.Printf("%d %s in %s; apply with \"%s\".\n", remaining, noun, planfile, again)
	}
	if len(res.Outputs) > 0 {
		out.Println()
		out.Println(outputsSummary(len(res.Outputs), res.ChangedOutputs))
	}
}

// policiesWritten is the policy versions of an apply for the JSON line: an empty
// list, never null, when the apply wrote none.
func policiesWritten(p []engine.PolicyWritten) []engine.PolicyWritten {
	if p == nil {
		return []engine.PolicyWritten{}
	}
	return p
}

// outputsSummary says how many outputs an apply recorded and which of them
// changed, and where to read them. Apply does not print their values: they
// reach logs that are kept, and "pravo output" and "pravo serve" show them.
func outputsSummary(total int, changed []string) string {
	noun := "outputs"
	if total == 1 {
		noun = "output"
	}
	what := "none changed"
	if len(changed) > 0 {
		names := make([]string, len(changed))
		for i, n := range changed {
			names[i] = convert.FieldLabel(n)
		}
		what = fmt.Sprintf("%d changed (%s)", len(changed), strings.Join(names, ", "))
	}
	return fmt.Sprintf("%d %s, %s. See them with \"pravo output\" or \"pravo serve\".", total, noun, what)
}

func printOutputs(outputs map[string]json.RawMessage, asJSON bool, indent string) {
	if asJSON {
		plain := make(map[string]json.RawMessage, len(outputs))
		for n, raw := range outputs {
			plain[n], _ = engine.UnwrapOutput(raw)
		}
		b, _ := jsonText(plain, true)
		fmt.Print(string(b))
		return
	}
	names := make([]string, 0, len(outputs))
	for n := range outputs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v, sensitive := engine.UnwrapOutput(outputs[n])
		if sensitive {
			fmt.Printf("%s%s: (sensitive)\n", indent, convert.FieldLabel(n))
			continue
		}
		fmt.Printf("%s%s: %s\n", indent, convert.FieldLabel(n), jsonToCUE(v, indent))
	}
}

func jsonToCUE(data []byte, indent string) string {
	expr, err := cuejson.Extract("", data)
	if err != nil {
		return string(data)
	}
	b, err := format.Node(expr)
	if err != nil {
		return string(data)
	}
	return strings.ReplaceAll(strings.TrimSpace(string(b)), "\n", "\n"+indent)
}

func outputCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "output [name]",
		Short: "Print outputs recorded in state",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := loadRoot()
			if err != nil {
				return err
			}
			st, err := openStateValues(cmd.Context(), root)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				v, ok := st.Outputs()[args[0]]
				if !ok {
					return fmt.Errorf("output %q is not recorded in state (run \"pravo apply\")", args[0])
				}
				v, _ = engine.UnwrapOutput(v)
				if asJSON {
					fmt.Println(string(unescapeHTML(v)))
				} else {
					fmt.Println(jsonToCUE(v, ""))
				}
				return nil
			}
			printOutputs(st.Outputs(), asJSON, "")
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	addTagFlags(cmd)
	return cmd
}

func importCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import <addr> <id>",
		Short: "Bring an existing resource under management",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := newSession(ctx, sessionOptions{install: true, schemas: true, loadState: true})
			if err != nil {
				return err
			}
			defer s.close()
			res := s.root.Resource(args[0])
			if res == nil {
				return fmt.Errorf("%s is not declared in the configuration; add it before importing", args[0])
			}
			if res.Mode != "managed" {
				return fmt.Errorf("only managed resources can be imported")
			}
			if s.state.Get(args[0]) != nil {
				return fmt.Errorf("%s is already in state", args[0])
			}
			ps := s.schemas[res.LocalProvider()]
			sch := ps.Resource(res.Type)
			prov, err := s.providers.Provider(ctx, res.ProviderKey)
			if err != nil {
				return err
			}
			imported, diags, err := prov.ImportResourceState(ctx, res.Type, plugin.ImportTarget{ID: args[1]}, typesOf(ps))
			if err != nil {
				return err
			}
			if err := diags.Err(); err != nil {
				return err
			}
			if len(imported) == 0 {
				return fmt.Errorf("provider returned nothing for %s %q", res.Type, args[1])
			}
			imp := imported[0]
			out.Printf("%s: Importing from ID %q...\n", args[0], args[1])
			newState, private, diags, err := prov.ReadResource(ctx, res.Type, imp.State, imp.Private)
			if err != nil {
				return err
			}
			if err := diags.Err(); err != nil {
				return err
			}
			if newState.IsNull() {
				return fmt.Errorf("%s: provider reports that %q does not exist", args[0], args[1])
			}
			attrs, err := convert.ToJSON(newState)
			if err != nil {
				return err
			}
			rp := s.providers.Resolved[res.LocalProvider()]
			txn := s.state.Begin("import", "pravo import "+args[0]+" "+args[1])
			if err := txn.Start(); err != nil {
				return err
			}
			protocol := ""
			if rp.Protocol != config.ProtocolTfplugin {
				protocol = rp.Protocol
			}
			node := &state.Resource{Addr: res.Address(), Mode: "managed", Type: res.Type, Name: res.Name, Provider: rp.Req.Source.String(), ProviderProtocol: protocol,
				ProviderKey: res.ProviderKey, ProviderVersion: rp.Version, SchemaVersion: sch.Version, Attributes: attrs, Private: private, RefreshedAt: state.Now()}
			if err := s.state.Set(node, txn.ID()); err != nil {
				return err
			}
			txn.Record(args[0], "import", 0, node.Revision)
			if err := txn.Finish("applied", "imported "+args[0], true); err != nil {
				return err
			}
			out.Printf("%s: Import complete. Run \"pravo plan\" to compare it with the configuration.\n", args[0])
			return nil
		},
	}
	addTagFlags(cmd)
	return cmd
}

func providersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "providers",
		Short: "List the providers the configuration requires",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession(cmd.Context(), sessionOptions{install: false, loadState: true, readOnlyState: true})
			if err != nil {
				return err
			}
			defer s.close()
			locals := make([]string, 0, len(s.providers.Resolved))
			for l := range s.providers.Resolved {
				locals = append(locals, l)
			}
			sort.Strings(locals)
			if flagJSON {
				list := []map[string]any{}
				for _, l := range locals {
					rp := s.providers.Resolved[l]
					entry := map[string]any{"name": l, "source": rp.Req.Source.String(), "version": rp.Version, "path": rp.Path, "implied": rp.Req.Implied, "protocol": rp.Protocol}
					if rp.Reattach != nil {
						entry["address"] = rp.Reattach.String()
					}
					list = append(list, entry)
				}
				return emitJSON(list)
			}
			for _, l := range locals {
				rp := s.providers.Resolved[l]
				implied := ""
				if rp.Req.Implied {
					implied = out.C(ui.Dim, " (implied by resource types)")
				}
				where := rp.Path
				if rp.Reattach != nil {
					where = "reattached at " + rp.Reattach.String() + " (not started or stopped by pravo)"
				}
				out.Printf("%s = %s v%s%s\n    %s\n", out.C(ui.Bold, l), rp.Req.Source.Display(), rp.Version, implied, where)
			}
			return nil
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use: "schema <local-name>", Short: "Print a provider's full schema as CUE", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession(cmd.Context(), sessionOptions{install: true})
			if err != nil {
				return err
			}
			defer s.close()
			rp, ok := s.providers.Resolved[args[0]]
			if !ok {
				return fmt.Errorf("provider %q is not required by this configuration", args[0])
			}
			ps, err := s.providers.Schema(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, pkg := genPackage(rp.Req.Source)
			os.Stdout.Write(schema.GenerateCUE(ps, pkg, schema.GenOptions{Header: fmt.Sprintf("Provider %s v%s", rp.Req.Source.String(), rp.Version), Source: rp.Req.Source.String()}))
			return nil
		},
	})
	var lockPlats string
	lk := &cobra.Command{
		Use:   "lock",
		Short: "Pin the archive hashes of the locked providers for more platforms",
		Long: `Record in pravo.lock.cue the sha256 of each locked provider's archive for
other platforms, so that a machine of another kind (a Linux CI runner when you
work on a Mac) installs the same bytes and rejects any others. The hashes are
the ones the provider's source states; nothing is downloaded. A hash that is
already pinned and differs is an error.

Platforms default to linux and darwin on amd64 and arm64.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := parsePlatforms(lockPlats)
			if err != nil {
				return err
			}
			s, err := newSession(cmd.Context(), sessionOptions{install: true})
			if err != nil {
				return err
			}
			defer s.close()
			for _, rp := range s.providers.Resolved {
				e, ok := s.lock.Providers[rp.Req.Source.String()]
				if !ok || rp.Req.Path != "" || rp.Reattached() {
					continue
				}
				if e.Hashes == nil {
					e.Hashes = map[string]string{}
				}
				for _, p := range list {
					sum, err := s.reg.Checksum(cmd.Context(), rp.Req.Source, e.Version, p)
					if err != nil {
						return err
					}
					sum = "sha256:" + sum
					if old, ok := e.Hashes[p.String()]; ok && old != sum {
						return fmt.Errorf("%s v%s for %s: %s pins %s, but the source now states %s", rp.Req.Source.Display(), e.Version, p, engine.LockFileName, old, sum)
					}
					e.Hashes[p.String()] = sum
				}
			}
			if err := pravo.WriteLock(s.lock, s.root.Dir); err != nil {
				return err
			}
			out.Printf("Pinned %d platforms in %s\n", len(list), engine.LockFileName)
			return nil
		},
	}
	lk.Flags().StringVar(&lockPlats, "platforms", "", "comma-separated os_arch list (default linux_amd64,linux_arm64,darwin_arm64,darwin_amd64)")
	cmd.AddCommand(lk)
	var plats string
	cp := &cobra.Command{
		Use:   "copy <source> <version> <oci://host/path/type>",
		Short: "Publish a provider version to a container registry",
		Long: `Copy one version of a provider, from a provider registry or another
container registry, into a container registry repository that pravo can then
use as the provider's $source. The archives are checked against the checksums
the source gives; the signature of the registry's release is not verified.
A published version is never replaced.

  pravo providers copy hashicorp/random 3.6.3 oci://ghcr.io/acme/providers/random

Platforms default to linux and darwin on amd64 and arm64.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := registry.ParseSource(args[0])
			if err != nil {
				return err
			}
			dst, err := registry.ParseSource(args[2])
			if err != nil {
				return err
			}
			list, err := parsePlatforms(plats)
			if err != nil {
				return err
			}
			if err := registry.NewClient(environment()).Copy(cmd.Context(), src, dst, args[1], list, func(m string) { out.Println(m) }); err != nil {
				return err
			}
			ip, _ := genPackage(dst)
			out.Printf("Published %s v%s\n\nImport it with: import %q\n", dst.Display(), args[1], ip)
			return nil
		},
	}
	cp.Flags().StringVar(&plats, "platforms", "", "comma-separated os_arch list (default linux_amd64,linux_arm64,darwin_arm64,darwin_amd64)")
	cmd.AddCommand(cp)
	addTagFlags(cmd)
	return cmd
}

func validateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Evaluate the configuration and check it against provider schemas",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession(cmd.Context(), sessionOptions{install: true, schemas: true})
			if err != nil {
				return engine.WithCode(engine.ExitInvalid, err)
			}
			defer s.close()
			if err := engine.CheckGraph(s.root, s.value); err != nil {
				return engine.WithCode(engine.ExitInvalid, err)
			}
			if flagJSON {
				return emitJSON(map[string]any{"valid": true, "resources": countMode(s.root, "managed"), "data_sources": countMode(s.root, "data"),
					"ephemeral": countMode(s.root, "ephemeral"), "calls": countMode(s.root, "call"), "outputs": len(s.root.Outputs), "providers": len(s.root.Providers)})
			}
			out.Printf("%s %d resources, %d data sources, %d ephemeral, %d function calls, %d outputs across %d providers.\n", out.C(ui.Bold+ui.Green, "Success!"),
				countMode(s.root, "managed"), countMode(s.root, "data"), countMode(s.root, "ephemeral"), countMode(s.root, "call"), len(s.root.Outputs), len(s.root.Providers))
			if h := hclHint(s.root); h != "" {
				out.Println()
				out.Println(h)
			}
			return nil
		},
	}
	addTagFlags(cmd)
	return cmd
}

func countMode(root *config.Root, mode string) int {
	n := 0
	for _, r := range root.Resources {
		if r.Mode == mode {
			n++
		}
	}
	return n
}

func evalCmd() *cobra.Command {
	var expr string
	var concrete, optional, raw bool
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Print the evaluated configuration, with recorded state filled in",
		Long: `Evaluates the specification the way CUE does and prints the result: references
resolved, comprehensions expanded, constraints unified, defaults selected, and
the recorded state of every resource filled in. Needs no provider for a file
that declares none, so it also serves to try out CUE.

  pravo eval                       the whole evaluated specification
  pravo eval -e logs.tags          one expression: a path or any CUE expression
  pravo eval -c                    fail unless every value is concrete
  pravo eval -e guard.rule --raw   the value as written, with its comprehensions, as a file
  pravo eval -t env=prod           inject a value for an @tag() field`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession(cmd.Context(), sessionOptions{install: true, schemas: true, loadState: true, readOnlyState: true})
			if err != nil {
				return err
			}
			defer s.close()
			v := s.value
			// The recorded state of every resource is unified in one step: filling
			// a resource at a time evaluates the whole specification again for each.
			var fills []convert.PathFill
			for _, r := range s.root.Resources {
				sr := s.state.Get(r.Address())
				if sr == nil {
					continue
				}
				ps := s.schemas[r.LocalProvider()]
				var sch *schema.Schema
				if r.Mode == "data" {
					sch = ps.DataSource(r.Type)
				} else {
					sch = ps.Resource(r.Type)
				}
				if sch == nil {
					continue
				}
				tv, err := tftypesFromJSON(sr.Attributes, sch)
				if err != nil {
					continue
				}
				if fill := convert.Fill(s.value.LookupPath(r.Path), sch.Block, tv); fill != nil {
					fills = append(fills, convert.PathFill{Path: r.Path, Value: fill})
				}
			}
			v = convert.FillAll(v, fills)
			if raw {
				// A path names the value itself, so that its syntax is the
				// author's; a compiled expression would be a reference to it.
				if p := cue.ParsePath(expr); expr != "" && p.Err() == nil && v.LookupPath(p).Exists() {
					v = v.LookupPath(p)
					expr = ""
				}
			}
			if expr != "" {
				v = v.Context().CompileString(expr, cue.Scope(v), cue.InferBuiltins(true))
				if err := v.Err(); err != nil {
					return fmt.Errorf("-e %q: %s", expr, cueerrors.Details(err, nil))
				}
			}
			if concrete {
				if err := v.Validate(cue.Concrete(true)); err != nil {
					return fmt.Errorf("the value is not concrete:\n%s", cueerrors.Details(err, nil))
				}
			}
			opts := []cue.Option{cue.Final(), cue.Definitions(true), cue.Hidden(false), cue.Optional(optional), cue.Docs(false), cue.Attributes(false)}
			if raw {
				opts = []cue.Option{cue.Raw(), cue.Docs(true)}
				// A policy rule prints as the policy stores it: what the author wrote,
				// without the schema it is unified with.
				if text, err := config.RuleText(v); err == nil {
					fmt.Print(text)
					return nil
				}
			}
			node := v.Syntax(opts...)
			// A struct prints as the fields of a file, without the outer braces,
			// as cue eval does.
			if st, ok := node.(*ast.StructLit); ok {
				node = &ast.File{Decls: st.Elts}
			}
			b, err := format.Node(node)
			if err != nil {
				return err
			}
			os.Stdout.Write(b)
			if len(b) > 0 && b[len(b)-1] != '\n' {
				fmt.Println()
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&expr, "expression", "e", "", "evaluate this expression, such as a path (logs.tags) or any CUE expression, in the scope of the specification")
	cmd.Flags().BoolVarP(&concrete, "concrete", "c", false, "fail unless every value is concrete")
	cmd.Flags().BoolVarP(&optional, "optional", "O", false, "also print optional fields")
	cmd.Flags().BoolVar(&raw, "raw", false, "print the value as written, before comprehensions are evaluated: a policy rule as a file for cue vet")
	addTagFlags(cmd)
	return cmd
}

func fmtCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fmt",
		Short: "Format the CUE files in the current directory",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			files, err := filepath.Glob("*.cue")
			if err != nil {
				return err
			}
			for _, f := range files {
				src, err := os.ReadFile(f)
				if err != nil {
					return err
				}
				formatted, err := format.Source(src)
				if err != nil {
					return fmt.Errorf("%s: %w", f, err)
				}
				if string(formatted) != string(src) {
					if err := os.WriteFile(f, formatted, 0o644); err != nil {
						return err
					}
					fmt.Println(f)
				}
			}
			return nil
		},
	}
}

// parsePlatforms reads a comma-separated os_arch list; empty means the
// default set.
func parsePlatforms(text string) ([]registry.Platform, error) {
	if text == "" {
		return registry.DefaultPlatforms, nil
	}
	var list []registry.Platform
	for _, p := range strings.Split(text, ",") {
		os_, arch, ok := strings.Cut(strings.TrimSpace(p), "_")
		if !ok {
			return nil, fmt.Errorf("platform %q: want os_arch, such as linux_amd64", p)
		}
		list = append(list, registry.Platform{OS: os_, Arch: arch})
	}
	return list, nil
}

// unwrapAll returns the values of recorded outputs, without their sensitivity.
func unwrapAll(outputs map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(outputs))
	for n, raw := range outputs {
		out[n], _ = engine.UnwrapOutput(raw)
	}
	return out
}
