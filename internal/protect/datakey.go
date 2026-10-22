package protect

import (
	"crypto/hpke"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"
)

// DataKey is the 256-bit key of one reader group. It is never stored in the
// clear: the store keeps one Lock per reader.
type DataKey struct{ b [32]byte }

// NewDataKey returns a fresh random data key.
func NewDataKey() DataKey {
	var d DataKey
	rand.Read(d.b[:])
	return d
}

// Wipe overwrites the key. It is best effort: copies made by the runtime (stack
// growth, register spills) are not reachable.
func (d *DataKey) Wipe() { clear(d.b[:]) }

// Equal compares in constant time.
func (d DataKey) Equal(e DataKey) bool { return subtle.ConstantTimeCompare(d.b[:], e.b[:]) == 1 }

// WrapCtx is bound into every lock, so a lock cut from one group of one state
// cannot be pasted into another.
type WrapCtx struct {
	Lineage string
	Group   string
}

func (c WrapCtx) info() []byte {
	return pae([]byte("pravo/lock/v1"), []byte(c.Lineage), []byte(c.Group))
}

// Wrap locks the data key for one reader: HPKE base mode, one fresh ephemeral
// key per call. The result is 80 bytes: enc (32) || key (32) || tag (16).
func Wrap(dk DataKey, to ReaderPublic, c WrapCtx) ([]byte, error) {
	if to.IsZero() {
		return nil, errors.New("protect: no reader key")
	}
	return hpke.Seal(to.pk, hpke.HKDFSHA256(), hpke.AES256GCM(), c.info(), dk.b[:])
}

// Unwrap opens a lock with the reader's private key.
func Unwrap(box []byte, me *ReaderKey, c WrapCtx) (DataKey, error) {
	pt, err := hpke.Open(me.sk, hpke.HKDFSHA256(), hpke.AES256GCM(), c.info(), box)
	if err != nil {
		return DataKey{}, ErrAuth
	}
	defer clear(pt)
	if len(pt) != 32 {
		return DataKey{}, ErrAuth
	}
	var d DataKey
	copy(d.b[:], pt)
	return d, nil
}

// Lock is the data key of a group, locked for one reader. It is what goes in
// meta.json. Name is informational (a reader can be renamed without a new lock);
// Key says whom the lock is for and is what matching uses.
type Lock struct {
	Name string       `json:"name"`
	Key  ReaderPublic `json:"key"`
	Box  []byte       `json:"box"` // 80 bytes, base64 in JSON
}

// Group is a data key and its locks. Group ids are g1, g2, ... A removal of a
// reader makes a new group; records stay in the old group until they are
// written again or `rewrap --all`.
type Group struct {
	ID    string `json:"id"`
	Alg   string `json:"alg"`
	Locks []Lock `json:"locks"`
}

// NewGroup makes a data key and locks it for the readers.
func NewGroup(id, lineage string, readers []Reader) (Group, DataKey, error) {
	dk := NewDataKey()
	g := Group{ID: id, Alg: Alg}
	if err := g.Rewrap(lineage, dk, readers); err != nil {
		return Group{}, DataKey{}, err
	}
	return g, dk, nil
}

// Unlock finds the lock for me and opens it.
func (g Group) Unlock(lineage string, me *ReaderKey) (DataKey, error) {
	if g.Alg != Alg {
		return DataKey{}, fmt.Errorf("protect: group %s uses %q, written by a newer pravo", g.ID, g.Alg)
	}
	mine := me.Public()
	for _, l := range g.Locks {
		if l.Key.Equal(mine) {
			return Unwrap(l.Box, me, WrapCtx{lineage, g.ID})
		}
	}
	return DataKey{}, ErrNotReader
}

// Rewrap makes the locks match the readers, keeping the data key. A reader who
// already has a lock keeps it byte for byte, only the name may change. New
// readers get a new lock. Readers that are gone lose theirs. Callers compare the
// group before and after (Diff, or reflect.DeepEqual) to skip a write.
func (g *Group) Rewrap(lineage string, dk DataKey, readers []Reader) error {
	have := map[string]Lock{}
	for _, l := range g.Locks {
		have[l.Key.String()] = l
	}
	next := make([]Lock, 0, len(readers))
	for _, r := range readers {
		if l, ok := have[r.Key.String()]; ok {
			l.Name = r.Name
			next = append(next, l)
			continue
		}
		box, err := Wrap(dk, r.Key, WrapCtx{lineage, g.ID})
		if err != nil {
			return err
		}
		next = append(next, Lock{Name: r.Name, Key: r.Key, Box: box})
	}
	g.Locks = next
	return nil
}

// Diff compares the reader keys that have locks with a reader list. `protect
// status` and the viewer show missing (in the file, no lock yet: run rewrap) and
// extra (a lock for someone no longer in the file: run rewrap). Names are returned.
func (g Group) Diff(readers []Reader) (missing, extra []string) {
	locked := map[string]string{}
	for _, l := range g.Locks {
		locked[l.Key.String()] = l.Name
	}
	listed := map[string]bool{}
	for _, r := range readers {
		listed[r.Key.String()] = true
		if _, ok := locked[r.Key.String()]; !ok {
			missing = append(missing, r.Name)
		}
	}
	for k, n := range locked {
		if !listed[k] {
			extra = append(extra, n)
		}
	}
	slices.Sort(extra)
	slices.Sort(missing)
	return
}
