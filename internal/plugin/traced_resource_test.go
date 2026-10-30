package plugin

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/telemetry"
)

type readOnly struct{ Provider }

func (readOnly) ProtocolVersion() int { return 6 }
func (readOnly) ReadResource(context.Context, string, tftypes.Value, []byte) (tftypes.Value, []byte, Diagnostics, error) {
	return tftypes.Value{}, nil, nil, nil
}

// A provider call names the resource it serves, by its CUE path, and the provider block as the specification writes it.
func TestProviderSpanNamesTheResourceAndInstance(t *testing.T) {
	t.Setenv("PRAVO_TRACES", "on")
	t.Setenv("OTEL_SDK_DISABLED", "")
	telemetry.Configure(env.OS())
	t.Cleanup(func() { telemetry.Configure(env.Env{}) })
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	defer otel.SetTracerProvider(prev)

	p := NewTraced(readOnly{}, "aws")
	SetInstance(p, "$aws")
	if _, _, _, err := p.ReadResource(WithResource(context.Background(), "net.vpc"), "aws_vpc", tftypes.Value{}, nil); err != nil {
		t.Fatal(err)
	}
	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("%d spans, want 1", len(spans))
	}
	got := map[string]string{}
	for _, a := range spans[0].Attributes() {
		got[string(a.Key)] = a.Value.Emit()
	}
	if got["pravo.resource.address"] != "net.vpc" || got["pravo.provider.instance"] != "$aws" || got["pravo.provider"] != "aws" {
		t.Errorf("attributes = %v", got)
	}
}
