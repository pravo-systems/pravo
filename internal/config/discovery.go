package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/registry"
)

// Discovery is a list resource type of a provider with the scope to list it in,
// written in the specification as
//
//	vms: $aws.#Discovery.aws_instance & {region: "eu-west-1"}
//
// A discovery finds objects that exist. It manages none of them, it is not a node
// of the graph, and no plan or apply reads it: `pravo discover` runs it and the
// viewer shows what it found.
type Discovery struct {
	// Name is the address of the struct, such as "vms" or "web.vms".
	Name string
	// Path is where the struct sits in the specification.
	Path cue.Path
	// Type is the list resource type, such as "aws_instance".
	Type string
	// Source is the provider that lists it.
	Source registry.Source
	// ProviderKey is the provider instance it is listed through.
	ProviderKey string
	// Limit is $limit: the most objects one run lists. Zero is no limit of pravo's.
	Limit int64
	// Scope is the fields of the struct that the list resource's schema gives it,
	// as JSON-ready values, without the fields that start with $. It is nil when
	// ScopeErr is set.
	Scope map[string]any
	// ScopeErr says why the scope cannot be listed with: a field that no value was
	// given, or one that reads a resource. A discovery with this error fails when it
	// runs, and nothing else does.
	ScopeErr error
	// Acks are the entries of $ack: objects that are known and meant to stay
	// unmanaged. They are not part of the scope, and changing them does not make a
	// run stale.
	Acks []*Ack
	// Digest names the scope. A result that was listed with another digest is out
	// of date: the scope, the limit, the type or the provider was changed since.
	// It is empty when ScopeErr is set.
	Digest string

	value cue.Value
}

// Value returns the discovery's struct, as the specification evaluates it.
func (d *Discovery) Value() cue.Value { return d.value }

// NewDiscovery makes the discovery that the struct v at path p is, listed
// through the provider instance providerKey. Loading a specification finds
// discoveries itself; this is for what builds one by hand.
func NewDiscovery(v cue.Value, p cue.Path, typ string, src registry.Source, providerKey string) *Discovery {
	d := newDiscovery(v, p, typ, src)
	d.ProviderKey = providerKey
	d.finish()
	return d
}

// Ack is one entry of the $ack list of a discovery: the objects that are known,
// and meant to stay as they are. They are still listed, and they stop asking to be
// adopted.
type Ack struct {
	// Index is the place of the entry in the list.
	Index int
	// IDs are patterns for the id of an object or its whole identity, and Names
	// patterns for the name the provider gave it. In a pattern * stands for any run
	// of characters and ? for one. An object is covered when any pattern matches.
	IDs    []string `json:"ids"`
	Names  []string `json:"names"`
	Reason string   `json:"reason"`
}

// Limits of an $ack entry.
const (
	MaxAckPatterns = 10000
	MaxPatternLen  = 512
	MaxReasonLen   = 2000
)

// Ref names the entry, as a message or a page says which one covers an object.
func (a *Ack) Ref() string { return fmt.Sprintf("$ack[%d]", a.Index) }

// Covers reports whether the entry covers an object. id is the id a person calls
// it, key its whole identity on one line, importID the id $import takes ("" when
// it gives none) and name the display name ("" when it has none).
func (a *Ack) Covers(id, key, importID, name string) bool {
	for _, p := range a.IDs {
		if Glob(p, id) || Glob(p, key) || (importID != "" && Glob(p, importID)) {
			return true
		}
	}
	if name != "" {
		for _, p := range a.Names {
			if Glob(p, name) {
				return true
			}
		}
	}
	return false
}

