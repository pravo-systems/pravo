//go:build ignore

// derive builds the "plans touch what changed" demo from
// the real runs recorded in site/data/blast.json (written by tools/capture
// from site/demos/blast/page.md) and from the files of page.md.
//
// It reads the 39 nodes and their edges from `pravo state query` and
// `pravo state graph`, the four scenes from `pravo plan`, `pravo state locks`
// and `pravo apply`, and the CUE of every node from the module files. It lays
// the graph out (nodes in columns by depth, one row per module instance,
// edges implied by a longer path left out of the drawing), adds the "view"
// object to site/data/blast.json, and writes site/demos/blast/embed.html from
// the template site/demos/blast/band.html. Nothing in the view is typed by
// hand. It checks every claim that the scene texts make against the record
// and exits 1 when one fails.
//
//	go run site/demos/blast/derive.go
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	pagePath = "site/demos/blast/page.md"
	dataPath = "site/data/blast.json"
	tmplPath = "site/demos/blast/band.html"
	outPath  = "site/demos/blast/embed.html"
)

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "derive: "+format+"\n", a...)
	os.Exit(1)
}

func must(cond bool, format string, a ...any) {
	if !cond {
		fail(format, a...)
	}
}

// ---------------------------------------------------------------- input

type fence struct {
	Line  int
	Lang  string
	Attrs map[string]string
	Run   bool
	Out   bool
	Body  []string
}

var (
	fenceRe = regexp.MustCompile("^```(\\S*)\\s*(.*)$")
	attrRe  = regexp.MustCompile(`(\w+)=("[^"]*"|\S+)`)
)

func parseFences(path string) []fence {
	b, err := os.ReadFile(path)
	if err != nil {
		fail("%v", err)
	}
	lines := strings.Split(string(b), "\n")
	var res []fence
	for i := 0; i < len(lines); i++ {
		m := fenceRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		f := fence{Line: i + 1, Lang: m[1], Attrs: map[string]string{}}
		for _, a := range attrRe.FindAllStringSubmatch(m[2], -1) {
			f.Attrs[a[1]] = strings.Trim(a[2], `"`)
		}
		for _, w := range strings.Fields(m[2]) {
			if w == "run" || m[1] == "run" {
				f.Run = true
			}
			if w == "out" {
				f.Out = true
			}
		}
		if m[1] == "run" {
			f.Run = true
		}
		j := i + 1
		for j < len(lines) && lines[j] != "```" {
			j++
		}
		f.Body = lines[i+1 : j]
		res = append(res, f)
		i = j
	}
	return res
}

type jblock struct {
	Block   int    `json:"block"`
	Line    int    `json:"line"`
	Command string `json:"command"`
	Exit    int    `json:"exit"`
	Output  string `json:"output"`
	Hidden  bool   `json:"hidden"`
}

type doc struct {
	Page    string            `json:"page"`
	Title   string            `json:"title,omitempty"`
	Version []string          `json:"version,omitempty"`
	Blocks  []json.RawMessage `json:"blocks"`
	View    any               `json:"view,omitempty"`
}

// ---------------------------------------------------------------- graph

type node struct {
	ID    string
	Type  string
	Inst  string // module instance: the address without its last element
	Field string // the last element: key, csr, cert or file
	Kind  string // root, zone or service
	Lane  string // root, a, b, c or d
	Ghost bool   // not in the specification until the zone scene
	Depth int
	Row   int
	Idx   int
}

type edge struct{ F, T string } // T reads F

type graph struct {
	nodes   []*node
	byID    map[string]*node
	edges   []edge // all recorded edges
	reduced []edge // the edges that no other path implies
	succ    map[string][]string
	pred    map[string][]string
}

func lastDot(s string) (string, string) {
	i := strings.LastIndex(s, ".")
	return s[:i], s[i+1:]
}

func newNode(id, typ string, ghost bool) *node {
	inst, field := lastDot(id)
	p := strings.Split(id, ".")
	n := &node{ID: id, Type: typ, Inst: inst, Field: field, Ghost: ghost}
	switch p[0] {
	case "root":
		n.Kind, n.Lane = "root", "root"
	case "zone":
		n.Kind, n.Lane = "zone", p[1]
	case "service":
		n.Kind, n.Lane = "service", p[1]
	default:
		fail("node %s: the layout knows root, zone and service addresses", id)
	}
	return n
}

// reachable reports whether to can be reached from from along edges, not
// using the edge from -> skip.
func reachable(succ map[string][]string, from, to, skip string) bool {
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, y := range succ[x] {
			if x == from && y == skip {
				continue
			}
			if y == to {
				return true
			}
			if !seen[y] {
				seen[y] = true
				stack = append(stack, y)
			}
		}
	}
	return false
}

