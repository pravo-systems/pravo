package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/parser"
)

// Environments. A specification that is applied to several environments, each with a
// state of its own, declares them once, in the top-level field $environments:
//
//	$environments: {
//		dev:     {workers: 1, $default: true}
//		staging: {workers: 2, $after: "dev"}
//		prod:    {workers: 3, $after: "staging"}
//	}
//
// Each key is an environment. A row holds the fields of the user, which say what differs
// between the environments, and the fields of pravo, which begin with $: $after names the
// environment (or a list of them) whose release this one takes, and $default marks the row
// that a command uses when it names none. -t env=prod selects a row, and before the
// specification is evaluated pravo fills $environment with it and with $name, the name of
// the row. Every other field of the specification reads the row from there:
//
//	$settings: state: path: "state/\($environment.$name)"
//	server: workers: $environment.workers
//
// The selector is not a tag of the specification: env is taken off the tags that the CUE
// loader is given, and a field of the specification that carries @tag(env) is refused, so
// that the environment has one source.

// EnvTag is the tag that selects an environment: -t env=prod.
const EnvTag = "env"

// envOverlayName is the file that fills $environment, laid over the package for the load.
// It is not on disk, and it is not a source of the root.
const envOverlayName = "pravo_environment.cue"

// Environment is one row of $environments.
type Environment struct {
	Name string
	// After lists the environments whose release this one takes, in the order the row
	// gives them. Empty for an environment that takes anything.
	After []string
	// Default marks the row that is used when no -t env is given.
	Default bool
}

// envNameRe is what an environment may be called: a name that goes into a path, a bucket
// prefix and a command line without quoting.
var envNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// declaresEnvironmentsRe finds a top-level $environments field in the text of a file
// cheaply, before it is parsed. A match is confirmed on the syntax tree.
var declaresEnvironmentsRe = regexp.MustCompile(`(?m)^[ \t]*\$environments[ \t]*:`)

// mayDeclareEnvironments reports whether a file of the package may declare
// $environments.
func mayDeclareEnvironments(abs string, names []string) bool {
	for _, n := range names {
		if strings.HasSuffix(n, "_gen.cue") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(abs, n))
		if err == nil && declaresEnvironmentsRe.Match(data) {
			return true
		}
	}
	return false
}

// splitEnvTag takes the selector off the tags: it returns the other tags, and the
// environment that -t env= names, if any. The last one given wins, as with any tag.
func splitEnvTag(tags []string) (rest []string, name string, given bool) {
	for _, t := range tags {
		if k, v, ok := strings.Cut(t, "="); ok && strings.TrimSpace(k) == EnvTag {
			name, given = strings.TrimSpace(v), true
			continue
		}
		rest = append(rest, t)
	}
	return rest, name, given
}

// WithEnvironment returns tags with the selector set to name: -t env=name.
func WithEnvironment(tags []string, name string) []string {
	rest, _, _ := splitEnvTag(tags)
	return append(append([]string{}, rest...), EnvTag+"="+name)
}

// EnvironmentOf returns the environment that -t env= names in tags.
func EnvironmentOf(tags []string) (string, bool) {
	_, name, given := splitEnvTag(tags)
	return name, given
}

// WithoutEnvironment returns tags without the selector.
func WithoutEnvironment(tags []string) []string {
	rest, _, _ := splitEnvTag(tags)
	return rest
}

// envTagField finds a field that carries @tag(env) in the files, and returns where, relative
// to the directory of the root.
func envTagField(dir string, files []*ast.File) string {
	where := ""
	for _, f := range files {
		ast.Walk(f, func(n ast.Node) bool {
			if where != "" {
				return false
			}
			a, ok := n.(*ast.Attribute)
			if !ok {
				return true
			}
			key, body := a.Split()
			if key != "tag" {
				return true
			}
			name, _, _ := strings.Cut(body, ",")
			if strings.TrimSpace(name) == EnvTag {
				p := a.Pos()
				file := p.Filename()
				if rel, err := filepath.Rel(dir, file); err == nil && !strings.HasPrefix(rel, "..") {
					file = "./" + filepath.ToSlash(rel)
				}
				where = fmt.Sprintf("%s:%d:%d", file, p.Line(), p.Column())
			}
			return true
		}, nil)
	}
	return where
}

