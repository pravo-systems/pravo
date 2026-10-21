package main

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/parser"
	gtext "github.com/yuin/goldmark/text"
)

// issue is a problem in a page's source: an error stops the page from
// running, a warning is reported and the page still runs.
type issue struct {
	Line int // 1-based
	Msg  string
}

// rule is one front matter normalisation rule.
type rule struct {
	Pattern string
	Replace string
	re      *regexp.Regexp
}

// envVar is one entry of the env map of a page.
type envVar struct {
	Name  string
	Value string
}

// frontMatter holds the part of a page's front matter that capture reads.
type frontMatter struct {
	Title     string
	Capture   bool
	Normalize []rule
	Services  []serviceSpec // services started for the page, in order
	Env       []envVar      // variables set for every command, sorted by name
}

// attr is one token of a fence info string after the language.
type attr struct {
	Key    string
	Val    string
	HasVal bool
}

// mode says what capture does with a block.
type mode int

const (
	modeMain     mode = iota // runs in the page workspace
	modeIgnore               // ::hint, ::answer: neither written nor run
	modeSolution             // ::solution: runs in a copy of the workspace
	modeIsolated             // :::deep-dive: runs in a copy of the workspace
	modeReveal               // ::reveal: runs in a copy of the workspace, so the printed answer is verified
)

func (m mode) stage() string {
	switch m {
	case modeSolution:
		return "solution"
	case modeIsolated:
		return "deep-dive"
	case modeReveal:
		return "reveal"
	}
	return "main"
}

// block is one fenced code block.
type block struct {
	N        int // 1-based ordinal among the fenced blocks of the page
	Open     int // 0-based line of the opening fence
	Close    int // 0-based line of the closing fence, -1 when unclosed
	Indent   int // columns of indentation of the opening fence
	FenceCh  byte
	FenceLen int
	Info     string
	InfoErr  string // problem with the info string, reported by validate
	Lang     string
	Attrs    []attr
	Content  []string

	Container string // enclosing container, "" at top level
	Part      string // enclosing part (hint, solution, reveal, answer), ""
	Mode      mode
	Scope     int // blocks with the same non-zero scope share one workspace copy

	File    string // file=NAME
	Run     bool
	Out     bool
	Hidden  bool
	Check   bool
	Exit    int
	HasExit bool

	Owner *block // out block: the run block it belongs to
	Pair  *block // run block: its out block
}

func (b *block) isCommand() bool { return b.Run || b.Check }

func (b *block) executable() bool { return b.isCommand() || b.Out || b.File != "" }

// script returns the shell text of a command block.
func (b *block) script() string { return strings.Join(b.Content, "\n") }

// page is a parsed Markdown page.
type page struct {
	Path     string
	Lines    []string
	BOM      bool
	CRLF     bool
	FinalNL  bool
	FM       frontMatter
	BodyFrom int // 0-based index of the first body line
	Blocks   []*block
	Errors   []issue
	Warnings []issue

	envLine int // 0-based line of the env key in the front matter
}

func (p *page) errorf(line int, format string, args ...any) {
	p.Errors = append(p.Errors, issue{Line: line + 1, Msg: fmt.Sprintf(format, args...)})
}

func (p *page) warnf(line int, format string, args ...any) {
	p.Warnings = append(p.Warnings, issue{Line: line + 1, Msg: fmt.Sprintf(format, args...)})
}

// runnable reports whether the page has a command block that is not ignored.
func (p *page) runnable() bool {
	for _, b := range p.Blocks {
		if b.Mode != modeIgnore && b.isCommand() {
			return true
		}
	}
	return false
}

// bom is the UTF-8 byte order mark that some editors put at the start of a file.
const bom = "\xef\xbb\xbf"

var (
	reContainerOpen  = regexp.MustCompile(`^ {0,3}:::([A-Za-z][A-Za-z0-9-]*)(?:[ \t]+(.*))?$`)
	reContainerClose = regexp.MustCompile(`^ {0,3}:::[ \t]*$`)
	rePart           = regexp.MustCompile(`^ {0,3}::([A-Za-z][A-Za-z0-9-]*)(?:[ \t]+(.*))?$`)
	reQuotePrefix    = regexp.MustCompile(`^ {0,3}>[ ]?`)
)

// parts maps the part markers of the Markdown grammar to what capture does
// with the blocks that follow them.
var parts = map[string]mode{
	"hint":     modeIgnore,
	"reveal":   modeReveal,
	"answer":   modeIgnore,
	"solution": modeSolution,
}

// flags are info string words that are attributes, never a language.
var flags = map[string]bool{"run": true, "out": true, "hidden": true, "check": true, "ln": true}

