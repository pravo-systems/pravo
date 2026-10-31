package serve

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/token"
	"cuelang.org/go/mod/modfile"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/convert"
	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/env"
	"github.com/pravo-systems/pravo/internal/state"
)

// specState is the specification as it was loaded, kept for as long as its
// files stay the same: evaluating a large specification takes seconds, and a
// change of the state does not change it.
type specState struct {
	sig    string
	loaded time.Time
	// root is nil when the specification does not load; err says why.
	root *config.Root
	err  string
	// settings is what could be read of the state location without the
	// specification, for the state-only view.
	settings *config.Root
	lock     *engine.Lock

	// edges maps a node to the nodes it reads, from the specification.
	edges map[string][]string
	// refs maps a node to where its struct is written, and discRefs a discovery.
	refs     map[string]*Ref
	discRefs map[string]*Ref
	// polRefs maps a policy, by the name the state gives it, to where it is written.
	polRefs map[string]*Ref
	// allowed lists the files that Root.Sources names, cleaned.
	allowed map[string]bool
	// real maps each of them to the file it resolved to when the specification was
	// loaded, so that a link that is pointed elsewhere afterwards is not followed.
	real map[string]string
	// base is the directory that display paths are relative to.
	base string
	// cacheRoots are the directories of the CUE module cache.
	modRoot string
	genDir  string

	// namespaces caches the position of a namespace struct and of the definition it
	// instantiates, by path.
	namespaces map[string][2]*Ref
	// byPath finds a declared resource by address.
	byPath map[string]*config.Resource
	// outputs finds a declared output by address, which is its name. Its edges are
	// in edges, with those of the resources.
	outputs map[string]*config.Output
	// files are the files whose change makes the specification stale.
	files []string
	// abs maps a display path to the file it was made from.
	abs map[string]string
	// typeDefs caches where the definition of a resource's type is written.
	typeDefs map[string]*Ref
	// taint lists, by address and field, the attributes that the field reads: see taint.go.
	taint map[string]map[string][]taintRef
	// cueMu orders every use of the CUE values of root: the evaluation is not
	// safe for concurrent use.
	cueMu sync.Mutex
}

// loadSpec loads the specification of the project. A specification that does
// not load is not an error: the viewer shows the state alone.
func loadSpec(opts Options, sig string) *specState {
	ss := &specState{sig: sig, loaded: time.Now().UTC(), edges: map[string][]string{}, refs: map[string]*Ref{}, discRefs: map[string]*Ref{}, polRefs: map[string]*Ref{},
		allowed: map[string]bool{}, real: map[string]string{}, namespaces: map[string][2]*Ref{}, byPath: map[string]*config.Resource{},
		abs: map[string]string{}, typeDefs: map[string]*Ref{}, outputs: map[string]*config.Output{}}
	dir := opts.Dir
	lo := config.LoadOptions{Tags: opts.Tags, SysTags: opts.SysTags, Env: opts.Env}
	ss.lock, _ = engine.ReadLock(dir)
	if ss.lock == nil {
		ss.lock = &engine.Lock{Providers: map[string]*engine.LockEntry{}, Modules: map[string]*engine.ModuleEntry{}}
	}
	// Loading a package that imports a module fetches the module when the cache
	// lacks it. The viewer downloads nothing.
	if missing := uncachedModule(dir, opts.Env); missing != "" {
		ss.err = fmt.Sprintf("the dependency module %s is not in the CUE cache; run \"pravo init\" to fetch it", missing)
	} else if root, err := config.Load(dir, lo); err != nil {
		ss.err = errorText(err)
	} else {
		ss.root = root
	}
	if ss.root == nil {
		ss.settings, _ = config.LoadSettingsWith(dir, lo)
		ss.base = config.FindModuleRoot(dir)
		if ss.base == "" {
			ss.base = dir
		}
		ss.modRoot = ss.base
		ss.genDir = filepath.Join(ss.base, "cue.mod", "gen")
		return ss
	}
	root := ss.root
	ss.modRoot = root.ModRoot
	ss.base = root.ModRoot
	if ss.base == "" {
		ss.base = root.Dir
	}
	ss.genDir = filepath.Join(ss.base, "cue.mod", "gen")
	for _, f := range root.Sources {
		ss.allowed[filepath.Clean(f)] = true
		if r, err := filepath.EvalSymlinks(f); err == nil {
			ss.real[filepath.Clean(f)] = r
		}
	}
	ss.files = append(ss.files, root.Sources...)
	for _, r := range root.Resources {
		if _, ok := ss.byPath[r.Address()]; !ok {
			ss.byPath[r.Address()] = r
		}
	}
	for _, o := range root.Outputs {
		if _, ok := ss.outputs[o.Name]; !ok {
			ss.outputs[o.Name] = o
		}
	}
	ss.computeEdges()
	for _, r := range root.Resources {
		if _, ok := ss.refs[r.Address()]; ok {
			continue
		}
		if ref := ss.refOf(root.Value.LookupPath(r.Path)); ref != nil {
			ss.refs[r.Address()] = ref
		}
	}
	for _, d := range root.Discoveries {
		if ref := ss.refOf(root.Value.LookupPath(d.Path)); ref != nil {
			ss.discRefs[d.Name] = ref
		}
	}
	for _, p := range root.Policies {
		if ref := ss.refOf(root.Value.LookupPath(p.Path)); ref != nil {
			ss.polRefs[state.PolicyName(p.Name)] = ref
		}
	}
	for _, o := range root.Outputs {
		if _, ok := ss.refs[o.Name]; ok {
			continue
		}
		if ref := ss.refOf(root.Value.LookupPath(o.Path)); ref != nil {
			ss.refs[o.Name] = ref
		}
	}
	return ss
}

