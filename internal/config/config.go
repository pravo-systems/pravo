// Package config loads a pravo root (a CUE package directory) and extracts
// the declarations the engine works with.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
	cueerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/load"

	"github.com/pravo-systems/pravo/internal/registry"
)

// refreshAge matches the values of $settings.refresh and $lifecycle.refresh.
var refreshAge = regexp.MustCompile(`^(always|never|[0-9]+(ms|s|m|h|d))$`)

// DefaultStatePath is the state directory when $settings.state.path is unset.
const DefaultStatePath = "pravo.state"

// ProviderReq is a provider requirement: the version and source of the provider structs of one source.
type ProviderReq struct {
	LocalName string
	Source    registry.Source
	// Protocol is how pravo talks to the provider: ProtocolTfplugin (the
	// default: an installed binary started by pravo) or ProtocolReattach
	// (an already running provider at Address).
	Protocol string
	// Address is where a reattached provider listens: unix:///path or tcp://127.0.0.1:port.
	Address string
	// Version is a constraint string; empty means any version.
	Version string
	// Path is a development override pointing at a binary or a directory.
	Path string
	// Implied is set when the requirement was derived from a resource type.
	Implied bool
	// Was lists the addresses this provider had before ($from).
	Was []registry.Source
}

// Provider protocols. The plugin protocol (OpenTofu's, versions 5 and 6) is
// the only one pravo speaks today; the field exists so that others can be
// added without changing the specification, the state or the lock file.
const (
	ProtocolTfplugin = "tfplugin"
	ProtocolReattach = "tfplugin+reattach"
)

// Protocols lists the supported provider protocols.
var Protocols = []string{ProtocolTfplugin, ProtocolReattach}

// Resource is a managed resource, data source, ephemeral resource or
// provider function call: any struct in the specification that carries the
// identity fields of a generated definition ($kind, $type and $source).
type Resource struct {
	Mode string // "managed", "data", "ephemeral", "call" or "builtin"
	Type string
	// Builtin marks a resource that pravo runs itself, with no provider:
	// pravo.#Env.
	Builtin bool
	// Remote is the state location read by a pravo.#RemoteOutputs.
	Remote *RemoteState
	// Name is the last label of the path, for display.
	Name string
	// Path is where the struct sits in the specification.
	Path cue.Path
	// ProviderKey is the provider configuration key: "aws" or "aws.west".
	ProviderKey string
	// RefreshPinned reports that the body sets $lifecycle.refresh: the resource
	// has a refresh age of its own, which holds whether or not a plan reaches it.
	RefreshPinned bool

	// addr is Path as text, set when the resource is found: the address is read
	// on every step of a run, and building it from the path each time adds up
	// over an estate.
	addr string
}

// Address is the resource's CUE path, such as "api.config".
func (r *Resource) Address() string {
	if r.addr != "" {
		return r.addr
	}
	return r.Path.String()
}

// LocalProvider returns the provider local name without alias.
func (r *Resource) LocalProvider() string { return LocalName(r.ProviderKey) }

// LocalName strips an alias from a provider key.
func LocalName(key string) string {
	if i := strings.IndexByte(key, '.'); i >= 0 {
		return key[:i]
	}
	return key
}

// Root is a loaded configuration.
type Root struct {
	// ModRoot is the directory holding cue.mod: the root itself, or an
	// ancestor when several roots share one module. "" when there is none.
	ModRoot string
	// Sources lists every file loaded for the root, including shared
	// packages, generated provider packages and dependency modules.
	Sources []string
	// Modules are the dependency modules that took part, with versions and
	// content hashes.
	Modules   []ModuleUse
	Dir       string
	PkgName   string
	Ctx       *cue.Context
	Value     cue.Value
	Providers map[string]*ProviderReq
	StatePath string
	// State holds the remote state settings when $settings.state.backend is set.
	State StateConfig
	// Refresh is the default refresh age from $settings.refresh.
	Refresh string
	// Moves are the $from declarations: the record at From now belongs to
	// the resource declared at To.
	Moves []Move
	// Imports are the $import declarations by resource address.
	Imports   map[string]*Import
	Resources []*Resource
	// ProviderKeys lists every provider instance key in use, sorted.
	ProviderKeys []string
	// ProviderDecls are the provider structs found in the specification,
	// without the ones that only pass another provider on.
	ProviderDecls []*ProviderDecl
	decls         []*ProviderDecl
	pending       []pendingResource
	moves         []pendingMove
	Outputs       []*Output

	// byAddr finds a resource by address; it is built on first use and again when
	// the number of resources changes.
	byAddrMu sync.Mutex
	byAddr   map[string]*Resource
	byAddrN  int
}

