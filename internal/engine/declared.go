package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pravo-systems/pravo/internal/state"
)

// What the specification declares about the state, as opposed to the objects: a record that
// $from moves to another address, a provider that $from renames, an object that $import
// brings in. A plan does not write them. It stages them (see state.Store.Stage), so that the nodes it
// plans see the record at the new address and the object that was read, and it lists them in
// the plan. The apply, the one that follows a plan in the same command and the one of a saved
// plan alike, writes them first, from what the plan says, and then walks the nodes.

// declared reports whether the change is a record that the apply is to write, and no object
// that it is to change: a move, or the import of an object.
func (c *Change) declared() bool {
	return !c.Applied && (c.Action == ActionMove || c.Imported)
}

// HasDeclared reports whether the plan has a move or an import that an apply is to write.
func (p *Plan) HasDeclared() bool {
	if len(p.Relabels) > 0 {
		return true
	}
	for _, c := range p.Changes {
		if c.declared() {
			return true
		}
	}
	return false
}

// HasWrites reports whether applying the plan would change an object, or write a record that the
// specification declares: a move, an import. A plan for which it is false has nothing for an
// apply to do but the records and outputs that follow from the others.
func (p *Plan) HasWrites() bool { return p.HasChanges() || p.HasDeclared() }

// touched notes that a record was written by the apply of a declaration, so that the plan can
// be brought up to date with its new revision.
func (e *Engine) touched(addr string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.touchedAddrs == nil {
		e.touchedAddrs = map[string]bool{}
	}
	e.touchedAddrs[addr] = true
}

// retarget gives the plan the revision of the records that the declarations wrote: the plan saw
// them as they were staged, and the checks that follow compare it with what the backend holds.
func (e *Engine) retarget(plan *Plan) {
	e.mu.Lock()
	addrs := make([]string, 0, len(e.touchedAddrs))
	for a := range e.touchedAddrs {
		addrs = append(addrs, a)
	}
	e.touchedAddrs = nil
	e.mu.Unlock()
	sort.Strings(addrs)
	for _, addr := range addrs {
		rev, gen := e.versionOf(addr)
		for _, ch := range plan.Changes {
			if ch.Addr == addr && ch.Action != ActionMove {
				ch.PriorRevision, ch.PriorGeneration = rev, gen
			}
		}
		if _, ok := plan.Inputs[addr]; ok {
			plan.Inputs[addr] = rev
			if plan.InputGenerations != nil {
				if gen != "" {
					plan.InputGenerations[addr] = gen
				} else {
					delete(plan.InputGenerations, addr)
				}
			}
		}
	}
}

// commitMoves writes the renames of the plan: the provider labels of the records, and the
// records that $from moves. A record that is not as the plan saw it stops the apply.
func (e *Engine) commitMoves(plan *Plan) error {
	e.State.DropStaged()
	if _, err := e.relabelProviders(false); err != nil {
		return err
	}
	var moves []*Change
	for _, ch := range plan.Changes {
		if ch.Action == ActionMove && !ch.Applied {
			moves = append(moves, ch)
		}
	}
	if len(moves) == 0 {
		return nil
	}
	addrs := make([]string, 0, 2*len(moves))
	for _, ch := range moves {
		addrs = append(addrs, ch.MovedFrom, ch.Addr)
	}
	have, err := e.State.Versions(addrs)
	if err != nil {
		return err
	}
	var reasons []string
	for _, ch := range moves {
		// A record that is at its new address and not at the old one was moved already.
		if have[ch.MovedFrom] == (state.Version{}) && have[ch.Addr] != (state.Version{}) {
			continue
		}
		if reason := staleReason(ch.MovedFrom, have[ch.MovedFrom], state.Version{Revision: ch.PriorRevision, Generation: ch.PriorGeneration}, false); reason != "" {
			reasons = append(reasons, reason)
		}
	}
	if len(reasons) > 0 {
		if e.saved != nil {
			return WithCode(ExitStale, &ErrStalePlan{Reasons: reasons})
		}
		return WithCode(ExitStale, fmt.Errorf("the plan changed while it waited; run \"pravo plan\" again:\n  - %s", strings.Join(reasons, "\n  - ")))
	}
	for _, ch := range moves {
		if _, err := e.moveNode(ch.MovedFrom, ch.Addr, e.Root.Resource(ch.Addr), false); err != nil {
			return err
		}
	}
	e.retarget(plan)
	return nil
}

// commitImports writes the records of the objects that $import brought in, as the plan read
// them. The apply holds the lock of a node that it writes; the others are locked here.
func (e *Engine) commitImports(plan *Plan) error {
	held := map[string]bool{}
	if e.held != nil {
		for _, a := range e.held.held() {
			held[a] = true
		}
	}
	for _, ch := range plan.Changes {
		if !ch.Imported || ch.Applied || ch.Action == ActionMove || ch.Prior.Type() == nil || ch.Prior.IsNull() {
			continue
		}
		if e.State.Get(ch.Addr) != nil {
			continue // somebody wrote the record: the plan was stale, or the object is in the state already
		}
		r := e.Root.Resource(ch.Addr)
		if r == nil || ch.Schema == nil {
			return fmt.Errorf("%s: the import has no resource to be written for", ch.Addr)
		}
		n, err := e.importedRecord(r, ch.Schema, ch.Prior, ch.PriorPrivate)
		if err != nil {
			return err
		}
		if !held[ch.Addr] {
			if ok, _ := e.State.TryLock(ch.Addr, e.Opts.Command+" (import)", e.txnID()); !ok {
				return fmt.Errorf("%s: cannot import while another command holds its lock", ch.Addr)
			}
		}
		err = e.State.Verify(ch.Addr)
		if err == nil {
			err = e.State.Set(n, e.txnID())
		}
		if !held[ch.Addr] {
			_ = e.State.Unlock(ch.Addr)
		}
		if err != nil {
			return fmt.Errorf("%s: cannot import: %w", ch.Addr, err)
		}
		e.txn.Record(ch.Addr, "import", 0, n.Revision)
		e.recordDiff(ch.Addr, ch.Schema, nil, n.Attributes, ch.Derived)
		e.Opts.Progress("%s: imported", ch.Addr)
		e.touched(ch.Addr)
	}
	e.retarget(plan)
	return nil
}

// moveReason says why a saved move cannot be carried out: the record at the old address is not the
// one the plan saw. A record that is at the new address and not at the old one was moved already.
func (e *Engine) moveReason(sc savedChange) string {
	have, err := e.State.Versions([]string{sc.MovedFrom, sc.Addr})
	if err != nil {
		return fmt.Sprintf("%s: the record could not be read: %v", sc.MovedFrom, err)
	}
	if have[sc.MovedFrom] == (state.Version{}) && have[sc.Addr] != (state.Version{}) {
		return ""
	}
	return staleReason(sc.MovedFrom, have[sc.MovedFrom], state.Version{Revision: sc.PriorRevision, Generation: sc.PriorGeneration}, false)
}
