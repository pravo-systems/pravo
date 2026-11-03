package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// deadBackend is a backend whose process was killed: once it is dead every write
// fails, so nothing the dying apply tries to do afterwards reaches the state, its
// locks included.
type deadBackend struct {
	state.Backend
	dead atomic.Bool
}

var errKilled = errors.New("the process was killed")

func (b *deadBackend) Put(ctx context.Context, key string, data []byte) error {
	if b.dead.Load() {
		return errKilled
	}
	return b.Backend.Put(ctx, key, data)
}

func (b *deadBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	if b.dead.Load() {
		return false, errKilled
	}
	return b.Backend.PutIfAbsent(ctx, key, data)
}

func (b *deadBackend) DeleteIf(ctx context.Context, key string, match func([]byte) bool) (bool, error) {
	if b.dead.Load() {
		return false, errKilled
	}
	return b.Backend.DeleteIf(ctx, key, match)
}

func (b *deadBackend) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	if b.dead.Load() {
		return false, errKilled
	}
	return b.Backend.ReplaceIf(ctx, key, update)
}

func (b *deadBackend) Delete(ctx context.Context, key string) error {
	if b.dead.Load() {
		return errKilled
	}
	return b.Backend.Delete(ctx, key)
}

// An apply that is killed after the provider created the object and before the
// record was written leaves the object in the cloud, no record and a lock that
// goes stale. The next apply must not create the node again, with or without
// approval, and says how to go on; --allow-create names the node that is known
// not to exist.
func TestApplyDoesNotCreateAgainAfterAnInterruptedCreate(t *testing.T) {
	ctx := context.Background()
	var dead *deadBackend
	h := newHarnessOver(t, func(b state.Backend) state.Backend { dead = &deadBackend{Backend: b}; return dead })
	s := spec{cue: "a: " + thing("a", "content: \"x\"") + "\nb: " + thing("b", "content: a.content") + "\n", resources: []string{"a", "b"}}

	// The process dies when the provider has created a.
	h.fake.afterApply = func(name string) {
		if name == "a" {
			dead.dead.Store(true)
		}
	}
	e := h.engine(s, Options{})
	plan, err := e.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(ctx, plan); err == nil {
		t.Fatal("the apply of a killed process succeeded")
	}
	h.fake.afterApply = nil
	dead.dead.Store(false)
	h.reopen()
	if h.state.Get("a") != nil {
		t.Fatal("the record of a was written by a process that was dead")
	}
	creates := h.fake.called("ApplyResourceChange")
	if creates == 0 {
		t.Fatal("the provider created nothing")
	}
	// The lock the dead process held is what is left; nothing renewed it for an hour.
	if got := locksHeld(t, h.state); len(got) == 0 {
		t.Fatal("the killed apply left no lock")
	}
	// b was locked too, and never asked for: only the lock of a says that a create began.
	// The locks are made stale as time does it, by what the heartbeat last wrote.
	for _, addr := range locksHeld(t, h.state) {
		raw, err := h.state.Backend().Get(ctx, "locks/"+addr+".lock")
		if err != nil {
			t.Fatal(err)
		}
		var l state.Lock
		if err := json.Unmarshal(raw, &l); err != nil {
			t.Fatal(err)
		}
		if l.Creating != (addr == "a") {
			t.Fatalf("the lock of %s has creating=%v", addr, l.Creating)
		}
		old := time.Now().Add(-time.Hour).UTC()
		l.PID, l.Txn, l.Since, l.RenewedAt = 999999, "T1", old, old
		data, _ := json.Marshal(l)
		if err := h.state.Backend().Put(ctx, "locks/"+addr+".lock", data); err != nil {
			t.Fatal(err)
		}
	}

	var said []string
	opts := Options{Progress: func(f string, a ...any) { said = append(said, f) }}
	e2 := h.engine(s, opts)
	plan2, err := e2.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e2.Apply(ctx, plan2)
	var coded *CodedError
	if err == nil || !errors.As(err, &coded) || coded.Code != ExitRefused {
		t.Fatalf("apply after an interrupted create = %v, want a refusal with exit code %d", err, ExitRefused)
	}
	for _, want := range []string{"a: an apply (txn T1", "pravo import a", "pravo discover", "--allow-create a", "Nothing was changed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %v", want, err)
		}
	}
	if n := h.fake.called("ApplyResourceChange"); n != creates {
		t.Errorf("the refused apply reached the provider: %d calls, was %d", n, creates)
	}
	if h.state.Get("b") != nil {
		t.Error("the refused apply wrote the record of b")
	}

	// Saying that the create never reached the cloud lets the apply go on.
	e3 := h.engine(s, Options{AllowCreate: []string{"a"}})
	plan3, err := e3.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e3.Apply(ctx, plan3); err != nil {
		t.Fatalf("apply with --allow-create: %v", err)
	}
	if h.state.Get("a") == nil || h.state.Get("b") == nil {
		t.Error("a and b were not recorded")
	}
}
