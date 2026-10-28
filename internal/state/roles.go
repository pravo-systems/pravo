package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// Roles in the state. The roles of the keys are kept in the state as a chain of immutable
// versions, one object each:
//
//	roles/<seq>.json   {"text":"…","at":"…","signature":{…}}
//
// <seq> is eight digits and counts from 1 without a gap. The text of a version is the
// roles of all keys, as protect.FormatRolesSet writes them: the name, the signing key
// and the role of each. A version is created with PutIfAbsent and never replaced.
//
// A key that leaves is kept in the next version as "NAME SHA256:FINGERPRINT role:former", and
// so is a key that signed a transaction before the roles were stored and that no one lists.
// The transactions of a key that is demoted, dropped or found unlisted that account for live
// records are vouched for in the same version, "@vouch FINGERPRINT TXN DIGEST": those stay
// valid, and nothing else that the key signs does (see Store.VerifyRecordsWith). Each
// version holds only the vouches it adds.
//
// The first version is pinned by the readers file of the repository ("roles-genesis" and
// the SHA-256 of its text), which a writer of the bucket cannot change; it is signed by a
// key that is an admin in its own text. Every later version is signed by a key that is an
// admin in the version before it. So a writer of the bucket who holds no admin key cannot
// add a version, replace the first, or make a role of their own: they can delete the
// newest versions, which an object lock on roles/ stops, and the chain they leave is
// the older one. A state whose pin finds no chain is refused.
//
// The roles of the readers file are what an admin asks for. Only the chain decides.

const rolesDir = "roles"

var rolesKeyRe = regexp.MustCompile(`^roles/([0-9]{8})\.json$`)

// ErrRolesChanged is returned when a version of the roles could not be put because another
// writer put one at that number first.
var ErrRolesChanged = errors.New("the roles changed since they were read")

func rolesKey(seq int) string { return fmt.Sprintf("%s/%08d.json", rolesDir, seq) }

// RolesVersion is one version of the roles.
type RolesVersion struct {
	Seq       int                `json:"-"`
	Text      string             `json:"text"`
	At        time.Time          `json:"at"`
	Signature *protect.Signature `json:"signature,omitempty"`
	// Set is the active keys of Text, with their roles; Former is the keys that left, and
	// Vouched the transactions it vouches for. All are set by what reads the version.
	Set     []protect.Reader    `json:"-"`
	Former  []protect.FormerKey `json:"-"`
	Vouched []protect.Vouch     `json:"-"`
}

// Digest is the SHA-256 of the text in hex, which the readers file pins for the first.
func (v RolesVersion) Digest() string { return TextDigest(v.Text) }

func rolesParts(seq int, text string, at time.Time) [][]byte {
	return [][]byte{[]byte(strconv.Itoa(seq)), []byte(TextDigest(text)), []byte(at.UTC().Format(time.RFC3339Nano))}
}

// RolesChain is the verified versions of the roles of a state.
type RolesChain struct {
	Versions []RolesVersion
}

// Head is the newest version, nil for an empty chain.
func (c *RolesChain) Head() *RolesVersion {
	if c == nil || len(c.Versions) == 0 {
		return nil
	}
	return &c.Versions[len(c.Versions)-1]
}

