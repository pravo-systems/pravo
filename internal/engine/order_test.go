package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// orderedSpec has n resources in a container, three that read one of them and one
// that reads the whole container through a comprehension, and an output that does
// the same.
func orderedSpec(n int) (spec, string) {
	var want []string
	resources := []string{"d1", "d2", "d3", "d4"}
	for i := 0; i < n; i++ {
		resources = append(resources, fmt.Sprintf("files.f%d", i))
		want = append(want, fmt.Sprintf("c%d", i))
	}
	cue := fmt.Sprintf(`import (
	"list"
	"strings"
)
files: {for i in list.Range(0, %d, 1) {"f\(i)": {name: "f\(i)", id: string | null, content: "c\(i)"}}}
d1: %s
d2: %s
d3: %s
d4: %s
all: {value: strings.Join([for k, v in files {v.content}], ",")}
`, n,
		thing("d1", `content: "after \(files.f0.id)"`),
		thing("d2", `content: "after \(files.f1.id)"`),
		thing("d3", `content: "after \(files.f2.id)"`),
		thing("d4", `content: strings.Join([for k, v in files {v.content}], ",")`))
	return spec{cue: cue, resources: resources, outputs: []string{"all"}}, strings.Join(want, ",")
}

// A comprehension over a container of resources visits them in the order the
// specification declares them, whatever order the providers finish in. The value
// a plan shows is the value the apply of it writes: an apply that plans a resource
// again with a different value is stale, and one that stops there leaves the
// estate half applied.
func TestContainerIterationFollowsTheDeclarationOrder(t *testing.T) {
	s, want := orderedSpec(12)
	for run := 0; run < 15; run++ {
		h := newHarness(t)
		e := h.engine(s, Options{})
		plan, err := e.Plan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var planned string
		for _, ch := range plan.Changes {
			if ch.Addr == "d4" {
				planned = contentOf(t, ch)
			}
		}
		if planned != want {
			t.Fatalf("run %d: the plan shows d4 content %q, want %q", run, planned, want)
		}
		if got := outputText(t, plan, "all"); got != want {
			t.Fatalf("run %d: the plan shows the output %q, want %q", run, got, want)
		}
		if _, err := e.Apply(context.Background(), plan); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		d4 := h.state.Get("d4")
		if d4 == nil || !strings.Contains(string(d4.Attributes), `"`+want+`"`) {
			t.Fatalf("run %d: the record of d4 is %+v, want content %q", run, d4, want)
		}
	}
}

// contentOf returns the planned content of a change.
func contentOf(t *testing.T, ch *Change) string {
	t.Helper()
	var m map[string]tftypes.Value
	if err := ch.Planned.As(&m); err != nil {
		t.Fatalf("%s: %v", ch.Addr, err)
	}
	var content string
	if err := m["content"].As(&content); err != nil {
		t.Fatalf("%s: content: %v", ch.Addr, err)
	}
	return content
}

// outputText returns the planned value of a string output.
func outputText(t *testing.T, plan *Plan, name string) string {
	t.Helper()
	v, ok := plan.Outputs[name]
	if !ok {
		t.Fatalf("the plan has no output %s", name)
	}
	text, err := v.String()
	if err != nil {
		t.Fatalf("output %s: %v", name, err)
	}
	return text
}
