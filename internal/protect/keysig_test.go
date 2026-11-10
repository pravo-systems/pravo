package protect

import (
	"errors"
	"testing"
)

func keysigIdentity(t *testing.T, name string) *Identity {
	t.Helper()
	id, err := NewIdentity(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(id.Close)
	return id
}

// The signature of the list covers the current group and the commitment of each group,
// and no lock.
func TestTheSignatureOfTheListOfKeys(t *testing.T) {
	const lineage = "lin-1"
	ada, fay := keysigIdentity(t, "ada"), keysigIdentity(t, "fay")
	readers := []Reader{ada.AsReader(), fay.AsReader()}
	m, k, err := NewMeta(lineage, readers)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := m.KeysStatus(readers, lineage); st != SigUnsigned {
		t.Fatalf("a new list = %s, want unsigned", st)
	}
	if err := m.SignKeys(ada, lineage); err != nil {
		t.Fatal(err)
	}
	status := func(m *Meta, readers []Reader, lineage string) string {
		st, name := m.KeysStatus(readers, lineage)
		return st.Key() + " " + name
	}
	if got := status(m, readers, lineage); got != "verified ada" {
		t.Fatalf("signed list = %s", got)
	}
	if got := status(m, readers[1:], lineage); got != "key_not_in_file " {
		t.Fatalf("to readers without the signer = %s", got)
	}
	if got := status(m, readers, "lin-2"); got != "invalid " {
		t.Fatalf("in another state = %s", got)
	}

	// A lock that is added or dropped leaves the signature.
	gil := keysigIdentity(t, "gil")
	if _, err := m.Rewrap(k, append(readers, gil.AsReader())); err != nil {
		t.Fatal(err)
	}
	if got := status(m, readers, lineage); got != "verified ada" {
		t.Fatalf("after a lock was added = %s", got)
	}

	for name, change := range map[string]func(m *Meta){
		"another current group": func(m *Meta) {
			m.Current = "g9"
			m.Groups = append(m.Groups, Group{ID: "g9", Alg: Alg, Commit: []byte{1}})
		},
		"another commitment": func(m *Meta) {
			m.Groups[0].Commit = append([]byte{}, m.Groups[0].Commit...)
			m.Groups[0].Commit[0] ^= 1
		},
		"a group more":  func(m *Meta) { m.Groups = append(m.Groups, Group{ID: "g2", Alg: Alg}) },
		"no commitment": func(m *Meta) { m.Groups[0].Commit = nil },
	} {
		c := *m
		c.Groups = append([]Group{}, m.Groups...)
		change(&c)
		if got := status(&c, readers, lineage); got != "invalid " {
			t.Errorf("%s: %s, want invalid", name, got)
		}
	}

	// A list whose current group commits to nothing is not signed, and one that is
	// signed all the same does not verify.
	bare := *m
	bare.Groups = []Group{{ID: m.Current, Alg: Alg, Locks: m.Groups[0].Locks}}
	if err := bare.SignKeys(ada, lineage); err == nil {
		t.Error("a list whose current group has no commitment was signed")
	}
	// An identity without a signing key signs nothing.
	if (&Identity{Name: "mute", Reader: fay.Reader}).CanSign() {
		t.Error("an identity without a signing key can sign")
	}
}

// A lock that holds another key than the group committed to does not open, and Rotate
// makes a group for the same holders with a key and a commitment of its own.
func TestCommitmentAndRotate(t *testing.T) {
	const lineage = "lin-1"
	ada, fay := keysigIdentity(t, "ada"), keysigIdentity(t, "fay")
	readers := []Reader{ada.AsReader(), fay.AsReader()}
	m, k, err := NewMeta(lineage, readers)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := NewGroup("g1", lineage, readers)
	if err != nil {
		t.Fatal(err)
	}
	swapped := *m
	swapped.Groups = []Group{{ID: "g1", Alg: Alg, Locks: other.Locks, Commit: m.Groups[0].Commit}}
	if _, err := swapped.Unlock(lineage, fay.Reader); !errors.Is(err, ErrDamaged) {
		t.Fatalf("a lock with another key = %v, want ErrDamaged", err)
	}
	// Without a commitment the same lock opens: this is what a list from before held.
	swapped.Groups[0].Commit = nil
	if _, err := swapped.Unlock(lineage, fay.Reader); err != nil {
		t.Fatalf("a group without a commitment = %v", err)
	}

	id, err := m.Rotate(k)
	if err != nil || id != "g2" || m.Current != "g2" || k.Current() != "g2" {
		t.Fatalf("Rotate = %q, %v (current %s)", id, err, m.Current)
	}
	g := m.Groups[1]
	if len(g.Locks) != 2 || len(g.Commit) == 0 || string(g.Commit) == string(m.Groups[0].Commit) {
		t.Fatalf("the new group = %+v", g)
	}
	for _, r := range []*Identity{ada, fay} {
		keys, err := m.Unlock(lineage, r.Reader)
		if err != nil || keys.Current() != "g2" {
			t.Fatalf("%s after Rotate: %v", r.Name, err)
		}
		sealed := keys.Seal(Ctx{Lineage: lineage, Kind: KindPlan, Name: "p"}, []byte("x"))
		if _, err := k.Open(Ctx{Lineage: lineage, Kind: KindPlan, Name: "p"}, sealed); err != nil || sealed.Group != "g2" {
			t.Fatalf("%s seals in %s, which the maker of the group cannot open: %v", r.Name, sealed.Group, err)
		}
	}
	if !k.SealIn("g1") || k.Current() != "g1" || k.SealIn("g7") {
		t.Fatal("SealIn does not pick a group the keys hold, or picks one they lack")
	}
	// Commit gives the groups that have none a commitment, and only those.
	had := m.Groups[1].Commit
	m.Groups[0].Commit = nil
	m.Commit(k)
	if len(m.Groups[0].Commit) == 0 || string(m.Groups[1].Commit) != string(had) {
		t.Fatal("Commit did not fill the missing commitment, or changed one")
	}
}
