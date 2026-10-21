package main

import (
	"html"
	"regexp"
	"strings"
)

var (
	ansiRe     = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	locRe      = regexp.MustCompile(`^\s+\S+:\d+(:\d+)?$`)
	planHintRe = regexp.MustCompile(`(?m)^(Plan:|Apply complete!|Destroy complete!|\s*# \S.* (will be|must be) )`)
	addRe      = regexp.MustCompile(`^\s*\+(\s|$)`)
	changeRe   = regexp.MustCompile(`^\s*(~|-/\+|\+/-)(\s|$)`)
	destroyRe  = regexp.MustCompile(`^\s*-(\s|$)`)

	unknownRe  = regexp.MustCompile(`\(known after apply\)`)
	forcesRe   = regexp.MustCompile(`# forces replacement`)
	nToAddRe   = regexp.MustCompile(`\b(\d+ (?:to add|added))\b`)
	nToChgRe   = regexp.MustCompile(`\b(\d+ (?:to change|changed))\b`)
	nToDestrRe = regexp.MustCompile(`\b(\d+ (?:to destroy|destroyed))\b`)
)

// colorizeOutput renders command output as HTML with the span classes of the
// terminal contract: out-add, out-change, out-destroy, out-err and out-dim.
// The classification works on the text of the line, so output captured
// without colour carries the same meaning.
func colorizeOutput(text string) string {
	text = ansiRe.ReplaceAllString(text, "")
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return ""
	}
	isPlan := planHintRe.MatchString(text)
	lines := strings.Split(text, "\n")
	var b strings.Builder
	inErr := false
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		trimmed := strings.TrimSpace(line)
		cls := ""
		switch {
		case trimmed == "":
			inErr = false
		case strings.HasPrefix(trimmed, "Error:"):
			cls, inErr = "err", true
		case locRe.MatchString(line):
			cls = "dim"
		case inErr:
			cls = "err"
		case trimmed == "...":
			cls = "dim"
		case isPlan && changeRe.MatchString(line):
			cls = "change"
		case isPlan && addRe.MatchString(line):
			cls = "add"
		case isPlan && destroyRe.MatchString(line):
			cls = "destroy"
		case isPlan && strings.HasPrefix(trimmed, "# "):
			cls = "dim"
		case strings.HasPrefix(trimmed, "Success!"):
			cls = "add"
		}
		esc := html.EscapeString(line)
		esc = unknownRe.ReplaceAllString(esc, `<span class="out-dim">(known after apply)</span>`)
		esc = forcesRe.ReplaceAllString(esc, `<span class="out-change"># forces replacement</span>`)
		if strings.HasPrefix(trimmed, "Plan:") || strings.HasPrefix(trimmed, "Apply complete!") || strings.HasPrefix(trimmed, "Destroy complete!") {
			esc = nToAddRe.ReplaceAllString(esc, `<span class="out-add">$1</span>`)
			esc = nToChgRe.ReplaceAllString(esc, `<span class="out-change">$1</span>`)
			esc = nToDestrRe.ReplaceAllString(esc, `<span class="out-destroy">$1</span>`)
		}
		if cls != "" {
			b.WriteString(`<span class="out-` + cls + `">` + esc + `</span>`)
		} else {
			b.WriteString(esc)
		}
	}
	return b.String()
}
