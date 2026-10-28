package main

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// out is a set of files to write below the output directory.
type outFiles struct {
	dir string
	// pretty writes the links to an index.html of the pages as links to the
	// directory: "../" for "../index.html", "learn/" for "learn/index.html".
	pretty bool
}

// indexLinkRe matches a relative link to an index.html, with its fragment or query.
var indexLinkRe = regexp.MustCompile(`href="((?:[^"#?:]*/)?)index\.html([#?][^"]*)?"`)

// prettyIndexLinks turns the relative links to an index.html into links to its
// directory. A link to the index.html of the page itself becomes "./".
func prettyIndexLinks(html []byte) []byte {
	return indexLinkRe.ReplaceAllFunc(html, func(m []byte) []byte {
		sub := indexLinkRe.FindSubmatch(m)
		dir := string(sub[1])
		if dir == "" {
			dir = "./"
		}
		return []byte(`href="` + dir + string(sub[2]) + `"`)
	})
}

func (o *outFiles) write(rel string, data []byte) error {
	if o.pretty && strings.HasSuffix(rel, ".html") {
		data = prettyIndexLinks(data)
	}
	dst := filepath.Join(o.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// safeOutDir refuses an output directory that would delete sources.
func safeOutDir(out string, protect ...string) error {
	abs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if abs == filepath.Dir(abs) {
		return fmt.Errorf("refusing to use %s as the output directory", abs)
	}
	for _, p := range protect {
		pa, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(abs, pa)
		if err == nil && (rel == "." || !strings.HasPrefix(rel, "..")) {
			return fmt.Errorf("the output directory %s contains %s; choose another with -out", abs, pa)
		}
	}
	return nil
}

// --- views ------------------------------------------------------------------

func (s *site) headerFor(fromOut string, current *section) headerView {
	h := headerView{
		BrandHref:  relURL(fromOut, "index.html"),
		LogoSrc:    relURL(fromOut, "assets/logo.svg"),
		BrandName:  s.cfg.Name,
		StartHref:  relURL(fromOut, s.cfg.StartHref),
		StartLabel: s.cfg.StartLabel,
	}
	for _, sec := range s.sections {
		if sec.Missing || sec.Index == nil || !sec.cfg.inNav() {
			continue
		}
		h.Nav = append(h.Nav, navItem{Title: sec.Title, Href: relURL(fromOut, sec.Index.Out), Current: sec == current, IsPage: sec.Index.Out == fromOut})
	}
	if s.cfg.Repo != "" {
		h.RepoHref = s.cfg.Repo
		h.RepoLabel = "Repository"
		if u, err := url.Parse(s.cfg.Repo); err == nil && strings.EqualFold(strings.TrimPrefix(u.Host, "www."), "github.com") {
			h.RepoLabel = "GitHub"
		}
	}
	return h
}

func (s *site) footerFor(fromOut string) footerView {
	f := footerView{Name: s.cfg.Name, Applies: s.applies, Note: s.cfg.FooterNote}
	for _, sec := range s.sections {
		if sec.Missing || sec.Index == nil || !sec.cfg.inNav() {
			continue
		}
		f.Sections = append(f.Sections, navItem{Title: sec.Title, Href: relURL(fromOut, sec.Index.Out)})
	}
	if s.cfg.Repo != "" {
		f.Project = append(f.Project, navItem{Title: "Repository", Href: s.cfg.Repo, External: true})
	}
	f.Project = append(f.Project,
		navItem{Title: "All pages", Href: relURL(fromOut, "contents.html")},
		navItem{Title: "llms.txt", Href: relURL(fromOut, "llms.txt")},
	)
	return f
}

// siteFileExists reports whether a file of the site directory exists.
func (s *site) siteFileExists(rel string) bool {
	st, err := os.Stat(filepath.Join(s.siteDir, filepath.FromSlash(rel)))
	return err == nil && !st.IsDir()
}

func (s *site) headFor(out, title, ogTitle, desc, ogType, twin string, widgets bool) headView {
	root := rootPrefix(out)
	h := headView{
		Title:       title,
		OGTitle:     ogTitle,
		OGType:      ogType,
		Description: desc,
		SiteName:    s.cfg.Name,
		Root:        root,
		TwinHref:    twin,
		Widgets:     widgets,
	}
	if s.cfg.BaseURL != "" {
		h.Canonical = s.cfg.BaseURL + "/" + strings.TrimSuffix(out, "index.html")
		if s.siteFileExists("assets/og.png") {
			h.OGImage = s.cfg.BaseURL + "/assets/og.png"
		}
	}
	h.ICO = s.siteFileExists("favicon.ico")
	h.TouchIcon = s.siteFileExists("assets/apple-touch-icon.png")
	for _, f := range s.cfg.Preload {
		h.Preload = append(h.Preload, root+strings.TrimPrefix(f, "/"))
	}
	return h
}

var lessonRefRe = regexp.MustCompile(`\b([A-Z])(\d{1,3})\b`)

// metaFor builds the "Time, Needs, You will learn, Done when" list of a page.
func (s *site) metaFor(p *page, r *renderer) []metaItem {
	var m []metaItem
	fm := p.FM
	if fm.Minutes > 0 {
		m = append(m, metaItem{Label: "Time", Value: template.HTML(fmt.Sprintf("%d min", fm.Minutes))})
	}
	if fm.Level != "" {
		m = append(m, metaItem{Label: "Level", Value: template.HTML(template.HTMLEscapeString(fm.Level))})
	}
	if fm.Needs != "" {
		needs := lessonRefRe.ReplaceAllStringFunc(fm.Needs, func(id string) string {
			t := s.lessonPage(p, id)
			if t == nil || t == p {
				return id
			}
			return fmt.Sprintf("[%s](%s)", id, relURL(p.Doc, t.Doc))
		})
		m = append(m, metaItem{Label: "Needs", Value: template.HTML(r.inline(needs, p.Lines))})
	}
	if fm.Learn != "" {
		m = append(m, metaItem{Label: "You will learn", Value: template.HTML(r.inline(fm.Learn, p.Lines))})
	}
	if fm.DoneWhen != "" {
		m = append(m, metaItem{Label: "Done when", Class: "meta-done", Value: template.HTML(r.inline(fm.DoneWhen, p.Lines))})
	}
	return m
}

func (s *site) docViewFor(p *page) docView {
	sec := p.Section
	r := &renderer{s: s, p: p}
	title := p.plainTitle()
	pageTitle := title + " · " + sec.Title + " · " + s.cfg.Name
	if p.IsIndex {
		pageTitle = title + " · " + s.cfg.Name
	}
	applies := s.applies
	if p.FM.AppliesTo != "" {
		applies = p.FM.AppliesTo
	}
	v := docView{
		Head:      s.headFor(p.Out, pageTitle, title, plainText(p.FM.Description), "article", path.Base(p.Twin), p.HasWidget),
		Header:    s.headerFor(p.Out, sec),
		Footer:    s.footerFor(p.Out),
		Section:   sec.ID,
		Root:      rootPrefix(p.Out),
		Kicker:    p.kicker(),
		TitleHTML: template.HTML(r.inline(p.FM.Title, p.Lines)),
		Lede:      template.HTML(r.inline(p.FM.Description, p.Lines)),
		Meta:      s.metaFor(p, r),
		TwinHref:  path.Base(p.Twin),
		Content:   p.Content,
		MarkDone:  sec.cfg.Progress && !p.IsIndex,
		PagePath:  p.Out,
		Applies:   applies,
	}
	for _, g := range sec.Groups {
		sg := sideGroup{Name: g.Name}
		for _, q := range g.Pages {
			sg.Pages = append(sg.Pages, sideEntry{
				Title:   q.plainTitle(),
				Href:    relURL(p.Out, q.Out),
				Page:    q.Out,
				Lesson:  q.Lesson,
				Current: q == p,
			})
		}
		v.Sidebar = append(v.Sidebar, sg)
	}
	for _, pl := range sec.cfg.Pinned {
		t, ok := s.byDoc[pl.Href]
		if !ok {
			s.msgs.warnf("site.json: pinned link %q in section %q names no page", pl.Href, sec.ID)
			continue
		}
		v.Pinned = append(v.Pinned, navItem{Title: pl.Title, Href: relURL(p.Out, s.finalTarget(t).Out)})
	}
	if p.IsIndex {
		v.Breadcrumb = []crumb{{Title: sec.Title, Current: true}}
	} else {
		v.Breadcrumb = []crumb{{Title: sec.Title, Href: relURL(p.Out, sec.Index.Out)}}
		if p.FM.Group != "" {
			v.Breadcrumb = append(v.Breadcrumb, crumb{Title: p.FM.Group})
		}
		v.Breadcrumb = append(v.Breadcrumb, crumb{Title: title, Current: true})
	}
	pager := func(q *page) *pagerLink {
		l := &pagerLink{Title: q.plainTitle(), Href: relURL(p.Out, q.Out), Description: plainText(q.FM.Description)}
		if q.Lesson != "" {
			l.Kicker = "§ " + q.Lesson
		}
		return l
	}
	if p.Prev != nil {
		v.Prev = pager(p.Prev)
	}
	if p.Next != nil {
		v.Next = pager(p.Next)
	}
	if p.FM.Gate != "" {
		v.Runbook = template.HTML(r.runbookHTML(p.FM.Group, p))
	}
	if !p.FM.NoTOC {
		for _, t := range p.TOC {
			v.TOC = append(v.TOC, tocItem(t))
		}
	}
	return v
}

// twinText is the Markdown twin of a page: its title, description and source.
func twinText(p *page) []byte {
	var b bytes.Buffer
	b.WriteString("# " + p.plainTitle() + "\n\n")
	if p.FM.Description != "" {
		b.WriteString(p.FM.Description + "\n\n")
	}
	if meta := twinMeta(p); meta != "" {
		b.WriteString(meta + "\n\n")
	}
	b.Write(stripLayout(demoDirectiveRe.ReplaceAll(bytes.TrimSpace(p.Body), []byte("> An interactive demo appears here on the web page."))))
	b.WriteString("\n")
	return b.Bytes()
}

// twinMeta is the lesson header of a page as plain lines: level, time and
// needs, what you will learn, and when you are done. The front matter is not
// part of the twin, so these lines carry what the header contract shows.
func twinMeta(p *page) string {
	fm := p.FM
	var facts []string
	if fm.Level != "" {
		facts = append(facts, "Level: "+fm.Level)
	}
	if fm.Minutes > 0 {
		facts = append(facts, fmt.Sprintf("Time: %d minutes", fm.Minutes))
	}
	if fm.Needs != "" {
		facts = append(facts, "Needs: "+fm.Needs)
	}
	var lines []string
	if len(facts) > 0 {
		lines = append(lines, strings.Join(facts, " · "))
	}
	if fm.Learn != "" {
		lines = append(lines, "You will learn: "+fm.Learn)
	}
	if fm.DoneWhen != "" {
		lines = append(lines, "Done when: "+fm.DoneWhen)
	}
	return strings.Join(lines, "\n\n")
}

// --- writing ----------------------------------------------------------------

func (s *site) writeAll(of *outFiles) error {
	for _, p := range s.allPages() {
		var buf bytes.Buffer
		if err := shell.ExecuteTemplate(&buf, "doc", s.docViewFor(p)); err != nil {
			return fmt.Errorf("%s: %w", p.File, err)
		}
		if err := of.write(p.Out, buf.Bytes()); err != nil {
			return err
		}
		if err := of.write(p.Twin, twinText(p)); err != nil {
			return err
		}
	}
	for _, sec := range s.sections {
		for _, p := range sec.redirects {
			if p.RedirOut == "" {
				continue
			}
			href := relURL(p.Out, p.RedirOut)
			if p.RedirFrg != "" {
				href += "#" + p.RedirFrg
			}
			canonical := href
			if s.cfg.BaseURL != "" {
				canonical = s.cfg.BaseURL + "/" + p.RedirOut
			}
			target := s.finalTarget(p)
			var buf bytes.Buffer
			err := shell.ExecuteTemplate(&buf, "redirect", redirectView{Root: rootPrefix(p.Out), SiteName: s.cfg.Name, Canonical: canonical, Href: href, Title: target.plainTitle()})
			if err != nil {
				return err
			}
			if err := of.write(p.Out, buf.Bytes()); err != nil {
				return err
			}
		}
	}
	if err := s.writeLanding(of); err != nil {
		return err
	}
	if err := s.writeContents(of); err != nil {
		return err
	}
	if err := s.writeNotFound(of); err != nil {
		return err
	}
	if err := s.writeSearchIndex(of); err != nil {
		return err
	}
	if err := s.writeSitemap(of); err != nil {
		return err
	}
	return s.writeLLMS(of)
}

// contentsDescription is the meta description of the contents page: every
// page of the documentation by section, naming the sections.
func (s *site) contentsDescription() string {
	var names []string
	for _, sec := range s.sections {
		if !sec.Missing && len(sec.Pages) > 0 {
			names = append(names, sec.Title)
		}
	}
	list := strings.Join(names, ", ")
	if n := len(names); n > 1 {
		list = strings.Join(names[:n-1], ", ") + " and " + names[n-1]
	}
	return fmt.Sprintf("Every page of the %s documentation by section: %s.", s.cfg.Name, list)
}

func (s *site) writeContents(of *outFiles) error {
	out := "contents.html"
	v := contentsView{
		Head:   s.headFor(out, "All pages · "+s.cfg.Name, "All pages", s.contentsDescription(), "website", "", false),
		Header: s.headerFor(out, nil),
		Footer: s.footerFor(out),
		Root:   "",
	}
	for _, sec := range s.sections {
		if sec.Missing || len(sec.Pages) == 0 {
			continue
		}
		cs := contentsSection{ID: sec.ID, Title: sec.Title, Blurb: sec.Blurb}
		for _, g := range sec.Groups {
			cg := contentsGroup{Name: g.Name}
			for _, p := range g.Pages {
				cg.Pages = append(cg.Pages, contentsPage{Title: p.plainTitle(), Href: p.Out, Description: plainText(p.FM.Description)})
			}
			cs.Groups = append(cs.Groups, cg)
		}
		v.Sections = append(v.Sections, cs)
	}
	var buf bytes.Buffer
	if err := shell.ExecuteTemplate(&buf, "contents", v); err != nil {
		return err
	}
	return of.write(out, buf.Bytes())
}

// copyTree copies the files below src to dst, calling keep to filter.
func copyTree(src, dst string, keep func(rel string) bool) error {
	return copyTreeWith(src, dst, keep, nil)
}

// copyTreeWith is copyTree with a transform that may replace the bytes of a
// file; rel is the slash path below src.
func copyTreeWith(src, dst string, keep func(rel string) bool, transform func(rel string, data []byte) []byte) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if keep != nil && !keep(filepath.ToSlash(rel)) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if transform != nil {
			data = transform(filepath.ToSlash(rel), data)
		}
		target := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

var imageExts = map[string]bool{".svg": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".avif": true}

// sharedImages is the directory below docs/ that holds images used by pages of
// several sections. It is not a section: its images are published at /img.
const sharedImages = "img"

// isPublishedImage reports whether the file below a docs directory is an image
// that is copied to the output: it has an image extension and its name does not
// start with a dot or an underscore.
func isPublishedImage(rel string) bool {
	base := path.Base(rel)
	return imageExts[strings.ToLower(path.Ext(rel))] && !strings.HasPrefix(base, ".") && !strings.HasPrefix(base, "_")
}

// copyStatic copies assets, root files, the images next to the pages and the
// shared images of docs/img.
func (s *site) copyStatic(of *outFiles) error {
	assets := filepath.Join(s.siteDir, "assets")
	if st, err := os.Stat(assets); err == nil && st.IsDir() {
		if err := copyTreeWith(assets, filepath.Join(of.dir, "assets"), func(rel string) bool {
			return !strings.HasPrefix(path.Base(rel), ".")
		}, s.assetData); err != nil {
			return fmt.Errorf("copy assets: %w", err)
		}
	} else {
		s.msgs.warnf("%s does not exist; the pages link to assets/style.css and assets/site.js", assets)
	}
	for _, name := range []string{"robots.txt", "CNAME", ".nojekyll", "favicon.ico", "404.html"} {
		data, err := os.ReadFile(filepath.Join(s.siteDir, name))
		if err == nil {
			if err := of.write(name, data); err != nil {
				return err
			}
		}
	}
	for _, sec := range s.sections {
		if sec.Missing {
			continue
		}
		dir := filepath.Join(s.docsDir, sec.ID)
		if err := copyTree(dir, filepath.Join(of.dir, sec.ID), isPublishedImage); err != nil {
			return err
		}
	}
	shared := filepath.Join(s.docsDir, sharedImages)
	if st, err := os.Stat(shared); err == nil && st.IsDir() {
		if err := copyTree(shared, filepath.Join(of.dir, sharedImages), isPublishedImage); err != nil {
			return err
		}
	}
	return nil
}

// lessonPage finds the page with the given lesson id, looking in the section
// of from first and then in the others.
func (s *site) lessonPage(from *page, id string) *page {
	if t, ok := from.Section.lessons[id]; ok {
		return t
	}
	for _, sec := range s.sections {
		if t, ok := sec.lessons[id]; ok {
			return t
		}
	}
	return nil
}

// sitePath is the path part of base_url with slashes at both ends: "/" for a
// site at the root of its host, "/pravo/" for one below a path.
func (s *site) sitePath() string {
	if s.cfg.BaseURL == "" {
		return "/"
	}
	u, err := url.Parse(s.cfg.BaseURL)
	if err != nil || strings.Trim(u.Path, "/") == "" {
		return "/"
	}
	return "/" + strings.Trim(u.Path, "/") + "/"
}

var relAttrRe = regexp.MustCompile(`(href|src)="([^"#/:][^":]*)"`)

// writeNotFound writes 404.html. A host serves it for any address, at any
// depth, so every link and asset in it is a path from the root of the site.
func (s *site) writeNotFound(of *outFiles) error {
	out := "404.html"
	prefix := s.sitePath()
	v := notFoundView{
		Head:   s.headFor(out, "Page not found · "+s.cfg.Name, "Page not found", "The address does not exist or has moved.", "website", "", false),
		Header: s.headerFor(out, nil),
		Footer: s.footerFor(out),
		Root:   prefix,
	}
	v.Head.NoIndex = true
	v.Head.Root = prefix
	v.Head.Canonical = ""
	for _, sec := range s.sections {
		if !sec.Missing && sec.Index != nil {
			v.Links = append(v.Links, contentsPage{Title: sec.Title, Href: sec.Index.Out, Description: sec.Blurb})
		}
	}
	v.Links = append(v.Links, contentsPage{Title: "All pages", Href: "contents.html", Description: "Every page of the documentation, by section."})
	var buf bytes.Buffer
	if err := shell.ExecuteTemplate(&buf, "notfound", v); err != nil {
		return err
	}
	page := relAttrRe.ReplaceAllString(buf.String(), `$1="`+prefix+`$2"`)
	return of.write(out, []byte(page))
}
