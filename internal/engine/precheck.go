package engine

import (
	"context"
	"fmt"
	"sort"
)

// wholeCheck is the dry walk that a saved plan is checked with before an apply
// changes anything: every resource of the plan is planned again, from the records
// and the reads the plan holds, and each answer is compared with the saved change.
// diffs holds the changes that differ, by address.
type wholeCheck struct {
	diffs map[string]*savedDiff
}

// checking returns the dry walk that runs, nil when none does.
func (e *Engine) checking() *wholeCheck {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.check
}

// savedImport is the change of the saved plan that imports the object of the node at
// addr, while the plan is being checked; nil otherwise.
func (e *Engine) savedImport(addr string) *Change {
	if e.checking() == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if sc := e.saved[addr]; sc != nil && sc.Imported && sc.Prior.Type() != nil && !sc.Prior.IsNull() {
		return sc
	}
	return nil
}

// noteChecked compares the change that the dry walk planned for a resource with the
// saved one and notes a difference.
func (e *Engine) noteChecked(w *wholeCheck, ch *Change) {
	if ch.Deferred || ch.Action == ActionDefer {
		// What waits for a value of the apply is planned when that value is known; the
		// check of each change during the apply covers it.
		return
	}
	if d := e.diffSaved(ch); d != nil {
		e.mu.Lock()
		w.diffs[ch.Addr] = d
		e.mu.Unlock()
	}
}

// checkWhole plans every resource of a saved plan again before the apply changes any
// of them, and refuses the whole apply when one would not be planned as the plan
// says. The apply walks the resources in order and plans each one when the ones it
// reads have been applied; a resource that differs at the end of the walk would find
// the estate half changed. The dry walk plans them all the way the plan was made,
// with what the apply changes still unknown, and compares each with the saved
// change. It reads nothing from the providers but what planning a change asks of
// them, and writes nothing. The check of each resource during the apply stays, for
// what changes in between.
func (e *Engine) checkWhole(ctx context.Context, plan *Plan, sc *applyScope) error {
	if e.saved == nil || e.approved || e.Opts.Destroy || sc == nil {
		return nil
	}
	w := &wholeCheck{diffs: map[string]*savedDiff{}}
	// The check says nothing of its own: what it opens or reads is said by the apply.
	refresh, progress, event := e.Opts.Refresh, e.Opts.Progress, e.Opts.Event
	e.Opts.Refresh, e.Opts.Progress, e.Opts.Event = RefreshNone, func(string, ...any) {}, nil
	leave := e.enterApply(plan, sc)
	e.mu.Lock()
	e.check = w
	e.mu.Unlock()
	cctx, done := e.phase(ctx, "check the saved plan")
	_, err := e.run(cctx, false)
	e.mu.Lock()
	e.check = nil
	e.mu.Unlock()
	leave()
	e.Opts.Refresh, e.Opts.Progress, e.Opts.Event = refresh, progress, event
	switch {
	case len(w.diffs) > 0:
		done(nil)
		return e.differing(w)
	case err != nil:
		err = fmt.Errorf("the saved plan could not be checked before anything was changed: %w", err)
		done(err)
		return err
	}
	done(nil)
	return nil
}

// differing is the error of a saved plan that the dry walk found stale: every
// resource that would be planned differently, with the attributes that differ.
func (e *Engine) differing(w *wholeCheck) error {
	addrs := make([]string, 0, len(w.diffs))
	for addr := range w.diffs {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	reasons := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		reasons = append(reasons, addr+": "+w.diffs[addr].ofSaved)
	}
	return WithCode(ExitStale, &ErrStalePlan{Reasons: reasons, Note: "Nothing was changed: the whole plan was checked before the apply began."})
}
