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
	if resolve == nil || pending == nil {
		return nil, false
	}
	hit := func(info RefInfo) bool {
		for _, t := range info.TaskRefs {
			if pending(t) {
				return true
			}
		}
		return false
	}
	var walk func(v cue.Value, rel []cue.Selector) bool
	walk = func(v cue.Value, rel []cue.Selector) bool {
		switch v.IncompleteKind() {
		case cue.StructKind:
			if !containerFree(v, self, resolve, pending, hit) {
				return false
			}
			it, err := v.Fields()
			if err != nil {
				return false
			}
			for it.Next() {
				if !walk(it.Value(), append(append([]cue.Selector(nil), rel...), it.Selector())) {
					return false
				}
			}
			return true
		case cue.ListKind:
			if !containerFree(v, self, resolve, pending, hit) {
				return false
			}
			it, err := v.List()
			if err != nil {
				return false
			}
			for i := 0; it.Next(); i++ {
				if !walk(it.Value(), append(append([]cue.Selector(nil), rel...), cue.Index(i))) {
					return false
				}
			}
			return true
		}
		// A leaf. One that no pending task reaches is the same before and after
		// the values are unified in; the analysis follows plain fields, so a
		// value that is concrete because of a default is not missed.
		if !hit(analyse(v, resolve, &self, true)) {
			return true
		}
		var item cue.Value
		count := 0
		for _, c := range conjuncts(v) {
			if hit(analyse(c, resolve, &self, true)) {
				item = c
				count++
			}
		}
		if count != 1 {
			return false
		}
		root, p := item.ReferencePath()
		if !root.Exists() || len(p.Selectors()) == 0 {
			return false
		}
		task, alias, found := resolve.Task(p)
		if !found || alias.String() != task.String() || !pending(task) {
			return false
		}
		sels := p.Selectors()
		n := len(task.Selectors())
		if len(sels) <= n {
			return false
		}
		refs = append(refs, LeafRef{Path: cue.MakePath(rel...), Task: task, Attr: sels[n:]})
		return true
	}
	if !walk(body, nil) {
		return nil, false
	}
	return refs, true
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
		scanComprehensions(src, resolve, &self, scanned, add, follow, func(*ast.LetClause) { found = true })
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
