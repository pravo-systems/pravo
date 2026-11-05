package pravo

import "github.com/pravo-systems/pravo/internal/state"

// A state is kept in a Backend: a flat store of named objects with the atomic
// operations that the locks of a state rest on (PutIfAbsent, ReplaceIf, DeleteIf).
// Config.Backend and StoreOptions.Backend give a workspace a Backend of the
// program's own, in place of the directory, bucket or server that the
// specification names. Package backendtest checks an implementation against the
// contract.
//
// Everything that makes a state trustworthy is done above the Backend, by the
// store that wraps it: a sealed state is encrypted before a Backend sees it, the
// records and transactions of a state with roles are signed, and the checks for a
// record that was replaced, removed or rolled back run on what the Backend
// returns. A Backend that alters or withholds objects is detected, and can at worst
// deny service; it cannot make a store accept a state that its readers' keys did
// not write. What a Backend must get right is the atomicity of its conditional
// operations: two commands that race for a lock rely on exactly one of them
// winning.

// ErrNotFound is what Backend.Get returns for a key that holds no object, and
// DeleteIf and ReplaceIf for a missing one. A Backend must return it, or an error
// that wraps it, and no other error for that case.
var ErrNotFound = state.ErrNotFound

// ErrUnknownOutcome is what PutIfAbsent and ReplaceIf return for a write that the
// client sent more than once, that the store refused, and whose outcome it could
// not check. See Backend.
var ErrUnknownOutcome = state.ErrUnknownOutcome

// NewMemoryBackend returns an empty Backend that keeps its objects in memory. It
// serves tests, and is the reference implementation of the contract for one that
// keeps them elsewhere. name is how messages call it.
func NewMemoryBackend(name string) Backend { return state.NewMemBackend(name) }
