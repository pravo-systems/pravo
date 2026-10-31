package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/engine"
)

// Templates. A template is a definition that embeds pravo.#Template. The catalog lists the templates of the
// specification, the inputs of each (the fields of the definition that hold no resource), and the instances made
// from it: the structs that carry its marker. The wizard builds the CUE of a new instance from values, loads the
// specification with that CUE laid over it, in memory, and says what CUE and the policies say. Nothing is written:
// the page shows the CUE and a person pastes it.

// Templates is the answer of GET /api/templates.
type Templates struct {
	Version  int    `json:"version"`
	Revision string `json:"revision"`
	// SpecOK is false when the specification does not load, so that no template is known.
	SpecOK    bool            `json:"spec_ok"`
	Templates []TemplateEntry `json:"templates"`
}

// TemplateEntry is one template.
type TemplateEntry struct {
	// Name is the name in the marker; Definition is the definition, such as #Service.
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Definition  string `json:"definition"`
	// Source is where the definition is written.
	Source *Ref `json:"source,omitempty"`
	// Inputs are what the wizard asks for, in order.
	Inputs []TemplateInput `json:"inputs"`
	// Problems are what is wrong with the way the template describes its inputs.
	Problems []string `json:"problems,omitempty"`
	// Resources is how many resources one instance holds.
	Resources int `json:"resources"`
	// Instances are the instances in the specification, by address.
	Instances []TemplateInstance `json:"instances"`
	// Targets are the structs the instances sit in, most used first; "" is the top level.
	Targets []TemplateTarget `json:"targets"`
	// File is the file that most instances are written in, where the wizard suggests the new one.
	File string `json:"file,omitempty"`
}

// TemplateInput is one field of the definition that a person fills in.
type TemplateInput struct {
	Name string `json:"name"`
	// Kind is string, int, number, bool, enum, object, list or any.
	Kind string `json:"kind"`
	// Type is the CUE of the field, such as `*8080 | int & >1024`.
	Type string `json:"type,omitempty"`
	// Label is what the form calls the input when the template says so in ask; Doc is the comment above the field, or the
	// description that ask gives instead.
	Label    string `json:"label,omitempty"`
	Doc      string `json:"doc,omitempty"`
	Required bool   `json:"required,omitempty"`
	// Default is the CUE of the default, empty when there is none.
	Default string `json:"default,omitempty"`
	// Options are the CUE of each value an enum allows.
	Options []string `json:"options,omitempty"`
	// Elem is the kind of the items of a list of strings or numbers, empty for another list.
	Elem string `json:"elem,omitempty"`
	// Long asks for a text area, and Secret says that the value is a secret that the form never takes: it asks where the
	// secret comes from. Choices is the path of the attribute that holds the values to choose from, Entity the type of
	// resource to pick one of, and the attribute to read of it. These come from $template.inputs.
	Long    bool            `json:"long,omitempty"`
	Secret  bool            `json:"secret,omitempty"`
	Choices string          `json:"choices,omitempty"`
	Entity  *TemplateEntity `json:"entity,omitempty"`
}

// TemplateEntity says which resources an input picks from, and what it reads of the one it picks.
type TemplateEntity struct {
	Type string `json:"type"`
	Attr string `json:"attr"`
}

// TemplateInstance is one struct made from a template.
type TemplateInstance struct {
	Address   string `json:"address"`
	Source    *Ref   `json:"source,omitempty"`
	Resources int    `json:"resources"`
	// Recorded counts the resources that the state holds.
	Recorded int `json:"recorded"`
	// Values are the inputs that the instance sets to a value, as CUE.
	Values map[string]string `json:"values"`
}

// TemplateTarget is a struct that holds instances.
type TemplateTarget struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

const (
	maxTemplates     = 200
	maxTemplateInput = 40
	maxInstances     = 500
	maxTemplateExpr  = 2048
	previewFile      = "zz_pravo_template_preview.cue"
)

// templates builds the catalog once for a model.
func (m *model) templates() *Templates { return m.catalog(true) }

// catalog builds the catalog the first time it is asked for. lock says whether the caller holds the lock of the
// specification already: the model is built under it.
func (m *model) catalog(lock bool) *Templates {
	m.tplOnce.Do(func() {
		t := &Templates{Version: APIVersion, Revision: m.rev, SpecOK: m.spec.root != nil, Templates: []TemplateEntry{}}
		if m.spec.root != nil {
			if lock {
				m.spec.cueMu.Lock()
				defer m.spec.cueMu.Unlock()
			}
			t.Templates = m.buildTemplates()
		}
		m.tpl = t
	})
	return m.tpl
}

