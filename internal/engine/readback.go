package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/state"
)

// readBackTimeout is the longest that one read after an apply may take.
const readBackTimeout = 30 * time.Second

// readBack reads back what an apply wrote, and records what the providers return.
// A resource is recorded as the provider answered when it was made; a resource made
// after it in the same run can change it (a policy attached to a role changes the
// role, a queue policy changes the queue), and the next read would find the record
// out of date and mark it as drifted. The read here brings the record up to date at
// once, without a mark: what differs a moment after the apply wrote the record is
// the effect of the run, and not a change that someone made outside it.
//
// The scope is the nodes the apply wrote and no other: a record that the apply only
// read may differ because someone changed the object, and is left for a read that
// marks it. That is one read for each node written, so an apply costs at most twice
// its own reads, and what it costs follows the change and not the size of the
// state. A read that fails or finds no object leaves the record as the apply wrote
// it: the apply has succeeded, and a later plan reads the node again.
func (e *Engine) readBack(ctx context.Context) {
	if e.Opts.NoReadBack {
		return
	}
	var targets []*state.Resource
	for _, addr := range e.completedAddrs() {
		if sr := e.State.Get(addr); sr != nil && sr.Mode == "managed" && !sr.Gone {
			targets = append(targets, sr)
		}
	}
	if len(targets) == 0 {
		return
	}
	// The type of a schema is worked out when it is first asked for: ask for it
	// before the reads run at the same time.
	schemas := make([]*schema.Schema, len(targets))
	for i, sr := range targets {
		s, err := e.recordSchema(sr)
		if err != nil {
			plog.Debug(ctx, "read back: "+err.Error())
			return
		}
		s.Block.Type()
		schemas[i] = s
	}
	var (
		wg      sync.WaitGroup
		sem     = make(chan struct{}, Parallelism)
		changed int
		mu      sync.Mutex
	)
	for i, sr := range targets {
		sem <- struct{}{}
		wg.Add(1)
		go func(sr *state.Resource, s *schema.Schema) {
			defer wg.Done()
			defer func() { <-sem }()
			if e.readBackOne(ctx, sr, s) {
				mu.Lock()
				changed++
				mu.Unlock()
			}
		}(sr, schemas[i])
	}
	wg.Wait()
	plog.Debug(ctx, fmt.Sprintf("read back %d records, %d changed", len(targets), changed))
}

// readBackOne reads one record and writes it when the read differs. It reports
// whether it wrote.
func (e *Engine) readBackOne(ctx context.Context, sr *state.Resource, s *schema.Schema) bool {
	// A read that does not return must not hold up an apply that has succeeded.
	ctx, cancel := context.WithTimeout(ctx, readBackTimeout)
	defer cancel()
	addr := sr.Address()
	prov, err := e.Providers.Provider(ctx, sr.ProviderKey)
	if err != nil {
		plog.Debug(ctx, "read back "+addr+": "+err.Error())
		return false
	}
	prior, err := e.upgrade(ctx, prov, sr, s, addr)
	if err != nil || prior.IsNull() {
		return false
	}
	newState, newPrivate, diags, err := prov.ReadResource(ctx, sr.Type, prior, sr.Private)
	if err != nil || diags.Err() != nil || newState.IsNull() {
		return false
	}
	attrs, err := convert.ToJSON(newState)
	if err != nil || jsonEqual(attrs, sr.Attributes) {
		return false
	}
	e.persistRead(addr, sr, attrs, newPrivate, false)
	return true
}
