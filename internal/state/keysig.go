package state

import (
	"context"
	"fmt"

	"github.com/pravo-systems/pravo/internal/protect"
)

// The list of the key groups of a sealed state is signed by a reader (see
// protect/keysig.go for why). The store keeps the signature true:
//
//   - a change of meta.json that leaves the list as it was leaves the signature;
//   - a change that makes a new current group is signed by the identity that made it,
//     which knows the key because it made it;
//   - any other change of the list (a group that gc drops) is signed when the list it
//     found was signed by a reader the store was told to trust (TrustReaders), and is
//     left without a signature otherwise: the store never signs a list that nobody
//     vouched for;
//   - an identity that cannot sign, such as the key of the recovery, leaves the list
//     without a signature.
//
// A command that is running looks at meta.json again from time to time, and moves to a
// new current group only when a reader it trusts signed the list that names it
// (trustsKeys). Otherwise it goes on sealing in the group it opened with.
//
// A list without a signature of a reader is not an error: a pravo that predates the
// signature wrote one, and so does a recovery. The first reader that writes to the
// state replaces the current group by one whose key it made, and signs (AdoptKeys),
// so that nothing is sealed with a key that nobody vouches for.

// keyList is what updateMeta remembers of the key groups before a change.
type keyList struct {
	list     string
	ids      map[string]bool
	verified bool
}

func (s *Store) keyListOf(m *meta) keyList {
	if m.Sealed == nil {
		return keyList{}
	}
	was := keyList{list: m.Sealed.KeyList(), ids: map[string]bool{}}
	for _, g := range m.Sealed.Groups {
		was.ids[g.ID] = true
	}
	s.mu.Lock()
	trusted := s.trusted
	s.mu.Unlock()
	if trusted != nil {
		st, _ := m.Sealed.KeysStatus(trusted, m.Lineage)
		was.verified = st == protect.SigVerified
	}
	return was
}

// signKeys signs the list of key groups of m after a change, or takes the signature
// off, by the rules above.
func (s *Store) signKeys(m *meta, was keyList, signer *protect.Identity) error {
	if m.Sealed == nil || m.Sealed.KeyList() == was.list {
		return nil
	}
	made := !was.ids[m.Sealed.Current]
	if (!made && !was.verified) || !signer.CanSign() {
		m.Sealed.Signed = nil
		return nil
	}
	sealed := *m.Sealed
	if err := sealed.SignKeys(signer, m.Lineage); err != nil {
		return fmt.Errorf("signing the list of keys: %w", err)
	}
	m.Sealed = &sealed
	return nil
}

// trustsKeys reports whether a store that is running may move to the current group of a
// meta.json it has just read: a reader it trusts signed the list. A store that was told
// of no readers moves, as a store did before lists were signed.
func (s *Store) trustsKeys(m *meta) bool {
	s.mu.Lock()
	trusted := s.trusted
	s.mu.Unlock()
	if trusted == nil {
		return true
	}
	st, _ := m.Sealed.KeysStatus(trusted, m.Lineage)
	return st == protect.SigVerified
}

// TrustReaders tells the store the readers of the repository: the keys whose signature
// of the list of key groups it takes. A command sets them from the readers file.
func (s *Store) TrustReaders(readers []protect.Reader) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trusted = append([]protect.Reader{}, readers...)
}

// KeysStatus says what the readers make of the signature of the list of key groups, and
// the name that signed: the name the readers file gives the key when the signature
// verifies, and the name the signature claims otherwise. A state that is not sealed is
// unsigned.
func (s *Store) KeysStatus(readers []protect.Reader) (protect.SigStatus, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.meta.Sealed == nil {
		return protect.SigUnsigned, ""
	}
	st, name := s.meta.Sealed.KeysStatus(readers, s.meta.Lineage)
	if st != protect.SigVerified && s.meta.Sealed.Signed != nil {
		name = s.meta.Sealed.Signed.Signer
	}
	return st, name
}

// CanSignKeys reports whether the identity of the store can sign the list of key groups.
func (s *Store) CanSignKeys() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id.CanSign()
}

// AdoptKeys is what a reader who writes does with a list of key groups that no reader
// in readers signed: it replaces the current group by a new one, with a key that this
// process makes, and signs the list. It returns the id of the new group, empty when the
// list is signed by a reader, by another command in the meantime, or when the identity
// cannot sign. The readers are those of the state: no lock is added or removed.
func (s *Store) AdoptKeys(ctx context.Context, readers []protect.Reader) (string, error) {
	res, err := s.Rewrap(ctx, RewrapOptions{Readers: readers, keysOnly: true})
	if err != nil {
		return "", err
	}
	if res.KeysWere == "" {
		return "", nil
	}
	return res.Change.NewGroup, nil
}
