package state

import (
	"context"
	"crypto/hmac"
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

// Secrets. A secret is a series of immutable versions, one object each:
//
//	secret/<name>@<seq>.json
//
// <name> is the name of the secret as encodeAddr writes an address, so it never holds
// "@" or "/". <seq> is eight digits and counts from 1 without a gap. A version is created
// with PutIfAbsent. It has one of three shapes:
//
//	a value    {"v":1,"at","txn","commit","sealed":{…}}
//	a removal  {"v":1,"at","txn","commit","removed":true}
//	a purge    {"v":1,"at","txn","commit","purged":true,"purged_by":TXN}
//
// A value holds the secret in its sealed part, {value, nonce, note}, sealed to the
// readers of the state in the group that was current when it was written. A removal ends
// the secret: a later version continues the numbering. A purge is a value from which the
// sealed part was dropped, in place, so that the numbering has no gap and a deleted version
// is always noticed.
//
// Nothing here is signed in the object. A secret command is a transaction (kind "secret")
// whose change for secret:NAME names the version and the commit of the version, and the
// finish signature of the transaction covers it, as it covers the key of a record. The
// commit is an HMAC, under a nonce that sits in the sealed part, of the value, the note, the
// name, the number and the lineage: it hides a weak value from whoever reads the
// transactions, it ties the transaction to the version, and a rewrap, which writes the
// ciphertext again, leaves it as it is. Reading a version checks the object against its
// transaction, the transaction against the roles, and the opened value against the commit.
//
// The first version of any secret raises meta.json to FormatSecret, which a pravo that does
// not know secrets refuses.

const secretDir = "secret"

const (
	// MaxSecretValue is the most bytes a secret holds.
	MaxSecretValue = 1 << 20
	// MaxSecretNote is the most bytes the note of a version holds.
	MaxSecretNote = 1000

	maxSecretName     = 200
	secretFileVersion = 1
	secretAddrPrefix  = "secret:"

	domainCommit  = "pravo/v1/secret-commit"
	domainRemoval = "pravo/v1/secret-removed"
)

var (
	// ErrNoSecret is returned for a name that the state holds no secret of.
	ErrNoSecret = errors.New("there is no such secret")
	// ErrSecretRemoved is returned for a secret whose latest version is a removal.
	ErrSecretRemoved = errors.New("the secret was removed")
	// ErrSecretPurged is returned for a version whose value was purged.
	ErrSecretPurged = errors.New("the value of this version was purged")
	// ErrSecretChanged is returned when a version could not be put because another writer
	// put one at that number first.
	ErrSecretChanged = errors.New("the secret changed since it was read")
	// ErrSecretsNeedSeal is returned for a secret in a state that is not sealed.
	ErrSecretsNeedSeal = errors.New("secrets are kept in a sealed state")
	// ErrSecretsHeld is returned by Unseal for a state that holds the value of a secret.
	ErrSecretsHeld = errors.New("the state holds secret values")
)

var (
	secretKeyRe  = regexp.MustCompile(`^secret/([A-Za-z0-9._%-]+)@([0-9]{8})\.json$`)
	secretNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)*$`)
	txnIDRe      = regexp.MustCompile(`^[0-9A-Za-z-]{1,64}$`)
)

// ValidSecretName says why name is not the name of a secret, nil when it is: words of
// letters, digits, dots, dashes and underscores, joined by slashes, such as prod/db_password.
func ValidSecretName(name string) error {
	switch {
	case name == "":
		return errors.New("a secret needs a name")
	case len(name) > maxSecretName:
		return fmt.Errorf("the name of a secret has at most %d bytes", maxSecretName)
	case !secretNameRe.MatchString(name):
		return fmt.Errorf("%q is not the name of a secret: use letters, digits, dots, dashes and underscores, with a slash between parts, such as prod/db_password", name)
	}
	return nil
}

// SecretAddr is the address that the changes of a transaction give a secret.
func SecretAddr(name string) string { return secretAddrPrefix + name }

func secretKey(enc string, seq int) string {
	return fmt.Sprintf("%s/%s@%08d.json", secretDir, enc, seq)
}

// SecretDisplay is the name of a secret as the user wrote it: the state keeps it encoded as
// an object name.
func SecretDisplay(enc string) string { return PolicyDisplay(enc) }

// secretFile is a version as it is stored.
type secretFile struct {
	V        int             `json:"v"`
	At       time.Time       `json:"at"`
	Txn      string          `json:"txn"`
	Commit   string          `json:"commit"`
	Removed  bool            `json:"removed,omitempty"`
	Purged   bool            `json:"purged,omitempty"`
	PurgedBy string          `json:"purged_by,omitempty"`
	Sealed   *protect.Sealed `json:"sealed,omitempty"`
}

// The kinds of a version.
const (
	SecretKindValue   = "value"
	SecretKindRemoved = "removed"
	SecretKindPurged  = "purged"
)

// kind is the shape of the version, or an error when it is none of the three.
func (f secretFile) kind() (string, error) {
	switch {
	case f.Removed && !f.Purged && f.PurgedBy == "" && f.Sealed == nil:
		return SecretKindRemoved, nil
	case f.Purged && !f.Removed && f.PurgedBy != "" && f.Sealed == nil:
		return SecretKindPurged, nil
	case !f.Removed && !f.Purged && f.PurgedBy == "" && f.Sealed != nil:
		return SecretKindValue, nil
	}
	return "", errors.New("it is not a value, a removal or a purge")
}

// secretBody is what the sealed part of a value holds.
type secretBody struct {
	Value []byte `json:"value"`
	Nonce []byte `json:"nonce"`
	Note  string `json:"note,omitempty"`
}

func secretCtx(lineage, name string, seq int) protect.Ctx {
	return protect.Ctx{Lineage: lineage, Kind: protect.KindSecret, Name: name, Revision: uint64(seq)}
}

func seqPart(seq int) []byte { return []byte(strconv.Itoa(seq)) }

// secretCommit is the commit of a value: see the comment at the top of this file.
func secretCommit(lineage, name string, seq int, nonce, value []byte, note string) string {
	m := hmac.New(sha256.New, nonce)
	m.Write(protect.SigningMessage(domainCommit, lineage, []byte(name), seqPart(seq), value, []byte(note)))
	return hex.EncodeToString(m.Sum(nil))
}

// removalCommit is the commit of a removal, which has no value to hide.
func removalCommit(lineage, name string, seq int) string {
	sum := sha256.Sum256(protect.SigningMessage(domainRemoval, lineage, []byte(name), seqPart(seq)))
	return hex.EncodeToString(sum[:])
}

// SecretTrust is what the transactions make of a version.
type SecretTrust string

const (
	// TrustOK means a finished transaction of a key that may do it names the commit.
	TrustOK SecretTrust = "ok"
	// TrustVouched is TrustOK for a key that is not a writer now and that an admin vouched for.
	TrustVouched SecretTrust = "vouched"
	// TrustUnchecked means the transaction was signed by a key that is not in the readers file,
	// and the rules are not strict: it could be a member who left, or a key someone made.
	TrustUnchecked SecretTrust = "unchecked"
	// TrustUnconfirmed means the transaction that wrote the version did not finish.
	TrustUnconfirmed SecretTrust = "unconfirmed"
	// TrustProblem means no transaction of a key that may do it accounts for the version.
	TrustProblem SecretTrust = "problem"
)

// SecretInfo is one version of a secret as listings show it. Nothing in it is secret.
type SecretInfo struct {
	Name string `json:"name"`
	Seq  int    `json:"version"`
	// Kind is SecretKindValue, SecretKindRemoved or SecretKindPurged.
	Kind string `json:"kind"`
	// At is when the transaction that wrote the version started, as its signature has it, or
	// the time in the object when the transaction cannot be found.
	At       time.Time `json:"at"`
	Txn      string    `json:"txn,omitempty"`
	PurgedBy string    `json:"purged_by,omitempty"`
	Commit   string    `json:"commit"`
	// By is the name the readers file gives the key that signed the transaction, empty unless a
	// signature verified; Claimed is the name the signature gives itself, a label and not a proof.
	By      string `json:"by,omitempty"`
	Claimed string `json:"claimed,omitempty"`
	// Key is the fingerprint of the signing key of the transaction.
	Key     string      `json:"key,omitempty"`
	Trust   SecretTrust `json:"trust"`
	Problem string      `json:"problem,omitempty"`
}

// Signer is the name to show for who wrote the version.
func (i SecretInfo) Signer() string {
	if i.By != "" {
		return i.By
	}
	return i.Claimed
}

// SecretSummary is a secret in a listing.
type SecretSummary struct {
	Name     string `json:"name"`
	Versions int    `json:"versions"`
	// Head is the latest version, a removal included.
	Head SecretInfo `json:"head"`
}

// Live reports whether the secret has a value to read: its latest version is a value.
func (s SecretSummary) Live() bool { return s.Head.Kind == SecretKindValue }

// SecretValue is a version with its value, opened. The caller clears Value when it is done.
type SecretValue struct {
	SecretInfo
	Value []byte
	Note  string
}

// txnLookup finds a transaction by id, nil when there is none.
type txnLookup func(id string) *Transaction

// loadTxns reads the transactions with the ids from the bucket, once each.
func (s *Store) loadTxns(ctx context.Context, ids []string) txnLookup {
	uniq := map[string]bool{}
	var list []string
	for _, id := range ids {
		if id != "" && txnIDRe.MatchString(id) && !uniq[id] {
			uniq[id] = true
			list = append(list, id)
		}
	}
	sort.Strings(list)
	got := make([]*Transaction, len(list))
	s.each(len(list), func(i int) {
		data, err := s.b.Get(ctx, "txn/"+list[i]+".json")
		if err != nil {
			return
		}
		var t Transaction
		if json.Unmarshal(data, &t) == nil && t.ID == list[i] {
			got[i] = &t
		}
	})
	byID := make(map[string]*Transaction, len(list))
	for i, id := range list {
		byID[id] = got[i]
	}
	return func(id string) *Transaction { return byID[id] }
}

// secretNames lists the secrets of the state: the encoded name and the number of versions of
// each. The numbers of a name run from 1 without a gap, and an object that is not a version
// of a secret is ErrUntrusted.
func (s *Store) secretNames(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	keys, err := s.b.List(ctx, secretDir)
	if err != nil {
		return nil, err
	}
	seqs := map[string][]int{}
	for _, k := range keys {
		m := secretKeyRe.FindStringSubmatch(k)
		if m == nil {
			return nil, fmt.Errorf("%w: %s is not a version of a secret", ErrUntrusted, k)
		}
		n, _ := strconv.Atoi(m[2])
		seqs[m[1]] = append(seqs[m[1]], n)
	}
	for enc, list := range seqs {
		sort.Ints(list)
		for i, n := range list {
			if n != i+1 {
				return nil, fmt.Errorf("%w: the secret %s has no version %d", ErrUntrusted, SecretDisplay(enc), i+1)
			}
		}
		out[enc] = len(list)
	}
	return out, nil
}

// readSecretFile reads one version.
func (s *Store) readSecretFile(ctx context.Context, enc string, seq int) (secretFile, error) {
	key := secretKey(enc, seq)
	data, err := s.b.Get(ctx, key)
	if err != nil {
		return secretFile{}, fmt.Errorf("%s: %w", key, err)
	}
	return parseSecretFile(key, data)
}

func parseSecretFile(key string, data []byte) (secretFile, error) {
	var f secretFile
	if err := json.Unmarshal(data, &f); err != nil {
		return secretFile{}, fmt.Errorf("%w: %s: %v", ErrUntrusted, key, err)
	}
	if f.V != secretFileVersion {
		return secretFile{}, fmt.Errorf("%s is a secret of version %d; this pravo reads version %d", key, f.V, secretFileVersion)
	}
	if _, err := f.kind(); err != nil {
		return secretFile{}, fmt.Errorf("%w: %s: %v", ErrUntrusted, key, err)
	}
	if f.Txn == "" || f.Commit == "" {
		return secretFile{}, fmt.Errorf("%w: %s names no transaction or no commit", ErrUntrusted, key)
	}
	return f, nil
}

func secretInfoOf(name string, seq int, f secretFile) SecretInfo {
	kind, _ := f.kind()
	return SecretInfo{Name: name, Seq: seq, Kind: kind, At: f.At, Txn: f.Txn, PurgedBy: f.PurgedBy, Commit: f.Commit, Trust: TrustProblem}
}

// secretClaim is the change that a transaction must hold for a version.
type secretClaim struct {
	addr, action string
	// seq is the version; a change is found by its After, or by its Before for a purge.
	seq    int
	commit string
}

func (c secretClaim) find(t *Transaction) (why string) {
	var other *Change
	for i := range t.Changes {
		ch := &t.Changes[i]
		if ch.Addr != c.addr {
			continue
		}
		mine := ch.After == c.seq
		if c.action == "purge" {
			mine = ch.Before == c.seq && ch.After == 0
		}
		if !mine {
			continue
		}
		if ch.Action == c.action && ch.Record == c.commit {
			return ""
		}
		other = ch
	}
	if other == nil {
		return fmt.Sprintf("the transaction %s does not name version %d", t.ID, c.seq)
	}
	return fmt.Sprintf("the transaction %s signed another commit for version %d", t.ID, c.seq)
}

// secretTxn judges the transaction t as the signer of a change that needs the role need.
// It returns the trust, the problem when there is one, and what the signatures make of t.
func (s *Store) secretTxn(t *Transaction, need protect.Role, claim secretClaim, rules RecordRules) (SecretTrust, string, TxnVerdict) {
	lineage, readers := s.Lineage(), rules.Readers
	v := VerifyTransaction(lineage, t, readers)
	unknown := func() (SecretTrust, string, TxnVerdict) {
		if rules.Strict {
			return TrustProblem, "the transaction " + t.ID + " was signed by a key that is not in the roles, and no admin vouched for it", v
		}
		return TrustUnchecked, "", v
	}
	if t.Finished == nil || t.FinishSig == nil && t.Status == "running" {
		// A run that is going, or was killed: only its start is signed.
		role, _ := protect.RoleOf(readers, startKey(t))
		switch {
		case v.Start == protect.SigUnknownKey:
			return unknown()
		case v.Start != protect.SigVerified:
			return TrustProblem, "the transaction " + t.ID + " was not started with a signature that verifies", v
		case !role.Allows(need):
			return TrustProblem, "the transaction " + t.ID + " was not started by " + article(need), v
		}
		return TrustUnconfirmed, "the transaction " + t.ID + " did not finish", v
	}
	vouched := false
	if vo, ok := rules.Vouched[t.ID]; ok && t.FinishSig != nil && vo.Key == t.FinishSig.Key && vo.Digest == VouchDigest(lineage, t) {
		vouched = true
	}
	switch v.Finish {
	case protect.SigUnknownKey:
		if !vouched {
			return unknown()
		}
	case protect.SigVerified:
	default:
		return TrustProblem, "the transaction " + t.ID + " is not signed, or its signature does not verify", v
	}
	if v.Altered {
		return TrustProblem, "the transaction " + t.ID + " was altered", v
	}
	if role, _ := protect.RoleOf(readers, t.FinishSig.Key); role.Allows(need) {
		vouched = false // a key that may do it needs no vouching
	} else if !vouched {
		who := v.Signer
		if who == "" {
			who = "a key"
		}
		return TrustProblem, fmt.Sprintf("the transaction %s was run by %s, who is %s; this needs %s", t.ID, who, article(role), article(need)), v
	}
	if why := claim.find(t); why != "" {
		return TrustProblem, why, v
	}
	if vouched {
		return TrustVouched, "", v
	}
	return TrustOK, "", v
}

// article says a role with its article, "a writer" or "an admin".
func article(r protect.Role) string {
	if r == protect.RoleAdmin {
		return "an admin"
	}
	if r == protect.RoleNone {
		return "no one in the roles"
	}
	return "a " + r.String()
}

// judgeSecret fills the trust of a version: the transaction that wrote it, and for a purge the
// transaction that purged it.
func (s *Store) judgeSecret(info *SecretInfo, lookup txnLookup, rules RecordRules) {
	addr := SecretAddr(info.Name)
	need, action := protect.RoleWriter, "set"
	if info.Kind == SecretKindRemoved {
		need, action = protect.RoleAdmin, "remove"
	}
	t := lookup(info.Txn)
	if t == nil {
		info.Trust, info.Problem = TrustProblem, "the transaction "+info.Txn+" that wrote it is not in the state"
		return
	}
	trust, why, v := s.secretTxn(t, need, secretClaim{addr: addr, action: action, seq: info.Seq, commit: info.Commit}, rules)
	info.Trust, info.Problem = trust, why
	info.By, info.Claimed = v.Signer, v.Claimed
	switch {
	case t.FinishSig != nil:
		info.Key = t.FinishSig.Key
	case t.StartSig != nil:
		info.Key = t.StartSig.Key
	}
	if !t.Started.IsZero() && (v.Start == protect.SigVerified || v.Finish == protect.SigVerified) {
		info.At = t.Started
	}
	if info.Kind != SecretKindPurged || trust == TrustProblem {
		return
	}
	// The stub names the transaction that purged it, which an admin ran and which holds the
	// commit of the version it purged.
	pt := lookup(info.PurgedBy)
	if pt == nil {
		info.Trust, info.Problem = TrustProblem, "the transaction "+info.PurgedBy+" that purged it is not in the state"
		return
	}
	ptrust, pwhy, _ := s.secretTxn(pt, protect.RoleAdmin, secretClaim{addr: addr, action: "purge", seq: info.Seq, commit: info.Commit}, rules)
	switch ptrust {
	case TrustProblem:
		info.Trust, info.Problem = TrustProblem, "the purge: "+pwhy
	case TrustUnconfirmed:
		info.Trust, info.Problem = TrustUnconfirmed, "the purge: "+pwhy
	case TrustUnchecked:
		if info.Trust == TrustOK || info.Trust == TrustVouched {
			info.Trust = TrustUnchecked
		}
	case TrustVouched:
		if info.Trust == TrustOK {
			info.Trust = TrustVouched
		}
	}
}

// secretTxnIDs lists the ids of the transactions that a version names.
func secretTxnIDs(f secretFile) []string {
	if f.PurgedBy != "" {
		return []string{f.Txn, f.PurgedBy}
	}
	return []string{f.Txn}
}

// Secrets lists the secrets of the state, by name, each with its latest version judged by the
// transactions. It opens no value, so it needs no key. A state without secrets has none.
func (s *Store) Secrets(ctx context.Context, rules RecordRules) ([]SecretSummary, error) {
	names, err := s.secretNames(ctx)
	if err != nil {
		return nil, err
	}
	encs := make([]string, 0, len(names))
	for enc := range names {
		encs = append(encs, enc)
	}
	sort.Slice(encs, func(i, j int) bool { return SecretDisplay(encs[i]) < SecretDisplay(encs[j]) })
	files := make([]secretFile, len(encs))
	errs := make([]error, len(encs))
	s.each(len(encs), func(i int) { files[i], errs[i] = s.readSecretFile(ctx, encs[i], names[encs[i]]) })
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	var ids []string
	for _, f := range files {
		ids = append(ids, secretTxnIDs(f)...)
	}
	lookup := s.loadTxns(ctx, ids)
	out := make([]SecretSummary, len(encs))
	for i, enc := range encs {
		name := SecretDisplay(enc)
		info := secretInfoOf(name, names[enc], files[i])
		s.judgeSecret(&info, lookup, rules)
		out[i] = SecretSummary{Name: name, Versions: names[enc], Head: info}
	}
	return out, nil
}

// SecretHistory lists every version of the secret, oldest first, each judged by the
// transactions. It opens no value.
func (s *Store) SecretHistory(ctx context.Context, name string, rules RecordRules) ([]SecretInfo, error) {
	if err := ValidSecretName(name); err != nil {
		return nil, err
	}
	enc := encodeAddr(name)
	names, err := s.secretNames(ctx)
	if err != nil {
		return nil, err
	}
	n := names[enc]
	if n == 0 {
		return nil, fmt.Errorf("%s: %w", name, ErrNoSecret)
	}
	files := make([]secretFile, n)
	errs := make([]error, n)
	s.each(n, func(i int) { files[i], errs[i] = s.readSecretFile(ctx, enc, i+1) })
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	var ids []string
	for _, f := range files {
		ids = append(ids, secretTxnIDs(f)...)
	}
	lookup := s.loadTxns(ctx, ids)
	out := make([]SecretInfo, n)
	for i, f := range files {
		out[i] = secretInfoOf(name, i+1, f)
		s.judgeSecret(&out[i], lookup, rules)
	}
	return out, nil
}

// refuseSecret is the error for a version that the roles do not accept: nil when they are off,
// or the version is accepted. A version whose transaction did not finish is refused as well, since
// nothing says what the writer meant to put there.
func refuseSecret(info SecretInfo, rules RecordRules) error {
	if !protect.RolesOn(rules.Readers) {
		return nil
	}
	switch info.Trust {
	case TrustProblem, TrustUnconfirmed:
		return fmt.Errorf("%w: version %d of %s: %s. Nothing is read from it; set the secret again", ErrUntrusted, info.Seq, info.Name, info.Problem)
	}
	return nil
}

// pickSecret finds the version to use: the latest when version is 0. It judges the version and,
// in a state with roles, refuses one that the transactions do not account for. A removed or
// purged version is the error for it.
func (s *Store) pickSecret(ctx context.Context, name string, version int, rules RecordRules) (SecretInfo, secretFile, error) {
	if err := ValidSecretName(name); err != nil {
		return SecretInfo{}, secretFile{}, err
	}
	if !s.sealedState() {
		return SecretInfo{}, secretFile{}, fmt.Errorf("%s: %w: seal the state with pravo protect init --encrypt", name, ErrSecretsNeedSeal)
	}
	enc := encodeAddr(name)
	names, err := s.secretNames(ctx)
	if err != nil {
		return SecretInfo{}, secretFile{}, err
	}
	n := names[enc]
	if n == 0 {
		return SecretInfo{}, secretFile{}, fmt.Errorf("%s: %w", name, ErrNoSecret)
	}
	seq := version
	switch {
	case version == 0:
		seq = n
	case version < 0 || version > n:
		return SecretInfo{}, secretFile{}, fmt.Errorf("%s has versions 1 to %d, not %d", name, n, version)
	}
	f, err := s.readSecretFile(ctx, enc, seq)
	if err != nil {
		return SecretInfo{}, secretFile{}, err
	}
	info := secretInfoOf(name, seq, f)
	s.judgeSecret(&info, s.loadTxns(ctx, secretTxnIDs(f)), rules)
	if err := refuseSecret(info, rules); err != nil {
		return info, f, err
	}
	switch info.Kind {
	case SecretKindRemoved:
		if version == 0 {
			return info, f, fmt.Errorf("%s: %w", name, ErrSecretRemoved)
		}
		return info, f, fmt.Errorf("%s version %d is the removal of the secret, not a value", name, seq)
	case SecretKindPurged:
		return info, f, fmt.Errorf("%s version %d: %w", name, seq, ErrSecretPurged)
	}
	return info, f, nil
}

// SecretHead is the version of the secret that a run reads, judged, without its value: the
// latest, or version when it is not 0. It needs no key.
func (s *Store) SecretHead(ctx context.Context, name string, version int, rules RecordRules) (SecretInfo, error) {
	info, _, err := s.pickSecret(ctx, name, version, rules)
	return info, err
}

// ReadSecret opens the latest version of the secret, or version when it is not 0. In a state with
// roles it refuses, with ErrUntrusted, a version that no transaction of a key that may write
// accounts for; the value that is opened must also match the commit the transaction signed.
func (s *Store) ReadSecret(ctx context.Context, name string, version int, rules RecordRules) (*SecretValue, error) {
	info, f, err := s.pickSecret(ctx, name, version, rules)
	if err != nil {
		return nil, err
	}
	body, err := s.openSecretBody(name, info.Seq, f)
	if err != nil {
		return nil, err
	}
	return &SecretValue{SecretInfo: info, Value: body.Value, Note: body.Note}, nil
}

// openSecretBody opens the sealed part of a value and checks it against its commit.
func (s *Store) openSecretBody(name string, seq int, f secretFile) (secretBody, error) {
	keyed, openable, known := s.sealedGroup(f.Sealed.Group)
	switch {
	case !known:
		return secretBody{}, fmt.Errorf("%s version %d is sealed in group %q, which meta.json does not list: %w", name, seq, f.Sealed.Group, ErrUntrusted)
	case !keyed:
		return secretBody{}, fmt.Errorf("%s: %w", name, ErrNotReader)
	case !openable:
		return secretBody{}, fmt.Errorf("%s version %d is sealed in group %s, which your key cannot open: %w", name, seq, f.Sealed.Group, ErrNotReader)
	}
	var body secretBody
	if err := s.openJSON(secretCtx(s.Lineage(), name, seq), *f.Sealed, &body); err != nil {
		return secretBody{}, fmt.Errorf("version %d of %s does not open where it is: %w", seq, name, ErrUntrusted)
	}
	want := secretCommit(s.Lineage(), name, seq, body.Nonce, body.Value, body.Note)
	if !hmac.Equal([]byte(want), []byte(f.Commit)) {
		clear(body.Value)
		return secretBody{}, fmt.Errorf("version %d of %s does not match its commit: %w", seq, name, ErrUntrusted)
	}
	return body, nil
}

// SecretNotes returns the note of each version of the secret that holds a value, by number.
// It opens the values to do it, and needs a key; a version that cannot be opened has no note.
func (s *Store) SecretNotes(ctx context.Context, name string) (map[int]string, error) {
	if err := ValidSecretName(name); err != nil {
		return nil, err
	}
	enc := encodeAddr(name)
	names, err := s.secretNames(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int]string{}
	for seq := 1; seq <= names[enc]; seq++ {
		f, err := s.readSecretFile(ctx, enc, seq)
		if err != nil {
			return nil, err
		}
		if k, _ := f.kind(); k != SecretKindValue {
			continue
		}
		body, err := s.openSecretBody(name, seq, f)
		if err != nil {
			if errors.Is(err, ErrNotReader) {
				continue
			}
			return nil, err
		}
		clear(body.Value)
		if body.Note != "" {
			out[seq] = body.Note
		}
	}
	return out, nil
}

// SecretProblem is a version that no transaction of a key that may do it accounts for.
type SecretProblem struct {
	Name    string `json:"name"`
	Version int    `json:"version,omitempty"`
	Why     string `json:"why"`
}

// SecretVerdict is what VerifySecrets makes of the secrets of a state.
type SecretVerdict struct {
	// Secrets and Versions count what the state holds.
	Secrets  int `json:"secrets"`
	Versions int `json:"versions"`
	// Bound counts the versions that a verified transaction of a key that may do it names, and
	// Vouched those among them whose key is not a writer now and that an admin vouched for.
	Bound   int `json:"bound"`
	Vouched int `json:"vouched,omitempty"`
	// Unchecked counts the versions signed by a key that is not in the readers file, when the
	// rules are not strict. Unconfirmed counts the versions of a transaction that did not finish
	// and that a later version replaced.
	Unchecked   int `json:"unchecked,omitempty"`
	Unconfirmed int `json:"unconfirmed,omitempty"`
	// Problems lists the versions and the secrets that are not accounted for: sorted by name.
	Problems []SecretProblem `json:"problems,omitempty"`
}

// VerifySecrets checks every version of every secret against the transactions txns, and
// the transactions against the roles. It also finds a version that a verified transaction names
// and the state lacks: the newest of a secret deleted from the bucket. It opens no value.
func (s *Store) VerifySecrets(ctx context.Context, txns []Transaction, rules RecordRules) (SecretVerdict, error) {
	var v SecretVerdict
	names, err := s.secretNames(ctx)
	if err != nil {
		return v, err
	}
	byID := make(map[string]*Transaction, len(txns))
	for i := range txns {
		byID[txns[i].ID] = &txns[i]
	}
	lookup := func(id string) *Transaction { return byID[id] }
	encs := make([]string, 0, len(names))
	for enc := range names {
		encs = append(encs, enc)
	}
	sort.Slice(encs, func(i, j int) bool { return SecretDisplay(encs[i]) < SecretDisplay(encs[j]) })
	type slot struct {
		enc string
		seq int
	}
	var slots []slot
	for _, enc := range encs {
		for seq := 1; seq <= names[enc]; seq++ {
			slots = append(slots, slot{enc, seq})
		}
	}
	files := make([]secretFile, len(slots))
	errs := make([]error, len(slots))
	s.each(len(slots), func(i int) { files[i], errs[i] = s.readSecretFile(ctx, slots[i].enc, slots[i].seq) })
	if err := errors.Join(errs...); err != nil {
		return v, err
	}
	v.Secrets, v.Versions = len(encs), len(slots)
	for i, sl := range slots {
		name := SecretDisplay(sl.enc)
		info := secretInfoOf(name, sl.seq, files[i])
		s.judgeSecret(&info, lookup, rules)
		switch info.Trust {
		case TrustOK:
			v.Bound++
		case TrustVouched:
			v.Bound++
			v.Vouched++
		case TrustUnchecked:
			v.Unchecked++
		case TrustUnconfirmed:
			// A version that a later one replaced is a note; the latest is what a run reads.
			if sl.seq == names[sl.enc] {
				v.Problems = append(v.Problems, SecretProblem{Name: name, Version: sl.seq, Why: info.Problem})
			} else {
				v.Unconfirmed++
			}
		default:
			v.Problems = append(v.Problems, SecretProblem{Name: name, Version: sl.seq, Why: info.Problem})
		}
	}
	// A verified transaction that names a version above the newest the bucket holds: the
	// newest versions were deleted, and the secret is an older one.
	latest := map[string]int{}
	for _, t := range txns {
		if t.Kind != "secret" || t.FinishSig == nil || t.Finished == nil {
			continue
		}
		if st, _ := protect.VerifySignature(t.FinishSig, rules.Readers, protect.DomainTxnFinish, s.Lineage(), txnFinishParts(&t)...); st != protect.SigVerified {
			continue
		}
		for _, c := range t.Changes {
			name, ok := strings.CutPrefix(c.Addr, secretAddrPrefix)
			if !ok || (c.Action != "set" && c.Action != "remove") {
				continue
			}
			latest[name] = max(latest[name], c.After)
		}
	}
	var missing []SecretProblem
	for name, seq := range latest {
		if seq > names[encodeAddr(name)] {
			missing = append(missing, SecretProblem{Name: name, Version: seq, Why: fmt.Sprintf("the transaction that wrote version %d is signed, and the version is not in the bucket: newer versions were deleted", seq)})
		}
	}
	if len(missing) > 0 {
		// A secret written while the transactions were read can look deleted. Look again.
		again, err := s.secretNames(ctx)
		if err != nil {
			return v, err
		}
		for _, m := range missing {
			if m.Version > again[encodeAddr(m.Name)] {
				v.Problems = append(v.Problems, m)
			}
		}
	}
	sort.SliceStable(v.Problems, func(i, j int) bool {
		if v.Problems[i].Name != v.Problems[j].Name {
			return v.Problems[i].Name < v.Problems[j].Name
		}
		return v.Problems[i].Version < v.Problems[j].Version
	})
	return v, nil
}
