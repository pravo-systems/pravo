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
	// Roles are the roles of the keys, nil for a state that is not sealed.
	Roles *RolesFacts
	// Problem is a sentence for the page when the facts could not be made in full.
	Problem string
}

// RolesFacts are the roles of the keys of a sealed state: what the readers file gives, what the
// state holds, and whether the records are the work of writers.
type RolesFacts struct {
	// Source is "off" (no line of the readers file gives a role), "file" (the readers file
	// decides: the roles are not pinned in the state) or "state" (a chain of versions in the
	// state decides, as the readers file pins it).
	Source string
	// Version is the newest version of the chain, 0 without one.
	Version int
	// Unpinned is set when the state holds a chain that the readers file does not pin.
	Unpinned bool
	// Differs is set when the readers file asks for other roles than the newest version.
	Differs bool
	// Error says why the chain does not verify, or is not the one the readers file pins.
	Error string
	// You is the role of the identity of the process: "admin", "writer", "reader" or "".
	You string
	// Keys are the signing keys the roles name: the readers' by fingerprint, the roles
	// as they decide, and the keys that left (Role "former").
	Keys []RoleKeyFacts
	// Versions are the versions of the chain, oldest first.
	Versions []RolesVersionFacts
	// Records is how the records stand against the transactions of writers, nil when the
	// roles are off.
	Records *RecordCheckFacts
}

// RoleKeyFacts is one signing key with its role: "admin", "writer", "reader" or "former".
type RoleKeyFacts struct {
	Name        string
	Fingerprint string
	Role        string
	// Vouched counts the transactions an admin vouched for, of a key that left or was demoted.
	Vouched int
}

// RolesVersionFacts is one version of the roles in the state.
type RolesVersionFacts struct {
	Seq int
	At  time.Time
	// By is the name of the admin who signed it.
	By string
	// Changes are the lines of what changed from the version before, as protect roles prints them.
	Changes []string
	// Vouched counts the transactions the version vouches for.
	Vouched int
}

// RecordCheckFacts is the verdict on the records: each is the work of a writer, or is not.
type RecordCheckFacts struct {
	// Skipped is set, with the reason, when the check was not made, for a state too large to
	// be walked each time the page is read.
	Skipped                            string
	Bound, Vouched, Earlier, Unchecked int
	Problems                           []RecordProblemFacts
}

// RecordProblemFacts is a record that no transaction of a writer accounts for.
type RecordProblemFacts struct {
	Addr, Why string
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
	// Unseal is set when the seal is being taken off and not put on.
	Unseal bool
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
