package state

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/fakehttp"
)

// storeClock is the clock of a store in a test: it moves only when the test moves it. It
// starts at a whole second of today, since the headers of a store carry whole seconds.
type storeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newStoreClock() *storeClock { return &storeClock{now: time.Now().UTC().Truncate(time.Second)} }

func (c *storeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *storeClock) pass(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// timedBackends returns a bucket, an http server and a backend in memory, each of which
// tells its own time with an object, and the clock that each tells it by.
func timedBackends() []struct {
	name string
	make func(t *testing.T) (Backend, *storeClock)
} {
	return []struct {
		name string
		make func(t *testing.T) (Backend, *storeClock)
	}{
		{"s3", func(t *testing.T) (Backend, *storeClock) {
			f, b := newFakeS3(t)
			c := newStoreClock()
			f.Clock = c.Now
			return b, c
		}},
		{"http", func(t *testing.T) (Backend, *storeClock) {
			f, b := newFakeHTTP(t)
			c := newStoreClock()
			f.Clock = c.Now
			return b, c
		}},
		{"memory", func(t *testing.T) (Backend, *storeClock) {
			b := NewMemBackend("memory")
			c := newStoreClock()
			b.Clock = c.Now
			return b, c
		}},
	}
}

// machineClock makes the clock of this process, by which the locks are stamped, differ from
// the true time by off until the test ends or the function it returns is called.
func machineClock(t *testing.T, off time.Duration) func() {
	t.Helper()
	before := lockClock
	lockClock = func() time.Time { return time.Now().Add(off) }
	restore := func() { lockClock = before }
	t.Cleanup(restore)
	return restore
}

func mustLock(t *testing.T, s *Store, addr string) {
	t.Helper()
	if err := s.Lock(addr, "pravo apply", "t1"); err != nil {
		t.Fatalf("lock %s: %v", addr, err)
	}
}

func refused(err error) bool {
	var locked *ErrLocked
	return errors.As(err, &locked)
}

// A lock that is held is not taken over by a command whose clock is far ahead, and the lock
// of a command whose clock is far behind is not stale to the others: the age of a lock is
// what the store says it is.
func TestAWrongClockTakesNoLockThatIsHeld(t *testing.T) {
	for _, bk := range timedBackends() {
		for name, c := range map[string]struct{ holder, reader time.Duration }{
			"the reader is 11 minutes ahead":   {0, 11 * time.Minute},
			"the reader is a day ahead":        {0, 24 * time.Hour},
			"the holder is 11 minutes behind":  {-11 * time.Minute, 0},
			"the two are 22 minutes apart":     {-11 * time.Minute, 11 * time.Minute},
			"both are 11 minutes ahead":        {11 * time.Minute, 11 * time.Minute},
			"the reader is 11 minutes behind":  {0, -11 * time.Minute},
			"the holder is 11 minutes ahead":   {11 * time.Minute, 0},
			"the clocks of the machines agree": {0, 0},
		} {
			t.Run(bk.name+"/"+name, func(t *testing.T) {
				b, clock := bk.make(t)
				stores := openMany(t, b, 2)
				holder, reader := stores[0], stores[1]

				restore := machineClock(t, c.holder)
				mustLock(t, holder, "web")
				restore()
				// Nine minutes pass, with a renewal on the way: the lock was written 3
				// minutes ago.
				clock.pass(6 * time.Minute)
				machineClock(t, c.holder)
				if err := holder.Renew("web"); err != nil {
					t.Fatal(err)
				}
				clock.pass(3 * time.Minute)

				machineClock(t, c.reader)
				if err := reader.Lock("web", "pravo apply", "t2"); !refused(err) {
					t.Fatalf("the other command took a lock that was renewed 3 minutes ago: %v", err)
				}
				if err := reader.Conflict("web"); !refused(err) {
					t.Errorf("Conflict: %v, want the lock of the holder", err)
				}
				locks, err := reader.Locks()
				if err != nil || len(locks) != 1 || locks[0].Stale {
					t.Errorf("Locks: %+v, %v, want one lock that is not stale", locks, err)
				}
				if released, err := reader.ReleaseStale(); err != nil || len(released) != 0 {
					t.Errorf("ReleaseStale released %v, %v", released, err)
				}
				if suspects, _ := reader.Suspects(); len(suspects) != 0 {
					t.Errorf("suspects: %v", suspects)
				}
				// The holder still has its lock.
				machineClock(t, c.holder)
				if err := holder.Renew("web"); err != nil {
					t.Errorf("the holder lost its lock: %v", err)
				}
			})
		}
	}
}

// A lock that its holder left is taken over ten minutes after the store last wrote it,
// whatever the clocks of the two machines say.
func TestALockLeftBehindIsTakenOverByTheClockOfTheStore(t *testing.T) {
	for _, bk := range timedBackends() {
		for name, c := range map[string]struct{ holder, reader time.Duration }{
			"the holder was 11 minutes ahead":  {11 * time.Minute, 0},
			"the reader is 11 minutes behind":  {0, -11 * time.Minute},
			"the two are 22 minutes apart":     {11 * time.Minute, -11 * time.Minute},
			"the clocks of the machines agree": {0, 0},
		} {
			t.Run(bk.name+"/"+name, func(t *testing.T) {
				b, clock := bk.make(t)
				stores := openMany(t, b, 2)
				holder, reader := stores[0], stores[1]
				restore := machineClock(t, c.holder)
				mustLock(t, holder, "web")
				restore()
				machineClock(t, c.reader)

				clock.pass(StaleAfter - time.Second)
				if err := reader.Conflict("web"); !refused(err) {
					t.Fatalf("a second before the lock goes stale: %v", err)
				}
				clock.pass(2 * time.Second)
				locks, err := reader.Locks()
				if err != nil || len(locks) != 1 || !locks[0].Stale {
					t.Fatalf("Locks: %+v, %v, want one stale lock", locks, err)
				}
				if err := reader.Lock("web", "pravo apply", "t2"); err != nil {
					t.Fatalf("the lock was not taken over a second after it went stale: %v", err)
				}
				// The lock is the reader's now, and the one who left it cannot renew it.
				if err := holder.Renew("web"); !refused(err) {
					t.Errorf("the former holder renewed the lock: %v", err)
				}
			})
		}
	}
}

// ReleaseStale releases the lock that is stale by the clock of the store, though the stamp
// in it is new to this machine, and leaves the one that the store wrote a moment ago, though
// the stamp in it is old.
func TestReleaseStaleGoesByTheClockOfTheStore(t *testing.T) {
	for _, bk := range timedBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b, clock := bk.make(t)
			stores := openMany(t, b, 3)
			gone, live, reader := stores[0], stores[1], stores[2]
			restore := machineClock(t, 11*time.Minute)
			mustLock(t, gone, "left")
			restore()
			clock.pass(StaleAfter + time.Second)
			restore = machineClock(t, -11*time.Minute)
			mustLock(t, live, "held")
			restore()

			released, err := reader.ReleaseStale()
			if err != nil || strings.Join(released, " ") != "left" {
				t.Fatalf("released %v, %v, want [left]", released, err)
			}
			if _, err := reader.LockOf("held"); err != nil {
				t.Errorf("the lock that is held is gone: %v", err)
			}
		})
	}
}

