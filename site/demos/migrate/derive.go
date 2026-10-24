//go:build ignore

// derive reads site/data/migrate.json, which tools/capture wrote from
// site/demos/migrate/page.md, and adds the "view" object that the migration
// demo embeds with {{data}} and {{json}} tokens:
//
//   - the three terminal runs as classified lines,
//   - the pairs of lines that correspond in main.tf and stack.cue, found from
//     the $from field of each CUE resource and the resource blocks of the HCL,
//   - the counts the demo quotes (records imported and moved, resources checked),
//     and the OpenTofu version that wrote the state file.
//
// The state file of page.md is the one that OpenTofu 1.13.1 wrote and that
// tofu/record.sh keeps in tofu/state/landing.tfstate; derive fails when the two
// differ (go run site/demos/migrate/fixtures.go -update copies the file into the page).
// It also writes the two highlighted code windows and the terminal markup into
// the regions of site/demos/migrate/embed.html that are marked
// <!--gen:NAME--> ... <!--/gen:NAME-->. The highlighting is done by tools/sitegen
// itself, so the spans are the ones the documentation pages use. Nothing in the
// view is typed by hand.
//
//	go run site/demos/migrate/derive.go          # rewrite the view and the regions
//	go run site/demos/migrate/derive.go -check   # fail when the partial is stale
//
// Run it from the root of the module.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	dataFile = "site/data/migrate.json"
	pageFile = "site/demos/migrate/page.md"
	partial  = "site/demos/migrate/embed.html"
	// stateFile is the state file that OpenTofu wrote, as record.sh kept it. The state block of page.md equals it.
	stateFile = "site/demos/migrate/tofu/state/landing.tfstate"
)

type fence struct {
	lang  string
	attrs map[string]string
	flags map[string]bool
	body  string
}

type line struct {
	Cls string `json:"cls"` // class of the line: out-dim, out-add or empty
	T   string `json:"t"`   // the text
}

type run struct {
	Cmd   string `json:"cmd"`
	Exit  int    `json:"exit"`
	Lines []line `json:"lines"`
}

type group struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // providers or resource
	HCL      string `json:"hcl"`  // the resource address in OpenTofu
	CUE      string `json:"cue"`  // the path in the specification
	HCLLines [2]int `json:"hcl_lines"`
	CUELines [2]int `json:"cue_lines"`
	Label    string `json:"label"`
}

type summary struct {
	Pre  string `json:"pre"`
	Add  string `json:"add"`
	M1   string `json:"m1"`
	Chg  string `json:"chg"`
	M2   string `json:"m2"`
	Dst  string `json:"dst"`
	Post string `json:"post"`
}

type view struct {
	Ran       string   `json:"ran"`
	Tofu      string   `json:"tofu"` // the OpenTofu version that wrote the state file
	CUE       string   `json:"cue"`
	Imported  int      `json:"imported"`
	Moved     int      `json:"moved"`
	Resources int      `json:"resources"`
	Providers int      `json:"providers"`
	Runs      []run    `json:"runs"`
	Summary   summary  `json:"summary"`
	Groups    []group  `json:"groups"`
	HCLFile   string   `json:"hcl_file"`
	CUEFile   string   `json:"cue_file"`
	Records   []string `json:"records"`
	Status    string   `json:"status"` // the first sentence of the empty plan
}

type block struct {
	Command string `json:"command"`
	Hidden  bool   `json:"hidden"`
	Exit    int    `json:"exit"`
	Output  string `json:"output"`
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "derive: "+format+"\n", a...)
	os.Exit(1)
}

var infoAttr = regexp.MustCompile(`(\w+)=("[^"]*"|\S+)`)

// fences reads the fenced blocks of a Markdown page.
func fences(src string) []fence {
	var out []fence
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if !strings.HasPrefix(l, "```") {
			continue
		}
		info := strings.TrimSpace(strings.TrimLeft(l, "`"))
		var body []string
		for i++; i < len(lines) && !strings.HasPrefix(lines[i], "```"); i++ {
			body = append(body, lines[i])
		}
		f := fence{attrs: map[string]string{}, flags: map[string]bool{}, body: strings.Join(body, "\n")}
		words := strings.Fields(info)
		if len(words) > 0 {
			f.lang = words[0]
		}
		for _, w := range words {
			f.flags[w] = true
		}
		for _, m := range infoAttr.FindAllStringSubmatch(info, -1) {
			f.attrs[m[1]] = strings.Trim(m[2], `"`)
		}
		out = append(out, f)
	}
	return out
}

