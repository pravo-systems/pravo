package state

// Format 3 seals what a record holds that is a value: its attributes, its private
// data, and the two digests of its configuration. The address, type, name,
// provider, dependencies, revision, generation and times stay readable, so that
// the graph, the plans and the viewer work for a reader that holds no key. The
// sealed part is one AES-256-GCM message under the data key of a reader group; the
// data key is locked for each reader in meta.json, under "sealed" (see
// internal/protect). What binds a sealed part to its place is the associated
// data: the lineage of the state, the group, the address, the revision and the
// generation of the record.
//
// meta.json of a sealed state:
//
//	{
//	  "format": 3, "lineage": "...", "created": "...",
//	  "sealed": {"alg": "...", "current": "g2", "groups": [...], "readers": "..."},
//	  "migration": {"to": 3, "started": "...", "by": "ada", "done": 120, "total": 2001},
//	  "history": [{"at": "...", "by": "ada", "what": "sealed", "added": ["ada", "dan"]}]
//	}
//
// "migration" is there only while a seal has begun and has not finished.
//
// config_hash and provider_config are sealed with the attributes. The first is
// a digest of the whole configuration, which a guess at a secret value confirms;
// the second is a digest of the configuration of the provider instance, from
// which values that are sensitive are left out, and which only a command that
// reads the state with a key uses. Nothing that reads without a key needs
// either, so both are sealed.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/pravo-systems/pravo/internal/protect"
)

// recordVersion and outputsVersion are the versions of the sealed record and of
// the sealed outputs object.
const (
	recordVersion  = 1
	outputsVersion = 1
)

// sealRecheck is how long a store goes on sealing in the group it opened with
// before it looks at meta.json again. A reader who is removed while a long apply
// runs gets a new group for the records written after the removal; the apply
// switches to it within this time.
var sealRecheck = 30 * time.Second

// historyKeep is how many entries of the history meta.json keeps.
const historyKeep = 50

// migrationMeta is the "migration" key of meta.json while a seal has begun and
// has not finished.
type migrationMeta struct {
	To      int       `json:"to"`
	Started time.Time `json:"started"`
	By      string    `json:"by,omitempty"`
	Done    int       `json:"done"`
	Total   int       `json:"total"`
	// Unseal is set while the seal is being taken off: the records are plain or
	// sealed, as the work has reached them, and writers are refused.
	Unseal bool `json:"unseal,omitempty"`
}

// historyMeta is one entry of the "history" key of meta.json.
type historyMeta struct {
	At      time.Time `json:"at"`
	By      string    `json:"by,omitempty"`
	What    string    `json:"what"`
	Added   []string  `json:"added,omitempty"`
	Removed []string  `json:"removed,omitempty"`
}

// sealedRecord is a record of format 3 as it is stored. The fields that shadow
// those of Resource are empty on a record that is sealed, and so are left out;
// a record that is not sealed yet, in a state that is being sealed, has them.
type sealedRecord struct {
	Resource
	Attributes     json.RawMessage `json:"attributes,omitempty"`
	Private        []byte          `json:"private,omitempty"`
	ConfigHash     string          `json:"config_hash,omitempty"`
	ProviderConfig string          `json:"provider_config,omitempty"`
	V              int             `json:"v,omitempty"`
	Sealed         *protect.Sealed `json:"sealed,omitempty"`
}

// sealedBody is what a record seals.
type sealedBody struct {
	Attributes     json.RawMessage `json:"attributes"`
	Private        []byte          `json:"private,omitempty"`
	ConfigHash     string          `json:"config_hash,omitempty"`
	ProviderConfig string          `json:"provider_config,omitempty"`
}

// marshalBody encodes v as compact JSON without escaping <, > and &.
func marshalBody(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// sealedState reports whether the state is sealed, or being sealed.
func (s *Store) sealedState() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta.Sealed != nil
}

