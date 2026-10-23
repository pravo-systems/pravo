package state

// A state of format 2 or 3 never overwrites a record, so it keeps every revision
// of every record, the record of a resource that was destroyed, and the records
// that a write left behind. Nothing else deletes them (a refresh that changes only
// the time of a record deletes its predecessor, and rewrap --all deletes the ones
// sealed in an earlier group). GC is the collection: it deletes the records that
// no pointer names once they are older than a grace and two collections, a settle
// interval apart, have found them so, and in a sealed state it drops the reader
// groups that nothing refers to any more, so that the data key of a reader who was
// removed opens nothing that is left.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// DefaultGCGrace is how old a record that no pointer names must be before GC
// deletes it, unless the caller says otherwise.
const DefaultGCGrace = 24 * time.Hour

// GCSettle is how long before a collection an earlier one must have found a record
// unnamed for the collection to delete it. A writer that copies records with the
// times they already have (rewrap --all, protect init, state push and pull) puts
// them before it replaces the pointers, and the age of the record says nothing of
// that; the second look does, as long as the writer is done within the interval.
// Tests set it.
var GCSettle = 15 * time.Minute

// gcMarksKey is the object that holds the marks of a collection: the records that
// a collection found unnamed, and when. It is the only object outside rec/ that GC
// writes, and a copy of the state does not carry it.
const gcMarksKey = "gc/marks.json"

// ErrNoRecords is GC's error for a state of format 1, which keeps one object for
// each resource and no record that a pointer could stop naming.
var ErrNoRecords = errors.New("the state is format 1: it keeps one object for each resource, and has nothing to collect")

// GCGrace returns the grace GC applies for the age a caller asked for: the default
// for zero, and never less than the time a writer that opened the store before a
// rewrap goes on sealing in the old group, which the sweep of an upgrade leaves a
// record alone for. raised says that the age asked for was below that.
func GCGrace(requested time.Duration) (grace time.Duration, raised bool) {
	if requested <= 0 {
		return DefaultGCGrace, false
	}
	if least := sweepGrace(); requested < least {
		return least, true
	}
	return requested, false
}

// GCOptions say what a collection does.
type GCOptions struct {
	// OlderThan is how old a record that no pointer names must be to be deleted.
	// Zero means DefaultGCGrace; see GCGrace.
	OlderThan time.Duration
	// DryRun makes the collection say what it would delete and write nothing.
	DryRun bool
	// Groups also drops the reader groups, other than the current one, that
	// nothing refers to. It needs a sealed state, a key that is a reader, and the
	// state at rest, which the caller checks first (GC looks at the locks too).
	Groups bool
	// Plans counts the sealed objects of the stored plans in each group, which the
	// plan store owns. It is required with Groups. An error from it keeps every
	// group.
	Plans func(ctx context.Context) (sealedIn map[string]int, err error)
	// By is the name recorded in the history when groups are dropped.
	By string
	// Now is the clock; tests set it.
	Now func() time.Time
}

// GCResult says what a collection found and did, or with DryRun would do.
type GCResult struct {
	Grace  time.Duration
	Raised bool // OlderThan was below the least grace, and Grace is that
	DryRun bool

	// Listed is the number of records in the bucket and Named the number of them
	// that a pointer names. Unnamed counts the ones that no pointer names and that
	// match their name; Young of those are inside the grace, Unknown are kept
	// because their age cannot be told, Marked are old enough and were first found
	// unnamed by this collection, Settling were marked less than the settle
	// interval ago, and Deleted are deleted, Bytes in all. With DryRun, Marked and
	// Deleted are what the collection would mark and delete.
	Listed, Named           int
	Unnamed, Young, Unknown int
	Marked, Settling        int
	Deleted                 int
	Bytes                   int64
	// Settle is the interval that was applied.
	Settle time.Duration
	// Foreign lists the objects of rec/ that do not match their name. They are not
	// records pravo wrote, and are left alone.
	Foreign []string

	// Groups lists the groups other than the current one, oldest first, and what
	// refers to each. It is empty unless GCOptions.Groups was set and the groups
	// could be counted. GroupsRefused says why they could not.
	Groups        []GCGroup
	GroupsRefused string
}

// GCGroup is a group of a sealed state and what is sealed in it.
type GCGroup struct {
	ID string
	// Records counts the records that a pointer names, Spare the ones that none
	// names and that are kept (they may belong to a writer that has not yet
	// pointed at them), Outputs says whether outputs.json is sealed in the group
	// and Plans counts the sealed objects of stored plans. Summaries counts the
	// texts of errors that transactions keep sealed.
	Records, Spare int
	Outputs        bool
	Plans          int
	Summaries      int
	// Dropped is set for a group that was removed from meta.json, or with DryRun
	// would be.
	Dropped bool
}

