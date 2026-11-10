package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// thingsIn lists the resources of a specification written for these tests:
// every struct that has a name and an id.
func thingsIn(t *testing.T, text string) spec {
	t.Helper()
	v := cuecontext.New().CompileString(text)
	if err := v.Err(); err != nil {
		t.Fatal(err)
	}
	s := spec{cue: text}
	var walk func(v cue.Value, p []cue.Selector)
	walk = func(v cue.Value, p []cue.Selector) {
		switch v.IncompleteKind() {
		case cue.StructKind:
			if v.LookupPath(cue.ParsePath("id")).Exists() && v.LookupPath(cue.ParsePath("name")).Exists() {
				s.resources = append(s.resources, cue.MakePath(p...).String())
				return
			}
			it, err := v.Fields()
			if err != nil {
				return
			}
			for it.Next() {
				walk(it.Value(), append(append([]cue.Selector{}, p...), it.Selector()))
			}
		case cue.ListKind:
			it, err := v.List()
			if err != nil {
				return
			}
			for i := 0; it.Next(); i++ {
				walk(it.Value(), append(append([]cue.Selector{}, p...), cue.Index(i)))
			}
		}
	}
	walk(v, nil)
	return s
}

// edgesOf builds the graph of the specification and returns what each task
// reads, as "a.k0,a.k1". fromSyntax makes it the graph of the syntax alone.
func edgesOf(t *testing.T, text string, fromSyntax bool) (map[string]string, *Engine) {
	t.Helper()
	h := newHarness(t)
	e := h.engine(thingsIn(t, text), Options{})
	e.depsFromSyntax = fromSyntax
	if _, err := e.buildGraph(); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for addr, deps := range e.deps {
		out[addr] = strings.Join(deps, ",")
	}
	return out, e
}

const loopPrelude = `
import (
	"strings"
	"encoding/json"
)

#T: {name: string | *"n", id: string | null, content?: string, tag?: string, t?: _, opt?: string, list: [...string] | null, sub: {x: string | null}, ...}

a: {for j in ["0", "1", "2"] {"k\(j)": #T & {tag: j}}}
_imports: [strings.ToUpper("x"), json.Marshal(1)]
`

