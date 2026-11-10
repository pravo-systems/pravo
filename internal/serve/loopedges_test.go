package serve

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/engine"
)

// The viewer draws the edges that the engine's graph has: a resource that a
// loop over a collection of resources makes reads its own element, and with
// PRAVO_DEPENDENCIES=syntax the whole collection.
func TestALoopOverACollectionIsDrawnByElement(t *testing.T) {
	var b strings.Builder
	b.WriteString(strings.SplitN(specCUE, "// A reusable unit.", 2)[0])
	b.WriteString("\nsrc: {\n")
	for i := range 4 {
		fmt.Fprintf(&b, "\tk%d: $fake.#Resource.fake_db & {name: \"s%d\"}\n", i, i)
	}
	b.WriteString("}\ndst: {for k, v in src {\"\\(k)\": $fake.#Resource.fake_db & {name: \"d-\\(k)\", port: v.port}}}\n")
	edges := func() []string {
		dir := t.TempDir()
		writeSpec(t, dir, b.String())
		srv, err := New(Options{Dir: dir, Schemas: fakeLookup, Version: "test"})
		if err != nil {
			t.Fatal(err)
		}
		m, err := srv.current()
		if err != nil {
			t.Fatal(err)
		}
		if m.spec == nil || m.spec.root == nil {
			t.Fatal("the specification did not load")
		}
		return m.spec.edges["dst.k2"]
	}
	if got := strings.Join(edges(), ","); got != "src.k2" {
		t.Errorf("dst.k2 reads %q, want src.k2", got)
	}
	engine.DependenciesFromSyntax = true
	defer func() { engine.DependenciesFromSyntax = false }()
	if got := strings.Join(edges(), ","); got != "src.k0,src.k1,src.k2,src.k3" {
		t.Errorf("from the syntax alone dst.k2 reads %q, want the four resources of src", got)
	}
}
