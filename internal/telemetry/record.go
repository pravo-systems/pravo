package telemetry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/pravo-systems/pravo/internal/env"
)

// The recorder keeps the spans of a run in a file of the project, so that
// "pravo serve" can show the run without a collector. It is off until
// PRAVO_TRACES=on turns it on: a run that is recorded leaves a file for each command, which is not something to do
// to a project that did not ask for it. It keeps the spans that the other exporters get: the
// addresses, types, actions and counts of the run, never attribute values.
//
// A run is written only if the command did some work, which is to say that it
// has spans besides its own (a plan, an apply, a discovery, a read of the
// state, a load of the specification); "pravo version" leaves nothing, and
// neither does "pravo serve", which runs until it is stopped and would hold the
// spans of everything that it read. It is written to .pravo/traces/<trace id>.json of the directory the command ran in, or to
// $PRAVO_TRACE_DIR. The directory keeps the newest KeepTraces runs and
// ignores itself in git.

// TraceFormat is the version of the file a run is written to.
const TraceFormat = 1

// KeepTraces is how many runs the directory keeps.
const KeepTraces = 100

// maxSpans bounds the spans of a run held in memory. Past it the provider
// calls and the requests to the state are dropped, and counted; the command, engine and resource spans never are.
const maxSpans = 20000

// maxSpansHard bounds the rest.
const maxSpansHard = 100000

// RunTrace is the file of one run.
type RunTrace struct {
	Version int    `json:"version"`
	TraceID string `json:"trace_id"`
	Service string `json:"service"`
	// Start is the start of the earliest span, RFC 3339 with nanoseconds.
	Start string `json:"start"`
	// DurationUS is from the start of the earliest span to the end of the last.
	DurationUS int64 `json:"duration_us"`
	// Dropped counts the provider calls and state requests that were left out of a very large run.
	Dropped int        `json:"dropped,omitempty"`
	Spans   []SpanData `json:"spans"`
}

// ID names the run: the trace and its first root span. Commands that a caller
// ran in one trace (TRACEPARENT, as CI sets it) are each a run.
func (t *RunTrace) ID() string {
	ids := map[string]bool{}
	for _, s := range t.Spans {
		ids[s.ID] = true
	}
	for _, s := range t.Spans {
		if s.Parent == "" || !ids[s.Parent] {
			return t.TraceID + "-" + s.ID
		}
	}
	return t.TraceID
}

// SpanData is one span. Times are microseconds from the start of the run.
type SpanData struct {
	ID     string `json:"id"`
	Parent string `json:"parent,omitempty"`
	Name   string `json:"name"`
	// Status is "ok", "error" or "" (unset).
	Status  string         `json:"status,omitempty"`
	Message string         `json:"message,omitempty"`
	StartUS int64          `json:"start_us"`
	DurUS   int64          `json:"dur_us"`
	Attrs   map[string]any `json:"attrs,omitempty"`
	Events  []EventData    `json:"events,omitempty"`
}