// errorText is the message of a load error, shortened to what a banner holds.
func errorText(err error) string {
	text := strings.TrimSpace(err.Error())
	if len(text) > 1500 {
		text = text[:1500] + "…"
	}
	return text
}

// uncachedModule returns the first dependency module of the project that the
// CUE module cache does not hold, or "".
func uncachedModule(dir string, e env.Env) string {
	root := config.FindModuleRoot(dir)
	if root == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, "cue.mod", "module.cue"))
	if err != nil {
		return ""
	}
	mf, err := modfile.ParseNonStrict(data, "module.cue")
	if err != nil || len(mf.Deps) == 0 {
		return ""
	}
	cache := e.Get("CUE_CACHE_DIR")
	if cache == "" {
		base, ok := e.UserCacheDir()
		if !ok {
			return ""
		}
		cache = filepath.Join(base, "cue")
	}
	paths := make([]string, 0, len(mf.Deps))
	for p := range mf.Deps {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		d := mf.Deps[p]
		if d == nil || d.Version == "" {
			continue
		}
		name := p
		if i := strings.IndexByte(name, '@'); i >= 0 {
			name = name[:i]
		}
		extracted := filepath.Join(cache, "mod", "extract", filepath.FromSlash(name)+"@"+d.Version)
		if _, err := os.Stat(extracted); err != nil {
			return p
		}
	}
	return ""
}

// specResolver tells the reference analysis where the resources are. With
// outputs it takes the outputs of the root for tasks as well, so that an output
// that reads another is seen to read it, and not what the other one reads.
type specResolver struct {
	root    *config.Root
	byPath  map[string]*config.Resource
	outputs map[string]*config.Output
	below   map[string][]cue.Path
}

// addrOf names the node at the path of a task, or "" when it is none.
func (r *specResolver) addrOf(task cue.Path) string {
	if res, ok := r.byPath[task.String()]; ok {
		return res.Address()
	}
	if o, ok := r.outputs[task.String()]; ok {
		return o.Name
	}
	return ""
}

func newSpecResolver(root *config.Root) *specResolver {
	r := &specResolver{root: root, byPath: map[string]*config.Resource{}, outputs: map[string]*config.Output{}, below: map[string][]cue.Path{}}
	for _, res := range root.Resources {
		r.byPath[res.Path.String()] = res
		r.addBelow(res.Path)
	}
	return r
}

// withOutputs makes the outputs of the root tasks of the resolver.
func (r *specResolver) withOutputs() *specResolver {
	for _, o := range r.root.Outputs {
		if _, ok := r.outputs[o.Path.String()]; !ok {
			r.outputs[o.Path.String()] = o
			r.addBelow(o.Path)
		}
	}
	return r
}

func (r *specResolver) addBelow(p cue.Path) {
	sels := p.Selectors()
	for i := 1; i < len(sels); i++ {
		k := cue.MakePath(sels[:i]...).String()
		r.below[k] = append(r.below[k], p)
	}
}

func (r *specResolver) Task(p cue.Path) (cue.Path, cue.Path, bool) {
	sels := p.Selectors()
	for i := 1; i <= len(sels); i++ {
		cand := cue.MakePath(sels[:i]...)
		if _, ok := r.byPath[cand.String()]; ok {
			return cand, cand, true
		}
		if _, ok := r.outputs[cand.String()]; ok {
			return cand, cand, true
		}
	}
	return cue.Path{}, cue.Path{}, false
}

func (r *specResolver) Below(p cue.Path) []cue.Path {
	return r.below[cue.MakePath(p.Selectors()...).String()]
}

func (r *specResolver) Value(p cue.Path) cue.Value { return r.root.Value.LookupPath(p) }

var _ convert.Resolver = (*specResolver)(nil)

