package extract

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Param is a parameter of a definition.
type Param struct {
	Name     string
	Family   string   // string, number, bool, ref
	Type     string   // string, int, number, bool, _
	Vals     []string // the argument of each instance: the content of a string, or the text of the value
	Default  *string
	Uses     []*Gen
	Distinct int
	Merged   int      // the holes it fills beyond the first, which had the same values in every instance
	Derived  int      // the holes it fills inside a larger text
	Notes    []string // what was decided, for the report
}

// makeParams turns the holes of a generalisation into parameters: holes that hold the same values
// in every instance share one, a text that is a parameter between two constant texts is written as
// an interpolation, and a value that most instances share becomes the default.
func makeParams(g *Gen, n int, opt *Options) []*Param {
	var params []*Param
	full := map[*Param][]string{} // the whole values of a parameter whose argument is a part of them
	for _, gr := range holeGroups(g, n, opt) {
		// A text that is an earlier parameter between two constant texts.
		if gr.family == "string" && !opt.NoAffix && joinParam(gr, params, full, n, opt) {
			continue
		}
		params = append(params, newParam(gr, params, full, n, opt))
	}
	return params
}

// holeGroup is the holes that one parameter stands for.
type holeGroup struct {
	family string
	vals   []string
	holes  []*Gen
}

// holeGroups gathers the holes: those with the same values in every instance share a group,
// unless they are flags, or numbers with too few different values to be one thing.
func holeGroups(g *Gen, n int, opt *Options) []*holeGroup {
	var groups []*holeGroup
	byTuple := map[string]*holeGroup{}
	for _, h := range holes(g, nil) {
		family, _ := holeFamily(h, opt)
		vals := make([]string, n)
		for i, nd := range h.Nodes {
			if family == "ref" {
				vals[i] = nd.Text
			} else {
				vals[i] = nd.LitVal
			}
		}
		key := family + "\x00" + strings.Join(vals, "\x00")
		merge := !opt.NoMerge
		switch family {
		case "bool":
			merge = false // two flags that agree in every instance are two flags
		case "number":
			merge = merge && distinct(vals) >= opt.MergeNumbers
		}
		if gr := byTuple[key]; gr != nil && merge {
			gr.holes = append(gr.holes, h)
			continue
		}
		gr := &holeGroup{family: family, vals: vals, holes: []*Gen{h}}
		groups = append(groups, gr)
		if merge {
			byTuple[key] = gr
		}
	}
	return groups
}

// joinParam writes the holes of gr as a text around an earlier parameter, when they are one.
func joinParam(gr *holeGroup, params []*Param, full map[*Param][]string, n int, opt *Options) bool {
	for _, p := range params {
		if p.Family != "string" {
			continue
		}
		for _, base := range [][]string{p.Vals, full[p]} {
			pre, suf, ok := template(gr.vals, base)
			if !ok || opt.NoMerge && pre == "" && suf == "" {
				continue
			}
			for _, h := range gr.holes {
				h.Param, h.Pre, h.Suf = p, pre, suf
				p.Uses = append(p.Uses, h)
			}
			p.Derived += len(gr.holes)
			p.Notes = append(p.Notes, fmt.Sprintf("%s is %s in all %d instances", paths(gr.holes), show(pre, p.Name, suf), n))
			return true
		}
	}
	return false
}

// newParam makes the parameter of a group of holes.
func newParam(gr *holeGroup, params []*Param, full map[*Param][]string, n int, opt *Options) *Param {
	p := &Param{Family: gr.family, Vals: gr.vals, Uses: gr.holes, Distinct: distinct(gr.vals), Merged: len(gr.holes) - 1}
	pre, suf := "", ""
	if gr.family == "string" && !opt.NoAffix {
		if a, b, mids, ok := affix(gr.vals); ok {
			pre, suf = a, b
			full[p] = gr.vals
			p.Vals = mids
		}
	}
	for _, h := range gr.holes {
		h.Param, h.Pre, h.Suf = p, pre, suf
	}
	switch gr.family {
	case "string":
		p.Type = "string"
	case "bool":
		p.Type = "bool"
	case "ref":
		p.Type = "_"
	default:
		p.Type = "int"
		for _, v := range gr.vals {
			if strings.ContainsAny(v, ".eE") {
				p.Type = "number"
			}
		}
	}
	p.Name = paramName(gr.holes, params)
	if len(gr.holes) > 1 {
		p.Notes = append(p.Notes, fmt.Sprintf("%s hold the same value in all %d instances (%d different values): one parameter", paths(gr.holes), n, p.Distinct))
	}
	if pre != "" || suf != "" {
		p.Notes = append(p.Notes, fmt.Sprintf("every value of %s is %s: the parameter is the part that differs", paths(gr.holes), show(pre, p.Name, suf)))
	}
	if !opt.NoDefaults && p.Family != "ref" && n >= 3 {
		top, count := mostCommon(p.Vals)
		if float64(count) >= opt.DefaultShare*float64(n) && count < n {
			p.Default = &top
			p.Notes = append(p.Notes, fmt.Sprintf("%d of %d instances have %s: it is the default", count, n, p.argText(top)))
		}
	}
	return p
}

func paths(hs []*Gen) string {
	var out []string
	for _, h := range hs {
		out = append(out, h.Path)
	}
	return strings.Join(out, " and ")
}

