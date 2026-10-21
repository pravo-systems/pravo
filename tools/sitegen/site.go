package main

import (
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// messages collects the errors and warnings of one build so that a single run
// reports everything it finds.
type messages struct {
	errs  []string
	warns []string
}

func (m *messages) errorf(format string, a ...any) {
	m.errs = append(m.errs, fmt.Sprintf(format, a...))
}
func (m *messages) warnf(format string, a ...any) {
	m.warns = append(m.warns, fmt.Sprintf(format, a...))
}

// site is the whole documentation set of one build.
type site struct {
	cfg      *siteConfig
	docsDir  string
	siteDir  string
	outDir   string
	msgs     *messages
	sections []*section
	byDoc    map[string]*page // docs path such as "learn/a03.md" to page, redirects included
	gloss    map[string]glossEntry
	applies  string
	data     map[string]any // site/data/*.json read by data tokens in the landing partials

	landing       template.HTML // the landing page body, built before the messages are reported
	legacyLanding bool          // site/index.html is copied as it is

	minifier *assetMinifier    // nil when -minify=false
	minified map[string][]byte // site/assets JavaScript and CSS after minification, by path below assets/
	minStats minifyStats
}

// section is a configured section with its pages in reading order.
type section struct {
	cfg       *sectionConfig
	ID        string
	Title     string
	Blurb     string
	Pages     []*page // index first, then groups in order; redirects excluded
	Index     *page
	Groups    []*group // sidebar groups; the first may have an empty name
	Missing   bool     // no directory under docs/
	lessons   map[string]*page
	redirects []*page
}

type group struct {
	Name  string
	Pages []*page
}

// page is one Markdown source.
type page struct {
	Section  *section
	Doc      string // docs path, "learn/a03-writing-a-field-twice.md"
	Rel      string // path inside the section, "a03-writing-a-field-twice.md"
	File     string // file system path, for messages
	Out      string // output path relative to the site root, "learn/a03-writing-a-field-twice.html"
	Twin     string // output path of the Markdown twin
	IsIndex  bool
	FM       frontMatter
	Body     []byte
	Lines    int // lines of the front matter block
	Lesson   string
	Prev     *page
	Next     *page
	RedirOut string // output path of the final target of a redirect stub
	RedirFrg string

	// results of rendering
	Content   template.HTML
	TOC       []tocEntry
	HasWidget bool
	Text      string // plain text excerpt for the search index
}

type tocEntry struct {
	Level int
	ID    string
	Text  string
}

var lessonRe = regexp.MustCompile(`^([a-z])(\d{1,3})-`)

func (p *page) isRedirect() bool { return p.FM.Redirect != "" }

// plainTitle is the title with Markdown code marks removed.
func (p *page) plainTitle() string { return plainText(p.FM.Title) }

func plainText(s string) string {
	r := strings.NewReplacer("`", "", "**", "", "\\_", "_", "\\*", "*")
	return r.Replace(s)
}

// discover finds and parses every page of every configured section.
func (s *site) discover() {
	known := map[string]bool{}
	for i := range s.cfg.Sections {
		sc := &s.cfg.Sections[i]
		known[sc.ID] = true
		sec := &section{cfg: sc, ID: sc.ID, Title: sc.Title, Blurb: sc.Blurb, lessons: map[string]*page{}}
		s.sections = append(s.sections, sec)
		dir := filepath.Join(s.docsDir, sc.ID)
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			sec.Missing = true
			s.msgs.warnf("section %q: directory %s does not exist; the section is left out", sc.ID, dir)
			continue
		}
		var files []string
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			base := d.Name()
			if d.IsDir() {
				if p != dir && (strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_")) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(base, ".md") && !strings.HasPrefix(base, ".") && !strings.HasPrefix(base, "_") {
				files = append(files, p)
			}
			return nil
		})
		sort.Strings(files)
		for _, f := range files {
			rel, _ := filepath.Rel(dir, f)
			s.loadPage(sec, filepath.ToSlash(rel), f)
		}
	}
	// Directories under docs/ that are not sections.
	entries, _ := os.ReadDir(s.docsDir)
	for _, e := range entries {
		if e.IsDir() && !known[e.Name()] && !strings.HasPrefix(e.Name(), ".") && !strings.HasPrefix(e.Name(), "_") {
			s.msgs.warnf("docs/%s is not a section in site.json; its pages are not published", e.Name())
		}
	}
}

