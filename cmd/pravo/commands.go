package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	"github.com/pravo-systems/pravo/internal/ui"
	"github.com/pravo-systems/pravo/pravo"
)

func sortStrings(s []string) { sort.Strings(s) }

func initCmd(rt *runContext) *cobra.Command {
	var upgrade bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Install providers, write the lock file and generate CUE schemas",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			root, _, err := loadOrGenerate(ctx, rt, upgrade, false)
			var none *config.NoSpecificationError
			if errors.As(err, &none) {
				return initWithoutSpecification(ctx, rt, upgrade)
			}
			if err != nil {
				return err
			}
			s, err := newSession(ctx, rt, sessionOptions{install: true, upgrade: upgrade, schemas: true, root: root, relockModules: true, regenerates: true})
			if err != nil {
				return err
			}
			defer s.close()
			locals := make([]string, 0, len(s.providers.Resolved))
			for l := range s.providers.Resolved {
				locals = append(locals, l)
			}
			sort.Strings(locals)
			rt.out.Println(rt.out.C(ui.Bold, "Providers:"))
			for _, l := range locals {
				rp := s.providers.Resolved[l]
				rt.out.Printf("  %s = %s v%s\n", l, rp.Req.Source.Display(), rp.Version)
			}
			if err := s.writeGenerated(rt, true); err != nil {
				return err
			}
			rt.out.Println()
			rt.out.Println(rt.out.C(ui.Bold+ui.Green, "pravo has been initialised.") + " Run \"pravo plan\" to see what it would do.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&upgrade, "upgrade", false, "ignore the lock file and select the newest matching versions")
	addTagFlags(rt, cmd)
	return cmd
}

// loadOrGenerate loads the specification. When it imports generated packages
// that do not exist yet, it generates them first, and reports that it did.
// Bootstrap: read the import lines and the literal requirements, generate the
// packages, and load again. A package that only an imported module needs shows
// up as a load error, so repeat with what the error names. With report set,
// each package written is listed.
func loadOrGenerate(ctx context.Context, rt *runContext, upgrade, report bool) (root *config.Root, generated bool, err error) {
	root, loadErr := loadRootWith(rt, true)
	var none *config.NoSpecificationError
	if errors.As(loadErr, &none) {
		return nil, false, loadErr // there is nothing to generate packages for
	}
	var extra []registry.Source
	for round := 0; loadErr != nil && round < 6; round++ {
		settings, err := config.LoadSettingsWith(".", config.LoadOptions{Tags: rt.flags.Tags, SysTags: rt.flags.SysTags, Env: environment(), LazyState: true})
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
			rt.out.Println(rt.out.C(ui.Yellow, "Configuration does not load yet; generating schemas for the declared providers first."))
		}
		bs, err := newSession(ctx, rt, sessionOptions{install: true, upgrade: upgrade, schemas: true, root: settings, regenerates: true})
		if err != nil {
			return nil, generated, err
		}
		err = bs.writeGenerated(rt, report)
		bs.close()
		if err != nil {
			return nil, generated, err
		}
		generated = true
		root, loadErr = loadRootWith(rt, true)
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
func initWithoutSpecification(ctx context.Context, rt *runContext, upgrade bool) error {
	settings, err := config.LoadSettingsWith(".", config.LoadOptions{Tags: rt.flags.Tags, SysTags: rt.flags.SysTags, Env: environment(), LazyState: true})
	if err != nil {
		return engine.WithCode(engine.ExitInvalid, err)
	}
	bs, err := newSession(ctx, rt, sessionOptions{install: true, upgrade: upgrade, schemas: true, root: settings, regenerates: true})
	if err != nil {
		return err
	}
	defer bs.close()
	if err := bs.writeGenerated(rt, false); err != nil {
		return err
	}
	rt.out.Println("There is no specification yet. Write a .cue file in this directory, then run \"pravo eval\" or \"pravo validate\".")
	return nil
}

func genCmd(rt *runContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen",
		Short: "Regenerate the CUE schema packages under cue.mod/gen",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// A package that the imports name and that does not exist yet is
			// generated, as init does.
			root, generated, err := loadOrGenerate(cmd.Context(), rt, false, true)
			if err != nil {
				return err
			}
			if generated {
				return nil // every package was written, and listed, on the way
			}
			s, err := newSession(cmd.Context(), rt, sessionOptions{install: true, schemas: true, root: root, regenerates: true})
			if err != nil {
				return err
			}
			defer s.close()
			return s.writeGenerated(rt, true)
		},
	}
	addTagFlags(rt, cmd)
	return cmd
}

// planTTL is how long a stored plan is kept: the default, or what --ttl says, which goes
// with --store.
func planTTL(cmd *cobra.Command, store bool, ttlText string) (time.Duration, error) {
	if !cmd.Flags().Changed("ttl") {
		return engine.DefaultPlanTTL, nil
	}
	if !store {
		return 0, errors.New("--ttl applies to --store")
	}
	ttl, err := parseSpan("ttl", ttlText)
	if err != nil {
		return 0, err
	}
	if ttl == 0 {
		return 0, fmt.Errorf("--ttl %s: want a length of time above zero", ttlText)
	}
	return ttl, nil
}

// printPlan prints the plan: as Markdown for a comment, as one JSON document, or for a
// terminal. stored is the stored plan when the plan was stored before it is printed.
func printPlan(ctx context.Context, rt *runContext, s *session, eng *engine.Engine, plan *engine.Plan, view *engine.View, policyErr error, stored *storedPlan, destroy bool) error {
	switch {
	case rt.flags.Markdown:
		opts := engine.MarkdownOptions{Refused: refusal(policyErr)}
		// A comment on a pull request may hold the plans of several environments.
		if env := s.root.Environment; env != "" {
			opts.Title = "pravo plan for " + env
			if !destroy {
				if line, err := eng.EnvironmentLine(ctx, plan); err == nil && line != "" {
					opts.Notes = append(opts.Notes, engine.MarkdownNote(line))
				}
			}
		}
		if stored != nil {
			opts.ID, opts.Expires, opts.Apply = stored.Meta.ID, stored.Meta.Expires, stored.Meta.ApplyCommand()
		}
		return emitMarkdown(view.Markdown(opts))
	case rt.flags.JSON && stored != nil:
		return emitJSON(storedPlanDoc{View: view, Stored: storedInfoOf(stored)})
	case rt.flags.JSON:
		return emitJSON(view)
	}
	rt.out.RenderPlan(plan)
	if h := hclHint(s.root); h != "" {
		rt.out.Println()
		rt.out.Println(h)
	}
	if plan.Reservation != nil {
		noun := "nodes"
		if len(plan.Reservation.Addrs) == 1 {
			noun = "node"
		}
		rt.out.Printf("%s %d %s until %s.\n", rt.out.C(ui.Dim, "Reserved"), len(plan.Reservation.Addrs), noun, plan.Reservation.Until.Local().Format(time.RFC3339))
	}
	return nil
}

