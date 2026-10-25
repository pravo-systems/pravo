// Package state implements pravo's state store: a graph of resource records
// (one object per resource), a transaction log, outputs and per-resource
// locks, kept in a Backend: a local directory or an S3-compatible bucket.
// There is no single state file and no global lock; every record is written
// on its own, so independent changes can be applied concurrently by
// different processes and from different machines.
//
// Layout (keys relative to the backend root):
//
//	meta.json                format version and lineage
//	resources/<addr>.json    one node per resource, data source or import
//	outputs.json             output values
//	txn/<id>.json            recorded plan/apply/destroy transactions
//	locks/<addr>.lock        held while a process changes that node
package state

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	mrand "math/rand/v2"
	"os"
	"os/user"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// Format is the format of a new store, and the layout that every released pravo
// reads and writes: resources/<address>.json holds the whole record. This build
// reads up to MaxFormat; a store with a higher number was written by a newer
// pravo and is refused. Formats 2 and 3 are in protect_api.go.
const Format = 1

// NewStoreFormat is the format of a store that an open creates when Options
// leaves it zero. It is Format, and is a variable only so that the helpers of the
// tests (statetest) can run a suite over a store of another format.
var NewStoreFormat = Format

// NewStoreIdentity is, for the same helpers, the identity that opens every store
// whose options give none, and that a new store of format 3 is sealed to. It is
// nil, and a store opened with no identity is locked.
var NewStoreIdentity *protect.Identity

// writeStripes is the number of mutexes that order the writes of records.
const writeStripes = 64

// revisionWidth is how many records Revisions reads at the same time.
const revisionWidth = 16

// readWidthLocal and readWidthRemote are how many records a store reads at the
// same time when it opens: a read on this machine is a file, and a read from
// another is a round trip.
const (
	readWidthLocal  = 8
	readWidthRemote = 64
)

// HeartbeatInterval is how often a running command renews the locks it
// holds; StaleAfter is how long a lock may go unrenewed before another
// process may reclaim it (a crashed command on another host). A command that
// ends within HeartbeatInterval renews nothing.
const (
	HeartbeatInterval = 2 * time.Minute
	StaleAfter        = 10 * time.Minute
)

