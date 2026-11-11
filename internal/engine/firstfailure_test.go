package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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

// A plan whose tasks all fail reports the one with the lowest address, however long each takes: the
// tasks that already started are not stopped by the first failure, so the one that gets there first is
// not the one that is reported.
func TestPlanReportsTheLowestAddressOfTasksThatFailTogether(t *testing.T) {
	for range 5 {
		h := newHarness(t)
		h.fake.planFails = map[string]time.Duration{"a": 60 * time.Millisecond, "b": 30 * time.Millisecond, "c": 0}
		s := spec{
			cue:       "a: " + thing("a", "content: \"a\"") + "\nb: " + thing("b", "content: \"b\"") + "\nc: " + thing("c", "content: \"c\"") + "\n",
			resources: []string{"a", "b", "c"},
		}
		_, err := h.engine(s, Options{}).Plan(context.Background())
		if err == nil || !strings.Contains(err.Error(), "a is invalid") {
			t.Fatalf("the error is %v, want the one of a", err)
		}
	}
}

// A task that fails and waits for a task that succeeds is still started when another task, of a higher
// address, has failed before: a plan reports the lowest address of everything that could fail.
func TestPlanStartsWhatWaitsForAValidTaskAfterAnotherFailed(t *testing.T) {
	for range 20 {
		h := newHarness(t)
		h.fake.planFails = map[string]time.Duration{"alpha": 0, "zeta": 0}
		s := spec{
			cue:       "m: " + thing("m", "content: \"m\"") + "\nalpha: " + thing("alpha", "content: \"after \\(m.id)\"") + "\nzeta: " + thing("zeta", "content: \"z\"") + "\n",
			resources: []string{"m", "alpha", "zeta"},
		}
		_, err := h.engine(s, Options{}).Plan(context.Background())
		if err == nil || !strings.Contains(err.Error(), "alpha is invalid") {
			t.Fatalf("the error is %v, want the one of alpha", err)
		}
	}
}
