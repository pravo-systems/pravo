package main

import (
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
)

// fenceInfo is the parsed info string of a code fence
// (docs/AUTHORING.md section 4.1).
type fenceInfo struct {
	Lang    string
	File    string
	Title   string
	HL      map[int]bool
	LN      bool
	Status  string
	Run     bool
	Out     bool
	Exit    int
	Hidden  bool
	Check   bool
	Present map[string]bool
}

var bareAttrs = map[string]bool{"run": true, "out": true, "hidden": true, "check": true, "ln": true}

var statusBadges = map[string]struct{ text, title string }{
	"valid":      {"valid", "Accepted"},
	"conflict":   {"conflict", "Two values for one field disagree"},
	"rejected":   {"rejected", "Rejected before any provider starts"},
	"unintended": {"unintended", "Valid, but not what was meant"},
}

// splitInfo splits an info string on spaces, keeping quoted values together.
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
			quote = r
			started = true
		case r == ' ' || r == '\t':
			if started || cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in %q", s)
	}
	if started || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out, nil
}

// parseFenceInfo parses ```lang key=value flag.
func parseFenceInfo(info string) (fenceInfo, error) {
	fi := fenceInfo{Present: map[string]bool{}}
	toks, err := splitInfo(strings.TrimSpace(info))
	if err != nil {
		return fi, err
	}
	for i, t := range toks {
		key, val, hasVal := strings.Cut(t, "=")
		if i == 0 && !hasVal && !bareAttrs[key] {
			fi.Lang = t
			continue
		}
		fi.Present[key] = true
		switch key {
		case "file":
			if !hasVal || val == "" {
				return fi, fmt.Errorf("file needs a name: file=NAME")
			}
			fi.File = val
		case "title":
			if !hasVal {
				return fi, fmt.Errorf(`title needs a value: title="TEXT"`)
			}
			fi.Title = val
		case "hl":
			set, err := parseLineSet(val)
			if err != nil {
				return fi, fmt.Errorf("hl=%q: %v", val, err)
			}
			fi.HL = set
		case "status":
			if _, ok := statusBadges[val]; !ok {
				return fi, fmt.Errorf("status=%q: want valid, conflict, rejected or unintended", val)
			}
			fi.Status = val
		case "exit":
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 || n > 255 {
				return fi, fmt.Errorf("exit=%q is not an exit code", val)
			}
			fi.Exit = n
		case "ln", "run", "out", "hidden", "check":
			if hasVal {
				return fi, fmt.Errorf("%s takes no value", key)
			}
			switch key {
			case "ln":
				fi.LN = true
			case "run":
				fi.Run = true
			case "out":
				fi.Out = true
			case "hidden":
				fi.Hidden = true
			case "check":
				fi.Check = true
			}
		default:
			return fi, fmt.Errorf("unknown code attribute %q", key)
		}
	}
	switch {
	case fi.Run && fi.Out:
		return fi, fmt.Errorf("a block is either run or out")
	case fi.Check && !fi.Run:
		return fi, fmt.Errorf("check belongs on a run block")
	case fi.Present["exit"] && !fi.Run:
		return fi, fmt.Errorf("exit belongs on a run block")
	case fi.Run && fi.File != "":
		return fi, fmt.Errorf("a run block cannot also have file=")
	}
	if fi.Lang == "" {
		switch {
		case fi.Run:
			fi.Lang = "bash"
		case fi.Out:
			fi.Lang = "text"
		}
	}
	return fi, nil
}

// parseLineSet parses "2-3,7" into a set of line numbers.
func parseLineSet(s string) (map[int]bool, error) {
	set := map[int]bool{}
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("empty")
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil || a < 1 {
			return nil, fmt.Errorf("%q is not a line number", lo)
		}
		b := a
		if isRange {
			b, err = strconv.Atoi(strings.TrimSpace(hi))
			if err != nil || b < a {
				return nil, fmt.Errorf("%q is not a line range", part)
			}
		}
		for n := a; n <= b; n++ {
			set[n] = true
		}
	}
	return set, nil
}

// codeText strips the trailing newlines of a block.
func codeText(code string) string { return strings.TrimRight(code, "\r\n") }

