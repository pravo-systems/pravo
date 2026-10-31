package serve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

func nodeOf(g Graph, addr string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].Addr == addr {
			return &g.Nodes[i]
		}
	}
	return nil
}

// resourceNodes are the nodes of the graph that are not outputs.
func resourceNodes(g Graph) []Node {
	var out []Node
	for _, n := range g.Nodes {
		if n.Mode != "output" {
			out = append(out, n)
		}
	}
	return out
}

func namespaceOf(g Graph, path string) *Namespace {
	for i := range g.Namespaces {
		if g.Namespaces[i].Path == path {
			return &g.Namespaces[i]
		}
	}
	return nil
}

func TestGraph(t *testing.T) {
	p := newProject(t)
	g := p.graph()
	if g.Version != APIVersion || g.Revision == "" {
		t.Errorf("version %d revision %q", g.Version, g.Revision)
	}
	if g.Project.SpecError != "" || g.Project.Package != "demo" || g.Project.Version != "test" {
		t.Errorf("project %+v", g.Project)
	}
	if st := g.Project.State; st.Backend != "local" || st.Records != 5 || st.Lineage == "" || st.Format != 1 {
		t.Errorf("state info %+v", st)
	}
	if len(g.Project.Providers) != 1 || g.Project.Providers[0].Local != "fake" || g.Project.Providers[0].Version != "1.0.0" {
		t.Errorf("providers %+v", g.Project.Providers)
	} else if in := g.Project.Providers[0].Instances; len(in) != 1 || in[0].Key != "fake" || in[0].Count == 0 {
		t.Errorf("instances %+v, want one, fake, used", in)
	}

	// Presence: both, spec and state.
	want := map[string]string{
		"services.a.db": "both", "services.a.file": "both", "services.b.db": "both", "shared": "both",
		"services.b.file": "spec", "hosts.x": "spec", "hosts.y": "spec",
		"old.thing": "state",
	}
	if n := len(resourceNodes(g)); n != len(want) {
		t.Errorf("%d resources, want %d", n, len(want))
	}
	for addr, presence := range want {
		n := nodeOf(g, addr)
		if n == nil {
			t.Errorf("no node %s", addr)
			continue
		}
		if n.Presence != presence {
			t.Errorf("%s is %s, want %s", addr, n.Presence, presence)
		}
		if (presence == "state") != (n.Source == nil) {
			t.Errorf("%s: source %+v for presence %s", addr, n.Source, presence)
		}
	}
	if !sort.SliceIsSorted(g.Nodes, func(i, j int) bool { return g.Nodes[i].Addr < g.Nodes[j].Addr }) {
		t.Errorf("the nodes are not in the order of their addresses")
	}

	// A resource that comes from a definition sits at the definition, once for each instance.
	a, b := nodeOf(g, "services.a.db"), nodeOf(g, "services.b.db")
	if a.Source == nil || *a.Source != *b.Source || a.Source.File != "main.cue" || a.Source.Line < 20 {
		t.Errorf("services.a.db is at %+v, services.b.db at %+v", a.Source, b.Source)
	}
	if a.Namespace != "services.a" || nodeOf(g, "shared").Namespace != "" || nodeOf(g, "hosts.x").Namespace != "hosts" {
		t.Errorf("namespaces of nodes: %q %q %q", a.Namespace, nodeOf(g, "shared").Namespace, nodeOf(g, "hosts.x").Namespace)
	}
	if a.Sensitive != 7 || a.Revision != 1 || a.RefreshedAt == nil {
		t.Errorf("services.a.db: sensitive %d revision %d refreshed %v", a.Sensitive, a.Revision, a.RefreshedAt)
	}
	// blob, api_token (by name), payload (write-only), tags.db_password (by key), settings.key, rule[0].pin, legacy (unknown to the schema).

	// Namespaces.
	for path, c := range map[string]struct {
		parent, name string
		resources    int
		depth        int
		instance     bool
	}{
		"services":   {"", "services", 4, 0, false},
		"services.a": {"services", "a", 2, 1, true},
		"services.b": {"services", "b", 2, 1, true},
		"hosts":      {"", "hosts", 2, 0, false},
		"old":        {"", "old", 1, 0, false},
	} {
		m := namespaceOf(g, path)
		if m == nil {
			t.Errorf("no namespace %s", path)
			continue
		}
		if m.Parent != c.parent || m.Name != c.name || m.Resources != c.resources || m.Depth != c.depth {
			t.Errorf("namespace %s is %+v, want %+v", path, *m, c)
		}
		if c.instance {
			if m.Definition == nil || m.Declared == nil || m.Definition.Line >= m.Declared.Line && false {
				t.Errorf("namespace %s: declared %+v definition %+v", path, m.Declared, m.Definition)
			}
			if m.Definition != nil && m.Declared != nil && m.Definition.Line == m.Declared.Line {
				t.Errorf("namespace %s: declared and definition are on the same line", path)
			}
		}
		if path != "old" && m.Declared == nil {
			t.Errorf("namespace %s has no position", path)
		}
	}
	if namespaceOf(g, "old").Declared != nil {
		t.Errorf("a namespace of the state alone has a position")
	}
	if namespaceOf(g, "services.a").Declared.Line == namespaceOf(g, "services.b").Declared.Line {
		t.Errorf("two instances at one line")
	}
	if len(g.Namespaces) != 5 {
		t.Errorf("%d namespaces", len(g.Namespaces))
	}

	// Edges run from the dependency to the dependent.
	edges := map[string]bool{}
	for _, e := range g.Edges {
		edges[e.Dependency+" -> "+e.Dependent] = true
	}
	for _, e := range []string{"services.a.db -> services.a.file", "services.b.db -> services.b.file", "services.a.db -> shared", "services.b.db -> old.thing", "services.a.db -> leak"} {
		if !edges[e] {
			t.Errorf("no edge %s; have %v", e, edges)
		}
	}
	if len(edges) != 5 {
		t.Errorf("%d edges: %v", len(edges), edges)
	}

	// Outputs.
	outs := map[string]Output{}
	for _, o := range g.Outputs {
		outs[o.Name] = o
	}
	if o := outs["host"]; o.Value != "db.example" || o.Sensitive || o.Unknown {
		t.Errorf("host output %+v", o)
	}
	if o := outs["leak"]; o.Value != nil || !o.Sensitive || o.Redacted != "derived" {
		t.Errorf("leak output %+v", o)
	}
	if o := outs["safe"]; o.Value != nil || !o.Sensitive || o.Redacted != "sensitive" {
		t.Errorf("safe output %+v", o)
	}
	if len(g.Plan.nilIfNone()) != 0 {
		t.Errorf("a plan without --plan: %+v", g.Plan)
	}
	raw := p.get("/api/graph")
	noSecrets(t, "/api/graph", raw)
	if len(raw) > 64<<10 {
		t.Errorf("the graph is %d bytes", len(raw))
	}
}

