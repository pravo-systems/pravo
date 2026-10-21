package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"cuelang.org/go/cue/build"
	"cuelang.org/go/mod/modfile"

	"github.com/pravo-systems/pravo/internal/registry"
)

// ProviderPackagePrefix is the import path under which pravo generates
// provider schema packages (cue.mod/gen/pravo.dev/providers/...).
const ProviderPackagePrefix = "pravo.dev/providers/"

// ModuleUse is a dependency module that took part in loading a root.
type ModuleUse struct {
	// Path is the module path with its major version, e.g. "github.com/acme/vpc@v1".
	Path string
	// Version is the resolved version, e.g. "v1.2.0", when it can be read
	// from where the module was extracted.
	Version string
	// Hash is "sha256:<hex>" over the module's loaded files.
	Hash string
}

// FindModuleRoot returns the nearest directory at or above dir that holds a
// cue.mod directory, or "" when there is none.
func FindModuleRoot(dir string) string {
	d, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if st, err := os.Stat(filepath.Join(d, "cue.mod")); err == nil && st.IsDir() {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// collectTree records the module root, every loaded source file, the
// dependency modules in use and the provider schema packages imported, by
// walking the instance tree CUE built for the root.
func (r *Root) collectTree(main *build.Instance) {
	r.ModRoot = FindModuleRoot(r.Dir)
	files := map[string]bool{}
	mods := map[string]*moduleAcc{}
	seen := map[*build.Instance]bool{}
	var walk func(i *build.Instance)
	walk = func(i *build.Instance) {
		if i == nil || seen[i] {
			return
		}
		seen[i] = true
		// Dependency modules carry a major version ("path@v1"); the main
		// module and the generated provider packages do not.
		isDep := i.Module != "" && i.Module != main.Module && strings.Contains(i.Module, "@")
		for _, f := range i.BuildFiles {
			files[f.Filename] = true
			if isDep {
				m := mods[i.Module]
				if m == nil {
					root, ver := moduleRootOf(f.Filename)
					m = &moduleAcc{root: root, version: ver}
					mods[i.Module] = m
				}
				m.files = append(m.files, f.Filename)
			}
		}
		for _, imp := range i.Imports {
			walk(imp)
		}
	}
	walk(main)
	if mf, err := r.ModuleFile(); err == nil {
		for path, m := range mods {
			if d, ok := mf.Deps[path]; ok && d != nil && d.Version != "" {
				m.version = d.Version
			}
		}
	}
	r.Sources = r.Sources[:0]
	for f := range files {
		r.Sources = append(r.Sources, f)
	}
	sort.Strings(r.Sources)
	r.Modules = r.Modules[:0]
	for path, m := range mods {
		r.Modules = append(r.Modules, ModuleUse{Path: path, Version: m.version, Hash: hashFiles(m.root, m.files)})
	}
	sort.Slice(r.Modules, func(i, j int) bool { return r.Modules[i].Path < r.Modules[j].Path })
}

type moduleAcc struct {
	root    string
	version string
	files   []string
}

// moduleRootOf finds the extracted module directory of a file in the CUE
// cache, such as ".../github.com/acme/vpc@v1.2.0/net/net.cue", and the
// version in its name.
func moduleRootOf(file string) (root, version string) {
	parts := strings.Split(filepath.ToSlash(file), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if j := strings.LastIndexByte(parts[i], '@'); j >= 0 && strings.HasPrefix(parts[i][j+1:], "v") {
			return filepath.FromSlash(strings.Join(parts[:i+1], "/")), parts[i][j+1:]
		}
	}
	return filepath.Dir(file), ""
}

func hashFiles(root string, files []string) string {
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, f)
		if err != nil {
			rel = f
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(data))
		h.Write(data)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// relName names a loaded file independently of where the checkout or the
// CUE cache lives, so fingerprints agree across machines.
func (r *Root) relName(f string) string {
	if r.ModRoot != "" {
		if rel, err := filepath.Rel(r.ModRoot, f); err == nil && !strings.HasPrefix(rel, "..") {
			return "module/" + filepath.ToSlash(rel)
		}
	}
	// Dependency modules: keep the part from the module path onwards.
	slash := filepath.ToSlash(f)
	if i := strings.Index(slash, "/mod/extract/"); i >= 0 {
		return "dep/" + slash[i+len("/mod/extract/"):]
	}
	return "other/" + filepath.Base(f)
}

// ModuleFile parses cue.mod/module.cue of the module root.
func (r *Root) ModuleFile() (*modfile.File, error) {
	root := r.ModRoot
	if root == "" {
		root = r.Dir
	}
	p := filepath.Join(root, "cue.mod", "module.cue")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return modfile.ParseNonStrict(data, p)
}

// RegistryPrefix is where modules published under github.com/<owner> live
// on the GitHub container registry: ghcr.io/<owner>/cue/<module path>.
const RegistryPrefix = "cue"

// DefaultRegistry builds a CUE_REGISTRY value that sends modules under
// github.com/<owner>/ to ghcr.io/<owner>/cue and everything else to CUE's
// central registry. It returns "" when no module path is under github.com.
func DefaultRegistry(modulePaths ...string) string {
	owners := map[string]bool{}
	for _, p := range modulePaths {
		p = strings.SplitN(p, "@", 2)[0]
		parts := strings.Split(p, "/")
		if len(parts) >= 3 && parts[0] == "github.com" && parts[1] != "" {
			owners[parts[1]] = true
		}
	}
	if len(owners) == 0 {
		return ""
	}
	names := make([]string, 0, len(owners))
	for o := range owners {
		names = append(names, o)
	}
	sort.Strings(names)
	var elems []string
	for _, o := range names {
		elems = append(elems, fmt.Sprintf("github.com/%s=ghcr.io/%s/%s", o, o, RegistryPrefix))
	}
	elems = append(elems, "registry.cue.works")
	return strings.Join(elems, ",")
}

// ConfigureRegistry sets CUE_REGISTRY for this process when the user has not,
// so that dependency modules under github.com/<owner> resolve from
// ghcr.io/<owner>/cue without configuration. extra holds module paths not
// yet in module.cue (for "pravo mod get").
func ConfigureRegistry(dir string, extra ...string) {
	if os.Getenv("CUE_REGISTRY") != "" {
		return
	}
	var paths []string
	paths = append(paths, extra...)
	if root := FindModuleRoot(dir); root != "" {
		if data, err := os.ReadFile(filepath.Join(root, "cue.mod", "module.cue")); err == nil {
			if f, err := modfile.ParseNonStrict(data, "module.cue"); err == nil {
				paths = append(paths, f.Module)
				for p := range f.Deps {
					paths = append(paths, p)
				}
			}
		}
	}
	if reg := DefaultRegistry(paths...); reg != "" {
		os.Setenv("CUE_REGISTRY", reg)
	}
}

// Fingerprint digests everything that was loaded for the root: its own
// files, shared packages elsewhere in the module, generated provider
// packages and the code of dependency modules. A saved plan uses it to
// notice that what it was computed from has changed.
func (r *Root) Fingerprint() (string, error) {
	if len(r.Sources) == 0 {
		return Fingerprint(r.Dir)
	}
	h := sha256.New()
	type entry struct{ name, path string }
	var es []entry
	for _, f := range r.Sources {
		es = append(es, entry{r.relName(f), f})
	}
	sort.Slice(es, func(i, j int) bool { return es[i].name < es[j].name })
	for _, e := range es {
		data, err := os.ReadFile(e.path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", e.name, len(data))
		h.Write(data)
	}
	// The module file pins dependency versions.
	if mf := filepath.Join(r.modRootOrDir(), "cue.mod", "module.cue"); fileExists(mf) {
		data, _ := os.ReadFile(mf)
		fmt.Fprintf(h, "module.cue\x00%d\x00", len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))[:32], nil
}

func (r *Root) modRootOrDir() string {
	if r.ModRoot != "" {
		return r.ModRoot
	}
	return r.Dir
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ModuleNotFoundHint adds guidance to errors about missing provider packages
// and about a root that has no module yet.
func ModuleNotFoundHint(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), ProviderPackagePrefix) && strings.Contains(err.Error(), "cannot find package") {
		return fmt.Errorf("%w\n\nhint: a provider schema package is missing. Run \"pravo init\" to generate it", err)
	}
	if strings.Contains(err.Error(), "imports are unavailable because there is no cue.mod/module.cue file") {
		return fmt.Errorf("%w\n\nhint: the specification imports packages and there is no module. Run \"pravo init\" to create cue.mod/module.cue and generate the provider schema packages", err)
	}
	return err
}

// MissingProviderPackages lists the providers whose generated packages a load
// error says are missing. init uses it to find providers that only an imported
// module needs, which the files of the root itself do not show.
func MissingProviderPackages(err error) []registry.Source {
	if err == nil {
		return nil
	}
	var out []registry.Source
	seen := map[string]bool{}
	text := err.Error()
	marker := "cannot find package \"" + ProviderPackagePrefix
	for {
		i := strings.Index(text, marker)
		if i < 0 {
			return out
		}
		text = text[i+len(marker):]
		j := strings.IndexByte(text, '"')
		if j < 0 {
			return out
		}
		tail := text[:j]
		if k := strings.IndexByte(tail, ':'); k >= 0 {
			tail = tail[:k]
		}
		if src, ok := sourceOfPackage(tail); ok && !seen[src.String()] {
			seen[src.String()] = true
			out = append(out, src)
		}
		text = text[j:]
	}
}

// sourceOfPackage maps the tail of a provider package path, "hashicorp/aws"
// or "example.com/acme/thing", to the provider's source address.
func sourceOfPackage(tail string) (registry.Source, bool) {
	parts := strings.Split(tail, "/")
	if parts[0] == "oci" && len(parts) >= 4 {
		return registry.Source{Host: hostOfPackage(parts[1]), Namespace: strings.Join(parts[2:len(parts)-1], "/"), Type: parts[len(parts)-1], OCI: true}, true
	}
	switch len(parts) {
	case 2:
		return registry.Source{Host: registry.DefaultHost, Namespace: parts[0], Type: parts[1]}, true
	case 3:
		return registry.Source{Host: strings.ToLower(parts[0]), Namespace: parts[1], Type: parts[2]}, true
	}
	return registry.Source{}, false
}

var portSuffix = regexp.MustCompile(`_(\d+)$`)

// hostOfPackage undoes the encoding of a registry port in a package path
// element: "localhost_5000" is localhost:5000.
func hostOfPackage(s string) string {
	return strings.ToLower(portSuffix.ReplaceAllString(s, ":$1"))
}
