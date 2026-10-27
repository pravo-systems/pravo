package main

import (
	"regexp"
	"strings"
)

// The scanner understands as much HCL as the completion step needs: where
// strings and comments are, where a block ends, and which names a body sets.
// It is not a parser. tofu does the parsing.

// skipString returns the index just after the string that starts at s[i] (a
// double quote), following escapes and ${ } interpolations, which may hold
// strings of their own.
func skipString(s string, i int) int {
	i++ // opening quote
	for i < len(s) {
		switch s[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1
		case '$', '%':
			if i+2 < len(s) && s[i+1] == s[i] && s[i+2] == '{' { // $${ and %%{ are literal
				i += 3
				continue
			}
			if i+1 < len(s) && s[i+1] == '{' {
				i = skipBraces(s, i+1)
				continue
			}
			i++
		default:
			i++
		}
	}
	return i
}

// skipBraces returns the index just after the brace that closes the one at
// s[i].
func skipBraces(s string, i int) int {
	depth := 0
	for i < len(s) {
		switch s[i] {
		case '"':
			i = skipString(s, i)
			continue
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
		i++
	}
	return i
}

// stripComments blanks out # // and /* */ comments outside strings. Newlines
// stay, so line numbers do not move.
func stripComments(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); {
		switch {
		case b[i] == '"':
			i = skipString(s, i)
		case b[i] == '#' || (b[i] == '/' && i+1 < len(b) && b[i+1] == '/'):
			for i < len(b) && b[i] != '\n' {
				b[i] = ' '
				i++
			}
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '*':
			for i < len(b) && (b[i] != '*' || i+1 >= len(b) || b[i+1] != '/') {
				if b[i] != '\n' {
					b[i] = ' '
				}
				i++
			}
			if i+1 < len(b) {
				b[i], b[i+1] = ' ', ' '
				i += 2
			}
		default:
			i++
		}
	}
	return string(b)
}

// A hclBlock is a block at the top of a body: `type "label" "label" { body }`.
type hclBlock struct {
	Type   string
	Labels []string
	Body   string
}

var blockHead = regexp.MustCompile(`^[ \t]*([A-Za-z_][\w-]*)((?:[ \t]+(?:"[^"\n]*"|[A-Za-z_][\w-]*))*)[ \t]*\{`)
var labelRe = regexp.MustCompile(`"([^"]*)"|([A-Za-z_][\w-]*)`)

// blocks returns the blocks at the top level of a comment-free body.
func blocks(src string) []hclBlock {
	var out []hclBlock
	i := 0
	for i < len(src) {
		// Start of a line at depth 0.
		if m := blockHead.FindStringSubmatchIndex(src[i:]); m != nil {
			head := blockHead.FindStringSubmatch(src[i:])
			open := i + m[1] - 1
			end := skipBraces(src, open)
			b := hclBlock{Type: head[1]}
			for _, l := range labelRe.FindAllStringSubmatch(head[2], -1) {
				if strings.HasPrefix(l[0], `"`) {
					b.Labels = append(b.Labels, l[1])
				} else {
					b.Labels = append(b.Labels, l[2])
				}
			}
			b.Body = src[open+1 : max(open+1, end-1)]
			out = append(out, b)
			i = end
			continue
		}
		// Not a block head: skip the logical line, which may span brackets.
		i = skipLogicalLine(src, i)
	}
	return out
}

// skipLogicalLine returns the index of the start of the next top-level line:
// it goes over brackets and strings that span lines.
func skipLogicalLine(s string, i int) int {
	depth := 0
	for i < len(s) {
		switch s[i] {
		case '"':
			i = skipString(s, i)
			continue
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			depth--
		case '\n':
			if depth <= 0 {
				return i + 1
			}
		}
		i++
	}
	return i
}

var identEq = regexp.MustCompile(`^([A-Za-z_][\w-]*)[ \t]*=([^=>]|$)`)

// attrs returns the names of the attributes set at the top level of a
// comment-free body, in order.
func attrs(body string) []string {
	var out []string
	i := 0
	for i < len(body) {
		// Skip blanks at the start of a logical line.
		for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '\r' || body[i] == ',') {
			i++
		}
		if i >= len(body) {
			break
		}
		if m := identEq.FindStringSubmatch(body[i:]); m != nil {
			out = append(out, m[1])
		}
		i = skipLogicalLine(body, i)
	}
	return out
}

// attrValue returns the text after `name =` for an attribute set at the top
// level of a comment-free body, up to the end of its logical line.
func attrValue(body, name string) (string, bool) {
	i := 0
	for i < len(body) {
		for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '\r') {
			i++
		}
		if i >= len(body) {
			break
		}
		if m := identEq.FindStringSubmatch(body[i:]); m != nil && m[1] == name {
			j := skipLogicalLine(body, i)
			v := body[i:j]
			_, v, _ = strings.Cut(v, "=")
			return strings.TrimSpace(v), true
		}
		i = skipLogicalLine(body, i)
	}
	return "", false
}