// A resource that a comprehension makes for each element of a collection of
// resources reads the element of its own iteration, whatever the form of the
// read, where the syntax alone reads the whole collection.
func TestALoopReadsItsOwnElement(t *testing.T) {
	cases := []struct{ name, cue, addr, want string }{
		{"a field of the element", `b: {for k, v in a {"\(k)": #T & {content: v.id}}}`, "b.k1", "a.k1"},
		{"the key and the value", `b: {for k, v in a {"\(k)": #T & {tag: k, content: v.id}}}`, "b.k1", "a.k1"},
		{"an optional field that is not set", `b: {for k, v in a {"\(k)": #T & {content: v.opt}}}`, "b.k1", "a.k1"},
		{"a field that the element does not have", `b: {for k, v in a {"\(k)": #T & {content: v.missing}}}`, "b.k1", "a.k1"},
		{"a nested field next to a field", `b: {for k, v in a {"\(k)": #T & {content: v.sub.x, tag: v.tag}}}`, "b.k1", "a.k1"},
		{"an interpolation", `b: {for k, v in a {"\(k)": #T & {content: "\(v.id)-x"}}}`, "b.k1", "a.k1"},
		{"the whole element in a call", `b: {for k, v in a {"\(k)": #T & {content: json.Marshal(v)}}}`, "b.k1", "a.k1"},
		{"the whole element as a value", `b: {for k, v in a {"\(k)": #T & {t: v}}}`, "b.k1", "a.k1"},
		{"the element in $depends_on", `b: {for k, v in a {"\(k)": #T & {$depends_on: [v]}}}`, "b.k1", "a.k1"},
		{"an index with a literal", `b: {for k, v in a {"\(k)": #T & {content: v["id"]}}}`, "b.k1", "a.k1"},
		{"inside a list", `b: {for k, v in a {"\(k)": #T & {content: [v.id, "x"][0]}}}`, "b.k1", "a.k1"},
		{"inside a default", `b: {for k, v in a {"\(k)": #T & {content: *v.id | "none"}}}`, "b.k1", "a.k1"},
		{"deep inside the body", `b: {for k, v in a {"\(k)": #T & {t: {deep: {deeper: [v.id]}}}}}`, "b.k1", "a.k1"},
		{"a list of resources", `l: [for j in ["0", "1", "2"] {#T & {tag: j}}], b: [for v in l {#T & {content: v.id}}]`, "b[1]", "l[1]"},
		{"a nested container", `zoo: {pets: {x: #T & {tag: "x"}, y: #T & {tag: "y"}}}, b: {for k, v in zoo.pets {"\(k)": #T & {content: v.id}}}`, "b.y", "zoo.pets.y"},
		{"two resources in one iteration", `b: {for k, v in a {"\(k)": {x: #T & {content: v.id}, y: #T & {content: v.tag}}}}`, "b.k1.y", "a.k1"},
		{"two loops", `c: {for j in ["0", "1"] {"k\(j)": #T & {tag: j}}}, b: {for k, v in a for k2, w in c if k == k2 {"\(k)": #T & {content: v.id + w.id}}}`, "b.k1", "a.k1,c.k1"},
		{"a guard on the element", `b: {for k, v in a if v.tag != "1" {"\(k)": #T & {content: v.id}}}`, "b.k2", "a.k2"},
		{"elements written out", `w: {x: #T & {tag: "x"}, y: #T & {tag: "y"}}, b: {for k, v in w {"\(k)": #T & {content: v.id}}}`, "b.y", "w.y"},
		{"elements that embed a definition", `w: {for j in ["0", "1"] {"k\(j)": {#T}}}, b: {for k, v in w {"\(k)": #T & {content: v.id}}}`, "b.k1", "w.k1"},
		{"a computed index on the variable", `o: #T & {tag: "o"}, b: {for k, v in a {"\(k)": #T & {content: v[o.id]}}}`, "b.k1", "a.k1,o"},
		{"an element next to a direct read of another", `b: {for k, v in a {"\(k)": #T & {content: v.id, tag: a.k0.id}}}`, "b.k1", "a.k0,a.k1"},
		{"the key alone, and the element by the key", `b: {for k, _ in a {"\(k)": #T & {content: a[k].id}}}`, "b.k1", "a.k1"},
		{"a container inside a definition", `#S: {n: string, subnets: {x: #T & {tag: "x"}, y: #T & {tag: "y"}}, for k, v in subnets {"assoc-\(k)": #T & {content: v.id}}}, s1: #S & {n: "1"}, s2: #S & {n: "2"}`, `s2."assoc-y"`, "s2.subnets.y"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, e := edgesOf(t, loopPrelude+c.cue, false)
			if got[c.addr] != c.want {
				t.Errorf("%s reads %q, want %q", c.addr, got[c.addr], c.want)
			}
			if e.loops == nil || e.loops.Narrowed == 0 {
				t.Errorf("no loop was read by element")
			}
			old, _ := edgesOf(t, loopPrelude+c.cue, true)
			if !strings.Contains(old[c.addr], c.want) && !subset(c.want, old[c.addr]) {
				t.Errorf("the syntax alone reads %q for %s, which lacks %q", old[c.addr], c.addr, c.want)
			}
			t.Logf("%s: by element %q, the syntax alone %q", c.addr, got[c.addr], old[c.addr])
		})
	}
}

// subset reports whether every address of the list a is in the list b.
func subset(a, b string) bool {
	have := map[string]bool{}
	for _, x := range strings.Split(b, ",") {
		have[x] = true
	}
	for _, x := range strings.Split(a, ",") {
		if x != "" && !have[x] {
			return false
		}
	}
	return true
}

