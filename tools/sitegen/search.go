package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	xhtml "golang.org/x/net/html"
)

// skipTags are elements whose text is not prose.
var skipTags = map[string]bool{"pre": true, "script": true, "style": true, "button": true, "noscript": true, "svg": true}

// skipClasses mark elements kept out of the search excerpt: code, terminals,
// and the parts a reader has not yet earned (answers, hints, solutions).
var skipClasses = []string{"code", "term", "solution", "hint", "predict-reveal", "check", "anchor-side", "tab-list"}

// inlineTags do not break the text around them.
var inlineTags = map[string]bool{"a": true, "abbr": true, "b": true, "code": true, "em": true, "i": true, "kbd": true, "mark": true, "small": true, "span": true, "strong": true, "sub": true, "sup": true, "u": true, "s": true, "q": true, "cite": true, "del": true}

var voidTags = map[string]bool{"br": true, "hr": true, "img": true, "input": true, "meta": true, "link": true, "wbr": true, "source": true}

// excerpt returns the prose of an HTML fragment as plain text, at most max
// bytes long and cut at a word boundary.
func excerpt(fragment string, max int) string {
	z := xhtml.NewTokenizer(strings.NewReader(fragment))
	var b strings.Builder
	var stack []bool // per open element: is it skipped
	skipping := func() bool {
		for _, s := range stack {
			if s {
				return true
			}
		}
		return false
	}
	var names []string
loop:
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			break loop
		case xhtml.TextToken:
			if !skipping() {
				b.WriteString(string(z.Text()))
			}
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			tok := z.Token()
			if !inlineTags[tok.Data] {
				b.WriteByte(' ')
			}
			if voidTags[tok.Data] || tok.Type == xhtml.SelfClosingTagToken {
				continue
			}
			skip := skipTags[tok.Data]
			for _, a := range tok.Attr {
				if a.Key == "class" {
					for _, c := range strings.Fields(a.Val) {
						for _, sc := range skipClasses {
							if c == sc {
								skip = true
							}
						}
					}
				}
			}
			stack = append(stack, skip)
			names = append(names, tok.Data)
		case xhtml.EndTagToken:
			tok := z.Token()
			if !inlineTags[tok.Data] {
				b.WriteByte(' ')
			}
			for i := len(names) - 1; i >= 0; i-- {
				if names[i] == tok.Data {
					stack = stack[:i]
					names = names[:i]
					break
				}
			}
		}
		if b.Len() > max*3 {
			break
		}
	}
	text := strings.Join(strings.Fields(b.String()), " ")
	if len(text) <= max {
		return text
	}
	cut := text[:max]
	if i := strings.LastIndexByte(cut, ' '); i > max/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

type searchHeading struct {
	Text string `json:"text"`
	ID   string `json:"id"`
}

type searchEntry struct {
	Title       string          `json:"title"`
	Section     string          `json:"section"`
	Group       string          `json:"group,omitempty"`
	Lesson      string          `json:"lesson,omitempty"`
	Minutes     int             `json:"minutes,omitempty"`
	URL         string          `json:"url"`
	Description string          `json:"description"`
	Headings    []searchHeading `json:"headings"`
	Body        string          `json:"body"`
	Keywords    string          `json:"keywords,omitempty"` // front matter keywords: words the page does not say
	Terms       string          `json:"terms"`              // every word of the page, code and tables included, once
	Suggest     int             `json:"suggest,omitempty"`  // position in search_suggest, from 1
}

var (
	termRe      = regexp.MustCompile(`[a-z0-9]+`)
	markupRe    = regexp.MustCompile(`(?s)<svg.*?</svg>|<script.*?</script>|<[^>]+>`)
	hexNoiseRe  = regexp.MustCompile(`^[0-9a-f]{8,}$`)
	digitsOnlyR = regexp.MustCompile(`^[0-9]{5,}$`)
)

// searchTerms lists the words of the sources once each, in alphabetical order,
// separated by spaces. The words are lower case runs of letters and digits, so
// for_each, for-each and aws_s3_bucket are found by their parts. Hashes,
// long numbers and drawing data are left out.
func searchTerms(sources ...string) string {
	seen := map[string]bool{}
	var words []string
	for _, src := range sources {
		src = markupRe.ReplaceAllString(src, " ")
		for _, w := range termRe.FindAllString(strings.ToLower(src), -1) {
			if len(w) < 2 || len(w) > 32 || seen[w] || hexNoiseRe.MatchString(w) || digitsOnlyR.MatchString(w) {
				continue
			}
			seen[w] = true
			words = append(words, w)
		}
	}
	sort.Strings(words)
	return strings.Join(words, " ")
}

