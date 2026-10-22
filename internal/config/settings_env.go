package config

import (
	"fmt"
	"os"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/token"
)

// settingsWithEnv reads the $settings block again, on its own, with the
// environment variables that it reads filled in. A pravo.#Env is read when the
// run starts, which is after the specification is evaluated, so a setting that
// reads one (a bucket that a variable names) is not concrete when the block is
// read from the evaluated specification. first is the error of that read; it is
// returned when the block still does not read, followed by what is known of the
// variables.
func (r *Root) settingsWithEnv(first error, opts LoadOptions) error {
	s, notes, err := loadSettings(r.Dir, opts, false)
	if err != nil {
		return notes.after(first)
	}
	r.State, r.StatePath, r.Refresh = s.State, s.StatePath, s.Refresh
	return nil
}

// candidate is a top-level declaration that the block may refer to: a field, or
// a let clause that fields of the file refer to.
type candidate struct {
	decl    ast.Decl
	val     ast.Expr
	label   string
	imports bool // refers to an imported package, which cannot resolve yet
	// env is set for a pravo.#Env: val is then the struct without the
	// definition, which has no value yet.
	env bool
}

// envNotes says what is known of the environment variables of a block.
type envNotes struct {
	unset []string // variables that are not set and have no default
	stuck []string // a pravo.#Env whose name or default cannot be read without the packages
}

// after puts the notes after err.
func (n envNotes) after(err error) error {
	if err == nil {
		return nil
	}
	if len(n.unset) > 0 {
		err = fmt.Errorf("%w\n(pravo.#Env reads %s, which is not set and has no default)", err, strings.Join(n.unset, ", "))
	}
	if len(n.stuck) > 0 {
		err = fmt.Errorf("%w\n(%s cannot be read here: the settings read the name and the default of a pravo.#Env as strings, with the value of another pravo.#Env interpolated)", err, strings.Join(n.stuck, ", "))
	}
	return err
}

// envOperand reports whether the field is a pravo.#Env, written as
// name: pravo.#Env & {...}, and returns its value without the definition. alias
// is the name that the file gives to the pravo package.
func envOperand(fld *ast.Field, alias string) (ast.Expr, bool) {
	if alias == "" {
		return nil, false
	}
	env := false
	var structs []ast.Expr
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
			structs = append(structs, x)
			return true
		}
		return false
	}
	if !collect(fld.Value) || !env || len(structs) == 0 {
		return nil, false
	}
	plain := structs[0]
	for _, e := range structs[1:] {
		plain = ast.NewBinExpr(token.AND, plain, e)
	}
	return plain, true
}

// labelPath is the path of a field of the declaration with the label.
func labelPath(label, field string) cue.Path {
	if strings.HasPrefix(label, "_") {
		return cue.MakePath(cue.Hid(label, "_"), cue.Str(field))
	}
	return cue.MakePath(cue.Str(label), cue.Str(field))
}

// readableDecls lists the declarations that the block can be read with, and
// settles the value of every pravo.#Env among them. An environment variable is
// read as the run reads it: from the variable, else from the default. The name
// and the default of one may be strings or interpolate the value of another, so
// the values are found in rounds, each reading the declarations that do not
// depend on a value that is not known yet.
func readableDecls(ctx *cue.Context, cs candidates) ([]ast.Decl, envNotes) {
	var notes envNotes
	values := map[string]string{} // label of a pravo.#Env -> its value
	names := map[string]string{}  // label of a pravo.#Env -> its variable
	dead := map[string]bool{}     // a pravo.#Env that has no value
	for round := 0; round <= len(cs); round++ {
		decls, pending := cs.declsOf(values, names, dead, true)
		if len(pending) == 0 {
			break
		}
		fv, err := compileDecls(ctx, decls)
		if err != nil {
			break
		}
		progress := false
		for _, i := range pending {
			label := cs[i].label
			nm, err := fv.LookupPath(labelPath(label, "name")).String()
			if err != nil {
				continue // the name depends on a value that is not known yet
			}
			def, hasDef := "", false
			if d := fv.LookupPath(labelPath(label, "default")); d.Exists() {
				if def, err = d.String(); err != nil {
					continue
				}
				hasDef = true
			}
			progress = true
			val, ok := os.LookupEnv(nm)
			switch {
			case ok:
			case hasDef:
				val = def
			default:
				dead[label] = true
				notes.unset = append(notes.unset, nm)
				continue
			}
			values[label], names[label] = val, nm
		}
		if !progress {
			break
		}
	}
	decls, _ := cs.declsOf(values, names, dead, false)
	for _, c := range cs {
		if _, known := values[c.label]; c.env && !known && !dead[c.label] {
			notes.stuck = append(notes.stuck, "pravo.#Env "+c.label)
		}
	}
	return decls, notes
}

// candidates is the list of declarations that a block may refer to.
type candidates []candidate

// declsOf lists the declarations that can be read now: the ones that do not
// refer to a package, to a pravo.#Env that has no value yet or to one that has
// none. A pravo.#Env whose variable is read becomes a struct with its name and
// its value. With withPending, a pravo.#Env that is not read yet is listed too,
// as its struct, so that its name and default can be read, and its index is
// returned.
func (cs candidates) declsOf(values, names map[string]string, dead map[string]bool, withPending bool) ([]ast.Decl, []int) {
	known := func(c candidate) bool { _, ok := values[c.label]; return ok }
	blocked := map[string]bool{}
	for _, c := range cs {
		if c.imports || dead[c.label] || (c.env && !known(c)) {
			blocked[c.label] = true
		}
	}
	// What refers to a name that is blocked is left out, and so is what refers
	// to that. A pravo.#Env that is not read yet may refer to a name that is
	// blocked only when it is its own.
	skip := map[int]bool{}
	for changed := true; changed; {
		changed = false
		for i, c := range cs {
			if skip[i] || c.imports || dead[c.label] || (c.env && known(c)) {
				continue
			}
			check := blocked
			if c.env {
				check = map[string]bool{}
				for l := range blocked {
					if l != c.label {
						check[l] = true
					}
				}
			}
			if mentionsAny(c.val, check) {
				skip[i] = true
				blocked[c.label] = true
				changed = true
			}
		}
	}
	var decls []ast.Decl
	var pending []int
	for i, c := range cs {
		if c.imports || dead[c.label] {
			continue
		}
		fld, isField := c.decl.(*ast.Field)
		switch {
		case c.env && isField && known(c):
			cp := *fld
			cp.Value = ast.NewStruct("name", ast.NewString(names[c.label]), "value", ast.NewString(values[c.label]))
			decls = append(decls, &cp)
		case c.env && isField:
			if skip[i] || !withPending {
				continue
			}
			cp := *fld
			cp.Value = c.val
			decls = append(decls, &cp)
			pending = append(pending, i)
		case !skip[i]:
			decls = append(decls, c.decl)
		}
	}
	return decls, pending
}

// compileDecls compiles the declarations as one file. They came from different
// files, so the references between them are resolved afresh, from text.
func compileDecls(ctx *cue.Context, decls []ast.Decl) (cue.Value, error) {
	if len(decls) == 0 {
		return ctx.CompileString("{}"), nil
	}
	src, err := format.Node(&ast.File{Decls: decls})
	if err != nil {
		return cue.Value{}, err
	}
	fv := ctx.CompileBytes(src)
	return fv, fv.Err()
}