// Where the evaluator cannot be trusted to name the element, the loop is read
// as the syntax reads it: the whole collection.
func TestALoopIsReadWholeWhereTheElementIsNotCertain(t *testing.T) {
	cases := []struct{ name, cue, addr, want string }{
		{"only the key is read", `b: {for k, _ in a {"\(k)": #T & {content: k}}}`, "b.k1", "a.k0,a.k1,a.k2"},
		{"only a nested field is read", `b: {for k, v in a {"\(k)": #T & {content: v.sub.x}}}`, "b.k1", "a.k0,a.k1,a.k2"},
		{"an element that is only a reference", `w: {for j in ["0", "1"] {"k\(j)": #T}}, b: {for k, v in w {"\(k)": #T & {content: v.id}}}`, "b.k1", "w.k0,w.k1"},
		{"elements written out as references", `w: {x: #T, y: #T}, b: {for k, v in w {"\(k)": #T & {content: v.id}}}`, "b.y", "w.x,w.y"},
		{"an element that holds resources", `z: {g0: {x: #T & {tag: "1"}, y: #T & {tag: "2"}}, g1: {x: #T & {tag: "3"}, y: #T & {tag: "4"}}}, b: {for k, v in z {"\(k)": #T & {content: v.x.id + json.Marshal(v)}}}`, "b.g1", "z.g0.x,z.g0.y,z.g1.x,z.g1.y"},
		{"a plain value among the elements", `w: {x: #T & {tag: "x"}, y: #T & {tag: "y"}, extra: {id: "plain"}}, b: {for k, v in w {"\(k)": #T & {content: v.id}}}`, "b.y", "w.x,w.y"},
		{"the source is a plain field built from resources", `ids: {for k, v in a {"\(k)": v.id}}, b: {for k, v in ids {"\(k)": #T & {content: v}}}`, "b.k1", "a.k0,a.k1,a.k2"},
		{"the source is a let", "let _a = a\n" + `b: {for k, v in _a {"\(k)": #T & {content: v.id}}}`, "b.k1", "a.k0,a.k1,a.k2"},
		{"the source is an expression", `b: {for k, v in {for k2, v2 in a if k2 != "k0" {(k2): v2}} {"\(k)": #T & {content: v.id}}}`, "b.k1", "a.k0,a.k1,a.k2"},
		{"a loop inside the body reads the whole collection", `b: #T & {content: strings.Join([for k, v in a {v.id}], ",")}`, "b", "a.k0,a.k1,a.k2"},
		{"the whole collection in a call", `b: {for k, v in a {"\(k)": #T & {content: v.id, tag: json.Marshal(a)}}}`, "b.k1", "a.k0,a.k1,a.k2"},
		{"the whole collection in $depends_on", `b: {for k, v in a {"\(k)": #T & {content: v.id, $depends_on: [a]}}}`, "b.k1", "a.k0,a.k1,a.k2"},
		{"the resource is declared in a definition", `#S: {src: string | null, out: #T & {content: src}}, b: {for k, v in a {"\(k)": #S & {src: v.id}}}`, "b.k1.out", "a.k0,a.k1,a.k2"},
		{"one resource that every iteration adds to", `for k, v in a {b: #T & {t: "\(k)": v.id}}`, "b", "a.k0,a.k1,a.k2"},
		{"instances of a definition that are only references", `#S: {subnets: {x: #T & {tag: "x"}, y: #T & {tag: "y"}}, for k, v in subnets {"assoc-\(k)": #T & {content: v.id}}}, s1: #S, s2: #S`, `s2."assoc-y"`, "s2.subnets.x,s2.subnets.y"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := edgesOf(t, loopPrelude+c.cue, false)
			if got[c.addr] != c.want {
				t.Errorf("%s reads %q, want %q", c.addr, got[c.addr], c.want)
			}
			old, _ := edgesOf(t, loopPrelude+c.cue, true)
			if !subset(old[c.addr], got[c.addr]) {
				t.Errorf("%s reads %q, which lacks an edge of the syntax alone, %q", c.addr, got[c.addr], old[c.addr])
			}
		})
	}
}

