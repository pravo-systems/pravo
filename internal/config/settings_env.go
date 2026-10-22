package config

import (
	"fmt"
	"os"
	"strings"

	"cuelang.org/go/cue"
)

// settingsWithEnv reads the $settings block again with the environment
// variables of the specification filled in. A pravo.#Env is read when the run
// starts, which is after the specification is evaluated, so a setting that
// reads one (a bucket that a variable names) is not concrete until then. first
// is the error of the block as it stands; it is returned when no variable can
// be filled in, and it is followed by the variables that have no value.
func (r *Root) settingsWithEnv(first error) error {
	v := r.Value
	var unset []string
	filled := false
	eachEnv(r.Value, func(path []cue.Selector, env cue.Value) {
		name, err := env.LookupPath(cue.ParsePath("name")).String()
		if err != nil {
			return // the name depends on something that is not known
		}
		val, ok := os.LookupEnv(name)
		if !ok {
			def, err := env.LookupPath(cue.ParsePath("default")).String()
			if err != nil {
				unset = append(unset, name)
				return
			}
			val = def
		}
		v = v.FillPath(cue.MakePath(append(append([]cue.Selector{}, path...), cue.Str("value"))...), val)
		filled = true
	})
	err := first
	if filled {
		err = nil
		if settings := v.LookupPath(cue.MakePath(cue.Str("$settings"))); settings.Exists() {
			err = r.extractSettings(settings)
		}
	}
	if err != nil && len(unset) > 0 {
		err = fmt.Errorf("%w\n(pravo.#Env reads %s, which is not set and has no default)", err, strings.Join(unset, ", "))
	}
	return err
}

// eachEnv calls fn for every pravo.#Env of the specification, with the path it
// sits at. A pravo.#SecretEnv is left out: a secret is not read into a setting.
func eachEnv(v cue.Value, fn func(path []cue.Selector, env cue.Value)) {
	var walk func(v cue.Value, path []cue.Selector, depth int)
	walk = func(v cue.Value, path []cue.Selector, depth int) {
		if depth > 48 {
			return
		}
		switch v.Kind() {
		case cue.StructKind:
			if kind, ok := stringField(v, "$kind"); ok {
				if kind == "env" {
					fn(path, v)
				}
				return
			}
			fields, err := v.Fields()
			if err != nil {
				return
			}
			for fields.Next() {
				sel := fields.Selector()
				if depth == 0 && sel.Unquoted() == "$settings" {
					continue
				}
				walk(fields.Value(), append(append([]cue.Selector{}, path...), sel), depth+1)
			}
		case cue.ListKind:
			list, err := v.List()
			if err != nil {
				return
			}
			for i := 0; list.Next(); i++ {
				walk(list.Value(), append(append([]cue.Selector{}, path...), cue.Index(i)), depth+1)
			}
		}
	}
	walk(v, nil, 0)
}
