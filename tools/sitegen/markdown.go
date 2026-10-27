package main

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"path"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	grender "github.com/yuin/goldmark/renderer"
	htmlr "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// renderer holds the state of one page while it is rendered: the counters for
// figures and tabs, and the way to report problems with a file and line.
type renderer struct {
	s         *site
	p         *page
	fig       int
	tabs      int
	hasWidget bool
	ids       map[string]int // ids given to generated headings, to keep them unique on the page
}

var nonSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// generatedID returns a unique id for a heading that a container generates:
// the prefix and the slug of the text, with a number after the first use.
func (r *renderer) generatedID(prefix, text string) string {
	if r.ids == nil {
		r.ids = map[string]int{}
	}
	base := strings.Trim(nonSlugRe.ReplaceAllString(strings.ToLower(plainText(text)), "-"), "-")
	if base != prefix && !strings.HasPrefix(base, prefix+"-") {
		base = prefix + "-" + base
	}
	r.ids[base]++
	if n := r.ids[base]; n > 1 {
		return fmt.Sprintf("%s-%d", base, n)
	}
	return base
}

func (r *renderer) errorf(line int, format string, a ...any) {
	r.s.msgs.errorf("%s:%d: %s", r.p.File, line, fmt.Sprintf(format, a...))
}

func (r *renderer) warnf(line int, format string, a ...any) {
	r.s.msgs.warnf("%s:%d: %s", r.p.File, line, fmt.Sprintf(format, a...))
}

// renderPage renders the body of a page and fills Content, TOC and HasWidget.
func (s *site) renderPage(p *page) {
	r := &renderer{s: s, p: p}
	lines := strings.Split(string(p.Body), "\n")
	out, doc, src := r.renderMD(lines, p.Lines+1, false)
	p.Content = template.HTML(out)
	p.HasWidget = r.hasWidget
	if doc != nil {
		p.TOC = collectTOC(doc, src)
	}
	p.Text = excerpt(out, 1200)
}

const slotFmt = "<!--sitegen-slot:%d-->"

// renderMD renders Markdown lines to HTML. lineBase is the file line of
// lines[0]. Container directives and callouts are rendered first, each with a
// recursive call, and spliced into the output of the single Markdown pass over
// the rest of the text. For a top-level render it also returns the parsed
// document and its source so the caller can collect headings.
func (r *renderer) renderMD(lines []string, lineBase int, nested bool) (string, ast.Node, []byte) {
	scanned, dirs := r.scan(lines, lineBase, nested)
	src := []byte(strings.Join(scanned, "\n"))
	md := r.newMarkdown(nested, lineBase)
	ctx := parser.NewContext()
	doc := md.Parser().Parse(text.NewReader(src), parser.WithContext(ctx))
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, src, doc); err != nil {
		r.errorf(lineBase, "render: %v", err)
	}
	out := strings.ReplaceAll(buf.String(), ` data-aria-label="`, ` aria-label="`)
	for i, d := range dirs {
		slot := fmt.Sprintf(slotFmt, i)
		out = strings.Replace(out, slot, r.renderDirective(d), 1)
	}
	// Let wide tables scroll horizontally instead of breaking the layout.
	out = strings.ReplaceAll(out, "<table>", `<div class="table-wrap"><table>`)
	out = strings.ReplaceAll(out, "</table>", "</table></div>")
	out = causeFix(out)
	if nested {
		return out, nil, nil
	}
	return out, doc, src
}

// inline renders a short piece of Markdown without a surrounding paragraph.
func (r *renderer) inline(s string, line int) string {
	out, _, _ := r.renderMD([]string{s}, line, true)
	out = strings.TrimSpace(out)
	if strings.HasPrefix(out, "<p>") && strings.HasSuffix(out, "</p>") && strings.Count(out, "<p>") == 1 {
		out = out[3 : len(out)-4]
	}
	return out
}

func (r *renderer) newMarkdown(nested bool, lineBase int) goldmark.Markdown {
	popts := []parser.Option{
		parser.WithAttribute(),
		parser.WithASTTransformers(util.Prioritized(&linkTransformer{r: r, lineBase: lineBase}, 100)),
	}
	if !nested {
		popts = append(popts, parser.WithAutoHeadingID())
	}
	fr := &fenceRenderer{r: r, lineBase: lineBase, skip: map[ast.Node]bool{}}
	hr := &headingRenderer{r: r, lineBase: lineBase}
	return goldmark.New(
		goldmark.WithExtensions(extension.Table, extension.Strikethrough),
		goldmark.WithParserOptions(popts...),
		goldmark.WithRendererOptions(
			htmlr.WithUnsafe(), // inline SVG and HTML in figures
			grender.WithNodeRenderers(
				util.Prioritized(fr, 100),
				util.Prioritized(hr, 100),
			),
		),
	)
}

