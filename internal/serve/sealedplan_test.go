package serve

import (
	"context"
	"encoding/json"
	"path/filepath"
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

// The plan view of a stored plan says who signed it, and why that could not be checked when
// the plan cannot be opened. A viewer with nothing to check against says nothing.
func TestThePlanViewSaysWhoSignedAStoredPlan(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	var asked []string
	p := start(t, dir, st, Options{VerifyPlan: func(_ context.Context, _ *state.Store, id, digest string) (*PlanSignatures, bool) {
		asked = append(asked, id+" "+digest)
		return &PlanSignatures{Items: []PlanSignature{
			{Status: "verified", Signer: "ada"},
			{Status: "key_not_in_file", Claimed: "dan <script>alert(1)</script>"},
			{Status: "invalid"},
		}}, true
	}})
	id := storePlan(t, p, stored{Author: "ana@acme", Addrs: []string{"services.a.db"}})
	waitFor(t, "the stored plan", func() bool { return len(p.plans().Plans) == 1 })
	o := p.overlay(id)
	sg := o.Plan.Signatures
	if sg == nil || len(sg.Items) != 3 || sg.Items[0].Status != "verified" || sg.Items[0].Signer != "ada" || sg.Items[2].Status != "invalid" {
		t.Fatalf("signatures %+v", sg)
	}
	if len(asked) == 0 || !strings.HasPrefix(asked[0], id+" ") || len(asked[0]) < len(id)+60 {
		t.Errorf("the check was given %q, want the id and the digest of the plan file", asked)
	}
	// A plan this process cannot open is not checked, and the view says why.
	sealedID := storePlan(t, p, stored{Author: "ana@acme", Addrs: []string{"services.b.file"}, Sealed: true})
	waitFor(t, "the sealed plan", func() bool { return len(p.plans().Plans) == 2 })
	if sg := p.overlay(sealedID).Plan.Signatures; sg == nil || len(sg.Items) != 0 || !strings.Contains(sg.Note, "cannot open") {
		t.Errorf("a sealed plan: %+v", sg)
	}
	// Nothing to check against: no signatures in the view.
	q := newProject(t)
	qid := storePlan(t, q, stored{Author: "ana@acme", Addrs: []string{"services.a.db"}})
	waitFor(t, "the stored plan", func() bool { return len(q.plans().Plans) == 1 })
	if q.overlay(qid).Plan.Signatures != nil {
		t.Errorf("a viewer with nothing to check says %+v", q.overlay(qid).Plan.Signatures)
	}
}
