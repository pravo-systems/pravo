package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// Change is one entry of a transaction.
type Change struct {
	Addr   string `json:"addr"`
	Action string `json:"action"`
	Before int    `json:"before_revision,omitempty"`
	After  int    `json:"after_revision,omitempty"`
	// Record is the key of the immutable record the change wrote (rec/ and the SHA-256
	// of its bytes), set in a state whose readers file gives roles. The finish
	// signature covers it, so a record that no signed transaction of a writer names is
	// not one a writer made (see VerifyRecords).
	Record string `json:"record,omitempty"`
	// Diff is what the change did to the attributes of the record. A sealed state
	// keeps it in Transaction.DiffSealed and leaves this empty.
	Diff *Diff `json:"diff,omitempty"`
}

// Transaction records one plan, apply, destroy or import run.
type Transaction struct {
	ID       string     `json:"id"`
	Kind     string     `json:"kind"`
	Command  string     `json:"command"`
	User     string     `json:"user,omitempty"`
	Host     string     `json:"host,omitempty"`
	Started  time.Time  `json:"started"`
	Finished *time.Time `json:"finished,omitempty"`
	Status   string     `json:"status"`
	Summary  string     `json:"summary,omitempty"`
	// SummarySealed holds the full text of the error of a run that failed or
	// was rejected, in a sealed state, where Summary says only that the text is
	// sealed. It is bound to the id of the transaction.
	SummarySealed *protect.Sealed `json:"summary_sealed,omitempty"`
	// DiffSealed holds, in a sealed state, the Diff of each change by address. It is
	// bound to the id of the transaction and is not covered by the signatures.
	DiffSealed *protect.Sealed `json:"diff_sealed,omitempty"`
	Changes    []Change        `json:"changes"`
	// Actor, Reason and GitRevision attribute the run: who (a person or
	// an agent), why, and which revision of the specification.
	Actor       string `json:"actor,omitempty"`
	Reason      string `json:"reason,omitempty"`
	GitRevision string `json:"git_revision,omitempty"`
	// TraceID links the transaction to its OpenTelemetry trace.
	TraceID string `json:"trace_id,omitempty"`
	// Version is the pravo that wrote the transaction. Transactions written
	// before it was recorded have none.
	Version string `json:"version,omitempty"`
	// PlanID and PlanDigest name the plan an apply ran from: the id of a stored plan,
	// and the SHA-256 of the plan file in hex. They are signed with the rest.
	PlanID     string `json:"plan,omitempty"`
	PlanDigest string `json:"plan_digest,omitempty"`
	// Policies lists the versions of the policies that judged the plan, each as
	// NAME@SEQ:SHA-256 of its text, sorted. It is signed with the start.
	Policies []string `json:"policies,omitempty"`
	// PolicyWrites lists the policy versions the run wrote, each as NAME@SEQ:SHA-256 of
	// its text (the digest of the empty text for a removal), sorted. It is signed with the
	// end, so that a version that is deleted from the state later is missed.
	PolicyWrites []string `json:"policy_writes,omitempty"`
	// StartSig and FinishSig are the signatures of the identity that ran the command,
	// in a sealed state (see sign.go). The first covers what the run set out to do, and
	// the second what it did; a run that is killed leaves the first.
	StartSig  *protect.Signature `json:"start_sig,omitempty"`
	FinishSig *protect.Signature `json:"finish_sig,omitempty"`
	// Removes lists, sorted, the addresses whose pointers the run may delete, in a sealed
	// state. It is signed with the start, and it is what tells a pointer that a running
	// command, or one that was killed, deleted from one that is gone with nothing to
	// account for it (see Store.MissingNodes). The finished transaction says what was
	// removed in its changes and keeps only RemovesDigest, the SHA-256 of the addresses
	// joined by newlines, which is what the signature of the start covers.
	Removes       []string `json:"removes,omitempty"`
	RemovesDigest string   `json:"removes_digest,omitempty"`
}

// Txn is an in-progress transaction.
type Txn struct {
	store *Store
	mu    sync.Mutex
	t     Transaction
	// started is set once the transaction was written as running (see Start).
	started bool
	// removing holds the addresses of t.Removes.
	removing map[string]bool
}

