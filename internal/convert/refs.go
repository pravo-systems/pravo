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

// Memo is what a Resolver can offer to spare the analysis of a plain field that
// is read by many tasks (a shared setting): whether following it reaches a task
// is the same every time, so it is found once. The map holds the paths that
// were followed and reached nothing. A resolver offers one only while the values
// it reads do not change.
type Memo interface {
	InertFields() map[string]InertField
}

// InertField is what following a field gave: whether it was followed, and the
// non-concrete field it first met.
type InertField struct {
	Followed   bool
	Incomplete *cue.Path
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

// FieldObserver is a Resolver that wants to know in which field of a task body the
// references that it resolves are read. Dependencies tells it the name of the
// top-level field before it analyses what is below it, and the empty name for the
// body itself, so that one walk gives both the tasks a body reads and the field
// that reads each.
type FieldObserver interface {
	SetField(name string)
}

// LoopRead is one place where the syntax reads a variable that a for clause
// binds: v or k in "for k, v in subnets", read as v, v.id or v.tags.Name.
type LoopRead struct {
	// Clause is the for clause that binds the variable.
	Clause *ast.ForClause
	// Name is the variable where it is read.
	Name *ast.Ident
	// Key is set when the variable is the key of the clause and not its value.
	Key bool
	// Depth counts the selectors that follow the name: 0 for v, 1 for v.id.
	Depth int
	// Nested is set for a read that was found in the syntax of a comprehension
	// or of a let, and not in the expression tree of the value.
	Nested bool
	// Self is the task whose body is analysed, nil when the value is no task's.
	Self *cue.Path
}

// LoopReader is a Resolver that knows which elements a loop variable stands for
// in the body of a task. A resource that a comprehension over a collection of
// resources makes reads, through the variable, the one element its iteration was
// bound to. The syntax does not say which one that is, so without a LoopReader
// the variable stands for the whole source of its clause. The evaluator knows:
// see the engine, which reads the element from CUE's own dependency analysis.
type LoopReader interface {
	// LoopRead returns the tasks the read stands for and true. It returns false
	// when it knows nothing better than the source of the clause, which the
	// analysis then reads whole.
	LoopRead(rd LoopRead) ([]cue.Path, bool)
}

// HasTaskRef reports whether the expression depends on another task.
func (r RefInfo) HasTaskRef() bool { return len(r.TaskRefs) > 0 }

// AnalyseWith walks the expression tree of v (through unification,
// interpolation, calls, operators and literal lists/structs in expression
// position) and reports references to tasks and to incomplete non-task
// values. References into self are ignored.
func AnalyseWith(v cue.Value, resolve Resolver, self *cue.Path) RefInfo {
	return analyse(v, resolve, self, false)
}

// analyse is AnalyseWith. With deep set it follows a reference to a plain field
// whether or not that field is concrete yet: once a task has filled in its
// values, a field built from it is concrete, and only the chain of references
// still says where the value came from.
func analyse(v cue.Value, resolve Resolver, self *cue.Path, deep bool) RefInfo {
	if resolve == nil {
		resolve = ResolverFunc(func(cue.Path) (cue.Path, cue.Path, bool) { return cue.Path{}, cue.Path{}, false })
	}
	a := &analysis{
		resolve: resolve, self: self, deep: deep,
		seen: map[string]bool{}, tasks: map[string]bool{}, aliasSeen: map[string]bool{},
		scanned: map[ast.Node]bool{}, letSeen: map[*ast.LetClause]bool{}, loopSeen: map[*ast.ForClause]bool{},
	}
	if m, ok := resolve.(Memo); ok {
		a.inert = m.InertFields()
	}
	a.loops, _ = resolve.(LoopReader)
	a.walk(v, 0, false)
	return a.info
}

// analysis is one walk of analyse: what it found, and what it has visited.
type analysis struct {
	resolve Resolver
	self    *cue.Path
	deep    bool

	info RefInfo
	// seen holds the references and the followed fields that were read, tasks and
	// aliasSeen what is in info already.
	seen, tasks, aliasSeen map[string]bool
	// inert is the memo of the resolver, nil when it keeps none.
	inert map[string]InertField
	// loops is the resolver when it knows what a loop variable stands for.
	loops LoopReader
	// touched counts the tasks that were reached, those of self among them.
	touched int
	scanned map[ast.Node]bool
	// letSeen holds the lets whose expression has been read: a let that names a
	// task, or a value built from tasks, is a dependency on them wherever it is
	// used, and one that is used twice is read once.
	letSeen  map[*ast.LetClause]bool
	loopSeen map[*ast.ForClause]bool
}

func (a *analysis) addTask(task, alias cue.Path) {
	a.touched++
	if a.self != nil && task.String() == a.self.String() {
		return
	}
	if !a.tasks[task.String()] {
		a.tasks[task.String()] = true
		a.info.TaskRefs = append(a.info.TaskRefs, task)
	}
	if alias.String() != task.String() && !a.aliasSeen[alias.String()] {
		a.aliasSeen[alias.String()] = true
		a.info.Aliases = append(a.info.Aliases, Alias{Path: alias, Task: task})
	}
}

// follow reads the plain field at p that a comprehension names. Whatever
// the field is built from may be a task, as for a reference in the
// expression tree. It reports whether the specification has such a field.
func (a *analysis) follow(p cue.Path, depth int) bool {
	target := a.resolve.Value(p)
	if !target.Exists() {
		return false
	}
	if key := "follow " + p.String(); !a.seen[key] {
		a.seen[key] = true
		if !IsConcrete(target) {
			a.walk(target, depth+1, false)
		}
	}
	return true
}

// scanSyntax reads the tasks that the syntax at node names.
func (a *analysis) scanSyntax(node ast.Node) {
	var anc []cue.Selector
	if a.self != nil {
		anc = a.self.Selectors()
	}
	sc := &refScan{resolve: a.resolve, anc: anc, add: a.addTask, follow: func(p cue.Path) bool { return a.follow(p, 0) }, local: map[string]bool{}, let: a.scanLet, loop: a.loopVariable}
	ast.Walk(node, sc.visit, nil)
}

// loopVariable reads a variable that a for clause binds. Where the resolver
// knows the element the variable stands for, that element is read. It is the
// source of the clause otherwise, read for the tasks it names: CUE shows no
// path for the variable.
func (a *analysis) loopVariable(rd LoopRead) {
	if a.loops != nil {
		rd.Self = a.self
		if tasks, ok := a.loops.LoopRead(rd); ok {
			for _, t := range tasks {
				a.addTask(t, t)
			}
			return
		}
	}
	if fc := rd.Clause; fc.Source != nil && !a.loopSeen[fc] {
		a.loopSeen[fc] = true
		a.scanSyntax(fc.Source)
	}
}

func (a *analysis) scanLet(lc *ast.LetClause) {
	if a.letSeen[lc] || lc.Expr == nil {
		return
	}
	a.letSeen[lc] = true
	a.scanSyntax(lc.Expr)
}

// walk visits v. A value that is a reference has operands, the container
// and the key of ports[z], that CUE used to reach what it refers to; they
// are the way to the value, and the value alone is read. nav is set for
// them, so that a container among them is not a reference to all of its
// tasks.
func (a *analysis) walk(v cue.Value, depth int, nav bool) {
	if depth > 64 {
		return
	}
	// A name bound by a let is not a path in the specification: the value
	// behind it is what the let was set to, and reaching it through the
	// expression tree reads that value's own body, which would make the task
	// depend on what its dependency depends on. The expression of the let
	// is read instead.
	if lc := letReference(v.Source()); lc != nil {
		a.scanLet(lc)
		return
	}
	// A loop variable that the resolver knows the element of stands for that
	// element, whatever selectors follow it.
	if a.loops != nil {
		if rd, ok := loopRead(v.Source()); ok {
			rd.Self = a.self
			if tasks, ok := a.loops.LoopRead(rd); ok {
				for _, t := range tasks {
					a.addTask(t, t)
				}
				return
			}
		}
	}
	// A loop variable on its own is bound by the source of its for clause.
	if fc := loopReference(v.Source()); fc != nil && fc.Source != nil {
		if !a.loopSeen[fc] {
			a.loopSeen[fc] = true
			a.scanSyntax(fc.Source)
		}
		return
	}
	root, p := v.ReferencePath()
	isRef := root.Exists() && len(p.Selectors()) > 0
	if isRef {
		a.reference(root, p, depth, nav)
	}
	op, args := v.Expr()
	if op == cue.NoOp {
		// The expression tree stops at a comprehension: what its clauses
		// and its body refer to is found in the syntax.
		scanComprehensions(v.Source(), a.resolve, a.self, a.scanned, a.addTask, func(p cue.Path) bool { return a.follow(p, depth) }, a.scanLet, a.loopVariable)
		switch {
		case v.IncompleteKind() == cue.ListKind:
			if it, err := v.List(); err == nil {
				for it.Next() {
					a.walk(it.Value(), depth+1, false)
				}
			}
		case v.IncompleteKind() == cue.StructKind && depth > 0:
			if it, err := v.Fields(); err == nil {
				for it.Next() {
					a.walk(it.Value(), depth+1, false)
				}
			}
		}
		return
	}
	for _, arg := range args {
		a.walk(arg, depth+1, isRef)
	}
}

// reference reads the reference to p under root, once: a task, or a plain field.
func (a *analysis) reference(root cue.Value, p cue.Path, depth int, nav bool) {
	key := p.String()
	if nav {
		key = "operand " + key
	}
	if a.seen[key] {
		return
	}
	a.seen[key] = true
	if task, alias, ok := a.resolve.Task(p); ok {
		a.addTask(task, alias)
		return
	}
	// Anything but a reference to a struct the task sits inside
	// (the instance of a definition), whose inner references are
	// reported on their own paths.
	if a.self != nil && isPrefixPath(p, *a.self) {
		return
	}
	// A plain field. A container of tasks stands for every
	// task in it. If its value is not concrete, whatever it
	// is built from may be a task, so follow it.
	var below []cue.Path
	if !nav {
		below = a.resolve.Below(p)
	}
	for _, t := range below {
		a.addTask(t, t)
	}
	// An operand of a reference is not Followed: the reference
	// it belongs to names the path that is read, and that path is
	// followed. Following the container would take every task
	// below it as a source and validate everything it holds.
	if target := root.LookupPath(p); !nav && target.Exists() {
		a.plainField(p, target, depth, len(below) == 0)
	}
}

// plainField follows the plain field at p, whose value is target, when it may be built
// from a task, and notes a field that is not concrete and reaches none. alone is set when
// no task sits below the field.
func (a *analysis) plainField(p cue.Path, target cue.Value, depth int, alone bool) {
	// What following a field reaches is the same for every
	// task that reads it, when it reaches no task. The key
	// holds where the field is declared, since the same path
	// in two packages is two fields.
	memoKey := ""
	if a.inert != nil {
		memoKey = fmt.Sprintf("%v %s %s", a.deep, p, target.Pos())
	}
	before := len(a.info.TaskRefs)
	followed := false
	if m, ok := a.inert[memoKey]; ok && memoKey != "" {
		followed = m.Followed
		if followed && a.info.Incomplete == nil {
			a.info.Incomplete = m.Incomplete
		}
	} else {
		d, _ := target.Default()
		if a.deep || d.Validate(cue.Concrete(true)) != nil {
			followed = true
			touchedBefore, incBefore := a.touched, a.info.Incomplete
			a.walk(target, depth+1, false)
			if memoKey != "" && a.touched == touchedBefore && incBefore == nil && len(a.info.TaskRefs) == before {
				a.inert[memoKey] = InertField{Followed: true, Incomplete: a.info.Incomplete}
			}
		} else if memoKey != "" {
			a.inert[memoKey] = InertField{}
		}
	}
	if followed && !a.deep && alone && len(a.info.TaskRefs) == before && a.info.Incomplete == nil {
		pp := p
		a.info.Incomplete = &pp
	}
}

// Dependencies walks every field and element of a task body and reports all
// tasks it references, whether or not the referenced values are concrete,
// together with the alias prefixes used to reach them.
func Dependencies(v cue.Value, self cue.Path, resolve Resolver) (deps []cue.Path, aliases []Alias) {
	return dependencies(v, self, resolve, false)
}

// SourceTasks is Dependencies, but follows every reference to a plain field to
// what the field is built from, concrete or not. It answers which tasks a value
// came from after their results were filled in.
func SourceTasks(v cue.Value, self cue.Path, resolve Resolver) []cue.Path {
	deps, _ := dependencies(v, self, resolve, true)
	return deps
}

func dependencies(v cue.Value, self cue.Path, resolve Resolver, deep bool) (deps []cue.Path, aliases []Alias) {
	seen := map[string]bool{}
	aliasSeen := map[string]bool{}
	obs, _ := resolve.(FieldObserver)
	if obs != nil {
		obs.SetField("")
		defer obs.SetField("")
	}
	var visit func(v cue.Value, depth int)
	visit = func(v cue.Value, depth int) {
		if depth > 32 {
			return
		}
		info := analyse(v, resolve, &self, deep)
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
			// The hidden fields and the definitions of a body are read too: a
			// field of the body may be set from one (_id: net.id, subnet: _id),
			// and such a reference stays inside the body.
			if it, err := v.Fields(cue.Hidden(true), cue.Definitions(true)); err == nil {
				for it.Next() {
					if obs != nil && depth == 0 {
						if sel := it.Selector(); sel.LabelType() == cue.StringLabel {
							obs.SetField(sel.Unquoted())
						} else {
							obs.SetField("")
						}
					}
					visit(it.Value(), depth+1)
					if obs != nil && depth == 0 {
						obs.SetField("")
					}
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
		return fmt.Errorf("%s: value depends on %s, which is not concrete (give it a value, or pass -t if it is a tag)", path, info.Incomplete)
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
//
// loop is called for a variable that a for clause outside the comprehension
// binds, as let is for a name that a let outside it binds.
func scanComprehensions(src ast.Node, resolve Resolver, self *cue.Path, scanned map[ast.Node]bool, add func(task, alias cue.Path), follow func(p cue.Path) bool, let func(*ast.LetClause), loop func(LoopRead)) {
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
			s := &refScan{resolve: resolve, anc: anc, add: add, follow: follow, local: map[string]bool{}, let: let, loop: loop}
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
	// clauses holds the for clauses of the syntax being scanned, and loop reads
	// a variable that a for clause outside it binds: the variable of the loop
	// that makes the task, read in a condition or in a comprehension of its
	// body, or in a let.
	clauses map[*ast.ForClause]bool
	loop    func(LoopRead)
	// dynamic counts the computed indexes of the chain being resolved.
	dynamic int
}

func (s *refScan) visit(n ast.Node) bool {
	switch x := n.(type) {
	case *ast.ForClause:
		if s.clauses == nil {
			s.clauses = map[*ast.ForClause]bool{}
		}
		s.clauses[x] = true
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
		s.dynamic = len(dyn)
		s.reference(head, sels)
		s.dynamic = 0
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
	// The parser knows the clause that binds a loop variable, whatever else
	// has the same name.
	if fc := forClauseOf(head); fc != nil {
		if !s.clauses[fc] && s.loop != nil {
			s.loop(LoopRead{
				Clause: fc, Name: head, Nested: true,
				Key:   fc.Key != nil && fc.Value != nil && fc.Key.Name == head.Name,
				Depth: len(sels) + s.dynamic,
			})
		}
		return
	}
	if s.local[head.Name] {
		return
	}
	if lc := letOf(head); lc != nil {
		if s.let != nil {
			s.let(lc)
		}
		return
	}
	first, last, ok := scopeRange(head, len(s.anc))
	if !ok {
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
	return forClauseOf(id)
}

// forClauseOf returns the for clause that binds a name, or nil.
func forClauseOf(id *ast.Ident) *ast.ForClause {
	if id == nil {
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

// loopRead describes src when it is a loop variable followed by selectors that
// are all names or literal strings: v, v.id, v["id"], or the value of a field
// that is one of these. An index that is computed (v[other.id]) is not such a
// read: what it reads besides the element is found by the walk of its parts.
func loopRead(src ast.Node) (LoopRead, bool) {
	if f, ok := src.(*ast.Field); ok {
		src = f.Value
	}
	e, ok := src.(ast.Expr)
	if !ok {
		return LoopRead{}, false
	}
	head, sels, dyn := refChain(e)
	if head == nil || len(dyn) > 0 {
		return LoopRead{}, false
	}
	fc := forClauseOf(head)
	if fc == nil || fc.Source == nil {
		return LoopRead{}, false
	}
	return LoopRead{Clause: fc, Name: head, Key: fc.Key != nil && fc.Value != nil && fc.Key.Name == head.Name, Depth: len(sels)}, true
}

// LoopContainer returns the container of tasks that the source of fc names, for
// a task at self: the path p that the scan of the source resolves the name to
// when it is neither a task nor built from one, and tasks sit below it. It is
// false for any other source: an attribute of a task, a plain field, a let, a
// call, another loop's variable.
func LoopContainer(fc *ast.ForClause, self cue.Path, resolve Resolver) (cue.Path, bool) {
	if fc == nil || fc.Source == nil || resolve == nil {
		return cue.Path{}, false
	}
	head, sels, dyn := refChain(fc.Source)
	if head == nil || len(dyn) > 0 || letOf(head) != nil || forClauseOf(head) != nil {
		return cue.Path{}, false
	}
	anc := self.Selectors()
	first, last, ok := scopeRange(head, len(anc))
	if !ok {
		return cue.Path{}, false
	}
	chain := append([]cue.Selector{cue.Str(head.Name)}, sels...)
	if strings.HasPrefix(head.Name, "#") {
		chain[0] = cue.Def(head.Name)
	}
	for n := first; n >= last; n-- {
		p := cue.MakePath(append(append([]cue.Selector{}, anc[:n]...), chain...)...)
		if _, _, ok := resolve.Task(p); ok {
			return cue.Path{}, false
		}
		if isPrefixPath(p, self) {
			return cue.Path{}, false
		}
		if len(resolve.Below(p)) > 0 {
			return p, true
		}
		if resolve.Value(p).Exists() {
			return cue.Path{}, false
		}
	}
	return cue.Path{}, false
}

// scopeRange gives the structs around a task in which the name head is looked
// up, as indexes into the selectors of the task from the innermost (first) to
// the outermost (last); see refScan.reference. ok is false for a name that is no
// field: an import.
func scopeRange(head *ast.Ident, depth int) (first, last int, ok bool) {
	first, last = max(depth-1, 0), 0
	switch head.Scope.(type) {
	case *ast.ImportSpec, *ast.ForClause:
		return 0, 0, false
	case *ast.File:
		first = 0
	case *ast.StructLit:
		last = 1
	}
	if _, isImport := head.Node.(*ast.ImportSpec); isImport {
		return 0, 0, false
	}
	return first, last, true
}

// shadowedName returns the name that a struct literal in the syntax of v
// reads inside the value of a field of that name, as in {stage: stage.value}.
// CUE resolves such a name to the field itself, not to the one outside, so the
// value never settles. It returns "" if there is none.
func shadowedName(v cue.Value) string {
	src := v.Source()
	if fc := loopReference(src); fc != nil && fc.Source != nil {
		src = fc.Source
	}
	if src == nil {
		return ""
	}
	found := ""
	ast.Walk(src, func(n ast.Node) bool {
		f, ok := n.(*ast.Field)
		if !ok || found != "" {
			return found == ""
		}
		label, ok := f.Label.(*ast.Ident)
		if !ok || f.Value == nil {
			return true
		}
		ast.Walk(f.Value, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok && id.Name == label.Name {
				if _, ok := id.Scope.(*ast.StructLit); ok {
					found = id.Name
				}
			}
			return found == ""
		}, nil)
		return found == ""
	}, nil)
	return found
}
