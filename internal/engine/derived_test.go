package engine

import (
	"context"
	"testing"
)

// A field that reads a sensitive attribute is as secret as the attribute: the plan says which fields of each change
// read one, so that no output of the plan prints them.
func TestPlanMarksTheFieldsThatReadASensitiveAttribute(t *testing.T) {
	h := newHarness(t)
	e := h.engine(spec{resources: []string{"a", "b", "c", "d"}, cue: `
a: {name: "a", id: string | null, pin: "pin-typed-4471"}
b: {name: "b", id: string | null, content: "pin=\(a.pin)"}
c: {name: "c", id: string | null, content: "plain"}
d: {name: "d", id: string | null, content: b.content}
`}, Options{})
	plan, err := e.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	derived := map[string]map[string]bool{}
	for _, ch := range plan.Changes {
		derived[ch.Addr] = ch.Derived
	}
	if !derived["b"]["content"] {
		t.Errorf("b.content reads a.pin and is not marked: %v", derived)
	}
	if !derived["d"]["content"] {
		t.Errorf("d.content reads b.content, which is derived, and is not marked: %v", derived)
	}
	if len(derived["c"]) != 0 {
		t.Errorf("c reads nothing withheld and has %v marked", derived["c"])
	}
	if derived["b"]["name"] {
		t.Errorf("b.name reads nothing and is marked: %v", derived["b"])
	}
}
