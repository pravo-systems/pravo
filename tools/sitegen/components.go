package main

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Three containers shape what a page already says: a table that maps one
// thing to another, a list of short cards, and the path through a runbook.
// None of them needs a script, and each reads as plain Markdown without the
// site.
//
//	:::mapping          a table whose first column is what the reader has and
//	                    whose second is what replaces it (or a status)
//	:::cards            "###" headings, each with the text under it
//	:::runbook [GROUP]  the steps of a group, from the pages that state a gate

var (
	mapRowRe    = regexp.MustCompile(`(?s)<tr>.*?</tr>`)
	mapCellRe   = regexp.MustCompile(`<td([^>]*)>(.*?)</td>`)
	mapTagRe    = regexp.MustCompile(`<[^>]*>`)
	mapStatusRe = regexp.MustCompile(`<th[^>]*>\s*Status\s*</th>`)
	mapNoneRe   = regexp.MustCompile(`^(no equivalent|none|absent|not supported)\b`)
	cardTitleRe = regexp.MustCompile(`<h3>`)
)

// mapStatus gives the value of data-v for a cell that holds one status word.
var mapStatus = map[string]string{"supported": "yes", "partial": "part", "not supported": "no"}

func cellText(inner string) string {
	t := html.UnescapeString(mapTagRe.ReplaceAllString(inner, ""))
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(t)), ".")
}

// markMapping marks the rows and cells of a rendered table: a cell that holds
// a status word carries it in data-v, and a row whose second cell says that
// there is nothing (or whose status is "no") gets the class map-none.
func markMapping(h string) string {
	return mapRowRe.ReplaceAllStringFunc(h, func(row string) string {
		if !strings.Contains(row, "<td") {
			return row
		}
		i, none := 0, false
		out := mapCellRe.ReplaceAllStringFunc(row, func(cell string) string {
			m := mapCellRe.FindStringSubmatch(cell)
			text := cellText(m[2])
			col := i
			i++
			if v, ok := mapStatus[text]; ok {
				if v == "no" {
					none = true
				}
				return fmt.Sprintf(`<td%s class="map-status" data-v="%s">%s</td>`, m[1], v, m[2])
			}
			if col == 1 && mapNoneRe.MatchString(text) {
				none = true
			}
			return cell
		})
		if none {
			out = strings.Replace(out, "<tr>", `<tr class="map-none">`, 1)
		}
		return out
	})
}

// renderMapping renders ":::mapping": the tables of its body, marked for the
// style sheet. A table whose second column is headed Status is a matrix; any
// other table is a map from the first column to the second.
func (r *renderer) renderMapping(d *directive) string {
	body := r.sub(d.parts[0])
	if !strings.Contains(body, "<table") {
		r.errorf(d.line, ":::mapping holds a table, and its body has none")
		return body
	}
	cls := "mapping"
	if mapStatusRe.MatchString(body) {
		cls += " mapping-status"
	}
	return `<div class="` + cls + `">` + "\n" + markMapping(body) + "\n</div>"
}

// renderCards renders ":::cards": each "###" heading and the text under it is
// one card. The links in a card are stretched over the whole card by the
// style sheet.
func (r *renderer) renderCards(d *directive) string {
	body := r.sub(d.parts[0])
	idx := cardTitleRe.FindAllStringIndex(body, -1)
	if len(idx) == 0 {
		r.errorf(d.line, ":::cards needs one ### heading for each card")
		return body
	}
	if strings.TrimSpace(body[:idx[0][0]]) != "" {
		r.errorf(d.line, ":::cards has text before its first ### heading")
	}
	var b strings.Builder
	b.WriteString("<div class=\"cards doc-cards\">\n")
	for i, at := range idx {
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		card := body[at[0]:end]
		card = strings.Replace(card, "<h3>", `<h3 class="card-title">`, 1)
		card = strings.ReplaceAll(card, "<p>", `<p class="card-text">`)
		b.WriteString("<div class=\"card spot\">\n" + strings.TrimSpace(card) + "\n</div>\n")
	}
	b.WriteString("</div>")
	return b.String()
}

// renderRunbook renders ":::runbook [GROUP]" for a page that is not a step.
func (r *renderer) renderRunbook(d *directive) string {
	group := strings.TrimSpace(d.arg)
	if group == "" {
		group = "Runbook"
	}
	out := r.runbookHTML(group, nil)
	if out == "" {
		r.errorf(d.line, ":::runbook finds no page with a gate in the group %q of this section", group)
	}
	return out
}

// runbookSteps lists the pages of a group of the section that state a gate,
// in the order of the group.
func runbookSteps(sec *section, group string) []*page {
	var out []*page
	for _, g := range sec.Groups {
		if g.Name != group {
			continue
		}
		for _, q := range g.Pages {
			if q.FM.Gate != "" {
				out = append(out, q)
			}
		}
	}
	return out
}

