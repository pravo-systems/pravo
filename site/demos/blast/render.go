//go:build ignore

// render.go is the second half of derive.go: it turns the scenes and the
// layout into the view object of site/data/blast.json and into the markup of
// site/demos/blast/embed.html. Run both files together:
//
//	go run site/demos/blast/derive.go site/demos/blast/render.go
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"regexp"
	"sort"
	"strings"
)

type viewOut struct {
	Data map[string]any
	JS   *jsData
}

type jsNode struct {
	ID string `json:"id"`
	T  string `json:"t"`
	S  string `json:"s"`
	N  []int  `json:"n"`            // the nodes it reads, as recorded (pravo state graph)
	R  []int  `json:"r"`            // everything a change can reach (pravo state dependents)
	RZ []int  `json:"rz,omitempty"` // the same once the fourth zone exists, where it differs
	G  bool   `json:"g,omitempty"`  // not in the specification until the zone scene
}

type jsSnip struct {
	File string `json:"file"`
	From int    `json:"from"`
	To   int    `json:"to"`
	HTML string `json:"html"`
}

type jsTall struct {
	W      float64      `json:"w"`
	H      float64      `json:"h"`
	Nodes  [][2]float64 `json:"nodes"`
	Edges  []string     `json:"edges"`
	Arrows []string     `json:"arrows"`
	Dots   [][2]float64 `json:"dots"`
	Bands  [][4]float64 `json:"bands"`
	Caps   [][3]float64 `json:"caps"` // x, y, anchor (0 start, 1 middle, 2 end)
}

type jsData struct {
	Nodes  []jsNode          `json:"nodes"`
	Snips  map[string]jsSnip `json:"snips"`
	Scenes []*sceneJS        `json:"scenes"`
	Tall   jsTall            `json:"tall"`
	Slots  int               `json:"slots"`
}

// ---------------------------------------------------------------- texts

func capFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func setTexts(sc map[string]*sceneJS, g *graph, total int) {
	s := sc["setting"]
	s.Sub = fmt.Sprintf("%d of %d planned", s.Planned, s.Total)
	s.Say = fmt.Sprintf("One certificate gets a longer renewal window, an attribute that the provider updates in place. %d of %d nodes are planned. The file that holds the certificate and the %d other nodes are skipped.", s.Planned, s.Total, s.Skipped-1)

	r := sc["replace"]
	r.Sub = fmt.Sprintf("%d of %d planned", r.Planned, r.Total)
	r.Say = fmt.Sprintf("The root key changes its curve, which cannot change in place. The key is replaced, and the replacement follows the edges to every certificate below it and to the files that hold them: %d of %d nodes. The %d keys and certificate requests that do not read the root are skipped.", r.Planned, r.Total, r.Skipped)

	z := sc["zone"]
	z.Sub = fmt.Sprintf("%d of %d planned", z.Planned, z.Total)
	lane := ""
	for id := range z.Act {
		lane = g.byID[id].Lane
	}
	zones := map[string]bool{}
	for _, n := range g.nodes {
		if n.Kind == "zone" {
			zones[n.Lane] = true
		}
	}
	ordinal := map[int]string{2: "second", 3: "third", 4: "fourth", 5: "fifth", 6: "sixth"}[len(zones)]
	must(ordinal != "", "no ordinal for %d zones", len(zones))
	z.Say = fmt.Sprintf("A %s zone, %s, joins the specification. Its %d resources are created, and the %d existing nodes are skipped: planned %d of %d.", ordinal, lane, z.Planned, z.Skipped, z.Planned, z.Total)

	l := sc["lanes"]
	l.Sub = fmt.Sprintf("%d locks held at once", len(l.Lock))
	a, b := l.Lanes[0], l.Lanes[1]
	l.Say = fmt.Sprintf("%s replaces the key of zone %s and %s replaces the key of zone %s. The two plans share no node and no output, so both applies hold their locks at once: %d locks, two holders. Both applies complete.", capFirst(a.Actor), a.Lane, capFirst(b.Actor), b.Lane, len(l.Lock))
	l.Unit = "nodes planned by two plans"
	for _, s := range []*sceneJS{s, r, z} {
		s.Unit = "nodes planned"
	}
}