// buildTemplates lists the definitions of the specification that carry the marker, and the instances made from them.
func (m *model) buildTemplates() []TemplateEntry {
	root := m.spec.root
	var out []TemplateEntry
	byName := map[string]int{}
	it, err := root.Value.Fields(cue.Definitions(true))
	if err != nil {
		return nil
	}
	for it.Next() {
		sel := it.Selector()
		if !sel.IsDefinition() || len(out) >= maxTemplates {
			continue
		}
		dv := it.Value()
		name, ok := stringAt(dv, "$template.name")
		if !ok || name == "" {
			continue
		}
		if _, dup := byName[name]; dup {
			continue
		}
		e := TemplateEntry{Name: name, Definition: sel.String(), Inputs: []TemplateInput{}, Instances: []TemplateInstance{}, Targets: []TemplateTarget{}}
		e.Title, _ = stringAt(dv, "$template.title")
		if e.Title == "" {
			e.Title = name
		}
		e.Description, _ = stringAt(dv, "$template.description")
		e.Source = m.spec.refOf(dv)
		e.Inputs, e.Problems = templateInputs(dv)
		byName[name] = len(out)
		out = append(out, e)
	}
	if len(out) == 0 {
		return []TemplateEntry{}
	}

	// Instances: the structs that carry a marker and hold resources.
	type inst struct {
		tmpl int
		ti   TemplateInstance
		val  cue.Value
	}
	found := map[string]*inst{}
	var order []string
	seenPrefix := map[string]bool{}
	for _, res := range root.Resources {
		sels := res.Path.Selectors()
		for i := 1; i < len(sels); i++ {
			p := cue.MakePath(sels[:i]...)
			key := p.String()
			if !seenPrefix[key] {
				seenPrefix[key] = true
				v := root.Value.LookupPath(p)
				if n, ok := stringAt(v, "$template.name"); ok {
					if ti, known := byName[n]; known && len(found) < maxInstances {
						found[key] = &inst{tmpl: ti, ti: TemplateInstance{Address: key, Values: map[string]string{}}, val: v}
						order = append(order, key)
					}
				}
			}
			if in := found[key]; in != nil {
				in.ti.Resources++
				if mn := m.nodes[res.Address()]; mn != nil && mn.rec != nil {
					in.ti.Recorded++
				}
			}
		}
	}
	files := map[int]map[string]int{}
	targets := map[int]map[string]int{}
	for _, key := range order {
		in := found[key]
		e := &out[in.tmpl]
		for _, ip := range e.Inputs {
			fv := in.val.LookupPath(cue.MakePath(cue.Str(ip.Name)))
			if !fv.Exists() {
				continue
			}
			if fv.IsConcrete() {
				if b, err := fv.MarshalJSON(); err == nil && len(b) <= 200 {
					in.ti.Values[ip.Name] = string(b)
				}
			} else if _, rp := fv.ReferencePath(); len(rp.Selectors()) > 0 {
				in.ti.Values[ip.Name] = rp.String()
			}
		}
		if declared, _ := m.spec.namespaceRefs(key); declared != nil {
			in.ti.Source = declared
			if files[in.tmpl] == nil {
				files[in.tmpl] = map[string]int{}
			}
			files[in.tmpl][declared.File]++
		}
		sels := cue.ParsePath(key).Selectors()
		parent := cue.MakePath(sels[:len(sels)-1]...).String()
		if len(sels) == 1 {
			parent = ""
		}
		if targets[in.tmpl] == nil {
			targets[in.tmpl] = map[string]int{}
		}
		targets[in.tmpl][parent]++
		e.Instances = append(e.Instances, in.ti)
		if in.ti.Resources > e.Resources {
			e.Resources = in.ti.Resources
		}
	}
	for i := range out {
		e := &out[i]
		for p, c := range targets[i] {
			e.Targets = append(e.Targets, TemplateTarget{Path: p, Count: c})
		}
		sort.Slice(e.Targets, func(a, b int) bool {
			if e.Targets[a].Count != e.Targets[b].Count {
				return e.Targets[a].Count > e.Targets[b].Count
			}
			return e.Targets[a].Path < e.Targets[b].Path
		})
		best := 0
		for f, c := range files[i] {
			if c > best || (c == best && f < e.File) {
				best, e.File = c, f
			}
		}
	}
	return out
}

