package serve

import (
	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/sensitivity"
)

// Sensitivity follows references. A field of a resource that reads an attribute
// that is withheld, or a field that is itself derived from one, is derived too,
// and it is withheld by the field it reads and not by what its text spells.
//
// The specification says what each field reads: taintRefs records, for the
// fields of a declared resource and the value of an output, the attributes of the
// other tasks that the expression reads. The model then works out, from the
// attributes that are withheld by their schema, their name or their type, which
// fields are derived (model.computeTaint).

// taintRef is an attribute that a field reads (sensitivity.Ref).
type taintRef = sensitivity.Ref

// taintOf runs the analysis that says where a value came from and returns what
// each field of v reads (sensitivity.Record); fixed, when set, is the one field
// that everything is noted under.
func taintOf(v cue.Value, self cue.Path, res *specResolver, fixed string) map[string][]taintRef {
	return sensitivity.Record(v, self, res, res.addrOf, fixed)
}

// recordTaint keeps what the fields of a declared resource, or the value of an
// output (field "value"), read of the tasks (computeEdges).
func (ss *specState) recordTaint(addr string, refs map[string][]taintRef) {
	if len(refs) > 0 {
		if ss.taint == nil {
			ss.taint = map[string]map[string][]taintRef{}
		}
		ss.taint[addr] = refs
	}
}

// topSensitive lists the attributes and blocks of a type that are withheld by what they are.
func topSensitive(b *schema.Block) map[string]bool { return sensitivity.TopSensitive(b) }

// withMarked adds to the fields that the schema withholds the ones that hold a withheld value only in the record: a key
// of a map that says it is a secret (tags.db_password), which the schema, that does not know the keys, cannot name. A
// reference into such a field reads it whole, as the grain is the top-level field. t is shared and is not changed.
func withMarked(t, marked map[string]bool) map[string]bool {
	var out map[string]bool
	for f := range marked {
		if t[f] {
			continue
		}
		if out == nil {
			out = make(map[string]bool, len(t)+len(marked))
			for k := range t {
				out[k] = true
			}
		}
		out[f] = true
	}
	if out == nil {
		return t
	}
	return out
}

// computeTaint works out which fields of the declared nodes are derived from a
// withheld attribute, by following what each field reads until nothing changes.
// A node that the specification does not declare has no references to follow, and
// is judged by text (judge).
//
// A data source reads from outside the graph, so a field of it can hold a string
// that no reference leads to; the fields that do (echoes) are found by the walk
// that renders them, and they are withheld like a sensitive attribute for whatever
// reads them.
func (m *model) computeTaint() {
	m.derived = map[string]map[string]bool{}
	m.echoes = map[string]map[string]bool{}
	m.because = map[string]map[string]string{}
	if m.spec.root == nil || len(m.spec.taint) == 0 {
		return
	}
	m.findEchoes()
	type tops struct {
		set map[string]bool
		all bool
	}
	bySchema := map[*schema.Block]map[string]bool{}
	base := map[string]tops{}
	sensOut := m.sensitiveOutputs()
	for _, addr := range m.order {
		mn := m.nodes[addr]
		switch {
		case mn.isOutput():
			if sensOut[addr] {
				base[addr] = tops{set: map[string]bool{"value": true}}
			}
		case mn.res != nil && mn.res.Builtin && (mn.res.Type == "secret" || mn.res.Type == "secret_env"):
			base[addr] = tops{all: true}
		case mn.sch != nil && mn.sch.Block != nil:
			t, ok := bySchema[mn.sch.Block]
			if !ok {
				t = topSensitive(mn.sch.Block)
				bySchema[mn.sch.Block] = t
			}
			if t = withMarked(t, mn.marked); len(t) > 0 {
				base[addr] = tops{set: t}
			}
		case mn.rec != nil && !transient(mn.Mode) && !mn.Sealed && mn.sch == nil:
			// Nothing says which attributes are sensitive: all of them are withheld.
			base[addr] = tops{all: true}
		}
	}
	m.echoTextless()
	sensitive := func(task, attr string) bool {
		if b, ok := base[task]; ok {
			if b.all || (attr != "" && b.set[attr]) || (attr == "" && len(b.set) > 0) {
				return true
			}
		}
		d, e := m.derived[task], m.echoes[task]
		if mn := m.nodes[task]; mn != nil && transient(mn.Mode) && len(d) > 0 {
			return true // a call computes what it gives from what it is given, and nothing says which of it from which
		}
		if attr == "" {
			return len(d) > 0 || len(e) > 0
		}
		return d[attr] || e[attr]
	}
	seed := sensitivity.Derived{Fields: m.derived, Because: m.because}
	sensitivity.Derive(m.order, m.spec.taint, seed, func(task, attr string, _ sensitivity.Derived) bool { return sensitive(task, attr) })
}

