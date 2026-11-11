package engine

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"

	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// firstFailure picks the error that a run reports when tasks fail side by side. They
// fail in whatever order they get there, so the error kept is the one of the task with
// the lowest address, the same on every run of one specification. A task that stopped
// only because another failed says just that, and does not take the place.
type firstFailure struct {
	err error
	at  string
	// stopped is set by the first failure of an apply: no other task is started.
	stopped atomic.Bool
}

func (f *firstFailure) note(err error, at string) {
	stopped := wasStopped(err)
	if f.err == nil || (!stopped && (wasStopped(f.err) || at < f.at)) {
		f.err, f.at = err, at
	}
}

// wasStopped reports whether err says only that the task was stopped: the run was cancelled, here or in
// the call that a provider was serving.
func wasStopped(err error) bool {
	return errors.Is(err, context.Canceled) || status.Code(err) == grpccodes.Canceled || strings.Contains(err.Error(), "code = Canceled")
}
