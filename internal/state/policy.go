package state

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
	"strconv"
	"strings"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// Policies. A policy is a series of immutable versions, one object each:
//
//	policy/<name>@<seq>.json   {"text":"…","at":"…","signature":{…}}
//
// <name> is the policy name as encodeAddr writes an address, so it never holds "@"
// or ":". <seq> is eight digits and counts from 1 without a gap. A version is created
// with PutIfAbsent and never replaced or deleted; a removal is a version with empty
// text, a tombstone. The policies in force are the latest version of each name that is
// not a tombstone. A version is signed by its writer with the domain
// protect.DomainPolicyVersion over the encoded name, the seq, the SHA-256 of the text
// and the time. The first object raises meta.json to FormatPolicy, which a pravo that
// does not know policies refuses.

const policyDir = "policy"

// ErrPolicyChanged is returned when a version of a policy could not be put because
// another writer put one at that number first.
var ErrPolicyChanged = errors.New("the policy changed since it was read")

var policyKeyRe = regexp.MustCompile(`^policy/([A-Za-z0-9._%-]+)@([0-9]{8})\.json$`)

// PolicyName is the name a policy has in the state: its address, encoded as an object
// name.
func PolicyName(name string) string { return encodeAddr(name) }

var policyEscapeRe = regexp.MustCompile(`%([0-9A-F]{4})`)

// PolicyDisplay is the name of a policy as its address reads: the state keeps it
// encoded as an object name.
func PolicyDisplay(name string) string {
	return policyEscapeRe.ReplaceAllStringFunc(name, func(s string) string {
		n, err := strconv.ParseInt(s[1:], 16, 32)
		if err != nil {
			return s
		}
		return string(rune(n))
	})
}

func policyKey(name string, seq int) string {
	return fmt.Sprintf("%s/%s@%08d.json", policyDir, name, seq)
}

// PolicyVersion is one version of a policy.
type PolicyVersion struct {
	// Name and Seq come from the name of the object.
	Name      string             `json:"-"`
	Seq       int                `json:"-"`
	Text      string             `json:"text"`
	At        time.Time          `json:"at"`
	Signature *protect.Signature `json:"signature,omitempty"`
	// Status says what the readers file makes of the signature, and Signer is the name
	// it gives the key. They are set by what reads the version.
	Status protect.SigStatus `json:"-"`
	Signer string            `json:"-"`
	// RoleShort is set when the readers file gives roles and the key that signed the
	// version has a lesser role than the version needs today: a writer for a version, an
	// admin for a tombstone. It is shown and does not fail the read: a key whose role was
	// lowered since it signed looks the same as one that never had the role.
	RoleShort bool `json:"-"`
}

// RoleNeeded is the role a key must have to sign the version: a writer adds and edits a
// policy, and only an admin removes it.
func (v PolicyVersion) RoleNeeded() protect.Role {
	if v.Tombstone() {
		return protect.RoleAdmin
	}
	return protect.RoleWriter
}

// Tombstone reports whether the version removes the policy.
func (v PolicyVersion) Tombstone() bool { return v.Text == "" }

// Digest is the SHA-256 of the text in hex.
func (v PolicyVersion) Digest() string { return TextDigest(v.Text) }

// Ref names the version and its text: NAME@SEQ:SHA-256, as a receipt lists it.
func (v PolicyVersion) Ref() string { return fmt.Sprintf("%s@%d:%s", v.Name, v.Seq, v.Digest()) }

// TextDigest is the SHA-256 of a policy text in hex.
func TextDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func policyParts(name string, seq int, text string, at time.Time) [][]byte {
	return [][]byte{[]byte(name), []byte(strconv.Itoa(seq)), []byte(TextDigest(text)), []byte(at.UTC().Format(time.RFC3339Nano))}
}

// PolicySet is the policies of a state: the latest version of each name.
type PolicySet struct {
	heads map[string]PolicyVersion
}

// Head returns the latest version of the policy, a tombstone included.
func (p *PolicySet) Head(name string) (PolicyVersion, bool) {
	v, ok := p.heads[name]
	return v, ok
}

