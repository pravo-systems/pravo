package serve

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// otherKey seals what it is given under a key that nothing else holds.
type otherKey struct{}

func (otherKey) Seal(kind, name string, plain []byte) (protect.Sealed, error) {
	return protect.Sealed{Alg: "test", Group: "g", CT: append([]byte(nil), plain...)}, nil
}

func (otherKey) Open(kind, name string, s protect.Sealed) ([]byte, error) {
	return nil, state.ErrNotReader
}

// A stored plan that the viewer cannot open still lays its actions over the
// graph, from the summary, and says that its values are sealed: no error, in the
// overlay, the detail of a node or the source.
func TestSealedStoredPlanKeepsItsSummary(t *testing.T) {
	p := newProject(t)
	id := storePlan(t, p, stored{Author: "ana@acme", Addrs: []string{"services.a.db", "services.b.file"}, Sealed: true})
	waitFor(t, "the stored plan", func() bool { return len(p.plans().Plans) == 1 })
	o := p.overlay(id)
	if len(o.Warnings) != 1 || o.Warnings[0] != "The values of this plan are sealed." {
		t.Fatalf("warnings %q", o.Warnings)
	}
	if o.Plan.ID != id || o.Plan.Planned != 2 || len(o.Nodes) != 2 || o.Nodes[0].Action == "" || o.Nodes[1].Action == "" {
		t.Fatalf("overlay %+v nodes %+v", o.Plan, o.Nodes)
	}
	if sum := o.Plan.Summary; sum.Add+sum.Change+sum.Destroy < 2 {
		t.Errorf("the counts come from the actions: %+v", o.Plan.Summary)
	}
	var d Detail
	if err := json.Unmarshal(p.get("/api/detail?addr=services.a.db&plan="+queryEscape(id)), &d); err != nil {
		t.Fatal(err)
	}
	if d.Change == nil || d.Change.Action != "update" || len(d.Change.Attrs) != 0 {
		t.Errorf("detail change %+v", d.Change)
	}
	if !strings.Contains(strings.Join(d.Notes, " "), "not shown") {
		t.Errorf("detail notes %v", d.Notes)
	}
	p.get("/api/source?addr=services.a.db&plan=" + queryEscape(id))
}

// A viewer started with --plan ID on a plan that it cannot open lays the summary
// over its own graph, and says so in the warnings, and does not fail.
func TestSealedStoredPlanOfTheGraph(t *testing.T) {
	p := newProject(t)
	id := storePlan(t, p, stored{Author: "ana@acme", Addrs: []string{"services.a.db"}, Sealed: true})
	q := start(t, p.dir, p.store, Options{PlanID: id})
	g := q.graph()
	if g.Plan == nil || g.Plan.ID != id || g.Plan.Planned != 1 {
		t.Fatalf("the plan of the graph: %+v", g.Plan)
	}
	var sealed, failed bool
	for _, w := range g.Warnings {
		sealed = sealed || w == "The values of this plan are sealed."
		failed = failed || strings.Contains(w, "could not be read")
	}
	if !sealed || failed {
		t.Errorf("warnings %q", g.Warnings)
	}
	if n := nodeOf(g, "services.a.db"); n == nil || n.Action != "update" {
		t.Errorf("node %+v", n)
	}
}