// A lock that was judged stale and renewed before the release is another object, and stays.
func TestReleaseStaleLeavesTheLockThatWasRenewedSince(t *testing.T) {
	b := NewMemBackend("memory")
	clock := newStoreClock()
	b.Clock = clock.Now
	stores := openMany(t, b, 2)
	holder, reader := stores[0], stores[1]
	// The clock of the machine moves only with the clock of the store. A renewal writes
	// the time of the machine into the lock, which is what makes it another object, and a
	// clock of coarse steps, as on Windows, gives the same reading twice within a test.
	machine := time.Now()
	before := lockClock
	lockClock = func() time.Time { return machine }
	t.Cleanup(func() { lockClock = before })
	mustLock(t, holder, "web")
	clock.pass(StaleAfter + time.Second)
	machine = machine.Add(StaleAfter + time.Second)
	// The holder comes back between the listing of the reader and its delete.
	reader.b = &beforeDelete{Backend: b, key: lockKey("web"), do: func() {
		if err := holder.Renew("web"); err != nil {
			t.Error(err)
		}
	}}
	released, err := reader.ReleaseStale()
	if err != nil || len(released) != 0 {
		t.Fatalf("released %v, %v: the lock was renewed after it was judged", released, err)
	}
	if err := holder.Renew("web"); err != nil {
		t.Errorf("the holder lost its lock: %v", err)
	}
}

