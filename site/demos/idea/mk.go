//go:build ignore

// mk builds the derived data and the embed file of one demo from its
// capture page. Run it from the module root, after tools/capture has recorded
// the page (site/demos/idea/build.sh does both):
//
//	go run site/demos/idea/mk.go hero|idea
//
// It reads site/demos/NAME/page.md, site/data/NAME.json (written by
// `capture -json`) and site/demos/NAME/partial.html, adds the derived keys to
// site/data/NAME.json, and writes the output (site/demos/NAME/embed.html, or the hero partial). Code windows and
// terminals in the partial are the figures that tools/sitegen renders for the
// blocks of the page, so the highlighting is the generator's own. Nothing is
// typed by hand: every number and every output comes from the recorded run.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "mk: "+format+"\n", a...)
	os.Exit(1)
}

func check(err error) {
	if err != nil {
		die("%v", err)
	}
}

// ---------------------------------------------------------------------------
// The capture page

type fence struct {
	N     int // 1-based ordinal among the fenced blocks, as in the capture JSON
	Line  int
	Info  string
	Lang  string
	Toks  []string // raw info tokens after the language
	Keys  map[string]string
	Flags map[string]bool
	Body  string
	fig   string // rendered figure, when the block has one
	hasFg bool
}

func (f *fence) id() string { return f.Keys["id"] }

var fenceOpen = regexp.MustCompile("^( {0,3})(`{3,})(.*)$")

