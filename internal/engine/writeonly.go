package engine

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/plugin"
	"github.com/pravo-systems/pravo/internal/schema"
	"github.com/pravo-systems/pravo/internal/sensitivity"
)

// withoutWriteOnly returns v with every write-only attribute set to null,
// in the block and in the blocks and nested attributes below it. A write-only
// value reaches the provider and nothing else: the state never holds it, and
// neither does a saved plan.
func withoutWriteOnly(blk *schema.Block, v tftypes.Value) tftypes.Value {
	return dropWriteOnly(blk, v, "", nil)
}

// dropWriteOnly is withoutWriteOnly for the block found at the path at. When
// set is given, it gets the path of each write-only attribute that held a value.
func dropWriteOnly(blk *schema.Block, v tftypes.Value, at string, set *[]string) tftypes.Value {
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
			if set != nil && !x.IsNull() {
				*set = append(*set, at+a.Name)
			}
			out[a.Name] = tftypes.NewValue(x.Type(), nil)
		case a.NestedType != nil:
			out[a.Name] = dropWriteOnlyNested(a.NestedType.Nesting, &schema.Block{Attributes: a.NestedType.Attributes}, x, at+a.Name+".", set)
		}
	}
	for _, nb := range blk.BlockTypes {
		if x, ok := out[nb.TypeName]; ok {
			out[nb.TypeName] = dropWriteOnlyNested(nb.Nesting, nb.Block, x, at+nb.TypeName+".", set)
		}
	}
	return tftypes.NewValue(v.Type(), out)
}

// dropWriteOnlyNested applies dropWriteOnly to the elements of a nested block
// or nested attribute, whatever its nesting.
func dropWriteOnlyNested(n schema.NestingMode, blk *schema.Block, v tftypes.Value, at string, set *[]string) tftypes.Value {
	if v.IsNull() || !v.IsKnown() {
		return v
	}
	switch n {
	case schema.NestingSingle, schema.NestingGroup:
		return dropWriteOnly(blk, v, at, set)
	case schema.NestingList, schema.NestingSet:
		var in []tftypes.Value
		if v.As(&in) != nil {
			return v
		}
		out := make([]tftypes.Value, len(in))
		for i, x := range in {
			out[i] = dropWriteOnly(blk, x, at, set)
		}
		return tftypes.NewValue(v.Type(), out)
	case schema.NestingMap:
		var in map[string]tftypes.Value
		if v.As(&in) != nil {
			return v
		}
		out := make(map[string]tftypes.Value, len(in))
		for k, x := range in {
			out[k] = dropWriteOnly(blk, x, at, set)
		}
		return tftypes.NewValue(v.Type(), out)
	}
	return v
}

// hasWriteOnly is sensitivity.HasWriteOnly, asked once for each block of a schema.
func (e *Engine) hasWriteOnly(blk *schema.Block) bool {
	if has, ok := e.writeOnly.Load(blk); ok {
		return has.(bool)
	}
	has := sensitivity.HasWriteOnly(blk)
	e.writeOnly.Store(blk, has)
	return has
}

// recordedHash is the digest of a configuration that a record and a plan keep:
// that of the configuration without its write-only values. The digest is short
// and has no key, so one that covered a write-only value would let a reader of
// the record try guesses of the value against it, and a value that is new on
// every run would make every plan reach the resource.
func (e *Engine) recordedHash(blk *schema.Block, cfg tftypes.Value) string {
	if e.hasWriteOnly(blk) {
		cfg = withoutWriteOnly(blk, cfg)
	}
	return configHash(cfg)
}

// writeOnlyGuard is a provider none of whose answers holds a write-only value.
// The protocol has a provider answer null for a write-only attribute. A
// provider that answers the value it was given would have it recorded, in the
// state or in a saved plan, so the guard sets it to null and says so in a
// warning. Refusing the answer of an apply would lose the record of an object
// that exists by then.
type writeOnlyGuard struct {
	plugin.Provider
	// block returns the block of a managed resource type, or nil.
	block func(ctx context.Context, typ string) (*schema.Block, error)
	// types holds, by type, whether its block has a write-only attribute.
	types sync.Map
}