func planCmd(rt *runContext) *cobra.Command {
	var refresh, outFile, ttlText string
	var destroy, full, detailed, includeSkipped, store bool
	var reserve time.Duration
	var replace []string
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Show the changes required by the configuration",
		Args:  noArgsBesidesSubcommands,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkMarkdown(rt); err != nil {
				return err
			}
			mode, err := parseRefresh(refresh)
			if err != nil {
				return err
			}
			ttl, err := planTTL(cmd, store, ttlText)
			if err != nil {
				return err
			}
			s, err := newSession(cmd.Context(), rt, sessionOptions{install: true, schemas: true, loadState: true, planning: reserve == 0})
			if err != nil {
				return err
			}
			defer s.close()
			eng := s.engine(rt, engine.Options{Refresh: mode, Destroy: destroy, Full: full, Reserve: reserve, Replace: replace})
			noteUnnamed(cmd.Context(), rt, s.root, s.state)
			plan, err := eng.Plan(cmd.Context())
			if err != nil {
				return err
			}
			// The plan is printed before it is judged, and the document that is
			// judged is the one that is printed. With --store and --json the document
			// says where the plan was stored, so the plan is judged and stored first.
			view := eng.NewView(plan, engine.ViewOptions{Skipped: includeSkipped, Redact: rt.flags.Withhold})
			var policyErr error
			var stored *storedPlan
			judged := false
			savedOut := false
			saveOut := func() error {
				if outFile == "" || savedOut {
					return nil
				}
				savedOut = true
				return eng.SavePlan(outFile, plan, rt.flags.Tags, rt.flags.SysTags)
			}
			if rt.flags.Markdown || (store && rt.flags.JSON) {
				policyErr, judged = eng.JudgePlan(cmd.Context(), plan), true
				if store && policyErr == nil && planWorth(plan) {
					// The file is written before the plan is stored, as it is without
					// --json: a plan that cannot be saved is not stored either.
					if err := saveOut(); err != nil {
						if rt.flags.JSON {
							_ = emitJSON(view)
						}
						return err
					}
					if stored, err = s.ws.StorePlan(cmd.Context(), eng, plan, pravo.StorePlanOptions{TTL: ttl}); err != nil {
						return err
					}
				}
			}
			if err := printPlan(cmd.Context(), rt, s, eng, plan, view, policyErr, stored, destroy); err != nil {
				return err
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
			if outFile != "" && !rt.flags.JSON && !rt.flags.Markdown {
				rt.out.Println()
				rt.out.Printf("Saved the plan to %s. To apply exactly these actions, run:\n  pravo apply %s\n", outFile, outFile)
			}
			switch {
			case store && stored == nil && !planWorth(plan):
				rt.out.Println()
				rt.out.Println("Nothing to store: the plan has no changes.")
			case store && stored == nil:
				if stored, err = s.ws.StorePlan(cmd.Context(), eng, plan, pravo.StorePlanOptions{TTL: ttl}); err != nil {
					return err
				}
				printStored(rt, stored)
			case store:
				printStored(rt, stored)
			case planWorth(plan):
				if overlaps := s.ws.PlanOverlaps(cmd.Context(), eng, plan); len(overlaps) > 0 {
					rt.out.Println()
					warnOverlaps(rt, overlaps)
				}
			}
			if detailed && plan.HasChanges() {
				return engine.WithCode(engine.ExitChanges, errSilent)
			}
			return nil
		},
	}
	addRunFlags(rt, cmd, &refresh, &full)
	cmd.Flags().BoolVar(&destroy, "destroy", false, "plan the destruction of everything in state")
	cmd.Flags().StringSliceVar(&replace, "replace", nil, "destroy and create this resource again even though the specification did not change (repeatable)")
	cmd.Flags().StringVar(&outFile, "out", "", "save the plan to this file, to be applied later with \"pravo apply <file>\"")
	cmd.Flags().BoolVar(&store, "store", false, "store the plan in the state, where others can list and apply it with \"pravo apply <plan-id>\"")
	cmd.Flags().StringVar(&ttlText, "ttl", "", "with --store: keep the plan this long if it is not applied (90m, 36h, 7d; the default is 7d)")
	cmd.Flags().BoolVar(&rt.flags.Markdown, "markdown", false, "print the plan as Markdown for a comment on a pull request: names and actions, never values")
	cmd.Flags().BoolVar(&detailed, "detailed-exitcode", false, "exit 2 when the plan has changes, 0 when it has none")
	cmd.Flags().BoolVar(&includeSkipped, "include-skipped", false, "with --json, list the nodes the plan left alone too, each as an entry with its address, mode, type and name")
	cmd.Flags().DurationVar(&reserve, "reserve", 0, "reserve the nodes with pending changes for this long (e.g. 15m), so other actors cannot plan or apply them meanwhile")
	// The tags select the state, so the commands that list stored plans take them too.
	pf := cmd.PersistentFlags()
	addTagFlagsTo(rt, pf)
	cmd.AddCommand(planCommands(rt)...)
	return cmd
}