// nilIfNone lets a test ask whether the plan section is absent.
func (p *PlanInfo) nilIfNone() []string {
	if p == nil {
		return nil
	}
	return []string{p.File}
}

func TestSpecErrorShowsTheStateAlone(t *testing.T) {
	p := newProject(t)
	writeSpec(t, p.dir, "package demo\n\nbroken: {\n")
	waitFor(t, "the specification error", func() bool { return p.graph().Project.SpecError != "" })
	g := p.graph()
	if len(resourceNodes(g)) != 5 {
		t.Fatalf("%d resources from the state alone, want 5", len(resourceNodes(g)))
	}
	for _, n := range g.Nodes {
		if n.Presence != "state" || n.Source != nil {
			t.Errorf("%s: presence %s source %+v", n.Addr, n.Presence, n.Source)
		}
	}
	if len(g.Warnings) == 0 || !strings.Contains(g.Warnings[0], "does not load") {
		t.Errorf("warnings %v", g.Warnings)
	}
	// The edges come from the records.
	edges := 0
	for _, e := range g.Edges {
		if e.Dependency == "services.a.db" && e.Dependent == "services.a.file" {
			edges++
		}
	}
	if edges != 1 {
		t.Errorf("the edge of the record is missing: %+v", g.Edges)
	}
	// The values are redacted from the schema of the provider the record names.
	d := p.detail("services.a.db")
	if !d.SchemaKnown || len(d.Attrs) == 0 {
		t.Errorf("detail without a specification: %+v", d.SchemaKnown)
	}
	noSecrets(t, "/api/graph", p.get("/api/graph"))
	// Mending the specification brings the nodes back.
	writeSpec(t, p.dir, specCUE)
	waitFor(t, "the specification to load again", func() bool { return p.graph().Project.SpecError == "" })
	if n := nodeOf(p.graph(), "services.b.file"); n == nil || n.Presence != "spec" {
		t.Errorf("services.b.file after mending: %+v", n)
	}
}

