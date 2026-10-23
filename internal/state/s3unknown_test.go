package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/fakes3"
)

// A conditional write whose answer was lost is sent again by the client and
// refused, because the first attempt made it. The client then reads the object to
// see whether it holds the bytes it sent. When that read fails the client does not
// know, and says so with ErrUnknownOutcome; it does not report a refusal, which
// callers take for another writer's object.

// lostThenUnreadable makes the store apply the first conditional write to a key
// that match accepts and answer 500, and then fail the reads of the next call of
// the client: one read, which it sends as many times as it sends any request.
// It returns a counter of the reads that were failed.
func lostThenUnreadable(f *fakes3.Server, match func(r *http.Request) bool) *atomic.Int64 {
	var lost atomic.Bool
	var failed atomic.Int64
	f.Lost = func(r *http.Request) (int, string) {
		if match(r) && fakes3.Conditional(r) && lost.CompareAndSwap(false, true) {
			return http.StatusInternalServerError, "InternalError"
		}
		return 0, ""
	}
	f.Fault = func(r *http.Request) (int, string) {
		if lost.Load() && r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "" && failed.Add(1) <= s3Attempts {
			return http.StatusInternalServerError, "InternalError"
		}
		return 0, ""
	}
	return &failed
}

// refusedThenUnreadable is lostThenUnreadable for a first attempt that the store
// did not apply: it fails with 500, and the write that is sent again is refused
// because another command's object is there.
func refusedThenUnreadable(f *fakes3.Server, match func(r *http.Request) bool) {
	var armed atomic.Bool
	var failed atomic.Int64
	var first atomic.Bool
	f.Fault = func(r *http.Request) (int, string) {
		switch {
		case match(r) && fakes3.Conditional(r) && first.CompareAndSwap(false, true):
			armed.Store(true)
			return http.StatusInternalServerError, "InternalError"
		case armed.Load() && r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "" && failed.Add(1) <= s3Attempts:
			return http.StatusInternalServerError, "InternalError"
		}
		return 0, ""
	}
}

func onKey(key string) func(r *http.Request) bool {
	return func(r *http.Request) bool { return strings.HasSuffix(r.URL.Path, "/"+key) }
}

func TestS3WriteThatCannotBeCheckedIsOfUnknownOutcome(t *testing.T) {
	quickRetries(t)
	ctx := context.Background()
	everything := func(*http.Request) bool { return true }

	t.Run("create", func(t *testing.T) {
		f, b := newFakeS3(t)
		failed := lostThenUnreadable(f, everything)
		ok, err := b.PutIfAbsent(ctx, "resources/a.json", []byte("mine"))
		if ok || !errors.Is(err, ErrUnknownOutcome) {
			t.Fatalf("PutIfAbsent = %v, %v, want ErrUnknownOutcome", ok, err)
		}
		if errors.Is(err, ErrNotFound) {
			t.Errorf("%v is also ErrNotFound", err)
		}
		if failed.Load() < s3Attempts {
			t.Errorf("%d reads failed: the verification read was not made", failed.Load())
		}
		if string(f.Objects["team/app/resources/a.json"]) != "mine" {
			t.Errorf("the object holds %q: the write was made", f.Objects["team/app/resources/a.json"])
		}
		// The next call is told what the object is.
		if ok, err := b.PutIfAbsent(ctx, "resources/a.json", []byte("mine")); ok || err != nil {
			t.Errorf("PutIfAbsent without a retry = %v, %v, want a refusal", ok, err)
		}
	})
	t.Run("replace", func(t *testing.T) {
		f, b := newFakeS3(t)
		if err := b.Put(ctx, "k", []byte("one")); err != nil {
			t.Fatal(err)
		}
		lostThenUnreadable(f, everything)
		ok, err := b.ReplaceIf(ctx, "k", func([]byte) ([]byte, bool) { return []byte("two"), true })
		if ok || !errors.Is(err, ErrUnknownOutcome) {
			t.Fatalf("ReplaceIf = %v, %v, want ErrUnknownOutcome", ok, err)
		}
		if string(f.Objects["team/app/k"]) != "two" {
			t.Errorf("the object holds %q: the write was made", f.Objects["team/app/k"])
		}
	})
	t.Run("a refusal of another writer's object", func(t *testing.T) {
		f, b := newFakeS3(t)
		if err := b.Put(ctx, "k", []byte("theirs")); err != nil {
			t.Fatal(err)
		}
		refusedThenUnreadable(f, everything)
		ok, err := b.PutIfAbsent(ctx, "k", []byte("mine"))
		if ok || !errors.Is(err, ErrUnknownOutcome) {
			t.Fatalf("PutIfAbsent = %v, %v, want ErrUnknownOutcome: the write may have been made as far as the client knows", ok, err)
		}
		if string(f.Objects["team/app/k"]) != "theirs" {
			t.Errorf("the object holds %q", f.Objects["team/app/k"])
		}
	})
	t.Run("a read that finds no object is a refusal", func(t *testing.T) {
		f, b := newFakeS3(t)
		var lost atomic.Bool
		f.Lost = func(r *http.Request) (int, string) {
			if fakes3.Conditional(r) && lost.CompareAndSwap(false, true) {
				return http.StatusInternalServerError, "InternalError"
			}
			return 0, ""
		}
		f.Fault = func(r *http.Request) (int, string) {
			if lost.Load() && r.Method == http.MethodGet {
				return http.StatusNotFound, "NoSuchKey"
			}
			return 0, ""
		}
		if ok, err := b.PutIfAbsent(ctx, "k", []byte("mine")); ok || err != nil {
			t.Errorf("PutIfAbsent = %v, %v, want a clean refusal: the object is not there", ok, err)
		}
	})
	t.Run("a write that was not sent twice is a refusal", func(t *testing.T) {
		f, b := newFakeS3(t)
		if err := b.Put(ctx, "k", []byte("theirs")); err != nil {
			t.Fatal(err)
		}
		f.Fault = func(r *http.Request) (int, string) {
			if r.Method == http.MethodGet {
				return http.StatusInternalServerError, "InternalError"
			}
			return 0, ""
		}
		if ok, err := b.PutIfAbsent(ctx, "k", []byte("mine")); ok || err != nil {
			t.Errorf("PutIfAbsent = %v, %v, want a clean refusal: nothing was sent twice, so nothing is checked", ok, err)
		}
	})
}