func showCmd(rt *runContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <planfile|plan-id>",
		Short: "Print a saved plan, or a plan stored in the state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkMarkdown(rt); err != nil {
				return err
			}
			if isStoredRef(args[0]) {
				return showStored(rt, cmd, args[0])
			}
			pf, err := readPlanFile(cmd.Context(), rt, args[0])
			if err != nil {
				return err
			}
			rt.flags.Tags, rt.flags.SysTags = pf.Tags, pf.SysTags
			s, err := newSession(cmd.Context(), rt, sessionOptions{install: true, schemas: true, loadState: true, readOnlyState: true})
			if err != nil {
				return err
			}
			defer s.close()
			eng := s.engine(rt, engine.Options{})
			plan, stale, err := loadToShow(eng, pf)
			if err != nil {
				return err
			}
			if stale != nil {
				rt.out.Warning("this plan can no longer be applied: " + strings.Join(stale.Reasons, "; "))
			}
			if rt.flags.Markdown {
				v := showView(rt, eng, plan, pf, false)
				opts := engine.MarkdownOptions{Title: "Saved plan"}
				if stale != nil {
					opts.Notes = append(opts.Notes, "**This plan can no longer be applied:** "+strings.Join(stale.Reasons, "; "))
				}
				if err := eng.JudgeFile(cmd.Context(), pf); err != nil {
					opts.Refused = refusal(err)
				}
				return emitMarkdown(v.Markdown(opts))
			}
			if rt.flags.JSON {
				return emitJSON(map[string]any{"planfile": args[0], "stale": stale, "plan": showView(rt, eng, plan, pf, true)})
			}
			rt.out.Printf("%s created %s by %q", rt.out.C(ui.Bold, "Saved plan"), pf.Created.Local().Format("2006-01-02 15:04:05"), ui.Safe(pf.Command))
			if pf.Actor != "" {
				rt.out.Printf(" (actor %s)", ui.Safe(pf.Actor))
			}
			rt.out.Println()
			rt.out.RenderPlan(plan)
			if err := eng.JudgeFile(cmd.Context(), pf); err != nil {
				rt.out.Warning("the policies in force would refuse this plan: " + err.Error())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&rt.flags.Markdown, "markdown", false, "print the plan as Markdown for a comment on a pull request: names and actions, never values")
	addWithholdFlag(rt, cmd)
	// A plan in the state is found by the state, which the tags select.
	addTagFlags(rt, cmd)
	return cmd
}

// loadToShow loads a plan to show it: one that can no longer be applied is shown all the same,
// with the reasons in stale. Showing a plan from a file and from the store both read it here.
func loadToShow(eng *engine.Engine, pf *engine.PlanFile) (*engine.Plan, *engine.ErrStalePlan, error) {
	plan, err := eng.LoadPlan(pf)
	var stale *engine.ErrStalePlan
	if errors.As(err, &stale) {
		plan, _ = eng.LoadPlanUnchecked(pf)
		return plan, stale, nil
	}
	return plan, nil, err
}

// showView is the view of a plan that is shown, with who made it and why. full adds when it
// was made and the reservation, which the JSON form carries and the Markdown form does not.
func showView(rt *runContext, eng *engine.Engine, plan *engine.Plan, pf *engine.PlanFile, full bool) *engine.View {
	v := eng.NewView(plan, engine.ViewOptions{Redact: rt.flags.Withhold})
	v.Actor, v.Reason, v.GitRevision = pf.Actor, pf.Reason, pf.GitRevision
	if full {
		v.Created, v.Reservation = pf.Created, pf.Reservation
	}
	return v
}

// readPlanFile reads a plan file. A sealed one opens with the key of a reader of the
// state, so the state is opened to find the key. The tags that select the state are
// inside the seal, so they are given again, as they are for a stored plan, and must be
// those the plan was made with.
func readPlanFile(ctx context.Context, rt *runContext, path string) (*engine.PlanFile, error) {
	sealed, err := engine.PlanFileSealed(path)
	if err != nil || !sealed {
		return engine.ReadPlanFile(path)
	}
	s, err := newSession(ctx, rt, sessionOptions{loadState: true, readOnlyState: true})
	if err != nil {
		return nil, err
	}
	defer s.close()
	pf, err := engine.ReadPlanFileWith(path, s.state.Sealer())
	if err != nil {
		return nil, err
	}
	if !slices.Equal(sortedCopy(pf.Tags), sortedCopy(rt.flags.Tags)) || pf.SysTags != rt.flags.SysTags {
		return nil, fmt.Errorf("%s was made with other tags than these: a sealed plan file is opened in the state that its tags select, so give the tags it was made with (-t, -T)", path)
	}
	return pf, nil
}

func sortedCopy(list []string) []string {
	out := slices.Clone(list)
	slices.Sort(out)
	return out
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

func addRunFlags(rt *runContext, cmd *cobra.Command, refresh *string, full *bool) {
	addRefreshFlag(cmd, refresh)
	cmd.Flags().BoolVar(full, "full", false, "plan every resource with its provider instead of skipping unchanged ones")
	addWithholdFlag(rt, cmd)
}

// addWithholdFlag adds --withhold-sensitive to a command that prints a plan document.
func addWithholdFlag(rt *runContext, cmd *cobra.Command) {
	cmd.Flags().BoolVar(&rt.flags.Withhold, "withhold-sensitive", false, "with --json, put {\"$sensitive\": true} in place of sensitive and write-only attributes and sensitive outputs; the plan file and the policies keep the values")
}

func applyCmd(rt *runContext) *cobra.Command {
	var refresh string
	var autoApprove, full bool
	var only, replace, allowDestroy, allowCreate []string
	var webhook string
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
			if webhook != "" && (len(args) == 0 || !isStoredRef(args[0])) {
				return errors.New("--webhook applies to a stored plan: pravo apply <plan-id>")
			}
			if len(args) == 1 {
				if len(replace) > 0 {
					return errors.New("-replace is decided when the plan is made: use \"pravo plan -replace <addr> -out <file>\"")
				}
				if isStoredRef(args[0]) {
					return applyStored(rt, cmd, args[0], refresh, full, only, webhook, allowCreate)
				}
				return applySaved(rt, cmd, args[0], refresh, full, only, allowCreate)
			}
			if len(only) > 0 {
				return errors.New("-only applies to a saved plan: run \"pravo plan -out <file>\" first, then \"pravo apply <file> -only ...\"")
			}
			return runApply(rt, cmd, refresh, full, autoApprove, false, replace, allowDestroy, allowCreate)
		},
	}
	cmd.Flags().StringSliceVar(&replace, "replace", nil, "destroy and create this resource again even though the specification did not change (repeatable)")
	cmd.Flags().StringSliceVar(&allowDestroy, "allow-destroy", nil, "with --auto-approve: a resource whose $lifecycle.prevent_destroy went with its block that the plan may destroy (repeatable)")
	cmd.Flags().StringSliceVar(&allowCreate, "allow-create", nil, "a resource to create although an apply that stopped may already have created it: you know the create never reached the cloud (repeatable)")
	cmd.Flags().StringSliceVar(&only, "only", nil, "with a saved plan: apply only these resources (and nothing they depend on is left out); the rest of the plan stays in the file")
	cmd.Flags().StringVar(&webhook, "webhook", "", "with a stored plan: POST a plan.applied event, or a plan.refused one when the apply fails, as JSON to this URL")
	addRunFlags(rt, cmd, &refresh, &full)
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", false, "skip the interactive approval")
	addTagFlags(rt, cmd)
	return cmd
}

