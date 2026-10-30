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
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
	"github.com/pravo-systems/pravo/internal/telemetry"
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
	readWidthRemote = 256
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
	// roles is the chain of roles read last, and rolesAt when (see RolesChain).
	rolesMu sync.Mutex
	roles   *RolesChain
	rolesAt time.Time

	b Backend
	// ctx is the context of every call to the backend once the store is open. It
	// carries the values of the context the store was opened with, not its
	// cancellation.
	ctx context.Context
	// ctxMu guards ctx, which SetContext replaces while a command runs.
	ctxMu sync.RWMutex
	meta  meta

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
	// bindRecordsOn makes the transactions sign the records they write (BindRecords).
	bindRecordsOn bool
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

	actor, actorIgnored, reason, gitRevision, traceID string
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
	b = traced(b)
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
		if o.Identity == nil && o.Choose != nil {
			// Finding the identity can wait on an agent, a keychain or a passphrase typed by a person.
			_, end := telemetry.Span(ctx, "setup choose identity")
			id, err := o.Choose(s.Protection())
			end(err)
			if err == nil {
				o.Identity = id
			}
		}
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
	s.SetContext(ctx)
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
	if vl, ok := s.snapshotOn(); ok {
		objs, err := vl.ListVersions(s.cx(), "resources")
		if err != nil {
			return err
		}
		objs = slices.DeleteFunc(objs, func(o ObjectVersion) bool { return !strings.HasSuffix(o.Key, ".json") })
		if err := s.loadNodes(s.cx(), vl, objs); err != nil {
			return err
		}
		return s.loadOutputs()
	}
	keys, err := s.b.List(s.cx(), "resources")
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
	for range min(width, len(jsonKeys)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(jsonKeys) {
					return
				}
				r, h, err := s.readNode(s.cx(), jsonKeys[i])
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
	return s.loadOutputs()
}

// loadOutputs reads the outputs of the state.
func (s *Store) loadOutputs() error {
	data, err := s.b.Get(s.cx(), "outputs.json")
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

// SetActor records who is operating the store, for transactions and locks. In a
// sealed state the actor is the name of the identity that signs, and a label given
// here is dropped: a label anyone can type would sit beside a signature that says
// who really ran the command. ActorLabelIgnored says whether that happened.
func (s *Store) SetActor(actor, reason, gitRevision string) {
	s.reason, s.gitRevision = reason, gitRevision
	s.actorIgnored = ""
	if id := s.signingIdentity(); id != nil && id.Name != "" {
		if actor != "" && actor != id.Name {
			s.actorIgnored = actor
			if warn := s.warnFunc(); warn != nil {
				warn(fmt.Sprintf("--actor %q is ignored: this state is sealed, so the actor is %q, the identity that signs", actor, id.Name))
			}
		}
		actor = id.Name
	}
	s.actor = actor
}

// ActorLabelIgnored is the label that SetActor dropped in favour of the name of the
// signing identity, or "" when it dropped none.
func (s *Store) ActorLabelIgnored() string { return s.actorIgnored }

// cx is the context the store's requests to the backend run under.
func (s *Store) cx() context.Context {
	s.ctxMu.RLock()
	defer s.ctxMu.RUnlock()
	return s.ctx
}

// SetContext makes the requests the store sends to the backend from now on part
// of the trace in ctx, under its current span. Like the context the store was
// opened with, it does not end the requests: a command that is interrupted still
// records what it did.
func (s *Store) SetContext(ctx context.Context) {
	s.ctxMu.Lock()
	s.ctx = context.WithoutCancel(ctx)
	s.ctxMu.Unlock()
}

// Context is the context the store's requests run under (see SetContext).
func (s *Store) Context() context.Context { return s.cx() }

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
		case r > 0xFFFF:
			// Five or six digits after a "%" would read as four digits and a
			// character. A "~" is always escaped, so it opens no other form.
			fmt.Fprintf(&b, "~%X~", r)
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
	return s.b.Put(s.cx(), key, data)
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
	data, err := s.b.Get(s.cx(), nodeKey(addr))
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
	// The read of the pointer and the look at the seal do not depend on each other.
	sealed := make(chan struct{})
	go func() { defer close(sealed); s.recheckSeal() }()
	cur, found, err := s.head(addr)
	<-sealed
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

// outputsLockKey is the object an update of the outputs holds while it reads,
// merges and writes outputs.json.
const outputsLockKey = "outputs.lock"

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

// --- locks ---

// --- transactions ---

// PravoVersion is the version of the pravo that writes transactions. The program
// sets it when it starts; the state package cannot see the version of the
// program that links it. An empty PravoVersion leaves the field out. (Version is
// the name of the revision and generation of a record.)
var PravoVersion string

// SealedErrorText is what the summary of a transaction that failed or was rejected
// says in a sealed state, in place of the text of the error. The text is kept
// sealed beside it (Transaction.SummarySealed), for the readers.
const SealedErrorText = "the text of the error is sealed to the readers of the state"

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
