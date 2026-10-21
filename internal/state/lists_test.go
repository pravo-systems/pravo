package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A query that finds nothing returns an empty list and not nil, so that a
// command that prints it as JSON prints [] and not null.
func TestEmptyQueriesReturnEmptyLists(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "pravo.state"))
	if err != nil {
		t.Fatal(err)
	}
	txns, err := s.Transactions()
	if err != nil || txns == nil || len(txns) != 0 {
		t.Errorf("Transactions() = %#v, %v", txns, err)
	}
	locks, err := s.Locks()
	if err != nil || locks == nil || len(locks) != 0 {
		t.Errorf("Locks() = %#v, %v", locks, err)
	}
	if e := s.Edges(); e == nil || len(e) != 0 {
		t.Errorf("Edges() = %#v", e)
	}
	if d := s.Dependents("nothing"); d == nil || len(d) != 0 {
		t.Errorf("Dependents() = %#v", d)
	}
	for name, v := range map[string]any{"transactions": txns, "locks": locks, "edges": s.Edges(), "dependents": s.Dependents("x")} {
		b, err := json.Marshal(v)
		if err != nil || string(b) != "[]" {
			t.Errorf("%s encode as %s, %v; want []", name, b, err)
		}
	}
}

// A transaction file written without its list of changes reads back with an
// empty list.
func TestTransactionWithoutChangesReadsAsEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pravo.state")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "txn"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "txn", "20260101T000000Z-aaaaaa.json"), []byte(`{"id":"20260101T000000Z-aaaaaa","kind":"apply","command":"pravo apply","started":"2026-01-01T00:00:00Z","status":"applied"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	txns, err := s.Transactions()
	if err != nil || len(txns) != 1 {
		t.Fatalf("Transactions() = %#v, %v", txns, err)
	}
	if txns[0].Changes == nil {
		t.Error("the changes of the transaction are nil")
	}
}

// The text of a lock is written as it is: <, > and & are not escaped.
func TestLockFileKeepsHTMLCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pravo.state")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Lock("a", "pravo apply --reason \"a<b & c>d\"", ""); err != nil {
		t.Fatal(err)
	}
	defer s.Unlock("a")
	entries, err := os.ReadDir(filepath.Join(dir, "locks"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("locks: %v, %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "locks", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `a<b & c>d`) || strings.Contains(string(data), `\u003c`) {
		t.Errorf("the lock file escapes < or &:\n%s", data)
	}
}
