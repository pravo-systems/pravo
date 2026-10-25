// Package telemetry wires OpenTelemetry traces and metrics into pravo.
//
// It is off unless configured with the standard OTEL_* environment
// variables; when off, the global OpenTelemetry API is a no-op and the
// instrumentation elsewhere costs next to nothing. Spans and metrics carry
// addresses, types, actions and outcomes, never attribute values: a
// resource's values may be secrets.
package telemetry

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const scope = "pravo.dev/pravo"

// Tracer is the tracer for pravo's own spans.
func Tracer() trace.Tracer { return otel.Tracer(scope) }

// Meter is the meter for pravo's own metrics.
func Meter() metric.Meter { return otel.Meter(scope) }

// Instruments are created once from the global meter, which delegates to the
// real provider when Setup installs one.
var (
	runDuration, _   = Meter().Float64Histogram("pravo.run.duration", metric.WithUnit("s"), metric.WithDescription("Duration of a pravo command"))
	rpcDuration, _   = Meter().Float64Histogram("pravo.provider.rpc.duration", metric.WithUnit("s"), metric.WithDescription("Duration of a provider RPC"))
	resourceOps, _   = Meter().Int64Counter("pravo.resource.operations", metric.WithDescription("Resource operations by action and outcome"))
	nodes, _         = Meter().Int64Counter("pravo.plan.nodes", metric.WithDescription("Nodes in a plan by disposition: planned, skipped, refreshed, fresh, drifted"))
	lockWait, _      = Meter().Float64Histogram("pravo.lock.acquire.duration", metric.WithUnit("s"), metric.WithDescription("Time to take the locks of an apply"))
	rejections, _    = Meter().Int64Counter("pravo.rejections", metric.WithDescription("Runs refused by a policy, a lock or a reservation"))
	ephemerals, _    = Meter().Int64Counter("pravo.ephemeral.opened", metric.WithDescription("Ephemeral resources opened"))
	functionCalls, _ = Meter().Int64Counter("pravo.function.calls", metric.WithDescription("Provider function calls"))
)

// Enabled reports whether telemetry is configured through the environment.
func Enabled() bool {
	return tracesExporter() != "none" || metricsExporter() != "none"
}

// Active reports whether anything takes the spans: an exporter configured
// through the environment, or the local record of the run.
func Active() bool { return Enabled() || recordingEnabled() }

func endpointConfigured() bool {
	for _, k := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"} {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}

func exporterName(env string) string {
	if strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true") {
		return "none"
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv(env)))
	switch v {
	case "":
		if endpointConfigured() {
			return "otlp"
		}
		return "none"
	case "otlp", "console", "none":
		return v
	}
	// e.g. "prometheus", "logging": unsupported here; treat as off.
	return "none"
}

func tracesExporter() string  { return exporterName("OTEL_TRACES_EXPORTER") }
func metricsExporter() string { return exporterName("OTEL_METRICS_EXPORTER") }

func protocol(signal string) string {
	for _, k := range []string{"OTEL_EXPORTER_OTLP_" + signal + "_PROTOCOL", "OTEL_EXPORTER_OTLP_PROTOCOL"} {
		if v := strings.ToLower(os.Getenv(k)); v != "" {
			return v
		}
	}
	return "http/protobuf"
}

var errOnce sync.Once