// topEnvironments finds the top-level $environments declarations of the files.
func topEnvironments(files []*ast.File) bool {
	for _, f := range files {
		if strings.HasSuffix(f.Filename, "_gen.cue") {
			continue
		}
		for _, d := range f.Decls {
			if fld, ok := d.(*ast.Field); ok {
				if name, _, _ := ast.LabelName(fld.Label); name == "$environments" {
					return true
				}
			}
		}
	}
	return false
}

// parseEnvironments reads the evaluated $environments and checks it: every row is a
// struct, every $after names another row, the rows that follow one another make no
// circle, and at most one row is the default. The rows are returned in the order of the
// ladder: an environment after every one it follows, and otherwise in the order written.
func parseEnvironments(v cue.Value) ([]Environment, error) {
	const label = "$environments"
	if v.IncompleteKind() != cue.StructKind {
		return nil, fmt.Errorf("%s is a struct with one field for each environment, such as {dev: {}, prod: {$after: \"dev\"}}", label)
	}
	it, err := v.Fields()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	var envs []Environment
	index := map[string]int{}
	defaults := []string{}
	for it.Next() {
		name := it.Selector().Unquoted()
		if !envNameRe.MatchString(name) {
			return nil, fmt.Errorf("%s.%s: an environment is named with letters, digits, '.', '-' and '_', and starts with a letter or a digit", label, strconv.Quote(name))
		}
		row := it.Value()
		if row.IncompleteKind() != cue.StructKind {
			return nil, fmt.Errorf("%s.%s is a struct, such as {} or {$after: \"dev\"}", label, name)
		}
		e := Environment{Name: name}
		if a := row.LookupPath(cue.MakePath(cue.Str("$after"))); a.Exists() {
			switch a.IncompleteKind() {
			case cue.StringKind:
				s, err := a.String()
				if err != nil {
					return nil, fmt.Errorf("%s.%s.$after: %w", label, name, err)
				}
				e.After = []string{s}
			case cue.ListKind:
				l, err := a.List()
				if err != nil {
					return nil, fmt.Errorf("%s.%s.$after: %w", label, name, err)
				}
				for l.Next() {
					s, err := l.Value().String()
					if err != nil {
						return nil, fmt.Errorf("%s.%s.$after: each item is the name of an environment: %w", label, name, err)
					}
					e.After = append(e.After, s)
				}
				if len(e.After) == 0 {
					return nil, fmt.Errorf("%s.%s.$after is an empty list: leave it out for an environment that takes anything", label, name)
				}
			default:
				return nil, fmt.Errorf("%s.%s.$after is the name of an environment, or a list of them", label, name)
			}
		}
		if d := row.LookupPath(cue.MakePath(cue.Str("$default"))); d.Exists() {
			b, err := d.Bool()
			if err != nil {
				return nil, fmt.Errorf("%s.%s.$default: %w", label, name, err)
			}
			e.Default = b
			if b {
				defaults = append(defaults, name)
			}
		}
		index[name] = len(envs)
		envs = append(envs, e)
	}
	if len(envs) == 0 {
		return nil, fmt.Errorf("%s declares no environment", label)
	}
	if len(defaults) > 1 {
		return nil, fmt.Errorf("%s: %s are each marked $default: true, and one is the most", label, joinAnd(defaults))
	}
	for _, e := range envs {
		seen := map[string]bool{}
		for _, a := range e.After {
			if a == e.Name {
				return nil, fmt.Errorf("%s.%s.$after names %s itself", label, e.Name, e.Name)
			}
			if _, ok := index[a]; !ok {
				return nil, fmt.Errorf("%s.%s.$after names %s, which is not an environment (they are %s)", label, e.Name, strconv.Quote(a), joinAnd(names(envs)))
			}
			if seen[a] {
				return nil, fmt.Errorf("%s.%s.$after names %s twice", label, e.Name, a)
			}
			seen[a] = true
		}
	}
	ordered, err := ladderOrder(envs)
	if err != nil {
		return nil, err
	}
	return ordered, nil
}

// ladderOrder sorts the environments so that each comes after every one it follows, and
// otherwise keeps the order they were written in. A circle is an error that names it.
func ladderOrder(envs []Environment) ([]Environment, error) {
	placed := map[string]bool{}
	var out []Environment
	for len(out) < len(envs) {
		progress := false
		for _, e := range envs {
			if placed[e.Name] {
				continue
			}
			ready := true
			for _, a := range e.After {
				if !placed[a] {
					ready = false
				}
			}
			if ready {
				placed[e.Name] = true
				out = append(out, e)
				progress = true
			}
		}
		if !progress {
			return nil, fmt.Errorf("$environments: %s follow one another in a circle through $after, so none of them can be first", joinAnd(circleOf(envs, placed)))
		}
	}
	return out, nil
}

