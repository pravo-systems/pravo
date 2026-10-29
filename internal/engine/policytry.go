package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cueerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"

	"github.com/pravo-systems/pravo/internal/schema"
)

// Trying a rule. A draft rule can be held against plans without being stored: the
// functions here judge the document of a plan by a text, in a CUE context of their own,
// and touch no state. They are what the policy studio ("pravo policy test", and the
// viewer's page) is made of, and they decide as the apply does: a rule holds when
// it unifies with the document and makes it concrete.

// RuleIssue is one reason a rule does not hold, or does not compile.
type RuleIssue struct {
	// Path is where CUE found the conflict, in the document or in the rule, such as
	// changes.2.action; empty for an error of the text itself.
	Path string `json:"path,omitempty"`
	// Message is CUE's wording.
	Message string `json:"message"`
	// Line and Column place the error in the text of the rule, when CUE knows where.
	Line   int `json:"line,omitempty"`
	Column int `json:"column,omitempty"`
	// Policy names the policy of a file of policies that the issue is about.
	Policy string `json:"policy,omitempty"`
	// Change is the address of the change that Path is inside, and Got the value the
	// document holds at Path (compact JSON, clipped): what the rule was held against.
	Change string `json:"change,omitempty"`
	Got    string `json:"got,omitempty"`
}

// RuleProblem is why a text cannot be a policy.
type RuleProblem struct {
	// Stage is "parse", "imports", "compile", "empty" (the rule refuses the document
	// of a plan that changes nothing) or "shape" (it does not fit the plan document).
	Stage string `json:"stage"`
	// Message is the whole error, as "pravo plan" words it when a spec declares the rule.
	Message string      `json:"message"`
	Issues  []RuleIssue `json:"issues,omitempty"`
}

// CheckRule reports why text cannot be the rule of the policy name, or nil. It is the
// check that "pravo plan" makes of a policy that a specification adds or changes: the
// rule parses, imports only the CUE standard library, accepts the document of an empty
// plan, and fits the shape of the plan document.
func CheckRule(ctx *cue.Context, name, text string) *RuleProblem {
	file, err := parser.ParseFile(name, text)
	if err != nil {
		return &RuleProblem{Stage: "parse", Message: fmt.Sprintf("policy %s: %s", name, cueerrors.Details(err, nil)), Issues: issuesOf(err, name)}
	}
	for imp := range file.ImportSpecs() {
		ip, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return &RuleProblem{Stage: "imports", Message: fmt.Sprintf("policy %s: %s", name, err)}
		}
		if i := strings.IndexByte(ip, ':'); i >= 0 {
			ip = ip[:i]
		}
		if strings.Contains(strings.SplitN(ip, "/", 2)[0], ".") {
			return &RuleProblem{Stage: "imports", Message: fmt.Sprintf("policy %s imports %q; a policy is standalone and may import only the CUE standard library", name, ip)}
		}
	}
	rule := ctx.CompileString(text, cue.Filename(name))
	if err := rule.Err(); err != nil {
		return &RuleProblem{Stage: "compile", Message: fmt.Sprintf("policy %s does not compile: %s", name, cueerrors.Details(err, nil)), Issues: issuesOf(err, name)}
	}
	return checkRuleValue(ctx, name, name, rule)
}

// checkRuleValue is the part of CheckRule that needs the rule as a value: it accepts the document of an
// empty plan and fits the shape of the plan document. label names the policy in the messages, and file is the
// file that the positions of its errors are in.
func checkRuleValue(ctx *cue.Context, label, file string, rule cue.Value) *RuleProblem {
	var nilEngine *Engine
	data, _, err := nilEngine.PolicyDocument(&PlanFile{Format: PlanFileFormat, Providers: map[string]string{}}, time.Time{})
	if err != nil {
		return &RuleProblem{Stage: "empty", Message: err.Error()}
	}
	doc, err := buildDoc(ctx, data)
	if err != nil {
		return &RuleProblem{Stage: "empty", Message: err.Error()}
	}
	if err := rule.Unify(doc).Validate(cue.Concrete(true)); err != nil {
		return &RuleProblem{Stage: "empty", Message: fmt.Sprintf("policy %s does not accept the document of an empty plan:\n%s", label, cueerrors.Details(err, nil)), Issues: issuesOf(err, file)}
	}
	return checkRuleShape(ctx, label, file, rule)
}

