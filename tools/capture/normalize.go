package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// pathPair replaces an absolute path prefix by a stable token.
type pathPair struct{ from, to string }

// normalizer turns raw command output into the text that is compared with
// an out block and written to one.
type normalizer struct {
	pairs []pathPair
	rules []rule
	ports []pathPair // port numbers of services on a free port, and their tokens
}

var (
	reCSI = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	reOSC = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
)

// newNormalizer returns a normalizer for one page. The home and temporary
// directories of the caller become "~" and "<tmp>"; workspaces added later
// become ".".
func newNormalizer(home string, tmpDirs []string, rules []rule) *normalizer {
	n := &normalizer{rules: rules}
	if len(home) > 1 {
		n.add(home, "~")
	}
	for _, t := range tmpDirs {
		if len(t) > 1 {
			n.add(t, "<tmp>")
		}
	}
	return n
}

// add registers dir, and its symbolic-link-free form, for replacement by to.
func (n *normalizer) add(dir, to string) {
	dir = strings.TrimRight(dir, "/")
	if dir == "" {
		return
	}
	seen := func(d string) bool {
		for _, p := range n.pairs {
			if p.from == d {
				return true
			}
		}
		return false
	}
	for _, d := range []string{dir, realPath(dir)} {
		if d != "" && !seen(d) {
			n.pairs = append(n.pairs, pathPair{d, to})
		}
	}
	sort.SliceStable(n.pairs, func(i, j int) bool { return len(n.pairs[i].from) > len(n.pairs[j].from) })
}

// addPort registers the port that a service was given by the system, so that
// the number, which differs between runs, becomes the token <NAME-port>. A
// port that a page names itself is not registered: the page shows it.
func (n *normalizer) addPort(name string, port int) {
	n.ports = append(n.ports, pathPair{strconv.Itoa(port), "<" + name + "-port>"})
}

// replacePorts replaces each registered port where it stands as a whole number.
func (n *normalizer) replacePorts(s string) string {
	for _, p := range n.ports {
		if !strings.Contains(s, p.from) {
			continue
		}
		var sb strings.Builder
		for i := 0; i < len(s); {
			if strings.HasPrefix(s[i:], p.from) &&
				(i == 0 || !digit(s[i-1])) &&
				(i+len(p.from) == len(s) || !digit(s[i+len(p.from)])) {
				sb.WriteString(p.to)
				i += len(p.from)
				continue
			}
			sb.WriteByte(s[i])
			i++
		}
		s = sb.String()
	}
	return s
}

func digit(c byte) bool { return c >= '0' && c <= '9' }

// addWorkspace registers a directory that commands run in; its path becomes ".".
func (n *normalizer) addWorkspace(dir string) { n.add(dir, ".") }

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// replacePaths replaces absolute path prefixes in one left-to-right pass. A
// prefix matches only as a whole path: the character after it must not
// continue a name, the one before it must not be part of a longer path.
func (n *normalizer) replacePaths(s string) string {
	if len(n.pairs) == 0 || !strings.Contains(s, "/") {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '/' && (i == 0 || !pathBefore(s[i-1])) {
			matched := false
			for _, p := range n.pairs {
				if strings.HasPrefix(s[i:], p.from) && (i+len(p.from) == len(s) || !pathAfter(s[i+len(p.from)])) {
					sb.WriteString(p.to)
					i += len(p.from)
					matched = true
					break
				}
			}
			if matched {
				continue
			}
		}
		sb.WriteByte(s[i])
		i++
	}
	return sb.String()
}

func pathBefore(c byte) bool {
	return c == '/' || c == '.' || c == '_' || c == '-' || c == '~' || alnum(c)
}

func pathAfter(c byte) bool { return c == '_' || c == '-' || alnum(c) }

func alnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// lines normalises raw output: line endings, terminal escapes, paths, the
// page's rules, trailing whitespace and trailing blank lines.
func (n *normalizer) lines(raw []byte) []string {
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	s = reOSC.ReplaceAllString(s, "")
	s = reCSI.ReplaceAllString(s, "")
	s = n.replacePaths(s)
	s = n.replacePorts(s)
	for _, r := range n.rules {
		s = r.re.ReplaceAllString(s, r.Replace)
	}
	return trimLines(strings.Split(s, "\n"))
}

