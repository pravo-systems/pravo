package plugin

import (
	"context"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/pravo-systems/pravo/internal/plugin/netcap"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/telemetry"
)

type resourceKey struct{}

// WithResource says that the provider calls made under ctx serve the resource at addr, its CUE path, so that their spans name it.
func WithResource(ctx context.Context, addr string) context.Context {
	return context.WithValue(ctx, resourceKey{}, addr)
}

// Traced wraps a Provider so that every RPC is an OpenTelemetry span and a
// duration measurement. It adds the resource type and provider name, never
// values.
type Traced struct {
	inner Provider
	name  string
	// instance is the key of the provider block the process serves, "aws.west" for one of several, "" before it is known.
	instance string

	mu      sync.Mutex
	running []context.Context
	capture *netcap.Capture
}

// AttachCapture makes the network calls of the provider p spans under the RPC that is running, and closes c with p. It does
// nothing for a provider that is not traced, and then c is closed at once.
func AttachCapture(p Provider, c *netcap.Capture) {
	t, ok := p.(*Traced)
	if !ok {
		c.Close()
		return
	}
	t.capture = c
	c.Parent = t.parent
}

// parent is the context of the newest RPC still running, which is the best guess for who a request belongs to when several
// run at once; nil when none does.
func (t *Traced) parent() context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n := len(t.running); n > 0 {
		return t.running[n-1]
	}
	return nil
}

func (t *Traced) push(ctx context.Context) {
	t.mu.Lock()
	t.running = append(t.running, ctx)
	t.mu.Unlock()
}

func (t *Traced) pop(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := len(t.running) - 1; i >= 0; i-- {
		if t.running[i] == ctx {
			t.running = append(t.running[:i], t.running[i+1:]...)
			return
		}
	}
}

// SetInstance says which instance of the provider p is, as the specification writes it ("$aws"), so that its spans name it. It does nothing for a provider that is
// not traced.
func SetInstance(p Provider, key string) {
	if t, ok := p.(*Traced); ok {
		t.instance = key
	}
}

// NewTraced wraps p; name is the provider's local name.
func NewTraced(p Provider, name string) Provider { return &Traced{inner: p, name: name} }

