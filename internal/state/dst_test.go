package state_test

// Deterministic exploration of the coordination protocol of the store: the
// scenarios play two or three commands against one simulated bucket, in every
// order of their requests (or many random ones), with crashes, lost answers,
// stalls and clocks that disagree. See internal/dst.
//
// A run of the tests plays every scenario at its smallest size. DST_BUDGET=full
// adds the searches that take longer (the nightly run), and DST_BUDGET=big
// raises the limits of the bounded ones.
//
//	go test ./internal/state -run 'TestDST' -v
//	DST_BUDGET=big go test ./internal/state -run 'TestDST' -v -timeout 60m

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/dst"
	"github.com/pravo-systems/pravo/internal/state"
)

// report prints a search and fails the test when a schedule of it failed, unless
// the search shows a known limit.
func report(t *testing.T, rep dst.Report, knownLimit bool) {
	t.Helper()
	dst.Publish(t, rep, knownLimit)
}

const addr = "web.server"

func counter(r *state.Resource) int {
	var a struct {
		N int `json:"n"`
	}
	if r == nil || json.Unmarshal(r.Attributes, &a) != nil {
		return -1
	}
	return a.N
}

func resource(n int) *state.Resource {
	return &state.Resource{Addr: addr, Mode: "managed", Type: "thing", Name: "server", Provider: "p", ProviderKey: "p",
		Attributes: json.RawMessage(fmt.Sprintf(`{"n":%d}`, n)), ConfigHash: fmt.Sprintf("h%d", n)}
}

// cloud is what the scripts share: the object "in the cloud", who is changing it,
// and what each process ended with.
type cloud struct {
	mu      sync.Mutex
	n       int
	inside  map[int]time.Time // process -> when its hold was last confirmed (true time)
	outcome map[int]string
	nonce   map[int]string
	stamp   map[int]time.Time // process -> the time its lock says it was last renewed
}

// enter is called by a process that has passed its fence and is about to change
// the object. The process must hold the lock at that moment, and no other process
// may be changing the object under a lock whose own stamp is still fresh.
func (c *cloud) enter(w *dst.World, p *dst.Proc, confirmed time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	raw, ok := w.Object("locks/" + addr + ".lock")
	var l state.Lock
	_ = json.Unmarshal(raw, &l)
	if !ok || l.Nonce != c.nonce[p.ID] {
		w.Fail("fence: %s is about to change %s and believes it holds the lock (confirmed %s ago, a renewal is due after %s), and the lock is not its own any more; inside already: %v",
			p.Name, addr, now.Sub(confirmed), state.StaleAfter/2, len(c.inside))
	}
	for q, at := range c.inside {
		if q != p.ID && now.Sub(at) <= state.StaleAfter/2 {
			w.Fail("mutual exclusion: %s and P%d change %s at the same time. P%d confirmed its lock %s ago, so its fence does not ask for a renewal (due after %s); the lock it wrote is stamped %s ago, so %s took it over as stale",
				p.Name, q, addr, q, now.Sub(at), state.StaleAfter/2, now.Sub(c.stamp[q]), p.Name)
		}
	}
	c.inside[p.ID] = confirmed
}

func (c *cloud) leave(p *dst.Proc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inside, p.ID)
}

func (c *cloud) set(p *dst.Proc, s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.outcome[p.ID] = s
}

// foreignLock checks, on every change of a lock object, that a process deletes or
// replaces only a lock of its own, or one that is stale by the true clock: counted
// from the time its holder stamped it with, or, for a store that tells its own
// time, from the true time at which the store wrote it.
func foreignLock(w *dst.World, c *cloud, slack time.Duration) {
	w.OnMutate = func(p *dst.Proc, key string, before, after []byte, written, now time.Time) error {
		if !strings.HasPrefix(key, "locks/") && key != "outputs.lock" {
			return nil
		}
		var b, a state.Lock
		if after != nil && json.Unmarshal(after, &a) == nil {
			if before == nil {
				c.nonce[p.ID] = a.Nonce
			}
			if a.Nonce == c.nonce[p.ID] {
				c.stamp[p.ID] = a.RenewedAt
			}
		}
		if before == nil || json.Unmarshal(before, &b) != nil {
			return nil
		}
		if b.Nonce == c.nonce[p.ID] {
			return nil
		}
		last := b.RenewedAt
		if last.IsZero() {
			last = b.Since
		}
		limit := state.StaleAfter
		if key == "outputs.lock" {
			limit = state.OutputsLockStale()
			last = b.Since
		}
		if w.TellsTime {
			last = written
		}
		if now.Sub(last)+slack <= limit {
			return fmt.Errorf("foreign lock: %s removed or replaced the lock %s of another process, which was stamped %s ago (it goes stale after %s)", p.Name, key, now.Sub(last), limit)
		}
		return nil
	}
}

