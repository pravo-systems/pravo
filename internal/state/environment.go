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
	"strings"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// Environments. A state that serves one environment of a specification that declares
// $environments keeps three kinds of object beside its records:
//
//	environment/<seq>.json                      which environment it serves, and which it follows
//	runs/<id>.json                              a note of what each complete apply left it running
//	promotions/<fp>.<tags>.<key>.<kind>.json    a release that this environment may take
//
// The record of the environment is a series of immutable versions, like a policy: the first
// is written by the first apply, and a later one, which changes what the state serves or
// follows, by an admin ("pravo environments record"). A note names the specification by its
// fingerprint and the digest of each tag, and the transaction of the apply. A promotion lets
// one release into the environment: it carries the note of each environment the record says
// this one follows, signed there, so that an apply here checks it against that
// environment's readers without opening its state.
//
// In a sealed state every object is signed, and the first one raises the state to
// FormatEnvironment, which a pravo that does not know them refuses. In a state that is not
// sealed they are written without a signature, as advice: they catch a mistake, such as a
// bucket copied from another environment, and they hold nobody to anything.

const (
	environmentDir = "environment"
	// RunDir holds the notes of what the state runs.
	RunDir = "runs"
	// PromotionDir holds the promotions.
	PromotionDir = "promotions"

	runVersion       = 1
	promotionVersion = 1
	maxRunNote       = 16 << 10
	maxPromotion     = 64 << 10
	// FutureSkew is how far ahead of the clock a note may be dated and still count. A note
	// dated later is ignored, so that one written with a fast clock does not stay the
	// newest for good.
	FutureSkew = 5 * time.Minute
)

// Kinds of promotion.
const (
	// PromotionPromoted lets a release in: the environments this one follows run it.
	PromotionPromoted = "promoted"
	// PromotionWaived lets a release in without that check: an admin gave a reason.
	PromotionWaived = "waived"
	// PromotionRevoked withdraws the promotions of a release that were signed before it.
	PromotionRevoked = "revoked"
)

// ErrEnvironmentChanged is returned when a version of the record of the environment could
// not be put because another command put one at that number first.
var ErrEnvironmentChanged = errors.New("the record of the environment changed since it was read")

var (
	environmentKeyRe = regexp.MustCompile(`^environment/([0-9]{8})\.json$`)
	runKeyRe         = regexp.MustCompile(`^runs/([0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[0-9a-f]{6})\.json$`)
	promotionKeyRe   = regexp.MustCompile(`^promotions/([0-9a-f]{32})\.([0-9a-f]{16})\.([0-9a-f]{16})\.(promoted|waived|revoked)\.json$`)
)

// Upstream is an environment that this one follows, and the lineage of its state once a
// promotion has read it.
type Upstream struct {
	Name    string `json:"name"`
	Lineage string `json:"lineage,omitempty"`
}

// EnvironmentRecord is one version of the record of the environment that a state serves.
type EnvironmentRecord struct {
	// Seq comes from the name of the object: 1 for the first version.
	Seq int `json:"-"`
	// Name is empty in a version that says the state serves no environment any more.
	Name string `json:"name"`
	// After lists the environments it follows, by name.
	After []Upstream `json:"after,omitempty"`
	// RolesPin is the digest of the first version of the roles of the state, as the readers
	// file pins it (roles-genesis). Once it is recorded, a readers file that pins other roles,
	// or none, is refused until an admin records the change, and the roles that decide who is
	// an admin are those of the chain it pins, whatever the readers file says.
	RolesPin  string             `json:"roles_pin,omitempty"`
	At        time.Time          `json:"at"`
	Signature *protect.Signature `json:"signature,omitempty"`
	// Status and Signer say what the readers make of the signature.
	Status protect.SigStatus `json:"-"`
	Signer string            `json:"-"`
}

// AfterNames lists the environments the record follows.
func (r *EnvironmentRecord) AfterNames() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.After))
	for i, u := range r.After {
		out[i] = u.Name
	}
	return out
}

// Lineage is the lineage the record holds for the state of the environment name, empty
// when none was read yet.
func (r *EnvironmentRecord) Lineage(name string) string {
	if r == nil {
		return ""
	}
	for _, u := range r.After {
		if u.Name == name {
			return u.Lineage
		}
	}
	return ""
}