func (s *site) loadPage(sec *section, rel, file string) {
	src, err := os.ReadFile(file)
	if err != nil {
		s.msgs.errorf("%v", err)
		return
	}
	yamlText, body, hl, ok := splitFrontMatter(src)
	if !ok {
		s.msgs.errorf("%s:1: no front matter (a page starts with a --- block holding title, description and order)", file)
		return
	}
	fm, err := parseFrontMatter(file, yamlText)
	if err != nil {
		s.msgs.errorf("%v", err)
		return
	}
	p := &page{
		Section: sec,
		Doc:     sec.ID + "/" + rel,
		Rel:     rel,
		File:    file,
		IsIndex: rel == "index.md",
		FM:      fm,
		Body:    body,
		Lines:   hl,
	}
	if err := fm.validate(file, p.IsIndex); err != nil {
		s.msgs.errorf("%v", err)
		return
	}
	p.Out = strings.TrimSuffix(p.Doc, ".md") + ".html"
	p.Twin = strings.TrimSuffix(p.Doc, ".md") + ".md"
	base := path.Base(rel)
	if m := lessonRe.FindStringSubmatch(base); m != nil && !p.IsIndex {
		n, _ := strconv.Atoi(m[2])
		p.Lesson = strings.ToUpper(m[1]) + strconv.Itoa(n)
	}
	if s.byDoc == nil {
		s.byDoc = map[string]*page{}
	}
	s.byDoc[p.Doc] = p
	if p.isRedirect() {
		sec.redirects = append(sec.redirects, p)
		return
	}
	sec.Pages = append(sec.Pages, p)
}

// arrange orders the pages of each section, assigns groups, lesson ids and
// previous/next links.
func (s *site) arrange() {
	for _, sec := range s.sections {
		if sec.Missing {
			continue
		}
		var index *page
		var rest []*page
		for _, p := range sec.Pages {
			if p.IsIndex {
				index = p
			} else {
				rest = append(rest, p)
			}
		}
		if index == nil {
			s.msgs.errorf("%s: section %q has no index.md", filepath.Join(s.docsDir, sec.ID), sec.ID)
		}
		sec.Index = index
		sort.SliceStable(rest, func(i, j int) bool { return rest[i].Rel < rest[j].Rel })

		// Group order: configured groups first, then first appearance in file order.
		var order []string
		seen := map[string]bool{"": true}
		for _, g := range sec.cfg.Groups {
			if !seen[g] {
				order = append(order, g)
				seen[g] = true
			}
		}
		used := map[string]bool{}
		for _, p := range rest {
			g := p.FM.Group
			used[g] = true
			if !seen[g] {
				order = append(order, g)
				seen[g] = true
			}
		}
		for _, g := range sec.cfg.Groups {
			if !used[g] {
				s.msgs.warnf("site.json: section %q lists group %q, which no page uses", sec.ID, g)
			}
		}
		byGroup := map[string][]*page{}
		for _, p := range rest {
			byGroup[p.FM.Group] = append(byGroup[p.FM.Group], p)
		}
		sortGroup := func(name string, pages []*page) {
			sort.SliceStable(pages, func(i, j int) bool {
				if pages[i].FM.Order != pages[j].FM.Order {
					return pages[i].FM.Order < pages[j].FM.Order
				}
				return pages[i].Rel < pages[j].Rel
			})
			for i := 1; i < len(pages); i++ {
				if pages[i].FM.Order == pages[i-1].FM.Order {
					where := "section " + sec.ID
					if name != "" {
						where = fmt.Sprintf("group %q", name)
					}
					s.msgs.errorf("%s: order %d is also used by %s in %s", pages[i].File, pages[i].FM.Order, pages[i-1].Rel, where)
				}
			}
		}
		sec.Groups = nil
		sec.Pages = nil
		sortGroup("", byGroup[""])
		var first []*page
		if index != nil {
			first = append(first, index)
		}
		first = append(first, byGroup[""]...)
		if len(first) > 0 {
			sec.Groups = append(sec.Groups, &group{Name: "", Pages: first})
		}
		for _, g := range order {
			pages := byGroup[g]
			sortGroup(g, pages)
			if len(pages) > 0 {
				sec.Groups = append(sec.Groups, &group{Name: g, Pages: pages})
			}
		}
		for _, g := range sec.Groups {
			sec.Pages = append(sec.Pages, g.Pages...)
		}
		for i, p := range sec.Pages {
			if i > 0 {
				p.Prev = sec.Pages[i-1]
			}
			if i+1 < len(sec.Pages) {
				p.Next = sec.Pages[i+1]
			}
			if p.Lesson != "" {
				if prev, dup := sec.lessons[p.Lesson]; dup {
					s.msgs.errorf("%s: lesson id %s is also used by %s", p.File, p.Lesson, prev.Rel)
				}
				sec.lessons[p.Lesson] = p
			}
		}
	}
	s.resolveRedirects()
}

