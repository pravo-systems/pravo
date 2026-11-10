package convert

import (
	"errors"
	"fmt"
	"sort"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/tools/flow"
)

// The dependencies of a task come from two readers of the specification.
//
// The first is the analysis of the syntax (Dependencies). It follows every
// expression of a task body to the tasks it names. A resource that a
// comprehension makes, one for each element of a collection of resources, reads
// its own element through the loop variable, and the syntax does not say which
// element that is: the analysis takes the whole collection. A collection of n
// resources and a loop over it then make n times n edges.
//
// The second is CUE's evaluator, through the graph of the package
// cuelang.org/go/tools/flow (its graph only, never its runner). It resolves
// every reference in the environment the evaluator built for it, so it knows the
// element a loop variable was bound to. It does not see everything: a container
// of tasks named as a whole (json.Marshal(subnets), $depends_on: [subnets]), a
// task that is nothing but a reference to a definition, and a value that is
// shared with another are not attributed to the tasks a reader of the
// specification means.
//
// The graph is the union of both, with one difference to the syntax alone: where
// a task is made inside a loop over a container of tasks, and the conditions in
// Loops.Read hold, the loop variable stands for the elements the
// evaluator found and not for the whole container. Everything else is the
// analysis of the syntax as it was, and it is all of it whenever the evaluator's
// graph cannot be had.

// Loops is the second reader for one specification: what the evaluator says
// each task reads, and what it settles about the loops that make tasks. The
// engine and the viewer build their graphs with it, so they show the same edges.
// It is not safe for use by several goroutines at once.
type Loops struct {
	resolve Resolver
	// reads holds what the evaluator's graph says each of its tasks reads, by the
	// path of the task, in the order of the paths.
	reads map[string][]cue.Path
	// elems holds, for a task and a for clause around its declaration, the
	// elements of the clause's source that the task's iterations were bound to.
	elems map[loopKey][]cue.Path
	// whole holds the loops that are read as the syntax reads them.
	whole map[loopKey]bool
	// containers is the memo of containerAt, by path; nil for a container that
	// does not qualify.
	containers map[string]*loopContainer
	// decl holds the declaration of each task that has a loop read by element,
	// by path.
	decl map[string]ast.Node

	// task is the path of the task whose own body is being analysed, field its
	// declaration; cand and order are the loops it read so far.
	task  string
	field ast.Node
	cand  map[loopKey]*loopCandidate
	order []loopKey

	// Narrowed and Unconfirmed count the loops that were read by element and
	// the ones the evaluator did not confirm an element for.
	Narrowed, Unconfirmed int
}

// loopKey names the iterations of one for clause that made one task.
type loopKey struct {
	task   string // the path of the task
	clause *ast.ForClause
}

// loopContainer is a container that holds tasks and nothing else, each with a
// node of its own in the evaluator.
type loopContainer struct {
	children map[string]bool // by path
}

// loopCandidate is a loop whose variable the task being analysed reads.
type loopCandidate struct {
	container *loopContainer
	// own is set by a read of the value variable itself or of one field of it
	// (v, v.id): the evaluator attributes such a read to the element.
	own bool
	// value is set by any read of the value variable. A loop of which only the
	// key is read (for k, _ in subnets, with subnets[k].id in the body) reads
	// nothing of the collection through its variable.
	value bool
	// stray is set by a read that is not written inside the declaration of the
	// task: it may belong to another iteration.
	stray bool
}