// fills reports whether next only fills in what r left empty: the lineages of the states
// before, and the pin of the roles, with the same name and the same environments followed. A
// writer may sign such a version, since a promotion reads the lineage the first time it opens
// the state before, and a pin that the roles of the state do not match is refused before it
// is written.
func (r *EnvironmentRecord) fills(next *EnvironmentRecord) bool {
	if r.Name != next.Name || len(r.After) != len(next.After) || (r.RolesPin != "" && r.RolesPin != next.RolesPin) {
		return false
	}
	for i := range r.After {
		a, b := r.After[i], next.After[i]
		if a.Name != b.Name || (a.Lineage != "" && a.Lineage != b.Lineage) {
			return false
		}
	}
	return true
}

func environmentKey(seq int) string { return fmt.Sprintf("%s/%08d.json", environmentDir, seq) }

func environmentParts(seq int, r *EnvironmentRecord) [][]byte {
	var after strings.Builder
	for _, u := range r.After {
		after.WriteString(u.Name + "\x00" + u.Lineage + "\n")
	}
	parts := [][]byte{[]byte(strconv.Itoa(seq)), []byte(r.Name), []byte(after.String()), []byte(r.At.UTC().Format(time.RFC3339Nano))}
	if r.RolesPin != "" {
		parts = append(parts, []byte("roles-genesis "+r.RolesPin))
	}
	return parts
}

// EnvironmentRecord reads the newest version of the record of the environment the state
// serves, nil when there is none, and checks it. In a sealed state each version is signed:
// one without a signature, or with one that a key of the readers file did not make, is
// ErrUntrusted, and so is a version after the first that changes the record and was not
// signed by an admin.
func (s *Store) EnvironmentRecord(ctx context.Context, readers []protect.Reader) (*EnvironmentRecord, error) {
	keys, err := s.b.List(ctx, environmentDir)
	if err != nil {
		return nil, err
	}
	var seqs []int
	for _, k := range keys {
		m := environmentKeyRe.FindStringSubmatch(k)
		if m == nil {
			return nil, fmt.Errorf("%w: %s is not a version of the record of the environment", ErrUntrusted, k)
		}
		n, _ := strconv.Atoi(m[1])
		seqs = append(seqs, n)
	}
	if len(seqs) == 0 {
		return nil, nil
	}
	sort.Ints(seqs)
	for i, n := range seqs {
		if n != i+1 {
			return nil, fmt.Errorf("%w: the record of the environment has no version %d", ErrUntrusted, i+1)
		}
	}
	head, err := s.environmentVersion(ctx, readers, len(seqs))
	if err != nil {
		return nil, err
	}
	// The newest note of what the state runs names the version of the record it was written
	// under: a record older than that had its newest versions deleted.
	if s.sealedState() && len(readers) > 0 {
		if n, _, err := s.LatestRun(ctx, time.Now()); err == nil && n != nil && n.EnvRecord > head.Seq {
			if st, _ := n.Verify(s.Lineage(), readers); st == protect.SigVerified {
				return nil, fmt.Errorf("%w: the note %s was written under version %d of the record of the environment, and the bucket holds %d: newer versions were deleted", ErrUntrusted, n.ID, n.EnvRecord, head.Seq)
			}
		}
		facts, err := s.signedFacts(ctx, readers)
		if err != nil {
			return nil, err
		}
		for id, f := range facts {
			if f.envRecord > head.Seq {
				return nil, fmt.Errorf("%w: the transaction %s signed version %d of the record of the environment, and the bucket holds %d: newer versions were deleted", ErrUntrusted, id, f.envRecord, head.Seq)
			}
		}
	}
	if head.Seq > 1 && s.sealedState() {
		prev, err := s.environmentVersion(ctx, readers, head.Seq-1)
		if err != nil {
			return nil, err
		}
		if !prev.fills(head) && (protect.RolesOn(readers) || prev.RolesPin != "") {
			// Once the record pins the roles, the admins are those of the chain it pins: a
			// readers file that pins other roles, or none, makes nobody an admin.
			judges := readers
			if prev.RolesPin != "" {
				if judges, _, err = s.RolesFor(ctx, readers, prev.RolesPin); err != nil {
					return nil, err
				}
			}
			if role, _ := protect.RoleOf(judges, head.Signature.Key); (head.Status == protect.SigVerified || prev.RolesPin != "") && !role.Allows(protect.RoleAdmin) {
				return nil, fmt.Errorf("%w: version %d of the record of the environment changes it, and %s, who signed it, is not an admin", ErrUntrusted, head.Seq, head.Signer)
			}
		}
	}
	return head, nil
}

