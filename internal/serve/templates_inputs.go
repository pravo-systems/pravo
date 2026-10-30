package serve

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/parser"
)

// The inputs of a template that are not typed in: values to choose from that a node of the graph holds, existing
// resources to refer to, and secrets. A template says which in $template.inputs; the form takes the choice and the
// server reads the rest. Nothing a template does not name is read, and what is read is shown as the Inventory shows
// it: a value that is withheld there is withheld here.

// TemplateChoices is the answer of GET /api/templates/choices?template=NAME&input=FIELD.
type TemplateChoices struct {
	Version int    `json:"version"`
	Input   string `json:"input"`
	// Source is the path the template names, such as regions.outputs, and Node the node that holds it.
	Source string `json:"source"`
	Node   string `json:"node,omitempty"`
	// State is "ok", "empty", "unread" (the node has no record), "withheld" (the values are not shown) or "missing".
	State string `json:"state"`
	Note  string `json:"note,omitempty"`
	// ReadAt is when the state last read the node.
	ReadAt  *time.Time       `json:"read_at,omitempty"`
	Items   []TemplateChoice `json:"items"`
	Omitted int              `json:"omitted,omitempty"`
}

// TemplateChoice is one value to choose; Label is what a map says of it.
type TemplateChoice struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// TemplateEntities is the answer of GET /api/templates/entities?template=NAME&input=FIELD&q=TEXT.
type TemplateEntities struct {
	Version int    `json:"version"`
	Input   string `json:"input"`
	Type    string `json:"type"`
	Attr    string `json:"attr"`
	// Note says why the list is empty or short when it is.
	Note     string              `json:"note,omitempty"`
	Entities []TemplateEntityRow `json:"entities"`
}

// TemplateEntityRow is a resource that an input can refer to.
type TemplateEntityRow struct {
	Addr string `json:"addr"`
	// Expr is the expression that reads the attribute: what the code holds when this row is picked.
	Expr      string `json:"expr"`
	Namespace string `json:"namespace,omitempty"`
	Provider  string `json:"provider,omitempty"`
	// Recorded says that the state holds the resource, that is, an apply has made it.
	Recorded bool `json:"recorded"`
	// Value and ValueState say what the state holds for the attribute now.
	Value      string `json:"value,omitempty"`
	ValueState string `json:"value_state"`
}

// TemplateSecret is a secret that an instance reads, as the code of the instance declares it.
type TemplateSecret struct {
	Input string `json:"input"`
	// Source is "state" (pravo.#Secret) or "env" (pravo.#SecretEnv); Name is the name of the secret or of the variable.
	Source string `json:"source"`
	Name   string `json:"name"`
	// Decl is the address of the declaration that the snippet adds.
	Decl string `json:"decl"`
	// Exists says that the sealed state holds the secret; it is only asked of a secret of the state.
	Exists bool `json:"exists"`
	// Command sets the secret, for a secret of the state.
	Command string `json:"command,omitempty"`
}

