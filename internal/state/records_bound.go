package state

import (
	"encoding/json"
	"sort"
	"strconv"

	"github.com/pravo-systems/pravo/internal/protect"
)

// A record of a state whose readers file gives roles is bound to the transaction that
// wrote it: the finish signature of the transaction covers the key of the record
// (Change.Record), and the transaction is signed by a key that may write. A reader can
// seal a record, since it holds the data key of the group, but it cannot make a
// transaction of a writer name it. VerifyRecords finds the records that no such
// transaction names. It detects and does not prevent: only the credentials of the
// bucket stop a write.

// BindRecords makes the transactions of this store sign the records they write. It is set
// when the readers file of the repository gives roles; an older pravo does not know the
// extra part of the signature, and cannot read a readers file with roles either.
func (s *Store) BindRecords(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindRecordsOn = on
}

func (s *Store) bindsRecords() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bindRecordsOn
}

// heldRecord is the key of the record that the pointer of addr names, as this store
// read or wrote it. It is empty in a state of format 1.
func (s *Store) heldRecord(addr string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held[addr].Record
}

// RecordProblem is a record that a transaction of a writer does not account for.
type RecordProblem struct {
	Addr string `json:"addr"`
	// Why says what is wrong, in a sentence.
	Why string `json:"why"`
}

// RecordVerdict is what VerifyRecords makes of the records of a state.
type RecordVerdict struct {
	// Bound counts the records that a verified transaction of a writer names.
	Bound int `json:"bound"`
	// Earlier counts the records of a verified transaction of a writer that was made before
	// the transactions bound their records: nothing says they are not the writer's.
	Earlier int `json:"before_roles"`
	// Unchecked counts the records whose transaction is still running or was killed, or
	// is signed with a key that is not in the readers file: a member who has left, or a
	// key that someone made. Nothing tells those two apart.
	Unchecked int `json:"unchecked"`
	// Problems lists the records that are not a writer's work, sorted by address.
	Problems []RecordProblem `json:"problems,omitempty"`
}

// VerifyRecords checks the records of the nodes in addrs, or of every node when addrs is
// empty, against the transactions. It is meaningful only when the readers give roles.
func (s *Store) VerifyRecords(txns []Transaction, readers []protect.Reader, addrs ...string) RecordVerdict {
	byID := make(map[string]*Transaction, len(txns))
	for i := range txns {
		byID[txns[i].ID] = &txns[i]
	}
	return s.verifyRecords(func(id string) *Transaction { return byID[id] }, readers, addrs)
}

// CheckRecords is VerifyRecords for the nodes in addrs, reading only the transactions
// that wrote them. A command that is about to build on a record checks the ones it reads.
func (s *Store) CheckRecords(readers []protect.Reader, addrs ...string) RecordVerdict {
	if len(addrs) == 0 {
		return RecordVerdict{}
	}
	cache := map[string]*Transaction{}
	return s.verifyRecords(func(id string) *Transaction {
		if t, ok := cache[id]; ok {
			return t
		}
		var t *Transaction
		if data, err := s.b.Get(s.cx(), "txn/"+id+".json"); err == nil {
			var got Transaction
			if json.Unmarshal(data, &got) == nil && got.ID == id {
				t = &got
			}
		}
		cache[id] = t
		return t
	}, readers, addrs)
}

func (s *Store) verifyRecords(lookup func(id string) *Transaction, readers []protect.Reader, addrs []string) RecordVerdict {
	type node struct {
		addr, rec, txn string
		revision       int
	}
	var nodes []node
	s.mu.Lock()
	want := map[string]bool{}
	for _, a := range addrs {
		want[a] = true
	}
	for addr, r := range s.nodes {
		if len(want) > 0 && !want[addr] {
			continue
		}
		if ref := s.held[addr]; ref.Record != "" {
			nodes = append(nodes, node{addr, ref.Record, r.Txn, r.Revision})
		}
	}
	s.mu.Unlock()
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].addr < nodes[j].addr })

	var v RecordVerdict
	problem := func(addr, why string) { v.Problems = append(v.Problems, RecordProblem{addr, why}) }
	lineage := s.Lineage()
	for _, n := range nodes {
		if n.txn == "" {
			problem(n.addr, "it names no transaction that wrote it")
			continue
		}
		t := lookup(n.txn)
		if t == nil {
			// Transactions are never collected: a record that names one that is not there
			// may name one that never was.
			problem(n.addr, "the transaction "+n.txn+" that wrote it is not in the state")
			continue
		}
		if t.Finished == nil || t.FinishSig == nil && t.Status == "running" {
			// A run that is still going, or was killed. Its start is signed by a writer, or
			// it is not a run of a writer.
			sv := VerifyTransaction(lineage, t, readers)
			switch role, _ := protect.RoleOf(readers, startKey(t)); {
			case sv.Start == protect.SigUnknownKey:
				v.Unchecked++
			case sv.Start != protect.SigVerified || !role.Allows(protect.RoleWriter):
				problem(n.addr, "the transaction "+t.ID+" that wrote it was not started by a writer")
			default:
				v.Unchecked++
			}
			continue
		}
		verdict := VerifyTransaction(lineage, t, readers)
		switch verdict.Finish {
		case protect.SigUnknownKey:
			v.Unchecked++
			continue
		case protect.SigVerified:
		default:
			problem(n.addr, "the transaction "+t.ID+" that wrote it is not signed, or its signature does not verify")
			continue
		}
		if verdict.Altered {
			problem(n.addr, "the transaction "+t.ID+" that wrote it was altered")
			continue
		}
		if role, _ := protect.RoleOf(readers, t.FinishSig.Key); !role.Allows(protect.RoleWriter) {
			problem(n.addr, "the transaction "+t.ID+" that wrote it was run by "+verdict.Signer+", who is a "+role.String()+" and may not write")
			continue
		}
		switch c := changeOf(t, n.addr, n.revision); {
		case c == nil:
			problem(n.addr, "the transaction "+t.ID+" does not name revision "+strconv.Itoa(n.revision))
		case c.Record == "":
			v.Earlier++
		case c.Record != n.rec:
			problem(n.addr, "the transaction "+t.ID+" signed another record for revision "+strconv.Itoa(n.revision))
		default:
			v.Bound++
		}
	}
	return v
}

// changeOf is the change of t that made revision of addr.
func changeOf(t *Transaction, addr string, revision int) *Change {
	for i := range t.Changes {
		if c := &t.Changes[i]; c.Addr == addr && c.After == revision {
			return c
		}
	}
	return nil
}

// startKey is the fingerprint of the key that signed the start of t, empty if it has none.
func startKey(t *Transaction) string {
	if t.StartSig == nil {
		return ""
	}
	return t.StartSig.Key
}