// parsePage splits src into front matter and body, finds every fenced code
// block and records, for each, the container and part it sits in.
func parsePage(pagePath string, src []byte) *page {
	p := &page{Path: pagePath, FM: frontMatter{Capture: true}}
	text := string(src)
	if strings.HasPrefix(text, bom) {
		p.BOM = true
		text = strings.TrimPrefix(text, bom)
	}
	p.CRLF = strings.Contains(text, "\r\n")
	p.FinalNL = strings.HasSuffix(text, "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" && !p.FinalNL {
		p.Lines = nil
	} else {
		p.Lines = strings.Split(text, "\n")
	}
	for i, l := range p.Lines {
		p.Lines[i] = strings.TrimSuffix(l, "\r")
	}
	p.parseFrontMatter()
	if !p.FM.Capture {
		return p
	}
	p.scan()
	p.pair()
	p.checkHolders()
	sort.SliceStable(p.Errors, func(i, j int) bool { return p.Errors[i].Line < p.Errors[j].Line })
	sort.SliceStable(p.Warnings, func(i, j int) bool { return p.Warnings[i].Line < p.Warnings[j].Line })
	return p
}

// parseFrontMatter reads the YAML block at the top of the page.
func (p *page) parseFrontMatter() {
	if len(p.Lines) == 0 || strings.TrimRight(p.Lines[0], " \t") != "---" {
		return
	}
	end := -1
	for i := 1; i < len(p.Lines); i++ {
		t := strings.TrimRight(p.Lines[i], " \t")
		if t == "---" || t == "..." {
			end = i
			break
		}
	}
	if end < 0 {
		p.errorf(0, "front matter is not closed with a line of ---")
		return
	}
	p.BodyFrom = end + 1

	md := goldmark.New(goldmark.WithExtensions(meta.Meta))
	ctx := parser.NewContext()
	yamlSrc := "---\n" + strings.Join(p.Lines[1:end], "\n") + "\n---\n"
	md.Parser().Parse(gtext.NewReader([]byte(yamlSrc)), parser.WithContext(ctx))
	m, err := meta.TryGet(ctx)
	if err != nil {
		p.errorf(0, "front matter: %v", err)
		return
	}
	front := p.Lines[1:end]
	keyLine := func(key string) int {
		for i, l := range front {
			if strings.HasPrefix(l, key+":") {
				return i + 1
			}
		}
		return 0
	}

	p.FM.Title = asString(m["title"])
	switch v := m["capture"].(type) {
	case nil:
	case bool:
		p.FM.Capture = v
	case string:
		switch strings.ToLower(v) {
		case "false", "no", "off":
			p.FM.Capture = false
		case "true", "yes", "on":
		default:
			p.errorf(keyLine("capture"), "front matter: capture must be true or false, not %q", v)
		}
	default:
		p.errorf(keyLine("capture"), "front matter: capture must be true or false")
	}

	p.parseServices(m["services"], keyLine("services"))
	p.parseEnv(m["env"], keyLine("env"))

	raw, ok := m["normalize"]
	if !ok || raw == nil {
		return
	}
	line := keyLine("normalize")
	list, ok := raw.([]any)
	if !ok {
		p.errorf(line, "front matter: normalize must be a list of entries with pattern and replace")
		return
	}
	for i, item := range list {
		entry, ok := asMap(item)
		if !ok {
			p.errorf(line, "front matter: normalize entry %d must have pattern and replace", i+1)
			continue
		}
		for k := range entry {
			if k != "pattern" && k != "replace" {
				p.errorf(line, "front matter: normalize entry %d has unknown key %q", i+1, k)
			}
		}
		pat := asString(entry["pattern"])
		if pat == "" {
			p.errorf(line, "front matter: normalize entry %d has no pattern", i+1)
			continue
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			p.errorf(line, "front matter: normalize entry %d: %v", i+1, err)
			continue
		}
		p.FM.Normalize = append(p.FM.Normalize, rule{Pattern: pat, Replace: asString(entry["replace"]), re: re})
	}
}

// parseServices reads the services list: names, each with an optional port,
// such as [fakes3, fakeoci:5078].
func (p *page) parseServices(raw any, line int) {
	if raw == nil {
		return
	}
	var items []any
	switch v := raw.(type) {
	case []any:
		items = v
	case string:
		items = []any{v}
	default:
		p.errorf(line, "front matter: services must be a list of service names, such as [fakes3, fakeoci:5078]")
		return
	}
	names, ports := map[string]bool{}, map[int]string{}
	for i, item := range items {
		str, ok := item.(string)
		if !ok {
			p.errorf(line, "front matter: services entry %d must be a name, optionally with a port: fakes3 or fakes3:9000", i+1)
			continue
		}
		spec, err := parseServiceSpec(str)
		if err != nil {
			p.errorf(line, "front matter: services entry %d: %v", i+1, err)
			continue
		}
		if names[spec.Name] {
			p.errorf(line, "front matter: service %s is listed twice", spec.Name)
			continue
		}
		if other, dup := ports[spec.Port]; spec.Port != 0 && dup {
			p.errorf(line, "front matter: %s and %s name the same port %d", other, spec.Name, spec.Port)
			continue
		}
		names[spec.Name] = true
		if spec.Port != 0 {
			ports[spec.Port] = spec.Name
		}
		p.FM.Services = append(p.FM.Services, spec)
	}
}

var reEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseEnv reads the env map: variables set for every command of the page.
func (p *page) parseEnv(raw any, line int) {
	p.envLine = line
	if raw == nil {
		return
	}
	m, ok := asMap(raw)
	if !ok {
		p.errorf(line, "front matter: env must be a map from variable names to values")
		return
	}
	var names []string
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		switch {
		case !reEnvName.MatchString(k):
			p.errorf(line, "front matter: env: %q is not a variable name", k)
		case k == "PATH" || k == "PWD":
			p.errorf(line, "front matter: env: capture sets %s itself", k)
		case m[k] != nil && !isScalar(m[k]):
			p.errorf(line, "front matter: env: the value of %s must be a string", k)
		default:
			p.FM.Env = append(p.FM.Env, envVar{Name: k, Value: asString(m[k])})
		}
	}
}