// Validate says why an entry cannot be used.
func (a *Ack) Validate() error {
	n := len(a.IDs) + len(a.Names)
	if n == 0 {
		return fmt.Errorf("%s has nothing to match: give ids or names", a.Ref())
	}
	if n > MaxAckPatterns {
		return fmt.Errorf("%s has %d patterns, and at most %d are kept", a.Ref(), n, MaxAckPatterns)
	}
	for _, p := range append(append([]string(nil), a.IDs...), a.Names...) {
		switch {
		case p == "":
			return fmt.Errorf("%s has an empty pattern, which matches nothing", a.Ref())
		case len(p) > MaxPatternLen:
			return fmt.Errorf("%s has a pattern of %d bytes, and at most %d are kept", a.Ref(), len(p), MaxPatternLen)
		case strings.Trim(p, "*") == "":
			return fmt.Errorf("%s: the pattern %q matches everything; write what the objects have in common", a.Ref(), p)
		}
	}
	if len(a.Reason) > MaxReasonLen {
		return fmt.Errorf("%s has a reason of %d bytes, and at most %d are kept", a.Ref(), len(a.Reason), MaxReasonLen)
	}
	return nil
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

// parseAcks reads the $ack list of a discovery.
func parseAcks(v cue.Value, at string) ([]*Ack, error) {
	f := v.LookupPath(cue.MakePath(cue.Str("$ack")))
	if !f.Exists() {
		return nil, nil
	}
	list, err := f.List()
	if err != nil {
		return nil, fmt.Errorf("%s: $ack: %w", at, err)
	}
	var out []*Ack
	for i := 0; list.Next(); i++ {
		a := &Ack{Index: i}
		if err := list.Value().Decode(a); err != nil {
			return nil, fmt.Errorf("%s: $ack[%d]: %s", at, i, firstLine(err))
		}
		a.Index = i
		if err := a.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", at, err)
		}
		out = append(out, a)
	}
	return out, nil
}

func newDiscovery(v cue.Value, p cue.Path, typ string, src registry.Source) *Discovery {
	d := &Discovery{Name: p.String(), Path: p, Type: typ, Source: src, value: v}
	if lim := v.LookupPath(cue.MakePath(cue.Str("$limit"))); lim.Exists() {
		if n, err := lim.Int64(); err == nil {
			d.Limit = n
		}
	}
	d.Scope, d.ScopeErr = scopeOf(v)
	if d.ScopeErr == nil {
		d.Digest = scopeDigest(d)
	}
	return d
}

// scopeOf reads the scope of a discovery: its fields that do not start with $.
func scopeOf(v cue.Value) (map[string]any, error) {
	it, err := v.Fields()
	if err != nil {
		return nil, fmt.Errorf("the scope cannot be read: %w", err)
	}
	scope := map[string]any{}
	for it.Next() {
		sel := it.Selector()
		name := sel.Unquoted()
		if strings.HasPrefix(name, "$") {
			continue
		}
		var x any
		fv := it.Value()
		err := fv.Validate(cue.Concrete(true))
		if err == nil {
			err = fv.Decode(&x)
		}
		if err != nil {
			return nil, fmt.Errorf("%s has no value yet: a scope is written out in the specification and cannot read what a run produces (%v)", name, firstLine(err))
		}
		if x != nil {
			scope[name] = x
		}
	}
	return scope, nil
}

func firstLine(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// scopeDigest is the digest of everything that decides what a run of the
// discovery lists: the provider, the type, the scope and the limit. Go writes the
// keys of a map in order, so the same scope has the same digest whatever order
// its fields were written in.
func scopeDigest(d *Discovery) string {
	doc, _ := json.Marshal(map[string]any{
		"source": d.Source.String(), "key": d.ProviderKey, "type": d.Type, "limit": d.Limit, "scope": d.Scope,
	})
	sum := sha256.Sum256(doc)
	return hex.EncodeToString(sum[:])[:16]
}

// finish works out what depends on the provider instance, which is known only
// after the providers are resolved.
func (d *Discovery) finish() {
	if d.ScopeErr == nil {
		d.Digest = scopeDigest(d)
	}
}

// DiscoveryByName finds a discovery by its address.
func (r *Root) DiscoveryByName(name string) *Discovery {
	for _, d := range r.Discoveries {
		if d.Name == name {
			return d
		}
	}
	return nil
}

// DiscoveryNames returns the addresses of the discoveries, sorted.
func (r *Root) DiscoveryNames() []string {
	out := make([]string, 0, len(r.Discoveries))
	for _, d := range r.Discoveries {
		out = append(out, d.Name)
	}
	sort.Strings(out)
	return out
}
