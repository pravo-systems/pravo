package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A listing in the documentation shows part of a root: it uses a variable that
// another file declares, or a resource from an earlier step. Before tofu
// validates a root, complete writes _hclcheck.tf beside the listing with what
// the root lacks: required_providers for the providers it uses, a variable for
// each var.NAME that no block declares, a stand-in for each local, resource or
// data source it reads, and a stub module for each local module that no
// listing wrote. The names it adds are returned, and the table prints them.

const stubFile = "_hclcheck.tf"

// providerSources maps the prefix of a resource type to the provider that owns
// it. The empty source is built in to tofu.
var providerSources = map[string]string{
	"aws":        "hashicorp/aws",
	"local":      "hashicorp/local",
	"random":     "hashicorp/random",
	"tls":        "hashicorp/tls",
	"kubernetes": "hashicorp/kubernetes",
	"null":       "hashicorp/null",
	"time":       "hashicorp/time",
	"http":       "hashicorp/http",
	"archive":    "hashicorp/archive",
	"external":   "hashicorp/external",
	"dns":        "hashicorp/dns",
	"helm":       "hashicorp/helm",
	"github":     "integrations/github",
	"terraform":  "",
}

// resourceStubs is the smallest body that makes a resource of the type valid.
var resourceStubs = map[string]string{
	"aws_vpc":         `cidr_block = "10.0.0.0/16"`,
	"aws_instance":    `ami = "ami-0abc1234"` + "\n  " + `instance_type = "t3.micro"`,
	"random_integer":  "min = 1\n  max = 2",
	"random_password": "length = 8",
	"tls_private_key": `algorithm = "ED25519"`,
	"local_file":      `filename = "stub.txt"` + "\n  " + `content  = "stub"`,
}

var dataStubs = map[string]string{
	"local_file":             `filename = "stub.txt"`,
	"terraform_remote_state": `backend = "local"` + "\n  " + `config = { path = "stub.tfstate" }`,
}

var (
	varRef   = regexp.MustCompile(`\bvar\.([A-Za-z_][\w-]*)`)
	localRef = regexp.MustCompile(`\blocal\.([A-Za-z_][\w-]*)`)
	modRef   = regexp.MustCompile(`\bmodule\.([A-Za-z_][\w-]*)(?:\.([A-Za-z_][\w-]*))?`)
	dataRef  = regexp.MustCompile(`\bdata\.([a-z0-9]+_[\w]+)\.([A-Za-z_][\w-]*)`)
	resRef   = regexp.MustCompile(`(^|[^\w.-])([a-z0-9]+_[a-z0-9_]+)\.([A-Za-z_][\w-]*)`)
	fnRef    = regexp.MustCompile(`\bprovider::([A-Za-z_][\w-]*)::`)
)

type moduleCall struct {
	Name, Source string
	Body         string
}

// inventory is what a root declares and what it reads.
type inventory struct {
	vars, locals     map[string]bool
	resources, datas map[string]bool // "type.name"
	modules          map[string]moduleCall
	providers        map[string]bool // local names: provider blocks, resource prefixes, functions
	declared         map[string]bool // local names in required_providers
	hasRequired      bool
}

func newInventory() *inventory {
	return &inventory{
		vars: map[string]bool{}, locals: map[string]bool{},
		resources: map[string]bool{}, datas: map[string]bool{},
		modules: map[string]moduleCall{}, providers: map[string]bool{}, declared: map[string]bool{},
	}
}

func typePrefix(t string) string {
	p, _, _ := strings.Cut(t, "_")
	return p
}

// scanDeclared reads the top-level blocks of a comment-free source.
func (inv *inventory) scanDeclared(src string) {
	for _, b := range blocks(src) {
		label := func(i int) string {
			if i < len(b.Labels) {
				return b.Labels[i]
			}
			return ""
		}
		switch b.Type {
		case "variable":
			inv.vars[label(0)] = true
		case "locals":
			for _, n := range attrs(b.Body) {
				inv.locals[n] = true
			}
		case "resource":
			inv.resources[label(0)+"."+label(1)] = true
			inv.providers[typePrefix(label(0))] = true
		case "data":
			inv.datas[label(0)+"."+label(1)] = true
			inv.providers[typePrefix(label(0))] = true
		case "module":
			src, _ := attrValue(b.Body, "source")
			inv.modules[label(0)] = moduleCall{Name: label(0), Source: strings.Trim(src, `"`), Body: b.Body}
		case "provider":
			inv.providers[label(0)] = true
		case "terraform":
			for _, sub := range blocks(b.Body) {
				if sub.Type == "required_providers" {
					inv.hasRequired = true
					for _, n := range attrs(sub.Body) {
						inv.declared[n] = true
					}
				}
			}
		}
	}
}

