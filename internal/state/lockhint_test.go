package state

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// bucketLike is a local directory that says it is not on this machine, as a bucket is.
type bucketLike struct{ Backend }

func (bucketLike) Local() bool { return false }

func plantLock(t *testing.T, b Backend, l Lock) {
	t.Helper()
	data, _ := json.Marshal(l)
	if err := b.Put(context.Background(), lockKey(l.Addr), data); err != nil {
		t.Fatal(err)
	}
}

// With a bucket, the lock of a killed command on this host stays until StaleAfter, and the
// error that refuses a second command says that the process is gone and how to release it.
func TestLockOfADeadProcessOnABucketHintsAtUnlock(t *testing.T) {
	lb, err := NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := bucketLike{lb}
	s, err := OpenBackend(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	now := time.Now().UTC()
	plantLock(t, b, Lock{Addr: "a", PID: 999999, Host: host, Command: "pravo apply", Nonce: "dead", Since: now, RenewedAt: now, Kind: "lock"})
	plantLock(t, b, Lock{Addr: "b", PID: 999999, Host: "elsewhere", Command: "pravo apply", Nonce: "far", Since: now, RenewedAt: now, Kind: "lock"})
	plantLock(t, b, Lock{Addr: "c", PID: os.Getppid(), Host: host, Command: "pravo apply", Nonce: "up", Since: now, RenewedAt: now, Kind: "lock"})

	err = s.Lock("a", "pravo apply", "t")
	var locked *ErrLocked
	if !errors.As(err, &locked) {
		t.Fatalf("lock on a: %v, want ErrLocked (a bucket cannot reclaim at once)", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "not running on this host") || !strings.Contains(msg, "pravo state unlock a") {
		t.Errorf("no hint in %q", msg)
	}
	for _, addr := range []string{"b", "c"} {
		err := s.Lock(addr, "pravo apply", "t")
		if err == nil || strings.Contains(err.Error(), "not running on this host") {
			t.Errorf("lock on %s: %v, want a refusal with no hint", addr, err)
		}
	}
}

// "state unlock" takes a lock for live when its process runs on this host, or when it was
// renewed in the last three minutes; a reservation and a lock left behind are not live.
func TestLockLive(t *testing.T) {
	host, _ := os.Hostname()
	now := time.Now().UTC()
	old := now.Add(-time.Hour)
	for name, c := range map[string]struct {
		l    Lock
		want bool
	}{
		"alive on this host, long ago": {Lock{PID: os.Getppid(), Host: host, Since: old, RenewedAt: old, Kind: "lock"}, true},
		"dead on this host, just now":  {Lock{PID: 999999, Host: host, Since: now, RenewedAt: now, Kind: "lock"}, false},
		"other host, just renewed":     {Lock{PID: 1, Host: "elsewhere", Since: now, RenewedAt: now, Kind: "lock"}, true},
		"other host, two minutes":      {Lock{PID: 1, Host: "elsewhere", Since: old, RenewedAt: now.Add(-2 * time.Minute), Kind: "lock"}, true},
		"other host, four minutes":     {Lock{PID: 1, Host: "elsewhere", Since: old, RenewedAt: now.Add(-4 * time.Minute), Kind: "lock"}, false},
		"reservation":                  {Lock{PID: os.Getppid(), Host: host, Since: now, RenewedAt: now, Kind: "reservation"}, false},
	} {
		if got := c.l.Live(); got != c.want {
			t.Errorf("%s: Live = %v, want %v", name, got, c.want)
		}
	}
}
