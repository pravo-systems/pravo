package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	cueerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/parser"

	"github.com/pravo-systems/pravo/internal/schema"
)

// Trying a file of policies. The studio takes the text a specification would hold: an import of the
// pravo package and one or more policies, each a field that is a pravo.#Policy with its rule. The file is
// compiled with the part of the pravo package that describes a policy, so that a misspelt field, a wrong type
// and an unknown name are errors of the file at the line that holds them, and each rule is tried on the
// documents as an apply would try it.

// DraftFile is the file name that the positions of a draft file are in.
const DraftFile = "policy.cue"

const pravoImport = "pravo.dev/pravo"

var pravoImportLine = regexp.MustCompile(`^\s*(?:import\s+)?(?:pravo\s+)?"pravo\.dev/pravo"\s*(?://.*)?$`)

// DraftPolicy is a policy found in a file.
type DraftPolicy struct {
	// Name is the path the policy sits at, such as cost.max_instances.
	Name string `json:"name"`
	// Path is the same as labels.
	Path []string `json:"-"`
	// Line is the line of the file that declares it.
	Line int `json:"line"`
}

// FileResult is what a file of policies says of documents.
type FileResult struct {
	// Problem is why the file cannot be stored as it is, or nil. A file that compiles is tried on the documents even so.
	Problem  *RuleProblem
	Policies []DraftPolicy
	// Plans has one result for each key. A document is refused when any policy refuses it, and the issues name the policy.
	Plans []RuleResult
}

// TryFile holds every policy of the file against the documents known under the keys.
func (t *Trier) TryFile(text string, keys []string) FileResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	res := FileResult{Plans: make([]RuleResult, len(keys))}
	userLines := strings.Count(text, "\n") + 1
	fail := func(p *RuleProblem) FileResult {
		res.Problem = p
		for i := range res.Plans {
			res.Plans[i] = RuleResult{Outcome: RuleInvalid}
		}
		return res
	}
	parsed, err := parser.ParseFile(DraftFile, text)
	if err != nil {
		return fail(&RuleProblem{Stage: "parse", Message: cueerrors.Details(err, nil), Issues: ownLines(issuesOf(err, DraftFile), userLines)})
	}
	for imp := range parsed.ImportSpecs() {
		ip, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return fail(&RuleProblem{Stage: "imports", Message: err.Error()})
		}
		if i := strings.IndexByte(ip, ':'); i >= 0 {
			ip = ip[:i]
		}
		switch {
		case ip == pravoImport:
			if imp.Name != nil && imp.Name.Name != "pravo" {
				return fail(&RuleProblem{Stage: "imports", Message: "import the pravo package as pravo", Issues: []RuleIssue{{Message: "import the pravo package as pravo", Line: imp.Pos().Line()}}})
			}
		case strings.Contains(strings.SplitN(ip, "/", 2)[0], "."):
			msg := fmt.Sprintf("imports %q; a policy may import only %s and the CUE standard library", ip, pravoImport)
			return fail(&RuleProblem{Stage: "imports", Message: msg, Issues: []RuleIssue{{Message: msg, Line: imp.Pos().Line()}}})
		}
	}
	collectPolicies(parsed.Decls, nil, &res.Policies)
	if len(res.Policies) == 0 {
		msg := "the file declares no policy: write NAME: pravo.#Policy & {rule: …}"
		return fail(&RuleProblem{Stage: "policies", Message: msg, Issues: []RuleIssue{{Message: msg}}})
	}

	// The pravo import is the package that the prelude below stands in for, so its line is left empty, which keeps the numbering.
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if pravoImportLine.MatchString(l) {
			lines[i] = ""
		}
	}
	src := strings.Join(lines, "\n") + "\n\npravo: {\n" + schema.PlanDocumentSource() + "}\n"
	val := t.ctx.CompileString(src, cueFilename(DraftFile))
	if err := val.Err(); err != nil {
		return fail(&RuleProblem{Stage: "compile", Message: cueerrors.Details(err, nil), Issues: ownLines(issuesOf(err, DraftFile), userLines)})
	}

	var problems []*RuleProblem
	add := func(name string, p *RuleProblem) {
		for i := range p.Issues {
			p.Issues[i].Policy = name
		}
		problems = append(problems, p)
	}
	for _, dp := range res.Policies {
		sels := make([]cue.Selector, len(dp.Path))
		for i, l := range dp.Path {
			sels[i] = cue.Str(l)
		}
		v := val.LookupPath(cue.MakePath(sels...))
		if err := v.Validate(); err != nil {
			add(dp.Name, &RuleProblem{Stage: "compile", Message: fmt.Sprintf("policy %s: %s", dp.Name, cueerrors.Details(err, nil)), Issues: ownLines(issuesOf(err, DraftFile), userLines)})
			markInvalid(res.Plans)
			continue
		}
		rule := v.LookupPath(cue.ParsePath("rule"))
		if !rule.Exists() {
			add(dp.Name, &RuleProblem{Stage: "compile", Message: fmt.Sprintf("policy %s has no rule", dp.Name), Issues: []RuleIssue{{Message: "the policy has no rule", Line: dp.Line}}})
			markInvalid(res.Plans)
			continue
		}
		if err := checkRuleValue(t.ctx, dp.Name, DraftFile, rule); err != nil {
			err.Issues = ownLines(inRule(err.Issues, dp.Name), userLines)
			add(dp.Name, err)
		}
		for i, key := range keys {
			d := t.docs[key]
			var r RuleResult
			switch {
			case d == nil:
				r = RuleResult{Outcome: RuleInvalid, Issues: []RuleIssue{{Message: "no such document"}}}
			case d.err != nil:
				r = RuleResult{Outcome: RuleInvalid, Issues: []RuleIssue{{Message: d.err.Error()}}}
			default:
				if err := rule.Unify(d.val).Validate(cue.Concrete(true)); err != nil {
					r = RuleResult{Outcome: RuleRefused}
					r.Issues = capIssues(inRule(issuesOf(err, DraftFile), dp.Name), &r)
					annotate(r.Issues, d.doc)
					r.Issues = foldDisjunctions(r.Issues)
				} else {
					r = RuleResult{Outcome: RuleHolds}
				}
			}
			for k := range r.Issues {
				r.Issues[k].Policy = dp.Name
			}
			r.Issues = ownLines(r.Issues, userLines)
			res.Plans[i] = mergeResults(res.Plans[i], r)
		}
	}
	for i := range res.Plans {
		res.Plans[i].Issues = capIssues(res.Plans[i].Issues, &res.Plans[i])
	}
	if len(problems) > 0 {
		p := &RuleProblem{Stage: problems[0].Stage}
		var msgs []string
		for _, q := range problems {
			msgs = append(msgs, q.Message)
			p.Issues = append(p.Issues, q.Issues...)
		}
		p.Message = strings.Join(msgs, "\n\n")
		res.Problem = p
	}
	return res
}

