package sensitivity

import (
	"encoding/json"

	"github.com/pravo-systems/pravo/internal/schema"
)

// This file is the one place that walks a schema to say what is secret. Every surface that
// withholds a value by what it is (the printed plan, plan JSON, the diff of a transaction, the
// viewer, --redact) asks here, so that they agree on the depth that is followed, on the name
// guard and on write-only attributes.

// HasWriteOnly reports whether the block has a write-only attribute at any level.
func HasWriteOnly(b *schema.Block) bool {
	return blockHolds(b, 0, func(a *schema.Attribute) bool { return a.WriteOnly }, nil)
}

// AttrHeld reports whether an attribute is withheld by what it is or holds something that is.
func AttrHeld(a *schema.Attribute) bool {
	return Withheld(a.Name, a.Sensitive, a.WriteOnly) || nestedSensitive(a.NestedType, 0)
}

// BlockHeld reports whether a nested block holds something that is withheld by what it is.
func BlockHeld(b *schema.Block) bool { return blockHoldsSensitive(b, 0) }

// SensitiveNames lists the names of the attributes and blocks of b, at any depth, whose values
// are withheld by what they are.
func SensitiveNames(b *schema.Block) map[string]bool {
	out := map[string]bool{}
	blockHolds(b, 0, func(a *schema.Attribute) bool {
		if Withheld(a.Name, a.Sensitive, a.WriteOnly) {
			out[a.Name] = true
		}
		return false
	}, func(nb *schema.NestedBlock) bool {
		if guardedName(nb.TypeName) {
			out[nb.TypeName] = true
		}
		return false
	})
	return out
}

// SensitiveObjectNames is SensitiveNames for the attributes of a nested attribute type.
func SensitiveObjectNames(o *schema.Object) map[string]bool {
	if o == nil {
		return nil
	}
	return SensitiveNames(&schema.Block{Attributes: o.Attributes})
}

// blockHolds walks a block and what is nested in it, to MaxDepth, and reports whether attr
// answers true for an attribute or block does for a nested block. Either may be nil.
func blockHolds(b *schema.Block, depth int, attr func(*schema.Attribute) bool, blk func(*schema.NestedBlock) bool) bool {
	if b == nil || depth > MaxDepth {
		return false
	}
	found := false
	var obj func(o *schema.Object, depth int)
	visit := func(a *schema.Attribute, depth int) {
		if attr != nil && attr(a) {
			found = true
		}
		obj(a.NestedType, depth+1)
	}
	obj = func(o *schema.Object, depth int) {
		if o == nil || depth > MaxDepth {
			return
		}
		for _, a := range o.Attributes {
			visit(a, depth)
		}
	}
	for _, a := range b.Attributes {
		visit(a, depth)
	}
	for _, nb := range b.BlockTypes {
		if blk != nil && blk(nb) {
			found = true
		}
		if blockHolds(nb.Block, depth+1, attr, blk) {
			found = true
		}
	}
	return found
}

func guardedName(name string) bool { return schema.NameGuard.MatchString(name) }

// HasGuardedKey reports whether a decoded JSON value holds an object with a key whose name says it
// is a secret, at any depth.
func HasGuardedKey(v any) bool {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			if HasGuardedKey(e) {
				return true
			}
		}
	case map[string]any:
		for k, e := range x {
			if guardedName(k) || HasGuardedKey(e) {
				return true
			}
		}
	}
	return false
}

// HasGuardedKeyJSON is HasGuardedKey for JSON text.
func HasGuardedKeyJSON(raw []byte) bool {
	var v any
	return json.Unmarshal(raw, &v) == nil && HasGuardedKey(v)
}

// RedactData replaces, in decoded plan data of a block, every attribute that is withheld by what it
// is with marker, and follows nested blocks and nested attribute types. Null stays null. The data
// that is passed is not changed.
func RedactData(d any, blk *schema.Block, marker any) any {
	m, ok := d.(map[string]any)
	if !ok || blk == nil {
		return d
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	for _, a := range blk.Attributes {
		v, ok := out[a.Name]
		if !ok || v == nil {
			continue
		}
		switch {
		case Withheld(a.Name, a.Sensitive, a.WriteOnly):
			out[a.Name] = marker
		case a.NestedType != nil:
			out[a.Name] = redactObject(v, a.NestedType, marker, 0)
		}
	}
	for _, nb := range blk.BlockTypes {
		switch x := out[nb.TypeName].(type) {
		case map[string]any:
			out[nb.TypeName] = RedactData(x, nb.Block, marker)
		case []any:
			list := make([]any, len(x))
			for i, e := range x {
				list[i] = RedactData(e, nb.Block, marker)
			}
			out[nb.TypeName] = list
		}
	}
	return out
}

func redactObject(v any, o *schema.Object, marker any, depth int) any {
	if depth > MaxDepth {
		return v
	}
	switch x := v.(type) {
	case []any:
		list := make([]any, len(x))
		for i, e := range x {
			list[i] = redactObject(e, o, marker, depth+1)
		}
		return list
	case map[string]any:
		// a map of objects is keyed by anything; an object has the names of the type
		if o.Nesting == schema.NestingMap {
			out := make(map[string]any, len(x))
			for k, e := range x {
				out[k] = redactObject(e, o, marker, depth+1)
			}
			return out
		}
		return RedactData(x, &schema.Block{Attributes: o.Attributes}, marker)
	}
	return v
}

// PathPredicate returns a predicate over attribute paths of a block that says whether the value at
// the path is withheld by what it is. A path runs through attribute and block names, with an index
// or key after a collection.
func PathPredicate(blk *schema.Block) func([]string) bool {
	return func(path []string) bool {
		b := blk
		var obj *schema.Object
		for i := 0; i < len(path); i++ {
			name := path[i]
			if obj != nil {
				a := obj.Attribute(name)
				if a == nil {
					return false
				}
				if Withheld(a.Name, a.Sensitive, a.WriteOnly) {
					return true
				}
				if a.NestedType == nil {
					return false
				}
				obj = a.NestedType
				if obj.Nesting != schema.NestingSingle {
					i++
				}
				continue
			}
			if a := b.Attribute(name); a != nil {
				if Withheld(a.Name, a.Sensitive, a.WriteOnly) {
					return true
				}
				if a.NestedType == nil {
					return false
				}
				obj = a.NestedType
				if obj.Nesting != schema.NestingSingle {
					i++
				}
				continue
			}
			nb := b.BlockType(name)
			if nb == nil {
				return false
			}
			if guardedName(nb.TypeName) {
				return true
			}
			b = nb.Block
			if nb.Nesting != schema.NestingSingle && nb.Nesting != schema.NestingGroup {
				i++
			}
		}
		return false
	}
}
