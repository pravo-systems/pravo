package serve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"
)

// The fold of the drawing board (README rule 14) turns resources of the specification into a template: a definition that
// holds them, written in a new file with one instance of it. The instance takes their records with $from, so a plan of the
// fold moves records and changes no object. What the resources read of other resources becomes an input of the template,
// which the instance gives; a value the person asks for becomes an input with that value as its default; what reads the
// resources reads them in the instance. Like the rest of the pen it writes only what it can write exactly: a resource it
// cannot lift as it is written, or a reference it cannot place, stops the fold with the reason.

// BoardFold is a fold on the board: which resources, and the name of the template they become.
type BoardFold struct {
	// Name is the name of the template, in small letters, digits and underscores: the definition is #Name in camel case,
	// and the file NAME.cue.
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
	// Label is the address of the instance; Name when empty.
	Label string   `json:"label,omitempty"`
	Addrs []string `json:"addrs"`
	// Ask are fields of the resources, as ADDR.FIELD, whose value as written the template asks for, with it as the default.
	Ask []string `json:"ask,omitempty"`
}

// BoardFolded is what a fold makes, for the page: the definition, the instance, its inputs, and the moves of the records.
type BoardFolded struct {
	Def    string           `json:"def"`
	Label  string           `json:"label"`
	File   string           `json:"file"`
	Inputs []BoardFoldInput `json:"inputs"`
	// Moves are the new addresses of the folded resources, by the old.
	Moves map[string]string `json:"moves"`
	// Fields are the fields of each folded resource that the template can ask for: values typed as they are.
	Fields map[string][]string `json:"fields"`
}

// BoardFoldInput is an input of a folded template: what a resource read of another, or a value the person asked for.
type BoardFoldInput struct {
	Name  string `json:"name"`
	From  string `json:"from"`
	Asked bool   `json:"asked,omitempty"`
}

const maxFoldAddrs = 24

var foldNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// foldWork is a fold as the pen makes it: the new file, and the changes to the files of the project.
type foldWork struct {
	file    BoardFileChange
	splices map[string][]penSplice
	srcs    map[string][]byte
	folded  *BoardFolded
}

// foldInput is an input as it is written into the definition.
type foldInput struct {
	BoardFoldInput
	decl, doc, value string
}