// A loop variable is read wherever the body of the task names it: in a
// condition or a comprehension inside the body, through a let, through a hidden
// field. The syntax alone reads the whole collection for each of them, and the
// graph the element where the body also reads the variable in the plain.
func TestALoopVariableIsReadWhereverTheBodyNamesIt(t *testing.T) {
	const all = "a.k0,a.k1,a.k2"
	cases := []struct{ name, cue, addr, want, syntax string }{
		{"a condition on the element inside the body", `b: {for k, v in a {"\(k)": #T & {if v.tag != "1" {content: "set"}}}}`, "b.k2", all, all},
		{"a condition next to a plain read", `b: {for k, v in a {"\(k)": #T & {tag: v.tag, if v.tag != "1" {content: "set"}}}}`, "b.k2", "a.k2", all},
		{"a loop over a field of the element", `b: {for k, v in a {"\(k)": #T & {content: strings.Join([for s in v.list {s}], ",")}}}`, "b.k1", all, all},
		{"a loop over a field next to a plain read", `b: {for k, v in a {"\(k)": #T & {tag: v.tag, content: strings.Join([for s in v.list {s}], ",")}}}`, "b.k1", "a.k1", all},
		{"a let of the element", `b: {for k, v in a {"\(k)": #T & {let w = v, content: w.id}}}`, "b.k1", all, all},
		{"a let of the element next to a plain read", `b: {for k, v in a {"\(k)": #T & {let w = v, content: w.id, tag: v.tag}}}`, "b.k1", "a.k1", all},
		{"a hidden field of the body", `o: #T & {tag: "o"}, b: #T & {_h: o.id, content: _h}`, "b", "o", "o"},
		{"a hidden field set from the element", `b: {for k, v in a {"\(k)": #T & {_h: v.id, content: _h}}}`, "b.k1", "a.k1", all},
		{"a definition inside the body", `o: #T & {tag: "o"}, b: #T & {#h: o.id, content: #h}`, "b", "o", "o"},
		{"the variable of an outer loop in the source of an inner one", `g: {x: {p: #T & {tag: "p"}}, y: {q: #T & {tag: "q"}}}, b: {for k, v in g for k2, v2 in v {"\(k2)": #T & {content: v2.id}}}`, "b.q", "g.x.p,g.y.q", "g.x.p,g.y.q"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := edgesOf(t, loopPrelude+c.cue, false)
			if got[c.addr] != c.want {
				t.Errorf("%s reads %q, want %q", c.addr, got[c.addr], c.want)
			}
			old, _ := edgesOf(t, loopPrelude+c.cue, true)
			if old[c.addr] != c.syntax {
				t.Errorf("the syntax alone reads %q for %s, want %q", old[c.addr], c.addr, c.syntax)
			}
		})
	}
}

// pairs is a collection of n resources and a second one made by a loop over
// the first, each of whose resources reads its own element.
func pairs(n int) string {
	var b strings.Builder
	b.WriteString("#Thing: {name: string, id: string | null, content?: string}\na: {\n")
	for i := range n {
		fmt.Fprintf(&b, "\tk%03d: #Thing & {name: \"a%03d\"}\n", i, i)
	}
	b.WriteString("}\nb: {for k, v in a {\"\\(k)\": #Thing & {name: \"b-\\(k)\", content: v.id}}}\n")
	return b.String()
}

