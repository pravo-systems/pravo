package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

type collector struct {
	mu      sync.Mutex
	traces  []*coltrace.ExportTraceServiceRequest
	metrics []*colmetrics.ExportMetricsServiceRequest
}

func (c *collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch r.URL.Path {
	case "/v1/traces":
		var req coltrace.ExportTraceServiceRequest
		if proto.Unmarshal(body, &req) == nil {
			c.traces = append(c.traces, &req)
		}
		out, _ := proto.Marshal(&coltrace.ExportTraceServiceResponse{})
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.Write(out)
	case "/v1/metrics":
		var req colmetrics.ExportMetricsServiceRequest
		if proto.Unmarshal(body, &req) == nil {
			c.metrics = append(c.metrics, &req)
		}
		out, _ := proto.Marshal(&colmetrics.ExportMetricsServiceResponse{})
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.Write(out)
	default:
		http.NotFound(w, r)
	}
}

func TestEnabledFollowsEnvironment(t *testing.T) {
	for _, k := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_TRACES_EXPORTER", "OTEL_METRICS_EXPORTER", "OTEL_SDK_DISABLED"} {
		t.Setenv(k, "")
	}
	if Enabled() {
		t.Fatal("telemetry must be off without configuration")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	if !Enabled() || tracesExporter() != "otlp" || metricsExporter() != "otlp" {
		t.Fatal("an OTLP endpoint turns traces and metrics on")
	}
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	if metricsExporter() != "none" || tracesExporter() != "otlp" {
		t.Fatal("per-signal exporter selection")
	}
	t.Setenv("OTEL_SDK_DISABLED", "true")
	if Enabled() {
		t.Fatal("OTEL_SDK_DISABLED wins")
	}
}

func TestExportsSpansAndMetricsOverOTLPHTTP(t *testing.T) {
	c := &collector{}
	srv := httptest.NewServer(c)
	defer srv.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", srv.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_SERVICE_NAME", "pravo-test")

	shutdown := Setup(context.Background(), "9.9.9")
	ctx, parent := Tracer().Start(context.Background(), "pravo plan")
	_, child := Tracer().Start(ctx, "resource random_pet.a")
	child.End()
	parent.End()
	RunDone(ctx, "pravo plan", 3*time.Millisecond, 0)
	RPC(ctx, "random", "PlanResourceChange", time.Millisecond, false)
	Nodes(ctx, "planned", 2)
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.traces) == 0 {
		t.Fatal("no trace export reached the collector")
	}
	names := map[string]bool{}
	var svc, version string
	for _, req := range c.traces {
		for _, rs := range req.ResourceSpans {
			for _, a := range rs.Resource.Attributes {
				switch a.Key {
				case "service.name":
					svc = a.Value.GetStringValue()
				case "service.version":
					version = a.Value.GetStringValue()
				}
			}
			for _, ss := range rs.ScopeSpans {
				for _, s := range ss.Spans {
					names[s.Name] = true
				}
			}
		}
	}
	if !names["pravo plan"] || !names["resource random_pet.a"] {
		t.Errorf("spans exported: %v", names)
	}
	if svc != "pravo-test" {
		t.Errorf("OTEL_SERVICE_NAME ignored: service.name = %q", svc)
	}
	if version != "9.9.9" {
		t.Errorf("service.version = %q", version)
	}
	metricNames := map[string]bool{}
	for _, req := range c.metrics {
		for _, rm := range req.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					metricNames[m.Name] = true
				}
			}
		}
	}
	for _, want := range []string{"pravo.run.duration", "pravo.provider.rpc.duration", "pravo.plan.nodes"} {
		if !metricNames[want] {
			t.Errorf("metric %s not exported; got %v", want, metricNames)
		}
	}
}

func TestTraceparentRoundTrip(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("TRACEPARENT", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	ctx := ParentFromEnv(context.Background())
	// Without a recording tracer the extracted context is remote but valid.
	env := Environ(ctx)
	if len(env) == 0 || !strings.HasPrefix(env[0], "TRACEPARENT=00-4bf92f3577b34da6a3ce929d0e0e4736-") {
		t.Fatalf("Environ(ctx) = %v: the trace must continue into child processes", env)
	}
}