// ---------------------------------------------------------------- terminal

var cmdRe = regexp.MustCompile(`^\s*$`)

func classify(line string, s *sceneJS, ordOf map[string]int) string {
	esc := html.EscapeString
	last := 0
	for _, o := range ordOf {
		if o > last {
			last = o
		}
	}
	tail := fmt.Sprintf(` style="--o:%d"`, last+1)
	nodeAttr := func(id string) string {
		o, ok := ordOf[id]
		if !ok {
			return fmt.Sprintf(` data-n="%s"`, esc(id))
		}
		return fmt.Sprintf(` data-n="%s" style="--o:%d"`, esc(id), o)
	}
	if m := headRe.FindStringSubmatch(line); m != nil {
		cls := map[string]string{"add": "out-add", "change": "out-change", "replace": "out-replace", "destroy": "out-destroy"}[actOf[m[3]]]
		return fmt.Sprintf(`<span class="bl-pl %s"%s>%s`+"\n</span>", cls, nodeAttr(m[1]), esc(line))
	}
	if m := planRe.FindStringSubmatch(line); m != nil {
		return fmt.Sprintf(`<span class="bl-pl bl-sum"%s>Plan: <span class="out-add">%s to add</span>, <span class="out-change">%s to change</span>, <span class="out-destroy">%s to destroy</span>.`+"\n</span>", tail, m[1], m[2], m[3])
	}
	if m := lockRe.FindStringSubmatch(line); m != nil {
		return fmt.Sprintf(`<span class="bl-pl out-dim"%s>%s  locked by <span class="bl-actor">%s</span>`+"\n</span>", nodeAttr(m[1]), esc(m[1]), esc(m[2]))
	}
	if m := doneRe.FindStringSubmatch(line); m != nil {
		return fmt.Sprintf(`<span class="bl-pl bl-sum"%s><span class="out-add">Apply complete!</span> Resources: <span class="out-add">%s added</span>, <span class="out-change">%s changed</span>, <span class="out-destroy">%s destroyed</span>.`+"\n</span>", tail, m[1], m[2], m[3])
	}
	return fmt.Sprintf(`<span class="bl-pl out-dim"%s>%s`+"\n</span>", tail, esc(line))
}

func termHTML(s *sceneJS, blocks []jblock) string {
	var b strings.Builder
	for _, blk := range blocks {
		for _, c := range strings.Split(blk.Command, "\n") {
			fmt.Fprintf(&b, `<div class="term-line cmd"><span class="prompt">$</span><span class="cmd-text">%s</span></div>`+"\n", html.EscapeString(c))
		}
		b.WriteString(`<pre class="term-out"><code>`)
		for _, l := range strings.Split(strings.TrimRight(blk.Output, "\n"), "\n") {
			b.WriteString(classify(l, s, s.Ord))
		}
		b.WriteString("</code></pre>\n")
	}
	return b.String()
}

// ---------------------------------------------------------------- view