type applyOpts struct {
	format         int
	openInRun      bool // the store is opened as part of the interleaving
	fence          bool // renew before the change when the hold is older than StaleAfter/2, as the engine does
	sleepFirst     map[int]time.Duration
	procs          int
	skew           map[int]time.Duration
	renewFirst     map[int]bool  // the process renews its lock once before it changes the object (a heartbeat)
	work           time.Duration // how long the provider takes to change the object
	crashAfterLock map[int]bool  // the process takes the lock and is never heard of again
	wantApplied    int           // how many commands must report that they applied, when not 0
}

// applyScenario is the shape of an apply of one node: lock, compare the revision
// with the one the plan was made against, change the object, write the record,
// unlock. Each process adds one to the counter it read.
func applyScenario(name string, o applyOpts) dst.Scenario {
	if o.procs == 0 {
		o.procs = 2
	}
	return dst.Scenario{
		Name:  name,
		Procs: o.procs,
		Setup: func(w *dst.World, procs []*dst.Proc) []func(*dst.Proc) {
			c := &cloud{inside: map[int]time.Time{}, outcome: map[int]string{}, nonce: map[int]string{}, stamp: map[int]time.Time{}}
			w.Data = c
			restoreFormat := state.NewStoreFormat
			state.NewStoreFormat = o.format
			restore := state.SetLockClock(w.Clock, w.Jitter)
			w.T.Cleanup(func() { restore(); state.NewStoreFormat = restoreFormat })
			seed, err := state.OpenBackend(context.Background(), procs[0].B)
			if err != nil {
				w.T.Fatal(err)
			}
			if err := seed.Set(resource(0), "seed"); err != nil {
				w.T.Fatal(err)
			}
			stores := make([]*state.Store, len(procs))
			if !o.openInRun {
				for i, p := range procs {
					if stores[i], err = state.OpenBackend(context.Background(), p.B); err != nil {
						w.T.Fatal(err)
					}
				}
			}
			var slack time.Duration
			for i, p := range procs {
				p.Skew = o.skew[i]
				slack = max(slack, p.Skew, -p.Skew)
			}
			foreignLock(w, c, 0)
			scripts := make([]func(*dst.Proc), len(procs))
			for i := range procs {
				scripts[i] = func(p *dst.Proc) {
					st := stores[p.ID]
					if st == nil {
						var err error
						if st, err = state.OpenBackend(context.Background(), p.B); err != nil {
							c.set(p, "open failed: "+err.Error())
							return
						}
					}
					if d := o.sleepFirst[p.ID]; d > 0 {
						time.Sleep(d)
					}
					base := st.Get(addr)
					txn := fmt.Sprintf("txn-%s", p.Name)
					// As the engine does, the hold is counted from the moment before the request
					// that took or renewed the lock: the lock is stamped then.
					confirmed := time.Now()
					if err := st.Lock(addr, "apply", txn); err != nil {
						c.set(p, "rejected: "+short(err))
						return
					}
					if p.Dead() {
						return
					}
					if o.crashAfterLock[p.ID] {
						return // no outcome: End counts it as crashed
					}
					vers, err := st.Versions([]string{addr})
					if err != nil || vers[addr] != (state.Version{Revision: base.Revision, Generation: base.Generation}) {
						_ = st.Unlock(addr)
						if !p.Dead() {
							c.set(p, "stale")
						}
						return
					}
					if o.renewFirst[p.ID] {
						asked := time.Now()
						if err := st.Renew(addr); err != nil {
							c.set(p, "lock lost: "+short(err))
							return
						}
						confirmed = asked
					}
					if o.fence && time.Since(confirmed) > state.StaleAfter/2 {
						asked := time.Now()
						if err := st.Renew(addr); err != nil {
							c.set(p, "lock lost: "+short(err))
							return
						}
						confirmed = asked
					}
					if p.Dead() {
						return
					}
					// The change reaches the provider.
					if !p.Observe("passes its fence", "locks/"+addr+".lock") {
						return
					}
					c.enter(w, p, confirmed)
					c.mu.Lock()
					c.n = counter(base) + 1
					c.mu.Unlock()
					w.Note("%s changes the object to n=%d", p.Name, counter(base)+1)
					if o.work > 0 {
						time.Sleep(o.work)
					}
					if !p.Observe("the provider has changed the object", "locks/"+addr+".lock") {
						return
					}
					next := resource(counter(base) + 1)
					next.Revision, next.Generation = base.Revision, base.Generation
					err = st.Set(next, txn)
					p.Observe("has written or failed to write", "locks/"+addr+".lock")
					c.leave(p)
					if p.Dead() {
						return
					}
					uerr := st.Unlock(addr)
					if p.Dead() {
						return
					}
					if err != nil {
						c.set(p, "write failed: "+short(err))
					} else {
						c.set(p, "applied")
					}
					_ = uerr
				}
			}
			return scripts
		},
		End: func(w *dst.World) error {
			c := w.Data.(*cloud)
			st, err := state.OpenBackend(context.Background(), w.Reopen())
			if err != nil {
				return fmt.Errorf("the state does not open after the run: %w", err)
			}
			applied := 0
			for _, s := range c.outcome {
				if s == "applied" {
					applied++
				}
			}
			got := counter(st.Get(addr))
			unknown := 0
			for i := range o.procs {
				if _, ok := c.outcome[i]; !ok {
					unknown++ // crashed
				}
				if strings.HasPrefix(c.outcome[i], "write failed") {
					unknown++
				}
			}
			if o.wantApplied > 0 && applied != o.wantApplied {
				return fmt.Errorf("%d commands applied their change and %d must: a lock that its holder left was not taken over, or one that is held was; outcomes %v", applied, o.wantApplied, c.outcome)
			}
			if got < applied || got > applied+unknown {
				return fmt.Errorf("lost update: %d commands report that they applied their change (each adds one), %d crashed or failed to write, and the record holds n=%d; outcomes %v", applied, unknown, got, c.outcome)
			}
			if unknown == 0 && c.n != got {
				return fmt.Errorf("the record holds n=%d and the object holds n=%d, and no command crashed; outcomes %v", got, c.n, c.outcome)
			}
			if unknown == 0 {
				if locks := w.Keys("locks"); len(locks) > 0 {
					return fmt.Errorf("a lock is left though every command ended: %v; outcomes %v", locks, c.outcome)
				}
			}
			return nil
		},
	}
}

