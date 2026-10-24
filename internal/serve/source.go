package serve

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"

	"github.com/pravo-systems/pravo/internal/config"
)

const (
	// maxBlockLines bounds the lines of a source block.
	maxBlockLines = 200
	// maxSourceFile bounds the size of a file that is read.
	maxSourceFile = 4 << 20
)

// source answers /api/source for a node: the lines that write it, the lines of
// each instance that fills it in, and everything else that shapes it. It reads
// files that the evaluation loaded and no other, selected by the address of the
// node, and masks what a sensitive attribute is given as a literal.
func (m *model) source(mn *mnode) *Source { return m.sourceFor(mn, m.plan) }

// sourceFor is source with the lines marked that the plan po changes.
func (m *model) sourceFor(mn *mnode, po *planOverlay) *Source {
	src := &Source{Version: APIVersion, Addr: mn.Addr, Blocks: []SourceBlock{}, Contributions: []Contribution{}}
	ss := m.spec
	switch {
	case ss.root == nil:
		src.Notes = append(src.Notes, "The specification does not load, so no lines are shown.")
		return src
	case mn.res == nil && mn.out == nil:
		src.Notes = append(src.Notes, "The node is recorded in the state and the specification no longer declares it, so there are no lines to show.")
		return src
	}
	ss.cueMu.Lock()
	defer ss.cueMu.Unlock()

	sens := map[string]bool{}
	switch {
	case mn.out != nil:
		// A literal that a sensitive output is given is its value: it is masked.
		sens["value"] = mn.out.Sensitive || mn.output.Sensitive
	case mn.sch != nil:
		sens = sensitiveNames(mn.sch.Block)
	case mn.res.Mode == "managed" || mn.res.Mode == "data":
		src.Notes = append(src.Notes, "The schema of the type is not cached, so only literals assigned to attributes with a name that looks like a secret are masked.")
	}
	changed := m.changedTop(mn, po)
	reader := &blockReader{ss: ss, sens: sens, secrets: m.secretsFor(po), changed: changed, notes: &src.Notes}

	if mn.Source != nil {
		if b := reader.read("definition", "", mn.Source, true); b != nil {
			src.Blocks = append(src.Blocks, *b)
		}
	}
	// The instances that fill the definition in, the innermost first.
	var namespaces []string
	if path := splitAddr(mn.Addr); len(path.names) > 1 {
		for i := len(path.names) - 1; i >= 1; i-- {
			namespaces = append(namespaces, path.prefix(i))
		}
	}
	for _, p := range namespaces {
		ns := m.namespaces[p]
		if ns == nil || ns.Definition == nil || ns.Declared == nil {
			continue
		}
		label := p
		if ns.Definition != nil {
			if def := reader.line(ns.Definition); def != "" {
				label = p + " of " + def
			}
		}
		if b := reader.read("instance", label, ns.Declared, false); b != nil {
			src.Blocks = append(src.Blocks, *b)
		}
	}
	for _, b := range src.Blocks {
		src.Contributions = append(src.Contributions, reader.contribution(b, mn))
	}
	src.Contributions = append(src.Contributions, m.otherContributions(mn, reader)...)
	if mn.Source != nil {
		src.Generated = m.definitions[refKey(mn.Source)]
	}
	src.Notes = dedupe(src.Notes)
	return src
}

// blockReader reads the blocks of one answer.
type blockReader struct {
	ss      *specState
	sens    map[string]bool
	secrets *secretSet
	changed map[string]bool
	notes   *[]string

	parsed map[string]*parsedFile
	// idents are the identifiers written in the lines that were read.
	idents map[string]bool
}

type parsedFile struct {
	data []byte
	file *ast.File
	mod  time.Time
	err  error
}