// Move is one $from declaration.
type Move struct {
	From string
	To   string
	// Prefix means From and To are paths with resources below them rather
	// than the address of one resource.
	Prefix bool
}

// Import is one entry of the top-level import field.
type Import struct {
	Addr string
	ID   string
}

// StateConfig is the parsed $settings.state block.
type StateConfig struct {
	// Backend is "local" (the default) or "s3".
	Backend   string
	Bucket    string
	Prefix    string
	Region    string
	Endpoint  string
	PathStyle bool
	Profile   string
	AccessKey string
	SecretKey string
}

// LoadOptions control CUE loading.
type LoadOptions struct {
	Tags    []string
	SysTags bool
}

// Load evaluates the package in dir.
func Load(dir string, opts LoadOptions) (*Root, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	files, err := checkPackageClauses(abs)
	if err != nil {
		return nil, err
	}
	ConfigureRegistry(abs)
	cfg := &load.Config{Dir: abs, Tags: opts.Tags}
	if opts.SysTags {
		cfg.TagVars = load.DefaultTagVars()
	}
	insts := load.Instances([]string{"."}, cfg)
	if len(insts) == 0 {
		return nil, noSpecification(files, fmt.Errorf("no CUE package found in %s", abs))
	}
	inst := insts[0]
	if inst.Err != nil {
		return nil, noSpecification(files, ModuleNotFoundHint(fmt.Errorf("loading %s:\n%s", abs, cueerrors.Details(inst.Err, nil))))
	}
	ctx := cuecontext.New()
	v := ctx.BuildInstance(inst)
	if err := v.Err(); err != nil {
		return nil, ModuleNotFoundHint(fmt.Errorf("evaluating %s:\n%s", abs, cueerrors.Details(err, nil)))
	}
	if err := v.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration:\n%s", cueerrors.Details(err, nil))
	}
	r := &Root{Dir: abs, PkgName: inst.PkgName, Ctx: ctx, Value: v, Providers: map[string]*ProviderReq{}, StatePath: DefaultStatePath}
	r.collectTree(inst)
	if err := r.extract(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Root) extract() error {
	v := r.Value
	settings := v.LookupPath(cue.MakePath(cue.Str("$settings")))
	if settings.Exists() {
		if err := r.extractSettings(settings); err != nil {
			if err = r.settingsWithEnv(err); err != nil {
				return err
			}
		}
	}
	if err := r.discover(); err != nil {
		return err
	}
	return nil
}

// RemoteState says where another root keeps its state.
type RemoteState struct {
	State StateConfig
	// Dir is the state directory of a local backend, absolute.
	Dir string
}

// Output is a value the root publishes: any struct built from pravo.#Output.
// Its name is its address, the path it sits at.
type Output struct {
	Name string
	Path cue.Path
	// Sensitive hides the value where outputs are listed.
	Sensitive bool
}

// ValuePath is where the published value sits.
func (o *Output) ValuePath() cue.Path {
	return cue.MakePath(append(o.Path.Selectors(), cue.Str("value"))...)
}