// mayHoldPlain reports whether a record of this state may be one that is not
// sealed: only while a seal has begun and has not finished. After that every
// record is sealed, and one that is not was written by someone who holds no key.
func (s *Store) mayHoldPlain() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta.Migration != nil
}

// openSealed unlocks the data keys of a sealed state with the identity. A store
// that has no identity, or whose identity is not a reader, is locked.
func (s *Store) openSealed(id *protect.Identity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.meta.Sealed == nil {
		return fmt.Errorf("the state at %s is format %d and has no sealed part in meta.json; it is damaged: %w", s.b.Name(), s.meta.Format, ErrUntrusted)
	}
	if err := s.meta.Sealed.Check(); err != nil {
		return fmt.Errorf("%s: %v: %w", s.b.Name(), err, ErrUntrusted)
	}
	s.id, s.locked, s.openable = id, true, nil
	s.ring.set(nil)
	s.metaRead = time.Now()
	if id == nil || id.Reader == nil {
		s.you = You{State: "no-identity"}
		return nil
	}
	pub := id.Reader.Public()
	s.you = You{Name: id.Name, Key: pub.String()}
	keys, err := s.meta.Sealed.Unlock(s.meta.Lineage, id.Reader)
	switch {
	case err == nil:
		s.ring.set(keys)
		s.locked, s.you.State = false, "reader"
		s.openable = map[string]bool{}
		for _, g := range s.meta.Sealed.Groups {
			for _, l := range g.Locks {
				if l.Key.Equal(pub) {
					s.openable[g.ID] = true
				}
			}
		}
	case errors.Is(err, protect.ErrNotReader):
		s.you.State = "not-reader"
	default:
		return fmt.Errorf("opening the sealed state at %s: %w", s.b.Name(), err)
	}
	return nil
}

// groupListed reports whether meta.json lists the group. The caller holds s.mu.
// A group that is not listed holds no lock for anyone, so a value sealed in it
// is not one that a key could be missing for.
func (s *Store) groupListed(group string) bool {
	if s.meta.Sealed == nil {
		return false
	}
	for _, g := range s.meta.Sealed.Groups {
		if g.ID == group {
			return true
		}
	}
	return false
}

// groupReloadGap is the least time between two readings of meta.json that a store
// makes because a value is sealed in a group it does not know.
const groupReloadGap = time.Second

// sealedGroup says what the store holds of the group a value is sealed in:
// whether it holds any key, whether it holds the key of the group, and whether
// meta.json lists the group at all. A group that is not
// listed may have been made after this store read meta.json, by a rewrap in
// another process, which writes meta.json before it seals anything in the new
// group. So meta.json is read again before a group is called unknown.
func (s *Store) sealedGroup(group string) (keyed, openable, listed bool) {
	s.mu.Lock()
	listed = s.groupListed(group)
	s.mu.Unlock()
	if !listed {
		listed = s.listedAfterReload(group)
	}
	s.mu.Lock()
	openable = s.openable[group]
	s.mu.Unlock()
	return s.ring.has(), openable, listed
}

// listedAfterReload reads meta.json again, unless it did a moment ago, and
// reports whether it lists the group. One goroutine reads for all that wait.
func (s *Store) listedAfterReload(group string) bool {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	s.mu.Lock()
	listed, recent := s.groupListed(group), time.Since(s.groupReload) < groupReloadGap
	s.mu.Unlock()
	if listed || recent {
		return listed
	}
	s.reloadSealed()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groupReload = time.Now()
	return s.groupListed(group)
}

