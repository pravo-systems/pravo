package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/parser"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/schema"
)

// The drawing board (README rule 14). A person draws new resources into the Inventory graph: a resource of a provider
// the specification declares, or an instance of a template. The page holds the drawing; the server turns it into the
// CUE of one new file, loads the specification with that file laid over it in memory, and says what CUE, the schemas
// and the policies say of it, as the template wizard does. The code is the only thing that is kept: no position, no
// layout, nothing that is not the specification. With Options.Board (pravo serve --edit) the board can also write that
// file, once, under a name that no file has, and run a real plan of the specification with the drawing in it. Without
// it the page shows the code to copy, and nothing is written.

// BoardOptions turn on the writes of the board: saving the drawing as a new file of the specification, and planning.
type BoardOptions struct {
	// Plan runs a plan of the specification in the project directory, loaded with the tags of the request and the files
	// of Extra laid over it, as "pravo plan" would make it, and reports its steps to step as they happen. It is the
	// command's: the server starts nothing. The result carries no value.
	Plan func(ctx context.Context, req BoardPlanRequest, step func(BoardStep)) (*BoardPlanResult, error)
	// Actor is who draws, for the title block: the actor of the command, else the user of the operating system.
	Actor string
}

// BoardPlanRequest is what a plan of the board is made of.
type BoardPlanRequest struct {
	Tags    []string
	SysTags bool
	// Extra holds the drawing, by the name of its file, when it is not saved yet.
	Extra map[string]string
}

// BoardStep is one step of a plan as it runs: a resource that is being planned, or a stage of the run.
type BoardStep struct {
	Addr  string `json:"addr,omitempty"`
	Kind  string `json:"kind"`
	Phase string `json:"phase"`
}

// BoardPlanResult is what a plan of the board found: the action for each node it changes, and what the policies said.
type BoardPlanResult struct {
	Changes []BoardChange `json:"changes"`
	Add     int           `json:"add"`
	Change  int           `json:"change"`
	Destroy int           `json:"destroy"`
	// Refused is what a policy in force said against the plan, empty when they all hold.
	Refused string `json:"refused,omitempty"`
}

// BoardChange is the action of a plan for one node.
type BoardChange struct {
	Addr   string `json:"addr"`
	Action string `json:"action"`
}

// BoardConfig is the answer of GET /api/board.
type BoardConfig struct {
	Version int `json:"version"`
	// Edit is set when the server was started with --edit: the board can save and plan.
	Edit bool `json:"edit"`
	// Actor is who draws, for the title block.
	Actor   string `json:"actor,omitempty"`
	Package string `json:"package,omitempty"`
	// File is the name the board suggests for the new file: board.cue, or board-2.cue when that is taken.
	File      string          `json:"file"`
	Providers []BoardProvider `json:"providers"`
	Templates int             `json:"templates"`
	// Problem says why nothing can be drawn: the specification does not load, or declares no provider whose schema is
	// cached.
	Problem string `json:"problem,omitempty"`
}

// BoardProvider is a provider the board draws with.
type BoardProvider struct {
	// Label is the struct of the provider in the specification, such as $aws.
	Label   string `json:"label"`
	Local   string `json:"local"`
	Source  string `json:"source"`
	Version string `json:"version"`
	Types   int    `json:"types"`
}

// BoardDraft is the drawing that the page sends: the body of POST /api/board/check, /save and /plan.
type BoardDraft struct {
	// File is the name of the new file; empty for the one the board suggests.
	File  string      `json:"file,omitempty"`
	Items []BoardItem `json:"items"`
}

// BoardItem is one thing drawn: a resource or an instance of a template.
type BoardItem struct {
	// Label is its name, and its address: the drawing goes at the top level of the specification.
	Label string `json:"label"`
	// Type and Provider name a resource type and the provider struct that runs it ($aws); Template names a template.
	Type     string                `json:"type,omitempty"`
	Provider string                `json:"provider,omitempty"`
	Template string                `json:"template,omitempty"`
	Values   map[string]BoardValue `json:"values,omitempty"`
}

// BoardValue is what one field is set to: a string, CUE as written, or a wire to another resource (or several).
type BoardValue struct {
	Text  *string    `json:"text,omitempty"`
	Expr  string     `json:"expr,omitempty"`
	Wire  *BoardRef  `json:"wire,omitempty"`
	Wires []BoardRef `json:"wires,omitempty"`
}

