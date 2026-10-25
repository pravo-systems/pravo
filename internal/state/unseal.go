package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// ErrNotSealed is returned by Unseal for a state that is not sealed.
var ErrNotSealed = errors.New("the state is not sealed")

// ErrPlansInUse is returned by Unseal when a stored plan that is sealed is in use by
// an apply: the plan cannot be opened once the seal is off, so the state is left
// unsealing and the command is run again when the apply is done.
var ErrPlansInUse = errors.New("stored plans are in use")

// UnsealOptions say how to take the seal off a store.
type UnsealOptions struct {
	// Identity must be a reader of every group that something is sealed in.
	Identity *protect.Identity
	// By is the name recorded in the history.
	By string
	// Progress is called as records are converted. It may be called from several
	// goroutines.
	Progress func(done, total int)
	// Plans unseals the stored plans, which the state package does not know. It
	// returns how many it unsealed and how many it left because an apply uses them.
	// Nil means that the caller has no stored plans: any that are sealed are lost
	// when the seal comes off.
	Plans func(ctx context.Context) (unsealed, skipped int, err error)
	// PlanGroups counts the sealed objects of the stored plans for each group, so that
	// an unseal that this key cannot finish is refused before it writes. Nil skips the
	// check for the plans.
	PlanGroups func(ctx context.Context) (map[string]int, error)
}

// UnsealResult says what an unseal did.
type UnsealResult struct {
	// Records is the number of records written in the open in this run.
	Records int
	// PlansUnsealed is the number of stored plans that were unsealed.
	PlansUnsealed int
	// Resumed is set when an unseal that was begun earlier was finished.
	Resumed bool
}

