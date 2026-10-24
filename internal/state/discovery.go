package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Discovery keeps two kinds of object in the state, apart from the records:
//
//	discovery/runs/<name>.json   what the last run of one discovery found. Replaced by the next run.
//	discovery/ack/<id>.json      a record that says "known, leave it": which objects of a discovery
//	                             it covers, and why. Written once, deleted when it is withdrawn.
//
// Neither is a resource. A discovery lists objects that exist and manages none, so
// nothing here is read by a plan or an apply, takes a lock, or is sealed: the
// objects hold what a provider listed (an identity and a display name) and what a
// person wrote, and no attribute of any object.
const (
	// DiscoveryRunDir and DiscoveryAckDir are the prefixes of the two kinds.
	DiscoveryRunDir = "discovery/runs"
	DiscoveryAckDir = "discovery/ack"

	// DiscoveryFormat is the layout version of both objects.
	DiscoveryFormat = 1

	// MaxDiscoveryBytes bounds what is read of one object. A larger one is refused.
	MaxDiscoveryBytes = 128 << 20
	// MaxListed bounds the objects that one run keeps.
	MaxListed = 100000
	// MaxAckPatterns bounds the patterns of one record, and MaxPatternLen one pattern.
	MaxAckPatterns = 10000
	MaxPatternLen  = 512
	// MaxReasonLen bounds the reason of a record.
	MaxReasonLen = 2000
)

// The statuses of a run.
const (
	// RunComplete says that the provider ended its list.
	RunComplete = "complete"
	// RunTruncated says that the run stopped at its limit, so there may be more.
	RunTruncated = "truncated"
)

// Listed is one object that a discovery found.
type Listed struct {
	// Key is the whole identity as one line, "account=1,id=i-1": the names sorted,
	// each with its value. Two objects of one type are the same object when their
	// keys are equal.
	Key string `json:"key"`
	// ID is what a person calls the object: the identity attribute id when it has
	// one, else the key.
	ID string `json:"id"`
	// Name is the display name the provider gave.
	Name string `json:"name,omitempty"`
	// Identity is the identity as the provider gave it.
	Identity map[string]any `json:"identity,omitempty"`
	// ImportID is the id that $import takes to adopt the object, when it can be told
	// from the identity; it is empty when the provider has to be asked.
	ImportID string `json:"import_id,omitempty"`
}