func (s *Store) environmentVersion(ctx context.Context, readers []protect.Reader, seq int) (*EnvironmentRecord, error) {
	key := environmentKey(seq)
	data, err := s.b.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	r := &EnvironmentRecord{Seq: seq}
	// A version after the first may name no environment: the specification declares none
	// any more, and an admin recorded that.
	if err := json.Unmarshal(data, r); err != nil || (r.Name == "" && seq == 1) {
		return nil, fmt.Errorf("%w: %s is not a version of the record of the environment", ErrUntrusted, key)
	}
	if !s.sealedState() {
		return r, nil
	}
	r.Status, r.Signer = protect.VerifySignature(r.Signature, readers, protect.DomainEnvironment, s.Lineage(), environmentParts(seq, r)...)
	switch r.Status {
	case protect.SigUnsigned:
		return nil, fmt.Errorf("%w: %s has no signature", ErrUntrusted, key)
	case protect.SigInvalid:
		return nil, fmt.Errorf("%w: the signature of %s does not verify", ErrUntrusted, key)
	case protect.SigUnknownKey:
		r.Signer = r.Signature.Signer
	}
	return r, nil
}

// PutEnvironmentRecord writes the record as version seq, the number after the head the
// caller read. In a sealed state it is signed, and the first version raises the format of the
// state. A version at that number that holds the same record is the caller's own earlier
// attempt; any other is ErrEnvironmentChanged.
func (s *Store) PutEnvironmentRecord(ctx context.Context, seq int, rec EnvironmentRecord) error {
	if err := s.writable(); err != nil {
		return err
	}
	rec.At = rec.At.UTC()
	if rec.At.IsZero() {
		rec.At = time.Now().UTC()
	}
	rec.Signature = nil
	sig, signed, err := s.SignMessage(protect.DomainEnvironment, environmentParts(seq, &rec)...)
	if err != nil {
		return fmt.Errorf("signing the record of the environment: %w", err)
	}
	if signed {
		rec.Signature = &sig
		format := FormatEnvironment
		if rec.RolesPin != "" {
			// A pravo before the pin signs the record without it and cannot verify this one:
			// the format makes it stop with "written by a newer pravo".
			format = FormatEnvRoles
		}
		if err := s.setFormat(format); err != nil {
			return err
		}
	}
	data, err := indentJSON(rec)
	if err != nil {
		return err
	}
	key := environmentKey(seq)
	created, err := s.b.PutIfAbsent(ctx, key, data)
	if err != nil {
		return Advise(err)
	}
	if created {
		if signed {
			return s.witnessEnvironmentRecord(seq)
		}
		return nil
	}
	cur, err := s.b.Get(ctx, key)
	if err != nil {
		return err
	}
	var old EnvironmentRecord
	if bytes.Equal(cur, data) || (json.Unmarshal(cur, &old) == nil && old.Name == rec.Name && old.RolesPin == rec.RolesPin && sameUpstreams(old.After, rec.After)) {
		return nil
	}
	return fmt.Errorf("%w: %s exists", ErrEnvironmentChanged, key)
}

// EnvironmentAddr is the address that the change of a transaction gives the record of the
// environment.
const EnvironmentAddr = "environment:record"

// witnessEnvironmentRecord signs a transaction that names the version just written, so that
// deleting the newest versions of the record, which would roll it back, is found even
// before any apply has noted what the state runs.
func (s *Store) witnessEnvironmentRecord(seq int) error {
	t := s.Begin("environment", "record of the environment")
	if err := t.Start(); err != nil {
		return err
	}
	t.Record(EnvironmentAddr, "set", seq-1, seq)
	return t.FinishError("applied", fmt.Sprintf("version %d of the record of the environment", seq), "", true)
}

// EnsureEnvironmentFormat raises the state to the format that the record of the environment
// needs, ahead of the apply that writes the record: a key that cannot put meta.json finds
// out before anything is created. withPin says the record will pin the roles. A state that
// is not sealed holds no signed objects and is left alone.
func (s *Store) EnsureEnvironmentFormat(withPin bool) error {
	if s.Sealer() == nil {
		return nil
	}
	if withPin {
		return s.setFormat(FormatEnvRoles)
	}
	return s.setFormat(FormatEnvironment)
}