// Unseal takes the seal off a sealed state. The format stays what it is: the state
// is the same state in the same lineage, with the layout of format 2 and without
// the sealed part of meta.json.
//
//  1. meta.json gets a "migration" key with Unseal set. From here this pravo and
//     the others refuse to write the store (ErrUnsealing), and it is still sealed.
//  2. Each sealed record is written again in the open, by a compare-and-swap of its
//     pointer on the bytes that were read, and the sealed record is deleted.
//  3. outputs.json is written in the open.
//  4. o.Plans unseals the stored plans, and the text of the errors of transactions
//     is written back. A plan that an apply uses is not unsealed; the command stops
//     there, with the state still sealed, and goes on when it is run again.
//  5. The sealed records that nothing names any more are deleted.
//  6. meta.json is replaced without "sealed" and "migration", with the highest
//     number of a group in "groups_used" and an entry of the history.
//
// A crash leaves the store in step 1 to 5, and the next run goes on from there: a
// record that is in the open is skipped. The store is at rest while it runs.
//
// What sealing replaced by a tag, the values of -t in the command lines of
// transactions that were recorded before the seal, is not restored: they were not
// kept.
func (s *Store) Unseal(ctx context.Context, o UnsealOptions) (UnsealResult, error) {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	var res UnsealResult
	if o.Identity == nil || o.Identity.Reader == nil {
		return res, fmt.Errorf("unsealing a state needs the identity of one of its readers: %w", ErrNotReader)
	}
	s.mu.Lock()
	sealed, mig := s.meta.Sealed != nil, s.meta.Migration
	s.mu.Unlock()
	switch {
	case !sealed:
		return res, ErrNotSealed
	case mig != nil && !mig.Unseal:
		return res, errors.New("a seal is in progress; run pravo protect init --encrypt to finish it before the seal is taken off")
	}
	started := mig != nil
	if started {
		res.Resumed = true
		if err := s.checkLocks(); err != nil {
			return res, err
		}
	} else if err := s.checkAtRest(); err != nil {
		return res, err
	}
	wasLocked := s.Locked()
	if wasLocked {
		if err := s.openSealed(o.Identity); err != nil {
			return res, err
		}
		if s.Locked() {
			return res, fmt.Errorf("identity %s is not a reader of the state at %s: %w", o.Identity.Name, s.b.Name(), ErrNotReader)
		}
	}
	if err := s.refuseUnopenable(ctx, o); err != nil {
		return res, err
	}
	by := o.By
	if by == "" {
		by = o.Identity.Name
	}
	if err := s.beginUnseal(by); err != nil {
		return res, err
	}
	// A process that last looked at meta.json just before the mark still writes, sealed,
	// until it looks again. Wait that out, and see that nothing is held, before any
	// record is converted: what it wrote is then a record to convert and not one that
	// shows up after the seal is off.
	if err := s.waitForWriters(ctx); err != nil {
		return res, err
	}

	var noted atomic.Int64
	progress := func(done, total int) {
		if last := noted.Load(); int64(done)-last >= migrationNoteEvery && noted.CompareAndSwap(last, int64(done)) {
			_ = s.updateMeta(func(m *meta) error {
				if m.Migration != nil {
					m.Migration.Done, m.Migration.Total = done, total
				}
				return nil
			})
		}
		if o.Progress != nil {
			o.Progress(done, total)
		}
	}
	n, err := s.convertNodes(ctx, rewrite{
		needs: func(group string, _ bool) bool { return group != "" },
		encode: func(r *Resource, _ []byte) ([]byte, string, error) {
			data, err := indentJSON(r)
			return data, "", err
		},
		dropOld: true,
	}, progress)
	res.Records = n
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			err = fmt.Errorf("interrupted after %d records were unsealed in this run; the rest are left as they were, and the state is left unsealing. Run the command again to finish: %w", n, err)
		}
		return res, err
	}
	_ = s.updateMeta(func(m *meta) error {
		if m.Migration != nil {
			m.Migration.Done = m.Migration.Total
		}
		return nil
	})
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("interrupted before the outputs and the stored plans were unsealed; run the command again to finish: %w", err)
	}
	if err := s.unsealOutputs(); err != nil {
		return res, err
	}
	if o.Plans != nil {
		var skipped int
		if res.PlansUnsealed, skipped, err = o.Plans(ctx); err != nil {
			return res, err
		}
		if skipped > 0 {
			return res, fmt.Errorf("%d stored plans are in use by an apply and are still sealed; run the command again when it is done: %w", skipped, ErrPlansInUse)
		}
	}
	if err := s.unsealSummaries(ctx); err != nil {
		return res, err
	}
	// Writers are refused, and the key is gone with the next write: a sealed record that
	// is young is as unreadable as an old one, so none is kept for a grace.
	if err := s.sweepRecordsGrace(func(group string) bool { return group == "" }, 0); err != nil {
		return res, err
	}
	if err := s.verifyUnsealed(ctx); err != nil {
		return res, err
	}
	err = s.updateMeta(func(m *meta) error {
		if m.Sealed == nil || m.Migration == nil || !m.Migration.Unseal {
			return errors.New("the state is not being unsealed")
		}
		m.GroupsUsed = max(m.GroupsUsed, highestGroup(m.Sealed))
		m.History = appendHistory(m.History, historyMeta{At: time.Now().UTC(), By: by, What: "unsealed", Removed: readerNames(m.Sealed)})
		m.Sealed, m.Migration = nil, nil
		return nil
	})
	if err != nil {
		return res, err
	}
	s.mu.Lock()
	s.openable, s.locked, s.you = nil, false, You{}
	if wasLocked {
		// The nodes are the shells that a locked store lists, without attributes.
		s.nodes, s.held, s.index = map[string]*Resource{}, map[string]nodeRef{}, nil
	}
	s.mu.Unlock()
	s.ring.set(nil)
	if wasLocked {
		if err := s.load(); err != nil {
			return res, fmt.Errorf("the seal is off, and the state could not be read again: %w", err)
		}
	}
	return res, nil
}

// waitForWriters waits until every process that could still write to the store sealed
// has looked at meta.json again, and then checks that no lock is held.
func (s *Store) waitForWriters(ctx context.Context) error {
	s.mu.Lock()
	var began time.Time
	if m := s.meta.Migration; m != nil {
		began = m.Started
	}
	s.mu.Unlock()
	if wait := 2*metaRecheck - time.Since(began); wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.checkLocks()
}

// verifyUnsealed checks, before the seal is taken off for good, that nothing the
// state names is still sealed: a record, and the outputs. A writer that wrote sealed
// after the records were converted is found here, while the keys can still open it,
// and the command is run again.
func (s *Store) verifyUnsealed(ctx context.Context) error {
	if key, err := s.firstSealedRecord(ctx); err != nil {
		return err
	} else if key != "" {
		return fmt.Errorf("%s is sealed again: a command wrote it while the seal was coming off. Nothing was lost; run the command again", key)
	}
	raw, err := s.b.Get(s.cx(), "outputs.json")
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if _, sealed := asSealedOutputs(raw); sealed {
		return errors.New("outputs.json is sealed again: a command wrote it while the seal was coming off. Nothing was lost; run the command again")
	}
	return nil
}