func isScalar(v any) bool {
	switch v.(type) {
	case string, bool, int, int64, uint64, float64:
		return true
	}
	return false
}

// checkHolders reports a {{NAME_PORT}} placeholder in a file= block or in an
// env value for a service that the page does not start.
func (p *page) checkHolders() {
	declared := map[string]bool{}
	for _, s := range p.FM.Services {
		declared[knownServices[s.Name].Prefix()] = true
	}
	check := func(line int, text string) {
		for _, m := range reHolder.FindAllStringSubmatch(text, -1) {
			if holderService(m[1]) != "" && !declared[m[1]] {
				p.errorf(line, "%s names the service %s, which the page does not list under services", m[0], holderService(m[1]))
			}
		}
	}
	for _, b := range p.Blocks {
		if b.File == "" || b.Mode == modeIgnore {
			continue
		}
		for i, l := range b.Content {
			check(b.Open+1+i, l)
		}
	}
	for _, e := range p.FM.Env {
		check(p.envLine, e.Value)
	}
}

func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	}
	return fmt.Sprint(v)
}

func asMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			out[fmt.Sprint(k)] = val
		}
		return out, true
	}
	return nil, false
}

// scan walks the body line by line. A line scanner, not a Markdown parser,
// keeps exact line numbers for reports and for -update.
func (p *page) scan() {
	var (
		cur       *block
		container string
		contLine  int
		part      string
		scope     int // counter of containers and ::solution parts
		curScope  int
	)
	push := func(i int, f fenceOpen) *block {
		b := &block{
			N: len(p.Blocks) + 1, Open: i, Close: -1, Indent: f.indent,
			FenceCh: f.ch, FenceLen: f.n, Info: f.info,
			Container: container, Part: part, Scope: curScope,
		}
		if part != "" {
			b.Mode = parts[part]
		} else if container == "deep-dive" {
			b.Mode = modeIsolated
		}
		if b.Mode == modeMain {
			b.Scope = 0
		}
		p.Blocks = append(p.Blocks, b)
		return b
	}

	for i := p.BodyFrom; i < len(p.Lines); i++ {
		line := p.Lines[i]
		if cur != nil {
			if closesFence(line, cur) {
				cur.Close = i
				cur = nil
			} else {
				cur.Content = append(cur.Content, dedent(line, cur.Indent))
			}
			continue
		}
		if f, ok := parseOpenFence(line); ok {
			cur = push(i, f)
			p.classify(cur)
			continue
		}
		if reQuotePrefix.MatchString(line) {
			p.checkQuoted(i, line)
			continue
		}
		switch {
		case reContainerClose.MatchString(line):
			if container == "" {
				p.errorf(i, "':::' closes a container, but none is open")
				continue
			}
			container, part = "", ""
			curScope = 0
		case reContainerOpen.MatchString(line):
			m := reContainerOpen.FindStringSubmatch(line)
			if container != "" {
				p.errorf(i, "container :::%s starts inside :::%s (line %d): containers do not nest", m[1], container, contLine+1)
			}
			container, contLine, part = m[1], i, ""
			scope++
			curScope = scope
		case rePart.MatchString(line):
			m := rePart.FindStringSubmatch(line)
			_, known := parts[m[1]]
			switch {
			case container == "" && known:
				p.errorf(i, "::%s outside a container", m[1])
			case container == "":
				// ordinary text that starts with two colons
			case !known:
				p.errorf(i, "unknown part ::%s in :::%s (known: hint, solution, reveal, answer)", m[1], container)
			default:
				part = m[1]
				scope++
				curScope = scope
			}
		}
	}
	if cur != nil {
		p.errorf(cur.Open, "code fence opened here is never closed")
	}
	if container != "" {
		p.errorf(contLine, "container :::%s is never closed with ':::'", container)
	}
	for _, b := range p.Blocks {
		p.validate(b)
	}
}