// mergeResults combines what two policies say of one document: refused beats invalid, and invalid beats holds.
func mergeResults(a, b RuleResult) RuleResult {
	rank := map[string]int{"": 0, RuleHolds: 1, RuleInvalid: 2, RuleRefused: 3}
	out := a
	if rank[b.Outcome] > rank[a.Outcome] {
		out.Outcome = b.Outcome
	}
	out.Issues = append(append([]RuleIssue(nil), a.Issues...), b.Issues...)
	out.More += b.More
	return out
}

func markInvalid(plans []RuleResult) {
	for i := range plans {
		plans[i] = mergeResults(plans[i], RuleResult{Outcome: RuleInvalid})
	}
}

// inRule makes the path of an issue start in the rule of the policy, as it does in the document of a plan.
func inRule(issues []RuleIssue, name string) []RuleIssue {
	for i := range issues {
		switch p := issues[i].Path; {
		case p == name+".rule":
			issues[i].Path = ""
		case strings.HasPrefix(p, name+".rule."):
			issues[i].Path = strings.TrimPrefix(p, name+".rule.")
		}
	}
	return issues
}

// ownLines drops the line of an issue that is not in the text that the person wrote: the part of the pravo package
// that the file is compiled with comes after it.
func ownLines(issues []RuleIssue, userLines int) []RuleIssue {
	for i := range issues {
		if issues[i].Line > userLines {
			issues[i].Line, issues[i].Column = 0, 0
		}
	}
	return issues
}

// collectPolicies finds the fields that are a pravo.#Policy, at the top of the file or inside structs that
// only hold them, as in cost: max_instances: pravo.#Policy & {...}.
func collectPolicies(decls []ast.Decl, prefix []string, out *[]DraftPolicy) {
	for _, d := range decls {
		f, ok := d.(*ast.Field)
		if !ok {
			continue
		}
		name, _, err := ast.LabelName(f.Label)
		if err != nil || strings.HasPrefix(name, "#") || strings.HasPrefix(name, "_") {
			continue
		}
		path := append(append([]string(nil), prefix...), name)
		if isPolicyExpr(f.Value) {
			*out = append(*out, DraftPolicy{Name: strings.Join(path, "."), Path: path, Line: f.Pos().Line()})
		} else if st, ok := f.Value.(*ast.StructLit); ok {
			collectPolicies(st.Elts, path, out)
		}
	}
}

// isPolicyExpr reports whether an expression is pravo.#Policy, alone or unified with something.
func isPolicyExpr(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		sel, ok2 := x.Sel.(*ast.Ident)
		return ok && ok2 && id.Name == "pravo" && sel.Name == "#Policy"
	case *ast.BinaryExpr:
		return x.Op.String() == "&" && (isPolicyExpr(x.X) || isPolicyExpr(x.Y))
	case *ast.ParenExpr:
		return isPolicyExpr(x.X)
	}
	return false
}

func cueFilename(name string) cue.BuildOption { return cue.Filename(name) }
