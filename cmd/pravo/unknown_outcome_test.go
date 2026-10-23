package main

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pravo-systems/pravo/internal/fakes3"
)

// A command whose write to the bucket was made, whose answer was lost, and whose
// check of the write failed, does not know whether the write was made. It stops
// with the generic failure and says so, and the command run again finishes.
func TestACommandThatEndsInAWriteOfUnknownOutcomeFailsAndRunsAgain(t *testing.T) {
	requireSealing(t)
	ids := isolateIdentities(t)
	e := newS3Env(t, false)
	st := e.store(t)
	e.record(t, st, "net.vpc", secretA)
	_, ada := person(t, ids, "ada")
	e.readers(t, ada)

	var lost atomic.Bool
	var unread atomic.Int64
	e.f.Lost = func(r *http.Request) (int, string) {
		if strings.Contains(r.URL.Path, "/rec/") && fakes3.Conditional(r) && lost.CompareAndSwap(false, true) {
			return http.StatusInternalServerError, "InternalError"
		}
		return 0, ""
	}
	e.f.Fault = func(r *http.Request) (int, string) {
		// The client sends a request up to eight times: the one read that checks the write.
		if lost.Load() && r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "" && unread.Add(1) <= 8 {
			return http.StatusInternalServerError, "InternalError"
		}
		return 0, ""
	}
	text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes")
	if code != 1 || !strings.Contains(text, "the store did not say whether the write was made") || !strings.Contains(text, "run the command again") {
		t.Fatalf("exit %d, want 1 and the advice to run the command again:\n%s", code, text)
	}
	if strings.Contains(text, "changed in the state since it was read") {
		t.Errorf("the write is reported as a change by another command:\n%s", text)
	}
	e.f.Lost, e.f.Fault = nil, nil
	if text, code := runPravo(t, e.dir, "protect", "init", "--encrypt", "--yes"); code != 0 {
		t.Fatalf("the command run again exited %d:\n%s", code, text)
	}
	if text, code := runPravo(t, e.dir, "protect", "status"); code != 0 || !strings.Contains(text, "format 3") {
		t.Fatalf("protect status exited %d:\n%s", code, text)
	}
}
