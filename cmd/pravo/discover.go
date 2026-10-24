package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

// maxShown is how many objects of one kind a listing prints before it says how
// many more there are.
const maxShown = 20

func discoverCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "discover [<name>...]",
		Short: "Find the objects that exist and no resource of the specification manages",
		Long: `Runs the discoveries of the specification. A discovery is a list resource
type of a provider with the scope to list it in:

    vms: $aws.#Discovery.aws_instance & {region: "eu-west-1"}

It asks the provider which objects exist in that scope and keeps what it
lists in the state. The objects that a record of the state already is are
counted and left alone. The rest are unmanaged, and are shown for adoption
unless the specification acknowledges them: an entry in the $ack list of the
discovery says that an object is known and is meant to stay as it is.

Without names, discover runs the discoveries that have not run yet and the
ones whose scope changed since they ran, since what they found answers another
question. With --all it runs every discovery again, and with names it runs
those. A discovery lists and manages nothing: no plan or apply reads it.

Use "pravo discover show" to read what the last runs found, and "pravo
discover adopt" to write the resource that adopts an object.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				return fmt.Errorf("--all runs every discovery; name some or give --all, not both")
			}
			s, err := newSession(cmd.Context(), sessionOptions{install: true, schemas: true, loadState: true})
			if err != nil {
				return err
			}
			defer s.close()
			if len(s.root.Discoveries) == 0 {
				if flagJSON {
					return emitJSON(map[string]any{"discoveries": []any{}})
				}
				out.Println("The specification has no discoveries.")
				return nil
			}
			chosen, err := chooseDiscoveries(s, args, all)
			if err != nil {
				return engine.WithCode(engine.ExitInvalid, err)
			}
			eng := s.engine(engine.Options{Progress: func(format string, a ...any) { out.Printf(format+"\n", a...) }})
			failed := 0
			for _, d := range chosen {
				run, err := eng.Discover(cmd.Context(), d)
				if err == nil {
					err = s.state.PutDiscovery(run)
				}
				if err != nil {
					failed++
					out.Printf("%s %s\n", out.C(ui.Red+ui.Bold, "Error:"), err)
				}
			}
			rep, err := discoveryReport(s)
			if err != nil {
				return err
			}
			if flagJSON {
				if err := emitJSON(discoveryJSON(rep, names(chosen), false)); err != nil {
					return err
				}
			} else {
				if len(chosen) == 0 {
					out.Printf("Every discovery is current. Run pravo discover --all to list them again.\n")
				}
				for _, d := range chosen {
					if v := rep.View(d.Name); v != nil && v.Run != nil && v.Freshness == engine.FreshCurrent {
						printView(v, false)
					}
				}
			}
			if failed > 0 {
				return fmt.Errorf("%s failed", plural(failed, "discovery", "discoveries"))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "run every discovery, not only the ones that have not run or whose scope changed")
	addTagFlags(cmd)
	cmd.AddCommand(discoverShowCmd(), discoverAdoptCmd())
	return cmd
}

func names(ds []*config.Discovery) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Name
	}
	return out
}

// chooseDiscoveries picks the discoveries to run: the ones named, every one, or
// the ones that have not run and the ones whose scope changed.
func chooseDiscoveries(s *session, args []string, all bool) ([]*config.Discovery, error) {
	var chosen []*config.Discovery
	if len(args) > 0 {
		for _, name := range args {
			d := s.root.DiscoveryByName(name)
			if d == nil {
				return nil, unknownDiscovery(s.root, name)
			}
			chosen = append(chosen, d)
		}
		return chosen, nil
	}
	runs, _, err := s.state.Discoveries()
	if err != nil {
		return nil, err
	}
	for _, d := range s.root.Discoveries {
		run := runs[d.Name]
		if all || d.ScopeErr != nil || run == nil || run.Digest != d.Digest {
			chosen = append(chosen, d)
		}
	}
	return chosen, nil
}

func unknownDiscovery(root *config.Root, name string) error {
	known := root.DiscoveryNames()
	if len(known) == 0 {
		return fmt.Errorf("there is no discovery %q: the specification has none", name)
	}
	return fmt.Errorf("there is no discovery %q in the specification; it has %s", name, strings.Join(known, ", "))
}

// discoveryReport reads the runs and the acknowledgements of the state and sets
// them against its records.
func discoveryReport(s *session) (*engine.DiscoveryReport, error) {
	runs, problems, err := s.state.Discoveries()
	if err != nil {
		return nil, err
	}
	for _, p := range problems {
		out.Warning("cannot read " + p.Error())
	}
	return engine.BuildDiscoveryReport(engine.DiscoveryInputs{Root: s.root, Runs: runs, Records: s.state.List()}), nil
}

// ---------- pravo discover show ----------

func discoverShowCmd() *cobra.Command {
	var exitCode, managed bool
	cmd := &cobra.Command{
		Use:   "show [<name>...]",
		Short: "Show what the last runs of the discoveries found",
		Long: `Shows each discovery with what its last run found, set against the state: the
