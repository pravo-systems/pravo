package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/plugin"
)

// ActionDefer marks a change that the plan defers to the next plan: its provider
// answered that it cannot plan or read the node yet, because a value it needs is
// not known until the apply (a cluster that is not created, a kind that a chart
// installs), or the node reads a change that is deferred. The apply leaves the
// node alone, and a plan made after the apply plans it with what is known by then.
// DeferReason says why.
//
// It is not the Deferred of a data source, which is read during the apply.
const ActionDefer Action = "defer"

// errDeferred is returned by a read of the plan that the provider deferred.
type errDeferred struct{ reason string }

func (e *errDeferred) Error() string {
	return "the provider cannot answer yet (" + e.reason + ")"
}

// deferredBy returns the reason when diags hold a deferred answer.
func deferredBy(diags plugin.Diagnostics) error {
	if reason, ok := diags.Deferred(); ok {
		return &errDeferred{reason: reason}
	}
	return nil
}

// deferChange marks the change of p as deferred to the next plan, for reason.
// A plan only defers: an apply that is answered so fails.
func (e *Engine) deferChange(p *resourcePlan, reason string) *Change {
	ch := p.ch
	ch.Action, ch.DeferReason = ActionDefer, reason
	t := p.s.Block.Type()
	if ch.Prior.Type() == nil {
		ch.Prior = tftypes.NewValue(t, nil)
		if p.sr != nil {
			if v, err := storedValue(p.sr, p.s); err == nil {
				ch.Prior = v
			}
		}
	}
	ch.Planned = tftypes.NewValue(t, tftypes.UnknownValue)
	ch.Reasons = append(ch.Reasons, "deferred to the next plan: "+reason)
	return ch
}

// deferredDep returns the first node that addr reads whose change is deferred,
// or "".
func (e *Engine) deferredDep(addr string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, d := range e.deps[addr] {
		if ch, ok := e.changes[d]; ok && ch.Action == ActionDefer {
			return d
		}
	}
	return ""
}

// deferIfAsked defers the change of p when err is a deferred answer and p is a
// plan; it returns err otherwise.
func (e *Engine) deferIfAsked(p *resourcePlan, err error) (*Change, error) {
	var d *errDeferred
	if !p.strict && errors.As(err, &d) {
		return e.deferChange(p, "the provider cannot plan it yet: "+d.reason), nil
	}
	if p.strict && errors.As(err, &d) {
		if !strings.HasPrefix(err.Error(), p.ch.Addr+":") {
			err = fmt.Errorf("%s: %w", p.ch.Addr, err)
		}
		return nil, WithCode(ExitStale, fmt.Errorf("%w, and the plan did not defer it: plan again", err))
	}
	return nil, err
}

// DeferredCount is the number of changes that the plan defers to the next plan.
func (p *Plan) DeferredCount() int {
	n := 0
	for _, c := range p.Changes {
		if c.Action == ActionDefer {
			n++
		}
	}
	return n
}
