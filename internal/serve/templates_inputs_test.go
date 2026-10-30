package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A second template, whose inputs are not all typed in: values from a node of the graph, a resource to refer to, a list, a
// long text and a secret.
const askSpecCUE = templateSpecCUE + `
#Pool: pravo.#Template & {
	$template: {
		name:   "pool"
		inputs: ["name", "zone", "tag", "owner_db", "hosts", "notes", "key"]
		ask: {
			zone:     {choices: "boxes.one.db.zones"}
			tag:      {choices: "boxes.one.db.tags"}
			owner_db: {entity: {type: "fake_db", attr: "name"}}
			notes:    {long: true}
			key:      {secret: true}
		}
	}
	name:     string & =~"^[a-z]+$"
	zone:     string
	tag:      *"owner" | string
	owner_db: string
	hosts:    *[] | [...string]
	notes:    *"" | string
	key:      string
	member: $fake.#Resource.fake_db & {"name": name, payload: key, zones: [zone], tags: {(tag): owner_db}}
}
`

func askProject(t *testing.T) *project {
	t.Helper()
	return templateProjectOf(t, askSpecCUE, fmt.Sprintf(dbAttrs, "one"))
}

func (p *project) tryPool(name string, q url.Values) TemplateTry {
	p.t.Helper()
	q.Set("template", "pool")
	q.Set("name", name)
	q.Set("at", "pools")
	var out TemplateTry
	if err := json.Unmarshal(p.get("/api/templates/try?"+q.Encode()), &out); err != nil {
		p.t.Fatal(err)
	}
	return out
}

// What the template says in $template.ask shows in the inputs, and nothing else changes them.
func TestTemplateAskShapesTheInputs(t *testing.T) {
	p := askProject(t)
	var pool *TemplateEntry
	cat := p.templates()
	for i := range cat.Templates {
		if cat.Templates[i].Name == "pool" {
			pool = &cat.Templates[i]
		}
	}
	if pool == nil || len(pool.Inputs) != 7 {
		t.Fatalf("pool: %+v", pool)
	}
	by := map[string]TemplateInput{}
	for _, in := range pool.Inputs {
		by[in.Name] = in
	}
	if z := by["zone"]; z.Choices != "boxes.one.db.zones" || z.Kind != "string" {
		t.Errorf("zone: %+v", z)
	}
	if o := by["owner_db"]; o.Entity == nil || o.Entity.Type != "fake_db" || o.Entity.Attr != "name" {
		t.Errorf("owner_db: %+v", o)
	}
	if h := by["hosts"]; h.Kind != "list" || h.Elem != "string" {
		t.Errorf("hosts: %+v", h)
	}
	if n := by["notes"]; !n.Long {
		t.Errorf("notes: %+v", n)
	}
	if k := by["key"]; !k.Secret {
		t.Errorf("key: %+v", k)
	}
	if n := by["name"]; n.Long || n.Secret || n.Choices != "" || n.Entity != nil {
		t.Errorf("name takes no hint: %+v", n)
	}
	// The other template says nothing and keeps typed inputs.
	for _, tpl := range cat.Templates {
		if tpl.Name != "box" {
			continue
		}
		for _, in := range tpl.Inputs {
			if in.Long || in.Secret || in.Choices != "" || in.Entity != nil {
				t.Errorf("box %s: %+v", in.Name, in)
			}
		}
	}
}

func (p *project) choices(input string) TemplateChoices {
	p.t.Helper()
	var out TemplateChoices
	if err := json.Unmarshal(p.get("/api/templates/choices?template=pool&input="+input), &out); err != nil {
		p.t.Fatal(err)
	}
	return out
}

// The values on offer are what the state holds, shown as the inventory shows them: a list offers its items, a map its
// keys, and a value that the inventory withholds is not offered.
func TestTemplateChoicesReadTheRecordedNode(t *testing.T) {
	p := askProject(t)
	zones := p.choices("zone")
	if zones.State != "ok" || zones.Node != "boxes.one.db" || len(zones.Items) != 2 || zones.Items[0].Value != "eu-1" || zones.Items[1].Value != "eu-2" || zones.ReadAt == nil {
		t.Errorf("zones: %+v", zones)
	}
	tags := p.choices("tag")
	if tags.State != "ok" || len(tags.Items) != 1 || tags.Items[0].Value != "owner" || tags.Items[0].Label != "team" || tags.Omitted != 1 {
		t.Errorf("tags: %+v", tags)
	}
	body, _ := json.Marshal(tags)
	if strings.Contains(string(body), secretMapKey) {
		t.Errorf("a withheld value is in the answer: %s", body)
	}
}

