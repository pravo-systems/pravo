package schema

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
)

// A provider schema is tens of megabytes of JSON for a large provider, and a
// run looks at a few dozen of its thousands of types. The cache keeps it as a
// pack: an index of the types, then each type as its own JSON document, so that
// a type is read and decoded when it is first asked for and the rest are never
// read.
//
//	"PRAVO-SCHEMA-PACK 1\n"
//	uint32, big endian: the length N of the index
//	N bytes: the index, JSON
//	the documents, one after another
//
// The index gives the offset and the length of each document, from the start of
// the documents, for the provider block, the provider_meta block and each
// resource, data source, ephemeral resource and function.
const packMagic = "PRAVO-SCHEMA-PACK 1\n"

// maxPackIndex bounds what a damaged file can make the reader allocate.
const maxPackIndex = 64 << 20

// packRange is the offset and the length of one document.
type packRange [2]int64

type packIndex struct {
	CacheVersion int                  `json:"cache_version"`
	Provider     *packRange           `json:"provider,omitempty"`
	ProviderMeta *packRange           `json:"provider_meta,omitempty"`
	Resources    map[string]packRange `json:"resources"`
	DataSources  map[string]packRange `json:"data_sources"`
	Ephemerals   map[string]packRange `json:"ephemerals"`
	Functions    map[string]packRange `json:"functions"`
	// ShadowsBuiltins is what shadowsBuiltins says of the whole schema, which the
	// generated CUE depends on and which would otherwise need every type.
	ShadowsBuiltins bool `json:"shadows_builtins,omitempty"`
}

// kind says which set of types a name is in.
type kind int

const (
	kindResource kind = iota
	kindData
	kindEphemeral
	kindFunction
)

// lazyPack is the open pack that a schema reads its types from.
type lazyPack struct {
	mu    sync.Mutex
	f     *os.File
	base  int64 // where the documents start
	index packIndex
}

func (l *lazyPack) ranges(k kind) map[string]packRange {
	switch k {
	case kindData:
		return l.index.DataSources
	case kindEphemeral:
		return l.index.Ephemerals
	case kindFunction:
		return l.index.Functions
	}
	return l.index.Resources
}

// read returns the document at r.
func (l *lazyPack) read(r packRange) ([]byte, error) {
	if r[0] < 0 || r[1] < 0 {
		return nil, errors.New("a range of the schema pack is negative")
	}
	buf := make([]byte, r[1])
	if _, err := l.f.ReadAt(buf, l.base+r[0]); err != nil && !(errors.Is(err, io.EOF) && int64(len(buf)) == r[1]) {
		return nil, err
	}
	return buf, nil
}

// MarshalPack encodes the schema as a pack.
func (p *ProviderSchema) MarshalPack() ([]byte, error) {
	if err := p.Materialize(); err != nil {
		return nil, err
	}
	var payload bytes.Buffer
	add := func(v any) (packRange, error) {
		b, err := json.Marshal(v)
		if err != nil {
			return packRange{}, err
		}
		r := packRange{int64(payload.Len()), int64(len(b))}
		payload.Write(b)
		return r, nil
	}
	idx := packIndex{
		CacheVersion: CacheVersion,
		Resources:    map[string]packRange{}, DataSources: map[string]packRange{},
		Ephemerals: map[string]packRange{}, Functions: map[string]packRange{},
	}
	if p.Provider != nil {
		r, err := add(p.Provider)
		if err != nil {
			return nil, err
		}
		idx.Provider = &r
	}
	if p.ProviderMeta != nil {
		r, err := add(p.ProviderMeta)
		if err != nil {
			return nil, err
		}
		idx.ProviderMeta = &r
	}
	for _, set := range []struct {
		names []string
		into  map[string]packRange
		get   func(string) any
	}{
		{sortedKeys(p.Resources), idx.Resources, func(n string) any { return p.Resources[n] }},
		{sortedKeys(p.DataSources), idx.DataSources, func(n string) any { return p.DataSources[n] }},
		{sortedKeys(p.Ephemerals), idx.Ephemerals, func(n string) any { return p.Ephemerals[n] }},
		{p.SortedFunctionNames(), idx.Functions, func(n string) any { return p.Functions[n] }},
	} {
		for _, n := range set.names {
			r, err := add(set.get(n))
			if err != nil {
				return nil, err
			}
			set.into[n] = r
		}
	}
	idx.ShadowsBuiltins = shadowsBuiltins(p)
	head, err := json.Marshal(idx)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.Grow(len(packMagic) + 4 + len(head) + payload.Len())
	out.WriteString(packMagic)
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(head)))
	out.Write(n[:])
	out.Write(head)
	out.Write(payload.Bytes())
	return out.Bytes(), nil
}

