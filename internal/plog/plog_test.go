package plog

import (
	"bytes"
	"context"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestOffByDefaultAndOnWithVerbose(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	Configure(0, "")
	Debug(context.Background(), "hidden", "k", "v")
	if buf.Len() != 0 {
		t.Fatalf("nothing should print by default, got %q", buf.String())
	}
	Configure(1, "")
	Debug(context.Background(), "shown", "addr", "a b")
	Trace(context.Background(), "too fine")
	got := buf.String()
	if !strings.Contains(got, "debug shown addr=\"a b\"") || strings.Contains(got, "too fine") {
		t.Errorf("-v output = %q", got)
	}
	Configure(2, "")
	Trace(context.Background(), "fine")
	if !strings.Contains(buf.String(), "trace fine") {
		t.Errorf("-vv should print trace events: %q", buf.String())
	}
	Configure(0, "debug")
	buf.Reset()
	Debug(context.Background(), "env")
	if !strings.Contains(buf.String(), "env") {
		t.Error("PRAVO_LOG=debug should print")
	}
}

func TestEventsReachTheSpanWithoutVerbose(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	Configure(0, "")
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	ctx, span := tp.Tracer("t").Start(context.Background(), "s")
	Debug(ctx, "decided", "addr", "pet")
	span.End()
	if buf.Len() != 0 {
		t.Errorf("nothing should print, got %q", buf.String())
	}
	spans := rec.Ended()
	if len(spans) != 1 || len(spans[0].Events()) != 1 || spans[0].Events()[0].Name != "decided" {
		t.Fatalf("span events = %+v", spans)
	}
}
