package engine

import (
	"context"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/plog"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/telemetry"
)

// The spans below carry addresses, types, actions and counts, never
// attribute values: a resource's values may be secrets.

func (e *Engine) runAttrs() []attribute.KeyValue {
	attrs := []attribute.KeyValue{attribute.Bool("pravo.destroy", e.Opts.Destroy)}
	if e.Opts.Actor != "" {
		attrs = append(attrs, attribute.String("pravo.actor", e.Opts.Actor))
	}
	if e.Opts.Reason != "" {
		attrs = append(attrs, attribute.String("pravo.reason", e.Opts.Reason))
	}
	if e.Opts.GitRevision != "" {
		attrs = append(attrs, attribute.String("vcs.revision", e.Opts.GitRevision))
	}
	return attrs
}

// phase starts a span for one stage of a plan or an apply and returns the
// context the stage runs under and the function that ends it. The requests the
// stage makes to the state are part of the span until it ends.
func (e *Engine) phase(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, func(error)) {
	parent := ctx
	ctx, span := telemetry.Tracer().Start(ctx, "phase "+name, trace.WithAttributes(attrs...))
	e.State.SetContext(ctx)
	return ctx, func(err error) {
		telemetry.End(span, err)
		e.State.SetContext(parent)
	}
}

// traceCtx is the context for a span of work that has no context of its own:
// the one the run was started with, or none.
func (e *Engine) traceCtx() context.Context {
	if e.runCtx != nil {
		return e.runCtx
	}
	return context.Background()
}

// setup starts a span for work that readies the run: resolving and installing
// providers, starting them, reading their schemas and configuring them.
func setup(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, func(error)) {
	return telemetry.Span(ctx, "setup "+name, attrs...)
}

// Plan computes the changes needed to reach the configuration.
func (e *Engine) Plan(ctx context.Context) (*Plan, error) {
	e.runCtx = ctx
	defer e.State.SetContext(ctx)
	ctx, span := telemetry.Tracer().Start(ctx, "engine plan", trace.WithAttributes(e.runAttrs()...))
	e.traceID = telemetry.TraceID(ctx)
	e.State.SetTrace(e.traceID)
	e.State.SetContext(ctx)
	plog.Debug(ctx, "plan", "refresh", [...]string{"age", "none", "all", "expired"}[e.Opts.Refresh], "full", e.Opts.Full, "destroy", e.Opts.Destroy, "tasks", len(e.Root.Resources))
	plan, err := e.planRun(ctx)
	if plan != nil {
		add, change, destroy, _ := plan.Counts()
		span.SetAttributes(
			attribute.Int("pravo.plan.add", add), attribute.Int("pravo.plan.change", change), attribute.Int("pravo.plan.destroy", destroy),
			attribute.Int("pravo.nodes.total", plan.Stats.Total), attribute.Int("pravo.nodes.planned", plan.Stats.Reached),
			attribute.Int("pravo.nodes.skipped", plan.Stats.Skipped), attribute.Int("pravo.nodes.refreshed", plan.Stats.Refreshed),
			attribute.Int("pravo.nodes.fresh", plan.Stats.Fresh), attribute.Int("pravo.nodes.not_read", plan.Stats.NotRead), attribute.Int("pravo.nodes.drifted", plan.Stats.Drifted),
			attribute.Int("pravo.ephemeral.opened", plan.Stats.Opened), attribute.Int("pravo.function.calls", plan.Stats.Called),
		)
		telemetry.Nodes(ctx, "planned", plan.Stats.Reached)
		telemetry.Nodes(ctx, "skipped", plan.Stats.Skipped)
		telemetry.Nodes(ctx, "refreshed", plan.Stats.Refreshed)
		telemetry.Nodes(ctx, "fresh", plan.Stats.Fresh)
		telemetry.Nodes(ctx, "not_read", plan.Stats.NotRead)
		telemetry.Nodes(ctx, "drifted", plan.Stats.Drifted)
	}
	span.SetAttributes(attribute.String("pravo.txn", e.txnID()))
	telemetry.End(span, err)
	return plan, err
}

