package pravo

import (
	"context"
	"errors"
	"time"

	"github.com/pravo-systems/pravo/internal/engine"
)

// RefreshMode says which records a plan refreshes with their providers.
type RefreshMode = engine.RefreshMode

// The refresh modes, as "-refresh" names them.
const (
	RefreshAge     = engine.RefreshAge
	RefreshNone    = engine.RefreshNone
	RefreshAll     = engine.RefreshAll
	RefreshExpired = engine.RefreshExpired
)

// PlanOptions say what a plan computes.
type PlanOptions struct {
	// Refresh is which records are refreshed first. The default is RefreshAge.
	Refresh RefreshMode
	// Destroy plans the removal of everything.
	Destroy bool
	// Full plans every resource with the provider instead of skipping unchanged nodes.
	Full bool
	// Replace lists addresses to destroy and create again though they did not change.
	Replace []string
	// Reserve, when positive, reserves the nodes with a pending change for that long.
	Reserve time.Duration
	// Skipped makes the view of the plan list the nodes it left alone as well.
	Skipped bool
}

// Planned is a plan with the engine that computed it, which is the engine that
// applies it.
type Planned struct {
	eng  *Engine
	plan *Plan
	opts PlanOptions
}

// Plan computes the changes the specification needs. It reads the state and, per
// the refresh mode, the resources; it changes neither (except for the
// reservations Reserve asks for).
func (w *Workspace) Plan(ctx context.Context, o PlanOptions) (*Planned, error) {
	if w.state == nil {
		return nil, errors.New("pravo: a plan needs the state: open the workspace with Config.State set")
	}
	if w.schemas == nil {
		return nil, errors.New("pravo: a plan needs the schemas of the providers: open the workspace with Config.Schemas set")
	}
	eng := w.Engine(EngineOptions{Refresh: o.Refresh, Destroy: o.Destroy, Full: o.Full, Reserve: o.Reserve, Replace: o.Replace})
	plan, err := eng.Plan(ctx)
	if err != nil {
		return nil, err
	}
	return &Planned{eng: eng, plan: plan, opts: o}, nil
}

// Plan is the plan itself.
func (p *Planned) Plan() *Plan { return p.plan }

// Engine is the engine that computed the plan.
func (p *Planned) Engine() *Engine { return p.eng }

// HasChanges reports whether applying the plan would change anything.
func (p *Planned) HasChanges() bool { return p.plan.HasWrites() }

// View is the document of the plan, the one "pravo plan --json" prints.
func (p *Planned) View() *View {
	return p.eng.NewView(p.plan, engine.ViewOptions{Skipped: p.opts.Skipped})
}

// CheckPolicy judges the plan with the policies the specification declares. The
// error says which rule refused it; it carries the exit code the command line
// would use.
func (p *Planned) CheckPolicy(ctx context.Context) error { return p.eng.JudgePlan(ctx, p.plan) }

// Apply makes the changes of the plan.
func (p *Planned) Apply(ctx context.Context) (*ApplyResult, error) {
	return p.eng.Apply(ctx, p.plan)
}
