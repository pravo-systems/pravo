package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// chainSpec is n resources, each of which reads the id of the one before it
// when after is set, and is independent of the others otherwise.
func chainSpec(n int, after bool) spec {
	var src strings.Builder
	var names []string
	for i := range n {
		name := fmt.Sprintf("r%03d", i)
		names = append(names, name)
		extra := ""
		if after && i > 0 {
			extra = fmt.Sprintf("content: \"after \\(r%03d.id)\"", i-1)
		}
		fmt.Fprintf(&src, "%s: %s\n", name, thing(name, extra))
	}
	return spec{cue: src.String(), resources: names}
}

// Every fill unified into the configuration evaluates the whole of it again,
// so independent resources must not each cost one.
func TestIndependentResourcesAreFilledInOneStep(t *testing.T) {
	s := chainSpec(120, false)
	h := newHarness(t)

	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if e.settles > 1 {
		t.Errorf("planning unified fills %d times, want at most once", e.settles)
	}
	if _, err := e.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if e.settles > 2 {
		t.Errorf("planning and applying unified fills %d times, want at most twice", e.settles)
	}

	e = h.engine(s, Options{})
	plan, err = e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.HasChanges() {
		t.Error("a second plan found changes")
	}
	if e.settles > 1 {
		t.Errorf("a plan without changes unified fills %d times, want at most once", e.settles)
	}
}

// A resource that reads another's computed value runs after that value is part
// of the configuration, however the fills are batched.
func TestDependentReadsTheFilledValueOfItsDependency(t *testing.T) {
	s := chainSpec(12, true)
	h := newHarness(t)
	h.apply(s)
	for i := 1; i < 12; i++ {
		prev := h.state.Get(fmt.Sprintf("r%03d", i-1))
		got := h.state.Get(fmt.Sprintf("r%03d", i))
		if prev == nil || got == nil {
			t.Fatalf("r%03d is not in the state", i)
		}
		if want := fmt.Sprintf("%q", "after "+idFromJSON(t, prev.Attributes)); !strings.Contains(string(got.Attributes), `"content":`+want) {
			t.Errorf("r%03d has %s, want content %s", i, got.Attributes, want)
		}
	}
	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.HasChanges() {
		t.Error("a second plan found changes")
	}
}

func idFromJSON(t *testing.T, attrs []byte) string {
	t.Helper()
	const key = `"id":"`
	s := string(attrs)
	i := strings.Index(s, key)
	if i < 0 {
		t.Fatalf("no id in %s", s)
	}
	s = s[i+len(key):]
	return s[:strings.Index(s, `"`)]
}

// A value the provider computes that conflicts with the specification is
// refused, and the error names the resource it came from even when its fill
// was unified together with those of others.
func TestConflictingFillIsRefusedAndNamesItsResource(t *testing.T) {
	s := spec{
		cue: "a: " + thing("a", "") + "\n" +
			"b: {name: \"b\", id: =~\"^never\"}\n" +
			"c: " + thing("c", "") + "\n",
		resources: []string{"a", "b", "c"},
	}
	h := newHarness(t)
	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("apply accepted a value that conflicts with the specification")
	}
	if !strings.Contains(err.Error(), "b: filling values into the configuration") {
		t.Errorf("error %q does not name the resource b", err)
	}
	if strings.Contains(err.Error(), "a: filling") || strings.Contains(err.Error(), "c: filling") {
		t.Errorf("error %q names a resource that did not conflict", err)
	}
}

// pureChain is n resources where each reads the id of the one before it in a plain
// reference. mixed turns every third link into a value built from the id.
func pureChain(n int, mixed bool) spec {
	var src strings.Builder
	var names []string
	for i := range n {
		name := fmt.Sprintf("r%03d", i)
		names = append(names, name)
		extra := ""
		if i > 0 {
			prev := fmt.Sprintf("r%03d", i-1)
			extra = "content: string & " + prev + ".id"
			if mixed && i%3 == 0 {
				extra = fmt.Sprintf("content: \"after \\(%s.id)\"", prev)
			}
		}
		fmt.Fprintf(&src, "%s: %s\n", name, thing(name, extra))
	}
	return spec{cue: src.String(), resources: names}
}

