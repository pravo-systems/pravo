// Package convert translates between CUE values and provider (tftypes)
// values, implementing pravo's known / unknown / null classification.
package convert

import (
	"fmt"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
)

// Resolver tells the reference analysis where the tasks (resources, data
// sources, ephemeral resources and function calls) of a specification are.
type Resolver interface {
	// Task maps a referenced path to the task it belongs to: the shortest
	// prefix of p that is a declared task. alias is kept for callers that
	// track copies of a task body; it equals task when the reference names
	// the task directly.
	Task(p cue.Path) (task, alias cue.Path, ok bool)
	// Below lists the tasks that sit below p, in a struct or a list that is
	// not a task itself. A reference to p stands for every one of them: a
	// comprehension over pets reads pets.a and pets.b.
	Below(p cue.Path) []cue.Path
	// Value returns the specification's value at the absolute path p, or a
	// value that does not exist. The analysis follows a plain field that a
	// comprehension names and that is built from tasks.
	Value(p cue.Path) cue.Value
}

// ResolverFunc is a Resolver whose tasks all sit at the paths the function
// maps; it knows no container of tasks.
type ResolverFunc func(p cue.Path) (task, alias cue.Path, ok bool)

// Task calls f.
func (f ResolverFunc) Task(p cue.Path) (cue.Path, cue.Path, bool) { return f(p) }

// Below reports no tasks.
func (f ResolverFunc) Below(cue.Path) []cue.Path { return nil }

// Value reports no value.
func (f ResolverFunc) Value(cue.Path) cue.Value { return cue.Value{} }

// RefInfo is the result of analysing the expression tree of a value.
type RefInfo struct {
	// TaskRefs lists the tasks the expression refers to.
	TaskRefs []cue.Path
	// Aliases lists, for every reference that went through a merged copy,
	// the alias prefix and the task it denotes.
	Aliases []Alias
	// Incomplete is set when the expression refers to a value outside any
	// task that is itself not concrete (for example an unset @tag field).
	Incomplete *cue.Path
}

// Alias is a path holding a copy of a task body (for example
// services.web.resource.random_pet.x) together with the task it refers to.
type Alias struct {
	Path cue.Path
	Task cue.Path
}

// HasTaskRef reports whether the expression depends on another task.
func (r RefInfo) HasTaskRef() bool { return len(r.TaskRefs) > 0 }