// reloadSealed reads meta.json and, if it is the meta.json of this state and has
// a sealed part that holds together, takes the groups and the history from it, and
// the data keys the identity of the store holds in it. A store whose identity has
// no lock in it any more keeps the keys it has: it can read what it could. A
// meta.json that cannot be used changes nothing.
func (s *Store) reloadSealed() {
	s.mu.Lock()
	id, lineage := s.id, s.meta.Lineage
	s.mu.Unlock()
	data, err := s.b.Get(s.ctx, "meta.json")
	if err != nil {
		return
	}
	var m meta
	if json.Unmarshal(data, &m) != nil || m.Lineage != lineage || m.Format < FormatSealed || m.Sealed == nil || m.Sealed.Check() != nil {
		return
	}
	var (
		keys     *protect.Keys
		openable map[string]bool
	)
	if id != nil && id.Reader != nil {
		k, err := m.Sealed.Unlock(lineage, id.Reader)
		switch {
		case err == nil:
			keys, openable = k, openableGroups(m.Sealed, id)
		case !errors.Is(err, protect.ErrNotReader):
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta.Sealed, s.meta.History = m.Sealed, m.History
	switch {
	case keys == nil:
	case s.closed:
		keys.Close() // a closed store does not take keys again
	default:
		s.ring.set(keys)
		s.openable = openable
	}
}

// recordCtx says where the sealed part of a record belongs.
func (s *Store) recordCtx(r *Resource) (protect.Ctx, error) {
	if r.Revision < 0 {
		return protect.Ctx{}, fmt.Errorf("%s: the revision %d is negative: %w", r.Address(), r.Revision, ErrUntrusted)
	}
	return protect.Ctx{Lineage: s.Lineage(), Kind: protect.KindRecord, Name: r.Address(), Generation: r.Generation, Revision: uint64(r.Revision)}, nil
}

// decodeRecord decodes the bytes of an immutable record and returns the group it
// is sealed in, empty for a record that is not sealed. A store that holds no key
// returns the readable part of a sealed record.
func (s *Store) decodeRecord(data []byte) (*Resource, string, error) {
	if !s.sealedState() {
		var r Resource
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, "", err
		}
		return &r, "", nil
	}
	var sr sealedRecord
	if err := json.Unmarshal(data, &sr); err != nil {
		return nil, "", fmt.Errorf("a record of a sealed state cannot be read (%v): %w", err, ErrUntrusted)
	}
	r := sr.Resource
	if sr.Sealed == nil {
		if !s.mayHoldPlain() {
			return nil, "", fmt.Errorf("%s is not sealed, and the state is: %w", r.Address(), ErrUntrusted)
		}
		r.Attributes, r.Private, r.ConfigHash, r.ProviderConfig = sr.Attributes, sr.Private, sr.ConfigHash, sr.ProviderConfig
		return &r, "", nil
	}
	if sr.V != recordVersion {
		return nil, "", fmt.Errorf("%s is a sealed record of version %d; this pravo reads version %d", r.Address(), sr.V, recordVersion)
	}
	group := sr.Sealed.Group
	keyed, openable, known := s.sealedGroup(group)
	if !known {
		return nil, "", fmt.Errorf("%s is sealed in group %q, which meta.json does not list: %w", r.Address(), group, ErrUntrusted)
	}
	if !keyed {
		return &r, group, nil
	}
	if !openable {
		return nil, "", fmt.Errorf("%s is sealed in group %s, which your key cannot open: %w", r.Address(), group, ErrNotReader)
	}
	c, err := s.recordCtx(&r)
	if err != nil {
		return nil, "", err
	}
	var body sealedBody
	if err := s.ring.openJSON(c, *sr.Sealed, &body); err != nil {
		return nil, "", fmt.Errorf("the sealed part of %s does not open where it is: %w", r.Address(), ErrUntrusted)
	}
	r.Attributes, r.Private, r.ConfigHash, r.ProviderConfig = body.Attributes, body.Private, body.ConfigHash, body.ProviderConfig
	return &r, group, nil
}

// encodeRecord returns the bytes of the immutable record of r, sealed when the
// state is, and the group it is sealed in.
func (s *Store) encodeRecord(r *Resource) ([]byte, string, error) {
	if !s.sealedState() {
		data, err := indentJSON(r)
		return data, "", err
	}
	if !s.ring.has() {
		return nil, "", ErrNotReader
	}
	c, err := s.recordCtx(r)
	if err != nil {
		return nil, "", err
	}
	plain, err := marshalBody(sealedBody{Attributes: r.Attributes, Private: r.Private, ConfigHash: r.ConfigHash, ProviderConfig: r.ProviderConfig})
	if err != nil {
		return nil, "", err
	}
	defer clear(plain)
	sealed, ok := s.ring.seal(c, plain)
	if !ok {
		return nil, "", ErrNotReader
	}
	open := *r
	open.Attributes, open.Private, open.ConfigHash, open.ProviderConfig = nil, nil, "", ""
	data, err := indentJSON(sealedRecord{Resource: open, V: recordVersion, Sealed: &sealed})
	return data, sealed.Group, err
}

// recheckSeal looks at meta.json again if the store has not for sealRecheck, and
// switches to the current group when a rewrap made a new one. It is best effort:
// a store that cannot read meta.json, or whose key has no lock in the new group,
// goes on sealing in the group it has.
func (s *Store) recheckSeal() {
	s.mu.Lock()
	if !s.ring.has() || time.Since(s.metaRead) < sealRecheck {
		s.mu.Unlock()
		return
	}
	s.metaRead = time.Now()
	id, lineage, current := s.id, s.meta.Lineage, s.ring.current()
	s.mu.Unlock()
	data, err := s.b.Get(s.ctx, "meta.json")
	if err != nil {
		return
	}
	var m meta
	if json.Unmarshal(data, &m) != nil || m.Lineage != lineage || m.Sealed == nil || m.Sealed.Current == current {
		return
	}
	keys, err := m.Sealed.Unlock(lineage, id.Reader)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		keys.Close()
		return
	}
	s.ring.set(keys)
	s.meta.Sealed, s.meta.History = m.Sealed, m.History
	s.openable = openableGroups(m.Sealed, id)
}

