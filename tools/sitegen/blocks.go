package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
)

// directive is a container (":::name args ... :::") or a callout
// ("> [!NOTE]") found by the pre-pass.
type directive struct {
	kind  string // "container" or "callout"
	name  string
	arg   string
	line  int // file line of the opener
	parts []*part
}

// part is a run of lines of a container: the body before the first
// "::marker" has an empty name, each marker starts a new part.
type part struct {
	name  string
	line  int // file line of the first line of the part
	lines []string
}

func (p *part) text() string { return strings.Join(p.lines, "\n") }

var (
	fenceOpenRe = regexp.MustCompile("^( {0,3})(`{3,}|~{3,})(.*)$")
	containerRe = regexp.MustCompile(`^:::([a-z][a-z-]*)(?:[ \t]+(.*?))?[ \t]*$`)
	closeRe     = regexp.MustCompile(`^:::[ \t]*$`)
	partRe      = regexp.MustCompile(`^::([a-z]+)[ \t]*$`)
	calloutRe   = regexp.MustCompile(`^>[ \t]?\[!([A-Za-z]+)\][ \t]*$`)
)

var calloutTitles = map[string]string{
	"NOTE": "Note", "TIP": "Tip", "WARNING": "Warning", "PITFALL": "Pitfall", "CHECK": "Check",
}

// containerSpec lists the containers, their part markers and whether the
// argument is required.
var containerSpec = map[string]struct {
	parts  []string
	needed string // description of the required argument, empty if none
}{
	"compare":   {nil, ""},
	"tabs":      {nil, ""},
	"predict":   {[]string{"reveal"}, "the question"},
	"exercise":  {[]string{"hint", "solution"}, "a title"},
	"check":     {[]string{"question", "answer"}, ""},
	"deep-dive": {nil, "a title"},
	"figure":    {nil, "a caption"},
	"widget":    {[]string{"fallback"}, "the widget name"},
	"demo":      {nil, "the demo name"},
	"mapping":   {nil, ""},
	"cards":     {nil, ""},
	"runbook":   {nil, ""},
}

// fenceTracker follows fenced code blocks line by line.
type fenceTracker struct {
	ch byte
	n  int
}

func (f *fenceTracker) in() bool { return f.n > 0 }

// feed updates the state with one line and reports whether the line belongs to
// a fence (opener, body or closer).
func (f *fenceTracker) feed(line string) bool {
	if f.n > 0 {
		t := strings.TrimLeft(line, " ")
		if len(line)-len(t) <= 3 && t != "" && t[0] == f.ch {
			run := len(t) - len(strings.TrimLeft(t, string(f.ch)))
			if run >= f.n && strings.TrimSpace(t[run:]) == "" {
				f.ch, f.n = 0, 0
			}
		}
		return true
	}
	if m := fenceOpenRe.FindStringSubmatch(line); m != nil {
		if m[2][0] == '`' && strings.Contains(m[3], "`") {
			return false
		}
		f.ch, f.n = m[2][0], len(m[2])
		return true
	}
	return false
}

// scan finds the directives in lines and returns the lines with every
// directive replaced by a placeholder comment (followed by empty lines so that
// line numbers stay the same) and the directives in placeholder order.
func (r *renderer) scan(lines []string, lineBase int, nested bool) ([]string, []*directive) {
	out := make([]string, 0, len(lines))
	var dirs []*directive
	var fence fenceTracker
	for i := 0; i < len(lines); {
		line := lines[i]
		if fence.feed(line) {
			out = append(out, line)
			i++
			continue
		}
		if m := containerRe.FindStringSubmatch(line); m != nil {
			d, end := r.collect(lines, i, lineBase, m[1], strings.TrimSpace(m[2]), nested)
			if d == nil {
				out = append(out, "")
				i++
				continue
			}
			out = append(out, fmt.Sprintf(slotFmt, len(dirs)))
			dirs = append(dirs, d)
			for k := i + 1; k <= end; k++ {
				out = append(out, "")
			}
			i = end + 1
			continue
		}
		if closeRe.MatchString(line) {
			r.errorf(lineBase+i, "::: closes no container")
			out = append(out, "")
			i++
			continue
		}
		if m := calloutRe.FindStringSubmatch(line); m != nil {
			typ := strings.ToUpper(m[1])
			if _, ok := calloutTitles[typ]; ok {
				d, end := collectCallout(lines, i, lineBase, typ)
				out = append(out, fmt.Sprintf(slotFmt, len(dirs)))
				dirs = append(dirs, d)
				for k := i + 1; k <= end; k++ {
					out = append(out, "")
				}
				i = end + 1
				continue
			}
		}
		out = append(out, line)
		i++
	}
	return out, dirs
}