unmanaged objects first, then the ones a resource is about to adopt and the ones
that were acknowledged. The objects that are managed are counted and left out
unless --managed. It runs nothing and writes nothing.

A discovery whose scope changed since it ran says so: what it shows is the answer
to the old scope, and "pravo discover" runs it again.

With --exit-code the command exits 2 when any discovery shown has an unmanaged
object, so that a schedule can tell someone.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newSession(cmd.Context(), sessionOptions{loadState: true, readOnlyState: true})
			if err != nil {
				return err
			}
			defer s.close()
			rep, err := discoveryReport(s)
			if err != nil {
				return err
			}
			views := rep.Views
			if len(args) > 0 {
				views = nil
				for _, name := range args {
					v := rep.View(name)
					if v == nil {
						return engine.WithCode(engine.ExitInvalid, unknownDiscovery(s.root, name))
					}
					views = append(views, v)
				}
			}
			unmanaged := 0
			for _, v := range views {
				unmanaged += v.Counts[engine.Unmanaged]
			}
			if flagJSON {
				names := make([]string, len(views))
				for i, v := range views {
					names[i] = v.Name
				}
				if err := emitJSON(discoveryJSON(rep, names, managed)); err != nil {
					return err
				}
			} else {
				if len(views) == 0 {
					out.Println("The specification has no discoveries.")
				}
				for i, v := range views {
					if i > 0 {
						out.Println()
					}
					printView(v, managed)
				}
			}
			if exitCode && unmanaged > 0 {
				return engine.WithCode(engine.ExitChanges, fmt.Errorf("%s unmanaged", plural(unmanaged, "object is", "objects are")))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&exitCode, "exit-code", false, "exit 2 when an unmanaged object is shown")
	cmd.Flags().BoolVar(&managed, "managed", false, "list the managed objects too")
	addTagFlags(cmd)
	return cmd
}

func printView(v *engine.DiscoveryView, showManaged bool) {
	head := v.Name
	if !v.Declared {
		head += " " + out.C(ui.Dim, "(no longer in the specification)")
	}
	out.Printf("%s  %s\n", out.C(ui.Bold, head), out.C(ui.Dim, v.Type+" via "+providerLabel(v)))
	if len(v.Scope) > 0 || v.Limit > 0 {
		out.Printf("  scope: %s\n", scopeText(v.Scope, v.Limit))
	}
	switch v.Freshness {
	case engine.FreshNever:
		out.Printf("  %s\n", out.C(ui.Yellow, "not run yet. Run: pravo discover "+engine.ShellWord(v.Name)))
		return
	case engine.FreshBroken:
		out.Printf("  %s\n", out.C(ui.Red, "cannot run: "+v.ScopeErr))
	case engine.FreshStale:
		out.Printf("  %s\n", out.C(ui.Yellow, "the scope changed since the last run, which answers the old one. Run: pravo discover "+engine.ShellWord(v.Name)))
	case engine.FreshUndeclared:
		out.Printf("  %s\n", out.C(ui.Dim, "the specification has no discovery of this name; this is what its last run found"))
	}
	if v.Run == nil {
		return
	}
	t := v.Run.Finished
	by := ""
	if v.Run.Actor != "" {
		by = " by " + v.Run.Actor
	}
	out.Printf("  last run %s%s: %s\n", ago(&t), engine.PlainText(by), plural(len(v.Run.Items), "object", "objects"))
	if v.Run.Status == state.RunTruncated {
		out.Printf("  %s\n", out.C(ui.Yellow, fmt.Sprintf("the list was cut off at the limit of %d; there may be more", v.Limit)))
	}
	for _, w := range v.Run.Warnings {
		out.Printf("  %s %s\n", out.C(ui.Yellow, "warning:"), engine.PlainText(w))
	}
	if v.Unreadable > 0 {
		out.Printf("  %s\n", out.C(ui.Yellow, fmt.Sprintf("%s of type %s could not be read, so what they manage may be listed as unmanaged", plural(v.Unreadable, "record", "records"), v.Type)))
	}
	out.Printf("  %s\n", countsText(v))
	for _, group := range []struct {
		status engine.DiscoveryStatus
		title  string
		color  string
	}{
		{engine.Unmanaged, "UNMANAGED", ui.Yellow + ui.Bold},
		{engine.Adopting, "ADOPTING", ui.Cyan},
		{engine.Acknowledged, "ACKNOWLEDGED", ui.Dim},
		{engine.Managed, "MANAGED", ui.Dim},
	} {
		if group.status == engine.Managed && !showManaged {
			continue
		}
		items := v.WithStatus(group.status)
		if len(items) == 0 {
			continue
		}
		out.Printf("\n  %s\n", out.C(group.color, group.title))
		limit := maxShown
		if group.status == engine.Managed || showManaged {
			limit = len(items)
		}
		for i, it := range items {
			if i == limit {
				out.Printf("    ... and %d more: pravo discover show %s --json\n", len(items)-limit, engine.ShellWord(v.Name))
				break
			}
			out.Printf("    %s\n", itemLine(v, it))
		}
	}
}

