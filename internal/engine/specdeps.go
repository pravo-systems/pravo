package engine

import (
	"slices"
	"sort"

	"cuelang.org/go/cue"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
)

// DepsResolver is a convert.Resolver that also takes the loops that the derivation
// of the dependencies reads, so that the reference analysis sees them as it walks.
type DepsResolver interface {
	convert.Resolver
	SetLoops(*convert.Loops)
}

// SpecInput is what the dependencies of a specification are worked out from. The
// engine and the viewer hold the specification in different forms and each passes
// its own; the derivation is one.
type SpecInput struct {
	Root *config.Root
	// Value is the whole specification, which the loops are read from.
	Value cue.Value
	// At reads the value at a path of the specification.
	At func(cue.Path) cue.Value
	// Resolver tells the analysis where the tasks are.
	Resolver DepsResolver
	// ByPath maps the path of each resource to it.
	ByPath map[string]*config.Resource
	// FromSyntax reads the syntax alone, as DependenciesFromSyntax does.
	FromSyntax bool
}

// SpecDeps are the dependencies of the tasks of a specification.
type SpecDeps struct {
	// Body maps the address of each resource to the addresses of the resources its
	// body reads, sorted.
	Body map[string][]string
	// Provider maps each provider key to the addresses of the tasks its
	// configuration reads: a resource of the provider runs after them. A provider
	// that the specification declares and no resource uses is in it too.
	Provider map[string][]string
	// Loops is the evaluator's reading of the loops, nil when it was not used.
	Loops *convert.Loops
	// LoopsErr is why the evaluator was not used, when it was asked for and failed.
	LoopsErr error
}

// DeriveDeps works out what each task reads. The references in the body of a
// resource come from the syntax and, unless in.FromSyntax is set, from CUE's
// evaluator, which knows the element that a loop over a collection of resources
// reads (convert.Loops); a resource that a provider's configuration reads is
// a dependency of the resources that use the provider.
func DeriveDeps(in SpecInput) *SpecDeps {
	d := &SpecDeps{Body: make(map[string][]string, len(in.Root.Resources))}
	in.Resolver.SetLoops(nil)
	if !in.FromSyntax {
		if l, err := convert.NewLoops(in.Value, in.Resolver); err != nil {
			d.LoopsErr = err
		} else {
			d.Loops = l
			in.Resolver.SetLoops(l)
		}
	}
	for _, r := range in.Root.Resources {
		v := in.At(r.Path)
		var deps []cue.Path
		if d.Loops != nil {
			deps, _ = d.Loops.Dependencies(v, r.Path, in.Resolver)
		} else {
			deps, _ = convert.Dependencies(v, r.Path, in.Resolver)
		}
		set := map[string]bool{}
		for _, p := range deps {
			if dr, ok := in.ByPath[p.String()]; ok && dr != r {
				set[dr.Address()] = true
			}
		}
		own := make([]string, 0, len(set))
		for a := range set {
			own = append(own, a)
		}
		sort.Strings(own)
		d.Body[r.Address()] = own
	}
	d.Provider = ProviderDeps(in.Root, in.At, in.Resolver, in.ByPath)
	return d
}

// ProviderDeps returns, per provider key, the tasks its configuration references,
// so that resources using the provider run after them.
func ProviderDeps(root *config.Root, at func(cue.Path) cue.Value, res convert.Resolver, byPath map[string]*config.Resource) map[string][]string {
	out := map[string][]string{}
	// A provider the specification declares but no resource uses is in the list
	// too: the record of a removed resource may still be read through it.
	keys := slices.Clone(root.ProviderKeys)
	for _, d := range root.ProviderDecls {
		keys = append(keys, d.Key)
	}
	slices.Sort(keys)
	for _, key := range slices.Compact(keys) {
		p, ok := root.ProviderPath(key)
		if !ok {
			continue
		}
		v := at(p)
		if !v.Exists() {
			continue
		}
		deps, _ := convert.Dependencies(v, p, res)
		for _, d := range deps {
			if dr, ok := byPath[d.String()]; ok {
				out[key] = append(out[key], dr.Address())
			}
		}
	}
	return out
}

// Edges returns, per resource, everything it waits for: what its body reads and
// what its provider is set up from, other than itself, sorted. These are the edges
// of the graph that a walk runs by and the viewer draws.
func (d *SpecDeps) Edges(root *config.Root) map[string][]string {
	out := make(map[string][]string, len(root.Resources))
	for _, r := range root.Resources {
		addr := r.Address()
		set := map[string]bool{}
		for _, a := range d.Body[addr] {
			set[a] = true
		}
		for _, a := range d.Provider[r.ProviderKey] {
			if a != addr {
				set[a] = true
			}
		}
		list := make([]string, 0, len(set))
		for a := range set {
			list = append(list, a)
		}
		sort.Strings(list)
		out[addr] = list
	}
	return out
}

// HasCycle reports whether the edges of resources have a cycle: nothing on it could
// run first.
func HasCycle(edges map[string][]string) bool {
	waiting := make(map[string]int, len(edges))
	dependents := map[string][]string{}
	var ready []string
	for addr, deps := range edges {
		waiting[addr] = len(deps)
		if len(deps) == 0 {
			ready = append(ready, addr)
		}
		for _, d := range deps {
			dependents[d] = append(dependents[d], addr)
		}
	}
	for len(ready) > 0 {
		addr := ready[len(ready)-1]
		ready = ready[:len(ready)-1]
		delete(waiting, addr)
		for _, a := range dependents[addr] {
			if _, open := waiting[a]; open {
				if waiting[a]--; waiting[a] == 0 {
					ready = append(ready, a)
				}
			}
		}
	}
	return len(waiting) > 0
}

// DeriveEdges is DeriveDeps with the fallback the engine's graph has: if the edges
// that the evaluator finds make a cycle, the syntax alone is tried, since an edge
// that only the evaluator finds must not stop a specification that worked without
// it. The viewer draws the graph that the apply of the same specification runs by.
func DeriveEdges(in SpecInput) (*SpecDeps, map[string][]string) {
	d := DeriveDeps(in)
	edges := d.Edges(in.Root)
	if d.Loops != nil && HasCycle(edges) {
		in.FromSyntax = true
		d = DeriveDeps(in)
		edges = d.Edges(in.Root)
	}
	return d, edges
}

// OutputReads returns, per output, what its value reads: the addresses of resources,
// and with outputs set, the names of other outputs too. With the outputs known to the
// resolver as tasks an output that reads another is joined to it, and not to what it
// reads; the engine, which only needs the resources, leaves them out. Sorted, once each.
func OutputReads(root *config.Root, at func(cue.Path) cue.Value, res convert.Resolver, byPath map[string]*config.Resource, outputs map[string]*config.Output) map[string][]string {
	reads := make(map[string][]string, len(root.Outputs))
	for _, o := range root.Outputs {
		p := o.ValuePath()
		deps, _ := convert.Dependencies(at(p), p, res)
		var addrs []string
		for _, d := range deps {
			if dr, ok := byPath[d.String()]; ok {
				addrs = append(addrs, dr.Address())
			} else if do, ok := outputs[d.String()]; ok && do != o {
				addrs = append(addrs, do.Name)
			}
		}
		sort.Strings(addrs)
		reads[o.Name] = slices.Compact(addrs)
	}
	return reads
}
