package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// offboard adds a second admin and then removes them again, which makes a new group of the
// state, the first step of the documented offboarding.
func (b *boundHarness) offboard(opts state.RewrapOptions) state.RewrapResult {
	b.t.Helper()
	bob, err := protect.NewIdentity("bob", "")
	if err != nil {
		b.t.Fatal(err)
	}
	br := bob.AsReader()
	br.Role = protect.RoleAdmin
	ctx := context.Background()
	if _, err := b.state.Rewrap(ctx, state.RewrapOptions{Readers: append(append([]protect.Reader{}, b.readers...), br), By: "ada"}); err != nil {
		b.t.Fatal(err)
	}
	opts.Readers, opts.By = b.readers, "ada"
	res, err := b.state.Rewrap(ctx, opts)
	if err != nil {
		b.t.Fatal(err)
	}
	return res
}

func (b *boundHarness) problems() []state.RecordProblem {
	b.t.Helper()
	b.open()
	txns, err := b.state.Transactions()
	if err != nil {
		b.t.Fatal(err)
	}
	return b.state.VerifyRecords(txns, b.readers).Problems
}

// C1: rewrap --all seals every record again, which changes its key. The records stay bound
// to a transaction of a writer: the rewrap signs the new keys.
func TestRewrapAllKeepsEveryRecordBound(t *testing.T) {
	b := newBoundHarness(t)
	b.applyBound(spec{cue: beforeMove, resources: []string{"a", "b"}})
	b.verify("before")
	if res := b.offboard(state.RewrapOptions{All: true}); res.Resealed != 2 {
		t.Fatalf("resealed %d records, want 2", res.Resealed)
	}
	b.verify("after rewrap --all")
	// Twice: nothing is left to seal, and nothing breaks.
	if _, err := b.state.Rewrap(context.Background(), state.RewrapOptions{Readers: b.readers, All: true, By: "ada"}); err != nil {
		t.Fatal(err)
	}
	b.verify("after rewrap --all again")
	// The state is still usable: a plan runs and an apply writes a record that names its own transaction.
	b.open()
	b.applyBound(spec{cue: afterMove2, resources: []string{"a", "b"}})
	b.verify("after an apply that follows")
}

var afterMove2 = "a: " + thing("a", `content: "two"`) + "\n" +
	"b: " + thing("b", "content: a.content") + "\n"

// A record that was sealed again without the rewrap signing it, as an earlier pravo did, is
// repaired by the next rewrap --all with the addresses named in Rebind, run by an admin.
func TestRewrapAllRepairsRecordsAnEarlierRewrapLeftUnsigned(t *testing.T) {
	b := newBoundHarness(t)
	b.applyBound(spec{cue: beforeMove, resources: []string{"a", "b"}})
	b.state.BindRecords(false) // the rewrap of an earlier pravo signed nothing
	b.offboard(state.RewrapOptions{All: true})
	got := b.problems()
	if len(got) != 2 || !strings.Contains(got[0].Why, "signed another record") {
		t.Fatalf("problems %+v, want two records no transaction names", got)
	}
	b.state.BindRecords(true)
	if _, err := b.state.Rewrap(context.Background(), state.RewrapOptions{Readers: b.readers, All: true, By: "ada", Rebind: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	b.verify("after the repair")
}

// The signature of the rewrap names the keys of the records it sealed, so a record that
// someone with the data key writes afterwards, carrying the name of the rewrap, is refused.
func TestRewrapAllDoesNotBlessARecordSomeoneElseSealed(t *testing.T) {
	b := newBoundHarness(t)
	b.applyBound(spec{cue: beforeMove, resources: []string{"a", "b"}})
	b.offboard(state.RewrapOptions{All: true})
	b.open()
	r := *b.state.Get("a")
	r.Attributes = []byte(`{"name":"a","id":"forged","content":"evil"}`)
	if err := b.state.Set(&r, ""); err != nil {
		t.Fatal(err)
	}
	got := b.problems()
	if len(got) != 1 || got[0].Addr != "a" {
		t.Fatalf("problems %+v, want the forged record only", got)
	}
}

// A secret is not a record: it is bound by its own commit, and rewrap --all leaves it valid.
func TestRewrapAllKeepsSecretsValid(t *testing.T) {
	b := newBoundHarness(t)
	ctx := context.Background()
	if _, err := b.state.SetSecret(ctx, "token", []byte("hunter2"), "", "pravo secret set"); err != nil {
		t.Fatal(err)
	}
	b.applyBound(spec{cue: beforeMove, resources: []string{"a", "b"}})
	b.offboard(state.RewrapOptions{All: true})
	b.open()
	txns, err := b.state.Transactions()
	if err != nil {
		t.Fatal(err)
	}
	v, err := b.state.VerifySecrets(ctx, txns, state.RecordRules{Readers: b.readers})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Problems) != 0 {
		t.Fatalf("secrets: %+v", v)
	}
	b.verify("records")
}

// applyBound applies as a writer of a state with roles, so that the transaction signs the
// records it writes.
func (b *boundHarness) applyBound(s spec) {
	b.t.Helper()
	e := b.engine(s, Options{Readers: b.readers})
	p, err := e.Plan(context.Background())
	if err != nil {
		b.t.Fatalf("plan: %v", err)
	}
	if _, err := e.Apply(context.Background(), p); err != nil {
		b.t.Fatalf("apply: %v", err)
	}
}
