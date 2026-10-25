package serve

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/engine"
)

func (p *project) try(rule string, refs ...string) WorkbenchTry {
	p.t.Helper()
	q := url.Values{"rule": {rule}, "ref": refs}
	var out WorkbenchTry
	if err := json.Unmarshal(p.get("/api/workbench/try?"+q.Encode()), &out); err != nil {
		p.t.Fatal(err)
	}
	return out
}

// The workbench lists the plans a rule can be tried on, the schema of the document and the rules to start from.
func TestWorkbenchListsPlansAndExamples(t *testing.T) {
	p, _ := newPlanProject(t)
	var wb Workbench
	if err := json.Unmarshal(p.get("/api/workbench"), &wb); err != nil {
		t.Fatal(err)
	}
	if !wb.SpecOK || len(wb.Plans) != 1 {
		t.Fatalf("workbench: %+v", wb)
	}
	pl := wb.Plans[0]
	if pl.Ref != "file:plan.pravo" || pl.Kind != "file" || pl.Add != 2 || pl.Change != 1 || pl.Destroy != 1 {
		t.Errorf("plan: %+v", pl)
	}
	for _, want := range []string{"#Policy", "#Plan", "#Change", "#Action"} {
		if !strings.Contains(wb.Schema, want) {
			t.Errorf("the schema lacks %s", want)
		}
	}
	// A rule offered to start from can be stored.
	tr := engine.NewTrier()
	for _, ex := range wb.Examples {
		if prob := tr.Check(ex.ID, ex.Rule); prob != nil {
			t.Errorf("example %s: %s", ex.ID, prob.Message)
		}
	}
}

// A rule is tried on the plans, and a refusal says where in the document and in which change.
func TestWorkbenchTriesARuleOnPlans(t *testing.T) {
	p, _ := newPlanProject(t)
	before := digestTree(t, p.dir)
	got := p.try("summary: destroy: 0\n", "file:plan.pravo")
	if got.Check != nil || len(got.Plans) != 1 {
		t.Fatalf("try: %+v", got)
	}
	tp := got.Plans[0]
	if tp.State != "judged" || tp.Changes != 4 || tp.Draft == nil || tp.Draft.Outcome != engine.RuleRefused {
		t.Fatalf("plan: %+v", tp)
	}
	is := tp.Draft.Issues[0]
	if is.Path != "summary.destroy" || is.Got != "1" || is.Line != 1 {
		t.Errorf("issue: %+v", is)
	}

	got = p.try("summary: destroy: <=1\n", "file:plan.pravo")
	if tp := got.Plans[0]; tp.Draft == nil || tp.Draft.Outcome != engine.RuleHolds {
		t.Errorf("a rule that holds: %+v", tp)
	}

	// The refusal names the change it is about.
	got = p.try("changes: [...{action: \"create\" | \"update\"}]\n", "file:plan.pravo")
	var found bool
	for _, is := range got.Plans[0].Draft.Issues {
		if is.Change == "old.thing" && strings.HasPrefix(is.Path, "changes.3.") {
			found = true
		}
	}
	if !found {
		t.Errorf("issues: %+v", got.Plans[0].Draft.Issues)
	}

	// A text that does not compile says so, and says nothing of the plan.
	got = p.try("summary: destroy: <=\n", "file:plan.pravo")
	if got.Check == nil || got.Check.Stage != "parse" || got.Plans[0].Draft.Outcome != engine.RuleInvalid {
		t.Errorf("a broken rule: %+v", got)
	}
	// A rule that is not storable is still tried: the plan shows what it would do.
	got = p.try("summary: destroy: 0\nstray: 1\n", "file:plan.pravo")
	if got.Check == nil || got.Check.Stage != "shape" || got.Plans[0].Draft.Outcome != engine.RuleRefused {
		t.Errorf("a rule that does not fit: %+v", got)
	}
	if after := digestTree(t, p.dir); after != before {
		t.Error("the workbench changed the project")
	}
}

