package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// A Block is one ```hcl fence of a Markdown page.
type Block struct {
	Page    string // path of the page, as given on the command line
	N       int    // 1-based position among the hcl blocks of the page
	Line    int    // line of the opening fence
	Body    string
	Attrs   map[string]string // key=value tokens of the info string
	Path    string            // file the block belongs to, relative to the page root
	Excerpt bool              // the title says "lines A-B": the block is part of Path
	Files   []string          // every file the title names; more than one is not checkable
	Expect  string            // "" or "error"
	Match   string            // substring the expected error must contain
	Skip    string            // reason the block is not checked
}

var (
	fenceOpen   = regexp.MustCompile("^( {0,3})(`{3,}|~{3,})(.*)$")
	markerLine  = regexp.MustCompile(`^<!--\s*hclcheck:?\s*(.*?)\s*-->\s*$`)
	linesRe     = regexp.MustCompile(`^lines?\s+\d+(\s*-\s*\d+)?$`)
	hclFileExts = []string{".tf", ".tfvars", ".tf.json", ".tfvars.json"}
)

// splitInfo splits an info string into tokens. Single and double quotes group
// words and are removed.
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

// extract returns the hcl fences of a page. Fences follow CommonMark: three or
// more backticks or tildes, indented by at most three spaces, closed by a fence
// of the same character that is at least as long; a longer fence that holds a
// shorter one shows it as text.
func extract(page, src string) ([]Block, error) {
	lines := strings.Split(src, "\n")
	var blocks []Block
	i := 0
	// Front matter.
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for j := 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "---" {
				i = j + 1
				break
			}
		}
	}
	for ; i < len(lines); i++ {
		m := fenceOpen.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		indent, fence, info := len(m[1]), m[2], strings.TrimSpace(m[3])
		if fence[0] == '`' && strings.Contains(info, "`") {
			continue
		}
		open := i
		var body []string
		j := i + 1
		for ; j < len(lines); j++ {
			l := lines[j]
			t := strings.TrimLeft(l, " ")
			if len(l)-len(t) <= 3 && strings.HasPrefix(t, fence[:1]) {
				n := 0
				for n < len(t) && t[n] == fence[0] {
					n++
				}
				if n >= len(fence) && strings.TrimSpace(t[n:]) == "" {
					break
				}
			}
			body = append(body, trimIndent(l, indent))
		}
		i = j
		toks, err := splitInfo(info)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %v", page, open+1, err)
		}
		if len(toks) == 0 || toks[0] != "hcl" {
			continue
		}
		b := Block{Page: page, N: len(blocks) + 1, Line: open + 1, Body: strings.Join(body, "\n") + "\n", Attrs: map[string]string{}}
		for _, t := range toks[1:] {
			k, v, _ := strings.Cut(t, "=")
			b.Attrs[k] = v
		}
		if err := b.configure(markerAbove(lines, open)); err != nil {
			return nil, fmt.Errorf("%s:%d: %v", page, open+1, err)
		}
		blocks = append(blocks, b)
	}
	return blocks, nil
}

func trimIndent(l string, n int) string {
	k := 0
	for k < n && k < len(l) && l[k] == ' ' {
		k++
	}
	return l[k:]
}

// markerAbove returns the tokens of an `<!-- hclcheck: ... -->` comment on the
// nearest non-blank line above line open, or nil.
func markerAbove(lines []string, open int) []string {
	for k := open - 1; k >= 0; k-- {
		if strings.TrimSpace(lines[k]) == "" {
			continue
		}
		m := markerLine.FindStringSubmatch(strings.TrimSpace(lines[k]))
		if m == nil {
			return nil
		}
		toks, _ := splitInfo(m[1])
		return toks
	}
	return nil
}

// configure reads the markers (from the info string and from the comment above
// the fence) and the title.
func (b *Block) configure(comment []string) error {
	for _, t := range comment {
		k, v, _ := strings.Cut(t, "=")
		b.Attrs[k] = v
	}
	switch v := b.Attrs["expect"]; v {
	case "", "error":
		b.Expect = v
	default:
		return fmt.Errorf("expect=%q: want expect=error", v)
	}
	b.Match = b.Attrs["match"]
	b.Skip = b.Attrs["skip"]
	if _, ok := b.Attrs["skip"]; ok && b.Skip == "" {
		return fmt.Errorf("skip needs a reason: skip=REASON")
	}
	if b.Match != "" && b.Expect == "" {
		return fmt.Errorf("match=TEXT needs expect=error")
	}
	b.Path, b.Excerpt, b.Files = pathOf(b.Attrs["file"], b.Attrs["title"])
	return nil
}

// pathOf derives the file a block belongs to from its file= attribute or its
// title. A title is a comma-separated list: file names, and "lines A-B" when
// the block shows part of a file. A block without a name is main.tf.
func pathOf(file, title string) (path string, excerpt bool, files []string) {
	if file != "" {
		files = append(files, file)
	}
	for _, part := range strings.Split(title, ",") {
		part = strings.TrimSpace(part)
		switch {
		case linesRe.MatchString(part):
			excerpt = true
		case isHCLFile(part) && file == "":
			files = append(files, part)
		}
	}
	if len(files) == 0 {
		return "main.tf", excerpt, []string{"main.tf"}
	}
	return files[0], excerpt, files
}

func isHCLFile(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t") {
		return false
	}
	for _, e := range hclFileExts {
		if strings.HasSuffix(s, e) {
			return true
		}
	}
	return false
}

// label is the block column of the table: "#2 l.241".
func (b *Block) label() string { return "#" + strconv.Itoa(b.N) + " l." + strconv.Itoa(b.Line) }