// A loop over a collection of n resources makes n edges, not n times n, and the
// apply creates each resource after the one it reads, with the value it read.
func TestALoopOverACollectionAppliesByElement(t *testing.T) {
	const n = 60
	text := pairs(n)
	got, e := edgesOf(t, text, false)
	edges := 0
	for _, deps := range got {
		if deps != "" {
			edges += len(strings.Split(deps, ","))
		}
	}
	if edges != n || e.loops.Narrowed != n {
		t.Fatalf("%d edges and %d loops read by element, want %d and %d", edges, e.loops.Narrowed, n, n)
	}
	old, _ := edgesOf(t, text, true)
	was := 0
	for _, deps := range old {
		if deps != "" {
			was += len(strings.Split(deps, ","))
		}
	}
	if was != n*n {
		t.Fatalf("the syntax alone gives %d edges, want %d", was, n*n)
	}
	h := newHarness(t)
	s := thingsIn(t, text)
	h.apply(s)
	created := map[string]int{}
	for i, step := range h.fake.order {
		created[strings.TrimPrefix(step, "create ")] = i
	}
	for i := range n {
		name := fmt.Sprintf("%03d", i)
		if created["b-k"+name] < created["a"+name] {
			t.Errorf("b.k%s was created before a.k%s", name, name)
		}
		rec := h.state.Get("b.k" + name)
		src := h.state.Get("a.k" + name)
		if rec == nil || src == nil {
			t.Fatalf("k%s is not in the state", name)
		}
		var attrs struct{ ID string }
		if err := json.Unmarshal(src.Attributes, &attrs); err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("%q", attrs.ID); !strings.Contains(string(rec.Attributes), `"content":`+want) {
			t.Errorf("b.k%s holds %s, want the id of a.k%s (%s)", name, rec.Attributes, name, want)
		}
		if len(rec.Dependencies) != 1 || rec.Dependencies[0] != "a.k"+name {
			t.Errorf("the record of b.k%s reads %v, want a.k%s alone", name, rec.Dependencies, name)
		}
	}
	// Nothing is left to do, and a change of one element reaches one resource.
	p := plan(t, h.engine(s, Options{}))
	if p.HasChanges() {
		t.Fatalf("the plan after the apply is not empty: %v", p.Changes)
	}
	changed := thingsIn(t, strings.Replace(text, `name: "a007"`, `name: "a007x"`, 1))
	p = plan(t, h.engine(changed, Options{}))
	var touched []string
	for _, ch := range p.Changes {
		if ch.Action != ActionNoop {
			touched = append(touched, ch.Addr)
		}
	}
	sort.Strings(touched)
	if strings.Join(touched, ",") != "a.k007,b.k007" {
		t.Errorf("a change of a.k007 plans %v, want a.k007 and b.k007", touched)
	}
}