func chainContent(t *testing.T, h *harness, addr string) string {
	t.Helper()
	r := h.state.Get(addr)
	if r == nil {
		t.Fatalf("%s is not in the state", addr)
	}
	var attrs map[string]any
	if err := json.Unmarshal(r.Attributes, &attrs); err != nil {
		t.Fatal(err)
	}
	s, _ := attrs["content"].(string)
	return s
}

// A chain where each link reads the id of the one before it must not unify the
// whole configuration once per link: that made applying a chain grow with the
// square of its length (400 links took more than 100 s).
func TestChainOfPlainReferencesIsNotUnifiedPerLink(t *testing.T) {
	const n = 60
	s := pureChain(n, false)
	h := newHarness(t)
	e := h.engine(s, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if e.settles > 3 {
		t.Errorf("the chain unified fills %d times, want a few, not one per link", e.settles)
	}
	if e.fastFills < n-1 {
		t.Errorf("%d bodies took their values leaf by leaf, want %d", e.fastFills, n-1)
	}
	for i := 1; i < n; i++ {
		prev := recordID(t, thingRecord(t, h, fmt.Sprintf("r%03d", i-1)))
		if got := chainContent(t, h, fmt.Sprintf("r%03d", i)); got != prev {
			t.Fatalf("r%03d read %q, want the id of r%03d, %q", i, got, i-1, prev)
		}
	}
	again := h.engine(s, Options{})
	p2, err := again.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p2.HasChanges() {
		t.Error("a second plan found changes")
	}

	// A change at the root replaces it, and every link reads the new id.
	changed := strings.Replace(s.cue, `r000: {name: "r000"`, `r000: {name: "root"`, 1)
	h.apply(spec{cue: changed, resources: s.resources})
	for i := 1; i < n; i++ {
		prev := recordID(t, thingRecord(t, h, fmt.Sprintf("r%03d", i-1)))
		if got := chainContent(t, h, fmt.Sprintf("r%03d", i)); got != prev {
			t.Fatalf("after the root changed, r%03d read %q, want %q", i, got, prev)
		}
	}

	// And the whole chain is destroyed.
	d := h.engine(s, Options{Destroy: true})
	dp, err := d.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Apply(context.Background(), dp); err != nil {
		t.Fatal(err)
	}
	for _, name := range s.resources {
		if h.state.Get(name) != nil {
			t.Fatalf("%s is still in the state after the destroy", name)
		}
	}
}

// A link that builds its value from the id of the one before it needs CUE to
// evaluate it, and still reads the right id among links that do not.
func TestChainWithBuiltValuesStillReadsTheRightIDs(t *testing.T) {
	const n = 30
	s := pureChain(n, true)
	h := newHarness(t)
	h.apply(s)
	for i := 1; i < n; i++ {
		prev := recordID(t, thingRecord(t, h, fmt.Sprintf("r%03d", i-1)))
		want := prev
		if i%3 == 0 {
			want = "after " + prev
		}
		if got := chainContent(t, h, fmt.Sprintf("r%03d", i)); got != want {
			t.Fatalf("r%03d read %q, want %q", i, got, want)
		}
	}
}

// A value that conflicts where a link reads it is refused before the link runs,
// and the error names the resource that the value came from.
func TestConflictAtAPlainReferenceNamesTheSource(t *testing.T) {
	for name, s := range map[string]spec{
		"at the reader": {
			cue:       "r000: " + thing("r000", "") + "\nr001: " + thing("r001", `content: string & r000.id & =~"^never"`) + "\n",
			resources: []string{"r000", "r001"},
		},
		"at the source": {
			cue:       "r000: " + thing("r000", `id: =~"^never"`) + "\nr001: " + thing("r001", "content: string & r000.id") + "\n",
			resources: []string{"r000", "r001"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			e := h.engine(s, Options{})
			plan, err := e.Plan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Apply(context.Background(), plan)
			if err == nil {
				t.Fatal("apply accepted a value that conflicts with the specification")
			}
			if !strings.Contains(err.Error(), "r000: filling values into the configuration") {
				t.Errorf("error %q does not name r000", err)
			}
			if h.state.Get("r001") != nil {
				t.Error("r001 was created from a value that conflicts")
			}
		})
	}
}

// Chains whose links build their value from the one before, with an interpolation
// or a condition, take the fast path too.
func TestChainOfBuiltValuesIsNotUnifiedPerLink(t *testing.T) {
	const n = 50
	var cond strings.Builder
	var names []string
	for i := range n {
		name := fmt.Sprintf("r%03d", i)
		names = append(names, name)
		extra := ""
		if i > 0 {
			extra = fmt.Sprintf("if r%03d.name != \"\" {pin: \"set\"}", i-1)
		}
		fmt.Fprintf(&cond, "%s: %s\n", name, thing(name, extra))
	}
	for name, s := range map[string]spec{
		"interpolation": chainSpec(n, true),
		"condition":     {cue: cond.String(), resources: names},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			e := h.engine(s, Options{})
			plan, err := e.Plan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.Apply(context.Background(), plan); err != nil {
				t.Fatal(err)
			}
			if e.settles > 3 {
				t.Errorf("the chain unified fills %d times, want a few, not one per link", e.settles)
			}
			if e.fastFills < n-1 {
				t.Errorf("%d bodies were worked out on their own, want %d", e.fastFills, n-1)
			}
			for i := 1; i < n; i++ {
				addr := fmt.Sprintf("r%03d", i)
				prev := recordID(t, thingRecord(t, h, fmt.Sprintf("r%03d", i-1)))
				r := thingRecord(t, h, addr)
				if name == "interpolation" {
					if got := chainContent(t, h, addr); got != "after "+prev {
						t.Fatalf("%s read %q, want %q", addr, got, "after "+prev)
					}
				} else if !strings.Contains(string(r.Attributes), `"pin":"set"`) {
					t.Fatalf("%s has %s, want pin set", addr, r.Attributes)
				}
			}
			again := h.engine(s, Options{})
			p2, err := again.Plan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if p2.HasChanges() {
				t.Error("a second plan found changes")
			}
		})
	}
}

