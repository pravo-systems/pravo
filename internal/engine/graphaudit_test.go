package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
)

// Every graph that a test of this package builds is checked here, whatever the
// test is about. The graph is built from the syntax and from CUE's evaluator
// (loopdeps.go), and the package tools/flow that gives the evaluator's part is
// being reworked upstream, so the two readings are compared on every
// specification the tests have, each time they run.
//
// Two things are checked. The first is by construction: an edge that the syntax
// gives is in the graph, unless the task is made inside a loop that is read by
// element. The second does not trust either reading. It fills in the computed
// values of every task that the graph says a task does not read, directly or
// through others, and evaluates the specification again: the body of the task
// must come out as it was. A dependency that the graph lacks shows as a body
// that changed.

var audit struct {
	mu sync.Mutex
	// graphs, tasks and edges count what was checked; fromSyntax counts the
	// edges of the syntax alone and evaluatorOnly the edges only the evaluator
	// found. byElement counts the loops read by element, unconfirmed the ones
	// the evaluator named no element for, and probes the evaluations made.
	graphs, tasks, edges, fromSyntax, evaluatorOnly, byElement, unconfirmed, probes int
	failures                                                                        []string
}

func init() { graphAudit = auditGraph }

func TestMain(m *testing.M) {
	code := m.Run()
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if testing.Verbose() || len(audit.failures) > 0 {
		fmt.Printf("graph audit: %d graphs, %d tasks, %d edges (%d from the syntax alone, %d only from the evaluator), %d loops read by element, %d unconfirmed, %d probes\n",
			audit.graphs, audit.tasks, audit.edges, audit.fromSyntax, audit.evaluatorOnly, audit.byElement, audit.unconfirmed, audit.probes)
	}
	if len(audit.failures) > 0 {
		shown := audit.failures
		if len(shown) > 20 {
			shown = shown[:20]
		}
		fmt.Printf("graph audit: %d failures\n%s\n", len(audit.failures), strings.Join(shown, "\n"))
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// auditGraph checks one graph; see the comment at the top of the file.
func auditGraph(e *Engine, syntax map[string][]string) {
	var failures []string
	byElement := map[string]bool{}
	for _, r := range e.Root.Resources {
		if e.loops.ByElement(r.Path) {
			byElement[r.Address()] = true
		}
	}
	edges, fromSyntax, evaluatorOnly := 0, 0, 0
	for addr, deps := range e.deps {
		have := map[string]bool{}
		for _, d := range deps {
			have[d] = true
		}
		was := map[string]bool{}
		for _, d := range syntax[addr] {
			was[d] = true
			if !have[d] && !byElement[addr] {
				failures = append(failures, fmt.Sprintf("%s: the syntax reads %s, the graph does not, and no loop of the task is read by element", addr, d))
			}
		}
		for _, d := range deps {
			if !was[d] {
				evaluatorOnly++
			}
		}
		edges += len(deps)
		fromSyntax += len(syntax[addr])
	}
	// The tasks to probe: every task made in a loop that is read by element,
	// and the others, up to a number that keeps a large graph cheap.
	var loops, others []string
	for _, r := range e.Root.Resources {
		if byElement[r.Address()] {
			loops = append(loops, r.Address())
		} else {
			others = append(others, r.Address())
		}
	}
	probes := 0
	for _, addr := range append(spread(loops, 24), spread(others, 12)...) {
		ran, diff := probeIndependence(e, addr)
		if ran {
			probes++
		}
		if diff != "" {
			failures = append(failures, diff)
		}
	}
	if len(failures) > 0 && e.Root.Dir != "" {
		// The harness writes the specification of a test next to its state.
		if text, err := os.ReadFile(filepath.Join(e.Root.Dir, "main.cue")); err == nil {
			failures[len(failures)-1] += "\n  specification:\n" + string(text)
		}
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	audit.graphs++
	audit.tasks += len(e.deps)
	audit.edges += edges
	audit.fromSyntax += fromSyntax
	audit.evaluatorOnly += evaluatorOnly
	audit.byElement += e.loops.Narrowed
	audit.unconfirmed += e.loops.Unconfirmed
	audit.probes += probes
	audit.failures = append(audit.failures, failures...)
}

// spread returns at most n of the addresses, evenly spread over the list.
func spread(addrs []string, n int) []string {
	if len(addrs) <= n {
		return addrs
	}
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, addrs[i*len(addrs)/n])
	}
	return out
}

// probeIndependence fills in the computed values of every task that the task at
// addr does not read, directly or through others, and reports a body of the
// task that is not what it was. ran is false when there was nothing to fill.
func probeIndependence(e *Engine, addr string) (ran bool, diff string) {
	reads := map[string]bool{}
	var mark func(a string)
	mark = func(a string) {
		if reads[a] {
			return
		}
		reads[a] = true
		for _, d := range e.deps[a] {
			mark(d)
		}
	}
	mark(addr)
	var self *config.Resource
	// A fill meets the fields of a struct ahead of the comprehension that
	// declares them, so the fields would come out in the order of the fills
	// and a list made by a loop over the struct would hold other elements at
	// its indexes. Empty fills in the order of the declarations keep the
	// order, as the engine does for a run (seedFills).
	var seed, fills []convert.PathFill
	for _, r := range e.Root.Resources {
		if r.Address() == addr {
			self = r
		}
		if plainPath(r.Path) {
			seed = append(seed, convert.PathFill{Path: r.Path, Value: map[string]any{}})
		}
		if reads[r.Address()] {
			continue
		}
		if f := computedFill(e, r); len(f) > 0 {
			fills = append(fills, convert.PathFill{Path: r.Path, Value: f})
		}
	}
	if self == nil || len(fills) == 0 {
		return false, ""
	}
	before := printed(convert.FillAll(e.Value, seed).LookupPath(self.Path))
	after := printed(convert.FillAll(e.Value, append(seed, fills...)).LookupPath(self.Path))
	if before != after {
		return true, fmt.Sprintf("%s: its body changes when tasks that the graph says it does not read are filled in\n  before: %s\n  after:  %s\n  reads: %s", addr, before, after, strings.Join(e.deps[addr], ", "))
	}
	return true, ""
}

// computedFill makes a value for every computed string attribute of the task
// that the specification leaves open.
func computedFill(e *Engine, r *config.Resource) map[string]any {
	if r.Builtin || e.Schemas == nil {
		return nil
	}
	s, err := e.resourceSchema(r)
	if err != nil || s.Block == nil {
		return nil
	}
	out := map[string]any{}
	for _, a := range s.Block.Attributes {
		if !a.Computed {
			continue
		}
		v := e.Value.LookupPath(cue.MakePath(append(r.Path.Selectors(), cue.Str(a.Name))...))
		if !v.Exists() || convert.IsConcrete(v) || v.IncompleteKind()&cue.StringKind == 0 {
			continue
		}
		out[a.Name] = "audit:" + r.Address() + ":" + a.Name
	}
	return out
}

// printed is the value as evaluated, field by field, hidden fields included.
func printed(v cue.Value) string { return printedAt(v, 0) }

func printedAt(v cue.Value, depth int) string {
	if depth > 32 {
		return "..."
	}
	switch v.IncompleteKind() {
	case cue.StructKind:
		it, err := v.Fields(cue.Hidden(true))
		if err != nil {
			return fmt.Sprintf("!%v", err)
		}
		var fields []string
		for it.Next() {
			fields = append(fields, it.Selector().String()+":"+printedAt(it.Value(), depth+1))
		}
		sort.Strings(fields)
		return "{" + strings.Join(fields, " ") + "}"
	case cue.ListKind:
		it, err := v.List()
		if err != nil {
			return fmt.Sprintf("!%v", err)
		}
		var elems []string
		for it.Next() {
			elems = append(elems, printedAt(it.Value(), depth+1))
		}
		return "[" + strings.Join(elems, ",") + "]"
	}
	if v.IsConcrete() {
		js, _ := v.MarshalJSON()
		return string(js)
	}
	return fmt.Sprintf("?%v", v)
}
