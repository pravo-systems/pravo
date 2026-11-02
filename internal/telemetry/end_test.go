package telemetry

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// A span whose work was canceled did not fail: it carries pravo.canceled, and no status of
// an error, so that the calls a provider stops waiting for do not mark a run as failed.
func TestEndTellsACancelFromAFailure(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	tr := tp.Tracer("t")
	_, a := tr.Start(context.Background(), "canceled")
	End(a, fmt.Errorf("Post \"https://iam.amazonaws.com/\": %w", context.Canceled))
	_, b := tr.Start(context.Background(), "failed")
	End(b, errors.New("AccessDenied"))
	spans := rec.Ended()
	if spans[0].Status().Code == codes.Error || len(spans[0].Attributes()) != 1 || spans[0].Attributes()[0].Key != "pravo.canceled" {
		t.Errorf("canceled: status %v, attributes %v", spans[0].Status(), spans[0].Attributes())
	}
	if spans[1].Status().Code != codes.Error {
		t.Errorf("failed: status %v", spans[1].Status())
	}
}
