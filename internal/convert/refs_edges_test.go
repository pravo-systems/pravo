package convert

import (
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/cuecontext"
)

// edgeSets holds the tasks that an analysis found for one task, and where it
// found each: below an operand of a reference that it followed, or elsewhere.
type edgeSets struct {
	all         map[string]bool
	fromOperand map[string]bool
	fromOther   map[string]bool
}

// dependenciesFollowingOperands is Dependencies with one difference: the
// target of an operand of a reference, the container in ports[z].result, is
// validated and followed like the target of a reference of its own. It takes
// every task below such a container as a source, so its result is a superset
// of the result of Dependencies. It is the reference for the differential
// tests below.
func dependenciesFollowingOperands(v cue.Value, self cue.Path, resolve Resolver) edgeSets {
	out := edgeSets{all: map[string]bool{}, fromOperand: map[string]bool{}, fromOther: map[string]bool{}}
	var visit func(v cue.Value, depth int)
	visit = func(v cue.Value, depth int) {
		if depth > 32 {
			return
		}
		viaOperand := 0
		note := func(task cue.Path) {
			out.all[task.String()] = true
			if viaOperand > 0 {
				out.fromOperand[task.String()] = true
			} else {
				out.fromOther[task.String()] = true
			}
		}
		analyseFollowingOperands(v, resolve, &self, &viaOperand, note)
		if _, p := v.ReferencePath(); len(p.Selectors()) > 0 {
			return
		}
		switch v.IncompleteKind() {
		case cue.StructKind:
			if it, err := v.Fields(); err == nil {
				for it.Next() {
					visit(it.Value(), depth+1)
				}
			}
		case cue.ListKind:
			if it, err := v.List(); err == nil {
				for it.Next() {
					visit(it.Value(), depth+1)
				}
			}
		}
	}
	visit(v, 0)
	return out
}

// analyseFollowingOperands is AnalyseWith with the difference described at
// dependenciesFollowingOperands. viaOperand is above zero while the walk is
// below the target of an operand.
func analyseFollowingOperands(v cue.Value, resolve Resolver, self *cue.Path, viaOperand *int, note func(task cue.Path)) {
	seen := map[string]bool{}
	tasks := map[string]bool{}
	var incomplete bool
	addTask := func(task, alias cue.Path) {
		if self != nil && task.String() == self.String() {
			return
		}
		note(task)
		tasks[task.String()] = true
	}
	scanned := map[ast.Node]bool{}
	letSeen := map[*ast.LetClause]bool{}
	loopSeen := map[*ast.ForClause]bool{}
	var scanLet func(lc *ast.LetClause)
	var walk func(v cue.Value, depth int, nav bool)
	follow := func(p cue.Path, depth int) bool {
		target := resolve.Value(p)
		if !target.Exists() {
			return false
		}
		if key := "follow " + p.String(); !seen[key] {
			seen[key] = true
			if !IsConcrete(target) {
				walk(target, depth+1, false)
			}
		}
		return true
	}
	scanLet = func(lc *ast.LetClause) {
		if letSeen[lc] || lc.Expr == nil {
			return
		}
		letSeen[lc] = true
		var anc []cue.Selector
		if self != nil {
			anc = self.Selectors()
		}
		sc := &refScan{resolve: resolve, anc: anc, add: addTask, follow: func(p cue.Path) bool { return follow(p, 0) }, local: map[string]bool{}, let: scanLet}
		ast.Walk(lc.Expr, sc.visit, nil)
	}
	walk = func(v cue.Value, depth int, nav bool) {
		if depth > 64 {
			return
		}
		if lc := letReference(v.Source()); lc != nil {
			scanLet(lc)
			return
		}
		if fc := loopReference(v.Source()); fc != nil && fc.Source != nil {
			if !loopSeen[fc] {
				loopSeen[fc] = true
				var anc []cue.Selector
				if self != nil {
					anc = self.Selectors()
				}
				sc := &refScan{resolve: resolve, anc: anc, add: addTask, follow: func(p cue.Path) bool { return follow(p, 0) }, local: map[string]bool{}, let: scanLet}
				ast.Walk(fc.Source, sc.visit, nil)
			}
			return
		}
		root, p := v.ReferencePath()
		isRef := root.Exists() && len(p.Selectors()) > 0
		if isRef {
			key := p.String()
			if nav {
				key = "operand " + key
			}
			if !seen[key] {
				seen[key] = true
				if task, alias, ok := resolve.Task(p); ok {
					addTask(task, alias)
				} else if self != nil && isPrefixPath(p, *self) {
				} else {
					var below []cue.Path
					if !nav {
						below = resolve.Below(p)
					}
					for _, t := range below {
						addTask(t, t)
					}
					target := root.LookupPath(p)
					if target.Exists() {
						if d, _ := target.Default(); d.Validate(cue.Concrete(true)) != nil {
							before := len(tasks)
							if nav {
								*viaOperand++
							}
							walk(target, depth+1, false)
							if nav {
								*viaOperand--
							}
							if len(below) == 0 && len(tasks) == before && !incomplete {
								incomplete = true
							}
						}
					}
				}
			}
		}
		op, args := v.Expr()
		if op == cue.NoOp {
			scanComprehensions(v.Source(), resolve, self, scanned, addTask, func(p cue.Path) bool { return follow(p, depth) }, scanLet)
			switch {
			case v.IncompleteKind() == cue.ListKind:
				if it, err := v.List(); err == nil {
					for it.Next() {
						walk(it.Value(), depth+1, false)
					}
				}
			case v.IncompleteKind() == cue.StructKind && depth > 0:
				if it, err := v.Fields(); err == nil {
					for it.Next() {
						walk(it.Value(), depth+1, false)
					}
				}
			}
			return
		}
		for _, a := range args {
			walk(a, depth+1, isRef)
		}
	}
	walk(v, 0, false)
}