func TestNoStateAndNoSpecification(t *testing.T) {
	dir := t.TempDir()
	srv, err := New(Options{Dir: dir, Schemas: fakeLookup})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pravo.state")); err == nil {
		t.Fatal("opening the viewer on an empty directory made a state")
	}
	m, err := srv.current()
	if err != nil {
		t.Fatal(err)
	}
	if len(m.graph.Nodes) != 0 || m.graph.Project.SpecError == "" || m.graph.Project.State.Records != 0 {
		t.Errorf("graph of an empty directory: %+v", m.graph.Project)
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Errorf("the viewer left %d entries in an empty directory", len(es))
	}
}

func TestSearch(t *testing.T) {
	p := newProject(t)
	var res Search
	if err := jsonUnmarshal(p.get("/api/search?q=file+b"), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].Addr != "services.b.file" {
		t.Errorf("search %+v", res.Nodes)
	}
	if err := jsonUnmarshal(p.get("/api/search?q=services"), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 4 || len(res.Namespaces) != 3 {
		t.Errorf("search services: %d nodes %d namespaces", len(res.Nodes), len(res.Namespaces))
	}
	if err := jsonUnmarshal(p.get("/api/search?q="), &res); err != nil || len(res.Nodes) != 0 {
		t.Errorf("an empty query: %v %d", err, len(res.Nodes))
	}
}

func TestDetailUnknownAndBadRequests(t *testing.T) {
	p := newProject(t)
	for path, want := range map[string]int{
		"/api/detail?addr=nope":     404,
		"/api/detail":               400,
		"/api/source?addr=nope":     404,
		"/api/source":               400,
		"/api/detail?addr=services": 404, // a namespace is not a node
	} {
		resp, body := p.do("GET", path, nil)
		if resp.StatusCode != want {
			t.Errorf("%s: %d %s, want %d", path, resp.StatusCode, body, want)
		}
		if want == 404 && !strings.Contains(string(body), `"error"`) {
			t.Errorf("%s: the 404 has no error: %s", path, body)
		}
	}
	d := p.detail("hosts.x")
	if d.Node.Presence != "spec" || len(d.Attrs) != 0 || len(d.Notes) == 0 || !strings.Contains(d.Notes[0], "not created") {
		t.Errorf("detail of a declared node: %+v", d)
	}
	d = p.detail("old.thing")
	if d.Node.Presence != "state" || len(d.Notes) == 0 || !strings.Contains(strings.Join(d.Notes, " "), "destroy") {
		t.Errorf("detail of an orphan: %+v", d.Notes)
	}
	if len(d.DependsOn) != 1 || d.DependsOn[0] != "services.b.db" {
		t.Errorf("an orphan depends on %v", d.DependsOn)
	}
	d = p.detail("services.a.db")
	if len(d.UsedBy) != 3 || len(d.Reach) != 2 {
		t.Errorf("services.a.db: used by %v, reach %v", d.UsedBy, d.Reach)
	}
	if len(d.History) != 1 || d.History[0].Kind != "apply" || d.History[0].Action != "create" || d.History[0].RevAfter != 1 {
		t.Errorf("history %+v", d.History)
	}
	if d.Times.AppliedAt == nil || d.Times.UpdatedAt == nil || d.Provider.Source != "example.test/x/fake" || d.Provider.Version != "1.0.0" {
		t.Errorf("times %+v provider %+v", d.Times, d.Provider)
	}
}

// Marks, times and holds of a record show on the node.
func TestMarksAndHolds(t *testing.T) {
	p := newProject(t)
	now := time.Now().UTC().Truncate(time.Second)
	drifted := record("services.a.file", "fake_file", `{"path":"/tmp/a","content":"changed"}`)
	drifted.DriftedAt = &now
	gone := record("shared", "fake_db", `{"name":"shared"}`)
	gone.DriftedAt, gone.Gone = &now, true
	for _, r := range []*state.Resource{drifted, gone} {
		if err := p.store.Set(r, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.store.ReserveAll([]string{"services.b.db"}, "pravo plan", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the marks", func() bool { n := nodeOf(p.graph(), "shared"); return n != nil && n.Gone })
	g := p.graph()
	if n := nodeOf(g, "services.a.file"); !n.Drifted || n.Gone || n.Revision != 2 {
		t.Errorf("drifted node %+v", n)
	}
	if n := nodeOf(g, "shared"); n.Drifted || !n.Gone {
		t.Errorf("gone node %+v", n)
	}
	if n := nodeOf(g, "services.b.db"); !n.Locked {
		t.Errorf("a reservation does not show on the node")
	}
	d := p.detail("services.a.file")
	if d.Times.DriftedAt == nil || !d.Times.DriftedAt.Equal(now) || !strings.Contains(strings.Join(d.Notes, " "), "differs") {
		t.Errorf("drifted detail: times %+v notes %v", d.Times, d.Notes)
	}
	if d := p.detail("shared"); !strings.Contains(strings.Join(d.Notes, " "), "no longer exists") {
		t.Errorf("gone detail: %v", d.Notes)
	}
	d = p.detail("services.b.db")
	if d.Lock == nil || d.Lock.Kind != "reservation" || d.Lock.Expires == nil || d.Lock.Command != "pravo plan" {
		t.Errorf("reservation %+v", d.Lock)
	}
	if h := p.detail("services.a.file").History; len(h) != 1 {
		t.Errorf("history %+v", h)
	}
}

func TestStateThatCannotBeOpenedIsAnErrorAtStart(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	os.MkdirAll(filepath.Join(dir, "pravo.state"), 0o755)
	os.WriteFile(filepath.Join(dir, "pravo.state", "meta.json"), []byte(`{"format":99,"lineage":"x"}`), 0o644)
	if _, err := New(Options{Dir: dir, Schemas: fakeLookup}); err == nil || !strings.Contains(err.Error(), "newer pravo") {
		t.Errorf("New over a state of a newer format: %v", err)
	}
	// A state path that is a file is an error too, and nothing is made.
	dir = t.TempDir()
	writeSpec(t, dir, specCUE)
	os.WriteFile(filepath.Join(dir, "pravo.state"), []byte("x"), 0o644)
	if _, err := New(Options{Dir: dir, Schemas: fakeLookup}); err == nil {
		t.Errorf("New over a state that is a file")
	}
}

// The history of a node says what the signatures of each transaction come to, as the command
// that started the viewer judged them.
func TestHistoryShowsTheSignatureOfATransaction(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	p := start(t, dir, st, Options{VerifyTxn: func(_ *state.Store, tx *state.Transaction) (string, string) {
		return "verified", "ada <script>"
	}})
	h := p.detail("services.a.db").History
	if len(h) != 1 || h[0].Signature != "verified" || h[0].Signer == "" {
		t.Fatalf("history %+v", h)
	}
	// With nothing to check against the entry says nothing.
	q := newProject(t)
	if h := q.detail("services.a.db").History; len(h) != 1 || h[0].Signature != "" || h[0].Signer != "" {
		t.Errorf("history %+v", h)
	}
}

// An apply that ran from a stored plan names the plan, and the revision of the
// specification it ran against, in the history of the node.
func TestHistoryNamesThePlanAndTheRevision(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	st.SetActor("", "", "0123456789abcdef")
	txn := st.Begin("apply", "pravo apply plan-12345678")
	txn.SetPlan("plan-12345678", "feed")
	r := st.Get("services.a.db")
	next := *r
	if err := st.Set(&next, txn.ID()); err != nil {
		t.Fatal(err)
	}
	txn.Record("services.a.db", "update", r.Revision, next.Revision)
	if err := txn.Finish("applied", "1 updated", true); err != nil {
		t.Fatal(err)
	}
	p := start(t, dir, st, Options{})
	h := p.detail("services.a.db").History
	if len(h) != 2 || h[0].Plan != "plan-12345678" || h[0].GitRevision != "0123456789abcdef" || h[1].Plan != "" {
		t.Fatalf("history %+v", h)
	}
}

// An apply records what it did to the attributes of a record, and the history of the
// node shows it redacted again (the transaction here keeps every value, as one written
// by another program might) as a plan's changes are: a value the schema or its name
// withholds is reported as changed and not shown.
func TestHistoryShowsWhatAChangeAlteredRedacted(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	r := st.Get("services.a.db")
	before := string(r.Attributes)
	after := strings.NewReplacer(`"port":5432`, `"port":5433`, secretBlob, "rotated-blob-value-9999",
		`"owner":"team"`, `"owner":"ops"`, secretMapKey, "rotated-map-secret-9999").Replace(before)
	txn := st.Begin("apply", "pravo apply")
	next := *r
	next.Attributes = json.RawMessage(after)
	if err := st.Set(&next, txn.ID()); err != nil {
		t.Fatal(err)
	}
	txn.Record("services.a.db", "update", r.Revision, next.Revision)
	txn.RecordDiff("services.a.db", json.RawMessage(before), next.Attributes, nil)
	if err := txn.Finish("applied", "1 changed", true); err != nil {
		t.Fatal(err)
	}
	p := start(t, dir, st, Options{})
	h := p.detail("services.a.db").History
	if len(h) != 2 || len(h[1].Attrs) != 0 {
		t.Fatalf("history %+v", h)
	}
	got := map[string]AttrChange{}
	for _, c := range h[0].Attrs {
		got[c.Path] = c
	}
	if c := got["port"]; fmt.Sprint(c.Before) != "5432" || fmt.Sprint(c.After) != "5433" {
		t.Errorf("port %+v", c)
	}
	if c := got["tags.owner"]; c.Before != "team" || c.After != "ops" {
		t.Errorf("tags.owner %+v", c)
	}
	for _, path := range []string{"blob", "tags.db_password"} {
		if c := got[path]; !c.BeforeRedacted || !c.AfterRedacted || c.Before != nil || c.After != nil {
			t.Errorf("%s %+v", path, c)
		}
	}
	if len(got) != 4 {
		t.Errorf("changes %+v", got)
	}
	raw, _ := json.Marshal(h)
	for _, secret := range []string{secretBlob, secretMapKey, "rotated-blob-value-9999", "rotated-map-secret-9999"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the history shows %q", secret)
		}
	}
}

// A read that found an object changed outside pravo is in the history of its node, with
// what differed, though it was a plan that made it.
func TestHistoryShowsADriftThatAPlanFound(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	r := st.Get("services.a.db")
	next := *r
	next.Attributes = json.RawMessage(strings.Replace(string(r.Attributes), `"port":5432`, `"port":6543`, 1))
	txn := st.Begin("plan", "pravo plan")
	if err := st.Refresh(&next, txn.ID()); err != nil {
		t.Fatal(err)
	}
	txn.Record("services.a.db", "drift", r.Revision, next.Revision)
	txn.RecordDiff("services.a.db", r.Attributes, next.Attributes, nil)
	txn.Record("services.b.db", "refresh", 1, 1)
	if err := txn.Finish("planned", "1 drifted", true); err != nil {
		t.Fatal(err)
	}
	p := start(t, dir, st, Options{})
	h := p.detail("services.a.db").History
	if len(h) != 2 || h[0].Kind != "plan" || h[0].Action != "drift" || len(h[0].Attrs) != 1 || h[0].Attrs[0].Path != "port" || fmt.Sprint(h[0].Attrs[0].After) != "6543" {
		t.Fatalf("history %+v", h)
	}
	if h := p.detail("services.b.db").History; len(h) != 1 {
		t.Errorf("a refresh that found nothing is in the history: %+v", h)
	}
}

// The record holds what a provider filled in; the detail says which attributes the specification sets.
func TestDetailSaysWhatTheSpecificationSets(t *testing.T) {
	p := newProject(t)
	d := p.detail("services.a.db")
	if !d.SpecKnown || len(d.SpecSet) == 0 {
		t.Fatalf("spec_known %v, spec_set %v", d.SpecKnown, d.SpecSet)
	}
	have := map[string]bool{}
	for _, n := range d.SpecSet {
		have[n] = true
	}
	if !have["name"] || have["id"] {
		t.Errorf("spec_set %v: want name, not the computed id", d.SpecSet)
	}
}