// highestGroup is the largest number among the groups of m.
func highestGroup(m *protect.Meta) int {
	hi := 0
	for _, g := range m.Groups {
		if n, err := strconv.Atoi(strings.TrimPrefix(g.ID, "g")); err == nil && n > hi {
			hi = n
		}
	}
	return hi
}

// refuseUnopenable stops an unseal that this key cannot finish: something sealed in a
// group that it holds no lock in would be left sealed with no key, once the seal is
// off. It looks at the records, the outputs, the text of the errors of transactions and
// the stored plans. Nothing is written.
func (s *Store) refuseUnopenable(ctx context.Context, o UnsealOptions) error {
	bad := map[string]int{}
	s.mu.Lock()
	for _, ref := range s.held {
		if ref.Group != "" && !s.openable[ref.Group] {
			bad[ref.Group]++
		}
	}
	s.mu.Unlock()
	note := func(counts map[string]int) {
		s.mu.Lock()
		defer s.mu.Unlock()
		for g, n := range counts {
			if g != "" && !s.openable[g] {
				bad[g] += n
			}
		}
	}
	if raw, err := s.b.Get(s.cx(), "outputs.json"); err == nil {
		if env, sealed := asSealedOutputs(raw); sealed {
			note(map[string]int{env.Sealed.Group: 1})
		}
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if counts, err := s.sealedSummaries(ctx); err != nil {
		return err
	} else {
		note(counts)
	}
	if o.PlanGroups != nil {
		counts, err := o.PlanGroups(ctx)
		if err != nil {
			return err
		}
		note(counts)
	}
	if len(bad) == 0 {
		return nil
	}
	var parts []string
	for g, n := range bad {
		parts = append(parts, fmt.Sprintf("%d in group %s", n, g))
	}
	sort.Strings(parts)
	return fmt.Errorf("objects are sealed in groups that your key cannot open (%s). A reader of those groups can run pravo protect rewrap --all first, which seals everything again under the current group: %w", strings.Join(parts, ", "), ErrNotReader)
}

// beginUnseal marks meta.json as unsealing. A mark that is there already is left.
func (s *Store) beginUnseal(by string) error {
	total := s.Len()
	return s.updateMeta(func(m *meta) error {
		switch {
		case m.Sealed == nil:
			return ErrNotSealed
		case m.Migration != nil && !m.Migration.Unseal:
			return errors.New("another command began to seal the state; run the command again")
		case m.Migration != nil:
			return nil
		}
		m.Migration = &migrationMeta{To: m.Format, Started: time.Now().UTC(), By: by, Total: total, Unseal: true}
		return nil
	})
}

// unsealOutputs writes outputs.json in the open, while it holds the lock on the
// outputs. Outputs that are not sealed, or absent, are left alone.
func (s *Store) unsealOutputs() error {
	unlock, err := s.lockOutputs()
	if err != nil {
		return err
	}
	defer unlock()
	raw, err := s.b.Get(s.cx(), "outputs.json")
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, sealed := asSealedOutputs(raw); !sealed {
		return nil
	}
	outputs, err := s.decodeOutputs(raw)
	if err != nil {
		return err
	}
	if outputs == nil {
		outputs = map[string]json.RawMessage{}
	}
	data, err := indentJSON(outputs)
	if err != nil {
		return err
	}
	ok, err := s.b.ReplaceIf(s.cx(), "outputs.json", func(cur []byte) ([]byte, bool) { return data, bytes.Equal(cur, raw) })
	switch {
	case err != nil:
		return Advise(err)
	case !ok:
		return errors.New("outputs.json changed while it was unsealed; run the command again")
	}
	s.mu.Lock()
	s.outputs = outputs
	s.mu.Unlock()
	return nil
}

// unsealSummaries writes the text of the errors of transactions back in the open, as
// the seal moved it into a box. A box that does not open is left as it is: a
// transaction is a log, and one that is damaged is not a reason to stop.
func (s *Store) unsealSummaries(ctx context.Context) error {
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
		if json.Unmarshal(data, &t) != nil || t.SummarySealed == nil {
			continue
		}
		plain, err := sl.Open(protect.KindSummary, t.ID, *t.SummarySealed)
		if err != nil {
			continue
		}
		t.Summary, t.SummarySealed = string(plain), nil
		clear(plain)
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