// Begin starts a transaction of the given kind (plan, apply, destroy, import, edit).
func (s *Store) Begin(kind, command string) *Txn {
	host, _ := os.Hostname()
	return &Txn{store: s, t: Transaction{
		ID: newTxnID(), Kind: kind, Command: command,
		User: osUser(), Host: host, Started: time.Now().UTC(), Status: "running", Changes: []Change{},
		Actor: s.actor, Reason: s.reason, GitRevision: s.gitRevision, TraceID: s.traceID, Version: PravoVersion,
	}}
}

// ID returns the transaction id.
func (t *Txn) ID() string { return t.t.ID }

// Start writes the transaction as it is, with the status running. A command
// that is killed after Start leaves a transaction that is still running; Finish
// replaces it.
func (t *Txn) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.start()
}

// Ensure starts the transaction unless it was started. A command calls it before the first
// record it writes: a record names the transaction that wrote it, and the transaction is in
// the state before the record is, so that a run that is killed leaves a record whose
// transaction is there (and running, which reads as not checked) and never one that no
// transaction accounts for. A command that writes no record does not call it.
func (t *Txn) Ensure() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started {
		return nil
	}
	return t.start()
}

// Started reports whether the transaction was written as running.
func (t *Txn) Started() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.started
}

// start is Start for a caller that holds the lock of t. Started again, the transaction is
// written and signed again, with what was set since.
func (t *Txn) start() error {
	if err := t.store.writable(); err != nil {
		return err
	}
	// A sealed state records no run that its owner cannot be named for: nothing has
	// been changed yet, so a run that cannot sign is refused here.
	sort.Strings(t.t.Removes)
	if err := t.signStart(); err != nil {
		return err
	}
	if err := t.store.putJSON("txn/"+t.t.ID+".json", t.t); err != nil {
		return err
	}
	t.store.forgetFacts()
	t.store.markKind(t.t.ID, t.t.Kind)
	if !t.started {
		t.store.running(t, true)
	}
	t.started = true
	return nil
}

// WillRemove says that the run may delete the pointers of addrs, in a sealed state. A run
// deletes a pointer before it has finished: when it destroys a resource, replaces one, moves
// one or forgets one. Until the transaction is finished and signed, nothing else in the state
// says that the pointer went with a command of a writer, and a command of anyone else that
// found it gone would take the state for one that was tampered with. So the addresses are in
// the transaction that is written when the run starts, under its first signature.
//
// It is called before Start with every address the run knows of. Called after, it writes
// and signs the running transaction again when it adds an address, which costs a request:
// Store.Remove does that for an address nobody announced.
func (t *Txn) WillRemove(addrs ...string) error {
	if !t.store.sealedState() {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	added := false
	for _, a := range addrs {
		if t.removing[a] {
			continue
		}
		if t.removing == nil {
			t.removing = map[string]bool{}
		}
		t.removing[a] = true
		t.t.Removes = append(t.t.Removes, a)
		added = true
	}
	if !added || !t.started {
		return nil
	}
	return t.start()
}

// removes reports whether the run said that it may delete the pointer of addr.
func (t *Txn) removes(addr string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.removing[addr]
}

// running keeps, or drops, a transaction that was written as running and is not finished.
func (s *Store) running(t *Txn, on bool) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.run = slices.DeleteFunc(s.run, func(o *Txn) bool { return o == t })
	if on {
		s.run = append(s.run, t)
	}
}

// announceRemoval makes sure that a running transaction of this store says that it may
// delete the pointer of addr, before the pointer is deleted. The newest one is told when
// none does. A store with no running transaction deletes as it did: the removal is in no
// transaction until one is finished with it.
func (s *Store) announceRemoval(addr string) error {
	if !s.sealedState() {
		return nil
	}
	s.runMu.Lock()
	run := slices.Clone(s.run)
	s.runMu.Unlock()
	if len(run) == 0 {
		return nil
	}
	for _, t := range run {
		if t.removes(addr) {
			return nil
		}
	}
	return run[len(run)-1].WillRemove(addr)
}

// removesDigest is what the signature of the start covers of the addresses the run may
// remove: the SHA-256 of the sorted addresses joined by newlines, or "" when there are none.
func removesDigest(t *Transaction) string {
	if len(t.Removes) == 0 {
		return t.RemovesDigest
	}
	sorted := slices.Clone(t.Removes)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(sum[:])
}

// Record adds a change to the transaction.
func (t *Txn) Record(addr, action string, before, after int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := Change{Addr: addr, Action: action, Before: before, After: after}
	if t.store.bindsRecords() && after > 0 {
		c.Record = t.store.heldRecord(addr)
	}
	t.t.Changes = append(t.t.Changes, c)
}

