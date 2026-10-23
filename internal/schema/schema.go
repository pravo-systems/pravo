// Package schema holds the provider-independent model of an OpenTofu provider
// schema, as returned by GetProviderSchema over protocol 5 or 6, plus the
// derivations pravo needs from it: implied tftypes types and CUE definitions.
package schema

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// NestingMode is how a nested block or nested attribute is arranged.
type NestingMode string

const (
	NestingInvalid NestingMode = ""
	NestingSingle  NestingMode = "single"
	NestingList    NestingMode = "list"
	NestingSet     NestingMode = "set"
	NestingMap     NestingMode = "map"
	NestingGroup   NestingMode = "group"
)

// Attribute is a single attribute of a block or nested object.
type Attribute struct {
	Name        string          `json:"name"`
	TypeJSON    json.RawMessage `json:"type,omitempty"`
	NestedType  *Object         `json:"nested_type,omitempty"`
	Description string          `json:"description,omitempty"`
	Required    bool            `json:"required,omitempty"`
	Optional    bool            `json:"optional,omitempty"`
	Computed    bool            `json:"computed,omitempty"`
	Sensitive   bool            `json:"sensitive,omitempty"`
	Deprecated  bool            `json:"deprecated,omitempty"`
	WriteOnly   bool            `json:"write_only,omitempty"`

	typ tftypes.Type
}

// Object is a nested attribute type (protocol 6 only).
type Object struct {
	Attributes []*Attribute `json:"attributes"`
	Nesting    NestingMode  `json:"nesting"`
}

// Block is a configuration block: attributes plus nested block types.
type Block struct {
	Attributes  []*Attribute   `json:"attributes,omitempty"`
	BlockTypes  []*NestedBlock `json:"block_types,omitempty"`
	Description string         `json:"description,omitempty"`
	Deprecated  bool           `json:"deprecated,omitempty"`
}

// NestedBlock is a block type nested inside another block.
type NestedBlock struct {
	TypeName string      `json:"type_name"`
	Block    *Block      `json:"block"`
	Nesting  NestingMode `json:"nesting"`
	MinItems int64       `json:"min_items,omitempty"`
	MaxItems int64       `json:"max_items,omitempty"`
}

// Schema is a versioned block schema for a provider, resource or data source.
type Schema struct {
	Version int64  `json:"version"`
	Block   *Block `json:"block"`
}

// CacheVersion changes whenever the cached JSON form gains fields that
// older caches lack; stale caches are refetched from the provider.
const CacheVersion = 3

// ProviderSchema is everything pravo needs to know about a provider.
type ProviderSchema struct {
	CacheVersion int                  `json:"cache_version,omitempty"`
	Provider     *Schema              `json:"provider"`
	ProviderMeta *Schema              `json:"provider_meta,omitempty"`
	Resources    map[string]*Schema   `json:"resources"`
	DataSources  map[string]*Schema   `json:"data_sources"`
	Ephemerals   map[string]*Schema   `json:"ephemerals"`
	Functions    map[string]*Function `json:"functions"`

	// lazy is the pack that the types are read from when the schema was opened
	// with OpenPack; the maps then hold the types that were asked for. Read the
	// types of such a schema through Resource, DataSource, Ephemeral and Function.
	lazy *lazyPack
}

// Parameter is one parameter of a provider-defined function.
type Parameter struct {
	Name         string          `json:"name"`
	TypeJSON     json.RawMessage `json:"type"`
	AllowNull    bool            `json:"allow_null,omitempty"`
	AllowUnknown bool            `json:"allow_unknown,omitempty"`
	Description  string          `json:"description,omitempty"`

	typ tftypes.Type
}

// Type returns the parameter's tftypes type.
func (p *Parameter) Type() tftypes.Type {
	if p.typ == nil {
		p.typ = parseType(p.TypeJSON)
	}
	return p.typ
}

// Function is a provider-defined function.
type Function struct {
	Parameters  []*Parameter    `json:"parameters"`
	Variadic    *Parameter      `json:"variadic,omitempty"`
	ReturnJSON  json.RawMessage `json:"return"`
	Summary     string          `json:"summary,omitempty"`
	Description string          `json:"description,omitempty"`
	Deprecated  bool            `json:"deprecated,omitempty"`

	ret tftypes.Type
}

// ReturnType returns the function's declared result type.
func (f *Function) ReturnType() tftypes.Type {
	if f.ret == nil {
		f.ret = parseType(f.ReturnJSON)
	}
	return f.ret
}

func parseType(raw json.RawMessage) tftypes.Type {
	if len(raw) == 0 {
		return tftypes.DynamicPseudoType
	}
	t, err := tftypes.ParseJSONType(raw)
	if err != nil {
		return tftypes.DynamicPseudoType
	}
	return t
}

// SortedFunctionNames returns function names in order.
func (p *ProviderSchema) SortedFunctionNames() []string { return p.names(kindFunction) }

// Type returns the tftypes type of the attribute.
func (a *Attribute) Type() tftypes.Type {
	if a.typ != nil {
		return a.typ
	}
	if a.NestedType != nil {
		a.typ = a.NestedType.Type()
		return a.typ
	}
	if len(a.TypeJSON) == 0 {
		a.typ = tftypes.DynamicPseudoType
		return a.typ
	}
	t, err := tftypes.ParseJSONType(a.TypeJSON)
	if err != nil {
		// Unparseable types are treated as dynamic; the provider would reject
		// anything we send anyway.
		t = tftypes.DynamicPseudoType
	}
	a.typ = t
	return t
}