// Resource is one node of the state graph: a resource or data source
// instance together with the bookkeeping that scoped planning needs.
type Resource struct {
	// Addr is the node's address: the CUE path of the resource in the
	// specification, such as "api.config". Nodes written before addresses
	// were paths have none and use the type.name form.
	Addr     string `json:"addr,omitempty"`
	Mode     string `json:"mode"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	// ProviderProtocol is how the provider was reached when the node was
	// last written; empty means the default (tfplugin).
	ProviderProtocol string          `json:"provider_protocol,omitempty"`
	ProviderKey      string          `json:"provider_key"`
	ProviderVersion  string          `json:"provider_version,omitempty"`
	SchemaVersion    int64           `json:"schema_version"`
	Attributes       json.RawMessage `json:"attributes"`
	Private          []byte          `json:"private,omitempty"`
	Dependencies     []string        `json:"dependencies,omitempty"`

	// ConfigHash is a digest of the configuration value the provider last
	// saw for this node. A plan skips the node when the hash is unchanged.
	ConfigHash string `json:"config_hash,omitempty"`
	// ProviderConfig is a digest of the configuration of the provider instance
	// that last wrote the node, left out of the digest where it is sensitive or
	// not known. A plan reads a node whose digest differs from the one of its
	// provider now. A node written without one has none, and nothing is read for
	// it.
	ProviderConfig string `json:"provider_config,omitempty"`
	// DriftedAt is when a read found that the real state of the node differs
	// from the record, or that the object is gone. A node with a mark is planned
	// by every plan until an apply writes it.
	DriftedAt *time.Time `json:"drifted_at,omitempty"`
	// Gone marks a node whose object a read did not find. The record keeps the
	// attributes of the last state that was read.
	Gone bool `json:"gone,omitempty"`
	// Revision increases on every write of the node.
	Revision int `json:"revision"`
	// Generation is the transaction that created the record. A record that is
	// removed and created again has another generation and starts at revision 1
	// again, so a plan is made against a revision and a generation. A record
	// written before generations were recorded has none, and gets one when it is
	// next written.
	Generation string `json:"generation,omitempty"`
	// RefreshedAt is when the provider last reported the node's real state
	// (by reading or by applying it). Freshness policies compare against it.
	RefreshedAt *time.Time `json:"refreshed_at,omitempty"`
	// AppliedAt is when the node was last created or updated by pravo.
	AppliedAt *time.Time `json:"applied_at,omitempty"`
	// UpdatedAt is when the record was last written.
	UpdatedAt time.Time `json:"updated_at"`
	// Txn is the id of the transaction that last wrote the node.
	Txn string `json:"txn,omitempty"`
}

// Marked reports whether a read left a mark on the node: it drifted or it is
// gone.
func (r *Resource) Marked() bool { return r.DriftedAt != nil || r.Gone }

// NoReadTime reports whether an apply wrote the node and the node carries no time
// of a read. An apply that could not tell what it did to the node leaves it so,
// and the next plan reads it whatever its refresh age.
func (r *Resource) NoReadTime() bool { return r.RefreshedAt == nil && r.ConfigHash != "" }

// Address returns the resource address.
func (r *Resource) Address() string {
	if r.Addr != "" {
		return r.Addr
	}
	return Address(r.Mode, r.Type, r.Name)
}

// Address formats a resource address.
func Address(mode, typ, name string) string {
	if mode == "data" {
		return "data." + typ + "." + name
	}
	return typ + "." + name
}

// ParseAddress splits an address into mode, type and name.
func ParseAddress(addr string) (mode, typ, name string, err error) {
	parts := strings.Split(addr, ".")
	switch {
	case len(parts) == 3 && parts[0] == "data":
		return "data", parts[1], parts[2], nil
	case len(parts) == 2:
		return "managed", parts[0], parts[1], nil
	}
	return "", "", "", fmt.Errorf("invalid resource address %q: want <type>.<name> or data.<type>.<name>", addr)
}

type meta struct {
	Format  int       `json:"format"`
	Lineage string    `json:"lineage"`
	Created time.Time `json:"created"`
	// Sealed, Migration and History belong to format 3; see seal.go. The state is
	// sealed when Sealed is present, and not otherwise: a state that was unsealed
	// stays in format 3, with the layout of format 2 and its history.
	Sealed    *protect.Meta  `json:"sealed,omitempty"`
	Migration *migrationMeta `json:"migration,omitempty"`
	History   []historyMeta  `json:"history,omitempty"`
	// GroupsUsed is the highest number of a group that the state ever had. A state
	// that is sealed again after an unseal numbers its groups from there, so that a
	// record left from the first seal is never taken for one of the second.
	GroupsUsed int `json:"groups_used,omitempty"`
}

// Store is an open state graph.
type Store struct {
	b Backend
	// ctx is the context of every call to the backend once the store is open. It
	// carries the values of the context the store was opened with, not its
	// cancellation.
	ctx  context.Context
	meta meta

	// mu guards the maps and meta. It is never held across a call to the
	// backend, so a slow write does not hold up a read of the graph.
	mu      sync.Mutex
	nodes   map[string]*Resource
	outputs map[string]json.RawMessage
	// staged holds what the addresses that a plan changed in memory held before (see stage.go).
	staged map[string]stagedNode
	// held is what the store read or wrote of the object of each node: the version
	// and, in format 2, the record the pointer names. A write replaces the object
	// only if it is still that.
	held map[string]nodeRef
	// strays are the addresses of records that were found sealed in a state whose
	// meta.json says it is not sealed: the sealed part was removed or replaced by
	// something other than an unseal. Such a record reads without its values, and a
	// write over it would lose them.
	strays []string
	// warn receives what a command should tell its user and no more than that: the end of a
	// transaction that could not be signed. Without it the error is returned.
	warn func(string)
	// index is derived from nodes: the addresses in order and the reverse edges.
	// It is built by the first query that needs it and dropped by every change of
	// the nodes. A built index is never changed, so a query reads it without the
	// lock.
	index *graphIndex
	// stripes order the writes of one node. A write reads the record it replaces,
	// puts the new one and publishes it; two writes of one address do not
	// interleave, and writes of different addresses put at the same time.
	stripes [writeStripes]sync.Mutex
	// metaMu orders the change of the format; outMu the writes of outputs.json
	// from this process.
	metaMu, outMu sync.Mutex
	// nonce identifies this process's locks across hosts.
	nonce string
	// The state is sealed (see seal.go): id is the identity the store was opened
	// with, ring holds the data keys it unlocked, openable lists the groups it holds
	// a lock in, and locked is set when it holds no key for the state. metaRead is
	// when meta.json was last read, for the check for a new group.
	id       *protect.Identity
	ring     keyring
	openable map[string]bool
	locked   bool
	// closed is set by Close: the store takes no keys again.
	closed   bool
	you      You
	metaRead time.Time
	// groupReload is when meta.json was last read again because a value is sealed in
	// a group the store does not know; reloadMu lets one goroutine do it for all.
	groupReload time.Time
	reloadMu    sync.Mutex
	// metaChecked is when writable last compared meta.json with the format the store
	// holds; zero until the first write, which always looks.
	metaChecked time.Time

	actor, reason, gitRevision, traceID string
}

// Open opens (or creates) a local store in the directory path. A path that
// names a file is an error and changes nothing: the state is a directory, and
// an OpenTofu state file is read with "pravo state import-tfstate".
func Open(path string) (*Store, error) {
	return OpenWith(path, Options{})
}

// OpenBackend opens (or creates) the store kept in b. ctx governs the opening
// only: once the store is open, its reads and writes do not end with ctx. A
// command that is interrupted still has to record what it did, release its locks
// and finish its transaction, and a backend that honours the context, as the S3
// client does, would refuse every one of those calls.
func OpenBackend(ctx context.Context, b Backend) (*Store, error) {
	return openBackend(ctx, b, Options{})
}

// openBackend opens the store kept in b.
func openBackend(ctx context.Context, b Backend, o Options) (*Store, error) {
	s := &Store{b: b, ctx: ctx, nodes: map[string]*Resource{}, held: map[string]nodeRef{}, outputs: map[string]json.RawMessage{}, nonce: newID(8)}
	if o.Identity == nil {
		o.Identity = NewStoreIdentity
	}
	sealNew := false
	data, err := b.Get(ctx, "meta.json")
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &s.meta); err != nil {
			return nil, fmt.Errorf("reading %s/meta.json: %w", b.Name(), err)
		}
		if s.meta.Format > MaxFormat {
			return nil, fmt.Errorf("state at %s was written by a newer pravo (format %d)", b.Name(), s.meta.Format)
		}
	case errors.Is(err, ErrNotFound):
		format := o.NewFormat
		if format == 0 {
			format = NewStoreFormat
		}
		if format == FormatSealed && NewStoreIdentity != nil {
			// A suite that runs over sealed stores: the store is made in format 2 and sealed
			// once it is open.
			format, sealNew = FormatImmutable, true
		}
		if format != Format && format != FormatImmutable {
			return nil, fmt.Errorf("a new state is created in format %d or %d, not %d; a sealed state is made by upgrading one", Format, FormatImmutable, format)
		}
		s.meta = meta{Format: format, Lineage: newID(16), Created: time.Now().UTC()}
		if err := s.putJSON("meta.json", s.meta); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("opening state at %s: %w", b.Name(), err)
	}
	if s.meta.Sealed != nil {
		err := s.openSealed(o.Identity)
		if errors.Is(err, ErrUntrusted) && s.rereadMeta(ctx, data) {
			// meta.json was replaced since it was read, and what was read of it may
			// have been a part of a change.
			err = s.openSealed(o.Identity)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	s.ctx = context.WithoutCancel(ctx)
	if sealNew {
		id := NewStoreIdentity
		if _, err := s.Upgrade(ctx, UpgradeOptions{To: FormatSealed, Readers: []protect.Reader{id.AsReader()}, Identity: id}); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// rereadMeta reads meta.json again and, if it differs from first, the bytes read
// when the store was opened, takes it as the meta of the store. It reports whether
// it did.
func (s *Store) rereadMeta(ctx context.Context, first []byte) bool {
	data, err := s.b.Get(ctx, "meta.json")
	if err != nil || bytes.Equal(data, first) {
		return false
	}
	var m meta
	if json.Unmarshal(data, &m) != nil || m.Format > MaxFormat || m.Lineage != s.meta.Lineage {
		return false
	}
	s.meta = m
	return true
}

func (s *Store) load() error {
	keys, err := s.b.List(s.ctx, "resources")
	if err != nil {
		return err
	}
	var jsonKeys []string
	for _, k := range keys {
		if strings.HasSuffix(k, ".json") {
			jsonKeys = append(jsonKeys, k)
		}
	}
	// Fetch nodes concurrently; remote backends pay a round trip per node.
	type res struct {
		r   *Resource
		h   nodeRef
		err error
	}
	results := make([]res, len(jsonKeys))
	width := readWidthLocal
	if !s.b.Local() {
		width = readWidthRemote
	}
	var (
		wg   sync.WaitGroup
		next atomic.Int64
	)
	for w := 0; w < min(width, len(jsonKeys)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(jsonKeys) {
					return
				}
				r, h, err := s.readNode(s.ctx, jsonKeys[i])
				results[i].r, results[i].h, results[i].err = r, h, err
			}
		}()
	}
	wg.Wait()
	for _, r := range results {
		if r.err != nil {
			return r.err
		}
		if r.r != nil {
			s.nodes[r.r.Address()] = r.r
			s.held[r.r.Address()] = r.h
		}
	}
	data, err := s.b.Get(s.ctx, "outputs.json")
	if err == nil {
		// A locked store lists the records and has no outputs.
		if out, err := s.decodeOutputs(data); err == nil {
			s.outputs = out
		} else if !s.locked || errors.Is(err, ErrUntrusted) {
			return err
		}
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if s.outputs == nil {
		s.outputs = map[string]json.RawMessage{}
	}
	return nil
}

// Backend returns the storage the store uses.
func (s *Store) Backend() Backend { return s.b }

// Dir returns the backend name (a directory for local stores).
func (s *Store) Dir() string { return s.b.Name() }

// Path is an alias of Dir kept for messages.
func (s *Store) Path() string { return s.b.Name() }

// Lineage identifies the store across its whole life.
func (s *Store) Lineage() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta.Lineage
}

// Format returns the format version of the store as it is written now.
func (s *Store) Format() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta.Format
}

// SetActor records who is operating the store, for transactions and locks.
func (s *Store) SetActor(actor, reason, gitRevision string) {
	s.actor, s.reason, s.gitRevision = actor, reason, gitRevision
}

// SetTrace records the OpenTelemetry trace id stored on new transactions.
func (s *Store) SetTrace(traceID string) { s.traceID = traceID }

// Actor returns the operating actor.
func (s *Store) Actor() string { return s.actor }

// encodeAddr turns an address into a safe object name.
func encodeAddr(addr string) string {
	var b strings.Builder
	for _, r := range addr {
		switch {
		case r == '.' || r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, "%%%04X", r)
		}
	}
	return b.String()
}

func nodeKey(addr string) string { return "resources/" + encodeAddr(addr) + ".json" }
func lockKey(addr string) string { return "locks/" + encodeAddr(addr) + ".lock" }

// indentJSON encodes v as indented JSON that ends with a newline. The text of
// a value is written as it is: <, > and & are not written as \u003c, \u003e
// and \u0026.
func indentJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// putJSON writes v as indented JSON.
func (s *Store) putJSON(key string, v any) error {
	data, err := indentJSON(v)
	if err != nil {
		return err
	}
	return s.b.Put(s.ctx, key, data)
}

// newTxnID returns the id of a transaction: the UTC time and six hex digits.
func newTxnID() string {
	return time.Now().UTC().Format("20060102T150405Z") + "-" + newID(3)
}

func newID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Get returns the node at addr, or nil.
func (s *Store) Get(addr string) *Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nodes[addr]
}

// Verify reads the record at addr from the backend and reports ErrChanged if it
// is not the node this store holds: another process wrote, removed or created it
// since the store read it. A command that rewrites a node it holds the lock of,
// from the copy it read, verifies it first, or it would write over what another
// command wrote before the lock was taken.
func (s *Store) Verify(addr string) error {
	if s.Staged(addr) {
		return nil // there is no record in the backend to differ from
	}
	cur, found, err := s.head(addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	n, ok := s.nodes[addr]
	var held Version
	if ok {
		held = Version{Revision: n.Revision, Generation: n.Generation}
	}
	s.mu.Unlock()
	if found != ok || cur != held {
		return fmt.Errorf("%s: %w", addr, ErrChanged)
	}
	return nil
}

// Version identifies a record as it is in the backend: the revision of the
// record and the generation it belongs to. An address with no record has the
// zero Version.
type Version struct {
	Revision   int
	Generation string
}

// head reads the version of the record at addr from the backend. found is false
// when there is no record.
func (s *Store) head(addr string) (v Version, found bool, err error) {
	data, err := s.b.Get(s.ctx, nodeKey(addr))
	switch {
	case errors.Is(err, ErrNotFound):
		return Version{}, false, nil
	case err != nil:
		return Version{}, false, fmt.Errorf("reading %s: %w", nodeKey(addr), err)
	}
	var head struct {
		Revision   int    `json:"revision"`
		Generation string `json:"generation"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Version{}, false, fmt.Errorf("reading state node %s: %w", nodeKey(addr), err)
	}
	return Version{Revision: head.Revision, Generation: head.Generation}, true, nil
}

