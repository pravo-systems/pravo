package state

import (
	"context"
	"encoding/json"
	"strings"
)

// The transactions are objects named txn/ID.json. Three questions are asked of them in many
// places, and each has one answer here: which objects are transactions, what id does a name
// stand for, and does an object hold the transaction that its name says.

const (
	txnPrefix = "txn/"
	txnSuffix = ".json"
)

// txnKey is the name of the object of the transaction with the id.
func txnKey(id string) string { return txnPrefix + id + txnSuffix }

// txnIDOf is the id that an object name stands for, false for a name that is not one of a
// transaction (a temporary file, a note).
func txnIDOf(key string) (string, bool) {
	id, ok := strings.CutPrefix(key, txnPrefix)
	if !ok {
		return "", false
	}
	return strings.CutSuffix(id, txnSuffix)
}

// txnKeys lists the names of the objects of the transactions.
func (s *Store) txnKeys(ctx context.Context) ([]string, error) {
	keys, err := s.b.List(ctx, "txn")
	if err != nil {
		return nil, err
	}
	out := keys[:0:0]
	for _, k := range keys {
		if _, ok := txnIDOf(k); ok {
			out = append(out, k)
		}
	}
	return out, nil
}

// decodeTxn parses the transaction in an object that is named for id. A damaged object, and
// one that holds another transaction (a copy under another name is not another
// transaction), is nil: a transaction is a log, and one that is damaged is left out of what
// is read from it, by every reader alike.
func decodeTxn(data []byte, id string) *Transaction {
	var t Transaction
	if json.Unmarshal(data, &t) != nil || t.ID != id {
		return nil
	}
	return &t
}

// mustTxnID is txnIDOf for a name that txnKeys listed.
func mustTxnID(key string) string {
	id, _ := txnIDOf(key)
	return id
}
