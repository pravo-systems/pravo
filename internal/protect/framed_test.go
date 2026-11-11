package protect

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// The bytes of the digests that are stored must not change: this is the closure that was
// written four times, kept as the reference.
func TestDigestFramedIsTheStoredEncoding(t *testing.T) {
	parts := [][]byte{[]byte("lineage"), nil, []byte("key"), []byte("sig-bytes")}
	h := sha256.New()
	for _, b := range parts {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	want := hex.EncodeToString(h.Sum(nil))
	if got := DigestFramed(parts...); got != want {
		t.Fatalf("DigestFramed = %s, want %s", got, want)
	}
	// The split of the same bytes is another digest.
	if DigestFramed([]byte("ab"), []byte("c")) == DigestFramed([]byte("a"), []byte("bc")) {
		t.Error("two splits of the same bytes must differ")
	}
}
