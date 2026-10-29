package config

import (
	"fmt"
	"strconv"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"

	"github.com/pravo-systems/pravo/internal/schema"
)

// PoliciesImport is the path of the package of starter rules, which pravo init writes.
const PoliciesImport = "pravo.dev/policies"

// packRule returns what the rule #name of the package of starter rules is made of: the
// declarations of its body, and the standard library imports they use. A rule is stored
// as standalone text, so a rule that names one of these has the body copied into it.
func packRule(name string) ([]ast.Decl, []*ast.ImportSpec, error) {
	file, err := parser.ParseFile("policies", schema.PoliciesPackage, parser.ParseComments)
	if err != nil {
		return nil, nil, err
	}
	for _, d := range file.Decls {
		f, ok := d.(*ast.Field)
		if !ok {
			continue
		}
		body, ok := f.Value.(*ast.StructLit)
		if id, isID := f.Label.(*ast.Ident); !ok || !isID || id.Name != name {
			continue
		}
		astutil.Apply(body, func(c astutil.Cursor) bool {
			ast.SetComments(c.Node(), nil)
			return true
		}, nil)
		used := map[string]bool{}
		ast.Walk(body, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok {
					used[id.Name] = true
				}
			}
			return true
		}, nil)
		var imports []*ast.ImportSpec
		for imp := range file.ImportSpecs() {
			path, _ := strconv.Unquote(imp.Path.Value)
			if used[lastElem(path)] {
				imports = append(imports, ast.NewImport(nil, path))
			}
		}
		return body.Elts, imports, nil
	}
	return nil, nil, fmt.Errorf("%s has no rule %s", PoliciesImport, name)
}

func lastElem(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// packAlias is the name a file gives pravo.dev/policies, or "".
func packAlias(decls []ast.Decl) string {
	for _, d := range decls {
		imp, ok := d.(*ast.ImportDecl)
		if !ok {
			continue
		}
		for _, sp := range imp.Specs {
			if p, err := strconv.Unquote(sp.Path.Value); err == nil && p == PoliciesImport {
				if sp.Name != nil {
					return sp.Name.Name
				}
				return "policies"
			}
		}
	}
	return ""
}

// withImports leaves the imports of decls as one declaration at the front, each package
// once, without pravo.dev/policies: its rules were copied into the text.
func withImports(decls []ast.Decl) []ast.Decl {
	var specs []*ast.ImportSpec
	seen := map[string]bool{}
	var rest []ast.Decl
	for _, d := range decls {
		imp, ok := d.(*ast.ImportDecl)
		if !ok {
			rest = append(rest, d)
			continue
		}
		for _, sp := range imp.Specs {
			p, err := strconv.Unquote(sp.Path.Value)
			key := p
			if sp.Name != nil {
				key = sp.Name.Name + " " + p
			}
			if err != nil || p == PoliciesImport || seen[key] {
				continue
			}
			seen[key] = true
			specs = append(specs, sp)
		}
	}
	if len(specs) == 0 {
		return rest
	}
	return append([]ast.Decl{&ast.ImportDecl{Specs: specs}}, rest...)
}

// PackRuleText is the standalone text of the rule #name with the parameters set by
// params, CUE fields such as "#keys: [\"owner\"]". It is the text that RuleText gives a
// policy whose rule names it.
func PackRuleText(name, params string) (string, error) {
	body, imports, err := packRule(name)
	if err != nil {
		return "", err
	}
	extra, err := parser.ParseFile("params", params)
	if err != nil {
		return "", err
	}
	decls := withImports(append(append([]ast.Decl{&ast.ImportDecl{Specs: imports}}, body...), extra.Decls...))
	var text strings.Builder
	for _, d := range decls {
		if _, ok := d.(*ast.Ellipsis); ok {
			continue
		}
		b, err := format.Node(d)
		if err != nil {
			return "", err
		}
		text.Write(b)
		text.WriteByte('\n')
	}
	return text.String(), nil
}