// Specifications put together at random from the forms of a collection, of an
// element and of a read. The check is the audit of every graph (see
// graphaudit_test.go): what a task does not read, by the graph, must leave its
// body as it was when it is filled in. The forms include the ones the
// evaluator does not attribute to the element.
func TestLoopsOfRandomShapeAreSound(t *testing.T) {
	elements := []string{
		`#T & {tag: "\(j)"}`,
		`{#T}`,
		`#T`,
		`_tmpl`,
		`_tmpl & {opt: "\(j)"}`,
		`#T & {tag: "\(j)", t: _shared}`,
		`#T & {if j > 0 {content: c0.e0.id}}`,
	}
	containers := []func(i int, elem string) string{
		func(i int, elem string) string {
			return fmt.Sprintf("c%d: {for j in [0, 1, 2] {\"e\\(j)\": %s}}\n", i, elem)
		},
		func(i int, elem string) string {
			var b strings.Builder
			fmt.Fprintf(&b, "c%d: {\n", i)
			for j := range 3 {
				fmt.Fprintf(&b, "\te%d: %s\n", j, strings.ReplaceAll(strings.ReplaceAll(elem, `\(j)`, fmt.Sprint(j)), "j > 0", fmt.Sprint(j > 0)))
			}
			b.WriteString("}\n")
			return b.String()
		},
		func(i int, elem string) string {
			return fmt.Sprintf("c%d: {for j in [0, 1, 2] {\"e\\(j)\": %s}, plain: {id: \"p\"}}\n", i, elem)
		},
		func(i int, elem string) string {
			return fmt.Sprintf("c%d: {for j in [0, 1, 2] {\"e\\(j)\": {x: %s, y: #T & {tag: \"y\\(j)\"}}}}\n", i, elem)
		},
	}
	reads := []string{
		`content: v.id`,
		`content: "\(v.id)-x"`,
		`content: v.sub.x`,
		`content: json.Marshal(v)`,
		`t: v`,
		`$depends_on: [v]`,
		`content: k`,
		`content: v.t.i`,
		`content: v.id, tag: c0.e0.id`,
		`content: v.sub.x, tag: c0.e0.id`,
		`content: strings.Join([for s in v.list {s}], ",")`,
		`let w = v, content: w.id`,
		`_h: v.id, content: _h`,
		`content: c0[k].id`,
		`content: SRC[k].id`,
		`content: SRC[k].id, tag: k`,
		`content: v.x.id`,
		`content: json.Marshal(v), tag: v.y.id`,
		`if v.tag != "1" {content: v.id}`,
		`t: [for kk, vv in c0 {vv.id}]`,
		`t: {for kk, vv in c0 if kk == k {(kk): vv.id}}`,
		`content: (v & {}).id`,
		`content: [v][0].id`,
		`content: {out: v.id}.out`,
		`content: *v.id | "none"`,
	}
	loops := []func(i, src int, read string) string{
		func(i, src int, read string) string {
			return fmt.Sprintf("r%d: {for k, v in c%d {\"\\(k)\": #T & {%s}}}\n", i, src, read)
		},
		func(i, src int, read string) string {
			return fmt.Sprintf("r%d: {for k, v in c%d {\"\\(k)\": {p: #T & {%s}, q: #T & {content: v.id}}}}\n", i, src, read)
		},
		func(i, src int, read string) string {
			return fmt.Sprintf("r%d: {for k, v in c%d for k2, v2 in c0 if k == k2 {\"\\(k)\": #T & {%s, opt: v2.id}}}\n", i, src, read)
		},
		func(i, src int, read string) string {
			return fmt.Sprintf("r%d: [for k, v in c%d {#T & {%s}}]\n", i, src, read)
		},
	}
	// A deterministic sequence, so that a failure names a specification that
	// the next run builds again.
	seed := uint32(20261110)
	next := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % n
	}
	built, byElement := 0, 0
	evaluatorOnly := 0
	// A pull request builds a third of them (-short); main builds them all.
	specs, wantBuilt, wantByElement := 400, 150, 300
	if testing.Short() {
		specs, wantBuilt, wantByElement = 130, 50, 100
	}
	for i := range specs {
		var b strings.Builder
		b.WriteString(loopPrelude)
		b.WriteString("_tmpl: #T & {tag: \"t\"}\n_shared: {i: \"1\"}\n")
		b.WriteString(containers[0](0, elements[0]))
		b.WriteString(containers[next(len(containers))](1, elements[next(len(elements))]))
		b.WriteString(strings.ReplaceAll(loops[next(len(loops))](0, 1, reads[next(len(reads))]), "SRC", "c1"))
		src := next(2)
		b.WriteString(strings.ReplaceAll(loops[next(len(loops))](1, src, reads[next(len(reads))]), "SRC", fmt.Sprintf("c%d", src)))
		text := b.String()
		v := cuecontext.New().CompileString(text)
		if v.Err() != nil || v.Validate() != nil {
			continue // a combination that is not a specification
		}
		h := newHarness(t)
		e := h.engine(thingsIn(t, text), Options{})
		if _, err := e.buildGraph(); err != nil {
			t.Fatalf("specification %d: %v\n%s", i, err, text)
		}
		built++
		if e.loops != nil {
			byElement += e.loops.Narrowed
		}
		// The graph of the syntax alone is held to the same check.
		_, se := edgesOf(t, text, true)
		for _, r := range se.Root.Resources {
			if _, diff := probeIndependence(se, r.Address()); diff != "" {
				t.Errorf("specification %d, from the syntax alone: %s\n%s", i, diff, text)
			}
		}
		for addr, deps := range e.deps {
			if !subset(strings.Join(deps, ","), strings.Join(se.deps[addr], ",")) {
				evaluatorOnly++
			}
		}
	}
	if built < wantBuilt || byElement < wantByElement {
		t.Fatalf("%d specifications built and %d loops read by element: the generator no longer makes what this test is for", built, byElement)
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if len(audit.failures) > 0 {
		t.Fatalf("the audit of the graphs found %d failures; the first:\n%s", len(audit.failures), audit.failures[0])
	}
	t.Logf("%d specifications, %d loops read by element, %d tasks with a read that only the evaluator found", built, byElement, evaluatorOnly)
}
