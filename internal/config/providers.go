package config

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/parser"

	"pravo.dev/pravo/internal/registry"
)

// ProviderDecl is a provider declared in the specification: a struct built
// from a generated #Provider definition. Its fields other than the $ meta
// fields are the provider's configuration.
type ProviderDecl struct {
	// Path is where the struct sits.
	Path cue.Path
	// Key identifies the configured provider instance: the provider's local
	// name when it is the only one of its source, otherwise <local>.<path>.
	Key    string
	Local  string
	Source registry.Source
	// Requirement fields from the $ meta fields.
	Version, FilePath, Protocol, Address string
	// From is the $from text, the address the provider had before.
	From string

	value cue.Value
	// alias is set when the struct is built from another provider struct (a
	// provider passed into a module); it then shares that provider's instance.
	alias *ProviderDecl
}

type pendingResource struct {
	res *Resource
	src registry.Source
	val cue.Value // the resource struct, to see how its definition was reached
}

// ProviderPath returns where the configuration of the provider instance key
// sits in the specification. It reports false for an implied provider, which
// has no configuration.
func (r *Root) ProviderPath(key string) (cue.Path, bool) {
	for _, d := range r.ProviderDecls {
		if d.Key == key {
			return d.Path, true
		}
	}
	return cue.Path{}, false
}

// resolveProviders works out, once every struct has been found, which provider
// requirements the specification has, which provider instance each provider
// struct and each resource belongs to.
func (r *Root) resolveProviders() error {
	byPath := map[string]*ProviderDecl{}
	for _, d := range r.decls {
		byPath[d.Path.String()] = d
	}
	// A provider struct built from another one shares its instance.
	for _, d := range r.decls {
		for _, p := range referencePaths(d.value) {
			if t, ok := byPath[p.String()]; ok && t != d {
				d.alias = t
				break
			}
		}
	}
	canon := func(d *ProviderDecl) *ProviderDecl {
		for i := 0; d.alias != nil && i < 16; i++ {
			d = d.alias
		}
		return d
	}
	var own []*ProviderDecl
	for _, d := range r.decls {
		if canon(d) == d {
			own = append(own, d)
			continue
		}
		// A passed-in provider is the caller's struct under another name.
		// Configuration written on the other name would never reach the
		// provider, so it is an error rather than a silent difference.
		t := canon(d)
		if p, ok := extraConfig(d.value, t.value, nil, 0); ok {
			return fmt.Errorf("%s sets %s, but %s is another name for the provider %s, which does not set it. The provider is configured from %s alone: set %s there", d.Path, p, d.Path, t.Path, t.Path, p)
		}
	}

	// Requirements, one per source.
	var sources []registry.Source
	seen := map[registry.Source]bool{}
	add := func(s registry.Source) {
		if !seen[s] {
			seen[s] = true
			sources = append(sources, s)
		}
	}
	for _, d := range own {
		add(d.Source)
	}
	for _, pr := range r.pending {
		add(pr.src)
	}
	locals := localNames(sources)
	r.Providers = map[string]*ProviderReq{}
	for _, src := range sources {
		local := locals[src]
		req := &ProviderReq{LocalName: local, Source: src, Protocol: ProtocolTfplugin, Implied: true}
		for _, d := range own {
			if d.Source != src {
				continue
			}
			req.Implied = false
			if d.Version != "" {
				if req.Version != "" && req.Version != d.Version {
					return fmt.Errorf("%s: $version %q conflicts with %q on another %s provider; one version of a provider is used", d.Path, d.Version, req.Version, src.Display())
				}
				req.Version = d.Version
			}
			if d.FilePath != "" {
				req.Path = d.FilePath
				if !filepath.IsAbs(req.Path) {
					req.Path = filepath.Join(r.Dir, req.Path)
				}
			}
			if d.Protocol != "" {
				req.Protocol = d.Protocol
			}
			if d.Address != "" {
				req.Address = d.Address
			}
			if d.From != "" {
				old, err := registry.ParseSource(d.From)
				if err != nil {
					return fmt.Errorf("%s: $from: %w", d.Path, err)
				}
				if old == src {
					return fmt.Errorf("%s: $from is the address of the provider itself", d.Path)
				}
				req.Was = append(req.Was, old)
			}
		}
		if err := validateProtocol(local, req); err != nil {
			return err
		}
		r.Providers[local] = req
	}

	// Instance keys.
	perSource := map[registry.Source][]*ProviderDecl{}
	for _, d := range own {
		perSource[d.Source] = append(perSource[d.Source], d)
	}
	for _, d := range own {
		d.Local = locals[d.Source]
		if len(perSource[d.Source]) == 1 {
			d.Key = d.Local
		} else {
			d.Key = d.Local + "." + d.Path.String()
		}
	}
	for _, d := range r.decls {
		if t := canon(d); t != d {
			d.Local, d.Key = t.Local, t.Key
		}
	}
	r.ProviderDecls = own

	// Which instance each resource uses.
	used := map[string]bool{}
	for _, pr := range r.pending {
		key, err := r.instanceFor(pr, byPath, canon)
		if err != nil {
			return err
		}
		pr.res.ProviderKey = key
		used[key] = true
	}
	r.pending = nil
	for k := range used {
		r.ProviderKeys = append(r.ProviderKeys, k)
	}
	sort.Strings(r.ProviderKeys)
	return nil
}

