package serve

import (
	"encoding/json"
	"os"
	"strings"

	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/schema"
)

// unrecorded fills the detail of a node that has no record: an ephemeral resource,
// an environment variable, a node that is declared and not created yet. The
// State tab of such a node has no attributes to take from the state and the
// Schema tab has none to take them from, so the detail carries what can be said
// without a record: the schema of the type, and for an ephemeral resource and
// an environment variable what the specification sets, as far as it is known
// before a command runs.
func (m *model) unrecorded(mn *mnode, d *Detail, sec *secretSet) {
	if mn.res == nil || mn.Sealed || mn.isOutput() {
		return
	}
	if mn.res.Builtin {
		switch mn.res.Type {
		case "env", "secret_env":
			m.envDetail(mn, d, sec)
		}
		return
	}
	if mn.sch != nil {
		d.Schema = schemaListing(mn.sch.Block)
	}
	if mn.Mode == "ephemeral" {
		m.specDetail(mn, d, sec)
	}
}

// schemaListing lists the attributes of a block without values: names, types,
// kinds and descriptions.
func schemaListing(b *schema.Block) []Attr {
	w := &walk{secrets: newSecretSet(), build: true}
	out := w.block(b, map[string]any{}, 0)
	for i := range out {
		out[i].Null = false
	}
	return out
}

// specDetail fills the attributes of an ephemeral resource with what the
// specification sets on it. A value that depends on another node, or on what the
// provider fills in, is not known before a command runs: it reads "known after
// apply", as it does in a plan.
func (m *model) specDetail(mn *mnode, d *Detail, sec *secretSet) {
	args, unknown, ok := m.specArgs(mn)
	if !ok {
		return
	}
	w := &walk{secrets: sec, build: true}
	var attrs []Attr
	if mn.sch != nil {
		for _, a := range w.block(mn.sch.Block, args, 0) {
			if _, set := args[a.Name]; set {
				attrs = append(attrs, a)
			} else if unknown[a.Name] {
				a.Null, a.Unknown = false, true
				attrs = append(attrs, a)
			}
		}
		for name := range unknown {
			if !hasAttr(attrs, name) {
				attrs = append(attrs, Attr{Name: name, Unknown: true})
			}
		}
	} else {
		attrs = withholdAll(args)
		for name := range unknown {
			attrs = append(attrs, Attr{Name: name, Unknown: true})
		}
		d.Notes = append(d.Notes, "The schema of this type is not cached, so the values that the specification sets are withheld. Run \"pravo init\" to fetch it.")
	}
	d.Attrs = attrs
	if len(attrs) > 0 {
		d.Notes = append(d.Notes, "These are the values that the specification sets. What the provider fills in is known only while a command runs.")
	}
}

func hasAttr(attrs []Attr, name string) bool {
	for _, a := range attrs {
		if a.Name == name {
			return true
		}
	}
	return false
}

// specArgs reads the fields that the specification sets on a node: the ones that
// are concrete, and the names of the ones that are not.
func (m *model) specArgs(mn *mnode) (args map[string]any, unknown map[string]bool, ok bool) {
	ss := m.spec
	if ss.root == nil || mn.res == nil {
		return nil, nil, false
	}
	ss.cueMu.Lock()
	defer ss.cueMu.Unlock()
	v := ss.root.Value.LookupPath(mn.res.Path)
	if !v.Exists() {
		return nil, nil, false
	}
	it, err := v.Fields(cue.Optional(false), cue.Hidden(false), cue.Definitions(false))
	if err != nil {
		return nil, nil, false
	}
	args, unknown = map[string]any{}, map[string]bool{}
	for it.Next() {
		sel := it.Selector()
		if sel.LabelType() != cue.StringLabel {
			continue
		}
		name := sel.Unquoted()
		if strings.HasPrefix(name, "$") {
			continue
		}
		raw, err := it.Value().MarshalJSON()
		if err != nil {
			unknown[name] = true
			continue
		}
		dv, err := decodeAttrs(json.RawMessage(`{"v":` + string(raw) + `}`))
		if err != nil {
			unknown[name] = true
			continue
		}
		args[name] = dv["v"]
	}
	return args, unknown, true
}