// Versions reads the records at addrs from the backend and returns the version
// of each; an address with no record has the zero Version. The nodes this store
// holds were read when it was opened: another process may have written a record
// since, and only the backend says so.
func (s *Store) Versions(addrs []string) (map[string]Version, error) {
	out := make(map[string]Version, len(addrs))
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		sem      = make(chan struct{}, revisionWidth)
	)
	for _, addr := range addrs {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			v, _, err := s.head(addr)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			out[addr] = v
		}(addr)
	}
	wg.Wait()
	return out, firstErr
}

// Revisions is Versions for the revision alone.
func (s *Store) Revisions(addrs []string) (map[string]int, error) {
	versions, err := s.Versions(addrs)
	out := make(map[string]int, len(versions))
	for addr, v := range versions {
		out[addr] = v.Revision
	}
	return out, err
}

// List returns all nodes sorted by address.
func (s *Store) List() []*Resource {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexLocked()
	out := make([]*Resource, len(idx.addrs))
	for i, addr := range idx.addrs {
		out[i] = s.nodes[addr]
	}
	return out
}

// Len returns the number of nodes.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.nodes)
}

// Addresses lists all node addresses in order.
func (s *Store) Addresses() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.indexLocked().addrs)
}

// stripe returns the mutex that orders the writes of the node at addr.
func (s *Store) stripe(addr string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(addr))
	return &s.stripes[h.Sum32()%writeStripes]
}

// ErrChanged is returned by Refresh when the record in the backend is not the
// one this store holds: another process wrote, removed or created it since the
// store read it.
var ErrChanged = errors.New("the record changed in the state since it was read")

// Refresh writes a node after a refresh. When nothing but the timestamps
// differs from the stored node, the revision is kept, so that merely looking
// at the real world does not invalidate saved plans made against the node.
//
// What a refresh read belongs to the record that the store holds. The caller
// holds the lock of the node, and the record in the backend is read once more
// under it: when its revision or generation is not that of the node the store
// holds, nothing is written and the error is ErrChanged, because the new record
// would replace what another process wrote with what this one read before.
func (s *Store) Refresh(r *Resource, txn string) error {
	addr := r.Address()
	if s.writeStaged(r, addr, false) {
		return nil
	}
	mu := s.stripe(addr)
	mu.Lock()
	defer mu.Unlock()
	s.mu.Lock()
	prev, ok := s.nodes[addr]
	same := ok && prev != r && jsonSame(prev.Attributes, r.Attributes) && bytes.Equal(prev.Private, r.Private) &&
		slices.Equal(prev.Dependencies, r.Dependencies) && prev.ConfigHash == r.ConfigHash &&
		prev.ProviderVersion == r.ProviderVersion && prev.SchemaVersion == r.SchemaVersion &&
		prev.Gone == r.Gone && sameTime(prev.DriftedAt, r.DriftedAt)
	var held Version
	if ok {
		held = Version{Revision: prev.Revision, Generation: prev.Generation}
	}
	s.mu.Unlock()
	cur, found, err := s.head(addr)
	if err != nil {
		return err
	}
	if found != ok || cur != held {
		return fmt.Errorf("%s: %w", addr, ErrChanged)
	}
	if !same {
		return s.set(r, txn)
	}
	return s.put(r, prev.Revision, prev.Generation, txn)
}

// Set writes a node, bumping its revision. txn is recorded on the node and
// may be empty.
func (s *Store) Set(r *Resource, txn string) error {
	if s.writeStaged(r, r.Address(), false) {
		return nil
	}
	mu := s.stripe(r.Address())
	mu.Lock()
	defer mu.Unlock()
	return s.set(r, txn)
}

// Invalidate writes the node at addr again without its refresh time. Every
// refresh age has run out for such a node, so the next plan reads it. It is a
// write like Set: the revision moves, and a plan made against the node is stale.
// It returns the revision before and after, and false when there is no node.
func (s *Store) Invalidate(addr, txn string) (before, after int, found bool, err error) {
	if s.Staged(addr) {
		s.mu.Lock()
		prev, ok := s.nodes[addr]
		s.mu.Unlock()
		if !ok {
			return 0, 0, false, nil
		}
		n := *prev
		n.RefreshedAt = nil
		s.writeStaged(&n, addr, false)
		return prev.Revision, prev.Revision, true, nil
	}
	mu := s.stripe(addr)
	mu.Lock()
	defer mu.Unlock()
	s.mu.Lock()
	prev, ok := s.nodes[addr]
	s.mu.Unlock()
	if !ok {
		return 0, 0, false, nil
	}
	n := *prev
	n.RefreshedAt = nil
	if err := s.set(&n, txn); err != nil {
		return prev.Revision, prev.Revision, true, err
	}
	return prev.Revision, n.Revision, true, nil
}

// set is Set for a caller that holds the stripe of the node. A node that has no
// generation gets one: the record is new, or was written before generations
// were recorded.
func (s *Store) set(r *Resource, txn string) error {
	s.mu.Lock()
	rev, gen := r.Revision, r.Generation
	if prev, ok := s.nodes[r.Address()]; ok && prev != r {
		rev = prev.Revision
		if gen == "" {
			gen = prev.Generation
		}
	}
	s.mu.Unlock()
	if gen == "" {
		gen = txn
	}
	if gen == "" {
		gen = newTxnID()
	}
	return s.put(r, rev+1, gen, txn)
}

// put writes a copy of the node at the given revision to the backend, and only
// when that succeeded makes it the node of the store: a write that fails
// changes nothing in memory, neither the store nor r. The caller holds the
// stripe of the node.
func (s *Store) put(r *Resource, revision int, generation, txn string) error {
	if err := s.writable(); err != nil {
		return err
	}
	c := *r
	c.Revision, c.Generation = revision, generation
	c.UpdatedAt = time.Now().UTC()
	if txn != "" {
		c.Txn = txn
	}
	ref, err := s.putNode(&c)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r.Revision, r.Generation, r.UpdatedAt, r.Txn = c.Revision, c.Generation, c.UpdatedAt, c.Txn
	s.nodes[r.Address()] = r
	s.held[r.Address()] = ref
	s.index = nil
	return nil
}

// sameTime reports whether two optional times are the same instant.
func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// Remove deletes the node at addr and reports whether it existed.
func (s *Store) Remove(addr string) (bool, error) {
	if s.Staged(addr) {
		s.mu.Lock()
		_, ok := s.nodes[addr]
		s.mu.Unlock()
		s.writeStaged(nil, addr, true)
		return ok, nil
	}
	mu := s.stripe(addr)
	mu.Lock()
	defer mu.Unlock()
	s.mu.Lock()
	_, ok := s.nodes[addr]
	ref := s.held[addr]
	s.mu.Unlock()
	if !ok {
		return false, nil
	}
	if err := s.writable(); err != nil {
		return true, err
	}
	if err := s.removeNode(addr, ref); err != nil {
		return true, err
	}
	s.mu.Lock()
	delete(s.nodes, addr)
	delete(s.held, addr)
	s.index = nil
	s.mu.Unlock()
	return true, nil
}

