package engine

import (
	"context"
	"fmt"
	"sort"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/state"
	"github.com/pravo-systems/pravo/internal/telemetry"
)

// Discover runs one discovery: it asks the provider to list the objects of the
// discovery's type in its scope and returns what it listed. It stores nothing and
// touches no resource: the caller keeps the run with Store.PutDiscovery. A
// provider that fails, or a scope that cannot be listed with, is an error, and
// the caller keeps the run it had.
func (e *Engine) Discover(ctx context.Context, d *config.Discovery) (*state.DiscoveryRun, error) {
	e.runCtx = ctx
	defer e.State.SetContext(ctx)
	ctx, span := telemetry.Tracer().Start(ctx, "engine discover "+d.Name, trace.WithAttributes(append(e.runAttrs(),
		attribute.String("pravo.discovery.name", d.Name), attribute.String("pravo.discovery.type", d.Type), attribute.String("pravo.provider", providerLocal(d.ProviderKey)), attribute.String("pravo.provider.instance", d.ProviderKey))...))
	e.State.SetContext(ctx)
	run, err := e.discover(ctx, d)
	if run != nil {
		span.SetAttributes(attribute.Int("pravo.discovery.items", len(run.Items)), attribute.String("pravo.discovery.status", string(run.Status)), attribute.Int("pravo.warnings", len(run.Warnings)))
	}
	telemetry.End(span, err)
	return run, err
}

func (e *Engine) discover(ctx context.Context, d *config.Discovery) (*state.DiscoveryRun, error) {
	if d.ScopeErr != nil {
		return nil, fmt.Errorf("discovery %s: %w", d.Name, d.ScopeErr)
	}
	local := config.LocalName(d.ProviderKey)
	ps := e.Schemas[local]
	if ps == nil {
		return nil, fmt.Errorf("discovery %s: no schema for provider %s", d.Name, local)
	}
	ls := ps.ListResource(d.Type)
	if ls == nil || ls.Block == nil {
		return nil, fmt.Errorf("discovery %s: provider %s cannot list %q. It is a version that has no list resource of that name; the types it can list are: %v", d.Name, d.Source.Display(), d.Type, ps.SortedListNames())
	}
	conv := &convert.Converter{AllowEphemeral: true}
	cfg := conv.Block("discovery "+d.Name, ls.Block, d.Value())
	if err := conv.Err(); err != nil {
		return nil, fmt.Errorf("discovery %s: %w", d.Name, err)
	}
	if !cfg.IsFullyKnown() {
		return nil, fmt.Errorf("discovery %s: its scope has a value that is not known before a run", d.Name)
	}
	prov, err := e.Providers.Provider(ctx, d.ProviderKey)
	if err != nil {
		return nil, fmt.Errorf("discovery %s: %w", d.Name, err)
	}
	// One more than the bound is asked for, to tell a list that ended at the bound
	// from one that was cut off by it.
	bound := int64(state.MaxListed)
	if d.Limit > 0 && d.Limit < bound {
		bound = d.Limit
	}
	started := time.Now().UTC()
	e.Opts.Progress("%s: listing %s...", d.Name, d.Type)
	listed, diags, err := prov.ListResource(ctx, d.Type, cfg, bound+1)
	if err != nil {
		return nil, fmt.Errorf("discovery %s: listing %s: %w", d.Name, d.Type, err)
	}
	if err := diags.Err(); err != nil {
		return nil, fmt.Errorf("discovery %s: listing %s: %w", d.Name, d.Type, err)
	}
	run := &state.DiscoveryRun{
		Name: d.Name, Type: d.Type, Provider: d.Source.String(), ProviderKey: d.ProviderKey,
		Digest: d.Digest, Scope: d.Scope, Limit: d.Limit,
		Started: started, Finished: time.Now().UTC(), Actor: e.Opts.Actor, Status: state.RunComplete,
	}
	for _, w := range diags.Warnings() {
		run.Warnings = append(run.Warnings, warningText(w.Summary, w.Detail))
	}
	if int64(len(listed)) > bound {
		listed = listed[:bound]
		run.Status = state.RunTruncated
	}
	seen := map[string]bool{}
	for _, o := range listed {
		l := state.ListedOf(o.Identity, o.DisplayName)
		switch {
		case l.Key == "":
			run.Warnings = append(run.Warnings, "the provider listed an object with no identity, which is left out")
		case seen[l.Key]:
			run.Warnings = append(run.Warnings, fmt.Sprintf("the provider listed %s twice; it is kept once", l.Key))
		default:
			seen[l.Key] = true
			run.Items = append(run.Items, l)
		}
	}
	sort.SliceStable(run.Items, func(i, j int) bool { return run.Items[i].Key < run.Items[j].Key })
	return run, nil
}

func warningText(summary, detail string) string {
	if detail == "" {
		return summary
	}
	return summary + ": " + detail
}
