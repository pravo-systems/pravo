package serve

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/state"
)

// outputsCUE adds to the standard specification the outputs the tests below
// look at: one that reads two resources and another output, one that reads an
// output, a sensitive one, a sensitive literal, and one in a struct of its own.
const outputsCUE = specCUE + `
summary: {
	$kind:       "output"
	description: "What runs"
	value: {db: services.a.db.name, file: services.a.file.path, via: host.value}
}
chain: {$kind: "output", value: summary.value.db}
secretOut: {$kind: "output", value: services.b.db.blob, sensitive: true, description: "The blob"}
pin: {$kind: "output", value: "pin-literal-9876543", sensitive: true}
report: a: {$kind: "output", value: services.b.db.name}
`

const secretPinLiteral = "pin-literal-9876543"

// newOutputsProject is a project with the outputs above: the recorded outputs are
// host, leak, summary, a sensitive secretOut, and two that the specification does
// not declare, one plain (retired) and one sensitive (safe).
func newOutputsProject(t *testing.T) *project {
	t.Helper()
	dir := t.TempDir()
	writeSpec(t, dir, outputsCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	outs := map[string]json.RawMessage{
		"host":      json.RawMessage(`"db.example"`),
		"leak":      json.RawMessage(`"token=` + secretBlob + `"`),
		"safe":      json.RawMessage(`{"$sensitive":"` + secretBlob + `"}`),
		"summary":   json.RawMessage(`{"db":"a","file":"/tmp/a","via":"db.example","password":"p-1234567"}`),
		"secretOut": json.RawMessage(`{"$sensitive":"` + secretBlob + `"}`),
		"retired":   json.RawMessage(`"old-value"`),
	}
	if err := st.SetOutputs(outs); err != nil {
		t.Fatal(err)
	}
	return start(t, dir, st, Options{})
}

func edgeSet(g Graph) map[string]bool {
	out := map[string]bool{}
	for _, e := range g.Edges {
		out[e.Dependency+" -> "+e.Dependent] = true
	}
	return out
}

func TestOutputsAreNodes(t *testing.T) {
	p := newOutputsProject(t)
	g := p.graph()
	for addr, c := range map[string]struct {
		presence, name, module string
		sensitive              int
	}{
		"host":      {"both", "host", "", 0},
		"summary":   {"both", "summary", "", 0},
		"leak":      {"both", "leak", "", 1}, // its value holds a sensitive value of the state
		"secretOut": {"both", "secretOut", "", 1},
		"chain":     {"spec", "chain", "", 0},
		"pin":       {"spec", "pin", "", 1},
		"report.a":  {"spec", "a", "report", 0},
		"safe":      {"state", "safe", "", 1},
		"retired":   {"state", "retired", "", 0},
	} {
		n := nodeOf(g, addr)
		if n == nil {
			t.Errorf("no node %s", addr)
			continue
		}
		if n.Mode != "output" || n.Type != "output" || n.Name != c.name || n.Module != c.module || n.Presence != c.presence || n.Sensitive != c.sensitive {
			t.Errorf("%s is %+v, want %+v", addr, *n, c)
		}
		if n.Provider != "" || n.Revision != 0 || n.RefreshedAt != nil || n.Locked || n.Drifted || n.Gone || n.Action != "" {
			t.Errorf("%s carries what only a record has: %+v", addr, *n)
		}
		if (c.presence == "state") != (n.Source == nil) {
			t.Errorf("%s: source %+v for presence %s", addr, n.Source, c.presence)
		}
	}
	if n := nodeOf(g, "summary"); n.Source == nil || n.Source.File != "main.cue" || n.Source.EndLine <= n.Source.Line {
		t.Errorf("summary is written at %+v", n.Source)
	}
	// The graph still lists the outputs, as it did.
	if len(g.Outputs) != 9 {
		t.Errorf("%d outputs in the graph, want 9", len(g.Outputs))
	}
}

func TestOutputsAreNotResources(t *testing.T) {
	p := newOutputsProject(t)
	g := p.graph()
	// A module that holds outputs only counts none as a resource, and is known to hold one.
	if m := moduleOf(g, "report"); m == nil || m.Resources != 0 || m.Outputs != 1 {
		t.Errorf("module report is %+v, want no resources and one output", m)
	}
	for path, want := range map[string]int{"services": 4, "services.a": 2, "services.b": 2, "hosts": 2, "old": 1} {
		if m := moduleOf(g, path); m == nil || m.Resources != want || m.Outputs != 0 {
			t.Errorf("module %s is %+v, want %d resources and no outputs", path, m, want)
		}
	}
	if n := len(resourceNodes(g)); n != 8 {
		t.Errorf("%d resources, want 8", n)
	}
	// The providers count the nodes that use an instance: an output uses none.
	count := 0
	for _, pi := range g.Project.Providers {
		for _, in := range pi.Instances {
			count += in.Count
		}
	}
	if count != 8 {
		t.Errorf("the provider instances count %d nodes, want 8", count)
	}
	if g.Project.State.Records != 5 {
		t.Errorf("%d records, want 5", g.Project.State.Records)
	}
}

func TestOutputEdges(t *testing.T) {
	p := newOutputsProject(t)
	g := p.graph()
	edges := edgeSet(g)
	for _, e := range []string{
		"services.a.db -> summary", "services.a.file -> summary", "host -> summary", // an output that reads two resources and another output
		"summary -> chain",           // an output that reads an output is joined to it
		"services.b.db -> secretOut", // through a field of the resource
		"services.b.db -> report.a",
	} {
		if !edges[e] {
			t.Errorf("no edge %s", e)
		}
	}
	for _, e := range []string{"services.a.db -> chain", "services.a.file -> chain", "chain -> summary", "summary -> summary"} {
		if edges[e] {
			t.Errorf("edge %s: an output is joined to what it reads and not to what that reads", e)
		}
	}
	// No resource reads an output, and an output that is only recorded reads nothing.
	for _, e := range g.Edges {
		if nodeOf(g, e.Dependency).Mode == "output" && nodeOf(g, e.Dependent).Mode != "output" {
			t.Errorf("edge %s -> %s: a resource reads an output", e.Dependency, e.Dependent)
		}
		if e.Dependent == "retired" || e.Dependent == "safe" {
			t.Errorf("edge %s -> %s for an output the specification does not declare", e.Dependency, e.Dependent)
		}
	}
	d := p.detail("summary")
	if strings.Join(d.DependsOn, ",") != "host,services.a.db,services.a.file" || strings.Join(d.UsedBy, ",") != "chain" {
		t.Errorf("summary depends on %v and is used by %v", d.DependsOn, d.UsedBy)
	}
	if d := p.detail("services.a.db"); !contains(d.UsedBy, "summary") {
		t.Errorf("services.a.db is used by %v, want summary among them", d.UsedBy)
	}
}

func TestOutputDetail(t *testing.T) {
	p := newOutputsProject(t)
	d := p.detail("summary")
	if d.Node.Mode != "output" || !d.SchemaKnown || d.History != nil || d.Lock != nil || d.Times != (Times{}) || d.Change != nil || len(d.Reach) != 0 {
		t.Errorf("detail %+v", d)
	}
	if len(d.Attrs) != 2 || d.Attrs[0].Name != "value" || d.Attrs[0].Type != "output" || d.Attrs[1].Name != "description" || d.Attrs[1].Value != "What runs" {
		t.Fatalf("attrs %+v", d.Attrs)
	}
	// A map is shown by its entries, and an entry whose name says secret is withheld.
	got := map[string]Attr{}
	for _, c := range d.Attrs[0].Children {
		got[c.Name] = c
	}
	if got["db"].Value != "a" || got["via"].Value != "db.example" || got["password"].Redacted != "name" || got["password"].Value != nil {
		t.Errorf("children of the value: %+v", d.Attrs[0].Children)
	}

	// A plain output shows its recorded value.
	if d := p.detail("host"); len(d.Attrs) != 1 || d.Attrs[0].Value != "db.example" || d.Attrs[0].Redacted != "" {
		t.Errorf("host: %+v", d.Attrs)
	}
	// An output that is declared and has no value yet does not know it.
	if d := p.detail("chain"); len(d.Attrs) != 1 || !d.Attrs[0].Unknown || d.Attrs[0].Value != nil {
		t.Errorf("chain: %+v", d.Attrs)
	}
	// One that the specification no longer declares shows the value that is recorded, and says what an apply does.
	if d := p.detail("retired"); len(d.Attrs) != 1 || d.Attrs[0].Value != "old-value" || len(d.Notes) != 1 || !strings.Contains(d.Notes[0], "would remove") {
		t.Errorf("retired: %+v notes %v", d.Attrs, d.Notes)
	}
	// The value is withheld when the output is marked sensitive, whether the specification or the record says it,
	// and when it holds a sensitive value of the state.
	for addr, why := range map[string]string{"secretOut": "sensitive", "safe": "sensitive", "leak": "derived", "pin": "sensitive"} {
		d := p.detail(addr)
		if d.Attrs[0].Redacted != why || !d.Attrs[0].Sensitive || d.Attrs[0].Value != nil || len(d.Attrs[0].Children) != 0 || d.Attrs[0].Unknown {
			t.Errorf("%s: value %+v, want withheld as %s", addr, d.Attrs[0], why)
		}
	}
	if d := p.detail("secretOut"); len(d.Attrs) != 2 || d.Attrs[1].Value != "The blob" {
		t.Errorf("secretOut keeps its description: %+v", d.Attrs)
	}
}

func TestOutputsSendNoSensitiveValue(t *testing.T) {
	p := newOutputsProject(t)
	secrets := append([]string{secretPinLiteral}, allSecrets...)
	noSecrets(t, "graph", p.get("/api/graph"), secrets...)
	for _, addr := range []string{"summary", "host", "chain", "secretOut", "pin", "leak", "safe", "retired", "report.a"} {
		q := queryEscape(addr)
		noSecrets(t, "detail of "+addr, p.get("/api/detail?addr="+q), secrets...)
		noSecrets(t, "source of "+addr, p.get("/api/source?addr="+q), secrets...)
	}
	noSecrets(t, "search", p.get("/api/search?q=out"), secrets...)
	noSecrets(t, "plans", p.get("/api/plans"), secrets...)
	// The recorded value of the sensitive output is not in the answer of any output, nor in the list.
	for _, o := range p.graph().Outputs {
		if (o.Name == "secretOut" || o.Name == "safe" || o.Name == "pin" || o.Name == "leak") && (o.Value != nil || !o.Sensitive) {
			t.Errorf("output %s: %+v", o.Name, o)
		}
	}
}

func TestOutputSource(t *testing.T) {
	p := newOutputsProject(t)
	s := p.source("summary")
	if len(s.Blocks) != 1 || s.Blocks[0].Role != "definition" || s.Blocks[0].File != "main.cue" || s.Blocks[0].Label != "summary" {
		t.Fatalf("blocks %+v", s.Blocks)
	}
	b := s.Blocks[0]
	var text []string
	for _, l := range b.Lines {
		if l.Mark == "focus" {
			text = append(text, l.Text)
		}
	}
	joined := strings.Join(text, "\n")
	if !strings.Contains(joined, "summary: {") || !strings.Contains(joined, "services.a.db.name") || !strings.Contains(joined, `description: "What runs"`) {
		t.Errorf("the lines of the declaration are %q", joined)
	}
	if len(s.Contributions) < 2 || s.Contributions[0].Kind != "file" || !strings.Contains(s.Contributions[0].Note, "writes the output") {
		t.Errorf("contributions %+v", s.Contributions)
	}
	last := s.Contributions[len(s.Contributions)-1]
	if last.Kind != "builtin" || !strings.Contains(last.Note, "pravo.#Output") {
		t.Errorf("last contribution %+v", last)
	}

	// An output in a struct: the lines are those of the field.
	if s := p.source("report.a"); len(s.Blocks) != 1 || s.Blocks[0].Label != "report.a" {
		t.Errorf("report.a: %+v", s.Blocks)
	}
	// A literal that a sensitive output is given is masked, and its line is marked.
	pin := p.source("pin")
	if len(pin.Blocks) != 1 {
		t.Fatalf("pin: %+v", pin.Blocks)
	}
	masked := false
	for _, l := range pin.Blocks[0].Lines {
		if strings.Contains(l.Text, secretPinLiteral) {
			t.Errorf("the literal of a sensitive output is shown: %q", l.Text)
		}
		masked = masked || l.Mark == "sensitive"
	}
	if !masked {
		t.Errorf("no line of the sensitive literal is marked: %+v", pin.Blocks[0].Lines)
	}
	// An output that is only recorded has no lines.
	if s := p.source("retired"); len(s.Blocks) != 0 || len(s.Notes) == 0 {
		t.Errorf("retired: %+v", s)
	}
}

func TestOutputsAreFound(t *testing.T) {
	p := newOutputsProject(t)
	var res Search
	if err := json.Unmarshal(p.get("/api/search?q=summary"), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].Addr != "summary" || res.Nodes[0].Mode != "output" {
		t.Errorf("search for summary: %+v", res.Nodes)
	}
	// The module that holds only outputs is found like any.
	if err := json.Unmarshal(p.get("/api/search?q=report"), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Modules) != 1 || res.Modules[0].Path != "report" || len(res.Nodes) != 1 || res.Nodes[0].Addr != "report.a" {
		t.Errorf("search for report: %+v %+v", res.Modules, res.Nodes)
	}
}

func TestOutputsFromTheStateAlone(t *testing.T) {
	p := newOutputsProject(t)
	writeSpec(t, p.dir, "package demo\n\nbroken: {\n")
	waitFor(t, "the specification error", func() bool { return p.graph().Project.SpecError != "" })
	g := p.graph()
	// Every recorded output is shown, as an orphan is, with no edges and nothing to read.
	n := 0
	for _, node := range g.Nodes {
		if node.Mode != "output" {
			continue
		}
		n++
		if node.Presence != "state" || node.Source != nil {
			t.Errorf("%s: presence %s source %+v", node.Addr, node.Presence, node.Source)
		}
	}
	if n != 6 {
		t.Errorf("%d outputs from the state alone, want the 6 that are recorded", n)
	}
	if d := p.detail("host"); d.Attrs[0].Value != "db.example" || !strings.Contains(strings.Join(d.Notes, " "), "does not load") {
		t.Errorf("host: %+v %v", d.Attrs, d.Notes)
	}
	if s := p.source("host"); len(s.Blocks) != 0 || len(s.Notes) == 0 {
		t.Errorf("source: %+v", s)
	}
}

// A record in the state keeps its address when an output is declared there, and
// when an output of that name is recorded: the page keys its nodes by address.
func TestOutputWithTheAddressOfARecord(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE+"\nold: thing: {$kind: \"output\", value: 1}\n")
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st) // the record old.thing is in the state, and the specification does not declare a resource there
	if err := st.SetOutputs(map[string]json.RawMessage{"shared": json.RawMessage(`"x"`)}); err != nil {
		t.Fatal(err)
	}
	p := start(t, dir, st, Options{})
	g := p.graph()
	for _, addr := range []string{"old.thing", "shared"} {
		if n := nodeOf(g, addr); n == nil || n.Mode != "managed" {
			t.Errorf("%s is %+v, want the record", addr, n)
		}
	}
	found := false
	for _, w := range g.Warnings {
		found = found || strings.Contains(w, "The output old.thing has the address of a record")
	}
	if !found {
		t.Errorf("warnings %v", g.Warnings)
	}
}