// hasResourcesUnder reports whether some declared resource sits below the
// path addr.
func (r *Root) hasResourcesUnder(addr string) bool {
	p := cue.ParsePath(addr)
	if p.Err() != nil {
		return false
	}
	want := p.Selectors()
	for _, res := range r.Resources {
		have := res.Path.Selectors()
		if len(have) <= len(want) {
			continue
		}
		match := true
		for i := range want {
			if have[i].String() != want[i].String() {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func validateProtocol(local string, pr *ProviderReq) error {
	switch pr.Protocol {
	case ProtocolTfplugin:
		if pr.Address != "" {
			return fmt.Errorf("provider %s: address applies to protocol %q, not %q", local, ProtocolReattach, ProtocolTfplugin)
		}
	case ProtocolReattach:
		if pr.Path != "" {
			return fmt.Errorf("provider %s: path starts a provider; protocol %q connects to one that is already running", local, ProtocolReattach)
		}
		if pr.Version != "" {
			return fmt.Errorf("provider %s: version does not apply to protocol %q: pravo does not install or select what it connects to", local, ProtocolReattach)
		}
	default:
		return fmt.Errorf("provider %s: unknown protocol %q (supported: %s)", local, pr.Protocol, strings.Join(Protocols, ", "))
	}
	return nil
}

func (r *Root) extractSettings(v cue.Value) error {
	if fs, err := v.Fields(); err == nil {
		for fs.Next() {
			switch name := fs.Selector().Unquoted(); name {
			case "state", "refresh":
			default:
				return fmt.Errorf("$settings.%s is not a setting (the block takes state and refresh)", name)
			}
		}
	}
	if sb := v.LookupPath(cue.ParsePath("state")); sb.Exists() {
		st, path, err := parseState(sb, "$settings.state")
		if err != nil {
			return err
		}
		r.State = st
		if path != "" {
			r.StatePath = path
		}
	}
	if rp := v.LookupPath(cue.ParsePath("refresh")); rp.Exists() {
		s, err := rp.String()
		if err != nil {
			return fmt.Errorf("$settings.refresh: %w", err)
		}
		if !refreshAge.MatchString(s) {
			return fmt.Errorf("$settings.refresh: %q is not a refresh age; want \"always\", \"never\" or a duration such as \"30m\", \"6h\" or \"7d\"", s)
		}
		r.Refresh = s
	}
	return nil
}

// LoadSettings evaluates only the top-level "$settings" field of the package in
// dir, ignoring imports and everything else. It lets "pravo init" discover
// provider requirements when the full configuration cannot be loaded yet
// (for example because it imports generated packages that do not exist).
func LoadSettings(dir string) (*Root, error) {
	return LoadSettingsWith(dir, LoadOptions{})
}

// LoadSettingsWith is LoadSettings with the tags that the specification is
// loaded with, so that a block which depends on a tagged field is read with
// the value the tag is given.
func LoadSettingsWith(dir string, opts LoadOptions) (*Root, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	names, err := checkPackageClauses(abs)
	if err != nil {
		return nil, err
	}
	var files []*ast.File
	pkg := ""
	if len(names) > 0 {
		// The loader reads the files and injects the tags; the imports stay
		// unresolved.
		cfg := &load.Config{Dir: abs, Tags: opts.Tags, SkipImports: true}
		if opts.SysTags {
			cfg.TagVars = load.DefaultTagVars()
		}
		insts := load.Instances([]string{"."}, cfg)
		if len(insts) == 0 {
			return nil, fmt.Errorf("no CUE package found in %s", abs)
		}
		if insts[0].Err != nil {
			return nil, fmt.Errorf("loading %s:\n%s", abs, cueerrors.Details(insts[0].Err, nil))
		}
		files, pkg = insts[0].Files, insts[0].PkgName
	}
	ctx := cuecontext.New()
	// The pravo block is read before the imports can resolve. It may refer to
	// other top-level fields (a tagged env, say), so those that do not
	// themselves depend on an import are included.
	var pravoDecls, otherDecls []ast.Decl
	type candidate struct {
		fld     *ast.Field
		label   string
		imports bool // refers to an imported package, which cannot resolve yet
	}
	var others []candidate
	for _, f := range files {
		if strings.HasSuffix(f.Filename, "_gen.cue") {
			continue
		}
		aliases := map[string]bool{}
		for imp := range f.ImportSpecs() {
			if imp.Name != nil {
				aliases[imp.Name.Name] = true
			} else if ip, err := strconv.Unquote(imp.Path.Value); err == nil {
				aliases[path.Base(ip)] = true
			}
		}
		for _, d := range f.Decls {
			fld, ok := d.(*ast.Field)
			if !ok {
				continue
			}
			if name, _, _ := ast.LabelName(fld.Label); name == "$settings" {
				// $settings: pravo.#Settings & {...} is read as $settings: {...}.
				cp := *fld
				if v := dropImportRefs(fld.Value, aliases); v != nil {
					cp.Value = v
				} else {
					cp.Value = &ast.StructLit{}
				}
				pravoDecls = append(pravoDecls, &cp)
			} else {
				others = append(others, candidate{fld: fld, label: name, imports: mentionsAny(fld.Value, aliases)})
			}
		}
	}
	// A field that refers to a package cannot be read yet, and neither can one
	// that refers to such a field: leave out everything that depends on one.
	excluded := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for i, c := range others {
			if c.imports {
				if !excluded[c.label] {
					excluded[c.label] = true
					changed = true
				}
				continue
			}
			if mentionsAny(c.fld.Value, excluded) {
				others[i].imports = true
				excluded[c.label] = true
				changed = true
			}
		}
	}
	for _, c := range others {
		if !c.imports {
			otherDecls = append(otherDecls, c.fld)
		}
	}
	build := func(decls []ast.Decl) (cue.Value, error) {
		if len(decls) == 0 {
			return ctx.CompileString("{}"), nil
		}
		// Compile from text: the declarations came from different files, so
		// references between them must be resolved afresh.
		src, err := format.Node(&ast.File{Decls: decls})
		if err != nil {
			return cue.Value{}, err
		}
		fv := ctx.CompileBytes(src)
		return fv, fv.Err()
	}
	v, err := build(append(append([]ast.Decl{}, otherDecls...), pravoDecls...))
	if err != nil {
		// Something the block does not need could not be evaluated; read the
		// block on its own.
		if v, err = build(pravoDecls); err != nil {
			return nil, fmt.Errorf("evaluating pravo settings:\n%s", cueerrors.Details(err, nil))
		}
	}
	r := &Root{Dir: abs, PkgName: pkg, Ctx: ctx, Value: v, Providers: map[string]*ProviderReq{}, StatePath: DefaultStatePath, ModRoot: FindModuleRoot(abs)}
	if settings := v.LookupPath(cue.MakePath(cue.Str("$settings"))); settings.Exists() {
		if err := r.extractSettings(settings); err != nil {
			return nil, err
		}
	}
	// The providers the code needs are the ones whose definitions it imports;
	// the module, not just this root, may import them.
	scanDir := abs
	if r.ModRoot != "" {
		scanDir = r.ModRoot
	}
	reqs, err := ScanProviders(scanDir)
	if err != nil {
		return nil, err
	}
	r.Providers = reqs
	return r, nil
}

// StateFile returns the absolute path of the state directory.
func (r *Root) StateFile() string {
	if filepath.IsAbs(r.StatePath) {
		return r.StatePath
	}
	return filepath.Join(r.Dir, r.StatePath)
}

// Resource looks up a declaration by address.
func (r *Root) Resource(addr string) *Resource {
	r.byAddrMu.Lock()
	defer r.byAddrMu.Unlock()
	if r.byAddr == nil || r.byAddrN != len(r.Resources) {
		r.byAddr, r.byAddrN = make(map[string]*Resource, len(r.Resources)), len(r.Resources)
		for _, res := range r.Resources {
			// The first of two resources with one address is the one found.
			if _, ok := r.byAddr[res.Address()]; !ok {
				r.byAddr[res.Address()] = res
			}
		}
	}
	return r.byAddr[addr]
}

// ModuleDir returns the cue.mod directory of the root's module: in the root
// itself or in an ancestor shared with other roots. When there is none it
// returns where one would be created (in the root) and false.
func (r *Root) ModuleDir() (string, bool) {
	if r.ModRoot != "" {
		return filepath.Join(r.ModRoot, "cue.mod"), true
	}
	return filepath.Join(r.Dir, "cue.mod"), false
}

// SharedModule reports whether the root lives below its module root, so
// that other roots may share the module's generated provider packages.
func (r *Root) SharedModule() bool {
	return r.ModRoot != "" && r.ModRoot != r.Dir
}

// Fingerprint digests every CUE file of the root (the package directory and
// cue.mod), so that a saved plan can detect configuration changes.
func Fingerprint(dir string) (string, error) {
	h := sha256.New()
	var files []string
	top, err := filepath.Glob(filepath.Join(dir, "*.cue"))
	if err != nil {
		return "", err
	}
	files = append(files, top...)
	_ = filepath.WalkDir(filepath.Join(dir, "cue.mod"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".cue") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		rel, _ := filepath.Rel(dir, f)
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))[:32], nil
}

// FileHashes lists the files that Fingerprint(dir) digests, each by its path
// below dir and a short digest of its contents.
func FileHashes(dir string) (map[string]string, error) {
	var files []string
	top, err := filepath.Glob(filepath.Join(dir, "*.cue"))
	if err != nil {
		return nil, err
	}
	files = append(files, top...)
	_ = filepath.WalkDir(filepath.Join(dir, "cue.mod"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".cue") {
			files = append(files, p)
		}
		return nil
	})
	out := map[string]string{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(dir, f)
		out[filepath.ToSlash(rel)] = shortDigest(data)
	}
	return out, nil
}

// dropImportRefs removes operands of a unification that are references into
// an imported package, which cannot be resolved while only the pravo block
// is being read.
func dropImportRefs(e ast.Expr, aliases map[string]bool) ast.Expr {
	switch x := e.(type) {
	case *ast.BinaryExpr:
		if x.Op.String() != "&" {
			return e
		}
		l, r := dropImportRefs(x.X, aliases), dropImportRefs(x.Y, aliases)
		switch {
		case l == nil && r == nil:
			return nil
		case l == nil:
			return r
		case r == nil:
			return l
		}
		cp := *x
		cp.X, cp.Y = l, r
		return &cp
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok && aliases[id.Name] {
			return nil
		}
	case *ast.StructLit:
		// The same applies to fields inside the struct: state: p.#S3State &
		// {...} is read as state: {...}.
		cp := *x
		cp.Elts = nil
		for _, el := range x.Elts {
			fld, ok := el.(*ast.Field)
			if !ok {
				cp.Elts = append(cp.Elts, el)
				continue
			}
			v := dropImportRefs(fld.Value, aliases)
			if v == nil {
				continue
			}
			f := *fld
			f.Value = v
			cp.Elts = append(cp.Elts, &f)
		}
		return &cp
	}
	return e
}

// mentionsAny reports whether the expression refers to any of the names.
func mentionsAny(e ast.Expr, names map[string]bool) bool {
	found := false
	ast.Walk(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && names[id.Name] {
			found = true
		}
		return !found
	}, nil)
	return found
}