// beforeDelete runs do before the first conditional delete of key, and keeps the times of
// the store.
type beforeDelete struct {
	Backend
	key  string
	do   func()
	once sync.Once
}

func (b *beforeDelete) GetTimed(ctx context.Context, key string) ([]byte, StoreTime, error) {
	return getTimed(ctx, b.Backend, key)
}

func (b *beforeDelete) DeleteIf(ctx context.Context, key string, match func([]byte) bool) (bool, error) {
	if key == b.key {
		b.once.Do(b.do)
	}
	return b.Backend.DeleteIf(ctx, key, match)
}

// A lock that its holder stranded is stale at once, though the store has just written it.
func TestAStrandedLockIsStaleAtOnceOnAStoreThatTellsItsTime(t *testing.T) {
	for _, bk := range timedBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b, _ := bk.make(t)
			stores := openMany(t, b, 2)
			holder, reader := stores[0], stores[1]
			mustLock(t, holder, "web")
			if err := holder.Strand("web"); err != nil {
				t.Fatal(err)
			}
			l, err := reader.LockOf("web")
			if err != nil || l.Live() {
				t.Errorf("LockOf: live=%v, %v, want a lock that is not live", l.Live(), err)
			}
			suspects, err := reader.Suspects()
			if err != nil || !suspects["web"].Creating {
				t.Errorf("suspects: %+v, %v, want the stranded lock with its note of a create", suspects, err)
			}
		})
	}
}

// "state unlock" takes a lock for live when the store wrote it less than three minutes ago.
func TestLockLiveGoesByTheClockOfTheStore(t *testing.T) {
	for _, bk := range timedBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b, clock := bk.make(t)
			stores := openMany(t, b, 2)
			holder, reader := stores[0], stores[1]
			mustLock(t, holder, "web")
			// On this host the process of the holder is this one, which is asked first: make
			// the lock one of another host.
			var l Lock
			raw, _ := b.Get(context.Background(), lockKey("web"))
			_ = json.Unmarshal(raw, &l)
			l.Host = "elsewhere"
			data, _ := json.Marshal(l)
			if err := b.Put(context.Background(), lockKey("web"), data); err != nil {
				t.Fatal(err)
			}
			machineClock(t, time.Hour)
			clock.pass(LiveAfter - time.Second)
			if got, err := reader.LockOf("web"); err != nil || !got.Live() {
				t.Errorf("a lock written a second less than three minutes ago: live=%v, %v", got.Live(), err)
			}
			clock.pass(2 * time.Second)
			machineClock(t, -time.Hour)
			if got, err := reader.LockOf("web"); err != nil || got.Live() {
				t.Errorf("a lock written a second more than three minutes ago: live=%v, %v", got.Live(), err)
			}
		})
	}
}

