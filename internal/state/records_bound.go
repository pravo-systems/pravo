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
	// Vouched counts the records whose transaction was signed by a key that is not a writer
	// now, and that an admin vouched for.
	Vouched int `json:"vouched,omitempty"`
	// Earlier counts the records of a verified transaction of a writer that was made before
	// the transactions bound their records: nothing says they are not the writer's.
	Earlier int `json:"before_roles"`
	// Unchecked counts the records whose transaction is still running or was killed. Without
	// strict rules it also counts those signed by a key that is not in the readers file,
	// which could be a member who left or a key that someone made.
	Unchecked int `json:"unchecked"`
	// Problems lists the records that are not a writer's work, sorted by address.
	Problems []RecordProblem `json:"problems,omitempty"`
}

// RecordRules says whom the transactions that wrote the records may be signed by.
type RecordRules struct {
	// Readers are the keys of the roles, against which the signatures are verified.
	Readers []protect.Reader
	// Vouched are the transactions that an admin vouched for, by id: a key that was demoted or
	// left keeps these, and nothing else it signs.
	Vouched map[string]protect.Vouch
	// Strict refuses a transaction signed by a key that is not in the roles and whose
	// transaction is not vouched for, such as a key a holder of the data key made for the
	// purpose or a leaver's. It is set when the roles are pinned in the state, which is when
	// the lists are complete.
	Strict bool
}

// VerifyRecords checks the records of the nodes in addrs, or of every node when addrs is
// empty, against the transactions. It is meaningful only when the readers give roles.
func (s *Store) VerifyRecords(txns []Transaction, readers []protect.Reader, addrs ...string) RecordVerdict {
	return s.VerifyRecordsWith(txns, RecordRules{Readers: readers}, addrs...)
}

// VerifyRecordsWith is VerifyRecords with the rules for keys that are not in the roles.
func (s *Store) VerifyRecordsWith(txns []Transaction, rules RecordRules, addrs ...string) RecordVerdict {
	byID := make(map[string]*Transaction, len(txns))
	for i := range txns {
		byID[txns[i].ID] = &txns[i]
	}
	return s.verifyRecords(func(id string) *Transaction { return byID[id] }, rules, addrs)
}

// CheckRecords is VerifyRecords for the nodes in addrs, reading only the transactions
// that wrote them. A command that is about to build on a record checks the ones it reads.
func (s *Store) CheckRecords(readers []protect.Reader, addrs ...string) RecordVerdict {
	return s.CheckRecordsWith(RecordRules{Readers: readers}, addrs...)
}

// CheckRecordsWith is CheckRecords with the rules for keys that are not in the roles.
func (s *Store) CheckRecordsWith(rules RecordRules, addrs ...string) RecordVerdict {
	if len(addrs) == 0 {
		return RecordVerdict{}
	}
	fetch := func(id string) *Transaction {
		if data, err := s.b.Get(s.cx(), "txn/"+id+".json"); err == nil {
			var got Transaction
			if json.Unmarshal(data, &got) == nil && got.ID == id {
				return &got
			}
		}
		return nil
	}
	// The transactions that wrote the records are read together, not one after another.
	seen := map[string]bool{}
	var ids []string
	s.mu.Lock()
	for _, a := range addrs {
		if r := s.nodes[a]; r != nil && r.Txn != "" && !seen[r.Txn] {
			seen[r.Txn] = true
			ids = append(ids, r.Txn)
		}
	}
	s.mu.Unlock()
	got := make([]*Transaction, len(ids))
	s.each(len(ids), func(i int) { got[i] = fetch(ids[i]) })
	cache := make(map[string]*Transaction, len(ids))
	for i, id := range ids {
		cache[id] = got[i]
	}
	return s.verifyRecords(func(id string) *Transaction {
		if t, ok := cache[id]; ok {
			return t
		}
		t := fetch(id)
		cache[id] = t
		return t
	}, rules, addrs)
}

func (s *Store) verifyRecords(lookup func(id string) *Transaction, rules RecordRules, addrs []string) RecordVerdict {
	readers := rules.Readers
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
	// A key the signature names that is not in the roles is a problem when the rules are strict.
	unknown := func(addr, txn string) {
		if rules.Strict {
			problem(addr, "the transaction "+txn+" that wrote it was signed by a key that is not in the roles, and no admin vouched for it")
			return
		}
		v.Unchecked++
	}
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
				unknown(n.addr, t.ID)
			case sv.Start != protect.SigVerified || !role.Allows(protect.RoleWriter):
				problem(n.addr, "the transaction "+t.ID+" that wrote it was not started by a writer")
			default:
				v.Unchecked++
			}
			continue
		}
		if t.FinishSig == nil && t.StartSig == nil {
			// Written with no identity, before the state was sealed or by a job that had none.
			// Only the transaction an admin vouched for by id and digest is accepted.
			vo, ok := rules.Vouched[t.ID]
			if !ok || vo.Key != protect.UnsignedKey || vo.Digest != VouchDigest(lineage, t) {
				problem(n.addr, "the transaction "+t.ID+" that wrote it is not signed, or its signature does not verify")
				continue
			}
			switch c := changeOf(t, n.addr, n.revision); {
			case c == nil:
				problem(n.addr, "the transaction "+t.ID+" does not name revision "+strconv.Itoa(n.revision))
			case c.Record != "" && c.Record != n.rec:
				problem(n.addr, "the transaction "+t.ID+" signed another record for revision "+strconv.Itoa(n.revision))
			default:
				v.Vouched++
			}
			continue
		}
		verdict := VerifyTransaction(lineage, t, readers)
		vouched := false
		if vo, ok := rules.Vouched[t.ID]; ok && t.FinishSig != nil && vo.Key == t.FinishSig.Key && vo.Digest == VouchDigest(lineage, t) {
			vouched = true
		}
		switch verdict.Finish {
		case protect.SigUnknownKey:
			if !vouched {
				unknown(n.addr, t.ID)
				continue
			}
		case protect.SigVerified:
		default:
			problem(n.addr, "the transaction "+t.ID+" that wrote it is not signed, or its signature does not verify")
			continue
		}
		if verdict.Altered {
			problem(n.addr, "the transaction "+t.ID+" that wrote it was altered")
			continue
		}
		if role, _ := protect.RoleOf(readers, t.FinishSig.Key); role.Allows(protect.RoleWriter) {
			vouched = false // a writer needs no vouching
		} else if !vouched {
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
		case vouched:
			v.Vouched++
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