func buildGraph(types map[string]string, ghostTypes map[string]string, edges []edge) *graph {
	g := &graph{byID: map[string]*node{}, succ: map[string][]string{}, pred: map[string][]string{}}
	var ids []string
	for id := range types {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		n := newNode(id, types[id], false)
		g.nodes = append(g.nodes, n)
		g.byID[id] = n
	}
	var gids []string
	for id := range ghostTypes {
		gids = append(gids, id)
	}
	sort.Strings(gids)
	for _, id := range gids {
		n := newNode(id, ghostTypes[id], true)
		g.nodes = append(g.nodes, n)
		g.byID[id] = n
	}
	for _, e := range edges {
		must(g.byID[e.F] != nil && g.byID[e.T] != nil, "edge %s -> %s names an unknown node", e.T, e.F)
		g.edges = append(g.edges, e)
		g.succ[e.F] = append(g.succ[e.F], e.T)
		g.pred[e.T] = append(g.pred[e.T], e.F)
	}
	// transitive reduction: drop F -> T when another path leads from F to T
	for _, e := range g.edges {
		if !reachable(g.succ, e.F, e.T, e.T) {
			g.reduced = append(g.reduced, e)
		}
	}
	return g
}

// closure returns every node reachable from id along the recorded edges.
func (g *graph) closure(id string) []string {
	seen := map[string]bool{}
	stack := []string{id}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, y := range g.succ[x] {
			if !seen[y] {
				seen[y] = true
				stack = append(stack, y)
			}
		}
	}
	var res []string
	for k := range seen {
		res = append(res, k)
	}
	sort.Strings(res)
	return res
}

// layout assigns a depth to every node (the longest chain of reads behind it)
// and a row to every module instance.
func (g *graph) layout() (insts []string, lanes []string) {
	rsucc := map[string][]string{}
	rpred := map[string][]string{}
	for _, e := range g.reduced {
		rsucc[e.F] = append(rsucc[e.F], e.T)
		rpred[e.T] = append(rpred[e.T], e.F)
	}
	memo := map[string]int{}
	var depth func(id string) int
	depth = func(id string) int {
		if d, ok := memo[id]; ok {
			return d
		}
		d := 0
		for _, p := range rpred[id] {
			if x := depth(p) + 1; x > d {
				d = x
			}
		}
		memo[id] = d
		return d
	}
	laneSet := map[string]bool{}
	for _, n := range g.nodes {
		n.Depth = depth(n.ID)
		if n.Lane != "root" {
			laneSet[n.Lane] = true
		}
	}
	for l := range laneSet {
		lanes = append(lanes, l)
	}
	sort.Strings(lanes)
	lanes = append([]string{"root"}, lanes...)
	laneIdx := map[string]int{}
	for i, l := range lanes {
		laneIdx[l] = i
	}
	instSet := map[string]*node{}
	for _, n := range g.nodes {
		if _, ok := instSet[n.Inst]; !ok {
			instSet[n.Inst] = n
		}
	}
	for i := range instSet {
		insts = append(insts, i)
	}
	rank := func(i string) string {
		n := instSet[i]
		k := map[string]string{"root": "0", "zone": "1", "service": "2"}[n.Kind]
		return fmt.Sprintf("%02d%s%s", laneIdx[n.Lane], k, i)
	}
	sort.Slice(insts, func(a, b int) bool { return rank(insts[a]) < rank(insts[b]) })
	rowOf := map[string]int{}
	for i, s := range insts {
		rowOf[s] = i
	}
	for i, n := range g.nodes {
		n.Row = rowOf[n.Inst]
		n.Idx = i
	}
	// the DOM order is row-major
	sort.SliceStable(g.nodes, func(a, b int) bool {
		if g.nodes[a].Row != g.nodes[b].Row {
			return g.nodes[a].Row < g.nodes[b].Row
		}
		return g.nodes[a].Depth < g.nodes[b].Depth
	})
	for i, n := range g.nodes {
		n.Idx = i
	}
	return insts, lanes
}

// ---------------------------------------------------------------- geometry

type pt struct{ X, Y float64 }

type geom struct {
	Name       string
	Wide       bool
	W, H       float64
	U0, DU     float64 // depth axis
	V0, DV, LG float64 // instance axis, lane gap
	Half       float64 // half the side of a node
	Arrow      float64
	Rad        float64
	insts      []string
	lanes      []string
	laneOf     map[string]int // instance -> lane index
	rowV       []float64
}

func newGeom(wide bool, insts, lanes []string, g *graph) *geom {
	m := &geom{Wide: wide, insts: insts, lanes: lanes, laneOf: map[string]int{}, Half: 10, Arrow: 5, Rad: 7}
	for _, n := range g.nodes {
		for i, l := range lanes {
			if l == n.Lane {
				m.laneOf[n.Inst] = i
			}
		}
	}
	if wide {
		m.Name = "wide"
		m.U0, m.DU = 156, 146
		m.V0, m.DV, m.LG = 68, 48, 20
	} else {
		m.Name = "tall"
		m.U0, m.DU = 74, 62
		m.V0, m.DV, m.LG = 30, 29, 12
	}
	last := 0.0
	for i, s := range insts {
		v := m.V0 + float64(i)*m.DV + float64(m.laneOf[s])*m.LG
		m.rowV = append(m.rowV, v)
		last = v
	}
	maxDepth := 0
	for _, n := range g.nodes {
		if n.Depth > maxDepth {
			maxDepth = n.Depth
		}
	}
	endU := m.U0 + float64(maxDepth)*m.DU
	if wide {
		m.W = endU + 58
		m.H = last + 40
	} else {
		m.W = last + 30
		m.H = endU + 40
	}
	return m
}

