package engine

import (
	"context"
	"fmt"

	"cuelang.org/go/cue"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

// ActionMove marks a record that a $from declaration renames. A plan stages the move and the
// apply writes it.
const ActionMove Action = "move"

// applyMoves goes through the $from declarations and returns one change per move.
// A plan stages them (see state.Store.Stage): it does not write the state, but the
// nodes it plans see the record at its new address. An apply carries them out. A move
// whose source no longer exists and whose target already does is complete and
// produces nothing. A prefix move expands to one move per record below the old path.
func (e *Engine) applyMoves(ctx context.Context, stage bool) ([]*Change, error) {
	var out []*Change
	for _, mv := range e.Root.Moves {
		pairs, err := e.expandMove(mv)
		if err != nil {
			return nil, err
		}
		for _, pr := range pairs {
			ch, err := e.moveNode(pr.from, pr.to, pr.target, stage)
			if err != nil {
				return nil, err
			}
			if ch != nil {
				out = append(out, ch)
			}
		}
	}
	return out, nil
}

type movePair struct {
	from, to string
	target   *config.Resource
}

// expandMove turns a $from declaration into the single moves it stands for.
func (e *Engine) expandMove(mv config.Move) ([]movePair, error) {
	if !mv.Prefix {
		return []movePair{{from: mv.From, to: mv.To, target: e.Root.Resource(mv.To)}}, nil
	}
	fromP, toP := cue.ParsePath(mv.From), cue.ParsePath(mv.To)
	if fromP.Err() != nil {
		return nil, fmt.Errorf("$from %s -> %s: %q is not a path", mv.From, mv.To, mv.From)
	}
	fromSels := fromP.Selectors()
	var pairs []movePair
	for _, n := range e.State.List() {
		sels := cue.ParsePath(n.Address()).Selectors()
		if len(sels) <= len(fromSels) || !hasPrefix(sels, fromSels) {
			continue
		}
		to := cue.MakePath(append(append([]cue.Selector{}, toP.Selectors()...), sels[len(fromSels):]...)...).String()
		target := e.Root.Resource(to)
		if target == nil {
			return nil, fmt.Errorf("$from %s -> %s: the record %s would move to %s, but no resource is declared there", mv.From, mv.To, n.Address(), to)
		}
		pairs = append(pairs, movePair{from: n.Address(), to: to, target: target})
	}
	return pairs, nil
}

func hasPrefix(sels, prefix []cue.Selector) bool {
	if len(sels) < len(prefix) {
		return false
	}
	for i := range prefix {
		if sels[i].String() != prefix[i].String() {
			return false
		}
	}
	return true
}

// moveNode renames one record, or with stage only in memory. It returns nil when there
// is nothing to do.
func (e *Engine) moveNode(fromAddr, toAddr string, target *config.Resource, stage bool) (*Change, error) {
	from, to := e.State.Get(fromAddr), e.State.Get(toAddr)
	switch {
	case from == nil && to == nil:
		return nil, nil // nothing to move; the resource will be created
	case from == nil:
		return nil, nil // already moved
	case to != nil:
		return nil, fmt.Errorf("$from %s -> %s: both addresses exist in state; remove one with \"pravo state rm\"", fromAddr, toAddr)
	}
	if target != nil && (from.Mode != target.Mode || from.Type != target.Type) {
		return nil, fmt.Errorf("$from %s -> %s: the record is a %s %s but %s is declared as a %s %s; the type must stay the same", fromAddr, toAddr, from.Mode, from.Type, toAddr, target.Mode, target.Type)
	}
	if stage {
		return e.stageMove(from, toAddr, target), nil
	}
	var locked []string
	defer func() {
		for _, a := range locked {
			_ = e.State.Unlock(a)
		}
	}()
	for _, a := range []string{fromAddr, toAddr} {
		if err := e.State.Lock(a, e.Opts.Command+" (moved)", e.txnID()); err != nil {
			return nil, fmt.Errorf("$from %s -> %s: %w", fromAddr, toAddr, err)
		}
		locked = append(locked, a)
	}
	// The records that depend on the one that moves are written too.
	deps, releaseDeps, err := e.State.LockDependents(fromAddr, e.Opts.Command+" (moved)", e.txnID())
	if err != nil {
		return nil, fmt.Errorf("$from %s -> %s: %w", fromAddr, toAddr, err)
	}
	defer releaseDeps()
	// With the locks held, the records are read from the backend again: a record
	// that another command wrote since this one was read is not moved from a copy.
	for _, a := range []string{fromAddr, toAddr} {
		if err := e.State.Verify(a); err != nil {
			return nil, fmt.Errorf("$from %s -> %s: %w", fromAddr, toAddr, err)
		}
	}
	moved := *from
	moved.Addr = toAddr
	if target != nil {
		moved.Name = target.Name
	}
	moved.Revision = from.Revision
	before := from.Revision
	if _, err := e.State.Remove(fromAddr); err != nil {
		return nil, err
	}
	if err := e.State.Set(&moved, e.txnID()); err != nil {
		return nil, err
	}
	e.touched(toAddr)
	for _, d := range deps {
		other := e.State.Get(d)
		changed := false
		for i, dep := range other.Dependencies {
			if dep == fromAddr {
				other.Dependencies[i] = toAddr
				changed = true
			}
		}
		if changed {
			if err := e.State.Set(other, e.txnID()); err != nil {
				return nil, err
			}
			e.touched(d)
		}
	}
	e.txn.Record(fromAddr, "move-from", before, 0)
	e.txn.Record(toAddr, "move-to", 0, moved.Revision)
	e.Opts.Progress("%s: moved to %s", fromAddr, toAddr)
	e.count(func(st *Stats) { st.Moved++ })
	return &Change{Addr: toAddr, Mode: moved.Mode, Type: moved.Type, Name: moved.Name, Action: ActionMove, MovedFrom: fromAddr, ProviderKey: moved.ProviderKey}, nil
}

// stageMove is the move of a plan: the record is at its new address, and gone from the old
// one, in memory. What the plan says of the change is what an apply checks: the version of the
// record that moves.
func (e *Engine) stageMove(from *state.Resource, toAddr string, target *config.Resource) *Change {
	fromAddr := from.Address()
	deps := e.State.Dependents(fromAddr)
	moved := *from
	moved.Addr = toAddr
	if target != nil {
		moved.Name = target.Name
	}
	e.State.StageRemove(fromAddr)
	e.State.Stage(&moved)
	for _, d := range deps {
		other := *e.State.Get(d)
		other.Dependencies = append([]string(nil), other.Dependencies...)
		changed := false
		for i, dep := range other.Dependencies {
			if dep == fromAddr {
				other.Dependencies[i] = toAddr
				changed = true
			}
		}
		if changed {
			e.State.Stage(&other)
		}
	}
	e.count(func(st *Stats) { st.Moved++ })
	return &Change{Addr: toAddr, Mode: moved.Mode, Type: moved.Type, Name: moved.Name, Action: ActionMove, MovedFrom: fromAddr, ProviderKey: moved.ProviderKey,
		PriorRevision: from.Revision, PriorGeneration: from.Generation}
}

// importResource reads the object that an $import names: the provider is asked for it by id
// or identity, and what it says is the prior state of the resource, which the plan compares
// with the configuration. The record is staged, in memory: the apply writes it (see
// commitImport).
func (e *Engine) importResource(ctx context.Context, prov plugin.Provider, r *config.Resource, s *schema.Schema, imp *config.Import, ch *Change) (tftypes.Value, []byte, error) {
	addr := r.Address()
	ps := e.Schemas[r.LocalProvider()]
	types := map[string]tftypes.Type{}
	for name, rs := range ps.AllResources() {
		types[name] = rs.Block.Type()
	}
	label := imp.Label()
	if imp.Identity != nil {
		e.Opts.Progress("%s: Reading the object of the identity %q...", addr, label)
	} else {
		e.Opts.Progress("%s: Reading the object of id %q...", addr, label)
	}
	imported, diags, err := prov.ImportResourceState(ctx, r.Type, importTarget(imp), types)
	if err != nil {
		return tftypes.Value{}, nil, fmt.Errorf("%s: importing %q: %w", addr, label, err)
	}
	if err := diags.Err(); err != nil {
		return tftypes.Value{}, nil, fmt.Errorf("%s: importing %q: %w", addr, label, err)
	}
	var found *plugin.ImportedResource
	for i := range imported {
		if imported[i].TypeName == r.Type {
			found = &imported[i]
			break
		}
	}
	if found == nil {
		return tftypes.Value{}, nil, fmt.Errorf("%s: the provider returned no %s for %q", addr, r.Type, label)
	}
	st, private, diags, err := prov.ReadResource(ctx, r.Type, found.State, found.Private)
	if err != nil {
		return tftypes.Value{}, nil, fmt.Errorf("%s: reading imported object: %w", addr, err)
	}
	if err := diags.Err(); err != nil {
		return tftypes.Value{}, nil, fmt.Errorf("%s: reading imported object: %w", addr, err)
	}
	if st.IsNull() {
		return tftypes.Value{}, nil, fmt.Errorf("%s: no object %q exists", addr, label)
	}
	n, err := e.importedRecord(r, s, st, private)
	if err != nil {
		return tftypes.Value{}, nil, err
	}
	e.State.Stage(n)
	ch.Imported, ch.ImportID, ch.ImportIdentity = true, label, imp.Identity != nil
	e.mu.Lock()
	if e.imported == nil {
		e.imported = map[string]importMark{}
	}
	e.imported[addr] = importMark{label: label, identity: imp.Identity != nil}
	e.mu.Unlock()
	e.count(func(st *Stats) { st.Imported++ })
	return st, private, nil
}

// importedRecord is the record of an object that an $import brought in: what the provider read.
func (e *Engine) importedRecord(r *config.Resource, s *schema.Schema, st tftypes.Value, private []byte) (*state.Resource, error) {
	attrs, err := convert.ToJSON(st)
	if err != nil {
		return nil, fmt.Errorf("%s: imported state: %w", r.Address(), err)
	}
	return &state.Resource{
		Addr: r.Address(), Mode: "managed", Type: r.Type, Name: r.Name, Provider: e.providerSource(r.ProviderKey), ProviderKey: r.ProviderKey,
		ProviderProtocol: e.providerProtocol(r.ProviderKey),
		ProviderVersion:  e.providerVersion(r.ProviderKey), SchemaVersion: s.Version, Attributes: attrs, Private: private,
		ProviderConfig: e.stamp(r.ProviderKey, ""), RefreshedAt: state.Now(),
	}, nil
}

// relabelProviders carries out the provider $from declarations: a record
// written under an address the provider used to have is relabelled with its
// current address. A record under some other address than the provider's own
// is an error that says how to declare the change. A plan stages the new label: only an
// apply writes it.
func (e *Engine) relabelProviders(stage bool) ([]Relabel, error) {
	var labels []Relabel
	for _, n := range e.State.List() {
		if n.Provider == "" {
			continue
		}
		req, ok := e.Root.Providers[config.LocalName(n.ProviderKey)]
		if !ok || req.Implied || n.Provider == req.Source.String() {
			continue
		}
		known := false
		for _, was := range req.Was {
			known = known || was.String() == n.Provider
		}
		if !known {
			return nil, fmt.Errorf("%s was written by provider %s, but the provider is now %s; add $from: %q to the provider", n.Address(), n.Provider, req.Source.String(), n.Provider)
		}
		addr := n.Address()
		if stage {
			moved := *n
			moved.Provider = req.Source.String()
			e.State.Stage(&moved)
			labels = append(labels, Relabel{Addr: addr, From: n.Provider, To: req.Source.String()})
			continue
		}
		if err := e.State.Lock(addr, e.Opts.Command+" (provider moved)", e.txnID()); err != nil {
			return nil, fmt.Errorf("provider $from %s: %w", n.Provider, err)
		}
		if err := e.State.Verify(addr); err != nil {
			_ = e.State.Unlock(addr)
			return nil, fmt.Errorf("provider $from %s: %w", n.Provider, err)
		}
		moved := *n
		moved.Provider = req.Source.String()
		before := n.Revision
		if err := e.State.Set(&moved, e.txnID()); err != nil {
			return nil, err
		}
		e.txn.Record(addr, "provider-move", before, moved.Revision)
		_ = e.State.Unlock(addr)
		e.Opts.Progress("%s: provider %s -> %s", addr, n.Provider, req.Source.String())
	}
	return labels, nil
}

// Relabel is a record whose provider a provider $from renames.
type Relabel struct {
	Addr string `json:"addr"`
	From string `json:"from"`
	To   string `json:"to"`
}

// importedID returns the id of the import that this plan made at addr.
func (e *Engine) importedID(addr string) (importMark, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	mark, ok := e.imported[addr]
	return mark, ok
}

// importMark is what an import named: an id, or an identity written as pairs.
type importMark struct {
	label    string
	identity bool
}

// importTarget is what the provider is asked to import.
func importTarget(imp *config.Import) plugin.ImportTarget {
	return plugin.ImportTarget{ID: imp.ID, Identity: imp.Identity}
}
