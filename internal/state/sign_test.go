package state

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/internal/protect"
)

// run records a transaction the way a command does, and returns its id.
func run(t *testing.T, s *Store, status, full string) string {
	t.Helper()
	tx := s.Begin("apply", "pravo apply")
	if err := tx.Start(); err != nil {
		t.Fatal(err)
	}
	tx.Record("net.vpc", "update", 1, 2)
	tx.Record("db.main", "update", 2, 3)
	if err := tx.FinishError(status, "the run ended: "+status, full, true); err != nil {
		t.Fatal(err)
	}
	return tx.ID()
}

func txnOf(t *testing.T, s *Store, id string) Transaction {
	t.Helper()
	data, err := s.b.Get(context.Background(), "txn/"+id+".json")
	if err != nil {
		t.Fatal(err)
	}
	var tx Transaction
	if err := json.Unmarshal(data, &tx); err != nil {
		t.Fatal(err)
	}
	return tx
}

func putTxn(t *testing.T, s *Store, tx Transaction) {
	t.Helper()
	data, _ := json.Marshal(tx)
	if err := s.b.Put(context.Background(), "txn/"+tx.ID+".json", data); err != nil {
		t.Fatal(err)
	}
}

func TestTransactionsOfASealedStateAreSigned(t *testing.T) {
	skipWhenSealed(t)
	b := testBackends()[0].make(t)
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	s := sealedStore(t, b, ada, dan)
	readers := readersOf(ada, dan)
	id := run(t, s, "applied", "")
	tx := txnOf(t, s, id)
	if tx.StartSig == nil || tx.FinishSig == nil || tx.StartSig.Signer != "ada" {
		t.Fatalf("signatures %+v %+v", tx.StartSig, tx.FinishSig)
	}
	v := VerifyTransaction(s.Lineage(), &tx, readers)
	if v.Overall() != protect.SigVerified || v.Signer != "ada" {
		t.Fatalf("verdict %+v", v)
	}
	// Another reader opens the store and signs as themself.
	other := openIdentity(t, b, dan)
	oid := run(t, other, "applied", "")
	otx := txnOf(t, other, oid)
	if v := VerifyTransaction(s.Lineage(), &otx, readers); v.Overall() != protect.SigVerified || v.Signer != "dan" {
		t.Errorf("dan's run: %+v", v)
	}
	// A key that is no longer listed is not a failure.
	if v := VerifyTransaction(s.Lineage(), &tx, readersOf(dan)); v.Overall() != protect.SigUnknownKey || v.Claimed != "ada" {
		t.Errorf("a departed signer: %+v", v)
	}
}

func TestATamperedTransactionIsInvalid(t *testing.T) {
	skipWhenSealed(t)
	b := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	readers := readersOf(ada)
	id := run(t, s, "applied", "")
	good := txnOf(t, s, id)
	for name, change := range map[string]func(*Transaction){
		"status":      func(x *Transaction) { x.Status = "failed" },
		"command":     func(x *Transaction) { x.Command = "pravo plan" },
		"actor":       func(x *Transaction) { x.Actor = "mallory" },
		"user":        func(x *Transaction) { x.User = "mallory" },
		"change":      func(x *Transaction) { x.Changes[0].After = 9 },
		"added":       func(x *Transaction) { x.Changes = append(x.Changes, Change{Addr: "x.y", Action: "create"}) },
		"dropped":     func(x *Transaction) { x.Changes = x.Changes[:1] },
		"plan digest": func(x *Transaction) { x.PlanDigest = "00" },
		"kind":        func(x *Transaction) { x.Kind = "destroy" },
		"id":          func(x *Transaction) { x.ID = "other" },
		"start time":  func(x *Transaction) { x.Started = x.Started.Add(1) },
	} {
		tx := good
		tx.Changes = append([]Change(nil), good.Changes...)
		change(&tx)
		if v := VerifyTransaction(s.Lineage(), &tx, readers); v.Overall() != protect.SigInvalid {
			t.Errorf("%s: %v", name, v.Overall())
		}
	}
	// The text of the error is not covered, so that unsealing can write it back.
	tx := good
	tx.Summary = "something else"
	if v := VerifyTransaction(s.Lineage(), &tx, readers); v.Overall() != protect.SigVerified {
		t.Errorf("summary: %v", v.Overall())
	}
	// A transaction that is finished and lost its second signature is not verified,
	// and a signature of another state does not verify.
	stripped := good
	stripped.FinishSig = nil
	if v := VerifyTransaction(s.Lineage(), &stripped, readers); v.Overall() != protect.SigUnsigned {
		t.Errorf("stripped: %v", v.Overall())
	}
	if v := VerifyTransaction("another-lineage", &good, readers); v.Overall() != protect.SigInvalid {
		t.Errorf("another state: %v", v.Overall())
	}
}