func (m *geom) xy(u, v float64) pt {
	if m.Wide {
		return pt{u, v}
	}
	return pt{v, u}
}

func (m *geom) nodePos(n *node) pt {
	return m.xy(m.U0+float64(n.Depth)*m.DU, m.rowV[n.Row])
}

func f1(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	s = strings.TrimSuffix(s, ".0")
	if s == "-0" {
		s = "0"
	}
	return s
}

func dist(a, b pt) float64 {
	dx, dy := b.X-a.X, b.Y-a.Y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx + dy // segments are axis aligned
}

func roundedPath(pts []pt, r float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "M%s %s", f1(pts[0].X), f1(pts[0].Y))
	for i := 1; i < len(pts); i++ {
		if i == len(pts)-1 {
			fmt.Fprintf(&b, "L%s %s", f1(pts[i].X), f1(pts[i].Y))
			break
		}
		a, p, c := pts[i-1], pts[i], pts[i+1]
		d1, d2 := dist(p, a), dist(p, c)
		rr := r
		if d1/2 < rr {
			rr = d1 / 2
		}
		if d2/2 < rr {
			rr = d2 / 2
		}
		s := pt{p.X - (p.X-a.X)/d1*rr, p.Y - (p.Y-a.Y)/d1*rr}
		e := pt{p.X + (c.X-p.X)/d2*rr, p.Y + (c.Y-p.Y)/d2*rr}
		fmt.Fprintf(&b, "L%s %sQ%s %s %s %s", f1(s.X), f1(s.Y), f1(p.X), f1(p.Y), f1(e.X), f1(e.Y))
	}
	return b.String()
}

type edgeGeo struct {
	D     string
	Arrow string // points of the arrowhead
	Dot   *pt    // junction where the edge joins its row
}

func (m *geom) edge(g *graph, e edge) edgeGeo {
	f, t := g.byID[e.F], g.byID[e.T]
	uf, vf := m.U0+float64(f.Depth)*m.DU, m.rowV[f.Row]
	ut, vt := m.U0+float64(t.Depth)*m.DU, m.rowV[t.Row]
	start := m.xy(uf+m.Half, vf)
	end := m.xy(ut-m.Half-m.Arrow, vt)
	var pts []pt
	var dot *pt
	if vf == vt {
		pts = []pt{start, end}
	} else {
		um := ut - m.DU/2
		pts = []pt{start, m.xy(um, vf), m.xy(um, vt), end}
		d := m.xy(um, vt)
		dot = &d
	}
	tip := m.xy(ut-m.Half, vt)
	var a1, a2 pt
	if m.Wide {
		a1, a2 = pt{end.X, end.Y - 3.2}, pt{end.X, end.Y + 3.2}
	} else {
		a1, a2 = pt{end.X - 3.2, end.Y}, pt{end.X + 3.2, end.Y}
	}
	arrow := fmt.Sprintf("%s,%s %s,%s %s,%s", f1(a1.X), f1(a1.Y), f1(tip.X), f1(tip.Y), f1(a2.X), f1(a2.Y))
	return edgeGeo{D: roundedPath(pts, m.Rad), Arrow: arrow, Dot: dot}
}

// ---------------------------------------------------------------- scenes

type planLine struct {
	Raw  string
	Addr string
	Typ  string
	Act  string // add, change, replace, destroy
}

var (
	headRe  = regexp.MustCompile(`^  # (\S+) \((\S+)\) (will be created|will be updated in-place|must be replaced|will be destroyed)$`)
	planRe  = regexp.MustCompile(`^Plan: (\d+) to add, (\d+) to change, (\d+) to destroy\.$`)
	graphRe = regexp.MustCompile(`^Graph: planned (\d+) of (\d+), (\d+) unchanged and skipped`)
	lockRe  = regexp.MustCompile(`^(\S+)\s+locked by (\S+)$`)
	edgeRe  = regexp.MustCompile(`^(\S+) -> (\S+)$`)
	typeRe  = regexp.MustCompile(`^\s*"([^"]+)":\s*"([^"]+)"\s*$`)
	doneRe  = regexp.MustCompile(`^Apply complete! Resources: (\d+) added, (\d+) changed, (\d+) destroyed\.`)
	reachRe = regexp.MustCompile(`^([a-z]+(?:\.[a-z0-9]+)+):\s*(.*)$`)
)

var actOf = map[string]string{
	"will be created":          "add",
	"will be updated in-place": "change",
	"must be replaced":         "replace",
	"will be destroyed":        "destroy",
}