// templateInputs reads the fields of a template definition that a person fills in: those that hold no resource and
// are not fixed to a value. The struct $template.inputs, when there is one, picks and orders them and says how the form
// asks for each; its keys that are not such a field are returned as problems.
func templateInputs(dv cue.Value) ([]TemplateInput, []string) {
	var order []string
	spec := dv.LookupPath(cue.ParsePath("$template.inputs"))
	if spec.Exists() {
		if it, err := spec.Fields(cue.Optional(true)); err == nil {
			for it.Next() {
				order = append(order, it.Selector().Unquoted())
			}
		}
	}
	byName := map[string]TemplateInput{}
	var natural []string
	fs, err := dv.Fields(cue.Optional(true))
	if err != nil {
		return []TemplateInput{}, nil
	}
	for fs.Next() {
		sel := fs.Selector()
		if sel.IsDefinition() || sel.LabelType() == cue.IndexLabel {
			continue
		}
		name := sel.Unquoted()
		if strings.HasPrefix(name, "$") || len(natural) >= maxTemplateInput {
			continue
		}
		fv := fs.Value()
		if holdsResource(fv, 0) {
			continue
		}
		in := TemplateInput{Name: name, Kind: tplKindOf(fv)}
		for _, g := range fv.Doc() {
			in.Doc = strings.TrimSpace(strings.TrimSpace(in.Doc + " " + strings.ReplaceAll(strings.TrimSpace(g.Text()), "\n", " ")))
		}
		in.Required = sel.ConstraintType() == cue.RequiredConstraint
		d, hasDefault := fv.Default()
		if hasDefault && !fv.IsConcrete() {
			if b, err := format.Node(d.Syntax()); err == nil {
				in.Default = oneLine(string(b))
			}
		} else if fv.IsConcrete() && sel.ConstraintType() == cue.SelectorType(0) {
			continue // a field that is fixed is a property of the template, not an input
		}
		if !hasDefault && sel.ConstraintType() == cue.SelectorType(0) {
			in.Required = true
		}
		if in.Kind != "object" && in.Kind != "list" {
			if b, err := format.Node(fv.Syntax(cue.Raw())); err == nil {
				in.Type = clip(oneLine(string(b)), 300)
			}
		}
		if op, args := fv.Expr(); op == cue.OrOp && len(args) > 1 {
			var opts []string
			for _, a := range args {
				if !a.IsConcrete() {
					opts = nil
					break
				}
				b, err := format.Node(a.Syntax())
				if err != nil {
					opts = nil
					break
				}
				opts = append(opts, oneLine(string(b)))
			}
			if len(opts) > 1 {
				in.Kind, in.Options = "enum", opts
			}
		}
		if in.Kind == "list" {
			in.Elem = listElem(fv)
		}
		in.applyAsk(spec.LookupPath(cue.MakePath(cue.Str(name))))
		byName[name] = in
		natural = append(natural, name)
	}
	seen := map[string]bool{}
	var out []TemplateInput
	var problems []string
	for _, n := range order {
		// A definition that nothing instantiates is not evaluated far enough to refuse an entry that names two sources.
		ev := spec.LookupPath(cue.MakePath(cue.Str(n)))
		if err := ev.Validate(); err != nil {
			problems = append(problems, "inputs."+n+": "+headline(cueerrors.Details(err, nil)))
		}
		if in, ok := byName[n]; ok && !seen[n] {
			seen[n] = true
			out = append(out, in)
		} else if !ok {
			problems = append(problems, "inputs."+n+" is not a field of the definition that a person fills in: it does not exist, holds a resource, or is fixed to a value.")
		}
	}
	if len(order) == 0 {
		for _, n := range natural {
			out = append(out, byName[n])
		}
	}
	if out == nil {
		out = []TemplateInput{}
	}
	return out, problems
}

// listElem is the kind of the items of a list field: string, int or number, and empty when they are anything else. The
// field may be a disjunction with a default, such as *[] | [...string].
func listElem(fv cue.Value) string {
	vs := []cue.Value{fv}
	if op, args := fv.Expr(); op == cue.OrOp {
		vs = append(vs, args...)
	}
	for _, v := range vs {
		if e := v.LookupPath(cue.MakePath(cue.AnyIndex)); e.Exists() {
			if k := tplKindOf(e); k == "string" || k == "int" || k == "number" {
				return k
			}
		}
	}
	// the items of an open list are not reachable through a path, so the text of the type says
	if b, err := format.Node(fv.Syntax(cue.Raw())); err == nil {
		t := string(b)
		for _, k := range []string{"string", "int", "number"} {
			if strings.Contains(t, "[...("+k+")]") || strings.Contains(t, "[..."+k+"]") {
				return k
			}
		}
	}
	return ""
}