// Setup installs the OpenTelemetry providers configured by the environment
// and returns a function that flushes and shuts them down. Failures to
// export never fail a command; a single warning goes to stderr.
func Setup(ctx context.Context, version string) (shutdown func(context.Context) error) {
	noop := func(context.Context) error { return nil }
	record := recordingEnabled()
	if !Enabled() && !record {
		return noop
	}
	localOnly = tracesExporter() == "none"
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		errOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "pravo: telemetry export problem (further ones are not shown): %v\n", err)
		})
	}))
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	res, err := resource.New(ctx,
		resource.WithFromEnv(), resource.WithTelemetrySDK(), resource.WithHost(), resource.WithProcessPID(),
		resource.WithAttributes(semconv.ServiceName("pravo"), semconv.ServiceVersion(version)),
	)
	if err != nil {
		res = resource.Default()
	}
	// OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES (read by FromEnv) take
	// precedence over the defaults above because FromEnv is listed first
	// only for detection; merge explicitly.
	if n := os.Getenv("OTEL_SERVICE_NAME"); n != "" {
		if merged, err := resource.Merge(res, resource.NewSchemaless(semconv.ServiceName(n))); err == nil {
			res = merged
		}
	}

	var shutdowns []func(context.Context) error

	tpOpts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	switch tracesExporter() {
	case "otlp":
		var exp sdktrace.SpanExporter
		var err error
		if strings.HasPrefix(protocol("TRACES"), "grpc") {
			exp, err = otlptracegrpc.New(ctx)
		} else {
			exp, err = otlptracehttp.New(ctx)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "pravo: telemetry traces disabled: %v\n", err)
			localOnly = true
		} else {
			tpOpts = append(tpOpts, sdktrace.WithBatcher(exp))
		}
	case "console":
		exp, err := stdouttrace.New(stdouttrace.WithWriter(os.Stderr))
		if err == nil {
			tpOpts = append(tpOpts, sdktrace.WithSyncer(exp))
		} else {
			localOnly = true
		}
	}
	if record {
		tpOpts = append(tpOpts, sdktrace.WithSpanProcessor(&recorder{}))
	}
	if len(tpOpts) > 1 {
		tp := sdktrace.NewTracerProvider(tpOpts...)
		otel.SetTracerProvider(tp)
		shutdowns = append(shutdowns, tp.Shutdown)
	}

	switch metricsExporter() {
	case "otlp":
		var exp sdkmetric.Exporter
		var err error
		if strings.HasPrefix(protocol("METRICS"), "grpc") {
			exp, err = otlpmetricgrpc.New(ctx)
		} else {
			exp, err = otlpmetrichttp.New(ctx)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "pravo: telemetry metrics disabled: %v\n", err)
		} else {
			// A CLI lives for seconds: export once, at shutdown.
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(time.Hour))), sdkmetric.WithResource(res))
			otel.SetMeterProvider(mp)
			shutdowns = append(shutdowns, mp.Shutdown)
		}
	case "console":
		exp, err := stdoutmetric.New(stdoutmetric.WithWriter(os.Stderr))
		if err == nil {
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(time.Hour))), sdkmetric.WithResource(res))
			otel.SetMeterProvider(mp)
			shutdowns = append(shutdowns, mp.Shutdown)
		}
	}

	return func(ctx context.Context) error {
		// Bound the flush so that a dead collector cannot hang the command.
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var first error
		for _, f := range shutdowns {
			if err := f(ctx); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
}

// ParentFromEnv continues a trace started by the caller: the standard
// TRACEPARENT and TRACESTATE environment variables, as set by CI systems
// and by the MCP server for its tool calls.
func ParentFromEnv(ctx context.Context) context.Context {
	carrier := propagation.MapCarrier{}
	if v := os.Getenv("TRACEPARENT"); v != "" {
		carrier["traceparent"] = v
	}
	if v := os.Getenv("TRACESTATE"); v != "" {
		carrier["tracestate"] = v
	}
	if len(carrier) == 0 {
		return ctx
	}
	return propagation.TraceContext{}.Extract(ctx, carrier)
}

// Environ returns TRACEPARENT/TRACESTATE entries for a child process, so a
// subprocess continues the trace in ctx.
func Environ(ctx context.Context) []string {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	var out []string
	if v := carrier["traceparent"]; v != "" {
		out = append(out, "TRACEPARENT="+v)
	}
	if v := carrier["tracestate"]; v != "" {
		out = append(out, "TRACESTATE="+v)
	}
	return out
}

// localOnly is set when the spans go nowhere but the local record of the run.
// Such a trace is not named in the state or in the documents of a command: the
// pravo.txn attribute of its engine span names the transaction instead.
var localOnly bool

// TraceID returns the trace id of the span in ctx, or "". It is empty when
// the trace is only recorded locally, so that a command prints the same
// documents with or without the record.
func TraceID(ctx context.Context) string {
	if localOnly {
		return ""
	}
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.TraceID().String()
}

// End finishes a span, recording err if there is one.
func End(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

// ---- recording helpers used by the engine and the CLI ----

// RunDone records the duration and outcome of a command.
func RunDone(ctx context.Context, command string, d time.Duration, exit int) {
	runDuration.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String("pravo.command", command), attribute.Int("pravo.exit_code", exit)))
}

// RPC records one provider RPC.
func RPC(ctx context.Context, provider, method string, d time.Duration, failed bool) {
	rpcDuration.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.String("pravo.provider", provider), attribute.String("rpc.method", method), attribute.Bool("error", failed)))
}

// Resource records a resource operation.
func Resource(ctx context.Context, mode, typ, action, outcome string) {
	resourceOps.Add(ctx, 1, metric.WithAttributes(attribute.String("pravo.mode", mode), attribute.String("pravo.resource.type", typ), attribute.String("pravo.action", action), attribute.String("pravo.outcome", outcome)))
}

// Nodes records how many nodes a plan disposed of in a given way.
func Nodes(ctx context.Context, disposition string, n int) {
	if n > 0 {
		nodes.Add(ctx, int64(n), metric.WithAttributes(attribute.String("pravo.disposition", disposition)))
	}
}

// LockWait records the time an apply took to acquire its locks.
func LockWait(ctx context.Context, d time.Duration, held int) {
	lockWait.Record(ctx, d.Seconds(), metric.WithAttributes(attribute.Int("pravo.locks", held)))
}

// Rejected records a refusal by reason: policy, locked, reserved, stale.
func Rejected(ctx context.Context, reason string) {
	rejections.Add(ctx, 1, metric.WithAttributes(attribute.String("pravo.reason", reason)))
}

// EphemeralOpened records an ephemeral resource opening.
func EphemeralOpened(ctx context.Context, typ string) {
	ephemerals.Add(ctx, 1, metric.WithAttributes(attribute.String("pravo.resource.type", typ)))
}

// FunctionCalled records a provider function call.
func FunctionCalled(ctx context.Context, name string) {
	functionCalls.Add(ctx, 1, metric.WithAttributes(attribute.String("pravo.function", name)))
}
