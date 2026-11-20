package engine

import (
	"slices"

	"github.com/pravo-systems/pravo/internal/convert"
)

// The kinds of change that a plan can hold. A plan is worth storing when it has at least one:
// a plan that has none changes nothing in the state.
const (
	// KindResources: an object at a provider is created, updated, replaced or destroyed, or a
	// record is forgotten. A record that is only written again with the hash of the
	// configuration is no change.
	KindResources = "resources"
	// KindOutputs: an output is added, removed or gets another value.
	KindOutputs = "outputs"
	// KindMoves: a record moves to another address, or a provider is renamed.
	KindMoves = "moves"
	// KindImports: an object is brought under management.
	KindImports = "imports"
	// KindPolicies: a policy is added, changed or removed.
	KindPolicies = "policies"
)

// Kinds lists the kinds of change that applying the plan makes, in the order of the constants
// above. It is empty for a plan that would change nothing worth keeping: an apply of it writes
// at most the records of nodes whose configuration changed and whose object did not, and outputs
// that keep their values.
func (p *Plan) Kinds() []string {
	var out []string
	if p.hasProviderChanges() {
		out = append(out, KindResources)
	}
	if p.outputsChange() {
		out = append(out, KindOutputs)
	}
	moves, imports := len(p.Relabels) > 0, false
	for _, c := range p.Changes {
		switch {
		case c.Applied:
		case c.Imported:
			imports = true
		case c.Action == ActionMove:
			moves = true
		}
	}
	if moves {
		out = append(out, KindMoves)
	}
	if imports {
		out = append(out, KindImports)
	}
	if len(p.Policies) > 0 {
		out = append(out, KindPolicies)
	}
	return out
}

// ChangedOutputs names the outputs that the apply adds, removes or gives another value, sorted.
func (p *Plan) ChangedOutputs() []string {
	var out []string
	for _, n := range append(slices.Clone(p.EvalOutputs), p.DropOutputs...) {
		if p.OutputChanged(n) && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

// outputsChange reports whether the apply of the plan adds, removes or changes an output.
func (p *Plan) outputsChange() bool { return len(p.ChangedOutputs()) > 0 }

// OutputChanged reports whether the apply of the plan gives output n a value other than the one
// that is recorded: an output that is dropped, has no recorded value, whose value is not known
// yet, or whose value differs. An output that the plan evaluates because it reads a node that
// the plan writes, and whose value stays, is not changed.
func (p *Plan) OutputChanged(n string) bool {
	if slices.Contains(p.DropOutputs, n) {
		return true
	}
	if !slices.Contains(p.EvalOutputs, n) {
		return false
	}
	raw, ok := p.OldOutputs[n]
	if !ok {
		return true
	}
	old, _ := UnwrapOutput(raw)
	var now []byte
	switch v, have := p.Outputs[n]; {
	case have && convert.IsConcrete(v):
		b, err := outputJSON(v)
		if err != nil {
			return true
		}
		now = b
	case have:
		return true
	default:
		saved, ok := p.SavedOutputs[n]
		if !ok {
			return true
		}
		now = saved
	}
	return !sameOutput(old, now)
}
