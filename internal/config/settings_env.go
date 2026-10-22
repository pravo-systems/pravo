package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/token"
)

// settingsWithEnv reads the $settings block again, on its own, with the
// environment variables that it reads filled in. A pravo.#Env is read when the
// run starts, which is after the specification is evaluated, so a setting that
// reads one (a bucket that a variable names) is not concrete when the block is
// read from the evaluated specification. first is the error of that read; it is
// returned when the block still does not read, followed by the variables that
// have no value.
func (r *Root) settingsWithEnv(first error, opts LoadOptions) error {
	s, unset, err := loadSettings(r.Dir, opts, false)
	if err != nil {
		return withUnset(first, unset)
	}
	r.State, r.StatePath, r.Refresh = s.State, s.StatePath, s.Refresh
	return nil
}

// withUnset names the environment variables that a block reads and that have no
// value, after the error that the block ended in.
func withUnset(err error, unset []string) error {
	if err == nil || len(unset) == 0 {
		return err
	}
	return fmt.Errorf("%w\n(pravo.#Env reads %s, which is not set and has no default)", err, strings.Join(unset, ", "))
}

// envValue replaces a field of the form name: pravo.#Env & {name: "VAR", default: "x"}
// with a struct that holds the value, read from the variable or taken from the
// default, so that the block can be read without the package. alias is the name
// that the file gives to the pravo package. The field is returned as it is when
// it is not of that form, and when the name or the default is not a string
// written out; unset names the variable when it has no value.
func envValue(fld *ast.Field, alias string) (out *ast.Field, unset string) {
	if alias == "" {
		return fld, ""
	}
	var env bool
	var lits []*ast.StructLit
	var collect func(e ast.Expr) bool
	collect = func(e ast.Expr) bool {
		switch x := e.(type) {
		case *ast.BinaryExpr:
			return x.Op == token.AND && collect(x.X) && collect(x.Y)
		case *ast.ParenExpr:
			return collect(x.X)
		case *ast.SelectorExpr:
			id, ok := x.X.(*ast.Ident)
			sel, isIdent := x.Sel.(*ast.Ident)
			if !ok || !isIdent || id.Name != alias || sel.Name != "#Env" {
				return false
			}
			env = true
			return true
		case *ast.StructLit:
			lits = append(lits, x)
			return true
		}
		return false
	}
	if !collect(fld.Value) || !env {
		return fld, ""
	}
	var name, def string
	var hasDef bool
	for _, lit := range lits {
		for _, el := range lit.Elts {
			f, ok := el.(*ast.Field)
			if !ok {
				return fld, ""
			}
			label, _, err := ast.LabelName(f.Label)
			if err != nil || (label != "name" && label != "default") {
				continue
			}
			bl, ok := f.Value.(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				return fld, ""
			}
			s, err := strconv.Unquote(bl.Value)
			if err != nil {
				return fld, ""
			}
			if label == "name" {
				name = s
			} else {
				def, hasDef = s, true
			}
		}
	}
	if name == "" {
		return fld, ""
	}
	val, ok := os.LookupEnv(name)
	if !ok {
		if !hasDef {
			return fld, name
		}
		val = def
	}
	cp := *fld
	cp.Value = ast.NewStruct("name", ast.NewString(name), "value", ast.NewString(val))
	return &cp, ""
}