// parseState reads a state location, as it appears in $settings.state and in
// a pravo.#RemoteOutputs: the s3 settings, or a local directory path. label
// names it in errors.
func parseState(sb cue.Value, label string) (StateConfig, string, error) {
	var st StateConfig
	var path string
	if sp := sb.LookupPath(cue.ParsePath("path")); sp.Exists() {
		s, err := sp.String()
		if err != nil {
			if sp.Err() != nil || sp.Validate(cue.Concrete(true)) != nil {
				return st, "", fmt.Errorf("%s.path is not concrete: %w\n(if it depends on a tagged field such as env, pass -t env=...)", label, err)
			}
			return st, "", fmt.Errorf("%s.path: %w", label, err)
		}
		path = s
	}
	str := func(name string) (string, error) {
		f := sb.LookupPath(cue.ParsePath(name))
		if !f.Exists() {
			return "", nil
		}
		s, err := f.String()
		if err != nil {
			if !f.IsConcrete() {
				return "", fmt.Errorf("%s.%s: %w\n(if its value is a field of the same name, as in %s: %s, the name refers to the field itself; give the other field a different name)", label, name, err, name, name)
			}
			return "", fmt.Errorf("%s.%s: %w", label, name, err)
		}
		return s, nil
	}
	if bv := sb.LookupPath(cue.ParsePath("bucket")); bv.Exists() && !bv.IsConcrete() {
		_, err := bv.String()
		return st, "", fmt.Errorf("%s.bucket is not concrete: %w\n(if it depends on a tagged field such as env, pass -t env=...; if it is written as bucket: bucket, the name refers to the field itself)", label, err)
	}
	var err error
	fields := map[string]*string{
		"backend": &st.Backend, "bucket": &st.Bucket, "prefix": &st.Prefix, "region": &st.Region,
		"endpoint": &st.Endpoint, "profile": &st.Profile, "access_key": &st.AccessKey, "secret_key": &st.SecretKey,
	}
	for name, dst := range fields {
		if *dst, err = str(name); err != nil {
			return st, "", err
		}
	}
	if ps := sb.LookupPath(cue.ParsePath("path_style")); ps.Exists() {
		b, err := ps.Bool()
		if err != nil {
			return st, "", fmt.Errorf("%s.path_style: %w", label, err)
		}
		st.PathStyle = b
	}
	switch st.Backend {
	case "", "local", "s3":
	default:
		return st, "", fmt.Errorf("%s.backend: unknown backend %q (want local or s3)", label, st.Backend)
	}
	if st.Backend == "" && st.Bucket != "" {
		// A bucket can only mean the s3 backend.
		st.Backend = "s3"
	}
	if st.Backend != "s3" {
		for _, f := range []string{"bucket", "prefix", "region", "endpoint", "profile", "access_key", "secret_key", "path_style"} {
			if sb.LookupPath(cue.ParsePath(f)).Exists() {
				return st, "", fmt.Errorf("%s.%s belongs to the s3 backend: set backend: \"s3\", or remove it for local state", label, f)
			}
		}
	}
	if st.Backend == "s3" && sb.LookupPath(cue.ParsePath("path")).Exists() {
		return st, "", fmt.Errorf("%s.path belongs to the local backend: remove it, or remove backend: \"s3\"", label)
	}
	if st.Backend == "s3" && st.Bucket == "" {
		return st, "", fmt.Errorf("%s.bucket is required when backend is s3", label)
	}
	return st, path, nil
}
