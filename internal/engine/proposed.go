package engine

import (
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/pravo-systems/pravo/internal/schema"
)

// ProposedNew merges prior state and configuration into the proposed new
// state handed to PlanResourceChange, following OpenTofu core's rules:
// configuration wins, except that a null configuration value for a computed
// attribute inherits the prior value.
func ProposedNew(blk *schema.Block, prior, cfg tftypes.Value) tftypes.Value {
	if cfg.IsNull() || !cfg.IsKnown() || prior.IsNull() || !prior.IsKnown() {
		return cfg
	}
	var pm, cm map[string]tftypes.Value
	if err := prior.As(&pm); err != nil {
		return cfg
	}
	if err := cfg.As(&cm); err != nil {
		return cfg
	}
	out := make(map[string]tftypes.Value, len(cm))
	for _, a := range blk.Attributes {
		cv, pv := cm[a.Name], pm[a.Name]
		switch {
		case a.Computed && cv.IsNull():
			out[a.Name] = pv
		case a.NestedType != nil:
			out[a.Name] = proposedNested(a.NestedType, pv, cv)
		default:
			out[a.Name] = cv
		}
	}
	for _, nb := range blk.BlockTypes {
		out[nb.TypeName] = proposedBlocks(nb, pm[nb.TypeName], cm[nb.TypeName])
	}
	return tftypes.NewValue(cfg.Type(), out)
}

func proposedBlocks(nb *schema.NestedBlock, prior, cfg tftypes.Value) tftypes.Value {
	if cfg.IsNull() || !cfg.IsKnown() || prior.IsNull() || !prior.IsKnown() {
		return cfg
	}
	switch nb.Nesting {
	case schema.NestingSingle, schema.NestingGroup:
		return ProposedNew(nb.Block, prior, cfg)
	case schema.NestingList:
		var pl, cl []tftypes.Value
		if prior.As(&pl) != nil || cfg.As(&cl) != nil || len(pl) != len(cl) {
			return cfg
		}
		out := make([]tftypes.Value, len(cl))
		for i := range cl {
			out[i] = ProposedNew(nb.Block, pl[i], cl[i])
		}
		return tftypes.NewValue(cfg.Type(), out)
	case schema.NestingMap:
		var pm, cm map[string]tftypes.Value
		if prior.As(&pm) != nil || cfg.As(&cm) != nil {
			return cfg
		}
		out := make(map[string]tftypes.Value, len(cm))
		for k, cv := range cm {
			if pv, ok := pm[k]; ok {
				out[k] = ProposedNew(nb.Block, pv, cv)
			} else {
				out[k] = cv
			}
		}
		return tftypes.NewValue(cfg.Type(), out)
	}
	// Sets cannot be correlated reliably; configuration wins.
	return cfg
}

func proposedNested(o *schema.Object, prior, cfg tftypes.Value) tftypes.Value {
	if cfg.IsNull() || !cfg.IsKnown() || prior.IsNull() || !prior.IsKnown() {
		return cfg
	}
	blk := &schema.Block{Attributes: o.Attributes}
	switch o.Nesting {
	case schema.NestingSingle:
		return ProposedNew(blk, prior, cfg)
	case schema.NestingList:
		var pl, cl []tftypes.Value
		if prior.As(&pl) != nil || cfg.As(&cl) != nil || len(pl) != len(cl) {
			return cfg
		}
		out := make([]tftypes.Value, len(cl))
		for i := range cl {
			out[i] = ProposedNew(blk, pl[i], cl[i])
		}
		return tftypes.NewValue(cfg.Type(), out)
	case schema.NestingMap:
		var pm, cm map[string]tftypes.Value
		if prior.As(&pm) != nil || cfg.As(&cm) != nil {
			return cfg
		}
		out := make(map[string]tftypes.Value, len(cm))
		for k, cv := range cm {
			if pv, ok := pm[k]; ok {
				out[k] = ProposedNew(blk, pv, cv)
			} else {
				out[k] = cv
			}
		}
		return tftypes.NewValue(cfg.Type(), out)
	}
	return cfg
}

// applyIgnoreChanges replaces the configured value of ignored top-level
// attributes with the prior value, when a prior exists.
func applyIgnoreChanges(blk *schema.Block, prior, cfg tftypes.Value, ignore []string) tftypes.Value {
	if len(ignore) == 0 || prior.IsNull() || cfg.IsNull() || !cfg.IsKnown() {
		return cfg
	}
	all := false
	set := map[string]bool{}
	for _, n := range ignore {
		if n == "*" {
			all = true
		}
		set[n] = true
	}
	var pm, cm map[string]tftypes.Value
	if prior.As(&pm) != nil || cfg.As(&cm) != nil {
		return cfg
	}
	out := make(map[string]tftypes.Value, len(cm))
	for k, v := range cm {
		if all || set[k] {
			if pv, ok := pm[k]; ok {
				out[k] = pv
				continue
			}
		}
		out[k] = v
	}
	return tftypes.NewValue(cfg.Type(), out)
}