// circleOf finds a circle among the environments that are not placed: an environment that
// cannot be placed follows one that cannot either, so the walk from any of them comes back to
// an environment it has seen. Those after the circle, which wait for it, are left out. The
// names are in the order they were written in.
func circleOf(envs []Environment, placed map[string]bool) []string {
	byName := map[string]Environment{}
	for _, e := range envs {
		byName[e.Name] = e
	}
	var path []string
	seen := map[string]int{}
	for _, e := range envs {
		if placed[e.Name] {
			continue
		}
		for cur := e.Name; ; {
			if i, ok := seen[cur]; ok {
				in := map[string]bool{}
				for _, n := range path[i:] {
					in[n] = true
				}
				var out []string
				for _, e := range envs {
					if in[e.Name] {
						out = append(out, e.Name)
					}
				}
				return out
			}
			seen[cur] = len(path)
			path = append(path, cur)
			next := ""
			for _, a := range byName[cur].After {
				if !placed[a] {
					next = a
					break
				}
			}
			if next == "" {
				break
			}
			cur = next
		}
		break
	}
	return path
}

func names(envs []Environment) []string {
	out := make([]string, len(envs))
	for i, e := range envs {
		out[i] = e.Name
	}
	return out
}

// joinAnd writes a list as a sentence does: "a", "a and b", "a, b and c".
func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// selectEnvironment picks the environment of a run: the one -t env= names, else the
// default row. A name that is not declared, and a run that names none where no row is the
// default, are errors that list the environments.
func selectEnvironment(envs []Environment, name string, given bool) (string, error) {
	if given {
		for _, e := range envs {
			if e.Name == name {
				return name, nil
			}
		}
		if name == "" {
			return "", fmt.Errorf("-t env= names no environment; the environments are %s", joinAnd(names(envs)))
		}
		return "", fmt.Errorf("%s is not an environment of this specification; the environments are %s (pass -t env=NAME)", strconv.Quote(name), joinAnd(names(envs)))
	}
	for _, e := range envs {
		if e.Default {
			return e.Name, nil
		}
	}
	return "", fmt.Errorf("name the environment with -t env=NAME: this specification declares %s, and none is marked $default: true", joinAnd(names(envs)))
}

// environmentDecl is the declaration that fills $environment with the row of name.
func environmentDecl(name string) string {
	q := strconv.Quote(name)
	return "$environment: {$environments[" + q + "], $name: " + q + "}\n"
}

// environmentFile is the file laid over the package that holds environmentDecl.
func environmentFile(pkg, name string) string {
	return "// Filled in by pravo: the environment of this run.\n\npackage " + pkg + "\n\n" + environmentDecl(name)
}

// environmentDecls parses environmentDecl as declarations.
func environmentDecls(name string) ([]ast.Decl, error) {
	f, err := parser.ParseFile("environment", environmentDecl(name))
	if err != nil {
		return nil, err
	}
	return f.Decls, nil
}

// Env returns the row of the named environment.
func (r *Root) Env(name string) (Environment, bool) {
	for _, e := range r.Environments {
		if e.Name == name {
			return e, true
		}
	}
	return Environment{}, false
}

// Current is the row of the environment the root was loaded for.
func (r *Root) Current() (Environment, bool) {
	if r.Environment == "" {
		return Environment{}, false
	}
	return r.Env(r.Environment)
}

// Followers lists the environments whose $after names env, in ladder order.
func (r *Root) Followers(env string) []string {
	var out []string
	for _, e := range r.Environments {
		for _, a := range e.After {
			if a == env {
				out = append(out, e.Name)
			}
		}
	}
	return out
}

// EnvironmentNames lists the environments in ladder order.
func (r *Root) EnvironmentNames() []string { return names(r.Environments) }

