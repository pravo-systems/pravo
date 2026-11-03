package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// crashBackend lets a number of writes through and fails the ones after, as a machine that
// goes down in the middle of a command does.
type crashBackend struct {
	state.Backend
	left atomic.Int64
	seen atomic.Int64
}

var errDown = errors.New("the machine went down")

func (b *crashBackend) allow() error {
	b.seen.Add(1)
	if b.left.Add(-1) < 0 {
		return errDown
	}
	return nil
}

func (b *crashBackend) Put(ctx context.Context, key string, data []byte) error {
	if err := b.allow(); err != nil {
		return err
	}
	return b.Backend.Put(ctx, key, data)
}

func (b *crashBackend) PutIfAbsent(ctx context.Context, key string, data []byte) (bool, error) {
	if err := b.allow(); err != nil {
		return false, err
	}
	return b.Backend.PutIfAbsent(ctx, key, data)
}

func (b *crashBackend) ReplaceIf(ctx context.Context, key string, update func([]byte) ([]byte, bool)) (bool, error) {
	if err := b.allow(); err != nil {
		return false, err
	}
	return b.Backend.ReplaceIf(ctx, key, update)
}

func (b *crashBackend) DeleteIf(ctx context.Context, key string, match func([]byte) bool) (bool, error) {
	if err := b.allow(); err != nil {
		return false, err
	}
	return b.Backend.DeleteIf(ctx, key, match)
}

func (b *crashBackend) Delete(ctx context.Context, key string) error {
	if err := b.allow(); err != nil {
		return err
	}
	return b.Backend.Delete(ctx, key)
}

// boundHarness is a sealed state whose readers give roles, so that every record is bound to
// the transaction that wrote it. The backend can be made to fail after a number of writes.
type boundHarness struct {
	*harness
	crash   *crashBackend
	id      *protect.Identity
	readers []protect.Reader
}

func newBoundHarness(t *testing.T) *boundHarness {
	t.Helper()
	unsealedStart(t)
	crash := &crashBackend{}
	crash.left.Store(1 << 40)
	h := newHarnessOver(t, func(b state.Backend) state.Backend { crash.Backend = b; return crash })
	id, err := protect.NewIdentity("ada", "")
	if err != nil {
		t.Fatal(err)
	}
	reader := id.AsReader()
	reader.Role = protect.RoleAdmin
	readers := []protect.Reader{reader}
	if _, err := h.state.Upgrade(context.Background(), state.UpgradeOptions{
		To: state.FormatSealed, Readers: readers, Identity: id, By: "ada",
		Plans: NewPlanStoreFor(h.state).SealAll,
	}); err != nil {
		t.Fatal(err)
	}
	b := &boundHarness{harness: h, crash: crash, id: id, readers: readers}
	b.open()
	return b
}

// open opens the state as the next command does, with the identity and the roles.
func (b *boundHarness) open() {
	b.t.Helper()
	st, err := state.OpenBackendWith(context.Background(), b.backend, state.Options{Identity: b.id})
	if err != nil {
		b.t.Fatal(err)
	}
	st.BindRecords(protect.RolesOn(b.readers))
	b.state = st
}

// verify opens the state again, with the backend working, and fails the test for a record
// that no transaction of a writer accounts for.
func (b *boundHarness) verify(what string) {
	b.t.Helper()
	b.crash.left.Store(1 << 40)
	b.open()
	txns, err := b.state.Transactions()
	if err != nil {
		b.t.Fatalf("%s: %v", what, err)
	}
	v := b.state.VerifyRecords(txns, b.readers)
	for _, p := range v.Problems {
		b.t.Errorf("%s: %s: %s", what, p.Addr, p.Why)
	}
}

// renamed makes an engine for the specification with a $from declaration.
func (b *boundHarness) renamed(s spec, from, to string) *Engine {
	e := b.engine(s, Options{Command: "pravo apply"})
	e.Root.Moves = []config.Move{{From: from, To: to}}
	return e
}

var (
	beforeMove = "a: " + thing("a", `content: "one"`) + "\n" +
		"b: " + thing("b", "content: a.content") + "\n"
	afterMove = "c: " + thing("a", `content: "one"`) + "\n" +
		"b: " + thing("b", "content: c.content") + "\n"
	afterMoveAndChange = "c: " + thing("a", `content: "two"`) + "\n" +
		"b: " + thing("b", "content: c.content") + "\n"
)

