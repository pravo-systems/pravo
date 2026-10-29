package state

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// Promotions. A state that runs a specification can say so to the states that come
// after it: an attestation is a signed object beside the records,
//
//	promotions/<fingerprint>.<key>.json
//
// that names a specification by its fingerprint and the digests of its tags, the
// newest transaction of the state, and the key of the person who vouches for it. A state
// further along (production) reads that one object, and a policy of its own decides
// whether it suffices (see engine/promote.go). The attestation is signed in a domain of
// its own and with the lineage of the state it lies in, so it verifies nowhere else.
// It carries the public signing key, and a verifier trusts the key because the policy
// pins its fingerprint, not because the state that holds the object lists it: the state
// that produced it is not the one that decides.

// PromotionDir is the prefix of the attestations.
const PromotionDir = "promotions"

const (
	promotionVersion = 1
	// PromotionKind says what the attestation vouches for: the fingerprint of the
	// specification and the digests of the tags it was applied with. A later kind can
	// vouch for something else without a migration.
	PromotionKind = "config-fingerprint/v1"
	maxPromotion  = 8 << 10
)

var promotionKeyRe = regexp.MustCompile(`^promotions/([0-9a-f]{32})\.([0-9a-f]{16})\.json$`)

// Promotion is one attestation.
type Promotion struct {
	V    int    `json:"v"`
	Kind string `json:"kind"`
	// Fingerprint and Tags name the specification: the fingerprint of the root and the
	// digest of each tag it was applied with (SpecTagDigest).
	Fingerprint string            `json:"fingerprint"`
	Tags        map[string]string `json:"tags,omitempty"`
	// Txn is the newest transaction of the state that had finished when the key signed, and Applied
	// when it finished. They place the attestation in the history of the state.
	Txn     string    `json:"txn,omitempty"`
	Applied time.Time `json:"applied,omitempty"`
	// At is when the key signed. A soak time counts from it.
	At   time.Time `json:"at"`
	Note string    `json:"note,omitempty"`
	// SignerKey is the public signing key of the signature, so that a verifier needs
	// nothing from the state that holds the object.
	SignerKey []byte             `json:"signer_key"`
	Signature *protect.Signature `json:"signature"`
}

func promotionParts(p *Promotion) [][]byte {
	return [][]byte{
		[]byte(p.Kind), SpecPart(p.Fingerprint, p.Tags), []byte(p.Txn),
		[]byte(p.Applied.UTC().Format(time.RFC3339Nano)), []byte(p.At.UTC().Format(time.RFC3339Nano)), text(p.Note),
	}
}

// PromotionKey is the key of the attestation of fingerprint by the key sigKey.
func PromotionKey(fingerprint, sigKey string) string {
	sum := sha256.Sum256([]byte(sigKey))
	return PromotionDir + "/" + fingerprint + "." + hex.EncodeToString(sum[:8]) + ".json"
}

// Promote signs the attestation that the state runs the specification with the given fingerprint
// and tags, and writes it beside the records. The caller has shown that the state does: a plan of the
// specification against it changes nothing. The state must be sealed, since an attestation is a
// signature. An attestation by the same key is replaced, so that a later attestation of the same
// specification moves the time it counts from.
func (s *Store) Promote(ctx context.Context, fingerprint string, tags []string, note string) (*Promotion, error) {
	id := s.signingIdentity()
	if id == nil {
		return nil, errors.New("the state is not sealed, and an attestation is a signature (pravo protect init --encrypt)")
	}
	p := &Promotion{V: promotionVersion, Kind: PromotionKind, Fingerprint: fingerprint, Tags: SpecTagDigests(tags), At: time.Now().UTC(), Note: note}
	// The newest transaction that finished places the attestation in the history of the state: a
	// reader sees whether the state changed after it was signed.
	if txns, err := s.Transactions(); err == nil {
		for _, t := range txns {
			if t.Finished != nil {
				p.Txn, p.Applied = t.ID, t.Finished.UTC()
				break
			}
		}
	}
	if err := p.Sign(id, s.Lineage()); err != nil {
		return nil, err
	}
	sig := *p.Signature
	enc, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	key := PromotionKey(p.Fingerprint, sig.Key)
	ok, err := s.b.PutIfAbsent(ctx, key, enc)
	if err != nil {
		return nil, fmt.Errorf("writing the attestation: %w", Advise(err))
	}
	if !ok {
		if _, err := s.b.ReplaceIf(ctx, key, func([]byte) ([]byte, bool) { return enc, true }); err != nil && !errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("writing the attestation: %w", Advise(err))
		}
	}
	return p, nil
}

