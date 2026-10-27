package state

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/pravo-systems/pravo/internal/telemetry"
)

// tracedBackend makes every request to the store a span, so that a run shows
// the time it spent reading and writing records and taking locks. The spans
// carry the key (an address or the name of an object), the size of what moved
// and the kind of store, never the contents.
type tracedBackend struct{ Backend }

// traced wraps b when spans are being kept.
func traced(b Backend) Backend {
	if _, ok := b.(*tracedBackend); ok || !telemetry.Active() {
		return b
	}
	return &tracedBackend{b}
}

func (t *tracedBackend) kind() string {
	switch t.Backend.(type) {
	case *LocalBackend:
		return "local"
	case *S3Backend:
		return "s3"
	case *HTTPBackend:
		return "http"
	}
	if t.Local() {
		return "local"
	}
	return "remote"
}

// begin starts the span of one request; the function it returns ends it with
// the error of the request and the attributes the answer adds.
func (t *tracedBackend) begin(ctx context.Context, op, key string) (context.Context, func(err error, attrs ...attribute.KeyValue)) {
	ctx, span := telemetry.Tracer().Start(ctx, "state "+op+" "+key, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.String("pravo.state.op", op),
		attribute.String("pravo.state.key", key),
		attribute.String("pravo.state.backend", t.kind()),
	))
	return ctx, func(err error, attrs ...attribute.KeyValue) {
		span.SetAttributes(attrs...)
		// A missing object and a refused conditional write are answers, not failures.
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrChanged) {
			span.SetAttributes(attribute.String("pravo.state.result", err.Error()))
			err = nil
		}
		telemetry.End(span, err)
	}
}

func (t *tracedBackend) Get(ctx context.Context, key string) ([]byte, error) {
	ctx, end := t.begin(ctx, "get", key)
	data, err := t.Backend.Get(ctx, key)
	end(err, attribute.Int("pravo.state.bytes", len(data)))
	return data, err
}

func (t *tracedBackend) Put(ctx context.Context, key string, data []byte) error {
	ctx, end := t.begin(ctx, "put", key)
	err := t.Backend.Put(ctx, key, data)
	end(err, attribute.Int("pravo.state.bytes", len(data)))
	return err
}

func (t *tracedBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	ctx, end := t.begin(ctx, "create", key)
	ok, err := t.Backend.PutIfAbsent(ctx, key, data)
	end(err, attribute.Int("pravo.state.bytes", len(data)), attribute.Bool("pravo.state.applied", ok))
	return ok, err
}

func (t *tracedBackend) DeleteIf(ctx context.Context, key string, match func(current []byte) bool) (bool, error) {
	ctx, end := t.begin(ctx, "delete-if", key)
	ok, err := t.Backend.DeleteIf(ctx, key, match)
	end(err, attribute.Bool("pravo.state.applied", ok))
	return ok, err
}

func (t *tracedBackend) ReplaceIf(ctx context.Context, key string, update func(current []byte) ([]byte, bool)) (bool, error) {
	ctx, end := t.begin(ctx, "replace-if", key)
	n := 0
	ok, err := t.Backend.ReplaceIf(ctx, key, func(cur []byte) ([]byte, bool) {
		data, ok := update(cur)
		n = len(data)
		return data, ok
	})
	end(err, attribute.Int("pravo.state.bytes", n), attribute.Bool("pravo.state.applied", ok))
	return ok, err
}

func (t *tracedBackend) Delete(ctx context.Context, key string) error {
	ctx, end := t.begin(ctx, "delete", key)
	err := t.Backend.Delete(ctx, key)
	end(err)
	return err
}

func (t *tracedBackend) List(ctx context.Context, prefix string) ([]string, error) {
	ctx, end := t.begin(ctx, "list", prefix)
	keys, err := t.Backend.List(ctx, prefix)
	end(err, attribute.Int("pravo.state.keys", len(keys)))
	return keys, err
}