var (
	secretNameOK = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./-]{0,127}$`)
	envNameOK    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	declSafe     = regexp.MustCompile(`[^A-Za-z0-9_]`)
)

const (
	maxChoices  = 200
	maxEntities = 60
)

// templateInput finds an input of a template by name.
func (t *TemplateEntry) input(name string) *TemplateInput {
	for i := range t.Inputs {
		if t.Inputs[i].Name == name {
			return &t.Inputs[i]
		}
	}
	return nil
}

// templateByName finds a template of the catalog.
func (c *Templates) byName(name string) *TemplateEntry {
	for i := range c.Templates {
		if c.Templates[i].Name == name {
			return &c.Templates[i]
		}
	}
	return nil
}

// choicesOf reads the values an input of a template offers, from the node that its path names.
func (m *model) choicesOf(input, path string) TemplateChoices {
	out := TemplateChoices{Version: APIVersion, Input: input, Source: path, State: "missing", Items: []TemplateChoice{}}
	p := cue.ParsePath(path)
	if p.Err() != nil || len(p.Selectors()) < 2 {
		out.Note = "The path " + path + " does not name an attribute of a node."
		return out
	}
	sels := p.Selectors()
	var mn *mnode
	var rest []cue.Selector
	for k := len(sels) - 1; k >= 1; k-- {
		if n := m.nodes[cue.MakePath(sels[:k]...).String()]; n != nil && n.Mode != "output" {
			mn, rest = n, sels[k:]
			break
		}
	}
	if mn == nil {
		out.Note = "No node of the specification or of the state is at " + path + "."
		return out
	}
	out.Node = mn.Addr
	switch {
	case mn.Mode != "managed" && mn.Mode != "data":
		out.State = "withheld"
		out.Note = mn.Addr + " is not recorded, so it holds no value to choose from."
		return out
	case mn.rec == nil:
		out.State = "unread"
		if mn.Mode == "data" {
			out.Note = mn.Addr + " has not been read yet: a plan reads a data source and records what it found."
		} else {
			out.Note = mn.Addr + " has no record yet: an apply makes it."
		}
		return out
	case mn.Sealed || mn.sch == nil:
		out.State = "withheld"
		out.Note = "The values of " + mn.Addr + " are not shown to this reader."
		return out
	}
	out.ReadAt = mn.rec.RefreshedAt
	if out.ReadAt == nil {
		out.ReadAt = mn.rec.AppliedAt
	}
	attrs, err := decodeAttrs(mn.rec.Attributes)
	if err != nil {
		out.State = "withheld"
		out.Note = "The record of " + mn.Addr + " cannot be read."
		return out
	}
	w := m.walkFor(m.plan, mn.Addr, true)
	at := Attr{Children: w.block(mn.sch.Block, attrs, 0)}
	for _, sel := range rest {
		name := sel.Unquoted()
		var next *Attr
		for i := range at.Children {
			if at.Children[i].Name == name {
				next = &at.Children[i]
				break
			}
		}
		if next == nil {
			out.State = "missing"
			out.Note = mn.Addr + " has no value at " + path + "."
			return out
		}
		at = *next
	}
	if at.Redacted != "" {
		out.State = "withheld"
		out.Note = "The values of " + path + " are withheld."
		return out
	}
	isList := strings.HasPrefix(at.Type, "list") || strings.HasPrefix(at.Type, "set")
	for _, c := range at.Children {
		if len(out.Items) >= maxChoices {
			out.Omitted++
			continue
		}
		if c.Redacted != "" || c.Null || len(c.Children) > 0 {
			out.Omitted++
			continue
		}
		val := fmt.Sprint(c.Value)
		if isList {
			out.Items = append(out.Items, TemplateChoice{Value: val})
			continue
		}
		label := ""
		if _, ok := c.Value.(string); ok {
			label = val
		}
		out.Items = append(out.Items, TemplateChoice{Value: c.Name, Label: label})
	}
	out.Omitted += at.Truncated
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].Value < out.Items[j].Value })
	out.State = "ok"
	if len(out.Items) == 0 {
		out.State = "empty"
		out.Note = path + " holds no value to choose from."
	}
	return out
}

// entitiesOf lists the managed resources of the type that the specification declares, with the expression that reads
// the attribute of each.
func (m *model) entitiesOf(input string, ent *TemplateEntity, q string) TemplateEntities {
	out := TemplateEntities{Version: APIVersion, Input: input, Type: ent.Type, Attr: ent.Attr, Entities: []TemplateEntityRow{}}
	q = strings.ToLower(q)
	seen := 0
	for _, addr := range m.order {
		mn := m.nodes[addr]
		if mn == nil || mn.Mode != "managed" || mn.Type != ent.Type {
			continue
		}
		seen++
		if mn.sch != nil && mn.sch.Block != nil {
			ok := false
			for _, a := range mn.sch.Block.Attributes {
				if a.Name == ent.Attr {
					ok = !a.Sensitive && !a.WriteOnly
				}
			}
			if !ok {
				continue
			}
		}
		if mn.res == nil {
			continue // recorded but no longer declared: a new instance cannot refer to it
		}
		expr := addr + "." + ent.Attr
		if q != "" && !strings.Contains(strings.ToLower(expr), q) {
			continue
		}
		if len(out.Entities) >= maxEntities {
			break
		}
		row := TemplateEntityRow{Addr: addr, Expr: expr, Namespace: mn.Namespace, Provider: mn.Provider, Recorded: mn.rec != nil}
		row.Value, row.ValueState = m.valueOf(mn, ent.Attr)
		out.Entities = append(out.Entities, row)
	}
	switch {
	case seen == 0:
		out.Note = "The specification declares no resource of the type " + ent.Type + "."
	case len(out.Entities) == 0 && q == "":
		out.Note = ent.Type + " has no attribute " + ent.Attr + " that can be read."
	}
	return out
}

// Value states: what the state holds for an attribute that a reference reads.
const (
	valueKnown      = "known"      // Value is what the last plan or apply recorded
	valueUnapplied  = "unapplied"  // the resource has no record: the value is known after the apply
	valueWithheld   = "withheld"   // the Inventory does not show it, so this does not either
	valueNull       = "null"       // recorded as null
	valueStructured = "structured" // a list or an object: not shown here
)

// valueOf reads one attribute of a node as the Inventory shows it: the value the state holds now, through the redacting
// walk. It is a snapshot of the last plan or apply, not what a later apply will produce.
func (m *model) valueOf(mn *mnode, attr string) (string, string) {
	switch {
	case mn.rec == nil:
		return "", valueUnapplied
	case mn.Sealed || mn.sch == nil || mn.sch.Block == nil:
		return "", valueWithheld
	}
	attrs, err := decodeAttrs(mn.rec.Attributes)
	if err != nil {
		return "", valueWithheld
	}
	w := m.walkFor(m.plan, mn.Addr, true)
	for _, a := range w.block(mn.sch.Block, attrs, 0) {
		if a.Name != attr {
			continue
		}
		switch {
		case a.Redacted != "":
			return "", valueWithheld
		case a.Unknown:
			return "", valueUnapplied
		case a.Null:
			return "", valueNull
		case len(a.Children) > 0:
			return "", valueStructured
		}
		v := fmt.Sprint(a.Value)
		if _, ok := a.Value.(string); ok {
			v = strconv.Quote(v)
		}
		if len(v) > 120 {
			v = v[:120] + "…"
		}
		return v, valueKnown
	}
	return "", valueNull
}

// secretDecl builds the code that reads a secret for an input: the declaration, and the expression the input holds.
// pkg is the name the pravo package has in the file the code goes into.
func secretDecl(pkg, label string, in *TemplateInput, source, name string) (sec TemplateSecret, decl, expr string) {
	id := declSafe.ReplaceAllString(label+"_"+in.Name, "_")
	if id == "" || (id[0] >= '0' && id[0] <= '9') {
		id = "_" + id
	}
	sec = TemplateSecret{Input: in.Name, Source: source, Name: name, Decl: id}
	kind := "#Secret"
	if source == "env" {
		kind = "#SecretEnv"
	}
	decl = id + ": " + pkg + "." + kind + " & {name: " + fmt.Sprintf("%q", name) + "}"
	return sec, decl, id + ".value"
}

// importOf reports the name that a file of the package gives the import path, and whether it imports it at all.
func importOf(src, path string) (string, bool) {
	f, err := parser.ParseFile("f.cue", src, parser.ImportsOnly)
	if err != nil {
		return "", false
	}
	for spec := range f.ImportSpecs() {
		p := strings.Trim(spec.Path.Value, "`\"")
		if p != path {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name, true
		}
		return path[strings.LastIndex(path, "/")+1:], true
	}
	return "", false
}