func sameUpstreams(a, b []Upstream) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SpecTagDigest is the digest that stands for the value of a tag in a note or a promotion:
// the SHA-256 of the name and the value. It shows that two runs used the same value without
// showing it. It is not keyed: a value that can be guessed, such as a version, can be found
// from it, so a secret does not belong in a tag.
func SpecTagDigest(name, value string) string {
	sum := sha256.Sum256([]byte("pravo/v1/tag\x00" + name + "\x00" + value))
	return hex.EncodeToString(sum[:])
}

// SpecTagDigests digests tags, each given as "name=value". A tag with no "=" is a switch
// whose value is empty.
func SpecTagDigests(tags []string) map[string]string {
	out := map[string]string{}
	for _, t := range tags {
		name, value, _ := strings.Cut(t, "=")
		out[strings.TrimSpace(name)] = SpecTagDigest(strings.TrimSpace(name), value)
	}
	return out
}

// specPart is the part of a signed message that names a specification: its fingerprint and
// the digest of each tag, by tag name.
func specPart(fingerprint string, tags map[string]string) []byte {
	var b strings.Builder
	b.WriteString("spec\x00" + fingerprint + "\x00")
	names := make([]string, 0, len(tags))
	for n := range tags {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		b.WriteString(n + "=" + tags[n] + "\n")
	}
	return []byte(b.String())
}

