package engine

import (
	"fmt"
	"maps"
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
	return WithCode(ExitRefused, fmt.Errorf("apply refused: it would create %s that an interrupted apply may already have created.\n%sIf the create reached the cloud the object exists but is not tracked, and creating it again makes a second one. Nothing was changed. For each node above, either:\n  - find the object and adopt it: pravo discover (with a #Discovery rule), or %s; the lock is cleared by the next apply; or\n  - if you know the create never reached the cloud, create it anyway: %s", nodes(len(refused)), b.String(), e.importCommand(refused[0], "ID"), e.allowCreateCommand(refused)))
}

func nodes(n int) string {
	if n == 1 {
		return "a node"
	}
	return "nodes"
}

// envFlag is the flag that names the environment of the specification, with a space before
// it, or "" for a specification that declares none.
func (e *Engine) envFlag() string {
	if e.Root != nil && e.Root.Environment != "" {
		return " --env " + e.Root.Environment
	}
	return ""
}

// importCommand is the command that adopts the object with id as the node addr.
func (e *Engine) importCommand(addr, id string) string {
	return "pravo import " + addr + " " + id + e.envFlag()
}

// allowCreateCommand is the apply that creates addrs although an apply stopped while it
// created them.
func (e *Engine) allowCreateCommand(addrs []string) string {
	cmd := "pravo apply" + e.envFlag()
	for _, a := range addrs {
		cmd += " --allow-create " + a
	}
	return cmd
}

// warnUntracked warns of the nodes with the lock of an apply that stopped while it created
// them, that have no record, and that the plan does not import: if the create reached the
// cloud the object exists but nothing tracks it, and an apply would create it again.
func (e *Engine) warnUntracked(plan *Plan) {
	imported := map[string]bool{}
	for _, ch := range plan.Changes {
		if ch.Imported {
			imported[ch.Addr] = true
		}
	}
	for _, addr := range slices.Sorted(maps.Keys(e.suspects)) {
		if e.State.Get(addr) != nil || imported[addr] {
			continue
		}
		e.Opts.Progress("Warning: %s: %s, and the state has no record of it: if the create reached the cloud the object exists but is not tracked, and an apply would create it again. Look for it with pravo discover, or import it (%s), before you apply", addr, stoppedApply(e.suspects[addr]), e.importCommand(addr, "ID"))
	}
}
