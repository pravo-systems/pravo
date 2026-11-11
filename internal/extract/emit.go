package extract

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Extraction is a cluster written as one definition and its instances.
type Extraction struct {
	Name     string
	Type     string
	Head     string
	Members  []*Res // in the order of the specification
	G        *Gen
	Params   []*Param
	DefText  string   // the whole declaration of the definition
	InstText []string // the new value of each member
	Before   int      // bytes of the members as they were
	After    int      // bytes of the definition and of the instances
	Kept     map[string]int
	Optional map[string]int
}

func (x *Extraction) saving() int { return x.Before - x.After }

// generalise generalises the members and writes the definition and the instances.
func generalise(members []*Res, name string, opt *Options) *Extraction {
	sort.Slice(members, func(i, j int) bool { return members[i].Seq < members[j].Seq })
	x := &Extraction{Name: name, Type: members[0].Type, Head: members[0].Head, Members: members,
		Kept: map[string]int{}, Optional: map[string]int{}}
	nodes := make([]*Node, len(members))
	for i, m := range members {
		nodes[i] = m.Tree
		x.Before += m.srcLen()
	}
	x.G = lgg(nodes, "", "", "")
	settle(x.G, opt)
	x.Params = makeParams(x.G, len(members), opt)
	keptPaths(x.G, x.Kept, x.Optional)

	var lines []string
	for _, p := range x.Params {
		lines = append(lines, p.decl())
	}
	parts, _ := structParts(x.G)
	if len(lines) > 0 && len(parts) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, parts...)
	x.DefText = name + ": " + x.Head + " & {\n\t" + strings.Join(lines, "\n\t") + "\n}"
	x.After = len(x.DefText) + 1
	for i, m := range members {
		var args []string
		for _, p := range x.Params {
			if p.Default != nil && *p.Default == p.Vals[i] {
				continue
			}
			args = append(args, "#"+p.Name+": "+p.argText(p.Vals[i]))
		}
		if o := overlay(x.G, i); o != nil {
			for _, f := range o.Fields {
				args = append(args, ovField(f))
			}
		}
		text := name
		if len(args) > 0 {
			text = name + " & {" + strings.Join(args, ", ") + "}"
			if len(m.Label)+2+len(text) > opt.Width || strings.Contains(text, "\n") {
				text = name + " & {\n\t" + strings.Join(args, "\n\t") + "\n}"
			}
		}
		x.InstText = append(x.InstText, text)
		x.After += len(m.Label) + 2 + len(text)
	}
	return x
}

// structParts writes the fields of a generalised struct that belong to the definition. kept tells
// that some field was left to the instances.
func structParts(g *Gen) (parts []string, kept bool) {
	for _, f := range g.Fields {
		if f.Opt != nil {
			continue
		}
		if f.G.Kind == GStruct {
			sub, k := structParts(f.G)
			switch {
			case len(sub) == 0 && k:
				kept = true
			case len(sub) == 1 && !strings.Contains(sub[0], "\n"):
				parts = append(parts, f.LabelSrc+": "+sub[0])
			default:
				parts = append(parts, f.LabelSrc+": "+braces(sub, f.G.Nodes[0].OneLine))
			}
			continue
		}
		if f.G.Kind == GConst {
			parts = append(parts, constField(f.LabelSrc, f.G.Nodes[0]))
			continue
		}
		t, ok := genText(f.G)
		if !ok {
			kept = true
			continue
		}
		parts = append(parts, f.LabelSrc+": "+t)
	}
	return parts, kept
}

// constField writes a field whose value is the same in every instance, as the first one wrote it.
// A struct of one field keeps the short form a: b: c.
func constField(label string, n *Node) string {
	if n.Kind == KStruct && len(n.Fields) == 1 {
		return label + ": " + constField(n.Fields[0].LabelSrc, n.Fields[0].Val)
	}
	return label + ": " + nodeText(n.Expr)
}

func braces(parts []string, oneLine bool) string {
	if len(parts) == 0 {
		return "{}"
	}
	if oneLine {
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return "{\n\t" + strings.Join(indent(parts), "\n\t") + "\n}"
}

func indent(parts []string) []string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.ReplaceAll(p, "\n", "\n\t")
	}
	return out
}

