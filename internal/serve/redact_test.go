package serve

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

func attrNamed(attrs []Attr, name string) *Attr {
	for i := range attrs {
		if attrs[i].Name == name {
			return &attrs[i]
		}
	}
	return nil
}

func TestDetailRedaction(t *testing.T) {
	p := newProject(t)
	raw := p.get("/api/detail?addr=services.a.db")
	noSecrets(t, "/api/detail", raw)
	var d Detail
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if !d.SchemaKnown {
		t.Fatal("the schema is known")
	}
	withheld := func(a *Attr, why string) {
		t.Helper()
		if a == nil {
			t.Error("the attribute is missing")
			return
		}
		if a.Redacted != why || a.Value != nil || len(a.Children) != 0 {
			t.Errorf("%s: redacted %q value %v children %d, want withheld as %q", a.Name, a.Redacted, a.Value, len(a.Children), why)
		}
	}
	withheld(attrNamed(d.Attrs, "blob"), "sensitive")
	withheld(attrNamed(d.Attrs, "api_token"), "name")
	withheld(attrNamed(d.Attrs, "payload"), "write-only")
	withheld(attrNamed(d.Attrs, "legacy"), "schema")
	if a := attrNamed(d.Attrs, "port"); a == nil || a.Value != float64(5432) || a.Type != "number" || a.Kind != "optional,computed" {
		t.Errorf("port: %+v", a)
	}
	if a := attrNamed(d.Attrs, "name"); a == nil || a.Value != "a" || a.Kind != "required" {
		t.Errorf("name: %+v", a)
	}
	tags := attrNamed(d.Attrs, "tags")
	if tags == nil || tags.Type != "map(string)" || len(tags.Children) != 2 {
		t.Fatalf("tags: %+v", tags)
	}
	if o := attrNamed(tags.Children, "owner"); o == nil || o.Value != "team" {
		t.Errorf("tags.owner: %+v", o)
	}
	withheld(attrNamed(tags.Children, "db_password"), "name")
	zones := attrNamed(d.Attrs, "zones")
	if zones == nil || len(zones.Children) != 2 || zones.Children[0].Name != "0" || zones.Children[1].Value != "eu-2" || zones.Value != nil {
		t.Errorf("zones: %+v", zones)
	}
	settings := attrNamed(d.Attrs, "settings")
	if settings == nil || settings.Type != "object" || len(settings.Children) != 2 {
		t.Fatalf("settings: %+v", settings)
	}
	if r := attrNamed(settings.Children, "region"); r == nil || r.Value != "eu" {
		t.Errorf("settings.region: %+v", r)
	}
	withheld(attrNamed(settings.Children, "key"), "sensitive")
	rule := attrNamed(d.Attrs, "rule")
	if rule == nil || rule.Type != "list(block)" || len(rule.Children) != 1 || len(rule.Children[0].Children) != 2 {
		t.Fatalf("rule: %+v", rule)
	}
	if c := attrNamed(rule.Children[0].Children, "cidr"); c == nil || c.Value != "10.0.0.0/8" {
		t.Errorf("rule.cidr: %+v", c)
	}
	withheld(attrNamed(rule.Children[0].Children, "pin"), "sensitive")
	// Every secret of the record is out of every endpoint, whichever node asks.
	for _, n := range p.graph().Nodes {
		noSecrets(t, "detail of "+n.Addr, p.get("/api/detail?addr="+queryEscape(n.Addr)))
		noSecrets(t, "source of "+n.Addr, p.get("/api/source?addr="+queryEscape(n.Addr)))
	}
	noSecrets(t, "search", p.get("/api/search?q=db"))
}

// A value that the schema does not mark can still hold a password: the content of
// a file that is built from one.
func TestDerivedValuesAreWithheld(t *testing.T) {
	p := newProject(t)
	d := p.detail("services.a.file")
	c := attrNamed(d.Attrs, "content")
	if c == nil || c.Redacted != "derived" || c.Value != nil {
		t.Errorf("content: %+v", c)
	}
	if a := attrNamed(d.Attrs, "path"); a == nil || a.Value != "/tmp/a" {
		t.Errorf("path: %+v", a)
	}
	if len(d.Notes) == 0 || !strings.Contains(strings.Join(d.Notes, " "), "withheld") {
		t.Errorf("notes %v", d.Notes)
	}
	noSecrets(t, "detail", p.get("/api/detail?addr=services.a.file"))
}

