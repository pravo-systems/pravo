package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/pravo-systems/pravo/internal/env"
)

func clearEnv(t *testing.T) {
	for _, k := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_TRACES_EXPORTER", "OTEL_METRICS_EXPORTER", "OTEL_SDK_DISABLED", "PRAVO_TRACES"} {
		t.Setenv(k, "")
	}
	t.Setenv("PRAVO_TRACES", "on") // the record is off until it is asked for
}

// A run that has engine spans is written to the trace directory, with the nesting,
// the attributes, the events and the failure of its spans; the trace is not
// named in the documents of a command.
func TestRecorderWritesARunThatRanAnEngine(t *testing.T) {
	clearEnv(t)
	dir := filepath.Join(t.TempDir(), "traces")
	t.Setenv("PRAVO_TRACE_DIR", dir)
	shutdown := Setup(context.Background(), "test", env.OS())
	defer func() { localOnly = false; otel.SetTracerProvider(otel.GetTracerProvider()) }()

	ctx, cmd := Tracer().Start(context.Background(), "pravo apply")
	ctx, eng := Tracer().Start(ctx, "engine apply", traceAttrs("pravo.txn", "20260101T000000Z-abc123"))
	_, res := Tracer().Start(ctx, "resource web")
	res.AddEvent("task", traceAttr("action", "create"))
	time.Sleep(2 * time.Millisecond)
	End(res, errors.New("boom"))
	End(eng, nil)
	End(cmd, nil)
	if id := TraceID(ctx); id != "" {
		t.Fatalf("a trace that only the record holds is named: %q", id)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) != 1 {
		t.Fatalf("files %v", files)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); string(b) != "*\n" {
		t.Errorf("gitignore %q", b)
	}
	var rt RunTrace
	b, _ := os.ReadFile(files[0])
	if err := json.Unmarshal(b, &rt); err != nil {
		t.Fatal(err)
	}
	if rt.Version != TraceFormat || len(rt.Spans) != 3 || rt.Service != "pravo" {
		t.Fatalf("%+v", rt)
	}
	by := map[string]SpanData{}
	for _, s := range rt.Spans {
		by[s.Name] = s
	}
	root, e, r := by["pravo apply"], by["engine apply"], by["resource web"]
	if root.Parent != "" || e.Parent != root.ID || r.Parent != e.ID {
		t.Errorf("nesting: %+v", rt.Spans)
	}
	if e.Attrs["pravo.txn"] != "20260101T000000Z-abc123" || r.Status != "error" || r.Message != "boom" || len(r.Events) != 2 || r.Events[0].Attrs["action"] != "create" {
		t.Errorf("span data: %+v %+v", e, r)
	}
	if r.DurUS < 2000 || r.StartUS < 0 || rt.DurationUS < r.DurUS {
		t.Errorf("times: %+v in %d", r, rt.DurationUS)
	}
}

// A command that did some work is recorded even when it ran no engine (a read of
// the state, a load of the specification), and the viewer's own run is not.
func TestRecorderWritesAnyCommandThatDidWork(t *testing.T) {
	for name, want := range map[string]int{"pravo state list": 1, "pravo serve": 0} {
		clearEnv(t)
		dir := filepath.Join(t.TempDir(), "traces")
		t.Setenv("PRAVO_TRACE_DIR", dir)
		shutdown := Setup(context.Background(), "test", env.OS())
		ctx, cmd := Tracer().Start(context.Background(), name)
		_, st := Tracer().Start(ctx, "state get meta.json")
		End(st, nil)
		End(cmd, nil)
		shutdown(context.Background())
		localOnly = false
		files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		if len(files) != want {
			t.Errorf("%s: %d files, want %d", name, len(files), want)
		}
	}
}

// A command that did nothing leaves nothing behind, and the record is off unless
// PRAVO_TRACES=on asks for it.
func TestRecorderIgnoresOtherRunsAndCanBeOff(t *testing.T) {
	clearEnv(t)
	dir := filepath.Join(t.TempDir(), "traces")
	t.Setenv("PRAVO_TRACE_DIR", dir)
	shutdown := Setup(context.Background(), "test", env.OS())
	_, cmd := Tracer().Start(context.Background(), "pravo version")
	End(cmd, nil)
	shutdown(context.Background())
	if _, err := os.Stat(dir); err == nil {
		t.Error("a run with no work was written")
	}
	localOnly = false

	for _, v := range []string{"", "off", "0", "nonsense"} {
		t.Setenv("PRAVO_TRACES", v)
		Configure(env.OS())
		if recordingEnabled() {
			t.Errorf("PRAVO_TRACES=%q turns the record on", v)
		}
	}
	t.Setenv("PRAVO_TRACES", "on")
	Configure(env.OS())
	if !recordingEnabled() {
		t.Error("PRAVO_TRACES=on does not turn the record on")
	}
	t.Setenv("OTEL_SDK_DISABLED", "true")
	Configure(env.OS())
	if recordingEnabled() {
		t.Error("OTEL_SDK_DISABLED does not turn the record off")
	}
}

func TestTraceDirKeepsTheNewestRuns(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		rt := &RunTrace{Version: TraceFormat, TraceID: string(rune('a'+i)) + "0000000000000000000000000000000"}
		id := rt.ID()
		if err := writeTrace(dir, rt); err != nil {
			t.Fatal(err)
		}
		past := time.Now().Add(time.Duration(i-10) * time.Minute)
		os.Chtimes(filepath.Join(dir, id+".json"), past, past)
	}
	trim(dir, 2)
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) != 2 {
		t.Fatalf("kept %v", files)
	}
}

func traceAttrs(k, v string) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String(k, v))
}
func traceAttr(k, v string) trace.EventOption { return trace.WithAttributes(attribute.String(k, v)) }