// envSchema is the declaration of pravo.#Env and pravo.#SecretEnv, which the
// built-in package gives.
func envSchema(secret bool) []Attr {
	out := []Attr{{Name: "name", Type: "string", Kind: "required", Description: "The name of the environment variable."}}
	if !secret {
		out = append(out, Attr{Name: "default", Type: "string", Kind: "optional", Description: "Used when the variable is not set. Without it, an unset variable is an error."})
	}
	value := Attr{Name: "value", Type: "string", Kind: "computed", Description: "What the variable holds."}
	if secret {
		value.Sensitive = true
	}
	return append(out, value)
}

// envDetail fills the detail of a pravo.#Env or a pravo.#SecretEnv. Its State is
// what the declaration says, and the value that the variable has in the
// environment of this process; a command reads the variable from its own. A secret
// variable shows no value: it is ephemeral and kept out of the state, and so it is
// kept out of the viewer.
func (m *model) envDetail(mn *mnode, d *Detail, sec *secretSet) {
	secret := mn.res.Type == "secret_env"
	d.Schema = envSchema(secret)
	name, def, hasDef, known := m.envSpec(mn)
	w := &walk{secrets: sec, build: true}
	text := func(at *Attr, s string) {
		if guarded(name) {
			w.withhold(at, s, "name")
			return
		}
		w.scalar(at, s)
	}
	nameAt := Attr{Name: "name", Type: "string", Kind: "required"}
	if known {
		nameAt.Value = name
	} else {
		nameAt.Unknown = true
	}
	d.Attrs = append(d.Attrs, nameAt)
	if !secret {
		defAt := Attr{Name: "default", Type: "string", Kind: "optional"}
		if hasDef {
			text(&defAt, def)
		} else {
			defAt.Null = true
		}
		d.Attrs = append(d.Attrs, defAt)
	}
	value := Attr{Name: "value", Type: "string", Kind: "computed"}
	switch {
	case secret:
		value.Sensitive, value.Redacted = true, "sensitive"
		d.Notes = append(d.Notes, "A secret variable shows no value. Its value is ephemeral: it is kept out of the state and out of a saved plan, and out of this page.")
	case !known:
		value.Unknown = true
		d.Notes = append(d.Notes, "The name of the variable depends on something that is not known before a command runs.")
	default:
		v, set := os.LookupEnv(name)
		fromDefault := false
		if !set && hasDef {
			v, set, fromDefault = def, true, true
		}
		switch {
		case !set:
			value.Null = true
			d.Notes = append(d.Notes, "The variable "+name+" is not set in the environment of pravo serve, and there is no default: a command would stop with an error.")
		default:
			text(&value, v)
			if fromDefault {
				d.Notes = append(d.Notes, "The variable "+name+" is not set in the environment of pravo serve, so the value is the default.")
			} else {
				d.Notes = append(d.Notes, "The value is that of the variable in the environment of pravo serve. A command reads the variable from its own environment.")
			}
		}
	}
	d.Attrs = append(d.Attrs, value)
}

// envSpec reads the name and the default of a variable from the specification.
func (m *model) envSpec(mn *mnode) (name, def string, hasDef, known bool) {
	ss := m.spec
	if ss.root == nil || mn.res == nil {
		return "", "", false, false
	}
	ss.cueMu.Lock()
	defer ss.cueMu.Unlock()
	v := ss.root.Value.LookupPath(mn.res.Path)
	if !v.Exists() {
		return "", "", false, false
	}
	n, err := v.LookupPath(cue.ParsePath("name")).String()
	if err != nil {
		return "", "", false, false
	}
	if dv, err := v.LookupPath(cue.ParsePath("default")).String(); err == nil {
		def, hasDef = dv, true
	}
	return n, def, hasDef, true
}
