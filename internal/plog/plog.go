// Package plog is pravo's own debug log. It is off unless asked for with -v,
// -vv or PRAVO_LOG, and then writes to stderr. Every event is also attached to
// the active OpenTelemetry span when traces are on, whether or not it is
// printed, so a trace carries the same account of a run.
package plog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// LevelTrace is below slog.LevelDebug: the finest account of a run.
const LevelTrace = slog.Level(-8)

var (
	mu    sync.Mutex
	out   io.Writer = os.Stderr
	level           = slog.LevelWarn + 4 // nothing is printed
	start           = time.Now()
	log             = slog.New(handler{})
)

// Level converts a name such as "debug" or "trace" to a level; ok is false for
// an unknown name.
func Level(name string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "trace":
		return LevelTrace, true
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	}
	return 0, false
}

// Configure sets the printed level from the number of -v flags and from
// PRAVO_LOG, whichever is more verbose. It reports whether anything is printed.
func Configure(verbose int, env string) bool {
	l := slog.LevelWarn + 4
	switch {
	case verbose >= 2:
		l = LevelTrace
	case verbose == 1:
		l = slog.LevelDebug
	}
	if e, ok := Level(env); ok && e < l {
		l = e
	}
	mu.Lock()
	level = l
	mu.Unlock()
	return l < slog.LevelWarn
}

// SetOutput redirects the log, for tests.
func SetOutput(w io.Writer) {
	mu.Lock()
	out = w
	mu.Unlock()
}

// Enabled reports whether events at l are printed.
func Enabled(l slog.Level) bool {
	mu.Lock()
	defer mu.Unlock()
	return l >= level
}

// Debug logs what pravo decided or did, with key/value pairs.
func Debug(ctx context.Context, msg string, args ...any) { log.Log(ctx, slog.LevelDebug, msg, args...) }

// Trace logs the finest detail.
func Trace(ctx context.Context, msg string, args ...any) { log.Log(ctx, LevelTrace, msg, args...) }

type handler struct{ attrs []slog.Attr }

// Enabled is true for anything that is printed, and for debug events while a
// span records, so that traces carry them without -v.
func (handler) Enabled(ctx context.Context, l slog.Level) bool {
	if Enabled(l) {
		return true
	}
	return l >= slog.LevelDebug && trace.SpanFromContext(ctx).IsRecording()
}

func (h handler) Handle(ctx context.Context, r slog.Record) error {
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		var kv []attribute.KeyValue
		r.Attrs(func(a slog.Attr) bool {
			kv = append(kv, attribute.String(a.Key, a.Value.String()))
			return true
		})
		span.AddEvent(r.Message, trace.WithAttributes(kv...))
	}
	if !Enabled(r.Level) {
		return nil
	}
	var b strings.Builder
	name := "debug"
	if r.Level < slog.LevelDebug {
		name = "trace"
	} else if r.Level >= slog.LevelInfo {
		name = "info"
	}
	fmt.Fprintf(&b, "+%.3fs %-5s %s", time.Since(start).Seconds(), name, r.Message)
	r.Attrs(func(a slog.Attr) bool {
		v := a.Value.String()
		if strings.ContainsAny(v, " \t\"") {
			v = fmt.Sprintf("%q", v)
		}
		fmt.Fprintf(&b, " %s=%s", a.Key, v)
		return true
	})
	b.WriteByte('\n')
	mu.Lock()
	defer mu.Unlock()
	_, err := io.WriteString(out, b.String())
	return err
}

func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler { return handler{append(h.attrs, attrs...)} }
func (h handler) WithGroup(string) slog.Handler            { return h }