func destroyCmd(rt *runContext) *cobra.Command {
	var refresh string
	var autoApprove bool
	var allowDestroy []string
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy every resource recorded in state",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runApply(rt, cmd, refresh, true, autoApprove, true, nil, allowDestroy, nil)
		},
	}
	addRefreshFlag(cmd, &refresh)
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", false, "skip the interactive approval")
	cmd.Flags().StringSliceVar(&allowDestroy, "allow-destroy", nil, "with --auto-approve: a resource whose $lifecycle.prevent_destroy went with its block that may be destroyed (repeatable)")
	addTagFlags(rt, cmd)
	return cmd
}

// applySaved applies a plan file: no prompt, and every change is checked
// against the saved one.
func applySaved(rt *runContext, cmd *cobra.Command, path, refresh string, full bool, only, allowCreate []string) error {
	ctx := cmd.Context()
	streamEvents = true
	if sealed, _ := engine.PlanFileSealed(path); !sealed && (len(rt.flags.Tags) > 0 || rt.flags.SysTags) {
		return errors.New("-t, --env and -T cannot be combined with a saved plan; the plan carries its own tags")
	}
	if cmd.Flags().Changed("refresh") || full {
		return errors.New("-refresh and -full do not apply to a saved plan")
	}
	pf, err := readPlanFile(ctx, rt, path)
	if err != nil {
		return err
	}
	rt.flags.Tags, rt.flags.SysTags = pf.Tags, pf.SysTags
	s, err := newSession(ctx, rt, sessionOptions{install: true, schemas: true, loadState: true})
	if err != nil {
		return err
	}
	defer s.close()
	pf.SealWith(s.state.Sealer())
	if s.state.Sealer() != nil && !pf.Sealed {
		// Only a reader can seal a plan file, so a plain one in the place of a sealed one
		// may be anyone's.
		return fmt.Errorf("%s is not sealed, and the state is: a plan file of a sealed state is sealed. Make the plan again with \"pravo plan --out\"", path)
	}
	// The saved plan already refreshed what its refresh ages required; apply
	// re-plans against the recorded state without refreshing again.
	// The note that the plan was made with is the reason of the apply, unless the
	// apply was given its own: the history of the resources says why.
	if rt.flags.Reason == "" {
		rt.flags.Reason = pf.Reason
	}
	eng := s.engine(rt, engine.Options{Refresh: engine.RefreshNone, AllowCreate: allowCreate})
	eng.SetPlan("", pf.Digest)
	res, err := applyPlanFile(rt, cmd, eng, pf, path, "planfile", "pravo apply "+path, only, fileSink{path: path, pf: pf})
	if err != nil || res == nil || res.Deferred == 0 {
		return err
	}
	return saveFollowUp(ctx, rt, s, path, res.Deferred)
}

// fileSink keeps the progress of an apply in the plan file.
type fileSink struct {
	path string
	pf   *engine.PlanFile
}

func (k fileSink) Applied(_ context.Context, applied []string, txn string, _ []string) error {
	return engine.MarkApplied(k.path, k.pf, applied, txn)
}

func (k fileSink) PartlyApplied(_ context.Context, applied []string, txn string) error {
	return engine.MarkPartlyApplied(k.path, k.pf, applied, txn)
}

// Next is the default: a file has no state to look at.
func (k fileSink) Next(context.Context, string) string { return "" }

// applyPlanFile applies what is left of a plan that was saved, in a file or in the
// state, with the one sequence of pravo.ApplySavedPlan, and says what happened. name says
// which, and kind is the key under which --json repeats it. again is the command that
// applies the rest of the plan. The error is the error of the apply.
func applyPlanFile(rt *runContext, cmd *cobra.Command, eng *engine.Engine, pf *engine.PlanFile, name, kind, again string, only []string, sink pravo.SavedSink) (*engine.ApplyResult, error) {
	sa, err := pravo.ApplySavedPlan(cmd.Context(), eng, pf, pravo.SavedApplyOptions{
		Only: only, Name: name, Again: again, Sink: sink,
		Selected: func(selected, left int) { reportSelected(rt, name, selected, left) },
	})
	if sa != nil && sa.Partial != nil {
		reportPartial(rt, sa.Partial, name)
	}
	if err != nil {
		return nil, err
	}
	if sa.NothingLeft {
		reportNothingLeft(rt, kind, name)
		return nil, nil
	}
	reportApply(rt, sa.Result, sa.Destroy, kind, name, again, sa.Left)
	return sa.Result, nil
}

// reportSelected says how many of the remaining changes of a saved plan an apply with --only takes.
func reportSelected(rt *runContext, name string, selected, left int) {
	rt.out.Printf("Applying %d of %d remaining changes from %s.\n", selected, left, name)
}

// reportNothingLeft says that a saved plan has nothing left to apply.
func reportNothingLeft(rt *runContext, kind, name string) {
	if rt.flags.JSON {
		emitLine(map[string]any{"kind": "apply", "phase": "done", "message": "nothing left to apply", kind: name})
		return
	}
	rt.out.Println(rt.out.C(ui.Bold+ui.Green, "No changes.") + " The saved plan has nothing left to apply.")
}