// A delete whose answer was lost is sent again and finds nothing. It is
// ErrNotFound, which every caller takes as gone, and not an unknown outcome.
func TestS3DeleteWhoseAnswerWasLostIsGone(t *testing.T) {
	quickRetries(t)
	ctx := context.Background()
	f, b := newFakeS3(t)
	if err := b.Put(ctx, "locks/a.lock", []byte("mine")); err != nil {
		t.Fatal(err)
	}
	var lost atomic.Bool
	f.Lost = func(r *http.Request) (int, string) {
		if r.Method == http.MethodDelete && lost.CompareAndSwap(false, true) {
			return http.StatusInternalServerError, "InternalError"
		}
		return 0, ""
	}
	ok, err := b.DeleteIf(ctx, "locks/a.lock", func([]byte) bool { return true })
	if ok || !errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnknownOutcome) {
		t.Fatalf("DeleteIf = %v, %v, want ErrNotFound", ok, err)
	}
	if _, there := f.Objects["team/app/locks/a.lock"]; there {
		t.Error("the object is still there")
	}
}

// openS3 opens a store of format 2 over a bucket with the hooks of the fake.
func openS3(t *testing.T) (*fakes3Handle, *Store) {
	t.Helper()
	f, b := newFakeS3(t)
	s, err := OpenBackendWith(context.Background(), b, Options{NewFormat: FormatImmutable})
	if err != nil {
		t.Fatal(err)
	}
	return &fakes3Handle{f: f, b: b}, s
}

type fakes3Handle struct {
	f *fakes3.Server
	b *S3Backend
}