func TestARunThatIsKilledKeepsItsStartSignature(t *testing.T) {
	skipWhenSealed(t)
	b := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	tx := s.Begin("apply", "pravo apply")
	if err := tx.Start(); err != nil {
		t.Fatal(err)
	}
	got := txnOf(t, s, tx.ID())
	if got.Status != "running" || got.StartSig == nil || got.FinishSig != nil {
		t.Fatalf("%+v", got)
	}
	if v := VerifyTransaction(s.Lineage(), &got, readersOf(ada)); v.Overall() != protect.SigVerified {
		t.Errorf("a run that is killed: %+v", v)
	}
}

func TestAnUnsealedStateSignsNothing(t *testing.T) {
	skipWhenSealed(t)
	b := testBackends()[0].make(t)
	s, err := OpenBackendWith(context.Background(), b, Options{NewFormat: FormatImmutable})
	if err != nil {
		t.Fatal(err)
	}
	id := run(t, s, "applied", "")
	if tx := txnOf(t, s, id); tx.StartSig != nil || tx.FinishSig != nil {
		t.Errorf("signatures in a state that is not sealed: %+v", tx)
	}
}

func TestARunThatCannotSignIsRefusedBeforeItChangesAnything(t *testing.T) {
	skipWhenSealed(t)
	b := testBackends()[0].make(t)
	ctx := context.Background()
	// An identity that signs through an agent that is not there.
	pubOnly := testIdentity(t, "src")
	agent, err := protect.NewIdentity("ben", "ssh-ed25519 "+protect.SSHBlob(pubOnly.SignPub))
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada, agent)
	old := protect.DialAgentFunc
	defer func() { protect.DialAgentFunc = old }()
	protect.DialAgentFunc = func() (io.ReadWriteCloser, error) { return nil, errors.New("no agent") }

	ben := openIdentity(t, b, agent)
	tx := ben.Begin("apply", "pravo apply")
	err = tx.Start()
	if err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("Start = %v", err)
	}
	if _, err := ben.b.Get(ctx, "txn/"+tx.ID()+".json"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a transaction was written: %v", err)
	}
	// The cloud was changed already when the end cannot be signed: the record is kept,
	// without the signature, and the error says so.
	tx = ben.Begin("apply", "pravo apply")
	tx.Record("net.vpc", "update", 1, 2)
	err = tx.Finish("applied", "done", true)
	if err == nil || !strings.Contains(err.Error(), "without a signature") {
		t.Fatalf("Finish = %v", err)
	}
	got := txnOf(t, ben, tx.ID())
	if got.Status != "applied" || got.FinishSig != nil {
		t.Errorf("%+v", got)
	}
	if v := VerifyTransaction(s.Lineage(), &got, readersOf(ada, agent)); v.Overall() != protect.SigUnsigned {
		t.Errorf("verdict %v", v.Overall())
	}
}

// Sealing again, rewriting under a new group and unsealing rewrite what a record and a
// summary hold, and leave the signatures valid.
func TestSignaturesSurviveConversions(t *testing.T) {
	skipWhenSealed(t)
	fastRecheck(t)
	b := testBackends()[0].make(t)
	ctx := context.Background()
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	s := sealedStore(t, b, ada, dan)
	readers := readersOf(ada, dan)
	id := run(t, s, "failed", "provider said: "+secretValue)
	if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada), All: true}); err != nil {
		t.Fatal(err)
	}
	tx := txnOf(t, s, id)
	if v := VerifyTransaction(s.Lineage(), &tx, readersOf(ada)); v.Overall() != protect.SigVerified {
		t.Errorf("after rewrap --all: %+v", v)
	}
	if _, err := s.Unseal(ctx, UnsealOptions{Identity: ada}); err != nil {
		t.Fatal(err)
	}
	tx = txnOf(t, s, id)
	if !strings.Contains(tx.Summary, secretValue) {
		t.Fatalf("the text of the error did not come back: %q", tx.Summary)
	}
	if v := VerifyTransaction(s.Lineage(), &tx, readers); v.Overall() != protect.SigVerified {
		t.Errorf("after the unseal: %+v", v)
	}
}