// checkEdgeDifferential compares the tasks that Dependencies finds for the
// value at self with the tasks found when operands are followed too. Nothing
// may be found that following does not find, and every task that is not found
// had operands as its only source.
func checkEdgeDifferential(t *testing.T, name string, v cue.Value, self cue.Path, resolve Resolver) {
	t.Helper()
	deps, _ := Dependencies(v, self, resolve)
	got := map[string]bool{}
	for _, d := range deps {
		got[d.String()] = true
	}
	old := dependenciesFollowingOperands(v, self, resolve)
	for d := range got {
		if !old.all[d] {
			t.Errorf("%s: %s: Dependencies finds %s, which following operands does not", name, self, d)
		}
	}
	for d := range old.all {
		if !got[d] && (!old.fromOperand[d] || old.fromOther[d]) {
			t.Errorf("%s: %s: Dependencies lacks %s, which has a source other than an operand", name, self, d)
		}
	}
}

func sortedKeys(m map[string]bool) string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// The container in ports[z].result is the way to the entry, not a source of
// the task. The task depends on the entry that the reference names.
func TestNavigationOperandIsNotFollowed(t *testing.T) {
	const src = `
zones: ["a", "b", "c"]
l0: {for z in zones {"\(z)": {result: string | null}}}
l1: {for z in zones {"\(z)": {result: string | null, in: l0[z].result}}}
l2: {for z in zones {"\(z)": {in: l1[z].result}}}
`
	tasks := []string{"l0.a", "l0.b", "l0.c", "l1.a", "l1.b", "l1.c", "l2.a", "l2.b", "l2.c"}
	v := cuecontext.New().CompileString(src)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	resolve := taskPaths{paths: tasks, root: v}
	edges := 0
	for _, task := range tasks {
		self := cue.ParsePath(task)
		deps, _ := Dependencies(v.LookupPath(self), self, resolve)
		var got []string
		for _, d := range deps {
			got = append(got, d.String())
		}
		layer, key, _ := strings.Cut(task, ".")
		want := map[string]string{"l0": "", "l1": "l0." + key, "l2": "l1." + key}[layer]
		if strings.Join(got, ",") != want {
			t.Errorf("%s depends on %q, want %q", task, strings.Join(got, ","), want)
		}
		edges += len(got)
		checkEdgeDifferential(t, "layers", v, self, resolve)
	}
	if edges != 6 {
		t.Errorf("%d edges, want 6", edges)
	}
	// Following the operands makes each task depend on the whole layer below
	// it and on everything below that.
	self := cue.ParsePath("l2.a")
	if old := dependenciesFollowingOperands(v.LookupPath(self), self, resolve); len(old.all) <= 1 {
		t.Errorf("following operands finds %s for l2.a, which is not more than the entry; the fixture shows nothing", sortedKeys(old.all))
	}
}

