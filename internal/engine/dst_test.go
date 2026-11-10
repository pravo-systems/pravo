package engine

// Deterministic exploration of the locks of an apply (heldLocks): two or three
// applies take overlapping sets of locks over one simulated bucket, in every
// order of their requests. See internal/dst and internal/state/dst_test.go.
//
// A run of the tests plays the short searches; DST_BUDGET=full adds the ones
// that take minutes (the nightly run).
//
//	go test ./internal/engine -run 'TestDST' -v
//	DST_BUDGET=full go test ./internal/engine -run 'TestDST' -v -timeout 30m

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/dst"
	"github.com/pravo-systems/pravo/internal/state"
)

func dstLockKey(addr string) string { return "locks/" + addr + ".lock" }

// tally counts how the complete runs of a search ended, to show that a search
// that found nothing saw every outcome and not one only.
var tally = map[string]int{}

// publish prints a search with the tally of its outcomes.
func publish(t *testing.T, rep dst.Report, knownLimit bool) {
	t.Helper()
	var keys []string
	for k := range tally {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %d", k, tally[k]))
	}
	clear(tally)
	rep.Scenario += " [" + strings.Join(parts, "; ") + "]"
	dst.Publish(t, rep, knownLimit)
}

// lockRun is what the scripts of one run share.
type lockRun struct {
	mu      sync.Mutex
	inside  map[string]int // node -> the process that is changing it
	nonce   map[int]string
	outcome map[int]string
	changes map[string]int // node -> how many times it was changed
	by      map[int]string // a process that was turned away -> the transaction its error names
}

func (r *lockRun) set(p *dst.Proc, s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outcome[p.ID] = s
}

// enter is called by an apply whose fence has passed and whose change of addr is
// about to reach the provider.
func (r *lockRun) enter(w *dst.World, p *dst.Proc, addr string, beat time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	raw, ok := w.Object(dstLockKey(addr))
	var l state.Lock
	_ = json.Unmarshal(raw, &l)
	if !ok || l.Nonce != r.nonce[p.ID] {
		w.Fail("fence: the fence of %s passed (its locks were last confirmed %s ago) and the lock on %s is not its own any more",
			p.Name, time.Since(beat).Round(time.Second), addr)
	}
	if q, busy := r.inside[addr]; busy && q != p.ID {
		w.Fail("mutual exclusion: %s and P%d change %s at the same time (the fence of %s passed: its locks were last confirmed %s ago)",
			p.Name, q, addr, p.Name, time.Since(beat).Round(time.Second))
	}
	r.inside[addr] = p.ID
	r.changes[addr]++
}

func (r *lockRun) leave(p *dst.Proc, addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inside[addr] == p.ID {
		delete(r.inside, addr)
	}
}

type locksOpts struct {
	sets       [][]string // the nodes each apply writes, sorted
	work       time.Duration
	sleepFirst map[int]time.Duration
	interval   time.Duration // the heartbeat; 0 is none
	renew      map[int]bool  // the apply renews its locks once, as a heartbeat would, before its first change
	faultFree  bool
	anyHolder  bool // do not check which apply the error of one that is turned away names
}