// A node that is not recorded is not a source of values, and the answer says why instead of guessing.
func TestTemplateChoicesSayWhyTheyAreEmpty(t *testing.T) {
	p := templateProjectOf(t, strings.Replace(askSpecCUE, "boxes.one.db.zones", "boxes.two.db.zones", 1), `{"name":"one","port":5432}`)
	c := p.choices("zone")
	if c.State != "unread" || len(c.Items) != 0 || c.Node != "boxes.two.db" || !strings.Contains(c.Note, "no record") {
		t.Errorf("unrecorded: %+v", c)
	}
	p = templateProjectOf(t, strings.Replace(askSpecCUE, "boxes.one.db.zones", "nowhere.at.all", 1), `{"name":"one"}`)
	if c := p.choices("zone"); c.State != "missing" || len(c.Items) != 0 {
		t.Errorf("no such node: %+v", c)
	}
}

// Choices and entities answer only for an input that asks for them.
func TestTemplateInputEndpointsRefuseWhatTheTemplateDidNotAsk(t *testing.T) {
	p := askProject(t)
	for name, path := range map[string]string{
		"choices of a typed input":   "/api/templates/choices?template=pool&input=name",
		"entities of a typed input":  "/api/templates/entities?template=pool&input=zone",
		"choices of an entity input": "/api/templates/choices?template=pool&input=owner_db",
		"no such input":              "/api/templates/choices?template=pool&input=nope",
		"no such template":           "/api/templates/choices?template=nope&input=zone",
	} {
		if resp, body := p.do("GET", path, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d %s", name, resp.StatusCode, body)
		}
	}
	if resp, _ := p.do("POST", "/api/templates/choices?template=pool&input=zone", nil); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", resp.StatusCode)
	}
}

// The resources on offer are the managed ones of the type that the specification declares, and an attribute that is
// sensitive or write-only is never offered to read.
func TestTemplateEntitiesListDeclaredResources(t *testing.T) {
	p := askProject(t)
	var got TemplateEntities
	if err := json.Unmarshal(p.get("/api/templates/entities?template=pool&input=owner_db"), &got); err != nil {
		t.Fatal(err)
	}
	var exprs []string
	for _, e := range got.Entities {
		exprs = append(exprs, e.Expr)
		if e.Addr == "boxes.one.db" && !e.Recorded {
			t.Errorf("boxes.one.db is recorded: %+v", e)
		}
		if e.Addr == "boxes.two.db" && e.Recorded {
			t.Errorf("boxes.two.db is not recorded: %+v", e)
		}
	}
	for _, want := range []string{"boxes.one.db.name", "boxes.two.db.name", "plain.name"} {
		found := false
		for _, e := range exprs {
			found = found || e == want
		}
		if !found {
			t.Errorf("%s is not offered: %v", want, exprs)
		}
	}
	if err := json.Unmarshal(p.get("/api/templates/entities?template=pool&input=owner_db&q=plain"), &got); err != nil || len(got.Entities) != 1 || got.Entities[0].Addr != "plain" {
		t.Errorf("filtered: %+v %v", got, err)
	}

	for _, attr := range []string{"blob", "payload"} {
		p := templateProjectOf(t, strings.Replace(askSpecCUE, `attr: "name"`, `attr: "`+attr+`"`, 1), fmt.Sprintf(dbAttrs, "one"))
		var none TemplateEntities
		if err := json.Unmarshal(p.get("/api/templates/entities?template=pool&input=owner_db"), &none); err != nil {
			t.Fatal(err)
		}
		if len(none.Entities) != 0 || none.Note == "" {
			t.Errorf("%s is offered: %+v", attr, none)
		}
	}
}

