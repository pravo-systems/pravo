package engine

import (
	"sort"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
)

// effective is everything that is known of a task that has finished and whose
// fill is pending: the body it ran from with the values it computed.
func (pf *pendingFill) effective() cue.Value {
	if !pf.eff.Exists() && pf.body.Exists() {
		pf.eff = pf.body
		for _, older := range pf.older {
			pf.eff = pf.eff.FillPath(cue.Path{}, older)
		}
		pf.eff = pf.eff.FillPath(cue.Path{}, pf.fill)
	}
	return pf.eff
}

// fromPending returns the body of r with the values of the tasks it depends on
// put in, without unifying them into the configuration. Unifying evaluates the
// whole configuration, and a chain of tasks where each reads the one before has
// one such step for every link, so a chain of n grew with the square of n. The
// body is worked out from what is known of the tasks it reads instead, which
// costs the size of the body (bodyWith); the fills stay pending until a task
// needs them in another form, or the run ends.
//
// Unifying also checks the values against everything in the specification that
// reads them, before the task starts. The fast path keeps that check: it is used
// only for a value that nothing but task bodies reads, and every task that reads
// it is worked out with it first (checkFill). ok is false when this cannot be
// done exactly, and the caller unifies, which is exact and names what conflicts.
// The caller holds cueMu.
func (e *Engine) fromPending(r *config.Resource, deps []string) (cue.Value, bool) {
	if len(e.pending) == 0 {
		return cue.Value{}, false
	}
	waiting := false
	for _, a := range deps {
		if pf, ok := e.pending[a]; ok {
			waiting = true
			if !e.fastOK(pf) {
				return cue.Value{}, false
			}
		}
	}
	if !waiting {
		return e.current.LookupPath(r.Path), true
	}
	v, ok := e.bodyWith(r)
	if !ok {
		return cue.Value{}, false
	}
	e.fastFills++
	return v, true
}

// fastOK reports whether the pending fill may be used without unifying it.
func (e *Engine) fastOK(pf *pendingFill) bool {
	if pf.checked == 0 {
		pf.checked = 2
		if e.checkFill(pf) {
			pf.checked = 1
		}
	}
	return pf.checked == 1
}

// checkFill is the early check of a fill: the values fit the task they came
// from, nothing but task bodies reads them, and every task that reads them is
// still consistent with them.
func (e *Engine) checkFill(pf *pendingFill) bool {
	if !pf.body.Exists() || pf.effective().Validate() != nil {
		return false
	}
	if e.externalAll {
		return false
	}
	if !e.externalDone {
		e.externalReads, e.externalAll = e.scanExternal()
		e.externalDone = true
		if e.externalAll {
			return false
		}
	}
	if e.externalReads[pf.res.Path.String()] {
		return false
	}
	for _, a := range e.readersOf(pf.res.Address()) {
		d := e.addrRes(a)
		if d == nil {
			return false
		}
		if _, ok := e.bodyWith(d); !ok {
			return false
		}
	}
	return true
}

// scanExternal lists the tasks that something other than a task body reads: the
// configuration of a provider, an output, a plain field.
func (e *Engine) scanExternal() (map[string]bool, bool) {
	holds := map[string]bool{"": true}
	for _, r := range e.Root.Resources {
		sels := r.Path.Selectors()
		for i := 1; i < len(sels); i++ {
			holds[cue.MakePath(sels[:i]...).String()] = true
		}
	}
	reads, all := convert.ExternalReads(e.current, func(p cue.Path) bool {
		_, ok := e.byPath[p.String()]
		return ok
	}, func(p cue.Path) bool { return holds[p.String()] }, e.resolver())
	for _, addrs := range e.providerDeps {
		for _, a := range addrs {
			if r := e.addrRes(a); r != nil {
				reads[r.Path.String()] = true
			}
		}
	}
	for _, addrs := range e.outputDeps {
		for _, a := range addrs {
			if r := e.addrRes(a); r != nil {
				reads[r.Path.String()] = true
			}
		}
	}
	return reads, all
}

func (e *Engine) addrRes(addr string) *config.Resource {
	if n, ok := e.graph[addr]; ok {
		return n.res
	}
	return nil
}

// readersOf lists the tasks that wait for addr.
func (e *Engine) readersOf(addr string) []string {
	if e.readerIdx == nil {
		e.readerIdx = map[string][]string{}
		for a, deps := range e.deps {
			for _, d := range deps {
				e.readerIdx[d] = append(e.readerIdx[d], a)
			}
		}
		for _, l := range e.readerIdx {
			sort.Strings(l)
		}
	}
	return e.readerIdx[addr]
}

