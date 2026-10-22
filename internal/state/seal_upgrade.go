package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// migrationNoteEvery is how many records an upgrade converts between two notes
// of its progress in meta.json.
const migrationNoteEvery = 200

// upgradeSealed seals a store: format 1, 2 or a seal that began and did not
// finish, to format 3.
//
//  1. meta.json is replaced with format 3, the group g1 with a lock for each
//     reader, and a "migration" key. From here a pravo that does not know the
//     format refuses the store, and this one refuses to write it (ErrSealing).
//  2. Each record is sealed, by a compare-and-swap of its object on the bytes
//     that were read, with the revision, generation and times it had.
//  3. outputs.json is sealed in place.
//  4. o.Plans seals the stored plans, and the command lines of the transactions
//     recorded so far lose the values of -t (redactHistory).
//  5. The immutable records that hold values in plain text and that nothing
//     names any more are deleted.
//  6. meta.json is replaced again, without "migration" and with an entry of the
//     history.
//
// A crash leaves the store in step 1 to 5, and the next run goes on from there:
// a record that is sealed is skipped.
func (s *Store) upgradeSealed(ctx context.Context, o UpgradeOptions) (UpgradeResult, error) {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	res := UpgradeResult{From: s.Format(), To: FormatSealed}
	if o.Identity == nil || o.Identity.Reader == nil {
		return res, fmt.Errorf("sealing a state needs the identity of one of its readers: %w", ErrNotReader)
	}
	s.mu.Lock()
	started := s.meta.Migration != nil
	sealed := s.meta.Format >= FormatSealed && !started
	s.mu.Unlock()
	if sealed {
		return res, nil // already sealed; nothing is written
	}
	if started {
		if err := s.checkLocks(); err != nil {
			return res, err
		}
	} else if err := s.checkAtRest(); err != nil {
		return res, err
	}
	if started {
		res.Resumed = true
		if s.Locked() {
			if err := s.openSealed(o.Identity); err != nil {
				return res, err
			}
		}
		if s.Locked() {
			return res, fmt.Errorf("the seal of the state at %s was begun by someone else, and identity %s is not one of its readers: %w", s.b.Name(), o.Identity.Name, ErrNotReader)
		}
	} else if err := s.beginSeal(o); err != nil {
		return res, err
	}

	by := o.By
	if by == "" {
		by = o.Identity.Name
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
		needs:   func(group string, isPtr bool) bool { return !isPtr || group == "" },
		encode:  func(r *Resource, _ []byte) ([]byte, string, error) { return s.encodeRecord(r) },
		dropOld: true,
	}, progress)
	res.Records = n
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			err = fmt.Errorf("interrupted after %d records were sealed in this run; the rest are left as they were, and the state is left migrating. Run the command again to finish: %w", n, err)
		}
		return res, err
	}
	if err := ctx.Err(); err != nil {
		return res, fmt.Errorf("interrupted before the outputs and the stored plans were sealed; run the command again to finish: %w", err)
	}
	if err := s.sealOutputs(); err != nil {
		return res, err
	}
	if o.Plans != nil {
		if res.PlansSealed, res.PlansSkipped, err = o.Plans(ctx); err != nil {
			return res, err
		}
	}
	if err := s.redactHistory(ctx); err != nil {
		return res, err
	}
	if err := s.sweepRecords(func(group string) bool { return group != "" }); err != nil {
		return res, err
	}
	err = s.updateMeta(func(m *meta) error {
		if m.Sealed == nil {
			return errors.New("the state is not being sealed")
		}
		m.Migration = nil
		m.History = appendHistory(m.History, historyMeta{At: time.Now().UTC(), By: by, What: "sealed", Added: readerNames(m.Sealed)})
		return nil
	})
	return res, err
}

