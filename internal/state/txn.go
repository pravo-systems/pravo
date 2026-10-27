package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
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
	// StartSig and FinishSig are the signatures of the identity that ran the command,
	// in a sealed state (see sign.go). The first covers what the run set out to do, and
	// the second what it did; a run that is killed leaves the first.
	StartSig  *protect.Signature `json:"start_sig,omitempty"`
	FinishSig *protect.Signature `json:"finish_sig,omitempty"`
}

// Txn is an in-progress transaction.
type Txn struct {
	store *Store
	mu    sync.Mutex
	t     Transaction
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
	if err := t.store.writable(); err != nil {
		return err
	}
	// A sealed state records no run that its owner cannot be named for: nothing has
	// been changed yet, so a run that cannot sign is refused here.
	if err := t.signStart(); err != nil {
		return err
	}
	return t.store.putJSON("txn/"+t.t.ID+".json", t.t)
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
	if len(t.t.Changes) == 0 && !record {
		return nil
	}
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
	out := []Transaction{}
	for _, k := range keys {
		if !strings.HasSuffix(k, ".json") {
			continue
		}
		data, err := s.b.Get(s.cx(), k)
		if err != nil {
			continue
		}
		var t Transaction
		// A transaction is in the object that has its id: a copy under another name is
		// not another transaction.
		if json.Unmarshal(data, &t) == nil && k == "txn/"+t.ID+".json" {
			if t.Changes == nil {
				t.Changes = []Change{}
			}
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out, nil
}

// Len returns the number of recorded changes.
func (t *Txn) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.t.Changes)
}
