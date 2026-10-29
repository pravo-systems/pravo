package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cueerrors "cuelang.org/go/cue/errors"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/schema"
)

func policyEngine() *Engine {
	return &Engine{Root: &config.Root{Ctx: cuecontext.New()}, Opts: Options{Progress: func(string, ...any) {}}}
}

// The document a policy is judged against comes from the plan file alone: nothing of the
// state, the clock or the invocation is in it.
func TestTheJudgedDocumentComesFromThePlanFileAlone(t *testing.T) {
	e := policyEngine()
	e.Opts.Actor, e.Opts.Reason, e.traceID = "someone-else", "another reason", "trace-1"
	made := time.Date(2026, 10, 1, 8, 30, 0, 0, time.UTC)
	doc, err := e.judgedDoc(&PlanFile{Format: PlanFileFormat, Created: made, Actor: "ci", Reason: "nightly", GitRevision: "abc123", Providers: map[string]string{"local": "2.9.1"}}, made.Add(36*time.Hour+500*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	str := func(path string) string {
		s, err := doc.LookupPath(cue.ParsePath(path)).String()
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return s
	}
	if str("actor") != "ci" || str("reason") != "nightly" || str("git_revision") != "abc123" || str("providers.local") != "2.9.1" {
		t.Errorf("the file's attribution and providers: %v", doc)
	}
	if !strings.HasPrefix(str("created"), "2026-10-01T08:30:00") {
		t.Errorf("created is the file's: %s", str("created"))
	}
	if str("now") != "2026-10-02T20:30:00Z" {
		t.Errorf("now is the moment given, to the second: %s", str("now"))
	}
	changes := doc.LookupPath(cue.ParsePath("changes"))
	if n, _ := changes.Len().Int64(); changes.Kind() != cue.ListKind || n != 0 {
		t.Errorf("changes is an empty list, not null: %v", changes)
	}
	for _, absent := range []string{"trace_id", "hints", "summary.blast_radius", "policies"} {
		if doc.LookupPath(cue.ParsePath(absent)).Exists() {
			t.Errorf("%s must be absent from the judged document", absent)
		}
	}
}

func TestANewPolicyIsCheckedAgainstTheEmptyPlan(t *testing.T) {
	e := policyEngine()
	for name, text := range map[string]string{
		"a bound":                          "summary: destroy: <=2",
		"a rule that refuses every change": "changes: []",
		"a hidden helper":                  "_limit: 2\nsummary: destroy: <=_limit",
		"a constraint":                     "changes: [...{type: string, if type == \"x\" {after: encrypted!: true}}]",
		"the stdlib":                       "import \"strings\"\nsummary: destroy: 0\n_same: strings.ToLower(\"A\") & \"a\"",
		"a strict rule":                    "summary: add: <=0",
	} {
		if err := e.checkNewPolicy("p", text); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, text := range map[string]string{
		"a misspelt bound":                       "summary: destory: <=0",
		"a rule that is no CUE":                  "summary: {",
		"a module import":                        "import \"example.com/x/y\"\nsummary: destroy: 0",
		"a rule that cannot hold":                "summary: destroy: <0",
		"a misspelt field in a list template":    "changes: [...{adr: string}]",
		"a value outside the enum of an action":  "changes: [...{action: \"delte\"}]",
		"a field the judged document leaves out": "changes: [...{dependents: [...string]}]",
		"a field that is not in the document":    "extra: 1",
		"an optional field read without a test":  "changes: [...{type: string, action: string, name: before.id}]",
	} {
		err := e.checkNewPolicy("p", text)
		var coded *CodedError
		if err == nil || !errors.As(err, &coded) || coded.Code != ExitInvalid {
			t.Errorf("%s: want an error of exit 6, got %v", name, err)
		}
	}
}

func TestPolicyCounts(t *testing.T) {
	p := &Plan{Policies: []PolicyEntry{{Name: "a", Digest: "x"}, {Name: "b", Base: 2, Digest: "y"}, {Name: "c", Base: 1}, {Name: "d", Base: 3}}}
	if a, c, r := p.PolicyCounts(); a != 1 || c != 1 || r != 2 {
		t.Errorf("counts %d %d %d", a, c, r)
	}
	if !p.NeedsApply() || !p.HasChanges() || p.hasProviderChanges() {
		t.Error("a plan with policy entries needs an apply and asks, and changes no provider")
	}
	plan := &PlanFile{Changes: []savedChange{{Action: ActionNoop}, {Action: ActionRead}}}
	if plan.changesResources() {
		t.Error("reads are not resource changes")
	}
	plan.Changes = append(plan.Changes, savedChange{Action: ActionReplace})
	if !plan.changesResources() {
		t.Error("a replace is a resource change")
	}
}

// A plan that policies judged is kept: no collection removes it, however old.
func TestAKeptPlanIsNotCollected(t *testing.T) {
	eachBackend(t, func(t *testing.T, ps *PlanStore, clock *time.Time) {
		ctx := context.Background()
		kept := putPlan(t, ps, t0, "alice", 0, change("a", ActionCreate, 0))
		plain := putPlan(t, ps, t0, "bob", 0, change("b", ActionCreate, 0))
		for _, m := range []*PlanMeta{kept, plain} {
			if err := ps.MarkApplied(ctx, m.ID, []string{m.Changes[0].Addr}, "txn-"+m.ID, []string{m.Changes[0].Addr}); err != nil {
				t.Fatal(err)
			}
		}
		if err := ps.Keep(ctx, kept.ID); err != nil {
			t.Fatal(err)
		}
		if err := ps.Keep(ctx, kept.ID); err != nil {
			t.Fatalf("keeping twice: %v", err)
		}
		*clock = t0.Add(365 * 24 * time.Hour)
		res, err := ps.GC(ctx, GCOptions{Owner: "collector#1"})
		if err != nil {
			t.Fatal(err)
		}
		if ids(res.Removed) != plain.ID {
			t.Errorf("removed %s, want only %s", ids(res.Removed), plain.ID)
		}
		if _, _, err := ps.ReadFile(ctx, kept.ID); err != nil {
			t.Errorf("the kept plan is gone: %v", err)
		}
	})
}

// The schema that a rule is written against is the shape of the judged document: a
// field the document gains must be added to #Plan, or every rule that reads it fails.
func TestThePlanSchemaFitsTheJudgedDocument(t *testing.T) {
	e := policyEngine()
	doc, err := e.judgedDoc(&PlanFile{Format: PlanFileFormat, Created: time.Now(), Actor: "ci", Reason: "r", GitRevision: "abc", Providers: map[string]string{"local": "2.9.1"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pkg := e.Root.Ctx.CompileString(schema.SettingsPackage)
	plan := pkg.LookupPath(cue.MakePath(cue.Def("Plan")))
	if err := plan.Unify(doc).Validate(cue.Concrete(true)); err != nil {
		t.Fatalf("#Plan does not fit the judged document: %v", cueerrors.Details(err, nil))
	}
}
