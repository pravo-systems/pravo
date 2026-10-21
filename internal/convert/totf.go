package convert

import (
	"fmt"
	"math/big"
	"strings"

	"cuelang.org/go/cue"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/schema"
)

// Converter converts CUE values to tftypes values. Unknowns records whether
// any unknown value was produced; Errors collects non-fatal problems.
type Converter struct {
	// Strict makes task references errors instead of unknowns (apply time).
	Strict bool
	// Self is the path of the task being converted; references into it are
	// not dependencies.
	Self cue.Path
	// Resolve maps referenced paths to tasks; nil means nothing is a task.
	Resolve Resolver
	// IsEphemeral reports whether a task path is an ephemeral resource.
	IsEphemeral func(cue.Path) bool
	// AllowEphemeral permits references to ephemeral resources anywhere in
	// the value. When false, only write-only attributes may use them, since
	// everything else ends up in state.
	AllowEphemeral bool
	// Aliases collects alias prefixes seen while converting.
	Aliases []Alias
	// Unknown is set when at least one unknown value was emitted.
	Unknown bool
	// Deps collects the task paths referenced by the converted value.
	Deps []cue.Path
	errs []error
}

// Err returns the accumulated errors, if any.
func (c *Converter) Err() error {
	switch len(c.errs) {
	case 0:
		return nil
	case 1:
		return c.errs[0]
	}
	var b strings.Builder
	for i, e := range c.errs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(e.Error())
	}
	return fmt.Errorf("%s", b.String())
}

// DepAddrs lists the referenced task paths as strings.
func (c *Converter) DepAddrs() []string {
	var out []string
	for _, d := range c.Deps {
		out = append(out, d.String())
	}
	return out
}

func (c *Converter) addDep(p cue.Path) {
	for _, d := range c.Deps {
		if d.String() == p.String() {
			return
		}
	}
	c.Deps = append(c.Deps, p)
}

func (c *Converter) fail(err error) {
	c.errs = append(c.errs, err)
}

// unknownOrNull decides what a non-concrete value becomes.
func (c *Converter) unknownOrNull(path string, v cue.Value, t tftypes.Type) tftypes.Value {
	var self *cue.Path
	if c.Self.String() != "" {
		self = &c.Self
	}
	info := AnalyseWith(v, c.Resolve, self)
	c.Aliases = append(c.Aliases, info.Aliases...)
	if info.HasTaskRef() {
		for _, p := range info.TaskRefs {
			c.addDep(p)
		}
		if c.Strict {
			detail := ""
			if err := v.Err(); err != nil {
				detail = " (" + err.Error() + ")"
			}
			c.fail(fmt.Errorf("%s: value depends on %s, which is still unknown at apply time%s", path, info.TaskRefs[0], detail))
			return tftypes.NewValue(t, nil)
		}
		c.Unknown = true
		return tftypes.NewValue(t, tftypes.UnknownValue)
	}
	if info.Incomplete != nil || (v.Err() != nil && v.Exists()) {
		c.fail(IncompleteError(path, v, info))
	}
	return tftypes.NewValue(t, nil)
}

// EphemeralRef returns the first ephemeral resource the value refers to.
func (c *Converter) EphemeralRef(v cue.Value) (cue.Path, bool) { return c.ephemeralRef(v) }

// ephemeralRef returns the first ephemeral resource the value refers to.
func (c *Converter) ephemeralRef(v cue.Value) (cue.Path, bool) {
	deps, _ := Dependencies(v, c.Self, c.Resolve)
	for _, d := range deps {
		if c.IsEphemeral != nil && c.IsEphemeral(d) {
			return d, true
		}
	}
	return cue.Path{}, false
}

// Block converts a value against a block schema, producing an object value.
// At the top level it enforces the ephemeral flow rule.
func (c *Converter) Block(path string, blk *schema.Block, v cue.Value) tftypes.Value {
	if !c.AllowEphemeral && v.Exists() && Default(v).Kind() == cue.StructKind {
		for _, a := range blk.Attributes {
			if a.WriteOnly {
				continue
			}
			fv := v.LookupPath(cue.MakePath(cue.Str(a.Name)))
			if ref, ok := c.ephemeralRef(fv); ok {
				c.fail(fmt.Errorf("%s.%s: references %s, but ephemeral values may only be used in write-only attributes, provider configuration and other ephemeral resources (anything else is recorded in state)", path, a.Name, ref))
			}
		}
		for _, nb := range blk.BlockTypes {
			fv := v.LookupPath(cue.MakePath(cue.Str(nb.TypeName)))
			if ref, ok := c.ephemeralRef(fv); ok {
				c.fail(fmt.Errorf("%s.%s: references %s, but ephemeral values may only be used in write-only attributes, provider configuration and other ephemeral resources (anything else is recorded in state)", path, nb.TypeName, ref))
			}
		}
	}
	return c.block(path, blk, v)
}

