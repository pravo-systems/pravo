package engine

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/schema"
)

// Parallelism bounds how many resources are planned or applied at once. The
// command line sets it from PRAVO_PARALLELISM before a command runs; nothing
// changes it while one does.
var Parallelism = 10

type node struct {
	res        *config.Resource
	deps       map[string]bool
	dependents []*node
	pending    int
}

// resolve maps a referenced path to the declared task it names or lies
// inside: the shortest prefix of the path at which a resource sits. A task body
// never contains another task, so the prefix is unique.
func (e *Engine) resolve(p cue.Path) (task, alias cue.Path, ok bool) {
	sels := p.Selectors()
	for i := 1; i <= len(sels); i++ {
		cand := cue.MakePath(sels[:i]...)
		if _, known := e.byPath[cand.String()]; known {
			return cand, cand, true
		}
	}
	return cue.Path{}, cue.Path{}, false
}

// below lists the tasks that sit below the path p, which is a struct or a list
// that holds tasks and is not a task itself, in the order the specification
// declares them. A reference to such a path is a reference to each of them.
func (e *Engine) below(p cue.Path) []cue.Path {
	e.belowOnce.Do(func() {
		e.belowIdx = map[string][]cue.Path{}
		for _, r := range e.Root.Resources {
			sels := r.Path.Selectors()
			for i := 1; i < len(sels); i++ {
				k := cue.MakePath(sels[:i]...).String()
				e.belowIdx[k] = append(e.belowIdx[k], r.Path)
			}
		}
	})
	key := cue.MakePath(p.Selectors()...).String()
	tasks := e.belowIdx[key]
	if len(tasks) > 0 {
		// Something reads this container: it is iterated, indexed or passed on. See
		// seedFills.
		e.iterMu.Lock()
		if e.iterated == nil {
			e.iterated = map[string]bool{}
		}
		e.iterated[key] = true
		e.iterMu.Unlock()
	}
	return tasks
}

// seedFills returns the fills that settle the first batch of a run with: one
// empty fill for each task that sits below a container that something reads, in
// the order the specification declares the tasks. The fields of a struct are in
// the order in which they were first met, and a fill meets a field before the
// comprehension that declares it does. The fills come out in the order the tasks
// finish, which is not the same from one run to the next, so a comprehension over
// the container would give the tasks in a different order each time, and a plan
// would show a value that the apply of it does not produce. The empty fills meet
// the fields first, in declaration order, and the fills of the tasks add to
// fields that exist. A task whose path holds an index or a name that is not a
// plain field is left out: a list keeps its order.
func (e *Engine) seedFills() []convert.PathFill {
	e.iterMu.Lock()
	var below []string
	for k := range e.iterated {
		below = append(below, k)
	}
	e.iterMu.Unlock()
	if len(below) == 0 {
		return nil
	}
	wanted := map[string]bool{}
	for _, k := range below {
		for _, p := range e.belowIdx[k] {
			wanted[p.String()] = true
		}
	}
	var fills []convert.PathFill
	for _, r := range e.Root.Resources {
		if !wanted[r.Path.String()] || !plainPath(r.Path) {
			continue
		}
		fills = append(fills, convert.PathFill{Path: r.Path, Value: map[string]any{}})
	}
	return fills
}

// plainPath reports whether every selector of p names a regular field.
func plainPath(p cue.Path) bool {
	for _, s := range p.Selectors() {
		if s.Type() != cue.StringLabel {
			return false
		}
	}
	return true
}

// taskResolver gives the reference analysis the tasks of an engine.
type taskResolver struct{ e *Engine }

func (r taskResolver) Task(p cue.Path) (cue.Path, cue.Path, bool) { return r.e.resolve(p) }
func (r taskResolver) Below(p cue.Path) []cue.Path                { return r.e.below(p) }

// Value reads the current value of the specification. The caller holds cueMu.
func (r taskResolver) Value(p cue.Path) cue.Value { return r.e.at(p) }

// InertFields is the memo of the analysis while the graph is built, when the
// values it reads do not change; nil otherwise.
func (r taskResolver) InertFields() map[string]convert.InertField { return r.e.inert }

