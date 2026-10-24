package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// A run of a discovery is kept in the state, apart from the records:
//
//	discovery/runs/<name>.json   what the last run of one discovery found. Replaced by the next run.
//
// It is not a resource. A discovery lists objects that exist and manages none, so
// nothing here is read by a plan or an apply, takes a lock, or is sealed: the object
// holds what a provider listed (an identity and a display name), and no attribute of
// any object. What is known and left alone is written in the specification, in the
// $ack of the discovery.
const (
	// DiscoveryRunDir is the prefix of the runs.
	DiscoveryRunDir = "discovery/runs"

	// DiscoveryFormat is the layout version of a run.
	DiscoveryFormat = 1

	// MaxDiscoveryBytes bounds what is read of one object. A larger one is refused.
	MaxDiscoveryBytes = 128 << 20
	// MaxListed bounds the objects that one run keeps.
	MaxListed = 100000
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
// The id is what a person calls the object: the identity's id when it has a string
// one, its one attribute when it has one, else the whole identity. It is not what
// $import takes: that is the identity.
func ListedOf(identity map[string]any, name string) Listed {
	l := Listed{Key: IdentityKey(identity), Name: name, Identity: identity}
	id, hasID := identity["id"].(string)
	switch {
	case hasID && id != "":
		l.ID = id
	case len(identity) == 1:
		for _, v := range identity {
			if s, ok := v.(string); ok && s != "" {
				l.ID = s
			}
		}
	}
	if l.ID == "" {
		l.ID = l.Key
	}
	return l
}

func runKey(name string) string { return DiscoveryRunDir + "/" + encodeAddr(name) + ".json" }

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
