package protect

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
)

// WriteFramed writes b to w behind its length as eight bytes, big-endian. It is the framing
// of the digests that an admin vouches for and of the keys of the plan memo: a list of parts
// that is written this way cannot be read as another split of the same bytes. The digests
// that were stored are in this byte order, so it stays; pae, which frames what is signed, is
// little-endian and counts its parts first, and is not interchangeable with this.
func WriteFramed(w io.Writer, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	_, _ = w.Write(n[:])
	_, _ = w.Write(b)
}

// DigestFramed is the hex SHA-256 of the parts, each written by WriteFramed.
func DigestFramed(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		WriteFramed(h, p)
	}
	return hex.EncodeToString(h.Sum(nil))
}
