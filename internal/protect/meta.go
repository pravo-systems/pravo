package protect

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
)

// Meta is what meta.json holds for a sealed state, under the key "sealed". It
// holds no secret: locks are public-key encryptions. It is also all that the
// viewer and `protect status` need to say who the readers are, with no key.
type Meta struct {
	Alg     string  `json:"alg"`     // Alg
	Current string  `json:"current"` // group new writes are sealed in
	Groups  []Group `json:"groups"`  // oldest first
	Readers string  `json:"readers"` // SetID of the reader list the locks were made from
}

// ErrDamaged means the sealed part of meta.json cannot be what a pravo wrote: it
// names no group, or a current group that it does not list. It is a failure of
// verification, not a reader who has no lock.
var ErrDamaged = errors.New("the sealed part of meta.json is damaged")

// Check reports whether m is well formed: at least one group, and the current
// group is one of them. It needs no key.
func (m *Meta) Check() error {
	switch {
	case len(m.Groups) == 0:
		return fmt.Errorf("%w: it lists no group", ErrDamaged)
	case m.group(m.Current) == nil:
		return fmt.Errorf("%w: the current group %q is not in the list", ErrDamaged, m.Current)
	}
	return nil
}

// Keys holds the unlocked data keys of one open store, for one process. Unlock
// once at open: every Open after that costs about 1.5 µs per 2 KB.
type Keys struct {
	lineage string
	current string
	keys    map[string]DataKey
}

// NewMeta seals a state to readers: group g1, one lock per reader.
func NewMeta(lineage string, readers []Reader) (*Meta, *Keys, error) {
	if len(readers) == 0 {
		return nil, nil, errors.New("protect: at least one reader is needed")
	}
	g, dk, err := NewGroup("g1", lineage, readers)
	if err != nil {
		return nil, nil, err
	}
	m := &Meta{Alg: Alg, Current: g.ID, Groups: []Group{g}, Readers: SetID(readers)}
	return m, &Keys{lineage: lineage, current: g.ID, keys: map[string]DataKey{g.ID: dk}}, nil
}

// Unlock opens every group the reader has a lock in. It is an error to be in no
// group (ErrNotReader) or not in the current one: such a reader could read old
// records but not write.
func (m *Meta) Unlock(lineage string, me *ReaderKey) (*Keys, error) {
	if m.Alg != Alg {
		return nil, fmt.Errorf("protect: state is sealed with %q; this pravo knows %q", m.Alg, Alg)
	}
	if err := m.Check(); err != nil {
		return nil, err
	}
	k := &Keys{lineage: lineage, current: m.Current, keys: map[string]DataKey{}}
	for _, g := range m.Groups {
		dk, err := g.Unlock(lineage, me)
		switch {
		case err == nil:
			k.keys[g.ID] = dk
		case errors.Is(err, ErrNotReader):
		default:
			return nil, err
		}
	}
	if len(k.keys) == 0 {
		return nil, ErrNotReader
	}
	if _, ok := k.keys[m.Current]; !ok {
		return nil, fmt.Errorf("%w (current group %s)", ErrNotReader, m.Current)
	}
	return k, nil
}

// Change says what Rewrap did.
type Change struct {
	Added, Removed []string // reader names
	NewGroup       string   // set when a removal made a new data key
}