// The locks recover from a write of unknown outcome as they do from a refusal: the
// lock is not taken until it has been read, and then it is this call's if it holds
// what this call wrote.
func TestLocksRecoverFromAWriteOfUnknownOutcome(t *testing.T) {
	quickRetries(t)

	t.Run("lock that was made", func(t *testing.T) {
		h, s := openS3(t)
		lostThenUnreadable(h.f, onKey(lockKey("t.a")))
		release, ok, err := s.TryHold("t.a", "test", "")
		if err != nil || !ok {
			t.Fatalf("TryHold = %v, %v, want it held", ok, err)
		}
		if _, there := h.f.Objects["team/app/"+lockKey("t.a")]; !there {
			t.Fatal("the store holds no lock")
		}
		// It was created by this call, so releasing it deletes it.
		release()
		if _, there := h.f.Objects["team/app/"+lockKey("t.a")]; there {
			t.Error("the lock was left behind: the hold did not know it had created it")
		}
	})
	t.Run("lock that another holds", func(t *testing.T) {
		h, s := openS3(t)
		other, err := OpenBackend(context.Background(), h.b)
		if err != nil {
			t.Fatal(err)
		}
		if err := other.Lock("t.a", "other", ""); err != nil {
			t.Fatal(err)
		}
		refusedThenUnreadable(h.f, onKey(lockKey("t.a")))
		var locked *ErrLocked
		if err := s.Lock("t.a", "test", ""); !errors.As(err, &locked) || locked.Lock.Command != "other" {
			t.Fatalf("Lock = %v, want the lock of the other command", err)
		}
	})
	t.Run("lock of the outputs", func(t *testing.T) {
		h, s := openS3(t)
		lostThenUnreadable(h.f, onKey(outputsLockKey))
		unlock, err := s.lockOutputs()
		if err != nil {
			t.Fatalf("lockOutputs = %v", err)
		}
		if _, there := h.f.Objects["team/app/"+outputsLockKey]; !there {
			t.Fatal("the store holds no lock")
		}
		unlock()
		if _, there := h.f.Objects["team/app/"+outputsLockKey]; there {
			t.Error("the lock was left behind")
		}
	})
	t.Run("reservation that was made", func(t *testing.T) {
		h, s := openS3(t)
		lostThenUnreadable(h.f, onKey(lockKey("t.a")))
		until := time.Now().Add(time.Hour)
		ends, err := s.ReserveAll([]string{"t.a"}, "plan --reserve", until)
		if err != nil {
			t.Fatalf("ReserveAll = %v", err)
		}
		if !ends["t.a"].Equal(until) {
			t.Errorf("the reservation ends at %v, want %v", ends["t.a"], until)
		}
		if ok, err := s.Release("t.a", false); err != nil || !ok {
			t.Errorf("Release = %v, %v, want the reservation released", ok, err)
		}
	})
	t.Run("reservation that was replaced", func(t *testing.T) {
		h, s := openS3(t)
		first := time.Now().Add(time.Hour)
		if _, err := s.ReserveAll([]string{"t.a"}, "plan --reserve", first); err != nil {
			t.Fatal(err)
		}
		// The create is refused, because the reservation is there, and the replace is
		// made and its answer lost.
		var lost atomic.Bool
		var failed atomic.Int64
		h.f.Lost = func(r *http.Request) (int, string) {
			if onKey(lockKey("t.a"))(r) && r.Method == http.MethodPut && r.Header.Get("If-Match") != "" && lost.CompareAndSwap(false, true) {
				return http.StatusInternalServerError, "InternalError"
			}
			return 0, ""
		}
		h.f.Fault = func(r *http.Request) (int, string) {
			if lost.Load() && r.Method == http.MethodGet && failed.Add(1) <= s3Attempts {
				return http.StatusInternalServerError, "InternalError"
			}
			return 0, ""
		}
		later := first.Add(time.Hour)
		ends, err := s.ReserveAll([]string{"t.a"}, "plan --reserve", later)
		if err != nil {
			t.Fatalf("ReserveAll = %v", err)
		}
		if !ends["t.a"].Equal(later) {
			t.Errorf("the reservation ends at %v, want %v", ends["t.a"], later)
		}
		l, err := s.readLock("t.a")
		if err != nil || l.Expires == nil || !l.Expires.Equal(later) {
			t.Errorf("the reservation in the store = %+v, %v, want it to end at %v", l, err, later)
		}
	})
}

// A heartbeat that cannot tell whether the lock was renewed is not a lost lock.
func TestRenewOfUnknownOutcomeIsNotALostLock(t *testing.T) {
	quickRetries(t)
	h, s := openS3(t)
	if err := s.Lock("t.a", "test", ""); err != nil {
		t.Fatal(err)
	}
	// The clock of Windows ticks every few milliseconds, and a renewal in the tick
	// of the lock writes the bytes that are already there, which is a write that
	// is made whether its answer is lost or not.
	time.Sleep(50 * time.Millisecond)
	lostThenUnreadable(h.f, onKey(lockKey("t.a")))
	err := s.Renew("t.a")
	var locked *ErrLocked
	if !errors.Is(err, ErrUnknownOutcome) || errors.As(err, &locked) || errors.Is(err, ErrNotFound) {
		t.Fatalf("Renew = %v, want ErrUnknownOutcome and no lost lock", err)
	}
	if err := s.Renew("t.a"); err != nil {
		t.Errorf("the next Renew = %v", err)
	}
}