// A choice must be one of the values the state holds; CUE holds the rest at plan.
func TestTemplateTryHoldsAChoiceToTheState(t *testing.T) {
	p := askProject(t)
	base := func(zone string) url.Values {
		return url.Values{"v.name": {`"a"`}, "v.zone": {zone}, "v.owner_db": {"plain.name"}, "s.key": {"POOL_KEY"}, "sk.key": {"env"}}
	}
	ok := p.tryPool("a", base(`"eu-1"`))
	if ok.State != "valid" {
		t.Fatalf("a listed zone: %+v", ok)
	}
	bad := p.tryPool("a", base(`"mars-1"`))
	if bad.State != "invalid" || len(bad.Problems) != 1 || bad.Problems[0].Field != "zone" || !strings.Contains(bad.Problems[0].Message, "is not one of the values that boxes.one.db holds") {
		t.Errorf("an unlisted zone: %+v", bad)
	}
}

// A list, a long text and a reference are written as CUE and not as strings.
func TestTemplateTryWritesListsAndReferences(t *testing.T) {
	p := askProject(t)
	out := p.tryPool("a", url.Values{
		"v.name": {`"a"`}, "v.zone": {`"eu-2"`}, "v.owner_db": {"plain.name"}, "v.hosts": {`["x.example", "y.example"]`},
		"v.notes": {`"one\ntwo"`}, "s.key": {"POOL_KEY"}, "sk.key": {"env"},
	})
	if out.State != "valid" {
		t.Fatalf("%+v", out)
	}
	for _, want := range []string{"owner_db: plain.name", `hosts: ["x.example", "y.example"]`, `notes: "one\ntwo"`} {
		if !strings.Contains(out.Snippet, want) {
			t.Errorf("snippet lacks %q:\n%s", want, out.Snippet)
		}
	}
}

// A secret input is declared in the snippet and read from there; the value is not in the code nor in the answer, and
// the form never sends one.
func TestTemplateTryDeclaresASecret(t *testing.T) {
	p := askProject(t)
	vals := func() url.Values {
		return url.Values{"v.name": {`"a"`}, "v.zone": {`"eu-1"`}, "v.owner_db": {"plain.name"}}
	}

	q := vals()
	q.Set("s.key", "POOL_KEY")
	q.Set("sk.key", "env")
	env := p.tryPool("a", q)
	if env.State != "valid" || len(env.Secrets) != 1 {
		t.Fatalf("env: %+v", env)
	}
	if sec := env.Secrets[0]; sec.Input != "key" || sec.Source != "env" || sec.Name != "POOL_KEY" || sec.Decl != "a_key" || sec.Command != "" {
		t.Errorf("env secret: %+v", sec)
	}
	for _, want := range []string{"key: a_key.value", `a_key: pravo.#SecretEnv & {name: "POOL_KEY"}`} {
		if !strings.Contains(env.Snippet, want) {
			t.Errorf("snippet lacks %q:\n%s", want, env.Snippet)
		}
	}

	// A secret of the state needs a sealed state, which this project does not have: that is a problem of the field.
	q = vals()
	q.Set("s.key", "pool/a/key")
	st := p.tryPool("a", q)
	if st.State != "invalid" || len(st.Problems) == 0 || st.Problems[0].Field != "key" || !strings.Contains(st.Problems[0].Message, "not sealed") {
		t.Errorf("state: %+v", st)
	}

	// Nothing that is not a secret input takes a secret, and a value for a secret input is never read from the request.
	for name, bad := range map[string]url.Values{"not a secret input": {"s.zone": {"X"}}, "a name that is not one": {"s.key": {"a b"}, "sk.key": {"env"}}} {
		q := vals()
		for k, v := range bad {
			q[k] = v
		}
		q.Set("template", "pool")
		q.Set("name", "a")
		resp, body := p.do("GET", "/api/templates/try?"+q.Encode(), nil)
		if name == "not a secret input" && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, resp.StatusCode, body)
		}
		if name == "a name that is not one" && !strings.Contains(string(body), "not a name") {
			t.Errorf("%s: %d %s", name, resp.StatusCode, body)
		}
	}
	q = vals()
	q.Set("v.key", `"hunter2"`)
	q.Set("s.key", "POOL_KEY")
	q.Set("sk.key", "env")
	q.Set("template", "pool")
	q.Set("name", "a")
	if resp, body := p.do("GET", "/api/templates/try?"+q.Encode(), nil); strings.Contains(string(body), "hunter2") {
		t.Errorf("a typed secret value is echoed: %d %s", resp.StatusCode, body)
	}
}
