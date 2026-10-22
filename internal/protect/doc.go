// Package protect holds the cryptography of sealed state: reader keys, the
// reader list, data keys with their locks, sealed values, and identity files.
//
// It depends on the standard library only (Go 1.26: crypto/hpke, crypto/hkdf,
// crypto/pbkdf2, crypto/cipher.NewGCMWithRandomNonce). It does not know what a
// state store, a record or a backend is; internal/state calls it.
//
// Suite, named "hpke-x25519-aes256gcm" in every sealed object:
//   - a data key is 32 random bytes;
//   - a data key is locked for one reader with HPKE base mode:
//     DHKEM(X25519, HKDF-SHA256), HKDF-SHA256, AES-256-GCM (RFC 9180);
//   - a value is sealed with AES-256-GCM under the data key, with a random
//     96-bit nonce and associated data that binds where the value belongs.
package protect

import (
	"encoding/binary"
	"errors"
)

// Alg names the suite in sealed objects. Open refuses any other value.
const Alg = "hpke-x25519-aes256gcm"

// ErrNotReader means no lock in the group is for the key that was tried.
var ErrNotReader = errors.New("protect: not a reader of this group")

// ErrAuth means authentication failed: wrong key, wrong place, or damaged bytes.
// Callers must not say which.
var ErrAuth = errors.New("protect: message authentication failed")

// pae is a length-prefixed encoding of its parts that cannot be confused with
// any other split of the same bytes (as PASETO's PAE and SSHSIG do). Every
// preimage and every AAD in this package is built with it.
func pae(parts ...[]byte) []byte {
	n := 8
	for _, p := range parts {
		n += 8 + len(p)
	}
	out := make([]byte, 0, n)
	out = binary.LittleEndian.AppendUint64(out, uint64(len(parts)))
	for _, p := range parts {
		out = binary.LittleEndian.AppendUint64(out, uint64(len(p)))
		out = append(out, p...)
	}
	return out
}

func u64(v uint64) []byte { return binary.LittleEndian.AppendUint64(nil, v) }