// reportPartial says what an apply that failed had applied, and what to do with the rest.
func reportPartial(rt *runContext, p *pravo.PartialApply, name string) {
	noun, are := "changes were", "are"
	if p.Count == 1 {
		noun, are = "change was", "is"
	}
	if p.RecordErr != nil {
		rt.out.Warning(fmt.Sprintf("%d %s applied before the error and could not be recorded in %s: %v", p.Count, noun, name, p.RecordErr))
		return
	}
	if p.Count == 0 {
		return // only records were written; there is nothing to say about the plan's changes
	}
	rt.out.Printf("%d %s applied before the error and %s recorded in %s; %s.\n", p.Count, noun, are, name, p.Next)
}

func runApply(rt *runContext, cmd *cobra.Command, refresh string, full, autoApprove, destroy bool, replace, allowDestroy, allowCreate []string) error {
	ctx := cmd.Context()
	streamEvents = true
	mode, err := parseRefresh(refresh)
	if err != nil {
		return err
	}
	s, err := newSession(ctx, rt, sessionOptions{install: true, schemas: true, loadState: true})
	if err != nil {
		return err
	}
	defer s.close()
	eng := s.engine(rt, engine.Options{Refresh: mode, Destroy: destroy, Full: full, Replace: replace, AllowCreate: allowCreate})
	plan, err := eng.Plan(ctx)
	if err != nil {
		return err
	}
	view := eng.NewView(plan, engine.ViewOptions{Redact: rt.flags.Withhold})
	if rt.flags.JSON {
		emitLine(map[string]any{"kind": "plan", "phase": "done", "plan": view})
	} else {
		rt.out.RenderPlan(plan)
		if !destroy {
			if line, err := eng.EnvironmentLine(ctx, plan); err == nil && line != "" {
				rt.out.Println(line)
			}
		}
	}
	// An environment that follows another is refused here, before the question, when the
	// release was not promoted into it.
	if err := eng.CheckAdmission(ctx, plan); err != nil {
		return err
	}
	if !plan.HasWrites() {
		// The plan has nothing to change. What it still writes is the record of a
		// node whose configuration changed and whose object did not, and an output
		// whose value changed. The state of an environment notes the release it runs.
		if !destroy && (plan.NeedsApply() || s.root.Environment != "") {
			if _, err := eng.Apply(ctx, plan); err != nil {
				return err
			}
		}
		return nil
	}
	if !autoApprove && rt.flags.JSON {
		return errors.New("apply with --json needs -auto-approve, or apply a reviewed plan file")
	}
	if autoApprove {
		// A guard that went with its block is the one a person did not choose to drop
		// in this plan, so an unattended run names each resource it may destroy.
		var refused []string
		for _, addr := range plan.GuardedDestroys() {
			if !slices.Contains(allowDestroy, addr) {
				refused = append(refused, addr)
			}
		}
		if len(refused) > 0 {
			return fmt.Errorf("--auto-approve will not destroy %s: it carried $lifecycle.prevent_destroy, and its block is gone from the specification. Declare the block again, or name the resource with --allow-destroy %s", strings.Join(refused, ", "), refused[0])
		}
	}
	if !autoApprove {
		question := "Do you want to perform these actions?"
		if destroy {
			question = "Do you really want to destroy all resources?"
		}
		if ok, noInput := confirmApply(rt, question); !ok {
			if noInput {
				return errors.New("apply cancelled: not a terminal, so there was no one to ask. Use --auto-approve, or apply a saved plan file")
			}
			return errors.New("apply cancelled")
		}
	}
	opts := engine.Options{Refresh: mode, Destroy: destroy, Full: full, Replace: replace, AllowCreate: allowCreate}
	for round := 1; ; round++ {
		res, err := eng.Apply(ctx, plan)
		if err != nil {
			return err
		}
		reportApply(rt, res, destroy, "", "", "", 0)
		if res.Deferred == 0 || destroy {
			return nil
		}
		if round > maxFollowUps {
			if !rt.flags.JSON {
				rt.out.Printf("Stopped after %d plans that each deferred changes: run \"pravo plan\" to see what they wait for.\n", round)
			}
			return nil
		}
		// What the deferred changes waited for is known now: plan them, and ask again.
		if eng, plan, err = followUp(ctx, rt, s, opts, res.Deferred); err != nil || plan == nil {
			return err
		}
		if !plan.HasWrites() {
			return nil
		}
		if !autoApprove {
			if ok, _ := confirmApply(rt, "Do you want to perform these actions too?"); !ok {
				return errors.New("apply cancelled: the deferred changes are left to the next plan")
			}
		}
	}
}

// confirmApply asks the question and reports whether the answer was yes. noInput says
// that nothing could be read to answer with (a closed or empty input).
func confirmApply(rt *runContext, question string) (ok, noInput bool) {
	rt.out.Println()
	rt.out.Println(rt.out.C(ui.Bold, question))
	rt.out.Println("  Only 'yes' will be accepted to approve.")
	rt.out.Printf("\n  %s ", rt.out.C(ui.Bold, "Enter a value:"))
	line, read := readAnswer()
	rt.out.Println()
	return line == "yes", !read
}

