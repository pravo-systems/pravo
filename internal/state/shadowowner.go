package state

import "github.com/pravo-systems/pravo/internal/protect"

// The kinds of transaction that say whose the objects of a decompiled root are (pravo
// shadow). The owner is not kept in a file beside the specification, where deleting it hands
// the objects over: it is the newest of these transactions, which a sealed state signs and
// whose rollback it notices like that of any other.
const (
	// KindShadow is the transaction that wrote the records of a shadow. The objects are
	// Terraform's after it.
	KindShadow = "shadow"
	// KindHandover is the transaction that handed them to pravo.
	KindHandover = "handover"
)

// Owners of the objects of a root.
const (
	OwnerTerraform = "terraform"
	OwnerPravo     = "pravo"
)

// ShadowOwner says who owns the objects of the root: OwnerTerraform after a shadow, until a
// hand-over, OwnerPravo after one, and "" for a state that no shadow wrote. In a sealed
// state a hand-over counts only when a reader signed it, so that a transaction that was put
// there does not take the objects over; a shadow counts either way, since doubt makes the
// state refuse, not allow.
func (s *Store) ShadowOwner(readers []protect.Reader) (string, *Transaction, error) {
	t, err := s.LatestOfKinds(readers, KindShadow, KindHandover)
	if err != nil || t == nil {
		return "", nil, err
	}
	if t.Kind == KindShadow {
		return OwnerTerraform, t, nil
	}
	if t.Status != "applied" {
		return OwnerTerraform, t, nil
	}
	if s.sealedState() && VerifyTransaction(s.Lineage(), t, readers).Overall() != protect.SigVerified {
		return OwnerTerraform, t, nil
	}
	return OwnerPravo, t, nil
}
