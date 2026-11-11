package extract

import (
	"fmt"
	"sort"
	"strings"
)

// cand is a set of resources of one type that may become a definition.
type cand struct {
	members []*Res
	x       *Extraction // nil for a single resource
}

// value is what writing the candidate as a definition saves, in bytes: the description length
// before, less the description length after. A candidate that saves nothing stays as it is.
func value(x *Extraction) int {
	if x == nil || x.saving() < 0 {
		return 0
	}
	return x.saving()
}

func memberKey(a, b []*Res) string {
	ids := make([]int, 0, len(a)+len(b))
	for _, r := range a {
		ids = append(ids, r.Seq)
	}
	for _, r := range b {
		ids = append(ids, r.Seq)
	}
	sort.Ints(ids)
	return fmt.Sprint(ids)
}

// clusterType groups the resources of one type. Resources of one shape (the same fields, whatever
// their values) start together; then the two groups whose union saves the most bytes are joined,
// as long as a join saves more than MergeGain.
func clusterType(rs []*Res, name string, opt *Options, st *Stats) []*cand {
	byShape := map[string]*cand{}
	var cs []*cand
	for _, r := range rs {
		c := byShape[r.Tree.Shape]
		if c == nil {
			c = &cand{}
			byShape[r.Tree.Shape] = c
			cs = append(cs, c)
		}
		c.members = append(c.members, r)
	}
	st.Shapes += len(cs)
	for _, c := range cs {
		if len(c.members) > 1 {
			c.x = generalise(append([]*Res(nil), c.members...), name, opt)
			st.Builds++
		}
	}
	cache := map[string]*Extraction{}
	for len(cs) > 1 {
		best, bi, bj := opt.MergeGain, -1, -1
		var bx *Extraction
		for i := range cs {
			for j := i + 1; j < len(cs); j++ {
				key := memberKey(cs[i].members, cs[j].members)
				x, ok := cache[key]
				if !ok {
					x = generalise(append(append([]*Res(nil), cs[i].members...), cs[j].members...), name, opt)
					cache[key] = x
					st.Builds++
				}
				if d := value(x) - value(cs[i].x) - value(cs[j].x); d > best {
					best, bi, bj, bx = d, i, j, x
				}
			}
		}
		if bx == nil {
			break
		}
		cs[bi] = &cand{members: bx.Members, x: bx}
		cs = append(cs[:bj], cs[bj+1:]...)
		st.Merges++
	}
	return cs
}

// Left is a resource that stays as it is, and why.
type Left struct {
	Res    *Res
	Reason string
}

// Stats counts the work of a run.
type Stats struct {
	Shapes, Builds, Merges int
}

// Result is what a run decided.
type Result struct {
	Spec    *Spec
	Defs    []*Extraction
	Left    []*Left
	Skipped []*Res // left out by --skip or --only
	Stats   Stats
}

func decide(sp *Spec, opt *Options) *Result {
	res := &Result{Spec: sp}
	type typeKey struct{ prov, typ string }
	var order []typeKey
	byType := map[typeKey][]*Res{}
	for _, r := range sp.Res {
		if opt.Skip[r.Label] || len(opt.Only) > 0 && !opt.Only[r.Type] {
			res.Skipped = append(res.Skipped, r)
			continue
		}
		k := typeKey{r.Prov, r.Type}
		if byType[k] == nil {
			order = append(order, k)
		}
		byType[k] = append(byType[k], r)
	}
	taken := map[string]bool{}
	for n := range sp.Names {
		taken[n] = true
	}
	var chosen []*cand
	rejected := map[*Res]string{}
	for _, k := range order {
		rs := byType[k]
		if len(rs) < opt.MinInstances {
			for _, r := range rs {
				rejected[r] = fmt.Sprintf("only %d of type %s (a definition needs %d)", len(rs), k.typ, opt.MinInstances)
			}
			continue
		}
		for _, c := range clusterType(rs, defName(k.typ, nil), opt, &res.Stats) {
			why := ""
			switch {
			case len(c.members) < opt.MinInstances:
				why = fmt.Sprintf("only %d alike (a definition needs %d)", len(c.members), opt.MinInstances)
			case float64(c.x.saving())*100 < opt.MinSaving*float64(c.x.Before):
				why = fmt.Sprintf("the %d alike share too little: a definition would save %d of %d bytes (%.0f%%, the least is %.0f%%)",
					len(c.members), c.x.saving(), c.x.Before, 100*float64(c.x.saving())/float64(c.x.Before), opt.MinSaving)
			case len(c.x.Params) > opt.MaxParams:
				why = fmt.Sprintf("the %d alike would need %d parameters (the most is %d)", len(c.members), len(c.x.Params), opt.MaxParams)
			}
			if why != "" {
				for _, r := range c.members {
					rejected[r] = why
				}
				continue
			}
			chosen = append(chosen, c)
		}
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].members[0].Seq < chosen[j].members[0].Seq })
	for _, c := range chosen {
		name := defName(c.members[0].Type, taken)
		if alias := opt.Rename[name]; alias != "" {
			name = alias
		}
		if len(opt.Pick) > 0 && !opt.Pick[name] {
			for _, r := range c.members {
				rejected[r] = "its definition " + name + " was not picked"
			}
			continue
		}
		taken[name] = true
		res.Defs = append(res.Defs, generalise(c.members, name, opt))
	}
	// Why a resource that stays is unlike the definition of its type.
	for _, r := range sp.Res {
		why, ok := rejected[r]
		if !ok {
			continue
		}
		for _, x := range res.Defs {
			if x.Type != r.Type || x.Members[0].Prov != r.Prov {
				continue
			}
			with := generalise(append(append([]*Res(nil), x.Members...), r), x.Name, opt)
			var more []string
			for _, p := range sortedKeys(with.Kept) {
				if _, had := x.Kept[p]; !had {
					more = append(more, p+" written by every instance")
				}
			}
			if d := len(with.Params) - len(x.Params); d > 0 {
				more = append(more, fmt.Sprintf("%d more parameters", d))
			}
			var b strings.Builder
			b.WriteString(why)
			fmt.Fprintf(&b, "; in %s it would cost %d bytes more than it saves", x.Name, x.saving()-with.saving())
			if len(more) > 0 {
				b.WriteString(" (" + strings.Join(more, ", ") + ")")
			}
			why = b.String()
		}
		res.Left = append(res.Left, &Left{r, why})
	}
	return res
}
