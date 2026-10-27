// Command contrast checks the colour tokens of the site stylesheet.
//
// It reads the token blocks of site/assets/style.css (the light block, the
// explicit dark block and the prefers-color-scheme dark block), resolves every
// text and interface colour pair the design system uses, and fails when a text
// pair is below 4.5:1 or a non-text pair is below 3:1. Translucent tokens are
// composited over the surface they sit on.
//
// Usage (from the module root):
//
//	go run ./tools/sitegen/contrast            # report failures, exit 1 on any
//	go run ./tools/sitegen/contrast -v         # list every pair
//	go run ./tools/sitegen/contrast -css path/to/style.css
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type rgba struct{ r, g, b, a float64 }

func (c rgba) over(u rgba) rgba {
	return rgba{
		c.r*c.a + u.r*(1-c.a),
		c.g*c.a + u.g*(1-c.a),
		c.b*c.a + u.b*(1-c.a),
		1,
	}
}

func lin(v float64) float64 {
	v /= 255
	if v <= 0.03928 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func (c rgba) lum() float64 { return 0.2126*lin(c.r) + 0.7152*lin(c.g) + 0.0722*lin(c.b) }

func ratio(a, b rgba) float64 {
	la, lb := a.lum(), b.lum()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

var (
	hexRe  = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
	rgbaRe = regexp.MustCompile(`^rgba?\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)\s*(?:,\s*([\d.]+)\s*)?\)$`)
	declRe = regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*([^;]+);`)
)

func parseColor(v string) (rgba, bool) {
	v = strings.TrimSpace(v)
	if m := hexRe.FindStringSubmatch(v); m != nil {
		h := m[1]
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		n, _ := strconv.ParseUint(h, 16, 32)
		return rgba{float64(n >> 16 & 255), float64(n >> 8 & 255), float64(n & 255), 1}, true
	}
	if m := rgbaRe.FindStringSubmatch(v); m != nil {
		f := func(s string) float64 { x, _ := strconv.ParseFloat(s, 64); return x }
		a := 1.0
		if m[4] != "" {
			a = f(m[4])
		}
		return rgba{f(m[1]), f(m[2]), f(m[3]), a}, true
	}
	return rgba{}, false
}

// block returns the declarations inside the first block whose selector line
// starts with sel, after position from.
func block(css string, sel string, from int) (string, int, bool) {
	i := strings.Index(css[from:], sel)
	if i < 0 {
		return "", from, false
	}
	i += from
	open := strings.Index(css[i:], "{")
	if open < 0 {
		return "", from, false
	}
	depth, j := 0, i+open
	for ; j < len(css); j++ {
		if css[j] == '{' {
			depth++
		} else if css[j] == '}' {
			depth--
			if depth == 0 {
				break
			}
		}
	}
	return css[i+open+1 : j], j, true
}

func tokens(decls string) map[string]rgba {
	out := map[string]rgba{}
	for _, m := range declRe.FindAllStringSubmatch(decls, -1) {
		if c, ok := parseColor(m[2]); ok {
			out[m[1][2:]] = c
		}
	}
	return out
}

type pair struct {
	fg, bg string
	min    float64
	use    string
}

// An expression is a token name, optionally "@under" (composite over another
// expression) or "mix(token,pct)" (the token at pct percent opacity).
func resolve(expr string, t map[string]rgba) (rgba, error) {
	base, under, hasUnder := strings.Cut(expr, "@")
	var c rgba
	if strings.HasPrefix(base, "mix(") {
		in := strings.TrimSuffix(strings.TrimPrefix(base, "mix("), ")")
		name, pct, _ := strings.Cut(in, ",")
		tok, ok := t[strings.TrimSpace(name)]
		if !ok {
			return c, fmt.Errorf("unknown token %q", name)
		}
		p, _ := strconv.ParseFloat(strings.TrimSpace(pct), 64)
		c = tok
		c.a = p / 100
	} else {
		tok, ok := t[base]
		if !ok {
			return c, fmt.Errorf("unknown token %q", base)
		}
		c = tok
	}
	if c.a < 1 {
		if !hasUnder {
			return c, fmt.Errorf("%s is translucent and needs @under", expr)
		}
		u, err := resolve(under, t)
		if err != nil {
			return c, err
		}
		c = c.over(u)
	}
	return c, nil
}

// Pairs that depend on the theme.
var themed = []pair{
	{"text", "bg", 4.5, "body text"},
	{"text", "surface", 4.5, "text on cards, callouts, tables"},
	{"text", "surface-2", 4.5, "text on inline code, summaries"},
	{"text-2", "bg", 4.5, "secondary text, nav"},
	{"text-2", "surface", 4.5, "secondary text on surfaces"},
	{"text-2", "surface-2", 4.5, "secondary text on inline code"},
	{"text-3", "bg", 4.5, "tertiary text: kickers, breadcrumbs, labels"},
	{"text-3", "surface", 4.5, "tertiary text on surfaces: meta labels, pager labels"},
	{"text-3", "surface-2", 4.5, "tertiary text on inline code and progress"},
	{"text-3", "surface-3", 4.5, "tertiary text on the deepest surface"},
	{"accent", "bg", 4.5, "links, active tab"},
	{"accent", "surface", 4.5, "links on surfaces"},
	{"accent", "surface-2", 4.5, "links on inline code"},
	{"accent", "accent-wash@bg", 4.5, "accent text on its wash (callout tip, pressed state)"},
	{"accent", "accent-wash@surface", 4.5, "accent text on its wash over surfaces"},
	{"text", "accent-wash@bg", 4.5, "current sidebar link"},
	{"text-2", "accent-wash@bg", 4.5, "secondary text on the accent wash"},
	{"change", "bg", 4.5, "predict label, warning title"},
	{"change", "surface", 4.5, "predict label on surface"},
	{"change", "change-wash@bg", 4.5, "warning callout title"},
	{"conflict", "bg", 4.5, "pitfall title, errors"},
	{"conflict", "conflict-wash@bg", 4.5, "pitfall callout title"},
	{"key", "bg", 4.5, "deep dive label, note title"},
	{"key", "key-wash@bg", 4.5, "note callout title"},
	{"key", "surface", 4.5, "key text on surface"},
	{"text", "change-wash@bg", 4.5, "text in the warning callout"},
	{"text", "conflict-wash@bg", 4.5, "text in the pitfall callout"},
	{"text", "key-wash@bg", 4.5, "text in the note callout"},
	{"on-accent", "accent", 4.5, "primary button text, selection"},
	{"bg", "text", 4.5, "exercise label, skip link"},
	{"key", "bg", 3, "focus ring against the page"},
	{"key", "surface", 3, "focus ring against surfaces"},
	{"accent", "bg", 3, "reading progress, rail nodes, active indicators"},
	{"text-3", "bg", 3, "unfilled rail node outline"},
}

// Pairs on the dark code windows and terminals, the same in both themes.
var code = []pair{
	{"code-text", "code-bg", 4.5, "code text"},
	{"code-text", "code-bar", 4.5, "file tab text"},
	{"code-text", "code-hl@code-bg", 4.5, "text on a highlighted line"},
	{"code-dim", "code-bg", 4.5, "line numbers, dim output, comments"},
	{"code-dim", "code-bar", 4.5, "title bar text, ran-on tag"},
	{"code-add", "code-bg", 4.5, "plan glyph add, valid badge"},
	{"code-chg", "code-bg", 4.5, "plan glyph change, prompt"},
	{"code-del", "code-bg", 4.5, "plan glyph destroy, error"},
	{"code-key", "code-bg", 4.5, "highlight accent"},
	{"tok-kw", "code-bg", 4.5, "syntax keyword"},
	{"tok-def", "code-bg", 4.5, "syntax definition"},
	{"tok-type", "code-bg", 4.5, "syntax type"},
	{"tok-fn", "code-bg", 4.5, "syntax function"},
	{"tok-str", "code-bg", 4.5, "syntax string"},
	{"tok-num", "code-bg", 4.5, "syntax number"},
	{"tok-com", "code-bg", 4.5, "syntax comment"},
	{"tok-op", "code-bg", 4.5, "syntax operator"},
	{"tok-id", "code-bg", 4.5, "syntax identifier"},
	{"tok-kw", "code-hl@code-bg", 4.5, "keyword on a highlighted line"},
	{"tok-str", "code-hl@code-bg", 4.5, "string on a highlighted line"},
	{"tok-com", "code-hl@code-bg", 4.5, "comment on a highlighted line"},
	{"tok-num", "code-hl@code-bg", 4.5, "number on a highlighted line"},
	{"code-add", "mix(code-add,12)@code-bg", 4.5, "valid badge on its tint"},
	{"code-del", "mix(code-del,12)@code-bg", 4.5, "conflict badge on its tint"},
	{"code-chg", "mix(code-chg,12)@code-bg", 4.5, "unintended badge on its tint"},
	{"code-add", "mix(code-add,10)@code-bg", 4.5, "added line"},
	{"code-text", "mix(code-add,10)@code-bg", 4.5, "text on an added line"},
	{"code-del", "mix(code-del,10)@code-bg", 4.5, "removed line"},
	{"code-add", "code-bar", 4.5, "exit chip, status on the bar"},
	{"code-del", "code-bar", 4.5, "exit chip on the bar"},
	{"code-add", "code-hl@code-bg", 4.5, "plan add on a highlighted line"},
	{"code-key", "code-bg", 3, "focus ring inside a code window"},
}

func main() {
	css := flag.String("css", "site/assets/style.css", "stylesheet to read")
	verbose := flag.Bool("v", false, "list every pair, not only failures")
	flag.Parse()
	b, err := os.ReadFile(*css)
	if err != nil {
		fmt.Fprintln(os.Stderr, "contrast:", err)
		os.Exit(2)
	}
	n, err := check(string(b), *verbose, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "contrast:", err)
		os.Exit(2)
	}
	if n > 0 {
		os.Exit(1)
	}
}

// check reads the token blocks of src, checks every pair, writes the report to
// w and returns the number of failures.
func check(src string, verbose bool, w io.Writer) (int, error) {
	rootDecls, _, ok := block(src, ":root {", 0)
	if !ok {
		return 0, errors.New("no :root block")
	}
	explicit, _, ok := block(src, `:root[data-theme="dark"] {`, 0)
	if !ok {
		return 0, errors.New(`no :root[data-theme="dark"] block`)
	}
	light := tokens(rootDecls)
	dark := map[string]rgba{}
	for k, v := range light {
		dark[k] = v
	}
	for k, v := range tokens(explicit) {
		dark[k] = v
	}

	failures := 0
	// The system-preference block must carry the same values as the explicit one.
	if mq, _, ok := block(src, `:root:not([data-theme="light"]) {`, 0); ok {
		sys := tokens(mq)
		exp := tokens(explicit)
		for k, v := range exp {
			if s, has := sys[k]; !has || s != v {
				fmt.Fprintf(w, "FAIL  dark blocks differ on --%s\n", k)
				failures++
			}
		}
		for k := range sys {
			if _, has := exp[k]; !has {
				fmt.Fprintf(w, "FAIL  dark blocks differ on --%s\n", k)
				failures++
			}
		}
	} else {
		fmt.Fprintln(w, "FAIL  no prefers-color-scheme dark block")
		failures++
	}

	total := 0
	run := func(name string, t map[string]rgba, pairs []pair) {
		fmt.Fprintf(w, "\n%s\n", name)
		for _, p := range pairs {
			fg, e1 := resolve(p.fg, t)
			bg, e2 := resolve(p.bg, t)
			if e1 != nil || e2 != nil {
				fmt.Fprintf(w, "  ERR   %-22s on %-30s %v %v\n", p.fg, p.bg, e1, e2)
				failures++
				continue
			}
			total++
			r := ratio(fg, bg)
			status := "ok  "
			if r < p.min {
				status = "FAIL"
				failures++
			}
			if status == "FAIL" || verbose {
				fmt.Fprintf(w, "  %s  %5.2f  (min %.1f)  %-12s on %-28s %s\n", status, r, p.min, p.fg, p.bg, p.use)
			}
		}
	}
	run("paper (light)", light, themed)
	run("ink (dark)", dark, themed)
	run("code windows and terminals (both themes)", light, code)

	fmt.Fprintf(w, "\n%d pairs checked, %d failures\n", total, failures)
	return failures, nil
}
