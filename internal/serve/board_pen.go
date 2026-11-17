package serve

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"

	"github.com/pravo-systems/pravo/internal/config"
)

// The pen of the drawing board changes resources that the specification has: it sets and unsets fields of the struct a
// resource is written in, or removes the resource, in the file where it is written. It works on the bytes of that file
// and splices only what changes, so the comments and the layout of the rest stay as they were. A resource that is not
// written as one field of one file of the project (a template's, a loop's, one written in two places) is not changed
// here: the board says where it is written.

// BoardEditable is the answer of GET /api/board/edit: a resource as the pen can change it.
type BoardEditable struct {
	Version  int    `json:"version"`
	Addr     string `json:"addr"`
	Type     string `json:"type,omitempty"`
	Provider string `json:"provider,omitempty"`
	// File and Line say where it is written; Base is the digest of that file, which a change is made against.
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	Base string `json:"base,omitempty"`
	// Values are its fields as written: a text for a plain string, a wire for a reference to another resource, else CUE.
	Values map[string]BoardValue `json:"values"`
	// Withheld are the fields that hold a literal that the schema says is sensitive: their value is not sent.
	Withheld []string `json:"withheld"`
	// Kept are the fields the form does not show, which a change leaves as they are: $import, $depends_on and the like.
	Kept []string   `json:"kept"`
	Form *BoardType `json:"form,omitempty"`
	// Reason says why the pen cannot change it, when it cannot.
	Reason string `json:"reason,omitempty"`
}

// BoardEdit is a change to a resource of the specification: fields set or unset, or the resource removed.
type BoardEdit struct {
	Addr   string                `json:"addr"`
	Set    map[string]BoardValue `json:"set,omitempty"`
	Unset  []string              `json:"unset,omitempty"`
	Remove bool                  `json:"remove,omitempty"`
	// Base is the digest of the file the change was read from: a file that changed since is not written over.
	Base string `json:"base"`
}

// BoardFileChange is one file of the project as a change makes it: its new text and the lines it differs in.
type BoardFileChange struct {
	File string `json:"file"`
	Base string `json:"base"`
	Text string `json:"text"`
	// Lines are, for each resource changed in it, the first and last line of the resource in the new text (0 when removed).
	Lines map[string][2]int `json:"lines"`
	// Diff is the change as lines, with three lines of context around each change.
	Diff []BoardDiffLine `json:"diff"`
}

// BoardDiffLine is a line of a change: Op is " " kept, "-" taken out, "+" put in, or "…" for lines that are left out.
// Line is its number in the new text, or in the old one for a line taken out.
type BoardDiffLine struct {
	Op   string `json:"op"`
	Line int    `json:"line,omitempty"`
	Text string `json:"text,omitempty"`
}

const maxBoardEdits = 32

// penTarget is where a resource is written: one field of one file of the project, whose value has one struct literal.
type penTarget struct {
	abs, name string
	src       []byte
	field     *ast.Field
	body      *ast.StructLit
	// scope are the labels of the structs around the resource, its own included: a reference that starts with one of
	// them would read it, and not the resource of the top level that the wire means.
	scope map[string]bool
	res   *config.Resource
	path  []string
}

// penDigest names the bytes of a file.
func penDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:12])
}