// trimLines removes trailing whitespace from every line, and blank lines
// from the start and the end of the list.
func trimLines(in []string) []string {
	out := make([]string, len(in))
	for i, l := range in {
		out[i] = strings.TrimRight(l, " \t\r")
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// elision is the line that stands for any number of lines in an out block.
const elision = "..."

// segments splits expected lines at the elision lines. A block without
// elision has one segment.
func segments(exp []string) [][]string {
	segs := [][]string{nil}
	for _, l := range exp {
		if l == elision {
			segs = append(segs, nil)
			continue
		}
		segs[len(segs)-1] = append(segs[len(segs)-1], l)
	}
	return segs
}

// mismatch describes the first way in which output differs from an out block.
type mismatch struct {
	// ExpLine and ActLine are 0-based indexes of the lines that differ in the
	// out block and in the output, or -1 when there is no such line.
	ExpLine, ActLine int
	NoActual         bool // the difference has no single line in the output
	Msg              string
}

// matchElided compares output lines with the lines of an out block. The
// first segment must start the output, the last must end it, and the ones in
// between must appear in order. It returns nil when they agree.
func matchElided(exp, act []string) *mismatch {
	segs := segments(exp)
	if len(segs) == 1 {
		return matchExact(exp, act)
	}
	first, last, mids := segs[0], segs[len(segs)-1], segs[1:len(segs)-1]
	if len(act) < len(first)+len(last) {
		return &mismatch{ExpLine: -1, ActLine: -1,
			Msg: fmt.Sprintf("the output has %d lines, the out block needs at least %d around its '...'", len(act), len(first)+len(last))}
	}
	for i, l := range first {
		if act[i] != l {
			return &mismatch{ExpLine: i, ActLine: i, Msg: fmt.Sprintf("output line %d differs", i+1)}
		}
	}
	tail := len(act) - len(last)
	expTail := len(exp) - len(last)
	for j, l := range last {
		if act[tail+j] != l {
			return &mismatch{ExpLine: expTail + j, ActLine: tail + j,
				Msg: fmt.Sprintf("output line %d differs (the lines after '...' must end the output)", tail+j+1)}
		}
	}
	pos := len(first)
	expPos := len(first) + 1
	for _, seg := range mids {
		if len(seg) == 0 {
			expPos++
			continue
		}
		at := indexSeq(act[pos:tail], seg)
		if at < 0 {
			return &mismatch{ExpLine: expPos, ActLine: -1, NoActual: true,
				Msg: fmt.Sprintf("lines %d to %d of the out block are not found, in order, after output line %d", expPos+1, expPos+len(seg), pos)}
		}
		pos += at + len(seg)
		expPos += len(seg) + 1
	}
	return nil
}

func matchExact(exp, act []string) *mismatch {
	for i := 0; i < len(exp) || i < len(act); i++ {
		switch {
		case i >= len(act):
			return &mismatch{ExpLine: i, ActLine: -1, Msg: fmt.Sprintf("the output ends after line %d; the out block has more", len(act))}
		case i >= len(exp):
			return &mismatch{ExpLine: -1, ActLine: i, Msg: fmt.Sprintf("the output has more lines than the out block, from output line %d", i+1)}
		case exp[i] != act[i]:
			return &mismatch{ExpLine: i, ActLine: i, Msg: fmt.Sprintf("output line %d differs", i+1)}
		}
	}
	return nil
}

// indexSeq returns the first index at which seg occurs in s, or -1.
func indexSeq(s, seg []string) int {
	for i := 0; i+len(seg) <= len(s); i++ {
		ok := true
		for j := range seg {
			if s[i+j] != seg[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// rewrite returns the lines an out block should hold for output act. When
// the old block has one elision and the output is still longer than the
// lines kept around it, the same numbers of leading and trailing lines are
// kept; otherwise the whole output is written.
func rewrite(old, act []string) []string {
	segs := segments(old)
	if len(segs) == 2 {
		head, tail := len(segs[0]), len(segs[1])
		if len(act) > head+tail {
			out := append([]string{}, act[:head]...)
			out = append(out, elision)
			return append(out, act[len(act)-tail:]...)
		}
	}
	return act
}

// diffLine is one line of a line diff.
type diffLine struct {
	Op   byte // ' ', '-' (expected only), '+' (actual only)
	Text string
}

// lineDiff computes a minimal line diff from exp to act.
func lineDiff(exp, act []string) []diffLine {
	pre := 0
	for pre < len(exp) && pre < len(act) && exp[pre] == act[pre] {
		pre++
	}
	suf := 0
	for suf < len(exp)-pre && suf < len(act)-pre && exp[len(exp)-1-suf] == act[len(act)-1-suf] {
		suf++
	}
	var out []diffLine
	for _, l := range exp[:pre] {
		out = append(out, diffLine{' ', l})
	}
	a, b := exp[pre:len(exp)-suf], act[pre:len(act)-suf]
	if len(a)*len(b) > 4_000_000 {
		for _, l := range a {
			out = append(out, diffLine{'-', l})
		}
		for _, l := range b {
			out = append(out, diffLine{'+', l})
		}
	} else {
		out = append(out, lcsDiff(a, b)...)
	}
	for _, l := range exp[len(exp)-suf:] {
		out = append(out, diffLine{' ', l})
	}
	return out
}

func lcsDiff(a, b []string) []diffLine {
	n, m := len(a), len(b)
	t := make([][]int32, n+1)
	for i := range t {
		t[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	var out []diffLine
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, diffLine{' ', a[i]})
			i++
			j++
		case t[i+1][j] >= t[i][j+1]:
			out = append(out, diffLine{'-', a[i]})
			i++
		default:
			out = append(out, diffLine{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, diffLine{'-', a[i]})
	}
	for ; j < m; j++ {
		out = append(out, diffLine{'+', b[j]})
	}
	return out
}

// renderDiff formats d with context lines around the changes and a note for
// each run of unchanged lines that is left out.
func renderDiff(d []diffLine, context int) []string {
	keep := make([]bool, len(d))
	for i, l := range d {
		if l.Op == ' ' {
			continue
		}
		for k := max(0, i-context); k <= min(len(d)-1, i+context); k++ {
			keep[k] = true
		}
	}
	var out []string
	skipped := 0
	flush := func() {
		if skipped > 0 {
			out = append(out, fmt.Sprintf("  ... %s unchanged", plural(skipped, "line")))
			skipped = 0
		}
	}
	for i, l := range d {
		if !keep[i] {
			skipped++
			continue
		}
		flush()
		out = append(out, strings.TrimRight(fmt.Sprintf("%c %s", l.Op, l.Text), " "))
	}
	flush()
	return out
}