// An addition is what complete added to a root.
type addition struct {
	providers []string // required_providers entries
	stubs     []string // variables, locals, resources, data sources, modules and files
}

func (a addition) String() string {
	var parts []string
	if len(a.providers) > 0 {
		parts = append(parts, "providers "+strings.Join(a.providers, ", "))
	}
	if len(a.stubs) > 0 {
		parts = append(parts, "stubs "+strings.Join(a.stubs, ", "))
	}
	return strings.Join(parts, "; ")
}

// complete removes every stub file below ws, then writes the stubs that the
// root ws/rel needs. It returns what it added.
func complete(ws, rel string) (add addition, err error) {
	err = filepath.WalkDir(ws, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == stubFile {
			return os.Remove(p)
		}
		return err
	})
	if err != nil {
		return add, err
	}
	dir := filepath.Join(ws, rel)
	files, _ := filepath.Glob(filepath.Join(dir, "*.tf"))
	sort.Strings(files)
	var srcs, overrides []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return add, err
		}
		if isOverride(f) {
			overrides = append(overrides, stripComments(string(b)))
		} else {
			srcs = append(srcs, stripComments(string(b)))
		}
	}
	src := strings.Join(srcs, "\n")
	// What a file reads: the override files too, but not the addresses of a
	// moved block's from, which names an object that may no longer exist.
	all := movedFrom.ReplaceAllString(strings.Join(append(append([]string{}, srcs...), overrides...), "\n"), "")

	inv := newInventory()
	inv.scanDeclared(src)
	ov := newInventory()
	for _, o := range overrides {
		ov.scanDeclared(o)
	}

	needProv := map[string]bool{}
	for p := range inv.providers {
		needProv[p] = true
	}
	for p := range ov.providers {
		needProv[p] = true
	}
	for _, m := range fnRef.FindAllStringSubmatch(all, -1) {
		needProv[m[1]] = true
	}

	// Provider aliases: local.NAME in a providers map names a configuration.
	aliases := map[string]bool{}
	for _, b := range blocks(src) {
		if b.Type == "provider" && len(b.Labels) > 0 {
			if v, ok := attrValue(b.Body, "alias"); ok {
				aliases[b.Labels[0]+"."+strings.Trim(v, `"`)] = true
			}
		}
	}

	var vars, locals, res, dat []string
	for _, m := range varRef.FindAllStringSubmatch(all, -1) {
		if !inv.vars[m[1]] {
			inv.vars[m[1]] = true
			vars = append(vars, m[1])
		}
	}
	for _, m := range localRef.FindAllStringSubmatch(all, -1) {
		if !inv.locals[m[1]] && !aliases["local."+m[1]] {
			inv.locals[m[1]] = true
			locals = append(locals, m[1])
		}
	}
	for _, m := range dataRef.FindAllStringSubmatch(all, -1) {
		k := m[1] + "." + m[2]
		if !inv.datas[k] {
			inv.datas[k] = true
			dat = append(dat, k)
			needProv[typePrefix(m[1])] = true
		}
	}
	for _, m := range resRef.FindAllStringSubmatch(all, -1) {
		if _, known := providerSources[typePrefix(m[2])]; !known {
			continue
		}
		k := m[2] + "." + m[3]
		if !inv.resources[k] {
			inv.resources[k] = true
			res = append(res, k)
			needProv[typePrefix(m[2])] = true
		}
	}
	// An override file replaces arguments of an object another file declares.
	for k := range ov.resources {
		if !inv.resources[k] {
			inv.resources[k] = true
			res = append(res, k)
		}
	}
	for k := range ov.datas {
		if !inv.datas[k] {
			inv.datas[k] = true
			dat = append(dat, k)
		}
	}
	for k := range ov.vars {
		if !inv.vars[k] {
			inv.vars[k] = true
			vars = append(vars, k)
		}
	}
	sort.Strings(vars)
	sort.Strings(locals)
	sort.Strings(res)
	sort.Strings(dat)

	var out strings.Builder
	// A root that has its own required_providers is left alone.
	if !inv.hasRequired {
		var names []string
		for p := range needProv {
			names = append(names, p)
		}
		sort.Strings(names)
		var lines []string
		for _, p := range names {
			source, ok := providerSources[p]
			if !ok {
				add.stubs = append(add.stubs, "unknown provider "+p)
				continue
			}
			if source == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("    %s = { source = %q }", p, source))
			add.providers = append(add.providers, p)
		}
		if len(lines) > 0 {
			fmt.Fprintf(&out, "terraform {\n  required_providers {\n%s\n  }\n}\n\n", strings.Join(lines, "\n"))
		}
	}
	for _, v := range vars {
		fmt.Fprintf(&out, "variable %q {}\n\n", v)
		add.stubs = append(add.stubs, "var."+v)
	}
	for _, l := range locals {
		fmt.Fprintf(&out, "variable \"hclcheck_local_%s\" {}\n\nlocals {\n  %s = var.hclcheck_local_%s\n}\n\n", l, l, l)
		add.stubs = append(add.stubs, "local."+l)
	}
	for _, r := range res {
		t, n, _ := strings.Cut(r, ".")
		fmt.Fprintf(&out, "resource %q %q {\n  %s\n}\n\n", t, n, resourceStubs[t])
		add.stubs = append(add.stubs, r)
	}
	for _, d := range dat {
		t, n, _ := strings.Cut(d, ".")
		fmt.Fprintf(&out, "data %q %q {\n  %s\n}\n\n", t, n, dataStubs[t])
		add.stubs = append(add.stubs, "data."+d)
	}
	if out.Len() > 0 {
		if err := os.WriteFile(filepath.Join(dir, stubFile), []byte(out.String()), 0o644); err != nil {
			return add, err
		}
	}

	// Files that the configuration reads.
	for _, m := range fileRef.FindAllStringSubmatch(all, -1) {
		p := filepath.Join(dir, filepath.FromSlash(m[1]))
		if _, err := os.Stat(p); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return add, err
		}
		if err := os.WriteFile(p, []byte(fixture(m[1])), 0o644); err != nil {
			return add, err
		}
		add.stubs = append(add.stubs, "file "+m[1])
	}

	// Local modules that no listing wrote.
	var mods []string
	for name := range inv.modules {
		mods = append(mods, name)
	}
	sort.Strings(mods)
	for _, name := range mods {
		m := inv.modules[name]
		if !strings.HasPrefix(m.Source, "./") && !strings.HasPrefix(m.Source, "../") {
			continue
		}
		target := filepath.Clean(filepath.Join(dir, m.Source))
		if r, err := filepath.Rel(ws, target); err != nil || strings.HasPrefix(r, "..") {
			return add, fmt.Errorf("module %q: source %q leaves the page root", name, m.Source)
		}
		if tf, _ := filepath.Glob(filepath.Join(target, "*.tf")); len(tf) > 0 {
			continue
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return add, err
		}
		if err := os.WriteFile(filepath.Join(target, stubFile), []byte(stubModule(m, all)), 0o644); err != nil {
			return add, err
		}
		add.stubs = append(add.stubs, "module "+m.Source)
	}
	return add, nil
}