// Apply executes the plan: locks the nodes it touches, walks the nodes it
// changes and the nodes they read, destroys orphans and records outputs, as one
// transaction.
func (e *Engine) Apply(ctx context.Context, plan *Plan) (*ApplyResult, error) {
	e.runCtx = ctx
	defer e.State.SetContext(ctx)
	ctx, span := telemetry.Tracer().Start(ctx, "engine apply", trace.WithAttributes(e.runAttrs()...))
	e.traceID = telemetry.TraceID(ctx)
	e.State.SetTrace(e.traceID)
	e.State.SetContext(ctx)
	plog.Debug(ctx, "apply", "changes", len(plan.Changes), "destroy", e.Opts.Destroy)
	res, err := e.applyRun(ctx, plan)
	if res != nil {
		span.SetAttributes(attribute.Int("pravo.apply.added", res.Added), attribute.Int("pravo.apply.changed", res.Changed), attribute.Int("pravo.apply.destroyed", res.Destroyed))
	}
	span.SetAttributes(attribute.String("pravo.txn", e.txnID()))
	telemetry.End(span, err)
	return res, err
}

// runTask plans or applies one task inside a span of its own, so provider
// RPCs nest under the resource they serve.
func (e *Engine) runTask(ctx context.Context, r *config.Resource, apply bool) error {
	phase := "plan"
	if apply {
		phase = "apply"
	}
	ctx = plugin.WithResource(ctx, r.Address())
	ctx, span := telemetry.Tracer().Start(ctx, "resource "+r.Address(), trace.WithAttributes(
		attribute.String("pravo.resource.address", r.Address()),
		attribute.String("pravo.resource.type", r.Type),
		attribute.String("pravo.mode", r.Mode),
		attribute.String("pravo.provider", providerLocal(r.ProviderKey)),
		attribute.String("pravo.provider.instance", e.Providers.instanceName(r.ProviderKey)),
		attribute.String("pravo.phase", phase),
	))
	began := time.Now()
	plog.Trace(ctx, "task start", "addr", r.Address(), "phase", phase, "deps", strings.Join(e.deps[r.Address()], ","))
	err := e.runTaskInner(ctx, r, apply)
	action := ""
	ch := e.changeFor(r.Address())
	if ch != nil {
		action = string(ch.Action)
		span.SetAttributes(attribute.String("pravo.action", action), attribute.Bool("pravo.skipped", ch.Skipped),
			attribute.Bool("pravo.refreshed", ch.Refreshed), attribute.Bool("pravo.drifted", ch.Drifted), attribute.Bool("pravo.deferred", ch.Deferred))
	}
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	e.logTask(ctx, r, phase, ch, outcome, time.Since(began), err)
	if phase == "apply" || (ch != nil && ch.Action != ActionNoop) {
		telemetry.Resource(ctx, r.Mode, r.Type, action, outcome)
	}
	telemetry.End(span, err)
	return err
}

// logTask reports what became of one task: the action chosen and the reasons
// the plan gave for it.
func (e *Engine) logTask(ctx context.Context, r *config.Resource, phase string, ch *Change, outcome string, took time.Duration, err error) {
	args := []any{"addr", r.Address(), "phase", phase, "outcome", outcome, "took", took.Round(time.Millisecond).String()}
	if ch != nil {
		args = append(args, "action", string(ch.Action))
		for _, f := range []struct {
			name string
			on   bool
		}{{"skipped", ch.Skipped}, {"fresh", ch.Fresh}, {"unread", ch.Unread}, {"refreshed", ch.Refreshed}, {"drifted", ch.Drifted}, {"deferred", ch.Deferred}} {
			if f.on {
				args = append(args, f.name, true)
			}
		}
		if len(ch.Reasons) > 0 {
			args = append(args, "why", strings.Join(ch.Reasons, "; "))
		}
	}
	if err != nil {
		args = append(args, "error", err.Error())
	}
	plog.Debug(ctx, "task", args...)
}

// lockCUE takes the lock of the CUE evaluation, which one task at a time holds. A wait
// for it is a span of the context, so that a task that stands still shows why.
func (e *Engine) lockCUE(ctx context.Context) {
	waiting := time.Now()
	e.cueMu.Lock()
	telemetry.Waited(ctx, "wait for the CUE evaluation", waiting)
}
