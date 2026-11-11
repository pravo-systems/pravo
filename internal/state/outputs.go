package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ReadOutputs reads the recorded outputs of the state kept in b. It reads the
// one object outputs.json and nothing else, and creates nothing.
func ReadOutputs(ctx context.Context, b Backend) (map[string]json.RawMessage, error) {
	data, err := b.Get(ctx, "outputs.json")
	if errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("%s has no recorded outputs; apply that root first", b.Name())
	}
	if err != nil {
		return nil, err
	}
	if _, sealed := asSealedOutputs(data); sealed {
		return nil, fmt.Errorf("the outputs of %s are sealed to the readers of that state, and remote_outputs reads them without a key: %w", b.Name(), ErrNotReader)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("reading outputs of %s: %w", b.Name(), err)
	}
	return out, nil
}

// Outputs returns the recorded outputs.
func (s *Store) Outputs() map[string]json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]json.RawMessage, len(s.outputs))
	for k, v := range s.outputs {
		out[k] = v
	}
	return out
}

// ReloadOutputs reads the recorded outputs again from the backend, so that
// outputs written by another command since this store was opened are seen.
func (s *Store) ReloadOutputs() (map[string]json.RawMessage, error) {
	out, _, err := s.LoadOutputs()
	return out, err
}

// LoadOutputs is ReloadOutputs that also reports whether the backend holds the
// object outputs.json at all. A state whose outputs were never written has no
// such object, and a root that reads these outputs refuses it.
func (s *Store) LoadOutputs() (outputs map[string]json.RawMessage, present bool, err error) {
	outputs, _, present, err = s.loadOutputsRaw()
	return outputs, present, err
}

// loadOutputsRaw is LoadOutputs that also returns the bytes of outputs.json as the
// backend holds them.
func (s *Store) loadOutputsRaw() (outputs map[string]json.RawMessage, raw []byte, present bool, err error) {
	raw, err = s.b.Get(s.cx(), "outputs.json")
	out := map[string]json.RawMessage{}
	switch {
	case err == nil:
		present = true
		if out, err = s.decodeOutputs(raw); err != nil {
			return nil, nil, false, err
		}
		if out == nil {
			out = map[string]json.RawMessage{}
		}
	case !errors.Is(err, ErrNotFound):
		return nil, nil, false, err
	}
	s.mu.Lock()
	s.outputs = out
	s.mu.Unlock()
	return s.Outputs(), raw, present, nil
}

// SetOutputs replaces the recorded outputs.
func (s *Store) SetOutputs(outputs map[string]json.RawMessage) error {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	return s.setOutputs(outputs)
}

// setOutputs is SetOutputs for a caller that holds outMu.
func (s *Store) setOutputs(outputs map[string]json.RawMessage) error {
	if err := s.writable(); err != nil {
		return err
	}
	if outputs == nil {
		outputs = map[string]json.RawMessage{}
	}
	data, err := s.encodeOutputs(outputs)
	if err != nil {
		return err
	}
	if err := s.b.Put(s.cx(), "outputs.json", data); err != nil {
		return err
	}
	s.mu.Lock()
	s.outputs = outputs
	s.mu.Unlock()
	return nil
}

// The lock on the outputs is held for the time of one read and one write. A
// holder that has not released it after outputsLockStale is gone. An update that
// finds it held waits for outputsLockWait at most, which is longer than
// outputsLockStale, so that an update which meets the lock of a command that was
// killed reclaims it and does not fail after the resources are applied.
var (
	outputsLockStale = 60 * time.Second
	outputsLockWait  = outputsLockStale + 10*time.Second
)

// outputsTries is how many times an update of the outputs merges again after it
// found that outputs.json is not what it read.
const outputsTries = 8