// collectCallout reads a "> [!TYPE]" blockquote.
func collectCallout(lines []string, start, lineBase int, typ string) (*directive, int) {
	d := &directive{kind: "callout", name: typ, line: lineBase + start}
	p := &part{line: lineBase + start + 1}
	end := start
	for j := start + 1; j < len(lines) && strings.HasPrefix(lines[j], ">"); j++ {
		l := strings.TrimPrefix(lines[j], ">")
		l = strings.TrimPrefix(l, " ")
		p.lines = append(p.lines, l)
		end = j
	}
	d.parts = []*part{p}
	return d, end
}

// collect reads a container opened at lines[start].
func (r *renderer) collect(lines []string, start, lineBase int, name, arg string, nested bool) (*directive, int) {
	line := lineBase + start
	spec, known := containerSpec[name]
	if !known {
		r.errorf(line, "unknown container :::%s (want %s)", name, strings.Join(containerNames(), ", "))
		return nil, start
	}
	if nested {
		r.errorf(line, ":::%s inside another container: containers do not nest", name)
		return nil, start
	}
	d := &directive{kind: "container", name: name, arg: arg, line: line}
	cur := &part{line: line + 1}
	d.parts = append(d.parts, cur)
	var fence fenceTracker
	for j := start + 1; j < len(lines); j++ {
		l := lines[j]
		if fence.feed(l) {
			cur.lines = append(cur.lines, l)
			continue
		}
		switch {
		case closeRe.MatchString(l):
			if spec.needed != "" && arg == "" {
				r.errorf(line, ":::%s needs %s after the name", name, spec.needed)
			}
			return d, j
		case containerRe.MatchString(l):
			r.errorf(lineBase+j, "container opened inside :::%s (line %d): containers do not nest", name, line)
			cur.lines = append(cur.lines, "")
		case partRe.MatchString(l):
			marker := partRe.FindStringSubmatch(l)[1]
			ok := false
			for _, a := range spec.parts {
				if a == marker {
					ok = true
				}
			}
			if !ok {
				r.errorf(lineBase+j, "::%s is not a part of :::%s", marker, name)
			}
			cur = &part{name: marker, line: lineBase + j + 1}
			d.parts = append(d.parts, cur)
		default:
			cur.lines = append(cur.lines, l)
		}
	}
	r.errorf(line, ":::%s is never closed (a line with only ::: ends it)", name)
	return nil, start
}

func containerNames() []string {
	return []string{"compare", "tabs", "predict", "exercise", "check", "deep-dive", "figure", "widget", "demo", "mapping", "cards", "runbook"}
}

// sub renders the lines of a part as nested Markdown.
func (r *renderer) sub(p *part) string {
	out, _, _ := r.renderMD(p.lines, p.line, true)
	return strings.TrimSpace(out)
}

func nonBlank(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return true
		}
	}
	return false
}