// InForce lists the policies in force, by name.
func (p *PolicySet) InForce() []PolicyVersion {
	var out []PolicyVersion
	for _, v := range p.heads {
		if !v.Tombstone() {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Names lists every name the state holds a version of, by name.
func (p *PolicySet) Names() []string {
	out := make([]string, 0, len(p.heads))
	for n := range p.heads {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// PolicySet reads the policies of the state and checks the head of each against the
// readers. A state without policies, which is any below FormatPolicy, has none. A
// policy state that is not sealed cannot be trusted and is ErrNotReader; a gap in the
// seqs, an object that is not a version, a missing signature or a key of the readers
// file whose signature does not verify is ErrUntrusted. A signature by a key that is not
// in the file is accepted and shows as protect.SigUnknownKey.
func (s *Store) PolicySet(ctx context.Context, readers []protect.Reader) (*PolicySet, error) {
	set := &PolicySet{heads: map[string]PolicyVersion{}}
	if s.Format() < FormatPolicy {
		return set, nil
	}
	if !s.sealedState() {
		return nil, fmt.Errorf("%w: the policies of an unsealed state cannot be trusted", ErrNotReader)
	}
	keys, err := s.b.List(ctx, policyDir)
	if err != nil {
		return nil, err
	}
	seqs := map[string][]int{}
	for _, k := range keys {
		m := policyKeyRe.FindStringSubmatch(k)
		if m == nil {
			return nil, fmt.Errorf("%w: %s is not a policy version", ErrUntrusted, k)
		}
		n, _ := strconv.Atoi(m[2])
		seqs[m[1]] = append(seqs[m[1]], n)
	}
	for name, list := range seqs {
		sort.Ints(list)
		for i, n := range list {
			if n != i+1 {
				return nil, fmt.Errorf("%w: policy %s has no version %d", ErrUntrusted, name, i+1)
			}
		}
		v, err := s.PolicyVersion(ctx, readers, name, len(list))
		if err != nil {
			return nil, err
		}
		set.heads[name] = v
	}
	return set, nil
}

// PolicyHistory reads every version of every policy, oldest first for each name, and
// checks each signature against the readers, with the same refusals as PolicySet. A
// state without policies has none.
func (s *Store) PolicyHistory(ctx context.Context, readers []protect.Reader) (map[string][]PolicyVersion, error) {
	out := map[string][]PolicyVersion{}
	if s.Format() < FormatPolicy {
		return out, nil
	}
	if !s.sealedState() {
		return nil, fmt.Errorf("%w: the policies of an unsealed state cannot be trusted", ErrNotReader)
	}
	keys, err := s.b.List(ctx, policyDir)
	if err != nil {
		return nil, err
	}
	count := map[string]int{}
	for _, k := range keys {
		m := policyKeyRe.FindStringSubmatch(k)
		if m == nil {
			return nil, fmt.Errorf("%w: %s is not a policy version", ErrUntrusted, k)
		}
		count[m[1]]++
	}
	for name, n := range count {
		for seq := 1; seq <= n; seq++ {
			v, err := s.PolicyVersion(ctx, readers, name, seq)
			if err != nil {
				return nil, err
			}
			out[name] = append(out[name], v)
		}
	}
	return out, nil
}

// PolicyVersion reads one version and checks its signature against the readers.
func (s *Store) PolicyVersion(ctx context.Context, readers []protect.Reader, name string, seq int) (PolicyVersion, error) {
	key := policyKey(name, seq)
	data, err := s.b.Get(ctx, key)
	if err != nil {
		return PolicyVersion{}, fmt.Errorf("%s: %w", key, err)
	}
	v := PolicyVersion{Name: name, Seq: seq}
	if err := json.Unmarshal(data, &v); err != nil {
		return PolicyVersion{}, fmt.Errorf("%w: %s: %v", ErrUntrusted, key, err)
	}
	v.Status, v.Signer = protect.VerifySignature(v.Signature, readers, protect.DomainPolicyVersion, s.Lineage(), policyParts(name, seq, v.Text, v.At)...)
	switch v.Status {
	case protect.SigUnsigned:
		return PolicyVersion{}, fmt.Errorf("%w: %s has no signature", ErrUntrusted, key)
	case protect.SigInvalid:
		return PolicyVersion{}, fmt.Errorf("%w: the signature of %s does not verify", ErrUntrusted, key)
	}
	if v.Status == protect.SigVerified && protect.RolesOn(readers) {
		role, _ := protect.RoleOf(readers, v.Signature.Key)
		v.RoleShort = !role.Allows(v.RoleNeeded())
	}
	return v, nil
}

// PutPolicyVersion signs a version and creates it at seq, the number after the head the
// caller read. The first version raises the format of the state. A version at that number
// that holds the same text is the caller's own earlier attempt and is no error; any other
// is ErrPolicyChanged. An empty text writes a tombstone.
func (s *Store) PutPolicyVersion(ctx context.Context, name string, seq int, text string, at time.Time) error {
	at = at.UTC()
	sig, ok, err := s.SignMessage(protect.DomainPolicyVersion, policyParts(name, seq, text, at)...)
	if err != nil {
		return fmt.Errorf("signing policy %s: %w", name, err)
	}
	if !ok {
		return fmt.Errorf("%w: a policy version is signed and the state is not sealed", ErrNotReader)
	}
	data, err := indentJSON(PolicyVersion{Text: text, At: at, Signature: &sig})
	if err != nil {
		return err
	}
	if err := s.setFormat(FormatPolicy); err != nil {
		return err
	}
	key := policyKey(name, seq)
	created, err := s.b.PutIfAbsent(ctx, key, data)
	if err != nil {
		return Advise(err)
	}
	if created {
		return nil
	}
	cur, err := s.b.Get(ctx, key)
	if err != nil {
		return err
	}
	var old PolicyVersion
	if json.Unmarshal(cur, &old) == nil && old.Text == text && old.Signature != nil && old.Signature.Key == sig.Key {
		return nil
	}
	if bytes.Equal(cur, data) {
		return nil
	}
	return fmt.Errorf("%w: %s exists", ErrPolicyChanged, key)
}

// PolicyList joins refs as the part of the start of a transaction that lists the
// policies it judged.
func policyList(refs []string) []byte { return []byte(strings.Join(refs, "\n")) }