// Referenced reports whether anything is sealed in the group.
func (g GCGroup) Referenced() bool {
	return g.Records+g.Spare+g.Plans+g.Summaries > 0 || g.Outputs
}

// recHead is what GC needs of a record: whether it is what its name says it is,
// the group it is sealed in and the newest time it records.
type recHead struct {
	missing  bool
	size     int
	verified bool // the bytes hash to the name
	parsed   bool
	group    string // empty for a record that is not sealed
	newest   time.Time
	data     []byte // kept for an unnamed record, for the delete to compare
}

// readHead reads the record at key.
func (s *Store) readHead(ctx context.Context, key string, keep bool) (*recHead, error) {
	data, err := s.b.Get(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return &recHead{missing: true}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", key, err)
	}
	h := &recHead{size: len(data), verified: recKey(data) == key}
	if !h.verified {
		return h, nil
	}
	var head struct {
		Sealed      *protect.Sealed `json:"sealed"`
		UpdatedAt   time.Time       `json:"updated_at"`
		RefreshedAt *time.Time      `json:"refreshed_at"`
		AppliedAt   *time.Time      `json:"applied_at"`
	}
	if json.Unmarshal(data, &head) == nil {
		h.parsed = true
		if head.Sealed != nil {
			h.group = head.Sealed.Group
		}
		h.newest = head.UpdatedAt
		for _, t := range []*time.Time{head.RefreshedAt, head.AppliedAt} {
			if t != nil && t.After(h.newest) {
				h.newest = *t
			}
		}
	}
	if keep {
		h.data = data
	}
	return h, nil
}

// each runs fn for 0 to n-1 on several goroutines.
func (s *Store) each(n int, fn func(i int)) {
	width := convertWidthLocal
	if !s.b.Local() {
		width = convertWidthRemote
	}
	var (
		next atomic.Int64
		wg   sync.WaitGroup
	)
	for w := 0; w < min(width, n); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				fn(i)
			}
		}()
	}
	wg.Wait()
}

