package serve

import (
	"strings"
	"sync"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/pravo-systems/pravo/internal/schema"
)

// SchemaField is a field of the plan document, for completing what a person writes in the editor: its name, what the
// schema says of it, and the fields inside it. A list has the fields of its elements.
type SchemaField struct {
	Name     string `json:"name"`
	Doc      string `json:"doc,omitempty"`
	Type     string `json:"type"`
	Optional bool   `json:"optional,omitempty"`
	// List is set when Fields are those of each element of a list, which a rule writes inside [...{ }].
	List bool `json:"list,omitempty"`
	// Enum lists the values of a field that is one of a few strings.
	Enum   []string      `json:"enum,omitempty"`
	Fields []SchemaField `json:"fields,omitempty"`
}

var (
	schemaOnce   sync.Once
	schemaFields []SchemaField
)

// planSchema lists the fields of #Plan, to a depth that the document has.
func planSchema() []SchemaField {
	schemaOnce.Do(func() {
		ctx := cuecontext.New()
		pkg := ctx.CompileString(schema.SettingsPackage)
		if pkg.Err() != nil {
			return
		}
		plan := pkg.LookupPath(cue.MakePath(cue.Def("Plan")))
		if plan.Err() != nil {
			return
		}
		schemaFields = fieldsOf(plan, 0)
	})
	return schemaFields
}

func fieldsOf(v cue.Value, depth int) []SchemaField {
	if depth > 4 {
		return nil
	}
	it, err := v.Fields(cue.Optional(true))
	if err != nil {
		return nil
	}
	var out []SchemaField
	for it.Next() {
		fv := it.Value()
		f := SchemaField{Name: it.Selector().Unquoted(), Type: kindName(fv), Optional: it.IsOptional()}
		for _, c := range fv.Doc() {
			f.Doc += strings.TrimSpace(c.Text()) + " "
		}
		f.Doc = strings.TrimSpace(f.Doc)
		if op, args := fv.Eval().Expr(); op == cue.OrOp {
			for _, a := range args {
				if s, err := a.String(); err == nil {
					f.Enum = append(f.Enum, s)
				} else {
					f.Enum = nil
					break
				}
			}
			if len(f.Enum) > 0 {
				f.Type = "enum"
			}
		}
		switch fv.IncompleteKind() {
		case cue.ListKind:
			f.Type = "list"
			if e := fv.LookupPath(cue.MakePath(cue.AnyIndex)); e.Exists() {
				f.List = true
				f.Fields = fieldsOf(e, depth+1)
			}
		case cue.StructKind:
			f.Fields = fieldsOf(fv, depth+1)
		}
		out = append(out, f)
	}
	return out
}

func kindName(v cue.Value) string {
	k := v.IncompleteKind()
	switch k {
	case cue.StructKind:
		return "struct"
	case cue.ListKind:
		return "list"
	case cue.TopKind:
		return "any"
	}
	return k.String()
}
