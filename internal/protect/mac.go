package protect

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// HashKey keys the config hash of a record. The unkeyed hash (sha256 of the
// config JSON, 8 bytes) confirms a guessed secret: anyone with a copy of the
// bucket can hash a candidate value and compare. Keyed, it cannot be checked
// without the key. A reader already sees the values, so the key goes to readers:
// wrap it with Wrap exactly like a data key (it IS a DataKey-shaped secret).
//
// It must not be derived from a group's data key. Groups are replaced when a
// reader is removed, and a new key would change every hash, so the next plan would
// call every resource changed. Make one HashKey at `protect init --encrypt`, keep
// it for the life of the state, and store it locked for the readers beside the
// groups. See the notes for the simpler alternative: seal the hash inside the record.
type HashKey struct{ b [32]byte }

func NewHashKey() HashKey { return HashKey{NewDataKey().b} }

// Sum returns 8 bytes of HMAC-SHA256 as 16 hex characters, the same shape as
// the unkeyed hash. The MAC key is derived with HKDF from the hash key and the
// lineage, so two states never share a key.
func (h HashKey) Sum(lineage string, configJSON []byte) string {
	k, err := hkdf.Key(sha256.New, h.b[:], []byte(lineage), "pravo/config-hash/v1", 32)
	if err != nil {
		panic(err)
	}
	defer clear(k)
	m := hmac.New(sha256.New, k)
	m.Write(configJSON)
	return hex.EncodeToString(m.Sum(nil)[:8])
}
