package serve

import (
	"encoding/json"
	"path/filepath"
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

// Sensitivity follows the references. A string that spells a sensitive value of a
// node that the reader does not read says nothing of that node: it is shown. One
// that the reader reads, directly or through another node, is withheld.
func TestDerivedFollowsWhatANodeReads(t *testing.T) {
	p := newProject(t)
	// Reads nothing: its name is the same text as the blob of services.a.db.
	if err := p.store.Set(record("extra.one", "fake_db", `{"name":"`+secretBlob+`","port":1}`), ""); err != nil {
		t.Fatal(err)
	}
	// Reads shared, which reads services.a.db: two steps from the blob.
	if err := p.store.Set(record("extra.two", "fake_db", `{"name":"via `+secretBlob+`","port":2}`, "shared"), ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the records", func() bool {
		return nodeOf(p.graph(), "extra.one") != nil && nodeOf(p.graph(), "extra.two") != nil && p.detail("extra.two").DependsOn != nil
	})
	name := func(addr string) Attr {
		for _, a := range p.detail(addr).Attrs {
			if a.Name == "name" {
				return a
			}
		}
		t.Fatalf("%s has no name", addr)
		return Attr{}
	}
	if a := name("extra.one"); a.Redacted != "" || a.Value != secretBlob {
		t.Errorf("a node that reads nothing of the blob is judged by itself: %+v", a)
	}
	if a := name("extra.two"); a.Redacted != "derived" || a.Value != nil {
		t.Errorf("a node that reads the blob through another is withheld: %+v", a)
	}
}

func TestSecretSetWithMadeIsScoped(t *testing.T) {
	s := newSecretSet()
	s.addFrom("a", "alpha-secret-1")
	s.addFrom("b", "bravo-secret-2")
	s.addFrom("", "global-secret-3")
	s.addFrom("a", "bravo-secret-2")
	got := s.withMade(map[string]bool{"a": true}, "")
	for text, want := range map[string]bool{"alpha-secret-1": true, "bravo-secret-2": true, "global-secret-3": true} {
		if got.leaks(text) != want {
			t.Errorf("scoped to a: leaks(%q) = %v", text, !want)
		}
	}
	got = s.withMade(map[string]bool{"c": true}, "")
	if got.leaks("alpha-secret-1") || got.leaks("bravo-secret-2") || !got.leaks("global-secret-3") {
		t.Errorf("scoped to c: %v", got.list)
	}
	// The node itself can be left out: its own strings are not among those a value of it is looked at for.
	got = s.withMade(map[string]bool{"a": true, "b": true}, "a")
	if got.leaks("alpha-secret-1") || !got.leaks("bravo-secret-2") || !got.leaks("global-secret-3") {
		t.Errorf("scoped to b, a left out: %v", got.list)
	}
	if (*secretSet)(nil).withMade(nil, "") != nil {
		t.Error("a nil set is nil")
	}
}

// A field is derived when it reads a withheld attribute, by what it reads and not
// by what its text spells: one that reads another attribute of the same node is
// not, one whose text says nothing of the secret is, and so is one that reads a
// derived field of another node.
func TestDerivedFollowsTheAttributeThatIsRead(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE+`
plain: $fake.#Resource.fake_file & {path: "/p", content: "n=\(services.a.db.name)"}
hashed: $fake.#Resource.fake_file & {path: "/h", content: "h=\(services.a.db.blob)"}
chained: $fake.#Resource.fake_file & {path: "/c", content: "again \(hashed.content)"}
`)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	// The text of plain says "a" and the blob of services.a.db: coincidence, not a read.
	for addr, content := range map[string]string{"plain": "n=a " + secretBlob, "hashed": "h=sha-0000", "chained": "again h=sha-0000"} {
		path := "/" + addr[:1]
		if err := st.Set(record(addr, "fake_file", `{"path":"`+path+`","content":"`+content+`"}`, "services.a.db"), ""); err != nil {
			t.Fatal(err)
		}
	}
	p := start(t, dir, st, Options{})
	content := func(addr string) Attr {
		for _, a := range p.detail(addr).Attrs {
			if a.Name == "content" {
				return a
			}
		}
		t.Fatalf("%s has no content", addr)
		return Attr{}
	}
	if a := content("plain"); a.Redacted != "" || a.Value != "n=a "+secretBlob {
		t.Errorf("a field that reads the name is judged by itself: %+v", a)
	}
	for _, addr := range []string{"hashed", "chained"} {
		if a := content(addr); a.Redacted != "derived" || a.Value != nil {
			t.Errorf("%s reads the blob, directly or through a field that does: %+v", addr, a)
		}
	}
}

// madeOnly is what every value is looked at for: the strings a provider made and the
// ones found in no node in particular. What a person typed into a node says nothing of
// the other nodes' values, whatever node it was typed into.
func TestSecretSetMadeOnlyKeepsWhatTheProviderMade(t *testing.T) {
	s := newSecretSet()
	s.addFrom("p", "eu-north-1") // typed into the node
	s.addFrom("p", "generated-secret-1")
	s.addMade("p", "generated-secret-1")
	s.addFrom("q", "typed-secret-in-q")
	s.addFrom("", "global-secret-2")
	made := s.madeOnly()
	for text, want := range map[string]bool{"eu-north-1": false, "typed-secret-in-q": false, "generated-secret-1": true, "global-secret-2": true} {
		if made.leaks(text) != want {
			t.Errorf("madeOnly().leaks(%q) = %v", text, !want)
		}
	}
	// A string added later is seen: the cache is dropped.
	s.addMade("q", "generated-by-q-3")
	if !s.madeOnly().leaks("generated-by-q-3") {
		t.Error("a string made after the first call is not looked for")
	}
	if (*secretSet)(nil).madeOnly() != nil {
		t.Error("a nil set is nil")
	}
}

// A secret that holds another is replaced first, whichever was found first: replacing the shorter one first leaves the rest of
// the longer in the text.
func TestSecretSetScrubReplacesALongerSecretFirst(t *testing.T) {
	for _, order := range [][]string{{"abcdef", "abcdef-ghijkl-mnopqr"}, {"abcdef-ghijkl-mnopqr", "abcdef"}} {
		s := newSecretSet()
		for _, x := range order {
			s.add(x)
		}
		got, changed := s.scrub("token=abcdef-ghijkl-mnopqr end, short=abcdef")
		if want := "token=" + mask + " end, short=" + mask; !changed || got != want {
			t.Errorf("%v: scrub = %q, %v; want %q", order, got, changed, want)
		}
	}
}

// A generated password has & and < in it, and an XML document or an HTML page writes them as entities.
func TestSecretSetFindsTheXMLFormOfASecret(t *testing.T) {
	const pw = `Zq7r&T2v<N9x!K4m#B6w'"`
	s := newSecretSet()
	s.addFrom("pw", pw)
	for _, text := range []string{
		"<password>Zq7r&amp;T2v&lt;N9x!K4m#B6w&apos;&quot;</password>",
		"<p>Zq7r&amp;T2v&lt;N9x!K4m#B6w&#39;&#34;</p>",
	} {
		if !s.leaks(text) {
			t.Errorf("not found in %q", text)
		}
	}
}
