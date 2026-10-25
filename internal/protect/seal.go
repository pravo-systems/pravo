package protect

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"
)

// Kinds of sealed object. The kind is part of the associated data, so a sealed
// outputs.json cannot be presented as a record.
const (
	KindRecord  = "record"      // attributes + private data (+ config hash) of one resource
	KindOutputs = "outputs"     // outputs.json
	KindPlan    = "plan"        // plans/<id>.plan.json
	KindCommand = "txn-cmd"     // the sealed values of a transaction's command line
	KindPlanCmd = "plan-cmd"    // the same, in plans/<id>.meta.json
	KindSummary = "txn-summary" // the text of the error of a transaction that failed or was rejected
	KindDiff    = "txn-diff"    // what the changes of a transaction did to the attributes of their records
)

// Ctx says where a sealed value belongs. Every field is bound into the AEAD as
// associated data, so a sealed value that is moved to another address, another
// revision, another generation of the same address, another state (lineage),
// another group or another kind fails to open. The fields are not secret and
// the caller has them at hand when it reads: they come from the clear part of the
// stored object and from meta.json.
type Ctx struct {
	Lineage    string // meta.json lineage
	Group      string // "g1": the group that sealed it
	Kind       string // one of the Kind constants
	Name       string // address of a resource, id of a plan or transaction, "" for outputs
	Generation string // Resource.Generation; "" where there is none
	Revision   uint64 // Resource.Revision; 0 where there is none
}

// AAD returns the bytes bound into the cipher. Length-prefixed, so ("ab","c") and
// ("a","bc") differ.
func (c Ctx) AAD() []byte {
	return pae([]byte("pravo/seal/v1"), []byte(c.Lineage), []byte(c.Group), []byte(c.Kind),
		[]byte(c.Name), []byte(c.Generation), u64(c.Revision))
}

// Sealed is the JSON shape in a stored object: {"alg":"…","group":"g1","ct":"…"}.
// CT is nonce (12) || ciphertext || tag (16), in std base64 with padding.
type Sealed struct {
	Alg   string `json:"alg"`
	Group string `json:"group"`
	CT    []byte `json:"ct"`
}

// Overhead is what Seal adds to the plaintext.
const Overhead = 28

func newGCM(dk DataKey) cipher.AEAD {
	blk, err := aes.NewCipher(dk.b[:])
	if err != nil {
		panic(err) // key length is fixed
	}
	// A random 96-bit nonce is chosen and prepended on Seal, and cannot be
	// passed in (passing one panics). A key must not seal more than 2^32 values.
	g, err := cipher.NewGCMWithRandomNonce(blk)
	if err != nil {
		panic(err)
	}
	return g
}

// Seal encrypts plaintext under the data key of c.Group.
func Seal(dk DataKey, c Ctx, plaintext []byte) Sealed {
	return Sealed{Alg: Alg, Group: c.Group, CT: newGCM(dk).Seal(nil, nil, plaintext, c.AAD())}
}

// Open decrypts. Every failure (wrong key, wrong place, damaged bytes) is ErrAuth,
// except an unknown algorithm or a group other than c.Group, which are named.
func Open(dk DataKey, c Ctx, s Sealed) ([]byte, error) {
	if s.Alg != Alg {
		return nil, fmt.Errorf("protect: sealed with %q; this pravo knows %q (written by a newer pravo?)", s.Alg, Alg)
	}
	if s.Group != c.Group {
		return nil, fmt.Errorf("protect: sealed in group %q, opened with group %q", s.Group, c.Group)
	}
	if len(s.CT) < Overhead {
		return nil, ErrAuth
	}
	pt, err := newGCM(dk).Open(nil, nil, s.CT, c.AAD())
	if err != nil {
		return nil, ErrAuth
	}
	return pt, nil
}

// SealJSON marshals v and seals it.
func SealJSON(dk DataKey, c Ctx, v any) (Sealed, error) {
	pt, err := json.Marshal(v)
	if err != nil {
		return Sealed{}, err
	}
	defer clear(pt)
	return Seal(dk, c, pt), nil
}

// OpenJSON opens s and unmarshals the plaintext into v.
func OpenJSON(dk DataKey, c Ctx, s Sealed, v any) error {
	pt, err := Open(dk, c, s)
	if err != nil {
		return err
	}
	defer clear(pt)
	return unmarshalJSON(pt, v)
}

func unmarshalJSON(pt []byte, v any) error {
	if err := json.Unmarshal(pt, v); err != nil {
		return errors.New("protect: sealed value is not the expected JSON")
	}
	return nil
}