// reportApply prints the outcome of an apply, as text or as a final JSON line.
func reportApply(rt *runContext, res *engine.ApplyResult, destroy bool, kind, planfile, again string, remaining int) {
	if rt.flags.JSON {
		if kind == "" {
			kind = "planfile"
		}
		line := map[string]any{"kind": "apply", "phase": "done", "txn": res.Txn, "added": res.Added, "changed": res.Changed,
			"destroyed": res.Destroyed, "policies": policiesWritten(res.Policies), "outputs": unwrapAll(res.Outputs), kind: planfile, "remaining": remaining}
		if res.Deferred > 0 {
			line["deferred"] = res.Deferred
		}
		emitLine(line)
		return
	}
	rt.out.Println()
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
		rt.out.Printf("Policies: +%d new, ~%d changed, -%d removed.\n", added, changed, removed)
	}
	rt.out.Printf("%s Resources: %d added, %d changed, %d destroyed. %s\n", rt.out.C(ui.Bold+ui.Green, verb+" complete!"), res.Added, res.Changed, res.Destroyed, rt.out.C(ui.Dim, "[txn "+res.Txn+"]"))
	if res.Deferred > 0 {
		noun := "changes were"
		if res.Deferred == 1 {
			noun = "change was"
		}
		rt.out.Printf("%d %s deferred to the next plan.\n", res.Deferred, noun)
	}
	if remaining > 0 {
		noun := "changes remain"
		if remaining == 1 {
			noun = "change remains"
		}
		rt.out.Printf("%d %s in %s; apply with \"%s\".\n", remaining, noun, planfile, again)
	}
	if len(res.Outputs) > 0 {
		rt.out.Println()
		rt.out.Println(outputsSummary(len(res.Outputs), res.ChangedOutputs))
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

func outputCmd(rt *runContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "output [name]",
		Short: "Print outputs recorded in state",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := loadRoot(rt)
			if err != nil {
				return err
			}
			st, err := openStateValues(cmd.Context(), rt, root)
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
	addTagFlags(rt, cmd)
	return cmd
}

func importCmd(rt *runContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import <addr> <id>",
		Short: "Bring an existing resource under management",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			s, err := newSession(ctx, rt, sessionOptions{install: true, schemas: true, loadState: true, editsRecords: true})
			if err != nil {
				return err
			}
			defer s.close()
			if err := refuseShadowOwned(ctx, rt, s.root, s.state, "import"); err != nil {
				return err
			}
			res := s.root.Resource(args[0])
			if res == nil {
				return fmt.Errorf("%s is not declared in the configuration; add it before importing", args[0])
			}
			if res.Mode != "managed" {
				return errors.New("only managed resources can be imported")
			}
			if s.state.Get(args[0]) != nil {
				return fmt.Errorf("%s is already in state", args[0])
			}
			sch := s.schemas[res.LocalProvider()].Resource(res.Type)
			// The engine hands the providers the configuration of their blocks (region, endpoint,
			// credentials) and reads the object as a plan of an $import does.
			eng := s.engine(rt, engine.Options{Command: "import"})
			rt.out.Printf("%s: Importing from ID %q...\n", args[0], args[1])
			node, err := eng.ImportRecord(ctx, res, sch, plugin.ImportTarget{ID: args[1]})
			if err != nil {
				return err
			}
			if holder := engine.HeldBy(s.state, node); holder != "" {
				return engine.ErrHeld(args[0], args[1], holder)
			}
			txn := s.state.Begin("import", "pravo import "+args[0]+" "+args[1])
			if err := txn.Start(); err != nil {
				return err
			}
			if err := s.state.Set(node, txn.ID()); err != nil {
				return err
			}
			txn.Record(args[0], "import", 0, node.Revision)
			if err := txn.Finish("applied", "imported "+args[0], true); err != nil {
				return err
			}
			rt.out.Printf("%s: Import complete. Run \"pravo plan\" to compare it with the configuration.\n", args[0])
			return nil
		},
	}
	addTagFlags(rt, cmd)
	return cmd
}

func providersCmd(rt *runContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "providers",
		Short: "List the providers the configuration requires",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession(cmd.Context(), rt, sessionOptions{install: false, loadState: true, readOnlyState: true})
			if err != nil {
				return err
			}
			defer s.close()
			locals := make([]string, 0, len(s.providers.Resolved))
			for l := range s.providers.Resolved {
				locals = append(locals, l)
			}
			sort.Strings(locals)
			if rt.flags.JSON {
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
					implied = rt.out.C(ui.Dim, " (implied by resource types)")
				}
				where := rp.Path
				if rp.Reattach != nil {
					where = "reattached at " + rp.Reattach.String() + " (not started or stopped by pravo)"
				}
				rt.out.Printf("%s = %s v%s%s\n    %s\n", rt.out.C(ui.Bold, l), rp.Req.Source.Display(), rp.Version, implied, where)
			}
			return nil
		},
	}
	cmd.AddCommand(providersSchemaCmd(rt), providersLockCmd(rt), providersCopyCmd(rt))
	addTagFlags(rt, cmd)
	return cmd
}

// providersSchemaCmd is "pravo providers schema".
func providersSchemaCmd(rt *runContext) *cobra.Command {
	cmd := &cobra.Command{
		Use: "schema <local-name>", Short: "Print a provider's full schema as CUE", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession(cmd.Context(), rt, sessionOptions{install: true, lazyState: true})
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
	}
	addTagFlags(rt, cmd)
	return cmd
}

// providersLockCmd is "pravo providers lock".
func providersLockCmd(rt *runContext) *cobra.Command {
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
			s, err := newSession(cmd.Context(), rt, sessionOptions{install: true, lazyState: true})
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
			rt.out.Printf("Pinned %d platforms in %s\n", len(list), engine.LockFileName)
			return nil
		},
	}
	addTagFlags(rt, lk)
	lk.Flags().StringVar(&lockPlats, "platforms", "", "comma-separated os_arch list (default linux_amd64,linux_arm64,darwin_arm64,darwin_amd64)")
	return lk
}

// providersCopyCmd is "pravo providers copy".
func providersCopyCmd(rt *runContext) *cobra.Command {
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
			if err := registry.NewClient(environment()).Copy(cmd.Context(), src, dst, args[1], list, func(m string) { rt.out.Println(m) }); err != nil {
				return err
			}
			ip, _ := genPackage(dst)
			rt.out.Printf("Published %s v%s\n\nImport it with: import %q\n", dst.Display(), args[1], ip)
			return nil
		},
	}
	cp.Flags().StringVar(&plats, "platforms", "", "comma-separated os_arch list (default linux_amd64,linux_arm64,darwin_arm64,darwin_amd64)")
	return cp
}