func (c *Converter) block(path string, blk *schema.Block, v cue.Value) tftypes.Value {
	t := blk.Type()
	if !v.Exists() {
		return tftypes.NewValue(t, nil)
	}
	v = Default(v)
	if v.Kind() == cue.NullKind {
		return tftypes.NewValue(t, nil)
	}
	if v.Kind() != cue.StructKind {
		if IsConcrete(v) {
			c.fail(fmt.Errorf("%s: expected a struct, got %v", path, v.Kind()))
			return tftypes.NewValue(t, nil)
		}
		return c.unknownOrNull(path, v, t)
	}
	attrs := map[string]tftypes.Value{}
	for _, a := range blk.Attributes {
		fv := v.LookupPath(cue.MakePath(cue.Str(a.Name)))
		attrs[a.Name] = c.attribute(path+"."+a.Name, a, fv)
	}
	for _, nb := range blk.BlockTypes {
		fv := v.LookupPath(cue.MakePath(cue.Str(nb.TypeName)))
		attrs[nb.TypeName] = c.nestedBlock(path+"."+nb.TypeName, nb, fv)
	}
	return tftypes.NewValue(t, attrs)
}

func (c *Converter) attribute(path string, a *schema.Attribute, v cue.Value) tftypes.Value {
	if a.NestedType != nil {
		return c.nestedObject(path, a.NestedType, v)
	}
	return c.Value(path, a.Type(), v)
}

func (c *Converter) nestedBlock(path string, nb *schema.NestedBlock, v cue.Value) tftypes.Value {
	elem := nb.Block.Type()
	switch nb.Nesting {
	case schema.NestingList, schema.NestingSet:
		var t tftypes.Type = tftypes.List{ElementType: elem}
		if nb.Nesting == schema.NestingSet {
			t = tftypes.Set{ElementType: elem}
		}
		if !v.Exists() {
			return tftypes.NewValue(t, []tftypes.Value{})
		}
		v = Default(v)
		if v.Kind() != cue.ListKind {
			if v.Kind() == cue.NullKind {
				return tftypes.NewValue(t, []tftypes.Value{})
			}
			u := c.unknownOrNull(path, v, t)
			if u.IsNull() {
				return tftypes.NewValue(t, []tftypes.Value{})
			}
			return u
		}
		var elems []tftypes.Value
		it, err := v.List()
		if err != nil {
			c.fail(fmt.Errorf("%s: %w", path, err))
			return tftypes.NewValue(t, []tftypes.Value{})
		}
		for i := 0; it.Next(); i++ {
			elems = append(elems, c.block(fmt.Sprintf("%s[%d]", path, i), nb.Block, it.Value()))
		}
		if elems == nil {
			elems = []tftypes.Value{}
		}
		return tftypes.NewValue(t, elems)
	case schema.NestingMap:
		t := tftypes.Map{ElementType: elem}
		if !v.Exists() || Default(v).Kind() != cue.StructKind {
			if v.Exists() && Default(v).Kind() != cue.NullKind {
				if u := c.unknownOrNull(path, Default(v), t); !u.IsNull() {
					return u
				}
			}
			return tftypes.NewValue(t, map[string]tftypes.Value{})
		}
		m := map[string]tftypes.Value{}
		it, _ := Default(v).Fields()
		for it.Next() {
			k := it.Selector().Unquoted()
			m[k] = c.block(fmt.Sprintf("%s[%q]", path, k), nb.Block, it.Value())
		}
		return tftypes.NewValue(t, m)
	case schema.NestingGroup:
		if !v.Exists() {
			// A group block is always present; absent means all-null body.
			return c.block(path, nb.Block, cueEmptyStruct)
		}
		return c.block(path, nb.Block, v)
	default:
		return c.block(path, nb.Block, v)
	}
}

