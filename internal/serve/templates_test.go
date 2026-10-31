package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

const templateSpecCUE = `package demo

import "pravo.dev/pravo"

$fake: {
	$kind:   "provider"
	$source: "example.test/x/fake"
	#Resource: fake_db: {
		$kind:   "resource"
		$type:   "fake_db"
		$source: "example.test/x/fake"
		name:    string
		...
	}
}

// A box with a database of its own.
#Box: pravo.#Template & {
	$template: {
		name:        "box"
		title:       "Box"
		description: "A database in a box."
		inputs: {name: {}, port: {}, tier: {}}
	}
	// Lowercase letters only.
	name: string & =~"^[a-z]+$"
	// The port it listens on.
	port: *5432 | int & >1024
	tier: *"small" | "large"
	db: $fake.#Resource.fake_db & {"name": name, "port": port}
}

boxes: {
	one: #Box & {name: "one"}
	two: #Box & {name: "two", port: 6000, tier: "large"}
}

plain: $fake.#Resource.fake_db & {name: "plain"}
`

// templateProject is a project whose specification has a template with two instances.
func templateProject(t *testing.T) *project {
	t.Helper()
	return templateProjectOf(t, templateSpecCUE, `{"name":"one","port":5432}`)
}

// templateProjectOf is a project with the given specification, and a record of boxes.one.db with the given attributes.
func templateProjectOf(t *testing.T, spec, attrs string, more ...*state.Resource) *project {
	t.Helper()
	dir := t.TempDir()
	writeSpec(t, dir, spec)
	pkg := filepath.Join(dir, "cue.mod", "gen", "pravo.dev", "pravo", "settings_gen.cue")
	if err := os.MkdirAll(filepath.Dir(pkg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pkg, []byte(schema.SettingsPackage), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	txn := st.Begin("apply", "pravo apply")
	r := record("boxes.one.db", "fake_db", attrs)
	if err := st.Set(r, txn.ID()); err != nil {
		t.Fatal(err)
	}
	txn.Record(r.Address(), "create", 0, r.Revision)
	for _, m := range more {
		if err := st.Set(m, txn.ID()); err != nil {
			t.Fatal(err)
		}
		txn.Record(m.Address(), "read", 0, m.Revision)
	}
	if err := txn.Finish("applied", "1 created", true); err != nil {
		t.Fatal(err)
	}
	return start(t, dir, st, Options{})
}

func (p *project) templates() Templates {
	p.t.Helper()
	var out Templates
	if err := json.Unmarshal(p.get("/api/templates"), &out); err != nil {
		p.t.Fatal(err)
	}
	return out
}

func (p *project) tryTemplate(name, at string, values map[string]string) TemplateTry {
	p.t.Helper()
	q := url.Values{"template": {"box"}, "name": {name}}
	if at != "" {
		q.Set("at", at)
	}
	for k, v := range values {
		q.Set("v."+k, v)
	}
	var out TemplateTry
	if err := json.Unmarshal(p.get("/api/templates/try?"+q.Encode()), &out); err != nil {
		p.t.Fatal(err)
	}
	return out
}

// The catalog lists a template with the inputs the definition gives, and its instances with their values.
func TestTemplatesCatalog(t *testing.T) {
	p := templateProject(t)
	cat := p.templates()
	if !cat.SpecOK || len(cat.Templates) != 1 {
		t.Fatalf("catalog: %+v", cat)
	}
	tpl := cat.Templates[0]
	if tpl.Name != "box" || tpl.Title != "Box" || tpl.Definition != "#Box" || tpl.Description != "A database in a box." || tpl.Resources != 1 {
		t.Errorf("entry: %+v", tpl)
	}
	if len(tpl.Inputs) != 3 {
		t.Fatalf("inputs: %+v", tpl.Inputs)
	}
	name, port, tier := tpl.Inputs[0], tpl.Inputs[1], tpl.Inputs[2]
	if name.Name != "name" || !name.Required || name.Kind != "string" || name.Doc != "Lowercase letters only." || !strings.Contains(name.Type, `=~"^[a-z]+$"`) {
		t.Errorf("name: %+v", name)
	}
	if port.Name != "port" || port.Required || port.Kind != "int" || port.Default != "5432" {
		t.Errorf("port: %+v", port)
	}
	if tier.Kind != "enum" || tier.Default != `"small"` || len(tier.Options) != 2 {
		t.Errorf("tier: %+v", tier)
	}
	if len(tpl.Instances) != 2 {
		t.Fatalf("instances: %+v", tpl.Instances)
	}
	one, two := tpl.Instances[0], tpl.Instances[1]
	if one.Address != "boxes.one" || one.Resources != 1 || one.Recorded != 1 || one.Values["name"] != `"one"` {
		t.Errorf("one: %+v", one)
	}
	if two.Address != "boxes.two" || two.Recorded != 0 || two.Values["port"] != "6000" || two.Values["tier"] != `"large"` {
		t.Errorf("two: %+v", two)
	}
	if len(tpl.Targets) != 1 || tpl.Targets[0].Path != "boxes" || tpl.Targets[0].Count != 2 {
		t.Errorf("targets: %+v", tpl.Targets)
	}
}

// An instance is an ordinary group: the graph says which template it came from, and a plain resource is not one.
func TestTemplateMarksTheNamespaceOfAnInstance(t *testing.T) {
	p := templateProject(t)
	g := p.graph()
	seen := map[string]*NamespaceTemplate{}
	for _, ns := range g.Namespaces {
		seen[ns.Path] = ns.Template
	}
	if tp := seen["boxes.one"]; tp == nil || tp.Name != "box" || tp.Title != "Box" {
		t.Errorf("boxes.one: %+v (all: %v)", tp, seen)
	}
	if tp := seen["boxes"]; tp != nil {
		t.Errorf("boxes is not an instance: %+v", tp)
	}
}

// The wizard builds the snippet and loads the specification with it laid over, in memory.
func TestTemplateTryChecksWithoutWriting(t *testing.T) {
	p := templateProject(t)
	before := digestTree(t, p.dir)

	ok := p.tryTemplate("three", "boxes", map[string]string{"name": `"three"`, "port": "7000"})
	if ok.State != "valid" || ok.Address != "boxes.three" {
		t.Fatalf("valid: %+v", ok)
	}
	for _, want := range []string{"boxes: three: #Box & {", `name: "three"`, "port: 7000"} {
		if !strings.Contains(ok.Snippet, want) {
			t.Errorf("snippet lacks %q:\n%s", want, ok.Snippet)
		}
	}
	if len(ok.Creates) != 1 || ok.Creates[0].Address != "boxes.three.db" || ok.Creates[0].Type != "fake_db" {
		t.Errorf("creates: %+v", ok.Creates)
	}

	// Without a value the form is not complete, which is not an error.
	inc := p.tryTemplate("three", "boxes", nil)
	if inc.State != "incomplete" || len(inc.Problems) != 1 || inc.Problems[0].Field != "name" {
		t.Errorf("incomplete: %+v", inc)
	}

	// A value that breaks the constraint is refused by CUE, and the message names the field.
	bad := p.tryTemplate("three", "boxes", map[string]string{"name": `"three"`, "port": "80"})
	if bad.State != "invalid" || len(bad.Problems) == 0 || bad.Problems[0].Field != "port" || !strings.Contains(bad.Problems[0].Message, "out of bound") {
		t.Errorf("invalid: %+v", bad)
	}
	// What the form sent is quoted back as it is, even when it equals a sensitive string of the state.
	echo := p.tryTemplate("three", "boxes", map[string]string{"name": `"three"`, "port": `"` + secretBlob + `"`})
	echoed := false
	for _, pr := range echo.Problems {
		echoed = echoed || strings.Contains(pr.Message, secretBlob)
	}
	if echo.State != "invalid" || !echoed {
		t.Errorf("a value of the form that equals a sensitive string is masked: %+v", echo)
	}
	for _, pr := range bad.Problems {
		if strings.Contains(pr.Message, "zz_pravo_template_preview") {
			t.Errorf("the message names the preview file: %q", pr.Message)
		}
	}

	// A value that is not CUE is a problem of its field.
	nc := p.tryTemplate("three", "boxes", map[string]string{"name": `"three`})
	if nc.State != "invalid" || len(nc.Problems) != 1 || nc.Problems[0].Field != "name" || !strings.HasPrefix(nc.Problems[0].Message, "not CUE") {
		t.Errorf("not CUE: %+v", nc)
	}

	// An address that is taken is refused on the name.
	ex := p.tryTemplate("one", "boxes", map[string]string{"name": `"one"`})
	if ex.State != "exists" || len(ex.Problems) != 1 || ex.Problems[0].Field != "@label" {
		t.Errorf("exists: %+v", ex)
	}

	if after := digestTree(t, p.dir); after != before {
		t.Error("trying wrote to the project")
	}
}

// The handler refuses what it cannot make sense of before it reads anything.
func TestTemplateTryRefusesBadRequests(t *testing.T) {
	p := templateProject(t)
	for name, q := range map[string]url.Values{
		"no such template": {"template": {"nope"}, "name": {"x"}},
		"no name":          {"template": {"box"}},
		"a name with CUE":  {"template": {"box"}, "name": {`x" & {`}},
		"a path with CUE":  {"template": {"box"}, "name": {"x"}, "at": {`a[0]`}},
		"unknown input":    {"template": {"box"}, "name": {"x"}, "v.nope": {"1"}},
	} {
		resp, body := p.do("GET", "/api/templates/try?"+q.Encode(), nil)
		if resp.StatusCode < 400 || resp.StatusCode >= 500 {
			t.Errorf("%s: %d %s", name, resp.StatusCode, body)
		}
	}
	resp, _ := p.do("POST", "/api/templates/try", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", resp.StatusCode)
	}
}

// The picker offers attributes that resources declare, with the value that the state holds as the Inventory shows it: a
// value that the Inventory withholds is withheld here, and a resource that is not applied has none yet.
func TestTemplateRefsShowWhatTheStateHolds(t *testing.T) {
	p := templateProjectOf(t, templateSpecCUE, fmt.Sprintf(dbAttrs, "one"))
	get := func(q string) []TemplateRef {
		var refs TemplateRefs
		if err := json.Unmarshal(p.get("/api/templates/refs?kind=string&q="+q), &refs); err != nil {
			t.Fatal(err)
		}
		return refs.Refs
	}
	byExpr := map[string]TemplateRef{}
	for _, r := range get("") {
		byExpr[r.Expr] = r
	}
	if r := byExpr["boxes.one.db.name"]; r.Value != `"one"` || r.ValueState != "known" {
		t.Errorf("a recorded value: %+v", r)
	}
	if r := byExpr["plain.name"]; r.Value != "" || r.ValueState != "unapplied" {
		t.Errorf("a resource that is not applied: %+v", r)
	}
	// The name says secret, so the Inventory withholds it, and so does the picker.
	if r, ok := byExpr["boxes.one.db.api_token"]; !ok || r.Value != "" || r.ValueState != "withheld" {
		t.Errorf("a withheld value: %+v (offered: %v)", r, ok)
	}
	// An attribute that the schema marks sensitive is not offered at all.
	if _, ok := byExpr["boxes.one.db.blob"]; ok {
		t.Error("a sensitive attribute is offered")
	}
	body := p.get("/api/templates/refs?kind=string")
	for _, secret := range []string{secretBlob, secretToken, secretWrite} {
		if strings.Contains(string(body), secret) {
			t.Errorf("a secret is in the answer: %s", secret)
		}
	}
	if len(get("plain")) == 0 || get("plain")[0].Expr != "plain.name" {
		t.Errorf("filtered: %v", get("plain"))
	}
	resp, _ := p.do("GET", "/api/templates/refs?kind=object", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("an unknown kind: %d", resp.StatusCode)
	}
}

// A specification without a template has an empty catalog, not an error.
func TestTemplatesCatalogIsEmptyWithoutTemplates(t *testing.T) {
	p := newProject(t)
	cat := p.templates()
	if !cat.SpecOK || len(cat.Templates) != 0 {
		t.Errorf("catalog: %+v", cat)
	}
}