// AnalyseWith walks the expression tree of v (through unification,
// interpolation, calls, operators and literal lists/structs in expression
// position) and reports references to tasks and to incomplete non-task
// values. References into self are ignored.
func AnalyseWith(v cue.Value, resolve Resolver, self *cue.Path) RefInfo {
	if resolve == nil {
		resolve = ResolverFunc(func(cue.Path) (cue.Path, cue.Path, bool) { return cue.Path{}, cue.Path{}, false })
	}
	info := RefInfo{}
	seen := map[string]bool{}
	tasks := map[string]bool{}
	aliasSeen := map[string]bool{}
	addTask := func(task, alias cue.Path) {
		if self != nil && task.String() == self.String() {
			return
		}
		if !tasks[task.String()] {
			tasks[task.String()] = true
			info.TaskRefs = append(info.TaskRefs, task)
		}
		if alias.String() != task.String() && !aliasSeen[alias.String()] {
			aliasSeen[alias.String()] = true
			info.Aliases = append(info.Aliases, Alias{Path: alias, Task: task})
		}
	}
	scanned := map[ast.Node]bool{}
	// letSeen holds the lets whose expression has been read: a let that names a
	// task, or a value built from tasks, is a dependency on them wherever it is
	// used, and one that is used twice is read once.
	letSeen := map[*ast.LetClause]bool{}
	loopSeen := map[*ast.ForClause]bool{}
	var scanLet func(lc *ast.LetClause)
	// walk visits v. A value that is a reference has operands, the container
	// and the key of ports[z], that CUE used to reach what it refers to; they
	// are the way to the value, and the value alone is read. nav is set for
	// them, so that a container among them is not a reference to all of its
	// tasks.
	var walk func(v cue.Value, depth int, nav bool)
	// follow reads the plain field at p that a comprehension names. Whatever
	// the field is built from may be a task, as for a reference in the
	// expression tree. It reports whether the specification has such a field.
	follow := func(p cue.Path, depth int) bool {
		target := resolve.Value(p)
		if !target.Exists() {
			return false
		}
		if key := "follow " + p.String(); !seen[key] {
			seen[key] = true
			if !IsConcrete(target) {
				walk(target, depth+1, false)
			}
		}
		return true
	}
	scanLet = func(lc *ast.LetClause) {
		if letSeen[lc] || lc.Expr == nil {
			return
		}
		letSeen[lc] = true
		var anc []cue.Selector
		if self != nil {
			anc = self.Selectors()
		}
		sc := &refScan{resolve: resolve, anc: anc, add: addTask, follow: func(p cue.Path) bool { return follow(p, 0) }, local: map[string]bool{}, let: scanLet}
		ast.Walk(lc.Expr, sc.visit, nil)
	}
	walk = func(v cue.Value, depth int, nav bool) {
		if depth > 64 {
			return
		}
		// A name bound by a let is not a path in the specification: the value
		// behind it is what the let was set to, and reaching it through the
		// expression tree reads that value's own body, which would make the task
		// depend on what its dependency depends on. The expression of the let
		// is read instead.
		if lc := letReference(v.Source()); lc != nil {
			scanLet(lc)
			return
		}
		// A loop variable is bound by the source of its for clause, and CUE shows
		// no path for it: the source is read for the tasks it names.
		if fc := loopReference(v.Source()); fc != nil && fc.Source != nil {
			if !loopSeen[fc] {
				loopSeen[fc] = true
				var anc []cue.Selector
				if self != nil {
					anc = self.Selectors()
				}
				sc := &refScan{resolve: resolve, anc: anc, add: addTask, follow: func(p cue.Path) bool { return follow(p, 0) }, local: map[string]bool{}, let: scanLet}
				ast.Walk(fc.Source, sc.visit, nil)
			}
			return
		}
		root, p := v.ReferencePath()
		isRef := root.Exists() && len(p.Selectors()) > 0
		if isRef {
			key := p.String()
			if nav {
				key = "operand " + key
			}
			if !seen[key] {
				seen[key] = true
				if task, alias, ok := resolve.Task(p); ok {
					addTask(task, alias)
				} else if self == nil || !isPrefixPath(p, *self) {
					// Anything but a reference to a struct the task sits inside
					// (the instance of a definition), whose inner references are
					// reported on their own paths.

					// A plain field. A container of tasks stands for every
					// task in it. If its value is not concrete, whatever it
					// is built from may be a task, so follow it.
					var below []cue.Path
					if !nav {
						below = resolve.Below(p)
					}
					for _, t := range below {
						addTask(t, t)
					}
					// An operand of a reference is not followed: the reference
					// it belongs to names the path that is read, and that path is
					// followed. Following the container would take every task
					// below it as a source and validate everything it holds.
					target := root.LookupPath(p)
					if !nav && target.Exists() {
						if d, _ := target.Default(); d.Validate(cue.Concrete(true)) != nil {
							before := len(info.TaskRefs)
							walk(target, depth+1, false)
							if len(below) == 0 && len(info.TaskRefs) == before && info.Incomplete == nil {
								pp := p
								info.Incomplete = &pp
							}
						}
					}
				}
			}
		}
		op, args := v.Expr()
		if op == cue.NoOp {
			// The expression tree stops at a comprehension: what its clauses
			// and its body refer to is found in the syntax.
			scanComprehensions(v.Source(), resolve, self, scanned, addTask, func(p cue.Path) bool { return follow(p, depth) }, scanLet)
			switch {
			case v.IncompleteKind() == cue.ListKind:
				if it, err := v.List(); err == nil {
					for it.Next() {
						walk(it.Value(), depth+1, false)
					}
				}
			case v.IncompleteKind() == cue.StructKind && depth > 0:
				if it, err := v.Fields(); err == nil {
					for it.Next() {
						walk(it.Value(), depth+1, false)
					}
				}
			}
			return
		}
		for _, a := range args {
			walk(a, depth+1, isRef)
		}
	}
	walk(v, 0, false)
	return info
}