// cueEmptyStruct is replaced at init with an empty struct value.
var cueEmptyStruct cue.Value

func (c *Converter) nestedObject(path string, o *schema.Object, v cue.Value) tftypes.Value {
	elem := o.ObjectType()
	t := o.Type()
	if !v.Exists() {
		return tftypes.NewValue(t, nil)
	}
	v = Default(v)
	if v.Kind() == cue.NullKind {
		return tftypes.NewValue(t, nil)
	}
	objOf := func(p string, sv cue.Value) tftypes.Value {
		sv = Default(sv)
		if sv.Kind() != cue.StructKind {
			if IsConcrete(sv) {
				c.fail(fmt.Errorf("%s: expected a struct, got %v", p, sv.Kind()))
				return tftypes.NewValue(elem, nil)
			}
			return c.unknownOrNull(p, sv, elem)
		}
		attrs := map[string]tftypes.Value{}
		for _, a := range o.Attributes {
			attrs[a.Name] = c.attribute(p+"."+a.Name, a, sv.LookupPath(cue.MakePath(cue.Str(a.Name))))
		}
		return tftypes.NewValue(elem, attrs)
	}
	switch o.Nesting {
	case schema.NestingList, schema.NestingSet:
		if v.Kind() != cue.ListKind {
			return c.unknownOrNull(path, v, t)
		}
		elems := []tftypes.Value{}
		it, _ := v.List()
		for i := 0; it.Next(); i++ {
			elems = append(elems, objOf(fmt.Sprintf("%s[%d]", path, i), it.Value()))
		}
		return tftypes.NewValue(t, elems)
	case schema.NestingMap:
		if v.Kind() != cue.StructKind {
			return c.unknownOrNull(path, v, t)
		}
		m := map[string]tftypes.Value{}
		it, _ := v.Fields()
		for it.Next() {
			k := it.Selector().Unquoted()
			m[k] = objOf(fmt.Sprintf("%s[%q]", path, k), it.Value())
		}
		return tftypes.NewValue(t, m)
	default:
		return objOf(path, v)
	}
}

// Value converts a CUE value to a tftypes value of type t.
func (c *Converter) Value(path string, t tftypes.Type, v cue.Value) tftypes.Value {
	if !v.Exists() {
		return tftypes.NewValue(t, nil)
	}
	v = Default(v)
	if t.Is(tftypes.DynamicPseudoType) {
		if !IsConcrete(v) {
			return c.unknownOrNull(path, v, t)
		}
		return c.Value(path, inferType(v), v)
	}
	switch {
	case t.Is(tftypes.String), t.Is(tftypes.Number), t.Is(tftypes.Bool):
		return c.primitive(path, t, v)
	case t.Is(tftypes.List{}), t.Is(tftypes.Set{}):
		if v.Kind() == cue.NullKind {
			return tftypes.NewValue(t, nil)
		}
		if v.Kind() != cue.ListKind {
			return c.unknownOrNull(path, v, t)
		}
		var et tftypes.Type
		if l, ok := t.(tftypes.List); ok {
			et = l.ElementType
		} else {
			et = t.(tftypes.Set).ElementType
		}
		elems := []tftypes.Value{}
		it, err := v.List()
		if err != nil {
			c.fail(fmt.Errorf("%s: %w", path, err))
			return tftypes.NewValue(t, nil)
		}
		for i := 0; it.Next(); i++ {
			elems = append(elems, c.Value(fmt.Sprintf("%s[%d]", path, i), et, it.Value()))
		}
		return tftypes.NewValue(t, elems)
	case t.Is(tftypes.Tuple{}):
		if v.Kind() == cue.NullKind {
			return tftypes.NewValue(t, nil)
		}
		if v.Kind() != cue.ListKind {
			return c.unknownOrNull(path, v, t)
		}
		ets := t.(tftypes.Tuple).ElementTypes
		elems := []tftypes.Value{}
		it, _ := v.List()
		for i := 0; it.Next(); i++ {
			if i >= len(ets) {
				c.fail(fmt.Errorf("%s: too many elements for tuple of %d", path, len(ets)))
				break
			}
			elems = append(elems, c.Value(fmt.Sprintf("%s[%d]", path, i), ets[i], it.Value()))
		}
		for len(elems) < len(ets) {
			elems = append(elems, tftypes.NewValue(ets[len(elems)], nil))
		}
		return tftypes.NewValue(t, elems)
	case t.Is(tftypes.Map{}):
		if v.Kind() == cue.NullKind {
			return tftypes.NewValue(t, nil)
		}
		if v.Kind() != cue.StructKind {
			return c.unknownOrNull(path, v, t)
		}
		et := t.(tftypes.Map).ElementType
		m := map[string]tftypes.Value{}
		it, _ := v.Fields()
		for it.Next() {
			k := it.Selector().Unquoted()
			m[k] = c.Value(fmt.Sprintf("%s[%q]", path, k), et, it.Value())
		}
		return tftypes.NewValue(t, m)
	case t.Is(tftypes.Object{}):
		if v.Kind() == cue.NullKind {
			return tftypes.NewValue(t, nil)
		}
		if v.Kind() != cue.StructKind {
			return c.unknownOrNull(path, v, t)
		}
		ats := t.(tftypes.Object).AttributeTypes
		attrs := map[string]tftypes.Value{}
		for name, at := range ats {
			attrs[name] = c.Value(path+"."+name, at, v.LookupPath(cue.MakePath(cue.Str(name))))
		}
		return tftypes.NewValue(t, attrs)
	}
	c.fail(fmt.Errorf("%s: unsupported type %s", path, t))
	return tftypes.NewValue(t, nil)
}