// GC collects the garbage of a state of format 2 or 3.
//
// A record that no pointer names is garbage once it is older than the grace, by
// the newest of the times it records in the clear (updated_at, refreshed_at,
// applied_at): the backend does not tell when an object was written. A record
// whose age cannot be told is kept. The grace protects the record of a writer that
// has put it and not yet replaced the pointer, as long as the record carries the
// time it was written at. A writer that copies records with the times they already
// have (rewrap --all, protect init, state push and pull) is protected by the marks
// instead: a collection that finds an old record unnamed only marks it, in
// gc/marks.json, and a later one at least GCSettle after the mark deletes it if it
// is still unnamed. The marks of records that a pointer names, or that are gone,
// are dropped. Pointers are listed again right before the delete, and a record is
// deleted only if it still holds the bytes that were judged, so that one that was
// named or changed meanwhile is not. With DryRun nothing is written, the marks
// included.
//
// With Groups, a group other than the current one that no sealed record, no
// output and no stored plan is sealed in is dropped from meta.json. The records
// that the collection spared count: a writer may yet point at one. A group that
// anything is sealed in is kept, and the result says what. GC does not need a key
// to collect records. It needs one for the groups, and the state at rest.
func (s *Store) GC(ctx context.Context, o GCOptions) (GCResult, error) {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	res := GCResult{DryRun: o.DryRun, Settle: GCSettle}
	res.Grace, res.Raised = GCGrace(o.OlderThan)
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	m, err := readMeta(ctx, s.b)
	switch {
	case err != nil:
		return res, fmt.Errorf("reading meta.json of the state at %s: %w", s.b.Name(), err)
	case m.Lineage != s.Lineage():
		return res, fmt.Errorf("the lineage of the state at %s changed since it was opened", s.b.Name())
	case m.Format < FormatImmutable:
		return res, ErrNoRecords
	case m.Migration != nil && m.Migration.Unseal:
		return res, ErrUnsealing
	case m.Migration != nil:
		return res, ErrSealing
	}
	wantGroups := o.Groups && m.Sealed != nil
	if o.Groups {
		if s.Locked() {
			return res, fmt.Errorf("dropping groups needs a reader: %w", ErrNotReader)
		}
		if o.Plans == nil {
			return res, errors.New("dropping groups needs the count of the stored plans")
		}
		if err := s.checkAtRest(); err != nil {
			return res, err
		}
	}

	listed, err := s.b.List(ctx, "rec")
	if err != nil {
		return res, err
	}
	keys := slices.DeleteFunc(slices.Clone(listed), func(k string) bool { return !validRecKey(k) })
	named, err := s.namedRecords()
	if err != nil {
		return res, err
	}
	res.Listed = len(keys)
	var read []string
	for _, k := range keys {
		if named[k] {
			res.Named++
		}
		// The groups need the group of every record; the collection of records
		// needs only the ones that no pointer names.
		if !named[k] || wantGroups {
			read = append(read, k)
		}
	}

	heads := make([]*recHead, len(read))
	var (
		mu       sync.Mutex
		firstErr error
	)
	s.each(len(read), func(i int) {
		if ctx.Err() != nil {
			return
		}
		h, err := s.readHead(ctx, read[i], !named[read[i]])
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			firstErr = errors.Join(firstErr, err)
			return
		}
		heads[i] = h
	})
	if firstErr != nil {
		return res, firstErr
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	byKey := make(map[string]*recHead, len(read))
	for i, k := range read {
		byKey[k] = heads[i]
	}

	// The marks of the earlier collections. A record is garbage when it is older
	// than the grace by its own times and an earlier collection found it unnamed
	// at least the settle interval ago. The first look marks it.
	marks, err := s.readMarks(ctx)
	if err != nil {
		return res, err
	}
	var (
		garbage []string
		seen    = map[string]bool{}      // the records that are unnamed now
		added   = map[string]time.Time{} // the marks this collection makes
		at      = now()
	)
	for _, k := range read {
		h := byKey[k]
		if named[k] || h.missing {
			continue
		}
		if !h.verified {
			res.Foreign = append(res.Foreign, k)
			continue
		}
		seen[k] = true
		switch {
		case !h.parsed || h.newest.IsZero():
			res.Unnamed++
			res.Unknown++
		case at.Sub(h.newest) < res.Grace:
			res.Unnamed++
			res.Young++
		default:
			res.Unnamed++
			switch marked, ok := marks[k]; {
			case !ok:
				res.Marked++
				added[k] = at
			case at.Sub(marked) < GCSettle:
				res.Settling++
			default:
				garbage = append(garbage, k)
			}
		}
	}

	gone := map[string]bool{}
	if len(garbage) > 0 {
		// The pointers as they are now: a writer that named one of these since the
		// listing keeps it.
		fresh, err := s.namedRecords()
		if err != nil {
			return res, err
		}
		var (
			deleted atomic.Int64
			size    atomic.Int64
		)
		s.each(len(garbage), func(i int) {
			k := garbage[i]
			if ctx.Err() != nil || fresh[k] {
				return
			}
			h := byKey[k]
			if o.DryRun {
				deleted.Add(1)
				size.Add(int64(h.size))
				mu.Lock()
				gone[k] = true
				mu.Unlock()
				return
			}
			ok, err := s.b.DeleteIf(ctx, k, func(cur []byte) bool { return bytes.Equal(cur, h.data) })
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, ErrNotFound):
				gone[k] = true // someone else deleted it
			case err != nil:
				firstErr = errors.Join(firstErr, fmt.Errorf("deleting %s: %w", k, err))
			case ok:
				deleted.Add(1)
				size.Add(int64(h.size))
				gone[k] = true
			}
		})
		res.Deleted, res.Bytes = int(deleted.Load()), size.Load()
		named = fresh
	}
	// The marks are written after the delete: a collection that stops between the
	// two has deleted only records that an earlier one had marked.
	if !o.DryRun {
		for k := range named { // a pointer names it now
			delete(seen, k)
		}
		if err := s.writeMarks(ctx, seen, added, gone); err != nil {
			firstErr = errors.Join(firstErr, err)
		}
	}
	if firstErr != nil {
		return res, firstErr
	}
	if !wantGroups {
		return res, nil
	}
	return res, s.gcGroups(ctx, o, &res, m, named, byKey, gone)
}

// gcMarks is the contents of gc/marks.json.
type gcMarks struct {
	// Unnamed maps the key of a record to the time a collection first found that no
	// pointer names it.
	Unnamed map[string]time.Time `json:"unnamed"`
}

