package engine

import (
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// The rule of this file: a list, set or map that one side leaves null and the other holds
// empty is the same value. Providers write a null where the configuration has none and read
// back an empty collection (every AWS resource created without tags), so a comparison of a
// record, a read or a plan that treated them as different would see a change that is not
// one. There is one rule and every comparison of values that came from a provider uses it:
// sameAttributes and readWithin on the JSON of a record, consistent on the values of a plan.
// Nothing else may decide it again; a comparison of the same data that needs it calls these.

// sameAttributes reports whether two records of a resource say the same for the
// purpose of drift. It differs from jsonEqual in one way: a list or a map that
// one leaves null and the other has empty is the same. Providers write a null where
// the configuration has none and read back an empty list or map, so that a record
// would differ from the read of the object that it was made from.
func sameAttributes(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return string(a) == string(b)
	}
	ax, _ := json.Marshal(emptyAsNull(x))
	by, _ := json.Marshal(emptyAsNull(y))
	return string(ax) == string(by)
}

// emptyAsNull replaces every empty list and empty map in a decoded value with null.
func emptyAsNull(v any) any {
	switch t := v.(type) {
	case []any:
		if len(t) == 0 {
			return nil
		}
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = emptyAsNull(e)
		}
		return out
	case map[string]any:
		if len(t) == 0 {
			return nil
		}
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = emptyAsNull(e)
		}
		return out
	}
	return v
}

// emptyCollection reports whether v is a known list, set or map with no elements.
func emptyCollection(v tftypes.Value) bool {
	if v.IsNull() || !v.IsKnown() {
		return false
	}
	t := v.Type()
	switch {
	case t.Is(tftypes.Map{}):
		var m map[string]tftypes.Value
		return v.As(&m) == nil && len(m) == 0
	case t.Is(tftypes.List{}) || t.Is(tftypes.Set{}):
		var l []tftypes.Value
		return v.As(&l) == nil && len(l) == 0
	}
	return false
}

// readWithin reports whether every attribute of read that is not null or empty is the same in held.
func readWithin(read, held []byte) bool {
	var r, h map[string]any
	if json.Unmarshal(read, &r) != nil || json.Unmarshal(held, &h) != nil {
		return false
	}
	for k, v := range r {
		if v == "" || emptyAsNull(v) == nil {
			continue
		}
		a, _ := json.Marshal(emptyAsNull(v))
		b, _ := json.Marshal(emptyAsNull(h[k]))
		if string(a) != string(b) {
			return false
		}
	}
	return true
}