// RecordWith adds a change that names what it wrote itself, as the change of a secret
// names the commit of the version: the finish signature covers record, whatever the roles.
func (t *Txn) RecordWith(addr, action string, before, after int, record string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.t.Changes = append(t.t.Changes, Change{Addr: addr, Action: action, Before: before, After: after, Record: record})
}

// Finish writes the transaction with the given status and summary. A
// transaction without changes is not recorded unless record is true.
func (t *Txn) Finish(status, summary string, record bool) error {
	return t.FinishError(status, summary, "", record)
}

// FinishError is Finish for a run that failed or was rejected, whose full text
// can hold a value that a provider returned. In a sealed state summary is what
// anyone who can read the bucket sees, and full is kept sealed for the readers
// (see Store.SummaryText); when the store holds no key, or the state is not
// sealed, full is dropped and summary is all there is. full is empty when there
// is nothing to keep.
func (t *Txn) FinishError(status, summary, full string, record bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	defer t.store.running(t, false)
	if len(t.t.Changes) == 0 && !record && !t.started {
		return nil
	}
	// The changes say what was removed. The digest stays, for the first signature.
	t.t.RemovesDigest, t.t.Removes = removesDigest(&t.t), nil
	now := time.Now().UTC()
	t.t.Finished = &now
	t.t.Status = status
	t.t.Summary = summary
	t.t.SummarySealed = nil
	if sl := t.store.Sealer(); sl != nil && full != "" {
		if box, err := sl.Seal(protect.KindSummary, t.t.ID, []byte(full)); err == nil {
			t.t.SummarySealed = &box
		}
	}
	sort.Slice(t.t.Changes, func(i, j int) bool { return t.t.Changes[i].Addr < t.t.Changes[j].Addr })
	if t.store.sealedState() {
		t.store.sealDiffs(&t.t)
	}
	if err := t.store.writable(); err != nil {
		return err
	}
	// The cloud has been changed by now. A signature that cannot be made does not
	// lose the record of what happened: it is written without the signature, and the
	// error says so.
	signErr := t.signFinish()
	if err := t.store.putJSON("txn/"+t.t.ID+".json", t.t); err != nil {
		return err
	}
	t.store.forgetFacts()
	if !t.started {
		// A run that never started is written once, here, and gets its hint now.
		t.store.markKind(t.t.ID, t.t.Kind)
	}
	if signErr != nil {
		// The command has done its work. It reports the signature that is missing and does not
		// fail for it, since a failure would have its caller treat the apply as not done.
		if warn := t.store.warnFunc(); warn != nil {
			warn(signErr.Error())
			return nil
		}
	}
	return signErr
}

// SummaryText returns the summary of t as the holder of this store may read it:
// the full text of the error that a sealed state keeps for its readers when the
// store can open it, and the summary that anyone sees otherwise.
func (s *Store) SummaryText(t Transaction) string {
	sl := s.Sealer()
	if sl == nil || t.SummarySealed == nil {
		return t.Summary
	}
	full, err := sl.Open(protect.KindSummary, t.ID, *t.SummarySealed)
	if err != nil {
		return t.Summary
	}
	return string(full)
}

// resealSummaries seals the text of the errors that transactions keep sealed again
// under the current group, as the records are by a rewrap. A box that does not open
// is left as it is: a transaction is a log, and one that is damaged is not a reason
// to stop.
func (s *Store) resealSummaries(ctx context.Context, current string) error {
	sl := s.Sealer()
	keys, err := s.b.List(ctx, "txn")
	if err != nil {
		return err
	}
	for _, k := range keys {
		if !strings.HasSuffix(k, ".json") {
			continue
		}
		data, err := s.b.Get(ctx, k)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		var t Transaction
		if json.Unmarshal(data, &t) != nil {
			continue
		}
		changed := false
		for _, b := range []struct {
			box  **protect.Sealed
			kind string
		}{{&t.SummarySealed, protect.KindSummary}, {&t.DiffSealed, protect.KindDiff}} {
			if *b.box == nil || (*b.box).Group == current {
				continue
			}
			plain, err := sl.Open(b.kind, t.ID, **b.box)
			if err != nil {
				continue
			}
			box, err := sl.Seal(b.kind, t.ID, plain)
			clear(plain)
			if err != nil {
				return err
			}
			*b.box = &box
			changed = true
		}
		if !changed {
			continue
		}
		next, err := indentJSON(t)
		if err != nil {
			return err
		}
		if _, err := s.b.ReplaceIf(ctx, k, func(cur []byte) ([]byte, bool) { return next, bytes.Equal(cur, data) }); err != nil && !errors.Is(err, ErrNotFound) {
			return Advise(err)
		}
	}
	return nil
}