// LadderText draws the order of the environments on one line, as "dev → staging → prod",
// with the branches of a fan-out in braces: "dev → staging → {prod-eu, prod-us}". It is
// empty for a root without environments.
func LadderText(envs []Environment) string {
	if len(envs) == 0 {
		return ""
	}
	// Each environment sits at the depth of the longest chain that leads to it.
	depth := map[string]int{}
	maxDepth := 0
	for _, e := range envs {
		d := 0
		for _, a := range e.After {
			if depth[a]+1 > d {
				d = depth[a] + 1
			}
		}
		depth[e.Name] = d
		if d > maxDepth {
			maxDepth = d
		}
	}
	steps := make([][]string, maxDepth+1)
	for _, e := range envs {
		steps[depth[e.Name]] = append(steps[depth[e.Name]], e.Name)
	}
	parts := make([]string, len(steps))
	for i, s := range steps {
		if len(s) == 1 {
			parts[i] = s[0]
		} else {
			parts[i] = "{" + strings.Join(s, ", ") + "}"
		}
	}
	return strings.Join(parts, " → ")
}

// EnvironmentSettings is where one environment keeps its state, and who may read it, as
// $settings says when the specification is read for that environment.
type EnvironmentSettings struct {
	Environment
	State StateConfig
	// StatePath is the state directory of a local backend, absolute.
	StatePath string
	// AccessDir is $settings.access.dir, as written; empty when unset.
	AccessDir string
	// Err says why the settings of the environment cannot be read here, such as a
	// pravo.#Env that this job does not set. The other fields are then empty.
	Err error
}

// Location names the state of the environment the way two environments are compared: the
// bucket and prefix of S3, the URL of a server, or the directory.
func (e EnvironmentSettings) Location() string {
	switch e.State.Backend {
	case "s3":
		return "s3://" + e.State.Bucket + "/" + strings.Trim(e.State.Prefix, "/") + "@" + e.State.Endpoint
	case "http":
		return strings.TrimRight(e.State.URL, "/")
	}
	return filepath.Clean(e.StatePath)
}

// Where writes the location of the state for a person.
func (e EnvironmentSettings) Where() string {
	switch e.State.Backend {
	case "s3":
		w := "s3://" + e.State.Bucket
		if p := strings.Trim(e.State.Prefix, "/"); p != "" {
			w += "/" + p
		}
		return w
	case "http":
		return e.State.URL
	}
	return e.StatePath
}

// ReadEnvironmentSettings reads $settings once for each environment that the
// specification in dir declares, without the imports, which is cheap. tags are the tags of
// the run; the selector in them is replaced for each environment. A specification
// without environments gives none.
func ReadEnvironmentSettings(dir string, opts LoadOptions) ([]EnvironmentSettings, error) {
	opts.declaredOnly = true
	src, err := readSettingsSource(dir, opts)
	if err != nil {
		return nil, err
	}
	return readEnvironmentSettings(src.abs, src.envs, opts), nil
}

func readEnvironmentSettings(abs string, envs []Environment, opts LoadOptions) []EnvironmentSettings {
	out := make([]EnvironmentSettings, 0, len(envs))
	for _, e := range envs {
		o := opts
		o.Tags, o.environment, o.Extra, o.declaredOnly = WithEnvironment(opts.Tags, e.Name), "", nil, false
		es := EnvironmentSettings{Environment: e}
		r, notes, err := loadSettings(abs, o, false)
		if err != nil {
			es.Err = notes.after(err)
		} else {
			es.State, es.StatePath, es.AccessDir = r.State, r.StateFile(), r.AccessDir
		}
		out = append(out, es)
	}
	return out
}

// checkEnvironments reads the settings of every environment and refuses two that keep
// their state in one place: an apply of one would write over the other. An environment
// whose settings cannot be read here (a pravo.#Env that this job does not set) is left
// out; "pravo validate" reports it.
func (r *Root) checkEnvironments(opts LoadOptions) error {
	all := readEnvironmentSettings(r.Dir, r.Environments, opts)
	seen := map[string]string{}
	for _, e := range all {
		if e.Err != nil {
			continue
		}
		loc := e.Location()
		if other, ok := seen[loc]; ok {
			where := e.Where()
			if rel, err := filepath.Rel(r.Dir, where); err == nil && e.State.Backend != "s3" && e.State.Backend != "http" && !strings.HasPrefix(rel, "..") {
				where = filepath.ToSlash(rel)
			}
			return fmt.Errorf("the environments %s and %s keep their state in one place, %s: each environment needs a state of its own, so write the name into $settings.state, such as path: \"state/\\($environment.$name)\" or prefix: \"app/\\($environment.$name)\"", other, e.Name, where)
		}
		seen[loc] = e.Name
	}
	return nil
}
