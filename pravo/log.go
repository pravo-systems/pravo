package pravo

import (
	"log/slog"

	"github.com/pravo-systems/pravo/internal/plog"
)

// SetLogHandler sends pravo's debug log to a handler of the host program instead
// of printing it to standard error. The setting is process-wide, as the logger of
// a process is; nil restores the default, which prints only what -v asks for in
// the command line and nothing in a library.
//
// The same events are attached to the active OpenTelemetry span whether or not
// they are logged, and the spans come from the global tracer provider, which the
// host sets with otel.SetTracerProvider.
func SetLogHandler(h slog.Handler) { plog.SetSink(h) }