// Outputs returns the recorded outputs.
func (s *Store) Outputs() map[string]json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]json.RawMessage, len(s.outputs))
	for k, v := range s.outputs {
		out[k] = v
	}
	return out
}

// ReloadOutputs reads the recorded outputs again from the backend, so that
// outputs written by another command since this store was opened are seen.
func (s *Store) ReloadOutputs() (map[string]json.RawMessage, error) {
	out, _, err := s.LoadOutputs()
	return out, err
}

// LoadOutputs is ReloadOutputs that also reports whether the backend holds the
// object outputs.json at all. A state whose outputs were never written has no
// such object, and a root that reads these outputs refuses it.
func (s *Store) LoadOutputs() (outputs map[string]json.RawMessage, present bool, err error) {
	data, err := s.b.Get(s.ctx, "outputs.json")
	out := map[string]json.RawMessage{}
	switch {
	case err == nil:
		present = true
		if out, err = s.decodeOutputs(data); err != nil {
			return nil, false, err
		}
		if out == nil {
			out = map[string]json.RawMessage{}
		}
	case !errors.Is(err, ErrNotFound):
		return nil, false, err
	}
	s.mu.Lock()
	s.outputs = out
	s.mu.Unlock()
	return s.Outputs(), present, nil
}

// SetOutputs replaces the recorded outputs.
func (s *Store) SetOutputs(outputs map[string]json.RawMessage) error {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	return s.setOutputs(outputs)
}

// setOutputs is SetOutputs for a caller that holds outMu.
func (s *Store) setOutputs(outputs map[string]json.RawMessage) error {
	if err := s.writable(); err != nil {
		return err
	}
	if outputs == nil {
		outputs = map[string]json.RawMessage{}
	}
	data, err := s.encodeOutputs(outputs)
	if err != nil {
		return err
	}
	if err := s.b.Put(s.ctx, "outputs.json", data); err != nil {
		return err
	}
	s.mu.Lock()
	s.outputs = outputs
	s.mu.Unlock()
	return nil
}

// outputsLockKey is the object an update of the outputs holds while it reads,
// merges and writes outputs.json.
const outputsLockKey = "outputs.lock"

// The lock on the outputs is held for the time of one read and one write. A
// holder that has not released it after outputsLockStale is gone. An update that
// finds it held waits for outputsLockWait at most, which is longer than
// outputsLockStale, so that an update which meets the lock of a command that was
// killed reclaims it and does not fail after the resources are applied.
var (
	outputsLockStale = 60 * time.Second
	outputsLockWait  = outputsLockStale + 10*time.Second
)

