package engine

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// lateBackend delivers one request late: the first call of op on a key with the
// given prefix reaches the store after delay. It stands for a computer that is
// suspended while the request is on its way, or a connection that hangs and is
// sent again.
type lateBackend struct {
	state.Backend
	op, prefix string
	delay      time.Duration
	done       atomic.Bool
}

func (b *lateBackend) wait(op, key string) {
	if op == b.op && strings.HasPrefix(key, b.prefix) && b.done.CompareAndSwap(false, true) {
		time.Sleep(b.delay)
	}
}

func (b *lateBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	b.wait("PutIfAbsent", key)
	return b.Backend.PutIfAbsent(ctx, key, data)
}

// ReplaceIf reads the object, as a store with conditional writes does, and the
// write is what arrives late.
func (b *lateBackend) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	if b.op != "ReplaceIf" || !strings.HasPrefix(key, b.prefix) || b.done.Load() {
		return b.Backend.ReplaceIf(ctx, key, update)
	}
	read, err := b.Get(ctx, key)
	if err != nil {
		return false, err
	}
	next, ok := update(read)
	if !ok {
		return false, nil
	}
	b.wait("ReplaceIf", key)
	return b.Backend.ReplaceIf(ctx, key, func(current []byte) ([]byte, bool) {
		if string(current) != string(read) {
			return nil, false
		}
		return next, true
	})
}

// The lock of an apply is stamped before the request that writes it is sent. A
// request that arrives 11 minutes late writes a lock that every other command
// reads as stale, so the apply counts the time it may trust its locks from the
// moment before the request and not from the answer: its fence renews, and finds
// that another apply took the lock over. Found by the exploration of the
// interleavings of the lock protocol.
func TestALockThatLandsLateIsNotTrusted(t *testing.T) {
	for _, op := range []string{"PutIfAbsent", "ReplaceIf"} {
		t.Run(op, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mem := state.NewMemBackend("mem")
				ctx := context.Background()
				sa, err := state.OpenBackend(ctx, &lateBackend{Backend: mem, op: op, prefix: "locks/", delay: 11 * time.Minute})
				if err != nil {
					t.Fatal(err)
				}
				sb, err := state.OpenBackend(ctx, mem)
				if err != nil {
					t.Fatal(err)
				}
				quiet := func(string, ...any) {}
				addrs := []string{"web.server"}

				// Apply A takes its lock. With PutIfAbsent the create arrives 11 minutes
				// late; with ReplaceIf the first renewal of the heartbeat does.
				a := newHeldLocks(sa, quiet, state.HeartbeatInterval)
				defer a.release()
				if err := a.acquire(addrs, "apply", "txn-a"); err != nil {
					t.Fatal(err)
				}
				if op == "ReplaceIf" {
					time.Sleep(state.HeartbeatInterval + 11*time.Minute + time.Second) // the renewal that is late has returned
				}
				time.Sleep(30 * time.Second)
				trusted := time.Since(a.lastBeat())

				// Apply B starts: it finds a lock that is stamped more than 10 minutes ago.
				b := newHeldLocks(sb, quiet, state.HeartbeatInterval)
				defer b.release()
				errB := b.acquire(addrs, "apply", "txn-b")

				// A is about to send a change to its provider.
				errA := a.fence()
				if errA == nil && errB == nil {
					t.Fatalf("two applies hold the lock on %s at once: B took the lock of A over as stale, and the fence of A passed without a renewal (A last confirmed its locks %s ago, a renewal is due after %s)",
						addrs[0], trusted.Round(time.Second), state.StaleAfter/2)
				}
				t.Logf("A: %v, B: %v", errA, errB)
			})
		})
	}
}

// A computer that sleeps stops the clock that Go measures intervals with, and
// the time of day goes on: the other commands judge the lock by the time of day.
// An apply that wakes renews its locks before its next change, and finds out
// whether another command took one meanwhile.
func TestAnApplyThatSleptRenewsBeforeItChanges(t *testing.T) {
	for _, taken := range []bool{false, true} {
		name := "lock kept"
		if taken {
			name = "lock taken"
		}
		t.Run(name, func(t *testing.T) {
			mem := state.NewMemBackend("mem")
			ctx := context.Background()
			st, err := state.OpenBackend(ctx, mem)
			if err != nil {
				t.Fatal(err)
			}
			h := newHeldLocks(st, func(string, ...any) {}, state.HeartbeatInterval)
			defer h.release()
			if err := h.acquire([]string{"web.server"}, "apply", "txn-a"); err != nil {
				t.Fatal(err)
			}
			if err := h.fence(); err != nil {
				t.Fatal(err)
			}
			before := h.lastBeat()
			if taken {
				keys, _ := mem.List(ctx, "locks/")
				if len(keys) != 1 {
					t.Fatalf("locks: %v", keys)
				}
				data, _ := mem.Get(ctx, keys[0])
				var l state.Lock
				if err := json.Unmarshal(data, &l); err != nil {
					t.Fatal(err)
				}
				l.Nonce, l.Txn = "another", "txn-b"
				data, _ = json.Marshal(l)
				if err := mem.Put(ctx, keys[0], data); err != nil {
					t.Fatal(err)
				}
			}
			// The computer slept for an hour: the time of day says so once, and the
			// clock of intervals says that nothing happened.
			old := wallClock
			defer func() { wallClock = old }()
			slept := true
			wallClock = func() time.Time {
				if slept {
					slept = false
					return old().Add(time.Hour)
				}
				return old()
			}
			err = h.fence()
			switch {
			case taken && (err == nil || !strings.Contains(err.Error(), "lock lost")):
				t.Fatalf("the fence of an apply that slept while its lock was taken: %v", err)
			case !taken && err != nil:
				t.Fatal(err)
			case !taken && !h.lastBeat().After(before):
				t.Fatal("the fence of an apply that slept passed without a renewal")
			}
		})
	}
}

// An apply that is turned away names the command that turned it away, and not a
// third one that holds a smaller node for a moment while it takes its own locks:
// that one may be turned away itself. Found by the exploration of the
// interleavings of the lock protocol, with three applies of a,b / b,c / a,c.
func TestARejectionNamesTheCommandThatCausedIt(t *testing.T) {
	lock := func(addr, nonce, kind string) *state.ErrLocked {
		return &state.ErrLocked{Lock: state.Lock{Addr: addr, Nonce: nonce, Kind: kind}}
	}
	firm := func(l state.Lock) bool { return l.Kind == "reservation" || l.Nonce == "applies" }
	cause := lock("b", "applies", "lock")
	cases := []struct {
		name    string
		holders []*state.ErrLocked
		want    string
		others  int
	}{
		{"a third command holds a smaller node", []*state.ErrLocked{lock("a", "passing", "lock"), lock("b", "applies", "lock")}, "b", 1},
		{"the cause holds a smaller node as well", []*state.ErrLocked{lock("a", "applies", "lock"), lock("b", "applies", "lock")}, "a", 1},
		{"the cause is gone and a reservation is held", []*state.ErrLocked{lock("a", "passing", "lock"), lock("c", "bob", "reservation")}, "c", 1},
		{"the cause is gone and a third command passes", []*state.ErrLocked{lock("a", "passing", "lock")}, "b", 1},
		{"nothing is held any longer", nil, "b", 0},
	}
	for _, c := range cases {
		got, others := nameHolder(c.holders, cause, firm)
		if got.Lock.Addr != c.want || others != c.others {
			t.Errorf("%s: the error names %s and counts %d others; want %s and %d", c.name, got.Lock.Addr, others, c.want, c.others)
		}
	}
}