func TestMissingSchemaWithholdsEverything(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(dir + "/pravo.state")
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	p := start(t, dir, st, Options{Schemas: func(registry.Source, string) (Schemas, bool) { return nil, false }})
	g := p.graph()
	n := nodeOf(g, "services.a.db")
	if !n.NoSchema || n.Sensitive != 0 {
		t.Errorf("node: no_schema %v sensitive %d", n.NoSchema, n.Sensitive)
	}
	found := false
	for _, w := range g.Warnings {
		found = found || strings.Contains(w, "not cached") && strings.Contains(w, "fake")
	}
	if !found {
		t.Errorf("no warning about the schema: %v", g.Warnings)
	}
	d := p.detail("services.a.db")
	if d.SchemaKnown {
		t.Error("schema_known is true without a schema")
	}
	for _, a := range d.Attrs {
		if a.Redacted != "schema" || a.Value != nil || len(a.Children) != 0 {
			t.Errorf("%s: %+v", a.Name, a)
		}
	}
	if len(d.Attrs) != 10 {
		t.Errorf("%d attributes listed, want 10 (the names stay)", len(d.Attrs))
	}
	// A node without a record has nothing to withhold.
	if d := p.detail("hosts.x"); !d.SchemaKnown || len(d.Attrs) != 0 {
		t.Errorf("detail of a declared node: %+v", d)
	}
	for _, n := range g.Nodes {
		noSecrets(t, "detail of "+n.Addr, p.get("/api/detail?addr="+queryEscape(n.Addr)))
	}
	noSecrets(t, "graph", p.get("/api/graph"))
	// The outputs: the one that holds a secret under a name that looks like one is still caught.
	for _, o := range g.Outputs {
		if o.Name == "safe" && o.Value != nil {
			t.Errorf("sensitive output %+v", o)
		}
	}
}

func TestWalkBounds(t *testing.T) {
	str := json.RawMessage(`"string"`)
	b := &schema.Block{Attributes: []*schema.Attribute{
		{Name: "items", TypeJSON: json.RawMessage(`["list","string"]`), Optional: true},
		{Name: "text", TypeJSON: str, Optional: true},
		{Name: "secret_note", TypeJSON: str, Optional: true},
		{Name: "pin", TypeJSON: str, Optional: true, Sensitive: true},
		{Name: "dyn", TypeJSON: json.RawMessage(`"dynamic"`), Optional: true},
	}}
	items := make([]any, 250)
	for i := range items {
		items[i] = "item"
	}
	rec := map[string]any{"items": items, "text": strings.Repeat("é", 5000), "secret_note": nil, "pin": nil,
		"dyn": map[string]any{"a": []any{"x", map[string]any{"api_key": "k1-dynamic-secret"}}}}
	w := &walk{build: true, secrets: newSecretSet()}
	attrs := w.block(b, rec, 0)
	if a := attrNamed(attrs, "items"); len(a.Children) != maxChildren || a.Truncated != 150 {
		t.Errorf("items: %d children, %d left out", len(a.Children), a.Truncated)
	}
	text := attrNamed(attrs, "text")
	if s, _ := text.Value.(string); len(s) > maxText || len(s) < maxText-3 || text.Truncated != 10000-len(s) {
		t.Errorf("text: %d bytes kept, %d left out", len(s), text.Truncated)
	}
	for _, name := range []string{"secret_note", "pin"} {
		if a := attrNamed(attrs, name); !a.Null || a.Redacted != "" {
			t.Errorf("%s is null and says so: %+v", name, a)
		}
	}
	dyn := attrNamed(attrs, "dyn")
	raw, _ := json.Marshal(dyn)
	if strings.Contains(string(raw), "k1-dynamic-secret") {
		t.Errorf("a secret key of a dynamic value: %s", raw)
	}
	if w.withheld != 1 {
		t.Errorf("withheld %d, want 1", w.withheld)
	}
}

func TestSecretSet(t *testing.T) {
	s := newSecretSet()
	s.add("short")
	s.add("long-enough-secret")
	s.add(map[string]any{"k": []any{"nested-secret-value", 5, nil}})
	if s.leaks("a short thing") {
		t.Error("a string under the minimum is a secret")
	}
	for _, text := range []string{"prefix long-enough-secret suffix", "nested-secret-value"} {
		if !s.leaks(text) {
			t.Errorf("%q is not caught", text)
		}
	}
	if !s.leaksIn(map[string]any{"a": []any{"x", "has nested-secret-value in it"}}) {
		t.Error("a nested string is not caught")
	}
	if s.leaksIn("clean") || (*secretSet)(nil).leaks("x") {
		t.Error("false positives")
	}
}