// A transaction that is running holds what the start signed and nothing else: the outcome is
// signed by the end, so a running transaction with one is not what any signer wrote.
func TestARunningTransactionHoldsNothingThatOnlyTheEndSigns(t *testing.T) {
	skipWhenSealed(t)
	b := testBackends()[0].make(t)
	ada, dan := testIdentity(t, "ada"), testIdentity(t, "dan")
	s := sealedStore(t, b, ada, dan)
	readers := readersOf(ada, dan)
	good := txnOf(t, s, run(t, s, "applied", ""))
	for name, f := range map[string]func(*Transaction){
		"finished flipped back to running": func(x *Transaction) { x.Status, x.Finished = "running", nil },
		"changes added to a running one":   func(x *Transaction) { x.Status, x.Finished, x.FinishSig = "running", nil, nil },
		"a summary on a running one": func(x *Transaction) {
			x.Status, x.Finished, x.FinishSig, x.Changes, x.Summary = "running", nil, nil, nil, "forged"
		},
		"the end signed by another key": func(x *Transaction) {
			sig, err := dan.SignMessage(protect.DomainTxnFinish, s.Lineage(), txnFinishParts(x)...)
			if err != nil {
				t.Fatal(err)
			}
			x.FinishSig = &sig
		},
	} {
		tx := good
		tx.Changes = append([]Change(nil), good.Changes...)
		f(&tx)
		if v := VerifyTransaction(s.Lineage(), &tx, readers); v.Overall() != protect.SigInvalid {
			t.Errorf("%s: %v", name, v.Overall())
		}
	}
	// A run that is really running verifies.
	r := s.Begin("apply", "pravo apply")
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	got := txnOf(t, s, r.ID())
	if v := VerifyTransaction(s.Lineage(), &got, readers); v.Overall() != protect.SigVerified {
		t.Errorf("a run: %v", v.Overall())
	}
	// The trace link is covered.
	tr := good
	tr.TraceID = "elsewhere"
	if v := VerifyTransaction(s.Lineage(), &tr, readers); v.Overall() != protect.SigInvalid {
		t.Errorf("trace id: %v", v.Overall())
	}
}

// A field that is not valid UTF-8 is stored as U+FFFD, and the signature covers what is stored.
func TestATransactionWithBytesThatAreNotTextStillVerifies(t *testing.T) {
	skipWhenSealed(t)
	b := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	tx := s.Begin("apply", "pravo apply --reason caf\xe9\xff\xfe")
	tx.t.Reason = "caf\xe9"
	tx.t.Host = "h\xc3"
	if err := tx.Start(); err != nil {
		t.Fatal(err)
	}
	tx.Record("net.vpc\xff", "update", 1, 2)
	if err := tx.Finish("applied", "done", true); err != nil {
		t.Fatal(err)
	}
	got := txnOf(t, s, tx.ID())
	if v := VerifyTransaction(s.Lineage(), &got, readersOf(ada)); v.Overall() != protect.SigVerified {
		t.Errorf("verdict %v", v.Overall())
	}
}

// With a hook, an end that cannot be signed is a warning and the command does not fail.
func TestAnEndThatCannotBeSignedIsAWarningWhenThereIsAHook(t *testing.T) {
	skipWhenSealed(t)
	b := testBackends()[0].make(t)
	pubOnly := testIdentity(t, "src")
	agent, err := protect.NewIdentity("ben", "ssh-ed25519 "+protect.SSHBlob(pubOnly.SignPub))
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	ada := testIdentity(t, "ada")
	sealedStore(t, b, ada, agent)
	old := protect.DialAgentFunc
	defer func() { protect.DialAgentFunc = old }()
	protect.DialAgentFunc = func() (io.ReadWriteCloser, error) { return nil, errors.New("no agent") }
	ben := openIdentity(t, b, agent)
	var warned string
	ben.SetWarn(func(m string) { warned = m })
	tx := ben.Begin("apply", "pravo apply")
	tx.Record("net.vpc", "update", 1, 2)
	if err := tx.Finish("applied", "done", true); err != nil {
		t.Fatalf("Finish = %v", err)
	}
	if !strings.Contains(warned, "without a signature") {
		t.Errorf("warned %q", warned)
	}
}

// A copy of a transaction under another name is not another transaction.
func TestACopyOfATransactionIsNotListedTwice(t *testing.T) {
	skipWhenSealed(t)
	b := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	id := run(t, s, "applied", "")
	data, _ := s.b.Get(context.Background(), "txn/"+id+".json")
	if err := s.b.Put(context.Background(), "txn/zzzz.json", data); err != nil {
		t.Fatal(err)
	}
	txns, err := s.Transactions()
	if err != nil || len(txns) != 1 {
		t.Errorf("%d transactions, %v", len(txns), err)
	}
}
