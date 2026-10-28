package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/state"
)

// Who stored a plan. In a sealed state the identity that stores a plan signs its digest,
// and the signature is an object of its own beside the plan:
//
//	plans/<id>.sig-<key>.json   {"v":1,"id":"plan-2f9c1a4e","at":"…","signature":{"signer":…,"key":…,"sig":…}}
//
// <key> is the first 16 hexadecimal digits of the SHA-256 of the signing key. The object is
// created and never replaced, so a second person who stores the same plan adds a signature
// and the first stays. The signature covers the full SHA-256 of the plan file in hex and the
// time. Neither is stored: a reader recomputes the digest from the plan, so the object holds
// nothing that a guess of the plan could be checked against. It does not cover the summary,
// whose command and tags are rewritten when the state is sealed again.
//
// An object that was added by someone without a listed key shows as a signature by a key
// that is not in the file; one that names a listed key and does not verify shows as invalid.
// Neither stops an apply: anyone who can write to the bucket can add objects.

const (
	sigVersion = 1
	maxSigSize = 4 << 10
)

var sigObjectRe = regexp.MustCompile(`^(plan-[0-9a-f]{6,64})\.(?:sig|ok)-([0-9a-f]{16})\.json$`)

func sigKey(id, keyID string) string { return PlanDir + "/" + id + ".sig-" + keyID + ".json" }

// okKey is the key of the approval of the plan id by the key keyID (see approval.go).
func okKey(id, keyID string) string { return PlanDir + "/" + id + ".ok-" + keyID + ".json" }

// signatureID is the part of the name of a signature object that names the key.
func signatureID(fingerprint string) string {
	sum := sha256.Sum256([]byte(fingerprint))
	return hex.EncodeToString(sum[:8])
}

// PlanSignature is the object of a signature.
type PlanSignature struct {
	V         int                `json:"v"`
	ID        string             `json:"id"`
	At        time.Time          `json:"at"`
	Signature *protect.Signature `json:"signature"`
}

// PlanDigest is the SHA-256 of a plan file, in hex.
func PlanDigest(plain []byte) string {
	sum := sha256.Sum256(plain)
	return hex.EncodeToString(sum[:])
}

func planSigParts(digest string, at time.Time) [][]byte {
	return [][]byte{[]byte(digest), []byte(at.UTC().Format(time.RFC3339Nano))}
}

// signPlan makes the signature of the plan file data, or none for a state that is not
// sealed.
func (ps *PlanStore) signPlan(data []byte) (*PlanSignature, error) {
	if ps.st == nil {
		return nil, nil
	}
	at := ps.now()
	sig, ok, err := ps.st.SignMessage(protect.DomainPlan, planSigParts(PlanDigest(data), at)...)
	if err != nil {
		return nil, fmt.Errorf("storing the plan: %w", err)
	}
	if !ok {
		return nil, nil
	}
	return &PlanSignature{V: sigVersion, At: at, Signature: &sig}, nil
}

// putSignature creates the signature object of the plan id. A signature by the same key is
// there already when the plan was stored by the same identity before, which is no error.
func (ps *PlanStore) putSignature(ctx context.Context, id string, sig *PlanSignature) error {
	if sig == nil {
		return nil
	}
	sig.ID = id
	enc, err := indentJSON(sig)
	if err != nil {
		return err
	}
	key := sigKey(id, signatureID(sig.Signature.Key))
	ok, err := ps.B.PutIfAbsent(ctx, key, enc)
	if err != nil {
		return fmt.Errorf("storing the plan: %w", state.Advise(err))
	}
	if ok {
		return nil
	}
	// An object is there already: this identity's own signature of an earlier storing, or
	// something put under the name of its key by someone who writes to the bucket. Only the
	// first is kept; the second would stand in the way of the real signature for good.
	cur, err := ps.B.Get(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("storing the plan: %w", err)
	}
	var old PlanSignature
	if len(cur) <= maxSigSize && json.Unmarshal(cur, &old) == nil && old.V == sigVersion && old.ID == id && old.Signature != nil && old.Signature.Key == sig.Signature.Key {
		return nil
	}
	_, err = ps.B.ReplaceIf(ctx, key, func(c []byte) ([]byte, bool) { return enc, bytes.Equal(c, cur) })
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("storing the plan: %w", state.Advise(err))
	}
	return nil
}

// collectOrphanSignatures removes the signature objects of plans that are gone: a collection
// that stopped between its deletes, or a store that raced one. A signature that is young is
// left alone, since its plan may be about to appear.
func (ps *PlanStore) collectOrphanSignatures(ctx context.Context) {
	all, err := ps.ids(ctx)
	if err != nil {
		return
	}
	for id, kinds := range all {
		if !kinds["sig"] || kinds["plan"] || kinds["meta"] {
			continue
		}
		keys, err := ps.objectsWith(ctx, id, "")
		if err != nil {
			continue
		}
		for _, k := range keys {
			data, err := ps.B.Get(ctx, k)
			if err != nil {
				continue
			}
			var obj PlanSignature
			if json.Unmarshal(data, &obj) == nil && ps.now().Sub(obj.At) < time.Hour && !obj.At.IsZero() {
				continue
			}
			_, _ = ps.B.DeleteIf(ctx, k, func(c []byte) bool { return bytes.Equal(c, data) })
		}
	}
}