// EventData is an event on a span: a debug event of the log.
type EventData struct {
	Name  string         `json:"name"`
	AtUS  int64          `json:"at_us"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// Recording reports whether the runs are being recorded in the project (PRAVO_TRACES=on).
func Recording() bool { return recordingEnabled() }

func recordingEnabled() bool {
	if strings.EqualFold(environ().Get("OTEL_SDK_DISABLED"), "true") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(environ().Get("PRAVO_TRACES"))) {
	case "on", "true", "1", "yes":
		return true
	}
	return false
}

// RecordingNet reports whether the calls that providers make over the network are
// recorded too: the runs are recorded, and PRAVO_TRACES_NET is not off.
func RecordingNet() bool {
	if !recordingEnabled() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(environ().Get("PRAVO_TRACES_NET"))) {
	case "off", "false", "0", "none":
		return false
	}
	return true
}

// TraceDir is where the runs of a command started in cwd are kept.
func TraceDir(e env.Env) string {
	if d := e.Get("PRAVO_TRACE_DIR"); d != "" {
		return d
	}
	return filepath.Join(".pravo", "traces")
}

// recorder is a span processor that holds the finished spans until shutdown.
type recorder struct {
	mu      sync.Mutex
	spans   []sdktrace.ReadOnlySpan
	dropped int
}

func (r *recorder) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

func (r *recorder) OnEnd(s sdktrace.ReadOnlySpan) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.spans)
	if n >= maxSpansHard || (n >= maxSpans && (strings.HasPrefix(s.Name(), "provider ") || strings.HasPrefix(s.Name(), "network ") || strings.HasPrefix(s.Name(), "state ") || strings.HasPrefix(s.Name(), "seal ") || strings.HasPrefix(s.Name(), "unseal "))) {
		r.dropped++
		return
	}
	r.spans = append(r.spans, s)
}

func (r *recorder) ForceFlush(context.Context) error { return nil }

// Shutdown writes the runs whose command did some work (see didWork).
func (r *recorder) Shutdown(context.Context) error {
	r.mu.Lock()
	spans, dropped := r.spans, r.dropped
	r.spans = nil
	r.mu.Unlock()
	if len(spans) == 0 {
		return nil
	}
	by := map[string][]sdktrace.ReadOnlySpan{}
	for _, s := range spans {
		id := s.SpanContext().TraceID().String()
		by[id] = append(by[id], s)
	}
	dir := TraceDir(environ())
	for id, list := range by {
		if !didWork(list) {
			continue
		}
		_ = writeTrace(dir, buildTrace(id, list, dropped))
	}
	return nil
}

// didWork reports whether the run is of a command that has a span besides its
// own, and is not the viewer's. A trace without the span of a command, such as
// the requests that a store made outside of one, is not a run.
func didWork(spans []sdktrace.ReadOnlySpan) bool {
	command, other := false, false
	for _, s := range spans {
		switch {
		case s.Name() == "pravo serve":
			return false
		case strings.HasPrefix(s.Name(), "pravo "):
			command = true
		default:
			other = true
		}
	}
	return command && other
}

func buildTrace(id string, spans []sdktrace.ReadOnlySpan, dropped int) *RunTrace {
	t0, t1 := spans[0].StartTime(), spans[0].EndTime()
	service := ""
	for _, s := range spans {
		if s.StartTime().Before(t0) {
			t0 = s.StartTime()
		}
		if s.EndTime().After(t1) {
			t1 = s.EndTime()
		}
		if service == "" {
			for _, kv := range s.Resource().Attributes() {
				if kv.Key == "service.name" {
					service = kv.Value.AsString()
				}
			}
		}
	}
	rt := &RunTrace{Version: TraceFormat, TraceID: id, Service: service, Start: t0.UTC().Format(time.RFC3339Nano), DurationUS: t1.Sub(t0).Microseconds(), Dropped: dropped}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].StartTime().Before(spans[j].StartTime()) })
	for _, s := range spans {
		d := SpanData{
			ID: s.SpanContext().SpanID().String(), Name: s.Name(),
			StartUS: s.StartTime().Sub(t0).Microseconds(), DurUS: s.EndTime().Sub(s.StartTime()).Microseconds(),
			Attrs: attrMap(s.Attributes()),
		}
		if p := s.Parent(); p.SpanID().IsValid() {
			d.Parent = p.SpanID().String()
		}
		switch s.Status().Code {
		case codes.Error:
			d.Status, d.Message = "error", s.Status().Description
		case codes.Ok:
			d.Status = "ok"
		}
		for _, ev := range s.Events() {
			d.Events = append(d.Events, EventData{Name: ev.Name, AtUS: ev.Time.Sub(t0).Microseconds(), Attrs: attrMap(ev.Attributes)})
		}
		rt.Spans = append(rt.Spans, d)
	}
	return rt
}

func attrMap(kvs []attribute.KeyValue) map[string]any {
	if len(kvs) == 0 {
		return nil
	}
	m := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		m[string(kv.Key)] = kv.Value.AsInterface()
	}
	return m
}

// writeTrace writes a run to dir, atomically, and trims the directory.
func writeTrace(dir string, rt *RunTrace) error {
	data, err := json.Marshal(rt)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err != nil {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		// Runs are local to a checkout: keep them out of git.
		_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*\n"), 0o644)
	}
	final := filepath.Join(dir, rt.ID()+".json")
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	trim(dir, KeepTraces)
	return nil
}

// trim removes the oldest runs beyond keep.
func trim(dir string, keep int) {
	list, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		name string
		mod  time.Time
	}
	var files []file
	for _, e := range list {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if info, err := e.Info(); err == nil {
			files = append(files, file{e.Name(), info.ModTime()})
		}
	}
	if len(files) <= keep {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, f := range files[keep:] {
		_ = os.Remove(filepath.Join(dir, f.name))
	}
}
