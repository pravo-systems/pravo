package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/ui"
)

func policyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Work with the policies that judge plans",
		Args:  noArgs,
	}
	cmd.AddCommand(policyTestCmd())
	return cmd
}

// trialRule is a rule held against plans.
type trialRule struct {
	Name   string
	Origin string
	Text   string
	// File is set when Text is a file of policies (it declares pravo.#Policy), not a rule.
	File bool
}

// trialPlan is a plan that rules are held against, as the document a policy sees.
type trialPlan struct {
	Ref    string
	Status string
	Doc    engine.Doc
	// Judged is false for a plan that changes no resource, which no policy judges.
	Judged bool
	// Err is why the plan cannot be judged.
	Err string
}

// trialOutcome is what one rule says of one plan.
type trialOutcome struct {
	Plan    string             `json:"plan"`
	Status  string             `json:"status,omitempty"`
	Outcome string             `json:"outcome"`
	Note    string             `json:"note,omitempty"`
	Issues  []engine.RuleIssue `json:"issues,omitempty"`
	More    int                `json:"more,omitempty"`
}

// trialReport is what one rule says of all the plans.
type trialReport struct {
	Policy  string              `json:"policy"`
	Origin  string              `json:"origin"`
	Problem *engine.RuleProblem `json:"problem,omitempty"`
	Plans   []trialOutcome      `json:"plans"`
}

