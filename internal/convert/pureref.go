package convert

import (
	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
)

// LeafRef is a leaf of a task body that is exactly a reference to an attribute
// of another task: p: net.id.
type LeafRef struct {
	// Path is the leaf, relative to the body.
	Path cue.Path
	// Task is the task that the leaf reads from.
	Task cue.Path
	// Attr is the attribute of that task that the leaf reads.
	Attr []cue.Selector
}

// PureRefs finds the leaves of a task body that are plain references to the
// attributes of the tasks that pending names. The values of those tasks can
// then be put into the body one leaf at a time, without evaluating the whole
// specification again, and the result is the same value that unifying them into
// the specification would give.
//
// ok is false when the body depends on such a task in any other way: in an
// expression, a condition, a comprehension, an embedded struct, a let, or as a
// whole struct or list. A value that is built from the attribute cannot be put
// together without CUE, so the caller unifies the values into the
// specification then.
func PureRefs(body cue.Value, self cue.Path, resolve Resolver, pending func(task cue.Path) bool) (refs []LeafRef, ok bool) {
	return pureRefs(body, self, resolve, pending, false)
}

// BodyRefs is PureRefs for every task at once, and it also takes the variable
// of a loop that is read by element (subnet_id: v.id, where the resolver knows
// the one element that v stands for) as the reference to that element. ok says
// that the body is the specification's own value of it with nothing but such
// leaves left open: no task changes anything else in it, whatever it computes.
// The hidden fields and the definitions of the body are looked at as well.
func BodyRefs(body cue.Value, self cue.Path, resolve Resolver) (refs []LeafRef, ok bool) {
	return pureRefs(body, self, resolve, func(cue.Path) bool { return true }, true)
}

// pureRefs is PureRefs. With whole set it is BodyRefs.
func pureRefs(body cue.Value, self cue.Path, resolve Resolver, pending func(task cue.Path) bool, whole bool) (refs []LeafRef, ok bool) {
	if resolve == nil || pending == nil {
		return nil, false
	}
	w := &pureWalk{self: self, resolve: resolve, pending: pending, whole: whole}
	if loops, ok := resolve.(LoopReader); ok && whole {
		w.keys = keyless{resolve, loops}
	}
	if !w.walk(body, nil) {
		return nil, false
	}
	return w.refs, true
}

// pureWalk is one walk of pureRefs over a body.
type pureWalk struct {
	self    cue.Path
	resolve Resolver
	// keys is resolve with the keys of loops read as labels (keyless), set
	// for BodyRefs when the resolver knows the loops.
	keys    Resolver
	pending func(task cue.Path) bool
	whole   bool
	refs    []LeafRef
}

// hit reports whether the analysis reached a pending task.
func (w *pureWalk) hit(info RefInfo) bool {
	for _, t := range info.TaskRefs {
		if w.pending(t) {
			return true
		}
	}
	return false
}

// walk visits the value at rel in the body. It is false when the value depends
// on a pending task in a way that is not a plain reference.
func (w *pureWalk) walk(v cue.Value, rel []cue.Selector) bool {
	switch v.IncompleteKind() {
	case cue.StructKind:
		if !containerFree(v, w.self, w.resolve, w.pending, w.hit) {
			return false
		}
		var opts []cue.Option
		if w.whole {
			opts = []cue.Option{cue.Hidden(true), cue.Definitions(true)}
		}
		it, err := v.Fields(opts...)
		if err != nil {
			return false
		}
		for it.Next() {
			if !w.walk(it.Value(), append(append([]cue.Selector(nil), rel...), it.Selector())) {
				return false
			}
		}
		return true
	case cue.ListKind:
		if !containerFree(v, w.self, w.resolve, w.pending, w.hit) {
			return false
		}
		it, err := v.List()
		if err != nil {
			return false
		}
		for i := 0; it.Next(); i++ {
			if !w.walk(it.Value(), append(append([]cue.Selector(nil), rel...), cue.Index(i))) {
				return false
			}
		}
		return true
	}
	return w.leaf(v, rel)
}

