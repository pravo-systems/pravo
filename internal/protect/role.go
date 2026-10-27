package protect

import (
	"errors"
	"strings"
)

// Role is what a key may do. The strength is linear: an admin may do what a writer
// may, and a writer what a reader may.
//
//	reader  opens the state, plans without writing it, and stores a plan
//	writer  changes the state: apply, destroy, import, state edits, policies
//	admin   removes a policy, changes who reads, and recovers
//
// The role of a key is the fifth field of its line in the readers file, which
// the repository holds and a writer of the bucket cannot change. A readers file in
// which no line has a role gives none: every key is an admin, as before roles
// existed. As soon as one line has a role, a line without one is a reader.
type Role int

const (
	// RoleNone is a line that gives no role. See RoleOf for what it means.
	RoleNone Role = iota
	RoleReader
	RoleWriter
	RoleAdmin
)

func (r Role) String() string {
	switch r {
	case RoleReader:
		return "reader"
	case RoleWriter:
		return "writer"
	case RoleAdmin:
		return "admin"
	}
	return ""
}

// ParseRole parses the field "role:NAME" of a readers line.
func ParseRole(field string) (Role, error) {
	name, ok := strings.CutPrefix(field, "role:")
	if !ok {
		return RoleNone, errors.New("fifth field: want role:reader, role:writer or role:admin")
	}
	switch name {
	case "reader":
		return RoleReader, nil
	case "writer":
		return RoleWriter, nil
	case "admin":
		return RoleAdmin, nil
	}
	return RoleNone, errors.New("role: want reader, writer or admin")
}

// Allows reports whether a key of this role may do what need asks: a role that is
// at least as strong as need.
func (r Role) Allows(need Role) bool { return r >= need }

// RolesOn reports whether the readers give roles: at least one line has one.
func RolesOn(rs []Reader) bool {
	for _, r := range rs {
		if r.Role != RoleNone {
			return true
		}
	}
	return false
}

// RoleOf is the role of the key with the signing fingerprint fp in the readers.
// ok is false when no reader has that key. With roles off every key is an admin; with
// roles on, a line without a role is a reader.
func RoleOf(rs []Reader, fp string) (role Role, ok bool) {
	on := RolesOn(rs)
	for _, r := range rs {
		if SSHFingerprint(r.Sign) != fp {
			continue
		}
		switch {
		case !on:
			return RoleAdmin, true
		case r.Role == RoleNone:
			return RoleReader, true
		}
		return r.Role, true
	}
	return RoleNone, false
}