// applyAsk reads how the template says the form asks for the input: the entry of $template.inputs.
func (in *TemplateInput) applyAsk(ask cue.Value) {
	if !ask.Exists() {
		return
	}
	// An entry that gives no source is a disjunction that still has the empty entry as its default.
	if d, ok := ask.Default(); ok {
		ask = d
	}
	if l, ok := stringAt(ask, "label"); ok && strings.TrimSpace(l) != "" {
		in.Label = clip(oneLine(l), 80)
	}
	if d, ok := stringAt(ask, "description"); ok && strings.TrimSpace(d) != "" {
		in.Doc = clip(strings.TrimSpace(d), 600)
	}
	switch as, _ := stringAt(ask, "as"); as {
	case "text":
		if in.Kind == "string" || in.Kind == "any" {
			in.Long = true
		}
	case "secret":
		if in.Kind == "string" {
			in.Secret = true
		}
	}
	if c, ok := choicesPath(ask); ok && !in.Secret && len(c) <= 200 {
		in.Choices = c
		if in.Kind == "any" {
			in.Kind = "string" // a bound on the values that the data source gives is not known yet, so the type is not either
		}
	}
	if t, ok := stringAt(ask, "entity.type"); ok && t != "" && !in.Secret && in.Choices == "" {
		if a, ok := stringAt(ask, "entity.attr"); ok && a != "" && len(t) <= 120 && len(a) <= 120 {
			in.Entity = &TemplateEntity{Type: t, Attr: a}
		}
	}
}

// holdsResource reports whether a field is a resource or has one below it.
func holdsResource(v cue.Value, depth int) bool {
	if v.IncompleteKind()&cue.StructKind == 0 || depth > 3 {
		return false
	}
	if _, ok := stringAt(v, "$kind"); ok {
		return true
	}
	fs, err := v.Fields()
	if err != nil {
		return false
	}
	for fs.Next() {
		if holdsResource(fs.Value(), depth+1) {
			return true
		}
	}
	return false
}

// kindOf is the kind of value a field takes, as the form needs it.
func tplKindOf(v cue.Value) string {
	k := v.IncompleteKind() &^ cue.NullKind
	switch k {
	case cue.StringKind:
		return "string"
	case cue.IntKind:
		return "int"
	case cue.NumberKind, cue.FloatKind:
		return "number"
	case cue.BoolKind:
		return "bool"
	case cue.StructKind:
		return "object"
	case cue.ListKind:
		return "list"
	}
	return "any"
}

// stringAt reads a concrete string below a value.
func stringAt(v cue.Value, path string) (string, bool) {
	f := v.LookupPath(cue.ParsePath(path))
	if !f.Exists() {
		return "", false
	}
	s, err := f.String()
	return s, err == nil
}

// choicesPath reads where the values to choose from are: a reference to the attribute, or its path as a string.
func choicesPath(ask cue.Value) (string, bool) {
	c := ask.LookupPath(cue.ParsePath("choices"))
	if !c.Exists() {
		return "", false
	}
	if s, err := c.String(); err == nil {
		return s, s != ""
	}
	// The schema is a conjunct of the field, so the reference is one of the operands.
	vals := []cue.Value{c}
	if op, args := c.Expr(); op == cue.AndOp {
		vals = append(vals, args...)
	}
	for _, v := range vals {
		if _, p := v.ReferencePath(); p.Err() == nil && len(p.Selectors()) > 0 {
			return p.String(), true
		}
	}
	return "", false
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// TemplateTry is the answer of GET /api/templates/try.
type TemplateTry struct {
	Version  int    `json:"version"`
	Template string `json:"template"`
	// Snippet is the CUE to paste. File is where the template's other instances are.
	Snippet string `json:"snippet"`
	File    string `json:"file,omitempty"`
	// Address is where the instance will sit.
	Address string `json:"address"`
	// State is "valid", "incomplete" (an input that needs a value has none), "invalid" (the specification does not load
	// with the snippet) or "exists" (the address is taken).
	State string `json:"state"`
	// Problems are what the specification says; Field names the input a problem is about when it can be told, and "@label" the
	// name of the instance.
	Problems []TemplateProblem `json:"problems"`
	// Creates are the resources the snippet adds.
	Creates []TemplateCreate `json:"creates"`
	// Secrets are the secrets the snippet reads, each declared in the snippet; the value of one is never in it.
	Secrets []TemplateSecret `json:"secrets,omitempty"`
	// Policy is what the policies say of an approximate plan that creates those resources.
	Policy *TemplatePolicy `json:"policy,omitempty"`
	Took   int64           `json:"took_ms"`
}

// TemplateProblem is one thing wrong with the snippet.
type TemplateProblem struct {
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// TemplateCreate is a resource that the snippet adds.
type TemplateCreate struct {
	Address string `json:"address"`
	Type    string `json:"type"`
}

// TemplatePolicy is the preview of the policies.
type TemplatePolicy struct {
	// State is "none" (no policy to judge by), "holds", "refused" or "unsure".
	State string `json:"state"`
	// Note says what the preview could not decide.
	Note     string               `json:"note,omitempty"`
	Policies []TemplatePolicyItem `json:"policies"`
}

// TemplatePolicyItem is what one policy says.
type TemplatePolicyItem struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"` // in-force or declared
	Outcome string   `json:"outcome"`
	Issues  []string `json:"issues,omitempty"`
}

var labelOK = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)

