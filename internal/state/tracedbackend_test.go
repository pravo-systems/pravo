package state

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/telemetry"
)

// The requests a store sends to its backend are spans under the context the store
// was given, with the key and the kind of store, and a missing object is not a failure.
func TestStoreRequestsAreSpans(t *testing.T) {
	t.Setenv("PRAVO_TRACES", "on")
	t.Setenv("OTEL_SDK_DISABLED", "")
	telemetry.Configure(env.OS())
	t.Cleanup(func() { telemetry.Configure(env.Env{}) })
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

// The requests of a remote store are network spans under the state request that sent them, with the status and the sizes and
// not the query, the token or the body.
func TestRemoteStoreRequestsAreNetworkSpans(t *testing.T) {
	t.Setenv("PRAVO_TRACES", "on")
	t.Setenv("OTEL_SDK_DISABLED", "")
	telemetry.Configure(env.OS())
	t.Cleanup(func() { telemetry.Configure(env.Env{}) })
	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	defer otel.SetTracerProvider(prev)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	hb, err := NewHTTPBackend(HTTPConfig{URL: srv.URL + "/team", Token: "tok-secret"})
	if err != nil {
		t.Fatal(err)
	}
	b := &tracedBackend{Backend: hb}
	ctx, root := otel.Tracer("test").Start(context.Background(), "phase test")
	if _, err := b.Get(ctx, "missing.json"); err != ErrNotFound {
		t.Fatalf("get: %v", err)
	}
	root.End()

	ids := map[string]string{}
	for _, s := range rec.Ended() {
		ids[s.Name()] = s.SpanContext().SpanID().String()
	}
	var found bool
	for _, s := range rec.Ended() {
		if !strings.HasPrefix(s.Name(), "network GET ") {
			continue
		}
		found = true
		if s.Parent().SpanID().String() != ids["state get missing.json"] {
			t.Errorf("parent of %q is not the state request", s.Name())
		}
		for _, kv := range s.Attributes() {
			if strings.Contains(kv.Value.Emit(), "secret") {
				t.Errorf("%s holds %q", kv.Key, kv.Value.Emit())
			}
			if kv.Key == "http.response.status_code" && kv.Value.AsInt64() != 404 {
				t.Errorf("status %v", kv.Value.AsInt64())
			}
		}
	}
	if !found {
		t.Error("no network span")
	}
}

// Opening a sealed state records who opened it: the name of the identity and its public key, in the span of the keys and in
// the span that was running.
func TestUnsealRecordsTheIdentity(t *testing.T) {
	t.Setenv("PRAVO_TRACES", "on")
	t.Setenv("OTEL_SDK_DISABLED", "")
	telemetry.Configure(env.OS())
	t.Cleanup(func() { telemetry.Configure(env.Env{}) })
	ada := testIdentity(t, "ada")
	b := testBackends()[0].make(t)
	sealedStore(t, b, ada).Close()

	rec := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	defer otel.SetTracerProvider(prev)
	ctx, root := otel.Tracer("test").Start(context.Background(), "setup open state")
	s, err := OpenBackendWith(ctx, b, Options{Identity: ada})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	root.End()
	for _, sp := range rec.Ended() {
		if sp.Name() != "unseal state keys" {
			continue
		}
		var name, reader string
		for _, kv := range sp.Attributes() {
			switch kv.Key {
			case "pravo.identity":
				name = kv.Value.AsString()
			case "pravo.identity.reader":
				reader = kv.Value.AsString()
			}
		}
		if name != "ada" || reader == "" {
			t.Errorf("identity %q reader %q", name, reader)
		}
		return
	}
	t.Error("no unseal span")
}
