package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Of tasks that fail together the lowest address is reported, and a task that only says it was stopped
// (the context, or the provider call that the stop cancelled) never takes the place of one that failed.
func TestFirstFailureIgnoresWhatWasOnlyStopped(t *testing.T) {
	real := errors.New("invalid value")
	stopped := []error{
		context.Canceled,
		fmt.Errorf("planning: %w", status.Error(grpccodes.Canceled, "context canceled")),
		errors.New("planning: rpc error: code = Canceled desc = context canceled"),
	}
	for _, s := range stopped {
		var f firstFailure
		f.note(s, "a")
		f.note(real, "z")
		if f.err != real {
			t.Errorf("the stopped task %v kept the place", s)
		}
		f = firstFailure{}
		f.note(real, "z")
		f.note(s, "a")
		if f.err != real {
			t.Errorf("a stopped task %v took the place", s)
		}
	}
	var f firstFailure
	f.note(errors.New("b"), "b")
	f.note(errors.New("a"), "a")
	if f.err.Error() != "a" {
		t.Errorf("not the lowest address: %v", f.err)
	}
}
