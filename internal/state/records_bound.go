package state

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

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

// Vouches says which parts of the transaction t an admin's vouch carries over. It is the one
// place that compares a vouch with a transaction: the key must be the one that signed that
// part (or the unsigned key, for a transaction nobody signed) and the digest the one the
// part has now.
type Vouches struct {
	// Finish: the vouch is for the signed end of t.
	Finish bool
	// Start: the vouch is for the signed start of t, which was thought dead or did not end.
	Start bool
	// Unsigned: the vouch is for a transaction that nobody signed.
	Unsigned bool
	// Ended: the end is carried over, either by a vouch for it, or by one for the start of a run
	// that finished since under the same key.
	Ended bool
}

// VouchesOf compares the vouch for t, if there is one, with t.
func (r RecordRules) VouchesOf(lineage string, t *Transaction) Vouches {
	vo, ok := r.Vouched[t.ID]
	if !ok {
		return Vouches{}
	}
	var v Vouches
	if t.FinishSig != nil && vo.Key == t.FinishSig.Key && vo.Digest == VouchDigest(lineage, t) {
		v.Finish = true
	}
	if t.StartSig != nil && vo.Key == t.StartSig.Key && vo.Digest == startDigest(lineage, t) {
		v.Start = true
	}
	if t.StartSig == nil && t.FinishSig == nil && vo.Key == protect.UnsignedKey && vo.Digest == VouchDigest(lineage, t) {
		v.Unsigned = true
	}
	v.Ended = v.Finish || (v.Start && t.FinishSig != nil && t.FinishSig.Key == t.StartSig.Key)
	return v
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
		if data, err := s.txnBytes(s.cx(), id); err == nil {
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
		if r := s.nodes[a]; r != nil {
			for _, id := range []string{r.Txn, r.Resealed} {
				if id != "" && !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
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

// boundNode is a record that a pointer names, and what the pointer says about who wrote it.
type boundNode struct {
	addr, rec, txn, resealed string
	revision                 int
}

// recordCheck judges the records of one state against the transactions that wrote them.
type recordCheck struct {
	lookup  func(id string) *Transaction
	rules   RecordRules
	readers []protect.Reader
	lineage string
	v       RecordVerdict
}

func (s *Store) verifyRecords(lookup func(id string) *Transaction, rules RecordRules, addrs []string) RecordVerdict {
	var nodes []boundNode
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
			nodes = append(nodes, boundNode{addr, ref.Record, r.Txn, r.Resealed, r.Revision})
		}
	}
	s.mu.Unlock()
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].addr < nodes[j].addr })

	c := &recordCheck{lookup: lookup, rules: rules, readers: rules.Readers, lineage: s.Lineage()}
	for _, n := range nodes {
		c.check(n)
	}
	return c.v
}

func (c *recordCheck) problem(addr, why string) {
	c.v.Problems = append(c.v.Problems, RecordProblem{addr, why})
}

// unknown notes a key the signature names that is not in the roles: a problem when the rules
// are strict.
func (c *recordCheck) unknown(addr, txn string) {
	if c.rules.Strict {
		c.problem(addr, "the transaction "+txn+" that wrote it was signed by a key that is not in the roles, and no admin vouched for it")
		return
	}
	c.v.Unchecked++
}

// resealedTo says whether a rewrap that a writer signed sealed the record again.
func (c *recordCheck) resealedTo(id, addr string, revision int, rec string) bool {
	if id == "" {
		return false
	}
	t := c.lookup(id)
	if t == nil || t.Kind != "rewrap" || t.Finished == nil || t.FinishSig == nil {
		return false
	}
	verdict := VerifyTransaction(c.lineage, t, c.readers)
	if verdict.Altered {
		return false
	}
	switch verdict.Finish {
	case protect.SigVerified:
		if role, _ := protect.RoleOf(c.readers, t.FinishSig.Key); !role.Allows(protect.RoleWriter) {
			return false
		}
	case protect.SigUnknownKey:
		if !c.rules.VouchesOf(c.lineage, t).Finish {
			return false
		}
	default:
		return false
	}
	ch := changeOf(t, addr, revision)
	return ch != nil && ch.Action == "reseal" && ch.Record == rec
}

// check judges one record by the transaction that wrote it.
func (c *recordCheck) check(n boundNode) {
	if n.txn == "" {
		c.problem(n.addr, "it names no transaction that wrote it")
		return
	}
	t := c.lookup(n.txn)
	if t == nil {
		// Transactions are never collected: a record that names one that is not there
		// may name one that never was.
		c.problem(n.addr, "the transaction "+n.txn+" that wrote it is not in the state")
		return
	}
	switch {
	case t.Finished == nil || t.FinishSig == nil && t.Status == "running":
		c.unfinished(n, t)
	case t.FinishSig == nil && t.StartSig != nil:
		c.endUnsigned(n, t)
	case t.FinishSig == nil && t.StartSig == nil:
		c.unsigned(n, t)
	default:
		c.signed(n, t)
	}
}