// load reads and parses a file the evaluation loaded.
func (r *blockReader) load(display string) *parsedFile {
	if r.parsed == nil {
		r.parsed = map[string]*parsedFile{}
	}
	if pf, ok := r.parsed[display]; ok {
		return pf
	}
	pf := &parsedFile{}
	r.parsed[display] = pf
	abs, ok := r.ss.abs[display]
	if !ok || !r.ss.allowed[filepath.Clean(abs)] {
		pf.err = fmt.Errorf("%s is not a file of the evaluation", display)
		return pf
	}
	// The file is the one that was loaded: a link that points elsewhere now is not
	// followed.
	if now, err := filepath.EvalSymlinks(abs); err != nil || now != r.ss.real[filepath.Clean(abs)] {
		pf.err = fmt.Errorf("%s is not the file that was loaded", display)
		return pf
	}
	fi, err := os.Stat(abs)
	if err != nil {
		pf.err = err
		return pf
	}
	if fi.Size() > maxSourceFile {
		pf.err = fmt.Errorf("%s is too large to show", display)
		return pf
	}
	pf.mod = fi.ModTime()
	pf.data, pf.err = os.ReadFile(abs)
	if pf.err != nil {
		return pf
	}
	pf.file, pf.err = parser.ParseFile(abs, pf.data, parser.ParseComments)
	return pf
}

// line returns the first line of a reference, as a short name for it.
func (r *blockReader) line(ref *Ref) string {
	pf := r.load(ref.File)
	if pf.err != nil || ref.Line < 1 {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(pf.data), "\r\n", "\n"), "\n")
	if ref.Line > len(lines) {
		return ""
	}
	text := strings.TrimSpace(lines[ref.Line-1])
	if i := strings.IndexAny(text, ":{"); i > 0 {
		text = strings.TrimSpace(text[:i])
	}
	return text
}

// read builds the block of lines that a reference points to. A file that
// cannot be read or parsed gives no block: lines that cannot be checked for
// literals to mask are not shown.
func (r *blockReader) read(role, label string, ref *Ref, markChanged bool) *SourceBlock {
	pf := r.load(ref.File)
	if pf.err != nil {
		*r.notes = append(*r.notes, fmt.Sprintf("The lines of %s are not shown: %v.", ref.File, pf.err))
		return nil
	}
	field, stack := findField(pf.file, ref)
	if field == nil {
		*r.notes = append(*r.notes, fmt.Sprintf("%s changed after the specification was loaded, so the lines of %s:%d are not shown.", ref.File, ref.File, ref.Line))
		return nil
	}
	first, last := field.Pos().Line(), field.End().Line()
	if end := field.End(); end.Column() == 1 && last > first {
		last-- // an end at the start of a line closes the previous one
	}
	start, stop := first, last
	// A resource written in a comprehension is shown with the lines of the
	// comprehension around it.
	for i := len(stack) - 1; i >= 0; i-- {
		if c, ok := stack[i].(*ast.Comprehension); ok {
			cs, ce := c.Pos().Line(), c.End().Line()
			if ce-cs+1 <= maxBlockLines {
				start, stop = min(start, cs), max(stop, ce)
			}
			break
		}
	}
	if r.idents == nil {
		r.idents = map[string]bool{}
	}
	ast.Walk(field, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			r.idents[id.Name] = true
		}
		return true
	}, nil)
	if label == "" {
		var parts []string
		for _, n := range stack {
			if f, ok := n.(*ast.Field); ok {
				parts = append(parts, labelText(f.Label))
			}
		}
		label = strings.Join(append(parts, labelText(field.Label)), ".")
	}
	text := strings.ReplaceAll(string(pf.data), "\r\n", "\n")
	masked, touched := r.mask(pf, field, text)
	lines := strings.Split(masked, "\n")
	if stop > len(lines) {
		stop = len(lines)
	}
	if start < 1 || start > stop {
		return nil
	}
	// Whatever else the lines hold that is a sensitive value of the state is
	// withheld too: the header of a comprehension, a comment, a literal that a
	// definition shares.
	if r.secrets != nil && len(r.secrets.list) > 0 {
		for n := start; n <= stop && n <= len(lines); n++ {
			if t, ok := r.secrets.scrub(lines[n-1]); ok {
				lines[n-1] = t
				if touched == nil {
					touched = map[int]bool{}
				}
				touched[n] = true
			}
		}
	}
	changedLines := map[int]bool{}
	if markChanged && len(r.changed) > 0 {
		for _, f := range topFields(field) {
			name, _, err := ast.LabelName(f.Label)
			if err == nil && r.changed[name] {
				for l := f.Pos().Line(); l <= f.End().Line(); l++ {
					changedLines[l] = true
				}
			}
		}
	}
	b := &SourceBlock{Role: role, Label: label, File: ref.File, Kind: r.ss.fileKind(r.ss.abs[ref.File]), StartLine: start, EndLine: stop}
	mod := pf.mod
	b.Modified = &mod
	if mod.After(r.ss.loaded) {
		*r.notes = append(*r.notes, fmt.Sprintf("%s was written after the specification was loaded; the lines may not be the ones that were evaluated.", ref.File))
	}
	if stop-start+1 > maxBlockLines {
		stop = start + maxBlockLines - 1
		b.EndLine = stop
	}
	b.Lines = make([]SourceLine, 0, stop-start+1)
	for n := start; n <= stop; n++ {
		sl := SourceLine{No: n, Text: lines[n-1]}
		switch {
		case touched[n]:
			sl.Mark = "sensitive"
		case changedLines[n]:
			sl.Mark = "changed"
		case n >= first && n <= last:
			sl.Mark = "focus"
		default:
			sl.Dim = true
		}
		b.Lines = append(b.Lines, sl)
	}
	return b
}