// templateTryHandler answers GET /api/templates/try?template=NAME&at=PATH&name=LABEL&v.FIELD=EXPR...
func (s *Server) templateTryHandler(w http.ResponseWriter, r *http.Request, m *model) {
	began := time.Now()
	q := r.URL.Query()
	cat := m.templates()
	if !cat.SpecOK {
		writeError(w, http.StatusConflict, "The specification does not load, so no template is known.")
		return
	}
	var tpl *TemplateEntry
	for i := range cat.Templates {
		if cat.Templates[i].Name == q.Get("template") {
			tpl = &cat.Templates[i]
		}
	}
	if tpl == nil {
		writeError(w, http.StatusNotFound, "no such template")
		return
	}
	label := q.Get("name")
	if !labelOK.MatchString(label) {
		writeError(w, http.StatusBadRequest, "name is required: letters, digits, dashes and dots, up to 63")
		return
	}
	at := q.Get("at")
	var atSels []cue.Selector
	if at != "" {
		p := cue.ParsePath(at)
		if len(at) > 256 || p.Err() != nil || len(p.Selectors()) > 8 {
			writeError(w, http.StatusBadRequest, "at is not a path of the specification")
			return
		}
		for _, sel := range p.Selectors() {
			if sel.LabelType() != cue.StringLabel {
				writeError(w, http.StatusBadRequest, "at is not a path of the specification")
				return
			}
		}
		atSels = p.Selectors()
	}
	known := map[string]bool{}
	for _, in := range tpl.Inputs {
		known[in.Name] = true
	}
	values := map[string]string{}
	var problems []TemplateProblem
	for key, vs := range q {
		field, ok := strings.CutPrefix(key, "v.")
		if !ok || len(vs) == 0 || strings.TrimSpace(vs[0]) == "" {
			continue
		}
		if !known[field] {
			writeError(w, http.StatusBadRequest, "the template has no input "+strconv.Quote(clip(field, 60)))
			return
		}
		if len(vs[0]) > maxTemplateExpr {
			writeError(w, http.StatusBadRequest, "the value of "+field+" is too long")
			return
		}
		if _, err := parser.ParseExpr("input", vs[0]); err != nil {
			problems = append(problems, TemplateProblem{Field: field, Message: "not CUE: " + headline(cueerrors.Details(err, nil))})
			continue
		}
		values[field] = vs[0]
	}

	// The inputs that are secrets: the form sends where the secret comes from and its name, never a value.
	alias, hasImport := "pravo", false
	if tpl.File != "" {
		if b, err := os.ReadFile(filepath.Join(s.opts.Dir, filepath.FromSlash(tpl.File))); err == nil && len(b) < 1<<20 {
			if a, ok := importOf(string(b), pravoPath); ok {
				alias, hasImport = a, true
			}
		}
	}
	var secrets []TemplateSecret
	var decls []string
	for key, vs := range q {
		field, ok := strings.CutPrefix(key, "s.")
		if !ok || len(vs) == 0 || strings.TrimSpace(vs[0]) == "" {
			continue
		}
		in := tpl.input(field)
		if in == nil || !in.Secret {
			writeError(w, http.StatusBadRequest, "the template has no secret input "+strconv.Quote(clip(field, 60)))
			return
		}
		source, name := "state", strings.TrimSpace(vs[0])
		if q.Get("sk."+field) == "env" {
			source = "env"
		}
		if (source == "state" && !secretNameOK.MatchString(name)) || (source == "env" && !envNameOK.MatchString(name)) {
			problems = append(problems, TemplateProblem{Field: field, Message: "not a name a " + map[string]string{"state": "secret", "env": "variable"}[source] + " can have"})
			continue
		}
		sec, decl, expr := secretDecl(alias, label, in, source, name)
		if _, taken := m.spec.byPath[sec.Decl]; taken {
			problems = append(problems, TemplateProblem{Field: "@label", Message: "The secret would be declared as " + sec.Decl + ", which the specification already has. Another name avoids it."})
			continue
		}
		if source == "state" {
			if m.secs == nil || !m.secs.Sealed {
				problems = append(problems, TemplateProblem{Field: field, Message: "The state is not sealed, so it cannot keep a secret. Read it from an environment variable, or seal the state."})
				continue
			}
			for _, e := range m.secs.Secrets {
				if e.Name == name && e.Status == "set" {
					sec.Exists = true
				}
			}
			sec.Command = "pravo secret set " + name
		}
		values[field] = expr
		secrets = append(secrets, sec)
		note := "// " + in.Name + " is read from the secret " + name + " of the state."
		if source == "state" && !sec.Exists {
			note = "// " + in.Name + " reads the secret " + name + ".\n// Set it before the apply: " + sec.Command
		} else if source == "env" {
			note = "// " + in.Name + " is read from the environment variable " + name + "\n// when pravo runs."
		}
		decls = append(decls, note+"\n"+decl)
	}
	sort.Slice(secrets, func(i, j int) bool { return secrets[i].Input < secrets[j].Input })
	sort.Strings(decls)

	// A value that the template offers choices for must be one of them, as far as the state knows them.
	for _, in := range tpl.Inputs {
		raw, ok := values[in.Name]
		if !ok || in.Choices == "" || len(problems) > 0 {
			continue
		}
		var want string
		if json.Unmarshal([]byte(raw), &want) != nil {
			continue
		}
		ch := m.choicesOf(in.Name, in.Choices)
		if ch.State != "ok" {
			continue
		}
		found := false
		for _, c := range ch.Items {
			if c.Value == want {
				found = true
			}
		}
		if !found {
			problems = append(problems, TemplateProblem{Field: in.Name, Message: strconv.Quote(want) + " is not one of the values that " + ch.Node + " holds"})
		}
	}

	target := cue.MakePath(append(append([]cue.Selector{}, atSels...), cue.Str(label))...)
	out := &TemplateTry{Version: APIVersion, Template: tpl.Name, File: tpl.File, Address: target.String(), State: "valid", Problems: []TemplateProblem{}, Creates: []TemplateCreate{}, Secrets: secrets}
	body := templateSnippet(tpl, atSels, label, values, decls)
	importLine := ""
	if len(secrets) > 0 {
		importLine = "import " + importName(alias) + "\"" + pravoPath + "\"\n"
	}
	out.Snippet = body
	if importLine != "" && !hasImport {
		out.Snippet = importLine + "\n" + body
	}
	if len(problems) == 0 {
		for _, in := range tpl.Inputs {
			if _, ok := values[in.Name]; in.Required && !ok {
				problems = append(problems, TemplateProblem{Field: in.Name, Message: "needs a value"})
			}
		}
		if len(problems) > 0 {
			out.State, out.Problems = "incomplete", problems
			out.Took = time.Since(began).Milliseconds()
			writeJSON(w, http.StatusOK, out)
			return
		}
	}
	if len(problems) > 0 {
		out.State, out.Problems = "invalid", problems
		out.Took = time.Since(began).Milliseconds()
		writeJSON(w, http.StatusOK, out)
		return
	}
	for addr := range m.spec.byPath {
		if addr == out.Address || strings.HasPrefix(addr, out.Address+".") {
			out.State = "exists"
			out.Problems = append(out.Problems, TemplateProblem{Field: "@label", Message: out.Address + " is already in the specification."})
			break
		}
	}
	if out.State == "exists" {
		out.Took = time.Since(began).Milliseconds()
		writeJSON(w, http.StatusOK, out)
		return
	}

	s.tplMu.Lock()
	lo := config.LoadOptions{Tags: s.opts.Tags, SysTags: s.opts.SysTags, Env: s.opts.Env,
		Extra: map[string]string{previewFile: "package " + m.spec.root.PkgName + "\n\n" + importLine + body}}
	root, err := config.Load(s.opts.Dir, lo)
	s.tplMu.Unlock()
	if err != nil {
		// Loading reads no state: what the error quotes is what the form sent and what the
		// specification says, and a value that merely equals a sensitive string is not one.
		out.State = "invalid"
		out.Problems = append(out.Problems, templateProblems(errorText(err), out.Address)...)
		out.Took = time.Since(began).Milliseconds()
		writeJSON(w, http.StatusOK, out)
		return
	}
	var changes []engine.ChangeView
	for _, res := range root.Resources {
		if _, had := m.spec.byPath[res.Address()]; had {
			continue
		}
		out.Creates = append(out.Creates, TemplateCreate{Address: res.Address(), Type: res.Type})
		if res.Mode == "managed" {
			changes = append(changes, engine.ChangeView{Addr: res.Address(), Mode: "managed", Type: res.Type, Name: res.Name, Action: engine.ActionCreate,
				Provider: res.ProviderKey, After: configured(root.Value.LookupPath(res.Path))})
		}
	}
	out.Policy = s.previewPolicies(m, changes)
	out.Took = time.Since(began).Milliseconds()
	writeJSON(w, http.StatusOK, out)
}

