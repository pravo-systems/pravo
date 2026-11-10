// Package dst is a deterministic explorer of the interleavings of commands that
// share one state. Each simulated process gets its own Backend over one object
// map. Every call of a process to its backend stops at a yield point until the
// scheduler grants it, so the scheduler decides the order of every request that
// reaches the store, and with testing/synctest it also owns the clock: time
// passes only when the scheduler lets it.
//
// The scheduler can also crash a process (it makes no further request), lose the
// answer of a write (the effect lands, the caller is told nothing), and let time
// pass while requests wait (a stalled process, a suspended laptop).
//
// Strategies: exhaustive depth-first search with replay and sleep sets (DFS), and
// random schedules with priority change points (PCT). A schedule is the list of
// choices it made, so a failure replays from its path or its seed.
package dst

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/state"
)

// Mode is how the backend makes ReplaceIf and DeleteIf.
type Mode int

const (
	// Atomic makes ReplaceIf and DeleteIf in one step, as the local backend does
	// under its flock and MemBackend under its mutex.
	Atomic Mode = iota
	// S3 makes them as the S3 backend does: a read of the object and its tag, then a
	// write conditional on the tag. Another request can land between the two. The
	// store also tells its own time with every read, as S3 does (see
	// state.TimedReader), unless Options.NoStoreTime is set.
	S3
)

// ErrCrashed is what every call of a crashed process returns. No request of a
// crashed process reaches the store.
var ErrCrashed = errors.New("dst: the process has crashed")

// ErrLost is what a write whose answer was lost returns when the backend has no
// better error for it (Put, Delete, DeleteIf).
var ErrLost = errors.New("dst: the answer of the store was lost")

type object struct {
	data []byte
	tag  int
	// written is when the store wrote the object, by the clock of the store: the
	// virtual time, with the skew of no process.
	written time.Time
}

// World is one run: the objects, the processes and the trace.
type World struct {
	T    *testing.T
	Mode Mode
	// TellsTime says that the store gives its own time with a read: the locks are
	// then judged by the clock of the store and not by the clocks of the processes.
	TellsTime bool

	mu      sync.Mutex
	objects map[string]object
	nextTag int
	procs   []*Proc
	trace   []string
	arrive  chan struct{}
	abort   bool
	direct  bool
	failure error
	// current is the process whose step runs.
	current atomic.Pointer[Proc]
	stall   time.Duration
	// Start is the virtual time at which the run began.
	Start time.Time
	// OnMutate, when set, is told of every change of an object: who made it, the
	// bytes before and after (nil for none), and when the store wrote the bytes
	// before (the zero time for none). It runs inside the step and must not call
	// the World; what it returns, if anything, is the violation of the run.
	OnMutate func(p *Proc, key string, before, after []byte, written, now time.Time) error
	// Data is for the scenario.
	Data any
}

// Proc is one simulated process.
type Proc struct {
	W       *World
	ID      int
	Name    string
	B       state.Backend
	pending []*call
	seq     map[string]int
	crashed bool
	done    bool
	// Skew is added to the time this process reads through Now.
	Skew time.Duration
}

type call struct {
	p     *Proc
	op    string
	key   string
	id    string
	write bool
	list  bool
	grant chan grant
}

type grant int

const (
	gRun grant = iota
	gLost
	gDead
	gSlow
)

// Fail records the first violation of the run.
func (w *World) Fail(format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failure == nil {
		w.failure = fmt.Errorf(format, args...)
	}
}

// Note adds a line to the trace.
func (w *World) Note(format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.trace = append(w.trace, fmt.Sprintf("  %8s  ", time.Since(w.Start).Round(time.Millisecond))+fmt.Sprintf(format, args...))
}

// Object reads an object without a yield, for checks.
func (w *World) Object(key string) ([]byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	o, ok := w.objects[key]
	return bytes.Clone(o.data), ok
}

// Keys lists the keys under prefix without a yield, for checks.
func (w *World) Keys(prefix string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.keysLocked(prefix)
}

