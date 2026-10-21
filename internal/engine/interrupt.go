package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Interrupt connects the signal handling of a command to the apply that the
// command runs. The first signal stops the walk from starting tasks, and the
// tasks that a provider is working on finish and are recorded. The second asks
// the providers to stop. The third, or the end of the time the command allows
// them, gives the apply up: what was in flight is marked and the command exits.
// A command makes one, puts it in the context of the run with WithInterrupt, and
// calls its methods from the goroutine that receives the signals.
type Interrupt struct {
	mu      sync.Mutex
	runs    []*interruptible
	stopped bool
}

// interruptible is an apply the Interrupt can ask about and act on.
type interruptible struct {
	inFlight func() int
	stop     func()
	abandon  func() []string
}

type interruptKey struct{}

// WithInterrupt returns a context that carries i.
func WithInterrupt(ctx context.Context, i *Interrupt) context.Context {
	return context.WithValue(ctx, interruptKey{}, i)
}

func interruptFrom(ctx context.Context) *Interrupt {
	i, _ := ctx.Value(interruptKey{}).(*Interrupt)
	return i
}

// add registers a run and returns the function that takes it out again.
func (i *Interrupt) add(r *interruptible) (remove func()) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.runs = append(i.runs, r)
	return func() {
		i.mu.Lock()
		defer i.mu.Unlock()
		i.runs = slices.DeleteFunc(i.runs, func(x *interruptible) bool { return x == r })
	}
}

func (i *Interrupt) running() []*interruptible {
	i.mu.Lock()
	defer i.mu.Unlock()
	return slices.Clone(i.runs)
}

// InFlight is the number of changes that providers are making right now.
func (i *Interrupt) InFlight() int {
	n := 0
	for _, r := range i.running() {
		n += r.inFlight()
	}
	return n
}

// stopWait is how long a provider is given to answer the request to stop.
const stopWait = 10 * time.Second

// Stop asks the providers of the running applies to stop what they are doing.
// A provider that stops returns the state it had reached, and the apply records
// it.
func (i *Interrupt) Stop() {
	i.mu.Lock()
	i.stopped = true
	i.mu.Unlock()
	for _, r := range i.running() {
		r.stop()
	}
}

// Stopped reports whether the providers were asked to stop. It is false for a
// nil Interrupt.
func (i *Interrupt) Stopped() bool {
	if i == nil {
		return false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.stopped
}

// Abandon gives up the running applies without waiting for the providers. Every
// change still in flight is marked as one whose result is unknown, the
// transaction is written and the locks are released. It returns what it says of
// each, for the command to print. The caller exits afterwards.
func (i *Interrupt) Abandon() []string {
	var notes []string
	for _, r := range i.running() {
		notes = append(notes, r.abandon()...)
	}
	return notes
}

// register makes the apply known to the Interrupt that the context carries.
func (e *Engine) register(ctx context.Context, held *heldLocks) (remove func()) {
	i := interruptFrom(ctx)
	e.interrupt = i
	if i == nil {
		return func() {}
	}
	return i.add(&interruptible{
		inFlight: e.inFlight,
		stop: func() {
			ctx, cancel := context.WithTimeout(context.Background(), stopWait)
			defer cancel()
			e.Providers.StopAll(ctx)
		},
		abandon: func() []string { return e.abandon(held) },
	})
}

// fly notes that the provider of addr is making a change to its object: what is
// "creation", "update" or "destruction".
func (e *Engine) fly(addr, what string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.flying == nil {
		e.flying = map[string]string{}
	}
	e.flying[addr] = what
}

// land notes that the provider answered.
func (e *Engine) land(addr string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.flying, addr)
}

func (e *Engine) inFlight() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.flying)
}

// mutation says what a call to ApplyResourceChange does to the object.
func mutation(prior, planned tftypes.Value) string {
	switch {
	case planned.IsNull():
		return "destruction"
	case prior.IsNull():
		return "creation"
	}
	return "update"
}

// unsettle notes that the result of the change to addr is not known: the call to
// its provider failed, or the provider was asked to stop, before it said what it
// had done. The record of the node, if it has one, is written again with no
// refresh time, so that the next plan reads the object whatever the refresh age.
// A creation that has no record has none to write, and the note is what is left.
func (e *Engine) unsettle(addr, what string) {
	e.markUnsettled(addr, fmt.Sprintf("%s of %s was in flight; its result is unknown; check the provider", what, addr))
}

// unrecorded notes that the provider finished the change to addr and that the
// state could not be written to say so: the object is changed and the record is
// not. The record, if the node has one, is marked as unsettle marks it, and the
// note names the node in the error of the apply.
func (e *Engine) unrecorded(addr, what string) {
	e.markUnsettled(addr, fmt.Sprintf("%s of %s finished, but the state does not record it; check the provider", what, addr))
}

// markUnsettled keeps note for addr and writes its record again with no refresh
// time. The first note about a node is the one that is kept.
func (e *Engine) markUnsettled(addr, note string) {
	e.mu.Lock()
	if _, done := e.unsettled[addr]; done {
		e.mu.Unlock()
		return
	}
	if e.unsettled == nil {
		e.unsettled = map[string]string{}
	}
	e.unsettled[addr] = note
	e.mu.Unlock()
	before, after, found, err := e.State.Invalidate(addr, e.txnID())
	switch {
	case err != nil:
		e.Opts.Progress("Warning: %s: the record could not be marked for a read: %v", addr, err)
	case found && e.txn != nil:
		e.txn.Record(addr, "unsettled", before, after)
	}
}

// unsettledNotes returns what is said of the changes whose result is not known,
// in the order of their addresses.
func (e *Engine) unsettledNotes() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	notes := make([]string, 0, len(e.unsettled))
	for _, addr := range slices.Sorted(maps.Keys(e.unsettled)) {
		notes = append(notes, e.unsettled[addr])
	}
	return notes
}

// withUnsettled adds to the error of a failed apply the changes whose result is
// not known.
func (e *Engine) withUnsettled(err error) error {
	notes := e.unsettledNotes()
	if err == nil || len(notes) == 0 {
		return err
	}
	return fmt.Errorf("%w; %s", err, strings.Join(notes, "; "))
}

// abandon is what the apply does when the command gives it up: every change still
// in flight is unsettled, the transaction is written as failed and the locks are
// released. The providers are not waited for.
func (e *Engine) abandon(held *heldLocks) []string {
	e.mu.Lock()
	flying := maps.Clone(e.flying)
	e.mu.Unlock()
	for _, addr := range slices.Sorted(maps.Keys(flying)) {
		e.unsettle(addr, flying[addr])
	}
	notes := e.unsettledNotes()
	if e.txn != nil {
		_ = e.txn.Finish("failed", "interrupted; "+strings.Join(notes, "; "), true)
	}
	held.release()
	return notes
}