// unfinished judges a record of a run that is still going or was killed. Its start is signed by
// a writer, or it is not a run of a writer.
func (c *recordCheck) unfinished(n boundNode, t *Transaction) {
	sv := VerifyTransaction(c.lineage, t, c.readers)
	role, _ := protect.RoleOf(c.readers, startKey(t))
	if c.rules.VouchesOf(c.lineage, t).Start && (t.Status == "running" || t.FinishSig == nil) && !role.Allows(protect.RoleWriter) && sv.Start != protect.SigInvalid {
		// An admin vouched for the start of a run that was killed: its records stay.
		c.v.Vouched++
		return
	}
	switch {
	case sv.Start == protect.SigUnknownKey:
		c.unknown(n.addr, t.ID)
	case sv.Start != protect.SigVerified || !role.Allows(protect.RoleWriter):
		c.problem(n.addr, "the transaction "+t.ID+" that wrote it was not started by a writer")
	default:
		c.v.Unchecked++
	}
}

// endUnsigned judges a record of a run that finished and whose end is not signed: the ssh-agent
// refused, or someone removed the signature. Nothing but the start is signed, so no record of it
// is bound; an admin who has looked at it vouches for it (pravo protect roles --adopt-unsigned).
func (c *recordCheck) endUnsigned(n boundNode, t *Transaction) {
	sv := VerifyTransaction(c.lineage, t, c.readers)
	if c.rules.VouchesOf(c.lineage, t).Start && sv.Start != protect.SigInvalid {
		c.v.Vouched++
		return
	}
	c.problem(n.addr, "the transaction "+t.ID+" that wrote it "+endUnsignedWhy+", and no admin vouched for it (pravo protect roles --adopt-unsigned)")
}

// unsigned judges a record written with no identity, before the state was sealed or by a job
// that had none. Only the transaction an admin vouched for by id and digest is accepted.
func (c *recordCheck) unsigned(n boundNode, t *Transaction) {
	if !c.rules.VouchesOf(c.lineage, t).Unsigned {
		c.problem(n.addr, "the transaction "+t.ID+" that wrote it is not signed, or its signature does not verify")
		return
	}
	switch ch := changeOf(t, n.addr, n.revision); {
	case ch == nil:
		c.problem(n.addr, "the transaction "+t.ID+" does not name revision "+strconv.Itoa(n.revision))
	case ch.Record != "" && ch.Record != n.rec && !c.resealedTo(n.resealed, n.addr, n.revision, n.rec):
		c.problem(n.addr, "the transaction "+t.ID+" signed another record for revision "+strconv.Itoa(n.revision))
	default:
		c.v.Vouched++
	}
}

// signed judges a record of a transaction whose end is signed.
func (c *recordCheck) signed(n boundNode, t *Transaction) {
	verdict := VerifyTransaction(c.lineage, t, c.readers)
	vouched := c.rules.VouchesOf(c.lineage, t).Ended
	switch verdict.Finish {
	case protect.SigUnknownKey:
		if !vouched {
			c.unknown(n.addr, t.ID)
			return
		}
	case protect.SigVerified:
	default:
		c.problem(n.addr, "the transaction "+t.ID+" that wrote it is not signed, or its signature does not verify")
		return
	}
	if verdict.Altered {
		c.problem(n.addr, "the transaction "+t.ID+" that wrote it was altered")
		return
	}
	if role, _ := protect.RoleOf(c.readers, t.FinishSig.Key); role.Allows(protect.RoleWriter) {
		vouched = false // a writer needs no vouching
	} else if !vouched {
		c.problem(n.addr, "the transaction "+t.ID+" that wrote it was run by "+verdict.Signer+", who is a "+role.String()+" and may not write")
		return
	}
	switch ch := changeOf(t, n.addr, n.revision); {
	case ch == nil:
		c.problem(n.addr, "the transaction "+t.ID+" does not name revision "+strconv.Itoa(n.revision))
	case ch.Record == "":
		c.v.Earlier++
	case ch.Record != n.rec && !c.resealedTo(n.resealed, n.addr, n.revision, n.rec):
		c.problem(n.addr, "the transaction "+t.ID+" signed another record for revision "+strconv.Itoa(n.revision))
	case vouched:
		c.v.Vouched++
	default:
		c.v.Bound++
	}
}

// endUnsignedWhy is the part of a problem that says a transaction finished with its end not
// signed and no admin vouched for it.
const endUnsignedWhy = "finished with its end not signed"

// AllEndUnsigned says whether every problem is that: nothing is wrong with who wrote the
// records, only the end of their run has no signature for an admin to vouch for.
func AllEndUnsigned(ps []RecordProblem) bool {
	for _, p := range ps {
		if !strings.Contains(p.Why, endUnsignedWhy) {
			return false
		}
	}
	return len(ps) > 0
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
