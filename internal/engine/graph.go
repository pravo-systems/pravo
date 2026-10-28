package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/plog"
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
func (r taskResolver) Value(p cue.Path) cue.Value { return r.e.current.LookupPath(p) }

// resolver returns the engine's tasks as the analysis sees them.
func (e *Engine) resolver() convert.Resolver { return taskResolver{e} }

// isEphemeral reports whether the task at path p is an ephemeral resource.
func (e *Engine) isEphemeral(p cue.Path) bool {
	r, ok := e.byPath[p.String()]
	return ok && r.Mode == "ephemeral"
}

// buildGraph computes dependencies for every declared task from the
// references in its body, independent of whether values are known yet. A
// cycle among the tasks is an error: nothing could run first. The graph is
// built once; later walks of the engine use it.
func (e *Engine) buildGraph() (map[string]*node, error) {
	if e.graph != nil {
		return e.graph, nil
	}
	began := time.Now()
	nodes := map[string]*node{}
	for _, r := range e.Root.Resources {
		nodes[r.Address()] = &node{res: r, deps: map[string]bool{}}
	}
	e.cueMu.Lock()
	defer e.cueMu.Unlock()
	for _, r := range e.Root.Resources {
		v := e.current.LookupPath(r.Path)
		deps, _ := convert.Dependencies(v, r.Path, e.resolver())
		n := nodes[r.Address()]
		for _, d := range deps {
			if dr, ok := e.byPath[d.String()]; ok && dr != r {
				n.deps[dr.Address()] = true
			}
		}
	}
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
	e.graphBuilds++
	plog.Debug(context.Background(), "graph built", "tasks", len(nodes), "took", time.Since(began).Round(time.Millisecond))
	if err := checkCycles(nodes); err != nil {
		return nil, err
	}
	e.graph = nodes
	return nodes, nil
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
		deps, _ := convert.Dependencies(e.Value.LookupPath(p), p, e.resolver())
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
func CheckGraph(root *config.Root, value cue.Value) error {
	e := &Engine{Root: root, Value: value, cueState: cueState{current: value}, deps: map[string][]string{}, byPath: map[string]*config.Resource{}}
	for _, r := range root.Resources {
		e.byPath[r.Path.String()] = r
	}
	_, err := e.buildGraph()
	return err
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

// run executes every task in dependency order, with bounded parallelism,
// and returns the configuration value with all fills applied.
func (e *Engine) run(ctx context.Context, apply bool) (cue.Value, error) {
	e.current, e.seeded = e.Value, false
	e.pending, e.pendingOrder = nil, nil
	nodes, err := e.buildGraph()
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
	if err := e.checkSecrets(nodes); err != nil {
		return e.current, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		done     int
		closed   bool
		ready    = make(chan *node, len(nodes))
		sem      = make(chan struct{}, Parallelism)
	)
	for _, n := range nodes {
		if n.pending == 0 {
			ready <- n
		}
	}
	finish := func(n *node, err error) {
		mu.Lock()
		defer mu.Unlock()
		done++
		if err != nil {
			if firstErr == nil {
				firstErr = err
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
}

// snapshot returns the current value of a task body. The fills of the tasks it
// depends on are unified into the configuration first, so that a reference to
// one of their computed values resolves.
func (e *Engine) snapshot(r *config.Resource) (cue.Value, error) {
	e.cueMu.Lock()
	defer e.cueMu.Unlock()
	deps := e.deps[r.Address()]
	if e.scope != nil && e.scope.reads != nil {
		// The walk of an apply leaves nodes out. What a task reads through them is
		// filled by the nodes of the scope that it waited for.
		deps = e.scope.reads[r.Address()]
	}
	if err := e.settleLocked(deps); err != nil {
		return cue.Value{}, err
	}
	return e.current.LookupPath(r.Path), nil
}

// fill records the values a task computed, to be unified into the
// configuration. Unifying changes the value of the whole configuration, and
// evaluating the whole configuration costs the same whether one resource or
// all of them were filled; doing it after every task made a plan grow with the
// square of the number of resources. A fill waits until a task that depends on
// it starts, or until the run ends, and then every fill that waits is unified
// in one step.
func (e *Engine) fill(r *config.Resource, fill map[string]any) error {
	if len(fill) == 0 {
		return nil
	}
	e.cueMu.Lock()
	defer e.cueMu.Unlock()
	addr := r.Address()
	if e.onFill != nil {
		e.onFill(addr)
	}
	if e.pending == nil {
		e.pending = map[string]pendingFill{}
	}
	if _, ok := e.pending[addr]; !ok {
		e.pendingOrder = append(e.pendingOrder, addr)
	}
	e.pending[addr] = pendingFill{res: r, fill: fill}
	return nil
}

// settleLocked unifies the pending fills into the configuration if any of the
// tasks at addrs has one. The caller holds cueMu.
func (e *Engine) settleLocked(addrs []string) error {
	if len(e.pending) == 0 {
		return nil
	}
	for _, a := range addrs {
		if _, ok := e.pending[a]; ok {
			return e.settleAllLocked()
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
		fills = append(fills, convert.PathFill{Path: pending[addr].res.Path, Value: pending[addr].fill})
	}
	v := convert.FillAll(e.current, fills)
	if v.Validate() == nil {
		e.current = v
		return nil
	}
	var first error
	for _, addr := range order {
		p := pending[addr]
		v := e.current.FillPath(p.res.Path, p.fill)
		if err := v.Validate(); err != nil {
			if first == nil {
				first = fmt.Errorf("%s: filling values into the configuration: %w", addr, fillError(p.res, err))
			}
			continue
		}
		e.current = v
	}
	if first == nil {
		first = errors.New("filling values into the configuration: the configuration does not hold")
	}
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