func (w *World) keysLocked(prefix string) []string {
	var out []string
	for k := range w.objects {
		if prefix == "" || k == prefix || strings.HasPrefix(k, strings.TrimSuffix(prefix, "/")+"/") {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Clock is the clock of the process whose step runs: the virtual time and the
// skew of that process. A scenario gives it to the code under test.
func (w *World) Clock() time.Time {
	if p := w.current.Load(); p != nil {
		return time.Now().Add(p.Skew)
	}
	return time.Now()
}

// Jitter stands for the random part of a pause in a run: always half of the
// longest it can be, so that a schedule replays.
func (w *World) Jitter(d time.Duration) time.Duration { return d / 2 }

// Dead reports whether the process has crashed. A script asks before it records
// what the process believes.
func (p *Proc) Dead() bool {
	p.W.mu.Lock()
	defer p.W.mu.Unlock()
	return p.crashed || p.W.abort
}

// Observe is a step of the process that reads or changes something the store does
// not hold but that other processes' steps on key decide: the script calls it
// before it looks at the world to check an invariant, so that the search orders
// the look against every request on key. It returns false when the process is dead.
func (p *Proc) Observe(what, key string) bool {
	if p.yield("Observe:"+what, key, true, false) == gDead {
		return false
	}
	p.W.mu.Lock()
	p.W.log(p, "%s", what)
	p.W.mu.Unlock()
	return true
}

// Now is the clock of the process: the virtual time and its skew.
func (p *Proc) Now() time.Time { return time.Now().Add(p.Skew) }

// yield stops the calling goroutine until the scheduler grants the call.
func (p *Proc) yield(op, key string, write, list bool) grant {
	w := p.W
	w.mu.Lock()
	if w.direct {
		w.mu.Unlock()
		return gRun
	}
	if p.crashed || w.abort {
		w.mu.Unlock()
		return gDead
	}
	n := p.seq[op+" "+key]
	p.seq[op+" "+key] = n + 1
	c := &call{p: p, op: op, key: key, write: write, list: list, grant: make(chan grant, 1)}
	c.id = fmt.Sprintf("%s %s %s#%d", p.Name, op, key, n)
	p.pending = append(p.pending, c)
	w.mu.Unlock()
	select {
	case w.arrive <- struct{}{}:
	default:
	}
	g := <-c.grant
	if g != gSlow {
		return g
	}
	// The request is slow: the process waits while the others go on, and the
	// scheduler then orders the request among theirs as any other.
	w.mu.Lock()
	length := w.stall
	w.mu.Unlock()
	time.Sleep(length)
	w.mu.Lock()
	if p.crashed || w.abort {
		w.mu.Unlock()
		return gDead
	}
	c2 := &call{p: p, op: op, key: key, write: write, list: list, grant: make(chan grant, 1)}
	c2.id = c.id + " (late)"
	p.pending = append(p.pending, c2)
	w.mu.Unlock()
	select {
	case w.arrive <- struct{}{}:
	default:
	}
	return <-c2.grant
}

func (w *World) log(p *Proc, format string, args ...any) {
	w.trace = append(w.trace, fmt.Sprintf("  %8s  %s ", time.Since(w.Start).Round(time.Millisecond), p.Name)+fmt.Sprintf(format, args...))
}

// backend is the Backend of one process.
type backend struct{ p *Proc }

func (b backend) Name() string { return "dst://" + b.p.Name }
func (b backend) Local() bool  { return false }

func (b backend) Get(_ context.Context, key string) ([]byte, error) {
	if b.p.yield("Get", key, false, false) == gDead {
		return nil, ErrCrashed
	}
	w := b.p.W
	w.mu.Lock()
	defer w.mu.Unlock()
	o, ok := w.objects[key]
	if !ok {
		w.log(b.p, "Get %s -> not found", key)
		return nil, state.ErrNotFound
	}
	w.log(b.p, "Get %s -> tag %d", key, o.tag)
	return bytes.Clone(o.data), nil
}

// GetTimed is Get, and when the store tells its time, the time it wrote the object and
// the time it is: both by the clock of the store, which no process can move. It is the
// same request as Get to the scheduler.
func (b backend) GetTimed(_ context.Context, key string) ([]byte, state.StoreTime, error) {
	if b.p.yield("Get", key, false, false) == gDead {
		return nil, state.StoreTime{}, ErrCrashed
	}
	w := b.p.W
	w.mu.Lock()
	defer w.mu.Unlock()
	o, ok := w.objects[key]
	if !ok {
		w.log(b.p, "Get %s -> not found", key)
		return nil, state.StoreTime{}, state.ErrNotFound
	}
	var at state.StoreTime
	if w.TellsTime {
		at = state.StoreTime{Modified: o.written, Now: time.Now()}
		w.log(b.p, "Get %s -> tag %d, written %s ago", key, o.tag, at.Age().Round(time.Millisecond))
	} else {
		w.log(b.p, "Get %s -> tag %d", key, o.tag)
	}
	return bytes.Clone(o.data), at, nil
}

func (w *World) changed(p *Proc, key string, before *object, after []byte) {
	if w.OnMutate != nil && !w.direct {
		var data []byte
		var written time.Time
		if before != nil {
			data, written = before.data, before.written
		}
		if err := w.OnMutate(p, key, data, after, written, time.Now()); err != nil && w.failure == nil {
			w.failure = err
		}
	}
}

func (w *World) remove(p *Proc, key string) {
	before, ok := w.objects[key]
	delete(w.objects, key)
	if ok {
		w.changed(p, key, &before, nil)
	}
}

func (w *World) store(p *Proc, key string, data []byte) int {
	if before, ok := w.objects[key]; ok {
		w.changed(p, key, &before, data)
	} else {
		w.changed(p, key, nil, data)
	}
	w.nextTag++
	w.objects[key] = object{data: bytes.Clone(data), tag: w.nextTag, written: time.Now()}
	return w.nextTag
}

func (b backend) Put(_ context.Context, key string, data []byte) error {
	g := b.p.yield("Put", key, true, false)
	if g == gDead {
		return ErrCrashed
	}
	w := b.p.W
	w.mu.Lock()
	defer w.mu.Unlock()
	tag := w.store(b.p, key, data)
	if g == gLost {
		w.log(b.p, "Put %s -> tag %d, ANSWER LOST", key, tag)
		return ErrLost
	}
	w.log(b.p, "Put %s -> tag %d", key, tag)
	return nil
}

func (b backend) PutIfAbsent(_ context.Context, key string, data []byte) (bool, error) {
	g := b.p.yield("PutIfAbsent", key, true, false)
	if g == gDead {
		return false, ErrCrashed
	}
	w := b.p.W
	w.mu.Lock()
	defer w.mu.Unlock()
	_, exists := w.objects[key]
	if !exists {
		w.store(b.p, key, data)
	}
	if g == gLost {
		w.log(b.p, "PutIfAbsent %s -> created=%v, ANSWER LOST (unknown outcome)", key, !exists)
		return false, fmt.Errorf("%w: simulated", state.ErrUnknownOutcome)
	}
	w.log(b.p, "PutIfAbsent %s -> created=%v", key, !exists)
	return !exists, nil
}

func (b backend) Delete(_ context.Context, key string) error {
	g := b.p.yield("Delete", key, true, false)
	if g == gDead {
		return ErrCrashed
	}
	w := b.p.W
	w.mu.Lock()
	defer w.mu.Unlock()
	w.remove(b.p, key)
	if g == gLost {
		w.log(b.p, "Delete %s, ANSWER LOST", key)
		return ErrLost
	}
	w.log(b.p, "Delete %s", key)
	return nil
}

func (b backend) DeleteIf(_ context.Context, key string, match func([]byte) bool) (bool, error) {
	w := b.p.W
	if w.Mode == S3 && !w.isDirect() {
		// A read, then a delete conditional on the tag that was read.
		if b.p.yield("DeleteIf.read", key, false, false) == gDead {
			return false, ErrCrashed
		}
		w.mu.Lock()
		o, ok := w.objects[key]
		if !ok {
			w.log(b.p, "DeleteIf.read %s -> not found", key)
			w.mu.Unlock()
			return false, state.ErrNotFound
		}
		w.log(b.p, "DeleteIf.read %s -> tag %d", key, o.tag)
		w.mu.Unlock()
		if !match(bytes.Clone(o.data)) {
			return false, nil
		}
		g := b.p.yield("DeleteIf.delete", key, true, false)
		if g == gDead {
			return false, ErrCrashed
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		cur, ok := w.objects[key]
		switch {
		case !ok:
			w.log(b.p, "DeleteIf.delete %s if tag %d -> not found", key, o.tag)
			return false, state.ErrNotFound
		case cur.tag != o.tag:
			w.log(b.p, "DeleteIf.delete %s if tag %d -> refused, tag is %d", key, o.tag, cur.tag)
			return false, nil
		}
		w.remove(b.p, key)
		if g == gLost {
			w.log(b.p, "DeleteIf.delete %s if tag %d -> deleted, ANSWER LOST", key, o.tag)
			return false, ErrLost
		}
		w.log(b.p, "DeleteIf.delete %s if tag %d -> deleted", key, o.tag)
		return true, nil
	}
	g := b.p.yield("DeleteIf", key, true, false)
	if g == gDead {
		return false, ErrCrashed
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	o, ok := w.objects[key]
	if !ok {
		w.log(b.p, "DeleteIf %s -> not found", key)
		return false, state.ErrNotFound
	}
	if !match(bytes.Clone(o.data)) {
		w.log(b.p, "DeleteIf %s -> no match", key)
		return false, nil
	}
	w.remove(b.p, key)
	if g == gLost {
		w.log(b.p, "DeleteIf %s -> deleted, ANSWER LOST", key)
		return false, ErrLost
	}
	w.log(b.p, "DeleteIf %s -> deleted", key)
	return true, nil
}

func (w *World) isDirect() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.direct
}

func (b backend) ReplaceIf(_ context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	w := b.p.W
	if w.Mode == S3 && !w.isDirect() {
		if b.p.yield("ReplaceIf.read", key, false, false) == gDead {
			return false, ErrCrashed
		}
		w.mu.Lock()
		o, ok := w.objects[key]
		if !ok {
			w.log(b.p, "ReplaceIf.read %s -> not found", key)
			w.mu.Unlock()
			return false, state.ErrNotFound
		}
		w.log(b.p, "ReplaceIf.read %s -> tag %d", key, o.tag)
		w.mu.Unlock()
		next, yes := update(bytes.Clone(o.data))
		if !yes {
			return false, nil
		}
		g := b.p.yield("ReplaceIf.put", key, true, false)
		if g == gDead {
			return false, ErrCrashed
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		cur, ok := w.objects[key]
		switch {
		case !ok:
			w.log(b.p, "ReplaceIf.put %s if tag %d -> not found", key, o.tag)
			return false, state.ErrNotFound
		case cur.tag != o.tag:
			// The S3 backend answers a refusal of a write it sent once with false: a lost race.
			w.log(b.p, "ReplaceIf.put %s if tag %d -> refused, tag is %d", key, o.tag, cur.tag)
			if g == gLost {
				return false, fmt.Errorf("%w: simulated", state.ErrUnknownOutcome)
			}
			return false, nil
		}
		tag := w.store(b.p, key, next)
		if g == gLost {
			w.log(b.p, "ReplaceIf.put %s if tag %d -> tag %d, ANSWER LOST (unknown outcome)", key, o.tag, tag)
			return false, fmt.Errorf("%w: simulated", state.ErrUnknownOutcome)
		}
		w.log(b.p, "ReplaceIf.put %s if tag %d -> tag %d", key, o.tag, tag)
		return true, nil
	}
	g := b.p.yield("ReplaceIf", key, true, false)
	if g == gDead {
		return false, ErrCrashed
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	o, ok := w.objects[key]
	if !ok {
		w.log(b.p, "ReplaceIf %s -> not found", key)
		return false, state.ErrNotFound
	}
	next, yes := update(bytes.Clone(o.data))
	if !yes {
		w.log(b.p, "ReplaceIf %s -> refused by the caller", key)
		return false, nil
	}
	tag := w.store(b.p, key, next)
	if g == gLost {
		w.log(b.p, "ReplaceIf %s -> tag %d, ANSWER LOST (unknown outcome)", key, tag)
		return false, fmt.Errorf("%w: simulated", state.ErrUnknownOutcome)
	}
	w.log(b.p, "ReplaceIf %s -> tag %d", key, tag)
	return true, nil
}

func (b backend) List(_ context.Context, prefix string) ([]string, error) {
	if b.p.yield("List", prefix, false, true) == gDead {
		return nil, ErrCrashed
	}
	w := b.p.W
	w.mu.Lock()
	defer w.mu.Unlock()
	keys := w.keysLocked(prefix)
	w.log(b.p, "List %s -> %d keys", prefix, len(keys))
	return keys, nil
}

// Scenario is what a run plays.
type Scenario struct {
	Name string
	// Procs is how many processes there are.
	Procs int
	// Setup runs first, alone and without yields: it opens the stores and writes
	// what the run starts from. It returns the script of each process.
	Setup func(w *World, procs []*Proc) []func(p *Proc)
	// Step is checked after every step, End when every process has finished.
	Step func(w *World) error
	End  func(w *World) error
}

// Options bound a run.
type Options struct {
	Mode Mode
	// NoStoreTime makes a store in the mode S3 tell no time with a read, as a
	// compatible store that sends no Last-Modified: the locks are then judged by the
	// clocks of the processes.
	NoStoreTime bool
	// Crashes, Lost and Stalls are how many of each fault one run may have.
	Crashes, Lost, Stalls int
	// StallList, when set, gives each stall of a run its own length, in the order
	// they are taken, and Stalls is its length.
	StallList []time.Duration
	// Stall is how long a stalled request waits before it reaches the store: the
	// process is suspended, or its connection hangs, while the others go on.
	Stall time.Duration
	// MaxSteps ends a run that makes more steps: a livelock.
	MaxSteps int
	// NoCrash names processes that never crash; NoStallBefore keeps stalls for
	// later than the given step (to cut the space).
	NoCrash map[int]bool
}

// chooser picks one of the options at a decision point. It returns -1 to end the
// run (sleep-set blocked).
type chooser interface {
	choose(opts []option) int
}

type option struct {
	id   string
	kind string // "call", "lost", "crash", "slow"
	c    *call
	p    *Proc
}

// Result is what one run found.
type Result struct {
	Err   error
	Trace []string
	Path  []string
	Steps int
	// Pruned is set when the search cut the run: every option was in the sleep set.
	Pruned bool
}

// runOnce plays the scenario once under the chooser.
func runOnce(t *testing.T, sc Scenario, o Options, ch chooser) Result {
	var res Result
	if o.MaxSteps == 0 {
		o.MaxSteps = 2000
	}
	if o.Stall == 0 {
		o.Stall = state.StaleAfter + time.Minute
	}
	if len(o.StallList) > 0 {
		o.Stalls = len(o.StallList)
	}
	watch := time.AfterFunc(60*time.Second, func() {
		panic("dst: a run did not finish in 60 s of real time (a goroutine blocked on a mutex held across a yield?): " + sc.Name)
	})
	defer watch.Stop()
	synctest.Test(t, func(t *testing.T) {
		r := &run{sc: sc, o: o, res: &res, crashes: o.Crashes, lost: o.Lost, stalls: o.Stalls}
		r.start(t)
		r.play(ch)
		r.finish()
	})
	return res
}

// run is one run of a scenario: its world, and the faults it may still have.
type run struct {
	w                     *World
	sc                    Scenario
	o                     Options
	res                   *Result
	wg                    sync.WaitGroup
	crashes, lost, stalls int
}

// start makes the world and starts the script of each process, which runs to its
// first request.
func (r *run) start(t *testing.T) {
	w := &World{T: t, Mode: r.o.Mode, TellsTime: r.o.Mode == S3 && !r.o.NoStoreTime, stall: r.o.Stall, objects: map[string]object{}, arrive: make(chan struct{}, 1), direct: true, Start: time.Now()}
	r.w = w
	for i := range r.sc.Procs {
		p := &Proc{W: w, ID: i, Name: fmt.Sprintf("P%d", i), seq: map[string]int{}}
		p.B = backend{p}
		w.procs = append(w.procs, p)
	}
	scripts := r.sc.Setup(w, w.procs)
	w.mu.Lock()
	w.direct = false
	w.trace = nil
	w.mu.Unlock()
	for i, s := range scripts {
		r.wg.Add(1)
		w.current.Store(w.procs[i])
		go func(p *Proc, s func(*Proc)) {
			defer r.wg.Done()
			s(p)
			w.mu.Lock()
			p.done = true
			w.mu.Unlock()
			select {
			case w.arrive <- struct{}{}:
			default:
			}
		}(w.procs[i], s)
		// One process at a time runs to its first request, so that the clock it reads is its own.
		synctest.Wait()
	}
}

// play grants one request at a time, as the chooser picks, until the run ends.
func (r *run) play(ch chooser) {
	for {
		synctest.Wait()
		opts, calls, alive, failure := r.options()
		if failure != nil {
			r.res.Err = failure
			return
		}
		if alive == 0 {
			return
		}
		if calls == 0 {
			if !r.idle(alive) {
				return
			}
			continue
		}
		if r.res.Steps >= r.o.MaxSteps {
			r.res.Err = fmt.Errorf("livelock: the run made %d steps and has not ended", r.res.Steps)
			return
		}
		i := ch.choose(opts)
		if i < 0 {
			r.res.Pruned = true
			return
		}
		r.res.Path = append(r.res.Path, opts[i].id)
		r.res.Steps++
		r.take(opts[i])
		if r.sc.Step != nil {
			synctest.Wait()
			if err := r.sc.Step(r.w); err != nil && r.res.Err == nil {
				r.res.Err = err
				return
			}
		}
	}
}

// options lists what the scheduler can do next: grant one of the requests that
// wait, lose the answer of a write, crash a process, or stall a request. calls is
// how many of them are requests, and alive how many processes have not ended.
func (r *run) options() (opts []option, calls, alive int, failure error) {
	w := r.w
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failure != nil {
		return nil, 0, 0, w.failure
	}
	for _, p := range w.procs {
		if p.crashed {
			continue
		}
		if !p.done {
			alive++
		}
		sort.Slice(p.pending, func(i, j int) bool { return p.pending[i].id < p.pending[j].id })
		for _, c := range p.pending {
			opts = append(opts, option{id: c.id, kind: "call", c: c, p: p})
		}
	}
	calls = len(opts)
	if r.lost > 0 {
		for _, op := range opts[:calls] {
			if op.c.write && !strings.HasPrefix(op.c.op, "Observe:") {
				opts = append(opts, option{id: "LOST " + op.c.id, kind: "lost", c: op.c, p: op.p})
			}
		}
	}
	if r.crashes > 0 {
		for _, p := range w.procs {
			if !p.crashed && !p.done && len(p.pending) > 0 && !r.o.NoCrash[p.ID] {
				opts = append(opts, option{id: "CRASH " + p.Name, kind: "crash", p: p})
			}
		}
	}
	if r.stalls > 0 {
		for _, op := range opts[:calls] {
			if !strings.HasSuffix(op.c.id, "(late)") && !strings.HasPrefix(op.c.op, "Observe:") {
				opts = append(opts, option{id: "SLOW " + op.c.id, kind: "slow", c: op.c, p: op.p})
			}
		}
	}
	return opts, calls, alive, nil
}

// idle is the step of a run in which every process that lives sleeps: time passes
// until one wakes. It reports whether the run goes on.
func (r *run) idle(alive int) bool {
	w := r.w
	select {
	case <-w.arrive:
	default:
	}
	synctest.Wait()
	w.mu.Lock()
	moved := false
	for _, p := range w.procs {
		moved = moved || len(p.pending) > 0
	}
	w.mu.Unlock()
	if moved {
		return true
	}
	select {
	case <-w.arrive:
		return true
	case <-time.After(24 * time.Hour):
		r.res.Err = fmt.Errorf("deadlock: %d processes neither finish nor ask the store for anything in a day", alive)
		return false
	}
}

// take does what the chooser picked.
func (r *run) take(op option) {
	w := r.w
	if op.kind == "crash" {
		r.crashes--
		w.mu.Lock()
		op.p.crashed = true
		w.trace = append(w.trace, fmt.Sprintf("  %8s  %s CRASHES", time.Since(w.Start).Round(time.Millisecond), op.p.Name))
		for _, c := range op.p.pending {
			c.grant <- gDead
		}
		op.p.pending = nil
		w.mu.Unlock()
		return
	}
	w.mu.Lock()
	for j, c := range op.p.pending {
		if c == op.c {
			// A new list: the one that is replaced is not written to.
			op.p.pending = slices.Concat(op.p.pending[:j], op.p.pending[j+1:])
			break
		}
	}
	w.current.Store(op.p)
	w.mu.Unlock()
	switch op.kind {
	case "lost":
		r.lost--
		op.c.grant <- gLost
	case "slow":
		length := r.o.Stall
		if len(r.o.StallList) > 0 {
			length = r.o.StallList[len(r.o.StallList)-r.stalls]
		}
		r.stalls--
		w.mu.Lock()
		w.stall = length
		w.trace = append(w.trace, fmt.Sprintf("  %8s  %s STALLS for %s before %s %s reaches the store", time.Since(w.Start).Round(time.Millisecond), op.p.Name, length, op.c.op, op.c.key))
		w.mu.Unlock()
		op.c.grant <- gSlow
	default:
		op.c.grant <- gRun
	}
}

// finish ends the processes that still wait, runs the check of the end of the
// scenario and takes the trace.
func (r *run) finish() {
	w := r.w
	w.mu.Lock()
	w.abort = true
	for _, p := range w.procs {
		for _, c := range p.pending {
			c.grant <- gDead
		}
		p.pending = nil
	}
	w.mu.Unlock()
	r.wg.Wait()
	w.mu.Lock()
	if r.res.Err == nil && w.failure != nil {
		r.res.Err = w.failure
	}
	w.mu.Unlock()
	if r.res.Err == nil && !r.res.Pruned && r.sc.End != nil {
		w.mu.Lock()
		w.direct = true
		w.abort = false
		w.mu.Unlock()
		r.res.Err = r.sc.End(w)
	}
	w.mu.Lock()
	r.res.Trace = append([]string(nil), w.trace...)
	w.mu.Unlock()
}

// Reopen lets a check use a backend after the run: the yields are off.
func (w *World) Reopen() state.Backend {
	p := &Proc{W: w, ID: -1, Name: "check", seq: map[string]int{}}
	return backend{p}
}

// --- replay ---

type replay struct {
	path []string
	i    int
	bad  string
}

func (r *replay) choose(opts []option) int {
	if r.i >= len(r.path) {
		return 0
	}
	want := r.path[r.i]
	r.i++
	for i, o := range opts {
		if o.id == want {
			return i
		}
	}
	r.bad = want
	return 0
}

// Replay plays the scenario along a recorded path.
func Replay(t *testing.T, sc Scenario, o Options, path []string) Result {
	r := &replay{path: path}
	res := runOnce(t, sc, o, r)
	if r.bad != "" && res.Err == nil {
		res.Err = fmt.Errorf("the replay left the path at %q: the scenario is not deterministic", r.bad)
	}
	return res
}

// --- exhaustive search with sleep sets ---

type frame struct {
	ids   []string
	idx   int
	sleep map[string]bool
	tried []string
	kinds map[string]optInfo
}

type optInfo struct {
	kind  string
	op    string
	key   string
	write bool
	list  bool
}

func infoOf(o option) optInfo {
	if o.kind != "call" {
		return optInfo{kind: o.kind}
	}
	return optInfo{kind: "call", op: o.c.op, key: o.c.key, write: o.c.write, list: o.c.list}
}

// independent reports whether two steps commute: neither changes what the other
// reads or does. Faults and stalls commute with nothing.
func independent(a, b optInfo) bool {
	if NoReduce {
		return false
	}
	if a.kind != "call" || b.kind != "call" {
		return false
	}
	if !a.write && !b.write {
		return true
	}
	under := func(l, k optInfo) bool {
		return l.key == "" || k.key == l.key || strings.HasPrefix(k.key, strings.TrimSuffix(l.key, "/")+"/")
	}
	if a.list {
		return !under(a, b)
	}
	if b.list {
		return !under(b, a)
	}
	return a.key != b.key
}

// NoReduce turns the sleep sets off: every order of every pair of steps is played.
// It is for checking the reduction against the plain search.
var NoReduce = env.OS().Get("DST_NOREDUCE") != ""

type dfs struct {
	stack []*frame
	depth int
	// nondet is set when a replayed prefix met other options than it recorded.
	nondet string
}

func (d *dfs) choose(opts []option) int {
	ids := make([]string, len(opts))
	for i, o := range opts {
		ids[i] = o.id
	}
	if d.depth < len(d.stack) {
		f := d.stack[d.depth]
		d.depth++
		want := f.ids[f.idx]
		for i, id := range ids {
			if id == want {
				return i
			}
		}
		d.nondet = fmt.Sprintf("depth %d: wanted %q among %v", d.depth-1, want, ids)
		return -1
	}
	f := &frame{ids: ids, sleep: map[string]bool{}, kinds: map[string]optInfo{}}
	for _, o := range opts {
		f.kinds[o.id] = infoOf(o)
	}
	if d.depth > 0 {
		par := d.stack[d.depth-1]
		chosen := par.kinds[par.ids[par.idx]]
		carry := func(id string) {
			if in, ok := par.kinds[id]; ok && independent(in, chosen) {
				f.sleep[id] = true
			}
		}
		for id := range par.sleep {
			carry(id)
		}
		for _, id := range par.tried {
			carry(id)
		}
	}
	f.idx = -1
	for i, id := range ids {
		if !f.sleep[id] {
			f.idx = i
			break
		}
	}
	if f.idx < 0 {
		return -1
	}
	d.stack = append(d.stack, f)
	d.depth++
	return f.idx
}

// next moves the search to the next path and reports whether there is one.
func (d *dfs) next() bool {
	d.depth = 0
	for len(d.stack) > 0 {
		f := d.stack[len(d.stack)-1]
		f.tried = append(f.tried, f.ids[f.idx])
		found := false
		for i := f.idx + 1; i < len(f.ids); i++ {
			if !f.sleep[f.ids[i]] {
				f.idx = i
				found = true
				break
			}
		}
		if found {
			return true
		}
		d.stack = d.stack[:len(d.stack)-1]
	}
	return false
}

// Report is what a search found.
type Report struct {
	Scenario string
	Runs     int // complete interleavings played
	Pruned   int // runs cut by the sleep sets
	Complete bool
	MaxSteps int
	Failures []Result
	Elapsed  time.Duration
	Seed     uint64
}

func (r Report) String() string {
	how := "exhaustive"
	if !r.Complete {
		how = "bounded"
	}
	if r.Seed != 0 {
		how = fmt.Sprintf("random, seed %d", r.Seed)
	}
	return fmt.Sprintf("%-46s %9d runs (%s; %d cut by sleep sets; longest %d steps) %d failing, %s",
		r.Scenario, r.Runs, how, r.Pruned, r.MaxSteps, len(r.Failures), r.Elapsed.Round(time.Millisecond))
}

// Exhaust plays every interleaving of the scenario, up to maxRuns, and stops at
// keep failures.
func Exhaust(t *testing.T, sc Scenario, o Options, maxRuns, keep int) Report {
	began := time.Now()
	d := &dfs{}
	rep := Report{Scenario: sc.Name}
	for {
		res := runOnce(t, sc, o, d)
		if d.nondet != "" {
			t.Fatalf("%s: the scenario is not deterministic: %s", sc.Name, d.nondet)
		}
		if res.Pruned {
			rep.Pruned++
		} else {
			rep.Runs++
		}
		rep.MaxSteps = max(rep.MaxSteps, res.Steps)
		if res.Err != nil {
			rep.Failures = append(rep.Failures, res)
			if len(rep.Failures) >= keep {
				break
			}
		}
		if !d.next() {
			rep.Complete = true
			break
		}
		if maxRuns > 0 && rep.Runs+rep.Pruned >= maxRuns {
			break
		}
	}
	rep.Elapsed = time.Since(began)
	return rep
}

// --- random schedules with priority change points (PCT) ---

type pct struct {
	rng    *rand.Rand
	prio   map[string]int
	change map[int]bool
	step   int
	low    int
	// fault is the chance, in a thousand, that a step is a fault when one is offered.
	fault int
}

func (p *pct) choose(opts []option) int {
	p.step++
	var faults []int
	best, bestPrio := -1, 0
	for i, o := range opts {
		if o.kind != "call" {
			faults = append(faults, i)
			continue
		}
		name := o.p.Name
		if _, ok := p.prio[name]; !ok {
			p.prio[name] = 1000 + p.rng.IntN(1000)
		}
		if best < 0 || p.prio[name] > bestPrio {
			best, bestPrio = i, p.prio[name]
		}
	}
	if len(faults) > 0 && p.rng.IntN(1000) < p.fault {
		return faults[p.rng.IntN(len(faults))]
	}
	if p.change[p.step] {
		// The process that runs is put behind all the others.
		p.low--
		p.prio[opts[best].p.Name] = p.low
	}
	return best
}

// Random plays runs random schedules: each gives the processes random priorities
// and lowers the running one at depth-1 random steps (PCT), and takes a fault
// with the given chance per step. The seed of a failing run is in its Result path.
func Random(t *testing.T, sc Scenario, o Options, seed uint64, runs, depth, faultPerMille, horizon, keep int, budget time.Duration) Report {
	began := time.Now()
	rep := Report{Scenario: sc.Name, Seed: seed}
	for i := range runs {
		if budget > 0 && time.Since(began) > budget {
			break
		}
		rng := rand.New(rand.NewPCG(seed, uint64(i)))
		p := &pct{rng: rng, prio: map[string]int{}, change: map[int]bool{}, fault: faultPerMille}
		for range depth - 1 {
			p.change[1+rng.IntN(horizon)] = true
		}
		res := runOnce(t, sc, o, p)
		rep.Runs++
		rep.MaxSteps = max(rep.MaxSteps, res.Steps)
		if res.Err != nil {
			res.Err = fmt.Errorf("%w (seed %d, run %d)", res.Err, seed, i)
			rep.Failures = append(rep.Failures, res)
			if len(rep.Failures) >= keep {
				break
			}
		}
	}
	rep.Elapsed = time.Since(began)
	return rep
}

// Shrink tries to shorten a failing path: it replays the path with one choice
// removed at a time and keeps what still fails with the same kind of error.
func Shrink(t *testing.T, sc Scenario, o Options, res Result, same func(error) bool) Result {
	best := res
	for changed := true; changed; {
		changed = false
		for i := range len(best.Path) {
			path := append(append([]string(nil), best.Path[:i]...), best.Path[i+1:]...)
			r := &replay{path: path}
			got := runOnce(t, sc, o, r)
			if got.Err != nil && same(got.Err) && len(got.Path) < len(best.Path) {
				best = got
				changed = true
				break
			}
		}
	}
	return best
}

// Describe prints a failure.
func Describe(res Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "VIOLATION: %v\n", res.Err)
	fmt.Fprintf(&b, "trace (%d steps):\n", res.Steps)
	for _, l := range res.Trace {
		b.WriteString(l + "\n")
	}
	fmt.Fprintf(&b, "path: %q\n", res.Path)
	return b.String()
}

// Full reports whether the searches that take more than a few seconds run: the
// variable DST_BUDGET is "full" or "big". Without it a test run plays the short
// ones, which is every scenario at its smallest size.
func Full() bool { return env.OS().Get("DST_BUDGET") == "full" || Big() }

// Big reports whether the bounded searches run with their large limits.
func Big() bool { return env.OS().Get("DST_BUDGET") == "big" }

// Budget picks the size of a search by DST_BUDGET: short in a run of the tests,
// full when it is "full" (the nightly run), big when it is "big" (a search of
// hours).
func Budget(short, full, big int) int {
	switch {
	case Big():
		return big
	case Full():
		return full
	}
	return short
}

// Publish logs a search and fails the test when a schedule of it failed, unless
// the search shows a limit that is known and documented: knownLimit then names
// it in the log. With DST_OUT set it also appends the search to summary.txt
// in that directory and saves the traces of the failures there.
func Publish(t *testing.T, rep Report, knownLimit bool) {
	t.Helper()
	t.Logf("DST %s", rep)
	if dir := env.OS().Get("DST_OUT"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		if f, err := os.OpenFile(filepath.Join(dir, "summary.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(f, "%s\n", rep)
			f.Close()
		}
		for i, r := range rep.Failures {
			name := strings.NewReplacer(" ", "_", "/", "_", ",", "", "(", "", ")", "", ":", "").Replace(rep.Scenario)
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s.%d.trace.txt", name, i)), []byte(Describe(r)), 0o644)
		}
	}
	switch {
	case len(rep.Failures) == 0:
	case knownLimit:
		t.Logf("a known limit, %s: %v", rep.Scenario, rep.Failures[0].Err)
	default:
		t.Errorf("%s: %d failing schedules; the first:\n%s", rep.Scenario, len(rep.Failures), Describe(rep.Failures[0]))
	}
}