func short(err error) string {
	s := err.Error()
	if i := strings.Index(s, " (pid"); i > 0 {
		s = s[:i]
	}
	if len(s) > 90 {
		s = s[:90]
	}
	return s
}

// (a) two and three applies of one node, every interleaving.
func TestDSTApplyOneNode(t *testing.T) {
	for _, mode := range []dst.Mode{dst.Atomic, dst.S3} {
		for _, format := range []int{1, 2} {
			name := fmt.Sprintf("a: 2 applies of one node, format %d, mode %d", format, mode)
			report(t, dst.Exhaust(t, applyScenario(name, applyOpts{format: format}), dst.Options{Mode: mode}, 0, 3), false)
		}
	}
	report(t, dst.Exhaust(t, applyScenario("a: 2 applies, opened in the run, format 2, S3", applyOpts{format: 2, openInRun: true}), dst.Options{Mode: dst.S3}, 400000, 3), false)
	report(t, dst.Exhaust(t, applyScenario("a: 3 applies of one node, format 2, S3", applyOpts{format: 2, procs: 3}), dst.Options{Mode: dst.S3}, dst.Budget(300, 60000, 1500000), 3), false)
}

// (a) with one fault: a crash or a lost answer anywhere.
func TestDSTApplyOneNodeFaults(t *testing.T) {
	for _, format := range []int{1, 2} {
		report(t, dst.Exhaust(t, applyScenario(fmt.Sprintf("a: 2 applies, 1 crash, format %d, S3", format), applyOpts{format: format}), dst.Options{Mode: dst.S3, Crashes: 1}, 0, 3), false)
		report(t, dst.Exhaust(t, applyScenario(fmt.Sprintf("a: 2 applies, 1 lost answer, format %d, S3", format), applyOpts{format: format}), dst.Options{Mode: dst.S3, Lost: 1}, 0, 3), false)
	}
}