// renderDirective renders one container or callout to HTML.
func (r *renderer) renderDirective(d *directive) string {
	if d.kind == "callout" {
		title := calloutTitles[d.name]
		return fmt.Sprintf("<div class=\"callout callout-%s\" role=\"note\">\n<p class=\"callout-title\">%s</p>\n%s\n</div>",
			strings.ToLower(d.name), title, r.sub(d.parts[0]))
	}
	switch d.name {
	case "compare":
		return r.renderCompare(d)
	case "tabs":
		return r.renderTabs(d)
	case "predict":
		return r.renderPredict(d)
	case "exercise":
		return r.renderExercise(d)
	case "check":
		return r.renderCheck(d)
	case "deep-dive":
		return fmt.Sprintf("<details class=\"deep-dive\">\n<summary>%s</summary>\n%s\n</details>",
			r.inline(d.arg, d.line), r.sub(d.parts[0]))
	case "figure":
		return r.renderFigure(d)
	case "widget":
		return r.renderWidget(d)
	case "demo":
		return r.renderDemo(d)
	case "mapping":
		return r.renderMapping(d)
	case "cards":
		return r.renderCards(d)
	case "runbook":
		return r.renderRunbook(d)
	}
	return ""
}

// fenceSpan is a fenced block found at the top level of a part.
type fenceSpan struct {
	start, end int // indexes into the part's lines, opener and closer
	info       string
}

// topFences lists the fenced blocks of a run of lines.
func topFences(lines []string) []fenceSpan {
	var spans []fenceSpan
	var fence fenceTracker
	cur := -1
	for i, l := range lines {
		was := fence.in()
		in := fence.feed(l)
		if in && !was {
			m := fenceOpenRe.FindStringSubmatch(l)
			cur = len(spans)
			spans = append(spans, fenceSpan{start: i, end: len(lines) - 1, info: strings.TrimSpace(m[3])})
		}
		if was && !fence.in() && cur >= 0 {
			spans[cur].end = i
			cur = -1
		}
	}
	return spans
}

// renderCompare lays the listings of a :::compare out side by side. The style
// sheet puts them in columns at least as wide as their longest line, counted
// in characters here, and stacks them when two such columns do not fit.
func (r *renderer) renderCompare(d *directive) string {
	p := d.parts[0]
	spans := topFences(p.lines)
	count, longest := 0, 0
	for _, sp := range spans {
		fi, err := parseFenceInfo(sp.info)
		if err != nil || fi.Hidden || fi.Out {
			continue
		}
		count++
		if fi.Title == "" && fi.File == "" {
			r.errorf(p.line+sp.start, "every block in :::compare needs a title or a file name")
		}
		for _, l := range p.lines[sp.start+1 : sp.end] {
			longest = max(longest, columns(l))
		}
	}
	if count < 2 {
		r.errorf(d.line, ":::compare needs at least two code blocks, found %d", count)
	}
	return fmt.Sprintf("<div class=\"compare\" data-count=\"%d\" style=\"--longest:%d\">\n%s\n</div>", count, longest, r.sub(p))
}

// columns is the width of a line of a listing in characters, with a tab
// reaching the next stop of four, as the listings are set.
func columns(line string) int {
	n := 0
	for _, c := range line {
		if c == '\t' {
			n += 4 - n%4
			continue
		}
		n++
	}
	return n
}