func (c *Converter) primitive(path string, t tftypes.Type, v cue.Value) tftypes.Value {
	if !IsConcrete(v) {
		return c.unknownOrNull(path, v, t)
	}
	if v.Kind() == cue.NullKind {
		return tftypes.NewValue(t, nil)
	}
	switch {
	case t.Is(tftypes.String):
		switch v.Kind() {
		case cue.StringKind:
			s, _ := v.String()
			return tftypes.NewValue(t, s)
		case cue.BytesKind:
			b, _ := v.Bytes()
			return tftypes.NewValue(t, string(b))
		case cue.IntKind, cue.FloatKind, cue.BoolKind:
			// OpenTofu converts primitives to string implicitly; do the same.
			return tftypes.NewValue(t, fmt.Sprint(v))
		}
	case tftypes.Number.Is(t):
		switch v.Kind() {
		case cue.IntKind:
			if bi, err := v.Int(nil); err == nil {
				return tftypes.NewValue(t, new(big.Float).SetInt(bi))
			}
		case cue.FloatKind:
			f, err := v.Float64()
			if err == nil {
				return tftypes.NewValue(t, big.NewFloat(f))
			}
		case cue.StringKind:
			s, _ := v.String()
			if f, _, err := big.ParseFloat(strings.TrimSpace(s), 10, 512, big.ToNearestEven); err == nil {
				return tftypes.NewValue(t, f)
			}
		}
	case t.Is(tftypes.Bool):
		if v.Kind() == cue.BoolKind {
			b, _ := v.Bool()
			return tftypes.NewValue(t, b)
		}
		if v.Kind() == cue.StringKind {
			s, _ := v.String()
			switch strings.ToLower(s) {
			case "true":
				return tftypes.NewValue(t, true)
			case "false":
				return tftypes.NewValue(t, false)
			}
		}
	}
	c.fail(fmt.Errorf("%s: cannot use %v as %s", path, v.Kind(), t))
	return tftypes.NewValue(t, nil)
}

// inferType derives a tftypes type for a concrete CUE value (used for
// dynamically typed attributes).
func inferType(v cue.Value) tftypes.Type {
	switch v.Kind() {
	case cue.StringKind, cue.BytesKind:
		return tftypes.String
	case cue.IntKind, cue.FloatKind, cue.NumberKind:
		return tftypes.Number
	case cue.BoolKind:
		return tftypes.Bool
	case cue.ListKind:
		var ets []tftypes.Type
		it, _ := v.List()
		for it.Next() {
			ets = append(ets, inferType(it.Value()))
		}
		if ets == nil {
			ets = []tftypes.Type{}
		}
		return tftypes.Tuple{ElementTypes: ets}
	case cue.StructKind:
		ats := map[string]tftypes.Type{}
		it, _ := v.Fields()
		for it.Next() {
			ats[it.Selector().Unquoted()] = inferType(it.Value())
		}
		return tftypes.Object{AttributeTypes: ats}
	}
	return tftypes.DynamicPseudoType
}
