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
	"slices"
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
	if err := s.checkPolicyListings(ctx, readers); err != nil {
		return nil, err
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

// MissingPolicy is a policy version that a signed transaction lists as having judged its
// plan, and that no object of the state holds, or holds with other text.
type MissingPolicy struct {
	Name string
	Seq  int
	// Digest is the SHA-256 of the text the transaction lists.
	Digest string
	// Txn is the first transaction found that lists it.
	Txn string
	// Other is set when the state holds a version at that number with other text.
	Other bool
}

// Display is NAME@SEQ as the policy reads in the specification.
func (m MissingPolicy) Display() string { return fmt.Sprintf("%s@%d", PolicyDisplay(m.Name), m.Seq) }

// ErrPolicyMissing is the ErrUntrusted of a state that lacks a policy version a signed
// transaction lists.
func (m MissingPolicy) Error() string {
	what := "the state holds no object for it"
	if m.Other {
		what = "the state holds other text at that number"
	}
	return fmt.Sprintf("%v: transaction %s was judged by policy version %s (sha256:%s), and %s. A policy version is never deleted; an admin restores it with pravo protect policies --restore", ErrUntrusted, m.Txn, m.Display(), m.Digest[:min(12, len(m.Digest))], what)
}

// Unwrap makes a MissingPolicy an ErrUntrusted.
func (m MissingPolicy) Unwrap() error { return ErrUntrusted }

// txnFacts is what a transaction with a verified signature says about the objects of the
// state: the policy versions it judged by or wrote, and the newest version of each secret
// it set or removed.
type txnFacts struct {
	policies []string
	secrets  map[string]int
	// envRecord is the newest version of the record of the environment the transaction wrote.
	envRecord int
	// started and nodes are the start of the transaction and, for each resource address it
	// changed, the revision it left (0 when it removed the node), in the order of its changes.
	started time.Time
	nodes   []nodeChange
	// removing lists the addresses that a transaction with a signed start and no signed end
	// said it may remove: a run that is under way, or one that was killed. started is then
	// its start.
	removing []string
}

type nodeChange struct {
	addr  string
	after int
}

// signedFacts returns the facts of the transactions whose signatures verify against the
// readers, per transaction id. A finished transaction is immutable, so the ones read
// before are kept.
func (s *Store) signedFacts(ctx context.Context, readers []protect.Reader) (map[string]txnFacts, error) {
	key := readersKey(readers)
	s.listedMu.Lock()
	if s.facts != nil && s.factsKey == key && time.Since(s.factsAt) < factsFor {
		defer s.listedMu.Unlock()
		return s.facts, nil
	}
	s.listedMu.Unlock()
	return s.signedFactsNow(ctx, readers)
}

// factsFor is how long the facts of the log answer the checks of a command without another
// listing of it. A command checks the policies, the pointers and the secrets one after the
// other, and each check asks what the log vouches for. The checks read what they compare
// with the log after the log, so facts that are a moment old only ask for less. The one
// check that compares the log with something older, the pointers that the open read, asks
// again before it calls anything missing (see missingNodes). A transaction that this store
// writes ends the answer.
const factsFor = 2 * time.Second

// signedFactsNow is signedFacts from a listing of the log made now. The map is shared by
// the callers, who do not change it.
func (s *Store) signedFactsNow(ctx context.Context, readers []protect.Reader) (map[string]txnFacts, error) {
	at := time.Now()
	out, err := s.signedFactsOf(ctx, readers)
	if err != nil {
		return nil, err
	}
	s.listedMu.Lock()
	defer s.listedMu.Unlock()
	if !s.factsAt.After(at) {
		s.facts, s.factsAt, s.factsKey = out, at, readersKey(readers)
	}
	return out, nil
}

// forgetFacts ends the answer that signedFacts keeps, when this store writes a transaction.
func (s *Store) forgetFacts() {
	s.listedMu.Lock()
	defer s.listedMu.Unlock()
	s.facts, s.factsAt = nil, time.Now()
}

// readersKey tells one list of readers from another: the signing keys and the roles.
func readersKey(readers []protect.Reader) string {
	var b strings.Builder
	for _, r := range readers {
		b.Write(r.Sign)
		b.WriteByte(byte(r.Role))
	}
	return b.String()
}

// signedFactsOf is signedFacts for the transactions of the given kinds only, all of them when
// none is given. A transaction whose hint (see KindDir) names another kind is not read and is
// not in the result, so a caller that needs the facts of one kind does not pay for the plans,
// edits and secret sets of the state. A transaction without a hint is read.
func (s *Store) signedFactsOf(ctx context.Context, readers []protect.Reader, kinds ...string) (map[string]txnFacts, error) {
	keys, err := s.b.List(ctx, "txn")
	if err != nil {
		return nil, err
	}
	var hints map[string]string
	if len(kinds) > 0 {
		if hints, err = s.kindHints(readers); err != nil {
			return nil, err
		}
	}
	out := map[string]txnFacts{}
	var ids []string
	s.listedMu.Lock()
	for _, k := range keys {
		id, ok := strings.CutPrefix(k, "txn/")
		if !ok {
			continue
		}
		if id, ok = strings.CutSuffix(id, ".json"); !ok {
			continue
		}
		if k, ok := hints[id]; ok && !slices.Contains(kinds, k) {
			continue
		}
		if f, ok := s.listed[id]; ok {
			out[id] = f
			continue
		}
		ids = append(ids, id)
	}
	s.listedMu.Unlock()
	// The transactions that this process has not read are read together: a state holds one
	// for every command that ever changed it, and read one after another they would cost a
	// command a round trip each.
	type read struct {
		f        txnFacts
		ok, keep bool
	}
	got := make([]read, len(ids))
	lineage := s.Lineage()
	s.eachRead(len(ids), func(i int) {
		data, err := s.txnBytes(ctx, ids[i])
		if err != nil {
			return
		}
		var t Transaction
		if json.Unmarshal(data, &t) != nil || t.ID != ids[i] {
			return
		}
		got[i] = read{f: factsOf(&t, readers, lineage), ok: true, keep: t.Finished != nil && t.FinishSig != nil}
	})
	s.listedMu.Lock()
	defer s.listedMu.Unlock()
	if s.listed == nil {
		s.listed = map[string]txnFacts{}
	}
	for i, id := range ids {
		if !got[i].ok {
			continue
		}
		if got[i].keep {
			s.listed[id] = got[i].f
		}
		out[id] = got[i].f
	}
	return out, nil
}

// factsOf returns what the signatures of t vouch for.
func factsOf(t *Transaction, readers []protect.Reader, lineage string) txnFacts {
	var f txnFacts
	started := false
	if (len(t.Policies) > 0 || len(t.Removes) > 0) && t.StartSig != nil {
		st, _ := protect.VerifySignature(t.StartSig, readers, protect.DomainTxnStart, lineage, txnStartParts(t)...)
		started = st == protect.SigVerified
	}
	if started {
		f.policies = append(f.policies, t.Policies...)
	}
	finished := false
	if t.FinishSig != nil && t.Finished != nil {
		st, _ := protect.VerifySignature(t.FinishSig, readers, protect.DomainTxnFinish, lineage, txnFinishParts(t)...)
		finished = st == protect.SigVerified
	}
	if !finished {
		if started {
			// A run that has not finished, or was killed: what it said it may remove.
			f.removing, f.started = t.Removes, t.Started
		}
		return f
	}
	f.policies = append(f.policies, t.PolicyWrites...)
	f.started = t.Started
	for _, c := range t.Changes {
		if strings.HasPrefix(c.Addr, secretAddrPrefix) || c.Addr == EnvironmentAddr {
			continue
		}
		switch c.Action {
		case "unsettled", "deposed", "destroy":
			continue
		}
		after := c.After
		switch c.Action {
		case "delete", "forget", "move-from":
			after = 0 // a removal leaves no pointer, whatever revision it names
		}
		f.nodes = append(f.nodes, nodeChange{c.Addr, after})
	}
	if t.Kind == "environment" {
		for _, c := range t.Changes {
			if c.Addr == EnvironmentAddr && c.Action == "set" {
				f.envRecord = max(f.envRecord, c.After)
			}
		}
	}
	if t.Kind == "secret" {
		for _, c := range t.Changes {
			name, ok := strings.CutPrefix(c.Addr, secretAddrPrefix)
			if !ok || (c.Action != "set" && c.Action != "remove") {
				continue
			}
			if f.secrets == nil {
				f.secrets = map[string]int{}
			}
			f.secrets[name] = max(f.secrets[name], c.After)
		}
	}
	return f
}

// txnBytes reads the object of the transaction id. A transaction that is finished and signed
// is not written again in what its signatures cover, so its bytes are kept for the checks of
// this process that read it: the check of the records at the open and the facts of the log
// read the same transactions. What a rewrap or an unseal rewrites in a transaction, the
// sealed text of an error and of the diffs, is not signed and is not read from here.
func (s *Store) txnBytes(ctx context.Context, id string) ([]byte, error) {
	if data, ok := s.txnRead.Load(id); ok {
		return data.([]byte), nil
	}
	data, err := s.b.Get(ctx, "txn/"+id+".json")
	if err != nil {
		return nil, err
	}
	var t struct {
		Finished  *time.Time      `json:"finished"`
		FinishSig json.RawMessage `json:"finish_sig"`
	}
	if json.Unmarshal(data, &t) == nil && t.Finished != nil && len(t.FinishSig) > 0 {
		s.txnRead.Store(id, data)
	}
	return data, nil
}

// policyListings returns the policy versions that verified transactions list, per transaction id.
func (s *Store) policyListings(ctx context.Context, readers []protect.Reader) (map[string][]string, error) {
	facts, err := s.signedFacts(ctx, readers)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for id, f := range facts {
		if len(f.policies) > 0 {
			out[id] = f.policies
		}
	}
	return out, nil
}

// checkSecretsHeld refuses a state that lacks a version of a secret that a verified
// transaction set or removed: the newest versions were deleted from the bucket, and a read
// would hand out an older value. name limits the check to one secret, empty for all.
func (s *Store) checkSecretsHeld(ctx context.Context, readers []protect.Reader, name string, names map[string]int) error {
	if len(readers) == 0 {
		return nil
	}
	facts, err := s.signedFacts(ctx, readers)
	if err != nil {
		return err
	}
	newest := map[string]int{}
	for _, f := range facts {
		for n, seq := range f.secrets {
			newest[n] = max(newest[n], seq)
		}
	}
	for n, seq := range newest {
		if (name == "" || name == n) && seq > names[encodeAddr(n)] {
			return fmt.Errorf("%w: the transaction that wrote version %d of the secret %s is signed, and the bucket holds %d version(s): newer versions were deleted (pravo protect status)", ErrUntrusted, seq, n, names[encodeAddr(n)])
		}
	}
	return nil
}

// MissingPolicies lists the policy versions that a signed transaction lists and that
// the state no longer holds (or holds with other text). The ones a removal explains do
// not appear: a removal is a tombstone, a new version above the listed one, so the
// listed version stays. Only a deleted or replaced object makes one missing.
func (s *Store) MissingPolicies(ctx context.Context, readers []protect.Reader) ([]MissingPolicy, error) {
	if s.Format() < FormatPolicy || !s.sealedState() {
		return nil, nil
	}
	listings, err := s.policyListings(ctx, readers)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(listings))
	for id := range listings {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	seen := map[string]bool{}
	var out []MissingPolicy
	for _, id := range ids {
		for _, ref := range listings[id] {
			if seen[ref] {
				continue
			}
			seen[ref] = true
			name, seq, digest, ok := ParsePolicyRef(ref)
			if !ok {
				continue
			}
			m := MissingPolicy{Name: name, Seq: seq, Digest: digest, Txn: id}
			data, err := s.b.Get(ctx, policyKey(name, seq))
			if errors.Is(err, ErrNotFound) {
				out = append(out, m)
				continue
			}
			if err != nil {
				return nil, err
			}
			var v PolicyVersion
			if json.Unmarshal(data, &v) != nil || TextDigest(v.Text) != digest {
				m.Other = true
				out = append(out, m)
			}
		}
	}
	return out, nil
}

// checkPolicyListings refuses a state that lacks a policy version a signed transaction
// lists. Without it, deleting the only version of a policy from the bucket would lift the
// policy: the next plan that declares it again counts as a new policy, which no policy
// judges.
func (s *Store) checkPolicyListings(ctx context.Context, readers []protect.Reader) error {
	missing, err := s.MissingPolicies(ctx, readers)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	if len(missing) == 1 {
		return missing[0]
	}
	return fmt.Errorf("%w: %d policy versions that signed transactions list are gone from the state, among them %s (transaction %s). A policy version is never deleted; an admin restores them with pravo protect policies --restore", ErrUntrusted, len(missing), missing[0].Display(), missing[0].Txn)
}

// ParsePolicyRef splits NAME@SEQ:SHA-256.
func ParsePolicyRef(ref string) (name string, seq int, digest string, ok bool) {
	at := strings.IndexByte(ref, '@')
	colon := strings.IndexByte(ref, ':')
	if at <= 0 || colon < at {
		return "", 0, "", false
	}
	n, err := strconv.Atoi(ref[at+1 : colon])
	if err != nil || n < 1 {
		return "", 0, "", false
	}
	return ref[:at], n, ref[colon+1:], true
}

// RestorePolicyVersion writes a missing version back: text must have the digest the
// transactions list. The restoring key signs it, so it is the key of an admin when the
// readers give roles.
func (s *Store) RestorePolicyVersion(ctx context.Context, m MissingPolicy, text string, at time.Time) error {
	if TextDigest(text) != m.Digest {
		return fmt.Errorf("that text is not the one transaction %s lists for %s", m.Txn, m.Display())
	}
	if m.Other {
		key := policyKey(m.Name, m.Seq)
		if err := s.b.Delete(ctx, key); err != nil && !errors.Is(err, ErrNotFound) {
			return Advise(err)
		}
	}
	return s.PutPolicyVersion(ctx, m.Name, m.Seq, text, at)
}

// MissingNodes lists the resource addresses whose newest change in a verified, finished
// transaction left a revision, and whose pointer (resources/ADDRESS.json) the state does not
// hold: the pointer was deleted from the bucket, and the next plan would show the resource
// as new and plan a duplicate. A removal (state rm, a destroy, a move) is the newest change
// of its address and so explains an absent pointer.
func (s *Store) MissingNodes(ctx context.Context, readers []protect.Reader) ([]string, error) {
	return s.missingNodes(ctx, readers, false)
}

// missingNodes is MissingNodes; loaded takes the pointers the store read when it opened,
// without listing the bucket again.
func (s *Store) missingNodes(ctx context.Context, readers []protect.Reader, loaded bool) ([]string, error) {
	if len(readers) == 0 || !s.sealedState() {
		return nil, nil
	}
	if loaded {
		missing, err := s.missingAmong(ctx, readers, s.signedFacts, func() ([]string, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			keys := make([]string, 0, len(s.nodes))
			for addr := range s.nodes {
				keys = append(keys, nodeKey(addr))
			}
			return keys, nil
		})
		if err != nil || len(missing) == 0 {
			return missing, err
		}
		// The log was read after the pointers: a resource that another command created
		// since the open is in the log and not among them. The bucket is asked before
		// anything is called missing.
	}
	return s.missingAmong(ctx, readers, s.signedFactsNow, func() ([]string, error) {
		// The pointers the way a read finds them: from the versions of the bucket when it
		// keeps them, else by listing.
		if vl, _, on := s.snapshotOn(); on {
			objs, err := vl.ListVersions(ctx, "resources")
			if err != nil {
				return nil, err
			}
			keys := make([]string, len(objs))
			for i, o := range objs {
				keys[i] = o.Key
			}
			return keys, nil
		}
		return s.b.List(ctx, "resources")
	})
}

// missingAmong returns the addresses whose newest signed transaction left a record and whose
// pointer is not among the keys that pointers returns. The log is read first and the
// pointers after it, so that what the log names had been written when the pointers were
// read.
//
// A pointer that a command deletes is gone before the transaction of the command is
// finished: for as long as a destroy or a replacement takes, and for good when the command
// is killed. Such a transaction said, under its first signature, which pointers it may
// remove (Txn.WillRemove), and a pointer it named is not missing, unless a transaction that
// started after it left a record for the address.
func (s *Store) missingAmong(ctx context.Context, readers []protect.Reader, log func(context.Context, []protect.Reader) (map[string]txnFacts, error), pointers func() ([]string, error)) ([]string, error) {
	facts, err := log(ctx, readers)
	if err != nil {
		return nil, err
	}
	type last struct {
		at    time.Time
		after int
	}
	newest := map[string]last{}
	removing := map[string]time.Time{}
	for _, f := range facts {
		for _, c := range f.nodes {
			if cur, ok := newest[c.addr]; !ok || !f.started.Before(cur.at) {
				newest[c.addr] = last{f.started, c.after}
			}
		}
		for _, addr := range f.removing {
			if f.started.After(removing[addr]) {
				removing[addr] = f.started
			}
		}
	}
	keys, err := pointers()
	if err != nil {
		return nil, err
	}
	held := make(map[string]bool, len(keys))
	for _, k := range keys {
		held[k] = true
	}
	var out []string
	for addr, l := range newest {
		if l.after == 0 || held[nodeKey(addr)] {
			continue
		}
		if at, ok := removing[addr]; ok && !at.Before(l.at) {
			continue
		}
		out = append(out, addr)
	}
	sort.Strings(out)
	return out, nil
}

// CheckNodes refuses a state that lacks the pointer of a resource a signed transaction left.
func (s *Store) CheckNodes(ctx context.Context, readers []protect.Reader) error {
	return s.checkNodes(ctx, readers, false)
}

// CheckLoadedNodes is CheckNodes against the pointers the store read when it opened, for a
// command that only reads them: it asks the bucket for nothing more.
func (s *Store) CheckLoadedNodes(ctx context.Context, readers []protect.Reader) error {
	return s.checkNodes(ctx, readers, true)
}

func (s *Store) checkNodes(ctx context.Context, readers []protect.Reader, loaded bool) error {
	missing, err := s.missingNodes(ctx, readers, loaded)
	if err != nil || len(missing) == 0 {
		return err
	}
	list := missing[0]
	if len(missing) > 1 {
		list = fmt.Sprintf("%s and %d more", missing[0], len(missing)-1)
	}
	return fmt.Errorf("%w: the pointer of %s is gone from the bucket (resources/), and the newest signed transaction that changed it left a record: the resource would read as new and a duplicate would be planned. Restore the object from the bucket's history (pravo protect status lists them)", ErrUntrusted, list)
}

// Unnamed counts the resources whose pointers no signed transaction names, such as those
// of a state sealed before the baseline transaction existed: a pointer deleted from one of
// them cannot be found. It is 0 for a state that is not sealed, and when the readers needed
// to verify are not given.
func (s *Store) Unnamed(ctx context.Context, readers []protect.Reader) int {
	if len(readers) == 0 || !s.sealedState() || s.Locked() {
		return 0
	}
	facts, err := s.signedFacts(ctx, readers)
	if err != nil {
		return 0
	}
	named := map[string]bool{}
	for _, f := range facts {
		for _, c := range f.nodes {
			named[c.addr] = true
		}
	}
	n := 0
	for _, a := range s.Addresses() {
		if !named[a] && !strings.HasPrefix(a, secretAddrPrefix) && a != EnvironmentAddr {
			n++
		}
	}
	return n
}