// checkRuleShape unifies a rule with the shape of the plan document and one change
// that holds no values, only the schema of a change. A rule is judged against a plan
// that has no changes when it is added, so a field it misspells inside a list
// template, a value outside the enum of an action and a read of an optional field
// would pass unseen until the first plan that has such a change. A condition on a
// value the sample does not hold is not decided, so a guard is not run: only the
// structure of the rule is.
func checkRuleShape(ctx *cue.Context, name, file string, rule cue.Value) *RuleProblem {
	fail := func(err error) *RuleProblem { return &RuleProblem{Stage: "shape", Message: err.Error()} }
	pkg := ctx.CompileString(schema.SettingsPackage)
	if err := pkg.Err(); err != nil {
		return fail(err)
	}
	plan := pkg.LookupPath(cue.MakePath(cue.Def("Plan")))
	// A rule that fixes the length of changes, such as changes: [], refuses a plan
	// with a change by design; a sample change would only trip it.
	shaped := plan
	if _, err := rule.LookupPath(cue.ParsePath("changes")).Len().Int64(); err != nil {
		one := ctx.CompileString("[#Change]", cue.Scope(pkg), cue.InferBuiltins(true))
		if err := one.Err(); err != nil {
			return fail(err)
		}
		shaped = plan.FillPath(cue.ParsePath("changes"), one)
	}
	if err := rule.Unify(shaped).Validate(); err != nil {
		return &RuleProblem{Stage: "shape", Message: fmt.Sprintf("policy %s does not fit the plan document:\n%s", name, cueerrors.Details(err, nil)), Issues: issuesOf(err, file)}
	}
	return nil
}

// Outcomes of trying a rule on a document.
const (
	RuleHolds   = "holds"
	RuleRefused = "refused"
	RuleInvalid = "invalid"
)

// RuleResult is what one rule says of one document.
type RuleResult struct {
	// Outcome is "holds", "refused" (the plan violates the rule) or "invalid" (the
	// text does not compile, so it says nothing of the plan).
	Outcome string      `json:"outcome"`
	Issues  []RuleIssue `json:"issues,omitempty"`
	// More counts the issues left out of Issues.
	More int `json:"more,omitempty"`
}

// maxRuleIssues bounds the issues kept for one rule and one plan.
const maxRuleIssues = 40

// Trier tries rules on documents. It owns a CUE context, so the rules and documents
// it builds are not shared with a specification, and it builds each document once, so
// that a rule that is edited again and again is tried on documents that are at hand.
// It can be used from several goroutines; they take turns.
type Trier struct {
	mu   sync.Mutex
	ctx  *cue.Context
	docs map[string]*triedDoc
}

type triedDoc struct {
	doc Doc
	val cue.Value
	err error
}

// NewTrier makes a Trier.
func NewTrier() *Trier { return &Trier{ctx: cuecontext.New(), docs: map[string]*triedDoc{}} }

// Check is CheckRule in the context of the Trier.
func (t *Trier) Check(name, text string) *RuleProblem {
	t.mu.Lock()
	defer t.mu.Unlock()
	return CheckRule(t.ctx, name, text)
}

// Doc is the document of a plan: the JSON that PolicyDocument gives, and the address
// of each of its changes in order.
type Doc struct {
	JSON  []byte
	Addrs []string
}

// DocOf makes the Doc of the plan that PolicyDocument returned with the JSON.
func DocOf(data []byte, plan *Plan) Doc {
	d := Doc{JSON: data}
	for _, ch := range plan.Changes {
		d.Addrs = append(d.Addrs, ch.Addr)
	}
	return d
}

// Add makes the document known under the key. A key that is known is left as it is.
func (t *Trier) Add(key string, d Doc) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.docs[key]; ok {
		return
	}
	val, err := buildDoc(t.ctx, d.JSON)
	t.docs[key] = &triedDoc{doc: d, val: val, err: err}
}

// Try holds the rule against the documents known under the keys, which Add put in. The
// result has one entry for each key, in order.
func (t *Trier) Try(name, text string, keys []string) []RuleResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]RuleResult, len(keys))
	rule := t.ctx.CompileString(text, cue.Filename(name))
	if err := rule.Err(); err != nil {
		issues := issuesOf(err, name)
		for i := range out {
			out[i] = RuleResult{Outcome: RuleInvalid}
			out[i].Issues = capIssues(issues, &out[i])
		}
		return out
	}
	for i, key := range keys {
		d := t.docs[key]
		switch {
		case d == nil:
			out[i] = RuleResult{Outcome: RuleInvalid, Issues: []RuleIssue{{Message: "no such document"}}}
		case d.err != nil:
			out[i] = RuleResult{Outcome: RuleInvalid, Issues: []RuleIssue{{Message: d.err.Error()}}}
		default:
			if err := rule.Unify(d.val).Validate(cue.Concrete(true)); err != nil {
				out[i] = RuleResult{Outcome: RuleRefused}
				out[i].Issues = capIssues(issuesOf(err, name), &out[i])
				annotate(out[i].Issues, d.doc)
				out[i].Issues = foldDisjunctions(out[i].Issues)
			} else {
				out[i] = RuleResult{Outcome: RuleHolds}
			}
		}
	}
	return out
}

