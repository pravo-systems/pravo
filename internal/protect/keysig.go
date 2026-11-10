package protect

import (
	"crypto/ed25519"
	"errors"
)

// The list of the groups of a sealed state is signed by a reader.
//
// A lock is a public-key encryption, and anyone can make one for the public key of a
// reader. So someone who can write meta.json, and who is no reader, could add a group
// with a data key of their choosing and locks for every reader, make it the current
// group, and read what is sealed from then on. The locks alone cannot show it.
//
// Each group therefore publishes a commitment to its data key, and a reader signs the
// list: the current group, and the id and the commitment of each group. A lock that
// holds another key than the commitment names does not open (Group.Unlock). A list that
// no key of the readers file signed is one that nobody vouches for, and a reader who
// writes replaces its current group by a new one with a key of their own making before
// they seal anything (Rotate), and signs. Whoever changed the list gains nothing.
//
// The signature covers no lock: who has a lock is compared with the readers file, and a
// lock that was changed opens nothing or fails its commitment.

// keyParts are what the signature of the list covers.
func (m *Meta) keyParts() [][]byte {
	parts := [][]byte{[]byte(m.Alg), []byte(m.Current)}
	for _, g := range m.Groups {
		parts = append(parts, []byte(g.ID), []byte(g.Alg), g.Commit)
	}
	return parts
}

// KeyList is the list of groups and commitments as one comparable value: two metas with
// the same KeyList are covered by the same signature.
func (m *Meta) KeyList() string {
	if m == nil {
		return ""
	}
	return string(pae(m.keyParts()...))
}

// HasGroup reports whether the list holds the group.
func (m *Meta) HasGroup(id string) bool { return m != nil && m.group(id) != nil }

// CanSign reports whether the identity has a signing key.
func (id *Identity) CanSign() bool { return id != nil && len(id.SignPub) == ed25519.PublicKeySize }

// SignKeys signs the list of groups with the signing key of the identity. The current
// group must have a commitment: a list whose current group has none says nothing of
// the key that new writes are sealed with.
func (m *Meta) SignKeys(id *Identity, lineage string) error {
	if cur := m.group(m.Current); cur == nil || len(cur.Commit) == 0 {
		return errors.New("protect: the current group has no commitment to its key; make a new group first")
	}
	sig, err := id.SignMessage(DomainKeys, lineage, m.keyParts()...)
	if err != nil {
		return err
	}
	m.Signed = &sig
	return nil
}

// KeysStatus says what the readers make of the signature of the list of groups, and the
// name the readers file gives the key that signed. A list that is signed and whose
// current group has no commitment is SigInvalid: no pravo signs one.
func (m *Meta) KeysStatus(readers []Reader, lineage string) (SigStatus, string) {
	st, name := VerifySignature(m.Signed, readers, DomainKeys, lineage, m.keyParts()...)
	if st == SigVerified {
		if cur := m.group(m.Current); cur == nil || len(cur.Commit) == 0 {
			return SigInvalid, ""
		}
	}
	return st, name
}

// Commit gives a commitment to each group that has none and that the keys open: the
// groups that were made before groups carried one.
func (m *Meta) Commit(k *Keys) {
	for i := range m.Groups {
		g := &m.Groups[i]
		if dk, ok := k.keys[g.ID]; ok && len(g.Commit) == 0 {
			g.Commit = commitment(dk, k.lineage, g.ID)
		}
	}
}