// openableGroups lists the groups of the sealed part that the identity holds a
// lock in.
func openableGroups(m *protect.Meta, id *protect.Identity) map[string]bool {
	out := map[string]bool{}
	pub := id.Reader.Public()
	for _, g := range m.Groups {
		for _, l := range g.Locks {
			if l.Key.Equal(pub) {
				out[g.ID] = true
			}
		}
	}
	return out
}

// sealedOutputs is outputs.json of a sealed state.
type sealedOutputs struct {
	V      int            `json:"v"`
	Sealed protect.Sealed `json:"sealed"`
}

func (s *Store) outputsCtx() protect.Ctx {
	return protect.Ctx{Lineage: s.Lineage(), Kind: protect.KindOutputs}
}

// asSealedOutputs reports whether data is the object that a sealed state keeps its
// outputs in: exactly the keys v and sealed, where the second has the three keys
// of a sealed value.
func asSealedOutputs(data []byte) (sealedOutputs, bool) {
	var keys map[string]json.RawMessage
	if json.Unmarshal(data, &keys) != nil || len(keys) != 2 || keys["v"] == nil || keys["sealed"] == nil {
		return sealedOutputs{}, false
	}
	var env sealedOutputs
	if json.Unmarshal(data, &env) != nil || env.V != outputsVersion || env.Sealed.Alg == "" || len(env.Sealed.CT) == 0 || env.Sealed.Group == "" {
		return sealedOutputs{}, false
	}
	return env, true
}

