package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"cuelang.org/go/cue"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/plugin"
)

// ActionOpen marks an ephemeral resource opened for the run.
const ActionOpen Action = "open"

// openEphemeral is a provider object that lives for the duration of a run.
type openEphemeral struct {
	addr    string
	typ     string
	key     string
	private []byte
	renewAt *time.Time
	stop    chan struct{}
	mu      sync.Mutex
}

// ephemeralTask opens an ephemeral resource with the current configuration
// (or defers it while the configuration is unknown) and returns its result
// for filling into the configuration.
func (e *Engine) ephemeralTask(ctx context.Context, r *config.Resource, v cue.Value, strict bool) (tftypes.Value, error) {
	s, err := e.resourceSchema(r)
	if err != nil {
		return tftypes.Value{}, err
	}
	addr := r.Address()
	e.cueMu.Lock()
	conv := &convert.Converter{Strict: strict, Self: r.Path, Resolve: e.resolver(), IsEphemeral: e.isEphemeral, AllowEphemeral: true}
	cfg := conv.Block(addr, s.Block, v)
	e.cueMu.Unlock()
	if err := conv.Err(); err != nil {
		return tftypes.Value{}, err
	}
	ch := &Change{Addr: addr, Mode: r.Mode, Type: r.Type, Name: r.Name, ProviderKey: r.ProviderKey, Schema: s, Config: cfg, Action: ActionOpen, Deps: e.deps[addr]}
	prov, err := e.Providers.Provider(ctx, r.ProviderKey)
	if err != nil {
		return tftypes.Value{}, err
	}
	diags, err := prov.ValidateEphemeralConfig(ctx, r.Type, cfg)
	if err != nil {
		return tftypes.Value{}, fmt.Errorf("%s: validating: %w", addr, err)
	}
	if err := diags.Err(); err != nil {
		return tftypes.Value{}, fmt.Errorf("%s: %w", addr, err)
	}
	if conv.Unknown {
		ch.Deferred = true
		e.record(ch)
		return tftypes.Value{}, nil
	}
	e.Opts.Progress("%s: Opening...", addr)
	res, diags, err := prov.OpenEphemeral(ctx, r.Type, cfg)
	if err != nil {
		return tftypes.Value{}, fmt.Errorf("%s: opening: %w", addr, err)
	}
	if err := diags.Err(); err != nil {
		return tftypes.Value{}, fmt.Errorf("%s: %w", addr, err)
	}
	ch.Warnings = diags.Warnings()
	if res.Deferred {
		ch.Deferred = true
		e.record(ch)
		e.Opts.Progress("%s: deferred by the provider", addr)
		return tftypes.Value{}, nil
	}
	h := &openEphemeral{addr: addr, typ: r.Type, key: r.ProviderKey, private: res.Private, renewAt: res.RenewAt, stop: make(chan struct{})}
	e.mu.Lock()
	e.open = append(e.open, h)
	e.stats.Opened++
	e.mu.Unlock()
	if res.RenewAt != nil {
		go e.renewLoop(ctx, prov, h)
	}
	ch.Planned = res.Result
	e.record(ch)
	return res.Result, nil
}

// renewLoop keeps an ephemeral resource alive until the run closes it.
func (e *Engine) renewLoop(ctx context.Context, prov plugin.Provider, h *openEphemeral) {
	for {
		h.mu.Lock()
		at := h.renewAt
		h.mu.Unlock()
		if at == nil {
			return
		}
		wait := time.Until(*at) * 9 / 10
		if wait < time.Second {
			wait = time.Second
		}
		select {
		case <-h.stop:
			return
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		h.mu.Lock()
		private := h.private
		h.mu.Unlock()
		res, diags, err := prov.RenewEphemeral(ctx, h.typ, private)
		if err != nil || diags.HasErrors() {
			e.Opts.Progress("%s: renewing failed: %v %v", h.addr, err, diags.Err())
			return
		}
		h.mu.Lock()
		h.private = res.Private
		h.renewAt = res.RenewAt
		h.mu.Unlock()
	}
}

// closeEphemerals closes every ephemeral resource opened during the run.
func (e *Engine) closeEphemerals(ctx context.Context) {
	e.mu.Lock()
	open := e.open
	e.open = nil
	e.mu.Unlock()
	for i := len(open) - 1; i >= 0; i-- {
		h := open[i]
		close(h.stop)
		prov, err := e.Providers.Provider(ctx, h.key)
		if err != nil {
			continue
		}
		h.mu.Lock()
		private := h.private
		h.mu.Unlock()
		if diags, err := prov.CloseEphemeral(ctx, h.typ, private); err != nil || diags.HasErrors() {
			e.Opts.Progress("%s: closing failed: %v %v", h.addr, err, diags.Err())
		}
	}
}

// checkOutputs refuses outputs that would record ephemeral values in state.
func (e *Engine) checkOutputs() error {
	e.cueMu.Lock()
	defer e.cueMu.Unlock()
	reads := e.outputReadsLocked()
	for _, o := range e.Root.Outputs {
		for _, d := range reads[o.Name] {
			if r := e.byPath[d]; r != nil && r.Mode == "ephemeral" {
				return fmt.Errorf("output %s references %s; outputs are recorded in state, so ephemeral values cannot be used in them", o.Name, d)
			}
		}
	}
	return nil
}

// providerDepsOf returns, per provider key, the tasks its configuration
// references, so that resources using the provider run after them.
func (e *Engine) providerDepsOf() map[string][]string {
	out := map[string][]string{}
	for _, key := range e.Root.ProviderKeys {
		p, ok := e.Root.ProviderPath(key)
		if !ok {
			continue
		}
		v := e.current.LookupPath(p)
		if !v.Exists() {
			continue
		}
		deps, _ := convert.Dependencies(v, p, e.resolver())
		for _, d := range deps {
			if dr, ok := e.byPath[d.String()]; ok {
				out[key] = append(out[key], dr.Address())
			}
		}
	}
	return out
}