// NewLoops reads the dependencies between the tasks of root from CUE's
// evaluator. A task is a path that resolve names as one. It fails when the
// evaluator's graph cannot be had or holds no task; the caller then works from
// the syntax alone. Only the graph of the flow package is used. Its runner
// evaluates the whole specification again after every task and is never
// started.
func NewLoops(root cue.Value, resolve Resolver) (l *Loops, err error) {
	defer func() {
		if x := recover(); x != nil {
			l, err = nil, fmt.Errorf("the evaluator's dependency graph failed: %v", x)
		}
	}()
	isTask := func(p cue.Path) bool {
		task, _, ok := resolve.Task(p)
		return ok && task.String() == p.String()
	}
	run := flow.RunnerFunc(func(*flow.Task) error { return nil })
	c := flow.New(&flow.Config{}, root, func(v cue.Value) (flow.Runner, error) {
		if isTask(v.Path()) {
			return run, nil
		}
		return nil, nil
	})
	tasks := c.Tasks()
	if len(tasks) == 0 {
		return nil, errors.New("the evaluator's dependency graph has no task")
	}
	l = &Loops{
		resolve: resolve, reads: make(map[string][]cue.Path, len(tasks)),
		elems: map[loopKey][]cue.Path{}, whole: map[loopKey]bool{}, containers: map[string]*loopContainer{}, decl: map[string]ast.Node{},
	}
	for _, t := range tasks {
		p := t.Path()
		if !isTask(p) {
			continue
		}
		var deps []cue.Path
		seen := map[string]bool{p.String(): true}
		for _, d := range t.Dependencies() {
			if dp := d.Path(); isTask(dp) && !seen[dp.String()] {
				seen[dp.String()] = true
				deps = append(deps, dp)
			}
		}
		sort.Slice(deps, func(i, j int) bool { return deps[i].String() < deps[j].String() })
		l.reads[p.String()] = deps
	}
	return l, nil
}

// Tasks is the number of tasks that the evaluator's graph has.
func (l *Loops) Tasks() int { return len(l.reads) }

// ByElement reports whether a loop that makes the task at p is read by element.
func (l *Loops) ByElement(p cue.Path) bool {
	_, ok := l.decl[p.String()]
	return ok
}

// Dependencies is the package's Dependencies for the body v of the task at
// self, with what the evaluator adds: the loops that qualify are read by
// element, and the tasks the evaluator found are among the result. resolve must
// answer LoopRead with the Read of l.
func (l *Loops) Dependencies(v cue.Value, self cue.Path, resolve Resolver) (deps []cue.Path, aliases []Alias) {
	reads, inFlow := l.reads[self.String()]
	if !inFlow {
		return Dependencies(v, self, resolve)
	}
	l.task, l.field = self.String(), v.Source()
	l.cand, l.order = map[loopKey]*loopCandidate{}, nil
	deps, aliases = Dependencies(v, self, resolve)
	if l.decide(reads) {
		// A loop that was left out is read whole after all.
		deps, aliases = Dependencies(v, self, resolve)
		l.decide(nil)
	}
	l.task, l.field, l.cand, l.order = "", nil, nil, nil
	seen := make(map[string]bool, len(deps))
	for _, d := range deps {
		seen[d.String()] = true
	}
	for _, d := range reads {
		if !seen[d.String()] {
			deps = append(deps, d)
		}
	}
	return deps, aliases
}

// Read tells the analysis what a loop variable stands for in the body of a
// task: the elements the evaluator found, or nothing better than the source of
// the clause (false). The variable stands for elements when all of this holds:
//
//   - the read is written inside the declaration of the task, and the clause
//     is written around that declaration, so the read belongs to an iteration
//     that made the task;
//   - the source of the clause names a container in which every element is a
//     task with a node of its own (containerAt);
//   - the body reads the value variable itself or one field of it at least
//     once (v, v.id), which the evaluator always attributes to the element, or
//     it reads the key alone, which reads nothing of the collection;
//   - the evaluator's graph has the task, and at least one element of the
//     container among what the task reads.
//
// The last two are known only after the body was read, so while Dependencies
// reads a body a read that may qualify is left out and decide settles it.
func (l *Loops) Read(rd LoopRead) ([]cue.Path, bool) {
	if l == nil || rd.Self == nil {
		return nil, false
	}
	key := loopKey{rd.Self.String(), rd.Clause}
	if elems, ok := l.elems[key]; ok {
		if !writtenIn(l.decl[key.task], rd.Name) {
			return nil, false
		}
		return elems, true
	}
	if l.task == "" || l.whole[key] || l.task != key.task {
		return nil, false
	}
	c := l.cand[key]
	if c == nil {
		c = &loopCandidate{}
		if l.field != nil && writtenBefore(rd.Clause, l.field) {
			c.container = l.containerAt(rd.Clause, *rd.Self)
		}
		if c.container == nil {
			l.whole[key] = true
			return nil, false
		}
		l.cand[key] = c
		l.order = append(l.order, key)
	}
	if !rd.Key {
		c.value = true
	}
	switch {
	case !writtenIn(l.field, rd.Name):
		c.stray = true
	case !rd.Key && !rd.Nested && rd.Depth <= 1:
		c.own = true
	}
	return nil, true
}

