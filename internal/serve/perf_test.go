package serve

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An estate of 5000 records: the graph builds in well under a second and is a
// few megabytes at most, since it holds no attribute values.
func TestBigEstate(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 5000 records")
	}
	const n = 5000
	dir := t.TempDir()
	res := filepath.Join(dir, "pravo.state", "resources")
	if err := os.MkdirAll(res, 0o755); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "pravo.state", "txn"), 0o755)
	os.MkdirAll(filepath.Join(dir, "pravo.state", "locks"), 0o755)
	os.WriteFile(filepath.Join(dir, "pravo.state", "meta.json"), []byte(`{"format":1,"lineage":"abc","created":"2026-01-01T00:00:00Z"}`), 0o644)
	for i := 0; i < n; i++ {
		addr := fmt.Sprintf("estate.g%02d.n%03d", i/100, i%100)
		deps := ""
		if i%100 != 0 {
			deps = fmt.Sprintf(`,"dependencies":["estate.g%02d.n%03d"]`, i/100, i%100-1)
		}
		body := fmt.Sprintf(`{"addr":%q,"mode":"managed","type":"fake_db","name":"n","provider":"example.test/x/fake","provider_key":"fake","provider_version":"1.0.0","schema_version":1,`+
			`"attributes":{"name":"n%d","blob":"secret-blob-%d","port":%d,"tags":{"a":"b","c":"d"},"zones":["x","y","z"],"settings":{"region":"eu","key":"k-%d"},"rule":[{"cidr":"10.0.0.0/8","pin":"p-%d"}]},`+
			`"revision":1,"generation":"g","updated_at":"2026-01-01T00:00:00Z","refreshed_at":"2026-01-01T00:00:00Z"%s}`, addr, i, i, i, i, i, deps)
		if err := os.WriteFile(filepath.Join(res, addr+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	began := time.Now()
	srv, err := New(Options{Dir: dir, Schemas: fakeLookup, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	first := time.Since(began)
	srv.buildMu.Lock()
	began = time.Now()
	m, err := srv.build()
	rebuild := time.Since(began)
	srv.buildMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("5000 records: first model %s (state open, schemas, graph); rebuild %s; graph %d bytes, %d nodes, %d edges, %d namespaces",
		first.Round(time.Millisecond), rebuild.Round(time.Millisecond), len(m.graphJSON), len(m.graph.Nodes), len(m.graph.Edges), len(m.graph.Namespaces))
	if len(m.graph.Nodes) != n || len(m.graph.Edges) != n-n/100 || len(m.graph.Namespaces) != 51 {
		t.Errorf("%d nodes, %d edges, %d namespaces", len(m.graph.Nodes), len(m.graph.Edges), len(m.graph.Namespaces))
	}
	if len(m.graphJSON) > 4<<20 {
		t.Errorf("the graph is %d bytes", len(m.graphJSON))
	}
	// The limit catches a model that has become slow by an order of magnitude. A
	// runner of CI, Windows in particular, takes up to twice as long as a laptop.
	limit := 5 * time.Second
	if raceOn {
		limit = 15 * time.Second
	}
	if rebuild > limit {
		t.Errorf("the model of 5000 records took %s, want under %s", rebuild, limit)
	}
	if n0 := m.graph.Nodes[0]; n0.Sensitive == 0 {
		t.Errorf("sensitive count %d", n0.Sensitive)
	}
	// A detail and a source are built per request.
	began = time.Now()
	d := m.detail(m.nodes["estate.g10.n050"])
	if len(d.Attrs) == 0 || len(d.UsedBy) != 1 {
		t.Errorf("detail %+v", d)
	}
	t.Logf("detail of one node: %s", time.Since(began).Round(time.Microsecond))
	// The field index is built on the first search and reused after it.
	began = time.Now()
	f := m.fieldMatches("settings.region=eu")
	build := time.Since(began)
	began = time.Now()
	g := m.fieldMatches("settings.region=eu")
	query := time.Since(began)
	began = time.Now()
	m.fieldMatches("*.pin=p-*")
	wild := time.Since(began)
	t.Logf("field index of %d records: first search %s, next %s, a wildcard on paths %s", n, build.Round(time.Millisecond), query.Round(time.Millisecond), wild.Round(time.Millisecond))
	if len(f.Addrs) != n || len(g.Addrs) != n || f.Indexed != n {
		t.Errorf("settings.region=eu found %d of %d, indexed %d", len(f.Addrs), n, f.Indexed)
	}
	if got := m.fieldMatches("rule.0.pin=p-1"); len(got.Addrs) != 0 {
		t.Errorf("a sensitive value matched %v", got.Addrs)
	}
	if build > limit/2 || query > limit/50 {
		t.Errorf("field search of 5000 records: index %s, query %s", build, query)
	}
}
