package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"

	"github.com/pravo-systems/pravo/internal/registry"
)

// reservedTop lists the top-level fields that are not searched for
// resources: they have a meaning of their own.
var reservedTop = map[string]bool{"$settings": true}

// discover finds every resource in the evaluated specification. A resource
// is any struct that carries the identity fields of a generated definition:
// $kind says what it is, $type which provider type and $source which
// provider. Its address is the CUE path it sits at. keys collects the
// provider configuration keys in use.
func (r *Root) discover() error {
	it, err := r.Value.Fields(cue.Optional(true))
	if err != nil {
		return fmt.Errorf("evaluating the specification: %w", err)
	}
	for it.Next() {
		sel := it.Selector()
		if reservedTop[sel.Unquoted()] {
			continue
		}
		switch sel.ConstraintType() {
		case cue.OptionalConstraint:
			if err := skippedPolicy(it.Value(), []cue.Selector{sel}); err != nil {
				return err
			}
			continue
		case cue.RequiredConstraint:
			if err := unsuppliedProvider(it.Value(), nil, sel); err != nil {
				return err
			}
			if err := skippedPolicy(it.Value(), []cue.Selector{sel}); err != nil {
				return err
			}
			continue
		}
		if err := r.walk(it.Value(), []cue.Selector{sel}, 0); err != nil {
			return err
		}
	}
	if err := r.resolveMoves(); err != nil {
		return err
	}
	return r.resolveProviders()
}

type pendingMove struct {
	to, from string
	group    bool // a struct of resources: every record under from moves under to
}

// resolveMoves turns the $from declarations into moves. A resource's $from is
// the address its record has now; a group's $from is the path its resources
// had, and every record below it moves to the same place below the group.
func (r *Root) resolveMoves() error {
	for _, m := range r.moves {
		if m.from == m.to {
			return fmt.Errorf("%s: $from is the address of the resource itself", m.to)
		}
		mv := Move{From: m.from, To: m.to}
		if m.group {
			if !r.hasResourcesUnder(m.to) {
				return fmt.Errorf("%s: $from on a struct that holds no resources", m.to)
			}
			mv.Prefix = true
		}
		r.Moves = append(r.Moves, mv)
	}
	r.moves = nil
	return nil
}

