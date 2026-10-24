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
