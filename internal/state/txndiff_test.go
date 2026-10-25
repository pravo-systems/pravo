package state

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const diffPlanted = "planted-diff-value-0042"

func recordDiff(t *testing.T, s *Store, addr string) string {
	t.Helper()
	txn := s.Begin("apply", "pravo apply")
	txn.Record(addr, "update", 1, 2)
	txn.RecordDiff(addr, json.RawMessage(`{"name":"a","password":"old","port":5432,"gone":null}`),
		json.RawMessage(`{"name":"a","password":"`+diffPlanted+`","port":5433}`), nil)
	if err := txn.Finish("applied", "1 changed", true); err != nil {
		t.Fatal(err)
	}
	return txn.ID()
}

func TestDiffOfKeepsTheTopLevelAttributesThatDiffer(t *testing.T) {
	d := DiffOf(json.RawMessage(`{"a":1,"b":{"x":1},"c":null}`), json.RawMessage(`{"a":1,"b":{"x":2},"d":"n"}`), nil)
	if d == nil || len(d.Before) != 1 || string(d.Before["b"]) != `{"x":1}` || len(d.After) != 2 || string(d.After["d"]) != `"n"` {
		t.Fatalf("%+v", d)
	}
	if DiffOf(json.RawMessage(`{"a":1}`), json.RawMessage(`{ "a": 1 }`), nil) != nil {
		t.Error("equal attributes have a diff")
	}
	long := make([]byte, MaxDiffBytes+1)
	for i := range long {
		long[i] = 'a'
	}
	if d := DiffOf(nil, json.RawMessage(`{"x":"`+string(long)+`"}`), nil); d == nil || !d.Omitted || d.After != nil {
		t.Errorf("a long value: %+v", d)
	}
	if d := DiffOf(nil, json.RawMessage(`{"a":1}`), nil); d == nil || d.Before != nil || string(d.After["a"]) != "1" {
		t.Errorf("a create: %+v", d)
	}
}

// An attribute that the caller withholds is named with the sides that held a value,
// and its values are not kept.
func TestDiffOfWithholdsWhatTheCallerSays(t *testing.T) {
	d := DiffOf(json.RawMessage(`{"a":1,"pw":"old"}`), json.RawMessage(`{"a":2,"pw":"new","tok":"x"}`),
		func(name string, _, _ json.RawMessage) bool { return name != "a" })
	if d == nil || len(d.Before) != 1 || len(d.After) != 1 || d.Withheld["pw"] != "both" || d.Withheld["tok"] != "after" {
		t.Fatalf("%+v", d)
	}
	raw, _ := json.Marshal(d)
	for _, v := range []string{"old", "new", `"x"`} {
		if strings.Contains(string(raw), v) {
			t.Errorf("the diff keeps %s: %s", v, raw)
		}
	}
}

// In a state that is not sealed the diff stays in the change.
func TestTransactionKeepsTheDiffOfAChange(t *testing.T) {
	skipWhenSealed(t)
	b := testBackends()[0].make(t)
	s, err := OpenBackendWith(context.Background(), b, Options{NewFormat: Format})
	if err != nil {
		t.Fatal(err)
	}
	id := recordDiff(t, s, "db.main")
	txns, _ := s.Transactions()
	if len(txns) != 1 || txns[0].ID != id {
		t.Fatalf("%v", txns)
	}
	diffs, sealed := s.TxnDiffs(txns[0])
	d, ok := diffs["db.main"]
	if sealed || !ok || string(d.After["port"]) != "5433" || string(d.Before["gone"]) != "" || len(d.Before) != 2 {
		t.Errorf("%+v sealed=%v", diffs, sealed)
	}
}

// In a sealed state the values are in a box: not in the bucket, readable by a
// reader, moved by rewrap --all, and written back by an unseal.
func TestSealedStateKeepsTheDiffSealed(t *testing.T) {
	skipWhenSealed(t)
	fastRecheck(t)
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			ada, fay := testIdentity(t, "ada"), testIdentity(t, "fay")
			s := sealedStore(t, b, ada, fay)
			recordDiff(t, s, "db.main")
			assertNoSecret(t, b, diffPlanted)
			read := func(st *Store) (map[string]Diff, bool, Transaction) {
				t.Helper()
				txns, err := st.Transactions()
				if err != nil || len(txns) != 1 {
					t.Fatalf("%v %v", txns, err)
				}
				d, sealed := st.TxnDiffs(txns[0])
				return d, sealed, txns[0]
			}
			if d, sealed, tx := read(openIdentity(t, b, fay)); sealed || string(d["db.main"].After["password"]) != `"`+diffPlanted+`"` || tx.Changes[0].Diff != nil {
				t.Fatalf("fay reads %+v sealed=%v", d, sealed)
			}
			if _, sealed, _ := read(openIdentity(t, b, nil)); !sealed {
				t.Error("a store with no key opens the diff")
			}
			if _, err := s.Rewrap(ctx, RewrapOptions{Readers: readersOf(ada), All: true}); err != nil {
				t.Fatal(err)
			}
			d, sealed, tx := read(openIdentity(t, b, ada))
			if sealed || tx.DiffSealed.Group != "g2" || string(d["db.main"].After["port"]) != "5433" {
				t.Errorf("after rewrap --all: %+v sealed=%v %v", d, sealed, tx.DiffSealed)
			}
			if _, sealed, _ := read(openIdentity(t, b, fay)); !sealed {
				t.Error("fay opens the diff after rewrap --all")
			}
			if _, err := s.Unseal(ctx, UnsealOptions{Identity: ada}); err != nil {
				t.Fatal(err)
			}
			d, sealed, tx = read(openIdentity(t, b, nil))
			if sealed || tx.DiffSealed != nil || string(d["db.main"].After["password"]) != `"`+diffPlanted+`"` {
				t.Errorf("after unseal: %+v sealed=%v", d, sealed)
			}
		})
	}
}

// A diff recorded before the seal moves into a box when the state is sealed.
func TestUpgradeToSealedSealsEarlierDiffs(t *testing.T) {
	skipWhenSealed(t)
	for _, bk := range testBackends() {
		t.Run(bk.name, func(t *testing.T) {
			b := bk.make(t)
			ctx := context.Background()
			ada := testIdentity(t, "ada")
			s, err := OpenBackendWith(ctx, b, Options{NewFormat: Format, Identity: ada})
			if err != nil {
				t.Fatal(err)
			}
			seedSecrets(t, s)
			recordDiff(t, s, "db.main")
			if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: readersOf(ada), Identity: ada}); err != nil {
				t.Fatal(err)
			}
			assertNoSecret(t, b, diffPlanted)
			r := openIdentity(t, b, ada)
			txns, _ := r.Transactions()
			d, sealed := r.TxnDiffs(txns[len(txns)-1])
			if sealed || string(d["db.main"].After["port"]) != "5433" {
				t.Errorf("%+v sealed=%v", d, sealed)
			}
		})
	}
}
