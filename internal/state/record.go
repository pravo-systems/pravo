package state

// Format 2 keeps every record of a resource as an object of its own that is
// never overwritten, rec/<sha256 of its bytes>, and makes resources/<address>.json
// a small pointer to the record that is current. The pointer is created with
// PutIfAbsent and replaced only with ReplaceIf, so that two commands that write
// the same address from the same view cannot both succeed. A reader checks every
// record it fetches against the name it was fetched by and against the pointer
// that named it. Format 1 keeps the whole record in resources/<address>.json.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// recPrefix is the directory of the immutable records.
const recPrefix = "rec/"

// pointerVersion is the version of the pointer object.
const pointerVersion = 1

// pointer is the object at resources/<address>.json in format 2 and 3. Nothing in
// it is secret, and a locked store reads it as it is.
type pointer struct {
	V          int    `json:"v"`
	Address    string `json:"address"`
	Revision   int    `json:"revision"`
	Generation string `json:"generation"`
	// Record is the key of the record: rec/ and the SHA-256 of the record's bytes
	// in hex.
	Record string `json:"record"`
}

// nodeRef is what a store knows of the object at a node key: the version of the
// record it read or wrote there, and the key of the immutable record that the
// pointer named. Record is empty for an object that is the whole record, which is
// every object of a format 1 store and the ones a conversion has not reached.
type nodeRef struct {
	Version
	Record string
	// Group is the group the record is sealed in; empty for a record that is not
	// sealed.
	Group string
}

// recKey returns the key of the immutable record with the given bytes.
func recKey(data []byte) string {
	sum := sha256.Sum256(data)
	return recPrefix + hex.EncodeToString(sum[:])
}