// (b) the zombie writer: one request of one command is stalled for 11 minutes
// (a suspended laptop, a connection that hangs) while the other command goes on.
func TestDSTZombieWriter(t *testing.T) {
	for _, format := range []int{1, 2} {
		for _, fence := range []bool{false, true} {
			name := fmt.Sprintf("b: 2 applies, 1 request stalled 11 min, format %d, fence %v, S3", format, fence)
			report(t, dst.Exhaust(t, applyScenario(name, applyOpts{format: format, fence: fence}), dst.Options{Mode: dst.S3, Stalls: 1}, 0, 3), false)
		}
	}
	// The second command starts 10.5 minutes in, while the request of the first is still stalled.
	late := map[int]time.Duration{1: 10*time.Minute + 30*time.Second}
	for _, format := range []int{1, 2} {
		name := fmt.Sprintf("b: 2 applies, 1 request stalled 11 min, P1 starts at 10m30s, format %d, fence, S3", format)
		// In format 1 the last writer of a record wins (ARCHITECTURE.md): the record that
		// arrives late replaces the one of the second command.
		report(t, dst.Exhaust(t, applyScenario(name, applyOpts{format: format, fence: true, sleepFirst: late}), dst.Options{Mode: dst.S3, Stalls: 1}, 0, 3), format == 1)
	}
	// The request that takes the lock is the one that may stall, the provider takes a minute to
	// change the object, and the second command starts at 11m30s.
	later := map[int]time.Duration{1: 11*time.Minute + 30*time.Second}
	for _, hb := range []bool{false, true} {
		name := fmt.Sprintf("b: 2 applies, 1 request stalled 11 min, provider takes 1 min, P1 starts at 11m30s, heartbeat %v, format 2, fence, S3", hb)
		report(t, dst.Exhaust(t, applyScenario(name, applyOpts{format: 2, fence: true, work: time.Minute, renewFirst: map[int]bool{0: hb}, sleepFirst: later}), dst.Options{Mode: dst.S3, Stalls: 1}, 0, 3), false)
	}
}

// (b) two commands find the same stale lock: one takes it over, and the other must
// not delete the lock that the first has just written.
func TestDSTTwoReclaimOneStaleLock(t *testing.T) {
	both := map[int]time.Duration{1: 10*time.Minute + 30*time.Second, 2: 10*time.Minute + 30*time.Second}
	limit := dst.Budget(300, 200000, 3000000)
	for _, mode := range []dst.Mode{dst.Atomic, dst.S3} {
		name := fmt.Sprintf("b: 3 applies, P0 holds a lock and crashes, P1 and P2 start at 10m30s, format 2, mode %d", mode)
		report(t, dst.Exhaust(t, applyScenario(name, applyOpts{format: 2, fence: true, procs: 3, sleepFirst: both, crashAfterLock: map[int]bool{0: true}}), dst.Options{Mode: mode}, limit, 3), false)
	}
}

// (b) clocks that disagree. A bucket tells its own time with every read, and the age
// of a lock is counted on that clock alone, so no clock of a command is part of the
// reclaim of a lock: a command whose clock is 9 or 11 minutes ahead takes no lock
// that is held, and the lock of a command whose clock is 11 minutes behind is not
// stale to the others.
//
// The holder is stalled for 2 minutes, the time between two renewals, so its lock is
// at most that old; the clock of the other command is ahead.
func TestDSTClockSkew(t *testing.T) {
	for _, skew := range []time.Duration{time.Minute, 7 * time.Minute, 8*time.Minute + time.Second, 9 * time.Minute, 11 * time.Minute, 24 * time.Hour} {
		name := fmt.Sprintf("b: 2 applies, P1 clock +%s and starts at 1m59s, 1 request stalled 2 min, format 2", skew)
		report(t, dst.Exhaust(t, applyScenario(name, applyOpts{format: 2, fence: true, skew: map[int]time.Duration{1: skew}, sleepFirst: map[int]time.Duration{1: 119 * time.Second}}),
			dst.Options{Mode: dst.S3, Stalls: 1, Stall: 2 * time.Minute}, 0, 3), false)
	}
	for who, skew := range map[int]time.Duration{1: 11 * time.Minute, 0: -11 * time.Minute} {
		name := fmt.Sprintf("b: 2 applies, P%d clock %+v, nothing stalled, format 2", who, skew)
		report(t, dst.Exhaust(t, applyScenario(name, applyOpts{format: 2, fence: true, skew: map[int]time.Duration{who: skew}}), dst.Options{Mode: dst.S3}, 0, 3), false)
	}
	// The holder is gone, and the other command comes 10m30s later: the lock is taken
	// over whatever the two clocks say, also when the stamp in it is of the future or
	// the clock of the reader is behind.
	for _, skew := range []map[int]time.Duration{{0: 11 * time.Minute}, {1: -11 * time.Minute}, {0: -11 * time.Minute, 1: 11 * time.Minute}} {
		name := fmt.Sprintf("b: 2 applies, P0 holds a lock and crashes, P1 starts at 10m30s, clocks %v, format 2", skew)
		sc := applyScenario(name, applyOpts{format: 2, fence: true, skew: skew, sleepFirst: map[int]time.Duration{1: 10*time.Minute + 30*time.Second}, crashAfterLock: map[int]bool{0: true}, wantApplied: 1})
		report(t, dst.Exhaust(t, sc, dst.Options{Mode: dst.S3}, 0, 3), false)
	}
}

