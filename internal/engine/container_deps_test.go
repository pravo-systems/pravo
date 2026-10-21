package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// depsOfTask returns the sorted addresses that a task waits for.
func depsOfTask(t *testing.T, h *harness, s spec, addr string) string {
	t.Helper()
	nodes, err := h.engine(s, Options{}).buildGraph()
	if err != nil {
		t.Fatalf("buildGraph: %v", err)
	}
	n, ok := nodes[addr]
	if !ok {
		t.Fatalf("no node %s", addr)
	}
	var out []string
	for d := range n.deps {
		out = append(out, d)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// recordedContent returns the content attribute recorded for a resource.
func recordedContent(t *testing.T, h *harness, addr string) string {
	t.Helper()
	sr := h.state.Get(addr)
	if sr == nil {
		t.Fatalf("%s is not in the state", addr)
	}
	var attrs map[string]any
	if err := json.Unmarshal(sr.Attributes, &attrs); err != nil {
		t.Fatal(err)
	}
	s, _ := attrs["content"].(string)
	return s
}

func recordedID(t *testing.T, h *harness, addr string) string {
	t.Helper()
	var attrs map[string]any
	if err := json.Unmarshal(h.state.Get(addr).Attributes, &attrs); err != nil {
		t.Fatal(err)
	}
	s, _ := attrs["id"].(string)
	return s
}

// A comprehension over a container of resources depends on every resource in
// the container. The resource that iterates waits for all of them and reads
// their ids.
func TestComprehensionOverAContainerOfResourcesWaitsForThem(t *testing.T) {
	cases := []struct {
		name string
		cue  string
		want string
	}{
		{
			"struct of resources",
			"import \"strings\"\n" +
				"pets: {a: " + thing("a", "") + ", b: " + thing("b", "") + "}\n" +
				"all: " + thing("all", `content: strings.Join([for k, v in pets {v.id}], ",")`) + "\n",
			"pets.a,pets.b",
		},
		{
			"struct inside a plain field",
			"import \"strings\"\n" +
				"zoo: {label: \"x\", pets: {a: " + thing("a", "") + ", b: " + thing("b", "") + "}}\n" +
				"all: " + thing("all", `content: strings.Join([for k, v in zoo.pets {v.id}], ",")`) + "\n",
			"zoo.pets.a,zoo.pets.b",
		},
		{
			"plain field above the container",
			"import \"strings\"\n" +
				"zoo: {label: \"x\", pets: {a: " + thing("a", "") + ", b: " + thing("b", "") + "}}\n" +
				"all: " + thing("all", `content: strings.Join([for k, v in zoo.pets {"\(k)/\(v.id)"}], ",")`) + "\n",
			"zoo.pets.a,zoo.pets.b",
		},
		{
			"list of resources",
			"import \"strings\"\n" +
				"pets: [" + thing("a", "") + ", " + thing("b", "") + "]\n" +
				"all: " + thing("all", `content: strings.Join([for v in pets {v.id}], ",")`) + "\n",
			"pets[0],pets[1]",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var resources []string
			switch {
			case strings.Contains(c.cue, "zoo.pets"):
				resources = []string{"zoo.pets.a", "zoo.pets.b", "all"}
			case strings.Contains(c.cue, "pets: ["):
				resources = []string{"pets[0]", "pets[1]", "all"}
			default:
				resources = []string{"pets.a", "pets.b", "all"}
			}
			s := spec{cue: c.cue, resources: resources}
			h := newHarness(t)
			if got := depsOfTask(t, h, s, "all"); got != c.want {
				t.Fatalf("all depends on %q, want %q", got, c.want)
			}
		})
	}
}

// The graph runs the iterating resource after the resources it iterates over,
// so that it reads their ids, whatever the order the scheduler picks.
func TestComprehensionOverAContainerAppliesAfterTheContainer(t *testing.T) {
	s := spec{
		cue: "import \"strings\"\n" +
			"pets: {a: " + thing("a", "") + ", b: " + thing("b", "") + ", c: " + thing("c", "") + "}\n" +
			"all: " + thing("all", `content: strings.Join([for k, v in pets {v.id}], ",")`) + "\n" +
			"sorted: " + thing("sorted", `content: strings.Join([for k, v in pets {"\(k)=\(v.id)"}], ",")`) + "\n",
		resources: []string{"pets.a", "pets.b", "pets.c", "all", "sorted"},
	}
	for i := 0; i < 20; i++ {
		h := newHarness(t)
		res := h.apply(s)
		if res.Added != 5 {
			t.Fatalf("added %d, want 5", res.Added)
		}
		a, b, c := recordedID(t, h, "pets.a"), recordedID(t, h, "pets.b"), recordedID(t, h, "pets.c")
		if got, want := recordedContent(t, h, "all"), a+","+b+","+c; got != want {
			t.Fatalf("run %d: all.content = %q, want %q", i, got, want)
		}
		if got, want := recordedContent(t, h, "sorted"), "a="+a+",b="+b+",c="+c; got != want {
			t.Fatalf("run %d: sorted.content = %q, want %q", i, got, want)
		}
		plan, err := h.engine(s, Options{}).Plan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if plan.HasChanges() {
			t.Fatalf("run %d: a second plan found changes", i)
		}
	}
}

// A reference to a container in an expression, not a comprehension, is a
// dependency on every resource in it too.
func TestReferenceToAContainerOfResourcesIsADependency(t *testing.T) {
	s := spec{
		cue: "import \"encoding/json\"\n" +
			"pets: {a: " + thing("a", "") + ", b: " + thing("b", "") + "}\n" +
			"dump: " + thing("dump", `content: json.Marshal(pets)`) + "\n",
		resources: []string{"pets.a", "pets.b", "dump"},
	}
	h := newHarness(t)
	if got := depsOfTask(t, h, s, "dump"); got != "pets.a,pets.b" {
		t.Fatalf("dump depends on %q, want pets.a,pets.b", got)
	}
}

// A comprehension over a plain field that is built from resources waits for
// them, and reads their ids once they exist.
func TestComprehensionOverAPlainFieldBuiltFromResourcesAppliesAfterThem(t *testing.T) {
	s := spec{
		cue: "import \"strings\"\n" +
			"a: " + thing("a", "") + "\nb: " + thing("b", "") + "\n" +
			"ids: {x: a.id, y: b.id}\n" +
			"all: " + thing("all", `content: strings.Join([for k, v in ids {v}], ",")`) + "\n",
		resources: []string{"a", "b", "all"},
	}
	h := newHarness(t)
	if got := depsOfTask(t, h, s, "all"); got != "a,b" {
		t.Fatalf("all depends on %q, want a,b", got)
	}
	for i := 0; i < 10; i++ {
		h := newHarness(t)
		h.apply(s)
		if got, want := recordedContent(t, h, "all"), recordedID(t, h, "a")+","+recordedID(t, h, "b"); got != want {
			t.Fatalf("run %d: all.content = %q, want %q", i, got, want)
		}
	}
}

// An output that reads a container of resources is recorded after the
// resources are, and a destroy of one of them drops it.
func TestOutputOverAContainerOfResources(t *testing.T) {
	s := spec{
		cue: "pets: {a: " + thing("a", "") + ", b: " + thing("b", "") + "}\n" +
			"other: " + thing("other", "") + "\n" +
			"ids: {value: [for k, v in pets {v.id}]}\n" +
			"mine: {value: other.id}\n",
		resources: []string{"pets.a", "pets.b", "other"},
		outputs:   []string{"ids", "mine"},
	}
	h := newHarness(t)
	h.apply(s)
	if got := keys(h.state.Outputs()); got != "ids mine" {
		t.Fatalf("outputs after apply = %s", got)
	}
	_, pf := destroyPlan(t, h, s)
	e := h.engine(s, Options{Refresh: RefreshNone})
	loaded, err := e.LoadPlan(pf)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := loaded.Subset([]string{"pets.b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplySaved(context.Background(), sel); err != nil {
		t.Fatal(err)
	}
	if got := keys(h.state.Outputs()); got != "mine" {
		t.Errorf("outputs after destroying pets.b = %s, want mine", got)
	}
}

// A resource built for one key of a comprehension waits for the entry of that
// key in the container, and for no other.
func TestIndexedEntryOfAContainerIsTheOnlyDependency(t *testing.T) {
	s := spec{
		cue: "zones: [\"a\", \"b\", \"c\"]\n" +
			"ports: {for z in zones {\"\\(z)\": " + thing("p", "") + "}}\n" +
			"deploy: {for z in zones {\"\\(z)\": " + thing("d", `content: "\(ports[z].id)"`) + "}}\n",
		resources: []string{"ports.a", "ports.b", "ports.c", "deploy.a", "deploy.b", "deploy.c"},
	}
	h := newHarness(t)
	for _, z := range []string{"a", "b", "c"} {
		if got := depsOfTask(t, h, s, "deploy."+z); got != "ports."+z {
			t.Errorf("deploy.%s depends on %q, want ports.%s", z, got, z)
		}
	}
}

// layered is a specification of layers of n resources: each resource of a layer
// reads the resource of the same key in the layer below, through the key of the
// comprehension that builds it.
func layered(layers, n int) spec {
	var keys, src strings.Builder
	var resources []string
	for i := 0; i < n; i++ {
		if i > 0 {
			keys.WriteString(", ")
		}
		fmt.Fprintf(&keys, "%q", fmt.Sprintf("k%d", i))
	}
	fmt.Fprintf(&src, "keys: [%s]\n", keys.String())
	for l := 0; l < layers; l++ {
		extra := ""
		if l > 0 {
			extra = fmt.Sprintf(`content: "\(l%d[k].id)"`, l-1)
		}
		fmt.Fprintf(&src, "l%d: {for k in keys {\"\\(k)\": %s}}\n", l, thing("p", extra))
		for i := 0; i < n; i++ {
			resources = append(resources, fmt.Sprintf("l%d.k%d", l, i))
		}
	}
	return spec{cue: src.String(), resources: resources}
}

// The container in l0[k].id is the way to the resource of the key, and not a
// dependency: each resource of a layer waits for one resource of the layer
// below, and the state records 2N edges for three layers of N.
func TestNavigationOperandIsNotFollowed(t *testing.T) {
	const n = 15
	s := layered(3, n)
	h := newHarness(t)
	for _, c := range []struct{ addr, want string }{{"l0.k4", ""}, {"l1.k4", "l0.k4"}, {"l2.k4", "l1.k4"}} {
		if got := depsOfTask(t, h, s, c.addr); got != c.want {
			t.Errorf("%s depends on %q, want %q", c.addr, got, c.want)
		}
	}
	if res := h.apply(s); res.Added != 3*n {
		t.Fatalf("added %d, want %d", res.Added, 3*n)
	}
	if got := len(h.state.Edges()); got != 2*n {
		t.Errorf("the state records %d edges, want %d", got, 2*n)
	}
	plan, err := h.engine(s, Options{}).Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.HasChanges() {
		t.Errorf("a second plan found changes")
	}
}