// decodeOutputs reads the bytes of outputs.json. A sealed object in a state this
// store holds no key for is ErrNotReader.
func (s *Store) decodeOutputs(data []byte) (map[string]json.RawMessage, error) {
	if s.sealedState() {
		if env, ok := asSealedOutputs(data); ok {
			keyed, openable, known := s.sealedGroup(env.Sealed.Group)
			if !known {
				return nil, fmt.Errorf("the outputs are sealed in group %q, which meta.json does not list: %w", env.Sealed.Group, ErrUntrusted)
			}
			if !keyed {
				return nil, fmt.Errorf("the outputs: %w", ErrNotReader)
			}
			if !openable {
				return nil, fmt.Errorf("the outputs are sealed in group %s, which your key cannot open: %w", env.Sealed.Group, ErrNotReader)
			}
			var out map[string]json.RawMessage
			if err := s.ring.openJSON(s.outputsCtx(), env.Sealed, &out); err != nil {
				return nil, fmt.Errorf("the sealed outputs do not open where they are: %w", ErrUntrusted)
			}
			return out, nil
		}
		if !s.mayHoldPlain() {
			return nil, fmt.Errorf("the outputs are not sealed, and the state is: %w", ErrUntrusted)
		}
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("reading outputs: %w", err)
	}
	return out, nil
}

// encodeOutputs returns the bytes of outputs.json: the outputs as they are, or
// sealed.
func (s *Store) encodeOutputs(outputs map[string]json.RawMessage) ([]byte, error) {
	if !s.sealedState() {
		return indentJSON(outputs)
	}
	s.recheckSeal()
	if !s.ring.has() {
		return nil, ErrNotReader
	}
	plain, err := marshalBody(outputs)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	sealed, ok := s.ring.seal(s.outputsCtx(), plain)
	if !ok {
		return nil, ErrNotReader
	}
	return indentJSON(sealedOutputs{V: outputsVersion, Sealed: sealed})
}

// storeSealer is the Sealer of a store.
type storeSealer struct{ s *Store }

// Sealer returns the sealer of the state, or nil when the state is not sealed.
// A locked store returns a sealer whose Seal and Open fail with ErrNotReader.
func (s *Store) Sealer() Sealer {
	if !s.sealedState() {
		return nil
	}
	return storeSealer{s}
}

func (z storeSealer) Seal(kind, name string, plain []byte) (protect.Sealed, error) {
	z.s.recheckSeal()
	sealed, ok := z.s.ring.seal(protect.Ctx{Lineage: z.s.Lineage(), Kind: kind, Name: name}, plain)
	if !ok {
		return protect.Sealed{}, ErrNotReader
	}
	return sealed, nil
}

func (z storeSealer) Open(kind, name string, sealed protect.Sealed) ([]byte, error) {
	keyed, openable, _ := z.s.sealedGroup(sealed.Group)
	if !keyed {
		return nil, ErrNotReader
	}
	if !openable {
		return nil, fmt.Errorf("sealed in group %s, which your key cannot open: %w", sealed.Group, ErrNotReader)
	}
	plain, err := z.s.ring.open(protect.Ctx{Lineage: z.s.Lineage(), Kind: kind, Name: name}, sealed)
	if err != nil {
		return nil, fmt.Errorf("a sealed %s does not open where it is: %w", kind, ErrUntrusted)
	}
	return plain, nil
}

// Protection returns how the state is protected.
func (s *Store) Protection() Protection {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := Protection{State: "off", Format: s.meta.Format, Location: s.b.Name(), Records: len(s.nodes)}
	switch {
	case s.meta.Sealed != nil && s.meta.Migration != nil && s.meta.Migration.Unseal:
		p.State = "unsealing"
	case s.meta.Sealed != nil && s.meta.Migration != nil:
		p.State = "migrating"
	case s.meta.Sealed != nil:
		p.State = "sealed"
	case s.meta.Format >= FormatImmutable:
		p.State = "immutable"
	}
	if s.meta.Sealed == nil {
		return p
	}
	sealed := *s.meta.Sealed
	sealed.Groups = slices.Clone(sealed.Groups)
	p.Sealed = &sealed
	p.RecoveryUnverified = s.ring.verifyRecovery(&sealed)
	p.ByGroup = map[string]int{}
	for _, ref := range s.held {
		switch {
		case ref.Group == "":
			p.Plain++
		default:
			p.ByGroup[ref.Group]++
			if !s.openable[ref.Group] {
				p.Unreadable++
			}
		}
	}
	if m := s.meta.Migration; m != nil {
		p.Migration = &Migration{Started: m.Started, By: m.By, Done: m.Done, Total: m.Total, Unseal: m.Unseal}
	}
	for _, h := range s.meta.History {
		p.History = append(p.History, HistoryEntry{At: h.At, By: h.By, What: h.What, Added: h.Added, Removed: h.Removed})
	}
	p.You = s.you
	return p
}

