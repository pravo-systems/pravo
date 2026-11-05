package pravo

import (
	"io"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// The sealing surface is small on purpose. A sealed state is opened with an Identity
// (StoreOptions.Identity, Config.Identity); the readers who may open it are Readers;
// what each of them may do is a Role. Store.Rewrap changes who the state is sealed for,
// and Store.RolesChain and Store.RolesFor read the signed chain of roles. The rest of
// the machinery (data keys, signatures, recovery shares) stays inside pravo.
type (
	// Reader is one reader of a sealed state: a name, the keys, and the role.
	Reader = protect.Reader
	// Role is what the key of a reader may do: read, write the state, or administer it.
	Role = protect.Role
	// RewrapOptions says who a state is sealed for, and what to seal again.
	RewrapOptions = state.RewrapOptions
	// RewrapResult counts what Store.Rewrap sealed again.
	RewrapResult = state.RewrapResult
	// RolesChain is the signed history of the roles of a state.
	RolesChain = state.RolesChain
	// RolesStatus says where the roles of a state come from and whether they are pinned.
	RolesStatus = state.RolesStatus
)

// The roles, from the least to the most a key may do.
const (
	// RoleNone is a reader line that gives no role. See RoleOf.
	RoleNone = protect.RoleNone
	// RoleReader may read the state.
	RoleReader = protect.RoleReader
	// RoleWriter may also change it.
	RoleWriter = protect.RoleWriter
	// RoleAdmin may also change who the readers are and what they may do.
	RoleAdmin = protect.RoleAdmin
)

// ParseReaders reads a readers file: one reader per line, as "pravo protect" writes it.
func ParseReaders(r io.Reader) ([]Reader, error) { return protect.ParseReaders(r) }

// RolesOn reports whether any of the readers has a role. Without one every key is an
// admin, as before roles existed.
func RolesOn(readers []Reader) bool { return protect.RolesOn(readers) }

// RoleOf is the role of the key with the fingerprint fp among the readers, and whether
// the key is a reader at all.
func RoleOf(readers []Reader, fp string) (Role, bool) { return protect.RoleOf(readers, fp) }