// OpenPack opens a pack. The provider block is decoded now and each type when
// it is first asked for, through Resource, DataSource, Ephemeral and Function.
// It returns ErrStaleCache for a pack that an older pravo wrote.
func OpenPack(path string) (*ProviderSchema, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
		}
	}()
	head := make([]byte, len(packMagic)+4)
	if _, err := io.ReadFull(f, head); err != nil || string(head[:len(packMagic)]) != packMagic {
		return nil, fmt.Errorf("%s is not a provider schema pack", path)
	}
	n := binary.BigEndian.Uint32(head[len(packMagic):])
	if n > maxPackIndex {
		return nil, fmt.Errorf("%s: the index of the schema pack is too long", path)
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(f, raw); err != nil {
		return nil, fmt.Errorf("%s: reading the index of the schema pack: %w", path, err)
	}
	l := &lazyPack{f: f, base: int64(len(head)) + int64(n)}
	if err := json.Unmarshal(raw, &l.index); err != nil {
		return nil, fmt.Errorf("%s: decoding the index of the schema pack: %w", path, err)
	}
	if l.index.CacheVersion < CacheVersion {
		return nil, ErrStaleCache
	}
	p := &ProviderSchema{
		CacheVersion: l.index.CacheVersion,
		Resources:    map[string]*Schema{}, DataSources: map[string]*Schema{},
		Ephemerals: map[string]*Schema{}, Functions: map[string]*Function{},
		lazy: l,
	}
	for _, blk := range []struct {
		r  *packRange
		to **Schema
	}{{l.index.Provider, &p.Provider}, {l.index.ProviderMeta, &p.ProviderMeta}} {
		if blk.r == nil {
			continue
		}
		b, err := l.read(*blk.r)
		if err != nil {
			return nil, fmt.Errorf("%s: reading the schema pack: %w", path, err)
		}
		var s Schema
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("%s: decoding the schema pack: %w", path, err)
		}
		if s.Block != nil {
			s.Block.Type()
		}
		*blk.to = &s
	}
	ok = true
	return p, nil
}

// Resource returns the schema of a managed resource type, or nil.
func (p *ProviderSchema) Resource(name string) *Schema { return p.schemaOf(kindResource, name) }

// DataSource returns the schema of a data source type, or nil.
func (p *ProviderSchema) DataSource(name string) *Schema { return p.schemaOf(kindData, name) }

// Ephemeral returns the schema of an ephemeral resource type, or nil.
func (p *ProviderSchema) Ephemeral(name string) *Schema { return p.schemaOf(kindEphemeral, name) }

// Function returns a provider-defined function, or nil.
func (p *ProviderSchema) Function(name string) *Function {
	if p.lazy == nil {
		return p.Functions[name]
	}
	l := p.lazy
	l.mu.Lock()
	defer l.mu.Unlock()
	if f, ok := p.Functions[name]; ok {
		return f
	}
	r, ok := l.index.Functions[name]
	if !ok {
		return nil
	}
	b, err := l.read(r)
	if err != nil {
		return nil
	}
	var f Function
	if json.Unmarshal(b, &f) != nil {
		return nil
	}
	p.Functions[name] = &f
	return &f
}

func (p *ProviderSchema) schemaOf(k kind, name string) *Schema {
	m := p.setOf(k)
	if p.lazy == nil {
		return m[name]
	}
	l := p.lazy
	l.mu.Lock()
	defer l.mu.Unlock()
	if s, ok := m[name]; ok {
		return s
	}
	r, ok := l.ranges(k)[name]
	if !ok {
		return nil
	}
	b, err := l.read(r)
	if err != nil {
		return nil
	}
	var s Schema
	if json.Unmarshal(b, &s) != nil || s.Block == nil {
		return nil
	}
	// The types of the attributes are worked out on first use and kept. Work them
	// out now, before the schema is shared between tasks.
	s.Block.Type()
	m[name] = &s
	return &s
}

func (p *ProviderSchema) setOf(k kind) map[string]*Schema {
	switch k {
	case kindData:
		return p.DataSources
	case kindEphemeral:
		return p.Ephemerals
	}
	return p.Resources
}

// names returns the names of the types of a kind, in order.
func (p *ProviderSchema) names(k kind) []string {
	if p.lazy != nil {
		p.lazy.mu.Lock()
		defer p.lazy.mu.Unlock()
		r := p.lazy.ranges(k)
		out := make([]string, 0, len(r))
		for n := range r {
			out = append(out, n)
		}
		sort.Strings(out)
		return out
	}
	if k == kindFunction {
		out := make([]string, 0, len(p.Functions))
		for n := range p.Functions {
			out = append(out, n)
		}
		sort.Strings(out)
		return out
	}
	return sortedKeys(p.setOf(k))
}

// AllResources returns every managed resource type, decoding the ones that are
// not yet.
func (p *ProviderSchema) AllResources() map[string]*Schema {
	if p.lazy == nil {
		return p.Resources
	}
	for _, n := range p.names(kindResource) {
		p.schemaOf(kindResource, n)
	}
	p.lazy.mu.Lock()
	defer p.lazy.mu.Unlock()
	out := make(map[string]*Schema, len(p.Resources))
	for n, s := range p.Resources {
		out[n] = s
	}
	return out
}

// Materialize decodes every type that is not decoded yet, so that the maps of
// the schema hold all of them. It is for what works on the whole schema: sorting
// it, encoding it, and the code that lists it.
func (p *ProviderSchema) Materialize() error {
	if p.lazy == nil {
		return nil
	}
	for _, n := range p.names(kindResource) {
		if p.schemaOf(kindResource, n) == nil {
			return fmt.Errorf("decoding resource type %s of the schema pack", n)
		}
	}
	for _, n := range p.names(kindData) {
		if p.schemaOf(kindData, n) == nil {
			return fmt.Errorf("decoding data source type %s of the schema pack", n)
		}
	}
	for _, n := range p.names(kindEphemeral) {
		if p.schemaOf(kindEphemeral, n) == nil {
			return fmt.Errorf("decoding ephemeral type %s of the schema pack", n)
		}
	}
	for _, n := range p.names(kindFunction) {
		if p.Function(n) == nil {
			return fmt.Errorf("decoding function %s of the schema pack", n)
		}
	}
	return nil
}