// beginSeal makes the sealed part of meta.json and flips the store to format 3
// with the migration marker. It does so only if no one else has begun.
func (s *Store) beginSeal(o UpgradeOptions) error {
	if len(o.Readers) == 0 {
		return errors.New("sealing a state needs at least one reader")
	}
	me := o.Identity.Reader.Public()
	if !slices.ContainsFunc(o.Readers, func(r protect.Reader) bool { return r.Key.Equal(me) }) {
		return fmt.Errorf("identity %s is not one of the readers; sealing to a list you are not on would lock you out: %w", o.Identity.Name, ErrNotReader)
	}
	sealedMeta, keys, err := protect.NewMeta(s.Lineage(), o.Readers)
	if err != nil {
		return err
	}
	by := o.By
	if by == "" {
		by = o.Identity.Name
	}
	total := s.Len()
	err = s.updateMeta(func(m *meta) error {
		if m.Format >= FormatSealed || m.Sealed != nil {
			return errors.New("another command began to seal the state; run the command again")
		}
		m.Format, m.Sealed = FormatSealed, sealedMeta
		m.Migration = &migrationMeta{To: FormatSealed, Started: time.Now().UTC(), By: by, Total: total}
		return nil
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ring.set(keys)
	s.id, s.locked, s.metaRead = o.Identity, false, time.Now()
	s.openable = openableGroups(sealedMeta, o.Identity)
	s.you = You{State: "reader", Name: o.Identity.Name, Key: me.String()}
	s.mu.Unlock()
	return nil
}

func readerNames(m *protect.Meta) []string {
	for _, g := range m.Groups {
		if g.ID == m.Current {
			var out []string
			for _, l := range g.Locks {
				out = append(out, l.Name)
			}
			return out
		}
	}
	return nil
}

func appendHistory(h []historyMeta, e historyMeta) []historyMeta {
	h = append(h, e)
	if len(h) > historyKeep {
		h = h[len(h)-historyKeep:]
	}
	return h
}

// sealOutputs seals outputs.json in place, while it holds the lock on the
// outputs. Outputs that are sealed, or absent, are left alone.
func (s *Store) sealOutputs() error {
	unlock, err := s.lockOutputs()
	if err != nil {
		return err
	}
	defer unlock()
	return s.rewriteOutputs(func(raw []byte) (bool, error) {
		_, sealed := asSealedOutputs(raw)
		return !sealed, nil
	})
}

// rewriteOutputs replaces outputs.json with the same outputs sealed under the
// current group, if want says so of the bytes that are there. The caller holds the
// lock on the outputs.
func (s *Store) rewriteOutputs(want func(raw []byte) (bool, error)) error {
	raw, err := s.b.Get(s.ctx, "outputs.json")
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if ok, err := want(raw); err != nil || !ok {
		return err
	}
	outputs, err := s.decodeOutputs(raw)
	if err != nil {
		return err
	}
	if outputs == nil {
		outputs = map[string]json.RawMessage{}
	}
	data, err := s.encodeOutputs(outputs)
	if err != nil {
		return err
	}
	ok, err := s.b.ReplaceIf(s.ctx, "outputs.json", func(cur []byte) ([]byte, bool) { return data, bytes.Equal(cur, raw) })
	switch {
	case err != nil:
		return err
	case !ok:
		return errors.New("outputs.json changed while it was sealed; run the command again")
	}
	s.mu.Lock()
	s.outputs = outputs
	s.mu.Unlock()
	return nil
}

// namedRecords returns the keys of the immutable records that the pointers in the
// backend name, as they are now and not as this store read them.
func (s *Store) namedRecords() (map[string]bool, error) {
	keys, err := s.b.List(s.ctx, "resources")
	if err != nil {
		return nil, err
	}
	keys = jsonKeysOf(keys)
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		next atomic.Int64
		out  = map[string]bool{}
		fail error
	)
	width := readWidthLocal
	if !s.b.Local() {
		width = readWidthRemote
	}
	for w := 0; w < min(width, len(keys)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(keys) {
					return
				}
				data, err := s.b.Get(s.ctx, keys[i])
				mu.Lock()
				switch {
				case errors.Is(err, ErrNotFound):
				case err != nil:
					fail = errors.Join(fail, fmt.Errorf("reading %s: %w", keys[i], err))
				default:
					if p, isPtr, err := parsePointer(data); err == nil && isPtr {
						out[p.Record] = true
					}
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out, fail
}

// sweepGrace is how long an unnamed record is left alone by the sweep: the time
// a writer that opened the store before a rewrap goes on sealing in the old group
// (twice sealRecheck, for a recheck that is just missed), and a margin for the
// round trips of a slow bucket.
var sweepGrace = func() time.Duration { return 2*sealRecheck + 5*time.Second }

// sweepRecords deletes the immutable records that no pointer names and that keep
// does not keep. keep is given the group a record is sealed in, empty for a record
// that is not sealed. The store is at rest: a record that is not named is one that
// a write left behind, or a revision that was replaced.
// A record younger than sweepGrace is left alone.
func (s *Store) sweepRecords(keep func(group string) bool) error {
	keys, err := s.b.List(s.ctx, "rec")
	if err != nil {
		return err
	}
	named, err := s.namedRecords()
	if err != nil {
		return err
	}
	for _, k := range keys {
		if named[k] || !validRecKey(k) {
			continue
		}
		data, err := s.b.Get(s.ctx, k)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", k, err)
		}
		var head struct {
			Sealed    *protect.Sealed `json:"sealed"`
			UpdatedAt time.Time       `json:"updated_at"`
		}
		group := ""
		if json.Unmarshal(data, &head) == nil && head.Sealed != nil {
			group = head.Sealed.Group
		}
		if keep(group) {
			continue
		}
		// A sealed record that is young may be one that a stale writer has put and not
		// yet pointed at. A writer opened before a rewrap seals in the old group until
		// it looks at meta.json again, so its record is not one that keep keeps. A
		// record that is not sealed is deleted whatever its age: it holds what the
		// seal is there to remove, and a writer of the old layout is refused.
		if group != "" && time.Since(head.UpdatedAt) < sweepGrace() {
			continue
		}
		if err := s.b.Delete(s.ctx, k); err != nil {
			return fmt.Errorf("deleting %s: %w", k, err)
		}
	}
	return nil
}

// Rewrap makes the locks in meta.json match the readers. It needs a key that
// is a reader of every group, and returns ErrNotReader without one.
func (s *Store) Rewrap(ctx context.Context, o RewrapOptions) (RewrapResult, error) {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	var res RewrapResult
	if !s.sealedState() {
		return res, errors.New("the state is not sealed")
	}
	if err := s.writable(); err != nil {
		return res, err
	}
	s.mu.Lock()
	id := s.id
	s.mu.Unlock()
	if id == nil || id.Reader == nil {
		return res, ErrNotReader
	}
	by := o.By
	if by == "" {
		by = id.Name
	}
	var (
		change protect.Change
		keys   *protect.Keys
	)
	err := s.updateMeta(func(m *meta) error {
		if keys != nil {
			// An earlier attempt of this update, which found meta.json changed.
			keys.Close()
			keys = nil
		}
		k, err := m.Sealed.Unlock(m.Lineage, id.Reader)
		if err != nil {
			return fmt.Errorf("rewrapping the state: %w", err)
		}
		sealed := *m.Sealed
		sealed.Groups = cloneGroups(sealed.Groups)
		ch, err := sealed.Rewrap(k, o.Readers)
		if err != nil {
			k.Close()
			return err
		}
		before, _ := json.Marshal(m.Sealed)
		after, _ := json.Marshal(&sealed)
		change = ch
		if bytes.Equal(before, after) {
			k.Close()
			return errNoRewrap
		}
		keys = k
		m.Sealed = &sealed
		m.History = appendHistory(m.History, historyMeta{At: time.Now().UTC(), By: by, What: "rewrap", Added: ch.Added, Removed: ch.Removed})
		return nil
	})
	switch {
	case errors.Is(err, errNoRewrap):
	case err != nil:
		if keys != nil {
			keys.Close()
		}
		return res, err
	default:
		s.mu.Lock()
		s.ring.set(keys)
		s.openable = openableGroups(s.meta.Sealed, id)
		s.metaRead = time.Now()
		s.mu.Unlock()
	}
	res.Change = change
	if !o.All {
		return res, nil
	}
	if err := s.checkAtRest(); err != nil {
		return res, err
	}
	n, err := s.reseal(ctx, o, &res)
	res.Resealed = n
	if err != nil {
		return res, err
	}
	return res, s.updateMeta(func(m *meta) error {
		m.History = appendHistory(m.History, historyMeta{At: time.Now().UTC(), By: by, What: "rewrap --all"})
		return nil
	})
}

// errNoRewrap is the error of an update of meta.json that finds nothing to change.
var errNoRewrap = errors.New("the readers are the readers of the state")

// cloneGroups copies the groups, so that a rewrap that does not take effect
// changes nothing of the meta it was made from.
func cloneGroups(gs []protect.Group) []protect.Group {
	out := make([]protect.Group, len(gs))
	for i, g := range gs {
		g.Locks = slices.Clone(g.Locks)
		out[i] = g
	}
	return out
}

// reseal seals every record, the outputs, the stored plans and the sealed text of
// the errors of transactions again under the current group, and returns how many
// records it sealed. It sets the counts of the plans in res.
func (s *Store) reseal(ctx context.Context, o RewrapOptions, res *RewrapResult) (int, error) {
	s.mu.Lock()
	current := s.ring.current()
	s.mu.Unlock()
	n, err := s.convertNodes(ctx, rewrite{
		needs:   func(group string, isPtr bool) bool { return !isPtr || group != current },
		encode:  func(r *Resource, _ []byte) ([]byte, string, error) { return s.encodeRecord(r) },
		dropOld: true,
	}, o.Progress)
	if err != nil {
		return n, err
	}
	unlock, err := s.lockOutputs()
	if err != nil {
		return n, err
	}
	err = s.rewriteOutputs(func(raw []byte) (bool, error) {
		env, sealed := asSealedOutputs(raw)
		return !sealed || env.Sealed.Group != current, nil
	})
	unlock()
	if err != nil {
		return n, err
	}
	if o.Plans != nil {
		var err error
		res.PlansSealed, res.PlansSkipped, err = o.Plans(ctx)
		if err != nil {
			return n, err
		}
	}
	if err := s.resealSummaries(ctx, current); err != nil {
		return n, err
	}
	return n, s.sweepRecords(func(group string) bool { return group == current })
}
