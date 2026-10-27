package convert

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/schema"
)

// ErrUnknown is returned by ToGo when a value contains unknowns.
var ErrUnknown = errors.New("value contains unknowns")

// ToGo converts a fully known tftypes value to plain Go data suitable for
// filling into CUE (nil for null, string, *big.Int or *big.Float, bool,
// []any, map[string]any).
func ToGo(v tftypes.Value) (any, error) { return toGo(v, false) }

func toGo(v tftypes.Value, jsonNumbers bool) (any, error) {
	if !v.IsKnown() {
		return nil, ErrUnknown
	}
	if v.IsNull() {
		return nil, nil
	}
	t := v.Type()
	switch {
	case t.Is(tftypes.String):
		var s string
		return s, v.As(&s)
	case t.Is(tftypes.Bool):
		var b bool
		return b, v.As(&b)
	case t.Is(tftypes.Number):
		var f big.Float
		if err := v.As(&f); err != nil {
			return nil, err
		}
		if jsonNumbers {
			return json.Number(f.Text('f', -1)), nil
		}
		if f.IsInt() {
			i, _ := f.Int(nil)
			return i, nil
		}
		return &f, nil
	case t.Is(tftypes.List{}), t.Is(tftypes.Set{}), t.Is(tftypes.Tuple{}):
		var elems []tftypes.Value
		if err := v.As(&elems); err != nil {
			return nil, err
		}
		out := make([]any, 0, len(elems))
		for _, e := range elems {
			g, err := toGo(e, jsonNumbers)
			if err != nil {
				return nil, err
			}
			out = append(out, g)
		}
		return out, nil
	case t.Is(tftypes.Map{}), t.Is(tftypes.Object{}):
		var m map[string]tftypes.Value
		if err := v.As(&m); err != nil {
			return nil, err
		}
		out := make(map[string]any, len(m))
		for k, e := range m {
			g, err := toGo(e, jsonNumbers)
			if err != nil {
				return nil, err
			}
			out[k] = g
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported type %s", t)
}

// ToJSON encodes a fully known value as JSON for the state file.
func ToJSON(v tftypes.Value) ([]byte, error) {
	g, err := toGo(v, true)
	if err != nil {
		return nil, err
	}
	return json.Marshal(g)
}

// Fill computes the Go value to unify into the configuration at the resource
// path so that computed attributes become concrete. Only parts of the
// configuration that are not already concrete are filled; unknowns are
// skipped; nulls are filled only where the configuration allows null.
func Fill(cfg cue.Value, blk *schema.Block, st tftypes.Value) map[string]any {
	out, _ := fillBlock(cfg, blk, st)
	return out
}

// PathFill is a value to unify into a configuration at Path.
type PathFill struct {
	Path  cue.Path
	Value any
}

// FillAll unifies every fill into v with one evaluation of v. Filling one path
// at a time evaluates the whole configuration again for each fill, which makes
// the cost of n fills grow with n times the size of the configuration. The
// fills are combined with each other first, in a balanced tree of small
// structs, and v is unified with the result once.
func FillAll(v cue.Value, fills []PathFill) cue.Value {
	if len(fills) == 0 {
		return v
	}
	empty := v.Context().CompileString("{}")
	vals := make([]cue.Value, len(fills))
	for i, f := range fills {
		vals[i] = empty.FillPath(f.Path, f.Value)
	}
	return v.Unify(unifyAll(vals))
}

func unifyAll(vals []cue.Value) cue.Value {
	if len(vals) == 1 {
		return vals[0]
	}
	mid := len(vals) / 2
	return unifyAll(vals[:mid]).Unify(unifyAll(vals[mid:]))
}

func fillBlock(cfg cue.Value, blk *schema.Block, st tftypes.Value) (map[string]any, bool) {
	if !st.IsKnown() || st.IsNull() {
		return nil, false
	}
	var obj map[string]tftypes.Value
	if err := st.As(&obj); err != nil {
		return nil, false
	}
	out := map[string]any{}
	for _, a := range blk.Attributes {
		sv, ok := obj[a.Name]
		if !ok {
			continue
		}
		fv := cfg.LookupPath(cue.MakePath(cue.Str(a.Name)))
		if x, ok := fillValue(fv, sv); ok {
			out[a.Name] = x
		}
	}
	for _, nb := range blk.BlockTypes {
		sv, ok := obj[nb.TypeName]
		if !ok {
			continue
		}
		fv := cfg.LookupPath(cue.MakePath(cue.Str(nb.TypeName)))
		if x, ok := fillValue(fv, sv); ok {
			out[nb.TypeName] = x
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func fillValue(cfg cue.Value, sv tftypes.Value) (any, bool) {
	if !sv.IsKnown() {
		return nil, false
	}
	if !cfg.Exists() {
		if sv.IsNull() {
			return nil, false
		}
		g, err := ToGo(sv)
		if err != nil {
			return nil, false
		}
		return g, true
	}
	cfg = Default(cfg)
	if sv.IsNull() {
		if cfg.IncompleteKind()&cue.NullKind != 0 && !IsConcrete(cfg) {
			return nil, true
		}
		return nil, false
	}
	if IsConcrete(cfg) {
		return nil, false
	}
	t := sv.Type()
	switch {
	case t.Is(tftypes.Object{}), t.Is(tftypes.Map{}):
		if cfg.Kind() != cue.StructKind {
			g, err := ToGo(sv)
			return g, err == nil
		}
		var m map[string]tftypes.Value
		if err := sv.As(&m); err != nil {
			return nil, false
		}
		out := map[string]any{}
		for k, e := range m {
			if x, ok := fillValue(cfg.LookupPath(cue.MakePath(cue.Str(k))), e); ok {
				out[k] = x
			}
		}
		return out, len(out) > 0
	case t.Is(tftypes.List{}), t.Is(tftypes.Set{}), t.Is(tftypes.Tuple{}):
		var elems []tftypes.Value
		if err := sv.As(&elems); err != nil {
			return nil, false
		}
		if cfg.Kind() != cue.ListKind {
			g, err := ToGo(sv)
			return g, err == nil
		}
		var cfgElems []cue.Value
		it, _ := cfg.List()
		for it.Next() {
			cfgElems = append(cfgElems, it.Value())
		}
		if len(cfgElems) != len(elems) {
			g, err := ToGo(sv)
			return g, err == nil
		}
		// Lists can only be filled element-wise when every element can be
		// expressed; fill the whole list with configuration values merged in.
		out := make([]any, len(elems))
		filled := false
		for i, e := range elems {
			x, ok := fillValue(cfgElems[i], e)
			if ok {
				out[i] = x
				filled = true
			} else {
				g, err := ToGo(e)
				if err != nil {
					return nil, false
				}
				out[i] = g
			}
		}
		return out, filled
	}
	g, err := ToGo(sv)
	return g, err == nil
}

// Indent is the indentation unit used when rendering CUE.
const Indent = "    "

// RenderCUE renders a tftypes value as CUE source. Unknown values render as
// the placeholder "(known after apply)" marker handled by the caller via
// the unknown callback.
func RenderCUE(v tftypes.Value, indent string, sensitive func(path []string) bool, path []string) string {
	if sensitive != nil && sensitive(path) && !v.IsNull() {
		return "(sensitive)"
	}
	if !v.IsKnown() {
		return "(known after apply)"
	}
	if v.IsNull() {
		return "null"
	}
	t := v.Type()
	switch {
	case t.Is(tftypes.String):
		var s string
		_ = v.As(&s)
		return quoteCUE(s)
	case t.Is(tftypes.Bool):
		var b bool
		_ = v.As(&b)
		return strconv.FormatBool(b)
	case t.Is(tftypes.Number):
		var f big.Float
		_ = v.As(&f)
		return f.Text('f', -1)
	case t.Is(tftypes.List{}), t.Is(tftypes.Set{}), t.Is(tftypes.Tuple{}):
		var elems []tftypes.Value
		_ = v.As(&elems)
		if len(elems) == 0 {
			return "[]"
		}
		var b strings.Builder
		b.WriteString("[\n")
		for i, e := range elems {
			b.WriteString(indent + Indent + RenderCUE(e, indent+Indent, sensitive, append(path, strconv.Itoa(i))) + ",\n")
		}
		b.WriteString(indent + "]")
		return b.String()
	case t.Is(tftypes.Map{}), t.Is(tftypes.Object{}):
		var m map[string]tftypes.Value
		_ = v.As(&m)
		if len(m) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("{\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "%s%s%s: %s\n", indent, Indent, FieldLabel(k), RenderCUE(m[k], indent+Indent, sensitive, append(path, k)))
		}
		b.WriteString(indent + "}")
		return b.String()
	}
	return v.String()
}

// FieldLabel quotes a label when needed for CUE output.
func FieldLabel(k string) string {
	if k == "" {
		return `""`
	}
	for i, r := range k {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return quoteCUE(k)
	}
	switch k {
	case "package", "import", "for", "in", "if", "let", "true", "false", "null":
		return quoteCUE(k)
	}
	return k
}

// quoteCUE writes s as a quoted CUE string. The characters <, > and & stay
// as they are; they are not written as \u003c, \u003e and \u0026.
func quoteCUE(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}