type diffLine struct {
	K string // ctx, del, ins
	T string
	N int
}

type changeJS struct {
	File  string `json:"file"`
	Lines string `json:"lines"` // html of the diff lines
	Plain string `json:"plain"`
}

type sceneJS struct {
	ID      string            `json:"id"`
	Title   string            `json:"title"`
	Sub     string            `json:"sub"`
	Say     string            `json:"say"`
	Planned int               `json:"planned"`
	Total   int               `json:"total"`
	Skipped int               `json:"skipped"`
	Graph   string            `json:"graph"`
	Plan    string            `json:"plan"`
	Act     map[string]string `json:"act"`
	Ord     map[string]int    `json:"ord"`
	Lock    map[string]string `json:"lock,omitempty"`
	Lines   map[string]string `json:"lines"` // node -> the line that names it
	Lanes   []laneJS          `json:"lanes,omitempty"`
	Term    string            `json:"term"`
	Change  changeJS          `json:"change"`
	Origin  string            `json:"origin"`
	Ghost   bool              `json:"ghost"`
	Cmd     string            `json:"cmd"`
	Unit    string            `json:"unit"`
}

type laneJS struct {
	Lane  string `json:"lane"`
	Actor string `json:"actor"`
	N     int    `json:"n"`
	Text  string `json:"text"`
}

// ---------------------------------------------------------------- source

type snippet struct {
	Key   string `json:"key"`
	File  string `json:"file"`
	From  int    `json:"from"`
	To    int    `json:"to"`
	HTML  string `json:"html"`
	Field string `json:"field"`
}

var (
	tokStr = regexp.MustCompile(`^"(?:[^"\\]|\\.)*"`)
	tokNum = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?`)
	tokId  = regexp.MustCompile(`^#?[A-Za-z_$][A-Za-z0-9_$]*`)
)

var cueKeywords = map[string]bool{"for": true, "in": true, "if": true, "let": true, "import": true, "package": true}
var cueTypes = map[string]bool{"string": true, "int": true, "bool": true, "number": true, "float": true, "bytes": true}

func span(cls, s string) string {
	return `<span class="tok-` + cls + `">` + html.EscapeString(s) + `</span>`
}

// highlight marks one line of CUE with the token classes of the site.
func highlight(line string) string {
	var b strings.Builder
	rest := line
	for len(rest) > 0 {
		switch {
		case strings.HasPrefix(rest, "//"):
			b.WriteString(span("com", rest))
			rest = ""
		case rest[0] == '"':
			m := tokStr.FindString(rest)
			if m == "" {
				m = rest
			}
			b.WriteString(span("str", m))
			rest = rest[len(m):]
		case rest[0] >= '0' && rest[0] <= '9':
			m := tokNum.FindString(rest)
			b.WriteString(span("num", m))
			rest = rest[len(m):]
		case tokId.MatchString(rest):
			m := tokId.FindString(rest)
			after := strings.TrimLeft(rest[len(m):], " ")
			switch {
			case strings.HasPrefix(m, "#"):
				b.WriteString(span("def", m))
			case m == "true" || m == "false" || m == "null":
				b.WriteString(span("num", m))
			case cueKeywords[m]:
				b.WriteString(span("kw", m))
			case cueTypes[m]:
				b.WriteString(span("type", m))
			case strings.HasPrefix(after, ":") && !strings.HasPrefix(after, "::"):
				b.WriteString(span("id", m))
			default:
				b.WriteString(html.EscapeString(m))
			}
			rest = rest[len(m):]
		case strings.ContainsRune("&|*=<>!?", rune(rest[0])):
			b.WriteString(span("op", rest[:1]))
			rest = rest[1:]
		default:
			b.WriteString(html.EscapeString(rest[:1]))
			rest = rest[1:]
		}
	}
	return b.String()
}

func codeLines(lines []string, cls func(i int) string) string {
	var b strings.Builder
	for i, l := range lines {
		c := "line"
		if cls != nil {
			if x := cls(i); x != "" {
				c += " " + x
			}
		}
		fmt.Fprintf(&b, `<span class="%s">%s`+"\n</span>", c, highlight(strings.ReplaceAll(l, "\t", "    ")))
	}
	return b.String()
}

// moduleSnippet returns the lines of the field `field` of the module file.
func moduleSnippet(file string, src []string, field string) snippet {
	prefix := "\t" + field + ":"
	for i, l := range src {
		if strings.HasPrefix(l, prefix) {
			j := i
			if !strings.HasSuffix(strings.TrimSpace(l), "{") {
				j = i
			} else {
				for j < len(src) && src[j] != "\t}" {
					j++
				}
			}
			ded := make([]string, 0, j-i+1)
			for _, x := range src[i : j+1] {
				ded = append(ded, strings.TrimPrefix(x, "\t"))
			}
			return snippet{File: file, From: i + 1, To: j + 1, HTML: codeLines(ded, nil), Field: field}
		}
	}
	fail("%s has no field %q at the first level", file, field)
	return snippet{}
}

