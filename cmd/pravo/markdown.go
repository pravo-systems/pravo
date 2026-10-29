package main

import (
	"fmt"
	"strings"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/pravo"
)

// The commands that print Markdown for an issue or a comment: names, addresses
// and counts, never an attribute value.

// driftMarkdown is the report of "pravo drift --markdown". A proposal that holds
// a plan adds the plan, as "pravo plan --markdown" prints it.
func driftMarkdown(rep *pravo.DriftReport, proposal *driftProposal) string {
	var b strings.Builder
	switch {
	case len(rep.Drifted) == 0:
		fmt.Fprintf(&b, "### pravo drift: no drift\n\n%d resources refreshed; %s pending from the specification.\n", rep.Refreshed, plural(rep.Pending, "change", "changes"))
	default:
		fmt.Fprintf(&b, "### pravo drift: %d of %d resources changed outside pravo\n", len(rep.Drifted), rep.Refreshed)
		b.WriteString("\n| Resource | What happened |\n|---|---|\n")
		for _, d := range rep.Drifted {
			what := "the real state differs from the state"
			if d.Gone {
				what = "the object does not exist"
			}
			fmt.Fprintf(&b, "| %s | %s |\n", engine.MDCode(d.Addr), what)
		}
		fmt.Fprintf(&b, "\n%s pending from the specification.\n", plural(rep.Pending, "change", "changes"))
	}
	if proposal != nil && proposal.Plan != nil {
		opts := engine.MarkdownOptions{Title: "pravo plan that puts them back", Refused: proposal.Refused}
		if proposal.Stored != nil {
			opts.ID, opts.Expires, opts.Apply = proposal.Stored.ID, proposal.Stored.Expires, proposal.Stored.ApplyCommand
		}
		b.WriteString("\n" + proposal.Plan.Markdown(opts))
	}
	return b.String()
}

// dueMarkdown is the report of "pravo drift --due --markdown".
func dueMarkdown(rep *engine.DueReport) string {
	var b strings.Builder
	if len(rep.Drifted) == 0 {
		fmt.Fprintf(&b, "### pravo drift: no drift\n\n%s\n", dueSummary(rep))
		return b.String()
	}
	fmt.Fprintf(&b, "### pravo drift: %d of %d %s changed outside pravo\n", len(rep.Drifted), len(rep.Read), recordNoun(len(rep.Read)))
	b.WriteString("\n| Resource | What happened |\n|---|---|\n")
	for _, d := range rep.Drifted {
		what := "the real state differs from the state"
		if d.Gone {
			what = "the object does not exist"
		}
		fmt.Fprintf(&b, "| %s | %s |\n", engine.MDCode(d.Addr), what)
	}
	fmt.Fprintf(&b, "\n%s\n", dueSummary(rep))
	return b.String()
}

// markdownShown is the most unmanaged objects a discovery lists in Markdown.
const markdownShown = 50

// discoveryMarkdown is "pravo discover show --markdown": for each discovery its
// counts and the unmanaged objects, with the command that adopts each.
func discoveryMarkdown(views []*engine.DiscoveryView) string {
	if len(views) == 0 {
		return "### pravo discover: no discoveries\n\nThe specification has no discoveries.\n"
	}
	var b strings.Builder
	for i, v := range views {
		if i > 0 {
			b.WriteString("\n")
		}
		n := v.Counts[engine.Unmanaged]
		if n == 0 {
			fmt.Fprintf(&b, "### pravo discover %s: nothing unmanaged\n", v.Name)
		} else {
			fmt.Fprintf(&b, "### pravo discover %s: %d unmanaged\n", v.Name, n)
		}
		fmt.Fprintf(&b, "\n%s via %s", engine.MDCode(v.Type), engine.MDCode(providerLabel(v)))
		if len(v.Scope) > 0 || v.Limit > 0 {
			fmt.Fprintf(&b, ", scope %s", engine.MDCode(scopeText(v.Scope, v.Limit)))
		}
		b.WriteString("\n")
		switch v.Freshness {
		case engine.FreshNever:
			fmt.Fprintf(&b, "\nNot run yet. Run `pravo discover %s`.\n", engine.ShellWord(v.Name))
			continue
		case engine.FreshBroken:
			fmt.Fprintf(&b, "\nCannot run: %s\n", engine.MDLine(v.ScopeErr))
		case engine.FreshStale:
			fmt.Fprintf(&b, "\nThe scope changed since the last run, which answers the old one. Run `pravo discover %s`.\n", engine.ShellWord(v.Name))
		case engine.FreshUndeclared:
			b.WriteString("\nThe specification has no discovery of this name; this is what its last run found.\n")
		}
		if v.Run == nil {
			continue
		}
		fmt.Fprintf(&b, "\nLast run %s", v.Run.Finished.UTC().Format("2006-01-02 15:04 MST"))
		if v.Run.Actor != "" {
			fmt.Fprintf(&b, " by %s", engine.MDCode(v.Run.Actor))
		}
		fmt.Fprintf(&b, ": %s. %s\n", plural(len(v.Run.Items), "object", "objects"), plainCounts(v))
		if v.Run.Status == state.RunTruncated {
			fmt.Fprintf(&b, "\nThe list was cut off at the limit of %d, so there may be more.\n", v.Limit)
		}
		if v.Unreadable > 0 {
			fmt.Fprintf(&b, "\n%s of type %s could not be read, so what they manage may be listed as unmanaged.\n", plural(v.Unreadable, "record", "records"), engine.MDCode(v.Type))
		}
		items := v.Unmanaged()
		if len(items) == 0 {
			continue
		}
		b.WriteString("\n| Object | Name | Adopt with |\n|---|---|---|\n")
		for j, it := range items {
			if j == markdownShown {
				fmt.Fprintf(&b, "\n%d more are not listed here.\n", len(items)-markdownShown)
				break
			}
			lead, name := it.ID, it.Name
			if it.ID == it.Key && name != "" {
				lead, name = name, ""
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", engine.MDCode(lead), engine.MDLine(name), engine.MDCode("pravo discover adopt "+engine.ShellWord(v.Name)+" "+engine.ShellWord(it.ID)))
		}
	}
	return b.String()
}

// plainCounts is the counts of a discovery without colour.
func plainCounts(v *engine.DiscoveryView) string {
	var parts []string
	for _, st := range []engine.DiscoveryStatus{engine.Unmanaged, engine.Adopting, engine.Acknowledged, engine.Managed} {
		parts = append(parts, fmt.Sprintf("%d %s", v.Counts[st], st))
	}
	return strings.Join(parts, " · ")
}
