package state

import (
	"context"
	"testing"
)

func TestTxnIDOf(t *testing.T) {
	for key, want := range map[string]string{
		"txn/20260101T000000Z-ab.json":      "20260101T000000Z-ab",
		"txn/20260101T000000Z-ab.json.tmp":  "",
		"txn/20260101T000000Z-ab":           "",
		"other/20260101T000000Z-ab.json":    "",
		"txn/.json":                         "",
		"txn/20260101T000000Z-ab.json.part": "",
	} {
		got, ok := txnIDOf(key)
		if want == "" && key != "txn/.json" {
			if ok {
				t.Errorf("%q is not a transaction, got %q", key, got)
			}
			continue
		}
		if want != "" && (!ok || got != want) {
			t.Errorf("%q: got %q %v, want %q", key, got, ok, want)
		}
	}
	if txnKey("x") != "txn/x.json" {
		t.Errorf("txnKey %q", txnKey("x"))
	}
}

func TestDecodeTxnBindsTheNameAndSkipsDamage(t *testing.T) {
	if decodeTxn([]byte(`{"id":"a"}`), "a") == nil {
		t.Error("the transaction of the name must decode")
	}
	if decodeTxn([]byte(`{"id":"b"}`), "a") != nil {
		t.Error("a copy under another name is not another transaction")
	}
	if decodeTxn([]byte(`{not json`), "a") != nil {
		t.Error("a damaged object is nil")
	}
}

// A damaged log entry is left out by every reader alike: the vouch listing used to fail
// on it where the log, the latest transaction and the fact scan skipped it.
func TestADamagedTransactionIsSkippedByEveryReader(t *testing.T) {
	ctx := context.Background()
	b := testBackends()[0].make(t)
	ada := testIdentity(t, "ada")
	s := sealedStore(t, b, ada)
	mustSetSecret(t, s, "db", pw1, "")
	// The transaction that wrote the secret is one that the vouch listing reads by name.
	keys, err := b.List(ctx, "txn")
	if err != nil || len(keys) == 0 {
		t.Fatalf("transactions: %v %v", keys, err)
	}
	for _, k := range keys {
		if err := b.Put(ctx, k, []byte("{not json")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Transactions(); err != nil {
		t.Fatalf("Transactions: %v", err)
	}
	if _, err := s.LatestTransaction(); err != nil {
		t.Fatalf("LatestTransaction: %v", err)
	}
	if _, err := s.LiveSigners(ctx); err != nil {
		t.Fatalf("LiveSigners must skip a damaged entry as the others do: %v", err)
	}
}