// decide settles the loops that the body of the task read: the elements the
// evaluator found among what the task reads, or the whole source. again is true
// when a loop was left out of the analysis and has to be read whole after all.
func (l *Loops) decide(reads []cue.Path) (again bool) {
	for _, key := range l.order {
		c := l.cand[key]
		var elems []cue.Path
		certain := !c.stray && (c.own || !c.value)
		if certain {
			for _, d := range reads {
				if c.container.children[d.String()] {
					elems = append(elems, d)
				}
			}
		}
		if len(elems) == 0 {
			if certain && c.value {
				l.Unconfirmed++
			}
			l.whole[key] = true
			again = true
			continue
		}
		l.elems[key] = elems
		l.decl[key.task] = l.field
		l.Narrowed++
	}
	l.cand, l.order = map[loopKey]*loopCandidate{}, nil
	return again
}

// containerAt returns the container that the source of fc names for the task
// at self, when every element of it is a task that the evaluator's graph has
// and that has a node of its own. A task that is nothing but a reference
// (x: #Thing, x: _template) shares the node of what it refers to, and a read of
// it is not attributed to it. A container that holds a plain value or a struct
// of tasks next to its tasks is read whole as well: such an element is read
// through what it is built from.
func (l *Loops) containerAt(fc *ast.ForClause, self cue.Path) *loopContainer {
	p, ok := LoopContainer(fc, self, l.resolve)
	if !ok {
		return nil
	}
	key := p.String()
	if c, ok := l.containers[key]; ok {
		return c
	}
	l.containers[key] = nil
	tasks := l.resolve.Below(p)
	depth := len(p.Selectors()) + 1
	c := &loopContainer{children: make(map[string]bool, len(tasks))}
	for _, t := range tasks {
		if _, inFlow := l.reads[t.String()]; len(t.Selectors()) != depth || !inFlow {
			return nil
		}
		if _, ref := l.resolve.Value(t).ReferencePath(); len(ref.Selectors()) > 0 {
			return nil
		}
		c.children[t.String()] = true
	}
	v, elements := l.resolve.Value(p), 0
	switch v.IncompleteKind() {
	case cue.StructKind:
		it, err := v.Fields()
		if err != nil {
			return nil
		}
		for it.Next() {
			elements++
		}
	case cue.ListKind:
		it, err := v.List()
		if err != nil {
			return nil
		}
		for it.Next() {
			elements++
		}
	default:
		return nil
	}
	if elements != len(tasks) {
		return nil
	}
	l.containers[key] = c
	return c
}

// writtenIn reports whether the name is written inside the declaration f.
func writtenIn(f ast.Node, id *ast.Ident) bool {
	if f == nil || id == nil {
		return false
	}
	from, to, at := f.Pos(), f.End(), id.Pos()
	if !from.IsValid() || !to.IsValid() || !at.IsValid() || from.Filename() != at.Filename() {
		return false
	}
	return from.Offset() <= at.Offset() && at.Offset() < to.Offset()
}

// writtenBefore reports whether the for clause is written ahead of the declaration f in
// the same file. A clause that binds a name read inside f and that is written
// ahead of f is written around f.
func writtenBefore(fc *ast.ForClause, f ast.Node) bool {
	a, b := fc.Pos(), f.Pos()
	return a.IsValid() && b.IsValid() && a.Filename() == b.Filename() && a.Offset() < b.Offset()
}