// A refusal quotes the values it names, and no value that the viewer withholds is among them.
func TestWorkbenchDoesNotQuoteSecrets(t *testing.T) {
	p, _ := newPlanProject(t)
	rule := "changes: [...{after?: {tags?: {db_password: \"nope\", ...}, ...}, ...}]\n"
	got := p.try(rule, "file:plan.pravo")
	raw, _ := json.Marshal(got)
	if got.Plans[0].Draft.Outcome != engine.RuleRefused {
		t.Fatalf("want a refusal: %s", raw)
	}
	noSecrets(t, "try", raw, append(allSecrets, "new-map-secret-5555")...)

	for _, q := range []string{"/api/workbench/doc?ref=file:plan.pravo&i=0", "/api/workbench/doc?ref=file:plan.pravo"} {
		body := p.get(q)
		noSecrets(t, q, body, append(allSecrets, "new-blob-value-9999", "new-map-secret-5555", "new-nested-key-6666", "new-pin-value-7777")...)
	}
}

// The document lists the changes in the order a rule's paths count them.
func TestWorkbenchDocument(t *testing.T) {
	p, _ := newPlanProject(t)
	var d WorkbenchDoc
	if err := json.Unmarshal(p.get("/api/workbench/doc?ref=file:plan.pravo&i=3"), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Outline) != 4 || d.Outline[3].Addr != "old.thing" || d.Outline[3].Action != "delete" || d.Outline[3].Index != 3 {
		t.Errorf("outline: %+v", d.Outline)
	}
	if !strings.Contains(d.Change, `"addr": "old.thing"`) || !strings.Contains(d.Head, `"summary"`) || strings.Contains(d.Head, `"addr"`) {
		t.Errorf("document: head %q change %q", d.Head, d.Change)
	}
}

// Stored plans are tried like files, and a plan that is not known, or a request that is out of bounds, is refused.
func TestWorkbenchStoredPlansAndBadRequests(t *testing.T) {
	p := newProject(t)
	id := storePlan(t, p, stored{Author: "ana@acme"})
	waitFor(t, "the stored plan", func() bool { return len(p.plans().Plans) == 1 })
	var wb Workbench
	if err := json.Unmarshal(p.get("/api/workbench"), &wb); err != nil {
		t.Fatal(err)
	}
	if len(wb.Plans) < 1 || wb.Plans[0].Ref != id || wb.Plans[0].Kind != "stored" || !wb.Plans[0].Open {
		t.Fatalf("plans: %+v", wb.Plans)
	}
	got := p.try("summary: destroy: 0\n", id, "plan-ffffffff", "file:../elsewhere", "nonsense")
	if got.Plans[0].State != "judged" || got.Plans[0].Draft.Outcome != engine.RuleRefused {
		t.Errorf("stored plan: %+v", got.Plans[0])
	}
	for _, tp := range got.Plans[1:] {
		if tp.State != "unknown" || tp.Draft != nil {
			t.Errorf("an unknown plan: %+v", tp)
		}
	}
	for path, want := range map[string]int{
		"/api/workbench/try?rule=" + url.QueryEscape(strings.Repeat("a: 1\n", 10000)): http.StatusBadRequest,
		"/api/workbench/try?base=nope":               http.StatusNotFound,
		"/api/workbench/doc":                         http.StatusBadRequest,
		"/api/workbench/doc?ref=plan-ffffffff":       http.StatusNotFound,
		"/api/workbench/doc?ref=file:plan.pravo&i=x": http.StatusNotFound,
	} {
		if resp, body := p.do("GET", path, nil); resp.StatusCode != want {
			t.Errorf("%s: %d, want %d: %s", path[:min(len(path), 60)], resp.StatusCode, want, body)
		}
	}
	if resp, _ := p.do("POST", "/api/workbench/try", nil); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", resp.StatusCode)
	}
}
