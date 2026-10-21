package main

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// tok is a run of source text with a highlight class. The classes are the
// suffixes of the tok-* CSS classes: kw, str, num, com, id, op, def, type, fn.
// The empty class is plain text.
type tok struct {
	cls  string
	text string
}

// langAliases maps the names authors write to canonical language names.
var langAliases = map[string]string{
	"sh": "bash", "shell": "bash", "zsh": "bash", "bash": "bash",
	"terraform": "hcl", "tf": "hcl", "tofu": "hcl", "hcl": "hcl",
	"yml": "yaml", "yaml": "yaml",
	"txt": "text", "text": "text", "plain": "text", "plaintext": "text", "output": "text", "none": "text", "": "text",
	"cue": "cue", "json": "json", "diff": "diff", "patch": "diff",
}

// canonLang returns the canonical language name for an info-string language.
func canonLang(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	if c, ok := langAliases[l]; ok {
		return c
	}
	return l
}

// highlight tokenises code in the given language. Unknown languages come back
// as plain text.
func highlight(lang, code string) []tok {
	switch canonLang(lang) {
	case "text":
		return []tok{{"", code}}
	case "cue":
		return lexCUE(code)
	case "hcl":
		return lexHCL(code)
	case "diff":
		return lexDiff(code)
	}
	lx := lexers.Get(lang)
	if lx == nil {
		return []tok{{"", code}}
	}
	it, err := lx.Tokenise(nil, code)
	if err != nil {
		return []tok{{"", code}}
	}
	canon := canonLang(lang)
	var out []tok
	for t := it(); t != chroma.EOF; t = it() {
		cls := chromaClass(t.Type)
		if cls == "op" && canon == "bash" {
			cls = "" // shell operators are punctuation, not a reading aid
		}
		out = appendTok(out, cls, t.Value)
	}
	return out
}

// chromaClass maps a chroma token type to a tok-* class.
func chromaClass(t chroma.TokenType) string {
	switch {
	case t.InCategory(chroma.Comment):
		return "com"
	case t.InSubCategory(chroma.LiteralString):
		return "str"
	case t.InSubCategory(chroma.LiteralNumber):
		return "num"
	case t == chroma.KeywordType:
		return "type"
	case t == chroma.KeywordConstant:
		return "num"
	case t.InCategory(chroma.Keyword):
		return "kw"
	case t == chroma.NameFunction || t == chroma.NameBuiltin || t == chroma.NameDecorator || t == chroma.NameFunctionMagic:
		return "fn"
	case t == chroma.NameClass || t == chroma.NameNamespace || t == chroma.NameConstant:
		return "def"
	case t == chroma.NameTag || t == chroma.NameAttribute || t == chroma.NameVariable:
		return "id"
	case t == chroma.Operator || t == chroma.OperatorWord:
		return "op"
	}
	return ""
}

// appendTok appends a token, merging it into the previous one when the class
// is the same.
func appendTok(out []tok, cls, text string) []tok {
	if text == "" {
		return out
	}
	if n := len(out); n > 0 && out[n-1].cls == cls {
		out[n-1].text += text
		return out
	}
	return append(out, tok{cls, text})
}

// splitLines splits tokens at newlines. The result has one entry per line of
// the source; a source ending in a newline does not produce a trailing empty
// line.
func splitLines(toks []tok) [][]tok {
	var lines [][]tok
	var cur []tok
	for _, t := range toks {
		parts := strings.Split(t.text, "\n")
		for i, part := range parts {
			if i > 0 {
				lines = append(lines, cur)
				cur = nil
			}
			if part != "" {
				cur = append(cur, tok{t.cls, part})
			}
		}
	}
	if len(cur) > 0 || len(lines) == 0 {
		lines = append(lines, cur)
	}
	return lines
}

// ---------------------------------------------------------------------------
// Shared scanner helpers

type scanner struct {
	s   string
	i   int
	out []tok
}

func (sc *scanner) eof() bool { return sc.i >= len(sc.s) }

func (sc *scanner) peek(off int) byte {
	if sc.i+off < len(sc.s) {
		return sc.s[sc.i+off]
	}
	return 0
}

func (sc *scanner) hasPrefix(p string) bool { return strings.HasPrefix(sc.s[sc.i:], p) }

func (sc *scanner) emit(cls string, n int) {
	sc.out = appendTok(sc.out, cls, sc.s[sc.i:sc.i+n])
	sc.i += n
}

func (sc *scanner) emitText(cls, text string) { sc.out = appendTok(sc.out, cls, text) }

