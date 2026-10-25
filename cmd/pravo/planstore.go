package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

// opportunisticGC is the most plans that "plan --store" removes on its way.
const opportunisticGC = 25

// planCommands are the subcommands of "pravo plan" that work on stored plans.
func planCommands() []*cobra.Command {
	return []*cobra.Command{planListCmd(), planShowCmd(), planPullCmd(), planGCCmd()}
}

// parseSpan reads a length of time: a number and a unit, as in 90m, 36h or 7d.
func parseSpan(flag, v string) (time.Duration, error) {
	n, isDays := strings.CutSuffix(v, "d")
	if isDays {
		days, err := strconv.ParseFloat(n, 64)
		if err != nil || days < 0 {
			return 0, fmt.Errorf("--%s %q: want a length of time such as 90m, 36h or 7d", flag, v)
		}
		return time.Duration(days * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("--%s %q: want a length of time such as 90m, 36h or 7d", flag, v)
	}
	return d, nil
}

// openPlanStore loads the specification, only to find the state, and opens the
// plan store in it. Nothing is installed or evaluated beyond the specification.
// write opens the state for changes; otherwise nothing is created or changed.
func openPlanStore(ctx context.Context, write bool) (*state.Store, *engine.PlanStore, error) {
	root, err := loadRoot()
	if err != nil {
		return nil, nil, err
	}
	var st *state.Store
	if write {
		st, err = openState(ctx, root)
	} else {
		st, err = openStateReadOnly(ctx, root)
	}
	if err != nil {
		return nil, nil, err
	}
	return st, engine.NewPlanStoreFor(st), nil
}

// storedPlanJSON is how --json describes a stored plan.
type storedPlanJSON struct {
	ID           string             `json:"id"`
	Status       engine.PlanStatus  `json:"status"`
	Author       string             `json:"author"`
	Actor        string             `json:"actor,omitempty"`
	Reason       string             `json:"reason,omitempty"`
	Command      string             `json:"command,omitempty"`
	GitRevision  string             `json:"git_revision,omitempty"`
	Created      time.Time          `json:"created"`
	Expires      time.Time          `json:"expires"`
	Summary      engine.PlanCounts  `json:"summary"`
	Changes      []engine.PlanTouch `json:"changes"`
	Applied      int                `json:"applied,omitempty"`
	StaleReasons []string           `json:"stale_reasons,omitempty"`
	// Conflicts, ApplyOnly and ApplyOnlyCommand describe a partly-stale plan: the
	// changes that cannot be applied now, with the reasons; the addresses that
	// "--only" takes to apply the rest, and the command that does it.
	Conflicts        map[string][]string     `json:"conflicts,omitempty"`
	ApplyOnly        []string                `json:"apply_only,omitempty"`
	ApplyOnlyCommand string                  `json:"apply_only_command,omitempty"`
	Reservation      *engine.ReservationView `json:"reservation,omitempty"`
	// BeingCollected is set while a collection holds the plan: an apply from it is refused.
	BeingCollected bool   `json:"being_collected,omitempty"`
	Size           int    `json:"size"`
	ApplyCommand   string `json:"apply_command"`
}

func storedPlanView(e *engine.PlanEntry) storedPlanJSON {
	m := e.Meta
	v := storedPlanJSON{
		ID: m.ID, Status: e.Status, Author: m.Author, Actor: m.Actor, Reason: m.Reason, Command: m.Command, GitRevision: m.GitRevision,
		Created: m.Created, Expires: m.Expires, Summary: m.Summary, Changes: m.Changes, Applied: e.AppliedCount(),
		StaleReasons: e.Reasons, Reservation: m.Reservation, BeingCollected: e.Progress.BeingCollected(time.Now()), Size: m.Size, ApplyCommand: m.ApplyCommand(),
	}
	if e.Status == engine.PlanPartlyStale {
		// The reasons of a partly-stale plan are its conflicts.
		v.StaleReasons, v.Conflicts, v.ApplyOnly, v.ApplyOnlyCommand = nil, e.Conflicts, e.Live, e.ApplyLiveCommand()
	}
	return v
}

// counts writes the numbers of a plan as the plan prints its summary: +2 ~1 -0.
func counts(c engine.PlanCounts) string {
	return fmt.Sprintf("+%d ~%d -%d", c.Add, c.Change, c.Destroy)
}

// since writes how long ago t was.
func since(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	return engine.DurationText(d) + " ago"
}

// expiry writes when an unapplied plan is collected.
func expiry(m *engine.PlanMeta) string {
	d := time.Until(m.Expires)
	if d <= 0 {
		return "expired " + engine.DurationText(-d) + " ago"
	}
	return "expires in " + engine.DurationText(d)
}

func statusText(s engine.PlanStatus) string {
	switch s {
	case engine.PlanPending:
		return out.C(ui.Green, string(s))
	case engine.PlanPartial, engine.PlanPartlyStale:
		return out.C(ui.Yellow, string(s))
	case engine.PlanStale, engine.PlanExpired:
		return out.C(ui.Red, string(s))
	}
	return out.C(ui.Dim, string(s))
}

func planListCmd() *cobra.Command {
	var pending, all bool
	var touching string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the plans stored in the state",
		Args:  noArgs,
		Long: `Lists the plans that were stored with "pravo plan --store": who made each, how
long ago, where it stands and what it changes. A plan stands as pending (it can
be applied), partial (some of it was applied and the rest can be), partly-stale
(the state moved on under some of the changes, and the others can be applied by
name with --only), stale (the state moved on, so it can no longer be applied),
applied, or expired.

By default the plans that can still be applied, all of them or a part, are
listed. --all lists every plan that has not been collected yet. A plan is judged
by the state alone; the configuration and the providers are compared when the
plan is applied.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if pending && all {
				return fmt.Errorf("--pending and --all exclude each other")
			}
			st, ps, err := openPlanStore(cmd.Context(), false)
			if err != nil {
				return err
			}
			entries, skipped, err := ps.EntriesAndSkipped(cmd.Context(), engine.PlanCheckFor(st))
			if err != nil {
				return err
			}
			unsealed := unsealedPlans(entries)
			if touching != "" {
				entries = engine.Touching(entries, touching)
			}
			var shown []*engine.PlanEntry
			for _, e := range entries {
				if all || e.Status.Open() {
					shown = append(shown, e)
				}
			}
			if flagJSON {
				list := make([]storedPlanJSON, 0, len(shown))
				for _, e := range shown {
					list = append(list, storedPlanView(e))
				}
				doc := map[string]any{"plans": list}
				if len(skipped) > 0 {
					doc["unreadable"] = unreadablePlans(skipped)
				}
				if len(unsealed) > 0 {
					doc["unsealed"] = unsealed
				}
				return emitJSON(doc)
			}
			defer warnUnreadable(skipped)
			defer warnUnsealed(unsealed)
			if len(shown) == 0 {
				switch {
				case touching != "":
					out.Println("No stored plan changes " + touching + ".")
				case len(entries) > 0:
					out.Println("No stored plan can be applied. \"pravo plan list --all\" lists the others.")
				default:
					out.Println("No plans are stored. \"pravo plan --store\" stores one.")
				}
				return nil
			}
			width, ageWidth, countWidth, statusWidth := 0, 0, 0, 7
			for _, e := range shown {
				statusWidth = max(statusWidth, len(e.Status))
				width = max(width, len(e.Meta.Author))
				ageWidth = max(ageWidth, len(since(e.Meta.Created)))
				countWidth = max(countWidth, len(counts(e.Meta.Summary)))
			}
			for _, e := range shown {
				m := e.Meta
				note := expiry(m)
				switch {
				case e.Status == engine.PlanApplied:
					note = "applied " + since(*e.Progress.DoneAt)
				case e.Status == engine.PlanPartlyStale:
					note = fmt.Sprintf("%d of %d unapplied changes conflict: %s; apply with %s", len(e.Conflicts), e.Unapplied(), e.FirstConflict(), e.OnlyFlag())
				case len(e.Reasons) > 0:
					note = e.Reasons[0]
				case e.Status == engine.PlanPartial:
					note = fmt.Sprintf("%d of %d changes applied; %s", e.AppliedCount(), len(m.Changes), expiry(m))
				}
				if r := m.Reservation; r != nil && r.Until.After(time.Now()) && e.Status.Open() {
					note += "; reserved until " + r.Until.Local().Format("15:04")
				}
				out.Printf("%s  %s  %-*s  %-*s  %-*s  %s\n", m.ID, pad(statusText(e.Status), string(e.Status), statusWidth), width, m.Author, ageWidth, since(m.Created), countWidth, counts(m.Summary), out.C(ui.Dim, note))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&pending, "pending", false, "list the plans that can still be applied (the default)")
	cmd.Flags().BoolVar(&all, "all", false, "list every stored plan, including stale, applied and expired ones")
	cmd.Flags().StringVar(&touching, "touching", "", "list the plans that change this resource, e.g. aws_s3_bucket.logs")
	return cmd
}

type unreadablePlanJSON struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

func unreadablePlans(skipped []engine.SkippedPlan) []unreadablePlanJSON {
	out := make([]unreadablePlanJSON, 0, len(skipped))
	for _, sk := range skipped {
		out = append(out, unreadablePlanJSON{ID: sk.ID, Error: sk.Err.Error()})
	}
	return out
}

// unsealedPlans returns the ids of the plans of a sealed state that were stored
// before it was sealed and are not sealed yet.
func unsealedPlans(entries []*engine.PlanEntry) []string {
	var ids []string
	for _, e := range entries {
		if e.Meta != nil && e.Meta.Unsealed {
			ids = append(ids, e.Meta.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// warnUnsealed says which stored plans are not sealed in a sealed state.
func warnUnsealed(ids []string) {
	if len(ids) == 0 {
		return
	}
	out.Warning(fmt.Sprintf("stored before the state was sealed and not sealed: %s. Anyone who can read the bucket can read their values; \"pravo protect rewrap --all\" seals the plans that nothing uses", strings.Join(ids, ", ")))
}

// warnUnreadable says which stored plans a listing could not read, and why.
func warnUnreadable(skipped []engine.SkippedPlan) {
	for _, sk := range skipped {
		out.Warning(fmt.Sprintf("%s is not listed: %v", sk.ID, sk.Err))
	}
}

// pad adds spaces to the coloured text of a word, so that columns line up, as
// if the word were not coloured.
func pad(coloured, plain string, width int) string {
	if n := width - len(plain); n > 0 {
		return coloured + strings.Repeat(" ", n)
	}
	return coloured
}

func planShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <plan-id>",
		Short: "Print a plan stored in the state",
		Args:  cobra.ExactArgs(1),
		Long:  "Prints a stored plan as \"pravo show\" prints a plan file, after a header that says who made it, when it expires and how to apply it. Give the -t and -T that the plan was made with.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return showStored(cmd, args[0])
		},
	}
}

func planPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull <plan-id> <file>",
		Short: "Write a stored plan to a plan file",
		Args:  cobra.ExactArgs(2),
		Long: `Writes the plan to a file, as "pravo plan --out" would have written it, with the
changes already applied from it marked. The file can be applied, shown and sent
to someone, like any plan file. Applying the file records its progress in the
file and not in the state.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, ps, err := openPlanStore(cmd.Context(), false)
			if err != nil {
				return err
			}
			id, err := ps.Resolve(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := ps.Pull(cmd.Context(), id, args[1]); err != nil {
				return err
			}
			if !flagJSON {
				out.Printf("Wrote %s to %s. To apply exactly these actions, run:\n  pravo apply %s\n", id, args[1], args[1])
			} else {
				return emitJSON(map[string]any{"id": id, "planfile": args[1]})
			}
			return nil
		},
	}
}