// penFiles are the files of the package of the project that the pen may change: .cue files directly in the project
// directory, regular, not links.
func (s *Server) penFiles(m *model) []string {
	dir, err := filepath.Abs(s.opts.Dir)
	if err != nil || m.spec.root == nil {
		return nil
	}
	var out []string
	for _, f := range m.spec.root.Sources {
		if filepath.Dir(f) != dir || !strings.HasSuffix(f, ".cue") {
			continue
		}
		if fi, err := os.Lstat(f); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// penFind finds where the resource at addr is written. reason says why it cannot be changed here, when it cannot.
func (s *Server) penFind(m *model, addr string) (*penTarget, string) {
	res := m.spec.byPath[addr]
	if res == nil || m.spec.root == nil {
		return nil, "The specification has no resource " + strconv.Quote(clip(addr, 80)) + "."
	}
	if res.Mode != "managed" {
		return nil, "Only a managed resource is changed on the board."
	}
	var path []string
	for _, sel := range res.Path.Selectors() {
		if sel.LabelType() != cue.StringLabel {
			return nil, "It is an element of a list: change it where the list is written."
		}
		path = append(path, sel.Unquoted())
	}
	var found []*penTarget
	for _, f := range s.penFiles(m) {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		file, err := parser.ParseFile(f, src, parser.ParseComments)
		if err != nil {
			continue
		}
		for _, t := range penSearch(file.Decls, path, map[string]bool{}) {
			t.abs, t.name, t.src, t.res, t.path = f, filepath.Base(f), src, res, path
			found = append(found, t)
		}
	}
	switch {
	case len(found) == 0:
		return nil, "It is not written as a field of its own in a file of the project: a template, a loop or a condition makes it. Change it where that is written."
	case len(found) > 1:
		return nil, "It is written in " + strconv.Itoa(len(found)) + " places, which the board does not join: change it in the code."
	case found[0].body == nil:
		return nil, "Its value is not a struct that the board can add fields to: change it in the code."
	}
	return found[0], ""
}

// penSearch finds the fields at path in a list of declarations, each with the struct literal its value holds. A field in
// a comprehension is not looked into: what it makes is not a field of the file.
func penSearch(decls []ast.Decl, path []string, scope map[string]bool) []*penTarget {
	here := map[string]bool{}
	for k := range scope {
		here[k] = true
	}
	var out []*penTarget
	for _, d := range decls {
		f, ok := d.(*ast.Field)
		if !ok {
			continue
		}
		name, _, err := ast.LabelName(f.Label)
		if err != nil || name != path[0] {
			continue
		}
		bodies := penBodies(f.Value)
		if len(path) == 1 {
			t := &penTarget{field: f, scope: here}
			if len(bodies) == 1 {
				t.body = bodies[0]
				for _, e := range t.body.Elts {
					if g, ok := e.(*ast.Field); ok {
						if l, _, err := ast.LabelName(g.Label); err == nil {
							t.scope[l] = true
						}
					}
				}
			}
			out = append(out, t)
			continue
		}
		for _, b := range bodies {
			inner := map[string]bool{}
			for k := range here {
				inner[k] = true
			}
			for _, e := range b.Elts {
				if g, ok := e.(*ast.Field); ok {
					if l, _, err := ast.LabelName(g.Label); err == nil {
						inner[l] = true
					}
				}
			}
			out = append(out, penSearch(b.Elts, path[1:], inner)...)
		}
	}
	return out
}

// penBodies are the struct literals of a value: the value itself, or the operands of a chain of &.
func penBodies(x ast.Expr) []*ast.StructLit {
	switch v := x.(type) {
	case *ast.StructLit:
		return []*ast.StructLit{v}
	case *ast.BinaryExpr:
		if v.Op == token.AND {
			return append(penBodies(v.X), penBodies(v.Y)...)
		}
	case *ast.ParenExpr:
		return penBodies(v.X)
	}
	return nil
}

// boardEditHandler answers GET /api/board/edit?addr=ADDR.
func (s *Server) boardEditHandler(w http.ResponseWriter, r *http.Request, m *model) {
	addr := r.URL.Query().Get("addr")
	if addr == "" || len(addr) > 512 {
		writeError(w, http.StatusBadRequest, "addr is the address of a resource")
		return
	}
	writeJSON(w, http.StatusOK, s.boardEditable(m, addr))
}

// boardEditable reads a resource as the pen can change it.
func (s *Server) boardEditable(m *model, addr string) *BoardEditable {
	out := &BoardEditable{Version: APIVersion, Addr: addr, Values: map[string]BoardValue{}, Withheld: []string{}, Kept: []string{}}
	t, reason := s.penFind(m, addr)
	if t == nil {
		out.Reason = reason
		if res := m.spec.byPath[addr]; res != nil {
			out.Type = res.Type
		}
		return out
	}
	out.Type = t.res.Type
	out.File, out.Line, out.Base = t.name, t.field.Pos().Line(), penDigest(t.src)
	p := s.penProvider(m, t.res)
	if p == nil {
		out.Reason = "The schema of " + t.res.Type + " is not cached, so the board has no form for it."
		return out
	}
	out.Provider = p.Label
	sch := p.sch.Type("managed", t.res.Type)
	if sch == nil || sch.Block == nil {
		out.Reason = "The schema of " + t.res.Type + " is not cached, so the board has no form for it."
		return out
	}
	out.Form = s.boardForm(m, p, t.res.Type, sch.Block)
	known := map[string]BoardField{}
	for _, f := range out.Form.Fields {
		known[f.Name] = f
	}
	for _, e := range t.body.Elts {
		g, ok := e.(*ast.Field)
		if !ok {
			continue
		}
		name, _, err := ast.LabelName(g.Label)
		f, inForm := known[name]
		if err != nil || !inForm {
			if err == nil {
				out.Kept = append(out.Kept, name)
			}
			continue
		}
		if (f.Sensitive || f.WriteOnly) && penLiteral(g.Value) {
			out.Withheld = append(out.Withheld, name)
			continue
		}
		out.Values[name] = penValue(m, t.src, g.Value, t.scope)
	}
	return out
}

// penProvider is the provider of the board that runs a resource.
func (s *Server) penProvider(m *model, res *config.Resource) *boardProv {
	label := ""
	if p, ok := m.spec.root.ProviderPath(res.ProviderKey); ok {
		label = p.String()
	}
	return s.boardProvider(m, label, res.Type)
}

// penLiteral says whether an expression holds a literal anywhere: a value typed into the code.
func penLiteral(x ast.Expr) bool {
	lit := false
	ast.Walk(x, func(n ast.Node) bool {
		if b, ok := n.(*ast.BasicLit); ok && b.Kind != token.NULL {
			lit = true
		}
		return !lit
	}, nil)
	return lit
}

// penValue is a value as written, as the form shows it: a plain string as a text, a reference to a resource, or a list
// of them, as wires, anything else as CUE.
func penValue(m *model, src []byte, x ast.Expr, scope map[string]bool) BoardValue {
	if b, ok := x.(*ast.BasicLit); ok && b.Kind == token.STRING && !strings.HasPrefix(b.Value, `"""`) && !strings.HasPrefix(b.Value, "#") {
		if s, err := literal.Unquote(b.Value); err == nil {
			return BoardValue{Text: &s}
		}
	}
	if r, ok := penWire(m, x, scope); ok {
		return BoardValue{Wire: &r}
	}
	if l, ok := x.(*ast.ListLit); ok && len(l.Elts) > 0 {
		var ws []BoardRef
		for _, e := range l.Elts {
			r, ok := penWire(m, e, scope)
			if !ok {
				ws = nil
				break
			}
			ws = append(ws, r)
		}
		if ws != nil {
			return BoardValue{Wires: ws}
		}
	}
	return BoardValue{Expr: string(src[x.Pos().Offset():x.End().Offset()])}
}

// penWire reads a reference to an attribute of a resource of the specification, such as network.id, as a wire.
func penWire(m *model, x ast.Expr, scope map[string]bool) (BoardRef, bool) {
	sel, ok := x.(*ast.SelectorExpr)
	if !ok {
		return BoardRef{}, false
	}
	read, _, err := ast.LabelName(sel.Sel)
	if err != nil || !boardReadOK.MatchString(read) {
		return BoardRef{}, false
	}
	var parts []string
	for e := sel.X; ; {
		switch v := e.(type) {
		case *ast.SelectorExpr:
			l, _, err := ast.LabelName(v.Sel)
			if err != nil {
				return BoardRef{}, false
			}
			parts = append([]string{l}, parts...)
			e = v.X
			continue
		case *ast.Ident:
			parts = append([]string{v.Name}, parts...)
		default:
			return BoardRef{}, false
		}
		break
	}
	to := strings.Join(parts, ".")
	if _, ok := m.spec.byPath[to]; !ok || scope[parts[0]] {
		return BoardRef{}, false
	}
	return BoardRef{To: to, Read: read}, true
}

// penSplice is one change to the bytes of a file: what is between Start and End becomes Text.
type penSplice struct {
	start, end int
	text       string
}

// boardEdits makes the changes of a drawing to the files of the project: the new text of each file it changes. A
// problem is about the change of one resource, by its address.
func (s *Server) boardEdits(m *model, d *BoardDraft, labels map[string]bool, problems *drawProblems) []BoardFileChange {
	if len(d.Edits) > maxBoardEdits {
		problems.add("", "", "A drawing changes at most "+strconv.Itoa(maxBoardEdits)+" resources.")
		return nil
	}
	type fileWork struct {
		t       *penTarget
		splices []penSplice
		addrs   []string
		paths   map[string][]string
		removed map[string]bool
	}
	work := map[string]*fileWork{}
	seen := map[string]bool{}
	for _, e := range d.Edits {
		if seen[e.Addr] {
			problems.add(e.Addr, "", "The drawing changes "+e.Addr+" twice.")
			continue
		}
		seen[e.Addr] = true
		if labels[e.Addr] {
			problems.add(e.Addr, "@label", "A thing drawn has the name of a resource that the drawing changes.")
			continue
		}
		t, reason := s.penFind(m, e.Addr)
		if t == nil {
			problems.add(e.Addr, "", reason)
			continue
		}
		if e.Base != penDigest(t.src) {
			problems.add(e.Addr, "@base", t.name+" changed since "+e.Addr+" was read: read it again.")
			continue
		}
		fw := work[t.abs]
		if fw == nil {
			fw = &fileWork{t: t, removed: map[string]bool{}, paths: map[string][]string{}}
			work[t.abs] = fw
		} else {
			t.src = fw.t.src
		}
		sp, ok := s.penSplices(m, t, e, labels, problems)
		if !ok {
			continue
		}
		fw.splices = append(fw.splices, sp...)
		fw.addrs = append(fw.addrs, e.Addr)
		fw.paths[e.Addr] = t.path
		fw.removed[e.Addr] = e.Remove
	}
	var out []BoardFileChange
	for _, fw := range work {
		text, err := penApply(fw.t.src, fw.splices)
		if err != nil {
			problems.add(fw.addrs[0], "", "The change of "+fw.t.name+" does not make CUE: "+clip(err.Error(), 200))
			continue
		}
		fc := BoardFileChange{File: fw.t.name, Base: penDigest(fw.t.src), Text: text, Lines: map[string][2]int{}, Diff: penDiff(string(fw.t.src), text)}
		if f, err := parser.ParseFile(fw.t.name, text); err == nil {
			for _, a := range fw.addrs {
				if fw.removed[a] {
					fc.Lines[a] = [2]int{}
					continue
				}
				if hits := penSearch(f.Decls, fw.paths[a], nil); len(hits) == 1 {
					fc.Lines[a] = [2]int{hits[0].field.Pos().Line(), hits[0].field.End().Line()}
				}
			}
		}
		out = append(out, fc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

// penSplices are the changes to the bytes of one file that one edit makes.
func (s *Server) penSplices(m *model, t *penTarget, e BoardEdit, labels map[string]bool, problems *drawProblems) ([]penSplice, bool) {
	if e.Remove {
		if len(e.Set) > 0 || len(e.Unset) > 0 {
			problems.add(e.Addr, "", "A resource that is removed has no fields to set.")
			return nil, false
		}
		start, end, ok := penLines(t.src, penStart(t.field), t.field.End().Offset())
		if !ok {
			problems.add(e.Addr, "", "It shares its lines with other code: remove it in the code.")
			return nil, false
		}
		return []penSplice{{start: start, end: end}}, true
	}
	p := s.penProvider(m, t.res)
	if p == nil {
		problems.add(e.Addr, "", "The schema of "+t.res.Type+" is not cached.")
		return nil, false
	}
	sch := p.sch.Type("managed", t.res.Type)
	if sch == nil || sch.Block == nil {
		problems.add(e.Addr, "", "The schema of "+t.res.Type+" is not cached.")
		return nil, false
	}
	known := map[string]BoardField{}
	for _, f := range s.boardForm(m, p, t.res.Type, sch.Block).Fields {
		known[f.Name] = f
	}
	written := map[string]*ast.Field{}
	for _, el := range t.body.Elts {
		if g, ok := el.(*ast.Field); ok {
			if name, _, err := ast.LabelName(g.Label); err == nil {
				written[name] = g
			}
		}
	}
	oneLine := t.body.Lbrace.IsValid() && t.body.Lbrace.Line() == t.body.Rbrace.Line()
	set, names, ok := penSets(m, t, e, known, labels, problems)
	if !ok {
		return nil, false
	}
	unset := map[string]bool{}
	for _, name := range e.Unset {
		unset[name] = true
	}
	if oneLine || !t.body.Lbrace.IsValid() {
		return penRewriteStruct(t, written, set, unset, e.Addr, problems)
	}
	var out []penSplice
	for name, expr := range set {
		if g := written[name]; g != nil {
			out = append(out, penSplice{start: g.Value.Pos().Offset(), end: g.Value.End().Offset(), text: expr})
		}
	}
	for name := range unset {
		g := written[name]
		if g == nil {
			continue
		}
		start, end, ok := penLines(t.src, penStart(g), g.End().Offset())
		if !ok {
			problems.add(e.Addr, name, "It shares its line with other code: unset it in the code.")
			return nil, false
		}
		out = append(out, penSplice{start: start, end: end})
	}
	var added []string
	for _, name := range names {
		if written[name] == nil {
			if _, isSet := set[name]; isSet {
				added = append(added, cueLabel(name)+": "+set[name])
			}
		}
	}
	if len(added) > 0 {
		at, indent := penInsertAt(t)
		var b strings.Builder
		for _, a := range added {
			b.WriteString(indent + strings.ReplaceAll(a, "\n", "\n"+indent) + "\n")
		}
		out = append(out, penSplice{start: at, end: at, text: b.String()})
	}
	return out, true
}

// penSets are the values of the fields an edit sets, as CUE, and the names of those fields in order. ok is false when
// one of them cannot be written, or a field is set and unset at once.
func penSets(m *model, t *penTarget, e BoardEdit, known map[string]BoardField, labels map[string]bool, problems *drawProblems) (map[string]string, []string, bool) {
	ok := true
	set := map[string]string{}
	names := make([]string, 0, len(e.Set))
	for name := range e.Set {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f, isField := known[name]
		if !isField {
			problems.add(e.Addr, name, "There is no field "+strconv.Quote(clip(name, 60))+" to set.")
			ok = false
			continue
		}
		expr, heads, msg := boardExpr(e.Set[name], f, e.Addr, labels, m)
		// A wire means a resource of the top level, which a field around it would hide. CUE that is typed goes in as it
		// was written, and reads what CUE says it reads there.
		if v := e.Set[name]; msg == "" && (v.Wire != nil || len(v.Wires) > 0) {
			for h := range heads {
				if _, adds := e.Set[h]; t.scope[h] || adds {
					msg = "Here " + h + " names a field around the resource, which the wire would read instead: write the reference as CUE."
				}
			}
		}
		if msg != "" {
			problems.add(e.Addr, name, msg)
			ok = false
			continue
		}
		set[name] = expr
	}
	for _, name := range e.Unset {
		if _, isSet := e.Set[name]; isSet {
			problems.add(e.Addr, name, "A field is set or unset, not both.")
			ok = false
		}
	}
	return set, names, ok
}

// penRewriteStruct writes a struct that is on one line again, with the fields it keeps as they were written and the ones
// it gets: a field added to {a: 1} goes inside the braces.
func penRewriteStruct(t *penTarget, written map[string]*ast.Field, set map[string]string, unset map[string]bool, addr string, problems *drawProblems) ([]penSplice, bool) {
	if !t.body.Lbrace.IsValid() {
		problems.add(addr, "", "Its struct has no braces: change it in the code.")
		return nil, false
	}
	var parts []string
	done := map[string]bool{}
	for _, el := range t.body.Elts {
		g, ok := el.(*ast.Field)
		if !ok {
			parts = append(parts, string(t.src[el.Pos().Offset():el.End().Offset()]))
			continue
		}
		name, _, _ := ast.LabelName(g.Label)
		switch {
		case unset[name]:
		case set[name] != "":
			parts = append(parts, string(t.src[g.Pos().Offset():g.Value.Pos().Offset()])+set[name])
			done[name] = true
		default:
			parts = append(parts, string(t.src[g.Pos().Offset():g.End().Offset()]))
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		if !done[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		parts = append(parts, cueLabel(name)+": "+set[name])
	}
	text := "{" + strings.Join(parts, ", ") + "}"
	if strings.Contains(text, "\n") || len(text) > 100 {
		indent := penIndent(t.src, t.field.Pos().Offset())
		var b strings.Builder
		b.WriteString("{\n")
		for _, p := range parts {
			b.WriteString(indent + "\t" + strings.ReplaceAll(p, "\n", "\n"+indent+"\t") + "\n")
		}
		b.WriteString(indent + "}")
		text = b.String()
	}
	return []penSplice{{start: t.body.Lbrace.Offset(), end: t.body.Rbrace.Offset() + 1, text: text}}, true
}

// penStart is where a field begins, with the comments written above it.
func penStart(f *ast.Field) int {
	start := f.Pos().Offset()
	for _, cg := range ast.Comments(f) {
		if cg.Position == 0 && cg.Pos().IsValid() && cg.Pos().Offset() < start {
			start = cg.Pos().Offset()
		}
	}
	return start
}

// penLines widens [start, end) to whole lines, with the line break that ends them. ok is false when the lines hold other
// code than that; a comment at the end of the last line goes with it.
func penLines(src []byte, start, end int) (int, int, bool) {
	ls := bytes.LastIndexByte(src[:start], '\n') + 1
	if strings.TrimSpace(string(src[ls:start])) != "" {
		return 0, 0, false
	}
	le := bytes.IndexByte(src[end:], '\n')
	if le < 0 {
		le = len(src)
	} else {
		le += end + 1
	}
	rest := strings.TrimSpace(string(src[end:le]))
	rest = strings.TrimPrefix(rest, ",")
	if rest = strings.TrimSpace(rest); rest != "" && !strings.HasPrefix(rest, "//") {
		return 0, 0, false
	}
	return ls, le, true
}

// penInsertAt is where a new field of a struct written on several lines goes, before its closing brace, and the indent
// of its fields.
func penInsertAt(t *penTarget) (int, string) {
	at := bytes.LastIndexByte(t.src[:t.body.Rbrace.Offset()], '\n') + 1
	for _, el := range t.body.Elts {
		return at, penIndent(t.src, el.Pos().Offset())
	}
	return at, penIndent(t.src, t.body.Rbrace.Offset()) + "\t"
}

// penIndent is the white space that begins the line of an offset.
func penIndent(src []byte, off int) string {
	ls := bytes.LastIndexByte(src[:off], '\n') + 1
	i := ls
	for i < off && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	return string(src[ls:i])
}

// penApply makes the splices, from the last to the first, and formats the result when the file was formatted before, so
// that the fields of a changed struct line up again and the rest stays as it was.
func penApply(src []byte, splices []penSplice) (string, error) {
	sort.Slice(splices, func(i, j int) bool {
		if splices[i].start != splices[j].start {
			return splices[i].start > splices[j].start
		}
		return splices[i].end > splices[j].end
	})
	out := append([]byte(nil), src...)
	last := len(src) + 1
	for _, sp := range splices {
		if sp.end > last || sp.start > sp.end {
			return "", errors.New("two changes touch the same lines")
		}
		out = append(out[:sp.start], append([]byte(sp.text), out[sp.end:]...)...)
		last = sp.start
	}
	if _, err := parser.ParseFile("changed.cue", out); err != nil {
		return "", err
	}
	if was, err := format.Source(src); err == nil && bytes.Equal(was, src) {
		if f, err := format.Source(out); err == nil {
			out = f
		}
	}
	return string(out), nil
}

// penDiff is the change from one text to another as lines, with three lines of context around each change. It compares
// line by line after the common head and tail, which is what a change of the pen makes: one run of changed lines a struct.
func penDiff(a, b string) []BoardDiffLine {
	ops := lcsDiff(strings.Split(a, "\n"), strings.Split(b, "\n"))
	keep := make([]bool, len(ops))
	for i, op := range ops {
		if op[0] != ' ' {
			for j := max(0, i-3); j <= min(len(ops)-1, i+3); j++ {
				keep[j] = true
			}
		}
	}
	var out []BoardDiffLine
	gap := false
	was, now := 0, 0
	for i, op := range ops {
		l := BoardDiffLine{Op: op[:1], Text: op[1:]}
		switch l.Op {
		case "-":
			was++
			l.Line = was
		case "+":
			now++
			l.Line = now
		default:
			was++
			now++
			l.Line = now
		}
		if !keep[i] {
			gap = true
			continue
		}
		if gap && len(out) > 0 {
			out = append(out, BoardDiffLine{Op: "…"})
		}
		gap = false
		out = append(out, l)
	}
	if len(out) > 400 {
		out = append(out[:400], BoardDiffLine{Op: "…"})
	}
	return out
}

// lcsDiff is the diff of two lists of lines by their longest common subsequence, after the lines they share at the head
// and the tail. A middle of more than 2,000 lines on a side is shown as all taken out and all put in.
func lcsDiff(a, b []string) []string {
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	var out []string
	for _, l := range a[:head] {
		out = append(out, " "+l)
	}
	ma, mb := a[head:len(a)-tail], b[head:len(b)-tail]
	if len(ma) > 2000 || len(mb) > 2000 {
		for _, l := range ma {
			out = append(out, "-"+l)
		}
		for _, l := range mb {
			out = append(out, "+"+l)
		}
	} else {
		n, k := len(ma), len(mb)
		dp := make([][]int, n+1)
		for i := range dp {
			dp[i] = make([]int, k+1)
		}
		for i := n - 1; i >= 0; i-- {
			for j := k - 1; j >= 0; j-- {
				if ma[i] == mb[j] {
					dp[i][j] = dp[i+1][j+1] + 1
				} else {
					dp[i][j] = max(dp[i+1][j], dp[i][j+1])
				}
			}
		}
		i, j := 0, 0
		for i < n || j < k {
			switch {
			case i < n && j < k && ma[i] == mb[j]:
				out = append(out, " "+ma[i])
				i++
				j++
			case i < n && (j == k || dp[i+1][j] >= dp[i][j+1]):
				out = append(out, "-"+ma[i])
				i++
			default:
				out = append(out, "+"+mb[j])
				j++
			}
		}
	}
	for _, l := range a[len(a)-tail:] {
		out = append(out, " "+l)
	}
	return out
}

// penRead reads a file the pen may change: a regular file, not a link.
func penRead(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New(filepath.Base(path) + " is not a regular file")
	}
	return os.ReadFile(path)
}

// penReplace writes the new text of a file of the project: into a file beside it, then renamed over it, so a reader
// sees the old text or the new and never a part. The file keeps its mode.
func penReplace(path, text string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".board-*")
	if err != nil {
		return err
	}
	_, werr := tmp.WriteString(text)
	if werr == nil {
		werr = tmp.Chmod(fi.Mode().Perm())
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
	}
	return werr
}