func isIdentStart(r rune) bool { return r == '_' || r == '$' || unicode.IsLetter(r) }
func isIdentPart(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// identLen returns the byte length of the identifier starting at i, or 0.
func identLen(s string, i int) int {
	r, w := utf8.DecodeRuneInString(s[i:])
	if w == 0 || !isIdentStart(r) {
		return 0
	}
	n := w
	for i+n < len(s) {
		r, w = utf8.DecodeRuneInString(s[i+n:])
		if !isIdentPart(r) {
			break
		}
		n += w
	}
	return n
}

// skipSpaces returns the index of the first byte at or after i that is not a
// space or tab.
func skipSpaces(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// numberLen returns the length of a numeric literal at the start of s (CUE
// and HCL share the shapes), or 0.
func numberLen(s string, cue bool) int {
	if len(s) == 0 {
		return 0
	}
	i := 0
	switch {
	case cue && len(s) > 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X'):
		i = 2
		for i < len(s) && (isHex(s[i]) || s[i] == '_') {
			i++
		}
		return i
	case cue && len(s) > 2 && s[0] == '0' && (s[1] == 'b' || s[1] == 'B' || s[1] == 'o' || s[1] == 'O'):
		i = 2
		for i < len(s) && (isDigit(s[i]) || s[i] == '_') {
			i++
		}
		return i
	}
	if s[0] == '.' {
		if len(s) < 2 || !isDigit(s[1]) {
			return 0
		}
		i = 1
	} else if !isDigit(s[0]) {
		return 0
	}
	for i < len(s) && (isDigit(s[i]) || (cue && s[i] == '_')) {
		i++
	}
	if i < len(s) && s[i] == '.' && i+1 < len(s) && isDigit(s[i+1]) {
		i++
		for i < len(s) && (isDigit(s[i]) || (cue && s[i] == '_')) {
			i++
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && isDigit(s[j]) {
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			i = j
		}
	}
	if cue && i < len(s) && strings.IndexByte("KMGTP", s[i]) >= 0 {
		i++
		if i < len(s) && s[i] == 'i' {
			i++
		}
	}
	return i
}

func isHex(b byte) bool {
	return isDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// ---------------------------------------------------------------------------
// CUE

var cueKeywords = map[string]bool{
	"package": true, "import": true, "for": true, "in": true, "if": true, "let": true,
}

var cueTypes = map[string]bool{
	"string": true, "int": true, "bool": true, "float": true, "number": true, "bytes": true,
	"int8": true, "int16": true, "int32": true, "int64": true, "int128": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uint128": true,
	"float32": true, "float64": true, "rune": true, "byte": true, "_": true,
}

var cueConstants = map[string]bool{"true": true, "false": true, "null": true}

// lexCUE tokenises CUE source: comments, strings with \( ) interpolation,
// numbers, keywords, definitions (#Name), labels, builtin types, bounds and
// operators, attributes (@tag(...)) and function calls.
func lexCUE(src string) []tok {
	sc := &scanner{s: src}
	sc.cueExpr(false)
	return sc.out
}

// cueExpr scans until the end of input or, inside an interpolation, until the
// parenthesis that closes it (which it leaves unconsumed).
func (sc *scanner) cueExpr(inInterp bool) {
	depth := 0
	for !sc.eof() {
		c := sc.s[sc.i]
		switch {
		case c == '\n' || c == ' ' || c == '\t' || c == '\r':
			sc.emit("", 1)
		case sc.hasPrefix("//"):
			n := strings.IndexByte(sc.s[sc.i:], '\n')
			if n < 0 {
				n = len(sc.s) - sc.i
			}
			sc.emit("com", n)
		case c == '"' || c == '\'' || (c == '#' && sc.rawStringAhead()):
			sc.cueString()
		case c == '@' && identLen(sc.s, sc.i+1) > 0:
			sc.cueAttribute()
		case c == '#' || (c == '_' && sc.peek(1) == '#'):
			j := sc.i + 1
			if c == '_' {
				j++
			}
			n := identLen(sc.s, j)
			if n == 0 {
				sc.emit("op", 1)
				break
			}
			sc.emit("def", j-sc.i+n)
		case isDigit(c) || (c == '.' && numberLen(sc.s[sc.i:], true) > 0 && !sc.prevIsOperand()):
			sc.emit("num", numberLen(sc.s[sc.i:], true))
		case sc.hasPrefix("_|_"):
			sc.emit("type", 3)
		case isIdentStartAt(sc.s, sc.i):
			sc.cueIdent()
		case sc.hasPrefix("..."):
			sc.emit("op", 3)
		case sc.hasPrefix("==") || sc.hasPrefix("!=") || sc.hasPrefix("=~") || sc.hasPrefix("!~") ||
			sc.hasPrefix("<=") || sc.hasPrefix(">=") || sc.hasPrefix("&&") || sc.hasPrefix("||"):
			sc.emit("op", 2)
		case strings.IndexByte("&|*?!<>=+-/%", c) >= 0:
			sc.emit("op", 1)
		case c == '(':
			depth++
			sc.emit("", 1)
		case c == ')':
			if inInterp && depth == 0 {
				return
			}
			if depth > 0 {
				depth--
			}
			sc.emit("", 1)
		default:
			_, w := utf8.DecodeRuneInString(sc.s[sc.i:])
			sc.emit("", w)
		}
	}
}

func isIdentStartAt(s string, i int) bool {
	r, w := utf8.DecodeRuneInString(s[i:])
	return w > 0 && isIdentStart(r)
}

// prevIsOperand reports whether the last emitted non-space token ends an
// operand, so that a following "." is a selector and not the start of ".5".
func (sc *scanner) prevIsOperand() bool {
	for i := len(sc.out) - 1; i >= 0; i-- {
		t := sc.out[i]
		txt := strings.TrimRight(t.text, " \t\n\r")
		if txt == "" {
			continue
		}
		last := txt[len(txt)-1]
		return isDigit(last) || last == ')' || last == ']' || last == '"' || last == '_' || (last >= 'a' && last <= 'z') || (last >= 'A' && last <= 'Z')
	}
	return false
}

// rawStringAhead reports whether the "#" at the cursor starts a raw string
// such as #"..."# or ##'...'##.
func (sc *scanner) rawStringAhead() bool {
	j := sc.i
	for j < len(sc.s) && sc.s[j] == '#' {
		j++
	}
	return j < len(sc.s) && (sc.s[j] == '"' || sc.s[j] == '\'')
}

// cueString scans a string or bytes literal, including raw (#"..."#) and
// multi-line forms, and recurses into \( ) interpolations.
func (sc *scanner) cueString() {
	start := sc.i
	hashes := 0
	for sc.s[sc.i] == '#' {
		hashes++
		sc.i++
	}
	q := sc.s[sc.i]
	quoteN := 1
	if strings.HasPrefix(sc.s[sc.i:], strings.Repeat(string(q), 3)) {
		quoteN = 3
	}
	closer := strings.Repeat(string(q), quoteN) + strings.Repeat("#", hashes)
	sc.i += quoteN
	// text accumulates the string body between interpolations.
	chunkStart := start
	flush := func() {
		if chunkStart < sc.i {
			sc.emitText("str", sc.s[chunkStart:sc.i])
		}
	}
	for !sc.eof() {
		if strings.HasPrefix(sc.s[sc.i:], closer) {
			sc.i += len(closer)
			flush()
			return
		}
		c := sc.s[sc.i]
		if c == '\n' && quoteN == 1 {
			// unterminated single-line string: stop at the line end
			flush()
			return
		}
		if c == '\\' && strings.HasPrefix(sc.s[sc.i+1:], strings.Repeat("#", hashes)) {
			after := sc.i + 1 + hashes
			if after < len(sc.s) && sc.s[after] == '(' {
				flush()
				sc.emitText("op", sc.s[sc.i:after+1])
				sc.i = after + 1
				sc.cueExpr(true)
				if !sc.eof() && sc.s[sc.i] == ')' {
					sc.emit("op", 1)
				}
				chunkStart = sc.i
				continue
			}
			// an escape: keep the next character with the backslash
			sc.i = after
			if sc.i < len(sc.s) {
				_, w := utf8.DecodeRuneInString(sc.s[sc.i:])
				sc.i += w
			}
			continue
		}
		_, w := utf8.DecodeRuneInString(sc.s[sc.i:])
		sc.i += w
	}
	flush()
}

// cueAttribute scans @name(...) with balanced parentheses.
func (sc *scanner) cueAttribute() {
	j := sc.i + 1 + identLen(sc.s, sc.i+1)
	if j < len(sc.s) && sc.s[j] == '(' {
		depth := 0
		for k := j; k < len(sc.s); k++ {
			switch sc.s[k] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					sc.emit("fn", k+1-sc.i)
					return
				}
			case '\n':
				k = len(sc.s)
			}
		}
	}
	sc.emit("fn", j-sc.i)
}

func (sc *scanner) cueIdent() {
	n := identLen(sc.s, sc.i)
	word := sc.s[sc.i : sc.i+n]
	after := skipSpaces(sc.s, sc.i+n)
	isLabel := false
	if after < len(sc.s) {
		switch sc.s[after] {
		case ':':
			isLabel = after+1 >= len(sc.s) || sc.s[after+1] != ':'
		case '?', '!':
			j := skipSpaces(sc.s, after+1)
			isLabel = j < len(sc.s) && sc.s[j] == ':'
		}
	}
	switch {
	case isLabel:
		sc.emit("id", n)
	case cueKeywords[word]:
		sc.emit("kw", n)
	case cueConstants[word]:
		sc.emit("num", n)
	case cueTypes[word]:
		sc.emit("type", n)
	default:
		// dotted call: strings.ToUpper(x), list.Sum(x), len(x)
		end := sc.i + n
		for end < len(sc.s) && sc.s[end] == '.' {
			m := identLen(sc.s, end+1)
			if m == 0 {
				break
			}
			end += 1 + m
		}
		if j := skipSpaces(sc.s, end); j < len(sc.s) && sc.s[j] == '(' && !cueKeywords[word] {
			sc.emit("fn", end-sc.i)
			return
		}
		sc.emit("", n)
	}
}

// ---------------------------------------------------------------------------
// HCL

var hclTypes = map[string]bool{
	"string": true, "number": true, "bool": true, "any": true,
}

var hclTemplateKeywords = map[string]bool{
	"for": true, "in": true, "if": true, "else": true, "endif": true, "endfor": true,
}

// lexHCL tokenises HCL: comments (#, //, /* */), strings with ${ } and %{ }
// interpolation, heredocs, numbers, block types, attribute names, function
// calls and the usual operators.
func lexHCL(src string) []tok {
	sc := &scanner{s: src}
	sc.hclExpr(false)
	return sc.out
}

func (sc *scanner) lineStart() bool {
	for i := sc.i - 1; i >= 0; i-- {
		switch sc.s[i] {
		case '\n':
			return true
		case ' ', '\t':
			continue
		default:
			return false
		}
	}
	return true
}

func (sc *scanner) hclExpr(inInterp bool) {
	depth := 0
	for !sc.eof() {
		c := sc.s[sc.i]
		switch {
		case c == '\n' || c == ' ' || c == '\t' || c == '\r':
			sc.emit("", 1)
		case c == '#' || sc.hasPrefix("//"):
			n := strings.IndexByte(sc.s[sc.i:], '\n')
			if n < 0 {
				n = len(sc.s) - sc.i
			}
			sc.emit("com", n)
		case sc.hasPrefix("/*"):
			n := strings.Index(sc.s[sc.i+2:], "*/")
			if n < 0 {
				n = len(sc.s) - sc.i
			} else {
				n += 4
			}
			sc.emit("com", n)
		case c == '"':
			sc.hclString()
		case sc.hasPrefix("<<") && sc.heredocAhead():
			sc.hclHeredoc()
		case isDigit(c):
			sc.emit("num", numberLen(sc.s[sc.i:], false))
		case isIdentStartAt(sc.s, sc.i):
			sc.hclIdent()
		case sc.hasPrefix("..."):
			sc.emit("op", 3)
		case sc.hasPrefix("=>"):
			sc.emit("op", 2)
		case sc.hasPrefix("==") || sc.hasPrefix("!=") || sc.hasPrefix("<=") || sc.hasPrefix(">=") || sc.hasPrefix("&&") || sc.hasPrefix("||"):
			sc.emit("op", 2)
		case strings.IndexByte("<>!?&|", c) >= 0:
			sc.emit("op", 1)
		case c == '{':
			depth++
			sc.emit("", 1)
		case c == '}':
			if inInterp && depth == 0 {
				return
			}
			if depth > 0 {
				depth--
			}
			sc.emit("", 1)
		default:
			_, w := utf8.DecodeRuneInString(sc.s[sc.i:])
			sc.emit("", w)
		}
	}
}

func (sc *scanner) heredocAhead() bool {
	j := sc.i + 2
	if j < len(sc.s) && sc.s[j] == '-' {
		j++
	}
	return identLen(sc.s, j) > 0
}

func (sc *scanner) hclHeredoc() {
	j := sc.i + 2
	if sc.s[j] == '-' {
		j++
	}
	n := identLen(sc.s, j)
	tag := sc.s[j : j+n]
	pos := j + n
	nl := strings.IndexByte(sc.s[pos:], '\n')
	if nl < 0 {
		sc.emit("str", len(sc.s)-sc.i)
		return
	}
	pos += nl + 1
	for pos <= len(sc.s) {
		e := strings.IndexByte(sc.s[pos:], '\n')
		line, next := sc.s[pos:], len(sc.s)
		if e >= 0 {
			line, next = sc.s[pos:pos+e], pos+e
		}
		if strings.TrimSpace(line) == tag {
			sc.emit("str", next-sc.i)
			return
		}
		if e < 0 {
			break
		}
		pos = next + 1
	}
	sc.emit("str", len(sc.s)-sc.i)
}

func (sc *scanner) hclString() {
	chunkStart := sc.i
	sc.i++
	flush := func() {
		if chunkStart < sc.i {
			sc.emitText("str", sc.s[chunkStart:sc.i])
		}
	}
	for !sc.eof() {
		c := sc.s[sc.i]
		switch {
		case c == '"':
			sc.i++
			flush()
			return
		case c == '\n':
			flush()
			return
		case c == '\\':
			sc.i++
			if !sc.eof() {
				_, w := utf8.DecodeRuneInString(sc.s[sc.i:])
				sc.i += w
			}
		case (c == '$' || c == '%') && sc.peek(1) == '{':
			// "$${" and "%%{" are escaped literals
			if sc.i-1 > chunkStart && sc.s[sc.i-1] == c {
				sc.i++
				continue
			}
			flush()
			sc.emitText("op", sc.s[sc.i:sc.i+2])
			sc.i += 2
			sc.hclExpr(true)
			if !sc.eof() && sc.s[sc.i] == '}' {
				sc.emit("op", 1)
			}
			chunkStart = sc.i
		default:
			_, w := utf8.DecodeRuneInString(sc.s[sc.i:])
			sc.i += w
		}
	}
	flush()
}

func (sc *scanner) hclIdent() {
	n := identLen(sc.s, sc.i)
	// identifiers in HCL may contain hyphens
	for sc.i+n+1 < len(sc.s) && sc.s[sc.i+n] == '-' && isIdentStartAt(sc.s, sc.i+n+1) {
		m := identLen(sc.s, sc.i+n+1)
		n += 1 + m
	}
	word := sc.s[sc.i : sc.i+n]
	after := skipSpaces(sc.s, sc.i+n)
	var next byte
	if after < len(sc.s) {
		next = sc.s[after]
	}
	switch {
	case cueConstants[word]:
		sc.emit("num", n)
	case hclTypes[word]:
		sc.emit("type", n)
	case next == '=' && (after+1 >= len(sc.s) || (sc.s[after+1] != '=' && sc.s[after+1] != '~' && sc.s[after+1] != '>')):
		sc.emit("id", n)
	case next == '(':
		sc.emit("fn", n)
	case hclTemplateKeywords[word]:
		sc.emit("kw", n)
	case sc.lineStart() && sc.blockAhead(after):
		sc.emit("kw", n)
	default:
		sc.emit("", n)
	}
}

// blockAhead reports whether the text from i is block syntax: any number of
// quoted or bare labels followed by "{".
func (sc *scanner) blockAhead(i int) bool {
	for i < len(sc.s) {
		i = skipSpaces(sc.s, i)
		if i >= len(sc.s) {
			return false
		}
		switch c := sc.s[i]; {
		case c == '{':
			return true
		case c == '"':
			j := i + 1
			for j < len(sc.s) && sc.s[j] != '"' && sc.s[j] != '\n' {
				if sc.s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(sc.s) || sc.s[j] != '"' {
				return false
			}
			i = j + 1
		case isIdentStartAt(sc.s, i):
			i += identLen(sc.s, i)
		default:
			return false
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Diff

// lexDiff tokenises unified diffs line by line: headers and hunk markers are
// comments. Added and removed lines are classified by the caller through
// diffLineClass.
func lexDiff(src string) []tok {
	var out []tok
	for _, line := range strings.SplitAfter(src, "\n") {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "@@"), strings.HasPrefix(line, "diff "), strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
			out = appendTok(out, "com", line)
		default:
			out = appendTok(out, "", line)
		}
	}
	return out
}

// diffLineClass returns "ins" or "del" for added and removed lines.
func diffLineClass(line string) string {
	switch {
	case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "):
		return ""
	case strings.HasPrefix(line, "+"):
		return "ins"
	case strings.HasPrefix(line, "-"):
		return "del"
	}
	return ""
}
