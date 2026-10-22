package state

// This file is the contract between internal/state and everything that calls
// it for the protection of the state: the id and protect commands, the viewer
// and the plan store. It holds the types and the errors, and the small methods
// that go with them; the work is in record.go (immutable records), upgrade.go
// (the upgrade to format 2 and VerifyAll), seal.go (the sealed records and
// outputs, Protection, Sealer, OpenOutputs) and seal_upgrade.go (the upgrade to
// format 3 and Rewrap).
//
// The design is in the scratchpad notes the work started from:
// recon/spec.md (the product spec), recon/state.md and recon/crypto.md.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// Formats of the store. Format 1 is the layout that every released pravo reads
// and writes, and stays the default for a new store. Format 2 keeps each record
// as an immutable object, rec/<sha256>, and resources/<address>.json as a small
// pointer to it. Format 3 is format 2 with the records, the outputs, the stored
// plans and the values of command lines sealed to a list of readers.
const (
	FormatImmutable = 2
	FormatSealed    = 3
	MaxFormat       = FormatSealed
)

var (
	// ErrNotReader is returned for a value of a sealed state that this process
	// holds no key for, and for every write to such a state. It maps to exit
	// code 5.
	ErrNotReader = errors.New("you are not a reader of this state")
	// ErrUntrusted means the store failed verification: an object whose bytes do
	// not match its name, a pointer that disagrees with its record, a sealed
	// value that does not open where it is. It maps to exit code 7.
	ErrUntrusted = errors.New("the state failed verification")
	// ErrSealing is returned for a write while a seal is in progress.
	ErrSealing = errors.New("a seal is in progress; run pravo protect init --encrypt to finish")
	// ErrNotImplemented is for an operation that has no implementation yet. The
	// state package no longer returns it.
	ErrNotImplemented = errors.New("not implemented yet")
)

// Options are the ways of opening a store beyond the defaults.
type Options struct {
	// Identity is the key to open a sealed state with. Nil means no key: a
	// sealed state opens locked (see Store.Locked).
	Identity *protect.Identity
	// NewFormat is the format of a store that is created by this open. Zero
	// means 1. It has no effect on a store that exists.
	NewFormat int
}

// OpenBackendWith opens (or creates) the store kept in b with the options.
// OpenBackend is OpenBackendWith with no options.
func OpenBackendWith(ctx context.Context, b Backend, o Options) (*Store, error) {
	return openBackend(ctx, b, o)
}

// OpenWith is Open with options, for a local directory.
func OpenWith(path string, o Options) (*Store, error) {
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		return nil, fmt.Errorf("the state path %s is a file; a state path is a directory (set $settings.state.path to a directory such as pravo.state, and read an OpenTofu state file with \"pravo state import-tfstate\")", path)
	}
	b, err := NewLocalBackend(path)
	if err != nil {
		return nil, err
	}
	return openBackend(context.Background(), b, o)
}

// Locked reports whether the state is sealed and this store holds no key to
// open it with. A locked store lists addresses, types, names, dependencies and
// revisions, and returns ErrNotReader for any value, and for any write.
func (s *Store) Locked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locked
}

// ErrUpgraded is returned for a write by a store that was opened before another
// command moved the state to another format or began to seal it: what the store
// holds of the format is out of date, and a record it wrote would not be in the
// layout the state now has, or would be in plain text in a sealed one.
var ErrUpgraded = errors.New("the state was upgraded since this command opened it; run the command again")

// metaRecheck is how long a store goes on writing before it looks at meta.json
// again for an upgrade. An upgrade needs the state at rest, which a command that
// has opened the store and not yet taken a lock does not stop.
var metaRecheck = time.Second

// writable returns the error for a write that the store refuses: ErrNotReader
// for a locked store, ErrSealing while a seal is in progress, ErrUpgraded for a
// store that was opened before the state was upgraded.
func (s *Store) writable() error {
	s.mu.Lock()
	switch {
	case s.locked:
		s.mu.Unlock()
		return ErrNotReader
	case s.meta.Migration != nil:
		s.mu.Unlock()
		return ErrSealing
	}
	s.mu.Unlock()
	return s.CheckCurrent()
}

// CheckCurrent returns ErrUpgraded if the state was moved to another format, or
// sealed, since this store was opened. It reads meta.json on the first call and
// then at most once in metaRecheck. A writer that does not write through the store,
// as the plan store does not, calls it before it writes.
func (s *Store) CheckCurrent() error {
	s.mu.Lock()
	due := time.Since(s.metaChecked) >= metaRecheck
	if due {
		s.metaChecked = time.Now()
	}
	held := s.meta
	s.mu.Unlock()
	if !due {
		return nil
	}
	data, err := s.b.Get(s.ctx, "meta.json")
	if err != nil {
		return nil // a bucket that cannot be read refuses the write itself
	}
	var m meta
	if json.Unmarshal(data, &m) != nil || m.Lineage != held.Lineage {
		return nil
	}
	if m.Format != held.Format || (m.Sealed != nil) != (held.Sealed != nil) || (m.Migration != nil) != (held.Migration != nil) {
		return ErrUpgraded
	}
	return nil
}