// findEchoes lists, for each data source that the specification declares, the
// top-level fields of its record that hold a string of what it depends on or a
// string that a provider made. The walk is the one that renders the record, with
// the text the judge gives it, so that a field is an echo exactly when the page
// withholds some value inside it.
func (m *model) findEchoes() {
	for _, addr := range m.order {
		mn := m.nodes[addr]
		if mn.Mode != "data" || mn.res == nil || mn.rec == nil || mn.sch == nil || mn.Sealed || mn.undecoded {
			continue
		}
		attrs, err := decodeAttrs(mn.rec.Attributes)
		if err != nil {
			continue
		}
		w := &walk{secrets: m.echoText(addr), hits: map[string]bool{}}
		w.block(mn.sch.Block, attrs, 0)
		if len(w.hits) > 0 {
			m.echoes[addr] = w.hits
		}
	}
}

// textless says that the node holds a secret in no record: a pravo.#Secret, a
// pravo.#SecretEnv or a managed resource with a write-only attribute. Nothing
// of it can be looked for as text.
func (m *model) textless(mn *mnode) bool {
	if mn.res != nil && mn.res.Builtin && (mn.res.Type == "secret" || mn.res.Type == "secret_env") {
		return true
	}
	return mn.Mode != "data" && mn.sch != nil && blockWriteOnly(mn.sch.Block, 0)
}

func blockWriteOnly(b *schema.Block, depth int) bool {
	if b == nil || depth > maxDepth {
		return false
	}
	for _, a := range b.Attributes {
		if a.WriteOnly || objectWriteOnly(a.NestedType, depth+1) {
			return true
		}
	}
	for _, nb := range b.BlockTypes {
		if blockWriteOnly(nb.Block, depth+1) {
			return true
		}
	}
	return false
}

func objectWriteOnly(o *schema.Object, depth int) bool {
	if o == nil || depth > maxDepth {
		return false
	}
	for _, a := range o.Attributes {
		if a.WriteOnly || objectWriteOnly(a.NestedType, depth+1) {
			return true
		}
	}
	return false
}

// echoTextless withholds every field that a data source reads from outside the
// graph (a computed one), when a node whose secret has no text is among what it
// depends on: the data source may read that secret back from a file, and there is
// no string to find it by. The field is derived, so what reads it is withheld too.
func (m *model) echoTextless() {
	var held map[string]bool
	for _, addr := range m.order {
		mn := m.nodes[addr]
		if mn.Mode != "data" || mn.res == nil || mn.rec == nil || mn.sch == nil || mn.Sealed || mn.undecoded {
			continue
		}
		if held == nil {
			held = map[string]bool{}
			for _, a := range m.order {
				if n := m.nodes[a]; n != nil && m.textless(n) {
					held[a] = true
				}
			}
			if len(held) == 0 {
				return
			}
		}
		found := false
		for dep := range m.scopeOf(addr) {
			if dep != addr && held[dep] {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		for _, a := range mn.sch.Block.Attributes {
			if a.Computed {
				if m.derived[addr] == nil {
					m.derived[addr] = map[string]bool{}
					m.because[addr] = map[string]string{}
				}
				m.derived[addr][a.Name] = true
				m.because[addr][a.Name] = "It is read from outside, and a secret that has no text (a write-only value or a pravo.#Secret) is among what it depends on."
			}
		}
	}
}

// sensitiveOutputs lists the outputs whose value is not shown: the declaration
// says so, or the state recorded it as sensitive.
func (m *model) sensitiveOutputs() map[string]bool {
	out := map[string]bool{}
	if m.spec.root != nil {
		for _, o := range m.spec.root.Outputs {
			if o.Sensitive {
				out[o.Name] = true
			}
		}
	}
	for n, raw := range m.store.Outputs() {
		if _, sens := engine.UnwrapOutput(raw); sens {
			out[n] = true
		}
	}
	return out
}

// taintOf returns the derived fields of a node that the specification declares,
// and whether there is such a node: only then are the references known.
func (m *model) taintOf(addr string) (map[string]bool, bool) {
	mn := m.nodes[addr]
	if mn == nil || m.spec.root == nil || (mn.res == nil && mn.out == nil) {
		return nil, false
	}
	return m.derived[addr], true
}
