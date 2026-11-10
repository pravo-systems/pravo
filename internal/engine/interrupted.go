package engine

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/pravo-systems/pravo/internal/state"
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
	if err != nil {
		return nil
	}
	replacing := e.orphanedReplacements(plan, e.pendingNotes())
	if len(suspects) == 0 && len(replacing) == 0 {
		return nil
	}
	var refused, replaced []string
	for _, ch := range plan.Changes {
		if p, ok := replacing[ch.Addr]; ok && !ch.Applied && ch.Action != ActionDelete && ch.Action != ActionMove {
			if slices.Contains(e.Opts.AllowCreate, ch.Addr) {
				e.Opts.Progress("Warning: %s: creating the replacement although %s, as --allow-create said", ch.Addr, stoppedReplacement(p))
				continue
			}
			replaced = append(replaced, ch.Addr)
			continue
		}
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
	if len(refused) == 0 && len(replaced) == 0 {
		return nil
	}
	slices.Sort(refused)
	slices.Sort(replaced)
	if len(refused) == 0 {
		return e.refuseReplacements(replaced, replacing)
	}
	var b strings.Builder
	for _, addr := range refused {
		fmt.Fprintf(&b, "%s: %s, and the state has no record of it\n", addr, stoppedApply(suspects[addr]))
	}
	for _, addr := range replaced {
		fmt.Fprintf(&b, "%s: %s\n", addr, stoppedReplacement(replacing[addr]))
	}
	all := append(slices.Clone(refused), replaced...)
	return WithCode(ExitRefused, fmt.Errorf("apply refused: it would create %s that an interrupted apply may already have created.\n%sIf the create reached the cloud the object exists but is not tracked, and creating it again makes a second one. Nothing was changed. For each node above, either:\n  - find the object and adopt it: pravo discover (with a #Discovery rule), or %s; the lock is cleared by the next apply; or\n  - if you know the create never reached the cloud, create it anyway: %s", nodes(len(all)), b.String(), e.importCommand(all[0], "ID"), e.allowCreateCommand(all)))
}

// pendingNotes is one reading of the notes of interrupted creates, and of the locks that say
// whether the command of a note still runs. A plan or an apply asks about every node that it
// deletes, replaces or creates over a record, and one listing answers for all of them: a
// read for each node would cost a destroy of five hundred nodes five hundred round trips,
// one after the other.
type pendingNotes struct {
	st     *state.Store
	all    []*state.PendingCreate
	byAddr map[string]*state.PendingCreate
	// locks is read when the first note asks, which a store with no note never does.
	locks    []state.Lock
	locksErr error
	asked    bool
}

func (e *Engine) pendingNotes() *pendingNotes {
	n := &pendingNotes{st: e.State, byAddr: map[string]*state.PendingCreate{}}
	n.all, _ = e.State.PendingCreates()
	for _, p := range n.all {
		n.byAddr[p.Addr] = p
	}
	return n
}

// orphaned is Store.Orphaned for the note p of a node whose record is rec.
func (n *pendingNotes) orphaned(p *state.PendingCreate, rec *state.Resource) (bool, error) {
	if rec != nil && !p.Holds(rec) {
		return false, nil
	}
	if !n.asked {
		n.locks, n.locksErr = n.st.Locks()
		n.asked = true
	}
	if n.locksErr != nil {
		return false, n.locksErr
	}
	return p.OrphanedAmong(rec, n.locks), nil
}

// orphanedReplacements returns, by address, the notes of replacements that create before
// they destroy, that a command left when it stopped, for the nodes the plan replaces that way.
// A note the record has outgrown is deleted.
func (e *Engine) orphanedReplacements(plan *Plan, notes *pendingNotes) map[string]*state.PendingCreate {
	if len(notes.all) == 0 {
		return nil
	}
	var out map[string]*state.PendingCreate
	// A node that the plan moves has its note under the address it comes from, until the
	// apply carries it along.
	movedFrom := map[string]string{}
	for _, ch := range plan.Changes {
		if ch.Action == ActionMove && ch.MovedFrom != "" {
			movedFrom[ch.Addr] = ch.MovedFrom
		}
	}
	for _, ch := range plan.Changes {
		if ch.Mode != "managed" || ch.Applied || (ch.Action != ActionReplace || !ch.CreateBeforeDestroy) && ch.Action != ActionDelete && ch.Action != ActionCreate {
			continue
		}
		if ch.Action == ActionCreate && e.State.Get(ch.Addr) == nil {
			continue // a create over no record is marked on its lock
		}
		p := notes.byAddr[ch.Addr]
		if p == nil && movedFrom[ch.Addr] != "" {
			p = notes.byAddr[movedFrom[ch.Addr]]
		}
		if p == nil {
			continue
		}
		orphan, err := notes.orphaned(p, e.State.Get(ch.Addr))
		if err != nil {
			continue
		}
		if !orphan {
			if rec := e.State.Get(ch.Addr); rec != nil && !p.Holds(rec) {
				_ = e.State.ClearPendingCreate(ch.Addr)
			}
			continue
		}
		if out == nil {
			out = map[string]*state.PendingCreate{}
		}
		out[ch.Addr] = p
	}
	return out
}