// findField finds the field at a reference in a parsed file, and the nodes
// above it.
func findField(f *ast.File, ref *Ref) (*ast.Field, []ast.Node) {
	var found *ast.Field
	var stack, at []ast.Node
	ast.Walk(f, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		if fl, ok := n.(*ast.Field); ok {
			if p := fl.Pos(); p.Line() == ref.Line && (ref.Col == 0 || p.Column() == ref.Col) {
				found = fl
				at = append([]ast.Node{}, stack...)
				return false
			}
		}
		stack = append(stack, n)
		return true
	}, func(n ast.Node) {
		if found == nil {
			stack = stack[:len(stack)-1]
		}
	})
	return found, at
}

// topFields returns the fields written directly in the struct literals that the
// value of a field unifies.
func topFields(f *ast.Field) []*ast.Field {
	var out []*ast.Field
	var visit func(e ast.Expr)
	visit = func(e ast.Expr) {
		switch x := e.(type) {
		case *ast.StructLit:
			for _, el := range x.Elts {
				if fl, ok := el.(*ast.Field); ok {
					out = append(out, fl)
				}
			}
		case *ast.BinaryExpr:
			if x.Op == token.AND {
				visit(x.X)
				visit(x.Y)
			}
		case *ast.ParenExpr:
			visit(x.X)
		}
	}
	visit(f.Value)
	return out
}