// UpdateOutputs reads the recorded outputs from the backend while it holds the
// lock on them, passes them to fn and records what fn returns. present says
// whether the backend holds outputs.json at all. fn returns the outputs to
// record and whether to write them. Two commands that finish together each
// merge their outputs into what the other wrote.
func (s *Store) UpdateOutputs(fn func(recorded map[string]json.RawMessage, present bool) (updated map[string]json.RawMessage, write bool, err error)) error {
	if err := s.writable(); err != nil {
		return err
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	unlock, err := s.lockOutputs()
	if err != nil {
		return err
	}
	defer unlock()
	recorded, present, err := s.LoadOutputs()
	if err != nil {
		return err
	}
	updated, write, err := fn(recorded, present)
	if err != nil || !write {
		return err
	}
	return s.setOutputs(updated)
}

// lockOutputs takes the lock on outputs.json, waiting for a holder that is
// about to release it, and returns the function that releases it.
func (s *Store) lockOutputs() (func(), error) {
	l := s.newLock("outputs.json", "update outputs", "")
	data, _ := indentJSON(l)
	deadline := time.Now().Add(outputsLockWait)
	delay := 2 * time.Millisecond
	unreadable := false
	misses := 0
	for {
		created, err := s.b.PutIfAbsent(s.ctx, outputsLockKey, data)
		unknown := errors.Is(err, ErrUnknownOutcome)
		if err != nil && !unknown {
			return nil, err
		}
		if created {
			return s.unlockOutputs, nil
		}
		raw, holder, rerr := s.readOutputsLock()
		if rerr == nil && bytes.Equal(raw, data) {
			// A create of unknown outcome that was made, found now or by a later round
			// whose own create was refused: the bytes are this call's.
			return s.unlockOutputs, nil
		}
		if errors.Is(rerr, ErrNotFound) {
			// Released between the two calls: ask again. A store that refuses the
			// create and has no object under the key is not waited for for ever.
			if misses++; misses > 3 {
				if time.Now().After(deadline) {
					return nil, fmt.Errorf("outputs.json stayed locked for %s: the store refuses to create the lock and has none to read", outputsLockWait)
				}
				time.Sleep(delay + mrand.N(delay))
				if delay < 100*time.Millisecond {
					delay *= 2
				}
			}
			continue
		}
		misses = 0
		// An object that cannot be read may be half written; it is the holder's no
		// longer when it is still unreadable the next time.
		gone := rerr == nil && s.outputsLockStale(holder) || rerr != nil && unreadable
		unreadable = rerr != nil
		if gone {
			// Deleted only if it is still the object that was judged.
			_, _ = s.b.DeleteIf(s.ctx, outputsLockKey, func(current []byte) bool { return bytes.Equal(current, raw) })
			unreadable = false
		}
		if time.Now().After(deadline) {
			if rerr != nil {
				return nil, fmt.Errorf("outputs.json stayed locked for %s: %w", outputsLockWait, rerr)
			}
			return nil, fmt.Errorf("outputs.json stayed locked for %s: %w", outputsLockWait, &ErrLocked{Lock: holder})
		}
		time.Sleep(delay + mrand.N(delay))
		if delay < 100*time.Millisecond {
			delay *= 2
		}
	}
}

// unlockOutputs releases the lock on outputs.json if this process holds it.
func (s *Store) unlockOutputs() {
	_, _ = s.b.DeleteIf(s.ctx, outputsLockKey, func(current []byte) bool {
		var l Lock
		return json.Unmarshal(current, &l) == nil && l.Nonce == s.nonce
	})
}

// readOutputsLock reads the lock on outputs.json with the bytes it was read from.
func (s *Store) readOutputsLock() ([]byte, Lock, error) {
	var l Lock
	data, err := s.b.Get(s.ctx, outputsLockKey)
	if err != nil {
		return nil, l, err
	}
	err = json.Unmarshal(data, &l)
	if l.Addr == "" {
		l.Addr = "outputs.json"
	}
	return data, l, err
}

// outputsLockStale reports whether the holder of the lock on the outputs is
// gone: its process is gone on this host, or it has held the lock too long.
func (s *Store) outputsLockStale(l Lock) bool {
	if s.b.Local() {
		host, _ := os.Hostname()
		if l.Host == host && l.PID != os.Getpid() && !processAlive(l.PID) {
			return true
		}
	}
	return time.Since(l.Since) > outputsLockStale
}

// Clear removes every node and all outputs (used by imports with -force).
func (s *Store) Clear() error {
	for _, addr := range s.Addresses() {
		if _, err := s.Remove(addr); err != nil {
			return err
		}
	}
	return s.SetOutputs(nil)
}

// --- graph queries ---

// graphIndex answers the questions about the edges of the graph without a pass
// over every record.
type graphIndex struct {
	// addrs lists the address of every node, sorted.
	addrs []string
	// dependents maps an address to the addresses of the nodes that list it in
	// their dependencies, sorted, each once.
	dependents map[string][]string
}

// indexLocked returns the index of the nodes, built if a change dropped it. The
// caller holds mu.
func (s *Store) indexLocked() *graphIndex {
	if s.index != nil {
		return s.index
	}
	idx := &graphIndex{addrs: make([]string, 0, len(s.nodes)), dependents: map[string][]string{}}
	for addr := range s.nodes {
		idx.addrs = append(idx.addrs, addr)
	}
	sort.Strings(idx.addrs)
	for _, addr := range idx.addrs {
		for _, d := range s.nodes[addr].Dependencies {
			// The lists fill in address order, so a repeated dependency of one node
			// is the last entry of the list it repeats in.
			if list := idx.dependents[d]; len(list) == 0 || list[len(list)-1] != addr {
				idx.dependents[d] = append(list, addr)
			}
		}
	}
	s.index = idx
	return idx
}

// graph returns the index of the nodes.
func (s *Store) graph() *graphIndex {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.indexLocked()
}

// Dependents returns the addresses of nodes that directly depend on addr.
func (s *Store) Dependents(addr string) []string {
	return append([]string{}, s.graph().dependents[addr]...)
}

// LockDependents takes the lock of every record that directly depends on addr,
// all or none, and checks that each is in the backend as this store read it. It
// is for a command that rewrites the dependencies of those records, which is a
// write to each of them: a record that another process holds the lock of, or has
// written since this store read it, would be overwritten with an older copy. It
// returns the addresses of the records and the function that releases the locks.
func (s *Store) LockDependents(addr, command, txn string) (deps []string, release func(), err error) {
	deps = s.Dependents(addr)
	var held []string
	release = func() {
		for _, a := range held {
			_ = s.Unlock(a)
		}
	}
	for _, d := range deps {
		if err := s.Lock(d, command, txn); err != nil {
			release()
			return nil, nil, fmt.Errorf("%s depends on %s: %w", d, addr, err)
		}
		held = append(held, d)
	}
	versions, err := s.Versions(deps)
	if err != nil {
		release()
		return nil, nil, err
	}
	for _, d := range deps {
		s.mu.Lock()
		n := s.nodes[d]
		s.mu.Unlock()
		if n == nil || versions[d] != (Version{Revision: n.Revision, Generation: n.Generation}) {
			release()
			return nil, nil, fmt.Errorf("%s depends on %s and changed in the state since it was read; run the command again", d, addr)
		}
	}
	return deps, release, nil
}

// Reach returns every node that transitively depends on addr: the blast
// radius of a change to it. The result is sorted and excludes addr.
func (s *Store) Reach(addr string) []string {
	return s.ReachOf([]string{addr})
}

// ReachOf returns every node that transitively depends on one of the addresses,
// sorted, in one walk of the edges that visits each node once. A node is in the
// result when an edge leads to it, so an address is in it only when another
// address, or a cycle, leads back to it.
func (s *Store) ReachOf(addrs []string) []string {
	idx := s.graph()
	seen := map[string]bool{}
	queue := slices.Clone(addrs)
	for len(queue) > 0 {
		a := queue[0]
		queue = queue[1:]
		for _, d := range idx.dependents[a] {
			if !seen[d] {
				seen[d] = true
				queue = append(queue, d)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// Edge is one dependency edge of the graph.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Edges lists every dependency edge (from depends on to).
func (s *Store) Edges() []Edge {
	out := []Edge{}
	for _, r := range s.List() {
		for _, d := range r.Dependencies {
			out = append(out, Edge{From: r.Address(), To: d})
		}
	}
	return out
}

// Snapshot is a whole-graph view for queries and export.
type Snapshot struct {
	Format    int                        `json:"format"`
	Lineage   string                     `json:"lineage"`
	Resources map[string]*Resource       `json:"resources"`
	Edges     []Edge                     `json:"edges"`
	Outputs   map[string]json.RawMessage `json:"outputs"`
}

// Snapshot returns the whole graph as one document.
func (s *Store) Snapshot() *Snapshot {
	snap := &Snapshot{Format: s.Format(), Lineage: s.Lineage(), Resources: map[string]*Resource{}, Edges: s.Edges(), Outputs: s.Outputs()}
	for _, r := range s.List() {
		snap.Resources[r.Address()] = r
	}
	if snap.Edges == nil {
		snap.Edges = []Edge{}
	}
	return snap
}

// --- locks ---

// Lock describes a held resource lock.
type Lock struct {
	Addr    string    `json:"addr"`
	PID     int       `json:"pid"`
	Host    string    `json:"host"`
	User    string    `json:"user,omitempty"`
	Command string    `json:"command"`
	Txn     string    `json:"txn,omitempty"`
	Nonce   string    `json:"nonce,omitempty"`
	Actor   string    `json:"actor,omitempty"`
	Since   time.Time `json:"since"`
	// Kind is "lock" (held by a running command) or "reservation" (taken by
	// a plan for a later apply by the same actor). Expires applies to
	// reservations.
	Kind    string     `json:"kind,omitempty"`
	Expires *time.Time `json:"expires,omitempty"`
	// RenewedAt is bumped by the holder's heartbeat; a lock not renewed for
	// StaleAfter may be reclaimed.
	RenewedAt time.Time `json:"renewed_at"`
	// Stale is set by Locks on a lock that may be reclaimed. It is not stored.
	Stale bool `json:"stale,omitempty"`
}

// Holder is the name a lock or reservation is shown and matched under: the
// actor label it was taken with, or the operating system user when there was
// none.
func (l Lock) Holder() string {
	if l.Actor != "" {
		return l.Actor
	}
	return l.User
}

// ErrLocked is returned when another process holds a resource lock.
type ErrLocked struct {
	Lock Lock
}

func (e *ErrLocked) Error() string {
	l := e.Lock
	who := l.Holder()
	if l.Kind == "reservation" {
		until := ""
		if l.Expires != nil {
			until = " until " + l.Expires.Local().Format(time.RFC3339)
		}
		return fmt.Sprintf("%s is reserved by %s (%s%s, since %s)", l.Addr, who, l.Command, until, l.Since.Local().Format(time.RFC3339))
	}
	return fmt.Sprintf("%s is locked by %s (pid %d on %s, %s, since %s)", l.Addr, l.Command, l.PID, l.Host, who, l.Since.Local().Format(time.RFC3339))
}

// osUser returns the name of the operating system user, or "" when it cannot
// be found.
func osUser() string {
	if cu, err := user.Current(); err == nil {
		return cu.Username
	}
	return ""
}

// Owner is the name under which this store's commands hold reservations: the
// actor label, or the operating system user when no label was given. Without
// it, the reservation of an unlabelled run would belong to no one that could
// match it again. Labels are not verified, so a label that equals a user name
// names the same owner as that user's unlabelled runs.
func (s *Store) Owner() string {
	if s.actor != "" {
		return s.actor
	}
	return osUser()
}

func (s *Store) newLock(addr, command, txn string) Lock {
	host, _ := os.Hostname()
	now := time.Now().UTC()
	return Lock{Addr: addr, PID: os.Getpid(), Host: host, User: osUser(), Command: command, Txn: txn, Nonce: s.nonce, Actor: s.actor, Since: now, RenewedAt: now, Kind: "lock"}
}

// stale reports whether a lock may be reclaimed: its process is gone on
// this host, or it has not been renewed for StaleAfter.
func (s *Store) stale(l Lock) bool {
	if l.Kind == "reservation" {
		// A reservation outlives the process that took it; only time ends it.
		return l.Expires == nil || time.Now().After(*l.Expires)
	}
	if s.b.Local() {
		host, _ := os.Hostname()
		if l.Host == host && l.PID != os.Getpid() && !processAlive(l.PID) {
			return true
		}
	}
	last := l.RenewedAt
	if last.IsZero() {
		last = l.Since
	}
	return !last.IsZero() && time.Since(last) > StaleAfter
}

// Lock takes the lock for addr on behalf of this process, reclaiming a
// stale one and taking over the reservation that this store's owner holds.
func (s *Store) Lock(addr, command, txn string) error {
	_, err := s.lock(addr, command, txn, false)
	return err
}

// deleteLock deletes the lock at addr when it is still the object that was read
// as raw, and reports whether it did. A lock that was judged gone and has been
// taken again since is another object and is left alone: of two processes that
// judge the same lock gone, one deletes it and the other deletes nothing.
func (s *Store) deleteLock(addr string, raw []byte) bool {
	ok, _ := s.b.DeleteIf(s.ctx, lockKey(addr), func(current []byte) bool { return bytes.Equal(current, raw) })
	return ok
}

// lock takes the lock for addr and reports whether it created the lock
// object. With keep, the live reservation of this store's owner is left as
// it is and counts as held without being taken over; a lock this process
// already holds counts as held in either case.
func (s *Store) lock(addr, command, txn string, keep bool) (bool, error) {
	l := s.newLock(addr, command, txn)
	data, _ := indentJSON(l)
	for attempt := 0; attempt < 3; attempt++ {
		created, err := s.b.PutIfAbsent(s.ctx, lockKey(addr), data)
		// A write of unknown outcome is not acquired, as a refusal is not, and the
		// lock is read: if the store holds the bytes written, this call took it.
		unknown := errors.Is(err, ErrUnknownOutcome)
		if err != nil && !unknown {
			return false, err
		}
		if created {
			return true, nil
		}
		raw, holder, rerr := s.readHolder(addr)
		if rerr != nil {
			if errors.Is(rerr, ErrNotFound) {
				continue // released between the two calls; try again
			}
			// Unreadable lock: treat as stale.
			s.deleteLock(addr, raw)
			continue
		}
		if holder.Nonce == s.nonce {
			// The nonce is this store's, and so is a lock that holds the bytes of
			// this write: it was created by this call, which must release it. The
			// bytes name this call, not this round: a create of unknown outcome that
			// was made is found by a later round, whose own create was refused.
			return bytes.Equal(raw, data), nil
		}
		stale := s.stale(holder)
		if keep && !stale && s.OwnsReservation(holder) {
			return false, nil
		}
		if stale || s.OwnsReservation(holder) {
			s.deleteLock(addr, raw)
			continue
		}
		return false, &ErrLocked{Lock: holder}
	}
	holder, err := s.readLock(addr)
	if err != nil {
		return false, fmt.Errorf("locking %s: %w", addr, err)
	}
	return false, &ErrLocked{Lock: holder}
}

// Conflict returns the error that Lock returns for addr, or nil when Lock
// would take the lock, and takes nothing.
func (s *Store) Conflict(addr string) error {
	holder, err := s.readLock(addr)
	if err != nil || holder.Nonce == s.nonce || s.stale(holder) || s.OwnsReservation(holder) {
		return nil // free, unreadable (which Lock reclaims), ours or reclaimable
	}
	return &ErrLocked{Lock: holder}
}

// TxnRunning reports whether the transaction with the given id has been
// written with the status running: its command holds all its locks and has begun
// to apply. A lock whose transaction is not running belongs to a command that is
// still taking its locks, or to a short write of a record, or to a command that
// is gone.
func (s *Store) TxnRunning(id string) bool {
	if id == "" {
		return false
	}
	data, err := s.b.Get(s.ctx, "txn/"+id+".json")
	if err != nil {
		return false
	}
	var t struct {
		Status string `json:"status"`
	}
	return json.Unmarshal(data, &t) == nil && t.Status == "running"
}

// TryHold takes the lock for addr for the short write of a record and
// reports whether the caller may write. Unlike TryLock it does not end the
// reservation that this store's owner holds on addr: the reservation stays as
// it is, and the returned release function leaves it alone. A lock that
// another process holds, which means it is applying the node, is not taken
// and ok is false.
func (s *Store) TryHold(addr, command, txn string) (release func(), ok bool, err error) {
	created, err := s.lock(addr, command, txn, true)
	var locked *ErrLocked
	if errors.As(err, &locked) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !created {
		return func() {}, true, nil
	}
	return func() { _ = s.Unlock(addr) }, true, nil
}

// OwnsReservation reports whether l is a reservation of this store's owner,
// which the owner's own apply may take over and own plan may renew.
func (s *Store) OwnsReservation(l Lock) bool {
	owner := s.Owner()
	return l.Kind == "reservation" && owner != "" && l.Holder() == owner
}

// ReserveAll takes a reservation on every address until the given time, or
// on none: a declaration of intent that keeps other actors from planning or
// applying changes to the nodes, without a process having to stay alive. The
// owner's later apply takes a reservation over; it lapses by itself at
// expiry.
//
// A reservation the owner already holds is rewritten in place, so that the
// node is never without one, and it ends at the later of its own expiry and
// until: a reservation can be extended and not shortened, and the command
// recorded on it is the latest one. If any address cannot be reserved, the
// addresses done so far are restored to what the owner held on them before,
// a reservation or nothing, and the error is returned. On success the result
// maps each address to the end of its reservation.
func (s *Store) ReserveAll(addrs []string, command string, until time.Time) (map[string]time.Time, error) {
	type taken struct {
		addr string
		prev *Lock
	}
	var done []taken
	ends := map[string]time.Time{}
	for _, a := range addrs {
		end, prev, err := s.reserve(a, command, until)
		if err != nil {
			for _, d := range done {
				s.undoReserve(d.addr, d.prev)
			}
			return nil, err
		}
		done = append(done, taken{a, prev})
		ends[a] = end
	}
	return ends, nil
}

// reserve reserves one address. It returns the end of the reservation and the
// reservation of the owner that it replaced, if any.
func (s *Store) reserve(addr, command string, until time.Time) (time.Time, *Lock, error) {
	// maybe lists the writes of this call that the store may or may not have made,
	// from any round: the bytes written, the end of the reservation, and the
	// reservation it replaced, nil for a create. A round that finds one of them in
	// the store took the reservation, and the rounds after an unknown outcome and a
	// failed read must not take it for the reservation of an earlier command.
	type possible struct {
		data []byte
		end  time.Time
		prev *Lock
	}
	var maybe []possible
	for attempt := 0; attempt < 4; attempt++ {
		l := s.newLock(addr, command, "")
		l.Kind = "reservation"
		l.Expires = &until
		data, _ := indentJSON(l)
		created, err := s.b.PutIfAbsent(s.ctx, lockKey(addr), data)
		unknown := errors.Is(err, ErrUnknownOutcome)
		if err != nil && !unknown {
			return time.Time{}, nil, err
		}
		if created {
			return until, nil, nil
		}
		if unknown {
			maybe = append(maybe, possible{data, until, nil})
		}
		raw, holder, rerr := s.readHolder(addr)
		if rerr != nil {
			if errors.Is(rerr, ErrNotFound) {
				continue
			}
			s.deleteLock(addr, raw)
			continue
		}
		for _, m := range maybe {
			if bytes.Equal(raw, m.data) {
				return m.end, m.prev, nil // the create or the replacement was made
			}
		}
		stale := s.stale(holder)
		if !stale && s.OwnsReservation(holder) {
			end := until
			if holder.Expires != nil && holder.Expires.After(until) {
				end = *holder.Expires
			}
			l.Expires = &end
			if !holder.Since.IsZero() {
				l.Since = holder.Since
			}
			data, _ = indentJSON(l)
			// The reservation is replaced only if it is still the one that was read.
			ok, err := s.b.ReplaceIf(s.ctx, lockKey(addr), func(current []byte) ([]byte, bool) { return data, bytes.Equal(current, raw) })
			switch {
			case errors.Is(err, ErrNotFound):
				continue
			case errors.Is(err, ErrUnknownOutcome):
				// Read again, as after a refusal; the lock is this call's if it holds
				// these bytes, and then the reservation that it replaced is this one.
				maybe = append(maybe, possible{data, end, &holder})
				continue
			case err != nil:
				return time.Time{}, nil, err
			case !ok:
				continue
			}
			return end, &holder, nil
		}
		if holder.Nonce == s.nonce || stale || s.OwnsReservation(holder) {
			s.deleteLock(addr, raw)
			continue
		}
		return time.Time{}, nil, &ErrLocked{Lock: holder}
	}
	holder, err := s.readLock(addr)
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("reserving %s: %w", addr, err)
	}
	return time.Time{}, nil, &ErrLocked{Lock: holder}
}

// undoReserve puts addr back as it was before reserve: the reservation prev
// that was replaced, or no lock when there was none. It leaves alone a lock that
// is no longer this store's.
func (s *Store) undoReserve(addr string, prev *Lock) {
	if prev == nil {
		_, _ = s.Release(addr, false)
		return
	}
	data, _ := indentJSON(prev)
	_, _ = s.b.ReplaceIf(s.ctx, lockKey(addr), func(current []byte) ([]byte, bool) {
		var cur Lock
		return data, json.Unmarshal(current, &cur) == nil && cur.Nonce == s.nonce
	})
}

// Release removes a reservation held by this store's owner (or any lock, when
// force is set).
func (s *Store) Release(addr string, force bool) (bool, error) {
	raw, l, err := s.readLockRaw(addr)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if force {
		return true, s.b.Delete(s.ctx, lockKey(addr))
	}
	if !s.OwnsReservation(l) && l.Nonce != s.nonce {
		return false, &ErrLocked{Lock: l}
	}
	ok, err := s.b.DeleteIf(s.ctx, lockKey(addr), func(current []byte) bool { return bytes.Equal(current, raw) })
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return ok, err
}

// readHolder reads the lock at addr that another process holds, with the bytes it
// was read from. A backend that makes a lock exist before its contents are all
// there leaves a lock that cannot be read for a moment, so a lock that cannot be
// read is read again a few times before it is taken for a broken one. A lock is
// created whole by the local backend and by S3, and this is for a filesystem with
// no hard links.
func (s *Store) readHolder(addr string) ([]byte, Lock, error) {
	var (
		raw []byte
		l   Lock
		err error
	)
	for i := 1; i <= 5; i++ {
		if raw, l, err = s.readLockRaw(addr); err == nil || errors.Is(err, ErrNotFound) {
			break
		}
		time.Sleep(time.Duration(i) * time.Millisecond)
	}
	return raw, l, err
}

func (s *Store) readLock(addr string) (Lock, error) {
	_, l, err := s.readLockRaw(addr)
	return l, err
}

// readLockRaw reads the lock at addr and returns the bytes as well. The bytes
// are returned when the lock cannot be parsed.
func (s *Store) readLockRaw(addr string) ([]byte, Lock, error) {
	var l Lock
	data, err := s.b.Get(s.ctx, lockKey(addr))
	if err != nil {
		return nil, l, err
	}
	err = json.Unmarshal(data, &l)
	if l.Addr == "" {
		l.Addr = addr
	}
	return data, l, err
}

// Renew refreshes a lock this process holds (the heartbeat). The lock is written
// only if it is still this process's at the moment it is replaced, so a lock that
// another process reclaimed in the meantime is not written over.
func (s *Store) Renew(addr string) error {
	var (
		other  Lock
		broken error
	)
	ok, err := s.b.ReplaceIf(s.ctx, lockKey(addr), func(current []byte) ([]byte, bool) {
		var l Lock
		if err := json.Unmarshal(current, &l); err != nil {
			broken = err
			return nil, false
		}
		if l.Nonce != s.nonce {
			other = l
			return nil, false
		}
		l.RenewedAt = time.Now().UTC()
		data, _ := indentJSON(l)
		return data, true
	})
	switch {
	case err != nil:
		return err
	case ok:
		return nil
	case broken != nil:
		return fmt.Errorf("the lock on %s cannot be read: %w", addr, broken)
	}
	if other.Addr == "" {
		other.Addr = addr
	}
	return &ErrLocked{Lock: other}
}

// Unlock releases the lock for addr if this process holds it. A lock that
// another process holds, or that cannot be read, is left as it is.
func (s *Store) Unlock(addr string) error {
	var (
		other  Lock
		broken error
	)
	ok, err := s.b.DeleteIf(s.ctx, lockKey(addr), func(current []byte) bool {
		var l Lock
		if err := json.Unmarshal(current, &l); err != nil {
			broken = err
			return false
		}
		if l.Nonce != s.nonce {
			other = l
			return false
		}
		return true
	})
	switch {
	case errors.Is(err, ErrNotFound):
		return nil
	case err != nil:
		return err
	case ok:
		return nil
	case broken != nil:
		return fmt.Errorf("the lock on %s cannot be read: %w", addr, broken)
	case other.Nonce != "" || other.PID != 0:
		if other.Addr == "" {
			other.Addr = addr
		}
		return &ErrLocked{Lock: other}
	}
	return nil
}

// ForceUnlock releases the lock for addr whoever holds it.
func (s *Store) ForceUnlock(addr string) (bool, error) {
	_, err := s.readLock(addr)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return true, s.b.Delete(s.ctx, lockKey(addr))
}

// ReleaseStale releases every lock and reservation that may be reclaimed, and
// returns their addresses. A lock is deleted only if it is stale at the moment it
// is deleted: one that its holder renewed, or that another process took, since
// the listing is left alone.
func (s *Store) ReleaseStale() ([]string, error) {
	locks, err := s.Locks()
	if err != nil {
		return nil, err
	}
	var released []string
	for _, l := range locks {
		if !l.Stale {
			continue
		}
		ok, err := s.b.DeleteIf(s.ctx, lockKey(l.Addr), func(current []byte) bool {
			var cur Lock
			return json.Unmarshal(current, &cur) == nil && s.stale(cur)
		})
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return released, err
		}
		if ok {
			released = append(released, l.Addr)
		}
	}
	return released, nil
}

// TryLock takes the lock when it is free and reports whether it did.
func (s *Store) TryLock(addr, command, txn string) (bool, error) {
	err := s.Lock(addr, command, txn)
	var locked *ErrLocked
	if errors.As(err, &locked) {
		return false, nil
	}
	return err == nil, err
}

// Locks lists the locks currently held on the store. Stale is set on each lock
// that may be reclaimed: its process is gone on this host, it has not been
// renewed for StaleAfter, or it is a reservation that has expired.
func (s *Store) Locks() ([]Lock, error) {
	keys, err := s.b.List(s.ctx, "locks")
	if err != nil {
		return nil, err
	}
	out := []Lock{}
	for _, k := range keys {
		if !strings.HasSuffix(k, ".lock") {
			continue
		}
		data, err := s.b.Get(s.ctx, k)
		if err != nil {
			continue
		}
		var l Lock
		if json.Unmarshal(data, &l) == nil {
			l.Stale = s.stale(l)
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr < out[j].Addr })
	return out, nil
}

// Suspects returns, by address, the locks that may be reclaimed: the command that
// took each is gone, or has not renewed it for StaleAfter. A command that stops
// while it holds a lock may have changed the object, and the node's record says
// nothing about it. A reservation is not a suspect: it outlives its command by
// design. It lists the locks once, and reads the ones it finds; a store with no
// locks makes no read.
func (s *Store) Suspects() (map[string]Lock, error) {
	locks, err := s.Locks()
	if err != nil {
		return nil, err
	}
	out := map[string]Lock{}
	for _, l := range locks {
		if l.Stale && l.Kind != "reservation" {
			out[l.Addr] = l
		}
	}
	return out, nil
}

// Interrupted returns the ids of the transactions among txns that are recorded as
// running and whose command is gone: no lock that has not gone stale carries
// their id. A command that applies holds a lock for its transaction from before
// the transaction is written until after it is finished, so a running
// transaction without one was left by a command that was killed.
func (s *Store) Interrupted(txns []Transaction) (map[string]bool, error) {
	out := map[string]bool{}
	var running []string
	for _, t := range txns {
		if t.Status == "running" {
			running = append(running, t.ID)
		}
	}
	if len(running) == 0 {
		return out, nil
	}
	locks, err := s.Locks()
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, l := range locks {
		if !l.Stale && l.Txn != "" {
			live[l.Txn] = true
		}
	}
	for _, id := range running {
		if !live[id] {
			out[id] = true
		}
	}
	return out, nil
}

// --- transactions ---

// Change is one entry of a transaction.
type Change struct {
	Addr   string `json:"addr"`
	Action string `json:"action"`
	Before int    `json:"before_revision,omitempty"`
	After  int    `json:"after_revision,omitempty"`
	// Diff is what the change did to the attributes of the record. A sealed state
	// keeps it in Transaction.DiffSealed and leaves this empty.
	Diff *Diff `json:"diff,omitempty"`
}

// Transaction records one plan, apply, destroy or import run.
type Transaction struct {
	ID       string     `json:"id"`
	Kind     string     `json:"kind"`
	Command  string     `json:"command"`
	User     string     `json:"user,omitempty"`
	Host     string     `json:"host,omitempty"`
	Started  time.Time  `json:"started"`
	Finished *time.Time `json:"finished,omitempty"`
	Status   string     `json:"status"`
	Summary  string     `json:"summary,omitempty"`
	// SummarySealed holds the full text of the error of a run that failed or
	// was rejected, in a sealed state, where Summary says only that the text is
	// sealed. It is bound to the id of the transaction.
	SummarySealed *protect.Sealed `json:"summary_sealed,omitempty"`
	// DiffSealed holds, in a sealed state, the Diff of each change by address. It is
	// bound to the id of the transaction and is not covered by the signatures.
	DiffSealed *protect.Sealed `json:"diff_sealed,omitempty"`
	Changes    []Change        `json:"changes"`
	// Actor, Reason and GitRevision attribute the run: who (a person or
	// an agent), why, and which revision of the specification.
	Actor       string `json:"actor,omitempty"`
	Reason      string `json:"reason,omitempty"`
	GitRevision string `json:"git_revision,omitempty"`
	// TraceID links the transaction to its OpenTelemetry trace.
	TraceID string `json:"trace_id,omitempty"`
	// Version is the pravo that wrote the transaction. Transactions written
	// before it was recorded have none.
	Version string `json:"version,omitempty"`
	// PlanID and PlanDigest name the plan an apply ran from: the id of a stored plan,
	// and the SHA-256 of the plan file in hex. They are signed with the rest.
	PlanID     string `json:"plan,omitempty"`
	PlanDigest string `json:"plan_digest,omitempty"`
	// StartSig and FinishSig are the signatures of the identity that ran the command,
	// in a sealed state (see sign.go). The first covers what the run set out to do, and
	// the second what it did; a run that is killed leaves the first.
	StartSig  *protect.Signature `json:"start_sig,omitempty"`
	FinishSig *protect.Signature `json:"finish_sig,omitempty"`
}

// Txn is an in-progress transaction.
type Txn struct {
	store *Store
	mu    sync.Mutex
	t     Transaction
}

// PravoVersion is the version of the pravo that writes transactions. The program
// sets it when it starts; the state package cannot see the version of the
// program that links it. An empty PravoVersion leaves the field out. (Version is
// the name of the revision and generation of a record.)
var PravoVersion string

// Begin starts a transaction of the given kind (plan, apply, destroy, import, edit).
func (s *Store) Begin(kind, command string) *Txn {
	host, _ := os.Hostname()
	return &Txn{store: s, t: Transaction{
		ID: newTxnID(), Kind: kind, Command: command,
		User: osUser(), Host: host, Started: time.Now().UTC(), Status: "running", Changes: []Change{},
		Actor: s.actor, Reason: s.reason, GitRevision: s.gitRevision, TraceID: s.traceID, Version: PravoVersion,
	}}
}

// ID returns the transaction id.
func (t *Txn) ID() string { return t.t.ID }

// Start writes the transaction as it is, with the status running. A command
// that is killed after Start leaves a transaction that is still running; Finish
// replaces it.
func (t *Txn) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.store.writable(); err != nil {
		return err
	}
	// A sealed state records no run that its owner cannot be named for: nothing has
	// been changed yet, so a run that cannot sign is refused here.
	if err := t.signStart(); err != nil {
		return err
	}
	return t.store.putJSON("txn/"+t.t.ID+".json", t.t)
}

// Record adds a change to the transaction.
func (t *Txn) Record(addr, action string, before, after int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.t.Changes = append(t.t.Changes, Change{Addr: addr, Action: action, Before: before, After: after})
}

// Len returns the number of recorded changes.
func (t *Txn) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.t.Changes)
}

