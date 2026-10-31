// Package sensitivity says which fields of a resource are withheld because of
// what they read.
//
// A value is secret for what it is (the schema marks it, its name says so) and
// for what it reads: a file whose content embeds a password is as secret as the
// password. The specification says what each field reads, so the second kind is
// worked out from references and not from what the text spells. The viewer, the
// output of a plan and the diffs of the state all ask the same questions of the
// same code, so that they do not disagree about what is secret.
package sensitivity

import (
	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/schema"
)

// MaxDepth bounds how far below an attribute a nested type is followed.
const MaxDepth = 32

// Ref is an attribute that a field reads. Attr is empty when the field reads the
// task as a whole: a container that it iterates, or a task that it passes on.
type Ref struct{ Task, Attr string }

// Reads says what a field that reads r is withheld for.
func (r Ref) Reads() string {
	if r.Attr == "" {
		return "It reads " + r.Task + ", a part of which is withheld."
	}
	return "It reads " + r.Task + "." + r.Attr + ", which is withheld."
}

// Refs maps a node to its top-level fields, and each field to what it reads. An
// output has the one field "value".
type Refs map[string]map[string][]Ref

// Recorder is the resolver of the analysis, noting what it resolves and in which
// top-level field of the body (convert.FieldObserver). What the analysis reads of
// the body as a whole, before it looks at the fields, is not noted: the fields are
// walked after it, and each reads what is below it. fixed, when set, is the one
// field that everything is noted under: an output has a value and no fields.
type Recorder struct {
	convert.Resolver
	addr  func(cue.Path) string
	fixed string
	cur   string
	byFld map[string][]Ref
}

// NewRecorder is a resolver that notes what the analysis resolves under the field
// it is working on, or under fixed when that is set (see Record).
func NewRecorder(res convert.Resolver, addr func(cue.Path) string, fixed string) *Recorder {
	return &Recorder{Resolver: res, addr: addr, fixed: fixed}
}

// SetField is called by the analysis when it starts on a top-level field.
func (r *Recorder) SetField(name string) { r.cur = name }

func (r *Recorder) note(ref Ref) {
	field := r.fixed
	if field == "" {
		field = r.cur
	}
	if field == "" {
		return
	}
	if r.byFld == nil {
		r.byFld = map[string][]Ref{}
	}
	r.byFld[field] = append(r.byFld[field], ref)
}

// Task resolves a reference and notes the attribute it names.
func (r *Recorder) Task(p cue.Path) (cue.Path, cue.Path, bool) {
	task, alias, ok := r.Resolver.Task(p)
	if ok {
		ref := Ref{Task: r.addr(task)}
		if rel := p.Selectors()[len(task.Selectors()):]; len(rel) > 0 && rel[0].LabelType() == cue.StringLabel {
			ref.Attr = rel[0].Unquoted()
		}
		r.note(ref)
	}
	return task, alias, ok
}

// Below notes every task below a container that the field reads whole.
func (r *Recorder) Below(p cue.Path) []cue.Path {
	below := r.Resolver.Below(p)
	for _, t := range below {
		r.note(Ref{Task: r.addr(t)})
	}
	return below
}

// Record runs the analysis that says where a value came from and returns what
// each top-level field of v reads (fixed, when set, is the one field that
// everything is noted under). addr names the node at a task path, or "" when the
// path is none. It is not the analysis that gives the edges: that follows a
// reference to a plain field only while the field is not concrete yet, as ordering
// needs, and a string that a person typed into an attribute is concrete from the
// start. A value is withheld for the attribute it came from, however many fields
// of the specification it was passed on through (the parameter of a definition, a
// name that stands for an attribute), so every reference to a plain field is
// followed to what it is built from (convert.SourceTasks).
//
// A reference to one attribute reads that attribute and not the task: the analysis
// resolves db for db.port as well, so the task is dropped where the same value
// names an attribute of it.
func Record(v cue.Value, self cue.Path, res convert.Resolver, addr func(cue.Path) string, fixed string) map[string][]Ref {
	rec := NewRecorder(res, addr, fixed)
	convert.SourceTasks(v, self, rec)
	return rec.Reads()
}