func validateCmd(rt *runContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Evaluate the configuration and check it against provider schemas",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// A specification with environments is valid when it is valid in each of them. -t
			// env=NAME checks the one.
			if _, given := config.EnvironmentOf(rt.flags.Tags); !given {
				list, err := config.ReadEnvironmentSettings(".", config.LoadOptions{Tags: rt.flags.Tags, SysTags: rt.flags.SysTags, Env: environment()})
				if err != nil {
					return engine.WithCode(engine.ExitInvalid, err)
				}
				if len(list) > 0 {
					return validateEnvironments(rt, cmd, list)
				}
			}
			s, err := newSession(cmd.Context(), rt, sessionOptions{install: true, schemas: true})
			if err != nil {
				return engine.WithCode(engine.ExitInvalid, err)
			}
			defer s.close()
			if err := engine.CheckGraph(s.root, s.value, s.schemas); err != nil {
				return engine.WithCode(engine.ExitInvalid, err)
			}
			if rt.flags.JSON {
				return emitJSON(map[string]any{"valid": true, "resources": s.root.CountMode("managed"), "data_sources": s.root.CountMode("data"),
					"ephemeral": s.root.CountMode("ephemeral"), "calls": s.root.CountMode("call"), "outputs": len(s.root.Outputs), "providers": len(s.root.Providers)})
			}
			rt.out.Printf("%s %d resources, %d data sources, %d ephemeral, %d function calls, %d outputs across %d providers.\n", rt.out.C(ui.Bold+ui.Green, "Success!"),
				s.root.CountMode("managed"), s.root.CountMode("data"), s.root.CountMode("ephemeral"), s.root.CountMode("call"), len(s.root.Outputs), len(s.root.Providers))
			if h := hclHint(s.root); h != "" {
				rt.out.Println()
				rt.out.Println(h)
			}
			return nil
		},
	}
	addTagFlags(rt, cmd)
	return cmd
}

// validateEnvironments validates the specification in each environment, and says for each what
// it declares or why it is not valid.
// rowProblem checks the row of one environment in $environments against the others: a field
// that another row has and this one does not, or a value that is not concrete, is found here
// rather than in a plan of that environment, where it would stop the run or wait for the apply.
// The fields of pravo, which begin with $, are left out.
func rowProblem(v cue.Value, envs []config.Environment, name string) string {
	all := v.LookupPath(cue.MakePath(cue.Str("$environments")))
	if !all.Exists() {
		return ""
	}
	fields := func(row cue.Value) []string {
		var out []string
		it, err := row.Fields()
		if err != nil {
			return nil
		}
		for it.Next() {
			if l := it.Selector().String(); !strings.HasPrefix(l, "$") {
				out = append(out, l)
			}
		}
		return out
	}
	mine := fields(all.LookupPath(cue.MakePath(cue.Str(name))))
	holders := map[string][]string{}
	var order []string
	for _, e := range envs {
		if e.Name == name {
			continue
		}
		for _, f := range fields(all.LookupPath(cue.MakePath(cue.Str(e.Name)))) {
			if !slices.Contains(mine, f) {
				if _, ok := holders[f]; !ok {
					order = append(order, f)
				}
				holders[f] = append(holders[f], e.Name)
			}
		}
	}
	if len(order) > 0 {
		f := order[0]
		return fmt.Sprintf("$environments.%s has no %s, which %s %s", name, f, joinAnd(holders[f]), map[bool]string{true: "has", false: "have"}[len(holders[f]) == 1])
	}
	row := all.LookupPath(cue.MakePath(cue.Str(name)))
	for _, f := range mine {
		if err := row.LookupPath(cue.ParsePath(f)).Validate(cue.Concrete(true)); err != nil {
			return fmt.Sprintf("$environments.%s.%s has no value: %s", name, f, firstLine(err))
		}
	}
	return ""
}

func validateEnvironments(rt *runContext, cmd *cobra.Command, list []config.EnvironmentSettings) error {
	saved := rt.flags.Tags
	defer func() { rt.flags.Tags = saved }()
	type result struct {
		Environment string `json:"environment"`
		Valid       bool   `json:"valid"`
		Resources   int    `json:"resources"`
		DataSources int    `json:"data_sources"`
		Outputs     int    `json:"outputs"`
		Error       string `json:"error,omitempty"`
		// Settings says why the settings of the environment cannot be read here.
		Settings string `json:"settings,omitempty"`
	}
	var results []result
	var failed []string
	for _, es := range list {
		rt.flags.Tags = config.WithEnvironment(saved, es.Name)
		r := result{Environment: es.Name}
		if es.Err != nil {
			r.Settings = firstLine(es.Err)
		}
		err := func() error {
			s, err := newSession(cmd.Context(), rt, sessionOptions{install: true, schemas: true})
			if err != nil {
				return err
			}
			defer s.close()
			// A row that misses a field is the cause of the values that are not
			// concrete because of it, so it is reported first.
			if p := rowProblem(s.value, s.root.Environments, es.Name); p != "" {
				return errors.New(p)
			}
			if err := engine.CheckGraph(s.root, s.value, s.schemas); err != nil {
				return err
			}
			r.Resources, r.DataSources, r.Outputs = s.root.CountMode("managed"), s.root.CountMode("data"), len(s.root.Outputs)
			return nil
		}()
		r.Valid = err == nil
		if err != nil {
			r.Error = err.Error()
			failed = append(failed, es.Name)
		}
		results = append(results, r)
	}
	if rt.flags.JSON {
		if err := emitJSON(map[string]any{"valid": len(failed) == 0, "environments": results}); err != nil {
			return err
		}
		if len(failed) > 0 {
			return engine.WithCode(engine.ExitInvalid, fmt.Errorf("the specification is not valid in %s", joinAnd(failed)))
		}
		return nil
	}
	// An error that several environments share, such as a mistake in $environments itself, is
	// written once, with the environments it is about.
	var texts []string
	byText := map[string][]string{}
	for _, r := range results {
		switch {
		case r.Valid:
			rt.out.Printf("%s %s: %d resources, %d data sources, %d outputs.\n", rt.out.C(ui.Green, "✓"), r.Environment, r.Resources, r.DataSources, r.Outputs)
		default:
			rt.out.Printf("%s %s\n", rt.out.C(ui.Red, "✗"), r.Environment)
			text := strings.TrimSuffix(r.Error, "\n(evaluated for the environment "+r.Environment+")")
			if _, ok := byText[text]; !ok {
				texts = append(texts, text)
			}
			byText[text] = append(byText[text], r.Environment)
		}
		if r.Settings != "" {
			rt.out.Printf("  %s\n", rt.out.C(ui.Dim, "its settings cannot be read here: "+r.Settings))
		}
	}
	if len(failed) > 0 {
		if len(texts) == 1 {
			return engine.WithCode(engine.ExitInvalid, fmt.Errorf("the specification is not valid in %s: %s", joinAnd(failed), texts[0]))
		}
		errs := make([]string, len(texts))
		for i, t := range texts {
			errs[i] = joinAnd(byText[t]) + ": " + t
		}
		return engine.WithCode(engine.ExitInvalid, fmt.Errorf("the specification is not valid in %s:\n\n%s", joinAnd(failed), strings.Join(errs, "\n\n")))
	}
	names := make([]string, len(results))
	for i, r := range results {
		names[i] = r.Environment
	}
	rt.out.Printf("%s The specification is valid in %s.\n", rt.out.C(ui.Bold+ui.Green, "Success!"), joinAnd(names))
	return nil
}

