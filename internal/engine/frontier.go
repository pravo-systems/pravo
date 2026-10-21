package engine

import (
	"sort"

	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/state"
)

// role says what the walk of an apply does with a task.
type role int

const (
	// roleWrite is a node the apply changes: it is planned again with the values
	// that are known by then and compared with the plan.
	roleWrite role = iota + 1
	// roleInput is a node that a node of the scope reads and the plan did not
	// change, or a node that an output to evaluate reads. It is not converted and
	// not sent to its provider: its value is its record.
	roleInput
	// roleSupport is an ephemeral resource, a function call or a built-in value
	// that a node of the scope reads. The walk runs it again.
	roleSupport
)

// applyScope is the set of tasks that the walk of an apply runs. A node that is
// not in it is not walked: the apply writes nothing there and reads nothing
// from there.
type applyScope struct {
	roles map[string]role
	// reads maps each node of the scope to the nodes of the scope that it reads,
	// directly or through nodes that are not in the scope. nodes fills it.
	reads map[string][]string
}

// addrs lists the tasks of the scope that have the role, sorted.
func (sc *applyScope) addrs(r role) []string {
	var out []string
	for addr, have := range sc.roles {
		if have == r {
			out = append(out, addr)
		}
	}
	sort.Strings(out)
	return out
}

// inputsOf returns, from the versions of the records that a plan lists, those of
// the records that the walk reads as inputs. A plan that was cut down from a file
// lists the records of the whole file; the apply of a part reads some of them.
func (sc *applyScope) inputsOf(plan *Plan) map[string]state.Version {
	if sc == nil {
		return nil
	}
	out := make(map[string]state.Version, len(plan.Inputs))
	for addr, role := range sc.roles {
		if rev, ok := plan.Inputs[addr]; ok && role == roleInput {
			out[addr] = state.Version{Revision: rev, Generation: plan.InputGenerations[addr]}
		}
	}
	return out
}

// nodes returns the graph that the walk runs: the nodes of the scope, each
// waiting for the nodes of the scope that it reads, directly or through nodes
// that are not in the scope. A node whose value is its record waits as well. The
// record fills in what the configuration of the node does not say yet, and the
// configuration of a node that is still to be applied may read a node that this
// apply writes: the node has to have the new value by then, or the record and
// the configuration disagree.
func (sc *applyScope) nodes(full map[string]*node) map[string]*node {
	out := make(map[string]*node, len(sc.roles))
	for addr := range sc.roles {
		if n, ok := full[addr]; ok {
			out[addr] = &node{res: n.res, deps: map[string]bool{}}
		}
	}
	// through[addr] lists the nodes of the scope that the node reads. A node
	// outside the scope is looked through, once.
	through := make(map[string][]string, len(out))
	var find func(addr string) []string
	find = func(addr string) []string {
		if got, ok := through[addr]; ok {
			return got
		}
		through[addr] = nil
		found := map[string]bool{}
		for d := range full[addr].deps {
			if _, in := out[d]; in {
				found[d] = true
			} else if full[d] != nil {
				for _, x := range find(d) {
					found[x] = true
				}
			}
		}
		list := make([]string, 0, len(found))
		for d := range found {
			list = append(list, d)
		}
		sort.Strings(list)
		through[addr] = list
		return list
	}
	sc.reads = make(map[string][]string, len(out))
	for addr, n := range out {
		sc.reads[addr] = find(addr)
		for _, d := range sc.reads[addr] {
			n.deps[d] = true
			out[d].dependents = append(out[d].dependents, n)
		}
		n.pending = len(n.deps)
	}
	return out
}

// writes reports whether the apply of a plan writes or removes the record of the
// node: a create, an update, a replacement, a delete, a read of a data source
// that returned something other than the record, and a node that is planned and
// has nothing to do but record the configuration. Opening an ephemeral
// resource, calling a function, a node that the plan skipped and a data source
// whose read returned its record write nothing.
func (c *Change) writes() bool {
	if c.Applied || c.Skipped || c.Kept {
		return false
	}
	switch c.Action {
	case "", ActionMove, ActionOpen, ActionCall:
		return false
	}
	return c.Mode == "managed" || c.Mode == "data"
}

