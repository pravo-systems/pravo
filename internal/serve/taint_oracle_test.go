package serve

import (
	"fmt"
	"path/filepath"
	"testing"

	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/state"
)

// oracleTaint is what the fields of each declared resource read, worked out one
// field at a time with an analysis of its own. The model gets the same from the walk
// that finds the edges, which costs one analysis and not two; this checks that the
// walk misses nothing that the analysis of a field finds.
func oracleTaint(ss *specState) map[string]map[string]map[taintRef]bool {
	out := map[string]map[string]map[taintRef]bool{}
	res := newSpecResolver(ss.root)
	for _, r := range ss.root.Resources {
		it, err := ss.root.Value.LookupPath(r.Path).Fields()
		if err != nil {
			continue
		}
		for it.Next() {
			sel := it.Selector()
			if sel.LabelType() != cue.StringLabel {
				continue
			}
			rec := &taintRecorder{specResolver: res, fixed: sel.Unquoted()}
			convert.Dependencies(it.Value(), cue.MakePath(append(r.Path.Selectors(), sel)...), rec)
			for field, refs := range rec.reads() {
				if out[r.Address()] == nil {
					out[r.Address()] = map[string]map[taintRef]bool{}
				}
				out[r.Address()][field] = map[taintRef]bool{}
				for _, ref := range refs {
					out[r.Address()][field][ref] = true
				}
			}
		}
	}
	return out
}

const shapesCUE = `
base: $fake.#Resource.fake_db & {name: "base", blob: "b-secret-1", port: 1}
other: $fake.#Resource.fake_db & {name: "other", blob: "b-secret-2", port: 2}

direct: $fake.#Resource.fake_file & {path: "/d", content: base.blob}
interp: $fake.#Resource.fake_file & {path: "/i", content: "x=\(base.name)-\(other.blob)"}
plainBody: $fake.#Resource.fake_file & {path: "/p", content: base.blob}
ownField: $fake.#Resource.fake_db & {name: "o", blob: "b-secret-3", tags: {a: "\(blob)"}}
nested: $fake.#Resource.fake_db & {name: "n", rule: [{cidr: "10.0.0.0/8", pin: other.blob}], settings: {region: "eu", key: base.blob}}
listed: $fake.#Resource.fake_db & {name: "l", zones: [base.name, other.name]}
cond: $fake.#Resource.fake_db & {name: "c", if base.port > 0 {blob: other.blob}}
let1: $fake.#Resource.fake_file & {
	let x = base.blob
	path: "/l"
	content: "v=\(x)"
}
loop: {
	for k, v in {a: base, b: other} {
		"\(k)": $fake.#Resource.fake_file & {path: "/\(k)", content: v.name}
	}
}
whole: $fake.#Resource.fake_db & {name: "w", tags: {x: "\(base.name)"}, zones: [other.name]}
`

func TestTaintOfTheEdgeWalkCoversTheOracle(t *testing.T) {
	dir := t.TempDir()
	writeSpec(t, dir, specCUE+shapesCUE)
	st, err := state.Open(filepath.Join(dir, "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	seed(t, st)
	srv, err := New(Options{Dir: dir, Schemas: fakeLookup, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := srv.current()
	if err != nil {
		t.Fatal(err)
	}
	if m.spec.root == nil {
		t.Fatalf("the specification did not load: %s", m.graph.Project.SpecError)
	}
	want := oracleTaint(m.spec)
	checked := 0
	for addr, fields := range want {
		for field, refs := range fields {
			have := map[taintRef]bool{}
			for _, ref := range m.spec.taint[addr][field] {
				have[ref] = true
			}
			for ref := range refs {
				checked++
				if !have[ref] {
					t.Errorf("%s.%s reads %+v, and the walk of the edges does not see it (has %v)", addr, field, ref, m.spec.taint[addr][field])
				}
			}
		}
	}
	if checked < 15 {
		t.Errorf("only %d reads were compared: %s", checked, fmt.Sprint(want))
	}
}