// RolesChain reads the chain of roles of the state and checks it: no gap, every text
// parses, the first is signed by an admin of its own text, and every later one by an admin of
// the version before it. It does not look at the pin; RolesFor does. An empty chain is no
// error. A state that is not sealed has none.
func (s *Store) RolesChain(ctx context.Context) (*RolesChain, error) {
	chain := &RolesChain{}
	if !s.sealedState() {
		return chain, nil
	}
	keys, err := s.b.List(ctx, rolesDir)
	if err != nil {
		return nil, err
	}
	var seqs []int
	for _, k := range keys {
		m := rolesKeyRe.FindStringSubmatch(k)
		if m == nil {
			return nil, fmt.Errorf("%w: %s is not a version of the roles", ErrUntrusted, k)
		}
		n, _ := strconv.Atoi(m[1])
		seqs = append(seqs, n)
	}
	sort.Ints(seqs)
	for i, n := range seqs {
		if n != i+1 {
			return nil, fmt.Errorf("%w: the roles have no version %d", ErrUntrusted, i+1)
		}
		data, err := s.b.Get(ctx, rolesKey(n))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rolesKey(n), err)
		}
		v := RolesVersion{Seq: n}
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrUntrusted, rolesKey(n), err)
		}
		if v.Set, v.Former, v.Vouched, err = protect.ParseRolesSet(v.Text); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrUntrusted, rolesKey(n), err)
		}
		signers := v.Set
		if n > 1 {
			signers = chain.Versions[n-2].Set
		}
		if v.Signature == nil {
			return nil, fmt.Errorf("%w: %s has no signature", ErrUntrusted, rolesKey(n))
		}
		status, _ := protect.VerifySignature(v.Signature, signers, protect.DomainRolesVersion, s.Lineage(), rolesParts(n, v.Text, v.At)...)
		if status != protect.SigVerified {
			return nil, fmt.Errorf("%w: %s is not signed by an admin of the roles it follows", ErrUntrusted, rolesKey(n))
		}
		if role, _ := protect.RoleOf(append([]protect.Reader(nil), signers...), v.Signature.Key); role != protect.RoleAdmin {
			return nil, fmt.Errorf("%w: %s is signed by a key that is not an admin of the roles it follows", ErrUntrusted, rolesKey(n))
		}
		chain.Versions = append(chain.Versions, v)
	}
	return chain, nil
}

// RolesStatus says where the roles of a state come from.
type RolesStatus struct {
	// Source is "file" when the readers file decides, because it gives roles and pins
	// no chain, "state" when the chain decides, and "off" when there are no roles.
	Source string
	// Version is the number of the head of the chain, 0 without one.
	Version int
	// Unpinned is set when the state holds a chain that the readers file does not pin: the
	// file decides until its pin is committed.
	Unpinned bool
	// Differs is set when the roles of the readers file are not the head of the chain.
	Differs bool
	// Former are the former keys of the head of the chain. Set with a chain.
	Former []protect.FormerKey
	// Vouched are the transactions that admins vouched for, by transaction id, in every
	// version of the chain. Set when the chain decides.
	Vouched map[string]protect.Vouch
}

// RolesFor gives the readers the roles that decide for this state. file are the readers of the
// repository, as parsed, and pin is the digest its roles-genesis line holds, empty without the
// line. With a pin, the chain decides, and the roles of the file only say what an admin asks
// for; a pin without a chain, a chain that does not begin with the pin, or a chain that does
// not verify is ErrUntrusted. Without a pin the readers file decides, and a chain is
// reported and not used.
func (s *Store) RolesFor(ctx context.Context, file []protect.Reader, pin string) ([]protect.Reader, RolesStatus, error) {
	st := RolesStatus{Source: "off"}
	if protect.RolesOn(file) {
		st.Source = "file"
	}
	if !s.sealedState() {
		return file, st, nil
	}
	chain, err := s.RolesChain(ctx)
	if err != nil {
		if pin == "" {
			return file, st, nil // nobody relies on it; status says so
		}
		return nil, st, err
	}
	head := chain.Head()
	if head != nil {
		st.Version = head.Seq
	}
	switch {
	case pin == "" && head == nil:
		return file, st, nil
	case pin == "":
		st.Unpinned = true
		return file, st, nil
	case head == nil:
		return nil, st, fmt.Errorf("%w: the readers file pins the roles of the state, and the state has none", ErrUntrusted)
	case chain.Versions[0].Digest() != pin:
		return nil, st, fmt.Errorf("%w: the first roles in the state are not the ones the readers file pins", ErrUntrusted)
	}
	st.Source = "state"
	st.Former = head.Former
	st.Vouched = map[string]protect.Vouch{}
	for _, v := range chain.Versions {
		for _, vo := range v.Vouched {
			st.Vouched[vo.Txn] = vo
		}
	}
	// The roles in the state decide for the keys in the readers file. A key that the file
	// dropped keeps its role until an admin stores a version without it, since only that
	// version is signed by an admin: it is in force too.
	out := make([]protect.Reader, len(file), len(file)+len(head.Set))
	copy(out, file)
	for i := range out {
		out[i].Role = protect.RoleReader
		for _, r := range head.Set {
			if protect.SSHFingerprint(r.Sign) == protect.SSHFingerprint(out[i].Sign) {
				out[i].Role = r.Role
			}
		}
	}
	for _, r := range head.Set {
		if !listedIn(file, r.Sign) {
			out = append(out, protect.Reader{Name: r.Name, Sign: r.Sign, Role: r.Role})
		}
	}
	want, wantFormer := protect.NextRoles(file, head.Set, head.Former, nil)
	st.Differs = protect.FormatRolesSet(want, wantFormer, nil) != protect.FormatRolesSet(head.Set, head.Former, nil)
	return out, st, nil
}

