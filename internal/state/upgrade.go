package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// convertWidth is how many records an upgrade converts at the same time. They are
// variables so that a test can make the order of the calls to the backend
// deterministic.
var (
	convertWidthLocal  = 16
	convertWidthRemote = 32
)

// Upgrade moves the store to format 2 or 3. It is safe to run again after a
// crash: it finishes what an earlier run began. The store must be at rest: no
// lock held and no transaction running.
//
// The order is what keeps a released pravo from misreading a half converted
// store. meta.json is replaced first, with the new format, so that from then on
// a pravo that does not know the format refuses the store; only then is a
// record turned into an immutable record and a pointer, each by a
// compare-and-swap on the bytes that were read, so that a record someone wrote
// in the meantime is never overwritten. A store of format 2 or 3 reads a record
// that is not yet a pointer as it reads a pointer, so a store that was left
// half converted lists every address.
func (s *Store) Upgrade(ctx context.Context, o UpgradeOptions) (UpgradeResult, error) {
	switch o.To {
	case FormatImmutable:
		return s.upgradeImmutable(ctx, o)
	case FormatSealed:
		return s.upgradeSealed(ctx, o)
	}
	return UpgradeResult{}, fmt.Errorf("a state is upgraded to format %d or %d, not %d", FormatImmutable, FormatSealed, o.To)
}

func (s *Store) upgradeImmutable(ctx context.Context, o UpgradeOptions) (UpgradeResult, error) {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	res := UpgradeResult{From: s.Format(), To: FormatImmutable}
	if res.From > FormatImmutable {
		return res, fmt.Errorf("the state at %s is format %d; a state is not moved back to format %d", s.b.Name(), res.From, FormatImmutable)
	}
	if err := s.checkAtRest(); err != nil {
		return res, err
	}
	if res.From < FormatImmutable {
		if err := s.setFormat(FormatImmutable); err != nil {
			return res, err
		}
	}
	n, err := s.convertNodes(ctx, rewrite{
		needs:  func(_ string, isPtr bool) bool { return !isPtr },
		encode: func(_ *Resource, raw []byte) ([]byte, string, error) { return raw, "", nil },
	}, o.Progress)
	res.Records = n
	res.Resumed = res.From == FormatImmutable && n > 0
	return res, err
}

// checkAtRest reports an error if a command holds a lock on the state. A
// transaction that is recorded as running holds a lock for as long as its command
// lives, so a running transaction that has none is one a killed command left.
func (s *Store) checkAtRest() error {
	if err := s.checkLocks(); err != nil {
		return err
	}
	if _, l, err := s.readOutputsLock(); err == nil && !s.outputsLockStale(l) {
		return &ErrLocked{Lock: l}
	}
	return nil
}

// checkLocks is checkAtRest for the locks on the records alone. The lock on the
// outputs that a command killed in the middle of an upgrade leaves goes stale by
// itself, and an upgrade that finishes the work waits for it where it takes it.
func (s *Store) checkLocks() error {
	locks, err := s.Locks()
	if err != nil {
		return err
	}
	for _, l := range locks {
		if !l.Stale {
			return &ErrLocked{Lock: l}
		}
	}
	return nil
}

// updateMeta replaces meta.json with what fn makes of it, only if it is still
// the object that fn was shown, and keeps the result as the meta of the store.
// fn returns an error to refuse. A bucket that says the object changed after it
// was read makes fn run again on what is there now.
func (s *Store) updateMeta(fn func(m *meta) error) error {
	for attempt := 0; attempt < replaceAttempts; attempt++ {
		var (
			next    meta
			refused error
		)
		ok, err := s.b.ReplaceIf(s.ctx, "meta.json", func(cur []byte) ([]byte, bool) {
			var m meta
			if err := json.Unmarshal(cur, &m); err != nil {
				refused = fmt.Errorf("reading %s/meta.json: %w", s.b.Name(), err)
				return nil, false
			}
			if m.Lineage != s.Lineage() {
				refused = fmt.Errorf("the lineage of the state at %s changed since it was opened", s.b.Name())
				return nil, false
			}
			if m.Format > MaxFormat {
				refused = fmt.Errorf("state at %s was written by a newer pravo (format %d)", s.b.Name(), m.Format)
				return nil, false
			}
			if err := fn(&m); err != nil {
				refused = err
				return nil, false
			}
			out, err := indentJSON(m)
			if err != nil {
				refused = err
				return nil, false
			}
			next = m
			return out, true
		})
		switch {
		case err != nil:
			return err
		case ok:
			s.mu.Lock()
			s.meta = next
			s.mu.Unlock()
			return nil
		case refused != nil:
			return refused
		}
	}
	return fmt.Errorf("meta.json of the state at %s changed again and again while it was written; run the command again", s.b.Name())
}