// Dependencies walks every field and element of a task body and reports all
// tasks it references, whether or not the referenced values are concrete,
// together with the alias prefixes used to reach them.
func Dependencies(v cue.Value, self cue.Path, resolve Resolver) (deps []cue.Path, aliases []Alias) {
	seen := map[string]bool{}
	aliasSeen := map[string]bool{}
	var visit func(v cue.Value, depth int)
	visit = func(v cue.Value, depth int) {
		if depth > 32 {
			return
		}
		info := AnalyseWith(v, resolve, &self)
		for _, t := range info.TaskRefs {
			if !seen[t.String()] {
				seen[t.String()] = true
				deps = append(deps, t)
			}
		}
		for _, a := range info.Aliases {
			if !aliasSeen[a.Path.String()] {
				aliasSeen[a.Path.String()] = true
				aliases = append(aliases, a)
			}
		}
		if _, p := v.ReferencePath(); len(p.Selectors()) > 0 {
			// Do not descend into referenced structures; their own
			// dependencies belong to them.
			return
		}
		switch v.IncompleteKind() {
		case cue.StructKind:
			if it, err := v.Fields(); err == nil {
				for it.Next() {
					visit(it.Value(), depth+1)
				}
			}
		case cue.ListKind:
			if it, err := v.List(); err == nil {
				for it.Next() {
					visit(it.Value(), depth+1)
				}
			}
		}
	}
	visit(v, 0)
	return deps, aliases
}

// IsConcrete reports whether v (after applying defaults) is fully concrete.
func IsConcrete(v cue.Value) bool {
	if !v.Exists() {
		return false
	}
	d, _ := v.Default()
	return d.Validate(cue.Concrete(true)) == nil
}

// Default returns v with defaults applied where possible.
func Default(v cue.Value) cue.Value {
	d, _ := v.Default()
	return d
}