func providerLabel(v *engine.DiscoveryView) string {
	if v.ProviderKey != "" {
		return v.ProviderKey
	}
	return v.Provider
}

func scopeText(scope map[string]any, limit int64) string {
	keys := make([]string, 0, len(scope))
	for k := range scope {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)+1)
	for _, k := range keys {
		parts = append(parts, engine.PlainText(k+"="+state.IdentityValue(scope[k])))
	}
	if limit > 0 {
		parts = append(parts, fmt.Sprintf("limit=%d", limit))
	}
	return strings.Join(parts, " ")
}

func countsText(v *engine.DiscoveryView) string {
	var parts []string
	if n := v.Counts[engine.Unmanaged]; n > 0 {
		parts = append(parts, out.C(ui.Yellow+ui.Bold, fmt.Sprintf("%d unmanaged", n)))
	} else {
		parts = append(parts, "0 unmanaged")
	}
	for _, st := range []engine.DiscoveryStatus{engine.Adopting, engine.Acknowledged, engine.Managed} {
		parts = append(parts, fmt.Sprintf("%d %s", v.Counts[st], st))
	}
	return strings.Join(parts, " · ")
}

func itemLine(v *engine.DiscoveryView, it engine.DiscoveredItem) string {
	// An object that only its whole identity names is called by its display name, and
	// the identity is in the command that adopts it.
	lead, name := it.ID, it.Name
	if it.ID == it.Key && name != "" {
		lead, name = name, ""
	}
	line := fmt.Sprintf("%-28s", engine.PlainText(lead))
	if name != "" {
		line += "  " + engine.PlainText(name)
	}
	switch it.Status {
	case engine.Unmanaged:
		line += "  " + out.C(ui.Dim, "pravo discover adopt "+engine.ShellWord(v.Name)+" "+engine.ShellWord(it.ID))
	case engine.Adopting:
		line += "  " + out.C(ui.Dim, "by "+it.By)
	case engine.Acknowledged:
		line += "  " + out.C(ui.Dim, it.By)
		if it.Reason != "" {
			line += " " + out.C(ui.Dim, "“"+engine.PlainText(it.Reason)+"”")
		}
	case engine.Managed:
		line += "  " + out.C(ui.Dim, "is "+it.By)
	}
	return line
}

