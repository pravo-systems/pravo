package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	partialNameRe = regexp.MustCompile(`^(\d+)-([a-z][a-z0-9-]*)\.html$`)
	bandClassRe   = regexp.MustCompile(`class="([^"]*\s)?band(\s[^"]*)?"`)
	wrapRe        = regexp.MustCompile(`^<section[^>]*>\s*<div class="wrap(\s[^"]*)?"`)
	dataTokenRe   = regexp.MustCompile(`\{\{(data|json) ([^{}]*)\}\}`)
)

// landingPartials reads site/landing/NN-name.html in file name order.
func (s *site) landingPartials() (names []string, texts []string, err error) {
	dir := filepath.Join(s.siteDir, "landing")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") || strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return nil, nil, err
		}
		names = append(names, f)
		texts = append(texts, string(data))
	}
	return names, texts, nil
}

// substituteTokens replaces the site tokens a landing partial may use, and
// the data tokens that read the files of site/data.
func (s *site) substituteTokens(file, text string) string {
	r := strings.NewReplacer(
		"{{applies}}", html.EscapeString(s.applies),
		"{{pravo_version}}", html.EscapeString(s.cfg.PravoVersion),
		"{{cue_version}}", html.EscapeString(s.cfg.CUEVersion),
		"{{start}}", s.cfg.StartHref,
		"{{start_label}}", html.EscapeString(s.cfg.StartLabel),
		"{{repo}}", html.EscapeString(s.cfg.Repo),
		"{{name}}", html.EscapeString(s.cfg.Name),
	)
	text = r.Replace(text)
	for _, tok := range []string{"go_version", "start_minutes"} {
		if !strings.Contains(text, "{{"+tok+"}}") {
			continue
		}
		v, err := s.derivedValue(tok)
		if err != nil {
			s.msgs.errorf("site/%s: {{%s}}: %v", file, tok, err)
		}
		text = strings.ReplaceAll(text, "{{"+tok+"}}", html.EscapeString(v))
	}
	return dataTokenRe.ReplaceAllStringFunc(text, func(tok string) string {
		m := dataTokenRe.FindStringSubmatch(tok)
		v, err := s.dataValue(strings.TrimSpace(m[2]))
		if err != nil {
			s.msgs.errorf("site/%s: %s: %v", file, tok, err)
			return ""
		}
		if m[1] == "json" {
			return jsonForScript(v)
		}
		switch x := v.(type) {
		case string:
			return html.EscapeString(x)
		case float64, bool, nil:
			b, _ := json.Marshal(x)
			return html.EscapeString(string(b))
		}
		s.msgs.errorf("site/%s: %s: the value is an object or an array; use {{json ...}} to embed it", file, tok)
		return ""
	})
}