func show(pre, name, suf string) string {
	if pre == "" && suf == "" {
		return "#" + name
	}
	return `"` + quoteInner(pre) + `\(#` + name + `)` + quoteInner(suf) + `"`
}

// argText is a value of the parameter as CUE.
func (p *Param) argText(v string) string {
	if p.Family == "string" {
		return `"` + quoteInner(v) + `"`
	}
	return v
}

// use is the expression that stands where the hole was.
func (p *Param) use(h *Gen) string {
	return show(h.Pre, p.Name, h.Suf)
}

// decl is the declaration of the parameter in the definition. A parameter without a default is
// required (!): an instance that forgets it fails pravo validate. Without the mark pravo would
// write null and say nothing.
func (p *Param) decl() string {
	if p.Default != nil {
		return fmt.Sprintf("#%s: *%s | %s", p.Name, p.argText(*p.Default), p.Type)
	}
	return fmt.Sprintf("#%s!: %s", p.Name, p.Type)
}

func distinct(vals []string) int {
	seen := map[string]bool{}
	for _, v := range vals {
		seen[v] = true
	}
	return len(seen)
}

func mostCommon(vals []string) (string, int) {
	count := map[string]int{}
	best, n := "", 0
	for _, v := range vals {
		count[v]++
		if count[v] > n {
			best, n = v, count[v]
		}
	}
	return best, n
}

func isWord(r byte) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

// affix finds the text that every value starts with and the text every value ends with, cut where
// a word ends, and gives the parts in between. It gives up on a text with a space in it (a
// sentence is no naming scheme), on a part that is empty, and on a part without a letter (the
// middle of an address or of a number).
func affix(vals []string) (pre, suf string, mids []string, ok bool) {
	pre = vals[0]
	for _, v := range vals {
		if strings.ContainsAny(v, " \t\n") {
			return "", "", nil, false
		}
		for !strings.HasPrefix(v, pre) {
			pre = pre[:len(pre)-1]
		}
	}
	for pre != "" && isWord(pre[len(pre)-1]) {
		pre = pre[:len(pre)-1]
	}
	suf = vals[0][len(pre):]
	for _, v := range vals {
		rest := v[len(pre):]
		for !strings.HasSuffix(rest, suf) {
			suf = suf[1:]
		}
	}
	for suf != "" && isWord(suf[0]) {
		suf = suf[1:]
	}
	if len(pre)+len(suf) < 3 {
		return "", "", nil, false
	}
	for _, v := range vals {
		mid := v[len(pre) : len(v)-len(suf)]
		if mid == "" || !strings.ContainsFunc(mid, unicode.IsLetter) {
			return "", "", nil, false
		}
		mids = append(mids, mid)
	}
	return pre, suf, mids, true
}

// template tells whether every value is the same two texts around the value of the base.
func template(vals, base []string) (pre, suf string, ok bool) {
	if base == nil {
		return "", "", false
	}
	for _, b := range base {
		if len(b) < 2 {
			return "", "", false
		}
	}
	for from := 0; ; {
		at := strings.Index(vals[0][from:], base[0])
		if at < 0 {
			return "", "", false
		}
		at += from
		pre, suf = vals[0][:at], vals[0][at+len(base[0]):]
		fits := true
		for i, v := range vals {
			if v != pre+base[i]+suf {
				fits = false
				break
			}
		}
		if fits {
			return pre, suf, true
		}
		from = at + 1
	}
}

// paramName names a parameter after the field it first fills. Holes under fields whose names end
// in the same words are named by those words (from_port and to_port give port).
func paramName(hs []*Gen, taken []*Param) string {
	words := func(label string) []string {
		var b strings.Builder
		for i, r := range label {
			switch {
			case unicode.IsUpper(r):
				if i > 0 && b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
					prev := rune(label[i-1])
					if unicode.IsLower(prev) || unicode.IsDigit(prev) {
						b.WriteByte('_')
					}
				}
				b.WriteRune(unicode.ToLower(r))
			case unicode.IsLetter(r) || unicode.IsDigit(r):
				b.WriteRune(r)
			default:
				b.WriteByte('_')
			}
		}
		var out []string
		for _, w := range strings.Split(b.String(), "_") {
			if w != "" {
				out = append(out, w)
			}
		}
		return out
	}
	common := words(hs[0].Label)
	for _, h := range hs[1:] {
		w := words(h.Label)
		k := 0
		for k < len(common) && k < len(w) && common[len(common)-1-k] == w[len(w)-1-k] {
			k++
		}
		common = common[len(common)-k:]
	}
	name := strings.Join(common, "_")
	if name == "" {
		name = strings.Join(words(hs[0].Label), "_")
	}
	if strings.HasSuffix(hs[0].Path, "]") && strings.HasSuffix(name, "s") && !strings.HasSuffix(name, "ss") && len(name) > 3 {
		name = name[:len(name)-1] // one element of cidr_blocks is a cidr_block
	}
	if name == "" || unicode.IsDigit(rune(name[0])) {
		name = "p" + name
	}
	used := func(s string) bool {
		for _, p := range taken {
			if p.Name == s {
				return true
			}
		}
		return false
	}
	if used(name) {
		if parent := strings.Join(words(hs[0].Parent), "_"); parent != "" && !used(parent+"_"+name) {
			return parent + "_" + name
		}
		for i := 2; ; i++ {
			if s := fmt.Sprintf("%s%d", name, i); !used(s) {
				return s
			}
		}
	}
	return name
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