// searchEntries builds the index, one entry per published page.
func (s *site) searchEntries() []searchEntry {
	var out []searchEntry
	for _, p := range s.allPages() {
		e := searchEntry{
			Title:       p.plainTitle(),
			Section:     p.Section.Title,
			Group:       p.FM.Group,
			Lesson:      p.Lesson,
			Minutes:     p.FM.Minutes,
			URL:         p.Out,
			Description: plainText(p.FM.Description),
			Headings:    []searchHeading{},
			Body:        p.Text,
			Keywords:    p.FM.Keywords,
			Terms:       searchTerms(string(p.Body), p.FM.Learn, p.FM.DoneWhen),
		}
		for i, doc := range s.cfg.Suggest {
			if doc == p.Doc {
				e.Suggest = i + 1
			}
		}
		for _, t := range p.TOC {
			if t.ID != "" {
				e.Headings = append(e.Headings, searchHeading{Text: t.Text, ID: t.ID})
			}
		}
		out = append(out, e)
	}
	return out
}

// writeSearchIndex writes search-index.json and search-index.js. The script
// form assigns window.PRAVO_SEARCH so that search also works from file:// URLs,
// where fetch() cannot read the JSON.
func (s *site) writeSearchIndex(of *outFiles) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s.searchEntries()); err != nil {
		return err
	}
	data := bytes.TrimSpace(buf.Bytes())
	if err := of.write("search-index.json", append(data, '\n')); err != nil {
		return err
	}
	js := append([]byte("window.PRAVO_SEARCH="), data...)
	js = append(js, ';', '\n')
	// A line separator inside a string would end the statement early in older engines.
	for _, r := range []rune{0x2028, 0x2029} {
		js = bytes.ReplaceAll(js, []byte(string(r)), []byte(fmt.Sprintf("\\u%04x", r)))
	}
	return of.write("search-index.js", js)
}

type sitemapURL struct {
	Loc string `xml:"loc"`
}

type sitemapSet struct {
	XMLName xml.Name     `xml:"urlset"`
	Xmlns   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

// writeSitemap writes sitemap.xml and robots.txt. A sitemap lists absolute
// URLs, so neither is written without a base_url. A robots.txt in the site
// directory is copied instead of the generated one.
func (s *site) writeSitemap(of *outFiles) error {
	if s.cfg.BaseURL == "" {
		return nil
	}
	loc := func(rel string) string {
		return s.cfg.BaseURL + "/" + strings.TrimSuffix(rel, "index.html")
	}
	if _, err := os.Stat(filepath.Join(s.siteDir, "robots.txt")); err != nil {
		robots := "User-agent: *\nAllow: /\n\nSitemap: " + s.cfg.BaseURL + "/sitemap.xml\n"
		if err := of.write("robots.txt", []byte(robots)); err != nil {
			return err
		}
	}
	set := sitemapSet{Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	set.URLs = append(set.URLs, sitemapURL{loc("index.html")}, sitemapURL{loc("contents.html")})
	for _, p := range s.allPages() {
		set.URLs = append(set.URLs, sitemapURL{loc(p.Out)})
	}
	data, err := xml.MarshalIndent(set, "", "  ")
	if err != nil {
		return err
	}
	return of.write("sitemap.xml", append([]byte(xml.Header), append(data, '\n')...))
}

// writeLLMS writes llms.txt (an index for language models) and llms-full.txt
// (every page's Markdown in one file).
func (s *site) writeLLMS(of *outFiles) error {
	link := func(rel string) string {
		if s.cfg.BaseURL != "" {
			return s.cfg.BaseURL + "/" + rel
		}
		return rel
	}
	var idx, full bytes.Buffer
	fmt.Fprintf(&idx, "# %s\n\n", s.cfg.Name)
	if s.cfg.Description != "" {
		fmt.Fprintf(&idx, "> %s\n", s.cfg.Description)
	}
	fmt.Fprintf(&full, "# %s documentation\n", s.cfg.Name)
	if s.cfg.Description != "" {
		fmt.Fprintf(&full, "\n%s\n", s.cfg.Description)
	}
	for _, sec := range s.sections {
		if sec.Missing || len(sec.Pages) == 0 {
			continue
		}
		fmt.Fprintf(&idx, "\n## %s\n\n", sec.Title)
		if sec.Blurb != "" {
			fmt.Fprintf(&idx, "%s\n\n", sec.Blurb)
		}
		for _, p := range sec.Pages {
			fmt.Fprintf(&idx, "- [%s](%s): %s\n", p.plainTitle(), link(p.Twin), plainText(p.FM.Description))
			fmt.Fprintf(&full, "\n\n---\n\n# %s: %s\n\nURL: %s\n\n%s\n", sec.Title, p.plainTitle(), link(p.Out), strings.TrimSpace(string(twinBody(p))))
		}
	}
	fmt.Fprintf(&idx, "\n## Optional\n\n- [llms-full.txt](%s): all pages in one file\n- [Search index](%s): titles, headings and excerpts as JSON\n", link("llms-full.txt"), link("search-index.json"))
	if err := of.write("llms.txt", idx.Bytes()); err != nil {
		return err
	}
	return of.write("llms-full.txt", full.Bytes())
}

// twinBody is the page source for llms-full.txt: the description and the body.
func twinBody(p *page) []byte {
	var b bytes.Buffer
	if p.FM.Description != "" {
		b.WriteString(p.FM.Description + "\n\n")
	}
	if meta := twinMeta(p); meta != "" {
		b.WriteString(meta + "\n\n")
	}
	b.Write(bytes.TrimSpace(p.Body))
	return b.Bytes()
}