// lineAt returns the file line of a byte offset in the source of a render.
func lineAt(src []byte, off, lineBase int) int {
	if off > len(src) {
		off = len(src)
	}
	return lineBase + bytes.Count(src[:off], []byte("\n"))
}

// ---------------------------------------------------------------------------
// Headings

type headingRenderer struct {
	r        *renderer
	lineBase int
}

func (h *headingRenderer) RegisterFuncs(reg grender.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindHeading, h.render)
}

func (h *headingRenderer) render(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Heading)
	if entering {
		if n.Level == 1 {
			off := 0
			if c, ok := n.FirstChild().(*ast.Text); ok {
				off = c.Segment.Start
			}
			h.r.errorf(lineAt(source, off, h.lineBase), "a page body does not start a level-1 heading; the title comes from the front matter")
		}
		_, _ = w.WriteString("<h")
		_ = w.WriteByte("0123456"[n.Level])
		if n.Attributes() != nil {
			htmlr.RenderAttributes(w, node, htmlr.HeadingAttributeFilter)
		}
		_ = w.WriteByte('>')
		// The heading text is the permalink, so the name of the heading is its
		// text alone. A heading that holds a link of its own cannot be wrapped
		// in another; it gets a separate permalink after the text.
		if id := headingID(n); id != "" && !hasLink(n) {
			_, _ = w.WriteString(`<a class="anchor" href="#` + html.EscapeString(id) + `">`)
		}
		return ast.WalkContinue, nil
	}
	if id := headingID(n); id != "" {
		if hasLink(n) {
			_, _ = w.WriteString(`<a class="anchor-side" href="#` + html.EscapeString(id) + `" aria-label="Link to this section">#</a>`)
		} else {
			_, _ = w.WriteString(`</a>`)
		}
	}
	_, _ = w.WriteString("</h")
	_ = w.WriteByte("0123456"[n.Level])
	_, _ = w.WriteString(">\n")
	return ast.WalkContinue, nil
}

// headingID returns the id attribute of a heading, or "".
func headingID(n *ast.Heading) string {
	id, ok := n.AttributeString("id")
	if !ok {
		return ""
	}
	switch v := id.(type) {
	case []byte:
		return string(v)
	case string:
		return v
	}
	return ""
}

// hasLink reports whether an inline link sits inside the heading.
func hasLink(n ast.Node) bool {
	found := false
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if _, ok := c.(*ast.Link); ok && entering {
			found = true
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return found
}

// collectTOC gathers h2 and h3 headings with their generated ids.
func collectTOC(doc ast.Node, src []byte) []tocEntry {
	var toc []tocEntry
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok || h.Level < 2 || h.Level > 3 {
			return ast.WalkContinue, nil
		}
		id, _ := h.AttributeString("id")
		idStr := ""
		switch v := id.(type) {
		case []byte:
			idStr = string(v)
		case string:
			idStr = v
		}
		toc = append(toc, tocEntry{Level: h.Level, ID: idStr, Text: nodeText(h, src)})
		return ast.WalkSkipChildren, nil
	})
	return toc
}