// validRecKey reports whether k has the form recKey gives.
func validRecKey(k string) bool {
	h, ok := strings.CutPrefix(k, recPrefix)
	if !ok || len(h) != 2*sha256.Size {
		return false
	}
	for _, c := range h {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// parsePointer reports whether data is a pointer and decodes it. An object that
// is not a pointer is a whole record.
func parsePointer(data []byte) (p pointer, isPointer bool, err error) {
	var probe struct {
		Record *string `json:"record"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return pointer{}, false, err
	}
	if probe.Record == nil {
		return pointer{}, false, nil
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return pointer{}, true, err
	}
	return p, true, nil
}

// versionOf reads the revision and generation, and the record a pointer names,
// from the bytes of the object at a node key, whether the object is a pointer or
// a whole record. rec is empty for a whole record.
func versionOf(cur []byte) (v Version, rec string, ok bool) {
	var p struct {
		Revision   int     `json:"revision"`
		Generation string  `json:"generation"`
		Record     *string `json:"record"`
	}
	if json.Unmarshal(cur, &p) != nil {
		return Version{}, "", false
	}
	if p.Record != nil {
		rec = *p.Record
	}
	return Version{Revision: p.Revision, Generation: p.Generation}, rec, true
}

// untrusted returns the error for an object that failed verification. It names
// the object and says what failed; it carries no value of the object.
func untrusted(key, what string) error {
	return fmt.Errorf("%s: %s: %w", key, what, ErrUntrusted)
}

// errRecordGone is readRecord's error for a record that is not in the backend,
// which a reader of a pointer that was replaced a moment ago meets.
var errRecordGone = errors.New("the record is not in the state")

// readAttempts is how often readNode reads a pointer again when the record it
// names is gone. A writer that replaced the pointer deletes the record it
// superseded, when only the time of the record changed, and a reader that was
// slow finds it missing. Between two attempts the reader waits a little longer
// each time, so that a node that is refreshed in a tight loop gets a chance.
const readAttempts = 5

// readPause is how long to wait after the attempt-th (counting from zero) attempt
// that found the record gone.
func readPause(attempt int) time.Duration { return time.Duration(attempt+1) * time.Millisecond }

// readNode reads the node whose object is at key. The object is a record or a
// pointer, whatever the format of the store says, so that a store that is
// half converted can be read.
func (s *Store) readNode(ctx context.Context, key string) (*Resource, nodeRef, error) {
	for attempt := 0; ; attempt++ {
		data, err := s.b.Get(ctx, key)
		if err != nil {
			return nil, nodeRef{}, fmt.Errorf("reading %s: %w", key, err)
		}
		p, isPtr, err := parsePointer(data)
		if err != nil {
			return nil, nodeRef{}, fmt.Errorf("reading state node %s: %w", key, err)
		}
		if !isPtr {
			if s.sealedState() && !s.mayHoldPlain() {
				return nil, nodeRef{}, untrusted(key, "is a whole record, and the state is sealed")
			}
			var r Resource
			if err := json.Unmarshal(data, &r); err != nil {
				return nil, nodeRef{}, fmt.Errorf("reading state node %s: %w", key, err)
			}
			return &r, nodeRef{Version: Version{Revision: r.Revision, Generation: r.Generation}}, nil
		}
		r, group, err := s.readRecord(ctx, key, p)
		if errors.Is(err, errRecordGone) && attempt+1 < readAttempts {
			time.Sleep(readPause(attempt))
			continue
		}
		if errors.Is(err, errRecordGone) {
			return nil, nodeRef{}, untrusted(key, "the record "+p.Record+" is not in the state")
		}
		if err != nil {
			return nil, nodeRef{}, err
		}
		return r, nodeRef{Version: Version{Revision: p.Revision, Generation: p.Generation}, Record: p.Record, Group: group}, nil
	}
}

// addrOfKey returns the address that a node key stands for, the inverse of
// encodeAddr. A key that is not well formed gives what could be decoded.
func addrOfKey(key string) string {
	name := strings.TrimSuffix(strings.TrimPrefix(key, "resources/"), ".json")
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if name[i] == '%' && i+4 < len(name) {
			if n, err := strconv.ParseUint(name[i+1:i+5], 16, 32); err == nil {
				b.WriteRune(rune(n))
				i += 4
				continue
			}
		}
		b.WriteByte(name[i])
	}
	return b.String()
}

// checkPointer verifies a pointer against the key it was read from.
func checkPointer(key string, p pointer) error {
	switch {
	case p.V != pointerVersion:
		return fmt.Errorf("reading state node %s: pointer version %d; this pravo reads version %d", key, p.V, pointerVersion)
	case key != nodeKey(p.Address):
		return untrusted(key, "the pointer names another address")
	case !validRecKey(p.Record):
		return untrusted(key, "the pointer names no record")
	}
	return nil
}

// readRecord fetches the record that p names and verifies it: its bytes against
// its name, and its address, revision and generation against the pointer. group is
// the group the record is sealed in, empty if it is not sealed.
func (s *Store) readRecord(ctx context.Context, key string, p pointer) (r *Resource, group string, err error) {
	if err := checkPointer(key, p); err != nil {
		return nil, "", err
	}
	data, err := s.b.Get(ctx, p.Record)
	if errors.Is(err, ErrNotFound) {
		return nil, "", errRecordGone
	}
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", p.Record, err)
	}
	if recKey(data) != p.Record {
		return nil, "", untrusted(p.Record, "does not match its name ("+p.Address+")")
	}
	r, group, err = s.decodeRecord(data)
	if err != nil {
		if errors.Is(err, ErrUntrusted) || errors.Is(err, ErrNotReader) {
			return nil, "", fmt.Errorf("%s (%s): %w", p.Record, p.Address, err)
		}
		return nil, "", fmt.Errorf("reading %s (%s): %w", p.Record, p.Address, err)
	}
	if r.Address() != p.Address || r.Revision != p.Revision || r.Generation != p.Generation {
		return nil, "", untrusted(key, "the pointer and the record "+p.Record+" disagree ("+p.Address+")")
	}
	return r, group, nil
}

// Advise adds to an error of ErrUnknownOutcome what the user is to do about it.
// Whether the write was made is not known, so the command is run again and reads
// the state as it is; the write is not reported as refused, which would say that
// another command changed the object. Any other error is returned as it is.
func Advise(err error) error {
	if errors.Is(err, ErrUnknownOutcome) {
		return fmt.Errorf("%w; run the command again", err)
	}
	return err
}

// replaceAttempts is how often a conditional write is tried again when a bucket
// answers that the object changed between the read and the write, while the
// object still holds what the caller judged.
const replaceAttempts = 5

// putNode writes the node r, at the revision and generation it carries, and
// returns what the store now holds of its object. In format 1 the record is the
// object. In format 2 it is a new immutable record and a pointer that replaces
// the one the store read, or creates it for a node the store did not hold; a
// pointer that is not the one the store holds, because another command wrote
// the node, is ErrChanged and nothing is changed.
func (s *Store) putNode(r *Resource) (nodeRef, error) {
	addr := r.Address()
	if s.Format() < FormatImmutable {
		return nodeRef{}, s.putJSON(nodeKey(addr), r)
	}
	s.recheckSeal()
	data, group, err := s.encodeRecord(r)
	if err != nil {
		return nodeRef{}, err
	}
	rkey := recKey(data)
	if _, err := s.b.PutIfAbsent(s.ctx, rkey, data); err != nil {
		return nodeRef{}, fmt.Errorf("writing %s: %w", rkey, Advise(err))
	}
	ptr, err := indentJSON(pointer{V: pointerVersion, Address: addr, Revision: r.Revision, Generation: r.Generation, Record: rkey})
	if err != nil {
		return nodeRef{}, err
	}
	s.mu.Lock()
	prev, had := s.held[addr]
	s.mu.Unlock()
	next := nodeRef{Version: Version{Revision: r.Revision, Generation: r.Generation}, Record: rkey, Group: group}
	if !had {
		created, err := s.b.PutIfAbsent(s.ctx, nodeKey(addr), ptr)
		if err != nil {
			return nodeRef{}, fmt.Errorf("writing %s: %w", nodeKey(addr), Advise(err))
		}
		if !created {
			return nodeRef{}, fmt.Errorf("%s: %w", addr, ErrChanged)
		}
		return next, nil
	}
	// The pointer is replaced if it still names the revision and generation that
	// the store holds. A refresh that rewrote the record at the same revision is
	// not a change in this sense: the version is what plans and applies are made
	// against, and what Verify compares. The record that is superseded is the one
	// the pointer names when it is replaced, which may not be the one the store
	// read.
	for attempt := 0; attempt < replaceAttempts; attempt++ {
		var (
			refused    bool
			superseded string
		)
		ok, err := s.b.ReplaceIf(s.ctx, nodeKey(addr), func(cur []byte) ([]byte, bool) {
			v, rec, parsed := versionOf(cur)
			if !parsed || v != prev.Version {
				refused = true
				return nil, false
			}
			superseded = rec
			return ptr, true
		})
		switch {
		case errors.Is(err, ErrNotFound):
			return nodeRef{}, fmt.Errorf("%s: %w", addr, ErrChanged)
		case err != nil:
			return nodeRef{}, fmt.Errorf("writing %s: %w", nodeKey(addr), Advise(err))
		case ok:
			// A write that only moved the time of the record leaves a record that
			// nothing refers to: no stored plan or transaction names a record, only
			// a revision and a generation.
			if superseded != "" && superseded != rkey && prev.Version == next.Version {
				_ = s.b.Delete(s.ctx, superseded)
			}
			return next, nil
		case refused:
			return nodeRef{}, fmt.Errorf("%s: %w", addr, ErrChanged)
		}
		// The bucket refused the write because the pointer was replaced after it was
		// read, while the closure had accepted it: read it again.
	}
	return nodeRef{}, fmt.Errorf("%s: %w", addr, ErrChanged)
}

// removeNode deletes the object of the node at addr, which the store holds as h.
// A node that is already gone counts as removed. In format 2 the pointer is
// deleted only if it is the one the store holds; the record stays.
func (s *Store) removeNode(addr string, h nodeRef) error {
	if s.Format() < FormatImmutable {
		return s.b.Delete(s.ctx, nodeKey(addr))
	}
	for attempt := 0; attempt < replaceAttempts; attempt++ {
		refused := false
		ok, err := s.b.DeleteIf(s.ctx, nodeKey(addr), func(cur []byte) bool {
			if v, _, parsed := versionOf(cur); !parsed || v != h.Version {
				refused = true
				return false
			}
			return true
		})
		switch {
		case errors.Is(err, ErrNotFound):
			return nil
		case err != nil:
			return err
		case ok:
			return nil
		case refused:
			return fmt.Errorf("%s: %w", addr, ErrChanged)
		}
	}
	return fmt.Errorf("%s: %w", addr, ErrChanged)
}
