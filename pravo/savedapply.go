package pravo

import (
	"context"
	"fmt"
	"strings"

	"github.com/pravo-systems/pravo/internal/engine"
)

// SavedSink is where a saved plan keeps what an apply of it did: a plan file, or a plan
// in the plan store. The sequence that applies a saved plan is one function,
// ApplySavedPlan, whichever the plan is kept in; the sink is the only part that differs.
type SavedSink interface {
	// Applied records the changes the apply finished, with the changes that were left to
	// apply when it began.
	Applied(ctx context.Context, applied []string, txn string, pending []string) error
	// PartlyApplied records the changes that an apply finished before it failed or was
	// interrupted.
	PartlyApplied(ctx context.Context, applied []string, txn string) error
	// Next says what to do with the rest of the plan once the applied changes are
	// recorded: it looks at the plan as it stands then, since the records that the apply
	// changed may have made the rest stale. again is the command that applies the plan
	// again. An empty answer is the default of ApplySavedPlan.
	Next(ctx context.Context, again string) string
}

// SavedApplyOptions say how a saved plan is applied.
type SavedApplyOptions struct {
	// Only limits the apply to these addresses (and what they need).
	Only []string
	// Name says what the plan is called in what is said about it: the path of the file,
	// or the id of the stored plan.
	Name string
	// Again is the command that applies the rest of the plan, without Only.
	Again string
	// Selected is called before the apply starts when Only selects part of what is
	// left, with the number of changes selected and of changes left.
	Selected func(selected, left int)
	// Sink records what the apply did.
	Sink SavedSink
}

// SavedApply is what applying a saved plan did.
type SavedApply struct {
	// NothingLeft is true when the plan had nothing left to apply: no apply ran.
	NothingLeft bool
	// Result is what the apply did. After a failed apply it holds what was done before it.
	Result *ApplyResult
	// Destroy says whether the plan is a destroy plan.
	Destroy bool
	// Left is the number of changes of the plan that remain after the apply.
	Left int
	// Partial is set when the apply failed or was interrupted after it applied some
	// of the changes. ApplySavedPlan returns the error of the apply with it.
	Partial *PartialApply
}

// ApplySavedPlan applies what is left of a plan that was saved, in a file or in the
// plan store, with the engine that is set up for it: every change is checked against
// the saved one. This is the one sequence for both; the sink says where progress is kept.
// An apply that fails or is interrupted records the changes it did finish, and the error
// it returns is the error of the apply.
func ApplySavedPlan(ctx context.Context, eng *engine.Engine, pf *engine.PlanFile, o SavedApplyOptions) (*SavedApply, error) {
	plan, err := eng.LoadPlanFor(pf, o.Only)
	if err != nil {
		return nil, engine.WithCode(engine.ExitStale, err)
	}
	res := &SavedApply{Destroy: plan.Destroy}
	remaining := plan.Remaining()
	if !pf.LeftToApply(remaining) {
		res.NothingLeft = true
		return res, nil
	}
	// The saved plan is judged whole, whatever Only selects, so that a limit on the
	// size of a plan cannot be passed by applying it in stages.
	eng.SetPlanFile(pf)
	selected := remaining
	if len(o.Only) > 0 {
		selected, err = plan.Subset(o.Only)
		if err != nil {
			return nil, err
		}
		if o.Selected != nil {
			o.Selected(selected.PendingCount(), remaining.PendingCount())
		}
	}
	applied, err := eng.ApplySaved(ctx, selected)
	res.Result = applied
	// Progress is recorded even when the caller's context is cancelled: the changes are applied.
	background := context.WithoutCancel(ctx)
	if err != nil {
		if applied != nil && len(applied.Applied) > 0 {
			again := o.Again
			if len(o.Only) > 0 {
				again += " --only " + engine.ShellWord(strings.Join(o.Only, ","))
			}
			res.Partial = recordSavedPartial(background, o, again, applied, remaining.PendingAddrs())
		}
		return res, err
	}
	var addrs []string
	for _, ch := range selected.Changes {
		addrs = append(addrs, ch.Addr)
	}
	res.Left = remaining.PendingCount() - selected.PendingCount()
	if err := o.Sink.Applied(background, addrs, applied.Txn, remaining.PendingAddrs()); err != nil {
		return res, savedRecordErr(applied.Txn, o.Name, err)
	}
	return res, nil
}

func savedRecordErr(txn, name string, err error) error {
	return fmt.Errorf("the changes were applied (txn %s) but the progress of %s could not be recorded: %w", txn, name, err)
}

// recordSavedPartial records in the saved plan the changes that an apply finished before
// it failed or was interrupted. The number is that of the changes of the plan, which
// pending lists, and not of the data sources and records that the apply also recorded: it
// is the number that "plan list" gives.
func recordSavedPartial(ctx context.Context, o SavedApplyOptions, again string, res *ApplyResult, pending []string) *PartialApply {
	isChange := map[string]bool{}
	for _, a := range pending {
		isChange[a] = true
	}
	pa := &PartialApply{}
	for _, a := range res.Applied {
		if isChange[a] {
			pa.Count++
		}
	}
	if err := o.Sink.PartlyApplied(ctx, res.Applied, res.Txn); err != nil {
		pa.RecordErr = savedRecordErr(res.Txn, o.Name, err)
		return pa
	}
	if pa.Next = o.Sink.Next(ctx, again); pa.Next == "" {
		pa.Next = fmt.Sprintf("run \"%s\" again to apply the rest; if the resource that failed was changed, the plan is stale and must be made again", again)
	}
	return pa
}