// UpdateOutputs reads the recorded outputs from the backend while it holds the
// lock on them, passes them to fn and records what fn returns. present says
// whether the backend holds outputs.json at all. fn returns the outputs to
// record and whether to write them. Two commands that finish together each
// merge their outputs into what the other wrote.
//
// The lock is held for a minute at most and is not renewed, so it does not by
// itself keep a write that arrives late from replacing what another command
// recorded meanwhile. The write is therefore made only if outputs.json is still
// what was read; when it is not, the outputs are read and fn is called again.
func (s *Store) UpdateOutputs(fn func(recorded map[string]json.RawMessage, present bool) (updated map[string]json.RawMessage, write bool, err error)) error {
	if err := s.writable(); err != nil {
		return err
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	unlock, err := s.lockOutputs()
	if err != nil {
		return err
	}
	defer unlock()
	for try := 1; ; try++ {
		recorded, raw, present, err := s.loadOutputsRaw()
		if err != nil {
			return err
		}
		updated, write, err := fn(recorded, present)
		if err != nil || !write {
			return err
		}
		done, err := s.replaceOutputs(updated, raw, present)
		if err != nil || done {
			return err
		}
		if try == outputsTries {
			return fmt.Errorf("outputs.json was changed by another command each of the %d times this one tried to record its outputs", try)
		}
	}
}

// replaceOutputs records outputs if outputs.json is still read, the bytes that
// the caller merged them into, or still absent when present is false. It reports
// whether it wrote.
func (s *Store) replaceOutputs(outputs map[string]json.RawMessage, read []byte, present bool) (bool, error) {
	if outputs == nil {
		outputs = map[string]json.RawMessage{}
	}
	data, err := s.encodeOutputs(outputs)
	if err != nil {
		return false, err
	}
	var wrote bool
	if present {
		wrote, err = s.b.ReplaceIf(s.cx(), "outputs.json", func(current []byte) ([]byte, bool) {
			return data, bytes.Equal(current, read)
		})
	} else {
		wrote, err = s.b.PutIfAbsent(s.cx(), "outputs.json", data)
	}
	switch {
	case errors.Is(err, ErrNotFound):
		return false, nil // deleted since it was read
	case errors.Is(err, ErrUnknownOutcome):
		// The write was made if the object holds its bytes. When it does not, the next
		// round reads what is there and merges again.
		current, gerr := s.b.Get(s.cx(), "outputs.json")
		wrote = gerr == nil && bytes.Equal(current, data)
	case err != nil:
		return false, err
	}
	if wrote {
		s.mu.Lock()
		s.outputs = outputs
		s.mu.Unlock()
	}
	return wrote, nil
}

// lockOutputs takes the lock on outputs.json, waiting for a holder that is
// about to release it, and returns the function that releases it.
func (s *Store) lockOutputs() (func(), error) {
	l := s.newLock("outputs.json", "update outputs", "")
	data, _ := indentJSON(l)
	deadline := time.Now().Add(outputsLockWait)
	delay := 2 * time.Millisecond
	unreadable := false
	misses := 0
	for {
		created, err := s.b.PutIfAbsent(s.cx(), outputsLockKey, data)
		unknown := errors.Is(err, ErrUnknownOutcome)
		if err != nil && !unknown {
			return nil, err
		}
		if created {
			return s.unlockOutputs, nil
		}
		raw, holder, rerr := s.readOutputsLock()
		if rerr == nil && bytes.Equal(raw, data) {
			// A create of unknown outcome that was made, found now or by a later round
			// whose own create was refused: the bytes are this call's.
			return s.unlockOutputs, nil
		}
		if errors.Is(rerr, ErrNotFound) {
			// Released between the two calls: ask again. A store that refuses the
			// create and has no object under the key is not waited for for ever.
			if misses++; misses > 3 {
				if time.Now().After(deadline) {
					return nil, fmt.Errorf("outputs.json stayed locked for %s: the store refuses to create the lock and has none to read", outputsLockWait)
				}
				time.Sleep(delay + lockJitter(delay))
				if delay < 100*time.Millisecond {
					delay *= 2
				}
			}
			continue
		}
		misses = 0
		// An object that cannot be read may be half written; it is the holder's no
		// longer when it is still unreadable the next time.
		gone := rerr == nil && s.outputsLockStale(holder) || rerr != nil && unreadable
		unreadable = rerr != nil
		if gone {
			// Deleted only if it is still the object that was judged.
			_, _ = s.b.DeleteIf(s.cx(), outputsLockKey, func(current []byte) bool { return bytes.Equal(current, raw) })
			unreadable = false
		}
		if time.Now().After(deadline) {
			if rerr != nil {
				return nil, fmt.Errorf("outputs.json stayed locked for %s: %w", outputsLockWait, rerr)
			}
			return nil, fmt.Errorf("outputs.json stayed locked for %s: %w", outputsLockWait, &ErrLocked{Lock: holder})
		}
		time.Sleep(delay + lockJitter(delay))
		if delay < 100*time.Millisecond {
			delay *= 2
		}
	}
}

// unlockOutputs releases the lock on outputs.json if this process holds it.
func (s *Store) unlockOutputs() {
	_, _ = s.b.DeleteIf(s.cx(), outputsLockKey, func(current []byte) bool {
		var l Lock
		return json.Unmarshal(current, &l) == nil && l.Nonce == s.nonce
	})
}

// readOutputsLock reads the lock on outputs.json with the bytes it was read from.
func (s *Store) readOutputsLock() ([]byte, Lock, error) {
	var l Lock
	data, at, err := getTimed(s.cx(), s.b, outputsLockKey)
	if err != nil {
		return nil, l, err
	}
	err = json.Unmarshal(data, &l)
	if l.Addr == "" {
		l.Addr = "outputs.json"
	}
	l.at = at
	return data, l, err
}

// outputsLockStale reports whether the holder of the lock on the outputs is
// gone: its process is gone on this host, or it has held the lock too long. The lock
// is written once and never renewed, so for a store that tells its own time that is
// the time since the store wrote it.
func (s *Store) outputsLockStale(l Lock) bool {
	if s.holderProcessGone(l) {
		return true
	}
	if l.at.Known() {
		return l.at.Age() > outputsLockStale
	}
	return lockClock().Sub(l.Since) > outputsLockStale
}
