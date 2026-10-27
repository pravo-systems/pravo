package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/engine"
)

// try tries a rule, declared as the policy draft, on plans.
func (p *project) try(rule string, refs ...string) StudioTry {
	p.t.Helper()
	return p.tryFile(policyDeclaration("draft", "", rule), refs...)
}

// tryFile tries a file of policies on plans.
func (p *project) tryFile(text string, refs ...string) StudioTry {
	p.t.Helper()
	q := url.Values{"rule": {text}, "ref": refs}
	var out StudioTry
	if err := json.Unmarshal(p.get("/api/studio/try?"+q.Encode()), &out); err != nil {
		p.t.Fatal(err)
	}
	return out
}

// The studio lists the plans a rule can be tried on, the schema of the document and the rules to start from.
func TestStudioListsPlansAndExamples(t *testing.T) {
	p, _ := newPlanProject(t)
	var studio Studio
	if err := json.Unmarshal(p.get("/api/studio"), &studio); err != nil {
		t.Fatal(err)
	}
	if !studio.SpecOK || len(studio.Plans) != 1 {
		t.Fatalf("studio: %+v", studio)
	}
	pl := studio.Plans[0]
	if pl.Ref != "file:plan.pravo" || pl.Kind != "file" || pl.Add != 2 || pl.Change != 1 || pl.Destroy != 1 {
		t.Errorf("plan: %+v", pl)
	}
	for _, want := range []string{"#Policy", "#Plan", "#Change", "#Action"} {
		if !strings.Contains(studio.Schema, want) {
			t.Errorf("the schema lacks %s", want)
		}
	}
	// A policy offered to start from is a whole file that can be stored.
	tr := engine.NewTrier()
	for _, ex := range studio.Examples {
		if !strings.HasPrefix(ex.Policy, "import \"pravo.dev/pravo\"\n") || !strings.Contains(ex.Policy, ": pravo.#Policy & {") {
			t.Errorf("example %s is not a policy: %q", ex.ID, ex.Policy)
		}
		if res := tr.TryFile(ex.Policy, nil); res.Problem != nil || len(res.Policies) != 1 {
			t.Errorf("example %s: %+v", ex.ID, res)
		}
	}
}