// fenceOpen describes an opening code fence.
type fenceOpen struct {
	indent int
	ch     byte
	n      int
	info   string
}

// parseOpenFence recognises an opening fence: up to three spaces, then three
// or more backticks or tildes, then an info string. A backtick fence cannot
// have a backtick in its info string.
func parseOpenFence(line string) (fenceOpen, bool) {
	i := 0
	for i < len(line) && line[i] == ' ' && i < 4 {
		i++
	}
	if i > 3 || i >= len(line) || (line[i] != '`' && line[i] != '~') {
		return fenceOpen{}, false
	}
	ch := line[i]
	j := i
	for j < len(line) && line[j] == ch {
		j++
	}
	if j-i < 3 {
		return fenceOpen{}, false
	}
	info := strings.TrimSpace(line[j:])
	if ch == '`' && strings.Contains(info, "`") {
		return fenceOpen{}, false
	}
	return fenceOpen{indent: i, ch: ch, n: j - i, info: info}, true
}

// closesFence reports whether line closes b: the same character, at least as
// many times, nothing after it.
func closesFence(line string, b *block) bool {
	i := 0
	for i < len(line) && line[i] == ' ' && i < 4 {
		i++
	}
	if i > 3 || i >= len(line) || line[i] != b.FenceCh {
		return false
	}
	j := i
	for j < len(line) && line[j] == b.FenceCh {
		j++
	}
	return j-i >= b.FenceLen && strings.TrimSpace(line[j:]) == ""
}

// dedent removes up to n leading spaces.
func dedent(line string, n int) string {
	i := 0
	for i < n && i < len(line) && line[i] == ' ' {
		i++
	}
	return line[i:]
}

// checkQuoted reports an executable fence inside a quotation, which capture
// does not look into.
func (p *page) checkQuoted(i int, line string) {
	rest := line
	for reQuotePrefix.MatchString(rest) {
		rest = reQuotePrefix.ReplaceAllString(rest, "")
	}
	f, ok := parseOpenFence(rest)
	if !ok {
		return
	}
	var b block
	if err := parseInfoInto(&b, f.info); err == nil && b.executable() {
		p.errorf(i, "a code block inside a callout is not executed: move it out of the quotation")
	}
}

// classify reads the info string of b into its fields. Errors are recorded
// by validate, once the part of the page the block belongs to is known.
func (p *page) classify(b *block) {
	if err := parseInfoInto(b, b.Info); err != nil {
		b.InfoErr = err.Error()
	}
}

func parseInfoInto(b *block, info string) error {
	toks, err := tokenizeInfo(info)
	if err != nil {
		return err
	}
	if len(toks) > 0 && !toks[0].HasVal && !flags[toks[0].Key] {
		b.Lang = toks[0].Key
		toks = toks[1:]
	}
	b.Attrs = toks
	var errs []string
	for _, a := range toks {
		switch a.Key {
		case "run":
			b.Run = true
		case "out":
			b.Out = true
		case "hidden":
			b.Hidden = true
		case "check":
			b.Check = true
		case "file":
			if !a.HasVal || a.Val == "" {
				errs = append(errs, "file= needs a name")
			}
			b.File = a.Val
		case "exit":
			n, err := strconv.Atoi(a.Val)
			if !a.HasVal || err != nil || n < 0 || n > 255 {
				errs = append(errs, fmt.Sprintf("exit=%s is not an exit code from 0 to 255", a.Val))
			}
			b.Exit, b.HasExit = n, true
		}
		if a.HasVal && (a.Key == "run" || a.Key == "out" || a.Key == "hidden" || a.Key == "check") {
			errs = append(errs, a.Key+" takes no value")
		}
	}
	// A hidden block that is not a file and not an output is a hidden command:
	// "hidden" means "run, but do not show", so it needs no second word.
	if b.Hidden && !b.Run && !b.Check && !b.Out && b.File == "" {
		b.Run = true
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// tokenizeInfo splits an info string into words and key=value pairs. A value
// may be quoted with " or ' and use \ to escape.
func tokenizeInfo(s string) ([]attr, error) {
	var out []attr
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			break
		}
		start := i
		for i < len(s) && s[i] != ' ' && s[i] != '\t' && s[i] != '=' {
			i++
		}
		key := s[start:i]
		if i < len(s) && s[i] == '=' {
			if key == "" {
				return nil, fmt.Errorf("attribute without a name before '='")
			}
			i++
			val, n, err := readValue(s[i:])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			i += n
			out = append(out, attr{Key: key, Val: val, HasVal: true})
			continue
		}
		out = append(out, attr{Key: key})
	}
	return out, nil
}