// Protection describes how the state is protected. It holds no secret and no
// key; it is all that `pravo protect status`, `pravo protect check` and the
// viewer's Access view need.
type Protection struct {
	// State is "off" (format 1), "immutable" (format 2), "sealed" (format 3) or
	// "migrating" (format 3 with a seal in progress).
	State  string
	Format int
	// Location is the name of the backend, as Backend.Name reports it.
	Location string
	// Sealed is the sealed part of meta.json: the groups, their locks and the
	// current group. Nil unless State is "sealed" or "migrating". The names and
	// reader keys of the current readers are the locks of the current group.
	Sealed *protect.Meta
	// Records is the number of resources. Plain counts records that are not
	// sealed in a sealed state (written before the seal and not yet reached);
	// Unreadable counts records sealed in a group that this process holds no key
	// for; ByGroup counts the sealed records of each group.
	Records, Plain, Unreadable int
	ByGroup                    map[string]int
	// Migration is set while State is "migrating".
	Migration *Migration
	// History is the most recent changes of the seal, oldest first, at most 50.
	History []HistoryEntry
	// You says who this process is, for a sealed state.
	You You
}

// Migration describes a seal that has started and has not finished.
type Migration struct {
	Started     time.Time
	By          string
	Done, Total int
}

// HistoryEntry is one change of the seal: "sealed", "rewrap", "rewrap --all" or
// "gc", which dropped groups that nothing was sealed in. By is the name of the
// identity, taken from the machine that made the change. It is a label and not a
// proof; signatures arrive with roles.
type HistoryEntry struct {
	At             time.Time
	By             string
	What           string
	Added, Removed []string
}

// You is the identity of this process in relation to a sealed state.
type You struct {
	// State is "reader", "not-reader" or "no-identity". It is empty for a state
	// that is not sealed.
	State string
	Name  string
	// Key is the reader key, as it appears in the readers file ("x25519:...").
	Key string
}

// UpgradeOptions say how to move a store to a newer format.
type UpgradeOptions struct {
	// To is 2 (immutable records) or 3 (sealed).
	To int
	// Readers is the list to seal to, from the readers file. Required for 3.
	Readers []protect.Reader
	// Identity must be one of the Readers when To is 3.
	Identity *protect.Identity
	// By is the name recorded in the history.
	By string
	// Progress is called as records are converted. It may be called from several
	// goroutines.
	Progress func(done, total int)
	// Plans, if set, is called when the records and the outputs are converted
	// and before the state is marked sealed. It seals the stored plans, which
	// the plan store owns, and returns how many it sealed and how many it left
	// because an apply holds a claim on them. A crash before the mark leaves the
	// store migrating, and the next run calls it again.
	Plans func(ctx context.Context) (sealed, skipped int, err error)
}

// UpgradeResult says what an upgrade did.
type UpgradeResult struct {
	From, To int
	Records  int
	// PlansSealed and PlansSkipped count the stored plans sealed in place and
	// the ones left alone because an apply holds a claim on them.
	PlansSealed, PlansSkipped int
	// Resumed is set when an earlier upgrade did not finish and this one did.
	Resumed bool
}

// RewrapOptions say how to bring the locks of a sealed state in line with the
// readers file.
type RewrapOptions struct {
	Readers []protect.Reader
	// All seals every record, the outputs and the stored plans again under the
	// current group, instead of leaving them to be sealed when next written.
	All      bool
	By       string
	Progress func(done, total int)
	// Plans, if set and All is set, seals the stored plans again under the
	// current group, as Upgrade's does, and returns how many it sealed and how
	// many it left because an apply holds a claim on them.
	Plans func(ctx context.Context) (sealed, skipped int, err error)
}

// RewrapResult says what a rewrap did. Change is empty when the store already
// matched the readers.
type RewrapResult struct {
	Change   protect.Change
	Resealed int
	// PlansSealed and PlansSkipped count the stored plans sealed again and the
	// ones left as they were because an apply holds a claim on them. They are
	// zero unless All is set and Plans is.
	PlansSealed, PlansSkipped int
}

// VerifyResult is what VerifyAll found.
type VerifyResult struct {
	Checked int
	// Bad names the objects that failed, as "rec/<sha256> (address)".
	Bad []string
}

// Sealer seals and opens the stored objects that are not records: outputs.json,
// the stored plans and their metadata, and the values of command lines. Kind is
// one of the protect.Kind constants and name says which object: the id of a
// plan or a transaction, empty for the outputs.
type Sealer interface {
	// Seal seals plain in the current group.
	Seal(kind, name string, plain []byte) (protect.Sealed, error)
	// Open opens a sealed value. It returns ErrNotReader without a key.
	Open(kind, name string, s protect.Sealed) ([]byte, error)
}