// A rule is tried on the plans, and a refusal says where in the document and in which change.
func TestStudioTriesARuleOnPlans(t *testing.T) {
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
	if is.Path != "summary.destroy" || is.Got != "1" || is.Line != 5 || is.Policy != "draft" {
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
	if got.Check == nil || got.Check.Stage != "compile" || got.Plans[0].Draft.Outcome != engine.RuleInvalid {
		t.Errorf("a rule that does not fit: %+v", got)
	}
	// Several policies in one file are tried together, and a refusal names the policy.
	got = p.tryFile("import \"pravo.dev/pravo\"\n\nkeep: pravo.#Policy & {\n\trule: summary: destroy: <=1\n}\n\nnone: pravo.#Policy & {\n\trule: summary: add: 0\n}\n", "file:plan.pravo")
	if got.Check != nil || len(got.Policies) != 2 || got.Plans[0].Draft.Outcome != engine.RuleRefused || got.Plans[0].Draft.Issues[0].Policy != "none" {
		t.Errorf("two policies: %+v", got)
	}
	if after := digestTree(t, p.dir); after != before {
		t.Error("the studio changed the project")
	}
}

// A refusal quotes the values it names, and no value that the viewer withholds is among them.
func TestStudioDoesNotQuoteSecrets(t *testing.T) {
	p, _ := newPlanProject(t)
	rule := "changes: [...{after?: {tags?: {db_password: \"nope\", ...}, ...}, ...}]\n"
	got := p.try(rule, "file:plan.pravo")
	raw, _ := json.Marshal(got)
	if got.Plans[0].Draft.Outcome != engine.RuleRefused {
		t.Fatalf("want a refusal: %s", raw)
	}
	noSecrets(t, "try", raw, append(allSecrets, "new-map-secret-5555")...)

	for _, q := range []string{"/api/studio/doc?ref=file:plan.pravo&i=0", "/api/studio/doc?ref=file:plan.pravo"} {
		body := p.get(q)
		noSecrets(t, q, body, append(allSecrets, "new-blob-value-9999", "new-map-secret-5555", "new-nested-key-6666", "new-pin-value-7777")...)
	}
}

// The document lists the changes in the order a rule's paths count them.
func TestStudioDocument(t *testing.T) {
	p, _ := newPlanProject(t)
	var d StudioDoc
	if err := json.Unmarshal(p.get("/api/studio/doc?ref=file:plan.pravo&i=3"), &d); err != nil {
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
func TestStudioStoredPlansAndBadRequests(t *testing.T) {
	p := newProject(t)
	id := storePlan(t, p, stored{Author: "ana@acme"})
	waitFor(t, "the stored plan", func() bool { return len(p.plans().Plans) == 1 })
	var studio Studio
	if err := json.Unmarshal(p.get("/api/studio"), &studio); err != nil {
		t.Fatal(err)
	}
	if len(studio.Plans) < 1 || studio.Plans[0].Ref != id || studio.Plans[0].Kind != "stored" || !studio.Plans[0].Open {
		t.Fatalf("plans: %+v", studio.Plans)
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
		"/api/studio/try?rule=" + url.QueryEscape(strings.Repeat("a: 1\n", 10000)): http.StatusBadRequest,
		"/api/studio/doc":                         http.StatusBadRequest,
		"/api/studio/doc?ref=plan-ffffffff":       http.StatusNotFound,
		"/api/studio/doc?ref=file:plan.pravo&i=x": http.StatusNotFound,
	} {
		if resp, body := p.do("GET", path, nil); resp.StatusCode != want {
			t.Errorf("%s: %d, want %d: %s", path[:min(len(path), 60)], resp.StatusCode, want, body)
		}
	}
	if resp, _ := p.do("POST", "/api/studio/try", nil); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", resp.StatusCode)
	}
}

// The fields of the document are listed for completion, with their docs, the fields of a change, and the values of an action.
func TestStudioSchemaTree(t *testing.T) {
	var find = func(fs []SchemaField, name string) *SchemaField {
		for i := range fs {
			if fs[i].Name == name {
				return &fs[i]
			}
		}
		return nil
	}
	fs := planSchema()
	sum := find(fs, "summary")
	if sum == nil || find(sum.Fields, "destroy") == nil || sum.Doc == "" {
		t.Fatalf("summary: %+v", sum)
	}
	ch := find(fs, "changes")
	if ch == nil || !ch.List || find(ch.Fields, "addr") == nil {
		t.Fatalf("changes: %+v", ch)
	}
	if act := find(ch.Fields, "action"); act == nil || len(act.Enum) < 8 || act.Enum[0] != "create" {
		t.Errorf("action: %+v", act)
	}
	if af := find(ch.Fields, "after"); af == nil || !af.Optional {
		t.Errorf("after: %+v", af)
	}
}

// A plan file that is dropped on the studio is judged like one of the project and is written nowhere.
func TestStudioTakesADroppedPlanFile(t *testing.T) {
	p, _ := newPlanProject(t)
	data, err := os.ReadFile(filepath.Join(p.dir, "plan.pravo"))
	if err != nil {
		t.Fatal(err)
	}
	before := digestTree(t, p.dir)
	post := func(path, ctype string, body []byte) (*http.Response, []byte) {
		req, _ := http.NewRequest(http.MethodPost, p.base+path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+p.srv.Token())
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		return p.send(req)
	}
	resp, body := post("/api/studio/plan?name="+url.QueryEscape("../../saved/dropped.json"), "application/octet-stream", data)
	var pl StudioPlan
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &pl) != nil {
		t.Fatalf("upload: %d %s", resp.StatusCode, body)
	}
	if !strings.HasPrefix(pl.Ref, "upload:") || pl.Kind != "upload" || pl.Label != "dropped.json" || pl.Add != 2 || pl.Change != 1 || pl.Destroy != 1 {
		t.Errorf("plan: %+v", pl)
	}
	var studio Studio
	if err := json.Unmarshal(p.get("/api/studio"), &studio); err != nil {
		t.Fatal(err)
	}
	if len(studio.Plans) != 2 || studio.Plans[1].Ref != pl.Ref {
		t.Errorf("the list lacks the dropped file: %+v", studio.Plans)
	}
	got := p.try("summary: destroy: 0\n", pl.Ref, "file:plan.pravo")
	if len(got.Plans) != 2 || got.Plans[0].Draft == nil || got.Plans[1].Draft == nil || got.Plans[0].Draft.Outcome != got.Plans[1].Draft.Outcome || got.Plans[0].Draft.Outcome != "refused" {
		t.Errorf("the dropped file is judged like the file: %+v", got.Plans)
	}
	// What is not a plan file, a wrong type and a GET are refused, and a file that was never dropped is not held.
	if r, b := post("/api/studio/plan", "application/octet-stream", []byte("{}")); r.StatusCode != http.StatusBadRequest {
		t.Errorf("not a plan: %d %s", r.StatusCode, b)
	}
	if r, b := post("/api/studio/plan", "text/plain", data); r.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("wrong type: %d %s", r.StatusCode, b)
	}
	if r, _ := p.do("GET", "/api/studio/plan", nil); r.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", r.StatusCode)
	}
	if r, _ := post("/api/graph", "application/octet-stream", data); r.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST elsewhere: %d", r.StatusCode)
	}
	if got := p.try("summary: destroy: 0\n", "upload:000000000000"); got.Plans[0].State != "unknown" {
		t.Errorf("an unknown file: %+v", got.Plans[0])
	}
	if after := digestTree(t, p.dir); after != before {
		t.Error("the dropped file changed the project")
	}
}