// discoveryJSON is the document of "discover" and "discover show --json".
func discoveryJSON(rep *engine.DiscoveryReport, only []string, managed bool) map[string]any {
	keep := map[string]bool{}
	for _, n := range only {
		keep[n] = true
	}
	docs := []any{}
	for _, v := range rep.Views {
		if !keep[v.Name] {
			continue
		}
		items := []any{}
		for _, it := range v.Items {
			if it.Status == engine.Managed && !managed {
				continue
			}
			item := map[string]any{"id": it.ID, "key": it.Key, "status": it.Status, "identity": it.Identity}
			if it.Name != "" {
				item["name"] = it.Name
			}
			if it.By != "" {
				item["by"] = it.By
			}
			if it.Reason != "" {
				item["reason"] = it.Reason
			}
			items = append(items, item)
		}
		doc := map[string]any{
			"name": v.Name, "type": v.Type, "provider": v.Provider, "declared": v.Declared, "freshness": v.Freshness,
			"scope": v.Scope, "counts": countsJSON(v), "items": items, "unreadable_records": v.Unreadable,
		}
		if v.Limit > 0 {
			doc["limit"] = v.Limit
		}
		if v.ScopeErr != "" {
			doc["scope_error"] = v.ScopeErr
		}
		if len(v.Acks) > 0 {
			acks := []any{}
			for _, a := range v.Acks {
				covers := 0
				if v.Run != nil {
					for _, l := range v.Run.Items {
						if a.Covers(l.ID, l.Key, l.Name) {
							covers++
						}
					}
				}
				acks = append(acks, map[string]any{"ref": a.Ref(), "ids": a.IDs, "names": a.Names, "reason": a.Reason, "covers": covers})
			}
			doc["acks"] = acks
		}
		if r := v.Run; r != nil {
			doc["last_run"] = map[string]any{"started": r.Started, "finished": r.Finished, "actor": r.Actor, "status": r.Status, "warnings": r.Warnings, "scope_digest": r.Digest}
		}
		docs = append(docs, doc)
	}
	return map[string]any{"discoveries": docs}
}

func countsJSON(v *engine.DiscoveryView) map[string]int {
	m := map[string]int{}
	for _, st := range []engine.DiscoveryStatus{engine.Unmanaged, engine.Adopting, engine.Acknowledged, engine.Managed} {
		m[string(st)] = v.Counts[st]
	}
	return m
}

// ---------- pravo discover adopt ----------

func discoverAdoptCmd() *cobra.Command {
	var read bool
	cmd := &cobra.Command{
		Use:   "adopt <name> <id>",
		Short: "Write the resource that adopts an object a discovery found",
		Long: `Prints the CUE of a resource that brings one discovered object under
management: a resource of the object's type, built through the provider the
discovery lists through, that names the object in $import by its identity: the
attributes the provider listed it by. Add it to the specification, and "pravo
plan" reads the object and says what differs.

<id> is the id the discovery shows, or the whole identity. With --read the
provider is asked for the object, and the attributes it has are written into the
resource, so that a plan of it has little to say. Sensitive attributes are named
in a comment and not written. Nothing is recorded in the state.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, id := args[0], args[1]
			s, err := newSession(cmd.Context(), sessionOptions{install: read, schemas: read, loadState: true, readOnlyState: true})
			if err != nil {
				return err
			}
			defer s.close()
			rep, err := discoveryReport(s)
			if err != nil {
				return err
			}
			v := rep.View(name)
			if v == nil {
				return engine.WithCode(engine.ExitInvalid, unknownDiscovery(s.root, name))
			}
			if v.Run == nil {
				return engine.WithCode(engine.ExitInvalid, fmt.Errorf("discovery %s has not run: pravo discover %s", name, engine.ShellWord(name)))
			}
			var item *engine.DiscoveredItem
			for i := range v.Items {
				it := &v.Items[i]
				if it.ID == id || it.Key == id {
					item = it
					break
				}
			}
			if item == nil {
				return engine.WithCode(engine.ExitInvalid, fmt.Errorf("the last run of %s did not find %q", name, id))
			}
			if item.Status == engine.Managed {
				return engine.WithCode(engine.ExitInvalid, fmt.Errorf("%s is already managed, as %s", id, item.By))
			}
			l := item.Listed
			body := ""
			if read {
				eng := s.engine(engine.Options{Progress: func(string, ...any) {}})
				if body, err = eng.ReadAdoption(cmd.Context(), v.ProviderKey, v.Type, plugin.ImportTarget{Identity: l.Identity}); err != nil {
					return err
				}
			}
			snippet := engine.FormatCUE(engine.AdoptSnippet(s.root, v, l, body))
			if flagJSON {
				return emitJSON(map[string]any{"discovery": name, "id": l.ID, "identity": l.Identity, "status": item.Status, "snippet": snippet})
			}
			fmt.Print(snippet)
			if item.Status == engine.Adopting {
				out.Printf("// Note: %s already adopts this object.\n", item.By)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&read, "read", false, "ask the provider for the object and write its attributes into the resource")
	addTagFlags(cmd)
	return cmd
}
