package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/pravo-systems/pravo/internal/state"
)

// SealedGroups counts the objects of the stored plans that are sealed, for each
// group of the state: a plan file, and the box of a summary that holds the command
// line and the tags as they were typed. An object that is not sealed is in no
// group. It is what `pravo protect gc` asks before it drops a group of the state,
// and it needs no key: the group is in the clear.
//
// A plan object that cannot be read or told apart is an error, and the counts
// that were made are returned with it. A caller that drops groups must not drop one
// on a count that is not whole.
func (ps *PlanStore) SealedGroups(ctx context.Context) (map[string]int, error) {
	ids, all, err := ps.planObjects(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	var errs []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return counts, err
		}
		kinds := all[id]
		if kinds["plan"] {
			data, err := ps.B.Get(ctx, planKey(id))
			switch {
			case errors.Is(err, state.ErrNotFound):
			case err != nil:
				errs = append(errs, fmt.Errorf("%s: %w", id, err))
			default:
				if env, ok := asSealedPlan(data); ok {
					counts[env.Sealed.Group]++
				}
			}
		}
		if kinds["meta"] {
			data, err := ps.B.Get(ctx, metaKey2(id))
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", id, err))
				continue
			}
			m, err := decodeMeta(id, data)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if m.Sealed != nil {
				counts[m.Sealed.Group]++
			}
		}
	}
	return counts, errors.Join(errs...)
}