// mask replaces, in the text of the file, the string literals that a sensitive
// attribute is given in the field, and any literal that holds a sensitive value
// of the state. The lines keep their numbers. It returns the text and the lines
// that were masked.
func (r *blockReader) mask(pf *parsedFile, field *ast.Field, text string) (string, map[int]bool) {
	type span struct{ from, to int }
	var spans []span
	// Offsets are into the bytes the file was parsed from, which had no
	// carriage returns removed: map them onto the text when the file has any.
	crlf := strings.Contains(string(pf.data), "\r\n")
	add := func(n ast.Node) {
		from, to := n.Pos().Offset(), n.End().Offset()
		if from < 0 || to <= from || to > len(pf.data) {
			return
		}
		if crlf {
			from -= strings.Count(string(pf.data[:from]), "\r\n")
			to -= strings.Count(string(pf.data[:to]), "\r\n")
		}
		spans = append(spans, span{from, to})
	}
	var strings_ func(n ast.Node)
	strings_ = func(n ast.Node) {
		ast.Walk(n, func(c ast.Node) bool {
			switch x := c.(type) {
			case *ast.BasicLit:
				if x.Kind == token.STRING {
					add(x)
				}
				return false
			case *ast.Interpolation:
				add(x)
				return false
			}
			return true
		}, nil)
	}
	ast.Walk(field, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Field:
			name, _, err := ast.LabelName(x.Label)
			if err != nil {
				name = labelText(x.Label)
			}
			if r.sens[name] || guarded(name) {
				strings_(x.Value)
				return false
			}
		case *ast.BasicLit:
			if x.Kind == token.STRING && r.secrets != nil && len(r.secrets.list) > 0 {
				if s, err := unquote(x.Value); err == nil && r.secrets.leaks(s) {
					add(x)
				}
			}
		}
		return true
	}, nil)
	if len(spans) == 0 {
		return text, nil
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].from < spans[j].from })
	var b strings.Builder
	touched := map[int]bool{}
	pos, line := 0, 1
	advance := func(upto int) {
		seg := text[pos:upto]
		line += strings.Count(seg, "\n")
		b.WriteString(seg)
		pos = upto
	}
	for _, sp := range spans {
		if sp.from < pos || sp.to > len(text) {
			continue // inside a literal that is masked already
		}
		advance(sp.from)
		lit := text[sp.from:sp.to]
		pieces := strings.Split(lit, "\n")
		for i := range pieces {
			touched[line+i] = true
		}
		for i := range pieces {
			if i > 0 {
				b.WriteByte('\n')
			}
			if i == 0 {
				b.WriteString(`"` + mask + `"`)
			} else {
				b.WriteString(mask)
			}
		}
		line += len(pieces) - 1
		pos = sp.to
	}
	b.WriteString(text[pos:])
	return b.String(), touched
}

// unquote reads the value of a simple CUE string literal.
func unquote(lit string) (string, error) {
	if len(lit) >= 2 && lit[0] == '"' && lit[len(lit)-1] == '"' {
		return strings.ReplaceAll(lit[1:len(lit)-1], `\"`, `"`), nil
	}
	return lit, nil
}

