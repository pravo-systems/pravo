package main

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// glossEntry is one term of the glossary page: a heading and the first
// paragraph below it.
type glossEntry struct {
	ID   string
	Term string
	Def  string
}

// loadGlossary reads the glossary page, if there is one, so that links to
// its entries can carry the definition for a hover card.
func (s *site) loadGlossary() {
	s.gloss = map[string]glossEntry{}
	p, ok := s.byDoc[s.cfg.Glossary]
	if !ok || p.isRedirect() {
		return
	}
	// A throwaway message list: the real render reports the same problems.
	tmp := *s
	tmp.msgs = &messages{}
	r := &renderer{s: &tmp, p: p}
	lines, _ := r.scan(strings.Split(string(p.Body), "\n"), p.Lines+1, false)
	src := []byte(strings.Join(lines, "\n"))
	md := goldmark.New(goldmark.WithParserOptions(parser.WithAutoHeadingID(), parser.WithAttribute()))
	doc := md.Parser().Parse(text.NewReader(src))
	var cur *glossEntry
	flush := func() {
		if cur != nil && cur.ID != "" {
			s.gloss[cur.ID] = *cur
		}
	}
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		switch t := n.(type) {
		case *ast.Heading:
			if t.Level < 2 {
				continue
			}
			flush()
			id, _ := t.AttributeString("id")
			var ids string
			switch v := id.(type) {
			case []byte:
				ids = string(v)
			case string:
				ids = v
			}
			cur = &glossEntry{ID: ids, Term: nodeText(t, src)}
		case *ast.Paragraph:
			if cur != nil && cur.Def == "" {
				cur.Def = shorten(nodeText(t, src), 280)
			}
		}
	}
	flush()
}

// shorten cuts s to at most n bytes at the end of a sentence or word.
func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndex(cut, ". "); i > n/3 {
		return cut[:i+1]
	}
	if i := strings.LastIndexByte(cut, ' '); i > 0 {
		cut = cut[:i]
	}
	return cut + "…"
}