// resolveRedirects turns the redirect values of stub pages into output paths.
func (s *site) resolveRedirects() {
	for _, sec := range s.sections {
		for _, p := range sec.redirects {
			target, frag, err := s.resolveDocLink(p, p.FM.Redirect)
			if err != nil {
				s.msgs.errorf("%s: redirect: %v", p.File, err)
				continue
			}
			seen := map[*page]bool{p: true}
			for target.isRedirect() {
				if seen[target] {
					s.msgs.errorf("%s: redirect loop through %s", p.File, target.File)
					break
				}
				seen[target] = true
				next, f2, err := s.resolveDocLink(target, target.FM.Redirect)
				if err != nil {
					s.msgs.errorf("%s: redirect: %v", target.File, err)
					break
				}
				if f2 != "" {
					frag = f2
				}
				target = next
			}
			if target.isRedirect() {
				continue
			}
			p.RedirOut = target.Out
			p.RedirFrg = frag
		}
	}
}

// resolveDocLink resolves a link written in page from (relative to its
// directory, ending in .md) to the page it names.
func (s *site) resolveDocLink(from *page, dest string) (*page, string, error) {
	p, frag, _ := strings.Cut(dest, "#")
	if !strings.HasSuffix(p, ".md") {
		return nil, "", fmt.Errorf("%q is not a link to a Markdown page", dest)
	}
	doc := path.Join(path.Dir(from.Doc), p)
	t, ok := s.byDoc[doc]
	if !ok {
		return nil, "", fmt.Errorf("no page %s (linked as %q)", doc, dest)
	}
	return t, frag, nil
}

// finalTarget follows redirect stubs.
func (s *site) finalTarget(p *page) *page {
	for i := 0; i < 8 && p.isRedirect(); i++ {
		next, _, err := s.resolveDocLink(p, p.FM.Redirect)
		if err != nil {
			return p
		}
		p = next
	}
	return p
}

// allPages returns every published page in site order.
func (s *site) allPages() []*page {
	var out []*page
	for _, sec := range s.sections {
		out = append(out, sec.Pages...)
	}
	return out
}

// kicker is the small label above the title: "§ A3 · Track A".
func (p *page) kicker() string {
	short := ""
	if p.FM.Group != "" {
		short = strings.TrimSpace(strings.SplitN(p.FM.Group, ",", 2)[0])
	}
	var parts []string
	switch {
	case p.Lesson != "":
		parts = append(parts, "§ "+p.Lesson)
		if short != "" {
			parts = append(parts, short)
		}
	default:
		parts = append(parts, "§ "+p.Section.Title)
		if short != "" {
			parts = append(parts, short)
		}
	}
	return strings.Join(parts, " · ")
}

// relURL returns the URL of the site-relative path to as seen from the
// site-relative path from.
func relURL(from, to string) string {
	fromDir := path.Dir(from)
	if fromDir == "." {
		fromDir = ""
	}
	fs := splitPath(fromDir)
	ts := splitPath(to)
	i := 0
	for i < len(fs) && i < len(ts)-1 && fs[i] == ts[i] {
		i++
	}
	var b strings.Builder
	for j := i; j < len(fs); j++ {
		b.WriteString("../")
	}
	b.WriteString(strings.Join(ts[i:], "/"))
	return b.String()
}

func splitPath(p string) []string {
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// rootPrefix is the relative path from a page to the site root: "", "../", "../../".
func rootPrefix(out string) string {
	return strings.Repeat("../", strings.Count(out, "/"))
}
