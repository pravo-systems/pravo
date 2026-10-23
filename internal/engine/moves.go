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

// ActionMove marks a record renamed by a "moved" declaration.
const ActionMove Action = "move"

// applyMoves carries out the $from declarations against the state, as
// the first step of the plan's transaction, and returns one change per
// move performed. A move whose source no longer exists and whose target
// already does is complete and produces nothing. A prefix move expands to
// one move per record below the old path.
func (e *Engine) applyMoves(ctx context.Context) ([]*Change, error) {
	var out []*Change
	for _, mv := range e.Root.Moves {
		pairs, err := e.expandMove(mv)
		if err != nil {
			return nil, err
		}
		for _, pr := range pairs {
			ch, err := e.moveNode(pr.from, pr.to, pr.target)
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

// moveNode renames one record. It returns nil when there is nothing to do.
func (e *Engine) moveNode(fromAddr, toAddr string, target *config.Resource) (*Change, error) {
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
		}
	}
	e.txn.Record(fromAddr, "move-from", before, 0)
	e.txn.Record(toAddr, "move-to", 0, moved.Revision)
	e.Opts.Progress("%s: moved to %s", fromAddr, toAddr)
	e.count(func(st *Stats) { st.Moved++ })
	return &Change{Addr: toAddr, Mode: moved.Mode, Type: moved.Type, Name: moved.Name, Action: ActionMove, MovedFrom: fromAddr, ProviderKey: moved.ProviderKey}, nil
}

// importResource brings an existing object under management from an
// "import" declaration: the provider reads it by id and the result becomes
// the resource's record, which the plan then compares with the
// configuration.
func (e *Engine) importResource(ctx context.Context, prov plugin.Provider, r *config.Resource, s *schema.Schema, imp *config.Import, ch *Change) (tftypes.Value, []byte, error) {
	addr := r.Address()
	ps := e.Schemas[r.LocalProvider()]
	types := map[string]tftypes.Type{}
	for name, rs := range ps.AllResources() {
		types[name] = rs.Block.Type()
	}
	e.Opts.Progress("%s: Importing from id %q...", addr, imp.ID)
	imported, diags, err := prov.ImportResourceState(ctx, r.Type, imp.ID, types)
	if err != nil {
		return tftypes.Value{}, nil, fmt.Errorf("%s: importing %q: %w", addr, imp.ID, err)
	}
	if err := diags.Err(); err != nil {
		return tftypes.Value{}, nil, fmt.Errorf("%s: importing %q: %w", addr, imp.ID, err)
	}
	var found *plugin.ImportedResource
	for i := range imported {
		if imported[i].TypeName == r.Type {
			found = &imported[i]
			break
		}
	}
	if found == nil {
		return tftypes.Value{}, nil, fmt.Errorf("%s: the provider returned no %s for id %q", addr, r.Type, imp.ID)
	}
	st, private, diags, err := prov.ReadResource(ctx, r.Type, found.State, found.Private)
	if err != nil {
		return tftypes.Value{}, nil, fmt.Errorf("%s: reading imported object: %w", addr, err)
	}
	if err := diags.Err(); err != nil {
		return tftypes.Value{}, nil, fmt.Errorf("%s: reading imported object: %w", addr, err)
	}
	if st.IsNull() {
		return tftypes.Value{}, nil, fmt.Errorf("%s: no object with id %q exists", addr, imp.ID)
	}
	attrs, err := convert.ToJSON(st)
	if err != nil {
		return tftypes.Value{}, nil, fmt.Errorf("%s: imported state: %w", addr, err)
	}
	if ok, _ := e.State.TryLock(addr, e.Opts.Command+" (import)", e.txnID()); !ok {
		return tftypes.Value{}, nil, fmt.Errorf("%s: cannot import while another command holds its lock", addr)
	}
	defer e.State.Unlock(addr)
	if err := e.State.Verify(addr); err != nil {
		return tftypes.Value{}, nil, fmt.Errorf("%s: cannot import: %w", addr, err)
	}
	n := &state.Resource{
		Addr: addr, Mode: "managed", Type: r.Type, Name: r.Name, Provider: e.providerSource(r.ProviderKey), ProviderKey: r.ProviderKey,
		ProviderProtocol: e.providerProtocol(r.ProviderKey),
		ProviderVersion:  e.providerVersion(r.ProviderKey), SchemaVersion: s.Version, Attributes: attrs, Private: private,
		ProviderConfig: e.stamp(r.ProviderKey, ""), RefreshedAt: state.Now(),
	}
	if err := e.State.Set(n, e.txnID()); err != nil {
		return tftypes.Value{}, nil, err
	}
	e.txn.Record(addr, "import", 0, n.Revision)
	ch.Imported, ch.ImportID = true, imp.ID
	e.mu.Lock()
	if e.imported == nil {
		e.imported = map[string]string{}
	}
	e.imported[addr] = imp.ID
	e.mu.Unlock()
	e.count(func(st *Stats) { st.Imported++ })
	return st, private, nil
}

// relabelProviders carries out the provider $from declarations: a record
// written under an address the provider used to have is relabelled with its
// current address. A record under some other address than the provider's own
// is an error that says how to declare the change.
func (e *Engine) relabelProviders() error {
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
			return fmt.Errorf("%s was written by provider %s, but the provider is now %s; add $from: %q to the provider", n.Address(), n.Provider, req.Source.String(), n.Provider)
		}
		addr := n.Address()
		if err := e.State.Lock(addr, e.Opts.Command+" (provider moved)", e.txnID()); err != nil {
			return fmt.Errorf("provider $from %s: %w", n.Provider, err)
		}
		if err := e.State.Verify(addr); err != nil {
			_ = e.State.Unlock(addr)
			return fmt.Errorf("provider $from %s: %w", n.Provider, err)
		}
		moved := *n
		moved.Provider = req.Source.String()
		before := n.Revision
		if err := e.State.Set(&moved, e.txnID()); err != nil {
			return err
		}
		e.txn.Record(addr, "provider-move", before, moved.Revision)
		_ = e.State.Unlock(addr)
		e.Opts.Progress("%s: provider %s -> %s", addr, n.Provider, req.Source.String())
	}
	return nil
}

// importedID returns the id of the import that this plan made at addr.
func (e *Engine) importedID(addr string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	id, ok := e.imported[addr]
	return id, ok
}