// leaf visits a leaf. One that no pending task reaches is the same before and
// after the values are unified in; the analysis follows plain fields, so a
// value that is concrete because of a default is not missed. Any other leaf has
// to be one reference to an attribute of a pending task, unified with what
// reads none.
func (w *pureWalk) leaf(v cue.Value, rel []cue.Selector) bool {
	if !w.hit(analyse(v, w.resolve, &w.self, true)) {
		return true
	}
	var item cue.Value
	count := 0
	for _, c := range conjuncts(v) {
		if !w.hit(analyse(c, w.resolve, &w.self, true)) {
			continue
		}
		if w.keys != nil && !w.hit(analyse(c, w.keys, &w.self, true)) {
			// Of the tasks, the conjunct reads the key of a loop and nothing
			// more: the label of the element, which is the same before and
			// after the values are unified in.
			continue
		}
		item = c
		count++
	}
	if count == 0 && w.keys != nil {
		return true
	}
	if count != 1 {
		return false
	}
	root, p := item.ReferencePath()
	if !root.Exists() || len(p.Selectors()) == 0 || p.Err() != nil {
		// The variable of a loop has no path in the specification.
		ref, ok := loopLeaf(item, w.self, w.resolve)
		if !w.whole || !ok {
			return false
		}
		ref.Path = cue.MakePath(rel...)
		w.refs = append(w.refs, ref)
		return true
	}
	task, alias, found := w.resolve.Task(p)
	if !found || alias.String() != task.String() || !w.pending(task) {
		return false
	}
	sels := p.Selectors()
	n := len(task.Selectors())
	if len(sels) <= n {
		return false
	}
	w.refs = append(w.refs, LeafRef{Path: cue.MakePath(rel...), Task: task, Attr: sels[n:]})
	return true
}

// keyless is a resolver for which the key of a loop that is read by element
// reads no task. It does not share the memo of the resolver it wraps, whose
// answers are for the keys as reads of their elements.
type keyless struct {
	Resolver
	loops LoopReader
}

func (k keyless) LoopRead(rd LoopRead) ([]cue.Path, bool) {
	tasks, ok := k.loops.LoopRead(rd)
	if ok && rd.Key {
		return nil, true
	}
	return tasks, ok
}

// loopLeaf reads a leaf that is the variable of a loop followed by names, v.id:
// the attribute id of the one element that the resolver says v stands for in
// the task at self.
func loopLeaf(item cue.Value, self cue.Path, resolve Resolver) (LeafRef, bool) {
	loops, ok := resolve.(LoopReader)
	if !ok {
		return LeafRef{}, false
	}
	src := item.Source()
	rd, ok := loopRead(src)
	if !ok || rd.Key || rd.Depth == 0 {
		return LeafRef{}, false
	}
	rd.Self = &self
	tasks, ok := loops.LoopRead(rd)
	if !ok || len(tasks) != 1 {
		return LeafRef{}, false
	}
	if f, ok := src.(*ast.Field); ok {
		src = f.Value
	}
	_, sels, _ := refChain(src.(ast.Expr))
	return LeafRef{Task: tasks[0], Attr: sels}, true
}

// conjuncts lists what a value is the unification of: the arguments of &,
// at any depth, or the value itself.
func conjuncts(v cue.Value) []cue.Value {
	op, args := v.Expr()
	if op != cue.AndOp {
		return []cue.Value{v}
	}
	var out []cue.Value
	for _, a := range args {
		out = append(out, conjuncts(a)...)
	}
	return out
}