// A reservation ends when the time of the store passes its end, whatever the clock of the
// machine that reads it says.
func TestAReservationEndsByTheClockOfTheStore(t *testing.T) {
	for _, bk := range timedBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b, clock := bk.make(t)
			stores := openMany(t, b, 2)
			alice, bob := stores[0], stores[1]
			alice.SetActor("alice", "", "")
			bob.SetActor("bob", "", "")
			if _, err := alice.ReserveAll([]string{"web"}, "pravo plan --reserve 1h", clock.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			machineClock(t, 2*time.Hour)
			clock.pass(59 * time.Minute)
			if err := bob.Conflict("web"); !refused(err) {
				t.Fatalf("a minute before the end of the reservation, on a machine two hours ahead: %v", err)
			}
			machineClock(t, -2*time.Hour)
			clock.pass(2 * time.Minute)
			if err := bob.Conflict("web"); err != nil {
				t.Fatalf("a minute after the end of the reservation, on a machine two hours behind: %v", err)
			}
		})
	}
}

// The lock on the outputs is its holder's for a minute from the time the store wrote it.
func TestTheLockOnTheOutputsIsJudgedByTheClockOfTheStore(t *testing.T) {
	for _, bk := range timedBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b, clock := bk.make(t)
			s := openMany(t, b, 1)[0]
			restore := machineClock(t, -time.Hour)
			l := s.newLock("outputs.json", "pravo apply", "")
			l.Host, l.Nonce = "elsewhere", "another"
			restore()
			data, _ := json.Marshal(l)
			if err := b.Put(context.Background(), outputsLockKey, data); err != nil {
				t.Fatal(err)
			}
			clock.pass(outputsLockStale - time.Second)
			_, got, err := s.readOutputsLock()
			if err != nil || s.outputsLockStale(got) {
				t.Fatalf("a lock written 59 s ago with a stamp of an hour ago is taken for abandoned (%v)", err)
			}
			if err := s.checkAtRest(); !refused(err) {
				t.Errorf("the store is taken for one at rest: %v", err)
			}
			clock.pass(2 * time.Second)
			machineClock(t, -time.Hour)
			if _, got, err = s.readOutputsLock(); err != nil || !s.outputsLockStale(got) {
				t.Fatalf("a lock written 61 s ago is not taken for abandoned (%v)", err)
			}
		})
	}
}

// The backends give the two times of the answer to a read, and none when the store sends
// no Last-Modified or the answer was kept by a cache on the way.
func TestBackendsGiveTheTimeOfTheStore(t *testing.T) {
	ctx := context.Background()
	check := func(t *testing.T, b Backend, clock *storeClock) {
		t.Helper()
		if err := b.Put(ctx, "locks/a.lock", []byte("x")); err != nil {
			t.Fatal(err)
		}
		wrote := clock.Now()
		clock.pass(90 * time.Second)
		for name, wrapped := range map[string]Backend{"itself": b, "traced": traced(b), "read only": ReadOnly(b), "under an overlay": OverlayBackend(b)} {
			data, at, err := getTimed(ctx, wrapped, "locks/a.lock")
			if err != nil || string(data) != "x" {
				t.Fatalf("%s: %q, %v", name, data, err)
			}
			if !at.Known() || !at.Modified.Equal(wrote) || !at.Now.Equal(clock.Now()) || at.Age() != 90*time.Second {
				t.Errorf("%s: store time %+v, want written at %s and read at %s", name, at, wrote, clock.Now())
			}
		}
		if _, _, err := getTimed(ctx, b, "locks/none.lock"); !errors.Is(err, ErrNotFound) {
			t.Errorf("an object that is not there: %v", err)
		}
	}
	for _, bk := range timedBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b, clock := bk.make(t)
			check(t, b, clock)
		})
	}
	t.Run("s3 without Last-Modified", func(t *testing.T) {
		_, b := newFakeS3(t)
		_ = b.Put(ctx, "locks/a.lock", []byte("x"))
		if _, at, err := b.GetTimed(ctx, "locks/a.lock"); err != nil || at.Known() {
			t.Errorf("store time %+v, %v, want none", at, err)
		}
	})
	t.Run("s3 through a cache", func(t *testing.T) {
		f, b := newFakeS3(t)
		f.Clock, f.Aged = newStoreClock().Now, "120"
		_ = b.Put(ctx, "locks/a.lock", []byte("x"))
		if _, at, err := b.GetTimed(ctx, "locks/a.lock"); err != nil || at.Known() {
			t.Errorf("store time %+v, %v, want none: the answer carries Age", at, err)
		}
	})
	t.Run("http without Last-Modified", func(t *testing.T) {
		_, b := newFakeHTTP(t)
		_ = b.Put(ctx, "locks/a.lock", []byte("x"))
		if _, at, err := b.GetTimed(ctx, "locks/a.lock"); err != nil || at.Known() {
			t.Errorf("store time %+v, %v, want none", at, err)
		}
	})
	t.Run("memory without a clock", func(t *testing.T) {
		b := NewMemBackend("memory")
		_ = b.Put(ctx, "locks/a.lock", []byte("x"))
		if _, at, err := b.GetTimed(ctx, "locks/a.lock"); err != nil || at.Known() {
			t.Errorf("store time %+v, %v, want none", at, err)
		}
	})
}