// parseMarks reads the marks. An object that cannot be read holds none: the next
// collection marks again and replaces it.
func parseMarks(data []byte) map[string]time.Time {
	var m gcMarks
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	for k := range m.Unnamed {
		if !validRecKey(k) {
			delete(m.Unnamed, k)
		}
	}
	return m.Unnamed
}

func encodeMarks(m map[string]time.Time) []byte {
	data, _ := json.MarshalIndent(gcMarks{Unnamed: m}, "", "  ")
	return append(data, '\n')
}

// readMarks returns the marks, none if the object is not there.
func (s *Store) readMarks(ctx context.Context) (map[string]time.Time, error) {
	data, err := s.b.Get(ctx, gcMarksKey)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", gcMarksKey, err)
	}
	return parseMarks(data), nil
}

// writeMarks replaces the marks with the ones of the records that are still
// unnamed (seen) and not deleted (gone), and adds the new ones. It merges with
// what is stored now, so that the marks of a collection that ran at the same time
// are not lost, and creates the object only when there is a mark to put in it.
func (s *Store) writeMarks(ctx context.Context, seen map[string]bool, added map[string]time.Time, gone map[string]bool) error {
	update := func(cur []byte) ([]byte, bool) {
		old := parseMarks(cur)
		next := make(map[string]time.Time, len(old)+len(added))
		for k, t := range old {
			if seen[k] && !gone[k] {
				next[k] = t
			}
		}
		for k, t := range added {
			if _, ok := next[k]; !ok && seen[k] && !gone[k] {
				next[k] = t
			}
		}
		if maps.EqualFunc(old, next, time.Time.Equal) {
			return nil, false
		}
		return encodeMarks(next), true
	}
	for range 10 {
		_, err := s.b.ReplaceIf(ctx, gcMarksKey, update)
		switch {
		case err == nil:
			return nil
		case !errors.Is(err, ErrNotFound):
			return fmt.Errorf("writing %s: %w", gcMarksKey, Advise(err))
		case len(added) == 0:
			return nil
		}
		// Not there yet: create it, or merge with the one that a collection that ran
		// at the same time has just created.
		created, err := s.b.PutIfAbsent(ctx, gcMarksKey, encodeMarks(added))
		if err != nil {
			return fmt.Errorf("writing %s: %w", gcMarksKey, Advise(err))
		}
		if created {
			return nil
		}
	}
	return fmt.Errorf("writing %s: it keeps changing", gcMarksKey)
}

// errNothingDropped is the error of an update of meta.json that finds no group to
// drop.
var errNothingDropped = errors.New("no group to drop")