// setFormat raises the format in meta.json.
func (s *Store) setFormat(format int) error {
	return s.updateMeta(func(m *meta) error {
		if m.Format < format {
			m.Format = format
		}
		return nil
	})
}

// errConvertChanged is the error of a record that was written by another command
// while it was converted.
var errConvertChanged = errors.New("changed while it was converted")

// rewrite says which objects under resources/ an upgrade rewrites, and how.
type rewrite struct {
	// needs reports whether the object must be rewritten. isPtr says whether it
	// is a pointer, and group which group its record is sealed in, empty if the
	// record is not sealed.
	needs func(group string, isPtr bool) bool
	// encode makes the bytes of the new immutable record from the record and the
	// bytes of the object it was read from, and says which group it is sealed in.
	encode func(r *Resource, raw []byte) (data []byte, group string, err error)
	// dropOld deletes the record that the pointer named, once the pointer is
	// replaced. A rewrite that seals sets it: the record it replaces holds the
	// values in plain text, or sealed in a group that is no longer wanted.
	dropOld bool
}

// convertNodes rewrites every object under resources/ that rw selects into an
// immutable record and a pointer, and returns how many it rewrote. A record that
// someone wrote since it was read is not rewritten and is named in the error; the
// others are, and the store stays readable and can be upgraded again.
func (s *Store) convertNodes(ctx context.Context, rw rewrite, progress func(done, total int)) (int, error) {
	keys, err := s.b.List(s.ctx, "resources")
	if err != nil {
		return 0, err
	}
	keys = jsonKeysOf(keys)
	var (
		converted, done atomic.Int64
		mu              sync.Mutex
		changed         []string
		firstErr        error
		next            atomic.Int64
		wg              sync.WaitGroup
	)
	width := convertWidthLocal
	if !s.b.Local() {
		width = convertWidthRemote
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
				// A record that is begun is finished, and no other is begun once the
				// context is done: what is left is converted when the upgrade is run
				// again.
				if err := ctx.Err(); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
				did, err := s.convertNode(ctx, keys[i], rw)
				switch {
				case errors.Is(err, errConvertChanged):
					mu.Lock()
					changed = append(changed, addrOfKey(keys[i]))
					mu.Unlock()
				case err != nil:
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
				case did:
					converted.Add(1)
				}
				if progress != nil {
					progress(int(done.Add(1)), len(keys))
				}
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return int(converted.Load()), firstErr
	}
	if len(changed) > 0 {
		sort.Strings(changed)
		return int(converted.Load()), fmt.Errorf("%d records were written by another command while they were converted, and are left as they are (%s); run the upgrade again", len(changed), strings.Join(changed, ", "))
	}
	return int(converted.Load()), nil
}

func jsonKeysOf(keys []string) []string {
	var out []string
	for _, k := range keys {
		if strings.HasSuffix(k, ".json") {
			out = append(out, k)
		}
	}
	return out
}

