package protect

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Tag returns a short keyed tag of value: the first 4 bytes of an HMAC-SHA256,
// as 8 hexadecimal characters. The MAC key is derived with HKDF from the data
// key of the current group, so equal values have equal tags within a state and
// a group, and the tag tells nothing about the value to anyone without the key.
// It stands in for a value that is sealed in a place where it is only compared,
// such as the values in a command line that the history of the state shows.
//
// Eight characters are enough to tell values apart and few enough that a tag
// cannot be used to check a guess: there are 2^32 of them, and a guess that
// fits a tag by chance is no evidence.
func (k *Keys) Tag(value string) string {
	dk, ok := k.keys[k.current]
	if !ok {
		return ""
	}
	mk, err := hkdf.Key(sha256.New, dk.b[:], []byte(k.lineage), "pravo/cmd/v1", 32)
	if err != nil {
		panic(err) // the length is fixed
	}
	defer clear(mk)
	m := hmac.New(sha256.New, mk)
	m.Write([]byte(value))
	return hex.EncodeToString(m.Sum(nil)[:4])
}
