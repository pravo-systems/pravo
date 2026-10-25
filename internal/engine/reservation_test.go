package engine

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// reserveRun is one pravo command run by an actor ("" for none) against the
// store in dir, with its own lock identity as a separate process has.
func reserveRun(t *testing.T, dir, actor string) *Engine {
	t.Helper()
	st, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.SetActor(actor, "", "")
	return &Engine{State: st, Opts: Options{Actor: actor, Command: "pravo plan", Progress: func(string, ...any) {}}}
}

func pendingPlan(addrs ...string) *Plan {
	p := &Plan{}
	for _, a := range addrs {
		p.Changes = append(p.Changes, &Change{Addr: a, Mode: "managed", Action: ActionUpdate})
	}
	return p
}

func exitCodeOf(err error) ExitCode {
	var c *CodedError
	if errors.As(err, &c) {
		return c.Code
	}
	return ExitOK
}

func reservationsOf(t *testing.T, e *Engine) map[string]state.Lock {
	t.Helper()
	locks, err := e.State.Locks()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]state.Lock{}
	for _, l := range locks {
		out[l.Addr] = l
	}
	return out
}

// Point 1: a reservation taken without an actor label is the invoker's own.
func TestUnlabelledInvokerIsNotLockedOutByTheirReservation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "st")
	first := reserveRun(t, dir, "")
	if first.State.Owner() == "" {
		t.Skip("no operating system user")
	}
	first.Opts.Reserve = 15 * time.Minute
	plan := pendingPlan("random_pet.a")
	if err := first.reserve(plan); err != nil {
		t.Fatal(err)
	}

	again := reserveRun(t, dir, "")
	again.Opts.Reserve = 15 * time.Minute
	if err := again.checkReservations(plan); err != nil {
		t.Errorf("plan by the same user: %v", err)
	}
	if err := again.reserve(plan); err != nil {
		t.Errorf("second plan -reserve by the same user: %v", err)
	}
	// Other actors are still told to wait.
	if err := reserveRun(t, dir, "bob").checkReservations(plan); exitCodeOf(err) != ExitLocked {
		t.Errorf("plan by another actor: %v", err)
	}
	bob := reserveRun(t, dir, "bob")
	bob.Opts.Reserve = time.Minute
	if err := bob.reserve(pendingPlan("random_pet.b", "random_pet.a")); exitCodeOf(err) != ExitLocked {
		t.Errorf("reserve by another actor: %v", err)
	}

	held, err := again.lockAll(plan)
	if err != nil {
		t.Fatalf("apply by the same user: %v", err)
	}
	held.release()
}

// Point 2: a refresh writes the record and leaves the owner's reservation as
// it is, whether or not the node is in the state, and writes nothing on a node
// that another actor reserved.
func TestRefreshDoesNotEndTheOwnersReservation(t *testing.T) {
	for _, actor := range []string{"agent", ""} {
		dir := filepath.Join(t.TempDir(), "st")
		owner := reserveRun(t, dir, actor)
		if owner.State.Owner() == "" {
			t.Skip("no operating system user")
		}
		old := state.Now()
		rec := &state.Resource{Mode: "managed", Type: "random_pet", Name: "a", Attributes: json.RawMessage(`{"id":"x"}`), RefreshedAt: old}
		if err := owner.State.Set(rec, ""); err != nil {
			t.Fatal(err)
		}
		owner.Opts.Reserve = time.Hour
		if err := owner.reserve(pendingPlan("random_pet.a", "random_pet.new")); err != nil {
			t.Fatal(err)
		}
		before := reservationsOf(t, owner)

		plain := reserveRun(t, dir, actor) // a plain plan: no -reserve
		plain.persistRefreshed("random_pet.a", owner.State.Get("random_pet.a"), nil, []byte(`{"id":"y"}`), nil)
		got := plain.State.Get("random_pet.a")
		if !jsonEqual(got.Attributes, []byte(`{"id":"y"}`)) {
			t.Errorf("actor %q: the refreshed record was not written: %s", actor, got.Attributes)
		}
		after := reservationsOf(t, owner)
		for _, a := range []string{"random_pet.a", "random_pet.new"} {
			b, ok := before[a]
			if !ok || after[a].Nonce != b.Nonce || after[a].Kind != "reservation" || !after[a].Expires.Equal(*b.Expires) {
				t.Errorf("actor %q: reservation of %s changed by the refresh: before %+v, after %+v", actor, a, b, after[a])
			}
		}

		// Another actor's refresh of a reserved node writes nothing.
		bob := reserveRun(t, dir, "bob")
		bob.persistRefreshed("random_pet.a", plain.State.Get("random_pet.a"), nil, []byte(`{"id":"z"}`), nil)
		reread := reserveRun(t, dir, "bob")
		if got := reread.State.Get("random_pet.a"); !jsonEqual(got.Attributes, []byte(`{"id":"y"}`)) {
			t.Errorf("a reserved record was refreshed by another actor: %s", got.Attributes)
		}

		// An apply of the node by the owner ends the reservation.
		held, err := plain.lockAll(pendingPlan("random_pet.a"))
		if err != nil {
			t.Fatal(err)
		}
		held.release()
		if l, ok := reservationsOf(t, owner)["random_pet.a"]; ok {
			t.Errorf("actor %q: the apply left a lock on a: %+v", actor, l)
		}
		if _, ok := reservationsOf(t, owner)["random_pet.new"]; !ok {
			t.Errorf("actor %q: the apply of a ended the reservation of another node", actor)
		}
	}
}