// A node that was written, whose answer was lost, is not reported as changed by
// another command: it was not, and the command is told to run again.
func TestNodeWriteOfUnknownOutcomeIsNotAChange(t *testing.T) {
	quickRetries(t)
	ctx := context.Background()
	node := func(attrs string) *Resource {
		return &Resource{Mode: "managed", Type: "random_pet", Name: "a", Attributes: json.RawMessage(attrs)}
	}
	check := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, ErrUnknownOutcome) || errors.Is(err, ErrChanged) {
			t.Fatalf("Set = %v, want ErrUnknownOutcome and not ErrChanged", err)
		}
		if !strings.Contains(err.Error(), "run the command again") {
			t.Errorf("Set = %q: it does not tell the user to run the command again", err)
		}
	}
	t.Run("create", func(t *testing.T) {
		h, s := openS3(t)
		lostThenUnreadable(h.f, onKey(nodeKey("random_pet.a")))
		check(t, s.Set(node(`{"id":"x"}`), ""))
		// The pointer is there: the write was made.
		again, err := OpenBackend(ctx, h.b)
		if err != nil {
			t.Fatal(err)
		}
		if again.Get("random_pet.a") == nil {
			t.Error("the node is not in the store")
		}
	})
	t.Run("record", func(t *testing.T) {
		h, s := openS3(t)
		lostThenUnreadable(h.f, func(r *http.Request) bool { return strings.Contains(r.URL.Path, "/rec/") })
		check(t, s.Set(node(`{"id":"x"}`), ""))
	})
	t.Run("replace", func(t *testing.T) {
		h, s := openS3(t)
		if err := s.Set(node(`{"id":"x"}`), ""); err != nil {
			t.Fatal(err)
		}
		lostThenUnreadable(h.f, onKey(nodeKey("random_pet.a")))
		next := s.Get("random_pet.a")
		next.Attributes = json.RawMessage(`{"id":"y"}`)
		check(t, s.Set(next, ""))
		again, err := OpenBackend(ctx, h.b)
		if err != nil {
			t.Fatal(err)
		}
		if got := again.Get("random_pet.a"); got == nil || !strings.Contains(string(got.Attributes), `"y"`) {
			t.Errorf("the store holds %+v: the write was made", got)
		}
	})
}

// The callers of DeleteIf take the ErrNotFound of a delete that was sent again as
// gone, which it is: a lock released, a node removed.
func TestCallersOfADeleteWhoseAnswerWasLost(t *testing.T) {
	quickRetries(t)
	loseDeletes := func(h *fakes3Handle, key string) {
		var lost atomic.Bool
		h.f.Lost = func(r *http.Request) (int, string) {
			if r.Method == http.MethodDelete && onKey(key)(r) && lost.CompareAndSwap(false, true) {
				return http.StatusInternalServerError, "InternalError"
			}
			return 0, ""
		}
	}
	t.Run("unlock", func(t *testing.T) {
		h, s := openS3(t)
		if err := s.Lock("t.a", "test", ""); err != nil {
			t.Fatal(err)
		}
		loseDeletes(h, lockKey("t.a"))
		if err := s.Unlock("t.a"); err != nil {
			t.Errorf("Unlock = %v", err)
		}
		if _, there := h.f.Objects["team/app/"+lockKey("t.a")]; there {
			t.Error("the lock is still there")
		}
	})
	t.Run("release of a reservation", func(t *testing.T) {
		h, s := openS3(t)
		if _, err := s.ReserveAll([]string{"t.a"}, "plan --reserve", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		loseDeletes(h, lockKey("t.a"))
		if _, err := s.Release("t.a", false); err != nil {
			t.Errorf("Release = %v", err)
		}
		if _, there := h.f.Objects["team/app/"+lockKey("t.a")]; there {
			t.Error("the reservation is still there")
		}
	})
	t.Run("removal of a node", func(t *testing.T) {
		h, s := openS3(t)
		if err := s.Set(&Resource{Mode: "managed", Type: "random_pet", Name: "a", Attributes: json.RawMessage(`{}`)}, ""); err != nil {
			t.Fatal(err)
		}
		loseDeletes(h, nodeKey("random_pet.a"))
		if ok, err := s.Remove("random_pet.a"); err != nil || !ok {
			t.Errorf("Remove = %v, %v", ok, err)
		}
		if _, there := h.f.Objects["team/app/"+nodeKey("random_pet.a")]; there {
			t.Error("the pointer is still there")
		}
	})
}

// The write of meta.json in an upgrade returns the error of a write of unknown
// outcome with the advice to run the command again.
func TestUpgradeWhoseMetaWriteIsOfUnknownOutcomeIsAdvised(t *testing.T) {
	skipWhenSealed(t) // a store that is sealed already is not upgraded
	s, g := openGated(t)
	g.set(func(g *gateBackend) {
		g.putGate = func(k string) error {
			if k == "meta.json" {
				return fmt.Errorf("s3 conditional put %s: %w", k, ErrUnknownOutcome)
			}
			return nil
		}
	})
	_, err := s.Upgrade(context.Background(), UpgradeOptions{To: FormatImmutable})
	if !errors.Is(err, ErrUnknownOutcome) || !strings.Contains(err.Error(), "run the command again") {
		t.Fatalf("Upgrade = %v, want ErrUnknownOutcome and the advice to run the command again", err)
	}
}