// Transactions lists recorded transactions, newest first.
func (s *Store) Transactions() ([]Transaction, error) {
	keys, err := s.b.List(s.cx(), "txn")
	if err != nil {
		return nil, err
	}
	var jsonKeys []string
	for _, k := range keys {
		if strings.HasSuffix(k, ".json") {
			jsonKeys = append(jsonKeys, k)
		}
	}
	// Remote backends pay a round trip per object, and a state has one per command that did work.
	read := make([]*Transaction, len(jsonKeys))
	s.eachRead(len(jsonKeys), func(i int) {
		k := jsonKeys[i]
		data, err := s.b.Get(s.cx(), k)
		if err != nil {
			return
		}
		var t Transaction
		// A transaction is in the object that has its id: a copy under another name is
		// not another transaction.
		if json.Unmarshal(data, &t) == nil && k == "txn/"+t.ID+".json" {
			if t.Changes == nil {
				t.Changes = []Change{}
			}
			read[i] = &t
		}
	})
	out := []Transaction{}
	for _, t := range read {
		if t != nil {
			out = append(out, *t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out, nil
}

// LatestTransaction reads the newest recorded transaction, nil when there is none. Ids begin
// with the time to the second and end in random hex, so only the objects of the last second
// are read, and the newest of them by its start.
func (s *Store) LatestTransaction() (*Transaction, error) {
	keys, err := s.b.List(s.cx(), "txn")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, k := range keys {
		if strings.HasSuffix(k, ".json") {
			ids = append(ids, k)
		}
	}
	sort.Strings(ids)
	var newest *Transaction
	for i := len(ids) - 1; i >= 0; i-- {
		at, _, _ := strings.Cut(strings.TrimPrefix(ids[i], "txn/"), "-")
		if i+1 < len(ids) {
			// A different second than the one after ends the search.
			if next, _, _ := strings.Cut(strings.TrimPrefix(ids[i+1], "txn/"), "-"); next != at && newest != nil {
				break
			}
		}
		data, err := s.b.Get(s.cx(), ids[i])
		if err != nil {
			return nil, err
		}
		var t Transaction
		if json.Unmarshal(data, &t) != nil || ids[i] != "txn/"+t.ID+".json" {
			continue
		}
		if t.Changes == nil {
			t.Changes = []Change{}
		}
		if newest == nil || t.Started.After(newest.Started) {
			c := t
			newest = &c
		}
	}
	return newest, nil
}

// Len returns the number of recorded changes.
func (t *Txn) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.t.Changes)
}

// KindDir holds one small object per transaction, kinds/<id>.<kind>, written when the
// transaction starts. It is only a hint of what the transaction object says, so that a reader
// that looks for the newest apply or destroy skips a plan, an edit or a secret set without
// reading it. A transaction of an earlier pravo has no hint and is read, and so is one whose
// hint names a kind that is wanted. A hint is never the answer: the answer is always taken from
// a transaction object, and a hint only decides which objects are not read.
//
// In a sealed state the name ends in the signature of the writer, kinds/<id>.<kind>.<key>-<sig>:
// the signature of the id and the kind under the key of the identity that ran the transaction,
// which a reader checks against the readers file and needs no key of the state to do. A hint
// without a valid signature is not believed and its transaction is read, so someone who can
// write the bucket cannot make a transaction look like another kind, and a hint they delete or
// replace only costs a read. A writer whose signing key lives in an ssh-agent writes no hint in
// a sealed state, so as not to ask the agent (and its owner's touch) for one more signature
// per run; its transactions are read.
const KindDir = "kinds"

// hintParts are what the signature of a hint covers.
func hintParts(id, kind string) [][]byte { return [][]byte{text(id), text(kind)} }

// hintSuffix is the part of the name of a hint that proves it: the first bytes of the SHA-256
// of the fingerprint of the key, which tell the readers apart without a dot in the name, and
// the signature, in hex.
func hintSuffix(sig protect.Signature) string {
	sum := sha256.Sum256([]byte(sig.Key))
	return hex.EncodeToString(sum[:8]) + "-" + hex.EncodeToString(sig.Sig)
}

// markKind writes the hint of a transaction. A hint that cannot be written is not an error:
// the transaction is then read when it is looked for.
func (s *Store) markKind(id, kind string) {
	if kind == "" || strings.ContainsAny(kind, "./") {
		return
	}
	name := KindDir + "/" + id + "." + kind
	if s.sealedState() {
		who := s.signingIdentity()
		if who == nil || who.SignVia != "file" {
			return
		}
		sig, err := who.SignMessage(protect.DomainTxnKind, s.Lineage(), hintParts(id, kind)...)
		if err != nil {
			return
		}
		name += "." + hintSuffix(sig)
	}
	_ = s.b.Put(s.cx(), name, []byte("{}\n"))
}

// hintVerified reports whether the suffix of a hint is the signature of a reader on the id and
// the kind.
func (s *Store) hintVerified(readers []protect.Reader, id, kind, suffix string) bool {
	prefix, sigHex, ok := strings.Cut(suffix, "-")
	if !ok {
		return false
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	for _, r := range readers {
		sum := sha256.Sum256([]byte(protect.SSHFingerprint(r.Sign)))
		if len(r.Sign) == 0 || hex.EncodeToString(sum[:8]) != prefix {
			continue
		}
		st, _ := protect.VerifySignature(&protect.Signature{Key: protect.SSHFingerprint(r.Sign), Sig: sig}, readers, protect.DomainTxnKind, s.Lineage(), hintParts(id, kind)...)
		return st == protect.SigVerified
	}
	return false
}

// kindHints returns the kind that the hints name, per transaction id, for the hints that are
// believed: in a sealed state those whose signature a reader made. A transaction with two hints
// that name different kinds is left out, so it is read.
func (s *Store) kindHints(readers []protect.Reader) (map[string]string, error) {
	keys, err := s.b.List(s.cx(), KindDir)
	if err != nil {
		return nil, err
	}
	sealed := s.sealedState()
	out := make(map[string]string, len(keys))
	conflict := map[string]bool{}
	for _, k := range keys {
		parts := strings.Split(strings.TrimPrefix(k, KindDir+"/"), ".")
		if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" {
			continue
		}
		id, kind := parts[0], parts[1]
		if sealed && (len(parts) != 3 || !s.hintVerified(readers, id, kind, parts[2])) {
			continue
		}
		if prev, ok := out[id]; ok && prev != kind {
			conflict[id] = true
		}
		out[id] = kind
	}
	for id := range conflict {
		delete(out, id)
	}
	return out, nil
}

// LatestOfKinds reads the newest recorded transaction of one of the kinds (such as "apply"
// and "destroy"), nil when there is none. It goes back from the newest object and stops at
// the first match, so a plan, an edit or a secret that came after does not hide it. A
// transaction whose hint (see KindDir) names another kind is not read, so the cost does not
// grow with the plans, edits and secrets that came after the match.
func (s *Store) LatestOfKinds(readers []protect.Reader, kinds ...string) (*Transaction, error) {
	keys, err := s.b.List(s.cx(), "txn")
	if err != nil {
		return nil, err
	}
	hints, err := s.kindHints(readers)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, k := range keys {
		if strings.HasSuffix(k, ".json") {
			ids = append(ids, k)
		}
	}
	sort.Strings(ids)
	var best *Transaction
	for i := len(ids) - 1; i >= 0; i-- {
		at, _, _ := strings.Cut(strings.TrimPrefix(ids[i], "txn/"), "-")
		if best != nil {
			// Ids begin with the time to the second: an earlier second cannot be newer.
			if bat, _, _ := strings.Cut(best.ID, "-"); at != bat {
				break
			}
		}
		if k, ok := hints[strings.TrimSuffix(strings.TrimPrefix(ids[i], "txn/"), ".json")]; ok && !slices.Contains(kinds, k) {
			continue
		}
		data, err := s.b.Get(s.cx(), ids[i])
		if err != nil {
			return nil, err
		}
		var t Transaction
		if json.Unmarshal(data, &t) != nil || ids[i] != "txn/"+t.ID+".json" || !slices.Contains(kinds, t.Kind) {
			continue
		}
		if best == nil || t.Started.After(best.Started) {
			tt := t
			best = &tt
		}
	}
	return best, nil
}
