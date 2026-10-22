package serve

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures of the interface (web/dev/fixtures) are what the page is built
// and tested against. They must say what the server says: every field a fixture
// has is a field of the types of api.go, which win when the two differ.
func TestInterfaceFixturesMatchTheContract(t *testing.T) {
	dir := filepath.Join("web", "dev", "fixtures")
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Skipf("no fixtures in %s", dir)
	}
	for _, f := range files {
		name := filepath.Base(f)
		var into any
		switch kind, _, _ := strings.Cut(name, "."); kind {
		case "graph":
			into = &Graph{}
		case "detail":
			into = &Detail{}
		case "source":
			into = &Source{}
		case "search":
			into = &Search{}
		case "event":
			into = &Event{}
		case "plans":
			into = &PlanList{}
		case "plan":
			into = &PlanOverlay{}
		default:
			t.Errorf("%s: a fixture is named graph.*, detail.*, source.*, search.*, event.*, plans.* or plan.*", name)
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(into); err != nil {
			t.Errorf("%s does not match the types of internal/serve/api.go (the Go types win; change the fixture, or ask for the field to be added there): %v", name, err)
		}
	}
}

// The stored plans of the platform fixture, its graph and the overlays say the
// same things: the plans on a node are the open plans that change it, two of
// them are an overlap, and each plan has an overlay whose nodes are its touches.
func TestPlanFixturesAgree(t *testing.T) {
	dir := filepath.Join("web", "dev", "fixtures")
	var g Graph
	var l PlanList
	for file, into := range map[string]any{"graph.platform.json": &g, "plans.platform.json": &l} {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Skipf("no %s: %v", file, err)
		}
		if err := json.Unmarshal(data, into); err != nil {
			t.Fatal(err)
		}
	}
	if l.Revision != g.Revision || len(l.Plans) < 4 {
		t.Fatalf("the list has revision %s and %d plans, the graph %s", l.Revision, len(l.Plans), g.Revision)
	}
	statuses := map[string]bool{}
	for _, p := range l.Plans {
		statuses[p.Status] = true
		want := p.Status == "pending" || p.Status == "partial"
		if p.Open != want || p.Apply != "pravo apply "+p.ID {
			t.Errorf("%s: open %v, apply %q", p.ID, p.Open, p.Apply)
		}
		var o PlanOverlay
		data, err := os.ReadFile(filepath.Join(dir, "plan."+strings.TrimPrefix(p.ID, "plan-")+".json"))
		if err != nil {
			t.Errorf("%s has no overlay: %v", p.ID, err)
			continue
		}
		if err := json.Unmarshal(data, &o); err != nil {
			t.Fatal(err)
		}
		if o.Plan.ID != p.ID || o.Plan.Status != p.Status || len(o.Nodes) != len(p.Touches) {
			t.Errorf("%s: overlay %+v with %d nodes for %d touches", p.ID, o.Plan, len(o.Nodes), len(p.Touches))
		}
	}
	for _, s := range []string{"pending", "partial", "stale", "applied", "expired"} {
		if !statuses[s] {
			t.Errorf("no %s plan among the fixtures", s)
		}
	}
	overlap := false
	for _, n := range g.Nodes {
		var want []string
		for _, p := range l.Plans {
			for _, c := range p.Touches {
				if p.Open && c.Addr == n.Addr && !c.Applied {
					want = append(want, p.ID)
				}
			}
		}
		if strings.Join(n.Plans, " ") != strings.Join(want, " ") || n.Overlap != (len(want) > 1) {
			t.Errorf("%s: plans %v overlap %v, the list says %v", n.Addr, n.Plans, n.Overlap, want)
		}
		overlap = overlap || n.Overlap
	}
	if !overlap {
		t.Error("no node of the fixture is changed by two open plans")
	}
}
