package serve

import (
	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/schema"
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

// taintRef is an attribute that a field reads. attr is empty when the field
// reads the task as a whole: a container that it iterates, or a task that it
// passes on.
type taintRef struct{ task, attr string }

// taintRecorder is the resolver of the analysis, noting what it resolves and in
// which top-level field of the body (convert.FieldObserver). What the analysis
// reads of the body as a whole, before it looks at the fields, is not noted: the
// fields are walked after it, and each reads what is below it. fixed, when set,
// is the one field that everything is noted under: an output has a value and no
// fields.
type taintRecorder struct {
	*specResolver
	fixed string
	cur   string
	byFld map[string][]taintRef
}

func (r *taintRecorder) SetField(name string) { r.cur = name }

func (r *taintRecorder) addr(task cue.Path) string {
	if res, ok := r.byPath[task.String()]; ok {
		return res.Address()
	}
	if o, ok := r.outputs[task.String()]; ok {
		return o.Name
	}
	return ""
}

func (r *taintRecorder) note(ref taintRef) {
	field := r.fixed
	if field == "" {
		field = r.cur
	}
	if field == "" {
		return
	}
	if r.byFld == nil {
		r.byFld = map[string][]taintRef{}
	}
	r.byFld[field] = append(r.byFld[field], ref)
}

func (r *taintRecorder) Task(p cue.Path) (cue.Path, cue.Path, bool) {
	task, alias, ok := r.specResolver.Task(p)
	if ok {
		ref := taintRef{task: r.addr(task)}
		if rel := p.Selectors()[len(task.Selectors()):]; len(rel) > 0 && rel[0].LabelType() == cue.StringLabel {
			ref.attr = rel[0].Unquoted()
		}
		r.note(ref)
	}
	return task, alias, ok
}

func (r *taintRecorder) Below(p cue.Path) []cue.Path {
	below := r.specResolver.Below(p)
	for _, t := range below {
		r.note(taintRef{task: r.addr(t)})
	}
	return below
}

// reads returns what the analysis resolved for each field, less the task that
// the path of a reference names the way to: the analysis resolves db for db.port as
// well, and a reference to one attribute reads that attribute and not the task. A
// field that reads an attribute of a task and passes the same task on as a whole in
// the same value is read as the attribute only.
func (r *taintRecorder) reads() map[string][]taintRef {
	out := map[string][]taintRef{}
	for field, refs := range r.byFld {
		named := map[string]bool{}
		for _, ref := range refs {
			if ref.attr != "" {
				named[ref.task] = true
			}
		}
		seen := map[taintRef]bool{}
		for _, ref := range refs {
			if (ref.attr == "" && named[ref.task]) || seen[ref] {
				continue
			}
			seen[ref] = true
			out[field] = append(out[field], ref)
		}
	}
	return out
}

// recordTaint keeps what the fields of a declared resource, or the value of an
// output (field "value"), read of the tasks. It is filled by computeEdges, which
// runs the analysis for the edges: one walk gives both.
func (ss *specState) recordTaint(addr string, rec *taintRecorder) {
	if refs := rec.reads(); len(refs) > 0 {
		if ss.taint == nil {
			ss.taint = map[string]map[string][]taintRef{}
		}
		ss.taint[addr] = refs
	}
}

// topSensitive lists the attributes and blocks of a type that are withheld by
// what they are, or that hold something that is: the schema marks them, their
// name says so, or a nested attribute or block does.
func topSensitive(b *schema.Block) map[string]bool {
	out := map[string]bool{}
	for _, a := range b.Attributes {
		if withholdReason(a.Name, a.Sensitive, a.WriteOnly) != "" || nestedSensitive(a.NestedType, 0) {
			out[a.Name] = true
		}
	}
	for _, nb := range b.BlockTypes {
		if guarded(nb.TypeName) || blockHoldsSensitive(nb.Block, 0) {
			out[nb.TypeName] = true
		}
	}
	return out
}

func nestedSensitive(o *schema.Object, depth int) bool {
	if o == nil || depth > maxDepth {
		return false
	}
	for _, a := range o.Attributes {
		if withholdReason(a.Name, a.Sensitive, a.WriteOnly) != "" || nestedSensitive(a.NestedType, depth+1) {
			return true
		}
	}
	return false
}

func blockHoldsSensitive(b *schema.Block, depth int) bool {
	if b == nil || depth > maxDepth {
		return false
	}
	for _, a := range b.Attributes {
		if withholdReason(a.Name, a.Sensitive, a.WriteOnly) != "" || nestedSensitive(a.NestedType, depth+1) {
			return true
		}
	}
	for _, nb := range b.BlockTypes {
		if guarded(nb.TypeName) || blockHoldsSensitive(nb.Block, depth+1) {
			return true
		}
	}
	return false
}

// computeTaint works out which fields of the declared nodes are derived from a
// withheld attribute, by following what each field reads until nothing changes.
// A node that the specification does not declare has no references to follow, and
// is judged by text (secretsAt).
func (m *model) computeTaint() {
	m.derived = map[string]map[string]bool{}
	if m.spec.root == nil || len(m.spec.taint) == 0 {
		return
	}
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
			if len(t) > 0 {
				base[addr] = tops{set: t}
			}
		case mn.rec != nil && !transient(mn.Mode) && !mn.Sealed && mn.sch == nil:
			// Nothing says which attributes are sensitive: all of them are withheld.
			base[addr] = tops{all: true}
		}
	}
	sensitive := func(task, attr string) bool {
		if b, ok := base[task]; ok {
			if b.all || (attr != "" && b.set[attr]) || (attr == "" && len(b.set) > 0) {
				return true
			}
		}
		d := m.derived[task]
		if attr == "" {
			return len(d) > 0
		}
		return d[attr]
	}
	for changed, pass := true, 0; changed && pass <= len(m.order); pass++ {
		changed = false
		for _, addr := range m.order {
			for field, refs := range m.spec.taint[addr] {
				if m.derived[addr][field] {
					continue
				}
				for _, r := range refs {
					if r.task != "" && sensitive(r.task, r.attr) {
						if m.derived[addr] == nil {
							m.derived[addr] = map[string]bool{}
						}
						m.derived[addr][field] = true
						changed = true
						break
					}
				}
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

// walkFor is the walk that renders the values of the node at addr. A node that the
// specification declares is judged by what its fields read (taintOf), and by its
// own strings, since a provider may echo a sensitive value into an attribute that
// it computes; any other is judged by the strings of what it reads (secretsAt).
func (m *model) walkFor(po *planOverlay, addr string, build bool) *walk {
	if t, ok := m.taintOf(addr); ok {
		return &walk{secrets: m.ownText(po, addr), taint: t, build: build}
	}
	return &walk{secrets: m.secretsAt(po, addr), build: build}
}