func splitInfo(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	started := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			started = true
		case r == ' ' || r == '\t':
			if started || cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if started || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func parsePage(path string) []*fence {
	raw, err := os.ReadFile(path)
	check(err)
	lines := strings.Split(string(raw), "\n")
	var fs []*fence
	for i := 0; i < len(lines); i++ {
		m := fenceOpen.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		ticks := len(m[2])
		f := &fence{N: len(fs) + 1, Line: i + 1, Info: strings.TrimSpace(m[3]), Keys: map[string]string{}, Flags: map[string]bool{}}
		var body []string
		j := i + 1
		for ; j < len(lines); j++ {
			t := strings.TrimRight(lines[j], " \t")
			if strings.HasPrefix(strings.TrimLeft(t, " "), strings.Repeat("`", ticks)) && strings.Trim(strings.TrimLeft(t, " "), "`") == "" {
				break
			}
			body = append(body, lines[j])
		}
		f.Body = strings.Join(body, "\n")
		toks := splitInfo(f.Info)
		for k, t := range toks {
			key, val, has := strings.Cut(t, "=")
			if k == 0 && !has && key != "run" && key != "out" && key != "hidden" && key != "check" && key != "ln" {
				f.Lang = t
				continue
			}
			f.Toks = append(f.Toks, t)
			if has {
				f.Keys[key] = val
			} else {
				f.Flags[key] = true
			}
		}
		fs = append(fs, f)
		i = j
	}
	return fs
}

// ---------------------------------------------------------------------------
// Figures from tools/sitegen

var ownKeys = map[string]bool{"id": true, "beats": true, "nocount": true}

// renderFigures renders the blocks of the page with tools/sitegen in a private
// directory and attaches each figure to its block.
func renderFigures(fs []*fence) {
	tmp, err := os.MkdirTemp("", "mk-")
	check(err)
	defer os.RemoveAll(tmp)
	check(os.MkdirAll(filepath.Join(tmp, "docs", "learn"), 0o755))
	check(os.MkdirAll(filepath.Join(tmp, "site"), 0o755))
	conf := `{"name":"pravo","title":"t","description":"d","pravo_version":"0.1","cue_version":"0.17","start_href":"learn/index.html","sections":[{"id":"learn","title":"Learn","blurb":"b"}]}`
	check(os.WriteFile(filepath.Join(tmp, "site", "site.json"), []byte(conf), 0o644))
	check(os.WriteFile(filepath.Join(tmp, "docs", "learn", "index.md"), []byte("---\ntitle: Learn\ndescription: x\norder: 1\n---\n\nx\n"), 0o644))
	var b strings.Builder
	b.WriteString("---\ntitle: Demo\ndescription: x\norder: 2\n---\n\n")
	var order []*fence
	for _, f := range fs {
		var toks []string
		for _, t := range f.Toks {
			key, _, _ := strings.Cut(t, "=")
			if ownKeys[key] {
				continue
			}
			if strings.ContainsAny(t, " \t") { // a quoted value
				k, v, _ := strings.Cut(t, "=")
				t = k + `="` + v + `"`
			}
			toks = append(toks, t)
		}
		info := f.Lang
		if len(toks) > 0 {
			info += " " + strings.Join(toks, " ")
		}
		b.WriteString("```" + strings.TrimSpace(info) + "\n" + f.Body + "\n```\n\n")
		if !f.Flags["out"] && !f.Flags["hidden"] && !f.Flags["check"] {
			order = append(order, f)
		}
	}
	check(os.WriteFile(filepath.Join(tmp, "docs", "learn", "demo.md"), []byte(b.String()), 0o644))
	cmd := exec.Command("go", "run", "./tools/sitegen", "-docs", filepath.Join(tmp, "docs"), "-site", filepath.Join(tmp, "site"),
		"-out", filepath.Join(tmp, "out"), "-readme", "/nonexistent", "-lint=false", "-check=false")
	var errb bytes.Buffer
	cmd.Stderr, cmd.Stdout = &errb, &errb
	if err := cmd.Run(); err != nil {
		die("sitegen failed: %v\n%s", err, errb.String())
	}
	out, err := os.ReadFile(filepath.Join(tmp, "out", "learn", "demo.html"))
	check(err)
	figs := regexp.MustCompile(`(?s)<figure class="(?:code|term)".*?</figure>`).FindAllString(string(out), -1)
	if len(figs) != len(order) {
		die("sitegen rendered %d figures for %d blocks", len(figs), len(order))
	}
	for i, f := range order {
		f.fig, f.hasFg = figs[i], true
	}
}

// ---------------------------------------------------------------------------
// The recorded runs

type record struct {
	Block  int    `json:"block"`
	Cmd    string `json:"command"`
	Exit   int    `json:"exit"`
	Output string `json:"output"`
}

type dataFile struct {
	path string
	m    map[string]any
	recs map[int]record
}

func loadData(demo string) *dataFile {
	p := filepath.Join("site", "data", demo+".json")
	raw, err := os.ReadFile(p)
	check(err)
	d := &dataFile{path: p, m: map[string]any{}, recs: map[int]record{}}
	check(json.Unmarshal(raw, &d.m))
	var probe struct {
		Blocks []record `json:"blocks"`
	}
	check(json.Unmarshal(raw, &probe))
	for _, r := range probe.Blocks {
		d.recs[r.Block] = r
	}
	return d
}

func (d *dataFile) save() {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	check(enc.Encode(d.m))
	check(os.WriteFile(d.path, buf.Bytes(), 0o644))
}

func byID(fs []*fence) map[string]*fence {
	m := map[string]*fence{}
	for _, f := range fs {
		if id := f.id(); id != "" {
			if m[id] != nil {
				die("id %q used twice", id)
			}
			m[id] = f
		}
	}
	return m
}

func (d *dataFile) run(f *fence) record {
	r, ok := d.recs[f.N]
	if !ok {
		die("no recorded run for block %d (%s); run tools/capture -json first", f.N, f.id())
	}
	return r
}

// ---------------------------------------------------------------------------
// Figure helpers

var (
	lineRe  = regexp.MustCompile(`(?s)<span class="line"><span class="ln"></span>(.*?)\n</span>`)
	tagRe   = regexp.MustCompile(`<[^>]*>`)
	codeRe  = regexp.MustCompile(`(?s)<pre><code>(.*)</code></pre>`)
	tokIDRe = regexp.MustCompile(`<span class="tok-id">([^<]*)</span>`)
)

func plain(s string) string { return html.UnescapeString(tagRe.ReplaceAllString(s, "")) }

// codeLines returns the inner HTML of each line of a code figure.
func codeLines(fig string) []string {
	m := codeRe.FindStringSubmatch(fig)
	if m == nil {
		die("figure without code")
	}
	var out []string
	for _, l := range lineRe.FindAllStringSubmatch(m[1], -1) {
		out = append(out, l[1])
	}
	return out
}

func addFigClass(fig, class string) string {
	return strings.Replace(fig, `<figure class="`, `<figure class="`+class+` `, 1)
}

func addFigAttr(fig, attr string) string {
	i := strings.Index(fig, ">")
	return fig[:i] + " " + attr + fig[i:]
}

// ---------------------------------------------------------------------------
// Templates

var tokenRe = regexp.MustCompile(`\{\{(fig|win)\s+([^{}]*)\}\}`)

func writePartial(demo, dest string, expand func(kind string, args []string) string) {
	raw, err := os.ReadFile(filepath.Join("site", "demos", demo, "partial.html"))
	check(err)
	out := tokenRe.ReplaceAllStringFunc(string(raw), func(tok string) string {
		m := tokenRe.FindStringSubmatch(tok)
		return expand(m[1], strings.Fields(m[2]))
	})
	check(os.WriteFile(dest, []byte(out), 0o644))
	fmt.Printf("mk: wrote %s (%d bytes)\n", dest, len(out))
}

func figByID(ids map[string]*fence, id string) *fence {
	f := ids[id]
	if f == nil || !f.hasFg {
		die("no figure for id %q", id)
	}
	return f
}

// plainFig expands {{fig ID [ln] [class=NAME]}}.
func plainFig(ids map[string]*fence, args []string) string {
	if len(args) == 0 {
		die("{{fig}} needs an id")
	}
	fig := figByID(ids, args[0]).fig
	for _, a := range args[1:] {
		switch {
		case a == "ln":
			fig = addFigAttr(fig, "data-ln")
		case strings.HasPrefix(a, "class="):
			fig = addFigClass(fig, strings.TrimPrefix(a, "class="))
		case strings.HasPrefix(a, "attr="):
			fig = addFigAttr(fig, strings.TrimPrefix(a, "attr="))
		default:
			die("{{fig %s}}: unknown option %q", args[0], a)
		}
	}
	return fig
}

func main() {
	if len(os.Args) != 2 {
		die("usage: go run site/demos/idea/mk.go hero|idea")
	}
	if _, err := os.Stat("go.mod"); err != nil {
		die("run from the module root")
	}
	demo := os.Args[1]
	fs := parsePage(filepath.Join("site", "demos", demo, "page.md"))
	renderFigures(fs)
	d := loadData(demo)
	switch demo {
	case "hero":
		mkHero(fs)
	case "idea":
		mkIdea(fs, d)
	default:
		die("unknown demo %q", demo)
	}
	d.save()
}

// ---------------------------------------------------------------------------
// hero: the file and the plan of the first band

func mkHero(fs []*fence) {
	ids := byID(fs)
	writePartial("hero", "site/landing/00-hero.html", func(kind string, args []string) string {
		if kind != "fig" {
			die("hero: the token {{%s}} is not used by the hero", kind)
		}
		return plainFig(ids, args)
	})
}

// ---------------------------------------------------------------------------
// idea: the unify widget and the four programs

func mkIdea(fs []*fence, d *dataFile) {
	ids := byID(fs)
	type run struct {
		Value  string `json:"value"`
		OK     bool   `json:"ok"`
		Exit   int    `json:"exit"`
		Output string `json:"output"`
	}
	var runs []run
	var merged string
	for _, pair := range [][2]string{{"v3", "r3"}, {"v0", "r0"}, {"v11", "r11"}, {"vs3", "rs3"}} {
		file, cmd := ids[pair[0]], ids[pair[1]]
		if file == nil || cmd == nil {
			die("idea: missing block %v", pair)
		}
		ls := strings.Split(strings.TrimRight(file.Body, "\n"), "\n")
		var first, last string
		for _, l := range ls {
			if strings.HasPrefix(l, "size: ") {
				if first == "" {
					first = strings.TrimPrefix(l, "size: ")
				}
				last = strings.TrimPrefix(l, "size: ")
			}
		}
		if merged == "" {
			merged = first
		} else if merged != first {
			die("idea: the constraint differs between programs")
		}
		r := d.run(cmd)
		runs = append(runs, run{Value: last, OK: r.Exit == 0, Exit: r.Exit, Output: r.Output})
	}
	left, right, ok := strings.Cut(merged, " & ")
	if !ok {
		die("idea: cannot split %q", merged)
	}
	type chip struct {
		Value  string `json:"value"`
		OK     bool   `json:"ok"`
		Result string `json:"result"`
	}
	var chips []chip
	for _, r := range runs {
		res := r.Output
		if !r.OK {
			// the Error line names the directory; the rest is the message
			if _, rest, found := strings.Cut(res, "\n"); found {
				res = rest
			}
		}
		chips = append(chips, chip{r.Value, r.OK, res})
	}
	d.m["widget"] = map[string]any{
		"title":  "Unification",
		"left":   map[string]string{"tag": "a type", "label": left},
		"right":  map[string]string{"tag": "a range", "label": right},
		"merged": merged,
		"chips":  chips,
	}
	d.m["runs"] = runs
	d.m["constraint"] = merged
	writePartial("idea", "site/demos/idea/embed.html", func(kind string, args []string) string {
		if kind != "fig" {
			die("idea: only {{fig}} is used")
		}
		return plainFig(ids, args)
	})
}