// Finish writes the transaction with the given status and summary. A
// transaction without changes is not recorded unless record is true.
func (t *Txn) Finish(status, summary string, record bool) error {
	return t.FinishError(status, summary, "", record)
}

// FinishError is Finish for a run that failed or was rejected, whose full text
// can hold a value that a provider returned. In a sealed state summary is what
// anyone who can read the bucket sees, and full is kept sealed for the readers
// (see Store.SummaryText); when the store holds no key, or the state is not
// sealed, full is dropped and summary is all there is. full is empty when there
// is nothing to keep.
func (t *Txn) FinishError(status, summary, full string, record bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.t.Changes) == 0 && !record {
		return nil
	}
	now := time.Now().UTC()
	t.t.Finished = &now
	t.t.Status = status
	t.t.Summary = summary
	t.t.SummarySealed = nil
	if sl := t.store.Sealer(); sl != nil && full != "" {
		if box, err := sl.Seal(protect.KindSummary, t.t.ID, []byte(full)); err == nil {
			t.t.SummarySealed = &box
		}
	}
	sort.Slice(t.t.Changes, func(i, j int) bool { return t.t.Changes[i].Addr < t.t.Changes[j].Addr })
	if t.store.sealedState() {
		t.store.sealDiffs(&t.t)
	}
	if err := t.store.writable(); err != nil {
		return err
	}
	// The cloud has been changed by now. A signature that cannot be made does not
	// lose the record of what happened: it is written without the signature, and the
	// error says so.
	signErr := t.signFinish()
	if err := t.store.putJSON("txn/"+t.t.ID+".json", t.t); err != nil {
		return err
	}
	if signErr != nil {
		// The command has done its work. It reports the signature that is missing and does not
		// fail for it, since a failure would have its caller treat the apply as not done.
		if warn := t.store.warnFunc(); warn != nil {
			warn(signErr.Error())
			return nil
		}
	}
	return signErr
}

