// Command skillgen builds the reference files of the pravo skill from the
// documentation, and checks that the files in the repository are the ones it
// would build.
//
//	go run ./tools/skillgen            # write skills/pravo/references
//	go run ./tools/skillgen -check     # fail when a file is missing, stale or extra
//	go run ./tools/skillgen -zip OUT -version 0.2.0   # pack the skill for a release
//
// SKILL.md is written by hand. Everything below skills/pravo/references is
// generated, so a change to a documentation page reaches the skill by running
// this program, and the check fails until it has. See README.md.
package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const siteURL = "https://pravo.systems/"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("skillgen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	docs := fs.String("docs", "docs", "the documentation directory")
	skill := fs.String("skill", "skills/pravo", "the skill directory")
	check := fs.Bool("check", false, "report what differs and exit 1, writing nothing")
	zipOut := fs.String("zip", "", "write the skill to this zip file, for a release")
	version := fs.String("version", "", "with -zip: the pravo version the skill is packed for")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	files, err := build(*docs, *skill)
	if err != nil {
		fmt.Fprintln(stderr, "skillgen:", err)
		return 1
	}
	switch {
	case *zipOut != "":
		if err := writeZip(*zipOut, *skill, *version, files); err != nil {
			fmt.Fprintln(stderr, "skillgen:", err)
			return 1
		}
		fmt.Fprintf(stdout, "wrote %s\n", *zipOut)
	case *check:
		problems := compare(*skill, files)
		for _, p := range problems {
			fmt.Fprintln(stderr, "skillgen:", p)
		}
		if len(problems) > 0 {
			fmt.Fprintln(stderr, "skillgen: run go run ./tools/skillgen and commit the result")
			return 1
		}
		fmt.Fprintf(stdout, "the skill is up to date: %d reference files\n", len(files))
	default:
		n, err := write(*skill, files)
		if err != nil {
			fmt.Fprintln(stderr, "skillgen:", err)
			return 1
		}
		fmt.Fprintf(stdout, "wrote %d reference files, removed %d\n", len(files), n)
	}
	return 0
}

// A page is one documentation page that goes into the skill.
type page struct {
	Rel     string // "reference/plan.md", below the documentation directory
	Section string
	Title   string
	Desc    string
	Group   string
	Order   int
	Body    string
}

// sections lists the documentation sections that the skill takes, in the order
// of its index.
var sections = []struct{ Dir, Title, Blurb string }{
	{"reference", "Reference", "One page for each command and each part of the specification, tables first."},
	{"concepts", "Concepts", "How pravo behaves: evaluation, plans, state, providers, policies."},
	{"how-to", "How-to", "One task each, for a reader who knows the basics."},
	{"learn", "Learn", "The tutorial on pravo (track B), the team track (track C) and the companion pages on CUE errors."},
	{"migrate", "Migrate", "From OpenTofu or Terraform: a runbook with its lookups."},
}

// skipped lists the pages the skill leaves out, as patterns on the path
// below the documentation directory. A pattern that matches no page is an
// error, so the list cannot go stale.
var skipped = []struct{ Pattern, Why string }{
	{`^learn/a\d\d-`, "track A teaches CUE from zero; the cheat sheet stays"},
	{`^learn/lab\d-`, "labs are exercises"},
	{`^learn/b11-`, "a project exercise"},
	{`(^|/)index\.md$`, "section indexes route readers; the index of the skill does that"},
}

// build reads the documentation and returns the generated files, by path
// below the skill directory.
func build(docsDir, skillDir string) (map[string][]byte, error) {
	pages, err := readPages(docsDir)
	if err != nil {
		return nil, err
	}
	included := map[string]*page{}
	for _, p := range pages {
		included[p.Rel] = p
	}
	out := map[string][]byte{}
	for _, p := range pages {
		body, err := convert(p, included)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", p.Rel, err)
		}
		out["references/"+p.Rel] = body
	}
	out["references/INDEX.md"] = index(pages, out)
	if err := checkCore(skillDir, out); err != nil {
		return nil, err
	}
	return out, nil
}

// readPages loads every page below the sections, drops the skipped ones and
// reports a skip pattern that matched nothing.
func readPages(docsDir string) ([]*page, error) {
	used := make([]bool, len(skipped))
	res := make([]*regexp.Regexp, len(skipped))
	for i, s := range skipped {
		res[i] = regexp.MustCompile(s.Pattern)
	}
	var pages []*page
	for _, sec := range sections {
		dir := filepath.Join(docsDir, sec.Dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			rel := sec.Dir + "/" + e.Name()
			skip := false
			for i, re := range res {
				if re.MatchString(rel) {
					used[i], skip = true, true
				}
			}
			if skip {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return nil, err
			}
			p, redirect, err := parsePage(rel, sec.Dir, data)
			if err != nil {
				return nil, err
			}
			if redirect {
				continue
			}
			pages = append(pages, p)
		}
	}
	for i, u := range used {
		if !u {
			return nil, fmt.Errorf("the skip pattern %q matches no page: remove it", skipped[i].Pattern)
		}
	}
	return pages, nil
}