// BoardRef is a wire: what it reads, of what. To is the label of a thing drawn, or the address of a node of the
// specification.
type BoardRef struct {
	To   string `json:"to"`
	Read string `json:"read"`
}

// BoardCheck is the answer of POST /api/board/check.
type BoardCheck struct {
	Version int    `json:"version"`
	File    string `json:"file"`
	// Code is the whole new file, and Lines the first and last line of each thing drawn in it.
	Code  string            `json:"code"`
	Lines map[string][2]int `json:"lines"`
	// State is the lamp of the specification: "empty" (nothing drawn), "valid", "incomplete" (a field that needs a
	// value has none), "invalid" (CUE or the schema says no), or "exists" (a file of that name is there already).
	State    string         `json:"state"`
	Problems []BoardProblem `json:"problems"`
	// Creates are the resources the drawing adds, by address.
	Creates []TemplateCreate `json:"creates"`
	// Reads are, for each thing drawn, the nodes of the specification and the other things drawn that its code reads, as
	// the engine reads its references: a wire, and a reference in an expression that was typed.
	Reads map[string][]string `json:"reads,omitempty"`
	// Policy is the lamp of the policies, judged on an approximate plan that creates them.
	Policy *TemplatePolicy `json:"policy,omitempty"`
	Took   int64           `json:"took_ms"`
}

