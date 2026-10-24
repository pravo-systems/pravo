package plugin

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// ImportTarget names the object that ImportResourceState brings in. It is an id,
// the one string that a provider documents for importing, or an identity: the
// attributes that the resource's identity schema lists, which a list of the type
// gives. A target has one of them.
type ImportTarget struct {
	ID       string
	Identity map[string]any
}

// Label writes the target on one line, for a message.
func (t ImportTarget) Label() string {
	if t.Identity == nil {
		return t.ID
	}
	names := make([]string, 0, len(t.Identity))
	for n := range t.Identity {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		v := t.Identity[n]
		if s, ok := v.(string); ok {
			parts[i] = n + "=" + s
		} else {
			b, _ := json.Marshal(v)
			parts[i] = n + "=" + string(b)
		}
	}
	return strings.Join(parts, ",")
}

// IdentityAttribute is one attribute of the identity of a resource type.
type IdentityAttribute struct {
	Name string
	Type tftypes.Type
	// RequiredForImport says that an import fails without it. OptionalForImport says
	// that the provider can supply it, from its configuration, when it is left out.
	RequiredForImport bool
	OptionalForImport bool
}

// IdentitySchema is the identity of a resource type: what names one object of it.
type IdentitySchema struct {
	Version    int64
	Attributes []IdentityAttribute
}

// Type is the object type that the identity has on the wire.
func (s *IdentitySchema) Type() tftypes.Object {
	m := make(map[string]tftypes.Type, len(s.Attributes))
	for _, a := range s.Attributes {
		m[a.Name] = a.Type
	}
	return tftypes.Object{AttributeTypes: m}
}

// encode makes the value of an identity from the attributes a person wrote, and
// says what is wrong with them in the words of the specification: an attribute
// that the type does not have, one that is needed and missing, a value of the wrong
// type. An attribute that is left out is null.
func (s *IdentitySchema) encode(typ string, identity map[string]any) (tftypes.Value, error) {
	known := map[string]IdentityAttribute{}
	names := make([]string, 0, len(s.Attributes))
	for _, a := range s.Attributes {
		known[a.Name] = a
		names = append(names, a.Name)
	}
	sort.Strings(names)
	for n := range identity {
		if _, ok := known[n]; !ok {
			return tftypes.Value{}, fmt.Errorf("%s has no identity attribute %q; its identity is made of %s", typ, n, strings.Join(names, ", "))
		}
	}
	doc := make(map[string]any, len(known))
	for n, a := range known {
		v, ok := identity[n]
		if !ok || v == nil {
			if a.RequiredForImport {
				return tftypes.Value{}, fmt.Errorf("the identity of a %s needs %q to import", typ, n)
			}
			doc[n] = nil
			continue
		}
		doc[n] = v
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return tftypes.Value{}, fmt.Errorf("the identity of a %s: %w", typ, err)
	}
	v, err := tftypes.ValueFromJSON(b, s.Type())
	if err != nil {
		return tftypes.Value{}, fmt.Errorf("the identity of a %s does not fit its schema: %w", typ, err)
	}
	return v, nil
}
