package protect

import (
	"crypto/ecdh"
	"crypto/hpke"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ReaderKeyPrefix starts the text form of a reader public key.
const ReaderKeyPrefix = "x25519:"

// kem is DHKEM(X25519, HKDF-SHA256), KEM id 0x0020.
var kem = hpke.DHKEM(ecdh.X25519())

// ReaderPublic is the public half of a reader key: what goes on a line of
// .pravo/access/readers.txt and what a data key is locked for.
type ReaderPublic struct{ pk hpke.PublicKey }

// ReaderKey is a reader's private key. It never leaves the machine of the
// reader, except sealed in an identity file.
type ReaderKey struct{ sk hpke.PrivateKey }

// GenerateReaderKey makes a new X25519 reader key.
func GenerateReaderKey() (*ReaderKey, error) {
	sk, err := kem.GenerateKey()
	if err != nil {
		return nil, err
	}
	return &ReaderKey{sk}, nil
}

// ReaderKeyFromBytes loads the 32-byte private key from Bytes.
func ReaderKeyFromBytes(b []byte) (*ReaderKey, error) {
	sk, err := kem.NewPrivateKey(b)
	if err != nil {
		return nil, fmt.Errorf("protect: bad reader key: %w", err)
	}
	return &ReaderKey{sk}, nil
}

// Bytes returns the 32-byte private key. The caller should clear the slice
// when done (clear(b)); Go gives no stronger promise.
func (k *ReaderKey) Bytes() []byte {
	b, err := k.sk.Bytes()
	if err != nil { // cannot happen for X25519
		panic(err)
	}
	return b
}

// Public returns the public half.
func (k *ReaderKey) Public() ReaderPublic { return ReaderPublic{k.sk.PublicKey()} }

// ParsePublic parses "x25519:BASE64URL" (unpadded, 43 characters). It rejects
// the all-zero and other low-order points, which parse fine in crypto/ecdh and
// only fail later in ECDH.
func ParsePublic(s string) (ReaderPublic, error) {
	rest, ok := strings.CutPrefix(s, ReaderKeyPrefix)
	if !ok {
		return ReaderPublic{}, fmt.Errorf("reader key %q: want %sBASE64URL", abbreviate(s), ReaderKeyPrefix)
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(rest)
	if err != nil || len(raw) != 32 {
		return ReaderPublic{}, fmt.Errorf("reader key %q: want 32 bytes as unpadded base64url", abbreviate(s))
	}
	return publicFromBytes(raw)
}

func publicFromBytes(raw []byte) (ReaderPublic, error) {
	pk, err := kem.NewPublicKey(raw)
	if err != nil {
		return ReaderPublic{}, fmt.Errorf("reader key: %w", err)
	}
	// A low-order point makes every shared secret zero. ECDH reports that.
	probe, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return ReaderPublic{}, err
	}
	xp, err := ecdh.X25519().NewPublicKey(raw)
	if err != nil {
		return ReaderPublic{}, fmt.Errorf("reader key: %w", err)
	}
	if _, err := probe.ECDH(xp); err != nil {
		return ReaderPublic{}, errors.New("reader key is a low-order point")
	}
	return ReaderPublic{pk}, nil
}

func abbreviate(s string) string {
	if len(s) > 24 {
		return s[:24] + "…"
	}
	return s
}

// Bytes returns the 32-byte public key.
func (p ReaderPublic) Bytes() []byte { return p.pk.Bytes() }

// String is the text form that goes on a readers line.
func (p ReaderPublic) String() string {
	return ReaderKeyPrefix + base64.RawURLEncoding.EncodeToString(p.pk.Bytes())
}

// Short is the form the viewer and `id show` print: "x25519:7Hc3Kp…".
func (p ReaderPublic) Short() string {
	return ReaderKeyPrefix + base64.RawURLEncoding.EncodeToString(p.pk.Bytes())[:6] + "…"
}

// IsZero reports whether p is the zero value.
func (p ReaderPublic) IsZero() bool { return p.pk == nil }

// Equal compares two public keys. Public values need no constant-time compare;
// this uses one anyway so that callers cannot get it wrong for secrets by copying.
func (p ReaderPublic) Equal(q ReaderPublic) bool {
	if p.pk == nil || q.pk == nil {
		return false
	}
	return subtle.ConstantTimeCompare(p.pk.Bytes(), q.pk.Bytes()) == 1
}

// ID is a short stable name for the key, for logs and for matching a lock to
// a reader: 8 bytes of SHA-256 of the key, hex. It is not a security boundary.
func (p ReaderPublic) ID() string {
	sum := sha256.Sum256(p.pk.Bytes())
	return hex.EncodeToString(sum[:8])
}

// MarshalText and UnmarshalText make ReaderPublic a JSON string.
func (p ReaderPublic) MarshalText() ([]byte, error) { return []byte(p.String()), nil }
func (p *ReaderPublic) UnmarshalText(b []byte) error {
	q, err := ParsePublic(string(b))
	if err != nil {
		return err
	}
	*p = q
	return nil
}

// randBytes returns n random bytes. crypto/rand.Read cannot fail since Go 1.24:
// it crashes the program if the system has no randomness.
func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}