// Reads returns what the analysis resolved for each field.
func (r *Recorder) Reads() map[string][]Ref {
	out := map[string][]Ref{}
	for field, refs := range r.byFld {
		named := map[string]bool{}
		for _, ref := range refs {
			if ref.Attr != "" {
				named[ref.Task] = true
			}
		}
		seen := map[Ref]bool{}
		for _, ref := range refs {
			if (ref.Attr == "" && named[ref.Task]) || seen[ref] {
				continue
			}
			seen[ref] = true
			out[field] = append(out[field], ref)
		}
	}
	return out
}

// Withheld says whether an attribute is withheld by what it is: the schema marks
// it sensitive or write-only, or its name says it is a secret.
func Withheld(name string, sensitive, writeOnly bool) bool {
	return sensitive || writeOnly || schema.NameGuard.MatchString(name)
}

// TopSensitive lists the attributes and blocks of a type that are withheld by
// what they are, or that hold something that is: the schema marks them, their
// name says so, or a nested attribute or block does.
func TopSensitive(b *schema.Block) map[string]bool {
	out := map[string]bool{}
	for _, a := range b.Attributes {
		if Withheld(a.Name, a.Sensitive, a.WriteOnly) || nestedSensitive(a.NestedType, 0) {
			out[a.Name] = true
		}
	}
	for _, nb := range b.BlockTypes {
		if schema.NameGuard.MatchString(nb.TypeName) || blockHoldsSensitive(nb.Block, 0) {
			out[nb.TypeName] = true
		}
	}
	return out
}

func nestedSensitive(o *schema.Object, depth int) bool {
	if o == nil || depth > MaxDepth {
		return false
	}
	for _, a := range o.Attributes {
		if Withheld(a.Name, a.Sensitive, a.WriteOnly) || nestedSensitive(a.NestedType, depth+1) {
			return true
		}
	}
	return false
}

func blockHoldsSensitive(b *schema.Block, depth int) bool {
	if b == nil || depth > MaxDepth {
		return false
	}
	for _, a := range b.Attributes {
		if Withheld(a.Name, a.Sensitive, a.WriteOnly) || nestedSensitive(a.NestedType, depth+1) {
			return true
		}
	}
	for _, nb := range b.BlockTypes {
		if schema.NameGuard.MatchString(nb.TypeName) || blockHoldsSensitive(nb.Block, depth+1) {
			return true
		}
	}
	return false
}

// Derived is the result of Derive: by node, the top-level fields that read
// something withheld, and the sentence that says what.
type Derived struct {
	Fields  map[string]map[string]bool
	Because map[string]map[string]string
}

// NewDerived is a result with nothing in it.
func NewDerived() Derived {
	return Derived{Fields: map[string]map[string]bool{}, Because: map[string]map[string]string{}}
}

// Mark adds a field that is withheld for a reason that is not a reference.
func (d Derived) Mark(addr, field, because string) {
	if d.Fields[addr] == nil {
		d.Fields[addr] = map[string]bool{}
		d.Because[addr] = map[string]string{}
	}
	d.Fields[addr][field] = true
	d.Because[addr][field] = because
}

// Derive follows what the fields read, in the order given, until nothing
// changes, and adds what it finds to d (which may hold fields already). withheld
// says whether attribute attr of task (the task as a whole when attr is empty) is
// withheld, given what is derived so far; it is called again as the result grows.
// Nothing is compared as text.
func Derive(order []string, refs Refs, d Derived, withheld func(task, attr string, so Derived) bool) Derived {
	for changed, pass := true, 0; changed && pass <= len(order); pass++ {
		changed = false
		for _, addr := range order {
			for field, rs := range refs[addr] {
				if d.Fields[addr][field] {
					continue
				}
				for _, r := range rs {
					if r.Task != "" && withheld(r.Task, r.Attr, d) {
						d.Mark(addr, field, r.Reads())
						changed = true
						break
					}
				}
			}
		}
	}
	return d
}