// containerFree reports whether a struct or a list is made without any pending
// task: not by a reference to one or to something built from one, not by a
// comprehension or a condition that reads one, and not by embedding anything. The
// fields of it are looked at on their own.
func containerFree(v cue.Value, self cue.Path, resolve Resolver, pending func(cue.Path) bool, hit func(RefInfo) bool) bool {
	for _, c := range conjuncts(v) {
		if root, p := c.ReferencePath(); root.Exists() && len(p.Selectors()) > 0 {
			// A struct that is a reference stands for what it refers to.
			if hit(analyse(c, resolve, &self, true)) {
				return false
			}
			continue
		}
		op, _ := c.Expr()
		if op != cue.NoOp {
			// A call, a disjunction, an operator: any of them may be built from
			// a task.
			if hit(analyse(c, resolve, &self, true)) {
				return false
			}
			continue
		}
		src := c.Source()
		if src == nil {
			continue
		}
		scanned := map[ast.Node]bool{}
		found := false
		add := func(task, _ cue.Path) {
			if pending(task) {
				found = true
			}
		}
		follow := func(cue.Path) bool { return true }
		scanComprehensions(src, resolve, &self, scanned, add, follow, func(*ast.LetClause) { found = true }, func(LoopRead) { found = true })
		if found {
			return false
		}
		embeds := false
		ast.Walk(src, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.EmbedDecl, *ast.Comprehension, *ast.LetClause:
				embeds = true
			}
			return !embeds
		}, nil)
		if embeds {
			return false
		}
	}
	return true
}

// ExternalReads lists the tasks that something other than a task body reads: a
// plain field, an output's value, a comprehension that builds tasks. A value
// computed by one of them cannot be checked against everything that reads it by
// looking at the task bodies alone. all is true when the specification holds
// something this cannot account for (an embedding), and then every task counts as
// read.
func ExternalReads(root cue.Value, isTask, holdsTask func(cue.Path) bool, resolve Resolver) (reads map[string]bool, all bool) {
	reads = map[string]bool{}
	add := func(task, _ cue.Path) { reads[task.String()] = true }
	var walk func(v cue.Value, p cue.Path)
	walk = func(v cue.Value, p cue.Path) {
		if all || (len(p.Selectors()) > 0 && isTask(p)) {
			return
		}
		switch v.IncompleteKind() {
		case cue.StructKind:
			if src := v.Source(); src != nil {
				if holdsTask(p) {
					// The syntax of a struct that holds tasks includes their bodies, which
					// are theirs: only what is written beside the tasks is looked at.
					ast.Walk(src, func(n ast.Node) bool {
						switch n.(type) {
						case *ast.Field:
							return false
						case *ast.EmbedDecl, *ast.Comprehension, *ast.LetClause:
							all = true
						}
						return !all
					}, nil)
				} else {
					scanComprehensions(src, resolve, nil, map[ast.Node]bool{}, add, func(cue.Path) bool { return true }, func(*ast.LetClause) {}, nil)
					ast.Walk(src, func(n ast.Node) bool {
						if _, ok := n.(*ast.EmbedDecl); ok {
							all = true
						}
						return !all
					}, nil)
				}
			}
			for _, c := range conjuncts(v) {
				if r, rp := c.ReferencePath(); r.Exists() && len(rp.Selectors()) > 0 {
					for _, t := range SourceTasks(c, cue.Path{}, resolve) {
						reads[t.String()] = true
					}
				}
			}
			it, err := v.Fields()
			if err != nil {
				all = true
				return
			}
			for it.Next() {
				walk(it.Value(), cue.MakePath(append(append([]cue.Selector(nil), p.Selectors()...), it.Selector())...))
			}
		case cue.ListKind:
			it, err := v.List()
			if err != nil {
				all = true
				return
			}
			for i := 0; it.Next(); i++ {
				walk(it.Value(), cue.MakePath(append(append([]cue.Selector(nil), p.Selectors()...), cue.Index(i))...))
			}
		default:
			for _, t := range SourceTasks(v, cue.Path{}, resolve) {
				reads[t.String()] = true
			}
		}
	}
	walk(root, cue.Path{})
	return reads, all
}
