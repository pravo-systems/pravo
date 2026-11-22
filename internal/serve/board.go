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
	// Apply applies the plan of the board that a ticket names, as "pravo apply <planfile>" applies a plan file: every
	// change is checked against the planned one. Nil is off (--edit without --apply): the board applies nothing.
	Apply func(ctx context.Context, ticket string, step func(BoardStep)) (*BoardApplyResult, error)
	// Start brings a Terraform state onto the board as "pravo shadow" does: a new file of the specification and the
	// records of the objects, which Terraform keeps until "pravo shadow take-over". It applies nothing. The server
	// calls it only while the state of the project holds nothing.
	Start func(ctx context.Context, req BoardStartRequest) (*BoardStartResult, error)
}

// BoardPlanRequest is what a plan of the board is made of.
type BoardPlanRequest struct {
	Tags    []string
	SysTags bool
	// Extra holds the drawing, by the name of its file, when it is not saved yet.
	Extra map[string]string
	// Keep asks for the plan to be kept to apply, with a ticket: the board can apply, and nothing on it is unsaved, so
	// the plan is of the files as they are.
	Keep bool
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
	// Move counts the records the plan moves to a new address: a fold moves, and changes nothing that was made.
	Move int `json:"move,omitempty"`
	// Refused is what a policy in force said against the plan, empty when they all hold.
	Refused string `json:"refused,omitempty"`
	// Ticket names the plan, kept to be applied from the board, when it was asked to be kept and can be applied.
	Ticket string `json:"ticket,omitempty"`
	// Held says why a plan that was asked to be kept cannot be applied from the board.
	Held string `json:"held,omitempty"`
}

// BoardApplyResult is what an apply from the board did.
type BoardApplyResult struct {
	Added     int    `json:"added"`
	Changed   int    `json:"changed"`
	Destroyed int    `json:"destroyed"`
	Moved     int    `json:"moved,omitempty"`
	Txn       string `json:"txn"`
	// Deferred are the changes that waited for a value the apply made: the next plan makes them.
	Deferred int `json:"deferred,omitempty"`
}

// BoardChange is the action of a plan for one node.
type BoardChange struct {
	Addr   string `json:"addr"`
	Action string `json:"action"`
	// From is the address a record moves from, on a move.
	From string `json:"from,omitempty"`
}

// BoardConfig is the answer of GET /api/board.
type BoardConfig struct {
	Version int `json:"version"`
	// Edit is set when the server was started with --edit: the board can save and plan.
	Edit bool `json:"edit"`
	// Apply is set when the server was started with --apply: the board can apply a plan that it made of the files as
	// they are, which it showed.
	Apply bool `json:"apply,omitempty"`
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
	// Guide names the model of the guide, when pravo serve was started with --chat: it draws from a wish, reviews the
	// drawing and explains a thing on it, and writes nothing.
	Guide string `json:"guide,omitempty"`
	// Start is set when the board can start from a Terraform state: --edit, and a state that holds nothing yet.
	Start bool `json:"start,omitempty"`
}

// BoardProvider is a provider the board draws with.
type BoardProvider struct {
	// Label is the struct of the provider in the specification, such as $aws.
	Label   string `json:"label"`
	Local   string `json:"local"`
	Source  string `json:"source"`
	Version string `json:"version"`
	Types   int    `json:"types"`
	// Hint tells providers of one source apart: what the specification sets of region, location, zone, project or
	// profile, as text that is no secret, such as "region us-east-1".
	Hint string `json:"hint,omitempty"`
}

// BoardDraft is the drawing that the page sends: the body of POST /api/board/check, /save and /plan.
type BoardDraft struct {
	// File is the name of the new file; empty for the one the board suggests.
	File  string      `json:"file,omitempty"`
	Items []BoardItem `json:"items"`
	// Edits change resources that the specification has, in the files where they are written.
	Edits []BoardEdit `json:"edits,omitempty"`
	// Fold folds resources of the specification into a template.
	Fold *BoardFold `json:"fold,omitempty"`
	// Seen are the digests of the files whose change the page showed, by name: a save writes a file only as it was shown.
	Seen map[string]string `json:"seen,omitempty"`
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
	// Adopt makes the resource adopt an object that a discovery found, by its identity in $import.
	Adopt *BoardAdopt `json:"adopt,omitempty"`
}