func capIssues(issues []RuleIssue, into *RuleResult) []RuleIssue {
	if len(issues) > maxRuleIssues {
		into.More = len(issues) - maxRuleIssues
		issues = issues[:maxRuleIssues]
	}
	return issues
}

// issuesOf lists the errors of a CUE error, each once, in a stable order. name is the
// file of the rule: a position in another file is dropped.
func issuesOf(err error, name string) []RuleIssue {
	var out []RuleIssue
	seen := map[string]bool{}
	for _, e := range cueerrors.Errors(err) {
		format, args := e.Msg()
		is := RuleIssue{Path: strings.Join(e.Path(), "."), Message: fmt.Sprintf(format, args...)}
		for _, pos := range append([]token.Pos{e.Position()}, e.InputPositions()...) {
			if pos.IsValid() && pos.Filename() == name {
				is.Line, is.Column = pos.Line(), pos.Column()
				break
			}
		}
		key := is.Path + "\x00" + is.Message
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, is)
	}
	return out
}

// ChangesResources reports whether a plan file has a change that creates, updates,
// replaces or deletes a resource. Only such a plan is judged by the policies.
func (pf *PlanFile) ChangesResources() bool { return pf.changesResources() }

// annotate fills in the change and the value of each issue whose path leads into the document.
func annotate(issues []RuleIssue, d Doc) {
	var root any
	for i := range issues {
		is := &issues[i]
		if is.Path == "" {
			continue
		}
		steps := strings.Split(is.Path, ".")
		if len(steps) >= 2 && steps[0] == "changes" {
			if n, err := strconv.Atoi(steps[1]); err == nil && n >= 0 && n < len(d.Addrs) {
				is.Change = d.Addrs[n]
			}
		}
		if root == nil {
			dec := json.NewDecoder(bytes.NewReader(d.JSON))
			dec.UseNumber()
			if err := dec.Decode(&root); err != nil {
				return
			}
		}
		cur := root
		for _, st := range steps {
			switch v := cur.(type) {
			case map[string]any:
				cur = v[st]
			case []any:
				n, err := strconv.Atoi(st)
				if err != nil || n < 0 || n >= len(v) {
					cur = nil
				} else {
					cur = v[n]
				}
			default:
				cur = nil
			}
			if cur == nil {
				break
			}
		}
		switch cur.(type) {
		case nil, map[string]any, []any:
			// a whole change or list is not a value to show: the change is named already
			continue
		}
		b, err := json.Marshal(cur)
		if err != nil {
			continue
		}
		if len(b) > 200 {
			b = append(b[:200:200], "…"...)
		}
		is.Got = string(b)
	}
}

// foldDisjunctions replaces the errors of a disjunction that matched nothing, which CUE
// reports as the sum of its branches, with one issue that lists the values the rule allows.
// The branches are told by the value the document holds, which Got has.
func foldDisjunctions(issues []RuleIssue) []RuleIssue {
	allowed := map[string][]string{}
	folded := map[string]bool{}
	for _, is := range issues {
		if strings.Contains(is.Message, "errors in empty disjunction") && is.Got != "" {
			folded[is.Path] = true
		}
	}
	if len(folded) == 0 {
		return issues
	}
	var out []RuleIssue
	for _, is := range issues {
		if folded[is.Path] && strings.HasPrefix(is.Message, "conflicting values ") {
			rest := strings.TrimPrefix(is.Message, "conflicting values ")
			switch {
			case strings.HasSuffix(rest, " and "+is.Got):
				allowed[is.Path] = append(allowed[is.Path], strings.TrimSuffix(rest, " and "+is.Got))
			case strings.HasPrefix(rest, is.Got+" and "):
				allowed[is.Path] = append(allowed[is.Path], strings.TrimPrefix(rest, is.Got+" and "))
			default:
				out = append(out, is)
			}
			continue
		}
		out = append(out, is)
	}
	for i := range out {
		if vals := allowed[out[i].Path]; len(vals) > 0 && strings.Contains(out[i].Message, "errors in empty disjunction") {
			out[i].Message = "the rule allows " + strings.Join(vals, " | ") + ", and the plan has " + out[i].Got
		}
	}
	return out
}