// clean returns v, an object of the type typ that the provider answered, with
// no write-only value, and the diagnostics with a warning for what was dropped.
func (g *writeOnlyGuard) clean(ctx context.Context, typ string, v tftypes.Value, diags plugin.Diagnostics) (tftypes.Value, plugin.Diagnostics, error) {
	if has, ok := g.types.Load(typ); ok && !has.(bool) {
		return v, diags, nil
	}
	blk, err := g.block(ctx, typ)
	if err != nil {
		return v, diags, err
	}
	has := sensitivity.HasWriteOnly(blk)
	g.types.Store(typ, has)
	if !has {
		return v, diags, nil
	}
	var set []string
	out := dropWriteOnly(blk, v, "", &set)
	if len(set) == 0 {
		return v, diags, nil
	}
	slices.Sort(set)
	set = slices.Compact(set)
	return out, append(diags, plugin.Diagnostic{
		Severity: "warning",
		Summary:  "The provider returned a value for a write-only attribute of " + typ + ": " + strings.Join(set, ", "),
		Detail:   "A provider answers null for a write-only attribute. pravo dropped the value, so it is not recorded. This is a fault of the provider, to report to its authors.",
	}), nil
}

func (g *writeOnlyGuard) UpgradeResourceState(ctx context.Context, typ string, version int64, jsonState []byte, want tftypes.Type) (tftypes.Value, plugin.Diagnostics, error) {
	v, diags, err := g.Provider.UpgradeResourceState(ctx, typ, version, jsonState, want)
	if err != nil {
		return v, diags, err
	}
	return g.clean(ctx, typ, v, diags)
}

func (g *writeOnlyGuard) ReadResource(ctx context.Context, typ string, state tftypes.Value, private []byte) (tftypes.Value, []byte, plugin.Diagnostics, error) {
	v, newPrivate, diags, err := g.Provider.ReadResource(ctx, typ, state, private)
	if err != nil {
		return v, newPrivate, diags, err
	}
	v, diags, err = g.clean(ctx, typ, v, diags)
	return v, newPrivate, diags, err
}

func (g *writeOnlyGuard) PlanResourceChange(ctx context.Context, typ string, prior, proposed, config tftypes.Value, priorPrivate []byte) (*plugin.PlanResult, plugin.Diagnostics, error) {
	res, diags, err := g.Provider.PlanResourceChange(ctx, typ, prior, proposed, config, priorPrivate)
	if err != nil || res == nil {
		return res, diags, err
	}
	res.PlannedState, diags, err = g.clean(ctx, typ, res.PlannedState, diags)
	return res, diags, err
}

func (g *writeOnlyGuard) ApplyResourceChange(ctx context.Context, typ string, prior, planned, config tftypes.Value, plannedPrivate []byte) (*plugin.ApplyResult, plugin.Diagnostics, error) {
	res, diags, err := g.Provider.ApplyResourceChange(ctx, typ, prior, planned, config, plannedPrivate)
	if err != nil || res == nil {
		return res, diags, err
	}
	res.NewState, diags, err = g.clean(ctx, typ, res.NewState, diags)
	return res, diags, err
}

func (g *writeOnlyGuard) ImportResourceState(ctx context.Context, typ string, target plugin.ImportTarget, types map[string]tftypes.Type) ([]plugin.ImportedResource, plugin.Diagnostics, error) {
	imported, diags, err := g.Provider.ImportResourceState(ctx, typ, target, types)
	if err != nil {
		return imported, diags, err
	}
	for i := range imported {
		if imported[i].State, diags, err = g.clean(ctx, imported[i].TypeName, imported[i].State, diags); err != nil {
			return imported, diags, err
		}
	}
	return imported, diags, nil
}