// IncompleteError returns a descriptive error for a non-concrete value that
// does not depend on any task.
func IncompleteError(path string, v cue.Value, info RefInfo) error {
	if info.Incomplete != nil {
		return fmt.Errorf("%s: value depends on %s, which is not concrete (is a -t tag missing?)", path, info.Incomplete)
	}
	if err := v.Err(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return fmt.Errorf("%s: value is not concrete: %v", path, v)
}

// isPrefixPath reports whether p is self or one of its ancestors.
func isPrefixPath(p, self cue.Path) bool {
	ps, ss := p.Selectors(), self.Selectors()
	if len(ps) > len(ss) {
		return false
	}
	for i := range ps {
		if ps[i].String() != ss[i].String() {
			return false
		}
	}
	return true
}

// scanComprehensions reports the tasks that the comprehensions inside the
// syntax of a value refer to. A for clause, an if clause or the body of a
// comprehension is not part of the expression tree that cue.Value.Expr
// returns, so a value such as [for k, v in net.outputs.subnets {v}] shows no
// reference to net in it.
func scanComprehensions(src ast.Node, resolve Resolver, self *cue.Path, scanned map[ast.Node]bool, add func(task, alias cue.Path), follow func(p cue.Path) bool, let func(*ast.LetClause)) {
	if src == nil || resolve == nil || scanned[src] {
		return
	}
	scanned[src] = true
	var anc []cue.Selector
	if self != nil {
		anc = self.Selectors()
	}
	ast.Walk(src, func(n ast.Node) bool {
		c, ok := n.(*ast.Comprehension)
		if !ok {
			// A name that a let binds, outside any comprehension: what the let
			// was set to is what the name refers to.
			if lc := letReference(n); lc != nil {
				let(lc)
				return false
			}
			return true
		}
		if !scanned[c] {
			scanned[c] = true
			s := &refScan{resolve: resolve, anc: anc, add: add, follow: follow, local: map[string]bool{}, let: let}
			ast.Walk(c, s.visit, nil)
		}
		return false
	}, nil)
}

// refScan collects the references in the syntax of one comprehension.
type refScan struct {
	resolve Resolver
	// anc holds the selectors of the task that contains the comprehension.
	anc []cue.Selector
	add func(task, alias cue.Path)
	// follow looks into the plain field at an absolute path; it reports
	// whether there is one.
	follow func(p cue.Path) bool
	// local holds the names the comprehension declares: loop variables, let
	// names and the fields of structs inside it.
	local map[string]bool
	// let reads the expression of a let that was declared outside the syntax
	// being scanned, when the syntax uses its name.
	let func(*ast.LetClause)
}

func (s *refScan) visit(n ast.Node) bool {
	switch x := n.(type) {
	case *ast.ForClause:
		for _, id := range []*ast.Ident{x.Key, x.Value} {
			if id != nil {
				s.local[id.Name] = true
			}
		}
	case *ast.LetClause:
		if x.Ident != nil {
			s.local[x.Ident.Name] = true
		}
	case *ast.Field:
		// A label names a field, it does not refer to one; a computed label does.
		switch l := x.Label.(type) {
		case *ast.Ident:
			s.local[l.Name] = true
		case *ast.BasicLit:
		default:
			ast.Walk(x.Label, s.visit, nil)
		}
		if x.Value != nil {
			ast.Walk(x.Value, s.visit, nil)
		}
		return false
	case *ast.Ident:
		s.reference(x, nil)
		return false
	case *ast.SelectorExpr, *ast.IndexExpr:
		head, sels, dyn := refChain(n.(ast.Expr))
		if head == nil {
			// Not a name followed by selectors: look at the parts of the
			// expression, but not at the selector, which is a label.
			switch y := n.(type) {
			case *ast.SelectorExpr:
				ast.Walk(y.X, s.visit, nil)
			case *ast.IndexExpr:
				ast.Walk(y.X, s.visit, nil)
				ast.Walk(y.Index, s.visit, nil)
			}
			return false
		}
		s.reference(head, sels)
		for _, d := range dyn {
			ast.Walk(d, s.visit, nil)
		}
		return false
	}
	return true
}

// reference resolves the name head followed by sels to a task. The name is
// declared by the nearest enclosing struct that has a field of that name,
// which is not known here, so the structs around the task are tried from the
// innermost outwards. Where the parser has resolved the name, that narrows
// the search: a name found at the top of a file is a field of the root, and a
// name found in a struct is not.
func (s *refScan) reference(head *ast.Ident, sels []cue.Selector) {
	if s.local[head.Name] {
		return
	}
	first, last := max(len(s.anc)-1, 0), 0
	if lc := letOf(head); lc != nil {
		if s.let != nil {
			s.let(lc)
		}
		return
	}
	switch head.Scope.(type) {
	case *ast.ImportSpec, *ast.ForClause:
		return
	case *ast.File:
		first = 0
	case *ast.StructLit:
		last = 1
	}
	if _, ok := head.Node.(*ast.ImportSpec); ok {
		return
	}
	chain := append([]cue.Selector{cue.Str(head.Name)}, sels...)
	if strings.HasPrefix(head.Name, "#") {
		chain[0] = cue.Def(head.Name)
	}
	for n := first; n >= last; n-- {
		p := cue.MakePath(append(append([]cue.Selector{}, s.anc[:n]...), chain...)...)
		if task, alias, ok := s.resolve.Task(p); ok {
			s.add(task, alias)
			return
		}
		// A struct the task sits inside is not a dependency of the task, as
		// for a reference in the expression tree: the tasks beside it are
		// reached by the references that name them.
		if s.encloses(p) {
			return
		}
		// A name that stands for a container of tasks is every task in it.
		below := s.resolve.Below(p)
		for _, t := range below {
			s.add(t, t)
		}
		// A plain field that is built from tasks stands for them.
		if (s.follow != nil && s.follow(p)) || len(below) > 0 {
			return
		}
	}
}

// encloses reports whether p is the task being scanned or a struct it sits in.
func (s *refScan) encloses(p cue.Path) bool {
	ps := p.Selectors()
	if len(ps) > len(s.anc) {
		return false
	}
	for i := range ps {
		if ps[i].String() != s.anc[i].String() {
			return false
		}
	}
	return true
}

// refChain flattens a.b["c"].d into the name a and the selectors b, c and d.
// The selectors stop at the first one that is not a name or a string. dyn
// holds the expressions of the indexes it could not read.
func refChain(e ast.Expr) (head *ast.Ident, sels []cue.Selector, dyn []ast.Expr) {
	done := false
	var walk func(e ast.Expr) bool
	walk = func(e ast.Expr) bool {
		switch x := e.(type) {
		case *ast.Ident:
			head = x
			return true
		case *ast.ParenExpr:
			return walk(x.X)
		case *ast.SelectorExpr:
			if !walk(x.X) {
				return false
			}
			if done {
				return true
			}
			switch l := x.Sel.(type) {
			case *ast.Ident:
				sels = append(sels, cue.Str(l.Name))
			case *ast.BasicLit:
				if u, err := strconv.Unquote(l.Value); err == nil {
					sels = append(sels, cue.Str(u))
					return true
				}
				done = true
			default:
				done = true
			}
			return true
		case *ast.IndexExpr:
			if !walk(x.X) {
				return false
			}
			if done {
				dyn = append(dyn, x.Index)
				return true
			}
			if l, ok := x.Index.(*ast.BasicLit); ok {
				if u, err := strconv.Unquote(l.Value); err == nil {
					sels = append(sels, cue.Str(u))
					return true
				}
			}
			done = true
			dyn = append(dyn, x.Index)
			return true
		}
		return false
	}
	if !walk(e) {
		return nil, nil, nil
	}
	return head, sels, dyn
}

// letOf returns the let that binds a name, or nil when the name is not bound by
// one. The parser records the binding of every name it has resolved.
func letOf(id *ast.Ident) *ast.LetClause {
	if id == nil {
		return nil
	}
	if lc, ok := id.Node.(*ast.LetClause); ok {
		return lc
	}
	if lc, ok := id.Scope.(*ast.LetClause); ok {
		return lc
	}
	return nil
}

// letReference returns the let that an expression names at its head: _x, _x.id
// or _x["id"] where _x is bound by a let. It is nil for any other node. It is
// called for every node of the syntax that is scanned, so it allocates nothing.
func letReference(n ast.Node) *ast.LetClause {
	var e ast.Expr
	switch x := n.(type) {
	case *ast.Ident:
		return letOf(x)
	case *ast.SelectorExpr:
		e = x.X
	case *ast.IndexExpr:
		e = x.X
	default:
		return nil
	}
	for {
		switch y := e.(type) {
		case *ast.Ident:
			return letOf(y)
		case *ast.SelectorExpr:
			e = y.X
		case *ast.IndexExpr:
			e = y.X
		case *ast.ParenExpr:
			e = y.X
		default:
			return nil
		}
	}
}

// loopReference returns the for clause that binds the name that src is, or
// that src is the value of a field of; nil if src is not such a name.
func loopReference(src ast.Node) *ast.ForClause {
	if f, ok := src.(*ast.Field); ok {
		src = f.Value
	}
	id, ok := src.(*ast.Ident)
	if !ok {
		return nil
	}
	if fc, ok := id.Node.(*ast.ForClause); ok {
		return fc
	}
	if fc, ok := id.Scope.(*ast.ForClause); ok {
		return fc
	}
	return nil
}