func buildView(d doc, g *graph, sc map[string]*sceneJS, order []string, snipOf func(*node) string, snippets func() []snippet,
	wide, tall *geom, insts, lanes []string, reach, reachZ map[string][]string, total, ghosts int, runs map[string]jblock) viewOut {

	setTexts(sc, g, total)
	blocks := map[string][]jblock{
		"setting": {runs["setting"]},
		"replace": {runs["replace"]},
		"zone":    {runs["zone"]},
		"lanes":   {runs["lanes"], runs["locks"]},
	}
	for id, s := range sc {
		s.Term = termHTML(s, blocks[id])
		if id == "lanes" {
			s.Graph = strings.Join(graphLines(runs["lanes"].Output), "\n")
			s.Plan = ""
		}
	}
	// the nodes, in DOM order
	idx := map[string]int{}
	for i, n := range g.nodes {
		idx[n.ID] = i
	}
	rpred := map[string][]string{}
	for _, e := range g.edges {
		rpred[e.T] = append(rpred[e.T], e.F)
	}
	js := &jsData{Snips: map[string]jsSnip{}, Slots: len(g.nodes)}
	for _, n := range g.nodes {
		key := snipOf(n)
		needs, rch := []int{}, []int{}
		for _, p := range rpred[n.ID] {
			needs = append(needs, idx[p])
		}
		sort.Ints(needs)
		for _, r := range reach[n.ID] {
			rch = append(rch, idx[r])
		}
		sort.Ints(rch)
		jn := jsNode{ID: n.ID, T: n.Type, S: key, N: needs, R: rch, G: n.Ghost}
		if z, ok := reachZ[n.ID]; ok && !n.Ghost {
			for _, x := range z {
				jn.RZ = append(jn.RZ, idx[x])
			}
			sort.Ints(jn.RZ)
		}
		js.Nodes = append(js.Nodes, jn)
	}
	for _, sn := range snippets() {
		js.Snips[sn.Key] = jsSnip{File: sn.File, From: sn.From, To: sn.To, HTML: sn.HTML}
	}
	for _, id := range order {
		js.Scenes = append(js.Scenes, sc[id])
	}
	// the tall layout
	tl := jsTall{W: tall.W, H: tall.H}
	for _, n := range g.nodes {
		p := tall.nodePos(n)
		tl.Nodes = append(tl.Nodes, [2]float64{p.X, p.Y})
	}
	for _, e := range g.reduced {
		eg := tall.edge(g, e)
		tl.Edges = append(tl.Edges, eg.D)
		tl.Arrows = append(tl.Arrows, eg.Arrow)
		if eg.Dot != nil {
			tl.Dots = append(tl.Dots, [2]float64{eg.Dot.X, eg.Dot.Y})
		} else {
			tl.Dots = append(tl.Dots, [2]float64{-9, -9})
		}
	}
	for _, band := range bands(tall, g, lanes) {
		tl.Bands = append(tl.Bands, [4]float64{band.X, band.Y, band.W, band.H})
		tl.Caps = append(tl.Caps, band.Cap)
	}
	js.Tall = tl

	ran := ""
	if len(d.Version) > 0 {
		ran = strings.TrimPrefix(d.Version[0], "pravo ")
	}
	cue := ""
	if len(d.Version) > 1 {
		cue = strings.TrimPrefix(d.Version[1], "cue ")
	}
	data := map[string]any{
		"ran":    ran,
		"cue":    cue,
		"total":  total,
		"ghosts": ghosts,
		"slots":  len(g.nodes),
		"rows":   len(insts),
		"edges":  len(g.reduced),
		"all":    len(g.edges),
		"js":     js,
	}
	return viewOut{Data: data, JS: js}
}

func graphLines(out string) []string {
	var res []string
	for _, l := range strings.Split(out, "\n") {
		if graphRe.MatchString(l) {
			res = append(res, l)
		}
	}
	return res
}

type band struct {
	Lane       string
	X, Y, W, H float64
	Cap        [3]float64
}