// Rewrap makes the locks match the readers file.
//   - A reader who is new gets a lock in every group (so they can read old records).
//   - A reader who is gone loses the locks in every group (they lose live access
//     at once; copies of the bucket they already have still open), and the
//     current group is replaced by a new one with a new data key, so that records
//     written from now on are sealed to nobody who left.
//   - Locks of readers who stay are kept byte for byte.
//
// The caller must hold keys for all groups (Unlock does, for a reader of all of
// them). Records stay in their old group until they are written again;
// `rewrap --all` seals them again, after which Prune drops the old groups.
func (m *Meta) Rewrap(k *Keys, readers []Reader) (Change, error) {
	var ch Change
	if len(readers) == 0 {
		return ch, errors.New("protect: refusing to remove every reader")
	}
	cur := m.group(m.Current)
	if cur == nil {
		return ch, errors.New("protect: current group is missing")
	}
	miss, extra := cur.Diff(readers)
	ch.Added, ch.Removed = miss, extra
	for _, g := range m.Groups {
		if _, ok := k.keys[g.ID]; !ok {
			return ch, fmt.Errorf("protect: you cannot open group %s, so you cannot rewrap it", g.ID)
		}
	}
	for i := range m.Groups {
		g := &m.Groups[i]
		if err := g.Rewrap(k.lineage, k.keys[g.ID], readers); err != nil {
			return ch, err
		}
	}
	if len(extra) > 0 {
		id := "g" + strconv.Itoa(m.nextGroupNumber())
		g, dk, err := NewGroup(id, k.lineage, readers)
		if err != nil {
			return ch, err
		}
		m.Groups = append(m.Groups, g)
		m.Current, k.current, k.keys[id] = id, id, dk
		ch.NewGroup = id
	}
	m.Readers = SetID(readers)
	return ch, nil
}

// Prune drops groups, other than the current one, that inUse reports as unused.
func (m *Meta) Prune(k *Keys, inUse func(group string) bool) (dropped []string) {
	m.Groups = slices.DeleteFunc(m.Groups, func(g Group) bool {
		if g.ID == m.Current || inUse(g.ID) {
			return false
		}
		delete(k.keys, g.ID)
		dropped = append(dropped, g.ID)
		return true
	})
	return
}

func (m *Meta) group(id string) *Group {
	for i := range m.Groups {
		if m.Groups[i].ID == id {
			return &m.Groups[i]
		}
	}
	return nil
}

func (m *Meta) nextGroupNumber() int {
	n := 0
	for _, g := range m.Groups {
		if v, err := strconv.Atoi(g.ID[1:]); err == nil && v > n {
			n = v
		}
	}
	return n + 1
}

// Status is what `protect status` and the viewer's Access view show. It needs
// no key.
type Status struct {
	Readers []StatusReader // from the store: who has a lock in the current group
	Missing []string       // in the readers file, no lock yet: run rewrap
	Extra   []string       // has a lock, not in the readers file: run rewrap
	Matches bool           // the file and the store agree
	Groups  int
	Current string
}

// StatusReader is one reader the store knows.
type StatusReader struct {
	Name string
	Key  ReaderPublic
}

// Status compares the store with a parsed readers file (nil if there is none).
func (m *Meta) Status(readers []Reader) Status {
	st := Status{Groups: len(m.Groups), Current: m.Current}
	cur := m.group(m.Current)
	if cur == nil {
		return st
	}
	for _, l := range cur.Locks {
		st.Readers = append(st.Readers, StatusReader{l.Name, l.Key})
	}
	st.Missing, st.Extra = cur.Diff(readers)
	st.Matches = readers != nil && len(st.Missing) == 0 && len(st.Extra) == 0
	return st
}

// Current is the group new writes are sealed in.
func (k *Keys) Current() string { return k.current }

// Seal seals in the current group; c.Group is set for the caller.
func (k *Keys) Seal(c Ctx, plaintext []byte) Sealed {
	c.Group = k.current
	return Seal(k.keys[k.current], c, plaintext)
}

// SealJSON marshals v and seals it in the current group.
func (k *Keys) SealJSON(c Ctx, v any) (Sealed, error) {
	c.Group = k.current
	return SealJSON(k.keys[k.current], c, v)
}

// Open opens a sealed value in the group it names. c.Lineage is set from the store.
func (k *Keys) Open(c Ctx, s Sealed) ([]byte, error) {
	dk, ok := k.keys[s.Group]
	if !ok {
		return nil, fmt.Errorf("protect: sealed in group %q, which your key cannot open", s.Group)
	}
	c.Group = s.Group
	return Open(dk, c, s)
}

// OpenJSON opens s and unmarshals it into v.
func (k *Keys) OpenJSON(c Ctx, s Sealed, v any) error {
	pt, err := k.Open(c, s)
	if err != nil {
		return err
	}
	defer clear(pt)
	return unmarshalJSON(pt, v)
}

// Close wipes the data keys.
func (k *Keys) Close() {
	for id, dk := range k.keys {
		dk.Wipe()
		delete(k.keys, id)
	}
}