// begin starts the span for one RPC and returns the function that ends it.
func (t *Traced) begin(ctx context.Context, method, typ string) (context.Context, func(error, Diagnostics)) {
	attrs := []attribute.KeyValue{
		attribute.String("rpc.system", "grpc"),
		attribute.String("rpc.service", "tfplugin.Provider"),
		attribute.String("rpc.method", method),
		attribute.String("pravo.provider", t.name),
		attribute.Int("pravo.protocol", t.inner.ProtocolVersion()),
	}
	if t.instance != "" {
		attrs = append(attrs, attribute.String("pravo.provider.instance", t.instance))
	}
	if typ != "" {
		attrs = append(attrs, attribute.String("pravo.resource.type", typ))
	}
	if addr, ok := ctx.Value(resourceKey{}).(string); ok && addr != "" {
		attrs = append(attrs, attribute.String("pravo.resource.address", addr))
	}
	ctx, span := telemetry.Tracer().Start(ctx, "provider "+method, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
	start := time.Now()
	if t.capture != nil {
		t.push(ctx)
	}
	return ctx, func(err error, diags Diagnostics) {
		if t.capture != nil {
			t.pop(ctx)
		}
		if err == nil {
			err = diags.Err()
		}
		if n := len(diags.Warnings()); n > 0 {
			span.SetAttributes(attribute.Int("pravo.warnings", n))
		}
		telemetry.RPC(ctx, t.name, method, time.Since(start), err != nil)
		telemetry.End(span, err)
	}
}

func (t *Traced) ProtocolVersion() int { return t.inner.ProtocolVersion() }
func (t *Traced) Close() {
	t.inner.Close()
	if t.capture != nil {
		t.capture.Close()
	}
}

// Stop is shutdown housekeeping, run outside any trace; it is not a span.
func (t *Traced) Stop(ctx context.Context) error { return t.inner.Stop(ctx) }

func (t *Traced) GetSchema(ctx context.Context) (*schema.ProviderSchema, Diagnostics, error) {
	ctx, end := t.begin(ctx, "GetSchema", "")
	ps, d, err := t.inner.GetSchema(ctx)
	end(err, d)
	return ps, d, err
}

func (t *Traced) ValidateProviderConfig(ctx context.Context, cfg tftypes.Value) (Diagnostics, error) {
	ctx, end := t.begin(ctx, "ValidateProviderConfig", "")
	d, err := t.inner.ValidateProviderConfig(ctx, cfg)
	end(err, d)
	return d, err
}

func (t *Traced) Configure(ctx context.Context, cfg tftypes.Value) (Diagnostics, error) {
	ctx, end := t.begin(ctx, "ConfigureProvider", "")
	d, err := t.inner.Configure(ctx, cfg)
	end(err, d)
	return d, err
}

func (t *Traced) ValidateResourceConfig(ctx context.Context, typ string, cfg tftypes.Value) (Diagnostics, error) {
	ctx, end := t.begin(ctx, "ValidateResourceConfig", typ)
	d, err := t.inner.ValidateResourceConfig(ctx, typ, cfg)
	end(err, d)
	return d, err
}

func (t *Traced) ValidateDataSourceConfig(ctx context.Context, typ string, cfg tftypes.Value) (Diagnostics, error) {
	ctx, end := t.begin(ctx, "ValidateDataSourceConfig", typ)
	d, err := t.inner.ValidateDataSourceConfig(ctx, typ, cfg)
	end(err, d)
	return d, err
}

func (t *Traced) ValidateEphemeralConfig(ctx context.Context, typ string, cfg tftypes.Value) (Diagnostics, error) {
	ctx, end := t.begin(ctx, "ValidateEphemeralResourceConfig", typ)
	d, err := t.inner.ValidateEphemeralConfig(ctx, typ, cfg)
	end(err, d)
	return d, err
}

func (t *Traced) UpgradeResourceState(ctx context.Context, typ string, version int64, jsonState []byte, want tftypes.Type) (tftypes.Value, Diagnostics, error) {
	ctx, end := t.begin(ctx, "UpgradeResourceState", typ)
	v, d, err := t.inner.UpgradeResourceState(ctx, typ, version, jsonState, want)
	end(err, d)
	return v, d, err
}

func (t *Traced) ReadResource(ctx context.Context, typ string, state tftypes.Value, private []byte) (tftypes.Value, []byte, Diagnostics, error) {
	ctx, end := t.begin(ctx, "ReadResource", typ)
	v, p, d, err := t.inner.ReadResource(ctx, typ, state, private)
	end(err, d)
	return v, p, d, err
}

func (t *Traced) PlanResourceChange(ctx context.Context, typ string, prior, proposed, config tftypes.Value, priorPrivate []byte) (*PlanResult, Diagnostics, error) {
	ctx, end := t.begin(ctx, "PlanResourceChange", typ)
	r, d, err := t.inner.PlanResourceChange(ctx, typ, prior, proposed, config, priorPrivate)
	end(err, d)
	return r, d, err
}

func (t *Traced) ApplyResourceChange(ctx context.Context, typ string, prior, planned, config tftypes.Value, plannedPrivate []byte) (*ApplyResult, Diagnostics, error) {
	ctx, end := t.begin(ctx, "ApplyResourceChange", typ)
	r, d, err := t.inner.ApplyResourceChange(ctx, typ, prior, planned, config, plannedPrivate)
	end(err, d)
	return r, d, err
}

func (t *Traced) ImportResourceState(ctx context.Context, typ string, target ImportTarget, types map[string]tftypes.Type) ([]ImportedResource, Diagnostics, error) {
	ctx, end := t.begin(ctx, "ImportResourceState", typ)
	r, d, err := t.inner.ImportResourceState(ctx, typ, target, types)
	end(err, d)
	return r, d, err
}

func (t *Traced) ReadDataSource(ctx context.Context, typ string, cfg tftypes.Value) (tftypes.Value, Diagnostics, error) {
	ctx, end := t.begin(ctx, "ReadDataSource", typ)
	v, d, err := t.inner.ReadDataSource(ctx, typ, cfg)
	end(err, d)
	return v, d, err
}

func (t *Traced) OpenEphemeral(ctx context.Context, typ string, cfg tftypes.Value) (*EphemeralResult, Diagnostics, error) {
	ctx, end := t.begin(ctx, "OpenEphemeralResource", typ)
	r, d, err := t.inner.OpenEphemeral(ctx, typ, cfg)
	end(err, d)
	if err == nil && !d.HasErrors() {
		telemetry.EphemeralOpened(ctx, typ)
	}
	return r, d, err
}

func (t *Traced) ListResource(ctx context.Context, typ string, cfg tftypes.Value, limit int64) ([]ListedResource, Diagnostics, error) {
	ctx, end := t.begin(ctx, "ListResource", typ)
	out, d, err := t.inner.ListResource(ctx, typ, cfg, limit)
	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("pravo.listed", len(out)))
	end(err, d)
	return out, d, err
}

func (t *Traced) RenewEphemeral(ctx context.Context, typ string, private []byte) (*EphemeralResult, Diagnostics, error) {
	ctx, end := t.begin(ctx, "RenewEphemeralResource", typ)
	r, d, err := t.inner.RenewEphemeral(ctx, typ, private)
	end(err, d)
	return r, d, err
}

func (t *Traced) CloseEphemeral(ctx context.Context, typ string, private []byte) (Diagnostics, error) {
	ctx, end := t.begin(ctx, "CloseEphemeralResource", typ)
	d, err := t.inner.CloseEphemeral(ctx, typ, private)
	end(err, d)
	return d, err
}

func (t *Traced) CallFunction(ctx context.Context, name string, args []tftypes.Value, argTypes []tftypes.Type, ret tftypes.Type) (tftypes.Value, *FunctionError, error) {
	ctx, end := t.begin(ctx, "CallFunction", "")
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("pravo.function", name))
	v, ferr, err := t.inner.CallFunction(ctx, name, args, argTypes, ret)
	if err == nil && ferr != nil {
		err = ferr
	}
	end(err, nil)
	telemetry.FunctionCalled(ctx, name)
	return v, ferr, err
}