// templateSnippet is the CUE of an instance: the path, the definition and the values that were given.
func templateSnippet(tpl *TemplateEntry, at []cue.Selector, label string, values map[string]string, decls []string) string {
	var b strings.Builder
	for _, sel := range at {
		b.WriteString(cueLabel(sel.Unquoted()) + ": ")
	}
	b.WriteString(cueLabel(label) + ": " + tpl.Definition + " & {")
	n := 0
	for _, in := range tpl.Inputs {
		if v, ok := values[in.Name]; ok {
			b.WriteString("\n\t" + cueLabel(in.Name) + ": " + v)
			n++
		}
	}
	if n > 0 {
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	for _, d := range decls {
		b.WriteString("\n" + d + "\n")
	}
	return b.String()
}

const pravoPath = "pravo.dev/pravo"

// importName is what precedes the path in an import: nothing for the name the package has already.
func importName(alias string) string {
	if alias == "pravo" || alias == "" {
		return ""
	}
	return alias + " "
}

func cueLabel(l string) string {
	if cueIdent.MatchString(l) {
		return l
	}
	return strconv.Quote(l)
}

var problemHead = regexp.MustCompile(`^([A-Za-z0-9_."\-]+): `)

// templateProblems splits the message of a load error into the problems it lists, each tied to the input it is about
// when its path leads into the new instance. The lines that only repeat a disjunction are left out: the line that
// says which bound the value is out of says it better.
func templateProblems(text, addr string) []TemplateProblem {
	var out []TemplateProblem
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue // blank, or a position in a file
		}
		if strings.HasPrefix(line, "Error:") || strings.HasPrefix(line, "evaluating ") || strings.HasPrefix(line, "invalid configuration") || strings.HasPrefix(line, "loading ") {
			continue
		}
		p := TemplateProblem{Message: strings.TrimSuffix(line, ":")}
		if mm := problemHead.FindStringSubmatch(line); mm != nil {
			if rest, ok := strings.CutPrefix(mm[1], addr+"."); ok {
				p.Field, _, _ = strings.Cut(rest, ".")
				p.Message = strings.TrimSuffix(strings.TrimPrefix(line, addr+"."), ":")
			}
		}
		out = append(out, p)
	}
	// a field that has a specific problem needs no line about its disjunction
	specific := map[string]bool{}
	for _, p := range out {
		if p.Field != "" && !strings.Contains(p.Message, "errors in empty disjunction") && !strings.Contains(p.Message, ": conflicting values") {
			specific[p.Field] = true
		}
	}
	var kept []TemplateProblem
	for _, p := range out {
		if specific[p.Field] && (strings.Contains(p.Message, "errors in empty disjunction") || strings.Contains(p.Message, ": conflicting values")) {
			continue
		}
		kept = append(kept, p)
	}
	if len(kept) == 0 {
		kept = append(kept, TemplateProblem{Message: headline(text)})
	}
	if len(kept) > 12 {
		kept = kept[:12]
	}
	return kept
}