// nodeText returns the plain text of an inline subtree.
func nodeText(n ast.Node, src []byte) string {
	var sb strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			sb.Write(t.Segment.Value(src))
			if t.SoftLineBreak() {
				sb.WriteByte(' ')
			}
		case *ast.String:
			sb.Write(t.Value)
		case *ast.CodeSpan:
			for ch := t.FirstChild(); ch != nil; ch = ch.NextSibling() {
				if tx, ok := ch.(*ast.Text); ok {
					sb.Write(tx.Segment.Value(src))
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return sb.String()
}

// ---------------------------------------------------------------------------
// Code fences

type fenceRenderer struct {
	r        *renderer
	lineBase int
	skip     map[ast.Node]bool // out blocks already merged into their run block
}

func (f *fenceRenderer) RegisterFuncs(reg grender.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, f.render)
}

func fenceInfoText(n *ast.FencedCodeBlock, source []byte) string {
	if n.Info == nil {
		return ""
	}
	return string(n.Info.Segment.Value(source))
}

func fenceCode(n *ast.FencedCodeBlock, source []byte) string {
	var b strings.Builder
	for i := range n.Lines().Len() {
		seg := n.Lines().At(i)
		b.Write(seg.Value(source))
	}
	return b.String()
}

func (f *fenceRenderer) line(n *ast.FencedCodeBlock, source []byte) int {
	off := 0
	switch {
	case n.Info != nil:
		off = n.Info.Segment.Start
	case n.Lines().Len() > 0:
		off = n.Lines().At(0).Start
	}
	return lineAt(source, off, f.lineBase)
}

func (f *fenceRenderer) render(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)
	if f.skip[n] {
		return ast.WalkSkipChildren, nil
	}
	line := f.line(n, source)
	fi, err := parseFenceInfo(fenceInfoText(n, source))
	if err != nil {
		f.r.errorf(line, "code block: %v", err)
		return ast.WalkSkipChildren, nil
	}
	code := fenceCode(n, source)
	switch {
	case fi.Out:
		f.r.errorf(line, "an out block must directly follow the run block it belongs to")
	case fi.Run:
		var out *string
		if next, ok := n.NextSibling().(*ast.FencedCodeBlock); ok {
			if nfi, err := parseFenceInfo(fenceInfoText(next, source)); err == nil && nfi.Out {
				s := fenceCode(next, source)
				out = &s
				f.skip[next] = true
			}
		}
		if fi.Hidden || (fi.Check && !renderCheckBlocks) {
			break
		}
		ran := ""
		if out != nil && f.r.p.FM.Capture {
			ran = f.r.s.cfg.PravoVersion
		}
		_, _ = w.WriteString(renderTerminal(fi, code, out, ran))
		_ = w.WriteByte('\n')
	default:
		if fi.Hidden {
			break
		}
		htmlOut, err := renderCode(fi, code)
		if err != nil {
			f.r.errorf(line, "code block: %v", err)
			break
		}
		_, _ = w.WriteString(htmlOut)
		_ = w.WriteByte('\n')
	}
	return ast.WalkSkipChildren, nil
}

// renderCheckBlocks controls whether `run check` blocks are shown. They exist
// to verify a solution, so the reader does not see them.
const renderCheckBlocks = false

// ---------------------------------------------------------------------------
// Links

type linkTransformer struct {
	r        *renderer
	lineBase int
}

var schemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// lessonIDRe matches the text of a link that is only a lesson id: A6, C1, B11, M4.
var lessonIDRe = regexp.MustCompile(`^[A-Z][0-9]{1,3}$`)

func (t *linkTransformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	src := reader.Source()
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		l, ok := n.(*ast.Link)
		if !ok {
			return ast.WalkContinue, nil
		}
		dest := string(l.Destination)
		if schemeRe.MatchString(dest) || strings.HasPrefix(dest, "//") || strings.HasPrefix(dest, "#") || strings.HasPrefix(dest, "/") {
			return ast.WalkContinue, nil
		}
		pth, frag, _ := strings.Cut(dest, "#")
		if strings.HasSuffix(pth, "/") {
			// A link to a directory is a link to its index page, which also
			// works from the file system.
			if _, ok := t.r.s.byDoc[path.Join(path.Dir(t.r.p.Doc), pth, "index.md")]; ok {
				pth += "index.md"
			}
		}
		if !strings.HasSuffix(pth, ".md") {
			return ast.WalkContinue, nil
		}
		target, _, err := t.r.s.resolveDocLink(t.r.p, pth)
		if err != nil {
			t.r.errorf(t.nodeLine(n, src), "link: %v", err)
			return ast.WalkContinue, nil
		}
		final := t.r.s.finalTarget(target)
		href := relURL(t.r.p.Out, final.Out)
		if frag != "" {
			href += "#" + frag
		}
		l.Destination = []byte(href)
		// A link that is only a lesson id ("A6") says nothing in a list of links.
		if final.Lesson != "" && lessonIDRe.MatchString(strings.TrimSpace(nodeText(l, src))) {
			// goldmark writes only data-* attributes on a link; renderMD renames this one
			l.SetAttributeString("data-aria-label", []byte(final.Lesson+", "+final.plainTitle()))
		}
		if frag != "" && target.Doc == t.r.s.cfg.Glossary {
			l.SetAttributeString("class", []byte("term-link"))
			l.SetAttributeString("data-term", []byte(frag))
			if e, ok := t.r.s.gloss[frag]; ok && e.Def != "" {
				l.SetAttributeString("data-def", []byte(e.Def))
			}
		}
		return ast.WalkContinue, nil
	})
}

func (t *linkTransformer) nodeLine(n ast.Node, src []byte) int {
	for c := n; c != nil; c = c.Parent() {
		if c.Type() == ast.TypeBlock && c.Lines() != nil && c.Lines().Len() > 0 {
			return lineAt(src, c.Lines().At(0).Start, t.lineBase)
		}
	}
	return t.lineBase
}
