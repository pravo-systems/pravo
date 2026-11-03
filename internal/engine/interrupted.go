package engine

import (
	"fmt"
	"slices"
	"strings"
)

// checkInterruptedCreates refuses an apply that would create a node whose last
// trace is an apply that stopped while it was creating it: the lock that apply took
// is still there, marked as taken for a create, and the state has no record of the node. If the provider had accepted the create before
// the apply stopped, the object exists in the cloud and nothing tracks it, and
// creating the node again makes a second one. The apply goes no further, before
// it writes anything, and names the node, the apply that stopped and the ways out:
// find the object and adopt it, or say with Options.AllowCreate that the create
// never reached the cloud.
func (e *Engine) checkInterruptedCreates(plan *Plan) error {
	suspects, err := e.State.Suspects()
	if err != nil || len(suspects) == 0 {
		return nil
	}
	var refused []string
	for _, ch := range plan.Changes {
		if ch.Action != ActionCreate || ch.Mode != "managed" || ch.Applied {
			continue
		}
		if l, stale := suspects[ch.Addr]; !stale || !l.Creating || e.State.Get(ch.Addr) != nil {
			continue
		}
		if slices.Contains(e.Opts.AllowCreate, ch.Addr) {
			e.Opts.Progress("Warning: %s: creating it although %s, as --allow-create said", ch.Addr, stoppedApply(suspects[ch.Addr]))
			continue
		}
		refused = append(refused, ch.Addr)
	}
	if len(refused) == 0 {
		return nil
	}
	slices.Sort(refused)
	var b strings.Builder
	for _, addr := range refused {
		fmt.Fprintf(&b, "%s: %s, and the state has no record of it\n", addr, stoppedApply(suspects[addr]))
	}
	first := refused[0]
	return WithCode(ExitRefused, fmt.Errorf("apply refused: it would create a node that an interrupted apply may already have created.\n%sIf the create reached the cloud the object exists but is not tracked, and creating it again makes a second one. Nothing was changed. Either:\n  - find the object and adopt it: pravo discover (with a #Discovery rule), or pravo import %s ID; the lock is cleared by the next apply; or\n  - if you know the create never reached the cloud, create it anyway: pravo apply --allow-create %s", b.String(), first, first))
}
