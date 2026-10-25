package engine

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// A span is inside the time of its parent, and a run is one tree: a request to the
// state that is made after a plan has ended is not a part of the plan.
func TestSpansNestInTime(t *testing.T) {
	t.Setenv("PRAVO_TRACES", "on")
	t.Setenv("OTEL_SDK_DISABLED", "")
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	defer otel.SetTracerProvider(prev)

	h := newHarness(t)
	s := spec{cue: "a: " + thing("a", "") + "\nb: " + thing("b", "") + "\n", resources: []string{"a", "b"}}
	ctx, root := otel.Tracer("test").Start(context.Background(), "pravo apply")
	within(t, func() {
		e := h.engine(s, Options{})
		plan, err := e.Plan(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		// what a command does after the plan: lists the state, reads a record
		e.State.List()
		if _, err := e.State.Backend().List(e.State.Context(), "plans"); err != nil {
			t.Error(err)
		}
		if _, err := e.Apply(ctx, plan); err != nil {
			t.Error(err)
		}
		if _, err := e.State.Backend().List(e.State.Context(), "plans"); err != nil {
			t.Error(err)
		}
	})
	root.End()

	spans := rec.Ended()
	by := map[string]sdktrace.ReadOnlySpan{}
	for _, sp := range spans {
		by[sp.SpanContext().SpanID().String()] = sp
	}
	kinds := map[string]bool{}
	for _, sp := range spans {
		kinds[sp.Name()[:min(len(sp.Name()), 5)]] = true
		if !sp.Parent().IsValid() {
			continue
		}
		p, ok := by[sp.Parent().SpanID().String()]
		if !ok {
			t.Errorf("%q has a parent that was not recorded", sp.Name())
			continue
		}
		if sp.StartTime().Before(p.StartTime()) || sp.EndTime().After(p.EndTime()) {
			t.Errorf("%q [%v, %v] is not inside %q [%v, %v]", sp.Name(), sp.StartTime().Format("05.000000"), sp.EndTime().Format("05.000000"), p.Name(), p.StartTime().Format("05.000000"), p.EndTime().Format("05.000000"))
		}
	}
	for _, want := range []string{"state", "phase", "engin", "resou"} {
		if !kinds[want] {
			t.Errorf("no %s spans among %d", want, len(spans))
		}
	}
}

// The provider of a span is its name, and the instance is the key of the block: aws and aws.$west, random and random.$r1.
func TestProviderLocalNameOfAnInstance(t *testing.T) {
	for key, want := range map[string]string{"aws": "aws", "aws.$west": "aws", "random.$r1": "random"} {
		if got := providerLocal(key); got != want {
			t.Errorf("providerLocal(%q) = %q, want %q", key, got, want)
		}
	}
}