// bands returns one rectangle per lane in the given geometry.
func bands(m *geom, g *graph, lanes []string) []band {
	var res []band
	for li, lane := range lanes {
		lo, hi := 1e9, -1e9
		for _, n := range g.nodes {
			if n.Lane != lane {
				continue
			}
			v := m.rowV[n.Row]
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		_ = li
		var b band
		b.Lane = lane
		if m.Wide {
			pad := 19.0
			b.X, b.W = 6, m.W-12
			b.Y, b.H = lo-pad, hi-lo+2*pad
			b.Cap = [3]float64{m.W - 18, lo - pad + 15, 2}
		} else {
			pad := 17.0
			b.X, b.W = lo-pad, hi-lo+2*pad
			b.Y, b.H = 24, m.H-24-6
			b.Cap = [3]float64{lo + (hi-lo)/2, 44, 1}
		}
		res = append(res, b)
	}
	return res
}

// ---------------------------------------------------------------- markup

const glyphs = `<defs>
<path id="bl-ga" d="M-4.5 0H4.5M0-4.5V4.5"/>
<path id="bl-gc" d="M-5.5 1.2C-3.6-2.6-1.6-2.6 0-.4S3.6 2.8 5.5-1"/>
<path id="bl-gr" d="M-5 -2.6H4M1.4 -5.2L4.2 -2.6L1.4 0M5 3H-4M-1.4 .4L-4.2 3L-1.4 5.6"/>
<path id="bl-gk" d="M-4.2 .2L-1.2 3.2L4.4 -2.8"/>
<path id="bl-gl" d="M-3.6-.4h7.2v5.6h-7.2zM-2.2-.4v-2.2a2.2 2.2 0 0 1 4.4 0v2.2"/>
</defs>`

func actClass(act string) string { return act }

func nodeHTML(n *node, p pt, act string, tabbable bool, title string) string {
	var b strings.Builder
	ti := "-1"
	if tabbable {
		ti = "0"
	}
	fmt.Fprintf(&b, `<g class="bl-n" data-n="%s" data-act="%s" transform="translate(%s %s)" role="button" tabindex="%s" aria-label="%s" style="--dp:%d">`,
		html.EscapeString(n.ID), act, f1(p.X), f1(p.Y), ti, html.EscapeString(title), n.Depth)
	fmt.Fprintf(&b, `<title>%s</title>`, html.EscapeString(title))
	b.WriteString(`<rect class="bl-hit" x="-16" y="-16" width="32" height="32"/>`)
	b.WriteString(`<g class="bl-ni"><g class="bl-nd">`)
	b.WriteString(`<rect class="bl-halo" x="-10" y="-10" width="20" height="20" rx="2"/>`)
	b.WriteString(`<rect class="bl-box" x="-10" y="-10" width="20" height="20" rx="2"/>`)
	b.WriteString(`<rect class="bl-f bl-fa" x="-10" y="-10" width="20" height="20" rx="2"/>`)
	b.WriteString(`<rect class="bl-f bl-fc" x="-10" y="-10" width="20" height="20" rx="2"/>`)
	b.WriteString(`<use class="bl-g bl-ga" href="#bl-ga"/><use class="bl-g bl-gc" href="#bl-gc"/><use class="bl-g bl-gr" href="#bl-gr"/><use class="bl-g bl-gk" href="#bl-gk"/>`)
	b.WriteString(`</g><g class="bl-lk"><circle cx="10" cy="-10" r="6.8"/><use href="#bl-gl" x="10" y="-9"/></g></g>`)
	b.WriteString(`<rect class="bl-org" x="-15" y="-15" width="30" height="30" rx="4"/><rect class="bl-ring" x="-14" y="-14" width="28" height="28" rx="3"/>`)
	b.WriteString(`</g>`)
	return b.String()
}

func actTitle(n *node, act string) string {
	state := map[string]string{"add": "created", "change": "updated in place", "replace": "replaced", "skip": "skipped", "ghost": "not yet in the specification"}[act]
	return fmt.Sprintf("%s, %s, %s", n.ID, n.Type, state)
}

func svgHTML(g *graph, m *geom, lanes []string, s0 *sceneJS, nIDs int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="bl-svg" viewBox="0 0 %s %s" role="group" aria-labelledby="bl-svg-t bl-svg-d" focusable="false">`+"\n", f1(m.W), f1(m.H))
	fmt.Fprintf(&b, `<title id="bl-svg-t">Dependency graph of the estate</title>`+"\n")
	nodes, insts, drawn, all := baseCounts(g)
	fmt.Fprintf(&b, `<desc id="bl-svg-d">%d resources in %d module instances. Each node is a resource. An edge runs from the resource that is read to the resource that reads it. The drawing leaves out an edge that a longer path already implies: %d edges are drawn of the %d that pravo state graph prints. Use the arrow keys to move between nodes.</desc>`+"\n", nodes, insts, drawn, all)
	b.WriteString(glyphs + "\n")

	// depth header
	b.WriteString(`<g class="bl-head" aria-hidden="true">`)
	maxD := 0
	for _, n := range g.nodes {
		if n.Depth > maxD {
			maxD = n.Depth
		}
	}
	fmt.Fprintf(&b, `<text class="bl-ht" x="16" y="30">depth</text>`)
	for k := 0; k <= maxD; k++ {
		p := m.xy(m.U0+float64(k)*m.DU, 30)
		fmt.Fprintf(&b, `<text class="bl-hn" x="%s" y="%s">%d</text>`, f1(p.X), f1(p.Y), k)
	}
	fmt.Fprintf(&b, `<path class="bl-hr" d="M10 42H%s"/>`, f1(m.W-10))
	b.WriteString("</g>\n")

	// depth guides
	b.WriteString(`<g class="bl-guides" aria-hidden="true">`)
	for k := 0; k <= maxD; k++ {
		p := m.xy(m.U0+float64(k)*m.DU, 0)
		fmt.Fprintf(&b, `<path class="bl-guide" d="M%s 42V%s"/>`, f1(p.X), f1(m.H-6))
	}
	b.WriteString("</g>\n")

	// lane bands
	b.WriteString(`<g class="bl-lanes">`)
	for _, band := range bands(m, g, lanes) {
		cap := laneCaption(band.Lane)
		fmt.Fprintf(&b, `<g class="bl-lane" data-lane="%s"><rect class="bl-band" x="%s" y="%s" width="%s" height="%s"/><rect class="bl-hull" x="%s" y="%s" width="%s" height="%s"/><text class="bl-cap" x="%s" y="%s" text-anchor="end" data-cap="%s">%s</text></g>`,
			band.Lane, f1(band.X), f1(band.Y), f1(band.W), f1(band.H), f1(band.X), f1(band.Y), f1(band.W), f1(band.H), f1(band.Cap[0]), f1(band.Cap[1]), html.EscapeString(cap), html.EscapeString(cap))
	}
	b.WriteString("</g>\n")

	// row labels
	b.WriteString(`<g class="bl-rows" aria-hidden="true">`)
	for i, inst := range m.insts {
		fmt.Fprintf(&b, `<text class="bl-rl" x="16" y="%s">%s</text>`, f1(m.rowV[i]+4), html.EscapeString(inst))
	}
	b.WriteString("</g>\n")

	// edges
	b.WriteString(`<g class="bl-edges" aria-hidden="true">`)
	for _, e := range g.reduced {
		eg := m.edge(g, e)
		t := g.byID[e.T]
		ghost := ""
		if t.Ghost {
			ghost = ` data-ghost`
		}
		fmt.Fprintf(&b, `<g class="bl-e" data-f="%s" data-t="%s"%s style="--dp:%d"><path class="bl-eb" pathLength="1" d="%s"/><path class="bl-ev" pathLength="1" d="%s"/><polygon class="bl-ah" points="%s"/><polygon class="bl-ahv" points="%s"/>`,
			html.EscapeString(e.F), html.EscapeString(e.T), ghost, t.Depth, eg.D, eg.D, eg.Arrow, eg.Arrow)
		if eg.Dot != nil {
			fmt.Fprintf(&b, `<circle class="bl-j" cx="%s" cy="%s" r="2.3"/>`, f1(eg.Dot.X), f1(eg.Dot.Y))
		}
		b.WriteString("</g>")
	}
	b.WriteString("</g>\n")

	// nodes
	b.WriteString(`<g class="bl-nodes">`)
	for i, n := range g.nodes {
		act := "skip"
		if n.Ghost {
			act = "ghost"
		}
		if a, ok := s0.Act[n.ID]; ok {
			act = a
		}
		b.WriteString(nodeHTML(n, m.nodePos(n), act, i == originIndex(g, s0), actTitle(n, act)))
	}
	b.WriteString("</g>\n</svg>")
	return b.String()
}

func originIndex(g *graph, s *sceneJS) int {
	return g.byID[s.Origin].Idx
}

// baseCounts counts what exists before the zone scene adds a fourth zone: the
// resources, the module instances that hold one, the edges the drawing shows
// and the edges that pravo state graph prints.
func baseCounts(g *graph) (nodes, insts, drawn, all int) {
	held := map[string]bool{}
	for _, n := range g.nodes {
		if !n.Ghost {
			nodes++
			held[n.Inst] = true
		}
	}
	real := func(e edge) bool { return !g.byID[e.F].Ghost && !g.byID[e.T].Ghost }
	for _, e := range g.reduced {
		if real(e) {
			drawn++
		}
	}
	for _, e := range g.edges {
		if real(e) {
			all++
		}
	}
	return nodes, len(held), drawn, all
}

func countGhosts(g *graph) int {
	c := 0
	for _, n := range g.nodes {
		if n.Ghost {
			c++
		}
	}
	return c
}

func laneCaption(l string) string {
	if l == "root" {
		return "root"
	}
	return "zone " + l
}

func tallyHTML(g *graph, s0 *sceneJS) string {
	var b strings.Builder
	b.WriteString(`<div class="bl-tally" aria-hidden="true">`)
	for _, n := range g.nodes {
		act := "skip"
		if n.Ghost {
			act = "ghost"
		}
		if a, ok := s0.Act[n.ID]; ok {
			act = a
		}
		fmt.Fprintf(&b, `<i class="bl-t" data-n="%s" data-act="%s"></i>`, html.EscapeString(n.ID), act)
	}
	b.WriteString("</div>")
	return b.String()
}

func chipsHTML(order []*sceneJS) string {
	var b strings.Builder
	for i, s := range order {
		sel, ti := "false", "-1"
		if i == 0 {
			sel, ti = "true", "0"
		}
		fmt.Fprintf(&b, `<button class="bl-chip" type="button" role="tab" id="bl-tab-%s" aria-selected="%s" aria-controls="bl-fig" tabindex="%s" data-i="%d"><span class="bl-chip-n">%02d</span><span class="bl-chip-t">%s</span><span class="bl-chip-s">%s</span></button>`+"\n",
			s.ID, sel, ti, i, i+1, html.EscapeString(s.Title), html.EscapeString(s.Sub))
	}
	return b.String()
}

func changeHTML(s *sceneJS) string {
	return fmt.Sprintf(`<figcaption class="code-title"><span class="code-file bl-chg-file">%s</span></figcaption>
<div class="code-body"><pre><code class="bl-chg-code">%s</code></pre></div>`, html.EscapeString(s.Change.File), s.Change.Lines)
}

func inspectorHTML(g *graph, js *jsData, s0 *sceneJS) string {
	n := g.byID[s0.Origin]
	i := n.Idx
	jn := js.Nodes[i]
	sn := js.Snips[jn.S]
	act := s0.Act[n.ID]
	if act == "" {
		act = "skip"
	}
	badge := map[string]string{"add": "created", "change": "updated in place", "replace": "replaced", "skip": "skipped"}[act]
	var reads []string
	for _, x := range jn.N {
		reads = append(reads, "<code>"+html.EscapeString(js.Nodes[x].ID)+"</code>")
	}
	readsText := "reads nothing"
	if len(reads) > 0 {
		readsText = "reads " + strings.Join(reads, ", ")
	}
	reachText := fmt.Sprintf("a change here can reach %d %s", len(jn.R), plural(len(jn.R), "node", "nodes"))
	if len(jn.R) == 0 {
		reachText = "nothing reads it"
	}
	return fmt.Sprintf(`<figcaption class="code-title"><span class="code-file bl-insp-file">%s</span><span class="bl-badge" data-act="%s">%s</span></figcaption>
<div class="code-body"><pre><code class="bl-insp-src">%s</code></pre></div>
<div class="bl-insp-meta"><p class="bl-insp-id"><b>%s</b> <span>%s</span></p><p class="bl-insp-line">%s</p><p class="bl-insp-rel">%s · %s</p></div>`,
		fmt.Sprintf("%s:%d-%d", sn.File, sn.From, sn.To), act, badge, sn.HTML, html.EscapeString(n.ID), html.EscapeString(n.Type), html.EscapeString(s0.Lines[n.ID]), readsText, reachText)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func writePartial(view viewOut, g *graph, sc map[string]*sceneJS, order []string, wide, tall *geom) error {
	tb, err := os.ReadFile(tmplPath)
	if err != nil {
		return err
	}
	text := string(tb)
	var ordered []*sceneJS
	for _, id := range order {
		ordered = append(ordered, sc[id])
	}
	s0 := ordered[0]
	// the lanes of the wide layout are the lanes of the graph
	var lanes []string
	seen := map[string]bool{}
	for _, n := range g.nodes {
		if !seen[n.Lane] {
			seen[n.Lane] = true
			lanes = append(lanes, n.Lane)
		}
	}
	sort.Slice(lanes, func(a, b int) bool {
		if lanes[a] == "root" || lanes[b] == "root" {
			return lanes[a] == "root"
		}
		return lanes[a] < lanes[b]
	})
	repl := map[string]string{
		"<!--@chips-->":     chipsHTML(ordered),
		"<!--@svg-->":       svgHTML(g, wide, lanes, s0, len(g.nodes)),
		"<!--@tally-->":     tallyHTML(g, s0),
		"<!--@term-->":      s0.Term,
		"<!--@change-->":    changeHTML(s0),
		"<!--@insp-->":      inspectorHTML(g, view.JS, s0),
		"<!--@generated-->": "<!-- Generated by site/demos/blast/derive.go from site/demos/blast/band.html and site/data/blast.json. Edit the template, not this file. -->",
	}
	for k, v := range repl {
		if !strings.Contains(text, k) {
			return fmt.Errorf("%s has no %s", tmplPath, k)
		}
		text = strings.ReplaceAll(text, k, v)
	}
	if strings.Contains(text, "<!--@") {
		return fmt.Errorf("%s has a placeholder that derive does not know", tmplPath)
	}
	if err := verifyTokens(text, view); err != nil {
		return err
	}
	return os.WriteFile(outPath, []byte(text), 0o644)
}

var tokenRe = regexp.MustCompile(`\{\{(data|json) ([^{}]*)\}\}`)

// verifyTokens resolves every {{data ...}} and {{json ...}} token of the
// partial against the file that derive has just written.
func verifyTokens(text string, view viewOut) error {
	raw, err := os.ReadFile(dataPath)
	if err != nil {
		return err
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	for _, m := range tokenRe.FindAllStringSubmatch(text, -1) {
		parts := strings.Split(strings.TrimSpace(m[2]), ".")
		if parts[0] != "blast" {
			return fmt.Errorf("token %s: this partial reads site/data/blast.json only", m[0])
		}
		cur := root
		for _, k := range parts[1:] {
			switch x := cur.(type) {
			case map[string]any:
				v, ok := x[k]
				if !ok {
					return fmt.Errorf("token %s: no key %q", m[0], k)
				}
				cur = v
			case []any:
				var i int
				if _, err := fmt.Sscanf(k, "%d", &i); err != nil || i < 0 || i >= len(x) {
					return fmt.Errorf("token %s: no index %q", m[0], k)
				}
				cur = x[i]
			default:
				return fmt.Errorf("token %s: %q is not inside an object or an array", m[0], k)
			}
		}
	}
	return nil
}
