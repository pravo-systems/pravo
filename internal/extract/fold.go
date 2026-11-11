// Package extract folds the resources of a specification that are alike into one definition
// and small instances. It finds the definition by anti-unification (the least general
// generalisation of the bodies: what is equal stays, what differs becomes a parameter) and
// decides by description length: a definition is written only where it makes the specification
// shorter. It works on syntax, reads no schema and needs no provider, and keeps every address.
// Whether the result means what the original did is for the caller to prove, with a plan.
package extract

import "sort"

// File is a file of the specification.
type File struct {
	Name string
	Src  []byte
}

// Definition says what one definition of a fold took.
type Definition struct {
	Name       string
	Type       string
	Parameters int
	// Labels are the resources that became instances, in the order of the specification.
	Labels        []string
	Before, After int
}

// Folded is the outcome of Fold.
type Folded struct {
	// Files holds the new text of each file that changed, by name.
	Files       map[string][]byte
	Definitions []Definition
	// Instances is the number of resources that became instances.
	Instances int
	// Left counts the resources that were looked at and stay as they are.
	Left int
}

// Fold folds the repeated resources of the files. Only a resource written at the top of a file as
// LABEL: $PROVIDER.#Resource.TYPE & {...} is looked at.
func Fold(files []File, opt *Options) (*Folded, error) {
	if opt == nil {
		opt = DefaultOptions()
	}
	in := make([]Input, len(files))
	for i, f := range files {
		in[i] = Input{Path: f.Name, Name: f.Name, Src: f.Src}
	}
	sp, err := parseSpec("", in)
	if err != nil {
		return nil, err
	}
	res := decide(sp, opt)
	out, err := rewrite(res)
	if err != nil {
		return nil, err
	}
	f := &Folded{Files: out, Left: len(res.Left)}
	for _, x := range res.Defs {
		d := Definition{Name: x.Name, Type: x.Type, Parameters: len(x.Params), Before: x.Before, After: x.After}
		for _, m := range x.Members {
			d.Labels = append(d.Labels, m.Label)
		}
		f.Instances += len(x.Members)
		f.Definitions = append(f.Definitions, d)
	}
	sort.Slice(f.Definitions, func(i, j int) bool { return f.Definitions[i].Name < f.Definitions[j].Name })
	return f, nil
}
