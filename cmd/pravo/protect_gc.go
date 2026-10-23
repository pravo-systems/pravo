package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
)

func protectGCCmd() *cobra.Command {
	var (
		olderThan string
		dryRun    bool
	)
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Delete the records that no pointer names and the reader groups that nothing refers to",
		Long: `A state of format 2 or 3 never overwrites a record, so every revision of every
record stays in the bucket, and so does the record of a resource that was
destroyed. gc deletes the records that no pointer names, once they are older than
24 hours (--older-than says another time, and not less than 65 seconds) and two
collections at least 15 minutes apart have found them so. The first collection
marks the records and deletes nothing; a collection at least 15 minutes later
deletes the ones that are still unnamed. A writer that has put a record and not
yet replaced the pointer is protected by that age, which needs the clocks of the
machines that write to agree to within it, and by the marks, for a writer that
copies records with the times they already have (rewrap --all, protect init,
state push and pull). The age is the newest of the times the record shows in the
clear; a record that shows none is kept. A record that a pointer names is never
deleted. gc needs no key for this.

In a sealed state gc also drops the reader groups, other than the current one,
that nothing is sealed in: no record, no output and no stored plan. Run
"pravo protect rewrap --all" first, so that everything is under the current group.
A removed reader's data key then opens nothing that is left. This needs an
identity that is a reader, the readers file, and a state at rest: no command is
running and no lock is held. When one is missing, gc still collects the records.
It exits with 4, 5 or 6 for a command that is running or a readers file that is
missing, invalid or different from the state, and with 0 for an identity that is
not a reader.

A bucket that keeps old versions of its objects keeps the records that gc deleted
and the meta.json that listed the groups it dropped.

--dry-run says what would be marked and deleted and changes nothing, the marks
included.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var grace time.Duration
			if olderThan != "" {
				d, err := parseSpan("older-than", olderThan)
				if err != nil {
					return err
				}
				if d == 0 {
					return fmt.Errorf("--older-than %s: want a length of time above zero", olderThan)
				}
				grace = d
			}
			p, err := newProtectRun(cmd)
			if err != nil {
				return err
			}
			return p.gc(grace, dryRun)
		},
	}
	f := cmd.Flags()
	f.StringVar(&olderThan, "older-than", "", "delete the records that no pointer names once they are older than this (default 24h; 36h, 7d); it should exceed the clock skew of the machines that write")
	f.BoolVar(&dryRun, "dry-run", false, "say what would be marked and deleted and change nothing")
	return cmd
}

func (p *protectRun) gc(grace time.Duration, dryRun bool) error {
	b, err := p.backend(false)
	if err != nil {
		return err
	}
	if _, err := b.Get(p.ctx, "meta.json"); errors.Is(err, state.ErrNotFound) {
		return gcFormatOne(0, "The state is empty: there is nothing to collect.")
	} else if err != nil {
		return err
	}
	st, id, idErr, err := p.open(b, !dryRun, true)
	if errors.Is(err, state.ErrUntrusted) {
		return reportUntrusted(err)
	}
	if err != nil {
		return err
	}
	prot := st.Protection()
	switch {
	case prot.Format < state.FormatImmutable:
		return gcFormatOne(prot.Format, fmt.Sprintf("The state is format %d: it keeps one object for each resource and has nothing to collect.", prot.Format))
	case prot.State == "migrating":
		return state.ErrSealing
	case prot.State == "unsealing":
		return state.ErrUnsealing
	}
	// A state that is not sealed in a repository that lists readers may have been
	// downgraded in the bucket: a collection that deletes is a writer, as any other.
	if rf := loadReadersFile(p.root.Dir, flagAccessDir); expectsSealed(prot, rf) {
		problem := downgradeProblem(rf)
		if !dryRun {
			return engine.WithCode(engine.ExitRefused, errors.New(problem))
		}
		fmt.Fprintln(os.Stderr, "Warning: "+problem)
	}

	// The groups are looked at only by a reader, with the readers file in hand, and
	// only while nothing runs. The records are collected whatever the key.
	groups, skipped := false, ""
	var groupsErr error // why the groups were not looked at, when that is a failure
	if prot.State == "sealed" {
		switch {
		case st.Locked() || prot.You.State != "reader":
			skipped = "you are not a reader of this state"
			switch {
			case id != nil:
				skipped = "identity " + id.Name + " is not a reader"
			case idErr != nil && !errors.Is(idErr, errNoIdentity):
				skipped = strings.TrimSuffix(idErr.Error(), ".")
			case idErr != nil:
				skipped = "no identity found"
			}
		default:
			if groupsErr = checkReadersFile(p.root, st, !dryRun); groupsErr == nil {
				groupsErr = atRest(st)
			}
			if groupsErr != nil {
				skipped = groupsErr.Error()
			} else {
				groups = true
			}
		}
	}

	opts := state.GCOptions{OlderThan: grace, DryRun: dryRun, Groups: groups}
	if groups {
		opts.By = id.Name
		opts.Plans = engine.NewPlanStoreFor(st).SealedGroups
	}
	res, err := st.GC(p.ctx, opts)
	if err != nil {
		return err
	}
	if res.Raised {
		fmt.Fprintf(os.Stderr, "Warning: --older-than %s is below the least, %s, which keeps the record of a writer that opened the state before a rewrap. Using %s.\n", graceText(grace), graceText(res.Grace), graceText(res.Grace))
	}
	if flagJSON {
		if err := emitJSON(gcJSON(prot.Format, res, groups, skipped)); err != nil {
			return err
		}
	} else {
		if groupsErr != nil {
			skipped = "" // the error says it
		}
		printGCReport(res, prot.State == "sealed", groups, skipped)
	}
	if groupsErr != nil {
		return fmt.Errorf("the groups were not looked at: %w", groupsErr)
	}
	return nil
}

// gcFormatOne reports a state with no records to collect.
func gcFormatOne(format int, line string) error {
	if flagJSON {
		return emitJSON(map[string]any{"format": format, "records": map[string]any{"listed": 0, "deleted": 0}, "note": strings.TrimSuffix(line, ".")})
	}
	out.Println(line)
	return nil
}

// gcJSON is the JSON form of protect gc.
func gcJSON(format int, res state.GCResult, examined bool, skipped string) map[string]any {
	foreign := res.Foreign
	if foreign == nil {
		foreign = []string{}
	}
	doc := map[string]any{
		"format": format, "dry_run": res.DryRun, "older_than_seconds": int64(res.Grace / time.Second),
		"settle_seconds": int64(res.Settle / time.Second),
		"records": map[string]any{
			"listed": res.Listed, "named": res.Named, "unnamed": res.Unnamed, "young": res.Young, "unknown_age": res.Unknown,
			"marked": res.Marked, "settling": res.Settling,
			"deleted": res.Deleted, "bytes": res.Bytes, "foreign": foreign,
		},
	}
	if format < state.FormatSealed {
		return doc
	}
	dropped, kept := []string{}, []map[string]any{}
	for _, g := range res.Groups {
		if g.Dropped {
			dropped = append(dropped, g.ID)
			continue
		}
		kept = append(kept, map[string]any{"id": g.ID, "records": g.Records, "unnamed_records": g.Spare, "outputs": g.Outputs, "plans": g.Plans, "summaries": g.Summaries})
	}
	gr := map[string]any{"examined": examined && res.GroupsRefused == "", "dropped": dropped, "kept": kept}
	switch {
	case skipped != "":
		gr["skipped"] = skipped
	case res.GroupsRefused != "":
		gr["skipped"] = printable(res.GroupsRefused)
	}
	doc["groups"] = gr
	return doc
}

func printGCReport(res state.GCResult, sealed, groups bool, skipped string) {
	verb, mark, none := "Deleted", "Marked", "No record is garbage: a pointer names every record."
	if res.DryRun {
		verb, mark = "Would delete", "Would mark"
	}
	age, settle := graceText(res.Grace), graceText(res.Settle)
	switch {
	case res.Deleted > 0:
		out.Printf("%s %s older than %s (%s)\n", verb, counted(res.Deleted, "record", "records"), age, bytesText(res.Bytes))
	case res.Unnamed > 0 && res.Marked == 0 && res.Settling == 0:
		out.Printf("No record is older than %s: %s that no pointer names %s kept.\n", age, counted(res.Unnamed, "record", "records"), isAre(res.Unnamed))
	case res.Unnamed == 0 && len(res.Foreign) == 0:
		out.Println(none)
	}
	if res.Marked > 0 {
		out.Printf("%s %s that no pointer names. A collection at least %s later deletes the ones that are still unnamed.\n", mark, counted(res.Marked, "record", "records"), settle)
	}
	if res.Settling > 0 {
		out.Printf("Kept %s that no pointer names: marked less than %s ago.\n", counted(res.Settling, "record", "records"), settle)
	}
	if kept := res.Young + res.Unknown; (res.Deleted > 0 || res.Marked > 0 || res.Settling > 0) && kept > 0 {
		var why []string
		if res.Young > 0 {
			why = append(why, fmt.Sprintf("%d younger than %s", res.Young, age))
		}
		if res.Unknown > 0 {
			why = append(why, fmt.Sprintf("%d whose age is not recorded", res.Unknown))
		}
		out.Printf("Kept %s that no pointer names: %s\n", counted(kept, "record", "records"), strings.Join(why, ", "))
	}
	if n := len(res.Foreign); n > 0 {
		list := res.Foreign
		if len(list) > listedNames {
			list = list[:listedNames]
		}
		out.Printf("Left alone %s that do not match their names: %s. pravo did not write them.\n",
			counted(n, "object in rec/", "objects in rec/"), strings.Join(list, " "))
	}
	if !sealed {
		return
	}
	switch {
	case skipped != "":
		out.Println("Groups were not looked at: " + skipped + ".")
		return
	case res.GroupsRefused != "":
		out.Println("Groups were kept: " + printable(res.GroupsRefused) + ".")
		return
	case !groups:
		return
	case len(res.Groups) == 0:
		out.Println("No group needs to be dropped: the state has only its current group.")
		return
	}
	drop, rewrap, later := "Dropped", false, false
	if res.DryRun {
		drop = "Would drop"
	}
	for _, g := range res.Groups {
		switch {
		case g.Dropped:
			out.Printf("%s group %s: nothing refers to it.\n", drop, g.ID)
		default:
			out.Printf("Kept group %s: %s.\n", g.ID, groupRefs(g, age))
			if g.Records > 0 || g.Outputs || g.Plans > 0 || g.Summaries > 0 {
				rewrap = true
			}
			if g.Spare > 0 {
				later = true
			}
		}
	}
	if rewrap {
		out.Println("pravo protect rewrap --all seals records, outputs, stored plans and the texts of errors again under the current group.")
	}
	if later {
		out.Printf("A record that no pointer names is kept until it is older than %s. Run pravo protect gc again then.\n", age)
	}
}

// groupRefs says what is sealed in a group that was kept.
func groupRefs(g state.GCGroup, age string) string {
	var parts []string
	if g.Records > 0 {
		parts = append(parts, counted(g.Records, "record", "records"))
	}
	if g.Outputs {
		parts = append(parts, "the outputs")
	}
	if g.Plans > 0 {
		parts = append(parts, counted(g.Plans, "object of a stored plan", "objects of stored plans"))
	}
	if g.Summaries > 0 {
		parts = append(parts, counted(g.Summaries, "text of an error in the history", "texts of errors in the history"))
	}
	if g.Spare > 0 {
		parts = append(parts, counted(g.Spare, "record that no pointer names and that is kept", "records that no pointer names and that are kept"))
	}
	s := strings.Join(parts, ", ")
	if len(parts) > 1 {
		i := strings.LastIndex(s, ", ")
		s = s[:i] + " and" + s[i+1:]
	}
	if len(parts) == 0 {
		return "something is sealed in it"
	}
	return s + " are sealed in it"
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// graceText writes a length of time the way --older-than takes it: 36h, 7d, 90m.
func graceText(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0 && d >= 72*time.Hour:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return fmt.Sprintf("%ds", int(d.Round(time.Second).Seconds()))
}

// bytesText writes a size in decimal units: 512 B, 38 KB, 1.2 MB.
func bytesText(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d B", n)
	}
	v, unit := float64(n), ""
	for _, u := range []string{"KB", "MB", "GB", "TB"} {
		v, unit = v/1000, u
		if v < 1000 {
			break
		}
	}
	if v < 10 {
		return fmt.Sprintf("%.1f %s", v, unit)
	}
	return fmt.Sprintf("%.0f %s", v, unit)
}