// renderCode renders a block as <figure class="code">.
func renderCode(fi fenceInfo, code string) (string, error) {
	code = codeText(code)
	lang := canonLang(fi.Lang)
	lines := splitLines(highlight(fi.Lang, code))
	if fi.HL != nil {
		var bad []int
		for n := range fi.HL {
			if n > len(lines) {
				bad = append(bad, n)
			}
		}
		if len(bad) > 0 {
			sort.Ints(bad)
			return "", fmt.Errorf("hl: line %d is past the end of the block (%d lines)", bad[0], len(lines))
		}
	}
	title := fi.Title
	if title == "" {
		title = fi.File
	}
	var b strings.Builder
	b.WriteString(`<figure class="code" data-lang="` + html.EscapeString(lang) + `"`)
	if fi.Status != "" {
		b.WriteString(` data-status="` + fi.Status + `"`)
	}
	if fi.LN {
		b.WriteString(` data-ln`)
	}
	b.WriteString(">\n")
	if title != "" || fi.Status != "" {
		b.WriteString(`<figcaption class="code-title">`)
		if title != "" {
			b.WriteString(`<span class="code-file">` + html.EscapeString(title) + `</span>`)
		}
		if fi.Status != "" {
			sb := statusBadges[fi.Status]
			b.WriteString(`<span class="code-badge" title="` + html.EscapeString(sb.title) + `">` + sb.text + `</span>`)
		}
		b.WriteString("</figcaption>\n")
	}
	b.WriteString(`<div class="code-body"><pre><code>`)
	writeLines(&b, lines, fi.HL, lang == "diff")
	b.WriteString("</code></pre></div>\n")
	b.WriteString(`<button class="copy" type="button" aria-label="Copy ` + html.EscapeString(copyName(title, lang)) + `"></button>` + "\n")
	b.WriteString("</figure>")
	return b.String(), nil
}

// copyName names what the copy button of a code block copies: its title or
// file name, else the language.
func copyName(title, lang string) string {
	if title != "" {
		return title
	}
	if lang != "" && lang != "text" {
		return lang + " code"
	}
	return "code"
}

func commandWord(n int) string {
	if n == 1 {
		return "command"
	}
	return "commands"
}

// writeLines writes the <span class="line"> elements of a code block.
func writeLines(b *strings.Builder, lines [][]tok, hl map[int]bool, diff bool) {
	for i, line := range lines {
		cls := "line"
		if hl[i+1] {
			cls += " hl"
		}
		if diff {
			var text strings.Builder
			for _, t := range line {
				text.WriteString(t.text)
			}
			if c := diffLineClass(text.String()); c != "" {
				cls += " " + c
			}
		}
		b.WriteString(`<span class="` + cls + `"><span class="ln"></span>`)
		for _, t := range line {
			if t.cls == "" {
				b.WriteString(html.EscapeString(t.text))
			} else {
				b.WriteString(`<span class="tok-` + t.cls + `">` + html.EscapeString(t.text) + `</span>`)
			}
		}
		b.WriteString("\n</span>")
	}
}

// commands splits the text of a run block into commands. A line that ends in a
// backslash continues the command on the next line.
func commands(code string) []string {
	var cmds []string
	var cur []string
	for _, line := range strings.Split(codeText(code), "\n") {
		if len(cur) == 0 && strings.TrimSpace(line) == "" {
			continue
		}
		cur = append(cur, line)
		if !strings.HasSuffix(strings.TrimRight(line, " \t"), "\\") {
			cmds = append(cmds, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	if len(cur) > 0 {
		cmds = append(cmds, strings.Join(cur, "\n"))
	}
	return cmds
}

// renderTerminal renders a run block and its output as <figure class="term">.
// ran is the pravo version that produced the output ("0.1"), or empty when the
// page does not run its blocks.
func renderTerminal(fi fenceInfo, code string, out *string, ran string) string {
	title := fi.Title
	if title == "" {
		title = "terminal"
	}
	cmds := commands(code)
	var b strings.Builder
	b.WriteString(`<figure class="term" data-exit="` + strconv.Itoa(fi.Exit) + `"`)
	if fi.Status != "" {
		b.WriteString(` data-status="` + fi.Status + `"`)
	}
	if ran != "" {
		b.WriteString(` data-ran="` + html.EscapeString(ran) + `"`)
	}
	b.WriteString(">\n")
	b.WriteString(`<figcaption class="code-title"><span class="code-file">` + html.EscapeString(title) + `</span>`)
	if fi.Status != "" {
		sb := statusBadges[fi.Status]
		b.WriteString(`<span class="code-badge" title="` + html.EscapeString(sb.title) + `">` + sb.text + `</span>`)
	}
	if fi.Exit != 0 {
		b.WriteString(`<span class="code-badge" data-exit="` + strconv.Itoa(fi.Exit) + `" title="Exit code">exit ` + strconv.Itoa(fi.Exit) + `</span>`)
	}
	b.WriteString("</figcaption>\n")
	b.WriteString(`<div class="term-body">` + "\n")
	for _, c := range cmds {
		b.WriteString(`<div class="term-line cmd"><span class="prompt">$</span><span class="cmd-text">` + html.EscapeString(c) + `</span></div>` + "\n")
	}
	if out != nil && strings.TrimSpace(*out) != "" {
		b.WriteString(`<pre class="term-out"><code>` + colorizeOutput(*out) + "</code></pre>\n")
	}
	b.WriteString("</div>\n")
	copyText := strings.ReplaceAll(html.EscapeString(strings.Join(cmds, "\n")), "\n", "&#10;")
	b.WriteString(`<button class="copy" type="button" data-copy="` + copyText + `" aria-label="Copy ` + commandWord(len(cmds)) + `">Copy</button>` + "\n")
	b.WriteString("</figure>")
	return b.String()
}