// Sign signs the attestation with the signing key of id, for the state of lineage, and sets the key
// that verifies it.
func (p *Promotion) Sign(id *protect.Identity, lineage string) error {
	p.SignerKey = id.SignPub
	sig, err := id.SignMessage(protect.DomainPromotion, lineage, promotionParts(p)...)
	if err != nil {
		return fmt.Errorf("signing the attestation: %w", err)
	}
	p.Signature = &sig
	return nil
}

// ReadLineage reads the lineage of the state in b from its meta.json, which is not secret.
func ReadLineage(ctx context.Context, b Backend) (string, error) {
	m, err := readMeta(ctx, b)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", fmt.Errorf("%s holds no state", b.Name())
		}
		return "", err
	}
	return m.Lineage, nil
}

// PromotionObject is an attestation as it was read, or the reason it cannot be one.
type PromotionObject struct {
	Key string
	*Promotion
	// Err is set for an object that is under the name of an attestation and is not one.
	Err error
}

// ReadPromotions lists the attestations of fingerprint in the state of b, or all of them
// when fingerprint is empty. It reads objects and creates nothing.
func ReadPromotions(ctx context.Context, b Backend, fingerprint string) ([]PromotionObject, error) {
	prefix := PromotionDir
	keys, err := b.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var out []PromotionObject
	for _, k := range keys {
		m := promotionKeyRe.FindStringSubmatch(k)
		if m == nil || (fingerprint != "" && m[1] != fingerprint) {
			continue
		}
		data, err := b.Get(ctx, k)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		o := PromotionObject{Key: k}
		var p Promotion
		switch {
		case len(data) > maxPromotion:
			o.Err = errors.New("larger than an attestation can be")
		case json.Unmarshal(data, &p) != nil || p.V != promotionVersion || p.Signature == nil:
			o.Err = errors.New("not an attestation")
		case k != PromotionKey(p.Fingerprint, p.Signature.Key):
			o.Err = errors.New("under the name of another fingerprint or key")
		default:
			o.Promotion = &p
		}
		out = append(out, o)
	}
	return out, nil
}

// Verify checks the signature of p against the lineage of the state it lies in. pinned
// are the fingerprints of the keys that may sign ("SHA256:…", as ssh-keygen -l prints
// them); with none, any key that signed passes, and the caller says so. It returns the
// status and, when the key is not one of the pinned, nothing else is said about it.
func (p *Promotion) Verify(lineage string, pinned []string) protect.SigStatus {
	if p.Kind != PromotionKind || len(p.SignerKey) != ed25519.PublicKeySize || p.Signature == nil {
		return protect.SigInvalid
	}
	key := ed25519.PublicKey(p.SignerKey)
	fp := protect.SSHFingerprint(key)
	if fp != p.Signature.Key {
		return protect.SigInvalid
	}
	if len(pinned) > 0 {
		listed := false
		for _, want := range pinned {
			if strings.TrimSpace(want) == fp {
				listed = true
			}
		}
		if !listed {
			return protect.SigUnknownKey
		}
	}
	status, _ := protect.VerifySignature(p.Signature, []protect.Reader{{Name: p.Signature.Signer, Sign: key}}, protect.DomainPromotion, lineage, promotionParts(p)...)
	return status
}