func planGCCmd() *cobra.Command {
	var dryRun bool
	var olderThan string
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Remove the stored plans that are no longer needed",
		Args:  noArgs,
		Long: `Removes stored plans that nobody will apply:

  - a plan that was not applied is removed when it expires, seven days after it
    was made unless "pravo plan --store --ttl" said otherwise;
  - a plan that cannot be applied any more because the state moved on is removed
    an hour after the first collection saw it so;
  - a plan that was applied completely is removed a day later;
  - with --older-than, a plan that was not applied and was made longer ago than
    that is removed too.

A plan that an apply is running from, that is being removed by another
collection, or that holds a reservation that has not ended is never removed.
Nothing but the objects of stored plans is removed. "pravo plan --store" runs a
small collection of its own.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var opts engine.GCOptions
			if olderThan != "" {
				d, err := parseSpan("older-than", olderThan)
				if err != nil {
					return err
				}
				if d == 0 {
					return fmt.Errorf("--older-than %s: want a length of time above zero", olderThan)
				}
				opts.OlderThan = d
			}
			st, ps, err := openPlanStore(cmd.Context(), false)
			if err != nil {
				return err
			}
			if !dryRun {
				// The state is opened for changes only when it holds a stored plan: a
				// state that has none is not made by a collection.
				keys, err := st.Backend().List(cmd.Context(), engine.PlanDir)
				if err != nil {
					return err
				}
				if len(keys) > 0 {
					if st, ps, err = openPlanStore(cmd.Context(), true); err != nil {
						return err
					}
				}
			}
			opts.DryRun, opts.Owner, opts.Check = dryRun, collector(st), engine.PlanCheckFor(st)
			res, err := ps.GC(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if flagJSON {
				return emitJSON(res)
			}
			printGC(res)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "say what would be removed and remove nothing")
	cmd.Flags().StringVar(&olderThan, "older-than", "", "also remove the plans that were not applied and were made longer ago than this (90m, 36h, 7d)")
	return cmd
}

// processToken tells this process from every other one that has the same actor,
// host and process id, such as two containers that run pravo as process 1.
var processToken = func() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}()

// collector names this process in the claims that it makes on plans: who it acts
// as, where, and which process it is.
func collector(st *state.Store) string {
	who := st.Owner()
	if flagActor != "" {
		who = flagActor
	}
	host, _ := os.Hostname()
	return who + "@" + host + "#" + strconv.Itoa(os.Getpid()) + "." + processToken
}

func printGC(res *engine.GCResult) {
	verb, none := "Removed", "No stored plan needs to be removed."
	if res.DryRun {
		verb = "Would remove"
	}
	if len(res.Removed) == 0 && len(res.Marked) == 0 && len(res.Kept) == 0 {
		out.Println(none)
		return
	}
	list := func(items []engine.GCItem) {
		for _, it := range items {
			who := it.Author
			if who == "" {
				who = "-"
			}
			out.Printf("  %s  %-10s %s\n", it.ID, who, it.Reason)
		}
	}
	if len(res.Removed) > 0 {
		out.Printf("%s %s:\n", verb, plural(len(res.Removed), "stored plan", "stored plans"))
		list(res.Removed)
	}
	if len(res.Marked) > 0 {
		noun, pronoun := "plans", "them"
		if len(res.Marked) == 1 {
			noun, pronoun = "plan", "it"
		}
		if res.DryRun {
			out.Printf("%d %s can no longer be applied; a collection would mark %s, and one that runs %s after it would remove %s:\n", len(res.Marked), noun, pronoun, engine.DurationText(engine.DefaultStaleGrace), pronoun)
		} else {
			out.Printf("%d %s can no longer be applied; a collection that runs %s later removes %s:\n", len(res.Marked), noun, engine.DurationText(engine.DefaultStaleGrace), pronoun)
		}
		list(res.Marked)
	}
	if len(res.Kept) > 0 {
		be := "are"
		if len(res.Kept) == 1 {
			be = "is"
		}
		out.Printf("Kept %s that would be removed but %s in use:\n", plural(len(res.Kept), "plan", "plans"), be)
		list(res.Kept)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// storedPlan is what "plan --store" did, for the output.
type storedPlan struct {
	Meta     *engine.PlanMeta
	Overlaps []engine.Overlap
	Removed  []engine.GCItem
}

// storedPlanDoc is the document that "plan --store --json" prints: the plan
// document, and what was stored.
type storedPlanDoc struct {
	*engine.View
	Stored *storedInfo `json:"stored,omitempty"`
}

type storedInfo struct {
	ID           string              `json:"id"`
	Expires      time.Time           `json:"expires"`
	ApplyCommand string              `json:"apply_command"`
	Overlaps     []storedOverlapJSON `json:"overlaps,omitempty"`
	Removed      []engine.GCItem     `json:"removed,omitempty"`
}

type storedOverlapJSON struct {
	ID     string   `json:"id"`
	Author string   `json:"author"`
	Addrs  []string `json:"addrs"`
	// Changes are what the other plan changes and this one reads; ReadBy what this one changes and the other reads.
	Changes []string `json:"changes,omitempty"`
	ReadBy  []string `json:"read_by,omitempty"`
}

func (sp *storedPlan) info() *storedInfo {
	info := &storedInfo{ID: sp.Meta.ID, Expires: sp.Meta.Expires, ApplyCommand: sp.Meta.ApplyCommand(), Removed: sp.Removed}
	for _, o := range sp.Overlaps {
		info.Overlaps = append(info.Overlaps, storedOverlapJSON{ID: o.Plan.ID, Author: o.Plan.Author, Addrs: o.Addrs, Changes: o.Changes, ReadBy: o.ReadBy})
	}
	return info
}

// planWorth says whether a plan is worth storing: it has changes to apply. A plan
// that only records outputs or data sources is applied directly.
func planWorth(plan *engine.Plan) bool { return plan.HasWrites() }

// storePlan writes the plan into the state, looks for the stored plans that
// change the same resources, and removes a few that are no longer needed.
func storePlan(ctx context.Context, s *session, eng *engine.Engine, plan *engine.Plan, ttl time.Duration) (*storedPlan, error) {
	meta, err := eng.StorePlan(ctx, plan, flagTags, flagSysTags, ttl)
	if err != nil {
		return nil, err
	}
	sp := &storedPlan{Meta: meta}
	ps := engine.NewPlanStoreFor(s.state)
	check := engine.PlanCheckFor(s.state)
	if entries, err := ps.Entries(ctx, check); err == nil {
		sp.Overlaps = engine.OverlapsOf(entries, meta)
	}
	if res, err := ps.GC(ctx, engine.GCOptions{Owner: collector(s.state), Check: check, Limit: opportunisticGC, Skip: meta.ID}); err == nil {
		sp.Removed = res.Removed
	}
	return sp, nil
}

// warnOverlaps tells who else has a plan that changes the same resources. It
// only tells: the plans are not touched.
func warnOverlaps(overlaps []engine.Overlap) {
	names := func(addrs []string) string {
		if len(addrs) > 4 {
			return strings.Join(addrs[:4], ", ") + fmt.Sprintf(", and %d more", len(addrs)-4)
		}
		return strings.Join(addrs, ", ")
	}
	for _, o := range overlaps {
		m := o.Plan
		var parts []string
		if len(o.Addrs) > 0 {
			parts = append(parts, fmt.Sprintf("also changes %d of these resources: %s", len(o.Addrs), names(o.Addrs)))
		}
		if len(o.Changes) > 0 {
			parts = append(parts, fmt.Sprintf("changes %s, which this plan reads", names(o.Changes)))
		}
		if len(o.ReadBy) > 0 {
			parts = append(parts, fmt.Sprintf("reads %s, which this plan changes", names(o.ReadBy)))
		}
		out.Warning(fmt.Sprintf("%s by %s (%s) %s", m.ID, m.Author, since(m.Created), strings.Join(parts, "; ")))
	}
}

// printStored says where the plan is stored and how to apply it.
func printStored(sp *storedPlan) {
	out.Println()
	warnOverlaps(sp.Overlaps)
	out.Printf("Stored the plan as %s; unless it is applied it is removed after %s. To apply exactly these actions, run:\n  %s\n",
		out.C(ui.Bold, sp.Meta.ID), engine.SpanText(sp.Meta.Expires.Sub(sp.Meta.Created)), sp.Meta.ApplyCommand())
	if n := len(sp.Removed); n > 0 {
		was := "were"
		if n == 1 {
			was = "was"
		}
		out.Println(out.C(ui.Dim, fmt.Sprintf("Removed %s that %s no longer needed.", plural(n, "stored plan", "stored plans"), was)))
	}
}

// storedOverlapsOf looks for the stored plans that change the resources that the
// plan changes, for a plan that is not stored. It looks quietly: a state that
// cannot be listed has none.
func storedOverlapsOf(ctx context.Context, s *session, eng *engine.Engine, plan *engine.Plan) []engine.Overlap {
	entries, err := engine.NewPlanStoreFor(s.state).Entries(ctx, engine.PlanCheckFor(s.state))
	if err != nil || len(entries) == 0 {
		return nil
	}
	meta, err := eng.SummarizePlan(plan, flagTags, flagSysTags)
	if err != nil {
		return nil
	}
	return engine.OverlapsOf(entries, meta)
}

// planTagsMatch checks that the -t and -T of this command are the ones that the
// stored plan was made with, which select the state it is in and the
// specification it applies to.
func planTagsMatch(m *engine.PlanMeta, verb string) error {
	have := append([]string(nil), flagTags...)
	want := append([]string(nil), m.Tags...)
	sort.Strings(have)
	sort.Strings(want)
	if strings.Join(have, "\x00") == strings.Join(want, "\x00") && flagSysTags == m.SysTags {
		return nil
	}
	cmdline := strings.Replace(m.ApplyCommand(), "apply", verb, 1)
	if len(m.Tags) == 0 && !m.SysTags {
		return fmt.Errorf("%s was made without -t and -T: run %s without them", m.ID, cmdline)
	}
	return fmt.Errorf("%s was made with other -t and -T than these: run %s", m.ID, cmdline)
}

// readStored resolves a reference to a stored plan, checks that this command
// has the tags the plan was made with, and reads the plan with what was applied
// from it. The state must be open: s.
func readStored(ctx context.Context, s *session, ref, verb string) (*engine.PlanStore, *engine.PlanFile, *engine.PlanEntry, error) {
	ps := engine.NewPlanStoreFor(s.state)
	id, err := ps.Resolve(ctx, ref)
	if err != nil {
		return nil, nil, nil, err
	}
	entry, err := ps.Entry(ctx, id, engine.PlanCheckFor(s.state))
	if err != nil {
		return nil, nil, nil, err
	}
	if err := planTagsMatch(entry.Meta, verb); err != nil {
		return nil, nil, nil, err
	}
	pf, _, err := ps.ReadFile(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	return ps, pf, entry, nil
}

// showStored prints a stored plan.
func showStored(cmd *cobra.Command, ref string) error {
	ctx := cmd.Context()
	s, err := newSession(ctx, sessionOptions{install: true, schemas: true, loadState: true, readOnlyState: true})
	if err != nil {
		return err
	}
	defer s.close()
	_, pf, entry, err := readStored(ctx, s, ref, "plan show")
	if err != nil {
		return err
	}
	eng := s.engine(engine.Options{})
	plan, err := eng.LoadPlan(pf)
	var stale *engine.ErrStalePlan
	if errors.As(err, &stale) {
		plan, _ = eng.LoadPlanUnchecked(pf)
	} else if err != nil {
		return err
	}
	sigs, sigNote := storedPlanSignatures(ctx, s, entry.Meta.ID)
	if flagJSON {
		v := eng.NewView(plan, engine.ViewOptions{})
		v.Created, v.Actor, v.Reason, v.GitRevision = pf.Created, pf.Actor, pf.Reason, pf.GitRevision
		v.Reservation = pf.Reservation
		doc := map[string]any{"plan_id": entry.Meta.ID, "stored": storedPlanView(entry), "stale": stale, "plan": v}
		if sigs != nil || sigNote != "" {
			doc["signatures"] = planSigsJSON(sigs, sigNote)
		}
		return emitJSON(doc)
	}
	m := entry.Meta
	out.Printf("%s %s by %s, %s (%s)", out.C(ui.Bold, "Stored plan"), m.ID, m.Author, since(m.Created), statusText(entry.Status))
	out.Println()
	if m.Reason != "" {
		out.Printf("  reason:  %s\n", m.Reason)
	}
	out.Printf("  command: %s\n", m.CommandText())
	if m.GitRevision != "" {
		out.Printf("  git:     %s\n", m.GitRevision)
	}
	if sigNote != "" {
		out.Printf("  signed:  %s\n", out.C(ui.Dim, sigNote))
	} else if sigs != nil {
		out.Printf("  signed:  %s\n", planSigsText(sigs))
	}
	if entry.Status.Open() || entry.Status == engine.PlanStale {
		out.Printf("  %s\n", expiry(m))
	}
	if n := entry.AppliedCount(); n > 0 {
		out.Printf("  %d of %d changes applied\n", n, len(m.Changes))
	}
	if r := m.Reservation; r != nil && r.Until.After(time.Now()) {
		out.Printf("  reserved until %s: %s\n", r.Until.Local().Format(time.RFC3339), strings.Join(r.Addrs, ", "))
	}
	collecting := entry.Progress.BeingCollected(time.Now())
	switch {
	case collecting:
		// a collection holds the plan: an apply is refused until the claim lapses, so no command is offered
	case entry.Status == engine.PlanPartlyStale:
		out.Printf("  apply:   %s\n", entry.ApplyLiveCommand())
	case entry.Status.Open():
		out.Printf("  apply:   %s\n", m.ApplyCommand())
	}
	if collecting {
		out.Warning("a collection is removing this plan, so it cannot be applied; make it again with \"pravo plan --store\"")
	}
	switch {
	case entry.Status == engine.PlanPartlyStale:
		printConflicts(entry, stale)
	case stale != nil:
		out.Warning("this plan can no longer be applied: " + strings.Join(stale.Reasons, "; "))
	case len(entry.Reasons) > 0:
		out.Warning("this plan can no longer be applied: " + strings.Join(entry.Reasons, "; "))
	}
	out.RenderPlan(plan)
	return nil
}

// printConflicts says which changes of a partly-stale plan cannot be applied and
// why. The command above it applies the others. stale holds what the plan finds
// changed when it is loaded as a whole; what it adds to the conflicts, such as a
// configuration that changed, keeps the rest of the plan from being applied from
// here as well.
func printConflicts(entry *engine.PlanEntry, stale *engine.ErrStalePlan) {
	out.Warning(fmt.Sprintf("%d of %d unapplied changes conflict with the state and are left out of the command above:", len(entry.Conflicts), entry.Unapplied()))
	addrs := make([]string, 0, len(entry.Conflicts))
	for addr := range entry.Conflicts {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	known := map[string]bool{}
	for _, addr := range addrs {
		for _, r := range entry.Conflicts[addr] {
			known[r] = true
			out.Printf("  - %s\n", r)
		}
	}
	if stale == nil {
		return
	}
	var others []string
	for _, r := range stale.Reasons {
		if !known[r] {
			others = append(others, r)
		}
	}
	if len(others) > 0 {
		out.Warning("this plan can no longer be applied from here: " + strings.Join(others, "; "))
	}
}

// applyStored applies a plan that is stored in the state. The checks are those of
// a plan file; besides them the plan must not have expired.
func applyStored(cmd *cobra.Command, ref, refresh string, full bool, only []string) error {
	ctx := cmd.Context()
	streamEvents = true
	if cmd.Flags().Changed("refresh") || full {
		return fmt.Errorf("-refresh and -full do not apply to a stored plan")
	}
	s, err := newSession(ctx, sessionOptions{install: true, schemas: true, loadState: true})
	if err != nil {
		return err
	}
	defer s.close()
	ps := engine.NewPlanStoreFor(s.state)
	id, err := ps.Resolve(ctx, ref)
	if err != nil {
		return err
	}
	meta, err := ps.Meta(ctx, id)
	if err != nil {
		return err
	}
	if err := planTagsMatch(meta, "apply"); err != nil {
		return err
	}
	progress, err := ps.Progress(ctx, id)
	if err != nil {
		return err
	}
	if progress.DoneAt == nil && time.Now().After(meta.Expires) {
		return engine.WithCode(engine.ExitStale, fmt.Errorf("%s expired %s ago; plans are kept for a limited time: run \"pravo plan --store\" again", id, engine.DurationText(time.Since(meta.Expires))))
	}
	// From here no collection removes the plan, until the apply is over. What the
	// apply did is recorded in the plan when it returns. An apply that fails or is
	// interrupted records the changes it finished, as one from a plan file does; the
	// change that failed is not among them.
	owner := collector(s.state)
	if err := ps.BeginApply(ctx, id, owner); err != nil {
		if errors.Is(err, engine.ErrPlanRemoving) {
			return engine.WithCode(engine.ExitStale, err)
		}
		return err
	}
	background := context.WithoutCancel(ctx)
	defer ps.EndApply(background, id, owner)
	// An apply may run for longer than a claim lasts: the claim is renewed until it ends.
	defer ps.KeepApply(background, id, owner)()
	pf, _, err := ps.ReadFile(ctx, id)
	if err != nil {
		return err
	}
	// The note that the plan was made with is the reason of the apply, unless the
	// apply was given its own: the history of the resources says why.
	if flagReason == "" {
		flagReason = meta.Reason
	}
	// A plan that policies judged is the receipt of the apply, and no collection removes it.
	eng := s.engine(engine.Options{Refresh: engine.RefreshNone, Judged: func() { _ = ps.Keep(background, id) }})
	eng.SetPlan(id, pf.Digest)
	mark := func(applied []string, txn string, pending []string, finished bool) error {
		var err error
		if finished {
			err = ps.MarkApplied(background, id, applied, txn, pending)
		} else {
			err = ps.MarkPartlyApplied(background, id, applied, txn)
		}
		if err != nil {
			return fmt.Errorf("the changes were applied (txn %s) but the progress of %s could not be recorded: %w", txn, id, err)
		}
		return nil
	}
	// After a partial apply the plan is judged again: the records that the apply
	// changed may have made the rest of it stale, or only a part.
	again := meta.ApplyCommand()
	if len(only) > 0 {
		again += " --only " + engine.ShellWord(strings.Join(only, ","))
	}
	next := func() string {
		e, err := ps.Entry(background, id, engine.PlanCheckFor(s.state))
		switch {
		case err != nil:
			return fmt.Sprintf("run \"%s\" again to apply the rest", again)
		case e.Status == engine.PlanPartlyStale:
			return fmt.Sprintf("some of the rest is stale; apply the part that is still valid with \"%s\"", e.ApplyLiveCommand())
		case e.Status == engine.PlanStale:
			return "the rest is stale now and must be planned again with \"pravo plan --store\""
		}
		return fmt.Sprintf("run \"%s\" again to apply the rest", again)
	}
	return applyPlanFile(cmd, eng, pf, id, "plan", meta.ApplyCommand(), next, only, mark)
}
