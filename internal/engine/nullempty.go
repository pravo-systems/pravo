package engine

import (
	"bytes"
	"encoding/json"
	"math/big"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// The rule of this file: a list, set or map that one side leaves null and the other holds
// empty is the same value. Providers write a null where the configuration has none and read
// back an empty collection (every AWS resource created without tags), so a comparison of a
// record, a read or a plan that treated them as different would see a change that is not
// one. There is one rule and every comparison of values that came from a provider uses it:
// sameAttributes and readWithin on the JSON of a record, equivalent (through consistent) on the values of a plan.
// Nothing else may decide it again; a comparison of the same data that needs it calls these.

// sameAttributes reports whether two records of a resource say the same for the
// purpose of drift, of the read of a data source and of the read-back. A list or a map that
// one leaves null and the other has empty is the same. Providers write a null where
// the configuration has none and read back an empty list or map, so that a record
// would differ from the read of the object that it was made from. Numbers compare by value
// at any size, not through a float64.
func sameAttributes(a, b []byte) bool { return sameJSON(a, b, true) }

// sameOutput reports whether two values of an output are the same. Unlike a record, an output
// keeps a null and an empty collection apart: what reads the output sees the difference.
func sameOutput(a, b []byte) bool { return sameJSON(a, b, false) }

// sameJSON is the one comparison of JSON that came from a provider or from a specification.
func sameJSON(a, b []byte, nullIsEmpty bool) bool {
	x, okx := decodeJSON(a)
	y, oky := decodeJSON(b)
	if !okx || !oky {
		return string(a) == string(b)
	}
	if nullIsEmpty {
		x, y = emptyAsNull(x), emptyAsNull(y)
	}
	ax, _ := json.Marshal(canonicalNumbers(x))
	by, _ := json.Marshal(canonicalNumbers(y))
	return string(ax) == string(by)
}

// decodeJSON decodes with numbers kept as written, so that none is rounded.
func decodeJSON(raw []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil || dec.More() {
		return nil, false
	}
	return v, true
}

// canonicalNumbers writes every number of a decoded value in one form (1.0 and 1 are one).
func canonicalNumbers(v any) any {
	switch t := v.(type) {
	case json.Number:
		if f, _, err := big.ParseFloat(string(t), 10, 512, big.ToNearestEven); err == nil {
			return json.Number(f.Text('g', -1))
		}
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = canonicalNumbers(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = canonicalNumbers(e)
		}
		return out
	}
	return v
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

// equivalent reports whether two fully known values are the same, with the rule of this file at
// every depth: inside a list, a map, an object and a set. A set compares its elements without
// regard to their order.
func equivalent(a, b tftypes.Value) bool {
	if !a.IsKnown() || !b.IsKnown() {
		return a.IsKnown() == b.IsKnown()
	}
	if a.IsNull() || b.IsNull() {
		return a.IsNull() == b.IsNull() || emptyCollection(a) || emptyCollection(b)
	}
	t := a.Type()
	switch {
	case t.Is(tftypes.Object{}) || t.Is(tftypes.Map{}):
		var am, bm map[string]tftypes.Value
		if a.As(&am) != nil || b.As(&bm) != nil || len(am) != len(bm) {
			return false
		}
		for k, av := range am {
			bv, ok := bm[k]
			if !ok || !equivalent(av, bv) {
				return false
			}
		}
		return true
	case t.Is(tftypes.List{}) || t.Is(tftypes.Tuple{}):
		var al, bl []tftypes.Value
		if a.As(&al) != nil || b.As(&bl) != nil || len(al) != len(bl) {
			return false
		}
		for i := range al {
			if !equivalent(al[i], bl[i]) {
				return false
			}
		}
		return true
	case t.Is(tftypes.Set{}):
		var al, bl []tftypes.Value
		if a.As(&al) != nil || b.As(&bl) != nil || len(al) != len(bl) {
			return false
		}
		used := make([]bool, len(bl))
	next:
		for _, av := range al {
			for j, bv := range bl {
				if !used[j] && equivalent(av, bv) {
					used[j] = true
					continue next
				}
			}
			return false
		}
		return true
	}
	return a.Equal(b)
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