func (r *Root) walk(v cue.Value, path []cue.Selector, depth int) error {
	if depth > 48 {
		return skippedPolicy(v, path)
	}
	switch v.Kind() {
	case cue.BottomKind:
		// A definition that does not exist is not a value still to be computed:
		// it means the generated package is missing or out of date.
		if err := v.Err(); err != nil && strings.Contains(err.Error(), "undefined field: #") {
			return fmt.Errorf("%s: %w. The generated provider package may be out of date; run \"pravo init\"", cue.MakePath(path...), err)
		}
	case cue.StructKind:
		if kind, ok := stringField(v, "$kind"); ok {
			return r.register(v, kind, path)
		}
		// A struct that groups resources, such as an instance of a module, may
		// say where its resources used to be.
		if from, ok := stringField(v, "$from"); ok {
			r.moves = append(r.moves, pendingMove{to: cue.MakePath(path...).String(), from: from, group: true})
		}
		fields, err := v.Fields(cue.Optional(true))
		if err != nil {
			return nil // not an evaluable struct; nothing to find here
		}
		for fields.Next() {
			sel := fields.Selector()
			switch sel.ConstraintType() {
			case cue.OptionalConstraint:
				if err := skippedPolicy(fields.Value(), append(append([]cue.Selector{}, path...), sel)); err != nil {
					return err
				}
				continue
			case cue.RequiredConstraint:
				// A required field that nothing filled in. When it is a
				// provider, a resource built through it cannot be found.
				if err := unsuppliedProvider(fields.Value(), path, sel); err != nil {
					return err
				}
				if err := skippedPolicy(fields.Value(), append(append([]cue.Selector{}, path...), sel)); err != nil {
					return err
				}
				continue
			}
			next := append(append([]cue.Selector{}, path...), sel)
			if err := r.walk(fields.Value(), next, depth+1); err != nil {
				return err
			}
		}
	case cue.ListKind:
		list, err := v.List()
		if err != nil {
			return nil
		}
		for i := 0; list.Next(); i++ {
			next := append(append([]cue.Selector{}, path...), cue.Index(i))
			if err := r.walk(list.Value(), next, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// skippedPolicy fails the load for a policy the walk would not visit, one
// behind an optional or required label or deeper than the walk goes: a policy
// that is silently not in force would judge nothing.
func skippedPolicy(v cue.Value, path []cue.Selector) error {
	return skippedPolicyBelow(v, path, 0)
}

func skippedPolicyBelow(v cue.Value, path []cue.Selector, depth int) error {
	if v.Kind() != cue.StructKind || depth > 96 {
		return nil
	}
	if kind, _ := stringField(v, "$kind"); kind == "policy" {
		return fmt.Errorf("policy %s is not found: it sits behind an optional or required field, or deeper than 48 levels. Declare it at a regular path", cue.MakePath(path...))
	}
	fields, err := v.Fields(cue.Optional(true))
	if err != nil {
		return nil
	}
	for fields.Next() {
		next := append(append([]cue.Selector{}, path...), fields.Selector())
		if err := skippedPolicyBelow(fields.Value(), next, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// unsuppliedProvider reports a required field that holds a provider and that
// nothing filled in, such as the provider!: tls.#Provider of a module whose
// instance does not pass one. It names the instance and the field. Any other
// required field is not its concern: CUE reports it where a value needs it.
func unsuppliedProvider(v cue.Value, parent []cue.Selector, sel cue.Selector) error {
	if kind, _ := stringField(v, "$kind"); kind != "provider" || sel.LabelType() != cue.StringLabel {
		return nil
	}
	field := sel.Unquoted()
	at := cue.MakePath(append(append([]cue.Selector{}, parent...), cue.Str(field))...)
	what := "a provider"
	local := "provider"
	if text, ok := stringField(v, "$source"); ok {
		if src, err := registry.ParseSource(text); err == nil {
			what = "a " + src.Display() + " provider"
			local = strings.NewReplacer("-", "_", ".", "_").Replace(src.Type)
		}
	}
	if len(parent) == 0 {
		return fmt.Errorf("provider %s was not supplied: the specification requires %s in its field %s. Set the field to a provider struct", at, what, field)
	}
	inst := cue.MakePath(parent...)
	return fmt.Errorf("provider %s was not supplied: %s requires %s in its field %s. Pass one, for example\n\t%s: ... & {%s: $%s}", at, inst, what, field, inst, field, local)
}

func stringField(v cue.Value, name string) (string, bool) {
	f := v.LookupPath(cue.MakePath(cue.Str(name)))
	if !f.Exists() {
		return "", false
	}
	s, err := f.String()
	if err != nil || s == "" {
		return "", false
	}
	return s, true
}

// importField reads $import: an id as a string, or an identity as a struct. A
// value that is neither, an empty one, or one that is not concrete is an error.
func importField(v cue.Value, addr string) (*Import, error) {
	f := v.LookupPath(cue.MakePath(cue.Str("$import")))
	if !f.Exists() {
		return nil, nil
	}
	switch f.Kind() {
	case cue.StringKind:
		s, err := f.String()
		if err != nil || s == "" {
			return nil, nil
		}
		return &Import{Addr: addr, ID: s}, nil
	case cue.StructKind:
		if err := f.Validate(cue.Concrete(true)); err != nil {
			return nil, fmt.Errorf("%s: the identity in $import is not concrete: %w", addr, err)
		}
		var id map[string]any
		if err := f.Decode(&id); err != nil {
			return nil, fmt.Errorf("%s: the identity in $import: %w", addr, err)
		}
		if len(id) == 0 {
			return nil, fmt.Errorf("%s: the identity in $import has no attribute", addr)
		}
		return &Import{Addr: addr, Identity: id}, nil
	case cue.BottomKind:
		return nil, nil
	}
	return nil, fmt.Errorf("%s: $import is an id, a string, or an identity, a struct of the attributes of the identity of the resource", addr)
}

// register adds the struct at path as a resource, data source, ephemeral
// resource, function call or provider.
func (r *Root) register(v cue.Value, kind string, path []cue.Selector) error {
	p := cue.MakePath(path...)
	var mode string
	switch kind {
	case "resource":
		mode = "managed"
	case "data", "ephemeral", "call":
		mode = kind
	case "provider":
		mode = "provider"
	case "discovery":
		mode = "discovery"
	case "output":
		if nested := findNested(v, p, 0); nested != "" {
			return fmt.Errorf("%s is an output, so it cannot contain %s, which is a resource", p, nested)
		}
		if name := selfReference(v.LookupPath(cue.ParsePath("value"))); name != "" {
			return fmt.Errorf("output %s: the field %s of its value reads itself, not the %s outside it. Rename the field, or bind the name first with let", p, name, name)
		}
		out := &Output{Name: p.String(), Path: p}
		out.Sensitive, _ = v.LookupPath(cue.ParsePath("sensitive")).Bool()
		r.Outputs = append(r.Outputs, out)
		return nil
	case "policy":
		if nested := findNested(v, p, 0); nested != "" {
			return fmt.Errorf("%s is a policy, so it cannot contain %s, which is a resource", p, nested)
		}
		rule, err := ruleText(v.LookupPath(cue.ParsePath("rule")))
		if err != nil {
			return fmt.Errorf("policy %s: %w", p, err)
		}
		r.Policies = append(r.Policies, &Policy{Name: p.String(), Path: p, Rule: rule})
		return nil
	case "remote_outputs":
		if nested := findNested(v, p, 0); nested != "" {
			return fmt.Errorf("%s reads another root's outputs, so it cannot contain %s, which is a resource", p, nested)
		}
		st, dir, err := parseState(v.LookupPath(cue.ParsePath("state")), p.String()+".state")
		if err != nil {
			return err
		}
		if !st.Remote() {
			if dir == "" {
				return fmt.Errorf("%s.state needs a path (a state directory), an s3 bucket or an http url", p)
			}
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(r.Dir, dir)
			}
		}
		name := path[len(path)-1].String()
		if path[len(path)-1].Type() == cue.StringLabel {
			name = path[len(path)-1].Unquoted()
		}
		r.Resources = append(r.Resources, &Resource{Mode: "builtin", Type: kind, Name: name, Path: p, addr: p.String(), Builtin: true, Remote: &RemoteState{State: st, Dir: dir}})
		return nil
	case "env", "secret_env":
		if nested := findNested(v, p, 0); nested != "" {
			return fmt.Errorf("%s is an environment variable, so it cannot contain %s, which is a resource", p, nested)
		}
		name := path[len(path)-1].String()
		if path[len(path)-1].Type() == cue.StringLabel {
			name = path[len(path)-1].Unquoted()
		}
		mode := "builtin"
		if kind == "secret_env" {
			mode = "ephemeral" // the rules for ephemeral values keep it out of state
		}
		r.Resources = append(r.Resources, &Resource{Mode: mode, Type: kind, Name: name, Path: p, addr: p.String(), Builtin: true})
		return nil
	default:
		return fmt.Errorf("%s: $kind is %q; want resource, data, ephemeral, call, provider, discovery, output, policy, env, secret_env or remote_outputs", p, kind)
	}
	srcText, ok := stringField(v, "$source")
	if !ok {
		return fmt.Errorf("%s: carries $kind but no $source; use a definition from a generated provider package", p)
	}
	src, err := registry.ParseSource(srcText)
	if err != nil {
		return fmt.Errorf("%s: $source: %w", p, err)
	}
	if nested := findNested(v, p, 0); nested != "" {
		return fmt.Errorf("%s is a %s, so it cannot contain %s, which is one too", p, kind, nested)
	}
	if mode == "provider" {
		d := &ProviderDecl{Path: p, Source: src, value: v}
		d.Version, _ = stringField(v, "$version")
		d.FilePath, _ = stringField(v, "$path")
		d.Protocol, _ = stringField(v, "$protocol")
		d.Address, _ = stringField(v, "$address")
		d.From, _ = stringField(v, "$from")
		r.decls = append(r.decls, d)
		return nil
	}
	typ, ok := stringField(v, "$type")
	if !ok {
		return fmt.Errorf("%s: carries $kind but no $type; use a definition from a generated provider package", p)
	}
	if mode == "discovery" {
		d := newDiscovery(v, p, typ, src)
		acks, err := parseAcks(v, p.String())
		if err != nil {
			return err
		}
		d.Acks = acks
		r.Discoveries = append(r.Discoveries, d)
		// A discovery is not a node of the graph: it finds objects and manages none.
		// It still names the provider it lists through, which is found as for a
		// resource.
		res := &Resource{Mode: mode, Type: typ, Name: d.Name, Path: p, addr: p.String()}
		r.pending = append(r.pending, pendingResource{res: res, src: src, val: v, disc: d})
		return nil
	}
	if from, ok := stringField(v, "$from"); ok {
		r.moves = append(r.moves, pendingMove{to: p.String(), from: from})
	}
	imp, err := importField(v, p.String())
	if err != nil {
		return err
	}
	if imp != nil {
		if mode != "managed" {
			return fmt.Errorf("%s: $import applies to resources, not to %s", p, kind)
		}
		if r.Imports == nil {
			r.Imports = map[string]*Import{}
		}
		r.Imports[p.String()] = imp
	}
	name := path[len(path)-1].String()
	if path[len(path)-1].Type() == cue.StringLabel {
		name = path[len(path)-1].Unquoted()
	}
	res := &Resource{Mode: mode, Type: typ, Name: name, Path: p, addr: p.String()}
	if age := v.LookupPath(cue.MakePath(cue.Str("$lifecycle"), cue.Str("refresh"))); age.Exists() && age.IsConcrete() {
		res.RefreshPinned = true
	}
	r.Resources = append(r.Resources, res)
	r.pending = append(r.pending, pendingResource{res: res, src: src, val: v})
	return nil
}

// findNested returns the path of a resource inside the body of one, or "".
// Meta fields such as $depends_on may refer to other resources and are
// skipped.
func findNested(v cue.Value, at cue.Path, depth int) string {
	if depth > 24 {
		return ""
	}
	fields, err := v.Fields()
	if err != nil {
		return ""
	}
	for fields.Next() {
		sel := fields.Selector()
		if strings.HasPrefix(sel.String(), "$") {
			continue
		}
		next := cue.MakePath(append(at.Selectors(), sel)...)
		child := fields.Value()
		if child.Kind() != cue.StructKind {
			continue
		}
		if _, ok := stringField(child, "$kind"); ok {
			return next.String()
		}
		if n := findNested(child, next, depth+1); n != "" {
			return n
		}
	}
	return ""
}

// ruleText turns the rule of a policy, a CUE struct written in the spec itself,
// into the canonical source the engine stores, signs and judges. The rule is
// parsed code, not a string holding code: the text here is the formatted syntax
// tree of the struct, so the same rule always has the same text and digest.
func ruleText(v cue.Value) (string, error) {
	if !v.Exists() {
		return "", fmt.Errorf("rule is missing: write the rule as a CUE struct, such as rule: {summary: destroy: 0}")
	}
	if v.Kind() != cue.StructKind {
		return "", fmt.Errorf("rule must be a CUE struct, not a string of CUE: write rule: {summary: destroy: 0}")
	}
	// A rule that uses the standard library comes out as a file with its
	// imports; any other as a struct. The imports stay in the text, which is
	// standalone CUE.
	var decls []ast.Decl
	switch n := v.Syntax(cue.Raw(), cue.Docs(true)).(type) {
	case *ast.StructLit:
		decls = n.Elts
	case *ast.File:
		decls = n.Decls
	default:
		return "", fmt.Errorf("rule must be a CUE struct")
	}
	// The schema leaves the struct open so that any rule fits; that trailing
	// "..." is the schema's, not the author's.
	elts := decls[:0:0]
	for _, e := range decls {
		switch e.(type) {
		case *ast.Ellipsis, *ast.Package:
		default:
			elts = append(elts, e)
		}
	}
	empty := true
	for _, e := range elts {
		if _, imp := e.(*ast.ImportDecl); !imp {
			empty = false
		}
	}
	if empty {
		return "", fmt.Errorf("rule is empty: a policy with no rule judges nothing")
	}
	var text strings.Builder
	for _, e := range elts {
		b, err := format.Node(e)
		if err != nil {
			return "", fmt.Errorf("rule cannot be written out: %w", err)
		}
		text.Write(b)
		text.WriteByte('\n')
	}
	return text.String(), nil

}
