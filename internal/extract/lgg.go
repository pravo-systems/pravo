package extract

// The kinds of a node of a generalisation.
const (
	GConst  = iota // the same in every instance
	GStruct        // a struct: some fields in every instance, some in a few
	GList          // lists of one length
	GCall          // the same function, generalised argument by argument
	GInterp        // the same text between the expressions
	GHole          // the instances differ here
)

// Gen is the least general generalisation of the bodies of a cluster (Plotkin 1970), over the
// normal form: what is equal stays, and what differs becomes a hole.
type Gen struct {
	Kind   int
	Nodes  []*Node // what each instance has here
	Fields []*GField
	Elems  []*Gen
	Path   string // where it is, for the report
	Label  string // the field it is the value of, or is under
	Parent string // the field above that one

	// For a hole, set when the holes are turned into parameters.
	Kept  bool   // every instance writes this itself
	Param *Param // or this parameter fills it,
	Pre   string // between this text
	Suf   string // and this one
}

// GField is a field of a generalised struct.
type GField struct {
	Label, LabelSrc string
	G               *Gen    // when every instance has the field
	Opt             []*Node // when only some have it: what each has, or nil
}

func lgg(nodes []*Node, path, label, parent string) *Gen {
	g := &Gen{Nodes: nodes, Path: path, Label: label, Parent: parent}
	same, kind := true, nodes[0].Kind
	for _, n := range nodes[1:] {
		if n.Key != nodes[0].Key {
			same = false
		}
		if n.Kind != kind {
			kind = -1
		}
	}
	if same {
		g.Kind = GConst
		return g
	}
	switch kind {
	case KStruct:
		g.Kind = GStruct
		seen := map[string]bool{}
		for _, n := range nodes {
			for _, f := range n.Fields {
				if seen[f.Label] {
					continue
				}
				seen[f.Label] = true
				vals := make([]*Node, len(nodes))
				all := true
				for i, m := range nodes {
					if mf := m.field(f.Label); mf != nil {
						vals[i] = mf.Val
					} else {
						all = false
					}
				}
				gf := &GField{Label: f.Label, LabelSrc: f.LabelSrc}
				if all {
					p := f.Label
					if path != "" {
						p = path + "." + f.Label
					}
					gf.G = lgg(vals, p, f.Label, label)
				} else {
					gf.Opt = vals
				}
				g.Fields = append(g.Fields, gf)
			}
		}
		return g
	case KList, KCall, KInterp:
		for _, n := range nodes[1:] {
			if len(n.Elems) != len(nodes[0].Elems) || n.Fun != nodes[0].Fun || !sameStrings(n.Static, nodes[0].Static) {
				g.Kind = GHole
				return g
			}
		}
		g.Kind = map[int]int{KList: GList, KCall: GCall, KInterp: GInterp}[kind]
		for j := range nodes[0].Elems {
			vals := make([]*Node, len(nodes))
			for i, n := range nodes {
				vals[i] = n.Elems[j]
			}
			p := path
			switch kind {
			case KList:
				p += "[" + itoa(j) + "]"
			case KCall:
				p += "(" + nodes[0].Fun + ")"
			}
			g.Elems = append(g.Elems, lgg(vals, p, label, parent))
		}
		return g
	}
	g.Kind = GHole
	return g
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// holeFamily tells whether a parameter can fill the hole, and of which family: every instance has
// a literal of one family there. References are a family of their own, which only --ref-params
// lets a parameter carry (pravo writes null for it: see RESULT.md, E1).
func holeFamily(g *Gen, opt *Options) (string, bool) {
	family := ""
	for _, n := range g.Nodes {
		f, ok := n.scalar()
		if !ok {
			if opt.RefParams && n.Kind == KOther {
				f = "ref"
			} else {
				return "", false
			}
		}
		if family != "" && f != family {
			return "", false
		}
		family = f
	}
	return family, true
}

// expressible tells whether a generalisation can be written inside a call or an interpolation,
// where an instance cannot add to it by unification: nothing optional, and every hole one that a
// parameter fills.
func expressible(g *Gen, opt *Options) bool {
	switch g.Kind {
	case GConst:
		return true
	case GHole:
		_, ok := holeFamily(g, opt)
		return ok
	case GStruct:
		for _, f := range g.Fields {
			if f.Opt != nil || !expressible(f.G, opt) {
				return false
			}
		}
		return true
	}
	for _, e := range g.Elems {
		if !expressible(e, opt) {
			return false
		}
	}
	return true
}

// settle decides, hole by hole, between a parameter and a field that every instance writes. A call
// or an interpolation that cannot be generalised inside becomes one hole as a whole.
func settle(g *Gen, opt *Options) {
	switch g.Kind {
	case GHole:
		_, ok := holeFamily(g, opt)
		g.Kept = !ok
	case GStruct:
		for _, f := range g.Fields {
			if f.G != nil {
				settle(f.G, opt)
			}
		}
	case GList:
		for _, e := range g.Elems {
			settle(e, opt)
		}
	case GCall, GInterp:
		if !expressible(g, opt) {
			g.Kind, g.Kept, g.Fields, g.Elems = GHole, true, nil, nil
		}
	}
}

// holes lists the holes that parameters fill, in the order of the definition.
func holes(g *Gen, out []*Gen) []*Gen {
	switch g.Kind {
	case GHole:
		if !g.Kept {
			out = append(out, g)
		}
	case GStruct:
		for _, f := range g.Fields {
			if f.G != nil {
				out = holes(f.G, out)
			}
		}
	default:
		for _, e := range g.Elems {
			out = holes(e, out)
		}
	}
	return out
}

// keptPaths lists the fields that every instance writes itself, and optionalPaths the fields that
// only some instances have.
func keptPaths(g *Gen, kept, optional map[string]int) {
	switch g.Kind {
	case GHole:
		if g.Kept {
			kept[g.Path] = len(g.Nodes)
		}
	case GStruct:
		for _, f := range g.Fields {
			if f.G != nil {
				keptPaths(f.G, kept, optional)
				continue
			}
			n := 0
			for _, v := range f.Opt {
				if v != nil {
					n++
				}
			}
			p := f.Label
			if g.Path != "" {
				p = g.Path + "." + f.Label
			}
			optional[p] = n
		}
	default:
		for _, e := range g.Elems {
			keptPaths(e, kept, optional)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for ; i > 0; i /= 10 {
		s = string(rune('0'+i%10)) + s
	}
	return s
}