// runbookLookups lists the pages of the group that state no gate: the pages
// that a reader looks things up in, beside the path.
func runbookLookups(sec *section, group string) []*page {
	var out []*page
	for _, g := range sec.Groups {
		if g.Name != group {
			continue
		}
		for _, q := range g.Pages {
			if q.FM.Gate == "" && !q.IsIndex {
				out = append(out, q)
			}
		}
	}
	return out
}

// runbookHTML draws the steps of a group as a row of nodes: the steps before
// the open page are done, the open page is marked and the rest are ahead.
// current is nil on a page that is not a step. The gate of each step stands
// under its title. The markup is a list of links, and it needs no script.
func (r *renderer) runbookHTML(group string, current *page) string {
	steps := runbookSteps(r.p.Section, group)
	if len(steps) == 0 {
		return ""
	}
	cols := len(steps)
	if cols > 4 {
		cols = (cols + 1) / 2
	}
	at := -1
	for i, q := range steps {
		if q == current {
			at = i
		}
	}
	var b strings.Builder
	label := fmt.Sprintf("The runbook, %d steps", len(steps))
	if at >= 0 {
		label = fmt.Sprintf("The runbook, step %d of %d", at+1, len(steps))
	}
	fmt.Fprintf(&b, "<nav class=\"runbook\" aria-label=\"%s\">\n<p class=\"rb-label\">%s</p>\n<ol class=\"rb-list\" style=\"--cols:%d\">\n", html.EscapeString(label), html.EscapeString(label), cols)
	for i, q := range steps {
		state := ""
		switch {
		case at < 0:
		case i < at:
			state = " rb-past"
		case i == at:
			state = " rb-here"
		default:
			state = " rb-ahead"
		}
		if (i+1)%cols == 0 || i == len(steps)-1 {
			state += " rb-end"
		}
		inner := fmt.Sprintf(`<span class="rb-node" aria-hidden="true"></span><span class="rb-n">%s</span><span class="rb-t">%s</span><span class="rb-g"><span class="sr-only">Gate: </span>%s</span>`,
			html.EscapeString(q.Lesson), html.EscapeString(q.plainTitle()), r.inline(q.FM.Gate, q.Lines))
		if i == at {
			fmt.Fprintf(&b, "<li class=\"rb-step%s\"><span class=\"rb-in\" aria-current=\"step\">%s</span></li>\n", state, inner)
			continue
		}
		fmt.Fprintf(&b, "<li class=\"rb-step%s\"><a class=\"rb-in\" href=\"%s\">%s</a></li>\n", state, relURL(r.p.Out, q.Out), inner)
	}
	b.WriteString("</ol>\n")
	if looks := runbookLookups(r.p.Section, group); len(looks) > 0 {
		b.WriteString("<p class=\"rb-lookup\"><span class=\"rb-lookup-l\">Lookups</span>")
		for _, q := range looks {
			fmt.Fprintf(&b, " <a href=\"%s\"><span class=\"rb-n\">%s</span> %s</a>", relURL(r.p.Out, q.Out), html.EscapeString(q.Lesson), html.EscapeString(q.plainTitle()))
		}
		b.WriteString("</p>\n")
	}
	b.WriteString("</nav>")
	return b.String()
}

var layoutOpenRe = regexp.MustCompile(`^:::(mapping|cards|runbook)\b`)

// stripLayout removes the fences of the three layout containers from the text
// of a Markdown twin. What the container held, a table or headings, stays as
// plain Markdown, and a runbook, which has no body, leaves nothing.
func stripLayout(body []byte) []byte {
	var out []string
	var ft fenceTracker
	closing := false
	for _, l := range strings.Split(string(body), "\n") {
		if ft.feed(l) {
			out = append(out, l)
			continue
		}
		if layoutOpenRe.MatchString(l) {
			closing = true
			continue
		}
		if closing && closeRe.MatchString(l) {
			closing = false
			continue
		}
		out = append(out, l)
	}
	return []byte(strings.Join(out, "\n"))
}

// upperFirst capitalises the first letter of a sentence that starts with a word.
// A sentence that starts with a tag or a symbol is left as it is.
func upperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if !unicode.IsLower(r) {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

var causeFixRe = regexp.MustCompile(`(?s)<p>Cause: (.*?)</p>`)

// causeFix sets a paragraph that starts with "Cause:" as two labelled lines.
// The text after the first " Fix: " is the fix; a paragraph without one is a
// cause alone. The pages that list pitfalls write each entry this way.
func causeFix(h string) string {
	if !strings.Contains(h, "<p>Cause: ") {
		return h
	}
	return causeFixRe.ReplaceAllStringFunc(h, func(m string) string {
		inner := causeFixRe.FindStringSubmatch(m)[1]
		cause, fix, ok := strings.Cut(inner, " Fix: ")
		out := "<div class=\"cause-fix\">\n<p><span class=\"cf-l\">Cause</span> " + upperFirst(cause) + "</p>\n"
		if ok {
			out += "<p><span class=\"cf-l cf-fix\">Fix</span> " + upperFirst(fix) + "</p>\n"
		}
		return out + "</div>"
	})
}