// at is current.LookupPath(p). While current is still the specification as
// loaded, a declaration comes from the root's own record of them, since a
// lookup in CUE walks the fields of the parent and the passes over all
// resources would be quadratic. The caller holds cueMu.
func (e *Engine) at(p cue.Path) cue.Value {
	if !e.filled {
		if v, ok := e.Root.Found(p); ok {
			return v
		}
	}
	return e.current.LookupPath(p)
}

// resolver returns the engine's tasks as the analysis sees them.
func (e *Engine) resolver() convert.Resolver { return taskResolver{e} }

// isEphemeral reports whether the task at path p is an ephemeral resource.
func (e *Engine) isEphemeral(p cue.Path) bool {
	r, ok := e.byPath[p.String()]
	return ok && r.Mode == "ephemeral"
}

// ephemeralCheck is isEphemeral for a converter, or nil when the specification
// has no ephemeral resource: following the references of every value for one is
// work that a specification without any does not need.
func (e *Engine) ephemeralCheck() func(cue.Path) bool {
	if e.hasEphemeral {
		return e.isEphemeral
	}
	return nil
}

// atRoot is Value.LookupPath(p), from the root's record of its declarations
// when it has one.
func (e *Engine) atRoot(p cue.Path) cue.Value {
	if v, ok := e.Root.Found(p); ok {
		return v
	}
	return e.Value.LookupPath(p)
}

// buildGraph computes dependencies for every declared task from the
// references in its body, independent of whether values are known yet. A
// cycle among the tasks is an error: nothing could run first. The graph is
// built once; later walks of the engine use it.
//
// The references are read from the syntax and from CUE's evaluator; see
// loopdeps.go. If the graph that uses the evaluator has a cycle, the graph of
// the syntax alone is tried: an edge that only the evaluator finds must not stop
// a specification that worked without it.
func (e *Engine) buildGraph() (map[string]*node, error) {
	if e.graph != nil {
		return e.graph, nil
	}
	began := time.Now()
	e.cueMu.Lock()
	defer e.cueMu.Unlock()
	e.inert = map[string]convert.InertField{}
	defer func() { e.inert = nil }()
	var syntax map[string][]string
	wantEvaluator := !DependenciesFromSyntax && !e.depsFromSyntax
	if graphAudit != nil && wantEvaluator {
		// The audit compares with the syntax alone. What that reading marks as
		// iterated is put back, so that a run under the audit seeds as any other.
		e.iterMu.Lock()
		iterated := maps.Clone(e.iterated)
		e.iterMu.Unlock()
		e.graphNodes(false)
		syntax = maps.Clone(e.deps)
		e.iterMu.Lock()
		e.iterated = iterated
		e.iterMu.Unlock()
	}
	nodes, evaluator := e.graphNodes(wantEvaluator)
	err := checkCycles(nodes)
	if err != nil && evaluator {
		plog.Debug(context.Background(), "the graph with the evaluator's edges has a cycle; trying the syntax alone", "cycle", err.Error())
		nodes, evaluator = e.graphNodes(false)
		err = checkCycles(nodes)
	}
	e.graphBuilds++
	narrowed, unconfirmed := 0, 0
	if e.loops != nil {
		narrowed, unconfirmed = e.loops.Narrowed, e.loops.Unconfirmed
	}
	plog.Debug(context.Background(), "graph built", "tasks", len(nodes), "evaluator", evaluator, "loops by element", narrowed, "loops unconfirmed", unconfirmed, "took", time.Since(began).Round(time.Millisecond))
	if err != nil {
		return nil, err
	}
	e.evaluatorEdges = evaluator
	e.graph = nodes
	if syntax != nil && evaluator {
		graphAudit(e, syntax)
	}
	return nodes, nil
}

// graphAudit, when it is set, is given every graph that is built with the
// evaluator's edges, next to the edges that the syntax alone gives for the same
// specification. The tests set it and check each graph that any of them builds.
// The caller holds cueMu.
var graphAudit func(e *Engine, syntax map[string][]string)