// configured reads the values that a resource of the specification sets, leaving out what is not known yet: an
// attribute that reads another resource, or that a provider fills in.
func configured(v cue.Value) map[string]any {
	out := map[string]any{}
	if m, ok := concreteOf(v, 0).(map[string]any); ok {
		for k := range m {
			if !strings.HasPrefix(k, "$") {
				out[k] = m[k]
			}
		}
	}
	return out
}

// concreteOf is the value without the parts that are not concrete; nil when none is.
func concreteOf(v cue.Value, depth int) any {
	if d, ok := v.Default(); ok {
		v = d
	}
	if depth > 12 {
		return nil
	}
	switch k := v.IncompleteKind(); {
	case k == cue.NullKind:
		return nil
	case k&cue.StructKind != 0 && k&^cue.StructKind == 0:
		fs, err := v.Fields()
		if err != nil {
			return nil
		}
		m := map[string]any{}
		for fs.Next() {
			if x := concreteOf(fs.Value(), depth+1); x != nil {
				m[fs.Selector().Unquoted()] = x
			}
		}
		return m
	case k&cue.ListKind != 0 && k&^cue.ListKind == 0:
		it, err := v.List()
		if err != nil {
			return nil
		}
		var l []any
		for it.Next() {
			x := concreteOf(it.Value(), depth+1)
			if x == nil {
				return nil // a list with a part that is not known is not known
			}
			l = append(l, x)
		}
		return l
	}
	if !v.IsConcrete() {
		return nil
	}
	var x any
	if err := v.Decode(&x); err != nil {
		return nil
	}
	return x
}

