package protect

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
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

// FormerKey is a signing key that held a role in the roles of a state and does not now: a
// member who has left, or a key that signed transactions before the roles were stored. Only the
// fingerprint is kept, since the signatures of a former key are not verified: the
// transactions of it that count are the ones vouched for (see Vouch).
type FormerKey struct {
	Name string
	// Key is the fingerprint of the signing key, as SSHFingerprint writes it.
	Key string
}

// Vouch is an admin's statement that one finished transaction of a key was legitimate: the
// key was a writer when it ran, or the roles were off. A key that is demoted or dropped keeps
// the records it wrote, and these lines are what lets them stay: only the transactions
// listed here are accepted from it. Digest is VouchDigest of the transaction, which covers
// everything its finish signature covers, so a transaction rewritten in place is not the
// one that was vouched for.
type Vouch struct {
	// Key is the fingerprint of the signing key of the transaction.
	Key    string
	Txn    string
	Digest string
}

// VouchKey is the first field of the line of a vouch in the text of a version of the roles. No
// name starts with it, so it cannot be mistaken for a key.
const VouchKey = "@vouch"

var (
	fingerprintRE = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)
	vouchTxnRE    = regexp.MustCompile(`^[0-9A-Za-z._-]{1,64}$`)
)

// NextRoles is the roles to store after prev (nil for the first): the readers of the file, a
// line without a role being a reader, and every key of prev that the file no longer lists as a
// former key, with the fingerprints in seen added as former keys when no one lists them. Names
// are made unique.
func NextRoles(file []Reader, prev []Reader, prevFormer []FormerKey, seen []string) ([]Reader, []FormerKey) {
	active := make([]Reader, 0, len(file))
	listed := map[string]bool{}
	names := map[string]bool{}
	for _, r := range file {
		if r.Role == RoleNone {
			r.Role = RoleReader
		}
		active = append(active, Reader{Name: r.Name, Sign: r.Sign, Role: r.Role})
		listed[SSHFingerprint(r.Sign)] = true
		names[strings.ToLower(r.Name)] = true
	}
	var former []FormerKey
	add := func(name, fp string) {
		if listed[fp] {
			return
		}
		listed[fp] = true
		base, n := name, 1
		for names[strings.ToLower(name)] {
			n++
			name = fmt.Sprintf("%s-%d", base, n)
		}
		names[strings.ToLower(name)] = true
		former = append(former, FormerKey{Name: name, Key: fp})
	}
	for _, r := range prev {
		add(r.Name, SSHFingerprint(r.Sign))
	}
	for _, f := range prevFormer {
		add(f.Name, f.Key)
	}
	for _, fp := range seen {
		add("former-"+strings.TrimPrefix(fp, "SHA256:")[:8], fp)
	}
	return active, former
}

// FormatRolesSet writes the roles that a version of the roles in the state holds: one line
// for each key, sorted by name. An active key is "NAME ssh-ed25519 BASE64 role:ROLE", with the
// signing key and not the reader key, since a role is about who may sign. A former key is
// "NAME SHA256:FINGERPRINT role:former". A reader without a role is written as a reader. The
// vouches come last, one "@vouch FINGERPRINT TXN DIGEST" line each.
func FormatRolesSet(rs []Reader, former []FormerKey, vouches []Vouch) string {
	type line struct{ name, text string }
	var lines []line
	for _, r := range rs {
		role := r.Role
		if role == RoleNone {
			role = RoleReader
		}
		lines = append(lines, line{r.Name, r.Name + " " + sshEd25519 + " " + SSHBlob(r.Sign) + " role:" + role.String()})
	}
	for _, f := range former {
		lines = append(lines, line{f.Name, f.Name + " " + f.Key + " role:former"})
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].name < lines[j].name })
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.text + "\n")
	}
	sorted := append([]Vouch(nil), vouches...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Key != sorted[j].Key {
			return sorted[i].Key < sorted[j].Key
		}
		return sorted[i].Txn < sorted[j].Txn
	})
	for _, v := range sorted {
		b.WriteString(VouchKey + " " + v.Key + " " + v.Txn + " " + v.Digest + "\n")
	}
	return b.String()
}

// ParseRolesSet is the reverse of FormatRolesSet. The readers it returns have a name, a
// signing key and a role, and no reader key.
func ParseRolesSet(text string) ([]Reader, []FormerKey, []Vouch, error) {
	var out []Reader
	var former []FormerKey
	var vouches []Vouch
	seenVouch := map[string]bool{}
	seen := map[string]bool{}
	keys := map[string]bool{}
	for n, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && f[0] == VouchKey {
			if len(f) != 4 || !fingerprintRE.MatchString(f[1]) || !vouchTxnRE.MatchString(f[2]) || !digestRE.MatchString(f[3]) || seenVouch[f[2]] {
				return nil, nil, nil, fmt.Errorf("roles line %d: want %s FINGERPRINT TXN DIGEST, once for each transaction", n+1, VouchKey)
			}
			seenVouch[f[2]] = true
			vouches = append(vouches, Vouch{Key: f[1], Txn: f[2], Digest: f[3]})
			continue
		}
		if len(f) < 3 || !nameRE.MatchString(f[0]) || seen[strings.ToLower(f[0])] {
			return nil, nil, nil, fmt.Errorf("roles line %d: the name is not valid or is used twice", n+1)
		}
		seen[strings.ToLower(f[0])] = true
		if len(f) == 3 {
			if !fingerprintRE.MatchString(f[1]) || f[2] != "role:former" || keys[f[1]] {
				return nil, nil, nil, fmt.Errorf("roles line %d: want NAME SHA256:FINGERPRINT role:former", n+1)
			}
			keys[f[1]] = true
			former = append(former, FormerKey{Name: f[0], Key: f[1]})
			continue
		}
		if len(f) != 4 || f[1] != sshEd25519 {
			return nil, nil, nil, fmt.Errorf("roles line %d: want NAME ssh-ed25519 BASE64 role:ROLE", n+1)
		}
		sign, err := ParseSSHBlob(f[2])
		if err != nil {
			return nil, nil, nil, fmt.Errorf("roles line %d: signing key: %w", n+1, err)
		}
		if fp := SSHFingerprint(sign); keys[fp] {
			return nil, nil, nil, fmt.Errorf("roles line %d: the key is listed twice", n+1)
		} else {
			keys[fp] = true
		}
		role, err := ParseRole(f[3])
		if err != nil {
			return nil, nil, nil, fmt.Errorf("roles line %d: %w", n+1, err)
		}
		out = append(out, Reader{Name: f[0], Sign: sign, Role: role})
	}
	if len(out) == 0 {
		return nil, nil, nil, errors.New("the roles are empty")
	}
	return out, former, vouches, nil
}
