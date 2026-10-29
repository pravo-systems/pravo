package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// MarkdownLimit is the most bytes a plan comment has. GitHub refuses a comment
// of more than 65536 characters; the rest is left for what a workflow adds.
const MarkdownLimit = 60000

// MarkdownOptions are what a plan comment says besides the plan.
type MarkdownOptions struct {
	// Title is the heading; "pravo plan" when empty.
	Title string
	// ID, Expires and Apply say where the plan is stored and how to apply it.
	ID      string
	Expires time.Time
	Apply   string
	// Refused is the reason that a policy refuses the plan.
	Refused string
	// Notes are further lines, such as who approved the plan.
	Notes []string
}

// Markdown renders the plan for a comment on a pull request. It names the
// resources, the actions and the attributes that change, and never a value:
// the comment is read by people who may not be allowed to read the state.
func (v *View) Markdown(o MarkdownOptions) string {
	var b strings.Builder
	title := o.Title
	if title == "" {
		title = "pravo plan"
	}
	var rows []ChangeView
	for _, c := range v.Changes {
		switch c.Action {
		case ActionCreate, ActionUpdate, ActionReplace, ActionDelete, ActionMove:
			rows = append(rows, c)
		case ActionRead:
			if c.Deferred {
				rows = append(rows, c)
			}
		}
	}
	s := v.Summary
	switch {
	case len(rows) == 0 && len(v.Policies) == 0:
		fmt.Fprintf(&b, "### %s: no changes\n", title)
	default:
		fmt.Fprintf(&b, "### %s: %d to add, %d to change, %d to destroy\n", title, s.Add, s.Change, s.Destroy)
	}
	if line := v.markdownByline(); line != "" {
		fmt.Fprintf(&b, "\n%s\n", line)
	}
	if o.Refused != "" {
		fmt.Fprintf(&b, "\n**Refused by policy.** %s\n", mdLine(o.Refused))
	}
	for _, n := range o.Notes {
		fmt.Fprintf(&b, "\n%s\n", n)
	}
	if len(rows) > 0 {
		b.WriteString("\n| Action | Resource | Type | Details |\n|---|---|---|---|\n")
		budget := MarkdownLimit - b.Len() - 1500 // the lines that follow the table
		for i, c := range rows {
			line := fmt.Sprintf("| %s | %s | %s | %s |\n", c.Action, code(c.Addr), code(c.Type), c.markdownDetails())
			if budget -= len(line); budget < 0 {
				fmt.Fprintf(&b, "\n%d more changes are not listed here.\n", len(rows)-i)
				break
			}
			b.WriteString(line)
		}
	}
	if s.BlastRadius > len(rows) {
		fmt.Fprintf(&b, "\nThe changes reach %s.\n", count(s.BlastRadius, "resource"))
	}
	if len(v.Policies) > 0 {
		b.WriteString("\nPolicies in this plan:\n\n")
		for _, p := range v.Policies {
			what := "changed"
			switch {
			case p.removal():
				what = "removed"
			case p.Base == 0:
				what = "new"
			}
			fmt.Fprintf(&b, "- %s (%s)\n", code(p.Name), what)
		}
	}
	if len(v.Hints) > 0 {
		b.WriteString("\n")
		for _, h := range v.Hints {
			fmt.Fprintf(&b, "- %s\n", mdLine(h))
		}
	}
	if o.ID != "" {
		fmt.Fprintf(&b, "\nStored as %s", code(o.ID))
		if !o.Expires.IsZero() {
			fmt.Fprintf(&b, ", removed on %s unless it is applied", o.Expires.UTC().Format("2006-01-02 15:04 MST"))
		}
		b.WriteString(".\n")
	}
	if o.Apply != "" {
		fmt.Fprintf(&b, "\nTo apply exactly these actions:\n\n```sh\n%s\n```\n", o.Apply)
	}
	return b.String()
}

func (v *View) markdownByline() string {
	var parts []string
	if v.Actor != "" {
		parts = append(parts, "by "+code(v.Actor))
	}
	if v.Reason != "" {
		parts = append(parts, mdLine(v.Reason))
	}
	if v.GitRevision != "" {
		parts = append(parts, "at "+code(v.GitRevision))
	}
	if v.Destroy {
		parts = append(parts, "destroys everything in the state")
	}
	return strings.Join(parts, " · ")
}

// markdownDetails says why a change is what it is, in names only.
func (c ChangeView) markdownDetails() string {
	var parts []string
	switch c.Action {
	case ActionUpdate, ActionReplace:
		if keys := changedKeys(c.Before, c.After); len(keys) > 0 {
			parts = append(parts, "changes "+codes(keys, 6))
		}
	}
	if len(c.RequiresReplace) > 0 {
		parts = append(parts, "replaced because of "+codes(c.RequiresReplace, 6))
	}
	if c.Drifted {
		parts = append(parts, "drifted")
	}
	if c.Deferred {
		parts = append(parts, "read at apply")
	}
	if len(c.Dependents) > 0 {
		parts = append(parts, "needed by "+count(len(c.Dependents), "resource"))
	}
	if len(c.Warnings) > 0 {
		parts = append(parts, "provider warns: "+mdLine(strings.Join(c.Warnings, "; ")))
	}
	return strings.Join(parts, "; ")
}

// changedKeys lists the top-level attributes whose value differs between the
// recorded and the planned object.
func changedKeys(before, after any) []string {
	b, _ := before.(map[string]any)
	a, _ := after.(map[string]any)
	seen := map[string]bool{}
	var keys []string
	for k, av := range a {
		seen[k] = true
		if m, ok := av.(map[string]any); ok && m["$unknown"] == true {
			continue // not known until the apply, so not a change the plan can name
		}
		if !sameData(b[k], av) {
			keys = append(keys, k)
		}
	}
	for k, bv := range b {
		if !seen[k] && bv != nil {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func sameData(a, b any) bool { return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b) }

// code writes a name as inline code that cannot break a table cell.
func code(s string) string {
	s = strings.NewReplacer("|", "\\|", "`", "'", "\n", " ").Replace(s)
	return "`" + s + "`"
}

func codes(names []string, most int) string {
	var parts []string
	for i, n := range names {
		if i == most {
			parts = append(parts, fmt.Sprintf("%d more", len(names)-most))
			break
		}
		parts = append(parts, code(n))
	}
	return strings.Join(parts, ", ")
}

func mdLine(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", "").Replace(strings.TrimSpace(s))
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// MDCode writes a name as inline code that cannot break a table cell. The
// commands that print Markdown for a pull request share it.
func MDCode(s string) string { return code(s) }

// MDLine writes text on one line, with the characters that would break a table
// cell escaped.
func MDLine(s string) string { return mdLine(s) }
