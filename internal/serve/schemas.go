package serve

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/pravo-systems/pravo/internal/registry"
	"github.com/pravo-systems/pravo/internal/schema"
)

// SchemaFunc returns the schema of a provider at a version, or false when it is
// not at hand. The viewer never starts a provider to ask: the default reads the
// cache that "pravo init" and every other command leave under $PRAVO_HOME.
type SchemaFunc func(src registry.Source, version string) (Schemas, bool)

// Schemas gives the schema of a resource, data source or ephemeral resource type.
type Schemas interface {
	// Type returns the schema of the type of the given mode ("managed", "data"
	// or "ephemeral").
	Type(mode, typ string) *schema.Schema
	// Provider returns the schema of the provider block, or nil.
	Provider() *schema.Schema
}

// ProviderSchemas wraps a decoded provider schema as Schemas.
type ProviderSchemas struct{ PS *schema.ProviderSchema }

// Type implements Schemas.
func (p ProviderSchemas) Type(mode, typ string) *schema.Schema {
	switch mode {
	case "data":
		return p.PS.DataSource(typ)
	case "ephemeral":
		return p.PS.Ephemeral(typ)
	}
	return p.PS.Resource(typ)
}

// Provider implements Schemas.
func (p ProviderSchemas) Provider() *schema.Schema { return p.PS.Provider }

// cacheStore reads the schema cache lazily. The cache of a large provider is
// tens of megabytes of JSON, so a file is split into the schemas of its types
// without decoding them, and a type is decoded when a node of it is first seen.
type cacheStore struct {
	client *registry.Client

	mu    sync.Mutex
	files map[string]*cacheFile
	packs map[string]*cachePack
}

func newCacheStore() *cacheStore {
	return &cacheStore{client: &registry.Client{Home: registry.Home()}, files: map[string]*cacheFile{}, packs: map[string]*cachePack{}}
}

// cachePack is one cached provider schema in the form that pravo writes now: an
// index and a document for each type, which the schema reads as they are asked for.
type cachePack struct {
	mod     time.Time
	size    int64
	schemas Schemas
}

// cacheFile is one cached provider schema. mod and size say which version of
// the file the entry was made from.
type cacheFile struct {
	mod  time.Time
	size int64

	provider json.RawMessage
	types    [3]map[string]json.RawMessage // resources, data sources, ephemerals

	mu      sync.Mutex
	decoded map[string]*schema.Schema
	prov    *schema.Schema
}

// lookup is the SchemaFunc of the cache.
func (c *cacheStore) lookup(src registry.Source, version string) (Schemas, bool) {
	if version == "" {
		return nil, false
	}
	if s, ok := c.lookupPack(c.client.SchemaPackPath(src, version)); ok {
		return s, true
	}
	path := c.client.SchemaCachePath(src, version)
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return nil, false
	}
	c.mu.Lock()
	f := c.files[path]
	c.mu.Unlock()
	if f != nil && f.mod.Equal(fi.ModTime()) && f.size == fi.Size() {
		return f, true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var raw struct {
		CacheVersion int                        `json:"cache_version"`
		Provider     json.RawMessage            `json:"provider"`
		Resources    map[string]json.RawMessage `json:"resources"`
		DataSources  map[string]json.RawMessage `json:"data_sources"`
		Ephemerals   map[string]json.RawMessage `json:"ephemerals"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.CacheVersion < schema.CacheVersion {
		return nil, false
	}
	f = &cacheFile{mod: fi.ModTime(), size: fi.Size(), provider: raw.Provider, decoded: map[string]*schema.Schema{},
		types: [3]map[string]json.RawMessage{raw.Resources, raw.DataSources, raw.Ephemerals}}
	c.mu.Lock()
	c.files[path] = f
	c.mu.Unlock()
	return f, true
}

// lookupPack opens the cache of a provider that is kept as a pack.
func (c *cacheStore) lookupPack(path string) (Schemas, bool) {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return nil, false
	}
	c.mu.Lock()
	p := c.packs[path]
	c.mu.Unlock()
	if p != nil && p.mod.Equal(fi.ModTime()) && p.size == fi.Size() {
		return p.schemas, true
	}
	ps, err := schema.OpenPack(path)
	if err != nil {
		return nil, false
	}
	p = &cachePack{mod: fi.ModTime(), size: fi.Size(), schemas: ProviderSchemas{PS: ps}}
	c.mu.Lock()
	c.packs[path] = p
	c.mu.Unlock()
	return p.schemas, true
}

// Type implements Schemas.
func (f *cacheFile) Type(mode, typ string) *schema.Schema {
	which := 0
	switch mode {
	case "data":
		which = 1
	case "ephemeral":
		which = 2
	}
	key := mode + "/" + typ
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.decoded[key]; ok {
		return s
	}
	var s *schema.Schema
	if raw, ok := f.types[which][typ]; ok {
		var dec schema.Schema
		if json.Unmarshal(raw, &dec) == nil && dec.Block != nil {
			// The types of the attributes are worked out on first use and kept. Work
			// them out now, before the schema is shared between requests.
			dec.Block.Type()
			s = &dec
		}
	}
	f.decoded[key] = s
	return s
}

// Provider implements Schemas.
func (f *cacheFile) Provider() *schema.Schema {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.prov == nil && len(f.provider) > 0 {
		var dec schema.Schema
		if json.Unmarshal(f.provider, &dec) == nil {
			f.prov = &dec
		}
	}
	return f.prov
}