// (b) the same for a store that tells no time, as an S3-compatible one that sends no
// Last-Modified: the lock is then judged by the time its holder stamped it with and
// the clock of the command that reads it, and the reclaim tolerates as much skew as
// is left of StaleAfter. These searches show the limit that stays for such a store.
func TestDSTClockSkewStoreWithoutTime(t *testing.T) {
	// limit publishes a search that must show the limit when shows is set, and must
	// be clean when it is not.
	limit := func(sc dst.Scenario, o dst.Options, shows bool) {
		t.Helper()
		o.Mode, o.NoStoreTime = dst.S3, true
		rep := dst.Exhaust(t, sc, o, 0, 3)
		report(t, rep, shows)
		if shows && len(rep.Failures) == 0 {
			t.Errorf("%s: no schedule fails, and the limit of a store that tells no time was to show here", sc.Name)
		}
	}
	for _, skew := range []time.Duration{time.Minute, 7 * time.Minute, 8*time.Minute + time.Second, 9 * time.Minute, 11 * time.Minute} {
		name := fmt.Sprintf("b: 2 applies, P1 clock +%s and starts at 1m59s, 1 request stalled 2 min, format 2, the store tells no time", skew)
		// A lock that was stamped 1m59s ago is taken over by a command whose clock is
		// ahead by more than what is left of StaleAfter, a second over 8 minutes.
		limit(applyScenario(name, applyOpts{format: 2, fence: true, skew: map[int]time.Duration{1: skew}, sleepFirst: map[int]time.Duration{1: 119 * time.Second}}),
			dst.Options{Stalls: 1, Stall: 2 * time.Minute}, skew+119*time.Second > state.StaleAfter)
	}
	// No time passes at all: a clock that is ahead by more than StaleAfter takes any lock.
	name := "b: 2 applies, P1 clock +11m, nothing stalled, format 2, the store tells no time"
	limit(applyScenario(name, applyOpts{format: 2, fence: true, skew: map[int]time.Duration{1: 11 * time.Minute}}), dst.Options{}, true)
	// A clock that is behind: the lock it takes is stale to everyone else at once.
	name = "b: 2 applies, P0 clock -11m, nothing stalled, format 2, the store tells no time"
	limit(applyScenario(name, applyOpts{format: 2, fence: true, skew: map[int]time.Duration{0: -11 * time.Minute}}), dst.Options{}, true)
	// A clock that is ahead: the lock it left is held for that much longer.
	name = "b: 2 applies, P0 clock +11m holds a lock and crashes, P1 starts at 10m30s, format 2, the store tells no time"
	limit(applyScenario(name, applyOpts{format: 2, fence: true, skew: map[int]time.Duration{0: 11 * time.Minute}, sleepFirst: map[int]time.Duration{1: 10*time.Minute + 30*time.Second}, crashAfterLock: map[int]bool{0: true}, wantApplied: 1}), dst.Options{}, true)
}

