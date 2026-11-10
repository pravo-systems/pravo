package engine

import (
	"context"
	"errors"
)

// firstFailure picks the error that a run reports when tasks fail side by side. They
// fail in whatever order they get there, so the error kept is the one of the task with
// the lowest address, the same on every run of one specification. A task that stopped
// only because another failed says just that, and does not take the place.
type firstFailure struct {
	err error
	at  string
}

func (f *firstFailure) note(err error, at string) {
	stopped := errors.Is(err, context.Canceled)
	if f.err == nil || (!stopped && (errors.Is(f.err, context.Canceled) || at < f.at)) {
		f.err, f.at = err, at
	}
}