// DiscoveryRun is what one run of one discovery found.
type DiscoveryRun struct {
	V int `json:"v"`
	// Name is the address of the discovery in the specification.
	Name string `json:"name"`
	// Type is the list resource type, and Provider the source of its provider.
	Type     string `json:"type"`
	Provider string `json:"provider"`
	// ProviderKey is the provider instance it was listed through.
	ProviderKey string `json:"provider_key,omitempty"`
	// Digest names the scope the run listed in; see config.Discovery.Digest.
	Digest string `json:"scope_digest"`
	// Scope is the scope itself, for a person to read.
	Scope map[string]any `json:"scope,omitempty"`
	Limit int64          `json:"limit,omitempty"`
	// Started and Finished are UTC.
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`
	Actor    string    `json:"actor,omitempty"`
	Status   string    `json:"status"`
	// Warnings are the warnings the provider gave.
	Warnings []string `json:"warnings,omitempty"`
	Items    []Listed `json:"items"`
}

// AckMatch says which objects of a discovery an acknowledgement covers. An object
// is covered when any pattern matches: IDs against its id and its key, Names
// against its display name. In a pattern * is any run of characters, ? is one,
// and everything else is itself.
type AckMatch struct {
	IDs   []string `json:"ids,omitempty"`
	Names []string `json:"names,omitempty"`
}

// Ack is an acknowledgement: the objects of one discovery that someone knows are
// unmanaged and means to leave so. They stay in the list of the discovery, without
// asking to be adopted. It may cover more than one object, and objects that appear
// later and match.
type Ack struct {
	V int `json:"v"`
	// ID is "ack-" and eight hex digits of the digest of the discovery and the match,
	// so the same record written twice is one record.
	ID        string   `json:"id"`
	Discovery string   `json:"discovery"`
	Match     AckMatch `json:"match"`
	// Reason is why they are left alone, in the words of whoever wrote the record.
	Reason string    `json:"reason,omitempty"`
	By     string    `json:"by,omitempty"`
	At     time.Time `json:"at"`
}

// Covers reports whether the acknowledgement covers an object of its discovery.
func (a *Ack) Covers(l Listed) bool {
	for _, p := range a.Match.IDs {
		if Glob(p, l.ID) || Glob(p, l.Key) || (l.ImportID != "" && Glob(p, l.ImportID)) {
			return true
		}
	}
	for _, p := range a.Match.Names {
		if l.Name != "" && Glob(p, l.Name) {
			return true
		}
	}
	return false
}

// Glob reports whether s matches the pattern: * for any run of characters
// (including none), ? for any one character, and every other character itself.
func Glob(pattern, s string) bool {
	p, t := []rune(pattern), []rune(s)
	pi, ti := 0, 0
	star, mark := -1, 0
	for ti < len(t) {
		switch {
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, ti
			pi++
		case pi < len(p) && (p[pi] == '?' || p[pi] == t[ti]):
			pi++
			ti++
		case star >= 0:
			mark++
			pi, ti = star+1, mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// IdentityKey writes an identity as one line: the names sorted, each with its
// value as JSON writes it when it is not a plain string.
func IdentityKey(identity map[string]any) string {
	names := make([]string, 0, len(identity))
	for n := range identity {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = n + "=" + identityValue(identity[n])
	}
	return strings.Join(parts, ",")
}

// IdentityValue writes one value of an identity, or of a scope, as text: a string as it is
// and anything else as JSON.
func IdentityValue(v any) string { return identityValue(v) }

func identityValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// ListedOf makes the record of a listed object from its identity and display name.
// The id is the identity's id when it has a string one. $import takes an id, and
// the identity gives one only when it is made of an id or of one attribute: with
// more, the provider has to be asked which of them is the id of an import, so
// ImportID is left empty.
func ListedOf(identity map[string]any, name string) Listed {
	l := Listed{Key: IdentityKey(identity), Name: name, Identity: identity}
	id, hasID := identity["id"].(string)
	switch {
	case hasID && id != "":
		l.ID, l.ImportID = id, id
	case len(identity) == 1:
		for _, v := range identity {
			if s, ok := v.(string); ok && s != "" {
				l.ID, l.ImportID = s, s
			}
		}
	}
	if l.ID == "" {
		l.ID = l.Key
	}
	return l
}

func runKey(name string) string { return DiscoveryRunDir + "/" + encodeAddr(name) + ".json" }
func ackKey(id string) string   { return DiscoveryAckDir + "/" + id + ".json" }

// ErrTooLargeDiscovery is returned for an object larger than MaxDiscoveryBytes.
var ErrTooLargeDiscovery = errors.New("a discovery object is too large")

// PutDiscovery stores what a run found, in place of what the last run found.
func (s *Store) PutDiscovery(run *DiscoveryRun) error {
	if run.Name == "" {
		return errors.New("a discovery run needs the address of its discovery")
	}
	if len(run.Items) > MaxListed {
		return fmt.Errorf("discovery %s found %d objects, and at most %d are kept", run.Name, len(run.Items), MaxListed)
	}
	run.V = DiscoveryFormat
	if run.Items == nil {
		run.Items = []Listed{}
	}
	return s.putJSON(runKey(run.Name), run)
}

// Discovery returns what the last run of the discovery found, or nil when it has
// not been run.
func (s *Store) Discovery(name string) (*DiscoveryRun, error) {
	return readRun(s.ctx, s.b, runKey(name))
}

// DeleteDiscovery forgets what the last run of a discovery found.
func (s *Store) DeleteDiscovery(name string) error {
	return s.b.Delete(s.ctx, runKey(name))
}

// DiscoveryProblem is an object that could not be read.
type DiscoveryProblem struct {
	Key string
	Err error
}

func (p DiscoveryProblem) Error() string { return p.Key + ": " + p.Err.Error() }

// Discoveries returns the last run of every discovery that has one, by name.
// An object that cannot be read is returned as a problem and the rest are read.
func (s *Store) Discoveries() (map[string]*DiscoveryRun, []DiscoveryProblem, error) {
	keys, err := s.b.List(s.ctx, DiscoveryRunDir)
	if err != nil {
		return nil, nil, err
	}
	out := map[string]*DiscoveryRun{}
	var problems []DiscoveryProblem
	for _, k := range keys {
		if !strings.HasSuffix(k, ".json") {
			continue
		}
		run, err := readRun(s.ctx, s.b, k)
		switch {
		case err != nil:
			problems = append(problems, DiscoveryProblem{k, err})
		case run == nil: // gone since the listing
		case runKey(run.Name) != k:
			problems = append(problems, DiscoveryProblem{k, errors.New("the object is not at the key of its discovery")})
		default:
			out[run.Name] = run
		}
	}
	return out, problems, nil
}

func readRun(ctx context.Context, b Backend, key string) (*DiscoveryRun, error) {
	data, err := b.Get(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > MaxDiscoveryBytes {
		return nil, fmt.Errorf("%w: %s is %d bytes, and at most %d are read", ErrTooLargeDiscovery, key, len(data), MaxDiscoveryBytes)
	}
	var run DiscoveryRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("reading %s: %w", key, err)
	}
	if run.V > DiscoveryFormat {
		return nil, fmt.Errorf("%s was written by a newer pravo (format %d)", key, run.V)
	}
	return &run, nil
}

// AckID returns the id of an acknowledgement of the discovery with the match: the
// same discovery and the same patterns, whatever their order, give the same id.
func AckID(discovery string, m AckMatch) string {
	ids, names := append([]string(nil), m.IDs...), append([]string(nil), m.Names...)
	sort.Strings(ids)
	sort.Strings(names)
	doc, _ := json.Marshal([]any{discovery, ids, names})
	sum := sha256.Sum256(doc)
	return "ack-" + hex.EncodeToString(sum[:])[:8]
}

// ValidateAck says why an acknowledgement cannot be stored.
func ValidateAck(a *Ack) error {
	if a.Discovery == "" {
		return errors.New("an acknowledgement needs the address of the discovery it is about")
	}
	n := len(a.Match.IDs) + len(a.Match.Names)
	if n == 0 {
		return errors.New("an acknowledgement needs something to match: an id or a name")
	}
	if n > MaxAckPatterns {
		return fmt.Errorf("an acknowledgement has %d patterns, and at most %d are kept", n, MaxAckPatterns)
	}
	for _, p := range append(append([]string(nil), a.Match.IDs...), a.Match.Names...) {
		if p == "" {
			return errors.New("an acknowledgement has an empty pattern, which matches nothing")
		}
		if len(p) > MaxPatternLen {
			return fmt.Errorf("a pattern of an acknowledgement is %d bytes, and at most %d are kept", len(p), MaxPatternLen)
		}
		if strings.Trim(p, "*") == "" {
			return fmt.Errorf("the pattern %q matches everything; acknowledge by what the objects have in common", p)
		}
	}
	if len(a.Reason) > MaxReasonLen {
		return fmt.Errorf("the reason is %d bytes, and at most %d are kept", len(a.Reason), MaxReasonLen)
	}
	return nil
}

// PutAck stores an acknowledgement. It sets the id, the format and the time, and
// the author unless they are set; the same discovery and patterns replace the
// record that had them. It returns the record as stored.
func (s *Store) PutAck(a *Ack) (*Ack, error) {
	if err := ValidateAck(a); err != nil {
		return nil, err
	}
	rec := *a
	rec.V = DiscoveryFormat
	rec.ID = AckID(rec.Discovery, rec.Match)
	if rec.At.IsZero() {
		rec.At = time.Now().UTC()
	}
	if rec.By == "" {
		rec.By = s.actor
	}
	if err := s.putJSON(ackKey(rec.ID), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// Acks returns every acknowledgement, sorted by discovery and id. An object that
// cannot be read is returned as a problem and the rest are read.
func (s *Store) Acks() ([]*Ack, []DiscoveryProblem, error) {
	keys, err := s.b.List(s.ctx, DiscoveryAckDir)
	if err != nil {
		return nil, nil, err
	}
	var out []*Ack
	var problems []DiscoveryProblem
	for _, k := range keys {
		if !strings.HasSuffix(k, ".json") {
			continue
		}
		data, err := s.b.Get(s.ctx, k)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			problems = append(problems, DiscoveryProblem{k, err})
			continue
		}
		var a Ack
		switch {
		case len(data) > MaxDiscoveryBytes:
			problems = append(problems, DiscoveryProblem{k, ErrTooLargeDiscovery})
		case json.Unmarshal(data, &a) != nil:
			problems = append(problems, DiscoveryProblem{k, errors.New("not valid JSON")})
		case a.V > DiscoveryFormat:
			problems = append(problems, DiscoveryProblem{k, fmt.Errorf("written by a newer pravo (format %d)", a.V)})
		case ackKey(a.ID) != k:
			problems = append(problems, DiscoveryProblem{k, errors.New("the object is not at the key of its id")})
		case ValidateAck(&a) != nil:
			problems = append(problems, DiscoveryProblem{k, ValidateAck(&a)})
		default:
			out = append(out, &a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Discovery != out[j].Discovery {
			return out[i].Discovery < out[j].Discovery
		}
		return out[i].ID < out[j].ID
	})
	return out, problems, nil
}

// DeleteAck withdraws an acknowledgement and reports whether there was one.
func (s *Store) DeleteAck(id string) (bool, error) {
	if !validAckID(id) {
		return false, fmt.Errorf("%q is not the id of an acknowledgement", id)
	}
	if _, err := s.b.Get(s.ctx, ackKey(id)); errors.Is(err, ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, s.b.Delete(s.ctx, ackKey(id))
}

func validAckID(id string) bool {
	if !strings.HasPrefix(id, "ack-") || len(id) != 12 {
		return false
	}
	for _, r := range id[4:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