// graphNodes reads the dependencies of every task and sets what the engine
// keeps of them (deps, readers, providerDeps, outputDeps, loops). With evaluator
// set it adds what CUE's evaluator finds and reads the loops that qualify by
// element; used reports whether it did. The caller holds cueMu.
func (e *Engine) graphNodes(evaluator bool) (nodes map[string]*node, used bool) {
	nodes = map[string]*node{}
	for _, r := range e.Root.Resources {
		nodes[r.Address()] = &node{res: r, deps: map[string]bool{}}
	}
	e.loops = nil
	bodyDeps := make(map[string][]string, len(e.Root.Resources))
	if evaluator {
		t := time.Now()
		loops, err := convert.NewLoops(e.Value, e.resolver())
		if err != nil {
			plog.Debug(context.Background(), "dependencies come from the syntax alone", "reason", err.Error())
		} else {
			e.loops, used = loops, true
			plog.Debug(context.Background(), "evaluator dependencies", "tasks", loops.Tasks(), "took", time.Since(t).Round(time.Millisecond))
		}
	}
	for _, r := range e.Root.Resources {
		v := e.at(r.Path)
		if f, ok := v.Source().(*ast.Field); ok {
			if e.fieldSrc == nil {
				e.fieldSrc = map[string]*ast.Field{}
			}
			e.fieldSrc[r.Address()] = f
		}
		var deps []cue.Path
		if e.loops != nil {
			deps, _ = e.loops.Dependencies(v, r.Path, e.resolver())
		} else {
			deps, _ = convert.Dependencies(v, r.Path, e.resolver())
		}
		n := nodes[r.Address()]
		for _, d := range deps {
			if dr, ok := e.byPath[d.String()]; ok && dr != r {
				n.deps[dr.Address()] = true
			}
		}
		own := make([]string, 0, len(n.deps))
		for d := range n.deps {
			own = append(own, d)
		}
		sort.Strings(own)
		bodyDeps[r.Address()] = own
	}
	e.bodyDeps = bodyDeps
	// Resources run after whatever their provider's configuration references.
	e.providerDeps = e.providerDepsOf()
	for key, pdeps := range e.providerDeps {
		for _, r := range e.Root.Resources {
			if r.ProviderKey != key {
				continue
			}
			n := nodes[r.Address()]
			for _, d := range pdeps {
				if d != r.Address() {
					n.deps[d] = true
				}
			}
		}
	}
	readers := map[string]bool{}
	for addr, n := range nodes {
		var deps []string
		for d := range n.deps {
			deps = append(deps, d)
			dn := nodes[d]
			dn.dependents = append(dn.dependents, n)
			readers[d] = true
		}
		sort.Strings(deps)
		e.deps[addr] = deps
		n.pending = len(n.deps)
		plog.Trace(context.Background(), "graph node", "addr", addr, "deps", strings.Join(deps, ","))
	}
	// A provider is set up from the tasks its configuration reads, and an output
	// is the value of the tasks it reads: they are read as well.
	for _, pdeps := range e.providerDeps {
		for _, d := range pdeps {
			readers[d] = true
		}
	}
	outputDeps := e.outputReadsLocked()
	for _, reads := range outputDeps {
		for _, d := range reads {
			readers[d] = true
		}
	}
	e.readers, e.outputDeps = readers, outputDeps
	return nodes, used
}

// outputReadsLocked lists, per output, the addresses of the tasks its value
// reads, sorted. The reads are worked out once for the engine: the graph, the
// check of the outputs and the destroy of a part of the state all use them. The
// caller holds cueMu.
func (e *Engine) outputReadsLocked() map[string][]string {
	if e.outputReads != nil {
		return e.outputReads
	}
	reads := make(map[string][]string, len(e.Root.Outputs))
	for _, o := range e.Root.Outputs {
		p := o.ValuePath()
		deps, _ := convert.Dependencies(e.atRoot(p), p, e.resolver())
		var addrs []string
		for _, d := range deps {
			if dr, ok := e.byPath[d.String()]; ok {
				addrs = append(addrs, dr.Address())
			}
		}
		sort.Strings(addrs)
		reads[o.Name] = slices.Compact(addrs)
	}
	e.outputReads = reads
	return reads
}