// foldDef is the name of the definition of a template: web_app is #WebApp.
func foldDef(name string) string {
	var b strings.Builder
	b.WriteByte('#')
	up := true
	for _, r := range name {
		if r == '_' {
			up = true
			continue
		}
		if up {
			r = unicode.ToUpper(r)
			up = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// foldPackage reads the files of the package that a fold may change, with the names they declare at the top.
type foldPackage struct {
	files   []string
	srcs    map[string][]byte
	parsed  map[string]*ast.File
	imports map[string]map[string]string // by file: the name of an import, and its path
	top     map[string]bool
	pkg     string
}

func (s *Server) foldPackage(m *model, sel map[string]*penTarget) (*foldPackage, string) {
	p := &foldPackage{srcs: map[string][]byte{}, parsed: map[string]*ast.File{}, imports: map[string]map[string]string{}, top: map[string]bool{}}
	given := map[string][]byte{}
	for _, t := range sel {
		given[t.abs] = t.src
	}
	for _, f := range s.penFiles(m) {
		src, ok := given[f]
		if !ok {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, filepath.Base(f) + " cannot be read."
			}
			src = b
		}
		file, err := parser.ParseFile(f, src, parser.ParseComments)
		if err != nil {
			return nil, filepath.Base(f) + " does not parse."
		}
		p.files = append(p.files, f)
		p.srcs[f], p.parsed[f] = src, file
		if name := file.PackageName(); name != "" {
			p.pkg = name
		}
		p.imports[f] = map[string]string{}
		for spec := range file.ImportSpecs() {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			name := path[strings.LastIndexAny(path, "/")+1:]
			if i := strings.IndexByte(name, ':'); i >= 0 {
				name = name[i+1:]
			}
			if spec.Name != nil {
				name = spec.Name.Name
			}
			p.imports[f][name] = path
		}
		for _, d := range file.Decls {
			if g, ok := d.(*ast.Field); ok {
				if name, _, err := ast.LabelName(g.Label); err == nil {
					p.top[name] = true
				}
			}
		}
	}
	if p.pkg == "" {
		return nil, "The files of the project have no package clause."
	}
	return p, ""
}

// boardFold makes the fold of a drawing: the new file of the template and its instance, and the splices that take the
// resources out of their files and point what reads them at the instance. A problem stops it.
func (s *Server) boardFold(m *model, d *BoardDraft, labels map[string]bool, problems *drawProblems) *foldWork {
	f := d.Fold
	label := f.Label
	if label == "" {
		label = f.Name
	}
	before := len(*problems)
	switch {
	case !foldNameRE.MatchString(f.Name):
		problems.add("@fold", "@name", "Name the template with small letters, digits and underscores, starting with a letter.")
	case !foldNameRE.MatchString(label):
		problems.add("@fold", "@label", "Name the instance with small letters, digits and underscores, starting with a letter.")
	case len(f.Addrs) == 0 || len(f.Addrs) > maxFoldAddrs:
		problems.add("@fold", "", "A fold takes from 1 to "+strconv.Itoa(maxFoldAddrs)+" resources.")
	case labels[label]:
		problems.add("@fold", "@label", "A thing drawn has the name of the instance.")
	}
	if len(*problems) > before {
		return nil
	}
	sel := s.foldTargets(m, d, problems)
	if len(*problems) > before {
		return nil
	}
	pk, reason := s.foldPackage(m, sel)
	if pk == nil {
		problems.add("@fold", "", reason)
		return nil
	}
	def, file := foldDef(f.Name), f.Name+".cue"
	switch {
	case pk.top[label]:
		problems.add("@fold", "@label", label+" is a name of the specification already.")
	case pk.top[def]:
		problems.add("@fold", "@name", def+" is a definition of the specification already.")
	case fileThere(filepath.Join(s.opts.Dir, file)):
		problems.add("@fold", "@name", file+" is there already: name the template otherwise.")
	}
	if len(*problems) > before {
		return nil
	}
	w := &foldWork{splices: map[string][]penSplice{}, srcs: pk.srcs}
	inner, inputs, needs, taken := s.foldRefs(m, pk, sel, label, problems)
	asked := foldAsk(sel, f.Ask, inner, taken, problems)
	if len(*problems) > before {
		return nil
	}
	inputs = append(inputs, asked...)
	text, ok := foldText(pk, sel, f, def, label, inner, inputs, needs, problems)
	if !ok {
		return nil
	}
	for _, a := range f.Addrs {
		t := sel[a]
		start, end, ok := penLines(t.src, penStart(t.field), t.field.End().Offset())
		if !ok {
			problems.add(a, "", "It shares its lines with other code: fold it in the code.")
			return nil
		}
		w.splices[t.abs] = append(w.splices[t.abs], penSplice{start: start, end: end})
	}
	for abs, sp := range inner.outside {
		w.splices[abs] = append(w.splices[abs], sp...)
	}
	w.file = BoardFileChange{File: file, New: true, Text: text, Lines: map[string][2]int{}, Diff: penDiff("", text)}
	w.folded = &BoardFolded{Def: def, Label: label, File: file, Inputs: []BoardFoldInput{}, Moves: map[string]string{}, Fields: map[string][]string{}}
	for _, in := range inputs {
		w.folded.Inputs = append(w.folded.Inputs, in.BoardFoldInput)
	}
	for _, a := range f.Addrs {
		w.folded.Moves[a] = label + "." + a
		w.folded.Fields[a] = foldAskable(sel[a])
	}
	return w
}

// fileThere says whether a path names anything.
func fileThere(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// foldTargets finds where each resource of a fold is written: at the top of a file, as one field, that no change of the
// drawing touches and that moves nowhere already.
func (s *Server) foldTargets(m *model, d *BoardDraft, problems *drawProblems) map[string]*penTarget {
	edited := map[string]bool{}
	for _, e := range d.Edits {
		edited[e.Addr] = true
	}
	sel := map[string]*penTarget{}
	srcs := map[string][]byte{}
	for _, a := range d.Fold.Addrs {
		if sel[a] != nil {
			problems.add(a, "", "The fold takes "+a+" twice.")
			continue
		}
		if edited[a] {
			problems.add(a, "", "It is changed on the board: fold it once the change is saved, or drop the change.")
			continue
		}
		t, reason := s.penFind(m, a)
		if t == nil {
			problems.add(a, "", reason)
			continue
		}
		if len(t.path) != 1 {
			problems.add(a, "", "Only a resource written at the top of a file is folded: this one is inside "+t.path[0]+".")
			continue
		}
		for _, el := range t.body.Elts {
			if g, ok := el.(*ast.Field); ok {
				if name, _, _ := ast.LabelName(g.Label); name == "$from" {
					problems.add(a, "", "It moves already ($from): fold it once that move is applied.")
				}
			}
		}
		// every resource of one file is cut from the same bytes
		if src, ok := srcs[t.abs]; ok {
			if penDigest(src) != penDigest(t.src) {
				problems.add(a, "", t.name+" changed while it was read: try again.")
				continue
			}
			t.src = src
		}
		srcs[t.abs] = t.src
		sel[a] = t
	}
	return sel
}

// foldInner are the splices that a fold makes inside the resources it lifts, by address, and outside them, by file.
type foldInner struct {
	inside  map[string][]penSplice
	outside map[string][]penSplice
}

// foldRefs reads every reference of the package to a name of its top level. A reference to a folded resource from
// outside the fold reads it in the instance; one from inside the fold to another resource becomes an input. needs are
// the imports that the lifted code reads.
func (s *Server) foldRefs(m *model, pk *foldPackage, sel map[string]*penTarget, label string, problems *drawProblems) (*foldInner, []foldInput, map[string]string, map[string]bool) {
	inner := &foldInner{inside: map[string][]penSplice{}, outside: map[string][]penSplice{}}
	names := map[string]bool{}
	for a := range sel {
		names[a] = true
	}
	for addr := range m.spec.byPath {
		top, _, _ := strings.Cut(addr, ".")
		names[top] = true
	}
	var inputs []foldInput
	byFrom := map[string]string{}
	taken := foldTaken(sel)
	needs := map[string]string{}
	refs := map[string][]penRef{}
	for _, abs := range pk.files {
		look := map[string]bool{}
		for n := range names {
			look[n] = true
		}
		for n := range pk.imports[abs] {
			look[n] = true
		}
		refs[abs] = penRefs(pk.parsed[abs], look)
	}
	for _, abs := range pk.files {
		for _, r := range refs[abs] {
			in := foldWithin(sel, abs, r.start)
			if in == "" {
				if sel[r.name] == nil {
					continue
				}
				if r.unsure {
					problems.add(r.name, "", "Something reads it in a way the board cannot point at the instance: fold it in the code.")
					continue
				}
				inner.outside[abs] = append(inner.outside[abs], penSplice{start: r.start, end: r.identEnd, text: label + "." + r.name})
				continue
			}
			if sel[r.name] != nil {
				continue // one folded resource reads another: they stay side by side in the definition
			}
			if path, ok := pk.imports[abs][r.name]; ok {
				if have, ok := needs[r.name]; ok && have != path {
					problems.add(in, "", "It reads the import "+r.name+", which names another package in another file: fold it in the code.")
				}
				needs[r.name] = path
				continue
			}
			res := foldResource(m, r.path)
			if res == "" {
				continue // a value of the package: the definition reads it as the resource did
			}
			if r.unsure {
				problems.add(in, "", "It reads "+res+" in a way the board cannot make an input of: fold it in the code.")
				continue
			}
			from := strings.Join(r.path, ".")
			name, ok := byFrom[from]
			if !ok {
				name = foldInputName(strings.Join(r.path, "_"), taken)
				byFrom[from] = name
				inputs = append(inputs, foldInput{BoardFoldInput: BoardFoldInput{Name: name, From: from}, decl: s.foldInputType(m, res, r.path), doc: "What the template reads of " + res + ".", value: from})
			}
			inner.inside[in] = append(inner.inside[in], penSplice{start: r.start, end: r.end, text: name})
		}
	}
	return inner, inputs, needs, taken
}

// foldTaken are the names an input may not take: a name inside the definition reads the input in place of what it read
// before. They are every name written in the resources that are lifted, and the fields of the definition.
func foldTaken(sel map[string]*penTarget) map[string]bool {
	taken := map[string]bool{"$template": true}
	for a, t := range sel {
		taken[a] = true
		ast.Walk(t.field, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				taken[id.Name] = true
			}
			return true
		}, nil)
	}
	return taken
}

// foldWithin is the folded resource whose field holds an offset of a file, or "".
func foldWithin(sel map[string]*penTarget, abs string, off int) string {
	for a, t := range sel {
		if t.abs == abs && off >= penStart(t.field) && off < t.field.End().Offset() {
			return a
		}
	}
	return ""
}

// foldResource is the address of the resource that a chain of selectors reads, the longest that is one, or "".
func foldResource(m *model, path []string) string {
	for i := len(path); i > 0; i-- {
		addr := strings.Join(path[:i], ".")
		if res := m.spec.byPath[addr]; res != nil && (res.Mode == "managed" || res.Mode == "data") {
			return addr
		}
	}
	return ""
}

// foldInputName is a name for an input that nothing takes yet.
func foldInputName(base string, taken map[string]bool) string {
	var b strings.Builder
	for _, r := range base {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	name := b.String()
	if name == "" || name[0] >= '0' && name[0] <= '9' {
		name = "in_" + name
	}
	for i := 2; taken[name]; i++ {
		name = strings.TrimRight(b.String(), "_") + "_" + strconv.Itoa(i)
	}
	taken[name] = true
	return name
}

// foldInputType is the CUE type of what an input reads: of an attribute of a resource, as its schema says, else _.
func (s *Server) foldInputType(m *model, addr string, path []string) string {
	res := m.spec.byPath[addr]
	rest := path[len(strings.Split(addr, ".")):]
	if res == nil || len(rest) != 1 {
		return "_"
	}
	p := s.penProvider(m, res)
	if p == nil {
		return "_"
	}
	sch := p.sch.Type(res.Mode, res.Type)
	if sch == nil || sch.Block == nil {
		return "_"
	}
	for _, at := range sch.Block.Attributes {
		if at.Name != rest[0] {
			continue
		}
		var typ string
		if json.Unmarshal(at.TypeJSON, &typ) == nil {
			switch typ {
			case "string":
				return "string"
			case "number":
				return "number"
			case "bool":
				return "bool"
			}
		}
	}
	return "_"
}

// foldAskable are the fields of a resource whose value is typed as it is: a string, a number, true or false.
func foldAskable(t *penTarget) []string {
	out := []string{}
	for _, el := range t.body.Elts {
		if g, ok := el.(*ast.Field); ok {
			if name, _, err := ast.LabelName(g.Label); err == nil && !strings.HasPrefix(name, "$") && foldLiteralKind(g.Value) != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// foldLiteralKind is the CUE type of a value typed as it is, or "".
func foldLiteralKind(x ast.Expr) string {
	if u, ok := x.(*ast.UnaryExpr); ok && u.Op == token.SUB {
		x = u.X
	}
	lit, ok := x.(*ast.BasicLit)
	if !ok {
		return ""
	}
	switch lit.Kind {
	case token.STRING:
		if strings.HasPrefix(lit.Value, "'") {
			return ""
		}
		return "string"
	case token.INT:
		return "int"
	case token.FLOAT:
		return "number"
	case token.TRUE, token.FALSE:
		return "bool"
	}
	return ""
}

// foldAsk makes an input of each field the person asks for: the value as written is its default.
func foldAsk(sel map[string]*penTarget, ask []string, inner *foldInner, taken map[string]bool, problems *drawProblems) []foldInput {
	var out []foldInput
	seen := map[string]bool{}
	for _, q := range ask {
		addr, field, _ := strings.Cut(q, ".")
		t := sel[addr]
		if t == nil || field == "" || seen[q] {
			problems.add("@fold", "", q+" is not a field of a resource of the fold.")
			continue
		}
		seen[q] = true
		var g *ast.Field
		for _, el := range t.body.Elts {
			if f, ok := el.(*ast.Field); ok {
				if name, _, _ := ast.LabelName(f.Label); name == field {
					g = f
				}
			}
		}
		kind := ""
		if g != nil {
			kind = foldLiteralKind(g.Value)
		}
		if kind == "" {
			problems.add(addr, field, "Only a value typed as it is (a string, a number, true or false) is asked for.")
			continue
		}
		name := foldInputName(addr+"_"+field, taken)
		lit := string(t.src[g.Value.Pos().Offset():g.Value.End().Offset()])
		out = append(out, foldInput{BoardFoldInput: BoardFoldInput{Name: name, From: q, Asked: true}, decl: "*" + lit + " | " + kind, doc: "The " + field + " of " + addr + ": " + lit + ", as it was written."})
		inner.inside[addr] = append(inner.inside[addr], penSplice{start: g.Value.Pos().Offset(), end: g.Value.End().Offset(), text: name})
	}
	return out
}

// foldText writes the new file: the definition, with the inputs and the resources as they were written, and the instance.
func foldText(pk *foldPackage, sel map[string]*penTarget, f *BoardFold, def, label string, inner *foldInner, inputs []foldInput, needs map[string]string, problems *drawProblems) (string, bool) {
	if p, ok := needs["pravo"]; ok && p != "pravo.dev/pravo" {
		problems.add("@fold", "", "The resources read an import named pravo that is not pravo.dev/pravo: fold them in the code.")
		return "", false
	}
	needs["pravo"] = "pravo.dev/pravo"
	var b strings.Builder
	names := make([]string, 0, len(needs))
	for n := range needs {
		names = append(names, n)
	}
	sort.Strings(names)
	specs := make([]string, 0, len(names))
	for _, n := range names {
		p := needs[n]
		if n == p[strings.LastIndexByte(p, '/')+1:] {
			specs = append(specs, strconv.Quote(p))
		} else {
			specs = append(specs, n+" "+strconv.Quote(p))
		}
	}
	b.WriteString("package " + pk.pkg + "\n\n")
	if len(specs) == 1 {
		b.WriteString("import " + specs[0] + "\n")
	} else {
		b.WriteString("import (\n\t" + strings.Join(specs, "\n\t") + "\n)\n")
	}
	title := f.Title
	if title == "" {
		title = strings.ToUpper(f.Name[:1]) + strings.ReplaceAll(f.Name[1:], "_", " ")
	}
	b.WriteString("\n// " + def[1:] + " was folded on the drawing board from " + strings.Join(f.Addrs, ", ") + ".\n")
	b.WriteString(def + ": pravo.#Template & {\n\t$template: {\n\t\tname:  " + strconv.Quote(f.Name) + "\n\t\ttitle: " + strconv.Quote(title) + "\n\t}\n")
	for _, in := range inputs {
		b.WriteString("\n\t// " + in.doc + "\n\t" + in.Name + ": " + in.decl + "\n")
	}
	for _, a := range f.Addrs {
		t := sel[a]
		start, end := penStart(t.field), t.field.End().Offset()
		var rel []penSplice
		for _, sp := range inner.inside[a] {
			rel = append(rel, penSplice{start: sp.start - start, end: sp.end - start, text: sp.text})
		}
		body, err := penSpliced(t.src[start:end], rel)
		if err != nil {
			problems.add(a, "", "It cannot be lifted as it is written: "+clip(err.Error(), 200))
			return "", false
		}
		b.WriteString("\n\t" + strings.ReplaceAll(body, "\n", "\n\t") + "\n")
	}
	b.WriteString("}\n\n" + label + ": " + def + " & {\n")
	for _, in := range inputs {
		if !in.Asked {
			b.WriteString("\t" + in.Name + ": " + in.value + "\n")
		}
	}
	for _, a := range f.Addrs {
		b.WriteString("\t" + a + ": $from: " + strconv.Quote(a) + "\n")
	}
	b.WriteString("}\n")
	out, err := format.Source([]byte(b.String()))
	if err != nil {
		problems.add("@fold", "", "The fold does not make CUE: "+clip(err.Error(), 200))
		return "", false
	}
	return string(out), true
}

// penSpliced is a text with splices made in it, without the check and the format of a whole file.
func penSpliced(src []byte, splices []penSplice) (string, error) {
	sort.Slice(splices, func(i, j int) bool { return splices[i].start > splices[j].start })
	out := append([]byte(nil), src...)
	last := len(src) + 1
	for _, sp := range splices {
		if sp.start < 0 || sp.end > last || sp.start > sp.end {
			return "", errOverlap
		}
		out = append(out[:sp.start], append([]byte(sp.text), out[sp.end:]...)...)
		last = sp.start
	}
	return string(out), nil
}

// penRef is a reference in a file to a name of the top level of the package: the identifier, and the chain of selectors
// that it starts, as a path and as the span of the chain.
type penRef struct {
	name                 string
	path                 []string
	start, end, identEnd int
	// unsure is a reference the walk found where it does not read the syntax: it is not changed.
	unsure bool
}

// penRefs finds the references in a file to names of the top level of the package: identifiers that no struct, let,
// alias or comprehension around them declares. The declarations of the top level of the file are the package's own.
func penRefs(f *ast.File, names map[string]bool) []penRef {
	w := &refWalk{names: names}
	for _, d := range f.Decls {
		switch x := d.(type) {
		case *ast.Field:
			w.label(x.Label, nil)
			w.expr(x.Value, nil)
		case *ast.EmbedDecl:
			w.expr(x.Expr, nil)
		case *ast.LetClause:
			w.expr(x.Expr, nil)
		case *ast.Comprehension:
			w.comprehension(x, nil)
		case *ast.ImportDecl, *ast.Package, *ast.CommentGroup, *ast.Attribute:
		default:
			w.unsure(d, nil)
		}
	}
	return w.refs
}

type refWalk struct {
	names map[string]bool
	refs  []penRef
}

// declared says whether a scope around a place declares a name.
func declared(scopes []map[string]bool, name string) bool {
	for _, sc := range scopes {
		if sc[name] {
			return true
		}
	}
	return false
}

// unsure reports every identifier with a name of the package under a node the walk does not read.
func (w *refWalk) unsure(n ast.Node, scopes []map[string]bool) {
	ast.Walk(n, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && w.names[id.Name] && !declared(scopes, id.Name) {
			w.refs = append(w.refs, penRef{name: id.Name, path: []string{id.Name}, start: id.Pos().Offset(), end: id.End().Offset(), identEnd: id.End().Offset(), unsure: true})
		}
		return true
	}, nil)
}

// label reads the expressions in a label: a dynamic label or a pattern. It returns the name an alias in it declares.
func (w *refWalk) label(l ast.Label, scopes []map[string]bool) string {
	switch x := l.(type) {
	case *ast.Ident, *ast.BasicLit:
		return ""
	case *ast.Alias:
		if id, ok := x.Expr.(ast.Label); ok {
			w.label(id, scopes)
		} else {
			w.expr(x.Expr, scopes)
		}
		return x.Ident.Name
	case *ast.ParenExpr:
		w.expr(x.X, scopes)
	case *ast.Interpolation:
		w.expr(x, scopes)
	case *ast.ListLit:
		name := ""
		for _, el := range x.Elts {
			if a, ok := el.(*ast.Alias); ok {
				w.expr(a.Expr, scopes)
				name = a.Ident.Name
				continue
			}
			w.expr(el, scopes)
		}
		return name
	default:
		w.unsure(l, scopes)
	}
	return ""
}

func (w *refWalk) structLit(x *ast.StructLit, scopes []map[string]bool) {
	sc := map[string]bool{}
	for _, el := range x.Elts {
		switch e := el.(type) {
		case *ast.Field:
			if name, _, err := ast.LabelName(e.Label); err == nil {
				sc[name] = true
			}
			if a, ok := e.Label.(*ast.Alias); ok {
				sc[a.Ident.Name] = true
			}
		case *ast.LetClause:
			sc[e.Ident.Name] = true
		}
	}
	inner := append(append([]map[string]bool{}, scopes...), sc)
	for _, el := range x.Elts {
		switch e := el.(type) {
		case *ast.Field:
			if alias := w.label(e.Label, inner); alias != "" {
				if _, isList := e.Label.(*ast.ListLit); isList {
					w.expr(e.Value, append(append([]map[string]bool{}, inner...), map[string]bool{alias: true}))
					continue
				}
			}
			w.expr(e.Value, inner)
		case *ast.EmbedDecl:
			w.expr(e.Expr, inner)
		case *ast.LetClause:
			w.expr(e.Expr, inner)
		case *ast.Comprehension:
			w.comprehension(e, inner)
		case *ast.Ellipsis:
			if e.Type != nil {
				w.expr(e.Type, inner)
			}
		case *ast.CommentGroup, *ast.Attribute:
		default:
			w.unsure(el, inner)
		}
	}
}

func (w *refWalk) comprehension(c *ast.Comprehension, scopes []map[string]bool) {
	sc := map[string]bool{}
	inner := append(append([]map[string]bool{}, scopes...), sc)
	for _, cl := range c.Clauses {
		switch x := cl.(type) {
		case *ast.ForClause:
			w.expr(x.Source, inner)
			if x.Key != nil {
				sc[x.Key.Name] = true
			}
			if x.Value != nil {
				sc[x.Value.Name] = true
			}
		case *ast.IfClause:
			w.expr(x.Condition, inner)
		case *ast.LetClause:
			w.expr(x.Expr, inner)
			sc[x.Ident.Name] = true
		default:
			w.unsure(cl, inner)
		}
	}
	w.expr(c.Value, inner)
}

func (w *refWalk) expr(x ast.Expr, scopes []map[string]bool) {
	switch e := x.(type) {
	case nil, *ast.BasicLit, *ast.BottomLit:
	case *ast.Ident:
		if w.names[e.Name] && !declared(scopes, e.Name) {
			w.refs = append(w.refs, penRef{name: e.Name, path: []string{e.Name}, start: e.Pos().Offset(), end: e.End().Offset(), identEnd: e.End().Offset()})
		}
	case *ast.SelectorExpr:
		var sels []string
		base := ast.Expr(e)
		for {
			s, ok := base.(*ast.SelectorExpr)
			if !ok {
				break
			}
			name, _, err := ast.LabelName(s.Sel)
			if err != nil {
				w.expr(s.X, scopes)
				return
			}
			sels = append([]string{name}, sels...)
			base = s.X
		}
		id, ok := base.(*ast.Ident)
		if !ok {
			w.expr(base, scopes)
			return
		}
		if w.names[id.Name] && !declared(scopes, id.Name) {
			w.refs = append(w.refs, penRef{name: id.Name, path: append([]string{id.Name}, sels...), start: id.Pos().Offset(), end: e.End().Offset(), identEnd: id.End().Offset()})
		}
	case *ast.StructLit:
		w.structLit(e, scopes)
	case *ast.ListLit:
		for _, el := range e.Elts {
			w.expr(el, scopes)
		}
	case *ast.Comprehension:
		w.comprehension(e, scopes)
	case *ast.Interpolation:
		for _, el := range e.Elts {
			w.expr(el, scopes)
		}
	case *ast.ParenExpr:
		w.expr(e.X, scopes)
	case *ast.UnaryExpr:
		w.expr(e.X, scopes)
	case *ast.BinaryExpr:
		w.expr(e.X, scopes)
		w.expr(e.Y, scopes)
	case *ast.IndexExpr:
		w.expr(e.X, scopes)
		w.expr(e.Index, scopes)
	case *ast.SliceExpr:
		w.expr(e.X, scopes)
		w.expr(e.Low, scopes)
		w.expr(e.High, scopes)
	case *ast.CallExpr:
		w.expr(e.Fun, scopes)
		for _, a := range e.Args {
			w.expr(a, scopes)
		}
	case *ast.Ellipsis:
		w.expr(e.Type, scopes)
	default:
		w.unsure(x, scopes)
	}
}