// (d) Refresh racing Set: a plan that writes what it read while an apply writes the node.
func TestDSTRefreshRacesSet(t *testing.T) {
	for _, format := range []int{1, 2} {
		for _, mode := range []dst.Mode{dst.Atomic, dst.S3} {
			sc := dst.Scenario{
				Name:  fmt.Sprintf("d: refresh (TryHold, Refresh) against apply (Lock, Set), format %d, mode %d", format, mode),
				Procs: 2,
				Setup: func(w *dst.World, procs []*dst.Proc) []func(*dst.Proc) {
					c := &cloud{inside: map[int]time.Time{}, outcome: map[int]string{}, nonce: map[int]string{}, stamp: map[int]time.Time{}}
					w.Data = c
					restoreFormat := state.NewStoreFormat
					state.NewStoreFormat = format
					restore := state.SetLockClock(w.Clock, w.Jitter)
					w.T.Cleanup(func() { restore(); state.NewStoreFormat = restoreFormat })
					seed, _ := state.OpenBackend(context.Background(), procs[0].B)
					if err := seed.Set(resource(0), "seed"); err != nil {
						w.T.Fatal(err)
					}
					stores := make([]*state.Store, 2)
					for i, p := range procs {
						stores[i], _ = state.OpenBackend(context.Background(), p.B)
					}
					foreignLock(w, c, 0)
					apply := func(p *dst.Proc) {
						st := stores[p.ID]
						base := st.Get(addr)
						if err := st.Lock(addr, "apply", "txn-apply"); err != nil {
							c.set(p, "rejected")
							return
						}
						vers, err := st.Versions([]string{addr})
						if err != nil || vers[addr] != (state.Version{Revision: base.Revision, Generation: base.Generation}) {
							_ = st.Unlock(addr)
							c.set(p, "stale")
							return
						}
						next := resource(1)
						next.Revision, next.Generation = base.Revision, base.Generation
						if err := st.Set(next, "txn-apply"); err != nil {
							c.set(p, "write failed: "+short(err))
						} else {
							c.set(p, "applied")
						}
						_ = st.Unlock(addr)
					}
					refresh := func(p *dst.Proc) {
						// What a plan does with a record it read from the provider (persistRefreshed):
						// take the lock for the short write if nobody holds it, write with Refresh.
						st := stores[p.ID]
						base := st.Get(addr)
						release, ok, err := st.TryHold(addr, "plan", "")
						if err != nil || !ok {
							c.set(p, "not written")
							return
						}
						read := *base
						now := time.Now().UTC()
						read.RefreshedAt = &now
						read.Attributes = json.RawMessage(`{"n":0,"seen":true}`) // the provider reports a drifted attribute of the old object
						read.DriftedAt = &now
						err = st.Refresh(&read, "")
						release()
						switch {
						case errors.Is(err, state.ErrChanged):
							c.set(p, "changed")
						case err != nil:
							c.set(p, "write failed: "+short(err))
						default:
							c.set(p, "refreshed")
						}
					}
					return []func(*dst.Proc){apply, refresh}
				},
				End: func(w *dst.World) error {
					c := w.Data.(*cloud)
					st, err := state.OpenBackend(context.Background(), w.Reopen())
					if err != nil {
						return err
					}
					r := st.Get(addr)
					// The apply wrote n=1. A refresh that read n=0 must not be the record afterwards
					// when the apply reports that it applied, unless the refresh came first and the
					// apply then found the plan stale.
					if c.outcome[0] == "applied" && counter(r) != 1 {
						return fmt.Errorf("lost update: the apply reports applied (n=1) and the record holds %s after a refresh of the old object; outcomes %v", r.Attributes, c.outcome)
					}
					if locks := w.Keys("locks"); len(locks) > 0 {
						return fmt.Errorf("a lock is left: %v", locks)
					}
					return nil
				},
			}
			report(t, dst.Exhaust(t, sc, dst.Options{Mode: mode}, 0, 3), false)
		}
	}
}