func (r *Root) instanceFor(pr pendingResource, byPath map[string]*ProviderDecl, canon func(*ProviderDecl) *ProviderDecl) (string, error) {
	at := pr.res.Path
	d := r.providerOf(pr.val, byPath, canon, 0, map[string]bool{})
	if d == nil {
		d = r.providerBelowInstance(pr.val, at, byPath, canon)
	}
	if d == nil {
		return "", fmt.Errorf("%s was not built through a provider. Declare a provider, for example\n\t$aws: awsp.#Provider & {...}\nand build the resource through it: $aws.#Resource.%s & {...}", at, pr.res.Type)
	}
	if d.Source != pr.src {
		return "", fmt.Errorf("%s: built through %s, a provider of %s, but this is a resource of %s", at, d.Path, d.Source.Display(), pr.src.Display())
	}
	return d.Key, nil
}

// providerOf finds the provider a resource was built through: the struct whose
// #Resource, #Data, #Ephemeral or #Function the resource's definition was
// selected from. It follows the resource's own unification and plain fields it
// refers to, so x: eu.#Resource.aws_s3_bucket and then b: x & {...} resolves
// to eu.
func (r *Root) providerOf(v cue.Value, byPath map[string]*ProviderDecl, canon func(*ProviderDecl) *ProviderDecl, depth int, seen map[string]bool) *ProviderDecl {
	for _, p := range referencePaths(v) {
		sels := p.Selectors()
		for i := 1; i < len(sels); i++ {
			if !sels[i].IsDefinition() {
				continue
			}
			if d, ok := byPath[cue.MakePath(sels[:i]...).String()]; ok {
				return canon(d)
			}
			break
		}
		key := p.String()
		if depth >= 8 || seen[key] {
			continue
		}
		seen[key] = true
		if t := r.Value.LookupPath(p); t.Exists() {
			if d := r.providerOf(t, byPath, canon, depth+1, seen); d != nil {
				return d
			}
		}
	}
	return nil
}

// extraConfig finds, at any depth, a field that alias sets and canon leaves
// unset. A provider struct built from another one shares its instance, and
// the instance is configured from canon alone.
func extraConfig(alias, canon cue.Value, at []cue.Selector, depth int) (cue.Path, bool) {
	if depth > 16 {
		return cue.Path{}, false
	}
	sub := func(sel cue.Selector) []cue.Selector { return append(append([]cue.Selector{}, at...), sel) }
	switch alias.Kind() {
	case cue.StructKind:
		fields, err := alias.Fields()
		if err != nil {
			return cue.Path{}, false
		}
		for fields.Next() {
			sel := fields.Selector()
			other := canon.LookupPath(cue.MakePath(sel))
			if !other.Exists() {
				return cue.MakePath(sub(sel)...), true
			}
			if p, ok := extraConfig(fields.Value(), other, sub(sel), depth+1); ok {
				return p, true
			}
		}
	case cue.ListKind:
		mine, err := alias.List()
		if err != nil {
			return cue.Path{}, false
		}
		theirs, err := canon.List()
		for i := 0; mine.Next(); i++ {
			if err != nil || !theirs.Next() {
				return cue.MakePath(sub(cue.Index(i))...), true
			}
			if p, ok := extraConfig(mine.Value(), theirs.Value(), sub(cue.Index(i)), depth+1); ok {
				return p, true
			}
		}
	}
	return cue.Path{}, false
}

// providerBelowInstance finds the provider of a resource inside an instance
// that is a bare reference to its definition, as in s: #S. CUE then names the
// provider relative to the definition (#S.provider) and not to the instance,
// so the provider that #S declares is looked for below the instance instead,
// at s.provider.
func (r *Root) providerBelowInstance(v cue.Value, at cue.Path, byPath map[string]*ProviderDecl, canon func(*ProviderDecl) *ProviderDecl) *ProviderDecl {
	own := at.Selectors()
	for _, p := range referencePaths(v) {
		sels := p.Selectors()
		if len(sels) < 2 || !sels[0].IsDefinition() {
			continue
		}
		end := len(sels)
		for i := 1; i < len(sels); i++ {
			if sels[i].IsDefinition() {
				end = i
				break
			}
		}
		if end == 1 {
			continue
		}
		for n := len(own) - 1; n >= 1; n-- {
			cand := cue.MakePath(append(append([]cue.Selector{}, own[:n]...), sels[1:end]...)...)
			if d, ok := byPath[cand.String()]; ok {
				return canon(d)
			}
		}
	}
	return nil
}

