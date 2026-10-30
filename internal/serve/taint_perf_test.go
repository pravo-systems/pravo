package serve

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// An estate whose resources read each other: what working out which fields are
// derived costs next to what the specification costs to load and to link.
func TestTaintCostOnAChainedEstate(t *testing.T) {
	if testing.Short() {
		t.Skip("evaluates a specification of 2000 resources")
	}
	const n = 2000
	var b strings.Builder
	b.WriteString(strings.SplitN(specCUE, "// A reusable unit.", 2)[0])
	b.WriteString("\nestate: {\n")
	for i := range n {
		var prev string
		if i > 0 {
			prev = fmt.Sprintf(`, port: estate.n%d.port, tags: {up: estate.n%d.tags.up}`, i-1, i-1)
		} else {
			// the head of a chain of 2000 fields that all derive from one sensitive attribute
			prev = `, tags: {up: estate.n0.blob}`
		}
		fmt.Fprintf(&b, "\tn%d: $fake.#Resource.fake_db & {name: \"n%d\", blob: \"b%d-secret\", zones: [\"a\", \"b\"]%s}\n", i, i, i, prev)
	}
	b.WriteString("}\n")
	dir := t.TempDir()
	writeSpec(t, dir, b.String())
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		deps := []string{}
		if i > 0 {
			deps = append(deps, fmt.Sprintf("estate.n%d", i-1))
		}
		if err := st.Set(record(fmt.Sprintf("estate.n%d", i), "fake_db", fmt.Sprintf(`{"name":"n%d","blob":"b%d-secret","port":%d}`, i, i, i), deps...), ""); err != nil {
			t.Fatal(err)
		}
	}
	began := time.Now()
	srv, err := New(Options{Dir: dir, Schemas: fakeLookup, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	first := time.Since(began)
	m, err := srv.current()
	if err != nil {
		t.Fatal(err)
	}
	began = time.Now()
	m.spec.computeEdges()
	edges := time.Since(began)
	began = time.Now()
	m.spec.computeTaint()
	taint := time.Since(began)
	began = time.Now()
	m.computeTaint()
	fix := time.Since(began)
	began = time.Now()
	m.fields()
	idx := time.Since(began)
	began = time.Now()
	for i := range 200 {
		m.detail(m.nodes[fmt.Sprintf("estate.n%d", i)])
	}
	det := time.Since(began) / 200
	t.Logf("%d chained resources: first model %s; edges %s; references per field %s; derived fields %s; field index %s; one detail %s; %d nodes have a derived field",
		n, first.Round(time.Millisecond), edges.Round(time.Millisecond), taint.Round(time.Millisecond), fix.Round(time.Millisecond), idx.Round(time.Millisecond), det.Round(time.Microsecond), len(m.derived))
}