func (r *renderer) renderTabs(d *directive) string {
	p := d.parts[0]
	spans := topFences(p.lines)
	r.tabs++
	group := r.tabs
	type tab struct {
		label string
		html  string
	}
	var tabs []tab
	cursor := 0
	seen := map[string]bool{}
	for k := 0; k < len(spans); k++ {
		sp := spans[k]
		fi, err := parseFenceInfo(sp.info)
		if err != nil {
			r.errorf(p.line+sp.start, "code block: %v", err)
			continue
		}
		if fi.Out {
			r.errorf(p.line+sp.start, "an out block must directly follow the run block it belongs to")
			continue
		}
		end := sp.end
		if fi.Run && k+1 < len(spans) {
			if nfi, err := parseFenceInfo(spans[k+1].info); err == nil && nfi.Out {
				end = spans[k+1].end
				k++
			}
		}
		label := fi.Title
		if label == "" {
			label = fi.File
		}
		if label == "" && fi.Run {
			label = "Terminal"
		}
		if label == "" {
			r.errorf(p.line+sp.start, "every block in :::tabs needs a title")
			label = fmt.Sprintf("Tab %d", len(tabs)+1)
		}
		if seen[label] {
			r.errorf(p.line+sp.start, "two tabs are titled %q", label)
		}
		seen[label] = true
		body := &part{line: p.line + cursor, lines: p.lines[cursor : end+1]}
		cursor = end + 1
		tabs = append(tabs, tab{label, r.sub(body)})
	}
	if cursor < len(p.lines) && nonBlank(p.lines[cursor:]) && len(tabs) > 0 {
		r.errorf(p.line+cursor, "text after the last block of :::tabs")
	}
	if len(tabs) < 2 {
		r.errorf(d.line, ":::tabs needs at least two code blocks, found %d", len(tabs))
	}
	var b strings.Builder
	listName := d.arg
	if listName == "" {
		var labels []string
		for _, t := range tabs {
			labels = append(labels, t.label)
		}
		listName = strings.Join(labels, ", ")
	}
	b.WriteString("<div class=\"tabs\">\n<div class=\"tab-list\" role=\"tablist\" aria-label=\"" + html.EscapeString(plainText(listName)) + "\">\n")
	for i, t := range tabs {
		sel := "false"
		if i == 0 {
			sel = "true"
		}
		fmt.Fprintf(&b, "<button role=\"tab\" type=\"button\" id=\"tab-%d-%d\" aria-selected=\"%s\" aria-controls=\"panel-%d-%d\">%s</button>\n",
			group, i+1, sel, group, i+1, html.EscapeString(t.label))
	}
	b.WriteString("</div>\n")
	for i, t := range tabs {
		active := ""
		if i == 0 {
			active = " data-active"
		}
		fmt.Fprintf(&b, "<div class=\"tab-panel\" role=\"tabpanel\" id=\"panel-%d-%d\" aria-labelledby=\"tab-%d-%d\" data-title=\"%s\"%s>\n%s\n</div>\n",
			group, i+1, group, i+1, html.EscapeString(t.label), active, t.html)
	}
	b.WriteString("</div>")
	return b.String()
}

func (r *renderer) renderPredict(d *directive) string {
	var pre, reveal *part
	for _, p := range d.parts {
		switch p.name {
		case "reveal":
			if reveal != nil {
				r.errorf(p.line-1, "::reveal appears twice")
			}
			reveal = p
		case "":
			pre = p
		}
	}
	if reveal == nil {
		reveal, pre = pre, nil
	}
	if reveal == nil || !nonBlank(reveal.lines) {
		r.errorf(d.line, ":::predict has no answer to reveal")
		return ""
	}
	box := fmt.Sprintf("<details class=\"predict\">\n<summary><span class=\"predict-label\">Predict</span> %s</summary>\n<div class=\"predict-reveal\">\n%s\n</div>\n</details>",
		r.inline(d.arg, d.line), r.sub(reveal))
	if pre != nil && nonBlank(pre.lines) {
		return "<div class=\"predict-group\">\n" + r.sub(pre) + "\n" + box + "\n</div>"
	}
	return box
}