// previewPolicies tries every policy of the estate on the approximate plan.
func (s *Server) previewPolicies(m *model, changes []engine.ChangeView) *TemplatePolicy {
	res := &TemplatePolicy{State: "none", Policies: []TemplatePolicyItem{}}
	if m.pol == nil || len(m.pol.Policies) == 0 {
		res.Note = "The estate has no policy."
		return res
	}
	if len(changes) == 0 {
		res.Note = "The snippet adds no resource, so no policy judges it."
		return res
	}
	doc, err := engine.ProposedDoc(changes)
	if err != nil {
		res.State, res.Note = "unsure", "The plan could not be made into a document."
		return res
	}
	tr := m.studio.tr()
	key := fmt.Sprintf("template:%d:%x", len(changes), time.Now().UnixNano())
	tr.Add(key, doc)
	res.State = "holds"
	for _, p := range m.pol.Policies {
		t := m.polText[p.Name]
		text, status := t.inForce, "in-force"
		if text == "" {
			text, status = t.declared, "declared"
		}
		if text == "" {
			continue
		}
		fr := tr.TryFile(policyDeclaration(p.Name, "", clip(text, maxPolicyText)), []string{key})
		item := TemplatePolicyItem{Name: p.Name, Status: status, Outcome: "holds"}
		switch {
		case fr.Problem != nil || len(fr.Plans) == 0:
			item.Outcome = "unsure"
			if res.State == "holds" {
				res.State = "unsure"
			}
		case fr.Plans[0].Outcome == engine.RuleRefused:
			item.Outcome = "refused"
			res.State = "refused"
			for _, is := range fr.Plans[0].Issues {
				if len(item.Issues) < 6 && !strings.Contains(is.Message, "errors in empty disjunction") {
					item.Issues = append(item.Issues, clip(stripControl(m.scrubText(is.Message)), 300))
				}
			}
		case fr.Plans[0].Outcome == engine.RuleInvalid:
			item.Outcome = "unsure"
			if res.State == "holds" {
				res.State = "unsure"
			}
		}
		res.Policies = append(res.Policies, item)
	}
	if len(res.Policies) == 0 {
		res.State, res.Note = "none", "The estate has no policy."
		return res
	}
	res.Note = "Judged on an approximate plan: every new resource is a create, with the values the specification sets and none that a provider fills in. A rule that reads a value the provider decides cannot be decided here."
	return res
}

// TemplateRefs is the answer of GET /api/templates/refs.
type TemplateRefs struct {
	Version int           `json:"version"`
	Refs    []TemplateRef `json:"refs"`
}

// TemplateRef is a value of the graph that an input could read.
type TemplateRef struct {
	// Expr is the CUE that reads it, such as network.vpc.id.
	Expr string `json:"expr"`
	Addr string `json:"addr"`
	Attr string `json:"attr"`
	Type string `json:"type"`
	// Value and ValueState say what the state holds for it now, as the Inventory would show it.
	Value      string `json:"value,omitempty"`
	ValueState string `json:"value_state"`
}

// templateRefsHandler answers GET /api/templates/refs?kind=string&q=TEXT: attributes of the resources of the estate that
// have the kind, by the names of the schemas, each with the value that the state holds as the Inventory shows it.
func (s *Server) templateRefsHandler(w http.ResponseWriter, r *http.Request, m *model) {
	q := strings.ToLower(r.URL.Query().Get("q"))
	if len(q) > 80 {
		q = q[:80]
	}
	kind := r.URL.Query().Get("kind")
	switch kind {
	case "string", "number", "int", "bool":
	default:
		writeError(w, http.StatusBadRequest, "kind is string, number, int or bool")
		return
	}
	want := kind
	if kind == "int" {
		want = "number"
	}
	out := &TemplateRefs{Version: APIVersion, Refs: []TemplateRef{}}
	for _, addr := range m.order {
		mn := m.nodes[addr]
		if mn == nil || mn.res == nil || mn.res.Mode != "managed" || mn.sch == nil || mn.sch.Block == nil {
			continue
		}
		for _, a := range mn.sch.Block.Attributes {
			if a.Sensitive || a.WriteOnly || len(a.TypeJSON) == 0 || strings.Trim(string(a.TypeJSON), `"`) != want {
				continue
			}
			expr := addr + "." + a.Name
			if q != "" && !strings.Contains(strings.ToLower(expr), q) {
				continue
			}
			ref := TemplateRef{Expr: expr, Addr: addr, Attr: a.Name, Type: mn.res.Type}
			ref.Value, ref.ValueState = m.valueOf(mn, a.Name)
			out.Refs = append(out.Refs, ref)
			if len(out.Refs) >= 40 {
				writeJSON(w, http.StatusOK, out)
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}