// convertNode rewrites the object at key if rw selects it, and reports whether it
// did.
func (s *Store) convertNode(ctx context.Context, key string, rw rewrite) (bool, error) {
	for attempt := 0; attempt < replaceAttempts; attempt++ {
		raw, err := s.b.Get(s.ctx, key)
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("reading %s: %w", key, err)
		}
		p, isPtr, err := parsePointer(raw)
		if err != nil {
			return false, fmt.Errorf("reading state node %s: %w", key, err)
		}
		var (
			r     *Resource
			group string
		)
		if isPtr {
			if r, group, err = s.readRecord(ctx, key, p); errors.Is(err, errRecordGone) {
				continue
			} else if err != nil {
				return false, err
			}
		} else {
			r = new(Resource)
			if err := json.Unmarshal(raw, r); err != nil {
				return false, fmt.Errorf("reading state node %s: %w", key, err)
			}
		}
		if !rw.needs(group, isPtr) {
			return false, nil
		}
		addr := r.Address()
		if key != nodeKey(addr) {
			return false, fmt.Errorf("%s holds the record of %s", key, addr)
		}
		data, newGroup, err := rw.encode(r, raw)
		if err != nil {
			return false, fmt.Errorf("%s: %w", key, err)
		}
		rkey := recKey(data)
		if _, err := s.b.PutIfAbsent(s.ctx, rkey, data); err != nil {
			return false, fmt.Errorf("writing %s: %w", rkey, err)
		}
		ptr, err := indentJSON(pointer{V: pointerVersion, Address: addr, Revision: r.Revision, Generation: r.Generation, Record: rkey})
		if err != nil {
			return false, err
		}
		refused := false
		ok, err := s.b.ReplaceIf(s.ctx, key, func(cur []byte) ([]byte, bool) {
			if !bytes.Equal(cur, raw) {
				refused = true
				return nil, false
			}
			return ptr, true
		})
		switch {
		case errors.Is(err, ErrNotFound):
			return false, errConvertChanged
		case err != nil:
			return false, fmt.Errorf("writing %s: %w", key, err)
		case ok:
			if rw.dropOld && isPtr && p.Record != rkey {
				_ = s.b.Delete(s.ctx, p.Record)
			}
			s.mu.Lock()
			if _, in := s.nodes[addr]; in {
				s.held[addr] = nodeRef{Version: Version{Revision: r.Revision, Generation: r.Generation}, Record: rkey, Group: newGroup}
			}
			s.mu.Unlock()
			return true, nil
		case refused:
			return false, errConvertChanged
		}
	}
	return false, errConvertChanged
}

// VerifyAll hashes every immutable record against its name, and every pointer
// against its record. It needs no key. A non-empty Bad is ErrUntrusted for the
// caller. On a format 1 store it checks nothing and returns Checked 0.
func (s *Store) VerifyAll(ctx context.Context) (VerifyResult, error) {
	var res VerifyResult
	if s.Format() < FormatImmutable {
		return res, nil
	}
	keys, err := s.b.List(s.ctx, "resources")
	if err != nil {
		return res, err
	}
	keys = jsonKeysOf(keys)
	var (
		mu       sync.Mutex
		firstErr error
		next     atomic.Int64
		wg       sync.WaitGroup
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
				checked, bad, err := s.verifyNode(ctx, keys[i])
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = err
				}
				if checked {
					res.Checked++
				}
				if bad != "" {
					res.Bad = append(res.Bad, bad)
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	sort.Strings(res.Bad)
	return res, firstErr
}

// verifyNode checks the object at key. checked is true for a pointer; bad names
// the record that failed. An object that is a whole record is not checked.
func (s *Store) verifyNode(ctx context.Context, key string) (checked bool, bad string, err error) {
	for attempt := 0; attempt < readAttempts; attempt++ {
		data, err := s.b.Get(s.ctx, key)
		if errors.Is(err, ErrNotFound) {
			return false, "", nil
		}
		if err != nil {
			return false, "", fmt.Errorf("reading %s: %w", key, err)
		}
		p, isPtr, err := parsePointer(data)
		if err != nil || !isPtr {
			if err != nil {
				return false, key, nil
			}
			return false, "", nil
		}
		label := p.Record + " (" + addrOfKey(key) + ")"
		if checkPointer(key, p) != nil {
			return true, label, nil
		}
		rec, err := s.b.Get(s.ctx, p.Record)
		if errors.Is(err, ErrNotFound) {
			if attempt+1 < readAttempts {
				time.Sleep(readPause(attempt))
				continue
			}
			return true, label, nil
		}
		if err != nil {
			return false, "", fmt.Errorf("reading %s: %w", p.Record, err)
		}
		if recKey(rec) != p.Record {
			return true, label, nil
		}
		var head Resource
		if json.Unmarshal(rec, &head) != nil || head.Address() != p.Address || head.Revision != p.Revision || head.Generation != p.Generation {
			return true, label, nil
		}
		return true, "", nil
	}
	return true, "", nil
}