// OutputsReaders returns the reader keys that have a lock in the current group of
// the state kept in b, which its meta.json holds in the clear: the readers a
// caller who has several identities to choose from can choose among. It is nil,
// with no error, for a state that is not sealed or has no meta.json yet. It
// reads that one object and creates nothing, as OpenOutputs does.
func OutputsReaders(ctx context.Context, b Backend) ([]protect.ReaderPublic, error) {
	m, err := readMeta(ctx, b)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	case m.Sealed == nil:
		return nil, nil
	}
	var keys []protect.ReaderPublic
	for _, g := range m.Sealed.Groups {
		if g.ID != m.Sealed.Current {
			continue
		}
		for _, l := range g.Locks {
			if !l.Key.IsZero() && !m.Sealed.IsRecoveryLock(l) {
				keys = append(keys, l.Key)
			}
		}
	}
	return keys, nil
}

// OpenOutputs reads the outputs.json of another state with an identity. It is
// ReadOutputs for a state that may be sealed.
func OpenOutputs(ctx context.Context, b Backend, id *protect.Identity) (map[string]json.RawMessage, error) {
	data, err := b.Get(ctx, "outputs.json")
	if errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("%s has no recorded outputs; apply that root first", b.Name())
	}
	if err != nil {
		return nil, err
	}
	env, ok := asSealedOutputs(data)
	if !ok {
		// A state that is sealed, and not being sealed, holds no outputs in plain
		// text: whoever wrote this object holds no key.
		m, err := readMeta(ctx, b)
		switch {
		case errors.Is(err, ErrNotFound):
		case err != nil:
			return nil, fmt.Errorf("reading the outputs of %s: %w", b.Name(), err)
		case m.Sealed != nil && m.Migration == nil:
			return nil, fmt.Errorf("the outputs of %s are not sealed, and the state is: %w", b.Name(), ErrUntrusted)
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("reading outputs of %s: %w", b.Name(), err)
		}
		return out, nil
	}
	m, err := readMeta(ctx, b)
	if err != nil {
		return nil, fmt.Errorf("reading the outputs of %s: %w", b.Name(), err)
	}
	if m.Sealed == nil {
		return nil, fmt.Errorf("the outputs of %s are sealed and its meta.json says they are not: %w", b.Name(), ErrUntrusted)
	}
	if id == nil || id.Reader == nil {
		return nil, fmt.Errorf("the outputs of %s are sealed and no identity was given: %w", b.Name(), ErrNotReader)
	}
	keys, err := m.Sealed.Unlock(m.Lineage, id.Reader)
	if errors.Is(err, protect.ErrNotReader) {
		return nil, fmt.Errorf("the outputs of %s are sealed to other readers; identity %s is not one of them: %w", b.Name(), id.Name, ErrNotReader)
	}
	if errors.Is(err, protect.ErrDamaged) {
		return nil, fmt.Errorf("the outputs of %s: %v: %w", b.Name(), err, ErrUntrusted)
	}
	if err != nil {
		return nil, fmt.Errorf("the outputs of %s: %w", b.Name(), err)
	}
	defer keys.Close()
	var out map[string]json.RawMessage
	c := protect.Ctx{Lineage: m.Lineage, Kind: protect.KindOutputs}
	if err := keys.OpenJSON(c, env.Sealed, &out); err != nil {
		return nil, fmt.Errorf("the sealed outputs of %s do not open where they are: %w", b.Name(), ErrUntrusted)
	}
	return out, nil
}