// A namespace that holds tasks, such as a provider's definitions, is not
// read when a reference goes through it.
func TestNavigationThroughANamespaceReadsNothingOfIt(t *testing.T) {
	const src = `
other: {id: string | null}
ns: {R: {T: {name: string | *"x"}, link: other.id}}
self: {kind: ns.R.T}
`
	v := cuecontext.New().CompileString(src)
	self := cue.ParsePath("self")
	resolve := taskPaths{paths: []string{"other", "self"}, root: v}
	deps, _ := Dependencies(v.LookupPath(self), self, resolve)
	if len(deps) != 0 {
		t.Errorf("self depends on %v, want nothing: ns.R.T holds no task", deps)
	}
	if old := dependenciesFollowingOperands(v.LookupPath(self), self, resolve); !old.all["other"] {
		t.Errorf("following operands finds %s, want other: the fixture shows nothing", sortedKeys(old.all))
	}
	checkEdgeDifferential(t, "namespace", v, self, resolve)
}

// Fixtures of the engine tests of containers of resources, as sources of the
// analysis: the differential holds for each of them.
func TestFollowingOperandsAddsOnlyOperandEdges(t *testing.T) {
	cases := []struct {
		name, src string
		tasks     []string
		selves    []string
	}{
		{"struct of resources", `pets: {a: {id: string | null}, b: {id: string | null}}
all: {content: [for k, v in pets {v.id}]}`,
			[]string{"pets.a", "pets.b", "all"}, []string{"all"}},
		{"struct inside a plain field", `zoo: {label: "x", pets: {a: {id: string | null}, b: {id: string | null}}}
all: {content: [for k, v in zoo.pets {v.id}]}`,
			[]string{"zoo.pets.a", "zoo.pets.b", "all"}, []string{"all"}},
		{"plain field above the container", `zoo: {label: "x", pets: {a: {id: string | null}, b: {id: string | null}}}
all: {content: [for k, v in zoo.pets {"\(k)/\(v.id)"}]}`,
			[]string{"zoo.pets.a", "zoo.pets.b", "all"}, []string{"all"}},
		{"list of resources", `pets: [{id: string | null}, {id: string | null}]
all: {content: [for v in pets {v.id}]}`,
			[]string{"pets[0]", "pets[1]", "all"}, []string{"all"}},
		{"reference to a container in an expression", `import "encoding/json"
pets: {a: {id: string | null}, b: {id: string | null}}
dump: {content: json.Marshal(pets)}`,
			[]string{"pets.a", "pets.b", "dump"}, []string{"dump"}},
		{"plain field built from resources", `a: {id: string | null}
b: {id: string | null}
ids: {x: a.id, y: b.id}
all: {content: [for k, v in ids {v}]}`,
			[]string{"a", "b", "all"}, []string{"all"}},
		{"indexed entry of a container", `zones: ["a", "b", "c"]
ports: {for z in zones {"\(z)": {id: string | null}}}
deploy: {for z in zones {"\(z)": {content: "\(ports[z].id)"}}}`,
			[]string{"ports.a", "ports.b", "ports.c", "deploy.a", "deploy.b", "deploy.c"}, []string{"deploy.a", "deploy.b", "deploy.c"}},
		{"index by a value of another task", `names: {x: {id: string | null}, y: {id: string | null}}
key: {name: string | null}
use: {content: names[key.name].id}`,
			[]string{"names.x", "names.y", "key", "use"}, []string{"use"}},
		{"index by a plain field", `names: {x: {id: string | null}, y: {id: string | null}}
pick: "x"
use: {content: names[pick].id}`,
			[]string{"names.x", "names.y", "use"}, []string{"use"}},
		{"index by a value that is not known", `names: {x: {id: string | null}, y: {id: string | null}}
key: {name: string | null}
use: {content: names["\(key.name)"].id}`,
			[]string{"names.x", "names.y", "key", "use"}, []string{"use"}},
		{"a module", `mod: {pets: {x: {id: string | null}, y: {id: string | null}}, file: {names: [for k, v in pets {v.id}]}}`,
			[]string{"mod.pets.x", "mod.pets.y", "mod.file"}, []string{"mod.file"}},
	}
	for _, c := range cases {
		v := cuecontext.New().CompileString(c.src)
		if err := v.Err(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		resolve := taskPaths{paths: c.tasks, root: v}
		for _, task := range c.selves {
			checkEdgeDifferential(t, c.name, v.LookupPath(cue.ParsePath(task)), cue.ParsePath(task), resolve)
		}
	}
}