// SealedErrorText is what the summary of a transaction that failed or was rejected
// says in a sealed state, in place of the text of the error. The text is kept
// sealed beside it (Transaction.SummarySealed), for the readers.
const SealedErrorText = "the text of the error is sealed to the readers of the state"

// SummaryText returns the summary of t as the holder of this store may read it:
// the full text of the error that a sealed state keeps for its readers when the
// store can open it, and the summary that anyone sees otherwise.
func (s *Store) SummaryText(t Transaction) string {
	sl := s.Sealer()
	if sl == nil || t.SummarySealed == nil {
		return t.Summary
	}
	full, err := sl.Open(protect.KindSummary, t.ID, *t.SummarySealed)
	if err != nil {
		return t.Summary
	}
	return string(full)
}

// resealSummaries seals the text of the errors that transactions keep sealed again
// under the current group, as the records are by a rewrap. A box that does not open
// is left as it is: a transaction is a log, and one that is damaged is not a reason
// to stop.
func (s *Store) resealSummaries(ctx context.Context, current string) error {
	sl := s.Sealer()
	keys, err := s.b.List(ctx, "txn")
	if err != nil {
		return err
	}
	for _, k := range keys {
		if !strings.HasSuffix(k, ".json") {
			continue
		}
		data, err := s.b.Get(ctx, k)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		var t Transaction
		if json.Unmarshal(data, &t) != nil {
			continue
		}
		changed := false
		for _, b := range []struct {
			box  **protect.Sealed
			kind string
		}{{&t.SummarySealed, protect.KindSummary}, {&t.DiffSealed, protect.KindDiff}} {
			if *b.box == nil || (*b.box).Group == current {
				continue
			}
			plain, err := sl.Open(b.kind, t.ID, **b.box)
			if err != nil {
				continue
			}
			box, err := sl.Seal(b.kind, t.ID, plain)
			clear(plain)
			if err != nil {
				return err
			}
			*b.box = &box
			changed = true
		}
		if !changed {
			continue
		}
		next, err := indentJSON(t)
		if err != nil {
			return err
		}
		if _, err := s.b.ReplaceIf(ctx, k, func(cur []byte) ([]byte, bool) { return next, bytes.Equal(cur, data) }); err != nil && !errors.Is(err, ErrNotFound) {
			return Advise(err)
		}
	}
	return nil
}

// Transactions lists recorded transactions, newest first.
func (s *Store) Transactions() ([]Transaction, error) {
	keys, err := s.b.List(s.ctx, "txn")
	if err != nil {
		return nil, err
	}
	out := []Transaction{}
	for _, k := range keys {
		if !strings.HasSuffix(k, ".json") {
			continue
		}
		data, err := s.b.Get(s.ctx, k)
		if err != nil {
			continue
		}
		var t Transaction
		// A transaction is in the object that has its id: a copy under another name is
		// not another transaction.
		if json.Unmarshal(data, &t) == nil && k == "txn/"+t.ID+".json" {
			if t.Changes == nil {
				t.Changes = []Change{}
			}
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out, nil
}

// Now returns the current UTC time as a pointer, for the timestamp fields.
func Now() *time.Time {
	t := time.Now().UTC()
	return &t
}

// jsonSame reports whether two JSON documents have the same content,
// whatever their formatting or key order.
func jsonSame(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