// parsePage splits the front matter from the body. A redirect stub is not a
// page.
func parsePage(rel, section string, data []byte) (p *page, redirect bool, err error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, false, fmt.Errorf("%s: no front matter", rel)
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return nil, false, fmt.Errorf("%s: front matter does not end", rel)
	}
	var fm struct {
		Title       string `yaml:"title"`
		Description string `yaml:"description"`
		Group       string `yaml:"group"`
		Order       int    `yaml:"order"`
		Redirect    string `yaml:"redirect"`
	}
	if err := yaml.Unmarshal([]byte(text[4:4+end]), &fm); err != nil {
		return nil, false, fmt.Errorf("%s: front matter: %v", rel, err)
	}
	if fm.Redirect != "" {
		return nil, true, nil
	}
	if fm.Title == "" || fm.Description == "" {
		return nil, false, fmt.Errorf("%s: no title or description", rel)
	}
	return &page{Rel: rel, Section: section, Title: plain(fm.Title), Desc: plain(fm.Description), Group: fm.Group, Order: fm.Order,
		Body: text[4+end+5:]}, false, nil
}

var (
	codeSpanRe = regexp.MustCompile("`([^`]*)`")
	linkRe     = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]*)\)`)
	imageRe    = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
)

// plain removes the inline marks of a title or a description.
func plain(s string) string {
	s = codeSpanRe.ReplaceAllString(s, "$1")
	s = linkRe.ReplaceAllString(s, "$1")
	return strings.TrimSpace(s)
}

// kept containers keep their content; dropped ones are lesson furniture or
// pictures; a name in neither list is an error, so that a new container of the
// documentation is decided on here.
var (
	keptContainers    = map[string]bool{"compare": true, "tabs": true, "deep-dive": true, "mapping": true, "cards": true}
	droppedContainers = map[string]bool{"predict": true, "exercise": true, "check": true, "figure": true, "widget": true, "demo": true, "runbook": true}
)

// convert turns the Markdown source of a page into the Markdown of a
// reference file: the front matter becomes a heading, the attributes of code
// fences become words, the containers of the site become plain text and links
// point at files of the skill or at the site.
func convert(p *page, included map[string]*page) ([]byte, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- Generated from docs/%s by tools/skillgen. Do not edit. -->\n\n# %s\n\n%s\n\n", p.Rel, p.Title, p.Desc)
	lines := strings.Split(strings.TrimSpace(p.Body), "\n")
	var (
		inFence    bool
		fenceMark  string
		dropFence  bool
		dropOutFor bool // the next out block belongs to a dropped run block
		container  string
		drop       bool
		blank      = true
	)
	emit := func(s string) {
		if s == "" {
			if blank {
				return
			}
			blank = true
		} else {
			blank = false
		}
		b.WriteString(s)
		b.WriteByte('\n')
	}
	for i, line := range lines {
		if inFence {
			if strings.HasPrefix(strings.TrimSpace(line), fenceMark) && strings.TrimSpace(line) == fenceMark {
				inFence = false
				if !dropFence {
					emit(line)
				}
				continue
			}
			if !dropFence && !drop {
				b.WriteString(line)
				b.WriteByte('\n')
				blank = false
			}
			continue
		}
		trim := strings.TrimSpace(line)
		if m := fenceOpen(trim); m != "" {
			inFence = true
			fenceMark = m
			info, err := parseInfo(strings.TrimSpace(strings.TrimPrefix(trim, m)))
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", i+1, err)
			}
			dropFence = drop
			if !drop {
				switch {
				case info.hidden || info.check:
					dropFence = true
					if info.run {
						dropOutFor = true
					}
				case info.out && dropOutFor:
					dropFence = true
					dropOutFor = false
				}
			}
			if dropFence {
				continue
			}
			dropOutFor = false
			emit("")
			switch {
			case info.file != "":
				emit("File `" + info.file + "`:")
			case info.out:
				emit("Output:")
			case info.status != "":
				emit("Status: " + info.status + ".")
			}
			if info.file != "" || info.out || info.status != "" {
				emit("")
			}
			emit(fenceMark + info.lang)
			continue
		}
		// containers
		if strings.HasPrefix(trim, ":::") {
			name := strings.TrimSpace(strings.TrimPrefix(trim, ":::"))
			if name == "" { // the end of a container
				container, drop = "", false
				continue
			}
			word := strings.Fields(name)[0]
			args := strings.TrimSpace(strings.TrimPrefix(name, word))
			switch {
			case keptContainers[word]:
				container, drop = word, false
				if word == "deep-dive" && args != "" {
					emit("")
					emit("**" + plain(args) + "**")
					emit("")
				}
			case droppedContainers[word]:
				container, drop = word, true
				if word == "figure" && args != "" {
					emit("")
					emit("*Figure: " + plain(args) + "*")
					emit("")
				}
			default:
				return nil, fmt.Errorf("line %d: the container :::%s is not known to tools/skillgen: say in main.go whether the skill keeps it", i+1, word)
			}
			// A container with no body ends on the next line with :::.
			if (word == "demo" || word == "runbook") && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == ":::" {
				continue
			}
			continue
		}
		if drop {
			continue
		}
		if strings.HasPrefix(trim, "::") && !strings.HasPrefix(trim, ":::") && container != "" {
			continue // a part of a kept container: ::tab and the like
		}
		if strings.HasPrefix(trim, "<") && !strings.HasPrefix(trim, "<!--") {
			continue // raw HTML: figures, pictures
		}
		line = imageRe.ReplaceAllString(line, "*$1*")
		line = rewriteLinks(line, p, included)
		emit(strings.TrimRight(line, " \t"))
	}
	if inFence {
		return nil, errors.New("a code fence is not closed")
	}
	s := strings.TrimRight(b.String(), "\n") + "\n"
	return []byte(s), nil
}

func fenceOpen(trim string) string {
	for _, m := range []string{"````", "```", "~~~"} {
		if strings.HasPrefix(trim, m) {
			return m
		}
	}
	return ""
}