// A value that something else in the specification refuses is found before any
// task that reads it starts, whether the other thing is a plain field, an output
// or the body of another task.
func TestDependentNeverStartsOnAValueTheSpecificationRefuses(t *testing.T) {
	for name, s := range map[string]spec{
		"a plain field": {
			cue:       "r000: " + thing("r000", "") + "\nr001: " + thing("r001", "content: string & r000.id") + "\ncheck: r000.id & =~\"^never\"\n",
			resources: []string{"r000", "r001"},
		},
		"another task": {
			cue: "r000: " + thing("r000", "") + "\nr001: " + thing("r001", "content: string & r000.id") + "\n" +
				"r002: " + thing("r002", `content: string & r000.id & =~"^never"`) + "\n",
			resources: []string{"r000", "r001", "r002"},
		},
		"another task built from it": {
			cue: "r000: " + thing("r000", "") + "\nr001: " + thing("r001", "content: string & r000.id") + "\n" +
				"r002: " + thing("r002", `content: string & "x\(r000.id)" & =~"^never"`) + "\n",
			resources: []string{"r000", "r001", "r002"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			e := h.engine(s, Options{})
			plan, err := e.Plan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Apply(context.Background(), plan)
			if err == nil {
				t.Fatal("apply accepted a value that the specification refuses")
			}
			if !strings.Contains(err.Error(), "r000: filling values into the configuration") {
				t.Errorf("error %q does not name r000", err)
			}
			for _, a := range []string{"r001", "r002"} {
				if h.state.Get(a) != nil {
					t.Errorf("%s was created from a value that the specification refuses", a)
				}
			}
		})
	}
}

// A parameter that a definition declares inside the body of a resource, and
// that an instance fills with a reference to another resource, is read from
// that resource. It was written as null, and validate said Success.
func TestParameterInsideTheBodyReadsTheReference(t *testing.T) {
	s := spec{
		cue: `base: ` + thing("base", "") + `
#Wrap: {#src: _, name: "a", id: string | null, content: #src}
a: #Wrap & {#src: base.id}
`,
		resources: []string{"base", "a"},
	}
	h := newHarness(t)
	h.apply(s)
	base, got := h.state.Get("base"), h.state.Get("a")
	if base == nil || got == nil {
		t.Fatal("base or a is not in the state")
	}
	want := fmt.Sprintf("%q", idFromJSON(t, base.Attributes))
	if !strings.Contains(string(got.Attributes), `"content":`+want) {
		t.Errorf("a has %s, want content %s", got.Attributes, want)
	}
	plan, err := h.engine(s, Options{}).Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan.HasChanges() {
		t.Error("a second plan found changes")
	}
}