// A store that tells no time has its locks judged as before: by the stamp of the holder and
// the clock of the reader.
func TestAStoreThatTellsNoTimeIsJudgedByTheClocksOfTheMachines(t *testing.T) {
	for _, bk := range testBackends()[1:] {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			stores := openMany(t, b, 2)
			holder, reader := stores[0], stores[1]
			mustLock(t, holder, "web")
			machineClock(t, StaleAfter+time.Minute)
			if err := reader.Lock("web", "pravo apply", "t2"); err != nil {
				t.Fatalf("a reader whose clock is 11 minutes ahead did not take the lock: %v", err)
			}
		})
	}
}

// The probe refuses a store whose Last-Modified and Date are not read off one clock: by
// those two, a lock that is held would look abandoned.
func TestProbeRefusesAStoreWhoseTimesDisagree(t *testing.T) {
	ctx := context.Background()
	type prober interface {
		Probe(ctx context.Context) error
	}
	stores := map[string]func(t *testing.T, clock func() time.Time, off time.Duration) prober{
		"s3": func(t *testing.T, clock func() time.Time, off time.Duration) prober {
			f, b := newFakeS3(t)
			f.Clock, f.ModifiedOff = clock, off
			return b
		},
		"http": func(t *testing.T, clock func() time.Time, off time.Duration) prober {
			f := fakehttp.New()
			f.Clock, f.ModifiedOff = clock, off
			srv := httptest.NewServer(f)
			t.Cleanup(srv.Close)
			b, err := NewHTTPBackend(HTTPConfig{URL: srv.URL + "/team/app/"})
			if err != nil {
				t.Fatal(err)
			}
			return b
		},
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			for what, c := range map[string]struct {
				clock func() time.Time
				off   time.Duration
				fails bool
			}{
				"no times":                           {nil, 0, false},
				"one clock":                          {time.Now, 0, false},
				"one clock that is a day behind":     {func() time.Time { return time.Now().Add(-24 * time.Hour) }, 0, false},
				"Last-Modified five hours behind":    {time.Now, -5 * time.Hour, true},
				"Last-Modified two hours ahead":      {time.Now, 2 * time.Hour, true},
				"Last-Modified twenty seconds ahead": {time.Now, 20 * time.Second, false},
			} {
				err := open(t, c.clock, c.off).Probe(ctx)
				var pe *ProbeError
				switch {
				case c.fails && !errors.As(err, &pe):
					t.Errorf("%s: the probe passed (%v)", what, err)
				case c.fails && !strings.Contains(err.Error(), "Last-Modified"):
					t.Errorf("%s: the error does not name the header: %v", what, err)
				case !c.fails && err != nil:
					t.Errorf("%s: %v", what, err)
				}
			}
		})
	}
}