type info struct {
	lang   string
	file   string
	status string
	run    bool
	out    bool
	hidden bool
	check  bool
}

// parseInfo reads the info string of a fence (docs/AUTHORING.md section 4.1).
func parseInfo(s string) (info, error) {
	var in info
	toks, err := splitInfo(s)
	if err != nil {
		return in, err
	}
	for i, t := range toks {
		if i == 0 && !strings.Contains(t, "=") && t != "run" && t != "out" && t != "hidden" && t != "check" && t != "ln" {
			in.lang = t
			continue
		}
		k, v, _ := strings.Cut(t, "=")
		switch k {
		case "file":
			in.file = v
		case "status":
			in.status = v
		case "run":
			in.run = true
		case "out":
			in.out = true
		case "hidden":
			in.hidden = true
		case "check":
			in.check = true
		case "title", "hl", "ln", "exit":
		default:
			return in, fmt.Errorf("the fence attribute %q is not known to tools/skillgen", k)
		}
	}
	if in.lang == "" {
		switch {
		case in.run:
			in.lang = "sh"
		case in.out:
			in.lang = "text"
		}
	}
	return in, nil
}

// splitInfo splits on spaces and keeps quoted values together.
func splitInfo(s string) ([]string, error) {
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
			quote, started = r, true
		case r == ' ' || r == '\t':
			if started || cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("a quote is not closed in %q", s)
	}
	if started || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out, nil
}

// rewriteLinks points a link to a page of the documentation at the file of the
// skill when the page is in it, and at the site when it is not. A link to
// code or to another site stays.
func rewriteLinks(line string, p *page, included map[string]*page) string {
	return linkRe.ReplaceAllStringFunc(line, func(m string) string {
		sm := linkRe.FindStringSubmatch(m)
		text, target := sm[1], sm[2]
		if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "#") {
			return m
		}
		file, frag, _ := strings.Cut(target, "#")
		if frag != "" {
			frag = "#" + frag
		}
		if !strings.HasSuffix(file, ".md") {
			return text // a picture or a file of the repository: keep the words
		}
		rel := path.Clean(path.Join(path.Dir(p.Rel), file))
		if _, ok := included[rel]; ok {
			return "[" + text + "](" + file + frag + ")"
		}
		return "[" + text + "](" + siteURL + strings.TrimSuffix(rel, ".md") + ".html" + frag + ")"
	})
}

