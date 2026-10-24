package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"cuelang.org/go/cue/format"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/schema"
)

// ReadAdoption asks the provider for the object that $import would adopt and
// writes the attributes it has as the body of a resource, so that the resource
// that adopts it says what the object is. It imports and reads, as a plan of an
// $import does, and records nothing: no state is written.
//
// The body leaves out what the provider computes and the configuration does not
// set, what is write-only, and what is null. A sensitive attribute is named in a
// comment and its value is not written.
func (e *Engine) ReadAdoption(ctx context.Context, providerKey, typ string, target plugin.ImportTarget) (string, error) {
	label := target.Label()
	ps := e.Schemas[providerLocal(providerKey)]
	if ps == nil {
		return "", fmt.Errorf("no schema for provider %s", providerKey)
	}
	rs := ps.Resource(typ)
	if rs == nil {
		return "", fmt.Errorf("provider %s has no resource type %q to adopt the object as", providerKey, typ)
	}
	prov, err := e.Providers.Provider(ctx, providerKey)
	if err != nil {
		return "", err
	}
	types := map[string]tftypes.Type{}
	for name, s := range ps.AllResources() {
		types[name] = s.Block.Type()
	}
	imported, diags, err := prov.ImportResourceState(ctx, typ, target, types)
	if err != nil {
		return "", fmt.Errorf("importing %q: %w", label, err)
	}
	if err := diags.Err(); err != nil {
		return "", fmt.Errorf("importing %q: %w", label, err)
	}
	var found *tftypes.Value
	var private []byte
	for i := range imported {
		if imported[i].TypeName == typ {
			found, private = &imported[i].State, imported[i].Private
			break
		}
	}
	if found == nil {
		return "", fmt.Errorf("the provider returned no %s for %q", typ, label)
	}
	st, _, diags, err := prov.ReadResource(ctx, typ, *found, private)
	if err != nil {
		return "", fmt.Errorf("reading the object %q: %w", label, err)
	}
	if err := diags.Err(); err != nil {
		return "", fmt.Errorf("reading the object %q: %w", label, err)
	}
	if st.IsNull() {
		return "", fmt.Errorf("no %s %q exists", typ, label)
	}
	return RenderAdoption(rs.Block, st), nil
}

func providerLocal(key string) string {
	if i := strings.IndexByte(key, '.'); i >= 0 {
		return key[:i]
	}
	return key
}

// RenderAdoption writes the state of an object as the fields of a resource of the
// block's type, one tab in, ready to go between the braces of a snippet.
func RenderAdoption(blk *schema.Block, st tftypes.Value) string {
	var m map[string]tftypes.Value
	if st.As(&m) != nil {
		return ""
	}
	var b strings.Builder
	writeFields(&b, "\t", shape{attrs: blk.Attributes, blocks: blk.BlockTypes}, m)
	return b.String()
}

// shape is the attributes and the nested blocks that an object has.
type shape struct {
	attrs  []*schema.Attribute
	blocks []*schema.NestedBlock
}

func (s shape) attribute(name string) *schema.Attribute {
	for _, a := range s.attrs {
		if a.Name == name {
			return a
		}
	}
	return nil
}

func (s shape) block(name string) *schema.NestedBlock {
	for _, nb := range s.blocks {
		if nb.TypeName == name {
			return nb
		}
	}
	return nil
}

func writeFields(b *strings.Builder, indent string, sh shape, m map[string]tftypes.Value) {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v := m[n]
		if v.IsNull() || !v.IsKnown() {
			continue
		}
		label := convert.FieldLabel(n)
		if a := sh.attribute(n); a != nil {
			switch {
			case a.Computed && !a.Optional && !a.Required, a.WriteOnly:
				continue
			case a.Sensitive:
				fmt.Fprintf(b, "%s// %s is sensitive: write its value yourself.\n", indent, n)
			case a.NestedType != nil:
				if s, ok := nestedShape(indent, a.NestedType.Attributes, nil, string(a.NestedType.Nesting), v); ok {
					fmt.Fprintf(b, "%s%s: %s\n", indent, label, s)
				}
			default:
				fmt.Fprintf(b, "%s%s: %s\n", indent, label, convert.RenderCUE(v, indent, nil, nil))
			}
			continue
		}
		if nb := sh.block(n); nb != nil {
			if s, ok := nestedShape(indent, nb.Block.Attributes, nb.Block.BlockTypes, string(nb.Nesting), v); ok {
				fmt.Fprintf(b, "%s%s: %s\n", indent, label, s)
			}
			continue
		}
		// A field the schema does not name is written as it is.
		fmt.Fprintf(b, "%s%s: %s\n", indent, label, convert.RenderCUE(v, indent, nil, nil))
	}
}

// nestedShape renders a nested block or nested attribute: a list or set of
// objects, a map of objects, or one object. ok is false when it holds nothing.
func nestedShape(indent string, attrs []*schema.Attribute, blocks []*schema.NestedBlock, nesting string, v tftypes.Value) (string, bool) {
	sh := shape{attrs: attrs, blocks: blocks}
	object := func(ind string, ov tftypes.Value) (string, bool) {
		var m map[string]tftypes.Value
		if ov.IsNull() || !ov.IsKnown() || ov.As(&m) != nil {
			return "", false
		}
		var b strings.Builder
		writeFields(&b, ind+"\t", sh, m)
		if b.Len() == 0 {
			return "{}", true
		}
		return "{\n" + b.String() + ind + "}", true
	}
	switch nesting {
	case string(schema.NestingList), string(schema.NestingSet):
		var elems []tftypes.Value
		if v.As(&elems) != nil || len(elems) == 0 {
			return "", false
		}
		var b strings.Builder
		b.WriteString("[\n")
		for _, e := range elems {
			if s, ok := object(indent+"\t", e); ok {
				b.WriteString(indent + "\t" + s + ",\n")
			}
		}
		b.WriteString(indent + "]")
		return b.String(), true
	case string(schema.NestingMap):
		var entries map[string]tftypes.Value
		if v.As(&entries) != nil || len(entries) == 0 {
			return "", false
		}
		keys := make([]string, 0, len(entries))
		for k := range entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("{\n")
		for _, k := range keys {
			if s, ok := object(indent+"\t", entries[k]); ok {
				fmt.Fprintf(&b, "%s\t%s: %s\n", indent, convert.FieldLabel(k), s)
			}
		}
		b.WriteString(indent + "}")
		return b.String(), true
	}
	return object(indent, v)
}

// FormatCUE lays out a snippet as cue fmt does and returns it as it is when it
// does not parse.
func FormatCUE(src string) string {
	out, err := format.Source([]byte(src))
	if err != nil {
		return src
	}
	return string(out)
}
