package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

// DueReport is what DriftDue found.
type DueReport struct {
	// Records is the number of managed records visited.
	Records int `json:"records"`
	// Due is the number of them whose record is older than its refresh age.
	Due int `json:"due"`
	// Read lists the addresses that were read, oldest record first.
	Read []string `json:"read"`
	// Drifted lists the records whose object differs from the state or is gone,
	// sorted by address. Each carries its mark.
	Drifted []DueDrift `json:"drifted"`
}

// DueDrift is a record that a read of DriftDue found changed.
type DueDrift struct {
	Addr string `json:"addr"`
	// Gone reports that the object does not exist.
	Gone bool `json:"gone,omitempty"`
}

// DriftDue visits every managed record and reads the ones that are older than
// their refresh age, the oldest first and at most limit of them when limit is
// above zero. A record that was never refreshed comes before any other, and
// records refreshed at the same time come in the order of their addresses. It
// writes the records it read and the marks it found, as a plan does, and plans
// nothing.
func (e *Engine) DriftDue(ctx context.Context, limit int) (*DueReport, error) {
	e.txn = e.State.Begin("plan", e.Opts.Command)
	e.resetRun(false)
	e.forgetReads()
	rep := &DueReport{Read: []string{}, Drifted: []DueDrift{}}
	var due []*state.Resource
	for _, sr := range e.State.List() {
		if sr.Mode != "managed" {
			continue
		}
		rep.Records++
		past, err := e.pastAge(sr, e.ageOf(sr))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", sr.Address(), err)
		}
		if past {
			due = append(due, sr)
		}
	}
	rep.Due = len(due)
	sort.SliceStable(due, func(i, j int) bool {
		a, b := due[i].RefreshedAt, due[j].RefreshedAt
		switch {
		case a == nil && b == nil:
			return due[i].Address() < due[j].Address()
		case a == nil:
			return true
		case b == nil:
			return false
		case !a.Equal(*b):
			return a.Before(*b)
		}
		return due[i].Address() < due[j].Address()
	})
	if limit > 0 && len(due) > limit {
		due = due[:limit]
	}
	for _, sr := range due {
		rep.Read = append(rep.Read, sr.Address())
	}
	err := e.readDue(ctx, due, rep)
	e.keepOpen = false
	e.closeEphemerals(context.WithoutCancel(ctx))
	if err != nil {
		_ = e.txn.Finish("failed", err.Error(), false)
		return nil, err
	}
	sort.Slice(rep.Drifted, func(i, j int) bool { return rep.Drifted[i].Addr < rep.Drifted[j].Addr })
	summary := fmt.Sprintf("%d records, %d past their refresh age, %d read, %d drifted", rep.Records, rep.Due, len(rep.Read), len(rep.Drifted))
	return rep, e.txn.Finish("planned", summary, false)
}

// ageOf returns the refresh age that the declaration of a record sets for
// itself, or "" for the default.
func (e *Engine) ageOf(sr *state.Resource) string {
	r, ok := e.byPath[sr.Address()]
	if !ok || !r.RefreshPinned {
		return ""
	}
	_, _, _, age := lifecycle(e.Value.LookupPath(r.Path))
	return age
}

// readDue reads the records, ten at a time.
func (e *Engine) readDue(ctx context.Context, due []*state.Resource, rep *DueReport) error {
	if err := e.fillProviderInputs(ctx, due); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		sem      = make(chan struct{}, Parallelism)
	)
	// The type of a schema is worked out when it is first asked for. Ask for it
	// here, before the reads run at the same time.
	schemas := make([]*schema.Schema, len(due))
	for i, sr := range due {
		s, err := e.recordSchema(sr)
		if err != nil {
			return err
		}
		s.Block.Type()
		schemas[i] = s
	}
	for i, sr := range due {
		sem <- struct{}{}
		wg.Add(1)
		go func(sr *state.Resource, s *schema.Schema) {
			defer wg.Done()
			defer func() { <-sem }()
			gone, drifted, err := e.readOne(ctx, sr, s)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				return
			}
			if drifted {
				rep.Drifted = append(rep.Drifted, DueDrift{Addr: sr.Address(), Gone: gone})
			}
		}(sr, schemas[i])
	}
	wg.Wait()
	return firstErr
}

