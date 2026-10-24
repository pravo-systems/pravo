package config

import (
	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
)

// selfReference returns the name of a field in the value of an output whose
// value reads that field and not the declaration of the same name outside, as
// in value: {pet: pet.id}. CUE resolves the name to the field, so the field
// refers to itself and never becomes concrete; the apply would find that out
// after it has made the resources, so the specification is refused at once.
func selfReference(v cue.Value) string {
	var found string
	for _, src := range conjunctSyntax(v) {
		if found == "" {
			found = selfIn(src)
		}
	}
	return found
}

// conjunctSyntax lists the syntax of every part of a value that is the unification
// of several.
func conjunctSyntax(v cue.Value) []ast.Node {
	if op, args := v.Expr(); op == cue.AndOp {
		var out []ast.Node
		for _, a := range args {
			out = append(out, conjunctSyntax(a)...)
		}
		return out
	}
	if n, ok := v.Source().(ast.Node); ok && n != nil {
		return []ast.Node{n}
	}
	return nil
}

func selfIn(src ast.Node) string {
	var found string
	ast.Walk(src, func(n ast.Node) bool {
		if found != "" {
			return false
		}
		f, ok := n.(*ast.Field)
		if !ok {
			return true
		}
		ast.Walk(f.Value, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok && id.Node == f.Value {
				found = id.Name
			}
			return found == ""
		}, nil)
		return found == ""
	}, nil)
	return found
}