// referencePaths lists the paths the value's expression refers to, at the
// level of its unification: for awsp.#Provider & eu, the paths of both
// operands.
func referencePaths(v cue.Value) []cue.Path {
	var out []cue.Path
	var walk func(v cue.Value, depth int)
	walk = func(v cue.Value, depth int) {
		if depth > 8 {
			return
		}
		if root, p := v.ReferencePath(); root.Exists() && len(p.Selectors()) > 0 {
			out = append(out, p)
		}
		_, args := v.Expr()
		for _, a := range args {
			walk(a, depth+1)
		}
	}
	walk(v, 0)
	return out
}

// localNames gives each source a short name: its type, made unique when two
// sources share one. The names key the lock, the schemas and recordings.
func localNames(sources []registry.Source) map[registry.Source]string {
	out := map[registry.Source]string{}
	taken := map[string]bool{}
	sorted := append([]registry.Source(nil), sources...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].String() < sorted[j].String() })
	for _, s := range sorted {
		for _, name := range []string{s.Type, strings.ReplaceAll(s.Namespace, "/", "_") + "_" + s.Type, strings.NewReplacer(".", "_", "-", "_", ":", "_").Replace(s.Host) + "_" + strings.ReplaceAll(s.Namespace, "/", "_") + "_" + s.Type} {
			if !taken[name] {
				taken[name] = true
				out[s] = name
				break
			}
		}
	}
	return out
}

// ScanProviders reads the CUE files under dir without evaluating them and
// returns the provider requirements they imply: every provider whose
// generated package they import, with the literal $version, $path, $protocol
// and $address of any struct built from its #Provider. init uses it to
// install providers and generate packages before the specification can load.
func ScanProviders(dir string) (map[string]*ProviderReq, error) {
	byLocal := map[string]*ProviderReq{}
	bySource := map[registry.Source]*ProviderReq{}
	req := func(src registry.Source) *ProviderReq {
		if r, ok := bySource[src]; ok {
			return r
		}
		r := &ProviderReq{Source: src, Protocol: ProtocolTfplugin, Implied: true}
		bySource[src] = r
		return r
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "cue.mod" || strings.HasPrefix(d.Name(), ".") && p != dir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".cue") || strings.HasSuffix(p, "_test.cue") {
			return nil
		}
		f, err := parser.ParseFile(p, nil)
		if err != nil {
			return nil // reported when the root loads
		}
		aliases := map[string]registry.Source{}
		for imp := range f.ImportSpecs() {
			ip, err := strconv.Unquote(imp.Path.Value)
			if err != nil || !strings.HasPrefix(ip, ProviderPackagePrefix) {
				continue
			}
			if i := strings.IndexByte(ip, ':'); i >= 0 {
				ip = ip[:i]
			}
			src, ok := sourceOfPackage(strings.TrimPrefix(ip, ProviderPackagePrefix))
			if !ok {
				continue
			}
			req(src)
			name := path.Base(ip)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			aliases[name] = src
		}
		if len(aliases) == 0 {
			return nil
		}
		ast.Walk(f, func(n ast.Node) bool {
			b, ok := n.(*ast.BinaryExpr)
			if !ok || b.Op.String() != "&" {
				return true
			}
			src, lit := providerOperand(b, aliases)
			if lit == nil {
				return true
			}
			r := req(src)
			r.Implied = false
			for _, el := range lit.Elts {
				fld, ok := el.(*ast.Field)
				if !ok {
					continue
				}
				name, _, _ := ast.LabelName(fld.Label)
				bl, ok := fld.Value.(*ast.BasicLit)
				if !ok {
					continue
				}
				val, err := strconv.Unquote(bl.Value)
				if err != nil {
					continue
				}
				switch name {
				case "$version":
					r.Version = val
				case "$path":
					r.Path = val
				case "$protocol":
					r.Protocol = val
				case "$address":
					r.Address = val
				}
			}
			return true
		}, nil)
		return nil
	})
	if err != nil {
		return nil, err
	}
	locals := localNames(sourcesOf(bySource))
	for src, r := range bySource {
		r.LocalName = locals[src]
		if err := validateProtocol(r.LocalName, r); err != nil {
			return nil, err
		}
		byLocal[r.LocalName] = r
	}
	return byLocal, nil
}

func sourcesOf(m map[registry.Source]*ProviderReq) []registry.Source {
	var out []registry.Source
	for s := range m {
		out = append(out, s)
	}
	return out
}

// providerOperand recognises alias.#Provider & {...} and returns the
// provider's source and the struct literal.
func providerOperand(b *ast.BinaryExpr, aliases map[string]registry.Source) (registry.Source, *ast.StructLit) {
	isProvider := func(e ast.Expr) (registry.Source, bool) {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return registry.Source{}, false
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return registry.Source{}, false
		}
		name, _, _ := ast.LabelName(sel.Sel)
		src, ok := aliases[id.Name]
		return src, ok && name == "#Provider"
	}
	if src, ok := isProvider(b.X); ok {
		if lit, ok := b.Y.(*ast.StructLit); ok {
			return src, lit
		}
	}
	if src, ok := isProvider(b.Y); ok {
		if lit, ok := b.X.(*ast.StructLit); ok {
			return src, lit
		}
	}
	return registry.Source{}, nil
}