// index lists the reference files with what each covers.
func index(pages []*page, files map[string][]byte) []byte {
	var b strings.Builder
	b.WriteString("<!-- Generated by tools/skillgen. Do not edit. -->\n\n# Reference files\n\n")
	b.WriteString("Each file below is a page of the pravo documentation. Read the one that answers the question: the first column is the path below `references/`, and the size says what it costs to read. `grep` across the directory finds an error message or a flag.\n")
	for _, sec := range sections {
		var ps []*page
		for _, p := range pages {
			if p.Section == sec.Dir {
				ps = append(ps, p)
			}
		}
		if len(ps) == 0 {
			continue
		}
		groupOrder := map[string]int{}
		for _, p := range ps {
			if o, ok := groupOrder[p.Group]; !ok || p.Order < o {
				groupOrder[p.Group] = p.Order
			}
		}
		sort.SliceStable(ps, func(i, j int) bool {
			a, c := ps[i], ps[j]
			if a.Group != c.Group {
				if groupOrder[a.Group] != groupOrder[c.Group] {
					return groupOrder[a.Group] < groupOrder[c.Group]
				}
				return a.Group < c.Group
			}
			if a.Order != c.Order {
				return a.Order < c.Order
			}
			return a.Rel < c.Rel
		})
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", sec.Title, sec.Blurb)
		group := "\x00"
		for _, p := range ps {
			if p.Group != group {
				group = p.Group
				if group != "" {
					fmt.Fprintf(&b, "\n### %s\n\n", group)
				} else {
					b.WriteString("\n")
				}
				b.WriteString("| File | Covers | Size |\n|---|---|---|\n")
			}
			size := len(files["references/"+p.Rel])
			fmt.Fprintf(&b, "| [%s](%s) | %s | %s |\n", p.Rel, p.Rel, strings.ReplaceAll(p.Desc, "|", "\\|"), kb(size))
		}
	}
	return []byte(b.String())
}

func kb(n int) string { return strconv.Itoa((n+1023)/1024) + " KB" }

var frontRe = regexp.MustCompile(`(?s)^---\n(.*?)\n---\n`)

// checkCore checks the hand-written SKILL.md: its front matter, and that every
// reference file it names exists among the generated ones.
func checkCore(skillDir string, files map[string][]byte) error {
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return err
	}
	m := frontRe.FindSubmatch(data)
	if m == nil {
		return errors.New("SKILL.md has no front matter")
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal(m[1], &fm); err != nil {
		return fmt.Errorf("SKILL.md front matter: %v", err)
	}
	if fm.Name != "pravo" {
		return fmt.Errorf("SKILL.md: the name is %q, not pravo", fm.Name)
	}
	if fm.Description == "" || len(fm.Description) > 1024 {
		return fmt.Errorf("SKILL.md: the description must be 1 to 1024 characters, it has %d", len(fm.Description))
	}
	refRe := regexp.MustCompile(`references/[A-Za-z0-9_./-]+\.md`)
	for _, r := range refRe.FindAllString(string(data), -1) {
		if _, ok := files[r]; !ok {
			return fmt.Errorf("SKILL.md names %s, which is not a generated reference file", r)
		}
	}
	return nil
}

// write writes the generated files and removes the ones that are no longer
// generated. It returns the number removed.
func write(skillDir string, files map[string][]byte) (int, error) {
	removed := 0
	existing, _ := listGenerated(skillDir)
	for rel, data := range files {
		dst := filepath.Join(skillDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return 0, err
		}
		if old, err := os.ReadFile(dst); err == nil && bytes.Equal(old, data) {
			continue
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return 0, err
		}
	}
	for _, rel := range existing {
		if _, ok := files[rel]; !ok {
			if err := os.Remove(filepath.Join(skillDir, filepath.FromSlash(rel))); err != nil {
				return 0, err
			}
			removed++
		}
	}
	return removed, nil
}

// listGenerated lists the files below references/, as paths below the skill
// directory.
func listGenerated(skillDir string) ([]string, error) {
	var out []string
	root := filepath.Join(skillDir, "references")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(skillDir, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out, err
}

// compare lists the differences between the files on disk and the generated
// ones.
func compare(skillDir string, files map[string][]byte) []string {
	var out []string
	existing, _ := listGenerated(skillDir)
	have := map[string]bool{}
	for _, rel := range existing {
		have[rel] = true
	}
	var rels []string
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		old, err := os.ReadFile(filepath.Join(skillDir, filepath.FromSlash(rel)))
		switch {
		case err != nil:
			out = append(out, rel+" is missing")
		case !bytes.Equal(old, files[rel]):
			out = append(out, rel+" differs from the documentation it is built from")
		}
	}
	for _, rel := range existing {
		if _, ok := files[rel]; !ok {
			out = append(out, rel+" is not generated any more: remove it")
		}
	}
	return out
}

// writeZip packs the skill, SKILL.md and the generated files, with a VERSION
// file, under the directory name pravo. The entries have fixed times, so the
// same skill gives the same archive.
func writeZip(dst, skillDir, version string, files map[string][]byte) error {
	core, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return err
	}
	all := map[string][]byte{"SKILL.md": core, "VERSION": []byte(version + "\n")}
	for rel, data := range files {
		all[rel] = data
	}
	var names []string
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, n := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: "pravo/" + n, Method: zip.Deflate, Modified: stamp})
		if err != nil {
			return err
		}
		if _, err := w.Write(all[n]); err != nil {
			return err
		}
	}
	return zw.Close()
}