func TestPolicyDeclarationHoistsImports(t *testing.T) {
	got := policyDeclaration("tagged", "", "import \"list\"\n\nchanges: []\n")
	want := "import (\n\t\"pravo.dev/pravo\"\n\t\"list\"\n)\n\ntagged: pravo.#Policy & {\n\trule: {\n\t\tchanges: []\n\t}\n}\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The state is a plan that updates every resource to what it holds, so a rule can be tried on what exists.
func TestStudioTriesARuleOnTheState(t *testing.T) {
	p, _ := newPlanProject(t)
	var studio Studio
	if err := json.Unmarshal(p.get("/api/studio"), &studio); err != nil {
		t.Fatal(err)
	}
	if studio.State == nil || studio.State.Ref != "state" || studio.State.Resources == 0 {
		t.Fatalf("state: %+v", studio.State)
	}
	got := p.try("changes: [...{type: \"no_such_type\", ...}]\n", "state")
	tp := got.Plans[0]
	if tp.State != "judged" || tp.Changes != studio.State.Resources || tp.Draft == nil || tp.Draft.Outcome != engine.RuleRefused {
		t.Fatalf("state: %+v", tp)
	}
	if is := tp.Draft.Issues[0]; is.Change == "" {
		t.Errorf("a refusal names the resource: %+v", is)
	}
	got = p.try("changes: [...{action: \"update\", ...}]\n", "state")
	if tp := got.Plans[0]; tp.Draft == nil || tp.Draft.Outcome != engine.RuleHolds {
		t.Errorf("a rule that holds: %+v", tp)
	}
	raw, _ := json.Marshal(p.try("changes: [...{type: \"no_such_type\", ...}]\n", "state"))
	noSecrets(t, "try state", raw, allSecrets...)
	noSecrets(t, "doc state", p.get("/api/studio/doc?ref=state"), allSecrets...)
}
