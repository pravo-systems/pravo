package engine

import (
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/schema"
)

// withoutWriteOnly returns v with every write-only attribute set to null,
// in the block and in the blocks and nested attributes below it. A write-only
// value reaches the provider and nothing else: the state never holds it, and
// neither does a saved plan.
func withoutWriteOnly(blk *schema.Block, v tftypes.Value) tftypes.Value {
	if blk == nil || v.IsNull() || !v.IsKnown() {
		return v
	}
	var in map[string]tftypes.Value
	if v.As(&in) != nil {
		return v
	}
	out := make(map[string]tftypes.Value, len(in))
	for k, x := range in {
		out[k] = x
	}
	for _, a := range blk.Attributes {
		x, ok := out[a.Name]
		switch {
		case !ok:
		case a.WriteOnly:
			out[a.Name] = tftypes.NewValue(x.Type(), nil)
		case a.NestedType != nil:
			out[a.Name] = withoutWriteOnlyNested(a.NestedType.Nesting, &schema.Block{Attributes: a.NestedType.Attributes}, x)
		}
	}
	for _, nb := range blk.BlockTypes {
		if x, ok := out[nb.TypeName]; ok {
			out[nb.TypeName] = withoutWriteOnlyNested(nb.Nesting, nb.Block, x)
		}
	}
	return tftypes.NewValue(v.Type(), out)
}

// withoutWriteOnlyNested applies withoutWriteOnly to the elements of a nested
// block or nested attribute, whatever its nesting.
func withoutWriteOnlyNested(n schema.NestingMode, blk *schema.Block, v tftypes.Value) tftypes.Value {
	if v.IsNull() || !v.IsKnown() {
		return v
	}
	switch n {
	case schema.NestingSingle, schema.NestingGroup:
		return withoutWriteOnly(blk, v)
	case schema.NestingList, schema.NestingSet:
		var in []tftypes.Value
		if v.As(&in) != nil {
			return v
		}
		out := make([]tftypes.Value, len(in))
		for i, x := range in {
			out[i] = withoutWriteOnly(blk, x)
		}
		return tftypes.NewValue(v.Type(), out)
	case schema.NestingMap:
		var in map[string]tftypes.Value
		if v.As(&in) != nil {
			return v
		}
		out := make(map[string]tftypes.Value, len(in))
		for k, x := range in {
			out[k] = withoutWriteOnly(blk, x)
		}
		return tftypes.NewValue(v.Type(), out)
	}
	return v
}