var (
	movedFrom = regexp.MustCompile(`(?m)^[ \t]*from[ \t]*=.*$`)
	fileRef   = regexp.MustCompile(`\b(?:file|filebase64|filesha256|filemd5)\(\s*"(?:\$\{path\.module\}/|\./)?([^"$]+)"`)
)

// isOverride reports whether a file is an override file: override.tf, or a
// name that ends in _override.tf.
func isOverride(path string) bool {
	n := filepath.Base(path)
	return n == "override.tf" || strings.HasSuffix(n, "_override.tf")
}

// fixture is the content of a file that a configuration reads with file().
func fixture(name string) string {
	switch {
	case strings.HasSuffix(name, ".json"):
		return "{}\n"
	case strings.HasSuffix(name, ".yaml"), strings.HasSuffix(name, ".yml"):
		return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: stub\n"
	}
	return "stub\n"
}

var metaArgs = map[string]bool{"source": true, "version": true, "count": true, "for_each": true, "providers": true, "depends_on": true}

var providersMap = regexp.MustCompile(`(?s)\bproviders\s*=\s*\{(.*?)\}`)

// stubModule is the body of a module that accepts every argument the call
// passes and has an output for every attribute the root reads from it.
func stubModule(m moduleCall, rootSrc string) string {
	var b strings.Builder
	if pm := providersMap.FindStringSubmatch(m.Body); pm != nil {
		var names []string
		for _, n := range attrs(pm[1]) {
			names = append(names, n)
		}
		if len(names) > 0 {
			b.WriteString("terraform {\n  required_providers {\n")
			for _, n := range names {
				if src := providerSources[n]; src != "" {
					fmt.Fprintf(&b, "    %s = { source = %q }\n", n, src)
				}
			}
			b.WriteString("  }\n}\n\n")
		}
	}
	for _, a := range attrs(m.Body) {
		if !metaArgs[a] {
			fmt.Fprintf(&b, "variable %q {}\n\n", a)
		}
	}
	seen := map[string]bool{}
	for _, r := range modRef.FindAllStringSubmatch(rootSrc, -1) {
		if r[1] == m.Name && r[2] != "" && !seen[r[2]] {
			seen[r[2]] = true
			fmt.Fprintf(&b, "output %q {\n  value = null\n}\n\n", r[2])
		}
	}
	return b.String()
}