// Type returns the tftypes type implied by a nested attribute object.
func (o *Object) Type() tftypes.Type {
	obj := attributesType(o.Attributes)
	return nest(o.Nesting, obj)
}

// ObjectType returns the element object type of the nested attribute, without
// the collection wrapper.
func (o *Object) ObjectType() tftypes.Object {
	return attributesType(o.Attributes)
}

func attributesType(attrs []*Attribute) tftypes.Object {
	m := make(map[string]tftypes.Type, len(attrs))
	for _, a := range attrs {
		m[a.Name] = a.Type()
	}
	return tftypes.Object{AttributeTypes: m}
}

func nest(mode NestingMode, obj tftypes.Object) tftypes.Type {
	switch mode {
	case NestingList:
		return tftypes.List{ElementType: obj}
	case NestingSet:
		return tftypes.Set{ElementType: obj}
	case NestingMap:
		return tftypes.Map{ElementType: obj}
	default:
		return obj
	}
}

// Type returns the object type implied by the block, including nested blocks.
func (b *Block) Type() tftypes.Object {
	m := map[string]tftypes.Type{}
	for _, a := range b.Attributes {
		m[a.Name] = a.Type()
	}
	for _, nb := range b.BlockTypes {
		m[nb.TypeName] = nest(nb.Nesting, nb.Block.Type())
	}
	return tftypes.Object{AttributeTypes: m}
}

// Attribute looks up an attribute by name.
func (b *Block) Attribute(name string) *Attribute {
	for _, a := range b.Attributes {
		if a.Name == name {
			return a
		}
	}
	return nil
}

// BlockType looks up a nested block type by name.
func (b *Block) BlockType(name string) *NestedBlock {
	for _, nb := range b.BlockTypes {
		if nb.TypeName == name {
			return nb
		}
	}
	return nil
}

// Attribute looks up a nested attribute by name.
func (o *Object) Attribute(name string) *Attribute {
	for _, a := range o.Attributes {
		if a.Name == name {
			return a
		}
	}
	return nil
}

// SortedResourceNames returns resource type names in order.
func (p *ProviderSchema) SortedResourceNames() []string {
	return p.names(kindResource)
}

// SortedDataSourceNames returns data source type names in order.
func (p *ProviderSchema) SortedDataSourceNames() []string {
	return p.names(kindData)
}

// SortedEphemeralNames returns ephemeral resource type names in order.
func (p *ProviderSchema) SortedEphemeralNames() []string {
	return p.names(kindEphemeral)
}

func sortedKeys(m map[string]*Schema) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Sort orders attributes and block types by name, recursively, so that
// generated output is stable.
func (b *Block) Sort() {
	sort.Slice(b.Attributes, func(i, j int) bool { return b.Attributes[i].Name < b.Attributes[j].Name })
	sort.Slice(b.BlockTypes, func(i, j int) bool { return b.BlockTypes[i].TypeName < b.BlockTypes[j].TypeName })
	for _, a := range b.Attributes {
		if a.NestedType != nil {
			a.NestedType.sort()
		}
	}
	for _, nb := range b.BlockTypes {
		nb.Block.Sort()
	}
}

func (o *Object) sort() {
	sort.Slice(o.Attributes, func(i, j int) bool { return o.Attributes[i].Name < o.Attributes[j].Name })
	for _, a := range o.Attributes {
		if a.NestedType != nil {
			a.NestedType.sort()
		}
	}
}

// Sort orders everything in the provider schema for stable output.
func (p *ProviderSchema) Sort() {
	_ = p.Materialize()
	if p.Provider != nil && p.Provider.Block != nil {
		p.Provider.Block.Sort()
	}
	for _, s := range p.Resources {
		s.Block.Sort()
	}
	for _, s := range p.DataSources {
		s.Block.Sort()
	}
	for _, s := range p.Ephemerals {
		s.Block.Sort()
	}
}

// Marshal encodes the schema for the on-disk cache.
func (p *ProviderSchema) Marshal() ([]byte, error) {
	if err := p.Materialize(); err != nil {
		return nil, err
	}
	p.CacheVersion = CacheVersion
	return json.MarshalIndent(p, "", " ")
}

// ErrStaleCache is returned by Unmarshal for caches written by an older
// pravo that lack fields this build needs.
var ErrStaleCache = fmt.Errorf("cached provider schema is from an older pravo")

// Unmarshal decodes a cached schema.
func Unmarshal(data []byte) (*ProviderSchema, error) {
	var p ProviderSchema
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("decoding cached provider schema: %w", err)
	}
	if p.CacheVersion < CacheVersion {
		return nil, ErrStaleCache
	}
	if p.Ephemerals == nil {
		p.Ephemerals = map[string]*Schema{}
	}
	if p.Functions == nil {
		p.Functions = map[string]*Function{}
	}
	if p.Resources == nil {
		p.Resources = map[string]*Schema{}
	}
	if p.DataSources == nil {
		p.DataSources = map[string]*Schema{}
	}
	return &p, nil
}
