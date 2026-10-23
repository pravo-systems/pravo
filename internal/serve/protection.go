package serve

import (
	"context"
	"time"

	"github.com/pravo-systems/pravo/internal/state"
)

// This file is the seam between the viewer and the protection of the state. The
// viewer shows how a state is protected, who can read it and what the readers
// file says; it learns that through one function, given in Options, that the
// command fills from state.Store.Protection and from the readers file. Nothing
// here holds a key: a reader is a name and public keys, and the identity of the
// process is a name and whether it can open the state.

// Protection returns the facts of one revision for the Access view. It is called
// when a model is built, with the store that was opened and the path of the
// readers file. It reads and writes nothing else, and an error in it never fails
// the model: it is told on the page. A nil Protection describes a state that is
// protected as its format says (see Options.Protection).
type Protection func(ctx context.Context, st *state.Store, readersFile string) Facts

// Facts are what Protection returns.
type Facts struct {
	Store StoreFacts
	File  FileFacts
	// You is the identity the process holds, nil for none.
	You *YouFacts
	// Problem is a sentence for the page when the facts could not be made in full.
	Problem string
}

// StoreFacts are what the store says about its own protection, from meta.json
// and the public parts of the records.
type StoreFacts struct {
	// State is "off", "immutable", "sealed" or "migrating".
	State  string
	Format int
	// Locked is set when the store was opened without a key that opens its values:
	// no record has attributes then, whatever the store returns.
	Locked bool
	// Readers are the readers of the current group, the ones whose locks the store
	// holds: Name and Key are all that is known of them there.
	Readers []ReaderFacts
	// Groups are the groups of data keys, oldest first.
	Groups []GroupFacts
	// Records counts the resources. Sealed are the records sealed under any group,
	// Plain those of a sealed state that are not sealed yet, Unreadable those
	// sealed under a group this process holds no key for.
	Records, Sealed, Plain, Unreadable int
	Migration                          *MigrationFacts
	// History is the changes of the seal, oldest first.
	History []ChangeFacts
	// Recovery is the recovery recipient of the state, nil when it has none. Its
	// lock is not among Readers and Groups count it in no Locks.
	Recovery *RecoveryFacts
}

// RecoveryFacts is the recovery recipient of a sealed state: a key that is split
// into N shares, any K of which rebuild it.
type RecoveryFacts struct {
	K, N int
	// Key is the public key, "x25519:" and 43 characters of unpadded base64url.
	Key string
	// Locked is set when every group has a lock for the key. Groups lists those
	// that have none.
	Locked bool
	Groups []string
	// File is the verdict of the recovery file of the repository: "match",
	// "differs", "missing" or "invalid".
	File string
	// Unverified lists the groups whose recovery lock does not hold its check, when the
	// viewer can tell.
	Unverified []string
}

// ReaderFacts is a reader: a line of the readers file, or a lock of a group.
type ReaderFacts struct {
	Name string
	// Key is the reader key, "x25519:" and 43 characters of unpadded base64url.
	Key string
	// SSH is the base64 blob of the signing key of the readers line, "" where the
	// reader is known from a lock alone. Line is its line in the file, 0 for none.
	SSH  string
	Line int
}

// GroupFacts is a group of data keys.
type GroupFacts struct {
	ID      string
	Current bool
	Locks   int
	Records int
}

// MigrationFacts says how far a seal that started has come.
type MigrationFacts struct {
	Started     time.Time
	By          string
	Done, Total int
}

// ChangeFacts is one change of the seal, as the history of meta.json records it.
// By is a name that the machine of the change gave itself: a label, not a proof.
type ChangeFacts struct {
	At             time.Time
	By             string
	What           string
	Added, Removed []string
}

// FileFacts are what the readers file holds.
type FileFacts struct {
	// Path is the file that was looked at, absolute.
	Path string
	// State is "ok", "missing" or "invalid". Errors say why a file is invalid: a
	// line number and a reason, never the text of a line.
	State   string
	Readers []ReaderFacts
	Errors  []LineError
}

// LineError is a line of the readers file that does not load.
type LineError struct {
	Line   int
	Reason string
}

// YouFacts is the identity of the process in relation to a sealed state.
type YouFacts struct {
	// State is "reader", "not-reader", "no-identity" or "locked" (the identity is
	// sealed with a passphrase and none was available).
	State string
	Name  string
	// Key is the public reader key of the identity.
	Key string
}