var goVersionRe = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+)`)

// derivedValue returns a value that a landing partial may not type by hand:
// {{go_version}} is the Go release of go.mod, next to the site directory, and
// {{start_minutes}} is the minutes value of the page that start_href names.
func (s *site) derivedValue(name string) (string, error) {
	switch name {
	case "go_version":
		mod := filepath.Join(s.siteDir, "..", "go.mod")
		b, err := os.ReadFile(mod)
		if err != nil {
			return "", fmt.Errorf("cannot read %s: %v", mod, err)
		}
		m := goVersionRe.FindSubmatch(b)
		if m == nil {
			return "", fmt.Errorf("%s has no go directive", mod)
		}
		return string(m[1]), nil
	case "start_minutes":
		for _, p := range s.allPages() {
			if p.Out == s.cfg.StartHref {
				if p.FM.Minutes == 0 {
					return "", fmt.Errorf("docs/%s has no minutes in its front matter", p.Doc)
				}
				return strconv.Itoa(p.FM.Minutes), nil
			}
		}
		return "", fmt.Errorf("start_href %q is not a page with minutes", s.cfg.StartHref)
	}
	return "", errors.New("unknown token")
}

// dataValue resolves "file.key.0.key" against site/data/file.json. The first
// word is the file name without ".json"; the rest are object keys and array
// indexes. A path that does not resolve is an error, so a number or a
// recorded output cannot go stale or be typed by hand without notice.
func (s *site) dataValue(path string) (any, error) {
	parts := strings.Split(path, ".")
	if len(parts) == 0 || parts[0] == "" {
		return nil, errors.New("a data token names a file of site/data and a path inside it")
	}
	name := parts[0]
	if s.data == nil {
		s.data = map[string]any{}
	}
	root, ok := s.data[name]
	if !ok {
		raw, err := os.ReadFile(filepath.Join(s.siteDir, "data", name+".json"))
		if err != nil {
			return nil, fmt.Errorf("cannot read site/data/%s.json: %v", name, err)
		}
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, fmt.Errorf("site/data/%s.json is not valid JSON: %v", name, err)
		}
		s.data[name] = root
	}
	cur := root
	for _, key := range parts[1:] {
		switch x := cur.(type) {
		case map[string]any:
			v, ok := x[key]
			if !ok {
				return nil, fmt.Errorf("no key %q in site/data/%s.json", key, name)
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(x) {
				return nil, fmt.Errorf("no index %q in an array of %d in site/data/%s.json", key, len(x), name)
			}
			cur = x[i]
		default:
			return nil, fmt.Errorf("%q is not inside an object or an array in site/data/%s.json", key, name)
		}
	}
	return cur, nil
}

// jsonForScript writes v as compact JSON that is safe inside a script element.
func jsonForScript(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
	return strings.TrimSpace(b.String())
}

// checkPartial warns when a partial does not follow the band convention:
// <section class="band" id="name" data-band="name">.
func (s *site) checkPartial(file, text string) {
	m := partialNameRe.FindStringSubmatch(file)
	if m == nil {
		s.msgs.warnf("site/landing/%s: file names are NN-name.html", file)
		return
	}
	want := m[2]
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "<section") {
		s.msgs.warnf("site/landing/%s: a partial is one <section class=\"band\" id=%q data-band=%q>", file, want, want)
		return
	}
	open := t
	if i := strings.Index(t, ">"); i >= 0 {
		open = t[:i+1]
	}
	if !bandClassRe.MatchString(open) {
		s.msgs.warnf("site/landing/%s: the opening tag lacks class=\"band\"", file)
	}
	for _, need := range []string{`id="` + want + `"`, `data-band="` + want + `"`} {
		if !strings.Contains(open, need) {
			s.msgs.warnf("site/landing/%s: the opening tag lacks %s", file, need)
		}
	}
	if !wrapRe.MatchString(t) {
		s.msgs.warnf("site/landing/%s: the content of a band sits in one <div class=\"wrap\">", file)
	}
}

// landingBody builds the content between the shared header and footer.
func (s *site) landingBody() (template.HTML, error) {
	names, texts, err := s.landingPartials()
	if err != nil {
		return "", err
	}
	if len(names) > 0 {
		var b strings.Builder
		for i, t := range texts {
			s.checkPartial(names[i], t)
			b.WriteString(s.substituteTokens("landing/"+names[i], t))
			if !strings.HasSuffix(t, "\n") {
				b.WriteByte('\n')
			}
		}
		return template.HTML(b.String()), nil
	}
	return template.HTML(s.defaultLanding()), nil
}

// defaultLanding is the landing page used when site/landing/ has no partials:
// the name, the description, the start button and the sections.
func (s *site) defaultLanding() string {
	var b strings.Builder
	b.WriteString(`<section class="band" id="hero" data-band="hero"><div class="wrap">` + "\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n", html.EscapeString(s.cfg.Title))
	if s.cfg.Description != "" {
		fmt.Fprintf(&b, "<p class=\"lede\">%s</p>\n", html.EscapeString(s.cfg.Description))
	}
	fmt.Fprintf(&b, "<p><a class=\"btn btn-primary\" href=\"%s\">%s</a></p>\n", html.EscapeString(s.cfg.StartHref), html.EscapeString(s.cfg.StartLabel))
	b.WriteString("</div></section>\n")
	b.WriteString(`<section class="band" id="sections" data-band="sections"><div class="wrap">` + "\n<ul>\n")
	for _, sec := range s.sections {
		if sec.Missing || sec.Index == nil {
			continue
		}
		fmt.Fprintf(&b, "<li><a href=\"%s\">%s</a> %s</li>\n", html.EscapeString(sec.Index.Out), html.EscapeString(sec.Title), html.EscapeString(sec.Blurb))
	}
	b.WriteString("</ul>\n</div></section>\n")
	return b.String()
}

// prepareLanding builds the landing page body before the messages are
// reported, so that a partial that breaks the conventions, or a data token
// that does not resolve, is reported and fails the build.
func (s *site) prepareLanding() {
	names, _, err := s.landingPartials()
	if err != nil {
		s.msgs.errorf("site/landing: %v", err)
		return
	}
	// A hand-written site/index.html is used as it is when there are no
	// partials; the landing page of an older checkout keeps working.
	if len(names) == 0 {
		if _, err := os.Stat(filepath.Join(s.siteDir, "index.html")); err == nil {
			s.msgs.warnf("site/landing/ has no partials; site/index.html is copied unchanged")
			s.legacyLanding = true
			return
		}
	}
	body, err := s.landingBody()
	if err != nil {
		s.msgs.errorf("site/landing: %v", err)
		return
	}
	s.landing = body
}

func (s *site) writeLanding(of *outFiles) error {
	if s.legacyLanding {
		data, err := os.ReadFile(filepath.Join(s.siteDir, "index.html"))
		if err != nil {
			return err
		}
		return of.write("index.html", data)
	}
	body := s.landing
	desc := s.cfg.Description
	v := landingView{
		Head:    s.headFor("index.html", s.cfg.Title, s.cfg.Title, desc, "website", "", false),
		Header:  s.headerFor("index.html", nil),
		Footer:  s.footerFor("index.html"),
		Content: body,
	}
	// widgets.js is needed by the landing page when a partial contains a widget.
	if strings.Contains(string(body), `data-widget=`) {
		v.Head.Widgets = true
	}
	var buf bytes.Buffer
	if err := shell.ExecuteTemplate(&buf, "landing", v); err != nil {
		return err
	}
	return of.write("index.html", buf.Bytes())
}