// labelText writes a label as it appears in the file.
func labelText(l ast.Label) string {
	b, err := format.Node(l)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// contribution describes a block as something that shapes the node.
func (r *blockReader) contribution(b SourceBlock, mn *mnode) Contribution {
	where := fmt.Sprintf("%s:%d", b.File, b.StartLine)
	if b.EndLine > b.StartLine {
		where += fmt.Sprintf("-%d", b.EndLine)
	}
	c := Contribution{Kind: b.Kind, Where: where}
	switch b.Role {
	case "definition":
		c.Note = "writes the " + modeNoun(mn.Mode)
	default:
		c.Note = "instance " + b.Label + ": fills the definition in"
	}
	return c
}

func modeNoun(mode string) string {
	switch mode {
	case "data":
		return "data source"
	case "ephemeral":
		return "ephemeral resource"
	case "call":
		return "function call"
	case "builtin":
		return "built-in value"
	case "output":
		return "output"
	}
	return "resource"
}

// otherContributions lists what shapes the node apart from the lines shown:
// the generated package of its provider, the lock file, the values given on the
// command line and the built-in package.
func (m *model) otherContributions(mn *mnode, r *blockReader) []Contribution {
	ss := m.spec
	var out []Contribution
	if mn.out != nil {
		out = append(out, Contribution{Kind: "builtin", Where: "pravo.dev/pravo", Note: "the built-in package gives pravo.#Output"})
		return append(out, m.tagContributions(mn, r)...)
	}
	switch mn.res.Mode {
	case "builtin":
		out = append(out, Contribution{Kind: "builtin", Where: "pravo.dev/pravo", Note: "the built-in package gives pravo." + builtinName(mn.res.Type)})
	case "managed", "data", "ephemeral", "call":
		if ref := ss.typeDefinition(mn.res); ref != nil {
			note := fmt.Sprintf("the schema of %s %s", mn.res.Type, modeNoun(mn.res.Mode))
			if mn.providerVersion != "" {
				note += " in provider " + providerShort(mn) + " v" + mn.providerVersion
			}
			out = append(out, Contribution{Kind: "generated", Where: ref.File, Note: note})
		}
		if e, ok := ss.lock.Providers[providerSourceOf(mn)]; ok && e.Version != "" {
			out = append(out, Contribution{Kind: "file", Where: "pravo.lock.cue", Note: fmt.Sprintf("pins provider %s at v%s", providerShort(mn), e.Version)})
		}
	}
	out = append(out, m.tagContributions(mn, r)...)
	return out
}

func builtinName(typ string) string {
	switch typ {
	case "env":
		return "#Env"
	case "secret_env":
		return "#SecretEnv"
	case "remote_outputs":
		return "#RemoteOutputs"
	}
	return typ
}

func providerSourceOf(mn *mnode) string {
	if mn.providerSource != "" {
		return mn.providerSource
	}
	return ""
}

func providerShort(mn *mnode) string {
	return strings.TrimPrefix(providerSourceOf(mn), "registry.opentofu.org/")
}

// tagContributions lists the -t values that shape the node: those whose
// @tag(...) field is named in the lines that write it. The value is shown unless
// the field looks like a secret.
func (m *model) tagContributions(mn *mnode, r *blockReader) []Contribution {
	tags := m.srv.opts.Tags
	if len(tags) == 0 {
		return nil
	}
	values := map[string]string{}
	for _, t := range tags {
		k, v, _ := strings.Cut(t, "=")
		values[k] = v
	}
	idents := r.idents
	// The fields that take a tag, in the files that were read.
	var out []Contribution
	done := map[string]bool{}
	for _, pf := range r.parsed {
		if pf.file == nil {
			continue
		}
		ast.Walk(pf.file, func(n ast.Node) bool {
			f, ok := n.(*ast.Field)
			if !ok {
				return true
			}
			for _, a := range f.Attrs {
				key, body := a.Split()
				if key != "tag" {
					continue
				}
				tag, _, _ := strings.Cut(body, ",")
				tag = strings.TrimSpace(tag)
				name, _, _ := ast.LabelName(f.Label)
				if _, given := values[tag]; !given || done[tag] || !idents[name] {
					continue
				}
				done[tag] = true
				val := values[tag]
				if guarded(name) || guarded(tag) || r.secrets.leaks(val) {
					val = "(withheld)"
				}
				out = append(out, Contribution{Kind: "tag", Where: "-t " + tag + "=" + val, Note: fmt.Sprintf("the value of %s comes from the command line", name)})
			}
			return true
		}, nil)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Where < out[j].Where })
	return out
}

// changedTop lists the top-level attributes the plan changes in the node.
func (m *model) changedTop(mn *mnode, po *planOverlay) map[string]bool {
	ch := m.changeIn(po, mn)
	if ch == nil || len(ch.Attrs) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, a := range ch.Attrs {
		name := a.Path
		if i := strings.IndexAny(name, ".["); i >= 0 {
			name = name[:i]
		}
		out[name] = true
	}
	return out
}

// typeDefinition finds where the definition of a resource's type is written:
// the generated package of its provider.
func (ss *specState) typeDefinition(r *config.Resource) *Ref {
	if ss.root == nil {
		return nil
	}
	if ref, ok := ss.typeDefs[r.Address()]; ok {
		return ref
	}
	ref := ss.typeRef(ss.root.Value.LookupPath(r.Path), 0)
	ss.typeDefs[r.Address()] = ref
	return ref
}

func (ss *specState) typeRef(v cue.Value, depth int) *Ref {
	if depth > 4 {
		return nil
	}
	if root, p := v.ReferencePath(); root.Exists() && len(p.Selectors()) > 0 {
		if ref := ss.refOf(root.LookupPath(p)); ref != nil && strings.HasPrefix(ref.File, "generated/") {
			return &Ref{File: ref.File, Line: ref.Line, Col: ref.Col}
		}
	}
	op, args := v.Expr()
	if op != cue.AndOp {
		return nil
	}
	for _, a := range args {
		if ref := ss.typeRef(a, depth+1); ref != nil {
			return ref
		}
	}
	return nil
}

// dedupe removes repeated strings, keeping the order.
func dedupe(list []string) []string {
	seen := map[string]bool{}
	out := list[:0]
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