// BoardProblem is one thing wrong with the drawing: Item is the label of the thing drawn it is about, Field the field.
type BoardProblem struct {
	Item    string `json:"item,omitempty"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// BoardSaved is the answer of POST /api/board/save.
type BoardSaved struct {
	Version int    `json:"version"`
	File    string `json:"file"`
	Bytes   int    `json:"bytes"`
	// Commands are what a person runs next, to copy: the plan and the apply. The board applies nothing.
	Commands []string `json:"commands"`
}

const (
	maxBoardBody  = 256 << 10
	maxBoardItems = 64
	maxBoardExpr  = 4096
	boardDefault  = "board"
)

var (
	boardLabelOK = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	boardFileOK  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,60}\.cue$`)
	boardReadOK  = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,63}$`)
	// cueKeywords are the words a label of the top level cannot be: the keywords of CUE, and the identifiers it
	// declares, which a field of that name would hide from the rest of the package.
	cueKeywords = map[string]bool{
		"package": true, "import": true, "for": true, "in": true, "if": true, "let": true, "true": true, "false": true, "null": true,
		"bool": true, "bytes": true, "string": true, "number": true, "int": true, "float": true, "len": true, "close": true,
		"and": true, "or": true, "div": true, "mod": true, "quo": true, "rem": true, "error": true, "func": true,
		"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uint128": true, "int8": true, "int16": true,
		"int32": true, "int64": true, "int128": true, "float32": true, "float64": true, "rune": true, "byte": true, "matchN": true, "matchIf": true,
	}
)

// boardModel is what the board works out once for a revision of the model.
type boardModel struct {
	pkg   string
	provs []*boardProv
	// top are the labels of the top level of the specification, which a thing drawn cannot take.
	top   map[string]bool
	wires *wireIndex
	tpls  []TemplateEntry
}

// boardProv is a provider the board draws with: declared at the top level, locked, with its schema cached.
type boardProv struct {
	BoardProvider
	sch   Schemas
	types []string
	set   map[string]bool
}

func (s *Server) boardData(m *model) *boardModel {
	m.boardOnce.Do(func() {
		bm := &boardModel{top: map[string]bool{}}
		m.board = bm
		root := m.spec.root
		if root == nil {
			return
		}
		bm.pkg = root.PkgName
		m.spec.cueMu.Lock()
		if it, err := root.Value.Fields(cue.All()); err == nil {
			for it.Next() {
				bm.top[it.Selector().String()] = true
			}
		}
		m.spec.cueMu.Unlock()
		seen := map[string]bool{}
		for _, d := range root.ProviderDecls {
			sels := d.Path.Selectors()
			if len(sels) != 1 || seen[d.Path.String()] {
				continue // a provider inside a struct is not reached from the top level of a new file
			}
			label := d.Path.String()
			if !cueIdent.MatchString(label) && !strings.HasPrefix(label, "$") {
				continue
			}
			if m.spec.lock == nil {
				break
			}
			e, locked := m.spec.lock.Providers[d.Source.String()]
			if !locked {
				continue
			}
			sch, ok := s.lookupSchema(d.Source, e.Version)
			if !ok {
				continue
			}
			p := &boardProv{BoardProvider: BoardProvider{Label: label, Local: d.Local, Source: d.Source.String(), Version: e.Version}, sch: sch, set: map[string]bool{}}
			if l, ok := sch.(interface{ ResourceTypes() []string }); ok {
				p.types = l.ResourceTypes()
			}
			for _, t := range p.types {
				p.set[t] = true
			}
			p.Types = len(p.types)
			seen[label] = true
			bm.provs = append(bm.provs, p)
		}
		var all []string
		bySet := map[string]*boardProv{}
		for _, p := range bm.provs {
			for _, t := range p.types {
				if bySet[t] == nil {
					bySet[t] = p
					all = append(all, t)
				}
			}
		}
		bm.wires = newWireIndex(all, func(typ string) *schema.Block {
			if p := bySet[typ]; p != nil {
				if sch := p.sch.Type("managed", typ); sch != nil {
					return sch.Block
				}
			}
			return nil
		})
		// A template whose definition is reached from the top level of the package: one that needs an import is
		// pasted with the wizard.
		for _, t := range m.templates().Templates {
			if cueIdent.MatchString(strings.TrimPrefix(t.Definition, "#")) && strings.HasPrefix(t.Definition, "#") && len(t.Problems) == 0 {
				bm.tpls = append(bm.tpls, t)
			}
		}
	})
	return m.board
}

func (s *Server) boardProviders(m *model) []*boardProv { return s.boardData(m).provs }

func (s *Server) boardWires(m *model) *wireIndex {
	if ix := s.boardData(m).wires; ix != nil {
		return ix
	}
	return newWireIndex(nil, nil)
}

func (s *Server) boardTemplates(m *model) []TemplateEntry { return s.boardData(m).tpls }

func (s *Server) boardTemplate(m *model, name string) *TemplateEntry {
	ts := s.boardTemplates(m)
	for i := range ts {
		if ts[i].Name == name {
			return &ts[i]
		}
	}
	return nil
}

// boardProvider is the provider of the label that has the type, or, with no label, the first that has it.
func (s *Server) boardProvider(m *model, label, typ string) *boardProv {
	for _, p := range s.boardProviders(m) {
		if (label == "" || p.Label == label) && p.set[typ] {
			return p
		}
	}
	return nil
}

func (s *Server) boardSchema(m *model, label, typ string) *schema.Schema {
	p := s.boardProvider(m, label, typ)
	if p == nil {
		return nil
	}
	return p.sch.Type("managed", typ)
}

// boardFile is the name the board suggests: board.cue, else the first board-N.cue that no file has.
func (s *Server) boardFile() string {
	name := boardDefault + ".cue"
	for i := 2; i < 100; i++ {
		if _, err := os.Lstat(filepath.Join(s.opts.Dir, name)); errors.Is(err, os.ErrNotExist) {
			return name
		}
		name = fmt.Sprintf("%s-%d.cue", boardDefault, i)
	}
	return name
}

// boardConfigHandler answers GET /api/board.
func (s *Server) boardConfigHandler(w http.ResponseWriter, m *model) {
	out := &BoardConfig{Version: APIVersion, Edit: s.opts.Board != nil, Providers: []BoardProvider{}, File: s.boardFile()}
	if s.opts.Board != nil {
		out.Actor = clip(stripControl(s.opts.Board.Actor), 64)
	}
	bm := s.boardData(m)
	out.Package = bm.pkg
	for _, p := range bm.provs {
		out.Providers = append(out.Providers, p.BoardProvider)
	}
	out.Templates = len(bm.tpls)
	switch {
	case m.spec.root == nil:
		out.Problem = "The specification does not load, so nothing can be drawn into it."
	case len(bm.provs) == 0 && len(bm.tpls) == 0:
		out.Problem = "The specification declares no provider whose schema is cached, and no template. Run pravo init, then draw."
	}
	writeJSON(w, http.StatusOK, out)
}

// readBoardDraft reads the drawing from the body of a POST.
func readBoardDraft(w http.ResponseWriter, r *http.Request) (*BoardDraft, bool) {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "the drawing is sent as application/json")
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBoardBody+1))
	if err != nil || len(body) > maxBoardBody {
		writeError(w, http.StatusRequestEntityTooLarge, "the drawing is too large")
		return nil, false
	}
	var d BoardDraft
	if err := json.Unmarshal(body, &d); err != nil {
		writeError(w, http.StatusBadRequest, "the drawing is not a drawing: "+clip(err.Error(), 200))
		return nil, false
	}
	if len(d.Items) > maxBoardItems {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("a drawing holds at most %d things", maxBoardItems))
		return nil, false
	}
	return &d, true
}

// boardCode is the new file of a drawing.
type boardCode struct {
	file  string
	text  string
	lines map[string][2]int
	// missing are the fields that need a value and have none.
	missing []BoardProblem
}

// boardGenerate writes the CUE of a drawing. A problem that it returns means that no code could be written for the
// drawing as it is: a name that cannot be used, a type that no provider has, an expression that does not parse.
func (s *Server) boardGenerate(m *model, d *BoardDraft) (*boardCode, []BoardProblem) {
	bm := s.boardData(m)
	code := &boardCode{file: d.File, lines: map[string][2]int{}}
	if code.file == "" {
		code.file = s.boardFile()
	}
	var problems []BoardProblem
	bad := func(item, field, msg string) {
		problems = append(problems, BoardProblem{Item: item, Field: field, Message: msg})
	}
	if !boardFileOK.MatchString(code.file) || code.file == previewFile {
		bad("", "@file", "The file is named with small letters, digits, dashes and underscores, and ends in .cue.")
		return nil, problems
	}
	if bm.pkg == "" {
		bad("", "", "The specification does not load, so nothing can be drawn into it.")
		return nil, problems
	}
	labels := map[string]bool{}
	for _, it := range d.Items {
		switch {
		case !boardLabelOK.MatchString(it.Label) || cueKeywords[it.Label]:
			bad(it.Label, "@label", "A name is small letters, digits and underscores, starts with a letter, and is no word of CUE.")
		case labels[it.Label]:
			bad(it.Label, "@label", "Two things are named "+it.Label+".")
		case bm.top[it.Label]:
			bad(it.Label, "@label", it.Label+" is taken: the specification has a field of that name at the top level.")
		}
		labels[it.Label] = true
	}
	if len(problems) > 0 {
		return nil, problems
	}
	type field struct{ name, expr string }
	type block struct {
		label, head string
		fields      []field
		heads       map[string]bool // the identifiers its references start with
	}
	var blocks []block
	lets := map[string]bool{}
	for _, it := range d.Items {
		bl := block{label: it.Label, heads: map[string]bool{}}
		var order []string // the fields that the form shows, in its order
		known := map[string]BoardField{}
		switch {
		case it.Template != "":
			t := s.boardTemplate(m, it.Template)
			if t == nil {
				bad(it.Label, "", "No template of the specification is named "+strconv.Quote(clip(it.Template, 60))+".")
				continue
			}
			bl.head = t.Definition
			for _, f := range boardTemplateForm(t).Fields {
				order = append(order, f.Name)
				known[f.Name] = f
			}
		default:
			p := s.boardProvider(m, it.Provider, it.Type)
			if p == nil {
				bad(it.Label, "", "No provider of the specification has the type "+strconv.Quote(clip(it.Type, 80))+".")
				continue
			}
			sch := p.sch.Type("managed", it.Type)
			if sch == nil || sch.Block == nil {
				bad(it.Label, "", "The schema of "+it.Type+" is not cached.")
				continue
			}
			bl.head = p.Label + ".#Resource." + it.Type
			for _, f := range s.boardForm(m, p, it.Type, sch.Block).Fields {
				order = append(order, f.Name)
				known[f.Name] = f
			}
		}
		for name := range it.Values {
			if _, ok := known[name]; !ok {
				bad(it.Label, name, "There is no field "+strconv.Quote(clip(name, 60))+" to set.")
			}
		}
		for _, name := range order {
			f := known[name]
			v, set := it.Values[name]
			if !set {
				if f.Required {
					code.missing = append(code.missing, BoardProblem{Item: it.Label, Field: name, Message: "needs a value"})
				}
				continue
			}
			expr, heads, msg := boardExpr(v, f, it.Label, labels, m)
			if msg != "" {
				bad(it.Label, name, msg)
				continue
			}
			for h := range heads {
				bl.heads[h] = true
			}
			bl.fields = append(bl.fields, field{name, expr})
		}
		// A reference that starts with the name of a field of its own struct would read that field: it reads an alias
		// of the file instead.
		for i, f := range bl.fields {
			for h := range bl.heads {
				for _, g := range bl.fields {
					if g.name == h {
						bl.fields[i].expr = replaceHead(f.expr, h, "ref_"+h)
						lets[h] = true
					}
				}
			}
		}
		blocks = append(blocks, bl)
	}
	if len(problems) > 0 {
		return nil, problems
	}
	var b strings.Builder
	b.WriteString("package " + bm.pkg + "\n\n// Drawn on the drawing board of pravo serve.\n\n")
	var names []string
	for h := range lets {
		names = append(names, h)
	}
	sort.Strings(names)
	for _, h := range names {
		if bm.top["ref_"+h] || labels["ref_"+h] {
			bad("", "", "ref_"+h+" is taken, and the drawing needs it to read "+h+".")
			return nil, problems
		}
		b.WriteString("let ref_" + h + " = " + h + "\n")
	}
	if len(names) > 0 {
		b.WriteString("\n")
	}
	for _, bl := range blocks {
		b.WriteString(bl.label + ": " + bl.head + " & {")
		for _, f := range bl.fields {
			b.WriteString("\n\t" + cueLabel(f.name) + ": " + f.expr)
		}
		if len(bl.fields) > 0 {
			b.WriteString("\n")
		}
		b.WriteString("}\n\n")
	}
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		bad("", "", "The drawing does not make CUE: "+clip(err.Error(), 300))
		return nil, problems
	}
	code.text = string(src)
	if f, err := parser.ParseFile(code.file, src); err == nil {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.Field)
			if !ok {
				continue
			}
			if id, ok := fd.Label.(*ast.Ident); ok && labels[id.Name] {
				code.lines[id.Name] = [2]int{fd.Pos().Line(), fd.End().Line()}
			}
		}
	}
	return code, nil
}

// boardExpr is the CUE of one value, and the identifiers its references start with; msg says why there is none.
func boardExpr(v BoardValue, f BoardField, self string, labels map[string]bool, m *model) (expr string, heads map[string]bool, msg string) {
	heads = map[string]bool{}
	n := 0
	if v.Text != nil {
		n++
	}
	if v.Expr != "" {
		n++
	}
	if v.Wire != nil {
		n++
	}
	if len(v.Wires) > 0 {
		n++
	}
	if n != 1 {
		return "", nil, "A field is set to one thing: a text, an expression or a wire."
	}
	// A sensitive field takes no value that is typed: the code would hold the secret in the clear. It takes a wire, or CUE
	// that reads something, such as a secret of the specification.
	secret := f.Sensitive || f.WriteOnly
	switch {
	case v.Text != nil && secret:
		return "", nil, "A sensitive field takes no typed value: wire it, or write CUE that reads a secret."
	case v.Text != nil:
		if len(*v.Text) > maxBoardExpr {
			return "", nil, "The text is too long."
		}
		return literal.String.Quote(*v.Text), heads, ""
	case v.Expr != "":
		if len(v.Expr) > maxBoardExpr {
			return "", nil, "The expression is too long."
		}
		x, err := parser.ParseExpr("value", v.Expr)
		if err != nil {
			return "", nil, "This is not CUE: " + clip(strings.TrimPrefix(err.Error(), "value:"), 200)
		}
		out, err := format.Node(x)
		if err != nil {
			return "", nil, "This is not CUE."
		}
		for _, id := range refHeads(x) {
			heads[id.Name] = true
		}
		if secret && len(heads) == 0 {
			return "", nil, "A sensitive field takes no typed value: wire it, or write CUE that reads a secret."
		}
		return string(out), heads, ""
	case v.Wire != nil:
		ref, h, msg := boardRef(*v.Wire, self, labels, m)
		if msg != "" {
			return "", nil, msg
		}
		heads[h] = true
		if f.Wire != nil && f.Wire.Many {
			return "[" + ref + "]", heads, ""
		}
		return ref, heads, ""
	}
	if len(v.Wires) > 32 {
		return "", nil, "A list holds at most 32 wires."
	}
	var refs []string
	for _, wr := range v.Wires {
		ref, h, msg := boardRef(wr, self, labels, m)
		if msg != "" {
			return "", nil, msg
		}
		heads[h] = true
		refs = append(refs, ref)
	}
	return "[" + strings.Join(refs, ", ") + "]", heads, ""
}

// boardRef is the reference of a wire: label.read for a thing drawn, the address and the attribute for a node of the
// specification. h is the identifier it starts with.
func boardRef(r BoardRef, self string, labels map[string]bool, m *model) (ref, h, msg string) {
	if !boardReadOK.MatchString(r.Read) {
		return "", "", "A wire reads an attribute: small letters, digits and underscores."
	}
	if r.To == self {
		return "", "", "A thing cannot read itself."
	}
	if labels[r.To] {
		return r.To + "." + r.Read, r.To, ""
	}
	if len(r.To) > 512 {
		return "", "", "There is no node " + strconv.Quote(clip(r.To, 60)) + " to read."
	}
	if _, ok := m.spec.byPath[r.To]; !ok {
		return "", "", "There is no node " + strconv.Quote(clip(r.To, 60)) + " in the specification to read."
	}
	p := cue.ParsePath(r.To)
	sels := p.Selectors()
	if p.Err() != nil || len(sels) == 0 {
		return "", "", "There is no node " + strconv.Quote(clip(r.To, 60)) + " to read."
	}
	head := sels[0]
	if head.LabelType() != cue.StringLabel || !cueIdent.MatchString(head.Unquoted()) && !strings.HasPrefix(head.Unquoted(), "$") {
		return "", "", r.To + " cannot be read from the top level of a file: its first name is not a name CUE can refer to."
	}
	var b strings.Builder
	b.WriteString(head.Unquoted())
	for _, sel := range sels[1:] {
		switch {
		case sel.LabelType() == cue.StringLabel && cueIdent.MatchString(sel.Unquoted()):
			b.WriteString("." + sel.Unquoted())
		case sel.LabelType() == cue.StringLabel:
			b.WriteString("[" + strconv.Quote(sel.Unquoted()) + "]")
		case sel.LabelType() == cue.IndexLabel:
			b.WriteString("[" + sel.String() + "]")
		default:
			return "", "", r.To + " cannot be read from the top level of a file."
		}
	}
	b.WriteString("." + r.Read)
	return b.String(), head.Unquoted(), ""
}

// refHeads are the identifiers that the references of an expression start with: not the labels of its fields, and not
// what a selector selects.
func refHeads(x ast.Expr) []*ast.Ident {
	var out []*ast.Ident
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		switch t := n.(type) {
		case *ast.Field:
			ast.Walk(t.Value, visit, nil)
			return false
		case *ast.SelectorExpr:
			ast.Walk(t.X, visit, nil)
			return false
		case *ast.Ident:
			out = append(out, t)
		}
		return true
	}
	ast.Walk(x, visit, nil)
	return out
}

// replaceHead makes a reference that starts with the identifier h start with to instead.
func replaceHead(expr, h, to string) string {
	x, err := parser.ParseExpr("value", expr)
	if err != nil {
		return expr
	}
	for _, id := range refHeads(x) {
		if id.Name == h {
			id.Name = to
		}
	}
	out, err := format.Node(x)
	if err != nil {
		return expr
	}
	return string(out)
}

// boardCheckHandler answers POST /api/board/check.
func (s *Server) boardCheckHandler(w http.ResponseWriter, r *http.Request, m *model) {
	d, ok := readBoardDraft(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.boardCheck(m, d))
}

// boardCheck judges a drawing: the code, what CUE and the schemas say of it, and what the policies say.
func (s *Server) boardCheck(m *model, d *BoardDraft) *BoardCheck {
	began := time.Now()
	out := &BoardCheck{Version: APIVersion, File: d.File, State: "valid", Lines: map[string][2]int{}, Problems: []BoardProblem{}, Creates: []TemplateCreate{}}
	defer func() { out.Took = time.Since(began).Milliseconds() }()
	if len(d.Items) == 0 {
		out.State = "empty"
		if out.File == "" {
			out.File = s.boardFile()
		}
		return out
	}
	code, problems := s.boardGenerate(m, d)
	if code == nil {
		out.State, out.Problems = "invalid", problems
		return out
	}
	out.File, out.Code, out.Lines = code.file, code.text, code.lines
	if _, err := os.Lstat(filepath.Join(s.opts.Dir, code.file)); err == nil {
		out.State = "exists"
		out.Problems = append(out.Problems, BoardProblem{Field: "@file", Message: code.file + " is there already: the board writes a new file, and never one that exists."})
		return out
	}
	labels := map[string]bool{}
	for _, it := range d.Items {
		labels[it.Label] = true
	}
	lo := s.loadOptions()
	s.tplMu.Lock()
	root, err := config.Load(s.opts.Dir, config.LoadOptions{Tags: lo.Tags, SysTags: lo.SysTags, Env: lo.Env, Extra: map[string]string{code.file: code.text}})
	s.tplMu.Unlock()
	if err == nil {
		err = s.previewCheck(m, root)
	}
	if err != nil {
		out.State = "invalid"
		out.Problems = boardProblems(m.scrubText(errorText(err)), labels)
		// a field that needs a value is said once, by the board
		if len(code.missing) > 0 {
			only := true
			for _, p := range out.Problems {
				if !strings.Contains(p.Message, "is required and has no value") && !strings.Contains(p.Message, "incomplete value") {
					only = false
				}
			}
			if only {
				out.State, out.Problems = "incomplete", code.missing
			} else {
				out.Problems = append(code.missing, out.Problems...)
			}
		}
		return out
	}
	if len(code.missing) > 0 {
		out.State, out.Problems = "incomplete", code.missing
	}
	var changes []engine.ChangeView
	marked := engine.TemplateCache{}
	for _, res := range root.Resources {
		if _, had := m.spec.byPath[res.Address()]; had {
			continue
		}
		out.Creates = append(out.Creates, TemplateCreate{Address: res.Address(), Type: res.Type})
		if res.Mode == "managed" {
			changes = append(changes, engine.ChangeView{Addr: res.Address(), Mode: "managed", Type: res.Type, Name: res.Name, Action: engine.ActionCreate,
				Provider: res.ProviderKey, After: configured(root.Value.LookupPath(res.Path)), Template: engine.TemplateOf(root.Value, res.Path, marked)})
		}
	}
	out.Reads = boardReads(root, labels)
	out.Policy = s.previewPolicies(m, changes)
	return out
}

// boardReads finds what each thing drawn reads, by the reference analysis of the engine, so that the graph draws the edges
// of what was typed as it draws those of a wire. A template instance reads what its resources read, outside itself.
func boardReads(root *config.Root, labels map[string]bool) map[string][]string {
	res := newSpecResolver(root)
	sets := map[string]map[string]bool{}
	for _, r := range root.Resources {
		addr := r.Address()
		item, _, _ := strings.Cut(addr, ".")
		if !labels[item] || (r.Mode != "managed" && r.Mode != "data") {
			continue
		}
		deps, _ := convert.Dependencies(root.At(r.Path), r.Path, res)
		for _, p := range deps {
			d, ok := res.byPath[p.String()]
			if !ok {
				continue
			}
			da := d.Address()
			if dItem, _, _ := strings.Cut(da, "."); dItem == item {
				continue
			} else if labels[dItem] {
				da = dItem
			}
			if sets[item] == nil {
				sets[item] = map[string]bool{}
			}
			sets[item][da] = true
		}
	}
	out := map[string][]string{}
	for item, set := range sets {
		list := make([]string, 0, len(set))
		for a := range set {
			list = append(list, a)
		}
		sort.Strings(list)
		out[item] = list
	}
	return out
}

// boardProblems splits the message of a load error into its problems, each tied to the thing drawn and the field it is
// about when its path leads into one.
func boardProblems(text string, labels map[string]bool) []BoardProblem {
	var out []BoardProblem
	seen := map[string]bool{}
	for _, p := range templateProblems(text, "") {
		bp := BoardProblem{Message: p.Message}
		if mm := problemHead.FindStringSubmatch(p.Message + " "); mm != nil {
			path := strings.TrimSuffix(mm[1], ":")
			head, rest, _ := strings.Cut(path, ".")
			if labels[head] {
				bp.Item = head
				bp.Field, _, _ = strings.Cut(rest, ".")
				bp.Message = strings.TrimSpace(strings.TrimPrefix(p.Message, path+":"))
			}
		}
		key := bp.Item + "\x00" + bp.Field + "\x00" + bp.Message
		if !seen[key] {
			seen[key] = true
			out = append(out, bp)
		}
	}
	return out
}

// boardSaveHandler answers POST /api/board/save: it writes the drawing as a new file, once.
func (s *Server) boardSaveHandler(w http.ResponseWriter, r *http.Request, m *model) {
	if s.opts.Board == nil {
		writeError(w, http.StatusForbidden, "the board writes nothing unless pravo serve was started with --edit")
		return
	}
	d, ok := readBoardDraft(w, r)
	if !ok {
		return
	}
	chk := s.boardCheck(m, d)
	if chk.State != "valid" {
		writeJSON(w, http.StatusConflict, chk)
		return
	}
	s.boardMu.Lock()
	defer s.boardMu.Unlock()
	path := filepath.Join(s.opts.Dir, chk.File)
	// O_EXCL: a file that appeared since the check is not written over.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		writeError(w, http.StatusConflict, chk.File+" could not be written: it is there already, or the directory cannot be written")
		return
	}
	_, werr := f.WriteString(chk.Code)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		writeError(w, http.StatusInternalServerError, chk.File+" could not be written")
		return
	}
	cmds := []string{"pravo plan", "pravo apply"}
	if tags := boardTagArgs(s.loadOptions().Tags); tags != "" {
		cmds = []string{"pravo plan" + tags, "pravo apply" + tags}
	}
	writeJSON(w, http.StatusOK, &BoardSaved{Version: APIVersion, File: chk.File, Bytes: len(chk.Code), Commands: cmds})
}

// boardTagArgs are the -t arguments of the tags the page reads with, quoted for a shell; a tag named like a secret is
// masked, as in a recorded command line.
func boardTagArgs(tags []string) string {
	var b strings.Builder
	for _, t := range tags {
		k, v, _ := strings.Cut(t, "=")
		if schema.NameGuard.MatchString(k) {
			v = "<" + k + ">"
		}
		b.WriteString(" -t " + engine.ShellWord(k+"="+v))
	}
	return b.String()
}

// boardPlanHandler answers POST /api/board/plan: it runs a plan of the specification with the drawing laid over it, and
// streams its steps as lines of JSON, then the result.
func (s *Server) boardPlanHandler(w http.ResponseWriter, r *http.Request, m *model) {
	if s.opts.Board == nil || s.opts.Board.Plan == nil {
		writeError(w, http.StatusForbidden, "the board plans nothing unless pravo serve was started with --edit")
		return
	}
	d, ok := readBoardDraft(w, r)
	if !ok {
		return
	}
	req := BoardPlanRequest{}
	lo := s.loadOptions()
	req.Tags, req.SysTags = lo.Tags, lo.SysTags
	if len(d.Items) > 0 {
		chk := s.boardCheck(m, d)
		if chk.State != "valid" {
			writeJSON(w, http.StatusConflict, chk)
			return
		}
		req.Extra = map[string]string{chk.File: chk.Code}
	}
	if !s.boardPlanning.CompareAndSwap(false, true) {
		writeError(w, http.StatusConflict, "a plan of the board is running already")
		return
	}
	defer s.boardPlanning.Store(false)
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	var mu sync.Mutex
	send := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		_, _ = w.Write(append(b, '\n'))
		if flusher != nil {
			flusher.Flush()
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	began := time.Now()
	res, err := s.opts.Board.Plan(ctx, req, func(st BoardStep) {
		if len(st.Addr) > 512 {
			st.Addr = st.Addr[:512]
		}
		send(map[string]any{"step": st})
	})
	if err != nil {
		send(map[string]any{"error": s.boardText(m, err.Error()), "took_ms": time.Since(began).Milliseconds()})
		return
	}
	res.Refused = s.boardText(m, res.Refused)
	send(map[string]any{"done": res, "took_ms": time.Since(began).Milliseconds()})
}

// boardText is a sentence of a plan of the board as the page may show it: scrubbed of secrets, with the paths of the
// project's files relative to it, and with no control character but the line breaks between its errors.
func (s *Server) boardText(m *model, text string) string {
	text = m.scrubText(text)
	if dir, err := filepath.Abs(s.opts.Dir); err == nil {
		text = strings.ReplaceAll(text, dir+string(filepath.Separator), "")
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = stripControl(l)
	}
	return clip(strings.Join(lines, "\n"), 4096)
}