// Point 3: a repeat plan -reserve extends the reservation and never shortens
// it, and the plan reports the earliest end among its nodes.
func TestRepeatReserveExtendsAndNeverShortens(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "st")
	var events []Event
	e := reserveRun(t, dir, "agent")
	e.Opts.Event = func(ev Event) { events = append(events, ev) }

	e.Opts.Reserve = 30 * time.Minute
	first := pendingPlan("a")
	if err := e.reserve(first); err != nil {
		t.Fatal(err)
	}
	thirty := reservationsOf(t, e)["a"].Expires.Add(0)

	// Shorter: a is kept to its end; b is new and ends sooner.
	again := reserveRun(t, dir, "agent")
	again.Opts.Event = e.Opts.Event
	again.Opts.Reserve = time.Minute
	events = nil
	second := pendingPlan("a", "b")
	if err := again.reserve(second); err != nil {
		t.Fatal(err)
	}
	got := reservationsOf(t, again)
	if !got["a"].Expires.Equal(thirty) {
		t.Errorf("a: reservation shortened from %v to %v", thirty, got["a"].Expires)
	}
	if d := time.Until(*got["b"].Expires); d > time.Minute || d < 50*time.Second {
		t.Errorf("b: reserved for %v, want the minute asked for", d)
	}
	if !second.Reservation.Until.Equal(*got["b"].Expires) {
		t.Errorf("the plan reports %v, want the earliest end %v", second.Reservation.Until, got["b"].Expires)
	}
	if len(events) != 2 || events[0].Addr != "a" || events[0].Message != "reserved until "+thirty.UTC().Format(time.RFC3339) {
		t.Errorf("events = %+v", events)
	}
	if got["a"].Command != "pravo plan" {
		t.Errorf("command = %q", got["a"].Command)
	}

	// Only a: nothing is earlier than its old end, so that is what is reported.
	only := pendingPlan("a")
	if err := again.reserve(only); err != nil {
		t.Fatal(err)
	}
	if !only.Reservation.Until.Equal(thirty) {
		t.Errorf("the plan reports %v, want %v", only.Reservation.Until, thirty)
	}

}

// Point 4: a reserve that fails part-way leaves the actor with what it held
// before the plan, and takes nothing else.
func TestFailedReserveKeepsWhatTheActorHeldBefore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "st")
	e := reserveRun(t, dir, "agent")
	e.Opts.Reserve = 30 * time.Minute
	if err := e.reserve(pendingPlan("a")); err != nil {
		t.Fatal(err)
	}
	before := reservationsOf(t, e)["a"]

	// c is being applied by another actor.
	other := reserveRun(t, dir, "bob")
	if err := other.State.Lock("c", "pravo apply", ""); err != nil {
		t.Fatal(err)
	}
	defer other.State.Unlock("c")

	again := reserveRun(t, dir, "agent")
	again.Opts.Reserve = 2 * time.Hour
	plan := pendingPlan("a", "b", "c", "d")
	err := again.reserve(plan)
	if exitCodeOf(err) != ExitLocked {
		t.Fatalf("reserve = %v, want exit code %d", err, ExitLocked)
	}
	if plan.Reservation != nil {
		t.Errorf("the plan reports a reservation: %+v", plan.Reservation)
	}
	got := reservationsOf(t, again)
	if a := got["a"]; a.Kind != "reservation" || !a.Expires.Equal(*before.Expires) || a.Nonce != before.Nonce {
		t.Errorf("a: before %+v, after %+v", before, a)
	}
	for _, addr := range []string{"b", "d"} {
		if l, ok := got[addr]; ok {
			t.Errorf("%s was left reserved by the failed attempt: %+v", addr, l)
		}
	}
	if got["c"].Actor != "bob" {
		t.Errorf("c = %+v", got["c"])
	}
}
