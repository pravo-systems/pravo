package convert

import (
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// taskPaths is a Resolver over a list of task paths. A path inside a task
// resolves to the task; the tasks whose paths extend a path are below it.
type taskPaths struct {
	paths []string
	// root is the specification, for the plain fields a comprehension names.
	root cue.Value
}

func (ts taskPaths) Value(p cue.Path) cue.Value {
	if !ts.root.Exists() {
		return cue.Value{}
	}
	return ts.root.LookupPath(p)
}

func (ts taskPaths) Task(p cue.Path) (cue.Path, cue.Path, bool) {
	sels := p.Selectors()
	for _, tp := range ts.paths {
		want := cue.ParsePath(tp).Selectors()
		if len(sels) < len(want) {
			continue
		}
		match := true
		for i := range want {
			match = match && sels[i].String() == want[i].String()
		}
		if match {
			t := cue.MakePath(sels[:len(want)]...)
			return t, t, true
		}
	}
	return cue.Path{}, cue.Path{}, false
}

func (ts taskPaths) Below(p cue.Path) []cue.Path {
	sels := p.Selectors()
	var out []cue.Path
	for _, tp := range ts.paths {
		have := cue.ParsePath(tp).Selectors()
		if len(have) <= len(sels) {
			continue
		}
		match := true
		for i := range sels {
			match = match && sels[i].String() == have[i].String()
		}
		if match {
			out = append(out, cue.ParsePath(tp))
		}
	}
	return out
}

// taskSet resolves a path to the task it lies in or on, among the given task
// paths, and lists the tasks below a path that holds some.
func taskSet(paths ...string) Resolver { return taskPaths{paths: paths} }

// depsOf lists the tasks that the task at path refers to.
func depsOf(t *testing.T, src, path string, tasks ...string) string {
	t.Helper()
	v := cuecontext.New().CompileString(src)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	resolve := taskPaths{paths: tasks, root: v}
	checkEdgeDifferential(t, "depsOf", v.LookupPath(cue.ParsePath(path)), cue.ParsePath(path), resolve)
	deps, _ := Dependencies(v.LookupPath(cue.ParsePath(path)), cue.ParsePath(path), resolve)
	var out []string
	for _, d := range deps {
		out = append(out, d.String())
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// A reference in the source of a comprehension is a dependency, though the
// comprehension is not resolved until the task it refers to has run.
func TestComprehensionSourceIsADependency(t *testing.T) {
	const head = `
import "strings"
platform: {outputs: {...}}
pet: {id: string | null}
unused: strings.ToLower("X")
`
	cases := []struct {
		name, body, want string
	}{
		{"list in a call", `content: strings.Join([for k, v in platform.outputs.subnets {v}], ",")`, "platform"},
		{"struct", `tags: {for k, v in platform.outputs.subnets {"\(k)": v}}`, "platform"},
		{"list", `names: [for k, v in platform.outputs.subnets {k}]`, "platform"},
		{"nested", `content: strings.Join([for k, v in {for kk, vv in platform.outputs.subnets {"\(kk)": vv}} {v}], ",")`, "platform"},
		{"quoted selector", `names: [for k, v in platform.outputs["api.url"] {k}]`, "platform"},
		{"if clause", `names: [for k in ["a"] if platform.outputs.on {k}]`, "platform"},
		{"let clause", `names: [for k in ["a"] let p = platform.outputs.name {p}]`, "platform"},
		{"body", `names: [for k in ["a", "b"] {"\(k)-\(pet.id)"}]`, "pet"},
		{"two tasks", `names: [for k, v in platform.outputs.subnets {"\(k)-\(pet.id)"}]`, "pet,platform"},
		{"index with a reference", `names: [for k in platform.outputs.list {k}][0]`, "platform"},
		{"no task", `names: [for k, v in {a: 1} {k}]`, ""},
		{"loop variable", `names: [for pet in ["a", "b"] {pet}]`, ""},
		{"loop variable shadows a task", `names: [for platform in [{x: 1}] {platform.x}]`, ""},
		{"local field", `names: [for k in ["a"] {pet: k, out: pet}]`, ""},
		{"the task itself", `content: "x", names: [for k in [self.content] {k}]`, ""},
	}
	for _, c := range cases {
		src := head + "self: {" + c.body + "}\n"
		if got := depsOf(t, src, "self", "platform", "pet", "self"); got != c.want {
			t.Errorf("%s: dependencies = %q, want %q", c.name, got, c.want)
		}
	}
}

// The structs around the task are searched from the innermost outwards.
func TestComprehensionSourceInsideAModule(t *testing.T) {
	const src = `
platform: {outputs: {...}}
mod: {
	net: {outputs: {...}}
	file: {names: [for k, v in net.outputs.subnets {k}]}
	other: {names: [for k, v in platform.outputs.subnets {k}]}
}
`
	if got := depsOf(t, src, "mod.file", "platform", "mod.net", "mod.file", "mod.other"); got != "mod.net" {
		t.Errorf("mod.file depends on %q, want mod.net", got)
	}
	if got := depsOf(t, src, "mod.other", "platform", "mod.net", "mod.file", "mod.other"); got != "platform" {
		t.Errorf("mod.other depends on %q, want platform", got)
	}
}

// A name that the task's own struct declares is that field, whatever a task
// of the same name elsewhere is.
func TestComprehensionNameInsideTheTaskIsNotATask(t *testing.T) {
	const src = `
port: {id: string | null}
web: {
	port:  80
	names: [for k in ["a"] {"\(k)\(port)"}]
}
mod: {
	port:  {id: string | null}
	inner: {names: [for k in ["a"] {"\(k)\(port.id)"}]}
}
`
	if got := depsOf(t, src, "web", "port", "web", "mod.port", "mod.inner"); got != "" {
		t.Errorf("web depends on %q, want nothing: port is its own field", got)
	}
	if got := depsOf(t, src, "mod.inner", "port", "web", "mod.port", "mod.inner"); got != "mod.port" {
		t.Errorf("mod.inner depends on %q, want mod.port", got)
	}
}

// With the dependency known, a value built from a comprehension is unknown
// until the task has run, and not an error.
func TestComprehensionOverATaskIsUnknownBeforeItRuns(t *testing.T) {
	v := cuecontext.New().CompileString(`
import "strings"
platform: {outputs: {...}}
file: {
	filename: "x"
	content:  strings.Join([for k, v in platform.outputs.subnets {v}], ",")
}
`)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	c := &Converter{Resolve: taskSet("platform", "file"), Self: cue.ParsePath("file")}
	got := c.Block("file", fileBlock(), v.LookupPath(cue.ParsePath("file")))
	if err := c.Err(); err != nil {
		t.Fatalf("a comprehension over a task was rejected: %v", err)
	}
	var m map[string]tftypes.Value
	_ = got.As(&m)
	if m["content"].IsKnown() {
		t.Errorf("content should be unknown, got %s", m["content"])
	}
	if d := c.DepAddrs(); len(d) != 1 || d[0] != "platform" {
		t.Errorf("deps = %v, want [platform]", d)
	}
}

// A name that stands for a struct or a list that holds tasks, and is not a
// task itself, is a dependency on every task below it.
func TestReferenceToAContainerOfTasksIsEveryTaskInIt(t *testing.T) {
	const head = `
import "encoding/json"
pets: {a: {id: string | null}, b: {id: string | null}}
zoo:  {label: "x", pets: {a: {id: string | null}, b: {id: string | null}}}
list: [{id: string | null}, {id: string | null}]
other: {id: string | null}
ids:    {x: pets.a.id, y: pets.b.id}
nested: {inner: ids, label: "x"}
consts: {x: "1"}
unused: json.Marshal({})
`
	tasks := []string{"pets.a", "pets.b", "zoo.pets.a", "zoo.pets.b", "list[0]", "list[1]", "other", "self"}
	cases := []struct {
		name, body, want string
	}{
		{"for over the container", `names: [for k, v in pets {v.id}]`, "pets.a,pets.b"},
		{"for over a field of a plain struct", `names: [for k, v in zoo.pets {v.id}]`, "zoo.pets.a,zoo.pets.b"},
		{"for over the plain struct", `names: [for k, v in zoo {k}]`, "zoo.pets.a,zoo.pets.b"},
		{"for over a list", `names: [for v in list {v.id}]`, "list[0],list[1]"},
		{"struct built from the container", `tags: {for k, v in pets {"\(k)": v.id}}`, "pets.a,pets.b"},
		{"call over the comprehension", `content: json.Marshal([for k, v in pets {v.id}])`, "pets.a,pets.b"},
		{"container in an if clause", `names: [for k in ["x"] if len(pets) > 0 {k}]`, "pets.a,pets.b"},
		{"container in the body", `names: [for k in ["x"] {"\(k)-\(len(pets))"}]`, "pets.a,pets.b"},
		{"dynamic index", `names: [for k in ["a", "b"] {pets[k].id}]`, "pets.a,pets.b"},
		{"plain reference", `content: json.Marshal(pets)`, "pets.a,pets.b"},
		{"plain reference to a plain struct", `content: json.Marshal(zoo)`, "zoo.pets.a,zoo.pets.b"},
		{"plain reference to a list", `content: json.Marshal(list)`, "list[0],list[1]"},
		{"one task and a container", `names: [for k, v in pets {"\(k)\(other.id)\(v.id)"}]`, "other,pets.a,pets.b"},
		{"one task of a container", `names: [for k, v in {x: pets.a} {v.id}]`, "pets.a"},
		{"plain field built from tasks", `names: [for k, v in ids {v}]`, "pets.a,pets.b"},
		{"plain field inside a plain field", `names: [for k, v in nested.inner {v}]`, "pets.a,pets.b"},
		{"plain field that holds a plain field", `names: [for k, v in nested {k}]`, "pets.a,pets.b"},
		{"plain field of constants", `names: [for k, v in consts {v}]`, ""},
		{"loop variable shadows the container", `names: [for pets in [{id: "x"}] {pets.id}]`, ""},
		{"a container without tasks", `names: [for k, v in {a: 1} {k}]`, ""},
	}
	for _, c := range cases {
		src := head + "self: {" + c.body + "}\n"
		got := depsOf(t, src, "self", tasks...)
		if got != c.want {
			t.Errorf("%s: dependencies = %q, want %q", c.name, got, c.want)
		}
	}
}

// A task that iterates the struct it sits in does not depend on the tasks
// beside it, as for a reference in the expression tree. A struct that is not
// the one it sits in, even if it sits in the same module, is every task in it.
func TestIteratingTheStructOfTheTaskAddsNoDependency(t *testing.T) {
	const src = `
pets: {
	a: {id: string | null}
	b: {names: [for k, v in pets {k}], ids: [for k, v in zoo {v.id}]}
	c: {id: string | null}
}
zoo: {x: {id: string | null}}
`
	tasks := []string{"pets.a", "pets.b", "pets.c", "zoo.x"}
	if got := depsOf(t, src, "pets.b", tasks...); got != "zoo.x" {
		t.Errorf("pets.b depends on %q, want zoo.x", got)
	}
}

// Inside a module the name belongs to the nearest struct that has a container
// of that name.
func TestContainerInsideAModule(t *testing.T) {
	const src = `
pets: {a: {id: string | null}}
mod: {
	pets: {x: {id: string | null}, y: {id: string | null}}
	file: {names: [for k, v in pets {v.id}]}
}
`
	tasks := []string{"pets.a", "mod.pets.x", "mod.pets.y", "mod.file"}
	if got := depsOf(t, src, "mod.file", tasks...); got != "mod.pets.x,mod.pets.y" {
		t.Errorf("mod.file depends on %q, want mod.pets.x,mod.pets.y", got)
	}
}

// With the container known to hold tasks, a value that iterates it is unknown
// until they have run.
func TestComprehensionOverAContainerIsUnknownBeforeItRuns(t *testing.T) {
	v := cuecontext.New().CompileString(`
import "strings"
pets: {a: {id: string | null}, b: {id: string | null}}
file: {
	filename: "x"
	content:  strings.Join([for k, v in pets {v.id}], ",")
}
`)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	c := &Converter{Resolve: taskSet("pets.a", "pets.b", "file"), Self: cue.ParsePath("file")}
	got := c.Block("file", fileBlock(), v.LookupPath(cue.ParsePath("file")))
	if err := c.Err(); err != nil {
		t.Fatalf("a comprehension over a container of tasks was rejected: %v", err)
	}
	var m map[string]tftypes.Value
	_ = got.As(&m)
	if m["content"].IsKnown() {
		t.Errorf("content should be unknown, got %s", m["content"])
	}
	if d := strings.Join(c.DepAddrs(), ","); d != "pets.a,pets.b" {
		t.Errorf("deps = %s, want pets.a,pets.b", d)
	}
}

// A field that a comprehension builds for one key reads the entry of that key,
// not the whole container.
func TestIndexedEntryOfAContainerIsOneTask(t *testing.T) {
	const src = `
zones: ["a", "b", "c"]
ports: {
	for z in zones {"\(z)": {result: string | null}}
}
deploy: {
	for z in zones {
		"\(z)": {triggers: {zone: z, port: "\(ports[z].result)"}}
	}
}
`
	tasks := []string{"ports.a", "ports.b", "ports.c", "deploy.a", "deploy.b", "deploy.c"}
	for _, z := range []string{"a", "b", "c"} {
		if got := depsOf(t, src, "deploy."+z, tasks...); got != "ports."+z {
			t.Errorf("deploy.%s depends on %q, want ports.%s", z, got, z)
		}
	}
}

// Reading a plain field of a struct that also holds tasks is not a reference
// to the tasks: only the container itself, named as a value, is.
func TestReadingAPlainFieldOfAContainerIsNotEveryTask(t *testing.T) {
	const head = `
import "encoding/json"
mod: {
	name: "x"
	cfg:  {size: 3}
	net:  {id: string | null}
	file: {id: string | null}
}
unused: json.Marshal({})
`
	tasks := []string{"mod.net", "mod.file", "self"}
	cases := []struct {
		name, body, want string
	}{
		{"a plain field", `content: mod.name`, ""},
		{"a field of a plain field", `content: "\(mod.cfg.size)"`, ""},
		{"a task", `content: mod.net.id`, "mod.net"},
		{"the container as a value", `content: json.Marshal(mod)`, "mod.file,mod.net"},
		{"the container after an entry", `content: json.Marshal([mod.net.id, mod])`, "mod.file,mod.net"},
		{"the container before an entry", `content: json.Marshal([mod, mod.net.id])`, "mod.file,mod.net"},
		{"a plain field in a comprehension", `names: [for k in ["a"] {"\(k)\(mod.name)"}]`, ""},
	}
	for _, c := range cases {
		src := head + "self: {" + c.body + "}\n"
		if got := depsOf(t, src, "self", tasks...); got != c.want {
			t.Errorf("%s: dependencies = %q, want %q", c.name, got, c.want)
		}
	}
}

// A loop variable stands for what the source of its for clause holds: a task
// named in a struct or list the loop walks over is a dependency of the entry
// that reads the variable.
func TestLoopVariableCarriesTheTasksOfItsSource(t *testing.T) {
	const head = `
env: {value: string | *"x"}
pet: {id: string | null}
`
	cases := []struct {
		name, src, path, want string
	}{
		{"struct value", `files: {for k, v in {a: env.value, b: "x"} {"\(k)": {content: v}}}`, "files.a", "env"},
		{"unrelated entry", `files: {for k, v in {a: env.value, b: "x"} {"\(k)": {content: v}}}`, "files.b", "env"},
		{"interpolation", `files: {for k, v in {a: "p-\(pet.id)"} {"\(k)": {content: "got \(v)"}}}`, "files.a", "pet"},
		{"list", `files: {for i, v in [env.value] {"\(i)": {content: v}}}`, "files.\"0\"", "env"},
		{"no task", `files: {for k, v in {a: "x"} {"\(k)": {content: v}}}`, "files.a", ""},
	}
	for _, c := range cases {
		path := c.path
		if got := depsOf(t, head+c.src+"\n", path, "env", "pet"); got != c.want {
			t.Errorf("%s: dependencies = %q, want %q", c.name, got, c.want)
		}
	}
}