// bodyWith returns the body of r with the values of the pending tasks it reads put
// in, or false when that cannot be done exactly. A body that reads them in plain
// references (subnet_id: net.id) gets the values leaf by leaf; any other body is
// evaluated on its own, in a scope that holds only what it names (scopeEval).
func (e *Engine) bodyWith(r *config.Resource) (cue.Value, bool) {
	body := e.current.LookupPath(r.Path)
	if !body.Exists() {
		return cue.Value{}, false
	}
	// A task that the run does not walk keeps the value the configuration gives
	// it, and that value is built from the tasks it reads: if one of them waits for
	// its fill, the value is not what it will be.
	seen := map[string]bool{}
	for _, d := range e.deps[r.Address()] {
		if _, waits := e.pending[d]; !waits && e.reachesPending(d, seen) {
			return cue.Value{}, false
		}
	}
	isPending := func(task cue.Path) bool {
		t, ok := e.byPath[task.String()]
		if !ok {
			return false
		}
		_, ok = e.pending[t.Address()]
		return ok
	}
	refs, ok := convert.PureRefs(body, r.Path, e.resolver(), isPending)
	if !ok {
		return e.scopeEval(r, isPending)
	}
	for _, ref := range refs {
		pf := e.pending[e.byPath[ref.Task.String()].Address()]
		if !pf.body.Exists() {
			return cue.Value{}, false
		}
		av := pf.effective().LookupPath(cue.MakePath(ref.Attr...))
		if !av.Exists() || !convert.IsConcrete(av) {
			return cue.Value{}, false
		}
		body = body.FillPath(ref.Path, av)
	}
	if body.Validate() != nil {
		return cue.Value{}, false
	}
	return body, true
}

// reachesPending reports whether the task at addr reads, directly or through other
// tasks, one whose fill is pending.
func (e *Engine) reachesPending(addr string, seen map[string]bool) bool {
	if seen[addr] {
		return false
	}
	seen[addr] = true
	for _, d := range e.deps[addr] {
		if _, waits := e.pending[d]; waits || e.reachesPending(d, seen) {
			return true
		}
	}
	return false
}

// scopeEval evaluates the declaration of a task on its own. The names it uses are
// looked up in the configuration; a task that finished and waits for its fill is
// what is known of it, and a name that is built from such a task cannot be
// worked out here. The declaration must be the only one of the task, written at
// the top of the specification.
func (e *Engine) scopeEval(r *config.Resource, isPending func(cue.Path) bool) (cue.Value, bool) {
	sels := r.Path.Selectors()
	if len(sels) != 1 || sels[0].LabelType() != cue.StringLabel {
		return cue.Value{}, false
	}
	f := e.fieldSrc[r.Address()]
	if f == nil || f.Value == nil {
		return cue.Value{}, false
	}
	names := map[string]bool{}
	ast.Walk(f.Value, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			names[id.Name] = true
		}
		return true
	}, nil)
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	ctx := e.current.Context()
	scope := ctx.CompileString("{}")
	for _, n := range sorted {
		if strings.HasPrefix(n, "_") && n != "_" {
			// A hidden name belongs to a package; it cannot be looked up here.
			if e.current.LookupPath(cue.ParsePath(n)).Exists() {
				return cue.Value{}, false
			}
			continue
		}
		var sel cue.Selector
		if strings.HasPrefix(n, "#") {
			sel = cue.Def(n)
		} else {
			sel = cue.Str(n)
		}
		p := cue.MakePath(sel)
		var val cue.Value
		if t, ok := e.byPath[p.String()]; ok {
			if pf, waits := e.pending[t.Address()]; waits {
				val = pf.effective()
				if !val.Exists() {
					return cue.Value{}, false
				}
			} else {
				val = e.current.LookupPath(p)
			}
		} else {
			val = e.current.LookupPath(p)
			if !val.Exists() {
				continue
			}
			for _, pf := range e.pending {
				// A struct that holds a task that waits for its fill is not what it will be.
				if ps := pf.res.Path.Selectors(); len(ps) > 1 && ps[0].String() == p.Selectors()[0].String() {
					return cue.Value{}, false
				}
			}
			if e.builtFromPending(n, val, isPending) {
				return cue.Value{}, false
			}
		}
		scope = scope.FillPath(p, val)
	}
	// The syntax carries the references that the parser resolved against the files;
	// parsing what it prints leaves them for the scope to resolve.
	src, err := format.Node(f.Value)
	if err != nil {
		return cue.Value{}, false
	}
	expr, err := parser.ParseExpr("", src)
	if err != nil {
		return cue.Value{}, false
	}
	res := ctx.BuildExpr(expr, cue.Scope(scope))
	if res.Err() != nil || res.Validate() != nil {
		return cue.Value{}, false
	}
	return res, true
}

// builtFromPending reports whether the plain field name is built from a task whose
// fill is pending.
func (e *Engine) builtFromPending(name string, val cue.Value, isPending func(cue.Path) bool) bool {
	tasks, ok := e.nameTasks[name]
	if !ok {
		tasks = map[string]bool{}
		for _, t := range convert.SourceTasks(val, cue.Path{}, e.resolver()) {
			tasks[t.String()] = true
		}
		if e.nameTasks == nil {
			e.nameTasks = map[string]map[string]bool{}
		}
		e.nameTasks[name] = tasks
	}
	for t := range tasks {
		if isPending(cue.ParsePath(t)) {
			return true
		}
	}
	return false
}