func stoppedReplacement(p *state.PendingCreate) string {
	who := "txn " + p.Txn
	if p.Host != "" {
		who += " on host " + p.Host
	}
	if p.Recreate {
		return fmt.Sprintf("an apply (%s) stopped while it created the node again, and the state still records only the object it had before (id %s, which was gone): the new object may exist in the cloud untracked", who, p.OldID)
	}
	return fmt.Sprintf("an apply (%s) stopped while it created the replacement, and the state still records only the object it replaces (id %s): the replacement may exist in the cloud untracked", who, p.OldID)
}

func (e *Engine) refuseReplacements(replaced []string, replacing map[string]*state.PendingCreate) error {
	var b strings.Builder
	for _, addr := range replaced {
		fmt.Fprintf(&b, "%s: %s\n", addr, stoppedReplacement(replacing[addr]))
	}
	return WithCode(ExitRefused, fmt.Errorf("apply refused: it would create %s that an interrupted apply may already have created.\n%sIf the create reached the cloud the replacement exists but is not tracked, and creating it again makes a second one. Nothing was changed. For each node above, either:\n  - adopt the replacement in place of the old object: pravo state rm %s, then %s (%s); or\n  - if you know the create never reached the cloud, create it anyway: %s", nodes(len(replaced)), b.String(), replaced[0], e.importCommand(replaced[0], "ID"), e.oldObjectFate(replaced, replacing), e.allowCreateCommand(replaced)))
}

// oldObjectFate says what happens to the old objects after the replacements are adopted: a
// node whose old object is also kept as a deposed record has it destroyed by the next apply, and
// for a node with no such record (nothing but the stopped command knew of the old object) it is
// the person's to destroy.
func (e *Engine) oldObjectFate(replaced []string, replacing map[string]*state.PendingCreate) string {
	var kept, mine []string
	for _, addr := range replaced {
		if p := replacing[addr]; p != nil && e.hasDeposed(addr, p.OldID) {
			kept = append(kept, addr)
		} else {
			mine = append(mine, addr)
		}
	}
	switch {
	case len(mine) == 0:
		return "the next apply then destroys the old object, which the state keeps as a deposed record"
	case len(kept) == 0:
		return "the old object is then yours to destroy"
	}
	return fmt.Sprintf("the next apply then destroys the old object of %s, which the state keeps as a deposed record; the old object of %s is yours to destroy", strings.Join(kept, ", "), strings.Join(mine, ", "))
}

// hasDeposed reports whether the state keeps the object with id as a deposed record of addr.
func (e *Engine) hasDeposed(addr, id string) bool {
	if id == "" {
		return false
	}
	for _, r := range e.State.List() {
		if base, ok := deposedBase(r.Addr); ok && base == addr && r.ID() == id {
			return true
		}
	}
	return false
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
	var b strings.Builder
	b.WriteString("pravo apply" + e.envFlag())
	for _, a := range addrs {
		b.WriteString(" --allow-create " + a)
	}
	return b.String()
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
	notes := e.pendingNotes()
	replacing := e.orphanedReplacements(plan, notes)
	for _, addr := range slices.Sorted(maps.Keys(replacing)) {
		e.Opts.Progress("Warning: %s: %s. Adopt it (pravo state rm %s, then %s) or delete it in the cloud, before you apply", addr, stoppedReplacement(replacing[addr]), addr, e.importCommand(addr, "ID"))
	}
	e.warnStrayNotes(plan, notes, replacing)
	for _, addr := range slices.Sorted(maps.Keys(e.suspects)) {
		// Only a node whose create began can have an object nothing tracks; a node the stopped
		// apply had locked and never started has none.
		if e.State.Get(addr) != nil || imported[addr] || !e.suspects[addr].Creating {
			continue
		}
		e.Opts.Progress("Warning: %s: %s, and the state has no record of it: if the create reached the cloud the object exists but is not tracked, and an apply would create it again. Look for it with pravo discover, or import it (%s), before you apply", addr, stoppedApply(e.suspects[addr]), e.importCommand(addr, "ID"))
	}
}

// warnStrayNotes warns of the notes of interrupted creates for the nodes that the plan neither
// replaces, creates nor deletes (covered holds the ones it does, which the refusal speaks of):
// the replacement or the new object may exist in the cloud untracked, and a plan that
// says nothing of it lets the person forget it. Only the warning: the plan and its exit
// code are as they were. A note whose command is gone and that the record has outgrown is
// deleted.
func (e *Engine) warnStrayNotes(plan *Plan, notes *pendingNotes, covered map[string]*state.PendingCreate) {
	if len(notes.all) == 0 {
		return
	}
	movedFrom := map[string]bool{}
	for _, ch := range plan.Changes {
		if ch.Action == ActionMove && ch.MovedFrom != "" {
			movedFrom[ch.MovedFrom] = true
		}
	}
	for _, p := range notes.all {
		if covered[p.Addr] != nil || movedFrom[p.Addr] {
			continue
		}
		rec := e.State.Get(p.Addr)
		orphan, err := notes.orphaned(p, rec)
		if err != nil {
			continue
		}
		if !orphan {
			if rec != nil && !p.Holds(rec) {
				_ = e.State.ClearPendingCreate(p.Addr)
			}
			continue
		}
		e.Opts.Progress("Warning: %s: %s. Find it with pravo discover (with a #Discovery rule) or adopt it (pravo state rm %s, then %s), or if it does not exist, say so (%s), before you change this node", p.Addr, stoppedReplacement(p), p.Addr, e.importCommand(p.Addr, "ID"), "pravo state unlock --allow-create "+p.Addr+e.envFlag())
	}
}