// withHiddenFields returns v with its top-level hidden fields (the locals, _x) also
// declared in the scope that a compiled expression has, so that -e _x finds them. A
// hidden field belongs to the package that declares it, and an expression compiled on
// its own is another one.
func withHiddenFields(v cue.Value) cue.Value {
	it, err := v.Fields(cue.Hidden(true))
	if err != nil {
		return v
	}
	out := v
	for it.Next() {
		sel := it.Selector()
		if sel.PkgPath() == "" || sel.IsDefinition() {
			continue
		}
		out = out.FillPath(cue.MakePath(cue.Hid(sel.String(), "_")), it.Value())
	}
	return out
}

const evalNote = `
pravo eval prints once and exits; there is no interactive prompt. Locals nested inside
a struct (a._x) are not visible to -e: reach them with a path from the top (a.b).`

func evalCmd(rt *runContext) *cobra.Command {
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
  pravo eval -e logs.tags          one expression: a path, a top-level local _x, or any CUE
  pravo eval -c                    fail unless every value is concrete
  pravo eval -e guard.rule --raw   the value as written, with its comprehensions, as a file
  pravo eval -t env=prod           inject a value for an @tag() field` + evalNote,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession(cmd.Context(), rt, sessionOptions{install: true, schemas: true, loadState: true, readOnlyState: true})
			if err != nil {
				return err
			}
			defer s.close()
			v := s.value
			// The recorded state of every resource is unified in one step: filling
			// a resource at a time evaluates the whole specification again for each.
			v = fillRecorded(v, recordedFills(s))
			if raw {
				// A path names the value itself, so that its syntax is the
				// author's; a compiled expression would be a reference to it.
				if p := cue.ParsePath(expr); expr != "" && p.Err() == nil && v.LookupPath(p).Exists() {
					v = v.LookupPath(p)
					expr = ""
				}
			}
			if expr != "" {
				v = v.Context().CompileString(expr, cue.Scope(withHiddenFields(v)), cue.InferBuiltins(true))
				if err := v.Err(); err != nil {
					return fmt.Errorf("-e %q: %s", expr, cueerrors.Details(err, nil))
				}
			}
			if concrete {
				if err := v.Validate(cue.Concrete(true)); err != nil {
					return fmt.Errorf("the value is not concrete:\n%s", cueerrors.Details(err, nil))
				}
			}
			// A conflict is an error, as in cue eval: printing it and exiting 0 let a
			// script take a specification that cannot be evaluated for one that can.
			if !raw {
				if err := v.Validate(); err != nil {
					return fmt.Errorf("the specification does not evaluate:\n%s", cueerrors.Details(err, nil))
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
	addTagFlags(rt, cmd)
	return cmd
}

// recordedFills is what the state records of every resource, as values to unify.
func recordedFills(s *session) []convert.PathFill {
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
	return fills
}

// fillRecorded unifies the recorded values of the resources into v. A value that
// the specification no longer gives, because the expression for it was changed
// since the apply, conflicts with what is recorded; the specification is what
// eval shows, so that recorded value is left out and the rest is kept.
func fillRecorded(v cue.Value, fills []convert.PathFill) cue.Value {
	all := convert.FillAll(v, fills)
	if all.Validate() == nil {
		return all
	}
	cur := v
	for _, f := range fills {
		next := convert.FillAll(cur, []convert.PathFill{f})
		if next.Validate() == nil {
			cur = next
			continue
		}
		attrs, ok := f.Value.(map[string]any)
		if !ok {
			continue
		}
		keys := make([]string, 0, len(attrs))
		for k := range attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			one := convert.FillAll(cur, []convert.PathFill{{Path: f.Path, Value: map[string]any{k: attrs[k]}}})
			if one.Validate() == nil {
				cur = one
			}
		}
	}
	return cur
}

func fmtCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "fmt",
		Short: "Format the CUE files in the current directory",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			files, err := filepath.Glob("*.cue")
			if err != nil {
				return err
			}
			unformatted := 0
			for _, f := range files {
				src, err := os.ReadFile(f)
				if err != nil {
					return err
				}
				// A file keeps the line ending it has: git on Windows checks files out with CRLF, and
				// a formatter that wants LF would call every file of such a checkout unformatted, and
				// rewrite it all, on every run.
				crlf := firstLineEndingIsCRLF(src)
				in := src
				if crlf {
					in = bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
				}
				formatted, err := format.Source(in)
				if err != nil {
					return fmt.Errorf("%s: %w", f, err)
				}
				if crlf {
					formatted = bytes.ReplaceAll(formatted, []byte("\n"), []byte("\r\n"))
				}
				if string(formatted) != string(src) {
					if !check {
						if err := os.WriteFile(f, formatted, 0o644); err != nil {
							return err
						}
					}
					unformatted++
					fmt.Println(f)
				}
			}
			if check && unformatted > 0 {
				return engine.WithCode(engine.ExitChanges, fmt.Errorf("%d file(s) are not formatted: run pravo fmt", unformatted))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "write nothing: print the files that are not formatted and exit with code 2 if there are any")
	return cmd
}

// firstLineEndingIsCRLF says whether the first line break of src is CR LF, which is the ending
// that the whole file is taken to use.
func firstLineEndingIsCRLF(src []byte) bool {
	i := bytes.IndexByte(src, '\n')
	return i > 0 && src[i-1] == '\r'
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