// genText writes a generalisation as it stands in the definition. ok is false for what the
// instances write themselves.
func genText(g *Gen) (text string, ok bool) {
	switch g.Kind {
	case GConst:
		return nodeText(g.Nodes[0].Expr), true
	case GHole:
		if g.Kept {
			return "", false
		}
		return g.Param.use(g), true
	case GStruct:
		parts, kept := structParts(g)
		if len(parts) == 0 && kept {
			return "", false
		}
		return braces(parts, g.Nodes[0].OneLine), true
	case GList:
		var elems []string
		for _, e := range g.Elems {
			t, ok := genText(e)
			if !ok {
				t = "_" // an instance fills this place
			}
			elems = append(elems, t)
		}
		if g.Nodes[0].OneLine || len(elems) == 1 {
			return "[" + strings.Join(elems, ", ") + "]", true
		}
		return "[\n\t" + strings.Join(indent(elems), ",\n\t") + ",\n]", true
	case GCall:
		var args []string
		for _, e := range g.Elems {
			t, _ := genText(e)
			args = append(args, t)
		}
		return g.Nodes[0].Fun + "(" + strings.Join(args, ", ") + ")", true
	case GInterp:
		var b strings.Builder
		b.WriteByte('"')
		for i, e := range g.Elems {
			b.WriteString(quoteInner(g.Nodes[0].Static[i]))
			t, _ := genText(e)
			b.WriteString(`\(` + t + `)`)
		}
		b.WriteString(quoteInner(g.Nodes[0].Static[len(g.Elems)]))
		b.WriteByte('"')
		return b.String(), true
	}
	return "", false
}

// Ov is what an instance writes beside its parameters: the fields only it has, and the fields that
// no parameter can carry.
type Ov struct {
	Text   string // a leaf
	Fields []*OvField
	Elems  []*Ov // a list: nil where the definition says it all
	List   bool
}

// OvField is a field of an overlay.
type OvField struct {
	Label string
	Val   *Ov
}

func overlay(g *Gen, i int) *Ov {
	switch g.Kind {
	case GHole:
		if g.Kept {
			return &Ov{Text: nodeText(g.Nodes[i].Expr)}
		}
	case GStruct:
		o := &Ov{}
		for _, f := range g.Fields {
			if f.Opt != nil {
				if f.Opt[i] != nil {
					o.Fields = append(o.Fields, &OvField{f.LabelSrc, &Ov{Text: nodeText(f.Opt[i].Expr)}})
				}
				continue
			}
			if sub := overlay(f.G, i); sub != nil {
				o.Fields = append(o.Fields, &OvField{f.LabelSrc, sub})
			}
		}
		if len(o.Fields) > 0 {
			return o
		}
	case GList:
		o := &Ov{List: true}
		found := false
		for _, e := range g.Elems {
			sub := overlay(e, i)
			found = found || sub != nil
			o.Elems = append(o.Elems, sub)
		}
		if found {
			return o
		}
	}
	return nil
}

func ovField(f *OvField) string {
	if !f.Val.List && f.Val.Text == "" && len(f.Val.Fields) == 1 {
		return f.Label + ": " + ovField(f.Val.Fields[0])
	}
	return f.Label + ": " + ovText(f.Val)
}

func ovText(o *Ov) string {
	switch {
	case o == nil:
		return "_"
	case o.List:
		var elems []string
		for _, e := range o.Elems {
			elems = append(elems, ovText(e))
		}
		return "[" + strings.Join(elems, ", ") + "]"
	case o.Text != "":
		return o.Text
	}
	var parts []string
	for _, f := range o.Fields {
		parts = append(parts, ovField(f))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// defName names a definition after the type of its resources: aws_s3_bucket gives #S3Bucket.
func defName(typ string, taken map[string]bool) string {
	words := strings.Split(typ, "_")
	if len(words) > 1 {
		words = words[1:] // the first word is the provider
	}
	var b strings.Builder
	b.WriteByte('#')
	for _, w := range words {
		if w == "" {
			continue
		}
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	name := b.String()
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s%d", b.String(), i)
	}
	return name
}
