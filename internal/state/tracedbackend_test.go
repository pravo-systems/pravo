package state

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// The requests a store sends to its backend are spans under the context the store
// was given, with the key and the kind of store, and a missing object is not a failure.
func TestStoreRequestsAreSpans(t *testing.T) {
	t.Setenv("PRAVO_TRACES", "")
	t.Setenv("OTEL_SDK_DISABLED", "")
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	defer otel.SetTracerProvider(prev)

	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, span := otel.Tracer("test").Start(context.Background(), "phase test")
	st.SetContext(ctx)
	if _, err := st.Backend().Get(st.cx(), "missing.json"); err != ErrNotFound {
		t.Fatalf("get: %v", err)
	}
	span.End()

	var got bool
	for _, s := range rec.Ended() {
		if s.Name() != "state get missing.json" {
			continue
		}
		got = true
		if s.Parent().SpanID() != span.SpanContext().SpanID() {
			t.Errorf("the request is not under the phase")
		}
		if s.Status().Code != 0 {
			t.Errorf("a missing object is a failure: %v", s.Status())
		}
		attrs := map[string]string{}
		for _, kv := range s.Attributes() {
			attrs[string(kv.Key)] = kv.Value.Emit()
		}
		if attrs["pravo.state.key"] != "missing.json" || attrs["pravo.state.backend"] != "local" || attrs["pravo.state.result"] == "" {
			t.Errorf("attributes %v", attrs)
		}
	}
	if !got {
		names := []string{}
		for _, s := range rec.Ended() {
			names = append(names, s.Name())
		}
		t.Fatalf("no span for the request among %s", strings.Join(names, ", "))
	}
}