func locksScenario(name string, o locksOpts) dst.Scenario {
	return dst.Scenario{
		Name:  name,
		Procs: len(o.sets),
		Setup: func(w *dst.World, procs []*dst.Proc) []func(*dst.Proc) {
			r := &lockRun{inside: map[string]int{}, nonce: map[int]string{}, outcome: map[int]string{}, changes: map[string]int{}, by: map[int]string{}}
			w.Data = r
			restoreFormat := state.NewStoreFormat
			state.NewStoreFormat = 2
			jitter := applyJitter
			applyJitter = func(d time.Duration) time.Duration { return d / 2 }
			w.T.Cleanup(func() { applyJitter = jitter; state.NewStoreFormat = restoreFormat })
			stores := make([]*state.Store, len(procs))
			for i, p := range procs {
				var err error
				if stores[i], err = state.OpenBackend(context.Background(), p.B); err != nil {
					w.T.Fatal(err)
				}
			}
			w.OnMutate = func(p *dst.Proc, key string, before, after []byte, now time.Time) error {
				if !strings.HasPrefix(key, "locks/") {
					return nil
				}
				var b, a state.Lock
				if before == nil && after != nil && json.Unmarshal(after, &a) == nil {
					r.nonce[p.ID] = a.Nonce
				}
				if before == nil || json.Unmarshal(before, &b) != nil || b.Nonce == r.nonce[p.ID] {
					return nil
				}
				last := b.RenewedAt
				if last.IsZero() {
					last = b.Since
				}
				if now.Sub(last) <= state.StaleAfter {
					return fmt.Errorf("foreign lock: %s removed or replaced the lock %s of another apply, which was stamped %s ago", p.Name, key, now.Sub(last))
				}
				return nil
			}
			interval := o.interval
			if interval == 0 {
				interval = 1000 * time.Hour
			}
			scripts := make([]func(*dst.Proc), len(procs))
			for i := range procs {
				scripts[i] = func(p *dst.Proc) {
					if d := o.sleepFirst[p.ID]; d > 0 {
						time.Sleep(d)
					}
					txn := "txn-" + p.Name
					h := newHeldLocks(stores[p.ID], func(string, ...any) {}, interval)
					defer h.release()
					if err := h.acquire(o.sets[p.ID], "apply", txn); err != nil {
						if !p.Dead() {
							var locked *state.ErrLocked
							r.set(p, "rejected")
							if errors.As(err, &locked) {
								r.mu.Lock()
								r.by[p.ID] = locked.Lock.Txn
								r.mu.Unlock()
							}
						}
						return
					}
					if p.Dead() {
						return
					}
					// The apply has all its locks: its transaction is written as running.
					_ = p.B.Put(context.Background(), "txn/"+txn+".json", []byte(`{"status":"running"}`))
					if o.renew[p.ID] {
						h.renew()
					}
					for _, a := range o.sets[p.ID] {
						if err := h.fence(); err != nil {
							if !p.Dead() {
								r.set(p, "stopped: lock lost")
							}
							return
						}
						if !p.Observe("passes its fence for "+a, dstLockKey(a)) {
							return
						}
						r.enter(w, p, a, h.lastBeat())
						if o.work > 0 {
							time.Sleep(o.work)
						}
						if !p.Observe("has changed "+a, dstLockKey(a)) {
							return
						}
						r.leave(p, a)
					}
					_ = p.B.Put(context.Background(), "txn/"+txn+".json", []byte(`{"status":"applied"}`))
					h.release()
					if !p.Dead() {
						r.set(p, "applied")
					}
				}
			}
			return scripts
		},
		End: func(w *dst.World) error {
			r := w.Data.(*lockRun)
			ended := len(r.outcome) == len(o.sets)
			var outs []string
			for i := range o.sets {
				if s, ok := r.outcome[i]; ok {
					outs = append(outs, s)
				} else {
					outs = append(outs, "crashed")
				}
			}
			sort.Strings(outs)
			tally[strings.Join(outs, "+")]++
			applied, lost := 0, 0
			want := map[string]int{}
			for i, s := range r.outcome {
				if s == "applied" {
					applied++
					for _, a := range o.sets[i] {
						want[a]++
					}
				}
				if strings.HasPrefix(s, "stopped") {
					lost++
				}
			}
			if ended {
				if locks := w.Keys("locks"); len(locks) > 0 && lost == 0 {
					return fmt.Errorf("a lock is left though every apply ended: %v; outcomes %v", locks, r.outcome)
				}
				if lost == 0 {
					for a, n := range r.changes {
						if n != want[a] {
							return fmt.Errorf("%s was changed %d times and %d applies that write it report that they applied; outcomes %v", a, n, want[a], r.outcome)
						}
					}
				}
			}
			if o.faultFree && !o.anyHolder {
				// "An apply is not turned away because of another that was turned away."
				for i, txn := range r.by {
					for k := range o.sets {
						if txn == fmt.Sprintf("txn-P%d", k) && r.outcome[k] == "rejected" {
							return fmt.Errorf("P%d was turned away because of P%d, which was turned away itself; outcomes %v", i, k, r.outcome)
						}
					}
				}
			}
			if o.faultFree && applied == 0 {
				return fmt.Errorf("every apply was turned away, and nothing failed: none was granted; outcomes %v", r.outcome)
			}
			return nil
		},
	}
}