// ---------------------------------------------------------------- diff

// lineDiff returns the changed lines of b against a with one line of context.
func lineDiff(a, b []string) []diffLine {
	n, m := len(a), len(b)
	l := make([][]int, n+1)
	for i := range l {
		l[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				l[i][j] = l[i+1][j+1] + 1
			} else if l[i+1][j] >= l[i][j+1] {
				l[i][j] = l[i+1][j]
			} else {
				l[i][j] = l[i][j+1]
			}
		}
	}
	type op struct {
		k string
		t string
		n int
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, op{"ctx", b[j], j + 1})
			i++
			j++
		case j < m && (i == n || l[i][j+1] >= l[i+1][j]):
			ops = append(ops, op{"ins", b[j], j + 1})
			j++
		default:
			ops = append(ops, op{"del", a[i], i + 1})
			i++
		}
	}
	keep := make([]bool, len(ops))
	for k, o := range ops {
		if o.k != "ctx" {
			for d := -1; d <= 1; d++ {
				if k+d >= 0 && k+d < len(ops) {
					keep[k+d] = true
				}
			}
		}
	}
	var res []diffLine
	for k, o := range ops {
		if keep[k] {
			res = append(res, diffLine{K: o.k, T: o.t, N: o.n})
		}
	}
	return res
}

// ---------------------------------------------------------------- main

type cmdLine struct{ T string }

func main() {
	raw, err := os.ReadFile(dataPath)
	if err != nil {
		fail("%v (run site/demos/blast/build.sh from the module root)", err)
	}
	var d doc
	if err := json.Unmarshal(raw, &d); err != nil {
		fail("%s: %v", dataPath, err)
	}
	byLine := map[int]jblock{}
	for _, r := range d.Blocks {
		var b jblock
		if err := json.Unmarshal(r, &b); err != nil {
			fail("block: %v", err)
		}
		byLine[b.Line] = b
	}
	fences := parseFences(pagePath)
	runs := map[string]jblock{}
	files := map[string][]string{} // file -> lines, scene=base (the first one)
	snaps := map[string][]string{} // scene/file -> lines
	for _, f := range fences {
		sc := f.Attrs["scene"]
		if file := f.Attrs["file"]; file != "" && sc != "" {
			snaps[sc+"/"+file] = f.Body
			if sc == "base" && files[file] == nil {
				files[file] = f.Body
			}
		}
		if f.Run && sc != "" {
			b, ok := byLine[f.Line]
			must(ok, "%s:%d: no recorded run for this block; run site/demos/blast/build.sh", pagePath, f.Line)
			runs[sc] = b
		}
	}
	for _, sc := range []string{"base", "types", "edges", "reach", "replace", "setting", "lanes", "locks", "zone", "zone-edges"} {
		must(runs[sc].Command != "", "page.md has no run block with scene=%s", sc)
	}
	for _, b := range runs {
		must(b.Exit == 0, "the run %q exited %d", b.Command, b.Exit)
	}

	// ---- nodes and edges
	types := map[string]string{}
	for _, l := range strings.Split(runs["types"].Output, "\n") {
		if m := typeRe.FindStringSubmatch(l); m != nil {
			types[m[1]] = m[2]
		}
	}
	var edges []edge
	for _, l := range strings.Split(runs["edges"].Output, "\n") {
		if m := edgeRe.FindStringSubmatch(l); m != nil {
			edges = append(edges, edge{F: m[2], T: m[1]})
		}
	}
	// the new zone: its nodes from the plan, its edges from the state after the apply
	zoneLines := parsePlan(runs["zone"].Output)
	ghostTypes := map[string]string{}
	for _, pl := range zoneLines.Lines {
		must(pl.Act == "add", "the zone scene creates nodes only; %s is %s", pl.Addr, pl.Act)
		ghostTypes[pl.Addr] = pl.Typ
	}
	for _, l := range strings.Split(runs["zone-edges"].Output, "\n") {
		if m := edgeRe.FindStringSubmatch(l); m != nil {
			edges = append(edges, edge{F: m[2], T: m[1]})
		}
	}
	g := buildGraph(types, ghostTypes, edges)
	insts, lanes := g.layout()
	total := len(types)
	must(total == len(g.nodes)-len(ghostTypes), "node count")

	// recorded dependents of every node (state dependents), base and new zone
	reach := map[string][]string{}
	reachZ := map[string][]string{}
	for i, src := range []string{runs["reach"].Output, runs["zone-edges"].Output} {
		for _, l := range strings.Split(src, "\n") {
			if m := reachRe.FindStringSubmatch(l); m != nil {
				if i == 0 {
					reach[m[1]] = strings.Fields(m[2])
				} else {
					reachZ[m[1]] = strings.Fields(m[2])
				}
			}
		}
	}
	// the walk over the recorded edges must give the recorded dependents
	for id := range types {
		var got []string
		for _, x := range g.closure(id) {
			if !g.byID[x].Ghost {
				got = append(got, x)
			}
		}
		want := append([]string(nil), reach[id]...)
		sort.Strings(want)
		must(strings.Join(got, " ") == strings.Join(want, " "), "state dependents %s: recorded %v, walk %v", id, want, got)
	}
	for id := range ghostTypes {
		reach[id] = reachZ[id]
		got := g.closure(id)
		must(strings.Join(got, " ") == strings.Join(sorted(reachZ[id]), " "), "state dependents %s: recorded %v, walk %v", id, reachZ[id], got)
	}
	// the nodes that the fourth zone adds to the reach of an existing node
	for id := range reachZ {
		if g.byID[id] != nil && !g.byID[id].Ghost {
			got := g.closure(id)
			must(strings.Join(got, " ") == strings.Join(sorted(reachZ[id]), " "), "state dependents %s after the zone: recorded %v, walk %v", id, reachZ[id], got)
		}
	}

	// ---- scenes
	order := []string{"setting", "replace", "zone", "lanes"}
	titles := map[string]string{"setting": "Change a setting", "replace": "Replace a root key", "zone": "Add a zone", "lanes": "Two applies at once"}
	scenes := map[string]*sceneJS{}
	for _, id := range []string{"setting", "replace", "zone"} {
		scenes[id] = planScene(id, titles[id], runs[id], g, total, snaps, files)
	}
	scenes["lanes"] = laneScene(titles["lanes"], runs["lanes"], runs["locks"], g, total)

	// checks that the texts of the scenes rely on
	checkScenes(g, scenes, reach, total)

	// ---- sources
	var snippets []snippet
	snipIdx := map[string]int{}
	snipOf := func(n *node) string {
		key := n.Kind + "." + n.Field
		if i, ok := snipIdx[key]; ok {
			return snippets[i].Key
		}
		file := "pki/" + n.Kind + ".cue"
		src := files[file]
		must(src != nil, "page.md has no base %s", file)
		sn := moduleSnippet(file, src, n.Field)
		sn.Key = key
		snipIdx[key] = len(snippets)
		snippets = append(snippets, sn)
		return key
	}

	// ---- geometry
	wide := newGeom(true, insts, lanes, g)
	tall := newGeom(false, insts, lanes, g)

	view := buildView(d, g, scenes, order, snipOf, func() []snippet { return snippets }, wide, tall, insts, lanes, reach, reachZ, total, len(ghostTypes), runs)
	d.View = view.Data
	out, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		fail("%v", err)
	}
	if err := os.WriteFile(dataPath, append(out, '\n'), 0o644); err != nil {
		fail("%v", err)
	}
	if err := writePartial(view, g, scenes, order, wide, tall); err != nil {
		fail("%v", err)
	}
	fmt.Printf("derive: %d nodes (%d new), %d edges (%d drawn), %d scenes\n", len(g.nodes), len(ghostTypes), len(g.edges), len(g.reduced), len(scenes))
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