func readValue(s string) (string, int, error) {
	if s == "" {
		return "", 0, nil
	}
	if s[0] != '"' && s[0] != '\'' {
		n := 0
		for n < len(s) && s[n] != ' ' && s[n] != '\t' {
			n++
		}
		return s[:n], n, nil
	}
	q := s[0]
	var sb strings.Builder
	for i := 1; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && (s[i+1] == q || s[i+1] == '\\'):
			sb.WriteByte(s[i+1])
			i++
		case s[i] == q:
			return sb.String(), i + 1, nil
		default:
			sb.WriteByte(s[i])
		}
	}
	return "", 0, fmt.Errorf("unterminated quoted value")
}

// validate records the authoring errors of one block. Blocks in an ignored
// part are display only, so only executable-looking ones draw a warning.
func (p *page) validate(b *block) {
	if b.Mode == modeIgnore {
		if b.isCommand() || b.Out {
			p.warnf(b.Open, "%s block in ::%s is not executed, so its output is not verified", kindName(b), b.Part)
		}
		return
	}
	if b.InfoErr != "" {
		p.errorf(b.Open, "code fence: %s", b.InfoErr)
		return
	}
	if b.File != "" {
		clean := path.Clean(b.File)
		if path.IsAbs(b.File) || clean == ".." || strings.HasPrefix(clean, "../") || clean == "." {
			p.errorf(b.Open, "file=%s must be a relative path inside the workspace", b.File)
		}
	}
	if b.isCommand() && b.Out {
		p.errorf(b.Open, "a block cannot be both a command (run, check) and out")
	}
	if b.File != "" && (b.isCommand() || b.Out) {
		p.errorf(b.Open, "file= cannot be combined with run, check or out")
	}
	if b.HasExit && !b.isCommand() {
		p.errorf(b.Open, "exit= belongs on a run block")
	}
	if b.Check && b.HasExit && b.Exit != 0 {
		p.errorf(b.Open, "a check block must exit 0; remove exit=%d", b.Exit)
	}
	if b.isCommand() {
		first := ""
		for _, l := range b.Content {
			if strings.TrimSpace(l) != "" {
				first = strings.TrimSpace(l)
				break
			}
		}
		switch {
		case first == "":
			p.errorf(b.Open, "%s block has no commands", kindName(b))
		case strings.HasPrefix(first, "$ "):
			p.errorf(b.Open, "write commands without the '$ ' prompt; the page adds it")
		}
		for i, l := range b.Content {
			if f := strings.Fields(l); len(f) > 0 && f[0] == "cd" {
				p.warnf(b.Open+1+i, "commands never use cd: the workspace is the working directory")
			}
		}
	}
}

func kindName(b *block) string {
	switch {
	case b.Check:
		return "check"
	case b.Run:
		return "run"
	case b.Out:
		return "out"
	}
	return "file"
}

// pair attaches each out block to the command block directly before it.
// Between the two there may be blank lines and nothing else.
func (p *page) pair() {
	for i, b := range p.Blocks {
		if !b.Out || b.Mode == modeIgnore {
			continue
		}
		var prev *block
		if i > 0 {
			prev = p.Blocks[i-1]
		}
		if prev == nil || !prev.isCommand() || prev.Close < 0 || prev.Mode != b.Mode || prev.Scope != b.Scope ||
			prev.Pair != nil || !blankBetween(p.Lines, prev.Close, b.Open) {
			p.errorf(b.Open, "out block does not follow a run block: it must come directly after the run block it belongs to")
			continue
		}
		prev.Pair, b.Owner = b, prev
	}
}

func blankBetween(lines []string, closeLine, openLine int) bool {
	for i := closeLine + 1; i < openLine; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return false
		}
	}
	return true
}