// (c) applies whose sets of locks overlap, every interleaving.
func TestDSTApplyLocks(t *testing.T) {
	limit := dst.Budget(150000, 150000, 3000000)
	cases := []struct {
		name string
		sets [][]string
	}{
		{"same node", [][]string{{"a"}, {"a"}}},
		{"same 2 nodes", [][]string{{"a", "b"}, {"a", "b"}}},
		{"a,b and b,c", [][]string{{"a", "b"}, {"b", "c"}}},
		{"a,c and b,c (meet at the second lock)", [][]string{{"a", "c"}, {"b", "c"}}},
		{"a,b,c and b,c,d", [][]string{{"a", "b", "c"}, {"b", "c", "d"}}},
	}
	for _, c := range cases {
		if len(c.sets[0]) > 2 && !dst.Full() {
			continue // minutes
		}
		for _, mode := range []dst.Mode{dst.Atomic, dst.S3} {
			name := fmt.Sprintf("c: 2 applies lock %s, mode %d", c.name, mode)
			publish(t, dst.Exhaust(t, locksScenario(name, locksOpts{sets: c.sets, faultFree: true}), dst.Options{Mode: mode}, limit, 3), false)
		}
	}
	// The provider takes a minute: the apply that waits looks at the lock meanwhile.
	publish(t, dst.Exhaust(t, locksScenario("c: 2 applies lock a,c and b,c, provider takes 1 min, S3", locksOpts{sets: [][]string{{"a", "c"}, {"b", "c"}}, work: time.Minute, faultFree: true}), dst.Options{Mode: dst.S3}, limit, 3), false)
}

// (c) three applies in a ring: a,b / b,c / a,c.
func TestDSTApplyLocksRing(t *testing.T) {
	sets := [][]string{{"a", "b"}, {"b", "c"}, {"a", "c"}}
	// Every order of the requests of three applies is more than a search of hours
	// plays; the bounded search takes minutes, and a run of the tests plays its start.
	limit := dst.Budget(400, 150000, 3000000)
	publish(t, dst.Exhaust(t, locksScenario("c: 3 applies lock a,b / b,c / a,c, atomic", locksOpts{sets: sets, faultFree: true, anyHolder: true}), dst.Options{Mode: dst.Atomic}, limit, 3), false)
	// The same, and the error of an apply that is turned away must name an apply that was granted.
	publish(t, dst.Exhaust(t, locksScenario("c: 3 applies lock a,b / b,c / a,c, atomic, who the error names", locksOpts{sets: sets, faultFree: true}), dst.Options{Mode: dst.Atomic}, limit, 3), false)
	runs := dst.Budget(1500, 20000, 400000)
	publish(t, dst.Random(t, locksScenario("c: 3 applies lock a,b / b,c / a,c, S3, random", locksOpts{sets: sets, faultFree: true}), dst.Options{Mode: dst.S3}, 20261110, runs, 4, 0, 60, 3, 0), false)
	publish(t, dst.Random(t, locksScenario("c: 3 applies in a ring, S3, random, 1 crash + 1 lost answer", locksOpts{sets: sets}), dst.Options{Mode: dst.S3, Crashes: 1, Lost: 1}, 20261110, runs, 4, 30, 60, 3, 0), false)
}

// (c) with one fault.
func TestDSTApplyLocksFaults(t *testing.T) {
	limit := dst.Budget(400, 150000, 3000000)
	sets := [][]string{{"a", "c"}, {"b", "c"}}
	publish(t, dst.Exhaust(t, locksScenario("c: 2 applies lock a,c and b,c, 1 crash, S3", locksOpts{sets: sets}), dst.Options{Mode: dst.S3, Crashes: 1}, limit, 3), false)
	publish(t, dst.Exhaust(t, locksScenario("c: 2 applies lock a,c and b,c, 1 lost answer, S3", locksOpts{sets: sets}), dst.Options{Mode: dst.S3, Lost: 1}, limit, 3), false)
}