type planParsed struct {
	Lines   []planLine
	Plan    string
	Graph   string
	Add     int
	Chg     int
	Dst     int
	Planned int
	Total   int
	Skipped int
}

func parsePlan(out string) planParsed {
	var p planParsed
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if m := headRe.FindStringSubmatch(l); m != nil {
			p.Lines = append(p.Lines, planLine{Raw: l, Addr: m[1], Typ: m[2], Act: actOf[m[3]]})
		}
		if m := planRe.FindStringSubmatch(l); m != nil {
			p.Plan = l
			p.Add, _ = strconv.Atoi(m[1])
			p.Chg, _ = strconv.Atoi(m[2])
			p.Dst, _ = strconv.Atoi(m[3])
		}
		if m := graphRe.FindStringSubmatch(l); m != nil {
			p.Graph = l
			p.Planned, _ = strconv.Atoi(m[1])
			p.Total, _ = strconv.Atoi(m[2])
			p.Skipped, _ = strconv.Atoi(m[3])
		}
	}
	return p
}

// ord gives every reached node the length of the longest chain of reached
// nodes behind it, which orders the ripple.
func ord(g *graph, reached map[string]bool) map[string]int {
	memo := map[string]int{}
	var f func(id string) int
	f = func(id string) int {
		if v, ok := memo[id]; ok {
			return v
		}
		d := 0
		for _, p := range g.pred[id] {
			if reached[p] {
				if x := f(p) + 1; x > d {
					d = x
				}
			}
		}
		memo[id] = d
		return d
	}
	res := map[string]int{}
	for id := range reached {
		res[id] = f(id)
	}
	// compact to consecutive values
	vals := map[int]bool{}
	for _, v := range res {
		vals[v] = true
	}
	var keys []int
	for v := range vals {
		keys = append(keys, v)
	}
	sort.Ints(keys)
	rank := map[int]int{}
	for i, v := range keys {
		rank[v] = i
	}
	for id, v := range res {
		res[id] = rank[v]
	}
	return res
}

func firstCommand(cmd string) string {
	return strings.Split(cmd, "\n")[0]
}

