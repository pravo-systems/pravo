package state

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/fakes3"
)

// A write of unknown outcome that was made, followed by an outage that also fails
// the read of the lock, leaves a round whose own create is refused because the
// object is the one an earlier round wrote. The bytes are the call's, and the call
// must know that it took the lock (or the reservation) and not leave it behind.

// lostThenUnreadableN is lostThenUnreadable for an outage that outlasts the
// verification read: n reads fail after the lost answer.
func lostThenUnreadableN(f *fakes3.Server, match func(r *http.Request) bool, n int64) {
	var lost atomic.Bool
	var failed atomic.Int64
	f.Lost = func(r *http.Request) (int, string) {
		if match(r) && fakes3.Conditional(r) && lost.CompareAndSwap(false, true) {
			return http.StatusInternalServerError, "InternalError"
		}
		return 0, ""
	}
	f.Fault = func(r *http.Request) (int, string) {
		if lost.Load() && r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "" && failed.Add(1) <= n {
			return http.StatusInternalServerError, "InternalError"
		}
		return 0, ""
	}
}

// The create landed, its answer was lost, the verification read failed and so did
// the read of the lock that follows (an outage, not one bad request): the next
// round sees its own lock and must still know that this call created it.
func TestLockOfUnknownOutcomeAndAnOutageIsReleased(t *testing.T) {
	quickRetries(t)
	h, s := openS3(t)
	// 8 reads fail in the verification, 5 reads of 8 in readHolder.
	lostThenUnreadableN(h.f, onKey(lockKey("t.a")), int64(s3Attempts+5*s3Attempts))
	release, ok, err := s.TryHold("t.a", "test", "")
	if err != nil || !ok {
		t.Fatalf("TryHold = %v, %v", ok, err)
	}
	if _, there := h.f.Objects["team/app/"+lockKey("t.a")]; !there {
		t.Fatal("no lock in the store")
	}
	release()
	if _, there := h.f.Objects["team/app/"+lockKey("t.a")]; there {
		t.Error("LEAK: the lock of this call is still in the store after release")
	}
}

// The same for the lock of the outputs: after an unknown outcome and a failed read
// the call waits for its own lock to go stale.
func TestOutputsLockOfUnknownOutcomeAndAnOutageDoesNotWaitForItself(t *testing.T) {
	quickRetries(t)
	oldStale, oldWait := outputsLockStale, outputsLockWait
	outputsLockStale, outputsLockWait = 2*time.Second, 3*time.Second
	t.Cleanup(func() { outputsLockStale, outputsLockWait = oldStale, oldWait })
	h, s := openS3(t)
	lostThenUnreadableN(h.f, onKey(outputsLockKey), int64(2*s3Attempts))
	begun := time.Now()
	unlock, err := s.lockOutputs()
	took := time.Since(begun)
	if err != nil {
		t.Fatalf("lockOutputs = %v after %v", err, took)
	}
	unlock()
	if took > time.Second {
		t.Errorf("lockOutputs took %v: it waited for its own lock to go stale (%v)", took, outputsLockStale)
	}
	if _, there := h.f.Objects["team/app/"+outputsLockKey]; there {
		t.Error("the lock was left behind")
	}
}

// A reservation whose create landed and whose answer and reads were lost is read
// again on the next round as this owner's own reservation: replaced, and reported
// as a reservation that existed before. ReserveAll then restores that on a later
// failure, which leaves a reservation behind that nobody had before the call.
func TestReservationOfUnknownOutcomeAndAnOutageIsRolledBack(t *testing.T) {
	quickRetries(t)
	h, s := openS3(t)
	other, err := OpenBackend(s.ctx, h.b)
	if err != nil {
		t.Fatal(err)
	}
	// t.b is locked by another command, so the reservation of t.a is rolled back.
	if err := other.Lock("t.b", "other", ""); err != nil {
		t.Fatal(err)
	}
	lostThenUnreadableN(h.f, onKey(lockKey("t.a")), int64(s3Attempts+5*s3Attempts))
	_, err = s.ReserveAll([]string{"t.a", "t.b"}, "plan --reserve", time.Now().Add(time.Hour))
	if err == nil {
		t.Fatal("ReserveAll succeeded although t.b is locked")
	}
	for k := range h.f.Objects {
		if strings.HasSuffix(k, lockKey("t.a")) {
			t.Errorf("LEAK: %s is still in the store after the rollback: %s", k, h.f.Objects[k])
		}
	}
}