// TagsDiffer lists the tags whose digests differ between there and here, by name: a value
// is never shown.
func TagsDiffer(there, here map[string]string) []string {
	names := map[string]bool{}
	for k := range there {
		names[k] = true
	}
	for k := range here {
		names[k] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	var out []string
	for _, name := range sorted {
		t, ok1 := there[name]
		h, ok2 := here[name]
		switch {
		case ok1 && !ok2:
			out = append(out, fmt.Sprintf("-t %s was given then and is not given now", name))
		case !ok1 && ok2:
			out = append(out, fmt.Sprintf("-t %s was not given then and is given now", name))
		case t != h:
			out = append(out, fmt.Sprintf("-t %s had another value then", name))
		}
	}
	return out
}

// Admission is how the release that a note names came into the environment: the promotion
// that counted at the apply.
type Admission struct {
	Kind string    `json:"kind"`
	By   string    `json:"by"`
	Key  string    `json:"key"`
	At   time.Time `json:"at"`
	// Note is the reason of a waiver.
	Note string `json:"note,omitempty"`
}

// RunNote says what a state runs after a complete apply: the specification and its tags, the
// revision of the repository, the transaction of the apply, and how the release got in.
type RunNote struct {
	V  int    `json:"v"`
	ID string `json:"id"`
	// Environment is the environment the state serves.
	Environment string            `json:"environment"`
	Fingerprint string            `json:"fingerprint"`
	Tags        map[string]string `json:"tags,omitempty"`
	GitRevision string            `json:"git_revision,omitempty"`
	// Txn is the transaction of the apply; empty for an apply that found nothing to change,
	// which writes no transaction.
	Txn string `json:"txn,omitempty"`
	// At is when the state came to run the release.
	At       time.Time  `json:"at"`
	Admitted *Admission `json:"admitted,omitempty"`
	// Deferred is the number of changes that the apply deferred to the next plan: the state
	// runs the release in part. A note of a state that runs all of it has none.
	Deferred int `json:"deferred,omitempty"`
	// EnvRecord is the number of the version of the record of the environment the note was
	// written under. The note is signed with it, so that deleting the newest versions of the
	// record, which would roll it back to an older one, is found.
	EnvRecord int                `json:"env_record,omitempty"`
	Signature *protect.Signature `json:"signature,omitempty"`
}

func runParts(n *RunNote) [][]byte {
	adm := ""
	if a := n.Admitted; a != nil {
		adm = a.Kind + "\x00" + a.By + "\x00" + a.Key + "\x00" + a.At.UTC().Format(time.RFC3339Nano) + "\x00" + a.Note
	}
	parts := [][]byte{
		[]byte(strconv.Itoa(n.V)), []byte(n.ID), []byte(n.Environment), specPart(n.Fingerprint, n.Tags),
		[]byte(n.GitRevision), []byte(n.Txn), []byte(n.At.UTC().Format(time.RFC3339Nano)), []byte(adm),
	}
	if n.Deferred > 0 {
		// Signed only when set, so that the notes written before it verify as they did.
		parts = append(parts, []byte("deferred="+strconv.Itoa(n.Deferred)))
	}
	if n.EnvRecord > 0 {
		parts = append(parts, []byte("env_record="+strconv.Itoa(n.EnvRecord)))
	}
	return parts
}

// Verify checks the signature of the note against the lineage of the state it was written
// in and the readers of that state.
func (n *RunNote) Verify(lineage string, readers []protect.Reader) (protect.SigStatus, string) {
	return protect.VerifySignature(n.Signature, readers, protect.DomainRun, lineage, runParts(n)...)
}

// runWhen is the time in the id of a run note.
func runWhen(id string) (time.Time, bool) {
	at, _, _ := strings.Cut(id, "-")
	t, err := time.Parse(runIDTime, at)
	return t, err == nil
}

// runIDTime is the time at the start of the id of a run note. It has a fixed width down to the
// nanosecond, so that the names sort in the order the notes were written, also for two applies
// within one second.
const runIDTime = "20060102T150405.000000000Z"

func newRunID(at time.Time) string {
	return at.UTC().Format(runIDTime) + "-" + newID(3)
}

// PutRunNote writes the note, signed in a sealed state, and sets its id and version.
func (s *Store) PutRunNote(ctx context.Context, n *RunNote) error {
	if err := s.writable(); err != nil {
		return err
	}
	n.V, n.ID, n.At = runVersion, newRunID(time.Now()), n.At.UTC()
	if n.At.IsZero() {
		n.At = time.Now().UTC()
	}
	n.Signature = nil
	sig, signed, err := s.SignMessage(protect.DomainRun, runParts(n)...)
	if err != nil {
		return fmt.Errorf("signing the note of what the state runs: %w", err)
	}
	if signed {
		n.Signature = &sig
		if err := s.setFormat(FormatEnvironment); err != nil {
			return err
		}
	}
	data, err := indentJSON(n)
	if err != nil {
		return err
	}
	if _, err := s.b.PutIfAbsent(ctx, RunDir+"/"+n.ID+".json", data); err != nil {
		return Advise(err)
	}
	return nil
}

// LatestRun reads the newest note of what the state runs, nil when there is none. A note
// dated more than FutureSkew ahead of now is passed over, and its key is returned in
// ignored. The note is returned as it is stored: the caller checks its signature with the
// readers of this state.
func (s *Store) LatestRun(ctx context.Context, now time.Time) (note *RunNote, ignored []string, err error) {
	keys, err := s.b.List(ctx, RunDir)
	if err != nil {
		return nil, nil, err
	}
	var ids []string
	for _, k := range keys {
		if m := runKeyRe.FindStringSubmatch(k); m != nil {
			ids = append(ids, m[1])
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	for _, id := range ids {
		if t, ok := runWhen(id); !ok || t.After(now.Add(FutureSkew)) {
			ignored = append(ignored, RunDir+"/"+id+".json")
			continue
		}
		data, err := s.b.Get(ctx, RunDir+"/"+id+".json")
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, ignored, err
		}
		var n RunNote
		if len(data) > maxRunNote || json.Unmarshal(data, &n) != nil || n.ID != id || n.V != runVersion {
			return nil, ignored, fmt.Errorf("%w: %s/%s.json is not a note of what the state runs", ErrUntrusted, RunDir, id)
		}
		return &n, ignored, nil
	}
	return nil, ignored, nil
}

// PromotionSource is one environment that a promotion checked: the lineage of its state and
// the note, signed there, that says it runs the release.
type PromotionSource struct {
	Environment string   `json:"environment"`
	Lineage     string   `json:"lineage"`
	Run         *RunNote `json:"run"`
}

// Promotion lets one release (a specification and the digests of its tags) into the
// environment of the state that holds it, or withdraws that.
type Promotion struct {
	V    int    `json:"v"`
	Kind string `json:"kind"`
	// Environment is the environment the release is let into.
	Environment string            `json:"environment"`
	Fingerprint string            `json:"fingerprint"`
	Tags        map[string]string `json:"tags,omitempty"`
	// From holds the note of each environment this one follows. Empty for a waiver and a
	// withdrawal.
	From []PromotionSource `json:"from,omitempty"`
	At   time.Time         `json:"at"`
	// Note is the reason of a waiver or a withdrawal, or a line kept with a promotion.
	Note      string             `json:"note,omitempty"`
	Signature *protect.Signature `json:"signature,omitempty"`
}

func promotionParts(p *Promotion) [][]byte {
	var from strings.Builder
	for _, src := range p.From {
		from.WriteString(src.Environment + "\x00" + src.Lineage + "\x00")
		if src.Run != nil {
			from.WriteString(src.Run.ID)
			if src.Run.Signature != nil {
				from.WriteString("\x00" + src.Run.Signature.Key + "\x00" + hex.EncodeToString(src.Run.Signature.Sig))
			}
		}
		from.WriteString("\n")
	}
	return [][]byte{
		[]byte(strconv.Itoa(p.V)), []byte(p.Kind), []byte(p.Environment), specPart(p.Fingerprint, p.Tags),
		[]byte(from.String()), []byte(p.At.UTC().Format(time.RFC3339Nano)), []byte(p.Note),
	}
}

// TagsKey is the part of the name of a promotion that stands for the digests of the tags.
func TagsKey(tags map[string]string) string {
	names := make([]string, 0, len(tags))
	for n := range tags {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n + "=" + tags[n] + "\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// PromotionKey is the name of a promotion. A release keeps a promotion of its own, so that
// a rollback to a release that was promoted before stays possible.
func PromotionKey(kind, fingerprint string, tags map[string]string, sigKey string) string {
	k := sha256.Sum256([]byte(sigKey))
	return PromotionDir + "/" + fingerprint + "." + TagsKey(tags) + "." + hex.EncodeToString(k[:8]) + "." + kind + ".json"
}

// Verify checks the signature of the promotion against the lineage of the state that holds it
// and its readers.
func (p *Promotion) Verify(lineage string, readers []protect.Reader) (protect.SigStatus, string) {
	return protect.VerifySignature(p.Signature, readers, protect.DomainPromotion, lineage, promotionParts(p)...)
}

// PutPromotion signs the promotion and writes it. The state must be sealed: a promotion is a
// signature. A promotion by the same key of the same release replaces the one there, so that
// it counts from now; a waiver is never replaced, so that its reason stays.
func (s *Store) PutPromotion(ctx context.Context, p *Promotion) error {
	if err := s.writable(); err != nil {
		return err
	}
	p.V, p.At = promotionVersion, p.At.UTC()
	if p.At.IsZero() {
		p.At = time.Now().UTC()
	}
	p.Signature = nil
	sig, signed, err := s.SignMessage(protect.DomainPromotion, promotionParts(p)...)
	if err != nil {
		return fmt.Errorf("signing the promotion: %w", err)
	}
	if !signed {
		return fmt.Errorf("%w: a promotion is a signature, and the state is not sealed", ErrNotReader)
	}
	p.Signature = &sig
	if err := s.setFormat(FormatEnvironment); err != nil {
		return err
	}
	data, err := indentJSON(p)
	if err != nil {
		return err
	}
	key := PromotionKey(p.Kind, p.Fingerprint, p.Tags, sig.Key)
	created, err := s.b.PutIfAbsent(ctx, key, data)
	if err != nil {
		return Advise(err)
	}
	if created || p.Kind == PromotionWaived {
		return nil
	}
	if _, err := s.b.ReplaceIf(ctx, key, func([]byte) ([]byte, bool) { return data, true }); err != nil && !errors.Is(err, ErrNotFound) {
		return Advise(err)
	}
	return nil
}

// PromotionObject is a promotion as it was read, or why the object under its name is not one.
type PromotionObject struct {
	Key string
	*Promotion
	Err error
}

// Promotions reads the promotions of the release with the fingerprint, or every promotion
// when fingerprint is empty. Only the names of the objects are listed: the cost of an apply
// does not grow with the promotions of other releases.
func (s *Store) Promotions(ctx context.Context, fingerprint string) ([]PromotionObject, error) {
	keys, err := s.b.List(ctx, PromotionDir)
	if err != nil {
		return nil, err
	}
	var out []PromotionObject
	for _, k := range keys {
		m := promotionKeyRe.FindStringSubmatch(k)
		if len(m) != 5 || (fingerprint != "" && m[1] != fingerprint) {
			continue
		}
		data, err := s.b.Get(ctx, k)
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
			o.Err = errors.New("larger than a promotion can be")
		case json.Unmarshal(data, &p) != nil || p.V != promotionVersion || p.Signature == nil:
			o.Err = errors.New("not a promotion")
		case k != PromotionKey(p.Kind, p.Fingerprint, p.Tags, p.Signature.Key):
			o.Err = errors.New("under the name of another release, key or kind")
		default:
			o.Promotion = &p
		}
		out = append(out, o)
	}
	return out, nil
}

// dropEnvironmentAdvice removes the record of the environment and the notes of what the state
// runs that it wrote before it was sealed. They carry no signature, and a sealed state trusts
// only signed ones: its next apply writes them again, signed. A state that is not sealed has
// no promotions.
func (s *Store) dropEnvironmentAdvice(ctx context.Context) error {
	for _, prefix := range []string{environmentDir, RunDir} {
		keys, err := s.b.List(ctx, prefix)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if err := s.b.Delete(ctx, k); err != nil && !errors.Is(err, ErrNotFound) {
				return Advise(err)
			}
		}
	}
	return nil
}

// Vouching for run notes and promotions. An admin who removes a key can carry over the notes
// and promotions that key signed, by id and digest, in the same lines of the roles that vouch
// for its transactions. A vouch covers those objects only.

// vouchDigest is the SHA-256 of what the signature of an object covers, the lineage, the
// signature and its key: an object rewritten in place is not the one that was vouched for.
func vouchDigest(domain, lineage string, sig *protect.Signature, parts [][]byte) string {
	if sig == nil {
		return ""
	}
	h := sha256.New()
	put := func(b []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	put([]byte(domain))
	put([]byte(lineage))
	put([]byte(sig.Key))
	put(sig.Sig)
	for _, p := range parts {
		put(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// VouchID is the id under which the roles vouch for the note. It cannot be the id of a
// transaction.
func (n *RunNote) VouchID() string { return "run-" + n.ID }

// VouchDigest is what an admin vouches for when the note stays valid; empty for an unsigned one.
func (n *RunNote) VouchDigest(lineage string) string {
	return vouchDigest(protect.DomainRun, lineage, n.Signature, runParts(n))
}

// Vouch is the vouch for the note, empty for an unsigned one.
func (n *RunNote) Vouch(lineage string) (protect.Vouch, bool) {
	d := n.VouchDigest(lineage)
	return protect.Vouch{Key: sigKey(n.Signature), Txn: n.VouchID(), Digest: d}, d != ""
}

// Vouched says whether the vouches carry this note over.
func (n *RunNote) Vouched(lineage string, vouched map[string]protect.Vouch) bool {
	v, ok := n.Vouch(lineage)
	return ok && isVouched(vouched, v)
}

// VouchID is the id under which the roles vouch for the promotion: a hash of its name.
func (p *Promotion) VouchID() string {
	if p.Signature == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(PromotionKey(p.Kind, p.Fingerprint, p.Tags, p.Signature.Key)))
	return "promotion-" + hex.EncodeToString(sum[:12])
}

// Vouch is the vouch for the promotion, empty for an unsigned one.
func (p *Promotion) Vouch(lineage string) (protect.Vouch, bool) {
	d := vouchDigest(protect.DomainPromotion, lineage, p.Signature, promotionParts(p))
	if d == "" {
		return protect.Vouch{}, false
	}
	return protect.Vouch{Key: p.Signature.Key, Txn: p.VouchID(), Digest: d}, true
}

// Vouched says whether the vouches carry this promotion over.
func (p *Promotion) Vouched(lineage string, vouched map[string]protect.Vouch) bool {
	v, ok := p.Vouch(lineage)
	return ok && isVouched(vouched, v)
}

func isVouched(vouched map[string]protect.Vouch, v protect.Vouch) bool {
	vo, ok := vouched[v.Txn]
	return ok && vo.Key == v.Key && vo.Digest == v.Digest
}

// EnvironmentVouches are the vouches that would carry over the newest run note and every
// promotion of the state, signed by whichever key.
func (s *Store) EnvironmentVouches(ctx context.Context) ([]protect.Vouch, error) {
	var out []protect.Vouch
	note, _, err := s.LatestRun(ctx, time.Now())
	if err != nil {
		return nil, err
	}
	if note != nil {
		if v, ok := note.Vouch(s.Lineage()); ok {
			out = append(out, v)
		}
	}
	all, err := s.Promotions(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, o := range all {
		if o.Promotion == nil {
			continue
		}
		if v, ok := o.Vouch(s.Lineage()); ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func sigKey(sig *protect.Signature) string {
	if sig == nil {
		return ""
	}
	return sig.Key
}