// BoardAdopt names an object of the last run of a discovery, as /api/discovery names it.
type BoardAdopt struct {
	Discovery string `json:"discovery"`
	Key       string `json:"key"`
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
	// Files are the files of the project that the edits change, as they would be.
	Files []BoardFileChange `json:"files,omitempty"`
	// Removes are the resources that the edits take out of the specification.
	Removes []string `json:"removes,omitempty"`
	// Fold is what a fold of the drawing makes: a template and an instance that takes the records of what it folds.
	Fold *BoardFolded `json:"fold,omitempty"`
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
	// Changed are the files of the project that the edits wrote.
	Changed []string `json:"changed,omitempty"`
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
			m.spec.cueMu.Lock()
			p.Hint = boardProvHint(root.Value.LookupPath(d.Path), sch, m.scrubText)
			m.spec.cueMu.Unlock()
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

// boardHintKeys are the settings of a provider that tell two of one source apart, in the order the hint names them.
var boardHintKeys = []string{"region", "location", "zone", "project", "profile"}

// boardProvHint is the hint of the provider struct v: the first two of boardHintKeys that it sets to a concrete string,
// leaving out what the schema of the provider calls sensitive and any value that the viewer would withhold. The caller
// holds cueMu.
func boardProvHint(v cue.Value, sch Schemas, scrub func(string) string) string {
	if !v.Exists() {
		return ""
	}
	secret := map[string]bool{}
	if ps := sch.Provider(); ps != nil && ps.Block != nil {
		for _, a := range ps.Block.Attributes {
			secret[a.Name] = a.Sensitive || a.WriteOnly
		}
	}
	var parts []string
	for _, k := range boardHintKeys {
		if secret[k] {
			continue
		}
		f := v.LookupPath(cue.MakePath(cue.Str(k)))
		if !f.Exists() || !f.IsConcrete() || f.Kind() != cue.StringKind {
			continue
		}
		str, err := f.String()
		if err != nil || str == "" || len(str) > 40 || scrub(str) != str {
			continue
		}
		if parts = append(parts, k+" "+str); len(parts) == 2 {
			break
		}
	}
	return strings.Join(parts, ", ")
}

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
	out := &BoardConfig{Version: APIVersion, Edit: s.opts.Board != nil, Apply: s.opts.Board != nil && s.opts.Board.Apply != nil, Providers: []BoardProvider{}, File: s.boardFile()}
	out.Start = s.opts.Board != nil && s.opts.Board.Start != nil && m.recorded() == 0
	if c := s.opts.Chat; c != nil {
		out.Guide = strings.TrimSpace(c.Name + " " + c.Model)
	}
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

// drawProblems collects what is wrong with a drawing.
type drawProblems []BoardProblem

func (p *drawProblems) add(item, field, msg string) {
	*p = append(*p, BoardProblem{Item: item, Field: field, Message: msg})
}

// boardField is one field of a thing drawn, as CUE, and boardBlock the struct of one thing drawn.
type boardField struct{ name, expr string }

type boardBlock struct {
	label, head string
	fields      []boardField
	heads       map[string]bool // the identifiers its references start with
}

// boardGenerate writes the CUE of a drawing. A problem that it returns means that no code could be written for the
// drawing as it is: a name that cannot be used, a type that no provider has, an expression that does not parse.
func (s *Server) boardGenerate(m *model, d *BoardDraft) (*boardCode, []BoardProblem) {
	bm := s.boardData(m)
	code := &boardCode{file: d.File, lines: map[string][2]int{}}
	if code.file == "" {
		code.file = s.boardFile()
	}
	var problems drawProblems
	if !boardFileOK.MatchString(code.file) || code.file == previewFile {
		problems.add("", "@file", "The file is named with small letters, digits, dashes and underscores, and ends in .cue.")
		return nil, problems
	}
	if bm.pkg == "" {
		problems.add("", "", "The specification does not load, so nothing can be drawn into it.")
		return nil, problems
	}
	labels := boardLabels(bm, d, &problems)
	if len(problems) > 0 {
		return nil, problems
	}
	var blocks []boardBlock
	lets := map[string]bool{}
	for _, it := range d.Items {
		if bl, ok := s.boardBlockOf(m, it, labels, code, &problems); ok {
			// A reference that starts with the name of a field of its own struct would read that field: it reads an
			// alias of the file instead.
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
	}
	if len(problems) > 0 {
		return nil, problems
	}
	if !boardWrite(bm, code, blocks, lets, labels, &problems) {
		return nil, problems
	}
	return code, nil
}

// boardLabels checks the name of each thing drawn, and returns the names.
func boardLabels(bm *boardModel, d *BoardDraft, problems *drawProblems) map[string]bool {
	labels := map[string]bool{}
	for _, it := range d.Items {
		switch {
		case !boardLabelOK.MatchString(it.Label) || cueKeywords[it.Label]:
			problems.add(it.Label, "@label", "A name is small letters, digits and underscores, starts with a letter, and is no word of CUE.")
		case labels[it.Label]:
			problems.add(it.Label, "@label", "Two things are named "+it.Label+".")
		case bm.top[it.Label]:
			problems.add(it.Label, "@label", it.Label+" is taken: the specification has a field of that name at the top level.")
		}
		labels[it.Label] = true
	}
	return labels
}

// boardBlockOf is the struct of one thing drawn: what it unifies with, and its fields in the order of its form. A field
// that needs a value and has none goes to code.missing.
func (s *Server) boardBlockOf(m *model, it BoardItem, labels map[string]bool, code *boardCode, problems *drawProblems) (boardBlock, bool) {
	bl := boardBlock{label: it.Label, heads: map[string]bool{}}
	var form []BoardField
	switch {
	case it.Template != "":
		t := s.boardTemplate(m, it.Template)
		if t == nil {
			problems.add(it.Label, "", "No template of the specification is named "+strconv.Quote(clip(it.Template, 60))+".")
			return bl, false
		}
		if it.Adopt != nil {
			problems.add(it.Label, "@adopt", "A template does not adopt an object: draw a resource of its type.")
			return bl, false
		}
		bl.head = t.Definition
		form = boardTemplateForm(t).Fields
	default:
		p := s.boardProvider(m, it.Provider, it.Type)
		if p == nil {
			problems.add(it.Label, "", "No provider of the specification has the type "+strconv.Quote(clip(it.Type, 80))+".")
			return bl, false
		}
		sch := p.sch.Type("managed", it.Type)
		if sch == nil || sch.Block == nil {
			problems.add(it.Label, "", "The schema of "+it.Type+" is not cached.")
			return bl, false
		}
		bl.head = p.Label + ".#Resource." + it.Type
		if it.Adopt != nil {
			imp, msg := boardImport(m, it, p.Label)
			if msg != "" {
				problems.add(it.Label, "@adopt", msg)
				return bl, false
			}
			bl.fields = append(bl.fields, boardField{"$import", imp})
		}
		form = s.boardForm(m, p, it.Type, sch.Block).Fields
	}
	known := map[string]bool{}
	for _, f := range form {
		known[f.Name] = true
	}
	ok := true
	for name := range it.Values {
		if !known[name] {
			problems.add(it.Label, name, "There is no field "+strconv.Quote(clip(name, 60))+" to set.")
			ok = false
		}
	}
	for _, f := range form {
		v, set := it.Values[f.Name]
		if !set {
			if f.Required {
				code.missing = append(code.missing, BoardProblem{Item: it.Label, Field: f.Name, Message: "needs a value"})
			}
			continue
		}
		expr, heads, msg := boardExpr(v, f, it.Label, labels, m)
		if msg != "" {
			problems.add(it.Label, f.Name, msg)
			ok = false
			continue
		}
		for h := range heads {
			bl.heads[h] = true
		}
		bl.fields = append(bl.fields, boardField{f.Name, expr})
	}
	return bl, ok
}

// boardWrite writes the file of the drawing into code: the aliases it needs, then a struct for each thing drawn,
// formatted, with the lines of each.
func boardWrite(bm *boardModel, code *boardCode, blocks []boardBlock, lets, labels map[string]bool, problems *drawProblems) bool {
	var b strings.Builder
	b.WriteString("package " + bm.pkg + "\n\n// Drawn on the drawing board of pravo serve.\n\n")
	var names []string
	for h := range lets {
		names = append(names, h)
	}
	sort.Strings(names)
	for _, h := range names {
		if bm.top["ref_"+h] || labels["ref_"+h] {
			problems.add("", "", "ref_"+h+" is taken, and the drawing needs it to read "+h+".")
			return false
		}
		b.WriteString("let ref_" + h + " = " + h + "\n")
	}
	if len(names) > 0 {
		b.WriteString("\n")
	}
	for _, bl := range blocks {
		b.WriteString(bl.label + ": " + bl.head + " & {")
		for _, f := range bl.fields {
			label := cueLabel(f.name)
			if f.name == "$import" {
				label = f.name // as Pravo's own fields are written
			}
			b.WriteString("\n\t" + label + ": " + f.expr)
		}
		if len(bl.fields) > 0 {
			b.WriteString("\n")
		}
		b.WriteString("}\n\n")
	}
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		problems.add("", "", "The drawing does not make CUE: "+clip(err.Error(), 300))
		return false
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
	return true
}

// boardRoute answers the paths of the drawing board: three that read, and three that take a drawing by POST.
func (s *Server) boardRoute(w http.ResponseWriter, r *http.Request, m *model) {
	switch r.URL.Path {
	case "/api/board":
		s.boardConfigHandler(w, m)
		return
	case "/api/board/types":
		s.boardTypesHandler(w, r, m)
		return
	case "/api/board/type":
		s.boardTypeHandler(w, r, m)
		return
	case "/api/board/edit":
		s.boardEditHandler(w, r, m)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	switch r.URL.Path {
	case "/api/board/check":
		s.boardCheckHandler(w, r, m)
	case "/api/board/save":
		s.boardSaveHandler(w, r, m)
	case "/api/board/guide":
		s.boardGuideHandler(w, r, m)
	case "/api/board/apply":
		s.boardApplyHandler(w, r, m)
	case "/api/board/start":
		s.boardStartHandler(w, r, m)
	default:
		s.boardPlanHandler(w, r, m)
	}
}

// boardImport is the $import of a thing drawn that adopts an object a discovery found: the identity the provider listed
// it by, as the Discovery section writes it, and never a text that the page sent.
func boardImport(m *model, it BoardItem, provider string) (string, string) {
	root := m.spec.root
	switch {
	case root == nil:
		return "", "The specification does not load."
	case root.Environment != "":
		return "", "In a specification of environments the object is adopted in its environment alone: copy the resource from the Discovery section."
	}
	v, item := m.discovered(it.Adopt.Discovery, it.Adopt.Key)
	switch {
	case item == nil:
		return "", "The last run of the discovery has no such object, or a resource manages it."
	case item.Status == engine.Adopting:
		return "", item.By + " adopts this object already."
	case v.Type != it.Type:
		return "", "The object is a " + v.Type + ", not a " + it.Type + "."
	}
	if p, ok := root.ProviderPath(v.ProviderKey); !ok || p.String() != provider {
		return "", "The object belongs to another provider."
	}
	if len(item.Identity) == 0 {
		return "", "The provider listed this object with no identity to import it by."
	}
	imp := engine.ImportValue(item.Identity)
	if m.scrubTags(imp) != imp {
		return "", "The identity of the object holds the value of a tag that the viewer hides: adopt it with pravo discover adopt."
	}
	return imp, ""
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
	writeJSON(w, http.StatusOK, s.boardShown(m, s.boardCheck(m, d)))
}

// boardCheck judges a drawing: the code, what CUE and the schemas say of it, and what the policies say.
func (s *Server) boardCheck(m *model, d *BoardDraft) *BoardCheck {
	began := time.Now()
	out := &BoardCheck{Version: APIVersion, File: d.File, State: "valid", Lines: map[string][2]int{}, Problems: []BoardProblem{}, Creates: []TemplateCreate{}}
	defer func() { out.Took = time.Since(began).Milliseconds() }()
	if out.File == "" {
		out.File = s.boardFile()
	}
	if len(d.Items) == 0 && len(d.Edits) == 0 && d.Fold == nil {
		out.State = "empty"
		return out
	}
	// named are the things the problems can be about: the labels of the things drawn, the addresses of the changed
	named := map[string]bool{}
	for _, it := range d.Items {
		named[it.Label] = true
	}
	extra := map[string]string{}
	code := &boardCode{lines: map[string][2]int{}}
	if len(d.Items) > 0 {
		c, problems := s.boardGenerate(m, d)
		if c == nil {
			out.State, out.Problems = "invalid", problems
			return out
		}
		code = c
		out.File, out.Code, out.Lines = code.file, code.text, code.lines
		if _, err := os.Lstat(filepath.Join(s.opts.Dir, code.file)); err == nil {
			out.State = "exists"
			out.Problems = append(out.Problems, BoardProblem{Field: "@file", Message: code.file + " is there already: the board writes a new file, and never one that exists."})
			return out
		}
		extra[code.file] = code.text
	}
	var problems drawProblems
	out.Files, out.Fold = s.boardEdits(m, d, named, &problems)
	if d.Fold != nil {
		named["@fold"] = true
		for _, a := range d.Fold.Addrs {
			named[a] = true
		}
	}
	for _, e := range d.Edits {
		named[e.Addr] = true
		if e.Remove {
			out.Removes = append(out.Removes, e.Addr)
		}
	}
	boardRemovedReads(m, out.Removes, &problems)
	if len(problems) > 0 {
		out.State, out.Problems = "invalid", problems
		return out
	}
	for _, f := range out.Files {
		extra[f.File] = f.Text
	}
	lo := s.loadOptions()
	s.tplMu.Lock()
	root, err := config.Load(s.opts.Dir, config.LoadOptions{Tags: lo.Tags, SysTags: lo.SysTags, Env: lo.Env, Extra: extra})
	s.tplMu.Unlock()
	if err == nil {
		err = s.previewCheck(m, root)
	}
	if err != nil {
		boardLoadProblems(out, m.scrubText(errorText(err)), named, code.missing)
		return out
	}
	if len(code.missing) > 0 {
		out.State, out.Problems = "incomplete", code.missing
	}
	out.Reads = boardReads(root, named)
	out.Policy = s.previewPolicies(m, boardChanges(m, root, d, out))
	return out
}

// boardRemovedReads says which resources and outputs read a resource that the drawing removes, and are not removed too:
// the specification would not load without it.
func boardRemovedReads(m *model, removes []string, problems *drawProblems) {
	gone := map[string]bool{}
	for _, a := range removes {
		gone[a] = true
	}
	for _, a := range removes {
		var readers []string
		for _, r := range m.used[a] {
			if !gone[r] {
				readers = append(readers, r)
			}
		}
		if len(readers) == 0 {
			continue
		}
		sort.Strings(readers)
		verb, them := " reads ", "it"
		if len(readers) > 1 {
			verb, them = " read ", "them"
		}
		if len(readers) > 4 {
			readers = append(readers[:4], "and "+strconv.Itoa(len(readers)-4)+" more")
		}
		problems.add(a, "", strings.Join(readers, ", ")+verb+a+": remove "+them+" too, or change "+them+" to read something else.")
	}
}

// boardLoadProblems says what is wrong when the specification does not load with the drawing: a field that needs a value
// is said once, by the board.
func boardLoadProblems(out *BoardCheck, text string, named map[string]bool, missing []BoardProblem) {
	out.State = "invalid"
	out.Problems = boardProblems(text, named)
	if len(missing) == 0 {
		return
	}
	for _, p := range out.Problems {
		if !strings.Contains(p.Message, "is required and has no value") && !strings.Contains(p.Message, "incomplete value") {
			out.Problems = append(missing, out.Problems...)
			return
		}
	}
	out.State, out.Problems = "incomplete", missing
}

// boardChanges are the changes of an approximate plan of the drawing, which the policies judge: what it adds is created,
// what it changes is updated, and what it removes is deleted. It fills in what the check says it creates.
func boardChanges(m *model, root *config.Root, d *BoardDraft, out *BoardCheck) []engine.ChangeView {
	var changes []engine.ChangeView
	marked := engine.TemplateCache{}
	edited := map[string]bool{}
	for _, e := range d.Edits {
		edited[e.Addr] = true
	}
	moved := map[string]bool{}
	if out.Fold != nil {
		for _, to := range out.Fold.Moves {
			moved[to] = true
		}
	}
	for _, res := range root.Resources {
		addr := res.Address()
		_, had := m.spec.byPath[addr]
		switch {
		case moved[addr]:
			// a fold moves the record: the object stays as it is
		case !had:
			out.Creates = append(out.Creates, TemplateCreate{Address: addr, Type: res.Type})
			if res.Mode == "managed" {
				changes = append(changes, engine.ChangeView{Addr: addr, Mode: "managed", Type: res.Type, Name: res.Name, Action: engine.ActionCreate,
					Provider: res.ProviderKey, After: configured(root.Value.LookupPath(res.Path)), Template: engine.TemplateOf(root.Value, res.Path, marked)})
			}
		case edited[addr]:
			changes = append(changes, engine.ChangeView{Addr: addr, Mode: "managed", Type: res.Type, Name: res.Name, Action: engine.ActionUpdate,
				Provider: res.ProviderKey, After: configured(root.Value.LookupPath(res.Path))})
		}
	}
	for _, a := range out.Removes {
		if res := m.spec.byPath[a]; res != nil {
			changes = append(changes, engine.ChangeView{Addr: a, Mode: "managed", Type: res.Type, Name: res.Name, Action: engine.ActionDelete, Provider: res.ProviderKey})
		}
	}
	return changes
}

// boardReads finds what each thing drawn reads, by the reference analysis of the engine, so that the graph draws the edges
// of what was typed as it draws those of a wire. A template instance reads what its resources read, outside itself.
func boardReads(root *config.Root, labels map[string]bool) map[string][]string {
	res := newSpecResolver(root)
	sets := map[string]map[string]bool{}
	for _, r := range root.Resources {
		addr := r.Address()
		item, _, _ := strings.Cut(addr, ".")
		if labels[addr] {
			item = addr
		}
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
			if dItem, _, _ := strings.Cut(da, "."); da == item || dItem == item {
				continue
			} else if labels[dItem] && !labels[da] {
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
			head := ""
			for l := range labels {
				if (path == l || strings.HasPrefix(path, l+".")) && len(l) > len(head) {
					head = l
				}
			}
			if head != "" {
				bp.Item = head
				bp.Field, _, _ = strings.Cut(strings.TrimPrefix(strings.TrimPrefix(path, head), "."), ".")
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
		writeJSON(w, http.StatusConflict, s.boardShown(m, chk))
		return
	}
	s.boardMu.Lock()
	defer s.boardMu.Unlock()
	// Every file a change is made in is read again: one that changed since the check, or since the page showed its
	// change, is not written over. A fold has no base of its own: what the page showed of it is its base.
	for _, fc := range chk.Files {
		if seen, ok := d.Seen[fc.File]; (ok && seen != fc.Base) || (!ok && d.Fold != nil) {
			writeError(w, http.StatusConflict, fc.File+" is not as the board showed it: look at the change again")
			return
		}
		if fc.New {
			continue
		}
		if cur, err := penRead(filepath.Join(s.opts.Dir, fc.File)); err != nil || penDigest(cur) != fc.Base {
			writeError(w, http.StatusConflict, fc.File+" changed since it was read: read it again")
			return
		}
	}
	out := &BoardSaved{Version: APIVersion, Changed: []string{}}
	// a new file of a fold first: the resources leave their files only once the template that holds them is written
	for _, fc := range chk.Files {
		if !fc.New {
			continue
		}
		if err := boardCreate(filepath.Join(s.opts.Dir, fc.File), fc.Text); err != nil {
			writeError(w, http.StatusConflict, fc.File+" could not be written: it is there already, or the directory cannot be written")
			return
		}
		out.Changed = append(out.Changed, fc.File)
		out.Bytes += len(fc.Text)
	}
	if chk.Code != "" {
		if err := boardCreate(filepath.Join(s.opts.Dir, chk.File), chk.Code); err != nil {
			writeError(w, http.StatusConflict, chk.File+" could not be written: it is there already, or the directory cannot be written")
			return
		}
		out.File, out.Bytes = chk.File, len(chk.Code)
	}
	for _, fc := range chk.Files {
		if fc.New {
			continue
		}
		if err := penReplace(filepath.Join(s.opts.Dir, fc.File), fc.Text); err != nil {
			writeError(w, http.StatusInternalServerError, fc.File+" could not be written; the files before it in the list were: "+strings.Join(out.Changed, ", "))
			return
		}
		out.Changed = append(out.Changed, fc.File)
		out.Bytes += len(fc.Text)
	}
	cmds := []string{"pravo plan", "pravo apply"}
	if tags := boardTagArgs(s.loadOptions().Tags); tags != "" {
		cmds = []string{"pravo plan" + tags, "pravo apply" + tags}
	}
	out.Commands = cmds
	writeJSON(w, http.StatusOK, out)
}

// boardCreate writes a new file, and never one that is there: O_EXCL, so a file that appeared since the check stays.
func boardCreate(path, text string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(text)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
	}
	return werr
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
	if len(d.Items) > 0 || len(d.Edits) > 0 || d.Fold != nil {
		chk := s.boardCheck(m, d)
		if chk.State != "valid" {
			writeJSON(w, http.StatusConflict, s.boardShown(m, chk))
			return
		}
		req.Extra = map[string]string{}
		if len(d.Items) > 0 {
			req.Extra[chk.File] = chk.Code
		}
		for _, f := range chk.Files {
			req.Extra[f.File] = f.Text
		}
	}
	req.Keep = s.opts.Board.Apply != nil && req.Extra == nil
	if !s.boardPlanning.CompareAndSwap(false, true) {
		writeError(w, http.StatusConflict, "a plan of the board is running already")
		return
	}
	defer s.boardPlanning.Store(false)
	send := ndjsonWriter(w)
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
	res.Refused, res.Held = s.boardText(m, res.Refused), s.boardText(m, res.Held)
	send(map[string]any{"done": res, "took_ms": time.Since(began).Milliseconds()})
}

// boardApplyHandler answers POST /api/board/apply: it applies the plan of the board that the ticket names, and streams its
// steps as lines of JSON, then the result. Only with --apply; one run of the board at a time.
func (s *Server) boardApplyHandler(w http.ResponseWriter, r *http.Request, m *model) {
	if s.opts.Board == nil || s.opts.Board.Apply == nil {
		writeError(w, http.StatusForbidden, "the board applies nothing unless pravo serve was started with --apply")
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "the ticket is sent as application/json")
		return
	}
	var req struct {
		Ticket string `json:"ticket"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 4<<10))
	if err := dec.Decode(&req); err != nil || req.Ticket == "" || len(req.Ticket) > 64 {
		writeError(w, http.StatusBadRequest, "send the ticket of the plan to apply")
		return
	}
	if !s.boardPlanning.CompareAndSwap(false, true) {
		writeError(w, http.StatusConflict, "a run of the board is running already")
		return
	}
	defer s.boardPlanning.Store(false)
	send := ndjsonWriter(w)
	// an apply that started is not stopped by a page that goes away: it records what it did
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Hour)
	defer cancel()
	began := time.Now()
	res, err := s.opts.Board.Apply(ctx, req.Ticket, func(st BoardStep) {
		if len(st.Addr) > 512 {
			st.Addr = st.Addr[:512]
		}
		send(map[string]any{"step": st})
	})
	if err != nil {
		send(map[string]any{"error": s.boardText(m, err.Error()), "took_ms": time.Since(began).Milliseconds()})
		return
	}
	send(map[string]any{"done": res, "took_ms": time.Since(began).Milliseconds()})
}

// ndjsonWriter starts an answer of lines of JSON, and sends one line for each value, as it is written.
func ndjsonWriter(w http.ResponseWriter) func(any) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	var mu sync.Mutex
	return func(v any) {
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
