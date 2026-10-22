package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/pravo-systems/pravo/internal/state"
)

// MarkPartlyApplied records that the changes at addrs were applied by txn, as
// MarkApplied does, for an apply that failed or was interrupted after it applied
// them. The plan is not done, and the progress does not say that an apply from
// it finished: the outputs and the records that the plan only writes are
// recorded by the end of an apply, and are still left to the next one.
func (ps *PlanStore) MarkPartlyApplied(ctx context.Context, id string, addrs []string, txn string) error {
	if _, err := ps.B.Get(ctx, metaKey2(id)); errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("%w: %s was removed while it was applied", ErrNoPlan, id)
	}
	return ps.updateProgress(ctx, id, func(p *PlanProgress) error {
		if p.Applied == nil {
			p.Applied = map[string]string{}
		}
		for _, a := range addrs {
			p.Applied[a] = txn
		}
		// An apply that finished earlier recorded the outputs; this one did not, and
		// the next one writes them again.
		p.ApplyTxn = ""
		return nil
	})
}