// gcGroups counts what is sealed in each group other than the current one and
// drops the groups with nothing in them. named is the pointers as they are now,
// byKey the heads of all the records and gone the ones that were just deleted.
func (s *Store) gcGroups(ctx context.Context, o GCOptions, res *GCResult, m *meta, named map[string]bool, byKey map[string]*recHead, gone map[string]bool) error {
	current := m.Sealed.Current
	refs := map[string]*GCGroup{}
	var order []string
	for _, g := range m.Sealed.Groups {
		if g.ID == current {
			continue
		}
		refs[g.ID] = &GCGroup{ID: g.ID}
		order = append(order, g.ID)
	}
	refuse := func(why string) error {
		res.GroupsRefused = why
		return nil
	}
	// A command that opened the state before the readers last changed goes on
	// sealing in the group it opened with, until it looks at meta.json again. A group
	// is dropped only once no such command can be left: a record sealed in a group
	// that meta.json does not list stops the state opening for anyone.
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	for i := len(m.History) - 1; i >= 0; i-- {
		if h := m.History[i]; h.What != "gc" {
			if age := now().Sub(h.At); age < sweepGrace() {
				return refuse(fmt.Sprintf("the readers or the records were changed %s ago, and a command that opened the state before then may still seal in the old group; run it again after %s",
					age.Round(time.Second), sweepGrace().Round(time.Second)))
			}
			break
		}
	}
	// A record that a pointer names and that cannot be read says nothing of its
	// group: it may be sealed in the one that would be dropped.
	for k := range named {
		switch h := byKey[k]; {
		case h == nil || h.missing:
			return refuse("a pointer names a record that is not in the bucket (" + k + "), so the groups were not counted")
		case !h.verified || !h.parsed:
			return refuse("a record that a pointer names could not be read (" + k + "), so the groups were not counted")
		}
	}
	for k, h := range byKey {
		if gone[k] || h.missing || !h.verified || !h.parsed || h.group == "" {
			continue
		}
		if g := refs[h.group]; g != nil {
			if named[k] {
				g.Records++
			} else {
				g.Spare++
			}
		}
	}
	raw, err := s.b.Get(ctx, "outputs.json")
	switch {
	case errors.Is(err, ErrNotFound):
	case err != nil:
		return fmt.Errorf("reading outputs.json: %w", err)
	default:
		if env, ok := asSealedOutputs(raw); ok {
			if g := refs[env.Sealed.Group]; g != nil {
				g.Outputs = true
			}
		}
	}
	plans, err := o.Plans(ctx)
	if err != nil {
		return refuse("the stored plans could not all be read (" + err.Error() + "), so the groups were not counted")
	}
	for id, n := range plans {
		if g := refs[id]; g != nil {
			g.Plans = n
		}
	}
	// The text of the error of a transaction that failed is sealed in the group
	// that was current then, and is not written again by a record that is: only
	// rewrap --all seals it again. A group that held only these would be dropped
	// and the texts could not be opened by anyone.
	summaries, err := s.sealedSummaries(ctx)
	if err != nil {
		return refuse("the transactions could not all be read (" + err.Error() + "), so the groups were not counted")
	}
	for id, n := range summaries {
		if g := refs[id]; g != nil {
			g.Summaries = n
		}
	}

	unused := map[string]bool{}
	for _, id := range order {
		g := refs[id]
		g.Dropped = !g.Referenced()
		unused[id] = g.Dropped
		res.Groups = append(res.Groups, *g)
	}
	if o.DryRun || !slices.ContainsFunc(res.Groups, func(g GCGroup) bool { return g.Dropped }) {
		return nil
	}
	counted := map[string]bool{}
	for _, g := range m.Sealed.Groups {
		counted[g.ID] = true
	}
	s.mu.Lock()
	id := s.id
	s.mu.Unlock()
	by := o.By
	if by == "" {
		by = id.Name
	}
	err = s.updateMeta(func(cur *meta) error {
		if cur.Sealed == nil || cur.Migration != nil {
			return errors.New("the state is not sealed, or a seal is in progress")
		}
		if cur.Sealed.Current != current {
			return fmt.Errorf("meta.json changed while the groups were counted (the current group is now %s): %w", cur.Sealed.Current, ErrChanged)
		}
		k, err := cur.Sealed.Unlock(cur.Lineage, id.Reader)
		if err != nil {
			return fmt.Errorf("dropping groups: %w", err)
		}
		defer k.Close()
		sealed := *cur.Sealed
		sealed.Groups = cloneGroups(sealed.Groups)
		// A group that was not there when the records were counted is kept.
		sealed.Prune(k, func(g string) bool { return !counted[g] || !unused[g] })
		if len(sealed.Groups) == len(cur.Sealed.Groups) {
			return errNothingDropped
		}
		cur.Sealed = &sealed
		cur.History = appendHistory(cur.History, historyMeta{At: time.Now().UTC(), By: by, What: "gc"})
		return nil
	})
	switch {
	case errors.Is(err, errNothingDropped):
		for i := range res.Groups {
			res.Groups[i].Dropped = false
		}
		return nil
	case err != nil:
		return err
	}
	s.mu.Lock()
	sealed := s.meta.Sealed
	s.mu.Unlock()
	if keys, err := sealed.Unlock(s.Lineage(), id.Reader); err == nil {
		s.mu.Lock()
		s.ring.set(keys)
		s.openable, s.metaRead = openableGroups(sealed, id), time.Now()
		s.mu.Unlock()
	}
	return nil
}

// sealedSummaries counts the sealed texts of errors in the transactions, for each
// group. A transaction that is not valid JSON is a damaged log entry, which
// resealSummaries leaves alone as well, and counts for nothing.
func (s *Store) sealedSummaries(ctx context.Context) (map[string]int, error) {
	keys, err := s.b.List(ctx, "txn")
	if err != nil {
		return nil, err
	}
	var (
		mu       sync.Mutex
		counts   = map[string]int{}
		firstErr error
	)
	s.each(len(keys), func(i int) {
		k := keys[i]
		if !strings.HasSuffix(k, ".json") {
			return
		}
		data, err := s.b.Get(ctx, k)
		if errors.Is(err, ErrNotFound) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			firstErr = errors.Join(firstErr, err)
			return
		}
		var t struct {
			Sealed *struct {
				Group string `json:"group"`
			} `json:"summary_sealed"`
		}
		if json.Unmarshal(data, &t) == nil && t.Sealed != nil {
			counts[t.Sealed.Group]++
		}
	})
	return counts, firstErr
}