func listedIn(rs []protect.Reader, sign []byte) bool {
	fp := protect.SSHFingerprint(sign)
	for _, r := range rs {
		if protect.SSHFingerprint(r.Sign) == fp {
			return true
		}
	}
	return false
}

// PutRolesVersion signs a version of the roles and creates it at seq, the number after the head
// the caller read. A version at that number with the same text is the caller's own earlier
// attempt and is no error; any other is ErrRolesChanged.
func (s *Store) PutRolesVersion(ctx context.Context, seq int, text string, at time.Time) error {
	at = at.UTC()
	sig, ok, err := s.SignMessage(protect.DomainRolesVersion, rolesParts(seq, text, at)...)
	if err != nil {
		return fmt.Errorf("signing the roles: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: the roles are signed and the state is not sealed", ErrNotReader)
	}
	data, err := indentJSON(RolesVersion{Text: text, At: at, Signature: &sig})
	if err != nil {
		return err
	}
	created, err := s.b.PutIfAbsent(ctx, rolesKey(seq), data)
	if err != nil {
		return Advise(err)
	}
	if created {
		return nil
	}
	cur, err := s.b.Get(ctx, rolesKey(seq))
	if err != nil {
		return err
	}
	var old RolesVersion
	if bytes.Equal(cur, data) || json.Unmarshal(cur, &old) == nil && old.Text == text && old.Signature != nil && old.Signature.Key == sig.Key {
		return nil
	}
	return fmt.Errorf("%w: %s exists", ErrRolesChanged, rolesKey(seq))
}

// VouchDigest is what an admin vouches for when the transaction t stays valid: the SHA-256 of
// everything its finish signature covers, the lineage, the signature and the key. The text
// of an error and the diffs, which a rewrap rewrites, are not in it. It is empty for a
// transaction with no finish signature.
func VouchDigest(lineage string, t *Transaction) string {
	if t.FinishSig == nil {
		return ""
	}
	h := sha256.New()
	put := func(b []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	put([]byte(lineage))
	put([]byte(t.FinishSig.Key))
	put(t.FinishSig.Sig)
	for _, p := range txnFinishParts(t) {
		put(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// LiveSigners lists, for each finished and signed transaction that is the latest writer of a
// record in the state, the key that signed it and its VouchDigest. It reads each such
// transaction once. A transaction that is gone, or has no finish signature, is left out.
func (s *Store) LiveSigners(ctx context.Context) ([]protect.Vouch, error) {
	s.mu.Lock()
	ids := map[string]bool{}
	for addr, r := range s.nodes {
		if s.held[addr].Record != "" && r.Txn != "" {
			ids[r.Txn] = true
		}
	}
	s.mu.Unlock()
	// The transactions that wrote or purged a version of a secret count too: a key that is
	// demoted or dropped keeps the versions an admin vouched for.
	secretIDs, err := s.secretTxnIDsAll(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range secretIDs {
		ids[id] = true
	}
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	sort.Strings(list)
	out := make([]protect.Vouch, len(list))
	var mu sync.Mutex
	var firstErr error
	s.each(len(list), func(i int) {
		data, err := s.b.Get(ctx, "txn/"+list[i]+".json")
		if errors.Is(err, ErrNotFound) {
			return
		}
		var t Transaction
		if err == nil {
			err = json.Unmarshal(data, &t)
		}
		if err != nil {
			mu.Lock()
			firstErr = errors.Join(firstErr, fmt.Errorf("txn/%s.json: %w", list[i], err))
			mu.Unlock()
			return
		}
		if t.ID != list[i] || t.FinishSig == nil || t.Finished == nil {
			return
		}
		out[i] = protect.Vouch{Key: t.FinishSig.Key, Txn: t.ID, Digest: VouchDigest(s.Lineage(), &t)}
	})
	if firstErr != nil {
		return nil, firstErr
	}
	kept := out[:0]
	for _, v := range out {
		if v.Txn != "" {
			kept = append(kept, v)
		}
	}
	return kept, nil
}