// templateChoicesHandler answers GET /api/templates/choices.
func (s *Server) templateChoicesHandler(w http.ResponseWriter, r *http.Request, m *model) {
	_, in, ok := s.templateInputOf(w, r, m)
	if !ok {
		return
	}
	if in.Choices == "" {
		writeError(w, http.StatusNotFound, "the input has no values to choose from")
		return
	}
	writeJSON(w, http.StatusOK, m.choicesOf(in.Name, in.Choices))
}

// templateEntitiesHandler answers GET /api/templates/entities.
func (s *Server) templateEntitiesHandler(w http.ResponseWriter, r *http.Request, m *model) {
	_, in, ok := s.templateInputOf(w, r, m)
	if !ok {
		return
	}
	if in.Entity == nil {
		writeError(w, http.StatusNotFound, "the input does not refer to a resource")
		return
	}
	q := r.URL.Query().Get("q")
	if len(q) > 80 {
		q = q[:80]
	}
	writeJSON(w, http.StatusOK, m.entitiesOf(in.Name, in.Entity, q))
}

// templateInputOf finds the template and the input that a request names.
func (s *Server) templateInputOf(w http.ResponseWriter, r *http.Request, m *model) (*TemplateEntry, *TemplateInput, bool) {
	cat := m.templates()
	if !cat.SpecOK {
		writeError(w, http.StatusConflict, "The specification does not load, so no template is known.")
		return nil, nil, false
	}
	tpl := cat.byName(r.URL.Query().Get("template"))
	if tpl == nil {
		writeError(w, http.StatusNotFound, "no such template")
		return nil, nil, false
	}
	in := tpl.input(r.URL.Query().Get("input"))
	if in == nil {
		writeError(w, http.StatusNotFound, "the template has no such input")
		return nil, nil, false
	}
	return tpl, in, true
}