// (e) two updates of the outputs.
func outputsScenario(name string, procs int) dst.Scenario {
	return dst.Scenario{
		Name:  name,
		Procs: procs,
		Setup: func(w *dst.World, ps []*dst.Proc) []func(*dst.Proc) {
			c := &cloud{inside: map[int]time.Time{}, outcome: map[int]string{}, nonce: map[int]string{}, stamp: map[int]time.Time{}}
			w.Data = c
			restore := state.SetLockClock(w.Clock, w.Jitter)
			w.T.Cleanup(restore)
			stores := make([]*state.Store, len(ps))
			for i, p := range ps {
				stores[i], _ = state.OpenBackend(context.Background(), p.B)
			}
			foreignLock(w, c, 0)
			scripts := make([]func(*dst.Proc), len(ps))
			for i := range ps {
				scripts[i] = func(p *dst.Proc) {
					key := fmt.Sprintf("out%d", p.ID)
					err := stores[p.ID].UpdateOutputs(func(rec map[string]json.RawMessage, present bool) (map[string]json.RawMessage, bool, error) {
						rec[key] = json.RawMessage(`"v"`)
						return rec, true, nil
					})
					if p.Dead() {
						return
					}
					if err != nil {
						c.set(p, "failed: "+short(err))
					} else {
						c.set(p, "written")
					}
				}
			}
			return scripts
		},
		End: func(w *dst.World) error {
			c := w.Data.(*cloud)
			data, _ := w.Object("outputs.json")
			var out map[string]json.RawMessage
			_ = json.Unmarshal(data, &out)
			for i := range procs {
				if c.outcome[i] == "written" {
					if _, ok := out[fmt.Sprintf("out%d", i)]; !ok {
						return fmt.Errorf("lost update: P%d reports that it wrote its output and outputs.json holds %s; outcomes %v", i, data, c.outcome)
					}
				}
			}
			if _, ok := w.Object("outputs.lock"); ok && len(c.outcome) == procs {
				return fmt.Errorf("outputs.lock is left though every command ended; outcomes %v", c.outcome)
			}
			return nil
		},
	}
}

func TestDSTOutputs(t *testing.T) {
	for _, mode := range []dst.Mode{dst.Atomic, dst.S3} {
		report(t, dst.Exhaust(t, outputsScenario(fmt.Sprintf("e: 2 updates of the outputs, mode %d", mode), 2), dst.Options{Mode: mode}, 0, 3), false)
	}
	report(t, dst.Exhaust(t, outputsScenario("e: 2 updates of the outputs, 1 crash, S3", 2), dst.Options{Mode: dst.S3, Crashes: 1}, 0, 3), false)
	report(t, dst.Exhaust(t, outputsScenario("e: 2 updates of the outputs, 1 lost answer, S3", 2), dst.Options{Mode: dst.S3, Lost: 1}, 0, 3), false)
	report(t, dst.Exhaust(t, outputsScenario("e: 2 updates of the outputs, 1 request stalled 61 s, S3", 2), dst.Options{Mode: dst.S3, Stalls: 1, Stall: 61 * time.Second, MaxSteps: 20000}, 0, 3), false)
	report(t, dst.Exhaust(t, outputsScenario("e: 3 updates of the outputs, S3", 3), dst.Options{Mode: dst.S3}, dst.Budget(300, 100000, 1000000), 3), false)
}

// (g) two first opens of an empty state.
func TestDSTTwoFirstOpens(t *testing.T) {
	sc := dst.Scenario{
		Name:  "g: 2 first opens of an empty state",
		Procs: 2,
		Setup: func(w *dst.World, ps []*dst.Proc) []func(*dst.Proc) {
			lineages := make([]string, 2)
			w.Data = lineages
			scripts := make([]func(*dst.Proc), 2)
			for i := range ps {
				scripts[i] = func(p *dst.Proc) {
					st, err := state.OpenBackend(context.Background(), p.B)
					if err != nil {
						lineages[p.ID] = "error: " + err.Error()
						return
					}
					lineages[p.ID] = st.Lineage()
				}
			}
			return scripts
		},
		End: func(w *dst.World) error {
			lineages := w.Data.([]string)
			data, _ := w.Object("meta.json")
			var m struct {
				Lineage string `json:"lineage"`
			}
			_ = json.Unmarshal(data, &m)
			for i, l := range lineages {
				if l != m.Lineage {
					return fmt.Errorf("P%d went on with the lineage %s and the state has %s", i, l, m.Lineage)
				}
			}
			return nil
		},
	}
	for _, o := range []dst.Options{{Mode: dst.S3}, {Mode: dst.S3, Lost: 1}} {
		rep := dst.Exhaust(t, sc, o, 0, 3)
		if o.Lost > 0 {
			rep.Scenario += ", 1 lost answer"
		}
		report(t, rep, false)
	}
}