// (b) at the level of the engine: one request of an apply is stalled for 11
// minutes, the provider takes a minute, the other apply starts at 11m30s.
func TestDSTApplyLocksStall(t *testing.T) {
	one := [][]string{{"a"}, {"a"}}
	late := map[int]time.Duration{1: 11*time.Minute + 30*time.Second}
	publish(t, dst.Exhaust(t, locksScenario("c: 2 applies lock a, 1 request stalled 11 min, provider 1 min, P1 at 11m30s, heartbeat 2 min",
		locksOpts{sets: one, work: time.Minute, sleepFirst: late, interval: state.HeartbeatInterval}), dst.Options{Mode: dst.S3, Stalls: 1}, 0, 3), false)
	publish(t, dst.Exhaust(t, locksScenario("c: 2 applies lock a, 1 request stalled 11 min, provider 1 min, P1 at 11m30s, P0 renews once, no heartbeat",
		locksOpts{sets: one, work: time.Minute, sleepFirst: late, renew: map[int]bool{0: true}}), dst.Options{Mode: dst.S3, Stalls: 1}, 0, 3), false)
}

// planRun is what the scripts of a run with a stored plan share.
type planRun struct {
	mu      sync.Mutex
	id      string
	began   map[int]error
	read1   map[int]error
	read2   map[int]error
	removed map[int]bool
	ended   map[int]bool
}

type planOpts struct {
	applies, collectors int
	skew                map[int]time.Duration
	end                 bool // the apply says that it ended
}

// planScenario plays applies that begin on a stored plan that is past its time to
// live against collectors that want to remove it.
func planScenario(name string, o planOpts) dst.Scenario {
	return dst.Scenario{
		Name:  name,
		Procs: o.applies + o.collectors,
		Setup: func(w *dst.World, procs []*dst.Proc) []func(*dst.Proc) {
			r := &planRun{began: map[int]error{}, read1: map[int]error{}, read2: map[int]error{}, removed: map[int]bool{}, ended: map[int]bool{}}
			w.Data = r
			ctx := context.Background()
			stores := make([]*PlanStore, len(procs))
			for i, p := range procs {
				p.Skew = o.skew[i]
				stores[i] = NewPlanStore(p.B)
				stores[i].Now = p.Now
			}
			seed := NewPlanStore(procs[0].B)
			seed.Now = time.Now
			r.id = putPlan(w.T, seed, time.Now().Add(-2*time.Hour), "alice", time.Hour, change("r0", ActionCreate, 0)).ID
			scripts := make([]func(*dst.Proc), len(procs))
			for i := range procs {
				if i < o.applies {
					scripts[i] = func(p *dst.Proc) {
						ps, owner := stores[p.ID], "apply#"+p.Name
						err := ps.BeginApply(ctx, r.id, owner)
						if p.Dead() {
							return
						}
						r.mu.Lock()
						r.began[p.ID] = err
						r.mu.Unlock()
						if err != nil {
							return
						}
						_, _, err = ps.ReadFile(ctx, r.id)
						if p.Dead() {
							return
						}
						r.mu.Lock()
						r.read1[p.ID] = err
						r.mu.Unlock()
						if err != nil {
							return
						}
						// The apply works, and reads the plan again (as the mark of what it applied does).
						_, _, err = ps.ReadFile(ctx, r.id)
						if p.Dead() {
							return
						}
						r.mu.Lock()
						r.read2[p.ID] = err
						r.mu.Unlock()
						if o.end {
							ps.EndApply(ctx, r.id, owner)
						}
						r.mu.Lock()
						r.ended[p.ID] = true
						r.mu.Unlock()
					}
					continue
				}
				scripts[i] = func(p *dst.Proc) {
					res, err := stores[p.ID].GC(ctx, GCOptions{Owner: "collector#" + p.Name})
					if p.Dead() || err != nil {
						return
					}
					r.mu.Lock()
					defer r.mu.Unlock()
					for _, it := range res.Removed {
						if it.ID == r.id {
							r.removed[p.ID] = true
						}
					}
					r.ended[p.ID] = true
				}
			}
			return scripts
		},
		End: func(w *dst.World) error {
			r := w.Data.(*planRun)
			removers := len(r.removed)
			_, plan := w.Object(planKey(r.id))
			_, meta := w.Object(metaKey2(r.id))
			held := 0
			for i := range o.applies {
				if e, ok := r.read1[i]; ok && e == nil {
					held++
				}
			}
			tally[fmt.Sprintf("%d applies hold the plan, removed by %d", held, removers)]++
			for i := range o.applies {
				if e, ok := r.read1[i]; ok && e == nil {
					// The apply began and read the plan: it holds it.
					if e2, ok := r.read2[i]; ok && e2 != nil {
						return fmt.Errorf("the plan was removed from under P%d: its apply began and read the plan, and the second read failed: %v", i, e2)
					}
					if !o.end && r.ended[i] && (removers > 0 || !plan || !meta) {
						return fmt.Errorf("P%d began an apply from the plan, which never ended, and the plan was removed (%d collectors report it, plan object %v, summary %v)", i, removers, plan, meta)
					}
				}
			}
			if removers > 1 {
				return fmt.Errorf("%d collectors report that they removed the plan", removers)
			}
			if removers == 1 && (plan || meta) {
				return fmt.Errorf("a collector reports the plan removed and an object of it is there (plan object %v, summary %v)", plan, meta)
			}
			if plan != meta && len(r.ended) == o.applies+o.collectors {
				return fmt.Errorf("the plan is left half removed though every command ended: plan object %v, summary %v", plan, meta)
			}
			return nil
		},
	}
}

