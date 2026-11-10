package state

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// applied records a finished transaction that sets the nodes at addrs.
func applied(t *testing.T, s *Store, addrs ...string) {
	t.Helper()
	tx := s.Begin("apply", "pravo apply")
	if err := tx.Start(); err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		r := &Resource{Addr: a, Mode: "managed", Type: "t", Name: a[strings.LastIndex(a, ".")+1:], Attributes: []byte(`{}`)}
		if err := s.Set(r, tx.ID()); err != nil {
			t.Fatal(err)
		}
		tx.Record(a, "create", 0, r.Revision)
	}
	if err := tx.FinishError("applied", "", "", true); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond) // the next transaction starts later
}

// A command deletes a pointer before its transaction is finished: for as long as a destroy
// or a replacement takes, and for good when it is killed. Another command then finds the
// pointer gone, and the transaction that is still running says, under its first signature,
// that it may remove it. A pointer that the transaction did not name is still missing, and so
// is one that a later transaction left a record for.
func TestAPointerThatARunningCommandRemovesIsNotMissing(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	readers := readersOf(ada)
	applied(t, s, "web.a", "web.b", "web.c")

	// A destroy of web.a and web.b that removed web.a and was killed there.
	tx := s.Begin("destroy", "pravo destroy")
	if err := tx.WillRemove("web.b", "web.a"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Start(); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Remove("web.a"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	missing := func(want ...string) {
		t.Helper()
		other := openIdentity(t, b, ada) // another command
		for name, check := range map[string]func(context.Context, []protect.Reader) ([]string, error){
			"the bucket": other.MissingNodes,
			"what the open read": func(ctx context.Context, r []protect.Reader) ([]string, error) {
				return other.missingNodes(ctx, r, true)
			},
		} {
			got, err := check(ctx, readers)
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("missing against %s: %v, %v; want %v", name, got, err, want)
			}
		}
	}
	missing()
	// A pointer that it did not name is missing, with or without the run.
	if err := b.Delete(ctx, nodeKey("web.c")); err != nil {
		t.Fatal(err)
	}
	missing("web.c")
	// What the run named and did not get to remove may be gone too: nothing tells the two apart.
	if err := b.Delete(ctx, nodeKey("web.b")); err != nil {
		t.Fatal(err)
	}
	missing("web.c")
	// A later transaction that leaves a record for web.a: the run that was killed before it
	// explains nothing about that record.
	time.Sleep(5 * time.Millisecond)
	s2 := openIdentity(t, b, ada)
	applied(t, s2, "web.a")
	missing("web.c")
	if err := b.Delete(ctx, nodeKey("web.a")); err != nil {
		t.Fatal(err)
	}
	missing("web.a", "web.c")
}

// The list is under the first signature: an address added to the object of a running
// transaction explains nothing, and neither does a list in a state whose transaction the
// readers do not vouch for.
func TestTheRemovalsOfATransactionAreSigned(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	readers := readersOf(ada)
	applied(t, s, "web.a", "web.b")
	tx := s.Begin("destroy", "pravo destroy")
	if err := tx.WillRemove("web.a"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Start(); err != nil {
		t.Fatal(err)
	}
	key := "txn/" + tx.ID() + ".json"
	data, err := b.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	var running Transaction
	if err := json.Unmarshal(data, &running); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(running.Removes, []string{"web.a"}) || running.RemovesDigest != "" {
		t.Fatalf("the running transaction holds removes %v and digest %q", running.Removes, running.RemovesDigest)
	}
	// Someone who writes the bucket adds web.b to the list and deletes its pointer.
	forged := running
	forged.Removes = []string{"web.a", "web.b"}
	raw, _ := json.Marshal(forged)
	if err := b.Put(ctx, key, raw); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"web.a", "web.b"} {
		if err := b.Delete(ctx, nodeKey(a)); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := openIdentity(t, b, ada).MissingNodes(ctx, readers); err != nil || !slices.Equal(got, []string{"web.a", "web.b"}) {
		t.Fatalf("with a forged list: %v, %v; want both, since the signature does not cover that list", got, err)
	}
	// The list as it was signed explains web.a again.
	if err := b.Put(ctx, key, data); err != nil {
		t.Fatal(err)
	}
	if got, err := openIdentity(t, b, ada).MissingNodes(ctx, readers); err != nil || !slices.Equal(got, []string{"web.b"}) {
		t.Fatalf("with the signed list: %v, %v; want web.b", got, err)
	}
	// Readers who do not vouch for the key take nothing from it.
	if got, err := openIdentity(t, b, ada).MissingNodes(ctx, readersOf(testIdentity(t, "eve"))); err != nil || len(got) != 0 {
		// Without the key of ada no transaction verifies, so nothing is expected either.
		t.Fatalf("for readers who vouch for nobody here: %v, %v", got, err)
	}
	// The finished transaction keeps the digest and not the list, and both signatures hold.
	tx.Record("web.a", "delete", 1, 0)
	if err := tx.FinishError("applied", "", "", true); err != nil {
		t.Fatal(err)
	}
	done := txnOf(t, s, tx.ID())
	if len(done.Removes) != 0 || done.RemovesDigest == "" {
		t.Fatalf("the finished transaction holds removes %v and digest %q", done.Removes, done.RemovesDigest)
	}
	if st, _ := protect.VerifySignature(done.StartSig, readers, protect.DomainTxnStart, s.Lineage(), txnStartParts(&done)...); st != protect.SigVerified {
		t.Fatalf("the first signature of the finished transaction: %v", st)
	}
	if st, _ := protect.VerifySignature(done.FinishSig, readers, protect.DomainTxnFinish, s.Lineage(), txnFinishParts(&done)...); st != protect.SigVerified {
		t.Fatalf("the second signature of the finished transaction: %v", st)
	}
}

// A removal that nobody announced is announced by the store before the pointer goes: the
// running transaction is written again with the address. One that was announced costs
// nothing more.
func TestRemoveAnnouncesWhatNobodyDid(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	inner := testBackends()[0].make(t)
	b := &putCounter{Backend: inner}
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	applied(t, s, "web.a", "web.b")
	tx := s.Begin("apply", "pravo apply")
	if err := tx.WillRemove("web.a"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Start(); err != nil {
		t.Fatal(err)
	}
	key := "txn/" + tx.ID() + ".json"
	removes := func() []string {
		t.Helper()
		data, err := b.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		var got Transaction
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		return got.Removes
	}
	b.txnPuts.Store(0)
	if _, err := s.Remove("web.a"); err != nil {
		t.Fatal(err)
	}
	if n := b.txnPuts.Load(); n != 0 || !slices.Equal(removes(), []string{"web.a"}) {
		t.Fatalf("an announced removal wrote the transaction %d times; it holds %v", n, removes())
	}
	if _, err := s.Remove("web.b"); err != nil {
		t.Fatal(err)
	}
	if n := b.txnPuts.Load(); n != 1 || !slices.Equal(removes(), []string{"web.a", "web.b"}) {
		t.Fatalf("a removal nobody announced wrote the transaction %d times; it holds %v", n, removes())
	}
	if err := tx.FinishError("applied", "", "", true); err != nil {
		t.Fatal(err)
	}
	// A state that is not sealed signs nothing and announces nothing.
	plain, err := OpenBackend(ctx, testBackends()[0].make(t))
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, plain, node("a"))
	ptx := plain.Begin("apply", "pravo apply")
	if err := ptx.WillRemove("t.a"); err != nil {
		t.Fatal(err)
	}
	if err := ptx.Start(); err != nil {
		t.Fatal(err)
	}
	if got := txnOf(t, plain, ptx.ID()); len(got.Removes) != 0 || got.RemovesDigest != "" {
		t.Fatalf("a transaction of a state that is not sealed holds removes %v, digest %q", got.Removes, got.RemovesDigest)
	}
}

// putCounter counts the writes of transactions and the reads of them.
type putCounter struct {
	Backend
	txnPuts, txnGets atomic.Int64
}

func (b *putCounter) Put(ctx context.Context, key string, data []byte) error {
	if strings.HasPrefix(key, "txn/") {
		b.txnPuts.Add(1)
	}
	return b.Backend.Put(ctx, key, data)
}

func (b *putCounter) Get(ctx context.Context, key string) ([]byte, error) {
	if strings.HasPrefix(key, "txn/") {
		b.txnGets.Add(1)
	}
	return b.Backend.Get(ctx, key)
}

// A command reads each finished transaction of the log once, whichever of its checks asks
// first, and the checks that follow read only what is new.
func TestTheLogIsReadOnce(t *testing.T) {
	skipWhenSealed(t)
	ctx := context.Background()
	inner := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	seed := sealedStore(t, inner, ada)
	readers := readersOf(ada)
	const n = 12
	for i := range n {
		applied(t, seed, "web.r"+string(rune('a'+i)))
	}
	all, err := seed.Transactions()
	if err != nil {
		t.Fatal(err)
	}
	b := &putCounter{Backend: inner}
	s := openIdentity(t, b, ada)
	atOpen := b.txnGets.Load()
	if err := s.CheckNodes(ctx, readers); err != nil {
		t.Fatal(err)
	}
	if got := b.txnGets.Load(); got > int64(len(all)) {
		t.Fatalf("the open and the first check read %d transactions (%d at the open), and the log holds %d", got, atOpen, len(all))
	}
	before := b.txnGets.Load()
	if err := s.CheckNodes(ctx, readers); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PolicySet(ctx, readers); err != nil {
		t.Fatal(err)
	}
	if got := b.txnGets.Load() - before; got != 0 {
		t.Fatalf("the checks that followed read %d transactions again", got)
	}
	applied(t, seed, "web.new")
	if err := s.CheckNodes(ctx, readers); err != nil && !errors.Is(err, ErrUntrusted) {
		t.Fatal(err)
	}
	if got := b.txnGets.Load() - before; got != 1 {
		t.Fatalf("a check after one more transaction read %d, want 1", got)
	}
}