// sigObjects lists the keys of the signature objects of a plan.
func (ps *PlanStore) sigObjects(ctx context.Context, id string) ([]string, error) {
	return ps.objectsWith(ctx, id, ".sig-")
}

// objectsWith lists the keys of the objects of a plan whose name has infix: ".sig-" for
// the signatures of its storing, ".ok-" for its approvals, "" for both.
func (ps *PlanStore) objectsWith(ctx context.Context, id, infix string) ([]string, error) {
	keys, err := ps.B.List(ctx, PlanDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, k := range keys {
		if i, kind, ok := planObject(k); ok && i == id && kind == "sig" && strings.Contains(k, infix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

// deleteSignatures removes the signature and approval objects of a plan that is being removed.
func (ps *PlanStore) deleteSignatures(ctx context.Context, id string) {
	keys, err := ps.objectsWith(ctx, id, "")
	if err != nil {
		return
	}
	for _, k := range keys {
		_, _ = ps.B.DeleteIf(ctx, k, func(current []byte) bool {
			var h struct {
				ID string `json:"id"`
			}
			return json.Unmarshal(current, &h) == nil && h.ID == id
		})
	}
}

// PlanVerdict is what a verifier makes of one signature of a stored plan.
type PlanVerdict struct {
	Status protect.SigStatus
	// Signer is the name the readers file gives the key when the signature verified;
	// Claimed is the name the object gives itself.
	Signer, Claimed string
	// Key is the fingerprint of the signing key that the object names, verified or not.
	Key string
	At  time.Time
}

// PlanDigest returns the digest of a stored plan: the SHA-256 in hex of the plan file
// as a reader opens it. A plan that this store cannot open is state.ErrNotReader.
func (ps *PlanStore) PlanDigest(ctx context.Context, id string) (string, error) {
	data, err := ps.B.Get(ctx, planKey(id))
	if err != nil {
		return "", err
	}
	plain, err := ps.openPlan(id, data)
	if err != nil {
		return "", err
	}
	defer clear(plain)
	return PlanDigest(plain), nil
}

// Signatures reads and checks the signatures of a stored plan against the readers. A
// plan with none comes back with none. A plan that this store cannot open cannot be
// checked: that is state.ErrNotReader.
func (ps *PlanStore) Signatures(ctx context.Context, id string, readers []protect.Reader) ([]PlanVerdict, error) {
	keys, err := ps.sigObjects(ctx, id)
	if err != nil || len(keys) == 0 {
		return nil, err
	}
	digest, err := ps.PlanDigest(ctx, id)
	if err != nil {
		return nil, err
	}
	return ps.SignaturesOf(ctx, id, digest, readers)
}

// SignaturesOf checks the signatures of a stored plan against the readers, for a caller that
// has the digest of the plan already (PlanFile.Digest).
func (ps *PlanStore) SignaturesOf(ctx context.Context, id, digest string, readers []protect.Reader) ([]PlanVerdict, error) {
	return ps.verdicts(ctx, id, digest, readers, ".sig-", protect.DomainPlan, sigKey)
}

// verdicts checks the objects of a plan whose name has infix, each signed in domain.
func (ps *PlanStore) verdicts(ctx context.Context, id, digest string, readers []protect.Reader, infix, domain string, keyOf func(id, keyID string) string) ([]PlanVerdict, error) {
	keys, err := ps.objectsWith(ctx, id, infix)
	if err != nil || len(keys) == 0 {
		return nil, err
	}
	var out []PlanVerdict
	for _, k := range keys {
		data, err := ps.B.Get(ctx, k)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var obj PlanSignature
		if len(data) > maxSigSize || json.Unmarshal(data, &obj) != nil || obj.V != sigVersion || obj.ID != id || obj.Signature == nil ||
			k != keyOf(id, signatureID(obj.Signature.Key)) {
			// An object under the name of another key is not that key's signature.
			out = append(out, PlanVerdict{Status: protect.SigInvalid})
			continue
		}
		status, name := protect.VerifySignature(obj.Signature, readers, domain, ps.lineage(), planSigParts(digest, obj.At)...)
		out = append(out, PlanVerdict{Status: status, Signer: name, Claimed: obj.Signature.Signer, Key: obj.Signature.Key, At: obj.At})
	}
	return out, nil
}

func (ps *PlanStore) lineage() string {
	if ps.st == nil {
		return ""
	}
	return ps.st.Lineage()
}