func planScene(id, title string, run jblock, g *graph, total int, snaps map[string][]string, files map[string][]string) *sceneJS {
	p := parsePlan(run.Output)
	must(p.Graph != "" && p.Plan != "", "scene %s: the output has no Plan and Graph lines", id)
	must(p.Planned == len(p.Lines), "scene %s: %d plan lines but the Graph line says planned %d", id, len(p.Lines), p.Planned)
	must(p.Planned+p.Skipped == p.Total, "scene %s: planned and skipped do not add up to the total", id)
	s := &sceneJS{ID: id, Title: title, Planned: p.Planned, Total: p.Total, Skipped: p.Skipped, Graph: p.Graph, Plan: p.Plan,
		Act: map[string]string{}, Lines: map[string]string{}, Cmd: firstCommand(run.Command)}
	reached := map[string]bool{}
	for _, l := range p.Lines {
		s.Act[l.Addr] = l.Act
		s.Lines[l.Addr] = strings.TrimSpace(l.Raw)
		reached[l.Addr] = true
		must(g.byID[l.Addr] != nil, "scene %s: unknown node %s", id, l.Addr)
	}
	s.Origin = p.Lines[0].Addr
	s.Ord = ord(g, reached)
	s.Ghost = id == "zone"
	// the change in the specification
	var file string
	switch id {
	case "replace":
		file = "pki/root.cue"
	default:
		file = "main.cue"
	}
	before := files[file]
	after := snaps[id+"/"+file]
	must(before != nil && after != nil, "scene %s: page.md needs the base and the %s version of %s", id, id, file)
	diff := lineDiff(before, after)
	must(len(diff) > 0, "scene %s: the file does not change", id)
	var b strings.Builder
	var plain []string
	for _, dl := range diff {
		cls := dl.K
		sign := "  "
		switch dl.K {
		case "ins":
			sign = "+ "
		case "del":
			sign = "- "
		}
		fmt.Fprintf(&b, `<span class="line %s"><span class="bl-ln">%d</span><span class="bl-sg">%s</span>%s`+"\n</span>", cls, dl.N, strings.TrimSpace(sign), highlight(strings.ReplaceAll(strings.TrimLeft(dl.T, "\t"), "\t", "    ")))
		plain = append(plain, sign+strings.TrimSpace(dl.T))
	}
	s.Change = changeJS{File: file, Lines: b.String(), Plain: strings.Join(plain, "\n")}
	return s
}

type lockInfo struct {
	Addr, Actor string
}

func laneScene(title string, plans, locks jblock, g *graph, total int) *sceneJS {
	s := &sceneJS{ID: "lanes", Title: title, Act: map[string]string{}, Lines: map[string]string{}, Lock: map[string]string{}, Cmd: firstCommand(plans.Command)}
	// the two plans
	var pl []planParsed
	var cur planParsed
	for _, l := range strings.Split(plans.Output, "\n") {
		if m := planRe.FindStringSubmatch(l); m != nil {
			cur = planParsed{Plan: l}
			cur.Add, _ = strconv.Atoi(m[1])
			cur.Chg, _ = strconv.Atoi(m[2])
			cur.Dst, _ = strconv.Atoi(m[3])
		}
		if m := graphRe.FindStringSubmatch(l); m != nil {
			cur.Graph = l
			cur.Planned, _ = strconv.Atoi(m[1])
			cur.Total, _ = strconv.Atoi(m[2])
			cur.Skipped, _ = strconv.Atoi(m[3])
			pl = append(pl, cur)
		}
	}
	must(len(pl) == 2, "the lanes scene needs two plans, found %d", len(pl))
	var lk []lockInfo
	var doneLines []string
	for _, l := range strings.Split(locks.Output, "\n") {
		if m := lockRe.FindStringSubmatch(l); m != nil {
			lk = append(lk, lockInfo{m[1], m[2]})
		}
		if doneRe.MatchString(l) {
			doneLines = append(doneLines, l)
		}
	}
	must(len(doneLines) == 2, "both applies must complete, found %d completion lines", len(doneLines))
	actors := []string{}
	byActor := map[string][]string{}
	for _, x := range lk {
		if _, ok := byActor[x.Actor]; !ok {
			actors = append(actors, x.Actor)
		}
		byActor[x.Actor] = append(byActor[x.Actor], x.Addr)
		s.Lock[x.Addr] = x.Actor
		s.Act[x.Addr] = "replace"
		s.Lines[x.Addr] = x.Addr + "  locked by " + x.Actor
		must(g.byID[x.Addr] != nil, "lock on unknown node %s", x.Addr)
	}
	must(len(actors) == 2, "the lanes scene needs two lock holders, found %v", actors)
	for i, a := range actors {
		must(len(byActor[a]) == pl[i].Add && pl[i].Add == pl[i].Dst && pl[i].Chg == 0 && pl[i].Planned == pl[i].Add,
			"actor %s holds %d locks but plans %d to add, %d to destroy", a, len(byActor[a]), pl[i].Add, pl[i].Dst)
	}
	s.Planned = len(lk)
	s.Total = pl[0].Total
	s.Skipped = total - len(lk)
	s.Graph = pl[0].Graph
	s.Plan = pl[0].Plan
	reached := map[string]bool{}
	for a := range s.Act {
		reached[a] = true
	}
	s.Ord = ord(g, reached)
	s.Origin = byActor[actors[0]][0]
	for _, a := range actors {
		for _, n := range byActor[a] {
			if o := s.Ord[n]; o < s.Ord[s.Origin] || o == s.Ord[s.Origin] && g.byID[n].Idx < g.byID[s.Origin].Idx {
				s.Origin = n
			}
		}
	}
	for _, a := range actors {
		nodes := byActor[a]
		sort.Strings(nodes)
		lane := g.byID[nodes[0]].Lane
		for _, n := range nodes {
			must(g.byID[n].Lane == lane, "the locks of %s span two lanes", a)
		}
		s.Lanes = append(s.Lanes, laneJS{Lane: lane, Actor: a, N: len(nodes), Text: fmt.Sprintf("%s · %d locks", a, len(nodes))})
	}
	must(s.Lanes[0].Lane != s.Lanes[1].Lane, "the two actors work in the same lane")
	for _, ln := range s.Lanes {
		want := fmt.Sprintf("--replace zone.%s.key ", ln.Lane)
		found := false
		for _, c := range strings.Split(plans.Command, "\n") {
			if strings.Contains(c, want) && strings.Contains(c, "--actor "+ln.Actor+" ") {
				found = true
			}
		}
		must(found, "no plan command replaces zone.%s.key as %s", ln.Lane, ln.Actor)
	}
	// the change: the two plan commands up to their pipe
	var cmds []string
	for _, c := range strings.Split(plans.Command, "\n") {
		if i := strings.Index(c, " | "); i > 0 {
			c = c[:i]
		}
		cmds = append(cmds, c)
	}
	var b strings.Builder
	for _, c := range cmds {
		fmt.Fprintf(&b, `<span class="line">%s`+"\n</span>", highlight(c))
	}
	s.Change = changeJS{File: "two plans", Lines: b.String(), Plain: strings.Join(cmds, "\n")}
	return s
}