// highlight returns the highlighted lines of each source, as sitegen writes
// them: <span class="line">...</span> without the line number span's content.
func highlight(srcs map[string][2]string) map[string][]string {
	tmp, err := os.MkdirTemp("", "migrate-hl")
	if err != nil {
		die("%v", err)
	}
	defer os.RemoveAll(tmp)
	must(os.MkdirAll(filepath.Join(tmp, "docs", "x"), 0o755))
	must(os.MkdirAll(filepath.Join(tmp, "site"), 0o755))
	must(os.WriteFile(filepath.Join(tmp, "site", "site.json"), []byte(`{"name":"pravo","title":"t","description":"d","pravo_version":"0.1","cue_version":"0.17","start_href":"x/index.html","sections":[{"id":"x","title":"X","blurb":"b"}]}`), 0o644))
	var names []string
	var md strings.Builder
	md.WriteString("---\ntitle: X\ndescription: d\n---\n\n")
	for name := range srcs {
		names = append(names, name)
	}
	sortStrings(names)
	for _, n := range names {
		md.WriteString("```" + srcs[n][0] + "\n" + srcs[n][1] + "\n```\n\n")
	}
	must(os.WriteFile(filepath.Join(tmp, "docs", "x", "index.md"), []byte(md.String()), 0o644))
	cmd := exec.Command("go", "run", "./tools/sitegen", "-docs", filepath.Join(tmp, "docs"), "-site", filepath.Join(tmp, "site"),
		"-out", filepath.Join(tmp, "out"), "-readme", "/nonexistent", "-lint=false", "-check=false")
	if b, err := cmd.CombinedOutput(); err != nil {
		die("sitegen: %v\n%s", err, b)
	}
	raw, err := os.ReadFile(filepath.Join(tmp, "out", "x", "index.html"))
	must(err)
	res := map[string][]string{}
	figs := regexp.MustCompile(`(?s)<div class="code-body"><pre><code>(.*?)</code></pre></div>`).FindAllStringSubmatch(string(raw), -1)
	if len(figs) != len(names) {
		die("sitegen wrote %d code windows for %d sources", len(figs), len(names))
	}
	for i, n := range names {
		body := figs[i][1]
		parts := strings.Split(body, "</span><span class=\"line\">")
		for j := range parts {
			p := parts[j]
			if j == 0 {
				p = strings.TrimPrefix(p, `<span class="line">`)
			}
			if j == len(parts)-1 {
				p = strings.TrimSuffix(p, "</span>")
			}
			res[n] = append(res[n], p)
		}
	}
	return res
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func must(err error) {
	if err != nil {
		die("%v", err)
	}
}

// blockEnd returns the index of the line that closes the block opened on line
// i, by counting braces outside strings. Braces in the interpolations of a
// string do not count.
func blockEnd(lines []string, i int) int {
	depth := 0
	for j := i; j < len(lines); j++ {
		in := false
		for k := 0; k < len(lines[j]); k++ {
			c := lines[j][k]
			switch {
			case c == '\\' && in:
				k++
			case c == '"':
				in = !in
			case !in && c == '{':
				depth++
			case !in && c == '}':
				depth--
			}
		}
		if depth <= 0 {
			return j
		}
	}
	return len(lines) - 1
}

var (
	hclResource  = regexp.MustCompile(`^resource "([^"]+)" "([^"]+)" \{`)
	cueTop       = regexp.MustCompile(`^([a-z_][a-z0-9_]*): .*\{$`)
	cueFrom      = regexp.MustCompile(`\$from:\s+"([^"]+)"`)
	moveLine     = regexp.MustCompile(`^  # (\S+) will be moved to (\S+) \(`)
	planSummary  = regexp.MustCompile(`^(Plan: )(\d+ to add)(, )(\d+ to change)(, )(\d+ to destroy)(\.|, \d+ to move\.)$`)
	validateLine = regexp.MustCompile(`^Success! (\d+) resources, .* across (\d+) providers\.$`)
	importLine   = regexp.MustCompile(`^Imported (\d+) resources and (\d+) outputs `)
)

func classify(l string) string {
	switch {
	case strings.HasPrefix(l, "  # "), strings.HasPrefix(l, "Graph:"), strings.HasPrefix(l, "Renamed instances"), strings.HasPrefix(l, "pravo will perform"):
		return "out-dim"
	case strings.HasPrefix(l, "No changes."):
		return "out-add"
	}
	return ""
}

func main() {
	check := flag.Bool("check", false, "fail when the partial or the data is stale; write nothing")
	flag.Parse()

	rawData, err := os.ReadFile(dataFile)
	must(err)
	var doc map[string]json.RawMessage
	must(json.Unmarshal(rawData, &doc))
	var blocks []block
	must(json.Unmarshal(doc["blocks"], &blocks))
	var version []string
	must(json.Unmarshal(doc["version"], &version))

	var shown []block
	for _, b := range blocks {
		if !b.Hidden {
			shown = append(shown, b)
		}
	}
	want := []string{"pravo state import-tfstate terraform.tfstate", "pravo plan", "pravo plan", "pravo validate", "pravo state list"}
	if len(shown) != len(want) {
		die("%s has %d shown runs, the band needs %d", dataFile, len(shown), len(want))
	}
	for i, w := range want {
		if shown[i].Command != w {
			die("shown run %d is %q, the band needs %q", i, shown[i].Command, w)
		}
		if shown[i].Exit != 0 {
			die("run %q exited %d", w, shown[i].Exit)
		}
	}

	v := view{Ran: strings.TrimPrefix(version[0], "pravo "), CUE: strings.TrimPrefix(version[1], "cue ")}
	for i := 0; i < 3; i++ {
		r := run{Cmd: shown[i].Command, Exit: shown[i].Exit}
		for _, l := range strings.Split(shown[i].Output, "\n") {
			r.Lines = append(r.Lines, line{Cls: classify(l), T: l})
		}
		v.Runs = append(v.Runs, r)
	}
	if m := importLine.FindStringSubmatch(v.Runs[0].Lines[0].T); m != nil {
		v.Imported, _ = strconv.Atoi(m[1])
	} else {
		die("the import output does not start with the count line")
	}
	for _, l := range v.Runs[1].Lines {
		if m := moveLine.FindStringSubmatch(l.T); m != nil {
			v.Moved++
			v.Records = append(v.Records, m[1]+" -> "+m[2])
		}
		if m := planSummary.FindStringSubmatch(l.T); m != nil {
			v.Summary = summary{m[1], m[2], m[3], m[4], m[5], m[6], m[7]}
		}
	}
	if v.Summary.Pre == "" {
		die("the first plan has no summary line")
	}
	if m := validateLine.FindStringSubmatch(shown[3].Output); m != nil {
		v.Resources, _ = strconv.Atoi(m[1])
		v.Providers, _ = strconv.Atoi(m[2])
	} else {
		die("validate printed %q", shown[3].Output)
	}
	if last := v.Runs[2].Lines[0].T; !strings.HasPrefix(last, "No changes.") {
		die("the second plan is not empty: %q", last)
	} else {
		v.Status = strings.TrimSuffix(last[:strings.Index(last, ".")+1], ".")
	}

	// the two files, and the state file they belong to
	var hclSrc, cueSrc, stateSrc string
	for _, f := range fences(readFile(pageFile)) {
		switch {
		case f.lang == "json" && f.attrs["file"] == "terraform.tfstate":
			stateSrc = f.body + "\n"
		case f.lang == "hcl" && f.attrs["title"] == "main.tf":
			hclSrc = f.body
		case f.lang == "cue" && f.attrs["file"] == "stack.cue":
			cueSrc = f.body
		}
	}
	if hclSrc == "" || cueSrc == "" || stateSrc == "" {
		die("%s lacks the main.tf, the stack.cue or the terraform.tfstate block", pageFile)
	}
	if strings.TrimRight(readFile(stateFile), "\n") != strings.TrimRight(stateSrc, "\n") {
		die("the terraform.tfstate block of %s differs from %s; run go run site/demos/migrate/fixtures.go -update", pageFile, stateFile)
	}
	v.HCLFile, v.CUEFile = "main.tf", "stack.cue"
	var tf struct {
		Version string `json:"terraform_version"`
	}
	must(json.Unmarshal([]byte(stateSrc), &tf))
	if tf.Version == "" {
		die("the state file of %s has no terraform_version", pageFile)
	}
	v.Tofu = tf.Version

	hl := strings.Split(hclSrc, "\n")
	cl := strings.Split(cueSrc, "\n")
	hclGroup := make([]int, len(hl)) // 0 = none, else group number
	cueGroup := make([]int, len(cl))
	mark := func(g []int, a, b, n int) {
		for k := a; k <= b; k++ {
			g[k] = n
		}
	}
	// the providers: the terraform block, and the imports through the last provider line
	for i, l := range hl {
		if strings.HasPrefix(l, "terraform {") {
			j := blockEnd(hl, i)
			v.Groups = append(v.Groups, group{ID: "g1", Kind: "providers", HCL: "required_providers", CUE: "$random, $local", Label: "providers"})
			mark(hclGroup, i, j, 1)
			v.Groups[0].HCLLines = [2]int{i + 1, j + 1}
			break
		}
	}
	first, lastP := -1, -1
	for i, l := range cl {
		if strings.HasPrefix(l, "import ") && first < 0 {
			first = i
		}
		if strings.HasPrefix(l, "$") && !strings.Contains(l, "#Resource") {
			lastP = i
		}
	}
	if len(v.Groups) != 1 || first < 0 || lastP < first {
		die("cannot find the provider lines in the two files")
	}
	mark(cueGroup, first, lastP, 1)
	v.Groups[0].CUELines = [2]int{first + 1, lastP + 1}
	for i := first; i <= lastP; i++ {
		if strings.TrimSpace(cl[i]) == "" {
			cueGroup[i] = 0
		}
	}
	// the resources: a top-level CUE field whose body names the record with $from
	hclBlocks := map[string][2]int{}
	for i, l := range hl {
		if m := hclResource.FindStringSubmatch(l); m != nil {
			hclBlocks[m[1]+"."+m[2]] = [2]int{i, blockEnd(hl, i)}
		}
	}
	for i, l := range cl {
		m := cueTop.FindStringSubmatch(l)
		if m == nil || strings.HasPrefix(l, "$") {
			continue
		}
		j := blockEnd(cl, i)
		var from string
		for k := i; k <= j; k++ {
			if f := cueFrom.FindStringSubmatch(cl[k]); f != nil {
				from = f[1]
				break
			}
		}
		if from == "" {
			continue
		}
		addr := from
		if p := strings.Index(addr, `_\(`); p >= 0 {
			addr = addr[:p]
		}
		hb, ok := hclBlocks[addr]
		if !ok {
			die("the $from %q of %s matches no resource block of main.tf", from, m[1])
		}
		n := len(v.Groups) + 1
		g := group{ID: fmt.Sprintf("g%d", n), Kind: "resource", HCL: addr, CUE: m[1]}
		g.Label = addr + " to " + m[1]
		g.HCLLines = [2]int{hb[0] + 1, hb[1] + 1}
		g.CUELines = [2]int{i + 1, j + 1}
		mark(hclGroup, hb[0], hb[1], n)
		mark(cueGroup, i, j, n)
		v.Groups = append(v.Groups, g)
	}
	if len(v.Groups) != 1+len(hclBlocks) {
		die("%d resource blocks in main.tf but %d groups matched", len(hclBlocks), len(v.Groups)-1)
	}
	// the CUE path of a group, as the plan prints it: the first "will be moved to" target that starts with the field name
	for gi := 1; gi < len(v.Groups); gi++ {
		g := &v.Groups[gi]
		var targets []string
		for _, l := range v.Runs[1].Lines {
			if m := moveLine.FindStringSubmatch(l.T); m != nil && (m[2] == g.CUE || strings.HasPrefix(m[2], g.CUE+".")) {
				targets = append(targets, m[2])
			}
		}
		if len(targets) == 0 {
			die("the first plan moved no record to %s", g.CUE)
		}
		g.CUE = strings.Join(targets, ", ")
		g.Label = g.HCL + " to " + g.CUE
	}

	// highlighted code
	hlOut := highlight(map[string][2]string{"a-hcl": {"hcl", hclSrc}, "b-cue": {"cue", cueSrc}})
	region := map[string]string{}
	region["main.tf"] = codeRegion(hlOut["a-hcl"], hclGroup)
	region["stack.cue"] = codeRegion(hlOut["b-cue"], cueGroup)
	region["runs"] = runsRegion(v)

	// write the view into the data file, then the regions into the partial
	vb, err := json.Marshal(v)
	must(err)
	doc["view"] = vb
	out, err := marshalOrdered(doc)
	must(err)

	src := readFile(partial)
	for name, body := range region {
		src = replaceRegion(src, name, body)
	}
	if *check {
		if readFile(dataFile) != string(out) {
			die("%s is stale; run go run site/demos/migrate/derive.go", dataFile)
		}
		if readFile(partial) != src {
			die("%s is stale; run go run site/demos/migrate/derive.go", partial)
		}
		fmt.Println("derive: migrate is current")
		return
	}
	must(os.WriteFile(dataFile, out, 0o644))
	must(os.WriteFile(partial, []byte(src), 0o644))
	fmt.Printf("derive: view written to %s, %d regions to %s\n", dataFile, len(region), partial)
}

func readFile(p string) string {
	b, err := os.ReadFile(p)
	must(err)
	return string(b)
}

// marshalOrdered writes the document with its keys in a fixed order and two spaces of indent.
func marshalOrdered(doc map[string]json.RawMessage) ([]byte, error) {
	order := []string{"page", "title", "version", "blocks", "view"}
	var b strings.Builder
	b.WriteString("{\n")
	first := true
	for _, k := range order {
		raw, ok := doc[k]
		if !ok {
			continue
		}
		var buf strings.Builder
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("  ", "  ")
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
		if !first {
			b.WriteString(",\n")
		}
		first = false
		kb, _ := json.Marshal(k)
		b.WriteString("  " + string(kb) + ": " + strings.TrimRight(buf.String(), "\n"))
	}
	b.WriteString("\n}\n")
	return []byte(b.String()), nil
}

// codeRegion writes the lines of a code window. A line that belongs to a pair
// carries data-g; the first line of a pair also carries data-gs.
func codeRegion(lines []string, groups []int) string {
	var b strings.Builder
	for i, l := range lines {
		attr := ""
		if groups[i] > 0 {
			attr = fmt.Sprintf(` data-g="g%d"`, groups[i])
		}
		if !strings.HasSuffix(l, "\n") {
			l += "\n"
		}
		b.WriteString(`<span class="line"` + attr + ">" + l + "</span>")
	}
	return b.String()
}

// runsRegion writes the three runs of the terminal. The text of every line
// comes from a {{data}} token, so a new capture shows at the next build; the
// class and the number of lines are checked by derive -check.
func runsRegion(v view) string {
	var b strings.Builder
	for ri, r := range v.Runs {
		fmt.Fprintf(&b, `<div class="mo-run" data-run="%d">`+"\n", ri)
		fmt.Fprintf(&b, `<div class="term-line cmd"><span class="prompt">$</span><span class="cmd-text">{{data migrate.view.runs.%d.cmd}}</span></div>`+"\n", ri)
		b.WriteString(`<pre class="term-out"><code>`)
		for li, l := range r.Lines {
			cls := "mo"
			if l.Cls != "" {
				cls += " " + l.Cls
			}
			if l.T == "" {
				cls += " mo-gap" // a blank line of the output, drawn at half height
			}
			tok := fmt.Sprintf("{{data migrate.view.runs.%d.lines.%d.t}}", ri, li)
			if l.T == "" {
				tok = ""
			}
			if planSummary.MatchString(l.T) {
				s := "migrate.view.summary."
				tok = "{{data " + s + "pre}}<span class=\"out-add\">{{data " + s + "add}}</span>{{data " + s + "m1}}<span class=\"out-change\">{{data " + s + "chg}}</span>{{data " + s + "m2}}<span class=\"out-destroy\">{{data " + s + "dst}}</span>{{data " + s + "post}}"
			}
			if ri == 2 && li == 0 {
				tok += `<span class="mo-check" aria-hidden="true"></span>`
			}
			b.WriteString(`<span class="` + cls + `">` + tok + "\n</span>")
		}
		b.WriteString("</code></pre>\n</div>\n")
	}
	return b.String()
}

var regionRe = func(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?s)<!--gen:` + regexp.QuoteMeta(name) + `-->.*?<!--/gen:` + regexp.QuoteMeta(name) + `-->`)
}

func replaceRegion(src, name, body string) string {
	re := regionRe(name)
	if !re.MatchString(src) {
		die("%s has no <!--gen:%s--> region", partial, name)
	}
	rep := "<!--gen:" + name + "-->" + body + "<!--/gen:" + name + "-->"
	return re.ReplaceAllLiteralString(src, rep)
}