// recordSchema returns the schema of the resource type that a record holds.
func (e *Engine) recordSchema(sr *state.Resource) (*schema.Schema, error) {
	local := config.LocalName(sr.ProviderKey)
	ps, ok := e.Schemas[local]
	if !ok {
		return nil, fmt.Errorf("%s: provider %s is needed to read it but is not available", sr.Address(), local)
	}
	s := ps.Resources[sr.Type]
	if s == nil {
		return nil, fmt.Errorf("%s: provider %s no longer has resource type %q", sr.Address(), local, sr.Type)
	}
	return s, nil
}

// readOne reads one record. It reports whether the object is gone and whether
// it differs from the record.
func (e *Engine) readOne(ctx context.Context, sr *state.Resource, s *schema.Schema) (gone, drifted bool, err error) {
	addr := sr.Address()
	prov, err := e.Providers.Provider(ctx, sr.ProviderKey)
	if err != nil {
		return false, false, err
	}
	prior, err := e.upgrade(ctx, prov, sr, s, addr)
	if err != nil || prior.IsNull() {
		return false, false, err
	}
	ch := &Change{Addr: addr}
	st, _, err := e.readRecord(ctx, prov, sr, ch, prior, sr.Private)
	if err != nil {
		return false, false, err
	}
	return st.IsNull(), ch.Drifted, nil
}

// fillProviderInputs gives the configuration the values that the providers of
// the records read their own configuration from. A managed resource or data
// source that a provider is set up from is read from the state, as the apply of a
// plan reads a record it does not change. A built-in value, an ephemeral resource
// and a function call run as they do in a plan, together with whatever they read:
// the environment, the outputs of another root, a provider that opens the
// resource. The ephemeral resources stay open until DriftDue has read the
// records, since a provider may be configured from what they returned. A provider
// that is set up from a managed resource or data source that has no record is an
// error: there is nothing to read its configuration from.
func (e *Engine) fillProviderInputs(ctx context.Context, due []*state.Resource) error {
	keys := map[string]bool{}
	for _, sr := range due {
		keys[sr.ProviderKey] = true
	}
	e.cueMu.Lock()
	e.current, e.seeded = e.Value, false
	e.providerDeps = e.providerDepsOf()
	e.cueMu.Unlock()
	reads := false
	for key := range keys {
		reads = reads || len(e.providerDeps[key]) > 0
	}
	if !reads {
		return nil // no provider of a due record is set up from anything
	}
	if _, err := e.buildGraph(); err != nil {
		return err
	}
	roles := map[string]role{}
	var need func(addr, provider string) error
	need = func(addr, provider string) error {
		if _, ok := roles[addr]; ok {
			return nil
		}
		r := e.byPath[addr]
		if r == nil {
			return nil
		}
		if r.Mode == "managed" || r.Mode == "data" {
			if e.State.Get(addr) == nil {
				return fmt.Errorf("provider %s is set up from %s, which the state does not hold; read the records of that provider with \"pravo drift\"", provider, addr)
			}
			roles[addr] = roleInput
			// Only what the configuration of the record reads is needed. The record is
			// not sent to its provider, so the tasks its provider is set up from are not.
			e.cueMu.Lock()
			deps, _ := convert.Dependencies(e.Value.LookupPath(r.Path), r.Path, e.resolver())
			e.cueMu.Unlock()
			for _, d := range deps {
				if dr := e.byPath[d.String()]; dr != nil && dr != r {
					if err := need(dr.Address(), provider); err != nil {
						return err
					}
				}
			}
			return nil
		}
		roles[addr] = roleSupport
		for _, d := range e.deps[addr] {
			if err := need(d, provider); err != nil {
				return err
			}
		}
		return nil
	}
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		for _, d := range e.providerDeps[key] {
			if err := need(d, key); err != nil {
				return err
			}
		}
	}
	if len(roles) == 0 {
		return nil
	}
	// The walk of an apply that writes nothing: the nodes of the scope run, a
	// record is its own value, and the ephemeral resources stay open.
	e.scope, e.only, e.keepOpen = &applyScope{roles: roles}, map[string]bool{}, true
	defer func() { e.scope, e.only = nil, nil }()
	_, err := e.run(ctx, true)
	return err
}
