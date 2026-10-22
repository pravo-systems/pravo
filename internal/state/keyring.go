package state

import (
	"sync"

	"github.com/pravo-systems/pravo/internal/protect"
)

// keyring holds the data keys of a store. A seal or an open takes the read lock
// for the length of the call; replacing the keys, or closing them, takes the
// write lock, wipes the old keys and so waits for the calls that are using them.
// A key is never wiped under a call that has it, and a call never sees a key
// half wiped.
//
// The lock is taken for crypto calls only. Nothing inside it takes another lock
// of the store, so a holder of the store's lock may replace the keys.
type keyring struct {
	mu   sync.RWMutex
	keys *protect.Keys
}

// has reports whether the ring holds keys.
func (r *keyring) has() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.keys != nil
}

// current is the group new values are sealed in, empty without keys.
func (r *keyring) current() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.keys == nil {
		return ""
	}
	return r.keys.Current()
}

// seal seals in the current group. ok is false without keys.
func (r *keyring) seal(c protect.Ctx, plain []byte) (sealed protect.Sealed, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.keys == nil {
		return protect.Sealed{}, false
	}
	return r.keys.Seal(c, plain), true
}

// open opens a sealed value. Without keys it is ErrNotReader.
func (r *keyring) open(c protect.Ctx, s protect.Sealed) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.keys == nil {
		return nil, ErrNotReader
	}
	return r.keys.Open(c, s)
}

// openJSON opens a sealed value and decodes it into v. Without keys it is
// ErrNotReader.
func (r *keyring) openJSON(c protect.Ctx, s protect.Sealed, v any) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.keys == nil {
		return ErrNotReader
	}
	return r.keys.OpenJSON(c, s, v)
}

// tag is the keyed tag of a value, empty without keys.
func (r *keyring) tag(value string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.keys == nil {
		return ""
	}
	return r.keys.Tag(value)
}

// set replaces the keys, nil for none, and wipes the ones it replaces.
func (r *keyring) set(keys *protect.Keys) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keys != nil && r.keys != keys {
		r.keys.Close()
	}
	r.keys = keys
}

// Close wipes the data keys of the store. A store that is sealed is locked from
// then on: it lists what it read, returns ErrNotReader for any value and refuses
// every write. A store that is not sealed holds no keys and is unchanged. The
// backend and the identity the store was opened with belong to the caller and
// stay as they are. Close may be called more than once.
func (s *Store) Close() {
	s.mu.Lock()
	if s.meta.Format >= FormatSealed {
		s.locked, s.openable = true, nil
	}
	s.mu.Unlock()
	s.ring.set(nil)
}