func (r *renderer) renderExercise(d *directive) string {
	var body *part
	var hints []*part
	var solution *part
	for _, p := range d.parts {
		switch p.name {
		case "":
			body = p
		case "hint":
			hints = append(hints, p)
		case "solution":
			if solution != nil {
				r.errorf(p.line-1, "::solution appears twice")
			}
			solution = p
		}
	}
	if len(hints) > 2 {
		r.errorf(hints[2].line-1, ":::exercise takes at most two hints")
		hints = hints[:2]
	}
	var b strings.Builder
	id := r.generatedID("exercise", d.arg)
	fmt.Fprintf(&b, "<section class=\"exercise\">\n<header><span class=\"exercise-label\">Exercise</span><h3 id=\"%s\">%s</h3></header>\n", id, permalink(id, r.inline(d.arg, d.line)))
	fmt.Fprintf(&b, "<div class=\"exercise-body\">\n%s\n</div>\n", r.sub(body))
	for i, h := range hints {
		label := "Hint"
		if len(hints) > 1 {
			label = fmt.Sprintf("Hint %d", i+1)
		}
		fmt.Fprintf(&b, "<details class=\"hint\" data-tier=\"%d\">\n<summary>%s</summary>\n%s\n</details>\n", i+1, label, r.sub(h))
	}
	if solution != nil {
		fmt.Fprintf(&b, "<details class=\"solution\">\n<summary>Solution</summary>\n%s\n</details>\n", r.sub(solution))
	}
	b.WriteString("</section>")
	return b.String()
}

// permalink makes the text of a generated heading its own link, as for the
// headings of the Markdown. A title that holds a link is left as it is.
func permalink(id, inner string) string {
	if strings.Contains(inner, "<a ") {
		return inner
	}
	return `<a class="anchor" href="#` + html.EscapeString(id) + `">` + inner + `</a>`
}

// checkQuestion is one question of a check: the question, one paragraph that
// ends in "?", and the premise before it, the paragraphs and listings that the
// question is about, when it has one.
type checkQuestion struct {
	premise  *part
	question string
	line     int
}

// lastParagraph returns the bounds of the last paragraph of lines: the lines
// after the last blank line, without the blank lines at the end.
func lastParagraph(lines []string) (start, end int) {
	end = len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start = end
	for start > 0 && strings.TrimSpace(lines[start-1]) != "" {
		start--
	}
	return start, end
}

// questionOf reads a question from the lines of a part: the last paragraph is
// the question, and what comes before it is the premise.
func (r *renderer) questionOf(p *part) *checkQuestion {
	start, end := lastParagraph(p.lines)
	q := &checkQuestion{question: strings.TrimSpace(strings.Join(p.lines[start:end], "\n")), line: p.line + start}
	if !strings.HasSuffix(q.question, "?") {
		r.errorf(q.line, "a check question ends in ?; found %q", q.question)
	}
	if nonBlank(p.lines[:start]) {
		q.premise = &part{line: p.line, lines: p.lines[:start]}
	}
	return q
}

// checkItem draws one question with its answer. A question with a premise is
// framed with it, and the number stands beside the premise.
func (r *renderer) checkItem(q *checkQuestion, answer *part) string {
	details := fmt.Sprintf("<details>\n<summary>%s</summary>\n%s\n</details>\n", r.inline(q.question, q.line), r.sub(answer))
	if q.premise == nil {
		return details
	}
	return fmt.Sprintf("<div class=\"check-item\">\n<div class=\"check-premise\">\n%s\n</div>\n%s</div>\n", r.sub(q.premise), details)
}

func (r *renderer) renderCheck(d *directive) string {
	// The parts are the first question, then an ::answer for each question. A
	// question is one paragraph that ends in "?": the text before the first
	// ::answer, the last paragraph of the answer before it, or the end of a
	// ::question part, whose other lines are the premise shown above it.
	var b strings.Builder
	title := "Check yourself"
	if d.arg != "" {
		title = d.arg
	}
	id := r.generatedID("check", title)
	fmt.Fprintf(&b, "<section class=\"check\">\n<h3 id=\"%s\">%s</h3>\n", id, permalink(id, r.inline(title, d.line)))
	var q *checkQuestion
	n := 0
	for i, p := range d.parts {
		switch p.name {
		case "":
			if nonBlank(p.lines) {
				q = r.questionOf(p)
			}
		case "question":
			if q != nil {
				r.errorf(p.line-1, "::question follows a question that has no ::answer")
			}
			q = r.questionOf(p)
		case "answer":
			if q == nil {
				r.errorf(p.line-1, "::answer follows no question")
				continue
			}
			answer := p.lines
			var next *checkQuestion
			if i+1 < len(d.parts) && d.parts[i+1].name == "answer" {
				// The last paragraph of this answer is the next question.
				start, end := lastParagraph(answer)
				next = &checkQuestion{question: strings.TrimSpace(strings.Join(answer[start:end], "\n")), line: p.line + start}
				if !strings.HasSuffix(next.question, "?") {
					r.errorf(p.line, "a check question ends in ?; found %q before ::answer", next.question)
				}
				answer = answer[:start]
			}
			if !nonBlank(answer) {
				r.errorf(p.line-1, "::answer has no answer")
			}
			b.WriteString(r.checkItem(q, &part{line: p.line, lines: answer}))
			n++
			q = next
		}
	}
	if n == 0 || q != nil {
		r.errorf(d.line, ":::check needs questions, each followed by ::answer")
		if n == 0 {
			return ""
		}
	}
	b.WriteString("</section>")
	return b.String()
}