// (f) the claims on a stored plan: an apply that begins against a collector that removes.
func TestDSTStoredPlanClaims(t *testing.T) {
	limit := dst.Budget(150000, 150000, 3000000)
	for _, mode := range []dst.Mode{dst.Atomic, dst.S3} {
		publish(t, dst.Exhaust(t, planScenario(fmt.Sprintf("f: 1 apply begins, 1 collector removes, mode %d", mode), planOpts{applies: 1, collectors: 1}), dst.Options{Mode: mode}, limit, 3), false)
	}
	publish(t, dst.Exhaust(t, planScenario("f: 1 apply begins and ends, 1 collector, S3", planOpts{applies: 1, collectors: 1, end: true}), dst.Options{Mode: dst.S3}, limit, 3), false)
	publish(t, dst.Exhaust(t, planScenario("f: 2 collectors, S3", planOpts{collectors: 2}), dst.Options{Mode: dst.S3}, limit, 3), false)
	publish(t, dst.Exhaust(t, planScenario("f: 1 apply, 1 collector, 1 crash, S3", planOpts{applies: 1, collectors: 1}), dst.Options{Mode: dst.S3, Crashes: 1}, limit, 3), false)
	publish(t, dst.Exhaust(t, planScenario("f: 1 apply, 1 collector, 1 lost answer, S3", planOpts{applies: 1, collectors: 1}), dst.Options{Mode: dst.S3, Lost: 1}, limit, 3), false)
	// A request of the collector, or of the apply, is stalled for longer than the claim of a collector holds.
	publish(t, dst.Exhaust(t, planScenario("f: 1 apply, 1 collector, 1 request stalled 11 min, S3", planOpts{applies: 1, collectors: 1}), dst.Options{Mode: dst.S3, Stalls: 1}, limit, 3), false)
	// Clocks that disagree by more than the claim holds.
	for _, skew := range []time.Duration{11 * time.Minute, -11 * time.Minute} {
		publish(t, dst.Exhaust(t, planScenario(fmt.Sprintf("f: 1 apply with clock %+v, 1 collector, S3", skew), planOpts{applies: 1, collectors: 1, skew: map[int]time.Duration{0: skew}}), dst.Options{Mode: dst.S3}, limit, 3), false)
	}
	publish(t, dst.Random(t, planScenario("f: 2 applies, 2 collectors, S3, random, 1 crash + 1 lost answer", planOpts{applies: 2, collectors: 2}), dst.Options{Mode: dst.S3, Crashes: 1, Lost: 1}, 20261110, dst.Budget(1500, 20000, 400000), 4, 30, 80, 3, 0), false)
}