// checkScenes verifies the statements that the scene texts make.
func checkScenes(g *graph, sc map[string]*sceneJS, reach map[string][]string, total int) {
	// setting: one node is planned; the nodes that read it are skipped
	s := sc["setting"]
	must(s.Planned == 1 && len(s.Act) == 1, "setting: one node should be planned, got %d", s.Planned)
	for id, a := range s.Act {
		must(a == "change", "setting: %s is %s, want an in-place change", id, a)
		deps := reach[id]
		must(len(deps) > 0, "setting: %s has no dependents", id)
		for _, dpd := range deps {
			must(s.Act[dpd] == "", "setting: dependent %s should be skipped", dpd)
		}
	}
	// replace: the origin and everything that reads it, certificates and files
	r := sc["replace"]
	origin := r.Origin
	must(g.byID[origin].Type == "tls_private_key", "replace: the origin %s is not a key", origin)
	want := map[string]bool{origin: true}
	for _, x := range reach[origin] {
		want[x] = true
	}
	must(len(want) == r.Planned, "replace: %d nodes reached, the origin and its dependents are %d", r.Planned, len(want))
	for id, a := range r.Act {
		must(want[id], "replace: %s is reached but does not read %s", id, origin)
		must(a == "replace", "replace: %s is %s", id, a)
		t := g.byID[id].Type
		if id != origin {
			must(t == "tls_self_signed_cert" || t == "tls_locally_signed_cert" || t == "local_file", "replace: %s is a %s, not a certificate or a file", id, t)
		}
	}
	for _, n := range g.nodes {
		if n.Ghost || r.Act[n.ID] != "" {
			continue
		}
		must(n.Type == "tls_private_key" || n.Type == "tls_cert_request", "replace: skipped node %s is a %s", n.ID, n.Type)
	}
	// zone: new nodes only; the existing nodes are skipped
	z := sc["zone"]
	must(z.Total == total+len(z.Act), "zone: total %d, want %d existing and %d new", z.Total, total, len(z.Act))
	for id, a := range z.Act {
		must(a == "add" && g.byID[id].Ghost, "zone: %s is %s", id, a)
	}
	// lanes: disjoint node sets
	l := sc["lanes"]
	must(len(l.Lock) == 16, "lanes: expected 16 locks, got %d", len(l.Lock))
	sets := map[string]map[string]bool{}
	for id, a := range l.Lock {
		if sets[a] == nil {
			sets[a] = map[string]bool{}
		}
		sets[a][id] = true
	}
	var holders []string
	for a := range sets {
		holders = append(holders, a)
	}
	for _, x := range holders {
		for id := range sets[x] {
			for _, y := range holders {
				must(x == y || !sets[y][id], "lanes: %s is locked by %s and %s", id, x, y)
			}
		}
	}
}