func (r *renderer) renderFigure(d *directive) string {
	r.fig++
	n := r.fig
	body := d.parts[0]
	if !nonBlank(body.lines) {
		r.errorf(d.line, ":::figure has no content")
	}
	return fmt.Sprintf("<figure class=\"fig\" id=\"fig-%d\">\n<div class=\"fig-body\">\n%s\n</div>\n<figcaption><span class=\"fig-n\">fig. %d</span> %s</figcaption>\n</figure>",
		n, r.sub(body), n, r.inline(d.arg, d.line))
}

var widgetNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func (r *renderer) renderWidget(d *directive) string {
	if !widgetNameRe.MatchString(d.arg) {
		r.errorf(d.line, ":::widget needs a name of lowercase letters, digits and hyphens, found %q", d.arg)
		return ""
	}
	r.hasWidget = true
	var data, fallback *part
	for _, p := range d.parts {
		switch p.name {
		case "":
			data = p
		case "fallback":
			fallback = p
		}
	}
	raw := strings.TrimSpace(data.text())
	// The JSON may sit in a ```json fence so that the page reads well without the site.
	if strings.HasPrefix(raw, "```") || strings.HasPrefix(raw, "~~~") {
		ls := strings.Split(raw, "\n")
		if len(ls) >= 2 {
			ls = ls[1:]
			if last := strings.TrimSpace(ls[len(ls)-1]); strings.Trim(last, "`~") == "" {
				ls = ls[:len(ls)-1]
			}
			raw = strings.Join(ls, "\n")
		}
	}
	if raw == "" {
		raw = "{}"
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(raw)); err != nil {
		r.errorf(data.line, ":::widget %s: the body is not valid JSON: %v", d.arg, err)
		return ""
	}
	jsonText := compact.String()
	// Keep the text safe inside <script>: no "</script>", no "<!--".
	for ch, esc := range map[string]string{"<": "003c", ">": "003e", "&": "0026"} {
		jsonText = strings.ReplaceAll(jsonText, ch, "\\"+"u"+esc)
	}

	var fb string
	switch {
	case fallback != nil && nonBlank(fallback.lines):
		fb = r.sub(fallback)
	default:
		var meta struct {
			Fallback string `json:"fallback"`
			Alt      string `json:"alt"`
		}
		_ = json.Unmarshal([]byte(raw), &meta)
		text := meta.Fallback
		if text == "" {
			text = meta.Alt
		}
		if text == "" {
			r.warnf(d.line, ":::widget %s has no fallback text (add a ::fallback part or a \"fallback\" string)", d.arg)
			text = "This figure needs JavaScript."
		}
		fb = "<p>" + html.EscapeString(text) + "</p>"
	}
	return fmt.Sprintf("<div class=\"widget\" data-widget=\"%s\">\n<script type=\"application/json\">%s</script>\n<noscript>%s</noscript>\n</div>",
		d.arg, jsonText, fb)
}