// computeEdges finds what each declared resource reads, as the engine does
// when it builds its graph: the references in its body, and the references in
// the configuration of its provider.
func (ss *specState) computeEdges() {
	root := ss.root
	res := newSpecResolver(root)
	deps := map[string]map[string]bool{}
	for _, r := range root.Resources {
		v := root.Value.LookupPath(r.Path)
		ds, _ := convert.Dependencies(v, r.Path, res)
		ss.recordTaint(r.Address(), taintOf(v, r.Path, res, ""))
		set := deps[r.Address()]
		if set == nil {
			set = map[string]bool{}
			deps[r.Address()] = set
		}
		for _, d := range ds {
			if dr, ok := res.byPath[d.String()]; ok && dr != r {
				set[dr.Address()] = true
			}
		}
	}
	providerDeps := map[string][]string{}
	for _, key := range root.ProviderKeys {
		p, ok := root.ProviderPath(key)
		if !ok {
			continue
		}
		v := root.Value.LookupPath(p)
		if !v.Exists() {
			continue
		}
		ds, _ := convert.Dependencies(v, p, res)
		for _, d := range ds {
			if dr, ok := res.byPath[d.String()]; ok {
				providerDeps[key] = append(providerDeps[key], dr.Address())
			}
		}
	}
	for _, r := range root.Resources {
		for _, d := range providerDeps[r.ProviderKey] {
			if d != r.Address() {
				deps[r.Address()][d] = true
			}
		}
	}
	// An output reads what its value reads: a resource, a built-in value or another
	// output. The reference analysis is the engine's, with the outputs counted as
	// tasks, so a reference through a namespace or an intermediate field is followed
	// and an output that reads another one is joined to it, not to what it reads.
	outRes := newSpecResolver(root).withOutputs()
	for _, o := range root.Outputs {
		if _, done := deps[o.Name]; done {
			continue
		}
		set := map[string]bool{}
		deps[o.Name] = set
		p := o.ValuePath()
		v := root.Value.LookupPath(p)
		ds, _ := convert.Dependencies(v, p, outRes)
		ss.recordTaint(o.Name, taintOf(v, p, outRes, "value"))
		for _, d := range ds {
			if dr, ok := outRes.byPath[d.String()]; ok {
				set[dr.Address()] = true
			} else if do, ok := outRes.outputs[d.String()]; ok && do != o {
				set[do.Name] = true
			}
		}
	}
	for addr, set := range deps {
		list := make([]string, 0, len(set))
		for d := range set {
			list = append(list, d)
		}
		sort.Strings(list)
		ss.edges[addr] = list
	}
}

// refOf returns where the value is written: the field that holds it.
func (ss *specState) refOf(v cue.Value) *Ref {
	if !v.Exists() {
		return nil
	}
	var pos, end token.Pos
	if src := v.Source(); src != nil {
		pos, end = src.Pos(), src.End()
	}
	if !pos.IsValid() {
		pos, end = v.Pos(), token.NoPos
	}
	if !pos.IsValid() || pos.Filename() == "" {
		return nil
	}
	ref := &Ref{File: ss.display(pos.Filename()), Line: pos.Line(), Col: pos.Column()}
	if end.IsValid() && end.Filename() == pos.Filename() && end.Line() >= pos.Line() {
		ref.EndLine = end.Line()
	}
	return ref
}

// display writes the path of a loaded file the way a page shows it: relative to
// the module root, cache/ for a dependency module, generated/ for a generated
// package.
func (ss *specState) display(file string) string {
	file = filepath.Clean(file)
	name := ss.displayName(file)
	ss.abs[name] = file
	return name
}

func (ss *specState) displayName(file string) string {
	slash := filepath.ToSlash(file)
	if rel, err := filepath.Rel(ss.genDir, file); err == nil && !strings.HasPrefix(rel, "..") {
		return "generated/" + filepath.ToSlash(rel)
	}
	if i := strings.Index(slash, "/mod/extract/"); i >= 0 {
		return "cache/" + slash[i+len("/mod/extract/"):]
	}
	if ss.base != "" {
		if rel, err := filepath.Rel(ss.base, file); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(file)
}

// fileKind says what kind of file a loaded file is: "file", "cache" or
// "generated".
func (ss *specState) fileKind(file string) string {
	file = filepath.Clean(file)
	if rel, err := filepath.Rel(ss.genDir, file); err == nil && !strings.HasPrefix(rel, "..") {
		return "generated"
	}
	if strings.Contains(filepath.ToSlash(file), "/mod/extract/") {
		return "cache"
	}
	return "file"
}

// namespaceRefs returns where the struct at path is written, and where the
// definition it instantiates is written.
func (ss *specState) namespaceRefs(path string) (declared, definition *Ref) {
	if ss.root == nil {
		return nil, nil
	}
	if r, ok := ss.namespaces[path]; ok {
		return r[0], r[1]
	}
	p := cue.ParsePath(path)
	if p.Err() == nil {
		v := ss.root.Value.LookupPath(p)
		declared = ss.refOf(v)
		if v.Exists() {
			definition = ss.definitionOf(v, 0)
		}
	}
	ss.namespaces[path] = [2]*Ref{declared, definition}
	return declared, definition
}

// definitionOf finds the definition that a struct is built from: an operand of
// its unification that is a reference to a definition, such as #Service in
// #Service & {name: "api"}.
func (ss *specState) definitionOf(v cue.Value, depth int) *Ref {
	if depth > 4 {
		return nil
	}
	if root, p := v.ReferencePath(); root.Exists() {
		if sels := p.Selectors(); len(sels) > 0 && sels[len(sels)-1].IsDefinition() {
			return ss.refOf(root.LookupPath(p))
		}
	}
	op, args := v.Expr()
	if op != cue.AndOp {
		return nil
	}
	for _, a := range args {
		if ref := ss.definitionOf(a, depth+1); ref != nil {
			return ref
		}
	}
	return nil
}