func policyTestCmd() *cobra.Command {
	var plans []string
	var all, inForce bool
	cmd := &cobra.Command{
		Use:   "test [policy|rule.cue]...",
		Short: "Hold policy rules against stored plans and plan files, and say which they would refuse",
		Long: `Judges plans by rules without applying anything and without writing anything:
the rules are tried on the document that a policy sees, the one of "pravo plan --json"
without what only the state can give. Use it to see what a rule would do before it is
stored.

A rule is named by one argument: the name of a policy the specification declares, or a
CUE file. A file that declares policies, as a specification does (pravo.#Policy with a
rule), has each of them tried and checked as CUE against the pravo package, such as a
draft that is not in the specification yet; any other file holds one rule, the struct
that "rule:" would hold. With no argument every policy of the specification is
tried. --in-force tries the versions the state holds instead, for the question "what
do the policies of the state say about this plan".

The plans are the stored plans that can still be applied, or --all of them, and any
--plan: the id of a stored plan or the path of a plan file. A plan that changes no
resource is not judged by any policy.

The command exits 5 when a rule would refuse a plan, 6 when a rule cannot be used, and
0 otherwise.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPolicyTest(cmd.Context(), args, plans, all, inForce)
		},
	}
	cmd.Flags().StringArrayVar(&plans, "plan", nil, "a stored plan id or a plan file to try the rules on (repeatable); without it the stored plans that can be applied")
	cmd.Flags().BoolVar(&all, "all", false, "try every stored plan, including applied, stale and expired ones")
	cmd.Flags().BoolVar(&inForce, "in-force", false, "try the versions in force in the state, not the specification's text")
	addTagFlags(cmd)
	return cmd
}

func runPolicyTest(ctx context.Context, args, planRefs []string, all, inForce bool) error {
	s, err := newSession(ctx, sessionOptions{install: true, schemas: true, loadState: true, readOnlyState: true})
	if err != nil {
		return err
	}
	defer s.close()
	rules, err := trialRules(ctx, s, args, inForce)
	if err != nil {
		return engine.WithCode(engine.ExitInvalid, err)
	}
	eng := s.engine(engine.Options{})
	trials, err := trialPlans(ctx, s, eng, planRefs, all)
	if err != nil {
		return err
	}
	trier := engine.NewTrier()
	var keys []string
	var judged []int
	for i, p := range trials {
		if p.Err == "" && p.Judged {
			trier.Add(p.Ref, p.Doc)
			keys = append(keys, p.Ref)
			judged = append(judged, i)
		}
	}
	reports := make([]trialReport, 0, len(rules))
	refused, invalid := 0, 0
	for _, r := range rules {
		rep := trialReport{Policy: r.Name, Origin: r.Origin, Plans: []trialOutcome{}}
		var results []engine.RuleResult
		if r.File {
			res := trier.TryFile(r.Text, keys)
			if rep.Problem = res.Problem; rep.Problem != nil {
				invalid++
			}
			if len(res.Policies) > 0 {
				var names []string
				for _, p := range res.Policies {
					names = append(names, p.Name)
				}
				rep.Origin = r.Origin + ": " + strings.Join(names, ", ")
			}
			// a file that compiles is tried even when it could not be stored as it is
			if res.Problem == nil || res.Problem.Stage != "parse" && res.Problem.Stage != "imports" && res.Problem.Stage != "policies" && res.Problem.Stage != "compile" {
				results = res.Plans
			}
		} else if rep.Problem = trier.Check(r.Name, r.Text); rep.Problem != nil {
			invalid++
		} else {
			results = trier.Try(r.Name, r.Text, keys)
		}
		at := map[int]engine.RuleResult{}
		for k, i := range judged {
			if results != nil {
				at[i] = results[k]
			}
		}
		for i, p := range trials {
			o := trialOutcome{Plan: p.Ref, Status: p.Status}
			switch {
			case p.Err != "":
				o.Outcome, o.Note = "unreadable", p.Err
			case !p.Judged:
				o.Outcome, o.Note = "not-judged", "it changes no resource, so no policy judges it"
			case rep.Problem != nil:
				o.Outcome = "unchecked"
			default:
				res := at[i]
				o.Outcome, o.Issues, o.More = res.Outcome, res.Issues, res.More
				if res.Outcome != engine.RuleHolds {
					refused++
				}
			}
			rep.Plans = append(rep.Plans, o)
		}
		reports = append(reports, rep)
	}
	if flagJSON {
		if err := emitJSON(map[string]any{"rules": reports, "refused": refused, "invalid": invalid}); err != nil {
			return err
		}
	} else {
		printTrials(reports, len(trials))
	}
	switch {
	case invalid > 0:
		return engine.WithCode(engine.ExitInvalid, fmt.Errorf("%s cannot be used", plural(invalid, "rule", "rules")))
	case refused > 0:
		return engine.WithCode(engine.ExitRefused, fmt.Errorf("%s would be refused", plural(refused, "plan", "plans")))
	}
	return nil
}

// trialRules finds the rules to try: the files and policy names given, else every
// policy of the specification, or of the state with inForce.
func trialRules(ctx context.Context, s *session, args []string, inForce bool) ([]trialRule, error) {
	var declared, held map[string]trialRule
	if inForce {
		set, err := s.state.PolicySet(ctx, loadReadersFile(s.root.Dir, flagAccessDir).Readers)
		if err != nil {
			return nil, err
		}
		held = map[string]trialRule{}
		for _, v := range set.InForce() {
			name := state.PolicyDisplay(v.Name)
			held[name] = trialRule{Name: name, Origin: fmt.Sprintf("the state, version %d", v.Seq), Text: v.Text}
		}
	} else {
		declared = map[string]trialRule{}
		for _, p := range s.root.Policies {
			declared[p.Name] = trialRule{Name: p.Name, Origin: "the specification", Text: p.Rule}
		}
	}
	known := declared
	if inForce {
		known = held
	}
	var rules []trialRule
	for _, a := range args {
		if r, ok := known[a]; ok {
			rules = append(rules, r)
			continue
		}
		data, err := os.ReadFile(a)
		if err != nil {
			var names []string
			for n := range known {
				names = append(names, n)
			}
			sort.Strings(names)
			where := "the specification declares"
			if inForce {
				where = "the state holds"
			}
			if len(names) == 0 {
				return nil, fmt.Errorf("%q is not a rule file, and no policy of that name exists: %s no policy", a, where)
			}
			return nil, fmt.Errorf("%q is not a rule file, and %s no policy of that name (it has %s)", a, where, strings.Join(names, ", "))
		}
		name := strings.TrimSuffix(filepath.Base(a), filepath.Ext(a))
		rules = append(rules, trialRule{Name: name, Origin: a, Text: string(data), File: strings.Contains(string(data), "pravo.#Policy")})
	}
	if len(args) == 0 {
		for _, r := range known {
			rules = append(rules, r)
		}
		sort.Slice(rules, func(i, j int) bool { return rules[i].Name < rules[j].Name })
		if len(rules) == 0 {
			where := "The specification declares"
			if inForce {
				where = "The state holds"
			}
			return nil, fmt.Errorf("%s no policy: name a rule file to try one", where)
		}
	}
	return rules, nil
}

// trialPlans reads the plans to try, and the document each is judged by.
func trialPlans(ctx context.Context, s *session, eng *engine.Engine, refs []string, all bool) ([]trialPlan, error) {
	var out []trialPlan
	add := func(ref, status string, pf *engine.PlanFile, err error) {
		p := trialPlan{Ref: ref, Status: status}
		if err == nil {
			var data []byte
			var plan *engine.Plan
			if data, plan, err = eng.PolicyDocument(pf); err == nil {
				p.Doc, p.Judged = engine.DocOf(data, plan), pf.ChangesResources()
			}
		}
		if err != nil {
			p.Err = err.Error()
		}
		out = append(out, p)
	}
	ps := engine.NewPlanStoreFor(s.state)
	storedRead := func(id string) (*engine.PlanFile, error) {
		pf, _, err := ps.ReadFile(ctx, id)
		if errors.Is(err, state.ErrNotReader) {
			return nil, errors.New("sealed: this identity cannot open the plan")
		}
		return pf, err
	}
	if len(refs) == 0 || all {
		entries, _, err := ps.EntriesAndSkipped(ctx, engine.PlanCheckFor(s.state))
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, r := range refs {
			seen[r] = true
		}
		for _, e := range entries {
			if seen[e.Meta.ID] || !all && !e.Status.Open() {
				continue
			}
			pf, err := storedRead(e.Meta.ID)
			add(e.Meta.ID, string(e.Status), pf, err)
		}
	}
	for _, ref := range refs {
		if isStoredRef(ref) {
			id, err := ps.Resolve(ctx, ref)
			if err != nil {
				return nil, err
			}
			status := ""
			if e, err := ps.Entry(ctx, id, engine.PlanCheckFor(s.state)); err == nil {
				status = string(e.Status)
			}
			pf, err := storedRead(id)
			add(id, status, pf, err)
			continue
		}
		pf, err := engine.ReadPlanFile(ref)
		add(ref, "file", pf, err)
	}
	return out, nil
}

func printTrials(reports []trialReport, plans int) {
	if plans == 0 {
		out.Println("No stored plan can be applied. Name one with --plan, or use --all.")
	}
	for i, r := range reports {
		if i > 0 {
			out.Println()
		}
		out.Printf("%s %s %s\n", out.C(ui.Bold, "Policy"), out.C(ui.Bold, r.Policy), out.C(ui.Dim, "("+r.Origin+")"))
		if r.Problem != nil {
			out.Printf("  %s %s\n", out.C(ui.Red, "cannot be used:"), indent(r.Problem.Message, "  "))
			continue
		}
		holds, judged := 0, 0
		for _, o := range r.Plans {
			mark, text := out.C(ui.Green, "✓"), "holds"
			switch o.Outcome {
			case engine.RuleRefused:
				judged++
				mark, text = out.C(ui.Red, "✗"), out.C(ui.Red, "would be refused")
			case engine.RuleInvalid:
				judged++
				mark, text = out.C(ui.Red, "✗"), out.C(ui.Red, "cannot be judged")
			case "not-judged", "unreadable":
				mark, text = out.C(ui.Dim, "–"), out.C(ui.Dim, o.Note)
			default:
				holds, judged = holds+1, judged+1
			}
			status := ""
			if o.Status != "" {
				status = "  " + out.C(ui.Dim, o.Status)
			}
			out.Printf("  %s %s%s  %s\n", mark, o.Plan, status, text)
			for _, is := range o.Issues {
				line := is.Message
				if is.Path != "" {
					line = is.Path + ": " + line
				}
				if is.Policy != "" {
					line = is.Policy + ": " + line
				}
				out.Printf("      %s\n", line)
				if is.Change != "" || is.Got != "" {
					var bits []string
					if is.Change != "" {
						bits = append(bits, "change "+is.Change)
					}
					if is.Got != "" {
						bits = append(bits, "the plan has "+is.Got)
					}
					out.Printf("      %s\n", out.C(ui.Dim, strings.Join(bits, "; ")))
				}
			}
			if o.More > 0 {
				out.Printf("      %s\n", out.C(ui.Dim, fmt.Sprintf("and %d more", o.More)))
			}
		}
		out.Printf("  %s\n", out.C(ui.Dim, fmt.Sprintf("%d of %s hold.", holds, plural(judged, "judged plan", "judged plans"))))
	}
}

func indent(text, prefix string) string {
	return strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n"+prefix)
}