// A rename of a resource that others depend on rewrites the records of the dependents; the
// transaction names them (F20), and it is in the state before the first of them (F22).
func TestRenameKeepsEveryRecordBoundToATransaction(t *testing.T) {
	for _, tc := range []struct {
		name, cue string
		res       []string
	}{
		{"rename", afterMove, []string{"c", "b"}},
		{"rename and change", afterMoveAndChange, []string{"c", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newBoundHarness(t)
			b.apply(spec{cue: beforeMove, resources: []string{"a", "b"}})
			b.verify("after the first apply")
			e := b.renamed(spec{cue: tc.cue, resources: tc.res}, "a", "c")
			p := plan(t, e)
			if _, err := e.Apply(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			if b.state.Get("c") == nil || b.state.Get("a") != nil {
				t.Fatal("the record did not move")
			}
			b.verify("after the rename")
		})
	}
}

// Killed at any write of an apply that renames and changes, the state still verifies: a
// record is never in the state before the transaction that wrote it.
func TestRenameAndChangeKilledAtEveryWriteLeavesNoUnaccountedRecord(t *testing.T) {
	probe := newBoundHarness(t)
	probe.apply(spec{cue: beforeMove, resources: []string{"a", "b"}})
	writes := func() int64 {
		start := probe.crash.seen.Load()
		e := probe.renamed(spec{cue: afterMoveAndChange, resources: []string{"c", "b"}}, "a", "c")
		p := plan(t, e)
		if _, err := e.Apply(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		return probe.crash.seen.Load() - start
	}()
	if writes < 6 {
		t.Fatalf("an apply with a rename and a change made %d writes", writes)
	}
	for k := int64(0); k <= writes; k++ {
		b := newBoundHarness(t)
		b.apply(spec{cue: beforeMove, resources: []string{"a", "b"}})
		e := b.renamed(spec{cue: afterMoveAndChange, resources: []string{"c", "b"}}, "a", "c")
		p := plan(t, e)
		b.crash.left.Store(k)
		_, _ = e.Apply(context.Background(), p)
		b.verify(fmt.Sprintf("killed after write %d", k))
	}
}

// A refresh that rewrites records starts its transaction first, so a run that is killed
// leaves records whose transaction is in the state (F13).
func TestRefreshKilledAtEveryWriteLeavesNoUnaccountedRecord(t *testing.T) {
	cue := beforeMove
	res := []string{"a", "b"}
	probe := newBoundHarness(t)
	probe.apply(spec{cue: cue, resources: res})
	writes := func() int64 {
		start := probe.crash.seen.Load()
		e := probe.engine(spec{cue: cue, resources: res}, Options{Command: "pravo plan --refresh", Refresh: RefreshAll})
		plan(t, e)
		return probe.crash.seen.Load() - start
	}()
	if writes < 3 {
		t.Fatalf("a refreshing plan made %d writes", writes)
	}
	for k := int64(0); k <= writes; k++ {
		b := newBoundHarness(t)
		b.apply(spec{cue: cue, resources: res})
		e := b.engine(spec{cue: cue, resources: res}, Options{Command: "pravo plan --refresh", Refresh: RefreshAll})
		b.crash.left.Store(k)
		_, _ = e.Plan(context.Background())
		b.verify(fmt.Sprintf("killed after write %d", k))
	}
}

// A plan that reads nothing writes nothing: no record, and no transaction either.
func TestPlanWithoutRefreshWritesNothing(t *testing.T) {
	b := newBoundHarness(t)
	b.apply(spec{cue: beforeMove, resources: []string{"a", "b"}})
	before := b.crash.seen.Load()
	e := b.engine(spec{cue: beforeMove, resources: []string{"a", "b"}}, Options{Command: "pravo plan", Refresh: RefreshNone})
	plan(t, e)
	if got := b.crash.seen.Load() - before; got != 0 {
		t.Errorf("a plan that reads nothing made %d writes", got)
	}
}

// $from may list the addresses a record can have: the first that exists in the state moves,
// so one specification renames across environments that are at different steps of a chain.
func TestFromListMovesTheFirstAddressThatExists(t *testing.T) {
	for _, tc := range []struct {
		name     string
		have     string // the address the state has
		from     string
		also     []string
		wantMove bool
	}{
		{"the first address", "a", "a", []string{"b"}, true},
		{"a later address", "b", "a", []string{"b"}, true},
		{"already moved", "c", "a", []string{"b"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.apply(spec{cue: tc.have + ": " + thing("x", `content: "one"`) + "\n", resources: []string{tc.have}})
			id := string(h.state.Get(tc.have).Attributes)
			id = strings.Join(strings.Fields(id), "")
			e := h.engine(spec{cue: "c: " + thing("x", `content: "one"`) + "\n", resources: []string{"c"}}, Options{})
			e.Root.Moves = []config.Move{{From: tc.from, Also: tc.also, To: "c"}}
			p := plan(t, e)
			if got := p.Changes != nil && changeOf(t, p, "c").Action == ActionMove; got != tc.wantMove {
				t.Fatalf("a move is planned = %v, want %v", got, tc.wantMove)
			}
			if _, err := e.Apply(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			h.reopen()
			n := h.state.Get("c")
			if n == nil || h.state.Get(tc.have) != nil && tc.have != "c" {
				t.Fatalf("the record is not at c only: c=%v %s=%v", n != nil, tc.have, h.state.Get(tc.have) != nil)
			}
			if got := strings.Join(strings.Fields(string(n.Attributes)), ""); tc.wantMove && got != id {
				t.Errorf("the object changed: %s, was %s", got, id)
			}
		})
	}
}