// CheckGraph builds the dependency graph of the root and reports a cycle in
// it. It needs neither state nor providers, so "pravo validate" can use it.
//
// With the schemas of the providers it also reports a value that is not concrete: an
// attribute that needs a value and has none, which a plan would stop at. A value that
// depends on another resource is not one, since it is known when that resource is.
func CheckGraph(root *config.Root, value cue.Value, schemas map[string]*schema.ProviderSchema) error {
	e := &Engine{Root: root, Value: value, cueState: cueState{current: value}, deps: map[string][]string{}, byPath: map[string]*config.Resource{}, Schemas: schemas}
	for _, r := range root.Resources {
		e.byPath[r.Path.String()] = r
		if r.Mode == "ephemeral" {
			e.hasEphemeral = true
		}
	}
	if _, err := e.buildGraph(); err != nil {
		return err
	}
	if schemas == nil {
		return nil
	}
	var errs []string
	for _, r := range root.Resources {
		if r.Mode == "ephemeral" || r.Mode == "call" {
			continue
		}
		s, err := e.resourceSchema(r)
		if err != nil {
			continue
		}
		conv := &convert.Converter{Self: r.Path, Resolve: e.resolver(), IsEphemeral: e.ephemeralCheck(), AllowEphemeral: true}
		conv.Block(r.Address(), s.Block, e.at(r.Path))
		if err := conv.Err(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n"))
	}
	return nil
}

// checkCycles reports the tasks that wait for each other. Tasks that merely
// depend on a cycle are not named: they are not part of it.
func checkCycles(nodes map[string]*node) error {
	waiting := make(map[string]int, len(nodes))
	var ready []string
	for addr, n := range nodes {
		waiting[addr] = len(n.deps)
		if len(n.deps) == 0 {
			ready = append(ready, addr)
		}
	}
	for len(ready) > 0 {
		addr := ready[len(ready)-1]
		ready = ready[:len(ready)-1]
		delete(waiting, addr)
		for _, d := range nodes[addr].dependents {
			a := d.res.Address()
			if waiting[a]--; waiting[a] == 0 {
				ready = append(ready, a)
			}
		}
	}
	if len(waiting) == 0 {
		return nil
	}
	// What is left is the tasks on cycles and the tasks behind them. The
	// strongly connected groups of two or more are the cycles.
	var (
		index   = map[string]int{}
		low     = map[string]int{}
		onStack = map[string]bool{}
		stack   []string
		cycles  [][]string
		next    int
		visit   func(addr string)
	)
	visit = func(addr string) {
		index[addr], low[addr] = next, next
		next++
		stack = append(stack, addr)
		onStack[addr] = true
		deps := make([]string, 0, len(nodes[addr].deps))
		for d := range nodes[addr].deps {
			if _, left := waiting[d]; left {
				deps = append(deps, d)
			}
		}
		sort.Strings(deps)
		for _, d := range deps {
			if _, seen := index[d]; !seen {
				visit(d)
				low[addr] = min(low[addr], low[d])
			} else if onStack[d] {
				low[addr] = min(low[addr], index[d])
			}
		}
		if low[addr] != index[addr] {
			return
		}
		var group []string
		for {
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[top] = false
			group = append(group, top)
			if top == addr {
				break
			}
		}
		if len(group) > 1 {
			sort.Strings(group)
			cycles = append(cycles, group)
		}
	}
	left := make([]string, 0, len(waiting))
	for addr := range waiting {
		left = append(left, addr)
	}
	sort.Strings(left)
	for _, addr := range left {
		if _, seen := index[addr]; !seen {
			visit(addr)
		}
	}
	sort.Slice(cycles, func(i, j int) bool { return cycles[i][0] < cycles[j][0] })
	msgs := make([]string, len(cycles))
	for i, c := range cycles {
		msgs[i] = "dependency cycle between " + strings.Join(c, ", ")
	}
	return WithCode(ExitInvalid, errors.New(strings.Join(msgs, "; ")))
}

// release counts a finished node off its dependents and queues each one that has
// nothing left to wait for. Once the queue is closed, because a failure stopped
// the run, nothing is queued: a node that the stopped run finishes without
// planning would otherwise send into a closed channel.
func release(n *node, ready chan<- *node, closed bool) {
	for _, d := range n.dependents {
		d.pending--
		if d.pending == 0 && !closed {
			ready <- d
		}
	}
}

// resetWalk puts the engine back to the specification as it was loaded, with
// no value of a task in it, for the walk that starts.
func (e *Engine) resetWalk() {
	e.current, e.seeded = e.Value, false
	e.pending, e.pendingOrder, e.refused = nil, nil, nil
	e.memoRun()
}

// run executes every task in dependency order, with bounded parallelism,
// and returns the configuration value with all fills applied.
func (e *Engine) run(ctx context.Context, apply bool) (cue.Value, error) {
	e.resetWalk()
	_, endGraph := setup(ctx, "build task graph")
	nodes, err := e.buildGraph()
	endGraph(err)
	if err != nil {
		return e.current, err
	}
	if apply && e.scope != nil {
		nodes = e.scope.nodes(nodes)
	} else {
		for _, n := range nodes {
			n.pending = len(n.deps)
		}
	}
	sctx, endSecrets := setup(ctx, "check secrets")
	err = e.checkSecrets(sctx, nodes)
	endSecrets(err)
	if err != nil {
		return e.current, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		firstAt  string // the address of the task whose error firstErr is
		done     int
		closed   bool
		ready    = make(chan *node, len(nodes))
		sem      = make(chan struct{}, Parallelism)
	)
	// The tasks that wait for nothing start in the order of their addresses, not in
	// the order a map gives them, so that two runs start the same ones first.
	for _, addr := range slices.Sorted(maps.Keys(nodes)) {
		if n := nodes[addr]; n.pending == 0 {
			ready <- n
		}
	}
	finish := func(n *node, err error) {
		mu.Lock()
		defer mu.Unlock()
		done++
		if err != nil {
			// Tasks that run side by side fail in whatever order they get there. The error
			// that is reported is the one of the task with the lowest address, so that two
			// runs of one specification report the same one. A task that stopped because
			// another failed says only that, and does not take the place.
			stopped := errors.Is(err, context.Canceled)
			if firstErr == nil || (!stopped && (errors.Is(firstErr, context.Canceled) || n.res.Address() < firstAt)) {
				firstErr, firstAt = err, n.res.Address()
			}
			cancel()
		} else {
			release(n, ready, closed)
		}
		if !closed && (done == len(nodes) || (firstErr != nil && len(sem) == 0)) {
			closed = true
			close(ready)
		}
	}
	if len(nodes) == 0 {
		closed = true
		close(ready)
	}
	walked := time.Now()
	left := 0 // tasks that were not started because the run was stopped
	for n := range ready {
		if ctx.Err() != nil {
			left++
			finish(n, nil)
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(n *node) {
			defer wg.Done()
			if e.onTask != nil {
				e.onTask(n.res.Address(), apply)
			}
			err := e.runTask(ctx, n.res, apply)
			<-sem
			finish(n, err)
		}(n)
	}
	wg.Wait()
	if left > 0 && firstErr == nil {
		// Nothing failed: the command was interrupted. A run that stops before it
		// has walked every task is not a run that finished.
		firstErr = fmt.Errorf("interrupted: %d of %d tasks were not started", left, len(nodes))
	}
	if !e.keepOpen {
		e.closeEphemerals(context.WithoutCancel(ctx))
	}
	phase := "plan"
	if apply {
		phase = "apply"
	}
	plog.Debug(ctx, "tasks run", "phase", phase, "tasks", len(nodes), "took", time.Since(walked).Round(time.Millisecond))
	// Whatever no later task needed is unified now, so that the outputs read
	// the finished configuration and a value that conflicts with it is
	// refused.
	e.cueMu.Lock()
	defer e.cueMu.Unlock()
	firstErr = cmp.Or(firstErr, e.fillExternalLocked())
	e.dropChecked()
	settled, fills := time.Now(), len(e.pending)
	if err := e.settleAllLocked(); err != nil && firstErr == nil {
		firstErr = err
	}
	plog.Debug(ctx, "settled", "phase", phase, "fills", fills, "took", time.Since(settled).Round(time.Millisecond))
	return e.current, firstErr
}

// pendingFill is what a finished task computed, waiting to be unified into
// the configuration.
type pendingFill struct {
	res  *config.Resource
	fill map[string]any
	// body is the value of the task body the task ran from; with the fill it is
	// everything that is known of the task (see fromPending). It does not exist
	// for a task that fills without one.
	body  cue.Value
	eff   cue.Value
	older []map[string]any
	// checked is 0 before the fill is checked against what reads it, 1 when it
	// may be used without unifying, 2 when it may not.
	checked int
}

// snapshot returns the current value of a task body. The fills of the tasks it
// depends on are unified into the configuration first, so that a reference to
// one of their computed values resolves.
func (e *Engine) snapshot(r *config.Resource) (cue.Value, error) {
	e.cueMu.Lock()
	defer e.cueMu.Unlock()
	return e.snapshotLocked(r)
}

// snapshotLocked is snapshot. The caller holds cueMu.
func (e *Engine) snapshotLocked(r *config.Resource) (cue.Value, error) {
	// A fill that the specification refused is dropped, and the run is ending.
	// A task that was ready at the same time as the one that found it starts
	// all the same, and would read the task as if it had computed nothing.
	if e.refused != nil {
		return cue.Value{}, e.refused
	}
	deps := e.deps[r.Address()]
	if e.scope != nil && e.scope.reads != nil {
		// The walk of an apply leaves nodes out. What a task reads through them is
		// filled by the nodes of the scope that it waited for.
		deps = e.scope.reads[r.Address()]
	}
	// A node that the plan left alone on the word of its memo made no fill. It
	// makes it now that a task reads it (memo.go).
	if err := e.fillLazyLocked(deps); err != nil {
		return cue.Value{}, err
	}
	// A task that finished and left no fill (its values are unknown, or already in
	// its configuration) is read from the configuration, where it is built from
	// the tasks it read; if their fills wait, it is not yet what it will be.
	if e.hiddenPending(deps) {
		if err := e.settleEveryLocked(); err != nil {
			return cue.Value{}, err
		}
	}
	if v, ok := e.fromPending(r, deps); ok {
		return v, nil
	}
	if err := e.settleLocked(deps); err != nil {
		return cue.Value{}, err
	}
	return e.at(r.Path), nil
}

// fill records the values a task computed, to be unified into the
// configuration. Unifying changes the value of the whole configuration, and
// evaluating the whole configuration costs the same whether one resource or
// all of them were filled; doing it after every task made a plan grow with the
// square of the number of resources. A fill waits until a task that depends on
// it starts, or until the run ends, and then every fill that waits is unified
// in one step.
func (e *Engine) fill(r *config.Resource, fill map[string]any) error {
	return e.fillFrom(r, fill, cue.Value{})
}

// fillFrom is fill for a task that ran from the body v.
func (e *Engine) fillFrom(r *config.Resource, fill map[string]any, v cue.Value) error {
	if len(fill) == 0 {
		return nil
	}
	e.cueMu.Lock()
	defer e.cueMu.Unlock()
	e.fillFromLocked(r, fill, v)
	return nil
}

// fillFromLocked is fillFrom for a fill that is not empty. The caller holds
// cueMu.
func (e *Engine) fillFromLocked(r *config.Resource, fill map[string]any, v cue.Value) {
	addr := r.Address()
	e.memoFill(r, fill)
	if e.onFill != nil && !e.shadowing {
		e.onFill(addr)
	}
	if e.pending == nil {
		e.pending = map[string]*pendingFill{}
	}
	if _, ok := e.pending[addr]; !ok {
		e.pendingOrder = append(e.pendingOrder, addr)
	}
	next := &pendingFill{res: r, fill: fill, body: v}
	if old, ok := e.pending[addr]; ok {
		// A task that fills again before its fill was unified: what it filled
		// before is not lost, as it would not be had that been unified first.
		next.older = append(append([]map[string]any(nil), old.older...), old.fill)
	}
	e.pending[addr] = next
	if v.Exists() && len(e.readersOf(addr)) == 0 {
		e.releaseLeaf(next)
	}
}

// dropChecked leaves out of the final settle the fills that nothing reads but task
// bodies and that were checked against all of them (checkFill). The final
// unification evaluates the whole configuration once more, and CUE's check of it
// takes memory that grows faster than the number of tasks. A task that an
// output, a field or a provider reads, and everything such a task is built from,
// is unified as before. The caller holds cueMu.
func (e *Engine) dropChecked() {
	e.iterMu.Lock()
	iterated := len(e.iterated)
	e.iterMu.Unlock()
	if len(e.pending) == 0 || iterated > 0 {
		return
	}
	if !e.externalDone {
		e.externalReads, e.externalAll = e.scanExternal()
		e.externalDone = true
	}
	if e.externalAll {
		return
	}
	needed := map[string]bool{}
	var need func(addr string)
	need = func(addr string) {
		if needed[addr] {
			return
		}
		needed[addr] = true
		for _, d := range e.deps[addr] {
			need(d)
		}
	}
	for _, r := range e.Root.Resources {
		if e.externalReads[r.Path.String()] {
			need(r.Address())
		}
	}
	keep := e.pendingOrder[:0:0]
	for _, a := range e.pendingOrder {
		pf, ok := e.pending[a]
		if !ok {
			continue
		}
		if !needed[a] && e.fastOK(pf) {
			delete(e.pending, a)
			continue
		}
		keep = append(keep, a)
	}
	e.pendingOrder = keep
}

// settleLocked unifies the pending fills into the configuration if any of the
// tasks at addrs has one. The caller holds cueMu.
func (e *Engine) settleLocked(addrs []string) error {
	if err := e.fillLazyLocked(addrs); err != nil {
		return err
	}
	if len(e.pending) == 0 {
		return nil
	}
	for _, a := range addrs {
		if _, ok := e.pending[a]; ok {
			return e.settleEveryLocked()
		}
	}
	return nil
}

// settleAllLocked unifies every pending fill into the configuration and checks
// the result. If it does not hold, the fills are applied one at a time to find
// the one that conflicts with the configuration; the others are kept.
func (e *Engine) settleAllLocked() error {
	if len(e.pending) == 0 {
		return nil
	}
	order, pending := e.pendingOrder, e.pending
	e.pending, e.pendingOrder = nil, nil
	e.settles++
	var fills []convert.PathFill
	if !e.seeded {
		e.seeded = true
		fills = e.seedFills()
	}
	for _, addr := range order {
		for _, older := range pending[addr].older {
			fills = append(fills, convert.PathFill{Path: pending[addr].res.Path, Value: older})
		}
		fills = append(fills, convert.PathFill{Path: pending[addr].res.Path, Value: pending[addr].fill})
	}
	v := convert.FillAll(e.current, fills)
	if v.Validate() == nil {
		e.current, e.filled = v, true
		return nil
	}
	var first error
	for _, addr := range order {
		p := pending[addr]
		v := e.current
		for _, older := range p.older {
			v = v.FillPath(p.res.Path, older)
		}
		v = v.FillPath(p.res.Path, p.fill)
		if err := v.Validate(); err != nil {
			if first == nil {
				first = fmt.Errorf("%s: filling values into the configuration: %w", addr, fillError(p.res, err))
			}
			continue
		}
		e.current, e.filled = v, true
	}
	if first == nil {
		first = errors.New("filling values into the configuration: the configuration does not hold")
	}
	e.refused = first
	return first
}

// fillError is the error of a fill that does not fit the configuration. CUE says which values
// conflict, and the value of a secret must not reach a message, so for a secret it says that
// the value is not shown.
func fillError(r *config.Resource, err error) error {
	if r.Builtin && (r.Type == "secret" || r.Type == "secret_env") {
		return errors.New("the value does not satisfy what the specification requires of it (the value is not shown)")
	}
	return err
}