// writeAddrs lists the addresses that the apply of the plan writes or removes,
// sorted.
func (p *Plan) writeAddrs() []string {
	var out []string
	for _, ch := range p.Changes {
		if ch.writes() {
			out = append(out, ch.Addr)
		}
	}
	sort.Strings(out)
	return out
}

// NeedsApply reports whether applying the plan writes a record or an output, or
// removes an output.
func (p *Plan) NeedsApply() bool {
	return len(p.EvalOutputs) > 0 || len(p.DropOutputs) > 0 || len(p.writeAddrs()) > 0
}

// scopeFor computes what the apply of the plan walks. The nodes it writes run.
// Whatever they read, and whatever the outputs it evaluates read, is in the
// scope too: a node that the plan did not change is read as its record, and an
// ephemeral resource, a function call or a built-in value runs again together
// with whatever it reads.
func (e *Engine) scopeFor(plan *Plan) (*applyScope, error) {
	if _, err := e.buildGraph(); err != nil {
		return nil, err
	}
	sc := &applyScope{roles: map[string]role{}}
	var writes []string
	for _, addr := range plan.writeAddrs() {
		if e.byPath[addr] != nil {
			sc.roles[addr] = roleWrite
			writes = append(writes, addr)
		}
	}
	var need func(addr string)
	need = func(addr string) {
		if _, ok := sc.roles[addr]; ok {
			return
		}
		r := e.byPath[addr]
		if r == nil {
			return
		}
		if r.Mode == "managed" || r.Mode == "data" {
			sc.roles[addr] = roleInput
			// A field of the node that its configuration spells as an expression is
			// only resolved when what the expression reads is known, and a node that
			// reads that field reads the expression. What the node reads is an input
			// as well.
			for _, d := range e.deps[addr] {
				need(d)
			}
			return
		}
		sc.roles[addr] = roleSupport
		for _, d := range e.deps[addr] {
			need(d)
		}
	}
	for _, addr := range writes {
		for _, d := range e.deps[addr] {
			need(d)
		}
	}
	for _, name := range plan.EvalOutputs {
		for _, d := range e.outputDeps[name] {
			need(d)
		}
	}
	return sc, nil
}

// outputsToEval lists the outputs that the apply of the plan evaluates: those
// that read a node it writes, those that the plan could not tell yet and those
// whose value differs from the one that is recorded. The others keep the value
// that is recorded.
func (e *Engine) outputsToEval(plan *Plan) []string {
	write := stringSet(plan.writeAddrs())
	recorded := e.State.Outputs()
	var out []string
	for _, o := range e.Root.Outputs {
		eval := false
		for _, d := range e.outputDeps[o.Name] {
			if write[d] {
				eval = true
				break
			}
		}
		if v := plan.Outputs[o.Name]; !eval {
			eval = !convert.IsConcrete(v)
			if !eval {
				b, err := outputJSON(v)
				old, ok := recorded[o.Name]
				eval = err != nil || !ok || !jsonEqual(old, WrapOutput(b, o.Sensitive))
			}
		}
		if eval {
			out = append(out, o.Name)
		}
	}
	return out
}

// outputsToDrop lists the recorded outputs that the specification no longer
// declares, sorted: the apply removes them.
func (e *Engine) outputsToDrop() []string {
	declared := make(map[string]bool, len(e.Root.Outputs))
	for _, o := range e.Root.Outputs {
		declared[o.Name] = true
	}
	var out []string
	for name := range e.State.Outputs() {
		if !declared[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// frontier records on the plan what its apply walks: the addresses it writes,
// the outputs it evaluates and the ones it removes, the ephemeral resources,
// calls and built-in values it runs again and the revision of every record it
// reads.
func (e *Engine) frontier(plan *Plan) error {
	plan.Write = plan.writeAddrs()
	plan.EvalOutputs = e.outputsToEval(plan)
	plan.DropOutputs = e.outputsToDrop()
	sc, err := e.scopeFor(plan)
	if err != nil {
		return err
	}
	plan.Support = sc.addrs(roleSupport)
	plan.Inputs, plan.InputGenerations = map[string]int{}, map[string]string{}
	for _, addr := range sc.addrs(roleInput) {
		rev, gen := e.versionOf(addr)
		plan.Inputs[addr] = rev
		if gen != "" {
			plan.InputGenerations[addr] = gen
		}
	}
	return nil
}
